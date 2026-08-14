package remote

// control_arbiter.go — ControlArbiter: per-session control holder state machine
// (design §4.2, §4.4, §4.5, §5, §7, §9).
//
// The arbiter is the SOLE authority for holder state, control/run/backend
// generations, grace timers, and session tombstones. It never performs raw I/O,
// never waits for an operation lane or done channel under stateMu, and never
// calls back into registry/Server/socket from within stateMu.
//
// Lock order (design §9.3):
//
//	tableMu            → entry pointer lookup/insert/delete only; NEVER waits
//	                     for stateMu; pointer fetched then released.
//	controlEntry.stateMu → holder/run/backend/op metadata + event reservation;
//	                     NEVER waits for opLane/done; NEVER raw I/O/socket/
//	                     registry/Server/durable sink.
//	SessionEventHub.mu → may nest UNDER stateMu (stateMu → hub); NO hub → state.
//	permitMu           → pending permit index + cancel snapshot; NEVER locks
//	                     entry; NEVER executes compensation/raw.
//
// The arbiter's state-commit methods produce internal controlTransition records
// and enqueue them to the in-memory hub. All transitions are fail-closed: a
// nil/unready gate, missing entry, unknown state, or stale epoch yields a
// denial with zero side effects.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// lifecycleDrainIntent — two-phase stop/remove/restart (design §9.1.2)
// ---------------------------------------------------------------------------

// lifecycleDrainIntent is committed under stateMu in phase 1 of a lifecycle
// operation. It fences regular writes and observations WITHOUT overwriting an
// existing currentOp. Phase 2 acquires the lane and exact-matches the intent to
// mint a drain permit.
//
// H1/§4A.1: the intent now exact-matches on holderGeneration in addition to
// id/kind/run, so a fence (release/expiry/takeover/revoke/Stop) that advances
// generation writes a typed closed outcome and the old phase-2/checkpoint/raw/
// commit are all no-ops (ABA defense, design §5.6).
type lifecycleDrainIntent struct {
	id               uint64
	run              *runIdentity
	runEpoch         uint64
	kind             LifecycleOperation
	holderGeneration HolderGeneration
	// closed is set when a fence closes this intent before its raw effect
	// committed. A closed intent must NOT clear a replacement intent or commit a
	// stale result (design §4A.1, §5.6).
	closed *lifecycleClosedOutcome
}

// ---------------------------------------------------------------------------
// controlEntry — per-session state (design §4.2)
// ---------------------------------------------------------------------------

// controlEntry holds all per-session arbitration state. stateMu protects every
// field below it. stateMu is NEVER held across raw I/O, lane waits, or done
// channel receives.
type controlEntry struct {
	stateMu sync.Mutex

	sessionID    contract.SessionID
	owner        controlOwner
	controlEpoch uint64

	// Run identity (design §4.2, §6.4). currentRun is pointer-exact-matched;
	// runEpoch is the monotonic run counter. Both must match for a valid permit.
	currentRun *runIdentity
	runEpoch   uint64
	// runEpochSeq is the monotonic allocation frontier. A restart stage reserves
	// and consumes an epoch here without publishing it through runEpoch/currentRun;
	// failed stages therefore cannot reuse an epoch (ABA defense).
	runEpochSeq uint64
	runPhase    runPhase

	// holderGeneration tracks device-holder tenure (design §4A.1, §4.3 INV-03).
	// Advances monotonically on none→holder, holder→none, or authority-identity
	// change. Does NOT advance on connected↔grace or same-device rebind.
	holderGeneration HolderGeneration
	// lifecycleIntentSeq is the monotonic counter for unique lifecycle intent IDs
	// (design §4A.1: "递增占用唯一intent ID").
	lifecycleIntentSeq uint64

	// Operation lane + current operation (design §9.1.2).
	opLane *boundedOperationLane
	// currentOp is read/written ONLY under stateMu. The operation itself runs
	// OUTSIDE stateMu (in the lane).
	currentOp    *operationPermit
	operationSeq uint64
	pendingDrain *lifecycleDrainIntent

	// Backend health (design §9.1.3).
	backendEpoch  uint64
	backend       backendHealth
	backendDetach backendDetachRecord

	// Tombstone (design §4.2).
	removed bool

	// Grace timer descriptor + handle (design §7.2).
	graceDesc  graceTimerDescriptor
	graceTimer securityTimer

	// Session state mirror (design §4.3). Updated by controlled commands and
	// validated run observations under stateMu. Read atomically with
	// owner/runPhase for wire DTO assembly.
	stateMirror    contract.SessionState
	stateMirrorSet bool

	initialStage              *initialRunStage
	preparedActivation        *PreparedCompositeActivation
	preparedRestartActivation *PreparedCompositeRestart
	preparedRestart           *PreparedControlRestart
	preparedStop              *PreparedControlStop
	preparedRemoval           *PreparedControlRemove
}

// ---------------------------------------------------------------------------
// ControlArbiter (design §4.2)
// ---------------------------------------------------------------------------

// ControlArbiter is the per-session control holder state authority. It owns:
//   - the entry table (tableMu-protected map of SessionID → *controlEntry)
//   - global generations (acceptance/runtime/launch)
//   - the pending launch permit index (permitMu-protected)
//   - the revoked device set (permitMu-protected)
type ControlArbiter struct {
	tableMu sync.RWMutex
	entries map[contract.SessionID]*controlEntry

	// Global readiness + acceptance (design §4.2).
	ready           atomic.Bool
	acceptingRemote atomic.Bool

	// Global generations (design §5.3). These are atomic to allow lock-free
	// reads from Checkpoint and launch validation.
	acceptanceGeneration atomic.Uint64 // remote Start/Stop/latch fence
	runtimeGeneration    atomic.Uint64 // app shutdown / arbiter close fence
	launchGeneration     atomic.Uint64 // per-LaunchPermit unique

	// Pending launch permit index + revoked device set (design §4.2, §6.4).
	// permitMu NEVER locks an entry and NEVER executes compensation/raw.
	permitMu        sync.Mutex
	pendingLaunch   map[uint64]*LaunchPermit
	pendingByDevice map[contract.DeviceID]map[uint64]*LaunchPermit
	revokedDevices  map[contract.DeviceID]bool

	// Dependencies (injected).
	clock     Clock
	hub       *SessionEventHub
	projector ControlProjector
	directory *AttachmentDirectory
	grace     time.Duration

	// healthLatched is set when the arbiter enters a fail-closed health state
	// (transition budget overflow, epoch overflow, event core failure).
	healthLatched atomic.Bool
}

// NewControlArbiter creates an arbiter in the not-ready, not-accepting state.
// clock MUST be non-nil (production uses systemClock; tests inject a fake).
func NewControlArbiter(clock Clock, hub *SessionEventHub, directory *AttachmentDirectory) *ControlArbiter {
	if clock == nil {
		panic("control: NewControlArbiter requires non-nil Clock")
	}
	return &ControlArbiter{
		entries:         make(map[contract.SessionID]*controlEntry),
		pendingLaunch:   make(map[uint64]*LaunchPermit),
		pendingByDevice: make(map[contract.DeviceID]map[uint64]*LaunchPermit),
		revokedDevices:  make(map[contract.DeviceID]bool),
		clock:           clock,
		hub:             hub,
		projector:       NewControlProjector(),
		directory:       directory,
		grace:           controlGraceDuration,
	}
}

