package remote

// r4_005_clear_stopped_gate_test.go — R4-005: batch clear of stopped sessions
// must route EACH id through the desktop authority Gate (lifecycle intent +
// drain permit + authoritative N-002 completion), returning per-id typed
// results. Transaction invariants under test:
//
//  1. Per-id authority: every cleared id went through ClearStoppedDesktopSession
//     (lifecycle intent + permit + authoritative completion), not a state-only
//     bulk delete.
//  2. Race guard: a session that is no longer stopped (it restarted → runActive)
//     is SKIPPED, not force-removed, and its entry is retained.
//  3. Fail-closed: when the gate is not ready, ZERO control-managed ids are
//     cleared; all are retained as skipped.
//  4. Legacy / not-found ids are skipped (not errors).
//  5. Deterministic ordering: results are returned in sorted SessionID order.
//  6. Manager-sync contract: ClearedIDs() returns exactly the authoritatively
//     removed set, so the App deletes manager records only for those.

import (
	"context"
	"sort"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// beginAndActivateForClear begins + activates a desktop session and returns the
// run observation permit (to later observe an exit → terminal/stopped state).
func beginAndActivateForClear(t *testing.T, rt *ControlRuntime, sid contract.SessionID) *RunObservationPermit {
	t.Helper()
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun(%s): %v", sid, err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun(%s): %v", sid, err)
	}
	rt.Projector().TrackRun(sid, obsPermit)
	return obsPermit
}

// stopForClear transitions an active session to terminal (stopped) via an exit
// observation — mirrors the real PTY-exit flow that makes a session "stopped".
func stopForClear(t *testing.T, rt *ControlRuntime, sid contract.SessionID, obsPermit *RunObservationPermit) {
	t.Helper()
	if !rt.Arbiter().ObserveExit(obsPermit, ProcessExitObservation{ExitCode: 0, Failed: false}) {
		t.Fatalf("ObserveExit did not apply for %s (stale permit?)", sid)
	}
}

// assertEntryGone asserts the entry was physically deleted (cleared).
func assertEntryGone(t *testing.T, rt *ControlRuntime, sid contract.SessionID) {
	t.Helper()
	if _, phase, ok := rt.Arbiter().SessionStateMirror(sid); ok {
		t.Fatalf("R4-005: entry for %s still present after clear (phase=%d); want physically deleted", sid, phase)
	}
}

// assertEntryRetained asserts the entry is still present (not cleared).
func assertEntryRetained(t *testing.T, rt *ControlRuntime, sid contract.SessionID) {
	t.Helper()
	if _, _, ok := rt.Arbiter().SessionStateMirror(sid); !ok {
		t.Fatalf("R4-005: entry for %s was removed; want retained", sid)
	}
}

// TestR4_005_ClearStopped_AllTerminal_AllClearedAuthoritatively: three stopped
// sessions are all cleared through the gate; entries are physically deleted and
// ClearedIDs matches the input set.
func TestR4_005_ClearStopped_AllTerminal_AllClearedAuthoritatively(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()

	sids := []contract.SessionID{"r4-005-a", "r4-005-b", "r4-005-c"}
	for _, sid := range sids {
		obs := beginAndActivateForClear(t, rt, sid)
		stopForClear(t, rt, sid, obs)
	}

	res := rt.DesktopClearStopped(context.Background(), sids)

	if len(res.Results) != len(sids) {
		t.Fatalf("expected %d per-id results, got %d (%+v)", len(sids), len(res.Results), res.Results)
	}
	cleared := res.ClearedIDs()
	if len(cleared) != len(sids) {
		t.Fatalf("expected %d cleared, got %d", len(sids), len(cleared))
	}
	want := append([]contract.SessionID{}, sids...)
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	for i := range want {
		if cleared[i] != want[i] {
			t.Fatalf("cleared[%d]=%s want %s (sorted order)", i, cleared[i], want[i])
		}
	}
	for _, r := range res.Results {
		if r.Status != DesktopClearCleared {
			t.Fatalf("id %s: expected Cleared, got %s (%s)", r.ID, clearStatusName(r.Status), r.Reason)
		}
	}
	for _, sid := range sids {
		assertEntryGone(t, rt, sid)
	}
}

