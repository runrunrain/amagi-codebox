package envcheck

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// specRunner: arg-aware ProcessRunner double for fix/uninstall regression tests
// ---------------------------------------------------------------------------

// specRunner records every Run call and dispatches responses from a callback
// that inspects the full CommandSpec (path + args), which path-substring
// mocks cannot distinguish (e.g. `npm root -g` vs `npm uninstall -g x`).
type specRunner struct {
	mu      sync.Mutex
	calls   []platform.CommandSpec
	respond func(spec platform.CommandSpec) (*platform.ProcessResult, error)
}

func (r *specRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, spec)
	r.mu.Unlock()
	return r.respond(spec)
}

func (r *specRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, errors.New("specRunner: Start is not supported")
}

func (r *specRunner) sawCall(pathContains string, args ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if pathContains != "" && !strings.Contains(strings.ToLower(call.Path), strings.ToLower(pathContains)) {
			continue
		}
		if len(call.Args) < len(args) {
			continue
		}
		matched := true
		for i := range args {
			if call.Args[i] != args[i] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// guiEnvIsolation points HOME/PATH at an isolated fixture layout that mimics a
// GUI-launched process (minimal PATH, no fnm/npm tooling visible).
func guiEnvIsolation(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	t.Setenv("FNM_DIR", "")
	return home
}

// versionProbeRunner answers `--version`/`-v` probes for the named command
// with the given version output; every other command fails with ErrNotExist.
func versionProbeRunner(command string, versionOutput string) *specRunner {
	return &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		base := filepath.Base(spec.Path)
		if strings.Contains(base, command) && len(spec.Args) > 0 &&
			(spec.Args[0] == "--version" || spec.Args[0] == "-v") {
			return &platform.ProcessResult{Stdout: versionOutput}, nil
		}
		return &platform.ProcessResult{}, os.ErrNotExist
	}}
}

// ---------------------------------------------------------------------------
// RC-1: fix_path must keep the persisted profile and the CURRENT process
// environment consistent. A GUI-launched CodeBox never re-reads .zprofile, so
// without an os.Setenv sync SystemPATHOk stays false forever and the
// path_not_in_system_path issue can never clear ("PATH 修复无效").
// ---------------------------------------------------------------------------

func TestFixPathAppliesDirsToCurrentProcessEnv(t *testing.T) {
	home := guiEnvIsolation(t)
	toolBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(toolBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, toolBin, "pi")
	// macOS TMPDIR is a symlink (/var -> /private/var); collectPathDirs now
	// canonicalizes through EvalSymlinks, so all expectations use the
	// canonical form.
	canonicalToolBin := canonicalDirPath(toolBin)

	runner := versionProbeRunner("pi", "pi 0.81.1")
	svc := NewServiceWithRunner(runner)

	before, err := svc.CheckOne(ToolPi)
	if err != nil {
		t.Fatalf("CheckOne(pi) before fix: %v", err)
	}
	if !before.Installed {
		t.Fatalf("pi should be detected via resolver-controlled ~/.local/bin (GUI env), got %+v", before)
	}
	if before.SystemPATHOk {
		t.Fatal("precondition: minimal GUI PATH must not contain the tool dir")
	}
	if !hasIssueCode(before, "path_not_in_system_path") {
		t.Fatal("precondition: path_not_in_system_path issue expected before fix")
	}

	result, err := svc.RunFixAction(FixActionRequest{Action: SolutionFixPath, Tool: ToolPi})
	if err != nil {
		t.Fatalf("RunFixAction(fix_path) error: %v", err)
	}
	if result == nil || !result.Success || !result.Changed {
		t.Fatalf("fix_path should succeed and change the profile, got %+v (err=%v)", result, err)
	}

	// The fixed directories must now be visible to the CURRENT process env so
	// phase-1 detection (exec.LookPath) agrees with the persisted profile.
	if !pathListContainsDir(os.Getenv("PATH"), canonicalToolBin) {
		t.Fatalf("current process PATH %q does not contain fixed dir %q after fix_path (profile/process env divergence)", os.Getenv("PATH"), canonicalToolBin)
	}

	after, err := svc.CheckOne(ToolPi)
	if err != nil {
		t.Fatalf("CheckOne(pi) after fix: %v", err)
	}
	if !after.SystemPATHOk {
		t.Fatalf("SystemPATHOk should flip to true after the fixed dir entered the process PATH; PATH=%q", os.Getenv("PATH"))
	}
	if hasIssueCode(after, "path_not_in_system_path") {
		t.Fatal("path_not_in_system_path issue must clear after a successful fix_path")
	}
}

