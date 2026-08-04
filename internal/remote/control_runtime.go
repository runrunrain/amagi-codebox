package remote

// control_runtime.go — ControlRuntime: composes the M3-A1 arbiter/gate/directory/
// hub/projector into a single runtime that the App wires to its raw ports.
//
// It is the bridge between the A1 state-machine core (control_*.go) and the A2
// raw-port migration (Bind收口, PTY producer, desktop write gating, shutdown).
//
// Design authority: fuxi/20260803-m3-a-control-arbitration-design/design.md
// §4.1 (module boundaries), §6.1 (ControlGate surface), §6.3 (write-path
// migration), §6.4 (launch transaction), §8.6 (run-scoped projection),
// §10.3 (shutdown). The runtime holds the stable DesktopAuthority (§5.1) and
// the legacy loopback WS authority (Leader ruling: shares desktop holder
// identity with legacy:true).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Raw port injection interfaces (design §4.1: raw ports sit BEHIND the gate)
// ---------------------------------------------------------------------------

// PTYRawPort is the raw PTY mutation surface injected by the App. It is NEVER
// Wails-bound (design §6.3 C-01). Raw bytes (not base64) so the gate closure
// can checkpoint per chunk before each irreversible syscall. The context
// carries the operation deadline (M-009): the raw port SHOULD observe it so a
// gated/timeout-cancelled effect can bail before its syscall; the underlying
// syscall itself cannot be interrupted, so the gate additionally bounds the
// effect and quarantines the backend on timeout.
//
// R3-004: DetachSession is the forceful backend detach invoked when a bounded
// raw effect times out (mid-syscall). On Windows it closes the ConPTY handle so
// a stuck Write/Resize syscall is released by the OS (the handle close cancels
// outstanding overlapped I/O); on Darwin it kills the process group. This is
// best-effort — the goal is to unblock the stuck goroutine + release the OS
// resource so a trusted desktop recovery lifecycle (Stop/Restart) can clean up.
// BackendDetachReceipt is typed evidence for one exact detached backend. The
// PTY implementation's reaper owns the captured backend pointer; Wait never
// re-resolves by SessionID, so a late retry cannot close a replacement run.
type BackendDetachReceipt interface {
	Identity() uint64
	Confirmed() bool
	LastError() error
	Wait(ctx context.Context) error
}

type PTYRawPort interface {
	WriteRaw(ctx context.Context, sessionID string, data []byte) error
	ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error
	// DetachSession moves the exact active backend out of the session namespace
	// and returns a receipt. A first close failure is returned (never swallowed);
	// the receipt may later confirm after its exact-backend reaper succeeds.
	DetachSession(sessionID string) (BackendDetachReceipt, error)
}

// ErrControlNotReady is returned when the control runtime gate is nil/unready.
var ErrControlNotReady = errors.New("control: runtime not ready")

// errRestartExitedBeforeActivate is the honest transaction outcome when the
// newly spawned process delivers its sole exit while the restart is still
// hidden. The caller compensates/aborts instead of publishing a running run.
var errRestartExitedBeforeActivate = errors.New("control: restarted process exited before activation")

// ---------------------------------------------------------------------------
// DesktopAuthority minting (design §5.1, §6.1)
// ---------------------------------------------------------------------------

// desktopAuthoritySource is a process-stable opaque fingerprint. The exact
// value is irrelevant; it only needs to be stable for the process lifetime and
// distinct between the Wails root and the legacy loopback root so that exact-
// source disconnect fencing works (design §4.5).
var (
	desktopAuthoritySourceWails  uint64 = 0x5741494c53 // "WAILS"
	desktopAuthoritySourceLegacy uint64 = 0x4c45474143 // "LEGAC"
)

// MintDesktopAuthority creates the stable process-level Wails desktop
// authority (design §5.1). Internal-only; the App obtains it from the runtime.
func MintDesktopAuthority() *DesktopAuthority {
	return &DesktopAuthority{source: desktopAuthoritySourceWails, legacy: false}
}

// MintLegacyWSAuthority creates the legacy loopback WS authority (Leader
// ruling): a loopback peer with a valid legacy token shares desktop holder
// identity (same desktop semantics) but carries legacy=true for exact-source
// disconnect fencing.
func MintLegacyWSAuthority() *DesktopAuthority {
	return &DesktopAuthority{source: desktopAuthoritySourceLegacy, legacy: true}
}

// ---------------------------------------------------------------------------
// ControlRuntime
// ---------------------------------------------------------------------------

// ControlRuntime composes the control arbitration components and exposes the
// desktop-gated facade methods consumed by the App. It owns the stable
// DesktopAuthority, the legacy WS authority, the RunEventProjector, and the
// injected PTYRawPort.
type ControlRuntime struct {
	arbiter   *ControlArbiter
	gate      ControlGate
	directory *AttachmentDirectory
	hub       *SessionEventHub

	// H1 run-segment committer + live run continuity feed (design §4A.2). The
	// committer uses the hub as its causal reservation port (three-lock domain:
	// stateMu → feed.mu → causal-ledger.mu). The feed is the sole ordered record
	// ring consumed by the M2 SessionStreamStore pump (design §7.1).
	committer RunSegmentCommitter
	feed      LiveRunContinuityFeed

	projector *RunEventProjector

	desktopAuthority *DesktopAuthority
	legacyAuthority  *DesktopAuthority

	ptyRaw PTYRawPort

	// ptyLifecycle is the raw close/remove port for desktop lifecycle (M-005).
	ptyLifecycle PTYLifecycleRawPort

	mu     sync.Mutex
	ready  bool
	closed bool
}

// NewControlRuntime constructs a runtime with a fresh arbiter/gate/directory/
// hub/projector. MarkReady must be called (after Clock/hub wiring) before the
// gate accepts operations. ptyRaw may be nil during construction and set via
// SetPTYRawPort before MarkReady.
func NewControlRuntime(clock Clock, log *logging.Service) *ControlRuntime {
	hub := NewSessionEventHub()
	directory := NewAttachmentDirectory()
	arbiter := NewControlArbiter(clock, hub, directory)
	gate := NewControlGate(arbiter, hub, directory)
	projector := NewRunEventProjector(arbiter, log)
	// Wire the hub's defense-in-depth validation-failure latch to the arbiter
	// health latch (design §8.1: a producer contract never-event latches the gate).
	hub.SetOnValidationError(func() { arbiter.healthLatched.Store(true) })
	// H1 concrete causal port wiring: the hub implements
	// SessionCausalReservationPort + SessionCausalPublicationPort (design §4A.4).
	// The committer reserves event ordinals at commit time; the pump publishes
	// payloads via PublishReserved (design §4A.5: production wiring composes both).
	committer := NewRunSegmentCommitter(hub, newCountingOutcomeRecorder())
	feed := NewLiveRunContinuityFeed(committer)
	projector.SetH1Wiring(committer, feed, hub) // M-003: unique producer welding
	rt := &ControlRuntime{
		arbiter:          arbiter,
		gate:             gate,
		directory:        directory,
		hub:              hub,
		committer:        committer,
		feed:             feed,
		projector:        projector,
		desktopAuthority: MintDesktopAuthority(),
		legacyAuthority:  MintLegacyWSAuthority(),
	}
	return rt
}

