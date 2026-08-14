package launcher

import (
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/config"
	"gopkg.in/yaml.v3"
)

// TestWriteOmpAgentConfigTightPerms verifies the agent dir is 0700 and the
// models.yml file is 0600 (same P1-7 policy as WritePiAgentConfig), and that
// the written file is parseable YAML with the official omp shape: providers is
// a map, models is an array.
func TestWriteOmpAgentConfigTightPerms(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".omp", "agent")
	cfg := map[string]any{"providers": map[string]any{"amagi-x": map[string]any{"baseUrl": "https://x"}}}
	if err := WriteOmpAgentConfig(agentDir, cfg); err != nil {
		t.Fatalf("WriteOmpAgentConfig: %v", err)
	}
	info, err := os.Stat(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("agentDir perm = %o, want 0700", info.Mode().Perm())
	}
	mi, err := os.Stat(filepath.Join(agentDir, "models.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if mi.Mode().Perm() != 0o600 {
		t.Errorf("models.yml perm = %o, want 0600", mi.Mode().Perm())
	}
	// content is valid YAML and preserves the providers-map / models-array shape
	b, _ := os.ReadFile(filepath.Join(agentDir, "models.yml"))
	var out map[string]any
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatalf("models.yml not valid YAML: %v", err)
	}
	providers, ok := out["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers not a map: %#v", out["providers"])
	}
	entry, ok := providers["amagi-x"].(map[string]any)
	if !ok || entry["baseUrl"] != "https://x" {
		t.Fatalf("amagi-x provider missing or wrong: %#v", providers["amagi-x"])
	}
}

// TestBuildOmpModelsConfigProviderShapes verifies both provider formats map to
// the omp api values (openai-completions / anthropic-messages) and the
// amagi-<name> isolated provider id.
func TestBuildOmpModelsConfigProviderShapes(t *testing.T) {
	openAI := config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com/v1"},
	}
	cfg, err := BuildOmpModelsConfig("custom", openAI, "m", "k", config.Parameters{})
	if err != nil {
		t.Fatalf("BuildOmpModelsConfig (openai): %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-custom"]
	if entry["api"] != "openai-completions" {
		t.Errorf("openai api = %#v, want openai-completions", entry["api"])
	}
	if entry["apiKey"] != "k" {
		t.Errorf("apiKey = %#v, want k", entry["apiKey"])
	}
	if entry["baseUrl"] != "https://api.example.com/v1" {
		t.Errorf("baseUrl = %#v", entry["baseUrl"])
	}

	anthropic := config.Provider{
		Anthropic: &config.AnthropicFormat{Enabled: true, BaseURL: "https://api.example.com/anthropic"},
	}
	cfg2, err := BuildOmpModelsConfig("custom", anthropic, "m", "k", config.Parameters{})
	if err != nil {
		t.Fatalf("BuildOmpModelsConfig (anthropic): %v", err)
	}
	entry2 := cfg2["providers"].(map[string]map[string]any)["amagi-custom"]
	if entry2["api"] != "anthropic-messages" {
		t.Errorf("anthropic api = %#v, want anthropic-messages", entry2["api"])
	}

	// no baseURL -> error (same contract as pi)
	broken := config.Provider{}
	if _, err := BuildOmpModelsConfig("x", broken, "m", "k", config.Parameters{}); err == nil {
		t.Error("expected error for provider without baseURL")
	}
}

// TestBuildOmpModelsConfigKeylessAuth verifies a custom provider without an
// API key is explicitly marked keyless. omp rejects the entire models.yml when
// a provider has custom models but has neither apiKey nor auth:none.
func TestBuildOmpModelsConfigKeylessAuth(t *testing.T) {
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "http://127.0.0.1:8000/v1"},
	}

	cfg, err := BuildOmpModelsConfig("local", provider, "local-model", "", config.Parameters{})
	if err != nil {
		t.Fatalf("BuildOmpModelsConfig: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-local"]
	if entry["auth"] != "none" {
		t.Fatalf("auth = %#v, want none", entry["auth"])
	}
	if _, present := entry["apiKey"]; present {
		t.Fatalf("apiKey must be omitted for a keyless provider, got %#v", entry["apiKey"])
	}

	withKey, err := BuildOmpModelsConfig("hosted", provider, "hosted-model", "secret", config.Parameters{})
	if err != nil {
		t.Fatalf("BuildOmpModelsConfig with key: %v", err)
	}
	keyedEntry := withKey["providers"].(map[string]map[string]any)["amagi-hosted"]
	if _, present := keyedEntry["auth"]; present {
		t.Fatalf("auth must be omitted when apiKey is present, got %#v", keyedEntry["auth"])
	}
}

