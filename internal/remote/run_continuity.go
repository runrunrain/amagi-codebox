package remote

// run_continuity.go — H1: RunSegmentCommitter + LiveRunContinuityFeed (design
// §4A.2). This is the sole linearization domain for run observations (output/
// exit) and restart segment transitions.
//
// Unified commit domain (design §4A.2, §9.1 lock order #7→#8→#9):
//
//	stateMu → feed.mu → causal-ledger.mu
//
// Inside this domain, a single critical section does: exact-match run/epochs/
// segment, preflight feed+reservation capacity, allocate source ordinal AND
// event ordinal atomically, append record + mint opaque ticket, and apply the
// exit state mirror. All three locks are released BEFORE any wake/marshal/
// socket I/O. There is NO verify→emit two-phase (design §4A.2: R-A unified
// commit; R-C bool→emit is retired).
//
// H1 is independently testable: it consumes SessionCausalReservationPort (a
// fake in standalone tests) and RunObservationOutcomeRecorder (a no-fail
// counter). It has NO concrete dependency on H3 or M2 (design §4A.5).
//
// The feed is a self-contained process-memory ring (1 MiB / 4096 records).
// It does NOT depend on M2 stream store, does NOT contain v1 Seq/Gap/DTO, and
// does NOT import PTY byte tail (design §4A.2).

