package envcheck

import (
	"errors"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
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

func TestOpenCodeVersionSpawnEFTYPEFallsBackToNPMList(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestExecutable(t, tmpDir, "npm")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		mockResponse{pathPrefix: "opencode", stderr: "Error: spawn EFTYPE\nNode.js v24.14.1", err: errors.New("spawn EFTYPE")},
		mockResponse{pathPrefix: "npm", stdout: "C:\\Users\\alice\\AppData\\Roaming\\npm\n`-- opencode-ai@1.2.3", err: nil},
	)

	version, issues, err := svc.openCodeVersionWithDiagnostics("opencode")
	if err != nil {
		t.Fatalf("expected fallback success, got error: %v", err)
	}
	if version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", version)
	}
	if len(issues) == 0 {
		t.Fatal("expected diagnostic issue preserving original EFTYPE failure")
	}
	if detail := issues[0].Detail; !strings.Contains(detail, "EFTYPE") || !strings.Contains(detail, "Node.js v24.14.1") {
		t.Fatalf("expected original EFTYPE/Node diagnostic in issue detail, got: %s", detail)
	}
}

func TestCheckOpenCodeSpawnEFTYPEFallbackDoesNotBlockStatus(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestExecutable(t, tmpDir, "opencode")
	writeTestExecutable(t, tmpDir, "npm")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		mockResponse{pathPrefix: "opencode", stderr: "Error: spawn EFTYPE", err: errors.New("spawn EFTYPE")},
		mockResponse{pathPrefix: "npm", stdout: "`-- opencode-ai@1.4.0", err: nil},
	)

	status, err := svc.checkOpenCode()
	if err != nil {
		t.Fatalf("checkOpenCode should not block when fallback succeeds: %v", err)
	}
	if status == nil || !status.Installed || status.Version != "1.4.0" {
		t.Fatalf("expected installed status from fallback, got %+v", status)
	}
	if strings.TrimSpace(status.Error) != "" {
		t.Fatalf("fallback success should not leave blocking status error: %s", status.Error)
	}
	if len(status.Issues) == 0 || !strings.Contains(status.Issues[0].Detail, "EFTYPE") {
		t.Fatalf("expected non-blocking diagnostic issue with EFTYPE detail, got %+v", status.Issues)
	}
}

func TestOpenCodeVersionSpawnEFTYPEWithoutFallbackRemainsError(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestExecutable(t, tmpDir, "npm")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		mockResponse{pathPrefix: "opencode", stderr: "Error: spawn EFTYPE", err: errors.New("spawn EFTYPE")},
		mockResponse{pathPrefix: "npm", stderr: "empty", err: errors.New("not installed")},
	)

	_, _, err := svc.openCodeVersionWithDiagnostics("opencode")
	if err == nil {
		t.Fatal("expected error when fallback cannot confirm OpenCode")
	}
	if !strings.Contains(err.Error(), "替代检测") || !strings.Contains(err.Error(), "spawn EFTYPE") {
		t.Fatalf("expected friendly fallback diagnostic preserving original error, got: %v", err)
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

// ---------------------------------------------------------------------------
// AC3/AC4 extended: fallback package list and NPM list parsing
// ---------------------------------------------------------------------------

func TestParseOpenCodeNPMListVersion(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		pkg     string
		want    string
	}{
		{"opencode-ai version", "`-- opencode-ai@1.2.3\n", "opencode-ai", "1.2.3"},
		{"opencode version", "opencode@0.5.10\n", "opencode", "0.5.10"},
		{"no version match", "some random output\n", "opencode-ai", ""},
		{"empty output", "", "opencode-ai", ""},
		{"multiline with version", "C:\\Users\\alice\\AppData\n`-- opencode-ai@2.0.0\n", "opencode-ai", "2.0.0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseOpenCodeNPMListVersion(tc.output, tc.pkg)
			if got != tc.want {
				t.Errorf("parseOpenCodeNPMListVersion(%q, %q) = %q, want %q", tc.output, tc.pkg, got, tc.want)
			}
		})
	}
}

func TestParsePackageJSONVersion_ValidManifest(t *testing.T) {
	manifest := `{"name":"opencode-ai","version":"1.4.2"}`
	got := parsePackageJSONVersion(manifest, "opencode-ai")
	if got != "1.4.2" {
		t.Errorf("parsePackageJSONVersion() = %q, want %q", got, "1.4.2")
	}
}

func TestParsePackageJSONVersion_NameMismatch(t *testing.T) {
	manifest := `{"name":"other-package","version":"1.0.0"}`
	got := parsePackageJSONVersion(manifest, "opencode-ai")
	if got != "" {
		t.Errorf("expected empty version for name mismatch, got %q", got)
	}
}

func TestParsePackageJSONVersion_InvalidJSON(t *testing.T) {
	got := parsePackageJSONVersion("not json", "opencode-ai")
	if got != "" {
		t.Errorf("expected empty version for invalid JSON, got %q", got)
	}
}

func TestOpenCodeFallbackPackages_ContainsExpectedEntries(t *testing.T) {
	hasOpencodeAI := false
	hasOpencode := false
	for _, pkg := range openCodeFallbackPackages {
		if pkg == "opencode-ai" {
			hasOpencodeAI = true
		}
		if pkg == "opencode" {
			hasOpencode = true
		}
	}
	if !hasOpencodeAI {
		t.Error("openCodeFallbackPackages should contain 'opencode-ai'")
	}
	if !hasOpencode {
		t.Error("openCodeFallbackPackages should contain 'opencode'")
	}
}

func TestOpenCodeVersionFailureDiagnostic_NilResult(t *testing.T) {
	diag := openCodeVersionFailureDiagnostic(nil, errors.New("spawn EFTYPE"))
	if !strings.Contains(diag, "EFTYPE") {
		t.Errorf("expected diagnostic to contain EFTYPE, got: %s", diag)
	}
}

func TestOpenCodeVersionFailureDiagnostic_WithResult(t *testing.T) {
	result := &platform.ProcessResult{Stderr: "Error: spawn EFTYPE\nNode.js v24.14.1"}
	diag := openCodeVersionFailureDiagnostic(result, errors.New("spawn EFTYPE"))
	if !strings.Contains(diag, "EFTYPE") {
		t.Errorf("expected diagnostic to contain EFTYPE, got: %s", diag)
	}
	if !strings.Contains(diag, "v24") {
		t.Errorf("expected diagnostic to contain Node version, got: %s", diag)
	}
}
