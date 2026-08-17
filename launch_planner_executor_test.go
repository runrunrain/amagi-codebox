package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/settings"
)

// fakeCLIResolver returns a valid spec for any request.
type fakeCLIResolver struct{}

func (fakeCLIResolver) Resolve(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	command := strings.TrimSpace(strings.Join(append([]string{req.AppType}, req.CLIArgs...), " "))
	return platform.ResolvedLaunchSpec{
		AppType: req.AppType, LaunchMode: req.LaunchMode, WorkDir: req.WorkDir,
		CLI:            platform.ResolvedCLI{Name: req.AppType, Path: "/usr/bin/echo", Args: append([]string(nil), req.CLIArgs...)},
		Env:            platform.ResolvedEnv{Variables: append([]string(nil), req.Env...)},
		PTYCols:        120,
		PTYRows:        40,
		BootstrapMode:  platform.BootstrapShellAttach,
		StartupCommand: command,
	}, nil
}

func (fakeCLIResolver) ResolveExecutable(command string, args []string, env []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return platform.ResolvedCLI{Name: "test", Path: "/usr/bin/echo"}, platform.LaunchDiagnostics{}, nil
}

func fakePlatformCaps() platform.PlatformCapabilities {
	return platform.PlatformCapabilities{
		OS: "linux", EmbeddedTerminalSupported: true, StandaloneTerminalSupported: true,
	}
}

// isolatedHome is a helper that sets HOME and USERPROFILE to a temp dir.
func isolatedHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// testPlannerSetup creates a planner with isolated temp-dir services.
// All tests MUST call isolatedHome first (m-01).
func testPlannerSetup(t *testing.T, cli contract.CLIType, providerName, apiKey string) (*appLaunchPlanner, *settings.Service, *config.ConfigService, *secrets.SecretsService) {
	t.Helper()
	homeDir := isolatedHome(t)
	dir := t.TempDir()
	cfgSvc := config.NewConfigService(dir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatalf("config Load: %v", err)
	}
	secSvc := secrets.NewSecretsService(dir)
	settingsSvc := settings.NewService(dir)
	if err := settingsSvc.Load(); err != nil {
		t.Fatalf("settings Load: %v", err)
	}
	envVarsSvc := envvars.NewEnvVarsService(dir)
	pathsSvc := paths.NewPathsService(dir)
	defaults, err := launchplan.NewDefaultStore(settingsSvc)
	if err != nil {
		t.Fatalf("NewDefaultStore: %v", err)
	}
	provider := config.Provider{
		Anthropic: &config.AnthropicFormat{
			Enabled: true, BaseURL: "https://api.test.com",
		},
		DefaultModel: "test-model",
		Presets: map[string]config.Preset{
			"test-preset": {Name: "test-preset", Model: "test-model"},
		},
	}
	if cli == contract.CLITypeCodex {
		provider.Anthropic = nil
		provider.OpenAI = &config.OpenAIFormat{
			Enabled: true, BaseURL: "https://api.openai.com/v1", AuthKey: "OPENAI_API_KEY",
		}
	}
	if err := cfgSvc.SaveProvider(providerName, provider); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := secSvc.SetAPIKey(providerName, apiKey); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	recipe := launchplan.StableRecipe{
		CLIType: cli, Workdir: dir,
		ProviderRef: providerName, PresetRef: "test-preset", ModelRef: "test-model",
		ShellRef: "/bin/sh",
	}
	if err := defaults.RecordDesktopActivation(recipe); err != nil {
		t.Fatalf("RecordDesktopActivation: %v", err)
	}
	return newAppLaunchPlanner(cfgSvc, secSvc, defaults, fakeCLIResolver{}, fakePlatformCaps(), pathsSvc, envVarsSvc, homeDir), settingsSvc, cfgSvc, secSvc
}

// --- Probe / BuildPlan basics ---

func TestProbeChecksInstalledCLIWithoutDefaults(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()
	settingsSvc := settings.NewService(dir)
	settingsSvc.Load()
	defaults, _ := launchplan.NewDefaultStore(settingsSvc)
	p := newAppLaunchPlanner(
		config.NewConfigService(dir), secrets.NewSecretsService(dir),
		defaults, fakeCLIResolver{}, fakePlatformCaps(),
		paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), dir,
	)
	for _, cli := range contract.KnownCLITypes {
		avail, failure := p.Probe(context.Background(), cli)
		if !avail.Available || failure != nil {
			t.Fatalf("%s Probe should reflect installed CLI without requiring launch defaults", cli)
		}
	}
}

func TestBuildPlanFiveCLICanonicalStructure(t *testing.T) {
	for _, cli := range contract.KnownCLITypes {
		t.Run(string(cli), func(t *testing.T) {
			p, _, _, _ := testPlannerSetup(t, cli, "test-provider", "test-key-123456789")
			plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
				CLIType: cli, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
			})
			if failure != nil {
				t.Fatalf("%s BuildPlan failure: kind=%d", cli, failure.Kind)
			}
			defer plan.Secrets.Dispose()
			if err := plan.Validate(); err != nil {
				t.Fatalf("%s plan Validate: %v", cli, err)
			}
			processCount := 0
			hasBootstrap := false
			lastRank := -1
			for _, eff := range plan.Effects {
				rank := launchplan.CanonicalEffectRank(eff)
				if rank < lastRank {
					t.Fatalf("%s effects out of canonical order", cli)
				}
				lastRank = rank
				if eff.Process != nil {
					processCount++
					if eff.Process.Mode != launchplan.ModeEmbedded || !eff.Process.RequireRunHandle {
						t.Fatalf("%s process effect invalid", cli)
					}
				}
				if eff.Bootstrap != nil {
					hasBootstrap = true
				}
			}
			if processCount != 1 {
				t.Fatalf("%s plan has %d process effects", cli, processCount)
			}
			// C-01: every plan must include a bootstrap effect.
			if !hasBootstrap {
				t.Fatalf("%s plan missing EffectBootstrapWrite (C-01)", cli)
			}
		})
	}
}

func TestProbeSamePathAsBuildPlan(t *testing.T) {
	for _, cli := range contract.KnownCLITypes {
		t.Run(string(cli), func(t *testing.T) {
			p, _, _, _ := testPlannerSetup(t, cli, "probe-provider", "probe-key-123456789")
			plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
				CLIType: cli, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
			})
			if failure != nil {
				t.Fatalf("%s BuildPlan failure", cli)
			}
			plan.Secrets.Dispose()
			avail, probeFailure := p.Probe(context.Background(), cli)
			if !avail.Available || probeFailure != nil {
				t.Fatalf("%s Probe should return true when BuildPlan succeeds", cli)
			}
		})
	}
}

