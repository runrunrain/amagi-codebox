package remote

import (
	"context"
	"errors"
	"sync"
	"time"

	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

type removeGCStep struct {
	done bool
	run  func() error
}

type removeGCEntry struct {
	mu      sync.Mutex
	receipt session.RemoveReceipt
	steps   []removeGCStep
}

// RemoveGCRegistry retains receipt-keyed post-commit cleanup debt. Public
// membership is already tombstoned; retries can only release exact capabilities.
type RemoveGCRegistry struct {
	mu      sync.Mutex
	entries map[uint64]*removeGCEntry
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

func NewRemoveGCRegistry() *RemoveGCRegistry {
	r := &RemoveGCRegistry{
		entries: make(map[uint64]*removeGCEntry), wake: make(chan struct{}, 1),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go r.reapLoop()
	return r
}

func newRemoveGCEntry(receipt session.RemoveReceipt, runtime *ControlRuntime, streams *SessionStreamStore, destroyLedger func(contract.SessionID), releaseShared func(string), registry *processcap.Registry, binding processcap.Binding, runGeneration uint64, authority *session.Manager) *removeGCEntry {
	sid := contract.SessionID(receipt.SessionID)
	steps := []removeGCStep{
		{run: func() error {
			if runtime != nil {
				runtime.RemoveDesktopSession(context.Background(), sid)
			}
			return nil
		}},
		{run: func() error {
			if runtime != nil {
				runtime.Projector().ForgetRun(sid)
			}
			return nil
		}},
		{run: func() error {
			if streams != nil {
				streams.RemoveStream(sid)
			}
			return nil
		}},
		{run: func() error {
			if destroyLedger != nil {
				destroyLedger(sid)
			}
			return nil
		}},
		{run: func() error {
			if releaseShared != nil {
				releaseShared(receipt.SessionID)
			}
			return nil
		}},
		{run: func() error {
			if registry != nil && binding != nil {
				return registry.ReleaseExact(binding.BindingID(), runGeneration, binding)
			}
			return nil
		}},
		{run: func() error {
			if authority == nil {
				return errors.New("remove gc: authority unavailable")
			}
			return authority.ReclaimTombstone(receipt)
		}},
	}
	return &removeGCEntry{receipt: receipt, steps: steps}
}

func (r *RemoveGCRegistry) Activate(entry *removeGCEntry) {
	if r == nil || entry == nil || entry.receipt.ReceiptID == 0 {
		return
	}
	r.mu.Lock()
	if _, exists := r.entries[entry.receipt.ReceiptID]; !exists {
		r.entries[entry.receipt.ReceiptID] = entry
	}
	r.mu.Unlock()
	r.retryBounded(1)
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *RemoveGCRegistry) retryEntry(entry *removeGCEntry) bool {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	for i := range entry.steps {
		if entry.steps[i].done {
			continue
		}
		if entry.steps[i].run == nil || entry.steps[i].run() == nil {
			entry.steps[i].done = true
			continue
		}
		return false
	}
	return true
}

func (r *RemoveGCRegistry) retryBounded(limit int) {
	if r == nil || limit <= 0 {
		return
	}
	r.mu.Lock()
	entries := make([]*removeGCEntry, 0, min(limit, len(r.entries)))
	for _, entry := range r.entries {
		entries = append(entries, entry)
		if len(entries) == limit {
			break
		}
	}
	r.mu.Unlock()
	for _, entry := range entries {
		if !r.retryEntry(entry) {
			continue
		}
		r.mu.Lock()
		if r.entries[entry.receipt.ReceiptID] == entry {
			delete(r.entries, entry.receipt.ReceiptID)
		}
		r.mu.Unlock()
	}
}

func (r *RemoveGCRegistry) reapLoop() {
	defer close(r.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.wake:
			r.retryBounded(16)
		case <-ticker.C:
			r.retryBounded(16)
		case <-r.stop:
			return
		}
	}
}

func (r *RemoveGCRegistry) Flush(ctx context.Context) bool {
	if r == nil {
		return true
	}
	for {
		r.mu.Lock()
		remaining := len(r.entries)
		r.mu.Unlock()
		if remaining == 0 {
			return true
		}
		r.retryBounded(16)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (r *RemoveGCRegistry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
