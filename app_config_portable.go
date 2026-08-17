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
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/usage"

	"gopkg.in/yaml.v3"
)

const completeConfigExportVersion = "2.0"

// portableConfigSnapshot is the device-independent portion of a complete
// export. Runtime history, logs, usage.db, paired devices, generated deployment
// manifests and installed plugin files are deliberately outside this contract.
type portableConfigSnapshot struct {
	Settings       *settings.AppSettings   `json:"settings"`
	Paths          *paths.PathsConfig      `json:"paths"`
	EnvVars        *envvars.PortableConfig `json:"env_vars"`
	Secrets        *map[string]string      `json:"secrets"`
	Pricing        *usage.PricingData      `json:"pricing"`
	OpenCodeConfig json.RawMessage         `json:"opencode_global_config"`

	// CLI 独立配置全文快照（可选 section）。仅当源设备上对应文件存在且
	// 内容合法时导出；导入按存在性整体替换目标文件，旧 v2 导出文件缺失
	// 这些字段时行为完全不变。内容含明文凭据（pi auth.json token、
	// models.json/models.yml 内联 apiKey 等），与顶层 provider api_key
	// 明文导出语义一致。
	PiModelsConfig  json.RawMessage `json:"pi_models_config,omitempty"`  // ~/.pi/agent/models.json
	PiAuthConfig    json.RawMessage `json:"pi_auth_config,omitempty"`    // ~/.pi/agent/auth.json
	PiAmagiConfig   json.RawMessage `json:"pi_amagi_config,omitempty"`   // ~/.pi/agent/amagi.json
	OmpConfig       string          `json:"omp_config,omitempty"`        // ~/.omp/agent/config.yml
	OmpModelsConfig string          `json:"omp_models_config,omitempty"` // ~/.omp/agent/models.yml
}

