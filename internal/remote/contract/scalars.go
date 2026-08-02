package contract

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Scalar aliases and enums for the v1 wire contract. All are opaque to clients:
// clients MUST NOT parse SessionID/DeviceID as UUID or by prefix. Time is
// carried as RFC3339 string in DTOs, not via these scalars.

// Opaque ID aliases.
type (
	// RequestID is a non-empty opaque request-correlation ID. REST travels in
	// the X-Request-ID header; WS travels as the top-level requestId field.
	RequestID string
	// SessionID is a non-empty opaque session ID.
	SessionID string
	// DeviceID is a non-empty opaque device ID (server-side only; never sent
	// to clients in v1 wire DTOs).
	DeviceID string
	// MessageID is input.id — the per-WS-connection idempotency key.
	MessageID string
)

// Seq is a per-session replay cursor. It is scoped to a single session
// lifetime (not a connection): reconnects and concurrent observers see the
// same seq for the same replay frame. 0 is the "no replay frame yet" sentinel;
// real replay frames start at 1 and increase monotonically. The same-entry
// restart boundary also occupies a seq.
type Seq uint64

// MaxSeqSafeInteger is the JavaScript safe-integer ceiling (2^53 - 1). Servers
// MUST guarantee seq <= MaxSeqSafeInteger before encoding to JSON so the value
// cannot be silently rounded in TS clients.
const MaxSeqSafeInteger Seq = 9007199254740991

// CLIType enumerates the four frozen remote-controlled CLI types.
type CLIType string

const (
	CLITypeClaudeCode CLIType = "claudecode"
	CLITypeOpenCode   CLIType = "opencode"
	CLITypeCodex      CLIType = "codex"
	CLITypePi         CLIType = "pi"
)

// KnownCLITypes is the complete set of four CLI types in canonical order.
var KnownCLITypes = []CLIType{
	CLITypeClaudeCode,
	CLITypeOpenCode,
	CLITypeCodex,
	CLITypePi,
}

// SessionState enumerates the five frozen session lifecycle states. `removed`
// is primarily used in events; GET does not return already-deleted resources.
type SessionState string

const (
	SessionStateRunning     SessionState = "running"
	SessionStateStopped     SessionState = "stopped"
	SessionStateExited      SessionState = "exited"
	SessionStateUnavailable SessionState = "unavailable"
	SessionStateRemoved     SessionState = "removed"
)

// KnownSessionStates is the complete set of five session states.
var KnownSessionStates = []SessionState{
	SessionStateRunning,
	SessionStateStopped,
	SessionStateExited,
	SessionStateUnavailable,
	SessionStateRemoved,
}

// ControlState enumerates the four control states relative to the current
// device.
type ControlState string

const (
	ControlStateNone    ControlState = "none"
	ControlStateYou     ControlState = "you"
	ControlStateOther   ControlState = "other"
	ControlStateDesktop ControlState = "desktop"
)

// KnownControlStates is the complete set of four control states.
var KnownControlStates = []ControlState{
	ControlStateNone,
	ControlStateYou,
	ControlStateOther,
	ControlStateDesktop,
}

// HistoryState enumerates the three history/window states.
type HistoryState string

const (
	HistoryStateContinuous HistoryState = "continuous"
	HistoryStateBackfilled HistoryState = "backfilled"
	HistoryStateGap        HistoryState = "gap"
)

// KnownHistoryStates is the complete set of three history states.
var KnownHistoryStates = []HistoryState{
	HistoryStateContinuous,
	HistoryStateBackfilled,
	HistoryStateGap,
}

// ConnectionState / AuthState are constrained at attach time: attached only
// emits connection=connected and auth=authorized (design §8.3). The client's
// connecting/reconnecting/disconnected are local channel states, not server
// facts. They are plain strings to keep the wire open within these constraints.
const (
	AttachedConnectionState = "connected"
	AttachedAuthState       = "authorized"
)

// ---------------------------------------------------------------------------
// Shared strict-decode primitives (addendum §5.2). These are pure functions
// with no business/network dependency. They are used by the production
// Decode* entry points in rest.go/errors.go/ws.go.
// ---------------------------------------------------------------------------

