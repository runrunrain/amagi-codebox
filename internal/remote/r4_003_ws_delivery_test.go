package remote

// R4-003 regression coverage for the /ws/v1 delivery transaction. These tests
// deliberately exercise the production control-subscriber fencer seam rather
// than the old hub-only “both queues populated” assertion.

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"amagi-codebox/internal/remote/contract"
)

// TestR4_003_LateFencerInstallCompletesAttachWindowTeardown proves the exact
// reviewed gap: an overflow can happen after AttachControl subscribes but before
// the WS actor installs its transport fencer. Installing the fencer after that
// overflow must observe the already-fenced subscriber and complete teardown
// exactly once; merely assigning the callback leaves a fenced lease on a live
// socket.
func TestR4_003_LateFencerInstallCompletesAttachWindowTeardown(t *testing.T) {
	arb, _, hub, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	principal := newTestDevicePrincipal("devA", "Device A")
	lease, _ := dir.Attach(principal.DeviceID, principal.DeviceName, "connA", sid)
	consumer := newSpyConsumer()
	sub := hub.Subscribe(sid, principal.DeviceID, lease, consumer)
	sub.mu.Lock()
	sub.capacity = 1
	sub.mu.Unlock()

	// Overflow before a transport fencer exists: none→you fills the one-slot
	// queue; you→desktop overflows and consumes the authority-fence once.
	arb.Acquire(principal, lease, sid)
	arb.TakeDesktop(newWailsAuthority(1), sid)
	if !sub.IsFenced() || lease.IsLive() {
		t.Fatal("pre-install overflow must synchronously fence subscriber and lease")
	}

	var teardownCalls atomic.Int32
	fencer := SubscriptionAuthorityFencer(recordingFenceFunc(func() {
		teardownCalls.Add(1)
	}))
	sub.SetAuthorityFencer(fencer)
	// Re-installation must not duplicate the exact teardown effect.
	sub.SetAuthorityFencer(fencer)

	deadline := time.Now().Add(time.Second)
	for teardownCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := teardownCalls.Load(); got != 1 {
		t.Fatalf("late fencer install teardown calls = %d, want exactly 1", got)
	}
}

// recordingFenceFunc is a tiny fencer used to assert the late-install effect.
type recordingFenceFunc func()

func (f recordingFenceFunc) FenceSubscriptionWrites(SubscriptionFenceToken, interface{}) AuthorityFenceReceipt {
	f()
	return AuthorityFenceReceipt{LeaseFenced: true}
}

// TestR4_003_AllWebSocketWritesHaveOneOwner is a structural guard against the
// reviewed read-loop bypass. Text events, terminal pre-close events, and the WS
// close frame must all reach gorilla's WriteMessage from one function owned by
// the outbound writer goroutine.
func TestR4_003_AllWebSocketWritesHaveOneOwner(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "ws_v1_session.go", nil, 0)
	if err != nil {
		t.Fatalf("parse ws_v1_session.go: %v", err)
	}
	writeAPIs := map[string]bool{
		"WriteMessage": true,
		"WriteJSON":    true,
		"WriteControl": true,
		"NextWriter":   true,
	}
	var owners []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if ok && writeAPIs[sel.Sel.Name] {
				owners = append(owners, fn.Name.Name+"."+sel.Sel.Name)
			}
			return true
		})
	}
	if len(owners) != 1 || owners[0] != "writeSocketFrame.WriteMessage" {
		t.Fatalf("socket write API owners = %v, want exactly [writeSocketFrame.WriteMessage]", owners)
	}
}

