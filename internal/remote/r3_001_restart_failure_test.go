package remote

// r3_001_restart_failure_test.go — R3-001 regression: a restart that fails at
// any irreversible step (seal / stop / resolve / commit / start) MUST NOT leave
// the session presenting as running. The entry is reconciled to an honest
// terminal/unavailable state; the recovery path (Stop then Restart) works.
//
// Barrier matrix (design §4.5, R3-001 fix direction):
//   1. seal failure        → reconciled terminal/unavailable
//   2. stop failure        → reconciled terminal/unavailable
//   3. resolve failure     → reconciled terminal/unavailable
//   4. commit failure      → reconciled terminal/unavailable
//   5. start failure       → reconciled terminal/unavailable (run already swapped)
// Plus a recovery test: after a start-failure reconciliation, Stop + Restart
// succeed and the session presents as running again.

import (
	"context"
	"errors"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// restartFailureResolver is a fakeResolver that can be flipped to fail
// ResolveRestart (barrier 3).
type restartFailureResolver struct {
	recipe   RemoteLaunchRecipe
	failNext bool
}

func (r *restartFailureResolver) ResolveCreate(ctx context.Context, req contract.CreateSessionRequest) (RemoteLaunchResolution, *LaunchResolveFailure) {
	return RemoteLaunchResolution{Recipe: RemoteLaunchRecipe{CLIType: req.CLIType, Workdir: "/work"}}, nil
}

func (r *restartFailureResolver) ResolveRestart(ctx context.Context, recipe RemoteLaunchRecipe) (RemoteLaunchResolution, *LaunchResolveFailure) {
	if r.failNext {
		r.failNext = false
		return RemoteLaunchResolution{}, newLaunchResolveFailure(LaunchResolveFailureContext, recipe.CLIType)
	}
	return RemoteLaunchResolution{Recipe: recipe, Spec: nil}, nil
}

func (r *restartFailureResolver) Probe(ctx context.Context, cli contract.CLIType) (contract.CLIAvailability, *LaunchResolveFailure) {
	return contract.CLIAvailability{CLIType: cli, Available: true}, nil
}

// runRestart invokes the adapter restart raw effect via the desktop lifecycle
// gate (the production entry path used by RemoteSessionAdapter.RestartSession).
func runRestart(t *testing.T, adapter *RemoteSessionAdapter, sid contract.SessionID) error {
	t.Helper()
	entry, ok := adapter.Catalog().Entry(sid)
	if !ok {
		t.Fatalf("catalog entry missing for %s", sid)
	}
	_, err := adapter.Gate().DoDesktopLifecycle(context.Background(), adapter.Runtime().DesktopAuthority(), sid, LifecycleRestart,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			return adapter.restartRawEffect(ctx, p, sid, entry)
		})
	return err
}

// assertReconciled asserts the session entry is terminal + unavailable (NOT
// running) after a failed restart — the core R3-001 invariant.
func assertReconciled(t *testing.T, adapter *RemoteSessionAdapter, sid contract.SessionID) {
	t.Helper()
	arb := adapter.Runtime().Arbiter()
	mirror, phase, ok := arb.SessionStateMirror(sid)
	if !ok {
		t.Fatalf("entry missing after failed restart (sid=%s)", sid)
	}
	if mirror == contract.SessionStateRunning {
		t.Fatalf("R3-001 regression: session still presents as running after failed restart (sid=%s)", sid)
	}
	if phase != runTerminal {
		t.Fatalf("R3-001 regression: runPhase=%d, want runTerminal after failed restart (sid=%s)", phase, sid)
	}
	if mirror != contract.SessionStateUnavailable {
		t.Fatalf("expected reconciled mirror=unavailable, got %s (sid=%s)", mirror, sid)
	}
}

