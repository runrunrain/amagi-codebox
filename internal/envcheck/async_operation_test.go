package envcheck

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// slowSequentialRunner: a sequentialRunner with configurable delay per call
// ---------------------------------------------------------------------------

type slowSequentialRunner struct {
	responses []seqResponse
	delay     time.Duration
	mu        sync.Mutex
	next      int
}

func newSlowSequentialRunner(responses []seqResponse, delay time.Duration) *slowSequentialRunner {
	return &slowSequentialRunner{
		responses: responses,
		delay:     delay,
	}
}

func (r *slowSequentialRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	return r.RunWithSpec(spec)
}

func (r *slowSequentialRunner) RunWithSpec(spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	idx := r.next
	if sequentialRunnerShouldBypassOpenCodeFallback(spec, r.peek(idx)) {
		r.mu.Unlock()
		if r.delay > 0 {
			time.Sleep(r.delay)
		}
		return &platform.ProcessResult{}, errors.New("opencode fallback probe not configured")
	}
	r.next++
	r.mu.Unlock()

	if r.delay > 0 {
		time.Sleep(r.delay)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Note: idx already incremented, need to use idx before next increment
	if idx >= len(r.responses) {
		return &platform.ProcessResult{}, nil
	}
	resp := r.responses[idx]
	return &platform.ProcessResult{Stdout: resp.stdout, Stderr: resp.stderr}, resp.err
}

func (r *slowSequentialRunner) peek(idx int) *seqResponse {
	if idx < 0 || idx >= len(r.responses) {
		return nil
	}
	return &r.responses[idx]
}

func (r *slowSequentialRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helper: poll until operation reaches terminal state
// ---------------------------------------------------------------------------

func waitForOperation(t *testing.T, svc *Service, timeout time.Duration) *OperationState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		op := svc.GetOperationState()
		if op == nil {
			return nil
		}
		if op.Status != OperationStatusRunning {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("operation did not complete within %v", timeout)
	return nil
}

// ---------------------------------------------------------------------------
// 1. StartUpdateTool returns immediately with running state
// ---------------------------------------------------------------------------

func TestStartUpdateTool_ReturnsImmediately(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("version command crashed")}, // pre-check
		{stdout: "10.0.0", err: nil},                             // npm available
		{stdout: "installed", err: nil},                          // install
		{stdout: "opencode v2.0.0", err: nil},                    // verify
		{stdout: "2.0.0", err: nil},                              // latest version
	}, 200*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	start := time.Now()
	op, err := svc.StartUpdateTool(ToolOpenCode)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("StartUpdateTool error: %v", err)
	}
	// Should return in well under the 200ms delay
	if elapsed > 100*time.Millisecond {
		t.Errorf("StartUpdateTool took %v, should return immediately", elapsed)
	}
	if op == nil {
		t.Fatal("expected non-nil OperationState")
	}
	if op.Status != OperationStatusRunning {
		t.Errorf("op.Status = %q, want %q", op.Status, OperationStatusRunning)
	}
	if op.Tool != ToolOpenCode {
		t.Errorf("op.Tool = %q, want %q", op.Tool, ToolOpenCode)
	}
	if op.Kind != OperationKindUpdate {
		t.Errorf("op.Kind = %q, want %q", op.Kind, OperationKindUpdate)
	}
	if op.StartedAt.IsZero() {
		t.Error("op.StartedAt should not be zero")
	}
}

// ---------------------------------------------------------------------------
// 2. StartInstallTool returns immediately with running state
// ---------------------------------------------------------------------------

func TestStartInstallTool_ReturnsImmediately(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("not installed")}, // pre-check
		{stdout: "10.0.0", err: nil},                   // npm available
		{stdout: "installed", err: nil},                // install
		{stdout: "opencode v1.0.0", err: nil},          // verify
		{stdout: "1.0.0", err: nil},                    // latest version
	}, 200*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	start := time.Now()
	op, err := svc.StartInstallTool(ToolOpenCode)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("StartInstallTool error: %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("StartInstallTool took %v, should return immediately", elapsed)
	}
	if op.Status != OperationStatusRunning {
		t.Errorf("op.Status = %q, want %q", op.Status, OperationStatusRunning)
	}
	if op.Kind != OperationKindInstall {
		t.Errorf("op.Kind = %q, want %q", op.Kind, OperationKindInstall)
	}
}