func TestBuildPlanMissingProviderFails(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()
	settingsSvc := settings.NewService(dir)
	settingsSvc.Load()
	defaults, _ := launchplan.NewDefaultStore(settingsSvc)
	defaults.RecordDesktopActivation(launchplan.StableRecipe{
		CLIType: contract.CLITypeClaudeCode, Workdir: dir,
		ProviderRef: "nonexistent", PresetRef: "x", ModelRef: "m",
	})
	p := newAppLaunchPlanner(
		config.NewConfigService(dir), secrets.NewSecretsService(dir),
		defaults, fakeCLIResolver{}, fakePlatformCaps(),
		paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), dir,
	)
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if plan != nil {
		plan.Secrets.Dispose()
		t.Fatal("BuildPlan should fail with missing provider")
	}
	if failure == nil || failure.Kind != launchplan.FailureLaunchContext {
		t.Fatalf("expected FailureLaunchContext, got %v", failure)
	}
}

func TestRemoteModeForcedEmbedded(t *testing.T) {
	p, _, _, _ := testPlannerSetup(t, contract.CLITypePi, "force-provider", "force-key-123456789")
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeExternal,
	})
	if failure != nil {
		t.Fatalf("BuildPlan failure: %v", failure)
	}
	defer plan.Secrets.Dispose()
	for _, eff := range plan.Effects {
		if eff.Process != nil && eff.Process.Mode != launchplan.ModeEmbedded {
			t.Fatal("remote should force embedded")
		}
	}
}

func TestBuildPlanRemoteOverridesStoredDefaults(t *testing.T) {
	p, _, cfgSvc, secSvc := testPlannerSetup(t, contract.CLITypeCodex, "old-provider", "old-key-123456789")
	if err := cfgSvc.SaveProvider("new-provider", config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.openai.com/v1", AuthKey: "OPENAI_API_KEY"},
		DefaultModel: "gpt-new",
	}); err != nil {
		t.Fatal(err)
	}
	if err := secSvc.SetAPIKey("new-provider", "new-key-123456789"); err != nil {
		t.Fatal(err)
	}
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeCodex, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
		StableRefs: &launchplan.StableLaunchRefs{ProviderRef: "new-provider", ModelRef: "gpt-new"},
	})
	if failure != nil {
		t.Fatalf("override BuildPlan failure: %#v", failure)
	}
	defer plan.Secrets.Dispose()
	if plan.Recipe.ProviderRef != "new-provider" || plan.Recipe.ModelRef != "gpt-new" {
		t.Fatalf("remote override not applied: %#v", plan.Recipe)
	}
}

func TestBuildPlanCodexUsesProviderAndPresetAsPerProcessOverrides(t *testing.T) {
	home := isolatedHome(t)
	dir := t.TempDir()
	cfgSvc := config.NewConfigService(dir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatal(err)
	}
	secSvc := secrets.NewSecretsService(dir)
	settingsSvc := settings.NewService(dir)
	if err := settingsSvc.Load(); err != nil {
		t.Fatal(err)
	}
	defaults, err := launchplan.NewDefaultStore(settingsSvc)
	if err != nil {
		t.Fatal(err)
	}
	providerID := "codex-proxy"
	if err := cfgSvc.SaveProvider(providerID, config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://proxy.example.com/v1", AuthKey: "OPENAI_API_KEY"},
		DefaultModel: "gpt-5.6-luna",
	}); err != nil {
		t.Fatal(err)
	}
	if err := cfgSvc.SaveTerminalPreset("codex", providerID+"/max", config.TerminalPreset{
		Name: "Max", Provider: providerID, Model: "gpt-5.6-luna",
		Parameters: config.Parameters{
			ReasoningEffort: "max",
			ContextWindow: &config.ContextWindowConfig{
				ModelContextWindow: 1047576, AutoCompactTokenLimit: 900000,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := secSvc.SetAPIKey(providerID, "codex-secret-123456789"); err != nil {
		t.Fatal(err)
	}
	if err := defaults.RecordDesktopActivation(launchplan.StableRecipe{
		CLIType: contract.CLITypeCodex, Workdir: dir, ProviderRef: providerID, ModelRef: providerID + "/max",
	}); err != nil {
		t.Fatal(err)
	}
	planner := newAppLaunchPlanner(cfgSvc, secSvc, defaults, fakeCLIResolver{}, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{CLIType: contract.CLITypeCodex, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded})
	if failure != nil {
		t.Fatalf("BuildPlan failure: %#v", failure)
	}
	defer plan.Secrets.Dispose()
	for _, effect := range plan.Effects {
		if effect.Config != nil {
			t.Fatal("Codex plan must not mutate shared config.toml")
		}
		if effect.Process == nil {
			continue
		}
		joinedArgs := strings.Join(effect.Process.Resolved.CLI.Args, "\n")
		for _, want := range []string{
			"gpt-5.6-luna", `model_reasoning_effort="max"`, "model_context_window=1047576",
			"model_auto_compact_token_limit=900000", `model_provider="amagi-codebox-provider"`,
			`base_url="https://proxy.example.com/v1"`,
		} {
			if !strings.Contains(joinedArgs, want) {
				t.Fatalf("Codex process args missing %q: %v", want, effect.Process.Resolved.CLI.Args)
			}
		}
		if !envContainsExact(effect.Process.Resolved.Env.Variables, "OPENAI_API_KEY=codex-secret-123456789") {
			t.Fatalf("Codex process env missing provider credential: %v", effect.Process.Resolved.Env.Variables)
		}
	}
}

// --- M-01: SharedStartSpec carries upstream/port ---

func TestBuildPlanClaudeHeadroomUpstreamPort(t *testing.T) {
	dir := isolatedHome(t)
	cfgSvc := config.NewConfigService(dir)
	cfgSvc.Load()
	secSvc := secrets.NewSecretsService(dir)
	settingsSvc := settings.NewService(dir)
	settingsSvc.Load()
	defaults, _ := launchplan.NewDefaultStore(settingsSvc)
	cfgSvc.SaveProvider("claude-provider", config.Provider{
		Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://api.anthropic.com"},
		DefaultModel: "claude-3",
	})
	secSvc.SetAPIKey("claude-provider", "sk-ant-test123456")
	defaults.RecordDesktopActivation(launchplan.StableRecipe{
		CLIType: contract.CLITypeClaudeCode, Workdir: dir,
		ProviderRef: "claude-provider", PresetRef: "p", ModelRef: "claude-3",
		UseHeadroom: true,
	})
	p := newAppLaunchPlanner(cfgSvc, secSvc, defaults, fakeCLIResolver{}, fakePlatformCaps(),
		paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), dir)
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure != nil {
		t.Fatalf("BuildPlan failure: %v", failure)
	}
	defer plan.Secrets.Dispose()
	// M-01: verify SharedStartSpec carries non-empty upstream + correct ports.
	for _, eff := range plan.Effects {
		if eff.Kind == launchplan.EffectHeadroomStart && eff.Shared != nil {
			if eff.Shared.UpstreamURL != "https://api.anthropic.com" {
				t.Fatalf("headroom upstream = %q, want https://api.anthropic.com", eff.Shared.UpstreamURL)
			}
			if eff.Shared.ListenPort != 8787 {
				t.Fatalf("headroom port = %d, want 8787", eff.Shared.ListenPort)
			}
		}
	}
}

// --- M-01: fake services verify Start parameters ---

type fakeHeadroomService struct {
	mu         sync.Mutex
	running    bool
	port       int
	backendURL string
	startCalls []string
}

func (f *fakeHeadroomService) IsRunning() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.running }
func (f *fakeHeadroomService) GetStatus() headroom.HeadroomStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return headroom.HeadroomStatus{Running: f.running, Port: f.port, BackendURL: f.backendURL}
}
func (f *fakeHeadroomService) SetPort(port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.port = port
	return nil
}
func (f *fakeHeadroomService) Start(backendURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls = append(f.startCalls, backendURL)
	f.backendURL = backendURL
	f.running = true
	return nil
}
func (f *fakeHeadroomService) StartForOpenAI(backendURL string) error { return f.Start(backendURL) }
func (f *fakeHeadroomService) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
	return nil
}

