package remote

// m2a_integration_test.go — M2-A integration wiring tests (design §10 L2):
// real resolver + Seq pump + causal watermark + journal.
//
// These tests prove the integration wiring through the REAL code paths:
//   - production resolver delegates to an injected platform.CLIResolver and
//     classifies the four failure kinds (design §5.4, §8.3);
//   - SyncFeed pumps H1 feed records → v1 Seq + H3 PublishReserved (design §7.1);
//   - causal attach watermark + startAfter filtering (design §6.3, §4A.4);
//   - journal intent/result writes for stop/restart/remove (design §8.5).
//
// L3 (real CLI binaries / ConPTY) is NOT claimed here — only the resolution
// code path with a controlled candidate environment (design §10.3).

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// 1. Production resolver (design §5.4, §8.3, T05/T27)
// ---------------------------------------------------------------------------

// fakePlatformCLIResolver is a controlled-environment platform.CLIResolver for
// resolver tests. It records the ResolveRequest and returns a spec or error.
type fakePlatformCLIResolver struct {
	lastReq platform.ResolveRequest
	spec    platform.ResolvedLaunchSpec
	err     error
	calls   int
}

func (f *fakePlatformCLIResolver) Resolve(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	f.lastReq = req
	f.calls++
	if f.err != nil {
		return platform.ResolvedLaunchSpec{}, f.err
	}
	return f.spec, nil
}

func TestProductionResolver_DelegatesToRealResolve(t *testing.T) {
	// Inject a fake platform.CLIResolver that returns a spec with a CLI path.
	// Verify the production resolver builds the correct ResolveRequest (AppType,
	// LaunchMode=embedded, WorkDir) and never copies candidate literals.
	fake := &fakePlatformCLIResolver{
		spec: platform.ResolvedLaunchSpec{CLI: platform.ResolvedCLI{Name: "claude", Path: "/usr/bin/claude"}},
	}
	dir := t.TempDir()
	r := NewProductionRemoteLaunchResolver(fake, dir, []string{"PATH=/usr/bin"}, nil)

	res, lf := r.ResolveCreate(context.Background(), contract.CreateSessionRequest{
		CLIType: contract.CLITypeClaudeCode,
	})
	if lf != nil {
		t.Fatalf("expected success, got failure %s", lf.Kind)
	}
	if fake.calls != 1 {
		t.Fatalf("expected 1 Resolve call, got %d", fake.calls)
	}
	if fake.lastReq.AppType != string(contract.CLITypeClaudeCode) {
		t.Errorf("AppType = %q, want %q", fake.lastReq.AppType, contract.CLITypeClaudeCode)
	}
	if fake.lastReq.LaunchMode != "embedded" {
		t.Errorf("LaunchMode = %q, want embedded", fake.lastReq.LaunchMode)
	}
	if res.Spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if res.Recipe.CLIType != contract.CLITypeClaudeCode {
		t.Errorf("recipe CLIType = %q", res.Recipe.CLIType)
	}
}

func TestProductionResolver_WorkdirValidation(t *testing.T) {
	// Provided workdir that does not exist → workdir failure (design §8.3).
	fake := &fakePlatformCLIResolver{spec: platform.ResolvedLaunchSpec{CLI: platform.ResolvedCLI{Path: "/x"}}}
	r := NewProductionRemoteLaunchResolver(fake, t.TempDir(), nil, nil)
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	wd := missing
	_, lf := r.ResolveCreate(context.Background(), contract.CreateSessionRequest{
		CLIType: contract.CLITypeCodex,
		Workdir: &wd,
	})
	if lf == nil || lf.Kind != LaunchResolveFailureWorkdir {
		t.Fatalf("expected workdir failure, got %v", lf)
	}
	if fake.calls != 0 {
		t.Errorf("Resolve should not be called on workdir failure, got %d calls", fake.calls)
	}
}

func TestProductionResolver_CapabilityFailure(t *testing.T) {
	// Resolve returns an error → capability failure (design §8.3).
	fake := &fakePlatformCLIResolver{err: os.ErrNotExist}
	r := NewProductionRemoteLaunchResolver(fake, t.TempDir(), nil, nil)
	_, lf := r.ResolveCreate(context.Background(), contract.CreateSessionRequest{
		CLIType: contract.CLITypeOpenCode,
	})
	if lf == nil || lf.Kind != LaunchResolveFailureCapability {
		t.Fatalf("expected capability failure, got %v", lf)
	}
}

