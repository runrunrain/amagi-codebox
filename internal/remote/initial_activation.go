package remote

import (
	"context"
	"errors"
	"strconv"
	"time"

	"amagi-codebox/internal/remote/contract"
)

type initialRunStage struct {
	records      []LiveRunRecord
	totalBytes   uint64
	stageOrdinal RunSourceOrdinal
	terminal     bool
	faulted      bool
	sealed       bool
	owner        uint64
	resolved     chan struct{}
}

type preparedActivationDecision uint8

const (
	preparedActivationBuilding preparedActivationDecision = iota + 1
	preparedActivationReady
	preparedActivationCommitted
	preparedActivationAborted
)

// PreparedCompositeActivation owns every pointer and backing store needed by
// the Manager-held activation closure. Its fields remain private to remote.
type PreparedCompositeActivation struct {
	runtime         *ControlRuntime
	txnID           uint64
	sessionID       contract.SessionID
	entry           *controlEntry
	feed            *liveRunFeed
	ledger          *causalLedger
	stage           *initialRunStage
	runPermit       *RunPermit
	obsPermit       *RunObservationPermit
	projection      *runProjection
	records         []LiveRunRecord
	tickets         []CausalEventReservation
	count           int
	totalBytes      uint64
	resolved        chan struct{}
	decision        preparedActivationDecision
	postCommitFence bool
}

var (
	errInitialActivationStale    = errors.New("remote: stale initial activation")
	errInitialActivationTerminal = errors.New("remote: process exited before activation")
	errInitialActivationCapacity = errors.New("remote: initial activation capacity unavailable")
)

// PrepareCompositeActivation preallocates and seals the complete initial H1/H3
// batch plus a hidden projector slot. No Authority membership is visible yet.
func (r *ControlRuntime) PrepareCompositeActivation(sessionID contract.SessionID, runPermit *RunPermit, obsPermit *RunObservationPermit) (*PreparedCompositeActivation, error) {
	if r == nil || runPermit == nil || obsPermit == nil || runPermit.entry == nil || runPermit.run == nil ||
		obsPermit.entry != runPermit.entry || obsPermit.run != runPermit.run || obsPermit.runEpoch != runPermit.runEpoch {
		return nil, errInitialActivationStale
	}
	if gateErr := runPermit.launch.revalidate(r.arbiter); gateErr != nil {
		return nil, gateErr
	}
	committer, ok := r.committer.(*runSegmentCommitter)
	if !ok || committer == nil || r.hub == nil || r.projector == nil {
		return nil, errInitialActivationStale
	}
	txnID := r.activationTxn.Add(1)
	if txnID == 0 {
		return nil, errInitialActivationCapacity
	}
	entry := runPermit.entry
	feed := committer.EnsureFeed(sessionID)
	ledger := r.hub.ledgerFor(sessionID)
	projection := &runProjection{
		token:     runPermit.run.desktopRunToken,
		version:   strconv.FormatUint(runPermit.runEpoch, 10),
		flushing:  true,
		flushDone: make(chan struct{}),
	}
	prepared := &PreparedCompositeActivation{
		runtime: r, txnID: txnID, sessionID: sessionID, entry: entry, feed: feed, ledger: ledger,
		runPermit: runPermit, obsPermit: obsPermit, projection: projection,
		records:  make([]LiveRunRecord, liveFeedMaxRecords),
		tickets:  make([]CausalEventReservation, liveFeedMaxRecords),
		resolved: make(chan struct{}), decision: preparedActivationBuilding,
	}

	// Install a hidden projector slot and fully-sized ledger map before seal.
	r.projector.mu.Lock()
	if r.projector.runs[sessionID] != nil {
		r.projector.mu.Unlock()
		return nil, errInitialActivationStale
	}
	r.projector.runs[sessionID] = projection
	r.projector.mu.Unlock()
	projectionOwned := true
	defer func() {
		if projectionOwned {
			r.projector.abortPreparedInitialProjection(prepared)
		}
	}()

	ledger.mu.Lock()
	if ledger.preparedOwner != 0 || ledger.preparedCount != 0 || len(ledger.reservations) != 0 || ledger.unresolvedCount != 0 {
		ledger.mu.Unlock()
		return nil, errInitialActivationStale
	}
	ledger.reservations = make(map[SessionEventOrdinal]*CausalEventReservation, causalLedgerMaxUnresolved)
	ledger.mu.Unlock()

	entry.stateMu.Lock()
	feed.mu.Lock()
	ledger.mu.Lock()
	stage := entry.initialStage
	if entry.sessionID != sessionID || entry.removed || entry.runPhase != runStarting || entry.currentRun != runPermit.run ||
		runPermit.launch.canceled.Load() || runPermit.launch.runtimeGeneration != r.arbiter.runtimeGeneration.Load() ||
		(!runPermit.launch.isDesktop && (runPermit.launch.acceptanceGeneration != r.arbiter.acceptanceGeneration.Load() || !r.arbiter.acceptingRemote.Load())) ||
		entry.runEpoch != runPermit.runEpoch || entry.backendEpoch != obsPermit.backendEpoch || entry.preparedActivation != nil ||
		stage == nil || stage.sealed || stage.faulted || feed.preparedActivationOwner != 0 || feed.segmentID != 0 ||
		feed.currentRun != nil || len(feed.records) != 0 || ledger.preparedOwner != 0 || ledger.preparedCount != 0 {
		ledger.mu.Unlock()
		feed.mu.Unlock()
		entry.stateMu.Unlock()
		return nil, errInitialActivationStale
	}
	if stage.terminal {
		ledger.mu.Unlock()
		feed.mu.Unlock()
		entry.stateMu.Unlock()
		return nil, errInitialActivationTerminal
	}
	count := 1 + len(stage.records)
	if count > liveFeedMaxRecords || stage.totalBytes > liveFeedMaxBytes ||
		count > causalLedgerMaxUnresolved-ledger.unresolvedCount-ledger.preparedCount ||
		uint64(ledger.nextOrdinal) > ^uint64(0)-uint64(count) {
		ledger.mu.Unlock()
		feed.mu.Unlock()
		entry.stateMu.Unlock()
		return nil, errInitialActivationCapacity
	}

	prepared.stage = stage
	prepared.count = count
	prepared.totalBytes = stage.totalBytes
	prepared.records[0] = LiveRunRecord{
		SourceOrdinal: 1, SegmentID: 1, Run: runPermit.run, RunEpoch: runPermit.runEpoch,
		Kind: LiveRecordRunActivated,
	}
	for i, staged := range stage.records {
		staged.SourceOrdinal = RunSourceOrdinal(i + 2)
		staged.SegmentID = 1
		staged.Run = runPermit.run
		staged.RunEpoch = runPermit.runEpoch
		prepared.records[i+1] = staged
	}
	for i := 0; i < count; i++ {
		ordinal := ledger.nextOrdinal
		ledger.nextOrdinal++
		class := CausalReplay
		if prepared.records[i].Kind == LiveRecordRunActivated || prepared.records[i].Kind == LiveRecordExit {
			class = CausalRunState
		}
		ticket := &prepared.tickets[i]
		*ticket = CausalEventReservation{
			sessionID: sessionID,
			position:  RunCausalPosition{SegmentID: 1, Source: prepared.records[i].SourceOrdinal},
			ordinal:   ordinal, class: class, state: causalPrepared,
		}
		ledger.reservations[ordinal] = ticket
		prepared.records[i].Ticket = ticket
	}
	ledger.preparedCount = count
	ledger.preparedOwner = txnID
	feed.preparedActivationOwner = txnID
	stage.sealed = true
	stage.owner = txnID
	stage.resolved = prepared.resolved
	entry.preparedActivation = prepared
	prepared.decision = preparedActivationReady
	ledger.mu.Unlock()
	feed.mu.Unlock()
	entry.stateMu.Unlock()
	projectionOwned = false
	return prepared, nil
}

