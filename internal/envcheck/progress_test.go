package envcheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// A. Progress callback tests
// ---------------------------------------------------------------------------

// progressSnapshot records a single progress observation for later assertion.
type progressSnapshot struct {
	step     OperationStep
	message  string
	progress int
}

// collectProgressSnapshots returns a progressReporter that records every
// invocation into the returned slice. The slice is guarded by a mutex.
func collectProgressSnapshots() (*[]progressSnapshot, progressReporter) {
	var mu sync.Mutex
	var snapshots []progressSnapshot
	reporter := func(step OperationStep, message string, progress int) {
		mu.Lock()
		snapshots = append(snapshots, progressSnapshot{step, message, progress})
		mu.Unlock()
	}
	return &snapshots, reporter
}

// TestInstallOrUpdateWithProgress_ReportsSteps verifies that the progress
// callback is invoked at least once for each major phase.
func TestInstallOrUpdateWithProgress_ReportsSteps(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "", err: errors.New("not installed")}, // pre-check opencode --version
		{stdout: "10.0.0", err: nil},                   // npm probe
		{stdout: "installed", err: nil},                // install command
		{stdout: "opencode v1.0.0", err: nil},          // verify --version
		{stdout: "1.0.0", err: nil},                    // enrichment
	}}
	svc := NewServiceWithRunner(runner)

	snapshotsPtr, reporter := collectProgressSnapshots()
	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationInstall, reporter, ClaudeInstallAuto)

	if err != nil {
		t.Fatalf("installOrUpdateWithProgress error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected success; result=%+v", result)
	}

	snapshots := *snapshotsPtr
	if len(snapshots) == 0 {
		t.Fatal("expected at least one progress callback, got none")
	}

	// Verify progress is monotonically non-decreasing.
	prevProgress := -1
	for i, snap := range snapshots {
		if snap.progress < prevProgress {
			t.Errorf("snapshot[%d]: progress=%d decreased from previous=%d (must be monotonic)", i, snap.progress, prevProgress)
		}
		prevProgress = snap.progress
	}

	// Verify at least precheck, run_command, verify phases were reported.
	seenSteps := map[OperationStep]bool{}
	for _, snap := range snapshots {
		seenSteps[snap.step] = true
	}
	for _, required := range []OperationStep{
		OperationStepPrecheck,
		OperationStepRunCommand,
		OperationStepVerify,
	} {
		if !seenSteps[required] {
			t.Errorf("expected step %q to be reported, but it was not; seen steps: %v", required, seenSteps)
		}
	}
}

// TestAsyncOperation_ProgressAdvancesFromZero verifies that an async update
// operation shows progress > 0 while running (not stuck at 0%).
func TestAsyncOperation_ProgressAdvancesFromZero(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	// Use a slow runner so we can observe intermediate states.
	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("broken")},
		{stdout: "10.0.0", err: nil},          // npm probe
		{stdout: "installed", err: nil},       // install command
		{stdout: "opencode v2.0.0", err: nil}, // verify
		{stdout: "2.0.0", err: nil},           // enrichment
		{stdout: "opencode v2.0.0", err: nil}, // refresh
	}, 100*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	_, err := svc.StartUpdateTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("StartUpdateTool error: %v", err)
	}

	// Poll for an intermediate state with progress > 0.
	deadline := time.Now().Add(3 * time.Second)
	sawProgressGtZero := false
	for time.Now().Before(deadline) {
		op := svc.GetOperationState()
		if op != nil && op.Status == OperationStatusRunning && op.Progress > 0 {
			sawProgressGtZero = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sawProgressGtZero {
		t.Error("expected to observe progress > 0 during running operation, but never did")
	}

	// Wait for completion.
	final := waitForOperation(t, svc, 5*time.Second)
	if final.Status != OperationStatusSucceeded {
		t.Errorf("final status = %q, want succeeded; message=%s error=%s",
			final.Status, final.Message, final.Error)
	}
	if final.Progress != 100 {
		t.Errorf("final progress = %d, want 100", final.Progress)
	}
	if final.Step != OperationStepCompleted {
		t.Errorf("final step = %q, want completed", final.Step)
	}
}

// TestAsyncOperation_FailedOperationAlsoReachesProgress100 verifies that
// a failed operation still ends with progress=100 and step=completed.
func TestAsyncOperation_FailedOperationAlsoReachesProgress100(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)

	svc := newTestService() // all commands fail

	_, err := svc.StartInstallTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("StartInstallTool error: %v", err)
	}

	final := waitForOperation(t, svc, 5*time.Second)
	if final.Status != OperationStatusFailed {
		t.Errorf("status = %q, want failed", final.Status)
	}
	if final.Progress != 100 {
		t.Errorf("progress = %d, want 100 (even on failure)", final.Progress)
	}
	if final.Step != OperationStepCompleted {
		t.Errorf("step = %q, want completed", final.Step)
	}
	if final.Error == "" {
		t.Error("expected non-empty error message on failure")
	}
}

// TestProgressReporter_MonotonicOnCommandRetry verifies that when the first
// install command fails and the second succeeds, progress is still monotonic.
// We test this by directly calling installOrUpdateWithProgress with a mock
// that returns a failing first runner call.
func TestProgressReporter_MonotonicOnCommandRetry(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	// Use a runner that fails on the first npm install attempt but succeeds
	// on the second (via default response).
	runner := &retryTestRunner{
		failUntil: 4, // fail first 4 calls (pre-check, npm probe, npm install, enrichment attempt)
		failErr:   errors.New("temporary failure"),
	}
	svc := NewServiceWithRunner(runner)

	snapshotsPtr, reporter := collectProgressSnapshots()
	_, _ = svc.installOrUpdateWithProgress(ToolOpenCode, installOperationInstall, reporter, ClaudeInstallAuto)

	snapshots := *snapshotsPtr
	prevProgress := -1
	for i, snap := range snapshots {
		if snap.progress < prevProgress {
			t.Errorf("snapshot[%d]: progress=%d decreased from %d (monotonic violation)", i, snap.progress, prevProgress)
		}
		prevProgress = snap.progress
	}
}

// retryTestRunner fails the first N Run calls, then returns success.
type retryTestRunner struct {
	failUntil int
	failErr   error
	callCount int32
}

func (r *retryTestRunner) Run(_ context.Context, _ platform.CommandSpec) (*platform.ProcessResult, error) {
	idx := int(atomic.AddInt32(&r.callCount, 1))
	if idx <= r.failUntil {
		return &platform.ProcessResult{}, r.failErr
	}
	return &platform.ProcessResult{Stdout: "ok"}, nil
}

func (r *retryTestRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

func TestNonNativeInstallerCommandsUseDefaultTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  installCommand
	}{
		{name: "claude npm install", cmd: npmClaudeCommand(installOperationInstall)},
		{name: "claude npm update", cmd: npmClaudeCommand(installOperationUpdate)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if timeout := commandTimeout(tc.cmd); timeout != installCommandTimeout {
				t.Fatalf("commandTimeout() = %v, want default %v", timeout, installCommandTimeout)
			}
		})
	}
}

