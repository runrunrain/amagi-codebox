package main

// R6-001 production wiring tests: successful external Claude/Codex Headroom
// launches promote their startup admission to an opaque external-run lease and
// retain it through the real App lifecycle terminal paths.

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/cleanupstore"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
)

type r6ExternalLauncher struct {
	mu             sync.Mutex
	running        map[string]bool
	identities     map[string]string
	launchErr      error
	codexLaunchErr error
	stopErr        error
	stopCalls      int
	asyncStop      bool
	recoverRunning bool
	recoverErr     error
	onStarted      func(string)
}

func newR6ExternalLauncher() *r6ExternalLauncher {
	return &r6ExternalLauncher{
		running:    make(map[string]bool),
		identities: make(map[string]string),
	}
}

func (l *r6ExternalLauncher) Launch(
	id string,
	_ config.Provider,
	_ string,
	_ string,
	_ config.AgentTeamsConfig,
	_ session.LaunchMode,
	_ string,
) (*launcher.LaunchResult, error) {
	l.mu.Lock()
	err := l.launchErr
	if err == nil {
		l.running[id] = true
		l.identities[id] = "fake-process:" + id
	}
	hook := l.onStarted
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if hook != nil {
		hook(id)
	}
	return &launcher.LaunchResult{SessionID: id, PID: 6101}, nil
}

func (l *r6ExternalLauncher) LaunchGuarded(
	id string,
	provider config.Provider,
	presetName string,
	apiKey string,
	agentTeams config.AgentTeamsConfig,
	mode session.LaunchMode,
	workDir string,
	beforeRawStart func() error,
) (*launcher.LaunchResult, error) {
	if beforeRawStart != nil {
		if err := beforeRawStart(); err != nil {
			return nil, err
		}
	}
	return l.Launch(id, provider, presetName, apiKey, agentTeams, mode, workDir)
}

func (l *r6ExternalLauncher) LaunchCodex(id string, _ string, _ session.LaunchMode, _ string, _ map[string]string) (*launcher.LaunchResult, error) {
	l.mu.Lock()
	err := l.codexLaunchErr
	if err == nil {
		l.running[id] = true
		l.identities[id] = "fake-process:" + id
	}
	hook := l.onStarted
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if hook != nil {
		hook(id)
	}
	return &launcher.LaunchResult{SessionID: id, PID: 6102}, nil
}

func (l *r6ExternalLauncher) LaunchCodexGuarded(
	id string,
	model string,
	mode session.LaunchMode,
	workDir string,
	env map[string]string,
	beforeRawStart func() error,
) (*launcher.LaunchResult, error) {
	if beforeRawStart != nil {
		if err := beforeRawStart(); err != nil {
			return nil, err
		}
	}
	return l.LaunchCodex(id, model, mode, workDir, env)
}

func (l *r6ExternalLauncher) IsRunning(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running[id]
}

func (l *r6ExternalLauncher) Stop(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopCalls++
	if l.stopErr != nil {
		return l.stopErr
	}
	if !l.asyncStop {
		delete(l.running, id)
	}
	return nil
}

func (l *r6ExternalLauncher) CaptureProcessIdentity(id string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	identity := l.identities[id]
	if identity == "" {
		return "", errors.New("fake process identity unavailable")
	}
	return identity, nil
}

func (l *r6ExternalLauncher) RecoverProcess(id string, _ int, identity string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.recoverErr != nil {
		return l.recoverRunning, l.recoverErr
	}
	if !l.recoverRunning {
		return false, nil
	}
	l.running[id] = true
	l.identities[id] = identity
	return true, nil
}

func (l *r6ExternalLauncher) setStopError(err error) {
	l.mu.Lock()
	l.stopErr = err
	l.mu.Unlock()
}

func (l *r6ExternalLauncher) setAsyncStop(enabled bool) {
	l.mu.Lock()
	l.asyncStop = enabled
	l.mu.Unlock()
}

func (l *r6ExternalLauncher) setRecoverRunning(running bool) {
	l.mu.Lock()
	l.recoverRunning = running
	l.mu.Unlock()
}

func (l *r6ExternalLauncher) StopAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopErr != nil || l.asyncStop {
		return // model void StopAll retaining entries whose kill is unconfirmed
	}
	l.running = make(map[string]bool)
}