// SetPTYRawPort injects the raw PTY mutation port (behind the gate). R3-004: it
// ALSO wires the gate's backend detacher so a mid-syscall timeout force-detaches
// the stuck backend (the gate calls ptyRaw.DetachSession from the quarantine
// path, outside stateMu).
func (r *ControlRuntime) SetPTYRawPort(p PTYRawPort) {
	r.ptyRaw = p
	if g, ok := r.gate.(*controlGate); ok && p != nil {
		g.SetBackendDetacher(func(sessionID contract.SessionID) (BackendDetachReceipt, error) {
			return p.DetachSession(string(sessionID))
		})
	}
}

// PTYLifecycleRawPort is the raw close/remove surface for desktop lifecycle
// operations (M-005). It sits behind the gate like PTYRawPort.
type PTYLifecycleRawPort interface {
	CloseSession(sessionID contract.SessionID) error
	RemoveSession(sessionID contract.SessionID) error
}

// SetPTYLifecycleRawPort injects the raw lifecycle port (behind the gate).
func (r *ControlRuntime) SetPTYLifecycleRawPort(p PTYLifecycleRawPort) {
	r.ptyLifecycle = p
}

// SetWailsContext injects the Wails app context for run-scoped event emission.
func (r *ControlRuntime) SetWailsContext(ctx context.Context) { r.projector.SetContext(ctx) }

// Projector returns the run-scoped PTY projector (implements pty.RunEventSink).
func (r *ControlRuntime) Projector() *RunEventProjector { return r.projector }

// Gate returns the control gate (for advanced consumers).
func (r *ControlRuntime) Gate() ControlGate { return r.gate }

// Arbiter returns the arbiter (for shutdown/revoke integration).
func (r *ControlRuntime) Arbiter() *ControlArbiter { return r.arbiter }

// Directory returns the attachment directory.
func (r *ControlRuntime) Directory() *AttachmentDirectory { return r.directory }

// Hub returns the session event hub.
func (r *ControlRuntime) Hub() *SessionEventHub { return r.hub }

// Committer returns the H1 run-segment committer (design §4A.2). The M2 pump
// and launch/restart paths consume it to commit observations with causal
// reservations.
func (r *ControlRuntime) Committer() RunSegmentCommitter { return r.committer }

// Feed returns the H1 live run continuity feed (design §4A.2). The M2 attach
// catch-up reads snapshots from it (design §6.3 step 2 SyncFeed).
func (r *ControlRuntime) Feed() LiveRunContinuityFeed { return r.feed }

// DesktopAuthority returns the stable process-level Wails desktop authority.
func (r *ControlRuntime) DesktopAuthority() *DesktopAuthority { return r.desktopAuthority }

// LegacyWSAuthority returns the legacy loopback WS desktop authority.
func (r *ControlRuntime) LegacyWSAuthority() *DesktopAuthority { return r.legacyAuthority }

// MarkReady enables the runtime for production use.
func (r *ControlRuntime) MarkReady() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.arbiter.MarkReady()
	r.hub.MarkReady()
	r.directory.MarkReady()
	r.projector.MarkReady()
	r.ready = true
}

// IsReady reports whether the runtime is ready.
func (r *ControlRuntime) IsReady() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready && !r.closed
}

// CloseForShutdown delegates to the arbiter's infallible one-shot shutdown
// fence (design §10.3). After this, no new operations/launches are admitted.
func (r *ControlRuntime) CloseForShutdown() *ShutdownCleanupPermit {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return r.arbiter.CloseForShutdown()
}

// FenceAllRemote / ReleaseAllRemote / RestartRemote delegate to the gate
// (design §7.4 Server Stop / security latch).
func (r *ControlRuntime) FenceAllRemote()   { r.gate.FenceAllRemote() }
func (r *ControlRuntime) ReleaseAllRemote() { r.gate.ReleaseAllRemote() }
func (r *ControlRuntime) RestartRemote()    { r.gate.RestartRemote() }

// ---------------------------------------------------------------------------
// Desktop run registration (design §6.4 launch transaction, A2 minimal wiring)
// ---------------------------------------------------------------------------

// BeginDesktopRun starts a desktop launch transaction: mints a LaunchPermit and
// registers a staging (not-yet-public) control entry + run identity. Returns
// the LaunchPermit (for Activate/Abort), RunPermit (for ActivateRun), and
// RunObservationPermit (to inject into the raw PTY). The App MUST call
// ActivateDesktopRun after the raw PTY starts, or AbortDesktopRun on failure.
func (r *ControlRuntime) BeginDesktopRun(
	ctx context.Context,
	sessionID contract.SessionID,
) (*LaunchPermit, *RunPermit, *RunObservationPermit, error) {
	if r.gate == nil {
		return nil, nil, nil, ErrControlNotReady
	}
	permit, err := r.gate.BeginDesktopLaunch(ctx, r.desktopAuthority)
	if err != nil {
		return nil, nil, nil, err
	}
	runPermit, obsPermit, err := r.gate.RegisterStartingSession(ctx, permit, sessionID)
	if err != nil {
		r.gate.AbortLaunch(ctx, permit, err)
		return nil, nil, nil, err
	}
	return permit, runPermit, obsPermit, nil
}

// ActivateDesktopRun transitions a staging run to public/active (design §6.4.1
// step 7). Must be called after the raw PTY/process has started successfully.
func (r *ControlRuntime) ActivateDesktopRun(ctx context.Context, runPermit *RunPermit) error {
	return r.gate.ActivateRun(ctx, runPermit)
}

// AbortDesktopRun cancels a launch and performs reverse-order compensation.
func (r *ControlRuntime) AbortDesktopRun(ctx context.Context, permit *LaunchPermit, cause error) {
	r.gate.AbortLaunch(ctx, permit, cause)
}

// RemoveDesktopSession removes a session's control entry (tombstone + fence).
// Called by the App after the raw PTY is closed / session record removed.
func (r *ControlRuntime) RemoveDesktopSession(ctx context.Context, sessionID contract.SessionID) {
	// State-only removal: tombstone + fence. The arbiter's removeSession is
	// idempotent for already-removed sessions.
	_ = r.arbiter.removeSession(sessionID)
	r.arbiter.physicallyDeleteEntry(sessionID)
	r.projector.ForgetRun(sessionID)
}

// ---------------------------------------------------------------------------
// Restart run transition (M-004, design §4.5)
//
// SealRestartSegmentForPermit + CommitRestartRun implement the same-ID restart
// transaction: H1 seal the old segment (fencing late old-run observations) →
// (caller stops the old process + re-resolves off-lock) → atomic swap to a fresh
// run identity + new segment + boundary-first record. The new process is started
// by the caller with the exact new RunObservationPermit, so its output/exit flow
// through the H1 committer (no nil permit, no AppendBoundary). The atomic swap
// happens under the unified three-lock domain (stateMu→feed.mu→ledger.mu) with
// an N-002 exact-match fence, so a fenced/stale restart cannot swap the run.
// ---------------------------------------------------------------------------

