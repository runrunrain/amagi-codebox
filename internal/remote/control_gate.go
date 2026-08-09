package remote

// control_gate.go — ControlGate: the single command door for all external
// session write side effects (design §6).
//
// The gate orchestrates: actor validation → arbiter state check → OperationLane
// serialization → Checkpoint-validated raw effect → exact-match commit. It is
// the ONLY entry point for PTY input/resize, CLI launch, and session lifecycle
// writes. Raw ports (pty.Service, session.Manager, launchers) sit BEHIND the
// gate as closures; they are never directly callable from Wails/HTTP/WS.
//
// Fail-closed: nil/unready gate, missing entry, unknown state, stale epoch,
// fenced operation, or quarantined backend → denial with zero side effects.

import (
	"context"
	"errors"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Raw effect closure types (design §6.1)
// ---------------------------------------------------------------------------

// RawEffect is a PTY mutation closure. It MUST call permit.Checkpoint before
// each irreversible step and MUST NOT re-enter the gate, registry, Server, or
// do network broadcast. The context carries the operation deadline.
type RawEffect func(ctx context.Context, permit *operationPermit) error

// RawSessionEffect is a lifecycle mutation closure. In addition to RawEffect
// semantics, it returns the session mutation result for exact-match commit.
type RawSessionEffect func(ctx context.Context, permit *operationPermit) (SessionMutationResult, error)

// RawLaunchEffect is a launch step closure. It receives the operation permit
// and an EffectReceipt to fill before its first irreversible syscall.
type RawLaunchEffect func(ctx context.Context, permit *operationPermit, receipt *EffectReceipt) error

// ---------------------------------------------------------------------------
// ControlGate interface (design §6.1) — exposed for A2/A3 consumption.
//
// A1 implements the core subset (state machine delegation, PTY/lifecycle lane
// orchestration, launch fencing). The full §6.1 surface is declared here so A2
// (raw port wiring) and A3 (WS broadcast) can consume a stable interface.
// ---------------------------------------------------------------------------

// ControlGate is the single command door for session write side effects.
type ControlGate interface {
	// --- State queries / mutations ---
	Acquire(ctx context.Context, principal DevicePrincipal, lease *ControlConnectionLease, sessionID contract.SessionID) (contract.ControlSnapshot, error)
	Release(ctx context.Context, principal DevicePrincipal, sessionID contract.SessionID) (contract.ControlSnapshot, error)
	SnapshotForDevice(sessionID contract.SessionID, viewer contract.DeviceID) (contract.ControlSnapshot, error)
	TakeDesktop(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID) error
	ReleaseDesktop(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID) error

	// --- Desktop PTY / lifecycle (intentional = take-first) ---
	DoDesktopPTY(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID, op PTYOperation, mutate RawEffect) error
	DoDesktopPassiveResize(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID, mutate RawEffect) error
	DoDesktopLifecycle(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID, op LifecycleOperation, mutate RawSessionEffect) (SessionMutationResult, error)

	// ClearStoppedDesktopSession authoritatively removes ONE stopped session's
	// control tombstone through the desktop authority (R4-005). Unlike
	// DoDesktopLifecycle it does NOT take desktop control: a stopped tombstone
	// has no live PTY, and the clear must not preempt an active device holder of
	// a session that restarted after the caller's snapshot. The session MUST be
	// in a terminal (stopped) phase at permit time (race guard); otherwise it is
	// skipped (DenySessionNotStopped). Returns cleared=true only when the
	// tombstone was physically removed under an authoritative N-002 completion.
	ClearStoppedDesktopSession(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID) (bool, error)

	// --- Device PTY / lifecycle (exact lease required) ---
	DoDevicePTY(ctx context.Context, lease *ControlConnectionLease, sessionID contract.SessionID, op PTYOperation, mutate RawEffect) error
	DoDeviceLifecycle(ctx context.Context, principal DevicePrincipal, sessionID contract.SessionID, op LifecycleOperation, mutate RawSessionEffect) (SessionMutationResult, error)

	// --- Launch staging / fencing ---
	BeginDesktopLaunch(ctx context.Context, authority *DesktopAuthority) (*LaunchPermit, error)
	BeginDeviceLaunch(ctx context.Context, principal DevicePrincipal) (*LaunchPermit, error)
	RegisterStartingSession(ctx context.Context, p *LaunchPermit, id contract.SessionID) (*RunPermit, *RunObservationPermit, error)
	DoLaunchEffect(ctx context.Context, p *RunPermit, kind LaunchEffectKind, effect RawLaunchEffect) error
	DoBootstrapPTY(ctx context.Context, p *RunPermit, mutate RawEffect) error
	ActivateRun(ctx context.Context, p *RunPermit) error
	AbortLaunch(ctx context.Context, p *LaunchPermit, cause error)

	// --- Trusted observation / integration ---
	ObserveExit(ctx context.Context, permit *RunObservationPermit, obs ProcessExitObservation) error
	OnUnexpectedDetachForSession(sessionID contract.SessionID, lease *ControlConnectionLease, at time.Time)
	RebindAttachment(sessionID contract.SessionID, newLease *ControlConnectionLease, at time.Time) bool

	// --- Revoke / Server lifecycle ---
	MarkDeviceRevoked(deviceID contract.DeviceID)
	ReleaseRevokedDevice(notice DeviceRevocationNotice)
	FenceAllRemote()
	ReleaseAllRemote()
	RestartRemote()
	CloseForShutdown() *ShutdownCleanupPermit
}

// ---------------------------------------------------------------------------
// controlGate concrete implementation
// ---------------------------------------------------------------------------

// controlGate implements ControlGate. It wraps a ControlArbiter and provides
// the operation-lane orchestration for raw effects.
type controlGate struct {
	arbiter   *ControlArbiter
	hub       *SessionEventHub
	directory *AttachmentDirectory
	clock     Clock

	// backendDetacher forcibly moves one exact PTY backend out of the active
	// namespace and returns typed confirmation/retry evidence. nil fails closed:
	// quarantine state advances, but recovery lifecycle remains denied.
	backendDetacher func(sessionID contract.SessionID) (BackendDetachReceipt, error)
}

// NewControlGate creates a gate backed by the given arbiter, hub, and
// attachment directory. All must be non-nil.
func NewControlGate(arbiter *ControlArbiter, hub *SessionEventHub, directory *AttachmentDirectory) ControlGate {
	return &controlGate{
		arbiter:   arbiter,
		hub:       hub,
		directory: directory,
		clock:     arbiter.clock,
	}
}

// SetBackendDetacher injects the forceful backend detach callback (R3-004).
// Called from the quarantine path when a bounded raw effect times out
// mid-syscall, so the stuck backend (Windows ConPTY overlapped I/O / Darwin
// ptmx) is released by the OS rather than leaking until process shutdown.
func (g *controlGate) SetBackendDetacher(fn func(sessionID contract.SessionID) (BackendDetachReceipt, error)) {
	g.backendDetacher = fn
}

// unwrapErr converts a *ControlGateError to an error for the interface methods.
func unwrapErr(gErr *ControlGateError) error {
	if gErr == nil {
		return nil
	}
	return *gErr
}

// ---------------------------------------------------------------------------
// State queries / mutations (delegate to arbiter)
// ---------------------------------------------------------------------------

func (g *controlGate) Acquire(ctx context.Context, principal DevicePrincipal, lease *ControlConnectionLease, sessionID contract.SessionID) (contract.ControlSnapshot, error) {
	snap, gErr := g.arbiter.Acquire(principal, lease, sessionID)
	return snap, unwrapErr(gErr)
}

func (g *controlGate) Release(ctx context.Context, principal DevicePrincipal, sessionID contract.SessionID) (contract.ControlSnapshot, error) {
	snap, gErr := g.arbiter.Release(principal, sessionID)
	return snap, unwrapErr(gErr)
}

func (g *controlGate) SnapshotForDevice(sessionID contract.SessionID, viewer contract.DeviceID) (contract.ControlSnapshot, error) {
	snap, gErr := g.arbiter.SnapshotForDevice(sessionID, viewer)
	return snap, unwrapErr(gErr)
}

func (g *controlGate) TakeDesktop(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID) error {
	return unwrapErr(g.arbiter.TakeDesktop(authority, sessionID))
}

func (g *controlGate) ReleaseDesktop(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID) error {
	return unwrapErr(g.arbiter.ReleaseDesktop(authority, sessionID))
}

// ---------------------------------------------------------------------------
// Desktop PTY (intentional input = take-first)
// ---------------------------------------------------------------------------

func (g *controlGate) DoDesktopPTY(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID, op PTYOperation, mutate RawEffect) error {
	if err := g.arbiter.checkReady(); err != nil {
		return err
	}
	if authority == nil {
		return &ControlGateError{Kind: DenyControlUnavailable}
	}
	// Intentional input: take desktop first (preempts device holder).
	if gErr := g.arbiter.TakeDesktop(authority, sessionID); gErr != nil {
		return gErr
	}
	return g.doBoundedDesktopWrite(ctx, authority, sessionID, mutate)
}

// DoDesktopPassiveResize does NOT take; it only executes if holder is none or
// desktop. A device holder blocks passive resize (design §6.2, R-06).
func (g *controlGate) DoDesktopPassiveResize(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID, mutate RawEffect) error {
	if err := g.arbiter.checkReady(); err != nil {
		return err
	}
	if authority == nil {
		return &ControlGateError{Kind: DenyControlUnavailable}
	}
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return &ControlGateError{Kind: DenySessionNotFound}
	}
	// Check holder: passive resize allowed only for none/desktop.
	entry.stateMu.Lock()
	holderKind := entry.owner.kind
	entry.stateMu.Unlock()
	if holderKind == ownerDevice {
		return &ControlGateError{Kind: DenyNotController}
	}
	return g.doBoundedDesktopWrite(ctx, authority, sessionID, mutate)
}