// TestBuildOmpModelsConfigResolvesEnvHeaders verifies $ENV: refs are resolved
// at build time, literals pass through, unset refs are omitted (same P1-7
// contract as BuildPiModelsConfig).
func TestBuildOmpModelsConfigResolvesEnvHeaders(t *testing.T) {
	t.Setenv("AMAGI_OMP_HDR_RESOLVED", "resolved-secret")
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{
			Enabled: true,
			BaseURL: "https://api.example.com",
			Headers: map[string]string{
				"X-Resolved": "$ENV:AMAGI_OMP_HDR_RESOLVED",
				"X-Plain":    "literal",
				"X-Unset":    "$ENV:DEFINITELY_UNSET_AMAGI_VAR",
			},
		},
	}
	cfg, err := BuildOmpModelsConfig("custom", provider, "m", "k", config.Parameters{})
	if err != nil {
		t.Fatalf("BuildOmpModelsConfig: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-custom"]
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

// TestBuildOmpModelsConfigThinkingLevelMap verifies the omp model config:
// thinking enabled -> reasoning=true + thinkingLevelMap xhigh/max; disabled ->
// neither field; contextWindow/maxTokens passthrough.
func TestBuildOmpModelsConfigThinkingLevelMap(t *testing.T) {
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
	}
	enabled := config.Parameters{
		Thinking:      &config.ThinkingConfig{Type: "enabled"},
		ContextWindow: &config.ContextWindowConfig{ModelContextWindow: 128000},
		MaxTokens:     8192,
	}
	cfg, err := BuildOmpModelsConfig("kimi", provider, "k3-256k", "k", enabled)
	if err != nil {
		t.Fatalf("BuildOmpModelsConfig: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-kimi"]
	models := entry["models"].([]map[string]any)
	if len(models) != 1 {
		t.Fatalf("models len = %d, want 1", len(models))
	}
	if models[0]["reasoning"] != true {
		t.Errorf("reasoning = %#v, want true", models[0]["reasoning"])
	}
	if models[0]["contextWindow"] != 128000 {
		t.Errorf("contextWindow = %#v, want 128000", models[0]["contextWindow"])
	}
	if models[0]["maxTokens"] != 8192 {
		t.Errorf("maxTokens = %#v, want 8192", models[0]["maxTokens"])
	}
	levelMap, ok := models[0]["thinkingLevelMap"].(map[string]any)
	if !ok {
		t.Fatalf("thinkingLevelMap missing or wrong type: %#v", models[0]["thinkingLevelMap"])
	}
	if levelMap["xhigh"] != "xhigh" || levelMap["max"] != "max" {
		t.Errorf("thinkingLevelMap = %#v, want xhigh/max identity", levelMap)
	}

	// thinking disabled: no reasoning / thinkingLevelMap
	cfgOff, err := BuildOmpModelsConfig("kimi", provider, "k3-256k", "k", config.Parameters{})
	if err != nil {
		t.Fatalf("BuildOmpModelsConfig (no thinking): %v", err)
	}
	mOff := cfgOff["providers"].(map[string]map[string]any)["amagi-kimi"]["models"].([]map[string]any)[0]
	if _, present := mOff["reasoning"]; present {
		t.Errorf("reasoning must be omitted when thinking disabled, got %#v", mOff["reasoning"])
	}
	if _, present := mOff["thinkingLevelMap"]; present {
		t.Errorf("thinkingLevelMap must be omitted when thinking disabled, got %#v", mOff["thinkingLevelMap"])
	}
}

// TestBuildOmpModelsConfigCompatDefaults verifies supportsDeveloperRole defaults
// to false and pi_compat explicit overrides win (same semantics as pi).
func TestBuildOmpModelsConfigCompatDefaults(t *testing.T) {
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.kimi.com/coding/v1"},
	}

	cfg, err := BuildOmpModelsConfig("kimi", provider, "k3-256k", "k", config.Parameters{})
	if err != nil {
		t.Fatalf("BuildOmpModelsConfig: %v", err)
	}
	m := cfg["providers"].(map[string]map[string]any)["amagi-kimi"]["models"].([]map[string]any)[0]
	compat, ok := m["compat"].(map[string]any)
	if !ok {
		t.Fatalf("compat missing or wrong type: %#v", m["compat"])
	}
	if compat["supportsDeveloperRole"] != false {
		t.Errorf("supportsDeveloperRole = %#v, want false", compat["supportsDeveloperRole"])
	}

	cfg2, err := BuildOmpModelsConfig("kimi", provider, "k3-256k", "k", config.Parameters{
		PiCompat: map[string]any{
			"supportsDeveloperRole":   true,
			"supportsReasoningEffort": false,
		},
	})
	if err != nil {
		t.Fatalf("BuildOmpModelsConfig (override): %v", err)
	}
	compat2 := cfg2["providers"].(map[string]map[string]any)["amagi-kimi"]["models"].([]map[string]any)[0]["compat"].(map[string]any)
	if compat2["supportsDeveloperRole"] != true {
		t.Errorf("explicit supportsDeveloperRole=true overridden, got %#v", compat2["supportsDeveloperRole"])
	}
	if compat2["supportsReasoningEffort"] != false {
		t.Errorf("supportsReasoningEffort not passed through, got %#v", compat2["supportsReasoningEffort"])
	}
}