// SealRestartSegmentForPermit seals the current (old) segment for the run bound
// to the lifecycle permit (design §4.5 step 2). The seal marks the old feed
// segment sealed and suppresses not-yet-released old runState reservations in
// the H3 causal ledger; late old-run observations then return
// ObservationDroppedSegmentSealed. Must be called BEFORE stopping the old run.
func (r *ControlRuntime) SealRestartSegmentForPermit(permit *operationPermit, sessionID contract.SessionID) (*RunSegmentSealReceipt, error) {
	if permit == nil || permit.entry == nil || permit.run == nil || permit.entry.sessionID != sessionID {
		return nil, errFeedStaleRun
	}
	entry := permit.entry
	// Seal participates in the same stateMu→feed.mu→ledger.mu domain as
	// observation commit, so a fence cannot slip between permit validation and
	// the old-segment boundary.
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if !restartPermitMatchesLocked(entry, permit) || permit.restartStage != nil {
		return nil, errOperationFenced
	}
	return r.committer.SealRestartSegment(permit.restartIntent, permit.run, permit.runEpoch, sessionID)
}

// restartPermitMatchesLocked validates the old public run bound to a restart
// lifecycle permit. Caller holds entry.stateMu.
func restartPermitMatchesLocked(entry *controlEntry, permit *operationPermit) bool {
	return entry != nil && permit != nil && entry.currentOp == permit &&
		entry.operationSeq == permit.opSeq && entry.controlEpoch == permit.controlEpoch &&
		entry.currentRun == permit.run && entry.runEpoch == permit.runEpoch &&
		entry.backendEpoch == permit.backendEpoch && !permit.fenced.Load()
}

// CommitRestartRun is the atomic stage+activate convenience used only where no
// StartProcess window exists. Production restart uses the explicit three phases.
func (r *ControlRuntime) CommitRestartRun(permit *operationPermit, sealReceipt *RunSegmentSealReceipt, sessionID contract.SessionID) (*RunObservationPermit, *LiveBoundaryReceipt, error) {
	obsPermit, err := r.StageRestartRun(permit, sealReceipt, sessionID)
	if err != nil {
		return nil, nil, err
	}
	boundary, err := r.ActivateRestartRun(permit, sealReceipt, sessionID)
	if err != nil {
		_ = r.AbortRestartStage(permit, sealReceipt, sessionID)
		return nil, nil, err
	}
	return obsPermit, boundary, nil
}

// StageRestartRun reserves a never-reused epoch and mints the new run pointer,
// but does NOT publish either through entry.currentRun/runEpoch. The old public
// pointer remains exact-matchable while entry.runPhase=runStarting hides the
// transition. StartProcess receives the staged observation identity; output and
// exit offered before activation enter this exact transaction's bounded FIFO.
func (r *ControlRuntime) StageRestartRun(permit *operationPermit, sealReceipt *RunSegmentSealReceipt, sessionID contract.SessionID) (*RunObservationPermit, error) {
	if permit == nil || permit.entry == nil || permit.run == nil || sealReceipt == nil || permit.entry.sessionID != sessionID {
		return nil, errFeedNotSealed
	}
	entry := permit.entry
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if !restartPermitMatchesLocked(entry, permit) || permit.restartStage != nil ||
		!sealReceipt.Sealed || sealReceipt.Intent != permit.restartIntent ||
		sealReceipt.OldRun != permit.run || sealReceipt.OldEpoch != permit.runEpoch {
		return nil, errOperationFenced
	}
	newRun, newEpoch, ok := r.arbiter.reserveRunIdentityLocked(entry)
	if !ok {
		return nil, &ControlGateError{Kind: DenyControlUnavailable}
	}
	observations, err := r.feed.BeginRestart(permit.restartIntent, permit.run, sessionID)
	if err != nil {
		return nil, err
	}
	observations.newRun = newRun
	observations.newRunEpoch = newEpoch
	permit.restartStage = &restartRunStage{
		oldRun:       permit.run,
		oldRunEpoch:  permit.runEpoch,
		newRun:       newRun,
		newRunEpoch:  newEpoch,
		seal:         sealReceipt,
		observations: observations,
	}
	entry.runPhase = runStarting
	return &RunObservationPermit{entry: entry, run: newRun, runEpoch: newEpoch, backendEpoch: entry.backendEpoch}, nil
}

// ActivateRestartRun is the only publication point. After StartProcess
// succeeds it revalidates the old permit + hidden stage, commits the boundary
// under stateMu→feed.mu→ledger.mu, then atomically swaps currentRun/runEpoch,
// sets running/healthy, and refreshes the permit for N-002 completion.
func (r *ControlRuntime) ActivateRestartRun(permit *operationPermit, sealReceipt *RunSegmentSealReceipt, sessionID contract.SessionID) (*LiveBoundaryReceipt, error) {
	if permit == nil || permit.entry == nil || sealReceipt == nil || permit.entry.sessionID != sessionID {
		return nil, errFeedNotSealed
	}
	entry := permit.entry
	entry.stateMu.Lock()
	if !restartPermitMatchesLocked(entry, permit) {
		entry.stateMu.Unlock()
		return nil, errOperationFenced
	}
	stage := permit.restartStage
	if stage == nil || stage.seal != sealReceipt || stage.oldRun != permit.run ||
		stage.oldRunEpoch != permit.runEpoch || stage.newRun == nil || stage.newRunEpoch == 0 ||
		stage.observations == nil {
		entry.stateMu.Unlock()
		return nil, errOperationFenced
	}
	if stage.observations.faulted {
		entry.stateMu.Unlock()
		return nil, errFeedFault
	}
	if stage.observations.terminal {
		// The process already delivered its one terminal observation. Publishing
		// running now would permanently hide that exit, so fail the restart and let
		// the caller execute the normal compensation + exact seal rollback.
		entry.stateMu.Unlock()
		return nil, errRestartExitedBeforeActivate
	}
	stagedRecords := append([]LiveRunRecord(nil), stage.observations.records...)
	// R4-004: a quarantined predecessor can be replaced only when the receipt
	// still confirms detach for this exact backendEpoch. Confirmation is checked
	// again at activate, not merely at lifecycle admission/stage.
	if entry.backend == backendQuarantined && !exactBackendDetachConfirmedLocked(entry) {
		detach := entry.backendDetach.disposition
		entry.stateMu.Unlock()
		return nil, &ControlGateError{Kind: DenyControlUnavailable, Detach: detach}
	}
	var newObsPermit *RunObservationPermit
	boundary, err := r.committer.CommitRestartSegment(permit.restartIntent, sealReceipt, stage.observations, stage.newRun, stage.newRunEpoch, sessionID, func() {
		// O(1) state publication while stateMu + feed.mu are both held: no feed
		// snapshot can observe the boundary without the matching current pointer.
		entry.currentRun = stage.newRun
		entry.runEpoch = stage.newRunEpoch
		entry.runPhase = runActive
		entry.stateMirror = contract.SessionStateRunning
		entry.stateMirrorSet = true
		entry.backend = backendHealthy
		entry.backendDetach = backendDetachRecord{}
		permit.run = stage.newRun
		permit.runEpoch = stage.newRunEpoch
		permit.restartStage = nil
		newObsPermit = &RunObservationPermit{entry: entry, run: entry.currentRun, runEpoch: entry.runEpoch, backendEpoch: entry.backendEpoch}
	})
	if err != nil {
		entry.stateMu.Unlock()
		return nil, err
	}
	// Publish the desktop run tag before releasing stateMu. A new-process output
	// blocks on stateMu in the committer, so it cannot emit with the old Wails run
	// token between pointer activation and projector tracking. This is O(1) map
	// state only; stream pumping remains outside all transaction locks.
	r.projector.TrackRestartRunStaged(sessionID, newObsPermit, stagedRecords)
	entry.stateMu.Unlock()

	// Remote replay observes boundary → staged FIFO from the H1 feed. Wails
	// projection is then flushed behind a per-run barrier, so post-activate PTY
	// callbacks cannot overtake the staged prefix.
	r.projector.PumpPending(sessionID)
	r.projector.FlushRestartStage(sessionID, stagedRecords)
	return boundary, nil
}

