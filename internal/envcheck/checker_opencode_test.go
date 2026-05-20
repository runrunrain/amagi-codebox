package envcheck

import (
	"errors"
	"os"
	"path/filepath"
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
			name:       "homebrew cellar",
			execPath:   `/opt/homebrew/Cellar/opencode/1.14.50/bin/opencode`,
			wantMethod: InstallMethodHomebrew,
		},
		{
			name:       "npm under homebrew prefix is still npm",
			execPath:   `/opt/homebrew/lib/node_modules/opencode-ai/bin/opencode`,
			wantMethod: InstallMethodNPM,
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
// isOpenCodeHomebrewPath
// ---------------------------------------------------------------------------

func TestIsOpenCodeHomebrewPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		expect bool
	}{
		{"arm homebrew cellar", `/opt/homebrew/Cellar/opencode/1.14.50/bin/opencode`, true},
		{"linuxbrew cellar", `/home/linuxbrew/.linuxbrew/Cellar/opencode/1.14.50/bin/opencode`, true},
		{"homebrew opt", `/opt/homebrew/opt/opencode/bin/opencode`, true},
		{"homebrew bin symlink path", `/opt/homebrew/bin/opencode`, true},
		{"npm under homebrew prefix", `/opt/homebrew/lib/node_modules/opencode-ai/bin/opencode`, false},
		{"non homebrew", `/usr/local/custom/opencode`, false},
		{"empty", ``, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalized := normalizeOpenCodePath(tc.path)
			got := isOpenCodeHomebrewPath(normalized)
			if got != tc.expect {
				t.Errorf("isOpenCodeHomebrewPath(%q) = %v, want %v", normalized, got, tc.expect)
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
		name   string
		output string
		pkg    string
		want   string
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

// ---------------------------------------------------------------------------
// openCodeNPMGlobalExecutableCandidates
// ---------------------------------------------------------------------------

func TestOpenCodeNPMGlobalExecutableCandidates_ValidPrefix(t *testing.T) {
	candidates := openCodeNPMGlobalExecutableCandidates("/usr/local")
	if len(candidates) == 0 {
		t.Fatal("expected non-empty candidates for valid prefix")
	}
	// Must contain prefix/bin/opencode
	expectedBin := filepath.Join("/usr/local", "bin", "opencode")
	found := false
	for _, c := range candidates {
		if c == expectedBin {
			found = true
		}
	}
	if !found {
		t.Errorf("candidates should include %q, got: %v", expectedBin, candidates)
	}
}

func TestOpenCodeNPMGlobalExecutableCandidates_EmptyPrefix(t *testing.T) {
	candidates := openCodeNPMGlobalExecutableCandidates("")
	if candidates != nil {
		t.Errorf("expected nil for empty prefix, got: %v", candidates)
	}
}

func TestOpenCodeNPMGlobalExecutableCandidates_DotPrefix(t *testing.T) {
	candidates := openCodeNPMGlobalExecutableCandidates(".")
	if candidates != nil {
		t.Errorf("expected nil for '.' prefix, got: %v", candidates)
	}
}

func TestOpenCodeNPMGlobalExecutableCandidates_WhitespacePrefix(t *testing.T) {
	candidates := openCodeNPMGlobalExecutableCandidates("  ")
	if candidates != nil {
		t.Errorf("expected nil for whitespace-only prefix, got: %v", candidates)
	}
}

func TestOpenCodeNPMGlobalExecutableCandidates_Deduplicates(t *testing.T) {
	candidates := openCodeNPMGlobalExecutableCandidates("/opt/npm")
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c] {
			t.Errorf("duplicate candidate: %q", c)
		}
		seen[c] = true
	}
}

func TestOpenCodeNPMGlobalExecutableCandidates_IncludesPrefixAndNodeModulesBin(t *testing.T) {
	candidates := openCodeNPMGlobalExecutableCandidates("/opt/npm")
	prefixDirect := filepath.Join("/opt/npm", "opencode")
	nodeModulesBin := filepath.Join("/opt/npm", "node_modules", ".bin", "opencode")
	binDir := filepath.Join("/opt/npm", "bin", "opencode")

	hasPrefix := false
	hasNodeModulesBin := false
	hasBin := false
	for _, c := range candidates {
		if c == prefixDirect {
			hasPrefix = true
		}
		if c == nodeModulesBin {
			hasNodeModulesBin = true
		}
		if c == binDir {
			hasBin = true
		}
	}
	if !hasPrefix {
		t.Errorf("candidates should include prefix/opencode, got: %v", candidates)
	}
	if !hasNodeModulesBin {
		t.Errorf("candidates should include prefix/node_modules/.bin/opencode, got: %v", candidates)
	}
	if !hasBin {
		t.Errorf("candidates should include prefix/bin/opencode, got: %v", candidates)
	}
}

// ---------------------------------------------------------------------------
// isOpenCodeNPMGlobalInstallCommand
// ---------------------------------------------------------------------------

func TestIsOpenCodeNPMGlobalInstallCommand_OpenCodeNPMArgs(t *testing.T) {
	cmd := installCommand{
		description: "npm global install opencode-ai@latest",
		args:        []string{"install", "-g", "opencode-ai@latest"},
	}
	if !isOpenCodeNPMGlobalInstallCommand(ToolOpenCode, cmd) {
		t.Error("expected true for OpenCode npm install command")
	}
}

func TestIsOpenCodeNPMGlobalInstallCommand_OpenCodeBarePackage(t *testing.T) {
	cmd := installCommand{
		description: "npm global install opencode",
		args:        []string{"install", "-g", "opencode"},
	}
	if !isOpenCodeNPMGlobalInstallCommand(ToolOpenCode, cmd) {
		t.Error("expected true for OpenCode bare package install command")
	}
}

func TestIsOpenCodeNPMGlobalInstallCommand_ClaudeCodeNotTriggered(t *testing.T) {
	cmd := installCommand{
		description: "npm global install @anthropic-ai/claude-code@latest",
		args:        []string{"install", "-g", "@anthropic-ai/claude-code@latest"},
	}
	if isOpenCodeNPMGlobalInstallCommand(ToolClaudeCode, cmd) {
		t.Error("expected false for Claude Code tool (should not trigger OpenCode npm global prefix logic)")
	}
}

func TestIsOpenCodeNPMGlobalInstallCommand_CodexNotTriggered(t *testing.T) {
	cmd := installCommand{
		description: "npm global install @openai/codex",
		args:        []string{"install", "-g", "@openai/codex"},
	}
	if isOpenCodeNPMGlobalInstallCommand(ToolCodex, cmd) {
		t.Error("expected false for Codex tool (should not trigger OpenCode npm global prefix logic)")
	}
}

func TestIsOpenCodeNPMGlobalInstallCommand_OpenCodeUnrelatedArgs(t *testing.T) {
	cmd := installCommand{
		description: "some other command",
		args:        []string{"--version"},
	}
	// Neither args nor description mention opencode-ai/opencode, so should return false
	if isOpenCodeNPMGlobalInstallCommand(ToolOpenCode, cmd) {
		t.Error("should return false when neither args nor description reference opencode/opencode-ai")
	}
}

func TestIsOpenCodeNPMGlobalInstallCommand_OpenCodeViaDescription(t *testing.T) {
	cmd := installCommand{
		description: "opencode update via custom channel",
		args:        []string{"--version"},
	}
	// Description contains "opencode" so should match
	if !isOpenCodeNPMGlobalInstallCommand(ToolOpenCode, cmd) {
		t.Error("description-based detection should match opencode in description")
	}
}

// ---------------------------------------------------------------------------
// openCodeNPMGlobalVerificationDetail
// ---------------------------------------------------------------------------

func TestOpenCodeNPMGlobalVerificationDetail_WithStatusAndError(t *testing.T) {
	status := &CheckStatus{
		ExecutablePath: "/opt/npm/bin/opencode",
		Version:        "2.0.0",
	}
	candidates := []string{"/opt/npm/bin/opencode", "/opt/npm/opencode"}
	err := errors.New("candidate not executable")

	detail := openCodeNPMGlobalVerificationDetail(status, candidates, err)

	if !strings.Contains(detail, "npm global prefix/bin") {
		t.Errorf("detail should contain section header, got: %s", detail)
	}
	if !strings.Contains(detail, "path=/opt/npm/bin/opencode") {
		t.Errorf("detail should contain executable path, got: %s", detail)
	}
	if !strings.Contains(detail, "version=2.0.0") {
		t.Errorf("detail should contain version, got: %s", detail)
	}
	if !strings.Contains(detail, "candidates=") {
		t.Errorf("detail should contain candidates, got: %s", detail)
	}
	if !strings.Contains(detail, "error=") {
		t.Errorf("detail should contain error, got: %s", detail)
	}
}

func TestOpenCodeNPMGlobalVerificationDetail_NilStatusAndNoError(t *testing.T) {
	detail := openCodeNPMGlobalVerificationDetail(nil, nil, nil)
	if !strings.Contains(detail, "npm global prefix/bin") {
		t.Errorf("detail should contain section header even with nil status, got: %s", detail)
	}
}

// ---------------------------------------------------------------------------
// verifyOpenCodeNPMGlobalBinAfterCommand: version unchanged scenario
// ---------------------------------------------------------------------------

func TestUpdate_OpenCodeNPMGlobalBinAlsoOldVersion_StillFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldBinDir := filepath.Join(tmpDir, "old-bin")
	npmPrefix := filepath.Join(tmpDir, "npm-prefix")
	npmBinDir := filepath.Join(npmPrefix, "bin")
	if err := os.MkdirAll(npmBinDir, 0o755); err != nil {
		t.Fatalf("mkdir npm bin: %v", err)
	}
	if err := os.MkdirAll(oldBinDir, 0o755); err != nil {
		t.Fatalf("mkdir old bin: %v", err)
	}
	_ = writeTestExecutable(t, oldBinDir, "opencode")
	_ = writeTestExecutable(t, oldBinDir, "npm")
	_ = writeTestExecutable(t, npmBinDir, "opencode")
	t.Setenv("PATH", oldBinDir)

	// Runner: default verification sees old v1.0.0, npm prefix verification also sees v1.0.0
	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "opencode v1.0.0", err: nil},   // pre-check old PATH
		{stdout: "10.0.0", err: nil},            // npm probe
		{stdout: "2.0.0", err: nil},             // latest version enrichment
		{stdout: "changed 1 package", err: nil}, // npm install succeeds
		{stdout: "opencode v1.0.0", err: nil},   // default post-check still old
		{stdout: npmPrefix, err: nil},           // npm prefix -g
		{stdout: "opencode v1.0.0", err: nil},   // npm global bin candidate ALSO old version
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected failure when npm global bin also reports old version")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got: %+v", result)
	}
	// Error should mention the version unchanged context
	if !strings.Contains(strings.ToLower(result.Error), "unchanged") && !strings.Contains(strings.ToLower(result.Error), "verification") {
		t.Errorf("error should mention unchanged or verification failure, got: %s", result.Error)
	}
	// Error should include npm global prefix diagnostic info
	if !strings.Contains(result.Error, "npm global prefix") {
		t.Errorf("error should include npm global prefix diagnostic, got: %s", result.Error)
	}
}