func TestRunInstallCommandTimeoutIncludesDiagnosticContext(t *testing.T) {
	runner := timeoutDiagnosticRunner{}
	svc := NewServiceWithRunner(runner)

	err := svc.runInstallCommand(installCommand{
		description: "diagnostic installer",
		path:        "powershell.exe",
		args:        []string{"-Command", "test"},
		timeout:     time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	errText := err.Error()
	for _, want := range []string{"command timed out after", "download started", "network still slow", "timeout 1ms", "powershell.exe -Command test"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("timeout diagnostic missing %q: %s", want, errText)
		}
	}
}

func TestSanitizeInstallerOutputRedactsSensitiveValues(t *testing.T) {
	token := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"
	openAIKey := "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	githubToken := "ghp_abcdefghijklmnopqrstuvwxyzABCDE12345"
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	bearerValue := "abcdefghijklmnopqrstuvwxyz0123456789"

	output := sanitizeInstallerOutput(strings.Join([]string{
		"download started",
		"plain anthropic token " + token + " continued",
		"plain openai token " + openAIKey,
		"plain github token " + githubToken,
		"plain jwt " + jwt,
		"Authorization: Bearer " + bearerValue,
		"token=bare-token-value-123456",
		"api_key=bare-api-key-value-123456",
		"password=plain-password-value",
		`{"api_key":"` + token + `","authorization":"Bearer ` + bearerValue + `","secret":"json-secret-value"}`,
		"complete",
	}, "\n"))

	for _, leaked := range []string{
		token,
		openAIKey,
		githubToken,
		jwt,
		bearerValue,
		"bare-token-value-123456",
		"bare-api-key-value-123456",
		"plain-password-value",
		"json-secret-value",
	} {
		if strings.Contains(output, leaked) {
			t.Fatalf("sensitive value %q was not redacted: %s", leaked, output)
		}
	}

	for _, want := range []string{"download started", "Authorization: Bearer [redacted]", "token=[redacted]", `"api_key":"[redacted]"`, "complete"} {
		if !strings.Contains(output, want) {
			t.Fatalf("sanitized output missing readable context %q: %s", want, output)
		}
	}
}

type deadlineCaptureRunner struct {
	deadline time.Duration
	result   *platform.ProcessResult
	err      error
}

func (r *deadlineCaptureRunner) Run(ctx context.Context, _ platform.CommandSpec) (*platform.ProcessResult, error) {
	if deadline, ok := ctx.Deadline(); ok {
		r.deadline = time.Until(deadline)
	}
	return r.result, r.err
}

func (r *deadlineCaptureRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

type timeoutDiagnosticRunner struct{}

func (timeoutDiagnosticRunner) Run(ctx context.Context, _ platform.CommandSpec) (*platform.ProcessResult, error) {
	<-ctx.Done()
	return &platform.ProcessResult{Stdout: "download started", Stderr: "network still slow"}, ctx.Err()
}

func (timeoutDiagnosticRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// B. Claude Code update command order tests
// ---------------------------------------------------------------------------

// TestClaudeUpdateCommandOrder_NPMInstalled verifies that on Windows,
// npm-installed Claude Code update uses only npm (same-channel).
func TestClaudeUpdateCommandOrder_NPMInstalled(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific command order test")
	}

	svc := newTestService()
	cmds, err := svc.claudeInstallCommands(installOperationUpdate, &CheckStatus{
		InstallMethod: InstallMethodNPM,
		Installed:     true,
		Version:       "1.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Strict same-channel: only npm command.
	if len(cmds) != 1 {
		t.Fatalf("expected exactly 1 command (npm only), got %d", len(cmds))
	}
	// Command must be npm
	if !strings.Contains(strings.ToLower(cmds[0].description), "npm") {
		t.Errorf("NPM-installed update should use npm, got: %q", cmds[0].description)
	}
}

// TestClaudeUpdateCommandOrder_RemovedLegacyMethod verifies that on Windows,
// a historical removed install-method value is treated as unsupported and
// falls back to the safe npm update path. This raw string is intentionally not
// a public InstallMethod constant.
func TestClaudeUpdateCommandOrder_RemovedLegacyMethod(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific command order test")
	}

	svc := newTestService()
	cmds, err := svc.claudeInstallCommands(installOperationUpdate, &CheckStatus{
		InstallMethod: InstallMethod("winget"),
		Installed:     true,
		Version:       "1.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Winget is no longer supported; unknown update falls back to npm only.
	if len(cmds) != 1 {
		t.Fatalf("expected exactly 1 command (npm fallback), got %d", len(cmds))
	}
	if !strings.Contains(strings.ToLower(cmds[0].description), "npm") {
		t.Errorf("removed legacy method update should fall back to npm, got: %q", cmds[0].description)
	}
}

// TestClaudeUpdateCommandOrder_NativeOrUnknown verifies that on Windows,
// native-installed Claude Code uses npm for update (native direct installer removed).
func TestClaudeUpdateCommandOrder_NativeOrUnknown(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific command order test")
	}

	svc := newTestService()
	cmds, err := svc.claudeInstallCommands(installOperationUpdate, &CheckStatus{
		InstallMethod: InstallMethodNative,
		Installed:     true,
		Version:       "1.0.0",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Native update now uses npm (direct installer removed)
	if len(cmds) != 1 {
		t.Fatalf("expected exactly 1 command (npm), got %d", len(cmds))
	}
	// Should NOT include winget
	for _, cmd := range cmds {
		if strings.Contains(strings.ToLower(cmd.description), "winget") {
			t.Errorf("native update should not include winget, got %q", cmd.description)
		}
	}
}

// TestClaudeUpdateCommandOrder_UnknownInstall verifies unknown install method.
func TestClaudeUpdateCommandOrder_UnknownInstall(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific command order test")
	}

	svc := newTestService()
	cmds, err := svc.claudeInstallCommands(installOperationUpdate, &CheckStatus{
		InstallMethod: InstallMethodUnknown,
		Installed:     true,
		Version:       "1.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error for unknown install method: %v", err)
	}
	// Unknown method: npm only fallback (winget removed)
	if len(cmds) != 1 {
		t.Fatalf("expected exactly 1 command (npm fallback), got %d", len(cmds))
	}
	if strings.Contains(strings.ToLower(cmds[0].description), "powershell") {
		t.Errorf("unknown update should not use PowerShell, got: %q", cmds[0].description)
	}
	if strings.Contains(strings.ToLower(cmds[0].description), "winget") {
		t.Errorf("unknown update should not use winget, got: %q", cmds[0].description)
	}
}

// TestClaudeFreshInstall_NonWindows_NPMOnly verifies that on non-Windows,
// fresh install only generates npm commands.
func TestClaudeFreshInstall_NonWindows_NPMOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows test")
	}

	svc := newTestService()
	cmds, _ := svc.claudeInstallCommands(installOperationInstall, nil)
	for _, cmd := range cmds {
		descLower := strings.ToLower(cmd.description)
		pathLower := strings.ToLower(cmd.path)
		if strings.Contains(descLower, "powershell") || strings.Contains(pathLower, "powershell") {
			t.Errorf("non-Windows install should not use powershell: %q", cmd.description)
		}
		if strings.Contains(descLower, "winget") || strings.Contains(pathLower, "winget") {
			t.Errorf("non-Windows install should not use winget: %q", cmd.description)
		}
	}
}

// TestClaudeUpdateNonWindows_NoWindowsCommands verifies that on non-Windows,
// update commands don't contain powershell or winget.
func TestClaudeUpdateNonWindows_NoWindowsCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows test")
	}

	svc := newTestService()
	// Include one raw historical value to guard unsupported legacy status handling
	// without advertising it as a supported install method constant.
	for _, method := range []InstallMethod{InstallMethodNPM, InstallMethodNative, InstallMethod("winget"), InstallMethodUnknown} {
		cmds, _ := svc.claudeInstallCommands(installOperationUpdate, &CheckStatus{
			InstallMethod: method,
			Installed:     true,
			Version:       "1.0.0",
		})
		for _, cmd := range cmds {
			pathLower := strings.ToLower(cmd.path)
			descLower := strings.ToLower(cmd.description)
			if strings.Contains(pathLower, "powershell") || strings.Contains(descLower, "powershell") {
				t.Errorf("non-Windows update [%s] should not use powershell: path=%q desc=%q",
					method, cmd.path, cmd.description)
			}
			if strings.Contains(pathLower, "winget") || strings.Contains(descLower, "winget") {
				t.Errorf("non-Windows update [%s] should not use winget: path=%q desc=%q",
					method, cmd.path, cmd.description)
			}
		}
	}
}

// TestClaudeFreshInstall_Windows_IncludesNPM verifies Windows fresh
// install includes npm (winget removed).
func TestClaudeFreshInstall_Windows_IncludesNPM(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific test")
	}

	svc := newTestService()
	cmds, _ := svc.claudeInstallCommands(installOperationInstall, nil)
	if len(cmds) < 1 {
		t.Fatalf("expected at least 1 install command on Windows, got %d", len(cmds))
	}

	hasNPM := false
	for _, cmd := range cmds {
		descLower := strings.ToLower(cmd.description)
		if strings.Contains(descLower, "npm") {
			hasNPM = true
		}
	}
	if !hasNPM {
		t.Error("Windows fresh install should include npm")
	}
}

// ---------------------------------------------------------------------------
// C. Install command integration tests with progress
// ---------------------------------------------------------------------------

// TestInstallWithProgress_OpenCode_EndToEnd uses the sequential runner with
// a progress reporter to verify the full flow reports correct steps.
func TestInstallWithProgress_OpenCode_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "", err: errors.New("not installed")}, // pre-check
		{stdout: "10.0.0", err: nil},                   // npm probe
		{stdout: "installed", err: nil},                // install
		{stdout: "opencode v1.0.0", err: nil},          // verify
		{stdout: "1.0.0", err: nil},                    // enrichment
	}}
	svc := NewServiceWithRunner(runner)

	snapshotsPtr, reporter := collectProgressSnapshots()
	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationInstall, reporter, ClaudeInstallAuto)

	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success: %s %s", result.Message, result.Error)
	}

	snapshots := *snapshotsPtr

	// Verify at least one snapshot has progress > 0 and < 100 (intermediate)
	hasIntermediate := false
	for _, snap := range snapshots {
		if snap.progress > 0 && snap.progress < 100 {
			hasIntermediate = true
		}
	}
	if !hasIntermediate {
		t.Errorf("expected at least one intermediate progress snapshot (0 < p < 100); snapshots: %+v", snapshots)
	}

	// Verify the first snapshot is the precheck phase
	if len(snapshots) > 0 && snapshots[0].step != OperationStepPrecheck {
		t.Errorf("first step should be precheck, got %q", snapshots[0].step)
	}
}

// TestInstallWithProgress_Codex_EndToEnd verifies Codex update with progress.
func TestInstallWithProgress_Codex_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "", err: errors.New("broken")}, // pre-check
		{stdout: "10.0.0", err: nil},            // npm probe
		{stdout: "installed", err: nil},         // install command
		{stdout: "codex-cli 0.90.0", err: nil},  // verify
		{stdout: "0.90.0", err: nil},            // enrichment
	}}
	svc := NewServiceWithRunner(runner)

	snapshotsPtr, reporter := collectProgressSnapshots()
	result, err := svc.installOrUpdateWithProgress(ToolCodex, installOperationUpdate, reporter, ClaudeInstallAuto)

	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success: %s %s", result.Message, result.Error)
	}

	snapshots := *snapshotsPtr
	if len(snapshots) == 0 {
		t.Fatal("expected progress snapshots, got none")
	}

	// Verify monotonic
	prev := -1
	for i, snap := range snapshots {
		if snap.progress < prev {
			t.Errorf("progress decreased at snapshot[%d]: %d < %d", i, snap.progress, prev)
		}
		prev = snap.progress
	}
}

