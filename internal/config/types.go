package config

import (
	"encoding/json"
	"strings"
)

// ThinkingConfig 思考模式配置
// 完全兼容 models.json：thinking.type / thinking.budgetTokens
type ThinkingConfig struct {
	Type         string `json:"type"`                   // "enabled" | "disabled"
	BudgetTokens int    `json:"budgetTokens,omitempty"` // 可选预算
}

// ContextWindowConfig 上下文窗口配置
// 支持 Codex CLI 风格的配置格式
type ContextWindowConfig struct {
	ModelContextWindow    int `json:"model_context_window,omitempty"`           // 上下文窗口大小（如 1047576 表示 1M）
	AutoCompactTokenLimit int `json:"model_auto_compact_token_limit,omitempty"` // 历史上下文自动压缩触发阈值
}

// Parameters 模型参数
// 注意：do_sample/stream 使用指针以区分 false 与未设置（omitempty）。
type Parameters struct {
	Temperature      float64              `json:"temperature,omitempty"`
	TopP             float64              `json:"top_p,omitempty"`
	MaxTokens        int                  `json:"max_tokens,omitempty"`
	MaxContextLength int                  `json:"max_context_length,omitempty"`
	DoSample         *bool                `json:"do_sample,omitempty"`
	Thinking         *ThinkingConfig      `json:"thinking,omitempty"`
	Stream           *bool                `json:"stream,omitempty"`
	ContextWindow    *ContextWindowConfig `json:"context_window,omitempty"` // 上下文窗口配置（Codex CLI 风格）
	ReasoningEffort  string               `json:"reasoning_effort,omitempty"` // Claude Code 推理强度（low/medium/high/xhigh/max）
}

// PresetTargetType 定义 preset 目标 CLI 类型
type PresetTargetType string

const (
	// PresetTargetCodex 表示 preset 用于 Codex CLI（默认值）
	PresetTargetCodex PresetTargetType = "codex"
	// PresetTargetOpenCode 表示 preset 用于 OpenCode CLI
	PresetTargetOpenCode PresetTargetType = "opencode"
)

// Preset 预设配置
type Preset struct {
	Name           string           `json:"name"`
	Model          string           `json:"model"`
	ModelHaiku     string           `json:"model_haiku,omitempty"`     // Haiku 档位模型（Claude Code 专用）
	ModelSonnet    string           `json:"model_sonnet,omitempty"`    // Sonnet 档位模型（Claude Code 专用）
	ModelOpus      string           `json:"model_opus,omitempty"`      // Opus 档位模型（Claude Code 专用）
	Parameters     Parameters       `json:"parameters"`
	Target         PresetTargetType `json:"target,omitempty"`          // 目标 CLI 类型：codex（默认）或 opencode
	OpenCodeConfig json.RawMessage  `json:"opencode_config,omitempty"` // OpenCode 原始配置片段，原样保真，未知字段不丢失
}

// GetTarget 返回 preset 的目标 CLI 类型。
// 旧 preset 没有 target 字段时，默认按 codex 处理，保持向后兼容。
func (p Preset) GetTarget() PresetTargetType {
	if p.Target == "" {
		return PresetTargetCodex
	}
	return p.Target
}

// IsOpenCodeTarget 判断 preset 是否目标为 OpenCode CLI
func (p Preset) IsOpenCodeTarget() bool {
	return p.GetTarget() == PresetTargetOpenCode
}

// IsCodexTarget 判断 preset 是否目标为 Codex CLI
func (p Preset) IsCodexTarget() bool {
	return p.GetTarget() == PresetTargetCodex
}

