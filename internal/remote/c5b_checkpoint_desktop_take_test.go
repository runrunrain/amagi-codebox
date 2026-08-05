package remote

// c5b_checkpoint_desktop_take_test.go — M3-007 C5b 冻结命题权威证据（TEST-ONLY）。
// ===========================================================================
//
// 权威依据：fuxi 20260804-m3-continuity-design/design.md §8 九格 C5b +
//   M3-A control-arbitration-design（fenceCurrentOp 单点线性化）。
//
// 冻结命题 C5b（谛听 R4 精确要求）：
//   「设备 A 已取得 permit、停在 checkpoint，desktop take 先提交后，
//    旧 permit 在 checkpoint 失败（raw effect 不得 commit）」。
//
// 为何是 Go 单测而非 E2E：
//   生产 resize 事务（ws_v1_session.go handleResize）的闭包结构是
//     DoDevicePTY → permit.Checkpoint(1) → raw.ResizeRaw
//   「permit 取得」与「Checkpoint(1)」之间无任何测试可控 seam——permit 由
//   createDevicePTYPermit 在 stateMu 下铸造后立即交给闭包，闭包第一行即
//   Checkpoint(1)。harness 唯一可注入的 fakeSessionRaw.ResizeRaw 在 Checkpoint(1)
//   **之后**（post-checkpoint），无法证明「在 checkpoint 失败」（R3 已否）。
//   要在真实装配下证明该 TOCTOU 窗口，需在生产 resize 闭包内插入 test-only
//   barrier（luban 边界）。Go 单测以受控闭包直接驱动真实 gate，在 permit
//   取得与 Checkpoint(1) 之间建立确定性屏障，是该命题唯一可证的层级（谛听
//   R4 亦承认「Go 单测覆盖」存在）。
//
// fence 机制（设计 §5.2 INV-05 / §9.1.3，control_arbiter.go fenceCurrentOpLocked）：
//   TakeDesktop 在 stateMu 下 commitTransition(reasonTakeover, controlEpoch++)，
//   随后 fenceCurrentOpLocked 置 entry.currentOp.fenced=true 并 cancel(其 ctx,
//   cause=errOperationFenced)。该机制对 TakeDesktop / MarkDeviceRevoked /
//   Release / ServerStop 完全相同（同一 fenceCurrentOpLocked）。Checkpoint 的
//   fast path 先读 p.fenced.Load()，若 true 直接返回 errOperationFenced；
//   再读 ctx.Done()；最后在 stateMu 下比对 controlEpoch/runEpoch/backendEpoch/
//   currentOp/acceptanceGeneration/revokedSet。本测试以 **TakeDesktop** 作为
//   fence 触发（C5b 的精确场景：device 被 desktop 抢占），其余断言锚定
//   errOperationFenced + raw 不 commit + controlEpoch 推进 + desktop 随后 commit。
//
// 覆盖（三风险，见报告风险→测试映射表 R1/R2/R3）：
//   · R1（冻结命题本体）：permit 取得、停在 Checkpoint(1) 前 → TakeDesktop fence
//     → Checkpoint(1) 返回 errOperationFenced → raw 不到达（dims=0）。
//   · R2（生产 resize 结构映射）：Checkpoint(1) 通过 → in-flight 屏障 → TakeDesktop
//     fence → 二次 Checkpoint(2) 返回 errOperationFenced → raw 不 commit。证明
//     permit 被 fence 失效（非仅 ctx 取消），任何后续 checkpoint 都会拒。
//   · R3（系统一致）：设备 op 被拒后，desktop 随后 resize 经 DoDesktopPTY commit
//     （desktop dims=1），controlEpoch 已由 take 推进。
//
// 边界如实声明：本证据证明 fence 机制使「已取得的 device permit 在 checkpoint
//   失败」（命题本体）。它不证明 E2E/真 transport 下该窗口的端到端时序——那需
//   生产 seam（luban 边界），见报告 §4 结构性论证。
// ===========================================================================

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// TestC5b_Frozen_DevicePermitStoppedAtCheckpoint_DesktopTakeFences_CheckpointRejects
// 是 C5b 冻结命题的权威证据（R1）。
//
// 时序（确定性，channel 同步）：
//  1. 设备 A acquire 控制（attachAndAcquire → holder=device A）。
//  2. goroutine 起 DoDevicePTY(PTYResize)：gate 铸 permit（createDevicePTYPermit
//     通过 owner-check，A 仍是 holder）→ 闭包运行 → 在 Checkpoint(1) **之前**
//     命中 preCheckpoint 屏障挂起（此时 permit 已取得、持有 lane，停在 checkpoint）。
//  3. 主 goroutine 收到 permitAcquired → gate.TakeDesktop（desktopAuth）→
//     commitTransition(reasonTakeover, controlEpoch++) + fenceCurrentOpLocked
//     （permit.fenced=true，ctx cancel(errOperationFenced)）。
//  4. 释放 preCheckpoint 屏障 → 闭包恢复 → Checkpoint(1) fast path 读
//     permit.fenced=true → 返回 errOperationFenced → raw effect 不到达。
//
// 断言：DoDevicePTY 返回 errOperationFenced；Checkpoint(1) 返回 errOperationFenced；
//
//	raw 未 commit（dims=0）；entry.owner=desktop、controlEpoch 已推进。
func TestC5b_Frozen_DevicePermitStoppedAtCheckpoint_DesktopTakeFences_CheckpointRejects(t *testing.T) {
	arb, gate, _, dir, _ := newTestArbiter(t)
	ctx := context.Background()
	sid := startSessionDirect(t, arb)

	// 设备 A 取得控制（holder = device A）。
	lease := attachAndAcquire(t, dir, arb, "devA", "Device A", "connA", sid)

	const (
		deviceCols = 90
		deviceRows = 30
	)
	// preCheckpoint 屏障：在 Checkpoint(1) **之前** 挂起（permit 已取得）。
	permitAcquired := make(chan struct{})
	releaseBarrier := make(chan struct{})

	var checkpointErr atomic.Value // error
	var rawCommitted atomic.Int32  // 0 = 未 commit；1 = 已 commit（不得为 1）
	var rawCols, rawRows atomic.Int32

	resultErr := make(chan error, 1)
	go func() {
		err := gate.DoDevicePTY(ctx, lease, sid, PTYResize, func(opCtx context.Context, permit *operationPermit) error {
			// permit 已取得、持有 lane；停在 Checkpoint(1) 前。
			close(permitAcquired)
			<-releaseBarrier
			// Checkpoint(1)：fence 已发生（permit.fenced=true）→ errOperationFenced。
			ckErr := permit.Checkpoint(opCtx, 1)
			checkpointErr.Store(ckErr)
			if ckErr != nil {
				return ckErr
			}
			// 不应到达：raw effect commit。
			rawCols.Store(deviceCols)
			rawRows.Store(deviceRows)
			rawCommitted.Store(1)
			return nil
		})
		resultErr <- err
	}()

	<-permitAcquired // 确认 device A 的 permit 已取得、停在 checkpoint。

	// desktop take 先提交（fence）：controlEpoch++ + fenceCurrentOpLocked。
	desktopAuth := newWailsAuthority(7)
	if err := gate.TakeDesktop(ctx, desktopAuth, sid); err != nil {
		t.Fatalf("TakeDesktop: %v", err)
	}

	close(releaseBarrier) // 释放屏障：device op 恢复，Checkpoint(1) 见 fence。

	err := <-resultErr

	// —— 断言 R1：冻结命题本体 ——
	if err == nil {
		t.Fatal("expected DoDevicePTY to fail with errOperationFenced after desktop take fence, got nil")
	}
	if !errors.Is(err, errOperationFenced) {
		t.Fatalf("expected errOperationFenced, got %v", err)
	}
	ckErr, _ := checkpointErr.Load().(error)
	if !errors.Is(ckErr, errOperationFenced) {
		t.Fatalf("expected Checkpoint(1) to return errOperationFenced, got %v", ckErr)
	}
	if rawCommitted.Load() != 0 {
		t.Fatalf("device raw effect must NOT commit after checkpoint reject: rawCommitted=%d (cols=%d rows=%d)",
			rawCommitted.Load(), rawCols.Load(), rawRows.Load())
	}

	// —— 断言 R3：系统一致——desktop 已接管、controlEpoch 已推进 ——
	entry := arb.entryFor(sid)
	entry.stateMu.Lock()
	ownerKind := entry.owner.kind
	epochAfter := entry.controlEpoch
	entry.stateMu.Unlock()
	if ownerKind != ownerDesktop {
		t.Fatalf("expected owner=desktop after takeover, got %v", ownerKind)
	}
	if epochAfter <= 1 {
		t.Fatalf("expected controlEpoch advanced by takeover (>1), got %d", epochAfter)
	}

	// desktop 随后 resize commit（系统一致：device 被拒不阻塞 desktop）。
	const (
		desktopCols = 100
		desktopRows = 40
	)
	var desktopCommitted atomic.Int32
	dErr := gate.DoDesktopPTY(ctx, desktopAuth, sid, PTYResize, func(opCtx context.Context, permit *operationPermit) error {
		if err := permit.Checkpoint(opCtx, 1); err != nil {
			return err
		}
		desktopCommitted.Store(1) // 模拟 raw.ResizeRaw commit
		return nil
	})
	if dErr != nil {
		t.Fatalf("desktop resize after takeover must succeed, got %v", dErr)
	}
	if desktopCommitted.Load() != 1 {
		t.Fatal("desktop resize must commit after device fence failure")
	}
}

