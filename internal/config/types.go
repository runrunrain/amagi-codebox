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
	Parameters     Parameters       `json:"parameters"`
	Target         PresetTargetType `json:"target,omitempty"`           // 目标 CLI 类型：codex（默认）或 opencode
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

// Provider 服务商配置
type Provider struct {
	Type         string            `json:"type,omitempty"` // "anthropic"（默认）或 "openai"
	BaseURL      string            `json:"base_url"`
	DefaultModel string            `json:"default_model"`
	AuthKey      string            `json:"auth_key"` // "ANTHROPIC_API_KEY" | "ANTHROPIC_AUTH_TOKEN" | "OAUTH" | "OPENAI_API_KEY"
	Presets      map[string]Preset `json:"presets"`
	UrlHistory   []string          `json:"url_history,omitempty"` // URL历史记录，最近使用的在前
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
	Models     map[string]Provider `json:"models"`
	AgentTeams AgentTeamsConfig    `json:"agent_teams"`
	Version    string              `json:"version"`
}

// ExportConfig 导出配置的根结构
type ExportConfig struct {
	Version    string                    `json:"version"`
	ExportedAt string                    `json:"exported_at"`
	Source     string                    `json:"source"`
	Providers  map[string]ExportProvider `json:"providers"`
	AgentTeams AgentTeamsConfig          `json:"agent_teams"`
}

// ExportProvider 导出时的提供商配置（含 API key 明文）
type ExportProvider struct {
	Type         string            `json:"type,omitempty"`
	BaseURL      string            `json:"base_url"`
	DefaultModel string            `json:"default_model"`
	AuthKey      string            `json:"auth_key"`
	APIKey       string            `json:"api_key"`
	Presets      map[string]Preset `json:"presets"`
}
