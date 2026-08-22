package main

// shared_coordinator_test.go — M-006 regression: shared-service mutations
// (Codex headroom toggle, uninstall) consult the SharedServiceCoordinator and are
// rejected while active run leases exist (PG-06 confirm/reject, not warn-and-
// force-stop). Proves the guard wiring the toggle/uninstall rely on.

import (
	"context"
	"errors"
	"testing"

	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
)

// acquireTestLease begins a desktop run and acquires one shared-service lease,
// returning a release function. Uses a fixed (non-secret) fingerprint so the
// test does not depend on the Headroom service being wired.
func acquireTestLease(t *testing.T, app *App, kind remote.SharedServiceKind) func() {
	t.Helper()
	rt := wireTestControl(t, app)
	launchPermit, runPermit, _, err := rt.BeginDesktopRun(context.Background(), contract.SessionID("m006-run"))
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	var fp [32]byte
	fp[0] = byte(kind)
	lease, err := app.sharedCoord.AcquireForRun(context.Background(), runPermit, kind, fp)
	if err != nil {
		t.Fatalf("AcquireForRun: %v", err)
	}
	return func() { _ = app.sharedCoord.ReleaseExact(context.Background(), lease) }
}

// TestM006_UninstallRejectedWhenHeadroomLeaseActive proves the uninstall stopper
// defers to the coordinator: with an active headroom lease it returns
// ErrSharedServiceInUse instead of warn-and-force-stop.
func TestM006_UninstallRejectedWhenHeadroomLeaseActive(t *testing.T) {
	app := newTestApp(t)
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	release := acquireTestLease(t, app, remote.SharedServiceClaudeHeadroom)
	defer release()

	if err, _ := app.stopAllHeadroomForUninstall(); !errors.Is(err, envcheck.ErrHeadroomInUse) {
		t.Fatalf("stopAllHeadroomForUninstall with active lease: got %v, want ErrHeadroomInUse", err)
	}
}

// TestM006_UninstallProceedsWhenNoLease proves the uninstall stopper proceeds
// (no rejection) when no leases are active.
func TestM006_UninstallProceedsWhenNoLease(t *testing.T) {
	app := newTestApp(t)
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	wireTestControl(t, app)
	// No headroom services wired → the stop iterates nil services and returns nil.
	if err, _ := app.stopAllHeadroomForUninstall(); err != nil {
		t.Fatalf("stopAllHeadroomForUninstall with no leases: got %v, want nil", err)
	}
}

// TestM006_CodexHeadroomMutationRejectedWithLease proves the coordinator guard
// the SetCodexGlobalHeadroom toggle relies on: a Stop/Start mutation is rejected
// while a Codex headroom lease is active.
func TestM006_CodexHeadroomMutationRejectedWithLease(t *testing.T) {
	app := newTestApp(t)
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	release := acquireTestLease(t, app, remote.SharedServiceCodexHeadroom)
	defer release()

	var fp [32]byte
	fp[0] = byte(remote.SharedServiceCodexHeadroom)
	if err := app.sharedCoord.CheckMutation(remote.SharedServiceCodexHeadroom, remote.MutationStop, fp); err != remote.ErrSharedServiceInUse {
		t.Fatalf("CheckMutation(Stop) with active lease: got %v, want ErrSharedServiceInUse", err)
	}
	if err := app.sharedCoord.CheckMutation(remote.SharedServiceCodexHeadroom, remote.MutationStartDifferentConfig, fp); err != remote.ErrSharedServiceInUse {
		t.Fatalf("CheckMutation(Start) with active lease: got %v, want ErrSharedServiceInUse", err)
	}
}
