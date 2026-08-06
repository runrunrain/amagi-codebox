package launcher

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
)

// --- Preset Target 兼容性测试 ---

func TestPresetGetTargetDefaultsToCodex(t *testing.T) {
	// 旧 preset 没有 target 字段时，默认按 codex 处理
	preset := config.Preset{
		Name:  "Default",
		Model: "gpt-5",
	}
	if preset.GetTarget() != config.PresetTargetCodex {
		t.Fatalf("empty target should default to codex, got %q", preset.GetTarget())
	}
	if !preset.IsCodexTarget() {
		t.Fatal("empty target should be treated as codex target")
	}
	if preset.IsOpenCodeTarget() {
		t.Fatal("empty target should not be treated as opencode target")
	}
}

func TestPresetExplicitCodexTarget(t *testing.T) {
	preset := config.Preset{
		Name:   "Codex Preset",
		Model:  "codex-mini-latest",
		Target: config.PresetTargetCodex,
	}
	if !preset.IsCodexTarget() {
		t.Fatal("explicit codex target should be codex")
	}
	if preset.IsOpenCodeTarget() {
		t.Fatal("explicit codex target should not be opencode")
	}
}

func TestPresetOpenCodeTarget(t *testing.T) {
	preset := config.Preset{
		Name:   "OpenCode Preset",
		Model:  "claude-sonnet-4-5",
		Target: config.PresetTargetOpenCode,
	}
	if !preset.IsOpenCodeTarget() {
		t.Fatal("explicit opencode target should be opencode")
	}
	if preset.IsCodexTarget() {
		t.Fatal("explicit opencode target should not be codex")
	}
}

// --- JSON 序列化/反序列化测试 ---