// TestWriteOmpAgentConfigUpgradesLegacyPerms verifies a pre-existing 0755 agent
// dir is tightened to 0700 and an overwritten models.yml ends up 0600.
func TestWriteOmpAgentConfigUpgradesLegacyPerms(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".omp", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(agentDir, "models.yml")
	if err := os.WriteFile(legacy, []byte("providers: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{"providers": map[string]any{}}
	if err := WriteOmpAgentConfig(agentDir, cfg); err != nil {
		t.Fatalf("WriteOmpAgentConfig: %v", err)
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
		t.Errorf("overwritten models.yml perm = %o, want 0600", mi.Mode().Perm())
	}
}

// TestMergeOmpModelsConfigPreservesUserProviders verifies user-defined
// providers and other top-level fields (equivalence etc.) survive the merge,
// while sibling amagi-managed providers survive the launch-time overlay and the
// current one wins on name collision. Full stale-entry pruning is covered by
// ReconcileOmpAgentConfig/provider synchronization.
func TestMergeOmpModelsConfigPreservesUserProviders(t *testing.T) {
	agentDir := t.TempDir()
	existing := map[string]any{
		"equivalence": map[string]any{"anthropic/claude-x": "openrouter/foo"},
		"providers": map[string]any{
			"existing":     map[string]any{"baseUrl": "https://existing.example"},
			"amagi-new":    map[string]any{"baseUrl": "https://stale.example"},
			"amagi-broken": map[string]any{"baseUrl": "https://broken.example"},
		},
	}
	data, err := yaml.Marshal(existing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.yml"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{
		"providers": map[string]map[string]any{
			"amagi-new": {"baseUrl": "https://fresh.example"},
		},
	}
	merged := MergeOmpModelsConfig(cfg, agentDir)
	if _, ok := merged["equivalence"]; !ok {
		t.Fatalf("existing top-level config was not preserved: %#v", merged)
	}
	providers, ok := merged["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers type = %T, want map[string]any", merged["providers"])
	}
	if _, ok := providers["existing"]; !ok {
		t.Fatalf("existing provider was not preserved: %#v", providers)
	}
	if _, ok := providers["amagi-broken"]; !ok {
		t.Fatalf("sibling managed provider was removed by launch overlay: %#v", providers)
	}
	managed, ok := providers["amagi-new"].(map[string]any)
	if !ok || managed["baseUrl"] != "https://fresh.example" {
		t.Fatalf("managed provider did not override stale entry: %#v", providers["amagi-new"])
	}
	if _, changed := cfg["equivalence"]; changed {
		t.Fatal("input cfg was mutated")
	}
}

// TestMergeOmpModelsConfigNoExistingFile verifies a missing models.yml falls
// back to the input config unchanged (readOmpYAMLObject tolerates absence).
func TestMergeOmpModelsConfigNoExistingFile(t *testing.T) {
	cfg := map[string]any{
		"providers": map[string]map[string]any{
			"amagi-new": {"baseUrl": "https://fresh.example"},
		},
	}
	merged := MergeOmpModelsConfig(cfg, t.TempDir())
	if merged["providers"] == nil {
		t.Fatal("providers must survive when no existing models.yml")
	}
	if _, ok := merged["providers"].(map[string]map[string]any); !ok {
		t.Fatalf("providers type = %T, want map[string]map[string]any", merged["providers"])
	}
}