func TestSharedServiceFingerprintIsExactTupleHash(t *testing.T) {
	longPrefix := "https://api.example.test/" + strings.Repeat("segment/", 12)
	base := sharedServiceFingerprint("claude-headroom", longPrefix+"left", 8787)
	for name, candidate := range map[string][32]byte{
		"url-suffix": sharedServiceFingerprint("claude-headroom", longPrefix+"right", 8787),
		"port":       sharedServiceFingerprint("claude-headroom", longPrefix+"left", 8788),
		"service":    sharedServiceFingerprint("codex-headroom", longPrefix+"left", 8787),
		"boundary":   sharedServiceFingerprint("claude-headroom", longPrefix+"left8", 787),
	} {
		if candidate == base {
			t.Fatalf("%s change collided with exact tuple fingerprint", name)
		}
	}
	if sharedServiceFingerprint("svc", "https://x.test/x1", 23) == sharedServiceFingerprint("svc", "https://x.test/x12", 3) {
		t.Fatal("length-boundary tuple ambiguity collided")
	}
}

func TestExecutorRejectsRunningHeadroomConfigurationMismatch(t *testing.T) {
	service := &fakeHeadroomService{running: true, port: 8787, backendURL: "https://wrong.example/v1"}
	effect := &headroomStartEffect{
		deps: launchExecutorDeps{headroom: service}, service: launchplan.SharedClaudeHeadroom,
		upstreamURL: "https://expected.example/v1", listenPort: 8787,
	}
	if _, err := effect.Apply(context.Background()); err == nil {
		t.Fatal("running headroom with wrong upstream was accepted")
	}
	service.mu.Lock()
	service.backendURL = "https://expected.example/v1"
	service.port = 9999
	service.mu.Unlock()
	if _, err := effect.Apply(context.Background()); err == nil {
		t.Fatal("running headroom with wrong port was accepted")
	}
	service.mu.Lock()
	service.port = 8787
	service.mu.Unlock()
	if _, err := effect.Apply(context.Background()); err != nil {
		t.Fatalf("exact running headroom rejected: %v", err)
	}
}

func TestExecutorHeadroomStartWithUpstreamPort(t *testing.T) {
	dir := isolatedHome(t)
	cfgSvc := config.NewConfigService(dir)
	cfgSvc.Load()
	secSvc := secrets.NewSecretsService(dir)
	settingsSvc := settings.NewService(dir)
	settingsSvc.Load()
	defaults, _ := launchplan.NewDefaultStore(settingsSvc)
	cfgSvc.SaveProvider("p", config.Provider{
		Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://api.anthropic.com"},
		DefaultModel: "m",
	})
	secSvc.SetAPIKey("p", "key123456789")
	defaults.RecordDesktopActivation(launchplan.StableRecipe{
		CLIType: contract.CLITypeClaudeCode, Workdir: dir,
		ProviderRef: "p", PresetRef: "p", ModelRef: "m",
		UseHeadroom: true,
	})
	p := newAppLaunchPlanner(cfgSvc, secSvc, defaults, fakeCLIResolver{}, fakePlatformCaps(),
		paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), dir)
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure != nil {
		t.Fatalf("BuildPlan: %v", failure)
	}
	// Fake services as spies.
	headroomSpy := &fakeHeadroomService{}
	coord := remote.NewSharedServiceCoordinator()
	sharedAdmissions := make(map[launchplan.SharedServiceKind]any)
	for _, spec := range plan.Admissions {
		var kind remote.SharedServiceKind
		switch spec.Service {
		case launchplan.SharedClaudeHeadroom:
			kind = remote.SharedServiceClaudeHeadroom
		case launchplan.SharedCodexHeadroom:
			kind = remote.SharedServiceCodexHeadroom
		default:
			t.Fatalf("unknown shared kind %d", spec.Service)
		}
		admission, err := coord.AcquireLaunchAdmission(kind)
		if err != nil {
			t.Fatalf("AcquireLaunchAdmission: %v", err)
		}
		sharedAdmissions[spec.Service] = admission
		defer coord.ReleaseLaunchAdmission(admission)
	}
	exec := newAppLaunchExecutor(launchExecutorDeps{
		headroom: headroomSpy, sharedCoord: coord,
	})
	prepared, err := exec.Prepare(context.Background(), plan, launchplan.ExecutionBinding{
		SessionID: "s", RunEpoch: 1, RunHandle: "handle", SharedAdmissions: sharedAdmissions,
	})
	if err != nil {
		plan.Secrets.Dispose()
		t.Fatalf("Prepare: %v", err)
	}
	// Apply the Headroom effect (skip PTY + bootstrap).
	for i := 0; i < prepared.Count(); i++ {
		eff := prepared.Effect(i)
		if eff.Kind() == launchplan.EffectPTYStart || eff.Kind() == launchplan.EffectBootstrapWrite {
			continue
		}
		eff.ArmOwnership()
		_, applyErr := eff.Apply(context.Background())
		if applyErr != nil {
			t.Fatalf("effect %d Apply: %v", i, applyErr)
		}
		prepared.RecordApplied(i, launchplan.EffectEvidence{})
	}
	prepared.DisposeSecrets()
	// Assert headroom received real upstream.
	headroomSpy.mu.Lock()
	if len(headroomSpy.startCalls) != 1 || headroomSpy.startCalls[0] != "https://api.anthropic.com" {
		t.Fatalf("headroom Start calls = %v, want [https://api.anthropic.com]", headroomSpy.startCalls)
	}
	headroomSpy.mu.Unlock()
}

