package remote

// pairing_lifecycle_test.go — Major-03: the pairing service is suspended on
// construction / Stop / listen-fail / serve-fail and accepts ONLY after a
// successful listener publish (Server.Start → Resume). CreateWindow on a
// suspended service returns a closed error instead of publishing a window that
// no listener guards. These tests use a REAL Server.Start on 127.0.0.1:0 so the
// publish/Resume and Stop/Suspend transitions are exercised end-to-end.

import (
	"context"
	"embed"
	"net"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
)

// newSuspendedServer builds a security-enabled server whose pairing service is
// in the constructed-suspended state (LoadSecurityState done, NO Resume). It
// does NOT use newSecServer (which Resumes) so tests can drive the lifecycle.
func newSuspendedServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	opts := NewProductionSecurityOptions(dir, validHostSummary)
	srv := NewServerWithSecurity(0, nil, logging.NewService(t.TempDir()), embed.FS{}, opts)
	t.Cleanup(srv.log.Close)
	if err := srv.LoadSecurityState(); err != nil {
		t.Fatalf("LoadSecurityState: %v", err)
	}
	srv.SetHost("127.0.0.1")
	return srv
}

// startOnEphemeral starts srv on 127.0.0.1:0 and registers Stop for cleanup.
func startOnEphemeral(t *testing.T, srv *Server) {
	t.Helper()
	srv.SetPort(0)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(srv.Stop)
}

// waitForRunning polls IsRunning up to 1s.
func waitForRunning(t *testing.T, srv *Server, want bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if srv.IsRunning() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("IsRunning=%v want %v after 1s", srv.IsRunning(), want)
}

// TestPairing_ConstructedSuspended_CreateWindowRejected: a freshly constructed
// service (no Start) MUST reject CreateWindow with a closed error.
func TestPairing_ConstructedSuspended_CreateWindowRejected(t *testing.T) {
	srv := newSuspendedServer(t)
	if _, err := srv.CreatePairingWindow(true); err == nil {
		t.Fatal("CreatePairingWindow must be rejected when the service is constructed-suspended (not listening)")
	}
	if srv.pairing.WindowActive() {
		t.Fatal("no active window may exist on a suspended service")
	}
}

// TestPairing_ResumeThenSuspend: Resume enables CreateWindow; Suspend disables
// it and drops any active window (no listener publication races).
func TestPairing_ResumeThenSuspend(t *testing.T) {
	srv := newSuspendedServer(t)
	srv.pairing.Resume()
	if _, err := srv.CreatePairingWindow(true); err != nil {
		t.Fatalf("CreatePairingWindow after Resume: %v", err)
	}
	if !srv.pairing.WindowActive() {
		t.Fatal("window must be active after Resume + CreateWindow")
	}
	srv.pairing.Suspend()
	if srv.pairing.WindowActive() {
		t.Fatal("Suspend must cancel the active window")
	}
	if _, err := srv.CreatePairingWindow(true); err == nil {
		t.Fatal("CreatePairingWindow must be rejected after Suspend")
	}
}

// TestPairing_StartResumes_StopSuspends: real Start publishes the listener and
// Resumes; Stop Suspend()s so CreateWindow is rejected and the window is gone.
func TestPairing_StartResumes_StopSuspends(t *testing.T) {
	srv := newSuspendedServer(t)
	startOnEphemeral(t, srv)
	if !srv.IsRunning() {
		t.Fatal("server must be running after Start")
	}
	if _, err := srv.CreatePairingWindow(true); err != nil {
		t.Fatalf("CreatePairingWindow after Start: %v", err)
	}
	if !srv.pairing.WindowActive() {
		t.Fatal("window must be active after Start + CreateWindow")
	}
	srv.Stop()
	waitForRunning(t, srv, false)
	if srv.pairing.WindowActive() {
		t.Fatal("Stop must cancel the active window")
	}
	if _, err := srv.CreatePairingWindow(true); err == nil {
		t.Fatal("CreatePairingWindow must be rejected after Stop")
	}
}

// TestPairing_ListenFailStaysSuspended: Start whose net.Listen fails MUST NOT
// Resume; CreateWindow stays rejected. (A port held open forces EADDRINUSE.)
func TestPairing_ListenFailStaysSuspended(t *testing.T) {
	srv := newSuspendedServer(t)
	// Hold a listener so Server.Start cannot bind the same port.
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold listen: %v", err)
	}
	t.Cleanup(func() { hold.Close() })
	port := hold.Addr().(*net.TCPAddr).Port
	srv.SetPort(port)
	if err := srv.Start(context.Background()); err == nil {
		t.Fatalf("Start on a held port must fail (EADDRINUSE), got nil")
	}
	if srv.IsRunning() {
		t.Fatal("server must NOT be running after a listen failure")
	}
	if _, err := srv.CreatePairingWindow(true); err == nil {
		t.Fatal("CreatePairingWindow must be rejected after a listen failure (never Resumed)")
	}
}

