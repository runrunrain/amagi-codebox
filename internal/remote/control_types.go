package remote

// control_types.go — M3-A control arbitration: internal types, enums and
// frozen safety constants (design §4.2, §6.1, §9.1.1).
//
// All types in this file are INTERNAL (unexported or capability-typed). They
// are never serialized to wire, never constructed from request/frame data, and
// never logged with credential/input content. Wire DTOs live in
// internal/remote/contract and are consumed only via Validate/Marshal.

import (
	"context"
	"sync/atomic"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Frozen safety deadlines (design §9.1.1)
//
// These are M3-A correctness deadlines, NOT performance tuning values. Tests
// inject a fake Clock; wall-clock is only a deadlock sentinel. Implementation
// MUST NOT change these to unbounded waits.
// ---------------------------------------------------------------------------

const (
	// controlStateTransitionBudget is the max time for a state-only
	// take/release/expire/revoke commit + in-memory event reservation. Exceeding
	// it latches control health; state fence NEVER waits for raw I/O.
	controlStateTransitionBudget = 250 * time.Millisecond

	// controlOperationLaneWaitTimeout is the max wait for a previous operation
	// to release the per-session lane.
	controlOperationLaneWaitTimeout = 1 * time.Second

	// controlDataOperationTimeout is the total deadline for a single Write / paste
	// chunk / Resize including lane wait.
	controlDataOperationTimeout = 1 * time.Second

	// controlRawEffectTimeout is the bounded budget for a single raw PTY effect
	// (WriteRaw/ResizeRaw) executed behind the gate (M-009). The underlying
	// syscall cannot be interrupted, so on expiry the gate quarantines the
	// backend (backendEpoch isolation) and returns a typed timeout; the
	// in-flight goroutine is left to complete on its own and its late result is
	// dropped by the committer's backendEpoch check. Generous enough that a
	// healthy write (sub-millisecond) never trips, bounded so a stuck syscall
	// cannot occupy the caller/lane indefinitely.
	controlRawEffectTimeout = 3 * time.Second

	// controlLifecycleEffectTimeout is the bounded budget for a single lifecycle
	// raw effect (Checkpoint + CloseSession). The PTY Close itself caps its read-
	// loop wait at ptyCloseWaitTimeout (3s), so this budget never false-triggers
	// on a healthy close while still bounding a hung backend (M-009).
	controlLifecycleEffectTimeout = 5 * time.Second

	// controlCancelAckTimeout is the max wait for a fenced operation to
	// acknowledge after cancel. If exceeded, the backend is quarantined.
	controlCancelAckTimeout = 1 * time.Second

	// controlGracefulStopTimeout is the graceful stop deadline before force.
	controlGracefulStopTimeout = 2 * time.Second

	// controlForceDetachTimeout is the force close/kill acknowledgement window.
	controlForceDetachTimeout = 1 * time.Second

	// controlSessionStopTotalTimeout is the worst-case 1s drain + 2s graceful +
	// 1s force.
	controlSessionStopTotalTimeout = 4 * time.Second

	// controlLaunchStepTimeout is the per-effect deadline for a single
	// proxy/headroom/config/process start.
	controlLaunchStepTimeout = 5 * time.Second

	// controlShutdownCleanupTimeout is the aggregate deadline for app shutdown
	// parallel cleanup of all sessions/shared effects.
	controlShutdownCleanupTimeout = 5 * time.Second

	// controlGraceDuration is the device disconnect retention period (C-004
	// provisional 30s; adjustable via constructor for tests).
	controlGraceDuration = 30 * time.Second
)

// ---------------------------------------------------------------------------
// Owner state machine enums (design §4.2)
// ---------------------------------------------------------------------------

// ownerKind is the closed set of control holder kinds.
type ownerKind uint8

const (
	ownerNone ownerKind = iota
	ownerDesktop
	ownerDevice
)

// devicePhase tracks a device holder's sub-state (connected vs grace).
type devicePhase uint8

const (
	deviceConnected devicePhase = iota + 1
	deviceGrace
)

// controlOwner is the per-session holder record. Device/desktop payloads are
// mutually exclusive with kind.
type controlOwner struct {
	kind ownerKind

	// kind==ownerDesktop: source is the opaque desktop authority fingerprint.
	// Used only for exact-match legacy-local disconnect fencing; wire always
	// projects "desktop".
	desktopSource uint64

	// kind==ownerDevice: authorization is based on ID + server-minted lease only.
	// deviceName is projection-only (from authenticated record), never an
	// authorization basis.
	deviceID             contract.DeviceID
	deviceName           string
	connectionID         ConnectionID
	attachmentGeneration uint64
	phase                devicePhase
	graceDeadline        time.Time
}

// ---------------------------------------------------------------------------
// Run identity (design §4.2, §6.1, §6.4)
// ---------------------------------------------------------------------------

// runPhase tracks a run's lifecycle within a session.
type runPhase uint8

const (
	runAbsent   runPhase = iota // no run yet (staging before RegisterStartingSession)
	runStarting                 // staging entry exists; not yet visible to remote
	runActive                   // bootstrap complete; writes allowed
	runTerminal                 // process exited/failed or being stopped
)

// runIdentity is the server-minted per-run identity. Authorization requires
// POINTER exact-match (not just nonce equality) PLUS runEpoch equality.
type runIdentity struct {
	nonce           uint64 // server-minted; pointer identity is authoritative
	desktopRunToken string // random opaque alias for Wails filtering only
}

// ---------------------------------------------------------------------------
// Backend health (design §4.2, §9.1.3)
// ---------------------------------------------------------------------------

type backendHealth uint8

const (
	backendHealthy backendHealth = iota + 1
	backendDraining
	backendQuarantined
)

// BackendDetachDisposition is the typed quarantine/detach outcome attached to
// timeout denials. It lets internal callers/tests distinguish a quarantine that
// could not be established from one whose exact old backend is still retrying
// or has been confirmed detached, without exposing raw OS errors.
type BackendDetachDisposition uint8

const (
	BackendDetachUnquarantinedFailed BackendDetachDisposition = iota + 1
	BackendDetachQuarantinedPending
	BackendDetachQuarantinedDetached
)

func (d BackendDetachDisposition) String() string {
	switch d {
	case BackendDetachUnquarantinedFailed:
		return "unquarantined-failed"
	case BackendDetachQuarantinedPending:
		return "quarantined-pending"
	case BackendDetachQuarantinedDetached:
		return "quarantined-detached"
	default:
		return "unknown"
	}
}

// backendDetachRecord exact-matches detach confirmation to one backendEpoch.
// A late reaper receipt from an older epoch cannot authorize lifecycle or mark
// a replacement backend healthy.
type backendDetachRecord struct {
	backendEpoch   uint64
	detachIdentity uint64
	disposition    BackendDetachDisposition
}

// ---------------------------------------------------------------------------
// Operation / launch permit types (design §4.2, §6.1)
// ---------------------------------------------------------------------------

// PTYOperation enumerates PTY mutation kinds (internal only; not wire enums).
type PTYOperation uint8

const (
	PTYInput PTYOperation = iota + 1
	PTYPasteChunk
	PTYResize
)

// LifecycleOperation enumerates session lifecycle kinds (internal only).
type LifecycleOperation uint8

const (
	LifecycleStop LifecycleOperation = iota + 1
	LifecycleRestart
	LifecycleRemove
)

// LaunchEffectKind enumerates the launch effect categories (design §6.1).
type LaunchEffectKind uint8

const (
	LaunchProxyStart LaunchEffectKind = iota + 1
	LaunchHeadroomStart
	LaunchConfigMutation
	LaunchPTYStart
	LaunchProcessStart
	LaunchBootstrapWrite
)

// ControlDenyKind is the closed internal classification of gate denials. It
// maps to stable wire error codes in the adapter layer (design §6.6).
type ControlDenyKind uint8

const (
	DenyBusy ControlDenyKind = iota + 1
	DenyNotController
	DenyNoAuthoritativeAttachment
	DenySessionNotFound
	DenySessionNotWritable
	DenyControlUnavailable
	DenyDeviceRevoked
	DenyNotAccepting
	DenyShutdown
	DenyStalePermit
	DenyLifecycleInProgress
	DenyOperationTimeout
	// DenySessionNotStopped is returned by the clear-stopped authority path when a
	// session is no longer in a terminal/stopped phase (R4-005 race guard).
	DenySessionNotStopped
)

// ControlGateError carries a fixed deny kind. Error() returns fixed short text
// only — never wraps raw backend error, holder ID, or input content.
type ControlGateError struct {
	Kind ControlDenyKind
	// Detach is set on timeout/unavailable outcomes produced by quarantine. It
	// is process-local diagnostic evidence only and is never serialized to wire.
	Detach BackendDetachDisposition
}

func (e ControlGateError) Error() string {
	switch e.Kind {
	case DenyBusy:
		return "control already held"
	case DenyNotController:
		return "current device is not the controller"
	case DenyNoAuthoritativeAttachment:
		return "active session connection required"
	case DenySessionNotFound:
		return "session not found"
	case DenySessionNotWritable:
		return "session is not writable"
	case DenyControlUnavailable:
		return "control service unavailable"
	case DenyDeviceRevoked:
		return "device has been revoked"
	case DenyNotAccepting:
		return "control service not accepting"
	case DenyShutdown:
		return "control service shutting down"
	case DenyStalePermit:
		return "stale launch permit"
	case DenyLifecycleInProgress:
		return "lifecycle operation in progress"
	case DenyOperationTimeout:
		return "control raw operation timed out"
	case DenySessionNotStopped:
		return "session is not stopped"
	default:
		return "control denied"
	}
}

// SessionMutationResult is the internal result of a lifecycle effect
// (design §6.1).
type SessionMutationResult struct {
	State           contract.SessionState
	StateChanged    bool
	RestartBoundary bool
	RestartSeq      contract.Seq
	Removed         bool
}

// ProcessExitObservation is the sanitized process exit info from a trusted
// callback (design §6.1). Raw error is never projected.
type ProcessExitObservation struct {
	ExitCode int
	Failed   bool
}

// DesktopAuthority is the opaque local-root capability. It is composed at
// runtime (one stable Wails authority); the Server mints a per-connection
// legacy authority only after the loopback+token gate. Fields have no public
// constructor; external code obtains it only via the control runtime.
type DesktopAuthority struct {
	source uint64
	legacy bool
}

// IsLegacy reports whether this authority was minted from a legacy loopback
// connection (for exact-source disconnect fencing).
func (a *DesktopAuthority) IsLegacy() bool { return a.legacy }

// Source returns the opaque source fingerprint (for exact-match fencing only).
func (a *DesktopAuthority) Source() uint64 { return a.source }

// ---------------------------------------------------------------------------
// RunEventSink — injected into raw PTY at run creation (design §6.1, §8.6).
//
// Raw PTY has NO Wails context and NO SessionID-only callback. It can only
// call this sink with the server-minted RunObservationPermit.
// ---------------------------------------------------------------------------

// RunEventSink is the sole output/exit entry point for raw PTY goroutines.
type RunEventSink interface {
	// OfferOutput delivers a PTY output chunk for the exact run identified by
	// the permit. Returns nil (no-op) if the permit is stale/duplicate.
	OfferOutput(ctx context.Context, permit *RunObservationPermit, data []byte) error
	// OfferExit delivers the process exit observation for the exact run. Returns
	// nil (no-op) if stale/duplicate. Only the current run's exit updates state.
	OfferExit(ctx context.Context, permit *RunObservationPermit, obs ProcessExitObservation) error
}

// RunEventSinkFunc is a function adapter for RunEventSink (testing).
type RunEventSinkFunc struct {
	OutputFn func(ctx context.Context, permit *RunObservationPermit, data []byte) error
	ExitFn   func(ctx context.Context, permit *RunObservationPermit, obs ProcessExitObservation) error
}

func (f RunEventSinkFunc) OfferOutput(ctx context.Context, p *RunObservationPermit, data []byte) error {
	if f.OutputFn != nil {
		return f.OutputFn(ctx, p, data)
	}
	return nil
}
func (f RunEventSinkFunc) OfferExit(ctx context.Context, p *RunObservationPermit, obs ProcessExitObservation) error {
	if f.ExitFn != nil {
		return f.ExitFn(ctx, p, obs)
	}
	return nil
}

// ---------------------------------------------------------------------------
// ControlTransition — internal event record (design §8.1, §8.3)
// ---------------------------------------------------------------------------

// controlTransitionReason is the closed internal vocabulary for control state
// transitions (design §8.3). These are NOT wire enums; the wire reason is a
// non-empty string produced from these constants.
type controlTransitionReason string

const (
	reasonAcquired            controlTransitionReason = "acquired"
	reasonReleased            controlTransitionReason = "released"
	reasonTakeover            controlTransitionReason = "takeover"
	reasonConnectionExpired   controlTransitionReason = "connection_expired"
	reasonDeviceRevoked       controlTransitionReason = "device_revoked"
	reasonServiceStopped      controlTransitionReason = "service_stopped"
	reasonSecurityUnavailable controlTransitionReason = "security_unavailable"
	reasonSessionRemoved      controlTransitionReason = "session_removed"
)

// controlTransition is the internal event produced by arbiter state commits and
// consumed by the ControlProjector for audience-relative projection.
type controlTransition struct {
	sessionID    contract.SessionID
	oldOwner     controlOwner
	newOwner     controlOwner
	reason       controlTransitionReason
	occurredAt   time.Time
	controlEpoch uint64
}

// ---------------------------------------------------------------------------
// Shared dependency lease (design §6.7) — types defined for A2 consumption.
// ---------------------------------------------------------------------------

// SharedServiceKind enumerates the shared singleton services.
type SharedServiceKind uint8

const (
	SharedServiceClaudeProxy SharedServiceKind = iota + 1
	SharedServiceClaudeHeadroom
	SharedServiceCodexHeadroom
)

// SharedDependencyLease is the exact-run lease on a shared singleton. A2's
// SharedServiceCoordinator mints/releases these. Exactly one of run or
// externalRun identifies the consumer; an external identity carries no Control
// write authority.
type SharedDependencyLease struct {
	sessionID         contract.SessionID
	run               *runIdentity
	externalRun       *ExternalRunIdentity
	runEpoch          uint64
	serviceGeneration uint64
}

// ---------------------------------------------------------------------------
// ShutdownCleanupPermit (design §6.1, §10.3)
// ---------------------------------------------------------------------------

// ShutdownCleanupPermit is the one-shot, infallible cleanup capability minted
// ONLY by CloseForShutdown. It can close/kill/compensate but never
// input/resize/start. External code cannot construct it.
type ShutdownCleanupPermit struct {
	generation uint64
	used       atomic.Bool
}

// ---------------------------------------------------------------------------
// Grace timer descriptor — captures exact epoch/deadline for stale suppression.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// ControlEventConsumer — the in-process delivery seam for control state
// events (design §8.1: "one writer goroutine per connection").
//
// The real M2 /ws/v1 session WS adapter implements this alongside
// ManagedV1Connection; spy connections implement it in tests. It is NOT a wire
// DTO and carries no credential/input material. The hub validates each event
// via contract.ValidateServerEvent BEFORE invoking DeliverControlState, so the
// consumer always receives a well-formed contract.ControlStateEvent.
// ---------------------------------------------------------------------------

// ControlEventConsumer delivers a validated control state event to a v1 WS
// consumer. The implementation MUST be non-blocking (bounded-buffered) and
// MUST NOT call back into the hub/arbiter/registry from within
// DeliverControlState (design §9.3: no hub → state callback).
//
// A consumer that reports it is closed/full (returns false from
// ConsumerAlive) is fenced by the hub rather than blocking the arbiter.
type ControlEventConsumer interface {
	// DeliverControlState delivers one validated control state event.
	DeliverControlState(event contract.ControlStateEvent)
	// ConsumerAlive reports whether the consumer can still receive events. A
	// closed/full consumer is fenced by the hub (slow-subscriber isolation).
	ConsumerAlive() bool
}

// ---------------------------------------------------------------------------
// Grace timer descriptor — captures exact epoch/deadline for stale suppression.
// ---------------------------------------------------------------------------

// graceTimerDescriptor is captured when a grace timer is armed. On fire, the
// callback re-checks these fields under stateMu to suppress stale timers.
type graceTimerDescriptor struct {
	sessionID            contract.SessionID
	controlEpoch         uint64
	attachmentGeneration uint64
	graceDeadline        time.Time
}

// ---------------------------------------------------------------------------
// ControlConnectionLease — server-minted authoritative attachment identity
// (design §4.1, §5.1, §7.1).
//
// Minted by AttachmentDirectory for (DeviceID, SessionID). Cannot be
// constructed from frame data. acquire/release input require an exact live
// lease.
// ---------------------------------------------------------------------------

// ControlConnectionLease is the opaque authoritative connection lease. It
// carries DeviceID + ConnectionID + attachmentGeneration + a live/fenced bit.
// A fenced lease is no longer authoritative and cannot authorize writes.
type ControlConnectionLease struct {
	deviceID             contract.DeviceID
	deviceName           string // projection only; from authenticated record
	connectionID         ConnectionID
	attachmentGeneration uint64
	live                 atomic.Bool
}

// DeviceID returns the authenticated device for this lease.
func (l *ControlConnectionLease) DeviceID() contract.DeviceID { return l.deviceID }

// DeviceName returns the projection-only device name.
func (l *ControlConnectionLease) DeviceName() string { return l.deviceName }

// ConnectionID returns the server-minted connection identifier.
func (l *ControlConnectionLease) ConnectionID() ConnectionID { return l.connectionID }

// AttachmentGeneration returns the attachment generation for stale suppression.
func (l *ControlConnectionLease) AttachmentGeneration() uint64 { return l.attachmentGeneration }

// IsLive reports whether this lease is still authoritative (not fenced).
func (l *ControlConnectionLease) IsLive() bool { return l.live.Load() }

// fence marks this lease as no longer authoritative. Idempotent.
func (l *ControlConnectionLease) fence() { l.live.Store(false) }