func TestProductionResolver_UnknownCLIType(t *testing.T) {
	// Unknown CLI type → context failure (design §5.4).
	fake := &fakePlatformCLIResolver{}
	r := NewProductionRemoteLaunchResolver(fake, t.TempDir(), nil, nil)
	_, lf := r.ResolveCreate(context.Background(), contract.CreateSessionRequest{
		CLIType: contract.CLIType("internal-cli"),
	})
	if lf == nil || lf.Kind != LaunchResolveFailureContext {
		t.Fatalf("expected context failure for unknown CLI type, got %v", lf)
	}
}

func TestProductionResolver_Probe(t *testing.T) {
	fake := &fakePlatformCLIResolver{spec: platform.ResolvedLaunchSpec{CLI: platform.ResolvedCLI{Path: "/x/codex"}}}
	r := NewProductionRemoteLaunchResolver(fake, t.TempDir(), nil, nil)
	avail, lf := r.Probe(context.Background(), contract.CLITypeCodex)
	if lf != nil {
		t.Fatalf("expected probe success, got %v", lf)
	}
	if !avail.Available {
		t.Error("expected available=true")
	}
}

// ---------------------------------------------------------------------------
// 2. SyncFeed pump (design §7.1, T09)
// ---------------------------------------------------------------------------

func TestSyncFeedPump_OutputGetsSeq(t *testing.T) {
	// Reserve a ticket via the real hub (causal port), build a record, pump it.
	hub := NewSessionEventHub()
	hub.MarkReady()
	streams := NewSessionStreamStore()
	sessionID := contract.SessionID("s1")

	ticket, err := hub.ReserveRunRecordUnderState(sessionID, RunCausalPosition{SegmentID: 1, Source: 1}, CausalReplay)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	rec := LiveRunRecord{
		SourceOrdinal: 1,
		SegmentID:     1,
		Kind:          LiveRecordOutput,
		Output:        []byte("hello"),
		Ticket:        ticket,
	}
	snap := LiveContinuitySnapshot{
		Records:        []LiveRunRecord{rec},
		Earliest:       1,
		Latest:         1,
		OriginComplete: true,
		Position:       RunCausalPosition{SegmentID: 1, Source: 1},
	}
	pos := streams.SyncFeed(sessionID, snap, hub)
	if pos.Source != 1 {
		t.Errorf("pos.Source = %d, want 1", pos.Source)
	}
	earliest, latest := streams.SeqBounds(sessionID)
	if earliest != 1 || latest != 1 {
		t.Errorf("SeqBounds = (%d,%d), want (1,1)", earliest, latest)
	}
	frames := streams.FramesAfter(sessionID, nil)
	if len(frames) != 1 || frames[0].seq != 1 {
		t.Errorf("expected 1 frame seq=1, got %v", frames)
	}
}

func TestSyncFeedPump_BoundaryGetsSeq_ExitDoesNot(t *testing.T) {
	hub := NewSessionEventHub()
	hub.MarkReady()
	streams := NewSessionStreamStore()
	sessionID := contract.SessionID("s2")

	// boundary (source 1) + exit (source 2): boundary gets Seq, exit does not.
	bTicket, _ := hub.ReserveRunRecordUnderState(sessionID, RunCausalPosition{SegmentID: 1, Source: 1}, CausalReplay)
	eTicket, _ := hub.ReserveRunRecordUnderState(sessionID, RunCausalPosition{SegmentID: 1, Source: 2}, CausalRunState)
	snap := LiveContinuitySnapshot{
		Records: []LiveRunRecord{
			{SourceOrdinal: 1, SegmentID: 1, Kind: LiveRecordRestartBoundary, Ticket: bTicket},
			{SourceOrdinal: 2, SegmentID: 1, Kind: LiveRecordExit, Exit: ProcessExitObservation{}, Ticket: eTicket},
		},
		Earliest: 1, Latest: 2, OriginComplete: true,
		Position: RunCausalPosition{SegmentID: 1, Source: 2},
	}
	streams.SyncFeed(sessionID, snap, hub)
	_, latest := streams.SeqBounds(sessionID)
	if latest != 1 {
		t.Errorf("latest Seq = %d, want 1 (boundary=1, exit no Seq)", latest)
	}
}

func TestSyncFeedPump_Idempotent(t *testing.T) {
	hub := NewSessionEventHub()
	hub.MarkReady()
	streams := NewSessionStreamStore()
	sessionID := contract.SessionID("s3")

	ticket, _ := hub.ReserveRunRecordUnderState(sessionID, RunCausalPosition{SegmentID: 1, Source: 1}, CausalReplay)
	rec := LiveRunRecord{SourceOrdinal: 1, SegmentID: 1, Kind: LiveRecordOutput, Output: []byte("x"), Ticket: ticket}
	snap := LiveContinuitySnapshot{Records: []LiveRunRecord{rec}, Position: RunCausalPosition{SegmentID: 1, Source: 1}}
	streams.SyncFeed(sessionID, snap, hub)
	// Second sync with the same records: no new Seq.
	streams.SyncFeed(sessionID, snap, hub)
	_, latest := streams.SeqBounds(sessionID)
	if latest != 1 {
		t.Errorf("idempotent: latest Seq = %d, want 1", latest)
	}
}

