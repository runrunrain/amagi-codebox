package main

// Phase 1 第三轮：remote Prepare 与恢复 revision 的实际入口屏障。
import (
	"amagi-codebox/internal/claudeaccounts"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"context"
	"errors"
	"testing"
	"time"
)

type prepareBarrierExecutor struct {
	launchplan.Executor
	entered, resume chan struct{}
	calls, stopAt   int
}

func (e *prepareBarrierExecutor) Prepare(ctx context.Context, p *launchplan.Plan, b launchplan.ExecutionBinding) (launchplan.PreparedExecution, error) {
	e.calls++
	if e.calls == e.stopAt {
		close(e.entered)
		<-e.resume
		return nil, errors.New("fixture: Prepare stopped before process effects")
	}
	return e.Executor.Prepare(ctx, p, b)
}

// F01：真实 remote create/restart 在 absence check 后进入 Executor.Prepare，
// 此时尚无新进程，但生产 gate 必须拒绝首次 capture。Prepare 失败须释放。
func TestAbsentRemotePrepareReservationBlocksFirstCapture(t *testing.T) {
	for _, action := range []string{"create", "restart"} {
		t.Run(action, func(t *testing.T) {
			pty := newTransactionPTY(t)
			h := newTransactionHarness(t, pty)
			app, store, _, _ := newFacadeTestApp(t)
			app.sharedCoord = h.coord
			store.mu.Lock()
			store.pool = nil
			store.mu.Unlock()
			setClaudeAccountsCache(app, nil, nil, true)
			h.adapter.SetClaudeCredentialsAbsenceCheck(app.claudeCredentialsAbsenceCheck)
			stopAt := 1
			if action == "restart" {
				stopAt = 2
			}
			e := &prepareBarrierExecutor{Executor: newAppLaunchExecutor(launchExecutorDeps{pty: pty, sharedCoord: h.coord, debts: launchplan.NewCompensationDebtRegistry()}), entered: make(chan struct{}), resume: make(chan struct{}), stopAt: stopAt}
			h.adapter.SetLaunchExecutor(e, h.coord)
			principal := remote.DevicePrincipal{DeviceID: "fixture-device"}
			var sid contract.SessionID
			if action == "restart" {
				result, err := h.adapter.CreateSession(context.Background(), "first", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
				if err != nil {
					t.Fatal(err)
				}
				sid = result.Detail.ID
				acquireTransactionControl(t, h, principal, sid, "absence-restart-control")
			}
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				if action == "create" {
					_, _ = h.adapter.CreateSession(context.Background(), "second", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
				} else {
					_, _ = h.adapter.RestartSession(context.Background(), "second", principal, sid)
				}
			}()
			awaitBarrier(t, e.entered)
			opID, err := app.CaptureClaudeAccount("", "first", 0)
			if err != nil {
				t.Fatal(err)
			}
			if op := waitClaudeOpDone(t, app, opID, 5*time.Second); op.Code != string(claudeaccounts.ReasonSessionsActive) {
				t.Fatalf("remote Prepare 窗口未互斥: %+v", op)
			}
			close(e.resume)
			awaitBarrier(t, finished)
			// 仅检查 reservation 清理，fake 进程存活另由已有 quiescence 测试负责。
			release, err := (claudeCredentialsMutationGate{app: app}).Acquire(context.Background(), "after-prepare-failure")
			if err != nil {
				t.Fatalf("Prepare 失败后 reservation 泄漏: %v", err)
			}
			release()
		})
	}
}

// F06：真实恢复 operation 已接收 revision、restore-source 已读 journal，
// 随后 Rename 推进 revision（generation 不变），再执行领域恢复必须拒绝。
func TestRecoveryRevisionChangedAfterFacadeRead(t *testing.T) {
	for _, action := range []string{"complete-target", "restore-source"} {
		t.Run(action, func(t *testing.T) {
			app, store, _, _ := newFacadeTestApp(t)
			p, _ := store.Load(context.Background())
			p.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionSwap, Swap: &claudeaccounts.SwapJournal{TxnID: "selected-journal", SourceID: "acc-a", TargetID: "acc-b", FromGeneration: p.ActiveGeneration, Before: p.Account("acc-a").OwnedProjectionsOf(), After: p.Account("acc-b").OwnedProjectionsOf()}}
			if err := store.Save(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			entered, resume := make(chan struct{}), make(chan struct{})
			app.claudeAccountsRecoveryCheckpoint = func() { close(entered); <-resume }
			opID, err := app.ReconcileClaudeAccounts(action, p.Revision)
			if err != nil {
				t.Fatal(err)
			}
			awaitBarrier(t, entered)
			if err := app.RenameClaudeAccount("acc-b", "renamed", p.Revision); err != nil {
				t.Fatal(err)
			}
			close(resume)
			op := waitClaudeOpDone(t, app, opID.OperationID, 5*time.Second)
			if op.State != "error" || op.Code != string(claudeaccounts.ReasonRevisionConflict) {
				t.Fatalf("陈旧恢复仍执行: %+v", op)
			}
			after, _ := store.Load(context.Background())
			if after.Transaction == nil || after.Transaction.Swap.TxnID != "selected-journal" || after.ActiveGeneration != p.ActiveGeneration {
				t.Fatal("陈旧恢复修改了 journal/generation")
			}
		})
	}
}