// TestPairing_RestartResumesAgain: after Stop→Start the service Resumes again
// and CreateWindow works, proving Resume/Start are not one-shot.
func TestPairing_RestartResumesAgain(t *testing.T) {
	srv := newSuspendedServer(t)
	startOnEphemeral(t, srv)
	if _, err := srv.CreatePairingWindow(true); err != nil {
		t.Fatalf("first CreatePairingWindow: %v", err)
	}
	srv.Stop()
	waitForRunning(t, srv, false)
	if _, err := srv.CreatePairingWindow(true); err == nil {
		t.Fatal("CreatePairingWindow must be rejected after Stop")
	}
	// Restart on a fresh ephemeral port.
	startOnEphemeral(t, srv)
	if _, err := srv.CreatePairingWindow(true); err != nil {
		t.Fatalf("CreatePairingWindow after restart: %v", err)
	}
	if !srv.pairing.WindowActive() {
		t.Fatal("window must be active after restart + CreateWindow")
	}
}

// TestPairing_ServeFailStaysSuspended: after Start publishes, closing the
// underlying listener forces the serve goroutine into stopInternal(serveFail),
// which Suspend()s the pairing service. CreateWindow is then rejected.
func TestPairing_ServeFailStaysSuspended(t *testing.T) {
	srv := newSuspendedServer(t)
	startOnEphemeral(t, srv)
	if _, err := srv.CreatePairingWindow(true); err != nil {
		t.Fatalf("CreatePairingWindow after Start: %v", err)
	}
	// Force a serve-fail by closing the published listener out from under Serve.
	srv.mu.Lock()
	run := srv.curRun
	srv.mu.Unlock()
	if run == nil || run.listener == nil {
		t.Fatal("no published run/listener to force a serve-fail")
	}
	run.listener.Close()
	waitForRunning(t, srv, false)
	if srv.pairing.WindowActive() {
		t.Fatal("serve-fail must Suspend the active window")
	}
	if _, err := srv.CreatePairingWindow(true); err == nil {
		t.Fatal("CreatePairingWindow must be rejected after a serve-fail (suspended)")
	}
}

// --- R2-Major-03: publish↔Resume concurrency fence ---
//
// These tests prove a concurrent Stop in the publish→Resume window can no longer
// leave the pairing service accepting on a stopped server, and that started/
// stopped events never reorder to stopped→started.

// serviceEventKinds reads the durable service events (newest-first) and returns
// just the kind strings, filtered to service lifecycle kinds.
func serviceEventKinds(t *testing.T, srv *Server) []string {
	t.Helper()
	recs, err := srv.ListRemoteSecurityEvents(64)
	if err != nil {
		t.Fatalf("ListRemoteSecurityEvents: %v", err)
	}
	var kinds []string
	for _, r := range recs {
		if r.Kind == "remote_service_started" || r.Kind == "remote_service_stopped" {
			kinds = append(kinds, r.Kind)
		}
	}
	return kinds
}

// TestPairing_ConcurrentStopInPublishWindow_NotAccepting is the R2-Major-03
// regression. A test barrier injects a FULL synchronous Stop between the run
// publish (running=true, curRun set) and the fenced lifecycle acceptance. With
// the old code the original Start goroutine would still call pairing.Resume()
// afterward, leaving accepting=true while IsRunning()=false. With the
// generation/stopping fence, publishLifecycleAcceptance observes curRun!=run &&
// stopping and skips Resume entirely. Final state: stopped + not accepting, and
// no started event (so events cannot be stopped→started).
func TestPairing_ConcurrentStopInPublishWindow_NotAccepting(t *testing.T) {
	srv := newSuspendedServer(t)
	srv.SetPort(0)

	barrierHit := false
	testLifecycleBarrier = func() {
		barrierHit = true
		// Inject a concurrent Stop exactly in the publish→acceptance window.
		srv.Stop() // blocks until stopInternal completes (run.done closed)
	}
	t.Cleanup(func() { testLifecycleBarrier = nil })

	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !barrierHit {
		t.Fatal("barrier must have fired between publish and acceptance")
	}
	// Terminal state: stopped, not accepting.
	waitForRunning(t, srv, false)
	if srv.pairing.accepting {
		t.Fatal("pairing MUST NOT be accepting after a concurrent Stop won the publish race")
	}
	if srv.registry.IsAccepting() {
		t.Fatal("registry MUST NOT be accepting after a concurrent Stop won the publish race")
	}
	if _, err := srv.CreatePairingWindow(true); err == nil {
		t.Fatal("CreatePairingWindow must be rejected (service suspended) after concurrent Stop")
	}
	// Events: the run was stopped before acceptance, so NO started event was
	// emitted — only stopped. This guarantees events never go stopped→started.
	kinds := serviceEventKinds(t, srv)
	for _, k := range kinds {
		if k == "remote_service_started" {
			t.Fatalf("started event must NOT be emitted when Stop won the publish race; events=%v", kinds)
		}
	}
	foundStopped := false
	for _, k := range kinds {
		if k == "remote_service_stopped" {
			foundStopped = true
		}
	}
	if !foundStopped {
		t.Fatalf("stopped event MUST be emitted; events=%v", kinds)
	}
}

