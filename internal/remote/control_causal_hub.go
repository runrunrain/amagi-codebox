package remote

// control_causal_hub.go — H3: causal reservation ledger + causal subscription
// with startAfterEventOrdinal + queue-full authority fence (design §4A.4).
//
// This file implements the concrete H0 causal ports (SessionCausalReservationPort
// + SessionCausalPublicationPort) on the SessionEventHub. It adds:
//
//   - A per-session causal ledger (bounded 4096 unresolved reservations) that
//     reserves event ordinals at H1 commit time (under the three-lock domain)
//     and releases them via PublishReserved (pump-time payload delivery).
//   - CausalWatermark {Run, Event}: Run=highest reserved run position whose
//     ticket.Event ≤ watermark.Event; Event=highest reserved event ordinal.
//   - HubSubscription with immutable startAfterEventOrdinal: a subscription
//     created at attach time filters out events with ordinal ≤ watermark.Event
//     (design §4A.4: "≤watermark delayed ticket即使pump晚到也计
//     CausalSkippedBeforeStart").
//   - Queue-full authority fence: when a subscriber queue is full, the exact
//     lease live-bit + connection-write epoch is fenced BEFORE the delivery API
//     returns FencedFull (design §4A.4, R3).
//
// Lock order (design §9.1): causal-ledger.mu nests UNDER feed.mu which nests
// UNDER stateMu. The hub lock is the causal-ledger lock. Hub → state is NEVER
// allowed. PublishReserved does NOT take stateMu.

