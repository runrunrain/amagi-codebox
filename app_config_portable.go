package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/proxy"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/usage"
	"amagi-codebox/internal/workspace"
)

const completeConfigExportVersion = "2.0"

// portableConfigSnapshot is the device-independent portion of a complete
// export. Runtime history, logs, usage.db, paired devices, generated deployment
// manifests and installed plugin files are deliberately outside this contract.
type portableConfigSnapshot struct {
	Settings       *settings.AppSettings     `json:"settings"`
	Paths          *paths.PathsConfig        `json:"paths"`
	EnvVars        *envvars.PortableConfig   `json:"env_vars"`
	Secrets        *map[string]string        `json:"secrets"`
	Workspaces     *workspace.PortableConfig `json:"workspaces"`
	Proxy          *portableProxyConfig      `json:"proxy"`
	Pricing        *usage.PricingData        `json:"pricing"`
	OpenCodeConfig json.RawMessage           `json:"opencode_global_config"`
}

type portableProxyConfig struct {
	Rules             []proxy.InjectionRule `json:"rules"`
	BackendURLHistory []string              `json:"backend_url_history"`
}

func (p portableConfigSnapshot) validate() error {
	if p.Settings == nil || p.Paths == nil || p.EnvVars == nil || p.Secrets == nil ||
		p.Workspaces == nil || p.Proxy == nil || p.Pricing == nil {
		return errors.New("complete config snapshot is missing one or more required sections")
	}
	if len(p.OpenCodeConfig) == 0 || !json.Valid(p.OpenCodeConfig) {
		return errors.New("complete config snapshot has invalid OpenCode global config")
	}
	var openCodeObject map[string]any
	if err := json.Unmarshal(p.OpenCodeConfig, &openCodeObject); err != nil || openCodeObject == nil {
		return errors.New("OpenCode global config must be a JSON object")
	}
	for key := range *p.Secrets {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(key) != key {
			return fmt.Errorf("invalid secret key %q", key)
		}
	}
	return nil
}

func (a *App) buildCompleteExportConfig() (config.ExportConfig, error) {
	if a.Config == nil || a.Secrets == nil || a.Settings == nil || a.Paths == nil ||
		a.EnvVars == nil || a.Workspaces == nil || a.Proxy == nil || a.Usage == nil ||
		a.OpenCodeConfig == nil {
		return config.ExportConfig{}, errors.New("configuration services are not initialized")
	}

	providers := a.Config.GetProviders()
	exportProviders := make(map[string]config.ExportProvider, len(providers))
	for name, provider := range providers {
		apiKey, _ := a.getProviderAPIKey(name, provider)
		exportProviders[name] = buildExportProvider(provider, apiKey)
	}

	openCodeConfig, err := a.OpenCodeConfig.GetOpenCodeConfig()
	if err != nil {
		return config.ExportConfig{}, fmt.Errorf("read OpenCode global config: %w", err)
	}
	if !json.Valid([]byte(openCodeConfig)) {
		return config.ExportConfig{}, errors.New("OpenCode global config is invalid JSON; fix it before exporting")
	}

	secretsSnapshot, err := a.Secrets.Snapshot()
	if err != nil {
		return config.ExportConfig{}, fmt.Errorf("密钥尚未加载完成，无法处理完整配置，请稍后重试: %w", err)
	}
	portable := portableConfigSnapshot{
		Settings:       a.Settings.GetSettings(),
		Paths:          pointerTo(a.Paths.GetConfig()),
		EnvVars:        pointerTo(a.EnvVars.GetPortableConfig()),
		Secrets:        &secretsSnapshot,
		Workspaces:     pointerTo(a.Workspaces.GetPortableConfig()),
		Proxy:          &portableProxyConfig{Rules: a.Proxy.GetRules(), BackendURLHistory: a.Proxy.GetBackendURLHistory()},
		Pricing:        pointerTo(a.Usage.Pricing().Snapshot()),
		OpenCodeConfig: json.RawMessage(openCodeConfig),
	}
	portableJSON, err := json.Marshal(portable)
	if err != nil {
		return config.ExportConfig{}, fmt.Errorf("marshal portable config: %w", err)
	}

	return config.ExportConfig{
		Version:         completeConfigExportVersion,
		ExportedAt:      time.Now().Format(time.RFC3339),
		Source:          "amagi-codebox",
		Providers:       exportProviders,
		AgentTeams:      a.Config.GetAgentTeams(),
		TerminalPresets: a.Config.GetAllTerminalPresets(),
		OpenCodePresets: a.Config.GetAllOpenCodePresets(),
		Portable:        portableJSON,
	}, nil
}