// doBoundedDesktopWrite acquires the lane, validates desktop holder under
// stateMu, creates an operation permit, runs the raw effect, and commits.
func (g *controlGate) doBoundedDesktopWrite(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID, mutate RawEffect) error {
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return &ControlGateError{Kind: DenySessionNotFound}
	}

	// Acquire lane (outside stateMu). On timeout, quarantine.
	if err := g.acquireLaneOrQuarantine(ctx, entry); err != nil {
		return err
	}
	defer entry.opLane.release()

	// Create permit under stateMu.
	permit, gErr := g.createDesktopPermit(ctx, entry, authority)
	if gErr != nil {
		return gErr
	}
	// Execute raw effect (outside stateMu), bounded (M-009).
	rawErr := g.runBoundedRawEffect(entry, func() error { return mutate(permit.ctx(), permit) })
	// Commit / clear under stateMu.
	g.finishOperation(entry, permit)
	return rawErr
}

// ---------------------------------------------------------------------------
// Desktop lifecycle (two-phase: fence + lane + drain)
// ---------------------------------------------------------------------------

func (g *controlGate) DoDesktopLifecycle(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID, op LifecycleOperation, mutate RawSessionEffect) (SessionMutationResult, error) {
	if err := g.arbiter.checkReady(); err != nil {
		return SessionMutationResult{}, err
	}
	if authority == nil {
		return SessionMutationResult{}, &ControlGateError{Kind: DenyControlUnavailable}
	}
	// Lifecycle is intentional: take desktop first.
	if gErr := g.arbiter.TakeDesktop(authority, sessionID); gErr != nil {
		return SessionMutationResult{}, gErr
	}
	return g.doBoundedLifecycle(ctx, authority, sessionID, op, mutate)
}

// ClearStoppedDesktopSession (R4-005) authoritatively removes ONE stopped
// session's control tombstone through the desktop authority WITHOUT taking
// desktop control. A stopped tombstone has no live PTY; taking desktop control
// (as DoDesktopLifecycle does) would WRONGLY preempt an active device holder of
// a session that restarted after the caller's snapshot. Instead this path:
//
//   - commits a lifecycle Remove intent (serializes vs concurrent lifecycle),
//   - acquires the lane,
//   - mints a drain permit that requires runPhase==runTerminal under stateMu
//     (race guard: a session that restarted is skipped, not removed),
//   - runs a no-op raw effect (the PTY was already closed by Stop) under the
//     bounded budget + Checkpoint fence,
//   - on an authoritative N-002 completion, tombstones + physically deletes the
//     entry (the device holder, if any, receives session_removed).
//
// Returns (cleared=true, nil) only when the entry was physically removed.
func (g *controlGate) ClearStoppedDesktopSession(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID) (bool, error) {
	if err := g.arbiter.checkReady(); err != nil {
		return false, err
	}
	if authority == nil {
		return false, &ControlGateError{Kind: DenyControlUnavailable}
	}
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return false, &ControlGateError{Kind: DenySessionNotFound}
	}
	// Phase 1: commit lifecycle intent (fences in-flight regular writes; denies
	// if a concurrent lifecycle is already in progress).
	intentID, gErr := g.commitLifecycleIntent(entry, LifecycleRemove)
	if gErr != nil {
		return false, gErr
	}
	if err := g.acquireLaneOrQuarantine(ctx, entry); err != nil {
		g.abandonLifecycleIntent(entry, intentID)
		return false, err
	}
	defer entry.opLane.release()
	// Phase 2: drain permit with the clear-stopped guards (terminal + authority).
	permit, gErr := g.createClearStoppedPermit(ctx, entry, authority, intentID)
	if gErr != nil {
		// The intent was committed but not consumable (e.g. the session is no
		// longer stopped, or a concurrent fence superseded it). Abandon OUR
		// intent so it does not block a subsequent lifecycle on this session.
		g.abandonLifecycleIntent(entry, intentID)
		return false, gErr
	}
	// Raw effect: the stopped session's PTY was already closed by Stop, so there
	// is nothing to close here. The tombstone removal is authoritative-only
	// (runBoundedLifecycleEffect + finishLifecycleResult gate it).
	result, rawErr := g.runBoundedLifecycleEffect(entry, func() (SessionMutationResult, error) {
		if err := permit.Checkpoint(ctx, 1); err != nil {
			return SessionMutationResult{}, err
		}
		return SessionMutationResult{Removed: true}, nil
	})
	authoritative := g.finishLifecycleResult(entry, permit)
	if rawErr != nil {
		return false, rawErr
	}
	if !authoritative {
		// N-002: a fence/restart that linearized after the last Checkpoint makes
		// this completion stale — must not tombstone/remove.
		return false, &ControlGateError{Kind: DenyStalePermit}
	}
	if result.Removed {
		if gErr := g.arbiter.removeSession(sessionID); gErr != nil {
			return false, gErr
		}
		g.arbiter.physicallyDeleteEntry(sessionID)
		return true, nil
	}
	return false, nil
}

// createClearStoppedPermit mints the drain permit for ClearStoppedDesktopSession
// under stateMu. Unlike createLifecyclePermit it does NOT require (or take)
// desktop control. Guards (R4-005):
//   - not removed + exact-match intent (N-002 phase-2),
//   - desktop-source consistency when the current holder is desktop,
//   - runPhase==runTerminal: the race guard. A session that restarted after the
//     caller's snapshot is runActive and is skipped (DenySessionNotStopped), so
//     it is never force-removed and its active device holder is not disturbed.
func (g *controlGate) createClearStoppedPermit(ctx context.Context, entry *controlEntry, authority *DesktopAuthority, intentID uint64) (*operationPermit, *ControlGateError) {
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	if !exactMatchPendingDrain(entry, intentID, LifecycleRemove) {
		return nil, &ControlGateError{Kind: DenyStalePermit}
	}
	if entry.owner.kind == ownerDesktop && entry.owner.desktopSource != authority.source {
		return nil, &ControlGateError{Kind: DenyNotController}
	}
	// R4-005 race guard: only clear sessions that are still stopped (terminal).
	if entry.runPhase != runTerminal {
		return nil, &ControlGateError{Kind: DenySessionNotStopped}
	}
	// Consume the intent and mint the permit (lifecycle=true so finishLifecycleResult
	// treats it as a lifecycle completion).
	entry.pendingDrain = nil
	p := g.mintOperationPermit(ctx, entry, contract.DeviceID(""), authority.source)
	p.lifecycle = true
	return p, nil
}

// ---------------------------------------------------------------------------
// Device PTY (exact lease required)
// ---------------------------------------------------------------------------

func (g *controlGate) DoDevicePTY(ctx context.Context, lease *ControlConnectionLease, sessionID contract.SessionID, op PTYOperation, mutate RawEffect) error {
	if err := g.arbiter.checkReady(); err != nil {
		return err
	}
	if lease == nil || !lease.IsLive() {
		return &ControlGateError{Kind: DenyNoAuthoritativeAttachment}
	}
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return &ControlGateError{Kind: DenySessionNotFound}
	}
	// Acquire lane.
	if err := g.acquireLaneOrQuarantine(ctx, entry); err != nil {
		return err
	}
	defer entry.opLane.release()

	// Create permit under stateMu (validates exact device holder + lease).
	permit, gErr := g.createDevicePTYPermit(ctx, entry, lease)
	if gErr != nil {
		return gErr
	}
	rawErr := g.runBoundedRawEffect(entry, func() error { return mutate(permit.ctx(), permit) })
	g.finishOperation(entry, permit)
	return rawErr
}

// ---------------------------------------------------------------------------
// Device lifecycle
// ---------------------------------------------------------------------------

