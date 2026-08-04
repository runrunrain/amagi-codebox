package remote

// r4_001_staging_test.go — R4-001 issue #1: the restart transaction must NOT
// commit the boundary (or swap the run publicly) before StartProcess succeeds.
// On a start failure the session reverts to the pre-stage run, stays terminal,
// and NO restart boundary is published — so feed/ledger and the durable journal
// agree (failed). A subsequent recovery Stop+Restart succeeds.

import (
	"context"
	"sync"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// stageInspectLaunchRaw observes the control entry at the exact StartProcess
// call. The production restart effect runs in the gate's bounded goroutine, so
// observations are mutex-protected for -race.
type stageInspectLaunchRaw struct {
	mu sync.Mutex
	rt *ControlRuntime

	failNext       bool
	beforeReturn   func(*RunObservationPermit)
	startRunEpochs []uint64
	publicRuns     []*runIdentity
	publicEpochs   []uint64
	publicPhases   []runPhase
	backendStates  []backendHealth
}

func (f *stageInspectLaunchRaw) StartProcess(_ context.Context, _ contract.SessionID, _ RemoteLaunchRecipe, _ any, obs *RunObservationPermit) error {
	entry := obs.entry
	entry.stateMu.Lock()
	publicRun := entry.currentRun
	publicEpoch := entry.runEpoch
	publicPhase := entry.runPhase
	backend := entry.backend
	entry.stateMu.Unlock()

	f.mu.Lock()
	f.startRunEpochs = append(f.startRunEpochs, obs.runEpoch)
	f.publicRuns = append(f.publicRuns, publicRun)
	f.publicEpochs = append(f.publicEpochs, publicEpoch)
	f.publicPhases = append(f.publicPhases, publicPhase)
	f.backendStates = append(f.backendStates, backend)
	fail := f.failNext
	hook := f.beforeReturn
	f.failNext = false
	f.mu.Unlock()
	if hook != nil {
		hook(obs)
	}
	if fail {
		return closedTextError("injected start failure")
	}
	return nil
}

// TestR4_001_StartFailure_NoBoundaryRunReverted proves the core staging
// invariant: when StartProcess fails, the run identity is NOT left swapped, the
// entry is terminal, no new process is recorded, and no restart boundary is
// published to the stream.
func TestR4_001_StartFailure_NoBoundaryRunReverted(t *testing.T) {
	adapter, _, launchRaw, sessRaw := setupAdapterTest(t)
	activateTestSession(t, adapter, "r4-001-stage")
	adapter.Catalog().StoreRecipe("r4-001-stage", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	arb := adapter.Runtime().Arbiter()
	oldRun := arb.entryFor("r4-001-stage").currentRun

	// Stream bounds before the failed restart (a successful restart would pump a
	// boundary and advance latest).
	earliestBefore, latestBefore := adapter.Streams().SeqBounds("r4-001-stage")

	launchRaw.failNext = true
	baseStarts := len(launchRaw.started)
	err := runRestart(t, adapter, "r4-001-stage")
	if err == nil {
		t.Fatal("expected restart to fail when StartProcess fails")
	}
	if got := len(launchRaw.started); got != baseStarts {
		t.Fatalf("failed start should not record a started process: starts=%d want %d", got, baseStarts)
	}
	// R4-001#1: the run is NOT left swapped — the staged run was reverted on failure.
	if arb.entryFor("r4-001-stage").currentRun != oldRun {
		t.Fatal("R4-001#1 regression: run identity was left swapped after a start failure; the staged run must be reverted (no boundary)")
	}
	// Old process was stopped (step 2).
	if len(sessRaw.stopped) == 0 {
		t.Fatal("expected old process to be stopped before start failure")
	}
	assertReconciled(t, adapter, "r4-001-stage")

	// No restart boundary was published: the stream's latest seq is unchanged.
	_, latestAfter := adapter.Streams().SeqBounds("r4-001-stage")
	if latestAfter != latestBefore {
		t.Fatalf("R4-001#1: stream latest seq advanced after a failed restart (%d → %d); no boundary should be published. earliest=%d", latestBefore, latestAfter, earliestBefore)
	}
}

// TestR4_001_StageReservesIdentityWithoutPublishingPointer proves the missing
// stage/activate split directly at the StartProcess boundary: Start receives a
// freshly minted observation identity, while entry.currentRun/current epoch are
// still the old public identity. A failed staged epoch is consumed (never
// reused), and only the subsequent successful activate swaps the pointer.
func TestR4_001_StageReservesIdentityWithoutPublishingPointer(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	sid := contract.SessionID("r4-001-hidden-stage")
	activateTestSession(t, adapter, sid)
	adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	rt := adapter.Runtime()
	entry := rt.Arbiter().entryFor(sid)
	entry.stateMu.Lock()
	oldRun, oldEpoch := entry.currentRun, entry.runEpoch
	entry.stateMu.Unlock()

	launch := &stageInspectLaunchRaw{rt: rt, failNext: true}
	adapter.launchRaw = launch
	if err := runRestart(t, adapter, sid); err == nil {
		t.Fatal("expected injected start failure")
	}
	// A second attempt succeeds. Its reserved epoch must be greater than the
	// failed stage's epoch (no ABA reuse after rollback).
	if err := runRestart(t, adapter, sid); err != nil {
		t.Fatalf("second restart: %v", err)
	}

	launch.mu.Lock()
	defer launch.mu.Unlock()
	if len(launch.startRunEpochs) != 2 {
		t.Fatalf("StartProcess observations=%d, want 2", len(launch.startRunEpochs))
	}
	for i := range launch.startRunEpochs {
		if launch.publicRuns[i] != oldRun || launch.publicEpochs[i] != oldEpoch {
			t.Fatalf("attempt %d published staged run before StartProcess: public=(%p,%d) old=(%p,%d) stagedEpoch=%d", i+1, launch.publicRuns[i], launch.publicEpochs[i], oldRun, oldEpoch, launch.startRunEpochs[i])
		}
		if launch.publicPhases[i] != runStarting {
			t.Fatalf("attempt %d phase=%d at StartProcess, want hidden runStarting", i+1, launch.publicPhases[i])
		}
	}
	if launch.startRunEpochs[0] <= oldEpoch {
		t.Fatalf("first stage did not reserve a fresh epoch: old=%d staged=%d", oldEpoch, launch.startRunEpochs[0])
	}
	if launch.startRunEpochs[1] <= launch.startRunEpochs[0] {
		t.Fatalf("failed stage epoch was reused: first=%d second=%d", launch.startRunEpochs[0], launch.startRunEpochs[1])
	}
	entry.stateMu.Lock()
	newRun, newEpoch := entry.currentRun, entry.runEpoch
	entry.stateMu.Unlock()
	if newRun == oldRun || newEpoch != launch.startRunEpochs[1] {
		t.Fatalf("activate did not atomically publish the successful stage: runChanged=%v epoch=%d want=%d", newRun != oldRun, newEpoch, launch.startRunEpochs[1])
	}
}

// TestR4_001_StartFailureRollsBackExactFeedSeal proves rollback spans both H1
// feed state and the H3 seal tombstone: no boundary is written, and the exact
// failed transaction's seal is removed so recovery performs a fresh seal.
func TestR4_001_StartFailureRollsBackExactFeedSeal(t *testing.T) {
	adapter, _, launchRaw, _ := setupAdapterTest(t)
	sid := contract.SessionID("r4-001-seal-rollback")
	activateTestSession(t, adapter, sid)
	adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})

	launchRaw.failNext = true
	if err := runRestart(t, adapter, sid); err == nil {
		t.Fatal("expected injected start failure")
	}
	feed := adapter.Runtime().Committer().(*runSegmentCommitter).EnsureFeed(sid)
	feed.mu.Lock()
	sealed, segment := feed.sealed, feed.segmentID
	feed.mu.Unlock()
	if sealed {
		t.Fatal("failed restart left the feed seal committed; expected exact seal rollback")
	}
	ledger := adapter.Runtime().Hub().ledgerFor(sid)
	ledger.mu.Lock()
	_, causalSealed := ledger.sealedSegments[segment]
	ledger.mu.Unlock()
	if causalSealed {
		t.Fatal("failed restart left the causal-ledger seal tombstone committed")
	}
}

