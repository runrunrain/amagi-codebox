package main

// external_cleanup_harness_test.go — shared test doubles for the external
// process ownership/cleanup machinery: a deterministic Launcher lifecycle fake
// plus the minimal App harness (coordinator + cleanupstore + fake external
// launcher) the ownership, recovery and terminal-receipt tests drive.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/cleanupstore"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/session"
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
	_ []string,
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

func r7ExternalCleanupCount(app *App) int {
	app.externalCleanupMu.Lock()
	defer app.externalCleanupMu.Unlock()
	return len(app.externalCleanups)
}

type r10LegacyRecoveryLauncher struct {
	*r6ExternalLauncher
	recoverErr error
}

func (l *r10LegacyRecoveryLauncher) RecoverProcess(id string, _ int, identity string) (bool, error) {
	l.mu.Lock()
	l.running[id] = true
	l.identities[id] = identity
	l.mu.Unlock()
	return true, l.recoverErr
}