func (l *r6ExternalLauncher) finish(id string) {
	l.mu.Lock()
	delete(l.running, id)
	l.mu.Unlock()
}

func (l *r6ExternalLauncher) callsToStop() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopCalls
}

func newR6ExternalLeaseApp(t *testing.T) (*App, *r6ExternalLauncher, string) {
	t.Helper()
	app, configDir := newTestAppWithConfigDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	app.ctx = ctx
	app.Capabilities = platform.PlatformCapabilities{
		PlatformID:                  "r6-external-test",
		EmbeddedTerminalSupported:   true,
		StandaloneTerminalSupported: true,
	}
	app.configDir = configDir
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	app.sharedLeases = make(map[remote.SharedLeaseOwnerKey]*remote.SharedDependencyLease)
	app.externalCleanupStore = cleanupstore.NewFileStore(configDir)
	app.externalRunPollInterval = 5 * time.Millisecond
	fake := newR6ExternalLauncher()
	app.externalLauncher = fake

	runner := &r5CountingHeadroomRunner{}
	app.Headroom = headroom.NewHeadroomService(runner, app.Log)
	t.Cleanup(func() {
		_ = app.Headroom.Stop()
		runner.cleanup()
	})
	return app, fake, configDir
}

func configureR6ClaudeProvider(t *testing.T, app *App) string {
	t.Helper()
	const providerID = "r6-external-claude-provider"
	if err := app.Config.SaveProvider(providerID, config.Provider{
		Type: "anthropic", BaseURL: "https://api.anthropic.com", AuthKey: "ANTHROPIC_API_KEY", DefaultModel: "claude-r6",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey(providerID, "sk-r6-test"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	return providerID
}

func configureR6CodexHeadroom(t *testing.T, app *App, configDir string) {
	t.Helper()
	app.Settings = settings.NewService(configDir)
	if err := app.Settings.Load(); err != nil {
		t.Fatalf("Settings.Load: %v", err)
	}
	if err := app.Settings.SetCodexGlobalHeadroom(true, "https://api.openai.com/v1", CodexGlobalHeadroomDefaultPort); err != nil {
		t.Fatalf("persist Codex Headroom: %v", err)
	}
	app.CodexHeadroom = headroom.NewHeadroomService(&r5CountingHeadroomRunner{}, app.Log)
	isolatedHome := t.TempDir()
	// App's global Headroom marker intentionally targets ~/.codex/config.toml,
	// not CODEX_HOME; isolate both platform home variables before toggle tests.
	t.Setenv("HOME", isolatedHome)
	t.Setenv("USERPROFILE", isolatedHome)
	t.Setenv("CODEX_HOME", filepath.Join(isolatedHome, ".codex"))
}

func waitR6(t *testing.T, condition func() bool, detail string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", detail)
}

func TestR6_001_ClaudeExternalRunRejectsStopAndUninstallUntilStopSucceeds(t *testing.T) {
	app, fake, _ := newR6ExternalLeaseApp(t)
	providerID := configureR6ClaudeProvider(t, app)

	id, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, "")
	if err != nil {
		t.Fatalf("LaunchSession: %v", err)
	}
	if got := app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom); got != 1 {
		t.Fatalf("external Claude active leases=%d want 1", got)
	}
	if err := app.HeadroomStop(); !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Fatalf("manual HeadroomStop during external run error=%v", err)
	}
	uninstallErr, releaseDrain := app.stopAllHeadroomForUninstall()
	if releaseDrain != nil {
		releaseDrain()
	}
	if !errors.Is(uninstallErr, envcheck.ErrHeadroomInUse) {
		t.Fatalf("uninstall during external run error=%v want ErrHeadroomInUse", uninstallErr)
	}
	if !app.Headroom.IsRunning() {
		t.Fatal("rejected uninstall stopped the Headroom dependency")
	}

	fake.setStopError(errors.New("injected stop failure"))
	if err := app.StopSession(id); err == nil {
		t.Fatal("StopSession unexpectedly swallowed launcher stop failure")
	}
	if got := app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom); got != 1 {
		t.Fatalf("failed StopSession released live lease: count=%d", got)
	}
	fake.setStopError(nil)
	if err := app.StopSession(id); err != nil {
		t.Fatalf("StopSession retry: %v", err)
	}
	if got := app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom); got != 0 {
		t.Fatalf("successful StopSession retained lease: count=%d", got)
	}
	if status := app.Sessions.GetStatus(id); status != session.StatusStopped {
		t.Fatalf("session status=%q want stopped", status)
	}
}

