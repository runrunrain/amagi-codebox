package pty

// detach_receipt.go — platform-neutral exact-backend detach receipt + reaper.
// Platform services remove one concrete *PtySession from the active map, then
// pass a closure capturing that exact pointer to detachWithExactReaper. Retries
// never resolve by SessionID, so a later same-ID backend cannot be closed.

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const (
	detachRetryInitialDelay = 10 * time.Millisecond
	detachRetryMaxDelay     = 30 * time.Second
)

var detachReceiptSeq atomic.Uint64

// DetachReceipt is the typed evidence returned by Service.DetachSession. An
// initial close error is returned synchronously by DetachSession and retained
// here; the receipt becomes confirmed only when the exact captured backend has
// closed successfully (possibly by the background reaper).
type DetachReceipt struct {
	identity uint64
	done     chan struct{}
	once     sync.Once

	mu        sync.RWMutex
	confirmed bool
	lastErr   error
}

func newDetachReceipt() *DetachReceipt {
	return &DetachReceipt{
		identity: detachReceiptSeq.Add(1),
		done:     make(chan struct{}),
	}
}

// Identity is a process-local opaque detach identity. It contains no session,
// process, path, or credential data and is used only for exact-match diagnostics.
func (r *DetachReceipt) Identity() uint64 {
	if r == nil {
		return 0
	}
	return r.identity
}

// Confirmed reports whether the exact backend close has succeeded.
func (r *DetachReceipt) Confirmed() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.confirmed
}

// LastError returns the latest close failure observed by the exact reaper.
func (r *DetachReceipt) LastError() error {
	if r == nil {
		return context.Canceled
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastErr
}

// Wait blocks until exact detach confirmation or caller cancellation. A failed
// initial attempt does not close done because the reaper still owns the exact
// backend and continues retrying.
func (r *DetachReceipt) Wait(ctx context.Context) error {
	if r == nil {
		return context.Canceled
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *DetachReceipt) recordFailure(err error) {
	r.mu.Lock()
	r.lastErr = err
	r.mu.Unlock()
}

func (r *DetachReceipt) confirm() {
	r.mu.Lock()
	r.confirmed = true
	r.lastErr = nil
	r.mu.Unlock()
	r.once.Do(func() { close(r.done) })
}

// detachWithExactReaper executes the first close synchronously so its error is
// observable by the caller. On failure it starts a bounded-backoff reaper over
// the SAME closure. The closure must capture one detached backend pointer and
// must never look it up again by SessionID.
func detachWithExactReaper(receipt *DetachReceipt, detachExact func() error, onRetryError func(error)) error {
	if receipt == nil || detachExact == nil {
		return context.Canceled
	}
	if err := detachExact(); err != nil {
		receipt.recordFailure(err)
		go func() {
			delay := detachRetryInitialDelay
			for {
				time.Sleep(delay)
				if err := detachExact(); err == nil {
					receipt.confirm()
					return
				} else {
					receipt.recordFailure(err)
					if onRetryError != nil {
						onRetryError(err)
					}
				}
				if delay < detachRetryMaxDelay {
					delay *= 2
					if delay > detachRetryMaxDelay {
						delay = detachRetryMaxDelay
					}
				}
			}
		}()
		return err
	}
	receipt.confirm()
	return nil
}
