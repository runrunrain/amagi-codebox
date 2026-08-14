package remote

// ws_v1_session.go — M2-A /ws/v1 session consumer (design §6).
//
// Single WS upgrade path (contract.WebSocketV1Path). The consumer implements:
//   - upgrade handshake (path + empty query → Host → Origin → Cookie auth);
//   - M1 registry Register (global ConnectionID uniqueness);
//   - connection state machine (design §6.2);
//   - attach frame → AttachControl → session.attached FiveLayer snapshot;
//   - input/resize → gate.DoDevicePTY (exact live lease);
//   - backfill → stream store range query;
//   - ping → liveness refresh (silent);
//   - close/detach → DetachControl (grace on unexpected).
//
// Design authority: §6.1-6.6, §7 (output/history/Seq), §8 (errors).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Frozen constants (design §6.1, §6.5)
// ---------------------------------------------------------------------------

const (
	// wsV1AttachDeadline is the max time to wait for the first attach frame.
	wsV1AttachDeadline = 10 * time.Second
	// wsV1ReadDeadline is the per-read deadline (refreshed by pong/ping).
	wsV1ReadDeadline = 60 * time.Second
	// wsV1WriteDeadline is the per-write deadline.
	wsV1WriteDeadline = 10 * time.Second
	// wsV1ClientFrameMax is the max client text frame size (64 KiB).
	wsV1ClientFrameMax = 64 << 10
	// wsV1InputIDSetMax is the max input idempotency IDs per connection (8192).
	wsV1InputIDSetMax = 8192
	// wsV1InputIDBytesMax is the max total bytes for input IDs (1 MiB).
	wsV1InputIDBytesMax = 1 << 20
	// wsV1BackfillMaxFrames is the max frames per backfill request (256).
	wsV1BackfillMaxFrames = 256
	// wsV1BackfillMaxBytes is the max encoded estimate per backfill (1 MiB).
	wsV1BackfillMaxBytes = 1 << 20
)

// v1Upgrader is the WS upgrader for /ws/v1. CheckOrigin rejects all (the
// handler does its own Origin enforcement before upgrade).
var v1Upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
	Subprotocols:    []string{},
}

// ---------------------------------------------------------------------------
// wsV1Connection — the connection actor (design §6.1, §6.2)
// ---------------------------------------------------------------------------

// wsV1ConnectionState is the connection lifecycle state (design §6.2).
type wsV1ConnectionState uint8

const (
	wsV1StateUpgradedUnregistered wsV1ConnectionState = iota + 1
	wsV1StateRegisteredAwaitAttach
	wsV1StateAttached
	wsV1StateTerminating
	wsV1StateClosed
)

