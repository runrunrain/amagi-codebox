package remote

import (
	"errors"
	"strconv"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// PreparedCompositeRestart is the hidden H1/H3/projector half of a restart.
// Every allocation and capacity check completes before Authority commit.
type PreparedCompositeRestart struct {
	runtime    *ControlRuntime
	txnID      uint64
	sessionID  contract.SessionID
	entry      *controlEntry
	feed       *liveRunFeed
	ledger     *causalLedger
	permit     *operationPermit
	seal       *RunSegmentSealReceipt
	stage      *restartRunStage
	projection *runProjection

	records         []LiveRunRecord
	tickets         []CausalEventReservation
	batchOffset     int
	batchCount      int
	totalCount      int
	totalBytes      uint64
	pumpIndex       int
	originLost      bool
	newSegment      RunSegmentID
	boundary        LiveBoundaryReceipt
	resolved        chan struct{}
	decision        preparedActivationDecision
	postCommitFence bool
}

var (
	errRestartActivationStale    = errors.New("remote: stale prepared restart activation")
	errRestartActivationTerminal = errors.New("remote: restart process exited before composite commit")
	errRestartActivationCapacity = errors.New("remote: restart activation capacity unavailable")
)

// PrepareCompositeRestart seals the staged FIFO and reserves a hidden causal
// batch. Late observations wait on resolved and can neither overtake the prefix
// nor turn a ready transaction terminal between prepare and commit.
func (r *ControlRuntime) PrepareCompositeRestart(
	sessionID contract.SessionID,
	permit *operationPermit,
	seal *RunSegmentSealReceipt,
) (*PreparedCompositeRestart, error) {
	if r == nil || permit == nil || permit.entry == nil || seal == nil || permit.entry.sessionID != sessionID {
		return nil, errRestartActivationStale
	}
	committer, ok := r.committer.(*runSegmentCommitter)
	if !ok || committer == nil || r.hub == nil || r.projector == nil {
		return nil, errRestartActivationStale
	}
	txnID := r.activationTxn.Add(1)
	if txnID == 0 {
		return nil, errRestartActivationCapacity
	}
	entry := permit.entry
	feed := committer.EnsureFeed(sessionID)
	ledger := r.hub.ledgerFor(sessionID)
	prepared := &PreparedCompositeRestart{
		runtime: r, txnID: txnID, sessionID: sessionID, entry: entry, feed: feed, ledger: ledger,
		permit: permit, seal: seal, records: make([]LiveRunRecord, liveFeedMaxRecords),
		tickets: make([]CausalEventReservation, liveFeedMaxRecords), resolved: make(chan struct{}),
		decision: preparedActivationBuilding,
	}

	entry.stateMu.Lock()
	feed.mu.Lock()
	ledger.mu.Lock()
	stage := permit.restartStage
	if !restartPermitMatchesLocked(entry, permit) || entry.preparedRestartActivation != nil ||
		stage == nil || stage.seal != seal || stage.observations == nil || stage.newRun == nil || stage.newRunEpoch == 0 ||
		stage.observations.closed || stage.observations.sealed || stage.observations.faulted ||
		feed.preparedRestartOwner != 0 || ledger.preparedOwner != 0 || ledger.preparedCount != 0 ||
		!feed.sealed || feed.currentRun != stage.oldRun || feed.currentEpoch != stage.oldRunEpoch ||
		seal.Intent != permit.restartIntent || seal.SegmentID != feed.segmentID || seal.Generation != feed.sealGeneration {
		ledger.mu.Unlock()
		feed.mu.Unlock()
		entry.stateMu.Unlock()
		return nil, errRestartActivationStale
	}
	if stage.observations.terminal {
		ledger.mu.Unlock()
		feed.mu.Unlock()
		entry.stateMu.Unlock()
		return nil, errRestartActivationTerminal
	}
	if feed.segmentID == RunSegmentID(^uint64(0)) {
		ledger.mu.Unlock()
		feed.mu.Unlock()
		entry.stateMu.Unlock()
		return nil, errRestartActivationCapacity
	}
	batchCount := 1 + len(stage.observations.records)
	if batchCount > liveFeedMaxRecords || stage.observations.totalBytes > liveFeedMaxBytes ||
		batchCount > causalLedgerMaxUnresolved-ledger.unresolvedCount-ledger.preparedCount ||
		uint64(ledger.nextOrdinal) > ^uint64(0)-uint64(batchCount) {
		ledger.mu.Unlock()
		feed.mu.Unlock()
		entry.stateMu.Unlock()
		return nil, errRestartActivationCapacity
	}

	retainStart := 0
	retainedBytes := feed.totalBytes
	for len(feed.records)-retainStart+batchCount > liveFeedMaxRecords || retainedBytes+stage.observations.totalBytes > liveFeedMaxBytes {
		if retainStart >= len(feed.records) {
			ledger.mu.Unlock()
			feed.mu.Unlock()
			entry.stateMu.Unlock()
			return nil, errRestartActivationCapacity
		}
		retainedBytes -= uint64(len(feed.records[retainStart].Output))
		retainStart++
	}
	retainedCount := len(feed.records) - retainStart
	copy(prepared.records[:retainedCount], feed.records[retainStart:])
	prepared.batchOffset = retainedCount
	prepared.batchCount = batchCount
	prepared.totalCount = retainedCount + batchCount
	prepared.totalBytes = retainedBytes + stage.observations.totalBytes
	prepared.pumpIndex = feed.pumpIndex - retainStart
	if prepared.pumpIndex < 0 {
		prepared.pumpIndex = 0
	}
	if prepared.pumpIndex > retainedCount {
		prepared.pumpIndex = retainedCount
	}
	prepared.originLost = feed.originLost || retainStart > 0
	prepared.newSegment = feed.segmentID + 1
	prepared.stage = stage
	prepared.projection = &runProjection{
		token: stage.newRun.desktopRunToken, version: strconv.FormatUint(stage.newRunEpoch, 10),
		flushing: true, flushDone: make(chan struct{}),
	}
	prepared.projection.open.Store(true)

	boundaryIndex := prepared.batchOffset
	prepared.records[boundaryIndex] = LiveRunRecord{
		SourceOrdinal: 1, SegmentID: prepared.newSegment, Run: stage.newRun,
		RunEpoch: stage.newRunEpoch, Kind: LiveRecordRestartBoundary,
	}
	for i, staged := range stage.observations.records {
		staged.SourceOrdinal = RunSourceOrdinal(i + 2)
		staged.SegmentID = prepared.newSegment
		staged.Run = stage.newRun
		staged.RunEpoch = stage.newRunEpoch
		prepared.records[boundaryIndex+i+1] = staged
	}
	for i := 0; i < batchCount; i++ {
		record := &prepared.records[boundaryIndex+i]
		ordinal := ledger.nextOrdinal
		ledger.nextOrdinal++
		class := CausalReplay
		if record.Kind == LiveRecordExit {
			class = CausalRunState
		}
		ticket := &prepared.tickets[i]
		*ticket = CausalEventReservation{
			sessionID: sessionID,
			position:  RunCausalPosition{SegmentID: prepared.newSegment, Source: record.SourceOrdinal},
			ordinal:   ordinal, class: class, state: causalPrepared,
		}
		ledger.reservations[ordinal] = ticket
		record.Ticket = ticket
	}
	prepared.boundary = LiveBoundaryReceipt{
		Intent: seal.Intent, NewRun: stage.newRun, SegmentID: prepared.newSegment,
		Boundary: prepared.records[boundaryIndex],
	}
	ledger.preparedCount = batchCount
	ledger.preparedOwner = txnID
	feed.preparedRestartOwner = txnID
	stage.observations.sealed = true
	stage.observations.owner = txnID
	stage.observations.resolved = prepared.resolved
	entry.preparedRestartActivation = prepared
	prepared.decision = preparedActivationReady
	ledger.mu.Unlock()
	feed.mu.Unlock()
	entry.stateMu.Unlock()
	return prepared, nil
}

// CommitNoFail publishes the reserved restart batch and exact run pointer. It
// performs no allocation, map insertion, encoding, channel close, callback, or I/O.
func (p *PreparedCompositeRestart) CommitNoFail() {
	if p == nil || p.runtime == nil || p.decision != preparedActivationReady || p.stage == nil {
		panic("remote: invalid prepared restart activation commit")
	}
	entry := p.entry
	entry.stateMu.Lock()
	p.feed.mu.Lock()
	p.ledger.mu.Lock()
	stageObs := p.stage.observations
	if entry.preparedRestartActivation != p || p.permit.restartStage != p.stage ||
		stageObs == nil || !stageObs.sealed || stageObs.owner != p.txnID ||
		p.feed.preparedRestartOwner != p.txnID || p.ledger.preparedOwner != p.txnID ||
		p.ledger.preparedCount != p.batchCount {
		p.ledger.mu.Unlock()
		p.feed.mu.Unlock()
		entry.stateMu.Unlock()
		panic("remote: prepared restart activation ownership changed")
	}
	for i := 0; i < p.batchCount; i++ {
		ticket := &p.tickets[i]
		ticket.state = causalReserved
		if ticket.ordinal > p.ledger.watermark.Event {
			p.ledger.watermark.Event = ticket.ordinal
		}
		if ticket.position.SegmentID > p.ledger.watermark.Run.SegmentID ||
			(ticket.position.SegmentID == p.ledger.watermark.Run.SegmentID && ticket.position.Source > p.ledger.watermark.Run.Source) {
			p.ledger.watermark.Run = ticket.position
		}
	}
	p.ledger.preparedCount -= p.batchCount
	p.ledger.unresolvedCount += p.batchCount
	p.ledger.preparedOwner = 0

	p.feed.segmentID = p.newSegment
	p.feed.nextSourceOrdinal = RunSourceOrdinal(p.batchCount)
	p.feed.currentSegmentStart = 0
	p.feed.currentRun = p.stage.newRun
	p.feed.currentEpoch = p.stage.newRunEpoch
	p.feed.records = p.records[:p.totalCount]
	p.feed.totalBytes = p.totalBytes
	p.feed.pumpIndex = p.pumpIndex
	p.feed.originLost = p.originLost
	p.feed.sealed = false
	p.feed.terminal = false
	p.feed.faulted = false
	p.feed.preparedRestartOwner = 0

	entry.currentRun = p.stage.newRun
	entry.runEpoch = p.stage.newRunEpoch
	entry.runPhase = runActive
	entry.stateMirror = contract.SessionStateRunning
	entry.stateMirrorSet = true
	entry.backend = backendHealthy
	entry.backendDetach = backendDetachRecord{}
	p.permit.run = p.stage.newRun
	p.permit.runEpoch = p.stage.newRunEpoch
	p.permit.restartStage = nil
	stageObs.closed = true
	p.decision = preparedActivationCommitted

	p.runtime.projector.mu.Lock()
	closeProjectionFlushLocked(p.runtime.projector.runs[p.sessionID])
	p.runtime.projector.runs[p.sessionID] = p.projection
	p.runtime.projector.mu.Unlock()
	p.ledger.mu.Unlock()
	p.feed.mu.Unlock()
	entry.stateMu.Unlock()
}

// FinishCompositeRestart pumps boundary→staged FIFO, opens the projector
// barrier, and releases observation waiters after every composite owner committed.
func (r *ControlRuntime) FinishCompositeRestart(p *PreparedCompositeRestart) {
	if r == nil || p == nil || p.runtime != r || p.decision != preparedActivationCommitted {
		panic("remote: invalid prepared restart activation finish")
	}
	p.entry.stateMu.Lock()
	postCommitFence := p.postCommitFence
	if p.entry.preparedRestartActivation == p {
		p.entry.preparedRestartActivation = nil
	}
	if postCommitFence {
		p.entry.runPhase = runTerminal
		p.entry.stateMirror = contract.SessionStateUnavailable
		p.entry.stateMirrorSet = true
	}
	p.entry.stateMu.Unlock()

	if !postCommitFence {
		r.projector.PumpPending(p.sessionID)
		staged := p.records[p.batchOffset+1 : p.totalCount]
		r.projector.FlushRestartStage(p.sessionID, staged)
	} else {
		r.projector.ForgetRun(p.sessionID)
		r.projector.mu.Lock()
		authority := r.projector.authority
		r.projector.mu.Unlock()
		if authority != nil {
			authority.CommitExactRunUnavailable(string(p.sessionID), p.stage.newRunEpoch, time.Now())
		}
	}
	r.committer.(*runSegmentCommitter).recorder.RecordStage(LiveStageOutcome{
		Disposition: StageCommitted, StagedCount: len(p.stage.observations.records),
	})
	close(p.resolved)
}

// AbortCompositeRestart removes hidden causal reservations, aborts the exact
// stage/seal, and leaves the old closed generation honestly unavailable.
func (r *ControlRuntime) AbortCompositeRestart(p *PreparedCompositeRestart) error {
	if r == nil || p == nil || p.runtime != r || p.decision == preparedActivationCommitted || p.decision == preparedActivationAborted {
		return nil
	}
	p.entry.stateMu.Lock()
	p.feed.mu.Lock()
	p.ledger.mu.Lock()
	owned := p.entry.preparedRestartActivation == p && p.feed.preparedRestartOwner == p.txnID &&
		p.ledger.preparedOwner == p.txnID && p.stage != nil && p.stage.observations != nil &&
		p.stage.observations.owner == p.txnID
	if owned {
		for i := 0; i < p.batchCount; i++ {
			ticket := &p.tickets[i]
			if ticket.state == causalPrepared && p.ledger.reservations[ticket.ordinal] == ticket {
				delete(p.ledger.reservations, ticket.ordinal)
				ticket.state = causalSuppressed
			}
		}
		p.ledger.preparedCount -= p.batchCount
		p.ledger.preparedOwner = 0
		p.feed.preparedRestartOwner = 0
		p.stage.observations.sealed = false
		p.stage.observations.owner = 0
		p.entry.preparedRestartActivation = nil
	}
	p.ledger.mu.Unlock()
	p.feed.mu.Unlock()
	p.entry.stateMu.Unlock()
	p.decision = preparedActivationAborted
	abortErr := r.AbortRestartStage(p.permit, p.seal, p.sessionID)
	select {
	case <-p.resolved:
	default:
		close(p.resolved)
	}
	return abortErr
}

func (p *PreparedCompositeRestart) Boundary() LiveBoundaryReceipt {
	if p == nil {
		return LiveBoundaryReceipt{}
	}
	return p.boundary
}

func (p *PreparedCompositeRestart) RunEpoch() uint64 {
	if p == nil || p.stage == nil {
		return 0
	}
	return p.stage.newRunEpoch
}

func (p *PreparedCompositeRestart) PostCommitFenced() bool {
	if p == nil || p.entry == nil {
		return false
	}
	p.entry.stateMu.Lock()
	defer p.entry.stateMu.Unlock()
	return p.postCommitFence
}
