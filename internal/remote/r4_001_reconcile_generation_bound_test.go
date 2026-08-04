package remote

// r4_001_reconcile_generation_bound_test.go — R4-001 (issue #2): a late/stale
// restart-failure reconcile (e.g. from a timeout-abandoned raw goroutine that
// later errors) MUST NOT clobber a newer successful run. ReconcileRestartFailure
// is bound to the exact runEpoch of the failed attempt: it only transitions the
// session to terminal if the session is STILL in that run's generation. A newer
// successful restart (higher runEpoch) makes the stale reconcile a no-op.

import (
	"context"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// currentRunEpoch reads the entry's current runEpoch under stateMu (test helper).
func currentRunEpoch(t *testing.T, rt *ControlRuntime, sid contract.SessionID) uint64 {
	t.Helper()
	e := rt.Arbiter().entryFor(sid)
	if e == nil {
		t.Fatalf("entry missing for %s", sid)
	}
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return e.runEpoch
}

// TestR4_001_LateReconcile_DoesNotClobberNewerRun proves the core invariant: a
// stale reconcile (bound to an older runEpoch) is a no-op when a newer run is
// current, while a reconcile bound to the current runEpoch still fires.
func TestR4_001_LateReconcile_DoesNotClobberNewerRun(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "r4-001-rc")
	adapter.Catalog().StoreRecipe("r4-001-rc", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	arb := adapter.Runtime().Arbiter()

	e1 := currentRunEpoch(t, adapter.Runtime(), "r4-001-rc")

	// A successful restart advances to a newer run generation (E2), running.
	if err := runRestart(t, adapter, "r4-001-rc"); err != nil {
		t.Fatalf("first restart: %v", err)
	}
	e2 := currentRunEpoch(t, adapter.Runtime(), "r4-001-rc")
	if e2 <= e1 {
		t.Fatalf("restart did not advance runEpoch: e1=%d e2=%d", e1, e2)
	}
	mirror, phase, _ := arb.SessionStateMirror("r4-001-rc")
	if mirror != contract.SessionStateRunning || phase != runActive {
		t.Fatalf("expected running after restart, got mirror=%s phase=%d", mirror, phase)
	}

	// Stale reconcile (bound to the OLD generation E1) MUST be a no-op: the
	// newer run E2 is current and running.
	adapter.Runtime().ReconcileRestartFailure("r4-001-rc", e1)
	mirror2, phase2, _ := arb.SessionStateMirror("r4-001-rc")
	if mirror2 != contract.SessionStateRunning || phase2 != runActive {
		t.Fatalf("R4-001 regression: stale reconcile (E1) clobbered the newer running run (E2): mirror=%s phase=%d", mirror2, phase2)
	}

	// A reconcile bound to the CURRENT generation (E2) still fires (the session
	// is still in E2) → terminal/unavailable.
	adapter.Runtime().ReconcileRestartFailure("r4-001-rc", e2)
	mirror3, phase3, _ := arb.SessionStateMirror("r4-001-rc")
	if phase3 != runTerminal {
		t.Fatalf("expected terminal after current-generation reconcile, got phase=%d", phase3)
	}
	if mirror3 == contract.SessionStateRunning {
		t.Fatal("expected non-running mirror after current-generation reconcile")
	}
}

// TestR4_001_Reconcile_NoEntryIsNoOp proves reconcile on a session with no entry
// (removed/unknown) is a safe no-op (no panic).
func TestR4_001_Reconcile_NoEntryIsNoOp(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	// No entry for this id — must not panic.
	adapter.Runtime().ReconcileRestartFailure("r4-001-ghost", 999)
}

// TestR4_001_Reconcile_RemovedEntryIsNoOp proves reconcile on a removed entry is
// a no-op.
func TestR4_001_Reconcile_RemovedEntryIsNoOp(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "r4-001-rm")
	e := currentRunEpoch(t, adapter.Runtime(), "r4-001-rm")
	// Remove the session entry.
	adapter.Runtime().RemoveDesktopSession(context.Background(), "r4-001-rm")
	// Reconcile must be a no-op on the removed entry.
	adapter.Runtime().ReconcileRestartFailure("r4-001-rm", e)
}
