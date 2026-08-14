package remote

// control_causal.go — H0 neutral causal types + holderGeneration (design §4A.1).
//
// These types are INTERNAL, unexported, and never serialized to wire. They form
// the neutral causal port contract consumed by H1 (RunSegmentCommitter) and H3
// (causal ledger), without creating a concrete dependency between them (design
// §4A.5: H0 neutral contracts → {H1,H2,H3} → M2). H1 uses the
// SessionCausalReservationPort interface + a fake; H3 implements the concrete
// ledger. Production wiring composes both at the runtime level only.
//
// Lock order (design §9.1): the legal nesting for causal reservation is
// stateMu → feed.mu → causal-ledger.mu (the "three-lock domain"). Hub → state
// is NEVER allowed.

import (
	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Causal ordinal types (design §4A.1)
// ---------------------------------------------------------------------------

// RunSegmentID identifies a contiguous run segment within a session. Segment 1
// is the first activation; each restart boundary starts a new segment. Process-
// level monotonic per session; never serialized.
type RunSegmentID uint64

// RunSourceOrdinal is the per-segment monotonic ordinal of an observation record
// within the LiveRunContinuityFeed. Allocated under the unified commit domain.
type RunSourceOrdinal uint64

// SessionEventOrdinal is the per-session monotonic ordinal of an event in the
// H3 causal ledger. Reserved at H1 commit time (under the three-lock domain),
// released by the pump via PublishReserved.
type SessionEventOrdinal uint64

// CausalProjectionClass classifies which projection stream a causal reservation
// belongs to (design §4A.1). Only determines ordinal reservation semantics;
// never wire-visible.
type CausalProjectionClass uint8

const (
	// CausalReplay is for output records and restart boundaries (they consume a
	// v1 Seq and are replay-able).
	CausalReplay CausalProjectionClass = iota + 1
	// CausalRunState is for the runActivated/exit normal-state events (no v1 Seq).
	CausalRunState
	// CausalOrdinaryState is for ordinary control/state transitions.
	CausalOrdinaryState
)

// RunCausalPosition identifies a record's position within a run segment:
// {segmentID, sourceOrdinal}. Used for attach expected-position matching.
type RunCausalPosition struct {
	SegmentID RunSegmentID
	Source    RunSourceOrdinal
}

// CausalWatermark is the attach causal cut: the highest reserved Run position
// and Event ordinal at attach time. Captured atomically with the FiveLayer
// snapshot in the same stateMu→hub domain (design §4A.4, §6.3).
type CausalWatermark struct {
	Run   RunCausalPosition
	Event SessionEventOrdinal
}

// ---------------------------------------------------------------------------
// HolderGeneration (design §4A.1, §4.3 INV-03)
// ---------------------------------------------------------------------------

// HolderGeneration tracks device-holder tenure. It advances monotonically on
// none→holder, holder→none (release/expiry/takeover/revoke/Stop/remove), or
// authority-identity change. It does NOT advance on connected↔grace transitions
// or same-device rebind / idempotent acquire (design §4A.1).
//
// Lifecycle operations exact-match on holderGeneration: a fence writes a closed
// outcome for the old generation and ignores stale completions. This prevents
// ABA revival where the same DeviceID reacquires after release (design §5.6).
type HolderGeneration uint64

// LifecycleClosedReason is the reason a lifecycle intent was closed before its
// raw effect committed (design §4A.1).
type LifecycleClosedReason uint8

const (
	LifecycleClosedRelease LifecycleClosedReason = iota + 1
	LifecycleClosedExpiry
	LifecycleClosedTakeover
	LifecycleClosedRevoke
	LifecycleClosedServerStop
	LifecycleClosedRemove
	LifecycleClosedAborted
)

// closedOutcomeReason maps a LifecycleClosedReason to a human-readable label
// for diagnostics (never wire).
func (r LifecycleClosedReason) String() string {
	switch r {
	case LifecycleClosedRelease:
		return "release"
	case LifecycleClosedExpiry:
		return "expiry"
	case LifecycleClosedTakeover:
		return "takeover"
	case LifecycleClosedRevoke:
		return "revoke"
	case LifecycleClosedServerStop:
		return "server_stop"
	case LifecycleClosedRemove:
		return "remove"
	case LifecycleClosedAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// CausalEventReservation (design §4A.1, §4A.4)
// ---------------------------------------------------------------------------

// CausalEventReservation is an opaque ticket minted by the H3 causal ledger at
// H1 commit time (under the three-lock domain). It carries the exact session,
// run causal position, reserved event ordinal, and projection class. The pump
// uses PublishReserved(ticket, event) to deliver the payload; it cannot mint
// ordinals itself (design §4A.2).
//
// A reservation transitions: reserved → ready (payload provided) → released
// (enqueued to subscriber queue), or → suppressed (segment sealed / stale).
type CausalEventReservation struct {
	sessionID contract.SessionID
	position  RunCausalPosition
	ordinal   SessionEventOrdinal
	class     CausalProjectionClass
	state     causalReservationState
	// storedPayload holds the published event for idempotent retry.
	storedPayload contract.KnownServerEvent
	// storedOutcome holds the disposition after publish, for idempotent retry.
	storedOutcome CausalPublishOutcome
}

// causalReservationState tracks the reservation lifecycle.
type causalReservationState uint8

const (
	causalPrepared causalReservationState = iota + 1
	causalReserved
	causalReady
	causalReleased
	causalSuppressed
)

// SessionID returns the session this reservation belongs to.
func (r *CausalEventReservation) SessionID() contract.SessionID { return r.sessionID }

// Position returns the run causal position.
func (r *CausalEventReservation) Position() RunCausalPosition { return r.position }

// Ordinal returns the reserved event ordinal.
func (r *CausalEventReservation) Ordinal() SessionEventOrdinal { return r.ordinal }

// Class returns the projection class.
func (r *CausalEventReservation) Class() CausalProjectionClass { return r.class }

// ---------------------------------------------------------------------------
// CausalSealReceipt + publish disposition (design §4A.1, §4A.2)
// ---------------------------------------------------------------------------

// CausalSealReceipt is returned by SealRunSegmentUnderState. It records the
// sealed segment, last source ordinal, and the count of suppressed (not-yet-
// released runState) reservations (design §4A.2).
type CausalSealReceipt struct {
	SegmentID  RunSegmentID
	LastSource RunSourceOrdinal
	// Generation exact-matches one concrete seal tombstone. A rollback carrying
	// an older generation cannot unseal a later restart of the same segment.
	Generation             uint64
	SuppressedReservations uint32
}

// CausalPublishDisposition is the typed outcome of PublishReserved (design §4A.1).
type CausalPublishDisposition uint8

func (d CausalPublishDisposition) String() string {
	switch d {
	case CausalPublished:
		return "Published"
	case CausalSkippedBeforeStart:
		return "SkippedBeforeStart"
	case CausalSuppressedSegmentSealed:
		return "SuppressedSegmentSealed"
	case CausalSuppressedContinuityFault:
		return "SuppressedContinuityFault"
	case CausalStaleReservation:
		return "StaleReservation"
	default:
		return "Unknown"
	}
}

const (
	// CausalPublished: the ticket was released and enqueued to the subscriber queue.
	CausalPublished CausalPublishDisposition = iota + 1
	// CausalSkippedBeforeStart: the ticket's ordinal ≤ subscription startAfter;
	// the snapshot already absorbed it. Not enqueued.
	CausalSkippedBeforeStart
	// CausalSuppressedSegmentSealed: the segment was sealed before release.
	CausalSuppressedSegmentSealed
	// CausalSuppressedContinuityFault: the feed/ledger is in a faulted state.
	CausalSuppressedContinuityFault
	// CausalStaleReservation: the ticket is stale (session/run mismatch).
	CausalStaleReservation
)

// CausalPublishOutcome is the result of PublishReserved. It carries the
// disposition and the event ordinal; no payload or identity (design §4A.2).
type CausalPublishOutcome struct {
	Disposition CausalPublishDisposition
	Ordinal     SessionEventOrdinal
	// Delivered/Skipped are diagnostic counters (no payload/identity).
	Delivered uint32
	Skipped   uint32
}

// ---------------------------------------------------------------------------
// SessionCausalReservationPort — H0 neutral seam (design §4A.1, §4A.5)
//
// H1 (RunSegmentCommitter) consumes this interface. In production H3 implements
// it concretely; in standalone H1 tests a fake implements it. This keeps H1 and
// H3 independently compilable/auditable (design §4A.5).
// ---------------------------------------------------------------------------

// SessionCausalReservationPort is the neutral causal reservation seam. The
// caller MUST already hold stateMu (and feed.mu) when invoking these methods —
// they perform only O(1) capacity/ordinal/ticket mutation under the causal
// ledger lock, no validate/marshal/release/fence/callback (design §4A.4).
type SessionCausalReservationPort interface {
	// ReserveRunRecordUnderState reserves an event ordinal for a run record
	// (output/exit/boundary/runActivated). Returns an opaque ticket or an error
	// if the ledger is full/faulted (caller fails closed).
	ReserveRunRecordUnderState(
		sessionID contract.SessionID,
		position RunCausalPosition,
		class CausalProjectionClass,
	) (*CausalEventReservation, error)
	// SealRunSegmentUnderState suppresses not-yet-released runState reservations
	// for the given segment. O(1) tombstone; does not scan/wait for pump.
	SealRunSegmentUnderState(
		sessionID contract.SessionID,
		segmentID RunSegmentID,
		lastSource RunSourceOrdinal,
	) CausalSealReceipt
	// RollbackRunSegmentSealUnderState removes only the exact seal represented
	// by receipt. It fails closed when the seal has suppressed a reservation or
	// has already been superseded; callers then keep the feed sealed.
	RollbackRunSegmentSealUnderState(
		sessionID contract.SessionID,
		receipt CausalSealReceipt,
	) bool
}

// SessionCausalPublicationPort is the neutral publish seam consumed by the M2
// pump. PublishReserved delivers the payload for a previously-reserved ticket.
type SessionCausalPublicationPort interface {
	PublishReserved(
		ticket *CausalEventReservation,
		event contract.KnownServerEvent,
	) CausalPublishOutcome
}

// ---------------------------------------------------------------------------
// LifecycleIntent (design §4A.1)
// ---------------------------------------------------------------------------

// LifecycleIntentKind is the kind of lifecycle intent (stop/remove/restart).
type LifecycleIntentKind uint8

const (
	IntentStop LifecycleIntentKind = iota + 1
	IntentRemove
	IntentRestart
)

// lifecycleIntentID is the unique, monotonically increasing intent identifier,
// allocated under stateMu (design §4A.1: "递增占用唯一intent ID").
type lifecycleIntentID uint64

// lifecycleClosedOutcome records that an intent was closed by a fence before its
// raw effect committed, with the generation at close time (design §4A.1, §5.6).
type lifecycleClosedOutcome struct {
	reason     LifecycleClosedReason
	generation HolderGeneration
}

// isClosed reports whether the intent has a closed outcome (fence wrote it).