import (
	"sync"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Frozen capacity constants (design §4A.2, R9)
// ---------------------------------------------------------------------------

const (
	// liveFeedMaxBytes is the per-session ring byte budget (1 MiB).
	liveFeedMaxBytes = 1 << 20
	// liveFeedMaxRecords is the per-session ring record budget (4096).
	liveFeedMaxRecords = 4096
	// liveFeedMaxOutputRecordBytes is the max immutable bytes per output record
	// (32 KiB). Larger PTY chunks are split before commit (caller responsibility).
	liveFeedMaxOutputRecordBytes = 32 << 10
	// liveStageMaxBytes leaves one max-size append of headroom in the ring, so a
	// staged prefix cannot evict its own boundary/earliest output before pumping.
	liveStageMaxBytes = liveFeedMaxBytes - liveFeedMaxOutputRecordBytes
)

// ---------------------------------------------------------------------------
// Observation kinds + dispositions (design §4A.2)
// ---------------------------------------------------------------------------

// LiveRunRecordKind classifies a feed record (design §4A.2).
type LiveRunRecordKind uint8

func (k LiveRunRecordKind) String() string {
	switch k {
	case LiveRecordOutput:
		return "Output"
	case LiveRecordExit:
		return "Exit"
	case LiveRecordRunActivated:
		return "RunActivated"
	case LiveRecordRestartBoundary:
		return "RestartBoundary"
	default:
		return "Unknown"
	}
}

const (
	LiveRecordOutput LiveRunRecordKind = iota + 1
	LiveRecordExit
	LiveRecordRunActivated
	LiveRecordRestartBoundary
)

// RunObservationKind is the kind of a raw observation (output | exit).
type RunObservationKind uint8

const (
	ObservationOutput RunObservationKind = iota + 1
	ObservationExit
)

// RunObservationDisposition is the typed outcome of CommitRunObservation (design
// §4A.2). Exactly one disposition per call.
type RunObservationDisposition uint8

func (d RunObservationDisposition) String() string {
	switch d {
	case ObservationCommitted:
		return "Committed"
	case ObservationStaged:
		return "Staged"
	case ObservationDroppedInvalidPermit:
		return "DroppedInvalidPermit"
	case ObservationDroppedStaleRun:
		return "DroppedStaleRun"
	case ObservationDroppedSegmentSealed:
		return "DroppedSegmentSealed"
	case ObservationDroppedStageClosed:
		return "DroppedStageClosed"
	case ObservationDroppedDuplicateTerminal:
		return "DroppedDuplicateTerminal"
	case ObservationDroppedFeedFault:
		return "DroppedFeedFault"
	default:
		return "Unknown"
	}
}

const (
	ObservationCommitted RunObservationDisposition = iota + 1
	ObservationStaged
	ObservationDroppedInvalidPermit
	ObservationDroppedStaleRun
	ObservationDroppedSegmentSealed
	ObservationDroppedStageClosed
	ObservationDroppedDuplicateTerminal
	ObservationDroppedFeedFault
)

// RunObservationOutcome carries the disposition + kind + ordinals. No bytes or
// IDs (design §4A.2: outcome recorder records only kind+disposition).
type RunObservationOutcome struct {
	Disposition RunObservationDisposition
	Kind        RunObservationKind
	SegmentID   RunSegmentID
	// SourceOrdinal is non-zero iff Disposition==ObservationCommitted.
	SourceOrdinal RunSourceOrdinal
	// StageOrdinal is non-zero iff Disposition==ObservationStaged (private to
	// the stage; not yet a history record).
	StageOrdinal RunSourceOrdinal
}

// ---------------------------------------------------------------------------
// RunObservation — immutable bounded payload (design §4A.2)
// ---------------------------------------------------------------------------

// RunObservation is the immutable observation handed to the committer. The
// caller MUST make the immutable byte copy BEFORE taking any lock (design §4A.2:
// "bytes immutable copy在锁前").
type RunObservation struct {
	Kind     RunObservationKind
	Output   []byte // immutable; for ObservationOutput only, ≤32KiB
	Exit     ProcessExitObservation
	IsOutput bool
	// ProjectionSeq is the PTY callback sequence used only when a hidden restart
	// stage later flushes the matching Wails event. It does not participate in
	// H1 source ordinals or the remote replay Seq.
	ProjectionSeq uint64
}

// NewOutputObservation constructs an output observation with an immutable copy.
func NewOutputObservation(data []byte) RunObservation {
	cp := make([]byte, len(data))
	copy(cp, data)
	return RunObservation{Kind: ObservationOutput, Output: cp, IsOutput: true}
}

// NewExitObservation constructs an exit observation.
func NewExitObservation(obs ProcessExitObservation) RunObservation {
	return RunObservation{Kind: ObservationExit, Exit: obs}
}

// ---------------------------------------------------------------------------
// LiveRunRecord — feed ring entry (design §4A.2)
// ---------------------------------------------------------------------------

// LiveRunRecord is one entry in the feed ring. It carries the source ordinal,
// segment ID, run identity, kind, immutable payload, and the opaque causal
// reservation ticket. The ticket is nil for records committed before causal
// wiring (standalone H1 tests use a fake that always mints a ticket).
type LiveRunRecord struct {
	SourceOrdinal RunSourceOrdinal
	SegmentID     RunSegmentID
	Run           *runIdentity
	RunEpoch      uint64
	Kind          LiveRunRecordKind
	Output        []byte // immutable; for output records
	Exit          ProcessExitObservation
	Ticket        *CausalEventReservation
	// ProjectionSeq is private Wails projection metadata retained only so
	// pre-activate output can be emitted after the new run token is published.
	ProjectionSeq uint64
}

// ---------------------------------------------------------------------------
// LiveContinuitySnapshot + subscription (design §4A.2)
// ---------------------------------------------------------------------------

// LiveContinuitySnapshot is an ordered, bounded snapshot of the current segment's
// records (design §4A.2). originComplete=false signals an explicit gap (eviction
// or segment prefix loss), never a silent drop.
type LiveContinuitySnapshot struct {
	Records                    []LiveRunRecord
	Earliest                   RunSourceOrdinal
	Latest                     RunSourceOrdinal
	CurrentSegmentStartOrdinal RunSourceOrdinal
	OriginComplete             bool
	Position                   RunCausalPosition
}

// LiveRunSubscription is a pull-only subscription to the feed (design §4A.2).
// The cursor is a full RunCausalPosition (SegmentID + Source) so a restart that
// resets source ordinals to 0 in a new segment does NOT cause the subscription
// to replay old-segment records or miss new-segment records (N-001).
type LiveRunSubscription struct {
	feed   *liveRunFeed
	cursor RunCausalPosition
}

// Next returns the next record strictly after the cursor position
// {SegmentID, Source}, or (zero, false) if none. A record in a higher segment
// is always "after" a cursor in a lower segment; within the same segment the
// source ordinal must be greater (N-001 cross-segment relocation).
func (s *LiveRunSubscription) Next() (LiveRunRecord, bool) {
	return s.feed.nextAfter(s.cursor)
}

// ---------------------------------------------------------------------------
// LiveRestartStage + seal/boundary receipts (design §4A.2, §4.5)
// ---------------------------------------------------------------------------

// LiveRestartStage is the bounded staging area for a new run before activation
// (design §4.5 step 4). Starting output enters the stage; activation splices it
// into the feed as the new segment prefix.
type LiveRestartStage struct {
	sessionID    contract.SessionID
	intent       *LifecycleIntentStub
	oldRun       *runIdentity
	newRun       *runIdentity
	newRunEpoch  uint64
	records      []LiveRunRecord // staged records, committed after the boundary
	stageOrdinal RunSourceOrdinal
	totalBytes   uint64
	terminal     bool // pre-activate exit latch; production activation must fail
	faulted      bool // bounded FIFO overflow or invalid staged observation
	closed       bool
	sealed       bool
	owner        uint64
	resolved     chan struct{}
}

// LifecycleIntentStub is the minimal opaque intent pointer carried by the feed
// for seal/commit exact-match. In production this is the real LifecycleIntent;
// standalone tests pass a synthetic stub.
type LifecycleIntentStub struct {
	id               lifecycleIntentID
	sessionID        contract.SessionID
	holderGeneration HolderGeneration
}

// RunSegmentSealReceipt is the result of sealing an old segment (design §4.5).
type RunSegmentSealReceipt struct {
	Intent     *LifecycleIntentStub
	OldRun     *runIdentity
	OldEpoch   uint64
	SegmentID  RunSegmentID
	LastSource RunSourceOrdinal
	// Generation exact-matches the feed seal owned by this transaction.
	Generation uint64
	Causal     CausalSealReceipt
	Sealed     bool
	// OwnsSeal is false only for compatibility adoption of a pre-existing seal;
	// rollback must never remove a seal it did not create.
	OwnsSeal bool
}

// LiveBoundaryReceipt is the result of committing a restart segment (design
// §4.5 step 4). The boundary is the first record of the new segment.
type LiveBoundaryReceipt struct {
	Intent    *LifecycleIntentStub
	NewRun    *runIdentity
	SegmentID RunSegmentID
	Boundary  LiveRunRecord
}

// LiveStageDisposition / LiveStageOutcome (design §4A.2).
type LiveStageDisposition uint8

const (
	StageCommitted LiveStageDisposition = iota + 1
	StageAborted
)

type LiveStageOutcome struct {
	Disposition LiveStageDisposition
	StagedCount int
	Reason      string
}

// ---------------------------------------------------------------------------
// RunObservationOutcomeRecorder — no-fail counter / test seam (design §4A.2)
// ---------------------------------------------------------------------------

// RunObservationOutcomeRecorder records kind+disposition for each observation.
// It is a no-fail saturated counter; it never records bytes, IDs, or terminal
// content. It is an H1 readiness prerequisite.
type RunObservationOutcomeRecorder interface {
	Record(kind RunObservationKind, disp RunObservationDisposition)
	RecordStage(outcome LiveStageOutcome)
}

// noopOutcomeRecorder discards all records (default when none injected).
type noopOutcomeRecorder struct{}

func (noopOutcomeRecorder) Record(RunObservationKind, RunObservationDisposition) {}
func (noopOutcomeRecorder) RecordStage(LiveStageOutcome)                         {}

// countingOutcomeRecorder is a saturated counter used by tests and as the
// production readiness seam.
type countingOutcomeRecorder struct {
	mu     sync.Mutex
	counts map[RunObservationDisposition]uint32
	stages map[LiveStageDisposition]uint32
}

func newCountingOutcomeRecorder() *countingOutcomeRecorder {
	return &countingOutcomeRecorder{
		counts: make(map[RunObservationDisposition]uint32),
		stages: make(map[LiveStageDisposition]uint32),
	}
}

func (r *countingOutcomeRecorder) Record(_ RunObservationKind, disp RunObservationDisposition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts[disp] < ^uint32(0) {
		r.counts[disp]++
	}
}

func (r *countingOutcomeRecorder) RecordStage(o LiveStageOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stages[o.Disposition] < ^uint32(0) {
		r.stages[o.Disposition]++
	}
}

func (r *countingOutcomeRecorder) Count(disp RunObservationDisposition) uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[disp]
}