// TestR4_005_ClearStopped_RaceActiveSession_SkippedNotRemoved: two stopped +
// one still-active session. The active one MUST be skipped (race guard) and its
// entry retained; the two stopped ones are cleared.
func TestR4_005_ClearStopped_RaceActiveSession_SkippedNotRemoved(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()

	stoppedA := beginAndActivateForClear(t, rt, "r4-005-stopped-a")
	stopForClear(t, rt, "r4-005-stopped-a", stoppedA)
	stoppedB := beginAndActivateForClear(t, rt, "r4-005-stopped-b")
	stopForClear(t, rt, "r4-005-stopped-b", stoppedB)
	// Active session: the caller's snapshot said "stopped" but it restarted.
	beginAndActivateForClear(t, rt, "r4-005-active")

	res := rt.DesktopClearStopped(context.Background(), []contract.SessionID{
		"r4-005-stopped-a", "r4-005-active", "r4-005-stopped-b",
	})

	cleared := res.ClearedIDs()
	if len(cleared) != 2 {
		t.Fatalf("expected exactly 2 cleared (the stopped ones), got %d: %+v", len(cleared), cleared)
	}
	clearedSet := make(map[contract.SessionID]bool, len(cleared))
	for _, id := range cleared {
		clearedSet[id] = true
	}
	if !clearedSet["r4-005-stopped-a"] || !clearedSet["r4-005-stopped-b"] {
		t.Fatalf("expected stopped-a and stopped-b cleared, got %v", cleared)
	}
	if clearedSet["r4-005-active"] {
		t.Fatal("R4-005 race regression: the active (restarted) session was cleared; it must be skipped")
	}
	// The active session's entry is retained and still active.
	assertEntryRetained(t, rt, "r4-005-active")
	_, phase, _ := rt.Arbiter().SessionStateMirror("r4-005-active")
	if phase != runActive {
		t.Fatalf("R4-005: active session phase=%d, want runActive (must not be disturbed)", phase)
	}
	// Per-id result for the active one is Skipped.
	var activeRes *DesktopClearIDResult
	for i := range res.Results {
		if res.Results[i].ID == "r4-005-active" {
			activeRes = &res.Results[i]
			break
		}
	}
	if activeRes == nil || activeRes.Status != DesktopClearSkipped {
		t.Fatalf("expected active session result Skipped, got %+v", activeRes)
	}
	assertEntryGone(t, rt, "r4-005-stopped-a")
	assertEntryGone(t, rt, "r4-005-stopped-b")
}

// TestR4_005_ClearStopped_NotReady_FailClosedZeroCleared: when the gate is not
// ready, ZERO control-managed ids are cleared; all are returned as skipped and
// retained (fail-closed — clearing only the manager would diverge the stores).
func TestR4_005_ClearStopped_NotReady_FailClosedZeroCleared(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()
	sids := []contract.SessionID{"r4-005-nr-a", "r4-005-nr-b"}
	for _, sid := range sids {
		obs := beginAndActivateForClear(t, rt, sid)
		stopForClear(t, rt, sid, obs)
	}
	// Simulate the gate going not-ready (e.g. health-latched) AFTER registration,
	// so ClearStoppedDesktopSession's checkReady fails for every id.
	rt.Arbiter().healthLatched.Store(true)

	res := rt.DesktopClearStopped(context.Background(), sids)

	if got := len(res.ClearedIDs()); got != 0 {
		t.Fatalf("R4-005 fail-closed regression: %d cleared when gate not ready (want 0)", got)
	}
	if len(res.Results) != len(sids) {
		t.Fatalf("expected %d skipped results, got %d", len(sids), len(res.Results))
	}
	for _, r := range res.Results {
		if r.Status != DesktopClearSkipped {
			t.Fatalf("id %s: expected Skipped (not ready), got %s (%s)", r.ID, clearStatusName(r.Status), r.Reason)
		}
	}
	for _, sid := range sids {
		assertEntryRetained(t, rt, sid)
	}
}

