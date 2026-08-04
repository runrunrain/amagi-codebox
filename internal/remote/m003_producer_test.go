package remote

// M-003: the RunEventProjector is the UNIQUE production producer — OfferOutput
// commits the observation to the H1 feed and pumps it to the v1 stream Seq +
// causal hub, so remote attach/live delivery receives real data (no longer dead
// code).

import (
	"context"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// TestM003_ProducerCommitsAndPumps: TrackRun activates segment 1; OfferOutput
// commits to the feed and assigns a v1 Seq in the stream store + publishes to
// the causal hub.
func TestM003_ProducerCommitsAndPumps(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clock, nil)
	rt.MarkReady()
	streams := NewSessionStreamStore()
	// Wire the stream pump (as the adapter does in production).
	rt.Projector().SetStreamPump(streams)

	ctx := context.Background()
	sessionID := contract.SessionID("s-prod")
	_, _, obsPermit, err := rt.BeginDesktopRun(ctx, sessionID)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	// TrackRun activates H1 segment 1 (writes runActivated first record).
	rt.Projector().TrackRun(sessionID, obsPermit)
	// runActivated is published via the pump (no Seq, but advances the cursor).
	rt.Projector().OfferOutput(obsPermit, 1, []byte("hello"))

	earliest, latest := streams.SeqBounds(sessionID)
	if latest < 1 {
		t.Fatalf("expected at least 1 v1 Seq assigned, got earliest=%d latest=%d", earliest, latest)
	}
	// The output frame must be retrievable for replay/backfill.
	frames := streams.FramesAfter(sessionID, nil)
	found := false
	for _, f := range frames {
		if string(f.output) == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatal("committed output not found in stream store frames")
	}
}

// TestM003_StaleRunNotCommitted: an observation for a stale run is dropped (no
// Seq assigned, no emit) — the committer's exact-match is authoritative.
func TestM003_StaleRunNotCommitted(t *testing.T) {
	clock := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clock, nil)
	rt.MarkReady()
	streams := NewSessionStreamStore()
	rt.Projector().SetStreamPump(streams)

	ctx := context.Background()
	sessionID := contract.SessionID("s-stale")
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(ctx, sessionID)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	_ = launchPermit
	_ = runPermit
	rt.Projector().TrackRun(sessionID, obsPermit)
	rt.Projector().OfferOutput(obsPermit, 1, []byte("first"))
	// Abort the run, mint a new one; the old obsPermit is now stale.
	rt.AbortDesktopRun(ctx, launchPermit, nil)
	rt.RemoveDesktopSession(ctx, sessionID)

	_, _, newObs, err := rt.BeginDesktopRun(ctx, sessionID)
	if err != nil {
		t.Fatalf("second BeginDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(sessionID, newObs)
	// Stale permit should NOT commit/pump (dropped as stale run).
	beforeEarliest, beforeLatest := streams.SeqBounds(sessionID)
	rt.Projector().OfferOutput(obsPermit, 2, []byte("stale"))
	afterEarliest, afterLatest := streams.SeqBounds(sessionID)
	if beforeEarliest != afterEarliest || beforeLatest != afterLatest {
		t.Fatalf("stale observation must not advance the stream: before=(%d,%d) after=(%d,%d)", beforeEarliest, beforeLatest, afterEarliest, afterLatest)
	}
}
