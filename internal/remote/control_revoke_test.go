package remote

// control_revoke_test.go — T-19, T-20: revoke ordering, duplicate revoke;
// control_observation_test.go — T-29: stale observation no-op (design §7.3, §8.6).

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// T-19: Revoke clears device holder across multiple sessions (design §7.3)
// ---------------------------------------------------------------------------

func TestRevoke_ClearsMultipleSessions(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	pA := newTestDevicePrincipal("devA", "Device A")
	pB := newTestDevicePrincipal("devB", "Device B")

	// Create two sessions, both held by devA.
	sid1 := startSessionDirect(t, arb) // uses "s1"
	sid2 := contract.SessionID("s2")
	entry2 := &controlEntry{
		sessionID:    sid2,
		owner:        controlOwner{kind: ownerNone},
		controlEpoch: 1,
		opLane:       newBoundedOperationLane(),
		runPhase:     runActive,
		backend:      backendHealthy,
	}
	entry2.currentRun = &runIdentity{nonce: 1, desktopRunToken: "tok2"}
	entry2.runEpoch = 1
	entry2.stateMirror = contract.SessionStateRunning
	entry2.stateMirrorSet = true
	arb.tableMu.Lock()
	arb.entries[sid2] = entry2
	arb.tableMu.Unlock()

	leaseA1, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA1", sid1)
	leaseA2, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA2", sid2)
	arb.Acquire(pA, leaseA1, sid1)
	arb.Acquire(pA, leaseA2, sid2)

	// devB also holds sid2? No — single holder. Let's verify devA holds both.
	snap1, _ := arb.SnapshotForDevice(sid1, pA.DeviceID)
	snap2, _ := arb.SnapshotForDevice(sid2, pA.DeviceID)
	if snap1.State != contract.ControlStateYou || snap2.State != contract.ControlStateYou {
		t.Fatalf("devA should hold both: %s, %s", snap1.State, snap2.State)
	}

	// MarkDeviceRevoked: global no-new-admission fence.
	arb.MarkDeviceRevoked(pA.DeviceID)

	// After MarkDeviceRevoked, devA's acquire on a new session is denied.
	leaseA3, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA3", sid1)
	_, gErr := arb.Acquire(pA, leaseA3, sid1)
	if gErr == nil || gErr.Kind != DenyDeviceRevoked {
		t.Fatalf("expected DenyDeviceRevoked after MarkDeviceRevoked, got %v", gErr)
	}

	// ReleaseRevokedDevice: clears devA holders from all sessions.
	notice := DeviceRevocationNotice{
		DeviceID:   pA.DeviceID,
		OccurredAt: time.Now(),
	}
	arb.ReleaseRevokedDevice(notice)

	// Both sessions should now be none.
	snap1, _ = arb.SnapshotForDevice(sid1, pA.DeviceID)
	snap2, _ = arb.SnapshotForDevice(sid2, pA.DeviceID)
	if snap1.State != contract.ControlStateNone {
		t.Fatalf("sid1 after revoke: expected none, got %s", snap1.State)
	}
	if snap2.State != contract.ControlStateNone {
		t.Fatalf("sid2 after revoke: expected none, got %s", snap2.State)
	}

	// devB (unaffected) can still acquire.
	leaseB1, _ := dir.Attach(pB.DeviceID, pB.DeviceName, "connB1", sid1)
	_, gErr = arb.Acquire(pB, leaseB1, sid1)
	if gErr != nil {
		t.Fatalf("devB acquire after revoke: %v", gErr)
	}
}

// ---------------------------------------------------------------------------
// T-20: Duplicate revoke is idempotent (design §7.3)
// ---------------------------------------------------------------------------

func TestRevoke_DuplicateIsIdempotent(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	arb.Acquire(pA, leaseA, sid)

	// First revoke.
	arb.MarkDeviceRevoked(pA.DeviceID)
	arb.ReleaseRevokedDevice(DeviceRevocationNotice{DeviceID: pA.DeviceID, OccurredAt: time.Now()})

	// Holder is cleared.
	snap, _ := arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateNone {
		t.Fatalf("after first revoke: expected none, got %s", snap.State)
	}

	// Duplicate revoke: idempotent, no panic, no state change.
	arb.MarkDeviceRevoked(pA.DeviceID)
	arb.ReleaseRevokedDevice(DeviceRevocationNotice{DeviceID: pA.DeviceID, OccurredAt: time.Now()})

	snap, _ = arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateNone {
		t.Fatalf("after duplicate revoke: expected none, got %s", snap.State)
	}
}

// ---------------------------------------------------------------------------
// T-19b: M1 revoke ordering — MarkDeviceRevoked fences before holder cleanup
// (design §7.3). The no-new-admission point (revoked set) must precede any
// per-session holder release.
// ---------------------------------------------------------------------------

