package remote

// Code-aggregated bounded security health register (design §13.3). Issues are
// keyed by closed SecurityHealthCode only (≤ the closed code enum count). Each
// code keeps at most 8 recent EventIDs (ring, newest last), saturating
// Occurrences/DroppedEventIDs counters, and first/last timestamps. NO payload,
// free text, path, raw error, device secret/code or EventID→key map is stored.
// Acknowledge(code) never resolves/retries/restores securityReady. PreAccept
// and integrity codes are same-process sticky (no payload owner → unresolvable).

import (
	"sort"
	"sync"
	"time"
)

// securityHealthRegister is the bounded aggregate health register.
type securityHealthRegister struct {
	mu     sync.Mutex
	issues map[SecurityHealthCode]*healthAggregate
}

// healthAggregate is one per-code aggregate.
type healthAggregate struct {
	code            SecurityHealthCode
	active          bool
	acknowledged    bool
	firstObservedAt time.Time
	lastObservedAt  time.Time
	occurrences     uint64
	droppedEventIDs uint64
	recent          []SecurityEventID // max MaxSecurityHealthRecentEventIDs, newest last
}

// newSecurityHealthRegister returns an empty register.
func newSecurityHealthRegister() *securityHealthRegister {
	return &securityHealthRegister{issues: make(map[SecurityHealthCode]*healthAggregate)}
}

// stickyHealthCodes cannot be resolved in M1-A (no payload owner, no retry).
func stickyHealthCode(code SecurityHealthCode) bool {
	switch code {
	case HealthEventAppendFailed, HealthEventIntegrityFailed:
		return true
	}
	return false
}

// Record records an occurrence of a closed code, optionally with a sample
// EventID. It sets Active=true and Acknowledged=false (recurrence re-opens).
// Occurrences/DroppedEventIDs saturate at MaxUint64.
func (h *securityHealthRegister) Record(code SecurityHealthCode, eventID SecurityEventID, at time.Time) {
	if !isKnownHealthCode(code) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	agg := h.issues[code]
	if agg == nil {
		agg = &healthAggregate{code: code, recent: make([]SecurityEventID, 0, MaxSecurityHealthRecentEventIDs)}
		h.issues[code] = agg
	}
	agg.active = true
	agg.acknowledged = false
	agg.occurrences = saturatingAddU64(agg.occurrences, 1)
	if agg.firstObservedAt.IsZero() || at.Before(agg.firstObservedAt) {
		agg.firstObservedAt = at
	}
	if at.After(agg.lastObservedAt) {
		agg.lastObservedAt = at
	}
	if eventID != "" {
		// Move-to-newest: drop existing occurrence first.
		for i, id := range agg.recent {
			if id == eventID {
				agg.recent = append(agg.recent[:i], agg.recent[i+1:]...)
				break
			}
		}
		if len(agg.recent) < MaxSecurityHealthRecentEventIDs {
			agg.recent = append(agg.recent, eventID)
		} else {
			agg.recent = agg.recent[1:]
			agg.recent = append(agg.recent, eventID)
			agg.droppedEventIDs = saturatingAddU64(agg.droppedEventIDs, 1)
		}
	}
}

// Acknowledge marks a code acknowledged. It never resolves/retries/restores
// securityReady; recurrence (Record) re-opens it.
func (h *securityHealthRegister) Acknowledge(code SecurityHealthCode) {
	if !isKnownHealthCode(code) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	agg := h.issues[code]
	if agg == nil {
		return
	}
	agg.acknowledged = true
}

// Resolve clears the active condition for a non-sticky code. Resolved +
// acknowledged issues are removed from the map; otherwise they are retained.
// Sticky codes (append/integrity failed) are unresolvable and ignored. The
// caller is responsible for only resolving durability codes after a durable
// sink confirms its authoritative pending set is empty.
func (h *securityHealthRegister) Resolve(code SecurityHealthCode) {
	if stickyHealthCode(code) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	agg := h.issues[code]
	if agg == nil {
		return
	}
	agg.active = false
	if !agg.active && agg.acknowledged {
		delete(h.issues, code)
	}
}

// Issues returns a defensive, code-sorted copy of all current issues.
func (h *securityHealthRegister) Issues() []SecurityHealthIssue {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]SecurityHealthIssue, 0, len(h.issues))
	for _, agg := range h.issues {
		recent := make([]SecurityEventID, len(agg.recent))
		copy(recent, agg.recent)
		out = append(out, SecurityHealthIssue{
			Code:            agg.code,
			Active:          agg.active,
			Acknowledged:    agg.acknowledged,
			FirstObservedAt: rfc3339Nano(agg.firstObservedAt),
			LastObservedAt:  rfc3339Nano(agg.lastObservedAt),
			Occurrences:     agg.occurrences,
			DroppedEventIDs: agg.droppedEventIDs,
			RecentEventIDs:  recent,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Snapshot composes a health snapshot with the caller-supplied securityReady
// latch state (the latch lives in the store; health never clears it).
func (h *securityHealthRegister) Snapshot(securityReady bool) SecurityHealthSnapshot {
	return SecurityHealthSnapshot{SecurityReady: securityReady, Issues: h.Issues()}
}

// isKnownHealthCode reports whether code is one of the closed enum constants.
func isKnownHealthCode(code SecurityHealthCode) bool {
	switch code {
	case HealthStoreUnavailable, HealthStoreIndeterminate, HealthStoreDurabilityDegraded,
		HealthEventAppendFailed, HealthEventDurabilityDegraded, HealthEventIntegrityFailed,
		HealthDeviceSeenPersistFailed, HealthSnapshotTempCleanupFailed, HealthSnapshotTempBudgetExceeded:
		return true
	}
	return false
}

// saturatingAddU64 adds b to a, saturating at MaxUint64.
func saturatingAddU64(a, b uint64) uint64 {
	if b == 0 {
		return a
	}
	if a > ^uint64(0)-b {
		return ^uint64(0)
	}
	return a + b
}

// rfc3339Nano formats a time as UTC RFC3339Nano 'Z'. Zero renders as "".
func rfc3339Nano(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
