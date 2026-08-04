package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/proxy"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
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

	// Headroom/Proxy are touched by LaunchSession's proxy-selection switch
	// (even in the all-off default branch, which calls Headroom.IsRunning).
	// Initialize them with a real (idle) service so App-layer tests that
	// exercise LaunchSession do not nil-deref. They are never started here.
	testProcessRunner := platform.NewProcessRunner()

	return &App{
		Log:      logSvc,
		Config:   cfgSvc,
		Secrets:  secretsSvc,
		Sessions: session.NewManager(),
		Launcher: launcher.NewLauncherService(logSvc, envVarsSvc),
		Pty:      pty.NewService(logSvc),
		EnvVars:  envVarsSvc,
		Paths:    pathsSvc,
		Proxy:    proxy.NewProxyService(),
		Headroom: headroom.NewHeadroomService(testProcessRunner, logSvc),
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
		Config:    cfgSvc,
		Secrets:   secretsSvc,
		Sessions:  session.NewManager(),
		Launcher:  launcher.NewLauncherService(logSvc, envVarsSvc),
		Pty:       pty.NewService(logSvc),
		EnvVars:   envVarsSvc,
		Paths:     pathsSvc,
		Log:       logSvc,
		Settings:  settingsSvc,
		configDir: configDir,
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

// fakeResolver records Resolve calls and returns a standard-binary path for the
// canonical Claude app type only (proves the provider routes through Resolve and
// maps availability to bool; it does NOT replicate CLI candidate mapping).
type fakeResolver struct {
	called []string
}

func (f *fakeResolver) Resolve(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	f.called = append(f.called, req.AppType)
	if req.LaunchMode != string(session.ModeEmbedded) {
		return platform.ResolvedLaunchSpec{}, nil
	}
	spec := platform.ResolvedLaunchSpec{AppType: req.AppType, LaunchMode: req.LaunchMode}
	if req.AppType == string(contract.CLITypeClaudeCode) {
		spec.CLI = platform.ResolvedCLI{Path: "/usr/bin/claude"}
	}
	return spec, nil
}

func (f *fakeResolver) ResolveExecutable(string, []string, []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	panic("provider must not call ResolveExecutable")
}

func TestHostSummaryProviderUsesResolverAppType(t *testing.T) {
	r := &fakeResolver{}
	hs, err := hostSummaryFromResolver(r, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if hs.APIVersion != contract.APIVersionV1 || hs.ServerVersion != "1.2.3" {
		t.Fatalf("host summary header wrong: %+v", hs)
	}
	if len(hs.CLIAvailability) != len(contract.KnownCLITypes) {
		t.Fatalf("availability len=%d want %d", len(hs.CLIAvailability), len(contract.KnownCLITypes))
	}
	// Every KnownCLIType was resolved exactly once via Resolve (no ResolveExecutable).
	if len(r.called) != len(contract.KnownCLITypes) {
		t.Fatalf("Resolve called %d times want %d", len(r.called), len(contract.KnownCLITypes))
	}
	byType := map[contract.CLIType]bool{}
	for _, a := range hs.CLIAvailability {
		byType[a.CLIType] = a.Available
	}
	// Canonical Claude app type is available (standard binary); others are not.
	if !byType[contract.CLITypeClaudeCode] {
		t.Fatal("claudecode should be available via resolver standard binary")
	}
	for _, ct := range []contract.CLIType{contract.CLITypeOpenCode, contract.CLITypeCodex, contract.CLITypePi} {
		if byType[ct] {
			t.Fatalf("%s should be unavailable", ct)
		}
	}
}

// --- M1-B2c1: App user-initiated listen-config events ---

func validHostSummaryMain() contract.HostSummary {
	return contract.HostSummary{
		APIVersion:    contract.APIVersionV1,
		ServerVersion: "test",
		CLIAvailability: []contract.CLIAvailability{
			{CLIType: contract.CLITypeClaudeCode, Available: true},
			{CLIType: contract.CLITypeOpenCode, Available: false},
			{CLIType: contract.CLITypeCodex, Available: false},
			{CLIType: contract.CLITypePi, Available: false},
		},
	}
}

func TestAppConfigEventOnUserChange(t *testing.T) {
	app := newTestAppWithRemote(t)
	secDir := t.TempDir()
	opts := remote.NewProductionSecurityOptions(secDir, func() (contract.HostSummary, error) {
		return validHostSummaryMain(), nil
	})
	secSrv := remote.NewServerWithSecurity(0, app, app.Log, embed.FS{}, opts)
	app.Remote = secSrv
	if err := secSrv.LoadSecurityState(); err != nil {
		t.Fatalf("LoadSecurityState: %v", err)
	}

	configEventCount := func() int {
		n := 0
		for _, r := range func() []remote.SecurityEventRecord { r, _ := secSrv.ListRemoteSecurityEvents(0); return r }() {
			if r.Kind == "remote_listen_configuration_changed" {
				n++
			}
		}
		return n
	}

	// Startup-style direct SetPort must NOT emit a config event.
	secSrv.SetPort(8680)
	if configEventCount() != 0 {
		t.Fatal("direct SetPort (Startup-style restore) must not emit a config event")
	}

	// User SetRemotePort success → emits exactly one config event.
	if err := app.SetRemotePort(9001); err != nil {
		t.Fatalf("SetRemotePort: %v", err)
	}
	if configEventCount() != 1 {
		t.Fatalf("after SetRemotePort: config events=%d want 1", configEventCount())
	}

	// User SetRemoteHost success → emits a second config event.
	if err := app.SetRemoteHost("127.0.0.1"); err != nil {
		t.Fatalf("SetRemoteHost: %v", err)
	}
	if configEventCount() != 2 {
		t.Fatalf("after SetRemoteHost: config events=%d want 2", configEventCount())
	}
}

func TestAppListRemoteSecurityEventsWrapper(t *testing.T) {
	app := newTestAppWithRemote(t)
	secDir := t.TempDir()
	opts := remote.NewProductionSecurityOptions(secDir, func() (contract.HostSummary, error) {
		return validHostSummaryMain(), nil
	})
	secSrv := remote.NewServerWithSecurity(0, app, app.Log, embed.FS{}, opts)
	app.Remote = secSrv
	if err := secSrv.LoadSecurityState(); err != nil {
		t.Fatalf("LoadSecurityState: %v", err)
	}

	// Emit a closed event via the App wrapper (SetRemotePort emits config_changed).
	if err := app.SetRemotePort(9001); err != nil {
		t.Fatalf("SetRemotePort: %v", err)
	}

	// App.ListRemoteSecurityEvents(0) returns the sanitized record.
	recs, err := app.ListRemoteSecurityEvents(0)
	if err != nil {
		t.Fatalf("ListRemoteSecurityEvents(0): %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("expected at least one event")
	}
	if recs[0].Kind != "remote_listen_configuration_changed" {
		t.Fatalf("first record kind=%q want remote_listen_configuration_changed", recs[0].Kind)
	}

	// Invalid limit propagated as error.
	if _, err := app.ListRemoteSecurityEvents(-1); err == nil {
		t.Fatal("invalid limit must return error via the wrapper")
	}
	if _, err := app.ListRemoteSecurityEvents(501); err == nil {
		t.Fatal("limit>500 must return error via the wrapper")
	}
}

// --- Minor-02: SetRemoteEndpoint is a single transaction (host+port) ---

// newRemoteAppForEndpoint builds an App wired with a real security Remote and a
// loaded Settings service so SetRemoteEndpoint can be exercised transactionally.
func newRemoteAppForEndpoint(t *testing.T) *App {
	t.Helper()
	app := newTestAppWithRemote(t)
	secDir := t.TempDir()
	opts := remote.NewProductionSecurityOptions(secDir, func() (contract.HostSummary, error) {
		return validHostSummaryMain(), nil
	})
	secSrv := remote.NewServerWithSecurity(0, app, app.Log, embed.FS{}, opts)
	app.Remote = secSrv
	if err := secSrv.LoadSecurityState(); err != nil {
		t.Fatalf("LoadSecurityState: %v", err)
	}
	return app
}

// TestSetRemoteEndpoint_Success_BothPersisted: a valid host+port persists BOTH
// in one Save and updates the live server host/port.
func TestSetRemoteEndpoint_Success_BothPersisted(t *testing.T) {
	app := newRemoteAppForEndpoint(t)
	if err := app.SetRemoteEndpoint("192.168.1.55", 9999); err != nil {
		t.Fatalf("SetRemoteEndpoint: %v", err)
	}
	if got := app.Settings.GetRemoteHost(); got != "192.168.1.55" {
		t.Fatalf("GetRemoteHost=%q want 192.168.1.55", got)
	}
	if got := app.Settings.GetRemotePort(); got != 9999 {
		t.Fatalf("GetRemotePort=%d want 9999", got)
	}
	if got := app.Remote.GetHost(); got != "192.168.1.55" {
		t.Fatalf("server host=%q want 192.168.1.55", got)
	}
	if got := app.Remote.GetPort(); got != 9999 {
		t.Fatalf("server port=%d want 9999", got)
	}
}

// TestSetRemoteEndpoint_BadPort_HostNotPersisted: an out-of-range port fails
// validation with NO persistence — the host is NOT applied (transactional).
func TestSetRemoteEndpoint_BadPort_HostNotPersisted(t *testing.T) {
	app := newRemoteAppForEndpoint(t)
	hostBefore := app.Remote.GetHost()
	portBefore := app.Remote.GetPort()
	settingsHostBefore := app.Settings.GetRemoteHost()
	if err := app.SetRemoteEndpoint("10.0.0.5", 80); err == nil {
		t.Fatal("port 80 must be rejected (out of range)")
	}
	// Neither host nor port changed (compare each surface to its own before value).
	if got := app.Remote.GetHost(); got != hostBefore {
		t.Fatalf("server host leaked on bad port: got=%q want=%q", got, hostBefore)
	}
	if got := app.Remote.GetPort(); got != portBefore {
		t.Fatalf("server port leaked on bad port: got=%d want=%d", got, portBefore)
	}
	if got := app.Settings.GetRemoteHost(); got != settingsHostBefore {
		t.Fatalf("settings host leaked on bad port: got=%q want=%q", got, settingsHostBefore)
	}
}

// TestSetRemoteEndpoint_BadHost_PortNotPersisted: an empty host fails validation
// with NO persistence — the port is NOT applied (transactional).
func TestSetRemoteEndpoint_BadHost_PortNotPersisted(t *testing.T) {
	app := newRemoteAppForEndpoint(t)
	hostBefore := app.Remote.GetHost()
	portBefore := app.Remote.GetPort()
	settingsPortBefore := app.Settings.GetRemotePort()
	if err := app.SetRemoteEndpoint("   ", 7777); err == nil {
		t.Fatal("empty/blank host must be rejected")
	}
	if got := app.Remote.GetHost(); got != hostBefore {
		t.Fatalf("server host leaked on bad host: got=%q want=%q", got, hostBefore)
	}
	if got := app.Remote.GetPort(); got != portBefore {
		t.Fatalf("server port leaked on bad host: got=%d want=%d", got, portBefore)
	}
	if got := app.Settings.GetRemotePort(); got != settingsPortBefore {
		t.Fatalf("settings port leaked on bad host: got=%d want=%d", got, settingsPortBefore)
	}
}

// TestSetRemoteEndpoint_AtomicReload: reloading the SAME Settings service from
// disk after a successful SetRemoteEndpoint reads back BOTH values (on-disk
// atomicity, not just in-memory).
func TestSetRemoteEndpoint_AtomicReload(t *testing.T) {
	app := newRemoteAppForEndpoint(t)
	if err := app.SetRemoteEndpoint("172.16.0.9", 8681); err != nil {
		t.Fatalf("SetRemoteEndpoint: %v", err)
	}
	// Reload the live Settings service from its on-disk file and verify both.
	if err := app.Settings.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := app.Settings.GetRemoteHost(); got != "172.16.0.9" {
		t.Fatalf("reloaded host=%q want 172.16.0.9", got)
	}
	if got := app.Settings.GetRemotePort(); got != 8681 {
		t.Fatalf("reloaded port=%d want 8681", got)
	}
}

// --- R2-Minor-02: App-layer endpoint/toggle lifecycle serialization ---

// appEndpointPairs are distinct valid (host,port) tuples for the App concurrency
// tests. The live server stores host/port in separate fields, so without the
// remoteLifecycleMu two concurrent endpoints could interleave SetHost/SetPort
// and leave a mixed tuple (host_i, port_j).
func appEndpointPairs(n int) []struct {
	host string
	port int
} {
	out := make([]struct {
		host string
		port int
	}, n)
	for i := 0; i < n; i++ {
		out[i] = struct {
			host string
			port int
		}{host: fmt.Sprintf("10.0.0.%d", i+2), port: 9000 + i}
	}
	return out
}

// TestSetRemoteEndpoint_AppConcurrent_LiveTupleNotMixed runs N concurrent
// App.SetRemoteEndpoint calls (server NOT running, so the stopped-check→
// SetHost→SetPort→Save path is exercised). With remoteLifecycleMu the live
// server host:port must be ONE consistent pair — never a mix. Run with -race.
func TestSetRemoteEndpoint_AppConcurrent_LiveTupleNotMixed(t *testing.T) {
	app := newRemoteAppForEndpoint(t)
	app.ctx = t.Context()
	pairs := appEndpointPairs(8)
	var wg sync.WaitGroup
	for _, p := range pairs {
		wg.Add(1)
		go func(p struct {
			host string
			port int
		}) {
			defer wg.Done()
			if err := app.SetRemoteEndpoint(p.host, p.port); err != nil {
				t.Errorf("SetRemoteEndpoint(%s,%d): %v", p.host, p.port, err)
			}
		}(p)
	}
	wg.Wait()
	host := app.Remote.GetHost()
	port := app.Remote.GetPort()
	matched := false
	for _, p := range pairs {
		if host == p.host && port == p.port {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("live tuple (%s,%d) is MIXED — concurrent endpoints interleaved SetHost/SetPort", host, port)
	}
	// Live tuple must match persisted settings (single transaction).
	if got := app.Settings.GetRemoteHost(); got != host {
		t.Fatalf("settings host=%q != live host=%q", got, host)
	}
	if got := app.Settings.GetRemotePort(); got != port {
		t.Fatalf("settings port=%d != live port=%d", got, port)
	}
}

// TestSetRemoteEndpoint_VsToggle_Serialized runs concurrent SetRemoteEndpoint
// and ToggleRemoteServer(false). With remoteLifecycleMu they serialize: no
// panic, and the final live host:port is a consistent tuple (the endpoint's, or
// the pre-call baseline if the toggle ran last without changing host/port). The
// toggle(false) path does not touch host/port, so the endpoint's pair wins.
func TestSetRemoteEndpoint_VsToggle_Serialized(t *testing.T) {
	for trial := 0; trial < 10; trial++ {
		app := newRemoteAppForEndpoint(t)
		app.ctx = t.Context()
		// Baseline endpoint.
		if err := app.SetRemoteEndpoint("10.0.0.1", 9001); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = app.SetRemoteEndpoint("10.0.0.9", 9009)
		}()
		go func() {
			defer wg.Done()
			// Toggle off (server not running → Stop is a no-op; persists enabled=false).
			_ = app.ToggleRemoteServer(false)
		}()
		wg.Wait()
		host := app.Remote.GetHost()
		port := app.Remote.GetPort()
		// The toggle(false) never changes host/port, so the final tuple is either
		// the new endpoint pair or the baseline — both are consistent (not mixed).
		consistent := (host == "10.0.0.9" && port == 9009) || (host == "10.0.0.1" && port == 9001)
		if !consistent {
			t.Fatalf("trial %d: live tuple (%s,%d) is MIXED after endpoint-vs-toggle", trial, host, port)
		}
		// Live host/port must agree with persisted settings.
		if app.Settings.GetRemoteHost() != host || app.Settings.GetRemotePort() != port {
			t.Fatalf("trial %d: settings (%s,%d) != live (%s,%d)", trial, app.Settings.GetRemoteHost(), app.Settings.GetRemotePort(), host, port)
		}
	}
}

// --- R3-Minor-02 ①: OpenRemoteWebUI shares remoteLifecycleMu ---

// TestOpenRemoteWebUI_SerializedUnderLifecycleMu proves OpenRemoteWebUI acquires
// remoteLifecycleMu (R3-Minor-02 ①): while another lifecycle op holds the mutex,
// OpenRemoteWebUI blocks; once released it proceeds. This closes the bypass
// where OpenRemoteWebUI's Remote.Start could interleave with SetRemoteEndpoint.
func TestOpenRemoteWebUI_SerializedUnderLifecycleMu(t *testing.T) {
	app := newRemoteAppForEndpoint(t)
	app.ctx = t.Context()

	// Hold the lifecycle mutex from another goroutine.
	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		app.remoteLifecycleMu.Lock()
		close(held)
		<-release
		app.remoteLifecycleMu.Unlock()
	}()
	<-held // deterministic: the goroutine now holds the mutex

	// OpenRemoteWebUI must block while the mutex is held.
	openDone := make(chan error, 1)
	go func() {
		_, err := app.OpenRemoteWebUI()
		openDone <- err
	}()
	select {
	case err := <-openDone:
		t.Fatalf("OpenRemoteWebUI returned (err=%v) while remoteLifecycleMu was held — mutex not acquired", err)
	case <-time.After(40 * time.Millisecond):
		// Good: blocked.
	}
	// Release; OpenRemoteWebUI should now proceed (it returns an error because the
	// test app has no real mobile web / Wails browser runtime, but that is fine —
	// we only asserted the serialization).
	close(release)
	select {
	case <-openDone:
	case <-time.After(2 * time.Second):
		t.Fatal("OpenRemoteWebUI did not return after the mutex was released")
	}
}

// --- R4-Minor: OpenRemoteWebUI persistence-failure three-way consistency ---

// TestOpenRemoteWebUI_SetRemoteEnabledFails_StopsServer_ThreeWayConsistent
// proves the R4-Minor fix: when Open starts the server but SetRemoteEnabled(true)
// persistence fails, Open STOPS the just-started server and returns an error
// (symmetric with Toggle(true)) so running/settings/disk stay consistent at the
// pre-call value, instead of leaving a running server whose enabled state did
// not persist.
func TestOpenRemoteWebUI_SetRemoteEnabledFails_StopsServer_ThreeWayConsistent(t *testing.T) {
	app := newRemoteAppForEndpoint(t)
	app.ctx = t.Context()
	app.Remote.SetPort(0)
	app.Remote.SetHost("127.0.0.1")

	// Make the Web UI Openable: configure a webRoot with an index.html so
	// GetRemoteWebUIStatus.Openable==true and the Start path is reached.
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := app.Settings.SetMobileWebRoot(webDir); err != nil {
		t.Fatalf("SetMobileWebRoot: %v", err)
	}

	// Inject a Save failure for SetRemoteEnabled(true): make the .tmp path a dir.
	tmpDir := filepath.Join(app.configDir, "settings.json.tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir tmp fault: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	res, err := app.OpenRemoteWebUI()
	if err == nil {
		t.Fatal("OpenRemoteWebUI must return an error when SetRemoteEnabled persistence fails")
	}
	_ = res
	// Three-way consistency at the pre-call value (enabled was false, not running):
	// live server stopped, settings reverted, disk unchanged.
	if app.Remote.IsRunning() {
		t.Fatal("server must be STOPPED after Open's SetRemoteEnabled failure (no running/disk drift)")
	}
	if app.Settings.GetRemoteEnabled() {
		t.Fatal("settings enabled must be REVERTED to false after Open's persistence failure")
	}
	// Disk: reload a fresh service and confirm enabled is still false.
	fresh := settings.NewService(app.configDir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fresh.GetRemoteEnabled() {
		t.Fatal("disk enabled must still be false (SetRemoteEnabled failed before rename)")
	}
}
