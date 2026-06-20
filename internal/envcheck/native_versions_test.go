package envcheck

import (
	"bytes"
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

// This test file covers the "versions/ as native truth source" model
// introduced for the npm/native detection fix. Each test focuses on one
// concrete acceptance criterion called out in the task contract:
//   - versions/ scanning finds healthy binaries and sorts newest-first
//   - candidate priority: versions/ > shim > npm
//   - R1 PATH injection no longer depends on the shim when versions/ exists
//   - 5.11.B install verification falls back to versions/ when shim is missing
//   - 5.11.F install completion removes npm residue
//   - 5.11.J npm shim lookup retries while npm flushes the bin shim
//   - R6 npm-alongside-native info issue
//   - R7 Native uninstall reports surviving versions/ binaries

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// withClaudeUserHome redirects the package-level home resolver used by both
// checker_claude.go and native_versions.go, so versions/ scans target a
// per-test temp directory.
func withClaudeUserHome(t *testing.T, home string) {
	t.Helper()
	previous := claudeUserHomeDir
	claudeUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { claudeUserHomeDir = previous })
}

// withXDGDataHome overrides $XDG_DATA_HOME for POSIX versions/ layout tests.
func withXDGDataHome(t *testing.T, dir string) {
	t.Helper()
	previous := os.Getenv("XDG_DATA_HOME")
	os.Setenv("XDG_DATA_HOME", dir)
	t.Cleanup(func() { os.Setenv("XDG_DATA_HOME", previous) })
}

// writeHealthyNativeVersion writes a "healthy" claude binary (above the
// threshold enforced by withIntegrityThreshold) under
// <home>/.local/share/claude/versions/<version>/claude and returns its path.
func writeHealthyNativeVersion(t *testing.T, home, version string) string {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "claude", "versions", version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir versions/%s: %v", version, err)
	}
	binary := filepath.Join(dir, "claude")
	// 5 MB healthy binary. withIntegrityThreshold lowers the bar to 1 byte.
	if err := os.WriteFile(binary, bytes.Repeat([]byte{0xAA}, 5*1024*1024), 0o755); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	return binary
}

// fakeHealthyVersionsRunner is a ProcessRunner that returns a fixed version
// string when called against the versions/ binary path and a SIGKILL-style
// error when called against anything else. This lets the install-verification
// test exercise the "shim broken / versions/ good" scenario end to end.
type fakeHealthyVersionsRunner struct {
	mu              sync.Mutex
	calls           []platform.CommandSpec
	healthyPath     string
	healthyVersion  string
	prefix          string
	prefixAvailable bool
}

func (r *fakeHealthyVersionsRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, spec)

	// npm prefix -g probe: returns configured prefix or an error.
	if len(spec.Args) == 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g" {
		if !r.prefixAvailable {
			return &platform.ProcessResult{Stderr: "npm prefix -g: command not found"}, errors.New("npm prefix -g: command not found")
		}
		return &platform.ProcessResult{Stdout: r.prefix}, nil
	}
	// claude --version against the versions/ binary: success. We compare on
	// the resolved real path because checkClaudeCode / verifyClaudeNative-*
	// pipe invocationPath through resolveRealExecutablePath (EvalSymlinks)
	// before invoking claudeVersion.
	if r.healthyPath != "" {
		realSpecPath := resolveRealExecutablePath(spec.Path)
		realHealthy := resolveRealExecutablePath(r.healthyPath)
		if realSpecPath == realHealthy || spec.Path == r.healthyPath {
			return &platform.ProcessResult{Stdout: r.healthyVersion}, nil
		}
	}
	// claude --version against anything else: emulate AMFI SIGKILL so
	// classifyClaudeVersionError treats it as corruption.
	return &platform.ProcessResult{Stderr: "signal: killed"}, errors.New("signal: killed")
}

