package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ConfigService 管理 models.json 配置文件。
// 方法为 public，以便后续通过 Wails 暴露给前端。
type ConfigService struct {
	configPath string
	config     *AppConfig
	mu         sync.RWMutex
}

func NewConfigService(configDir string) *ConfigService {
	return &ConfigService{
		configPath: filepath.Join(configDir, "models.json"),
		config:     nil,
	}
}

func (s *ConfigService) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.config = DefaultConfig()
			return nil
		}
		return fmt.Errorf("read config: %w", err)
	}

	var cfg AppConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("parse config json: %w", err)
	}
	if cfg.Models == nil {
		cfg.Models = map[string]Provider{}
	}

	// 合并默认提供商（保留用户配置，添加缺失的默认提供商）
	defaultCfg := DefaultConfig()
	for name, provider := range defaultCfg.Models {
		if _, exists := cfg.Models[name]; !exists {
			cfg.Models[name] = provider
		}
	}

	// 迁移：将旧的 anthropic API Key 配置升级为 OAuth
	if p, ok := cfg.Models["anthropic"]; ok && p.AuthKey != AuthTypeOAuth {
		cfg.Models["anthropic"] = defaultCfg.Models["anthropic"]
	}

	if migrateProviderTypes(cfg.Models) {
		if err := s.saveLockedConfig(&cfg); err != nil {
			return fmt.Errorf("migrate provider types: %w", err)
		}
	}

	s.config = &cfg
	return nil
}

func (s *ConfigService) Save() error {
	s.mu.RLock()
	cfg := s.config
	path := s.configPath
	s.mu.RUnlock()

	if cfg == nil {
		return errors.New("config not loaded")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	b = append(b, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// saveLocked 在已持有写锁的情况下将当前 config 写入磁盘。
// 调用方必须持有 s.mu 写锁。
func (s *ConfigService) saveLocked() error {
	return s.saveLockedConfig(s.config)
}

func (s *ConfigService) saveLockedConfig(cfg *AppConfig) error {
	if cfg == nil {
		return errors.New("config not loaded")
	}
	if cfg.Models == nil {
		cfg.Models = map[string]Provider{}
	}
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	b = append(b, '\n')
	tmp := s.configPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, s.configPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func migrateProviderTypes(models map[string]Provider) bool {
	updated := false
	for name, provider := range models {
		normalized := normalizeProviderType(provider)
		if normalized.Type != provider.Type {
			models[name] = normalized
			updated = true
		}
	}
	return updated
}

func normalizeProviderType(provider Provider) Provider {
	if provider.Type != "" {
		return provider
	}
	if provider.AuthKey == "OPENAI_API_KEY" {
		provider.Type = "openai"
	} else {
		provider.Type = "anthropic"
	}
	return provider
}

func (s *ConfigService) GetConfig() *AppConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil
	}
	copy := *s.config
	// 浅拷贝 models map，避免外部直接改内部 map。
	if s.config.Models != nil {
		copy.Models = make(map[string]Provider, len(s.config.Models))
		for k, v := range s.config.Models {
			copy.Models[k] = v
		}
	}
	return &copy
}

func (s *ConfigService) GetProviders() map[string]Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil || s.config.Models == nil {
		return map[string]Provider{}
	}
	out := make(map[string]Provider, len(s.config.Models))
	for k, v := range s.config.Models {
		out[k] = v
	}
	return out
}

// GetProviderNames 返回所有已配置的提供商名称（排序后）
func (s *ConfigService) GetProviderNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil || s.config.Models == nil {
		return []string{}
	}
	names := make([]string, 0, len(s.config.Models))
	for k := range s.config.Models {
		names = append(names, k)
	}
	return names
}

func (s *ConfigService) GetProvider(name string) (*Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, errors.New("config not loaded")
	}
	p, ok := s.config.Models[name]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", name)
	}
	copy := p
	return &copy, nil
}