// TestPairing_NormalStartStop_EventsInOrder proves the normal (uncontended)
// path emits started before stopped chronologically. ListRemoteSecurityEvents is
// newest-first, so stopped must precede started in the returned order.
func TestPairing_NormalStartStop_EventsInOrder(t *testing.T) {
	srv := newSuspendedServer(t)
	startOnEphemeral(t, srv)
	if !srv.pairing.accepting {
		t.Fatal("pairing must be accepting after a normal Start")
	}
	srv.Stop()
	waitForRunning(t, srv, false)
	kinds := serviceEventKinds(t, srv)
	// newest-first: expect [stopped, started].
	want := []string{"remote_service_stopped", "remote_service_started"}
	if len(kinds) < 2 {
		t.Fatalf("expected at least started+stopped events, got %v", kinds)
	}
	// Find the first two service-lifecycle events (they should be stopped then started).
	if kinds[0] != want[0] || kinds[1] != want[1] {
		t.Fatalf("event order newest-first=%v want %v (started must precede stopped chronologically)", kinds, want)
	}
}

// --- R3-Major-03: Stop tail-window stopping gate ---
// --- R3-N01: s.mu not held across durable I/O ---

// blockingSink wraps a real sink and blocks every AppendSecurityEvent call on
// `release` until the test closes it. Used to prove Stop can still acquire s.mu
// and complete stopping + Suspend/fence while a durable append is stalled
// (R3-N01).
type blockingSink struct {
	inner   SecurityEventSink
	release chan struct{}
}

func newBlockingSink(inner SecurityEventSink) *blockingSink {
	return &blockingSink{inner: inner, release: make(chan struct{})}
}
func (b *blockingSink) Durability() EventSinkDurability { return b.inner.Durability() }
func (b *blockingSink) AppendSecurityEvent(e SecurityEvent) (EventAppendResult, error) {
	<-b.release
	return b.inner.AppendSecurityEvent(e)
}