func (g *controlGate) DoDeviceLifecycle(ctx context.Context, principal DevicePrincipal, sessionID contract.SessionID, op LifecycleOperation, mutate RawSessionEffect) (SessionMutationResult, error) {
	if err := g.arbiter.checkReady(); err != nil {
		return SessionMutationResult{}, err
	}
	// Device lifecycle: REST actor must match holder DeviceID (connected or grace).
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return SessionMutationResult{}, &ControlGateError{Kind: DenySessionNotFound}
	}
	entry.stateMu.Lock()
	holderDevice := ""
	if entry.owner.kind == ownerDevice {
		holderDevice = string(entry.owner.deviceID)
	}
	entry.stateMu.Unlock()
	if holderDevice != string(principal.DeviceID) {
		return SessionMutationResult{}, &ControlGateError{Kind: DenyNotController}
	}
	// Lifecycle uses the desktop write path with the device-as-holder validation.
	return g.doBoundedDeviceLifecycle(ctx, principal, sessionID, op, mutate)
}

// ---------------------------------------------------------------------------
// Lane acquire + quarantine
// ---------------------------------------------------------------------------

// acquireLaneOrQuarantine acquires the per-session lane. On timeout, it
// quarantines the backend (design §9.1.3): the old operation did not
// acknowledge cancel within the budget, so the backend is detached.
func (g *controlGate) acquireLaneOrQuarantine(ctx context.Context, entry *controlEntry) error {
	// Use a deadline derived from the operation timeout.
	laneCtx, cancel := context.WithTimeout(ctx, controlDataOperationTimeout)
	defer cancel()
	err := entry.opLane.acquire(laneCtx, controlOperationLaneWaitTimeout)
	if err == nil {
		return nil
	}
	if errors.Is(err, errOperationLaneTimeout) {
		// Old operation didn't release the lane within the cancel-ack budget.
		detach := g.quarantineBackend(entry)
		return &ControlGateError{Kind: DenyControlUnavailable, Detach: detach.disposition}
	}
	// ctx cancelled or other error.
	return &ControlGateError{Kind: DenyControlUnavailable}
}

// quarantineBackend advances backendEpoch, fences the blocked operation, and
// obtains typed detach evidence OUTSIDE stateMu. Recovery is authorized only
// after a receipt for this exact epoch confirms. A late receipt exact-matches
// {entry pointer, backendEpoch, detachIdentity} before changing state.
func (g *controlGate) quarantineBackend(entry *controlEntry) backendDetachRecord {
	entry.stateMu.Lock()
	newEpoch, ok := nextEpoch(entry.backendEpoch)
	if !ok {
		g.arbiter.healthLatched.Store(true)
		entry.stateMu.Unlock()
		return backendDetachRecord{disposition: BackendDetachUnquarantinedFailed}
	}
	entry.backendEpoch = newEpoch
	entry.backend = backendQuarantined
	entry.runPhase = runTerminal
	entry.backendDetach = backendDetachRecord{
		backendEpoch: newEpoch,
		disposition:  BackendDetachQuarantinedPending,
	}
	sessionID := entry.sessionID
	g.arbiter.fenceCurrentOpLocked(entry)
	entry.stateMu.Unlock()

	if g.backendDetacher == nil {
		return backendDetachRecord{backendEpoch: newEpoch, disposition: BackendDetachQuarantinedPending}
	}
	receipt, detachErr := g.backendDetacher(sessionID)
	if receipt == nil {
		// The raw port did not provide retriable exact-backend ownership. Keep the
		// entry quarantined and deny all recovery effects rather than guessing.
		return backendDetachRecord{backendEpoch: newEpoch, disposition: BackendDetachQuarantinedPending}
	}
	identity := receipt.Identity()
	if identity == 0 {
		return backendDetachRecord{backendEpoch: newEpoch, disposition: BackendDetachQuarantinedPending}
	}
	record := backendDetachRecord{
		backendEpoch:   newEpoch,
		detachIdentity: identity,
		disposition:    BackendDetachQuarantinedPending,
	}
	entry.stateMu.Lock()
	if entry.backend == backendQuarantined && entry.backendEpoch == newEpoch {
		entry.backendDetach = record
	}
	entry.stateMu.Unlock()

	if detachErr == nil && receipt.Confirmed() {
		g.confirmBackendDetach(entry, record)
		record.disposition = BackendDetachQuarantinedDetached
		return record
	}
	// Initial failure is visible through the returned typed pending disposition
	// and receipt.LastError. Await the PTY's exact-pointer reaper; no SessionID
	// lookup occurs here or in the receipt implementation.
	go func() {
		if err := receipt.Wait(context.Background()); err == nil && receipt.Confirmed() {
			g.confirmBackendDetach(entry, record)
		}
	}()
	return record
}

func (g *controlGate) confirmBackendDetach(entry *controlEntry, record backendDetachRecord) {
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed || entry.backend != backendQuarantined ||
		entry.backendEpoch != record.backendEpoch ||
		entry.backendDetach.backendEpoch != record.backendEpoch ||
		entry.backendDetach.detachIdentity != record.detachIdentity {
		return
	}
	entry.backendDetach.disposition = BackendDetachQuarantinedDetached
}

func exactBackendDetachConfirmedLocked(entry *controlEntry) bool {
	return entry.backend == backendQuarantined &&
		entry.backendDetach.backendEpoch == entry.backendEpoch &&
		entry.backendDetach.detachIdentity != 0 &&
		entry.backendDetach.disposition == BackendDetachQuarantinedDetached
}

// runBoundedRawEffect executes a data-operation raw effect (WriteRaw/ResizeRaw)
// under a bounded budget (M-009). The underlying syscall cannot be interrupted,
// so it runs in a goroutine; if it does not acknowledge within
// controlRawEffectTimeout the backend is quarantined (backendEpoch isolation —
// the stuck goroutine's late result is dropped by the committer's backendEpoch
// check) and a typed timeout is returned. quarantineBackend fences the current
// op, cancelling permit.ctx() so a ctx-aware raw port observing the deadline can
// bail. The caller still owns finishOperation (permit clear) + lane release
// (defer). The caller/lane are never occupied beyond the budget.
func (g *controlGate) runBoundedRawEffect(entry *controlEntry, raw func() error) error {
	done := make(chan error, 1)
	go func() { done <- raw() }()
	timer := time.NewTimer(controlRawEffectTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		detach := g.quarantineBackend(entry)
		return &ControlGateError{Kind: DenyOperationTimeout, Detach: detach.disposition}
	}
}

// runBoundedLifecycleEffect is the lifecycle variant (M-009). It uses the longer
// controlLifecycleEffectTimeout budget because the raw Close caps its own read-
// loop wait at ptyCloseWaitTimeout (3s); a healthy close never trips the budget
// while a hung backend is still isolated. On timeout the backend is quarantined
// (so finishLifecycleResult's backendEpoch exact-match marks the completion
// non-authoritative → no tombstone/success).
func (g *controlGate) runBoundedLifecycleEffect(entry *controlEntry, raw func() (SessionMutationResult, error)) (SessionMutationResult, error) {
	type lfResult struct {
		r SessionMutationResult
		e error
	}
	done := make(chan lfResult, 1)
	go func() {
		r, e := raw()
		done <- lfResult{r, e}
	}()
	timer := time.NewTimer(controlLifecycleEffectTimeout)
	defer timer.Stop()
	select {
	case res := <-done:
		return res.r, res.e
	case <-timer.C:
		detach := g.quarantineBackend(entry)
		return SessionMutationResult{}, &ControlGateError{Kind: DenyOperationTimeout, Detach: detach.disposition}
	}
}

// runBoundedRestartEffect gives the full typed restart transaction its own
// budget. On timeout it fences/detaches the backend and waits for the canceled
// transaction to acknowledge before ownership cleanup proceeds.
func (g *controlGate) runBoundedRestartEffect(entry *controlEntry, raw func() (SessionMutationResult, error)) (SessionMutationResult, error) {
	type restartResult struct {
		result SessionMutationResult
		err    error
	}
	done := make(chan restartResult, 1)
	go func() {
		result, err := raw()
		done <- restartResult{result: result, err: err}
	}()
	timer := time.NewTimer(controlRestartEffectTimeout)
	defer timer.Stop()
	select {
	case outcome := <-done:
		return outcome.result, outcome.err
	case <-timer.C:
		detach := g.quarantineBackend(entry)
		ack := time.NewTimer(controlCancelAckTimeout)
		defer ack.Stop()
		select {
		case <-done:
		case <-ack.C:
		}
		return SessionMutationResult{}, &ControlGateError{Kind: DenyOperationTimeout, Detach: detach.disposition}
	}
}

// ---------------------------------------------------------------------------
// Permit creation (stateMu-held validation)
// ---------------------------------------------------------------------------