func TestR6_001_CodexExternalNaturalExitReleasesLeaseAndUnblocksToggle(t *testing.T) {
	app, fake, configDir := newR6ExternalLeaseApp(t)
	configureR6CodexHeadroom(t, app, configDir)

	id, err := app.LaunchCodexSession("", "", "terminal", t.TempDir(), "")
	if err != nil {
		t.Fatalf("LaunchCodexSession: %v", err)
	}
	if got := app.sharedCoord.LeaseCount(remote.SharedServiceCodexHeadroom); got != 1 {
		t.Fatalf("external Codex active leases=%d want 1", got)
	}
	if _, err := app.SetCodexGlobalHeadroom(false, "", 0); !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Fatalf("Codex toggle-off during external run error=%v want ErrSharedServiceInUse", err)
	}

	fake.finish(id)
	waitR6(t, func() bool {
		return app.sharedCoord.LeaseCount(remote.SharedServiceCodexHeadroom) == 0 && app.Sessions.GetStatus(id) == session.StatusExited
	}, "natural exit exact lease release")
	if _, err := app.SetCodexGlobalHeadroom(false, "", 0); err != nil {
		t.Fatalf("Codex toggle remained blocked after natural exit: %v", err)
	}
}

func TestR6_001_InstantExternalCrashDoesNotLeakLease(t *testing.T) {
	app, fake, _ := newR6ExternalLeaseApp(t)
	providerID := configureR6ClaudeProvider(t, app)
	fake.onStarted = fake.finish // crash after OS start succeeds but before App returns

	id, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, "")
	if err != nil {
		t.Fatalf("LaunchSession with instant crash: %v", err)
	}
	waitR6(t, func() bool {
		return app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom) == 0 && app.Sessions.GetStatus(id) == session.StatusExited
	}, "instant-crash lease cleanup")
	if got := app.sharedCoord.LaunchAdmissionCount(remote.SharedServiceClaudeHeadroom); got != 0 {
		t.Fatalf("instant crash leaked launch admission: %d", got)
	}
}

func TestR6_001_ExternalRemoveReleasesExactLease(t *testing.T) {
	app, _, _ := newR6ExternalLeaseApp(t)
	sess := app.Sessions.Create(session.AppTypeClaudeCode, "p", "", "m", session.ModeTerminal, t.TempDir())
	admission, err := app.sharedCoord.AcquireLaunchAdmission(remote.SharedServiceClaudeHeadroom)
	if err != nil {
		t.Fatalf("AcquireLaunchAdmission: %v", err)
	}
	identity, err := app.sharedCoord.MintExternalRunIdentity(contract.SessionID(sess.ID))
	if err != nil {
		t.Fatalf("MintExternalRunIdentity: %v", err)
	}
	lease, err := app.sharedCoord.AcquireForExternalRunWithAdmission(context.Background(), identity, remote.SharedServiceClaudeHeadroom, [32]byte{6, 5}, admission)
	if err != nil {
		t.Fatalf("AcquireForExternalRunWithAdmission: %v", err)
	}
	app.rememberSharedLease(sess.ID, lease)
	app.Sessions.MarkExited(sess.ID)

	if err := app.RemoveSession(sess.ID); err != nil {
		t.Fatalf("RemoveSession: %v", err)
	}
	if got := app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom); got != 0 {
		t.Fatalf("RemoveSession retained external lease: count=%d", got)
	}
}