func (s *ConfigService) SaveProvider(name string, p Provider) error {
	if name == "" {
		return errors.New("provider name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.Models == nil {
		s.config.Models = map[string]Provider{}
	}
	if p.Presets == nil {
		p.Presets = map[string]Preset{}
	}
	p = normalizeProviderType(p)
	s.config.Models[name] = p
	return s.saveLocked()
}

func (s *ConfigService) DeleteProvider(name string) error {
	if name == "" {
		return errors.New("provider name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.Models == nil {
		return nil
	}
	delete(s.config.Models, name)
	return s.saveLocked()
}

func (s *ConfigService) GetPresets(providerName string) (map[string]Preset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, errors.New("config not loaded")
	}
	p, ok := s.config.Models[providerName]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", providerName)
	}
	if p.Presets == nil {
		return map[string]Preset{}, nil
	}
	out := make(map[string]Preset, len(p.Presets))
	for k, v := range p.Presets {
		out[k] = v
	}
	return out, nil
}

func (s *ConfigService) SavePreset(providerName, presetName string, p Preset) error {
	if providerName == "" {
		return errors.New("provider name is required")
	}
	if presetName == "" {
		return errors.New("preset name is required")
	}

	// 规范化 opencode_config：防止前端传回时双重编码（字符串嵌套 JSON）
	p.NormalizeOpenCodeConfig()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	prov, ok := s.config.Models[providerName]
	if !ok {
		return fmt.Errorf("provider not found: %s", providerName)
	}
	if prov.Presets == nil {
		prov.Presets = map[string]Preset{}
	}
	prov.Presets[presetName] = p
	s.config.Models[providerName] = prov
	return s.saveLocked()
}

