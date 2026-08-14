package remote

// This file contains the shared domain types, DTOs, the Clock seam, the
// StoreMutation three-state contract, security event/health/connection domain
// types, opaque maintenance/session capability tokens, and the pairing state
// machine + deviceService + RecordDeviceSeen three-state consumption (design
// §6/§8/§9.5). Route/auth/server/app integration lives in routes_v1.go /
// device_auth.go / server.go.

import (
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// base32NoPad is uppercase RFC4648 Base32 without padding (pairing codes).
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// ---------------------------------------------------------------------------
// Clock seam (design §6.1)
// ---------------------------------------------------------------------------

// Clock is the time seam for the security core. Production uses systemClock;
// tests inject a deterministic clock. It MUST NOT be nil in any production
// constructor (programmer error → panic).
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) securityTimer
}

// securityTimer wraps a one-shot timer so tests can substitute it. (Renamed
// from Timer to avoid colliding with the existing metrics.Timer in this package.)
type securityTimer interface {
	Stop() bool
}

// systemClock is the sole production Clock; it delegates to the stdlib.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
func (systemClock) AfterFunc(d time.Duration, f func()) securityTimer {
	return stdlibTimer{time.AfterFunc(d, f)}
}

// NewSystemClock returns the production Clock (delegates to stdlib time).
// Used by the ControlRuntime for grace timers and deadline derivation.
func NewSystemClock() Clock { return systemClock{} }

type stdlibTimer struct{ *time.Timer }

func (t stdlibTimer) Stop() bool { return t.Timer.Stop() }

// ---------------------------------------------------------------------------
// Pairing policy & DTOs (design §6.1)
// ---------------------------------------------------------------------------

// PairingPolicy holds the frozen pairing parameters (provisional, M1-INT freeze).
type PairingPolicy struct {
	WindowTTL     time.Duration
	MaxAttempts   uint8
	CredentialTTL time.Duration
}

// DefaultPairingPolicy returns the design §4.1 provisional policy values.
func DefaultPairingPolicy() PairingPolicy {
	return PairingPolicy{
		WindowTTL:     3 * time.Minute,
		MaxAttempts:   5,
		CredentialTTL: 30 * 24 * time.Hour,
	}
}

// PairingWindowInfo is returned ONCE by the desktop create wrapper. Code only
// crosses the trusted Wails boundary; it is never persisted, logged or echoed
// in a server-observed URL.
type PairingWindowInfo struct {
	Generation      uint64 `json:"generation"`
	Code            string `json:"code"`
	ExpiresAt       string `json:"expiresAt"`
	BaseURL         string `json:"baseUrl,omitempty"`
	AddressRequired bool   `json:"addressRequired"`
}

// PairingWindowStatus NEVER returns Code.
type PairingWindowStatus struct {
	Active            bool   `json:"active"`
	Generation        uint64 `json:"generation,omitempty"`
	ExpiresAt         string `json:"expiresAt,omitempty"`
	RemainingAttempts uint8  `json:"remainingAttempts,omitempty"`
}

// DeviceInfo is the desktop-visible device projection. It never carries
// credential/secret/salt/code/cookie material.
type DeviceInfo struct {
	ID                  contract.DeviceID `json:"id"`
	Name                string            `json:"name"`
	PairedAt            string            `json:"pairedAt"`
	LastSeenAt          string            `json:"lastSeenAt"`
	CredentialExpiresAt string            `json:"credentialExpiresAt"`
	RevokedAt           *string           `json:"revokedAt,omitempty"`
}

// HostSummaryFunc is the private app-host provider closure type.
type HostSummaryFunc func() (contract.HostSummary, error)

// ---------------------------------------------------------------------------
// Store mutation three-state contract (design §6.3, §7.7)
// ---------------------------------------------------------------------------

// StoreMutationState is the SOLE determinant of whether a store mutation took
// effect. Callers MUST NOT infer state from syscall return values, file
// existence or error==nil.
type StoreMutationState uint8

const (
	// StoreNotCommitted means read-back proved the target is still the exact old
	// bytes, or the ledger was rolled back to the proven old prefix.
	StoreNotCommitted StoreMutationState = iota + 1
	// StoreCommitted means pair/seen read back exact canonical next, or revoke
	// completed prepare+commit with the commit File.Sync succeeding.
	StoreCommitted
	// StoreIndeterminateFailClosed means the state cannot be proven and the
	// entire security face MUST latch unavailable.
	StoreIndeterminateFailClosed
)

// StoreMutationResult carries the state and durability flag. DurabilityDegraded
// may be true but can never turn a committed business outcome into a
// failure/retry.
type StoreMutationResult struct {
	State              StoreMutationState
	DurabilityDegraded bool
}

// ---------------------------------------------------------------------------
// Device credential principal & at-rest record (design §6.5, §7.2)
// ---------------------------------------------------------------------------

// DevicePrincipal is the authenticated device identity. It never contains
// secret/salt/hash. AuthenticatedAt is the single clock sample frozen at auth
// time; RecordDeviceSeen MUST reuse it (never a later clock.Now()).
type DevicePrincipal struct {
	DeviceID            contract.DeviceID
	DeviceName          string
	AuthenticatedAt     time.Time
	CredentialExpiresAt time.Time
}

// deviceSeenObservation carries the fields needed to update LastSeenAt.
type deviceSeenObservation struct {
	DeviceID            contract.DeviceID
	AuthenticatedAt     time.Time
	CredentialExpiresAt time.Time
}

// seenStoreDisposition is the closed result of a TouchSeen store decision.
type seenStoreDisposition uint8

const (
	seenPersist seenStoreDisposition = iota + 1
	seenCoalesced
	seenSkippedRevoked
	seenSkippedExpired
)

// deviceRecord is the in-memory projection of one snapshot device entry plus
// the ledger-derived revoked flag. Credential material is salt+digest only.
type deviceRecord struct {
	ID                  contract.DeviceID
	Name                string
	CredentialSalt      []byte // 16 bytes at rest
	CredentialHash      []byte // 32 bytes at rest (SHA-256 digest)
	PairedAt            time.Time
	LastSeenAt          time.Time
	CredentialExpiresAt time.Time
	RevokedAt           *time.Time // nil unless revoked
}

// ---------------------------------------------------------------------------
// Typed security events & EventID (design §13.1)
// ---------------------------------------------------------------------------

// SecurityEventID is the stable, content-addressed event identifier: strict
// RawURL base64 of a 32-byte SHA-256.
type SecurityEventID string

// PairingSecurityEventKind enumerates the closed pairing event kinds.
type PairingSecurityEventKind uint8

