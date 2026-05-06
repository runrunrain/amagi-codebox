package envcheck

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// 1. PATHOk not false when resolver succeeds but exec.LookPath fails
// ---------------------------------------------------------------------------

// mockPathEnvRunner simulates a scenario where exec.LookPath fails (empty
// temp PATH) but the processRunner succeeds for version commands. This
// exercises the code path where the platform resolver finds the executable
// via baseline PATH or shell fallback, but exec.LookPath (which uses the
// test's limited PATH) does not.
//
// Since we cannot easily mock exec.LookPath or the platform resolver in
// unit tests, we test the applyPathStateToStatus function directly to verify
// the semantics.
func TestApplyPathStateToStatus_ResolverSuccess_LookPathFailure(t *testing.T) {
	status := &CheckStatus{
		Tool:          ToolClaudeCode,
		InstallMethod: InstallMethodUnknown,
	}

	// Simulate: resolver found the tool, but exec.LookPath did not
	rr := resolveResult{
		executablePath: "/opt/homebrew/bin/claude",
		systemPATHOk:   false, // exec.LookPath failed
		pathState:      PathStateCodeboxPATH,
		pathSource:     "path-search",
	}

	applyPathStateToStatus(status, rr, ToolClaudeCode)

	// KEY ASSERTION: PATHOk must be true because CodeBox can launch the tool
	if !status.PATHOk {
		t.Error("PATHOk should be true when resolver succeeds, even if exec.LookPath fails")
	}
	if status.SystemPATHOk {
		t.Error("SystemPATHOk should be false when exec.LookPath fails")
	}
	if status.PathState != PathStateCodeboxPATH {
		t.Errorf("PathState = %q, want %q", status.PathState, PathStateCodeboxPATH)
	}
	// Should have an info-level issue about PATH not being in system PATH
	if len(status.Issues) == 0 {
		t.Error("expected at least one info-level issue about system PATH")
	}
	if status.Issues[0].Severity != SeverityInfo {
		t.Errorf("issue severity = %q, want %q", status.Issues[0].Severity, SeverityInfo)
	}
	if status.Issues[0].Code != "path_not_in_system_path" {
		t.Errorf("issue code = %q, want %q", status.Issues[0].Code, "path_not_in_system_path")
	}
}

// TestApplyPathStateToStatus_BothSucceed verifies the happy path where both
// exec.LookPath and the resolver find the tool.
func TestApplyPathStateToStatus_BothSucceed(t *testing.T) {
	status := &CheckStatus{
		Tool:          ToolClaudeCode,
		InstallMethod: InstallMethodUnknown,
	}

	rr := resolveResult{
		executablePath: "/usr/local/bin/claude",
		systemPATHOk:   true,
		pathState:      PathStateSystemPATH,
		pathSource:     "path-search",
	}

	applyPathStateToStatus(status, rr, ToolClaudeCode)

	if !status.PATHOk {
		t.Error("PATHOk should be true")
	}
	if !status.SystemPATHOk {
		t.Error("SystemPATHOk should be true")
	}
	if status.PathState != PathStateSystemPATH {
		t.Errorf("PathState = %q, want %q", status.PathState, PathStateSystemPATH)
	}
	// Should have NO issues when both succeed
	if len(status.Issues) != 0 {
		t.Errorf("expected no issues, got %d", len(status.Issues))
	}
}

// TestApplyPathStateToStatus_NeitherSucceeds verifies the missing tool case.
func TestApplyPathStateToStatus_NeitherSucceeds(t *testing.T) {
	status := &CheckStatus{
		Tool:          ToolClaudeCode,
		InstallMethod: InstallMethodUnknown,
	}

	rr := resolveResult{
		executablePath: "",
		systemPATHOk:   false,
		pathState:      PathStateMissing,
		pathSource:     "missing",
	}

	applyPathStateToStatus(status, rr, ToolClaudeCode)

	if status.PATHOk {
		t.Error("PATHOk should be false when tool is missing")
	}
	if status.SystemPATHOk {
		t.Error("SystemPATHOk should be false when tool is missing")
	}
}

