package remote

// Volatile in-memory security event sink (design §13.2). Caps: 256 entries,
// 1KiB per canonical event, 256KiB aggregate. Append-only; no drop-oldest.
// Duplicate-before-cap: an already-accepted same-ID/same-payload event returns
// DuplicateAccepted even when the buffer is full. A same-ID/different-payload
// event is an integrity failure (PreAcceptFailed). The volatile sink NEVER
// returns AcceptedButDurabilityDegraded and never retains payload on
// PreAcceptFailed. M1-A has no retry owner for PreAcceptFailed events.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// MaxVolatileSecurityEvents bounds the number of accepted events.
	MaxVolatileSecurityEvents = 256
	// MaxSecurityEventCanonicalBytes bounds a single canonical event line.
	MaxSecurityEventCanonicalBytes = 1 << 10
	// MaxVolatileSecurityEventBytes bounds the aggregate accepted payload.
	MaxVolatileSecurityEventBytes = 256 << 10
)

// acceptedVolatileEvent is one accepted buffer entry. It stores the canonical
// bytes (for integrity re-check) and the accept time.
type acceptedVolatileEvent struct {
	id         SecurityEventID
	canonical  []byte
	acceptedAt time.Time
}

// VolatileSecurityEventSink is the M1-A bounded in-memory sink (used by tests;
// M1-B production uses the durable sink).
type VolatileSecurityEventSink struct {
	mu        sync.Mutex
	events    []acceptedVolatileEvent
	byID      map[SecurityEventID]int
	usedBytes int
}

// NewVolatileSecurityEventSink returns a ready volatile sink.
func NewVolatileSecurityEventSink() *VolatileSecurityEventSink {
	return &VolatileSecurityEventSink{
		events: make([]acceptedVolatileEvent, 0, MaxVolatileSecurityEvents),
		byID:   make(map[SecurityEventID]int, MaxVolatileSecurityEvents),
	}
}

// Durability reports EventSinkVolatile.
func (s *VolatileSecurityEventSink) Durability() EventSinkDurability { return EventSinkVolatile }

// AppendSecurityEvent implements the closed append protocol.
func (s *VolatileSecurityEventSink) AppendSecurityEvent(e SecurityEvent) (EventAppendResult, error) {
	canonical, err := canonicalizeSecurityEvent(e)
	if err != nil {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureInvalid}, errPreAcceptFailed
	}
	if len(canonical) > MaxSecurityEventCanonicalBytes {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureInvalid}, errPreAcceptFailed
	}
	id := e.EventIDOf()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Duplicate-before-cap: an already-accepted same-ID entry is consulted
	// BEFORE any new-entry capacity check.
	if idx, ok := s.byID[id]; ok {
		if equalCanonical(s.events[idx].canonical, canonical) {
			return EventAppendResult{State: EventDuplicateAcceptedBySink}, nil
		}
		// Same ID, different canonical payload: integrity failure.
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureIntegrity}, errIntegrityFailed
	}

	// New event: entry + byte capacity gate. No drop-oldest; on failure the map
	// is NOT touched and no payload is retained.
	if len(s.events) >= MaxVolatileSecurityEvents {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureCapacity}, errPreAcceptFailed
	}
	if s.usedBytes+len(canonical) > MaxVolatileSecurityEventBytes {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureCapacity}, errPreAcceptFailed
	}

	cp := make([]byte, len(canonical))
	copy(cp, canonical)
	s.events = append(s.events, acceptedVolatileEvent{
		id:         id,
		canonical:  cp,
		acceptedAt: e.OccurredAtOf(),
	})
	s.byID[id] = len(s.events) - 1
	s.usedBytes += len(cp)
	return EventAppendResult{State: EventAcceptedBySink}, nil
}

// AcceptedCount returns the number of accepted entries (test/diagnostic only).
func (s *VolatileSecurityEventSink) AcceptedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// UsedBytes returns the aggregate accepted canonical bytes (test/diagnostic).
func (s *VolatileSecurityEventSink) UsedBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usedBytes
}

var (
	errPreAcceptFailed = fmt.Errorf("security event pre-accept failed")
	errIntegrityFailed = fmt.Errorf("security event integrity failed")
)