const (
	PairingWindowOpened PairingSecurityEventKind = iota + 1
	PairingWindowCanceled
	PairingWindowExpired
	PairingAttemptRejected
	PairingWindowLocked
)

// DeviceSecurityEventKind enumerates the closed device event kinds.
type DeviceSecurityEventKind uint8

const (
	DevicePaired DeviceSecurityEventKind = iota + 1
	DeviceRevoked
)

// StoreSecurityEventKind enumerates the closed store durability event kinds.
type StoreSecurityEventKind uint8

const (
	StoreDurabilityDegraded StoreSecurityEventKind = iota + 1
)

// PairingSecurityEvent is a closed pairing-domain security event.
type PairingSecurityEvent struct {
	EventID    SecurityEventID
	Kind       PairingSecurityEventKind
	OccurredAt time.Time
	Generation uint64
	Attempt    uint8
}

// DeviceSecurityEvent is a closed device-domain security event.
type DeviceSecurityEvent struct {
	EventID    SecurityEventID
	Kind       DeviceSecurityEventKind
	OccurredAt time.Time
	DeviceID   contract.DeviceID
}

// StoreSecurityEvent is a closed store-domain durability event.
type StoreSecurityEvent struct {
	EventID    SecurityEventID
	Kind       StoreSecurityEventKind
	OccurredAt time.Time
}

// ServiceSecurityEventKind enumerates the closed internal service lifecycle
// event kinds (NFR-17). Internal only; canonical wire is only
// {version,eventId,kind,occurredAt} — no host/port/token/path/reason.
type ServiceSecurityEventKind uint8

const (
	RemoteServiceStarted ServiceSecurityEventKind = iota + 1
	RemoteServiceStopped
	LegacyTokenRotated
	RemoteListenConfigurationChanged
)

// ServiceSecurityEvent is a closed internal service lifecycle event.
type ServiceSecurityEvent struct {
	EventID    SecurityEventID
	Kind       ServiceSecurityEventKind
	OccurredAt time.Time
}

// SecurityEvent is the closed union of all security event types. Exact field
// allowlist: eventId/kind/occurredAt/generation/attempt/deviceId. No
// message/detail/reason/map/name/code/credential/cookie/path/url/session
// content is permitted.
type SecurityEvent interface {
	securityEvent()
	EventIDOf() SecurityEventID
	OccurredAtOf() time.Time
}

func (PairingSecurityEvent) securityEvent()               {}
func (e PairingSecurityEvent) EventIDOf() SecurityEventID { return e.EventID }
func (e PairingSecurityEvent) OccurredAtOf() time.Time    { return e.OccurredAt }

func (DeviceSecurityEvent) securityEvent()               {}
func (e DeviceSecurityEvent) EventIDOf() SecurityEventID { return e.EventID }
func (e DeviceSecurityEvent) OccurredAtOf() time.Time    { return e.OccurredAt }

func (StoreSecurityEvent) securityEvent()               {}
func (e StoreSecurityEvent) EventIDOf() SecurityEventID { return e.EventID }
func (e StoreSecurityEvent) OccurredAtOf() time.Time    { return e.OccurredAt }

func (ServiceSecurityEvent) securityEvent()               {}
func (e ServiceSecurityEvent) EventIDOf() SecurityEventID { return e.EventID }
func (e ServiceSecurityEvent) OccurredAtOf() time.Time    { return e.OccurredAt }

// LegacyAuthCarrier enumerates the closed legacy-auth credential carriers.
type LegacyAuthCarrier uint8

const (
	CarrierBearer LegacyAuthCarrier = iota + 1
	CarrierQueryToken
	CarrierLocalSession
	CarrierLaunchGrant
)

// LegacyAuthRouteClass enumerates the closed legacy route classes.
type LegacyAuthRouteClass uint8

const (
	RouteBootstrap LegacyAuthRouteClass = iota + 1
	RouteAPIRead
	RouteAPIWrite
	RouteWebSocket
)

// LegacyAuthOutcome enumerates the closed legacy-auth outcomes (only accepted).
type LegacyAuthOutcome uint8

const (
	LegacyAuthAccepted LegacyAuthOutcome = iota + 1
)

// LegacyAuthSecurityEvent is a closed internal legacy-deprecation event (single
// kind legacy_auth_deprecated). Canonical wire is
// {version,eventId,kind,occurredAt,carrier,routeClass,outcome}; no
// IP/path/method/device/token/header/free text.
type LegacyAuthSecurityEvent struct {
	EventID    SecurityEventID
	OccurredAt time.Time
	Carrier    LegacyAuthCarrier
	RouteClass LegacyAuthRouteClass
	Outcome    LegacyAuthOutcome
}

func (LegacyAuthSecurityEvent) securityEvent()               {}
func (e LegacyAuthSecurityEvent) EventIDOf() SecurityEventID { return e.EventID }
func (e LegacyAuthSecurityEvent) OccurredAtOf() time.Time    { return e.OccurredAt }

// ---------------------------------------------------------------------------
// Bounded sink append outcome (design §13.2)
// ---------------------------------------------------------------------------

// EventAppendState is the closed append outcome. It is the sole ownership /
// idempotency determinant.
type EventAppendState uint8

const (
	// EventPreAcceptFailed: canonicalization/validation, single-event>1KiB, entry
	// 257, aggregate>256KiB, same-ID/different-payload or sink failure. The sink
	// does NOT take ownership and MUST NOT retain payload. No retry owner in M1-A.
	EventPreAcceptFailed EventAppendState = iota + 1
	// EventAcceptedBySink: volatile = entered bounded buffer.
	EventAcceptedBySink
	// EventAcceptedButDurabilityDegraded: only a durable sink may return this;
	// the sink owns the canonical payload/pending index. Caller never reappends.
	EventAcceptedButDurabilityDegraded
	// EventDuplicateAcceptedBySink: EventID already accepted with byte-identical
	// canonical payload. A second entry is NOT appended.
	EventDuplicateAcceptedBySink
)

// EventAppendFailure is the closed PreAccept failure category (only non-none
// when State==EventPreAcceptFailed). It lets callers map to the right closed
// health code (integrity vs capacity/io) without inspecting raw errors.
type EventAppendFailure uint8

const (
	EventFailureNone        EventAppendFailure = iota
	EventFailureInvalid                        // canonicalization/validation
	EventFailureCapacity                       // active/archive/ID cap reached (no eviction)
	EventFailureIO                             // open/write/Sync/rotation failure
	EventFailureIntegrity                      // same EventID, different canonical payload
	EventFailureUnavailable                    // sink not opened / closed / scan failed
)

// EventAppendResult is the append outcome.
type EventAppendResult struct {
	State   EventAppendState
	Failure EventAppendFailure // non-none only for PreAcceptFailed
}

