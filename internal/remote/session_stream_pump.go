package remote

// session_stream_pump.go — M2 H1→Seq pump: the SOLE consumer of the H1
// LiveRunContinuityFeed that maps feed records to v1 Seq + H3 causal
// publication (design §7.1, §6.3 step 2 SyncFeed).
//
// Design §7.1: "M2 SessionStreamStore保存RunCausalPosition、nextSeq/replay ring。
// 每个 H1 record已携H3 ticket；sole pump按source顺序先更新stream，再
// PublishReserved。"
//
// Seq classification (design §7.2):
//   - output + restart-boundary consume a v1 Seq (they are ReplayFrames);
//   - runActivated / exit (normal session.state) do NOT consume a Seq; they
//     only advance the feed cursor and get a causal reservation published.
//
// The pump is idempotent: it tracks the highest synced source ordinal per
// stream (runPos) and only processes records past it. This makes attach
// catch-up safe to re-run (design §6.3 retry loop).
//
// Lock order (design §9.1): the pump holds SessionStreamStore per-stream mu
// (#4) and calls PublishReserved which takes the causal-ledger/hub lock (#9).
// The feed lock (#8) is NOT held during the pump — the snapshot was already
// copied under feed.mu before SyncFeed is called. So the legal chain here is
// stream(#4) → hub(#9); no feed lock is held, no state lock is held. This
// matches design §6.3 step 2: "先放feed lock，再以record携带的ticket调用H3
// PublishReserved".

import (
	"amagi-codebox/internal/remote/contract"
)

// SyncFeed pumps the feed snapshot's records into the volatile v1 Seq window +
// H3 causal publication (design §6.3 step 2, §7.1). It is the sole bridge from
// the H1 LiveRunContinuityFeed to the M2 stream store + H3 hub.
//
// Idempotent: only records with SourceOrdinal > the stream's last-synced
// runPos.Source are processed. Returns the RunCausalPosition after sync (the
// expected position for H3 attach — design §6.3 step 2 "expectedRunPosition").
//
// pub may be nil (tests that only want Seq assignment without publication).
func (s *SessionStreamStore) SyncFeed(
	sessionID contract.SessionID,
	snap LiveContinuitySnapshot,
	pub SessionCausalPublicationPort,
) RunCausalPosition {
	st := s.EnsureStream(sessionID)
	st.mu.Lock()
	defer st.mu.Unlock()

	// Adopt the snapshot's segment.
	st.segmentID = snap.Position.SegmentID

	// Process records in source order past the last-synced position.
	for _, rec := range snap.Records {
		if rec.SourceOrdinal == 0 {
			continue // invalid (runActivated in some paths may have 0)
		}
		if rec.SourceOrdinal <= st.runPos.Source && rec.SegmentID == st.runPos.SegmentID {
			continue // already synced (idempotent)
		}
		st.pumpRecordLocked(sessionID, rec, pub)
		// Advance runPos to this record.
		if rec.SegmentID > st.runPos.SegmentID ||
			(rec.SegmentID == st.runPos.SegmentID && rec.SourceOrdinal > st.runPos.Source) {
			st.runPos = RunCausalPosition{SegmentID: rec.SegmentID, Source: rec.SourceOrdinal}
		}
	}

	// Advance to the snapshot's overall position (covers the case where the
	// latest records were evicted from the snapshot copy but the feed position
	// advanced). The expected position for attach is the feed's current position.
	if snap.Position.SegmentID > st.runPos.SegmentID ||
		(snap.Position.SegmentID == st.runPos.SegmentID && snap.Position.Source > st.runPos.Source) {
		st.runPos = snap.Position
	}
	return st.runPos
}