// TestPairing_StopTailWindow_NewStartBlocked proves the R3-Major-03 stopping
// gate: a Start injected DURING the Stop tail window (after running=false, before
// stopped event / done) MUST block on the entry gate (stopping=true) and cannot
// publish a new listener or emit a started event until the old Stop fully
// finishes. After Stop returns, the waiting Start proceeds; the stopped(old)
// event was appended before the started(new) event.
func TestPairing_StopTailWindow_NewStartBlocked(t *testing.T) {
	srv := newSuspendedServer(t)
	startOnEphemeral(t, srv) // emits started(old); t.Cleanup(srv.Stop)

	startErr := make(chan error, 1)
	var tailStartFired bool
	// One-shot (R4-N01): wrap the barrier so the cleanup srv.Stop() does NOT
	// re-trigger it and spawn a stray third Start. The first stopInternal (the
	// one under test) fires it exactly once.
	var once sync.Once
	testStopTailBarrier = func() {
		once.Do(func() {
			tailStartFired = true
			// Inject a concurrent Start in the tail window (stopping==true here).
			go func() { startErr <- srv.Start(context.Background()) }()
			// Give Start a chance to (incorrectly) proceed past the gate.
			time.Sleep(40 * time.Millisecond)
			select {
			case err := <-startErr:
				t.Errorf("Start returned during Stop tail window (stopping gate failed): %v", err)
			default:
				// Good: Start is blocked on the stopping gate.
			}
		})
	}
	t.Cleanup(func() { testStopTailBarrier = nil })

	srv.Stop() // runs the barrier inside stopInternal's tail window
	waitForRunning(t, srv, false)
	if !tailStartFired {
		t.Fatal("tail barrier must have fired during Stop")
	}
	// The old run fully stopped; the waiting Start now proceeds.
	select {
	case err := <-startErr:
		if err != nil {
			t.Fatalf("deferred Start after Stop returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deferred Start did not complete after Stop finished")
	}
	waitForRunning(t, srv, true)
	// Event order: stopped(old) appended BEFORE started(new). In newest-first the
	// most recent service event is started (the new run), not stopped.
	kinds := serviceEventKinds(t, srv)
	if len(kinds) < 2 {
		t.Fatalf("expected at least started+stopped events, got %v", kinds)
	}
	if kinds[0] != "remote_service_started" {
		t.Fatalf("newest service event=%q want remote_service_started (stopped(old) must precede started(new)); events=%v", kinds[0], kinds)
	}
	// Clean up the new run (barrier is one-shot, so this Stop does not re-inject).
	srv.Stop()
	waitForRunning(t, srv, false)
}

// TestPairing_BlockedSink_StopStillAcquiresStateMu proves R3-N01: when the
// durable sink append is blocked, Stop can STILL promptly acquire s.mu to commit
// stopping and complete Suspend/fence. With the old code Start held s.mu across
// emitServiceEvent; now emit is outside s.mu, so Stop is not stalled by a sink
// append (whether Start's started or Stop's own stopped).
func TestPairing_BlockedSink_StopStillAcquiresStateMu(t *testing.T) {
	srv := newSuspendedServer(t)
	startOnEphemeral(t, srv) // started(old) emitted before we block the sink
	t.Cleanup(srv.Stop)

	// Replace the sink with one that blocks every append until released.
	blk := newBlockingSink(srv.sink)
	srv.sink = blk
	srv.pairing.sink = blk // deviceService also appends via its sink reference

	// Stop in a goroutine: it will reach the stopped-event append (blocked), but
	// the state flip + Suspend/fence must complete first.
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		srv.Stop()
	}()

	// Within a short window, stopping must be committed and pairing suspended,
	// even though the (blocked) stopped-event append has not returned.
	deadline := time.Now().Add(2 * time.Second)
	suspended := false
	for time.Now().Before(deadline) {
		srv.mu.Lock()
		stopping := srv.stopping
		running := srv.running
		srv.mu.Unlock()
		if stopping && !running && !srv.pairing.accepting {
			suspended = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !suspended {
		t.Fatal("Stop did not commit stopping + Suspend/fence while the sink append was blocked (s.mu was held across durable I/O)")
	}
	// Stop is still blocked on the stopped-event append; release the sink so the
	// test can finish.
	close(blk.release)
	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return after the sink was released")
	}
}

// --- R4-Major: acceptance→emit window per-run handshake ---

// serviceRecordingSink wraps a real sink and records each appended event's kind string
// in append order. The recording stays readable even after the inner sink is
// closed (e.g. by CloseSecurityState), so tests can assert event order across a
// Shutdown without needing ListRemoteSecurityEvents (which requires an open
// sink). Used by the R4-Major Shutdown scenario.
type serviceRecordingSink struct {
	inner SecurityEventSink
	mu    sync.Mutex
	kinds []string
}

func newServiceRecordingSink(inner SecurityEventSink) *serviceRecordingSink {
	return &serviceRecordingSink{inner: inner}
}
func (r *serviceRecordingSink) Durability() EventSinkDurability { return r.inner.Durability() }
func (r *serviceRecordingSink) AppendSecurityEvent(e SecurityEvent) (EventAppendResult, error) {
	r.mu.Lock()
	r.kinds = append(r.kinds, serviceEventKindStr(e))
	r.mu.Unlock()
	return r.inner.AppendSecurityEvent(e)
}
func (r *serviceRecordingSink) kindsSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.kinds))
	copy(out, r.kinds)
	return out
}

// serviceEventKindStr returns the wire kind string for a service/legacy event,
// or "" for other event types.
func serviceEventKindStr(e SecurityEvent) string {
	switch ev := e.(type) {
	case ServiceSecurityEvent:
		return serviceKindString(ev.Kind)
	case LegacyAuthSecurityEvent:
		return "legacy_auth_deprecated"
	}
	return ""
}

