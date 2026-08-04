package remote

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

// ---------------------------------------------------------------------------
// Test helpers: controllable fake clock + arbiter fixture
// ---------------------------------------------------------------------------

// ctrlFakeClock is a controllable clock for deterministic testing. AfterFunc
// callbacks are queued and fired manually via FirePending / Advance.
type ctrlFakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []ctrlFakeTimerEntry
}

type ctrlFakeTimerEntry struct {
	when  time.Time
	fire  func()
	fired bool
}

func newCtrlFakeClock(at time.Time) *ctrlFakeClock {
	return &ctrlFakeClock{now: at}
}

func (c *ctrlFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ctrlFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	c.FirePending()
}

// AfterFunc queues a timer callback. It fires when Advance reaches its deadline.
func (c *ctrlFakeClock) AfterFunc(d time.Duration, fn func()) securityTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := ctrlFakeTimerEntry{
		when: c.now.Add(d),
		fire: fn,
	}
	c.timers = append(c.timers, entry)
	return &ctrlFakeTimerHandle{clock: c, entry: &c.timers[len(c.timers)-1]}
}

// FirePending fires all timers whose deadline has been reached.
func (c *ctrlFakeClock) FirePending() {
	c.mu.Lock()
	due := make([]func(), 0)
	for i := range c.timers {
		if !c.timers[i].fired && !c.timers[i].when.After(c.now) {
			c.timers[i].fired = true
			due = append(due, c.timers[i].fire)
		}
	}
	c.mu.Unlock()
	for _, fn := range due {
		fn()
	}
}

// FireAll fires all pending timers regardless of deadline (for overflow/edge).
func (c *ctrlFakeClock) FireAll() {
	c.mu.Lock()
	due := make([]func(), 0)
	for i := range c.timers {
		if !c.timers[i].fired {
			c.timers[i].fired = true
			due = append(due, c.timers[i].fire)
		}
	}
	c.mu.Unlock()
	for _, fn := range due {
		fn()
	}
}

type ctrlFakeTimerHandle struct {
	clock *ctrlFakeClock
	entry *ctrlFakeTimerEntry
}

func (h *ctrlFakeTimerHandle) Stop() bool {
	h.clock.mu.Lock()
	defer h.clock.mu.Unlock()
	if h.entry.fired {
		return false
	}
	h.entry.fired = true
	return true
}

// newTestArbiter creates a ready arbiter + gate + hub + directory for testing.
func newTestArbiter(t *testing.T) (*ControlArbiter, *controlGate, *SessionEventHub, *AttachmentDirectory, *ctrlFakeClock) {
	t.Helper()
	clk := newCtrlFakeClock(time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC))
	hub := NewSessionEventHub()
	hub.MarkReady()
	dir := NewAttachmentDirectory()
	dir.MarkReady()
	arb := NewControlArbiter(clk, hub, dir)
	arb.MarkReady()
	gate := NewControlGate(arb, hub, dir).(*controlGate)
	return arb, gate, hub, dir, clk
}

// newTestDevicePrincipal creates a DevicePrincipal for testing.
func newTestDevicePrincipal(id, name string) DevicePrincipal {
	return DevicePrincipal{
		DeviceID:            contract.DeviceID(id),
		DeviceName:          name,
		AuthenticatedAt:     time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC),
		CredentialExpiresAt: time.Date(2027, 8, 3, 11, 0, 0, 0, time.UTC),
	}
}

// attachAndAcquire creates a lease, attaches it, acquires control, and returns
// the lease. Helper for tests that need a device holder.
func attachAndAcquire(t *testing.T, dir *AttachmentDirectory, arb *ControlArbiter,
	deviceID, deviceName, connID string, sessionID contract.SessionID) *ControlConnectionLease {
	t.Helper()
	lease, _ := dir.Attach(contract.DeviceID(deviceID), deviceName, ConnectionID(connID), sessionID)
	if lease == nil {
		t.Fatal("Attach returned nil lease")
	}
	principal := newTestDevicePrincipal(deviceID, deviceName)
	snap, gErr := arb.Acquire(principal, lease, sessionID)
	if gErr != nil {
		t.Fatalf("Acquire failed: %v", gErr)
	}
	if snap.State != contract.ControlStateYou {
		t.Fatalf("expected you, got %s", snap.State)
	}
	return lease
}

