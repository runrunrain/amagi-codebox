package envcheck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// Shared fixture helpers for uninstall_tool regression tests
// ---------------------------------------------------------------------------

// npmLayoutFixture builds an hermetic npm global layout:
//
//	<prefix>/bin/{npm,opencode,pi,...}          fake shims
//	<prefix>/lib/node_modules/<pkg>/package.json fake package roots
//
// It returns the prefix and the bin dir. PATH is pointed at the bin dir so
// resolver phase-1 (exec.LookPath) finds the shims exactly like a terminal
// session would.
type npmLayoutFixture struct {
	home     string // isolated HOME (no real tooling leaks in)
	prefix   string // npm global prefix
	binDir   string // <prefix>/bin
	rootDir  string // <prefix>/lib/node_modules
	commands []string
}

func newNPMLayoutFixture(t *testing.T, commands ...string) *npmLayoutFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FNM_DIR", "")
	prefix := filepath.Join(home, "npm-global")
	binDir := filepath.Join(prefix, "bin")
	rootDir := filepath.Join(prefix, "lib", "node_modules")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, binDir, "npm")
	for _, command := range commands {
		writeTestExecutable(t, binDir, command)
	}
	f := &npmLayoutFixture{home: home, prefix: prefix, binDir: binDir, rootDir: rootDir, commands: commands}
	t.Setenv("PATH", binDir)
	return f
}

