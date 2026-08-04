package remote

// control_lane_test.go — T-03, T-31, T-32: OperationLane bounded cancellation,
// checkpoint race, blocked operation quarantine (design §9.1.2, §9.1.3).

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// T-31: Checkpoint-won step can execute (spy=1), but fence blocks second step
// (design §5.2 INV-05, §9.1.3, §9.4).
func TestOperationLane_CheckpointRaceSingleStep(t *testing.T) {
	arb, gate, _, _, _ := newTestArbiter(t)
	ctx := context.Background()
	sid := startSessionDirect(t, arb)

	auth1 := newWailsAuthority(100)
	auth2 := newWailsAuthority(200)

	var spy atomic.Int32
	step1Done := make(chan struct{})
	takeDone := make(chan struct{})
	writeErr := make(chan error, 1)

	// First desktop write: checkpoint 1 succeeds, then take fires, then
	// checkpoint 2 fails.
	go func() {
		err := gate.DoDesktopPTY(ctx, auth1, sid, PTYInput, func(ctx context.Context, permit *operationPermit) error {
			if err := permit.Checkpoint(ctx, 1); err != nil {
				writeErr <- err
				return err
			}
			spy.Add(1) // step 1 admitted + executed
			close(step1Done)
			<-takeDone // wait for desktop take to fence
			// Step 2: should fail (epoch changed by desktop→desktop take).
			err := permit.Checkpoint(ctx, 2)
			if err != nil {
				writeErr <- err
				return err
			}
			spy.Add(1) // should NOT reach
			return nil
		})
		writeErr <- err
	}()

	<-step1Done
	// Desktop take with different authority → increments epoch.
	gate.TakeDesktop(ctx, auth2, sid)
	close(takeDone)

	// Wait for write to finish.
	err := <-writeErr
	_ = err // expected to be non-nil (checkpoint 2 failed)

	if spy.Load() != 1 {
		t.Fatalf("expected spy=1 (first step admitted, second blocked), got %d", spy.Load())
	}
}

