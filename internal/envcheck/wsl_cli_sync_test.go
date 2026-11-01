package envcheck

// wsl_cli_sync_test.go — 安装/升级双侧联动（P2-C，direction §2.2）的入口级
// 回归：async StartInstallTool/StartUpdateTool、sync Install/Update/
// InstallClaudeCodeWithMethod、fix install_claude_method 的 WSL 回调触发与
// 门控（Windows 失败 / uninstall / headroom 不触发），以及异步操作内
// wsl_sync 步骤可见性与部分失败语义（OperationState.Status 仍只表示
// Windows 侧，WSL 结论单独装在 Result.WSL）。

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// wslSyncRecorder 记录注入回调的调用与回调执行瞬间观察到的操作状态。
type wslSyncRecorder struct {
	mu    sync.Mutex
	calls []struct {
		tool CLITool
		kind OperationKind
	}
	stepInsideCall OperationStep
	result         *WSLSyncResult // 每次调用返回的预设结果（nil=返回 nil）
}

func (r *wslSyncRecorder) install(svc *Service, result *WSLSyncResult) {
	svc.SetWSLCLISync(func(tool CLITool, kind OperationKind) *WSLSyncResult {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, struct {
			tool CLITool
			kind OperationKind
		}{tool, kind})
		if op := svc.GetOperationState(); op != nil {
			r.stepInsideCall = op.Step
		}
		return result
	})
}

func (r *wslSyncRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *wslSyncRecorder) lastCall() (CLITool, OperationKind) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return "", ""
	}
	c := r.calls[len(r.calls)-1]
	return c.tool, c.kind
}

// successfulOpenCodeResponses mirrors the canonical opencode install/update
// mock chain (precheck broken → npm available → install → verify → latest).
func successfulOpenCodeResponses() []seqResponse {
	return []seqResponse{
		{stdout: "", err: errors.New("version broken")}, // pre-check
		{stdout: "10.0.0", err: nil},                    // npm available
		{stdout: "installed", err: nil},                 // install
		{stdout: "opencode v3.0.0", err: nil},           // verify
		{stdout: "3.0.0", err: nil},                     // latest version
	}
}

func newOpenCodeInstallService(t *testing.T) *Service {
	t.Helper()
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)
	return NewServiceWithRunner(newSlowSequentialRunner(successfulOpenCodeResponses(), 0))
}

// --- async StartInstallTool：成功后填 Result.WSL，步骤经过 wsl_sync ---

func TestAttachWSLSync_AsyncInstallSuccess_FillsResultAndReportsStep(t *testing.T) {
	svc := newOpenCodeInstallService(t)
	rec := &wslSyncRecorder{result: &WSLSyncResult{Status: WSLSyncSucceeded, Distro: "Ubuntu", Version: "3.0.0"}}
	rec.install(svc, rec.result)

	op, err := svc.StartInstallTool(ToolOpenCode)
	if err != nil {
		t.Fatalf("StartInstallTool error: %v", err)
	}
	if op.Status != OperationStatusRunning {
		t.Fatalf("initial status = %q, want running", op.Status)
	}

	final := waitForOperation(t, svc, 5*time.Second)
	if final.Status != OperationStatusSucceeded {
		t.Fatalf("final.Status = %q, want succeeded (Windows 侧语义不变); message=%s error=%s",
			final.Status, final.Message, final.Error)
	}
	if final.Result == nil || final.Result.WSL == nil {
		t.Fatalf("expected final.Result.WSL to be filled, got %+v", final.Result)
	}
	if final.Result.WSL.Status != WSLSyncSucceeded {
		t.Errorf("Result.WSL.Status = %q, want %q", final.Result.WSL.Status, WSLSyncSucceeded)
	}
	if gotTool, gotKind := rec.lastCall(); gotTool != ToolOpenCode || gotKind != OperationKindInstall {
		t.Errorf("callback = (%s,%s), want (opencode,install)", gotTool, gotKind)
	}
	if rec.stepInsideCall != OperationStepWSLSync {
		t.Errorf("step observed inside callback = %q, want %q (异步操作内 WSL 步骤可见)",
			rec.stepInsideCall, OperationStepWSLSync)
	}
}

// --- async StartUpdateTool：成功后同样带 WSL，kind=update ---

func TestAttachWSLSync_AsyncUpdateSuccess_FillsResult(t *testing.T) {
	svc := newOpenCodeInstallService(t)
	rec := &wslSyncRecorder{result: &WSLSyncResult{Status: WSLSyncSkipped, Distro: "Ubuntu"}}
	rec.install(svc, rec.result)

	if _, err := svc.StartUpdateTool(ToolOpenCode); err != nil {
		t.Fatalf("StartUpdateTool error: %v", err)
	}
	final := waitForOperation(t, svc, 5*time.Second)
	if final.Status != OperationStatusSucceeded {
		t.Fatalf("final.Status = %q, want succeeded; message=%s error=%s", final.Status, final.Message, final.Error)
	}
	if final.Result == nil || final.Result.WSL == nil {
		t.Fatalf("expected final.Result.WSL, got %+v", final.Result)
	}
	if final.Result.WSL.Status != WSLSyncSkipped {
		t.Errorf("Result.WSL.Status = %q, want %q", final.Result.WSL.Status, WSLSyncSkipped)
	}
	if gotTool, gotKind := rec.lastCall(); gotTool != ToolOpenCode || gotKind != OperationKindUpdate {
		t.Errorf("callback = (%s,%s), want (opencode,update)", gotTool, gotKind)
	}
}

