package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
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

	if err := syncCodexCustomProviderConfig("gpt-5.5", "https://proxy.example.com/v1", ""); err != nil {
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

	if err := syncCodexCustomProviderConfig("gpt-5.5", "https://proxy.example.com/v1", ""); err != nil {
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

	if err := syncCodexCustomProviderConfig("gpt-5.5", "https://proxy.example.com/v1", ""); err != nil {
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

func TestSyncCodexConfigUsesCODEXHOME(t *testing.T) {
	home := t.TempDir()
	customCodexHome := filepath.Join(t.TempDir(), "codex-state")
	setTestUserHome(t, home)
	t.Setenv("CODEX_HOME", customCodexHome)
	configPath := filepath.Join(customCodexHome, "config.toml")
	if err := os.MkdirAll(customCodexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"old\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := syncCodexConfigModel("gpt-5.6-luna"); err != nil {
		t.Fatalf("syncCodexConfigModel: %v", err)
	}
	got := readCodexTestConfig(t, configPath)
	if !strings.Contains(got, `model = "gpt-5.6-luna"`) {
		t.Fatalf("CODEX_HOME config was not updated: %s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("default ~/.codex config should remain untouched, stat err=%v", err)
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

// readCodexTestConfig reads the config file back; fails the test on error.
func readCodexTestConfig(t *testing.T, configPath string) string {
	t.Helper()
	b, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func gotDirListing(dir string) string {
	entries, _ := os.ReadDir(dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ", ")
}

// TestRemoveCodexLegacyHeadroomMarkerConfig 验证启动善后链路（R4 §4.5）：
// 用户 config.toml 中遗留的 amagi-headroom-global 标记块被一次性移除；
// 无标记 / 文件不存在为 no-op；其余内容逐字保留。
func TestRemoveCodexLegacyHeadroomMarkerConfig(t *testing.T) {
	isolatedHome := t.TempDir()
	t.Setenv("HOME", isolatedHome)
	t.Setenv("USERPROFILE", isolatedHome)
	t.Setenv("CODEX_HOME", filepath.Join(isolatedHome, ".codex"))
	configDir := filepath.Join(isolatedHome, ".codex")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	configPath := filepath.Join(configDir, "config.toml")
	legacy := "model = \"gpt-5.6\"\n" +
		"# === amagi-headroom-global-start ===\n" +
		"openai_base_url = \"http://127.0.0.1:8788/v1\"\n" +
		"# === amagi-headroom-global-end ===\n" +
		"\n[profiles.minimal]\nmodel = \"mini\"\n"
	if err := os.WriteFile(configPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile legacy config: %v", err)
	}

	if err := removeCodexLegacyHeadroomMarkerConfig(); err != nil {
		t.Fatalf("removeCodexLegacyHeadroomMarkerConfig: %v", err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(got), "amagi-headroom-global") || strings.Contains(string(got), "openai_base_url") {
		t.Fatalf("marker block survived cleanup:\n%s", got)
	}
	if !strings.Contains(string(got), "model = \"gpt-5.6\"") || !strings.Contains(string(got), "[profiles.minimal]") {
		t.Fatalf("unrelated config lines were damaged:\n%s", got)
	}

	// 幂等：清除后再跑一次 no-op（内容不变）。
	before := string(got)
	if err := removeCodexLegacyHeadroomMarkerConfig(); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != before {
		t.Fatalf("idempotent rerun changed content:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// 文件不存在 → no-op。
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := removeCodexLegacyHeadroomMarkerConfig(); err != nil {
		t.Fatalf("missing config must be a no-op, got: %v", err)
	}
}

// TestRemoveCodexGlobalHeadroomBlockPureFunction 冻结纯函数语义：标记块
//（含首尾标记行与块内全部行）被剥离，无标记时原样返回。
func TestRemoveCodexGlobalHeadroomBlockPureFunction(t *testing.T) {
	lines := []string{"a = 1", "# === amagi-headroom-global-start ===", "openai_base_url = \"http://127.0.0.1:8788/v1\"", "# === amagi-headroom-global-end ===", "b = 2"}
	got := removeCodexGlobalHeadroomBlock(lines)
	if len(got) != 2 || got[0] != "a = 1" || got[1] != "b = 2" {
		t.Fatalf("removeCodexGlobalHeadroomBlock = %v, want [a = 1 b = 2]", got)
	}
	same := removeCodexGlobalHeadroomBlock([]string{"a = 1"})
	if len(same) != 1 || same[0] != "a = 1" {
		t.Fatalf("no-marker input was modified: %v", same)
	}
}

// TestSyncCodexCustomProviderConfigFile_WireAPI 验证 config.toml 托管 provider 段的
// wire_api 动态化："chat" 写入 chat；未设置/非法值维持 responses（legacy 默认回归）。
func TestSyncCodexCustomProviderConfigFile_WireAPI(t *testing.T) {
	newConfig := func(t *testing.T) string {
		t.Helper()
		return filepath.Join(t.TempDir(), ".codex", "config.toml")
	}

	cases := []struct {
		name     string
		wireAPI  string
		wantLine string
	}{
		{"chat writes chat", "chat", `wire_api = "chat"`},
		{"uppercase chat normalized to chat", " CHAT ", `wire_api = "chat"`},
		{"unset keeps legacy responses", "", `wire_api = "responses"`},
		{"explicit responses stays responses", "responses", `wire_api = "responses"`},
		{"illegal value falls back to responses", "grpc", `wire_api = "responses"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configPath := newConfig(t)
			if err := syncCodexCustomProviderConfigFile(configPath, "gpt-5.5", "https://proxy.example.com/v1", tc.wireAPI); err != nil {
				t.Fatalf("syncCodexCustomProviderConfigFile: %v", err)
			}
			data, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			got := string(data)
			if !strings.Contains(got, tc.wantLine) {
				t.Fatalf("config.toml missing %q, got:\n%s", tc.wantLine, got)
			}
		})
	}
}

// TestBuildCodexCLIArgsWireAPI 验证 -c 进程级覆盖的 wire_api 动态化：
// chat → wire_api="chat"；未设置/"responses"/非法值 → wire_api="responses"（legacy 默认回归）。
func TestBuildCodexCLIArgsWireAPI(t *testing.T) {
	base := codexLaunchSettings{
		Model:           "gpt-5.6-luna",
		ProviderBaseURL: "https://proxy.example.com/v1",
	}

	cases := []struct {
		name    string
		wireAPI string
		want    string
		forbid  string
	}{
		{"chat", "chat", `wire_api="chat"`, `wire_api="responses"`},
		{"unset keeps legacy responses", "", `wire_api="responses"`, `wire_api="chat"`},
		{"explicit responses", "responses", `wire_api="responses"`, `wire_api="chat"`},
		{"illegal value falls back to responses", "grpc", `wire_api="responses"`, `wire_api="chat"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := base
			settings.WireAPI = tc.wireAPI
			args := buildCodexCLIArgs(settings)
			joined := strings.Join(args, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("Codex args missing %q: %v", tc.want, args)
			}
			if strings.Contains(joined, tc.forbid) {
				t.Fatalf("Codex args should not contain %q: %v", tc.forbid, args)
			}
		})
	}
}

// TestResolveCodexLaunchSettings_WireAPI 验证 wire_api 从 provider.OpenAI 透传：
// 双格式 provider 取 EffectiveWireAPI 归一化值；legacy 单格式（无 OpenAI 子块）为空。
func TestResolveCodexLaunchSettings_WireAPI(t *testing.T) {
	cases := []struct {
		name   string
		openAI *config.OpenAIFormat
		want   string
	}{
		{"responses passthrough", &config.OpenAIFormat{Enabled: true, WireAPI: "responses"}, "responses"},
		{"chat passthrough", &config.OpenAIFormat{Enabled: true, WireAPI: "chat"}, "chat"},
		{"uppercase normalized", &config.OpenAIFormat{Enabled: true, WireAPI: " CHAT "}, "chat"},
		{"unset empty", &config.OpenAIFormat{Enabled: true}, ""},
		{"illegal normalized to empty", &config.OpenAIFormat{Enabled: true, WireAPI: "grpc"}, ""},
		{"nil openai block empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := config.Provider{
				Type:         "openai",
				DefaultModel: "gpt-5.5",
				OpenAI:       tc.openAI,
			}
			settings := resolveCodexLaunchSettings(provider, "")
			if settings.WireAPI != tc.want {
				t.Fatalf("WireAPI = %q, want %q", settings.WireAPI, tc.want)
			}
		})
	}
}

// TestSyncCodexConfigFile_AtomicWritePrivateNoBackup verifies syncCodexConfigFile
// writes atomically with 0600 permissions without creating .bak.* or leaving .tmp
// artifacts in the target directory.
func TestSyncCodexConfigFile_AtomicWritePrivateNoBackup(t *testing.T) {
	codexDir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5.4\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := syncCodexConfigFile(configPath, codexConfigSyncOptions{
		Model:                "gpt-5.5",
		CleanupManagedConfig: true,
	}); err != nil {
		t.Fatalf("syncCodexConfigFile: %v", err)
	}

	entries, err := os.ReadDir(codexDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Fatalf("expected only config.toml in %s, got: %s", codexDir, gotDirListing(codexDir))
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `model = "gpt-5.5"`) {
		t.Fatalf("updated model missing in config.toml:\n%s", string(data))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("config.toml mode = %04o, want 0600", perm)
		}
	}
}
