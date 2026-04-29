package main

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
)

// testMobileFSSource is embedded from the remote package's testdata so that
// App-layer tests can create a Server with real embedded mobile assets.
// Keys are "testdata/mobile/dist/...".
//
//go:embed internal/remote/testdata/mobile/dist
var appTestMobileFSSource embed.FS

// newTestSecretsService creates a SecretsService backed by an in-memory
// store, bypassing the real macOS Keychain / OS credential manager.
// This prevents test hangs caused by Keychain authorization prompts.
func newTestSecretsService(t *testing.T, configDir string) *secrets.SecretsService {
	t.Helper()
	store := &memorySecretStore{data: map[string]string{}}
	svc := secrets.NewSecretsServiceWithStore(configDir, store)
	if err := svc.Load(); err != nil {
		t.Fatalf("load secrets: %v", err)
	}
	return svc
}

// memorySecretStore implements secrets.SecretStore using an in-memory map.
// It is safe for concurrent use within a single test.
type memorySecretStore struct {
	data map[string]string
}

func (m *memorySecretStore) Load(path string) (map[string]string, error) {
	_ = path
	cp := make(map[string]string, len(m.data))
	for k, v := range m.data {
		cp[k] = v
	}
	return cp, nil
}

func (m *memorySecretStore) Save(path string, values map[string]string) error {
	_ = path
	m.data = make(map[string]string, len(values))
	for k, v := range values {
		m.data[k] = v
	}
	return nil
}

func (m *memorySecretStore) Kind() string { return "memory" }

func (m *memorySecretStore) LegacyImportPath(path string) string { return path }

// newTestApp creates a minimal App with all services wired for testing.
func newTestApp(t *testing.T) *App {
	app, _ := newTestAppWithConfigDir(t)
	return app
}

func TestEmbeddedDefaultLaunchMode_EmptyModeDefaultsToEmbedded(t *testing.T) {
	if got := embeddedDefaultLaunchMode(""); got != session.ModeEmbedded {
		t.Fatalf("empty mode resolved to %q, want %q", got, session.ModeEmbedded)
	}
	if got := embeddedDefaultLaunchMode(string(session.ModeTerminal)); got != session.ModeTerminal {
		t.Fatalf("explicit terminal mode resolved to %q, want %q", got, session.ModeTerminal)
	}
}

func newTestAppWithConfigDir(t *testing.T) (*App, string) {
	t.Helper()
	configDir := t.TempDir()
	logSvc := logging.NewService(configDir)
	t.Cleanup(logSvc.Close)

	cfgSvc := config.NewConfigService(configDir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}

	// Use an in-memory secret store to avoid accessing the real macOS
	// Keychain, which may block indefinitely in test processes (e.g. when
	// the Keychain is locked or the test runner lacks UI authorization).
	secretsSvc := newTestSecretsService(t, configDir)

	envVarsSvc := envvars.NewEnvVarsService(configDir)
	if err := envVarsSvc.Load(); err != nil {
		t.Fatalf("load envvars: %v", err)
	}

	pathsSvc := paths.NewPathsService(configDir)

	return &App{
		Log:      logSvc,
		Config:   cfgSvc,
		Secrets:  secretsSvc,
		Sessions: session.NewManager(),
		Launcher: launcher.NewLauncherService(logSvc, envVarsSvc),
		Pty:      pty.NewService(logSvc),
		EnvVars:  envVarsSvc,
		Paths:    pathsSvc,
	}, configDir
}

// envHasKey reports whether env (slice of "K=V") contains the given key with the expected value.
func envHasKey(env []string, key, wantValue string) bool {
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		// Windows: case-insensitive key comparison
		if strings.EqualFold(k, key) && v == wantValue {
			return true
		}
	}
	return false
}