// EventSinkDurability identifies the sink kind.
type EventSinkDurability uint8

const (
	EventSinkVolatile EventSinkDurability = iota + 1
	EventSinkDurable
)

// SecurityEventSink is the closed sink interface.
type SecurityEventSink interface {
	Durability() EventSinkDurability
	AppendSecurityEvent(SecurityEvent) (EventAppendResult, error)
}

// EventDurabilityConfirmation is the durable-sink confirmation result.
type EventDurabilityConfirmation struct {
	EventID      SecurityEventID
	Confirmed    bool
	PendingEmpty bool
}

// DurableSecurityEventSink is implemented by a durable sink (M1-B).
type DurableSecurityEventSink interface {
	SecurityEventSink
	ConfirmDurable(SecurityEventID) (EventDurabilityConfirmation, error)
}

// ---------------------------------------------------------------------------
// Security health domain (design §13.3)
// ---------------------------------------------------------------------------

// SecurityHealthCode is the closed set of aggregate health codes. Never
// dynamically constructed.
type SecurityHealthCode string

const (
	HealthStoreUnavailable           SecurityHealthCode = "store_unavailable"
	HealthStoreIndeterminate         SecurityHealthCode = "store_indeterminate"
	HealthStoreDurabilityDegraded    SecurityHealthCode = "store_durability_degraded"
	HealthEventAppendFailed          SecurityHealthCode = "event_append_failed"
	HealthEventDurabilityDegraded    SecurityHealthCode = "event_durability_degraded"
	HealthEventIntegrityFailed       SecurityHealthCode = "event_integrity_failed"
	HealthDeviceSeenPersistFailed    SecurityHealthCode = "device_seen_persist_failed"
	HealthSnapshotTempCleanupFailed  SecurityHealthCode = "snapshot_temp_cleanup_failed"
	HealthSnapshotTempBudgetExceeded SecurityHealthCode = "snapshot_temp_budget_exceeded"
)

// MaxSecurityHealthRecentEventIDs bounds the per-code recent EventID ring.
const MaxSecurityHealthRecentEventIDs = 8

// SecurityHealthIssue is one bounded aggregate issue. No payload/free
// text/path/raw error/secret/code is stored.
type SecurityHealthIssue struct {
	Code            SecurityHealthCode `json:"code"`
	Active          bool               `json:"active"`
	Acknowledged    bool               `json:"acknowledged"`
	FirstObservedAt string             `json:"firstObservedAt"`
	LastObservedAt  string             `json:"lastObservedAt"`
	Occurrences     uint64             `json:"occurrences"`     // saturates at MaxUint64
	DroppedEventIDs uint64             `json:"droppedEventIds"` // sample evictions; saturating
	RecentEventIDs  []SecurityEventID  `json:"recentEventIds"`  // defensive copy; max 8
}

// SecurityHealthSnapshot is the desktop query result.
type SecurityHealthSnapshot struct {
	SecurityReady bool                  `json:"securityReady"`
	Issues        []SecurityHealthIssue `json:"issues"`
}

// ---------------------------------------------------------------------------
// Capacity (C-010) typed errors (design §6.1)
// ---------------------------------------------------------------------------

// SecurityCapacityCode identifies which admission capacity was exhausted. Cap
// only blocks NEW pair/window; existing auth/revoke always remain.
type SecurityCapacityCode uint8

const (
	CapacityKnownDeviceIDs SecurityCapacityCode = iota + 1
	CapacitySnapshotBytes
	CapacityRevocationReserve
	CapacitySnapshotTemps
)

// SecurityCapacityError is the fixed closed-text capacity error. It never
// carries path/raw JSON/counts.
type SecurityCapacityError struct {
	Code SecurityCapacityCode
}

func (e SecurityCapacityError) Error() string {
	switch e.Code {
	case CapacityKnownDeviceIDs:
		return "security capacity reached"
	case CapacitySnapshotBytes:
		return "security capacity reached"
	case CapacityRevocationReserve:
		return "security capacity reached"
	case CapacitySnapshotTemps:
		return "security capacity reached"
	default:
		return "security capacity reached"
	}
}

// ---------------------------------------------------------------------------
// Connection registry domain (design §6.6)
// ---------------------------------------------------------------------------

// ConnectionID is the opaque connection identifier.
type ConnectionID string

// ConnectionTerminationCause is the closed cause of a connection termination.
type ConnectionTerminationCause uint8

const (
	TerminationDeviceRevoked ConnectionTerminationCause = iota + 1
	TerminationServerStopped
	TerminationDuplicateConnectionID
	TerminationSecurityStateUnavailable
)

// ConnectionTermination describes why/how a connection is being terminated.
type ConnectionTermination struct {
	Cause      ConnectionTerminationCause
	OccurredAt time.Time
}

// ManagedV1Connection is implemented by a real (future) WS adapter. Terminate
// MUST synchronously fence future business writes, enqueue the terminal
// event/close, and MUST NEVER call back into the registry.
type ManagedV1Connection interface {
	Terminate(ConnectionTermination)
}

// ConnectionRegistrationOutcome is the closed registration result.
type ConnectionRegistrationOutcome uint8

const (
	RegistrationAccepted ConnectionRegistrationOutcome = iota + 1
	RegistrationRejectedNotAccepting
	RegistrationRejectedRevoked
	RegistrationRejectedDuplicateLive
)

// ConnectionRegistration is the opaque registration handle (epoch-guarded).
type ConnectionRegistration struct {
	deviceID     contract.DeviceID
	connectionID ConnectionID
	epoch        uint64
}

// ConnectionRegistrationResult is the registration outcome (+handle on accept).
type ConnectionRegistrationResult struct {
	Outcome      ConnectionRegistrationOutcome
	Registration ConnectionRegistration
}

// ConnectionRegistrationError is the closed typed registration error. Error()
// is fixed closed text only.
type ConnectionRegistrationError struct {
	Outcome ConnectionRegistrationOutcome
}

func (e ConnectionRegistrationError) Error() string {
	switch e.Outcome {
	case RegistrationRejectedNotAccepting:
		return "connection registration rejected: not accepting"
	case RegistrationRejectedRevoked:
		return "connection registration rejected: device revoked"
	case RegistrationRejectedDuplicateLive:
		return "connection registration rejected: duplicate live connection id"
	default:
		return "connection registration rejected"
	}
}

// ---------------------------------------------------------------------------
// Revoke / seen delivery DTOs (design §6.1)
// ---------------------------------------------------------------------------

// SecurityEventDeliveryOutcome is the closed delivery outcome surfaced to the
// desktop. It maps from EventAppendResult plus the duplicate-revoke case.
type SecurityEventDeliveryOutcome string