// SetGraceDuration overrides the disconnect grace period (testing / C-004).
func (a *ControlArbiter) SetGraceDuration(d time.Duration) {
	a.grace = d
}

// MarkReady enables the arbiter and starts accepting remote control.
func (a *ControlArbiter) MarkReady() {
	a.ready.Store(true)
	a.acceptingRemote.Store(true)
}

// IsReady reports whether the arbiter is ready.
func (a *ControlArbiter) IsReady() bool { return a.ready.Load() }

// IsAcceptingRemote reports whether remote acquisition is currently accepted.
func (a *ControlArbiter) IsAcceptingRemote() bool { return a.acceptingRemote.Load() }

// IsHealthLatched reports whether the arbiter is in a fail-closed health state.
func (a *ControlArbiter) IsHealthLatched() bool { return a.healthLatched.Load() }

// AcceptanceGeneration returns the current acceptance generation (diagnostic).
func (a *ControlArbiter) AcceptanceGeneration() uint64 { return a.acceptanceGeneration.Load() }

// RuntimeGeneration returns the current runtime generation (diagnostic).
func (a *ControlArbiter) RuntimeGeneration() uint64 { return a.runtimeGeneration.Load() }

// ---------------------------------------------------------------------------
// Entry lookup (tableMu)
// ---------------------------------------------------------------------------

// entryFor returns the entry pointer for the session, or nil if not found.
// The caller MUST NOT hold tableMu when dereferencing the entry's fields under
// stateMu — fetch pointer under tableMu, release, then lock stateMu.
func (a *ControlArbiter) entryFor(id contract.SessionID) *controlEntry {
	a.tableMu.RLock()
	defer a.tableMu.RUnlock()
	return a.entries[id]
}

// isEntryPublic reports whether the entry is visible to remote callers.
// A staging entry (runPhase == starting) or a tombstone is NOT public.
func isEntryPublic(e *controlEntry) bool {
	if e == nil || e.removed {
		return false
	}
	return e.runPhase == runActive || e.runPhase == runTerminal
}

// ---------------------------------------------------------------------------
// Gate readiness check (fail-closed)
// ---------------------------------------------------------------------------

// checkReady returns a ControlGateError if the arbiter is not ready/accepting
// or is health-latched.
func (a *ControlArbiter) checkReady() *ControlGateError {
	if !a.ready.Load() || a.healthLatched.Load() {
		return &ControlGateError{Kind: DenyControlUnavailable}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Overflow-safe epoch increment (design §4.2: "到最大值时不得wrap")
// ---------------------------------------------------------------------------

// nextEpoch increments n and returns (newValue, true). On overflow it returns
// (maxUint64, false), signaling the caller to latch fail-closed.
func nextEpoch(n uint64) (uint64, bool) {
	if n == ^uint64(0) {
		return n, false
	}
	return n + 1, true
}

// ---------------------------------------------------------------------------
// State commit helper: commit a transition under stateMu and enqueue to hub.
// Returns false if epoch overflow forces a health latch (caller rolls back).
// ---------------------------------------------------------------------------

// commitTransition sets the new owner, increments controlEpoch, and enqueues the
// transition to the hub. Caller MUST hold entry.stateMu. Returns false on epoch
// overflow (caller latches health and rolls back).
//
// H1/§4A.1: every wire-visible holder change (the only callers of this method)
// closes any pending old-generation lifecycle intent and advances
// holderGeneration. This is the ABA defense: a release→reacquire of the same
// DeviceID produces a new generation, so an old in-flight lifecycle intent
// (stop/remove/restart waiting on the lane) receives a typed closed outcome and
// its phase-2/checkpoint/raw/commit are all no-ops (design §5.6).
//
// The transition carries a monotonic controlEpoch (design §5.3, §8 event
// ordering). The hub validates the projected event via contract.ValidateServerEvent
// (design §8.1); a producer never-event latches the gate via the hub's
// onValidationError callback.
func (a *ControlArbiter) commitTransition(
	entry *controlEntry,
	newOwner controlOwner,
	reason controlTransitionReason,
	now time.Time,
) bool {
	if entry.preparedRemoval != nil {
		return false
	}
	// Close any pending lifecycle intent for the OLD generation before advancing.
	// The intent's phase-2/checkpoint/result exact-match will fail on generation
	// mismatch, yielding a typed closed outcome (design §4A.1, §5.6).
	a.closeLifecycleIntentLocked(entry, closedReasonFromTransition(reason))
	// Advance holderGeneration (monotonic; overflow → health latch).
	newGen, ok := nextEpoch(uint64(entry.holderGeneration))
	if !ok {
		a.healthLatched.Store(true)
		return false
	}
	entry.holderGeneration = HolderGeneration(newGen)

	newEpoch, ok := nextEpoch(entry.controlEpoch)
	if !ok {
		a.healthLatched.Store(true)
		return false
	}
	t := controlTransition{
		sessionID:    entry.sessionID,
		oldOwner:     entry.owner,
		newOwner:     newOwner,
		reason:       reason,
		occurredAt:   now,
		controlEpoch: newEpoch,
	}
	entry.owner = newOwner
	entry.controlEpoch = newEpoch
	if a.hub != nil {
		a.hub.EnqueueControlTransition(t)
	}
	return true
}

// closeLifecycleIntentLocked marks the current pending lifecycle intent as closed
// for its generation, so a subsequent phase-2 exact-match (which compares
// holderGeneration) yields a typed denial rather than committing a stale result.
// Caller MUST hold entry.stateMu. The intent pointer is NOT cleared — the
// replacement (or abort) clears it; a closed old-generation intent must not
// clear a new-generation intent (design §4A.1).
func (a *ControlArbiter) closeLifecycleIntentLocked(entry *controlEntry, reason LifecycleClosedReason) {
	if entry.pendingDrain == nil || entry.pendingDrain.closed != nil {
		return
	}
	entry.pendingDrain.closed = &lifecycleClosedOutcome{
		reason:     reason,
		generation: entry.holderGeneration,
	}
}

// closedReasonFromTransition maps a controlTransitionReason to the corresponding
// LifecycleClosedReason for intent closing.
func closedReasonFromTransition(reason controlTransitionReason) LifecycleClosedReason {
	switch reason {
	case reasonReleased:
		return LifecycleClosedRelease
	case reasonConnectionExpired:
		return LifecycleClosedExpiry
	case reasonTakeover:
		return LifecycleClosedTakeover
	case reasonDeviceRevoked:
		return LifecycleClosedRevoke
	case reasonServiceStopped:
		return LifecycleClosedServerStop
	case reasonSecurityUnavailable:
		return LifecycleClosedServerStop
	case reasonSessionRemoved:
		return LifecycleClosedRemove
	default:
		return LifecycleClosedAborted
	}
}

// validateSnapshotOrLatch validates the projected wire snapshot via the frozen
// contract validator (design §8.2: audience projection through
// contract.ValidateControlSnapshot). This is a defense-in-depth never-event (the
// projector is deterministic); on failure the gate is latched fail-closed per
// §8.1. Returns true if the snapshot is valid.
func (a *ControlArbiter) validateSnapshotOrLatch(snap contract.ControlSnapshot) bool {
	if verr := contract.ValidateControlSnapshot(snap); verr != nil {
		a.healthLatched.Store(true)
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Acquire (design §4.5: none → device)
// ---------------------------------------------------------------------------

// Acquire attempts to grant control of sessionID to the authenticated device
// principal via the given authoritative lease. Returns the resulting snapshot
// relative to the principal, or a ControlGateError on denial.
//
// Linearization point: under entry.stateMu, event reservation + none→device +
// controlEpoch increment (design §9.2).
func (a *ControlArbiter) Acquire(
	principal DevicePrincipal,
	lease *ControlConnectionLease,
	sessionID contract.SessionID,
) (contract.ControlSnapshot, *ControlGateError) {
	if err := a.checkReady(); err != nil {
		return contract.ControlSnapshot{}, err
	}
	if !a.acceptingRemote.Load() {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyNotAccepting}
	}
	// Validate lease: must be live and match the principal.
	if lease == nil || !lease.IsLive() {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyNoAuthoritativeAttachment}
	}
	if lease.DeviceID() != principal.DeviceID {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyNoAuthoritativeAttachment}
	}
	a.permitMu.Lock()
	revoked := a.revokedDevices[principal.DeviceID]
	a.permitMu.Unlock()
	if revoked {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyDeviceRevoked}
	}

	entry := a.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenySessionNotFound}
	}

	now := a.clock.Now()
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()

	if entry.removed {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenySessionNotFound}
	}

	switch entry.owner.kind {
	case ownerNone:
		// Grant control.
		newOwner := controlOwner{
			kind:                 ownerDevice,
			deviceID:             principal.DeviceID,
			deviceName:           principal.DeviceName,
			connectionID:         lease.ConnectionID(),
			attachmentGeneration: lease.AttachmentGeneration(),
			phase:                deviceConnected,
		}
		if !a.commitTransition(entry, newOwner, reasonAcquired, now) {
			return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyControlUnavailable}
		}
		snap := a.projector.SnapshotForViewer(entry.owner, principal.DeviceID)
		if !a.validateSnapshotOrLatch(snap) {
			return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyControlUnavailable}
		}
		return snap, nil

	case ownerDevice:
		if entry.owner.deviceID == principal.DeviceID {
			// Idempotent rebind: same device re-acquires. Refresh connection
			// identity, increment epoch. No wire state change (still "device"
			// for all viewers) → no event.
			newEpoch, ok := nextEpoch(entry.controlEpoch)
			if !ok {
				a.healthLatched.Store(true)
				return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyControlUnavailable}
			}
			entry.owner.connectionID = lease.ConnectionID()
			entry.owner.attachmentGeneration = lease.AttachmentGeneration()
			entry.owner.phase = deviceConnected
			entry.owner.graceDeadline = time.Time{}
			entry.controlEpoch = newEpoch
			// Cancel any pending grace timer.
			a.cancelGraceTimerLocked(entry)
			snap := a.projector.SnapshotForViewer(entry.owner, principal.DeviceID)
			if !a.validateSnapshotOrLatch(snap) {
				return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyControlUnavailable}
			}
			return snap, nil
		}
		// Different device already holds → busy.
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyBusy}

	default:
		// desktop holds → busy.
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyBusy}
	}
}

