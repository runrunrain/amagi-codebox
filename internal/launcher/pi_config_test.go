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
	agentDir := filepath.Join(dir, "pi-runtime")
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

// TestWritePiAgentConfigUpgradesLegacyPerms (审核 Major-2③) verifies that a
// pre-existing 0755 agent dir (created by older versions) is tightened to 0700
// on the next write, and an overwritten models.json ends up 0600.
func TestWritePiAgentConfigUpgradesLegacyPerms(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "pi-runtime")
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