// RollbackRestartSealForPermit rolls back a seal when failure occurs before a
// stage exists (stop/resolve/stage failure). It is exact-run and safe for a late
// timeout goroutine: a newer activated run makes it a no-op.
func (r *ControlRuntime) RollbackRestartSealForPermit(permit *operationPermit, sealReceipt *RunSegmentSealReceipt, sessionID contract.SessionID) bool {
	if permit == nil || permit.entry == nil || sealReceipt == nil || permit.entry.sessionID != sessionID {
		return false
	}
	entry := permit.entry
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	if entry.removed || entry.currentRun != permit.run || entry.runEpoch != permit.runEpoch || permit.restartStage != nil {
		return false
	}
	return r.committer.RollbackRestartSegment(sealReceipt, sessionID)
}

// AbortRestartStage discards a hidden stage and rolls back only its exact
// feed/ledger seal. The old pointer was never replaced, so abort only marks the
// old public generation terminal/unavailable. A superseding activation changes
// currentRun/runEpoch and makes this late abort a no-op.
func (r *ControlRuntime) AbortRestartStage(permit *operationPermit, sealReceipt *RunSegmentSealReceipt, sessionID contract.SessionID) error {
	if permit == nil || permit.entry == nil || sealReceipt == nil || permit.entry.sessionID != sessionID {
		return errOperationFenced
	}
	entry := permit.entry
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	stage := permit.restartStage
	if entry.removed || stage == nil || stage.seal != sealReceipt ||
		entry.currentRun != stage.oldRun || entry.runEpoch != stage.oldRunEpoch {
		return errOperationFenced
	}
	// Close the FIFO first so a late PTY callback cannot append while rollback is
	// being decided. entry.stateMu serializes this with observation admission.
	r.feed.AbortRestart(stage.observations)
	if !r.committer.RollbackRestartSegment(sealReceipt, sessionID) {
		return errFeedFault
	}
	permit.restartStage = nil
	entry.runPhase = runTerminal
	entry.stateMirror = contract.SessionStateUnavailable
	entry.stateMirrorSet = true
	return nil
}

// ---------------------------------------------------------------------------
// Restart failure reconciliation (R3-001, design §4.5)
// ---------------------------------------------------------------------------

// ReconcileRestartFailure transitions a session to an honest terminal/
// unavailable state after a restart raw effect failed at an irreversible step.
// The session MUST NOT be presented as running after a restart failure; the
// recovery path is explicit (Stop then Restart). R4-001: the reconcile is bound
// to the EXACT runEpoch of the failed attempt (failedRunEpoch) — a stale/late
// reconcile whose generation is no longer current is a no-op, so it can never
// clobber a newer successful run. See ControlArbiter.ReconcileRestartFailure
// for the authoritative semantics.
func (r *ControlRuntime) ReconcileRestartFailure(sessionID contract.SessionID, failedRunEpoch uint64) {
	if r.arbiter == nil {
		return
	}
	r.arbiter.ReconcileRestartFailure(sessionID, failedRunEpoch)
}

// ---------------------------------------------------------------------------
// Desktop-gated PTY facade (design §6.2, §6.3)
// ---------------------------------------------------------------------------

// DesktopInput writes raw input to the PTY through the desktop control gate
// (intentional input = desktop take). Each write is one operation with one
// checkpoint (design §6.3).
func (r *ControlRuntime) DesktopInput(ctx context.Context, sessionID contract.SessionID, data []byte) error {
	if r.gate == nil || r.ptyRaw == nil {
		return ErrControlNotReady
	}
	return r.gate.DoDesktopPTY(ctx, r.desktopAuthority, sessionID, PTYInput, func(ctx context.Context, permit *operationPermit) error {
		if err := permit.Checkpoint(ctx, 1); err != nil {
			return err
		}
		return r.ptyRaw.WriteRaw(ctx, string(sessionID), data)
	})
}

// DesktopPasteChunk writes a paste as independent per-chunk operations (design
// §6.1, §9.4). A take/revoke fence between chunks stops remaining chunks with
// zero replay.
func (r *ControlRuntime) DesktopPasteChunk(ctx context.Context, sessionID contract.SessionID, data []byte) error {
	if r.gate == nil || r.ptyRaw == nil {
		return ErrControlNotReady
	}
	const chunkSize = 1024
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]
		step := uint32(offset/chunkSize + 1)
		if err := r.gate.DoDesktopPTY(ctx, r.desktopAuthority, sessionID, PTYPasteChunk, func(ctx context.Context, permit *operationPermit) error {
			if err := permit.Checkpoint(ctx, step); err != nil {
				return err
			}
			return r.ptyRaw.WriteRaw(ctx, string(sessionID), chunk)
		}); err != nil {
			return err
		}
	}
	return nil
}

// DesktopPassiveResize resizes the PTY without taking desktop control (design
// §6.2 R-06: allowed only when holder is none/desktop; a device holder blocks).
func (r *ControlRuntime) DesktopPassiveResize(ctx context.Context, sessionID contract.SessionID, cols, rows int) error {
	if r.gate == nil || r.ptyRaw == nil {
		return ErrControlNotReady
	}
	return r.gate.DoDesktopPassiveResize(ctx, r.desktopAuthority, sessionID, func(ctx context.Context, permit *operationPermit) error {
		if err := permit.Checkpoint(ctx, 1); err != nil {
			return err
		}
		return r.ptyRaw.ResizeRaw(ctx, string(sessionID), cols, rows)
	})
}

// DesktopStop closes the PTY for a control-managed embedded session through the
// desktop control gate (M-005). The irreversible Close is gated by a permit
// Checkpoint so a revoke/Stop/timeout fence aborts before the syscall. Returns
// ErrControlNotReady if the lifecycle port is not wired.
func (r *ControlRuntime) DesktopStop(ctx context.Context, sessionID contract.SessionID) error {
	if r.gate == nil || r.ptyLifecycle == nil {
		return ErrControlNotReady
	}
	_, err := r.gate.DoDesktopLifecycle(ctx, r.desktopAuthority, sessionID, LifecycleStop, func(ctx context.Context, permit *operationPermit) (SessionMutationResult, error) {
		if err := permit.Checkpoint(ctx, 1); err != nil {
			return SessionMutationResult{}, err
		}
		if err := r.ptyLifecycle.CloseSession(sessionID); err != nil {
			return SessionMutationResult{}, err
		}
		return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
	})
	return err
}

