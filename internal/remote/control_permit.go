package remote

// control_permit.go — Opaque capability permits (design §4.2, §6.1, §6.4).
//
// These types are NEVER constructed from request/frame data, never serialized,
// and never logged with credential/input content. They are server-minted and
// pointer/generation-identity-checked. External code obtains them only via the
// ControlGate.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// OperationPermit — the capability handed to a raw effect (design §6.1)
// ---------------------------------------------------------------------------

// operationPermit is the internal operation capability. It carries the
// snapshot of epochs at admission time and provides Checkpoint for the raw
// effect to call before each irreversible step.
//
// The permit is created under stateMu and registered as entry.currentOp. The
// raw effect runs OUTSIDE stateMu (in the lane). Checkpoint re-acquires
// stateMu to validate epochs atomically against the fence linearization point.
type operationPermit struct {
	id       uint64
	entry    *controlEntry
	arbiter  *ControlArbiter
	lane     *boundedOperationLane
	opSeq    uint64
	deviceID contract.DeviceID // empty for desktop/system
	// desktopSource is the authority fingerprint for desktop operations.
	desktopSource        uint64
	acceptanceGeneration uint64
	runtimeGeneration    uint64
	controlEpoch         uint64
	run                  *runIdentity
	runEpoch             uint64
	backendEpoch         uint64

	opCtx  context.Context // derived from caller; cancelled by cancel
	cancel context.CancelCauseFunc
	done   chan struct{}
	fenced atomic.Bool

	// restartIntent is the H1 lifecycle-intent stub for restart operations (M-004).
	// It is set only for lifecycle permits (so the restart raw effect can call H1
	// SealRestartSegment / CommitRestartSegment with the exact intent identity). It
	// is nil for ordinary write/launch operations.
	restartIntent *LifecycleIntentStub
	// restartStage is owned by this exact lifecycle permit and is read/written
	// only while entry.stateMu is held. Stage reserves a new identity/epoch but
	// deliberately leaves permit.run + entry.currentRun on the old public run;
	// activate publishes both atomically after StartProcess succeeds.
	restartStage *restartRunStage

	// lifecycle marks a permit minted for a lifecycle op (stop/restart/remove).
	// R3-004: lifecycle permits may Checkpoint on a quarantined backend so trusted
	// desktop recovery can clean up a mid-syscall-timeout session; DATA permits
	// (write/resize) are still denied on quarantined via createDesktopPermit /
	// createDevicePTYPermit.
	lifecycle bool
}

// ctx returns the operation context (cancelled on fence).
func (p *operationPermit) ctx() context.Context { return p.opCtx }