// TestC5b_ProductionResizeMap_Checkpoint1PassedThenFenced_ReCheckpointRejects_RawNotCommit
// 映射生产 resize 闭包结构（R2）：Checkpoint(1) 通过 → in-flight 屏障（模拟
// raw.ResizeRaw 在途）→ TakeDesktop fence → 二次 Checkpoint(2) 返回
// errOperationFenced → raw 不 commit。
//
// 证明：即便 Checkpoint(1) 已通过（permit 已 admit），fence 仍把 permit 置为
//
//	fenced，任何后续 checkpoint 都拒绝。这是保护 raw effect 不在 fence 后 commit
//	的机制（checkpoint 是「下一步前必须再校验」的能力点）。与 R1 互补：R1 证明
//	「停在 checkpoint 时 checkpoint 拒」；R2 证明「checkpoint 通过后 fence 仍使
//	permit 失效、再 checkpoint 拒」。
func TestC5b_ProductionResizeMap_Checkpoint1PassedThenFenced_ReCheckpointRejects_RawNotCommit(t *testing.T) {
	arb, gate, _, dir, _ := newTestArbiter(t)
	ctx := context.Background()
	sid := startSessionDirect(t, arb)
	lease := attachAndAcquire(t, dir, arb, "devA", "Device A·R2", "connA2", sid)

	checkpoint1Passed := make(chan struct{})
	releaseBarrier := make(chan struct{})

	var reCheckpointErr atomic.Value // error
	var rawCommitted atomic.Int32
	resultErr := make(chan error, 1)

	go func() {
		err := gate.DoDevicePTY(ctx, lease, sid, PTYResize, func(opCtx context.Context, permit *operationPermit) error {
			// 生产 resize 闭包第一道：Checkpoint(1)。A 仍是 holder → 通过。
			if err := permit.Checkpoint(opCtx, 1); err != nil {
				resultErr <- err
				return err
			}
			close(checkpoint1Passed) // Checkpoint(1) 已 admit（permit 已生效）
			<-releaseBarrier         // in-flight：模拟 raw.ResizeRaw 在途（post-checkpoint）
			// fence 已发生；二次 Checkpoint（生产单 checkpoint，此处显式证明 permit
			// 被失效）必须拒绝。
			ck2 := permit.Checkpoint(opCtx, 2)
			reCheckpointErr.Store(ck2)
			if ck2 != nil {
				return ck2
			}
			rawCommitted.Store(1) // 不应到达
			return nil
		})
		resultErr <- err
	}()

	<-checkpoint1Passed // Checkpoint(1) 已通过：permit 已 admit、停在 in-flight。

	desktopAuth := newWailsAuthority(9)
	if err := gate.TakeDesktop(ctx, desktopAuth, sid); err != nil {
		t.Fatalf("TakeDesktop: %v", err)
	}
	close(releaseBarrier)

	err := <-resultErr
	if err == nil {
		t.Fatal("expected DoDevicePTY to fail after fence, got nil")
	}
	if !errors.Is(err, errOperationFenced) {
		t.Fatalf("expected errOperationFenced after fence, got %v", err)
	}
	ck2, _ := reCheckpointErr.Load().(error)
	if !errors.Is(ck2, errOperationFenced) {
		t.Fatalf("expected re-Checkpoint(2) to return errOperationFenced after fence, got %v", ck2)
	}
	if rawCommitted.Load() != 0 {
		t.Fatalf("device raw effect must NOT commit after fence: rawCommitted=%d", rawCommitted.Load())
	}

	// desktop 接管已生效。
	entry := arb.entryFor(sid)
	entry.stateMu.Lock()
	ownerKind := entry.owner.kind
	entry.stateMu.Unlock()
	if ownerKind != ownerDesktop {
		t.Fatalf("expected owner=desktop after takeover, got %v", ownerKind)
	}
}

// 保证 contract 包被引用（与 control_lane_test.go 同款 suppress 模式）。
var _ = contract.SessionID("unused")
