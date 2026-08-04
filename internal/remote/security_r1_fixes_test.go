package remote

// security_r1_fixes_test.go — M2-INT R1 batch-1 security fixes (C-001,
// M-001, M-002, M-008) real-path tests.
//
// These tests exercise the production code paths (public Gate API, real hook
// ordering, real Terminate) — they do NOT manually construct internal state to
// fake a result (cf. diting M-011 critique of T39–T44).

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Shared fakes
// ---------------------------------------------------------------------------

// recordingRawPort implements both PTYRawPort and SessionRawPort, recording all
// raw mutations. It is the seam behind the gate for WS input/resize/lifecycle.
type recordingRawPort struct {
	mu        sync.Mutex
	writes    [][]byte
	resizes   []resizeCall
	lastWrite []byte
}

type resizeCall struct {
	sessionID  string
	cols, rows int
}

func (r *recordingRawPort) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	r.writes = append(r.writes, cp)
	r.lastWrite = cp
	return nil
}

func (r *recordingRawPort) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resizes = append(r.resizes, resizeCall{sessionID, cols, rows})
	return nil
}

func (r *recordingRawPort) DetachSession(sessionID string) (BackendDetachReceipt, error) {
	return confirmedTestDetachReceipt(), nil
}

func (r *recordingRawPort) StopSession(context.Context, contract.SessionID) error   { return nil }
func (r *recordingRawPort) RemoveSession(context.Context, contract.SessionID) error { return nil }
func (r *recordingRawPort) ResizeSession(_ context.Context, _ contract.SessionID, cols, rows int) error {
	return r.ResizeRaw(context.Background(), "", cols, rows)
}

func (r *recordingRawPort) WriteCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.writes)
}

// recordingSessionRaw records stop/remove/resize lifecycle calls.
type recordingSessionRaw struct {
	mu     sync.Mutex
	stop   int
	remove int
	resize int
	// blockStop, if non-nil, blocks StopSession until the channel is closed,
	// letting a lifecycle raw effect hold the operation lane.
	blockStop chan struct{}
}

func (r *recordingSessionRaw) StopSession(context.Context, contract.SessionID) error {
	r.mu.Lock()
	r.stop++
	blk := r.blockStop
	r.mu.Unlock()
	if blk != nil {
		<-blk // hold the lane
	}
	return nil
}
func (r *recordingSessionRaw) RemoveSession(context.Context, contract.SessionID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.remove++
	return nil
}
func (r *recordingSessionRaw) ResizeSession(context.Context, contract.SessionID, int, int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resize++
	return nil
}

