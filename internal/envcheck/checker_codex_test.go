package envcheck

import (
	"testing"
)

// ---------------------------------------------------------------------------
// parseCodexVersion
// ---------------------------------------------------------------------------

func TestParseCodexVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"codex-cli format", "codex-cli 0.87.0", "0.87.0"},
		{"bare version", "0.87.0", "0.87.0"},
		{"version with prefix text", "codex 0.1.0", "0.1.0"},
		{"version with prerelease", "codex 0.87.0-beta.1", "0.87.0-beta.1"},
		{"version with build metadata", "codex 0.87.0+build.123", "0.87.0+build.123"},
		{"multi-line output", "OpenAI Codex CLI\n0.5.0\n", "0.5.0"},
		{"two-part version", "codex 1.0", "1.0"},
		{"four-part version", "codex 1.2.3.4", "1.2.3.4"},
		{"empty string", "", ""},
		{"no version number", "codex-cli", ""},
		{"whitespace padded", "  0.87.0  ", "0.87.0"},
		{"version in sentence", "Installed: v0.87.0", "0.87.0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCodexVersion(tc.input)
			if got != tc.expected {
				t.Errorf("parseCodexVersion(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detectCodexInstallMethod
// ---------------------------------------------------------------------------

func TestDetectCodexInstallMethod(t *testing.T) {
	tests := []struct {
		name       string
		execPath   string
		wantMethod InstallMethod
	}{
		{
			name:       "npm via node_modules",
			execPath:   `C:\Users\alice\AppData\Roaming\npm\node_modules\@openai\codex\codex.cmd`,
			wantMethod: InstallMethodNPM,
		},
		{
			name:       "native default",
			execPath:   `C:\Tools\codex.exe`,
			wantMethod: InstallMethodNative,
		},
		{
			name:       "empty path",
			execPath:   "",
			wantMethod: InstallMethodUnknown,
		},
		{
			name:       "whitespace path",
			execPath:   "   ",
			wantMethod: InstallMethodUnknown,
		},
		{
			name:       "non-npm tools path",
			execPath:   `/usr/local/bin/codex`,
			wantMethod: InstallMethodNative,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectCodexInstallMethod(tc.execPath)
			if got != tc.wantMethod {
				t.Errorf("detectCodexInstallMethod(%q) = %q, want %q", tc.execPath, got, tc.wantMethod)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// normalizeCodexPath
// ---------------------------------------------------------------------------

func TestNormalizeCodexPath(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"upper case to lower backslash", `C:\Tools\Codex.exe`, `c:/tools/codex.exe`},
		{"forward slash", `C:/Tools/Codex.exe`, `c:/tools/codex.exe`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeCodexPath(tc.input)
			if got != tc.expect {
				t.Errorf("normalizeCodexPath(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}