func (r *countingOutcomeRecorder) StageCount(disp LiveStageDisposition) uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stages[disp]
}

// ---------------------------------------------------------------------------
// liveRunFeed — per-session process-memory ring (design §4A.2)
// ---------------------------------------------------------------------------

// liveRunFeed is the per-session feed state. mu is the feed lock (lock order #8
// in the unified domain). It nests UNDER stateMu and is acquired BEFORE the
// causal-ledger lock.
type liveRunFeed struct {
	mu        sync.Mutex
	sessionID contract.SessionID

	// Segment state.
	segmentID           RunSegmentID
	nextSourceOrdinal   RunSourceOrdinal
	currentSegmentStart RunSourceOrdinal

	// Run identity (mirror of the controlEntry's, captured at activation).
	currentRun   *runIdentity
	currentEpoch uint64

	// Ring of records (process-memory only).
	records    []LiveRunRecord
	totalBytes uint64
	// pumpIndex is a monotonic scan hint used by nextAfter so the live pump is
	// O(N) amortized across incremental calls instead of O(N²) (N-001). It points
	// just past the last record returned to the pump; records at lower indices are
	// already delivered. It is adjusted down on front-eviction (records shift
	// left) and clamped to len(records).
	pumpIndex int
	// originLost is set when records are evicted or a segment prefix is lost,
	// making originComplete=false for subsequent snapshots.
	originLost bool

	// sealed is set when the current segment has been sealed by a restart.
	sealed         bool
	sealGeneration uint64

	// terminal is set when an exit record has been committed for the current run.
	terminal bool

	// faulted is set on a feed health failure (an incoming batch cannot fit,
	// causal reservation fails, or an ordinal is exhausted). Ordinary ring
	// eviction is not a fault.
	faulted bool

	preparedActivationOwner uint64
	preparedRestartOwner    uint64
}

func newLiveRunFeed(sessionID contract.SessionID) *liveRunFeed {
	return &liveRunFeed{sessionID: sessionID}
}

// setRunLocked sets the current run identity. Caller holds feed.mu.
func (f *liveRunFeed) setRunLocked(run *runIdentity, epoch uint64) {
	f.currentRun = run
	f.currentEpoch = epoch
	f.terminal = false
}

// beginSegmentLocked starts a new segment and resets the source ordinal counter.
// Caller holds feed.mu.
func (f *liveRunFeed) beginSegmentLocked(segmentID RunSegmentID) {
	f.segmentID = segmentID
	f.nextSourceOrdinal = 0
	f.currentSegmentStart = 0
	f.sealed = false
}

// ---------------------------------------------------------------------------
// RunSegmentCommitter concrete implementation
// ---------------------------------------------------------------------------

// runSegmentCommitter implements RunSegmentCommitter. It holds one feed per
// session and performs the unified commit under stateMu→feed.mu→ledger.mu.
type runSegmentCommitter struct {
	mu       sync.Mutex
	feeds    map[contract.SessionID]*liveRunFeed
	causal   SessionCausalReservationPort
	recorder RunObservationOutcomeRecorder
}

// NewRunSegmentCommitter creates a committer with the given causal port and
// outcome recorder. Either may be nil (nil causal = standalone test without
// reservation; nil recorder = noop).
func NewRunSegmentCommitter(causal SessionCausalReservationPort, recorder RunObservationOutcomeRecorder) RunSegmentCommitter {
	if recorder == nil {
		recorder = noopOutcomeRecorder{}
	}
	return &runSegmentCommitter{
		feeds:    make(map[contract.SessionID]*liveRunFeed),
		causal:   causal,
		recorder: recorder,
	}
}

// RunSegmentCommitter is the H1 sole observation entry point (design §4A.2).
type runRecordBatchPreflighter interface {
	PreflightRunRecordBatchUnderState(sessionID contract.SessionID, count int) error
}

type RunSegmentCommitter interface {
	// CommitRunObservation commits one observation under the unified domain.
	CommitRunObservation(permit *RunObservationPermit, obs RunObservation) RunObservationOutcome
	// SealRestartSegment seals the old segment (design §4.5 step 2).
	SealRestartSegment(intent *LifecycleIntentStub, oldRun *runIdentity, oldEpoch uint64, sessionID contract.SessionID) (*RunSegmentSealReceipt, error)
	// CommitRestartSegment atomically swaps feed run/segment and writes boundary-first.
	CommitRestartSegment(intent *LifecycleIntentStub, receipt *RunSegmentSealReceipt, stage *LiveRestartStage, newRun *runIdentity, newEpoch uint64, sessionID contract.SessionID, activateUnderFeedLock func()) (*LiveBoundaryReceipt, error)
	// RollbackRestartSegment removes only this transaction's exact feed+causal
	// seal. A superseded/non-owned seal is left untouched (fail closed).
	RollbackRestartSegment(receipt *RunSegmentSealReceipt, sessionID contract.SessionID) bool
	// EnsureFeed returns (creating if needed) the feed for a session.
	EnsureFeed(sessionID contract.SessionID) *liveRunFeed
	// ActivateFirstSegment writes the runActivated first record for segment 1.
	ActivateFirstSegment(permit *RunObservationPermit, sessionID contract.SessionID) RunObservationOutcome
}

// EnsureFeed returns the feed for a session, creating it if needed.
func (c *runSegmentCommitter) EnsureFeed(sessionID contract.SessionID) *liveRunFeed {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.feeds[sessionID]
	if f == nil {
		f = newLiveRunFeed(sessionID)
		c.feeds[sessionID] = f
	}
	return f
}