// buildAdapterForTest builds a ready ControlRuntime + RemoteSessionAdapter with
// the given raw ports, returning all the pieces a WS/lifecycle test needs.
func buildAdapterForTest(t *testing.T, ptyRaw PTYRawPort, sessRaw SessionRawPort) (*ControlRuntime, *RemoteSessionAdapter, *SessionStreamStore) {
	t.Helper()
	clk := newCtrlFakeClock(time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	rt := NewControlRuntime(clk, nil)
	if ptyRaw != nil {
		rt.SetPTYRawPort(ptyRaw)
	}
	rt.MarkReady()
	streams := NewSessionStreamStore()
	catalog := NewSessionCatalog()
	adapter := NewRemoteSessionAdapter(rt.Gate(), rt, catalog, streams, nil, nil, nil, sessRaw, clk, "")
	return rt, adapter, streams
}

// newAttachedWSConn constructs a wsV1Connection in the attached state for a
// device holder, ready to receive input/resize frames.
func newAttachedWSConn(t *testing.T, adapter *RemoteSessionAdapter, sid contract.SessionID) (*wsV1Connection, *ControlConnectionLease) {
	t.Helper()
	dir := adapter.Runtime().Directory()
	arb := adapter.Runtime().Arbiter()
	lease := attachAndAcquire(t, dir, arb, "devA", "Device A", "conn1", sid)
	conn := &wsV1Connection{
		adapter:   adapter,
		state:     wsV1StateAttached,
		lease:     lease,
		sessionID: sid,
		inputIDs:  make(map[contract.MessageID]struct{}),
		done:      make(chan struct{}),
	}
	return conn, lease
}

// ===========================================================================
// C-001: WS input must NOT be copied into the output stream store / replay /
// broadcast. Only the real PTY output producer may allocate Seq + append to
// replay (diting C-001).
// ===========================================================================

func TestC001_InputNotWrittenToOutputStreamOrReplay(t *testing.T) {
	raw := &recordingRawPort{}
	rt, adapter, streams := buildAdapterForTest(t, raw, raw)
	arb := rt.Arbiter()
	sid := startSessionDirect(t, arb)

	conn, _ := newAttachedWSConn(t, adapter, sid)

	secret := []byte("hunter2-secret-token")
	conn.handleInput(contract.InputFrame{
		Type: contract.ClientFrameTypeInput,
		ID:   "in1",
		Data: base64.StdEncoding.EncodeToString(secret),
	})

	// The input DID reach the raw PTY (one-way sink).
	if got := raw.WriteCount(); got != 1 {
		t.Fatalf("expected exactly 1 raw PTY write, got %d", got)
	}
	if !bytes.Equal(raw.lastWrite, secret) {
		t.Fatalf("raw PTY write mismatch: got %q want %q", raw.lastWrite, secret)
	}

	// The stream store / replay must NOT contain the input bytes.
	earliest, latest := streams.SeqBounds(sid)
	if earliest != 0 || latest != 0 {
		t.Fatalf("expected empty stream store after input (no Seq allocated), got earliest=%d latest=%d", earliest, latest)
	}
	if frames := streams.FramesAfter(sid, nil); len(frames) != 0 {
		t.Fatalf("expected no replay frames after input, got %d", len(frames))
	}
}

// TestC001_ObserverBackfillExcludesInput proves a second observer's history /
// backfill never contains the controller's typed input (the leak vector C-001
// describes). A real PTY output is the only thing that appears in replay.
func TestC001_ObserverBackfillExcludesInput(t *testing.T) {
	raw := &recordingRawPort{}
	rt, adapter, streams := buildAdapterForTest(t, raw, raw)
	arb := rt.Arbiter()
	sid := startSessionDirect(t, arb)

	conn, _ := newAttachedWSConn(t, adapter, sid)

	// Controller types a secret.
	conn.handleInput(contract.InputFrame{
		Type: contract.ClientFrameTypeInput,
		ID:   "in1",
		Data: base64.StdEncoding.EncodeToString([]byte("PASSWORD-LEAK-ME")),
	})
	// Real PTY echoes output (the only legitimate replay content).
	streams.AppendOutput(sid, []byte("real-pty-output"))

	frames := streams.FramesAfter(sid, nil)
	if len(frames) != 1 {
		t.Fatalf("expected exactly 1 replay frame (the PTY output), got %d", len(frames))
	}
	for _, f := range frames {
		if bytes.Contains(f.output, []byte("PASSWORD-LEAK-ME")) {
			t.Fatalf("replay leaked controller input: %q", f.output)
		}
	}
}

// ===========================================================================
// M-008: device revoke termination must send auth.revoked{reason:
// device_revoked} via the sole writer BEFORE closing 1008, not just close.
// ===========================================================================

func TestM008_DeviceRevokeTerminationSendsAuthRevokedBeforeClose(t *testing.T) {
	// Use an in-process WS pair to observe the real sole-writer ordering.
	srvConn, clientConn := dialWSV1Pipe(t)
	defer srvConn.Close()
	defer clientConn.Close()

	at := time.Date(2026, 8, 4, 12, 0, 1, 0, time.UTC)
	conn := &wsV1Connection{
		conn:     srvConn,
		state:    wsV1StateAttached,
		done:     make(chan struct{}),
		inputIDs: make(map[contract.MessageID]struct{}),
	}

	// Production termination path for device revoke.
	conn.Terminate(ConnectionTermination{Cause: TerminationDeviceRevoked, OccurredAt: at})

	// Read frames until we see auth.revoked and then the close.
	var sawAuthRevoked bool
	var authRevokedBeforeClose bool
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msgType, payload, err := clientConn.ReadMessage()
		if err != nil {
			// A close error terminates the loop.
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.ClosePolicyViolation) || sawAuthRevoked {
				break
			}
			// Any transport end after we've seen the event is acceptable.
			if sawAuthRevoked {
				break
			}
			t.Fatalf("read error before auth.revoked: %v", err)
		}
		_ = msgType
		if bytes.Contains(payload, []byte(`"auth.revoked"`)) {
			sawAuthRevoked = true
			authRevokedBeforeClose = true // text event seen before the close frame
			// Validate the event fields (CG-01 frozen symbols).
			ev, derr := contract.DecodeKnownServerEvent(payload)
			if derr != nil {
				t.Fatalf("decode auth.revoked: %v", derr)
			}
			are, ok := ev.(contract.AuthRevokedEvent)
			if !ok {
				t.Fatalf("expected AuthRevokedEvent, got %T", ev)
			}
			if are.Reason != contract.AuthRevokedReasonDeviceRevoked {
				t.Fatalf("expected reason device_revoked, got %q", are.Reason)
			}
			if are.OccurredAt == "" {
				t.Fatal("expected non-empty occurredAt")
			}
		}
		if msgType == websocket.CloseMessage {
			break
		}
	}
	if !sawAuthRevoked {
		t.Fatal("expected auth.revoked event before close 1008, got none")
	}
	if !authRevokedBeforeClose {
		t.Fatal("auth.revoked event must precede the close frame")
	}
}

