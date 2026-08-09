package remote

// control_event.go — ControlProjector: audience-relative control snapshot/event
// projection (design §8.1–§8.4) + SessionEventHub: per-(connection,session)
// subscriber FIFO with contract validation and a writer-goroutine delivery seam
// to ControlEventConsumer (design §8.1: "one writer goroutine per connection").
//
// The arbiter produces internal controlTransition records under stateMu. The
// ControlProjector converts them into contract.ControlStateEvent for each
// audience. The SessionEventHub validates each event via
// contract.ValidateServerEvent, routes it only to subscribers attached to that
// session. Production /ws/v1 subscribers admit directly into the connection's
// unique bounded outbound queue; legacy/spy consumers retain the subscriber FIFO
// and optional writer-goroutine delivery seam.
//
// The real M2 /ws/v1 actor implements both ControlEventConsumer compatibility
// and the bounded outbound admission port. Spy consumers exercise the legacy
// FIFO path in projector/hub tests.
//
// Lock order (design §9.3): SessionEventHub.mu may be acquired WHILE holding
// stateMu (stateMu → hub is the only allowed nesting). There is NO hub → state
// callback. The hub does validate/route/reserve/enqueue only — no socket I/O.
// A production WS consumer may expose the bounded controlOutboundQueueConsumer
// admission port; that callback only performs bounded in-memory marshal/admit
// work and never writes transport I/O. Other handlers run OUTSIDE hub.mu.