// DesktopRemove closes the PTY and removes the control entry through the desktop
// control gate (M-005). The irreversible Close/Remove are gated by Checkpoint.
func (r *ControlRuntime) DesktopRemove(ctx context.Context, sessionID contract.SessionID) error {
	if r.gate == nil || r.ptyLifecycle == nil {
		return ErrControlNotReady
	}
	_, err := r.gate.DoDesktopLifecycle(ctx, r.desktopAuthority, sessionID, LifecycleRemove, func(ctx context.Context, permit *operationPermit) (SessionMutationResult, error) {
		if err := permit.Checkpoint(ctx, 1); err != nil {
			return SessionMutationResult{}, err
		}
		closeErr := r.ptyLifecycle.CloseSession(sessionID)
		if removeErr := r.ptyLifecycle.RemoveSession(sessionID); removeErr != nil && closeErr == nil {
			return SessionMutationResult{}, removeErr
		}
		if closeErr != nil {
			return SessionMutationResult{}, closeErr
		}
		return SessionMutationResult{Removed: true}, nil
	})
	if err == nil {
		// Best-effort control-entry cleanup after a successful gated remove.
		r.RemoveDesktopSession(ctx, sessionID)
	}
	return err
}

// DesktopClearStatus is the per-session outcome of an authoritative batch
// clear of stopped-session control entries (R4-005).
type DesktopClearStatus uint8

const (
	// DesktopClearCleared: the control tombstone was authoritatively removed
	// through the desktop authority (the caller may delete the matching manager
	// record).
	DesktopClearCleared DesktopClearStatus = iota + 1
	// DesktopClearSkipped: the session was not cleared (race: no longer stopped /
	// not found / legacy / lifecycle in progress / gate not ready). The manager
	// record MUST be retained.
	DesktopClearSkipped
	// DesktopClearErrored: an unexpected gate/lifecycle error occurred. The
	// manager record MUST be retained and the error surfaced.
	DesktopClearErrored
)

// DesktopClearIDResult is the per-session result of DesktopClearStopped.
type DesktopClearIDResult struct {
	ID     contract.SessionID
	Status DesktopClearStatus
	Reason string // non-empty for Skipped/Errored (short diagnostic)
}

// DesktopClearStoppedResult is the typed result of an authoritative batch clear
// (R4-005). Results is sorted by SessionID (canonical order).
type DesktopClearStoppedResult struct {
	Results []DesktopClearIDResult
}

// ClearedIDs returns the IDs whose control tombstones were authoritatively
// removed, in sorted order. The caller deletes manager records for exactly
// these IDs (plus legacy records that have no control entry).
func (r DesktopClearStoppedResult) ClearedIDs() []contract.SessionID {
	var cleared []contract.SessionID
	for _, res := range r.Results {
		if res.Status == DesktopClearCleared {
			cleared = append(cleared, res.ID)
		}
	}
	return cleared
}