// Checkpoint is called by the raw effect immediately before each irreversible
// step. It atomically validates the permit against the current entry state
// under stateMu. On success, this step is logically admitted (pre-fence); a
// concurrent fence that runs after this Checkpoint won't prevent this ONE step
// from physically executing, but will prevent any SECOND step or new writer
// (design §5.2 INV-05, §9.1.3).
//
// Returns:
//   - nil: this step is admitted.
//   - errOperationFenced: a fence has committed a newer generation.
//   - errBackendQuarantined: the backend has been quarantined.
//   - ctx.Err(): the operation context was cancelled.
func (p *operationPermit) Checkpoint(ctx context.Context, step uint32) error {
	// Fast path: if already fenced, fail immediately without locking.
	if p.fenced.Load() {
		return errOperationFenced
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	// Atomic validation under stateMu (linearization with fence).
	p.entry.stateMu.Lock()
	defer p.entry.stateMu.Unlock()

	if p.fenced.Load() {
		return errOperationFenced
	}
	if p.entry.controlEpoch != p.controlEpoch {
		return errOperationFenced
	}
	if p.entry.currentRun != p.run || p.entry.runEpoch != p.runEpoch {
		return errOperationFenced
	}
	if p.entry.backendEpoch != p.backendEpoch {
		return errOperationFenced
	}
	// R3-004: a quarantined backend (mid-syscall timeout) denies DATA writes, but
	// a LIFECYCLE permit (stop/restart/remove) may still Checkpoint so trusted
	// desktop recovery can clean up the session (the lifecycle raw effect is safe
	// on a force-detached backend). The lifecycle flag is set only on permits
	// minted via createLifecyclePermit / createDeviceLifecyclePermit.
	if p.entry.backend == backendQuarantined && !p.lifecycle {
		return errBackendQuarantined
	}
	// Verify this permit is still the current operation.
	if p.entry.currentOp != p {
		return errOperationFenced
	}
	// M-002: global acceptance-generation fence (server Stop/latch advanced) and
	// device-revoke fence. These complement the per-entry generation checks so a
	// device operation whose permit was minted before a revoke/Stop fails even if
	// the per-entry fence linearized just ahead of this Checkpoint step. Lock
	// order is stateMu → permitMu (no path nests permitMu → stateMu).
	if p.arbiter != nil {
		if p.acceptanceGeneration != p.arbiter.acceptanceGeneration.Load() {
			return errOperationFenced
		}
		if p.deviceID != "" {
			p.arbiter.permitMu.Lock()
			revoked := p.arbiter.revokedDevices[p.deviceID]
			p.arbiter.permitMu.Unlock()
			if revoked {
				return errOperationFenced
			}
		}
	}
	return nil
}

// finish marks the operation complete and signals done. Called by the gate
// after the raw effect returns (under stateMu).
func (p *operationPermit) finish() {
	if p.done != nil {
		select {
		case <-p.done:
		default:
			close(p.done)
		}
	}
}

// restartRunStage is the hidden half of a same-ID restart transaction. It is
// never serialized or exposed as authority. old* exact-match the still-public
// entry; new* are reserved at stage and become public only at activate.
type restartRunStage struct {
	oldRun      *runIdentity
	oldRunEpoch uint64
	newRun      *runIdentity
	newRunEpoch uint64
	seal        *RunSegmentSealReceipt
	// observations is the bounded, hidden pre-activate FIFO owned by this exact
	// restart transaction. Access is serialized by entry.stateMu. A terminal
	// record latches the stage so activation fails instead of publishing running.
	observations *LiveRestartStage
}

// ---------------------------------------------------------------------------
// LaunchPermit — launch fencing capability (design §6.1, §6.4)
// ---------------------------------------------------------------------------

// LaunchPermit binds a launch transaction to {DeviceID, acceptanceGeneration,
// runtimeGeneration, launchGeneration}. Device permits also carry the device
// identity for revoked-set checks. Every launch step revalidates this permit.
//
// External code CANNOT construct a LaunchPermit; it is minted exclusively by
// the gate's BeginDesktopLaunch / BeginDeviceLaunch.
type LaunchPermit struct {
	deviceID             contract.DeviceID // empty for desktop launch
	isDesktop            bool
	authority            *DesktopAuthority // non-nil for desktop
	acceptanceGeneration uint64
	runtimeGeneration    uint64
	launchGeneration     uint64

	ctx      context.Context
	cancelFn context.CancelCauseFunc
	canceled atomic.Bool

	// compensationStack tracks applied effects for reverse-order compensation
	// on Abort. Entries are added by DoLaunchEffect before the raw call.
	compensationStack []*EffectReceipt
}

// revalidate checks that the permit is still valid against the current arbiter
// state. Called at every launch step (design §6.4.1 step 5).
func (p *LaunchPermit) revalidate(a *ControlArbiter) *ControlGateError {
	if p.canceled.Load() {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	// Runtime generation fence (shutdown).
	if p.runtimeGeneration != a.runtimeGeneration.Load() {
		return &ControlGateError{Kind: DenyShutdown}
	}
	if p.isDesktop {
		// Desktop permits are valid as long as runtime is current and not
		// canceled.
		return nil
	}
	// Device permits: check acceptance generation + revoked set.
	if p.acceptanceGeneration != a.acceptanceGeneration.Load() {
		return &ControlGateError{Kind: DenyStalePermit}
	}
	if !a.acceptingRemote.Load() {
		return &ControlGateError{Kind: DenyNotAccepting}
	}
	a.permitMu.Lock()
	revoked := a.revokedDevices[p.deviceID]
	a.permitMu.Unlock()
	if revoked {
		return &ControlGateError{Kind: DenyDeviceRevoked}
	}
	return nil
}

// cancel marks the permit canceled and cancels its context.
func (p *LaunchPermit) cancel(cause error) {
	if p.canceled.CompareAndSwap(false, true) {
		if p.cancelFn != nil {
			p.cancelFn(cause)
		}
	}
}

// IsCanceled reports whether the permit has been canceled.
func (p *LaunchPermit) IsCanceled() bool { return p.canceled.Load() }

// IsDesktop reports whether this is a desktop launch permit.
func (p *LaunchPermit) IsDesktop() bool { return p.isDesktop }

// DeviceID returns the bound device ID (empty for desktop).
func (p *LaunchPermit) DeviceID() contract.DeviceID { return p.deviceID }

// LaunchGeneration returns the unique launch generation.
func (p *LaunchPermit) LaunchGeneration() uint64 { return p.launchGeneration }

// ---------------------------------------------------------------------------
// RunPermit — run-scoped write authority (design §6.1)
// ---------------------------------------------------------------------------

// RunPermit binds a run to its launch permit + entry. It is used for
// DoLaunchEffect, DoBootstrapPTY, and ActivateRun. Pointer + runEpoch must
// exact-match the entry's current run.
type RunPermit struct {
	launch   *LaunchPermit
	entry    *controlEntry
	run      *runIdentity
	runEpoch uint64
}

// Run returns the run identity pointer (for exact-match checks).
func (p *RunPermit) Run() *runIdentity { return p.run }

// RunEpoch returns the run epoch.
func (p *RunPermit) RunEpoch() uint64 { return p.runEpoch }

// Launch returns the owning launch permit.
func (p *RunPermit) Launch() *LaunchPermit { return p.launch }

// ---------------------------------------------------------------------------
// RunObservationPermit — trusted observation identity (design §6.1, §8.6)
// ---------------------------------------------------------------------------

// RunObservationPermit is captured by raw PTY goroutines at creation time. It
// carries the exact run pointer + runEpoch + backendEpoch. Stale observations
// (pointer/epoch mismatch) are silent no-ops.
//
// External code CANNOT construct this; it is minted by RegisterStartingSession
// alongside the RunPermit.
type RunObservationPermit struct {
	entry        *controlEntry
	run          *runIdentity
	runEpoch     uint64
	backendEpoch uint64
}

// Run returns the run identity pointer.
func (p *RunObservationPermit) Run() *runIdentity { return p.run }

// RunEpoch returns the run epoch.
func (p *RunObservationPermit) RunEpoch() uint64 { return p.runEpoch }

// BackendEpoch returns the backend epoch at mint time.
func (p *RunObservationPermit) BackendEpoch() uint64 { return p.backendEpoch }

// ---------------------------------------------------------------------------
// EffectReceipt — compensation record for launch effects (design §6.1, §6.4.3)
// ---------------------------------------------------------------------------

// EffectReceipt records one applied launch effect for reverse-order
// compensation. The raw effect fills in the compensate closure before its first
// irreversible syscall. On Abort, receipts are processed in reverse order;
// unapplied receipts are no-ops.
type EffectReceipt struct {
	ownerLaunchGeneration uint64
	kind                  LaunchEffectKind
	applied               atomic.Bool
	compensate            func(context.Context) error
}

// Kind returns the effect kind.
func (r *EffectReceipt) Kind() LaunchEffectKind { return r.kind }

// IsApplied reports whether the raw effect marked this receipt as applied.
func (r *EffectReceipt) IsApplied() bool { return r.applied.Load() }

// MarkApplied marks the receipt as applied and registers the compensate closure.
// Called by the raw effect before the first irreversible syscall.
func (r *EffectReceipt) MarkApplied(compensate func(context.Context) error) {
	r.compensate = compensate
	r.applied.Store(true)
}

// ---------------------------------------------------------------------------
// mintDesktopRunToken — 128-bit random opaque alias (design §8.6.1)
// ---------------------------------------------------------------------------

// mintDesktopRunToken generates a 128-bit random hex string for local Wails
// filtering only. It is NOT an authority and never enters remote wire/log. If
// crypto/rand fails, the launch MUST fail before side effects (fail-closed).
func mintDesktopRunToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "" // caller checks and fails the launch
	}
	return hex.EncodeToString(b)
}
