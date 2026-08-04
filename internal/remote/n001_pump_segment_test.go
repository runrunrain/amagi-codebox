package remote

// N-001: PumpIncremental must use a segment-aware cursor {SegmentID, Source} so a
// restart (which resets source ordinals to 1 in a new segment) does not replay
// old-segment records or miss new-segment records, and the cumulative cost over
// N incremental pumps is O(N), not O(N²).

import (
	"testing"
	"time"
)

// commitNOutputs commits n output records ("o1".."oN") via the committer and
// returns nothing (used to drive the feed).
func commitNOutputs(t *testing.T, c RunSegmentCommitter, permit *RunObservationPermit, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		out := c.CommitRunObservation(permit, NewOutputObservation([]byte{'o', byte('0' + i)}))
		if out.Disposition != ObservationCommitted {
			t.Fatalf("commit %d: expected Committed, got %s", i, out.Disposition)
		}
	}
}

// TestN001_PumpIncrementalCrossSegment_NoReplay: after a restart the new
// segment's source ordinals restart at 1. A source-only cursor would replay the
// old segment (old source ordinals > new low source) or rewind. The segment-aware
// cursor must relocate to the new segment: boundary + new output get NEW v1 Seqs
// and NO old-segment record is re-pumped.
func TestN001_PumpIncrementalCrossSegment_NoReplay(t *testing.T) {
	c, _, _ := newCommitterWithFake(t)
	feed := NewLiveRunContinuityFeed(c)
	streams := NewSessionStreamStore()
	streams.BeginSegment("s1", 1)

	arb := NewControlArbiter(newCtrlFakeClock(time.Now()), nil, NewAttachmentDirectory())
	arb.MarkReady()
	permit, oldRun := startSessionForCommitter(t, arb, "s1")
	_ = oldRun

	// Segment 1: runActivated + 3 output records.
	if c.ActivateFirstSegment(permit, "s1").Disposition != ObservationCommitted {
		t.Fatal("activate first segment")
	}
	commitNOutputs(t, c, permit, 3)
	// Pump segment 1 incrementally (live producer path).
	pos := streams.PumpIncremental("s1", feed, nil)
	_, latest := streams.SeqBounds("s1")
	if latest < 3 {
		t.Fatalf("expected >=3 Seqs after seg1 pump, got latest=%d", latest)
	}
	seg1Latest := latest
	seg1Pos := pos

	// Restart: seal old segment, commit restart segment (boundary-first), swap run.
	intent := &LifecycleIntentStub{id: 1, sessionID: "s1", holderGeneration: 1}
	seal, err := c.SealRestartSegment(intent, permit.run, permit.runEpoch, "s1")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	newRun := &runIdentity{nonce: 2, desktopRunToken: "tok2"}
	newEpoch := uint64(2)
	permit.entry.stateMu.Lock()
	permit.entry.currentRun = newRun
	permit.entry.runEpoch = newEpoch
	permit.entry.stateMu.Unlock()
	if _, err := c.CommitRestartSegment(intent, seal, nil, newRun, newEpoch, "s1", nil); err != nil {
		t.Fatalf("commit restart segment: %v", err)
	}
	// New-segment output (source ordinals restart at 1,2 — below seg1's last source).
	newPermit := &RunObservationPermit{entry: permit.entry, run: newRun, runEpoch: newEpoch, backendEpoch: permit.backendEpoch}
	commitNOutputs(t, c, newPermit, 2)

	// Pump incrementally from seg1Pos: must relocate to segment 2 WITHOUT replaying
	// segment-1 records. The boundary + 2 new outputs get exactly 3 new Seqs.
	pos = streams.PumpIncremental("s1", feed, nil)
	_, latest = streams.SeqBounds("s1")
	if latest != seg1Latest+3 {
		t.Fatalf("expected %d total Seqs (seg1 +%d + boundary +2 output), got %d", seg1Latest+3, 0, latest)
	}
	if pos.SegmentID != 2 {
		t.Fatalf("expected runPos segment=2 after restart pump, got %d pos=%+v", pos.SegmentID, pos)
	}
	if pos.Source != 3 { // boundary(1) + 2 output(2,3)
		t.Fatalf("expected runPos source=3 in seg2, got %d", pos.Source)
	}
	// No old-segment record should have been re-pumped: frames count == total Seqs.
	frames := streams.FramesAfter("s1", nil)
	if len(frames) != int(latest) {
		t.Fatalf("frame count %d != latest seq %d (replay detected)", len(frames), latest)
	}
	_ = seg1Pos
}

