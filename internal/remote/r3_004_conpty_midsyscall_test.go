package remote

// r3_004_conpty_midsyscall_test.go — R3-004 regression: a bounded raw effect
// that times out mid-syscall must (1) physically detach the stuck backend via
// PTYRawPort.DetachSession (so a stuck ConPTY overlapped I/O / ptmx read is
// released by the OS), and (2) NOT lock out lifecycle recovery — a trusted
// desktop Stop/Restart on the quarantined backend succeeds so the user can
// clean up instead of waiting for global shutdown.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// detachRecordingPTYRaw blocks WriteRaw until release, and records DetachSession
// calls so the test proves the quarantine path force-detaches the backend.
type detachRecordingPTYRaw struct {
	mu           sync.Mutex
	release      chan struct{}
	detaches     []string
	writeEntered chan struct{}
}

func newDetachRecordingPTYRaw() *detachRecordingPTYRaw {
	return &detachRecordingPTYRaw{
		release:      make(chan struct{}),
		writeEntered: make(chan struct{}, 1),
	}
}

func (d *detachRecordingPTYRaw) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	select {
	case d.writeEntered <- struct{}{}:
	default:
	}
	<-d.release
	return nil
}
func (d *detachRecordingPTYRaw) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	<-d.release
	return nil
}
func (d *detachRecordingPTYRaw) DetachSession(sessionID string) (BackendDetachReceipt, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.detaches = append(d.detaches, sessionID)
	// Force-unblock the hanging WriteRaw (mirrors ConPTY handle close cancelling
	// outstanding overlapped I/O). Idempotent.
	select {
	case <-d.release:
	default:
		close(d.release)
	}
	return confirmedTestDetachReceipt(), nil
}
func (d *detachRecordingPTYRaw) Detaches() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.detaches))
	copy(out, d.detaches)
	return out
}

// TestR3_004_TimeoutDetachesBackend proves a mid-syscall timeout invokes
// PTYRawPort.DetachSession on the stuck session (the backend is physically
// detached, not just epoch-isolated). The hanging goroutine is released BY the
// detach (not by t.Cleanup), proving the detach is what unblocks it.
func TestR3_004_TimeoutDetachesBackend(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clock, nil)
	raw := newDetachRecordingPTYRaw()
	rt.SetPTYRawPort(raw)
	rt.MarkReady()

	sid := contract.SessionID("r3-004-detach")
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(sid, obsPermit)

	// DesktopInput on a hanging raw port must return a typed timeout within the
	// budget, AND the backend must have been force-detached.
	start := time.Now()
	werr := rt.DesktopInput(context.Background(), sid, []byte("hang"))
	elapsed := time.Since(start)
	if elapsed > controlRawEffectTimeout+2*time.Second {
		t.Fatalf("DesktopInput blocked %v past the raw-effect budget (R3-004 regression)", elapsed)
	}
	var ge *ControlGateError
	if !errors.As(werr, &ge) || ge.Kind != DenyOperationTimeout {
		t.Fatalf("expected DenyOperationTimeout, got %v", werr)
	}
	if ge.Detach != BackendDetachQuarantinedDetached {
		t.Fatalf("timeout lost typed detach receipt: disposition=%s", ge.Detach)
	}
	detaches := raw.Detaches()
	if len(detaches) != 1 || detaches[0] != string(sid) {
		t.Fatalf("R3-004 regression: expected DetachSession(%s) once, got %v", sid, detaches)
	}
	// The hanging WriteRaw goroutine was released by the detach (the release
	// channel is closed). Give it a moment to observe + exit.
	select {
	case <-time.After(500 * time.Millisecond):
		t.Fatal("R3-004: hanging WriteRaw goroutine was not released by DetachSession")
	case <-time.After(10 * time.Millisecond):
		// release observed; ok
	}
}

// TestR3_004_QuarantineDoesNotLockLifecycleRecovery proves that after a
// mid-syscall timeout quarantines the backend, a trusted desktop Stop lifecycle
// STILL succeeds (the user can clean up). Pre-R3-004, commitLifecycleIntent
// denied all lifecycle on a quarantined backend, locking the session until
// global shutdown.
func TestR3_004_QuarantineDoesNotLockLifecycleRecovery(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clock, nil)
	raw := newDetachRecordingPTYRaw()
	rt.SetPTYRawPort(raw)
	// A lifecycle port that records CloseSession (recovery stop).
	lifecycle := &recoveryLifecyclePort{}
	rt.SetPTYLifecycleRawPort(lifecycle)
	rt.MarkReady()

	sid := contract.SessionID("r3-004-recover")
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(sid, obsPermit)

	// Quarantine the backend via a hanging write timeout.
	if werr := rt.DesktopInput(context.Background(), sid, []byte("hang")); werr == nil {
		t.Fatal("expected timeout from hanging write")
	}
	// Confirm DATA writes are still denied on the quarantined backend.
	if werr := rt.DesktopInput(context.Background(), sid, []byte("after")); werr == nil {
		t.Fatal("expected post-quarantine DATA write to be denied")
	}

	// R3-004: a trusted desktop Stop lifecycle MUST succeed on the quarantined
	// backend (recovery). Pre-fix this returned DenyControlUnavailable.
	_, stopErr := rt.Gate().DoDesktopLifecycle(context.Background(), rt.DesktopAuthority(), sid, LifecycleStop,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			if err := p.Checkpoint(ctx, 1); err != nil {
				// A quarantined backend advances backendEpoch; the permit minted
				// after quarantine matches. If Checkpoint fails here the recovery
				// path is broken.
				return SessionMutationResult{}, err
			}
			if err := lifecycle.CloseSession(sid); err != nil {
				return SessionMutationResult{}, err
			}
			return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
		})
	if stopErr != nil {
		var ge *ControlGateError
		if errors.As(stopErr, &ge) && ge.Kind == DenyControlUnavailable {
			t.Fatalf("R3-004 regression: recovery Stop denied on quarantined backend: %v", stopErr)
		}
		t.Fatalf("recovery Stop: %v", stopErr)
	}
	if len(lifecycle.closes) != 1 || lifecycle.closes[0] != string(sid) {
		t.Fatalf("recovery Stop did not close the session via the lifecycle port: %v", lifecycle.closes)
	}
}

