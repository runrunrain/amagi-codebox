package main

// launch_planner.go — Production launchplan.Planner that resolves desktop
// activation defaults into canonical Plans for all five CLIs.
//
// Design constraints (composite-commit-addendum.md §8):
//   - BuildPlan is pure read/pre-parse: no config write, no service mutation,
//     no process spawn. All side effects live in the Executor Apply phase.
//   - Remote/restart origins force ModeEmbedded.
//   - Per-CLI defaults are read from settings remoteLaunchDefaultsV1; missing
//     defaults produce a typed FailureLaunchContext (Probe=false, Create 422).
//   - Probe and Create share the same BuildPlan path.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/session"
)

// launchConfigPort is the narrow read-only interface over config.ConfigService
// used by the planner. Defined as an interface for testability.
type launchConfigPort interface {
	GetProvider(name string) (*config.Provider, error)
	ResolveTerminalPreset(terminalType, key string) (string, *config.TerminalPreset, error)
	GetOpenCodePreset(key string) (*config.OpenCodePreset, error)
	GetAgentTeams() config.AgentTeamsConfig
}

// launchSecretsPort is the narrow read-only interface over secrets.SecretsService.
type launchSecretsPort interface {
	GetAPIKeyWithFallback(provider string) (string, string)
	GetAPIKey(provider string) (string, error)
}

// launchEnvVarsPort is the narrow read-only interface over envvars.EnvVarsService.
type launchEnvVarsPort interface {
	MergeWithSystem() []string
}

// appLaunchPlanner is the production launchplan.Planner. It reads stored
// desktop activation defaults, re-resolves provider/preset/model/secret/CLI/
// shell/workdir, and builds a canonical Plan with typed Effects. BuildPlan has
// zero side effects; the Executor applies mutations.
type appLaunchPlanner struct {
	config       launchConfigPort
	secrets      launchSecretsPort
	defaults     *launchplan.DefaultStore
	resolver     platform.CLIResolver
	capabilities platform.PlatformCapabilities
	paths        *paths.PathsService
	envVars      launchEnvVarsPort
	homeDir      string
}

func newAppLaunchPlanner(
	cfg launchConfigPort,
	sec launchSecretsPort,
	defaults *launchplan.DefaultStore,
	resolver platform.CLIResolver,
	caps platform.PlatformCapabilities,
	pathsSvc *paths.PathsService,
	envVarsSvc launchEnvVarsPort,
	homeDir string,
) *appLaunchPlanner {
	return &appLaunchPlanner{
		config:       cfg,
		secrets:      sec,
		defaults:     defaults,
		resolver:     resolver,
		capabilities: caps,
		paths:        pathsSvc,
		envVars:      envVarsSvc,
		homeDir:      homeDir,
	}
}

// ---------------------------------------------------------------------------
// Planner interface
// ---------------------------------------------------------------------------

func (p *appLaunchPlanner) BuildPlan(_ context.Context, req launchplan.BuildRequest) (*launchplan.Plan, *launchplan.BuildFailure) {
	if !launchplan.KnownCLIType(req.CLIType) {
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureLaunchContext, CLIType: req.CLIType}
	}
	// Remote/restart force embedded.
	if req.Origin == launchplan.OriginRemote || req.Origin == launchplan.OriginRestart {
		req.Mode = launchplan.ModeEmbedded
	}
	// Read per-CLI defaults from settings.
	recipe, ok := p.defaults.HostDefaultRefs(req.CLIType)
	if !ok {
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureLaunchContext, CLIType: req.CLIType}
	}
	// Canonical workdir.
	workdir := strings.TrimSpace(req.Workdir)
	if workdir == "" {
		workdir = p.canonicalWorkdir()
	}
	if workdir == "" {
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureWorkdir, CLIType: req.CLIType}
	}
	if err := platform.ValidateLaunchRequest(p.capabilities, string(session.ModeEmbedded)); err != nil {
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureCapability, CLIType: req.CLIType}
	}
	recipe.Workdir = workdir

	switch req.CLIType {
	case contract.CLITypeClaudeCode:
		return p.buildClaudePlan(recipe, req)
	case contract.CLITypeOpenCode:
		return p.buildOpenCodePlan(recipe, req)
	case contract.CLITypeCodex:
		return p.buildCodexPlan(recipe, req)
	case contract.CLITypePi:
		return p.buildPiPlan(recipe, req)
	case contract.CLITypeOmp:
		return p.buildOmpPlan(recipe, req)
	default:
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureLaunchContext, CLIType: req.CLIType}
	}
}