// NormalizeOpenCodeConfig 确保 OpenCodeConfig 存储为原始 JSON 对象，
// 而不是 JSON 字符串（防止前端传回时双重编码）。
//
// 调用时机：SavePreset 保存前、LaunchOpenCode 使用前。
//
// 双重编码场景：前端 Wails 把 JS string 序列化为 JSON 时，
// json.RawMessage 收到的是带引号的 JSON 字符串如 `"\"...\""` 而非 `{...}`。
// 此方法将这种字符串解包为原始 JSON 对象。
func (p *Preset) NormalizeOpenCodeConfig() {
	if len(p.OpenCodeConfig) == 0 {
		p.OpenCodeConfig = nil
		return
	}
	// 去除前后空白
	trimmed := strings.TrimSpace(string(p.OpenCodeConfig))
	if len(trimmed) == 0 {
		p.OpenCodeConfig = nil
		return
	}
	// 如果以引号开头，说明是双重编码的 JSON 字符串
	if trimmed[0] == '"' {
		var unwrapped string
		if err := json.Unmarshal([]byte(trimmed), &unwrapped); err == nil {
			// 递归检查：解包后可能仍然是字符串（极端多重编码）
			unwrappedTrimmed := strings.TrimSpace(unwrapped)
			if len(unwrappedTrimmed) > 0 && unwrappedTrimmed[0] == '"' {
				// 递归解包
				p.OpenCodeConfig = json.RawMessage(unwrapped)
				p.NormalizeOpenCodeConfig()
				return
			}
			p.OpenCodeConfig = json.RawMessage(unwrapped)
		}
		// 如果解析失败，保持原样（可能本身就是合法的纯文本值）
	}
}

// TerminalPresetType 定义终端预设的目标 CLI 类型
// 与 PresetTargetType 不同，这是终端维度的独立容器标识。
type TerminalPresetType string

const (
	// TerminalPresetClaudeCode 表示 Claude Code 终端预设
	TerminalPresetClaudeCode TerminalPresetType = "claude_code"
	// TerminalPresetOpenCode 表示 OpenCode 终端预设
	TerminalPresetOpenCode TerminalPresetType = "opencode"
	// TerminalPresetCodex 表示 Codex 终端预设
	TerminalPresetCodex TerminalPresetType = "codex"
	// TerminalPresetPi 表示 Pi coding agent 终端预设
	TerminalPresetPi TerminalPresetType = "pi"
)

// ValidTerminalPresetTypes 返回所有合法的终端预设类型
func ValidTerminalPresetTypes() []TerminalPresetType {
	return []TerminalPresetType{TerminalPresetClaudeCode, TerminalPresetOpenCode, TerminalPresetCodex, TerminalPresetPi}
}

// IsValidTerminalPresetType 检查给定类型是否合法
func IsValidTerminalPresetType(t string) bool {
	for _, vt := range ValidTerminalPresetTypes() {
		if string(vt) == t {
			return true
		}
	}
	return false
}

// TerminalPreset 终端预设配置。
// 独立于 Provider，按终端维度管理预设。
// 每个 TerminalPreset 关联一个 provider（而非内嵌于 provider 内部）。
type TerminalPreset struct {
	Name        string          `json:"name"`                   // 预设显示名称
	Provider    string          `json:"provider"`               // 关联的 provider 名称（如 "anthropic", "openai"）
	Model       string          `json:"model"`                  // 模型名称（可覆盖 provider 默认值）
	ModelHaiku  string          `json:"model_haiku,omitempty"`  // Haiku 档位模型（Claude Code 专用）
	ModelSonnet string          `json:"model_sonnet,omitempty"` // Sonnet 档位模型（Claude Code 专用）
	ModelOpus   string          `json:"model_opus,omitempty"`   // Opus 档位模型（Claude Code 专用）
	Parameters  Parameters      `json:"parameters"`             // 模型参数
	OpenCodeCfg json.RawMessage `json:"opencode_cfg,omitempty"` // OpenCode 运行时 overlay（仅 opencode 类型使用）
}

