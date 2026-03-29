package config

func boolPtr(v bool) *bool { return &v }

// DefaultConfig 返回默认配置（等价于源 models.json）。
func DefaultConfig() *AppConfig {
	return &AppConfig{
		Models: map[string]Provider{
			"anthropic": {
				BaseURL:      "https://api.anthropic.com",
				DefaultModel: "",
				AuthKey:      AuthTypeOAuth,
				Presets: map[string]Preset{
					"default": {
						Name:  "Default",
						Model: "",
					},
				},
			},
			"openai": {
				Type:         "openai",
				BaseURL:      "https://api.openai.com/v1",
				DefaultModel: "codex-mini-latest",
				AuthKey:      "OPENAI_API_KEY",
				Presets: map[string]Preset{
					"default": {
						Name:  "Codex Mini",
						Model: "codex-mini-latest",
					},
				},
			},
			"glm": {
				BaseURL:      "https://open.bigmodel.cn/api/anthropic",
				DefaultModel: "glm-5",
				AuthKey:      "ANTHROPIC_API_KEY",
				Presets: map[string]Preset{
					"default": {
						Name:  "GLM-5",
						Model: "glm-5",
						Parameters: Parameters{
							Thinking: &ThinkingConfig{Type: "enabled"},
							Stream:   boolPtr(true),
						},
					},
				},
			},
			"minimax": {
				BaseURL:      "https://api.minimaxi.com/anthropic",
				DefaultModel: "MiniMax-M2.5",
				AuthKey:      "ANTHROPIC_API_KEY",
				Presets: map[string]Preset{
					"default": {
						Name:  "MiniMax-M2.5",
						Model: "MiniMax-M2.5",
						Parameters: Parameters{
							Thinking: &ThinkingConfig{Type: "enabled"},
							Stream:   boolPtr(true),
						},
					},
				},
			},
			"kimi": {
				BaseURL:      "https://api.moonshot.cn/anthropic",
				DefaultModel: "kimi-k2.5",
				AuthKey:      "ANTHROPIC_API_KEY",
				Presets: map[string]Preset{
					"default": {
						Name:  "Kimi K2.5",
						Model: "kimi-k2.5",
						Parameters: Parameters{
							Thinking: &ThinkingConfig{Type: "enabled"},
							Stream:   boolPtr(true),
						},
					},
				},
			},
		},
		AgentTeams: AgentTeamsConfig{
			Enabled:      true,
			TeammateMode: "in-process",
		},
		Version: "1.0.1",
	}
}