// createDesktopPermit validates desktop holder + active run under stateMu and
// creates an operation permit.
func (g *controlGate) createDesktopPermit(ctx context.Context, entry *controlEntry, authority *DesktopAuthority) (*operationPermit, *ControlGateError) {
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	if entry.owner.kind != ownerDesktop || entry.owner.desktopSource != authority.source {
		return nil, &ControlGateError{Kind: DenyNotController}
	}
	if entry.runPhase != runActive {
		return nil, &ControlGateError{Kind: DenySessionNotWritable}
	}
	if entry.backend == backendQuarantined {
		return nil, &ControlGateError{Kind: DenyControlUnavailable, Detach: entry.backendDetach.disposition}
	}
	return g.mintOperationPermit(ctx, entry, contract.DeviceID(""), authority.source), nil
}

// createDevicePTYPermit validates exact device holder + live lease under stateMu.
func (g *controlGate) createDevicePTYPermit(ctx context.Context, entry *controlEntry, lease *ControlConnectionLease) (*operationPermit, *ControlGateError) {
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	if entry.owner.kind != ownerDevice {
		return nil, &ControlGateError{Kind: DenyNotController}
	}
	if entry.owner.deviceID != lease.DeviceID() ||
		entry.owner.connectionID != lease.ConnectionID() ||
		entry.owner.attachmentGeneration != lease.AttachmentGeneration() {
		return nil, &ControlGateError{Kind: DenyNotController}
	}
	if entry.owner.phase != deviceConnected {
		// Grace: WS input forbidden (no live exact lease).
		return nil, &ControlGateError{Kind: DenyNotController}
	}
	// M-002: a revoked device / non-accepting server must be stably rejected in
	// the revoke window (before registry FenceDevice/Terminate completes). Lock
	// order is stateMu → permitMu (no path nests permitMu → stateMu).
	if !g.arbiter.acceptingRemote.Load() {
		return nil, &ControlGateError{Kind: DenyNotAccepting}
	}
	g.arbiter.permitMu.Lock()
	revoked := g.arbiter.revokedDevices[lease.DeviceID()]
	g.arbiter.permitMu.Unlock()
	if revoked {
		return nil, &ControlGateError{Kind: DenyDeviceRevoked}
	}
	if entry.runPhase != runActive {
		return nil, &ControlGateError{Kind: DenySessionNotWritable}
	}
	if entry.backend == backendQuarantined {
		return nil, &ControlGateError{Kind: DenyControlUnavailable, Detach: entry.backendDetach.disposition}
	}
	return g.mintOperationPermit(ctx, entry, lease.DeviceID(), 0), nil
}

// mintOperationPermit creates and registers an operation permit. Caller holds
// stateMu.
func (g *controlGate) mintOperationPermit(ctx context.Context, entry *controlEntry, deviceID contract.DeviceID, desktopSource uint64) *operationPermit {
	opCtx, cancel := context.WithCancelCause(ctx)
	entry.operationSeq++
	permit := &operationPermit{
		id:                   entry.operationSeq,
		entry:                entry,
		arbiter:              g.arbiter,
		lane:                 entry.opLane,
		opSeq:                entry.operationSeq,
		deviceID:             deviceID,
		desktopSource:        desktopSource,
		acceptanceGeneration: g.arbiter.acceptanceGeneration.Load(),
		runtimeGeneration:    g.arbiter.runtimeGeneration.Load(),
		controlEpoch:         entry.controlEpoch,
		run:                  entry.currentRun,
		runEpoch:             entry.runEpoch,
		backendEpoch:         entry.backendEpoch,
		opCtx:                opCtx,
		cancel:               cancel,
		done:                 make(chan struct{}),
	}
	entry.currentOp = permit
	return permit
}

// finishOperation clears the current operation under stateMu (exact-match).
func (g *controlGate) finishOperation(entry *controlEntry, permit *operationPermit) {
	entry.stateMu.Lock()
	if entry.currentOp == permit && entry.operationSeq == permit.opSeq {
		entry.currentOp = nil
	}
	permit.finish()
	entry.stateMu.Unlock()
}

// finishLifecycleResult completes a lifecycle operation and reports whether the
// raw completion is still authoritative (N-002, design §4A.1 "result commit只
// 接受同一 permit/session/generation/intent/currentOp"; §5.5 "stale completion
// 不得物理删 entry").
//
// A fence (revoke / Server Stop / holder takeover / run swap) that linearizes
// after the permit's last successful Checkpoint makes this completion stale: it
// releases its own lane but MUST NOT commit state — no tombstone, no physical
// entry delete, no success result/event. The caller gates the remove/return on
// the returned authoritative flag. Caller does NOT hold stateMu.
func (g *controlGate) finishLifecycleResult(entry *controlEntry, permit *operationPermit) bool {
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	authoritative := !permit.fenced.Load() &&
		entry.currentOp == permit &&
		entry.operationSeq == permit.opSeq &&
		entry.controlEpoch == permit.controlEpoch &&
		entry.currentRun == permit.run &&
		entry.runEpoch == permit.runEpoch &&
		entry.backendEpoch == permit.backendEpoch
	// Always release our own lane slot (exact pointer+seq) and signal done.
	if entry.currentOp == permit && entry.operationSeq == permit.opSeq {
		entry.currentOp = nil
	}
	permit.finish()
	return authoritative
}

// ---------------------------------------------------------------------------
// Lifecycle bounded write (two-phase)
// ---------------------------------------------------------------------------

func (g *controlGate) doBoundedLifecycle(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID, op LifecycleOperation, mutate RawSessionEffect) (SessionMutationResult, error) {
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return SessionMutationResult{}, &ControlGateError{Kind: DenySessionNotFound}
	}

	// Phase 1: commit lifecycle intent under stateMu (fence regular writes).
	intentID, gErr := g.commitLifecycleIntent(entry, op)
	if gErr != nil {
		return SessionMutationResult{}, gErr
	}

	// Acquire lane.
	if err := g.acquireLaneOrQuarantine(ctx, entry); err != nil {
		// M-001: a lane timeout closes our own intent so it does not block a
		// concurrent lifecycle (ownership-aware; a fenced/replacement intent is
		// left untouched).
		g.abandonLifecycleIntent(entry, intentID)
		return SessionMutationResult{}, err
	}
	defer entry.opLane.release()

	// Phase 2: exact-match intent + create drain permit.
	permit, gErr := g.createLifecyclePermit(ctx, entry, authority, intentID, op)
	if gErr != nil {
		// In particular, an unconfirmed quarantine detach must not leave its
		// phase-1 drain intent blocking the exact desktop recovery retry.
		g.abandonLifecycleIntent(entry, intentID)
		return SessionMutationResult{}, gErr
	}

	// Execute raw lifecycle effect, bounded (M-009).
	result, rawErr := g.runBoundedLifecycleEffect(entry, func() (SessionMutationResult, error) { return mutate(permit.ctx(), permit) })
	authoritative := g.finishLifecycleResult(entry, permit)
	if rawErr != nil {
		return SessionMutationResult{}, rawErr
	}
	// N-002: a fenced/stale completion (epoch advanced after the last Checkpoint)
	// released its lane but must not commit state — no tombstone, no success.
	if !authoritative {
		return SessionMutationResult{}, &ControlGateError{Kind: DenyStalePermit}
	}
	// For remove: tombstone after raw effect succeeds AND completion is authoritative.
	if op == LifecycleRemove && result.Removed {
		if gErr := g.arbiter.removeSession(sessionID); gErr != nil {
			return result, gErr
		}
		g.arbiter.physicallyDeleteEntry(sessionID)
	}
	return result, nil
}

func (g *controlGate) doBoundedDeviceLifecycle(ctx context.Context, principal DevicePrincipal, sessionID contract.SessionID, op LifecycleOperation, mutate RawSessionEffect) (SessionMutationResult, error) {
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return SessionMutationResult{}, &ControlGateError{Kind: DenySessionNotFound}
	}
	// Device lifecycle uses the same two-phase path with device holder validation.
	// Phase 1: intent.
	intentID, gErr := g.commitLifecycleIntent(entry, op)
	if gErr != nil {
		return SessionMutationResult{}, gErr
	}
	if err := g.acquireLaneOrQuarantine(ctx, entry); err != nil {
		g.abandonLifecycleIntent(entry, intentID)
		return SessionMutationResult{}, err
	}
	defer entry.opLane.release()
	// Phase 2: validate device holder + intent.
	permit, gErr := g.createDeviceLifecyclePermit(ctx, entry, principal, intentID, op)
	if gErr != nil {
		// Phase-2 admission failed (for example, quarantine committed after the
		// phase-1 intent). Close only this transaction's intent so the trusted
		// desktop recovery path is not left blocked by a dangling drain.
		g.abandonLifecycleIntent(entry, intentID)
		return SessionMutationResult{}, gErr
	}
	result, rawErr := g.runBoundedLifecycleEffect(entry, func() (SessionMutationResult, error) { return mutate(permit.ctx(), permit) })
	authoritative := g.finishLifecycleResult(entry, permit)
	if rawErr != nil {
		return SessionMutationResult{}, rawErr
	}
	if !authoritative {
		// N-002: stale completion — must not tombstone/remove/return success.
		return SessionMutationResult{}, &ControlGateError{Kind: DenyStalePermit}
	}
	if op == LifecycleRemove && result.Removed {
		if gErr := g.arbiter.removeSession(sessionID); gErr != nil {
			return result, gErr
		}
		g.arbiter.physicallyDeleteEntry(sessionID)
	}
	return result, nil
}

