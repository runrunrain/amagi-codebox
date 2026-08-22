package main

// clear_stopped_failclosed_test.go — R3-005 regression: ClearStoppedSessions
// must (a) route control-managed (embedded) stopped sessions through the desktop
// authority before clearing the manager, and (b) fail-closed when the control
// runtime is unavailable — skipping control-managed records entirely (neither
// control entry nor manager record is touched) so the two stores never diverge
// with a dangling control entry. Legacy/terminal sessions are always eligible.

import (
	"context"
	"testing"

	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

// TestR3_005_ClearStopped_ControlManagedFailClosedWhenControlUnavailable proves
// a control-managed (embedded) stopped session is NOT cleared when the control
// runtime is unavailable: ClearStoppedSessionsDetailed returns it in the skipped
// list and the manager still holds the record (no dangling mismatch).
func TestR3_005_ClearStopped_ControlManagedFailClosedWhenControlUnavailable(t *testing.T) {
	app := newTestApp(t)
	// control intentionally NOT wired (nil) — simulates pre-Startup / shutdown.
	emb := app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "model", session.ModeEmbedded, "")
	app.Sessions.MarkStopped(emb.ID)

	result := app.ClearStoppedSessionsDetailed()
	cleared, skipped := result.Cleared, result.RetainedIDs
	if cleared != 0 {
		t.Fatalf("control-managed stopped session must NOT be cleared when control unavailable: cleared=%d", cleared)
	}
	if len(skipped) != 1 || skipped[0] != emb.ID {
		t.Fatalf("expected the embedded session to be reported skipped, got %v", skipped)
	}
	// The manager record is retained (fail-closed: no partial clear).
	if _, err := app.Sessions.Get(emb.ID); err != nil {
		t.Fatal("fail-closed regression: embedded stopped session was removed from the manager while control was unavailable")
	}
}

// TestR3_005_ClearStopped_ControlManagedClearedWhenControlReady proves that once
// the control runtime is ready, a control-managed stopped session is cleared
// through the desktop authority AND removed from the manager (the two stores
// stay in sync), and the control entry is gone.
func TestR3_005_ClearStopped_ControlManagedClearedWhenControlReady(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)
	sid := registerGateSession(t, app) // embedded + stopped + gate entry

	result := app.ClearStoppedSessionsDetailed()
	cleared, skipped := result.Cleared, result.RetainedIDs
	if cleared != 1 {
		t.Fatalf("expected 1 cleared, got %d (skipped=%v)", cleared, skipped)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected no skipped sessions with control ready, got %v", skipped)
	}
	// Manager record gone.
	if _, err := app.Sessions.Get(sid); err == nil {
		t.Fatal("expected the embedded stopped session to be removed from the manager after gate-authorized clear")
	}
	// Control entry gone: a subsequent DesktopRemove is denied (entry no longer exists).
	if err := app.control.DesktopRemove(context.Background(), contract.SessionID(sid)); err == nil {
		t.Fatal("expected DesktopRemove to deny after ClearStoppedSessions cleaned the entry, got nil")
	}
}

// TestR3_005_ClearStopped_LegacyTerminalAlwaysEligible proves a terminal-mode
// (external launcher, no control entry by construction) stopped session is
// cleared from the manager regardless of whether control is available — it is
// never skipped. This preserves the legacy-record cleanup path.
func TestR3_005_ClearStopped_LegacyTerminalAlwaysEligible(t *testing.T) {
	app := newTestApp(t)
	// control intentionally NOT wired.
	term := app.Sessions.Create(session.AppTypeCodex, "codex", "", "gpt-5", session.ModeTerminal, t.TempDir())
	app.Sessions.MarkStopped(term.ID)

	result := app.ClearStoppedSessionsDetailed()
	cleared, skipped := result.Cleared, result.RetainedIDs
	if cleared != 1 {
		t.Fatalf("legacy terminal session should be cleared even without control: cleared=%d", cleared)
	}
	if len(skipped) != 0 {
		t.Fatalf("legacy terminal session must never be skipped, got %v", skipped)
	}
	if _, err := app.Sessions.Get(term.ID); err == nil {
		t.Fatal("expected the legacy terminal stopped session to be removed from the manager")
	}
}

// TestR3_005_ClearStopped_MixedBatchPartialClear proves a mixed batch (embedded
// + terminal) with control unavailable clears ONLY the terminal records and
// skips the embedded ones — a well-defined partial-clear semantic (not a silent
// mismatch).
func TestR3_005_ClearStopped_MixedBatchPartialClear(t *testing.T) {
	app := newTestApp(t)
	// control NOT wired.
	emb := app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "model", session.ModeEmbedded, "")
	app.Sessions.MarkStopped(emb.ID)
	term := app.Sessions.Create(session.AppTypeCodex, "codex", "", "gpt-5", session.ModeTerminal, t.TempDir())
	app.Sessions.MarkStopped(term.ID)

	result := app.ClearStoppedSessionsDetailed()
	cleared, skipped := result.Cleared, result.RetainedIDs
	if cleared != 1 {
		t.Fatalf("only the legacy terminal session should be cleared: cleared=%d", cleared)
	}
	if len(skipped) != 1 || skipped[0] != emb.ID {
		t.Fatalf("expected the embedded session skipped, got %v", skipped)
	}
	// Terminal removed; embedded retained.
	if _, err := app.Sessions.Get(term.ID); err == nil {
		t.Fatal("terminal session should be removed")
	}
	if _, err := app.Sessions.Get(emb.ID); err != nil {
		t.Fatal("embedded session should be retained (fail-closed)")
	}
}

// Compile-time assertion that the typed detailed method exists on App and the
// remote result type is exported (guards against accidental API removal).
var _ = func(a *App) ClearStoppedSessionsResult { return a.ClearStoppedSessionsDetailed() }
var _ remote.DesktopClearStoppedResult
var _ contract.SessionID
