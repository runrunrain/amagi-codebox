package main

// r4_005_clear_stopped_manager_sync_test.go — R4-005 App-layer invariant: the
// session manager is synced ONLY to the authority-cleared set. A control-managed
// session that is no longer stopped at gate-clear time (it restarted after the
// App snapshot → runActive) is retained by BOTH the control gate AND the manager
// (no divergence), and is reported in the retained list.

import (
	"context"
	"testing"

	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

// TestR4_005_ClearStopped_RaceRestart_RetainedByBothStores proves that when a
// control-managed stopped session has restarted (runActive) by the time the
// authoritative clear runs, it is SKIPPED: the control entry is retained, the
// manager record is retained, and it is reported in the retained list.
func TestR4_005_ClearStopped_RaceRestart_RetainedByBothStores(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)

	// Create a manager record (stopped) + an ACTIVE gate entry under the same id
	// (simulates: snapshot said stopped, then it restarted → runActive).
	sess := app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "model", session.ModeEmbedded, "")
	sid := sess.ID
	app.Sessions.MarkStopped(sid)
	rt := app.control
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), contract.SessionID(sid))
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(contract.SessionID(sid), obsPermit)
	// Deliberately do NOT observe an exit: the gate entry is runActive (restarted).

	result := app.ClearStoppedSessionsDetailed()
	cleared, retained := result.Cleared, result.RetainedIDs
	if cleared != 0 {
		t.Fatalf("R4-005 race regression: %d cleared; the restarted (active) session must be retained", cleared)
	}
	if len(retained) != 1 || retained[0] != sid {
		t.Fatalf("expected the restarted session retained/reported, got %v", retained)
	}
	// Manager record retained (no divergence).
	if _, err := app.Sessions.Get(sid); err != nil {
		t.Fatal("R4-005: manager record was removed for an active (restarted) session; must be retained")
	}
	// Control entry retained and still active (NOT removed by the clear).
	mirror, _, ok := app.control.Arbiter().SessionStateMirror(contract.SessionID(sid))
	if !ok {
		t.Fatal("R4-005 race regression: the active (restarted) session's control entry was removed; it must be retained")
	}
	if mirror != contract.SessionStateRunning {
		t.Fatalf("R4-005: active session mirror=%s, want running (must not be disturbed by clear)", mirror)
	}
}

// TestR4_005_ClearStopped_ClearedIDRemovedFromManager proves that an
// authoritatively-cleared stopped session is removed from the manager (the two
// stores stay in sync on the success path).
func TestR4_005_ClearStopped_ClearedIDRemovedFromManager(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)
	sid := registerGateSession(t, app) // embedded + stopped (terminal) + gate entry

	result := app.ClearStoppedSessionsDetailed()
	cleared, retained := result.Cleared, result.RetainedIDs
	if cleared != 1 {
		t.Fatalf("expected 1 cleared, got %d (retained=%v)", cleared, retained)
	}
	if len(retained) != 0 {
		t.Fatalf("expected no retained sessions, got %v", retained)
	}
	if _, err := app.Sessions.Get(sid); err == nil {
		t.Fatal("R4-005: manager record not removed for an authoritatively-cleared session")
	}
	// Control entry gone.
	if err := app.control.DesktopRemove(context.Background(), contract.SessionID(sid)); err == nil {
		t.Fatal("expected DesktopRemove to deny after authoritative clear cleaned the entry")
	}
}

// TestR4_005_ClearStopped_PartialClearMixedSync proves a mixed batch where one
// embedded session restarted (retained) and another stayed stopped (cleared)
// results in a correct partial clear with both stores in sync.
func TestR4_005_ClearStopped_PartialClearMixedSync(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)

	// stopped + gate-terminal → will be cleared.
	stopped := registerGateSession(t, app)
	// stopped manager record but ACTIVE gate entry → retained (race).
	active := app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "model", session.ModeEmbedded, "")
	app.Sessions.MarkStopped(active.ID)
	rt := app.control
	lp, rp, op, err := rt.BeginDesktopRun(context.Background(), contract.SessionID(active.ID))
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), rp); err != nil {
		rt.AbortDesktopRun(context.Background(), lp, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(contract.SessionID(active.ID), op)

	result := app.ClearStoppedSessionsDetailed()
	cleared, retained := result.Cleared, result.RetainedIDs
	if cleared != 1 {
		t.Fatalf("expected exactly 1 cleared (the stopped one), got %d (retained=%v)", cleared, retained)
	}
	// stopped removed from manager; active retained.
	if _, err := app.Sessions.Get(stopped); err == nil {
		t.Fatal("stopped session should be removed from the manager")
	}
	if _, err := app.Sessions.Get(active.ID); err != nil {
		t.Fatal("active (restarted) session should be retained in the manager")
	}
}
