package remote

import (
	"context"
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
)

type journalRetryKey struct {
	OperationID string
	ReceiptID   uint64
}

type journalRetryDebt struct {
	key         journalRetryKey
	journal     SessionOperationJournal
	permit      *OperationRecordPermit
	outcome     SessionOperationOutcome
	failureCode contract.ErrorCode
}

// JournalRetryRegistry owns same-process result debt. Entries are keyed by the
// committed operation and Authority remove receipt; successful Complete removes
// the exact debt. It is not durable committed proof.
type JournalRetryRegistry struct {
	mu      sync.Mutex
	entries map[journalRetryKey]*journalRetryDebt
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

func NewJournalRetryRegistry() *JournalRetryRegistry {
	r := &JournalRetryRegistry{
		entries: make(map[journalRetryKey]*journalRetryDebt),
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go r.reapLoop()
	return r
}

// ActivateCommitted may be called only after the composite returned a definitive
// RemoveReceipt. Duplicate activation for the same key is idempotent.
func (r *JournalRetryRegistry) ActivateCommitted(journal SessionOperationJournal, permit *OperationRecordPermit, evidence SessionOperationCommitEvidence) {
	if r == nil || journal == nil || permit == nil || evidence.ReceiptID == 0 || permit.intent.OperationID == "" {
		return
	}
	permit.BindCommitEvidence(evidence)
	key := journalRetryKey{OperationID: permit.intent.OperationID, ReceiptID: evidence.ReceiptID}
	r.mu.Lock()
	if _, exists := r.entries[key]; !exists {
		r.entries[key] = &journalRetryDebt{key: key, journal: journal, permit: permit, outcome: SessionOutcomeCommitted}
	}
	r.mu.Unlock()
	r.signal()
}

func (r *JournalRetryRegistry) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *JournalRetryRegistry) reapLoop() {
	defer close(r.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.wake:
			r.retryBounded(32)
		case <-ticker.C:
			r.retryBounded(32)
		case <-r.stop:
			return
		}
	}
}

func (r *JournalRetryRegistry) retryBounded(limit int) int {
	if r == nil || limit <= 0 {
		return 0
	}
	r.mu.Lock()
	debts := make([]*journalRetryDebt, 0, min(limit, len(r.entries)))
	for _, debt := range r.entries {
		debts = append(debts, debt)
		if len(debts) == limit {
			break
		}
	}
	r.mu.Unlock()
	completed := 0
	for _, debt := range debts {
		if debt.journal.Complete(context.Background(), debt.permit, debt.outcome, debt.failureCode) != nil {
			continue
		}
		r.mu.Lock()
		if r.entries[debt.key] == debt {
			delete(r.entries, debt.key)
			completed++
		}
		r.mu.Unlock()
	}
	return completed
}

// Flush retries within the caller's bounded context. It never infers a durable
// committed result after process restart; only in-memory receipts enter here.
func (r *JournalRetryRegistry) Flush(ctx context.Context) bool {
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
		r.retryBounded(32)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (r *JournalRetryRegistry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

func (r *JournalRetryRegistry) Close(ctx context.Context) bool {
	if r == nil {
		return true
	}
	flushed := r.Flush(ctx)
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	select {
	case <-r.done:
	case <-ctx.Done():
		return false
	}
	return flushed
}