func (s wsV1ConnectionState) String() string {
	switch s {
	case wsV1StateUpgradedUnregistered:
		return "upgraded_unregistered"
	case wsV1StateRegisteredAwaitAttach:
		return "registered_await_attach"
	case wsV1StateAttached:
		return "attached"
	case wsV1StateTerminating:
		return "terminating"
	case wsV1StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// Unique outbound queue (R4-003)
// ---------------------------------------------------------------------------

const (
	// One connection has one bounded final queue. Upstream control/H3 buffers are
	// ingress reservations only; every frame crosses this queue before the sole
	// goroutine that owns websocket.WriteMessage.
	wsV1OutboundCapacity = 256
	wsV1OutboundBytesMax = 2 << 20
)

type wsOutboundPriority uint8

const (
	// Bootstrap is absolute-first so session.attached cannot be preempted. Among
	// post-attach events the deterministic merge is authority first, then
	// correlated errors, requested history, and finally live causal traffic:
	// control > error > backfill > causal. FIFO is retained within each class.
	wsOutboundAttached wsOutboundPriority = iota
	wsOutboundControl
	wsOutboundError
	// wsOutboundInputAck is the CG-03 input.ack confirmation: a correlated response
	// to a client request (after error, before backfill/history). FIFO within class.
	wsOutboundInputAck
	wsOutboundBackfill
	wsOutboundCausal
	wsOutboundPriorityCount
)

var (
	errWSOutboundFull       = errors.New("ws outbound queue full")
	errWSOutboundFenced     = errors.New("ws outbound queue fenced")
	errWSOutboundTerminated = errors.New("ws outbound queue terminated")
	errWSSocketUnavailable  = errors.New("ws socket unavailable")
)

type wsOutboundFrame struct {
	priority   wsOutboundPriority
	payload    []byte
	completion chan error
	bootstrap  *attachmentBootstrap
}

type wsOutboundTerminal struct {
	preClosePayloads [][]byte
	removal          *PreparedRemovalTerminal
	closeCode        int
	completion       chan error
}

type wsOutboundTake struct {
	frame      *wsOutboundFrame
	terminal   *wsOutboundTerminal
	generation uint64
	stopped    bool
}

// wsOutboundQueue is one logical bounded queue with priority lanes. The lanes
// are an implementation detail of deterministic dequeue, not independent
// writers or admission domains: capacity/byte budget/fence/terminal state are
// global, and exactly one outbound writer calls take.
type wsOutboundQueue struct {
	mu sync.Mutex

	lanes [wsOutboundPriorityCount][]wsOutboundFrame

	pending          int
	pendingBytes     int
	capacity         int
	maxBytes         int
	generation       uint64
	bootstrapPending bool
	fenced           bool
	closed           bool
	terminal         *wsOutboundTerminal
	notify           chan struct{}
}

func newWSOutboundQueue(bootstrapPending bool) *wsOutboundQueue {
	return &wsOutboundQueue{
		capacity:         wsV1OutboundCapacity,
		maxBytes:         wsV1OutboundBytesMax,
		bootstrapPending: bootstrapPending,
		notify:           make(chan struct{}, 1),
	}
}

func (q *wsOutboundQueue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func completeWSOutbound(ch chan error, err error) {
	if ch == nil {
		return
	}
	ch <- err
	close(ch)
}

func (q *wsOutboundQueue) enqueue(priority wsOutboundPriority, payload []byte, wait bool) (<-chan error, error) {
	return q.enqueueWithBootstrap(priority, payload, wait, nil)
}

func (q *wsOutboundQueue) enqueueWithBootstrap(priority wsOutboundPriority, payload []byte, wait bool, bootstrap *attachmentBootstrap) (<-chan error, error) {
	var completion chan error
	if wait {
		completion = make(chan error, 1)
	}
	q.mu.Lock()
	switch {
	case q.fenced:
		q.mu.Unlock()
		completeWSOutbound(completion, errWSOutboundFenced)
		return completion, errWSOutboundFenced
	case q.closed || q.terminal != nil:
		q.mu.Unlock()
		completeWSOutbound(completion, errWSOutboundTerminated)
		return completion, errWSOutboundTerminated
	case q.pending >= q.capacity || q.pendingBytes+len(payload) > q.maxBytes:
		q.fenceLocked(errWSOutboundFull)
		q.mu.Unlock()
		completeWSOutbound(completion, errWSOutboundFull)
		q.signal()
		return completion, errWSOutboundFull
	}
	q.lanes[priority] = append(q.lanes[priority], wsOutboundFrame{
		priority: priority, payload: payload, completion: completion, bootstrap: bootstrap,
	})
	q.pending++
	q.pendingBytes += len(payload)
	q.mu.Unlock()
	q.signal()
	return completion, nil
}

func (q *wsOutboundQueue) enqueueTerminal(preClosePayload []byte, closeCode int) (<-chan error, error) {
	completion := make(chan error, 1)
	q.mu.Lock()
	if q.fenced {
		q.mu.Unlock()
		completeWSOutbound(completion, errWSOutboundFenced)
		return completion, errWSOutboundFenced
	}
	if q.closed || q.terminal != nil {
		q.mu.Unlock()
		completeWSOutbound(completion, errWSOutboundTerminated)
		return completion, errWSOutboundTerminated
	}
	// Terminal is a barrier: invalidate a frame already taken but not yet written
	// and discard every residual. Its optional typed pre-close event is carried
	// in the same item, so event→close cannot be split by another producer.
	q.generation++
	q.dropPendingLocked(errWSOutboundTerminated)
	var payloads [][]byte
	if len(preClosePayload) > 0 {
		payloads = [][]byte{preClosePayload}
	}
	q.terminal = &wsOutboundTerminal{
		preClosePayloads: payloads,
		closeCode:        closeCode,
		completion:       completion,
	}
	q.mu.Unlock()
	q.signal()
	return completion, nil
}

func (q *wsOutboundQueue) enqueuePreparedRemovalTerminal(item *PreparedRemovalTerminal) (<-chan error, error) {
	if item == nil || len(item.basePayloads) == 0 || item.closeCode != removalNormalCloseCode {
		return nil, errWSOutboundTerminated
	}
	completion := make(chan error, 1)
	q.mu.Lock()
	if q.fenced {
		q.mu.Unlock()
		completeWSOutbound(completion, errWSOutboundFenced)
		return completion, errWSOutboundFenced
	}
	if q.closed || q.terminal != nil {
		q.mu.Unlock()
		completeWSOutbound(completion, errWSOutboundTerminated)
		return completion, errWSOutboundTerminated
	}
	q.generation++
	q.dropPendingLocked(errWSOutboundTerminated)
	q.terminal = &wsOutboundTerminal{removal: item, closeCode: item.closeCode, completion: completion}
	q.mu.Unlock()
	q.signal()
	return completion, nil
}

func (q *wsOutboundQueue) take() wsOutboundTake {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.terminal != nil {
		terminal := q.terminal
		q.terminal = nil
		q.closed = true
		return wsOutboundTake{terminal: terminal, generation: q.generation}
	}
	if q.fenced || q.closed {
		return wsOutboundTake{stopped: true}
	}
	// During a successful attach, control transitions may be admitted directly
	// into this queue before history assembly completes. Hold all post-attach
	// classes until session.attached itself has been written by the sole writer.
	// A correlated error for a protocol-violating pre-attach frame may still be
	// returned; the read loop is serial, so such an error cannot race a valid
	// handleAttach assembly.
	if q.bootstrapPending {
		if take := q.takePriorityLocked(wsOutboundAttached); take.frame != nil {
			return take
		}
		return q.takePriorityLocked(wsOutboundError)
	}
	for priority := wsOutboundAttached; priority < wsOutboundPriorityCount; priority++ {
		if take := q.takePriorityLocked(priority); take.frame != nil {
			return take
		}
	}
	return wsOutboundTake{}
}

func (q *wsOutboundQueue) takePriorityLocked(priority wsOutboundPriority) wsOutboundTake {
	lane := q.lanes[priority]
	if len(lane) == 0 {
		return wsOutboundTake{}
	}
	frame := lane[0]
	if len(lane) == 1 {
		q.lanes[priority] = nil // release the last payload backing reference.
	} else {
		q.lanes[priority] = lane[1:]
	}
	q.pending--
	q.pendingBytes -= len(frame.payload)
	return wsOutboundTake{frame: &frame, generation: q.generation}
}

func (q *wsOutboundQueue) finishBootstrap() {
	q.mu.Lock()
	q.bootstrapPending = false
	q.mu.Unlock()
	q.signal()
}

// mayWrite establishes the queue-side write linearization point. A fence or
// terminal that won before this check invalidates the taken frame. A socket
// syscall already begun before a later fence is logically before that fence;
// the fencer never waits on transport I/O (N-004 remains intact).
func (q *wsOutboundQueue) mayWrite(generation uint64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return !q.fenced && !q.closed && q.terminal == nil && q.generation == generation
}

func (q *wsOutboundQueue) fence(err error) {
	q.mu.Lock()
	if !q.fenced && !q.closed {
		q.fenceLocked(err)
	}
	q.mu.Unlock()
	q.signal()
}

func (q *wsOutboundQueue) fenceLocked(err error) {
	q.generation++
	q.fenced = true
	q.dropPendingLocked(err)
}

func (q *wsOutboundQueue) dropPendingLocked(err error) {
	for priority := wsOutboundAttached; priority < wsOutboundPriorityCount; priority++ {
		for _, frame := range q.lanes[priority] {
			completeWSOutbound(frame.completion, err)
		}
		q.lanes[priority] = nil
	}
	q.pending = 0
	q.pendingBytes = 0
}

func (q *wsOutboundQueue) isFenced() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.fenced
}

// wsV1Connection is the per-connection actor. It owns:
//   - the WS conn (read loop + socket writer);
//   - the M1 registration (global ConnectionID uniqueness);
//   - the attach handle (lease + hub subscriber);
//   - the input idempotency set;
//   - the connection state machine.
type wsV1Connection struct {
	server       *Server
	conn         *websocket.Conn
	principal    DevicePrincipal
	connectionID ConnectionID
	adapter      *RemoteSessionAdapter
	// registration is the retained M1 registry handle (N-003). Kept so the
	// actor can Unregister on normal disconnect/write fault; epoch-guarded so a
	// stale unregister (after FenceDevice/Stop already detached, or superseded
	// by a newer-epoch entry) is an idempotent no-op. Zero value (epoch 0) until
	// a successful Register.
	registration ConnectionRegistration
	// closeFn overrides the transport close for whitebox tests (N-004 seam): a
	// test can inject a blocking close to prove requestTeardown is non-blocking.
	// Nil in production → c.conn.Close().
	closeFn func() error

	mu        sync.Mutex
	state     wsV1ConnectionState
	handle    *ControlAttachmentHandle
	lease     *ControlConnectionLease
	sessionID contract.SessionID

	// R4-003: every server text event and close frame enters outbound; only the
	// outbound writer goroutine may touch websocket.WriteMessage.
	outboundOnce       sync.Once
	outbound           *wsOutboundQueue
	outboundWriterDone chan struct{}
	// Test-only deterministic barriers. Nil in production.
	outboundWriterGate  <-chan struct{}
	beforeAttachedWrite func()

	// H3 causal subscription (design §6.3 attach). Drained by deliveryLoop;
	// events with ordinal ≤ startAfter are skipped (snapshot absorbed them).
	causalSub *causalHubSubscription

	// Input idempotency set (design §6.5 input dedupe).
	inputIDs     map[contract.MessageID]struct{}
	inputIDBytes int

	// writeFault performs a one-time connection teardown when a socket write
	// fails (m-002). The sole writer never blocks on its own teardown: it closes
	// the transport so the read loop observes EOF and runs the authoritative
	// detach/fence/unregister path (handleDisconnect).
	writeFault sync.Once

	// Lifecycle.
	done chan struct{}
}

// ---------------------------------------------------------------------------
// handleV1WebSocket — the upgrade entry point
// ---------------------------------------------------------------------------

// handleV1WebSocket handles the /ws/v1 upgrade (design §6.1).
func (s *Server) handleV1WebSocket(w http.ResponseWriter, r *http.Request) {
	adapter := s.sessionAdapter
	if adapter == nil {
		writeV1Error(w, "", http.StatusNotFound, contract.ErrorCodeBadRequest,
			contract.ErrorLayerConnection, "remote endpoint not available", contract.ActionHintRetry)
		return
	}

	// 1. Exact path + empty query (design §6.1).
	if r.URL.Path != contract.WebSocketV1Path {
		writeV1Error(w, "", http.StatusNotFound, contract.ErrorCodeBadRequest,
			contract.ErrorLayerConnection, "remote endpoint not available", contract.ActionHintRetry)
		return
	}
	if r.URL.RawQuery != "" {
		writeV1Error(w, "", http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerConnection, "query parameters are not allowed", contract.ActionHintRetry)
		return
	}

	// 2. Host gate (dynamic port).
	if !strictHostValid(r, s.GetPort()) {
		writeV1Error(w, "", http.StatusForbidden, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "request host rejected", contract.ActionHintCheckDesktop)
		return
	}

	// 3. Non-empty allowlisted Origin.
	originVals := r.Header.Values("Origin")
	if len(originVals) != 1 {
		writeV1Error(w, "", http.StatusForbidden, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "request origin rejected", contract.ActionHintCheckDesktop)
		return
	}
	origin := strings.TrimSpace(originVals[0])
	if origin == "" || !canonicalAllowedOrigin(r, origin) {
		writeV1Error(w, "", http.StatusForbidden, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "request origin rejected", contract.ActionHintCheckDesktop)
		return
	}

	// 4. M1 Cookie auth (design §6.1).
	if s.v1sec == nil {
		writeV1Error(w, "", http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "security state unavailable", contract.ActionHintCheckDesktop)
		return
	}
	principal, fail := s.v1sec.deviceAuth.AuthenticateRequest(r)
	if fail != 0 {
		// Map auth failure to HTTP status (same as REST).
		writeWSAuthFail(w, fail)
		return
	}

	// 5. Upgrade.
	conn, err := v1Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // upgrade failed; response already written by upgrader.
	}

	// 6. Mint ConnectionID (128-bit random).
	connIDBytes := make([]byte, 16)
	if _, err := cryptoRandRead(connIDBytes); err != nil {
		_ = conn.Close()
		return
	}
	connectionID := ConnectionID(rawURLBase64(connIDBytes))

	actor := &wsV1Connection{
		server:       s,
		conn:         conn,
		principal:    principal,
		connectionID: connectionID,
		adapter:      adapter,
		state:        wsV1StateUpgradedUnregistered,
		inputIDs:     make(map[contract.MessageID]struct{}),
		done:         make(chan struct{}),
	}
	actor.ensureOutboundWriter()

	// 7. M1 registry Register (design §6.1: before any business frame).
	regResult, regErr := s.RegisterV1Connection(principal, connectionID, actor)
	if regErr != nil || regResult.Outcome != RegistrationAccepted {
		// Registry rejected: send terminal close + cleanup.
		actor.terminate(wsV1Terminal{cause: "registration_rejected", code: websocket.ClosePolicyViolation})
		return
	}
	// N-003: retain the registration handle so normal disconnect/write fault can
	// Unregister from the M1 registry (epoch-guarded, idempotent).
	actor.registration = regResult.Registration

	// 8. RecordDeviceSeen (design §6.1: after Register success).
	if _, seenErr := s.v1sec.pairing.RecordDeviceSeen(principal); seenErr != nil {
		// Indeterminate: fail closed (stop the connection). readLoop never runs on
		// this path, so explicitly Unregister the retained handle (N-003: every
		// post-registration terminal path must release the registry entry).
		s.unregisterV1Connection(actor.registration)
		actor.terminate(wsV1Terminal{cause: "security_check_failed", code: websocket.ClosePolicyViolation})
		return
	}

	actor.setState(wsV1StateRegisteredAwaitAttach)

	// 9. Read loop (blocks until connection closes).
	actor.readLoop()
}

// ---------------------------------------------------------------------------
// wsV1Connection — ControlEventConsumer implementation
// ---------------------------------------------------------------------------

var _ controlOutboundQueueConsumer = (*wsV1Connection)(nil)
var _ RemovalTerminalPort = (*wsV1Connection)(nil)

// DeliverControlState implements the legacy ControlEventConsumer writer seam.
// Production control admission uses enqueueControlStateOutbound below; an
// explicitly started legacy subscriber writer still reaches the same queue.
func (c *wsV1Connection) DeliverControlState(event contract.ControlStateEvent) {
	c.writeServerEvent(event)
}

// enqueueControlStateOutbound implements the lock-safe control hub fast path.
// It performs bounded in-memory admission only: no socket I/O, teardown, or hub/
// arbiter callback is allowed here because stateMu → hub.mu may be held. A false
// result makes the hub fence the exact lease and invoke the installed fencer.
func (c *wsV1Connection) enqueueControlStateOutbound(event contract.ControlStateEvent) bool {
	payload, err := contract.MarshalServerEvent(event)
	if err != nil {
		return false
	}
	if c.outbound == nil {
		return false
	}
	_, err = c.outbound.enqueue(wsOutboundControl, payload, false)
	return err == nil
}

// ConsumerAlive implements ControlEventConsumer. The await-attach state is live:
// AttachControl has subscribed but intentionally has not started delivery yet,
// so transitions may accumulate behind session.attached. A fenced final queue is
// never considered alive.
func (c *wsV1Connection) ConsumerAlive() bool {
	c.mu.Lock()
	state := c.state
	done := c.done
	c.mu.Unlock()
	if state != wsV1StateRegisteredAwaitAttach && state != wsV1StateAttached {
		return false
	}
	if done != nil && isClosedChan(done) {
		return false
	}
	c.ensureOutboundWriter()
	return !c.outbound.isFenced()
}

// Terminate implements ManagedV1Connection (design §6.6). It consumes the
// termination cause to build the correct CG-01 close sequence: a device revoke
// sends auth.revoked{reason:device_revoked} via the sole writer and THEN closes
// 1008. Other causes close without a typed auth event (they are not device
// revokes; CG-01 only freezes device_revoked for v1).
func (c *wsV1Connection) Terminate(t ConnectionTermination) {
	code := websocket.ClosePolicyViolation
	var preClose contract.KnownServerEvent
	switch t.Cause {
	case TerminationDeviceRevoked:
		code = contract.AuthRevokedCloseCode
		occurredAt := t.OccurredAt
		if occurredAt.IsZero() {
			occurredAt = time.Now()
		}
		preClose = contract.AuthRevokedEvent{
			Type:       contract.ServerEventTypeAuthRevoked,
			Reason:     contract.AuthRevokedReasonDeviceRevoked,
			OccurredAt: occurredAt.UTC().Format(time.RFC3339Nano),
		}
	}
	c.terminate(wsV1Terminal{cause: "terminated", code: code, preCloseEvent: preClose})
}

// AdmitRemovalTerminal installs the prepared removed payload list as one queue
// barrier. Cleanup runs only after the sole writer has attempted removed→1000.
func (c *wsV1Connection) AdmitRemovalTerminal(item *PreparedRemovalTerminal) bool {
	if item == nil {
		return false
	}
	c.mu.Lock()
	if c.state == wsV1StateClosed || c.state == wsV1StateTerminating {
		c.mu.Unlock()
		return false
	}
	c.state = wsV1StateClosed
	handle := c.handle
	sub := c.causalSub
	c.causalSub = nil
	adapter := c.adapter
	server := c.server
	c.mu.Unlock()

	c.ensureOutboundWriter()
	completion, err := c.outbound.enqueuePreparedRemovalTerminal(item)
	if err != nil {
		c.requestTeardown()
		c.cleanupCausalSubscription(sub)
		if handle != nil && adapter != nil && adapter.Runtime() != nil {
			adapter.Runtime().DetachControl(handle, false)
		}
		if server != nil {
			server.unregisterV1Connection(c.registration)
		}
		return false
	}
	go func() {
		writeErr := <-completion
		c.cleanupCausalSubscription(sub)
		if handle != nil && adapter != nil && adapter.Runtime() != nil {
			adapter.Runtime().DetachControl(handle, false)
		}
		if server != nil {
			server.unregisterV1Connection(c.registration)
		}
		if writeErr != nil {
			c.requestTeardown()
		}
	}()
	return true
}

// ---------------------------------------------------------------------------
// Read loop (design §6.2, §6.5)
// ---------------------------------------------------------------------------

func (c *wsV1Connection) readLoop() {
	defer close(c.done)
	c.conn.SetReadLimit(wsV1ClientFrameMax)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsV1AttachDeadline))

	for {
		msgType, data, err := c.conn.ReadMessage()
		if err != nil {
			// Transport error / EOF / close.
			c.handleDisconnect(true)
			return
		}
		if msgType == websocket.BinaryMessage {
			// Binary frame is protocol error (design §6.2).
			c.sendErrorAndClose("", contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
				"binary frames are not allowed", contract.ActionHintRetry, websocket.CloseProtocolError)
			return
		}
		// Refresh read deadline on any message.
		_ = c.conn.SetReadDeadline(time.Now().Add(wsV1ReadDeadline))

		// Decode frame.
		frame, derr := contract.DecodeClientFrame(data)
		if derr != nil {
			// Unknown/invalid frame type → bad_request error + close.
			c.sendErrorAndClose("", contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
				"invalid client frame", contract.ActionHintRetry, websocket.CloseProtocolError)
			return
		}

		switch f := frame.(type) {
		case contract.AttachFrame:
			c.handleAttach(f)
		case contract.InputFrame:
			c.handleInput(f)
		case contract.ResizeFrame:
			c.handleResize(f)
		case contract.BackfillFrame:
			c.handleBackfill(f)
		case contract.PingFrame:
			// Ping: silent liveness refresh (design §6.5).
		}
	}
}