func (r *fakeHealthyVersionsRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// versions/ scanning & priority
// ---------------------------------------------------------------------------

func TestScanClaudeNativeVersionsDir_SortsNewestFirstAndSkipsCorrupted(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	home := t.TempDir()
	withClaudeUserHome(t, home)

	// Healthy binaries (5MB each). Threshold set so they pass.
	withIntegrityThreshold(t, 1 << 20) // 1MB
	writeHealthyNativeVersion(t, home, "2.1.150")
	writeHealthyNativeVersion(t, home, "2.1.181")
	writeHealthyNativeVersion(t, home, "2.1.143")

	// Add a corrupted entry that should be skipped (1-byte shard). Threshold
	// stays at 1MB so the 1-byte entry is rejected while the 5MB ones pass.
	corruptDir := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.99")
	if err := os.MkdirAll(corruptDir, 0o755); err != nil {
		t.Fatalf("mkdir corrupt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "claude"), []byte("x"), 0o755); err != nil {
		t.Fatalf("write shard: %v", err)
	}

	entries, err := scanClaudeNativeVersionsDir()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 healthy entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Version != "2.1.181" {
		t.Fatalf("newest first: got %s, want 2.1.181", entries[0].Version)
	}
	if entries[1].Version != "2.1.150" {
		t.Fatalf("second: got %s, want 2.1.150", entries[1].Version)
	}
	if entries[2].Version != "2.1.143" {
		t.Fatalf("third: got %s, want 2.1.143", entries[2].Version)
	}
	// Ensure the corrupted entry was skipped.
	for _, e := range entries {
		if e.Version == "2.1.99" {
			t.Fatalf("corrupted 2.1.99 entry should have been skipped")
		}
	}
}

func TestScanClaudeNativeVersionsDir_MissingDirIsNotAnError(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	entries, err := scanClaudeNativeVersionsDir()
	if err != nil {
		t.Fatalf("scan missing dir: %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil entries, got %+v", entries)
	}
}

func TestCompareClaudeNativeVersions_NumericSegmentOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int // >0 means a newer than b
	}{
		{"2.1.181", "2.1.150", 31},
		{"2.1.9", "2.1.10", -1},
		{"2.1.181", "2.1.181", 0},
		{"2.2.0", "2.1.99", 1},
		{"2.1.0", "2.1.0.5", -5}, // longer version with extra segment is newer when shared segments equal
	}
	for _, c := range cases {
		got := compareClaudeNativeVersions(c.a, c.b)
		// Normalize to sign so the assertion is robust against magnitude.
		sign := 0
		if got > 0 {
			sign = 1
		} else if got < 0 {
			sign = -1
		}
		wantSign := 0
		if c.want > 0 {
			wantSign = 1
		} else if c.want < 0 {
			wantSign = -1
		}
		if sign != wantSign {
			t.Errorf("compare(%q, %q) sign = %d, want %d", c.a, c.b, sign, wantSign)
		}
	}
}

func TestParseClaudeNativeVersionName_RejectsNonVersionNames(t *testing.T) {
	good := []string{"2.1.181", "v2.1.181", "3.0.0", "2.1.0.5"}
	for _, name := range good {
		if parseClaudeNativeVersionName(name) == "" {
			t.Errorf("expected %q to parse, got empty", name)
		}
	}
	bad := []string{"latest", "tmp", "downloads", "v", "", "x.y.z", "2.x.1"}
	for _, name := range bad {
		if parseClaudeNativeVersionName(name) != "" {
			t.Errorf("expected %q to be rejected, got %q", name, parseClaudeNativeVersionName(name))
		}
	}
}

func TestFirstHealthyClaudeNativeVersion_ReturnsNewest(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	writeHealthyNativeVersion(t, home, "2.1.143")
	writeHealthyNativeVersion(t, home, "2.1.181")
	writeHealthyNativeVersion(t, home, "2.1.150")

	got := firstHealthyClaudeNativeVersion()
	want := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.181", "claude")
	if got != want {
		t.Fatalf("firstHealthyClaudeNativeVersion = %q, want %q", got, want)
	}
}