func pointerTo[T any](value T) *T { return &value }

type configImportMetadata struct {
	OpenCodePresets *json.RawMessage `json:"opencode_presets"`
	Portable        *json.RawMessage `json:"portable"`
}

func decodeConfigExport(data []byte) (config.ExportConfig, configImportMetadata, error) {
	data = trimUTF8BOM(data)
	var exported config.ExportConfig
	if err := json.Unmarshal(data, &exported); err != nil {
		return config.ExportConfig{}, configImportMetadata{}, fmt.Errorf("parse JSON: %w", err)
	}
	var metadata configImportMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return config.ExportConfig{}, configImportMetadata{}, fmt.Errorf("parse import snapshot metadata: %w", err)
	}
	if exported.Version == "" || exported.Source != "amagi-codebox" {
		return config.ExportConfig{}, configImportMetadata{}, errors.New("invalid config file: unsupported or missing source/version")
	}
	if exported.Version != "1.0" && exported.Version != completeConfigExportVersion {
		return config.ExportConfig{}, configImportMetadata{}, fmt.Errorf("unsupported config export version %q", exported.Version)
	}
	if exported.Version == completeConfigExportVersion && metadata.Portable == nil {
		return config.ExportConfig{}, configImportMetadata{}, errors.New("invalid v2 config file: missing portable snapshot")
	}
	return exported, metadata, nil
}

func trimUTF8BOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

func decodePortableConfig(raw json.RawMessage) (portableConfigSnapshot, error) {
	var snapshot portableConfigSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return portableConfigSnapshot{}, fmt.Errorf("parse complete config snapshot: %w", err)
	}
	if err := snapshot.validate(); err != nil {
		return portableConfigSnapshot{}, err
	}
	return snapshot, nil
}

func importedProvidersAndSecrets(exported config.ExportConfig, baseSecrets map[string]string) (map[string]config.Provider, map[string]string, error) {
	providers := make(map[string]config.Provider, len(exported.Providers))
	secretsSnapshot := make(map[string]string, len(baseSecrets)+len(exported.Providers))
	for key, value := range baseSecrets {
		secretsSnapshot[key] = value
	}
	for rawName, exportedProvider := range exported.Providers {
		name := strings.TrimSpace(rawName)
		if name == "" || strings.Contains(name, "/") || name != rawName {
			return nil, nil, fmt.Errorf("invalid provider name %q", rawName)
		}
		providers[name] = buildProviderFromExportProvider(exportedProvider)
		if apiKey := selectImportedProviderAPIKey(exportedProvider); apiKey != "" {
			secretsSnapshot[name] = apiKey
			delete(secretsSnapshot, name+":anthropic")
			delete(secretsSnapshot, name+":openai")
		}
	}
	return providers, secretsSnapshot, nil
}