func (p portableConfigSnapshot) validate() error {
	if p.Settings == nil || p.Paths == nil || p.EnvVars == nil || p.Secrets == nil ||
		p.Pricing == nil {
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
		a.EnvVars == nil || a.Usage == nil ||
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
		Pricing:        pointerTo(a.Usage.Pricing().Snapshot()),
		OpenCodeConfig: json.RawMessage(openCodeConfig),
	}
	a.appendCLIConfigSections(&portable)
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

// validateJSONObjectConfig 校验 pi 配置内容：合法 JSON 且根为对象，与
// piconfig Save* 方法的写入校验保持一致。
func validateJSONObjectConfig(content string) error {
	if !json.Valid([]byte(content)) {
		return errors.New("content is not valid JSON")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err != nil || obj == nil {
		return errors.New("root must be a JSON object")
	}
	return nil
}

// validateYAMLMappingConfig 校验 omp 配置内容：合法 YAML 且根为映射，与
// ompconfig Save* 方法的写入校验保持一致。
func validateYAMLMappingConfig(content string) error {
	var root map[string]any
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	if root == nil {
		return errors.New("root must be a YAML mapping")
	}
	return nil
}

// appendCLIConfigSections 将 CLI 独立配置（pi 的 models.json/auth.json/
// amagi.json 与 omp 的 config.yml/models.yml）全文读入 portable 快照。
// 这些 section 采用尽力而为语义：对应文件不存在时静默跳过（不导出占位
// 骨架，避免把“源设备未使用该 CLI”误表达为“空注册表”），读取失败或
// 内容非法时记 Warn 跳过该字段，单个 CLI 配置损坏不阻断完整导出
// （区别于 OpenCode 全局配置的硬性要求）。
func (a *App) appendCLIConfigSections(portable *portableConfigSnapshot) {
	if a.PiConfig != nil {
		portable.PiModelsConfig = readCLIConfigSection(a, "pi_models_config",
			a.PiConfig.GetModelsConfigPath, a.PiConfig.GetModelsConfig, validateJSONObjectConfig)
		portable.PiAuthConfig = readCLIConfigSection(a, "pi_auth_config",
			a.PiConfig.GetAuthConfigPath, a.PiConfig.GetAuthConfig, validateJSONObjectConfig)
		portable.PiAmagiConfig = readCLIConfigSection(a, "pi_amagi_config",
			a.PiConfig.GetAmagiConfigPath, a.PiConfig.GetAmagiConfig, validateJSONObjectConfig)
	}
	if a.OmpConfig != nil {
		portable.OmpConfig = string(readCLIConfigSection(a, "omp_config",
			a.OmpConfig.GetOmpConfigPath, a.OmpConfig.GetOmpConfig, validateYAMLMappingConfig))
		portable.OmpModelsConfig = string(readCLIConfigSection(a, "omp_models_config",
			a.OmpConfig.GetModelsConfigPath, a.OmpConfig.GetModelsConfig, validateYAMLMappingConfig))
	}
}

// readCLIConfigSection 读取单个 CLI 配置 section：文件不存在时返回 nil
// （静默跳过）；读取失败或校验失败时记 Warn 并返回 nil。
func readCLIConfigSection(a *App, field string, pathFn func() (string, error), readFn func() (string, error), validate func(string) error) json.RawMessage {
	path, err := pathFn()
	if err != nil {
		a.warnCLIConfigSkip(field, fmt.Sprintf("resolve path: %v", err))
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			a.warnCLIConfigSkip(field, fmt.Sprintf("stat %s: %v", path, err))
		}
		return nil
	}
	content, err := readFn()
	if err != nil {
		a.warnCLIConfigSkip(field, err.Error())
		return nil
	}
	if err := validate(content); err != nil {
		a.warnCLIConfigSkip(field, err.Error())
		return nil
	}
	return json.RawMessage(content)
}

func (a *App) warnCLIConfigSkip(field, reason string) {
	if a.Log != nil {
		a.Log.Warn("app", "导出完整配置：跳过 CLI 独立配置字段", fmt.Sprintf("field=%s reason=%s", field, reason))
	}
}

// applyCLIConfigSections 按存在性恢复快照中的 CLI 独立配置（v2 完整
// 快照替换语义：整体替换目标文件）。字段缺失时跳过写入，因此旧 v2
// 导出文件导入行为不变。写失败按 errors.Join 聚合返回；调用方
// （applyCompleteConfig / restoreCompleteConfig）据此触发共享回滚。
func (a *App) applyCLIConfigSections(portable portableConfigSnapshot) error {
	var errs error
	if len(portable.PiModelsConfig) > 0 {
		if a.PiConfig == nil {
			errs = errors.Join(errs, errors.New("replace pi models config: pi config service is not initialized"))
		} else if err := a.PiConfig.SaveModelsConfig(string(portable.PiModelsConfig)); err != nil {
			errs = errors.Join(errs, fmt.Errorf("replace pi models config: %w", err))
		}
	}
	if len(portable.PiAuthConfig) > 0 {
		if a.PiConfig == nil {
			errs = errors.Join(errs, errors.New("replace pi auth config: pi config service is not initialized"))
		} else if err := a.PiConfig.SaveAuthConfig(string(portable.PiAuthConfig)); err != nil {
			errs = errors.Join(errs, fmt.Errorf("replace pi auth config: %w", err))
		}
	}
	if len(portable.PiAmagiConfig) > 0 {
		if a.PiConfig == nil {
			errs = errors.Join(errs, errors.New("replace pi amagi config: pi config service is not initialized"))
		} else if err := a.PiConfig.SaveAmagiConfig(string(portable.PiAmagiConfig)); err != nil {
			errs = errors.Join(errs, fmt.Errorf("replace pi amagi config: %w", err))
		}
	}
	if portable.OmpConfig != "" {
		if a.OmpConfig == nil {
			errs = errors.Join(errs, errors.New("replace omp config: omp config service is not initialized"))
		} else if err := a.OmpConfig.SaveOmpConfig(portable.OmpConfig); err != nil {
			errs = errors.Join(errs, fmt.Errorf("replace omp config: %w", err))
		}
	}
	if portable.OmpModelsConfig != "" {
		if a.OmpConfig == nil {
			errs = errors.Join(errs, errors.New("replace omp models config: omp config service is not initialized"))
		} else if err := a.OmpConfig.SaveModelsConfig(portable.OmpModelsConfig); err != nil {
			errs = errors.Join(errs, fmt.Errorf("replace omp models config: %w", err))
		}
	}
	return errs
}

// validateCLIConfigSections 在导入写入前校验快照中的 CLI 独立配置内容，
// 与 validateCompleteImportServices 的整体思路一致：畸形内容在触碰任何
// 实际状态前失败，不依赖回滚。内容校验镜像 Save* 写入校验；多个畸形
// section 的错误用 errors.Join 聚合，一次性全部报出。
func validateCLIConfigSections(portable portableConfigSnapshot) error {
	sections := []struct {
		name     string
		present  bool
		content  string
		validate func(string) error
	}{
		{"pi models config", len(portable.PiModelsConfig) > 0, string(portable.PiModelsConfig), validateJSONObjectConfig},
		{"pi auth config", len(portable.PiAuthConfig) > 0, string(portable.PiAuthConfig), validateJSONObjectConfig},
		{"pi amagi config", len(portable.PiAmagiConfig) > 0, string(portable.PiAmagiConfig), validateJSONObjectConfig},
		{"omp config", portable.OmpConfig != "", portable.OmpConfig, validateYAMLMappingConfig},
		{"omp models config", portable.OmpModelsConfig != "", portable.OmpModelsConfig, validateYAMLMappingConfig},
	}
	var errs error
	for _, section := range sections {
		if !section.present {
			continue
		}
		if err := section.validate(section.content); err != nil {
			errs = errors.Join(errs, fmt.Errorf("%s: %w", section.name, err))
		}
	}
	return errs
}

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
		a.EnvVars == nil || a.Usage == nil ||
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
	if err = a.Usage.Pricing().ReplaceSnapshot(*portable.Pricing); err != nil {
		return fmt.Errorf("replace pricing: %w", err)
	}
	if err = a.SaveOpenCodeConfig(string(portable.OpenCodeConfig)); err != nil {
		return fmt.Errorf("replace OpenCode global config: %w", err)
	}
	if err = a.applyCLIConfigSections(portable); err != nil {
		return fmt.Errorf("replace CLI standalone configs: %w", err)
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
		for _, terminalType := range config.CompatibleTerminalPresetTypes() {
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
	pricingService := usage.NewPricingService(validationDir)
	if err := pricingService.ReplaceSnapshot(*portable.Pricing); err != nil {
		return err
	}
	if err := validatePortableEnvConfig(*portable.EnvVars); err != nil {
		return err
	}
	return validateCLIConfigSections(portable)
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
	restoreErr = errors.Join(restoreErr, a.applyCLIConfigSections(portable))
	restoreErr = errors.Join(restoreErr, a.syncProvidersToHarnesses())
	return restoreErr
}
