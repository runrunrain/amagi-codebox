package envcheck

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// Mock ProcessRunner
// ---------------------------------------------------------------------------

// mockRunner is a test-double for platform.ProcessRunner. It records calls
// and returns pre-configured results based on the command path.
type mockRunner struct {
	// responses maps command Path -> mockResponse. First match wins.
	responses []mockResponse
	// calls records every Run invocation for post-test assertions.
	calls []platform.CommandSpec
}

type mockResponse struct {
	pathPrefix string // match if spec.Path contains this substring
	stdout     string
	stderr     string
	err        error
}

func (m *mockRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
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
// detectClaudeInstallMethod - winget paths
// ---------------------------------------------------------------------------

func TestDetectClaudeInstallMethod_Winget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("skipping winget path detection on non-Windows")
	}

	svc := newTestService()

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		t.Skip("LOCALAPPDATA not set")
	}

	wingetPath := filepath.Join(localAppData, "Programs", "Claude Code", "claude.exe")
	got := svc.detectClaudeInstallMethod(wingetPath)
	if got != InstallMethodWinget {
		t.Errorf("detectClaudeInstallMethod(%q) = %q, want %q", wingetPath, got, InstallMethodWinget)
	}
}

// ---------------------------------------------------------------------------
// isWingetPath
// ---------------------------------------------------------------------------

func TestIsWingetPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		expect bool
	}{
		{"winget segment", `\winget\foo`, true},
		{"microsoft winget", `\microsoft\winget\foo`, true},
		{"windowsapps", `\windowsapps\foo`, true},
		{"packages microsoft", `\packages\microsoft.desktopappinstaller_\foo`, true},
		{"not winget", `\tools\`, false},
		{"empty", ``, false},
		{"winget without trailing", `\winget`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Normalize using the same logic the code uses
			normalized := normalizeClaudePath(tc.path)
			got := isWingetPath(normalized)
			if got != tc.expect {
				t.Errorf("isWingetPath(%q) = %v, want %v", normalized, got, tc.expect)
			}
		})
	}
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
		{"simple path", `C:\Tools\Claude.exe`, `c:\tools\claude.exe`},
		{"forward slashes", "C:/Tools/Claude.exe", `c:\tools\claude.exe`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if runtime.GOOS != "windows" && strings.Contains(tc.expect, `\`) {
				t.Skip("skipping Windows-specific path normalization")
			}
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
		{"trailing slash cleaned", `C:\Tools\`, `C:\Tools`},
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
		{"exact match", `c:\tools`, `c:\tools`, true},
		{"child path", `c:\tools\bin`, `c:\tools`, true},
		{"no match", `c:\other`, `c:\tools`, false},
		{"empty prefix", `c:\tools`, ``, false},
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