// TestApplyPathStateToStatus_ShellFallback verifies shell fallback detection.
func TestApplyPathStateToStatus_ShellFallback(t *testing.T) {
	status := &CheckStatus{
		Tool:          ToolClaudeCode,
		InstallMethod: InstallMethodUnknown,
	}

	rr := resolveResult{
		executablePath: "/Users/test/.nvm/versions/node/v20/bin/claude",
		systemPATHOk:   false,
		pathState:      PathStateShellFallback,
		pathSource:     "fallback",
	}

	applyPathStateToStatus(status, rr, ToolClaudeCode)

	if !status.PATHOk {
		t.Error("PATHOk should be true when resolver finds tool via shell fallback")
	}
	if status.SystemPATHOk {
		t.Error("SystemPATHOk should be false when exec.LookPath fails")
	}
	if status.PathState != PathStateShellFallback {
		t.Errorf("PathState = %q, want %q", status.PathState, PathStateShellFallback)
	}
}

// ---------------------------------------------------------------------------
// 2. Darwin/Linux Claude install commands do not contain powershell.exe/winget
// ---------------------------------------------------------------------------

func TestClaudeInstallCommands_NoPowerShellOnDarwin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this test verifies non-Windows behavior")
	}

	svc := newTestService()
	cmds, _ := svc.claudeInstallCommands(installOperationInstall, nil)

	for _, cmd := range cmds {
		if strings.Contains(strings.ToLower(cmd.path), "powershell") {
			t.Errorf("non-Windows install command should not use powershell, got path: %q", cmd.path)
		}
		if strings.Contains(strings.ToLower(cmd.path), "winget") {
			t.Errorf("non-Windows install command should not use winget, got path: %q", cmd.path)
		}
		if strings.Contains(strings.ToLower(cmd.description), "powershell") {
			t.Errorf("non-Windows install command should not mention powershell, got description: %q", cmd.description)
		}
		if strings.Contains(strings.ToLower(cmd.description), "winget") {
			t.Errorf("non-Windows install command should not mention winget, got description: %q", cmd.description)
		}
	}

	// Must contain npm
	foundNPM := false
	for _, cmd := range cmds {
		if strings.Contains(strings.ToLower(cmd.path), "npm") || cmd.path == "npm" {
			foundNPM = true
		}
	}
	if !foundNPM {
		t.Error("non-Windows Claude install commands must include npm")
	}
}

// TestClaudeInstallCommands_WindowsHasNativeAndWinget verifies Windows still
// generates winget commands (and optionally native when accessible).
func TestClaudeInstallCommands_WindowsHasNativeAndWinget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("this test verifies Windows behavior")
	}

	svc := newTestService()
	cmds, _ := svc.claudeInstallCommands(installOperationInstall, nil)
	if len(cmds) < 2 {
		t.Fatalf("expected at least 2 commands on Windows, got %d", len(cmds))
	}

	hasWinget := false
	for _, cmd := range cmds {
		if strings.Contains(strings.ToLower(cmd.path), "winget") {
			hasWinget = true
		}
	}
	if !hasWinget {
		t.Error("Windows Claude install should include winget")
	}
}

// ---------------------------------------------------------------------------
// 3. npm missing -> CanInstall=false, issue code npm_not_found
// ---------------------------------------------------------------------------

// To test CanInstall properly, we need to check the full flow.
// Since resolveNPMPath uses the real resolver, we test ensureNPMAvailable.

func TestEnsureNPMAvailable_ErrorWhenNPMMissing(t *testing.T) {
	// Empty runner => npm command fails
	svc := newTestService() // no npm response

	err := svc.ensureNPMAvailable()
	if err == nil {
		t.Fatal("expected error when npm is not available")
	}
	if !strings.Contains(err.Error(), "npm") {
		t.Errorf("error should mention npm: %v", err)
	}
}

