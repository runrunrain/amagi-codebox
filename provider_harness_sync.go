package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launcher"
)

// syncProvidersToHarnesses reconciles the CodeBox-owned provider namespace in
// all supported harness configuration files. The primary CodeBox config is the
// source of truth; existing non-amagi providers remain owned by each harness.
func (a *App) syncProvidersToHarnesses() error {
	if a == nil || !a.providerSyncEnabled {
		return nil
	}
	a.providerSyncMu.Lock()
	defer a.providerSyncMu.Unlock()
	return a.syncProvidersToHarnessesLocked()
}

func (a *App) syncProvidersToHarnessesLocked() error {
	if a.Config == nil || a.Secrets == nil || a.OpenCodeConfig == nil {
		return errors.New("provider synchronization services are not initialized")
	}

	providers := a.Config.GetProviders()
	presets := a.Config.GetAllTerminalPresets()
	// Pi/OMP consume the shared OpenAI preset bucket only (the Anthropic bucket
	// belongs to Claude Code); OpenCode keeps consuming both public buckets as
	// before, so its opencode.json provider entries are unchanged.
	piOmpModelsByProvider := collectManagedProviderModels(providers, presets, a.Config.GetModalityProbeCache(), []config.TerminalPresetType{config.TerminalPresetOpenAI})
	openCodeModelsByProvider := collectManagedProviderModels(providers, presets, a.Config.GetModalityProbeCache(), config.ValidTerminalPresetTypes())
	piProviders := map[string]any{}
	ompProviders := map[string]any{}
	openCodeProviders := map[string]any{}
	var syncErrs []error

	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		provider := providers[name]
		apiKey, _ := a.getProviderAPIKey(name, provider)
		models := piOmpModelsByProvider[name]
		openCodeModels := openCodeModelsByProvider[name]
		// A harness custom provider without any model is unusable and some OMP
		// versions reject the whole models.yml for it. Keep the CodeBox provider
		// as the source of truth, but do not publish it until a model is set.
		if len(models) == 0 && len(openCodeModels) == 0 {
			continue
		}
		// A CodeBox entry with no explicit endpoint may intentionally rely on a
		// harness built-in/login provider of the same human name. Do not turn it
		// into a broken amagi-* custom provider and do not let it block syncing
		// the fully configured providers.
		if strings.TrimSpace(provider.EffectiveBaseURL(provider.PreferredFormat())) == "" {
			continue
		}

		piCfg, err := launcher.BuildPiManagedProviderConfig(name, provider, apiKey, models)
		if err != nil {
			syncErrs = append(syncErrs, fmt.Errorf("build Pi provider %q: %w", name, err))
		} else {
			appendHarnessProviderEntries(piProviders, piCfg)
		}

		ompCfg, err := launcher.BuildOmpManagedProviderConfig(name, provider, apiKey, models)
		if err != nil {
			syncErrs = append(syncErrs, fmt.Errorf("build OMP provider %q: %w", name, err))
		} else {
			appendHarnessProviderEntries(ompProviders, ompCfg)
		}

		id, entry, err := launcher.BuildOpenCodeManagedProviderEntry(name, provider, apiKey, openCodeModels)
		if err != nil {
			syncErrs = append(syncErrs, fmt.Errorf("build OpenCode provider %q: %w", name, err))
		} else {
			openCodeProviders[id] = entry
		}
	}

	piDir := a.providerSyncPiDir
	if strings.TrimSpace(piDir) == "" {
		piDir = defaultPiAgentDir()
	}
	ompDir := a.providerSyncOmpDir
	if strings.TrimSpace(ompDir) == "" {
		ompDir = defaultOmpAgentDir()
	}
	if err := launcher.ReconcilePiAgentConfig(map[string]any{"providers": piProviders}, piDir); err != nil {
		syncErrs = append(syncErrs, fmt.Errorf("sync Pi providers: %w", err))
	}
	if err := launcher.ReconcileOmpAgentConfig(map[string]any{"providers": ompProviders}, ompDir); err != nil {
		syncErrs = append(syncErrs, fmt.Errorf("sync OMP providers: %w", err))
	}
	if err := a.OpenCodeConfig.SyncManagedProviders(openCodeProviders); err != nil {
		syncErrs = append(syncErrs, fmt.Errorf("sync OpenCode providers: %w", err))
	}

	return errors.Join(syncErrs...)
}

func appendHarnessProviderEntries(destination map[string]any, cfg map[string]any) {
	raw, ok := cfg["providers"]
	if !ok {
		return
	}
	switch entries := raw.(type) {
	case map[string]any:
		for id, entry := range entries {
			destination[id] = entry
		}
	case map[string]map[string]any:
		for id, entry := range entries {
			destination[id] = entry
		}
	}
}

func collectManagedProviderModels(
	providers map[string]config.Provider,
	presets *config.TerminalPresetsConfig,
	probeCache config.ModalityProbeSnapshot,
	terminalTypes []config.TerminalPresetType,
) map[string][]launcher.ManagedProviderModel {
	result := make(map[string][]launcher.ManagedProviderModel, len(providers))
	for name, provider := range providers {
		if models := launcher.ManagedPresetModels(name, provider, presets, probeCache, terminalTypes...); len(models) > 0 {
			result[name] = models
		}
	}
	return result
}

func (a *App) providerSyncError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if a.Log != nil {
		a.Log.Warn("provider-sync", operation+"后同步 harness 配置失败", err.Error())
	}
	return fmt.Errorf("%s已保存，但同步 OpenCode/Pi/OMP 提供商配置失败: %w", operation, err)
}
