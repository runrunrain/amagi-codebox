package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/opencodeconfig"
	"gopkg.in/yaml.v3"
)

func TestProviderMutationsReconcileHarnessesAndPreserveAuthenticationData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	app, _ := newTestAppWithConfigDir(t)
	piDir := filepath.Join(home, ".pi", "agent")
	ompDir := filepath.Join(home, ".omp", "agent")
	openCodeDir := filepath.Join(home, ".config", "opencode")
	for _, dir := range []string{piDir, ompDir, openCodeDir, filepath.Join(home, ".local", "share", "opencode")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	piAuthPath := filepath.Join(piDir, "auth.json")
	ompAuthPath := filepath.Join(ompDir, "agent.db")
	openCodeAuthPath := filepath.Join(home, ".local", "share", "opencode", "auth.json")
	for path, content := range map[string]string{
		piAuthPath:       `{"anthropic":{"type":"oauth"}}`,
		ompAuthPath:      "opaque-auth-database",
		openCodeAuthPath: `{"anthropic":{"type":"oauth"}}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	piModels := `{"version":2,"providers":{"native-login":{"api":"anthropic-messages"},"amagi-stale":{"baseUrl":"https://stale"}}}`
	if err := os.WriteFile(filepath.Join(piDir, "models.json"), []byte(piModels), 0o600); err != nil {
		t.Fatal(err)
	}
	ompModels := "equivalence:\n  keep: value\nproviders:\n  native-login:\n    api: anthropic-messages\n  amagi-stale:\n    baseUrl: https://stale\n"
	if err := os.WriteFile(filepath.Join(ompDir, "models.yml"), []byte(ompModels), 0o600); err != nil {
		t.Fatal(err)
	}
	openCodeConfig := `{"theme":"dark","provider":{"anthropic":{"options":{"oauth":true}},"amagi-stale":{"options":{"apiKey":"old"}}}}`
	if err := os.WriteFile(filepath.Join(openCodeDir, "opencode.json"), []byte(openCodeConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	app.OpenCodeConfig = opencodeconfig.NewService()
	app.providerSyncEnabled = true
	app.providerSyncPiDir = piDir
	app.providerSyncOmpDir = ompDir
	if err := app.Config.SaveTerminalPreset("openai", "reasoning", config.TerminalPreset{
		Provider: "unified", Model: "model-reasoning", Parameters: config.Parameters{MaxTokens: 8192},
	}); err != nil {
		t.Fatal(err)
	}

	providerJSON := `{
  "default_model":"model-default",
  "api_key":"sk-unified",
  "openai":{"enabled":true,"base_url":"https://api.example.com/v1"}
}`
	if err := app.SaveProviderFromJSON("unified", providerJSON); err != nil {
		t.Fatalf("SaveProviderFromJSON: %v", err)
	}

	assertHarnessAuthFileUnchanged(t, piAuthPath, `{"anthropic":{"type":"oauth"}}`)
	assertHarnessAuthFileUnchanged(t, ompAuthPath, "opaque-auth-database")
	assertHarnessAuthFileUnchanged(t, openCodeAuthPath, `{"anthropic":{"type":"oauth"}}`)

	piData, err := os.ReadFile(filepath.Join(piDir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var piRoot map[string]any
	if err := json.Unmarshal(piData, &piRoot); err != nil {
		t.Fatal(err)
	}
	piProviders := piRoot["providers"].(map[string]any)
	assertManagedAndNativeProviders(t, piProviders)
	piManaged := piProviders["amagi-unified"].(map[string]any)
	if piManaged["apiKey"] != "sk-unified" {
		t.Fatalf("Pi API key was not synchronized: %#v", piManaged)
	}
	if models := piManaged["models"].([]any); len(models) != 2 {
		t.Fatalf("Pi models len = %d, want default + preset", len(models))
	}

	ompData, err := os.ReadFile(filepath.Join(ompDir, "models.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var ompRoot map[string]any
	if err := yaml.Unmarshal(ompData, &ompRoot); err != nil {
		t.Fatal(err)
	}
	ompProviders := ompRoot["providers"].(map[string]any)
	assertManagedAndNativeProviders(t, ompProviders)
	if _, preserved := ompRoot["equivalence"]; !preserved {
		t.Fatal("OMP top-level equivalence was removed")
	}

	ocData, err := os.ReadFile(filepath.Join(openCodeDir, "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ocRoot map[string]any
	if err := json.Unmarshal(ocData, &ocRoot); err != nil {
		t.Fatal(err)
	}
	ocProviders := ocRoot["provider"].(map[string]any)
	if _, exists := ocProviders["anthropic"]; !exists {
		t.Fatal("OpenCode login-backed provider was removed")
	}
	if _, exists := ocProviders["amagi-stale"]; exists {
		t.Fatal("OpenCode stale managed provider was preserved")
	}
	ocManaged := ocProviders["amagi-unified"].(map[string]any)
	if ocManaged["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("OpenCode provider schema mismatch: %#v", ocManaged)
	}

	clearKeyJSON := `{
  "default_model":"model-default",
  "clear_api_key":true,
  "openai":{"enabled":true,"base_url":"https://api.example.com/v1"}
}`
	if err := app.SaveProviderFromJSON("unified", clearKeyJSON); err != nil {
		t.Fatalf("clear provider API key: %v", err)
	}
	piData, _ = os.ReadFile(filepath.Join(piDir, "models.json"))
	if err := json.Unmarshal(piData, &piRoot); err != nil {
		t.Fatal(err)
	}
	piProviders = piRoot["providers"].(map[string]any)
	if _, exists := piProviders["amagi-unified"].(map[string]any)["apiKey"]; exists {
		t.Fatal("cleared API key remains in Pi config")
	}
	ocData, _ = os.ReadFile(filepath.Join(openCodeDir, "opencode.json"))
	if err := json.Unmarshal(ocData, &ocRoot); err != nil {
		t.Fatal(err)
	}
	ocProviders = ocRoot["provider"].(map[string]any)
	if _, exists := ocProviders["amagi-unified"].(map[string]any)["options"].(map[string]any)["apiKey"]; exists {
		t.Fatal("cleared API key remains in OpenCode config")
	}

	if err := app.DeleteProvider("unified"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	piData, _ = os.ReadFile(filepath.Join(piDir, "models.json"))
	if err := json.Unmarshal(piData, &piRoot); err != nil {
		t.Fatal(err)
	}
	piProviders = piRoot["providers"].(map[string]any)
	if _, exists := piProviders["amagi-unified"]; exists {
		t.Fatal("deleted provider remains in Pi config")
	}
	if _, exists := piProviders["native-login"]; !exists {
		t.Fatal("Pi native provider was removed during deletion")
	}
}

func TestProviderSyncPiOmpConsumeOnlyOpenAIBucketPresets(t *testing.T) {
	// 回归（实测用户配置）：双桶预设下，pi/omp 只消费 openai 公共预设桶——
	// 1) anthropic 桶专属预设模型（Claude Code 专用）不得写入 pi/omp models；
	// 2) openai 桶预设参数（DefaultModel 同 id）必须完整保留，不被裸注册剥掉；
	// 3) OpenCode 双桶语义保持不变。
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	app, _ := newTestAppWithConfigDir(t)
	piDir := filepath.Join(home, ".pi", "agent")
	ompDir := filepath.Join(home, ".omp", "agent")
	openCodeDir := filepath.Join(home, ".config", "opencode")
	for _, dir := range []string{piDir, ompDir, openCodeDir, filepath.Join(home, ".local", "share", "opencode")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	app.OpenCodeConfig = opencodeconfig.NewService()
	app.providerSyncEnabled = true
	app.providerSyncPiDir = piDir
	app.providerSyncOmpDir = ompDir

	providerJSON := `{"default_model":"glm-5.3","api_key":"sk-glm","openai":{"enabled":true,"base_url":"https://open.bigmodel.cn/api/coding/paas/v4"}}`
	if err := app.SaveProviderFromJSON("glm", providerJSON); err != nil {
		t.Fatalf("SaveProviderFromJSON: %v", err)
	}
	// anthropic 桶：Claude Code 专属预设（glm-5-turbo / glm-5.3[1m]）。
	if err := app.SaveTerminalPreset("anthropic", "agent", config.TerminalPreset{
		Provider: "glm", Model: "glm-5-turbo", Parameters: config.Parameters{ReasoningEffort: "max"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveTerminalPreset("anthropic", "code", config.TerminalPreset{
		Provider: "glm", Model: "glm-5.3[1m]", Parameters: config.Parameters{MaxTokens: 131072},
	}); err != nil {
		t.Fatal(err)
	}
	// openai 桶：pi/omp 消费的预设（glm-5.3，同 DefaultModel id，带完整参数）。
	// 经 App 层 SaveTerminalPreset 保存，触发统一同步。
	if err := app.SaveTerminalPreset("openai", "default", config.TerminalPreset{
		Provider: "glm", Model: "glm-5.3", Parameters: config.Parameters{
			MaxTokens:       65536,
			ContextWindow:   &config.ContextWindowConfig{ModelContextWindow: 1000000},
			ReasoningEffort: "max",
		},
	}); err != nil {
		t.Fatal(err)
	}

	piData, err := os.ReadFile(filepath.Join(piDir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var piRoot map[string]any
	if err := json.Unmarshal(piData, &piRoot); err != nil {
		t.Fatal(err)
	}
	piModels := piRoot["providers"].(map[string]any)["amagi-glm"].(map[string]any)["models"].([]any)
	piIDs := map[string]map[string]any{}
	for _, raw := range piModels {
		m := raw.(map[string]any)
		piIDs[m["id"].(string)] = m
	}
	if _, leaked := piIDs["glm-5-turbo"]; leaked {
		t.Errorf("anthropic-bucket-only model glm-5-turbo leaked into pi models.json: %v", piModels)
	}
	if _, leaked := piIDs["glm-5.3[1m]"]; leaked {
		t.Errorf("anthropic-bucket-only model glm-5.3[1m] leaked into pi models.json: %v", piModels)
	}
	glm53, ok := piIDs["glm-5.3"]
	if !ok {
		t.Fatalf("openai preset model glm-5.3 missing from pi models.json: %v", piModels)
	}
	if asInt(t, glm53["maxTokens"]) != 65536 || asInt(t, glm53["contextWindow"]) != 1000000 || glm53["reasoning"] != true {
		t.Errorf("glm-5.3 lost openai preset params in pi models.json: %v", glm53)
	}

	ompData, err := os.ReadFile(filepath.Join(ompDir, "models.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var ompRoot map[string]any
	if err := yaml.Unmarshal(ompData, &ompRoot); err != nil {
		t.Fatal(err)
	}
	ompModels := ompRoot["providers"].(map[string]any)["amagi-glm"].(map[string]any)["models"].([]any)
	ompIDs := map[string]map[string]any{}
	for _, raw := range ompModels {
		m := raw.(map[string]any)
		ompIDs[m["id"].(string)] = m
	}
	if _, leaked := ompIDs["glm-5-turbo"]; leaked {
		t.Errorf("anthropic-bucket-only model glm-5-turbo leaked into omp models.yml: %v", ompModels)
	}
	if omp53, ok := ompIDs["glm-5.3"]; !ok || asInt(t, omp53["maxTokens"]) != 65536 || omp53["reasoning"] != true {
		t.Fatalf("glm-5.3 lost openai preset params in omp models.yml: %v", ompModels)
	}

	ocData, err := os.ReadFile(filepath.Join(openCodeDir, "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ocRoot map[string]any
	if err := json.Unmarshal(ocData, &ocRoot); err != nil {
		t.Fatal(err)
	}
	ocManaged := ocRoot["provider"].(map[string]any)["amagi-glm"].(map[string]any)
	ocModelsRaw, ok := ocManaged["models"].(map[string]any)
	if !ok {
		t.Fatalf("opencode managed provider models missing: %v", ocManaged)
	}
	for _, want := range []string{"glm-5-turbo", "glm-5.3", "glm-5.3[1m]"} {
		if _, exists := ocModelsRaw[want]; !exists {
			t.Errorf("OpenCode keeps both preset buckets: model %s missing from opencode.json: %v", want, ocModelsRaw)
		}
	}
}

// asInt normalizes JSON/YAML-decoded numeric fields (float64 / int / int64)
// for comparison.
func asInt(t *testing.T, v any) int {
	t.Helper()
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		t.Fatalf("unexpected numeric type %T: %v", v, v)
		return 0
	}
}

func assertHarnessAuthFileUnchanged(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("authentication data %s changed: %q", path, data)
	}
}

func assertManagedAndNativeProviders(t *testing.T, providers map[string]any) {
	t.Helper()
	if _, exists := providers["native-login"]; !exists {
		t.Fatalf("native/login provider was removed: %#v", providers)
	}
	if _, exists := providers["amagi-stale"]; exists {
		t.Fatalf("stale managed provider was preserved: %#v", providers)
	}
	if _, exists := providers["amagi-unified"]; !exists {
		t.Fatalf("managed provider missing: %#v", providers)
	}
}
