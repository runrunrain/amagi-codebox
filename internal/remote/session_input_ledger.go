package remote

// session_input_ledger.go — CG-03 per-session input idempotency ledger
// (contract-addendum-cg03.md §5, design §9).
//
// The canonical input path (msg-v1- IDs) uses a per-session, bounded, NON-evicting
// ledger keyed by (DeviceID, MessageID) to deliver at-most-once raw PTY writes +
// exactly-once ACK settlement across reconnects. Legacy non-empty opaque IDs keep
// the per-connection dedupe + silent-success path (wsV1Connection.inputIDs) and
// NEVER touch this ledger.
//
// Authority-first: the ledger is only touched AFTER the M3-A OperationLane grants
// the exact permit (inside DoDevicePTY). The ledger NEVER calls back into the
// gate/hub/socket — the handler reads the Reserve status and decides raw/ACK.
//
// No payload/hash is stored (privacy: design §6/§9). The entry state is the sole
// record. Capacity is 8192 entries / 1 MiB ID bytes with NO eviction: a full
// ledger rejects new keys (rate.limited) rather than silently dropping a prior
// commitment. Session remove destroys the ledger.

import (
	"sync"

	"amagi-codebox/internal/remote/contract"
)

// inputLedgerState is the per-key ledger state.
type inputLedgerState uint8

const (
	inputLedgerPending       inputLedgerState = iota + 1 // reserved by an in-flight attempt; raw write not yet committed
	inputLedgerCommitted                                 // raw write committed exactly once (or a duplicate re-ACK)
	inputLedgerIndeterminate                             // raw was called but returned error; outcome unknown, no rewrite
)

// InputLedgerStatus is the Reserve result.
type InputLedgerStatus uint8

const (
	// InputLedgerOwner: this Reserve call created the pending entry; the caller
	// owns the raw write and MUST Commit, MarkIndeterminate or ReleaseUncalled.
	InputLedgerOwner InputLedgerStatus = iota + 1
	// InputLedgerCommitted: the key is already committed; re-ACK only, do NOT
	// rewrite raw.
	InputLedgerCommitted
	// InputLedgerPending: another attempt owns the in-flight entry; do NOT
	// rewrite (the owner resolves it).
	InputLedgerPending
	// InputLedgerIndeterminate: a prior raw call errored; outcome unknown; do NOT
	// rewrite.
	InputLedgerIndeterminate
	// InputLedgerFull: the ledger is at capacity (entries or ID bytes); reject
	// the new key with rate.limited (no eviction).
	InputLedgerFull
)

// SessionInputLedger caps (design §5): 8192 entries / 1 MiB ID bytes, no eviction.
const (
	sessionInputLedgerMaxEntries = 8192
	sessionInputLedgerMaxIDBytes = 1 << 20
)

type inputLedgerKey struct {
	device contract.DeviceID
	id     contract.MessageID
}

// SessionInputLedger is the per-session canonical input ledger.
type SessionInputLedger struct {
	mu      sync.Mutex
	entries map[inputLedgerKey]inputLedgerState
	idBytes int
}

// NewSessionInputLedger creates an empty per-session ledger.
func NewSessionInputLedger() *SessionInputLedger {
	return &SessionInputLedger{entries: make(map[inputLedgerKey]inputLedgerState)}
}

// Reserve atomically classifies the (device, id) key. See InputLedgerStatus.
// A brand-new key is recorded as pending and returns Owner unless the ledger is
// full (Full, no eviction). The ledger never stores payload/hash.
func (l *SessionInputLedger) Reserve(device contract.DeviceID, id contract.MessageID) InputLedgerStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := inputLedgerKey{device: device, id: id}
	switch l.entries[key] {
	case inputLedgerCommitted:
		return InputLedgerCommitted
	case inputLedgerIndeterminate:
		return InputLedgerIndeterminate
	case inputLedgerPending:
		return InputLedgerPending
	}
	// New key: capacity check (no eviction).
	if len(l.entries) >= sessionInputLedgerMaxEntries || l.idBytes+len(id) > sessionInputLedgerMaxIDBytes {
		return InputLedgerFull
	}
	l.entries[key] = inputLedgerPending
	l.idBytes += len(id)
	return InputLedgerOwner
}

// Commit transitions a pending entry to committed. No-op for non-pending keys.
func (l *SessionInputLedger) Commit(device contract.DeviceID, id contract.MessageID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := inputLedgerKey{device: device, id: id}
	if l.entries[key] == inputLedgerPending {
		l.entries[key] = inputLedgerCommitted
	}
}

// MarkIndeterminate transitions a pending entry to indeterminate (raw errored).
// No-op for non-pending keys.
func (l *SessionInputLedger) MarkIndeterminate(device contract.DeviceID, id contract.MessageID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := inputLedgerKey{device: device, id: id}
	if l.entries[key] == inputLedgerPending {
		l.entries[key] = inputLedgerIndeterminate
	}
}

// ReleaseUncalled removes a pending entry whose raw write was never attempted
// (e.g. the gate granted but the raw port was nil). No-op for non-pending keys.
func (l *SessionInputLedger) ReleaseUncalled(device contract.DeviceID, id contract.MessageID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := inputLedgerKey{device: device, id: id}
	if l.entries[key] == inputLedgerPending {
		delete(l.entries, key)
		l.idBytes -= len(id)
	}
}

// SessionInputLedgerRegistry holds one ledger per session. Destroy on session
// remove. Lazy-created on first canonical input for a session.
type SessionInputLedgerRegistry struct {
	mu      sync.Mutex
	ledgers map[contract.SessionID]*SessionInputLedger
}

// NewSessionInputLedgerRegistry creates an empty registry.
func NewSessionInputLedgerRegistry() *SessionInputLedgerRegistry {
	return &SessionInputLedgerRegistry{ledgers: make(map[contract.SessionID]*SessionInputLedger)}
}

// Ledger returns the ledger for sessionID, creating it on first use.
func (r *SessionInputLedgerRegistry) Ledger(sessionID contract.SessionID) *SessionInputLedger {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.ledgers[sessionID]
	if !ok {
		l = NewSessionInputLedger()
		r.ledgers[sessionID] = l
	}
	return l
}

// Destroy removes the ledger for sessionID (session remove). Idempotent.
func (r *SessionInputLedgerRegistry) Destroy(sessionID contract.SessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.ledgers, sessionID)
}