// ---------------------------------------------------------------------------
// Attach handling (design §6.3)
// ---------------------------------------------------------------------------

func (c *wsV1Connection) handleAttach(frame contract.AttachFrame) {
	c.ensureOutboundWriter()
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()

	if state != wsV1StateRegisteredAwaitAttach {
		// Duplicate attach or wrong state → bad_request + close.
		c.sendErrorAndClose(frame.RequestID, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			"attach is only valid as the first frame", contract.ActionHintRetry, websocket.CloseProtocolError)
		return
	}

	sessionID := frame.SessionID

	runtime := c.adapter.Runtime()
	if runtime == nil {
		c.sendErrorAndClose(frame.RequestID, contract.ErrorCodeServiceDown, contract.ErrorLayerConnection,
			"control service unavailable", contract.ActionHintCheckDesktop, websocket.CloseInternalServerErr)
		return
	}
	// Resolve the stable Authority entry before stream/attachment locks. The
	// final pass uses only this opaque token and never re-enters Manager.indexMu.
	membership, membershipErr := c.adapter.ResolveRemoteHandle(sessionID)
	if membershipErr != nil {
		c.terminate(wsV1Terminal{
			cause: "attach_not_found", code: websocket.CloseNormalClosure,
			preCloseEvent: c.newWSError(frame.RequestID, sessionID, contract.ErrorCodeSessionNotFound,
				contract.ErrorLayerSession, "session not found", contract.ActionHintRetry),
		})
		return
	}

	// M3-B timing (design §6): Start after strict-decoded attach, before the
	// causal cut. Omitted cursor = TimingAttach (first attach); present cursor
	// (incl. 0) = TimingResync. Observe only after the attached frame is
	// validated+marshaled and the first packet write succeeds.
	var timing *Timer
	if c.server.metrics != nil {
		kind := TimingAttach
		if frame.LastSeq != nil {
			kind = TimingResync
		}
		timing, _ = c.server.metrics.Start(kind)
	}

	// M-007 staging attach: the causal cut (SyncFeed convergence + watermark)
	// MUST succeed BEFORE any lease is committed. A causal failure returns
	// service.down with ZERO lease replacement (AttachControl is not called, so
	// any pre-existing lease for this device+session is untouched). The causal
	// cut touches only session-scoped state (feed/streams/hub), never the lease.
	_, watermark, _, cerr := causalCut(sessionID, runtime.Feed(), c.adapter.Streams(), runtime.Hub())
	if cerr != nil {
		c.terminate(wsV1Terminal{
			cause: "causal_attach_failed", code: websocket.CloseInternalServerErr,
			preCloseEvent: c.newWSError(frame.RequestID, sessionID, contract.ErrorCodeServiceDown,
				contract.ErrorLayerConnection, "control service unavailable", contract.ActionHintCheckDesktop),
		})
		return
	}

	// Allocate the complete hidden node before entering the membership guard.
	prepared, gErr := runtime.PrepareRemoteAttach(c.principal, c.connectionID, sessionID, c, watermark, wsQueueFullFencer{conn: c})
	if gErr != nil {
		c.terminate(wsV1Terminal{
			cause: "attach_failed", code: websocket.CloseNormalClosure,
			preCloseEvent: c.newWSGateError(frame.RequestID, sessionID, gErr),
		})
		return
	}
	committed := false
	defer func() {
		if !committed {
			runtime.AbortPreparedRemoteAttach(prepared)
		}
	}()

	// The fatal-guarded final block performs only state/slot locks and stable
	// pointer/live-bit stores. It does not allocate, launch goroutines, marshal,
	// or discover interfaces.
	var snap contract.ControlSnapshot
	var fencedOld *ControlConnectionLease
	commitErr := c.adapter.CommitResolvedAttach(membership, func() {
		snap, fencedOld, gErr = runtime.CommitPreparedRemoteAttachNoAlloc(prepared)
	})
	if commitErr != nil {
		c.terminate(wsV1Terminal{
			cause: "attach_not_found", code: websocket.CloseNormalClosure,
			preCloseEvent: c.newWSError(frame.RequestID, sessionID, contract.ErrorCodeSessionNotFound,
				contract.ErrorLayerSession, "session not found", contract.ActionHintRetry),
		})
		return
	}
	if gErr != nil {
		c.terminate(wsV1Terminal{
			cause: "attach_failed", code: websocket.CloseNormalClosure,
			preCloseEvent: c.newWSGateError(frame.RequestID, sessionID, gErr),
		})
		return
	}
	committed = true
	runtime.FinishPreparedRemoteAttach(prepared)
	handle := prepared.Handle()
	bootstrapResolved := false
	defer func() {
		if !bootstrapResolved {
			prepared.Bootstrap().ResolveAbsent()
		}
	}()
	// fencedOld is the previous (deviceID, sessionID) lease that this attach
	// atomically fenced (replaced) inside runtime.CommitPreparedRemoteAttachNoAlloc.
	// FinishPreparedRemoteAttach (called above) already performed the post-commit
	// supersession fencing of that old lease's authority, so the returned pointer
	// carries no further action for this caller. The explicit assignment
	// documents the deliberate drop instead of silently ignoring a returned lease.
	_ = fencedOld
	c.mu.Lock()
	attachedLocally := c.state == wsV1StateRegisteredAwaitAttach
	if attachedLocally {
		c.handle = handle
		c.lease = handle.Lease()
		c.causalSub = prepared.CausalSubscription()
		c.sessionID = sessionID
		c.state = wsV1StateAttached
	}
	c.mu.Unlock()
	if !handle.Lease().IsLive() || c.outbound.isFenced() {
		c.requestTeardown()
		return
	}

	// Build the session.attached event with FiveLayer snapshot.
	earliest, latest := c.adapter.Streams().SeqBounds(sessionID)
	// Compute history from retained frames after lastSeq (design §7.3).
	var history []contract.ReplayFrame
	frames := c.adapter.Streams().FramesAfter(sessionID, frame.LastSeq)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, e := range frames {
		rf, _ := e.toWireFrame(sessionID, now)
		if rf != nil {
			history = append(history, rf)
		}
	}
	if history == nil {
		history = []contract.ReplayFrame{}
	}

	// Determine history state (design §7.3).
	histState := contract.HistoryStateContinuous
	var gap *contract.GapRange
	originComplete := c.adapter.Streams().OriginComplete(sessionID)
	if !originComplete && len(history) > 0 {
		// Eviction occurred: compute gap if lastSeq < earliest-1.
		if frame.LastSeq != nil && *frame.LastSeq > 0 {
			earliestFrame := earliest
			if earliestFrame > 0 && *frame.LastSeq < earliestFrame-1 {
				histState = contract.HistoryStateGap
				fromSeq := *frame.LastSeq + 1
				toSeq := earliestFrame - 1
				code := contract.ErrorCodeHistoryGap
				gap = &contract.GapRange{Code: code, FromSeq: fromSeq, ToSeq: toSeq}
			} else {
				histState = contract.HistoryStateBackfilled
			}
		} else {
			// Omitted/0 lastSeq with eviction: gap from 1 to earliest-1.
			if earliest > 1 {
				histState = contract.HistoryStateGap
				code := contract.ErrorCodeHistoryGap
				gap = &contract.GapRange{Code: code, FromSeq: 1, ToSeq: earliest - 1}
			}
		}
	} else if frame.LastSeq != nil && *frame.LastSeq > 0 && *frame.LastSeq < latest {
		histState = contract.HistoryStateBackfilled
	}

	attached := contract.SessionAttachedEvent{
		Type:        contract.ServerEventTypeSessionAttached,
		RequestID:   frame.RequestID,
		APIVersion:  contract.APIVersionV1,
		SessionID:   sessionID,
		History:     history,
		EarliestSeq: earliest,
		LatestSeq:   latest,
		Snapshot: contract.FiveLayerSnapshot{
			Connection: contract.ConnectionSnapshot{State: contract.AttachedConnectionState},
			Auth:       contract.AuthSnapshot{State: contract.AttachedAuthState},
			Session:    contract.SessionSnapshot{State: c.adapterSessionState(sessionID)},
			Control:    snap,
			History:    contract.HistorySnapshot{State: histState, Gap: gap},
		},
	}
	// CG-03: declare the input-ack capability when the per-session ledger is
	// available (contract-addendum-cg03.md §3). A new client reads this to decide
	// whether canonical input confirmation is active.
	if c.server.inputLedgers != nil {
		mode := contract.InputAckModeSessionWindowV1
		attached.InputAckMode = &mode
	}
	bootstrapPayload, marshalErr := json.Marshal(attached)
	if marshalErr != nil {
		c.terminate(wsV1Terminal{cause: "attach_encode_failed", code: websocket.CloseInternalServerErr})
		return
	}
	prepared.Bootstrap().Store(bootstrapPayload)
	bootstrapResolved = true
	if !attachedLocally {
		runtime.DetachControl(handle, false)
		prepared.CausalSubscription().BeginTerminal()
		runtime.Hub().UnregisterCausalSubscription(prepared.CausalSubscription())
		return
	}
	c.mu.Lock()
	stateAfterBootstrap := c.state
	c.mu.Unlock()
	if stateAfterBootstrap != wsV1StateAttached {
		return
	}
	// Test barrier models a transition/overflow at the last attach instant. The
	// production value is nil; the fencer is already installed before this point.
	if c.beforeAttachedWrite != nil {
		c.beforeAttachedWrite()
	}
	// M-007 + R4-003: session.attached crosses the unique queue and is confirmed
	// on the socket before source draining starts. If attach-window overflow won,
	// the fenced queue rejects this frame and requestTeardown drives read-loop
	// DetachControl cleanup; no attached/residual event can escape afterward.
	if err := c.writeBootstrapPayloadSync(bootstrapPayload, prepared.Bootstrap()); err != nil {
		c.requestTeardown()
		return
	}
	// M3-B timing: Observe only on successful first-packet write (design §6).
	if timing != nil {
		timing.Observe()
	}
	go c.deliveryLoop()
}

