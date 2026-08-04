package remote

// control_launch_fence_test.go — T-09, T-30: launch fencing, permit revalidation,
// revoke/Stop canceling in-flight launches (design §6.4).

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// T-09: Delayed bootstrap write after stop/restart is rejected (design §6.4).
// An old RunPermit's checkpoint must fail after the run is fenced.
func TestLaunchFence_OldBootstrapRejectedAfterStop(t *testing.T) {
	arb, gate, _, _, _ := newTestArbiter(t)
	ctx := context.Background()
	sid, _, rp, _ := startSession(t, gate, arb)

	// First bootstrap write succeeds (checkpoint passes).
	var spy1 atomic.Int32
	err := gate.DoBootstrapPTY(ctx, rp, func(ctx context.Context, permit *operationPermit) error {
		if err := permit.Checkpoint(ctx, 1); err != nil {
			return err
		}
		spy1.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("bootstrap write: %v", err)
	}
	if spy1.Load() != 1 {
		t.Fatalf("expected spy=1, got %d", spy1.Load())
	}

	// Stop/restart: fence current run, mint new run.
	entry := arb.entryFor(sid)
	entry.stateMu.Lock()
	entry.runPhase = runTerminal
	newEpoch, ok := nextEpoch(entry.runEpoch)
	if !ok {
		t.Fatal("epoch overflow")
	}
	entry.runEpoch = newEpoch
	entry.currentRun = &runIdentity{nonce: newEpoch, desktopRunToken: "tok2"}
	entry.runPhase = runActive
	entry.stateMu.Unlock()

	// Old RunPermit's bootstrap write: checkpoint should reject (stale run).
	var spy2 atomic.Int32
	err = gate.DoBootstrapPTY(ctx, rp, func(ctx context.Context, permit *operationPermit) error {
		if err := permit.Checkpoint(ctx, 1); err != nil {
			return err
		}
		spy2.Add(1)
		return nil
	})
	if err == nil {
		t.Fatal("expected stale permit error")
	}
	if spy2.Load() != 0 {
		t.Fatalf("expected spy=0 for stale bootstrap, got %d", spy2.Load())
	}
}

// T-09b: Launch effect with compensation on abort (design §6.4.3).
func TestLaunchFence_AbortCompensatesReverseOrder(t *testing.T) {
	arb, gate, _, _, _ := newTestArbiter(t)
	ctx := context.Background()
	auth := newWailsAuthority(1)
	lp, err := gate.BeginDesktopLaunch(ctx, auth)
	if err != nil {
		t.Fatalf("BeginDesktopLaunch: %v", err)
	}
	sid := contract.SessionID("sess-abort")
	rp, _, err := gate.RegisterStartingSession(ctx, lp, sid)
	if err != nil {
		t.Fatalf("RegisterStartingSession: %v", err)
	}

	// Apply two effects with compensation tracking.
	var compOrder []LaunchEffectKind
	// Effect 1.
	err = gate.DoLaunchEffect(ctx, rp, LaunchProxyStart, func(ctx context.Context, permit *operationPermit, receipt *EffectReceipt) error {
		receipt.MarkApplied(func(ctx context.Context) error {
			compOrder = append(compOrder, LaunchProxyStart)
			return nil
		})
		return permit.Checkpoint(ctx, 1)
	})
	if err != nil {
		t.Fatalf("effect 1: %v", err)
	}
	// Effect 2.
	err = gate.DoLaunchEffect(ctx, rp, LaunchHeadroomStart, func(ctx context.Context, permit *operationPermit, receipt *EffectReceipt) error {
		receipt.MarkApplied(func(ctx context.Context) error {
			compOrder = append(compOrder, LaunchHeadroomStart)
			return nil
		})
		return permit.Checkpoint(ctx, 1)
	})
	if err != nil {
		t.Fatalf("effect 2: %v", err)
	}

	// Abort: compensation should run in REVERSE order (HeadroomStart then ProxyStart).
	gate.AbortLaunch(ctx, lp, errors.New("test abort"))
	if len(compOrder) != 2 {
		t.Fatalf("expected 2 compensations, got %d", len(compOrder))
	}
	if compOrder[0] != LaunchHeadroomStart || compOrder[1] != LaunchProxyStart {
		t.Fatalf("expected reverse order [HeadroomStart, ProxyStart], got %v", compOrder)
	}

	// Staging entry should be deletable (not public).
	if isEntryPublic(arb.entryFor(sid)) {
		t.Fatal("staging entry should not be public after abort")
	}
}

// T-30: Remote device launch canceled by revoke (design §6.4.2, §7.3).
func TestLaunchFence_DeviceLaunchCanceledByRevoke(t *testing.T) {
	arb, gate, _, _, _ := newTestArbiter(t)
	ctx := context.Background()
	pDev := newTestDevicePrincipal("devX", "Device X")
	lp, err := gate.BeginDeviceLaunch(ctx, pDev)
	if err != nil {
		t.Fatalf("BeginDeviceLaunch: %v", err)
	}

	// Revoke the device mid-launch.
	gate.MarkDeviceRevoked(pDev.DeviceID)

	// The launch permit should be canceled.
	if !lp.IsCanceled() {
		t.Fatal("expected device launch permit canceled after revoke")
	}

	// Attempt to register starting session: should fail (stale/revoked).
	sid := contract.SessionID("sess-revoked")
	_, _, err = gate.RegisterStartingSession(ctx, lp, sid)
	if err == nil {
		t.Fatal("expected error registering session for revoked device")
	}

	// Attempt to do a launch effect: should fail.
	_, rop, _ := arb.registerStartingSession(sid, lp)
	_ = rop
	rp := &RunPermit{launch: lp, entry: arb.entryFor(sid), run: &runIdentity{nonce: 1}, runEpoch: 1}
	if rp.entry == nil {
		// registerStartingSession failed (expected); create entry manually for test.
		rp.entry = &controlEntry{sessionID: sid, opLane: newBoundedOperationLane(), controlEpoch: 1, runPhase: runStarting}
	}
	err = gate.DoLaunchEffect(ctx, rp, LaunchProxyStart, func(ctx context.Context, permit *operationPermit, receipt *EffectReceipt) error {
		t.Fatal("effect should not execute for revoked device")
		return nil
	})
	if err == nil {
		t.Fatal("expected error for launch effect after revoke")
	}
}

// T-30b: Server Stop cancels device launch permits (design §7.4).
func TestLaunchFence_ServerStopCancelsDeviceLaunch(t *testing.T) {
	arb, gate, _, _, _ := newTestArbiter(t)
	ctx := context.Background()
	pDev := newTestDevicePrincipal("devY", "Device Y")
	lp, err := gate.BeginDeviceLaunch(ctx, pDev)
	if err != nil {
		t.Fatalf("BeginDeviceLaunch: %v", err)
	}

	// FenceAllRemote (Server Stop phase 1).
	gate.FenceAllRemote()

	// Device launch permit should be canceled.
	if !lp.IsCanceled() {
		t.Fatal("expected device launch permit canceled after FenceAllRemote")
	}

	// Acceptance generation changed; old permit revalidation fails.
	if gErr := lp.revalidate(arb); gErr == nil {
		t.Fatal("expected stale permit after acceptance generation change")
	}
}

// T-30c: Desktop launch survives remote stop (desktop is root authority).
func TestLaunchFence_DesktopLaunchSurvivesRemoteStop(t *testing.T) {
	arb, gate, _, _, _ := newTestArbiter(t)
	ctx := context.Background()
	auth := newWailsAuthority(1)
	lp, err := gate.BeginDesktopLaunch(ctx, auth)
	if err != nil {
		t.Fatalf("BeginDesktopLaunch: %v", err)
	}

	gate.FenceAllRemote()

	// Desktop permit should NOT be canceled (only device permits are).
	if lp.IsCanceled() {
		t.Fatal("desktop launch permit should not be canceled by FenceAllRemote")
	}
	// But it IS affected by runtime generation (shutdown), not acceptance.
	// Revalidation should succeed (runtime generation unchanged).
	if gErr := lp.revalidate(arb); gErr != nil {
		t.Fatalf("desktop permit revalidate after FenceAllRemote: %v", gErr)
	}
}