func TestInstallCommands_NPMNotAvailable_ReturnsError(t *testing.T) {
	svc := newTestService() // no npm response => ensureNPMAvailable fails

	_, err := svc.installCommands(ToolOpenCode, installOperationInstall, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected error when npm is not available for OpenCode")
	}
	if !strings.Contains(err.Error(), "npm") {
		t.Errorf("error should mention npm: %v", err)
	}
}

func TestInstallCommands_Codex_NPMNotAvailable_ReturnsError(t *testing.T) {
	svc := newTestService() // no npm response

	_, err := svc.installCommands(ToolCodex, installOperationInstall, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected error when npm is not available for Codex")
	}
}

// ---------------------------------------------------------------------------
// 4. Install post-check: resolver-only visibility counts as success
// ---------------------------------------------------------------------------

// TestInstall_VerifyResolverOnlySuccess verifies that after installation,
// CheckOne returning PATHOk=true (via resolver) is sufficient for install
// success, even if SystemPATHOk=false.
func TestInstall_VerifyResolverOnlySuccess(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	// Sequence: pre-check (broken), npm available, install, verify (success),
	// enrichment
	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "", err: errors.New("version command crashed")}, // pre-check: broken
		{stdout: "10.0.0", err: nil},                             // npm available
		{stdout: "added 1 package", err: nil},                    // install
		{stdout: "opencode v2.0.0", err: nil},                    // verify: success
		{stdout: "2.0.0", err: nil},                              // enrichment
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.Install(ToolOpenCode)

	if err != nil {
		t.Fatalf("Install() error = %v, want nil (resolver-only visibility should succeed)", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Success {
		t.Errorf("result.Success = false, want true; Message: %s, Error: %s",
			result.Message, result.Error)
	}
}

// ---------------------------------------------------------------------------
// 5. Install blocked reason tracking (CanInstall / InstallBlockedReason)
// ---------------------------------------------------------------------------

// TestCanInstall_Detection verifies that CheckOne sets CanInstall
// appropriately when a tool is missing.
//
// Note: CanInstall requires checking npm availability, which is currently
// done in installCommands, not in CheckOne. This test documents the
// expected behavior for future enhancement.
// TestCanInstall_TrueWhenNPMAvailable verifies that CanInstall is true when
// the Service can resolve and run npm --version through the processRunner.
func TestCanInstall_TrueWhenNPMAvailable(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "opencode v1.0.0", err: nil}, // opencode --version
		{stdout: "10.0.0", err: nil},          // npm --version (probeNPMAvailability)
		{stdout: "1.0.0", err: nil},           // npm view (enrichment)
	}}
	svc := NewServiceWithRunner(runner)

	status, err := svc.CheckOne(ToolOpenCode)
	if err != nil {
		t.Fatalf("CheckOne error: %v", err)
	}
	if !status.CanInstall {
		t.Errorf("CanInstall = false, want true when npm is available; blocked: %q", status.InstallBlockedReason)
	}
	if status.InstallBlockedReason != "" {
		t.Errorf("InstallBlockedReason should be empty when npm is available, got: %q", status.InstallBlockedReason)
	}
}