// adapterSessionState returns the wire session state for the attached session.
func (c *wsV1Connection) adapterSessionState(sessionID contract.SessionID) contract.SessionState {
	return c.adapter.sessionState(sessionID)
}

// deliveryLoop is the sole upstream merge actor. It never writes the socket;
// every drained event enters the connection's unique outbound queue, whose
// separate single writer owns websocket.WriteMessage (R4-003).
//
// Deterministic merge rule: drain the complete pending control FIFO before
// admitting the next causal event. The causal result branch performs a second
// control drain after select, eliminating Go select randomness when both inputs
// are ready. The final queue then applies the full documented order
// control > error > backfill > causal while preserving FIFO/ordinal order inside
// each class. A fence makes enqueue fail and the loop exits without flushing
// residual control or causal frames.
func (c *wsV1Connection) deliveryLoop() {
	c.mu.Lock()
	causalSub := c.causalSub
	handle := c.handle
	done := c.done
	c.mu.Unlock()
	if causalSub == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	type causalResult struct {
		ev queuedEvent
		ok bool
	}
	causalCh := make(chan causalResult, 1)
	launchCausal := func() {
		go func() {
			ev, ok := causalSub.Next(ctx)
			select {
			case causalCh <- causalResult{ev: ev, ok: ok}:
			case <-ctx.Done():
			}
		}()
	}
	launchCausal()

	var controlNotify <-chan struct{}
	if handle != nil {
		controlNotify = handle.ControlNotifyCh()
	}
	drainControl := func() bool {
		if handle == nil {
			return true
		}
		for _, ev := range handle.DrainPendingControl() {
			if !c.writeServerEvent(ev) {
				return false
			}
		}
		return true
	}

	for {
		if !drainControl() {
			return
		}
		select {
		case res := <-causalCh:
			// A control transition that was ready alongside causal must win even if
			// select chose causal. This second non-random drain is the merge point.
			if !drainControl() {
				return
			}
			if !res.ok {
				return
			}
			if !c.writeServerEvent(res.ev.event) {
				return
			}
			launchCausal()
		case <-controlNotify:
			// Loop to the priority drain.
		case <-done:
			cancel()
			return
		case <-ctx.Done():
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Input handling (design §6.5 input)
// ---------------------------------------------------------------------------

// errInputLedgerFull signals the per-session canonical input ledger is at
// capacity (8192 entries / 1 MiB); the handler maps it to rate.limited (design
// §5: no eviction, no new ID).
var errInputLedgerFull = errors.New("input ledger at capacity")

func (c *wsV1Connection) handleInput(frame contract.InputFrame) {
	c.mu.Lock()
	state := c.state
	lease := c.lease
	sessionID := c.sessionID
	c.mu.Unlock()

	if state != wsV1StateAttached || lease == nil {
		c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeControlForbidden, contract.ErrorLayerControl,
			"active session connection required", contract.ActionHintRequestControl)
		return
	}

	// CG-03 canonical classifier (contract-addendum-cg03.md §3/§5): canonical
	// msg-v1- IDs route to the per-session ledger + ACK; legacy non-empty opaque
	// IDs keep the per-connection dedupe + silent-success path and MUST NOT be
	// suppressed across connections.
	if contract.IsCanonicalMessageID(frame.ID) {
		c.handleCanonicalInput(frame, lease, sessionID)
		return
	}

	// Legacy path: per-connection dedupe (design §6.5: repeated id → silent drop).
	if c.isInputDuplicate(frame.ID) {
		return // silent
	}

	// Decode base64 data.
	data, err := base64.StdEncoding.DecodeString(frame.Data)
	if err != nil {
		c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeBadRequest, contract.ErrorLayerSession,
			"invalid input data", contract.ActionHintRetry)
		return
	}

	// Gate DoDevicePTY (design §6.5: exact live lease required).
	runtime := c.adapter.Runtime()
	if runtime == nil {
		c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeServiceDown, contract.ErrorLayerConnection,
			"control service unavailable", contract.ActionHintCheckDesktop)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlDataOperationTimeout)
	defer cancel()
	err = runtime.Gate().DoDevicePTY(ctx, lease, sessionID, PTYInput, func(ctx context.Context, permit *operationPermit) error {
		if err := permit.Checkpoint(ctx, 1); err != nil {
			return err
		}
		if raw := c.rawPTYPort(); raw != nil {
			return raw.WriteRaw(ctx, string(sessionID), data)
		}
		return nil
	})
	if err != nil {
		c.sendWSGateError(frame.RequestID, sessionID, err)
		return
	}
	// Success: silent (no JSON ACK, design §6.5). Input bytes are NEVER written
	// to the output stream store / replay / broadcast — only the real PTY output
	// producer may allocate a Seq and append to replay (C-001: input is a
	// one-way sink; echoing it back would leak secrets to observers/history).
	c.adapter.TouchActivity(sessionID, time.Now())
}

