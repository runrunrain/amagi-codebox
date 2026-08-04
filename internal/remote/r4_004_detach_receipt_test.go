package remote

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

type scriptedDetachPTYRaw struct {
	mu       sync.Mutex
	receipts []*testBackendDetachReceipt
	errs     []error
	calls    int
}

func (r *scriptedDetachPTYRaw) WriteRaw(context.Context, string, []byte) error { return nil }
func (r *scriptedDetachPTYRaw) ResizeRaw(context.Context, string, int, int) error {
	return nil
}
func (r *scriptedDetachPTYRaw) DetachSession(string) (BackendDetachReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.calls
	r.calls++
	if i >= len(r.receipts) {
		return nil, errors.New("no scripted detach receipt")
	}
	var err error
	if i < len(r.errs) {
		err = r.errs[i]
	}
	return r.receipts[i], err
}

func waitForDetachDisposition(t *testing.T, entry *controlEntry, want BackendDetachDisposition) backendDetachRecord {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		entry.stateMu.Lock()
		record := entry.backendDetach
		entry.stateMu.Unlock()
		if record.disposition == want {
			return record
		}
		time.Sleep(time.Millisecond)
	}
	entry.stateMu.Lock()
	record := entry.backendDetach
	entry.stateMu.Unlock()
	t.Fatalf("detach disposition=%s, want %s", record.disposition, want)
	return backendDetachRecord{}
}

func TestR4_004_QuarantineEpochOverflowIsTypedUnquarantinedFailure(t *testing.T) {
	clk := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clk, nil)
	rt.MarkReady()
	sid := startPublicSession(t, rt)
	entry := rt.Arbiter().entryFor(sid)
	entry.stateMu.Lock()
	entry.backendEpoch = ^uint64(0)
	before := entry.backend
	entry.stateMu.Unlock()

	outcome := rt.Gate().(*controlGate).quarantineBackend(entry)
	if outcome.disposition != BackendDetachUnquarantinedFailed {
		t.Fatalf("overflow disposition=%s, want unquarantined-failed", outcome.disposition)
	}
	entry.stateMu.Lock()
	after := entry.backend
	entry.stateMu.Unlock()
	if after != before {
		t.Fatalf("failed quarantine changed backend state: before=%d after=%d", before, after)
	}
	if !rt.Arbiter().IsHealthLatched() {
		t.Fatal("backend epoch overflow did not latch fail-closed health")
	}
}

// TestR4_004_DetachFailureBlocksDesktopUntilExactReceipt confirms the initial
// detach failure is represented as quarantined-pending (not silently treated as
// detached). Device and desktop SessionID lifecycle cannot run while ownership
// is unknown; after this exact receipt's reaper confirms, trusted desktop Stop
// is admitted.
func TestR4_004_DetachFailureBlocksDesktopUntilExactReceipt(t *testing.T) {
	clk := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clk, nil)
	pending := newTestDetachReceipt(false, errors.New("injected detach failure"))
	raw := &scriptedDetachPTYRaw{
		receipts: []*testBackendDetachReceipt{pending},
		errs:     []error{errors.New("injected detach failure")},
	}
	rt.SetPTYRawPort(raw)
	rt.MarkReady()
	sid := startPublicSession(t, rt)
	entry := rt.Arbiter().entryFor(sid)

	outcome := rt.Gate().(*controlGate).quarantineBackend(entry)
	if outcome.disposition != BackendDetachQuarantinedPending || outcome.detachIdentity != pending.Identity() {
		t.Fatalf("detach failure was hidden/misclassified: disposition=%s id=%d want pending id=%d", outcome.disposition, outcome.detachIdentity, pending.Identity())
	}
	var rawCalls atomic.Int32
	_, err := rt.Gate().DoDesktopLifecycle(context.Background(), rt.DesktopAuthority(), sid, LifecycleStop,
		func(context.Context, *operationPermit) (SessionMutationResult, error) {
			rawCalls.Add(1)
			return SessionMutationResult{}, nil
		})
	var ge *ControlGateError
	if !errors.As(err, &ge) || ge.Kind != DenyControlUnavailable || ge.Detach != BackendDetachQuarantinedPending {
		t.Fatalf("unconfirmed detach should deny typed desktop recovery, got %v (%+v)", err, ge)
	}
	if rawCalls.Load() != 0 {
		t.Fatal("desktop raw lifecycle ran before exact detach confirmation")
	}

	pending.confirm(nil)
	record := waitForDetachDisposition(t, entry, BackendDetachQuarantinedDetached)
	if record.backendEpoch != outcome.backendEpoch || record.detachIdentity != pending.Identity() {
		t.Fatalf("confirmation not exact: got=%+v outcome=%+v", record, outcome)
	}
	_, err = rt.Gate().DoDesktopLifecycle(context.Background(), rt.DesktopAuthority(), sid, LifecycleStop,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			if err := p.Checkpoint(ctx, 1); err != nil {
				return SessionMutationResult{}, err
			}
			rawCalls.Add(1)
			return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
		})
	if err != nil {
		t.Fatalf("desktop recovery after exact detach confirmation: %v", err)
	}
	if rawCalls.Load() != 1 {
		t.Fatalf("desktop recovery raw calls=%d, want 1", rawCalls.Load())
	}
}

