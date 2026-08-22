package main

// Round8 residual tests: cleanup ownership must survive an App-instance
// boundary, and an accepted asynchronous Stop must remain non-removable until
// the Launcher reports a real terminal receipt.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/cleanupstore"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/session"
)

type r8FailingCleanupStore struct{ cleanupstore.Store }

func (r8FailingCleanupStore) Register(cleanupstore.Record) error {
	return errors.New("injected durable write failure")
}

func TestR8_001_PostStartDurabilityFailureReturnsWithDurableReservation(t *testing.T) {
	app, fake, configDir := newR6ExternalLeaseApp(t)
	providerID := configureR6ClaudeProvider(t, app)
	underlying := cleanupstore.NewFileStore(configDir)
	app.externalCleanupStore = r8FailingCleanupStore{Store: underlying}
	fake.setAsyncStop(true)
	var sessionID string
	fake.onStarted = func(id string) { sessionID = id }

	if _, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, ""); err == nil {
		t.Fatal("durability failure unexpectedly returned success")
	}
	if sessionID == "" || !fake.IsRunning(sessionID) || r7ExternalCleanupCount(app) != 1 {
		t.Fatalf("in-process cleanup owner missing: id=%q running=%v claims=%d", sessionID, fake.IsRunning(sessionID), r7ExternalCleanupCount(app))
	}
	pending, err := underlying.LoadPending()
	if err != nil || len(pending) != 1 || pending[0].SessionID != sessionID {
		t.Fatalf("durable pre-start reservation missing: pending=%+v err=%v", pending, err)
	}

	fake.finish(sessionID)
	waitR6(t, func() bool {
		pending, loadErr := underlying.LoadPending()
		return loadErr == nil && len(pending) == 0
	}, "terminal reservation completion")
}

func TestR8_001_ShutdownFenceRejectsLateExternalProcessStart(t *testing.T) {
	app, fake, _ := newR6ExternalLeaseApp(t)
	providerID := configureR6ClaudeProvider(t, app)
	app.externalShutdown.Store(true)

	if _, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, ""); !errors.Is(err, remote.ErrSharedCoordinatorClosed) {
		t.Fatalf("late external launch error=%v want ErrSharedCoordinatorClosed", err)
	}
	fake.mu.Lock()
	runningCount := len(fake.running)
	fake.mu.Unlock()
	if runningCount != 0 {
		t.Fatalf("shutdown-fenced launch started %d process(es)", runningCount)
	}
	active, err := app.externalCleanupStore.LoadActive()
	if err != nil || len(active) != 0 {
		t.Fatalf("shutdown-fenced launch wrote ownership for nonexistent process: active=%+v err=%v", active, err)
	}
}

func TestR8_001_ActiveRunShutdownStopAllFailureRemainsDurable(t *testing.T) {
	app, fake, _ := newR6ExternalLeaseApp(t)
	providerID := configureR6ClaudeProvider(t, app)
	id, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, "")
	if err != nil {
		t.Fatalf("LaunchSession: %v", err)
	}
	active, err := app.externalCleanupStore.LoadActive()
	if err != nil || len(active) != 1 || active[0].SessionID != id {
		t.Fatalf("successful external run durable ownership=%+v err=%v", active, err)
	}

	fake.setStopError(errors.New("injected shutdown StopAll failure"))
	app.closeSharedLeasesForShutdown()
	app.stopExternalProcessesForShutdown()
	active, err = app.externalCleanupStore.LoadActive()
	if err != nil || len(active) != 1 || !fake.IsRunning(id) {
		t.Fatalf("Shutdown discarded live durable ownership: active=%+v running=%v err=%v", active, fake.IsRunning(id), err)
	}

	fake.finish(id)
	waitR6(t, func() bool {
		active, loadErr := app.externalCleanupStore.LoadActive()
		return loadErr == nil && len(active) == 0
	}, "active-run durable completion after late terminal")
}

