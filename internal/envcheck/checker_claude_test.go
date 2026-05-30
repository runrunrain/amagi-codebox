package envcheck

import (
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

	homeDir := t.TempDir()
	appData := filepath.Join(t.TempDir(), "AppData", "Roaming")
	npmPrefix := strings.TrimRight(appData, `/\`) + `\npm`
	fallbackPath := filepath.Join(npmPrefix, "node_modules", "@anthropic-ai", ".claude-code-nDGSeslo", "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0o755); err != nil {
		t.Fatalf("mkdir fallback dir: %v", err)
	}
	if err := os.WriteFile(fallbackPath, []byte("MZ"), 0o755); err != nil {
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