// TestR4_004_StaleDetachConfirmationCannotAuthorizeNewerBackendEpoch proves the
// callback fence: receipt #1 completing after quarantine #2 cannot overwrite
// #2's exact identity/state.
func TestR4_004_StaleDetachConfirmationCannotAuthorizeNewerBackendEpoch(t *testing.T) {
	clk := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clk, nil)
	oldPending := newTestDetachReceipt(false, errors.New("old close failed"))
	newConfirmed := newTestDetachReceipt(true, nil)
	raw := &scriptedDetachPTYRaw{
		receipts: []*testBackendDetachReceipt{oldPending, newConfirmed},
		errs:     []error{errors.New("old close failed"), nil},
	}
	rt.SetPTYRawPort(raw)
	rt.MarkReady()
	sid := startPublicSession(t, rt)
	entry := rt.Arbiter().entryFor(sid)
	gate := rt.Gate().(*controlGate)

	first := gate.quarantineBackend(entry)
	second := gate.quarantineBackend(entry)
	if second.backendEpoch <= first.backendEpoch || second.disposition != BackendDetachQuarantinedDetached {
		t.Fatalf("second quarantine did not advance+confirm: first=%+v second=%+v", first, second)
	}
	oldPending.confirm(nil)
	time.Sleep(20 * time.Millisecond) // allow stale waiter to attempt exact commit
	entry.stateMu.Lock()
	got := entry.backendDetach
	entry.stateMu.Unlock()
	if got.backendEpoch != second.backendEpoch || got.detachIdentity != newConfirmed.Identity() || got.disposition != BackendDetachQuarantinedDetached {
		t.Fatalf("stale receipt clobbered newer detach record: got=%+v want=%+v", got, second)
	}
}

// TestR4_004_QuarantinedBackendHealthyOnlyAfterActivate drives the production
// restart effect. Detach is already exact-confirmed, but backend remains
// quarantined throughout StartProcess and becomes healthy only in activate.
func TestR4_004_QuarantinedBackendHealthyOnlyAfterActivate(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	sid := contract.SessionID("r4-004-healthy-after-activate")
	activateTestSession(t, adapter, sid)
	adapter.Catalog().StoreRecipe(sid, RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	rt := adapter.Runtime()
	rt.SetPTYRawPort(&scriptedDetachPTYRaw{receipts: []*testBackendDetachReceipt{newTestDetachReceipt(true, nil)}})
	entry := rt.Arbiter().entryFor(sid)
	if outcome := rt.Gate().(*controlGate).quarantineBackend(entry); outcome.disposition != BackendDetachQuarantinedDetached {
		t.Fatalf("precondition detach not confirmed: %+v", outcome)
	}

	launch := &stageInspectLaunchRaw{rt: rt}
	adapter.launchRaw = launch
	if err := runRestart(t, adapter, sid); err != nil {
		t.Fatalf("restart: %v", err)
	}
	launch.mu.Lock()
	if len(launch.backendStates) != 1 || launch.backendStates[0] != backendQuarantined {
		states := append([]backendHealth(nil), launch.backendStates...)
		launch.mu.Unlock()
		t.Fatalf("backend became healthy before activate: %v", states)
	}
	launch.mu.Unlock()
	entry.stateMu.Lock()
	backend, detach := entry.backend, entry.backendDetach
	entry.stateMu.Unlock()
	if backend != backendHealthy || detach != (backendDetachRecord{}) {
		t.Fatalf("activate did not publish clean healthy backend: backend=%d detach=%+v", backend, detach)
	}
}
