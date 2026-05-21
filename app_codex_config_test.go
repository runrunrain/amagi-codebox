package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncCodexConfigModel(t *testing.T) {
	codexDir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexDir, "config.toml")

	original := "# header\n" +
		"model = \"gpt-5.4\"\n" +
		"\n" +
		"model_reasoning_effort = \"high\"\n" +
		"\n" +
		"[model_providers.amagi-codebox-provider]\n" +
		"name = \"amagi-codebox-provider\"\n" +
		"base_url = \"http://api.maorun.top/v1\"\n"

	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	setTestUserHome(t, filepath.Dir(codexDir))

	if err := syncCodexConfigModel("gpt-5.5"); err != nil {
		t.Fatalf("syncCodexConfigModel: %v", err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	if !strings.Contains(resultStr, "model = \"gpt-5.5\"") {
		t.Fatalf("expected model updated to gpt-5.5, got:\n%s", resultStr)
	}
	if hasTopLevelLine(resultStr, "model_provider = \"amagi-codebox-provider\"") {
		t.Fatalf("model-only sync should not force a custom model_provider, got:\n%s", resultStr)
	}

	if strings.Contains(resultStr, "amagi-codebox-provider") || strings.Contains(resultStr, "http://api.maorun.top/v1") {
		t.Fatalf("model-only sync should clean up amagi managed provider state, got:\n%s", resultStr)
	}

	if !strings.Contains(resultStr, "model_reasoning_effort = \"high\"") {
		t.Fatalf("model_reasoning_effort was incorrectly modified:\n%s", resultStr)
	}
}

func TestSyncCodexConfigModel_CleansManagedCustomProviderAfterOfficialSync(t *testing.T) {
	codexDir := filepath.Join(t.TempDir(), ".codex")
	configPath := filepath.Join(codexDir, "config.toml")

	setTestUserHome(t, filepath.Dir(codexDir))

	if err := syncCodexCustomProviderConfig("gpt-5.5", "https://proxy.example.com/v1"); err != nil {
		t.Fatalf("syncCodexCustomProviderConfig: %v", err)
	}
	customData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	customConfig := string(customData)
	if !strings.Contains(customConfig, "model_provider = \"amagi-codebox-provider\"") || !strings.Contains(customConfig, "forced_login_method = \"api\"") {
		t.Fatalf("custom sync did not create managed provider state:\n%s", customConfig)
	}

	if err := syncCodexConfigModel("gpt-5.6"); err != nil {
		t.Fatalf("syncCodexConfigModel: %v", err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	if !hasTopLevelLine(resultStr, "model = \"gpt-5.6\"") {
		t.Fatalf("official sync did not update top-level model:\n%s", resultStr)
	}
	for _, forbidden := range []string{
		"model_provider = \"amagi-codebox-provider\"",
		"forced_login_method = \"api\"",
		"[model_providers.amagi-codebox-provider]",
		"base_url = \"https://proxy.example.com/v1\"",
		"env_key = \"OPENAI_API_KEY\"",
	} {
		if strings.Contains(resultStr, forbidden) {
			t.Fatalf("official/no BaseURL sync should remove %q, got:\n%s", forbidden, resultStr)
		}
	}
}

func TestSyncCodexConfigModel_PreservesUserOfficialLoginConfig(t *testing.T) {
	codexDir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexDir, "config.toml")

	original := "model = \"gpt-5.4\"\n" +
		"model_provider = \"openai\"\n" +
		"forced_login_method = \"api\"\n" +
		"approval_policy = \"never\"\n" +
		"\n" +
		"[model_providers.other-provider]\n" +
		"base_url = \"https://other.example.com/v1\"\n"

	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	setTestUserHome(t, filepath.Dir(codexDir))

	if err := syncCodexConfigModel("gpt-5.5"); err != nil {
		t.Fatalf("syncCodexConfigModel: %v", err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	for _, want := range []string{
		"model = \"gpt-5.5\"",
		"model_provider = \"openai\"",
		"forced_login_method = \"api\"",
		"approval_policy = \"never\"",
		"[model_providers.other-provider]",
		"base_url = \"https://other.example.com/v1\"",
	} {
		if !strings.Contains(resultStr, want) {
			t.Fatalf("expected user config %q to be preserved/updated:\n%s", want, resultStr)
		}
	}
}

func TestSyncCodexConfigModel_ModelNotFound(t *testing.T) {
	codexDir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexDir, "config.toml")

	original := "# no model field here\napproval_policy = \"never\"\n"
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	setTestUserHome(t, filepath.Dir(codexDir))

	err := syncCodexConfigModel("gpt-5.5")
	if err == nil {
		t.Fatal("expected error when model field not found")
	}
	if !strings.Contains(err.Error(), "top-level model field not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncCodexConfigModel_DoesNotMatchModelInSections(t *testing.T) {
	codexDir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexDir, "config.toml")

	original := "model = \"gpt-5.4\"\n" +
		"\n" +
		"[profiles.minimal]\n" +
		"model = \"codex-mini-latest\"\n"

	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	setTestUserHome(t, filepath.Dir(codexDir))

	if err := syncCodexConfigModel("gpt-5.5"); err != nil {
		t.Fatalf("syncCodexConfigModel: %v", err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	if !strings.Contains(resultStr, "model = \"gpt-5.5\"") {
		t.Fatalf("top-level model not updated:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "model = \"codex-mini-latest\"") {
		t.Fatalf("section model was incorrectly modified:\n%s", resultStr)
	}
}

func TestSyncCodexCustomProviderConfig_CreatesMissingConfig(t *testing.T) {
	codexDir := filepath.Join(t.TempDir(), ".codex")
	configPath := filepath.Join(codexDir, "config.toml")

	setTestUserHome(t, filepath.Dir(codexDir))

	if err := syncCodexCustomProviderConfig("gpt-5.5", "https://proxy.example.com/v1"); err != nil {
		t.Fatalf("syncCodexCustomProviderConfig: %v", err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	for _, want := range []string{
		"model = \"gpt-5.5\"",
		"model_provider = \"amagi-codebox-provider\"",
		"forced_login_method = \"api\"",
		"[model_providers.amagi-codebox-provider]",
		"name = \"amagi-codebox-provider\"",
		"base_url = \"https://proxy.example.com/v1\"",
		"env_key = \"OPENAI_API_KEY\"",
		"requires_openai_auth = false",
		"wire_api = \"responses\"",
	} {
		if !strings.Contains(resultStr, want) {
			t.Fatalf("expected %q in generated config:\n%s", want, resultStr)
		}
	}
	if strings.Contains(resultStr, "sk-") {
		t.Fatalf("config.toml must not contain API key material:\n%s", resultStr)
	}
}

func TestSyncCodexCustomProviderConfig_UpdatesProviderSectionAndPreservesOtherConfig(t *testing.T) {
	codexDir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexDir, "config.toml")

	original := "model = \"gpt-5.4\"\n" +
		"model_provider = \"openai\"\n" +
		"forced_login_method = \"chatgpt\"\n" +
		"approval_policy = \"never\"\n" +
		"\n" +
		"[ghost_snapshot]\n" +
		"disable_warnings = true\n" +
		"\n" +
		"# === amagi-codebox-inject-start ===\n" +
		"model_provider = \"amagi-codebox-provider\"\n" +
		"\n" +
		"[model_providers.amagi-codebox-provider]\n" +
		"name = \"amagi-codebox-provider\"\n" +
		"base_url = \"https://old.example.com/v1\"\n" +
		"env_key = \"OLD_KEY\"\n" +
		"# === amagi-codebox-inject-end ===\n" +
		"\n" +
		"[model_providers.amagi-codebox-provider.auth]\n" +
		"command = \"old-auth-command\"\n" +
		"\n" +
		"[model_providers.other-provider]\n" +
		"base_url = \"https://other.example.com/v1\"\n"

	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	setTestUserHome(t, filepath.Dir(codexDir))

	if err := syncCodexCustomProviderConfig("gpt-5.5", "https://proxy.example.com/v1"); err != nil {
		t.Fatalf("syncCodexCustomProviderConfig: %v", err)
	}

	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	resultStr := string(result)

	if !hasTopLevelLine(resultStr, "model_provider = \"amagi-codebox-provider\"") {
		t.Fatalf("model_provider was not set at top-level:\n%s", resultStr)
	}
	if got := countExactLine(resultStr, "model_provider = \"amagi-codebox-provider\""); got != 1 {
		t.Fatalf("expected exactly one model_provider line, got %d:\n%s", got, resultStr)
	}
	for _, want := range []string{
		"model = \"gpt-5.5\"",
		"forced_login_method = \"api\"",
		"approval_policy = \"never\"",
		"[ghost_snapshot]",
		"disable_warnings = true",
		"[model_providers.other-provider]",
		"base_url = \"https://other.example.com/v1\"",
		"[model_providers.amagi-codebox-provider]",
		"base_url = \"https://proxy.example.com/v1\"",
		"env_key = \"OPENAI_API_KEY\"",
		"requires_openai_auth = false",
		"wire_api = \"responses\"",
	} {
		if !strings.Contains(resultStr, want) {
			t.Fatalf("expected %q in updated config:\n%s", want, resultStr)
		}
	}
	for _, forbidden := range []string{"https://old.example.com/v1", "env_key = \"OLD_KEY\"", "forced_login_method = \"chatgpt\"", "old-auth-command"} {
		if strings.Contains(resultStr, forbidden) {
			t.Fatalf("stale managed value %q should have been replaced:\n%s", forbidden, resultStr)
		}
	}
}

func TestIsCustomCodexOpenAIBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "empty", baseURL: "", want: false},
		{name: "official root", baseURL: "https://api.openai.com", want: false},
		{name: "official v1", baseURL: "https://api.openai.com/v1", want: false},
		{name: "official v1 trailing slash", baseURL: "https://api.openai.com/v1/", want: false},
		{name: "official without scheme", baseURL: "api.openai.com/v1", want: false},
		{name: "proxy host", baseURL: "https://proxy.example.com/v1", want: true},
		{name: "localhost proxy", baseURL: "http://localhost:11434/v1", want: true},
		{name: "openai host non-default path", baseURL: "https://api.openai.com/custom/v1", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCustomCodexOpenAIBaseURL(tt.baseURL); got != tt.want {
				t.Fatalf("isCustomCodexOpenAIBaseURL(%q) = %v, want %v", tt.baseURL, got, tt.want)
			}
		})
	}
}

func hasTopLevelLine(content, want string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			return false
		}
		if trimmed == want {
			return true
		}
	}
	return false
}

func countExactLine(content, want string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == want {
			count++
		}
	}
	return count
}

func setTestUserHome(t *testing.T, home string) {
	t.Helper()
	for _, key := range []string{"HOME", "USERPROFILE"} {
		key := key
		oldValue, hadValue := os.LookupEnv(key)
		if err := os.Setenv(key, home); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
		t.Cleanup(func() {
			if hadValue {
				_ = os.Setenv(key, oldValue)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}
