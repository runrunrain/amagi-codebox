package main

// m3_005_desktop_ledger_destroy_test.go — M3-005 cross-package regression: the
// desktop RemoveSession / ClearStoppedSessionsDetailed commit points MUST
// release the CG-03 per-session input ledger via Server.DestroySessionInputLedger
// so the registry has no unbounded per-session residual window.
//
// Covers the two desktop paths the remote REST remove path did NOT cover:
//   · App.RemoveSession — gate-managed (DesktopRemove) + legacy (Manager.Remove).
//   · App.ClearStoppedSessionsDetailed — per-ID successful Manager.Remove.
//
// diting R2裁定 (M3-005 NOT-CLOSED) 要求把 Destroy 接到所有权威 session-remove
// commit 的统一通知点；本测试断言 desktop 入口确实验证释放 ledger（failed/retained
// 不误删）。

import (
	"testing"

	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

// wireLedgerRegistry installs a Server + ledger registry on a newTestAppWithRemote
// App so desktop remove/clear can be observed for ledger release. Returns the App.
func wireLedgerRegistry(t *testing.T) *App {
	t.Helper()
	app := newTestAppWithRemote(t)
	app.ctx = t.Context()
	// SetSessionAdapter initializes inputLedgers (lazy-created per session).
	app.Remote.SetSessionAdapter(&remote.RemoteSessionAdapter{})
	return app
}

// ledgerExists reports whether a ledger instance for sessionID is still the same
// object as `before` (i.e., it was NOT destroyed). After Destroy, LedgerForSession
// returns a brand-new instance (lazy re-create), so identity inequality proves release.
func ledgerReleased(app *App, sid string, before *remote.SessionInputLedger) bool {
	after := app.Remote.LedgerForSession(contract.SessionID(sid))
	return before != nil && after != before
}

// TestM3_005_RemoveSession_LegacyDestroysLedger: the legacy/external remove path
// (control not wired) calls Manager.Remove; after a successful commit the ledger
// MUST be released.
func TestM3_005_RemoveSession_LegacyDestroysLedger(t *testing.T) {
	app := wireLedgerRegistry(t)
	// control intentionally NOT wired (nil) → legacy/manager path.
	sess := app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "model", session.ModeEmbedded, "", false)
	app.Sessions.MarkStopped(sess.ID)
	// Populate the ledger (simulate a prior canonical input on this session).
	before := app.Remote.LedgerForSession(contract.SessionID(sess.ID))
	if before == nil {
		t.Fatal("LedgerForSession must create a ledger after SetSessionAdapter")
	}
	if err := app.RemoveSession(sess.ID); err != nil {
		t.Fatalf("RemoveSession legacy path: %v", err)
	}
	if !ledgerReleased(app, sess.ID, before) {
		t.Fatal("RemoveSession legacy commit must release the per-session ledger (M3-005)")
	}
}

// TestM3_005_RemoveSession_GateManagedDestroysLedger: the gate-managed remove path
// (control ready, DesktopRemove succeeds) MUST release the ledger too.
func TestM3_005_RemoveSession_GateManagedDestroysLedger(t *testing.T) {
	app := wireLedgerRegistry(t)
	wireTestControl(t, app)
	sid := registerGateSession(t, app)

	rec := &recordingLifecycle{delegateTo: app}
	app.control.SetPTYLifecycleRawPort(rec)

	// Populate the ledger before remove.
	before := app.Remote.LedgerForSession(contract.SessionID(sid))
	if before == nil {
		t.Fatal("LedgerForSession must create a ledger after SetSessionAdapter")
	}
	if err := app.RemoveSession(sid); err != nil {
		t.Fatalf("RemoveSession gate-managed path: %v", err)
	}
	if !ledgerReleased(app, sid, before) {
		t.Fatal("RemoveSession gate-managed commit must release the per-session ledger (M3-005)")
	}
}

// TestM3_005_ClearStopped_LegacyDestroysLedger: batch clear per-ID successful
// Manager.Remove MUST release each cleared session's ledger.
func TestM3_005_ClearStopped_LegacyDestroysLedger(t *testing.T) {
	app := wireLedgerRegistry(t)
	a := app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "model", session.ModeTerminal, "", false)
	b := app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "model", session.ModeTerminal, "", false)
	app.Sessions.MarkStopped(a.ID)
	app.Sessions.MarkStopped(b.ID)

	// Populate ledgers for both (terminal/legacy sessions; no control entry).
	beforeA := app.Remote.LedgerForSession(contract.SessionID(a.ID))
	beforeB := app.Remote.LedgerForSession(contract.SessionID(b.ID))
	if beforeA == nil || beforeB == nil {
		t.Fatal("LedgerForSession must create ledgers after SetSessionAdapter")
	}

	result := app.ClearStoppedSessionsDetailed()
	if result.Cleared != 2 {
		t.Fatalf("ClearStoppedSessions cleared %d, want 2; result=%+v", result.Cleared, result)
	}
	if !ledgerReleased(app, a.ID, beforeA) {
		t.Fatal("ClearStoppedSessions must release ledger for cleared session A (M3-005)")
	}
	if !ledgerReleased(app, b.ID, beforeB) {
		t.Fatal("ClearStoppedSessions must release ledger for cleared session B (M3-005)")
	}
}

// TestM3_005_ClearStopped_RetainedDoesNotDestroyLedger: a session whose
// Manager.Remove FAILS is retained; its ledger MUST NOT be destroyed (the
// session may still be referenced). Asserts the review's "失败/retained项不得
// 误删ledger" requirement.
func TestM3_005_ClearStopped_RetainedDoesNotDestroyLedger(t *testing.T) {
	app := wireLedgerRegistry(t)
	failed := app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "model", session.ModeTerminal, "", false)
	app.Sessions.MarkStopped(failed.ID)

	// Populate the ledger.
	before := app.Remote.LedgerForSession(contract.SessionID(failed.ID))
	if before == nil {
		t.Fatal("LedgerForSession must create a ledger after SetSessionAdapter")
	}

	// Inject a per-ID Manager.Remove failure for the failed session.
	missingManager := session.NewManager()
	app.sessionRemove = func(id string) error {
		if id == failed.ID {
			return missingManager.Remove(id) // genuine "not found" error
		}
		return app.Sessions.Remove(id)
	}

	result := app.ClearStoppedSessionsDetailed()
	if result.Cleared != 0 {
		t.Fatalf("retained session must not be counted as cleared: %+v", result)
	}
	// The ledger MUST survive (same instance) because the remove FAILED.
	after := app.Remote.LedgerForSession(contract.SessionID(failed.ID))
	if before != after {
		t.Fatal("ClearStoppedSessions must NOT destroy the ledger for a retained/failed session (M3-005)")
	}
}