// ---------------------------------------------------------------------------
// Release (design §4.5: device → none)
// ---------------------------------------------------------------------------

// Release releases control if the requesting device is the current holder.
// Grace holders of the same device can also release.
func (a *ControlArbiter) Release(
	principal DevicePrincipal,
	sessionID contract.SessionID,
) (contract.ControlSnapshot, *ControlGateError) {
	if err := a.checkReady(); err != nil {
		return contract.ControlSnapshot{}, err
	}
	entry := a.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenySessionNotFound}
	}

	now := a.clock.Now()
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()

	if entry.removed {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenySessionNotFound}
	}
	if entry.owner.kind != ownerDevice || entry.owner.deviceID != principal.DeviceID {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyNotController}
	}
	// Release: holder → none.
	a.cancelGraceTimerLocked(entry)
	if !a.commitTransition(entry, controlOwner{kind: ownerNone}, reasonReleased, now) {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyControlUnavailable}
	}
	snap := a.projector.SnapshotForViewer(entry.owner, principal.DeviceID)
	if !a.validateSnapshotOrLatch(snap) {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyControlUnavailable}
	}
	return snap, nil
}

// ---------------------------------------------------------------------------
// Desktop take / release (design §4.5, §5.2 INV-04)
// ---------------------------------------------------------------------------

// TakeDesktop atomically sets the desktop as holder, preempting any device
// holder. The authority must be non-nil. This NEVER waits for a blocked
// operation — it only fences the current operation's context (state-only).
func (a *ControlArbiter) TakeDesktop(
	authority *DesktopAuthority,
	sessionID contract.SessionID,
) *ControlGateError {
	if err := a.checkReady(); err != nil {
		return err
	}
	if authority == nil {
		return &ControlGateError{Kind: DenyControlUnavailable}
	}
	entry := a.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return &ControlGateError{Kind: DenySessionNotFound}
	}

	now := a.clock.Now()
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()

	if entry.removed {
		return &ControlGateError{Kind: DenySessionNotFound}
	}

	oldKind := entry.owner.kind
	newOwner := controlOwner{
		kind:          ownerDesktop,
		desktopSource: authority.source,
	}
	a.cancelGraceTimerLocked(entry)

	if oldKind == ownerDesktop {
		// Idempotent: refresh source, increment epoch. No wire state change.
		newEpoch, ok := nextEpoch(entry.controlEpoch)
		if !ok {
			a.healthLatched.Store(true)
			return &ControlGateError{Kind: DenyControlUnavailable}
		}
		entry.owner = newOwner
		entry.controlEpoch = newEpoch
		return nil
	}

	// Preempt device holder (connected or grace). Produce takeover event.
	if !a.commitTransition(entry, newOwner, reasonTakeover, now) {
		return &ControlGateError{Kind: DenyControlUnavailable}
	}
	// Fence the current operation (state-only: cancel ctx, mark fenced).
	// Does NOT wait for done.
	a.fenceCurrentOpLocked(entry)
	return nil
}