// ActivateFirstSegment initializes segment 1 and writes the runActivated first
// record (design §4A.2: "first activation写internal runActivated作为segment 1
// 首 record"). This reserves a CausalRunState ticket.
func (c *runSegmentCommitter) ActivateFirstSegment(permit *RunObservationPermit, sessionID contract.SessionID) RunObservationOutcome {
	if permit == nil || permit.entry == nil || permit.run == nil {
		return c.record(ObservationExit, RunObservationOutcome{Disposition: ObservationDroppedInvalidPermit})
	}
	feed := c.EnsureFeed(sessionID)
	entry := permit.entry
	// Three-lock domain: stateMu → feed.mu.
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	feed.mu.Lock()
	defer feed.mu.Unlock()

	if entry.currentRun != permit.run || entry.runEpoch != permit.runEpoch {
		return c.record(ObservationExit, RunObservationOutcome{Disposition: ObservationDroppedStaleRun})
	}
	// Begin segment 1.
	feed.beginSegmentLocked(1)
	feed.setRunLocked(permit.run, permit.runEpoch)

	rec := LiveRunRecord{
		SegmentID: feed.segmentID,
		Run:       permit.run,
		RunEpoch:  permit.runEpoch,
		Kind:      LiveRecordRunActivated,
	}
	sourceOrd, ok := c.appendRecordLocked(feed, rec, CausalRunState)
	if !ok {
		return c.record(ObservationExit, RunObservationOutcome{Disposition: ObservationDroppedFeedFault, SegmentID: feed.segmentID})
	}
	return c.record(ObservationExit, RunObservationOutcome{
		Disposition:   ObservationCommitted,
		Kind:          ObservationExit,
		SegmentID:     feed.segmentID,
		SourceOrdinal: sourceOrd,
	})
}

// CommitRunObservation is the sole observation entry point (design §4A.2).
// It acquires the unified three-lock domain and atomically commits the record +
// state mirror + causal reservation.
func (c *runSegmentCommitter) CommitRunObservation(permit *RunObservationPermit, obs RunObservation) RunObservationOutcome {
	if permit == nil || permit.entry == nil || permit.run == nil {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedInvalidPermit, Kind: obs.Kind})
	}
	// Output size guard: caller must split >32KiB, but defend here too.
	if obs.IsOutput && len(obs.Output) > liveFeedMaxOutputRecordBytes {
		// Caller bug: drop as feed fault rather than truncating.
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedFeedFault, Kind: obs.Kind})
	}
	entry := permit.entry
	entry.stateMu.Lock()
	if entry.preparedStop != nil || entry.preparedRemoval != nil {
		entry.stateMu.Unlock()
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedStageClosed, Kind: obs.Kind})
	}
	if entry.currentRun == permit.run && entry.runEpoch == permit.runEpoch && entry.backendEpoch == permit.backendEpoch && entry.initialStage != nil {
		stage := entry.initialStage
		if stage.sealed {
			resolved := stage.resolved
			entry.stateMu.Unlock()
			if resolved != nil {
				<-resolved
			}
			return c.CommitRunObservation(permit, obs)
		}
		if entry.runPhase == runStarting {
			outcome := c.stageInitialRunObservationLocked(stage, permit, obs)
			entry.stateMu.Unlock()
			return outcome
		}
	}
	if stage := matchingRestartObservationStageLocked(entry, permit); stage != nil && stage.sealed {
		resolved := stage.resolved
		entry.stateMu.Unlock()
		if resolved != nil {
			<-resolved
		}
		return c.CommitRunObservation(permit, obs)
	}
	if entry.currentRun == permit.run && entry.runEpoch == permit.runEpoch && entry.runPhase == runStarting {
		entry.stateMu.Unlock()
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedStageClosed, Kind: obs.Kind})
	}
	entry.stateMu.Unlock()

	feed := c.EnsureFeed(permit.entry.sessionID)
	// Three-lock domain: stateMu → feed.mu → (causal ledger via port, which is
	// called while both are held — the port does O(1) work under its own lock).
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	feed.mu.Lock()
	defer feed.mu.Unlock()

	// Exact-match: pointer + runEpoch + backendEpoch. The only non-current
	// identity accepted is the hidden run owned by entry.currentOp's exact
	// restart stage. It is buffered under stateMu rather than stale-dropped.
	if entry.backendEpoch != permit.backendEpoch {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedStaleRun, Kind: obs.Kind})
	}
	if entry.currentRun != permit.run || entry.runEpoch != permit.runEpoch {
		if stage := matchingRestartObservationStageLocked(entry, permit); stage != nil {
			return c.stageRunObservationLocked(stage, permit, obs)
		}
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedStaleRun, Kind: obs.Kind})
	}
	if feed.faulted {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedFeedFault, Kind: obs.Kind, SegmentID: feed.segmentID})
	}
	if feed.sealed {
		// Segment was sealed by a restart; this is a late old-run observation.
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedSegmentSealed, Kind: obs.Kind})
	}
	// Duplicate terminal exit: only one exit per run.
	if obs.Kind == ObservationExit && feed.terminal {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedDuplicateTerminal, Kind: obs.Kind})
	}

	// Build the record.
	recKind := LiveRecordOutput
	if obs.Kind == ObservationExit {
		recKind = LiveRecordExit
	}
	rec := LiveRunRecord{
		SegmentID:     feed.segmentID,
		Run:           permit.run,
		RunEpoch:      permit.runEpoch,
		Kind:          recKind,
		Output:        obs.Output,
		Exit:          obs.Exit,
		ProjectionSeq: obs.ProjectionSeq,
	}
	class := CausalReplay
	if obs.Kind == ObservationExit {
		class = CausalRunState
	}

	sourceOrd, ok := c.appendRecordLocked(feed, rec, class)
	if !ok {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedFeedFault, Kind: obs.Kind, SegmentID: feed.segmentID})
	}

	// Exit state mirror: apply within the same domain (design §4A.2).
	if obs.Kind == ObservationExit {
		feed.terminal = true
		if obs.Exit.Failed {
			entry.stateMirror = contract.SessionStateUnavailable
		} else {
			entry.stateMirror = contract.SessionStateExited
		}
		entry.stateMirrorSet = true
		entry.runPhase = runTerminal
	}

	return c.record(obs.Kind, RunObservationOutcome{
		Disposition:   ObservationCommitted,
		Kind:          obs.Kind,
		SegmentID:     feed.segmentID,
		SourceOrdinal: sourceOrd,
	})
}