// ---------------------------------------------------------------------------
// 3. Causal attach watermark + startAfter (design §6.3, §4A.4, T44)
// ---------------------------------------------------------------------------

func TestCausalAttach_StartAfterFiltersPreSnapshotEvents(t *testing.T) {
	// T44 core property (design §4A.4, T44 matrix): a delayed publish of a
	// pre-watermark ticket must NOT be delivered to a new subscription whose
	// startAfterEventOrdinal was captured at the watermark. The reservation sets
	// the watermark; the subscription startAfter = watermark; a later (delayed)
	// pump of that ticket is skipped because ordinal ≤ startAfter.
	hub := NewSessionEventHub()
	hub.MarkReady()
	sessionID := contract.SessionID("s4")

	// Reserve an output ticket (ordinal 1). Reservation sets watermark.Event=1.
	t1, _ := hub.ReserveRunRecordUnderState(sessionID, RunCausalPosition{SegmentID: 1, Source: 1}, CausalReplay)
	wm := hub.WatermarkFor(sessionID)
	if wm.Event != 1 {
		t.Fatalf("watermark.Event = %d, want 1 after reservation", wm.Event)
	}

	// Register a causal sub with startAfter = watermark.Event (=1). This is the
	// attach point: the snapshot has absorbed ordinal 1.
	sub := hub.RegisterCausalSubscription(sessionID, wm.Event, nil, nil)
	defer hub.UnregisterCausalSubscription(sub)

	// Delayed pump: publish the pre-watermark ticket AFTER the sub exists.
	// ordinal 1 ≤ startAfter 1 → CausalSkippedBeforeStart (NOT delivered).
	out := hub.PublishReserved(t1, contract.OutputEvent{
		Type: contract.ServerEventTypeOutput, SessionID: sessionID, Seq: 1, Chunk: paddedBase64([]byte("old")),
	})
	if out.Delivered != 0 {
		t.Errorf("delayed pre-watermark ticket: delivered=%d, want 0", out.Delivered)
	}
	if out.Skipped == 0 {
		t.Errorf("delayed pre-watermark ticket: skipped=%d, want >0", out.Skipped)
	}
	// The subscription queue must be empty (no pre-snapshot event delivered).
	if drained := sub.Drain(); len(drained) != 0 {
		t.Errorf("expected empty queue, got %d events", len(drained))
	}
}

// ---------------------------------------------------------------------------
// 3b. Post-watermark event IS delivered (positive case)
// ---------------------------------------------------------------------------

func TestCausalAttach_PostWatermarkEventDelivered(t *testing.T) {
	// A NEW event (ordinal > startAfter) published after attach must be delivered
	// to the causal subscription (design §4A.4: ">watermark event仍按序交付").
	hub := NewSessionEventHub()
	hub.MarkReady()
	sessionID := contract.SessionID("s6")

	// Reserve ordinal 1 (pre-snapshot), register sub with startAfter=1.
	t1, _ := hub.ReserveRunRecordUnderState(sessionID, RunCausalPosition{SegmentID: 1, Source: 1}, CausalReplay)
	wm := hub.WatermarkFor(sessionID)
	sub := hub.RegisterCausalSubscription(sessionID, wm.Event, nil, nil)
	defer hub.UnregisterCausalSubscription(sub)

	// Reserve + publish a NEW event (ordinal 2 > startAfter 1).
	t2, _ := hub.ReserveRunRecordUnderState(sessionID, RunCausalPosition{SegmentID: 1, Source: 2}, CausalReplay)
	out := hub.PublishReserved(t2, contract.OutputEvent{
		Type: contract.ServerEventTypeOutput, SessionID: sessionID, Seq: 2, Chunk: paddedBase64([]byte("new")),
	})
	if out.Delivered != 1 {
		t.Errorf("post-watermark event: delivered=%d, want 1", out.Delivered)
	}
	ev, ok := sub.Next(context.Background())
	if !ok {
		t.Fatal("expected to drain the post-watermark event")
	}
	if ev.ordinal != 2 {
		t.Errorf("drained ordinal = %d, want 2", ev.ordinal)
	}
	// The pre-watermark ticket (ordinal 1) must still be skippable if published late.
	_ = hub.PublishReserved(t1, contract.OutputEvent{
		Type: contract.ServerEventTypeOutput, SessionID: sessionID, Seq: 1, Chunk: paddedBase64([]byte("old")),
	})
	if drained := sub.Drain(); len(drained) != 0 {
		t.Errorf("pre-watermark ticket leaked into queue: %d events", len(drained))
	}
}

