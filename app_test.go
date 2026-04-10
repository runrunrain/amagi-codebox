package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/session"
)

func setCodexTestHome(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if volume := filepath.VolumeName(home); volume != "" {
		t.Setenv("HOMEDRIVE", volume)
		t.Setenv("HOMEPATH", strings.TrimPrefix(home, volume))
	}

	return home
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func mustJSONUnmarshalObject(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return got
}

func TestResolveCodexSourceHomePrefersCODEXHOMEEnv(t *testing.T) {
	home := setCodexTestHome(t)
	custom := filepath.Join(home, "custom-codex-home")
	t.Setenv("CODEX_HOME", custom)

	got, err := resolveCodexSourceHome(nil)
	if err != nil {
		t.Fatalf("resolveCodexSourceHome returned error: %v", err)
	}
	if got != custom {
		t.Fatalf("resolveCodexSourceHome = %q, want %q", got, custom)
	}
}

func TestPrepareCodexSessionHomeUsesPersistentCODEXHOMEAndPreservesSourceFiles(t *testing.T) {
	home := setCodexTestHome(t)
	sourceHome := filepath.Join(home, ".codex")
	sourceConfigPath := filepath.Join(sourceHome, "config.toml")
	sourceAuthPath := filepath.Join(sourceHome, "auth.json")
	sourceHistoryPath := filepath.Join(sourceHome, "history.jsonl")
	configPrefix := "approval_policy = \"never\"\nprofile = \"custom-preset\"\nmodel = \"gpt-5\"\n"
	injectedSection := "# === amagi-codebox-inject-start ===\n" +
		"model_provider = \"amagi-codebox-provider\"\n\n" +
		"[model_providers.amagi-codebox-provider]\n" +
		"name = \"amagi-codebox-provider\"\n" +
		"base_url = \"https://old.example/v1\"\n" +
		"wire_api = \"responses\"\n" +
		"requires_openai_auth = true\n\n" +
		"[projects.'X:\\WorkSpace']\n" +
		"trust_level = \"trusted\"\n\n" +
		"[windows]\n" +
		"sandbox = \"elevated\"\n" +
		"# === amagi-codebox-inject-end ===\n\n"
	mcpSuffix := "[mcp_servers.keep]\ncommand = \"npx\"\nargs = [\"-y\", \"@modelcontextprotocol/server-filesystem\"]\n"
	sourceConfig := []byte(configPrefix + injectedSection + mcpSuffix)
	sourceAuth := []byte("{\n  \"auth_mode\": \"oauth\",\n  \"refresh_token\": \"keep-me\"\n}\n")
	sourceHistory := []byte("volatile history that should not be copied\n")

	writeTestFile(t, sourceConfigPath, sourceConfig)
	writeTestFile(t, sourceAuthPath, sourceAuth)
	writeTestFile(t, sourceHistoryPath, sourceHistory)

	app := &App{Log: logging.NewService(t.TempDir())}
	t.Cleanup(app.Log.Close)

	envOverrides := map[string]string{
		"OPENAI_API_KEY":  "sk-session-key",
		"OPENAI_BASE_URL": "https://example.com/v1",
	}

	isolatedHome, err := app.prepareCodexSessionHome("sess-openai", "", codexLaunchSettings{Model: "gpt-5.4"}, envOverrides)
	if err != nil {
		t.Fatalf("prepareCodexSessionHome returned error: %v", err)
	}
	if isolatedHome == "" {
		t.Fatal("prepareCodexSessionHome returned empty isolated home")
	}
	if isolatedHome == sourceHome {
		t.Fatalf("isolated home should differ from source home: %q", isolatedHome)
	}
	if envOverrides["CODEX_HOME"] != isolatedHome {
		t.Fatalf("CODEX_HOME = %q, want %q", envOverrides["CODEX_HOME"], isolatedHome)
	}
	if _, ok := envOverrides["OPENAI_BASE_URL"]; ok {
		t.Fatalf("OPENAI_BASE_URL should be removed after isolated config injection, got %q", envOverrides["OPENAI_BASE_URL"])
	}

	isolatedConfigPath := filepath.Join(isolatedHome, "config.toml")
	isolatedConfig := readTestFile(t, isolatedConfigPath)
	sessionHome, ok := app.codexSessionHomes["sess-openai"]
	if !ok {
		t.Fatalf("codexSessionHomes missing entry for session: %#v", app.codexSessionHomes)
	}
	if sessionHome.HomeKey != "base-url:https://example.com/v1|model:gpt-5.4|profile:custom-preset|root-model-provider:amagi-codebox-provider" {
		t.Fatalf("HomeKey = %q, want %q", sessionHome.HomeKey, "base-url:https://example.com/v1|model:gpt-5.4|profile:custom-preset|root-model-provider:amagi-codebox-provider")
	}
	newProviderBaseURL := []byte("base_url = \"https://example.com/v1\"")
	if !bytes.Contains(isolatedConfig, newProviderBaseURL) {
		t.Fatalf("isolated config missing updated provider base_url\nwant snippet:\n%s\n\ngot:\n%s", newProviderBaseURL, isolatedConfig)
	}
	if bytes.Contains(isolatedConfig, []byte("base_url = \"https://old.example/v1\"")) {
		t.Fatalf("isolated config should replace old provider base_url\nconfig:\n%s", isolatedConfig)
	}
	firstTable := bytes.Index(isolatedConfig, []byte("[mcp_servers.keep]"))
	if firstTable == -1 {
		t.Fatalf("isolated config missing source table suffix\nconfig:\n%s", isolatedConfig)
	}
	if !bytes.Contains(isolatedConfig[:firstTable], []byte("profile = \"custom-preset\"")) {
		t.Fatalf("isolated config should preserve custom profile before first table\nconfig:\n%s", isolatedConfig)
	}
	if bytes.Contains(isolatedConfig[:firstTable], []byte("model_provider = \"amagi-codebox-provider\"")) {
		t.Fatalf("isolated config should not force injected model_provider when custom profile exists\nconfig:\n%s", isolatedConfig)
	}
	if !bytes.Contains(isolatedConfig[:firstTable], []byte("wire_api = \"responses\"")) {
		t.Fatalf("isolated config missing wire_api = responses before first table\nconfig:\n%s", isolatedConfig)
	}
	if !bytes.Contains(isolatedConfig[:firstTable], []byte("[windows]\nsandbox = \"elevated\"")) {
		t.Fatalf("isolated config missing windows sandbox section before first table\nconfig:\n%s", isolatedConfig)
	}
	if bytes.Contains(isolatedConfig, []byte("openai_base_url = ")) {
		t.Fatalf("isolated config should keep provider-based config instead of root openai_base_url\nconfig:\n%s", isolatedConfig)
	}
	if !bytes.Equal(isolatedConfig[firstTable:], []byte(mcpSuffix)) {
		t.Fatalf("mcp/table suffix was not preserved byte-for-byte\nwant suffix:\n%s\n\ngot suffix:\n%s", mcpSuffix, isolatedConfig[firstTable:])
	}
	if bytes.Contains(isolatedConfig, []byte("OPENAI_API_KEY")) {
		t.Fatalf("isolated config unexpectedly contains OPENAI_API_KEY\nconfig:\n%s", isolatedConfig)
	}

	isolatedAuth := mustJSONUnmarshalObject(t, readTestFile(t, filepath.Join(isolatedHome, "auth.json")))
	if isolatedAuth["OPENAI_API_KEY"] != "sk-session-key" {
		t.Fatalf("isolated auth OPENAI_API_KEY = %#v, want %q", isolatedAuth["OPENAI_API_KEY"], "sk-session-key")
	}
	if isolatedAuth["auth_mode"] != "oauth" {
		t.Fatalf("isolated auth auth_mode = %#v, want %q", isolatedAuth["auth_mode"], "oauth")
	}
	if isolatedAuth["refresh_token"] != "keep-me" {
		t.Fatalf("isolated auth refresh_token = %#v, want %q", isolatedAuth["refresh_token"], "keep-me")
	}

	if _, err := os.Stat(filepath.Join(isolatedHome, "history.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("volatile file should not be copied, stat err=%v", err)
	}
	if !bytes.Equal(readTestFile(t, sourceConfigPath), sourceConfig) {
		t.Fatalf("source config.toml was mutated\nwant:\n%s\n\ngot:\n%s", sourceConfig, readTestFile(t, sourceConfigPath))
	}
	if !bytes.Equal(readTestFile(t, sourceAuthPath), sourceAuth) {
		t.Fatalf("source auth.json was mutated\nwant:\n%s\n\ngot:\n%s", sourceAuth, readTestFile(t, sourceAuthPath))
	}
}

func TestPrepareCodexSessionHomeReusesPersistentHomeForSameProvider(t *testing.T) {
	home := setCodexTestHome(t)
	sourceHome := filepath.Join(home, ".codex")
	writeTestFile(t, filepath.Join(sourceHome, "config.toml"), []byte("model = \"gpt-5\"\n"))
	writeTestFile(t, filepath.Join(sourceHome, "auth.json"), []byte("{\"auth_mode\":\"oauth\"}\n"))

	app := &App{Log: logging.NewService(t.TempDir())}
	t.Cleanup(app.Log.Close)

	firstOverrides := map[string]string{
		"OPENAI_API_KEY":  "sk-first",
		"OPENAI_BASE_URL": "https://example.com/v1",
	}
	firstHome, err := app.prepareCodexSessionHome("sess-first", "provider-a", codexLaunchSettings{Model: "gpt-5.4"}, firstOverrides)
	if err != nil {
		t.Fatalf("first prepareCodexSessionHome returned error: %v", err)
	}
	writeTestFile(t, filepath.Join(firstHome, "history.jsonl"), []byte("persist me\n"))

	secondOverrides := map[string]string{
		"OPENAI_API_KEY":  "sk-second",
		"OPENAI_BASE_URL": "https://example.com/v1",
	}
	secondHome, err := app.prepareCodexSessionHome("sess-second", "provider-a", codexLaunchSettings{Model: "gpt-5.4"}, secondOverrides)
	if err != nil {
		t.Fatalf("second prepareCodexSessionHome returned error: %v", err)
	}
	if secondHome != firstHome {
		t.Fatalf("persistent codex home should be reused\nfirst=%q\nsecond=%q", firstHome, secondHome)
	}
	if got := string(readTestFile(t, filepath.Join(secondHome, "history.jsonl"))); got != "persist me\n" {
		t.Fatalf("existing session history should be preserved, got %q", got)
	}

	isolatedAuth := mustJSONUnmarshalObject(t, readTestFile(t, filepath.Join(secondHome, "auth.json")))
	if isolatedAuth["OPENAI_API_KEY"] != "sk-second" {
		t.Fatalf("reused persistent auth OPENAI_API_KEY = %#v, want %q", isolatedAuth["OPENAI_API_KEY"], "sk-second")
	}
}

func TestPrepareCodexSessionHomeUsesDistinctPersistentHomeForDifferentRootProfiles(t *testing.T) {
	home := setCodexTestHome(t)
	sourceHome := filepath.Join(home, ".codex")
	writeTestFile(t, filepath.Join(sourceHome, "auth.json"), []byte("{\"auth_mode\":\"oauth\"}\n"))

	app := &App{Log: logging.NewService(t.TempDir())}
	t.Cleanup(app.Log.Close)

	writeTestFile(t, filepath.Join(sourceHome, "config.toml"), []byte("profile = \"preset-a\"\nmodel = \"gpt-5\"\n"))
	firstOverrides := map[string]string{
		"OPENAI_API_KEY":  "sk-first",
		"OPENAI_BASE_URL": "https://example.com/v1",
	}
	firstHome, err := app.prepareCodexSessionHome("sess-profile-a", "", codexLaunchSettings{Model: "gpt-5.4"}, firstOverrides)
	if err != nil {
		t.Fatalf("first prepareCodexSessionHome returned error: %v", err)
	}

	writeTestFile(t, filepath.Join(sourceHome, "config.toml"), []byte("profile = \"preset-b\"\nmodel = \"gpt-5\"\n"))
	secondOverrides := map[string]string{
		"OPENAI_API_KEY":  "sk-second",
		"OPENAI_BASE_URL": "https://example.com/v1",
	}
	secondHome, err := app.prepareCodexSessionHome("sess-profile-b", "", codexLaunchSettings{Model: "gpt-5.4"}, secondOverrides)
	if err != nil {
		t.Fatalf("second prepareCodexSessionHome returned error: %v", err)
	}

	if firstHome == secondHome {
		t.Fatalf("different root profiles should use different persistent homes\nfirst=%q\nsecond=%q", firstHome, secondHome)
	}
	if !bytes.Contains(readTestFile(t, filepath.Join(secondHome, "config.toml")), []byte("profile = \"preset-b\"")) {
		t.Fatalf("second persistent home should contain the new root profile")
	}
}

func TestPrepareCodexSessionHomeUsesDistinctPersistentHomeForDifferentRootModelProviders(t *testing.T) {
	home := setCodexTestHome(t)
	sourceHome := filepath.Join(home, ".codex")
	writeTestFile(t, filepath.Join(sourceHome, "auth.json"), []byte("{\"auth_mode\":\"oauth\"}\n"))

	app := &App{Log: logging.NewService(t.TempDir())}
	t.Cleanup(app.Log.Close)

	writeTestFile(t, filepath.Join(sourceHome, "config.toml"), []byte("model_provider = \"provider-a\"\nmodel = \"gpt-5\"\n"))
	firstHome, err := app.prepareCodexSessionHome("sess-provider-a", "", codexLaunchSettings{Model: "gpt-5.4"}, map[string]string{})
	if err != nil {
		t.Fatalf("first prepareCodexSessionHome returned error: %v", err)
	}

	writeTestFile(t, filepath.Join(sourceHome, "config.toml"), []byte("model_provider = \"provider-b\"\nmodel = \"gpt-5\"\n"))
	secondHome, err := app.prepareCodexSessionHome("sess-provider-b", "", codexLaunchSettings{Model: "gpt-5.4"}, map[string]string{})
	if err != nil {
		t.Fatalf("second prepareCodexSessionHome returned error: %v", err)
	}

	if firstHome == secondHome {
		t.Fatalf("different root model_provider values should use different persistent homes\nfirst=%q\nsecond=%q", firstHome, secondHome)
	}
	if !bytes.Contains(readTestFile(t, filepath.Join(secondHome, "config.toml")), []byte("model_provider = \"provider-b\"")) {
		t.Fatalf("second persistent home should contain the new root model_provider")
	}
}

func TestPrepareCodexSessionHomeUsesDistinctPersistentHomeForDifferentModels(t *testing.T) {
	home := setCodexTestHome(t)
	sourceHome := filepath.Join(home, ".codex")
	writeTestFile(t, filepath.Join(sourceHome, "config.toml"), []byte("profile = \"custom-preset\"\n"))
	writeTestFile(t, filepath.Join(sourceHome, "auth.json"), []byte("{\"auth_mode\":\"oauth\"}\n"))

	app := &App{Log: logging.NewService(t.TempDir())}
	t.Cleanup(app.Log.Close)

	firstHome, err := app.prepareCodexSessionHome("sess-model-a", "provider-a", codexLaunchSettings{Model: "gpt-5.4"}, map[string]string{"OPENAI_API_KEY": "sk-first", "OPENAI_BASE_URL": "https://example.com/v1"})
	if err != nil {
		t.Fatalf("first prepareCodexSessionHome returned error: %v", err)
	}
	secondHome, err := app.prepareCodexSessionHome("sess-model-b", "provider-a", codexLaunchSettings{Model: "gpt-5.4-mini"}, map[string]string{"OPENAI_API_KEY": "sk-second", "OPENAI_BASE_URL": "https://example.com/v1"})
	if err != nil {
		t.Fatalf("second prepareCodexSessionHome returned error: %v", err)
	}

	if firstHome == secondHome {
		t.Fatalf("different models should use different persistent homes\nfirst=%q\nsecond=%q", firstHome, secondHome)
	}
}

func TestPrepareCodexSessionHomeMovesInjectedModelProviderToRootBeforeFirstTable(t *testing.T) {
	home := setCodexTestHome(t)
	sourceHome := filepath.Join(home, ".codex")
	sourceConfigPath := filepath.Join(sourceHome, "config.toml")
	sourceAuthPath := filepath.Join(sourceHome, "auth.json")
	sourceConfig := []byte(
		"model = \"codex-mini-latest\"\n" +
			"approval_policy = \"never\"\n\n" +
			"[ghost_snapshot]\n" +
			"disable_warnings = true\n\n" +
			"# === amagi-codebox-inject-start ===\n" +
			"model_provider = \"amagi-codebox-provider\"\n\n" +
			"[model_providers.amagi-codebox-provider]\n" +
			"name = \"amagi-codebox-provider\"\n" +
			"base_url = \"https://old.example/v1\"\n" +
			"wire_api = \"responses\"\n" +
			"requires_openai_auth = true\n\n" +
			"[projects.'X:\\WorkSpace']\n" +
			"trust_level = \"trusted\"\n\n" +
			"[windows]\n" +
			"sandbox = \"elevated\"\n" +
			"# === amagi-codebox-inject-end ===\n\n" +
			"[mcp_servers.keep]\n" +
			"command = \"keep\"\n",
	)
	writeTestFile(t, sourceConfigPath, sourceConfig)
	writeTestFile(t, sourceAuthPath, []byte("{\"auth_mode\":\"oauth\"}\n"))

	app := &App{Log: logging.NewService(t.TempDir())}
	t.Cleanup(app.Log.Close)

	envOverrides := map[string]string{
		"OPENAI_API_KEY":  "sk-session-key",
		"OPENAI_BASE_URL": "https://example.com/v1",
	}
	isolateHome, err := app.prepareCodexSessionHome("sess-root-scope", "", codexLaunchSettings{Model: "gpt-5.4"}, envOverrides)
	if err != nil {
		t.Fatalf("prepareCodexSessionHome returned error: %v", err)
	}

	isolatedConfig := readTestFile(t, filepath.Join(isolateHome, "config.toml"))
	if got := extractRootLevelConfigValue(string(isolatedConfig), "model_provider"); got != "amagi-codebox-provider" {
		t.Fatalf("root model_provider = %q, want %q\nconfig:\n%s", got, "amagi-codebox-provider", isolatedConfig)
	}
	if bytes.Count(isolatedConfig, []byte("model_provider = \"amagi-codebox-provider\"")) != 1 {
		t.Fatalf("isolated config should contain exactly one injected model_provider selector\nconfig:\n%s", isolatedConfig)
	}
	firstTable := bytes.Index(isolatedConfig, []byte("[ghost_snapshot]"))
	if firstTable == -1 {
		t.Fatalf("isolated config missing ghost_snapshot table\nconfig:\n%s", isolatedConfig)
	}
	modelProviderIndex := bytes.Index(isolatedConfig, []byte("model_provider = \"amagi-codebox-provider\""))
	if modelProviderIndex == -1 || modelProviderIndex >= firstTable {
		t.Fatalf("root model_provider should appear before first table\nconfig:\n%s", isolatedConfig)
	}
	if !bytes.Contains(isolatedConfig, []byte("base_url = \"https://example.com/v1\"")) {
		t.Fatalf("isolated config missing updated provider base_url\nconfig:\n%s", isolatedConfig)
	}
	if bytes.Contains(isolatedConfig, []byte("base_url = \"https://old.example/v1\"")) {
		t.Fatalf("isolated config should replace old provider base_url\nconfig:\n%s", isolatedConfig)
	}
	for _, snippet := range []string{
		"[model_providers.amagi-codebox-provider]",
		"[projects.'X:\\WorkSpace']",
		"[windows]",
		"[ghost_snapshot]",
		"[mcp_servers.keep]",
	} {
		if !bytes.Contains(isolatedConfig, []byte(snippet)) {
			t.Fatalf("isolated config missing %s\nconfig:\n%s", snippet, isolatedConfig)
		}
	}
	if bytes.Contains(isolatedConfig, []byte("openai_base_url = ")) {
		t.Fatalf("isolated config should keep provider-based config instead of root openai_base_url\nconfig:\n%s", isolatedConfig)
	}
	if !bytes.Equal(readTestFile(t, sourceConfigPath), sourceConfig) {
		t.Fatalf("source config.toml was mutated\nwant:\n%s\n\ngot:\n%s", sourceConfig, readTestFile(t, sourceConfigPath))
	}
}

func TestPrepareCodexSessionHomeFallsBackToMinimalAuthOnMalformedSource(t *testing.T) {
	home := setCodexTestHome(t)
	sourceHome := filepath.Join(home, ".codex")
	writeTestFile(t, filepath.Join(sourceHome, "config.toml"), []byte("model = \"gpt-5\"\n"))
	writeTestFile(t, filepath.Join(sourceHome, "auth.json"), []byte("not-json"))

	app := &App{Log: logging.NewService(t.TempDir())}
	t.Cleanup(app.Log.Close)

	envOverrides := map[string]string{
		"OPENAI_API_KEY":  "sk-session-key",
		"OPENAI_BASE_URL": "https://example.com/v1",
	}

	isolatedHome, err := app.prepareCodexSessionHome("sess-malformed-auth", "", codexLaunchSettings{Model: "gpt-5.4"}, envOverrides)
	if err != nil {
		t.Fatalf("prepareCodexSessionHome returned error: %v", err)
	}

	isolatedAuth := mustJSONUnmarshalObject(t, readTestFile(t, filepath.Join(isolatedHome, "auth.json")))
	if len(isolatedAuth) != 1 {
		t.Fatalf("isolated auth should fall back to minimal object, got %#v", isolatedAuth)
	}
	if isolatedAuth["OPENAI_API_KEY"] != "sk-session-key" {
		t.Fatalf("isolated auth OPENAI_API_KEY = %#v, want %q", isolatedAuth["OPENAI_API_KEY"], "sk-session-key")
	}
	if !bytes.Equal(readTestFile(t, filepath.Join(sourceHome, "auth.json")), []byte("not-json")) {
		t.Fatalf("source malformed auth.json should remain unchanged")
	}
}

func TestPrepareCodexSessionHomeWithoutOpenAIOverridesCopiesOnlySafeAssets(t *testing.T) {
	home := setCodexTestHome(t)
	sourceHome := filepath.Join(home, ".codex")
	sourceConfig := []byte("approval_policy = \"never\"\n[mcp_servers.keep]\ncommand = \"keep\"\n")
	sourceAuth := []byte("{\n  \"auth_mode\": \"oauth\"\n}\n")
	writeTestFile(t, filepath.Join(sourceHome, "config.toml"), sourceConfig)
	writeTestFile(t, filepath.Join(sourceHome, "auth.json"), sourceAuth)

	app := &App{Log: logging.NewService(t.TempDir())}
	t.Cleanup(app.Log.Close)

	envOverrides := map[string]string{}

	isolatedHome, err := app.prepareCodexSessionHome("sess-safe-copy", "", codexLaunchSettings{}, envOverrides)
	if err != nil {
		t.Fatalf("prepareCodexSessionHome returned error: %v", err)
	}
	if envOverrides["CODEX_HOME"] != isolatedHome {
		t.Fatalf("CODEX_HOME = %q, want %q", envOverrides["CODEX_HOME"], isolatedHome)
	}
	if !bytes.Equal(readTestFile(t, filepath.Join(isolatedHome, "config.toml")), sourceConfig) {
		t.Fatalf("isolated config should match safe source config copy")
	}
	if !bytes.Equal(readTestFile(t, filepath.Join(isolatedHome, "auth.json")), sourceAuth) {
		t.Fatalf("isolated auth should match safe source auth copy")
	}
}

func TestBuildCodexIsolatedConfigTomlReplacesExistingRootOpenAIBaseURLBeforeFirstTable(t *testing.T) {
	source := []byte("approval_policy = \"never\"\nmodel_provider = \"user-provider\"\nopenai_base_url = \"https://old.example/v1\"\nmodel = \"gpt-5\"\n[mcp_servers.keep]\ncommand = \"keep\"\n")
	baseURL := "https://example.com/v1"

	got := buildCodexIsolatedConfigToml(source, codexLaunchSettings{}, baseURL)
	wantPrefix := "approval_policy = \"never\"\nmodel = \"gpt-5\"\nopenai_base_url = \"" + baseURL + "\"\n\n"
	if !bytes.HasPrefix(got, []byte(wantPrefix)) {
		t.Fatalf("isolated config prefix mismatch\nwant prefix:\n%s\n\ngot:\n%s", wantPrefix, got)
	}
	if bytes.Contains(got, []byte("openai_base_url = \"https://old.example/v1\"")) {
		t.Fatalf("old root-level openai_base_url should be removed\nconfig:\n%s", got)
	}
	if bytes.Contains(got, []byte("model_provider = \"user-provider\"")) {
		t.Fatalf("legacy root-level model_provider should be removed\nconfig:\n%s", got)
	}
	if bytes.Count(got, []byte("openai_base_url = ")) != 1 {
		t.Fatalf("isolated config should contain exactly one root openai_base_url\nconfig:\n%s", got)
	}
	firstTable := bytes.Index(got, []byte("[mcp_servers.keep]"))
	if firstTable == -1 {
		t.Fatalf("isolated config missing original table suffix\nconfig:\n%s", got)
	}
	if !bytes.Equal(got[firstTable:], []byte("[mcp_servers.keep]\ncommand = \"keep\"\n")) {
		t.Fatalf("table suffix should remain byte-for-byte\nwant:\n%s\n\ngot:\n%s", "[mcp_servers.keep]\ncommand = \"keep\"\n", got[firstTable:])
	}
	openAIBaseURLLine := buildCodexOpenAIBaseURLLine(baseURL)
	openAIBaseURLStart := bytes.Index(got, openAIBaseURLLine)
	if openAIBaseURLStart == -1 || openAIBaseURLStart >= firstTable {
		t.Fatalf("openai_base_url should appear before first table\nconfig:\n%s", got)
	}
}

func TestBuildCodexIsolatedConfigTomlPreservesInjectedProviderSectionAndCustomProfile(t *testing.T) {
	source := []byte(
		"approval_policy = \"never\"\n" +
			"profile = \"custom-preset\"\n" +
			"# === amagi-codebox-inject-start ===\n" +
			"model_provider = \"amagi-codebox-provider\"\n\n" +
			"[model_providers.amagi-codebox-provider]\n" +
			"name = \"amagi-codebox-provider\"\n" +
			"base_url = \"https://old.example/v1\"\n" +
			"wire_api = \"responses\"\n" +
			"requires_openai_auth = true\n\n" +
			"[windows]\n" +
			"sandbox = \"elevated\"\n" +
			"# === amagi-codebox-inject-end ===\n\n" +
			"[mcp_servers.keep]\n" +
			"command = \"keep\"\n",
	)

	got := buildCodexIsolatedConfigToml(source, codexLaunchSettings{}, "https://example.com/v1")
	if !bytes.Contains(got, []byte("profile = \"custom-preset\"")) {
		t.Fatalf("custom profile should be preserved\nconfig:\n%s", got)
	}
	if bytes.Contains(got, []byte("model_provider = \"amagi-codebox-provider\"")) {
		t.Fatalf("injected model_provider should not override custom profile\nconfig:\n%s", got)
	}
	if !bytes.Contains(got, []byte("base_url = \"https://example.com/v1\"")) {
		t.Fatalf("provider base_url should be updated\nconfig:\n%s", got)
	}
	if bytes.Contains(got, []byte("base_url = \"https://old.example/v1\"")) {
		t.Fatalf("old provider base_url should be removed\nconfig:\n%s", got)
	}
	if !bytes.Contains(got, []byte("wire_api = \"responses\"")) {
		t.Fatalf("wire_api should be preserved\nconfig:\n%s", got)
	}
	if !bytes.Contains(got, []byte("[windows]\nsandbox = \"elevated\"")) {
		t.Fatalf("windows section should be preserved\nconfig:\n%s", got)
	}
	if bytes.Contains(got, []byte("openai_base_url = ")) {
		t.Fatalf("provider-based injected config should not be downgraded to root openai_base_url\nconfig:\n%s", got)
	}
}

func TestBuildCodexIsolatedConfigTomlPreservesCustomRootModelProvider(t *testing.T) {
	source := []byte(
		"approval_policy = \"never\"\n" +
			"model_provider = \"custom-provider\"\n" +
			"# === amagi-codebox-inject-start ===\n" +
			"model_provider = \"amagi-codebox-provider\"\n\n" +
			"[model_providers.amagi-codebox-provider]\n" +
			"name = \"amagi-codebox-provider\"\n" +
			"base_url = \"https://old.example/v1\"\n" +
			"wire_api = \"responses\"\n" +
			"requires_openai_auth = true\n" +
			"# === amagi-codebox-inject-end ===\n\n" +
			"[mcp_servers.keep]\n" +
			"command = \"keep\"\n",
	)

	got := buildCodexIsolatedConfigToml(source, codexLaunchSettings{}, "https://example.com/v1")
	if !bytes.Contains(got, []byte("model_provider = \"custom-provider\"")) {
		t.Fatalf("custom root model_provider should be preserved\nconfig:\n%s", got)
	}
	if bytes.Contains(got, []byte("model_provider = \"amagi-codebox-provider\"")) {
		t.Fatalf("injected model_provider should not override custom root model_provider\nconfig:\n%s", got)
	}
	if !bytes.Contains(got, []byte("base_url = \"https://example.com/v1\"")) {
		t.Fatalf("provider base_url should be updated\nconfig:\n%s", got)
	}
	if !bytes.Contains(got, []byte("wire_api = \"responses\"")) {
		t.Fatalf("wire_api should be preserved\nconfig:\n%s", got)
	}
}

func TestBuildCodexIsolatedConfigTomlMovesInjectedModelProviderToRootBeforeFirstTable(t *testing.T) {
	source := []byte(
		"model = \"codex-mini-latest\"\n" +
			"approval_policy = \"never\"\n\n" +
			"[ghost_snapshot]\n" +
			"disable_warnings = true\n\n" +
			"# === amagi-codebox-inject-start ===\n" +
			"model_provider = \"amagi-codebox-provider\"\n\n" +
			"[model_providers.amagi-codebox-provider]\n" +
			"name = \"amagi-codebox-provider\"\n" +
			"base_url = \"https://old.example/v1\"\n" +
			"wire_api = \"responses\"\n" +
			"requires_openai_auth = true\n\n" +
			"[projects.'X:\\WorkSpace']\n" +
			"trust_level = \"trusted\"\n\n" +
			"[windows]\n" +
			"sandbox = \"elevated\"\n" +
			"# === amagi-codebox-inject-end ===\n\n" +
			"[mcp_servers.keep]\n" +
			"command = \"keep\"\n",
	)

	got := buildCodexIsolatedConfigToml(source, codexLaunchSettings{}, "https://example.com/v1")
	if rootModelProvider := extractRootLevelConfigValue(string(got), "model_provider"); rootModelProvider != "amagi-codebox-provider" {
		t.Fatalf("root model_provider = %q, want %q\nconfig:\n%s", rootModelProvider, "amagi-codebox-provider", got)
	}
	if bytes.Count(got, []byte("model_provider = \"amagi-codebox-provider\"")) != 1 {
		t.Fatalf("isolated config should contain exactly one injected model_provider selector\nconfig:\n%s", got)
	}
	firstTable := bytes.Index(got, []byte("[ghost_snapshot]"))
	if firstTable == -1 {
		t.Fatalf("isolated config missing ghost_snapshot table\nconfig:\n%s", got)
	}
	modelProviderIndex := bytes.Index(got, []byte("model_provider = \"amagi-codebox-provider\""))
	if modelProviderIndex == -1 || modelProviderIndex >= firstTable {
		t.Fatalf("root model_provider should appear before first table\nconfig:\n%s", got)
	}
	if !bytes.Contains(got, []byte("base_url = \"https://example.com/v1\"")) {
		t.Fatalf("provider base_url should be updated\nconfig:\n%s", got)
	}
	if bytes.Contains(got, []byte("base_url = \"https://old.example/v1\"")) {
		t.Fatalf("old provider base_url should be removed\nconfig:\n%s", got)
	}
	for _, snippet := range []string{
		"[model_providers.amagi-codebox-provider]",
		"[projects.'X:\\WorkSpace']",
		"[windows]",
		"[ghost_snapshot]",
		"[mcp_servers.keep]",
	} {
		if !bytes.Contains(got, []byte(snippet)) {
			t.Fatalf("config missing %s\nconfig:\n%s", snippet, got)
		}
	}
	if bytes.Contains(got, []byte("openai_base_url = ")) {
		t.Fatalf("provider-based injected config should not be downgraded to root openai_base_url\nconfig:\n%s", got)
	}
}

func TestBuildCodexIsolatedConfigTomlFallsBackForLegacyInjectedRootOpenAIBaseURL(t *testing.T) {
	source := []byte(
		"approval_policy = \"never\"\n" +
			"# === amagi-codebox-inject-start ===\n" +
			"openai_base_url = \"https://old.example/v1\"\n" +
			"# === amagi-codebox-inject-end ===\n\n" +
			"[mcp_servers.keep]\n" +
			"command = \"keep\"\n",
	)

	got := buildCodexIsolatedConfigToml(source, codexLaunchSettings{}, "https://example.com/v1")
	if !bytes.Contains(got, []byte("openai_base_url = \"https://example.com/v1\"")) {
		t.Fatalf("legacy injected openai_base_url should be rebuilt with new value\nconfig:\n%s", got)
	}
	if bytes.Contains(got, []byte("openai_base_url = \"https://old.example/v1\"")) {
		t.Fatalf("old legacy injected openai_base_url should be removed\nconfig:\n%s", got)
	}
	if bytes.Contains(got, []byte(codexInjectStartMarker)) || bytes.Contains(got, []byte(codexInjectEndMarker)) {
		t.Fatalf("legacy injected markers should be removed after fallback rebuild\nconfig:\n%s", got)
	}
}

func TestBuildCodexIsolatedConfigTomlAppliesContextWindowSettingsWithoutBaseURL(t *testing.T) {
	source := []byte(
		"approval_policy = \"never\"\n" +
			"model_context_window = 272000\n" +
			"model_auto_compact_token_limit = 200000\n" +
			"[mcp_servers.keep]\n" +
			"command = \"keep\"\n",
	)

	got := buildCodexIsolatedConfigToml(source, codexLaunchSettings{
		Model:                      "gpt-5.4",
		ModelContextWindow:         1_047_576,
		ModelAutoCompactTokenLimit: 400_000,
	}, "")

	if bytes.Contains(got, []byte("model_context_window = 272000")) {
		t.Fatalf("old model_context_window should be replaced\nconfig:\n%s", got)
	}
	if bytes.Contains(got, []byte("model_auto_compact_token_limit = 200000")) {
		t.Fatalf("old model_auto_compact_token_limit should be replaced\nconfig:\n%s", got)
	}
	if bytes.Count(got, []byte("model_context_window = ")) != 1 {
		t.Fatalf("config should contain exactly one model_context_window line\nconfig:\n%s", got)
	}
	if bytes.Count(got, []byte("model_auto_compact_token_limit = ")) != 1 {
		t.Fatalf("config should contain exactly one model_auto_compact_token_limit line\nconfig:\n%s", got)
	}
	firstTable := bytes.Index(got, []byte("[mcp_servers.keep]"))
	if firstTable == -1 {
		t.Fatalf("config missing original table suffix\nconfig:\n%s", got)
	}
	windowIndex := bytes.Index(got, []byte("model_context_window = 1047576"))
	compactIndex := bytes.Index(got, []byte("model_auto_compact_token_limit = 400000"))
	if windowIndex == -1 || compactIndex == -1 {
		t.Fatalf("config missing updated context window settings\nconfig:\n%s", got)
	}
	if windowIndex >= firstTable || compactIndex >= firstTable {
		t.Fatalf("context window settings should appear before first table\nconfig:\n%s", got)
	}
}

func TestPrepareCodexSessionHomeWritesContextWindowSettingsWithoutBaseURL(t *testing.T) {
	home := setCodexTestHome(t)
	sourceHome := filepath.Join(home, ".codex")
	writeTestFile(t, filepath.Join(sourceHome, "config.toml"), []byte("approval_policy = \"never\"\n[mcp_servers.keep]\ncommand = \"keep\"\n"))
	writeTestFile(t, filepath.Join(sourceHome, "auth.json"), []byte("{\"auth_mode\":\"oauth\"}\n"))

	app := &App{Log: logging.NewService(t.TempDir())}
	t.Cleanup(app.Log.Close)

	isolatedHome, err := app.prepareCodexSessionHome("sess-context-only", "", codexLaunchSettings{
		Model:                      "gpt-5.4",
		ModelContextWindow:         1_047_576,
		ModelAutoCompactTokenLimit: 400_000,
	}, map[string]string{})
	if err != nil {
		t.Fatalf("prepareCodexSessionHome returned error: %v", err)
	}

	isolatedConfig := readTestFile(t, filepath.Join(isolatedHome, "config.toml"))
	if !bytes.Contains(isolatedConfig, []byte("model_context_window = 1047576")) {
		t.Fatalf("isolated config missing model_context_window\nconfig:\n%s", isolatedConfig)
	}
	if !bytes.Contains(isolatedConfig, []byte("model_auto_compact_token_limit = 400000")) {
		t.Fatalf("isolated config missing model_auto_compact_token_limit\nconfig:\n%s", isolatedConfig)
	}
}

func TestResolveCodexLaunchSettingsMatchesPresetAndNormalizesOneMillionSuffix(t *testing.T) {
	provider := config.Provider{
		DefaultModel: "gpt-5.4[1m]",
		Presets: map[string]config.Preset{
			"code": {
				Name:  "code",
				Model: "gpt-5.4[1m]",
				Parameters: config.Parameters{
					MaxContextLength: 1_000_000,
					ContextWindow: &config.ContextWindowConfig{
						ModelContextWindow:    1_047_576,
						AutoCompactTokenLimit: 400_000,
					},
				},
			},
		},
	}

	settings := resolveCodexLaunchSettings(provider, "gpt-5.4[1m]")
	if settings.Model != "gpt-5.4" {
		t.Fatalf("normalized model = %q, want %q", settings.Model, "gpt-5.4")
	}
	if settings.ModelContextWindow != 1_047_576 {
		t.Fatalf("model context window = %d, want %d", settings.ModelContextWindow, 1_047_576)
	}
	if settings.ModelAutoCompactTokenLimit != 400_000 {
		t.Fatalf("auto compact token limit = %d, want %d", settings.ModelAutoCompactTokenLimit, 400_000)
	}
}

func TestCleanupCodexSessionHomeRemovesRegisteredHome(t *testing.T) {
	app := &App{
		Log:               logging.NewService(t.TempDir()),
		codexSessionHomes: map[string]codexSessionHomeInfo{},
	}
	t.Cleanup(app.Log.Close)

	isolatedHome := filepath.Join(t.TempDir(), "isolated-home")
	writeTestFile(t, filepath.Join(isolatedHome, "config.toml"), []byte("model = \"gpt-5\"\n"))
	app.codexSessionHomes["sess-cleanup"] = codexSessionHomeInfo{Path: isolatedHome, HomeKey: "provider:test"}

	app.cleanupCodexSessionHome("sess-cleanup")

	if _, ok := app.codexSessionHomes["sess-cleanup"]; ok {
		t.Fatalf("codexSessionHomes still contains cleaned session: %#v", app.codexSessionHomes)
	}
	if _, err := os.Stat(isolatedHome); err != nil {
		t.Fatalf("persistent home should remain on disk, stat err=%v", err)
	}
}

func TestCleanupCodexSessionHomesForStoppedSessionsRemovesOnlyStoppedOnes(t *testing.T) {
	root := t.TempDir()
	stoppedHome := filepath.Join(root, "stopped-home")
	runningHome := filepath.Join(root, "running-home")
	writeTestFile(t, filepath.Join(stoppedHome, "config.toml"), []byte("stopped\n"))
	writeTestFile(t, filepath.Join(runningHome, "config.toml"), []byte("running\n"))

	app := &App{
		Log:               logging.NewService(t.TempDir()),
		Sessions:          session.NewManager(),
		codexSessionHomes: map[string]codexSessionHomeInfo{},
	}
	t.Cleanup(app.Log.Close)

	stopped := app.Sessions.Create(session.AppTypeCodex, "codex", "", "", session.ModeTerminal, root, false)
	running := app.Sessions.Create(session.AppTypeCodex, "codex", "", "", session.ModeTerminal, root, false)
	app.Sessions.MarkStopped(stopped.ID)

	app.codexSessionHomes[stopped.ID] = codexSessionHomeInfo{Path: stoppedHome, HomeKey: "provider:stopped"}
	app.codexSessionHomes[running.ID] = codexSessionHomeInfo{Path: runningHome, HomeKey: "provider:running"}

	app.cleanupCodexSessionHomesForStoppedSessions()

	if _, ok := app.codexSessionHomes[stopped.ID]; ok {
		t.Fatalf("stopped session home should be removed from registry: %#v", app.codexSessionHomes)
	}
	if _, err := os.Stat(stoppedHome); err != nil {
		t.Fatalf("stopped persistent home should remain on disk, stat err=%v", err)
	}
	if _, ok := app.codexSessionHomes[running.ID]; !ok {
		t.Fatalf("running session home should remain registered: %#v", app.codexSessionHomes)
	}
	if _, err := os.Stat(runningHome); err != nil {
		t.Fatalf("running isolated home should remain, stat err=%v", err)
	}
}

func TestShutdownCleansAllRegisteredCodexHomes(t *testing.T) {
	root := t.TempDir()
	homeA := filepath.Join(root, "home-a")
	homeB := filepath.Join(root, "home-b")
	writeTestFile(t, filepath.Join(homeA, "config.toml"), []byte("a\n"))
	writeTestFile(t, filepath.Join(homeB, "config.toml"), []byte("b\n"))

	app := &App{
		Log:               logging.NewService(t.TempDir()),
		Launcher:          nil,
		Pty:               nil,
		Proxy:             nil,
		Remote:            nil,
		Tray:              nil,
		codexSessionHomes: map[string]codexSessionHomeInfo{},
	}
	t.Cleanup(app.Log.Close)

	app.codexSessionHomes["sess-a"] = codexSessionHomeInfo{Path: homeA, HomeKey: "provider:a"}
	app.codexSessionHomes["sess-b"] = codexSessionHomeInfo{Path: homeB, HomeKey: "provider:b"}

	app.cleanupAllCodexSessionHomes()

	if len(app.codexSessionHomes) != 0 {
		t.Fatalf("all codex session homes should be removed from registry, got %#v", app.codexSessionHomes)
	}
	if _, err := os.Stat(homeA); err != nil {
		t.Fatalf("homeA should remain on disk, stat err=%v", err)
	}
	if _, err := os.Stat(homeB); err != nil {
		t.Fatalf("homeB should remain on disk, stat err=%v", err)
	}
}
