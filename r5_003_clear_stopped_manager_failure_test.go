package main

// R4-005 residual regression: App must count only successful Manager.Remove
// calls and return a typed partial result that distinguishes cleared, retained,
// and manager-failed IDs. Errors are injected per ID through the App's narrow
// manager-removal seam; successful IDs still delegate to the real Manager.

import (
	"strings"
	"testing"

	"amagi-codebox/internal/session"
)

func containsR5ID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestR4_005_ManagerRemoveFailureIsPropagatedAndCountedAsPartial(t *testing.T) {
	app := newTestApp(t)

	first := app.Sessions.Create(session.AppTypeCodex, "codex", "", "m1", session.ModeTerminal, t.TempDir())
	failed := app.Sessions.Create(session.AppTypeCodex, "codex", "", "m2", session.ModeTerminal, t.TempDir())
	third := app.Sessions.Create(session.AppTypeCodex, "codex", "", "m3", session.ModeTerminal, t.TempDir())
	for _, id := range []string{first.ID, failed.ID, third.ID} {
		app.Sessions.MarkStopped(id)
	}

	// Produce a genuine Manager.Remove missing-record error for exactly one ID;
	// all other calls hit the App's real manager.
	missingManager := session.NewManager()
	app.sessionRemove = func(id string) error {
		if id == failed.ID {
			return missingManager.Remove(id)
		}
		return app.Sessions.Remove(id)
	}

	result := app.ClearStoppedSessionsDetailed()
	if result.Cleared != 2 {
		t.Fatalf("cleared=%d want 2 actual Manager.Remove successes; result=%+v", result.Cleared, result)
	}
	if len(result.ClearedIDs) != 2 || !containsR5ID(result.ClearedIDs, first.ID) || !containsR5ID(result.ClearedIDs, third.ID) {
		t.Fatalf("cleared IDs=%v want %s and %s", result.ClearedIDs, first.ID, third.ID)
	}
	if len(result.Failed) != 1 || result.Failed[0].ID != failed.ID || !strings.Contains(result.Failed[0].Reason, "session not found") {
		t.Fatalf("failed results=%+v want one Manager.Remove error for %s", result.Failed, failed.ID)
	}
	if len(result.RetainedIDs) != 1 || result.RetainedIDs[0] != failed.ID {
		t.Fatalf("retained IDs=%v want failed ID %s", result.RetainedIDs, failed.ID)
	}
	if _, err := app.Sessions.Get(failed.ID); err != nil {
		t.Fatal("manager-failed record must remain in the App manager")
	}
	if _, err := app.Sessions.Get(first.ID); err == nil {
		t.Fatal("first successfully removed record still exists")
	}
	if _, err := app.Sessions.Get(third.ID); err == nil {
		t.Fatal("third successfully removed record still exists")
	}
}

func TestR4_005_ManagerRemoveRunningRaceIsPropagatedAndRetained(t *testing.T) {
	app := newTestApp(t)
	candidate := app.Sessions.Create(session.AppTypeCodex, "codex", "", "race", session.ModeTerminal, t.TempDir())
	app.Sessions.MarkStopped(candidate.ID)

	// Model snapshot→remove changing back to running with a genuine
	// Manager.Remove error. The narrow seam maps that injected manager outcome
	// to the candidate ID without adding a production-only state mutator.
	tracingManager := session.NewManager()
	running := tracingManager.Create(session.AppTypeCodex, "codex", "", "running", session.ModeTerminal, t.TempDir())
	app.sessionRemove = func(string) error {
		return tracingManager.Remove(running.ID)
	}

	result := app.ClearStoppedSessionsDetailed()
	if result.Cleared != 0 || len(result.ClearedIDs) != 0 {
		t.Fatalf("running-race Manager.Remove was falsely counted: %+v", result)
	}
	if len(result.RetainedIDs) != 1 || result.RetainedIDs[0] != candidate.ID {
		t.Fatalf("retained IDs=%v want %s", result.RetainedIDs, candidate.ID)
	}
	if len(result.Failed) != 1 || result.Failed[0].ID != candidate.ID || !strings.Contains(result.Failed[0].Reason, "cannot remove running session") {
		t.Fatalf("running-race error was not propagated: %+v", result.Failed)
	}
	if _, err := app.Sessions.Get(candidate.ID); err != nil {
		t.Fatal("candidate must remain after running-race Manager.Remove failure")
	}
}

func TestR4_005_ControlClearedButManagerFailedIsNotReportedSuccessful(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)
	sid := registerGateSession(t, app)

	app.sessionRemove = func(id string) error {
		return session.NewManager().Remove(id)
	}
	result := app.ClearStoppedSessionsDetailed()
	if result.Cleared != 0 || len(result.ClearedIDs) != 0 {
		t.Fatalf("manager failure was falsely reported successful: %+v", result)
	}
	if len(result.Failed) != 1 || result.Failed[0].ID != sid {
		t.Fatalf("manager failure missing from partial result: %+v", result)
	}
	if _, err := app.Sessions.Get(sid); err != nil {
		t.Fatal("manager record must remain when Manager.Remove fails")
	}
}