// --- M-02: Pi/Omp provider ID = PiProviderID ---

func TestBuildPlanPiProviderIDMatchesConfig(t *testing.T) {
	p, _, _, _ := testPlannerSetup(t, contract.CLITypePi, "pi-provider", "pi-key-123456789")
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure != nil {
		t.Fatalf("BuildPlan: %v", failure)
	}
	defer plan.Secrets.Dispose()
	// Extract config mutation content and argv --provider.
	var configContent []byte
	for _, eff := range plan.Effects {
		if eff.Config != nil {
			content, ok := plan.Secrets.Buffer(eff.Config.Candidate)
			if !ok {
				t.Fatal("config buffer not found")
			}
			configContent = content
		}
	}
	if configContent == nil {
		t.Fatal("Pi plan should have config mutation")
	}
	// The config should contain amagi-pi-provider.
	var cfg map[string]any
	if err := json.Unmarshal(configContent, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	providers, ok := cfg["providers"].(map[string]any)
	if !ok {
		t.Fatal("config missing providers map")
	}
	expectedID := "amagi-pi-provider"
	if _, ok := providers[expectedID]; !ok {
		t.Fatalf("config providers missing %s, have: %v", expectedID, providers)
	}
}

func TestBuildPlanPiOmpCredentialArgvConfigEnvJoint(t *testing.T) {
	for _, cli := range []contract.CLIType{contract.CLITypePi, contract.CLITypeOmp} {
		t.Run(string(cli), func(t *testing.T) {
			home := isolatedHome(t)
			dir := t.TempDir()
			cfgSvc := config.NewConfigService(dir)
			if err := cfgSvc.Load(); err != nil {
				t.Fatal(err)
			}
			secSvc := secrets.NewSecretsService(dir)
			settingsSvc := settings.NewService(dir)
			if err := settingsSvc.Load(); err != nil {
				t.Fatal(err)
			}
			defaults, err := launchplan.NewDefaultStore(settingsSvc)
			if err != nil {
				t.Fatal(err)
			}
			providerID := "joint-provider"
			secret := "joint-secret-123456789"
			provider := config.Provider{
				Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://joint.example/v1", AuthKey: config.AuthTypeAPIKey},
				DefaultModel: "joint-model",
				Presets: map[string]config.Preset{
					"joint-preset": {Name: "joint-preset", Model: "joint-model", Parameters: config.Parameters{ReasoningEffort: "high"}},
				},
			}
			if err := cfgSvc.SaveProvider(providerID, provider); err != nil {
				t.Fatal(err)
			}
			if err := secSvc.SetAPIKey(providerID, secret); err != nil {
				t.Fatal(err)
			}
			if err := defaults.RecordDesktopActivation(launchplan.StableRecipe{
				CLIType: cli, Workdir: dir, ProviderRef: providerID, PresetRef: "joint-preset", ModelRef: "joint-preset", ShellRef: "/bin/sh",
			}); err != nil {
				t.Fatal(err)
			}
			planner := newAppLaunchPlanner(cfgSvc, secSvc, defaults, fakeCLIResolver{}, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
			plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{CLIType: cli, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded})
			if failure != nil {
				t.Fatalf("BuildPlan failure: %v", failure)
			}
			defer plan.Secrets.Dispose()

			var process *launchplan.ProcessStartSpec
			var configContent []byte
			for _, effect := range plan.Effects {
				if effect.Process != nil {
					process = effect.Process
				}
				if effect.Config != nil {
					configContent, _ = plan.Secrets.Buffer(effect.Config.Candidate)
				}
			}
			if process == nil || configContent == nil {
				t.Fatal("joint plan missing process or config effect")
			}
			wantProvider := "amagi-" + providerID
			wantArgs := []string{"--provider", wantProvider, "--model", "joint-model", "--thinking", "high"}
			if strings.Join(process.Resolved.CLI.Args, "|") != strings.Join(wantArgs, "|") {
				t.Fatalf("argv = %v, want %v", process.Resolved.CLI.Args, wantArgs)
			}
			if !strings.Contains(process.Resolved.StartupCommand, "--provider "+wantProvider) ||
				!strings.Contains(process.Resolved.StartupCommand, "--model joint-model") ||
				!strings.Contains(process.Resolved.StartupCommand, "--thinking high") {
				t.Fatalf("startup command missing exact args: %q", process.Resolved.StartupCommand)
			}
			if !envContainsExact(process.Resolved.Env.Variables, "ANTHROPIC_API_KEY="+secret) {
				t.Fatalf("resolved env missing exact credential: %v", process.Resolved.Env.Variables)
			}
			var cfg map[string]any
			if err := json.Unmarshal(configContent, &cfg); err != nil {
				t.Fatal(err)
			}
			providers := cfg["providers"].(map[string]any)
			entry := providers[wantProvider].(map[string]any)
			if entry["apiKey"] != secret {
				t.Fatalf("config apiKey = %#v, want exact secret", entry["apiKey"])
			}
		})
	}
}

func TestBuildPlanPiOmpMissingCredentialTypedFailBeforeResolve(t *testing.T) {
	for _, cli := range []contract.CLIType{contract.CLITypePi, contract.CLITypeOmp} {
		t.Run(string(cli), func(t *testing.T) {
			home := isolatedHome(t)
			dir := t.TempDir()
			cfgSvc := config.NewConfigService(dir)
			_ = cfgSvc.Load()
			settingsSvc := settings.NewService(dir)
			_ = settingsSvc.Load()
			defaults, _ := launchplan.NewDefaultStore(settingsSvc)
			_ = cfgSvc.SaveProvider("missing-key", config.Provider{
				Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://missing.example", AuthKey: config.AuthTypeAPIKey},
				DefaultModel: "model",
			})
			_ = defaults.RecordDesktopActivation(launchplan.StableRecipe{CLIType: cli, Workdir: dir, ProviderRef: "missing-key", ModelRef: "model"})
			resolver := &countingCLIResolver{}
			planner := newAppLaunchPlanner(cfgSvc, secrets.NewSecretsService(dir), defaults, resolver, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
			plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{CLIType: cli, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded})
			if plan != nil {
				plan.Secrets.Dispose()
				t.Fatal("missing credential produced a plan")
			}
			if failure == nil || failure.Kind != launchplan.FailureLaunchContext {
				t.Fatalf("failure = %#v, want typed launch context", failure)
			}
			if resolver.calls != 0 {
				t.Fatalf("resolver calls = %d, want 0 before process planning", resolver.calls)
			}
		})
	}
}

func TestBuildPlanPiOmpOAuthIsKeylessBuiltInAndClearsInheritedCredentials(t *testing.T) {
	for _, cli := range []contract.CLIType{contract.CLITypePi, contract.CLITypeOmp} {
		t.Run(string(cli), func(t *testing.T) {
			home := isolatedHome(t)
			t.Setenv("ANTHROPIC_API_KEY", "must-not-inherit")
			t.Setenv("ANTHROPIC_AUTH_TOKEN", "must-not-inherit")
			t.Setenv("ANTHROPIC_BASE_URL", "https://must-not-inherit.example")
			dir := t.TempDir()
			configService := config.NewConfigService(dir)
			_ = configService.Load()
			settingsService := settings.NewService(dir)
			_ = settingsService.Load()
			defaults, _ := launchplan.NewDefaultStore(settingsService)
			if err := configService.SaveProvider("oauth", config.Provider{
				Anthropic: &config.AnthropicFormat{Enabled: true, AuthKey: config.AuthTypeOAuth}, DefaultModel: "oauth-model",
			}); err != nil {
				t.Fatal(err)
			}
			if err := defaults.RecordDesktopActivation(launchplan.StableRecipe{
				CLIType: cli, Workdir: dir, ProviderRef: "oauth", ModelRef: "oauth-model",
			}); err != nil {
				t.Fatal(err)
			}
			planner := newAppLaunchPlanner(configService, secrets.NewSecretsService(dir), defaults, fakeCLIResolver{}, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
			plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{CLIType: cli, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded})
			if failure != nil {
				t.Fatalf("OAuth keyless plan failure: %#v", failure)
			}
			defer plan.Secrets.Dispose()
			for _, effect := range plan.Effects {
				if effect.Config != nil {
					t.Fatal("OAuth built-in plan generated a credential config mutation")
				}
				if effect.Process != nil {
					if strings.Join(effect.Process.Resolved.CLI.Args, "|") != "--provider|anthropic|--model|oauth-model" {
						t.Fatalf("OAuth argv = %v", effect.Process.Resolved.CLI.Args)
					}
					for _, entry := range effect.Process.Resolved.Env.Variables {
						if strings.HasPrefix(entry, "ANTHROPIC_API_KEY=") || strings.HasPrefix(entry, "ANTHROPIC_AUTH_TOKEN=") || strings.HasPrefix(entry, "ANTHROPIC_BASE_URL=") {
							t.Fatalf("OAuth plan inherited credential routing env: %q", entry)
						}
					}
				}
			}
		})
	}
}

func envContainsExact(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

type countingCLIResolver struct{ calls int }

func (r *countingCLIResolver) Resolve(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	r.calls++
	return fakeCLIResolver{}.Resolve(req)
}

func (r *countingCLIResolver) ResolveExecutable(command string, args []string, env []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return fakeCLIResolver{}.ResolveExecutable(command, args, env)
}

// --- M-02: builder failure produces typed failure, not silent fallback ---

func TestBuildPlanPiBuilderFailureFailsClosed(t *testing.T) {
	homeDir := isolatedHome(t)
	dir := t.TempDir()
	cfgSvc := config.NewConfigService(dir)
	cfgSvc.Load()
	settingsSvc := settings.NewService(dir)
	settingsSvc.Load()
	defaults, _ := launchplan.NewDefaultStore(settingsSvc)
	// Provider without any format (not Anthropic/OpenAI compatible) → BuildPiModelsConfig will fail.
	cfgSvc.SaveProvider("bad", config.Provider{DefaultModel: "m"})
	defaults.RecordDesktopActivation(launchplan.StableRecipe{
		CLIType: contract.CLITypePi, Workdir: dir,
		ProviderRef: "bad", PresetRef: "x", ModelRef: "m",
	})
	p := newAppLaunchPlanner(cfgSvc, secrets.NewSecretsService(dir), defaults, fakeCLIResolver{},
		fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), homeDir)
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if plan != nil {
		plan.Secrets.Dispose()
		t.Fatal("BuildPlan should fail for bad provider (M-02: no silent fallback)")
	}
	if failure == nil {
		t.Fatal("expected failure for bad provider")
	}
}

// --- C-01: Bootstrap effect carries startup command ---

func TestBuildBootstrapEffectWritesOnlyShellAttach(t *testing.T) {
	for _, tc := range []struct {
		mode platform.LaunchBootstrapMode
		want string
	}{
		{mode: platform.BootstrapDirectCommand},
		{mode: platform.BootstrapShellInline},
		{mode: platform.BootstrapShellAttach, want: "target --model exact"},
	} {
		effect := buildBootstrapEffect(platform.ResolvedLaunchSpec{BootstrapMode: tc.mode, StartupCommand: "target --model exact"})
		if effect.Bootstrap == nil || effect.Bootstrap.StartupCommand != tc.want {
			t.Fatalf("mode %q bootstrap = %#v, want command %q", tc.mode, effect.Bootstrap, tc.want)
		}
	}
}

func TestBootstrapEffectCarriesStartupCommand(t *testing.T) {
	p, _, _, _ := testPlannerSetup(t, contract.CLITypeClaudeCode, "bs-provider", "bs-key-123456789")
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure != nil {
		t.Fatalf("BuildPlan: %v", failure)
	}
	defer plan.Secrets.Dispose()
	var bootstrapCmd string
	for _, eff := range plan.Effects {
		if eff.Bootstrap != nil {
			bootstrapCmd = eff.Bootstrap.StartupCommand
		}
	}
	if bootstrapCmd == "" {
		t.Fatal("bootstrap effect should carry non-empty startup command (C-01)")
	}
}

// --- M-04: Config CAS rollback tests ---

type faultAtomicConfigFile struct {
	atomicConfigFile
	failStep string
}

func (f *faultAtomicConfigFile) Chmod(mode os.FileMode) error {
	if f.failStep == "chmod-temp" {
		return errors.New("injected chmod-temp failure")
	}
	return f.atomicConfigFile.Chmod(mode)
}

func (f *faultAtomicConfigFile) Write(data []byte) (int, error) {
	if f.failStep == "write-temp" {
		return 0, errors.New("injected write-temp failure")
	}
	return f.atomicConfigFile.Write(data)
}

func (f *faultAtomicConfigFile) Sync() error {
	if f.failStep == "sync-temp" {
		return errors.New("injected sync-temp failure")
	}
	return f.atomicConfigFile.Sync()
}

func (f *faultAtomicConfigFile) Close() error {
	err := f.atomicConfigFile.Close()
	if f.failStep == "close-temp" {
		return errors.Join(err, errors.New("injected close-temp failure"))
	}
	return err
}

type faultAtomicConfigOperations struct {
	base       atomicConfigOperations
	failStep   string
	chmodCalls int
}

func (o *faultAtomicConfigOperations) MkdirAll(path string, mode os.FileMode) error {
	if o.failStep == "mkdir" {
		return errors.New("injected mkdir failure")
	}
	return o.base.MkdirAll(path, mode)
}

func (o *faultAtomicConfigOperations) Chmod(path string, mode os.FileMode) error {
	o.chmodCalls++
	if (o.failStep == "chmod-directory" && o.chmodCalls == 1) || (o.failStep == "chmod-target" && o.chmodCalls == 2) {
		return errors.New("injected chmod failure")
	}
	return o.base.Chmod(path, mode)
}

func (o *faultAtomicConfigOperations) CreateTemp(dir, pattern string) (atomicConfigFile, error) {
	if o.failStep == "create-temp" {
		return nil, errors.New("injected create-temp failure")
	}
	file, err := o.base.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return &faultAtomicConfigFile{atomicConfigFile: file, failStep: o.failStep}, nil
}

func (o *faultAtomicConfigOperations) Rename(oldPath, newPath string) error {
	if o.failStep == "rename" {
		return errors.New("injected rename failure")
	}
	return o.base.Rename(oldPath, newPath)
}

func (o *faultAtomicConfigOperations) Remove(path string) error { return o.base.Remove(path) }
func (o *faultAtomicConfigOperations) SyncDirectory(path string) error {
	if o.failStep == "sync-directory" {
		return errors.New("injected sync-directory failure")
	}
	return o.base.SyncDirectory(path)
}

func TestAtomicConfigWriteFaultProjection(t *testing.T) {
	for _, step := range []string{
		"mkdir", "chmod-directory", "create-temp", "chmod-temp", "write-temp",
		"sync-temp", "close-temp", "rename", "chmod-target", "sync-directory",
	} {
		t.Run(step, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent", "models.json")
			ops := &faultAtomicConfigOperations{base: osAtomicConfigOperations{}, failStep: step}
			result, err := writeAtomicConfigWithOperations(path, []byte("candidate"), 0o600, 0o700, ops)
			if err == nil {
				t.Fatalf("%s failure was ignored", step)
			}
			var ioErr *configIOError
			if !errors.As(err, &ioErr) || ioErr.step != step {
				t.Fatalf("error = %v, want typed step %q", err, step)
			}
			wantReplaced := step == "chmod-target" || step == "sync-directory"
			if result.Replaced != wantReplaced {
				t.Fatalf("Replaced = %v, want %v", result.Replaced, wantReplaced)
			}
		})
	}
}