// ReleaseDesktop releases desktop control if the authority source matches the
// current desktop holder's source (or is the Wails root).
func (a *ControlArbiter) ReleaseDesktop(
	authority *DesktopAuthority,
	sessionID contract.SessionID,
) *ControlGateError {
	if err := a.checkReady(); err != nil {
		return err
	}
	if authority == nil {
		return &ControlGateError{Kind: DenyControlUnavailable}
	}
	entry := a.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return &ControlGateError{Kind: DenySessionNotFound}
	}

	now := a.clock.Now()
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()

	if entry.removed {
		return &ControlGateError{Kind: DenySessionNotFound}
	}
	if entry.owner.kind != ownerDesktop {
		return &ControlGateError{Kind: DenyNotController}
	}
	// Exact-source match: legacy disconnect only releases if source still
	// matches; a later Wails/legacy root must not be released by an old source.
	if entry.owner.desktopSource != authority.source {
		return &ControlGateError{Kind: DenyNotController}
	}
	if !a.commitTransition(entry, controlOwner{kind: ownerNone}, reasonReleased, now) {
		return &ControlGateError{Kind: DenyControlUnavailable}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Snapshot projection (design §8.2)
// ---------------------------------------------------------------------------

// SnapshotForDevice returns the audience-relative control snapshot for the
// given viewer. A viewer DeviceID of "" denotes desktop.
func (a *ControlArbiter) SnapshotForDevice(
	sessionID contract.SessionID,
	viewer contract.DeviceID,
) (contract.ControlSnapshot, *ControlGateError) {
	if err := a.checkReady(); err != nil {
		return contract.ControlSnapshot{}, err
	}
	entry := a.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenySessionNotFound}
	}
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenySessionNotFound}
	}
	snap := a.projector.SnapshotForViewer(entry.owner, viewer)
	if !a.validateSnapshotOrLatch(snap) {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyControlUnavailable}
	}
	return snap, nil
}

// ---------------------------------------------------------------------------
// Atomic attach-view (design §7.1 step 5, T-23)
// ---------------------------------------------------------------------------

// commitPreparedAttachView is the allocation-free final attach block. The
// directory node and both hidden subscriptions already exist; this method only
// flips stable pointers/live bits while stateMu excludes control transitions.
var errPreparedAttachUnavailable = &ControlGateError{Kind: DenyControlUnavailable}

func (a *ControlArbiter) commitPreparedAttachView(runtime *ControlRuntime, prepared *PreparedRemoteAttach) (contract.ControlSnapshot, *ControlConnectionLease, *ControlGateError) {
	if runtime == nil || prepared == nil || prepared.reservation == nil || prepared.handle == nil || prepared.finalFailure == nil {
		return contract.ControlSnapshot{}, nil, errPreparedAttachUnavailable
	}
	lease := prepared.reservation.Lease()
	entry := prepared.controlEntry
	if lease == nil || entry == nil || entry.sessionID != prepared.handle.sessionID {
		return contract.ControlSnapshot{}, nil, prepared.finalFailure
	}
	entry.stateMu.Lock()
	if !isEntryPublic(entry) || entry.removed || lease.fenced.Load() || entry.controlEpoch != prepared.expectedControlEpoch {
		entry.stateMu.Unlock()
		return contract.ControlSnapshot{}, nil, prepared.finalFailure
	}
	old, ok := runtime.directory.commitReservedAttachNoFail(prepared.reservation)
	if !ok {
		entry.stateMu.Unlock()
		return contract.ControlSnapshot{}, nil, prepared.finalFailure
	}
	runtime.hub.commitPreparedCausalSubscriptionNoFail(prepared.causalSub)
	runtime.hub.commitPreparedControlSubscriptionNoFail(prepared.controlSub)
	if prepared.rebindHolder {
		prepared.graceTimer = entry.graceTimer
		entry.graceTimer = nil
		entry.graceDesc = graceTimerDescriptor{}
		entry.owner.connectionID = lease.ConnectionID()
		entry.owner.attachmentGeneration = lease.AttachmentGeneration()
		entry.owner.phase = deviceConnected
		entry.owner.graceDeadline = time.Time{}
		entry.controlEpoch = prepared.reboundEpoch
	}
	snapshot := prepared.snapshot
	entry.stateMu.Unlock()
	return snapshot, old, nil
}

// AttachView atomically rebinds a grace holder (if applicable), invokes the
// subscribe callback under entry.stateMu, and returns the control snapshot —
// all under the same stateMu critical section. This guarantees no control
// transition is missed between subscription and snapshot (design §7.1 step 5,
// T-23: either the snapshot contains the new state or a subsequent event is
// delivered; never a gap).
//
// The subscribe callback MUST take hub.mu (the only lock allowed to nest under
// stateMu per design §9.3) and MUST NOT block on raw I/O or call back into the
// arbiter/registry. It receives the authenticated viewer DeviceID.
func (a *ControlArbiter) AttachView(
	sessionID contract.SessionID,
	lease *ControlConnectionLease,
	subscribe func(viewer contract.DeviceID),
) (contract.ControlSnapshot, *ControlGateError) {
	if err := a.checkReady(); err != nil {
		return contract.ControlSnapshot{}, err
	}
	if lease == nil || !lease.IsLive() {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyNoAuthoritativeAttachment}
	}
	entry := a.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenySessionNotFound}
	}
	now := a.clock.Now()
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenySessionNotFound}
	}
	// Step 4: rebind if this device is in grace for this session (no wire event;
	// holder wire state stays "device"). For a new observer this is a no-op.
	a.rebindAttachmentLocked(entry, lease, now)
	// Step 5: subscribe under stateMu, then snapshot. Both are atomic relative to
	// any state commit for this session (which also requires this stateMu).
	if subscribe != nil {
		subscribe(lease.DeviceID())
	}
	snap := a.projector.SnapshotForViewer(entry.owner, lease.DeviceID())
	if !a.validateSnapshotOrLatch(snap) {
		return contract.ControlSnapshot{}, &ControlGateError{Kind: DenyControlUnavailable}
	}
	return snap, nil
}

// ---------------------------------------------------------------------------
// Unexpected disconnect → grace (design §7.2)
// ---------------------------------------------------------------------------

// OnUnexpectedDetachForSession transitions a connected device holder to grace
// when its authoritative connection is unexpectedly lost. The lease's
// attachment generation and the entry's controlEpoch must match exactly — a
// stale detach (from a replaced connection) is a no-op.
//
// The A2 connection layer resolves the sessionID from the AttachmentDirectory
// (which maps (DeviceID, SessionID) → lease) before calling this method, then
// calls it with the resolved session + the fenced lease. It arms a grace timer.
func (a *ControlArbiter) OnUnexpectedDetachForSession(
	sessionID contract.SessionID,
	lease *ControlConnectionLease,
	at time.Time,
) {
	if lease == nil || a.healthLatched.Load() {
		return
	}
	entry := a.entryFor(sessionID)
	if entry == nil {
		return
	}
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()

	if entry.removed || entry.owner.kind != ownerDevice {
		return
	}
	if entry.owner.deviceID != lease.DeviceID() ||
		entry.owner.connectionID != lease.ConnectionID() ||
		entry.owner.attachmentGeneration != lease.AttachmentGeneration() {
		return
	}
	if entry.owner.phase != deviceConnected {
		return
	}

	newEpoch, ok := nextEpoch(entry.controlEpoch)
	if !ok {
		a.healthLatched.Store(true)
		return
	}
	deadline := at.Add(a.grace)
	entry.owner.phase = deviceGrace
	entry.owner.graceDeadline = deadline
	entry.controlEpoch = newEpoch

	desc := graceTimerDescriptor{
		sessionID:            entry.sessionID,
		controlEpoch:         newEpoch,
		attachmentGeneration: lease.AttachmentGeneration(),
		graceDeadline:        deadline,
	}
	entry.graceDesc = desc
	entry.graceTimer = a.clock.AfterFunc(a.grace, func() {
		a.expireGrace(desc)
	})
}

