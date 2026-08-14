package remote

// r4_002_headroom_uninstall_drain_test.go — R4-002: Headroom uninstall must
// hold a single install-drain critical section across BOTH headroom kinds that
// (a) blocks new AcquireForRun for the headroom kinds, (b) confirms both are
// lease-free, and (c) stays held until the venv deletion completes — closing the
// TOCTOU window between the empty check and RemoveAll (a launch that sneaks in
// after the check would otherwise have its venv dependency deleted).

import (
	"context"
	"errors"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// newRunPermitForDrainTest builds a real RunPermit via the gate (so AcquireForRun
// accepts it). Each call yields a distinct session/run.
func newRunPermitForDrainTest(t *testing.T, gate ControlGate, sid contract.SessionID) *RunPermit {
	t.Helper()
	permit, _ := gate.BeginDesktopLaunch(context.Background(), MintDesktopAuthority())
	rp, _, _ := gate.RegisterStartingSession(context.Background(), permit, sid)
	if err := gate.ActivateRun(context.Background(), rp); err != nil {
		t.Fatalf("ActivateRun(%s): %v", sid, err)
	}
	return rp
}

// TestR4_002_Drain_BlocksNewHeadroomAcquireWhileHeld proves that while the
// uninstall drain is held, AcquireForRun for BOTH headroom kinds is rejected
// (ErrSharedServiceInUse).
func TestR4_002_Drain_BlocksNewHeadroomAcquireWhileHeld(t *testing.T) {
	c := newTestCoordinator()
	clock := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clock, hub, dir)
	arb.MarkReady()
	gate := NewControlGate(arb, hub, dir)

	empty := c.BeginHeadroomUninstallDrain()
	if !empty {
		t.Fatal("expected empty=true with no prior leases")
	}
	defer c.EndHeadroomUninstallDrain()

	rpH := newRunPermitForDrainTest(t, gate, "drain-headroom")
	fp := [32]byte{9}
	if _, err := c.AcquireForRun(context.Background(), rpH, SharedServiceClaudeHeadroom, fp); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("claude headroom acquire during drain: expected ErrSharedServiceInUse, got %v", err)
	}
	if _, err := c.AcquireForRun(context.Background(), rpH, SharedServiceCodexHeadroom, fp); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("codex headroom acquire during drain: expected ErrSharedServiceInUse, got %v", err)
	}
}

// TestR4_002_Drain_ReleasedRestoresAcquire proves that after EndHeadroomUninstallDrain,
// new headroom leases can be acquired again (the drain is not permanent).
func TestR4_002_Drain_ReleasedRestoresAcquire(t *testing.T) {
	c := newTestCoordinator()
	clock := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clock, hub, dir)
	arb.MarkReady()
	gate := NewControlGate(arb, hub, dir)

	c.BeginHeadroomUninstallDrain()
	c.EndHeadroomUninstallDrain()

	rp := newRunPermitForDrainTest(t, gate, "post-drain")
	if _, err := c.AcquireForRun(context.Background(), rp, SharedServiceClaudeHeadroom, [32]byte{1}); err != nil {
		t.Fatalf("acquire after drain release should succeed, got %v", err)
	}
	if c.LeaseCount(SharedServiceClaudeHeadroom) != 1 {
		t.Fatalf("expected 1 lease after release, got %d", c.LeaseCount(SharedServiceClaudeHeadroom))
	}
}

// TestR4_002_Drain_EmptyCheckReflectsExistingLeases proves BeginHeadroomUninstallDrain
// reports empty=false when an existing lease is held (and still sets the drain,
// so the caller can decide to abort + release).
func TestR4_002_Drain_EmptyCheckReflectsExistingLeases(t *testing.T) {
	c := newTestCoordinator()
	clock := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clock, hub, dir)
	arb.MarkReady()
	gate := NewControlGate(arb, hub, dir)

	// Pre-existing codex headroom lease.
	rp := newRunPermitForDrainTest(t, gate, "pre-existing")
	if _, err := c.AcquireForRun(context.Background(), rp, SharedServiceCodexHeadroom, [32]byte{2}); err != nil {
		t.Fatalf("pre-existing acquire: %v", err)
	}

	empty := c.BeginHeadroomUninstallDrain()
	if empty {
		t.Fatal("expected empty=false with a pre-existing lease")
	}
	// Drain is set even when not empty (caller releases it on abort).
	defer c.EndHeadroomUninstallDrain()
}

// TestR4_002_Drain_LaunchDuringUninstallRejected simulates the TOCTOU the report
// flagged: the old code did two instantaneous LeaseCount checks then released the
// lock before RemoveAll. A concurrent launch could acquire a lease in that window.
// With the drain held across the whole uninstall, the launch is rejected.
func TestR4_002_Drain_LaunchDuringUninstallRejected(t *testing.T) {
	c := newTestCoordinator()
	clock := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clock, hub, dir)
	arb.MarkReady()
	gate := NewControlGate(arb, hub, dir)

	// Simulate the uninstall critical section: drain held across stop + RemoveAll.
	empty := c.BeginHeadroomUninstallDrain()
	if !empty {
		t.Fatal("expected empty at drain start")
	}

	// A concurrent launch attempts to acquire a headroom lease mid-uninstall.
	rp := newRunPermitForDrainTest(t, gate, "concurrent-launch")
	_, err := c.AcquireForRun(context.Background(), rp, SharedServiceClaudeHeadroom, [32]byte{3})
	if !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("R4-002 TOCTOU regression: concurrent launch acquired a lease during uninstall (err=%v); must be rejected", err)
	}
	// And no lease was recorded.
	if c.LeaseCount(SharedServiceClaudeHeadroom) != 0 {
		t.Fatal("R4-002: a lease was recorded during the drain (leak)")
	}

	c.EndHeadroomUninstallDrain()
	// After release, the launch can proceed (recovery).
	rp2 := newRunPermitForDrainTest(t, gate, "recovery-launch")
	if _, err := c.AcquireForRun(context.Background(), rp2, SharedServiceClaudeHeadroom, [32]byte{4}); err != nil {
		t.Fatalf("acquire after drain release should succeed, got %v", err)
	}
}

// TestR4_002_Drain_IdempotentEndIsSafe proves EndHeadroomUninstallDrain is safe
// to call without an active drain (no panic, no negative state).
func TestR4_002_Drain_IdempotentEndIsSafe(t *testing.T) {
	c := newTestCoordinator()
	c.EndHeadroomUninstallDrain() // no active drain — must not panic
	c.EndHeadroomUninstallDrain()
}