func TestConfigMutationCASExisted(t *testing.T) {
	isolatedHome(t)
	// Create an existing config file.
	agentDir := defaultPiAgentDir()
	configPath := filepath.Join(agentDir, "models.json")
	originalContent := []byte(`{"existing":true}`)
	os.MkdirAll(agentDir, 0755)
	os.WriteFile(configPath, originalContent, 0644)
	// Create effect that writes new content.
	newContent := []byte(`{"providers":{"amagi-test":{}}}`)
	effect := &configMutationEffect{
		target:   launchplan.ConfigPi,
		content:  newContent,
		preimage: sha256.Sum256(originalContent),
	}
	_, err := effect.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Verify file was written.
	data, _ := os.ReadFile(configPath)
	if string(data) == `{"existing":true}` {
		t.Fatal("config not written")
	}
	// Compensate (CAS rollback).
	effect.compensate(context.Background())
	// Verify restored to original.
	data, _ = os.ReadFile(configPath)
	if string(data) != `{"existing":true}` {
		t.Fatalf("config not restored after CAS rollback, got %s", data)
	}
}

func TestConfigMutationCASNonexistent(t *testing.T) {
	isolatedHome(t)
	agentDir := defaultPiAgentDir()
	configPath := filepath.Join(agentDir, "models.json")
	os.RemoveAll(agentDir) // ensure nonexistent
	newContent := []byte(`{"providers":{"amagi-test":{}}}`)
	effect := &configMutationEffect{
		target:   launchplan.ConfigPi,
		content:  newContent,
		preimage: [32]byte{}, // zero digest for nonexistent
	}
	_, err := effect.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatal("config not created")
	}
	// Compensate: should exact-delete since original didn't exist.
	effect.compensate(context.Background())
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatal("config should be deleted after CAS rollback of nonexistent original")
	}
}