// ---------------------------------------------------------------------------
// 3. Duplicate request for same tool+kind returns current state
// ---------------------------------------------------------------------------

func TestStartUpdateTool_DuplicateReturnsCurrentState(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("broken")}, // pre-check
		{stdout: "10.0.0", err: nil},            // npm available
		{stdout: "installed", err: nil},         // install
		{stdout: "opencode v2.0.0", err: nil},   // verify
		{stdout: "2.0.0", err: nil},             // latest version
	}, 300*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	op1, err := svc.StartUpdateTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("first StartUpdateTool error: %v", err)
	}

	// Second call for same tool+kind should return current state, no error
	op2, err := svc.StartUpdateTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("duplicate StartUpdateTool error: %v", err)
	}
	if op2.ID != op1.ID {
		t.Errorf("duplicate should return same operation ID: got %q, want %q", op2.ID, op1.ID)
	}
	if op2.Status != OperationStatusRunning {
		t.Errorf("duplicate should show running: got %q", op2.Status)
	}
}

// ---------------------------------------------------------------------------
// 4. Busy rejection: different tool while one is running
// ---------------------------------------------------------------------------

func TestStartUpdateTool_BusyWhenDifferentToolRunning(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	_ = writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("broken")}, // pre-check
		{stdout: "10.0.0", err: nil},            // npm available
		{stdout: "installed", err: nil},         // install
		{stdout: "opencode v2.0.0", err: nil},   // verify
		{stdout: "2.0.0", err: nil},             // latest version
	}, 300*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	_, err := svc.StartUpdateTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("first StartUpdateTool error: %v", err)
	}

	// Try to install a different tool while the first is running
	_, err = svc.StartInstallTool(ToolCodex)
	if err == nil {
		t.Fatal("expected ErrBusy when a different tool is running, got nil")
	}
	if !errors.Is(err, ErrBusy) {
		t.Errorf("expected ErrBusy, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 5. Busy rejection: different kind for same tool while running
// ---------------------------------------------------------------------------

func TestStartInstallTool_BusyWhenUpdateRunningForSameTool(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("broken")}, // pre-check
		{stdout: "10.0.0", err: nil},            // npm available
		{stdout: "installed", err: nil},         // install
		{stdout: "opencode v2.0.0", err: nil},   // verify
		{stdout: "2.0.0", err: nil},             // latest version
	}, 300*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	_, err := svc.StartUpdateTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("StartUpdateTool error: %v", err)
	}

	// Try install for same tool while update is running (different kind)
	_, err = svc.StartInstallTool(ToolOpenCode)
	if err == nil {
		t.Fatal("expected ErrBusy when different kind is running for same tool")
	}
	if !errors.Is(err, ErrBusy) {
		t.Errorf("expected ErrBusy, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 6. Operation succeeds and final state is queryable
// ---------------------------------------------------------------------------

func TestStartUpdateTool_Succeeds_FinalStateQueryable(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("version broken")}, // pre-check
		{stdout: "10.0.0", err: nil},                    // npm available
		{stdout: "installed", err: nil},                 // install
		{stdout: "opencode v3.0.0", err: nil},           // verify
		{stdout: "3.0.0", err: nil},                     // latest version
	}, 50*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	op, err := svc.StartUpdateTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("StartUpdateTool error: %v", err)
	}
	if op.Status != OperationStatusRunning {
		t.Fatalf("initial status = %q, want running", op.Status)
	}

	final := waitForOperation(t, svc, 5*time.Second)
	if final == nil {
		t.Fatal("expected non-nil final operation state")
	}
	if final.Status != OperationStatusSucceeded {
		t.Errorf("final.Status = %q, want %q; message=%s error=%s",
			final.Status, OperationStatusSucceeded, final.Message, final.Error)
	}
	if final.Result == nil {
		t.Fatal("expected non-nil result")
	}
	if !final.Result.Success {
		t.Errorf("result.Success = false; Message: %s, Error: %s", final.Result.Message, final.Result.Error)
	}
	if final.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}
	if !final.CacheRefreshed {
		t.Error("expected CacheRefreshed = true after successful operation")
	}
}