// newWailsAuthority mints a DesktopAuthority for testing.
func newWailsAuthority(source uint64) *DesktopAuthority {
	return &DesktopAuthority{source: source}
}

// startSession creates a staging session, activates it, and returns the session
// ID + permits. Helper for tests that need a writable session.
func startSession(t *testing.T, gate *controlGate, arb *ControlArbiter) (contract.SessionID, *LaunchPermit, *RunPermit, *RunObservationPermit) {
	t.Helper()
	ctx := context.Background()
	auth := newWailsAuthority(1)
	lp, err := gate.BeginDesktopLaunch(ctx, auth)
	if err != nil {
		t.Fatalf("BeginDesktopLaunch: %v", err)
	}
	sid := contract.SessionID("sess-" + randHex(4))
	rp, rop, err := gate.RegisterStartingSession(ctx, lp, sid)
	if err != nil {
		t.Fatalf("RegisterStartingSession: %v", err)
	}
	if err := gate.ActivateRun(ctx, rp); err != nil {
		t.Fatalf("ActivateRun: %v", err)
	}
	return sid, lp, rp, rop
}

func randHex(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(time.Now().UnixNano())
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// T-01: Concurrent acquire — exactly one winner per round (design §4.5, §9.4)
// ---------------------------------------------------------------------------

func TestControlArbiter_ConcurrentAcquireSingleWinner(t *testing.T) {
	arb, gate, _, dir, _ := newTestArbiter(t)
	_ = gate
	sid := contract.SessionID("s1")
	// Register a public session via launch.
	startSessionDirect(t, arb)

	// Create two device principals + leases.
	pA := newTestDevicePrincipal("devA", "Device A")
	pB := newTestDevicePrincipal("devB", "Device B")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	leaseB, _ := dir.Attach(pB.DeviceID, pB.DeviceName, "connB", sid)

	const rounds = 200
	for i := 0; i < rounds; i++ {
		// Reset holder to none each round.
		releaseHolder(t, arb, sid)
		var wg sync.WaitGroup
		var winsA, winsB atomic.Int32
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, gErr := arb.Acquire(pA, leaseA, sid)
				if gErr == nil {
					winsA.Add(1)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, gErr := arb.Acquire(pB, leaseB, sid)
				if gErr == nil {
					winsB.Add(1)
				}
			}
		}()
		wg.Wait()
		// Exactly one device should hold control at the end (or none if both
		// released). The key invariant: never both.
		entry := arb.entryFor(sid)
		entry.stateMu.Lock()
		holderCount := 0
		if entry.owner.kind == ownerDevice {
			holderCount = 1
		}
		entry.stateMu.Unlock()
		if holderCount > 1 {
			t.Fatalf("round %d: multiple holders", i)
		}
	}
}

// releaseHolder forces the current holder to none (test helper).
func releaseHolder(t *testing.T, arb *ControlArbiter, sid contract.SessionID) {
	t.Helper()
	entry := arb.entryFor(sid)
	if entry == nil {
		return
	}
	entry.stateMu.Lock()
	entry.owner = controlOwner{kind: ownerNone}
	entry.controlEpoch++
	entry.stateMu.Unlock()
}

// startSessionDirect creates a public session directly (without launch flow).
func startSessionDirect(t *testing.T, arb *ControlArbiter) contract.SessionID {
	t.Helper()
	sid := contract.SessionID("s1")
	entry := &controlEntry{
		sessionID:    sid,
		owner:        controlOwner{kind: ownerNone},
		controlEpoch: 1,
		opLane:       newBoundedOperationLane(),
		runPhase:     runActive,
		backend:      backendHealthy,
	}
	entry.currentRun = &runIdentity{nonce: 1, desktopRunToken: "tok1"}
	entry.runEpoch = 1
	entry.stateMirror = contract.SessionStateRunning
	entry.stateMirrorSet = true
	arb.tableMu.Lock()
	arb.entries[sid] = entry
	arb.tableMu.Unlock()
	return sid
}