func TestFixPathIdempotentKeepsProcessEnvInSyncWithoutDuplicates(t *testing.T) {
	home := guiEnvIsolation(t)
	toolBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(toolBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, toolBin, "pi")
	canonicalToolBin := canonicalDirPath(toolBin)

	runner := versionProbeRunner("pi", "pi 0.81.1")
	svc := NewServiceWithRunner(runner)

	if _, err := svc.RunFixAction(FixActionRequest{Action: SolutionFixPath, Tool: ToolPi}); err != nil {
		t.Fatalf("first fix_path: %v", err)
	}
	// Simulate the still-running session: process PATH keeps the synced dir.
	result, err := svc.RunFixAction(FixActionRequest{Action: SolutionFixPath, Tool: ToolPi})
	if err != nil {
		t.Fatalf("second fix_path: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("second fix_path should report the up-to-date profile as success, got %+v", result)
	}
	if result.Changed {
		t.Fatal("second fix_path must be a no-op on the profile (idempotent)")
	}
	if !pathListContainsDir(os.Getenv("PATH"), canonicalToolBin) {
		t.Fatalf("already-configured branch must still sync the process env; PATH=%q", os.Getenv("PATH"))
	}
	if countDirInPathList(os.Getenv("PATH"), canonicalToolBin) != 1 {
		t.Fatalf("process PATH must not accumulate duplicate entries: %q", os.Getenv("PATH"))
	}
	// The persisted profile itself must hold exactly one export line for the dir.
	profileBytes, readErr := os.ReadFile(filepath.Join(home, ".zprofile"))
	if readErr != nil {
		t.Fatalf("read profile: %v", readErr)
	}
	exportLine := "export PATH='" + canonicalToolBin + "':\"$PATH\""
	if strings.Count(string(profileBytes), exportLine) != 1 {
		t.Fatalf("profile must contain exactly one export line for the fixed dir, got %q", string(profileBytes))
	}
}

// ---------------------------------------------------------------------------
// RC-4: collectPathDirs must not treat resolver diagnostics labels
// ("merged-env" etc. from LaunchDiagnostics.PATHSources) as directories.
// ---------------------------------------------------------------------------

func TestCollectPathDirsIgnoresResolverDiagnosticsLabels(t *testing.T) {
	home := guiEnvIsolation(t)
	toolBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(toolBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, toolBin, "pi")

	// A directory named exactly like the diagnostics label must never be
	// collected, even when it exists relative to the working directory.
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "merged-env"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	svc := NewServiceWithRunner(versionProbeRunner("pi", "pi 0.81.1"))
	dirs := svc.collectPathDirs(FixActionRequest{Action: SolutionFixPath, Tool: ToolPi})

	// Resolver diagnostics labels ("merged-env", "path-search", ...) must never
	// be collected as directories, no matter what exists on disk next to cwd.
	for _, dir := range dirs {
		base := filepath.Base(filepath.Clean(dir))
		if base == "merged-env" || base == "path-search" || base == "inherited" || base == "app-env" {
			t.Fatalf("collectPathDirs collected diagnostics label %q as directory %q; dirs=%v", base, dir, dirs)
		}
	}
	found := false
	for _, dir := range dirs {
		if dir == canonicalDirPath(toolBin) {
			found = true
		}
	}
	if !found {
		t.Fatalf("collectPathDirs should still include the tool dir %q; dirs=%v", toolBin, dirs)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func pathListContainsDir(pathList string, dir string) bool {
	for _, entry := range filepath.SplitList(pathList) {
		if strings.TrimSpace(entry) == dir {
			return true
		}
	}
	return false
}

func countDirInPathList(pathList string, dir string) int {
	count := 0
	for _, entry := range filepath.SplitList(pathList) {
		if strings.TrimSpace(entry) == dir {
			count++
		}
	}
	return count
}