func TestRevoke_MarkFencesBeforeRelease(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	arb.Acquire(pA, leaseA, sid)

	// MarkDeviceRevoked is the linearization point.
	arb.MarkDeviceRevoked(pA.DeviceID)

	// Between Mark and Release, devA cannot pass any new checkpoint/admission.
	// Verify the revoked set is checked synchronously.
	arb.permitMu.Lock()
	revoked := arb.revokedDevices[pA.DeviceID]
	arb.permitMu.Unlock()
	if !revoked {
		t.Fatal("revoked set should contain devA after MarkDeviceRevoked")
	}

	// devA is still holder (Release hasn't happened), but cannot re-acquire.
	leaseA2, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA2", sid)
	_, gErr := arb.Acquire(pA, leaseA2, sid)
	if gErr == nil || gErr.Kind != DenyDeviceRevoked {
		t.Fatalf("expected DenyDeviceRevoked between Mark and Release, got %v", gErr)
	}

	// Now release.
	arb.ReleaseRevokedDevice(DeviceRevocationNotice{DeviceID: pA.DeviceID, OccurredAt: time.Now()})
}

// ---------------------------------------------------------------------------
// T-19c: Server Stop → FenceAllRemote + ReleaseAllRemote (design §7.4)
// ---------------------------------------------------------------------------

func TestServerStop_FenceAndReleaseAllRemote(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	arb.Acquire(pA, leaseA, sid)

	// Phase 1: FenceAllRemote.
	arb.FenceAllRemote()

	// Accepting is false.
	if arb.IsAcceptingRemote() {
		t.Fatal("expected not accepting after FenceAllRemote")
	}
	// Acceptance generation incremented.
	oldGen := arb.AcceptanceGeneration()
	arb.RestartRemote()
	if arb.AcceptanceGeneration() <= oldGen {
		t.Fatal("expected acceptance generation to increment on RestartRemote")
	}

	// But we need to release before restart for correctness. Let's redo.
	arb.FenceAllRemote()
	arb.ReleaseAllRemote(reasonServiceStopped)

	// devA holder cleared.
	snap, _ := arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateNone {
		t.Fatalf("after Server Stop: expected none, got %s", snap.State)
	}
}

// ---------------------------------------------------------------------------
// T-29: Stale run observation is a silent no-op (design §5.2 INV-08, §8.6)
// ---------------------------------------------------------------------------

func TestObservation_StaleExitIsNoOp(t *testing.T) {
	arb, _, _, _, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	entry := arb.entryFor(sid)

	// Capture the current run observation permit.
	oldRun := entry.currentRun
	oldEpoch := entry.runEpoch
	rop := &RunObservationPermit{
		entry:        entry,
		run:          oldRun,
		runEpoch:     oldEpoch,
		backendEpoch: entry.backendEpoch,
	}

	// Mint a new run (restart).
	entry.stateMu.Lock()
	newEpoch, _ := nextEpoch(entry.runEpoch)
	entry.runEpoch = newEpoch
	entry.currentRun = &runIdentity{nonce: newEpoch, desktopRunToken: "tok2"}
	entry.stateMu.Unlock()

	// Old observation: exit should be no-op (returns false).
	applied := arb.ObserveExit(rop, ProcessExitObservation{ExitCode: 0, Failed: false})
	if applied {
		t.Fatal("stale exit observation should be no-op")
	}

	// State mirror should NOT be changed by the stale observation.
	entry.stateMu.Lock()
	mirror := entry.stateMirror
	entry.stateMu.Unlock()
	if mirror != contract.SessionStateRunning {
		t.Fatalf("stale exit should not change mirror, got %s", mirror)
	}
}

// T-29b: Current run exit IS applied.
func TestObservation_CurrentExitApplied(t *testing.T) {
	arb, _, _, _, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	entry := arb.entryFor(sid)

	rop := &RunObservationPermit{
		entry:        entry,
		run:          entry.currentRun,
		runEpoch:     entry.runEpoch,
		backendEpoch: entry.backendEpoch,
	}

	// Current run exit: should be applied.
	applied := arb.ObserveExit(rop, ProcessExitObservation{ExitCode: 0, Failed: false})
	if !applied {
		t.Fatal("current exit should be applied")
	}

	entry.stateMu.Lock()
	mirror := entry.stateMirror
	phase := entry.runPhase
	entry.stateMu.Unlock()
	if mirror != contract.SessionStateExited {
		t.Fatalf("expected exited mirror, got %s", mirror)
	}
	if phase != runTerminal {
		t.Fatalf("expected terminal runPhase, got %d", phase)
	}
}

