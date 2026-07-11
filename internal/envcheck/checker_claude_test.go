package envcheck

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// Mock ProcessRunner
// ---------------------------------------------------------------------------

// mockRunner is a test-double for platform.ProcessRunner. It records calls
// and returns pre-configured results based on the command path.
// All methods are safe for concurrent use.
type mockRunner struct {
	// responses maps command Path -> mockResponse. First match wins.
	responses []mockResponse
	// calls records every Run invocation for post-test assertions.
	calls []platform.CommandSpec
	mu    sync.Mutex
}

type mockResponse struct {
	pathPrefix string // match if spec.Path contains this substring
	stdout     string
	stderr     string
	err        error
}

func (m *mockRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, spec)
	for _, r := range m.responses {
		if r.pathPrefix == "" || strings.Contains(spec.Path, r.pathPrefix) {
			return &platform.ProcessResult{
				Stdout: r.stdout,
				Stderr: r.stderr,
			}, r.err
		}
	}
	// Default: command not found behavior
	return &platform.ProcessResult{}, os.ErrNotExist
}

func (m *mockRunner) Start(spec platform.CommandSpec) (*exec.Cmd, error) {
	// Not needed for envcheck tests but required by interface.
	return nil, nil
}

// helper: build a mock Service backed by mockRunner.
func newTestService(responses ...mockResponse) *Service {
	m := &mockRunner{responses: responses}
	return NewServiceWithRunner(m)
}

// ---------------------------------------------------------------------------
// parseClaudeVersion
// ---------------------------------------------------------------------------

func TestParseClaudeVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"standard format", "Claude Code v2.1.110", "2.1.110"},
		{"version only", "2.1.110", "2.1.110"},
		{"with v prefix", "v2.1.110", "2.1.110"},
		{"simple version", "1.0", "1.0"},
		{"multi-line output", "Claude Code\n2.1.110", "2.1.110"},
		{"empty string", "", ""},
		{"no digits", "Claude Code", ""},
		{"version with extra text", "Claude Code v2.1.110 (official)", "2.1.110"},
		{"patch version", "3.0.5.1", "3.0.5.1"},
		{"whitespace padded", "  2.1.110  ", "2.1.110"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseClaudeVersion(tc.input)
			if got != tc.expected {
				t.Errorf("parseClaudeVersion(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detectClaudeInstallMethod
// ---------------------------------------------------------------------------

func TestDetectClaudeInstallMethod(t *testing.T) {
	svc := newTestService()

	tests := []struct {
		name         string
		execPath     string
		wantMethod   InstallMethod
		skipOnNonWin bool
	}{
		{
			name:       "npm via node_modules",
			execPath:   `C:\Users\alice\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code\claude.cmd`,
			wantMethod: InstallMethodNPM,
		},
		{
			name:       "npm via npm in path",
			execPath:   `C:\Users\alice\AppData\Roaming\npm\claude.cmd`,
			wantMethod: InstallMethodNPM,
		},
		{
			name:         "native install under .local/bin",
			execPath:     buildNativePath(t),
			wantMethod:   InstallMethodNative,
			skipOnNonWin: true,
		},
		{
			name:       "unknown random path",
			execPath:   `C:\Tools\claude.exe`,
			wantMethod: InstallMethodUnknown,
		},
		{
			name:       "empty path",
			execPath:   "",
			wantMethod: InstallMethodUnknown,
		},
		{
			name:       "whitespace only path",
			execPath:   "   ",
			wantMethod: InstallMethodUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipOnNonWin && runtime.GOOS != "windows" {
				t.Skip("skipping on non-Windows")
			}
			got := svc.detectClaudeInstallMethod(tc.execPath)
			if got != tc.wantMethod {
				t.Errorf("detectClaudeInstallMethod(%q) = %q, want %q", tc.execPath, got, tc.wantMethod)
			}
		})
	}
}

// buildNativePath constructs a path under USERPROFILE\.local\bin for testing.
func buildNativePath(t *testing.T) string {
	t.Helper()
	home := os.Getenv("USERPROFILE")
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		t.Skip("no USERPROFILE or HOME env var")
	}
	return filepath.Join(home, ".local", "bin", "claude.exe")
}

// ---------------------------------------------------------------------------
// detectClaudeInstallMethod - removed legacy package-manager paths
// ---------------------------------------------------------------------------

func TestDetectClaudeInstallMethod_RemovedLegacyPackageManagerPathIsUnknown(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("skipping removed legacy path detection on non-Windows")
	}

	svc := newTestService()

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		t.Skip("LOCALAPPDATA not set")
	}

	removedLegacyPath := filepath.Join(localAppData, "Programs", "Claude Code", "claude.exe")
	got := svc.detectClaudeInstallMethod(removedLegacyPath)
	if got != InstallMethodUnknown {
		t.Errorf("detectClaudeInstallMethod(%q) = %q, want %q", removedLegacyPath, got, InstallMethodUnknown)
	}
}