func TestR8_001_DurableCleanupReloadContinuesReaperAcrossAppInstance(t *testing.T) {
	app1, fake1, configDir := newR6ExternalLeaseApp(t)
	app1.configDir = configDir
	app1.externalCleanupStore = cleanupstore.NewFileStore(configDir)
	configureR6CodexHeadroom(t, app1, configDir)

	var sessionID string
	fake1.setStopError(errors.New("injected StopAll-era failure"))
	fake1.onStarted = func(id string) {
		sessionID = id
		app1.sharedCoord.ClearAll() // force post-start promotion failure
	}
	if _, err := app1.LaunchCodexSession("", "", "terminal", t.TempDir(), ""); !errors.Is(err, remote.ErrSharedCoordinatorClosed) {
		t.Fatalf("first App launch error=%v want ErrSharedCoordinatorClosed", err)
	}
	if sessionID == "" || !fake1.IsRunning(sessionID) {
		t.Fatalf("first App did not retain live process: id=%q", sessionID)
	}
	app1.stopExternalProcessesForShutdown() // StopAll fails; graceful hook phase returns
	if !fake1.IsRunning(sessionID) || r7ExternalCleanupCount(app1) != 1 {
		t.Fatal("Shutdown external phase discarded an unconfirmed live cleanup owner")
	}
	active, err := app1.externalCleanupStore.LoadActive()
	if err != nil {
		t.Fatalf("LoadActive after register: %v", err)
	}
	if len(active) != 1 || active[0].SessionID != sessionID || active[0].PID != 6102 || active[0].ProcessIdentity == "" {
		t.Fatalf("durable cleanup record=%+v want exact session/pid/identity", active)
	}

	// Simulate a new host process: a fresh App, coordinator, Launcher facade and
	// store instance load only the durable file. The fake's RecoverProcess models
	// OS identity verification finding the same still-live process.
	app2 := newTestApp(t)
	app2.configDir = configDir
	app2.ctx = context.Background()
	app2.sharedCoord = remote.NewSharedServiceCoordinator()
	app2.sharedLeases = make(map[remote.SharedLeaseOwnerKey]*remote.SharedDependencyLease)
	app2.externalRunPollInterval = 5 * time.Millisecond
	app2.externalCleanupStore = cleanupstore.NewFileStore(configDir)
	fake2 := newR6ExternalLauncher()
	fake2.setRecoverRunning(true)
	fake2.setStopError(errors.New("recovered process still resists Stop"))
	app2.externalLauncher = fake2

	if err := app2.recoverExternalCleanups(); err != nil {
		t.Fatalf("recoverExternalCleanups: %v", err)
	}
	if got := r7ExternalCleanupCount(app2); got != 1 {
		t.Fatalf("reloaded cleanup owners=%d want 1", got)
	}
	if !fake2.IsRunning(sessionID) {
		t.Fatal("fresh Launcher did not adopt the identity-verified process")
	}
	if got := app2.sharedCoord.LaunchAdmissionCount(remote.SharedServiceCodexHeadroom); got != 1 {
		t.Fatalf("fresh coordinator recovery admissions=%d want 1", got)
	}
	if _, err := app2.sharedCoord.AcquireMutationAdmission(remote.SharedServiceCodexHeadroom, remote.MutationStop); !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Fatalf("reloaded live cleanup did not fail-close mutation: %v", err)
	}

	fake2.setStopError(nil)
	waitR6(t, func() bool {
		active, loadErr := app2.externalCleanupStore.LoadActive()
		return loadErr == nil && len(active) == 0 && !fake2.IsRunning(sessionID) &&
			r7ExternalCleanupCount(app2) == 0 &&
			app2.sharedCoord.LaunchAdmissionCount(remote.SharedServiceCodexHeadroom) == 0
	}, "reloaded reaper terminal + durable completion")

	// A third store/App instance proves completion itself is durable and does not
	// resurrect the process claim on another restart.
	app3 := newTestApp(t)
	app3.configDir = configDir
	app3.ctx = context.Background()
	app3.sharedCoord = remote.NewSharedServiceCoordinator()
	app3.externalCleanupStore = cleanupstore.NewFileStore(configDir)
	app3.externalLauncher = newR6ExternalLauncher()
	if err := app3.recoverExternalCleanups(); err != nil {
		t.Fatalf("third-App recovery: %v", err)
	}
	if got := r7ExternalCleanupCount(app3); got != 0 {
		t.Fatalf("completed durable claim resurrected: %d", got)
	}

	// Let the first in-process reaper exit; duplicate exact completion is safe.
	fake1.finish(sessionID)
	waitR6(t, func() bool { return r7ExternalCleanupCount(app1) == 0 }, "first App test reaper exit")
}