func TestR6_001_ExternalLaunchFailureAndShutdownRaceDoNotLeak(t *testing.T) {
	t.Run("launcher failure", func(t *testing.T) {
		app, fake, _ := newR6ExternalLeaseApp(t)
		providerID := configureR6ClaudeProvider(t, app)
		fake.launchErr = errors.New("injected launcher failure")
		if _, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, ""); err == nil {
			t.Fatal("LaunchSession unexpectedly succeeded")
		}
		if got := app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom); got != 0 {
			t.Fatalf("failed launch leaked lease: %d", got)
		}
		if got := app.sharedCoord.LaunchAdmissionCount(remote.SharedServiceClaudeHeadroom); got != 0 {
			t.Fatalf("failed launch leaked admission: %d", got)
		}
		pending, loadErr := app.externalCleanupStore.LoadPending()
		if loadErr != nil || len(pending) != 0 {
			t.Fatalf("failed launch leaked durable reservation: pending=%+v err=%v", pending, loadErr)
		}
	})

	t.Run("shutdown closes admission before promotion", func(t *testing.T) {
		app, fake, _ := newR6ExternalLeaseApp(t)
		providerID := configureR6ClaudeProvider(t, app)
		fake.onStarted = func(string) { app.sharedCoord.ClearAll() }
		if _, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, ""); !errors.Is(err, remote.ErrSharedCoordinatorClosed) {
			t.Fatalf("launch/shutdown race error=%v want ErrSharedCoordinatorClosed", err)
		}
		if fake.callsToStop() != 1 {
			t.Fatalf("post-start promotion failure stop calls=%d want 1", fake.callsToStop())
		}
		if got := app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom); got != 0 {
			t.Fatalf("shutdown race leaked lease: %d", got)
		}
		app.sharedLeaseMu.Lock()
		remembered := len(app.sharedLeases)
		app.sharedLeaseMu.Unlock()
		if remembered != 0 {
			t.Fatalf("shutdown race leaked App lease registry entries=%d", remembered)
		}
	})
}

func r7ExternalCleanupCount(app *App) int {
	app.externalCleanupMu.Lock()
	defer app.externalCleanupMu.Unlock()
	return len(app.externalCleanups)
}

func TestR6_001_PromotionAndCompensationDoubleFailureTransfersAdmissionToRecoverableOwner(t *testing.T) {
	app, fake, _ := newR6ExternalLeaseApp(t)
	app.externalRunPollInterval = 250 * time.Millisecond
	providerID := configureR6ClaudeProvider(t, app)
	if err := app.Headroom.Start("https://api.anthropic.com"); err != nil {
		t.Fatalf("Headroom.Start: %v", err)
	}

	// Seed an incompatible active generation so the new process starts but its
	// exact admission cannot promote. The seed is released immediately after the
	// failure; only a correctly transferred startup admission can keep Stop busy.
	seedAdmission, err := app.sharedCoord.AcquireLaunchAdmission(remote.SharedServiceClaudeHeadroom)
	if err != nil {
		t.Fatalf("seed AcquireLaunchAdmission: %v", err)
	}
	seedIdentity, err := app.sharedCoord.MintExternalRunIdentity("r7-seed")
	if err != nil {
		t.Fatalf("seed MintExternalRunIdentity: %v", err)
	}
	seedLease, err := app.sharedCoord.AcquireForExternalRunWithAdmission(
		context.Background(), seedIdentity, remote.SharedServiceClaudeHeadroom, [32]byte{0x7f}, seedAdmission,
	)
	if err != nil {
		t.Fatalf("seed AcquireForExternalRunWithAdmission: %v", err)
	}
	defer func() { _ = app.sharedCoord.ReleaseExact(context.Background(), seedLease) }()

	var startedID string
	fake.onStarted = func(id string) { startedID = id }
	fake.setStopError(errors.New("injected compensation stop failure"))
	if _, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, ""); !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Fatalf("LaunchSession error=%v want promotion ErrSharedServiceInUse", err)
	}
	if startedID == "" || !fake.IsRunning(startedID) {
		t.Fatalf("post-start double failure lost live process: id=%q running=%v", startedID, fake.IsRunning(startedID))
	}
	if got := r7ExternalCleanupCount(app); got != 1 {
		t.Fatalf("live process cleanup owners=%d want 1", got)
	}
	if got := app.sharedCoord.LaunchAdmissionCount(remote.SharedServiceClaudeHeadroom); got != 1 {
		t.Fatalf("transferred cleanup admission count=%d want 1", got)
	}

	if err := app.sharedCoord.ReleaseExact(context.Background(), seedLease); err != nil {
		t.Fatalf("release seed lease: %v", err)
	}
	if err := app.HeadroomStop(); !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Fatalf("cleanup-owned live process did not block HeadroomStop: %v", err)
	}
	uninstallErr, releaseDrain := app.stopAllHeadroomForUninstall()
	if releaseDrain != nil {
		releaseDrain()
	}
	if !errors.Is(uninstallErr, envcheck.ErrHeadroomInUse) {
		t.Fatalf("cleanup-owned live process did not block uninstall: %v", uninstallErr)
	}

	// A later explicit StopSession is an exact recovery path: terminal receipt
	// removes the cleanup registration and releases only its transferred claim.
	fake.setStopError(nil)
	if err := app.StopSession(startedID); err != nil {
		t.Fatalf("StopSession recovery: %v", err)
	}
	waitR6(t, func() bool {
		return !fake.IsRunning(startedID) && r7ExternalCleanupCount(app) == 0 &&
			app.sharedCoord.LaunchAdmissionCount(remote.SharedServiceClaudeHeadroom) == 0
	}, "manual stop recovery of promotion-failure cleanup owner")
	if err := app.HeadroomStop(); err != nil {
		t.Fatalf("HeadroomStop remained blocked after exact recovery: %v", err)
	}
}

