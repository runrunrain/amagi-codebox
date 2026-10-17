package main

// app_wsl_supply_core_test.go — wsl-goal-closure 供给内核修复的 App 侧回归
//（SA-08 single-flight 补推死锁、SA-09 共享小文件启动补漏/撤回闸门、
// steer#3 必需模型供给严谨分类）。与 app_wsl_supply_test.go 的既有桩共用
// 同一测试包；本文件自含桩，避免与并行修复轮（C3/C4）的 hunk 冲突。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/wslsupply"
)

// ---------------------------------------------------------------------------
// SA-08：single-flight 的补推死锁。
// ---------------------------------------------------------------------------

// blockingSupplyRunner 阻塞在 RunUnits 内直到放行，用于在推送执行中制造
// 第二次保存（rerun 标记）。
type blockingSupplyRunner struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{} // 每次 RunUnits 进入时发信号
	release chan struct{} // 关闭后放行
}

func (s *blockingSupplyRunner) RunUnits(_ context.Context, _ string, _ wslsupply.CLI, _ wslsupply.Reason, _ []wslsupply.UnitID) (wslsupply.RunResult, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	s.entered <- struct{}{}
	<-s.release
	return wslsupply.RunResult{}, nil
}
func (s *blockingSupplyRunner) MarkDirty(string, uint64) error { return nil }
func (s *blockingSupplyRunner) GetStatus(string, wslsupply.CLI) wslsupply.TargetStatus {
	return wslsupply.TargetStatus{}
}
func (s *blockingSupplyRunner) Compensate(context.Context, ...*wslsupply.CompensationHandle) []wslsupply.CompensationOutcome {
	return nil
}
func (s *blockingSupplyRunner) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// 推送执行中再次保存（置 rerun）→ 补推必须真正执行第二轮，且 running 最终
// 释放（后续保存还能继续推进）。
func TestPushWSLSupplyOnceRerunLoopRecovers(t *testing.T) {
	origRunning, origDefault := wslDistroRunningFn, wslDefaultDistroFn
	t.Cleanup(func() {
		wslDistroRunningFn = origRunning
		wslDefaultDistroFn = origDefault
	})
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	wslDistroRunningFn = func(string) bool { return true }

	runner := &blockingSupplyRunner{entered: make(chan struct{}, 16), release: make(chan struct{})}
	app := newTestApp(t)
	app.WSLSupply = runner

	done := make(chan struct{})
	go func() { app.pushWSLSupplyOnce(); close(done) }()
	<-runner.entered // 第一轮执行中

	// 执行中的第二次保存：按 SA-08 旧实现此处置 rerun 后递归死锁；
	// 新实现只置标记，由工作循环消费。
	go app.pushWSLSupplyOnce() // nolint — 异步置 rerun

	// 给置标记一点时间，然后放行第一轮。
	time.Sleep(50 * time.Millisecond)
	close(runner.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("push worker must finish after rerun consumption")
	}
	// rerun 被消费：第二轮已执行（calls >= 2）。
	if got := runner.count(); got < 2 {
		t.Fatalf("rerun must trigger a second push round, calls=%d", got)
	}
	// running 已释放：新的推送仍能执行（不再永久停摆）。
	runner2 := &blockingSupplyRunner{entered: make(chan struct{}, 1), release: make(chan struct{})}
	app.WSLSupply = runner2
	go app.pushWSLSupplyOnce()
	select {
	case <-runner2.entered:
		close(runner2.release)
	case <-time.After(2 * time.Second):
		t.Fatal("running flag must be released after worker exit (SA-08 deadlock)")
	}
}

// ---------------------------------------------------------------------------
// SA-09：共享小文件启动补漏/撤回闸门。
// ---------------------------------------------------------------------------

// gateStubRunner 记录调用并返回固定结果。
type gateStubRunner struct {
	mu     sync.Mutex
	calls  []string
	result wslsupply.RunResult
	runErr error
	known  map[wslsupply.UnitID]bool
	status wslsupply.TargetStatus // GetStatus 返回值（managed-copy 证明面）
}

func (s *gateStubRunner) RunUnits(_ context.Context, _ string, cli wslsupply.CLI, reason wslsupply.Reason, units []wslsupply.UnitID) (wslsupply.RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, string(cli)+"|"+string(reason)+"|"+unitNames(units))
	if s.runErr != nil {
		return wslsupply.RunResult{}, s.runErr
	}
	return s.result, nil
}
func (s *gateStubRunner) MarkDirty(string, uint64) error { return nil }
func (s *gateStubRunner) GetStatus(string, wslsupply.CLI) wslsupply.TargetStatus {
	return s.status
}

