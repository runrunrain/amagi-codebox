package main

// R5-002 regression: a desktop launch that depends on Headroom must pass the
// uninstall-drain admission barrier before Headroom.Start, session creation,
// launch resolution, or PTY startup can occur. Releasing the drain permits a
// later retry to advance normally.

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
)

type r5CountingHeadroomRunner struct {
	started  atomic.Int32
	startErr error
	mu       sync.Mutex
	cmds     []*exec.Cmd
}

func (r *r5CountingHeadroomRunner) Start(platform.CommandSpec) (*exec.Cmd, error) {
	r.started.Add(1)
	if r.startErr != nil {
		return nil, r.startErr
	}
	cmd := exec.Command("sleep", "3600")
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cmds = append(r.cmds, cmd)
	r.mu.Unlock()
	return cmd, nil
}

func (r *r5CountingHeadroomRunner) Run(context.Context, platform.CommandSpec) (*platform.ProcessResult, error) {
	return nil, errors.New("unexpected Run call")
}

func (r *r5CountingHeadroomRunner) cleanup() {
	r.mu.Lock()
	cmds := append([]*exec.Cmd(nil), r.cmds...)
	r.mu.Unlock()
	for _, cmd := range cmds {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}

type r5BlockingHeadroomRunner struct {
	entered chan struct{}
	release chan struct{}
}

func (r *r5BlockingHeadroomRunner) Start(platform.CommandSpec) (*exec.Cmd, error) {
	close(r.entered)
	<-r.release
	return nil, errors.New("injected blocked headroom start")
}

func (*r5BlockingHeadroomRunner) Run(context.Context, platform.CommandSpec) (*platform.ProcessResult, error) {
	return nil, errors.New("unexpected Run call")
}

type r5RejectingResolver struct{ calls atomic.Int32 }

func (r *r5RejectingResolver) Resolve(platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	r.calls.Add(1)
	return platform.ResolvedLaunchSpec{}, errors.New("injected resolver stop")
}

func (*r5RejectingResolver) ResolveExecutable(string, []string, []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return platform.ResolvedCLI{}, platform.LaunchDiagnostics{}, errors.New("unexpected ResolveExecutable call")
}

func TestR5_002_InFlightHeadroomStartAdmissionMakesConcurrentUninstallDrainNonEmpty(t *testing.T) {
	app := newTestApp(t)
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	runner := &r5BlockingHeadroomRunner{entered: make(chan struct{}), release: make(chan struct{})}
	app.Headroom = headroom.NewHeadroomService(runner, app.Log)

	startDone := make(chan error, 1)
	go func() {
		startDone <- app.HeadroomStart("https://api.anthropic.com")
	}()
	<-runner.entered

	// A mutation that linearized first must be visible to the uninstall empty
	// check for its entire raw Start call, not just during a one-shot preflight.
	empty := app.sharedCoord.BeginHeadroomUninstallDrain()
	app.sharedCoord.EndHeadroomUninstallDrain()
	close(runner.release)
	if err := <-startDone; err == nil || !strings.Contains(err.Error(), "injected blocked headroom start") {
		t.Fatalf("HeadroomStart result=%v want injected runner error", err)
	}
	if empty {
		t.Fatal("uninstall drain reported empty while Headroom.Start mutation was in flight")
	}
	if empty := app.sharedCoord.BeginHeadroomUninstallDrain(); !empty {
		t.Fatal("completed Headroom.Start mutation admission was not released")
	}
	app.sharedCoord.EndHeadroomUninstallDrain()
}

func TestR5_002_ExternalHeadroomLaunchAlsoRejectsBeforeHeadroomStart(t *testing.T) {
	app := newTestApp(t)
	app.ctx = context.Background()
	app.Capabilities = platform.PlatformCapabilities{PlatformID: "r5-test", EmbeddedTerminalSupported: true, StandaloneTerminalSupported: true}
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	runner := &r5CountingHeadroomRunner{startErr: errors.New("Headroom.Start must remain unreachable")}
	app.Headroom = headroom.NewHeadroomService(runner, app.Log)

	const providerID = "r5-drain-external-provider"
	if err := app.Config.SaveProvider(providerID, config.Provider{
		Type: "anthropic", BaseURL: "https://api.anthropic.com", AuthKey: "ANTHROPIC_API_KEY", DefaultModel: "claude-test",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey(providerID, "sk-r5-test"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	if empty := app.sharedCoord.BeginHeadroomUninstallDrain(); !empty {
		t.Fatal("expected empty drain at test start")
	}
	defer app.sharedCoord.EndHeadroomUninstallDrain()

	_, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, "")
	if !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Fatalf("external launch during drain error=%v want ErrSharedServiceInUse", err)
	}
	if got := runner.started.Load(); got != 0 {
		t.Fatalf("external launch reached Headroom.Start during drain: calls=%d", got)
	}
	if got := len(app.Sessions.List()); got != 0 {
		t.Fatalf("external launch created session before admission: records=%d", got)
	}
}

func TestR5_002_LaunchDuringHeadroomDrainHasZeroPreAdmissionSideEffectsAndCanRetry(t *testing.T) {
	app := newTestApp(t)
	app.ctx = context.Background()
	app.sharedCoord = remote.NewSharedServiceCoordinator()

	runner := &r5CountingHeadroomRunner{}
	t.Cleanup(runner.cleanup)
	app.Headroom = headroom.NewHeadroomService(runner, app.Log)
	resolver := &r5RejectingResolver{}
	app.CLIResolver = resolver

	const providerID = "r5-drain-provider"
	if err := app.Config.SaveProvider(providerID, config.Provider{
		Type:         "anthropic",
		BaseURL:      "https://api.anthropic.com",
		AuthKey:      "ANTHROPIC_API_KEY",
		DefaultModel: "claude-test",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey(providerID, "sk-r5-test"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	if empty := app.sharedCoord.BeginHeadroomUninstallDrain(); !empty {
		t.Fatal("expected empty drain at test start")
	}
	_, err := app.LaunchSession(providerID, "", "embedded", t.TempDir(), true, "")
	if !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Errorf("launch during drain error=%v want ErrSharedServiceInUse", err)
	}
	if got := runner.started.Load(); got != 0 {
		t.Errorf("Headroom.Start side effects during drain=%d want 0", got)
	}
	if got := resolver.calls.Load(); got != 0 {
		t.Errorf("launch resolution ran before admission: calls=%d want 0", got)
	}
	if got := len(app.Sessions.List()); got != 0 {
		t.Errorf("session records created before admission=%d want 0", got)
	}

	app.sharedCoord.EndHeadroomUninstallDrain()
	_, retryErr := app.LaunchSession(providerID, "", "embedded", t.TempDir(), true, "")
	if errors.Is(retryErr, remote.ErrSharedServiceInUse) {
		t.Fatalf("retry after drain release remained rejected: %v", retryErr)
	}
	if retryErr == nil || retryErr.Error() != "injected resolver stop" {
		t.Fatalf("retry should pass admission and reach injected resolver, got %v", retryErr)
	}
	if got := runner.started.Load(); got != 1 {
		t.Fatalf("Headroom.Start calls after admitted retry=%d want 1", got)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("resolver calls after admitted retry=%d want 1", got)
	}
}
