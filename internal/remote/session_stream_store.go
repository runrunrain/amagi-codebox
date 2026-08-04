package remote

// session_stream_store.go — M2 SessionStreamStore: the volatile v1 Seq
// projection and replay-frame window (design §7).
//
// Each session has a per-run volatile stream that:
//   - maps H1 feed records (output/boundary) to v1 Seq (design §7.2);
//   - maintains a bounded replay ring (1 MiB / 4096 frames) with whole-frame
//     eviction (design §7.1, R9);
//   - exposes earliestSeq/latestSeq for detail/attach (design §7.3);
//   - supports backfill range queries (design §6.5, §7.4).
//
// The store is process-memory only; it does NOT persist, does NOT import PTY
// byte tail, and does NOT contain v1 DTOs (ReplayFrame is the wire form). It is
// the SOLE consumer of the H1 LiveRunContinuityFeed for the remote path (design
// §4.2: "SessionStreamStore.SyncFeed = sole consumer").
//
// Lock order (design §9.1 #4): SessionStreamStore.mu does only lookup/reservation.
// Per-session stream.mu may be acquired while holding the H1 feed.mu reader-side
// (pump = stream→feed release→hub PublishReserved).

import (
	"sync"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Frozen capacity constants (design §7.1)
// ---------------------------------------------------------------------------

const (
	// streamMaxFrames is the per-session replay ring record budget (4096).
	streamMaxFrames = 4096
	// streamMaxBytes is the per-session replay ring byte budget (1 MiB).
	streamMaxBytes = 1 << 20
)

// ---------------------------------------------------------------------------
// ReplayFrameEntry — one entry in the volatile replay ring
// ---------------------------------------------------------------------------

// replayFrameEntry stores one replay frame (output or restart-boundary) with
// its v1 Seq. It is immutable once appended.
type replayFrameEntry struct {
	seq    contract.Seq
	kind   LiveRunRecordKind // LiveRecordOutput or LiveRecordRestartBoundary
	output []byte            // immutable copy (for output)
	exit   ProcessExitObservation
}

// toWireFrame converts the entry to a wire ReplayFrame (OutputEvent or
// SessionRestartBoundaryEvent). occurredAt is supplied by the caller.
func (e *replayFrameEntry) toWireFrame(sessionID contract.SessionID, occurredAt string) (contract.ReplayFrame, error) {
	switch e.kind {
	case LiveRecordOutput:
		return contract.OutputEvent{
			Type:      contract.ServerEventTypeOutput,
			SessionID: sessionID,
			Seq:       e.seq,
			Chunk:     paddedBase64(e.output),
		}, nil
	case LiveRecordRestartBoundary:
		return contract.SessionRestartBoundaryEvent{
			Type:            contract.ServerEventTypeSessionState,
			SessionID:       sessionID,
			State:           contract.SessionStateRunning,
			RestartBoundary: true,
			Seq:             e.seq,
			OccurredAt:      occurredAt,
		}, nil
	default:
		return nil, errStreamInvalidFrame
	}
}

// ---------------------------------------------------------------------------
// sessionStream — per-session volatile replay window
// ---------------------------------------------------------------------------

// sessionStream is the per-session volatile replay window. It tracks the v1
// Seq assignment and the bounded replay ring.
type sessionStream struct {
	mu sync.Mutex

	// v1 Seq assignment.
	nextSeq contract.Seq

	// Replay ring (bounded).
	frames []replayFrameEntry
	bytes  uint64

	// originLost is set when frames are evicted (earliestSeq advances).
	originLost bool

	// State mirrors for detail/attach.
	segmentID RunSegmentID
	runPos    RunCausalPosition // current feed position (highest synced)
}

// newSessionStream creates an empty stream.
func newSessionStream() *sessionStream {
	return &sessionStream{}
}

// earliestSeq returns the first retained Seq, or 0 if no frames.
func (s *sessionStream) earliestSeq() contract.Seq {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) == 0 {
		return 0
	}
	return s.frames[0].seq
}

// latestSeq returns the last assigned Seq, or 0 if no frames.
func (s *sessionStream) latestSeq() contract.Seq {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) == 0 {
		return 0
	}
	return s.nextSeq
}

// appendOutput assigns the next Seq to an output record and appends it.
// Returns the assigned Seq. Caller ensures output ≤ 32 KiB (split by H1).
func (s *sessionStream) appendOutput(data []byte) contract.Seq {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSeq++
	seq := s.nextSeq
	s.appendFrameLocked(replayFrameEntry{
		seq:    seq,
		kind:   LiveRecordOutput,
		output: append([]byte(nil), data...),
	})
	return seq
}

// appendBoundary assigns the next Seq to a restart boundary.
func (s *sessionStream) appendBoundary() contract.Seq {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSeq++
	seq := s.nextSeq
	s.appendFrameLocked(replayFrameEntry{
		seq:  seq,
		kind: LiveRecordRestartBoundary,
	})
	return seq
}

// appendFrameLocked adds a frame with whole-frame eviction.
func (s *sessionStream) appendFrameLocked(e replayFrameEntry) {
	for len(s.frames) >= streamMaxFrames || (s.bytes+uint64(len(e.output)) > streamMaxBytes && len(s.frames) > 0) {
		old := s.frames[0]
		s.frames = s.frames[1:]
		s.bytes -= uint64(len(old.output))
		s.originLost = true
	}
	s.frames = append(s.frames, e)
	s.bytes += uint64(len(e.output))
}

// resetForRestart clears the replay window for a new segment after a restart
// boundary has been written. The boundary itself remains as the first frame.
func (s *sessionStream) beginSegment(segID RunSegmentID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.segmentID = segID
}

// setRunPos records the highest synced feed position.
func (s *sessionStream) setRunPos(pos RunCausalPosition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runPos = pos
}