func TestCheckClaudeCodePrefersNativeDefaultOverPATHShimOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin native Claude default path resolution is only defined on macOS")
	}

	homeDir := t.TempDir()
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	nativePath := filepath.Join(nativeDir, "claude")
	if err := os.MkdirAll(nativeDir, 0o755); err != nil {
		t.Fatalf("mkdir native dir: %v", err)
	}
	if err := os.WriteFile(nativePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write native claude: %v", err)
	}

	npmDir := t.TempDir()
	npmShim := filepath.Join(npmDir, "claude")
	if err := os.WriteFile(npmShim, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write npm shim: %v", err)
	}

	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", npmDir)
	previousHomeDir := claudeUserHomeDir
	claudeUserHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { claudeUserHomeDir = previousHomeDir })

	svc := newTestService(mockResponse{pathPrefix: nativePath, stdout: "Claude Code v2.1.132"})
	status, err := svc.checkClaudeCode()
	if err != nil {
		t.Fatalf("checkClaudeCode: %v", err)
	}
	if !status.Installed {
		t.Fatalf("expected installed status, got %+v", status)
	}
	if !sameNormalizedPath(status.ExecutablePath, nativePath) {
		t.Fatalf("executable path = %q, want native path %q instead of PATH shim %q", status.ExecutablePath, nativePath, npmShim)
	}
	if status.InstallMethod != InstallMethodNative {
		t.Fatalf("install method = %q, want %q", status.InstallMethod, InstallMethodNative)
	}
}