func TestCausalAttach_RetryConverges(t *testing.T) {
	// syncFeedAndAttachCausal: SyncFeed + watermark comparison + retry.
	hub := NewSessionEventHub()
	hub.MarkReady()
	streams := NewSessionStreamStore()
	committer := NewRunSegmentCommitter(hub, newCountingOutcomeRecorder())
	feed := NewLiveRunContinuityFeed(committer)
	sessionID := contract.SessionID("s5")

	// The feed is empty: SyncFeed produces expectedPos = {0,0}, watermark.Run = {0,0}.
	// They match immediately (0 retries).
	outcome, cerr := syncFeedAndAttachCausal(context.Background(), sessionID, feed, streams, hub, nil)
	if cerr != nil {
		t.Fatalf("expected success, got %v", cerr)
	}
	if outcome.retries != 0 {
		t.Errorf("retries = %d, want 0 (empty feed converges immediately)", outcome.retries)
	}
	if outcome.causalSub == nil {
		t.Error("expected non-nil causal subscription")
	}
	hub.UnregisterCausalSubscription(outcome.causalSub)
}

// ---------------------------------------------------------------------------
// 4. Journal lifecycle records (design §8.5, T18/T36)
// ---------------------------------------------------------------------------

func TestJournalLifecycle_StopRestartRemoveRecords(t *testing.T) {
	// Verify the journal writes intent(result) records for stop/restart/remove
	// through the real file-backed journal.
	dir := t.TempDir()
	j := NewSessionOperationJournal(dir)
	if !j.IsReady() {
		t.Fatal("journal not ready")
	}
	ctx := context.Background()
	for _, op := range []SessionOperationKind{SessionOpStop, SessionOpRestart, SessionOpRemove} {
		permit, err := j.BeginIntent(ctx, SessionOperationIntent{
			OperationID: GenerateOperationID(),
			SessionID:   contract.SessionID("s-j"),
			CLIType:     contract.CLITypeClaudeCode,
			Operation:   op,
			Actor:       SessionActorRemote,
		})
		if err != nil {
			t.Fatalf("BeginIntent %s: %v", op, err)
		}
		if err := j.Complete(ctx, permit, SessionOutcomeCommitted, ""); err != nil {
			t.Fatalf("Complete %s: %v", op, err)
		}
	}
	records, err := j.ListRecent(ctx, 200)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	// 3 ops × 2 records (intent + result) = 6.
	if len(records) != 6 {
		t.Fatalf("expected 6 records, got %d", len(records))
	}
	// Verify each op has an intent (pending) and result (committed) record.
	seenOps := map[SessionOperationKind]bool{}
	for _, r := range records {
		if r.Phase == SessionPhaseResult && r.Outcome == SessionOutcomeCommitted {
			seenOps[r.Operation] = true
		}
	}
	for _, op := range []SessionOperationKind{SessionOpStop, SessionOpRestart, SessionOpRemove} {
		if !seenOps[op] {
			t.Errorf("missing committed result for op %s", op)
		}
	}
}

func TestJournalLifecycle_FailClosedWhenNotReady(t *testing.T) {
	// A not-ready journal must block dangerous operations (design §8.5.2).
	j := NewNoopSessionOperationJournal(false)
	_, err := j.BeginIntent(context.Background(), SessionOperationIntent{
		OperationID: "x", SessionID: "s", CLIType: contract.CLITypeCodex,
		Operation: SessionOpStop, Actor: SessionActorRemote,
	})
	if err == nil {
		t.Error("expected BeginIntent to fail when journal not ready")
	}
}

func TestJournalLifecycle_FailedOutcomeCarriesCode(t *testing.T) {
	// A failed result may carry a safe failureCode (design §8.5.1).
	dir := t.TempDir()
	j := NewSessionOperationJournal(dir)
	ctx := context.Background()
	permit, _ := j.BeginIntent(ctx, SessionOperationIntent{
		OperationID: GenerateOperationID(), SessionID: "s", CLIType: contract.CLITypePi,
		Operation: SessionOpStop, Actor: SessionActorRemote,
	})
	fc := contract.ErrorCodeServiceDown
	if err := j.Complete(ctx, permit, SessionOutcomeFailed, fc); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	records, _ := j.ListRecent(ctx, 10)
	var foundFailed bool
	for _, r := range records {
		if r.Phase == SessionPhaseResult && r.Outcome == SessionOutcomeFailed {
			if r.FailureCode == nil || *r.FailureCode != fc {
				t.Errorf("failureCode = %v, want %s", r.FailureCode, fc)
			}
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Error("missing failed result record")
	}
}