// NormalizeOpenCodeCfg 确保 OpenCodeCfg 存储为原始 JSON 对象。
func (tp *TerminalPreset) NormalizeOpenCodeCfg() {
	if len(tp.OpenCodeCfg) == 0 {
		tp.OpenCodeCfg = nil
		return
	}
	trimmed := strings.TrimSpace(string(tp.OpenCodeCfg))
	if len(trimmed) == 0 {
		tp.OpenCodeCfg = nil
		return
	}
	if trimmed[0] == '"' {
		var unwrapped string
		if err := json.Unmarshal([]byte(trimmed), &unwrapped); err == nil {
			unwrappedTrimmed := strings.TrimSpace(unwrapped)
			if len(unwrappedTrimmed) > 0 && unwrappedTrimmed[0] == '"' {
				tp.OpenCodeCfg = json.RawMessage(unwrapped)
				tp.NormalizeOpenCodeCfg()
				return
			}
			tp.OpenCodeCfg = json.RawMessage(unwrapped)
		}
	}
}

// TerminalPresetsConfig 终端预设容器，按终端类型分组。
// 存储于 AppConfig.TerminalPresets。
type TerminalPresetsConfig struct {
	ClaudeCode map[string]TerminalPreset `json:"claude_code,omitempty"`
	OpenCode   map[string]TerminalPreset `json:"opencode,omitempty"`
	Codex      map[string]TerminalPreset `json:"codex,omitempty"`
	Pi         map[string]TerminalPreset `json:"pi,omitempty"`
}

// GetMap 按 TerminalPresetType 返回对应的预设 map。
func (tpc *TerminalPresetsConfig) GetMap(terminalType TerminalPresetType) map[string]TerminalPreset {
	if tpc == nil {
		return nil
	}
	switch terminalType {
	case TerminalPresetClaudeCode:
		return tpc.ClaudeCode
	case TerminalPresetOpenCode:
		return tpc.OpenCode
	case TerminalPresetCodex:
		return tpc.Codex
	case TerminalPresetPi:
		return tpc.Pi
	}
	return nil
}

// SetMap 按 TerminalPresetType 设置对应的预设 map。
func (tpc *TerminalPresetsConfig) SetMap(terminalType TerminalPresetType, m map[string]TerminalPreset) {
	if tpc == nil {
		return
	}
	switch terminalType {
	case TerminalPresetClaudeCode:
		tpc.ClaudeCode = m
	case TerminalPresetOpenCode:
		tpc.OpenCode = m
	case TerminalPresetCodex:
		tpc.Codex = m
	case TerminalPresetPi:
		tpc.Pi = m
	}
}

// AnthropicFormat Anthropic 兼容格式配置。
//
// APIKey 仅用于导入旧 JSON / 兼容历史导出结构，
// 运行时正式密钥来源始终是 provider 级 secrets（key = providerName）。
type AnthropicFormat struct {
	Enabled bool   `json:"enabled"`
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	AuthKey string `json:"auth_key,omitempty"`
}