// DesktopClearStopped clears stopped-session control entries through the desktop
// authority, ONE id at a time via ClearStoppedDesktopSession (R4-005). This is a
// per-session authoritative lifecycle transaction, not a state-only bulk delete:
//
//   - Each id is routed through the Gate (lifecycle intent + drain permit +
//     authoritative N-002 completion), so a concurrent fence/restart makes the
//     completion non-authoritative (the id is skipped, not force-removed).
//   - A session that is no longer stopped (it restarted after the caller's
//     snapshot) is skipped (race guard) and retained.
//   - Each id yields a typed cleared/skipped/errored result.
//   - Results are returned in sorted SessionID order (canonical processing;
//     never holds two stateMu simultaneously).
//
// The caller (App) attempts manager deletion only for ClearedIDs(). A later
// Manager.Remove failure is a possible cross-store race; App must retain/report
// that ID and must not count it as a successful clear.
func (r *ControlRuntime) DesktopClearStopped(ctx context.Context, stoppedIDs []contract.SessionID) DesktopClearStoppedResult {
	result := DesktopClearStoppedResult{}
	if r.gate == nil {
		for _, id := range stoppedIDs {
			result.Results = append(result.Results, DesktopClearIDResult{ID: id, Status: DesktopClearSkipped, Reason: "control runtime not wired"})
		}
		return result
	}
	// Canonical (sorted) order — never hold two stateMu simultaneously.
	sorted := append([]contract.SessionID(nil), stoppedIDs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, id := range sorted {
		cleared, err := r.gate.ClearStoppedDesktopSession(ctx, r.desktopAuthority, id)
		if err == nil && cleared {
			result.Results = append(result.Results, DesktopClearIDResult{ID: id, Status: DesktopClearCleared})
			continue
		}
		if err == nil {
			// cleared==false without error: not removable right now (treat as skipped).
			result.Results = append(result.Results, DesktopClearIDResult{ID: id, Status: DesktopClearSkipped, Reason: "not removable"})
			continue
		}
		kind := classifyClearStoppedError(err)
		result.Results = append(result.Results, DesktopClearIDResult{ID: id, Status: kind.Status, Reason: kind.Reason})
	}
	return result
}

// clearClassification carries the per-id outcome for a gate error.
type clearClassification struct {
	Status DesktopClearStatus
	Reason string
}

// classifyClearStoppedError maps a ClearStoppedDesktopSession error to a typed
// per-id outcome. Recoverable/availability/race denials are Skipped (retain);
// unexpected errors are Errored (retain + surface).
func classifyClearStoppedError(err error) clearClassification {
	var ge *ControlGateError
	if errors.As(err, &ge) {
		switch ge.Kind {
		case DenySessionNotFound:
			return clearClassification{DesktopClearSkipped, "no control entry (legacy/already cleared)"}
		case DenySessionNotStopped:
			return clearClassification{DesktopClearSkipped, "no longer stopped (restart race)"}
		case DenyStalePermit:
			return clearClassification{DesktopClearSkipped, "stale completion (concurrent fence/restart)"}
		case DenyLifecycleInProgress:
			return clearClassification{DesktopClearSkipped, "lifecycle in progress"}
		case DenyNotController:
			return clearClassification{DesktopClearSkipped, "not authorized for this holder/source"}
		case DenyControlUnavailable, DenyNotAccepting, DenyShutdown:
			return clearClassification{DesktopClearSkipped, "control unavailable"}
		case DenyOperationTimeout:
			return clearClassification{DesktopClearErrored, "lifecycle timed out"}
		}
	}
	return clearClassification{DesktopClearErrored, "unexpected error: "}
}

// DesktopBootstrap writes a delayed auto-command to the PTY through the gate's
// DoBootstrapPTY (M-005: the delayed bootstrap write is gated, not a raw
// cpty.Write goroutine). The run permit must still be valid (DoBootstrapPTY
// revalidates it); a revoke/stop during the bootstrap delay denies the write
// safely instead of writing into a dead/fenced session.
func (r *ControlRuntime) DesktopBootstrap(ctx context.Context, p *RunPermit, sessionID contract.SessionID, data []byte) error {
	if r.gate == nil || r.ptyRaw == nil {
		return ErrControlNotReady
	}
	return r.gate.DoBootstrapPTY(ctx, p, func(ctx context.Context, permit *operationPermit) error {
		return r.ptyRaw.WriteRaw(ctx, string(sessionID), data)
	})
}

// ---------------------------------------------------------------------------
// v1 attach / detach lifecycle (design §7.1, §7.2)
//
// These methods wire the AttachmentDirectory + SessionEventHub + ControlArbiter
// for a v1 WS device attaching to / detaching from a session. The real M2
// /ws/v1 session adapter calls these after M1 registry RegisterV1Connection;
// the consumer seam (ControlEventConsumer) is implemented by the adapter.
// Spy connections provide evidence in tests.
//
// IMPORTANT: these are the producer-side wiring + attach lifecycle. The full
// M2 session WS frame path (attach frame decode, session.attached FiveLayer
// assembly, input/resize dispatch) is NOT claimed here — those routes stay 404
// until M2 (design §6.5).
// ---------------------------------------------------------------------------

// ControlAttachmentHandle is the opaque handle returned by AttachControl. The
// caller (M2 WS adapter / test) holds it and passes it to DetachControl. It
// carries the sessionID, lease, and hub subscriber for cleanup.
type ControlAttachmentHandle struct {
	sessionID  contract.SessionID
	deviceID   contract.DeviceID
	lease      *ControlConnectionLease
	subscriber *hubSubscriber
}

// Lease returns the authoritative connection lease (for gate.Acquire).
func (h *ControlAttachmentHandle) Lease() *ControlConnectionLease { return h.lease }

// StartControlDelivery launches the control-transition writer goroutine for
// this attachment (M-007). It MUST be called by the WS session adapter ONLY
// after the `session.attached` frame is on the wire, so that attached is always
// the first event the client observes and no control transition enqueued during
// the attach window can preempt it on the socket. Idempotent.
//
// R4-003: production /ws/v1 does NOT use this per-subscriber writer. The
// control hub admits validated transitions directly into the connection's
// unique final queue, while deliveryLoop merges H3 causal ingress (and retains
// a FIFO fallback for non-direct consumers). This method remains for hub-level
// spy tests and legacy consumers.
func (h *ControlAttachmentHandle) StartControlDelivery() {
	if h == nil || h.subscriber == nil {
		return
	}
	h.subscriber.StartWriter()
}

// SetControlDeliveryFencer wires the control-delivery authority fencer. The
// production final-queue admission failure is reflected back through the hub
// subscriber, which fences the exact lease and invokes this teardown effect.
// Late installation also catches an overflow from Subscribe→return (R4-003).
func (h *ControlAttachmentHandle) SetControlDeliveryFencer(f SubscriptionAuthorityFencer) {
	if h == nil || h.subscriber == nil {
		return
	}
	h.subscriber.SetAuthorityFencer(f)
}

// DrainPendingControl drains the legacy/fallback subscriber FIFO. Production
// direct-queue control admission leaves this empty; deliveryLoop still drains it
// deterministically for compatibility. Returns nil without an attachment.
func (h *ControlAttachmentHandle) DrainPendingControl() []contract.KnownServerEvent {
	if h == nil || h.subscriber == nil {
		return nil
	}
	return h.subscriber.Drain()
}

// ControlNotifyCh returns the fallback FIFO notify channel. Production direct
// control admission wakes the final queue instead. Returns nil without an
// attachment.
func (h *ControlAttachmentHandle) ControlNotifyCh() <-chan struct{} {
	if h == nil || h.subscriber == nil {
		return nil
	}
	return h.subscriber.notify
}

// AttachControl mints an authoritative lease for (deviceID, sessionID), binds a
// hub subscriber (delivering events to consumer), and returns the audience-
// relative control snapshot for the initial session.attached (design §7.1).
//
// The attach is atomic relative to control transitions (design §7.1 step 5,
// T-23): snapshot + subscription happen under the same stateMu critical section
// as state commits, so no transition is missed between them.
//
// If a previous lease existed for the same (deviceID, sessionID), it is fenced
// (atomically replaced) and returned as fencedOld for the caller to downgrade.
// A device in grace for this session is rebound (no wire event).
//
// consumer may be nil (sync-drain mode for tests). When non-nil, a writer
// goroutine delivers events to it.
func (r *ControlRuntime) AttachControl(
	principal DevicePrincipal,
	connectionID ConnectionID,
	sessionID contract.SessionID,
	consumer ControlEventConsumer,
) (handle *ControlAttachmentHandle, snap contract.ControlSnapshot, fencedOld *ControlConnectionLease, gErr *ControlGateError) {
	if r.gate == nil {
		return nil, contract.ControlSnapshot{}, nil, &ControlGateError{Kind: DenyControlUnavailable}
	}
	// Step 3: mint the authoritative lease (atomically fences any old lease).
	newLease, old := r.directory.Attach(principal.DeviceID, principal.DeviceName, connectionID, sessionID)
	if newLease == nil {
		return nil, contract.ControlSnapshot{}, old, &ControlGateError{Kind: DenyControlUnavailable}
	}
	// Steps 4–5: atomic rebind + subscribe + snapshot under stateMu.
	var sub *hubSubscriber
	snapshot, arbErr := r.arbiter.AttachView(sessionID, newLease, func(viewer contract.DeviceID) {
		sub = r.hub.Subscribe(sessionID, viewer, newLease, consumer)
	})
	if arbErr != nil {
		// Attach failed: fence the lease we just minted and unsubscribe.
		newLease.fence()
		r.directory.Detach(principal.DeviceID, sessionID, newLease.AttachmentGeneration())
		if sub != nil {
			r.hub.Unsubscribe(sub)
		}
		return nil, contract.ControlSnapshot{}, old, arbErr
	}
	handle = &ControlAttachmentHandle{
		sessionID:  sessionID,
		deviceID:   principal.DeviceID,
		lease:      newLease,
		subscriber: sub,
	}
	return handle, snapshot, old, nil
}

// DetachControl fences the lease, unsubscribes the hub subscriber, and (for an
// unexpected disconnect) triggers the grace path on the arbiter (design §7.1
// step 6, §7.2). For a graceful detach the caller is expected to have already
// released control via gate.Release; DetachControl only cleans up the
// attachment.
//
// unexpected=true denotes a transport disconnect (the holder enters grace if it
// was the connected device holder). unexpected=false denotes a graceful close
// (the holder is left as-is; the caller controls release via the gate).
func (r *ControlRuntime) DetachControl(handle *ControlAttachmentHandle, unexpected bool) {
	if handle == nil || handle.lease == nil {
		return
	}
	// Step 6: exact-generation detach/fence lease (stale detach is a no-op).
	detached := r.directory.Detach(handle.deviceID, handle.sessionID, handle.lease.AttachmentGeneration())
	if handle.subscriber != nil {
		r.hub.Unsubscribe(handle.subscriber)
	}
	if unexpected && detached {
		// Unexpected disconnect → grace path (arms the 30s timer if this was the
		// connected device holder). The lease is already fenced by Detach; the
		// arbiter re-checks exact identity before entering grace.
		r.arbiter.OnUnexpectedDetachForSession(handle.sessionID, handle.lease, r.arbiter.clock.Now())
	}
}

// ---------------------------------------------------------------------------
// RunEventProjector (design §8.6) — the sole run-scoped PTY output/exit producer
// ---------------------------------------------------------------------------

// RunEventProjector is the central exact-run projector for PTY output/exit. It
// implements pty.RunEventSink (structural typing — the pty package does not
// import remote). Raw PTY goroutines call OfferOutput/OfferExit with the opaque
// run handle; the projector validates the handle against the arbiter's current
// run and emits run-tagged Wails events only if the run is still current.
//
// Stale/duplicate observations (old run, restarted session) are silent no-ops
// (design §8.6.1, INV-08).
type RunEventProjector struct {
	arbiter *ControlArbiter
	log     *logging.Service

	// M-003: the H1 committer + feed + causal hub + v1 stream pump. When wired,
	// OfferOutput/OfferExit COMMIT each observation to the H1 feed (the unique
	// producer) and incrementally pump it to the v1 stream Seq + causal hub so
	// attached remote clients receive live data. nil committer = legacy path
	// (arbiter validation only).
	committer  RunSegmentCommitter
	feed       LiveRunContinuityFeed
	hub        *SessionEventHub
	streamPump liveStreamPump

	mu    sync.Mutex
	ctx   context.Context // Wails app context for EventsEmit
	runs  map[contract.SessionID]*runProjection
	ready bool
}

// liveStreamPump is the incremental v1-stream pump surface the projector needs
// (M-003). *SessionStreamStore satisfies it.
type liveStreamPump interface {
	PumpIncremental(sessionID contract.SessionID, feed LiveRunContinuityFeed, pub SessionCausalPublicationPort) RunCausalPosition
}

// runProjection tracks the current run's token/version for a session (for
// Wails envelope tagging + snapshot). Set on ActivateRun, cleared on remove.
type runProjection struct {
	token   string
	version string // decimal string of runEpoch (avoids JS uint64 precision loss)
	// flushing holds a bounded restart-prefix barrier for Wails projection.
	// Post-activate callbacks wait on flushDone without holding any lock, so the
	// staged prefix cannot be overtaken and no unbounded side queue is created.
	flushing  bool
	flushDone chan struct{}
}

type runProjectionEvent struct {
	seq      uint64
	data     []byte
	isExit   bool
	exitCode uint32
}

// NewRunEventProjector creates a projector in the not-ready state.
func NewRunEventProjector(arbiter *ControlArbiter, log *logging.Service) *RunEventProjector {
	return &RunEventProjector{
		arbiter: arbiter,
		log:     log,
		runs:    make(map[contract.SessionID]*runProjection),
	}
}

// SetH1Wiring injects the H1 committer + feed + causal hub (M-003). Called by
// NewControlRuntime after the committer/feed/hub are constructed.
func (p *RunEventProjector) SetH1Wiring(committer RunSegmentCommitter, feed LiveRunContinuityFeed, hub *SessionEventHub) {
	p.mu.Lock()
	p.committer = committer
	p.feed = feed
	p.hub = hub
	p.mu.Unlock()
}

// SetStreamPump injects the v1 stream pump (M-003). Called by the adapter after
// the SessionStreamStore is created.
func (p *RunEventProjector) SetStreamPump(pump liveStreamPump) {
	p.mu.Lock()
	p.streamPump = pump
	p.mu.Unlock()
}

// MarkReady enables the projector.
func (p *RunEventProjector) MarkReady() {
	p.mu.Lock()
	p.ready = true
	p.mu.Unlock()
}

// SetContext injects the Wails app context.
func (p *RunEventProjector) SetContext(ctx context.Context) {
	p.mu.Lock()
	p.ctx = ctx
	p.mu.Unlock()
}

// TrackRun records the current run identity for a session (called by the App
// after ActivateRun). This populates the token/version used in Wails envelopes
// and the history snapshot. M-003: it also activates H1 segment 1 (writing the
// runActivated first record) so subsequent CommitRunObservation calls commit
// into an initialized segment.
func (p *RunEventProjector) TrackRun(sessionID contract.SessionID, permit *RunObservationPermit) {
	if permit == nil || permit.run == nil {
		return
	}
	p.mu.Lock()
	closeProjectionFlushLocked(p.runs[sessionID])
	p.runs[sessionID] = &runProjection{
		token:   permit.run.desktopRunToken,
		version: strconv.FormatUint(permit.runEpoch, 10),
	}
	committer := p.committer
	p.mu.Unlock()
	if committer != nil {
		committer.ActivateFirstSegment(permit, sessionID)
	}
}

// ForgetRun clears the run projection for a session (remove/shutdown).
func (p *RunEventProjector) ForgetRun(sessionID contract.SessionID) {
	p.mu.Lock()
	closeProjectionFlushLocked(p.runs[sessionID])
	delete(p.runs, sessionID)
	p.mu.Unlock()
}

// TrackRestartRun records the NEW run identity for a session after a restart
// (M-004), WITHOUT re-activating H1 segment 1 (the restart boundary already
// began the new segment via CommitRestartSegment). It only refreshes the run
// token/version used in Wails envelopes + history snapshots so post-restart
// output is tagged with the new run.
func (p *RunEventProjector) TrackRestartRun(sessionID contract.SessionID, permit *RunObservationPermit) {
	p.TrackRestartRunStaged(sessionID, permit, nil)
}

// TrackRestartRunStaged publishes the new Wails run token while entry.stateMu
// is still held and arms a projection barrier when a pre-activate prefix exists.
// Raw/event I/O is deferred to FlushRestartStage after stateMu is released.
func (p *RunEventProjector) TrackRestartRunStaged(sessionID contract.SessionID, permit *RunObservationPermit, staged []LiveRunRecord) {
	if permit == nil || permit.run == nil {
		return
	}
	p.mu.Lock()
	closeProjectionFlushLocked(p.runs[sessionID])
	rp := &runProjection{
		token:    permit.run.desktopRunToken,
		version:  strconv.FormatUint(permit.runEpoch, 10),
		flushing: len(staged) > 0,
	}
	if rp.flushing {
		rp.flushDone = make(chan struct{})
	}
	p.runs[sessionID] = rp
	p.mu.Unlock()
}

// FlushRestartStage emits the hidden Wails prefix after the new token is
// published. New callbacks wait behind flushDone without holding a lock; the
// barrier opens atomically only after the complete staged prefix is emitted.
func (p *RunEventProjector) FlushRestartStage(sessionID contract.SessionID, staged []LiveRunRecord) {
	if len(staged) == 0 {
		return
	}
	events := make([]runProjectionEvent, 0, len(staged))
	for _, rec := range staged {
		switch rec.Kind {
		case LiveRecordOutput:
			events = append(events, runProjectionEvent{seq: rec.ProjectionSeq, data: append([]byte(nil), rec.Output...)})
		case LiveRecordExit:
			events = append(events, runProjectionEvent{isExit: true, exitCode: uint32(max(rec.Exit.ExitCode, 0))})
		}
	}

	p.mu.Lock()
	rp := p.runs[sessionID]
	if rp == nil || !rp.flushing {
		p.mu.Unlock()
		return
	}
	ctx, token, version := p.ctx, rp.token, rp.version
	p.mu.Unlock()

	for _, event := range events {
		p.emitNow(ctx, sessionID, token, version, event)
	}
	p.mu.Lock()
	if p.runs[sessionID] == rp {
		closeProjectionFlushLocked(rp)
	}
	p.mu.Unlock()
}

func closeProjectionFlushLocked(rp *runProjection) {
	if rp == nil || !rp.flushing {
		return
	}
	rp.flushing = false
	if rp.flushDone != nil {
		close(rp.flushDone)
		rp.flushDone = nil
	}
}

// PumpPending incrementally pumps newly-committed feed records (e.g. the restart
// boundary committed by CommitRestartSegment, which is not driven by an
// OfferOutput call) to the v1 stream Seq + causal hub (M-004). Idempotent.
func (p *RunEventProjector) PumpPending(sessionID contract.SessionID) {
	p.mu.Lock()
	feed := p.feed
	hub := p.hub
	pump := p.streamPump
	p.mu.Unlock()
	if feed != nil && pump != nil {
		pump.PumpIncremental(sessionID, feed, hub)
	}
}

// OfferOutput implements pty.RunEventSink. M-003: it is the UNIQUE production
// producer — it first commits the observation to the H1 feed (CommitRunObservation)
// and incrementally pumps the committed record to the v1 stream Seq + causal hub,
// then emits the run-tagged Wails event. Stale/dropped observations are silent
// no-ops (no emit). When the H1 wiring is absent it falls back to the legacy
// arbiter-validation path.
func (p *RunEventProjector) OfferOutput(runHandle any, seq uint64, data []byte) {
	permit, ok := runHandle.(*RunObservationPermit)
	if !ok || permit == nil {
		return // untyped/nil handle: drop (fail-closed)
	}
	sid := sessionIDFromPermit(permit)
	p.mu.Lock()
	committer := p.committer
	feed := p.feed
	hub := p.hub
	pump := p.streamPump
	p.mu.Unlock()
	if committer != nil {
		staged := false
		// Split >32KiB chunks (caller contract; defend here too).
		for start := 0; start < len(data); start += liveFeedMaxOutputRecordBytes {
			end := start + liveFeedMaxOutputRecordBytes
			if end > len(data) {
				end = len(data)
			}
			chunk := data[start:end]
			obs := NewOutputObservation(chunk)
			obs.ProjectionSeq = seq
			outcome := committer.CommitRunObservation(permit, obs)
			if outcome.Disposition == ObservationCommitted && feed != nil && pump != nil {
				pump.PumpIncremental(sid, feed, hub)
			}
			if outcome.Disposition == ObservationStaged {
				staged = true
				continue
			}
			if outcome.Disposition != ObservationCommitted {
				return // stale/dropped: do not emit
			}
		}
		if staged {
			return // activation flushes the staged Wails prefix under the new token
		}
	} else if !p.arbiter.ObserveOutput(permit) {
		return // stale/duplicate run: silent no-op
	}
	p.emit(sid, seq, data, false, 0)
}

// OfferExit implements pty.RunEventSink. M-003: it commits the exit observation
// to the H1 feed (which updates the authoritative state mirror under the unified
// domain) and pumps it, then emits the run-tagged Wails event.
func (p *RunEventProjector) OfferExit(runHandle any, exitCode uint32, failed bool) {
	permit, ok := runHandle.(*RunObservationPermit)
	if !ok || permit == nil {
		return
	}
	sid := sessionIDFromPermit(permit)
	obs := ProcessExitObservation{ExitCode: int(exitCode), Failed: failed}
	p.mu.Lock()
	committer := p.committer
	feed := p.feed
	hub := p.hub
	pump := p.streamPump
	p.mu.Unlock()
	if committer != nil {
		outcome := committer.CommitRunObservation(permit, NewExitObservation(obs))
		if outcome.Disposition == ObservationCommitted && feed != nil && pump != nil {
			pump.PumpIncremental(sid, feed, hub)
		}
		if outcome.Disposition == ObservationStaged {
			return // terminal latch is consumed by activation/abort, never old-token emit
		}
		if outcome.Disposition != ObservationCommitted {
			return // stale/duplicate terminal: silent no-op
		}
	} else if !p.arbiter.ObserveExit(permit, obs) {
		return // stale/duplicate exit: silent no-op
	}
	p.emit(sid, 0, nil, true, exitCode)
}

// sessionIDFromPermit extracts the SessionID from a permit's entry.
func sessionIDFromPermit(permit *RunObservationPermit) contract.SessionID {
	if permit == nil || permit.entry == nil {
		return ""
	}
	return permit.entry.sessionID
}

// emit sends the run-tagged Wails event. For output: {r,v,s,d}. For exit:
// {r,v,exitCode}. The topic names pty:data:<id> / pty:exit:<id> are preserved
// (design §8.6.1: reduce migration surface). r/v are the opaque run token and
// decimal version; existing frontend consumers ignore unknown keys and read
// s/d/exitCode as before (A2 compat; A3 adds strict token filtering).
func (p *RunEventProjector) emit(sessionID contract.SessionID, seq uint64, data []byte, isExit bool, exitCode uint32) {
	event := runProjectionEvent{seq: seq, data: append([]byte(nil), data...), isExit: isExit, exitCode: exitCode}
	for {
		p.mu.Lock()
		ctx := p.ctx
		rp := p.runs[sessionID]
		if rp != nil && rp.flushing {
			done := rp.flushDone
			p.mu.Unlock()
			if done != nil {
				<-done
				continue // reload token/version after the barrier opens
			}
			continue
		}
		token := ""
		version := "0"
		if rp != nil {
			token = rp.token
			version = rp.version
		}
		p.mu.Unlock()
		p.emitNow(ctx, sessionID, token, version, event)
		return
	}
}

// emitNow performs Wails I/O with no projector/state/feed lock held.
func (p *RunEventProjector) emitNow(ctx context.Context, sessionID contract.SessionID, token, version string, event runProjectionEvent) {
	if ctx == nil {
		return // no Wails context: drop (fail-closed; should not happen post-Startup)
	}
	if event.isExit {
		wailsRuntime.EventsEmit(ctx, "pty:exit:"+string(sessionID), map[string]any{
			"r":        token,
			"v":        version,
			"exitCode": event.exitCode,
		})
		return
	}
	wailsRuntime.EventsEmit(ctx, "pty:data:"+string(sessionID), map[string]any{
		"r": token,
		"v": version,
		"s": event.seq,
		"d": base64.StdEncoding.EncodeToString(event.data),
	})
}

// RunSnapshotResult is the run-tagged history snapshot (design §8.6.1: data/seq
// + runToken/runVersion). Not a frozen v1 DTO; desktop-internal JSON only.
type RunSnapshotResult struct {
	Data       string `json:"data"`
	Seq        uint64 `json:"seq"`
	RunToken   string `json:"runToken"`
	RunVersion string `json:"runVersion"`
}

// GetRunSnapshot returns the current run's token/version. The history bytes and
// seq are obtained from the PTY service (App-level); this method adds the run
// identity. If no run is tracked, token/version are empty (consumer should
// treat as no active run).
func (p *RunEventProjector) GetRunSnapshot(sessionID contract.SessionID) (token, version string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rp := p.runs[sessionID]
	if rp == nil {
		return "", ""
	}
	return rp.token, rp.version
}

// FormatSnapshotJSON assembles the run-tagged snapshot JSON. Called by the App's
// GetOutputHistorySnapshot after obtaining history bytes + seq from the PTY.
func (p *RunEventProjector) FormatSnapshotJSON(sessionID contract.SessionID, history []byte, seq uint64) (string, error) {
	token, version := p.GetRunSnapshot(sessionID)
	result := RunSnapshotResult{
		Data:       base64.StdEncoding.EncodeToString(history),
		Seq:        seq,
		RunToken:   token,
		RunVersion: version,
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal history snapshot: %w", err)
	}
	return string(b), nil
}
