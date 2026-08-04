package remote

// control_h123_test_helpers.go — shared test helpers for H1/H2/H3 hardening
// tests (T39–T44). Provides a fake causal reservation port, a recording hook,
// and helpers to construct H1/H3 components in isolation.

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// fakeCausalPort — standalone H1 test causal port (design §4A.5)
// ---------------------------------------------------------------------------

// fakeCausalPort is a minimal in-memory SessionCausalReservationPort for H1
// standalone tests. It mints tickets with monotonic ordinals and records
// reservations for parity assertions (record↔ticket 1:1).
type fakeCausalPort struct {
	mu             sync.Mutex
	nextOrdinal    SessionEventOrdinal
	reservations   []*CausalEventReservation
	sealCalls      []sealedSegment
	rollbackCalls  []CausalSealReceipt
	publishResults []CausalPublishOutcome
	reserveErr     error
}

type sealedSegment struct {
	sessionID  contract.SessionID
	segmentID  RunSegmentID
	lastSource RunSourceOrdinal
}

func newFakeCausalPort() *fakeCausalPort {
	return &fakeCausalPort{nextOrdinal: 1}
}

func (f *fakeCausalPort) ReserveRunRecordUnderState(
	sessionID contract.SessionID,
	position RunCausalPosition,
	class CausalProjectionClass,
) (*CausalEventReservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reserveErr != nil {
		return nil, f.reserveErr
	}
	ord := f.nextOrdinal
	f.nextOrdinal++
	t := &CausalEventReservation{
		sessionID: sessionID,
		position:  position,
		ordinal:   ord,
		class:     class,
		state:     causalReserved,
	}
	f.reservations = append(f.reservations, t)
	return t, nil
}

func (f *fakeCausalPort) SealRunSegmentUnderState(
	sessionID contract.SessionID,
	segmentID RunSegmentID,
	lastSource RunSourceOrdinal,
) CausalSealReceipt {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sealCalls = append(f.sealCalls, sealedSegment{sessionID, segmentID, lastSource})
	return CausalSealReceipt{SegmentID: segmentID, LastSource: lastSource, Generation: uint64(len(f.sealCalls))}
}

func (f *fakeCausalPort) RollbackRunSegmentSealUnderState(_ contract.SessionID, receipt CausalSealReceipt) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if receipt.Generation == 0 || receipt.SuppressedReservations != 0 {
		return false
	}
	f.rollbackCalls = append(f.rollbackCalls, receipt)
	return true
}

func (f *fakeCausalPort) ReservationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reservations)
}

// ---------------------------------------------------------------------------
// recordingLifecycleHook — records all hook calls for H2 ordering assertions
// ---------------------------------------------------------------------------

// recordingLifecycleHook records the sequence of hook method calls for H2
// authority-order assertions (design §4A.3).
type recordingLifecycleHook struct {
	mu    sync.Mutex
	calls []string
	ready atomic.Bool
}

func newRecordingLifecycleHook() *recordingLifecycleHook {
	h := &recordingLifecycleHook{}
	h.ready.Store(true)
	return h
}

func (h *recordingLifecycleHook) IsReady() bool { return h.ready.Load() }
func (h *recordingLifecycleHook) MarkDeviceRevoked(deviceID contract.DeviceID) {
	h.mu.Lock()
	h.calls = append(h.calls, "MarkDeviceRevoked")
	h.mu.Unlock()
}
func (h *recordingLifecycleHook) ReleaseRevokedDevice(notice DeviceRevocationNotice) {
	h.mu.Lock()
	h.calls = append(h.calls, "ReleaseRevokedDevice")
	h.mu.Unlock()
}
func (h *recordingLifecycleHook) FenceAllRemote(cause ControlLifecycleCause, at time.Time) {
	h.mu.Lock()
	h.calls = append(h.calls, "FenceAllRemote")
	h.mu.Unlock()
}
func (h *recordingLifecycleHook) ReleaseAllRemote(cause ControlLifecycleCause, at time.Time) {
	h.mu.Lock()
	h.calls = append(h.calls, "ReleaseAllRemote")
	h.mu.Unlock()
}
func (h *recordingLifecycleHook) RestartRemote(at time.Time) {
	h.mu.Lock()
	h.calls = append(h.calls, "RestartRemote")
	h.mu.Unlock()
}

func (h *recordingLifecycleHook) Calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.calls))
	copy(out, h.calls)
	return out
}

func (h *recordingLifecycleHook) IndexOf(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, c := range h.calls {
		if c == name {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Helpers for constructing H1/H3 components
// ---------------------------------------------------------------------------

// newCommitterWithFake creates a RunSegmentCommitter backed by a fake causal
// port + counting outcome recorder.
func newCommitterWithFake(t *testing.T) (RunSegmentCommitter, *fakeCausalPort, *countingOutcomeRecorder) {
	t.Helper()
	port := newFakeCausalPort()
	rec := newCountingOutcomeRecorder()
	c := NewRunSegmentCommitter(port, rec)
	return c, port, rec
}

// startSessionForCommitter creates a controlEntry + RunObservationPermit for H1
// tests, mirroring startSessionDirect but returning the permit for the committer.
func startSessionForCommitter(t *testing.T, arb *ControlArbiter, sid contract.SessionID) (*RunObservationPermit, *runIdentity) {
	t.Helper()
	entry := &controlEntry{
		sessionID:    sid,
		owner:        controlOwner{kind: ownerNone},
		controlEpoch: 1,
		opLane:       newBoundedOperationLane(),
		runPhase:     runActive,
		backend:      backendHealthy,
	}
	run := &runIdentity{nonce: 1, desktopRunToken: "tok"}
	entry.currentRun = run
	entry.runEpoch = 1
	entry.holderGeneration = 1
	entry.stateMirror = contract.SessionStateRunning
	entry.stateMirrorSet = true
	arb.tableMu.Lock()
	arb.entries[sid] = entry
	arb.tableMu.Unlock()
	permit := &RunObservationPermit{
		entry:        entry,
		run:          run,
		runEpoch:     1,
		backendEpoch: 0,
	}
	return permit, run
}

// holderGen returns the current holderGeneration for a session's entry.
func holderGen(arb *ControlArbiter, sid contract.SessionID) HolderGeneration {
	entry := arb.entryFor(sid)
	if entry == nil {
		return 0
	}
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	return entry.holderGeneration
}
