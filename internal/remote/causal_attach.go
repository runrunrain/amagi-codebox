package remote

// causal_attach.go — M2 causal attach orchestration: SyncFeed catch-up +
// watermark comparison + retry ≤ 8 + causal subscription registration (design
// §6.3 attach linearization, §4A.4 CausalWatermark/startAfter).
//
// This is the M2-side orchestration called by the WS actor after AttachControl
// (which mints the lease + directory entry + control snapshot). It welds the
// H1 feed → M2 stream Seq → H3 causal publication + subscription into a single
// causal cut (design §4A.5: "production wiring composes both").
//
// Design §6.3 attach steps 2–5:
//  2. SyncFeed: pump feed records → stream Seq + PublishReserved. Get
//     expectedRunPosition.
//  3. Compare expectedRunPosition vs ledger watermark.Run; mismatch → retry
//     (re-snapshot + re-sync + re-read), up to maxRetries. Exhausted →
//     service.down, zero lease replace.
//  4. On match: register causalHubSubscription with startAfter=watermark.Event.
//     Events with ordinal ≤ startAfter are skipped (snapshot absorbed them).
//  5. The caller assembles session.attached from the history (≤ watermark.Run)
//     + state (≤ watermark.Event), then CommitBootstrap.
//
// Lock order: the caller holds NO lock entering this function. SyncFeed holds
// stream.mu(#4) → hub(#9). WatermarkFor/LedgerFaulted take hub(#9) only.
// RegisterCausalSubscription takes hub(#9) only. No feed lock is held across
// publication (design §6.3: "先放feed lock，再...PublishReserved").

import (
	"context"

	"amagi-codebox/internal/remote/contract"
)

// causalAttachMaxRetries is the design-mandated retry ceiling (design §6.3
// step 3, §4A.4 RetryCausalSnapshot: "最多8次").
const causalAttachMaxRetries = 8

// causalAttachOutcome holds the causal attach result.
type causalAttachOutcome struct {
	expectedPos RunCausalPosition
	watermark   CausalWatermark
	causalSub   *causalHubSubscription
	retries     int
}

// causalAttachError is the causal attach failure. All causal failures map to
// service.down (design §4A.4: retry exhausted or ledger unhealthy → service.down
// with zero lease replace).
type causalAttachError struct {
	reason string // internal diagnostics only (never wire)
}

func (e *causalAttachError) Error() string { return "causal attach failed: " + e.reason }

// causalCut performs the §6.3 convergence loop (snapshot + SyncFeed +
// watermark comparison + retry ≤ causalAttachMaxRetries) WITHOUT registering a
// subscription and WITHOUT touching any lease (M-007: the causal cut must
// converge BEFORE a lease is committed, so a causal failure is zero-lease-
// replace). It returns the converged stream position + causal watermark.
//
// Lock order: caller holds NO lock. SyncFeed holds stream.mu(#4) → hub(#9).
// WatermarkFor/LedgerFaulted take hub(#9) only. No feed lock is held across
// publication (design §6.3).
func causalCut(
	sessionID contract.SessionID,
	feed LiveRunContinuityFeed,
	streams *SessionStreamStore,
	hub *SessionEventHub,
) (RunCausalPosition, CausalWatermark, int, *causalAttachError) {
	if feed == nil || streams == nil || hub == nil {
		return RunCausalPosition{}, CausalWatermark{}, 0, &causalAttachError{reason: "missing dependency"}
	}
	if hub.LedgerFaulted(sessionID) {
		return RunCausalPosition{}, CausalWatermark{}, 0, &causalAttachError{reason: "causal ledger faulted"}
	}
	var lastExpected RunCausalPosition
	for attempt := 0; attempt <= causalAttachMaxRetries; attempt++ {
		snap, _, err := feed.SnapshotAndSubscribe(sessionID)
		if err != nil {
			return RunCausalPosition{}, CausalWatermark{}, 0, &causalAttachError{reason: "feed snapshot unavailable"}
		}
		expectedPos := streams.SyncFeed(sessionID, snap, hub)
		if hub.LedgerFaulted(sessionID) {
			return RunCausalPosition{}, CausalWatermark{}, 0, &causalAttachError{reason: "causal ledger faulted"}
		}
		watermark := hub.WatermarkFor(sessionID)
		if expectedPos == watermark.Run {
			return expectedPos, watermark, attempt, nil
		}
		lastExpected = expectedPos
	}
	_ = lastExpected
	return RunCausalPosition{}, CausalWatermark{}, causalAttachMaxRetries, &causalAttachError{reason: "causal snapshot retry exhausted"}
}

// syncFeedAndAttachCausal performs the §6.3 causal cut AND registers the causal
// subscription (with the given lease) once the cut converges. Kept for tests and
// as a convenience; production attach (handleAttach) calls causalCut first, then
// commits the lease, then registers the subscription itself so a causal failure
// never replaces a lease (M-007).
//
// ctx is used only for the drain-loop lifecycle wiring (not for blocking here);
// the retry loop is bounded by causalAttachMaxRetries.
func syncFeedAndAttachCausal(
	ctx context.Context,
	sessionID contract.SessionID,
	feed LiveRunContinuityFeed,
	streams *SessionStreamStore,
	hub *SessionEventHub,
	lease *ControlConnectionLease,
) (causalAttachOutcome, *causalAttachError) {
	expectedPos, watermark, retries, cerr := causalCut(sessionID, feed, streams, hub)
	if cerr != nil {
		return causalAttachOutcome{}, cerr
	}
	sub := hub.RegisterCausalSubscription(sessionID, watermark.Event, lease, nil)
	return causalAttachOutcome{
		expectedPos: expectedPos,
		watermark:   watermark,
		causalSub:   sub,
		retries:     retries,
	}, nil
}