// PumpIncremental pumps newly-committed feed records (those past the stream's
// last-synced runPos) into the volatile v1 Seq window + H3 causal publication,
// WITHOUT taking a full feed snapshot (M-003). It is the live producer path:
// after each CommitRunObservation the projector calls this to assign a v1 Seq +
// PublishReserved so attached remote clients receive the event live. It is
// idempotent (skips records already at/below runPos) and bounded (drains until
// the feed has no record past runPos).
//
// N-001: the cursor is the full RunCausalPosition {SegmentID, Source}. After a
// restart the feed begins a new segment with source ordinals reset to 1; a
// source-only cursor would either replay old-segment records (if the new
// segment's low source ordinal fell below the old cursor) or miss new-segment
// records (by rewinding runPos to an old segment). The segment-aware cursor
// relocates to the new segment without replaying or skipping. The feed's
// pumpIndex hint keeps the cumulative cost O(N) over N incremental pumps.
func (s *SessionStreamStore) PumpIncremental(
	sessionID contract.SessionID,
	feed LiveRunContinuityFeed,
	pub SessionCausalPublicationPort,
) RunCausalPosition {
	if feed == nil {
		return RunCausalPosition{}
	}
	st := s.EnsureStream(sessionID)
	st.mu.Lock()
	defer st.mu.Unlock()
	for {
		rec, ok := feed.NextAfter(sessionID, st.runPos)
		if !ok {
			break
		}
		// N-001: a record is "already synced" only if it is in the same segment
		// at/below runPos. A record in a higher segment is always new (the
		// restart boundary + new-segment output) and must be pumped even if its
		// source ordinal is smaller than the old segment's last source.
		if rec.SegmentID == st.runPos.SegmentID && rec.SourceOrdinal <= st.runPos.Source {
			continue
		}
		st.segmentID = rec.SegmentID
		st.pumpRecordLocked(sessionID, rec, pub)
		st.runPos = RunCausalPosition{SegmentID: rec.SegmentID, Source: rec.SourceOrdinal}
	}
	return st.runPos
}

// pumpRecordLocked processes one feed record: assigns a Seq (for output/
// boundary), appends to the replay ring, and publishes the causal reservation.
// Caller holds the per-stream mu.
func (s *sessionStream) pumpRecordLocked(sessionID contract.SessionID, rec LiveRunRecord, pub SessionCausalPublicationPort) {
	now := nowUTCNano()
	switch rec.Kind {
	case LiveRecordOutput:
		// Output: assign Seq + append ring + publish (design §7.2).
		s.nextSeq++
		seq := s.nextSeq
		s.appendFrameLocked(replayFrameEntry{
			seq:    seq,
			kind:   LiveRecordOutput,
			output: append([]byte(nil), rec.Output...),
		})
		if rec.Ticket != nil && pub != nil {
			pub.PublishReserved(rec.Ticket, contract.OutputEvent{
				Type:      contract.ServerEventTypeOutput,
				SessionID: sessionID,
				Seq:       seq,
				Chunk:     paddedBase64(rec.Output),
			})
		}
	case LiveRecordRestartBoundary:
		// Boundary: assign Seq + append ring + publish (design §7.2).
		s.nextSeq++
		seq := s.nextSeq
		s.appendFrameLocked(replayFrameEntry{
			seq:  seq,
			kind: LiveRecordRestartBoundary,
		})
		if rec.Ticket != nil && pub != nil {
			pub.PublishReserved(rec.Ticket, contract.SessionRestartBoundaryEvent{
				Type:            contract.ServerEventTypeSessionState,
				SessionID:       sessionID,
				State:           contract.SessionStateRunning,
				RestartBoundary: true,
				Seq:             seq,
				OccurredAt:      now,
			})
		}
	case LiveRecordExit:
		// Exit: NO Seq (normal session.state). Publish the causal reservation
		// so the H3 hub can deliver the exited state event (design §7.2).
		if rec.Ticket != nil && pub != nil {
			state := contract.SessionStateExited
			if rec.Exit.Failed {
				state = contract.SessionStateUnavailable
			}
			pub.PublishReserved(rec.Ticket, contract.SessionStateEvent{
				Type:       contract.ServerEventTypeSessionState,
				SessionID:  sessionID,
				State:      state,
				OccurredAt: now,
			})
		}
	case LiveRecordRunActivated:
		// runActivated: NO Seq. It is an internal barrier that advances the
		// causal release cursor (design §7.2). Publish as a no-payload running
		// state barrier so the subscription startAfter watermark is correct.
		if rec.Ticket != nil && pub != nil {
			pub.PublishReserved(rec.Ticket, contract.SessionStateEvent{
				Type:       contract.ServerEventTypeSessionState,
				SessionID:  sessionID,
				State:      contract.SessionStateRunning,
				OccurredAt: now,
			})
		}
	}
}
