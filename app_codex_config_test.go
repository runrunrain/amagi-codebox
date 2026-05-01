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
	if !hasTopLevelLine(resultStr, "model_provider = \"amagi-codebox-provider\"") {
		t.Fatalf("expected top-level model_provider, got:\n%s", resultStr)
	}

	if !strings.Contains(resultStr, "base_url = \"http://api.maorun.top/v1\"") {
		t.Fatalf("model_provider section was corrupted:\n%s", resultStr)
	}

	if !strings.Contains(resultStr, "model_reasoning_effort = \"high\"") {
		t.Fatalf("model_reasoning_effort was incorrectly modified:\n%s", resultStr)
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

func TestSyncCodexConfigModel_MovesMisplacedModelProviderToTopLevel(t *testing.T) {
	codexDir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexDir, "config.toml")

	original := "model = \"gpt-5.4\"\n" +
		"\n" +
		"[ghost_snapshot]\n" +
		"disable_warnings = true\n" +
		"\n" +
		"# === amagi-codebox-inject-start ===\n" +
		"model_provider = \"amagi-codebox-provider\"\n" +
		"\n" +
		"[model_providers.amagi-codebox-provider]\n" +
		"name = \"amagi-codebox-provider\"\n" +
		"base_url = \"http://api.maorun.top/v1\"\n" +
		"# === amagi-codebox-inject-end ===\n"

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

	if !hasTopLevelLine(resultStr, "model_provider = \"amagi-codebox-provider\"") {
		t.Fatalf("model_provider was not moved to top-level:\n%s", resultStr)
	}
	if got := countExactLine(resultStr, "model_provider = \"amagi-codebox-provider\""); got != 1 {
		t.Fatalf("expected exactly one model_provider line, got %d:\n%s", got, resultStr)
	}
	if !strings.Contains(resultStr, "[model_providers.amagi-codebox-provider]") {
		t.Fatalf("model_providers section was removed:\n%s", resultStr)
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