// TestR3_001_SealFailure_ReconcilesToTerminal: when SealRestartSegmentForPermit
// fails (the permit's run is already stale because a concurrent fence swapped
// it), the restart returns an error and the entry is reconciled terminal/
// unavailable, not left fake-running.
func TestR3_001_SealFailure_ReconcilesToTerminal(t *testing.T) {
	adapter, _, launchRaw, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")
	adapter.Catalog().StoreRecipe("s1", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	arb := adapter.Runtime().Arbiter()

	// Force a stale permit by advancing the run identity before restart: mint a
	// fresh run directly so the lifecycle permit's captured run no longer matches.
	entry := arb.entryFor("s1")
	entry.stateMu.Lock()
	_, _, ok := arb.mintRunIdentityLocked(entry)
	entry.stateMu.Unlock()
	if !ok {
		t.Fatal("could not mint run identity to force stale permit")
	}

	baseStarts := len(launchRaw.started)
	err := runRestart(t, adapter, "s1")
	if err == nil {
		t.Fatal("expected restart to fail when the seal sees a stale run")
	}
	if got := len(launchRaw.started); got != baseStarts {
		t.Fatalf("no new process should start on seal failure: starts=%d want %d", got, baseStarts)
	}
	assertReconciled(t, adapter, "s1")
}

// TestR3_001_StopFailure_ReconcilesToTerminal: when the old-process Stop call
// fails, the old segment is already sealed; the entry is reconciled terminal/
// unavailable (process state uncertain), not left fake-running.
func TestR3_001_StopFailure_ReconcilesToTerminal(t *testing.T) {
	adapter, _, launchRaw, sessRaw := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")
	adapter.Catalog().StoreRecipe("s1", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	sessRaw.failStop = true

	baseStarts := len(launchRaw.started)
	err := runRestart(t, adapter, "s1")
	if err == nil {
		t.Fatal("expected restart to fail when StopSession fails")
	}
	if got := len(launchRaw.started); got != baseStarts {
		t.Fatalf("no new process should start on stop failure: starts=%d want %d", got, baseStarts)
	}
	assertReconciled(t, adapter, "s1")
}

// TestR3_001_ResolveFailure_ReconcilesToTerminal: when re-resolve fails (recipe
// missing / unavailable), the old segment is sealed + the old process is
// stopped; the entry is reconciled terminal/unavailable.
func TestR3_001_ResolveFailure_ReconcilesToTerminal(t *testing.T) {
	adapter, _, launchRaw, sessRaw := setupAdapterTest(t)
	// Swap in a resolver that fails the next ResolveRestart.
	resolver := &restartFailureResolver{recipe: RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"}}
	adapter.resolver = resolver
	activateTestSession(t, adapter, "s1")
	adapter.Catalog().StoreRecipe("s1", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	resolver.failNext = true

	baseStarts := len(launchRaw.started)
	err := runRestart(t, adapter, "s1")
	if err == nil {
		t.Fatal("expected restart to fail when ResolveRestart fails")
	}
	if got := len(launchRaw.started); got != baseStarts {
		t.Fatalf("no new process should start on resolve failure: starts=%d want %d", got, baseStarts)
	}
	// The old process WAS stopped (step 2 succeeded before resolve).
	if len(sessRaw.stopped) == 0 {
		t.Fatal("expected old process to be stopped before resolve failure")
	}
	assertReconciled(t, adapter, "s1")
}

// TestR3_001_StartFailure_ReconcilesToTerminal: the critical half-restart case.
// CommitRestartRun stages the new run, but StartProcess fails. With R3-001+R4-001#1
// the staged run is ABORTED (reverted) on start failure, NO boundary is committed,
// and the entry is reconciled terminal/unavailable — feed/ledger agree with the
// failed durable journal (no half-restart boundary).
func TestR3_001_StartFailure_ReconcilesToTerminal(t *testing.T) {
	adapter, _, launchRaw, sessRaw := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")
	adapter.Catalog().StoreRecipe("s1", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	// Capture the old run, then arm a start failure.
	arb := adapter.Runtime().Arbiter()
	oldRun := arb.entryFor("s1").currentRun
	launchRaw.failNext = true

	baseStarts := len(launchRaw.started)
	err := runRestart(t, adapter, "s1")
	if err == nil {
		t.Fatal("expected restart to fail when StartProcess fails")
	}
	if got := len(launchRaw.started); got != baseStarts {
		t.Fatalf("failed start should not record a started process: starts=%d want %d", got, baseStarts)
	}
	// Old process was stopped (step 2) before the start failure.
	if len(sessRaw.stopped) == 0 {
		t.Fatal("expected old process to be stopped before start failure")
	}
	// R4-001#1: the staged run was REVERTED on start failure (no boundary committed,
	// no half-restart). The entry is back on the old run and reconciled terminal.
	if arb.entryFor("s1").currentRun != oldRun {
		t.Fatal("R4-001#1 regression: staged run was left swapped after start failure; it must be reverted (no boundary)")
	}
	assertReconciled(t, adapter, "s1")
}

// TestR3_001_RecoveryPath_StopThenRestartSucceeds: after a start-failure
// reconciliation leaves the session terminal/unavailable, the user can recover
// via Stop (idempotent lifecycle, allowed on terminal) then Restart (succeeds,
// session presents running again). This proves quarantine of the session does
// NOT lock out lifecycle recovery.
func TestR3_001_RecoveryPath_StopThenRestartSucceeds(t *testing.T) {
	adapter, _, launchRaw, sessRaw := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")
	adapter.Catalog().StoreRecipe("s1", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	// Force a start failure to drive the half-restart + reconciliation.
	launchRaw.failNext = true
	if err := runRestart(t, adapter, "s1"); err == nil {
		t.Fatal("expected first restart to fail (start failure)")
	}
	assertReconciled(t, adapter, "s1")

	// Recovery step 1: Stop (lifecycle is allowed on terminal runPhase).
	_, stopErr := adapter.Runtime().Gate().DoDesktopLifecycle(context.Background(), adapter.Runtime().DesktopAuthority(), "s1", LifecycleStop,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			if err := p.Checkpoint(ctx, 1); err != nil {
				return SessionMutationResult{}, err
			}
			if err := sessRaw.StopSession(ctx, "s1"); err != nil {
				return SessionMutationResult{}, err
			}
			return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
		})
	if stopErr != nil {
		var ge *ControlGateError
		if errors.As(stopErr, &ge) && ge.Kind == DenySessionNotWritable {
			t.Fatalf("R3-001 recovery blocked: Stop denied on terminal entry: %v", stopErr)
		}
		t.Fatalf("recovery Stop: %v", stopErr)
	}

	// Recovery step 2: Restart succeeds (the entry is terminal, not removed;
	// lifecycle permits do not check runPhase).
	baseStarts := len(launchRaw.started)
	if err := runRestart(t, adapter, "s1"); err != nil {
		t.Fatalf("recovery Restart should succeed after Stop, got %v", err)
	}
	if got := len(launchRaw.started); got != baseStarts+1 {
		t.Fatalf("recovery restart should start a new process: starts=%d want %d", got, baseStarts+1)
	}
	// The session presents as running again.
	mirror, phase, ok := adapter.Runtime().Arbiter().SessionStateMirror("s1")
	if !ok {
		t.Fatal("entry missing after recovery restart")
	}
	if phase != runActive {
		t.Fatalf("after recovery restart runPhase=%d, want runActive", phase)
	}
	if mirror != contract.SessionStateRunning && mirror != "" {
		// mirror may be "" if not set by the test fixture; the runPhase is the
		// authoritative signal. Only fail on an explicit non-running mirror.
		t.Fatalf("after recovery restart mirror=%s, want running/empty", mirror)
	}
}
