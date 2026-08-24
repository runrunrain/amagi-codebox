package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/config"
)

// TestPiAPITypeWireAPIMapping 验证 pi/omp 的 api 字段三值映射：
// OpenAI 兼容默认 openai-completions；wire_api=responses 时改用 openai-responses；
// Anthropic 兼容恒为 anthropic-messages。omp 与 pi 同构，复用同一函数
// （omp_config.go），改一处两引擎同时生效。
func TestPiAPITypeWireAPIMapping(t *testing.T) {
	tests := []struct {
		name     string
		provider config.Provider
		want     string
	}{
		{
			name:     "openai default (wire_api unset) maps to openai-completions",
			provider: config.Provider{OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com/v1"}},
			want:     "openai-completions",
		},
		{
			name:     "openai wire_api chat maps to openai-completions",
			provider: config.Provider{OpenAI: &config.OpenAIFormat{Enabled: true, WireAPI: "chat"}},
			want:     "openai-completions",
		},
		{
			name:     "openai wire_api responses maps to openai-responses",
			provider: config.Provider{OpenAI: &config.OpenAIFormat{Enabled: true, WireAPI: "responses"}},
			want:     "openai-responses",
		},
		{
			name:     "openai wire_api illegal value falls back to openai-completions",
			provider: config.Provider{OpenAI: &config.OpenAIFormat{Enabled: true, WireAPI: "grpc"}},
			want:     "openai-completions",
		},
		{
			name:     "legacy openai provider (nil sub-block) maps to openai-completions",
			provider: config.Provider{Type: "openai", AuthKey: "OPENAI_API_KEY"},
			want:     "openai-completions",
		},
		{
			name:     "anthropic maps to anthropic-messages",
			provider: config.Provider{Anthropic: &config.AnthropicFormat{Enabled: true}},
			want:     "anthropic-messages",
		},
		{
			name: "disabled openai block with wire_api does not flip anthropic",
			provider: config.Provider{
				Anthropic: &config.AnthropicFormat{Enabled: true},
				OpenAI:    &config.OpenAIFormat{WireAPI: "responses"},
			},
			want: "anthropic-messages",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := piAPIType(tt.provider); got != tt.want {
				t.Fatalf("piAPIType() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildPiModelsConfigWireAPIResponses 验证 wire_api=responses 时 pi models.json
// 的 api 字段透传为 openai-responses（BuildPiModelsConfig → piAPIType 全链路）。
func TestBuildPiModelsConfigWireAPIResponses(t *testing.T) {
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{
			Enabled: true,
			BaseURL: "https://api.example.com/v1",
			WireAPI: "responses",
		},
	}
	cfg, err := BuildPiModelsConfig("custom", provider, "m", "k", config.Parameters{}, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-custom"]
	if entry["api"] != "openai-responses" {
		t.Fatalf("api = %#v, want openai-responses", entry["api"])
	}
}

// TestWritePiAgentConfigTightPerms (P1-7) verifies the agent dir is 0700 and the
// models.json file is 0600, since the resolved header values it may carry can be
// sensitive (API keys referenced via $ENV: at build time).
func TestWritePiAgentConfigTightPerms(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".pi", "agent")
	cfg := map[string]any{"providers": map[string]any{"amagi-x": map[string]any{"baseUrl": "https://x"}}}
	if err := WritePiAgentConfig(agentDir, cfg); err != nil {
		t.Fatalf("WritePiAgentConfig: %v", err)
	}
	info, err := os.Stat(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("agentDir perm = %o, want 0700", info.Mode().Perm())
	}
	mi, err := os.Stat(filepath.Join(agentDir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mi.Mode().Perm() != 0o600 {
		t.Errorf("models.json perm = %o, want 0600", mi.Mode().Perm())
	}
	// content is valid JSON
	b, _ := os.ReadFile(filepath.Join(agentDir, "models.json"))
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Errorf("models.json not valid JSON: %v", err)
	}
}

// TestResolveEnvHeaderValue (P1-7) covers the $ENV: / ${ENV:} header-value
// resolver used by BuildPiModelsConfig.
func TestResolveEnvHeaderValue(t *testing.T) {
	t.Setenv("AMAGI_PI_TEST_KEY", "secret-value")
	cases := []struct {
		in    string
		want  string
		isRef bool
	}{
		{"$ENV:AMAGI_PI_TEST_KEY", "secret-value", true},
		{"${ENV:AMAGI_PI_TEST_KEY}", "secret-value", true},
		{"$ENV:UNSET_AMAGI_VAR_X", "", true},  // unset env -> empty, isRef
		{"plain-value", "plain-value", false}, // literal passthrough
		{"Bearer xyz", "Bearer xyz", false},   // literal passthrough
		{"$ENV:1bad", "$ENV:1bad", false},     // invalid var name -> literal
	}
	for _, c := range cases {
		got, isRef := resolveEnvHeaderValue(c.in)
		if got != c.want || isRef != c.isRef {
			t.Errorf("resolveEnvHeaderValue(%q) = (%q,%v), want (%q,%v)", c.in, got, isRef, c.want, c.isRef)
		}
	}
}

// TestBuildPiModelsConfigResolvesEnvHeaders (P1-7) verifies that header values
// written as $ENV: refs are resolved to the env value at build time, while plain
// literals pass through unchanged, and unset refs are omitted.
func TestBuildPiModelsConfigReasoningEffortAlone(t *testing.T) {
	// v1.3.23 回归：reasoning_effort 单独出现（无 thinking.type）也必须开启 reasoning，
	// 否则 pi clampThinkingLevel 把 --thinking max 钳回 off，预设强度静默失效。
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4"},
	}
	params := config.Parameters{
		ReasoningEffort: "max",
		ContextWindow:   &config.ContextWindowConfig{ModelContextWindow: 1000000},
	}
	cfg, err := BuildPiModelsConfig("glm", provider, "glm-5.3", "test-key", params, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig error: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-glm"]
	models := entry["models"].([]map[string]any)
	m := models[0]
	if m["reasoning"] != true {
		t.Errorf("reasoning = %#v, want true (reasoning_effort alone must enable reasoning)", m["reasoning"])
	}
	lm, ok := m["thinkingLevelMap"].(map[string]any)
	if !ok || lm["max"] != "max" || lm["xhigh"] != "xhigh" {
		t.Errorf("thinkingLevelMap = %#v, want xhigh/max identity", m["thinkingLevelMap"])
	}
	if m["contextWindow"] != 1000000 {
		t.Errorf("contextWindow = %#v, want 1000000", m["contextWindow"])
	}
}

func TestBuildPiModelsConfigResolvesEnvHeaders(t *testing.T) {
	t.Setenv("AMAGI_PI_HDR_RESOLVED", "resolved-secret")
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{
			Enabled: true,
			BaseURL: "https://api.example.com",
			Headers: map[string]string{
				"X-Resolved": "$ENV:AMAGI_PI_HDR_RESOLVED",
				"X-Plain":    "literal",
				"X-Unset":    "$ENV:DEFINITELY_UNSET_AMAGI_VAR",
			},
		},
	}
	cfg, err := BuildPiModelsConfig("custom", provider, "m", "k", config.Parameters{}, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	providers := cfg["providers"].(map[string]map[string]any)
	entry := providers["amagi-custom"]
	headers, ok := entry["headers"].(map[string]string)
	if !ok {
		t.Fatalf("headers missing or wrong type: %#v", entry["headers"])
	}
	if headers["X-Resolved"] != "resolved-secret" {
		t.Errorf("X-Resolved = %q, want resolved-secret", headers["X-Resolved"])
	}
	if headers["X-Plain"] != "literal" {
		t.Errorf("X-Plain = %q, want literal", headers["X-Plain"])
	}
	if _, present := headers["X-Unset"]; present {
		t.Errorf("X-Unset (unset env ref) must be omitted, got %q", headers["X-Unset"])
	}
}

// TestBuildPiModelsConfigEnablesExtendedThinkingLevels 验证回归：思考开启的模型
// 必须写入 thinkingLevelMap.xhigh/max——pi 仅在 map 显式声明时开放扩展级别，
// 缺省时 clampThinkingLevel 将 --thinking max/xhigh 钳回 high（k3-256k 无法切到
// max 的根因）。思考未开启时不写 reasoning 也不写 thinkingLevelMap。
func TestBuildPiModelsConfigEnablesExtendedThinkingLevels(t *testing.T) {
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
	}
	enabled := config.Parameters{
		Thinking: &config.ThinkingConfig{Type: "enabled"},
	}
	cfg, err := BuildPiModelsConfig("kimi", provider, "k3-256k", "k", enabled, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-kimi"]
	models := entry["models"].([]map[string]any)
	if len(models) != 1 {
		t.Fatalf("models len = %d, want 1", len(models))
	}
	if models[0]["reasoning"] != true {
		t.Errorf("reasoning = %#v, want true", models[0]["reasoning"])
	}
	levelMap, ok := models[0]["thinkingLevelMap"].(map[string]any)
	if !ok {
		t.Fatalf("thinkingLevelMap missing or wrong type: %#v", models[0]["thinkingLevelMap"])
	}
	if levelMap["xhigh"] != "xhigh" || levelMap["max"] != "max" {
		t.Errorf("thinkingLevelMap = %#v, want xhigh/max identity", levelMap)
	}

	// 思考未开启：不写 reasoning / thinkingLevelMap。
	cfgOff, err := BuildPiModelsConfig("kimi", provider, "k3-256k", "k", config.Parameters{}, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig (no thinking): %v", err)
	}
	mOff := cfgOff["providers"].(map[string]map[string]any)["amagi-kimi"]["models"].([]map[string]any)[0]
	if _, present := mOff["reasoning"]; present {
		t.Errorf("reasoning must be omitted when thinking disabled, got %#v", mOff["reasoning"])
	}
	if _, present := mOff["thinkingLevelMap"]; present {
		t.Errorf("thinkingLevelMap must be omitted when thinking disabled, got %#v", mOff["thinkingLevelMap"])
	}
}

// TestBuildPiModelsConfigCompatDefaults 验证回归：amagi 托管的第三方 OpenAI 兼容
// 服务商（如 kimi coding）不接受 developer 角色，pi 内置探测覆盖不到 api.kimi.com，
// 默认以 developer 角色发送 system prompt 会报 400。compat.supportsDeveloperRole
// 必须默认 false；预设 pi_compat 显式覆写时显式值优先。
func TestBuildPiModelsConfigCompatDefaults(t *testing.T) {
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.kimi.com/coding/v1"},
	}

	// 默认：supportsDeveloperRole=false。
	cfg, err := BuildPiModelsConfig("kimi", provider, "k3-256k", "k", config.Parameters{}, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	m := cfg["providers"].(map[string]map[string]any)["amagi-kimi"]["models"].([]map[string]any)[0]
	compat, ok := m["compat"].(map[string]any)
	if !ok {
		t.Fatalf("compat missing or wrong type: %#v", m["compat"])
	}
	if compat["supportsDeveloperRole"] != false {
		t.Errorf("supportsDeveloperRole = %#v, want false", compat["supportsDeveloperRole"])
	}

	// 显式覆写：pi_compat 的值优先，其余键原样透传。
	cfg2, err := BuildPiModelsConfig("kimi", provider, "k3-256k", "k", config.Parameters{
		PiCompat: map[string]any{
			"supportsDeveloperRole":   true,
			"supportsReasoningEffort": false,
		},
	}, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig (override): %v", err)
	}
	compat2 := cfg2["providers"].(map[string]map[string]any)["amagi-kimi"]["models"].([]map[string]any)[0]["compat"].(map[string]any)
	if compat2["supportsDeveloperRole"] != true {
		t.Errorf("explicit supportsDeveloperRole=true overridden, got %#v", compat2["supportsDeveloperRole"])
	}
	if compat2["supportsReasoningEffort"] != false {
		t.Errorf("supportsReasoningEffort not passed through, got %#v", compat2["supportsReasoningEffort"])
	}
}

// TestWritePiAgentConfigUpgradesLegacyPerms (审核 Major-2③) verifies that a
// pre-existing 0755 agent dir (created by older versions) is tightened to 0700
// on the next write, and an overwritten models.json ends up 0600.
func TestWritePiAgentConfigUpgradesLegacyPerms(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".pi", "agent")
	// Simulate a legacy install: loose dir + loose file.
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(agentDir, "models.json")
	if err := os.WriteFile(legacy, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{"providers": map[string]any{}}
	if err := WritePiAgentConfig(agentDir, cfg); err != nil {
		t.Fatalf("WritePiAgentConfig: %v", err)
	}

	info, err := os.Stat(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("legacy agentDir perm = %o, want tightened 0700", info.Mode().Perm())
	}
	mi, err := os.Stat(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if mi.Mode().Perm() != 0o600 {
		t.Errorf("overwritten models.json perm = %o, want 0600", mi.Mode().Perm())
	}
}

func TestMergePiAgentConfigPreservesExistingConfig(t *testing.T) {
	agentDir := t.TempDir()
	existing := map[string]any{
		"version": 2,
		"providers": map[string]any{
			"existing":  map[string]any{"baseUrl": "https://existing.example"},
			"amagi-new": map[string]any{"baseUrl": "https://stale.example"},
		},
	}
	data, err := json.Marshal(existing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{
		"providers": map[string]map[string]any{
			"amagi-new": {"baseUrl": "https://fresh.example"},
		},
	}
	merged := MergePiAgentConfig(cfg, agentDir)
	if merged["version"] != float64(2) {
		t.Fatalf("existing top-level config was not preserved: %#v", merged)
	}
	providers, ok := merged["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers type = %T, want map[string]any", merged["providers"])
	}
	if _, ok := providers["existing"]; !ok {
		t.Fatalf("existing provider was not preserved: %#v", providers)
	}
	managed, ok := providers["amagi-new"].(map[string]any)
	if !ok || managed["baseUrl"] != "https://fresh.example" {
		t.Fatalf("managed provider did not override stale entry: %#v", providers["amagi-new"])
	}
	if _, changed := cfg["version"]; changed {
		t.Fatal("input cfg was mutated")
	}
}

func TestBuildPiModelsConfigRegistersAllProviderPresetModels(t *testing.T) {
	// v1.3.34 回归：provider 有多个预设（各带不同模型/参数）时，托管条目
	// 必须注册全部预设模型——此前 desired 只含启动选中的单模型，mergeProviderConfig
	// 对同名托管条目整体替换 → 其他预设模型被覆盖丢失。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "model-d",
		Presets: map[string]config.Preset{
			"a": {Model: "model-a", Parameters: config.Parameters{
				ContextWindow: &config.ContextWindowConfig{ModelContextWindow: 1000000},
			}},
			"b": {Model: "model-b", Parameters: config.Parameters{
				ReasoningEffort: "max",
			}},
			// 同模型 id 的重复预设：去重，先注册者（启动选中/排序靠前）参数优先。
			"c": {Model: "model-a", Parameters: config.Parameters{
				MaxTokens: 9999,
			}},
		},
	}
	launchedParams := config.Parameters{MaxTokens: 2048}
	cfg, err := BuildPiModelsConfig("multi", provider, "model-b", "k", launchedParams, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-multi"]
	models := entry["models"].([]map[string]any)
	if len(models) != 3 {
		t.Fatalf("models len = %d, want 3 (model-b + model-a 去重 + model-d 兜底): %v", len(models), models)
	}
	byID := make(map[string]map[string]any, len(models))
	var order []string
	for _, m := range models {
		byID[m["id"].(string)] = m
		order = append(order, m["id"].(string))
	}
	// 启动选中的模型排首位，参数以本次传入为准（非预设 b 的 reasoning）。
	if order[0] != "model-b" {
		t.Errorf("first model = %q, want model-b (launched first)", order[0])
	}
	if byID["model-b"]["maxTokens"] != 2048 {
		t.Errorf("launched model maxTokens = %#v, want 2048 (launch params authoritative)", byID["model-b"]["maxTokens"])
	}
	if _, has := byID["model-b"]["reasoning"]; has {
		t.Errorf("launched model must use launch params, not preset b reasoning")
	}
	// 其余预设按各自 Parameters 注册。
	if byID["model-a"]["contextWindow"] != 1000000 {
		t.Errorf("model-a contextWindow = %#v, want 1000000 (preset a params)", byID["model-a"]["contextWindow"])
	}
	if byID["model-a"]["maxTokens"] == 9999 {
		t.Errorf("duplicate preset c params must not override first registration")
	}
	// DefaultModel 未被覆盖时兜底裸注册（零参数）。
	if _, has := byID["model-d"]["maxTokens"]; has {
		t.Errorf("default model should be bare-registered, got params: %v", byID["model-d"])
	}
}

func TestBuildOmpModelsConfigRegistersAllProviderPresetModels(t *testing.T) {
	// 与 pi 同构的多预设回归（omp models.yml 的 models 数组同样不得收敛为单模型）。
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		Presets: map[string]config.Preset{
			"fast": {Model: "flash-x"},
			"max":  {Model: "think-y", Parameters: config.Parameters{ReasoningEffort: "max"}},
		},
	}
	cfg, err := BuildOmpModelsConfig("m", provider, "flash-x", "k", config.Parameters{}, nil)
	if err != nil {
		t.Fatalf("BuildOmpModelsConfig: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-m"]
	models := entry["models"].([]map[string]any)
	if len(models) != 2 {
		t.Fatalf("models len = %d, want 2: %v", len(models), models)
	}
	if models[0]["id"] != "flash-x" {
		t.Errorf("first model = %v, want flash-x (launched first)", models[0]["id"])
	}
	byID := map[string]map[string]any{models[0]["id"].(string): models[0], models[1]["id"].(string): models[1]}
	if byID["think-y"]["reasoning"] != true {
		t.Errorf("think-y reasoning = %#v, want true (own preset params)", byID["think-y"]["reasoning"])
	}
}

func TestBuildPiModelsConfigBareLaunchInheritsMatchingPresetParams(t *testing.T) {
	// 2026-08-19 回归：裸参数启动（default_model 直启 / 请求未带 parameters）时，
	// 启动模型此前以零参数优先注册，同 id 预设的 contextWindow/maxTokens/reasoning
	// 被剥掉（实战 glm-5.3 裸注册 → reasoning 丢失 + maxTokens 缺省 16384 截断）。
	// 现在：零值参数回退继承同 Model 预设的 Parameters；显式传入仍优先。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "glm-5.3",
		Presets: map[string]config.Preset{
			"glm-5.3": {Model: "glm-5.3", Parameters: config.Parameters{
				MaxTokens:     131072,
				ContextWindow: &config.ContextWindowConfig{ModelContextWindow: 1000000},
				Thinking:      &config.ThinkingConfig{Type: "enabled"},
				PiCompat:      map[string]any{"thinkingFormat": "zai"},
			}},
		},
	}
	cfg, err := BuildPiModelsConfig("glm", provider, "", "k", config.Parameters{}, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-glm"]
	models := entry["models"].([]map[string]any)
	if len(models) != 1 {
		t.Fatalf("models len = %d, want 1: %v", len(models), models)
	}
	m := models[0]
	if m["maxTokens"] != 131072 {
		t.Errorf("maxTokens = %#v, want 131072 (inherited from same-id preset)", m["maxTokens"])
	}
	if m["contextWindow"] != 1000000 {
		t.Errorf("contextWindow = %#v, want 1000000", m["contextWindow"])
	}
	if m["reasoning"] != true {
		t.Errorf("reasoning = %#v, want true", m["reasoning"])
	}
	compat := m["compat"].(map[string]any)
	if compat["thinkingFormat"] != "zai" {
		t.Errorf("compat.thinkingFormat = %#v, want zai", compat["thinkingFormat"])
	}
	// 显式传入的参数仍优先（不被预设覆盖）。
	cfg2, err := BuildPiModelsConfig("glm", provider, "glm-5.3", "k", config.Parameters{MaxTokens: 2048}, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig explicit: %v", err)
	}
	m2 := cfg2["providers"].(map[string]map[string]any)["amagi-glm"]["models"].([]map[string]any)[0]
	if m2["maxTokens"] != 2048 {
		t.Errorf("explicit maxTokens = %#v, want 2048 (launch params authoritative)", m2["maxTokens"])
	}
}

func TestBuildPiModelsConfigRegistersTerminalPresetModels(t *testing.T) {
	// 回归：启动链路必须注册该 provider 在 openai 公共预设桶（pi/omp 消费的桶）
	// 下的全部预设模型。此前启动写入只含启动模型 + 旧版 provider.Presets，
	// 托管条目整体替换语义会把统一同步写入的同 provider 其他 openai 预设模型
	// 挤掉（实测 amagi-glm 在 ~/.pi/agent/models.json 收敛为单模型）。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "glm-5.3",
	}
	presetModels := []ManagedProviderModel{
		{ID: "glm-5.3", Parameters: config.Parameters{
			MaxTokens:       65536,
			ContextWindow:   &config.ContextWindowConfig{ModelContextWindow: 1000000},
			ReasoningEffort: "max",
		}},
		{ID: "glm-5.3[1m]", Parameters: config.Parameters{
			MaxTokens: 131072,
		}},
	}
	// 用另一预设模型启动（裸参数），DefaultModel 不是启动模型。
	cfg, err := BuildPiModelsConfig("glm", provider, "glm-5.3[1m]", "k", config.Parameters{}, presetModels)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-glm"]
	models := entry["models"].([]map[string]any)
	if len(models) != 2 {
		t.Fatalf("models len = %d, want 2 (openai 桶预设模型全部注册): %v", len(models), models)
	}
	byID := make(map[string]map[string]any, len(models))
	for _, m := range models {
		byID[m["id"].(string)] = m
	}
	// 启动模型排首；裸参数继承同 id openai 预设参数。
	if models[0]["id"] != "glm-5.3[1m]" {
		t.Errorf("first model = %v, want glm-5.3[1m] (launched first)", models[0]["id"])
	}
	if byID["glm-5.3[1m]"]["maxTokens"] != 131072 {
		t.Errorf("glm-5.3[1m] maxTokens = %#v, want 131072 (inherited from same-id openai preset)", byID["glm-5.3[1m]"]["maxTokens"])
	}
	// 未启动的 DefaultModel 同样保留自己的 openai 预设参数（glm-5.3 裸注册回归）。
	if byID["glm-5.3"]["maxTokens"] != 65536 {
		t.Errorf("glm-5.3 maxTokens = %#v, want 65536 (own openai preset params)", byID["glm-5.3"]["maxTokens"])
	}
	if byID["glm-5.3"]["contextWindow"] != 1000000 {
		t.Errorf("glm-5.3 contextWindow = %#v, want 1000000", byID["glm-5.3"]["contextWindow"])
	}
	if byID["glm-5.3"]["reasoning"] != true {
		t.Errorf("glm-5.3 reasoning = %#v, want true (reasoning_effort=max)", byID["glm-5.3"]["reasoning"])
	}
}

func TestBuildPiModelsConfigBareLaunchInheritsTerminalPresetParams(t *testing.T) {
	// 裸参数直启 DefaultModel 时，参数继承源新增 openai 桶 presetModels
	//（旧版 provider.Presets 已迁移为空，实际参数都在公共预设桶里）。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "glm-5.3",
	}
	presetModels := []ManagedProviderModel{
		{ID: "glm-5.3", Parameters: config.Parameters{
			MaxTokens:     65536,
			ContextWindow: &config.ContextWindowConfig{ModelContextWindow: 1000000},
			Thinking:      &config.ThinkingConfig{Type: "enabled"},
		}},
	}
	cfg, err := BuildPiModelsConfig("glm", provider, "", "k", config.Parameters{}, presetModels)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	models := cfg["providers"].(map[string]map[string]any)["amagi-glm"]["models"].([]map[string]any)
	if len(models) != 1 {
		t.Fatalf("models len = %d, want 1: %v", len(models), models)
	}
	m := models[0]
	if m["maxTokens"] != 65536 || m["contextWindow"] != 1000000 || m["reasoning"] != true {
		t.Errorf("bare default launch lost openai preset params: %v", m)
	}
}

func TestBuildPiModelsConfigVisionPresetDeclaresImageInput(t *testing.T) {
	// 多模态 input 声明回归：预设标记 Vision/Video 的模型必须在 pi models.json
	// 声明 input=["text","image"]，否则 pi 默认 ["text"]，amagi-pi 守卫据此拦截
	// read 图片直送（实战：amagi-kimi/k3 实为多模态模型却被误判为纯文本）。
	// 未标记模型不得出现 input 键（维持默认 ["text"]，与既有行为零差异）。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "k3",
	}
	presetModels := []ManagedProviderModel{
		{ID: "k3", Parameters: config.Parameters{MaxTokens: 60000}, Vision: true},
		{ID: "k3-plain", Parameters: config.Parameters{MaxTokens: 8192}},
	}
	cfg, err := BuildPiModelsConfig("kimi", provider, "k3", "k", config.Parameters{}, presetModels)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	models := cfg["providers"].(map[string]map[string]any)["amagi-kimi"]["models"].([]map[string]any)
	byID := make(map[string]map[string]any, len(models))
	for _, m := range models {
		byID[m["id"].(string)] = m
	}
	// 正向：Vision 标记的启动模型声明多模态输入。
	input, ok := byID["k3"]["input"].([]string)
	if !ok {
		t.Fatalf("k3 input missing or wrong type: %#v", byID["k3"]["input"])
	}
	if len(input) != 2 || input[0] != "text" || input[1] != "image" {
		t.Errorf("k3 input = %v, want [text image]", input)
	}
	// 负向：未标记的预设模型不写 input。
	if _, exists := byID["k3-plain"]["input"]; exists {
		t.Errorf("k3-plain input = %v, want absent (unmarked keeps default [text])", byID["k3-plain"]["input"])
	}
}

func TestBuildPiModelsConfigVisionLaunchedFromPresetModels(t *testing.T) {
	// 启动模型的多模态标记以 presetModels 清单为单一事实源：即使调用方传入
	// 显式 Parameters（启动权威），Vision 标记仍从同 id 清单条目继承。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "glm-5.3",
	}
	presetModels := []ManagedProviderModel{
		{ID: "glm-5.3", Parameters: config.Parameters{MaxTokens: 65536}, Vision: true},
	}
	cfg, err := BuildPiModelsConfig("glm", provider, "glm-5.3", "k", config.Parameters{MaxTokens: 2048}, presetModels)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	models := cfg["providers"].(map[string]map[string]any)["amagi-glm"]["models"].([]map[string]any)
	if len(models) != 1 {
		t.Fatalf("models len = %d, want 1: %v", len(models), models)
	}
	input, ok := models[0]["input"].([]string)
	if !ok || len(input) != 2 || input[1] != "image" {
		t.Errorf("launched vision model input = %#v, want [text image]", models[0]["input"])
	}
	if models[0]["maxTokens"] != 2048 {
		t.Errorf("maxTokens = %#v, want 2048 (launch params stay authoritative)", models[0]["maxTokens"])
	}
}

func TestBuildPiModelsConfigInfersVisionForLegacyAndBareModels(t *testing.T) {
	// diting M1 回归：legacy provider.Presets 与裸启 id 虽未进 presetModels
	// 清单，知识库已知其模型族时也必须声明 input=["text","image"]，
	// 否则下游守卫对这些注册条目误判为纯文本。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "gemini-2.5-pro", // 裸 DefaultModel，KB 已知族
		Presets: map[string]config.Preset{
			"legacy": {Model: "qwen2.5-vl-7b", Parameters: config.Parameters{MaxTokens: 4096}},
		},
	}
	cfg, err := BuildPiModelsConfig("acme", provider, "", "k", config.Parameters{}, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}
	models := cfg["providers"].(map[string]map[string]any)["amagi-acme"]["models"].([]map[string]any)
	byID := make(map[string]map[string]any, len(models))
	for _, m := range models {
		byID[m["id"].(string)] = m
	}
	for _, id := range []string{"gemini-2.5-pro", "qwen2.5-vl-7b"} {
		input, ok := byID[id]["input"].([]string)
		if !ok || len(input) != 2 || input[1] != "image" {
			t.Errorf("%s input = %#v, want [text image] via KB inference fallback", id, byID[id]["input"])
		}
	}
	// 负向：KB 未知的 legacy 模型仍不写 input（推断兜底不猜测）。
	provider2 := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "acme-text-1",
		Presets:      map[string]config.Preset{"legacy": {Model: "acme-text-2"}},
	}
	cfg2, err := BuildPiModelsConfig("acme", provider2, "", "k", config.Parameters{}, nil)
	if err != nil {
		t.Fatalf("BuildPiModelsConfig p2: %v", err)
	}
	for _, m := range cfg2["providers"].(map[string]map[string]any)["amagi-acme"]["models"].([]map[string]any) {
		if _, exists := m["input"]; exists {
			t.Errorf("unknown model %v must not declare input", m["id"])
		}
	}
}