import (
	"sync"
	"sync/atomic"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// ControlProjector — audience-relative projection (design §8.2)
// ---------------------------------------------------------------------------

// ControlProjector converts internal holder state into wire-valid
// contract.ControlSnapshot for a given viewer device. It enforces the privacy
// invariant: DeviceID/ConnectionID/epoch never appear on wire; deviceName
// appears ONLY in the "other" state.
type ControlProjector struct{}

// NewControlProjector returns a projector.
func NewControlProjector() ControlProjector { return ControlProjector{} }

// SnapshotForViewer projects the current holder into a wire ControlSnapshot
// relative to the viewer device. A viewer DeviceID of "" denotes the desktop
// trusted projection caller (which still gets the same relative view; the
// trusted-side extra metadata is a separate concern).
func (ControlProjector) SnapshotForViewer(owner controlOwner, viewer contract.DeviceID) contract.ControlSnapshot {
	switch owner.kind {
	case ownerNone:
		return contract.ControlSnapshot{State: contract.ControlStateNone}
	case ownerDesktop:
		return contract.ControlSnapshot{State: contract.ControlStateDesktop}
	case ownerDevice:
		if owner.deviceID == viewer && viewer != "" {
			// Same device (authoritative WS or same-device REST viewer, including
			// grace): projects as "you".
			return contract.ControlSnapshot{State: contract.ControlStateYou}
		}
		// Other device: project "other" with sanitized deviceName.
		name := owner.deviceName
		return contract.ControlSnapshot{
			State:      contract.ControlStateOther,
			DeviceName: &name,
		}
	default:
		return contract.ControlSnapshot{State: contract.ControlStateNone}
	}
}

// EventFromTransition converts an internal controlTransition into a validated
// contract.ControlStateEvent for the given viewer. Returns the event and true if
// the transition should be broadcast to this viewer; returns zero-value and
// false if no event is warranted (e.g., rebind/phase-change that doesn't change
// wire state).
//
// Per design §8.3, a transition is broadcast only when the WIRE state identity
// changes for the viewer. Rebind (same device, same wire state), connected→grace
// (wire holder unchanged), and desktop-source refresh (wire state unchanged) do
// NOT produce a control.state event.
func (p ControlProjector) EventFromTransition(
	t controlTransition,
	viewer contract.DeviceID,
) (contract.ControlStateEvent, bool) {
	oldSnap := p.SnapshotForViewer(t.oldOwner, viewer)
	newSnap := p.SnapshotForViewer(t.newOwner, viewer)
	if oldSnap.State == newSnap.State && oldSnap.DeviceName == newSnap.DeviceName {
		// Wire state identity unchanged for this viewer — no event.
		return contract.ControlStateEvent{}, false
	}
	ev := contract.ControlStateEvent{
		Type:       contract.ServerEventTypeControlState,
		SessionID:  t.sessionID,
		State:      newSnap.State,
		DeviceName: newSnap.DeviceName,
		Reason:     string(t.reason),
		OccurredAt: t.occurredAt.UTC().Format(time.RFC3339Nano),
	}
	return ev, true
}

// ---------------------------------------------------------------------------
// SessionEventHub — per-(connection,session) subscriber FIFO (design §8.1, §8.4)
//
// A3 wiring: the hub routes control transitions ONLY to subscribers attached to
// the same session, validates each per-viewer event via
// contract.ValidateServerEvent before enqueue, and (when a consumer is set)
// runs a writer goroutine per subscriber that drains the FIFO and delivers to
// the consumer. The hub does NOT do socket I/O directly.
//
// Lock order (design §9.3): stateMu → hub.mu is the only allowed nesting. The
// writer goroutine drains OUTSIDE hub.mu (it takes only the subscriber mutex).
// ---------------------------------------------------------------------------

// controlOutboundQueueConsumer is the production lock-safe fast path into the
// unique WS outbound queue (R4-003). It is invoked while stateMu → hub.mu may be
// held, so implementations MUST perform only bounded in-memory marshal/admission and
// MUST NOT write/close the transport or call back into the arbiter/hub.
type controlOutboundQueueConsumer interface {
	enqueueControlStateOutbound(contract.ControlStateEvent) bool
}

// hubSubscriber is one authenticated subscriber's FIFO buffer for a specific
// (connection, session) attachment. viewerDeviceID is the immutable
// authenticated device identity for audience-relative projection. lease is an
// opaque reference to the connection lease; a fenced lease means the subscriber
// is no longer active. consumer is the optional delivery seam; when nil, events
// accumulate for manual Drain (sync mode).
type hubSubscriber struct {
	sessionID      contract.SessionID
	viewerDeviceID contract.DeviceID
	lease          *ControlConnectionLease
	consumer       ControlEventConsumer // optional; nil = sync-drain mode

	// fencer (R3-003) fences the subscriber's write authority on a control-FIFO
	// overflow (writer mode). Mirrors the causal subscription's queue-full fence:
	// the lease live-bit is cleared synchronously and the transport is torn down
	// asynchronously so a slow consumer cannot keep a stale authority view.
	fencer    SubscriptionAuthorityFencer
	bootstrap *attachmentBootstrap
	active    atomic.Bool

	mu       sync.Mutex
	queue    []contract.KnownServerEvent
	capacity int
	fenced   bool

	// notify is a buffered-size-1 signal channel woken on enqueue (writer mode).
	notify chan struct{}
	// done is closed to stop the writer goroutine (unsubscribe/shutdown).
	done chan struct{}
	// once guards writer-goroutine start + done close.
	startOnce sync.Once
	stopOnce  sync.Once
	// fenceOnce guards the synchronous authority fence (idempotent).
	fenceOnce sync.Once
	// fencerOnce independently guards the transport-teardown effect. It cannot
	// share fenceOnce: attach may overflow before the WS fencer is installed, in
	// which case late installation must still complete fence → teardown exactly
	// once (R4-003).
	fencerOnce sync.Once
}

// SessionEventHub is the in-memory event subscription hub. It validates and
// enqueues server events per subscriber in FIFO order. Slow subscribers are
// fenced (isolated) rather than blocking the arbiter.
type SessionEventHub struct {
	mu          sync.Mutex
	subscribers map[*hubSubscriber]struct{}

	projector ControlProjector
	ready     atomic.Bool

	// onValidationError is invoked (outside hub.mu) when a projected event fails
	// contract validation. This is a defense-in-depth never-event (the projector
	// is deterministic); per design §8.1 a producer contract failure latches the
	// gate. The runtime wires this to the arbiter's health latch.
	onValidationError func()

	// H3 causal ledger map (design §4A.4). causalMu protects the ledgers map;
	// each ledger has its own mu (lock order #9). The ledgers map is independent
	// of the subscribers map above (which is the legacy/control-transition FIFO).
	causalMu sync.Mutex
	ledgers  map[contract.SessionID]*causalLedger
}

// NewSessionEventHub creates a hub in the not-ready state with default
// per-subscriber capacity.
func NewSessionEventHub() *SessionEventHub {
	h := &SessionEventHub{
		subscribers: make(map[*hubSubscriber]struct{}),
		projector:   NewControlProjector(),
		ledgers:     make(map[contract.SessionID]*causalLedger),
	}
	return h
}

const defaultHubSubscriberCapacity = 256

// MarkReady enables the hub for production use.
func (h *SessionEventHub) MarkReady() { h.ready.Store(true) }

// IsReady reports whether the hub is ready.
func (h *SessionEventHub) IsReady() bool { return h.ready.Load() }

// SetOnValidationError wires the defense-in-depth validation-failure callback
// (latches the arbiter health). Must be set before MarkReady.
func (h *SessionEventHub) SetOnValidationError(fn func()) { h.onValidationError = fn }

// Subscribe registers a subscriber attached to (sessionID) for the given viewer
// device and returns the subscriber handle. If consumer is non-nil the
// subscriber is in writer mode; the caller MUST call StartWriter to launch the
// drain goroutine (M-007: this is NOT done automatically, so a production
// attach flow can guarantee the `session.attached` frame is on the wire before
// any control.state event is delivered). Until StartWriter is called, events
// accumulate in the bounded FIFO.
//
// The subscriber starts with an empty queue. The lease is consulted on each
// enqueue to skip fenced/inactive subscribers.
func (h *SessionEventHub) Subscribe(
	sessionID contract.SessionID,
	viewerDeviceID contract.DeviceID,
	lease *ControlConnectionLease,
	consumer ControlEventConsumer,
) *hubSubscriber {
	sub := &hubSubscriber{
		sessionID:      sessionID,
		viewerDeviceID: viewerDeviceID,
		lease:          lease,
		consumer:       consumer,
		capacity:       defaultHubSubscriberCapacity,
		notify:         make(chan struct{}, 1),
		done:           make(chan struct{}),
	}
	sub.active.Store(true)
	h.mu.Lock()
	h.subscribers[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

func (h *SessionEventHub) PrepareControlSubscription(sessionID contract.SessionID, viewerDeviceID contract.DeviceID, lease *ControlConnectionLease, consumer ControlEventConsumer) *hubSubscriber {
	sub := &hubSubscriber{
		sessionID: sessionID, viewerDeviceID: viewerDeviceID, lease: lease,
		consumer: consumer, capacity: defaultHubSubscriberCapacity,
		notify: make(chan struct{}, 1), done: make(chan struct{}),
	}
	h.mu.Lock()
	h.subscribers[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

func (h *SessionEventHub) commitPreparedControlSubscriptionNoFail(sub *hubSubscriber) {
	if sub == nil {
		panic("remote: missing prepared control subscription")
	}
	sub.active.Store(true)
}

func (h *SessionEventHub) AbortPreparedControlSubscription(sub *hubSubscriber) {
	if sub == nil || sub.active.Load() {
		return
	}
	h.mu.Lock()
	delete(h.subscribers, sub)
	h.mu.Unlock()
	sub.stop()
}

// StartWriter launches the per-subscriber writer goroutine that drains the FIFO
// and delivers control.state events to the consumer (M-007). The caller MUST
// invoke this only after the `session.attached` frame is on the wire so that
// attached is always the first event the client observes (no control transition
// during the attach window can preempt it on the socket). Idempotent; no-op in
// sync-drain mode (consumer == nil).
func (s *hubSubscriber) StartWriter() {
	if s.consumer == nil {
		return
	}
	s.startOnce.Do(func() {
		go s.writeLoop()
	})
}

// SetAuthorityFencer wires the queue-full authority fencer (R4-003). Production
// installs it immediately after AttachControl returns, before history assembly.
// There is still an unavoidable Subscribe→return interval, so installation also
// detects an already-fenced subscriber and completes the missing transport
// teardown. The effect is exact-once even if SetAuthorityFencer is repeated.
func (s *hubSubscriber) SetAuthorityFencer(f SubscriptionAuthorityFencer) {
	s.mu.Lock()
	s.fencer = f
	alreadyFenced := s.fenced
	s.mu.Unlock()
	if alreadyFenced {
		s.notifyAuthorityFencer()
	}
}

// fenceAuthority synchronously fences the subscriber + exact lease, then
// invokes the transport fencer if one is installed. The authority and transport
// effects have separate once guards so a pre-install overflow cannot consume
// the later teardown opportunity (R4-003 fence → teardown closure).
func (s *hubSubscriber) fenceAuthority() {
	s.fenceOnce.Do(func() {
		s.mu.Lock()
		s.fenced = true
		lease := s.lease
		s.mu.Unlock()
		if lease != nil {
			lease.fence()
		}
	})
	s.notifyAuthorityFencer()
}

func (s *hubSubscriber) notifyAuthorityFencer() {
	s.mu.Lock()
	fenced := s.fenced
	lease := s.lease
	fencer := s.fencer
	s.mu.Unlock()
	if !fenced || fencer == nil {
		return
	}
	s.fencerOnce.Do(func() {
		fencer.FenceSubscriptionWrites(SubscriptionFenceToken{lease: lease}, nil)
	})
}

// Unsubscribe removes a subscriber and stops its writer goroutine (if any). It
// flushes any remaining buffered events to the consumer before stopping.
func (h *SessionEventHub) Unsubscribe(sub *hubSubscriber) {
	if sub == nil {
		return
	}
	h.mu.Lock()
	delete(h.subscribers, sub)
	h.mu.Unlock()
	sub.stop()
}

// EnqueueControlTransition projects a control transition to the active
// subscribers attached to the SAME session and enqueues the resulting event
// into each subscriber's FIFO. This is called by the arbiter under stateMu
// (stateMu → hub is allowed). It is a synchronous bounded memory operation — no
// socket I/O.
//
// Each per-viewer event is validated via contract.ValidateServerEvent before
// enqueue. On validation failure (a producer never-event), the hub invokes the
// onValidationError callback (which latches the arbiter health per design §8.1)
// and drops the offending event.
//
// If a subscriber's queue is full (slow subscriber) or its consumer reports it
// is no longer alive, the subscriber is fenced (marked inactive) and the event
// is dropped for that subscriber; it will be isolated/closed later outside the
// lock.
func (h *SessionEventHub) EnqueueControlTransition(t controlTransition) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subscribers {
		if sub.sessionID != t.sessionID {
			continue // route only to subscribers attached to this session
		}
		if !sub.active.Load() {
			continue
		}
		if sub.IsFenced() || (sub.lease != nil && !sub.lease.IsLive()) {
			continue
		}
		if sub.consumer != nil && !sub.consumer.ConsumerAlive() {
			// Consumer death is the same authority hazard as capacity overflow:
			// synchronously fence the exact lease and complete transport teardown
			// when a fencer is installed. Merely flipping sub.fenced can leave a
			// live directory lease behind during the attach window (R4-003).
			sub.fenceAuthority()
			continue
		}
		ev, ok := h.projector.EventFromTransition(t, sub.viewerDeviceID)
		if !ok {
			continue // no wire state change for this viewer
		}
		// Defense-in-depth: validate the projected event via the frozen contract
		// validator before enqueue. The projector is deterministic, so this is a
		// never-event; on failure latch the gate (design §8.1).
		if verr := contract.ValidateServerEvent(ev); verr != nil {
			h.invokeValidationError()
			continue
		}
		// Production /ws/v1 control transitions enter the same final queue as
		// error/backfill/causal events synchronously at this linearization point.
		// This removes scheduler lag between a control transition and a concurrent
		// read-loop backfill response. Legacy/spy consumers retain the bounded
		// per-subscriber FIFO below.
		if outbound, ok := sub.consumer.(controlOutboundQueueConsumer); ok {
			if !outbound.enqueueControlStateOutbound(ev) {
				sub.fenceAuthority()
			}
			continue
		}
		enqueued := sub.enqueue(ev)
		if !enqueued {
			// R3-003: a full control FIFO MUST fence the subscriber's authority (sync
			// OR writer mode). Previously writer-mode overflow silently dropped the
			// event, leaving the client with a stale authority view (e.g. it kept
			// acting on a control.state it never received). Now both modes fence:
			// sync mode marks fenced (isolation); writer mode additionally tears down
			// the transport via the fencer so the client reconnects with a fresh
			// attached snapshot rather than running on a stale authority.
			sub.fenceAuthority()
		}
	}
}

// invokeValidationError calls the latch callback outside hub.mu.
func (h *SessionEventHub) invokeValidationError() {
	fn := h.onValidationError
	h.mu.Unlock()
	if fn != nil {
		fn()
	}
	h.mu.Lock()
}

// enqueue appends an event to the subscriber's FIFO. Returns false if the queue
// is full (slow subscriber). In writer mode the slow subscriber is fenced by the
// writer draining; here we only drop + fence in sync mode.
func (s *hubSubscriber) enqueue(ev contract.KnownServerEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fenced {
		return false
	}
	if len(s.queue) >= s.capacity {
		return false // slow subscriber
	}
	s.queue = append(s.queue, ev)
	// Wake the writer goroutine (writer mode). Non-blocking; notify is buffered.
	if s.consumer != nil {
		select {
		case s.notify <- struct{}{}:
		default:
		}
	}
	return true
}

// writeLoop is the per-subscriber writer goroutine (writer mode only). It drains
// the FIFO and delivers events to the consumer OUTSIDE any hub/arbiter lock.
// On done (unsubscribe/shutdown) it flushes remaining events then exits.
func (s *hubSubscriber) writeLoop() {
	for {
		select {
		case <-s.done:
			s.drainAndDeliver()
			return
		case <-s.notify:
			s.drainAndDeliver()
		}
	}
}

// drainAndDeliver drains the FIFO under the subscriber mutex and delivers each
// event to the consumer outside the lock. The consumer is invoked one event at
// a time preserving FIFO order.
func (s *hubSubscriber) drainAndDeliver() {
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.mu.Unlock()
			return
		}
		batch := s.queue
		s.queue = nil
		fenced := s.fenced
		s.mu.Unlock()
		if fenced {
			return
		}
		for _, ev := range batch {
			if s.consumer == nil {
				return
			}
			if ctrl, ok := ev.(contract.ControlStateEvent); ok {
				s.consumer.DeliverControlState(ctrl)
			}
		}
	}
}

// stop signals the writer goroutine to flush + exit. Idempotent.
func (s *hubSubscriber) stop() {
	s.stopOnce.Do(func() {
		close(s.done)
	})
}

// Drain returns and clears the pending events for a subscriber (sync mode, for
// tests that check ordering directly). In writer mode the queue is drained by
// the goroutine, so Drain returns whatever remains.
func (s *hubSubscriber) Drain() []contract.KnownServerEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.queue
	s.queue = nil
	return out
}

// IsFenced reports whether this subscriber has been fenced (slow/isolated).
func (s *hubSubscriber) IsFenced() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fenced
}

// ViewerDeviceID returns the authenticated viewer device for this subscriber.
func (s *hubSubscriber) ViewerDeviceID() contract.DeviceID { return s.viewerDeviceID }

// SessionID returns the session this subscriber is attached to.
func (s *hubSubscriber) SessionID() contract.SessionID { return s.sessionID }

// Clear drops all subscribers, stops their writer goroutines, and marks
// not-ready (shutdown).
func (h *SessionEventHub) Clear() {
	h.mu.Lock()
	subs := make([]*hubSubscriber, 0, len(h.subscribers))
	for sub := range h.subscribers {
		subs = append(subs, sub)
	}
	h.subscribers = make(map[*hubSubscriber]struct{})
	h.mu.Unlock()
	for _, sub := range subs {
		sub.stop()
	}
	h.ready.Store(false)
}