func TestCheckClaudeCodeDetectsNPMPackageBinaryFallback(t *testing.T) {
	previousGOOS := runtimeGOOS
	runtimeGOOS = "windows"
	t.Cleanup(func() { runtimeGOOS = previousGOOS })

	// Self-heal (P0-3): the package-binary fallback is now gated by an
	// on-disk integrity check that rejects truncated shards. Lower the
	// threshold for this test so the small fixture still qualifies as
	// healthy; production threshold is 100MB.
	previousMin := claudeNPMIntegrityMinBytes
	claudeNPMIntegrityMinBytes = 1
	t.Cleanup(func() { claudeNPMIntegrityMinBytes = previousMin })

	homeDir := t.TempDir()
	appData := filepath.Join(t.TempDir(), "AppData", "Roaming")
	// npmPrefix emulates what `npm prefix -g` would return on Windows
	// (e.g. C:\Users\alice\AppData\Roaming\npm). We construct it with
	// filepath.Join so the fixture path on disk uses the host platform's
	// native separator. This keeps the test meaningful on every platform:
	//   - runtimeGOOS stub drives business code (claudeNPMPackageBinaryNames)
	//     to look up Windows package names (claude-code-win32-x64).
	//   - filepath.Join on the host makes the on-disk fixture and the glob
	//     pattern agree on separators, so filepath.Glob in
	//     claudeNPMPackageBinaryFallbackCandidates actually hits the fixture.
	// The original hard-coded `\npm` suffix produced a literal backslash in
	// the path on non-Windows hosts, which filepath.Glob treats as an escape
	// (not a separator), so the fallback directory was never matched.
	npmPrefix := filepath.Join(appData, "npm")
	fallbackPath := filepath.Join(npmPrefix, "node_modules", "@anthropic-ai", ".claude-code-nDGSeslo", "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0o755); err != nil {
		t.Fatalf("mkdir fallback dir: %v", err)
	}
	if err := os.WriteFile(fallbackPath, bytes.Repeat([]byte{0x4d, 0x5a}, 16), 0o755); err != nil {
		t.Fatalf("write fallback claude exe: %v", err)
	}

	t.Setenv("PATH", "")
	t.Setenv("APPDATA", appData)
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOME", homeDir)
	previousHomeDir := claudeUserHomeDir
	claudeUserHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { claudeUserHomeDir = previousHomeDir })

	runner := &claudePackageFallbackRunner{
		npmPrefix:    npmPrefix,
		fallbackPath: fallbackPath,
	}
	svc := NewServiceWithRunner(runner)
	status, err := svc.checkClaudeCode()
	if err != nil {
		t.Fatalf("checkClaudeCode returned error: %v", err)
	}
	if !status.Installed {
		t.Fatalf("expected installed status, got %+v", status)
	}
	if !status.PATHOk {
		t.Fatalf("expected PATHOk because CodeBox can launch fallback path, got %+v", status)
	}
	if status.Error != "" {
		t.Fatalf("expected npm confirmation failure to be non-blocking for package binary fallback, got error %q", status.Error)
	}
	if status.Version != "2.1.150" {
		t.Fatalf("version = %q, want 2.1.150", status.Version)
	}
	if !sameNormalizedPath(status.ExecutablePath, fallbackPath) {
		t.Fatalf("executable path = %q, want fallback path %q", status.ExecutablePath, fallbackPath)
	}
	if status.InstallMethod != InstallMethodNPM {
		t.Fatalf("install method = %q, want %q", status.InstallMethod, InstallMethodNPM)
	}
	if !hasIssueCode(status, "claude_npm_package_binary_fallback") {
		t.Fatalf("expected package fallback warning issue, got %+v", status.Issues)
	}
	if hasIssueCode(status, "tool_not_installed") {
		t.Fatalf("fallback executable must not be reported as missing, got %+v", status.Issues)
	}
}

type claudePackageFallbackRunner struct {
	npmPrefix    string
	fallbackPath string
	calls        []platform.CommandSpec
	mu           sync.Mutex
}

func (r *claudePackageFallbackRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, spec)

	pathLower := strings.ToLower(spec.Path)
	args := append([]string(nil), spec.Args...)
	if isNPMPath(pathLower) && len(args) == 2 && args[0] == "prefix" && args[1] == "-g" {
		return &platform.ProcessResult{Stdout: r.npmPrefix}, nil
	}
	if isNPMPath(pathLower) && len(args) >= 4 && args[0] == "list" && args[1] == "-g" && args[2] == "@anthropic-ai/claude-code" {
		return &platform.ProcessResult{Stdout: r.npmPrefix + "\n`-- (empty)"}, errors.New("not installed")
	}
	if sameNormalizedPath(spec.Path, r.fallbackPath) && len(args) == 1 && args[0] == "--version" {
		return &platform.ProcessResult{Stdout: "2.1.150 (Claude Code)"}, nil
	}
	return &platform.ProcessResult{}, os.ErrNotExist
}