func (c *runSegmentCommitter) stageInitialRunObservationLocked(stage *initialRunStage, permit *RunObservationPermit, obs RunObservation) RunObservationOutcome {
	if stage == nil || stage.sealed {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedStageClosed, Kind: obs.Kind})
	}
	if stage.faulted {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedFeedFault, Kind: obs.Kind})
	}
	if len(stage.records) >= liveFeedMaxRecords-1 || stage.totalBytes+uint64(len(obs.Output)) > liveStageMaxBytes {
		stage.faulted = true
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedFeedFault, Kind: obs.Kind})
	}
	if obs.Kind == ObservationExit && stage.terminal {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedDuplicateTerminal, Kind: obs.Kind})
	}
	recordKind := LiveRecordOutput
	if obs.Kind == ObservationExit {
		recordKind = LiveRecordExit
	}
	stage.stageOrdinal++
	stage.records = append(stage.records, LiveRunRecord{
		Run: permit.run, RunEpoch: permit.runEpoch, Kind: recordKind,
		Output: obs.Output, Exit: obs.Exit, ProjectionSeq: obs.ProjectionSeq,
	})
	stage.totalBytes += uint64(len(obs.Output))
	if obs.Kind == ObservationExit {
		stage.terminal = true
	}
	return c.record(obs.Kind, RunObservationOutcome{
		Disposition: ObservationStaged, Kind: obs.Kind, StageOrdinal: stage.stageOrdinal,
	})
}

// matchingRestartObservationStageLocked returns the exact hidden stage that
// owns permit, or nil for every stale/aborted/superseded observation. Caller
// holds entry.stateMu.
func matchingRestartObservationStageLocked(entry *controlEntry, permit *RunObservationPermit) *LiveRestartStage {
	if entry == nil || permit == nil || entry.currentOp == nil || entry.currentOp.restartStage == nil {
		return nil
	}
	stage := entry.currentOp.restartStage
	if stage.newRun != permit.run || stage.newRunEpoch != permit.runEpoch || stage.observations == nil {
		return nil
	}
	if stage.observations.newRun != permit.run || stage.observations.newRunEpoch != permit.runEpoch {
		return nil
	}
	return stage.observations
}

// stageRunObservationLocked appends one immutable observation to the bounded
// pre-activate FIFO. Caller holds entry.stateMu, which serializes PTY callbacks
// with activate/abort and therefore defines source order for the hidden stage.
// Space is O(min(4095 records, 992 KiB)); each call is O(1).
func (c *runSegmentCommitter) stageRunObservationLocked(stage *LiveRestartStage, permit *RunObservationPermit, obs RunObservation) RunObservationOutcome {
	if stage == nil || stage.closed || stage.sealed {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedStageClosed, Kind: obs.Kind})
	}
	if stage.faulted {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedFeedFault, Kind: obs.Kind})
	}
	if obs.IsOutput && len(obs.Output) > liveFeedMaxOutputRecordBytes {
		stage.faulted = true
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedFeedFault, Kind: obs.Kind})
	}
	// One record is reserved for the restart boundary. Byte overflow is a
	// transaction fault (activation fails); never silently retain a prefix.
	if len(stage.records) >= liveFeedMaxRecords-1 || stage.totalBytes+uint64(len(obs.Output)) > liveStageMaxBytes {
		stage.faulted = true
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedFeedFault, Kind: obs.Kind})
	}
	if obs.Kind == ObservationExit && stage.terminal {
		return c.record(obs.Kind, RunObservationOutcome{Disposition: ObservationDroppedDuplicateTerminal, Kind: obs.Kind})
	}

	recKind := LiveRecordOutput
	if obs.Kind == ObservationExit {
		recKind = LiveRecordExit
	}
	stage.stageOrdinal++
	stage.records = append(stage.records, LiveRunRecord{
		Run:           permit.run,
		RunEpoch:      permit.runEpoch,
		Kind:          recKind,
		Output:        obs.Output,
		Exit:          obs.Exit,
		ProjectionSeq: obs.ProjectionSeq,
	})
	stage.totalBytes += uint64(len(obs.Output))
	if obs.Kind == ObservationExit {
		stage.terminal = true
	}
	return c.record(obs.Kind, RunObservationOutcome{
		Disposition:  ObservationStaged,
		Kind:         obs.Kind,
		StageOrdinal: stage.stageOrdinal,
	})
}

// appendRecordLocked allocates the source ordinal, mints the causal ticket, and
// appends the record to the ring. Caller holds stateMu + feed.mu. Returns
// (sourceOrdinal, false) if capacity/ordinal overflow (caller drops as feed
// fault). The causal port is called while both locks are held (it does O(1)
// work under its own ledger lock — the three-lock domain).
func (c *runSegmentCommitter) appendRecordLocked(feed *liveRunFeed, rec LiveRunRecord, class CausalProjectionClass) (RunSourceOrdinal, bool) {
	// Both limits describe a replay ring, not a lifetime output quota. Evict
	// whole oldest records before appending so a long-running session cannot
	// permanently fault after 4096 small PTY reads.
	if !c.ensureFeedCapacityLocked(feed, 1, uint64(len(rec.Output))) {
		feed.faulted = true
		return 0, false
	}
	// Allocate source ordinal.
	feed.nextSourceOrdinal++
	sourceOrd := feed.nextSourceOrdinal
	rec.SourceOrdinal = sourceOrd
	// Mint causal ticket (three-lock domain: the port reserves under its own lock).
	pos := RunCausalPosition{SegmentID: feed.segmentID, Source: sourceOrd}
	if c.causal != nil {
		ticket, err := c.causal.ReserveRunRecordUnderState(feed.sessionID, pos, class)
		if err != nil {
			feed.faulted = true
			return 0, false
		}
		rec.Ticket = ticket
	}
	feed.records = append(feed.records, rec)
	feed.totalBytes += uint64(len(rec.Output))
	return sourceOrd, true
}

