package remote

import (
	"context"
	"sync"
	"sync/atomic"
)

var testDetachIdentity atomic.Uint64

type testBackendDetachReceipt struct {
	identity uint64
	done     chan struct{}
	once     sync.Once

	mu        sync.RWMutex
	confirmed bool
	lastErr   error
}

func newTestDetachReceipt(confirmed bool, initialErr error) *testBackendDetachReceipt {
	r := &testBackendDetachReceipt{
		identity: testDetachIdentity.Add(1),
		done:     make(chan struct{}),
		lastErr:  initialErr,
	}
	if confirmed {
		r.confirm(nil)
	}
	return r
}

func confirmedTestDetachReceipt() BackendDetachReceipt {
	return newTestDetachReceipt(true, nil)
}

func (r *testBackendDetachReceipt) Identity() uint64 { return r.identity }
func (r *testBackendDetachReceipt) Confirmed() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.confirmed
}
func (r *testBackendDetachReceipt) LastError() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastErr
}
func (r *testBackendDetachReceipt) Wait(ctx context.Context) error {
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *testBackendDetachReceipt) confirm(err error) {
	r.mu.Lock()
	r.lastErr = err
	r.confirmed = err == nil
	r.mu.Unlock()
	if err == nil {
		r.once.Do(func() { close(r.done) })
	}
}
