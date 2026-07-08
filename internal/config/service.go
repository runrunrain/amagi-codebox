package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// scrubProviderAPIKeys 清除 Provider 内嵌的双格式 APIKey 明文字段。
// 这是持久化层硬性保障：无论调用路径如何，只要 Provider 即将被写盘，
// Anthropic.APIKey 和 OpenAI.APIKey 都会被清空。
// 返回清理后的 Provider（值类型，不影响原始数据的指针字段所指向的对象）。
func scrubProviderAPIKeys(p Provider) Provider {
	if p.Anthropic != nil {
		p.Anthropic = &AnthropicFormat{
			Enabled: p.Anthropic.Enabled,
			BaseURL: p.Anthropic.BaseURL,
			AuthKey: p.Anthropic.AuthKey,
			// APIKey 刻意不复制
		}
	}
	if p.OpenAI != nil {
		p.OpenAI = &OpenAIFormat{
			Enabled:      p.OpenAI.Enabled,
			BaseURL:      p.OpenAI.BaseURL,
			Organization: p.OpenAI.Organization,
			AuthKey:      p.OpenAI.AuthKey,
			// APIKey 刻意不复制
		}
	}
	return p
}

// scrubConfigAPIKeys 对 AppConfig 内所有 Provider 执行敏感字段清除。
// 在 saveLockedConfig / Save 写盘前调用，作为最终兜底保险。
func scrubConfigAPIKeys(cfg *AppConfig) {
	if cfg == nil || cfg.Models == nil {
		return
	}
	for name, p := range cfg.Models {
		cfg.Models[name] = scrubProviderAPIKeys(p)
	}
}

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

	// Provider 双格式升级
	migrateProviderToDualFormat(cfg.Models)
	// 清理已迁移的旧 presets（仅当 terminal_presets 已建立时）
	CleanupMigratedProviderPresets(&cfg)

	// 清洗 terminal_presets 中被前端 bug 污染的重复前缀 key/name（幂等）
	presetKeyCleaned := CleanupDuplicatedPrefixPresetKeys(&cfg)

	// 自动迁移：terminal_presets.opencode -> opencode_presets（新模型）
	migrateTerminalPresetsToOpenCodePresets(&cfg)

	// 若 terminal_preset key 被清洗，持久化幂等结果
	if presetKeyCleaned {
		if err := s.saveLockedConfig(&cfg); err != nil {
			return fmt.Errorf("cleanup duplicated preset keys: %w", err)
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

	// 最终兜底保险：写盘前清除所有 Provider 的敏感字段
	scrubConfigAPIKeys(cfg)

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
	// 最终兜底保险：写盘前对所有 Provider 执行敏感字段清除，
	// 确保即使未来新增调用路径也不会遗漏。
	scrubConfigAPIKeys(cfg)
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
	// 新字段优先：从双格式结构推导类型
	if provider.OpenAI != nil && provider.OpenAI.Enabled {
		provider.Type = "openai"
		return provider
	}
	if provider.Anthropic != nil && provider.Anthropic.Enabled {
		provider.Type = "anthropic"
		return provider
	}
	// 回退旧字段
	if provider.AuthKey == "OPENAI_API_KEY" {
		provider.Type = "openai"
	} else {
		provider.Type = "anthropic"
	}
	return provider
}

// migrateProviderToDualFormat 将旧单格式 Provider 升级为双格式结构。
// - 若 Anthropic/OpenAI 均为 nil，从旧 Type/BaseURL/AuthKey 推断并填充
// - 升级后通过 SyncLegacyFields 回填旧字段，保证旧代码路径正常工作
func migrateProviderToDualFormat(models map[string]Provider) {
	for name, p := range models {
		changed := false

		// 1. 推断 Anthropic 格式
		if p.Anthropic == nil {
			if !strings.EqualFold(p.Type, "openai") && p.AuthKey != "OPENAI_API_KEY" {
				p.Anthropic = &AnthropicFormat{
					Enabled: true,
					BaseURL: p.BaseURL,
					AuthKey: p.AuthKey,
				}
				changed = true
			}
		}

		// 2. 推断 OpenAI 格式
		if p.OpenAI == nil {
			if strings.EqualFold(p.Type, "openai") || p.AuthKey == "OPENAI_API_KEY" {
				p.OpenAI = &OpenAIFormat{
					Enabled: true,
					BaseURL: p.BaseURL,
					AuthKey: p.AuthKey,
				}
				changed = true
			}
		}

		// 3. 如果已升级，回填旧字段（而非清空），保证旧代码路径可用
		if changed || p.Anthropic != nil || p.OpenAI != nil {
			p = p.SyncLegacyFields()
		}

		models[name] = p
	}
}

// CleanupMigratedProviderPresets 清理已迁移到 terminal_presets 的旧 provider.presets。
// 仅当 terminal_presets 中存在对应条目（同 provider/presetName stable key）时才清理。
// 幂等安全：多次调用无副作用。
func CleanupMigratedProviderPresets(cfg *AppConfig) {
	if cfg.TerminalPresets == nil {
		return
	}

	for provName, prov := range cfg.Models {
		if len(prov.Presets) == 0 {
			continue
		}

		allCleaned := true
		for presetName := range prov.Presets {
			stableKey := provName + "/" + presetName

			// 检查 terminal_presets 中是否存在对应条目
			found := false
			for _, tt := range []TerminalPresetType{TerminalPresetClaudeCode, TerminalPresetOpenCode, TerminalPresetCodex} {
				tpMap := cfg.TerminalPresets.GetMap(tt)
				if tpMap != nil {
					if _, exists := tpMap[stableKey]; exists {
						found = true
						break
					}
				}
			}

			if !found {
				allCleaned = false
				break
			}
		}

		if allCleaned {
			prov.Presets = nil
			cfg.Models[provName] = prov
		}
	}
}

// compressDuplicatedPrefixPresetKey 检测并压缩 terminal_preset key 中重复堆叠的前缀。
// 例：glm/glm/glm/max -> glm/max；glm/glm/glm/glm/max -> glm/max；glm/glm -> glm。
// 规则：以第一段为前缀，若其后续若干连续段都等于该前缀，则只保留一份前缀 + 剩余部分。
// 支持 2 段纯重复（glm/glm -> glm）到 N 段重复（含剩余 tail）。
// 幂等保证：无重复前缀的 key（如 glm/code、agent、glm/max）返回原值不压缩。
// 返回 (压缩后 key, 是否检测到污染)。
func compressDuplicatedPrefixPresetKey(key string) (string, bool) {
	if key == "" {
		return key, false
	}
	parts := strings.Split(key, "/")
	// 至少需要 2 段才可能形成重复前缀：
	//   - 2 段纯重复：glm/glm -> glm
	//   - 3+ 段含 tail：glm/glm/glm/max -> glm/max
	if len(parts) < 2 {
		return key, false
	}
	prefix := parts[0]
	if prefix == "" {
		return key, false
	}
	i := 1
	for i < len(parts) && parts[i] == prefix {
		i++
	}
	// i 表示连续 prefix 段的结束位置（不含）。至少 2 段 prefix 才算污染。
	if i < 2 {
		return key, false
	}
	// 剩余段数必须 >= 1（否则 key = "glm/glm" 这种纯重复，属于异常，但仍按规则压缩为 "glm"）
	if i == len(parts) {
		// 整串都是 prefix 重复：glm/glm -> glm；glm/glm/glm -> glm
		return prefix, true
	}
	compressed := prefix + "/" + strings.Join(parts[i:], "/")
	if compressed == key {
		return key, false
	}
	return compressed, true
}

// CleanupDuplicatedPrefixPresetKeys 清洗 terminal_presets 中被前端 bug 污染的 key/name。
//
// 背景：前端某 bug 导致 terminal_preset key 重复堆叠 provider 前缀，
// 例如正常 `glm/max` 被写为 `glm/glm/glm/max`（claude_code 3 层）或
// `glm/glm/glm/glm/max`（codex 4 层）。
//
// 清洗规则（幂等）：
//  1. 检测首段重复 N（>=2）次后跟剩余部分（compressDuplicatedPrefixPresetKey）
//  2. 压缩为 `prefix + 剩余`，如 `glm/glm/glm/max` -> `glm/max`
//  3. name 同步：若 name 等于旧污染 key（含重复前缀），恢复为压缩后的 key；
//     否则保留原 name（不破坏用户自定义 label）
//  4. 冲突合并：若压缩后的 key 已存在（用户已显式设置的预设或已清洗条目），
//     保留现有条目，不覆盖，保证幂等安全
//  5. 其他字段（Provider/Model/Model*/Parameters/OpenCodeCfg 等）原样保留
//
// 返回是否发生变更（用于决定是否需要写盘）。
func CleanupDuplicatedPrefixPresetKeys(cfg *AppConfig) bool {
	if cfg == nil || cfg.TerminalPresets == nil {
		return false
	}

	changed := false
	for _, tt := range []TerminalPresetType{TerminalPresetClaudeCode, TerminalPresetOpenCode, TerminalPresetCodex} {
		original := cfg.TerminalPresets.GetMap(tt)
		if len(original) == 0 {
			continue
		}

		// 先识别无重复前缀的"正常 key"集合（清洗后不允许覆盖它们）
		cleanKeys := make(map[string]bool, len(original))
		for k := range original {
			if _, polluted := compressDuplicatedPrefixPresetKey(k); !polluted {
				cleanKeys[k] = true
			}
		}

		compressed := make(map[string]TerminalPreset, len(original))
		// 第一遍：放入所有"正常 key"的条目
		for k, v := range original {
			if cleanKeys[k] {
				compressed[k] = v
			}
		}

		anyChangeInType := false
		// 第二遍：处理污染 key
		for oldKey, tp := range original {
			newKey, polluted := compressDuplicatedPrefixPresetKey(oldKey)
			if !polluted {
				// 已在第一遍放入
				continue
			}

			// 标记变更（至少有一个污染 key 被处理）
			anyChangeInType = true

			// name 处理：若 name 等于旧污染 key 则恢复为压缩 key
			tpCopy := tp
			if tpCopy.Name == oldKey {
				tpCopy.Name = newKey
			}

			// 冲突合并：若 newKey 已存在（正常 key 或先处理的污染条目），不覆盖
			if _, exists := compressed[newKey]; exists {
				continue
			}
			compressed[newKey] = tpCopy
		}

		if anyChangeInType {
			cfg.TerminalPresets.SetMap(tt, compressed)
			changed = true
		}
	}

	return changed
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
	// 同步：从新格式字段回填旧字段（Type/BaseURL/AuthKey），确保旧字段与新结构一致。
	// 必须在 normalizeProviderType 之前执行，因为 normalize 依赖 Type/AuthKey 判断。
	p = p.SyncLegacyFields()
	p = normalizeProviderType(p)
	// 硬性保障：清除双格式结构中的 APIKey 明文，防止落盘到 models.json。
	p = scrubProviderAPIKeys(p)
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

// RenameProvider 重命名 provider，同步迁移 config 内的所有引用：
//   - Models map key（含 legacy Presets，随 Provider 结构整体迁移）
//   - TerminalPresets 三个 engine map 的 stable key（策略 B：前缀同步重命名）+ Provider 字段
//   - OpenCodePresets.*.Bindings.*.LocalProvider
//
// 不含 secrets 迁移（由 App.UpdateProvider 在 App 层编排）。
// 校验在持锁后、改数据前完成；所有迁移完成后单次 saveLocked() 原子写盘。
// oldName == newName 时短路返回 nil（双保险，App 层已分流）。
func (s *ConfigService) RenameProvider(oldName, newName string) error {
	// —— 持锁前校验（不依赖 config 状态）——
	if oldName == "" || newName == "" {
		return errors.New("provider name is required")
	}
	if oldName == newName {
		return nil
	}
	// "/" 会破坏 stable key 结构（stable key = provider/presetName）；
	// 前后空白会造成 UI 显示与存储不一致。两者均拒绝。
	if strings.Contains(newName, "/") {
		return fmt.Errorf("invalid provider name %q: must not contain '/'", newName)
	}
	if strings.TrimSpace(newName) != newName {
		return fmt.Errorf("invalid provider name %q: must not have leading or trailing whitespace", newName)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// —— 持锁后校验（依赖 config 状态）——
	if s.config == nil {
		return errors.New("config not loaded")
	}
	prov, ok := s.config.Models[oldName]
	if !ok {
		return fmt.Errorf("provider not found: %s", oldName)
	}
	// newName 不得与"其他" provider 重名（oldName 自身除外，但此处 oldName != newName 已保证）。
	if _, exists := s.config.Models[newName]; exists {
		return fmt.Errorf("provider already exists: %s", newName)
	}

	// —— 1. Models map 改名（legacy Presets 随 Provider 结构整体迁移，无需单独处理）——
	delete(s.config.Models, oldName)
	s.config.Models[newName] = prov

	// —— 2. TerminalPresets 三个 map：stable key 同步重命名（策略 B）——
	// 精确前缀匹配 oldName+"/"，避免 glm 误伤 glm-pro。
	// 收集待迁移项到 slice 再批量 delete+写入，绝不边遍历边改 map。
	if s.config.TerminalPresets != nil {
		prefix := oldName + "/"
		for _, tt := range []TerminalPresetType{TerminalPresetClaudeCode, TerminalPresetOpenCode, TerminalPresetCodex} {
			tpMap := s.config.TerminalPresets.GetMap(tt)
			if tpMap == nil {
				continue
			}
			type pendingRename struct {
				oldKey string
				newKey string
				tp     TerminalPreset
			}
			var pending []pendingRename
			for k, tp := range tpMap {
				if !strings.HasPrefix(k, prefix) {
					continue
				}
				// shortName 从旧 key 的 "/" 后部分提取（系统派生，非用户输入），不会堆叠。
				shortName := strings.TrimPrefix(k, prefix)
				pending = append(pending, pendingRename{
					oldKey: k,
					newKey: newName + "/" + shortName,
					tp:     tp,
				})
			}
			for _, item := range pending {
				delete(tpMap, item.oldKey)
				item.tp.Provider = newName // 同步更新 Provider 字段
				tpMap[item.newKey] = item.tp
			}
			if len(pending) > 0 {
				s.config.TerminalPresets.SetMap(tt, tpMap)
			}
		}
	}

	// —— 3. OpenCodePresets.*.Bindings.*.LocalProvider 更新 ——
	// OpenCodeBinding 是值类型，修改后必须写回 bindings map；
	// OpenCodePreset 同样是值类型，改完 bindings 后写回 OpenCodePresets map。
	if s.config.OpenCodePresets != nil {
		for ocKey, ocPreset := range s.config.OpenCodePresets {
			if ocPreset.Bindings == nil {
				continue
			}
			changed := false
			for bKey, binding := range ocPreset.Bindings {
				if binding.LocalProvider == oldName {
					binding.LocalProvider = newName
					ocPreset.Bindings[bKey] = binding
					changed = true
				}
			}
			if changed {
				s.config.OpenCodePresets[ocKey] = ocPreset
			}
		}
	}

	// —— 4. legacy provider.Presets ——
	// 无需处理：legacy Presets 嵌在 Provider 结构内，第 1 步整体迁移到 newName 时已带走。

	// —— 5. 单次原子写盘（所有 map 改完后一次性写）——
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
	// 校验 reasoning_effort 字段
	if !IsValidClaudeReasoningEffort(preset.Parameters.ReasoningEffort) {
		return fmt.Errorf("invalid reasoning_effort value: %s (valid: empty, low, medium, high, xhigh, max)", preset.Parameters.ReasoningEffort)
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
			isOpenAI := prov.IsOpenAICompatible()

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

// GetAllOpenCodePresets 返回完整的 OpenCode 预设配置副本（用于导出）。
func (s *ConfigService) GetAllOpenCodePresets() map[string]OpenCodePreset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil || s.config.OpenCodePresets == nil {
		return nil
	}
	return cloneOpenCodePresetsMap(s.config.OpenCodePresets)
}

// SetAllTerminalPresets 批量设置终端预设配置（用于导入）。
// 采用 replace 策略：以传入快照整体替换当前 terminal_presets。
func (s *ConfigService) SetAllTerminalPresets(tp *TerminalPresetsConfig) error {
	if tp == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	s.config.TerminalPresets = cloneTerminalPresetsConfig(tp)
	return s.saveLocked()
}

func cloneTerminalPresetsConfig(src *TerminalPresetsConfig) *TerminalPresetsConfig {
	if src == nil {
		return nil
	}
	dst := &TerminalPresetsConfig{}
	if src.ClaudeCode != nil {
		dst.ClaudeCode = make(map[string]TerminalPreset, len(src.ClaudeCode))
		for k, v := range src.ClaudeCode {
			dst.ClaudeCode[k] = v
		}
	}
	if src.OpenCode != nil {
		dst.OpenCode = make(map[string]TerminalPreset, len(src.OpenCode))
		for k, v := range src.OpenCode {
			dst.OpenCode[k] = v
		}
	}
	if src.Codex != nil {
		dst.Codex = make(map[string]TerminalPreset, len(src.Codex))
		for k, v := range src.Codex {
			dst.Codex[k] = v
		}
	}
	return dst
}

func cloneOpenCodePresetsMap(src map[string]OpenCodePreset) map[string]OpenCodePreset {
	if src == nil {
		return nil
	}
	dst := make(map[string]OpenCodePreset, len(src))
	for k, v := range src {
		cp := v
		cp.Config = normalizeOpenCodePresetConfig(cp.Config)
		cp.Config = scrubOpenCodePresetConfig(cp.Config)
		if cp.ID == "" {
			cp.ID = k
		}
		if v.Bindings != nil {
			cp.Bindings = make(map[string]OpenCodeBinding, len(v.Bindings))
			for bindingKey, binding := range v.Bindings {
				bindingCopy := binding
				if binding.Inject != nil {
					bindingCopy.Inject = append([]string(nil), binding.Inject...)
				}
				cp.Bindings[bindingKey] = bindingCopy
			}
		}
		if v.Source != nil {
			sourceCopy := *v.Source
			cp.Source = &sourceCopy
		}
		dst[k] = cp
	}
	return dst
}

// ReplaceImportedPresetSnapshots 以导入快照整体替换 terminal_presets / opencode_presets。
// 兼容旧导入文件：仅当未显式提供 opencode_presets 时，才会从 terminal_presets.opencode 快照回填新模型。
// nil 输入表示"空快照"，不会保留本地历史残留数据。
func (s *ConfigService) ReplaceImportedPresetSnapshots(terminal *TerminalPresetsConfig, openCode map[string]OpenCodePreset, hasExplicitOpenCodeSnapshot bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}

	terminalSnapshot := cloneTerminalPresetsConfig(terminal)
	openCodeSnapshot := cloneOpenCodePresetsMap(openCode)
	if openCodeSnapshot == nil {
		openCodeSnapshot = map[string]OpenCodePreset{}
	}

	if !hasExplicitOpenCodeSnapshot {
		tmp := &AppConfig{
			Models:          s.config.Models,
			TerminalPresets: terminalSnapshot,
			OpenCodePresets: openCodeSnapshot,
		}
		migrateTerminalPresetsToOpenCodePresets(tmp)
		openCodeSnapshot = tmp.OpenCodePresets
	}

	s.config.TerminalPresets = terminalSnapshot
	s.config.OpenCodePresets = openCodeSnapshot
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
	Key         string     `json:"key"`                   // 稳定 key（用于读写后端）
	Label       string     `json:"label"`                 // 友好展示名
	Provider    string     `json:"provider"`
	Model       string     `json:"model"`
	ModelHaiku  string     `json:"model_haiku,omitempty"`  // Haiku 档位模型（Claude Code 专用）
	ModelSonnet string     `json:"model_sonnet,omitempty"` // Sonnet 档位模型（Claude Code 专用）
	ModelOpus   string     `json:"model_opus,omitempty"`   // Opus 档位模型（Claude Code 专用）
	Parameters  Parameters `json:"parameters"`              // 模型参数
	Source      string     `json:"source"`                 // "terminal_preset" 或 "provider_preset"
}

// GetMergedTerminalPresets 按 terminalType 返回合并后的预设列表。
// 仅从 terminal_presets（新体系）读取，不再回退到旧 provider.presets。
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

	// 1. 先从 terminal_presets 读取（新体系）
	if s.config.TerminalPresets != nil {
		tpMap := s.config.TerminalPresets.GetMap(tt)
		for key, tp := range tpMap {
			label := tp.Name
			if label == "" {
				label = key
			}
			result = append(result, MergedTerminalPreset{
				Key:         key,
				Label:       label,
				Provider:    tp.Provider,
				Model:       tp.Model,
				ModelHaiku:  tp.ModelHaiku,
				ModelSonnet: tp.ModelSonnet,
				ModelOpus:   tp.ModelOpus,
				Parameters:  tp.Parameters,
				Source:      "terminal_preset",
			})
		}
	}

	// 2. 收集已有 terminal_preset 的 (provider, key) 对
	seen := make(map[string]bool) // "provider/key"
	for _, mp := range result {
		seen[mp.Provider+"/"+mp.Key] = true
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

// ============================================================================
// OpenCodePresets CRUD -- 新模型
// ============================================================================

// scrubOpenCodePresetConfig 清除 Config 中的 provider.*.options.apiKey。
// 遍历 config.provider 的每个 entry，删除 options 中的 apiKey 字段。
// 返回清理后的 Config（新分配的 RawMessage）。
func scrubOpenCodePresetConfig(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}

	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		// 无法解析则原样返回（不阻断保存）
		return raw
	}

	providers, ok := cfg["provider"].(map[string]any)
	if !ok {
		return raw
	}

	scrubbed := false
	for _, provVal := range providers {
		provMap, ok := provVal.(map[string]any)
		if !ok {
			continue
		}
		options, ok := provMap["options"].(map[string]any)
		if !ok {
			continue
		}
		if _, hasAPIKey := options["apiKey"]; hasAPIKey {
			delete(options, "apiKey")
			scrubbed = true
		}
	}

	if !scrubbed {
		return raw
	}

	cleaned, err := json.Marshal(cfg)
	if err != nil {
		return raw
	}
	return cleaned
}

// normalizeOpenCodePresetConfig 规范化 Config 为标准 RawMessage。
// 处理双重编码（前端 JS string 场景），确保存储为 JSON 对象。
func normalizeOpenCodePresetConfig(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 {
		return nil
	}
	// 双重编码检测：以引号开头说明是 JSON string
	if trimmed[0] == '"' {
		var unwrapped string
		if err := json.Unmarshal([]byte(trimmed), &unwrapped); err == nil {
			return normalizeOpenCodePresetConfig(json.RawMessage(unwrapped))
		}
	}
	return json.RawMessage(trimmed)
}

// GetOpenCodePresets 返回所有 OpenCode 预设的副本。
func (s *ConfigService) GetOpenCodePresets() map[string]OpenCodePreset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil || s.config.OpenCodePresets == nil {
		return map[string]OpenCodePreset{}
	}
	out := make(map[string]OpenCodePreset, len(s.config.OpenCodePresets))
	for k, v := range s.config.OpenCodePresets {
		out[k] = v
	}
	return out
}

// GetOpenCodePreset 返回指定 key 的 OpenCode 预设。
func (s *ConfigService) GetOpenCodePreset(key string) (*OpenCodePreset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, errors.New("config not loaded")
	}
	p, ok := s.config.OpenCodePresets[key]
	if !ok {
		return nil, fmt.Errorf("opencode preset not found: %s", key)
	}
	cp := p
	return &cp, nil
}