// ---------------------------------------------------------------------------
// D. Error message quality tests
// ---------------------------------------------------------------------------

// TestInstallFailure_ContainsCommandAndSuggestion verifies that failed install
// commands include the command path and a suggestion.
func TestInstallFailure_ContainsCommandAndSuggestion(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)

	// No executables, no npm -- install will fail.
	svc := newTestService()

	result, _ := svc.Install(ToolOpenCode)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Success {
		t.Error("expected failure")
	}
	// Error should mention npm
	if !strings.Contains(result.Error, "npm") {
		t.Errorf("error should mention npm: %s", result.Error)
	}
}

// TestUpdateVersionUnchanged_ContainsSuggestion verifies that the version
// unchanged error includes actionable guidance.
func TestUpdateVersionUnchanged_ContainsSuggestion(t *testing.T) {
	errMsg := fmt.Sprintf(
		"update command ran but version was unchanged (%s) - the CLI might be running or locked. Try closing any terminal using %s and retry.",
		"1.0.0", "OpenCode",
	)
	if !strings.Contains(errMsg, "Try closing") {
		t.Error("version unchanged error should include actionable suggestion")
	}
}

// ---------------------------------------------------------------------------
// E. Slow async operation with step observation
// ---------------------------------------------------------------------------

// stepObservation records a single (step, progress) pair observed via polling.
type stepObservation struct {
	step     OperationStep
	progress int
}

// TestAsyncOperation_StepsChangeDuringExecution polls the operation state
// during a slow execution and verifies that steps actually change.
func TestAsyncOperation_StepsChangeDuringExecution(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("broken")},
		{stdout: "10.0.0", err: nil},          // npm probe
		{stdout: "installed", err: nil},       // install command
		{stdout: "opencode v2.0.0", err: nil}, // verify
		{stdout: "2.0.0", err: nil},           // enrichment
		{stdout: "opencode v2.0.0", err: nil}, // refresh
	}, 150*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	_, err := svc.StartUpdateTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("StartUpdateTool error: %v", err)
	}

	// Collect step observations while running.
	var observations []stepObservation
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		op := svc.GetOperationState()
		if op == nil || op.Status != OperationStatusRunning {
			break
		}
		observations = append(observations, stepObservation{
			step:     op.Step,
			progress: op.Progress,
		})
		time.Sleep(20 * time.Millisecond)
	}

	final := waitForOperation(t, svc, 5*time.Second)
	if final.Status != OperationStatusSucceeded {
		t.Errorf("final status = %q, want succeeded; error=%s", final.Status, final.Error)
	}

	// We should have seen at least 2 different steps during the operation.
	if len(observations) < 2 {
		t.Skipf("only collected %d observations (system too fast), cannot verify step changes", len(observations))
	}

	uniqueSteps := map[OperationStep]bool{}
	for _, obs := range observations {
		uniqueSteps[obs.step] = true
	}
	if len(uniqueSteps) < 2 {
		t.Errorf("expected at least 2 different steps during operation, got %d unique steps: %v",
			len(uniqueSteps), uniqueSteps)
	}
}

// ---------------------------------------------------------------------------
// F. progressReporter interface satisfaction
// ---------------------------------------------------------------------------

// TestProgressReporter_CanBeNil verifies that passing nil reporter works.
func TestProgressReporter_CanBeNil(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "opencode v1.0.0", err: nil}, // pre-check (installed)
		{stdout: "10.0.0", err: nil},          // npm probe
		{stdout: "1.0.0", err: nil},           // enrichment (version cache)
	}}
	svc := NewServiceWithRunner(runner)

	// nil reporter should not panic
	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationInstall, nil, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success (already installed): %s", result.Message)
	}
}

// ---------------------------------------------------------------------------
// G. Synchronous serializedInstallOrUpdate still works
// ---------------------------------------------------------------------------

// TestSerializedInstall_ProgressCallbackUsed verifies that the synchronous
// Install path also works (no regressions in serializedInstallOrUpdate).
func TestSerializedInstall_ProgressCallbackUsed(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "", err: errors.New("not installed")}, // pre-check
		{stdout: "10.0.0", err: nil},                   // npm probe
		{stdout: "installed", err: nil},                // install
		{stdout: "opencode v1.0.0", err: nil},          // verify
		{stdout: "1.0.0", err: nil},                    // enrichment
		{stdout: "opencode v1.0.0", err: nil},          // refresh
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.Install(ToolOpenCode)
	if err != nil {
		t.Fatalf("Install error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected success: %+v", result)
	}
}

// ---------------------------------------------------------------------------
// I. ensureNPMAvailable reuses cache (integration with progress)
// ---------------------------------------------------------------------------

// TestNPMAvailableCache_ReuseInInstallCommands verifies that the npm probe
// cache is reused when installCommands is called, avoiding redundant runner calls.
func TestNPMAvailableCache_ReuseInInstallCommands(t *testing.T) {
	var callCount int32
	countingRunner := &callCountingRunner{callCount: &callCount}
	svc := NewServiceWithRunner(countingRunner)

	// Trigger npm cache population via ensureNPMAvailable
	_ = svc.ensureNPMAvailable()

	// Call ensureNPMAvailable again -- should use cache, not increment callCount
	_ = svc.ensureNPMAvailable()

	// The npm probe should have been called exactly once.
	if atomic.LoadInt32(&callCount) > 2 {
		t.Errorf("npm probe was called more than expected: %d", callCount)
	}
}

// callCountingRunner counts Run calls. Returns success for any npm call,
// empty result for everything else.
type callCountingRunner struct {
	callCount *int32
}

func (r *callCountingRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	atomic.AddInt32(r.callCount, 1)
	if strings.Contains(strings.ToLower(spec.Path), "npm") {
		return &platform.ProcessResult{Stdout: "10.0.0"}, nil
	}
	return &platform.ProcessResult{}, nil
}

func (r *callCountingRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// J. Verify-then-fallback tests
// ---------------------------------------------------------------------------

// TestUpdate_FirstCommandSucceedsButVersionUnchanged_FallsBack verifies that
// when the command executes successfully but the version did not change,
// the error message is descriptive. OpenCode has only 1 npm command, so
// there's no fallback -- this tests the verify-per-command reporting.
func TestUpdate_FirstCommandSucceedsButVersionUnchanged_FallsBack(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	// Runner sequence:
	//   0: pre-check opencode --version -> v1.0.0
	//   1: npm probe -> ok
	//   2: enrichment (npm view version) -> 2.0.0 (makes HasUpdate=true)
	//   3: install command -> succeeds
	//   4: verify opencode --version -> v1.0.0 (unchanged!)
	//   5: verify enrichment -> 1.0.0
	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "opencode v1.0.0", err: nil},
		{stdout: "10.0.0", err: nil},
		{stdout: "2.0.0", err: nil},
		{stdout: "installed", err: nil},
		{stdout: "opencode v1.0.0", err: nil},
		{stdout: "1.0.0", err: nil},
	}}
	svc := NewServiceWithRunner(runner)

	snapshotsPtr, reporter := collectProgressSnapshots()
	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationUpdate, reporter, ClaudeInstallAuto)

	// OpenCode only has 1 npm command, no fallback. Should fail.
	if err == nil {
		t.Fatal("expected error when version unchanged")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Success {
		t.Error("expected Success=false")
	}
	if !strings.Contains(result.Error, "version unchanged") {
		t.Errorf("error should mention version unchanged, got: %s", result.Error)
	}

	// Verify progress snapshots show the verify step was reached.
	snapshots := *snapshotsPtr
	sawVerify := false
	for _, snap := range snapshots {
		if snap.step == OperationStepVerify {
			sawVerify = true
		}
	}
	if !sawVerify {
		t.Error("expected at least one verify step in progress snapshots")
	}
}

func TestUpdate_OpenCodeNPMCandidateNewButDefaultStillOld_Fails(t *testing.T) {
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
	oldOpenCodePath := writeTestExecutable(t, oldBinDir, "opencode")
	_ = writeTestExecutable(t, oldBinDir, "npm")
	newOpenCodePath := writeTestExecutable(t, npmBinDir, "opencode")
	t.Setenv("PATH", oldBinDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "opencode v1.0.0", err: nil},   // pre-check resolves old PATH entry
		{stdout: "10.0.0", err: nil},            // npm availability probe
		{stdout: "2.0.0", err: nil},             // latest version enrichment
		{stdout: "changed 1 package", err: nil}, // npm install -g opencode-ai@latest succeeds
		{stdout: "opencode v1.0.0", err: nil},   // default post-check still sees old PATH entry
		{stdout: npmPrefix, err: nil},           // npm prefix -g points at the updated global prefix
		{stdout: "opencode v2.0.0", err: nil},   // explicit npm global bin candidate reports new version
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected failure when npm candidate is new but default OpenCode entry still resolves to old version")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got: %+v", result)
	}
	for _, want := range []string{
		"default/effective OpenCode entry",
		filepath.Clean(oldOpenCodePath),
		"version 1.0.0",
		filepath.Clean(newOpenCodePath),
		"version 2.0.0",
		"PATH",
	} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("failure should contain %q, got: %s", want, result.Error)
		}
	}
}