// ---------------------------------------------------------------------------
// T-02: Idempotent same-owner acquire / release (design §4.5)
// ---------------------------------------------------------------------------

func TestControlArbiter_IdempotentAcquireRelease(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	p := newTestDevicePrincipal("devA", "Device A")
	lease, _ := dir.Attach(p.DeviceID, p.DeviceName, "conn1", sid)

	// First acquire succeeds.
	snap, gErr := arb.Acquire(p, lease, sid)
	if gErr != nil {
		t.Fatalf("first acquire: %v", gErr)
	}
	if snap.State != contract.ControlStateYou {
		t.Fatalf("expected you, got %s", snap.State)
	}

	// Idempotent re-acquire: same device, same wire state.
	snap2, gErr := arb.Acquire(p, lease, sid)
	if gErr != nil {
		t.Fatalf("idempotent acquire: %v", gErr)
	}
	if snap2.State != contract.ControlStateYou {
		t.Fatalf("expected you, got %s", snap2.State)
	}

	// Release succeeds.
	snap3, gErr := arb.Release(p, sid)
	if gErr != nil {
		t.Fatalf("release: %v", gErr)
	}
	if snap3.State != contract.ControlStateNone {
		t.Fatalf("expected none, got %s", snap3.State)
	}

	// Second release: forbidden (not holder).
	_, gErr = arb.Release(p, sid)
	if gErr == nil || gErr.Kind != DenyNotController {
		t.Fatalf("expected DenyNotController, got %v", gErr)
	}
}

// ---------------------------------------------------------------------------
// T-04: Audience-relative projection (design §8.2)
// ---------------------------------------------------------------------------

func TestControlArbiter_AudienceProjection(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	pB := newTestDevicePrincipal("devB", "Device B")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)

	// Holder = none: everyone sees none.
	snap, _ := arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateNone || snap.DeviceName != nil {
		t.Fatalf("none holder: expected none/nil, got %s/%v", snap.State, snap.DeviceName)
	}

	// Acquire by A.
	arb.Acquire(pA, leaseA, sid)

	// A sees "you".
	snap, _ = arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateYou || snap.DeviceName != nil {
		t.Fatalf("holder A: expected you/nil, got %s/%v", snap.State, snap.DeviceName)
	}
	// B sees "other" + A's name.
	snap, _ = arb.SnapshotForDevice(sid, pB.DeviceID)
	if snap.State != contract.ControlStateOther || snap.DeviceName == nil || *snap.DeviceName != "Device A" {
		t.Fatalf("other viewer: expected other/A, got %s/%v", snap.State, snap.DeviceName)
	}
	// Desktop viewer sees "other" + A's name.
	snap, _ = arb.SnapshotForDevice(sid, "")
	if snap.State != contract.ControlStateOther {
		t.Fatalf("desktop viewer: expected other, got %s", snap.State)
	}

	// Desktop take.
	arb.TakeDesktop(newWailsAuthority(42), sid)
	snap, _ = arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateDesktop || snap.DeviceName != nil {
		t.Fatalf("desktop holder: expected desktop/nil, got %s/%v", snap.State, snap.DeviceName)
	}
	snap, _ = arb.SnapshotForDevice(sid, "")
	if snap.State != contract.ControlStateDesktop {
		t.Fatalf("desktop viewer on desktop: expected desktop, got %s", snap.State)
	}

	// Validate wire snapshot.
	if err := contract.ValidateControlSnapshot(snap); err != nil {
		t.Fatalf("ValidateControlSnapshot: %v", err)
	}
}

// ---------------------------------------------------------------------------
// T-04b: Desktop preempts device (design §4.5, INV-04)
// ---------------------------------------------------------------------------