// expireGrace is the grace timer callback. It re-checks the captured descriptor
// under stateMu; a stale timer (epoch/generation/deadline mismatch) is a no-op.
func (a *ControlArbiter) expireGrace(desc graceTimerDescriptor) {
	if a.healthLatched.Load() {
		return
	}
	entry := a.entryFor(desc.sessionID)
	if entry == nil {
		return
	}
	now := a.clock.Now()
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()

	if entry.removed {
		return
	}
	if entry.owner.kind != ownerDevice || entry.owner.phase != deviceGrace {
		return // already resolved (rebind/release/take/revoke)
	}
	// Exact-match: epoch + generation + deadline must all match.
	if entry.controlEpoch != desc.controlEpoch ||
		entry.owner.attachmentGeneration != desc.attachmentGeneration ||
		!entry.owner.graceDeadline.Equal(desc.graceDeadline) {
		return // stale timer
	}
	// Expire: device.grace → none.
	a.commitTransition(entry, controlOwner{kind: ownerNone}, reasonConnectionExpired, now)
}

// ---------------------------------------------------------------------------
// Reconnect / rebind (design §7.2: device.grace → device.connected)
// ---------------------------------------------------------------------------

// RebindAttachment handles a same-device reattach during grace. The old and new
// leases must both be non-nil and belong to the same device. If the entry is in
// grace for this device, it cancels the timer, rebinds to the new connection,
// and increments controlEpoch. No wire event (holder wire state unchanged).
//
// Returns true if a rebind actually occurred.
func (a *ControlArbiter) RebindAttachment(
	sessionID contract.SessionID,
	newLease *ControlConnectionLease,
	at time.Time,
) bool {
	if newLease == nil || !newLease.IsLive() {
		return false
	}
	entry := a.entryFor(sessionID)
	if entry == nil {
		return false
	}
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	return a.rebindAttachmentLocked(entry, newLease, at)
}

// rebindAttachmentLocked is the stateMu-held core of RebindAttachment. It is
// also used by AttachView to rebind atomically with snapshot+subscribe (design
// §7.1 step 4). Returns true if a rebind occurred. Caller MUST hold
// entry.stateMu.
func (a *ControlArbiter) rebindAttachmentLocked(
	entry *controlEntry,
	newLease *ControlConnectionLease,
	at time.Time,
) bool {
	if entry == nil || newLease == nil || !newLease.IsLive() {
		return false
	}
	if entry.removed || entry.owner.kind != ownerDevice {
		return false
	}
	if entry.owner.deviceID != newLease.DeviceID() {
		return false
	}
	if entry.owner.phase != deviceGrace {
		return false // not in grace; nothing to rebind
	}
	newEpoch, ok := nextEpoch(entry.controlEpoch)
	if !ok {
		a.healthLatched.Store(true)
		return false
	}
	a.cancelGraceTimerLocked(entry)
	entry.owner.connectionID = newLease.ConnectionID()
	entry.owner.attachmentGeneration = newLease.AttachmentGeneration()
	entry.owner.phase = deviceConnected
	entry.owner.graceDeadline = time.Time{}
	entry.controlEpoch = newEpoch
	return true
}

// ---------------------------------------------------------------------------
// Revoke integration (design §7.3)
// ---------------------------------------------------------------------------

// MarkDeviceRevoked is the global no-new-admission fence for a device. It:
//  1. Adds the device to the revoked set (linearization point — all subsequent
//     Checkpoints for this device fail).
//  2. Cancels all of the device's pending launch permits.
//
// This is idempotent. Per-session holder cleanup happens in
// ReleaseRevokedDevice. MarkDeviceRevoked does NOT wait for raw I/O.
func (a *ControlArbiter) MarkDeviceRevoked(deviceID contract.DeviceID) {
	a.permitMu.Lock()
	a.revokedDevices[deviceID] = true
	// Cancel pending launch permits for this device.
	permits := a.pendingByDevice[deviceID]
	canceled := make([]*LaunchPermit, 0, len(permits))
	for _, p := range permits {
		canceled = append(canceled, p)
	}
	a.permitMu.Unlock()

	for _, p := range canceled {
		p.cancel(errDeviceRevoked)
	}

	// M-002: synchronously fence the device's current operations and lifecycle
	// intents on every session it holds, so an in-flight Checkpoint/raw step fails
	// immediately and no lifecycle can commit for this device during the revoke
	// window (before registry FenceDevice/Terminate completes). The revoked-set
	// check in createDevicePTYPermit + Checkpoint keeps new permits stably
	// rejected afterward. This mirrors FenceAllRemote's per-entry fencing but is
	// scoped to the single revoked device.
	a.tableMu.RLock()
	held := make([]*controlEntry, 0, len(a.entries))
	for _, e := range a.entries {
		held = append(held, e)
	}
	a.tableMu.RUnlock()
	preparedLifecycle := make([]chan struct{}, 0, len(held))
	for _, e := range held {
		e.stateMu.Lock()
		if !e.removed && e.owner.kind == ownerDevice && e.owner.deviceID == deviceID {
			a.fenceCurrentOpLocked(e)
			a.closeLifecycleIntentLocked(e, LifecycleClosedRevoke)
			if prepared := e.preparedRestart; prepared != nil {
				prepared.postCommitFence = true
				if prepared.activation != nil {
					prepared.activation.postCommitFence = true
				}
				preparedLifecycle = append(preparedLifecycle, prepared.resolved)
			}
			if prepared := e.preparedStop; prepared != nil {
				prepared.postCommitFence = true
				preparedLifecycle = append(preparedLifecycle, prepared.resolved)
			}
			if prepared := e.preparedRemoval; prepared != nil {
				prepared.postCommitFence = true
				preparedLifecycle = append(preparedLifecycle, prepared.resolved)
			}
		}
		e.stateMu.Unlock()
	}
	for _, resolved := range preparedLifecycle {
		<-resolved
	}
}