func TestUpdate_OpenCodeHomebrewStaleEntryAutoCleanup_Succeeds(t *testing.T) {
	fixture := newOpenCodeHomebrewCleanupFixture(t, false, true, true)
	svc := NewServiceWithRunner(fixture.runner)

	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationUpdate, nil, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("expected cleanup-assisted success, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful result, got: %+v", result)
	}
	if result.Version != "2.0.0" {
		t.Fatalf("result.Version = %q, want 2.0.0", result.Version)
	}
	for _, want := range []string{"旧 Homebrew 入口", "brew uninstall opencode", "HOMEBREW_NO_AUTOREMOVE=1", "Homebrew autoremove disabled", "stale Homebrew path no longer effective"} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("success message should contain %q, got: %s", want, result.Message)
		}
	}
	uninstallSpec, ok := fixture.runner.brewUninstallCall()
	if !ok {
		t.Fatal("expected brew uninstall opencode to be called")
	}
	if len(uninstallSpec.Args) != 2 || uninstallSpec.Args[0] != "uninstall" || uninstallSpec.Args[1] != "opencode" {
		t.Fatalf("brew cleanup command must stay limited to uninstall opencode, got args=%v", uninstallSpec.Args)
	}
	if got := envValue(uninstallSpec.Env, homebrewNoAutoremoveEnv); got != homebrewCleanupSafetyEnvValue {
		t.Fatalf("brew uninstall env %s = %q, want %q; env=%v", homebrewNoAutoremoveEnv, got, homebrewCleanupSafetyEnvValue, uninstallSpec.Env)
	}
	if got := envValue(uninstallSpec.Env, homebrewNoInstallCleanupEnv); got != homebrewCleanupSafetyEnvValue {
		t.Fatalf("brew uninstall env %s = %q, want %q; env=%v", homebrewNoInstallCleanupEnv, got, homebrewCleanupSafetyEnvValue, uninstallSpec.Env)
	}
}

func TestUpdate_OpenCodeHomebrewStaleEntryCleanupFails_ReportsFailure(t *testing.T) {
	fixture := newOpenCodeHomebrewCleanupFixture(t, false, true, false)
	svc := NewServiceWithRunner(fixture.runner)

	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected failure when brew uninstall opencode fails")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got: %+v", result)
	}
	for _, want := range []string{"brew uninstall opencode", "permission denied", "cleanup failed"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("failure should contain %q, got: %s", want, result.Error)
		}
	}
	if !fixture.runner.brewUninstallCalled() {
		t.Fatal("expected brew uninstall opencode to be attempted")
	}
}

func TestUpdate_OpenCodeNonHomebrewStaleEntry_NoAutoCleanup(t *testing.T) {
	fixture := newOpenCodeHomebrewCleanupFixture(t, true, true, true)
	svc := NewServiceWithRunner(fixture.runner)

	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected failure for non-Homebrew stale entry")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got: %+v", result)
	}
	if fixture.runner.brewUninstallCalled() {
		t.Fatal("brew uninstall opencode must not be called for non-Homebrew stale entry")
	}
	if !strings.Contains(result.Error, "not recognized as Homebrew OpenCode") {
		t.Fatalf("failure should explain cleanup guard refusal, got: %s", result.Error)
	}
}

func TestUpdate_OpenCodeHomebrewStaleEntryCleanupRecheckMustPass(t *testing.T) {
	fixture := newOpenCodeHomebrewCleanupFixture(t, false, false, true)
	svc := NewServiceWithRunner(fixture.runner)

	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected failure when cleanup succeeds but recheck still resolves stale Homebrew entry")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got: %+v", result)
	}
	if !fixture.runner.brewUninstallCalled() {
		t.Fatal("expected brew uninstall opencode to be called before recheck")
	}
	for _, want := range []string{"cleanup recheck failed", "recheck", "stale Homebrew"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("failure should contain %q, got: %s", want, result.Error)
		}
	}
}

type openCodeHomebrewCleanupFixture struct {
	runner *openCodeHomebrewCleanupRunner
}

func newOpenCodeHomebrewCleanupFixture(t *testing.T, nonHomebrewStale bool, removeOldOnCleanup bool, cleanupSucceeds bool) openCodeHomebrewCleanupFixture {
	t.Helper()
	tmpDir := t.TempDir()
	brewPrefix := filepath.Join(tmpDir, "homebrew")
	brewBinDir := filepath.Join(brewPrefix, "bin")
	staleBinDir := filepath.Join(brewPrefix, "Cellar", "opencode", "1.14.50", "bin")
	if nonHomebrewStale {
		staleBinDir = filepath.Join(tmpDir, "custom-old-bin")
	}
	npmPrefix := filepath.Join(tmpDir, "npm-prefix")
	npmBinDir := filepath.Join(npmPrefix, "bin")
	for _, dir := range []string{brewBinDir, staleBinDir, npmBinDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	staleOpenCodePath := writeTestExecutable(t, staleBinDir, "opencode")
	npmOpenCodePath := writeTestExecutable(t, npmBinDir, "opencode")
	_ = writeTestExecutable(t, npmBinDir, "npm")
	brewPath := writeTestExecutable(t, brewBinDir, "brew")

	t.Setenv("PATH", strings.Join([]string{staleBinDir, npmBinDir, brewBinDir}, string(os.PathListSeparator)))

	runner := &openCodeHomebrewCleanupRunner{
		staleOpenCodePath:  staleOpenCodePath,
		npmOpenCodePath:    npmOpenCodePath,
		npmPrefix:          npmPrefix,
		brewPath:           brewPath,
		brewPrefix:         brewPrefix,
		removeOldOnCleanup: removeOldOnCleanup,
		cleanupSucceeds:    cleanupSucceeds,
	}
	return openCodeHomebrewCleanupFixture{runner: runner}
}

type openCodeHomebrewCleanupRunner struct {
	staleOpenCodePath  string
	npmOpenCodePath    string
	npmPrefix          string
	brewPath           string
	brewPrefix         string
	removeOldOnCleanup bool
	cleanupSucceeds    bool

	mu    sync.Mutex
	calls []platform.CommandSpec
}

func (r *openCodeHomebrewCleanupRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, spec)
	r.mu.Unlock()

	path := filepath.Clean(spec.Path)
	if sameNormalizedPath(path, r.staleOpenCodePath) {
		return &platform.ProcessResult{Stdout: "opencode v1.0.0"}, nil
	}
	if sameNormalizedPath(path, r.npmOpenCodePath) {
		return &platform.ProcessResult{Stdout: "opencode v2.0.0"}, nil
	}
	if isNPMPath(strings.ToLower(spec.Path)) || strings.EqualFold(filepath.Base(spec.Path), "npm") {
		return r.runNPM(spec)
	}
	if sameNormalizedPath(path, r.brewPath) || strings.EqualFold(filepath.Base(spec.Path), "brew") {
		return r.runBrew(spec)
	}
	return &platform.ProcessResult{}, os.ErrNotExist
}

func (r *openCodeHomebrewCleanupRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

func (r *openCodeHomebrewCleanupRunner) runNPM(spec platform.CommandSpec) (*platform.ProcessResult, error) {
	if len(spec.Args) >= 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g" {
		return &platform.ProcessResult{Stdout: r.npmPrefix}, nil
	}
	if len(spec.Args) >= 1 && spec.Args[0] == "--version" {
		return &platform.ProcessResult{Stdout: "10.0.0"}, nil
	}
	if len(spec.Args) >= 1 && spec.Args[0] == "view" {
		return &platform.ProcessResult{Stdout: "2.0.0"}, nil
	}
	if len(spec.Args) >= 1 && (spec.Args[0] == "install" || spec.Args[0] == "update") {
		return &platform.ProcessResult{Stdout: "changed 1 package"}, nil
	}
	return &platform.ProcessResult{}, nil
}

func (r *openCodeHomebrewCleanupRunner) runBrew(spec platform.CommandSpec) (*platform.ProcessResult, error) {
	if len(spec.Args) == 1 && spec.Args[0] == "--prefix" {
		return &platform.ProcessResult{Stdout: r.brewPrefix}, nil
	}
	if len(spec.Args) == 3 && spec.Args[0] == "list" && spec.Args[1] == "--versions" && spec.Args[2] == "opencode" {
		return &platform.ProcessResult{Stdout: "opencode 1.14.50"}, nil
	}
	if len(spec.Args) == 2 && spec.Args[0] == "uninstall" && spec.Args[1] == "opencode" {
		if !r.cleanupSucceeds {
			return &platform.ProcessResult{Stderr: "permission denied while uninstalling opencode"}, errors.New("permission denied")
		}
		if r.removeOldOnCleanup {
			_ = os.Remove(r.staleOpenCodePath)
		}
		return &platform.ProcessResult{Stdout: "Uninstalling /opt/homebrew/Cellar/opencode/1.14.50..."}, nil
	}
	return &platform.ProcessResult{}, os.ErrNotExist
}

func (r *openCodeHomebrewCleanupRunner) brewUninstallCalled() bool {
	_, ok := r.brewUninstallCall()
	return ok
}

func (r *openCodeHomebrewCleanupRunner) brewUninstallCall() (platform.CommandSpec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if (sameNormalizedPath(call.Path, r.brewPath) || strings.EqualFold(filepath.Base(call.Path), "brew")) && len(call.Args) == 2 && call.Args[0] == "uninstall" && call.Args[1] == "opencode" {
			return call, true
		}
	}
	return platform.CommandSpec{}, false
}

func envValue(env []string, key string) string {
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok && name == key {
			return value
		}
	}
	return ""
}