// waitForStopping polls s.mu until stopping==true (Stop section-1 committed) or
// times out. Used by acceptance→emit barriers to make a direct Stop's section-1
// commit deterministic before Start continues past the barrier.
func waitForStopping(t *testing.T, srv *Server, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		srv.mu.Lock()
		st := srv.stopping
		srv.mu.Unlock()
		if st {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("stopping did not commit within %v", timeout)
}

// TestPairing_AcceptanceEmitWindow_DirectStop proves the R4-Major handshake: a
// direct Stop injected in the acceptance→emit window cannot leave a
// stopped→started event reorder or a Start that returns nil-success for a dead
// run. The handshake makes stopInternal wait on startedDone, so started is
// appended before stopped; Start re-checks stillCurrent and returns an error.
func TestPairing_AcceptanceEmitWindow_DirectStop(t *testing.T) {
	srv := newSuspendedServer(t)
	srv.SetPort(0)
	t.Cleanup(srv.Stop)

	stopDone := make(chan struct{})
	testAcceptanceEmitBarrier = func() {
		// acceptance committed (startedPending=true). Inject a direct Stop async;
		// stopInternal will wait on startedDone after section-1.
		go func() {
			defer close(stopDone)
			srv.Stop()
		}()
		// Make section-1 deterministic before Start continues past the barrier.
		waitForStopping(t, srv, time.Second)
	}
	t.Cleanup(func() { testAcceptanceEmitBarrier = nil })

	err := srv.Start(context.Background())
	if err == nil {
		t.Fatal("Start must return an error (not nil) when a direct Stop wins the acceptance→emit window — the run is dead")
	}
	// Nil the barrier so the fresh Start below does not re-trigger it.
	testAcceptanceEmitBarrier = nil
	// The async Stop completes (it was waiting on startedDone which Start closed).
	<-stopDone
	waitForRunning(t, srv, false)
	// Event order: started appended BEFORE stopped (handshake). newest-first =
	// [stopped, started] for this run's pair.
	kinds := serviceEventKinds(t, srv)
	if len(kinds) < 2 {
		t.Fatalf("expected started+stopped events, got %v", kinds)
	}
	if kinds[0] != "remote_service_stopped" || kinds[1] != "remote_service_started" {
		t.Fatalf("newest-first=%v want [stopped, started] (started must be appended before stopped); events=%v", kinds[:2], kinds)
	}
	// No listener/goroutine leak: Start did not spawn Serve/ctx for the dead run,
	// and Stop closed the listener. A fresh Start must succeed (port reusable).
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("fresh Start after acceptance→emit Stop failed (listener/goroutine leak?): %v", err)
	}
	waitForRunning(t, srv, true)
}

// TestPairing_AcceptanceEmitWindow_Shutdown proves the R4-Major handshake against
// the Shutdown path (Stop + CloseSecurityState): the started append completes
// (closes startedDone) BEFORE Stop proceeds to append stopped and BEFORE
// CloseSecurityState closes the sink — no late started write into a closed sink,
// and Start returns an error for the dead run.
func TestPairing_AcceptanceEmitWindow_Shutdown(t *testing.T) {
	srv := newSuspendedServer(t)
	srv.SetPort(0)
	t.Cleanup(srv.Stop)

	// Wrap the service-event sink with a recorder so event order stays readable
	// after CloseSecurityState closes the durable sink.
	rec := newServiceRecordingSink(srv.sink)
	srv.sink = rec
	srv.pairing.sink = rec

	shutdownDone := make(chan struct{})
	testAcceptanceEmitBarrier = func() {
		go func() {
			defer close(shutdownDone)
			srv.Stop()
			// CloseSecurityState closes the durable sink (simulates App.Shutdown).
			_ = srv.CloseSecurityState()
		}()
		waitForStopping(t, srv, time.Second)
	}
	t.Cleanup(func() { testAcceptanceEmitBarrier = nil })

	err := srv.Start(context.Background())
	if err == nil {
		t.Fatal("Start must return an error when Shutdown (Stop+CloseSecurityState) wins the acceptance→emit window")
	}
	testAcceptanceEmitBarrier = nil
	<-shutdownDone
	waitForRunning(t, srv, false)
	// The started event was appended before the sink closed: the handshake makes
	// stopInternal wait on startedDone (Start closes it right after the started
	// append), and CloseSecurityState runs after Stop returns. The recorder is
	// append-order and stays readable after the inner sink closed.
	raw := rec.kindsSnapshot()
	var kinds []string
	for _, k := range raw {
		if k == "remote_service_started" || k == "remote_service_stopped" {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) < 2 {
		t.Fatalf("expected started+stopped appends before sink close, got recorder=%v", raw)
	}
	// append-order: started first, then stopped.
	if kinds[0] != "remote_service_started" || kinds[1] != "remote_service_stopped" {
		t.Fatalf("append-order=%v want [started, stopped] (started appended before stopped, before sink close); recorder=%v", kinds, raw)
	}
}