func TestUpdate_OpenCodeNPMPrefixCommandFails_StillFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldBinDir := filepath.Join(tmpDir, "old-bin")
	if err := os.MkdirAll(oldBinDir, 0o755); err != nil {
		t.Fatalf("mkdir old bin: %v", err)
	}
	_ = writeTestExecutable(t, oldBinDir, "opencode")
	_ = writeTestExecutable(t, oldBinDir, "npm")
	t.Setenv("PATH", oldBinDir)

	// Runner: npm prefix -g returns error (broken npm config)
	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "opencode v1.0.0", err: nil},                         // pre-check
		{stdout: "10.0.0", err: nil},                                  // npm probe
		{stdout: "2.0.0", err: nil},                                   // latest version enrichment
		{stdout: "changed 1 package", err: nil},                       // npm install succeeds
		{stdout: "opencode v1.0.0", err: nil},                         // default post-check still old
		{stdout: "", err: errors.New("npm prefix -g failed: ENOENT")}, // npm prefix -g fails
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected failure when npm prefix -g fails")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got: %+v", result)
	}
	// Error should include npm global prefix failure diagnostic
	if !strings.Contains(result.Error, "npm global prefix") {
		t.Errorf("error should include npm global prefix diagnostic even on prefix failure, got: %s", result.Error)
	}
}

// ---------------------------------------------------------------------------
// Cross-tool guard: Codex update uses its own npm prefix diagnostics, not OpenCode-specific logic.
// ---------------------------------------------------------------------------

func TestUpdate_CodexUnchangedVersion_UsesCodexNPMPrefixDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "codex-cli v1.0.0", err: nil}, // pre-check
		{stdout: "10.0.0", err: nil},           // npm probe
		{stdout: "2.0.0", err: nil},            // latest version enrichment
		{stdout: "installed", err: nil},        // npm install succeeds
		{stdout: "codex-cli v1.0.0", err: nil}, // verify: unchanged
		{stdout: "1.0.0", err: nil},            // enrichment
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.installOrUpdateWithProgress(ToolCodex, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected error when Codex version unchanged")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Success {
		t.Error("expected failure")
	}
	if strings.Contains(result.Message, "OpenCode") {
		t.Errorf("Codex update should not mention OpenCode-specific npm global prefix logic, got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "Codex npm global prefix/bin") {
		t.Errorf("Codex update should include Codex-specific npm global prefix diagnostics, got: %s", result.Message)
	}
}