// --- async：Windows 成功而 WSL 失败 → 操作 succeeded + WSL=failed（不伪报全成功） ---

func TestAttachWSLSync_AsyncPartialFailure_KeepsWindowsStatusAndRecordsFailure(t *testing.T) {
	svc := newOpenCodeInstallService(t)
	rec := &wslSyncRecorder{result: &WSLSyncResult{Status: WSLSyncFailed, Distro: "Ubuntu", Error: "npm install 失败: 网络超时"}}
	rec.install(svc, rec.result)

	if _, err := svc.StartInstallTool(ToolOpenCode); err != nil {
		t.Fatalf("StartInstallTool error: %v", err)
	}
	final := waitForOperation(t, svc, 5*time.Second)
	if final.Status != OperationStatusSucceeded {
		t.Fatalf("final.Status = %q, want succeeded (Success/Status 仍只表示 Windows 侧)", final.Status)
	}
	if final.Result == nil || final.Result.WSL == nil || final.Result.WSL.Status != WSLSyncFailed {
		t.Fatalf("expected Result.WSL.Status=failed, got %+v", final.Result)
	}
	if final.Result.WSL.Error == "" {
		t.Error("partial failure must keep the WSL-side error text")
	}
}

// --- Windows 失败：回调不触发，Result.WSL 为 nil ---

func TestAttachWSLSync_WindowsFailure_NotCalled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir) // 空 PATH：安装必然失败
	svc := NewServiceWithRunner(newSlowSequentialRunner(nil, 0))
	rec := &wslSyncRecorder{result: &WSLSyncResult{Status: WSLSyncSucceeded}}
	rec.install(svc, rec.result)

	if _, err := svc.StartInstallTool(ToolOpenCode); err != nil {
		t.Fatalf("StartInstallTool error: %v", err)
	}
	final := waitForOperation(t, svc, 5*time.Second)
	if final.Status != OperationStatusFailed {
		t.Fatalf("final.Status = %q, want failed", final.Status)
	}
	if rec.count() != 0 {
		t.Errorf("WSL callback must not fire on Windows failure, got %d calls", rec.count())
	}
	if final.Result != nil && final.Result.WSL != nil {
		t.Errorf("Result.WSL must stay nil on Windows failure, got %+v", final.Result.WSL)
	}
}

// --- uninstall（经 runOperation 成功路径）：kind 门控不触发 ---

func TestAttachWSLSync_UninstallSuccess_NotCalled(t *testing.T) {
	f := newNPMLayoutFixture(t, "opencode")
	f.installFakePackage(t, "opencode-ai", "opencode")
	runner := &specRunner{respond: f.npmResponder(ToolOpenCode, "opencode", "opencode-ai", true)}
	svc := NewServiceWithRunner(runner)
	rec := &wslSyncRecorder{result: &WSLSyncResult{Status: WSLSyncSucceeded}}
	rec.install(svc, rec.result)

	if _, err := svc.CheckOne(ToolOpenCode); err != nil {
		t.Fatalf("CheckOne precondition: %v", err)
	}
	if _, err := svc.startUninstallTool(ToolOpenCode); err != nil {
		t.Fatalf("startUninstallTool: %v", err)
	}
	op := waitForTerminalOperation(t, svc, 15*time.Second)
	if op.Status != OperationStatusSucceeded || op.Result == nil || !op.Result.Success {
		t.Fatalf("uninstall should succeed, got %+v", op)
	}
	if rec.count() != 0 {
		t.Errorf("WSL callback must not fire for uninstall, got %d calls", rec.count())
	}
	if op.Result.WSL != nil {
		t.Errorf("uninstall Result.WSL must stay nil, got %+v", op.Result.WSL)
	}
}

// --- headroom（sync Install 成功）：工具门控不触发 ---

func TestAttachWSLSync_HeadroomInstallSuccess_NotCalled(t *testing.T) {
	prevAttempts, prevDelay := installRecheckAttempts, installRecheckDelay
	installRecheckAttempts, installRecheckDelay = 1, 0
	t.Cleanup(func() { installRecheckAttempts, installRecheckDelay = prevAttempts, prevDelay })

	pathDir := t.TempDir()
	writeTestExecutable(t, pathDir, "python3")
	t.Setenv("PATH", pathDir)
	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	svc := NewServiceWithRunner(&headroomVenvInstallRunner{venvDir: venvDir})
	svc.SetHeadroomVenvDir(venvDir)
	rec := &wslSyncRecorder{result: &WSLSyncResult{Status: WSLSyncSucceeded}}
	rec.install(svc, rec.result)

	result, err := svc.Install(ToolHeadroom)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("Install(ToolHeadroom) should succeed, got result=%+v err=%v", result, err)
	}
	if rec.count() != 0 {
		t.Errorf("WSL callback must not fire for headroom, got %d calls", rec.count())
	}
	if result.WSL != nil {
		t.Errorf("headroom Result.WSL must stay nil, got %+v", result.WSL)
	}
}