func assertRestartSealRolledBack(t *testing.T, rt *ControlRuntime, sid contract.SessionID) {
	t.Helper()
	feed := rt.Committer().(*runSegmentCommitter).EnsureFeed(sid)
	feed.mu.Lock()
	sealed, segment := feed.sealed, feed.segmentID
	feed.mu.Unlock()
	if sealed {
		t.Fatalf("feed remains sealed after failed restart (sid=%s)", sid)
	}
	ledger := rt.Hub().ledgerFor(sid)
	ledger.mu.Lock()
	_, causalSealed := ledger.sealedSegments[segment]
	ledger.mu.Unlock()
	if causalSealed {
		t.Fatalf("causal seal remains after failed restart (sid=%s)", sid)
	}
}

func TestR4_001_PreStageFailuresRollbackFeedAndLedgerSeal(t *testing.T) {
	t.Run("stop", func(t *testing.T) {
		adapter, _, _, sessRaw := setupAdapterTest(t)
		sid := contract.SessionID("r4-001-stop-rollback")
		activateTestSession(t, adapter, sid)
		adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
		sessRaw.failStop = true
		if err := runRestart(t, adapter, sid); err == nil {
			t.Fatal("expected stop failure")
		}
		assertRestartSealRolledBack(t, adapter.Runtime(), sid)
	})
	t.Run("resolve", func(t *testing.T) {
		adapter, _, _, _ := setupAdapterTest(t)
		sid := contract.SessionID("r4-001-resolve-rollback")
		activateTestSession(t, adapter, sid)
		adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
		resolver := &restartFailureResolver{}
		resolver.failNext = true
		adapter.resolver = resolver
		if err := runRestart(t, adapter, sid); err == nil {
			t.Fatal("expected resolve failure")
		}
		assertRestartSealRolledBack(t, adapter.Runtime(), sid)
	})
	t.Run("stage_epoch_reservation", func(t *testing.T) {
		adapter, _, _, _ := setupAdapterTest(t)
		sid := contract.SessionID("r4-001-stage-reserve-rollback")
		activateTestSession(t, adapter, sid)
		adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
		entry := adapter.Runtime().Arbiter().entryFor(sid)
		entry.stateMu.Lock()
		oldRun := entry.currentRun
		entry.runEpochSeq = ^uint64(0)
		entry.stateMu.Unlock()
		if err := runRestart(t, adapter, sid); err == nil {
			t.Fatal("expected stage epoch reservation failure")
		}
		entry.stateMu.Lock()
		currentRun := entry.currentRun
		entry.stateMu.Unlock()
		if currentRun != oldRun {
			t.Fatal("failed stage reservation published a run pointer")
		}
		assertRestartSealRolledBack(t, adapter.Runtime(), sid)
	})
}

