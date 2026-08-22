package main

// bind_failclosed_test.go — M-005 behavioral assertions: the bound App
// mutation methods (StopSession / RemoveSession / ClearStoppedSessions) truly
// route through the control gate, not just exist on the surface. Proves the
// fail-closed + gate-authoritative semantics by recording gate-side effects.

import (
	"context"
	"sync"
	"testing"

	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

// recordingLifecycle is a PTYLifecycleRawPort that records Close/Remove calls
// (proof the gate path was taken) and delegates Remove to the session manager.
type recordingLifecycle struct {
	mu         sync.Mutex
	closes     []string
	removes    []string
	delegateTo *App
}

func (r *recordingLifecycle) CloseSession(sessionID contract.SessionID) error {
	r.mu.Lock()
	r.closes = append(r.closes, string(sessionID))
	r.mu.Unlock()
	if r.delegateTo != nil {
		return r.delegateTo.Pty.Close(string(sessionID))
	}
	return nil
}

func (r *recordingLifecycle) RemoveSession(sessionID contract.SessionID) error {
	r.mu.Lock()
	r.removes = append(r.removes, string(sessionID))
	r.mu.Unlock()
	if r.delegateTo != nil {
		return r.delegateTo.Sessions.Remove(string(sessionID))
	}
	return nil
}

// wireTestControl installs a ready ControlRuntime on the App (mirrors Startup
// wiring) and returns it for direct inspection.
func wireTestControl(t *testing.T, app *App) *remote.ControlRuntime {
	t.Helper()
	app.control = remote.NewControlRuntime(remote.NewSystemClock(), app.Log)
	app.control.SetPTYRawPort(appPTYRaw{app.Pty})
	app.control.SetPTYLifecycleRawPort(appPTYLifecycle{a: app})
	app.control.SetWailsContext(context.Background())
	app.control.MarkReady()
	return app.control
}

// registerGateSession creates a manager record (stopped) + begins/activates a
// desktop run so the gate owns the session entry under the SAME id, then
// terminalizes the gate entry (exit observation) so the gate state matches the
// manager's "stopped" status (R4-005: ClearStoppedDesktopSession only clears
// terminal entries). Returns the session id. (The PTY stub cannot start a real
// process; the gate entry is what we assert against.)
func registerGateSession(t *testing.T, app *App) string {
	t.Helper()
	sess := app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "model", session.ModeEmbedded, "")
	sid := sess.ID
	app.Sessions.MarkStopped(sid) // manager.Remove rejects running records
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
	// R4-005: a stopped session's gate entry must be terminal (matches the
	// manager's stopped state) so the authoritative clear path accepts it.
	if !rt.Arbiter().ObserveExit(obsPermit, remote.ProcessExitObservation{ExitCode: 0, Failed: false}) {
		t.Fatalf("ObserveExit did not apply for %s (stale permit?)", sid)
	}
	return sid
}

// TestM005_RemoveSession_RoutesThroughGate proves RemoveSession cleans a
// gate-managed session via the gate's lifecycle port (Close + Remove recorded),
// not a raw manager bypass.
func TestM005_RemoveSession_RoutesThroughGate(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)
	sid := registerGateSession(t, app)

	rec := &recordingLifecycle{delegateTo: app}
	app.control.SetPTYLifecycleRawPort(rec)

	if err := app.RemoveSession(sid); err != nil {
		t.Fatalf("RemoveSession: %v", err)
	}
	rec.mu.Lock()
	closes, removes := rec.closes, rec.removes
	rec.mu.Unlock()
	if len(removes) == 0 || removes[0] != sid {
		t.Fatalf("RemoveSession did not route through the gate lifecycle port: removes=%v closes=%v", removes, closes)
	}
}

// TestM005_ClearStoppedSessions_RoutesThroughGate proves ClearStoppedSessions
// cleans stopped sessions' control entries through the desktop authority
// (RemoveDesktopSession recorded for the stopped session).
func TestM005_ClearStoppedSessions_RoutesThroughGate(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)
	sid := registerGateSession(t, app)

	if n := app.ClearStoppedSessions(); n != 1 {
		t.Fatalf("ClearStoppedSessions cleared %d, want 1", n)
	}
	// The gate entry is gone: a subsequent DesktopRemove returns a denial
	// (DenySessionNotFound), proving the batch clear cleaned it via the gate.
	if err := app.control.DesktopRemove(context.Background(), contract.SessionID(sid)); err == nil {
		t.Fatal("expected DesktopRemove to deny after ClearStoppedSessions cleaned the entry, got nil")
	}
}

// TestM005_StopSession_NoRawCloseBypass proves the raw Pty.Close bypass branch
// is gone: StopSession on an unknown/external session is a clean no-op (no raw
// PTY close, no panic). The stub PTY reports IsRunning=false, so this exercises
// the non-PTY path; the source no longer contains a raw Pty.Close fallback for
// PTY-running sessions (structurally fail-closed).
func TestM005_StopSession_NoRawCloseBypass(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)
	if err := app.StopSession("unknown-external"); err != nil {
		t.Fatalf("StopSession on unknown external session should be a no-op, got %v", err)
	}
}

// TestM005_RemoveSession_LegacyRecordWhenControlUnavailable proves RemoveSession
// still cleans a pure legacy record (no control entry, PTY not running) via the
// manager when control is unavailable — the only legitimate non-gate path.
func TestM005_RemoveSession_LegacyRecordWhenControlUnavailable(t *testing.T) {
	app := newTestApp(t)
	// control intentionally NOT wired (nil) — simulates pre-Startup.
	rec := app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "model", session.ModeEmbedded, "")
	app.Sessions.MarkStopped(rec.ID)
	if err := app.RemoveSession(rec.ID); err != nil {
		t.Fatalf("RemoveSession legacy record with control unavailable: %v", err)
	}
	if _, err := app.Sessions.Get(rec.ID); err == nil {
		t.Fatal("legacy record should be removed from the manager")
	}
}