// SaveOpenCodePreset 保存 OpenCode 预设。
// 保存前：规范化 Config、scrub apiKey、自动补 ID。
func (s *ConfigService) SaveOpenCodePreset(key string, preset OpenCodePreset) error {
	if key == "" {
		return errors.New("opencode preset key is required")
	}

	// 规范化 Config
	preset.Config = normalizeOpenCodePresetConfig(preset.Config)

	// Scrub apiKey
	preset.Config = scrubOpenCodePresetConfig(preset.Config)

	// 自动补 ID
	if preset.ID == "" {
		preset.ID = key
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.OpenCodePresets == nil {
		s.config.OpenCodePresets = map[string]OpenCodePreset{}
	}
	s.config.OpenCodePresets[key] = preset
	return s.saveLocked()
}

// DeleteOpenCodePreset 删除指定的 OpenCode 预设。
func (s *ConfigService) DeleteOpenCodePreset(key string) error {
	if key == "" {
		return errors.New("opencode preset key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.OpenCodePresets != nil {
		delete(s.config.OpenCodePresets, key)
	}
	if s.config.TerminalPresets != nil {
		legacy := s.config.TerminalPresets.GetMap(TerminalPresetOpenCode)
		if legacy != nil {
			delete(legacy, key)
			s.config.TerminalPresets.SetMap(TerminalPresetOpenCode, legacy)
		}
	}
	return s.saveLocked()
}

// migrateTerminalPresetsToOpenCodePresets 将 terminal_presets.opencode 中尚未迁移的条目
// 自动迁移到 opencode_presets（新模型）。
// 迁移策略：
//   - 调用当前 legacy BuildOpenCodeRuntimeConfig 生成"无 secrets 的完整 JSON"作为 Config
//   - Bindings 由旧 provider 字段推导为一个最小 binding
//   - Source.Kind = "migrated-overlay"
//
// 幂等安全：已有同名 key 的 opencode_presets 不会被覆盖。
func migrateTerminalPresetsToOpenCodePresets(cfg *AppConfig) {
	if cfg == nil || cfg.TerminalPresets == nil {
		return
	}

	ocTPMap := cfg.TerminalPresets.GetMap(TerminalPresetOpenCode)
	if len(ocTPMap) == 0 {
		return
	}

	if cfg.OpenCodePresets == nil {
		cfg.OpenCodePresets = map[string]OpenCodePreset{}
	}

	for stableKey, tp := range ocTPMap {
		// 不覆盖已有的 opencode_preset
		if _, exists := cfg.OpenCodePresets[stableKey]; exists {
			continue
		}

		// 构建完整 opencode.json（无 secrets）
		var fullConfig json.RawMessage
		if tp.Provider != "" {
			if prov, ok := cfg.Models[tp.Provider]; ok {
				// 使用 legacy builder 生成无 secrets 的配置
				runtimeCfg, err := buildMigratedOpenCodeConfig(tp.Provider, prov, tp)
				if err == nil {
					if raw, mErr := json.Marshal(runtimeCfg); mErr == nil {
						fullConfig = raw
					}
				}
			}
		}

		// 若无法生成（provider 不存在等），使用 tp.OpenCodeCfg 作为 fallback
		if len(fullConfig) == 0 && len(tp.OpenCodeCfg) > 0 {
			tp.NormalizeOpenCodeCfg()
			fullConfig = tp.OpenCodeCfg
		}

		// Scrub apiKey
		fullConfig = scrubOpenCodePresetConfig(fullConfig)

		// 推导最小 binding
		bindings := map[string]OpenCodeBinding{}
		if tp.Provider != "" {
			// 猜测 provider ID 和 format
			provFormat := "anthropic"
			if prov, ok := cfg.Models[tp.Provider]; ok {
				provFormat = prov.PreferredFormat()
			}

			ocProviderID := tp.Provider
			if prov, ok := cfg.Models[tp.Provider]; ok {
				ocProviderID = deriveOpenCodeProviderIDSimple(tp.Provider, prov)
			}

			bindings[ocProviderID] = OpenCodeBinding{
				LocalProvider: tp.Provider,
				Format:        provFormat,
				Inject:        []string{"apiKey", "baseURL"},
			}
		}

		cfg.OpenCodePresets[stableKey] = OpenCodePreset{
			ID:       stableKey,
			Name:     tp.Name,
			Config:   fullConfig,
			Bindings: bindings,
			Source: &OpenCodePresetSource{
				Kind:            "migrated-overlay",
				LegacyProvider:  tp.Provider,
				LegacyPresetKey: stableKey,
			},
		}
	}
}

// deriveOpenCodeProviderIDSimple 是一个无外部依赖的 provider ID 推导。
// 与 launcher 包中的 deriveOpenCodeProviderID 逻辑一致。
func deriveOpenCodeProviderIDSimple(providerName string, provider Provider) string {
	baseURL := strings.TrimSpace(strings.ToLower(provider.EffectiveBaseURL("")))
	if provider.IsOpenAICompatible() {
		if strings.Contains(baseURL, "api.openai.com") {
			return "openai"
		}
		return providerName
	}
	if strings.Contains(baseURL, "api.anthropic.com") {
		return "anthropic"
	}
	return providerName
}

// buildMigratedOpenCodeConfig 使用 legacy 逻辑生成迁移用的完整 opencode.json。
// 不注入 secrets（apiKey 传空）。
func buildMigratedOpenCodeConfig(providerName string, provider Provider, tp TerminalPreset) (map[string]any, error) {
	// 构建 legacy Preset 以复用已有逻辑
	legacyPreset := Preset{
		Name:           tp.Name,
		Model:          tp.Model,
		Parameters:     tp.Parameters,
		OpenCodeConfig: tp.OpenCodeCfg,
	}
	legacyPreset.NormalizeOpenCodeConfig()

	// 临时注入到 provider 副本中
	provCopy := provider
	if provCopy.Presets == nil {
		provCopy.Presets = map[string]Preset{}
	}
	provCopy.Presets["__migration__"] = legacyPreset

	// 确定 ocProviderID
	ocProviderID := deriveOpenCodeProviderIDSimple(providerName, provider)

	// 确定 model
	model := provider.DefaultModel
	if tp.Model != "" {
		model = tp.Model
	}

	result := map[string]any{}

	if model != "" {
		result["model"] = ocProviderID + "/" + model
	}

	// 构建 provider entry（无 apiKey）
	isOpenAIType := provider.IsOpenAICompatible()
	effectiveBaseURL := provider.EffectiveBaseURL("")
	options := map[string]any{}
	if isOpenAIType {
		if effectiveBaseURL != "" {
			options["baseURL"] = effectiveBaseURL
		}
	} else {
		lowerBaseURL := strings.TrimSpace(strings.ToLower(effectiveBaseURL))
		if lowerBaseURL != "" && !strings.Contains(lowerBaseURL, "api.anthropic.com") {
			options["baseURL"] = effectiveBaseURL
		}
	}

	providerEntry := map[string]any{}
	if len(options) > 0 {
		providerEntry["options"] = options
	}

	// 模型参数
	modelOpts := map[string]any{}
	if legacyPreset.Parameters.Thinking != nil {
		thinking := map[string]any{"type": legacyPreset.Parameters.Thinking.Type}
		if legacyPreset.Parameters.Thinking.BudgetTokens > 0 {
			thinking["budgetTokens"] = legacyPreset.Parameters.Thinking.BudgetTokens
		}
		modelOpts["thinking"] = thinking
	}
	if legacyPreset.Parameters.Temperature != 0 {
		modelOpts["temperature"] = legacyPreset.Parameters.Temperature
	}
	if legacyPreset.Parameters.TopP != 0 {
		modelOpts["topP"] = legacyPreset.Parameters.TopP
	}
	if legacyPreset.Parameters.MaxTokens != 0 {
		modelOpts["maxTokens"] = legacyPreset.Parameters.MaxTokens
	}
	if len(modelOpts) > 0 && model != "" {
		modelsMap := map[string]any{}
		modelsMap[model] = map[string]any{"options": modelOpts}
		providerEntry["models"] = modelsMap
	}

	result["provider"] = map[string]any{ocProviderID: providerEntry}

	// 深度合并 OpenCodeCfg（overlay）
	if len(legacyPreset.OpenCodeConfig) > 0 {
		var overlay map[string]any
		if err := json.Unmarshal(legacyPreset.OpenCodeConfig, &overlay); err == nil {
			result = deepMergeSimple(result, overlay)
		}
	}

	return result, nil
}

// deepMergeSimple 递归合并两个 map，other 中的值覆盖 base。
// 与 launcher 包中的 deepMerge 逻辑一致，避免循环导入。
func deepMergeSimple(base, other map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(other))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range other {
		existing, exists := result[k]
		if exists {
			existingMap, ok1 := existing.(map[string]any)
			otherMap, ok2 := v.(map[string]any)
			if ok1 && ok2 {
				result[k] = deepMergeSimple(existingMap, otherMap)
				continue
			}
		}
		result[k] = v
	}
	return result
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