func (s *ConfigService) DeletePreset(providerName, presetName string) error {
	if providerName == "" {
		return errors.New("provider name is required")
	}
	if presetName == "" {
		return errors.New("preset name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	prov, ok := s.config.Models[providerName]
	if !ok {
		return fmt.Errorf("provider not found: %s", providerName)
	}
	if prov.Presets == nil {
		return nil
	}
	delete(prov.Presets, presetName)
	s.config.Models[providerName] = prov
	return s.saveLocked()
}

// GetTerminalPresets 获取指定终端类型的所有预设。
// 返回预设 map 的副本。
func (s *ConfigService) GetTerminalPresets(terminalType string) (map[string]TerminalPreset, error) {
	if !IsValidTerminalPresetType(terminalType) {
		return nil, fmt.Errorf("invalid terminal preset type: %s (valid: claude_code, opencode, codex)", terminalType)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, errors.New("config not loaded")
	}
	if s.config.TerminalPresets == nil {
		return map[string]TerminalPreset{}, nil
	}
	m := s.config.TerminalPresets.GetMap(TerminalPresetType(terminalType))
	if m == nil {
		return map[string]TerminalPreset{}, nil
	}
	out := make(map[string]TerminalPreset, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out, nil
}

// SaveTerminalPreset 保存指定终端类型的预设。
func (s *ConfigService) SaveTerminalPreset(terminalType, presetName string, preset TerminalPreset) error {
	if !IsValidTerminalPresetType(terminalType) {
		return fmt.Errorf("invalid terminal preset type: %s", terminalType)
	}
	if presetName == "" {
		return errors.New("preset name is required")
	}
	preset.NormalizeOpenCodeCfg()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.TerminalPresets == nil {
		s.config.TerminalPresets = &TerminalPresetsConfig{}
	}
	m := s.config.TerminalPresets.GetMap(TerminalPresetType(terminalType))
	if m == nil {
		m = map[string]TerminalPreset{}
	}
	m[presetName] = preset
	s.config.TerminalPresets.SetMap(TerminalPresetType(terminalType), m)
	return s.saveLocked()
}

// DeleteTerminalPreset 删除指定终端类型的预设。
func (s *ConfigService) DeleteTerminalPreset(terminalType, presetName string) error {
	if !IsValidTerminalPresetType(terminalType) {
		return fmt.Errorf("invalid terminal preset type: %s", terminalType)
	}
	if presetName == "" {
		return errors.New("preset name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.TerminalPresets == nil {
		return nil
	}
	m := s.config.TerminalPresets.GetMap(TerminalPresetType(terminalType))
	if m == nil {
		return nil
	}
	delete(m, presetName)
	s.config.TerminalPresets.SetMap(TerminalPresetType(terminalType), m)
	return s.saveLocked()
}

// MigrateProviderPresetsToTerminal 将旧的 provider.presets 迁移到 terminal_presets。
// 迁移规则：
//   - target=codex 或无 target 的 anthropic provider presets -> claude_code 终端预设
//   - target=opencode 的 presets -> opencode 终端预设
//   - target=codex 或无 target 的 openai provider presets -> codex 终端预设
//
// 已存在的同名 terminal preset 不会被覆盖。
// 返回 (迁移数量, 是否有变更, error)。
func (s *ConfigService) MigrateProviderPresetsToTerminal() (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return 0, false, errors.New("config not loaded")
	}
	if s.config.TerminalPresets == nil {
		s.config.TerminalPresets = &TerminalPresetsConfig{}
	}

	migrated := 0
	changed := false

	for provName, prov := range s.config.Models {
		for presetName, preset := range prov.Presets {
			target := preset.GetTarget()
			isOpenAI := prov.Type == "openai" || prov.AuthKey == "OPENAI_API_KEY"

			var termType TerminalPresetType
			switch {
			case target == PresetTargetOpenCode:
				termType = TerminalPresetOpenCode
			case isOpenAI:
				termType = TerminalPresetCodex
			default:
				// anthropic + no target or target=codex -> claude_code
				termType = TerminalPresetClaudeCode
			}

			tpMap := s.config.TerminalPresets.GetMap(termType)
			if tpMap == nil {
				tpMap = map[string]TerminalPreset{}
			}

			// 使用 provider/presetName 作为稳定 key，避免不同 provider 同名 preset 碰撞
			stableKey := provName + "/" + presetName

			// 不覆盖已存在的
			if _, exists := tpMap[stableKey]; exists {
				continue
			}

			// 规范化 legacy OpenCodeConfig：旧数据可能因前端双重编码而存储为 JSON 字符串，
			// 迁移时解包为原始 JSON 对象，与 TerminalPreset.NormalizeOpenCodeCfg 行为一致。
			preset.NormalizeOpenCodeConfig()

			tpMap[stableKey] = TerminalPreset{
				Name:        preset.Name,
				Provider:    provName,
				Model:       preset.Model,
				Parameters:  preset.Parameters,
				OpenCodeCfg: preset.OpenCodeConfig,
			}
			s.config.TerminalPresets.SetMap(termType, tpMap)
			migrated++
			changed = true
		}
	}

	if changed {
		if err := s.saveLocked(); err != nil {
			return migrated, changed, fmt.Errorf("save after migration: %w", err)
		}
	}
	return migrated, changed, nil
}

// GetAllTerminalPresets 返回完整的终端预设配置（用于导出）。
func (s *ConfigService) GetAllTerminalPresets() *TerminalPresetsConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil || s.config.TerminalPresets == nil {
		return nil
	}
	// Deep copy
	cp := &TerminalPresetsConfig{}
	if s.config.TerminalPresets.ClaudeCode != nil {
		cp.ClaudeCode = make(map[string]TerminalPreset, len(s.config.TerminalPresets.ClaudeCode))
		for k, v := range s.config.TerminalPresets.ClaudeCode {
			cp.ClaudeCode[k] = v
		}
	}
	if s.config.TerminalPresets.OpenCode != nil {
		cp.OpenCode = make(map[string]TerminalPreset, len(s.config.TerminalPresets.OpenCode))
		for k, v := range s.config.TerminalPresets.OpenCode {
			cp.OpenCode[k] = v
		}
	}
	if s.config.TerminalPresets.Codex != nil {
		cp.Codex = make(map[string]TerminalPreset, len(s.config.TerminalPresets.Codex))
		for k, v := range s.config.TerminalPresets.Codex {
			cp.Codex[k] = v
		}
	}
	return cp
}

// SetAllTerminalPresets 批量设置终端预设配置（用于导入）。
// 采用 merge 策略：不删除已有的 key，仅覆盖同名 key 和添加新 key。
func (s *ConfigService) SetAllTerminalPresets(tp *TerminalPresetsConfig) error {
	if tp == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.TerminalPresets == nil {
		s.config.TerminalPresets = &TerminalPresetsConfig{}
	}
	for k, v := range tp.ClaudeCode {
		if s.config.TerminalPresets.ClaudeCode == nil {
			s.config.TerminalPresets.ClaudeCode = map[string]TerminalPreset{}
		}
		s.config.TerminalPresets.ClaudeCode[k] = v
	}
	for k, v := range tp.OpenCode {
		if s.config.TerminalPresets.OpenCode == nil {
			s.config.TerminalPresets.OpenCode = map[string]TerminalPreset{}
		}
		s.config.TerminalPresets.OpenCode[k] = v
	}
	for k, v := range tp.Codex {
		if s.config.TerminalPresets.Codex == nil {
			s.config.TerminalPresets.Codex = map[string]TerminalPreset{}
		}
		s.config.TerminalPresets.Codex[k] = v
	}
	return s.saveLocked()
}

func (s *ConfigService) GetAgentTeams() AgentTeamsConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return AgentTeamsConfig{}
	}
	return s.config.AgentTeams
}