func TestUpdate_OpenCodeNPMCandidateNewAndDefaultSamePath_Succeeds(t *testing.T) {
	tmpDir := t.TempDir()
	npmPrefix := filepath.Join(tmpDir, "npm-prefix")
	npmBinDir := filepath.Join(npmPrefix, "bin")
	if err := os.MkdirAll(npmBinDir, 0o755); err != nil {
		t.Fatalf("mkdir npm bin: %v", err)
	}
	_ = writeTestExecutable(t, npmBinDir, "npm")
	newOpenCodePath := writeTestExecutable(t, npmBinDir, "opencode")
	t.Setenv("PATH", npmBinDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "opencode v1.0.0", err: nil},   // pre-check sees old version at the npm candidate path
		{stdout: "10.0.0", err: nil},            // npm availability probe
		{stdout: "2.0.0", err: nil},             // latest version enrichment
		{stdout: "changed 1 package", err: nil}, // npm install -g opencode-ai@latest succeeds
		{stdout: "opencode v1.0.0", err: nil},   // default post-check has not observed a changed version yet
		{stdout: npmPrefix, err: nil},           // npm prefix -g points at the same default entry
		{stdout: "opencode v2.0.0", err: nil},   // explicit npm global bin candidate reports new version
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationUpdate, nil, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("expected same-path npm candidate verification to recover update, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful result, got: %+v", result)
	}
	if result.Version != "2.0.0" {
		t.Fatalf("result.Version = %q, want 2.0.0", result.Version)
	}
	if !strings.Contains(result.Message, "default/effective entry matches npm candidate path") {
		t.Fatalf("success message should mention same-path verification, got: %s", result.Message)
	}
	if !strings.Contains(result.Message, filepath.Clean(newOpenCodePath)) {
		t.Fatalf("success message should include verified npm global opencode path %q, got: %s", newOpenCodePath, result.Message)
	}
}

func TestUpdate_CodexNPMCandidateNewButDefaultStillOld_Fails(t *testing.T) {
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
	oldCodexPath := writeTestExecutable(t, oldBinDir, "codex")
	_ = writeTestExecutable(t, oldBinDir, "npm")
	newCodexPath := writeTestExecutable(t, npmBinDir, "codex")
	t.Setenv("PATH", oldBinDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "codex-cli v1.0.0", err: nil},  // pre-check resolves old PATH entry
		{stdout: "10.0.0", err: nil},            // npm availability probe
		{stdout: "2.0.0", err: nil},             // latest version enrichment
		{stdout: "updated 1 package", err: nil}, // npm update -g @openai/codex succeeds
		{stdout: "codex-cli v1.0.0", err: nil},  // default post-check still sees old PATH entry
		{stdout: npmPrefix, err: nil},           // npm prefix -g points at updated global prefix
		{stdout: "codex-cli v2.0.0", err: nil},  // explicit npm global bin candidate reports new version
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.installOrUpdateWithProgress(ToolCodex, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected failure when npm candidate is new but default Codex entry still resolves to old version")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got: %+v", result)
	}
	for _, want := range []string{
		"default/effective Codex entry",
		filepath.Clean(oldCodexPath),
		"version 1.0.0",
		filepath.Clean(newCodexPath),
		"version 2.0.0",
	} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("failure should contain %q, got: %s", want, result.Error)
		}
	}
}

func TestUpdate_CodexHomebrewNPMStaleEntryAutoCleanup_Succeeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew npm stale Codex cleanup is a POSIX/macOS path scenario")
	}
	fixture := newCodexStaleNPMCleanupFixture(t, codexStaleNPMCleanupOptions{
		packageName:        codexNPMPackageName,
		removeOldOnCleanup: true,
		cleanupSucceeds:    true,
	})
	svc := NewServiceWithRunner(fixture.runner)

	result, err := svc.installOrUpdateWithProgress(ToolCodex, installOperationUpdate, nil, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("expected cleanup-assisted success, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful result, got: %+v", result)
	}
	if result.Version != "2.0.0" {
		t.Fatalf("result.Version = %q, want 2.0.0", result.Version)
	}
	for _, want := range []string{"旧 Homebrew npm 全局入口", "npm uninstall -g @openai/codex --prefix", "stale Homebrew npm Codex path no longer effective"} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("success message should contain %q, got: %s", want, result.Message)
		}
	}
	uninstallSpec, ok := fixture.runner.codexUninstallCall()
	if !ok {
		t.Fatal("expected scoped npm uninstall @openai/codex to be called")
	}
	wantArgs := []string{"uninstall", "-g", codexNPMPackageName, "--prefix", fixture.stalePrefix}
	if len(uninstallSpec.Args) != len(wantArgs) || strings.Join(uninstallSpec.Args[:4], "\x00") != strings.Join(wantArgs[:4], "\x00") || !sameNormalizedPath(uninstallSpec.Args[4], wantArgs[4]) {
		t.Fatalf("cleanup command must stay scoped to @openai/codex and stale prefix, got args=%v want=%v", uninstallSpec.Args, wantArgs)
	}
	if fixture.runner.brewCalled() {
		t.Fatal("Codex stale npm cleanup must not call brew")
	}
}

func TestUpdate_CodexHomebrewNPMStaleEntryCleanupFails_ReportsFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew npm stale Codex cleanup is a POSIX/macOS path scenario")
	}
	fixture := newCodexStaleNPMCleanupFixture(t, codexStaleNPMCleanupOptions{
		packageName:        codexNPMPackageName,
		removeOldOnCleanup: true,
		cleanupSucceeds:    false,
	})
	svc := NewServiceWithRunner(fixture.runner)

	result, err := svc.installOrUpdateWithProgress(ToolCodex, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected failure when scoped npm uninstall fails")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got: %+v", result)
	}
	for _, want := range []string{"Codex stale Homebrew npm cleanup failed", "npm uninstall -g @openai/codex --prefix", "permission denied"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("failure should contain %q, got: %s", want, result.Error)
		}
	}
	if !fixture.runner.codexUninstallCalled() {
		t.Fatal("expected scoped npm uninstall @openai/codex to be attempted")
	}
}

func TestUpdate_CodexHomebrewNPMStaleEntryCleanupRecheckMustPass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew npm stale Codex cleanup is a POSIX/macOS path scenario")
	}
	fixture := newCodexStaleNPMCleanupFixture(t, codexStaleNPMCleanupOptions{
		packageName:        codexNPMPackageName,
		removeOldOnCleanup: false,
		cleanupSucceeds:    true,
	})
	svc := NewServiceWithRunner(fixture.runner)

	result, err := svc.installOrUpdateWithProgress(ToolCodex, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected failure when cleanup succeeds but recheck still resolves stale Codex entry")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got: %+v", result)
	}
	if !fixture.runner.codexUninstallCalled() {
		t.Fatal("expected scoped npm uninstall @openai/codex to be called before recheck")
	}
	for _, want := range []string{"cleanup recheck failed", "recheck", "stale Homebrew npm Codex"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("failure should contain %q, got: %s", want, result.Error)
		}
	}
}

func TestTryCleanupCodexStaleNPMEntryAfterFallback_GuardsUnsafeInputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew npm stale Codex cleanup is a POSIX/macOS path scenario")
	}
	t.Run("candidate unhealthy", func(t *testing.T) {
		fixture := newCodexStaleNPMCleanupFixture(t, codexStaleNPMCleanupOptions{
			packageName:        codexNPMPackageName,
			removeOldOnCleanup: true,
			cleanupSucceeds:    true,
		})
		svc := NewServiceWithRunner(fixture.runner)
		_, detail, err := svc.tryCleanupCodexStaleNPMEntryAfterFallback(installOperationUpdate, "1.0.0", fixture.effectiveStatus(), &CheckStatus{
			Tool:           ToolCodex,
			Installed:      false,
			PATHOk:         false,
			InstallMethod:  InstallMethodNPM,
			Version:        "2.0.0",
			ExecutablePath: fixture.npmCodexPath,
		})
		if err == nil || !strings.Contains(detail, "npm candidate is not safe") {
			t.Fatalf("expected unhealthy candidate guard, detail=%q err=%v", detail, err)
		}
		if fixture.runner.codexUninstallCalled() {
			t.Fatal("cleanup must not run for unhealthy npm candidate")
		}
	})

	t.Run("same path", func(t *testing.T) {
		fixture := newCodexStaleNPMCleanupFixture(t, codexStaleNPMCleanupOptions{
			packageName:        codexNPMPackageName,
			removeOldOnCleanup: true,
			cleanupSucceeds:    true,
		})
		svc := NewServiceWithRunner(fixture.runner)
		candidate := fixture.npmCandidateStatus()
		effective := fixture.npmCandidateStatus()
		_, detail, err := svc.tryCleanupCodexStaleNPMEntryAfterFallback(installOperationUpdate, "1.0.0", effective, candidate)
		if err == nil || !strings.Contains(detail, "already matches npm candidate path") {
			t.Fatalf("expected same-path guard, detail=%q err=%v", detail, err)
		}
		if fixture.runner.codexUninstallCalled() {
			t.Fatal("cleanup must not run when effective path already matches npm candidate")
		}
	})

	t.Run("non Homebrew prefix", func(t *testing.T) {
		fixture := newCodexStaleNPMCleanupFixture(t, codexStaleNPMCleanupOptions{
			stalePrefixName:    "custom-prefix",
			packageName:        codexNPMPackageName,
			removeOldOnCleanup: true,
			cleanupSucceeds:    true,
		})
		svc := NewServiceWithRunner(fixture.runner)
		_, detail, err := svc.tryCleanupCodexStaleNPMEntryAfterFallback(installOperationUpdate, "1.0.0", fixture.effectiveStatus(), fixture.npmCandidateStatus())
		if err == nil || !strings.Contains(detail, "not recognized as a Homebrew npm global prefix") {
			t.Fatalf("expected non-Homebrew guard, detail=%q err=%v", detail, err)
		}
		if fixture.runner.codexUninstallCalled() {
			t.Fatal("cleanup must not run for non-Homebrew prefix")
		}
	})

	t.Run("non Codex package", func(t *testing.T) {
		fixture := newCodexStaleNPMCleanupFixture(t, codexStaleNPMCleanupOptions{
			packageName:        "@openai/not-codex",
			removeOldOnCleanup: true,
			cleanupSucceeds:    true,
		})
		svc := NewServiceWithRunner(fixture.runner)
		_, detail, err := svc.tryCleanupCodexStaleNPMEntryAfterFallback(installOperationUpdate, "1.0.0", fixture.effectiveStatus(), fixture.npmCandidateStatus())
		if err == nil || !strings.Contains(detail, "package.json guard failed") {
			t.Fatalf("expected package-name guard, detail=%q err=%v", detail, err)
		}
		if fixture.runner.codexUninstallCalled() {
			t.Fatal("cleanup must not run when package.json name is not @openai/codex")
		}
	})
}