func TestR4_001_StaleSealRollbackCannotUnsealNewerTransaction(t *testing.T) {
	committer, _, _ := newCommitterWithFake(t)
	arb, _, _, _, _ := newTestArbiter(t)
	sid := contract.SessionID("r4-001-seal-aba")
	permit, oldRun := startSessionForCommitter(t, arb, sid)
	committer.ActivateFirstSegment(permit, sid)

	first, err := committer.SealRestartSegment(&LifecycleIntentStub{id: 1, sessionID: sid}, oldRun, permit.runEpoch, sid)
	if err != nil {
		t.Fatalf("first seal: %v", err)
	}
	if !committer.RollbackRestartSegment(first, sid) {
		t.Fatal("first exact rollback failed")
	}
	second, err := committer.SealRestartSegment(&LifecycleIntentStub{id: 2, sessionID: sid}, oldRun, permit.runEpoch, sid)
	if err != nil {
		t.Fatalf("second seal: %v", err)
	}
	if committer.RollbackRestartSegment(first, sid) {
		t.Fatal("stale first receipt unsealed the newer transaction")
	}
	feed := committer.(*runSegmentCommitter).EnsureFeed(sid)
	feed.mu.Lock()
	stillSealed := feed.sealed
	feed.mu.Unlock()
	if !stillSealed {
		t.Fatal("newer seal was cleared by stale rollback")
	}
	if !committer.RollbackRestartSegment(second, sid) {
		t.Fatal("second exact rollback failed")
	}
}

