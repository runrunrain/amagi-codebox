package envcheck

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseOmpVersion
// ---------------------------------------------------------------------------

func TestParseOmpVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"omp prefix", "omp/17.2.10", "17.2.10"},
		{"bare version", "17.2.10", "17.2.10"},
		{"semver with prerelease", "17.2.10-beta.1", "17.2.10-beta.1"},
		{"version in sentence", "omp version v17.2.10", "17.2.10"},
		{"multi-line", "omp CLI\n17.2.10", "17.2.10"},
		{"two-part version", "17.2", "17.2"},
		{"four-part version", "17.2.10.1", "17.2.10.1"},
		{"empty string", "", ""},
		{"no version", "omp CLI", ""},
		{"whitespace only", "   ", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseOmpVersion(tc.input)
			if got != tc.expected {
				t.Errorf("parseOmpVersion(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detectOmpInstallMethod
// ---------------------------------------------------------------------------

func TestDetectOmpInstallMethod(t *testing.T) {
	tests := []struct {
		name       string
		execPath   string
		wantMethod InstallMethod
	}{
		{
			name:       "homebrew cellar symlink target",
			execPath:   `/opt/homebrew/Cellar/omp/17.2.10/bin/omp`,
			wantMethod: InstallMethodHomebrew,
		},
		{
			name:       "homebrew prefix",
			execPath:   `/homebrew/foo/omp`,
			wantMethod: InstallMethodHomebrew,
		},
		{
			name:       "npm via node_modules",
			execPath:   `/usr/local/lib/node_modules/@oh-my-pi/pi-coding-agent/bin/omp`,
			wantMethod: InstallMethodNPM,
		},
		{
			name:       "bare bin shim without npm marker",
			execPath:   `/usr/local/bin/omp`,
			wantMethod: InstallMethodNative, // 与 detectPiInstallMethod 同语义：无 node_modules/npm 段则 native
		},
		{
			name:       "npm under homebrew prefix is still npm",
			execPath:   `/opt/homebrew/lib/node_modules/@oh-my-pi/pi-coding-agent/bin/omp`,
			wantMethod: InstallMethodNPM,
		},
		{
			name:       "native install script",
			execPath:   `~/.local/bin/omp`,
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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectOmpInstallMethod(tc.execPath)
			if got != tc.wantMethod {
				t.Errorf("detectOmpInstallMethod(%q) = %q, want %q", tc.execPath, got, tc.wantMethod)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// normalizeOmpPath
// ---------------------------------------------------------------------------

func TestNormalizeOmpPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"  ", ""},
		{"/opt/homebrew/Cellar/omp/17.2.10/bin/omp", "/opt/homebrew/cellar/omp/17.2.10/bin/omp"},
		{`C:\Users\alice\AppData\Roaming\npm\omp.cmd`, "c:/users/alice/appdata/roaming/npm/omp.cmd"},
	}
	for _, tc := range tests {
		got := normalizeOmpPath(tc.in)
		if got != tc.want {
			t.Errorf("normalizeOmpPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestOmpVersionOutputSample verifies the exact --version output shape of the
// brew-installed omp (observed: "omp/17.2.10").
func TestOmpVersionOutputSample(t *testing.T) {
	got := parseOmpVersion("omp/17.2.10")
	if got != "17.2.10" {
		t.Fatalf("parseOmpVersion(omp/17.2.10) = %q, want 17.2.10", got)
	}
}

// TestOmpNPMGlobalExecutableCandidates ensures omp npm candidates are derived
// from the omp command name and package name.
func TestOmpNPMGlobalExecutableCandidates_ValidPrefix(t *testing.T) {
	got := ompNPMGlobalExecutableCandidates("/usr/local")
	if len(got) == 0 {
		t.Fatal("expected candidates for valid prefix")
	}
	found := false
	for _, c := range got {
		if strings.Contains(c, "omp") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no candidate contains omp command name: %v", got)
	}
}