const (
	EventNotEmittedDuplicate        SecurityEventDeliveryOutcome = "not_emitted_duplicate"
	EventAccepted                   SecurityEventDeliveryOutcome = "accepted"
	EventFailed                     SecurityEventDeliveryOutcome = "failed"
	EventAcceptedDurabilityDegraded SecurityEventDeliveryOutcome = "accepted_durability_degraded"
	EventDuplicateAccepted          SecurityEventDeliveryOutcome = "duplicate_accepted"
)

// RevokeDeviceResult is the desktop revoke result. TerminationRequestedConnections
// only counts detached/enqueued connections; it does NOT claim network close.
type RevokeDeviceResult struct {
	Device                          DeviceInfo                   `json:"device"`
	AlreadyRevoked                  bool                         `json:"alreadyRevoked"`
	TerminationRequestedConnections int                          `json:"terminationRequestedConnections"`
	EventOutcome                    SecurityEventDeliveryOutcome `json:"eventOutcome"`
	DurabilityDegraded              bool                         `json:"durabilityDegraded"`
}

// DeviceSeenOutcome is the closed RecordDeviceSeen outcome.
type DeviceSeenOutcome string

const (
	SeenPersisted               DeviceSeenOutcome = "persisted"
	SeenCoalesced               DeviceSeenOutcome = "coalesced"
	SeenSkippedRevoked          DeviceSeenOutcome = "skipped_revoked"
	SeenSkippedExpired          DeviceSeenOutcome = "skipped_expired"
	SeenCapacityUnavailable     DeviceSeenOutcome = "capacity_unavailable"
	SeenNotCommitted            DeviceSeenOutcome = "not_committed"
	SeenIndeterminateFailClosed DeviceSeenOutcome = "indeterminate_fail_closed"
)

// DeviceSeenResult is the backend integration result (not a v1 wire DTO).
type DeviceSeenResult struct {
	Outcome            DeviceSeenOutcome
	DurabilityDegraded bool
}

// DeviceRevocationNotice is the M1-A-produced revocation notice for the future
// M3 control owner. No name/reason/free text/credential.
type DeviceRevocationNotice struct {
	DeviceID   contract.DeviceID
	OccurredAt time.Time
}

// ---------------------------------------------------------------------------
// Opaque maintenance / store-mutation capabilities (design §6.2, §6.3)
// ---------------------------------------------------------------------------

// maintenanceSessionToken is an unexported, per-session pointer-identity token.
// A MaintenanceSession is authorized ONLY by the live gate mapping this pointer
// to the current epoch + process nonce; a struct copy / manifest / backupID can
// never authorize.
type maintenanceSessionToken struct{}

// MaintenanceSession is the opaque maintenance-session capability.
type MaintenanceSession struct{ token *maintenanceSessionToken }

// deviceStoreBackupToken is an unexported, per-backup pointer-identity token.
type deviceStoreBackupToken struct{}

// DeviceStoreBackup is the opaque complete-store-set backup capability. It is
// bound to the live session identity + StoreID + snapshot hash + ledger head;
// restore is authorized only by the matching live session + this pointer.
type DeviceStoreBackup struct{ token *deviceStoreBackupToken }

// permitKind identifies whether a store permit authorizes a normal operation or
// a maintenance-writer callback.
type permitKind uint8

const (
	permitNormal permitKind = iota + 1
	permitMaintWrite
)

// normalStorePermit is the unexported, pointer-identity capability the gate
// signs for a single normal operation. It carries the process nonce + epoch so
// the repository can validate it WITHOUT acquiring the gate lock (the gate
// guarantees it cannot transition out of normal while any normal permit is
// outstanding). The repository validates nonce==processNonce && kind==normal.
type normalStorePermit struct {
	nonce [32]byte
	epoch uint64
	kind  permitKind
}

// ---------------------------------------------------------------------------
// Closed store error categories (design §7.4: "ordinary error只暴露closed
// category，不含raw line/key/path/stack")
// ---------------------------------------------------------------------------

// storeErrorCategory is the closed store error category. Callers map it to the
// §12.3 fixed service-down / capacity messages; it never leaks a path/raw JSON.
type storeErrorCategory uint8

const (
	storeErrIndeterminate storeErrorCategory = iota + 1 // read-back/corrupt/cannot-classify
	storeErrCapacity                                    // C-010 admission
	storeErrEntropy                                     // random source failure
	storeErrSchema                                      // strict validation failure on load
)

// storeClosedError is the fixed closed-text store error.
type storeClosedError struct {
	category storeErrorCategory
}

func (e storeClosedError) Error() string {
	switch e.category {
	case storeErrIndeterminate:
		return "security state unavailable"
	case storeErrCapacity:
		return "security capacity reached"
	case storeErrEntropy:
		return "security state unavailable"
	case storeErrSchema:
		return "security state unavailable"
	default:
		return "security state unavailable"
	}
}

// closedStoreErr returns a closed store error for the given category.
func closedStoreErr(c storeErrorCategory) error {
	return storeClosedError{category: c}
}

// errSecurityNotReady is returned by normal store entries when the security
// face has latched unavailable. Fixed text; no path/raw JSON.
var errSecurityNotReady = errors.New("security state unavailable")

// errServerStoppedDuringStart is returned by Server.Start when a direct
// Stop/Shutdown shut the run down during the acceptance→started-emit window
// (R4-Major): the run is dead, so Start must not report success. Fixed closed
// text; no path/host/port.
var errServerStoppedDuringStart = errors.New("remote server: stopped during start")

// ---------------------------------------------------------------------------
// Pairing state machine + deviceService (design §8/§9.5/§11)
// ---------------------------------------------------------------------------

type pairingWindowState uint8

const (
	pairWindowNone pairingWindowState = iota + 1
	pairWindowActive
	pairWindowLocked
	pairWindowCanceled
	pairWindowExpired
	pairWindowConsumed
)

// pairingWindow is the single in-memory pairing window. code is a fixed [16]byte
// zeroed on every terminal transition.
type pairingWindow struct {
	state      pairingWindowState
	generation uint64
	code       [16]byte
	expiresAt  time.Time
	attempts   uint8
	timer      securityTimer
}

// pairingGrant is the private, never-JSON-marshaled result of a successful
// pairing. The secret lives only here → Set-Cookie; it is never persisted,
// logged or placed in an event.
type pairingGrant struct {
	deviceID     string
	device       DeviceInfo
	secret       []byte
	expiresAt    time.Time
	responseBody []byte
	mutation     StoreMutationResult
	eventOutcome SecurityEventDeliveryOutcome
}

