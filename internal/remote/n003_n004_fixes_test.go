package remote

// n003_n004_fixes_test.go — R2 findings N-003 (WS registry leak) and N-004
// (queue-full fencer synchronous Close inside the H3 ledger lock) regression
// tests. Whitebox (package remote) so the unexported registry/conn seams are
// reachable.

import (
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// N-003: /ws/v1 must retain the M1 registration handle and Unregister on
// normal disconnect/write fault. A stale unregister (after FenceDevice/Stop
// detached the entry, or superseded by a newer epoch) is an idempotent no-op.
// ---------------------------------------------------------------------------

// TestN003_UnregisterOnDisconnect_LiveCountReturnsToZero proves the retained
// handle path: after a normal disconnect the registry entry is removed and
// LiveCount returns to zero.
func TestN003_UnregisterOnDisconnect_LiveCountReturnsToZero(t *testing.T) {
	r := newConnectionRegistry(newSecFakeClock(time.Now()))
	r.Start()
	srv := &Server{registry: r}

	c := &fakeConn{}
	res, err := r.Register(principal("devA"), "conn1", c)
	if err != nil || res.Outcome != RegistrationAccepted {
		t.Fatalf("register: outcome=%v err=%v", res.Outcome, err)
	}
	if r.LiveCount() != 1 {
		t.Fatalf("LiveCount after register = %d, want 1", r.LiveCount())
	}

	// Simulate the retained handle on the actor, then a normal disconnect.
	conn := &wsV1Connection{registration: res.Registration}
	srv.unregisterV1Connection(conn.registration)

	if r.LiveCount() != 0 {
		t.Fatalf("LiveCount after disconnect unregister = %d, want 0 (N-003 leak)", r.LiveCount())
	}
}

// TestN003_StaleUnregisterDoesNotDeleteNewerEpoch proves the epoch guard: after
// FenceDevice detaches an entry and a newer-epoch entry is registered for the
// same ConnectionID, an Unregister with the OLD handle is a no-op (the newer
// entry survives).
func TestN003_StaleUnregisterDoesNotDeleteNewerEpoch(t *testing.T) {
	r := newConnectionRegistry(newSecFakeClock(time.Now()))
	r.Start()
	srv := &Server{registry: r}

	old := &fakeConn{}
	res, err := r.Register(principal("devA"), "shared", old)
	if err != nil {
		t.Fatalf("register old: %v", err)
	}
	oldHandle := res.Registration

	// Revoke detaches the old entry (server-side Terminate path). The actor's
	// eventual handleDisconnect would then call Unregister with the stale handle.
	_ = r.FenceDevice("devA", time.Now())
	old.Terminate(ConnectionTermination{Cause: TerminationDeviceRevoked, OccurredAt: time.Now()})

	// A fresh register with the same ConnectionID succeeds (entry was detached).
	fresh := &fakeConn{}
	res2, err := r.Register(principal("devB"), "shared", fresh)
	if err != nil || res2.Outcome != RegistrationAccepted {
		t.Fatalf("register fresh: outcome=%v err=%v", res2.Outcome, err)
	}
	if r.LiveCount() != 1 {
		t.Fatalf("LiveCount after fresh register = %d, want 1", r.LiveCount())
	}

	// Stale unregister with the OLD handle: epoch no longer matches → no-op.
	srv.unregisterV1Connection(oldHandle)
	if r.LiveCount() != 1 {
		t.Fatalf("stale unregister deleted the fresh entry: LiveCount=%d, want 1", r.LiveCount())
	}

	// The fresh entry's own handle still unregisters correctly.
	srv.unregisterV1Connection(res2.Registration)
	if r.LiveCount() != 0 {
		t.Fatalf("LiveCount after fresh unregister = %d, want 0", r.LiveCount())
	}
}

// TestN003_RepeatedConnectDropCycleNoGrowth proves the leak fix under a
// connect/drop loop: repeated register+unregister does not accumulate entries.
func TestN003_RepeatedConnectDropCycleNoGrowth(t *testing.T) {
	r := newConnectionRegistry(newSecFakeClock(time.Now()))
	r.Start()
	srv := &Server{registry: r}

	for i := 0; i < 50; i++ {
		c := &fakeConn{}
		res, err := r.Register(principal("devA"), ConnectionID(connIDSeq(i)), c)
		if err != nil || res.Outcome != RegistrationAccepted {
			t.Fatalf("iter %d register: outcome=%v err=%v", i, res.Outcome, err)
		}
		srv.unregisterV1Connection(res.Registration)
		if got := r.LiveCount(); got != 0 {
			t.Fatalf("iter %d: LiveCount=%d after disconnect, want 0 (registry leak)", i, got)
		}
	}
}

// connIDSeq renders a deterministic distinct ConnectionID per index.
func connIDSeq(i int) ConnectionID {
	return ConnectionID(string(rune('A'+(i%26))) + string(rune('a'+(i/26%26))) + "-conn")
}

// ---------------------------------------------------------------------------
// N-004: the queue-full fencer must NOT close the transport synchronously while
// holding the H3 causal-ledger lock. requestTeardown defers Close to a goroutine
// so PublishReserved (which holds the ledger lock) returns bounded even when the
// transport Close blocks.
// ---------------------------------------------------------------------------

// TestN004_RequestTeardownNonBlocking proves that requestTeardown returns
// promptly even when the transport close would block (via the closeFn seam).
func TestN004_RequestTeardownNonBlocking(t *testing.T) {
	block := make(chan struct{})
	conn := &wsV1Connection{
		closeFn: func() error { <-block; return nil }, // blocks until released
	}
	done := make(chan struct{})
	go func() {
		conn.requestTeardown()
		close(done)
	}()
	select {
	case <-done:
		// requestTeardown returned without waiting for the blocking close.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("requestTeardown blocked on a hung transport close (N-004 regression)")
	}
	close(block) // release the goroutine so it can exit.
}

// TestN004_FenceSubscriptionWritesNonBlocking proves the fencer path used by
// PublishReserved (inside the ledger lock) is non-blocking.
func TestN004_FenceSubscriptionWritesNonBlocking(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	conn := &wsV1Connection{
		closeFn: func() error { <-block; return nil },
	}
	fencer := wsQueueFullFencer{conn: conn}

	done := make(chan struct{})
	go func() {
		fencer.FenceSubscriptionWrites(SubscriptionFenceToken{}, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("FenceSubscriptionWrites blocked on a hung transport close (N-004)")
	}
}

// TestN004_PublishReservedBoundedOnQueueFullWithBlockingClose proves the
// end-to-end invariant: with a full subscription queue and a blocking transport
// close, PublishReserved (which holds the causal ledger lock and calls
// fenceAuthority → the fencer) returns within a bounded budget. Before N-004 the
// synchronous conn.Close() inside the ledger lock could block PublishReserved
// indefinitely.
func TestN004_PublishReservedBoundedOnQueueFullWithBlockingClose(t *testing.T) {
	hub := NewSessionEventHub()
	hub.MarkReady()
	sid := contract.SessionID("s1")

	// Fencer with a blocking close.
	block := make(chan struct{})
	defer close(block)
	wsConn := &wsV1Connection{closeFn: func() error { <-block; return nil }}
	fencer := wsQueueFullFencer{conn: wsConn}

	lease := &ControlConnectionLease{deviceID: "devA", connectionID: "c1", attachmentGeneration: 1}
	lease.live.Store(true)
	sub := hub.RegisterCausalSubscription(sid, 0, lease, fencer)

	// Fill the queue to capacity so the next publish overflows → fenceAuthority.
	fillEv := contract.ControlStateEvent{
		Type: contract.ServerEventTypeControlState, SessionID: sid,
		State: contract.ControlStateNone, Reason: "fill",
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	for i := 0; i < causalSubscriptionCapacity; i++ {
		if !sub.enqueue(fillEv, SessionEventOrdinal(i+1)) {
			t.Fatalf("queue should accept fill event %d", i)
		}
	}

	// Reserve + publish one more event (ordinal > watermark) → queue-full.
	ticket, err := hub.ReserveRunRecordUnderState(sid, RunCausalPosition{1, 1}, CausalReplay)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	overflowEv := contract.ControlStateEvent{
		Type: contract.ServerEventTypeControlState, SessionID: sid,
		State: contract.ControlStateNone, Reason: "overflow",
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}

	done := make(chan struct{})
	go func() {
		hub.PublishReserved(ticket, overflowEv)
		close(done)
	}()
	select {
	case <-done:
		// PublishReserved returned despite the blocking transport close.
	case <-time.After(time.Second):
		t.Fatal("PublishReserved blocked inside the causal-ledger lock on a hung transport close (N-004)")
	}

	// The synchronous authority fence still ran: the lease is dead and the
	// subscription is fenced.
	if lease.IsLive() {
		t.Fatal("lease should be fenced (synchronous authority fence) after queue-full")
	}
	if !sub.IsFenced() {
		t.Fatal("subscription should be fenced after queue-full")
	}
}