// handleCanonicalInput runs the CG-03 per-session ledger + ACK path for a
// canonical msg-v1- input. Authority-first: the ledger is touched only after the
// M3-A OperationLane grants the exact permit. The ledger never calls back into
// gate/hub/socket; the handler reads the Reserve status and decides raw/ACK.
func (c *wsV1Connection) handleCanonicalInput(frame contract.InputFrame, lease *ControlConnectionLease, sessionID contract.SessionID) {
	data, err := base64.StdEncoding.DecodeString(frame.Data)
	if err != nil {
		c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeBadRequest, contract.ErrorLayerSession,
			"invalid input data", contract.ActionHintRetry)
		return
	}
	runtime := c.adapter.Runtime()
	if runtime == nil {
		c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeServiceDown, contract.ErrorLayerConnection,
			"control service unavailable", contract.ActionHintCheckDesktop)
		return
	}
	device := c.principal.DeviceID
	ctx, cancel := context.WithTimeout(context.Background(), controlDataOperationTimeout)
	defer cancel()
	err = runtime.Gate().DoDevicePTY(ctx, lease, sessionID, PTYInput, func(ctx context.Context, permit *operationPermit) error {
		if err := permit.Checkpoint(ctx, 1); err != nil {
			return err
		}
		// M3-005：ledger lookup/creation 移到 exact permit checkpoint 之后（authority-first）；
		// 观察者/陈旧 lease 在 gate 通过前不会创建空 ledger（不分配资源）。
		ledger := c.server.inputLedgers.Ledger(sessionID)
		status := ledger.Reserve(device, frame.ID)
		switch status {
		case InputLedgerCommitted:
			// Already committed (duplicate across reconnect): re-ACK only, no rewrite.
			c.sendInputAck(frame.RequestID, sessionID, frame.ID)
			return nil
		case InputLedgerPending, InputLedgerIndeterminate:
			// Another attempt owns the in-flight entry, or a prior raw errored;
			// do NOT rewrite (the owner resolves; indeterminate stays unknown).
			return nil
		case InputLedgerFull:
			return errInputLedgerFull
		}
		// status == Owner: exactly-once raw write.
		raw := c.rawPTYPort()
		if raw == nil {
			ledger.ReleaseUncalled(device, frame.ID)
			return nil
		}
		if werr := raw.WriteRaw(ctx, string(sessionID), data); werr != nil {
			ledger.MarkIndeterminate(device, frame.ID)
			return werr
		}
		ledger.Commit(device, frame.ID)
		c.sendInputAck(frame.RequestID, sessionID, frame.ID)
		return nil
	})
	if err != nil {
		if errors.Is(err, errInputLedgerFull) {
			c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeRateLimited, contract.ErrorLayerConnection,
				"input ledger at capacity", contract.ActionHintRetry)
			return
		}
		c.sendWSGateError(frame.RequestID, sessionID, err)
		return
	}
	c.adapter.TouchActivity(sessionID, time.Now())
}