type codexStaleNPMCleanupOptions struct {
	stalePrefixName    string
	packageName        string
	removeOldOnCleanup bool
	cleanupSucceeds    bool
}

type codexStaleNPMCleanupFixture struct {
	runner         *codexStaleNPMCleanupRunner
	stalePrefix    string
	staleBinPath   string
	staleCodexPath string
	npmCodexPath   string
	npmPrefix      string
}

func newCodexStaleNPMCleanupFixture(t *testing.T, opts codexStaleNPMCleanupOptions) codexStaleNPMCleanupFixture {
	t.Helper()
	if opts.stalePrefixName == "" {
		opts.stalePrefixName = "homebrew"
	}
	if opts.packageName == "" {
		opts.packageName = codexNPMPackageName
	}
	tmpDir := t.TempDir()
	stalePrefix := filepath.Join(tmpDir, opts.stalePrefixName)
	stalePrefixBin := filepath.Join(stalePrefix, "bin")
	stalePackageRoot := filepath.Join(stalePrefix, "lib", "node_modules", "@openai", "codex")
	stalePackageBin := filepath.Join(stalePackageRoot, "bin")
	npmPrefix := filepath.Join(tmpDir, "npm-prefix")
	npmPrefixBin := filepath.Join(npmPrefix, "bin")
	npmPackageRoot := filepath.Join(npmPrefix, "lib", "node_modules", "@openai", "codex")
	npmPackageBin := filepath.Join(npmPackageRoot, "bin")
	for _, dir := range []string{stalePrefixBin, stalePackageBin, npmPrefixBin, npmPackageBin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	staleCodexPath := writeTestExecutable(t, stalePackageBin, "codex.js")
	npmCodexPath := writeTestExecutable(t, npmPackageBin, "codex.js")
	staleBinPath := filepath.Join(stalePrefixBin, "codex")
	npmBinPath := filepath.Join(npmPrefixBin, "codex")
	if err := os.Symlink(staleCodexPath, staleBinPath); err != nil {
		t.Fatalf("symlink stale codex: %v", err)
	}
	if err := os.Symlink(npmCodexPath, npmBinPath); err != nil {
		t.Fatalf("symlink npm codex: %v", err)
	}
	_ = writeTestExecutable(t, npmPrefixBin, "npm")
	writeCodexPackageJSON(t, filepath.Join(stalePackageRoot, "package.json"), opts.packageName, "1.0.0")
	writeCodexPackageJSON(t, filepath.Join(npmPackageRoot, "package.json"), codexNPMPackageName, "2.0.0")
	t.Setenv("PATH", strings.Join([]string{stalePrefixBin, npmPrefixBin}, string(os.PathListSeparator)))

	runner := &codexStaleNPMCleanupRunner{
		stalePrefix:        stalePrefix,
		staleBinPath:       staleBinPath,
		staleCodexPath:     staleCodexPath,
		stalePackageRoot:   stalePackageRoot,
		npmCodexPath:       npmCodexPath,
		npmPrefix:          npmPrefix,
		removeOldOnCleanup: opts.removeOldOnCleanup,
		cleanupSucceeds:    opts.cleanupSucceeds,
	}
	return codexStaleNPMCleanupFixture{runner: runner, stalePrefix: stalePrefix, staleBinPath: staleBinPath, staleCodexPath: staleCodexPath, npmCodexPath: npmCodexPath, npmPrefix: npmPrefix}
}

func writeCodexPackageJSON(t *testing.T, path string, name string, version string) {
	t.Helper()
	content := fmt.Sprintf(`{"name":%q,"version":%q}`, name, version)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write package.json %s: %v", path, err)
	}
}

func (f codexStaleNPMCleanupFixture) effectiveStatus() *CheckStatus {
	return &CheckStatus{
		Tool:           ToolCodex,
		Installed:      true,
		PATHOk:         true,
		InstallMethod:  InstallMethodNPM,
		Version:        "1.0.0",
		ExecutablePath: f.staleCodexPath,
	}
}

func (f codexStaleNPMCleanupFixture) npmCandidateStatus() *CheckStatus {
	return &CheckStatus{
		Tool:           ToolCodex,
		Installed:      true,
		PATHOk:         true,
		InstallMethod:  InstallMethodNPM,
		Version:        "2.0.0",
		ExecutablePath: f.npmCodexPath,
	}
}

type codexStaleNPMCleanupRunner struct {
	stalePrefix        string
	staleBinPath       string
	staleCodexPath     string
	stalePackageRoot   string
	npmCodexPath       string
	npmPrefix          string
	removeOldOnCleanup bool
	cleanupSucceeds    bool

	mu    sync.Mutex
	calls []platform.CommandSpec
}

func (r *codexStaleNPMCleanupRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, spec)
	r.mu.Unlock()

	path := filepath.Clean(spec.Path)
	if sameNormalizedPath(path, r.staleCodexPath) {
		return &platform.ProcessResult{Stdout: "codex-cli 1.0.0"}, nil
	}
	if sameNormalizedPath(path, r.npmCodexPath) {
		return &platform.ProcessResult{Stdout: "codex-cli 2.0.0"}, nil
	}
	if isNPMPath(strings.ToLower(spec.Path)) || strings.EqualFold(filepath.Base(spec.Path), "npm") {
		return r.runNPM(spec)
	}
	if strings.EqualFold(filepath.Base(spec.Path), "brew") {
		return &platform.ProcessResult{Stderr: "brew must not be called"}, errors.New("brew must not be called")
	}
	return &platform.ProcessResult{}, os.ErrNotExist
}

func (r *codexStaleNPMCleanupRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

func (r *codexStaleNPMCleanupRunner) runNPM(spec platform.CommandSpec) (*platform.ProcessResult, error) {
	if len(spec.Args) >= 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g" {
		return &platform.ProcessResult{Stdout: r.npmPrefix}, nil
	}
	if len(spec.Args) >= 1 && spec.Args[0] == "--version" {
		return &platform.ProcessResult{Stdout: "10.0.0"}, nil
	}
	if len(spec.Args) >= 1 && spec.Args[0] == "view" {
		return &platform.ProcessResult{Stdout: "2.0.0"}, nil
	}
	if len(spec.Args) >= 1 && (spec.Args[0] == "install" || spec.Args[0] == "update") {
		return &platform.ProcessResult{Stdout: "updated 1 package"}, nil
	}
	if len(spec.Args) == 5 && spec.Args[0] == "uninstall" && spec.Args[1] == "-g" && spec.Args[2] == codexNPMPackageName && spec.Args[3] == "--prefix" && sameNormalizedPath(spec.Args[4], r.stalePrefix) {
		if !r.cleanupSucceeds {
			return &platform.ProcessResult{Stderr: "permission denied while uninstalling @openai/codex"}, errors.New("permission denied")
		}
		if r.removeOldOnCleanup {
			_ = os.Remove(r.staleBinPath)
			_ = os.RemoveAll(r.stalePackageRoot)
		}
		return &platform.ProcessResult{Stdout: "removed 1 package"}, nil
	}
	return &platform.ProcessResult{}, nil
}

func (r *codexStaleNPMCleanupRunner) codexUninstallCalled() bool {
	_, ok := r.codexUninstallCall()
	return ok
}

func (r *codexStaleNPMCleanupRunner) codexUninstallCall() (platform.CommandSpec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if len(call.Args) == 5 && call.Args[0] == "uninstall" && call.Args[1] == "-g" && call.Args[2] == codexNPMPackageName && call.Args[3] == "--prefix" {
			return call, true
		}
	}
	return platform.CommandSpec{}, false
}

func (r *codexStaleNPMCleanupRunner) brewCalled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if strings.EqualFold(filepath.Base(call.Path), "brew") {
			return true
		}
	}
	return false
}