func equalCanonical(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Canonical event serialization (design §13.1/§13.5 wire allowlist)
// ---------------------------------------------------------------------------

// canonicalPairingWire is the closed pairing-event wire shape. Field order is
// fixed so remarshal is byte-equal.
type canonicalPairingWire struct {
	Version           int    `json:"version"`
	EventID           string `json:"eventId"`
	Kind              string `json:"kind"`
	OccurredAt        string `json:"occurredAt"`
	PairingGeneration uint64 `json:"pairingGeneration"`
	Attempt           uint8  `json:"attempt"`
}

type canonicalDeviceWire struct {
	Version    int    `json:"version"`
	EventID    string `json:"eventId"`
	Kind       string `json:"kind"`
	OccurredAt string `json:"occurredAt"`
	DeviceID   string `json:"deviceId"`
}

type canonicalStoreWire struct {
	Version    int    `json:"version"`
	EventID    string `json:"eventId"`
	Kind       string `json:"kind"`
	OccurredAt string `json:"occurredAt"`
}

// canonicalServiceWire is the closed internal service-event wire shape:
// {version,eventId,kind,occurredAt} only (no host/port/token/path).
type canonicalServiceWire struct {
	Version    int    `json:"version"`
	EventID    string `json:"eventId"`
	Kind       string `json:"kind"`
	OccurredAt string `json:"occurredAt"`
}

// canonicalLegacyAuthWire is the closed legacy-deprecation wire shape:
// {version,eventId,kind,occurredAt,carrier,routeClass,outcome}.
type canonicalLegacyAuthWire struct {
	Version    int    `json:"version"`
	EventID    string `json:"eventId"`
	Kind       string `json:"kind"`
	OccurredAt string `json:"occurredAt"`
	Carrier    string `json:"carrier"`
	RouteClass string `json:"routeClass"`
	Outcome    string `json:"outcome"`
}

func pairingKindString(k PairingSecurityEventKind) string {
	switch k {
	case PairingWindowOpened:
		return "pairing_window_opened"
	case PairingWindowCanceled:
		return "pairing_window_canceled"
	case PairingWindowExpired:
		return "pairing_window_expired"
	case PairingAttemptRejected:
		return "pairing_attempt_rejected"
	case PairingWindowLocked:
		return "pairing_window_locked"
	default:
		return "pairing_unknown"
	}
}

func deviceKindString(k DeviceSecurityEventKind) string {
	switch k {
	case DevicePaired:
		return "device_paired"
	case DeviceRevoked:
		return "device_revoked"
	default:
		return "device_unknown"
	}
}

func storeKindString(k StoreSecurityEventKind) string {
	switch k {
	case StoreDurabilityDegraded:
		return "store_durability_degraded"
	default:
		return "store_unknown"
	}
}

// serviceKindString renders the closed internal service event kinds.
func serviceKindString(k ServiceSecurityEventKind) string {
	switch k {
	case RemoteServiceStarted:
		return "remote_service_started"
	case RemoteServiceStopped:
		return "remote_service_stopped"
	case LegacyTokenRotated:
		return "legacy_token_rotated"
	case RemoteListenConfigurationChanged:
		return "remote_listen_configuration_changed"
	default:
		return "service_unknown"
	}
}

// legacyCarrierString / legacyRouteClassString / legacyOutcomeString render the
// closed legacy-deprecation enums.
func legacyCarrierString(c LegacyAuthCarrier) string {
	switch c {
	case CarrierBearer:
		return "bearer"
	case CarrierQueryToken:
		return "query_token"
	case CarrierLocalSession:
		return "local_session"
	case CarrierLaunchGrant:
		return "launch_grant"
	}
	return "carrier_unknown"
}

func legacyRouteClassString(r LegacyAuthRouteClass) string {
	switch r {
	case RouteBootstrap:
		return "bootstrap"
	case RouteAPIRead:
		return "api_read"
	case RouteAPIWrite:
		return "api_write"
	case RouteWebSocket:
		return "websocket"
	}
	return "route_unknown"
}

func legacyOutcomeString(o LegacyAuthOutcome) string {
	if o == LegacyAuthAccepted {
		return "accepted"
	}
	return "outcome_unknown"
}

// validateSecurityEvent enforces the closed canonical invariants shared by the
// volatile and durable sinks (and re-applied by the durable parser on restart):
// canonical EventID, known kind, non-zero OccurredAt, and per-kind field
// constraints (pair generation>0; opened/canceled/expired attempt==0;
// rejected/locked attempt>=1; valid deviceId; service exact-4 kinds).
var (
	errSecEventInvalidID    = errors.New("security event: invalid eventId")
	errSecEventUnknownKind  = errors.New("security event: unknown kind")
	errSecEventZeroTime     = errors.New("security event: zero occurredAt")
	errSecEventGenZero      = errors.New("security event: pairing generation must be > 0")
	errSecEventAttemptRange = errors.New("security event: attempt out of range for kind")
	errSecEventInvalidDev   = errors.New("security event: invalid deviceId")
)

func validateSecurityEvent(e SecurityEvent) error {
	switch ev := e.(type) {
	case PairingSecurityEvent:
		if !validDurableEventID(string(ev.EventID)) {
			return errSecEventInvalidID
		}
		if pairingKindString(ev.Kind) == "pairing_unknown" {
			return errSecEventUnknownKind
		}
		if ev.OccurredAt.IsZero() {
			return errSecEventZeroTime
		}
		if ev.Generation == 0 {
			return errSecEventGenZero
		}
		switch ev.Kind {
		case PairingWindowOpened, PairingWindowCanceled, PairingWindowExpired:
			if ev.Attempt != 0 {
				return errSecEventAttemptRange
			}
		case PairingAttemptRejected, PairingWindowLocked:
			maxAtt := DefaultPairingPolicy().MaxAttempts
			if ev.Attempt == 0 || ev.Attempt > maxAtt {
				return errSecEventAttemptRange
			}
		}
	case DeviceSecurityEvent:
		if !validDurableEventID(string(ev.EventID)) {
			return errSecEventInvalidID
		}
		if deviceKindString(ev.Kind) == "device_unknown" {
			return errSecEventUnknownKind
		}
		if ev.OccurredAt.IsZero() {
			return errSecEventZeroTime
		}
		if !validRawURLID(string(ev.DeviceID)) {
			return errSecEventInvalidDev
		}
	case StoreSecurityEvent:
		if !validDurableEventID(string(ev.EventID)) {
			return errSecEventInvalidID
		}
		if storeKindString(ev.Kind) == "store_unknown" {
			return errSecEventUnknownKind
		}
		if ev.OccurredAt.IsZero() {
			return errSecEventZeroTime
		}
	case ServiceSecurityEvent:
		if !validDurableEventID(string(ev.EventID)) {
			return errSecEventInvalidID
		}
		if serviceKindString(ev.Kind) == "service_unknown" {
			return errSecEventUnknownKind
		}
		if ev.OccurredAt.IsZero() {
			return errSecEventZeroTime
		}
	case LegacyAuthSecurityEvent:
		if !validDurableEventID(string(ev.EventID)) {
			return errSecEventInvalidID
		}
		if legacyCarrierString(ev.Carrier) == "carrier_unknown" ||
			legacyRouteClassString(ev.RouteClass) == "route_unknown" ||
			legacyOutcomeString(ev.Outcome) == "outcome_unknown" {
			return errSecEventUnknownKind
		}
		if ev.OccurredAt.IsZero() {
			return errSecEventZeroTime
		}
	default:
		return fmt.Errorf("security event: unknown event type %T", e)
	}
	return nil
}

// canonicalizeSecurityEvent produces the deterministic wire bytes for one event.
// It validates that no disallowed material is present and that timestamps are
// representable. The bytes are used for the 1KiB size bound and same-ID
// integrity comparison.
func canonicalizeSecurityEvent(e SecurityEvent) ([]byte, error) {
	if err := validateSecurityEvent(e); err != nil {
		return nil, err
	}
	const version = 1
	switch ev := e.(type) {
	case PairingSecurityEvent:
		if ev.EventID == "" {
			return nil, fmt.Errorf("security event: missing eventId")
		}
		w := canonicalPairingWire{
			Version:           version,
			EventID:           string(ev.EventID),
			Kind:              pairingKindString(ev.Kind),
			OccurredAt:        canonicalTime(ev.OccurredAt),
			PairingGeneration: ev.Generation,
			Attempt:           ev.Attempt,
		}
		return json.Marshal(w)
	case DeviceSecurityEvent:
		if ev.EventID == "" {
			return nil, fmt.Errorf("security event: missing eventId")
		}
		if ev.DeviceID == "" {
			return nil, fmt.Errorf("security event: missing deviceId")
		}
		w := canonicalDeviceWire{
			Version:    version,
			EventID:    string(ev.EventID),
			Kind:       deviceKindString(ev.Kind),
			OccurredAt: canonicalTime(ev.OccurredAt),
			DeviceID:   string(ev.DeviceID),
		}
		return json.Marshal(w)
	case StoreSecurityEvent:
		if ev.EventID == "" {
			return nil, fmt.Errorf("security event: missing eventId")
		}
		w := canonicalStoreWire{
			Version:    version,
			EventID:    string(ev.EventID),
			Kind:       storeKindString(ev.Kind),
			OccurredAt: canonicalTime(ev.OccurredAt),
		}
		return json.Marshal(w)
	case ServiceSecurityEvent:
		if ev.EventID == "" {
			return nil, fmt.Errorf("security event: missing eventId")
		}
		sw := canonicalServiceWire{
			Version:    version,
			EventID:    string(ev.EventID),
			Kind:       serviceKindString(ev.Kind),
			OccurredAt: canonicalTime(ev.OccurredAt),
		}
		return json.Marshal(sw)
	case LegacyAuthSecurityEvent:
		if ev.EventID == "" {
			return nil, fmt.Errorf("security event: missing eventId")
		}
		lw := canonicalLegacyAuthWire{
			Version:    version,
			EventID:    string(ev.EventID),
			Kind:       "legacy_auth_deprecated",
			OccurredAt: canonicalTime(ev.OccurredAt),
			Carrier:    legacyCarrierString(ev.Carrier),
			RouteClass: legacyRouteClassString(ev.RouteClass),
			Outcome:    legacyOutcomeString(ev.Outcome),
		}
		return json.Marshal(lw)
	default:
		return nil, fmt.Errorf("security event: unknown event type %T", e)
	}
}

// canonicalTime renders a UTC RFC3339Nano 'Z' timestamp. Zero time is rejected
// upstream (events carry a real OccurredAt captured at the state/ledger commit).
func canonicalTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// ---------------------------------------------------------------------------
// EventID derivation (design §13.1)
// ---------------------------------------------------------------------------

// deviceCredentialDomain is the domain separator for device-credential digests.
const deviceCredentialDomain = "amagi-codebox/device-credential/v1"

// securityEventDomain is the domain separator for security-event EventIDs.
const securityEventDomain = "amagi-codebox/security-event/v1"

// deriveSecurityEventID computes the stable EventID =
// RawURLBase64(SHA-256(domain || 0x00 || canonical event fields excluding
// EventID)). It is called once per event, at the latest at the state/ledger
// commit, before the first sink call.
func deriveSecurityEventID(scopeID string, canonicalFieldsExcludingEventID []byte) SecurityEventID {
	h := newEventHash()
	h.Write([]byte(securityEventDomain))
	h.Write([]byte{0x00})
	h.Write([]byte(scopeID))
	h.Write([]byte{0x00})
	h.Write(canonicalFieldsExcludingEventID)
	return SecurityEventID(rawURLBase64(h.Sum(nil)))
}

// deriveServiceEventID computes a stable EventID for an internal service event
// from processScope + kind + occurredAt + a monotonic server-event sequence.
// The sequence is the only varying hash input (no payload/host/port/token).
func deriveServiceEventID(scopeID string, kind ServiceSecurityEventKind, occurredAt time.Time, seq uint64) SecurityEventID {
	fields := []byte(serviceKindString(kind) + "|" + canonicalTime(occurredAt) + "|" + uitoa(seq))
	return deriveSecurityEventID(scopeID, fields)
}

// derivePairingEventID computes a stable EventID for a pairing event from
// processScope + exact kind + generation + attempt + occurredAt (no code/name/IP).
func derivePairingEventID(scopeID string, kind PairingSecurityEventKind, generation uint64, attempt uint8, occurredAt time.Time) SecurityEventID {
	fields := []byte(pairingKindString(kind) + "|" + uitoa(generation) + "|" + uitoa(uint64(attempt)) + "|" + canonicalTime(occurredAt))
	return deriveSecurityEventID(scopeID, fields)
}

// deriveLegacyAuthEventID computes a stable per-process EventID for a legacy
// deprecation event from serverEventScope + carrier + routeClass + outcome.
// occurredAt is deliberately NOT included (the tuple dedupes within a process).
func deriveLegacyAuthEventID(scopeID string, carrier LegacyAuthCarrier, routeClass LegacyAuthRouteClass, outcome LegacyAuthOutcome) SecurityEventID {
	fields := []byte(legacyCarrierString(carrier) + "|" + legacyRouteClassString(routeClass) + "|" + legacyOutcomeString(outcome))
	return deriveSecurityEventID(scopeID, fields)
}

// recordEventAppendHealth is the generic append-result→health mapper: a degraded
// append records event-durability-degraded; a PreAccept integrity failure
// records event-integrity-failed; any other PreAccept records event-append-
// failed. Accepted/Duplicate record nothing. Nil-safe.
func recordEventAppendHealth(h *securityHealthRegister, res EventAppendResult, eid SecurityEventID, at time.Time) {
	if h == nil {
		return
	}
	switch res.State {
	case EventAcceptedButDurabilityDegraded:
		h.Record(HealthEventDurabilityDegraded, eid, at)
	case EventPreAcceptFailed:
		code := HealthEventAppendFailed
		if res.Failure == EventFailureIntegrity {
			code = HealthEventIntegrityFailed
		}
		h.Record(code, eid, at)
	}
}