// --- sync Install / Update：结果同样携带 WSL ---

func TestAttachWSLSync_SyncInstallAndUpdate_CarryWSL(t *testing.T) {
	t.Run("Install", func(t *testing.T) {
		svc := newOpenCodeInstallService(t)
		rec := &wslSyncRecorder{result: &WSLSyncResult{Status: WSLSyncSucceeded, Distro: "Ubuntu", Version: "2.0.0"}}
		rec.install(svc, rec.result)
		result, err := svc.Install(ToolOpenCode)
		if err != nil || result == nil || !result.Success {
			t.Fatalf("Install should succeed, got result=%+v err=%v", result, err)
		}
		if result.WSL == nil || result.WSL.Status != WSLSyncSucceeded {
			t.Fatalf("sync Install result.WSL = %+v, want succeeded", result.WSL)
		}
		if gotTool, gotKind := rec.lastCall(); gotTool != ToolOpenCode || gotKind != OperationKindInstall {
			t.Errorf("callback = (%s,%s), want (opencode,install)", gotTool, gotKind)
		}
	})
	t.Run("Update", func(t *testing.T) {
		svc := newOpenCodeInstallService(t)
		rec := &wslSyncRecorder{result: &WSLSyncResult{Status: WSLSyncSucceeded, Distro: "Ubuntu", Version: "3.0.0"}}
		rec.install(svc, rec.result)
		result, err := svc.Update(ToolOpenCode)
		if err != nil || result == nil || !result.Success {
			t.Fatalf("Update should succeed, got result=%+v err=%v", result, err)
		}
		if result.WSL == nil || result.WSL.Status != WSLSyncSucceeded {
			t.Fatalf("sync Update result.WSL = %+v, want succeeded", result.WSL)
		}
		if gotTool, gotKind := rec.lastCall(); gotTool != ToolOpenCode || gotKind != OperationKindUpdate {
			t.Errorf("callback = (%s,%s), want (opencode,update)", gotTool, gotKind)
		}
	})
}

// --- sync InstallClaudeCodeWithMethod（指定通道）：结果携带 WSL ---

func TestAttachWSLSync_SyncClaudeWithMethod_CarriesWSL(t *testing.T) {
	runner := &nativeBootstrapTestRunner{createOnClaudeInstall: true}
	svc := prepareNativeBootstrapTest(t, runner)
	rec := &wslSyncRecorder{result: &WSLSyncResult{Status: WSLSyncSucceeded, Distro: "Ubuntu", Version: "1.0.0"}}
	rec.install(svc, rec.result)

	result, err := svc.InstallClaudeCodeWithMethod(ClaudeInstallNative)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("InstallClaudeCodeWithMethod should succeed, got result=%+v err=%v", result, err)
	}
	if result.WSL == nil || result.WSL.Status != WSLSyncSucceeded {
		t.Fatalf("result.WSL = %+v, want succeeded", result.WSL)
	}
	if gotTool, gotKind := rec.lastCall(); gotTool != ToolClaudeCode || gotKind != OperationKindInstall {
		t.Errorf("callback = (%s,%s), want (claude_code,install)", gotTool, gotKind)
	}
}

// --- fix install_claude_method：WSL 失败时 FixActionResult.Error 非空（不漏报） ---

func TestAttachWSLSync_FixInstallClaudeMethod_PropagatesWSLFailure(t *testing.T) {
	runner := &nativeBootstrapTestRunner{createOnClaudeInstall: true}
	svc := prepareNativeBootstrapTest(t, runner)
	rec := &wslSyncRecorder{result: &WSLSyncResult{Status: WSLSyncFailed, Distro: "Ubuntu", Error: "npm i -g @anthropic-ai/claude-code@latest 失败"}}
	rec.install(svc, rec.result)

	result, err := svc.RunFixAction(FixActionRequest{Action: SolutionInstallClaudeMethod, Method: string(ClaudeInstallNative)})
	if err != nil {
		t.Fatalf("RunFixAction error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("Success 仍表示 Windows 侧，应为 true, got %+v", result)
	}
	if result.Error == "" {
		t.Error("WSL 同步失败必须写入 FixActionResult.Error（部分失败不漏报）")
	}
	if !strings.Contains(result.Message, result.Error) {
		t.Errorf("WSL 错误也应追加到 Message, message=%q error=%q", result.Message, result.Error)
	}
}

// --- 未注入回调：全部入口行为不变（nil 安全） ---

func TestAttachWSLSync_NoCallback_InstallStillSucceeds(t *testing.T) {
	svc := newOpenCodeInstallService(t) // 不注入回调
	result, err := svc.Install(ToolOpenCode)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("Install should succeed without WSL callback, got result=%+v err=%v", result, err)
	}
	if result.WSL != nil {
		t.Errorf("result.WSL must stay nil without callback, got %+v", result.WSL)
	}
}
