package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// --- Codex global headroom openai_base_url marker block tests ---

// newCodexTestConfig writes the given content to <tmpHome>/.codex/config.toml
// and points HOME at tmpHome. Returns the config path.
func newCodexTestConfig(t *testing.T, content string) (configPath string) {
	t.Helper()
	codexDir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath = filepath.Join(codexDir, "config.toml")
	if content != "" {
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setTestUserHome(t, filepath.Dir(codexDir))
	return configPath
}

// readCodexTestConfig reads the config file back; fails the test on error.
func readCodexTestConfig(t *testing.T, configPath string) string {
	t.Helper()
	b, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSyncCodexGlobalHeadroomConfig_AddInsertsBlock verifies enabling writes the
// openai_base_url marker block pointing at the given port.
func TestSyncCodexGlobalHeadroomConfig_AddInsertsBlock(t *testing.T) {
	configPath := newCodexTestConfig(t, "model = \"gpt-5.6-sol\"\n"+
		"model_reasoning_effort = \"high\"\n"+
		"\n"+
		"[profiles.minimal]\n"+
		"model = \"mini\"\n")

	if err := syncCodexGlobalHeadroomConfig(true, 8788); err != nil {
		t.Fatalf("syncCodexGlobalHeadroomConfig(true): %v", err)
	}
	got := readCodexTestConfig(t, configPath)

	// The marker block must be present with the correct base URL.
	if !strings.Contains(got, "# === amagi-headroom-global-start ===") {
		t.Fatalf("missing start marker:\n%s", got)
	}
	if !strings.Contains(got, "# === amagi-headroom-global-end ===") {
		t.Fatalf("missing end marker:\n%s", got)
	}
	if !strings.Contains(got, "openai_base_url = \"http://127.0.0.1:8788/v1\"") {
		t.Fatalf("missing openai_base_url line:\n%s", got)
	}
	// Exactly one top-level openai_base_url (no duplicate key).
	if c := countExactLine(got, "openai_base_url = \"http://127.0.0.1:8788/v1\""); c != 1 {
		t.Fatalf("expected exactly one openai_base_url line, got %d:\n%s", c, got)
	}
	// Existing user config must be preserved.
	for _, want := range []string{
		"model = \"gpt-5.6-sol\"",
		"model_reasoning_effort = \"high\"",
		"[profiles.minimal]",
		"model = \"mini\"",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q preserved:\n%s", want, got)
		}
	}
	// The marker block must live BEFORE the first section header so the
	// openai_base_url stays a top-level key in TOML.
	startIdx := strings.Index(got, "# === amagi-headroom-global-start ===")
	sectionIdx := strings.Index(got, "[profiles.minimal]")
	if startIdx == -1 || sectionIdx == -1 || startIdx > sectionIdx {
		t.Fatalf("marker block must precede the first section header:\n%s", got)
	}
}

// TestSyncCodexGlobalHeadroomConfig_CreatesMissingConfig verifies enabling on a
// missing config.toml creates it with the marker block.
func TestSyncCodexGlobalHeadroomConfig_CreatesMissingConfig(t *testing.T) {
	configPath := newCodexTestConfig(t, "")

	if err := syncCodexGlobalHeadroomConfig(true, 8788); err != nil {
		t.Fatalf("syncCodexGlobalHeadroomConfig(true): %v", err)
	}
	got := readCodexTestConfig(t, configPath)
	if !strings.Contains(got, "openai_base_url = \"http://127.0.0.1:8788/v1\"") {
		t.Fatalf("missing openai_base_url line in created config:\n%s", got)
	}
}

// TestSyncCodexGlobalHeadroomConfig_RemoveDeletesBlock verifies disabling
// removes the entire marker block (inclusive) while preserving everything else.
func TestSyncCodexGlobalHeadroomConfig_RemoveDeletesBlock(t *testing.T) {
	original := "model = \"gpt-5.6-sol\"\n" +
		"\n" +
		"# === amagi-headroom-global-start ===\n" +
		"openai_base_url = \"http://127.0.0.1:8788/v1\"\n" +
		"# === amagi-headroom-global-end ===\n" +
		"\n" +
		"[profiles.minimal]\n" +
		"model = \"mini\"\n"
	configPath := newCodexTestConfig(t, original)

	if err := syncCodexGlobalHeadroomConfig(false, 0); err != nil {
		t.Fatalf("syncCodexGlobalHeadroomConfig(false): %v", err)
	}
	got := readCodexTestConfig(t, configPath)

	for _, forbidden := range []string{
		"# === amagi-headroom-global-start ===",
		"# === amagi-headroom-global-end ===",
		"openai_base_url",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("disable should remove %q:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{
		"model = \"gpt-5.6-sol\"",
		"[profiles.minimal]",
		"model = \"mini\"",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q preserved after disable:\n%s", want, got)
		}
	}
}

// TestSyncCodexGlobalHeadroomConfig_DisableOnMissingConfigIsNoop verifies the
// disable path is idempotent and does not error/fabricate a file when none
// exists.
func TestSyncCodexGlobalHeadroomConfig_DisableOnMissingConfigIsNoop(t *testing.T) {
	configPath := newCodexTestConfig(t, "")
	if err := syncCodexGlobalHeadroomConfig(false, 0); err != nil {
		t.Fatalf("syncCodexGlobalHeadroomConfig(false) on missing file: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("disable must not create a file, got stat err=%v", err)
	}
}

// TestSyncCodexGlobalHeadroomConfig_AddIsIdempotent verifies enabling twice
// produces a single marker block (no duplication) and does not rewrite the
// file on the second pass (no needless backup churn).
func TestSyncCodexGlobalHeadroomConfig_AddIsIdempotent(t *testing.T) {
	configPath := newCodexTestConfig(t, "model = \"gpt-5.6-sol\"\n")

	if err := syncCodexGlobalHeadroomConfig(true, 8788); err != nil {
		t.Fatalf("first enable: %v", err)
	}
	first := readCodexTestConfig(t, configPath)
	mtimeFirst, _ := os.Stat(configPath)

	// Small delay so an accidental rewrite would observably change mtime.
	time.Sleep(15 * time.Millisecond)

	if err := syncCodexGlobalHeadroomConfig(true, 8788); err != nil {
		t.Fatalf("second enable: %v", err)
	}
	second := readCodexTestConfig(t, configPath)
	mtimeSecond, _ := os.Stat(configPath)

	if first != second {
		t.Fatalf("second enable changed content when it should be idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if c := countExactLine(second, "openai_base_url = \"http://127.0.0.1:8788/v1\""); c != 1 {
		t.Fatalf("expected exactly one openai_base_url after double-enable, got %d:\n%s", c, second)
	}
	// The second pass must be a true no-op (no write): mtime unchanged.
	if !mtimeFirst.ModTime().Equal(mtimeSecond.ModTime()) {
		t.Fatalf("second enable rewrote the file (mtime %s -> %s); expected no-op", mtimeFirst.ModTime(), mtimeSecond.ModTime())
	}
}

// TestSyncCodexGlobalHeadroomConfig_PortUpdateRefreshesBlock verifies that
// re-enabling with a different port refreshes the base URL in place.
func TestSyncCodexGlobalHeadroomConfig_PortUpdateRefreshesBlock(t *testing.T) {
	configPath := newCodexTestConfig(t, "model = \"gpt-5.6-sol\"\n")

	if err := syncCodexGlobalHeadroomConfig(true, 8788); err != nil {
		t.Fatalf("enable 8788: %v", err)
	}
	if err := syncCodexGlobalHeadroomConfig(true, 8789); err != nil {
		t.Fatalf("enable 8789: %v", err)
	}
	got := readCodexTestConfig(t, configPath)
	if strings.Contains(got, "http://127.0.0.1:8788/v1") {
		t.Fatalf("stale 8788 base URL should have been replaced:\n%s", got)
	}
	if c := countExactLine(got, "openai_base_url = \"http://127.0.0.1:8789/v1\""); c != 1 {
		t.Fatalf("expected exactly one refreshed 8789 base URL, got %d:\n%s", c, got)
	}
}

// TestSyncCodexGlobalHeadroomConfig_SurvivesModelOnlySync verifies the marker
// block survives a subsequent syncCodexConfigModel rewrite (model-only sync
// must not touch the openai_base_url block).
func TestSyncCodexGlobalHeadroomConfig_SurvivesModelOnlySync(t *testing.T) {
	original := "model = \"gpt-5.6-sol\"\n" +
		"approval_policy = \"never\"\n" +
		"\n" +
		"[mcp_servers.foo]\n" +
		"command = \"foo\"\n"
	configPath := newCodexTestConfig(t, original)

	if err := syncCodexGlobalHeadroomConfig(true, 8788); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := syncCodexConfigModel("gpt-5.7"); err != nil {
		t.Fatalf("syncCodexConfigModel: %v", err)
	}
	got := readCodexTestConfig(t, configPath)

	// model updated, block intact, other config preserved.
	if !strings.Contains(got, "model = \"gpt-5.7\"") {
		t.Fatalf("model not updated:\n%s", got)
	}
	if !strings.Contains(got, "# === amagi-headroom-global-start ===") ||
		!strings.Contains(got, "openai_base_url = \"http://127.0.0.1:8788/v1\"") ||
		!strings.Contains(got, "# === amagi-headroom-global-end ===") {
		t.Fatalf("global headroom marker block should survive model sync:\n%s", got)
	}
	if !strings.Contains(got, "approval_policy = \"never\"") {
		t.Fatalf("approval_policy not preserved:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers.foo]") || !strings.Contains(got, "command = \"foo\"") {
		t.Fatalf("mcp_servers block not preserved:\n%s", got)
	}
}

// TestSyncCodexGlobalHeadroomConfig_NotCrossDeletedByCustomProvider verifies
// the openai_base_url marker block is NOT removed by
// syncCodexCustomProviderConfig (distinct markers => orthogonal blocks).
func TestSyncCodexGlobalHeadroomConfig_NotCrossDeletedByCustomProvider(t *testing.T) {
	original := "model = \"gpt-5.6-sol\"\n" +
		"\n" +
		"# === amagi-headroom-global-start ===\n" +
		"openai_base_url = \"http://127.0.0.1:8788/v1\"\n" +
		"# === amagi-headroom-global-end ===\n" +
		"\n" +
		"[model_providers.other]\n" +
		"base_url = \"https://other.example.com/v1\"\n"
	configPath := newCodexTestConfig(t, original)

	if err := syncCodexCustomProviderConfig("gpt-5.7", "https://proxy.example.com/v1"); err != nil {
		t.Fatalf("syncCodexCustomProviderConfig: %v", err)
	}
	got := readCodexTestConfig(t, configPath)

	// The amagi-codebox-inject provider block should be created...
	if !strings.Contains(got, "[model_providers.amagi-codebox-provider]") {
		t.Fatalf("custom provider section not created:\n%s", got)
	}
	// ...but the global headroom openai_base_url block must survive untouched.
	if !strings.Contains(got, "# === amagi-headroom-global-start ===") ||
		!strings.Contains(got, "openai_base_url = \"http://127.0.0.1:8788/v1\"") ||
		!strings.Contains(got, "# === amagi-headroom-global-end ===") {
		t.Fatalf("global headroom marker block must NOT be removed by custom provider sync:\n%s", got)
	}
	// And the unrelated other-provider section must survive too.
	if !strings.Contains(got, "[model_providers.other]") {
		t.Fatalf("other provider section not preserved:\n%s", got)
	}
}

// TestSyncCodexGlobalHeadroomConfig_PreservesRichCodexConfig verifies the
// surgical edit leaves heavy real-world config (plugins / marketplaces /
// mcp_servers / profiles / nested tables) fully intact, mirroring the shape of
// the user's actual ~/.codex/config.toml.
func TestSyncCodexGlobalHeadroomConfig_PreservesRichCodexConfig(t *testing.T) {
	original := "model = \"gpt-5.6-sol\"\n" +
		"service_tier = \"priority\"\n" +
		"\n" +
		"[plugins]\n" +
		"enabled = [\"amagi\", \"foo\"]\n" +
		"\n" +
		"[marketplaces.amagi]\n" +
		"url = \"https://example.com/marketplace.json\"\n" +
		"\n" +
		"[mcp_servers.weather]\n" +
		"command = \"uvx\"\n" +
		"args = [\"-y\", \"weather\"]\n" +
		"\n" +
		"[profiles.research]\n" +
		"model = \"gpt-5.7\"\n"
	configPath := newCodexTestConfig(t, original)

	if err := syncCodexGlobalHeadroomConfig(true, 8788); err != nil {
		t.Fatalf("enable: %v", err)
	}
	got := readCodexTestConfig(t, configPath)

	// Every line of the original rich config must survive.
	for _, want := range []string{
		"model = \"gpt-5.6-sol\"",
		"service_tier = \"priority\"",
		"[plugins]",
		"enabled = [\"amagi\", \"foo\"]",
		"[marketplaces.amagi]",
		"url = \"https://example.com/marketplace.json\"",
		"[mcp_servers.weather]",
		"command = \"uvx\"",
		"args = [\"-y\", \"weather\"]",
		"[profiles.research]",
		"model = \"gpt-5.7\"",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rich config line %q not preserved:\n%s", want, got)
		}
	}
	// Marker block present and before the first section header.
	if !strings.Contains(got, "openai_base_url = \"http://127.0.0.1:8788/v1\"") {
		t.Fatalf("openai_base_url not written:\n%s", got)
	}
	startIdx := strings.Index(got, "# === amagi-headroom-global-start ===")
	pluginsIdx := strings.Index(got, "[plugins]")
	if startIdx == -1 || pluginsIdx == -1 || startIdx > pluginsIdx {
		t.Fatalf("marker block must precede [plugins]:\n%s", got)
	}
	// Round-trip disable restores the original content (apart from a trailing
	// newline difference which we tolerate by trimming).
	if err := syncCodexGlobalHeadroomConfig(false, 0); err != nil {
		t.Fatalf("disable: %v", err)
	}
	after := readCodexTestConfig(t, configPath)
	for _, want := range []string{
		"model = \"gpt-5.6-sol\"",
		"service_tier = \"priority\"",
		"[plugins]",
		"enabled = [\"amagi\", \"foo\"]",
		"[marketplaces.amagi]",
		"[mcp_servers.weather]",
		"args = [\"-y\", \"weather\"]",
		"[profiles.research]",
	} {
		if !strings.Contains(after, want) {
			t.Fatalf("after disable, line %q not preserved:\n%s", want, after)
		}
	}
	if strings.Contains(after, "openai_base_url") {
		t.Fatalf("after disable, openai_base_url should be gone:\n%s", after)
	}
}

// TestSyncCodexGlobalHeadroomConfig_BackupCreated verifies enabling writes a
// config.toml.bak.<ts> backup of the previous content before mutating.
func TestSyncCodexGlobalHeadroomConfig_BackupCreated(t *testing.T) {
	original := "model = \"gpt-5.6-sol\"\n"
	configPath := newCodexTestConfig(t, original)

	if err := syncCodexGlobalHeadroomConfig(true, 8788); err != nil {
		t.Fatalf("enable: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	var backup string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.toml.bak.") {
			backup = filepath.Join(filepath.Dir(configPath), e.Name())
			break
		}
	}
	if backup == "" {
		t.Fatalf("expected a config.toml.bak.<ts> backup:\n%s", gotDirListing(filepath.Dir(configPath)))
	}
	bk, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(bk) != original {
		t.Fatalf("backup should hold the pre-write content.\nwant:\n%s\ngot:\n%s", original, string(bk))
	}
}

func gotDirListing(dir string) string {
	entries, _ := os.ReadDir(dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ", ")
}