func (p *appLaunchPlanner) Probe(ctx context.Context, cli contract.CLIType) (contract.CLIAvailability, *launchplan.BuildFailure) {
	plan, failure := p.BuildPlan(ctx, launchplan.BuildRequest{
		CLIType: cli,
		Origin:  launchplan.OriginRemote,
		Mode:    launchplan.ModeEmbedded,
	})
	if plan != nil {
		plan.Secrets.Dispose()
		return contract.CLIAvailability{CLIType: cli, Available: true}, nil
	}
	return contract.CLIAvailability{CLIType: cli, Available: false}, failure
}

// ---------------------------------------------------------------------------
// Claude Code
// ---------------------------------------------------------------------------

func (p *appLaunchPlanner) buildClaudePlan(recipe launchplan.StableRecipe, req launchplan.BuildRequest) (*launchplan.Plan, *launchplan.BuildFailure) {
	fail := func(kind launchplan.BuildFailureKind) (*launchplan.Plan, *launchplan.BuildFailure) {
		return nil, &launchplan.BuildFailure{Kind: kind, CLIType: req.CLIType}
	}

	providerName := recipe.ProviderRef
	presetName := recipe.PresetRef

	// Terminal preset bridge.
	tpProvider, tp, tpErr := p.config.ResolveTerminalPreset("claude_code", presetName)
	tpFound := tpErr == nil && tp != nil
	if tpFound && tpProvider != "" {
		providerName = tpProvider
	}

	provider, err := p.config.GetProvider(providerName)
	if err != nil || provider == nil {
		return fail(launchplan.FailureLaunchContext)
	}
	// Bridge terminal preset into provider copy for model/parameter resolution.
	if tpFound {
		provCopy := *provider
		converted := config.Preset{
			Name: tp.Name, Model: tp.Model, ModelHaiku: tp.ModelHaiku,
			ModelSonnet: tp.ModelSonnet, ModelOpus: tp.ModelOpus, Parameters: tp.Parameters,
		}
		if provCopy.Presets == nil {
			provCopy.Presets = map[string]config.Preset{}
		}
		provCopy.Presets[presetName] = converted
		*provider = provCopy
	}
	if !provider.IsAnthropicCompatible() {
		return fail(launchplan.FailureLaunchContext)
	}

	// API key.
	var apiKey string
	if !provider.IsOAuthMode() {
		apiKey = p.getAPIKey(providerName, *provider)
		if apiKey == "" {
			return fail(launchplan.FailureLaunchContext)
		}
	}

	// Preset is resolved inside computeClaudeOverrides via provider.Presets.

	// Compute env overrides (pure; replicates launcher.BuildOverrides logic
	// without mutating the LauncherService proxyPort).
	effectiveProxyPort := claudeEffectiveProxyPort(recipe.UseProxy, recipe.UseHeadroom)
	overrides := computeClaudeOverrides(*provider, presetName, apiKey, p.config.GetAgentTeams(), effectiveProxyPort)
	env := launcher.BuildEnv(p.envVars.MergeWithSystem(), overrides)

	// Resolve CLI/shell.
	spec, err := p.resolver.Resolve(platform.ResolveRequest{
		AppType: string(session.AppTypeClaudeCode), LaunchMode: string(session.ModeEmbedded),
		RequestedShellPath: recipe.ShellRef, WorkDir: recipe.Workdir, Env: env,
		PTYCols: 120, PTYRows: 40,
	})
	if err != nil {
		return fail(launchplan.FailureCapability)
	}

	// Build effects in canonical order: Headroom → Proxy → PTY → Bootstrap.
	var admissions []launchplan.SharedAdmissionSpec
	var effects []launchplan.EffectSpec
	realBackend := strings.TrimRight(provider.EffectiveBaseURL("anthropic"), "/")
	if recipe.UseHeadroom {
		fp := sharedFingerprintForHeadroom("claude-headroom", realBackend, headroom.DefaultPort)
		admissions = append(admissions, launchplan.SharedAdmissionSpec{Service: launchplan.SharedClaudeHeadroom, ConfigFingerprint: fp})
		effects = append(effects, launchplan.EffectSpec{
			Kind: launchplan.EffectHeadroomStart,
			Shared: &launchplan.SharedStartSpec{
				Service: launchplan.SharedClaudeHeadroom, ConfigFingerprint: fp,
				UpstreamURL: realBackend, ListenPort: headroom.DefaultPort,
			},
		})
	}
	if recipe.UseProxy {
		// When both proxy and headroom are active, proxy forwards to headroom.
		proxyUpstream := realBackend
		if recipe.UseHeadroom {
			proxyUpstream = fmt.Sprintf("http://127.0.0.1:%d", headroom.DefaultPort)
		}
		fp := sharedFingerprintForProxy(proxyUpstream, proxyDefaultPort)
		admissions = append(admissions, launchplan.SharedAdmissionSpec{Service: launchplan.SharedClaudeProxy, ConfigFingerprint: fp})
		effects = append(effects, launchplan.EffectSpec{
			Kind: launchplan.EffectProxyStart,
			Shared: &launchplan.SharedStartSpec{
				Service: launchplan.SharedClaudeProxy, ConfigFingerprint: fp,
				UpstreamURL: proxyUpstream, ListenPort: proxyDefaultPort,
			},
		})
	}
	effects = append(effects, launchplan.EffectSpec{
		Kind: launchplan.EffectPTYStart,
		Process: &launchplan.ProcessStartSpec{
			Mode: launchplan.ModeEmbedded, Resolved: spec, RequireRunHandle: true,
		},
	})
	// C-01: Bootstrap write effect carries the canonical startup command so the
	// executor writes it through the control-gated path before composite publish.
	effects = append(effects, buildBootstrapEffect(spec))

	return p.finalizePlan(recipe, admissions, effects)
}