// TestM008_NonRevokeTerminationSendsNoAuthRevoked ensures only device-revoke
// emits the typed auth.revoked event (other causes just close).
func TestM008_NonRevokeTerminationSendsNoAuthRevoked(t *testing.T) {
	srvConn, clientConn := dialWSV1Pipe(t)
	defer srvConn.Close()
	defer clientConn.Close()

	conn := &wsV1Connection{
		conn:     srvConn,
		state:    wsV1StateAttached,
		done:     make(chan struct{}),
		inputIDs: make(map[contract.MessageID]struct{}),
	}
	conn.Terminate(ConnectionTermination{Cause: TerminationServerStopped, OccurredAt: time.Now()})

	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, payload, err := clientConn.ReadMessage()
		if err != nil {
			break
		}
		if bytes.Contains(payload, []byte(`"auth.revoked"`)) {
			t.Fatal("server-stopped termination must NOT emit auth.revoked")
		}
	}
}

var _ atomic.Int32 // used by M-001/M-002 tests added below

// dialWSV1Pipe creates an in-process WS pair (no network) for sole-writer
// ordering assertions.
func dialWSV1Pipe(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	srvCh := make(chan *websocket.Conn, 1)
	hs := httptestServer(upgrader, srvCh)
	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+hs.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	var srvConn *websocket.Conn
	select {
	case srvConn = <-srvCh:
	case <-time.After(2 * time.Second):
		t.Fatal("server side did not upgrade in time")
	}
	return srvConn, clientConn
}

// httptestServer starts a minimal HTTP server that upgrades one connection and
// hands the *websocket.Conn to srvCh.
func httptestServer(upgrader websocket.Upgrader, srvCh chan<- *websocket.Conn) *httptest.Server {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		srvCh <- c
		<-r.Context().Done() // keep conn open until the test closes it
	})
	return httptest.NewServer(h)
}

// ===========================================================================
// M-001: pendingDrain two-phase exact-match (diting M-001).
//
// These tests go through the PUBLIC Gate lifecycle API and serialize on the
// real operation lane — they do NOT manually construct pendingDrain state.
// ===========================================================================