// ensureFeedCapacityLocked evicts whole oldest records until an atomic append
// of additionalRecords/additionalBytes fits both replay-window limits. Caller
// holds feed.mu. It returns false only when the incoming batch cannot fit even
// in an empty ring.
func (c *runSegmentCommitter) ensureFeedCapacityLocked(feed *liveRunFeed, additionalRecords int, additionalBytes uint64) bool {
	if additionalRecords < 0 || additionalRecords > liveFeedMaxRecords || additionalBytes > liveFeedMaxBytes {
		return false
	}
	for len(feed.records)+additionalRecords > liveFeedMaxRecords || feed.totalBytes+additionalBytes > liveFeedMaxBytes {
		if len(feed.records) == 0 {
			return false
		}
		c.evictOldestLocked(feed)
	}
	return true
}

// evictOldestLocked removes one oldest record and marks originLost. Caller
// holds feed.mu. The pumpIndex hint is decremented so it keeps pointing at the
// same logical record after the remaining slice shifts left (N-001).
func (c *runSegmentCommitter) evictOldestLocked(feed *liveRunFeed) {
	if len(feed.records) == 0 {
		return
	}
	old := feed.records[0]
	feed.records = feed.records[1:]
	feed.totalBytes -= uint64(len(old.Output))
	feed.originLost = true
	if feed.pumpIndex > 0 {
		feed.pumpIndex--
	}
}

// SealRestartSegment seals the current (old) segment (design §4.5 step 2).
// Called under the unified domain before stopping the old run.
func (c *runSegmentCommitter) SealRestartSegment(intent *LifecycleIntentStub, oldRun *runIdentity, oldEpoch uint64, sessionID contract.SessionID) (*RunSegmentSealReceipt, error) {
	feed := c.EnsureFeed(sessionID)
	feed.mu.Lock()
	defer feed.mu.Unlock()
	// Fence stale runs first (a concurrent restart on a different run must not
	// re-seal/reuse this segment).
	if feed.currentRun != oldRun || feed.currentEpoch != oldEpoch {
		return nil, errFeedStaleRun
	}
	if feed.sealed {
		// Compatibility adoption: an older failed transaction may have left the
		// observation fence in place. The new transaction can activate from it,
		// but does not own (and therefore cannot roll back) that pre-existing seal.
		return &RunSegmentSealReceipt{
			Intent:     intent,
			OldRun:     oldRun,
			OldEpoch:   oldEpoch,
			SegmentID:  feed.segmentID,
			LastSource: feed.nextSourceOrdinal,
			Generation: feed.sealGeneration,
			Sealed:     true,
			OwnsSeal:   false,
		}, nil
	}
	if feed.sealGeneration == ^uint64(0) {
		feed.faulted = true
		return nil, errFeedFault
	}
	lastSource := feed.nextSourceOrdinal
	var causal CausalSealReceipt
	if c.causal != nil {
		causal = c.causal.SealRunSegmentUnderState(sessionID, feed.segmentID, lastSource)
		if causal.Generation == 0 {
			feed.faulted = true
			return nil, errFeedFault
		}
	}
	feed.sealGeneration++
	feed.sealed = true
	return &RunSegmentSealReceipt{
		Intent:     intent,
		OldRun:     oldRun,
		OldEpoch:   oldEpoch,
		SegmentID:  feed.segmentID,
		LastSource: lastSource,
		Generation: feed.sealGeneration,
		Causal:     causal,
		Sealed:     true,
		OwnsSeal:   true,
	}, nil
}

// RollbackRestartSegment removes only the exact seal created by receipt. Feed
// and causal tombstone roll back in one feed→ledger critical section. If causal
// suppression was irreversible or another transaction superseded the seal, the
// method returns false and retains the feed seal (fail closed).
func (c *runSegmentCommitter) RollbackRestartSegment(receipt *RunSegmentSealReceipt, sessionID contract.SessionID) bool {
	if receipt == nil || !receipt.Sealed {
		return false
	}
	if !receipt.OwnsSeal {
		return true // nothing owned by this transaction; do not unseal another.
	}
	feed := c.EnsureFeed(sessionID)
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if !feed.sealed || feed.currentRun != receipt.OldRun || feed.currentEpoch != receipt.OldEpoch ||
		feed.segmentID != receipt.SegmentID || feed.nextSourceOrdinal != receipt.LastSource ||
		feed.sealGeneration != receipt.Generation {
		return false
	}
	if c.causal != nil && !c.causal.RollbackRunSegmentSealUnderState(sessionID, receipt.Causal) {
		return false
	}
	feed.sealed = false
	return true
}