func (s *ConfigService) SetAgentTeams(config AgentTeamsConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	s.config.AgentTeams = config
	return s.saveLocked()
}

// MergedTerminalPreset 合并后的终端预设，供 Dashboard 展示用。
// 优先使用 terminal_presets（新体系），按 provider 分组回退到 provider.presets（旧体系）。
type MergedTerminalPreset struct {
	Key         string `json:"key"`         // 稳定 key（用于读写后端）
	Label       string `json:"label"`       // 友好展示名
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Source      string `json:"source"` // "terminal_preset" 或 "provider_preset"
}

// GetMergedTerminalPresets 按 terminalType 返回合并后的预设列表。
// terminal_presets 优先；若某个 provider 在新体系中没有预设，则回退到旧 provider.presets。
func (s *ConfigService) GetMergedTerminalPresets(terminalType string) ([]MergedTerminalPreset, error) {
	if !IsValidTerminalPresetType(terminalType) {
		return nil, fmt.Errorf("invalid terminal preset type: %s", terminalType)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, errors.New("config not loaded")
	}

	tt := TerminalPresetType(terminalType)
	var result []MergedTerminalPreset

	// 1. 先从 terminal_presets 读取（新体系，优先）
	if s.config.TerminalPresets != nil {
		tpMap := s.config.TerminalPresets.GetMap(tt)
		for key, tp := range tpMap {
			label := tp.Name
			if label == "" {
				label = key
			}
			result = append(result, MergedTerminalPreset{
				Key:      key,
				Label:    label,
				Provider: tp.Provider,
				Model:    tp.Model,
				Source:   "terminal_preset",
			})
		}
	}

	// 2. 收集已有 terminal_preset 的 (provider, key) 对
	seen := make(map[string]bool) // "provider/key"
	for _, mp := range result {
		seen[mp.Provider+"/"+mp.Key] = true
	}

	// 3. 回退到 provider.presets（旧体系），补充未在新体系中出现的预设
	for provName, prov := range s.config.Models {
		for presetName, preset := range prov.Presets {
			target := preset.GetTarget()
			isOpenAI := prov.Type == "openai" || prov.AuthKey == "OPENAI_API_KEY"

			var expectedType TerminalPresetType
			switch {
			case target == PresetTargetOpenCode:
				expectedType = TerminalPresetOpenCode
			case isOpenAI:
				expectedType = TerminalPresetCodex
			default:
				expectedType = TerminalPresetClaudeCode
			}

			if expectedType != tt {
				continue
			}

			stableKey := provName + "/" + presetName
			if seen[stableKey] {
				continue // 已在新体系中
			}

			label := preset.Name
			if label == "" {
				label = presetName
			}
			result = append(result, MergedTerminalPreset{
				Key:      stableKey,
				Label:    label,
				Provider: provName,
				Model:    preset.Model,
				Source:   "provider_preset",
			})
		}
	}

	if result == nil {
		result = []MergedTerminalPreset{}
	}
	return result, nil
}