// installFakePackage registers a package root under the global node_modules so
// npm-prefix candidate probing has real files to find. The bin shim is a
// symlink into the package (the real npm layout), which is also how the
// checkers classify the install as npm after resolving symlinks.
func (f *npmLayoutFixture) installFakePackage(t *testing.T, packageName string, commandName string) {
	t.Helper()
	pkgBinDir := filepath.Join(f.rootDir, packageName, "bin")
	if err := os.MkdirAll(pkgBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkgRoot := filepath.Join(f.rootDir, packageName)
	writeTestExecutable(t, pkgBinDir, commandName)
	if err := os.WriteFile(filepath.Join(pkgRoot, "package.json"), []byte(`{"name":"`+packageName+`","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// bin shim mirrors npm's symlink into the package root.
	_ = os.Remove(filepath.Join(f.binDir, commandName))
	if err := os.Symlink(filepath.Join(pkgBinDir, commandName), filepath.Join(f.binDir, commandName)); err != nil {
		t.Fatal(err)
	}
}

// npmResponder answers the npm subprocess probes used by the uninstall chain:
// `--version`, `prefix -g`, `root -g`, and executes `uninstall -g <pkg>` by
// removing the shim + package root from the fixture (simulating npm).
func (f *npmLayoutFixture) npmResponder(tool CLITool, shimName string, packageName string, executeRemoval bool) func(platform.CommandSpec) (*platform.ProcessResult, error) {
	return func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		base := filepath.Base(spec.Path)
		if !strings.Contains(base, "npm") {
			// version probes for the CLI itself run the shim path directly
			if strings.Contains(base, shimName) && len(spec.Args) > 0 && (spec.Args[0] == "--version" || spec.Args[0] == "-v") {
				return &platform.ProcessResult{Stdout: shimName + " 1.0.0"}, nil
			}
			return &platform.ProcessResult{}, os.ErrNotExist
		}
		switch {
		case len(spec.Args) >= 1 && spec.Args[0] == "--version":
			return &platform.ProcessResult{Stdout: "10.0.0"}, nil
		case len(spec.Args) >= 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g":
			return &platform.ProcessResult{Stdout: f.prefix}, nil
		case len(spec.Args) >= 2 && spec.Args[0] == "root" && spec.Args[1] == "-g":
			return &platform.ProcessResult{Stdout: f.rootDir}, nil
		case len(spec.Args) >= 2 && spec.Args[0] == "uninstall" && spec.Args[1] == "-g":
			// 新命令形如: uninstall -g --prefix <prefix> <pkg>；对 pkg 做包含判断。
			sawPkg := false
			for _, arg := range spec.Args {
				if arg == packageName {
					sawPkg = true
				}
			}
			if !sawPkg {
				return &platform.ProcessResult{Stderr: "unexpected package " + strings.Join(spec.Args, " ")}, errors.New("unexpected package")
			}
			if executeRemoval {
				_ = os.Remove(filepath.Join(f.binDir, shimName))
				_ = os.RemoveAll(filepath.Join(f.rootDir, packageName))
			}
			return &platform.ProcessResult{Stdout: "removed " + packageName}, nil
		case len(spec.Args) >= 3 && spec.Args[0] == "view":
			return &platform.ProcessResult{Stdout: "2.0.0"}, nil
		default:
			return &platform.ProcessResult{}, os.ErrNotExist
		}
	}
}

// waitForTerminalOperation polls GetOperationState until the operation reaches
// a terminal status or the timeout expires.
func waitForTerminalOperation(t *testing.T, svc *Service, timeout time.Duration) *OperationState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if op := svc.GetOperationState(); op != nil && op.Status != OperationStatusRunning {
			return op
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("operation did not reach a terminal state within %s", timeout)
	return nil
}

// ---------------------------------------------------------------------------
// RC-2: the four non-Claude agents need a generic, ownership-checked
// uninstall chain reachable via RunFixAction("uninstall_tool").
// ---------------------------------------------------------------------------

func TestRunFixActionUninstallToolRejectsUnsupportedTools(t *testing.T) {
	guiEnvIsolation(t)
	svc := NewServiceWithRunner(&specRunner{respond: func(platform.CommandSpec) (*platform.ProcessResult, error) {
		return &platform.ProcessResult{}, os.ErrNotExist
	}})

	for _, tool := range []CLITool{ToolClaudeCode, ToolHeadroom, CLITool("bogus")} {
		result, err := svc.RunFixAction(FixActionRequest{Action: SolutionUninstallTool, Tool: tool})
		if err == nil && result != nil && result.Success {
			t.Fatalf("uninstall_tool for %s must be rejected (dedicated flows only), got %+v", tool, result)
		}
		if err != nil && result == nil && !strings.Contains(err.Error(), "uninstall") && !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "专用") {
			t.Fatalf("uninstall_tool rejection for %s should carry a clear reason, got err=%v result=%+v", tool, err, result)
		}
	}
}

func TestUninstallToolNPMChannelRemovesAndVerifies(t *testing.T) {
	f := newNPMLayoutFixture(t, "opencode")
	f.installFakePackage(t, "opencode-ai", "opencode")

	runner := &specRunner{respond: f.npmResponder(ToolOpenCode, "opencode", "opencode-ai", true)}
	svc := NewServiceWithRunner(runner)

	before, err := svc.CheckOne(ToolOpenCode)
	if err != nil || !before.Installed {
		t.Fatalf("precondition: opencode must be detected via fixture PATH, status=%+v err=%v", before, err)
	}
	if before.InstallMethod != InstallMethodNPM {
		t.Fatalf("precondition: fixture shim under <prefix>/bin must classify as npm, got %q", before.InstallMethod)
	}

	state, err := svc.startUninstallTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}
	if state.Kind != OperationKindUninstall || state.Tool != ToolOpenCode {
		t.Fatalf("operation state = %+v, want kind=uninstall tool=opencode", state)
	}

	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusSucceeded {
		t.Fatalf("uninstall operation should succeed, got status=%s message=%q error=%q", op.Status, op.Message, op.Error)
	}
	if op.Result == nil || !op.Result.Success {
		t.Fatalf("uninstall result should be successful: %+v", op.Result)
	}
	if !runner.sawCall("npm", "uninstall", "-g", "--prefix", f.prefix, "opencode-ai") {
		t.Fatal("uninstall must run `npm uninstall -g --prefix <verified prefix> opencode-ai` through the resolved npm")
	}
	if !op.CacheRefreshed {
		t.Fatal("successful uninstall must refresh the cached tool status")
	}
	cached := svc.GetCachedStatus().Items[string(ToolOpenCode)]
	if cached.Installed {
		t.Fatalf("cached opencode status must reflect the removal, got %+v", cached)
	}
}

func TestUninstallToolStillDetectedAfterUninstallReportsHonestFailure(t *testing.T) {
	f := newNPMLayoutFixture(t, "pi")
	f.installFakePackage(t, piNPMPackageName, "pi")

	// executeRemoval=false: npm "succeeds" (exit 0) but the files stay —
	// e.g. a second copy or a foreign prefix. The result must NOT claim success.
	runner := &specRunner{respond: f.npmResponder(ToolPi, "pi", piNPMPackageName, false)}
	svc := NewServiceWithRunner(runner)

	if _, err := svc.startUninstallTool(ToolPi); err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}
	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusFailed {
		t.Fatalf("uninstall with surviving installation must report failure, got %+v", op)
	}
	if op.Result != nil && op.Result.Success {
		t.Fatal("result.Success must be false when the tool is still detectable after uninstall")
	}
	if !strings.Contains(op.Result.Message, "复检") && !strings.Contains(op.Result.Message, "仍") {
		t.Fatalf("failure message should honestly describe the surviving installation: %q", op.Result.Message)
	}
}

func TestUninstallToolRefusesForeignNPMPrefix(t *testing.T) {
	home := guiEnvIsolation(t)
	t.Setenv("FNM_DIR", "")
	// The detected pi shim lives under a DIFFERENT node root than the npm we
	// would drive (simulating another node version's install).
	foreignBin := filepath.Join(home, "other-node", "bin")
	if err := os.MkdirAll(foreignBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, foreignBin, "pi")
	// npm itself is reachable from an unrelated prefix.
	npmBin := filepath.Join(home, "npm-global", "bin")
	if err := os.MkdirAll(npmBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, npmBin, "npm")
	t.Setenv("PATH", foreignBin+string(os.PathListSeparator)+npmBin)

	currentRoot := filepath.Join(home, "npm-global", "lib", "node_modules")
	runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		base := filepath.Base(spec.Path)
		switch {
		case strings.Contains(base, "pi") && len(spec.Args) > 0 && (spec.Args[0] == "--version" || spec.Args[0] == "-v"):
			return &platform.ProcessResult{Stdout: "pi 1.0.0"}, nil
		case strings.Contains(base, "npm") && len(spec.Args) >= 1 && spec.Args[0] == "--version":
			return &platform.ProcessResult{Stdout: "10.0.0"}, nil
		case strings.Contains(base, "npm") && len(spec.Args) >= 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g":
			return &platform.ProcessResult{Stdout: filepath.Join(home, "npm-global")}, nil
		case strings.Contains(base, "npm") && len(spec.Args) >= 2 && spec.Args[0] == "root" && spec.Args[1] == "-g":
			return &platform.ProcessResult{Stdout: currentRoot}, nil
		default:
			return &platform.ProcessResult{}, os.ErrNotExist
		}
	}}
	svc := NewServiceWithRunner(runner)

	if _, err := svc.startUninstallTool(ToolPi); err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}
	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusFailed {
		t.Fatalf("foreign-prefix uninstall must fail, got %+v", op)
	}
	if runner.sawCall("npm", "uninstall") {
		t.Fatal("must NOT run npm uninstall against a prefix that does not own the installation")
	}
	if !strings.Contains(op.Result.Message, "npm") {
		t.Fatalf("refusal message should explain the prefix mismatch: %q", op.Result.Message)
	}
	// The foreign installation must survive untouched.
	if _, err := os.Stat(filepath.Join(foreignBin, "pi")); err != nil {
		t.Fatalf("foreign installation must not be deleted: %v", err)
	}
}

func TestUninstallToolRefusesUnknownOwnership(t *testing.T) {
	home := guiEnvIsolation(t)
	t.Setenv("FNM_DIR", "")
	nativeBin := filepath.Join(home, "custom", "bin")
	if err := os.MkdirAll(nativeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, nativeBin, "codex")
	t.Setenv("PATH", nativeBin)

	var sawDestructive bool
	runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		base := filepath.Base(spec.Path)
		if strings.Contains(base, "codex") && len(spec.Args) > 0 && (spec.Args[0] == "--version" || spec.Args[0] == "-V") {
			return &platform.ProcessResult{Stdout: "codex 1.0.0"}, nil
		}
		if len(spec.Args) > 0 && (spec.Args[0] == "uninstall" || spec.Args[0] == "remove") {
			sawDestructive = true
		}
		return &platform.ProcessResult{}, os.ErrNotExist
	}}
	svc := NewServiceWithRunner(runner)

	if _, err := svc.startUninstallTool(ToolCodex); err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}
	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusFailed {
		t.Fatalf("unknown-ownership uninstall must be refused, got %+v", op)
	}
	if sawDestructive {
		t.Fatal("no destructive command may run for unknown ownership")
	}
	if op.Result != nil && op.Result.Success {
		t.Fatal("refusal must not report success")
	}
	if !strings.Contains(op.Result.Message, "手动") && !strings.Contains(op.Result.Message, "拒绝") {
		t.Fatalf("refusal should guide the user to manual cleanup: %q", op.Result.Message)
	}
	// Unknown ownership must leave the file in place.
	if _, err := os.Stat(filepath.Join(nativeBin, "codex")); err != nil {
		t.Fatalf("unknown-ownership installation must not be deleted: %v", err)
	}
}

func TestUninstallToolHomebrewChannel(t *testing.T) {
	home := guiEnvIsolation(t)
	t.Setenv("FNM_DIR", "")
	brewPrefix := filepath.Join(home, "homebrew")
	cellars := filepath.Join(brewPrefix, "Cellar", "omp", "17.2.10", "bin")
	if err := os.MkdirAll(cellars, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, cellars, "omp")
	brewBin := filepath.Join(brewPrefix, "bin")
	if err := os.MkdirAll(brewBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, brewBin, "brew")
	// PATH sees the opt symlink dir (the usual brew layout).
	if err := os.MkdirAll(filepath.Join(brewPrefix, "opt", "omp"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", brewBin+string(os.PathListSeparator)+filepath.Join(brewPrefix, "opt", "omp", "bin"))
	_ = os.Symlink(cellars, filepath.Join(brewPrefix, "opt", "omp", "bin"))
	t.Cleanup(func() { _ = os.Remove(filepath.Join(brewPrefix, "opt", "omp", "bin")) })

	var brewUninstalled bool
	runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		base := filepath.Base(spec.Path)
		switch {
		case strings.Contains(base, "omp") && !strings.Contains(base, "brew") && len(spec.Args) > 0 && (spec.Args[0] == "--version" || spec.Args[0] == "-v"):
			return &platform.ProcessResult{Stdout: "omp 17.2.10"}, nil
		case strings.Contains(base, "brew") && len(spec.Args) >= 1 && spec.Args[0] == "--prefix":
			return &platform.ProcessResult{Stdout: brewPrefix}, nil
		case strings.Contains(base, "brew") && len(spec.Args) >= 2 && spec.Args[0] == "uninstall" && spec.Args[1] == "omp":
			brewUninstalled = true
			_ = os.RemoveAll(filepath.Join(brewPrefix, "Cellar", "omp"))
			_ = os.Remove(filepath.Join(brewPrefix, "opt", "omp", "bin"))
			return &platform.ProcessResult{Stdout: "Uninstalling /opt/.../Cellar/omp/17.2.10"}, nil
		default:
			return &platform.ProcessResult{}, os.ErrNotExist
		}
	}}
	svc := NewServiceWithRunner(runner)

	if _, err := svc.startUninstallTool(ToolOmp); err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}
	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusSucceeded {
		t.Fatalf("homebrew uninstall should succeed, got status=%s message=%q error=%q", op.Status, op.Message, op.Error)
	}
	if !brewUninstalled {
		t.Fatal("uninstall must run `brew uninstall omp` for a Cellar-owned installation")
	}
}

func TestUninstallToolNotInstalledIsNoop(t *testing.T) {
	guiEnvIsolation(t)
	svc := NewServiceWithRunner(&specRunner{respond: func(platform.CommandSpec) (*platform.ProcessResult, error) {
		return &platform.ProcessResult{}, os.ErrNotExist
	}})

	if _, err := svc.startUninstallTool(ToolOpenCode); err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}
	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusSucceeded {
		t.Fatalf("not-installed uninstall should be a successful no-op, got %+v", op)
	}
	if op.Result == nil || !strings.Contains(op.Result.Message, "无需卸载") {
		t.Fatalf("no-op result should say nothing to uninstall: %+v", op.Result)
	}
}

func TestRunFixActionUninstallToolAcceptedAndTracked(t *testing.T) {
	f := newNPMLayoutFixture(t, "opencode")
	f.installFakePackage(t, "opencode-ai", "opencode")

	runner := &specRunner{respond: f.npmResponder(ToolOpenCode, "opencode", "opencode-ai", true)}
	svc := NewServiceWithRunner(runner)

	result, err := svc.RunFixAction(FixActionRequest{Action: SolutionUninstallTool, Tool: ToolOpenCode})
	if err != nil {
		t.Fatalf("RunFixAction(uninstall_tool): %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("uninstall_tool should be accepted (async), got %+v err=%v", result, err)
	}

	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Kind != OperationKindUninstall {
		t.Fatalf("snapshot operation kind must be uninstall, got %q", op.Kind)
	}
	if op.Status != OperationStatusSucceeded {
		t.Fatalf("operation should reach succeeded, got %s (%q / %q)", op.Status, op.Message, op.Error)
	}
}

func TestUninstallToolBusyGateSharesOperationSlot(t *testing.T) {
	f := newNPMLayoutFixture(t, "opencode")
	f.installFakePackage(t, "opencode-ai", "opencode")

	release := make(chan struct{})
	runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		base := filepath.Base(spec.Path)
		if strings.Contains(base, "npm") && len(spec.Args) >= 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g" {
			return &platform.ProcessResult{Stdout: f.prefix}, nil
		}
		if strings.Contains(base, "npm") && len(spec.Args) >= 2 && spec.Args[0] == "root" && spec.Args[1] == "-g" {
			return &platform.ProcessResult{Stdout: f.rootDir}, nil
		}
		if strings.Contains(base, "npm") && len(spec.Args) >= 1 && spec.Args[0] == "--version" {
			return &platform.ProcessResult{Stdout: "10.0.0"}, nil
		}
		if strings.Contains(base, "opencode") && len(spec.Args) > 0 && (spec.Args[0] == "--version" || spec.Args[0] == "-v") {
			return &platform.ProcessResult{Stdout: "opencode 1.0.0"}, nil
		}
		if len(spec.Args) >= 1 && spec.Args[0] == "uninstall" {
			<-release
			_ = os.Remove(filepath.Join(f.binDir, "opencode"))
			_ = os.RemoveAll(filepath.Join(f.rootDir, "opencode-ai"))
			return &platform.ProcessResult{Stdout: "removed"}, nil
		}
		return &platform.ProcessResult{}, os.ErrNotExist
	}}
	svc := NewServiceWithRunner(runner)

	if _, err := svc.startUninstallTool(ToolOpenCode); err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}

	// While the uninstall runs, install/update/uninstall must be rejected with
	// ErrBusy (shared serialized operation slot).
	if _, err := svc.StartInstallTool(ToolPi); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent install should be busy-gated, got %v", err)
	}
	if _, err := svc.startUninstallTool(ToolPi); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent uninstall should be busy-gated, got %v", err)
	}
	close(release)
	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusSucceeded {
		t.Fatalf("uninstall should finish after release, got %+v", op)
	}
}

// context guard: keep the import used even when helpers change shape.
var _ = context.Background

// ---------------------------------------------------------------------------
// Integration finding #1: the Wails binding surface must not grow. Service is
// Wails-bound, so no new exported Service methods may appear for uninstall.
// ---------------------------------------------------------------------------

func TestUninstallToolAddsNoExportedServiceMethods(t *testing.T) {
	svcType := reflect.TypeOf((*Service)(nil))
	for _, method := range []string{"StartUninstallTool", "CanUninstallTool", "RunUninstallTool"} {
		if _, ok := svcType.MethodByName(method); ok {
			t.Fatalf("Service must not export %s: Service is Wails-bound and new exported methods leak into the binding surface", method)
		}
	}
	// The private entry must exist instead (reachable via RunFixAction).
	// reflect cannot see unexported methods, so existence is proven by this
	// compile-time reference; if the method disappears the build breaks.
	var entry func(*Service, CLITool) (*OperationState, error) = (*Service).startUninstallTool
	if entry == nil {
		t.Fatal("unreachable")
	}
}

// ---------------------------------------------------------------------------
// Integration finding #2 (npm): a sibling package under the SAME npm root
// must not pass ownership. The shim resolving into another package's dir is
// grounds for refusal with ZERO uninstall commands executed.
// ---------------------------------------------------------------------------

func TestUninstallToolRefusesSiblingPackageUnderSamePrefix(t *testing.T) {
	f := newNPMLayoutFixture(t, "pi")
	// The expected pi package is NOT installed; a DIFFERENT package owns the bin
	// shim (resolves into <root>/other-package/bin/pi).
	otherPkgBin := filepath.Join(f.rootDir, "some-other-tool", "bin")
	if err := os.MkdirAll(otherPkgBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, otherPkgBin, "pi")
	_ = os.Remove(filepath.Join(f.binDir, "pi"))
	if err := os.Symlink(filepath.Join(otherPkgBin, "pi"), filepath.Join(f.binDir, "pi")); err != nil {
		t.Fatal(err)
	}

	var uninstallCalls int
	runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		base := filepath.Base(spec.Path)
		switch {
		case strings.Contains(base, "pi") && !strings.Contains(base, "npm") && len(spec.Args) > 0 && (spec.Args[0] == "--version" || spec.Args[0] == "-v"):
			return &platform.ProcessResult{Stdout: "pi 1.0.0"}, nil
		case strings.Contains(base, "npm") && len(spec.Args) >= 1 && spec.Args[0] == "--version":
			return &platform.ProcessResult{Stdout: "10.0.0"}, nil
		case strings.Contains(base, "npm") && len(spec.Args) >= 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g":
			return &platform.ProcessResult{Stdout: f.prefix}, nil
		case strings.Contains(base, "npm") && len(spec.Args) >= 2 && spec.Args[0] == "root" && spec.Args[1] == "-g":
			return &platform.ProcessResult{Stdout: f.rootDir}, nil
		case strings.Contains(base, "npm") && len(spec.Args) >= 1 && spec.Args[0] == "uninstall":
			uninstallCalls++
			return &platform.ProcessResult{}, errors.New("must not be called")
		default:
			return &platform.ProcessResult{}, os.ErrNotExist
		}
	}}
	svc := NewServiceWithRunner(runner)

	if _, err := svc.startUninstallTool(ToolPi); err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}
	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusFailed {
		t.Fatalf("sibling-package uninstall must be refused, got %+v", op)
	}
	if uninstallCalls != 0 {
		t.Fatalf("no npm uninstall may run for a sibling-package installation, saw %d calls", uninstallCalls)
	}
	if !strings.Contains(op.Result.Message, "预期包") {
		t.Fatalf("refusal message should name the expected package: %q", op.Result.Message)
	}
	// The sibling installation must survive untouched.
	if _, err := os.Stat(filepath.Join(otherPkgBin, "pi")); err != nil {
		t.Fatalf("sibling package files must not be deleted: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Integration finding #2 (Homebrew): a Cellar path under the brew prefix does
// NOT prove tool ownership. A shared dependency keg (node/python/…) or an
// unmapped tool must be refused with ZERO brew uninstall calls.
// ---------------------------------------------------------------------------

func TestUninstallToolHomebrewRejectsSharedDependencyKeg(t *testing.T) {
	home := guiEnvIsolation(t)
	t.Setenv("FNM_DIR", "")
	brewPrefix := filepath.Join(home, "homebrew")
	// pi binary living inside the shared `node` keg — classic shared dependency.
	nodeKegBin := filepath.Join(brewPrefix, "Cellar", "node", "22.1.0", "bin")
	if err := os.MkdirAll(nodeKegBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, nodeKegBin, "pi")
	brewBin := filepath.Join(brewPrefix, "bin")
	if err := os.MkdirAll(brewBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, brewBin, "brew")
	t.Setenv("PATH", brewBin+string(os.PathListSeparator)+nodeKegBin)

	var uninstallRan bool
	runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		base := filepath.Base(spec.Path)
		switch {
		case strings.Contains(base, "pi") && !strings.Contains(base, "brew") && len(spec.Args) > 0 && (spec.Args[0] == "--version" || spec.Args[0] == "-v"):
			return &platform.ProcessResult{Stdout: "pi 1.0.0"}, nil
		case strings.Contains(base, "brew") && len(spec.Args) >= 1 && spec.Args[0] == "--prefix":
			return &platform.ProcessResult{Stdout: brewPrefix}, nil
		case strings.Contains(base, "brew") && len(spec.Args) >= 1 && spec.Args[0] == "uninstall":
			uninstallRan = true
			return &platform.ProcessResult{}, errors.New("must not be called")
		default:
			return &platform.ProcessResult{}, os.ErrNotExist
		}
	}}
	svc := NewServiceWithRunner(runner)

	if _, err := svc.startUninstallTool(ToolPi); err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}
	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusFailed {
		t.Fatalf("shared-dependency keg uninstall must be refused, got %+v", op)
	}
	if uninstallRan {
		t.Fatal("no brew uninstall may run for a shared dependency keg")
	}
	if !strings.Contains(op.Result.Message, "共享依赖") && !strings.Contains(op.Result.Message, "已知") {
		t.Fatalf("refusal message should explain the unmapped formula: %q", op.Result.Message)
	}
	if _, err := os.Stat(filepath.Join(nodeKegBin, "pi")); err != nil {
		t.Fatalf("shared dependency files must not be deleted: %v", err)
	}
}

// TestUninstallToolHomebrewRejectsUnmappedFormulaForTool: an allowlisted
// formula for a DIFFERENT tool (opencode keg) must not be uninstallable as
// codex.
func TestUninstallToolHomebrewRejectsUnmappedFormulaForTool(t *testing.T) {
	home := guiEnvIsolation(t)
	t.Setenv("FNM_DIR", "")
	brewPrefix := filepath.Join(home, "homebrew")
	opencodeKegBin := filepath.Join(brewPrefix, "Cellar", "opencode", "1.2.3", "bin")
	if err := os.MkdirAll(opencodeKegBin, 0o755); err != nil {
		t.Fatal(err)
	}
	// codex binary shadowed inside the opencode keg — mapped formula, wrong tool.
	writeTestExecutable(t, opencodeKegBin, "codex")
	brewBin := filepath.Join(brewPrefix, "bin")
	if err := os.MkdirAll(brewBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, brewBin, "brew")
	t.Setenv("PATH", brewBin+string(os.PathListSeparator)+opencodeKegBin)

	var uninstallRan bool
	runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		base := filepath.Base(spec.Path)
		switch {
		case strings.Contains(base, "codex") && !strings.Contains(base, "brew") && len(spec.Args) > 0 && (spec.Args[0] == "--version" || spec.Args[0] == "-V"):
			return &platform.ProcessResult{Stdout: "codex 1.0.0"}, nil
		case strings.Contains(base, "brew") && len(spec.Args) >= 1 && spec.Args[0] == "--prefix":
			return &platform.ProcessResult{Stdout: brewPrefix}, nil
		case strings.Contains(base, "brew") && len(spec.Args) >= 1 && spec.Args[0] == "uninstall":
			uninstallRan = true
			return &platform.ProcessResult{}, errors.New("must not be called")
		default:
			return &platform.ProcessResult{}, os.ErrNotExist
		}
	}}
	svc := NewServiceWithRunner(runner)

	if _, err := svc.startUninstallTool(ToolCodex); err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}
	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusFailed {
		t.Fatalf("cross-tool formula uninstall must be refused, got %+v", op)
	}
	if uninstallRan {
		t.Fatal("no brew uninstall may run for a cross-tool keg")
	}
}