// ---------------------------------------------------------------------------
// 7. Operation fails and final state reflects error
// ---------------------------------------------------------------------------

func TestStartInstallTool_Fails_FinalStateReflectsError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)

	// Empty PATH, no executables -- install will fail
	svc := newTestService() // no mock responses => commands fail

	op, err := svc.StartInstallTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("StartInstallTool error: %v", err)
	}
	if op.Status != OperationStatusRunning {
		t.Fatalf("initial status = %q, want running", op.Status)
	}

	final := waitForOperation(t, svc, 5*time.Second)
	if final == nil {
		t.Fatal("expected non-nil final operation state")
	}
	if final.Status != OperationStatusFailed {
		t.Errorf("final.Status = %q, want %q", final.Status, OperationStatusFailed)
	}
	if final.Error == "" {
		t.Error("expected non-empty error message")
	}
	if final.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}
}

// ---------------------------------------------------------------------------
// 8. Unsupported tool returns error immediately
// ---------------------------------------------------------------------------

func TestStartInstallTool_UnsupportedTool(t *testing.T) {
	svc := newTestService()
	_, err := svc.StartInstallTool(CLITool("nonexistent"))
	if err == nil {
		t.Fatal("expected error for unsupported tool")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error should mention unsupported: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 9. GetEnvCheckSnapshot returns combined data
// ---------------------------------------------------------------------------

func TestGetEnvCheckSnapshot_CombinedData(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		responseFor("opencode", "opencode v1.0.0", nil),
	)
	svc.CheckAll()

	snapshot := svc.GetEnvCheckSnapshot()
	if snapshot == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snapshot.Status == nil {
		t.Fatal("expected non-nil Status in snapshot")
	}
	// No operation running
	if snapshot.Operation != nil {
		t.Error("expected nil Operation when idle")
	}
}

func TestGetEnvCheckSnapshot_WithRunningOperation(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("broken")}, // pre-check
		{stdout: "10.0.0", err: nil},            // npm available
		{stdout: "installed", err: nil},         // install
		{stdout: "opencode v2.0.0", err: nil},   // verify
		{stdout: "2.0.0", err: nil},             // latest version
	}, 200*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	_, err := svc.StartUpdateTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("StartUpdateTool error: %v", err)
	}

	snapshot := svc.GetEnvCheckSnapshot()
	if snapshot == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snapshot.Operation == nil {
		t.Fatal("expected non-nil Operation in snapshot during running operation")
	}
	if snapshot.Operation.Status != OperationStatusRunning {
		t.Errorf("operation status = %q, want running", snapshot.Operation.Status)
	}
}

// ---------------------------------------------------------------------------
// 10. New operation can start after previous one completes
// ---------------------------------------------------------------------------

func TestStartUpdateTool_CanStartAfterCompletion(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	// First operation completes quickly
	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("broken")},
		{stdout: "10.0.0", err: nil},          // npm probe (populateCanInstall)
		{stdout: "installed", err: nil},       // install command
		{stdout: "opencode v1.0.0", err: nil}, // verify version
		{stdout: "1.0.0", err: nil},           // enrichment (latest version)
		{stdout: "opencode v1.0.0", err: nil}, // post-success refresh version
	}, 10*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	op1, _ := svc.StartUpdateTool(ToolOpenCode)
	final1 := waitForOperation(t, svc, 5*time.Second)
	if final1.Status != OperationStatusSucceeded {
		t.Fatalf("first op status = %q, want succeeded", final1.Status)
	}

	// Now start a new operation - should succeed
	// Note: npm availability is already cached from the first operation,
	// so no npm --version probe call goes to the runner.
	runner2 := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("broken again")}, // pre-check
		{stdout: "installed", err: nil},               // install command
		{stdout: "opencode v2.0.0", err: nil},         // verify version
		{stdout: "2.0.0", err: nil},                   // enrichment (latest version)
		{stdout: "opencode v2.0.0", err: nil},         // post-success refresh version
	}, 10*time.Millisecond)
	svc.processRunner = runner2

	op2, err := svc.StartUpdateTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("second StartUpdateTool error: %v", err)
	}
	if op2.ID == op1.ID {
		t.Error("second operation should have a different ID")
	}

	final2 := waitForOperation(t, svc, 5*time.Second)
	if final2.Status != OperationStatusSucceeded {
		t.Errorf("second op status = %q, want succeeded", final2.Status)
	}
}

