package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/config"
)

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
	cfg, err := BuildPiModelsConfig("glm", provider, "glm-5.3", "test-key", params)
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
	cfg, err := BuildPiModelsConfig("custom", provider, "m", "k", config.Parameters{})
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
	cfg, err := BuildPiModelsConfig("kimi", provider, "k3-256k", "k", enabled)
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
	cfgOff, err := BuildPiModelsConfig("kimi", provider, "k3-256k", "k", config.Parameters{})
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
	cfg, err := BuildPiModelsConfig("kimi", provider, "k3-256k", "k", config.Parameters{})
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
	})
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
	cfg, err := BuildPiModelsConfig("multi", provider, "model-b", "k", launchedParams)
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
	cfg, err := BuildOmpModelsConfig("m", provider, "flash-x", "k", config.Parameters{})
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
	cfg, err := BuildPiModelsConfig("glm", provider, "", "k", config.Parameters{})
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
	cfg2, err := BuildPiModelsConfig("glm", provider, "glm-5.3", "k", config.Parameters{MaxTokens: 2048})
	if err != nil {
		t.Fatalf("BuildPiModelsConfig explicit: %v", err)
	}
	m2 := cfg2["providers"].(map[string]map[string]any)["amagi-glm"]["models"].([]map[string]any)[0]
	if m2["maxTokens"] != 2048 {
		t.Errorf("explicit maxTokens = %#v, want 2048 (launch params authoritative)", m2["maxTokens"])
	}
}