// TestR4_001_ActivateFailureCompensatesProcessAndPublishesNothing injects a
// causal-ledger fault after StartProcess succeeds but before activate. The new
// process is compensated, currentRun stays old, and neither feed nor stream
// receives a restart boundary.
func TestR4_001_ActivateFailureCompensatesProcessAndPublishesNothing(t *testing.T) {
	adapter, _, _, sessRaw := setupAdapterTest(t)
	sid := contract.SessionID("r4-001-activate-fail")
	activateTestSession(t, adapter, sid)
	adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	rt := adapter.Runtime()
	entry := rt.Arbiter().entryFor(sid)
	entry.stateMu.Lock()
	oldRun := entry.currentRun
	entry.stateMu.Unlock()
	_, latestBefore := adapter.Streams().SeqBounds(sid)

	launch := &stageInspectLaunchRaw{rt: rt}
	launch.beforeReturn = func(*RunObservationPermit) {
		ledger := rt.Hub().ledgerFor(sid)
		ledger.mu.Lock()
		ledger.faulted = true
		ledger.mu.Unlock()
	}
	adapter.launchRaw = launch
	baseStops := len(sessRaw.stopped)
	if err := runRestart(t, adapter, sid); err == nil {
		t.Fatal("expected activate to fail after injected causal fault")
	}
	if got := len(sessRaw.stopped); got != baseStops+2 {
		t.Fatalf("stop calls=%d want %d (old stop + new-process compensation)", got, baseStops+2)
	}
	entry.stateMu.Lock()
	currentRun, phase, mirror := entry.currentRun, entry.runPhase, entry.stateMirror
	entry.stateMu.Unlock()
	if currentRun != oldRun || phase != runTerminal || mirror != contract.SessionStateUnavailable {
		t.Fatalf("activate failure state mismatch: oldRun=%v phase=%d mirror=%s", currentRun == oldRun, phase, mirror)
	}
	_, latestAfter := adapter.Streams().SeqBounds(sid)
	if latestAfter != latestBefore {
		t.Fatalf("activate failure published stream boundary: %d -> %d", latestBefore, latestAfter)
	}
	feed := rt.Committer().(*runSegmentCommitter).EnsureFeed(sid)
	feed.mu.Lock()
	segment, sealed := feed.segmentID, feed.sealed
	feed.mu.Unlock()
	if segment != 1 || sealed {
		t.Fatalf("activate failure left feed transaction committed: segment=%d sealed=%v", segment, sealed)
	}
}

// TestR4_001_ProductionStartFailureJournalFeedLedgerAgree drives the public
// device RestartSession path and checks all four outcome stores: journal=failed,
// current pointer=old terminal, no stream/feed boundary, no causal seal residue.
func TestR4_001_ProductionStartFailureJournalFeedLedgerAgree(t *testing.T) {
	adapter, _, launchRaw, _ := setupAdapterTest(t)
	sid := contract.SessionID("r4-001-journal-consistency")
	activateTestSession(t, adapter, sid)
	adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	rt := adapter.Runtime()
	principal := newTestDevicePrincipal("r4-device", "R4 Device")
	h, _, _, gErr := rt.AttachControl(principal, "r4-conn", sid, nil)
	if gErr != nil {
		t.Fatalf("attach: %v", gErr)
	}
	if _, err := rt.Gate().Acquire(context.Background(), principal, h.Lease(), sid); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	entry := rt.Arbiter().entryFor(sid)
	entry.stateMu.Lock()
	oldRun := entry.currentRun
	entry.stateMu.Unlock()
	_, latestBefore := adapter.Streams().SeqBounds(sid)

	launchRaw.failNext = true
	if _, aerr := adapter.RestartSession(context.Background(), "r4-request", principal, sid); aerr == nil {
		t.Fatal("expected production restart start failure")
	}
	entry.stateMu.Lock()
	currentRun, phase, mirror := entry.currentRun, entry.runPhase, entry.stateMirror
	entry.stateMu.Unlock()
	if currentRun != oldRun || phase != runTerminal || mirror != contract.SessionStateUnavailable {
		t.Fatalf("control state disagrees with failed restart: oldRun=%v phase=%d mirror=%s", currentRun == oldRun, phase, mirror)
	}
	_, latestAfter := adapter.Streams().SeqBounds(sid)
	if latestAfter != latestBefore {
		t.Fatalf("failed journal operation published a boundary: %d -> %d", latestBefore, latestAfter)
	}
	feed := rt.Committer().(*runSegmentCommitter).EnsureFeed(sid)
	feed.mu.Lock()
	segment, sealed := feed.segmentID, feed.sealed
	feed.mu.Unlock()
	if segment != 1 || sealed {
		t.Fatalf("feed disagrees with failed journal: segment=%d sealed=%v", segment, sealed)
	}
	ledger := rt.Hub().ledgerFor(sid)
	ledger.mu.Lock()
	_, sealLeft := ledger.sealedSegments[segment]
	ledger.mu.Unlock()
	if sealLeft {
		t.Fatal("causal ledger retained failed restart seal")
	}
	records, err := adapter.Journal().ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("journal read: %v", err)
	}
	var failed bool
	for _, rec := range records {
		if rec.SessionID == sid && rec.Operation == SessionOpRestart && rec.Phase == SessionPhaseResult && rec.Outcome == SessionOutcomeFailed {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatal("journal missing failed restart result")
	}
}

