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
	modelsByProvider := collectManagedProviderModels(providers, a.Config.GetAllTerminalPresets())
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
		models := modelsByProvider[name]
		// A harness custom provider without any model is unusable and some OMP
		// versions reject the whole models.yml for it. Keep the CodeBox provider
		// as the source of truth, but do not publish it until a model is set.
		if len(models) == 0 {
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

		id, entry, err := launcher.BuildOpenCodeManagedProviderEntry(name, provider, apiKey, models)
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
) map[string][]launcher.ManagedProviderModel {
	modelParams := make(map[string]map[string]config.Parameters, len(providers))
	for name, provider := range providers {
		modelParams[name] = map[string]config.Parameters{}
		if model := strings.TrimSpace(provider.DefaultModel); model != "" {
			modelParams[name][model] = config.Parameters{}
		}
		legacyNames := make([]string, 0, len(provider.Presets))
		for presetName := range provider.Presets {
			legacyNames = append(legacyNames, presetName)
		}
		sort.Strings(legacyNames)
		for _, presetName := range legacyNames {
			preset := provider.Presets[presetName]
			if model := strings.TrimSpace(preset.Model); model != "" {
				modelParams[name][model] = preset.Parameters
			}
		}
	}

	for _, terminalType := range config.ValidTerminalPresetTypes() {
		presetMap := presets.GetMap(terminalType)
		presetNames := make([]string, 0, len(presetMap))
		for presetName := range presetMap {
			presetNames = append(presetNames, presetName)
		}
		sort.Strings(presetNames)
		for _, presetName := range presetNames {
			preset := presetMap[presetName]
			providerName := strings.TrimSpace(preset.Provider)
			if _, exists := modelParams[providerName]; !exists {
				continue
			}
			for _, model := range []string{preset.Model, preset.ModelHaiku, preset.ModelSonnet, preset.ModelOpus} {
				if model = strings.TrimSpace(model); model != "" {
					modelParams[providerName][model] = preset.Parameters
				}
			}
		}
	}

	result := make(map[string][]launcher.ManagedProviderModel, len(modelParams))
	for providerName, byModel := range modelParams {
		modelNames := make([]string, 0, len(byModel))
		for model := range byModel {
			modelNames = append(modelNames, model)
		}
		sort.Strings(modelNames)
		models := make([]launcher.ManagedProviderModel, 0, len(modelNames))
		for _, model := range modelNames {
			models = append(models, launcher.ManagedProviderModel{ID: model, Parameters: byModel[model]})
		}
		result[providerName] = models
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