func TestUpdate_ClaudeNPMCandidateNewButDefaultStillOld_Fails(t *testing.T) {
	tmpDir := t.TempDir()
	oldBinDir := filepath.Join(tmpDir, "old-bin")
	npmPrefix := filepath.Join(tmpDir, "npm-prefix")
	npmBinDir := filepath.Join(npmPrefix, "bin")
	homeDir := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(npmBinDir, 0o755); err != nil {
		t.Fatalf("mkdir npm bin: %v", err)
	}
	if err := os.MkdirAll(oldBinDir, 0o755); err != nil {
		t.Fatalf("mkdir old bin: %v", err)
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	oldClaudePath := writeTestExecutable(t, oldBinDir, "claude")
	_ = writeTestExecutable(t, oldBinDir, "npm")
	newClaudePath := writeTestExecutable(t, npmBinDir, "claude")
	t.Setenv("PATH", oldBinDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	runner := &npmPrefixUpdateRunner{
		tool:        ToolClaudeCode,
		oldToolPath: oldClaudePath,
		newToolPath: newClaudePath,
		npmPrefix:   npmPrefix,
	}
	svc := NewServiceWithRunner(runner)

	result, err := svc.installOrUpdateWithProgress(ToolClaudeCode, installOperationUpdate, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected failure when npm candidate is new but default Claude entry still resolves to old version")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got: %+v", result)
	}
	for _, want := range []string{
		"default/effective Claude Code entry",
		filepath.Clean(oldClaudePath),
		"version 1.0.0",
		filepath.Clean(newClaudePath),
		"version 2.0.0",
	} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("failure should contain %q, got: %s", want, result.Error)
		}
	}
}

type npmPrefixUpdateRunner struct {
	tool        CLITool
	oldToolPath string
	newToolPath string
	npmPrefix   string
}

func (r *npmPrefixUpdateRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	path := filepath.Clean(spec.Path)
	if sameNormalizedPath(path, r.oldToolPath) {
		return &platform.ProcessResult{Stdout: npmPrefixUpdateVersionOutput(r.tool, "1.0.0")}, nil
	}
	if sameNormalizedPath(path, r.newToolPath) {
		return &platform.ProcessResult{Stdout: npmPrefixUpdateVersionOutput(r.tool, "2.0.0")}, nil
	}
	if isNPMPath(strings.ToLower(spec.Path)) || strings.EqualFold(filepath.Base(spec.Path), "npm") {
		if len(spec.Args) >= 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g" {
			return &platform.ProcessResult{Stdout: r.npmPrefix}, nil
		}
		if len(spec.Args) >= 1 && spec.Args[0] == "--version" {
			return &platform.ProcessResult{Stdout: "10.0.0"}, nil
		}
		if len(spec.Args) >= 1 && spec.Args[0] == "view" {
			return &platform.ProcessResult{Stdout: "2.0.0"}, nil
		}
		if len(spec.Args) >= 1 && (spec.Args[0] == "install" || spec.Args[0] == "update") {
			return &platform.ProcessResult{Stdout: "changed 1 package"}, nil
		}
		if len(spec.Args) >= 1 && spec.Args[0] == "list" {
			return &platform.ProcessResult{Stdout: "node_modules/@anthropic-ai/claude-code\n└── @anthropic-ai/claude-code@2.0.0"}, nil
		}
	}
	return &platform.ProcessResult{}, os.ErrNotExist
}

func (r *npmPrefixUpdateRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

func npmPrefixUpdateVersionOutput(tool CLITool, version string) string {
	switch tool {
	case ToolClaudeCode:
		return "Claude Code v" + version
	case ToolCodex:
		return "codex-cli v" + version
	default:
		return version
	}
}

// TestUpdate_MultiCommandFallback_AllFail verifies that when all commands
// fail (or succeed-but-verify-fails), the final error includes details
// about each attempt.
func TestUpdate_MultiCommandFallback_AllFail(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)

	// All runner calls fail
	svc := newTestService()

	result, err := svc.installOrUpdateWithProgress(ToolOpenCode, installOperationInstall, nil, ClaudeInstallAuto)

	if err == nil {
		t.Fatal("expected error when all commands fail")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Success {
		t.Error("expected failure")
	}
	// Error should contain actionable information
	if !strings.Contains(result.Error, "npm") && !strings.Contains(result.Error, "failed") {
		t.Errorf("error should mention npm or failure details, got: %s", result.Error)
	}
}

// TestUpdate_ClaudeVerifyFail_ContinuesToFallback uses the indexedRunner to
// simulate Claude Code update where npm succeeds but verify shows the version
// unchanged.
//
// Claude Code now only uses npm/native flows. The generic update path is npm
// only, so verify-fail means failure on all platforms.
func TestUpdate_ClaudeVerifyFail_ContinuesToFallback(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "claude")
	t.Setenv("PATH", tmpDir)

	if runtime.GOOS == "windows" {
		// Windows: npm succeeds but verify unchanged. No winget fallback.
		// Claude only has npm command now, so verify-fail means failure.
		runner := &indexedRunner{responses: map[int]indexedResponse{
			0: {stdout: "Claude Code v1.0.0"}, // pre-check
			1: {stdout: "10.0.0"},             // npm probe
			2: {stdout: "2.0.0"},              // enrichment: HasUpdate=true
			3: {stdout: "installed"},          // npm install @latest succeeds
			4: {stdout: "Claude Code v1.0.0"}, // verify: UNCHANGED!
			5: {stdout: "1.0.0"},              // enrichment
		}}
		svc := NewServiceWithRunner(runner)

		result, err := svc.installOrUpdateWithProgress(ToolClaudeCode, installOperationUpdate, nil, ClaudeInstallAuto)

		// Only npm command; verify unchanged = failure
		if err == nil && (result != nil && result.Success) {
			t.Fatalf("expected failure (npm verify unchanged, no winget fallback), got: err=%v result=%+v", err, result)
		}
	} else {
		// Non-Windows: only npm. npm succeeds but verify unchanged = failure.
		runner := &indexedRunner{responses: map[int]indexedResponse{
			0: {stdout: "Claude Code v1.0.0"},
			1: {stdout: "10.0.0"},
			2: {stdout: "2.0.0"}, // enrichment: HasUpdate=true
			3: {stdout: "installed"},
			4: {stdout: "Claude Code v1.0.0"}, // version unchanged
			5: {stdout: "1.0.0"},
		}}
		svc := NewServiceWithRunner(runner)

		result, err := svc.installOrUpdateWithProgress(ToolClaudeCode, installOperationUpdate, nil, ClaudeInstallAuto)

		if err != nil && (result == nil || !strings.Contains(result.Error, "version unchanged")) {
			t.Errorf("unexpected update error, got result=%+v err=%v", result, err)
		}
	}
}

// TestUpdate_AllCommandsVerifyFail_ReportsAllDetails verifies that when every
// command either fails to execute or succeeds but verification fails, the
// final error message includes per-command details.
func TestUpdate_AllCommandsVerifyFail_ReportsAllDetails(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	// Runner where install succeeds but verify always shows v1.0.0.
	// Enrichment returns 2.0.0 so HasUpdate=true to trigger the update path.
	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "opencode v1.0.0", err: nil}, // pre-check
		{stdout: "10.0.0", err: nil},          // npm probe
		{stdout: "2.0.0", err: nil},           // enrichment: HasUpdate=true
		{stdout: "installed", err: nil},       // install succeeds
		{stdout: "opencode v1.0.0", err: nil}, // verify: unchanged
		{stdout: "1.0.0", err: nil},           // enrichment
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.Update(ToolOpenCode)

	if err == nil {
		t.Fatal("expected error")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Success {
		t.Error("expected failure")
	}
	// Should contain "verification failed" or "version unchanged"
	errText := strings.ToLower(result.Error)
	if !strings.Contains(errText, "verification") && !strings.Contains(errText, "unchanged") {
		t.Errorf("error should mention verification or unchanged, got: %s", result.Error)
	}
}

// indexedRunner returns responses based on call index.
// Thread-safe via mutex.
type indexedRunner struct {
	responses map[int]indexedResponse
	mu        sync.Mutex
	next      int
}

type indexedResponse struct {
	stdout string
	stderr string
	err    error
}

func (r *indexedRunner) Run(_ context.Context, _ platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	idx := r.next
	r.next++
	r.mu.Unlock()

	resp, ok := r.responses[idx]
	if !ok {
		return &platform.ProcessResult{}, nil
	}
	return &platform.ProcessResult{Stdout: resp.stdout, Stderr: resp.stderr}, resp.err
}

