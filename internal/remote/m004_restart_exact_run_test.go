package remote

// M-004 (supplement): restart performs a real exact run transition — H1 seal
// the old segment → stop → re-resolve → mint exact new run → start the new
// process with a non-nil NEW RunObservationPermit → H1 CommitRestartSegment
// boundary. No nil permit, no manual AppendBoundary. Late old-run observations
// are dropped after the seal; new-run output commits to the new segment.

import (
	"context"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// TestM004_RestartExactRunTransition_NonNilPermit: restart mints a NEW run and
// starts the new process with a non-nil NEW RunObservationPermit (distinct
// pointer/epoch from the old run). The old process is stopped; the new segment
// boundary is committed via H1 (a boundary frame appears in the stream store
// without a manual AppendBoundary call).
func TestM004_RestartExactRunTransition_NonNilPermit(t *testing.T) {
	adapter, _, launchRaw, sessRaw := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")
	adapter.Catalog().StoreRecipe("s1", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	entry, _ := adapter.Catalog().Entry("s1")

	// Capture the old run identity before restart.
	oldRun := adapter.Runtime().Arbiter().entryFor("s1").currentRun

	baseStops := len(sessRaw.stopped)
	baseStarts := len(launchRaw.started)

	ctx := context.Background()
	result, err := adapter.Gate().DoDesktopLifecycle(ctx, adapter.Runtime().DesktopAuthority(), "s1", LifecycleRestart,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			return adapter.restartRawEffect(ctx, p, "s1", entry)
		})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if !result.RestartBoundary || result.State != contract.SessionStateRunning {
		t.Fatalf("expected running+boundary, got state=%s boundary=%v", result.State, result.RestartBoundary)
	}
	if len(sessRaw.stopped) != baseStops+1 {
		t.Fatalf("old process not stopped: stops=%d want %d", len(sessRaw.stopped), baseStops+1)
	}
	if len(launchRaw.started) != baseStarts+1 {
		t.Fatalf("new process not started: starts=%d want %d", len(launchRaw.started), baseStarts+1)
	}
	// M-004: the new process MUST receive a non-nil NEW permit (no nil-permit bypass).
	launchRaw.mu.Lock()
	newPermit := launchRaw.lastObsPermit
	launchRaw.mu.Unlock()
	if newPermit == nil {
		t.Fatal("M-004 regression: new process started with nil RunObservationPermit")
	}
	if newPermit.run == oldRun {
		t.Fatal("M-004 regression: restart did not mint a NEW run identity (same pointer as old)")
	}
	if newPermit.run == nil {
		t.Fatal("M-004 regression: new permit has nil run identity")
	}
	// The entry's current run is now the new run.
	curRun := adapter.Runtime().Arbiter().entryFor("s1").currentRun
	if curRun != newPermit.run {
		t.Fatal("M-004 regression: entry.currentRun != new permit run after restart")
	}
}

// TestM004_RestartSealsOldSegment_DropsLateOldRun: after restart, a late
// observation for the OLD run is dropped (ObservationDroppedStaleRun) — the H1
// seal + run swap fence it. New-run output commits to the new segment.
func TestM004_RestartSealsOldSegment_DropsLateOldRun(t *testing.T) {
	adapter, _, launchRaw, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")
	adapter.Catalog().StoreRecipe("s1", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	entry, _ := adapter.Catalog().Entry("s1")
	rt := adapter.Runtime()

	// Capture the old obs permit (for a late old-run observation after restart).
	arbEntry := rt.Arbiter().entryFor("s1")
	oldPermit := &RunObservationPermit{
		entry:        arbEntry,
		run:          arbEntry.currentRun,
		runEpoch:     arbEntry.runEpoch,
		backendEpoch: arbEntry.backendEpoch,
	}

	ctx := context.Background()
	if _, err := adapter.Gate().DoDesktopLifecycle(ctx, rt.DesktopAuthority(), "s1", LifecycleRestart,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			return adapter.restartRawEffect(ctx, p, "s1", entry)
		}); err != nil {
		t.Fatalf("restart: %v", err)
	}

	// Late OLD-run output: must be dropped (stale run after the swap).
	out := rt.Committer().CommitRunObservation(oldPermit, NewOutputObservation([]byte("late old")))
	if out.Disposition != ObservationDroppedStaleRun && out.Disposition != ObservationDroppedSegmentSealed {
		t.Fatalf("M-004: late old-run observation should be dropped, got %s", out.Disposition)
	}

	// New-run output commits to the new segment (segment 2).
	launchRaw.mu.Lock()
	newPermit := launchRaw.lastObsPermit
	launchRaw.mu.Unlock()
	if newPermit == nil {
		t.Fatal("restart did not capture a new permit")
	}
	out2 := rt.Committer().CommitRunObservation(newPermit, NewOutputObservation([]byte("new run")))
	if out2.Disposition != ObservationCommitted {
		t.Fatalf("M-004: new-run output should commit, got %s", out2.Disposition)
	}
	if out2.SegmentID != 2 {
		t.Fatalf("M-004: new-run output should be in segment 2, got %d", out2.SegmentID)
	}
}