func TestR6_001_ClosedPromotionAndCompensationFailureLateTerminalIsReaped(t *testing.T) {
	app, fake, configDir := newR6ExternalLeaseApp(t)
	configureR6CodexHeadroom(t, app, configDir)

	var startedID string
	fake.setStopError(errors.New("injected persistent compensation stop failure"))
	fake.onStarted = func(id string) {
		startedID = id
		app.sharedCoord.ClearAll()
	}
	if _, err := app.LaunchCodexSession("", "", "terminal", t.TempDir(), ""); !errors.Is(err, remote.ErrSharedCoordinatorClosed) {
		t.Fatalf("LaunchCodexSession error=%v want ErrSharedCoordinatorClosed", err)
	}
	if startedID == "" || !fake.IsRunning(startedID) {
		t.Fatalf("closed promotion double failure lost live process: id=%q running=%v", startedID, fake.IsRunning(startedID))
	}
	if got := r7ExternalCleanupCount(app); got != 1 {
		t.Fatalf("closed coordinator live process cleanup owners=%d want 1", got)
	}
	if fake.callsToStop() == 0 {
		t.Fatal("initial compensation Stop was not attempted")
	}
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	app.ctx = cancelledCtx // shutdown cancellation is not a terminal receipt
	waitR6(t, func() bool { return fake.callsToStop() >= 2 }, "cleanup reaper Stop retry after ctx cancellation")
	if got := r7ExternalCleanupCount(app); got != 1 {
		t.Fatalf("failed reaper retry discarded live cleanup owner: %d", got)
	}

	// Even though shutdown already removed the coordinator admission, the App
	// cleanup owner/reaper remains authoritative until a late natural terminal.
	fake.finish(startedID)
	waitR6(t, func() bool { return r7ExternalCleanupCount(app) == 0 }, "late-terminal cleanup reaper")
	if status := app.Sessions.GetStatus(startedID); status != session.StatusFailed {
		t.Fatalf("reaped failed launch status=%q want failed", status)
	}
}

func TestR6_001_ShutdownExactReleaseClearsAppLeaseRegistry(t *testing.T) {
	app, _, _ := newR6ExternalLeaseApp(t)
	admission, err := app.sharedCoord.AcquireLaunchAdmission(remote.SharedServiceCodexHeadroom)
	if err != nil {
		t.Fatalf("AcquireLaunchAdmission: %v", err)
	}
	identity, err := app.sharedCoord.MintExternalRunIdentity("shutdown-app-run")
	if err != nil {
		t.Fatalf("MintExternalRunIdentity: %v", err)
	}
	lease, err := app.sharedCoord.AcquireForExternalRunWithAdmission(context.Background(), identity, remote.SharedServiceCodexHeadroom, [32]byte{6, 6}, admission)
	if err != nil {
		t.Fatalf("AcquireForExternalRunWithAdmission: %v", err)
	}
	app.rememberSharedLease("shutdown-app-run", lease)

	app.closeSharedLeasesForShutdown()
	if got := app.sharedCoord.LeaseCount(remote.SharedServiceCodexHeadroom); got != 0 {
		t.Fatalf("shutdown retained coordinator lease: %d", got)
	}
	app.sharedLeaseMu.Lock()
	remembered := len(app.sharedLeases)
	app.sharedLeaseMu.Unlock()
	if remembered != 0 {
		t.Fatalf("shutdown retained App lease registry entries=%d", remembered)
	}
}