// ---------------------------------------------------------------------------
// 11. GetOperationState returns nil when idle
// ---------------------------------------------------------------------------

func TestGetOperationState_Idle(t *testing.T) {
	svc := newTestService()
	op := svc.GetOperationState()
	if op != nil {
		t.Error("expected nil OperationState when idle")
	}
}

// ---------------------------------------------------------------------------
// 12. GetOperationState returns defensive copy
// ---------------------------------------------------------------------------

func TestGetOperationState_DefensiveCopy(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("broken")},
		{stdout: "10.0.0", err: nil},
		{stdout: "installed", err: nil},
		{stdout: "opencode v1.0.0", err: nil},
		{stdout: "1.0.0", err: nil},
	}, 200*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	_, _ = svc.StartUpdateTool(ToolOpenCode)

	op1 := svc.GetOperationState()
	op2 := svc.GetOperationState()

	// Mutating op1 should not affect op2
	op1.Message = "mutated"
	if op2.Message == "mutated" {
		t.Error("GetOperationState should return a defensive copy")
	}
}

// ---------------------------------------------------------------------------
// 13. Concurrent access safety for operations
// ---------------------------------------------------------------------------

func TestStartUpdateTool_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: errors.New("broken")},
		{stdout: "10.0.0", err: nil},
		{stdout: "installed", err: nil},
		{stdout: "opencode v1.0.0", err: nil},
		{stdout: "1.0.0", err: nil},
	}, 200*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	var wg sync.WaitGroup
	errCh := make(chan error, 40)

	// Start the operation first
	_, _ = svc.StartUpdateTool(ToolOpenCode)

	// Concurrent reads while operation runs
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = svc.GetOperationState()
		}()
		go func() {
			defer wg.Done()
			snap := svc.GetEnvCheckSnapshot()
			if snap == nil {
				errCh <- errors.New("snapshot is nil")
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 14. Install failure (command not found) results in failed state
// ---------------------------------------------------------------------------

func TestStartInstallTool_CommandNotFound_Fails(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)

	// Empty mock: all commands fail
	svc := newTestService()

	op, err := svc.StartInstallTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("StartInstallTool error: %v", err)
	}
	if op.Status != OperationStatusRunning {
		t.Fatalf("expected running, got %q", op.Status)
	}

	final := waitForOperation(t, svc, 5*time.Second)
	if final.Status != OperationStatusFailed {
		t.Errorf("final.Status = %q, want failed", final.Status)
	}
}

// ---------------------------------------------------------------------------
// 15. Timeout results in OperationStatusTimeout (not Failed)
// ---------------------------------------------------------------------------

func TestStartUpdateTool_Timeout_StatusTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "opencode v1.0.0", err: nil},                         // pre-check version
		{stdout: "2.0.0", err: nil},                                   // latest version (enrichment)
		{stdout: "10.0.0", err: nil},                                  // npm available
		{stdout: "", err: fmt.Errorf("command timed out after 120s")}, // timeout!
	}, 10*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	_, err := svc.StartUpdateTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("StartUpdateTool error: %v", err)
	}

	final := waitForOperation(t, svc, 5*time.Second)
	if final.Status != OperationStatusTimeout {
		t.Errorf("final.Status = %q, want %q; error: %s", final.Status, OperationStatusTimeout, final.Error)
	}
}