// OpenAIFormat OpenAI 兼容格式配置。
//
// APIKey 仅用于导入旧 JSON / 兼容历史导出结构，
// 运行时正式密钥来源始终是 provider 级 secrets（key = providerName）。
type OpenAIFormat struct {
	Enabled      bool   `json:"enabled"`
	APIKey       string `json:"api_key,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	Organization string `json:"organization,omitempty"`
	AuthKey      string `json:"auth_key,omitempty"`
}

// Provider 服务商配置
type Provider struct {
	// 双格式支持（新字段）
	Anthropic *AnthropicFormat `json:"anthropic,omitempty"`
	OpenAI    *OpenAIFormat    `json:"openai,omitempty"`

	// 通用信息
	DefaultModel string   `json:"default_model"`
	UrlHistory   []string `json:"url_history,omitempty"`

	// 废弃字段（保留兼容读取，新数据不再写入）
	Type    string            `json:"type,omitempty"`
	BaseURL string            `json:"base_url,omitempty"`
	AuthKey string            `json:"auth_key,omitempty"`
	Presets map[string]Preset `json:"presets,omitempty"`
}

// 认证类型常量
const (
	AuthTypeAPIKey    = "ANTHROPIC_API_KEY"
	AuthTypeAuthToken = "ANTHROPIC_AUTH_TOKEN"
	AuthTypeOAuth     = "OAUTH"
)

// AgentTeamsConfig Agent Teams 配置
type AgentTeamsConfig struct {
	Enabled      bool   `json:"enabled"`
	TeammateMode string `json:"teammate_mode"`
}

// AppConfig 应用总配置（对应 models.json 根结构）
type AppConfig struct {
	Models          map[string]Provider       `json:"models"`
	AgentTeams      AgentTeamsConfig          `json:"agent_teams"`
	TerminalPresets *TerminalPresetsConfig    `json:"terminal_presets,omitempty"`
	OpenCodePresets map[string]OpenCodePreset `json:"opencode_presets,omitempty"`
	Version         string                    `json:"version"`
}

// OpenCodePreset 一个预设 = 一份完整的 opencode.json。
// Config 字段保存完整的 opencode.json 配置（不含 secrets）。
// Bindings 描述 preset 中各 provider id 与本地 Provider 的映射关系。
type OpenCodePreset struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	Config      json.RawMessage            `json:"config"`
	Bindings    map[string]OpenCodeBinding `json:"bindings,omitempty"`
	Source      *OpenCodePresetSource      `json:"source,omitempty"`
}

// OpenCodeBinding 描述 preset 中某个 provider id 与本地 Provider 的绑定关系。
type OpenCodeBinding struct {
	LocalProvider string   `json:"local_provider"`
	Format        string   `json:"format,omitempty"` // openai / anthropic / auto
	Inject        []string `json:"inject,omitempty"` // apiKey / baseURL / organization
	EnvFallback   bool     `json:"env_fallback,omitempty"`
}

// OpenCodePresetSource 记录 preset 的来源，用于追踪迁移。
type OpenCodePresetSource struct {
	Kind            string `json:"kind,omitempty"` // native / migrated-overlay
	LegacyProvider  string `json:"legacy_provider,omitempty"`
	LegacyPresetKey string `json:"legacy_preset_key,omitempty"`
}

// ExportConfig 导出配置的根结构
type ExportConfig struct {
	Version         string                    `json:"version"`
	ExportedAt      string                    `json:"exported_at"`
	Source          string                    `json:"source"`
	Providers       map[string]ExportProvider `json:"providers"`
	AgentTeams      AgentTeamsConfig          `json:"agent_teams"`
	TerminalPresets *TerminalPresetsConfig    `json:"terminal_presets,omitempty"`
	OpenCodePresets map[string]OpenCodePreset `json:"opencode_presets,omitempty"`
}

// ExportProvider 导入/导出时的提供商配置（含 API key 明文）。
//
// 正式导出模型：仅顶层 APIKey 是当前规范的 provider 级统一密钥。
// Anthropic/OpenAI 内嵌 APIKey 仅用于兼容导入旧 JSON，不应作为新导出写出。
// 双格式结构（anthropic/openai）仍保留用于 baseURL / organization / auth_key 表达。
type ExportProvider struct {
	// 双格式字段（新协议）
	Anthropic *AnthropicFormat `json:"anthropic,omitempty"`
	OpenAI    *OpenAIFormat    `json:"openai,omitempty"`

	// 通用字段
	DefaultModel string            `json:"default_model"`
	Presets      map[string]Preset `json:"presets"`

	// 旧字段（保留兼容旧版导入/导出）
	Type    string `json:"type,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	AuthKey string `json:"auth_key,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
}

func cloneAnthropicFormat(src *AnthropicFormat) *AnthropicFormat {
	if src == nil {
		return nil
	}
	copy := *src
	return &copy
}

func cloneOpenAIFormat(src *OpenAIFormat) *OpenAIFormat {
	if src == nil {
		return nil
	}
	copy := *src
	return &copy
}