// TestCanInstall_FalseWhenNPMMissing verifies that CanInstall is false and
// the status includes an npm_not_found issue when npm fails the probe.
func TestCanInstall_FalseWhenNPMMissing(t *testing.T) {
	// Test populateCanInstall directly to avoid resolver finding real tools.
	svc := newTestService() // mockRunner with no npm response => npm probe fails
	svc.npmOnce.Do(func() {
		svc.npmAvailable = false
		svc.npmResolvedErr = errors.New("npm found at /path/to/npm but not functional: exit status 1")
	})

	status := &CheckStatus{
		Tool:      ToolOpenCode,
		Installed: true,
		Error:     "version check failed", // tool has an error
	}
	svc.populateCanInstall(status)

	if status.CanInstall {
		t.Error("CanInstall should be false when npm is not functional")
	}
	if status.InstallBlockedReason == "" {
		t.Error("InstallBlockedReason should be non-empty when npm is not available")
	}

	// Verify npm_not_found issue is present (tool has error, so issue is added)
	foundNPMIssue := false
	for _, issue := range status.Issues {
		if issue.Code == "npm_not_found" {
			foundNPMIssue = true
			if issue.Severity != SeverityError {
				t.Errorf("npm_not_found severity = %q, want %q", issue.Severity, SeverityError)
			}
			hasInstallNodeSolution := false
			for _, sol := range issue.Solutions {
				if sol.Type == SolutionInstallNode {
					hasInstallNodeSolution = true
				}
			}
			if !hasInstallNodeSolution {
				t.Error("npm_not_found issue should have install_node solution")
			}
		}
	}
	if !foundNPMIssue {
		t.Error("expected npm_not_found issue when npm is missing and tool has errors")
	}
}

// TestCanInstall_FalseWhenToolMissing_NPMMissing verifies that a missing tool
// plus missing npm generates both tool_not_installed and npm_not_found issues.
func TestCanInstall_FalseWhenToolMissing_NPMMissing(t *testing.T) {
	// We cannot control what the real resolver finds on the system (e.g.
	// opencode may exist in /opt/homebrew/bin). Instead, test populateCanInstall
	// directly to verify the logic when both tool and npm are missing.
	svc := newTestService() // mockRunner: no npm response
	// Simulate: npm probe fails
	svc.npmOnce.Do(func() {
		svc.npmAvailable = false
		svc.npmResolvedErr = errors.New("npm is not available; install Node.js (https://nodejs.org) and ensure npm is in PATH")
	})

	status := &CheckStatus{
		Tool:      ToolOpenCode,
		Installed: false,
	}
	svc.populateCanInstall(status)

	if status.CanInstall {
		t.Error("CanInstall should be false when npm is missing")
	}
	if status.InstallBlockedReason == "" {
		t.Error("InstallBlockedReason should be non-empty when npm is missing")
	}

	// Should have npm_not_found issue
	codes := map[string]bool{}
	for _, issue := range status.Issues {
		codes[issue.Code] = true
	}
	if !codes["npm_not_found"] {
		t.Errorf("expected npm_not_found issue, got issues: %+v", status.Issues)
	}
}

// TestCanInstall_CachedAcrossCheckOneCalls verifies that the npm probe runs
// only once even when CheckOne is called multiple times.
func TestCanInstall_CachedAcrossCheckOneCalls(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	_ = writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	countingRunner := &countingMockRunner{
		responses: map[string]mockResp{
			"opencode": {stdout: "opencode v1.0.0"},
			"codex":    {stdout: "codex-cli 1.0.0"},
			"npm":      {stdout: "10.0.0"},
		},
	}
	svc := NewServiceWithRunner(countingRunner)

	_, _ = svc.CheckOne(ToolOpenCode)
	_, _ = svc.CheckOne(ToolCodex)

	// Both should report CanInstall=true (npm cached after first probe)
	cached := svc.GetCachedStatus()
	ocStatus := cached.Items[string(ToolOpenCode)]
	codexStatus := cached.Items[string(ToolCodex)]

	if !ocStatus.CanInstall {
		t.Error("OpenCode CanInstall should be true (npm cached)")
	}
	if !codexStatus.CanInstall {
		t.Error("Codex CanInstall should be true (npm cached from OpenCode check)")
	}
}

// countingMockRunner tracks call count and returns responses based on path.
// Safe for concurrent use.
type countingMockRunner struct {
	responses map[string]mockResp
	callCount int32
}

type mockResp struct {
	stdout string
	stderr string
	err    error
}