// ---------------------------------------------------------------------------
// OpenCode
// ---------------------------------------------------------------------------

func (p *appLaunchPlanner) buildOpenCodePlan(recipe launchplan.StableRecipe, req launchplan.BuildRequest) (*launchplan.Plan, *launchplan.BuildFailure) {
	fail := func(kind launchplan.BuildFailureKind) (*launchplan.Plan, *launchplan.BuildFailure) {
		return nil, &launchplan.BuildFailure{Kind: kind, CLIType: req.CLIType}
	}

	presetName := recipe.PresetRef
	envOverrides := map[string]string{}
	sessionProvider := "opencode"

	// Track 1: opencode_presets (new model).
	if presetName != "" {
		if ocPreset, err := p.config.GetOpenCodePreset(presetName); err == nil && ocPreset != nil {
			getAPIKey := func(localProvider, _ string) (string, error) {
				if local, pErr := p.config.GetProvider(localProvider); pErr == nil && local != nil {
					return p.getAPIKey(localProvider, *local), nil
				}
				return "", nil
			}
			getProvider := func(name string) (*config.Provider, error) {
				return p.config.GetProvider(name)
			}
			ocOverrides, err := launcher.BuildOpenCodeEnvOverridesFromPreset(*ocPreset, getAPIKey, getProvider)
			if err != nil {
				return fail(launchplan.FailureLaunchContext)
			}
			envOverrides = ocOverrides
			sessionProvider = "opencode-preset:" + presetName
			goto resolve
		}
	}
	// Track 2: terminal_presets / legacy provider preset.
	{
		providerName := recipe.ProviderRef
		tpProvider, tp, tpErr := p.config.ResolveTerminalPreset("opencode", presetName)
		tpFound := tpErr == nil && tp != nil
		if tpFound && tpProvider != "" {
			providerName = tpProvider
		}
		if providerName != "" {
			provider, err := p.config.GetProvider(providerName)
			if err != nil || provider == nil {
				return fail(launchplan.FailureLaunchContext)
			}
			if tpFound {
				provCopy := *provider
				converted := config.Preset{
					Name: tp.Name, Model: tp.Model, Parameters: tp.Parameters, OpenCodeConfig: tp.OpenCodeCfg,
				}
				if provCopy.Presets == nil {
					provCopy.Presets = map[string]config.Preset{}
				}
				provCopy.Presets[presetName] = converted
				*provider = provCopy
			}
			apiKey := p.getAPIKey(providerName, *provider)
			if apiKey == "" {
				return fail(launchplan.FailureLaunchContext)
			}
			ocOverrides, err := launcher.BuildOpenCodeEnvOverrides(providerName, *provider, presetName, apiKey)
			if err != nil {
				return fail(launchplan.FailureLaunchContext)
			}
			envOverrides = ocOverrides
			sessionProvider = providerName
		}
	}

resolve:
	env := launcher.BuildEnv(p.envVars.MergeWithSystem(), envOverrides)
	spec, err := p.resolver.Resolve(platform.ResolveRequest{
		AppType: string(session.AppTypeOpenCode), LaunchMode: string(session.ModeEmbedded),
		RequestedShellPath: recipe.ShellRef, WorkDir: recipe.Workdir, Env: env,
		PTYCols: 120, PTYRows: 40,
	})
	if err != nil {
		return fail(launchplan.FailureCapability)
	}

	// Update recipe provider ref for stable identification.
	recipe.ProviderRef = sessionProvider

	effects := []launchplan.EffectSpec{{
		Kind: launchplan.EffectPTYStart,
		Process: &launchplan.ProcessStartSpec{
			Mode: launchplan.ModeEmbedded, Resolved: spec, RequireRunHandle: true,
		},
	}}
	effects = append(effects, buildBootstrapEffect(spec))
	return p.finalizePlan(recipe, nil, effects)
}