// applyCompleteConfig imports a v2 snapshot. Each service has its own atomic
// replacement; if a later section fails, the already-applied sections are
// restored from the captured pre-import snapshot on a best-effort basis.
func (a *App) applyCompleteConfig(exported config.ExportConfig, portable portableConfigSnapshot, hasExplicitOpenCodeSnapshot bool) (err error) {
	if a.Config == nil || a.Secrets == nil || a.Settings == nil || a.Paths == nil ||
		a.EnvVars == nil || a.Workspaces == nil || a.Proxy == nil || a.Usage == nil ||
		a.OpenCodeConfig == nil {
		return errors.New("configuration services are not initialized")
	}

	providers, importedSecrets, err := importedProvidersAndSecrets(exported, *portable.Secrets)
	if err != nil {
		return err
	}
	if err := validateCompleteImportServices(a, providers, exported, portable, hasExplicitOpenCodeSnapshot); err != nil {
		return fmt.Errorf("validate complete config: %w", err)
	}
	previousExport, captureErr := a.buildCompleteExportConfig()
	if captureErr != nil {
		return fmt.Errorf("capture rollback snapshot: %w", captureErr)
	}
	previousPortable, err := decodePortableConfig(previousExport.Portable)
	if err != nil {
		return fmt.Errorf("decode rollback snapshot: %w", err)
	}

	rollbackNeeded := true
	defer func() {
		if err == nil || !rollbackNeeded {
			return
		}
		rollbackErr := a.restoreCompleteConfig(previousExport, previousPortable)
		if rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback imported configuration: %w", rollbackErr))
		}
	}()

	if err = a.Config.ReplaceProviders(providers); err != nil {
		return fmt.Errorf("replace providers: %w", err)
	}
	if err = a.Config.SetAgentTeams(exported.AgentTeams); err != nil {
		return fmt.Errorf("replace Agent Teams config: %w", err)
	}
	if err = a.Config.ReplaceImportedPresetSnapshots(exported.TerminalPresets, exported.OpenCodePresets, hasExplicitOpenCodeSnapshot); err != nil {
		return fmt.Errorf("replace preset snapshots: %w", err)
	}
	if err = a.Secrets.ReplaceAll(importedSecrets); err != nil {
		return fmt.Errorf("replace secrets: %w", err)
	}

	if err = a.Settings.ReplaceSettings(*portable.Settings); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	if err = a.Paths.ReplaceConfig(*portable.Paths); err != nil {
		return fmt.Errorf("replace paths: %w", err)
	}
	if err = a.EnvVars.ReplacePortableConfig(*portable.EnvVars); err != nil {
		return fmt.Errorf("replace environment variables: %w", err)
	}
	if err = a.Workspaces.ReplacePortableConfig(*portable.Workspaces); err != nil {
		return fmt.Errorf("replace workspaces: %w", err)
	}
	if err = a.Proxy.ReplacePortableConfig(portable.Proxy.Rules, portable.Proxy.BackendURLHistory); err != nil {
		return fmt.Errorf("replace proxy config: %w", err)
	}
	if err = a.Usage.Pricing().ReplaceSnapshot(*portable.Pricing); err != nil {
		return fmt.Errorf("replace pricing: %w", err)
	}
	if err = a.OpenCodeConfig.SaveOpenCodeConfig(string(portable.OpenCodeConfig)); err != nil {
		return fmt.Errorf("replace OpenCode global config: %w", err)
	}

	rollbackNeeded = false
	if a.Remote != nil && a.Remote.IsRunning() {
		a.Remote.Stop()
	}
	return nil
}