// BuildExportProvider 基于运行时 Provider 和统一 provider 级 API key 构建导出结构。
// Anthropic/OpenAI 内嵌 APIKey 始终会被清空，避免新导出再次写出格式化 key。
func BuildExportProvider(provider Provider, apiKey string) ExportProvider {
	presets := provider.Presets
	if presets == nil {
		presets = map[string]Preset{}
	}

	ep := ExportProvider{
		Anthropic:    cloneAnthropicFormat(provider.Anthropic),
		OpenAI:       cloneOpenAIFormat(provider.OpenAI),
		DefaultModel: provider.DefaultModel,
		Presets:      presets,
		Type:         provider.EffectiveType(),
		BaseURL:      provider.EffectiveBaseURL(""),
		AuthKey:      provider.EffectiveAuthKey(""),
		APIKey:       strings.TrimSpace(apiKey),
	}
	if ep.Anthropic != nil {
		ep.Anthropic.APIKey = ""
	}
	if ep.OpenAI != nil {
		ep.OpenAI.APIKey = ""
	}
	return ep
}

// ToProvider 将导入/编辑用的 ExportProvider 转回运行时 Provider，
// 并清理双格式结构中的 APIKey，避免明文进入 models.json。
func (ep ExportProvider) ToProvider() Provider {
	provider := Provider{
		DefaultModel: ep.DefaultModel,
		Presets:      ep.Presets,
		Anthropic:    cloneAnthropicFormat(ep.Anthropic),
		OpenAI:       cloneOpenAIFormat(ep.OpenAI),
	}
	if provider.Presets == nil {
		provider.Presets = map[string]Preset{}
	}
	if provider.Anthropic != nil {
		provider.Anthropic.APIKey = ""
	}
	if provider.OpenAI != nil {
		provider.OpenAI.APIKey = ""
	}
	if ep.Anthropic == nil && ep.OpenAI == nil {
		provider.Type = ep.Type
		provider.BaseURL = ep.BaseURL
		provider.AuthKey = ep.AuthKey
	}
	return provider
}

// UnifiedAPIKey 解析导入 JSON 中的 provider 级统一 API key。
// 优先级：顶层 api_key > 首选格式的 legacy api_key > 另一种 legacy api_key。
func (ep ExportProvider) UnifiedAPIKey() string {
	if key := strings.TrimSpace(ep.APIKey); key != "" {
		return key
	}

	provider := ep.ToProvider()
	switch provider.PreferredFormat() {
	case "openai":
		if ep.OpenAI != nil {
			if key := strings.TrimSpace(ep.OpenAI.APIKey); key != "" {
				return key
			}
		}
		if ep.Anthropic != nil {
			if key := strings.TrimSpace(ep.Anthropic.APIKey); key != "" {
				return key
			}
		}
	default:
		if ep.Anthropic != nil {
			if key := strings.TrimSpace(ep.Anthropic.APIKey); key != "" {
				return key
			}
		}
		if ep.OpenAI != nil {
			if key := strings.TrimSpace(ep.OpenAI.APIKey); key != "" {
				return key
			}
		}
	}

	return ""
}

// IsAnthropicCompatible 判断 Provider 是否兼容 Anthropic 格式。
// 优先检查新字段 Anthropic.Enabled，回退兼容旧字段 Type/AuthKey。
func (p Provider) IsAnthropicCompatible() bool {
	if p.Anthropic != nil && p.Anthropic.Enabled {
		return true
	}
	// 兼容旧数据：非 openai 类型且非 OPENAI_API_KEY
	return !strings.EqualFold(p.Type, "openai") && p.AuthKey != "OPENAI_API_KEY"
}

// IsOpenAICompatible 判断 Provider 是否兼容 OpenAI 格式。
func (p Provider) IsOpenAICompatible() bool {
	if p.OpenAI != nil && p.OpenAI.Enabled {
		return true
	}
	return strings.EqualFold(p.Type, "openai") || p.AuthKey == "OPENAI_API_KEY"
}