// CommitRestartSegment atomically swaps the run, begins a new segment, and
// writes the boundary-first record (design §4.5 step 4).
func (c *runSegmentCommitter) CommitRestartSegment(
	intent *LifecycleIntentStub,
	receipt *RunSegmentSealReceipt,
	stage *LiveRestartStage,
	newRun *runIdentity,
	newEpoch uint64,
	sessionID contract.SessionID,
	activateUnderFeedLock func(),
) (*LiveBoundaryReceipt, error) {
	if receipt == nil || !receipt.Sealed || newRun == nil || newEpoch == 0 {
		return nil, errFeedNotSealed
	}
	feed := c.EnsureFeed(sessionID)
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if !feed.sealed || receipt.Intent != intent || receipt.OldRun != feed.currentRun ||
		receipt.OldEpoch != feed.currentEpoch || receipt.SegmentID != feed.segmentID ||
		receipt.LastSource != feed.nextSourceOrdinal || receipt.Generation != feed.sealGeneration {
		return nil, errFeedNotSealed
	}
	if stage != nil && (stage.closed || stage.faulted || stage.intent != intent || stage.oldRun != receipt.OldRun ||
		(stage.newRun != nil && stage.newRun != newRun) || (stage.newRunEpoch != 0 && stage.newRunEpoch != newEpoch)) {
		return nil, errFeedStaleRun
	}
	stageRecordCount := 0
	if stage != nil {
		stageRecordCount = len(stage.records)
	}
	if feed.segmentID == RunSegmentID(^uint64(0)) {
		feed.faulted = true
		return nil, errFeedFault
	}
	if preflight, ok := c.causal.(runRecordBatchPreflighter); ok {
		if err := preflight.PreflightRunRecordBatchUnderState(sessionID, 1+stageRecordCount); err != nil {
			feed.faulted = true
			return nil, errFeedFault
		}
	}
	stageBytes := uint64(0)
	if stage != nil {
		stageBytes = stage.totalBytes
	}
	// Reserve the whole boundary + staged prefix before appending any of it. This
	// may evict old, already-pumpable replay records, but never the new prefix.
	if !c.ensureFeedCapacityLocked(feed, 1+stageRecordCount, stageBytes) {
		feed.faulted = true
		return nil, errFeedFault
	}

	// Snapshot the feed fields changed before the boundary append. appendRecordLocked
	// cannot fail after a successful causal reservation; on preflight/reservation
	// failure it appends no record, so restoring these fields leaves no boundary.
	oldSegment := feed.segmentID
	oldNextSource := feed.nextSourceOrdinal
	oldSegmentStart := feed.currentSegmentStart
	oldRun := feed.currentRun
	oldEpoch := feed.currentEpoch
	oldSealed := feed.sealed
	oldTerminal := feed.terminal

	newSeg := feed.segmentID + 1
	feed.beginSegmentLocked(newSeg)
	feed.setRunLocked(newRun, newEpoch)
	boundary := LiveRunRecord{
		SegmentID: newSeg,
		Run:       newRun,
		RunEpoch:  newEpoch,
		Kind:      LiveRecordRestartBoundary,
	}
	sourceOrd, ok := c.appendRecordLocked(feed, boundary, CausalReplay)
	if !ok {
		feed.segmentID = oldSegment
		feed.nextSourceOrdinal = oldNextSource
		feed.currentSegmentStart = oldSegmentStart
		feed.currentRun = oldRun
		feed.currentEpoch = oldEpoch
		feed.sealed = oldSealed
		feed.terminal = oldTerminal
		feed.faulted = true
		return nil, errFeedFault
	}
	boundary.SourceOrdinal = sourceOrd

	// Splice staged observations after the boundary in the source order assigned
	// under entry.stateMu. Exit uses the run-state causal class; production rejects
	// a pre-activate terminal latch before entering this method, while standalone
	// H1 callers still receive correct terminal feed semantics.
	if stage != nil {
		for _, sr := range stage.records {
			sr.SegmentID = newSeg
			sr.Run = newRun
			sr.RunEpoch = newEpoch
			class := CausalReplay
			if sr.Kind == LiveRecordExit {
				class = CausalRunState
			}
			if _, ok := c.appendRecordLocked(feed, sr, class); !ok {
				feed.faulted = true
				return nil, errFeedFault
			}
			if sr.Kind == LiveRecordExit {
				feed.terminal = true
			}
		}
		stage.closed = true
		c.recorder.RecordStage(LiveStageOutcome{Disposition: StageCommitted, StagedCount: len(stage.records)})
	}
	// Production caller already holds stateMu. Publishing entry.currentRun and
	// related state here, while feed.mu is still held, removes the boundary↔run
	// visibility window for feed-only snapshots. The callback must be O(1), must
	// not lock, and must not perform I/O.
	if activateUnderFeedLock != nil {
		activateUnderFeedLock()
	}

	return &LiveBoundaryReceipt{
		Intent:    intent,
		NewRun:    newRun,
		SegmentID: newSeg,
		Boundary:  boundary,
	}, nil
}

// record invokes the outcome recorder and returns the outcome.
func (c *runSegmentCommitter) record(kind RunObservationKind, o RunObservationOutcome) RunObservationOutcome {
	o.Kind = kind
	c.recorder.Record(kind, o.Disposition)
	return o
}

// errFeed* are sentinel errors for segment operations.
var (
	errFeedAlreadySealed = feedErr("feed already sealed")
	errFeedStaleRun      = feedErr("feed run is stale")
	errFeedNotSealed     = feedErr("feed not sealed")
	errFeedFault         = feedErr("feed faulted")
)

type feedErr string

func (e feedErr) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// LiveRunContinuityFeed concrete implementation (design §4A.2)
// ---------------------------------------------------------------------------

// liveRunContinuityFeedImpl implements LiveRunContinuityFeed. It delegates record
// storage to the RunSegmentCommitter's per-session feeds and provides snapshot/
// subscribe + restart staging.
type liveRunContinuityFeedImpl struct {
	committer *runSegmentCommitter
}

// LiveRunContinuityFeed is the H1 ordered record ring + restart staging seam
// (design §4A.2). It is self-contained process-memory; does not depend on M2.
type LiveRunContinuityFeed interface {
	// SnapshotAndSubscribe returns an ordered snapshot of the current segment's
	// records + a pull subscription. originComplete=false signals an explicit gap.
	SnapshotAndSubscribe(sessionID contract.SessionID) (LiveContinuitySnapshot, *LiveRunSubscription, error)
	// NextAfter returns the first record strictly after the cursor position
	// {SegmentID, Source}, without copying the whole ring (M-003 incremental live
	// pump). The cursor carries segment identity (N-001) so a restart that resets
	// source ordinals in a new segment does not replay old records or miss new
	// ones. Used by the production producer to pump newly-committed records to the
	// v1 stream with O(N) amortized cost.
	NextAfter(sessionID contract.SessionID, cursor RunCausalPosition) (LiveRunRecord, bool)
	// BeginRestart creates a bounded staging area for the new run.
	BeginRestart(intent *LifecycleIntentStub, oldRun *runIdentity, sessionID contract.SessionID) (*LiveRestartStage, error)
	// AbortRestart discards a staging area (seal-before-abort can keep old segment).
	AbortRestart(stage *LiveRestartStage)
	// StageOutput adds a starting output record to the stage.
	StageOutput(stage *LiveRestartStage, data []byte, newRun *runIdentity, newEpoch uint64) RunObservationOutcome
}

// NewLiveRunContinuityFeed creates a feed backed by the given committer.
func NewLiveRunContinuityFeed(committer RunSegmentCommitter) LiveRunContinuityFeed {
	impl, ok := committer.(*runSegmentCommitter)
	if !ok {
		panic("control: NewLiveRunContinuityFeed requires *runSegmentCommitter")
	}
	return &liveRunContinuityFeedImpl{committer: impl}
}