// ---------------------------------------------------------------------------
// Codex
// ---------------------------------------------------------------------------

func (p *appLaunchPlanner) buildCodexPlan(recipe launchplan.StableRecipe, req launchplan.BuildRequest) (*launchplan.Plan, *launchplan.BuildFailure) {
	fail := func(kind launchplan.BuildFailureKind) (*launchplan.Plan, *launchplan.BuildFailure) {
		return nil, &launchplan.BuildFailure{Kind: kind, CLIType: req.CLIType}
	}

	providerID := recipe.ProviderRef
	modelRef := recipe.ModelRef
	var selectedPresetParams *config.Parameters

	// Terminal preset / legacy fallback for model resolution.
	tpProvider, tp, tpErr := p.config.ResolveTerminalPreset("codex", modelRef)
	tpFound := tpErr == nil && tp != nil
	if tpFound {
		if tpProvider != "" {
			providerID = tpProvider
		}
		modelRef = tp.Model
		params := tp.Parameters
		selectedPresetParams = &params
	}
	if !tpFound && providerID != "" {
		if provider, pErr := p.config.GetProvider(providerID); pErr == nil && provider != nil {
			if preset, ok := provider.Presets[modelRef]; ok {
				params := preset.Parameters
				selectedPresetParams = &params
				resolved := preset.Model
				if resolved == "" {
					resolved = provider.DefaultModel
				}
				modelRef = resolved
			}
		}
	}

	provider, err := p.config.GetProvider(providerID)
	if err != nil || provider == nil {
		return fail(launchplan.FailureLaunchContext)
	}
	if !isOpenAIProvider(*provider) {
		return fail(launchplan.FailureLaunchContext)
	}
	launchSettings := resolveCodexLaunchSettings(*provider, modelRef)
	if selectedPresetParams != nil {
		applyCodexPresetParameters(&launchSettings, *selectedPresetParams)
	}
	if launchSettings.Model == "" {
		return fail(launchplan.FailureLaunchContext)
	}

	// Provider env injection.
	envOverrides := map[string]string{}
	codexProviderBaseURL := ""
	apiKey := p.getAPIKey(providerID, *provider)
	if apiKey != "" {
		envOverrides["OPENAI_API_KEY"] = apiKey
		if baseURL := provider.EffectiveBaseURL("openai"); baseURL != "" {
			envOverrides["OPENAI_BASE_URL"] = baseURL
			if isCustomCodexOpenAIBaseURL(baseURL) {
				codexProviderBaseURL = baseURL
			}
		}
	}
	if baseURL := provider.EffectiveBaseURL("openai"); isCustomCodexOpenAIBaseURL(baseURL) && codexProviderBaseURL == "" {
		return fail(launchplan.FailureLaunchContext)
	}

	launchSettings.ProviderBaseURL = codexProviderBaseURL

	env := launcher.BuildEnv(p.envVars.MergeWithSystem(), envOverrides)
	args := buildCodexCLIArgs(launchSettings)
	spec, err := p.resolver.Resolve(platform.ResolveRequest{
		AppType: string(session.AppTypeCodex), LaunchMode: string(session.ModeEmbedded),
		RequestedShellPath: recipe.ShellRef, WorkDir: recipe.Workdir, Env: env,
		CLIArgs: args, PTYCols: 120, PTYRows: 40,
	})
	if err != nil {
		return fail(launchplan.FailureCapability)
	}

	// Effects: [HeadroomStart?] PTYStart. Model/provider/preset routing is
	// carried as per-process CLI overrides, so concurrent Codex sessions never
	// race by rewriting the user's config.toml.
	var admissions []launchplan.SharedAdmissionSpec
	var effects []launchplan.EffectSpec
	if recipe.UseHeadroom {
		upstream := strings.TrimRight(provider.EffectiveBaseURL("openai"), "/")
		if upstream == "" {
			upstream = codexGlobalHeadroomDefaultTarget
		}
		fp := sharedFingerprintForHeadroom("codex-headroom", upstream, CodexGlobalHeadroomDefaultPort)
		admissions = append(admissions, launchplan.SharedAdmissionSpec{Service: launchplan.SharedCodexHeadroom, ConfigFingerprint: fp})
		effects = append(effects, launchplan.EffectSpec{
			Kind: launchplan.EffectHeadroomStart,
			Shared: &launchplan.SharedStartSpec{
				Service: launchplan.SharedCodexHeadroom, ConfigFingerprint: fp,
				UpstreamURL: upstream, ListenPort: CodexGlobalHeadroomDefaultPort,
			},
		})
	}
	effects = append(effects, launchplan.EffectSpec{
		Kind: launchplan.EffectPTYStart,
		Process: &launchplan.ProcessStartSpec{
			Mode: launchplan.ModeEmbedded, Resolved: spec, RequireRunHandle: true,
		},
	})
	effects = append(effects, buildBootstrapEffect(spec))
	return p.finalizePlan(recipe, admissions, effects)
}