// TestN001_PumpIncrementalIdempotentAndBounded: pumping twice with no new records
// is a no-op (idempotent); the cursor never rewinds. Pumping after each commit
// delivers exactly one new Seq per record (O(N) cumulative, no O(N²) rescan that
// would double-assign Seqs).
func TestN001_PumpIncrementalIdempotentAndBounded(t *testing.T) {
	c, _, _ := newCommitterWithFake(t)
	feed := NewLiveRunContinuityFeed(c)
	streams := NewSessionStreamStore()
	streams.BeginSegment("s2", 1)

	arb := NewControlArbiter(newCtrlFakeClock(time.Now()), nil, NewAttachmentDirectory())
	arb.MarkReady()
	permit, _ := startSessionForCommitter(t, arb, "s2")
	c.ActivateFirstSegment(permit, "s2")

	// Incremental pump after each commit: one new Seq each, never replay.
	for i := 0; i < 5; i++ {
		out := c.CommitRunObservation(permit, NewOutputObservation([]byte{'a' + byte(i)}))
		if out.Disposition != ObservationCommitted {
			t.Fatalf("commit %d: %s", i, out.Disposition)
		}
		pos := streams.PumpIncremental("s2", feed, nil)
		_, latest := streams.SeqBounds("s2")
		if int(latest) != i+1 {
			t.Fatalf("after %d commits expected latest=%d, got %d", i+1, i+1, latest)
		}
		if pos.SegmentID != 1 {
			t.Fatalf("runPos segment drift: %d", pos.SegmentID)
		}
		// Re-pump (no new records): idempotent, no extra Seq.
		streams.PumpIncremental("s2", feed, nil)
		_, latest2 := streams.SeqBounds("s2")
		if int(latest2) != i+1 {
			t.Fatalf("idempotent re-pump added Seq: %d -> %d", i+1, latest2)
		}
	}
	_, latest := streams.SeqBounds("s2")
	if int(latest) != 5 {
		t.Fatalf("expected 5 total output Seqs, got %d", latest)
	}
}

// TestN001_NextAfterSegmentAwareDirect: the feed-level NextAfter honors segment
// identity — a cursor in segment 2 does not surface segment-1 records even if
// their source ordinal is higher.
func TestN001_NextAfterSegmentAwareDirect(t *testing.T) {
	c, _, _ := newCommitterWithFake(t)
	feed := NewLiveRunContinuityFeed(c)
	arb := NewControlArbiter(newCtrlFakeClock(time.Now()), nil, NewAttachmentDirectory())
	arb.MarkReady()
	permit, oldRun := startSessionForCommitter(t, arb, "s3")
	_ = oldRun
	c.ActivateFirstSegment(permit, "s3")
	commitNOutputs(t, c, permit, 2) // seg1 sources: runAct=1, o1=2, o2=3

	// Cursor still in segment 1 at source 2 → next is seg1 source 3.
	rec, ok := feed.NextAfter("s3", RunCausalPosition{SegmentID: 1, Source: 2})
	if !ok || rec.SourceOrdinal != 3 {
		t.Fatalf("seg1 cursor src=2: expected src=3, got ok=%v src=%d", ok, rec.SourceOrdinal)
	}
	// Cursor at end of segment 1 → no more in seg1.
	if _, ok := feed.NextAfter("s3", RunCausalPosition{SegmentID: 1, Source: 3}); ok {
		t.Fatal("expected no record after seg1 source 3")
	}
	// A cursor claiming segment 2 (no seg2 records yet) → none.
	if _, ok := feed.NextAfter("s3", RunCausalPosition{SegmentID: 2, Source: 0}); ok {
		t.Fatal("expected no seg2 records before restart")
	}
}
