package remote

// session_state_adapter.go — SessionStatus(5) → SessionState(5) adapter
// (design §4.3).
//
// The internal session.SessionStatus has 5 values: running/stopping/stopped/exited/failed.
// The frozen wire contract.SessionState has 5 values:
// running/stopped/exited/unavailable/removed. The adapter maps internal → wire
// with fail-closed semantics for unknown values and treats `removed` as an
// event-only state (not derived from a missing record).

import (
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

// SessionStateAdapter maps internal session.SessionStatus to wire
// contract.SessionState. It is stateless and pure.
type SessionStateAdapter struct{}

// NewSessionStateAdapter returns a stateless adapter.
func NewSessionStateAdapter() *SessionStateAdapter { return &SessionStateAdapter{} }

// ToWireState maps an internal SessionStatus to a wire SessionState.
//
// Mapping (design §4.3):
//   - running   → running
//   - stopping  → unavailable (active but read-only; not a terminal receipt)
//   - stopped   → stopped
//   - exited    → exited
//   - failed    → unavailable  (frozen wire has no "failed"; safe downgrade)
//   - unknown   → unavailable  (unknown future values default to read-only)
//
// `removed` is NOT a SessionStatus value; it is produced only by a successful
// delete transaction event, never derived from a missing record.
func (SessionStateAdapter) ToWireState(status session.SessionStatus) contract.SessionState {
	switch status {
	case session.StatusRunning:
		return contract.SessionStateRunning
	case session.StatusStopping:
		// Frozen wire state has no "stopping". Unavailable is the fail-closed,
		// read-only projection; raw Remove/Clear still reject via Manager state.
		return contract.SessionStateUnavailable
	case session.StatusStopped:
		return contract.SessionStateStopped
	case session.StatusExited:
		return contract.SessionStateExited
	case session.StatusFailed:
		return contract.SessionStateUnavailable
	default:
		// Unknown future value → unavailable (fail-closed, read-only).
		return contract.SessionStateUnavailable
	}
}

// IsWritable reports whether a session in the given wire state accepts
// input/resize. Per design §4.3: only `running` with an active run and PTY
// capability accepts writes. stopped/exited/unavailable/removed reject input.
func (SessionStateAdapter) IsWritable(state contract.SessionState) bool {
	return state == contract.SessionStateRunning
}

// ToWireStatus maps internal status + the "removed" flag to the wire state. When
// removed is true, the result is SessionStateRemoved (event-only; never derived
// from a missing record). Otherwise delegates to ToWireState.
func (a SessionStateAdapter) ToWireStatus(status session.SessionStatus, removed bool) contract.SessionState {
	if removed {
		return contract.SessionStateRemoved
	}
	return a.ToWireState(status)
}

// ComposeSessionState reads the arbiter's session state mirror and the adapter
// to produce the wire SessionState for a session. The mirror is maintained by
// controlled commands and validated run observations under stateMu; this avoids
// M2 handlers independently reading Manager + arbiter and assembling a torn DTO
// (design §4.3).
//
// If the arbiter mirror is unset, the adapter falls back to the provided
// SessionStatus. If the session is not found in the arbiter, returns
// SessionStateUnavailable (fail-closed).
func ComposeSessionState(
	arbiter *ControlArbiter,
	adapter SessionStateAdapter,
	sessionID contract.SessionID,
	fallback session.SessionStatus,
) contract.SessionState {
	mirror, _, ok := arbiter.SessionStateMirror(sessionID)
	if !ok {
		return contract.SessionStateUnavailable
	}
	if mirror == "" {
		// Mirror not set: use the adapter fallback.
		return adapter.ToWireState(fallback)
	}
	return mirror
}
