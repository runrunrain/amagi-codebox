package remote

// control_lane.go — boundedOperationLane: per-session single-slot, context-aware
// raw command serializer (design §9.1.2, §9.1.3).
//
// The lane serializes raw PTY/session mutations for one session. The state
// fence (take/release/expire/revoke) NEVER waits for the lane or for a blocked
// operation's done channel. Instead, a fence cancels the current operation's
// context and increments controlEpoch/backendEpoch. The next operation that
// needs the same backend waits at most controlOperationLaneWaitTimeout for the
// old operation to release the lane; if it does not, the backend is quarantined.
//
// Lock order (design §9.3): the lane is acquired and released WITHOUT holding
// any control/registry/session/PTY lock. Raw effect execution holds NO control
// lock.

import (
	"context"
	"errors"
	"sync"
	"time"
)

// errOperationLaneTimeout is returned when acquiring the lane exceeds the wait
// budget. The caller decides whether to quarantine the backend.
var errOperationLaneTimeout = errors.New("control: operation lane wait timeout")

// errOperationFenced is returned by Checkpoint when the operation has been
// fenced (a take/release/expire/revoke/stop committed a new generation).
var errOperationFenced = errors.New("control: operation fenced")

// errBackendQuarantined is returned when the backend is quarantined due to a
// non-acknowledging operation.
var errBackendQuarantined = errors.New("control: backend quarantined")

// boundedOperationLane is a per-session single-slot serializer. It provides:
//   - acquire(ctx, timeout): wait for the slot with a deadline; returns nil on
//     success, ctx.Err() on context cancel, errOperationLaneTimeout on budget.
//   - release(): return the slot.
//
// The lane does NOT track ownership of the current operation — that is the
// controlEntry's responsibility under stateMu. The lane is purely a
// capacity/serialization primitive.
type boundedOperationLane struct {
	// token is a buffered channel of capacity 1; a token in the channel means
	// the slot is free.
	token chan struct{}

	// mu protects waitCh replacement. waitCh is closed when the slot becomes
	// free, allowing concurrent waiters to wake.
	mu     sync.Mutex
	waitCh chan struct{}
}

func newBoundedOperationLane() *boundedOperationLane {
	ch := make(chan struct{}, 1)
	ch <- struct{}{} // initially free
	return &boundedOperationLane{
		token:  ch,
		waitCh: make(chan struct{}),
	}
}

// acquire waits for the lane slot to become available, up to timeout or ctx
// cancellation. On success the caller owns the slot and MUST call release.
func (l *boundedOperationLane) acquire(ctx context.Context, timeout time.Duration) error {
	// Fast path: try to grab the token without waiting.
	select {
	case <-l.token:
		return nil
	default:
	}

	// Slow path: wait for token, timeout, or ctx cancel.
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-l.token:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errOperationLaneTimeout
		default:
		}
		// Wait for someone to signal the slot is free, then retry.
		l.mu.Lock()
		waitCh := l.waitCh
		l.mu.Unlock()
		select {
		case <-l.token:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errOperationLaneTimeout
		case <-waitCh:
			// loop and retry token acquisition
		}
	}
}

// release returns the slot and wakes all waiters.
func (l *boundedOperationLane) release() {
	select {
	case l.token <- struct{}{}:
		// Signal waiters that the slot is free.
		l.mu.Lock()
		close(l.waitCh)
		l.waitCh = make(chan struct{})
		l.mu.Unlock()
	default:
		// Slot already free (double release in error path — safe idempotent).
	}
}

// tryAcquire attempts to grab the slot without waiting. Returns true on success.
func (l *boundedOperationLane) tryAcquire() bool {
	select {
	case <-l.token:
		return true
	default:
		return false
	}
}
