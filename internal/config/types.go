package config

import (
	"encoding/json"
	"net/url"
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
	ContextWindow    *ContextWindowConfig `json:"context_window,omitempty"`   // 上下文窗口配置（Codex CLI 风格）
	ReasoningEffort  string               `json:"reasoning_effort,omitempty"` // Claude Code 推理强度（low/medium/high/xhigh/max）
	// PiCompat 是 pi 专属的 model 级兼容标志（supportsDeveloperRole/supportsReasoningEffort/
	// forceAdaptiveThinking/allowEmptySignature 等），原样透传给 pi models.json model.compat。
	// 仅在配置中存在时透传；例外是 supportsDeveloperRole——BuildPiModelsConfig 对未显式
	// 设置的预设默认写 false（第三方 OpenAI 兼容服务商多不接受 developer 角色，见
	// pi_config.go），显式值优先。其余键名必须与 pi 文档一致。
	PiCompat map[string]any `json:"pi_compat,omitempty"`
}

// PresetTargetType 定义 preset 目标 CLI 类型
type PresetTargetType string

const (
	// PresetTargetCodex 表示 preset 用于 Codex CLI（默认值）
	PresetTargetCodex PresetTargetType = "codex"
	// PresetTargetOpenCode 表示 preset 用于 OpenCode CLI
	PresetTargetOpenCode PresetTargetType = "opencode"
	// PresetTargetPi 表示 preset 用于 Pi coding agent
	PresetTargetPi PresetTargetType = "pi"
)