// PreparedControlRestart retains the exact lifecycle lane/permit after the old
// binding is closed and the new process/effects are staged. Authority commits
// it only with a PreparedCompositeRestart.
type PreparedControlRestart struct {
	gate            *controlGate
	entry           *controlEntry
	permit          *operationPermit
	activation      *PreparedCompositeRestart
	ready           bool
	committed       bool
	consumed        bool
	postCommitFence bool
	resolved        chan struct{}
}

func (g *controlGate) prepareDeviceRestart(ctx context.Context, principal DevicePrincipal, sessionID contract.SessionID, mutate RawSessionEffect) (*PreparedControlRestart, error) {
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	intentID, gateErr := g.commitLifecycleIntent(entry, LifecycleRestart)
	if gateErr != nil {
		return nil, gateErr
	}
	if err := g.acquireLaneOrQuarantine(ctx, entry); err != nil {
		g.abandonLifecycleIntent(entry, intentID)
		return nil, err
	}
	permit, gateErr := g.createDeviceLifecyclePermit(ctx, entry, principal, intentID, LifecycleRestart)
	if gateErr != nil {
		entry.opLane.release()
		g.abandonLifecycleIntent(entry, intentID)
		return nil, gateErr
	}
	token := &PreparedControlRestart{gate: g, entry: entry, permit: permit, resolved: make(chan struct{})}
	result, rawErr := g.runBoundedRestartEffect(entry, func() (SessionMutationResult, error) { return mutate(permit.ctx(), permit) })
	if rawErr != nil || !result.StateChanged || permit.restartStage == nil {
		g.finishLifecycleResult(entry, permit)
		entry.opLane.release()
		if rawErr != nil {
			return nil, rawErr
		}
		return nil, &ControlGateError{Kind: DenyControlUnavailable}
	}
	entry.stateMu.Lock()
	if !lifecyclePermitAuthoritativeLocked(entry, permit) || entry.removed || entry.preparedRestart != nil || entry.preparedStop != nil || entry.preparedRemoval != nil {
		entry.stateMu.Unlock()
		g.finishLifecycleResult(entry, permit)
		entry.opLane.release()
		return nil, &ControlGateError{Kind: DenyStalePermit}
	}
	entry.preparedRestart = token
	token.ready = true
	entry.stateMu.Unlock()
	return token, nil
}

