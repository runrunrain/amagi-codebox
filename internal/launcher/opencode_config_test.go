package launcher

import (
	"encoding/json"
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
							"apiKey": "base-key",
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
		"model": "openai/gpt-5-high",
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
		"model":     "openai/gpt-5-pro",
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

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey)
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

	BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey)

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

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey)
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
		ID:      "empty",
		Name:    "Empty",
		Config:  nil,
		Bindings: map[string]config.OpenCodeBinding{},
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, nil)
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

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, nil)
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

	overrides, err := BuildOpenCodeEnvOverridesFromPreset(preset, getAPIKey)
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

// TestBuildOpenCodeRuntimeConfigFromPreset_AutoFormat 验证 format=auto 时默认使用 anthropic。
func TestBuildOpenCodeRuntimeConfigFromPreset_AutoFormat(t *testing.T) {
	preset := config.OpenCodePreset{
		ID:   "auto-format",
		Name: "Auto Format",
		Config: json.RawMessage(`{
			"model": "glm/glm-5",
			"provider": {
				"glm": {
					"options": {"baseURL": "https://open.bigmodel.cn/api/anthropic"}
				}
			}
		}`),
		Bindings: map[string]config.OpenCodeBinding{
			"glm": {
				LocalProvider: "glm",
				Format:        "auto",
				Inject:        []string{"apiKey"},
			},
		},
	}

	getAPIKey := func(providerName, format string) (string, error) {
		if providerName == "glm" {
			return "sk-glm-key", nil
		}
		return "", nil
	}

	cfg, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey)
	if err != nil {
		t.Fatalf("BuildOpenCodeRuntimeConfigFromPreset: %v", err)
	}

	providers, _ := cfg["provider"].(map[string]any)
	glmProv, _ := providers["glm"].(map[string]any)
	options, _ := glmProv["options"].(map[string]any)
	if options["apiKey"] != "sk-glm-key" {
		t.Fatalf("apiKey = %v, want sk-glm-key", options["apiKey"])
	}
}