// envHasKeySet reports whether env contains the given key (regardless of value).
func envHasKeySet(env []string, key string) bool {
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

// readEnvValue returns the value for key from a "K=V" slice, or "".
func readEnvValue(env []string, key string) string {
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// ============================================================================
// B. Unit-level helpers (cross-platform, keep for fast feedback)
// ============================================================================

func TestIsOpenAIProvider_AuthKeyFallback(t *testing.T) {
	p := config.Provider{AuthKey: "OPENAI_API_KEY"}
	if !isOpenAIProvider(p) {
		t.Fatal("isOpenAIProvider should return true when AuthKey=OPENAI_API_KEY even with empty Type")
	}

	p2 := config.Provider{Type: "OpenAI"}
	if !isOpenAIProvider(p2) {
		t.Fatal("isOpenAIProvider should match Type case-insensitively")
	}

	p3 := config.Provider{Type: "anthropic", AuthKey: "ANTHROPIC_API_KEY"}
	if isOpenAIProvider(p3) {
		t.Fatal("isOpenAIProvider should return false for Anthropic provider")
	}

	p4 := config.Provider{}
	if isOpenAIProvider(p4) {
		t.Fatal("isOpenAIProvider should return false for empty provider")
	}
}

// --- Regression: StopSession/RemoveSession/ClearStopped without isolation ---

func TestStopSessionWithoutCodexHomeIsolation(t *testing.T) {
	app := newTestApp(t)

	sess := app.Sessions.Create(session.AppTypeCodex, "codex", "", "gpt-5", session.ModeTerminal, t.TempDir(), false)
	app.Sessions.MarkStopped(sess.ID)

	err := app.StopSession(sess.ID)
	if err != nil {
		t.Fatalf("StopSession on already-stopped session should not error, got: %v", err)
	}
}

func TestRemoveSessionWithoutCodexHomeIsolation(t *testing.T) {
	app := newTestApp(t)

	sess := app.Sessions.Create(session.AppTypeCodex, "codex", "", "gpt-5", session.ModeTerminal, t.TempDir(), false)
	app.Sessions.MarkStopped(sess.ID)

	err := app.RemoveSession(sess.ID)
	if err != nil {
		t.Fatalf("RemoveSession should succeed, got: %v", err)
	}
}

func TestClearStoppedSessionsWithoutCodexHomeIsolation(t *testing.T) {
	app := newTestApp(t)

	sess := app.Sessions.Create(session.AppTypeCodex, "codex", "", "gpt-5", session.ModeTerminal, t.TempDir(), false)
	app.Sessions.MarkStopped(sess.ID)

	cleared := app.ClearStoppedSessions()
	if cleared != 1 {
		t.Fatalf("expected 1 cleared session, got %d", cleared)
	}
}

func TestStopAllSessionsWithoutCodexHomeIsolation(t *testing.T) {
	app := newTestApp(t)
	_ = app.Sessions.Create(session.AppTypeCodex, "codex", "", "gpt-5", session.ModeTerminal, t.TempDir(), false)
	app.StopAllSessions()
}

// --- Model name normalization ---

func TestNormalizeCodexModelName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"gpt-5.4", "gpt-5.4"},
		{"gpt-5.4[1m]", "gpt-5.4"},
		{"  gpt-5.4[1m]  ", "gpt-5.4"},
		{"", ""},
		{"  ", ""},
	}
	for _, tt := range tests {
		got := normalizeCodexModelName(tt.input)
		if got != tt.want {
			t.Errorf("normalizeCodexModelName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- resolveCodexLaunchSettings only resolves model name ---

func TestResolveCodexLaunchSettings_ModelOnly(t *testing.T) {
	provider := config.Provider{
		DefaultModel: "gpt-5.4[1m]",
		Presets: map[string]config.Preset{
			"code": {
				Name:  "code",
				Model: "gpt-5.4[1m]",
			},
		},
	}

	settings := resolveCodexLaunchSettings(provider, "gpt-5.4[1m]")
	if settings.Model != "gpt-5.4" {
		t.Fatalf("normalized model = %q, want %q", settings.Model, "gpt-5.4")
	}
}

func TestResolveCodexLaunchSettings_FallsBackToProviderDefault(t *testing.T) {
	provider := config.Provider{
		DefaultModel: "gpt-5.4[1m]",
	}

	settings := resolveCodexLaunchSettings(provider, "")
	if settings.Model != "gpt-5.4" {
		t.Fatalf("normalized model = %q, want %q", settings.Model, "gpt-5.4")
	}
}

// --- BuildEnv unit test (kept for fast unit feedback) ---

func TestBuildEnv_OpenAIOverrides_ReachFinalEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/home/test"}
	overrides := map[string]string{
		"OPENAI_API_KEY":  "sk-test-key",
		"OPENAI_BASE_URL": "https://api.test.example.com/v1",
	}

	result := launcher.BuildEnv(base, overrides)

	if !envHasKey(result, "OPENAI_API_KEY", "sk-test-key") {
		t.Fatal("BuildEnv result should contain OPENAI_API_KEY=sk-test-key")
	}
	if !envHasKey(result, "OPENAI_BASE_URL", "https://api.test.example.com/v1") {
		t.Fatal("BuildEnv result should contain OPENAI_BASE_URL")
	}
	if envHasKeySet(result, "CODEX_HOME") {
		t.Fatal("BuildEnv result should not contain CODEX_HOME")
	}
}

// --- BuildEnv preserves pre-existing CODEX_HOME when not overridden ---

func TestBuildEnv_PreservesPreExistingCODEXHOME(t *testing.T) {
	origValue := `C:\Users\test\original-codex-home`
	base := []string{
		"PATH=C:\\Windows\\system32",
		"CODEX_HOME=" + origValue,
	}
	overrides := map[string]string{
		"OPENAI_API_KEY": "sk-test",
	}

	result := launcher.BuildEnv(base, overrides)

	got := readEnvValue(result, "CODEX_HOME")
	if got != origValue {
		t.Fatalf("CODEX_HOME = %q, want %q (preserved from base env)", got, origValue)
	}
}

// --- BuildEnv: overrides do NOT inject CODEX_HOME ---

func TestBuildEnv_NoCODEXHOMEInOverrides(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/home/test"}
	overrides := map[string]string{
		"OPENAI_API_KEY":  "sk-key",
		"OPENAI_BASE_URL": "https://api.test.example.com/v1",
	}

	result := launcher.BuildEnv(base, overrides)

	if envHasKeySet(result, "CODEX_HOME") {
		t.Fatal("BuildEnv should not contain CODEX_HOME when overrides don't set it and base doesn't have it")
	}
}

// --- Cross-platform provider/key tests ---

func TestGetProviderAPIKeyForFormat_LegacyFallback(t *testing.T) {
	app := newTestApp(t)

	const providerID = "legacy-provider"
	provider := config.Provider{
		Anthropic: &config.AnthropicFormat{Enabled: true},
		OpenAI:    &config.OpenAIFormat{Enabled: true},
	}
	if err := app.Config.SaveProvider(providerID, provider); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey(providerID+":openai", "sk-legacy-openai"); err != nil {
		t.Fatalf("SetAPIKey legacy: %v", err)
	}

	key, source := app.getProviderAPIKeyForFormat(providerID, "anthropic")
	if key != "sk-legacy-openai" {
		t.Fatalf("key = %q, want sk-legacy-openai", key)
	}
	if source != "legacy:openai" {
		t.Fatalf("source = %q, want legacy:openai", source)
	}
}

func TestGetProviderExportJSON_UsesSingleProviderAPIKey(t *testing.T) {
	app := newTestApp(t)

	const providerID = "export-provider"
	if err := app.Config.SaveProvider(providerID, config.Provider{
		Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://anthropic.example.com"},
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://openai.example.com/v1", Organization: "org-export"},
		DefaultModel: "claude-sonnet-4-5",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey(providerID, "sk-provider-export"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	if err := app.Secrets.SetAPIKey(providerID+":anthropic", "sk-legacy-should-not-export"); err != nil {
		t.Fatalf("Set legacy API key: %v", err)
	}

	jsonStr, err := app.GetProviderExportJSON(providerID)
	if err != nil {
		t.Fatalf("GetProviderExportJSON: %v", err)
	}
	if strings.Count(jsonStr, "\"api_key\"") != 1 {
		t.Fatalf("expected exactly one api_key in export JSON, got %d\n%s", strings.Count(jsonStr, "\"api_key\""), jsonStr)
	}

	var ep config.ExportProvider
	if err := json.Unmarshal([]byte(jsonStr), &ep); err != nil {
		t.Fatalf("unmarshal export JSON: %v", err)
	}
	if ep.APIKey != "sk-provider-export" {
		t.Fatalf("APIKey = %q, want sk-provider-export", ep.APIKey)
	}
	if ep.Anthropic != nil && ep.Anthropic.APIKey != "" {
		t.Fatal("Anthropic.APIKey should be empty in exported JSON")
	}
	if ep.OpenAI != nil && ep.OpenAI.APIKey != "" {
		t.Fatal("OpenAI.APIKey should be empty in exported JSON")
	}
}

func TestSaveProviderFromJSON_UnifiesProviderAPIKeyAndScrubsModels(t *testing.T) {
	app, configDir := newTestAppWithConfigDir(t)

	jsonStr := `{
		"default_model": "claude-sonnet-4-5",
		"api_key": "sk-provider-level",
		"anthropic": {
			"enabled": true,
			"api_key": "sk-anthropic-legacy",
			"base_url": "https://anthropic.example.com"
		},
		"openai": {
			"enabled": true,
			"api_key": "sk-openai-legacy",
			"base_url": "https://openai.example.com/v1",
			"organization": "org-import"
		}
	}`

	if err := app.SaveProviderFromJSON("json-provider", jsonStr); err != nil {
		t.Fatalf("SaveProviderFromJSON: %v", err)
	}

	if key, _ := app.Secrets.GetAPIKey("json-provider"); key != "sk-provider-level" {
		t.Fatalf("provider-level key = %q, want sk-provider-level", key)
	}
	if key, _ := app.Secrets.GetAPIKey("json-provider:anthropic"); key != "" {
		t.Fatalf("legacy anthropic key should be cleared, got %q", key)
	}
	if key, _ := app.Secrets.GetAPIKey("json-provider:openai"); key != "" {
		t.Fatalf("legacy openai key should be cleared, got %q", key)
	}

	data, err := os.ReadFile(filepath.Join(configDir, "models.json"))
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "sk-provider-level") || strings.Contains(content, "sk-anthropic-legacy") || strings.Contains(content, "sk-openai-legacy") {
		t.Fatalf("models.json should not contain any API key plaintext:\n%s", content)
	}
	if strings.Contains(content, "\"api_key\"") {
		t.Fatalf("models.json should not contain api_key fields:\n%s", content)
	}
}

// ============================================================================
// D. GetRemoteWebUIStatus with embedded mobile assets
// ============================================================================

// newTestAppWithRemote creates an App with a real Remote server backed by
// embedded mobile test fixtures from internal/remote/testdata.
// The embedded FS has "testdata/mobile/dist" prefix, matching test expectations.
func newTestAppWithRemote(t *testing.T) *App {
	t.Helper()
	configDir := t.TempDir()
	logSvc := logging.NewService(configDir)
	t.Cleanup(logSvc.Close)

	cfgSvc := config.NewConfigService(configDir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	secretsSvc := newTestSecretsService(t, configDir)
	envVarsSvc := envvars.NewEnvVarsService(configDir)
	if err := envVarsSvc.Load(); err != nil {
		t.Fatalf("load envvars: %v", err)
	}
	pathsSvc := paths.NewPathsService(configDir)
	settingsSvc := settings.NewService(configDir)
	if err := settingsSvc.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}

	app := &App{
		Config:   cfgSvc,
		Secrets:  secretsSvc,
		Sessions: session.NewManager(),
		Launcher: launcher.NewLauncherService(logSvc, envVarsSvc),
		Pty:      pty.NewService(logSvc),
		EnvVars:  envVarsSvc,
		Paths:    pathsSvc,
		Log:      logSvc,
		Settings: settingsSvc,
	}

	// Wire Remote with embedded test FS.
	// The embed directive is `//go:embed internal/remote/testdata/mobile/dist`
	// so the FS keys are "internal/remote/testdata/mobile/dist/...".
	srv := remote.NewServer(8680, app, logSvc, appTestMobileFSSource)
	srv.SetMobileAssetsPrefix("internal/remote/testdata/mobile/dist")
	app.Remote = srv

	return app
}

func TestGetRemoteWebUIStatus_EmbeddedAvailable_NoUserConfig(t *testing.T) {
	app := newTestAppWithRemote(t)
	app.ctx = t.Context()

	status := app.GetRemoteWebUIStatus()

	if !status.MobileWebEmbedded {
		t.Fatal("MobileWebEmbedded should be true when embedded assets are available")
	}
	if !status.MobileWebAvailable {
		t.Fatal("MobileWebAvailable should be true when embedded assets exist")
	}
	if status.MobileWebRootConfigured {
		t.Fatal("MobileWebRootConfigured should be false when no user directory is configured")
	}
	if !status.Openable {
		t.Fatalf("Openable should be true with embedded assets, got reason: %q", status.Reason)
	}
	if status.URL == "" {
		t.Fatal("URL should be populated when Openable is true")
	}
}

func TestGetRemoteWebUIStatus_NoEmbedded_NoUserConfig(t *testing.T) {
	app := newTestApp(t)
	// No Remote wired (nil), so GetRemoteWebUIStatus will panic/crash.
	// Instead, create Remote with empty FS.
	logSvc := logging.NewService(t.TempDir())
	t.Cleanup(logSvc.Close)
	app.Remote = remote.NewServer(8680, nil, logSvc, embed.FS{})
	app.ctx = t.Context()

	status := app.GetRemoteWebUIStatus()

	if status.MobileWebEmbedded {
		t.Fatal("MobileWebEmbedded should be false with empty FS")
	}
	if status.MobileWebAvailable {
		t.Fatal("MobileWebAvailable should be false with no embedded and no user config")
	}
	if status.MobileWebRootConfigured {
		t.Fatal("MobileWebRootConfigured should be false with no user config")
	}
	if status.Openable {
		t.Fatal("Openable should be false with no resources")
	}
}

func TestGetRemoteWebUIStatus_UserOverrideOverridesEmbedded(t *testing.T) {
	app := newTestAppWithRemote(t)
	app.ctx = t.Context()

	// Create a user web root with its own index.html
	userDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDir, "index.html"), []byte("<html>user</html>"), 0o644); err != nil {
		t.Fatalf("write user index.html: %v", err)
	}
	app.Remote.SetWebRoot(userDir)

	status := app.GetRemoteWebUIStatus()

	if !status.MobileWebEmbedded {
		t.Fatal("MobileWebEmbedded should still be true")
	}
	if !status.MobileWebRootConfigured {
		t.Fatal("MobileWebRootConfigured should be true when user dir is set")
	}
	if !status.MobileWebRootExists {
		t.Fatal("MobileWebRootExists should be true when user dir has index.html")
	}
	if !status.MobileWebAvailable {
		t.Fatal("MobileWebAvailable should be true")
	}
	if !status.Openable {
		t.Fatalf("Openable should be true, got reason: %q", status.Reason)
	}
}

func TestGetRemoteWebUIStatus_EmbeddedFallbackWhenUserInvalid(t *testing.T) {
	app := newTestAppWithRemote(t)
	app.ctx = t.Context()

	// Set a user dir without index.html -> should fall back to embedded
	userDir := t.TempDir()
	app.Remote.SetWebRoot(userDir)

	status := app.GetRemoteWebUIStatus()

	if !status.MobileWebEmbedded {
		t.Fatal("MobileWebEmbedded should be true")
	}
	if !status.MobileWebRootConfigured {
		t.Fatal("MobileWebRootConfigured should be true (dir is set)")
	}
	if status.MobileWebRootExists {
		t.Fatal("MobileWebRootExists should be false (no index.html)")
	}
	if !status.MobileWebAvailable {
		t.Fatal("MobileWebAvailable should be true due to embedded fallback")
	}
	if !status.Openable {
		t.Fatalf("Openable should be true via embedded fallback, got reason: %q", status.Reason)
	}
}

// ============================================================================
// E. Startup warnings mechanism
// ============================================================================

// TestGetStartupWarnings_Empty verifies that a freshly created app returns
// an empty slice (not nil) from GetStartupWarnings.
func TestGetStartupWarnings_Empty(t *testing.T) {
	app := newTestApp(t)
	warnings := app.GetStartupWarnings()
	if warnings == nil {
		t.Fatal("GetStartupWarnings should return empty slice, not nil")
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

// TestGetStartupWarnings_AfterAdd verifies that warnings recorded via
// addStartupWarning are returned by GetStartupWarnings.
func TestGetStartupWarnings_AfterAdd(t *testing.T) {
	app := newTestApp(t)
	app.addStartupWarning("first warning")
	app.addStartupWarning("second warning")

	warnings := app.GetStartupWarnings()
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d", len(warnings))
	}
	if warnings[0] != "first warning" {
		t.Fatalf("warnings[0] = %q, want %q", warnings[0], "first warning")
	}
	if warnings[1] != "second warning" {
		t.Fatalf("warnings[1] = %q, want %q", warnings[1], "second warning")
	}
}

// TestGetStartupWarnings_ReturnsCopy verifies that the returned slice is
// a copy and not a direct reference to the internal slice.
func TestGetStartupWarnings_ReturnsCopy(t *testing.T) {
	app := newTestApp(t)
	app.addStartupWarning("original")

	warnings := app.GetStartupWarnings()
	warnings[0] = "mutated"

	// Internal state should not be affected
	again := app.GetStartupWarnings()
	if again[0] != "original" {
		t.Fatalf("internal state was mutated: got %q, want %q", again[0], "original")
	}
}