func TestPresetJSONRoundTripWithTarget(t *testing.T) {
	original := config.Preset{
		Name:   "Test",
		Model:  "gpt-5",
		Target: config.PresetTargetOpenCode,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded config.Preset
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Target != config.PresetTargetOpenCode {
		t.Fatalf("target = %q, want %q", decoded.Target, config.PresetTargetOpenCode)
	}
}

func TestPresetJSONRoundTripWithoutTarget(t *testing.T) {
	// 旧 preset JSON 没有 target 字段
	jsonStr := `{"name":"Legacy","model":"gpt-5"}`
	var preset config.Preset
	if err := json.Unmarshal([]byte(jsonStr), &preset); err != nil {
		t.Fatalf("unmarshal legacy preset: %v", err)
	}
	if preset.Target != "" {
		t.Fatalf("target should be empty for legacy preset, got %q", preset.Target)
	}
	// GetTarget 仍然返回 codex
	if preset.GetTarget() != config.PresetTargetCodex {
		t.Fatalf("GetTarget = %q, want codex", preset.GetTarget())
	}
}

func TestPresetOpenCodeConfigPreservation(t *testing.T) {
	// opencode_config 中的未知字段应该原样保留
	originalConfig := `{
		"model": "anthropic/claude-sonnet-4-5",
		"provider": {
			"custom": {
				"options": {"apiKey": "sk-test"}
			}
		},
		"mcp": {
			"my-server": {
				"type": "remote",
				"url": "https://example.com/mcp"
			}
		},
		"custom_unknown_field": "preserved",
		"agent": {
			"reviewer": {
				"description": "Review code"
			}
		}
	}`
	preset := config.Preset{
		Name:           "OC Test",
		Model:          "claude-sonnet-4-5",
		Target:         config.PresetTargetOpenCode,
		OpenCodeConfig: json.RawMessage(originalConfig),
	}

	// 序列化/反序列化 round-trip
	data, err := json.Marshal(preset)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded config.Preset
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 验证原始 JSON 内容被保留
	var configMap map[string]any
	if err := json.Unmarshal(decoded.OpenCodeConfig, &configMap); err != nil {
		t.Fatalf("unmarshal opencode_config: %v", err)
	}

	// 验证关键字段
	if configMap["custom_unknown_field"] != "preserved" {
		t.Fatal("custom_unknown_field should be preserved")
	}
	mcp, ok := configMap["mcp"].(map[string]any)
	if !ok {
		t.Fatal("mcp should be a map")
	}
	if _, ok := mcp["my-server"]; !ok {
		t.Fatal("mcp.my-server should be preserved")
	}
	agent, ok := configMap["agent"].(map[string]any)
	if !ok {
		t.Fatal("agent should be a map")
	}
	reviewer, ok := agent["reviewer"].(map[string]any)
	if !ok || reviewer["description"] != "Review code" {
		t.Fatal("agent.reviewer.description should be preserved")
	}
}

// --- BuildOpenCodeRuntimeConfig 测试 ---

func TestBuildOpenCodeRuntimeConfigOpenAIProvider(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"default": {
				Name:  "GPT-5",
				Model: "gpt-5",
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("openai", provider, "default", "sk-test-key")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	// 验证 model 字段
	model, _ := cfg["model"].(string)
	if model != "openai/gpt-5" {
		t.Fatalf("model = %q, want %q", model, "openai/gpt-5")
	}

	// 验证 provider 配置
	providerSection, _ := cfg["provider"].(map[string]any)
	openaiProvider, _ := providerSection["openai"].(map[string]any)
	if openaiProvider == nil {
		t.Fatal("provider.openai should exist")
	}

	// 验证 apiKey
	options, _ := openaiProvider["options"].(map[string]any)
	if options == nil {
		t.Fatal("provider.openai.options should exist")
	}
	if options["apiKey"] != "sk-test-key" {
		t.Fatalf("apiKey = %v, want sk-test-key", options["apiKey"])
	}
}

func TestBuildOpenCodeRuntimeConfigOpenAIProviderIncludesOrganization(t *testing.T) {
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{
			Enabled:      true,
			BaseURL:      "https://api.openai.com/v1",
			Organization: "org-test",
			AuthKey:      "OPENAI_API_KEY",
		},
		DefaultModel: "gpt-5",
	}

	cfg, err := BuildOpenCodeRuntimeConfig("openai", provider, "", "sk-test-key")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	providerSection, _ := cfg["provider"].(map[string]any)
	openaiProvider, _ := providerSection["openai"].(map[string]any)
	options, _ := openaiProvider["options"].(map[string]any)
	if options["organization"] != "org-test" {
		t.Fatalf("organization = %v, want org-test", options["organization"])
	}
}

func TestBuildOpenCodeRuntimeConfigAnthropicProvider(t *testing.T) {
	provider := config.Provider{
		BaseURL:      "https://api.anthropic.com",
		DefaultModel: "claude-sonnet-4-5",
		AuthKey:      "ANTHROPIC_API_KEY",
		Presets: map[string]config.Preset{
			"default": {
				Name:  "Claude",
				Model: "claude-sonnet-4-5",
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("anthropic", provider, "default", "sk-ant-key")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	model, _ := cfg["model"].(string)
	if model != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("model = %q, want %q", model, "anthropic/claude-sonnet-4-5")
	}

	providerSection, _ := cfg["provider"].(map[string]any)
	anthropicProvider, _ := providerSection["anthropic"].(map[string]any)
	if anthropicProvider == nil {
		t.Fatal("provider.anthropic should exist")
	}
}

func TestBuildOpenCodeRuntimeConfigThirdPartyOpenAI(t *testing.T) {
	// 第三方 OpenAI 兼容提供商
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://custom.api.com/v1",
		DefaultModel: "custom-model",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"default": {
				Name:  "Custom",
				Model: "custom-model",
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("custom-provider", provider, "default", "sk-custom")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	// 第三方 OpenAI 兼容使用 providerName 作为 OpenCode provider ID
	model, _ := cfg["model"].(string)
	if model != "custom-provider/custom-model" {
		t.Fatalf("model = %q, want %q", model, "custom-provider/custom-model")
	}

	providerSection, _ := cfg["provider"].(map[string]any)
	customProv, _ := providerSection["custom-provider"].(map[string]any)
	if customProv == nil {
		t.Fatal("provider.custom-provider should exist")
	}

	// 第三方需要 baseURL
	options, _ := customProv["options"].(map[string]any)
	if options["baseURL"] != "https://custom.api.com/v1" {
		t.Fatalf("baseURL = %v, want https://custom.api.com/v1", options["baseURL"])
	}
}

func TestBuildOpenCodeRuntimeConfigThirdPartyAnthropic(t *testing.T) {
	// 第三方 Anthropic 兼容提供商
	provider := config.Provider{
		BaseURL:      "https://custom.anthropic.api",
		DefaultModel: "custom-llm",
		AuthKey:      "ANTHROPIC_API_KEY",
		Presets: map[string]config.Preset{
			"default": {
				Name:  "Custom Anthropic",
				Model: "custom-llm",
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("custom-anthropic", provider, "default", "sk-custom")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	model, _ := cfg["model"].(string)
	if model != "custom-anthropic/custom-llm" {
		t.Fatalf("model = %q, want %q", model, "custom-anthropic/custom-llm")
	}

	providerSection, _ := cfg["provider"].(map[string]any)
	customProv, _ := providerSection["custom-anthropic"].(map[string]any)
	if customProv == nil {
		t.Fatal("provider.custom-anthropic should exist")
	}

	// 第三方 Anthropic 兼容需要 baseURL
	options, _ := customProv["options"].(map[string]any)
	if options["baseURL"] != "https://custom.anthropic.api" {
		t.Fatalf("baseURL = %v, want https://custom.anthropic.api", options["baseURL"])
	}
}

// --- OPENCODE_CONFIG_CONTENT 生成测试 ---

func TestBuildOpenCodeEnvOverridesGeneratesConfigContent(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"default": {
				Name:  "GPT-5",
				Model: "gpt-5",
			},
		},
	}

	overrides, err := BuildOpenCodeEnvOverrides("openai", provider, "default", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverrides: %v", err)
	}

	configContent := overrides["OPENCODE_CONFIG_CONTENT"]
	if configContent == "" {
		t.Fatal("OPENCODE_CONFIG_CONTENT should not be empty")
	}

	// 验证是合法 JSON
	var cfg map[string]any
	if err := json.Unmarshal([]byte(configContent), &cfg); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not valid JSON: %v\ncontent: %s", err, configContent)
	}

	// 验证关键内容
	if cfg["model"] != "openai/gpt-5" {
		t.Fatalf("model = %v, want openai/gpt-5", cfg["model"])
	}

	// 验证 API Key 环境变量也设置了
	if overrides["OPENAI_API_KEY"] != "sk-test" {
		t.Fatalf("OPENAI_API_KEY = %q, want sk-test", overrides["OPENAI_API_KEY"])
	}
}

func TestBuildOpenCodeEnvOverridesAnthropicSetsEnvVar(t *testing.T) {
	provider := config.Provider{
		BaseURL:      "https://api.anthropic.com",
		DefaultModel: "claude-sonnet-4-5",
		AuthKey:      "ANTHROPIC_API_KEY",
		Presets: map[string]config.Preset{
			"default": {
				Name:  "Claude",
				Model: "claude-sonnet-4-5",
			},
		},
	}

	overrides, err := BuildOpenCodeEnvOverrides("anthropic", provider, "default", "sk-ant")
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverrides: %v", err)
	}

	if overrides["ANTHROPIC_API_KEY"] != "sk-ant" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want sk-ant", overrides["ANTHROPIC_API_KEY"])
	}

	configContent := overrides["OPENCODE_CONFIG_CONTENT"]
	if configContent == "" {
		t.Fatal("OPENCODE_CONFIG_CONTENT should not be empty")
	}
}

// --- 深度合并测试 ---

func TestBuildOpenCodeRuntimeConfigDeepMergeWithOpenCodeConfig(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"advanced": {
				Name:   "Advanced",
				Model:  "gpt-5",
				Target: config.PresetTargetOpenCode,
				OpenCodeConfig: json.RawMessage(`{
					"model": "openai/gpt-5-high",
					"autoupdate": false,
					"theme": "dark",
					"provider": {
						"openai": {
							"options": {
								"timeout": 600000
							}
						}
					},
					"mcp": {
						"filesystem": {
							"type": "remote",
							"url": "https://example.com/mcp"
						}
					}
				}`),
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("openai", provider, "advanced", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	// opencode_config 中的 model 覆盖了默认值
	model, _ := cfg["model"].(string)
	if model != "openai/gpt-5-high" {
		t.Fatalf("model = %q, want %q (opencode_config should override)", model, "openai/gpt-5-high")
	}

	// opencode_config 中的顶层字段保留
	if cfg["autoupdate"] != false {
		t.Fatal("autoupdate should be false from opencode_config")
	}
	if cfg["theme"] != "dark" {
		t.Fatal("theme should be dark from opencode_config")
	}

	// provider 深度合并：apiKey 保留，timeout 被添加
	providerSection, _ := cfg["provider"].(map[string]any)
	openaiProvider, _ := providerSection["openai"].(map[string]any)
	options, _ := openaiProvider["options"].(map[string]any)
	if options["apiKey"] != "sk-test" {
		t.Fatalf("apiKey should be preserved after deep merge, got %v", options["apiKey"])
	}
	if options["timeout"] != float64(600000) {
		t.Fatalf("timeout should be merged from opencode_config, got %v", options["timeout"])
	}

	// MCP 配置保留
	mcp, _ := cfg["mcp"].(map[string]any)
	if mcp == nil {
		t.Fatal("mcp should be preserved from opencode_config")
	}
	fs, _ := mcp["filesystem"].(map[string]any)
	if fs == nil {
		t.Fatal("mcp.filesystem should be preserved")
	}
}

func TestBuildOpenCodeRuntimeConfigOpenCodeConfigPreservesUnknownFields(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"custom": {
				Name:   "Custom",
				Model:  "gpt-5",
				Target: config.PresetTargetOpenCode,
				OpenCodeConfig: json.RawMessage(`{
					"custom_field_1": "value1",
					"nested": {
						"deep": {
							"key": "value"
						}
					},
					"permission": {
						"edit": "ask",
						"bash": "ask"
					}
				}`),
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("openai", provider, "custom", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	// 未知顶层字段保留
	if cfg["custom_field_1"] != "value1" {
		t.Fatal("custom_field_1 should be preserved")
	}

	// 嵌套未知字段保留
	nested, _ := cfg["nested"].(map[string]any)
	deep, _ := nested["deep"].(map[string]any)
	if deep["key"] != "value" {
		t.Fatal("nested.deep.key should be preserved")
	}

	// permission 保留
	permission, _ := cfg["permission"].(map[string]any)
	if permission["edit"] != "ask" || permission["bash"] != "ask" {
		t.Fatal("permission settings should be preserved")
	}
}

// --- Thinking 参数传递测试 ---

func TestBuildOpenCodeRuntimeConfigWithThinkingParams(t *testing.T) {
	provider := config.Provider{
		BaseURL:      "https://api.anthropic.com",
		DefaultModel: "claude-sonnet-4-5",
		AuthKey:      "ANTHROPIC_API_KEY",
		Presets: map[string]config.Preset{
			"thinking": {
				Name:  "Thinking",
				Model: "claude-sonnet-4-5",
				Parameters: config.Parameters{
					Thinking: &config.ThinkingConfig{
						Type:         "enabled",
						BudgetTokens: 16000,
					},
					Temperature: 0.7,
				},
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("anthropic", provider, "thinking", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	providerSection, _ := cfg["provider"].(map[string]any)
	anthropicProvider, _ := providerSection["anthropic"].(map[string]any)
	models, _ := anthropicProvider["models"].(map[string]any)
	claudeModel, _ := models["claude-sonnet-4-5"].(map[string]any)
	options, _ := claudeModel["options"].(map[string]any)

	thinking, _ := options["thinking"].(map[string]any)
	if thinking["type"] != "enabled" {
		t.Fatalf("thinking.type = %v, want enabled", thinking["type"])
	}
	// budgetTokens is set as int in buildOpenCodeModelOptions
	budgetTokens, ok := thinking["budgetTokens"].(int)
	if !ok || budgetTokens != 16000 {
		t.Fatalf("thinking.budgetTokens = %v (%T), want 16000", thinking["budgetTokens"], thinking["budgetTokens"])
	}
	// temperature is set as float64
	if temp, ok := options["temperature"].(float64); !ok || temp != 0.7 {
		t.Fatalf("temperature = %v, want 0.7", options["temperature"])
	}
}

// --- 无 preset 时的行为测试 ---

func TestBuildOpenCodeRuntimeConfigWithoutPreset(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets:      map[string]config.Preset{},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("openai", provider, "nonexistent", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	// 使用 provider.DefaultModel
	model, _ := cfg["model"].(string)
	if model != "openai/gpt-5" {
		t.Fatalf("model = %q, want %q", model, "openai/gpt-5")
	}
}

// --- deriveOpenCodeProviderID 测试 ---

func TestDeriveOpenCodeProviderID(t *testing.T) {
	tests := []struct {
		name         string
		providerName string
		provider     config.Provider
		want         string
	}{
		{
			name:         "OpenAI official",
			providerName: "openai",
			provider:     config.Provider{Type: "openai", BaseURL: "https://api.openai.com/v1", AuthKey: "OPENAI_API_KEY"},
			want:         "openai",
		},
		{
			name:         "Third-party OpenAI compatible",
			providerName: "deepseek",
			provider:     config.Provider{Type: "openai", BaseURL: "https://api.deepseek.com/v1", AuthKey: "OPENAI_API_KEY"},
			want:         "deepseek",
		},
		{
			name:         "Anthropic official",
			providerName: "anthropic",
			provider:     config.Provider{BaseURL: "https://api.anthropic.com", AuthKey: "ANTHROPIC_API_KEY"},
			want:         "anthropic",
		},
		{
			name:         "Third-party Anthropic compatible",
			providerName: "glm",
			provider:     config.Provider{BaseURL: "https://open.bigmodel.cn/api/anthropic", AuthKey: "ANTHROPIC_API_KEY"},
			want:         "glm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveOpenCodeProviderID(tt.providerName, tt.provider)
			if got != tt.want {
				t.Fatalf("deriveOpenCodeProviderID = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- deepMerge 测试 ---

func TestDeepMerge(t *testing.T) {
	tests := []struct {
		name  string
		base  map[string]any
		other map[string]any
		want  map[string]any
	}{
		{
			name:  "empty maps",
			base:  map[string]any{},
			other: map[string]any{},
			want:  map[string]any{},
		},
		{
			name:  "other adds new keys",
			base:  map[string]any{"a": 1},
			other: map[string]any{"b": 2},
			want:  map[string]any{"a": 1, "b": 2},
		},
		{
			name:  "other overrides base",
			base:  map[string]any{"a": 1},
			other: map[string]any{"a": 2},
			want:  map[string]any{"a": 2},
		},
		{
			name: "recursive merge nested maps",
			base: map[string]any{
				"provider": map[string]any{
					"openai": map[string]any{
						"options": map[string]any{
							"apiKey": "base-key",
						},
					},
				},
			},
			other: map[string]any{
				"provider": map[string]any{
					"openai": map[string]any{
						"options": map[string]any{
							"timeout": float64(600000),
						},
					},
				},
			},
			want: map[string]any{
				"provider": map[string]any{
					"openai": map[string]any{
						"options": map[string]any{
							"apiKey":  "base-key",
							"timeout": float64(600000),
						},
					},
				},
			},
		},
		{
			name: "other overrides nested value",
			base: map[string]any{
				"provider": map[string]any{
					"openai": map[string]any{
						"options": map[string]any{
							"apiKey": "base-key",
						},
					},
				},
			},
			other: map[string]any{
				"provider": map[string]any{
					"openai": map[string]any{
						"options": map[string]any{
							"apiKey": "override-key",
						},
					},
				},
			},
			want: map[string]any{
				"provider": map[string]any{
					"openai": map[string]any{
						"options": map[string]any{
							"apiKey": "override-key",
						},
					},
				},
			},
		},
		{
			name: "non-map value replaces map",
			base: map[string]any{
				"model": map[string]any{"a": 1},
			},
			other: map[string]any{
				"model": "simple-string",
			},
			want: map[string]any{
				"model": "simple-string",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deepMerge(tt.base, tt.other)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("deepMerge result:\ngot:  %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

// --- OPENCODE_CONFIG_CONTENT 优先级验证 ---

func TestBuildOpenCodeEnvOverridesConfigContentIsHighestPriority(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"default": {
				Name:  "GPT-5",
				Model: "gpt-5",
			},
		},
	}

	overrides, err := BuildOpenCodeEnvOverrides("openai", provider, "default", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverrides: %v", err)
	}

	// OPENCODE_CONFIG_CONTENT 必须存在且是合法 JSON
	content := overrides["OPENCODE_CONFIG_CONTENT"]
	if content == "" {
		t.Fatal("OPENCODE_CONFIG_CONTENT must be present")
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT must be valid JSON: %v", err)
	}

	// 内容必须包含 model 和 provider
	if _, ok := cfg["model"]; !ok {
		t.Fatal("OPENCODE_CONFIG_CONTENT must contain model")
	}
	if _, ok := cfg["provider"]; !ok {
		t.Fatal("OPENCODE_CONFIG_CONTENT must contain provider")
	}
}

// --- 确保 Codex 行为不受影响 ---

func TestPresetTargetDoesNotAffectCodexDefaults(t *testing.T) {
	// 默认配置中所有 preset 都是 codex target
	defaultCfg := config.DefaultConfig()
	for provName, prov := range defaultCfg.Models {
		for presetName, preset := range prov.Presets {
			if !preset.IsCodexTarget() {
				t.Fatalf("default preset %s/%s should be codex target, got %q", provName, presetName, preset.GetTarget())
			}
		}
	}
}

func TestPresetJSONWithoutTargetFieldsDeserializesCorrectly(t *testing.T) {
	// 模拟旧格式的 models.json
	oldJSON := `{
		"models": {
			"openai": {
				"type": "openai",
				"base_url": "https://api.openai.com/v1",
				"default_model": "codex-mini-latest",
				"auth_key": "OPENAI_API_KEY",
				"presets": {
					"default": {
						"name": "Codex Mini",
						"model": "codex-mini-latest"
					}
				}
			}
		}
	}`

	var cfg config.AppConfig
	if err := json.Unmarshal([]byte(oldJSON), &cfg); err != nil {
		t.Fatalf("unmarshal legacy config: %v", err)
	}

	openai := cfg.Models["openai"]
	preset := openai.Presets["default"]
	if preset.Target != "" {
		t.Fatalf("legacy preset target should be empty, got %q", preset.Target)
	}
	if preset.GetTarget() != config.PresetTargetCodex {
		t.Fatalf("legacy preset should default to codex target")
	}
	// OpenCodeConfig 应该为 nil
	if preset.OpenCodeConfig != nil {
		t.Fatal("legacy preset should not have opencode_config")
	}
}

// --- OpenCode provider ID 大小写不敏感测试 ---

func TestDeriveOpenCodeProviderIDCaseInsensitive(t *testing.T) {
	tests := []struct {
		name         string
		providerName string
		provider     config.Provider
		want         string
	}{
		{
			name:         "OpenAI type uppercase",
			providerName: "my-openai",
			provider:     config.Provider{Type: "OpenAI", BaseURL: "https://api.openai.com/v1", AuthKey: "OPENAI_API_KEY"},
			want:         "openai",
		},
		{
			name:         "empty type with OPENAI_API_KEY",
			providerName: "my-provider",
			provider:     config.Provider{Type: "", BaseURL: "https://api.openai.com/v1", AuthKey: "OPENAI_API_KEY"},
			want:         "openai",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveOpenCodeProviderID(tt.providerName, tt.provider)
			if got != tt.want {
				t.Fatalf("deriveOpenCodeProviderID = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- 确保 OPENCODE_CONFIG_CONTENT 不使用路径层 ---

func TestBuildOpenCodeEnvOverridesDoesNotSetConfigPath(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets:      map[string]config.Preset{},
	}

	overrides, err := BuildOpenCodeEnvOverrides("openai", provider, "", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverrides: %v", err)
	}

	// 不应设置 OPENCODE_CONFIG（路径层），只使用 OPENCODE_CONFIG_CONTENT
	if _, ok := overrides["OPENCODE_CONFIG"]; ok {
		t.Fatal("OPENCODE_CONFIG (path) should not be set, use OPENCODE_CONFIG_CONTENT instead")
	}
}

// --- 验证生成的 config JSON 格式正确 ---

func TestBuildOpenCodeRuntimeConfigProducesValidJSON(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"full": {
				Name:   "Full Config",
				Model:  "gpt-5",
				Target: config.PresetTargetOpenCode,
				Parameters: config.Parameters{
					Thinking:    &config.ThinkingConfig{Type: "enabled", BudgetTokens: 16000},
					Temperature: 0.7,
					TopP:        0.9,
					MaxTokens:   4096,
				},
				OpenCodeConfig: json.RawMessage(`{
					"theme": "dark",
					"autoupdate": false,
					"permission": {"edit": "ask"}
				}`),
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("openai", provider, "full", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	// 完整序列化
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	// 验证可以作为合法 JSON 反序列化
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("generated config is not valid JSON: %v\n%s", err, string(data))
	}

	// 验证核心结构
	if result["model"] != "openai/gpt-5" {
		t.Fatalf("model = %v, want openai/gpt-5", result["model"])
	}
	if result["theme"] != "dark" {
		t.Fatal("theme from opencode_config should be preserved")
	}
	if result["autoupdate"] != false {
		t.Fatal("autoupdate from opencode_config should be preserved")
	}

	t.Logf("Generated OpenCode config:\n%s", string(data))
}

// --- 确保 preset 不存在时不崩溃 ---

func TestBuildOpenCodeRuntimeConfigMissingPreset(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets:      map[string]config.Preset{},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("openai", provider, "nonexistent", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig with missing preset: %v", err)
	}

	// 应该使用 DefaultModel
	if cfg["model"] != "openai/gpt-5" {
		t.Fatalf("model = %v, want openai/gpt-5", cfg["model"])
	}
}

// --- 确保 apiKey 为空时不崩溃 ---

func TestBuildOpenCodeRuntimeConfigEmptyAPIKey(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets:      map[string]config.Preset{},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("openai", provider, "", "")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig with empty apiKey: %v", err)
	}

	// 不应崩溃
	if cfg["model"] != "openai/gpt-5" {
		t.Fatalf("model = %v, want openai/gpt-5", cfg["model"])
	}
}

// --- 确保 providerName 为空时不崩溃 ---

func TestBuildOpenCodeEnvOverridesNoProvider(t *testing.T) {
	overrides, err := BuildOpenCodeEnvOverrides("", config.Provider{}, "", "")
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverrides with empty provider: %v", err)
	}

	// 应该生成 config content（即使是空的）
	content := overrides["OPENCODE_CONFIG_CONTENT"]
	if content == "" {
		t.Fatal("OPENCODE_CONFIG_CONTENT should still be generated")
	}
}

// --- 确保 opencode_config 中的 provider 覆盖了自动生成的值 ---

func TestBuildOpenCodeRuntimeConfigOpenCodeConfigOverridesModel(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"override": {
				Name:  "Override",
				Model: "gpt-5",
				OpenCodeConfig: json.RawMessage(`{
					"model": "anthropic/claude-sonnet-4-5"
				}`),
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("openai", provider, "override", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	// opencode_config 的 model 应该覆盖自动生成的
	if cfg["model"] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("model = %v, want anthropic/claude-sonnet-4-5 (opencode_config override)", cfg["model"])
	}
}

// --- 验证 OPENCODE_CONFIG_CONTENT 是完整 JSON ---

func TestBuildOpenCodeEnvOverridesConfigContentIsComplete(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"default": {
				Name:  "GPT-5",
				Model: "gpt-5",
				OpenCodeConfig: json.RawMessage(`{
					"mcp": {
						"my-server": {"type": "remote", "url": "https://mcp.example.com"}
					},
					"agent": {
						"reviewer": {"description": "Code reviewer"}
					},
					"compaction": {"auto": true, "prune": true}
				}`),
			},
		},
	}

	overrides, err := BuildOpenCodeEnvOverrides("openai", provider, "default", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverrides: %v", err)
	}

	content := overrides["OPENCODE_CONFIG_CONTENT"]
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, content)
	}

	// 验证所有 opencode_config 字段都在
	if _, ok := cfg["mcp"]; !ok {
		t.Fatal("mcp should be present in config")
	}
	if _, ok := cfg["agent"]; !ok {
		t.Fatal("agent should be present in config")
	}
	if _, ok := cfg["compaction"]; !ok {
		t.Fatal("compaction should be present in config")
	}

	// 验证自动生成的字段也在
	if _, ok := cfg["model"]; !ok {
		t.Fatal("model should be present in config")
	}
	if _, ok := cfg["provider"]; !ok {
		t.Fatal("provider should be present in config")
	}
}

// --- 确保字符串比较工具函数 ---

func TestStringsInConfig(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets:      map[string]config.Preset{},
	}

	cfg, _ := BuildOpenCodeRuntimeConfig("openai", provider, "", "sk-test")
	data, _ := json.Marshal(cfg)
	s := string(data)

	// 确保不包含 OPENCODE_CONFIG 路径层关键字
	if strings.Contains(s, "OPENCODE_CONFIG") {
		t.Fatal("config content should not reference OPENCODE_CONFIG path env var")
	}
}

// ========================================================================
// 关键路径测试：模拟真实用户操作路径
// "编辑 preset -> 保存 -> 重读/反序列化 -> LaunchOpenCode 配置生成"
// ========================================================================

// TestPresetRoundTripViaConfigService 模拟完整的用户操作路径：
// 1. 创建 provider 并保存
// 2. 创建带 opencode_config 的 preset 并保存（通过 ConfigService）
// 3. 重新加载配置
// 4. 读取 preset，验证 opencode_config 保真
// 5. 用读取到的 preset 生成 OpenCode 运行时配置
func TestPresetRoundTripViaConfigService(t *testing.T) {
	// 准备临时配置目录
	configDir := t.TempDir()
	svc := config.NewConfigService(configDir)
	if err := svc.Load(); err != nil {
		t.Fatalf("initial load: %v", err)
	}

	// 1. 保存一个带 opencode_config 的 provider
	ocConfig := map[string]any{
		"model":      "openai/gpt-5-high",
		"autoupdate": false,
		"mcp": map[string]any{
			"filesystem": map[string]any{
				"type": "remote",
				"url":  "https://mcp.example.com",
			},
		},
		"permission": map[string]any{
			"edit": "ask",
		},
	}
	ocConfigJSON, err := json.Marshal(ocConfig)
	if err != nil {
		t.Fatalf("marshal opencode_config: %v", err)
	}

	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"opencode-pro": {
				Name:           "OpenCode Pro",
				Model:          "gpt-5",
				Target:         config.PresetTargetOpenCode,
				OpenCodeConfig: json.RawMessage(ocConfigJSON),
			},
		},
	}

	if err := svc.SaveProvider("test-openai", provider); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	// 2. 重新加载配置（模拟应用重启）
	svc2 := config.NewConfigService(configDir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// 3. 读取 provider 和 preset
	loadedProvider, err := svc2.GetProvider("test-openai")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	loadedPreset, ok := loadedProvider.Presets["opencode-pro"]
	if !ok {
		t.Fatal("preset 'opencode-pro' not found after reload")
	}

	// 4. 验证 opencode_config 保真
	if len(loadedPreset.OpenCodeConfig) == 0 {
		t.Fatal("opencode_config should be preserved after save/reload")
	}
	var reloadedConfig map[string]any
	if err := json.Unmarshal(loadedPreset.OpenCodeConfig, &reloadedConfig); err != nil {
		t.Fatalf("opencode_config is not valid JSON after reload: %v\nraw: %s", err, string(loadedPreset.OpenCodeConfig))
	}
	if reloadedConfig["model"] != "openai/gpt-5-high" {
		t.Fatalf("opencode_config.model = %v, want openai/gpt-5-high", reloadedConfig["model"])
	}
	if reloadedConfig["autoupdate"] != false {
		t.Fatal("opencode_config.autoupdate should be false")
	}
	mcp, _ := reloadedConfig["mcp"].(map[string]any)
	if mcp == nil {
		t.Fatal("opencode_config.mcp should be preserved")
	}

	// 5. 用读取到的 preset 生成 OpenCode 运行时配置
	runtimeCfg, err := BuildOpenCodeRuntimeConfig("test-openai", *loadedProvider, "opencode-pro", "sk-test-key")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	// opencode_config 中的 model 应覆盖自动生成的
	if runtimeCfg["model"] != "openai/gpt-5-high" {
		t.Fatalf("model = %v, want openai/gpt-5-high (opencode_config override)", runtimeCfg["model"])
	}
	// mcp 和 permission 应保留
	if _, ok := runtimeCfg["mcp"]; !ok {
		t.Fatal("mcp should be in runtime config")
	}
	if perm, _ := runtimeCfg["permission"].(map[string]any); perm == nil || perm["edit"] != "ask" {
		t.Fatal("permission.edit should be 'ask'")
	}
	// provider 配置应包含 apiKey
	providerSection, _ := runtimeCfg["provider"].(map[string]any)
	openaiProvider, _ := providerSection["openai"].(map[string]any)
	options, _ := openaiProvider["options"].(map[string]any)
	if options["apiKey"] != "sk-test-key" {
		t.Fatalf("apiKey = %v, want sk-test-key", options["apiKey"])
	}
}

// TestPresetDoubleEncodedStringNormalization 模拟前端双重编码场景：
// 前端把 opencode_config 作为 JS string 传回后端，Wails 序列化时
// json.RawMessage 收到的是带引号的 JSON 字符串（双重编码）。
// NormalizeOpenCodeConfig 应正确解包。
func TestPresetDoubleEncodedStringNormalization(t *testing.T) {
	originalJSON := `{"model":"openai/gpt-5","autoupdate":false}`

	// 模拟前端传回的双重编码：JS string -> JSON 序列化 -> json.RawMessage
	// 当前端设置 preset.opencode_config = '{"model":"openai/gpt-5"}'
	// Wails 序列化整个 Preset 对象时，字符串字段会变成 JSON string
	doubleEncoded, err := json.Marshal(originalJSON)
	if err != nil {
		t.Fatalf("marshal string: %v", err)
	}
	// doubleEncoded 现在是 "{\"model\":\"openai/gpt-5\",\"autoupdate\":false}"（带引号）

	preset := config.Preset{
		Name:           "Test",
		Model:          "gpt-5",
		Target:         config.PresetTargetOpenCode,
		OpenCodeConfig: json.RawMessage(doubleEncoded),
	}

	// 验证确实是双重编码的
	if len(preset.OpenCodeConfig) == 0 || preset.OpenCodeConfig[0] != '"' {
		t.Fatalf("test setup error: OpenCodeConfig should start with quote, got: %s", string(preset.OpenCodeConfig))
	}

	// 规范化
	preset.NormalizeOpenCodeConfig()

	// 验证解包后是合法 JSON 对象
	if len(preset.OpenCodeConfig) == 0 {
		t.Fatal("OpenCodeConfig should not be empty after normalization")
	}
	if preset.OpenCodeConfig[0] != '{' {
		t.Fatalf("OpenCodeConfig should start with '{' after normalization, got: %s", string(preset.OpenCodeConfig[:min(20, len(preset.OpenCodeConfig))]))
	}

	var cfg map[string]any
	if err := json.Unmarshal(preset.OpenCodeConfig, &cfg); err != nil {
		t.Fatalf("normalized OpenCodeConfig should be valid JSON: %v\nraw: %s", err, string(preset.OpenCodeConfig))
	}
	if cfg["model"] != "openai/gpt-5" {
		t.Fatalf("model = %v, want openai/gpt-5", cfg["model"])
	}
	if cfg["autoupdate"] != false {
		t.Fatal("autoupdate should be false")
	}
}

// TestPresetAlreadyNormalizedIsIdempotent 验证已正常编码的 opencode_config 不受影响
func TestPresetAlreadyNormalizedIsIdempotent(t *testing.T) {
	originalJSON := `{"model":"openai/gpt-5","theme":"dark"}`

	preset := config.Preset{
		Name:           "Test",
		Model:          "gpt-5",
		OpenCodeConfig: json.RawMessage(originalJSON),
	}

	preset.NormalizeOpenCodeConfig()

	if string(preset.OpenCodeConfig) != originalJSON {
		t.Fatalf("already-normalized config should be unchanged\ngot:  %s\nwant: %s", string(preset.OpenCodeConfig), originalJSON)
	}
}

// TestPresetEmptyOpenCodeConfigNormalization 验证空值不崩溃
func TestPresetEmptyOpenCodeConfigNormalization(t *testing.T) {
	preset := config.Preset{
		Name:           "Test",
		Model:          "gpt-5",
		OpenCodeConfig: nil,
	}
	preset.NormalizeOpenCodeConfig()
	if preset.OpenCodeConfig != nil {
		t.Fatal("nil OpenCodeConfig should remain nil")
	}

	preset.OpenCodeConfig = json.RawMessage("")
	preset.NormalizeOpenCodeConfig()
	if preset.OpenCodeConfig != nil {
		t.Fatal("empty OpenCodeConfig should become nil")
	}

	preset.OpenCodeConfig = json.RawMessage("  ")
	preset.NormalizeOpenCodeConfig()
	if preset.OpenCodeConfig != nil {
		t.Fatal("whitespace-only OpenCodeConfig should become nil")
	}
}

// TestFullRoundTripDoubleEncodedViaLauncher 模拟完整路径：
// 前端双重编码 -> ConfigService.SavePreset 规范化 -> 磁盘 -> 重新加载 -> LaunchOpenCode 配置生成
func TestFullRoundTripDoubleEncodedViaLauncher(t *testing.T) {
	configDir := t.TempDir()
	svc := config.NewConfigService(configDir)
	if err := svc.Load(); err != nil {
		t.Fatalf("initial load: %v", err)
	}

	// 先保存 provider
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets:      map[string]config.Preset{},
	}
	if err := svc.SaveProvider("test-openai", provider); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	// 模拟前端双重编码的 opencode_config
	originalMap := map[string]any{
		"model":      "openai/gpt-5-pro",
		"autoupdate": false,
		"mcp": map[string]any{
			"my-server": map[string]any{
				"type": "remote",
				"url":  "https://example.com/mcp",
			},
		},
	}
	originalJSON, _ := json.Marshal(originalMap)

	// 前端把字符串传给 Wails -> Wails 序列化为 JSON string -> json.RawMessage 收到双重编码
	doubleEncoded, _ := json.Marshal(string(originalJSON))

	// 保存 preset（模拟前端调用 SavePreset）
	preset := config.Preset{
		Name:           "OC Pro",
		Model:          "gpt-5",
		Target:         config.PresetTargetOpenCode,
		OpenCodeConfig: json.RawMessage(doubleEncoded),
	}
	if err := svc.SavePreset("test-openai", "oc-pro", preset); err != nil {
		t.Fatalf("SavePreset: %v", err)
	}

	// 重新加载（模拟应用重启）
	svc2 := config.NewConfigService(configDir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	loadedProvider, err := svc2.GetProvider("test-openai")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}

	// 生成 OpenCode 运行时配置
	overrides, err := BuildOpenCodeEnvOverrides("test-openai", *loadedProvider, "oc-pro", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverrides: %v", err)
	}

	configContent := overrides["OPENCODE_CONFIG_CONTENT"]
	var cfg map[string]any
	if err := json.Unmarshal([]byte(configContent), &cfg); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not valid JSON: %v\ncontent: %s", err, configContent)
	}

	// opencode_config 的 model 覆盖默认值
	if cfg["model"] != "openai/gpt-5-pro" {
		t.Fatalf("model = %v, want openai/gpt-5-pro", cfg["model"])
	}

	// MCP 配置保留
	mcp, _ := cfg["mcp"].(map[string]any)
	if mcp == nil {
		t.Fatal("mcp should be preserved in runtime config")
	}
	myServer, _ := mcp["my-server"].(map[string]any)
	if myServer == nil || myServer["url"] != "https://example.com/mcp" {
		t.Fatal("mcp.my-server.url should be preserved")
	}

	// autoupdate 保留
	if cfg["autoupdate"] != false {
		t.Fatal("autoupdate should be false")
	}

	// provider 配置存在
	providerSection, _ := cfg["provider"].(map[string]any)
	openaiProv, _ := providerSection["openai"].(map[string]any)
	options, _ := openaiProv["options"].(map[string]any)
	if options["apiKey"] != "sk-test" {
		t.Fatalf("apiKey should be preserved, got %v", options["apiKey"])
	}
}

// TestConfigServiceSavePresetNormalizesDoubleEncoded 验证 ConfigService.SavePreset
// 在保存前自动规范化双重编码的 opencode_config
func TestConfigServiceSavePresetNormalizesDoubleEncoded(t *testing.T) {
	configDir := t.TempDir()
	svc := config.NewConfigService(configDir)
	if err := svc.Load(); err != nil {
		t.Fatalf("initial load: %v", err)
	}

	// 保存 provider
	provider := config.Provider{
		Type:    "openai",
		BaseURL: "https://api.openai.com/v1",
		AuthKey: "OPENAI_API_KEY",
		Presets: map[string]config.Preset{},
	}
	if err := svc.SaveProvider("test", provider); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	// 双重编码的 opencode_config
	original := `{"theme":"dark"}`
	doubleEncoded, _ := json.Marshal(original)

	preset := config.Preset{
		Name:           "Test",
		Model:          "gpt-5",
		Target:         config.PresetTargetOpenCode,
		OpenCodeConfig: json.RawMessage(doubleEncoded),
	}
	if err := svc.SavePreset("test", "my-preset", preset); err != nil {
		t.Fatalf("SavePreset: %v", err)
	}

	// 重新加载验证磁盘上的值
	svc2 := config.NewConfigService(configDir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	p, err := svc2.GetProvider("test")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	saved := p.Presets["my-preset"]

	// 磁盘上应该是原始 JSON 对象，不是双重编码字符串
	if len(saved.OpenCodeConfig) == 0 {
		t.Fatal("opencode_config should not be empty")
	}
	if saved.OpenCodeConfig[0] == '"' {
		t.Fatalf("opencode_config on disk should be normalized (start with '{'), got: %s", string(saved.OpenCodeConfig))
	}

	var cfg map[string]any
	if err := json.Unmarshal(saved.OpenCodeConfig, &cfg); err != nil {
		t.Fatalf("normalized opencode_config should be valid JSON: %v\nraw: %s", err, string(saved.OpenCodeConfig))
	}
	if cfg["theme"] != "dark" {
		t.Fatalf("theme = %v, want dark", cfg["theme"])
	}
}

// ========================================================================
// P. BuildOpenCodeRuntimeConfigFromPreset -- 新模型运行时构建测试
// ========================================================================

// TestBuildOpenCodeRuntimeConfigFromPreset_InjectsAPIKey 验证新模型运行时构建
// 会注入 binding 对应 provider 的 secrets（apiKey）。
func TestBuildOpenCodeRuntimeConfigFromPreset_InjectsAPIKey(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "test-preset",
		Name: "Test Preset",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {
					"options": {
						"baseURL": "https://api.openai.com/v1"
					}
				}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"openai": {
				LocalProvider: "openai",
				Format:        "openai",
				Inject:        []string{"apiKey"},
			},
		},
	}

	// Mock getAPIKey function
	getAPIKey := func(providerName, format string) (string, error) {
		if providerName == "openai" {
			return "sk-test-injected-key", nil
		}
		return "", nil
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, nil)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	// 验证 apiKey 被注入
	providers, _ := cfg["provider"].(map[string]any)
	openaiProv, _ := providers["openai"].(map[string]any)
	options, _ := openaiProv["options"].(map[string]any)
	if options["apiKey"] != "sk-test-injected-key" {
		t.Fatalf("apiKey = %v, want sk-test-injected-key", options["apiKey"])
	}
	// baseURL 保留
	if options["baseURL"] != "https://api.openai.com/v1" {
		t.Fatalf("baseURL = %v, want https://api.openai.com/v1", options["baseURL"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_DoesNotMutatePreset 验证运行时构建
// 不修改 preset.Config 原始数据。
func TestBuildOpenCodeRuntimeConfigFromPreset_DoesNotMutatePreset(t *testing.T) {
	originalConfig := `{"model":"openai/gpt-5","provider":{"openai":{"options":{"baseURL":"https://api.openai.com/v1"}}}}`
	preset := config.OpenCodePreset{
		ID:     "test",
		Name:   "Test",
		Config: json.RawMessage(originalConfig),
		Bindings: map[string]config.OpenCodeBinding{
			"openai": {
				LocalProvider: "openai",
				Format:        "openai",
				Inject:        []string{"apiKey"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-runtime-key", nil
	}

	BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, nil)

	// 验证原始 Config 未被修改（不含 apiKey）
	var original map[string]any
	json.Unmarshal(preset.Config, &original)
	providers, _ := original["provider"].(map[string]any)
	openaiProv, _ := providers["openai"].(map[string]any)
	options, _ := openaiProv["options"].(map[string]any)
	if _, hasKey := options["apiKey"]; hasKey {
		t.Fatal("preset.Config should NOT be mutated by runtime builder")
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_MultipleBindings 验证多 binding 场景。
func TestBuildOpenCodeRuntimeConfigFromPreset_MultipleBindings(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "multi-bind",
		Name: "Multi Binding",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {
					"options": {"baseURL": "https://api.openai.com/v1"}
				},
				"anthropic": {
					"options": {"baseURL": "https://api.anthropic.com"}
				}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"openai": {
				LocalProvider: "my-openai",
				Format:        "openai",
				Inject:        []string{"apiKey"},
			},
			"anthropic": {
				LocalProvider: "my-anthropic",
				Format:        "anthropic",
				Inject:        []string{"apiKey"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		switch providerName {
		case "my-openai":
			return "sk-openai-123", nil
		case "my-anthropic":
			return "sk-ant-456", nil
		}
		return "", nil
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, nil)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)

	// openai binding
	openaiProv, _ := providers["openai"].(map[string]any)
	openaiOpts, _ := openaiProv["options"].(map[string]any)
	if openaiOpts["apiKey"] != "sk-openai-123" {
		t.Fatalf("openai apiKey = %v, want sk-openai-123", openaiOpts["apiKey"])
	}

	// anthropic binding
	anthProv, _ := providers["anthropic"].(map[string]any)
	anthOpts, _ := anthProv["options"].(map[string]any)
	if anthOpts["apiKey"] != "sk-ant-456" {
		t.Fatalf("anthropic apiKey = %v, want sk-ant-456", anthOpts["apiKey"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_EmptyConfig 验证空 Config 不崩溃。
func TestBuildOpenCodeRuntimeConfigFromPreset_EmptyConfig(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:       "empty",
		Name:     "Empty",
		Config:   nil,
		Bindings: map[string]config.OpenCodeBinding{},
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, nil, nil)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset with empty config: %v", err)
	}
	// 应有 provider 空节点
	if _, ok := cfg["provider"]; !ok {
		t.Fatal("expected provider key in result")
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_NilGetAPIKey 验证 getAPIKey 为 nil 不崩溃。
func TestBuildOpenCodeRuntimeConfigFromPreset_NilGetAPIKey(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "test",
		Name: "Test",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {
					"options": {"baseURL": "https://api.openai.com/v1"}
				}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"openai": {
				LocalProvider: "openai",
				Format:        "openai",
				Inject:        []string{"apiKey"},
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, nil, nil)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset with nil getAPIKey: %v", err)
	}

	// apiKey 不应被注入（getAPIKey 为 nil）
	providers, _ := cfg["provider"].(map[string]any)
	openaiProv, _ := providers["openai"].(map[string]any)
	options, _ := openaiProv["options"].(map[string]any)
	if _, hasKey := options["apiKey"]; hasKey {
		t.Fatal("apiKey should not be injected when getAPIKey is nil")
	}
}

// TestBuildOpenCodeEnvOverridesFromPreset 验证环境变量覆盖生成。
func TestBuildOpenCodeEnvOverridesFromPreset(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "env-test",
		Name: "Env Test",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {
					"options": {"baseURL": "https://api.openai.com/v1"}
				}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"openai": {
				LocalProvider: "openai",
				Format:        "openai",
				Inject:        []string{"apiKey"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		if providerName == "openai" {
			return "sk-env-test-key", nil
		}
		return "", nil
	}

	overrides, err := BuildOpenCodeEnvOverridesFromPreset(preset, getAPIKey, nil)
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverridesFromPreset: %v", err)
	}

	// OPENCODE_CONFIG_CONTENT 应存在且是合法 JSON
	content := overrides["OPENCODE_CONFIG_CONTENT"]
	if content == "" {
		t.Fatal("OPENCODE_CONFIG_CONTENT should not be empty")
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not valid JSON: %v", err)
	}

	// OPENAI_API_KEY 环境变量应设置
	if overrides["OPENAI_API_KEY"] != "sk-env-test-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want sk-env-test-key", overrides["OPENAI_API_KEY"])
	}

	// config content 中应包含注入的 apiKey
	providers, _ := cfg["provider"].(map[string]any)
	openaiProv, _ := providers["openai"].(map[string]any)
	options, _ := openaiProv["options"].(map[string]any)
	if options["apiKey"] != "sk-env-test-key" {
		t.Fatalf("apiKey in config = %v, want sk-env-test-key", options["apiKey"])
	}
}

// ========================================================================
// Q. resolveBindingFormat + inject 真实语义测试
// ========================================================================

// TestResolveBindingFormat_ExplicitOpenAI 验证显式 format=openai 直接返回。
func TestResolveBindingFormat_ExplicitOpenAI(t *testing.T) {
	binding := config.OpenCodeBinding{Format: "openai"}
	got := resolveBindingFormat(binding, nil)
	if got != "openai" {
		t.Fatalf("explicit openai: got %q, want openai", got)
	}
}

// TestResolveBindingFormat_ExplicitAnthropic 验证显式 format=anthropic 直接返回。
func TestResolveBindingFormat_ExplicitAnthropic(t *testing.T) {
	binding := config.OpenCodeBinding{Format: "anthropic"}
	got := resolveBindingFormat(binding, nil)
	if got != "anthropic" {
		t.Fatalf("explicit anthropic: got %q, want anthropic", got)
	}
}

// TestResolveBindingFormat_AutoWithOpenAIProvider 验证 format=auto + OpenAI provider -> openai。
func TestResolveBindingFormat_AutoWithOpenAIProvider(t *testing.T) {
	binding := config.OpenCodeBinding{Format: "auto"}
	provider := &config.Provider{
		OpenAI: &config.OpenAIFormat{
			Enabled: true,
			BaseURL: "https://api.openai.com/v1",
		},
	}
	got := resolveBindingFormat(binding, provider)
	if got != "openai" {
		t.Fatalf("auto + openai provider: got %q, want openai", got)
	}
}

// TestResolveBindingFormat_AutoWithAnthropicProvider 验证 format=auto + Anthropic provider -> anthropic。
func TestResolveBindingFormat_AutoWithAnthropicProvider(t *testing.T) {
	binding := config.OpenCodeBinding{Format: "auto"}
	provider := &config.Provider{
		Anthropic: &config.AnthropicFormat{
			Enabled: true,
			BaseURL: "https://api.anthropic.com",
		},
	}
	got := resolveBindingFormat(binding, provider)
	if got != "anthropic" {
		t.Fatalf("auto + anthropic provider: got %q, want anthropic", got)
	}
}

// TestResolveBindingFormat_EmptyWithOpenAIProvider 验证 format="" + OpenAI provider -> openai。
func TestResolveBindingFormat_EmptyWithOpenAIProvider(t *testing.T) {
	binding := config.OpenCodeBinding{Format: ""}
	provider := &config.Provider{
		Type:    "openai",
		AuthKey: "OPENAI_API_KEY",
	}
	got := resolveBindingFormat(binding, provider)
	if got != "openai" {
		t.Fatalf("empty format + openai provider: got %q, want openai", got)
	}
}

// TestResolveBindingFormat_NilProvider 退回到 anthropic。
func TestResolveBindingFormat_NilProvider(t *testing.T) {
	binding := config.OpenCodeBinding{Format: "auto"}
	got := resolveBindingFormat(binding, nil)
	if got != "anthropic" {
		t.Fatalf("auto + nil provider: got %q, want anthropic", got)
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_AutoFormatOpenAIProvider 验证
// binding.format=auto + localProvider 为 OpenAI provider -> 自动推导为 openai。
func TestBuildOpenCodeRuntimeConfigFromPreset_AutoFormatOpenAIProvider(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "auto-openai",
		Name: "Auto OpenAI",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"openai": {
				LocalProvider: "my-openai",
				Format:        "auto",
				Inject:        []string{"apiKey"},
			},
		},
	}

	// getAPIKey 只按 provider 读取统一 key，忽略 format。
	getAPIKey := func(providerName, format string) (string, error) {
		if providerName == "my-openai" {
			return "sk-openai-auto", nil
		}
		return "", nil
	}

	// getProvider 返回 OpenAI provider
	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-openai" {
			p := config.Provider{
				OpenAI: &config.OpenAIFormat{
					Enabled: true,
					BaseURL: "https://api.openai.com/v1",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	openaiProv, _ := providers["openai"].(map[string]any)
	options, _ := openaiProv["options"].(map[string]any)
	if options["apiKey"] != "sk-openai-auto" {
		t.Fatalf("apiKey = %v, want sk-openai-auto (auto resolved to openai format)", options["apiKey"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_AutoFormatAnthropicProvider 验证
// binding.format=auto + localProvider 为 Anthropic provider -> 自动推导为 anthropic。
func TestBuildOpenCodeRuntimeConfigFromPreset_AutoFormatAnthropicProvider(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "auto-anthropic",
		Name: "Auto Anthropic",
		Config: json.RawMessage(`{
			"model": "anthropic/claude-sonnet-4-5",
			"provider": {
				"anthropic": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"anthropic": {
				LocalProvider: "my-anthropic",
				Format:        "auto",
				Inject:        []string{"apiKey"},
			},
		},
	}

	// getAPIKey 只按 provider 读取统一 key，忽略 format。
	getAPIKey := func(providerName, format string) (string, error) {
		if providerName == "my-anthropic" {
			return "sk-ant-auto", nil
		}
		return "", nil
	}

	// getProvider 返回 Anthropic provider
	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-anthropic" {
			p := config.Provider{
				Anthropic: &config.AnthropicFormat{
					Enabled: true,
					BaseURL: "https://api.anthropic.com",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	anthProv, _ := providers["anthropic"].(map[string]any)
	options, _ := anthProv["options"].(map[string]any)
	if options["apiKey"] != "sk-ant-auto" {
		t.Fatalf("apiKey = %v, want sk-ant-auto (auto resolved to anthropic format)", options["apiKey"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_InjectBaseURL 验证
// inject=["baseURL"] 时会从 provider 真实注入对应格式的 baseURL。
func TestBuildOpenCodeRuntimeConfigFromPreset_InjectBaseURL(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "inject-baseurl",
		Name: "Inject BaseURL",
		Config: json.RawMessage(`{
			"model": "custom/my-model",
			"provider": {
				"custom": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"custom": {
				LocalProvider: "my-custom",
				Format:        "openai",
				Inject:        []string{"apiKey", "baseURL"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-custom-key", nil
	}

	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-custom" {
			p := config.Provider{
				OpenAI: &config.OpenAIFormat{
					Enabled: true,
					BaseURL: "https://custom.api.com/v1",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	customProv, _ := providers["custom"].(map[string]any)
	options, _ := customProv["options"].(map[string]any)

	if options["apiKey"] != "sk-custom-key" {
		t.Fatalf("apiKey = %v, want sk-custom-key", options["apiKey"])
	}
	if options["baseURL"] != "https://custom.api.com/v1" {
		t.Fatalf("baseURL = %v, want https://custom.api.com/v1", options["baseURL"])
	}
}

func TestBuildOpenCodeRuntimeConfigFromPreset_APIKeyIgnoresBindingFormat(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "binding-single-key",
		Name: "Binding Single Key",
		Config: json.RawMessage(`{
			"model": "custom/my-model",
			"provider": {
				"custom": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"custom": {
				LocalProvider: "my-provider",
				Format:        "openai",
				Inject:        []string{"apiKey", "baseURL", "organization"},
			},
		},
	}

	calledFormat := "__unset__"
	getAPIKey := func(providerName, format string) (string, error) {
		calledFormat = format
		if providerName == "my-provider" {
			return "sk-provider-level", nil
		}
		return "", nil
	}

	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-provider" {
			p := config.Provider{
				Anthropic: &config.AnthropicFormat{
					Enabled: true,
					BaseURL: "https://anthropic.example.com",
				},
				OpenAI: &config.OpenAIFormat{
					Enabled:      true,
					BaseURL:      "https://openai.example.com/v1",
					Organization: "org-single-key",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	if calledFormat != "" {
		t.Fatalf("getAPIKey format = %q, want empty because key source must be provider-level", calledFormat)
	}

	providers, _ := cfg["provider"].(map[string]any)
	customProv, _ := providers["custom"].(map[string]any)
	options, _ := customProv["options"].(map[string]any)
	if options["apiKey"] != "sk-provider-level" {
		t.Fatalf("apiKey = %v, want sk-provider-level", options["apiKey"])
	}
	if options["baseURL"] != "https://openai.example.com/v1" {
		t.Fatalf("baseURL = %v, want https://openai.example.com/v1", options["baseURL"])
	}
	if options["organization"] != "org-single-key" {
		t.Fatalf("organization = %v, want org-single-key", options["organization"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_InjectBaseURL_AnthropicFormat 验证
// inject=["baseURL"] + anthropic 格式时，使用 provider 的 anthropic baseURL。
func TestBuildOpenCodeRuntimeConfigFromPreset_InjectBaseURL_AnthropicFormat(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "inject-baseurl-anth",
		Name: "Inject BaseURL Anthropic",
		Config: json.RawMessage(`{
			"model": "custom/my-model",
			"provider": {
				"custom": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"custom": {
				LocalProvider: "my-anth-proxy",
				Format:        "anthropic",
				Inject:        []string{"apiKey", "baseURL"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-anth-key", nil
	}

	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-anth-proxy" {
			p := config.Provider{
				Anthropic: &config.AnthropicFormat{
					Enabled: true,
					BaseURL: "https://anthropic-proxy.example.com",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	customProv, _ := providers["custom"].(map[string]any)
	options, _ := customProv["options"].(map[string]any)

	if options["baseURL"] != "https://anthropic-proxy.example.com" {
		t.Fatalf("baseURL = %v, want https://anthropic-proxy.example.com", options["baseURL"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_InjectOrganization 验证
// inject=["organization"] + openai 格式时，注入 provider 的 organization。
func TestBuildOpenCodeRuntimeConfigFromPreset_InjectOrganization(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "inject-org",
		Name: "Inject Organization",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"openai": {
				LocalProvider: "my-openai",
				Format:        "openai",
				Inject:        []string{"apiKey", "baseURL", "organization"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-openai-key", nil
	}

	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-openai" {
			p := config.Provider{
				OpenAI: &config.OpenAIFormat{
					Enabled:      true,
					BaseURL:      "https://api.openai.com/v1",
					Organization: "org-test-123",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	openaiProv, _ := providers["openai"].(map[string]any)
	options, _ := openaiProv["options"].(map[string]any)

	if options["apiKey"] != "sk-openai-key" {
		t.Fatalf("apiKey = %v, want sk-openai-key", options["apiKey"])
	}
	if options["baseURL"] != "https://api.openai.com/v1" {
		t.Fatalf("baseURL = %v, want https://api.openai.com/v1", options["baseURL"])
	}
	if options["organization"] != "org-test-123" {
		t.Fatalf("organization = %v, want org-test-123", options["organization"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_InjectOrganization_AnthropicIgnored 验证
// inject=["organization"] + anthropic 格式时，organization 不注入（仅 openai 支持）。
func TestBuildOpenCodeRuntimeConfigFromPreset_InjectOrganization_AnthropicIgnored(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "inject-org-anth",
		Name: "Inject Organization Anthropic",
		Config: json.RawMessage(`{
			"model": "anthropic/claude-sonnet-4-5",
			"provider": {
				"anthropic": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"anthropic": {
				LocalProvider: "my-anth",
				Format:        "anthropic",
				Inject:        []string{"apiKey", "organization"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-ant-key", nil
	}

	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-anth" {
			p := config.Provider{
				Anthropic: &config.AnthropicFormat{
					Enabled: true,
					BaseURL: "https://api.anthropic.com",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	anthProv, _ := providers["anthropic"].(map[string]any)
	options, _ := anthProv["options"].(map[string]any)

	// apiKey 应存在
	if options["apiKey"] != "sk-ant-key" {
		t.Fatalf("apiKey = %v, want sk-ant-key", options["apiKey"])
	}
	// organization 不应注入（anthropic 格式不支持）
	if _, hasOrg := options["organization"]; hasOrg {
		t.Fatal("organization should NOT be injected for anthropic format")
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_DefaultInjectWithGetProvider 验证
// inject 为空（默认 apiKey+baseURL）时，getProvider 提供 baseURL 注入。
func TestBuildOpenCodeRuntimeConfigFromPreset_DefaultInjectWithGetProvider(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "default-inject",
		Name: "Default Inject",
		Config: json.RawMessage(`{
			"model": "custom/model",
			"provider": {
				"custom": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"custom": {
				LocalProvider: "my-custom",
				Format:        "openai",
				// Inject 为空 -> 默认 ["apiKey", "baseURL"]
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-default-key", nil
	}

	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-custom" {
			p := config.Provider{
				OpenAI: &config.OpenAIFormat{
					Enabled: true,
					BaseURL: "https://custom.default.com/v1",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	customProv, _ := providers["custom"].(map[string]any)
	options, _ := customProv["options"].(map[string]any)

	if options["apiKey"] != "sk-default-key" {
		t.Fatalf("apiKey = %v, want sk-default-key", options["apiKey"])
	}
	if options["baseURL"] != "https://custom.default.com/v1" {
		t.Fatalf("baseURL = %v, want https://custom.default.com/v1", options["baseURL"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_InjectBaseURLNoProvider 验证
// inject=["baseURL"] 但 getProvider 为 nil 时不崩溃，baseURL 不注入。
func TestBuildOpenCodeRuntimeConfigFromPreset_InjectBaseURLNoProvider(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "no-provider",
		Name: "No Provider",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {
					"options": {"baseURL": "https://preset-base.com"}
				}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"openai": {
				LocalProvider: "openai",
				Format:        "openai",
				Inject:        []string{"apiKey", "baseURL"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-key", nil
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, nil)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	openaiProv, _ := providers["openai"].(map[string]any)
	options, _ := openaiProv["options"].(map[string]any)

	// apiKey 应注入
	if options["apiKey"] != "sk-key" {
		t.Fatalf("apiKey = %v, want sk-key", options["apiKey"])
	}
	// preset.Config 中的 baseURL 应保留（深度合并行为）
	if options["baseURL"] != "https://preset-base.com" {
		t.Fatalf("preset baseURL = %v, want https://preset-base.com", options["baseURL"])
	}
}

// TestBuildOpenCodeEnvOverridesFromPreset_AutoFormatResolvesCorrectly 验证
// 环境变量生成时 format=auto 也能正确推导为实际格式。
func TestBuildOpenCodeEnvOverridesFromPreset_AutoFormatResolvesCorrectly(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "env-auto",
		Name: "Env Auto",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"openai": {
				LocalProvider: "my-openai",
				Format:        "auto",
				Inject:        []string{"apiKey"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		if providerName == "my-openai" {
			return "sk-openai-env", nil
		}
		return "", nil
	}

	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-openai" {
			p := config.Provider{
				OpenAI: &config.OpenAIFormat{
					Enabled: true,
					BaseURL: "https://api.openai.com/v1",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	overrides, err := BuildOpenCodeEnvOverridesFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverridesFromPreset: %v", err)
	}

	// 应设置 OPENAI_API_KEY（不是 ANTHROPIC_API_KEY）
	if overrides["OPENAI_API_KEY"] != "sk-openai-env" {
		t.Fatalf("OPENAI_API_KEY = %q, want sk-openai-env", overrides["OPENAI_API_KEY"])
	}
	if _, hasAnthKey := overrides["ANTHROPIC_API_KEY"]; hasAnthKey {
		t.Fatal("ANTHROPIC_API_KEY should not be set for openai provider with auto format")
	}
}

// TestBuildOpenCodeEnvOverridesFromPreset_AutoFormatAnthropic 验证
// 环境变量生成时 format=auto + anthropic provider -> ANTHROPIC_API_KEY。
func TestBuildOpenCodeEnvOverridesFromPreset_AutoFormatAnthropic(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "env-auto-anth",
		Name: "Env Auto Anthropic",
		Config: json.RawMessage(`{
			"model": "anthropic/claude-sonnet-4-5",
			"provider": {
				"anthropic": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"anthropic": {
				LocalProvider: "my-anthropic",
				Format:        "auto",
				Inject:        []string{"apiKey"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		if providerName == "my-anthropic" {
			return "sk-ant-env", nil
		}
		return "", nil
	}

	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-anthropic" {
			p := config.Provider{
				Anthropic: &config.AnthropicFormat{
					Enabled: true,
					BaseURL: "https://api.anthropic.com",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	overrides, err := BuildOpenCodeEnvOverridesFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverridesFromPreset: %v", err)
	}

	// 应设置 ANTHROPIC_API_KEY
	if overrides["ANTHROPIC_API_KEY"] != "sk-ant-env" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want sk-ant-env", overrides["ANTHROPIC_API_KEY"])
	}
}

// ========================================================================
// R. Legacy OpenCode terminal_presets.opencode regression tests
// ========================================================================

// TestBuildOpenCodeEnvOverrides_LegacyAnthropic_NoOpenAIKeyHardcode is a regression
// test for the legacy terminal_presets.opencode fallback path in LaunchOpenCode.
//
// Regression scenario: When a provider is Anthropic-compatible (not OpenAI),
// the legacy path must NOT hardcode or fall back to the OpenAI key namespace.
// It should use PreferredFormat() + alternate format fallback to read the correct key,
// and BuildOpenCodeEnvOverrides must set ANTHROPIC_API_KEY (not OPENAI_API_KEY).
//
// This test directly covers the "legacy fallback no longer hardcodes openai" behavior
// by exercising BuildOpenCodeEnvOverrides with a pure legacy Anthropic provider.
func TestBuildOpenCodeEnvOverrides_LegacyAnthropic_NoOpenAIKeyHardcode(t *testing.T) {
	// Construct a legacy Anthropic-compatible provider using old-style fields only.
	// No Anthropic/OpenAI format structs -- pure legacy config, exactly as a user who
	// only ever set AuthKey="ANTHROPIC_API_KEY" would have.
	provider := config.Provider{
		BaseURL:      "https://api.anthropic.com",
		DefaultModel: "claude-sonnet-4-5",
		AuthKey:      "ANTHROPIC_API_KEY",
		// Deliberately: no Anthropic struct, no OpenAI struct.
		// This mimics legacy terminal_presets.opencode users.
	}

	// --- Precondition checks: provider identity ---
	if provider.IsOpenAICompatible() {
		t.Fatal("provider should NOT be OpenAI-compatible for this regression test")
	}
	if !provider.IsAnthropicCompatible() {
		t.Fatal("provider should be Anthropic-compatible for this regression test")
	}
	if provider.PreferredFormat() != "anthropic" {
		t.Fatalf("PreferredFormat() = %q, want %q -- legacy Anthropic provider must resolve to anthropic format",
			provider.PreferredFormat(), "anthropic")
	}

	// Simulate the legacy OpenCode path: only an Anthropic key is available.
	apiKey := "sk-ant-legacy-regression-test-key"

	overrides, err := BuildOpenCodeEnvOverrides("anthropic", provider, "default", apiKey)
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverrides: %v", err)
	}

	// --- Assertion 1: ANTHROPIC_API_KEY is set correctly ---
	if overrides["ANTHROPIC_API_KEY"] != apiKey {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want %q", overrides["ANTHROPIC_API_KEY"], apiKey)
	}

	// --- Assertion 2: OPENAI_API_KEY must NOT be present ---
	// This is the core regression assertion: the legacy path must not fall back to
	// or hardcode the OpenAI key namespace for an Anthropic-compatible provider.
	if _, hasOpenAI := overrides["OPENAI_API_KEY"]; hasOpenAI {
		t.Fatal("OPENAI_API_KEY must NOT be set for Anthropic-compatible provider in legacy path " +
			"-- this indicates a regression where the legacy fallback hardcodes openai")
	}

	// --- Assertion 3: OPENCODE_CONFIG_CONTENT exists and is valid JSON ---
	content := overrides["OPENCODE_CONFIG_CONTENT"]
	if content == "" {
		t.Fatal("OPENCODE_CONFIG_CONTENT should not be empty")
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not valid JSON: %v\ncontent: %s", err, content)
	}

	// --- Assertion 4: config contains "anthropic" provider (not "openai") ---
	providerSection, ok := cfg["provider"].(map[string]any)
	if !ok {
		t.Fatal("OPENCODE_CONFIG_CONTENT must contain a 'provider' section")
	}
	anthropicProv, ok := providerSection["anthropic"].(map[string]any)
	if !ok {
		t.Fatalf("config.provider should contain 'anthropic' key; got keys: %v", mapKeys(providerSection))
	}

	// --- Assertion 5: apiKey injected in provider.anthropic.options ---
	options, ok := anthropicProv["options"].(map[string]any)
	if !ok {
		t.Fatal("config.provider.anthropic should have 'options'")
	}
	if options["apiKey"] != apiKey {
		t.Fatalf("config.provider.anthropic.options.apiKey = %v, want %q", options["apiKey"], apiKey)
	}

	// --- Assertion 6: no openai provider entry ---
	if _, hasOpenAI := providerSection["openai"]; hasOpenAI {
		t.Fatal("config.provider must NOT contain 'openai' entry for Anthropic-only legacy provider")
	}
}

// TestBuildOpenCodeEnvOverrides_LegacyAnthropic_ThirdPartyBaseURL verifies that
// a third-party Anthropic-compatible provider (non api.anthropic.com base URL)
// still correctly uses the Anthropic key path and generates a providerName-based
// OpenCode provider ID (not "anthropic" and definitely not "openai").
//
// This covers the legacy path for users with custom Anthropic API proxies.
func TestBuildOpenCodeEnvOverrides_LegacyAnthropic_ThirdPartyBaseURL(t *testing.T) {
	provider := config.Provider{
		BaseURL:      "https://my-anthropic-proxy.example.com",
		DefaultModel: "claude-sonnet-4-5",
		AuthKey:      "ANTHROPIC_API_KEY",
		// Legacy: no format structs
	}

	if provider.IsOpenAICompatible() {
		t.Fatal("third-party Anthropic provider should not be OpenAI-compatible")
	}
	if provider.PreferredFormat() != "anthropic" {
		t.Fatalf("PreferredFormat() = %q, want %q", provider.PreferredFormat(), "anthropic")
	}

	apiKey := "sk-ant-proxy-regression-key"

	overrides, err := BuildOpenCodeEnvOverrides("my-anthropic-proxy", provider, "default", apiKey)
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverrides: %v", err)
	}

	// Must set ANTHROPIC_API_KEY, not OPENAI_API_KEY
	if overrides["ANTHROPIC_API_KEY"] != apiKey {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want %q", overrides["ANTHROPIC_API_KEY"], apiKey)
	}
	if _, hasOpenAI := overrides["OPENAI_API_KEY"]; hasOpenAI {
		t.Fatal("OPENAI_API_KEY must NOT be set for third-party Anthropic provider")
	}

	// Config must have provider entry using providerName (not "anthropic" since it's third-party)
	content := overrides["OPENCODE_CONFIG_CONTENT"]
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	providerSection, _ := cfg["provider"].(map[string]any)

	// Third-party uses providerName as OC provider ID
	proxyProv, ok := providerSection["my-anthropic-proxy"].(map[string]any)
	if !ok {
		t.Fatalf("config.provider should contain 'my-anthropic-proxy'; got keys: %v", mapKeys(providerSection))
	}
	options, _ := proxyProv["options"].(map[string]any)
	if options["apiKey"] != apiKey {
		t.Fatalf("apiKey = %v, want %q", options["apiKey"], apiKey)
	}
	// Third-party Anthropic must include baseURL
	if options["baseURL"] != "https://my-anthropic-proxy.example.com" {
		t.Fatalf("baseURL = %v, want https://my-anthropic-proxy.example.com", options["baseURL"])
	}
}

// TestBuildOpenCodeEnvOverrides_LegacyAnthropic_EmptyPreset verifies that
// even when no preset is specified (empty presetName), the legacy Anthropic path
// still correctly uses ANTHROPIC_API_KEY and DefaultModel.
func TestBuildOpenCodeEnvOverrides_LegacyAnthropic_EmptyPreset(t *testing.T) {
	provider := config.Provider{
		BaseURL:      "https://api.anthropic.com",
		DefaultModel: "claude-sonnet-4-20250514",
		AuthKey:      "ANTHROPIC_API_KEY",
	}

	overrides, err := BuildOpenCodeEnvOverrides("anthropic", provider, "", "sk-ant-no-preset")
	if err != nil {
		t.Fatalf("BuildOpenCodeEnvOverrides: %v", err)
	}

	// ANTHROPIC_API_KEY, not OPENAI_API_KEY
	if overrides["ANTHROPIC_API_KEY"] != "sk-ant-no-preset" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want sk-ant-no-preset", overrides["ANTHROPIC_API_KEY"])
	}
	if _, hasOpenAI := overrides["OPENAI_API_KEY"]; hasOpenAI {
		t.Fatal("OPENAI_API_KEY must NOT be set")
	}

	// Config should use DefaultModel
	content := overrides["OPENCODE_CONFIG_CONTENT"]
	var cfg map[string]any
	json.Unmarshal([]byte(content), &cfg)
	if cfg["model"] != "anthropic/claude-sonnet-4-20250514" {
		t.Fatalf("model = %v, want anthropic/claude-sonnet-4-20250514 (from DefaultModel)", cfg["model"])
	}
}

// mapKeys returns the keys of a map[string]any for error messages.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ========================================================================
// S. npm 字段注入测试（OpenAI 兼容 provider 自动补 @ai-sdk/openai-compatible）
// ========================================================================

// TestBuildOpenCodeRuntimeConfig_ThirdPartyOpenAIInjectsNPM 验证旧轨道下
// 第三方 OpenAI 兼容 provider（baseURL 非 api.openai.com）生成的条目含
// npm: "@ai-sdk/openai-compatible"。
func TestBuildOpenCodeRuntimeConfig_ThirdPartyOpenAIInjectsNPM(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://opencode.ai/zen/go/v1",
		DefaultModel: "deepseek-v4-flash",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"default": {
				Name:  "Zen",
				Model: "deepseek-v4-flash",
			},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("zen-provider", provider, "default", "sk-zen")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	providerSection, _ := cfg["provider"].(map[string]any)
	// 第三方使用 providerName 作为 OC provider id
	provEntry, _ := providerSection["zen-provider"].(map[string]any)
	if provEntry == nil {
		t.Fatalf("provider.zen-provider should exist; got keys: %v", mapKeys(providerSection))
	}
	if got := provEntry["npm"]; got != "@ai-sdk/openai-compatible" {
		t.Fatalf("npm = %v, want @ai-sdk/openai-compatible", got)
	}
	// npm 与 options 平级（schema 校验）
	if _, hasOptions := provEntry["options"]; !hasOptions {
		t.Fatal("options should exist alongside npm")
	}
}

// TestBuildOpenCodeRuntimeConfig_OfficialOpenAINoNPM 验证 api.openai.com 的
// 内置 openai provider 条目不含 npm（走 opencode 预置实现）。
func TestBuildOpenCodeRuntimeConfig_OfficialOpenAINoNPM(t *testing.T) {
	provider := config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5",
		AuthKey:      "OPENAI_API_KEY",
		Presets: map[string]config.Preset{
			"default": {Name: "GPT", Model: "gpt-5"},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("openai", provider, "default", "sk-test")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	providerSection, _ := cfg["provider"].(map[string]any)
	openaiEntry, _ := providerSection["openai"].(map[string]any)
	if openaiEntry == nil {
		t.Fatal("provider.openai should exist")
	}
	if _, hasNPM := openaiEntry["npm"]; hasNPM {
		t.Fatalf("official openai provider must NOT contain npm; got entry: %v", openaiEntry)
	}
}

// TestBuildOpenCodeRuntimeConfig_AnthropicProviderNoNPM 验证 anthropic
// provider 条目不含 npm（npm 仅对 OpenAI 兼容 provider 注入）。
func TestBuildOpenCodeRuntimeConfig_AnthropicProviderNoNPM(t *testing.T) {
	provider := config.Provider{
		BaseURL:      "https://api.anthropic.com",
		DefaultModel: "claude-sonnet-4-5",
		AuthKey:      "ANTHROPIC_API_KEY",
		Presets: map[string]config.Preset{
			"default": {Name: "Claude", Model: "claude-sonnet-4-5"},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("anthropic", provider, "default", "sk-ant")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	providerSection, _ := cfg["provider"].(map[string]any)
	anthEntry, _ := providerSection["anthropic"].(map[string]any)
	if anthEntry == nil {
		t.Fatal("provider.anthropic should exist")
	}
	if _, hasNPM := anthEntry["npm"]; hasNPM {
		t.Fatalf("anthropic provider must NOT contain npm; got entry: %v", anthEntry)
	}
}

// TestBuildOpenCodeRuntimeConfig_ThirdPartyAnthropicNoNPM 验证第三方
// Anthropic 兼容 provider 条目不含 npm。
func TestBuildOpenCodeRuntimeConfig_ThirdPartyAnthropicNoNPM(t *testing.T) {
	provider := config.Provider{
		BaseURL:      "https://custom.anthropic.api",
		DefaultModel: "custom-llm",
		AuthKey:      "ANTHROPIC_API_KEY",
		Presets: map[string]config.Preset{
			"default": {Name: "Custom", Model: "custom-llm"},
		},
	}

	cfg, err := BuildOpenCodeRuntimeConfig("custom-anthropic", provider, "default", "sk-custom")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	providerSection, _ := cfg["provider"].(map[string]any)
	customEntry, _ := providerSection["custom-anthropic"].(map[string]any)
	if customEntry == nil {
		t.Fatal("provider.custom-anthropic should exist")
	}
	if _, hasNPM := customEntry["npm"]; hasNPM {
		t.Fatalf("third-party anthropic provider must NOT contain npm; got entry: %v", customEntry)
	}
}

// TestBuildOpenCodeRuntimeConfig_ThirdPartyOpenAIBaseURLNormalized 验证
// 第三方 OpenAI provider 的 baseURL 经过归一化（带 /chat/completions 后缀被剥离）
// 且仍注入 npm。
func TestBuildOpenCodeRuntimeConfig_ThirdPartyOpenAIBaseURLNormalized(t *testing.T) {
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{
			Enabled: true,
			BaseURL: "https://opencode.ai/zen/go/v1/chat/completions",
		},
		DefaultModel: "deepseek-v4-flash",
	}

	cfg, err := BuildOpenCodeRuntimeConfig("zen", provider, "", "sk-zen")
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfig: %v", err)
	}

	providerSection, _ := cfg["provider"].(map[string]any)
	// baseURL 不含 api.openai.com -> 用 providerName "zen" 作 id
	zenEntry, _ := providerSection["zen"].(map[string]any)
	if zenEntry == nil {
		t.Fatalf("provider.zen should exist; got keys: %v", mapKeys(providerSection))
	}
	options, _ := zenEntry["options"].(map[string]any)
	if options["baseURL"] != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("baseURL = %v, want https://opencode.ai/zen/go/v1 (suffix stripped)", options["baseURL"])
	}
	if zenEntry["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("npm = %v, want @ai-sdk/openai-compatible", zenEntry["npm"])
	}
}

// --- preset 轨道 npm 注入测试 ---

// TestBuildOpenCodeRuntimeConfigFromPreset_ThirdPartyOpenAIAutoNPM 验证
// preset 轨道下，代码为第三方 OpenAI 兼容 binding 新建的 provider entry
// 自动补 npm（binding key 非 "openai"）。
//
// 关键：preset.Config 的 provider 节点不含该 binding key 时，才算"代码新建"
// （entryExisted=false）；若用户在 preset.Config 手写了该 provider（哪怕空
// 对象 {}），则属"用户手写"，不补 npm（见 UserWrittenEntryPreservesNPMDecision）。
func TestBuildOpenCodeRuntimeConfigFromPreset_ThirdPartyOpenAIAutoNPM(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "auto-npm",
		Name: "Auto NPM",
		// provider 节点为空对象 -> providers["zen"] 不存在 -> 代码新建 entry
		Config: json.RawMessage(`{
			"model": "zen/deepseek-v4-flash",
			"provider": {}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"zen": {
				LocalProvider: "my-zen",
				Format:        "openai",
				Inject:        []string{"apiKey", "baseURL"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-zen", nil
	}
	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-zen" {
			p := config.Provider{
				OpenAI: &config.OpenAIFormat{
					Enabled: true,
					BaseURL: "https://opencode.ai/zen/go/v1",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	zenEntry, _ := providers["zen"].(map[string]any)
	if zenEntry == nil {
		t.Fatalf("provider.zen should exist; got keys: %v", mapKeys(providers))
	}
	if zenEntry["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("auto-created third-party openai entry npm = %v, want @ai-sdk/openai-compatible", zenEntry["npm"])
	}
	// baseURL 也应注入（带后缀会被归一化，此处无后缀原样）
	options, _ := zenEntry["options"].(map[string]any)
	if options["baseURL"] != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("baseURL = %v, want https://opencode.ai/zen/go/v1", options["baseURL"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_UserWrittenExplicitNPMPreserved
// 验证 preset 轨道下，用户在 preset.Config 中显式写了 npm 键（含自定义包名）
// 时，代码不覆盖用户值（保留用户指定的 npm 包）。
//
// 审核 Major-1：npm 注入条件改为"entry 无 npm 键才补"，已有 npm 键则保留不覆盖。
func TestBuildOpenCodeRuntimeConfigFromPreset_UserWrittenExplicitNPMPreserved(t *testing.T) {
	// 用户手写 provider.zen 且显式指定自定义 npm 包 @ai-sdk/glm。
	preset := config.OpenCodePreset{
		ID:   "user-npm",
		Name: "User NPM",
		Config: json.RawMessage(`{
			"model": "zen/deepseek-v4-flash",
			"provider": {
				"zen": {
					"npm": "@ai-sdk/glm",
					"options": {"baseURL": "https://opencode.ai/zen/go/v1"}
				}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"zen": {
				LocalProvider: "my-zen",
				Format:        "openai",
				Inject:        []string{"apiKey"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-zen", nil
	}
	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-zen" {
			p := config.Provider{
				OpenAI: &config.OpenAIFormat{
					Enabled: true,
					BaseURL: "https://opencode.ai/zen/go/v1",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	zenEntry, _ := providers["zen"].(map[string]any)
	if zenEntry == nil {
		t.Fatalf("provider.zen should exist; got keys: %v", mapKeys(providers))
	}
	// 用户显式写的 npm 必须保留，不被覆盖为默认 @ai-sdk/openai-compatible
	if zenEntry["npm"] != "@ai-sdk/glm" {
		t.Fatalf("user-written npm must be preserved; got %v, want @ai-sdk/glm", zenEntry["npm"])
	}
	// apiKey 仍应注入到已有 options
	options, _ := zenEntry["options"].(map[string]any)
	if options["apiKey"] != "sk-zen" {
		t.Fatalf("apiKey should still be injected into user entry; got %v", options["apiKey"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_BuiltinOpenAINoNPM 验证 preset
// 轨道下，binding key 为内置 "openai" 的 openai 格式 binding 不注入 npm。
func TestBuildOpenCodeRuntimeConfigFromPreset_BuiltinOpenAINoNPM(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "builtin-openai",
		Name: "Builtin OpenAI",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"openai": {
				LocalProvider: "my-openai",
				Format:        "openai",
				Inject:        []string{"apiKey"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-openai", nil
	}
	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-openai" {
			p := config.Provider{
				OpenAI: &config.OpenAIFormat{
					Enabled: true,
					BaseURL: "https://api.openai.com/v1",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	openaiEntry, _ := providers["openai"].(map[string]any)
	if openaiEntry == nil {
		t.Fatal("provider.openai should exist")
	}
	if _, hasNPM := openaiEntry["npm"]; hasNPM {
		t.Fatalf("builtin openai binding must NOT get npm; got entry: %v", openaiEntry)
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_AnthropicBindingNoNPM 验证
// preset 轨道下 anthropic 格式 binding 不注入 npm。
func TestBuildOpenCodeRuntimeConfigFromPreset_AnthropicBindingNoNPM(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "anth-binding",
		Name: "Anthropic Binding",
		Config: json.RawMessage(`{
			"model": "custom/claude",
			"provider": {
				"custom": {}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"custom": {
				LocalProvider: "my-anth",
				Format:        "anthropic",
				Inject:        []string{"apiKey"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		return "sk-ant", nil
	}
	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-anth" {
			p := config.Provider{
				Anthropic: &config.AnthropicFormat{
					Enabled: true,
					BaseURL: "https://api.anthropic.com",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	customEntry, _ := providers["custom"].(map[string]any)
	if customEntry == nil {
		t.Fatal("provider.custom should exist")
	}
	if _, hasNPM := customEntry["npm"]; hasNPM {
		t.Fatalf("anthropic binding must NOT get npm; got entry: %v", customEntry)
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_MigratedEntryWithoutNPMGetsDefaultNPM
// 验证迁移链：自动迁移（migrateTerminalPresetsToOpenCodePresets ->
// buildMigratedOpenCodeConfig, service.go:1475）生成的第三方 OpenAI preset，
// 其 provider entry 已存在但无 npm 键；BuildOpenCodeRuntimeConfigFromPreset 必须
// 为其补默认 npm @ai-sdk/openai-compatible，并正确注入 baseURL（归一化）、apiKey、
// 保留 model。
//
// 审核 Major-1：旧实现因 entryExisted=true 漏注入 npm，导致迁移主启动路径的
// OPENCODE_CONFIG_CONTENT.provider.<id> 缺 @ai-sdk/openai-compatible。
//
// preset.Config 模拟 buildMigratedOpenCodeConfig 的真实输出结构
// （service.go:1507-1551）：model 字段 + provider.<providerName>.options.baseURL
// （无 npm）。
func TestBuildOpenCodeRuntimeConfigFromPreset_MigratedEntryWithoutNPMGetsDefaultNPM(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "migrated-zen",
		Name: "Migrated Zen",
		// 迁移生成的 Config：provider.zen 已存在（entryExisted=true），含 options
		// 但无 npm —— 这是 buildMigratedOpenCodeConfig 的真实输出。
		Config: json.RawMessage(`{
			"model": "zen/deepseek-v4-flash",
			"provider": {
				"zen": {
					"options": {"baseURL": "https://opencode.ai/zen/go/v1"}
				}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"zen": {
				LocalProvider: "my-zen",
				Format:        "openai",
				Inject:        []string{"apiKey", "baseURL"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		if providerName == "my-zen" {
			return "sk-zen", nil
		}
		return "", nil
	}
	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-zen" {
			// 运行时读取的原始 baseURL 带后缀，应被归一化
			p := config.Provider{
				OpenAI: &config.OpenAIFormat{
					Enabled: true,
					BaseURL: "https://opencode.ai/zen/go/v1/chat/completions",
				},
			}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	// model 保留
	if cfg["model"] != "zen/deepseek-v4-flash" {
		t.Fatalf("model = %v, want zen/deepseek-v4-flash", cfg["model"])
	}

	providers, _ := cfg["provider"].(map[string]any)
	zenEntry, _ := providers["zen"].(map[string]any)
	if zenEntry == nil {
		t.Fatalf("provider.zen should exist; got keys: %v", mapKeys(providers))
	}
	// 迁移 entry 无 npm -> 必须补默认 npm（Major-1 核心断言）
	if zenEntry["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("migrated entry without npm must get default npm; got %v, want @ai-sdk/openai-compatible", zenEntry["npm"])
	}
	options, _ := zenEntry["options"].(map[string]any)
	if options == nil {
		t.Fatal("options should exist")
	}
	// baseURL 从 localProvider 运行时读取并归一化（带后缀被剥离）
	if options["baseURL"] != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("baseURL = %v, want https://opencode.ai/zen/go/v1 (normalized)", options["baseURL"])
	}
	// apiKey 注入
	if options["apiKey"] != "sk-zen" {
		t.Fatalf("apiKey = %v, want sk-zen", options["apiKey"])
	}
}

// TestBuildOpenCodeRuntimeConfigFromPreset_UserEntryWithoutNPMGetsDefaultNPM
// 验证用户在 preset.Config 手写了 provider 条目但未写 npm 键时，代码补默认 npm。
// 审核 Major-1：注入条件为"entry 无 npm 键才补"，不区分 entry 来源（用户手写
// 空条目与迁移生成的条目同等处理）。
func TestBuildOpenCodeRuntimeConfigFromPreset_UserEntryWithoutNPMGetsDefaultNPM(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "user-empty-entry",
		Name: "User Empty Entry",
		Config: json.RawMessage(`{
			"model": "zen/deepseek-v4-flash",
			"provider": {
				"zen": {
					"options": {"baseURL": "https://opencode.ai/zen/go/v1"}
				}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"zen": {
				LocalProvider: "my-zen",
				Format:        "openai",
				Inject:        []string{"apiKey"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) { return "sk-zen", nil }
	getProvider := func(providerName string) (*config.Provider, error) {
		if providerName == "my-zen" {
			p := config.Provider{OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://opencode.ai/zen/go/v1"}}
			return &p, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	zenEntry, _ := providers["zen"].(map[string]any)
	if zenEntry == nil {
		t.Fatalf("provider.zen should exist; got keys: %v", mapKeys(providers))
	}
	// 用户手写 entry 但无 npm 键 -> 补默认 npm
	if zenEntry["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("user entry without npm must get default npm; got %v, want @ai-sdk/openai-compatible", zenEntry["npm"])
	}
}

// TestDeriveOpenCodeProviderID_DeceptiveHost 验证官方 OpenAI host 判定使用
// 精确 host 比较，欺骗性 host/path 不被误判为内置 openai（审核 Major-4）。
func TestDeriveOpenCodeProviderID_DeceptiveHost(t *testing.T) {
	tests := []struct {
		name         string
		providerName string
		baseURL      string
		want         string
	}{
		{"official openai", "openai", "https://api.openai.com/v1", "openai"},
		{"deceptive trailing subdomain uses providerName", "mygw", "https://api.openai.com.evil.example/v1", "mygw"},
		{"deceptive path contains host uses providerName", "mygw", "https://gateway.example/proxy/api.openai.com/v1", "mygw"},
		{"deceptive path equals host uses providerName", "evil", "https://evil.example/api.openai.com", "evil"},
		{"third party uses providerName", "deepseek", "https://api.deepseek.com/v1", "deepseek"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := config.Provider{
				Type:    "openai",
				BaseURL: tt.baseURL,
				AuthKey: "OPENAI_API_KEY",
			}
			if got := deriveOpenCodeProviderID(tt.providerName, provider); got != tt.want {
				t.Fatalf("deriveOpenCodeProviderID(%q, %q) = %q, want %q", tt.providerName, tt.baseURL, got, tt.want)
			}
		})
	}
}

// TestMigrationChain_RealLoadMigrationThenRuntimeBuild 是真实跨包迁移链集成测试
// （增量复审 Major-1 证据缺口闭环）。
//
// 替代手工模拟 buildMigratedOpenCodeConfig 输出结构的 fixture 测试，改为：
//  1. 构造含第三方 OpenAI provider（baseURL 带后缀）+ opencode 类型 terminal preset 的 config；
//  2. 真实调用 config 包公开 API：SaveProvider + SaveTerminalPreset -> Save -> 新建 ConfigService
//     重新 Load，触发真实迁移函数 migrateTerminalPresetsToOpenCodePresets
//     -> buildMigratedOpenCodeConfig（config 包内私有，通过公开 Load 间接触发）；
//  3. 读取真实迁移产出的 OpenCodePreset，断言持久化 Config 中 baseURL 为用户原始值
//     （Major-2：迁移持久化全程原值，不被归一化污染）；
//  4. 将真实迁移产出的 preset 交给 launcher 运行时构建 BuildOpenCodeRuntimeConfigFromPreset
//     （getProvider 直接走 ConfigService.GetProvider 真实读取），断言：
//     npm 注入 @ai-sdk/openai-compatible（Major-1）、provider ID 为 providerName、
//     运行时 baseURL 归一化（来自 localProvider.EffectiveBaseURL）、apiKey 注入、model 保留。
//
// 该测试真实跨包：config 迁移（Load 内部）-> launcher 运行时构建，任何一层结构漂移都会失败。
func TestMigrationChain_RealLoadMigrationThenRuntimeBuild(t *testing.T) {
	dir := t.TempDir()

	// 1. 构造含第三方 OpenAI provider 的 config
	svc := config.NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	const rawBaseURL = "https://opencode.ai/zen/go/v1/chat/completions"
	if err := svc.SaveProvider("zen", config.Provider{
		Type:         "openai",
		BaseURL:      rawBaseURL,
		AuthKey:      "OPENAI_API_KEY",
		DefaultModel: "deepseek-v4-flash",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	// 2. 创建 opencode 类型 terminal preset，Provider 指向 zen
	if err := svc.SaveTerminalPreset("opencode", "zen/migrated", config.TerminalPreset{
		Name:     "Zen Migrated",
		Provider: "zen",
		Model:    "deepseek-v4-flash",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}

	// 3. 持久化 + 重新 Load，触发真实迁移链
	if err := svc.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	svc2 := config.NewConfigService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("re-Load: %v", err)
	}

	// 4. 拿到真实迁移产出的 OpenCodePreset
	ocPreset, err := svc2.GetOpenCodePreset("zen/migrated")
	if err != nil {
		t.Fatalf("GetOpenCodePreset after migration: %v", err)
	}
	if ocPreset.Source == nil || ocPreset.Source.Kind != "migrated-overlay" {
		t.Fatalf("Source.Kind = %v, want migrated-overlay", ocPreset.Source)
	}

	// 5. 断言持久化 Config 中 baseURL 保持原始值（Major-2 闭环：迁移持久化原值）
	var persistedCfg map[string]any
	if err := json.Unmarshal(ocPreset.Config, &persistedCfg); err != nil {
		t.Fatalf("unmarshal migrated Config: %v", err)
	}
	providers, _ := persistedCfg["provider"].(map[string]any)
	zenEntry, _ := providers["zen"].(map[string]any)
	if zenEntry == nil {
		t.Fatalf("persisted Config should contain provider.zen; got keys: %v", mapKeys(providers))
	}
	persistedOptions, _ := zenEntry["options"].(map[string]any)
	persistedBaseURL, _ := persistedOptions["baseURL"].(string)
	if persistedBaseURL != rawBaseURL {
		t.Fatalf("persisted baseURL = %q, want raw %q (Major-2: migration persistence must keep raw value)", persistedBaseURL, rawBaseURL)
	}
	// 迁移生成的 entry 不含 npm（由运行时层补，Major-1 的前提）
	if _, hasNPM := zenEntry["npm"]; hasNPM {
		t.Fatalf("migrated entry should NOT have npm (runtime layer adds it); got %v", zenEntry)
	}

	// 6. 真实迁移产出的 preset 交给 launcher 运行时构建
	//    getProvider 直接走 ConfigService.GetProvider，真实读取持久化 provider。
	getAPIKey := func(providerName, format string) (string, error) {
		if providerName == "zen" {
			return "sk-zen", nil
		}
		return "", nil
	}
	getProvider := func(providerName string) (*config.Provider, error) {
		return svc2.GetProvider(providerName)
	}

	runtimeCfg, err := BuildOpenCodeRuntimeConfigFromPreset(*ocPreset, getAPIKey, getProvider)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	// 7. 断言运行时产出（Major-1 npm 注入 + Major-2 运行时归一化）
	// model 字段保留：ocProviderID/model
	if model, _ := runtimeCfg["model"].(string); model != "zen/deepseek-v4-flash" {
		t.Fatalf("runtime model = %q, want zen/deepseek-v4-flash", model)
	}
	rtProviders, _ := runtimeCfg["provider"].(map[string]any)
	rtZen, _ := rtProviders["zen"].(map[string]any)
	if rtZen == nil {
		t.Fatalf("runtime config should contain provider.zen; got keys: %v", mapKeys(rtProviders))
	}
	// 第三方 OpenAI provider 补默认 npm（Major-1 核心断言：迁移 entry 无 npm -> 运行时补）
	if rtZen["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("runtime provider.zen.npm = %v, want @ai-sdk/openai-compatible (Major-1)", rtZen["npm"])
	}
	rtOptions, _ := rtZen["options"].(map[string]any)
	// 运行时 baseURL 归一化（带后缀被剥离），来自 localProvider.EffectiveBaseURL 覆盖持久化原值
	rtBaseURL, _ := rtOptions["baseURL"].(string)
	if rtBaseURL != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("runtime baseURL = %q, want normalized https://opencode.ai/zen/go/v1", rtBaseURL)
	}
	if rtAPIKey, _ := rtOptions["apiKey"].(string); rtAPIKey != "sk-zen" {
		t.Fatalf("runtime apiKey = %v, want sk-zen", rtOptions["apiKey"])
	}
}