func (g *controlGate) bindPreparedControlRestart(token *PreparedControlRestart, activation *PreparedCompositeRestart) error {
	if token == nil || activation == nil || token.gate != g || token.entry == nil || !token.ready || token.committed || token.consumed {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	token.entry.stateMu.Lock()
	defer token.entry.stateMu.Unlock()
	if token.entry.preparedRestart != token || activation.entry != token.entry || activation.permit != token.permit {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	token.activation = activation
	if token.postCommitFence {
		activation.postCommitFence = true
	}
	return nil
}

func (g *controlGate) commitPreparedControlRestartNoFail(token *PreparedControlRestart) {
	if token == nil || token.gate != g || token.entry == nil || token.activation == nil || !token.ready || token.consumed || token.committed {
		panic("remote: invalid prepared control restart")
	}
	entry := token.entry
	entry.stateMu.Lock()
	if entry.preparedRestart != token || entry.currentOp != token.permit {
		entry.stateMu.Unlock()
		panic("remote: prepared control restart ownership changed")
	}
	entry.currentOp = nil
	token.committed = true
	entry.stateMu.Unlock()
}

func (g *controlGate) finishPreparedControlRestart(token *PreparedControlRestart) {
	if token == nil || token.gate != g || !token.committed || token.consumed {
		return
	}
	token.entry.stateMu.Lock()
	if token.entry.preparedRestart == token {
		token.entry.preparedRestart = nil
	}
	token.entry.stateMu.Unlock()
	token.permit.finish()
	token.entry.opLane.release()
	token.consumed = true
	close(token.resolved)
}

func (g *controlGate) abortPreparedControlRestart(token *PreparedControlRestart) {
	if token == nil || token.gate != g || token.entry == nil || token.permit == nil || token.consumed || token.committed {
		return
	}
	token.entry.stateMu.Lock()
	if token.entry.preparedRestart == token {
		token.entry.preparedRestart = nil
	}
	if token.entry.currentOp == token.permit && token.entry.operationSeq == token.permit.opSeq {
		token.entry.currentOp = nil
	}
	token.entry.stateMu.Unlock()
	token.permit.finish()
	token.entry.opLane.release()
	token.consumed = true
	close(token.resolved)
}

type PreparedControlStop struct {
	gate            *controlGate
	entry           *controlEntry
	permit          *operationPermit
	ready           bool
	committed       bool
	consumed        bool
	postCommitFence bool
	resolved        chan struct{}
}

func (g *controlGate) prepareDeviceStop(ctx context.Context, principal DevicePrincipal, sessionID contract.SessionID, mutate RawSessionEffect) (*PreparedControlStop, error) {
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	intentID, gateErr := g.commitLifecycleIntent(entry, LifecycleStop)
	if gateErr != nil {
		return nil, gateErr
	}
	if err := g.acquireLaneOrQuarantine(ctx, entry); err != nil {
		g.abandonLifecycleIntent(entry, intentID)
		return nil, err
	}
	permit, gateErr := g.createDeviceLifecyclePermit(ctx, entry, principal, intentID, LifecycleStop)
	if gateErr != nil {
		entry.opLane.release()
		g.abandonLifecycleIntent(entry, intentID)
		return nil, gateErr
	}
	token := &PreparedControlStop{gate: g, entry: entry, permit: permit, resolved: make(chan struct{})}
	result, rawErr := g.runBoundedLifecycleEffect(entry, func() (SessionMutationResult, error) { return mutate(permit.ctx(), permit) })
	if rawErr != nil || !result.StateChanged {
		g.finishLifecycleResult(entry, permit)
		entry.opLane.release()
		if rawErr != nil {
			return nil, rawErr
		}
		return nil, &ControlGateError{Kind: DenyControlUnavailable}
	}
	if gateErr := g.sealPreparedControlStop(token); gateErr != nil {
		g.finishLifecycleResult(entry, permit)
		entry.opLane.release()
		return nil, gateErr
	}
	return token, nil
}

func (g *controlGate) prepareDesktopStop(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID, mutate RawSessionEffect) (*PreparedControlStop, error) {
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	intentID, gateErr := g.commitLifecycleIntent(entry, LifecycleStop)
	if gateErr != nil {
		return nil, gateErr
	}
	if err := g.acquireLaneOrQuarantine(ctx, entry); err != nil {
		g.abandonLifecycleIntent(entry, intentID)
		return nil, err
	}
	permit, gateErr := g.createLifecyclePermit(ctx, entry, authority, intentID, LifecycleStop)
	if gateErr != nil {
		entry.opLane.release()
		g.abandonLifecycleIntent(entry, intentID)
		return nil, gateErr
	}
	token := &PreparedControlStop{gate: g, entry: entry, permit: permit, resolved: make(chan struct{})}
	result, rawErr := g.runBoundedLifecycleEffect(entry, func() (SessionMutationResult, error) { return mutate(permit.ctx(), permit) })
	if rawErr != nil || !result.StateChanged {
		g.finishLifecycleResult(entry, permit)
		entry.opLane.release()
		if rawErr != nil {
			return nil, rawErr
		}
		return nil, &ControlGateError{Kind: DenyControlUnavailable}
	}
	if gateErr := g.sealPreparedControlStop(token); gateErr != nil {
		g.finishLifecycleResult(entry, permit)
		entry.opLane.release()
		return nil, gateErr
	}
	return token, nil
}

func (g *controlGate) sealPreparedControlStop(token *PreparedControlStop) *ControlGateError {
	if token == nil || token.entry == nil || token.permit == nil {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	entry := token.entry
	entry.stateMu.Lock()
	if !lifecyclePermitAuthoritativeLocked(entry, token.permit) || entry.removed || entry.preparedStop != nil || entry.preparedRemoval != nil {
		entry.stateMu.Unlock()
		return &ControlGateError{Kind: DenyStalePermit}
	}
	entry.preparedStop = token
	token.ready = true
	entry.stateMu.Unlock()
	return nil
}

func (g *controlGate) commitPreparedControlStopNoFail(token *PreparedControlStop) {
	if token == nil || token.gate != g || token.entry == nil || token.permit == nil || !token.ready || token.consumed || token.committed {
		panic("remote: invalid prepared control stop")
	}
	entry := token.entry
	entry.stateMu.Lock()
	if entry.preparedStop != token {
		entry.stateMu.Unlock()
		panic("remote: prepared control stop ownership changed")
	}
	entry.runPhase = runTerminal
	entry.stateMirror = contract.SessionStateStopped
	entry.stateMirrorSet = true
	entry.currentOp = nil
	token.committed = true
	entry.stateMu.Unlock()
}

func (g *controlGate) finishPreparedControlStop(token *PreparedControlStop) {
	if token == nil || token.gate != g || !token.committed || token.consumed {
		return
	}
	token.entry.stateMu.Lock()
	if token.entry.preparedStop == token {
		token.entry.preparedStop = nil
	}
	token.entry.stateMu.Unlock()
	token.permit.finish()
	token.entry.opLane.release()
	token.consumed = true
	close(token.resolved)
}

func (g *controlGate) abortPreparedControlStop(token *PreparedControlStop) {
	if token == nil || token.gate != g || token.entry == nil || token.permit == nil || token.consumed || token.committed {
		return
	}
	token.entry.stateMu.Lock()
	if token.entry.preparedStop == token {
		token.entry.preparedStop = nil
	}
	if token.entry.currentOp == token.permit && token.entry.operationSeq == token.permit.opSeq {
		token.entry.currentOp = nil
	}
	token.entry.stateMu.Unlock()
	token.permit.finish()
	token.entry.opLane.release()
	token.consumed = true
	close(token.resolved)
}

// PreparedControlRemove retains the exact lifecycle lane/permit after the raw
// process close. SessionAuthority commits this token together with membership
// and the pre-reserved H3 terminal ticket.
type PreparedControlRemove struct {
	gate                 *controlGate
	entry                *controlEntry
	permit               *operationPermit
	sessionID            contract.SessionID
	nextControlEpoch     uint64
	nextHolderGeneration HolderGeneration
	nextBackendEpoch     uint64
	advanceHolder        bool
	graceTimer           securityTimer
	ready                bool
	committed            bool
	consumed             bool
	postCommitFence      bool
	resolved             chan struct{}
}

func (g *controlGate) prepareDeviceRemove(ctx context.Context, principal DevicePrincipal, sessionID contract.SessionID, mutate RawSessionEffect) (*PreparedControlRemove, error) {
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	intentID, gateErr := g.commitLifecycleIntent(entry, LifecycleRemove)
	if gateErr != nil {
		return nil, gateErr
	}
	if err := g.acquireLaneOrQuarantine(ctx, entry); err != nil {
		g.abandonLifecycleIntent(entry, intentID)
		return nil, err
	}
	permit, gateErr := g.createDeviceLifecyclePermit(ctx, entry, principal, intentID, LifecycleRemove)
	if gateErr != nil {
		entry.opLane.release()
		g.abandonLifecycleIntent(entry, intentID)
		return nil, gateErr
	}
	token := &PreparedControlRemove{gate: g, entry: entry, permit: permit, sessionID: sessionID, resolved: make(chan struct{})}
	result, rawErr := g.runBoundedLifecycleEffect(entry, func() (SessionMutationResult, error) { return mutate(permit.ctx(), permit) })
	if rawErr != nil || !result.Removed {
		g.finishLifecycleResult(entry, permit)
		entry.opLane.release()
		if rawErr != nil {
			return nil, rawErr
		}
		return nil, &ControlGateError{Kind: DenyControlUnavailable}
	}
	if gateErr := g.sealPreparedControlRemove(token); gateErr != nil {
		g.finishLifecycleResult(entry, permit)
		entry.opLane.release()
		return nil, gateErr
	}
	return token, nil
}

func (g *controlGate) prepareDesktopRemove(ctx context.Context, authority *DesktopAuthority, sessionID contract.SessionID, mutate RawSessionEffect) (*PreparedControlRemove, error) {
	entry := g.arbiter.entryFor(sessionID)
	if !isEntryPublic(entry) {
		return nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	intentID, gateErr := g.commitLifecycleIntent(entry, LifecycleRemove)
	if gateErr != nil {
		return nil, gateErr
	}
	if err := g.acquireLaneOrQuarantine(ctx, entry); err != nil {
		g.abandonLifecycleIntent(entry, intentID)
		return nil, err
	}
	permit, gateErr := g.createLifecyclePermit(ctx, entry, authority, intentID, LifecycleRemove)
	if gateErr != nil {
		entry.opLane.release()
		g.abandonLifecycleIntent(entry, intentID)
		return nil, gateErr
	}
	token := &PreparedControlRemove{gate: g, entry: entry, permit: permit, sessionID: sessionID, resolved: make(chan struct{})}
	result, rawErr := g.runBoundedLifecycleEffect(entry, func() (SessionMutationResult, error) { return mutate(permit.ctx(), permit) })
	if rawErr != nil || !result.Removed {
		g.finishLifecycleResult(entry, permit)
		entry.opLane.release()
		if rawErr != nil {
			return nil, rawErr
		}
		return nil, &ControlGateError{Kind: DenyControlUnavailable}
	}
	if gateErr := g.sealPreparedControlRemove(token); gateErr != nil {
		g.finishLifecycleResult(entry, permit)
		entry.opLane.release()
		return nil, gateErr
	}
	return token, nil
}

func lifecyclePermitAuthoritativeLocked(entry *controlEntry, permit *operationPermit) bool {
	return entry != nil && permit != nil && !permit.fenced.Load() && entry.currentOp == permit &&
		entry.operationSeq == permit.opSeq && entry.controlEpoch == permit.controlEpoch &&
		entry.currentRun == permit.run && entry.runEpoch == permit.runEpoch && entry.backendEpoch == permit.backendEpoch
}

func (g *controlGate) sealPreparedControlRemove(token *PreparedControlRemove) *ControlGateError {
	if token == nil || token.entry == nil || token.permit == nil {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	entry := token.entry
	entry.stateMu.Lock()
	if !lifecyclePermitAuthoritativeLocked(entry, token.permit) || entry.removed || entry.preparedStop != nil || entry.preparedRemoval != nil {
		entry.stateMu.Unlock()
		return &ControlGateError{Kind: DenyStalePermit}
	}
	controlEpoch, controlOK := nextEpoch(entry.controlEpoch)
	backendEpoch, backendOK := nextEpoch(entry.backendEpoch)
	holderEpoch := uint64(entry.holderGeneration)
	holderOK := true
	if entry.owner.kind != ownerNone {
		holderEpoch, holderOK = nextEpoch(holderEpoch)
		token.advanceHolder = true
	}
	if !controlOK || !backendOK || !holderOK {
		entry.stateMu.Unlock()
		g.arbiter.healthLatched.Store(true)
		return &ControlGateError{Kind: DenyControlUnavailable}
	}
	token.nextControlEpoch = controlEpoch
	token.nextBackendEpoch = backendEpoch
	token.nextHolderGeneration = HolderGeneration(holderEpoch)
	token.graceTimer = entry.graceTimer
	entry.graceTimer = nil
	entry.graceDesc = graceTimerDescriptor{}
	entry.preparedRemoval = token
	token.ready = true
	entry.stateMu.Unlock()
	if token.graceTimer != nil {
		token.graceTimer.Stop()
		token.graceTimer = nil
	}
	return nil
}

func (g *controlGate) commitPreparedControlRemoveNoFail(token *PreparedControlRemove) {
	if token == nil || token.gate != g || token.entry == nil || token.permit == nil || !token.ready || token.consumed || token.committed {
		panic("remote: invalid prepared control remove")
	}
	entry := token.entry
	entry.stateMu.Lock()
	if entry.preparedRemoval != token {
		entry.stateMu.Unlock()
		panic("remote: prepared control remove ownership changed")
	}
	entry.owner = controlOwner{kind: ownerNone}
	entry.controlEpoch = token.nextControlEpoch
	if token.advanceHolder {
		entry.holderGeneration = token.nextHolderGeneration
	}
	entry.backendEpoch = token.nextBackendEpoch
	entry.removed = true
	entry.runPhase = runTerminal
	entry.currentRun = nil
	entry.stateMirror = contract.SessionStateRemoved
	entry.stateMirrorSet = true
	entry.currentOp = nil
	token.committed = true
	entry.stateMu.Unlock()
}

func (g *controlGate) finishPreparedControlRemove(token *PreparedControlRemove) {
	if token == nil || token.gate != g || !token.committed || token.consumed {
		return
	}
	token.entry.stateMu.Lock()
	if token.entry.preparedRemoval == token {
		token.entry.preparedRemoval = nil
	}
	token.entry.stateMu.Unlock()
	token.permit.finish()
	token.entry.opLane.release()
	token.consumed = true
	close(token.resolved)
}

func (g *controlGate) abortPreparedControlRemove(token *PreparedControlRemove) {
	if token == nil || token.gate != g || token.entry == nil || token.permit == nil || token.consumed || token.committed {
		return
	}
	token.entry.stateMu.Lock()
	if token.entry.preparedRemoval == token {
		token.entry.preparedRemoval = nil
	}
	if token.entry.currentOp == token.permit && token.entry.operationSeq == token.permit.opSeq {
		token.entry.currentOp = nil
	}
	token.entry.stateMu.Unlock()
	token.permit.finish()
	token.entry.opLane.release()
	token.consumed = true
	close(token.resolved)
}

// commitLifecycleIntent commits a lifecycle drain intent under stateMu (phase 1).
// M-001: it uses an INDEPENDENT monotonic intent sequence (lifecycleIntentSeq,
// not operationSeq+1), DENIES with lifecycle.in_progress when an active (non-
// closed) intent already exists, and records holderGeneration so phase-2 can
// exact-match. A closed (fenced) intent is superseded and may be replaced.
// Fencing the current regular write so the lifecycle can proceed. Returns the
// intent ID for phase-2 exact-match.
func (g *controlGate) commitLifecycleIntent(entry *controlEntry, op LifecycleOperation) (uint64, *ControlGateError) {
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return 0, &ControlGateError{Kind: DenySessionNotFound}
	}
	// Phase 1 intentionally records the intent even on quarantine so admission can
	// make the actor-specific decision atomically in phase 2: device lifecycle is
	// denied; desktop recovery requires an exact confirmed detach receipt. A
	// phase-2 denial abandons only this intent, so a later reaper confirmation can
	// be retried without a dangling lifecycle drain.
	// An active pending lifecycle intent must NOT be overwritten: a concurrent
	// stop/restart/remove gets a typed lifecycle.in_progress denial instead of
	// clobbering the in-flight intent (design §9.1.2, M-001).
	if entry.pendingDrain != nil && entry.pendingDrain.closed == nil {
		return 0, &ControlGateError{Kind: DenyLifecycleInProgress}
	}
	// Independent monotonic intent sequence (design §4A.1).
	entry.lifecycleIntentSeq++
	intentID := uint64(entry.lifecycleIntentSeq)
	entry.pendingDrain = &lifecycleDrainIntent{
		id:               intentID,
		run:              entry.currentRun,
		runEpoch:         entry.runEpoch,
		kind:             op,
		holderGeneration: entry.holderGeneration,
	}
	// Fence the current operation (regular write) so the lifecycle can proceed.
	g.arbiter.fenceCurrentOpLocked(entry)
	return intentID, nil
}

// exactMatchPendingDrain reports whether the entry's current pendingDrain is
// the exact same intent {id, kind, run pointer, runEpoch, holderGeneration} and
// has not been closed by a fence (design §4A.1, M-001 phase-2). Caller holds
// stateMu. A nil / closed / superseded intent returns false so phase-2 denies
// WITHOUT clearing a replacement intent.
func exactMatchPendingDrain(entry *controlEntry, intentID uint64, op LifecycleOperation) bool {
	pd := entry.pendingDrain
	if pd == nil || pd.closed != nil {
		return false
	}
	return pd.id == intentID &&
		pd.kind == op &&
		pd.run == entry.currentRun &&
		pd.runEpoch == entry.runEpoch &&
		pd.holderGeneration == entry.holderGeneration
}

// abandonLifecycleIntent closes the phase-1 intent identified by intentID when
// the lane could not be acquired (timeout/quarantine), so it does not block a
// concurrent lifecycle. It only closes its OWN active intent (ownership-aware):
// a fence-superseded or replacement intent is left untouched. Caller does NOT
// hold stateMu.
func (g *controlGate) abandonLifecycleIntent(entry *controlEntry, intentID uint64) {
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.pendingDrain != nil && entry.pendingDrain.id == intentID && entry.pendingDrain.closed == nil {
		entry.pendingDrain.closed = &lifecycleClosedOutcome{
			reason:     LifecycleClosedAborted,
			generation: entry.holderGeneration,
		}
	}
}

func (g *controlGate) createLifecyclePermit(ctx context.Context, entry *controlEntry, authority *DesktopAuthority, intentID uint64, op LifecycleOperation) (*operationPermit, *ControlGateError) {
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	// M-001 phase-2: exact-match the intent before consuming it. A fenced /
	// superseded / stale intent denies WITHOUT clearing a replacement intent.
	if !exactMatchPendingDrain(entry, intentID, op) {
		return nil, &ControlGateError{Kind: DenyStalePermit}
	}
	if entry.owner.kind != ownerDesktop || entry.owner.desktopSource != authority.source {
		return nil, &ControlGateError{Kind: DenyNotController}
	}
	// R4-004: trusted desktop recovery is allowed only after the PTY receipt has
	// confirmed detach of this exact backendEpoch. A nil/failed/stale receipt
	// remains quarantined but cannot start/stop/remove by SessionID.
	if entry.backend == backendQuarantined && !exactBackendDetachConfirmedLocked(entry) {
		return nil, &ControlGateError{Kind: DenyControlUnavailable, Detach: entry.backendDetach.disposition}
	}
	// Consume: clear the pending drain (exact-match confirmed). Capture the H1
	// intent stub first so a restart raw effect can seal/commit with exact intent
	// identity (M-004).
	var intentStub *LifecycleIntentStub
	if pd := entry.pendingDrain; pd != nil {
		intentStub = &LifecycleIntentStub{
			id:               lifecycleIntentID(pd.id),
			sessionID:        entry.sessionID,
			holderGeneration: pd.holderGeneration,
		}
	}
	entry.pendingDrain = nil
	p := g.mintOperationPermit(ctx, entry, contract.DeviceID(""), authority.source)
	p.restartIntent = intentStub
	p.lifecycle = true
	return p, nil
}

func (g *controlGate) createDeviceLifecyclePermit(ctx context.Context, entry *controlEntry, principal DevicePrincipal, intentID uint64, op LifecycleOperation) (*operationPermit, *ControlGateError) {
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed {
		return nil, &ControlGateError{Kind: DenySessionNotFound}
	}
	if !exactMatchPendingDrain(entry, intentID, op) {
		return nil, &ControlGateError{Kind: DenyStalePermit}
	}
	if entry.owner.kind != ownerDevice || entry.owner.deviceID != principal.DeviceID {
		return nil, &ControlGateError{Kind: DenyNotController}
	}
	// R4-004 asymmetric recovery boundary: a remote device must not run
	// lifecycle effects while detach state is unknown. Only the trusted desktop
	// path may recover a quarantined backend; createLifecyclePermit intentionally
	// remains permissive for that exact recovery path.
	if entry.backend == backendQuarantined {
		return nil, &ControlGateError{Kind: DenyControlUnavailable, Detach: entry.backendDetach.disposition}
	}
	// Consume: clear the pending drain (exact-match confirmed). Capture the H1
	// intent stub first (M-004).
	var intentStub *LifecycleIntentStub
	if pd := entry.pendingDrain; pd != nil {
		intentStub = &LifecycleIntentStub{
			id:               lifecycleIntentID(pd.id),
			sessionID:        entry.sessionID,
			holderGeneration: pd.holderGeneration,
		}
	}
	entry.pendingDrain = nil
	p := g.mintOperationPermit(ctx, entry, principal.DeviceID, 0)
	p.restartIntent = intentStub
	p.lifecycle = true
	return p, nil
}

// ---------------------------------------------------------------------------
// Launch staging / fencing (design §6.4)
// ---------------------------------------------------------------------------

func (g *controlGate) BeginDesktopLaunch(ctx context.Context, authority *DesktopAuthority) (*LaunchPermit, error) {
	if err := g.arbiter.checkReady(); err != nil {
		return nil, err
	}
	if authority == nil {
		return nil, &ControlGateError{Kind: DenyControlUnavailable}
	}
	launchCtx, cancel := context.WithCancelCause(ctx)
	p := &LaunchPermit{
		isDesktop:            true,
		authority:            authority,
		runtimeGeneration:    g.arbiter.runtimeGeneration.Load(),
		launchGeneration:     g.arbiter.launchGeneration.Add(1),
		acceptanceGeneration: 0, // desktop permits don't track acceptance
		ctx:                  launchCtx,
		cancelFn:             cancel,
	}
	return p, nil
}

func (g *controlGate) BeginDeviceLaunch(ctx context.Context, principal DevicePrincipal) (*LaunchPermit, error) {
	if err := g.arbiter.checkReady(); err != nil {
		return nil, err
	}
	if !g.arbiter.acceptingRemote.Load() {
		return nil, &ControlGateError{Kind: DenyNotAccepting}
	}
	g.arbiter.permitMu.Lock()
	revoked := g.arbiter.revokedDevices[principal.DeviceID]
	g.arbiter.permitMu.Unlock()
	if revoked {
		return nil, &ControlGateError{Kind: DenyDeviceRevoked}
	}
	launchCtx, cancel := context.WithCancelCause(ctx)
	p := &LaunchPermit{
		deviceID:             principal.DeviceID,
		isDesktop:            false,
		acceptanceGeneration: g.arbiter.acceptanceGeneration.Load(),
		runtimeGeneration:    g.arbiter.runtimeGeneration.Load(),
		launchGeneration:     g.arbiter.launchGeneration.Add(1),
		ctx:                  launchCtx,
		cancelFn:             cancel,
	}
	// Index the pending permit.
	g.arbiter.permitMu.Lock()
	g.arbiter.pendingLaunch[p.launchGeneration] = p
	if g.arbiter.pendingByDevice[principal.DeviceID] == nil {
		g.arbiter.pendingByDevice[principal.DeviceID] = make(map[uint64]*LaunchPermit)
	}
	g.arbiter.pendingByDevice[principal.DeviceID][p.launchGeneration] = p
	g.arbiter.permitMu.Unlock()
	return p, nil
}

func (g *controlGate) RegisterStartingSession(ctx context.Context, p *LaunchPermit, id contract.SessionID) (*RunPermit, *RunObservationPermit, error) {
	rp, rop, gErr := g.arbiter.registerStartingSession(id, p)
	if gErr != nil {
		return nil, nil, gErr
	}
	return rp, rop, nil
}

// DoLaunchEffect executes one launch step with checkpoint + receipt registration
// (design §6.4.1 step 5). The receipt is registered before the raw call; the
// raw effect fills ownership data + compensate before the first syscall.
func (g *controlGate) DoLaunchEffect(ctx context.Context, p *RunPermit, kind LaunchEffectKind, effect RawLaunchEffect) error {
	if p == nil {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	// Revalidate launch permit.
	if gErr := p.launch.revalidate(g.arbiter); gErr != nil {
		return gErr
	}
	// Register receipt for compensation.
	receipt := &EffectReceipt{
		ownerLaunchGeneration: p.launch.launchGeneration,
		kind:                  kind,
	}
	p.launch.compensationStack = append(p.launch.compensationStack, receipt)

	// Create a bounded operation permit for this effect (lane + checkpoint).
	entry := p.entry
	effectCtx, cancel := context.WithTimeout(ctx, controlLaunchStepTimeout)
	defer cancel()

	if err := g.acquireLaneOrQuarantine(effectCtx, entry); err != nil {
		return err
	}
	defer entry.opLane.release()

	// Validate + create permit under stateMu.
	entry.stateMu.Lock()
	if entry.removed {
		entry.stateMu.Unlock()
		return &ControlGateError{Kind: DenySessionNotFound}
	}
	if entry.currentRun != p.run || entry.runEpoch != p.runEpoch {
		entry.stateMu.Unlock()
		return &ControlGateError{Kind: DenyStalePermit}
	}
	permit := g.mintLaunchPermit(effectCtx, entry, p)
	entry.stateMu.Unlock()

	// Execute raw effect.
	rawErr := effect(permit.ctx(), permit, receipt)
	g.finishOperation(entry, permit)
	return rawErr
}

// DoBootstrapPTY writes the auto-command via a bounded operation. The run must
// be in starting phase (design §6.4.1 step 6).
func (g *controlGate) DoBootstrapPTY(ctx context.Context, p *RunPermit, mutate RawEffect) error {
	if p == nil {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	if gErr := p.launch.revalidate(g.arbiter); gErr != nil {
		return gErr
	}
	entry := p.entry
	bsCtx, cancel := context.WithTimeout(ctx, controlLaunchStepTimeout)
	defer cancel()
	if err := g.acquireLaneOrQuarantine(bsCtx, entry); err != nil {
		return err
	}
	defer entry.opLane.release()
	entry.stateMu.Lock()
	if entry.removed || entry.currentRun != p.run || entry.runEpoch != p.runEpoch {
		entry.stateMu.Unlock()
		return &ControlGateError{Kind: DenyStalePermit}
	}
	permit := g.mintLaunchPermit(bsCtx, entry, p)
	entry.stateMu.Unlock()
	rawErr := mutate(permit.ctx(), permit)
	g.finishOperation(entry, permit)
	return rawErr
}

// ActivateRun transitions the staging entry to public (design §6.4.1 step 7).
func (g *controlGate) ActivateRun(ctx context.Context, p *RunPermit) error {
	if gErr := g.arbiter.activateRun(p); gErr != nil {
		return gErr
	}
	// Remove the launch permit from the pending index.
	g.arbiter.permitMu.Lock()
	delete(g.arbiter.pendingLaunch, p.launch.launchGeneration)
	if idx := g.arbiter.pendingByDevice[p.launch.deviceID]; idx != nil {
		delete(idx, p.launch.launchGeneration)
		if len(idx) == 0 && p.launch.deviceID != "" {
			delete(g.arbiter.pendingByDevice, p.launch.deviceID)
		}
	}
	g.arbiter.permitMu.Unlock()
	return nil
}

// AbortLaunch cancels the permit and performs reverse-order, idempotent,
// bounded compensation (design §6.4.3). If a staging entry was registered, it
// is deleted without a removed event (the client never saw it).
func (g *controlGate) AbortLaunch(ctx context.Context, p *LaunchPermit, cause error) {
	if p == nil {
		return
	}
	p.cancel(cause)

	// Reverse-order compensation.
	compCtx, cancel := context.WithTimeout(ctx, controlLaunchStepTimeout)
	defer cancel()
	for i := len(p.compensationStack) - 1; i >= 0; i-- {
		receipt := p.compensationStack[i]
		if receipt.IsApplied() && receipt.compensate != nil {
			_ = receipt.compensate(compCtx) // idempotent, ownership-aware
		}
	}

	// Remove from pending index.
	g.arbiter.permitMu.Lock()
	delete(g.arbiter.pendingLaunch, p.launchGeneration)
	if idx := g.arbiter.pendingByDevice[p.deviceID]; idx != nil {
		delete(idx, p.launchGeneration)
		if len(idx) == 0 && p.deviceID != "" {
			delete(g.arbiter.pendingByDevice, p.deviceID)
		}
	}
	g.arbiter.permitMu.Unlock()
}

// mintLaunchPermit creates an operation permit for a launch effect/bootstrap.
// Caller holds stateMu.
func (g *controlGate) mintLaunchPermit(ctx context.Context, entry *controlEntry, p *RunPermit) *operationPermit {
	entry.operationSeq++
	opCtx, cancel := context.WithCancelCause(ctx)
	permit := &operationPermit{
		id:                   entry.operationSeq,
		entry:                entry,
		arbiter:              g.arbiter,
		lane:                 entry.opLane,
		opSeq:                entry.operationSeq,
		deviceID:             p.launch.deviceID,
		acceptanceGeneration: p.launch.acceptanceGeneration,
		runtimeGeneration:    p.launch.runtimeGeneration,
		controlEpoch:         entry.controlEpoch,
		run:                  entry.currentRun,
		runEpoch:             entry.runEpoch,
		backendEpoch:         entry.backendEpoch,
		opCtx:                opCtx,
		cancel:               cancel,
		done:                 make(chan struct{}),
	}
	entry.currentOp = permit
	return permit
}

// ---------------------------------------------------------------------------
// Trusted observation
// ---------------------------------------------------------------------------

func (g *controlGate) ObserveExit(ctx context.Context, permit *RunObservationPermit, obs ProcessExitObservation) error {
	if g.arbiter.ObserveExit(permit, obs) {
		return nil
	}
	return nil // stale observation: silent no-op
}

func (g *controlGate) OnUnexpectedDetachForSession(sessionID contract.SessionID, lease *ControlConnectionLease, at time.Time) {
	g.arbiter.OnUnexpectedDetachForSession(sessionID, lease, at)
}

func (g *controlGate) RebindAttachment(sessionID contract.SessionID, newLease *ControlConnectionLease, at time.Time) bool {
	return g.arbiter.RebindAttachment(sessionID, newLease, at)
}

// ---------------------------------------------------------------------------
// Revoke / Server lifecycle (delegate to arbiter)
// ---------------------------------------------------------------------------

func (g *controlGate) MarkDeviceRevoked(deviceID contract.DeviceID) {
	g.arbiter.MarkDeviceRevoked(deviceID)
}

func (g *controlGate) ReleaseRevokedDevice(notice DeviceRevocationNotice) {
	g.arbiter.ReleaseRevokedDevice(notice)
}

func (g *controlGate) FenceAllRemote() {
	g.arbiter.FenceAllRemote()
}

func (g *controlGate) ReleaseAllRemote() {
	g.arbiter.ReleaseAllRemote(reasonServiceStopped)
}

func (g *controlGate) RestartRemote() {
	g.arbiter.RestartRemote()
}

func (g *controlGate) CloseForShutdown() *ShutdownCleanupPermit {
	return g.arbiter.CloseForShutdown()
}
