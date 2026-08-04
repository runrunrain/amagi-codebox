package remote

// R5-001 regression: a hidden restart stage must retain observations produced
// by the new process before StartProcess returns. Successful activation commits
// the boundary first and then staged output in source order. A pre-activate exit
// is a terminal latch: the restart must fail rather than publish a permanently
// running generation.

import (
	"fmt"
	"runtime"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

func TestR5_001_PreActivateOutputPreservedInSourceOrder(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	sid := contract.SessionID("r5-001-pre-activate-output")
	activateTestSession(t, adapter, sid)
	adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})

	rt := adapter.Runtime()
	launch := &stageInspectLaunchRaw{rt: rt}
	launch.beforeReturn = func(obs *RunObservationPermit) {
		// This is the production timing the R5 report identified: PTY readLoop can
		// synchronously offer output before StartResolvedWithRun returns.
		rt.Projector().OfferOutput(obs, 1, []byte("early-one"))
		rt.Projector().OfferOutput(obs, 2, []byte("early-two"))
	}
	adapter.launchRaw = launch

	if err := runRestart(t, adapter, sid); err != nil {
		t.Fatalf("restart with pre-activate output: %v", err)
	}

	snap, _, err := rt.Feed().SnapshotAndSubscribe(sid)
	if err != nil {
		t.Fatalf("feed snapshot: %v", err)
	}
	if len(snap.Records) != 3 {
		t.Fatalf("pre-activate output was lost: records=%d want boundary+2 outputs; records=%+v", len(snap.Records), snap.Records)
	}
	if snap.Records[0].Kind != LiveRecordRestartBoundary {
		t.Fatalf("first new-segment record=%s want restart boundary", snap.Records[0].Kind)
	}
	for i, want := range []string{"early-one", "early-two"} {
		rec := snap.Records[i+1]
		if rec.Kind != LiveRecordOutput || string(rec.Output) != want {
			t.Fatalf("record[%d]=(%s,%q) want output %q", i+1, rec.Kind, rec.Output, want)
		}
		if rec.SourceOrdinal != RunSourceOrdinal(i+2) {
			t.Fatalf("record[%d] source ordinal=%d want %d", i+1, rec.SourceOrdinal, i+2)
		}
	}
	frames := adapter.Streams().FramesAfter(sid, nil)
	if len(frames) != 3 || frames[0].kind != LiveRecordRestartBoundary ||
		frames[1].kind != LiveRecordOutput || string(frames[1].output) != "early-one" ||
		frames[2].kind != LiveRecordOutput || string(frames[2].output) != "early-two" {
		t.Fatalf("remote replay order must be boundary→early-one→early-two, got %+v", frames)
	}
}

func TestR5_001_InstantPreActivateExitFailsRestartWithoutRunningPublication(t *testing.T) {
	adapter, _, _, sessRaw := setupAdapterTest(t)
	sid := contract.SessionID("r5-001-instant-exit")
	activateTestSession(t, adapter, sid)
	adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})

	rt := adapter.Runtime()
	entry := rt.Arbiter().entryFor(sid)
	entry.stateMu.Lock()
	oldRun := entry.currentRun
	entry.stateMu.Unlock()
	_, latestBefore := adapter.Streams().SeqBounds(sid)
	baseStops := len(sessRaw.stopped)

	launch := &stageInspectLaunchRaw{rt: rt}
	launch.beforeReturn = func(obs *RunObservationPermit) {
		// Model the real wait goroutine: the process exits on another goroutine,
		// but its sole exit callback completes before StartProcess returns.
		done := make(chan struct{})
		go func() {
			rt.Projector().OfferOutput(obs, 1, []byte("last-output"))
			rt.Projector().OfferExit(obs, 17, true)
			close(done)
		}()
		<-done
	}
	adapter.launchRaw = launch

	if err := runRestart(t, adapter, sid); err == nil {
		t.Fatal("restart succeeded after the staged process had already exited; want transaction failure")
	}
	entry.stateMu.Lock()
	currentRun, phase, mirror := entry.currentRun, entry.runPhase, entry.stateMirror
	entry.stateMu.Unlock()
	if currentRun != oldRun || phase != runTerminal || mirror == contract.SessionStateRunning {
		t.Fatalf("instant exit was overwritten by running publication: oldRun=%v phase=%d mirror=%s", currentRun == oldRun, phase, mirror)
	}
	_, latestAfter := adapter.Streams().SeqBounds(sid)
	if latestAfter != latestBefore {
		t.Fatalf("failed instant-exit restart published a boundary: latest %d -> %d", latestBefore, latestAfter)
	}
	if got := len(sessRaw.stopped); got != baseStops+2 {
		t.Fatalf("stop calls=%d want %d (old stop + staged-process compensation)", got, baseStops+2)
	}
}

