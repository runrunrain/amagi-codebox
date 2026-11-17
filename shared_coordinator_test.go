package main

// shared_coordinator_test.go — M-006 regression: shared-service mutations
// consult the SharedServiceCoordinator and are rejected while active run leases
// exist (PG-06 confirm/reject, not warn-and-force-stop).

import (
	"context"
	"testing"

	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
)

// acquireTestLease begins a desktop run and acquires one shared-service lease,
// returning a release function. Uses a fixed (non-secret) fingerprint so the
// test does not depend on any concrete service being wired.
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

// TestM006_CredentialMutationRejectedWithLease proves the coordinator guard the
// credentials mutations rely on: a Stop/Start mutation is rejected while a
// ClaudeCredentials lease is active.
func TestM006_CredentialMutationRejectedWithLease(t *testing.T) {
	app := newTestApp(t)
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	release := acquireTestLease(t, app, remote.SharedServiceClaudeCredentials)
	defer release()

	var fp [32]byte
	fp[0] = byte(remote.SharedServiceClaudeCredentials)
	if err := app.sharedCoord.CheckMutation(remote.SharedServiceClaudeCredentials, remote.MutationStop, fp); err != remote.ErrSharedServiceInUse {
		t.Fatalf("CheckMutation(Stop) with active lease: got %v, want ErrSharedServiceInUse", err)
	}
	if err := app.sharedCoord.CheckMutation(remote.SharedServiceClaudeCredentials, remote.MutationStartDifferentConfig, fp); err != remote.ErrSharedServiceInUse {
		t.Fatalf("CheckMutation(Start) with active lease: got %v, want ErrSharedServiceInUse", err)
	}
}