// waitForPendingLifecycle polls (with a deadline) until the entry has an active
// (non-closed) pendingDrain of the given kind, proving phase-1 committed.
func waitForPendingLifecycle(t *testing.T, entry *controlEntry, kind LifecycleOperation) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		entry.stateMu.Lock()
		pd := entry.pendingDrain
		active := pd != nil && pd.closed == nil && pd.kind == kind
		entry.stateMu.Unlock()
		if active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for active pendingDrain kind=%v", kind)
}

// TestM001_ConcurrentLifecycleDeniesInProgressNotOverwrite: a lifecycle is in
// phase-1 (pendingDrain active, blocked on the lane held by a regular write).
// A concurrent lifecycle MUST get lifecycle.in_progress and MUST NOT overwrite
// the in-flight intent. After the lane releases, the first lifecycle completes.
func TestM001_ConcurrentLifecycleDeniesInProgressNotOverwrite(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()
	gate := rt.Gate().(*controlGate)
	arb := rt.Arbiter()
	sid := startSessionDirect(t, arb)
	auth := newWailsAuthority(1)

	if err := gate.TakeDesktop(context.Background(), auth, sid); err != nil {
		t.Fatalf("TakeDesktop: %v", err)
	}

	// 1. A desktop PTY write holds the operation lane (raw effect blocks).
	holdLane := make(chan struct{})
	ptStarted := make(chan struct{})
	go func() {
		_ = gate.DoDesktopPTY(context.Background(), auth, sid, PTYInput, func(ctx context.Context, permit *operationPermit) error {
			if err := permit.Checkpoint(ctx, 1); err != nil {
				select {
				case <-ptStarted:
				default:
					close(ptStarted)
				}
				return err
			}
			close(ptStarted)
			<-holdLane // hold the lane until the test releases it
			return nil
		})
	}()
	<-ptStarted // PTY write now holds the lane

	// 2. A lifecycle stop commits phase-1 (sets pending=stop) then blocks on the
	//    lane acquire (still held by the PTY write).
	stopDone := make(chan error, 1)
	go func() {
		_, err := gate.DoDesktopLifecycle(context.Background(), auth, sid, LifecycleStop, func(ctx context.Context, permit *operationPermit) (SessionMutationResult, error) {
			return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
		})
		stopDone <- err
	}()
	entry := arb.entryFor(sid)
	waitForPendingLifecycle(t, entry, LifecycleStop) // phase-1 committed

	// 3. Concurrent remove: phase-1 MUST see the active stop intent and deny
	//    lifecycle.in_progress WITHOUT overwriting it.
	removeRawCalled := atomic.Int32{}
	_, rmErr := gate.DoDesktopLifecycle(context.Background(), auth, sid, LifecycleRemove, func(ctx context.Context, permit *operationPermit) (SessionMutationResult, error) {
		removeRawCalled.Add(1)
		return SessionMutationResult{Removed: true}, nil
	})
	if kind, ok := extractControlDenyKind(rmErr); !ok || kind != DenyLifecycleInProgress {
		t.Fatalf("expected DenyLifecycleInProgress for concurrent lifecycle, got %v", rmErr)
	}
	if removeRawCalled.Load() != 0 {
		t.Fatal("concurrent lifecycle raw effect must NOT execute")
	}
	entry.stateMu.Lock()
	pd := entry.pendingDrain
	entry.stateMu.Unlock()
	if pd == nil || pd.kind != LifecycleStop || pd.closed != nil {
		t.Fatalf("pendingDrain must remain the active stop intent, got %+v", pd)
	}

	// 4. Release the lane: the stop lifecycle acquires it, phase-2 exact-matches,
	//    and succeeds.
	close(holdLane)
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("stop lifecycle should succeed after lane release, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop lifecycle did not complete after lane release")
	}
}

