package envcheck

import (
	"testing"
)

// ---------------------------------------------------------------------------
// parseOpenCodeVersion
// ---------------------------------------------------------------------------

func TestParseOpenCodeVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"standard version with v prefix", "opencode v1.2.3", "1.2.3"},
		{"bare version", "1.2.3", "1.2.3"},
		{"semver with prerelease", "v2.0.0-rc.1+build.123", "2.0.0-rc.1"},
		{"version in sentence", "OpenCode version v0.5.10", "0.5.10"},
		{"multi-line", "OpenCode CLI\nv0.3.0", "0.3.0"},
		{"two-part version", "v1.0", "1.0"},
		{"four-part version", "v1.2.3.4", "1.2.3.4"},
		{"empty string", "", ""},
		{"no version", "OpenCode CLI", ""},
		{"whitespace only", "   ", ""},
		{"version with trailing text", "v1.2.3-dev", "1.2.3-dev"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseOpenCodeVersion(tc.input)
			if got != tc.expected {
				t.Errorf("parseOpenCodeVersion(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detectOpenCodeInstallMethod
// ---------------------------------------------------------------------------

func TestDetectOpenCodeInstallMethod(t *testing.T) {
	tests := []struct {
		name       string
		execPath   string
		wantMethod InstallMethod
	}{
		{
			name:       "npm via node_modules",
			execPath:   `C:\Users\alice\AppData\Roaming\npm\node_modules\opencode-ai\opencode.cmd`,
			wantMethod: InstallMethodNPM,
		},
		{
			name:       "unknown path",
			execPath:   `C:\Tools\opencode.exe`,
			wantMethod: InstallMethodUnknown,
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectOpenCodeInstallMethod(tc.execPath)
			if got != tc.wantMethod {
				t.Errorf("detectOpenCodeInstallMethod(%q) = %q, want %q", tc.execPath, got, tc.wantMethod)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isOpenCodeNPMPath
// ---------------------------------------------------------------------------

func TestIsOpenCodeNPMPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		expect bool
	}{
		{"node_modules", `c:\users\alice\appdata\roaming\npm\node_modules\opencode`, true},
		{"no npm indicators", `c:\tools\opencode.exe`, false},
		{"empty", ``, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalized := normalizeOpenCodePath(tc.path)
			got := isOpenCodeNPMPath(normalized)
			if got != tc.expect {
				t.Errorf("isOpenCodeNPMPath(%q [normalized: %q]) = %v, want %v", tc.path, normalized, got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isOpenCodeChocolateyPath
// ---------------------------------------------------------------------------

func TestIsOpenCodeChocolateyPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		expect bool
	}{
		{"chocolatey programdata", `c:\programdata\chocolatey\bin\opencode.exe`, true},
		{"chocolatey bin", `c:\chocolatey\bin\opencode.exe`, true},
		{"not chocolatey", `c:\tools\opencode.exe`, false},
		{"empty", ``, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalized := normalizeOpenCodePath(tc.path)
			got := isOpenCodeChocolateyPath(normalized)
			if got != tc.expect {
				t.Errorf("isOpenCodeChocolateyPath(%q) = %v, want %v", normalized, got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isOpenCodeScoopPath
// ---------------------------------------------------------------------------

func TestIsOpenCodeScoopPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		expect bool
	}{
		{"scoop apps", `c:\users\alice\scoop\apps\opencode\current\opencode.exe`, true},
		{"scoop shims", `c:\users\alice\scoop\shims\opencode.exe`, true},
		{"not scoop", `c:\tools\opencode.exe`, false},
		{"empty", ``, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalized := normalizeOpenCodePath(tc.path)
			got := isOpenCodeScoopPath(normalized)
			if got != tc.expect {
				t.Errorf("isOpenCodeScoopPath(%q) = %v, want %v", normalized, got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// normalizeOpenCodePath
// ---------------------------------------------------------------------------

func TestNormalizeOpenCodePath(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"upper case to lower backslash", `C:\Tools\OpenCode.exe`, `c:/tools/opencode.exe`},
		{"forward slash", `C:/Tools/OpenCode.exe`, `c:/tools/opencode.exe`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeOpenCodePath(tc.input)
			if got != tc.expect {
				t.Errorf("normalizeOpenCodePath(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}