func TestIsClaudeNativeVersionsBinaryPath(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	home := t.TempDir()
	withClaudeUserHome(t, home)

	inside := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.181", "claude")
	if !isClaudeNativeVersionsBinaryPath(inside) {
		t.Fatalf("expected %q to be recognized as versions/ binary", inside)
	}

	outside := filepath.Join(home, ".local", "bin", "claude")
	if isClaudeNativeVersionsBinaryPath(outside) {
		t.Fatalf("shim path %q must not be recognized as versions/ binary", outside)
	}

	npm := filepath.Join(home, ".local", "node", "bin", "claude")
	if isClaudeNativeVersionsBinaryPath(npm) {
		t.Fatalf("npm path %q must not be recognized as versions/ binary", npm)
	}
}

// ---------------------------------------------------------------------------
// R6: npm-alongside-native hint
// ---------------------------------------------------------------------------

func TestBuildClaudeNativeAvailableAlongsideNPMHint_NoVersionsReturnsNil(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	if got := buildClaudeNativeAvailableAlongsideNPMHint(filepath.Join(home, "npm", "claude")); got != nil {
		t.Fatalf("expected nil when no versions/ binary exists, got %+v", got)
	}
}

func TestBuildClaudeNativeAvailableAlongsideNPMHint_SurfacesIssueWhenNativeExists(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	nativePath := writeHealthyNativeVersion(t, home, "2.1.181")
	activeNPM := filepath.Join(home, ".local", "node", "bin", "claude")

	issue := buildClaudeNativeAvailableAlongsideNPMHint(activeNPM)
	if issue == nil {
		t.Fatalf("expected non-nil hint when native exists and active is npm")
	}
	if issue.Code != "claude_native_available_alongside_npm" {
		t.Fatalf("code = %q", issue.Code)
	}
	if issue.Severity != SeverityInfo {
		t.Fatalf("severity = %v, want info", issue.Severity)
	}
	if !strings.Contains(issue.Detail, nativePath) {
		t.Fatalf("detail should mention native path %q, got %q", nativePath, issue.Detail)
	}
	if len(issue.Solutions) == 0 {
		t.Fatalf("expected at least one solution")
	}
}

