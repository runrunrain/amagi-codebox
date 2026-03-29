package config

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

// Preset 预设配置
type Preset struct {
	Name       string     `json:"name"`
	Model      string     `json:"model"`
	Parameters Parameters `json:"parameters"`
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