// ReleaseRevokedDevice scans all sessions where the revoked device is holder and
// atomically clears each. Sessions are processed in canonical SessionID order;
// only one entry's stateMu is held at a time (design §9.3: no cross-entry lock).
//
// This is called AFTER MarkDeviceRevoked and AFTER registry Terminate, per the
// M1 revoke ordering (design §7.3).
func (a *ControlArbiter) ReleaseRevokedDevice(notice DeviceRevocationNotice) {
	if a.healthLatched.Load() {
		return
	}
	// Snapshot affected sessions under tableMu (pointer fetch only).
	a.tableMu.RLock()
	affectedSessions := make([]sessionEntryPair, 0, len(a.entries))
	for id, entry := range a.entries {
		affectedSessions = append(affectedSessions, sessionEntryPair{id: id, entry: entry})
	}
	a.tableMu.RUnlock()

	// Sort by SessionID for canonical ordering.
	sortSessionEntryPairs(affectedSessions)

	now := notice.OccurredAt
	if now.IsZero() {
		now = a.clock.Now()
	}
	for _, aff := range affectedSessions {
		entry := aff.entry
		entry.stateMu.Lock()
		if entry.removed {
			entry.stateMu.Unlock()
			continue
		}
		if entry.owner.kind == ownerDevice && entry.owner.deviceID == notice.DeviceID {
			a.cancelGraceTimerLocked(entry)
			a.commitTransition(entry, controlOwner{kind: ownerNone}, reasonDeviceRevoked, now)
		}
		entry.stateMu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// Server Stop / security latch (design §7.4)
// ---------------------------------------------------------------------------

// FenceAllRemote is phase 1 of Server Stop / security latch: it sets
// acceptingRemote=false, increments acceptanceGeneration, and cancels ALL
// device launch/run permits and current operations. This is a global
// no-new-admission point that completes within the state budget and does NOT
// wait for raw I/O.
//
// cause determines the transition reason used in phase 2 (ReleaseAllRemote).
func (a *ControlArbiter) FenceAllRemote() {
	a.acceptingRemote.Store(false)
	a.acceptanceGeneration.Add(1)

	// Cancel all pending device launch permits.
	a.permitMu.Lock()
	allPermits := make([]*LaunchPermit, 0, len(a.pendingLaunch))
	for _, p := range a.pendingLaunch {
		allPermits = append(allPermits, p)
	}
	a.permitMu.Unlock()
	for _, p := range allPermits {
		p.cancel(errServerStopped)
	}

	// Fence all current operations. A lifecycle that already installed a
	// prepared owner is allowed to finish its no-fail commit; wait only for that
	// bounded in-memory resolution before returning.
	a.tableMu.RLock()
	entries := make([]*controlEntry, 0, len(a.entries))
	for _, e := range a.entries {
		entries = append(entries, e)
	}
	a.tableMu.RUnlock()
	preparedLifecycle := make([]chan struct{}, 0, len(entries))
	for _, e := range entries {
		e.stateMu.Lock()
		a.fenceCurrentOpLocked(e)
		if prepared := e.preparedRestart; prepared != nil {
			prepared.postCommitFence = true
			if prepared.activation != nil {
				prepared.activation.postCommitFence = true
			}
			preparedLifecycle = append(preparedLifecycle, prepared.resolved)
		}
		if prepared := e.preparedStop; prepared != nil {
			prepared.postCommitFence = true
			preparedLifecycle = append(preparedLifecycle, prepared.resolved)
		}
		if prepared := e.preparedRemoval; prepared != nil {
			prepared.postCommitFence = true
			preparedLifecycle = append(preparedLifecycle, prepared.resolved)
		}
		e.stateMu.Unlock()
	}
	for _, resolved := range preparedLifecycle {
		<-resolved
	}
}

// ReleaseAllRemote is phase 2 of Server Stop / security latch: it clears all
// device holders and emits the appropriate transition. Desktop holders are
// preserved (design §7.4). Wails-source desktop is retained; only remote device
// holders are released.
func (a *ControlArbiter) ReleaseAllRemote(reason controlTransitionReason) {
	if a.healthLatched.Load() {
		return
	}
	a.tableMu.RLock()
	sessions := make([]sessionEntryPair, 0, len(a.entries))
	for id, e := range a.entries {
		sessions = append(sessions, sessionEntryPair{id: id, entry: e})
	}
	a.tableMu.RUnlock()
	sortSessionEntryPairs(sessions)

	now := a.clock.Now()
	for _, sa := range sessions {
		e := sa.entry
		e.stateMu.Lock()
		if e.removed {
			e.stateMu.Unlock()
			continue
		}
		if e.owner.kind == ownerDevice {
			a.cancelGraceTimerLocked(e)
			a.commitTransition(e, controlOwner{kind: ownerNone}, reason, now)
		}
		e.stateMu.Unlock()
	}
}

// RestartRemote marks the arbiter accepting again with a new acceptance
// generation. Old device holders/permits are NOT restored (design §7.4).
func (a *ControlArbiter) RestartRemote() {
	a.acceptanceGeneration.Add(1)
	a.acceptingRemote.Store(true)
}

// ---------------------------------------------------------------------------
// Shutdown (design §10.3)
// ---------------------------------------------------------------------------

// CloseForShutdown is the infallible one-shot shutdown fence. It sets
// ready/accepting false, increments all generations, and cancels all device +
// desktop launch permits and current operations. Returns a one-shot
// ShutdownCleanupPermit.
//
// This does not wait for raw I/O. It may wait only for a sealed, allocation-free
// composite activation to resolve so shutdown cannot return with a half-published
// run; the permit holder performs process cleanup outside the state lock.
func (a *ControlArbiter) CloseForShutdown() *ShutdownCleanupPermit {
	a.ready.Store(false)
	a.acceptingRemote.Store(false)
	a.runtimeGeneration.Add(1)
	a.acceptanceGeneration.Add(1)
	a.healthLatched.Store(false) // shutdown is authoritative, not a latch

	// Cancel all pending launch permits (device + desktop).
	a.permitMu.Lock()
	allPermits := make([]*LaunchPermit, 0, len(a.pendingLaunch))
	for _, p := range a.pendingLaunch {
		allPermits = append(allPermits, p)
	}
	a.pendingLaunch = make(map[uint64]*LaunchPermit)
	a.pendingByDevice = make(map[contract.DeviceID]map[uint64]*LaunchPermit)
	a.permitMu.Unlock()
	for _, p := range allPermits {
		p.cancel(errShutdown)
	}

	// Fence all current operations + cancel grace timers. A sealed composite
	// activation is an in-memory commit critical section: mark it for post-commit
	// fencing and wait for its preallocated resolution after releasing stateMu.
	a.tableMu.Lock()
	entries := make([]*controlEntry, 0, len(a.entries))
	for _, e := range a.entries {
		entries = append(entries, e)
	}
	a.entries = make(map[contract.SessionID]*controlEntry)
	a.tableMu.Unlock()
	preparedActivations := make([]*PreparedCompositeActivation, 0, len(entries))
	preparedLifecycle := make([]chan struct{}, 0, len(entries))
	for _, e := range entries {
		e.stateMu.Lock()
		a.cancelGraceTimerLocked(e)
		a.fenceCurrentOpLocked(e)
		if prepared := e.preparedActivation; prepared != nil {
			prepared.postCommitFence = true
			preparedActivations = append(preparedActivations, prepared)
		}
		if prepared := e.preparedRestart; prepared != nil {
			prepared.postCommitFence = true
			if prepared.activation != nil {
				prepared.activation.postCommitFence = true
			}
			preparedLifecycle = append(preparedLifecycle, prepared.resolved)
		}
		if prepared := e.preparedStop; prepared != nil {
			prepared.postCommitFence = true
			preparedLifecycle = append(preparedLifecycle, prepared.resolved)
		}
		if prepared := e.preparedRemoval; prepared != nil {
			prepared.postCommitFence = true
			preparedLifecycle = append(preparedLifecycle, prepared.resolved)
		}
		e.stateMu.Unlock()
	}
	for _, prepared := range preparedActivations {
		<-prepared.resolved
	}
	for _, resolved := range preparedLifecycle {
		<-resolved
	}

	permit := &ShutdownCleanupPermit{generation: a.runtimeGeneration.Load()}
	return permit
}

// ---------------------------------------------------------------------------
// Internal helpers (stateMu-held)
// ---------------------------------------------------------------------------

// cancelGraceTimerLocked stops and clears the grace timer. Caller holds stateMu.
func (a *ControlArbiter) cancelGraceTimerLocked(entry *controlEntry) {
	if entry.graceTimer != nil {
		entry.graceTimer.Stop()
		entry.graceTimer = nil
	}
	entry.graceDesc = graceTimerDescriptor{}
}

// fenceCurrentOpLocked marks the current operation as fenced and cancels its
// context. This is state-only: it does NOT wait for the operation to
// acknowledge or for done. Caller holds stateMu.
func (a *ControlArbiter) fenceCurrentOpLocked(entry *controlEntry) {
	if entry.preparedRestart != nil || entry.preparedStop != nil || entry.preparedRemoval != nil {
		return
	}
	if entry.currentOp != nil {
		entry.currentOp.fenced.Store(true)
		if entry.currentOp.cancel != nil {
			entry.currentOp.cancel(errOperationFenced)
		}
	}
}

// ---------------------------------------------------------------------------
// Entry lifecycle (design §6.4, §10.2)
// ---------------------------------------------------------------------------

// registerStartingSession creates a staging entry visible only to the control
// runtime. It mints a new runIdentity + runEpoch and returns the RunPermit and
// the RunObservationPermit that raw PTY goroutines MUST capture.
//
// The entry is NOT public (runPhase=starting) until ActivateRun succeeds.
func (a *ControlArbiter) registerStartingSession(
	id contract.SessionID,
	permit *LaunchPermit,
) (*RunPermit, *RunObservationPermit, *ControlGateError) {
	if err := a.checkReady(); err != nil {
		return nil, nil, err
	}
	// Revalidate the launch permit (design §6.4.1 step 3).
	if gateErr := permit.revalidate(a); gateErr != nil {
		return nil, nil, gateErr
	}

	entry := &controlEntry{
		sessionID:    id,
		owner:        controlOwner{kind: ownerNone},
		controlEpoch: 1,
		opLane:       newBoundedOperationLane(),
		runPhase:     runStarting,
		backend:      backendHealthy,
		initialStage: &initialRunStage{records: make([]LiveRunRecord, 0, liveFeedMaxRecords-1)},
	}

	run, runEpoch, ok := a.mintRunIdentityLocked(entry)
	if !ok {
		return nil, nil, &ControlGateError{Kind: DenyControlUnavailable}
	}

	a.tableMu.Lock()
	if _, exists := a.entries[id]; exists {
		a.tableMu.Unlock()
		return nil, nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	a.entries[id] = entry
	a.tableMu.Unlock()

	rp := &RunPermit{
		launch:   permit,
		entry:    entry,
		run:      run,
		runEpoch: runEpoch,
	}
	rop := &RunObservationPermit{
		entry:        entry,
		run:          run,
		runEpoch:     runEpoch,
		backendEpoch: entry.backendEpoch,
	}
	return rp, rop, nil
}

// reserveRunIdentityLocked reserves a fresh, never-reused run epoch and mints
// its pointer identity WITHOUT publishing it as entry.currentRun. Restart stage
// uses this split so StartProcess receives the new identity while readers and
// exact-match permits still see the old public run until activate commits.
// Caller holds entry.stateMu.
func (a *ControlArbiter) reserveRunIdentityLocked(entry *controlEntry) (*runIdentity, uint64, bool) {
	frontier := entry.runEpochSeq
	if entry.runEpoch > frontier {
		// Compatibility with fixtures/older entries created before runEpochSeq was
		// introduced: never allocate behind the active epoch.
		frontier = entry.runEpoch
	}
	newEpoch, ok := nextEpoch(frontier)
	if !ok {
		a.healthLatched.Store(true)
		return nil, 0, false
	}
	token := mintDesktopRunToken()
	if token == "" {
		// crypto/rand failure: fail-closed before publishing the reservation.
		a.healthLatched.Store(true)
		return nil, 0, false
	}
	run := &runIdentity{
		nonce:           newEpoch, // monotonic nonce; pointer identity is authoritative
		desktopRunToken: token,
	}
	entry.runEpochSeq = newEpoch
	return run, newEpoch, true
}

// mintRunIdentityLocked reserves and immediately publishes a fresh identity.
// It is used by first-run registration and test helpers; restart uses the split
// reserve-at-stage / publish-at-activate transaction instead.
func (a *ControlArbiter) mintRunIdentityLocked(entry *controlEntry) (*runIdentity, uint64, bool) {
	run, newEpoch, ok := a.reserveRunIdentityLocked(entry)
	if !ok {
		return nil, 0, false
	}
	entry.currentRun = run
	entry.runEpoch = newEpoch
	return run, newEpoch, true
}

// activateRun transitions a staging entry to public (runPhase=active). The
// RunPermit must exact-match the current run pointer + runEpoch.
func (a *ControlArbiter) activateRun(p *RunPermit) *ControlGateError {
	if err := a.checkReady(); err != nil {
		return err
	}
	if p == nil || p.entry == nil {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	p.entry.stateMu.Lock()
	defer p.entry.stateMu.Unlock()

	if p.entry.removed {
		return &ControlGateError{Kind: DenySessionNotFound}
	}
	if p.run != p.entry.currentRun || p.runEpoch != p.entry.runEpoch {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	if p.entry.runPhase != runStarting || p.entry.preparedActivation != nil {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	p.entry.initialStage = nil
	p.entry.runPhase = runActive
	if !p.entry.stateMirrorSet {
		p.entry.stateMirror = contract.SessionStateRunning
		p.entry.stateMirrorSet = true
	}
	return nil
}

// removeSession marks the entry as a tombstone, fences all generations, and
// emits a session_removed transition if the holder was non-none. The entry
// pointer remains in the table for stale-permit suppression until the caller
// physically removes it.
func (a *ControlArbiter) removeSession(id contract.SessionID) *ControlGateError {
	if err := a.checkReady(); err != nil {
		return err
	}
	entry := a.entryFor(id)
	if entry == nil {
		return &ControlGateError{Kind: DenySessionNotFound}
	}
	now := a.clock.Now()
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()

	if entry.removed {
		return &ControlGateError{Kind: DenySessionNotFound}
	}

	// control.none first (if holder non-none), then tombstone.
	oldKind := entry.owner.kind
	a.cancelGraceTimerLocked(entry)
	a.fenceCurrentOpLocked(entry)
	if oldKind != ownerNone {
		a.commitTransition(entry, controlOwner{kind: ownerNone}, reasonSessionRemoved, now)
	}
	entry.removed = true
	// Fence run/backend generations so stale observations/operations fail.
	entry.runPhase = runTerminal
	entry.currentRun = nil
	return nil
}

// physicallyDeleteEntry removes the tombstoned entry from the table. Called by
// the gate after the raw remove effect succeeds and events are delivered.
func (a *ControlArbiter) physicallyDeleteEntry(id contract.SessionID) {
	a.tableMu.Lock()
	delete(a.entries, id)
	a.tableMu.Unlock()
}

// ---------------------------------------------------------------------------
// Run observation (design §5.2 INV-08, §8.6)
// ---------------------------------------------------------------------------

// observeRunTransition processes a trusted run observation (output/exit). It
// exact-matches the RunObservationPermit's pointer + runEpoch + backendEpoch.
// A stale/duplicate observation is a silent no-op (returns nil).
func (a *ControlArbiter) observeRunTransition(
	permit *RunObservationPermit,
	apply func(entry *controlEntry),
) bool {
	if permit == nil || permit.entry == nil || permit.run == nil {
		return false
	}
	entry := permit.entry
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()

	if entry.removed {
		return false
	}
	// Exact-match: pointer + runEpoch + backendEpoch.
	if entry.currentRun != permit.run || entry.runEpoch != permit.runEpoch {
		return false
	}
	if entry.backendEpoch != permit.backendEpoch {
		return false
	}
	if entry.backend == backendQuarantined {
		return false
	}
	if apply != nil {
		apply(entry)
	}
	return true
}

// ObserveExit processes a process exit observation. Only the current run's exit
// updates state; stale/duplicate exits are no-ops.
func (a *ControlArbiter) ObserveExit(
	permit *RunObservationPermit,
	obs ProcessExitObservation,
) bool {
	return a.observeRunTransition(permit, func(entry *controlEntry) {
		entry.runPhase = runTerminal
		if obs.Failed {
			entry.stateMirror = contract.SessionStateUnavailable
		} else {
			entry.stateMirror = contract.SessionStateExited
		}
		entry.stateMirrorSet = true
	})
}

// ObserveOutput processes a PTY output observation. Only the current run's
// output is accepted; stale/duplicate is no-op. The actual per-run history
// buffering is the RunEventProjector's concern (A2); A1 only validates the
// permit.
func (a *ControlArbiter) ObserveOutput(permit *RunObservationPermit) bool {
	return a.observeRunTransition(permit, nil)
}

// CommitExactStopped records an exact run's confirmed stop after the process
// capability has reached terminal. A stale run generation is a silent no-op.
func (a *ControlArbiter) CommitExactStopped(sessionID contract.SessionID, runEpoch uint64) bool {
	entry := a.entryFor(sessionID)
	if entry == nil {
		return false
	}
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed || entry.currentRun == nil || entry.runEpoch != runEpoch {
		return false
	}
	entry.runPhase = runTerminal
	entry.stateMirror = contract.SessionStateStopped
	entry.stateMirrorSet = true
	return true
}

// ReconcileRestartFailure transitions an entry to an honest terminal/unavailable
// state after a restart raw effect failed at an irreversible step (R3-001,
// design §4.5). A restart is a transaction across seal → stop → resolve →
// commit → start; once any step with an irreversible side effect fails, the
// session MUST NOT be presented as running (the process may be stopped, the run
// identity may already be swapped, or the new process may have failed to start).
// The recovery path is explicit: the user issues Stop (idempotent PTY close,
// allowed on any runPhase) then Restart.
//
// R4-001 (generation-bound): the reconcile is bound to the EXACT runEpoch of the
// failed attempt. It only transitions the session to terminal if the session is
// STILL in that run's generation (entry.runEpoch == failedEpoch). A newer
// successful restart (higher runEpoch) — e.g. after the user recovered via
// Stop+Restart while a timeout-abandoned raw goroutine was still in flight —
// makes this (stale) reconcile a NO-OP, so a late failure can never clobber a
// newer running run. This only touches runPhase + stateMirror + fences the
// in-flight op; the durable H1 feed records remain as an honest audit trail.
// Lifecycle (stop/restart/remove) permits do NOT check runPhase, so recovery is
// not blocked by the terminal transition.
func (a *ControlArbiter) ReconcileRestartFailure(sessionID contract.SessionID, failedRunEpoch uint64) {
	entry := a.entryFor(sessionID)
	if entry == nil {
		return
	}
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return
	}
	// R4-001: only reconcile if the session is STILL in the failed attempt's run
	// generation. A newer successful restart advanced runEpoch beyond it → no-op
	// (do not clobber the newer running run).
	if entry.currentRun == nil || entry.runEpoch != failedRunEpoch {
		return
	}
	entry.runPhase = runTerminal
	entry.stateMirror = contract.SessionStateUnavailable
	entry.stateMirrorSet = true
	a.fenceCurrentOpLocked(entry)
}

// ---------------------------------------------------------------------------
// Session state mirror (design §4.3)
// ---------------------------------------------------------------------------

// SetSessionStateMirror updates the session state mirror under stateMu. Called
// by controlled lifecycle commands and validated run observations.
func (a *ControlArbiter) SetSessionStateMirror(id contract.SessionID, state contract.SessionState) *ControlGateError {
	entry := a.entryFor(id)
	if entry == nil {
		return &ControlGateError{Kind: DenySessionNotFound}
	}
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return &ControlGateError{Kind: DenySessionNotFound}
	}
	entry.stateMirror = state
	entry.stateMirrorSet = true
	return nil
}

// SessionStateMirror returns the mirrored state + runPhase for DTO assembly.
// Returns (0, 0, false) if the entry doesn't exist.
func (a *ControlArbiter) SessionStateMirror(id contract.SessionID) (contract.SessionState, runPhase, bool) {
	entry := a.entryFor(id)
	if entry == nil {
		return "", 0, false
	}
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return contract.SessionStateRemoved, runTerminal, true
	}
	return entry.stateMirror, entry.runPhase, true
}

// ---------------------------------------------------------------------------
// Sort helper
// ---------------------------------------------------------------------------

// sessionEntryPair pairs a session ID with its entry for sorted iteration.
type sessionEntryPair struct {
	id    contract.SessionID
	entry *controlEntry
}

// sortSessionEntryPairs sorts session-entry pairs by SessionID for canonical
// ordering (design §9.3: global revoke/clear processes entries one at a time in
// SessionID order; never holds two stateMu simultaneously).
func sortSessionEntryPairs(s []sessionEntryPair) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i].id > s[j].id {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Cancellation cause errors (for permit.cancel)
// ---------------------------------------------------------------------------

var (
	errDeviceRevoked = errors.New("control: device revoked")
	errServerStopped = errors.New("control: server stopped")
	errShutdown      = errors.New("control: shutdown")
)

// suppress unused import for context (used by permit types in control_gate.go)
var _ = context.Background