// SensitiveCopyPending 实现 PR01 的协调器能力面：status 携带 vision 单元
// 登记且未撤回时视为副本可能存在（与真实协调器判定同向）。
func (s *gateStubRunner) SensitiveCopyPending(_ string, unit wslsupply.UnitID) bool {
	if unit != wslsupply.UnitSharedVisionExport {
		return false
	}
	for _, u := range s.status.Units {
		if u.Unit != wslsupply.UnitSharedVisionExport {
			continue
		}
		if strings.HasPrefix(u.LastOutcome, "withdrawn") {
			return false
		}
		return u.Consented
	}
	return false
}

func (s *gateStubRunner) LegacySensitiveCopyPending(string) bool { return false }
func (s *gateStubRunner) Compensate(context.Context, ...*wslsupply.CompensationHandle) []wslsupply.CompensationOutcome {
	return nil
}
func (s *gateStubRunner) HasUnit(u wslsupply.UnitID) bool {
	if s.known == nil {
		return true
	}
	return s.known[u]
}
func (s *gateStubRunner) callsSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}
func unitNames(units []wslsupply.UnitID) string {
	names := make([]string, len(units))
	for i, u := range units {
		names[i] = string(u)
	}
	return strings.Join(names, ",")
}

func TestRunWSLSharedFilesAtLaunchGate(t *testing.T) {
	t.Run("nil runner / empty distro is a no-op", func(t *testing.T) {
		if err := runWSLSharedFilesAtLaunch(context.Background(), nil, "Ubuntu"); err != nil {
			t.Fatal(err)
		}
		if err := runWSLSharedFilesAtLaunch(context.Background(), &gateStubRunner{}, "  "); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("runs only shared small-file units at launch, no skills", func(t *testing.T) {
		runner := &gateStubRunner{}
		if err := runWSLSharedFilesAtLaunch(context.Background(), runner, "Ubuntu"); err != nil {
			t.Fatal(err)
		}
		calls := runner.callsSnapshot()
		if len(calls) != 1 {
			t.Fatalf("expected one shared-scope run, got %v", calls)
		}
		if !strings.HasPrefix(calls[0], "shared|launch|") {
			t.Fatalf("must run CLICommon with ReasonLaunch, got %q", calls[0])
		}
		if !strings.Contains(calls[0], "shared/vision-export") || !strings.Contains(calls[0], "shared/modality-kb") {
			t.Fatalf("must cover both shared units, got %q", calls[0])
		}
		if strings.Contains(calls[0], "skill") {
			t.Fatalf("skills must never enter the launch gate, got %q", calls[0])
		}
	})

	t.Run("unconfirmed withdrawal blocks with explicit error", func(t *testing.T) {
		runner := &gateStubRunner{result: wslsupply.RunResult{Results: []wslsupply.UnitResult{{
			Unit: wslsupply.UnitSharedVisionExport, Outcome: wslsupply.UnitBlocked,
			Detail: "withdrawal-unconfirmed-frozen",
		}}}}
		err := runWSLSharedFilesAtLaunch(context.Background(), runner, "Ubuntu")
		if err == nil || !strings.Contains(err.Error(), "未确认撤回") {
			t.Fatalf("unconfirmed withdrawal must return explicit error, got %v", err)
		}
	})

	t.Run("no consent / no copy does not block normal launch", func(t *testing.T) {
		runner := &gateStubRunner{result: wslsupply.RunResult{Results: []wslsupply.UnitResult{{
			Unit: wslsupply.UnitSharedVisionExport, Outcome: wslsupply.UnitBlocked,
			Detail: "awaiting-consent",
		}, {
			Unit: wslsupply.UnitSharedModalityKB, Outcome: wslsupply.UnitBlocked,
			Detail: "unmanaged-existing",
		}}}}
		if err := runWSLSharedFilesAtLaunch(context.Background(), runner, "Ubuntu"); err != nil {
			t.Fatalf("expected states must not block launch: %v", err)
		}
	})

	t.Run("withdrawal removal passes, transient failure without managed copy passes", func(t *testing.T) {
		runner := &gateStubRunner{result: wslsupply.RunResult{Results: []wslsupply.UnitResult{{
			Unit: wslsupply.UnitSharedVisionExport, Outcome: wslsupply.UnitApplied,
			Detail: "withdrawn",
		}, {
			Unit: wslsupply.UnitSharedModalityKB, Outcome: wslsupply.UnitFailed,
			Detail: "write-failed",
		}}}}
		if err := runWSLSharedFilesAtLaunch(context.Background(), runner, "Ubuntu"); err != nil {
			t.Fatalf("withdrawn + non-sensitive modality failure must not block: %v", err)
		}
	})

	// steer#7：受管敏感副本可能存在时，任何不能确认安全的结果必须阻断。
	managedCopyStatus := wslsupply.TargetStatus{Units: []wslsupply.UnitStatus{{
		Unit: wslsupply.UnitSharedVisionExport, Consented: true, LastOutcome: "written",
	}}}

	t.Run("run error with managed copy blocks", func(t *testing.T) {
		runner := &gateStubRunner{runErr: errors.New("supply runner broke"), status: managedCopyStatus}
		if err := runWSLSharedFilesAtLaunch(context.Background(), runner, "Ubuntu"); err == nil {
			t.Fatal("run error with a possible managed copy must block")
		}
	})

	t.Run("withdrawal-remove-failed with managed copy blocks", func(t *testing.T) {
		runner := &gateStubRunner{status: managedCopyStatus, result: wslsupply.RunResult{Results: []wslsupply.UnitResult{{
			Unit: wslsupply.UnitSharedVisionExport, Outcome: wslsupply.UnitFailed,
			Detail: "withdrawal-remove-failed",
		}}}}
		if err := runWSLSharedFilesAtLaunch(context.Background(), runner, "Ubuntu"); err == nil {
			t.Fatal("failed withdrawal removal with a managed copy must block")
		}
	})

	t.Run("staging unverified with managed copy blocks", func(t *testing.T) {
		runner := &gateStubRunner{status: managedCopyStatus, result: wslsupply.RunResult{Results: []wslsupply.UnitResult{{
			Unit: wslsupply.UnitSharedVisionExport, Outcome: wslsupply.UnitFailed,
			Detail: "write-failed: supply staging isolation unverified: mode=\"\" ok=false",
		}}}}
		if err := runWSLSharedFilesAtLaunch(context.Background(), runner, "Ubuntu"); err == nil {
			t.Fatal("unverified staging with a managed copy still present must block")
		}
	})

	t.Run("failed unit without managed copy passes", func(t *testing.T) {
		runner := &gateStubRunner{result: wslsupply.RunResult{Results: []wslsupply.UnitResult{{
			Unit: wslsupply.UnitSharedVisionExport, Outcome: wslsupply.UnitFailed,
			Detail: "write-failed",
		}}}}
		if err := runWSLSharedFilesAtLaunch(context.Background(), runner, "Ubuntu"); err != nil {
			t.Fatalf("failure with proven absence of a managed copy must not block: %v", err)
		}
	})

	t.Run("canceled context propagates", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := runWSLSharedFilesAtLaunch(ctx, &gateStubRunner{}, "Ubuntu"); err == nil {
			t.Fatal("canceled ctx must propagate to the launch chain")
		}
	})
}

// ---------------------------------------------------------------------------
// steer#3：必需模型供给（managed-models）未落地不得按可选配置放行。
// ---------------------------------------------------------------------------

func TestVerdictForWSLPiConfigSupplyRequiredUnits(t *testing.T) {
	t.Run("managed-models write failure blocks", func(t *testing.T) {
		launchErr, _, _ := verdictForWSLPiConfigSupply(wslsupply.RunResult{Results: []wslsupply.UnitResult{{
			Unit: wslsupply.UnitPiManagedModels, Outcome: wslsupply.UnitFailed, Detail: "write-failed",
		}}}, nil)
		if launchErr == nil || !strings.Contains(launchErr.Error(), "未落地") {
			t.Fatalf("required model supply failure must block, got %v", launchErr)
		}
	})
	t.Run("managed-models fresh passes", func(t *testing.T) {
		launchErr, warn, _ := verdictForWSLPiConfigSupply(wslsupply.RunResult{Results: []wslsupply.UnitResult{{
			Unit: wslsupply.UnitPiManagedModels, Outcome: wslsupply.UnitFresh, Detail: "no-change",
		}}}, nil)
		if launchErr != nil || warn != "" {
			t.Fatalf("fresh managed-models must pass silently, got err=%v warn=%q", launchErr, warn)
		}
	})
	t.Run("optional keyed units still warn only", func(t *testing.T) {
		launchErr, warn, _ := verdictForWSLPiConfigSupply(wslsupply.RunResult{Results: []wslsupply.UnitResult{
			{Unit: wslsupply.UnitPiManagedModels, Outcome: wslsupply.UnitApplied, Detail: "written"},
			{Unit: wslsupply.UnitPiMCP, Outcome: wslsupply.UnitFailed, Detail: "write-failed"},
		}}, nil)
		if launchErr != nil {
			t.Fatalf("optional unit failure must not block, got %v", launchErr)
		}
		if warn == "" {
			t.Fatal("optional unit failure must still warn")
		}
	})
	t.Run("expected states stay silent", func(t *testing.T) {
		launchErr, warn, _ := verdictForWSLPiConfigSupply(wslsupply.RunResult{Results: []wslsupply.UnitResult{
			{Unit: wslsupply.UnitPiManagedModels, Outcome: wslsupply.UnitApplied, Detail: "written"},
			{Unit: wslsupply.UnitPiAmagi, Outcome: wslsupply.UnitBlocked, Detail: "awaiting-consent"},
			{Unit: wslsupply.UnitPiSettings, Outcome: wslsupply.UnitBlocked, Detail: "local-modification-frozen"},
		}}, nil)
		if launchErr != nil || warn != "" {
			t.Fatalf("expected states must pass silently, got err=%v warn=%q", launchErr, warn)
		}
	})
}