// strictFields parses raw as a single top-level JSON object and returns its
// fields as raw JSON messages. It rejects non-object input (null/array/
// scalar), syntax errors and any trailing JSON value.
func strictFields(raw []byte) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("contract: empty input")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil {
		return nil, fmt.Errorf("contract: expected JSON object: %w", err)
	}
	// After the first object, require EOF: a second Decode must hit io.EOF, not
	// a trailing `]`, a second JSON value or garbage. dec.More() alone misses
	// some trailing-token edge cases (round2 probe 4).
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("contract: unexpected trailing JSON value")
	}
	if fields == nil {
		return nil, errors.New("contract: expected JSON object, got null")
	}
	return fields, nil
}

func isJSONNull(v json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(v), []byte("null"))
}

// requireField returns the raw value for key, erroring if absent or null.
func requireField(f map[string]json.RawMessage, key string) (json.RawMessage, error) {
	v, ok := f[key]
	if !ok {
		return nil, fmt.Errorf("contract: missing required field %q", key)
	}
	if isJSONNull(v) {
		return nil, fmt.Errorf("contract: field %q must not be null", key)
	}
	return v, nil
}

// optField returns the raw value for key if present-and-non-null. It errors if
// the key is present but explicitly null (v1 has no nullable fields).
func optField(f map[string]json.RawMessage, key string) (json.RawMessage, bool, error) {
	v, ok := f[key]
	if !ok {
		return nil, false, nil
	}
	if isJSONNull(v) {
		return nil, false, fmt.Errorf("contract: optional field %q must not be null", key)
	}
	return v, true, nil
}

// rejectUnknown errors if fields contains any key not in allowed.
func rejectUnknown(f map[string]json.RawMessage, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		set[a] = true
	}
	for k := range f {
		if !set[k] {
			return fmt.Errorf("contract: unknown field %q", k)
		}
	}
	return nil
}

// reqField decodes a required, non-null field of type T.
func reqField[T any](f map[string]json.RawMessage, key string) (T, error) {
	var zero T
	v, err := requireField(f, key)
	if err != nil {
		return zero, err
	}
	var t T
	if err := json.Unmarshal(v, &t); err != nil {
		return zero, fmt.Errorf("contract: field %q: %w", key, err)
	}
	return t, nil
}

// reqNonEmptyString decodes a required non-null non-empty string field.
func reqNonEmptyString(f map[string]json.RawMessage, key string) (string, error) {
	s, err := reqField[string](f, key)
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", fmt.Errorf("contract: field %q must be non-empty", key)
	}
	return s, nil
}

// optFieldT decodes an optional non-null field of type T; ok is false when
// absent. It errors on explicit null.
func optFieldT[T any](f map[string]json.RawMessage, key string) (T, bool, error) {
	var zero T
	v, ok, err := optField(f, key)
	if err != nil || !ok {
		return zero, ok, err
	}
	var t T
	if err := json.Unmarshal(v, &t); err != nil {
		return zero, false, fmt.Errorf("contract: field %q: %w", key, err)
	}
	return t, true, nil
}

// ---------------------------------------------------------------------------
// Scalar validators (pure).
// ---------------------------------------------------------------------------

// validateSeqRange rejects seq above the JS safe-integer ceiling. Sentinel 0
// is allowed (callers that require >= 1 use validateReplaySeq).
func validateSeqRange(s Seq) error {
	if s > MaxSeqSafeInteger {
		return fmt.Errorf("contract: seq %d exceeds MaxSeqSafeInteger", s)
	}
	return nil
}

// validateReplaySeq rejects seq below 1 (replay frames start at 1) or above
// the safe-integer ceiling.
func validateReplaySeq(s Seq) error {
	if s < 1 {
		return fmt.Errorf("contract: replay seq must be >= 1, got %d", s)
	}
	return validateSeqRange(s)
}

// validateBase64 requires a non-empty RFC4648 standard padded Base64 string.
func validateBase64(s string) error {
	if s == "" {
		return errors.New("contract: base64 payload must be non-empty")
	}
	if _, err := base64.StdEncoding.DecodeString(s); err != nil {
		return fmt.Errorf("contract: invalid base64: %w", err)
	}
	return nil
}