// Preset 预设配置
type Preset struct {
	Name           string           `json:"name"`
	Model          string           `json:"model"`
	ModelHaiku     string           `json:"model_haiku,omitempty"`  // Haiku 档位模型（Claude Code 专用）
	ModelSonnet    string           `json:"model_sonnet,omitempty"` // Sonnet 档位模型（Claude Code 专用）
	ModelOpus      string           `json:"model_opus,omitempty"`   // Opus 档位模型（Claude Code 专用）
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

// IsPiTarget 判断 preset 是否目标为 Pi coding agent
func (p Preset) IsPiTarget() bool {
	return p.GetTarget() == PresetTargetPi
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

// TerminalPresetType 定义公共预设的协议格式。
//
// 新模型只维护 Anthropic / OpenAI 两个公共桶：Claude Code 消费 Anthropic，
// Codex / Pi / OMP 及后续 OpenAI-compatible CLI 消费 OpenAI。OpenCode 使用
// 独立的 OpenCodePresets，不属于 TerminalPresetsConfig。
//
// 旧 CLI 级常量保留为 API/导入兼容别名，所有 CRUD/解析都会规范化到公共格式。
type TerminalPresetType string

const (
	TerminalPresetAnthropic TerminalPresetType = "anthropic"
	TerminalPresetOpenAI    TerminalPresetType = "openai"

	// 以下常量仅用于兼容历史 models.json 与旧调用方。
	TerminalPresetClaudeCode TerminalPresetType = "claude_code"
	TerminalPresetOpenCode   TerminalPresetType = "opencode"
	TerminalPresetCodex      TerminalPresetType = "codex"
	TerminalPresetPi         TerminalPresetType = "pi"
	TerminalPresetOmp        TerminalPresetType = "omp"
)

// ValidTerminalPresetTypes 返回当前持久化/导出的公共预设类型。
func ValidTerminalPresetTypes() []TerminalPresetType {
	return []TerminalPresetType{TerminalPresetAnthropic, TerminalPresetOpenAI}
}

// CompatibleTerminalPresetTypes returns every accepted import/API spelling.
// Persistence and exports must continue to use ValidTerminalPresetTypes only.
func CompatibleTerminalPresetTypes() []TerminalPresetType {
	return []TerminalPresetType{
		TerminalPresetAnthropic,
		TerminalPresetOpenAI,
		TerminalPresetClaudeCode,
		TerminalPresetOpenCode,
		TerminalPresetCodex,
		TerminalPresetPi,
		TerminalPresetOmp,
	}
}

// CanonicalTerminalPresetType 把公共格式或历史 CLI 名称解析为公共格式。
// OpenCode 返回自身仅用于旧 API 兼容迁移；新数据应写入 OpenCodePresets。
func CanonicalTerminalPresetType(t string) (TerminalPresetType, bool) {
	switch TerminalPresetType(t) {
	case TerminalPresetAnthropic, TerminalPresetClaudeCode:
		return TerminalPresetAnthropic, true
	case TerminalPresetOpenAI, TerminalPresetCodex, TerminalPresetPi, TerminalPresetOmp:
		return TerminalPresetOpenAI, true
	case TerminalPresetOpenCode:
		return TerminalPresetOpenCode, true
	default:
		return "", false
	}
}

// IsValidTerminalPresetType 检查给定类型是否合法
func IsValidTerminalPresetType(t string) bool {
	_, ok := CanonicalTerminalPresetType(t)
	return ok
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

// TerminalPresetsConfig 公共预设容器。
// Anthropic / OpenAI 是当前持久化模型；其余字段仅用于读取旧 models.json / 导入快照，
// Load 后会无损合并并清空，因此新写盘不再随 CLI 数量扩张而增加重复桶。
type TerminalPresetsConfig struct {
	Anthropic map[string]TerminalPreset `json:"anthropic,omitempty"`
	OpenAI    map[string]TerminalPreset `json:"openai,omitempty"`

	// Deprecated compatibility buckets.
	ClaudeCode map[string]TerminalPreset `json:"claude_code,omitempty"`
	OpenCode   map[string]TerminalPreset `json:"opencode,omitempty"`
	Codex      map[string]TerminalPreset `json:"codex,omitempty"`
	Pi         map[string]TerminalPreset `json:"pi,omitempty"`
	Omp        map[string]TerminalPreset `json:"omp,omitempty"`
}

// GetMap 按公共格式或旧 CLI 别名返回预设 map。
func (tpc *TerminalPresetsConfig) GetMap(terminalType TerminalPresetType) map[string]TerminalPreset {
	if tpc == nil {
		return nil
	}
	canonical, ok := CanonicalTerminalPresetType(string(terminalType))
	if !ok {
		return nil
	}
	switch canonical {
	case TerminalPresetAnthropic:
		if tpc.Anthropic != nil {
			return tpc.Anthropic
		}
		return tpc.ClaudeCode
	case TerminalPresetOpenAI:
		if tpc.OpenAI != nil {
			return tpc.OpenAI
		}
		// 仅供尚未经过 Load 迁移的兼容对象读取对应旧桶。
		switch terminalType {
		case TerminalPresetPi:
			return tpc.Pi
		case TerminalPresetOmp:
			return tpc.Omp
		default:
			return tpc.Codex
		}
	case TerminalPresetOpenCode:
		return tpc.OpenCode
	}
	return nil
}

// SetMap 按公共格式或旧 CLI 别名写入公共桶。
func (tpc *TerminalPresetsConfig) SetMap(terminalType TerminalPresetType, m map[string]TerminalPreset) {
	if tpc == nil {
		return
	}
	canonical, ok := CanonicalTerminalPresetType(string(terminalType))
	if !ok {
		return
	}
	switch canonical {
	case TerminalPresetAnthropic:
		tpc.Anthropic = m
	case TerminalPresetOpenAI:
		tpc.OpenAI = m
	case TerminalPresetOpenCode:
		tpc.OpenCode = m
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
	// Headers 是注入到该格式请求的自定义头（可选，透传给 pi models.json provider.headers）。
	Headers map[string]string `json:"headers,omitempty"`
	// AuthHeader 为 true 时强制携带 Authorization: Bearer（可选，透传给 pi authHeader）。
	AuthHeader *bool `json:"auth_header,omitempty"`
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
	// Headers 是注入到该格式请求的自定义头（可选，透传给 pi models.json provider.headers）。
	Headers map[string]string `json:"headers,omitempty"`
	// AuthHeader 为 true 时强制携带 Authorization: Bearer（可选，透传给 pi authHeader）。
	AuthHeader *bool `json:"auth_header,omitempty"`
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
	// Portable is present in v2 exports. It contains non-provider application
	// configuration required to reproduce the same setup on another device.
	// json.RawMessage keeps the config package independent from the individual
	// settings/path and other service packages; the App boundary owns composition.
	Portable json.RawMessage `json:"portable,omitempty"`
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
		BaseURL:      provider.EffectiveBaseURLRaw(""),
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

// NormalizeOpenAIBaseURL 归一化 OpenAI 兼容端点的 base URL。
//
// 处理规则（保守、不丢数据，仅作用于可确认的 URL path）：
//  1. TrimSpace；
//  2. 用 net/url 解析；解析失败、host 缺失（相对路径、scheme-only、普通非
//     URL 字符串）保守返回 TrimSpace 后的原值，避免对 hostless 输入做破坏性
//     裁剪（如 "/chat/completions" 被裁成空串、"https://" 被裁成 "https:"）；
//  3. 仅对 URL.Path 做后缀处理：循环剥离尾部 "/" 与 "/chat/completions" 后缀
//     （精确后缀匹配，大小写敏感，保证幂等：重复后缀也被完全剥离）；
//  4. query/fragment/host/scheme/userinfo 等其他部分原样保留；path 未变化时
//     直接返回原值，零副作用。
//
// 设计意图：用户常粘贴带 "/chat/completions" 后缀的完整端点（如
// https://opencode.ai/zen/go/v1/chat/completions），而各 AI CLI（opencode
// @ai-sdk/openai-compatible、codex、pi）默认会在 baseURL 后自行拼接该路径，
// 原样透传会导致双重后缀请求失败。此处统一剥离后缀，下游再按需拼接。
//
// 限制在 URL path 内处理（而非对整个字符串做后缀裁剪），可避免破坏带
// query/fragment 的企业网关或签名地址（如 ?redirect=/chat/completions/）。
//
// anthropic 格式的 base URL 不经此函数（见 EffectiveBaseURL），以避免破坏
// anthropic 端点语义。
func NormalizeOpenAIBaseURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	parsed, err := url.Parse(s)
	if err != nil {
		return s
	}
	// host 缺失（相对路径、scheme-only、非 URL 字符串）保守返回原值。
	if parsed.Host == "" {
		return s
	}
	// 基于编码形式（EscapedPath）做后缀剥离，保留 %2F 等转义序列不被误当真实分隔符。
	// parsed.Path 是解码值，无法区分字面 "/" 与 "%2F"；直接对 Path 操作会破坏转义路径
	// （如网关单段路由 /v1%2Fchat%2Fcompletions 会被解码为 /v1/chat/completions 而误裁剪，
	// 且清空 RawPath 会永久丢失编码提示）。EscapedPath 返回编码形式，仅匹配其中的字面
	// "/" 与 "chat/completions"，转义斜杠 %2F/%2f 不受影响，混合转义输入往返无损。
	// （增量复审新 Major：转义路径保守处理）
	escapedOrig := parsed.EscapedPath()
	escaped := escapedOrig
	for {
		next := strings.TrimRight(escaped, "/")
		if strings.HasSuffix(next, "/chat/completions") {
			next = strings.TrimSuffix(next, "/chat/completions")
			next = strings.TrimRight(next, "/")
		}
		if next == escaped {
			break
		}
		escaped = next
	}
	// escaped 未变化时原样返回，保留原始编码/格式（绝大多数无后缀的 base URL 命中此分支，零副作用）。
	if escaped == escapedOrig {
		return s
	}
	// 重建 URL：解码新 escaped 为 Path，并显式设 RawPath 保留转义序列。
	// escaped 由剥离字面子串得来，仍是合法编码（剥离不影响转义完整性），
	// PathUnescape 理论上不会失败；失败时保守返回原值（不死守重建）。
	newPath, unescErr := url.PathUnescape(escaped)
	if unescErr != nil {
		return s
	}
	parsed.Path = newPath
	parsed.RawPath = escaped
	return parsed.String()
}

// IsOfficialOpenAIBaseURL 判断 baseURL 是否指向官方 OpenAI API。
//
// 使用 net/url 解析后比较 Hostname()，仅当 host 精确等于 api.openai.com
// （大小写不敏感）时返回 true，避免 strings.Contains 子串匹配被欺骗性 host
// 误判（如 https://api.openai.com.evil.example/v1 或
// https://gateway.example/proxy/api.openai.com/v1 会被旧实现当成官方 OpenAI，
// 导致 provider ID 被改为内置 "openai" 且不注入 npm，第三方配置失效）。
//
// 对无 scheme 的输入补 https:// 后再解析；解析失败或 host 缺失时保守返回
// false（按第三方处理）。launcher 与 config 迁移共用本判定，保证语义一致
// （审核 Major-4）。
//
// FQDN 尾点 DNS 等价处理：https://api.openai.com./v1 的 Hostname() 为
// "api.openai.com."（含单个尾点），与无尾点域名 DNS 等价，应识别为官方
// （增量复审 Minor）。userinfo 不参与 host 比较（Hostname() 已排除）：
// https://user@api.openai.com/v1 判官方，https://api.openai.com@evil.example/v1
// 因 Hostname() 为 evil.example 而判第三方。
func IsOfficialOpenAIBaseURL(baseURL string) bool {
	s := strings.TrimSpace(baseURL)
	if s == "" {
		return false
	}
	parseTarget := s
	if !strings.Contains(parseTarget, "://") {
		parseTarget = "https://" + parseTarget
	}
	parsed, err := url.Parse(parseTarget)
	if err != nil || parsed.Host == "" {
		return false
	}
	host := parsed.Hostname()
	host = strings.TrimSuffix(host, ".") // 剥离单个 DNS 尾点（FQDN 等价）
	return strings.EqualFold(host, "api.openai.com")
}

// EffectiveBaseURLRaw 返回指定格式或首选格式的原始 BaseURL（未经归一化）。
// format 为空时使用 PreferredFormat()。
//
// 供存储/导出路径（SyncLegacyFields、BuildExportProvider）使用，以保证用户
// 原始输入值不被归一化污染。运行时消费端（launcher / codex / pi）应使用
// EffectiveBaseURL。
func (p Provider) EffectiveBaseURLRaw(format string) string {
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
	return p.BaseURL // 旧顶层字段回退
}

// EffectiveBaseURL 返回指定格式或首选格式的有效 BaseURL（运行时消费用）。
// format 为空时使用 PreferredFormat()。
//
// 当生效格式为 openai 时，返回值经 NormalizeOpenAIBaseURL 归一化（剥离
// /chat/completions 后缀与尾斜杠）；anthropic 格式分支一律保持原样透传，
// 不做归一化。空 format 经 PreferredFormat() 推导，openai 兼容 provider 在
// 空 format 下同样走归一化。
//
// 注意：归一化仅用于运行时消费端。存储/导出必须保持用户原始输入值，使用
// EffectiveBaseURLRaw（见 SyncLegacyFields / BuildExportProvider）。
func (p Provider) EffectiveBaseURL(format string) string {
	if format == "" {
		format = p.PreferredFormat()
	}
	raw := p.EffectiveBaseURLRaw(format)
	// 仅 OpenAI 格式做 base URL 归一化；anthropic 端点语义保持原样。
	if strings.ToLower(format) == "openai" {
		return NormalizeOpenAIBaseURL(raw)
	}
	return raw
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

// EffectiveHeaders 返回指定格式或首选格式的自定义请求头（透传给 pi headers）。
// format 为空时使用 PreferredFormat()。未配置时返回 nil。
func (p Provider) EffectiveHeaders(format string) map[string]string {
	if format == "" {
		format = p.PreferredFormat()
	}
	switch strings.ToLower(format) {
	case "openai":
		if p.OpenAI != nil && len(p.OpenAI.Headers) > 0 {
			return p.OpenAI.Headers
		}
	case "anthropic":
		if p.Anthropic != nil && len(p.Anthropic.Headers) > 0 {
			return p.Anthropic.Headers
		}
	}
	return nil
}

// EffectiveAuthHeader 返回指定格式或首选格式的 authHeader 开关（透传给 pi authHeader）。
// format 为空时使用 PreferredFormat()。未配置时返回 nil（表示不覆盖 pi 默认行为）。
func (p Provider) EffectiveAuthHeader(format string) *bool {
	if format == "" {
		format = p.PreferredFormat()
	}
	switch strings.ToLower(format) {
	case "openai":
		if p.OpenAI != nil && p.OpenAI.AuthHeader != nil {
			return p.OpenAI.AuthHeader
		}
	case "anthropic":
		if p.Anthropic != nil && p.Anthropic.AuthHeader != nil {
			return p.Anthropic.AuthHeader
		}
	}
	return nil
}

// IsOAuthMode 返回 Provider 是否使用 OAuth 认证（Anthropic 官方）。
func (p Provider) IsOAuthMode() bool {
	return p.EffectiveAuthKey("anthropic") == AuthTypeOAuth
}

// SyncLegacyFields 将新格式字段同步回旧顶层字段 Type/BaseURL/AuthKey，
// 以便仍依赖旧字段的代码路径能正常工作。
// 仅在新格式字段已建立时执行回填。
//
// 注意：BaseURL 使用 EffectiveBaseURLRaw（未归一化的原始值），保证存储到
// models.json 的 legacy base_url 与用户输入一致；归一化只发生在运行时
// 消费端（EffectiveBaseURL）。否则用户输入 .../v1/chat/completions 后，
// legacy 字段会被改写成 .../v1，破坏存储原样语义（审核 Major-2）。
func (p Provider) SyncLegacyFields() Provider {
	if p.Anthropic == nil && p.OpenAI == nil {
		return p
	}
	p.Type = p.EffectiveType()
	p.BaseURL = p.EffectiveBaseURLRaw("")
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
