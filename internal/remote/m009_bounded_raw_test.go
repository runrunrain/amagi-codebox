package remote

// m009_bounded_raw_test.go — M-009 regression: a raw PTY effect that blocks
// (stuck syscall) must not occupy the caller/lane indefinitely. The gate bounds
// the effect; on timeout it quarantines the backend (backendEpoch isolation) and
// returns a typed timeout.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// blockingPTYRaw's WriteRaw blocks until the ctx is cancelled (a well-behaved
// raw port that observes the gate deadline).
type blockingPTYRaw struct{}

func (blockingPTYRaw) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	<-ctx.Done()
	return ctx.Err()
}

func (blockingPTYRaw) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	<-ctx.Done()
	return ctx.Err()
}

func (blockingPTYRaw) DetachSession(sessionID string) (BackendDetachReceipt, error) {
	return confirmedTestDetachReceipt(), nil
}

// TestM009_BoundedRawEffect_TimeoutQuarantinesBackend proves a blocking raw
// write is bounded: DesktopInput returns a typed timeout within
// controlRawEffectTimeout (+margin) and the backend is quarantined so a
// subsequent write is rejected.
func TestM009_BoundedRawEffect_TimeoutQuarantinesBackend(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clock, nil)
	rt.SetPTYRawPort(blockingPTYRaw{})
	rt.MarkReady()

	sid := contract.SessionID("m009-block")
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(sid, obsPermit)

	// DesktopInput with a blocking raw port must return a typed timeout, bounded.
	start := time.Now()
	werr := rt.DesktopInput(context.Background(), sid, []byte("block-me"))
	elapsed := time.Since(start)

	if elapsed > controlRawEffectTimeout+2*time.Second {
		t.Fatalf("DesktopInput blocked %v past the raw-effect budget (M-009 regression)", elapsed)
	}
	var ge *ControlGateError
	if !errors.As(werr, &ge) || ge.Kind != DenyOperationTimeout {
		t.Fatalf("expected DenyOperationTimeout, got %v", werr)
	}

	// The backend is quarantined: a subsequent write is rejected, never silently
	// passing through.
	if err := rt.DesktopInput(context.Background(), sid, []byte("after")); err == nil {
		t.Fatal("expected post-quarantine write to be rejected, got nil")
	}
}

// hangingPTYRaw ignores ctx entirely (mirrors a genuinely hung kernel syscall
// that cannot be interrupted) but unblocks on release so the test does not leak a
// goroutine across -count runs. R3-004: DetachSession closes the release channel
// to prove the quarantine path force-detaches a stuck backend (the hanging
// goroutine is released by the detach, not just by t.Cleanup). The close is
// sync.Once-guarded so t.Cleanup + DetachSession cannot double-close.
type hangingPTYRaw struct {
	release   chan struct{}
	closeOnce sync.Once
}

func (h *hangingPTYRaw) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	<-h.release
	return nil
}
func (h *hangingPTYRaw) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	<-h.release
	return nil
}
func (h *hangingPTYRaw) DetachSession(sessionID string) (BackendDetachReceipt, error) {
	// Force-unblock the hanging syscall (mirrors ConPTY handle close cancelling
	// outstanding overlapped I/O). sync.Once-guarded so it is safe to call from
	// both the quarantine path and the test's t.Cleanup.
	h.closeOnce.Do(func() { close(h.release) })
	return confirmedTestDetachReceipt(), nil
}

// TestM009_BoundedRawEffect_HangingSyscallBounded proves that even a raw port
// that ignores the ctx deadline cannot occupy the caller: DesktopInput returns
// within the budget. The gate isolates the backend via quarantine.
func TestM009_BoundedRawEffect_HangingSyscallBounded(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clock, nil)
	hang := &hangingPTYRaw{release: make(chan struct{})}
	t.Cleanup(func() { hang.DetachSession("") }) // safe (sync.Once-guarded)
	rt.SetPTYRawPort(hang)
	rt.MarkReady()

	sid := contract.SessionID("m009-hang")
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(sid, obsPermit)

	start := time.Now()
	werr := rt.DesktopInput(context.Background(), sid, []byte("hang"))
	elapsed := time.Since(start)
	if elapsed > controlRawEffectTimeout+2*time.Second {
		t.Fatalf("DesktopInput blocked %v past the raw-effect budget (M-009 regression)", elapsed)
	}
	var ge *ControlGateError
	if !errors.As(werr, &ge) || ge.Kind != DenyOperationTimeout {
		t.Fatalf("expected DenyOperationTimeout, got %v", werr)
	}
}

// recordingPTYRaw records WriteRaw calls (for the bootstrap test).
type recordingPTYRaw struct {
	mu     sync.Mutex
	writes [][]byte
}

func (r *recordingPTYRaw) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	r.mu.Lock()
	r.writes = append(r.writes, data)
	r.mu.Unlock()
	return nil
}
func (r *recordingPTYRaw) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	return nil
}
func (r *recordingPTYRaw) DetachSession(sessionID string) (BackendDetachReceipt, error) {
	return confirmedTestDetachReceipt(), nil
}

// TestM005_DesktopBootstrap_RoutesAutoCommandThroughGate proves the delayed
// bootstrap (auto-command) write reaches the PTY through DoBootstrapPTY with a
// post-activation RunPermit (M-005), not a raw cpty.Write.
func TestM005_DesktopBootstrap_RoutesAutoCommandThroughGate(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clock, nil)
	raw := &recordingPTYRaw{}
	rt.SetPTYRawPort(raw)
	rt.MarkReady()

	sid := contract.SessionID("m005-bootstrap")
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(sid, obsPermit)

	cmd := []byte("claude\r\n")
	if err := rt.DesktopBootstrap(context.Background(), runPermit, sid, cmd); err != nil {
		t.Fatalf("DesktopBootstrap: %v", err)
	}
	raw.mu.Lock()
	writes := raw.writes
	raw.mu.Unlock()
	if len(writes) != 1 || string(writes[0]) != string(cmd) {
		t.Fatalf("bootstrap auto-command not routed through the gate: writes=%v", writes)
	}
}