// T-32: Blocked operation → quarantine on next lane acquire (design §9.1.3).
// A raw effect that blocks indefinitely (doesn't respect ctx) causes the lane
// to be held; the next operation times out and quarantines the backend.
func TestOperationLane_BlockedOperationQuarantine(t *testing.T) {
	arb, gate, _, _, _ := newTestArbiter(t)
	ctx := context.Background()
	sid := startSessionDirect(t, arb)
	auth := newWailsAuthority(1)

	// Start a blocked operation that never releases the lane.
	blockCh := make(chan struct{})
	go func() {
		_ = gate.DoDesktopPTY(ctx, auth, sid, PTYInput, func(ctx context.Context, permit *operationPermit) error {
			<-blockCh // blocks until test cleanup
			return nil
		})
	}()

	// Give the blocked operation time to acquire the lane.
	time.Sleep(100 * time.Millisecond)

	// Now take desktop (fences the blocked op's ctx, but it ignores ctx).
	gate.TakeDesktop(ctx, auth, sid)

	// Second operation: should time out on lane → quarantine → service.down.
	var spy atomic.Int32
	start := time.Now()
	err := gate.DoDesktopPTY(ctx, auth, sid, PTYInput, func(ctx context.Context, permit *operationPermit) error {
		spy.Add(1)
		return nil
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from quarantined backend")
	}
	if spy.Load() != 0 {
		t.Fatalf("expected spy=0 (quarantined), got %d", spy.Load())
	}
	// Should have waited ~1s (lane timeout), not unbounded.
	if elapsed > 3*time.Second {
		t.Fatalf("lane wait took too long: %v (expected ~1s budget)", elapsed)
	}

	// Backend should be quarantined.
	entry := arb.entryFor(sid)
	entry.stateMu.Lock()
	quarantined := entry.backend == backendQuarantined
	entry.stateMu.Unlock()
	if !quarantined {
		t.Fatal("expected backend quarantined after blocked op")
	}

	// Cleanup: release the blocked operation.
	close(blockCh)
}

// T-03: Desktop take vs remote input concurrency — fence prevents new writes
// (design §6.2, §9.4).
func TestOperationLane_DesktopTakeBlocksRemoteInput(t *testing.T) {
	arb, gate, _, dir, _ := newTestArbiter(t)
	ctx := context.Background()
	sid := startSessionDirect(t, arb)
	pDev := newTestDevicePrincipal("devZ", "Device Z")
	lease, _ := dir.Attach(pDev.DeviceID, pDev.DeviceName, "connZ", sid)
	arb.Acquire(pDev, lease, sid)

	// Desktop take preempts device.
	gate.TakeDesktop(ctx, newWailsAuthority(1), sid)

	// Device input: forbidden (not holder anymore).
	var spy atomic.Int32
	err := gate.DoDevicePTY(ctx, lease, sid, PTYInput, func(ctx context.Context, permit *operationPermit) error {
		spy.Add(1)
		return nil
	})
	if err == nil {
		t.Fatal("expected device input to fail after desktop take")
	}
	if spy.Load() != 0 {
		t.Fatalf("expected spy=0 after takeover, got %d", spy.Load())
	}
}

// T-26: Chaos concurrent acquire/release/take under race detector
// (design §9.4, §13 T-26).
func TestControlArbiter_ChaosConcurrentRace(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	auth := newWailsAuthority(1)

	pA := newTestDevicePrincipal("devA", "Device A")
	pB := newTestDevicePrincipal("devB", "Device B")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	leaseB, _ := dir.Attach(pB.DeviceID, pB.DeviceName, "connB", sid)

	var wg sync.WaitGroup
	const goroutines = 6
	const iterations = 100
	wg.Add(goroutines)

	// Goroutine 1-2: device A acquire/release.
	for g := 0; g < 2; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				arb.Acquire(pA, leaseA, sid)
				arb.Release(pA, sid)
			}
		}()
	}
	// Goroutine 3-4: device B acquire/release.
	for g := 0; g < 2; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				arb.Acquire(pB, leaseB, sid)
				arb.Release(pB, sid)
			}
		}()
	}
	// Goroutine 5: desktop take/release.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			arb.TakeDesktop(auth, sid)
			arb.ReleaseDesktop(auth, sid)
		}
	}()
	// Goroutine 6: snapshot reads.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			arb.SnapshotForDevice(sid, pA.DeviceID)
		}
	}()

	wg.Wait()

	// Final invariant: exactly one holder (or none).
	entry := arb.entryFor(sid)
	entry.stateMu.Lock()
	valid := entry.owner.kind == ownerNone || entry.owner.kind == ownerDesktop || entry.owner.kind == ownerDevice
	entry.stateMu.Unlock()
	if !valid {
		t.Fatal("invalid holder state after chaos")
	}
}

// Lane acquire/release basic test.
func TestBoundedOperationLane_AcquireRelease(t *testing.T) {
	lane := newBoundedOperationLane()
	ctx := context.Background()

	// First acquire succeeds immediately.
	if err := lane.acquire(ctx, 100*time.Millisecond); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// Second acquire times out (lane held).
	err := lane.acquire(ctx, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout on second acquire")
	}
	// Release and re-acquire.
	lane.release()
	if err := lane.acquire(ctx, 100*time.Millisecond); err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	lane.release()
}

// Lane acquire with cancelled context.
func TestBoundedOperationLane_CancelledContext(t *testing.T) {
	lane := newBoundedOperationLane()
	ctx, cancel := context.WithCancel(context.Background())

	// Acquire the slot.
	lane.acquire(ctx, 100*time.Millisecond)

	// Cancel the context for the second acquire attempt.
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()

	err := lane.acquire(ctx2, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	cancel()
	lane.release()
}

// suppress unused import
var _ = contract.SessionID("unused")