func TestCleanClaudeCode_UsesOperationStateAndBlocksOtherOperations(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	runner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: nil}, // npm uninstall
	}, 200*time.Millisecond)
	svc := NewServiceWithRunner(runner)

	done := make(chan error, 1)
	go func() {
		_, err := svc.CleanClaudeCode(InstallMethodNPM)
		done <- err
	}()

	var op *OperationState
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		op = svc.GetOperationState()
		if op != nil && op.Status == OperationStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if op == nil || op.Status != OperationStatusRunning {
		t.Fatalf("expected running uninstall operation, got %+v", op)
	}
	if op.Tool != ToolClaudeCode {
		t.Fatalf("operation tool = %q, want %q", op.Tool, ToolClaudeCode)
	}
	if op.Kind != OperationKindUninstall {
		t.Fatalf("operation kind = %q, want %q", op.Kind, OperationKindUninstall)
	}

	if _, err := svc.StartInstallTool(ToolOpenCode); !errors.Is(err, ErrBusy) {
		t.Fatalf("StartInstallTool during uninstall error = %v, want ErrBusy", err)
	}
	if _, err := svc.InstallClaudeCodeWithMethod(ClaudeInstallNPM); !errors.Is(err, ErrBusy) {
		t.Fatalf("InstallClaudeCodeWithMethod during uninstall error = %v, want ErrBusy", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("CleanClaudeCode returned unexpected error: %v", err)
	}
	if op := svc.GetOperationState(); op != nil {
		t.Fatalf("expected operation state to be cleared after uninstall, got %+v", op)
	}
}

func TestCleanClaudeCode_AllowsImmediateInstallAfterSynchronousRefresh(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	cleanRunner := newSlowSequentialRunner([]seqResponse{
		{stdout: "", err: nil}, // npm uninstall
	}, 1*time.Millisecond)
	svc := NewServiceWithRunner(cleanRunner)

	_, cleanErr := svc.CleanClaudeCode(InstallMethodNPM)
	if cleanErr != nil {
		t.Fatalf("CleanClaudeCode returned unexpected error: %v", cleanErr)
	}

	installRunner := newSlowSequentialRunner([]seqResponse{
		{stdout: "10.0.0", err: nil},    // npm availability
		{stdout: "installed", err: nil}, // npm install command
	}, 1*time.Millisecond)
	svc.processRunner = installRunner

	_, installErr := svc.InstallClaudeCodeWithMethod(ClaudeInstallNPM)
	if errors.Is(installErr, ErrBusy) {
		t.Fatalf("immediate install after uninstall returned ErrBusy")
	}
}

func TestStartInstallClaudeCodeWithMethod_ReportsNativeProgress(t *testing.T) {
	runner := &nativeBootstrapTestRunner{createOnClaudeInstall: true, claudeInstallWait: 80 * time.Millisecond}
	svc := prepareNativeBootstrapTest(t, runner)

	op, err := svc.StartInstallClaudeCodeWithMethod(ClaudeInstallNative)
	if err != nil {
		t.Fatalf("StartInstallClaudeCodeWithMethod error: %v", err)
	}
	if op.Status != OperationStatusRunning {
		t.Fatalf("initial status = %q, want running", op.Status)
	}
	if op.Tool != ToolClaudeCode || op.Kind != OperationKindInstall {
		t.Fatalf("operation = %+v, want Claude Code install", op)
	}
	if !strings.Contains(op.Message, "Native") {
		t.Fatalf("initial message should mention Native method, got %q", op.Message)
	}

	var observed *OperationState
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current := svc.GetOperationState()
		if current != nil && current.Status == OperationStatusRunning && current.Progress > 0 && current.Message != "" {
			observed = current
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if observed == nil {
		t.Fatal("expected to observe visible native install progress")
	}
	if observed.Step == "" {
		t.Fatalf("expected progress step to be populated, got %+v", observed)
	}

	final := waitForOperation(t, svc, 5*time.Second)
	if final == nil || final.Progress != 100 || final.FinishedAt == nil {
		t.Fatalf("expected terminal operation state with progress 100, got %+v", final)
	}
}