// sendInputAck enqueues a CG-03 input.ack to the requesting connection's sole
// outbound writer. Best-effort: a fenced/terminal queue drops the ACK, but the
// ledger stays committed and a client retry produces a re-ACK (design §5).
func (c *wsV1Connection) sendInputAck(reqID contract.RequestID, sessionID contract.SessionID, id contract.MessageID) {
	c.writeServerEvent(contract.InputAckEvent{
		Type:      contract.ServerEventTypeInputAck,
		RequestID: reqID,
		SessionID: sessionID,
		ID:        id,
	})
}

// rawPTYPort returns the PTY raw port from the adapter (if available).
func (c *wsV1Connection) rawPTYPort() PTYRawPort {
	if rp, ok := c.adapter.sessRaw.(PTYRawPort); ok {
		return rp
	}
	return nil
}

// isInputDuplicate checks and records the input ID for dedupe (design §6.5).
func (c *wsV1Connection) isInputDuplicate(id contract.MessageID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.inputIDs[id]; exists {
		return true
	}
	// Capacity check (design §6.5: entry/ID-byte cap).
	if len(c.inputIDs) >= wsV1InputIDSetMax || c.inputIDBytes+len(id) > wsV1InputIDBytesMax {
		// At capacity: new ID gets zero-write + rate.limited.
		return true
	}
	c.inputIDs[id] = struct{}{}
	c.inputIDBytes += len(id)
	return false
}

// ---------------------------------------------------------------------------
// Resize handling (design §6.5 resize)
// ---------------------------------------------------------------------------

func (c *wsV1Connection) handleResize(frame contract.ResizeFrame) {
	c.mu.Lock()
	state := c.state
	lease := c.lease
	sessionID := c.sessionID
	c.mu.Unlock()

	if state != wsV1StateAttached || lease == nil {
		c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeControlForbidden, contract.ErrorLayerControl,
			"active session connection required", contract.ActionHintRequestControl)
		return
	}

	runtime := c.adapter.Runtime()
	if runtime == nil {
		c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeServiceDown, contract.ErrorLayerConnection,
			"control service unavailable", contract.ActionHintCheckDesktop)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlDataOperationTimeout)
	defer cancel()
	err := runtime.Gate().DoDevicePTY(ctx, lease, sessionID, PTYResize, func(ctx context.Context, permit *operationPermit) error {
		if err := permit.Checkpoint(ctx, 1); err != nil {
			return err
		}
		if raw := c.rawPTYPort(); raw != nil {
			return raw.ResizeRaw(ctx, string(sessionID), frame.Cols, frame.Rows)
		}
		return nil
	})
	if err != nil {
		c.sendWSGateError(frame.RequestID, sessionID, err)
	}
	// Success: silent.
}

// ---------------------------------------------------------------------------
// Backfill handling (design §6.5 backfill, §7.4)
// ---------------------------------------------------------------------------

func (c *wsV1Connection) handleBackfill(frame contract.BackfillFrame) {
	c.mu.Lock()
	state := c.state
	sessionID := c.sessionID
	c.mu.Unlock()

	if state != wsV1StateAttached {
		c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeControlForbidden, contract.ErrorLayerControl,
			"active session connection required", contract.ActionHintRequestControl)
		return
	}

	earliest, latest := c.adapter.Streams().SeqBounds(sessionID)
	// Try to get the requested range (design §7.4).
	frames, ok := c.adapter.Streams().RangeFrames(sessionID, frame.FromSeq, frame.ToSeq)
	if !ok || len(frames) == 0 {
		// Range not fully retained → gap variant.
		gap := contract.GapRange{
			Code:    contract.ErrorCodeHistoryGap,
			FromSeq: frame.FromSeq,
			ToSeq:   frame.ToSeq,
		}
		c.writeServerEvent(contract.BackfillGapResultEvent{
			Type:        contract.ServerEventTypeBackfillResult,
			RequestID:   frame.RequestID,
			SessionID:   sessionID,
			FromSeq:     frame.FromSeq,
			ToSeq:       frame.ToSeq,
			EarliestSeq: earliest,
			LatestSeq:   latest,
			Gap:         gap,
		})
		return
	}
	// Check backfill cap (design §6.5: 256 frames / 1 MiB).
	if len(frames) > wsV1BackfillMaxFrames {
		c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeRateLimited, contract.ErrorLayerHistory,
			"backfill range too large", contract.ActionHintContinueFromLatest)
		return
	}
	// Build frames variant.
	replayFrames := make([]contract.ReplayFrame, 0, len(frames))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var encBytes int
	for _, e := range frames {
		rf, _ := e.toWireFrame(sessionID, now)
		if rf != nil {
			replayFrames = append(replayFrames, rf)
			encBytes += len(e.output) + 64 // rough estimate
		}
		if encBytes > wsV1BackfillMaxBytes {
			c.sendWSError(frame.RequestID, sessionID, contract.ErrorCodeRateLimited, contract.ErrorLayerHistory,
				"backfill range too large", contract.ActionHintContinueFromLatest)
			return
		}
	}
	c.writeServerEvent(contract.BackfillFramesResultEvent{
		Type:        contract.ServerEventTypeBackfillResult,
		RequestID:   frame.RequestID,
		SessionID:   sessionID,
		FromSeq:     frame.FromSeq,
		ToSeq:       frame.ToSeq,
		EarliestSeq: earliest,
		LatestSeq:   latest,
		Frames:      replayFrames,
	})
}

