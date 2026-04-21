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
