package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/session"
)

func setCodexTestHome(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
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

func TestPrepareCodexSessionHomeUsesIsolatedCODEXHOMEAndPreservesSourceFiles(t *testing.T) {
	home := setCodexTestHome(t)
	sourceHome := filepath.Join(home, ".codex")
	sourceConfigPath := filepath.Join(sourceHome, "config.toml")
	sourceAuthPath := filepath.Join(sourceHome, "auth.json")
	sourceHistoryPath := filepath.Join(sourceHome, "history.jsonl")
	configPrefix := "approval_policy = \"never\"\nmodel = \"gpt-5\"\n"
	mcpSuffix := "[mcp_servers.keep]\ncommand = \"npx\"\nargs = [\"-y\", \"@modelcontextprotocol/server-filesystem\"]\n"
	sourceConfig := []byte(configPrefix + mcpSuffix)
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

	isolatedHome, err := app.prepareCodexSessionHome("sess-openai", envOverrides)
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
	providerName, ok := app.codexSessionHomes["sess-openai"]
	if !ok {
		t.Fatalf("codexSessionHomes missing entry for session: %#v", app.codexSessionHomes)
	}
	providerSection := buildCodexIsolatedProviderSection(providerName.ProviderName, "https://example.com/v1")
	providerSectionStart := bytes.Index(isolatedConfig, providerSection)
	if providerSectionStart == -1 {
		t.Fatalf("isolated config missing provider section\nwant snippet:\n%s\n\ngot:\n%s", providerSection, isolatedConfig)
	}
	firstTable := bytes.Index(isolatedConfig, []byte("[mcp_servers.keep]"))
	if firstTable == -1 {
		t.Fatalf("isolated config missing source table suffix\nconfig:\n%s", isolatedConfig)
	}
	if providerSectionStart >= firstTable {
		t.Fatalf("provider section must be inserted before first table\nproviderStart=%d firstTable=%d\nconfig:\n%s", providerSectionStart, firstTable, isolatedConfig)
	}
	if !bytes.Contains(isolatedConfig[:firstTable], []byte("model_provider = \""+providerName.ProviderName+"\"\n")) {
		t.Fatalf("isolated config missing root-level model_provider before first table\nconfig:\n%s", isolatedConfig)
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

	isolatedHome, err := app.prepareCodexSessionHome("sess-malformed-auth", envOverrides)
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

	isolatedHome, err := app.prepareCodexSessionHome("sess-safe-copy", envOverrides)
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

func TestBuildCodexIsolatedConfigTomlReplacesExistingRootModelProviderBeforeFirstTable(t *testing.T) {
	source := []byte("approval_policy = \"never\"\nmodel_provider = \"user-provider\"\nmodel = \"gpt-5\"\n[mcp_servers.keep]\ncommand = \"keep\"\n")
	providerName := "amagi-codebox-provider-sess-root"
	baseURL := "https://example.com/v1"

	got := buildCodexIsolatedConfigToml(source, providerName, baseURL)
	wantPrefix := "approval_policy = \"never\"\nmodel = \"gpt-5\"\nmodel_provider = \"" + providerName + "\"\n\n"
	if !bytes.HasPrefix(got, []byte(wantPrefix)) {
		t.Fatalf("isolated config prefix mismatch\nwant prefix:\n%s\n\ngot:\n%s", wantPrefix, got)
	}
	if bytes.Contains(got, []byte("model_provider = \"user-provider\"")) {
		t.Fatalf("old root-level model_provider should be removed\nconfig:\n%s", got)
	}
	if bytes.Count(got, []byte("model_provider = ")) != 1 {
		t.Fatalf("isolated config should contain exactly one root model_provider\nconfig:\n%s", got)
	}
	firstTable := bytes.Index(got, []byte("[mcp_servers.keep]"))
	if firstTable == -1 {
		t.Fatalf("isolated config missing original table suffix\nconfig:\n%s", got)
	}
	if !bytes.Equal(got[firstTable:], []byte("[mcp_servers.keep]\ncommand = \"keep\"\n")) {
		t.Fatalf("table suffix should remain byte-for-byte\nwant:\n%s\n\ngot:\n%s", "[mcp_servers.keep]\ncommand = \"keep\"\n", got[firstTable:])
	}
	providerSection := buildCodexIsolatedProviderSection(providerName, baseURL)
	providerStart := bytes.Index(got, providerSection)
	if providerStart == -1 || providerStart >= firstTable {
		t.Fatalf("provider section should appear before first table\nconfig:\n%s", got)
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
	app.codexSessionHomes["sess-cleanup"] = codexSessionHomeInfo{Path: isolatedHome, ProviderName: "amagi-codebox-provider-test"}

	app.cleanupCodexSessionHome("sess-cleanup")

	if _, ok := app.codexSessionHomes["sess-cleanup"]; ok {
		t.Fatalf("codexSessionHomes still contains cleaned session: %#v", app.codexSessionHomes)
	}
	if _, err := os.Stat(isolatedHome); !os.IsNotExist(err) {
		t.Fatalf("isolated home should be removed, stat err=%v", err)
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

	app.codexSessionHomes[stopped.ID] = codexSessionHomeInfo{Path: stoppedHome, ProviderName: "amagi-codebox-provider-stopped"}
	app.codexSessionHomes[running.ID] = codexSessionHomeInfo{Path: runningHome, ProviderName: "amagi-codebox-provider-running"}

	app.cleanupCodexSessionHomesForStoppedSessions()

	if _, ok := app.codexSessionHomes[stopped.ID]; ok {
		t.Fatalf("stopped session home should be removed from registry: %#v", app.codexSessionHomes)
	}
	if _, err := os.Stat(stoppedHome); !os.IsNotExist(err) {
		t.Fatalf("stopped isolated home should be removed, stat err=%v", err)
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

	app.codexSessionHomes["sess-a"] = codexSessionHomeInfo{Path: homeA, ProviderName: "provider-a"}
	app.codexSessionHomes["sess-b"] = codexSessionHomeInfo{Path: homeB, ProviderName: "provider-b"}

	app.cleanupAllCodexSessionHomes()

	if len(app.codexSessionHomes) != 0 {
		t.Fatalf("all codex session homes should be removed from registry, got %#v", app.codexSessionHomes)
	}
	if _, err := os.Stat(homeA); !os.IsNotExist(err) {
		t.Fatalf("homeA should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(homeB); !os.IsNotExist(err) {
		t.Fatalf("homeB should be removed, stat err=%v", err)
	}
}