// validateCompleteImportServices exercises every service validator against an
// isolated temporary configuration tree before the live replacement starts.
// This catches malformed late sections up front and avoids relying on rollback
// for ordinary validation failures.
func validateCompleteImportServices(a *App, providers map[string]config.Provider, exported config.ExportConfig, portable portableConfigSnapshot, hasExplicitOpenCodeSnapshot bool) error {
	validationDir, err := os.MkdirTemp("", "amagi-codebox-import-validation-")
	if err != nil {
		return fmt.Errorf("create validation directory: %w", err)
	}
	defer os.RemoveAll(validationDir)

	configService := config.NewConfigService(validationDir)
	if err := configService.Load(); err != nil {
		return err
	}
	if err := configService.ReplaceProviders(providers); err != nil {
		return err
	}
	if err := configService.SetAgentTeams(exported.AgentTeams); err != nil {
		return err
	}
	if exported.TerminalPresets != nil {
		for _, terminalType := range config.ValidTerminalPresetTypes() {
			for key, preset := range exported.TerminalPresets.GetMap(terminalType) {
				if err := configService.SaveTerminalPreset(string(terminalType), key, preset); err != nil {
					return fmt.Errorf("terminal preset %s/%s: %w", terminalType, key, err)
				}
			}
		}
	}
	for key, preset := range exported.OpenCodePresets {
		if err := configService.SaveOpenCodePreset(key, preset); err != nil {
			return fmt.Errorf("OpenCode preset %s: %w", key, err)
		}
	}
	if err := configService.ReplaceImportedPresetSnapshots(exported.TerminalPresets, exported.OpenCodePresets, hasExplicitOpenCodeSnapshot); err != nil {
		return err
	}

	settingsService := settings.NewService(validationDir)
	if err := settingsService.Load(); err != nil {
		return err
	}
	if err := settingsService.ReplaceSettings(*portable.Settings); err != nil {
		return err
	}
	pathsService := paths.NewPathsService(validationDir)
	if err := pathsService.Load(); err != nil {
		return err
	}
	if err := pathsService.ReplaceConfig(*portable.Paths); err != nil {
		return err
	}
	workspaceService := workspace.NewService(validationDir, a.Plugins, a.Log)
	if err := workspaceService.Load(); err != nil {
		return err
	}
	if err := workspaceService.ReplacePortableConfig(*portable.Workspaces); err != nil {
		return err
	}
	proxyService := proxy.NewProxyService()
	if err := proxyService.LoadRules(validationDir); err != nil {
		return err
	}
	if err := proxyService.ReplacePortableConfig(portable.Proxy.Rules, portable.Proxy.BackendURLHistory); err != nil {
		return err
	}
	pricingService := usage.NewPricingService(validationDir)
	if err := pricingService.ReplaceSnapshot(*portable.Pricing); err != nil {
		return err
	}
	if err := validatePortableEnvConfig(*portable.EnvVars); err != nil {
		return err
	}
	return nil
}

func validatePortableEnvConfig(next envvars.PortableConfig) error {
	validationDir, err := os.MkdirTemp("", "amagi-codebox-env-validation-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(validationDir)
	service := envvars.NewEnvVarsService(validationDir)
	if err := service.Load(); err != nil {
		return err
	}
	// Validation must never mutate the destination OS environment. Disabling the
	// sync flag still exercises key validation and persisted snapshot shape.
	next.GlobalSyncEnabled = false
	return service.ReplacePortableConfig(next)
}

func (a *App) restoreCompleteConfig(exported config.ExportConfig, portable portableConfigSnapshot) error {
	providers, _, err := importedProvidersAndSecrets(exported, map[string]string{})
	if err != nil {
		return err
	}
	var restoreErr error
	restoreErr = errors.Join(restoreErr, a.OpenCodeConfig.SaveOpenCodeConfig(string(portable.OpenCodeConfig)))
	restoreErr = errors.Join(restoreErr, a.Usage.Pricing().ReplaceSnapshot(*portable.Pricing))
	restoreErr = errors.Join(restoreErr, a.Proxy.ReplacePortableConfig(portable.Proxy.Rules, portable.Proxy.BackendURLHistory))
	restoreErr = errors.Join(restoreErr, a.Workspaces.ReplacePortableConfig(*portable.Workspaces))
	restoreErr = errors.Join(restoreErr, a.EnvVars.ReplacePortableConfig(*portable.EnvVars))
	restoreErr = errors.Join(restoreErr, a.Paths.ReplaceConfig(*portable.Paths))
	restoreErr = errors.Join(restoreErr, a.Settings.ReplaceSettings(*portable.Settings))
	// Use the exact stored-secret snapshot when rolling back. Provider export
	// fields may contain environment fallbacks; persisting those would turn an
	// originally environment-only credential into a stored credential.
	restoreErr = errors.Join(restoreErr, a.Secrets.ReplaceAll(*portable.Secrets))
	restoreErr = errors.Join(restoreErr, a.Config.ReplaceProviders(providers))
	restoreErr = errors.Join(restoreErr, a.Config.SetAgentTeams(exported.AgentTeams))
	restoreErr = errors.Join(restoreErr, a.Config.ReplaceImportedPresetSnapshots(exported.TerminalPresets, exported.OpenCodePresets, true))
	return restoreErr
}