func TestConfigMutationCASConcurrentEdit(t *testing.T) {
	isolatedHome(t)
	agentDir := defaultPiAgentDir()
	configPath := filepath.Join(agentDir, "models.json")
	os.MkdirAll(agentDir, 0755)
	originalContent := []byte(`{"original":true}`)
	os.WriteFile(configPath, originalContent, 0644)
	newContent := []byte(`{"providers":{"amagi-test":{}}}`)
	effect := &configMutationEffect{
		target:   launchplan.ConfigPi,
		content:  newContent,
		preimage: sha256.Sum256(originalContent),
	}
	_, err := effect.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Simulate concurrent external edit.
	externalEdit := []byte(`{"external":true}`)
	os.WriteFile(configPath, externalEdit, 0644)
	// Compensate: should NOT overwrite external edit (CAS mismatch).
	outcome := effect.compensate(context.Background())
	data, _ := os.ReadFile(configPath)
	if string(data) != `{"external":true}` {
		t.Fatalf("CAS rollback overwrote external edit, got %s", data)
	}
	// The typed outcome conservatively projects the unresolved CAS owner.
	if outcome.Disposition != launchplan.CompensationIndeterminate {
		t.Fatalf("compensation disposition = %s, want indeterminate", outcome.Disposition)
	}
}

func TestConfigMutationCASOmpAndCodexModeRestore(t *testing.T) {
	home := isolatedHome(t)
	for _, tc := range []struct {
		name     string
		target   launchplan.ConfigTarget
		path     string
		original []byte
		content  []byte
		mode     os.FileMode
	}{
		{name: "omp", target: launchplan.ConfigOmp, path: filepath.Join(home, ".omp", "agent", "models.yml"), original: []byte("providers:\n  retained: true\n"), content: []byte(`{"providers":{"amagi-test":{"apiKey":"secret"}}}`), mode: 0o640},
		{name: "codex", target: launchplan.ConfigCodex, path: filepath.Join(home, ".codex", "config.toml"), original: []byte("model = \"retained\"\n"), content: []byte("replacement-model\nhttps://api.test.example/v1"), mode: 0o604},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Dir(tc.path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(tc.path, tc.original, tc.mode); err != nil {
				t.Fatal(err)
			}
			effect := &configMutationEffect{target: tc.target, content: tc.content, preimage: sha256.Sum256(tc.original)}
			if _, err := effect.Apply(context.Background()); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			outcome := effect.compensate(context.Background())
			if outcome.Disposition != launchplan.CompensationConfirmed {
				t.Fatalf("compensation = %#v", outcome)
			}
			data, err := os.ReadFile(tc.path)
			if err != nil || string(data) != string(tc.original) {
				t.Fatalf("restored content = %q, err=%v", data, err)
			}
			info, err := os.Stat(tc.path)
			if err != nil {
				t.Fatalf("stat restored config: %v", err)
			}
			if info.Mode().Perm() != tc.mode {
				t.Fatalf("restored mode = %v, want %v", info.Mode().Perm(), tc.mode)
			}
		})
	}
}