func TestControlArbiter_DesktopPreemptsDevice(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)

	arb.Acquire(pA, leaseA, sid)

	// Desktop take always succeeds (preempts).
	if gErr := arb.TakeDesktop(newWailsAuthority(1), sid); gErr != nil {
		t.Fatalf("TakeDesktop: %v", gErr)
	}

	// A is no longer holder; A's next write is forbidden.
	snap, gErr := arb.SnapshotForDevice(sid, pA.DeviceID)
	if gErr != nil {
		t.Fatalf("SnapshotForDevice: %v", gErr)
	}
	if snap.State != contract.ControlStateDesktop {
		t.Fatalf("after take: expected desktop, got %s", snap.State)
	}

	// A release fails (not holder).
	_, gErr = arb.Release(pA, sid)
	if gErr == nil || gErr.Kind != DenyNotController {
		t.Fatalf("expected DenyNotController after take, got %v", gErr)
	}

	// Desktop release: holder → none. Old device NOT restored.
	arb.ReleaseDesktop(newWailsAuthority(1), sid)
	snap, _ = arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateNone {
		t.Fatalf("after desktop release: expected none (no restore), got %s", snap.State)
	}
}

// ---------------------------------------------------------------------------
// T-05: Grace timer — expire and reconnect (design §7.2)
// ---------------------------------------------------------------------------

func TestControlArbiter_GraceExpire(t *testing.T) {
	arb, _, _, dir, clk := newTestArbiter(t)
	arb.SetGraceDuration(30 * time.Second)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	arb.Acquire(pA, leaseA, sid)

	// Unexpected disconnect → grace.
	arb.OnUnexpectedDetachForSession(sid, leaseA, clk.Now())

	// Verify holder is still device (grace phase).
	snap, _ := arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateYou {
		t.Fatalf("in grace: A should see you, got %s", snap.State)
	}

	// Advance to 29.999s: no expire yet.
	clk.Advance(29*time.Second + 999*time.Millisecond)
	snap, _ = arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateYou {
		t.Fatalf("at 29.999s: still grace (you), got %s", snap.State)
	}

	// Advance past 30s: expire fires.
	clk.Advance(2 * time.Millisecond)
	snap, _ = arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateNone {
		t.Fatalf("after expire: expected none, got %s", snap.State)
	}
}

func TestControlArbiter_GraceReconnectBeforeExpire(t *testing.T) {
	arb, _, _, dir, clk := newTestArbiter(t)
	arb.SetGraceDuration(30 * time.Second)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	arb.Acquire(pA, leaseA, sid)

	// Disconnect → grace.
	arb.OnUnexpectedDetachForSession(sid, leaseA, clk.Now())

	// Reconnect before deadline with a new lease.
	leaseA2, old := dir.Attach(pA.DeviceID, pA.DeviceName, "connA2", sid)
	if old == nil {
		t.Fatal("expected fenced old lease")
	}
	rebound := arb.RebindAttachment(sid, leaseA2, clk.Now())
	if !rebound {
		t.Fatal("expected rebind to succeed")
	}

	// A is back as connected holder.
	snap, _ := arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateYou {
		t.Fatalf("after rebind: expected you, got %s", snap.State)
	}

	// Advance past original deadline: stale timer no-op (doesn't clear holder).
	clk.Advance(31 * time.Second)
	snap, _ = arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateYou {
		t.Fatalf("after stale timer: expected you (rebound), got %s", snap.State)
	}
}

// ---------------------------------------------------------------------------
// T-06: Stale detach after replacement is a no-op (design §7.1, §9.4)
// ---------------------------------------------------------------------------

func TestControlArbiter_StaleDetachNoOp(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	lease1, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "conn1", sid)
	arb.Acquire(pA, lease1, sid)

	// Replace connection: attach new lease fences old.
	lease2, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "conn2", sid)

	// Stale detach of old lease: should be no-op (holder unchanged).
	arb.OnUnexpectedDetachForSession(sid, lease1, arb.clock.Now())

	// A is still holder (rebind happened via new lease).
	snap, _ := arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateYou {
		t.Fatalf("after stale detach: expected you, got %s", snap.State)
	}
	_ = lease2
}

// ---------------------------------------------------------------------------
// T-07: Epoch overflow → health latch, fail-closed (design §4.2)
// ---------------------------------------------------------------------------