// TestM001_FencedLifecycleIntentDeniesPhase2: a fence (release) advances
// holderGeneration between phase-1 and phase-2, so phase-2 exact-match fails
// and the raw effect does NOT execute. This proves the generation guard in the
// real two-phase path (not a manual pointer poke).
func TestM001_FencedLifecycleIntentDeniesPhase2(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()
	gate := rt.Gate().(*controlGate)
	arb := rt.Arbiter()
	sid := startSessionDirect(t, arb)
	auth := newWailsAuthority(1)
	if err := gate.TakeDesktop(context.Background(), auth, sid); err != nil {
		t.Fatalf("TakeDesktop: %v", err)
	}

	// A PTY write holds the lane so the lifecycle stops in phase-1.
	holdLane := make(chan struct{})
	ptStarted := make(chan struct{})
	go func() {
		_ = gate.DoDesktopPTY(context.Background(), auth, sid, PTYInput, func(ctx context.Context, permit *operationPermit) error {
			if err := permit.Checkpoint(ctx, 1); err != nil {
				close(ptStarted)
				return err
			}
			close(ptStarted)
			<-holdLane
			return nil
		})
	}()
	<-ptStarted

	// Lifecycle stop commits phase-1 then blocks on the lane.
	stopDone := make(chan error, 1)
	go func() {
		_, err := gate.DoDesktopLifecycle(context.Background(), auth, sid, LifecycleStop, func(ctx context.Context, permit *operationPermit) (SessionMutationResult, error) {
			return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
		})
		stopDone <- err
	}()
	entry := arb.entryFor(sid)
	waitForPendingLifecycle(t, entry, LifecycleStop)

	// Fence: release desktop advances holderGeneration (commitTransition closes
	// the intent for the old generation). Phase-2 exact-match must now fail.
	if err := gate.ReleaseDesktop(context.Background(), auth, sid); err != nil {
		t.Fatalf("ReleaseDesktop: %v", err)
	}

	close(holdLane) // let the lifecycle acquire the lane and attempt phase-2
	select {
	case err := <-stopDone:
		if kind, ok := extractControlDenyKind(err); !ok || kind != DenyStalePermit {
			t.Fatalf("expected DenyStalePermit after fence, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fenced lifecycle did not return")
	}
}

// ===========================================================================
// M-002: revoke / server-stop fence integrity (diting M-002).
//
// Real-path tests: MarkDeviceRevoked fences in-flight ops via the real
// arbiter method; server-stop clears device holders via the production
// lifecycleHookAdapter (no manual lease.fence()).
// ===========================================================================

// TestM002_MarkDeviceRevoked_FencesInflightOpAndRejectsNewPermits: a device
// holds a session with an in-flight gated PTY write. MarkDeviceRevoked (the
// production hook entrypoint) synchronously fences the in-flight op so a
// subsequent Checkpoint fails, and new device permits are stably rejected
// (DenyDeviceRevoked) in the revoke window.
func TestM002_MarkDeviceRevoked_FencesInflightOpAndRejectsNewPermits(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()
	gate := rt.Gate().(*controlGate)
	arb := rt.Arbiter()
	sid := startSessionDirect(t, arb)
	lease := attachAndAcquire(t, rt.Directory(), arb, "devA", "Device A", "conn1", sid)

	// Start a device PTY write that passes Checkpoint(1) then blocks before a
	// second Checkpoint. MarkDeviceRevoked must fence it.
	firstCkPassed := make(chan struct{})
	release := make(chan struct{})
	secondCkErr := make(chan error, 1)
	go func() {
		err := gate.DoDevicePTY(context.Background(), lease, sid, PTYInput, func(ctx context.Context, permit *operationPermit) error {
			if err := permit.Checkpoint(ctx, 1); err != nil {
				secondCkErr <- err
				return err
			}
			close(firstCkPassed)
			<-release
			e := permit.Checkpoint(ctx, 2)
			secondCkErr <- e
			return e
		})
		_ = err
	}()
	<-firstCkPassed // op is in-flight (past first checkpoint, holding the lane)

	// Production revoke fence entrypoint (called by lifecycleHookAdapter).
	arb.MarkDeviceRevoked("devA")

	// Release the in-flight op: its second Checkpoint must have been fenced, so
	// the raw effect returns and the lane is freed.
	close(release)
	select {
	case e := <-secondCkErr:
		if e == nil {
			t.Fatal("expected in-flight op second Checkpoint to fail after MarkDeviceRevoked")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight op did not observe fence")
	}

	// New device PTY permit must be stably rejected in the revoke window (lane
	// is now free, so this reaches createDevicePTYPermit's revoked-set check).
	newWrite := atomic.Int32{}
	rmErr := gate.DoDevicePTY(context.Background(), lease, sid, PTYInput, func(ctx context.Context, permit *operationPermit) error {
		newWrite.Add(1)
		return nil
	})
	if kind, ok := extractControlDenyKind(rmErr); !ok || kind != DenyDeviceRevoked {
		t.Fatalf("expected DenyDeviceRevoked after MarkDeviceRevoked, got %v", rmErr)
	}
	if newWrite.Load() != 0 {
		t.Fatal("new device PTY write must not execute after revoke")
	}
}

// TestM002_ServerStopReleasesAllRemoteHolders: the production server-stop
// sequence (lifecycleHookAdapter.FenceAllRemote then ReleaseAllRemote, exactly
// what stopInternal now performs) clears every device holder to ownerNone with
// a typed transition and advances holderGeneration, so a restart leaves no stale
// holder. No manual lease.fence() is used.
func TestM002_ServerStopReleasesAllRemoteHolders(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()
	arb := rt.Arbiter()
	sid := startSessionDirect(t, arb)
	sid2 := startSessionDirectEntry(t, arb, "s2")
	attachAndAcquire(t, rt.Directory(), arb, "devA", "Device A", "conn1", sid)
	attachAndAcquire(t, rt.Directory(), arb, "devB", "Device B", "conn2", sid2)

	// Production hook adapter (the concrete type stopInternal calls through).
	hook := NewControlLifecycleHook(rt).(*lifecycleHookAdapter)

	genBeforeA := holderGen(arb, sid)
	genBeforeB := holderGen(arb, sid2)

	// Server-stop two-phase via the real hook (FenceAllRemote FIRST, then
	// ReleaseAllRemote after registry terminate — the authority order stopInternal
	// now follows).
	hook.FenceAllRemote(ControlCauseServerStopped, time.Now())
	hook.ReleaseAllRemote(ControlCauseServerStopped, time.Now())

	// All device holders cleared (no stale holder survives a restart).
	for _, s := range []contract.SessionID{sid, sid2} {
		snap, gErr := arb.SnapshotForDevice(s, "devA")
		if gErr != nil || snap.State != contract.ControlStateNone {
			t.Fatalf("session %s: expected device holder cleared to none, got state=%s err=%v", s, snap.State, gErr)
		}
	}
	// holderGeneration advanced (typed transition committed, not a silent clear).
	if genAfter := holderGen(arb, sid); genAfter <= genBeforeA {
		t.Fatalf("holderGeneration must advance on server-stop release: before=%d after=%d", genBeforeA, genAfter)
	}
	if genAfter := holderGen(arb, sid2); genAfter <= genBeforeB {
		t.Fatalf("holderGeneration must advance on server-stop release: before=%d after=%d", genBeforeB, genAfter)
	}
}

// startSessionDirectEntry registers a public session with an arbitrary id.
func startSessionDirectEntry(t *testing.T, arb *ControlArbiter, id string) contract.SessionID {
	t.Helper()
	sid := contract.SessionID(id)
	entry := &controlEntry{
		sessionID:    sid,
		owner:        controlOwner{kind: ownerNone},
		controlEpoch: 1,
		opLane:       newBoundedOperationLane(),
		runPhase:     runActive,
		backend:      backendHealthy,
	}
	entry.currentRun = &runIdentity{nonce: 2, desktopRunToken: "tok2"}
	entry.runEpoch = 1
	entry.stateMirror = contract.SessionStateRunning
	entry.stateMirrorSet = true
	arb.tableMu.Lock()
	arb.entries[sid] = entry
	arb.tableMu.Unlock()
	return sid
}