// ---------------------------------------------------------------------------
// Pi
// ---------------------------------------------------------------------------

func (p *appLaunchPlanner) buildPiPlan(recipe launchplan.StableRecipe, req launchplan.BuildRequest) (*launchplan.Plan, *launchplan.BuildFailure) {
	providerID := recipe.ProviderRef
	modelRef := recipe.ModelRef
	tpProvider, tp, tpErr := p.config.ResolveTerminalPreset("pi", modelRef)
	tpFound := tpErr == nil && tp != nil
	var presetParams config.Parameters
	if tpFound {
		if tpProvider != "" {
			providerID = tpProvider
		}
		modelRef = tp.Model
		presetParams = tp.Parameters
	}
	if !tpFound && providerID != "" {
		if prov, pErr := p.config.GetProvider(providerID); pErr == nil && prov != nil {
			if preset, ok := prov.Presets[modelRef]; ok {
				resolved := preset.Model
				if resolved == "" {
					resolved = prov.DefaultModel
				}
				modelRef = resolved
				presetParams = preset.Parameters
			}
		}
	}
	var result piOmpLaunchResult
	if prov, pErr := p.config.GetProvider(providerID); pErr == nil && prov != nil {
		s := resolvePiLaunchSettings(*prov, modelRef, presetParams)
		result = piOmpLaunchResult{Provider: s.Provider, Model: s.Model, Thinking: s.Thinking}
	} else {
		result = piOmpLaunchResult{Model: modelRef}
	}
	return p.buildPiOmpPlan(recipe, req, session.AppTypePi, "pi",
		launcher.BuildPiModelsConfig, launcher.MergePiAgentConfig,
		defaultPiAgentDir, launchplan.ConfigPi, result,
	)
}

// ---------------------------------------------------------------------------
// Omp
// ---------------------------------------------------------------------------

func (p *appLaunchPlanner) buildOmpPlan(recipe launchplan.StableRecipe, req launchplan.BuildRequest) (*launchplan.Plan, *launchplan.BuildFailure) {
	providerID := recipe.ProviderRef
	modelRef := recipe.ModelRef
	tpProvider, tp, tpErr := p.config.ResolveTerminalPreset("omp", modelRef)
	tpFound := tpErr == nil && tp != nil
	var presetParams config.Parameters
	if tpFound {
		if tpProvider != "" {
			providerID = tpProvider
		}
		modelRef = tp.Model
		presetParams = tp.Parameters
	}
	if !tpFound && providerID != "" {
		if prov, pErr := p.config.GetProvider(providerID); pErr == nil && prov != nil {
			if preset, ok := prov.Presets[modelRef]; ok {
				resolved := preset.Model
				if resolved == "" {
					resolved = prov.DefaultModel
				}
				modelRef = resolved
				presetParams = preset.Parameters
			}
		}
	}
	var result piOmpLaunchResult
	if prov, pErr := p.config.GetProvider(providerID); pErr == nil && prov != nil {
		s := resolveOmpLaunchSettings(*prov, modelRef, presetParams)
		result = piOmpLaunchResult{Provider: s.Provider, Model: s.Model, Thinking: s.Thinking}
	} else {
		result = piOmpLaunchResult{Model: modelRef}
	}
	return p.buildPiOmpPlan(recipe, req, session.AppTypeOhMyPi, "omp",
		launcher.BuildOmpModelsConfig, launcher.MergeOmpModelsConfig,
		defaultOmpAgentDir, launchplan.ConfigOmp, result,
	)
}

// piOmpConfigBuilder is the shared signature for BuildPiModelsConfig / BuildOmpModelsConfig.
type piOmpConfigBuilder func(providerName string, provider config.Provider, model string, apiKey string, params config.Parameters) (map[string]any, error)

// piOmpConfigMerger is the shared signature for MergePiAgentConfig / MergeOmpModelsConfig.
type piOmpConfigMerger func(cfg map[string]any, agentDir string) map[string]any

// piOmpLaunchResult holds the resolved provider/model/thinking for Pi/Omp.
type piOmpLaunchResult struct {
	Provider string
	Model    string
	Thinking string
}