// deviceService owns the pairing window, device CRUD and the revoke/seen
// three-state consumption. It is the single normal-operation entry over the
// store/registry/sink/health.
type deviceService struct {
	gate           *securityMaintenanceGate
	store          *fileDeviceStore
	registry       *connectionRegistry
	random         io.Reader
	clock          Clock
	sink           SecurityEventSink
	health         *securityHealthRegister
	policy         PairingPolicy
	processScopeID string

	// H2: control lifecycle hook for revoke/latch wiring (design §4A.3).
	// Defaults to no-op; set via SetControlLifecycleHook before Server.Start.
	controlHook ControlLifecycleHook

	mu                sync.Mutex // pairMu
	accepting         bool       // true only after a successful listener publish (Resume); false on construct/Stop/listen-fail/serve-fail
	window            pairingWindow
	generationCounter uint64
}

// newDeviceService constructs a suspended service: accepting is false until a
// successful listener publish calls Resume, so CreateWindow rejects before the
// server is actually listening. processScopeID seeds window EventIDs; it is
// generated from crypto/rand (security cannot be ready without it).
func newDeviceService(
	gate *securityMaintenanceGate,
	store *fileDeviceStore,
	registry *connectionRegistry,
	random io.Reader,
	clock Clock,
	sink SecurityEventSink,
	health *securityHealthRegister,
	policy PairingPolicy,
) (*deviceService, error) {
	scope := make([]byte, 16)
	if _, err := io.ReadFull(random, scope); err != nil {
		return nil, err
	}
	return &deviceService{
		gate: gate, store: store, registry: registry,
		random: random, clock: clock, sink: sink, health: health,
		policy: policy, processScopeID: rawURLBase64(scope),
		controlHook: noopLifecycleHook{},
	}, nil
}

// Suspend cancels the active window, wipes the code, bumps the generation so
// stale timers/callbacks cannot affect a future window, and marks the service
// suspended (CreateWindow rejects until the next Resume).
func (d *deviceService) Suspend() {
	d.mu.Lock()
	wasActive := d.window.state == pairWindowActive
	gen := d.window.generation
	d.window.state = pairWindowNone
	d.accepting = false
	d.generationCounter++
	zeroBytes(d.window.code[:])
	if d.window.timer != nil {
		d.window.timer.Stop()
		d.window.timer = nil
	}
	occurredAt := d.clock.Now().UTC()
	d.mu.Unlock()
	if wasActive {
		d.appendPairingEvent(PairingWindowCanceled, gen, 0, occurredAt)
	}
}

// Resume marks the pairing service accepting. It is called ONLY after the
// listener has been successfully published (Server.Start success path). Before
// Resume the service is suspended and CreateWindow rejects with errSecurityNotReady.
func (d *deviceService) Resume() {
	d.mu.Lock()
	d.accepting = true
	d.mu.Unlock()
}

// CreateWindow opens a new active window (canceling any prior). Code is returned
// ONCE; it is never persisted/logged/echoed in a server-observed URL. A
// suspended service (constructed / Stopped / listen-failed / serve-failed)
// rejects before any store/permit work (Major-03); the accepting flag is also
// re-checked under pairMu right before publishing the window so a racing
// Suspend cannot leave an active window on a suspended service.
func (d *deviceService) CreateWindow(baseURL string) (PairingWindowInfo, error) {
	d.mu.Lock()
	if !d.accepting {
		d.mu.Unlock()
		return PairingWindowInfo{}, errSecurityNotReady
	}
	d.mu.Unlock()
	permit, ok := d.gate.issueNormalPermit()
	if !ok {
		return PairingWindowInfo{}, errSecurityNotReady
	}
	// The permit is held across AdmitNewDevice AND window publication (the lock
	// below) so a maintenance epoch cannot interleave; returned after unlock, then
	// events are appended.
	if err := d.store.AdmitNewDevice(permit); err != nil {
		d.gate.returnNormalPermit(permit)
		return PairingWindowInfo{}, err
	}

	code := make([]byte, 16)
	defer zeroBytes(code) // wipe the temporary random code on every path after copy
	if _, err := io.ReadFull(d.random, code); err != nil {
		d.gate.returnNormalPermit(permit)
		return PairingWindowInfo{}, closedStoreErr(storeErrEntropy)
	}

	d.mu.Lock()
	if !d.accepting {
		// A racing Suspend/Stop between the entry check and publication: release the
		// permit and reject closed instead of publishing a window on a suspended service.
		d.mu.Unlock()
		d.gate.returnNormalPermit(permit)
		return PairingWindowInfo{}, errSecurityNotReady
	}
	// Capture any active prior window so its cancellation is emitted after unlock.
	oldActive := d.window.state == pairWindowActive
	oldGen := d.window.generation
	oldTimer := d.window.timer
	d.generationCounter++
	gen := d.generationCounter
	expiresAt := d.clock.Now().Add(d.policy.WindowTTL).UTC()
	d.window = pairingWindow{
		state: pairWindowActive, generation: gen, expiresAt: expiresAt, attempts: 0,
	}
	copy(d.window.code[:], code) // set code on the NEW window struct
	expectedGen := gen
	d.window.timer = d.clock.AfterFunc(d.policy.WindowTTL, func() {
		d.expireIfGeneration(expectedGen)
	})
	codeStr := encodePairingCode(d.window.code[:])
	occurredAt := d.clock.Now().UTC()
	d.mu.Unlock()
	if oldTimer != nil {
		oldTimer.Stop()
	}
	d.gate.returnNormalPermit(permit) // publication complete; release before events

	// Emit lifecycle events outside pairMu: cancel the replaced window first.
	if oldActive {
		d.appendPairingEvent(PairingWindowCanceled, oldGen, 0, occurredAt)
	}
	d.appendPairingEvent(PairingWindowOpened, gen, 0, occurredAt)

	return PairingWindowInfo{
		Generation: gen, Code: codeStr, ExpiresAt: expiresAt.Format(time.RFC3339Nano),
		BaseURL: baseURL,
	}, nil
}

// WindowStatus returns the non-code status.
// WindowActive reports whether a pairing window is currently active (read-only
// state API for maintenance preconditions).
func (d *deviceService) WindowActive() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.window.state == pairWindowActive
}

func (d *deviceService) WindowStatus() (PairingWindowStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.window.state != pairWindowActive {
		return PairingWindowStatus{Active: false}, nil
	}
	remaining := uint8(0)
	if d.policy.MaxAttempts > d.window.attempts {
		remaining = d.policy.MaxAttempts - d.window.attempts
	}
	return PairingWindowStatus{
		Active:            true,
		Generation:        d.window.generation,
		ExpiresAt:         d.window.expiresAt.Format(time.RFC3339Nano),
		RemainingAttempts: remaining,
	}, nil
}