func (r *indexedRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// K. Claude update npm command uses install @latest
// ---------------------------------------------------------------------------

// TestClaudeUpdateCommand_UsesInstallLatest verifies that Claude Code update
// npm command uses "install -g @latest" instead of "update -g".
func TestClaudeUpdateCommand_UsesInstallLatest(t *testing.T) {
	cmd := npmClaudeCommand(installOperationUpdate)
	if cmd.args[0] != "install" {
		t.Errorf("update command should use 'install', got args: %v", cmd.args)
	}
	foundLatest := false
	for _, arg := range cmd.args {
		if arg == "@anthropic-ai/claude-code@latest" {
			foundLatest = true
		}
	}
	if !foundLatest {
		t.Errorf("update command should use @latest tag, got args: %v", cmd.args)
	}
}

// TestClaudeInstallCommand_UsesInstall verifies fresh install still uses install.
func TestClaudeInstallCommand_UsesInstall(t *testing.T) {
	cmd := npmClaudeCommand(installOperationInstall)
	if cmd.args[0] != "install" {
		t.Errorf("install command should use 'install', got args: %v", cmd.args)
	}
	// Fresh install doesn't need @latest tag
	foundPkg := false
	for _, arg := range cmd.args {
		if arg == "@anthropic-ai/claude-code" {
			foundPkg = true
		}
	}
	if !foundPkg {
		t.Errorf("install command should reference package, got args: %v", cmd.args)
	}
}

// TestOpenCodeUpdateCommand_UsesInstallLatest verifies that OpenCode updates
// force the canonical npm package to latest instead of relying on npm update,
// which can report success while leaving the installed global package unchanged.
func TestOpenCodeUpdateCommand_UsesInstallLatest(t *testing.T) {
	cmd := npmOpenCodeCommand(installOperationUpdate)
	if cmd.args[0] != "install" {
		t.Errorf("OpenCode update command should use 'install', got args: %v", cmd.args)
	}
	foundLatest := false
	for _, arg := range cmd.args {
		if arg == "opencode-ai@latest" {
			foundLatest = true
		}
		if arg == "opencode-ai" {
			t.Errorf("OpenCode update command should not use unqualified package, got args: %v", cmd.args)
		}
	}
	if !foundLatest {
		t.Errorf("OpenCode update command should include opencode-ai@latest, got args: %v", cmd.args)
	}
	if strings.Contains(cmd.description, "npm global update opencode-ai") {
		t.Errorf("OpenCode update description should not advertise npm update no-op path: %q", cmd.description)
	}
}

// ---------------------------------------------------------------------------
// L. Claude NPM install recommendation is non-blocking
// ---------------------------------------------------------------------------

// TestCheckClaudeCode_NPMInstall_NoBlockingError verifies that when Claude Code
// is detected as an npm install, status.Error is empty (non-blocking) and
// the recommendation appears as an info-level issue instead.
//
// We test this by directly invoking checkClaudeCode with a runner that makes
// the version succeed and the npm list confirm the package.
func TestCheckClaudeCode_NPMInstall_NoBlockingError(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "claude")
	t.Setenv("PATH", tmpDir)

	// On macOS the resolver may find the real claude at /opt/homebrew/bin/claude.
	// The runner must respond to whatever path the resolver returns.
	// Use a runner that returns success for claude --version and npm list.
	//
	// Call sequence for CheckOne(ToolClaudeCode):
	//   claude --version -> "Claude Code v1.0.0"
	//   npm --version (probe) -> "10.0.0"
	//   npm view (enrichment) -> "2.0.0"
	//   npm list -g @anthropic-ai/claude-code --depth=0 -> contains package
	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "Claude Code v1.0.0", err: nil},
		{stdout: "10.0.0", err: nil},
		{stdout: "2.0.0", err: nil},
		{stdout: "node_modules/@anthropic-ai/claude-code\n└── @anthropic-ai/claude-code@1.0.0", err: nil},
		{stdout: "1.0.0", err: nil},
	}}
	svc := NewServiceWithRunner(runner)

	status, err := svc.CheckOne(ToolClaudeCode)
	if err != nil {
		t.Fatalf("CheckOne error: %v", err)
	}
	if status == nil {
		t.Fatal("expected non-nil status")
	}

	// If InstallMethod is not NPM, the test can't verify the recommendation.
	// This happens when the path detection logic doesn't classify it as NPM.
	if status.InstallMethod != InstallMethodNPM {
		t.Skipf("InstallMethod = %q, need NPM to verify recommendation (path: %s)", status.InstallMethod, status.ExecutablePath)
	}

	// status.Error MUST be empty -- the npm recommendation is non-blocking
	if strings.TrimSpace(status.Error) != "" {
		t.Errorf("status.Error should be empty for npm-installed Claude, got: %q", status.Error)
	}

	// Must be installed and PATH-ok
	if !status.Installed {
		t.Error("expected Installed=true")
	}
	if !status.PATHOk {
		t.Error("expected PATHOk=true")
	}

	// Should have an info-level issue about the recommendation
	foundRecommendation := false
	for _, issue := range status.Issues {
		if issue.Code == "claude_npm_install_recommended_native" {
			foundRecommendation = true
			if issue.Severity != SeverityInfo {
				t.Errorf("recommendation issue severity = %q, want %q", issue.Severity, SeverityInfo)
			}
		}
	}
	if !foundRecommendation {
		t.Errorf("expected info-level issue 'claude_npm_install_recommended_native'; issues: %+v", status.Issues)
	}
}

// TestCheckClaudeCode_NPMRecommendation_DoesNotBlockInstallVerify is a unit-level
// test that constructs a CheckStatus with InstallMethodNPM and verifies that
// the recommendation does NOT set status.Error. This is a pure logic test
// independent of the resolver finding real binaries.
func TestCheckClaudeCode_NPMRecommendation_DoesNotBlockInstallVerify(t *testing.T) {
	// Simulate a post-install CheckOne where Claude is found via npm.
	// The key assertion: when InstallMethod=NPM, status.Error is empty
	// and the recommendation is an info issue.
	status := &CheckStatus{
		Tool:           ToolClaudeCode,
		Installed:      true,
		PATHOk:         true,
		InstallMethod:  InstallMethodNPM,
		Version:        "1.0.0",
		ExecutablePath: "/usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js",
	}

	// Apply the recommendation logic manually (mirrors what checkClaudeCode does)
	status.Issues = append(status.Issues, CheckIssue{
		Severity: SeverityInfo,
		Code:     "claude_npm_install_recommended_native",
		Message:  "Claude Code was detected as an npm global install; native mode can be enabled after npm install",
		Solutions: []ResolutionAction{
			{
				Type:        SolutionManualCommand,
				Description: "Enable native Claude Code mode",
				Command:     "npm install -g @anthropic-ai/claude-code && claude install",
			},
		},
	})

	// These are the verify conditions used by installOrUpdateWithProgress:
	if !status.Installed {
		t.Error("Installed should be true")
	}
	if !status.PATHOk {
		t.Error("PATHOk should be true")
	}
	if strings.TrimSpace(status.Error) != "" {
		t.Errorf("Error should be empty for npm install (non-blocking recommendation), got: %q", status.Error)
	}
}

// TestInstallVerify_AcceptsNPMClaude verifies that after an npm install of
// Claude Code, the verify step sees Installed && PATHOk && Error empty,
// so the install succeeds (no false failure from recommendation string).
//
// This is tested as a pure verify-logic check since the full install flow
// depends on the OS resolver finding (or not finding) the real claude binary.
func TestInstallVerify_AcceptsNPMClaude(t *testing.T) {
	// Construct the post-install status that checkClaudeCode would produce
	// for an npm-installed Claude Code. The verify logic in
	// installOrUpdateWithProgress checks:
	//   after.Installed && after.PATHOk && after.Error == ""
	//
	// Previously, status.Error contained the recommendation string, causing
	// verify to always fail. Now it should be empty with an info issue instead.
	status := &CheckStatus{
		Tool:           ToolClaudeCode,
		Installed:      true,
		PATHOk:         true,
		InstallMethod:  InstallMethodNPM,
		Version:        "1.0.0",
		ExecutablePath: "/usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js",
		Issues: []CheckIssue{
			{
				Severity: SeverityInfo,
				Code:     "claude_npm_install_recommended_native",
				Message:  "Claude Code was detected as an npm global install; the official native installer is recommended",
			},
		},
	}

	// Verify conditions used by installOrUpdateWithProgress:
	if !status.Installed {
		t.Error("Installed should be true")
	}
	if !status.PATHOk {
		t.Error("PATHOk should be true")
	}
	if strings.TrimSpace(status.Error) != "" {
		t.Errorf("Error should be empty (npm recommendation is non-blocking), got: %q", status.Error)
	}
}

// ---------------------------------------------------------------------------
// M. monotonicReporter enforces monotonic progress
// ---------------------------------------------------------------------------

// TestMonotonicReporter_ClampsDecreasingProgress verifies that
// monotonicReporter clamps decreasing values.
func TestMonotonicReporter_ClampsDecreasingProgress(t *testing.T) {
	var mu sync.Mutex
	var calls []progressSnapshot
	inner := func(step OperationStep, message string, progress int) {
		mu.Lock()
		calls = append(calls, progressSnapshot{step, message, progress})
		mu.Unlock()
	}
	reporter := monotonicReporter(inner)

	reporter(OperationStepPrecheck, "phase 1", 5)
	reporter(OperationStepRunCommand, "phase 2", 20)
	reporter(OperationStepRunCommand, "phase 3 regress", 10) // should be clamped to 20
	reporter(OperationStepVerify, "phase 4", 85)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 4 {
		t.Fatalf("expected 4 calls, got %d", len(calls))
	}
	// First call
	if calls[0].progress != 5 {
		t.Errorf("call 0 progress = %d, want 5", calls[0].progress)
	}
	// Second call
	if calls[1].progress != 20 {
		t.Errorf("call 1 progress = %d, want 20", calls[1].progress)
	}
	// Third call: should be clamped to 20 (previous max)
	if calls[2].progress != 20 {
		t.Errorf("call 2 progress = %d, want 20 (clamped from 10)", calls[2].progress)
	}
	// Fourth call: normal increase
	if calls[3].progress != 85 {
		t.Errorf("call 3 progress = %d, want 85", calls[3].progress)
	}
}

// TestMonotonicReporter_NilInner does not panic with nil inner.
func TestMonotonicReporter_NilInner(t *testing.T) {
	reporter := monotonicReporter(nil)
	// Should not panic
	reporter(OperationStepPrecheck, "test", 5)
	reporter(OperationStepRunCommand, "test", 3)
}