// PreferredFormat 返回当前 Provider 的首选格式："openai" 或 "anthropic"。
// 新字段优先，旧字段回退。若两种新格式都启用，OpenAI 优先（双格式场景）。
func (p Provider) PreferredFormat() string {
	// 双格式场景：若两者都启用，优先返回有新字段的那一方
	if p.OpenAI != nil && p.OpenAI.Enabled {
		return "openai"
	}
	if p.Anthropic != nil && p.Anthropic.Enabled {
		return "anthropic"
	}
	// 回退旧字段
	if strings.EqualFold(p.Type, "openai") || p.AuthKey == "OPENAI_API_KEY" {
		return "openai"
	}
	return "anthropic"
}

// EffectiveType 返回兼容旧逻辑的 Provider 类型。
// 新字段优先推导，旧字段回退。
func (p Provider) EffectiveType() string {
	if p.OpenAI != nil && p.OpenAI.Enabled {
		return "openai"
	}
	if p.Anthropic != nil && p.Anthropic.Enabled {
		return "anthropic"
	}
	if strings.EqualFold(p.Type, "openai") || p.AuthKey == "OPENAI_API_KEY" {
		return "openai"
	}
	if p.Type != "" {
		return p.Type
	}
	return "anthropic"
}

// EffectiveBaseURL 返回指定格式或首选格式的有效 BaseURL。
// format 为空时使用 PreferredFormat()。
func (p Provider) EffectiveBaseURL(format string) string {
	if format == "" {
		format = p.PreferredFormat()
	}
	switch strings.ToLower(format) {
	case "openai":
		if p.OpenAI != nil && p.OpenAI.BaseURL != "" {
			return p.OpenAI.BaseURL
		}
	case "anthropic":
		if p.Anthropic != nil && p.Anthropic.BaseURL != "" {
			return p.Anthropic.BaseURL
		}
	}
	return p.BaseURL
}

// EffectiveAuthKey 返回指定格式或首选格式的有效 AuthKey（认证类型标识）。
// format 为空时使用 PreferredFormat()。
func (p Provider) EffectiveAuthKey(format string) string {
	if format == "" {
		format = p.PreferredFormat()
	}
	switch strings.ToLower(format) {
	case "openai":
		if p.OpenAI != nil && p.OpenAI.AuthKey != "" {
			return p.OpenAI.AuthKey
		}
	case "anthropic":
		if p.Anthropic != nil && p.Anthropic.AuthKey != "" {
			return p.Anthropic.AuthKey
		}
	}
	return p.AuthKey
}

// IsOAuthMode 返回 Provider 是否使用 OAuth 认证（Anthropic 官方）。
func (p Provider) IsOAuthMode() bool {
	return p.EffectiveAuthKey("anthropic") == AuthTypeOAuth
}

// SyncLegacyFields 将新格式字段同步回旧顶层字段 Type/BaseURL/AuthKey，
// 以便仍依赖旧字段的代码路径能正常工作。
// 仅在新格式字段已建立时执行回填。
func (p Provider) SyncLegacyFields() Provider {
	if p.Anthropic == nil && p.OpenAI == nil {
		return p
	}
	p.Type = p.EffectiveType()
	p.BaseURL = p.EffectiveBaseURL("")
	p.AuthKey = p.EffectiveAuthKey("")
	return p
}


// IsValidClaudeReasoningEffort 检查给定的 reasoning effort 值是否合法。
// Claude Code 支持的推理强度：""（未设置/默认）| low | medium | high | xhigh | max
// 此为 Claude 划分（含 max），区别于 codexplugin 的 OpenAI 划分（none/low/medium/high/xhigh，无 max）。
// 不要与 codexplugin 的 isSupportedReasoningEffort 混淆。
func IsValidClaudeReasoningEffort(v string) bool {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return true // 空值视为合法（未设置）
	}
	switch trimmed {
	case "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}
