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

// DefaultPiPresets 返回 Pi 引擎的内置默认终端预设。
//
// Pi coding agent 无配置文件，纯靠进程环境变量 + CLI 参数驱动（--provider/--model），
// 因此每条 Pi 预设仅描述「关联 provider + 默认模型」。
// 覆盖全部内置 provider（anthropic/openai/glm/minimax/kimi），
// 启动时经 piProviderMapping 映射到 Pi 内置的 anthropic/openai provider。
//
// stable key = provider + "/default"，与各 provider 的 default 预设模型保持一致。
func DefaultPiPresets() map[string]TerminalPreset {
	return map[string]TerminalPreset{
		"anthropic/default": {
			Name:     "default",
			Provider: "anthropic",
			Model:    "", // 走 anthropic provider 默认（OAuth）
		},
		"openai/default": {
			Name:     "default",
			Provider: "openai",
			Model:    "codex-mini-latest",
		},
		"glm/default": {
			Name:     "default",
			Provider: "glm",
			Model:    "glm-5",
		},
		"minimax/default": {
			Name:     "default",
			Provider: "minimax",
			Model:    "MiniMax-M2.5",
		},
		"kimi/default": {
			Name:     "default",
			Provider: "kimi",
			Model:    "kimi-k2.5",
		},
	}
}

// DefaultOmpPresets 返回 Oh My Pi (omp) 引擎的内置默认终端预设。
//
// omp 与 pi 同构：同样的 --provider/--model/--thinking CLI 契约，models.yml
// 消费 Thinking/ReasoningEffort/ContextWindow/MaxTokens/PiCompat 参数。
// 因此逐条镜像 DefaultPiPresets() 的 5 条种子，覆盖全部内置 provider
// （anthropic/openai/glm/minimax/kimi），启动时经 ompProviderMapping 映射。
//
// stable key = provider + "/default"，与各 provider 的 default 预设模型保持一致。
func DefaultOmpPresets() map[string]TerminalPreset {
	return map[string]TerminalPreset{
		"anthropic/default": {
			Name:     "default",
			Provider: "anthropic",
			Model:    "", // 走 anthropic provider 默认（OAuth）
		},
		"openai/default": {
			Name:     "default",
			Provider: "openai",
			Model:    "codex-mini-latest",
		},
		"glm/default": {
			Name:     "default",
			Provider: "glm",
			Model:    "glm-5",
		},
		"minimax/default": {
			Name:     "default",
			Provider: "minimax",
			Model:    "MiniMax-M2.5",
		},
		"kimi/default": {
			Name:     "default",
			Provider: "kimi",
			Model:    "kimi-k2.5",
		},
	}
}

// DefaultTerminalPresets 返回内置默认终端预设容器。
// 当前仅 Pi/omp 引擎有内置种子；ClaudeCode/OpenCode/Codex 保持 nil（由迁移或用户填充）。
func DefaultTerminalPresets() *TerminalPresetsConfig {
	return &TerminalPresetsConfig{
		Pi:  DefaultPiPresets(),
		Omp: DefaultOmpPresets(),
	}
}
