package launchplan

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// CompensationDisposition is the conservative result of one compensation
// attempt. Only Confirmed permits the owner to be forgotten.
type CompensationDisposition string

const (
	CompensationConfirmed     CompensationDisposition = "confirmed"
	CompensationUnavailable   CompensationDisposition = "unavailable"
	CompensationIndeterminate CompensationDisposition = "indeterminate"
)

// CompensationOutcome identifies one exact owner/effect/step result.
type CompensationOutcome struct {
	Owner       string                  `json:"owner"`
	Effect      EffectKind              `json:"effect"`
	Step        string                  `json:"step"`
	Disposition CompensationDisposition `json:"disposition"`
	Message     string                  `json:"message,omitempty"`
}

// CompensationDebt is the query-safe projection retained for a failed or
// indeterminate compensation. It contains no config bytes or credentials.
type CompensationDebt struct {
	CompensationOutcome
	Attempts  uint64    `json:"attempts"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type compensationRetry func(context.Context) CompensationOutcome

type compensationDebtEntry struct {
	debt  CompensationDebt
	retry compensationRetry
}

// CompensationDebtRegistry owns exact receipt/key debts. Entries are
// queryable and retriable; successful retries remove only the exact owner.
type CompensationDebtRegistry struct {
	mu      sync.Mutex
	entries map[string]compensationDebtEntry
}

func NewCompensationDebtRegistry() *CompensationDebtRegistry {
	return &CompensationDebtRegistry{entries: make(map[string]compensationDebtEntry)}
}

// Record stores or replaces the debt for outcome.Owner. Confirmed outcomes
// resolve the exact owner instead of retaining a false debt.
func (r *CompensationDebtRegistry) Record(outcome CompensationOutcome, retry func(context.Context) CompensationOutcome) {
	if r == nil || outcome.Owner == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if outcome.Disposition == CompensationConfirmed {
		delete(r.entries, outcome.Owner)
		return
	}
	attempts := uint64(1)
	if old, ok := r.entries[outcome.Owner]; ok {
		attempts = old.debt.Attempts + 1
	}
	r.entries[outcome.Owner] = compensationDebtEntry{
		debt:  CompensationDebt{CompensationOutcome: outcome, Attempts: attempts, UpdatedAt: time.Now().UTC()},
		retry: retry,
	}
}

// List returns a stable owner-sorted snapshot.
func (r *CompensationDebtRegistry) List() []CompensationDebt {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]CompensationDebt, 0, len(r.entries))
	for _, entry := range r.entries {
		out = append(out, entry.debt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Owner < out[j].Owner })
	return out
}

// Retry retries one exact owner. The retry executes outside the registry lock.
func (r *CompensationDebtRegistry) Retry(ctx context.Context, owner string) (CompensationOutcome, error) {
	if r == nil || owner == "" {
		return CompensationOutcome{}, errors.New("launchplan: compensation debt owner is required")
	}
	r.mu.Lock()
	entry, ok := r.entries[owner]
	r.mu.Unlock()
	if !ok {
		return CompensationOutcome{}, errors.New("launchplan: compensation debt not found")
	}
	if entry.retry == nil {
		return entry.debt.CompensationOutcome, errors.New("launchplan: compensation retry unavailable")
	}
	outcome := entry.retry(ctx)
	if outcome.Owner == "" {
		outcome.Owner = owner
	}
	r.Record(outcome, entry.retry)
	if outcome.Disposition != CompensationConfirmed {
		return outcome, errors.New("launchplan: compensation remains unresolved")
	}
	return outcome, nil
}
