package remote

// control_runtime_test.go — M3-A2 targeted tests: RunEventProjector run-scoped
// filtering (M-01), SharedServiceCoordinator facade lease (N-01), and
// ControlRuntime desktop write gating.

import (
	"context"
	"errors"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote/contract"
)

// --- RunEventProjector filtering (design §8.6, T-29) ---

// TestRunEventProjector_StaleObservationNoOp verifies that an observation from
// an old run (pointer mismatch) is a silent no-op — no Wails event is emitted.
func TestRunEventProjector_StaleObservationNoOp(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clock, hub, dir)
	arb.MarkReady()
	hub.MarkReady()
	dir.MarkReady()
	projector := NewRunEventProjector(arb, nil)

	// Register a starting session + activate to mint a run identity.
	gate := NewControlGate(arb, hub, dir)
	permit, err := gate.BeginDesktopLaunch(context.Background(), MintDesktopAuthority())
	if err != nil {
		t.Fatalf("BeginDesktopLaunch: %v", err)
	}
	sid := contract.SessionID("proj-test-1")
	runPermit, obsPermit, err := gate.RegisterStartingSession(context.Background(), permit, sid)
	if err != nil {
		t.Fatalf("RegisterStartingSession: %v", err)
	}
	if err := gate.ActivateRun(context.Background(), runPermit); err != nil {
		t.Fatalf("ActivateRun: %v", err)
	}

	// Current-run output: projector should validate (no panic, no Wails ctx → drop).
	// Since ctx is nil, emit is a no-op, but OfferOutput must not panic.
	projector.OfferOutput(obsPermit, 1, []byte("hello"))
	projector.OfferExit(obsPermit, 0, false)

	// Stale handle: a different/nil permit must be a silent no-op.
	projector.OfferOutput(nil, 2, []byte("stale"))
	projector.OfferExit(nil, 1, true)

	// After remove, the run is terminal; observations must be no-op.
	_ = arb.removeSession(sid)
	projector.OfferOutput(obsPermit, 3, []byte("post-remove"))
}

// TestRunEventProjector_TypedHandleOnly verifies that an untyped handle (not a
// *RunObservationPermit) is rejected (fail-closed).
func TestRunEventProjector_TypedHandleOnly(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clock, hub, dir)
	arb.MarkReady()
	projector := NewRunEventProjector(arb, nil)

	// A raw string handle must be ignored.
	projector.OfferOutput("not-a-permit", 1, []byte("bad"))
	projector.OfferExit(42, 0, false)
}

// --- SharedServiceCoordinator facade lease (design §6.7, T-33) ---

func newTestCoordinator() *SharedServiceCoordinator {
	return NewSharedServiceCoordinator()
}

// TestSharedServiceCoordinator_NoLeaseAllowsMutation verifies that with no
// active leases, manual mutations are allowed.
func TestSharedServiceCoordinator_NoLeaseAllowsMutation(t *testing.T) {
	c := newTestCoordinator()
	if err := c.CheckMutation(SharedServiceClaudeHeadroom, MutationReconfigure, [32]byte{}); err != nil {
		t.Errorf("expected nil with no leases, got %v", err)
	}
}

// TestSharedServiceCoordinator_LeaseBlocksMutation verifies that with an active
// lease, manual mutations are stably rejected (raw call = 0).
func TestSharedServiceCoordinator_LeaseBlocksMutation(t *testing.T) {
	c := newTestCoordinator()
	clock := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clock, hub, dir)
	arb.MarkReady()
	gate := NewControlGate(arb, hub, dir)

	// Acquire a lease via a run.
	permit, _ := gate.BeginDesktopLaunch(context.Background(), MintDesktopAuthority())
	runPermit, _, _ := gate.RegisterStartingSession(context.Background(), permit, "lease-test")
	gate.ActivateRun(context.Background(), runPermit)

	fp := [32]byte{1, 2, 3}
	lease, err := c.AcquireForRun(context.Background(), runPermit, SharedServiceClaudeHeadroom, fp)
	if err != nil {
		t.Fatalf("AcquireForRun: %v", err)
	}
	if c.LeaseCount(SharedServiceClaudeHeadroom) != 1 {
		t.Errorf("expected 1 lease, got %d", c.LeaseCount(SharedServiceClaudeHeadroom))
	}

	// Mutations must be rejected while the lease exists.
	if err := c.CheckMutation(SharedServiceClaudeHeadroom, MutationStop, [32]byte{}); err != ErrSharedServiceInUse {
		t.Errorf("expected ErrSharedServiceInUse, got %v", err)
	}
	if err := c.CheckMutation(SharedServiceClaudeHeadroom, MutationReconfigure, [32]byte{}); err != ErrSharedServiceInUse {
		t.Errorf("expected ErrSharedServiceInUse, got %v", err)
	}

	// Exact no-op is always allowed.
	if err := c.CheckMutation(SharedServiceClaudeHeadroom, MutationExactNoOp, [32]byte{}); err != nil {
		t.Errorf("expected nil for exact no-op, got %v", err)
	}

	// After release, mutations are allowed again.
	c.ReleaseExact(context.Background(), lease)
	if c.LeaseCount(SharedServiceClaudeHeadroom) != 0 {
		t.Errorf("expected 0 leases after release, got %d", c.LeaseCount(SharedServiceClaudeHeadroom))
	}
	if err := c.CheckMutation(SharedServiceClaudeHeadroom, MutationStop, [32]byte{}); err != nil {
		t.Errorf("expected nil after release, got %v", err)
	}
}