// runPosition returns the current feed position.
func (s *sessionStream) runPosition() RunCausalPosition {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runPos
}

// framesAfter returns the retained frames with seq > lastSeq (ascending). If
// lastSeq is nil, returns all retained frames. Does NOT modify state.
func (s *sessionStream) framesAfter(lastSeq *contract.Seq) []replayFrameEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) == 0 {
		return nil
	}
	if lastSeq == nil {
		out := make([]replayFrameEntry, len(s.frames))
		copy(out, s.frames)
		return out
	}
	// Binary search for first seq > *lastSeq.
	lo, hi := 0, len(s.frames)
	for lo < hi {
		mid := (lo + hi) / 2
		if s.frames[mid].seq <= *lastSeq {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	out := make([]replayFrameEntry, len(s.frames)-lo)
	copy(out, s.frames[lo:])
	return out
}

// rangeFrames returns frames in [fromSeq, toSeq] if fully retained, or (nil,
// false) if any part of the range is not retained. Returns ascending frames.
func (s *sessionStream) rangeFrames(fromSeq, toSeq contract.Seq) ([]replayFrameEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) == 0 {
		return nil, false
	}
	earliest := s.frames[0].seq
	if fromSeq < earliest {
		return nil, false // range starts before retained window
	}
	var result []replayFrameEntry
	for _, f := range s.frames {
		if f.seq >= fromSeq && f.seq <= toSeq {
			result = append(result, f)
		}
	}
	if len(result) == 0 {
		return nil, false
	}
	return result, true
}

// ---------------------------------------------------------------------------
// SessionStreamStore — the store
// ---------------------------------------------------------------------------

// SessionStreamStore is the volatile v1 Seq projection and replay-frame window
// for all remote sessions (design §7). It is process-memory only.
type SessionStreamStore struct {
	mu      sync.Mutex
	streams map[contract.SessionID]*sessionStream
}

// NewSessionStreamStore creates an empty store.
func NewSessionStreamStore() *SessionStreamStore {
	return &SessionStreamStore{
		streams: make(map[contract.SessionID]*sessionStream),
	}
}

// EnsureStream returns (creating if needed) the stream for a session.
func (s *SessionStreamStore) EnsureStream(sessionID contract.SessionID) *sessionStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.streams[sessionID]
	if st == nil {
		st = newSessionStream()
		s.streams[sessionID] = st
	}
	return st
}

// GetStream returns the stream for a session, or nil if none.
func (s *SessionStreamStore) GetStream(sessionID contract.SessionID) *sessionStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[sessionID]
}

// RemoveStream drops the stream for a session (remove/shutdown).
func (s *SessionStreamStore) RemoveStream(sessionID contract.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, sessionID)
}

// SeqBounds returns the earliestSeq/latestSeq for a session. Returns (0, 0) if
// no stream or no frames (the empty sentinel).
func (s *SessionStreamStore) SeqBounds(sessionID contract.SessionID) (earliest, latest contract.Seq) {
	st := s.GetStream(sessionID)
	if st == nil {
		return 0, 0
	}
	return st.earliestSeq(), st.latestSeq()
}

// AppendOutput assigns a Seq to an output record and appends it to the replay
// window (design §7.2). Returns the assigned Seq.
func (s *SessionStreamStore) AppendOutput(sessionID contract.SessionID, data []byte) contract.Seq {
	return s.EnsureStream(sessionID).appendOutput(data)
}

// AppendBoundary assigns a Seq to a restart boundary (design §7.2).
func (s *SessionStreamStore) AppendBoundary(sessionID contract.SessionID) contract.Seq {
	return s.EnsureStream(sessionID).appendBoundary()
}

// BeginSegment records the segment ID for a session stream.
func (s *SessionStreamStore) BeginSegment(sessionID contract.SessionID, segID RunSegmentID) {
	s.EnsureStream(sessionID).beginSegment(segID)
}

// SetRunPos records the highest synced feed position for a session.
func (s *SessionStreamStore) SetRunPos(sessionID contract.SessionID, pos RunCausalPosition) {
	s.EnsureStream(sessionID).setRunPos(pos)
}

// RunPosition returns the current feed position for a session.
func (s *SessionStreamStore) RunPosition(sessionID contract.SessionID) RunCausalPosition {
	st := s.GetStream(sessionID)
	if st == nil {
		return RunCausalPosition{}
	}
	return st.runPosition()
}

// FramesAfter returns retained frames with seq > lastSeq (design §7.3 attach).
// If lastSeq is nil, returns all retained frames.
func (s *SessionStreamStore) FramesAfter(sessionID contract.SessionID, lastSeq *contract.Seq) []replayFrameEntry {
	st := s.GetStream(sessionID)
	if st == nil {
		return nil
	}
	return st.framesAfter(lastSeq)
}

// RangeFrames returns frames in [fromSeq, toSeq] if fully retained (design §7.4
// backfill). Returns (nil, false) if any part is not retained.
func (s *SessionStreamStore) RangeFrames(sessionID contract.SessionID, fromSeq, toSeq contract.Seq) ([]replayFrameEntry, bool) {
	st := s.GetStream(sessionID)
	if st == nil {
		return nil, false
	}
	return st.rangeFrames(fromSeq, toSeq)
}

// OriginComplete reports whether the session's replay origin is complete (no
// eviction has occurred).
func (s *SessionStreamStore) OriginComplete(sessionID contract.SessionID) bool {
	st := s.GetStream(sessionID)
	if st == nil {
		return true
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return !st.originLost
}

// errStreamInvalidFrame is returned when a frame entry has an invalid kind.
var errStreamInvalidFrame = streamErr("stream: invalid frame kind")

type streamErr string

func (e streamErr) Error() string { return string(e) }