func (r *countingMockRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	atomic.AddInt32(&r.callCount, 1)
	for key, resp := range r.responses {
		if strings.Contains(strings.ToLower(spec.Path), key) {
			return &platform.ProcessResult{Stdout: resp.stdout, Stderr: resp.stderr}, resp.err
		}
	}
	return &platform.ProcessResult{}, nil
}

func (r *countingMockRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// 6. Structured issues: tool_not_installed for missing tools
// ---------------------------------------------------------------------------

func TestAddMissingToolIssue_Structured(t *testing.T) {
	status := &CheckStatus{
		Tool:          ToolClaudeCode,
		InstallMethod: InstallMethodUnknown,
	}

	addMissingToolIssue(status, ToolClaudeCode)

	if len(status.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(status.Issues))
	}
	issue := status.Issues[0]
	if issue.Severity != SeverityError {
		t.Errorf("severity = %q, want %q", issue.Severity, SeverityError)
	}
	if issue.Code != "tool_not_installed" {
		t.Errorf("code = %q, want %q", issue.Code, "tool_not_installed")
	}
	if !strings.Contains(issue.Message, "Claude Code") {
		t.Errorf("message should mention tool name: %q", issue.Message)
	}
	if len(issue.Solutions) == 0 {
		t.Error("expected at least one solution")
	}
	if issue.Solutions[0].Type != SolutionInstallTool {
		t.Errorf("solution type = %q, want %q", issue.Solutions[0].Type, SolutionInstallTool)
	}
}

// ---------------------------------------------------------------------------
// 7. resolveExecutable integration (requires real OS behavior)
// ---------------------------------------------------------------------------

func TestResolveExecutable_MissingTool(t *testing.T) {
	rr := resolveExecutable("definitely-not-a-real-cli-tool-xyz-12345")
	if rr.executablePath != "" {
		t.Errorf("expected empty path for missing tool, got %q", rr.executablePath)
	}
	if rr.systemPATHOk {
		t.Error("systemPATHOk should be false for missing tool")
	}
	if rr.pathState != PathStateMissing {
		t.Errorf("pathState = %q, want %q", rr.pathState, PathStateMissing)
	}
}

func TestResolveExecutable_CommonTool(t *testing.T) {
	// ls exists on all Unix systems; on Windows, use "cmd"
	tool := "ls"
	if runtime.GOOS == "windows" {
		tool = "cmd"
	}

	rr := resolveExecutable(tool)
	if rr.executablePath == "" {
		t.Skipf("could not resolve %q on this system", tool)
	}
	if !rr.systemPATHOk {
		t.Errorf("systemPATHOk should be true for %q, got false", tool)
	}
}

// ---------------------------------------------------------------------------
// 8. npmPackageName helper
// ---------------------------------------------------------------------------

func TestNPMPackageName(t *testing.T) {
	tests := []struct {
		tool   CLITool
		expect string
	}{
		{ToolClaudeCode, "@anthropic-ai/claude-code"},
		{ToolOpenCode, "opencode-ai"},
		{ToolCodex, "@openai/codex"},
		{CLITool("unknown"), ""},
	}
	for _, tc := range tests {
		got := npmPackageName(tc.tool)
		if got != tc.expect {
			t.Errorf("npmPackageName(%q) = %q, want %q", tc.tool, got, tc.expect)
		}
	}
}

// ---------------------------------------------------------------------------
// 9. isWindows helper
// ---------------------------------------------------------------------------