func TestSharedLaunchAdmissionBindsExactConfigBeforeEffects(t *testing.T) {
	coordinator := NewSharedServiceCoordinator()
	firstFingerprint := [32]byte{1, 2, 3}
	secondFingerprint := [32]byte{1, 2, 4}
	first, err := coordinator.AcquireLaunchAdmissionForConfig(SharedServiceClaudeHeadroom, firstFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.ReleaseLaunchAdmission(first)
	compatible, err := coordinator.AcquireLaunchAdmissionForConfig(SharedServiceClaudeHeadroom, firstFingerprint)
	if err != nil {
		t.Fatalf("compatible pending config rejected: %v", err)
	}
	coordinator.ReleaseLaunchAdmission(compatible)
	if _, err := coordinator.AcquireLaunchAdmissionForConfig(SharedServiceClaudeHeadroom, secondFingerprint); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("incompatible pending config error = %v, want ErrSharedServiceInUse", err)
	}
	if _, err := coordinator.AcquireLaunchAdmission(SharedServiceClaudeHeadroom); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("unbound launch crossed exact pending config: %v", err)
	}
}

func TestSharedCompensatingStopRequiresExclusiveTransactionOwnership(t *testing.T) {
	coordinator := NewSharedServiceCoordinator()
	first, err := coordinator.AcquireLaunchAdmission(SharedServiceClaudeHeadroom)
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.AcquireLaunchAdmission(SharedServiceClaudeHeadroom)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.MarkLaunchTransactionStarted(first); err != nil {
		t.Fatal(err)
	}
	if coordinator.AuthorizeCompensatingStop(first) {
		t.Fatal("compensating Stop authorized while a competing launch transaction existed")
	}
	coordinator.ReleaseLaunchAdmission(second)
	if !coordinator.AuthorizeCompensatingStop(first) {
		t.Fatal("exclusive exact starter was not authorized to compensate")
	}
	if coordinator.AuthorizeCompensatingStop(first) {
		t.Fatal("one-shot compensating Stop authorization was reused")
	}
	coordinator.ReleaseLaunchAdmission(first)
}

// TestSharedServiceCoordinator_IncompatibleConfigRejected verifies that
// acquiring a lease with an incompatible config fingerprint while leases exist
// is rejected (design §6.7.1).
func TestSharedServiceCoordinator_IncompatibleConfigRejected(t *testing.T) {
	c := newTestCoordinator()
	clock := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clock, hub, dir)
	arb.MarkReady()
	gate := NewControlGate(arb, hub, dir)

	permit, _ := gate.BeginDesktopLaunch(context.Background(), MintDesktopAuthority())
	runPermit, _, _ := gate.RegisterStartingSession(context.Background(), permit, "incompat-test")
	gate.ActivateRun(context.Background(), runPermit)

	fp1 := [32]byte{1}
	fp2 := [32]byte{2}
	if _, err := c.AcquireForRun(context.Background(), runPermit, SharedServiceClaudeHeadroom, fp1); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := c.AcquireForRun(context.Background(), runPermit, SharedServiceClaudeHeadroom, fp2); err != ErrSharedServiceInUse {
		t.Errorf("expected ErrSharedServiceInUse for incompatible config, got %v", err)
	}
}

// --- ControlRuntime desktop write gating (design §6.3) ---

// TestControlRuntime_DesktopWriteRequiresEntry verifies that a desktop write to
// a session with no control entry is denied (the gate requires a registered,
// active run entry).
func TestControlRuntime_DesktopWriteRequiresEntry(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	log := (*logging.Service)(nil)
	rt := NewControlRuntime(clock, log)
	rt.SetPTYRawPort(nopPTYRaw{})
	rt.MarkReady()

	// No session registered → write must fail (session not found).
	err := rt.DesktopInput(context.Background(), "no-such-session", []byte("x"))
	if err == nil {
		t.Error("expected error for unregistered session, got nil")
	}
}

// TestControlRuntime_DesktopWriteGated verifies that after registering a
// desktop run, a write goes through the gate exactly once.
func TestControlRuntime_DesktopWriteGated(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clock, nil)
	raw := &countingPTYRaw{}
	rt.SetPTYRawPort(raw)
	rt.MarkReady()

	sid := contract.SessionID("gate-write-test")
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(sid, obsPermit)

	if err := rt.DesktopInput(context.Background(), sid, []byte("hello")); err != nil {
		t.Errorf("DesktopInput: %v", err)
	}
	if raw.writeCount != 1 {
		t.Errorf("expected 1 raw write, got %d", raw.writeCount)
	}

	// After removing the session, writes must be denied.
	rt.RemoveDesktopSession(context.Background(), sid)
	raw.writeCount = 0
	if err := rt.DesktopInput(context.Background(), sid, []byte("again")); err == nil {
		t.Error("expected error after session removal, got nil")
	}
	if raw.writeCount != 0 {
		t.Errorf("expected 0 raw writes after removal, got %d", raw.writeCount)
	}
}

// --- helpers ---

type nopPTYRaw struct{}

func (nopPTYRaw) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	return nil
}
func (nopPTYRaw) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	return nil
}
func (nopPTYRaw) DetachSession(sessionID string) (BackendDetachReceipt, error) {
	return confirmedTestDetachReceipt(), nil
}

type countingPTYRaw struct {
	writeCount int
}

func (c *countingPTYRaw) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	c.writeCount++
	return nil
}
func (c *countingPTYRaw) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	return nil
}
func (c *countingPTYRaw) DetachSession(sessionID string) (BackendDetachReceipt, error) {
	return confirmedTestDetachReceipt(), nil
}