func TestBuildClaudeNativeAvailableAlongsideNPMHint_NoHintWhenAlreadyOnVersions(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	nativePath := writeHealthyNativeVersion(t, home, "2.1.181")
	if got := buildClaudeNativeAvailableAlongsideNPMHint(nativePath); got != nil {
		t.Fatalf("expected nil when active == versions/ binary, got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// R1: PATH injection no longer shim-dependent
// ---------------------------------------------------------------------------

func TestClaudeNativeBinDirectoriesForPATH_IncludesLocalBinWhenVersionsExists(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	writeHealthyNativeVersion(t, home, "2.1.181")

	// Even when the shim does NOT exist, the versions/ truth source should
	// trigger PATH injection. R1 chicken-and-egg: must NOT require shim.
	dirs := claudeNativeBinDirectoriesForPATH()
	want := filepath.Join(home, ".local", "bin")
	found := false
	for _, d := range dirs {
		if d == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q in injected dirs without shim, got %v", want, dirs)
	}
}

func TestClaudeNativeBinDirectoriesForPATH_EmptyWhenNoVersions(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	if got := claudeNativeBinDirectoriesForPATH(); len(got) != 0 {
		t.Fatalf("expected no dirs when versions/ absent, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// R7: Native uninstall reports surviving versions/ binaries
// ---------------------------------------------------------------------------

func TestListClaudeNativeVersionsPathsForUninstallReport(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	writeHealthyNativeVersion(t, home, "2.1.150")
	writeHealthyNativeVersion(t, home, "2.1.181")

	got := listClaudeNativeVersionsPathsForUninstallReport()
	if len(got) != 2 {
		t.Fatalf("expected 2 surviving entries, got %d: %v", len(got), got)
	}
}

// ---------------------------------------------------------------------------
// 5.11.B: verifyClaudeNativeAvailableWithHint falls back to versions/
// ---------------------------------------------------------------------------

func TestVerifyClaudeNativeAvailableWithHint_FallsBackToVersionsTruth(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	nativePath := writeHealthyNativeVersion(t, home, "2.1.181")
	runner := &fakeHealthyVersionsRunner{
		healthyPath:     nativePath,
		healthyVersion:  "2.1.181 (Claude Code)",
		prefix:          filepath.Join(home, "npm-global"),
		prefixAvailable: false, // CheckOne will not see npm prefix; forces versions/ path
	}
	svc := NewServiceWithRunner(runner)

	status, err := svc.verifyClaudeNativeAvailableWithHint("")
	if err != nil {
		t.Fatalf("verifyClaudeNativeAvailableWithHint: %v", err)
	}
	if !status.Installed {
		t.Fatalf("expected Installed=true")
	}
	if status.InstallMethod != InstallMethodNative {
		t.Fatalf("install method = %v, want Native", status.InstallMethod)
	}
	if status.Version != "2.1.181" {
		t.Fatalf("version = %q, want 2.1.181", status.Version)
	}
	if status.PathSource != "native-versions-truth" {
		t.Fatalf("pathSource = %q, want native-versions-truth", status.PathSource)
	}
}

// ---------------------------------------------------------------------------
// 5.11.J: resolveClaudeNPMBootstrapPath retries after npm fsync
// ---------------------------------------------------------------------------

func TestClaudeBootstrapShimRetryAttempts_DefaultAndOverride(t *testing.T) {
	if got := claudeBootstrapShimRetryAttempts(); got != 3 {
		t.Fatalf("default attempts = %d, want 3", got)
	}
	previous := claudeBootstrapShimMaxAttempts
	claudeBootstrapShimMaxAttempts = 0
	t.Cleanup(func() { claudeBootstrapShimMaxAttempts = previous })
	if got := claudeBootstrapShimRetryAttempts(); got != 1 {
		t.Fatalf("zero attempts fallback = %d, want 1", got)
	}
}

func TestResolveClaudeNPMBootstrapPath_RetriesUntilShimAppears(t *testing.T) {
	// Strategy: point the npm global prefix at a temp directory; the shim
	// appears after the second attempt. resolveClaudeNPMBootstrapPath must
	// keep retrying (5.11.J) and eventually return the path.
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	prefix := filepath.Join(home, "npm-global")
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}

	// Use a runner that writes the shim on the SECOND npm prefix -g call so
	// the first scan returns empty.
	runner := &shimDelayedRunner{
		prefix:      prefix,
		createAfter: 1,
		shimBody:    []byte("#!/bin/sh\nexit 0\n"),
	}
	svc := NewServiceWithRunner(runner)

	// Speed up the retry loop for the test.
	previousDelay := claudeBootstrapShimRetryDelay
	claudeBootstrapShimRetryDelay = 0
	t.Cleanup(func() { claudeBootstrapShimRetryDelay = previousDelay })

	got, _, err := svc.resolveClaudeNPMBootstrapPath()
	if err != nil {
		t.Fatalf("resolveClaudeNPMBootstrapPath: %v", err)
	}
	want := filepath.Join(binDir, "claude")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// shimDelayedRunner is a ProcessRunner that emulates npm flushing the bin
// shim after `createAfter` calls to `npm prefix -g`. Before that, the shim
// does not exist on disk (5.11.J race).
type shimDelayedRunner struct {
	mu          sync.Mutex
	prefix      string
	createAfter int
	calls       int
	shimBody    []byte
	shimWritten bool
}

func (r *shimDelayedRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(spec.Args) == 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g" {
		r.calls++
		if r.calls > r.createAfter && !r.shimWritten {
			shim := filepath.Join(r.prefix, "bin", "claude")
			if err := os.WriteFile(shim, r.shimBody, 0o755); err == nil {
				r.shimWritten = true
			}
		}
		return &platform.ProcessResult{Stdout: r.prefix}, nil
	}
	return &platform.ProcessResult{}, nil
}

func (r *shimDelayedRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// 5.11.F: install completion cleans up npm bin residue
// ---------------------------------------------------------------------------

func TestCleanupNPMBinResidueAfterNativeInstall_RemovesNPMPackageDir(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	prefix := filepath.Join(home, "npm-global")
	scopedDir := filepath.Join(prefix, "node_modules", "@anthropic-ai")
	packageDir := filepath.Join(scopedDir, "claude-code")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatalf("mkdir package dir: %v", err)
	}
	// Marker file so we can prove the dir was removed.
	marker := filepath.Join(packageDir, "package.json")
	if err := os.WriteFile(marker, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	// runner returns prefix; this is the only command cleanClaudeNPMResidue
	// runs against the ProcessRunner.
	runner := &fakeHealthyVersionsRunner{
		prefix:          prefix,
		prefixAvailable: true,
	}
	svc := NewServiceWithRunner(runner)

	var captured []string
	reporter := progressReporter(func(_ OperationStep, message string, _ int) {
		captured = append(captured, message)
	})

	note := svc.cleanupNPMBinResidueAfterNativeInstall(reporter)
	if note == "" {
		t.Fatalf("expected non-empty cleanup note")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("npm package marker still exists after cleanup: %v", err)
	}
	if len(captured) == 0 {
		t.Fatalf("expected progress messages, got none")
	}
}

// ---------------------------------------------------------------------------
// Detection end-to-end: versions/ wins even when shim is absent
// ---------------------------------------------------------------------------

func TestCheckClaudeCode_PrefersVersionsOverShimAbsence(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	home := t.TempDir()
	withClaudeUserHome(t, home)

	nativePath := writeHealthyNativeVersion(t, home, "2.1.181")
	// Deliberately do NOT create the ~/.local/bin/claude shim -- this used
	// to be the failure mode where CodeBox saw no Native install.

	runner := &fakeHealthyVersionsRunner{
		healthyPath:    nativePath,
		healthyVersion: "2.1.181 (Claude Code)",
		// PATH/lookups not relevant since we do not call resolveExecutable
		// with versions/ path; checkClaudeCode's firstHealthyClaudeNativeVersion
		// finds the binary directly.
		prefix:          filepath.Join(home, "npm-global"),
		prefixAvailable: false,
	}
	svc := NewServiceWithRunner(runner)

	status, err := svc.checkClaudeCode()
	if err != nil {
		t.Fatalf("checkClaudeCode: %v", err)
	}
	if !status.Installed {
		t.Fatalf("expected Installed=true via versions/, got Installed=false")
	}
	if status.InstallMethod != InstallMethodNative {
		t.Fatalf("install method = %v, want Native", status.InstallMethod)
	}
	if status.Version != "2.1.181" {
		t.Fatalf("version = %q, want 2.1.181", status.Version)
	}
	if !strings.Contains(status.ExecutablePath, "versions/2.1.181/claude") {
		t.Fatalf("executable path = %q, expected versions/2.1.181/claude", status.ExecutablePath)
	}
}

// ---------------------------------------------------------------------------
// Cross-platform layout sanity
// ---------------------------------------------------------------------------

func TestClaudeNativeVersionsDir_POSIXLayout(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	home := t.TempDir()
	withClaudeUserHome(t, home)
	os.Unsetenv("XDG_DATA_HOME")

	got := claudeNativeVersionsDir()
	want := filepath.Join(home, ".local", "share", "claude", "versions")
	if got != want {
		t.Fatalf("POSIX versions dir = %q, want %q", got, want)
	}
}

func TestClaudeNativeVersionsDir_RespectsXDGDataHome(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	home := t.TempDir()
	withClaudeUserHome(t, home)
	xdg := t.TempDir()
	withXDGDataHome(t, xdg)

	got := claudeNativeVersionsDir()
	want := filepath.Join(xdg, "claude", "versions")
	if got != want {
		t.Fatalf("XDG versions dir = %q, want %q", got, want)
	}
}