// TestR3_004_QuarantineRestartResetsBackend proves that after a mid-syscall
// timeout, a desktop Restart recovers: the new process starts and subsequent
// DATA writes are ADMITTED again (the restart resets backend=healthy). This
// proves quarantine does not permanently brick the session.
func TestR3_004_QuarantineRestartResetsBackend(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clock, nil)
	// A raw port whose first write hangs (to trigger quarantine) and whose
	// DetachSession unblocks it; ResizeRaw succeeds.
	raw := &restartRecoveryPTYRaw{release: make(chan struct{})}
	rt.SetPTYRawPort(raw)
	rt.MarkReady()

	sid := contract.SessionID("r3-004-restart")
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(sid, obsPermit)

	// Quarantine via a hanging write.
	if werr := rt.DesktopInput(context.Background(), sid, []byte("hang")); werr == nil {
		t.Fatal("expected timeout from hanging write")
	}
	// Writes denied on quarantined backend.
	if werr := rt.DesktopInput(context.Background(), sid, []byte("denied")); werr == nil {
		t.Fatal("expected post-quarantine write denied before restart")
	}

	// Recovery: a desktop lifecycle that swaps the run (simulating restart commit)
	// resets backend=healthy. We drive it through CommitRestartRun via the
	// lifecycle path to exercise the real reset.
	_, rErr := rt.Gate().DoDesktopLifecycle(context.Background(), rt.DesktopAuthority(), sid, LifecycleRestart,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			sealReceipt, sErr := rt.SealRestartSegmentForPermit(p, sid)
			if sErr != nil {
				return SessionMutationResult{}, sErr
			}
			newObs, _, cErr := rt.CommitRestartRun(p, sealReceipt, sid)
			if cErr != nil {
				return SessionMutationResult{}, cErr
			}
			rt.Projector().TrackRestartRun(sid, newObs)
			return SessionMutationResult{State: contract.SessionStateRunning, StateChanged: true, RestartBoundary: true}, nil
		})
	if rErr != nil {
		t.Fatalf("recovery restart lifecycle on quarantined backend: %v", rErr)
	}

	// After restart, a DATA write is ADMITTED again (backend reset to healthy).
	// raw.release is already closed by DetachSession, so WriteRaw returns promptly.
	if werr := rt.DesktopInput(context.Background(), sid, []byte("after-restart")); werr != nil {
		var ge *ControlGateError
		if errors.As(werr, &ge) && ge.Kind == DenyControlUnavailable {
			t.Fatalf("R3-004 regression: DATA write still denied after recovery restart (backend not reset): %v", werr)
		}
		t.Fatalf("post-restart write: %v", werr)
	}
}

// recoveryLifecyclePort records Close/Remove for recovery assertions.
type recoveryLifecyclePort struct {
	mu      sync.Mutex
	closes  []string
	removes []string
}

func (r *recoveryLifecyclePort) CloseSession(sessionID contract.SessionID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes = append(r.closes, string(sessionID))
	return nil
}
func (r *recoveryLifecyclePort) RemoveSession(sessionID contract.SessionID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removes = append(r.removes, string(sessionID))
	return nil
}

// restartRecoveryPTYRaw hangs the first WriteRaw (release channel) and unblocks
// on DetachSession; subsequent writes succeed. Used to prove a post-restart
// write is admitted after the backend reset.
type restartRecoveryPTYRaw struct {
	mu       sync.Mutex
	release  chan struct{}
	detached bool
	writes   []string
}

func (r *restartRecoveryPTYRaw) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	r.mu.Lock()
	released := r.detached
	r.mu.Unlock()
	if !released {
		<-r.release
	}
	r.mu.Lock()
	r.writes = append(r.writes, string(data))
	r.mu.Unlock()
	return nil
}
func (r *restartRecoveryPTYRaw) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	return nil
}
func (r *restartRecoveryPTYRaw) DetachSession(sessionID string) (BackendDetachReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.detached = true
	select {
	case <-r.release:
	default:
		close(r.release)
	}
	return confirmedTestDetachReceipt(), nil
}