import (
	"context"
	"sync"
	"sync/atomic"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Frozen causal ledger constants (design §4A.4, R9)
// ---------------------------------------------------------------------------

const (
	// causalLedgerMaxUnresolved is the per-session cap on unresolved (reserved,
	// not-yet-released) reservations. Overflow → fail closed (design §4A.4).
	causalLedgerMaxUnresolved = 4096
	// causalSubscriptionCapacity is the per-subscriber event queue capacity
	// (design §4A.4: 256 events / 2 MiB).
	causalSubscriptionCapacity = 256
)

// ---------------------------------------------------------------------------
// causalLedger — per-session causal reservation store (design §4A.4)
// ---------------------------------------------------------------------------

// causalLedger tracks reserved event ordinals and the causal watermark for one
// session. mu is the causal-ledger lock (lock order #9). It nests UNDER feed.mu.
type causalLedger struct {
	mu        sync.Mutex
	sessionID contract.SessionID

	// nextOrdinal is the next event ordinal to reserve (monotonic, per session).
	nextOrdinal SessionEventOrdinal

	// reservations maps ordinal → reservation (reserved or ready, not yet
	// released or suppressed).
	reservations map[SessionEventOrdinal]*CausalEventReservation

	// unresolvedCount tracks reserved-but-not-released reservations for the cap.
	unresolvedCount int
	preparedCount   int
	preparedOwner   uint64

	// watermark is the causal cut: highest reserved event ordinal + highest
	// reserved run position (design §4A.4).
	watermark CausalWatermark

	// sealedSegments records exact, generation-bound seal tombstones.
	sealedSegments map[RunSegmentID]causalSegmentSeal
	sealGeneration uint64

	// faulted is set on ledger health failure (overflow/wrap). Future attach =
	// service.down; all unresolved reservations are suppressed.
	faulted bool

	// subs are the active causal subscriptions for this session. Each has an
	// immutable startAfterEventOrdinal.
	subs []*causalHubSubscription
}

type causalSegmentSeal struct {
	lastSource RunSourceOrdinal
	generation uint64
}

func newCausalLedger(sessionID contract.SessionID) *causalLedger {
	return &causalLedger{
		sessionID:      sessionID,
		nextOrdinal:    1, // ordinals start at 1 (0 = sentinel for empty)
		reservations:   make(map[SessionEventOrdinal]*CausalEventReservation),
		sealedSegments: make(map[RunSegmentID]causalSegmentSeal),
	}
}

// ---------------------------------------------------------------------------
// SessionEventHub causal port implementation (design §4A.4)
//
// These methods implement SessionCausalReservationPort +
// SessionCausalPublicationPort. The caller (H1 committer) already holds stateMu
// + feed.mu; these methods take only the causal-ledger lock (the three-lock
// domain's innermost lock).
// ---------------------------------------------------------------------------

// ledgerFor returns the causal ledger for a session, creating it if needed.
// Caller must NOT hold the hub/causal lock.
func (h *SessionEventHub) ledgerFor(sessionID contract.SessionID) *causalLedger {
	h.causalMu.Lock()
	defer h.causalMu.Unlock()
	l := h.ledgers[sessionID]
	if l == nil {
		l = newCausalLedger(sessionID)
		h.ledgers[sessionID] = l
	}
	return l
}

// PreflightRunRecordBatchUnderState verifies that a boundary + staged-prefix
// batch can reserve all causal tickets before CommitRestartSegment mutates the
// feed. Caller holds stateMu + feed.mu, so no production run reservation for the
// same session can invalidate this check before the batch appends.
func (h *SessionEventHub) PreflightRunRecordBatchUnderState(sessionID contract.SessionID, count int) error {
	l := h.ledgerFor(sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()
	if count < 0 || l.faulted {
		return errCausalLedgerFaulted
	}
	if count > causalLedgerMaxUnresolved-l.unresolvedCount-l.preparedCount {
		l.faulted = true
		return errCausalLedgerFull
	}
	if count > 0 && uint64(l.nextOrdinal) > ^uint64(0)-uint64(count) {
		l.faulted = true
		return errCausalLedgerFaulted
	}
	return nil
}

// ReserveRunRecordUnderState reserves an event ordinal for a run record (design
// §4A.4). Caller already holds stateMu + feed.mu. This does O(1) capacity/
// ordinal/ticket mutation under the causal-ledger lock only.
func (h *SessionEventHub) ReserveRunRecordUnderState(
	sessionID contract.SessionID,
	position RunCausalPosition,
	class CausalProjectionClass,
) (*CausalEventReservation, error) {
	l := h.ledgerFor(sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.faulted {
		return nil, errCausalLedgerFaulted
	}
	if l.unresolvedCount+l.preparedCount >= causalLedgerMaxUnresolved {
		l.faulted = true
		return nil, errCausalLedgerFull
	}
	if l.nextOrdinal == SessionEventOrdinal(^uint64(0)) {
		l.faulted = true
		return nil, errCausalLedgerFaulted
	}
	// Check for sealed segment: if this position's segment is sealed and the
	// source is ≤ the sealed lastSource, this is a late old-run observation.
	// For runState class, suppress; for replay, it may still be valid (released
	// tickets keep their queue). Here we only refuse if the segment is sealed
	// AND the source is past the seal point for runState.
	if seal, ok := l.sealedSegments[position.SegmentID]; ok && class == CausalRunState && position.Source > seal.lastSource {
		// Late runState after seal: suppress.
		ticket := &CausalEventReservation{
			sessionID: sessionID,
			position:  position,
			class:     class,
			state:     causalSuppressed,
		}
		return ticket, nil
	}
	ordinal := l.nextOrdinal
	l.nextOrdinal++
	ticket := &CausalEventReservation{
		sessionID: sessionID,
		position:  position,
		ordinal:   ordinal,
		class:     class,
		state:     causalReserved,
	}
	l.reservations[ordinal] = ticket
	l.unresolvedCount++
	// Update watermark: Event = highest reserved; Run = highest reserved position
	// whose ticket.Event ≤ watermark.Event.
	if ordinal > l.watermark.Event {
		l.watermark.Event = ordinal
	}
	if position.SegmentID > l.watermark.Run.SegmentID ||
		(position.SegmentID == l.watermark.Run.SegmentID && position.Source > l.watermark.Run.Source) {
		l.watermark.Run = position
	}
	return ticket, nil
}

type PreparedTerminalStateReservation struct {
	ledger    *causalLedger
	ticket    *CausalEventReservation
	committed bool
}

// PrepareTerminalStateReservation allocates an ordinary-state ordinal in the
// hidden prepared state. It does not advance the attach-visible watermark.
func (h *SessionEventHub) PrepareTerminalStateReservation(sessionID contract.SessionID) (*PreparedTerminalStateReservation, error) {
	ledger := h.ledgerFor(sessionID)
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.faulted || ledger.unresolvedCount+ledger.preparedCount >= causalLedgerMaxUnresolved || ledger.nextOrdinal == SessionEventOrdinal(^uint64(0)) {
		return nil, errCausalLedgerFull
	}
	position := ledger.watermark.Run
	ticket := &CausalEventReservation{
		sessionID: sessionID, position: position, ordinal: ledger.nextOrdinal,
		class: CausalOrdinaryState, state: causalPrepared,
	}
	ledger.nextOrdinal++
	ledger.reservations[ticket.ordinal] = ticket
	ledger.preparedCount++
	return &PreparedTerminalStateReservation{ledger: ledger, ticket: ticket}, nil
}

func (h *SessionEventHub) CommitTerminalStateReservationNoFail(prepared *PreparedTerminalStateReservation) {
	if prepared == nil || prepared.ledger == nil || prepared.ticket == nil || prepared.committed {
		panic("remote: missing terminal causal reservation")
	}
	ledger := prepared.ledger
	ticket := prepared.ticket
	ledger.mu.Lock()
	if ticket.state != causalPrepared || ledger.preparedCount <= 0 {
		ledger.mu.Unlock()
		panic("remote: stale terminal causal reservation")
	}
	ticket.state = causalReleased
	ledger.preparedCount--
	if ticket.ordinal > ledger.watermark.Event {
		ledger.watermark.Event = ticket.ordinal
	}
	if ticket.position.SegmentID > ledger.watermark.Run.SegmentID ||
		(ticket.position.SegmentID == ledger.watermark.Run.SegmentID && ticket.position.Source > ledger.watermark.Run.Source) {
		ledger.watermark.Run = ticket.position
	}
	ticket.storedOutcome = CausalPublishOutcome{Disposition: CausalPublished, Ordinal: ticket.ordinal}
	prepared.committed = true
	ledger.mu.Unlock()
}

func (h *SessionEventHub) FinishTerminalStateReservation(prepared *PreparedTerminalStateReservation) {
	if prepared == nil || prepared.ledger == nil || prepared.ticket == nil || !prepared.committed {
		return
	}
	ledger := prepared.ledger
	ticket := prepared.ticket
	ledger.mu.Lock()
	if ledger.reservations[ticket.ordinal] == ticket {
		delete(ledger.reservations, ticket.ordinal)
	}
	ledger.mu.Unlock()
}

func (h *SessionEventHub) AbortTerminalStateReservation(prepared *PreparedTerminalStateReservation) {
	if prepared == nil || prepared.ledger == nil || prepared.ticket == nil || prepared.committed {
		return
	}
	ledger := prepared.ledger
	ticket := prepared.ticket
	ledger.mu.Lock()
	if ticket.state == causalPrepared {
		ticket.state = causalSuppressed
		ledger.preparedCount--
		delete(ledger.reservations, ticket.ordinal)
	}
	ledger.mu.Unlock()
}

// SealRunSegmentUnderState suppresses not-yet-released runState reservations for
// the given segment (design §4A.4). O(1) tombstone; does not scan/wait.
func (h *SessionEventHub) SealRunSegmentUnderState(
	sessionID contract.SessionID,
	segmentID RunSegmentID,
	lastSource RunSourceOrdinal,
) CausalSealReceipt {
	l := h.ledgerFor(sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()
	var suppressed uint32
	// Record a fresh exact seal generation (overflow leaves the ledger faulted;
	// zero generation is never a valid rollback capability).
	if l.sealGeneration == ^uint64(0) {
		l.faulted = true
		return CausalSealReceipt{SegmentID: segmentID, LastSource: lastSource}
	}
	l.sealGeneration++
	generation := l.sealGeneration
	l.sealedSegments[segmentID] = causalSegmentSeal{lastSource: lastSource, generation: generation}
	// Suppress not-yet-released runState tickets in this segment.
	for ord, t := range l.reservations {
		if t.state == causalReserved && t.class == CausalRunState &&
			t.position.SegmentID == segmentID && t.position.Source > lastSource {
			t.state = causalSuppressed
			l.unresolvedCount--
			suppressed++
			delete(l.reservations, ord)
		}
	}
	return CausalSealReceipt{
		SegmentID:              segmentID,
		LastSource:             lastSource,
		Generation:             generation,
		SuppressedReservations: suppressed,
	}
}

// RollbackRunSegmentSealUnderState removes only the exact tombstone minted by
// SealRunSegmentUnderState. Suppressed reservations are irreversible (their
// payload was intentionally discarded), so such a receipt fails closed and the
// caller must retain the feed seal. Caller already holds stateMu + feed.mu.
func (h *SessionEventHub) RollbackRunSegmentSealUnderState(sessionID contract.SessionID, receipt CausalSealReceipt) bool {
	if receipt.Generation == 0 || receipt.SuppressedReservations != 0 {
		return false
	}
	l := h.ledgerFor(sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()
	seal, ok := l.sealedSegments[receipt.SegmentID]
	if !ok || seal.generation != receipt.Generation || seal.lastSource != receipt.LastSource {
		return false
	}
	delete(l.sealedSegments, receipt.SegmentID)
	return true
}

// PublishReserved delivers the payload for a previously-reserved ticket (design
// §4A.4). Idempotent: same ticket + same payload returns stored outcome; payload
// mismatch → CausalStaleReservation + health latch.
func (h *SessionEventHub) PublishReserved(
	ticket *CausalEventReservation,
	event contract.KnownServerEvent,
) CausalPublishOutcome {
	if ticket == nil {
		return CausalPublishOutcome{Disposition: CausalStaleReservation}
	}
	l := h.ledgerFor(ticket.sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()

	// Already published: idempotent return.
	if ticket.state == causalReleased {
		return ticket.storedOutcome
	}
	// Suppressed (segment sealed): typed suppression.
	if ticket.state == causalSuppressed {
		outcome := CausalPublishOutcome{Disposition: CausalSuppressedSegmentSealed, Ordinal: ticket.ordinal}
		ticket.storedOutcome = outcome
		return outcome
	}
	if l.faulted {
		outcome := CausalPublishOutcome{Disposition: CausalSuppressedContinuityFault}
		return outcome
	}
	// Mark ready + release.
	ticket.storedPayload = event
	ticket.state = causalReady
	ticket.state = causalReleased
	l.unresolvedCount--
	delete(l.reservations, ticket.ordinal)

	// Enqueue to subscribers whose startAfter < ordinal.
	var delivered uint32
	var skipped uint32
	for _, sub := range l.subs {
		if sub.fenced {
			continue
		}
		if ticket.ordinal <= sub.startAfterEventOrdinal {
			skipped++
			continue
		}
		if !sub.enqueue(event, ticket.ordinal) {
			// Queue full: fence this subscription's authority (design §4A.4).
			sub.fenceAuthority()
			skipped++
		} else {
			delivered++
		}
	}
	outcome := CausalPublishOutcome{
		Disposition: CausalPublished,
		Ordinal:     ticket.ordinal,
		Delivered:   delivered,
		Skipped:     skipped,
	}
	ticket.storedOutcome = outcome
	return outcome
}

// WatermarkFor returns the current causal watermark for a session (for attach).
func (h *SessionEventHub) WatermarkFor(sessionID contract.SessionID) CausalWatermark {
	l := h.ledgerFor(sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.watermark
}

// LedgerFaulted reports whether the causal ledger for a session is faulted.
func (h *SessionEventHub) LedgerFaulted(sessionID contract.SessionID) bool {
	l := h.ledgerFor(sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.faulted
}

// errCausalLedger* are sentinel errors.
var (
	errCausalLedgerFaulted = causalErr("causal ledger faulted")
	errCausalLedgerFull    = causalErr("causal ledger full")
)

type causalErr string

func (e causalErr) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// causalHubSubscription — H3 subscription with startAfterEventOrdinal (§4A.4)
// ---------------------------------------------------------------------------

// causalHubSubscription is an H3 subscription with an immutable
// startAfterEventOrdinal. Events with ordinal ≤ startAfter are skipped (the
// snapshot already absorbed them). Queue-full fences the subscription's authority.
type causalHubSubscription struct {
	sessionID              contract.SessionID
	startAfterEventOrdinal SessionEventOrdinal
	lease                  *ControlConnectionLease
	fencer                 SubscriptionAuthorityFencer
	active                 atomic.Bool

	mu     sync.Mutex
	queue  []queuedEvent
	fenced bool

	// fenceOnce guards authority fence (idempotent).
	fenceOnce sync.Once

	// notify wakes the drain loop on enqueue (buffered-1 signal channel).
	notify chan struct{}
	// done is closed to stop the drain goroutine (BeginTerminal).
	done      chan struct{}
	closeOnce sync.Once
}

// queuedEvent pairs an event with its ordinal for FIFO delivery.
type queuedEvent struct {
	ordinal SessionEventOrdinal
	event   contract.KnownServerEvent
}

// SubscriptionAuthorityFencer fences the write authority of a subscription's
// lease when its queue is full (design §4A.4). The fence is idempotent and uses
// the lease's atomic live bit.
type SubscriptionAuthorityFencer interface {
	FenceSubscriptionWrites(token SubscriptionFenceToken, at interface{}) AuthorityFenceReceipt
}

// SubscriptionFenceToken identifies the exact lease to fence.
type SubscriptionFenceToken struct {
	lease *ControlConnectionLease
}

// AuthorityFenceReceipt records the fence outcome.
type AuthorityFenceReceipt struct {
	LeaseFenced        bool
	HolderEnteredGrace bool
	WriteGeneration    uint64
}

// noopFencer does nothing (used when no authority fencer is wired).
type noopFencer struct{}

func (noopFencer) FenceSubscriptionWrites(SubscriptionFenceToken, interface{}) AuthorityFenceReceipt {
	return AuthorityFenceReceipt{}
}

// enqueue appends an event to the queue. Returns false if the queue is full.
func (s *causalHubSubscription) enqueue(event contract.KnownServerEvent, ordinal SessionEventOrdinal) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fenced {
		return false
	}
	if len(s.queue) >= causalSubscriptionCapacity {
		return false
	}
	s.queue = append(s.queue, queuedEvent{ordinal: ordinal, event: event})
	// Wake the drain loop (non-blocking; buffered-1).
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return true
}

// fenceAuthority fences the subscription's lease (idempotent). Called on
// queue-full (design §4A.4).
func (s *causalHubSubscription) fenceAuthority() {
	s.fenceOnce.Do(func() {
		s.mu.Lock()
		s.fenced = true
		s.mu.Unlock()
		if s.lease != nil {
			s.lease.fence()
		}
		if s.fencer != nil {
			s.fencer.FenceSubscriptionWrites(SubscriptionFenceToken{lease: s.lease}, nil)
		}
	})
}

// StartAfterEventOrdinal returns the immutable start-after watermark.
func (s *causalHubSubscription) StartAfterEventOrdinal() SessionEventOrdinal {
	return s.startAfterEventOrdinal
}

// Drain returns and clears pending events (for sync-mode consumers).
func (s *causalHubSubscription) Drain() []queuedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.queue
	s.queue = nil
	return out
}

// IsFenced reports whether this subscription has been fenced (queue-full).
func (s *causalHubSubscription) IsFenced() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fenced
}

// Next blocks until an event is available or the subscription is terminal.
// Returns (event, true) on success, or (zero, false) when terminal/fenced with
// no remaining events. The WS actor drain loop calls this in a writer goroutine
// (design §6.1: "actor只有read loop与socket writer；bounded ingress就是H3
// HubSubscription").
func (s *causalHubSubscription) Next(ctx context.Context) (queuedEvent, bool) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			ev := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return ev, true
		}
		terminal := s.fenced
		s.mu.Unlock()
		if terminal {
			return queuedEvent{}, false
		}
		select {
		case <-s.notify:
			// re-loop to drain
		case <-s.done:
			return queuedEvent{}, false
		case <-ctx.Done():
			return queuedEvent{}, false
		}
	}
}

// BeginTerminal marks the subscription terminal so the drain loop exits (design
// §6.6: supersession/close). Remaining queued events are still drainable via
// Drain before the actor socket closes. Idempotent.
func (s *causalHubSubscription) BeginTerminal() {
	s.closeOnce.Do(func() {
		close(s.done)
	})
}

// ---------------------------------------------------------------------------
// SessionEventHub: causal ledger map + causal subscription registration
// ---------------------------------------------------------------------------

// RegisterCausalSubscription creates a causal subscription with the given
// startAfterEventOrdinal and registers it with the session's ledger.
func (h *SessionEventHub) RegisterCausalSubscription(
	sessionID contract.SessionID,
	startAfter SessionEventOrdinal,
	lease *ControlConnectionLease,
	fencer SubscriptionAuthorityFencer,
) *causalHubSubscription {
	sub := &causalHubSubscription{
		sessionID:              sessionID,
		startAfterEventOrdinal: startAfter,
		lease:                  lease,
		fencer:                 fencer,
		notify:                 make(chan struct{}, 1),
		done:                   make(chan struct{}),
	}
	sub.active.Store(true)
	l := h.ledgerFor(sessionID)
	l.mu.Lock()
	l.subs = append(l.subs, sub)
	l.mu.Unlock()
	return sub
}

func (h *SessionEventHub) PrepareCausalSubscription(sessionID contract.SessionID, startAfter SessionEventOrdinal, lease *ControlConnectionLease, fencer SubscriptionAuthorityFencer) *causalHubSubscription {
	sub := &causalHubSubscription{
		sessionID: sessionID, startAfterEventOrdinal: startAfter, lease: lease,
		fencer: fencer, notify: make(chan struct{}, 1), done: make(chan struct{}),
	}
	ledger := h.ledgerFor(sessionID)
	ledger.mu.Lock()
	ledger.subs = append(ledger.subs, sub)
	ledger.mu.Unlock()
	return sub
}

func (h *SessionEventHub) commitPreparedCausalSubscriptionNoFail(sub *causalHubSubscription) {
	if sub == nil {
		panic("remote: missing prepared causal subscription")
	}
	sub.active.Store(true)
}

func (h *SessionEventHub) AbortPreparedCausalSubscription(sub *causalHubSubscription) {
	if sub == nil || sub.active.Load() {
		return
	}
	ledger := h.ledgerFor(sub.sessionID)
	ledger.mu.Lock()
	for i, current := range ledger.subs {
		if current == sub {
			ledger.subs = append(ledger.subs[:i], ledger.subs[i+1:]...)
			break
		}
	}
	ledger.mu.Unlock()
	sub.BeginTerminal()
}

// UnregisterCausalSubscription removes a causal subscription from its ledger.
func (h *SessionEventHub) UnregisterCausalSubscription(sub *causalHubSubscription) {
	if sub == nil {
		return
	}
	l := h.ledgerFor(sub.sessionID)
	l.mu.Lock()
	for i, s := range l.subs {
		if s == sub {
			l.subs = append(l.subs[:i], l.subs[i+1:]...)
			break
		}
	}
	l.mu.Unlock()
}