// TestR4_005_ClearStopped_LegacyNotFound_Skipped: an id with no control entry
// (legacy / already cleared) is skipped, not an error.
func TestR4_005_ClearStopped_LegacyNotFound_Skipped(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()

	res := rt.DesktopClearStopped(context.Background(), []contract.SessionID{"r4-005-legacy", "r4-005-gone"})

	if len(res.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res.Results))
	}
	for _, r := range res.Results {
		if r.Status != DesktopClearSkipped {
			t.Fatalf("legacy id %s: expected Skipped, got %s (%s)", r.ID, clearStatusName(r.Status), r.Reason)
		}
	}
	if got := len(res.ClearedIDs()); got != 0 {
		t.Fatalf("expected 0 cleared for legacy ids, got %d", got)
	}
}

// TestR4_005_ClearStopped_ResultsAreSorted: results are returned in canonical
// (sorted) SessionID order regardless of input order.
func TestR4_005_ClearStopped_ResultsAreSorted(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()

	res := rt.DesktopClearStopped(context.Background(), []contract.SessionID{
		"r4-005-zeta", "r4-005-alpha", "r4-005-mike",
	})
	for i := 1; i < len(res.Results); i++ {
		if res.Results[i-1].ID > res.Results[i].ID {
			t.Fatalf("results not sorted: [%d]=%s > [%d]=%s", i-1, res.Results[i-1].ID, i, res.Results[i].ID)
		}
	}
}

// TestR4_005_ClearStopped_LifecycleInProgress_Skipped: a stopped session that
// is mid-restart (an in-flight lifecycle intent) is skipped, not clobbered.
func TestR4_005_ClearStopped_LifecycleInProgress_Skipped(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()

	// Two stopped sessions.
	obsA := beginAndActivateForClear(t, rt, "r4-005-lip-a")
	stopForClear(t, rt, "r4-005-lip-a", obsA)
	obsB := beginAndActivateForClear(t, rt, "r4-005-lip-b")
	stopForClear(t, rt, "r4-005-lip-b", obsB)

	// Simulate an in-flight lifecycle intent on A (as if a restart is mid-flight):
	// commit a Remove intent manually but do NOT consume it.
	entry := rt.Arbiter().entryFor("r4-005-lip-a")
	if entry == nil {
		t.Fatal("entry missing")
	}
	g := rt.Gate().(*controlGate)
	if _, gErr := g.commitLifecycleIntent(entry, LifecycleRestart); gErr != nil {
		t.Fatalf("seed lifecycle intent: %v", gErr)
	}

	res := rt.DesktopClearStopped(context.Background(), []contract.SessionID{"r4-005-lip-a", "r4-005-lip-b"})

	// A is skipped (lifecycle in progress); B is cleared.
	cleared := res.ClearedIDs()
	if len(cleared) != 1 || cleared[0] != "r4-005-lip-b" {
		t.Fatalf("expected only lip-b cleared, got %v", cleared)
	}
	var aRes *DesktopClearIDResult
	for i := range res.Results {
		if res.Results[i].ID == "r4-005-lip-a" {
			aRes = &res.Results[i]
		}
	}
	if aRes == nil || aRes.Status != DesktopClearSkipped {
		t.Fatalf("expected lip-a Skipped (lifecycle in progress), got %+v", aRes)
	}
}

// clearStatusName is a tiny helper for readable test failures.
func clearStatusName(s DesktopClearStatus) string {
	switch s {
	case DesktopClearCleared:
		return "Cleared"
	case DesktopClearSkipped:
		return "Skipped"
	case DesktopClearErrored:
		return "Errored"
	default:
		return "Unknown"
	}
}