// TestR4_001_StartFailure_RecoveryStopThenRestartSucceeds proves that after a
// start-failure leaves the session terminal with NO boundary, the user can
// recover via Stop then Restart (the feed's already-sealed segment is tolerated
// by the recovery seal), and the session presents running again with exactly one
// boundary pumped.
func TestR4_001_StartFailure_RecoveryStopThenRestartSucceeds(t *testing.T) {
	adapter, _, launchRaw, sessRaw := setupAdapterTest(t)
	activateTestSession(t, adapter, "r4-001-stage-rec")
	adapter.Catalog().StoreRecipe("r4-001-stage-rec", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})

	// Drive a start failure → terminal, no boundary.
	launchRaw.failNext = true
	if err := runRestart(t, adapter, "r4-001-stage-rec"); err == nil {
		t.Fatal("expected first restart to fail (start failure)")
	}
	assertReconciled(t, adapter, "r4-001-stage-rec")

	// Recovery step 1: Stop (lifecycle allowed on terminal runPhase).
	_, stopErr := adapter.Runtime().Gate().DoDesktopLifecycle(context.Background(), adapter.Runtime().DesktopAuthority(), "r4-001-stage-rec", LifecycleStop,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			if err := p.Checkpoint(ctx, 1); err != nil {
				return SessionMutationResult{}, err
			}
			if err := sessRaw.StopSession(ctx, "r4-001-stage-rec"); err != nil {
				return SessionMutationResult{}, err
			}
			return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
		})
	if stopErr != nil {
		t.Fatalf("recovery Stop after failed restart: %v", stopErr)
	}

	// Recovery step 2: Restart succeeds (the feed was left sealed by the failed
	// restart; the recovery seal tolerates already-sealed).
	launchRaw.failNext = false
	baseStarts := len(launchRaw.started)
	_, latestBefore := adapter.Streams().SeqBounds("r4-001-stage-rec")
	if err := runRestart(t, adapter, "r4-001-stage-rec"); err != nil {
		t.Fatalf("recovery Restart should succeed after Stop, got %v", err)
	}
	if got := len(launchRaw.started); got != baseStarts+1 {
		t.Fatalf("recovery restart should start a process: starts=%d want %d", got, baseStarts+1)
	}
	// Session presents running again.
	mirror, phase, _ := adapter.Runtime().Arbiter().SessionStateMirror("r4-001-stage-rec")
	if mirror != contract.SessionStateRunning || phase != runActive {
		t.Fatalf("expected running after recovery restart, got mirror=%s phase=%d", mirror, phase)
	}
	// Exactly one boundary pumped by the recovery restart (latest advanced).
	_, latestAfter := adapter.Streams().SeqBounds("r4-001-stage-rec")
	if latestAfter <= latestBefore {
		t.Fatalf("recovery restart did not pump a boundary: latest %d → %d", latestBefore, latestAfter)
	}
}

// TestR4_001_SuccessfulRestart_PublishesBoundaryOnce proves the success path is
// unchanged: a successful restart swaps the run, sets running, and pumps exactly
// one boundary.
func TestR4_001_SuccessfulRestart_PublishesBoundaryOnce(t *testing.T) {
	adapter, _, launchRaw, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "r4-001-stage-ok")
	adapter.Catalog().StoreRecipe("r4-001-stage-ok", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	arb := adapter.Runtime().Arbiter()
	oldRun := arb.entryFor("r4-001-stage-ok").currentRun
	_, latestBefore := adapter.Streams().SeqBounds("r4-001-stage-ok")

	if err := runRestart(t, adapter, "r4-001-stage-ok"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if arb.entryFor("r4-001-stage-ok").currentRun == oldRun {
		t.Fatal("expected run identity to be swapped on a successful restart")
	}
	mirror, phase, _ := arb.SessionStateMirror("r4-001-stage-ok")
	if mirror != contract.SessionStateRunning || phase != runActive {
		t.Fatalf("expected running, got mirror=%s phase=%d", mirror, phase)
	}
	_, latestAfter := adapter.Streams().SeqBounds("r4-001-stage-ok")
	if latestAfter <= latestBefore {
		t.Fatal("successful restart did not pump a boundary")
	}
	if len(launchRaw.started) == 0 {
		t.Fatal("successful restart did not start a process")
	}
}