// CancelWindow CAS-cancels by expected generation.
func (d *deviceService) CancelWindow(expectedGeneration uint64) (bool, error) {
	d.mu.Lock()
	if d.window.state != pairWindowActive || d.window.generation != expectedGeneration {
		d.mu.Unlock()
		return false, nil
	}
	d.window.state = pairWindowCanceled
	zeroBytes(d.window.code[:])
	if d.window.timer != nil {
		d.window.timer.Stop()
		d.window.timer = nil
	}
	occurredAt := d.clock.Now().UTC()
	d.mu.Unlock()
	d.appendPairingEvent(PairingWindowCanceled, expectedGeneration, 0, occurredAt)
	return true, nil
}

func (d *deviceService) expireIfGeneration(expectedGeneration uint64) {
	d.mu.Lock()
	if d.window.state != pairWindowActive || d.window.generation != expectedGeneration {
		d.mu.Unlock()
		return
	}
	d.window.state = pairWindowExpired
	zeroBytes(d.window.code[:])
	d.window.timer = nil
	occurredAt := d.clock.Now().UTC()
	d.mu.Unlock()
	d.appendPairingEvent(PairingWindowExpired, expectedGeneration, 0, occurredAt)
}

// CompletePairing redeems a code. The handler has already performed Origin/body/
// HostSummary preflight; this method owns the code compare, candidate generation,
// response precompute and the store three-state. Concurrent correct redeemers
// serialize on pairMu; exactly one commits.
func (d *deviceService) CompletePairing(code string, deviceName string, validatedHost contract.HostSummary) (pairingGrant, error) {
	codeBytes, formatErr := decodePairingCode(code)
	defer zeroBytes(codeBytes) // wipe decoded code on every path
	name, nameErr := canonicalDeviceName(deviceName)

	d.mu.Lock()
	if d.window.state != pairWindowActive {
		d.mu.Unlock()
		return pairingGrant{}, errWindowNotActive
	}
	if formatErr != nil || nameErr != nil {
		// Format errors do not consume an attempt (avoid cheap format DoS).
		d.mu.Unlock()
		return pairingGrant{}, errBadCodeFormat
	}
	if subtle.ConstantTimeCompare(d.window.code[:], codeBytes) != 1 {
		d.window.attempts++
		attempt := d.window.attempts
		gen := d.window.generation
		occurredAt := d.clock.Now().UTC()
		locked := false
		if d.window.attempts >= d.policy.MaxAttempts {
			d.window.state = pairWindowLocked
			zeroBytes(d.window.code[:])
			if d.window.timer != nil {
				d.window.timer.Stop()
				d.window.timer = nil
			}
			locked = true
		}
		d.mu.Unlock()
		// Emit attempt_rejected (attempt 1..max) outside pairMu; final wrong also
		// emits locked after rejected.
		d.appendPairingEvent(PairingAttemptRejected, gen, attempt, occurredAt)
		if locked {
			d.appendPairingEvent(PairingWindowLocked, gen, attempt, occurredAt)
			return pairingGrant{}, errAttemptsExhausted
		}
		return pairingGrant{}, errWrongCode
	}

	// Correct code: issue a normal permit spanning store read (knownIDs) → Create
	// so maintenance cannot interleave between collision check and commit.
	permit, ok := d.gate.issueNormalPermit()
	if !ok {
		d.mu.Unlock()
		return pairingGrant{}, errSecurityNotReady
	}
	known := d.store.knownIDs()
	id, err := generateUniqueDeviceID(d.random, func(cid contract.DeviceID) bool { return known[cid] })
	if err != nil {
		d.gate.returnNormalPermit(permit)
		d.mu.Unlock()
		return pairingGrant{}, closedStoreErr(storeErrEntropy)
	}
	secret, err := generateDeviceSecret(d.random)
	salt, saltErr := generateDeviceSalt(d.random)
	if err != nil || saltErr != nil {
		if secret != nil {
			zeroBytes(secret)
		}
		if salt != nil {
			zeroBytes(salt)
		}
		d.gate.returnNormalPermit(permit)
		d.mu.Unlock()
		return pairingGrant{}, closedStoreErr(storeErrEntropy)
	}
	// defer-owner: wipe secret/salt on ANY failure; on success, ownership transfers
	// (secret -> grant, handler zeros after Cookie; salt -> persisted record).
	secretOwned := false
	saltOwned := false
	defer func() {
		if !secretOwned && secret != nil {
			zeroBytes(secret)
		}
		if !saltOwned && salt != nil {
			zeroBytes(salt)
		}
	}()
	now := d.clock.Now().UTC()
	expiresAt := now.Add(d.policy.CredentialTTL)
	rec := deviceRecord{
		ID: id, Name: name, CredentialSalt: salt,
		CredentialHash: computeDeviceDigest(salt, secret),
		PairedAt:       now, LastSeenAt: now, CredentialExpiresAt: expiresAt,
	}
	// Precompute + validate the response BEFORE the store commit.
	respBytes, respErr := precomputePairingResponse(rec, validatedHost)
	if respErr != nil {
		d.gate.returnNormalPermit(permit)
		d.mu.Unlock()
		return pairingGrant{}, errSecurityNotReady
	}
	// Store commit (pairMu → storeMu is the only business nesting).
	mutation, mErr := d.store.Create(permit, rec)
	d.gate.returnNormalPermit(permit)
	if mErr != nil {
		if isCapacityErr(mErr) {
			d.mu.Unlock()
			return pairingGrant{}, mErr
		}
		d.mu.Unlock()
		return pairingGrant{}, errSecurityNotReady
	}
	switch mutation.State {
	case StoreNotCommitted:
		// Window stays active; no attempt consumed.
		d.mu.Unlock()
		return pairingGrant{}, errStoreNotCommitted
	case StoreIndeterminateFailClosed:
		// Consume + wipe + global latch.
		d.consumeLocked()
		d.mu.Unlock()
		d.latchSecurity()
		return pairingGrant{}, errSecurityNotReady
	}
	// Committed: consume + wipe BEFORE releasing locks.
	d.consumeLocked()
	// Ownership transfer: secret → grant (handler zeros after Cookie); salt →
	// the durably-persisted record. Prevent the defer from wiping them.
	secretOwned = true
	saltOwned = true
	grant := pairingGrant{
		deviceID:     string(id),
		device:       deviceRecordToInfo(rec),
		secret:       secret,
		expiresAt:    expiresAt,
		responseBody: respBytes,
		mutation:     mutation,
	}
	d.mu.Unlock()

	// Append the paired event AFTER releasing security locks.
	eventOutcome := d.appendDeviceEvent(DevicePaired, id, now, 0)
	grant.eventOutcome = eventOutcome
	return grant, nil
}