func TestConfigCompensationUnavailableProjectionAndRetry(t *testing.T) {
	isolatedHome(t)
	path := filepath.Join(defaultPiAgentDir(), "models.json")
	original := []byte(`{"original":"unavailable"}`)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	debts := launchplan.NewCompensationDebtRegistry()
	effect := &configMutationEffect{
		target: launchplan.ConfigPi, content: []byte(`{"providers":{"replacement":{}}}`),
		preimage: sha256.Sum256(original), owner: "session-18/4/config/pi", debts: debts,
	}
	if _, err := effect.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	outcome := effect.compensate(canceled)
	if outcome.Disposition != launchplan.CompensationUnavailable {
		t.Fatalf("canceled compensation = %#v", outcome)
	}
	app := &App{compensationDebts: debts}
	listed := app.GetLaunchCompensationDebts()
	if len(listed) != 1 || listed[0].Disposition != launchplan.CompensationUnavailable {
		t.Fatalf("unavailable debt projection = %#v", listed)
	}
	if err := app.RetryLaunchCompensationDebt(effect.owner); err != nil {
		t.Fatal(err)
	}
	if len(app.GetLaunchCompensationDebts()) != 0 {
		t.Fatal("confirmed unavailable retry retained debt")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(original) {
		t.Fatalf("unavailable retry restored %q, err=%v", data, err)
	}
}

func TestConfigCompensationDebtQueryAndExactRetry(t *testing.T) {
	isolatedHome(t)
	path := filepath.Join(defaultPiAgentDir(), "models.json")
	original := []byte(`{"original":true}`)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	debts := launchplan.NewCompensationDebtRegistry()
	writeCalls := 0
	injectDurabilityFailure := true
	effect := &configMutationEffect{
		target: launchplan.ConfigPi, content: []byte(`{"providers":{"amagi-test":{}}}`),
		preimage: sha256.Sum256(original), owner: "session-17/2/config/pi", debts: debts,
	}
	effect.atomicWrite = func(path string, content []byte, mode, dirMode os.FileMode) (atomicConfigWriteResult, error) {
		writeCalls++
		result, err := writeAtomicConfig(path, content, mode, dirMode)
		if err == nil && injectDurabilityFailure && writeCalls <= 2 {
			return result, &configIOError{step: "sync-directory", err: errors.New("injected durability uncertainty")}
		}
		return result, err
	}
	if _, err := effect.Apply(context.Background()); err == nil {
		t.Fatal("post-rename durability failure was presented as success")
	}
	app := &App{compensationDebts: debts}
	listed := app.GetLaunchCompensationDebts()
	if len(listed) != 1 || listed[0].Owner != effect.owner || listed[0].Disposition != launchplan.CompensationIndeterminate {
		t.Fatalf("debt projection = %#v", listed)
	}
	injectDurabilityFailure = false
	if err := app.RetryLaunchCompensationDebt(effect.owner); err != nil {
		t.Fatalf("RetryLaunchCompensationDebt: %v", err)
	}
	if remaining := app.GetLaunchCompensationDebts(); len(remaining) != 0 {
		t.Fatalf("resolved debt retained: %#v", remaining)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(original) {
		t.Fatalf("retry restored content = %q, err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat retry-restored config: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("retry restored mode = %v", info.Mode().Perm())
	}
}

// --- Derived: DisposeSecrets zeroes env copies ---

func TestDisposeSecretsZeroesConfigCandidate(t *testing.T) {
	planner, _, _, _ := testPlannerSetup(t, contract.CLITypePi, "dispose-provider", "dispose-key-123456789")
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure != nil {
		t.Fatalf("BuildPlan: %v", failure)
	}
	prepared, err := newAppLaunchExecutor(launchExecutorDeps{}).Prepare(context.Background(), plan, launchplan.ExecutionBinding{SessionID: "dispose", RunEpoch: 1, RunHandle: "run"})
	if err != nil {
		plan.Secrets.Dispose()
		t.Fatal(err)
	}
	var derived []byte
	for i := 0; i < prepared.Count(); i++ {
		if effect, ok := prepared.Effect(i).(*configMutationEffect); ok {
			derived = effect.content
		}
	}
	if len(derived) == 0 {
		t.Fatal("config candidate copy was not prepared")
	}
	prepared.DisposeSecrets()
	for i, value := range derived {
		if value != 0 {
			t.Fatalf("derived config byte %d was not zeroed", i)
		}
	}
}

func TestDisposeSecretsZeroesEnv(t *testing.T) {
	p, _, _, _ := testPlannerSetup(t, contract.CLITypeOpenCode, "env-provider", "env-key-123456789")
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeOpenCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure != nil {
		t.Fatalf("BuildPlan: %v", failure)
	}
	exec := newAppLaunchExecutor(launchExecutorDeps{})
	prepared, err := exec.Prepare(context.Background(), plan, launchplan.ExecutionBinding{
		SessionID: "s", RunHandle: "dummy",
	})
	if err != nil {
		plan.Secrets.Dispose()
		t.Fatalf("Prepare: %v", err)
	}
	// Find the ptyStartEffect and check it has non-empty env.
	var hadEnv bool
	for i := 0; i < prepared.Count(); i++ {
		if pt, ok := prepared.Effect(i).(*ptyStartEffect); ok {
			if len(pt.spec.Env.Variables) > 0 {
				hadEnv = true
			}
		}
	}
	prepared.DisposeSecrets()
	// After dispose, env variables should be zeroed.
	for i := 0; i < prepared.Count(); i++ {
		if pt, ok := prepared.Effect(i).(*ptyStartEffect); ok {
			for _, v := range pt.spec.Env.Variables {
				if v != "" {
					t.Fatal("env variable not zeroed after DisposeSecrets")
				}
			}
		}
	}
	if !hadEnv {
		t.Fatal("no ptyStartEffect with non-empty env found; test setup did not exercise secret env vars")
	}
}

// --- Executor Prepare/Abort ---

type unresolvedCloseWaiter struct{}

func (unresolvedCloseWaiter) Wait(context.Context) error {
	return errors.New("close remains unresolved")
}
func (unresolvedCloseWaiter) Confirmed() bool { return false }

type unresolvedCloseBinding struct {
	id         processcap.BindingID
	closeCalls uint64
}

func (b *unresolvedCloseBinding) BindingID() processcap.BindingID { return b.id }
func (b *unresolvedCloseBinding) CloseExact(context.Context) processcap.ExactCloseEvidence {
	b.closeCalls++
	evidence, err := processcap.NewExactCloseEvidence(b.id, b.closeCalls, processcap.CloseIndeterminate, unresolvedCloseWaiter{})
	if err != nil {
		panic(err)
	}
	return evidence
}

type readyFailurePTY struct{ binding *unresolvedCloseBinding }

func (p *readyFailurePTY) StartResolvedWithRunEvidence(string, platform.ResolvedLaunchSpec, any) (processcap.StartEvidence, error) {
	return processcap.StartEvidence{PID: 7171, Binding: p.binding}, nil
}
func (*readyFailurePTY) WaitReadyForBinding(context.Context, string, processcap.BindingID) error {
	return errors.New("injected ready timeout")
}
func (*readyFailurePTY) WriteRawForBinding(context.Context, string, processcap.BindingID, []byte) error {
	return nil
}

func TestExecutorAbortRetriesUnrecordedPartialPTYApply(t *testing.T) {
	owner, err := processcap.NewOwnerID()
	if err != nil {
		t.Fatal(err)
	}
	binding := &unresolvedCloseBinding{id: processcap.BindingID{Kind: processcap.BackendPTY, Owner: owner, Generation: 1}}
	plan := &launchplan.Plan{
		Recipe: launchplan.StableRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: t.TempDir()},
		Effects: []launchplan.EffectSpec{{
			Kind: launchplan.EffectPTYStart,
			Process: &launchplan.ProcessStartSpec{Mode: launchplan.ModeEmbedded, Resolved: platform.ResolvedLaunchSpec{
				AppType: "claudecode", LaunchMode: "embedded", WorkDir: t.TempDir(),
				CLI: platform.ResolvedCLI{Name: "claudecode", Path: "/fake/claude"},
			}},
		}},
		Secrets: launchplan.NewEphemeralSecretBundle(),
	}
	prepared, err := newAppLaunchExecutor(launchExecutorDeps{pty: &readyFailurePTY{binding: binding}}).Prepare(context.Background(), plan, launchplan.ExecutionBinding{SessionID: "partial", RunEpoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	effect := prepared.Effect(0)
	effect.ArmOwnership()
	if _, err := effect.Apply(context.Background()); err == nil {
		t.Fatal("ready failure was presented as success")
	}
	report := prepared.Abort(context.Background())
	if report.Attempted != 1 || report.Failed != 1 || binding.closeCalls != 2 {
		t.Fatalf("partial Apply compensation report=%#v closeCalls=%d, want attempted/failed/closeCalls=1/1/2", report, binding.closeCalls)
	}
	prepared.DisposeSecrets()
}

func TestExecutorPrepareAndAbort(t *testing.T) {
	p, _, _, _ := testPlannerSetup(t, contract.CLITypeOpenCode, "exec-provider", "exec-key-123456789")
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeOpenCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure != nil {
		t.Fatalf("BuildPlan: %v", failure)
	}
	exec := newAppLaunchExecutor(launchExecutorDeps{})
	prepared, err := exec.Prepare(context.Background(), plan, launchplan.ExecutionBinding{
		SessionID: "test-session", RunHandle: "dummy-run-handle",
	})
	if err != nil {
		plan.Secrets.Dispose()
		t.Fatalf("Prepare: %v", err)
	}
	report := prepared.Abort(context.Background())
	if report.Failed > 0 {
		t.Fatalf("Abort should not fail for unapplied effects, got %d failed", report.Failed)
	}
	prepared.DisposeSecrets()
}

// --- Zero side effects ---

func TestBuildPlanDoesNotWriteConfigFiles(t *testing.T) {
	dir := isolatedHome(t)
	t.Setenv("HOME", dir) // extra safety
	p, _, _, _ := testPlannerSetup(t, contract.CLITypePi, "sideffect-provider", "side-key-123456789")
	// Reset HOME to test dir after testPlannerSetup (which sets it to its own temp).
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure != nil {
		t.Fatalf("BuildPlan: %v", failure)
	}
	plan.Secrets.Dispose()
	if _, err := os.Stat(filepath.Join(dir, ".pi", "agent", "models.json")); err == nil {
		t.Fatal("BuildPlan should not write pi models.json")
	}
}

// --- Workdir validation/fallback (launch_workdir.go) ---

// 无效的远程 workdir 不再作为不可用的 lpCurrentDirectory 击穿到 ConPTY，
// 而是回退 canonical workdir（默认目录→Home）后正常出计划。
func TestBuildPlanInvalidWorkdirFallsBackToCanonical(t *testing.T) {
	p, _, _, _ := testPlannerSetup(t, contract.CLITypePi, "workdir-fallback-provider", "workdir-key-123456789")
	missing := filepath.Join(t.TempDir(), "definitely-missing")
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
		Workdir: missing,
	})
	if failure != nil {
		t.Fatalf("BuildPlan should fall back to canonical workdir, got failure: %v", failure)
	}
	defer plan.Secrets.Dispose()
	if plan.Recipe.Workdir == "" || plan.Recipe.Workdir == missing {
		t.Fatalf("Recipe.Workdir = %q, want canonical fallback (not the invalid %q)", plan.Recipe.Workdir, missing)
	}
	if _, err := os.Stat(plan.Recipe.Workdir); err != nil {
		t.Fatalf("fallback workdir %q should exist: %v", plan.Recipe.Workdir, err)
	}
}

// requested、canonical（homeDir 字段）与真实 HOME 全部不可用时按 FailureWorkdir 失败。
func TestBuildPlanAllWorkdirsInvalidFails(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()
	missingHome := filepath.Join(dir, "missing-home")
	t.Setenv("HOME", missingHome)
	t.Setenv("USERPROFILE", missingHome)
	settingsSvc := settings.NewService(dir)
	if err := settingsSvc.Load(); err != nil {
		t.Fatalf("settings Load: %v", err)
	}
	defaults, _ := launchplan.NewDefaultStore(settingsSvc)
	defaults.RecordDesktopActivation(launchplan.StableRecipe{
		CLIType: contract.CLITypeClaudeCode, Workdir: filepath.Join(dir, "recipe"),
		ProviderRef: "p", PresetRef: "preset", ModelRef: "m",
	})
	p := newAppLaunchPlanner(
		config.NewConfigService(dir), secrets.NewSecretsService(dir),
		defaults, fakeCLIResolver{}, fakePlatformCaps(),
		paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), missingHome,
	)
	plan, failure := p.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
		Workdir: filepath.Join(dir, "missing-requested"),
	})
	if plan != nil {
		plan.Secrets.Dispose()
		t.Fatal("BuildPlan should fail when every workdir candidate is unusable")
	}
	if failure == nil || failure.Kind != launchplan.FailureWorkdir {
		t.Fatalf("expected FailureWorkdir, got %v", failure)
	}
}