// ResolveTerminalPreset 按 terminal type + stable key 解析出实际的 TerminalPreset。
// 用于启动链：先查新体系，返回 (providerName, *TerminalPreset)。
// 若 key 不在新体系中则返回 ("", nil)。
func (s *ConfigService) ResolveTerminalPreset(terminalType, key string) (string, *TerminalPreset, error) {
	if !IsValidTerminalPresetType(terminalType) {
		return "", nil, fmt.Errorf("invalid terminal preset type: %s", terminalType)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return "", nil, errors.New("config not loaded")
	}
	if s.config.TerminalPresets == nil {
		return "", nil, nil
	}
	tpMap := s.config.TerminalPresets.GetMap(TerminalPresetType(terminalType))
	if tpMap == nil {
		return "", nil, nil
	}
	tp, ok := tpMap[key]
	if !ok {
		return "", nil, nil
	}
	cp := tp // shallow copy
	return cp.Provider, &cp, nil
}

// GetUrlHistory 获取指定Provider的URL历史
func (s *ConfigService) GetUrlHistory(providerID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, errors.New("config not loaded")
	}
	p, ok := s.config.Models[providerID]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", providerID)
	}
	if p.UrlHistory == nil {
		return []string{}, nil
	}
	// 返回副本，避免外部修改影响内部数据
	history := make([]string, len(p.UrlHistory))
	copy(history, p.UrlHistory)
	return history, nil
}

// AddUrlToHistory 添加URL到历史记录（去重、限制20条、最近在前）
func (s *ConfigService) AddUrlToHistory(providerID, url string) error {
	if providerID == "" {
		return errors.New("provider ID is required")
	}
	if url == "" {
		return errors.New("URL is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	p, ok := s.config.Models[providerID]
	if !ok {
		return fmt.Errorf("provider not found: %s", providerID)
	}

	// 初始化历史记录
	if p.UrlHistory == nil {
		p.UrlHistory = []string{}
	}

	// 去重：如果URL已存在，先移除旧的
	newHistory := []string{}
	for _, existingUrl := range p.UrlHistory {
		if existingUrl == url {
			continue // 跳过已存在的URL
		}
		newHistory = append(newHistory, existingUrl)
	}

	// 将新URL添加到最前面
	newHistory = append([]string{url}, newHistory...)

	// 限制最多20条
	if len(newHistory) > 20 {
		newHistory = newHistory[:20]
	}

	p.UrlHistory = newHistory
	s.config.Models[providerID] = p
	return s.saveLocked()
}

// RemoveUrlFromHistory 从历史记录中删除指定URL
func (s *ConfigService) RemoveUrlFromHistory(providerID, url string) error {
	if providerID == "" {
		return errors.New("provider ID is required")
	}
	if url == "" {
		return errors.New("URL is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	p, ok := s.config.Models[providerID]
	if !ok {
		return fmt.Errorf("provider not found: %s", providerID)
	}

	if p.UrlHistory == nil {
		return nil // 空历史记录，无需删除
	}

	// 过滤掉要删除的URL
	newHistory := []string{}
	for _, existingUrl := range p.UrlHistory {
		if existingUrl != url {
			newHistory = append(newHistory, existingUrl)
		}
	}

	p.UrlHistory = newHistory
	s.config.Models[providerID] = p
	return s.saveLocked()
}