// CommitNoFail is called only by Manager.CommitPreparedActivation after all
// fallible work has completed. It performs no map lookup/insertion, append,
// allocation, encoding, CAS, channel close, callback, or I/O.
func (p *PreparedCompositeActivation) CommitNoFail() {
	if p == nil || p.runtime == nil || p.entry == nil || p.feed == nil || p.ledger == nil || p.decision != preparedActivationReady {
		panic("remote: invalid prepared activation commit")
	}
	entry := p.entry
	entry.stateMu.Lock()
	p.feed.mu.Lock()
	p.ledger.mu.Lock()
	if entry.preparedActivation != p || entry.initialStage != p.stage || !p.stage.sealed || p.stage.owner != p.txnID ||
		p.feed.preparedActivationOwner != p.txnID || p.ledger.preparedOwner != p.txnID || p.ledger.preparedCount != p.count {
		p.ledger.mu.Unlock()
		p.feed.mu.Unlock()
		entry.stateMu.Unlock()
		panic("remote: prepared activation ownership changed")
	}
	for i := 0; i < p.count; i++ {
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
	p.ledger.preparedCount -= p.count
	p.ledger.unresolvedCount += p.count
	p.ledger.preparedOwner = 0

	p.feed.segmentID = 1
	p.feed.nextSourceOrdinal = RunSourceOrdinal(p.count)
	p.feed.currentSegmentStart = 0
	p.feed.currentRun = p.runPermit.run
	p.feed.currentEpoch = p.runPermit.runEpoch
	p.feed.records = p.records[:p.count]
	p.feed.totalBytes = p.totalBytes
	p.feed.pumpIndex = 0
	p.feed.originLost = false
	p.feed.sealed = false
	p.feed.terminal = false
	p.feed.faulted = false
	p.feed.preparedActivationOwner = 0

	entry.runPhase = runActive
	if !entry.stateMirrorSet {
		entry.stateMirror = contract.SessionStateRunning
		entry.stateMirrorSet = true
	}
	p.decision = preparedActivationCommitted
	p.ledger.mu.Unlock()
	p.feed.mu.Unlock()
	entry.stateMu.Unlock()
}

// FinishCompositeActivation opens desktop projection, pumps the immutable H1
// prefix, resolves blocked PTY observations, and removes the launch permit.
func (r *ControlRuntime) FinishCompositeActivation(p *PreparedCompositeActivation) {
	if r == nil || p == nil || p.runtime != r || p.decision != preparedActivationCommitted {
		panic("remote: invalid prepared activation finish")
	}
	p.entry.stateMu.Lock()
	postCommitFence := p.postCommitFence
	p.entry.stateMu.Unlock()
	if !postCommitFence {
		p.projection.open.Store(true)
	}
	r.projector.mu.Lock()
	pump := r.projector.streamPump
	feedPort := r.projector.feed
	projectionContext := r.projector.ctx
	r.projector.mu.Unlock()
	if !postCommitFence {
		if pump != nil && feedPort != nil {
			pump.PumpIncremental(p.sessionID, feedPort, r.hub)
		}
		for i := 1; i < p.count; i++ {
			record := p.records[i]
			switch record.Kind {
			case LiveRecordOutput:
				r.projector.emitNow(projectionContext, p.sessionID, p.projection.token, p.projection.version, runProjectionEvent{seq: record.ProjectionSeq, data: record.Output})
			case LiveRecordExit:
				r.projector.emitNow(projectionContext, p.sessionID, p.projection.token, p.projection.version, runProjectionEvent{isExit: true, exitCode: uint32(max(record.Exit.ExitCode, 0))})
			}
		}
	}
	r.projector.mu.Lock()
	if r.projector.runs[p.sessionID] == p.projection {
		closeProjectionFlushLocked(p.projection)
	}
	r.projector.mu.Unlock()

	p.entry.stateMu.Lock()
	if p.entry.preparedActivation == p {
		p.entry.preparedActivation = nil
		p.entry.initialStage = nil
		if postCommitFence {
			p.entry.runPhase = runTerminal
			p.entry.stateMirror = contract.SessionStateUnavailable
			p.entry.stateMirrorSet = true
		}
	}
	p.entry.stateMu.Unlock()
	if postCommitFence {
		r.projector.ForgetRun(p.sessionID)
		r.projector.mu.Lock()
		authority := r.projector.authority
		r.projector.mu.Unlock()
		if authority != nil {
			authority.CommitExactRunUnavailable(string(p.sessionID), p.runPermit.runEpoch, time.Now())
		}
	}
	close(p.resolved)

	r.arbiter.permitMu.Lock()
	delete(r.arbiter.pendingLaunch, p.runPermit.launch.launchGeneration)
	if idx := r.arbiter.pendingByDevice[p.runPermit.launch.deviceID]; idx != nil {
		delete(idx, p.runPermit.launch.launchGeneration)
		if len(idx) == 0 && p.runPermit.launch.deviceID != "" {
			delete(r.arbiter.pendingByDevice, p.runPermit.launch.deviceID)
		}
	}
	r.arbiter.permitMu.Unlock()
}

func (p *RunEventProjector) abortPreparedInitialProjection(prepared *PreparedCompositeActivation) {
	if p == nil || prepared == nil || prepared.projection == nil {
		return
	}
	p.mu.Lock()
	if p.runs[prepared.sessionID] == prepared.projection {
		delete(p.runs, prepared.sessionID)
		closeProjectionFlushLocked(prepared.projection)
	}
	p.mu.Unlock()
}

// AbortCompositeActivation releases only this transaction's hidden claims.
// Ordinals already allocated during seal remain legal gaps and are never reused.
func (r *ControlRuntime) AbortCompositeActivation(p *PreparedCompositeActivation) {
	if r == nil || p == nil || p.runtime != r || p.decision == preparedActivationCommitted || p.decision == preparedActivationAborted {
		return
	}
	if p.stage != nil {
		p.entry.stateMu.Lock()
		p.feed.mu.Lock()
		p.ledger.mu.Lock()
		if p.entry.preparedActivation == p && p.feed.preparedActivationOwner == p.txnID && p.ledger.preparedOwner == p.txnID {
			for i := 0; i < p.count; i++ {
				ticket := &p.tickets[i]
				if ticket.state == causalPrepared && p.ledger.reservations[ticket.ordinal] == ticket {
					delete(p.ledger.reservations, ticket.ordinal)
					ticket.state = causalSuppressed
				}
			}
			p.ledger.preparedCount -= p.count
			p.ledger.preparedOwner = 0
			p.feed.preparedActivationOwner = 0
			p.entry.preparedActivation = nil
			p.entry.initialStage = nil
		}
		p.ledger.mu.Unlock()
		p.feed.mu.Unlock()
		p.entry.stateMu.Unlock()
	}
	p.decision = preparedActivationAborted
	r.projector.abortPreparedInitialProjection(p)
	select {
	case <-p.resolved:
	default:
		close(p.resolved)
	}
}

// WaitForPreparedActivation is used only by exact run observers that arrive
// after seal. It deliberately exposes no Authority or backend data.
func (p *PreparedCompositeActivation) WaitForPreparedActivation(ctx context.Context) bool {
	if p == nil {
		return false
	}
	select {
	case <-p.resolved:
		return p.decision == preparedActivationCommitted
	case <-ctx.Done():
		return false
	}
}