func TestR5_001_ExitVsActivateRaceNeverLeavesRunning(t *testing.T) {
	for i := 0; i < 50; i++ {
		adapter, _, _, _ := setupAdapterTest(t)
		sid := contract.SessionID(fmt.Sprintf("r5-001-exit-race-%d", i))
		activateTestSession(t, adapter, sid)
		adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
		rt := adapter.Runtime()
		exitDone := make(chan struct{})
		launch := &stageInspectLaunchRaw{rt: rt}
		launch.beforeReturn = func(obs *RunObservationPermit) {
			go func() {
				runtime.Gosched()
				rt.Projector().OfferExit(obs, 0, false)
				close(exitDone)
			}()
		}
		adapter.launchRaw = launch

		// Both outcomes are valid: exit-before-activate fails the transaction;
		// activate-before-exit succeeds momentarily then the exit terminalizes it.
		_ = runRestart(t, adapter, sid)
		<-exitDone
		mirror, phase, ok := rt.Arbiter().SessionStateMirror(sid)
		if !ok || phase != runTerminal || mirror == contract.SessionStateRunning {
			t.Fatalf("iteration %d left raced restart running: ok=%v phase=%d mirror=%s", i, ok, phase, mirror)
		}
	}
}

func TestR5_001_ActivateFailureAbortsBufferedObservations(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	sid := contract.SessionID("r5-001-stage-abort")
	activateTestSession(t, adapter, sid)
	adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})

	rt := adapter.Runtime()
	launch := &stageInspectLaunchRaw{rt: rt}
	launch.beforeReturn = func(obs *RunObservationPermit) {
		rt.Projector().OfferOutput(obs, 1, []byte("must-not-leak"))
		ledger := rt.Hub().ledgerFor(sid)
		ledger.mu.Lock()
		ledger.faulted = true
		ledger.mu.Unlock()
	}
	adapter.launchRaw = launch

	if err := runRestart(t, adapter, sid); err == nil {
		t.Fatal("expected activate failure after injected causal fault")
	}
	recorder := rt.Committer().(*runSegmentCommitter).recorder.(*countingOutcomeRecorder)
	if got := recorder.Count(ObservationStaged); got != 1 {
		t.Fatalf("buffered observation count=%d want 1", got)
	}
	if got := recorder.StageCount(StageAborted); got != 1 {
		t.Fatalf("aborted stage count=%d want 1", got)
	}
	snap, _, err := rt.Feed().SnapshotAndSubscribe(sid)
	if err != nil {
		t.Fatalf("feed snapshot: %v", err)
	}
	for _, rec := range snap.Records {
		if string(rec.Output) == "must-not-leak" {
			t.Fatal("activate-failure leaked a staged output into the old segment")
		}
	}
}

// Compile-time guard: the production projector remains the PTY callback sink
// exercised by these tests rather than a test-only observation port.
var _ interface {
	OfferOutput(any, uint64, []byte)
	OfferExit(any, uint32, bool)
} = (*RunEventProjector)(nil)