// T-29c: Output observation validates current run.
func TestObservation_OutputValidation(t *testing.T) {
	arb, _, _, _, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	entry := arb.entryFor(sid)

	// Current run output: accepted.
	rop := &RunObservationPermit{
		entry:        entry,
		run:          entry.currentRun,
		runEpoch:     entry.runEpoch,
		backendEpoch: entry.backendEpoch,
	}
	if !arb.ObserveOutput(rop) {
		t.Fatal("current output should be accepted")
	}

	// Stale run output: no-op.
	staleRop := &RunObservationPermit{
		entry:        entry,
		run:          &runIdentity{nonce: 999}, // wrong pointer
		runEpoch:     entry.runEpoch,
		backendEpoch: entry.backendEpoch,
	}
	if arb.ObserveOutput(staleRop) {
		t.Fatal("stale output should be no-op")
	}
}

// ---------------------------------------------------------------------------
// Shutdown: CloseForShutdown cancels everything, returns one-shot permit
// (design §10.3)
// ---------------------------------------------------------------------------

func TestShutdown_CloseForShutdown(t *testing.T) {
	arb, gate, _, dir, _ := newTestArbiter(t)
	ctx := context.Background()
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	arb.Acquire(pA, leaseA, sid)

	// Pending device launch.
	pDev := newTestDevicePrincipal("devB", "Device B")
	lp, _ := gate.BeginDeviceLaunch(ctx, pDev)

	// CloseForShutdown.
	permit := gate.CloseForShutdown()

	if permit == nil {
		t.Fatal("expected non-nil shutdown permit")
	}
	if permit.used.Load() {
		t.Fatal("permit should not be used yet")
	}
	if arb.IsReady() {
		t.Fatal("arbiter should not be ready after shutdown")
	}
	if !lp.IsCanceled() {
		t.Fatal("pending launch permit should be canceled on shutdown")
	}

	// After shutdown, all operations fail-closed.
	_, err := gate.SnapshotForDevice(sid, "")
	if err == nil {
		t.Fatal("expected error after shutdown")
	}
}

// ---------------------------------------------------------------------------
// ControlStateEvent projection via hub (design §8.1–§8.3)
// ---------------------------------------------------------------------------

func TestControlProjector_TransitionEvents(t *testing.T) {
	arb, _, hub, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)

	// Subscribe as devA.
	subA := hub.Subscribe(sid, pA.DeviceID, leaseA, nil)

	// Acquire: should produce acquired event for devA.
	arb.Acquire(pA, leaseA, sid)
	events := subA.Drain()
	if len(events) != 1 {
		t.Fatalf("expected 1 event after acquire, got %d", len(events))
	}
	ctrlEv, ok := events[0].(contract.ControlStateEvent)
	if !ok {
		t.Fatalf("expected ControlStateEvent, got %T", events[0])
	}
	if ctrlEv.State != contract.ControlStateYou {
		t.Fatalf("expected you, got %s", ctrlEv.State)
	}
	if ctrlEv.Reason != "acquired" {
		t.Fatalf("expected reason acquired, got %s", ctrlEv.Reason)
	}

	// Desktop take: should produce takeover event.
	arb.TakeDesktop(newWailsAuthority(1), sid)
	events = subA.Drain()
	if len(events) != 1 {
		t.Fatalf("expected 1 event after take, got %d", len(events))
	}
	ctrlEv, _ = events[0].(contract.ControlStateEvent)
	if ctrlEv.State != contract.ControlStateDesktop {
		t.Fatalf("expected desktop, got %s", ctrlEv.State)
	}
	if ctrlEv.Reason != "takeover" {
		t.Fatalf("expected reason takeover, got %s", ctrlEv.Reason)
	}

	// Idempotent desktop take: no event (wire state unchanged).
	arb.TakeDesktop(newWailsAuthority(1), sid)
	events = subA.Drain()
	if len(events) != 0 {
		t.Fatalf("expected 0 events for idempotent take, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// Session remove: control.none before removed (design §4.3, §10.2)
// ---------------------------------------------------------------------------

func TestControlArbiter_RemoveClearsHolder(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	arb.Acquire(pA, leaseA, sid)

	// Remove: should clear holder + tombstone.
	if gErr := arb.removeSession(sid); gErr != nil {
		t.Fatalf("removeSession: %v", gErr)
	}

	// Entry is tombstoned: snapshot returns not_found.
	_, gErr := arb.SnapshotForDevice(sid, pA.DeviceID)
	if gErr == nil || gErr.Kind != DenySessionNotFound {
		t.Fatalf("after remove: expected not_found, got %v", gErr)
	}
}

// suppress unused import
var _ = atomic.Int32{}