// TestR4_003_ControlQueuedDuringAttachStillFollowsAttached proves the direct
// control-to-final-queue path cannot violate M-007. A transition admitted before
// session.attached remains behind the bootstrap gate; the sole writer emits
// attached first and only then opens post-attach delivery.
func TestR4_003_ControlQueuedDuringAttachStillFollowsAttached(t *testing.T) {
	serverConn, clientConn := dialWSV1Pipe(t)
	defer serverConn.Close()
	defer clientConn.Close()

	conn := &wsV1Connection{
		conn:  serverConn,
		state: wsV1StateRegisteredAwaitAttach,
		done:  make(chan struct{}),
	}
	conn.ensureOutboundWriter()
	control := contract.ControlStateEvent{
		Type: contract.ServerEventTypeControlState, SessionID: "s-bootstrap",
		State: contract.ControlStateDesktop, Reason: "during_attach",
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if !conn.enqueueControlStateOutbound(control) {
		t.Fatal("control admission during attach unexpectedly failed")
	}
	attached := contract.SessionAttachedEvent{
		Type: contract.ServerEventTypeSessionAttached, RequestID: "attach-bootstrap",
		APIVersion: contract.APIVersionV1, SessionID: "s-bootstrap",
		History: []contract.ReplayFrame{}, EarliestSeq: 0, LatestSeq: 0,
		Snapshot: contract.FiveLayerSnapshot{
			Connection: contract.ConnectionSnapshot{State: contract.AttachedConnectionState},
			Auth:       contract.AuthSnapshot{State: contract.AttachedAuthState},
			Session:    contract.SessionSnapshot{State: contract.SessionStateRunning},
			Control:    contract.ControlSnapshot{State: contract.ControlStateNone},
			History:    contract.HistorySnapshot{State: contract.HistoryStateContinuous},
		},
	}
	if err := conn.writeServerEventSync(attached); err != nil {
		t.Fatalf("write attached: %v", err)
	}

	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	want := []string{contract.ServerEventTypeSessionAttached, contract.ServerEventTypeControlState}
	for i, expected := range want {
		_, payload, err := clientConn.ReadMessage()
		if err != nil {
			t.Fatalf("read event %d: %v", i, err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}
		if envelope.Type != expected {
			t.Fatalf("attach order[%d] = %q, want %q", i, envelope.Type, expected)
		}
	}
	conn.requestTeardown()
}

// TestR4_003_DeterministicMergePriority executes the production response,
// control-projector outbound, and causal-delivery seams against one paused outbound
// writer. Producers deliberately enqueue in reverse priority order. Once the
// writer is released, the documented order must be:
//
//	control > error > backfill > causal
//
// FIFO is preserved within each class. This covers a control transition arriving
// while a backfill result and live output are already pending.
func TestR4_003_DeterministicMergePriority(t *testing.T) {
	rt, adapter, streams := buildAdapterForTest(t, nil, nil)
	sid := startSessionDirect(t, rt.Arbiter())
	principal := newTestDevicePrincipal("devA", "Device A")
	lease, _ := rt.Directory().Attach(principal.DeviceID, principal.DeviceName, "connA", sid)

	serverConn, clientConn := dialWSV1Pipe(t)
	writerGate := make(chan struct{})
	var releaseWriter sync.Once
	release := func() { releaseWriter.Do(func() { close(writerGate) }) }
	t.Cleanup(release)
	conn := &wsV1Connection{
		conn:               serverConn,
		adapter:            adapter,
		state:              wsV1StateAttached,
		lease:              lease,
		sessionID:          sid,
		inputIDs:           make(map[contract.MessageID]struct{}),
		done:               make(chan struct{}),
		outboundWriterGate: writerGate,
	}
	conn.ensureOutboundWriter()
	t.Cleanup(func() {
		conn.requestTeardown()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	controlSub := rt.Hub().Subscribe(sid, principal.DeviceID, lease, conn)
	controlSub.SetAuthorityFencer(wsQueueFullFencer{conn: conn})
	handle := &ControlAttachmentHandle{
		sessionID: sid, deviceID: principal.DeviceID, lease: lease, subscriber: controlSub,
	}
	causalSub := rt.Hub().RegisterCausalSubscription(sid, 0, lease, wsQueueFullFencer{conn: conn})
	conn.mu.Lock()
	conn.handle = handle
	conn.causalSub = causalSub
	conn.mu.Unlock()
	go conn.deliveryLoop()
	t.Cleanup(func() {
		causalSub.BeginTerminal()
		rt.Hub().Unsubscribe(controlSub)
	})

	// Reverse-priority producer order: backfill, causal, error, control.
	seq := streams.AppendOutput(sid, []byte("retained"))
	conn.handleBackfill(contract.BackfillFrame{
		Type: contract.ClientFrameTypeBackfill, RequestID: "bf-1",
		FromSeq: seq, ToSeq: seq,
	})
	causal := contract.OutputEvent{
		Type: contract.ServerEventTypeOutput, SessionID: sid,
		Seq: seq + 1, Chunk: "live",
	}
	if !causalSub.enqueue(causal, 1) {
		t.Fatal("enqueue causal event")
	}
	conn.sendWSError("err-1", sid, contract.ErrorCodeServiceDown,
		contract.ErrorLayerConnection, "unavailable", contract.ActionHintCheckDesktop)
	// Production control path: projector → per-attachment FIFO → deliveryLoop.
	rt.Arbiter().TakeDesktop(rt.DesktopAuthority(), sid)

	waitR4003OutboundPending(t, conn, 4)
	release()

	want := []string{
		contract.ServerEventTypeControlState,
		contract.ServerEventTypeError,
		contract.ServerEventTypeBackfillResult,
		contract.ServerEventTypeOutput,
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i, expected := range want {
		msgType, payload, err := clientConn.ReadMessage()
		if err != nil {
			t.Fatalf("read event %d: %v", i, err)
		}
		if msgType != websocket.TextMessage {
			t.Fatalf("event %d message type = %d, want text", i, msgType)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}
		if envelope.Type != expected {
			t.Fatalf("event order[%d] = %q, want %q; payload=%s", i, envelope.Type, expected, payload)
		}
	}
}

// TestR4_003_AttachInstantOverflowFencesAndFullyTearsDown exercises the real
// handleAttach/readLoop path. The barrier fires after the fencer is installed
// but before session.attached is admitted. The capacity-one final outbound queue
// is then overflowed by control transitions. The connection must close, the read
// loop must perform exact DetachControl cleanup, and no attached frame may escape
// after the fence.
func TestR4_003_AttachInstantOverflowFencesAndFullyTearsDown(t *testing.T) {
	rt, adapter, _ := buildAdapterForTest(t, nil, nil)
	sid := startSessionDirect(t, rt.Arbiter())
	serverConn, clientConn := dialWSV1Pipe(t)
	defer clientConn.Close()

	principal := newTestDevicePrincipal("devA", "Device A")
	var closeCalls atomic.Int32
	var closeOnce sync.Once
	var hookCalls atomic.Int32
	actor := &wsV1Connection{
		server:       &Server{},
		conn:         serverConn,
		principal:    principal,
		connectionID: "connA",
		adapter:      adapter,
		state:        wsV1StateRegisteredAwaitAttach,
		inputIDs:     make(map[contract.MessageID]struct{}),
		done:         make(chan struct{}),
	}
	actor.closeFn = func() error {
		closeCalls.Add(1)
		var err error
		closeOnce.Do(func() { err = serverConn.Close() })
		return err
	}
	actor.beforeAttachedWrite = func() {
		hookCalls.Add(1)
		actor.mu.Lock()
		handle := actor.handle
		actor.mu.Unlock()
		if handle == nil || handle.subscriber == nil {
			t.Error("attach barrier reached without control subscriber")
			return
		}
		actor.outbound.mu.Lock()
		actor.outbound.capacity = 1
		actor.outbound.mu.Unlock()
		// none→you fills the unique queue; you→desktop overflows while the
		// bootstrap gate still prevents either event from preempting attached.
		rt.Arbiter().Acquire(principal, handle.Lease(), sid)
		rt.Arbiter().TakeDesktop(rt.DesktopAuthority(), sid)
	}
	actor.ensureOutboundWriter()
	go actor.readLoop()

	attach, err := json.Marshal(contract.AttachFrame{
		Type: contract.ClientFrameTypeAttach, RequestID: "attach-1",
		APIVersion: contract.APIVersionV1, SessionID: sid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := clientConn.WriteMessage(websocket.TextMessage, attach); err != nil {
		t.Fatalf("write attach: %v", err)
	}

	select {
	case <-actor.done:
	case <-time.After(2 * time.Second):
		t.Fatal("attach-window overflow did not close transport and finish read-loop teardown")
	}
	if hookCalls.Load() != 1 {
		t.Fatalf("attach barrier calls = %d, want 1", hookCalls.Load())
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("transport close calls = %d, want exactly 1", closeCalls.Load())
	}
	actor.mu.Lock()
	handle := actor.handle
	actor.mu.Unlock()
	if handle == nil || handle.Lease().IsLive() || !handle.subscriber.IsFenced() {
		t.Fatal("attach-window overflow must leave subscriber and exact lease fenced")
	}
	if got := rt.Directory().CurrentLease(principal.DeviceID, sid); got != nil {
		t.Fatalf("read-loop teardown left directory lease behind: %+v", got)
	}

	_ = clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	for {
		msgType, payload, readErr := clientConn.ReadMessage()
		if readErr != nil {
			break
		}
		if msgType == websocket.TextMessage {
			var envelope struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(payload, &envelope)
			if envelope.Type == contract.ServerEventTypeSessionAttached {
				t.Fatalf("session.attached escaped after attach-window fence: %s", payload)
			}
		}
	}
}

// TestR4_003_FenceDropsPendingBackfill proves the outbound fence is a delivery
// barrier, not only an authority bit. A backfill result queued before the fence
// must be discarded and must never reach the socket after the writer resumes.
func TestR4_003_FenceDropsPendingBackfill(t *testing.T) {
	rt, adapter, streams := buildAdapterForTest(t, nil, nil)
	sid := startSessionDirect(t, rt.Arbiter())
	principal := newTestDevicePrincipal("devA", "Device A")
	lease, _ := rt.Directory().Attach(principal.DeviceID, principal.DeviceName, "connA", sid)
	serverConn, clientConn := dialWSV1Pipe(t)
	defer serverConn.Close()
	defer clientConn.Close()

	writerGate := make(chan struct{})
	var releaseWriter sync.Once
	release := func() { releaseWriter.Do(func() { close(writerGate) }) }
	t.Cleanup(release)
	closeCalled := make(chan struct{}, 1)
	conn := &wsV1Connection{
		conn:               serverConn,
		adapter:            adapter,
		state:              wsV1StateAttached,
		lease:              lease,
		sessionID:          sid,
		inputIDs:           make(map[contract.MessageID]struct{}),
		done:               make(chan struct{}),
		outboundWriterGate: writerGate,
		closeFn: func() error {
			select {
			case closeCalled <- struct{}{}:
			default:
			}
			return nil // keep the socket observable; teardown was still requested.
		},
	}
	conn.ensureOutboundWriter()
	seq := streams.AppendOutput(sid, []byte("must-not-escape"))
	conn.handleBackfill(contract.BackfillFrame{
		Type: contract.ClientFrameTypeBackfill, RequestID: "bf-fenced",
		FromSeq: seq, ToSeq: seq,
	})
	waitR4003OutboundPending(t, conn, 1)

	lease.fence() // synchronous authority half of the subscription fence.
	wsQueueFullFencer{conn: conn}.FenceSubscriptionWrites(SubscriptionFenceToken{lease: lease}, nil)
	select {
	case <-closeCalled:
	case <-time.After(time.Second):
		t.Fatal("fence did not request transport teardown")
	}
	release()

	_ = clientConn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if msgType, payload, err := clientConn.ReadMessage(); err == nil {
		t.Fatalf("fenced residual reached socket: type=%d payload=%s", msgType, payload)
	}
	conn.outbound.mu.Lock()
	pending, fenced := conn.outbound.pending, conn.outbound.fenced
	conn.outbound.mu.Unlock()
	if pending != 0 || !fenced {
		t.Fatalf("outbound after fence: pending=%d fenced=%v, want 0/true", pending, fenced)
	}
}

func waitR4003OutboundPending(t *testing.T, conn *wsV1Connection, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn.outbound.mu.Lock()
		got := conn.outbound.pending
		conn.outbound.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	conn.outbound.mu.Lock()
	got := conn.outbound.pending
	conn.outbound.mu.Unlock()
	t.Fatalf("outbound pending = %d, want %d", got, want)
}