// RecordDeviceSeen updates LastSeenAt using the principal's frozen
// AuthenticatedAt (never a later clock sample).
func (d *deviceService) RecordDeviceSeen(principal DevicePrincipal) (DeviceSeenResult, error) {
	permit, ok := d.gate.issueNormalPermit()
	if !ok {
		return DeviceSeenResult{Outcome: SeenIndeterminateFailClosed}, errSecurityNotReady
	}
	obs := deviceSeenObservation{
		DeviceID: principal.DeviceID, AuthenticatedAt: principal.AuthenticatedAt,
		CredentialExpiresAt: principal.CredentialExpiresAt,
	}
	_, disp, mutation, err := d.store.TouchSeen(permit, obs, deviceSeenPersistInterval)
	d.gate.returnNormalPermit(permit)
	if err != nil {
		if isCapacityErr(err) {
			return DeviceSeenResult{Outcome: SeenCapacityUnavailable}, nil
		}
		return DeviceSeenResult{Outcome: SeenIndeterminateFailClosed}, err
	}
	// No-write dispositions never latch.
	switch disp {
	case seenCoalesced:
		return DeviceSeenResult{Outcome: SeenCoalesced}, nil
	case seenSkippedRevoked:
		return DeviceSeenResult{Outcome: SeenSkippedRevoked}, nil
	case seenSkippedExpired:
		return DeviceSeenResult{Outcome: SeenSkippedExpired}, nil
	}
	// disp == seenPersist: the mutation three-state decides.
	switch mutation.State {
	case StoreCommitted:
		if mutation.DurabilityDegraded {
			d.health.Record(HealthStoreDurabilityDegraded, "", d.clock.Now())
		}
		return DeviceSeenResult{Outcome: seenOutcome(disp), DurabilityDegraded: mutation.DurabilityDegraded}, nil
	case StoreNotCommitted:
		d.health.Record(HealthDeviceSeenPersistFailed, "", d.clock.Now())
		return DeviceSeenResult{Outcome: SeenNotCommitted}, nil
	default:
		d.latchSecurity()
		return DeviceSeenResult{Outcome: SeenIndeterminateFailClosed}, nil
	}
}

// ListDevices returns the desktop device list.
func (d *deviceService) ListDevices() ([]DeviceInfo, error) {
	permit, ok := d.gate.issueNormalPermit()
	if !ok {
		return nil, errSecurityNotReady
	}
	records, err := d.store.List(permit)
	d.gate.returnNormalPermit(permit)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceInfo, 0, len(records))
	for _, rec := range records {
		out = append(out, deviceRecordToInfo(rec))
	}
	return out, nil
}

// SetControlLifecycleHook injects the H2 control lifecycle hook (design §4A.3).
// Called by the Server before Start. When not set, a no-op hook is used.
func (d *deviceService) SetControlLifecycleHook(hook ControlLifecycleHook) {
	if hook == nil {
		hook = noopLifecycleHook{}
	}
	d.controlHook = hook
}

// RevokeDevice persists the revoke (ledger authority) → fences/detaches the
// device's connections → terminates them OUTSIDE the registry lock → appends the
// event. Duplicate revoke does not append a second event but still fences.
func (d *deviceService) RevokeDevice(deviceID contract.DeviceID) (RevokeDeviceResult, error) {
	permit, ok := d.gate.issueNormalPermit()
	if !ok {
		return RevokeDeviceResult{}, errSecurityNotReady
	}
	at := d.clock.Now().UTC()
	rr, err := d.store.Revoke(permit, deviceID, at)
	d.gate.returnNormalPermit(permit)
	if err != nil {
		if rr.Mutation.State == StoreIndeterminateFailClosed {
			d.latchSecurity()
		}
		return RevokeDeviceResult{Device: deviceRecordToInfo(rr.Device)}, err
	}
	if rr.Mutation.State == StoreIndeterminateFailClosed {
		d.latchSecurity()
	}

	// H2/§4A.3 authority order: committed-or-existing tombstone → Mark →
	// registry FenceDevice → Terminate → Release notice → existing security event.
	// MarkDeviceRevoked is the global no-new-admission fence; it is called AFTER
	// the ledger commit and BEFORE registry FenceDevice so no new device launch/
	// lifecycle intent can succeed while connections are being fenced.
	d.controlHook.MarkDeviceRevoked(deviceID)

	// Fence + detach under the registry lock; terminate OUTSIDE it.
	detached := d.registry.FenceDevice(deviceID, at)
	for _, c := range detached {
		c.Terminate(ConnectionTermination{Cause: TerminationDeviceRevoked, OccurredAt: at})
	}

	// H2/§4A.3: ReleaseRevokedDevice clears the revoked device's control holders
	// AFTER registry Terminate (design §4A.3 ordering: Mark→Fence→Terminate→
	// Release→event).
	d.controlHook.ReleaseRevokedDevice(DeviceRevocationNotice{
		DeviceID:   deviceID,
		OccurredAt: at,
	})

	outcome := EventAccepted
	if rr.AlreadyRevoked {
		outcome = EventNotEmittedDuplicate
	} else if rr.Mutation.State == StoreCommitted {
		outcome = d.appendDeviceEvent(DeviceRevoked, deviceID, at, rr.LedgerSequence)
	}
	return RevokeDeviceResult{
		Device:                          deviceRecordToInfo(rr.Device),
		AlreadyRevoked:                  rr.AlreadyRevoked,
		TerminationRequestedConnections: len(detached),
		EventOutcome:                    outcome,
		DurabilityDegraded:              rr.Mutation.DurabilityDegraded,
	}, nil
}

// consumeLocked marks the window consumed and wipes the code. Caller holds pairMu.
func (d *deviceService) consumeLocked() {
	d.window.state = pairWindowConsumed
	zeroBytes(d.window.code[:])
	if d.window.timer != nil {
		d.window.timer.Stop()
		d.window.timer = nil
	}
}

// latchSecurity latches the whole security face unavailable.
func (d *deviceService) latchSecurity() {
	d.store.latchReady()
	// H2/§4A.3 authority order: authoritative store latch → Fence(security) →
	// registry Stop → Terminate → Release. FenceAllRemote is called AFTER the
	// store latch and BEFORE registry Stop so no new device launch/write can
	// succeed while connections are being stopped (design §4A.3).
	d.controlHook.FenceAllRemote(ControlCauseSecurityUnavailable, d.clock.Now())
	detached := d.registry.Stop(d.clock.Now())
	for _, c := range detached {
		c.Terminate(ConnectionTermination{Cause: TerminationSecurityStateUnavailable, OccurredAt: d.clock.Now()})
	}
	// H2/§4A.3: ReleaseAllRemote clears device holders with the security reason.
	d.controlHook.ReleaseAllRemote(ControlCauseSecurityUnavailable, d.clock.Now())
	d.health.Record(HealthStoreIndeterminate, "", d.clock.Now())
}