func (r *claudePackageFallbackRunner) Start(spec platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

func hasIssueCode(status *CheckStatus, code string) bool {
	if status == nil {
		return false
	}
	for _, issue := range status.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// normalizeClaudePath
// ---------------------------------------------------------------------------

func TestNormalizeClaudePath(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"simple path", `C:\Tools\Claude.exe`, `c:/tools/claude.exe`},
		{"forward slashes", "C:/Tools/Claude.exe", `c:/tools/claude.exe`},
		{"mixed slashes", `C:\Tools/Claude.exe`, `c:/tools/claude.exe`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeClaudePath(tc.input)
			if got != tc.expect {
				t.Errorf("normalizeClaudePath(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveRealExecutablePath
// ---------------------------------------------------------------------------

func TestResolveRealExecutablePath(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"empty returns empty", "", ""},
		{"whitespace returns empty", "  ", ""},
		{"dot returns original", ".", "."},
		{"simple path cleaned", `C:\Tools\Claude.exe`, `C:\Tools\Claude.exe`},
		{"trailing backslash cleaned", `C:\Tools\`, `C:\Tools`},
		{"trailing forward slash cleaned", `C:/Tools/`, filepath.Clean(`C:/Tools`)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveRealExecutablePath(tc.input)
			if got != tc.expect {
				t.Errorf("resolveRealExecutablePath(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// pathHasPrefix
// ---------------------------------------------------------------------------

func TestPathHasPrefix(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		prefix string
		expect bool
	}{
		{"exact match", `c:/tools`, `c:/tools`, true},
		{"child path", `c:/tools/bin`, `c:/tools`, true},
		{"no match", `c:/other`, `c:/tools`, false},
		{"empty prefix", `c:/tools`, ``, false},
		{"empty both", ``, ``, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pathHasPrefix(tc.path, tc.prefix)
			if got != tc.expect {
				t.Errorf("pathHasPrefix(%q, %q) = %v, want %v", tc.path, tc.prefix, got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// M-1: buildClaudeCorruptionSelfHealIssue (detect-path self-heal rendering)
// ---------------------------------------------------------------------------

func TestBuildClaudeCorruptionSelfHealIssue_SuccessRendersResinstallSolutions(t *testing.T) {
	issue := buildClaudeCorruptionSelfHealIssue(
		errors.New("claude binary corrupted at /x/claude: binary size below healthy"),
		claudeNPMResidueSelfHealOutcome{
			Triggered: true,
			Integrity: ClaudeBinaryIntegrity{
				Exists:    true,
				Corrupted: true,
				Reason:    "binary size below healthy minimum",
			},
			Cleanup: &claudeNPMResidueCleanupResult{
				StagingDirs:    []string{"/x/.claude-code-AAA"},
				PackageDirs:    []string{"/x/claude-code"},
				OrphanBinLinks: []string{"/x/bin/claude"},
			},
		},
	)
	if issue.Severity != SeverityWarning {
		t.Fatalf("severity = %v, want %v", issue.Severity, SeverityWarning)
	}
	if issue.Code != "claude_install_interrupted_residue_cleaned" {
		t.Fatalf("code = %q", issue.Code)
	}
	if !strings.Contains(issue.Message, "已自动清理") {
		t.Fatalf("message should mention auto-cleanup: %q", issue.Message)
	}
	if !strings.Contains(issue.Detail, "诊断") {
		t.Fatalf("detail should include diagnosis: %q", issue.Detail)
	}
	if !strings.Contains(issue.Detail, "已清理") {
		t.Fatalf("detail should include cleanup summary: %q", issue.Detail)
	}
	// Must expose SolutionInstallClaudeMethod (frontend's primary reinstall
	// entry), SolutionCleanClaudeInstall (one-click cleanup), and
	// SolutionManualCommand (terminal fallback).
	seen := map[SolutionType]bool{}
	for _, sol := range issue.Solutions {
		seen[sol.Type] = true
	}
	if !seen[SolutionInstallClaudeMethod] || !seen[SolutionCleanClaudeInstall] || !seen[SolutionManualCommand] {
		t.Fatalf("missing required solutions, got %v", seen)
	}
	// The primary action must be SolutionInstallClaudeMethod so the frontend
	// highlights the reinstall button.
	var primary *ResolutionAction
	for i := range issue.Solutions {
		if issue.Solutions[i].IsPrimary {
			primary = &issue.Solutions[i]
		}
	}
	if primary == nil || primary.Type != SolutionInstallClaudeMethod {
		t.Fatalf("primary solution must be SolutionInstallClaudeMethod, got %+v", primary)
	}
}

func TestBuildClaudeCorruptionSelfHealIssue_CleanupFailureChangesCodeAndMessage(t *testing.T) {
	issue := buildClaudeCorruptionSelfHealIssue(
		errors.New("claude binary likely corrupted (AMFI SIGKILL / exit code 137): signal: killed"),
		claudeNPMResidueSelfHealOutcome{
			Triggered:  true,
			CleanupErr: errors.New("npm prefix -g: file does not exist"),
		},
	)
	if issue.Code != "claude_install_interrupted_residue_cleanup_failed" {
		t.Fatalf("code = %q, want claude_install_interrupted_residue_cleanup_failed", issue.Code)
	}
	if !strings.Contains(issue.Message, "自动清理未完成") {
		t.Fatalf("message should warn cleanup failed: %q", issue.Message)
	}
	if !strings.Contains(issue.Detail, "npm prefix -g") {
		t.Fatalf("detail should expose cleanup failure cause: %q", issue.Detail)
	}
}

func TestBuildClaudeCorruptionSelfHealIssue_EmptyOutcomeAdaptsDetail(t *testing.T) {
	issue := buildClaudeCorruptionSelfHealIssue(
		errors.New("claude binary likely corrupted (AMFI SIGKILL / exit code 137): signal: killed"),
		claudeNPMResidueSelfHealOutcome{
			Triggered: true,
			Cleanup:   &claudeNPMResidueCleanupResult{},
		},
	)
	if !strings.Contains(issue.Detail, "未发现可清理") {
		t.Fatalf("detail should note that no residue was found: %q", issue.Detail)
	}
}

// ---------------------------------------------------------------------------
// M-1: end-to-end detect-path self-heal via checkClaudeCode
// ---------------------------------------------------------------------------

// corruptBinaryRunner is a ProcessRunner double that emulates a corrupted
// Claude Code binary on disk:
//   - When invoked with a path containing the marker "/claude", it mimics
//     macOS AMFI SIGKILL by returning an error with stderr "signal: killed"
//     (so classifyClaudeVersionError identifies it via isClaudeSIGKILLSignal).
//   - When invoked with args ["prefix", "-g"] (npm prefix -g), it returns
//     the configured npm prefix so cleanClaudeNPMResidue can find the scoped
//     directory and clean it.
//
// This lets the test drive checkClaudeCode end-to-end and assert that
// detect-path self-heal was triggered as part of the check.
type corruptBinaryRunner struct {
	prefix string
	mu     sync.Mutex
	calls  []platform.CommandSpec
}

func (r *corruptBinaryRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, spec)

	if len(spec.Args) == 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g" {
		return &platform.ProcessResult{Stdout: r.prefix}, nil
	}
	if strings.HasSuffix(spec.Path, "/claude") || strings.HasSuffix(spec.Path, "claude") {
		// Emulate macOS AMFI SIGKILL: stderr carries "signal: killed" so
		// isClaudeSIGKILLSignal fires.
		return &platform.ProcessResult{Stderr: "signal: killed"}, errors.New("signal: killed")
	}
	return &platform.ProcessResult{}, nil
}

func (r *corruptBinaryRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// TestCheckClaudeCode_DetectPathSelfHealOnCorruptedBinary verifies the M-1
// detect-path integration: when claudeVersion fails with a corruption-class
// error, checkClaudeCode automatically runs cleanClaudeNPMResidue and
// surfaces a structured issue with reinstall solutions to the frontend.
func TestCheckClaudeCode_DetectPathSelfHealOnCorruptedBinary(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 100 * 1024 * 1024)

	root := t.TempDir()
	prefix := filepath.Join(root, "npm-global")
	scopedDir := filepath.Join(prefix, "lib", "node_modules", "@anthropic-ai")
	staging := filepath.Join(scopedDir, ".claude-code-DeadBeef")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	writeFixture(t, filepath.Join(staging, "leftover"), []byte("x"))

	// Place a (truncated) binary on disk so resolveExecutable can find it via
	// an explicit PATH entry.
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	claudePath := filepath.Join(binDir, "claude")
	writeFixture(t, claudePath, bytes_30MB()) // below 100MB threshold
	if err := os.Chmod(claudePath, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	// Force the PATH-based resolver to find our fixture. We point PATH at the
	// freshly created bin directory.
	t.Setenv("PATH", binDir)

	runner := &corruptBinaryRunner{prefix: prefix}
	svc := NewServiceWithRunner(runner)

	status, err := svc.checkClaudeCode()
	if err == nil {
		t.Fatal("expected error from claudeVersion (SIGKILL on corrupted binary)")
	}
	if status == nil {
		t.Fatal("expected non-nil status even when claudeVersion fails")
	}
	// Installed stays true because the binary file exists on disk (resolveExecutable
	// found it via PATH); the corruption surfaces through status.Error and the
	// self-heal issue. This is the same Installed=true + Error!="" signal the
	// frontend uses to distinguish "broken binary" from "not installed".
	if !status.Installed {
		t.Fatalf("Installed should remain true (binary exists on disk); got %+v", status)
	}
	if status.Error == "" {
		t.Fatalf("status.Error should carry the classified corruption message; got %+v", status)
	}

	// Find the self-heal issue.
	var issue *CheckIssue
	for i := range status.Issues {
		if status.Issues[i].Code == "claude_install_interrupted_residue_cleaned" ||
			status.Issues[i].Code == "claude_install_interrupted_residue_cleanup_failed" {
			issue = &status.Issues[i]
			break
		}
	}
	if issue == nil {
		t.Fatalf("expected self-heal issue on status, got %+v", status.Issues)
	}
	if !strings.Contains(issue.Message, "已自动清理") && !strings.Contains(issue.Message, "自动清理未完成") {
		t.Fatalf("issue message should describe self-heal outcome: %q", issue.Message)
	}
	if !strings.Contains(issue.Detail, "诊断") {
		t.Fatalf("detail should include diagnosis: %q", issue.Detail)
	}

	// Staging residue MUST have been removed by detect-path self-heal.
	if _, statErr := os.Stat(staging); !os.IsNotExist(statErr) {
		t.Fatalf("staging dir should be removed after detect-path self-heal: %v", statErr)
	}
}

// TestCheckClaudeCode_NoSelfHealOnHealthyBinary documents the inverse: when
// claudeVersion returns a version successfully, no self-heal issue is
// attached and no staging directory is touched.
func TestCheckClaudeCode_NoSelfHealOnHealthyBinary(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)

	root := t.TempDir()
	prefix := filepath.Join(root, "npm-global")
	scopedDir := filepath.Join(prefix, "lib", "node_modules", "@anthropic-ai")
	staging := filepath.Join(scopedDir, ".claude-code-ShouldNotBeTouched")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	writeFixture(t, filepath.Join(staging, "canary"), []byte("must-survive"))

	// healthyRunner returns a valid version output for `claude --version`
	// and the configured prefix for `npm prefix -g`.
	healthy := &healthyClaudeRunner{prefix: prefix, version: "2.1.183 (Claude Code)"}
	svc := NewServiceWithRunner(healthy)

	// Place the claude binary on disk so resolveExecutable can find it.
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	claudePath := filepath.Join(binDir, "claude")
	writeFixture(t, claudePath, []byte("#!/bin/sh\necho ok\n"))
	if err := os.Chmod(claudePath, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Setenv("PATH", binDir)

	status, err := svc.checkClaudeCode()
	if err != nil {
		t.Fatalf("healthy check should not error: %v", err)
	}
	if status == nil || !status.Installed {
		t.Fatalf("expected Installed=true, got %+v", status)
	}
	for _, issue := range status.Issues {
		if strings.Contains(issue.Code, "claude_install_interrupted_residue") {
			t.Fatalf("self-heal issue must not appear on healthy binary: %+v", issue)
		}
	}
	// Staging residue must survive untouched on healthy path.
	if _, err := os.Stat(staging); err != nil {
		t.Fatalf("staging canary must survive on healthy path: %v", err)
	}
}

// healthyClaudeRunner is the ProcessRunner double for the healthy-binary
// checkClaudeCode path: returns a valid version string for `claude` and the
// configured prefix for `npm prefix -g`.
type healthyClaudeRunner struct {
	prefix  string
	version string
}

func (r *healthyClaudeRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	if len(spec.Args) == 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g" {
		return &platform.ProcessResult{Stdout: r.prefix}, nil
	}
	if len(spec.Args) == 3 && spec.Args[0] == "list" && spec.Args[1] == "-g" {
		// confirmClaudeNPMInstall probe -- return a listing that mentions the package.
		return &platform.ProcessResult{Stdout: r.prefix + "/node_modules/@anthropic-ai/claude-code\n"}, nil
	}
	if len(spec.Args) == 1 && spec.Args[0] == "--version" {
		return &platform.ProcessResult{Stdout: r.version}, nil
	}
	return &platform.ProcessResult{}, nil
}

func (r *healthyClaudeRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}
