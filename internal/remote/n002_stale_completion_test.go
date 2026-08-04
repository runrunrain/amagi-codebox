package remote

// N-002: a lifecycle raw completion that linearizes after a fence (revoke /
// Server Stop / holder takeover / run swap advanced the epochs between the
// permit's last successful Checkpoint and the raw return) MUST NOT commit state
// — no tombstone, no physical entry delete, no success result. It only releases
// its own lane. Design §4A.1 ("result commit只接受同一
// permit/session/generation/intent/currentOp") + §5.5 ("stale completion不得
// 物理删entry").

import (
	"context"
	"errors"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// startDesktopRunForTest begins + activates a desktop run and returns the runtime.
func startDesktopRunForTest(t *testing.T, sid contract.SessionID) *ControlRuntime {
	t.Helper()
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()
	lp, rp, _, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), rp); err != nil {
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	_ = lp
	return rt
}

// TestN002_StaleLifecycleCompletionDoesNotCommit: a remove whose raw effect
// succeeds, but whose completion is fenced (controlEpoch advanced mid-effect by
// a concurrent takeover), must NOT tombstone/remove the entry and must return a
// stale-permit error rather than success.
func TestN002_StaleLifecycleCompletionDoesNotCommit(t *testing.T) {
	sid := contract.SessionID("s-n002")
	rt := startDesktopRunForTest(t, sid)
	auth := rt.DesktopAuthority()
	ctx := context.Background()

	// Sanity: entry is public before the lifecycle.
	if e := rt.Arbiter().entryFor(sid); e == nil || !isEntryPublic(e) {
		t.Fatal("entry should be public before lifecycle")
	}

	// Remove: raw effect passes Checkpoint (admits the syscall), then a concurrent
	// fence advances controlEpoch (what a desktop takeover / device acquire does
	// under stateMu). The raw "remove" returns success, but the completion is stale.
	_, err := rt.Gate().DoDesktopLifecycle(ctx, auth, sid, LifecycleRemove,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			if err := p.Checkpoint(ctx, 1); err != nil {
				return SessionMutationResult{}, err
			}
			// Simulate a fence linearizing after the Checkpoint (N-002 race window).
			p.entry.stateMu.Lock()
			p.entry.controlEpoch++
			p.entry.stateMu.Unlock()
			return SessionMutationResult{Removed: true}, nil
		})

	// Expect a stale-permit denial, NOT success.
	var ge *ControlGateError
	if !errors.As(err, &ge) || ge.Kind != DenyStalePermit {
		t.Fatalf("expected DenyStalePermit on stale completion, got %v", err)
	}
	// The entry must NOT have been tombstoned/removed.
	e := rt.Arbiter().entryFor(sid)
	if e == nil {
		t.Fatal("entry was physically deleted on a stale completion (N-002 regression)")
	}
	e.stateMu.Lock()
	removed := e.removed
	e.stateMu.Unlock()
	if removed {
		t.Fatal("entry was tombstoned on a stale completion (N-002 regression)")
	}
}

// TestN002_AuthoritativeCompletionCommits: the happy path — no fence — still
// commits the remove (guards against over-fencing).
func TestN002_AuthoritativeCompletionCommits(t *testing.T) {
	sid := contract.SessionID("s-n002-ok")
	rt := startDesktopRunForTest(t, sid)
	auth := rt.DesktopAuthority()
	ctx := context.Background()

	result, err := rt.Gate().DoDesktopLifecycle(ctx, auth, sid, LifecycleRemove,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			if err := p.Checkpoint(ctx, 1); err != nil {
				return SessionMutationResult{}, err
			}
			return SessionMutationResult{Removed: true}, nil
		})
	if err != nil {
		t.Fatalf("authoritative remove: %v", err)
	}
	if !result.Removed {
		t.Fatal("expected Removed=true on authoritative completion")
	}
	// Entry should be tombstoned + physically deleted.
	if e := rt.Arbiter().entryFor(sid); e != nil {
		t.Fatal("entry should be physically deleted after authoritative remove")
	}
}

// TestN002_StaleDeviceCompletionDoesNotCommit: same invariant on the device
// lifecycle path, fenced via a run swap (runEpoch/currentRun changed mid-effect).
func TestN002_StaleDeviceCompletionDoesNotCommit(t *testing.T) {
	sid := contract.SessionID("s-n002-dev")
	rt := startDesktopRunForTest(t, sid)
	auth := rt.DesktopAuthority()
	ctx := context.Background()

	// Desktop lifecycle fenced by a run swap (simulating a restart that minted a
	// new run while this stop was in flight). currentRun/runEpoch mismatch ⇒ stale.
	_, err := rt.Gate().DoDesktopLifecycle(ctx, auth, sid, LifecycleStop,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			if err := p.Checkpoint(ctx, 1); err != nil {
				return SessionMutationResult{}, err
			}
			// Concurrent run swap fence.
			p.entry.stateMu.Lock()
			p.entry.currentRun = &runIdentity{nonce: 99, desktopRunToken: "swap"}
			p.entry.runEpoch++
			p.entry.stateMu.Unlock()
			return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
		})
	var ge *ControlGateError
	if !errors.As(err, &ge) || ge.Kind != DenyStalePermit {
		t.Fatalf("expected DenyStalePermit on stale device completion, got %v", err)
	}
}