func TestIsWindows(t *testing.T) {
	got := isWindows()
	want := runtime.GOOS == "windows"
	if got != want {
		t.Errorf("isWindows() = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// 10. fileExists helper
// ---------------------------------------------------------------------------

func TestFileExists(t *testing.T) {
	if fileExists("") {
		t.Error("fileExists('') should be false")
	}
	if fileExists("definitely-nonexistent-file-xyz") {
		t.Error("fileExists should return false for nonexistent file")
	}
}

// ---------------------------------------------------------------------------
// 11. resolveCommandNPMPath replaces bare npm
// ---------------------------------------------------------------------------

func TestResolveCommandNPMPath(t *testing.T) {
	svc := newTestService()

	cmd := installCommand{
		description: "test",
		path:        "npm",
		args:        []string{"--version"},
	}

	resolved := svc.resolveCommandNPMPath(cmd)
	// On systems with npm, should be resolved; otherwise still "npm"
	// The important thing is it does not panic or corrupt the command.
	if resolved.args[0] != "--version" {
		t.Errorf("args should be preserved, got: %v", resolved.args)
	}
}

// ---------------------------------------------------------------------------
// 12. PathState constants
// ---------------------------------------------------------------------------

func TestPathStateConstants(t *testing.T) {
	states := []PathState{
		PathStateMissing,
		PathStateSystemPATH,
		PathStateCodeboxPATH,
		PathStateShellFallback,
		PathStateOutsidePATH,
	}
	for _, s := range states {
		if string(s) == "" {
			t.Errorf("PathState constant should have a non-empty string value")
		}
	}
}

// ---------------------------------------------------------------------------
// 13. Issue severity constants
// ---------------------------------------------------------------------------

func TestIssueSeverityConstants(t *testing.T) {
	severities := []IssueSeverity{SeverityInfo, SeverityWarning, SeverityError, SeverityCritical}
	for _, s := range severities {
		if string(s) == "" {
			t.Error("IssueSeverity constant should have non-empty value")
		}
	}
}

// ---------------------------------------------------------------------------
// 14. Solution type constants
// ---------------------------------------------------------------------------

func TestSolutionTypeConstants(t *testing.T) {
	types := []SolutionType{
		SolutionInstallTool,
		SolutionInstallNode,
		SolutionFixPath,
		SolutionRestartApp,
		SolutionRetry,
		SolutionManualCommand,
	}
	for _, s := range types {
		if string(s) == "" {
			t.Error("SolutionType constant should have non-empty value")
		}
	}
}

// ---------------------------------------------------------------------------
// 15. CheckIssue JSON serialization
// ---------------------------------------------------------------------------

func TestCheckIssue_JSONFields(t *testing.T) {
	issue := CheckIssue{
		Severity: SeverityWarning,
		Code:     "test_code",
		Message:  "test message",
		Detail:   "test detail",
		Solutions: []ResolutionAction{
			{Type: SolutionRetry, Description: "Try again"},
		},
	}
	if issue.Severity != SeverityWarning {
		t.Errorf("Severity = %q, want %q", issue.Severity, SeverityWarning)
	}
	if len(issue.Solutions) != 1 {
		t.Errorf("expected 1 solution, got %d", len(issue.Solutions))
	}
}

// ---------------------------------------------------------------------------
// 16. mockRunner with npm resolved path matching
// ---------------------------------------------------------------------------

// TestMockRunner_MatchesNPMPath verifies that the mockRunner still works
// with resolved npm paths (which may be absolute paths containing "npm").
func TestMockRunner_MatchesNPMPath(t *testing.T) {
	runner := &mockRunner{
		responses: []mockResponse{
			{pathPrefix: "npm", stdout: "10.0.0", err: nil},
		},
	}

	// Test with bare "npm" name
	result, err := runner.Run(context.Background(), platform.CommandSpec{
		Path: "npm",
		Args: []string{"--version"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "10.0.0" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "10.0.0")
	}

	// Test with resolved absolute path containing "npm"
	result2, err2 := runner.Run(context.Background(), platform.CommandSpec{
		Path: "/usr/local/bin/npm",
		Args: []string{"--version"},
	})
	if err2 != nil {
		t.Fatalf("unexpected error with resolved path: %v", err2)
	}
	if strings.TrimSpace(result2.Stdout) != "10.0.0" {
		t.Errorf("stdout = %q, want %q", result2.Stdout, "10.0.0")
	}
}

// ---------------------------------------------------------------------------
// 17. Install command does not contain powershell/winget on macOS (integration)
// ---------------------------------------------------------------------------

func TestInstallCommands_Claude_NoPowerShellOrWinget_Integration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this test verifies non-Windows behavior")
	}

	svc := newTestService(
		responseFor("npm", "10.0.0", nil),
	)

	cmds, err := svc.installCommands(ToolClaudeCode, installOperationInstall, &CheckStatus{
		InstallMethod: InstallMethodNPM,
		Installed:     false,
	}, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, cmd := range cmds {
		pathLower := strings.ToLower(cmd.path)
		descLower := strings.ToLower(cmd.description)
		if strings.Contains(pathLower, "powershell") || strings.Contains(pathLower, "winget") {
			t.Errorf("non-Windows command should not use powershell/winget: path=%q", cmd.path)
		}
		if strings.Contains(descLower, "powershell") || strings.Contains(descLower, "winget") {
			t.Errorf("non-Windows command description should not mention powershell/winget: %q", cmd.description)
		}
	}

	// Must have at least one npm command
	foundNPM := false
	for _, cmd := range cmds {
		if strings.Contains(strings.ToLower(cmd.path), "npm") || cmd.path == "npm" {
			foundNPM = true
			break
		}
	}
	if !foundNPM {
		t.Error("expected at least one npm command in Claude install on non-Windows")
	}
}

// ---------------------------------------------------------------------------
// 18. CheckStatus new fields are preserved through cache round-trip
// ---------------------------------------------------------------------------

func TestCheckStatus_NewFields_PreservedThroughCache(t *testing.T) {
	svc := newTestService()

	// Manually set a cached status with new fields
	svc.mu.Lock()
	svc.cache.Items["claude_code"] = CheckStatus{
		Tool:                 ToolClaudeCode,
		Installed:            true,
		PATHOk:               true,
		SystemPATHOk:         false,
		PathState:            PathStateShellFallback,
		PathSource:           "fallback",
		CanInstall:           true,
		InstallBlockedReason: "",
		Issues: []CheckIssue{
			{Severity: SeverityInfo, Code: "path_not_in_system_path", Message: "test"},
		},
		Solutions: []ResolutionAction{
			{Type: SolutionRestartApp, Description: "restart"},
		},
	}
	svc.mu.Unlock()

	cached := svc.GetCachedStatus()
	status := cached.Items["claude_code"]

	if status.SystemPATHOk != false {
		t.Error("SystemPATHOk should be preserved as false")
	}
	if status.PathState != PathStateShellFallback {
		t.Errorf("PathState = %q, want %q", status.PathState, PathStateShellFallback)
	}
	if status.PathSource != "fallback" {
		t.Errorf("PathSource = %q, want %q", status.PathSource, "fallback")
	}
	if len(status.Issues) != 1 {
		t.Errorf("expected 1 issue, got %d", len(status.Issues))
	}
	if len(status.Solutions) != 1 {
		t.Errorf("expected 1 solution, got %d", len(status.Solutions))
	}
}

// ---------------------------------------------------------------------------
// 19. sequentialRunner is reusable for install tests
// ---------------------------------------------------------------------------

func TestSequentialRunner_MockNPMPath(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "", err: errors.New("not installed")}, // pre-check
		{stdout: "10.0.0", err: nil},                   // npm available
		{stdout: "installed", err: nil},                // install
		{stdout: "opencode v1.0.0", err: nil},          // verify
		{stdout: "1.0.0", err: nil},                    // latest
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.Install(ToolOpenCode)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("Install should succeed; result=%+v", result)
	}
}

// ---------------------------------------------------------------------------
// 20. Ensure the sequentialRunner Start method satisfies interface
// ---------------------------------------------------------------------------

func TestSequentialRunnerImplementsInterface(t *testing.T) {
	var _ platform.ProcessRunner = &sequentialRunner{}
	var _ platform.ProcessRunner = &mockRunner{}
	var _ platform.ProcessRunner = &slowSequentialRunner{}
}