func launchR8AsyncStoppingSession(t *testing.T) (*App, *r6ExternalLauncher, string) {
	t.Helper()
	app, fake, _ := newR6ExternalLeaseApp(t)
	providerID := configureR6ClaudeProvider(t, app)
	fake.setAsyncStop(true) // Stop accepted; Wait/terminal deliberately delayed
	id, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, "")
	if err != nil {
		t.Fatalf("LaunchSession: %v", err)
	}
	if err := app.StopSession(id); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	if !fake.IsRunning(id) {
		t.Fatal("delayed-Wait fake became terminal too early")
	}
	if status := app.Sessions.GetStatus(id); status != session.StatusStopping {
		t.Fatalf("accepted async Stop status=%q want stopping", status)
	}
	if got := app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom); got != 1 {
		t.Fatalf("stopping session lease count=%d want 1", got)
	}
	return app, fake, id
}

func TestR8_002_StoppingSessionRemoveFailsTypedUntilTerminal(t *testing.T) {
	app, fake, id := launchR8AsyncStoppingSession(t)
	const contenders = 16
	errs := make(chan error, contenders)
	var wg sync.WaitGroup
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- app.RemoveSession(id)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, session.ErrSessionStopping) {
			t.Fatalf("RemoveSession during Wait error=%v want ErrSessionStopping", err)
		}
	}
	if got := app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom); got != 1 {
		t.Fatalf("RemoveSession race released active lease: %d", got)
	}
	if err := app.HeadroomStop(); !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Fatalf("HeadroomStop during stopping process error=%v", err)
	}

	fake.finish(id)
	waitR6(t, func() bool {
		return app.Sessions.GetStatus(id) == session.StatusStopped &&
			app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom) == 0
	}, "Stop terminal receipt")
	if err := app.RemoveSession(id); err != nil {
		t.Fatalf("RemoveSession after terminal: %v", err)
	}
}

func TestR8_002_StoppingSessionClearFailsTypedUntilTerminal(t *testing.T) {
	app, fake, id := launchR8AsyncStoppingSession(t)
	result := app.ClearStoppedSessionsDetailed()
	if result.Cleared != 0 || len(result.ClearedIDs) != 0 {
		t.Fatalf("ClearStopped cleared stopping process: %+v", result)
	}
	if !containsR8String(result.RetainedIDs, id) {
		t.Fatalf("ClearStopped did not retain stopping process: %+v", result)
	}
	if len(result.Failed) != 1 || result.Failed[0].ID != id || result.Failed[0].Reason != session.ErrSessionStopping.Error() {
		t.Fatalf("ClearStopped typed stopping rejection=%+v", result.Failed)
	}
	if got := app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom); got != 1 {
		t.Fatalf("ClearStopped race released active lease: %d", got)
	}

	fake.finish(id)
	waitR6(t, func() bool {
		return app.Sessions.GetStatus(id) == session.StatusStopped &&
			app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom) == 0
	}, "clear-path Stop terminal receipt")
	result = app.ClearStoppedSessionsDetailed()
	if result.Cleared != 1 || !containsR8String(result.ClearedIDs, id) {
		t.Fatalf("ClearStopped after terminal=%+v", result)
	}
}

func containsR8String(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