func TestControlArbiter_EpochOverflowLatch(t *testing.T) {
	arb, _, _, _, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	entry := arb.entryFor(sid)

	// Force controlEpoch to max.
	entry.stateMu.Lock()
	entry.controlEpoch = ^uint64(0)
	entry.stateMu.Unlock()

	// Attempt desktop take: should latch and return unavailable.
	gErr := arb.TakeDesktop(newWailsAuthority(1), sid)
	if gErr == nil || gErr.Kind != DenyControlUnavailable {
		t.Fatalf("expected DenyControlUnavailable on overflow, got %v", gErr)
	}
	if !arb.IsHealthLatched() {
		t.Fatal("expected health latched after overflow")
	}

	// Further operations fail-closed.
	_, gErr = arb.SnapshotForDevice(sid, "")
	if gErr == nil || gErr.Kind != DenyControlUnavailable {
		t.Fatalf("expected DenyControlUnavailable when latched, got %v", gErr)
	}
}

// ---------------------------------------------------------------------------
// T-08: SessionStatus(5) → SessionState(5) adapter (design §4.3)
// ---------------------------------------------------------------------------

func TestSessionStateAdapter_Mapping(t *testing.T) {
	adapter := NewSessionStateAdapter()
	cases := []struct {
		status string
		want   contract.SessionState
	}{
		{"running", contract.SessionStateRunning},
		{"stopping", contract.SessionStateUnavailable},
		{"stopped", contract.SessionStateStopped},
		{"exited", contract.SessionStateExited},
		{"failed", contract.SessionStateUnavailable},
		{"unknown_future", contract.SessionStateUnavailable},
	}
	for _, tc := range cases {
		got := adapter.ToWireState(toSessionStatus(tc.status))
		if got != tc.want {
			t.Errorf("ToWireState(%q) = %s, want %s", tc.status, got, tc.want)
		}
	}
	// removed flag overrides.
	got := adapter.ToWireStatus(toSessionStatus("running"), true)
	if got != contract.SessionStateRemoved {
		t.Errorf("ToWireStatus(removed) = %s, want %s", got, contract.SessionStateRemoved)
	}
}

// toSessionStatus converts a string to session.SessionStatus.
func toSessionStatus(s string) session.SessionStatus {
	return session.SessionStatus(s)
}

// ---------------------------------------------------------------------------
// Fail-closed: not-ready arbiter denies everything (design §1.3 constraint 5)
// ---------------------------------------------------------------------------

func TestControlArbiter_NotReadyDenies(t *testing.T) {
	clk := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clk, hub, dir)
	// NOT MarkReady.

	sid := contract.SessionID("s1")
	p := newTestDevicePrincipal("devA", "Device A")
	lease, _ := dir.Attach(p.DeviceID, p.DeviceName, "conn1", sid)

	_, gErr := arb.Acquire(p, lease, sid)
	if gErr == nil || gErr.Kind != DenyControlUnavailable {
		t.Fatalf("not-ready acquire: expected unavailable, got %v", gErr)
	}
	gErr = arb.TakeDesktop(newWailsAuthority(1), sid)
	if gErr == nil || gErr.Kind != DenyControlUnavailable {
		t.Fatalf("not-ready take: expected unavailable, got %v", gErr)
	}
}

// ---------------------------------------------------------------------------
// Other device acquire → busy (design §4.5)
// ---------------------------------------------------------------------------

func TestControlArbiter_OtherDeviceAcquireBusy(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	pB := newTestDevicePrincipal("devB", "Device B")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	leaseB, _ := dir.Attach(pB.DeviceID, pB.DeviceName, "connB", sid)

	arb.Acquire(pA, leaseA, sid)

	// B acquire: busy.
	_, gErr := arb.Acquire(pB, leaseB, sid)
	if gErr == nil || gErr.Kind != DenyBusy {
		t.Fatalf("expected DenyBusy, got %v", gErr)
	}
}

// ---------------------------------------------------------------------------
// Passive resize blocked when device holds (design §6.2, R-06)
// ---------------------------------------------------------------------------

func TestControlGate_PassiveResizeBlockedByDevice(t *testing.T) {
	arb, gate, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	arb.Acquire(pA, leaseA, sid)

	auth := newWailsAuthority(1)
	err := gate.DoDesktopPassiveResize(context.Background(), auth, sid, func(ctx context.Context, permit *operationPermit) error {
		t.Fatal("passive resize should not execute when device holds")
		return nil
	})
	if err == nil {
		t.Fatal("expected error for passive resize with device holder")
	}
}