// ---------------------------------------------------------------------------
// Close / disconnect handling (design §6.6)
// ---------------------------------------------------------------------------

// handleDisconnect is called on transport error/EOF/close. Fencing the final
// queue is the delivery barrier; exact DetachControl then removes the directory
// lease and subscriber so fence always converges to teardown (R4-003).
func (c *wsV1Connection) handleDisconnect(unexpected bool) {
	c.mu.Lock()
	handle := c.handle
	sub := c.causalSub
	c.causalSub = nil
	if c.state != wsV1StateClosed {
		c.state = wsV1StateTerminating
	}
	adapter := c.adapter
	server := c.server
	c.mu.Unlock()

	c.stopOutboundDelivery(errWSOutboundFenced)
	c.cleanupCausalSubscription(sub)
	if handle != nil && adapter != nil && adapter.Runtime() != nil {
		adapter.Runtime().DetachControl(handle, unexpected)
	}
	// Epoch-guarded and idempotent; safe after revoke/server-stop or a duplicate
	// terminal path.
	if server != nil {
		server.unregisterV1Connection(c.registration)
	}
}

// wsV1Terminal carries the terminal close info.
type wsV1Terminal struct {
	cause string
	code  int
	// preCloseEvent, when non-nil, is carried by the terminal queue item and
	// confirmed by the sole writer before the close frame (CG-01: a typed event
	// must precede the close so the client can distinguish revoke/forbidden/
	// superseded rather than inferring from a bare 1008).
	preCloseEvent contract.KnownServerEvent
}

// terminate submits one terminal queue item. Its optional typed event and close
// frame are emitted by the same outbound writer, with no residual frame allowed
// between them. Pending non-terminal frames are discarded at terminal admission.
func (c *wsV1Connection) terminate(t wsV1Terminal) {
	c.mu.Lock()
	if c.state == wsV1StateClosed {
		c.mu.Unlock()
		return
	}
	c.state = wsV1StateClosed
	handle := c.handle
	sub := c.causalSub
	c.causalSub = nil
	adapter := c.adapter
	server := c.server
	c.mu.Unlock()

	c.cleanupCausalSubscription(sub)
	if handle != nil && adapter != nil && adapter.Runtime() != nil {
		adapter.Runtime().DetachControl(handle, false)
	}
	if server != nil {
		server.unregisterV1Connection(c.registration)
	}

	var preClosePayload []byte
	if t.preCloseEvent != nil {
		preClosePayload, _ = contract.MarshalServerEvent(t.preCloseEvent)
	}
	c.ensureOutboundWriter()
	completion, err := c.outbound.enqueueTerminal(preClosePayload, t.code)
	if err != nil {
		c.requestTeardown()
		return
	}
	if writeErr := <-completion; writeErr != nil {
		c.requestTeardown()
	}
}

// cleanupCausalSubscription marks the causal subscription terminal and
// unregisters it from the hub (design §6.6 detach cleanup).
func (c *wsV1Connection) cleanupCausalSubscription(sub *causalHubSubscription) {
	if sub == nil {
		return
	}
	sub.BeginTerminal()
	if c.adapter != nil {
		if rt := c.adapter.Runtime(); rt != nil {
			rt.Hub().UnregisterCausalSubscription(sub)
		}
	}
}

// ---------------------------------------------------------------------------
// Unique outbound writer + enqueue helpers (R4-003)
// ---------------------------------------------------------------------------

func (c *wsV1Connection) ensureOutboundWriter() {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	bootstrapPending := state == wsV1StateUpgradedUnregistered || state == wsV1StateRegisteredAwaitAttach
	c.outboundOnce.Do(func() {
		c.outbound = newWSOutboundQueue(bootstrapPending)
		c.outboundWriterDone = make(chan struct{})
		go c.outboundWriteLoop()
	})
}

// outboundWriteLoop is the ONLY goroutine permitted to write a websocket frame.
// All server events, auth pre-close events, and close frames arrive as queue
// items. Producers only marshal/admit; they never touch the socket.
func (c *wsV1Connection) outboundWriteLoop() {
	defer close(c.outboundWriterDone)
	if c.outboundWriterGate != nil {
		<-c.outboundWriterGate
	}
	for {
		take := c.outbound.take()
		switch {
		case take.stopped:
			return
		case take.terminal != nil:
			err := c.writeTerminal(take.terminal)
			completeWSOutbound(take.terminal.completion, err)
			return
		case take.frame != nil:
			if !c.outbound.mayWrite(take.generation) {
				completeWSOutbound(take.frame.completion, errWSOutboundFenced)
				continue
			}
			err := c.writeSocketFrame(websocket.TextMessage, take.frame.payload)
			if err == nil && take.frame.priority == wsOutboundAttached {
				if take.frame.bootstrap != nil {
					take.frame.bootstrap.MarkWritten()
				}
				c.outbound.finishBootstrap()
			}
			completeWSOutbound(take.frame.completion, err)
			if err != nil {
				c.requestTeardown()
				return
			}
		default:
			<-c.outbound.notify
		}
	}
}

func (c *wsV1Connection) writeTerminal(terminal *wsOutboundTerminal) error {
	var writeErr error
	payloads := terminal.preClosePayloads
	if terminal.removal != nil {
		payloads = terminal.removal.PayloadsForWriter()
	}
	for _, payload := range payloads {
		if len(payload) == 0 {
			continue
		}
		writeErr = c.writeSocketFrame(websocket.TextMessage, payload)
		if writeErr != nil {
			break
		}
	}
	if writeErr == nil {
		writeErr = c.writeSocketFrame(websocket.CloseMessage,
			websocket.FormatCloseMessage(terminal.closeCode, ""))
	}
	c.closeTransportFromWriter()
	return writeErr
}

// writeSocketFrame is the single physical socket-write owner. Keeping the only
// WriteMessage call here makes read-loop bypasses mechanically testable.
func (c *wsV1Connection) writeSocketFrame(messageType int, payload []byte) error {
	if c.conn == nil {
		return errWSSocketUnavailable
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(wsV1WriteDeadline))
	return c.conn.WriteMessage(messageType, payload)
}

func (c *wsV1Connection) closeTransportFromWriter() {
	c.writeFault.Do(func() {
		closeFn := c.closeFn
		if closeFn == nil && c.conn != nil {
			closeFn = c.conn.Close
		}
		if closeFn != nil {
			_ = closeFn()
		}
	})
}

// requestTeardown performs the fence + non-blocking transport half of the
// transaction. Queue and lease are fenced synchronously; Close runs in a
// dedicated goroutine so an H3/control hub lock holder never waits on socket
// I/O (N-004). EOF drives handleDisconnect's exact DetachControl cleanup.
func (c *wsV1Connection) requestTeardown() {
	c.ensureOutboundWriter()
	c.outbound.fence(errWSOutboundFenced)
	c.mu.Lock()
	lease := c.lease
	c.mu.Unlock()
	if lease != nil {
		lease.fence()
	}
	c.writeFault.Do(func() {
		closeFn := c.closeFn
		if closeFn == nil && c.conn != nil {
			closeFn = c.conn.Close
		}
		if closeFn != nil {
			go func() { _ = closeFn() }()
		}
	})
}