// SnapshotAndSubscribe returns the current segment's ordered records + a pull
// subscription positioned at the latest record (design §4A.2).
func (f *liveRunContinuityFeedImpl) SnapshotAndSubscribe(sessionID contract.SessionID) (LiveContinuitySnapshot, *LiveRunSubscription, error) {
	feed := f.committer.EnsureFeed(sessionID)
	feed.mu.Lock()
	defer feed.mu.Unlock()

	snap := LiveContinuitySnapshot{
		OriginComplete:             !feed.originLost,
		CurrentSegmentStartOrdinal: feed.currentSegmentStart,
		Position:                   RunCausalPosition{SegmentID: feed.segmentID, Source: feed.nextSourceOrdinal},
	}
	if len(feed.records) > 0 {
		// Copy only the current segment's records (from currentSegmentStart).
		start := 0
		for i, r := range feed.records {
			if r.SegmentID == feed.segmentID {
				start = i
				break
			}
		}
		seg := feed.records[start:]
		snap.Records = make([]LiveRunRecord, len(seg))
		copy(snap.Records, seg)
		snap.Earliest = seg[0].SourceOrdinal
		snap.Latest = feed.nextSourceOrdinal
		snap.Position = RunCausalPosition{SegmentID: feed.segmentID, Source: feed.nextSourceOrdinal}
	}
	sub := &LiveRunSubscription{feed: feed, cursor: RunCausalPosition{SegmentID: feed.segmentID, Source: feed.nextSourceOrdinal}}
	return snap, sub, nil
}

// BeginRestart creates a staging area for the new run (design §4.5 step 4).
func (f *liveRunContinuityFeedImpl) BeginRestart(intent *LifecycleIntentStub, oldRun *runIdentity, sessionID contract.SessionID) (*LiveRestartStage, error) {
	return &LiveRestartStage{
		sessionID: sessionID,
		intent:    intent,
		oldRun:    oldRun,
	}, nil
}

// AbortRestart discards a staging area (design §4A.2).
func (f *liveRunContinuityFeedImpl) AbortRestart(stage *LiveRestartStage) {
	if stage != nil && !stage.closed {
		stage.closed = true
		f.committer.recorder.RecordStage(LiveStageOutcome{Disposition: StageAborted, StagedCount: len(stage.records)})
	}
}

// StageOutput adds a starting output record to the stage (design §4.5 step 4).
// Production observations use CommitRunObservation so entry.stateMu serializes
// this mutation; this compatibility seam is intended for single-owner H1 tests.
func (f *liveRunContinuityFeedImpl) StageOutput(stage *LiveRestartStage, data []byte, newRun *runIdentity, newEpoch uint64) RunObservationOutcome {
	if stage == nil || stage.closed {
		return f.committer.record(ObservationOutput, RunObservationOutcome{Disposition: ObservationDroppedStageClosed})
	}
	if stage.faulted || newRun == nil || newEpoch == 0 || len(data) > liveFeedMaxOutputRecordBytes ||
		len(stage.records) >= liveFeedMaxRecords-1 || stage.totalBytes+uint64(len(data)) > liveStageMaxBytes {
		stage.faulted = true
		return f.committer.record(ObservationOutput, RunObservationOutcome{Disposition: ObservationDroppedFeedFault})
	}
	if stage.newRun != nil && (stage.newRun != newRun || stage.newRunEpoch != newEpoch) {
		stage.faulted = true
		return f.committer.record(ObservationOutput, RunObservationOutcome{Disposition: ObservationDroppedFeedFault})
	}
	stage.newRun = newRun
	stage.newRunEpoch = newEpoch
	stage.stageOrdinal++
	output := append([]byte(nil), data...)
	stage.records = append(stage.records, LiveRunRecord{
		Kind:     LiveRecordOutput,
		Output:   output,
		Run:      newRun,
		RunEpoch: newEpoch,
	})
	stage.totalBytes += uint64(len(output))
	return f.committer.record(ObservationOutput, RunObservationOutcome{
		Disposition:  ObservationStaged,
		Kind:         ObservationOutput,
		StageOrdinal: stage.stageOrdinal,
	})
}

// NextAfter implements LiveRunContinuityFeed (delegates to the feed ring). The
// cursor is a full RunCausalPosition (N-001): restart begins a new segment whose
// source ordinals restart at 1, so comparing only Source would replay old
// records or miss new-segment records.
func (f *liveRunContinuityFeedImpl) NextAfter(sessionID contract.SessionID, cursor RunCausalPosition) (LiveRunRecord, bool) {
	return f.committer.EnsureFeed(sessionID).nextAfter(cursor)
}

// recordAfterPos reports whether rec is strictly after cursor (N-001): a higher
// segment is always after; same segment requires a greater source ordinal.
func recordAfterPos(rec LiveRunRecord, cursor RunCausalPosition) bool {
	if rec.SegmentID > cursor.SegmentID {
		return true
	}
	if rec.SegmentID == cursor.SegmentID {
		return rec.SourceOrdinal > cursor.Source
	}
	return false
}

// nextAfter returns the first record strictly after the cursor position
// {SegmentID, Source} (pull subscription / live pump). It uses pumpIndex as a
// monotonic hint so the cumulative cost across N incremental pumps is O(N), not
// O(N²) (N-001): each record is visited at most once because pumpIndex only
// advances. Correctness does not depend on the hint — it only bounds the scan;
// records behind the cursor are skipped and the hint advanced past them.
func (f *liveRunFeed) nextAfter(cursor RunCausalPosition) (LiveRunRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pumpIndex > len(f.records) {
		f.pumpIndex = len(f.records)
	}
	for i := f.pumpIndex; i < len(f.records); i++ {
		r := f.records[i]
		if recordAfterPos(r, cursor) {
			f.pumpIndex = i + 1
			return r, true
		}
		// Record is at or before the cursor: it is already delivered. Advance the
		// hint past it so it is never re-scanned (O(N) amortized).
		f.pumpIndex = i + 1
	}
	return LiveRunRecord{}, false
}