func (p *appLaunchPlanner) buildPiOmpPlan(
	recipe launchplan.StableRecipe,
	req launchplan.BuildRequest,
	appType session.AppType,
	terminalType string,
	configBuilder piOmpConfigBuilder,
	configMerger piOmpConfigMerger,
	agentDirFn func() string,
	configTarget launchplan.ConfigTarget,
	launchResult piOmpLaunchResult,
) (*launchplan.Plan, *launchplan.BuildFailure) {
	fail := func(kind launchplan.BuildFailureKind) (*launchplan.Plan, *launchplan.BuildFailure) {
		return nil, &launchplan.BuildFailure{Kind: kind, CLIType: req.CLIType}
	}

	providerID := recipe.ProviderRef
	modelRef := recipe.ModelRef

	// Terminal preset / legacy fallback.
	tpProvider, tp, tpErr := p.config.ResolveTerminalPreset(terminalType, modelRef)
	tpFound := tpErr == nil && tp != nil
	var presetParams config.Parameters
	if tpFound {
		if tpProvider != "" {
			providerID = tpProvider
		}
		modelRef = tp.Model
		presetParams = tp.Parameters
	}
	if !tpFound && providerID != "" {
		if provider, pErr := p.config.GetProvider(providerID); pErr == nil && provider != nil {
			if preset, ok := provider.Presets[modelRef]; ok {
				resolved := preset.Model
				if resolved == "" {
					resolved = provider.DefaultModel
				}
				modelRef = resolved
				presetParams = preset.Parameters
			}
		}
	}

	provider, err := p.config.GetProvider(providerID)
	if err != nil || provider == nil {
		return fail(launchplan.FailureLaunchContext)
	}
	if launchResult.Model == "" {
		return fail(launchplan.FailureLaunchContext)
	}

	apiKey := p.getAPIKey(providerID, *provider)

	// M-02: OAuth is the only keyless built-in mode. Every generated custom
	// provider is credential-bearing and fails with a typed launch-context error
	// before config/process effects when its resolved secret is absent.
	envOverrides := map[string]string{
		"PI_CODING_AGENT_DIR": "", // BuildEnv deletes on empty.
	}
	var configBuf []byte
	var preimage [32]byte
	hasConfigMutation := false
	if provider.IsOAuthMode() {
		var inheritedCredential string
		launchResult.Provider, inheritedCredential = piProviderMappingForType(terminalType, *provider)
		if launchResult.Provider == "" {
			return fail(launchplan.FailureLaunchContext)
		}
		if inheritedCredential != "" {
			envOverrides[inheritedCredential] = ""
		}
		switch launchResult.Provider {
		case "anthropic":
			envOverrides["ANTHROPIC_API_KEY"] = ""
			envOverrides["ANTHROPIC_AUTH_TOKEN"] = ""
			envOverrides["ANTHROPIC_BASE_URL"] = ""
		case "openai":
			envOverrides["OPENAI_API_KEY"] = ""
			envOverrides["OPENAI_BASE_URL"] = ""
		}
	} else {
		if strings.TrimSpace(apiKey) == "" {
			return fail(launchplan.FailureLaunchContext)
		}
		cfg, cfgErr := configBuilder(providerID, *provider, launchResult.Model, apiKey, presetParams)
		if cfgErr != nil {
			return fail(launchplan.FailureLaunchContext)
		}
		agentDir := agentDirFn()
		cfg = configMerger(cfg, agentDir)
		jsonBytes, jsonErr := json.Marshal(cfg)
		if jsonErr != nil {
			return fail(launchplan.FailureLaunchContext)
		}
		configBuf = jsonBytes
		preimage = computeFilePreimage(filepath.Join(agentDir, configFileName(configTarget)))
		hasConfigMutation = true
		launchResult.Provider = customProviderID(terminalType, providerID)
		if _, apiKeyEnv := piProviderMappingForType(terminalType, *provider); apiKeyEnv != "" {
			envOverrides[apiKeyEnv] = apiKey
		}
	}

	env := launcher.BuildEnv(p.envVars.MergeWithSystem(), envOverrides)
	args := []string{}
	if launchResult.Provider != "" {
		args = append(args, "--provider", launchResult.Provider)
	}
	if launchResult.Model != "" {
		args = append(args, "--model", launchResult.Model)
	}
	if launchResult.Thinking != "" {
		args = append(args, "--thinking", launchResult.Thinking)
	}
	spec, err := p.resolver.Resolve(platform.ResolveRequest{
		AppType: string(appType), LaunchMode: string(session.ModeEmbedded),
		RequestedShellPath: recipe.ShellRef, WorkDir: recipe.Workdir, Env: env,
		CLIArgs: args, PTYCols: 120, PTYRows: 40,
	})
	if err != nil {
		return fail(launchplan.FailureCapability)
	}

	var effects []launchplan.EffectSpec
	var secrets *launchplan.EphemeralSecretBundle
	if hasConfigMutation {
		secrets = launchplan.NewEphemeralSecretBundle(configBuf)
		effects = append(effects, launchplan.EffectSpec{
			Kind: launchplan.EffectConfigMutation,
			Config: &launchplan.ConfigMutationSpec{
				Target: configTarget, Candidate: launchplan.SecretBufferRef{Index: 0},
				ExpectedPreimageDigest: preimage,
			},
		})
	}
	effects = append(effects, launchplan.EffectSpec{
		Kind: launchplan.EffectPTYStart,
		Process: &launchplan.ProcessStartSpec{
			Mode: launchplan.ModeEmbedded, Resolved: spec, RequireRunHandle: true,
		},
	})
	effects = append(effects, buildBootstrapEffect(spec))
	if secrets == nil {
		secrets = launchplan.NewEphemeralSecretBundle()
	}
	return p.finalizePlanWithSecrets(recipe, nil, effects, secrets)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (p *appLaunchPlanner) canonicalWorkdir() string {
	if p.paths != nil {
		if dir := p.paths.GetDefaultPath(); dir != "" {
			return dir
		}
	}
	if p.homeDir != "" {
		return p.homeDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func (p *appLaunchPlanner) getAPIKey(providerName string, provider config.Provider) string {
	if key, _ := p.secrets.GetAPIKeyWithFallback(providerName); key != "" {
		return key
	}
	for _, format := range legacyProviderAPIKeyCandidates(provider) {
		if key, err := p.secrets.GetAPIKey(providerName + ":" + format); err == nil {
			trimmed := strings.TrimSpace(key)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func (p *appLaunchPlanner) finalizePlan(recipe launchplan.StableRecipe, admissions []launchplan.SharedAdmissionSpec, effects []launchplan.EffectSpec) (*launchplan.Plan, *launchplan.BuildFailure) {
	return p.finalizePlanWithSecrets(recipe, admissions, effects, launchplan.NewEphemeralSecretBundle())
}

// buildBootstrapEffect creates the canonical EffectBootstrapWrite from a
// resolved launch spec. Only shell-attach starts a shell without the CLI and
// therefore carries input; direct/shell-inline already execute the target and
// retain an explicit no-op effect to preserve canonical ordering.
func buildBootstrapEffect(spec platform.ResolvedLaunchSpec) launchplan.EffectSpec {
	startupCommand := ""
	if spec.BootstrapMode == platform.BootstrapShellAttach {
		startupCommand = spec.StartupCommand
	}
	return launchplan.EffectSpec{
		Kind: launchplan.EffectBootstrapWrite,
		Bootstrap: &launchplan.BootstrapWriteSpec{
			Payload:        launchplan.SecretBufferRef{Index: 0},
			StartupCommand: startupCommand,
		},
	}
}

func (p *appLaunchPlanner) finalizePlanWithSecrets(recipe launchplan.StableRecipe, admissions []launchplan.SharedAdmissionSpec, effects []launchplan.EffectSpec, secrets *launchplan.EphemeralSecretBundle) (*launchplan.Plan, *launchplan.BuildFailure) {
	plan := &launchplan.Plan{
		Recipe: recipe, Admissions: admissions, Effects: effects,
		Secrets: secrets, Dependency: launchplan.DependencyRevision{},
	}
	if err := plan.Validate(); err != nil {
		secrets.Dispose()
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureLaunchContext, CLIType: recipe.CLIType}
	}
	return plan, nil
}

// computeFilePreimage returns SHA-256 of the file content, or zero digest if
// the file does not exist.
func computeFilePreimage(path string) [32]byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return [32]byte{}
	}
	return sha256.Sum256(data)
}

// claudeEffectiveProxyPort returns the effective proxy port for Claude env
// computation, replicating the desktop LaunchSession logic.
// proxyDefaultPort is the canonical codebox injection proxy port (5280).
const proxyDefaultPort = 5280

func claudeEffectiveProxyPort(useProxy, useHeadroom bool) int {
	if useProxy {
		return proxyDefaultPort
	}
	if useHeadroom {
		return headroom.DefaultPort
	}
	return 0
}

// computeClaudeOverrides replicates launcher.LauncherService.BuildOverrides
// without mutating the LauncherService proxyPort. The effectiveProxyPort is
// pre-computed by the caller.
func computeClaudeOverrides(provider config.Provider, presetName, apiKey string, agentTeams config.AgentTeamsConfig, effectiveProxyPort int) map[string]string {
	preset, ok := provider.Presets[presetName]
	overrides := map[string]string{}
	if !provider.IsAnthropicCompatible() {
		overrides["ANTHROPIC_BASE_URL"] = ""
		overrides["ANTHROPIC_API_KEY"] = ""
		overrides["ANTHROPIC_AUTH_TOKEN"] = ""
		return overrides
	}
	if provider.IsOAuthMode() {
		overrides["ANTHROPIC_BASE_URL"] = ""
	} else if effectiveProxyPort > 0 {
		overrides["ANTHROPIC_BASE_URL"] = fmt.Sprintf("http://localhost:%d", effectiveProxyPort)
	} else {
		overrides["ANTHROPIC_BASE_URL"] = provider.EffectiveBaseURL("anthropic")
	}
	effectiveAuthKey := provider.EffectiveAuthKey("anthropic")
	switch effectiveAuthKey {
	case config.AuthTypeOAuth:
		overrides["ANTHROPIC_API_KEY"] = ""
		overrides["ANTHROPIC_AUTH_TOKEN"] = ""
	case config.AuthTypeAuthToken:
		overrides["ANTHROPIC_AUTH_TOKEN"] = apiKey
		overrides["ANTHROPIC_API_KEY"] = ""
	default:
		overrides["ANTHROPIC_API_KEY"] = apiKey
		overrides["ANTHROPIC_AUTH_TOKEN"] = ""
	}
	model := provider.DefaultModel
	if ok && strings.TrimSpace(preset.Model) != "" {
		model = preset.Model
	}
	overrides["ANTHROPIC_MODEL"] = model
	if ok {
		if trimmed := strings.TrimSpace(preset.ModelHaiku); trimmed != "" {
			overrides["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = trimmed
		}
		if trimmed := strings.TrimSpace(preset.ModelSonnet); trimmed != "" {
			overrides["ANTHROPIC_DEFAULT_SONNET_MODEL"] = trimmed
		}
		if trimmed := strings.TrimSpace(preset.ModelOpus); trimmed != "" {
			overrides["ANTHROPIC_DEFAULT_OPUS_MODEL"] = trimmed
		}
	}
	if ok {
		params := preset.Parameters
		if params.Temperature != 0 {
			overrides["ANTHROPIC_TEMPERATURE"] = fmt.Sprintf("%g", params.Temperature)
		}
		if params.TopP != 0 {
			overrides["ANTHROPIC_TOP_P"] = fmt.Sprintf("%g", params.TopP)
		}
		if params.MaxTokens != 0 {
			overrides["ANTHROPIC_MAX_TOKENS"] = fmt.Sprintf("%d", params.MaxTokens)
		}
		if params.MaxContextLength != 0 {
			overrides["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = fmt.Sprintf("%d", params.MaxContextLength)
		}
		if params.DoSample != nil {
			overrides["ANTHROPIC_DO_SAMPLE"] = fmt.Sprintf("%t", *params.DoSample)
		}
		if params.Stream != nil {
			overrides["ANTHROPIC_STREAM"] = fmt.Sprintf("%t", *params.Stream)
		}
		if params.Thinking != nil {
			if b, err := json.Marshal(params.Thinking); err == nil {
				overrides["ANTHROPIC_THINKING"] = string(b)
			}
		}
		if strings.TrimSpace(params.ReasoningEffort) != "" {
			overrides["CLAUDE_CODE_EFFORT_LEVEL"] = strings.TrimSpace(params.ReasoningEffort)
		}
	}
	if agentTeams.Enabled {
		overrides["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
		overrides["CLAUDE_TEAMMATE_MODE"] = agentTeams.TeammateMode
	}
	return overrides
}

// configFileName returns the config file name for a ConfigTarget.
func configFileName(target launchplan.ConfigTarget) string {
	switch target {
	case launchplan.ConfigPi:
		return "models.json"
	case launchplan.ConfigOmp:
		return "models.yml"
	default:
		return ""
	}
}

// piProviderMappingForType returns the provider mapping for pi or omp.
func piProviderMappingForType(terminalType string, p config.Provider) (string, string) {
	if terminalType == "omp" {
		return ompProviderMapping(p)
	}
	return piProviderMapping(p)
}

// customProviderID returns the amagi-generated provider ID for Pi or Omp.
func customProviderID(terminalType, providerID string) string {
	if terminalType == "omp" {
		return launcher.OmpProviderID(providerID)
	}
	return launcher.PiProviderID(providerID)
}

// Adapter type assertions to ensure concrete services satisfy the planner ports.
var (
	_ launchConfigPort  = (*config.ConfigService)(nil)
	_ launchSecretsPort = (*secrets.SecretsService)(nil)
	_ launchEnvVarsPort = (*envvars.EnvVarsService)(nil)
)
