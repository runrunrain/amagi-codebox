package remote

// r4_004_quarantine_lifecycle_gating_test.go — R4-004 part 2: a quarantined
// backend (mid-syscall timeout, unknown detach state) must DENY remote DEVICE
// lifecycle (stop/restart/remove via REST) — a remote holder must not operate on
// a backend whose handle may still be open. Trusted DESKTOP recovery (the Wails
// root) stays allowed so the user can clean up (R3-004 preserved).

import (
	"context"
	"errors"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// TestR4_004_DeviceLifecycleDeniedOnQuarantine_DesktopRecoveryOK proves the
// asymmetric gating: after a hanging DATA write quarantines the backend, a
// remote device restart is DENIED, while a trusted desktop Stop still succeeds.
func TestR4_004_DeviceLifecycleDeniedOnQuarantine_DesktopRecoveryOK(t *testing.T) {
	clk := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clk, nil)
	raw := &hangingPTYRaw{release: make(chan struct{})}
	rt.SetPTYRawPort(raw)
	lifecycle := &recoveryLifecyclePort{}
	rt.SetPTYLifecycleRawPort(lifecycle)
	rt.MarkReady()

	sid := startPublicSession(t, rt)
	// Ensure the hanging goroutine is always released (no leak across -count).
	t.Cleanup(func() { raw.DetachSession(string(sid)) })

	// Device attaches + acquires → becomes the holder.
	p := newTestDevicePrincipal("devQ", "Dev Q")
	h, _, _, gErr := rt.AttachControl(p, "connQ", sid, nil)
	if gErr != nil {
		t.Fatalf("AttachControl: %v", gErr)
	}
	if _, err := rt.Gate().Acquire(context.Background(), p, h.Lease(), sid); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Quarantine the backend via a hanging device DATA write (times out → quarantine).
	start := time.Now()
	werr := rt.Gate().DoDevicePTY(context.Background(), h.Lease(), sid, PTYInput, func(ctx context.Context, permit *operationPermit) error {
		return raw.WriteRaw(ctx, string(sid), []byte("hang"))
	})
	if elapsed := time.Since(start); elapsed > controlRawEffectTimeout+2*time.Second {
		t.Fatalf("hanging write blocked far past the budget: %v", elapsed)
	}
	var ge *ControlGateError
	if !errors.As(werr, &ge) || ge.Kind != DenyOperationTimeout {
		t.Fatalf("expected DenyOperationTimeout from the hanging write (quarantine), got %v", werr)
	}

	// R4-004: remote DEVICE lifecycle (restart) on the quarantined backend → DENIED.
	_, derr := rt.Gate().DoDeviceLifecycle(context.Background(), p, sid, LifecycleRestart,
		func(ctx context.Context, permit *operationPermit) (SessionMutationResult, error) {
			t.Fatal("R4-004: device restart raw effect must not run on a quarantined backend")
			return SessionMutationResult{}, nil
		})
	var ge2 *ControlGateError
	if !errors.As(derr, &ge2) || ge2.Kind != DenyControlUnavailable {
		t.Fatalf("R4-004: expected device lifecycle DENIED (DenyControlUnavailable) on quarantined backend, got %v", derr)
	}

	// Trusted DESKTOP recovery Stop on the SAME quarantined backend → ALLOWED (R3-004 preserved).
	_, stopErr := rt.Gate().DoDesktopLifecycle(context.Background(), rt.DesktopAuthority(), sid, LifecycleStop,
		func(ctx context.Context, permit *operationPermit) (SessionMutationResult, error) {
			if err := permit.Checkpoint(ctx, 1); err != nil {
				return SessionMutationResult{}, err
			}
			if err := lifecycle.CloseSession(sid); err != nil {
				return SessionMutationResult{}, err
			}
			return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
		})
	if stopErr != nil {
		t.Fatalf("R4-004: desktop recovery Stop on quarantined backend should succeed, got %v", stopErr)
	}
}