// appendDeviceEvent derives a stable EventID, appends to the sink and records
// health. Event failure never rolls back the security action.
func (d *deviceService) appendDeviceEvent(kind DeviceSecurityEventKind, deviceID contract.DeviceID, occurredAt time.Time, ledgerSeq uint64) SecurityEventDeliveryOutcome {
	scope := d.store.storeID
	fields := deviceEventHashInput(deviceID, occurredAt, kind, ledgerSeq)
	eid := deriveSecurityEventID(scope, fields)
	ev := DeviceSecurityEvent{EventID: eid, Kind: kind, OccurredAt: occurredAt, DeviceID: deviceID}
	res, _ := d.sink.AppendSecurityEvent(ev)
	switch res.State {
	case EventAcceptedBySink:
		return EventAccepted
	case EventDuplicateAcceptedBySink:
		return EventDuplicateAccepted
	case EventAcceptedButDurabilityDegraded:
		d.health.Record(HealthEventDurabilityDegraded, eid, occurredAt)
		return EventAcceptedDurabilityDegraded
	default:
		// PreAccept: map closed failure category to the right sticky health code.
		// integrity → its own sticky code; capacity/io/invalid/unavailable → append-failed.
		code := HealthEventAppendFailed
		if res.Failure == EventFailureIntegrity {
			code = HealthEventIntegrityFailed
		}
		d.health.Record(code, eid, occurredAt)
		return EventFailed
	}
}

// appendPairingEvent derives a stable EventID (processScope+kind+generation+
// attempt+occurredAt) and appends a pairing event to the sink OUTSIDE all
// pairMu/store/registry locks. Failure only records closed health; it never
// rolls back the transition.
func (d *deviceService) appendPairingEvent(kind PairingSecurityEventKind, generation uint64, attempt uint8, occurredAt time.Time) {
	if d.sink == nil {
		return
	}
	eid := derivePairingEventID(d.processScopeID, kind, generation, attempt, occurredAt)
	res, _ := d.sink.AppendSecurityEvent(PairingSecurityEvent{
		EventID: eid, Kind: kind, OccurredAt: occurredAt, Generation: generation, Attempt: attempt,
	})
	recordEventAppendHealth(d.health, res, eid, occurredAt)
}

// pairing fixed errors (closed text).
var (
	errWindowNotActive   = closedTextError("pairing window is not active")
	errBadCodeFormat     = closedTextError("invalid pairing request")
	errWrongCode         = closedTextError("pairing code rejected")
	errAttemptsExhausted = closedTextError("pairing attempts exhausted")
	errStoreNotCommitted = closedTextError("security state unavailable")
)

type closedTextError string

func (e closedTextError) Error() string { return string(e) }

func isCapacityErr(err error) bool {
	_, ok := err.(SecurityCapacityError)
	return ok
}

func seenOutcome(d seenStoreDisposition) DeviceSeenOutcome {
	switch d {
	case seenPersist:
		return SeenPersisted
	case seenCoalesced:
		return SeenCoalesced
	case seenSkippedRevoked:
		return SeenSkippedRevoked
	case seenSkippedExpired:
		return SeenSkippedExpired
	}
	return SeenPersisted
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// deviceSeenPersistInterval is the LastSeen coalescing interval (design §9.5).
const deviceSeenPersistInterval = 10 * time.Minute

// deviceRecordToInfo maps an internal record to the desktop DTO.
func deviceRecordToInfo(rec deviceRecord) DeviceInfo {
	info := DeviceInfo{
		ID:                  rec.ID,
		Name:                rec.Name,
		PairedAt:            rec.PairedAt.UTC().Format(time.RFC3339Nano),
		LastSeenAt:          rec.LastSeenAt.UTC().Format(time.RFC3339Nano),
		CredentialExpiresAt: rec.CredentialExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	if rec.RevokedAt != nil {
		s := rec.RevokedAt.UTC().Format(time.RFC3339Nano)
		info.RevokedAt = &s
	}
	return info
}

// deviceEventHashInput builds the canonical EventID input fields (excluding
// EventID) for a device event.
func deviceEventHashInput(deviceID contract.DeviceID, at time.Time, kind DeviceSecurityEventKind, ledgerSeq uint64) []byte {
	return []byte(deviceKindString(kind) + "|" + string(deviceID) + "|" + at.UTC().Format(time.RFC3339Nano) + "|" + uitoa(ledgerSeq))
}

func uitoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// encodePairingCode renders 16 bytes as uppercase Base32 no-padding (26 chars).
func encodePairingCode(b []byte) string {
	return base32NoPad.EncodeToString(b)
}

// decodePairingCode normalizes (trim outer whitespace, strip ASCII '-',
// uppercase) then strictly validates [A-Z2-7]{26} and decodes 16 bytes.
func decodePairingCode(s string) ([]byte, error) {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", ""))
	if len(s) != 26 {
		return nil, errBadCodeFormat
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= '2' && c <= '7')) {
			return nil, errBadCodeFormat
		}
	}
	b, err := base32NoPad.DecodeString(s)
	if err != nil || len(b) != 16 {
		return nil, errBadCodeFormat
	}
	return b, nil
}

// canonicalDeviceName validates + canonicalizes a device name (design §8.4).
func canonicalDeviceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errBadCodeFormat
	}
	if len(name) > 256 {
		return "", errBadCodeFormat
	}
	count := 0
	for _, r := range name {
		count++
		if r == 0 || r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return "", errBadCodeFormat // C0/C1 controls + NUL
		}
		switch r {
		case 0x2028, 0x2029, 0x200E, 0x200F, // bidi marks
			0x202A, 0x202B, 0x202C, 0x202D, 0x202E, // bidi overrides
			0x2066, 0x2067, 0x2068, 0x2069, // bidi isolates
			0xFEFF: // BOM
			return "", errBadCodeFormat
		}
	}
	if count > 64 {
		return "", errBadCodeFormat
	}
	return name, nil
}

// precomputePairingResponse validates + marshals the success body BEFORE the
// store commit.
func precomputePairingResponse(rec deviceRecord, host contract.HostSummary) ([]byte, error) {
	resp := contract.PairingCompleteResponse{
		Device: contract.DeviceSummary{
			ID:       rec.ID,
			Name:     rec.Name,
			PairedAt: rec.PairedAt.UTC().Format(time.RFC3339Nano),
		},
		Host: host,
	}
	return contract.MarshalRESTResponse(resp)
}