func (c *wsV1Connection) stopOutboundDelivery(reason error) {
	c.ensureOutboundWriter()
	c.outbound.fence(reason)
}

func outboundPriorityFor(ev contract.KnownServerEvent) wsOutboundPriority {
	switch ev.(type) {
	case contract.SessionAttachedEvent:
		return wsOutboundAttached
	case contract.ControlStateEvent, contract.AuthRevokedEvent:
		return wsOutboundControl
	case contract.ErrorEvent:
		return wsOutboundError
	case contract.InputAckEvent:
		return wsOutboundInputAck
	case contract.BackfillFramesResultEvent, contract.BackfillGapResultEvent:
		return wsOutboundBackfill
	default:
		// output + session.state + restart boundary are H3 causal projections.
		return wsOutboundCausal
	}
}

func (c *wsV1Connection) enqueueServerEvent(ev contract.KnownServerEvent, wait bool) (<-chan error, error) {
	payload, err := contract.MarshalServerEvent(ev)
	if err != nil {
		return nil, err
	}
	c.ensureOutboundWriter()
	completion, enqueueErr := c.outbound.enqueue(outboundPriorityFor(ev), payload, wait)
	if errors.Is(enqueueErr, errWSOutboundFull) || errors.Is(enqueueErr, errWSOutboundFenced) {
		c.requestTeardown()
	}
	return completion, enqueueErr
}

// writeServerEvent now means “admit to the unique outbound queue”. It returns
// false when a fence/terminal prevents admission; callers must stop draining.
func (c *wsV1Connection) writeServerEvent(ev contract.KnownServerEvent) bool {
	_, err := c.enqueueServerEvent(ev, false)
	return err == nil
}

func (c *wsV1Connection) writeServerEventSync(ev contract.KnownServerEvent) error {
	completion, err := c.enqueueServerEvent(ev, true)
	if err != nil {
		return err
	}
	return <-completion
}

func (c *wsV1Connection) writeBootstrapPayloadSync(payload []byte, bootstrap *attachmentBootstrap) error {
	c.ensureOutboundWriter()
	completion, err := c.outbound.enqueueWithBootstrap(wsOutboundAttached, payload, true, bootstrap)
	if err != nil {
		return err
	}
	return <-completion
}

func (c *wsV1Connection) newWSError(reqID contract.RequestID, sessionID contract.SessionID, code contract.ErrorCode, layer contract.ErrorLayer, msg string, hint contract.ActionHint) contract.ErrorEvent {
	return contract.ErrorEvent{
		Type:       contract.ServerEventTypeError,
		RequestID:  reqID,
		SessionID:  sessionID,
		Code:       code,
		Layer:      layer,
		Message:    msg,
		ActionHint: hint,
	}
}

// sendWSError enqueues an error response; the read loop never writes directly.
func (c *wsV1Connection) sendWSError(reqID contract.RequestID, sessionID contract.SessionID, code contract.ErrorCode, layer contract.ErrorLayer, msg string, hint contract.ActionHint) {
	c.writeServerEvent(c.newWSError(reqID, sessionID, code, layer, msg, hint))
}

func (c *wsV1Connection) newWSGateError(reqID contract.RequestID, sessionID contract.SessionID, err error) contract.ErrorEvent {
	mapper := c.adapter.Mapper()
	re, ok := mapper.mapGateError(reqID, err)
	if !ok {
		re = mapper.mapGenericError(reqID)
	}
	return contract.ErrorEvent{
		Type:       contract.ServerEventTypeError,
		RequestID:  reqID,
		SessionID:  sessionID,
		Code:       re.body.Code,
		Layer:      re.body.Layer,
		Message:    re.body.Message,
		ActionHint: re.body.ActionHint,
	}
}

// sendWSGateError maps and enqueues a gate error.
func (c *wsV1Connection) sendWSGateError(reqID contract.RequestID, sessionID contract.SessionID, err error) {
	c.writeServerEvent(c.newWSGateError(reqID, sessionID, err))
}

// sendErrorAndClose carries the error inside the terminal item, guaranteeing
// error → close adjacency without a second writer or a priority race.
func (c *wsV1Connection) sendErrorAndClose(reqID contract.RequestID, code contract.ErrorCode, layer contract.ErrorLayer, msg string, hint contract.ActionHint, closeCode int) {
	c.terminate(wsV1Terminal{
		cause: "protocol_error", code: closeCode,
		preCloseEvent: c.newWSError(reqID, "", code, layer, msg, hint),
	})
}

// wsQueueFullFencer implements SubscriptionAuthorityFencer for a WS connection.
// On a causal-subscription queue-full it tears the connection down exactly once
// (M-007). It NEVER blocks inside the H3 causal-ledger lock: the synchronous
// authority fence (sub.lease.fence()) marks the subscription fenced under the
// ledger lock, but the transport teardown is deferred to a goroutine via
// requestTeardown (N-004). Close() unblocks the read loop, whose
// handleDisconnect runs the authoritative exact-lease fence + detach +
// unregister. Reentrant-safe via the connection's writeFault Once.
type wsQueueFullFencer struct {
	conn *wsV1Connection
}

// FenceSubscriptionWrites implements SubscriptionAuthorityFencer.
func (f wsQueueFullFencer) FenceSubscriptionWrites(_ SubscriptionFenceToken, _ interface{}) AuthorityFenceReceipt {
	if f.conn == nil {
		return AuthorityFenceReceipt{}
	}
	// N-004: the transport teardown is async — the ledger lock holder returns
	// immediately. The lease fence (already performed synchronously by
	// fenceAuthority before invoking this fencer) is the authoritative signal;
	// the Close only accelerates the read-loop EOF.
	f.conn.requestTeardown()
	return AuthorityFenceReceipt{LeaseFenced: true}
}

// ---------------------------------------------------------------------------
// State helpers
// ---------------------------------------------------------------------------

func (c *wsV1Connection) setState(s wsV1ConnectionState) {
	c.mu.Lock()
	c.state = s
	c.mu.Unlock()
}

func isClosedChan(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// writeWSAuthFail writes the auth failure as an HTTP error (pre-upgrade).
func writeWSAuthFail(w http.ResponseWriter, fail deviceAuthFailure) {
	switch fail {
	case authMissing:
		writeV1Error(w, "", http.StatusUnauthorized, contract.ErrorCodeAuthUnpaired,
			contract.ErrorLayerAuth, "device not paired", contract.ActionHintRePair)
	case authExpired:
		http.SetCookie(w, clearDeviceCookie(&http.Request{Header: http.Header{}}))
		writeV1Error(w, "", http.StatusUnauthorized, contract.ErrorCodeAuthWindowExpired,
			contract.ErrorLayerAuth, "device credential expired", contract.ActionHintRePair)
	case authRevoked:
		writeV1Error(w, "", http.StatusUnauthorized, contract.ErrorCodeAuthRevoked,
			contract.ErrorLayerAuth, "device revoked", contract.ActionHintRePair)
	default:
		writeV1Error(w, "", http.StatusUnauthorized, contract.ErrorCodeAuthUnpaired,
			contract.ErrorLayerAuth, "device not paired", contract.ActionHintRePair)
	}
}

// unregisterV1Connection removes the connection from the M1 registry using the
// retained registration handle (N-003). Epoch-guarded: a stale unregister (the
// entry already detached by FenceDevice/Stop, or superseded by a newer epoch)
// is an idempotent no-op and never deletes a newer entry.
func (s *Server) unregisterV1Connection(reg ConnectionRegistration) {
	if s.registry != nil {
		s.registry.Unregister(reg)
	}
}
