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
