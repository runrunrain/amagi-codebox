package main

import (
	"encoding/json"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/opencodeconfig"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/usage"
)

func newPortableConfigTestApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := t.TempDir()
	logService := logging.NewService(configDir)
	t.Cleanup(logService.Close)

	configService := config.NewConfigService(configDir)
	if err := configService.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	secretService := secrets.NewSecretsServiceWithStore(configDir, &memorySecretStore{data: map[string]string{}})
	if err := secretService.Load(); err != nil {
		t.Fatalf("load secrets: %v", err)
	}
	settingsService := settings.NewService(configDir)
	if err := settingsService.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	pathsService := paths.NewPathsService(configDir)
	if err := pathsService.Load(); err != nil {
		t.Fatalf("load paths: %v", err)
	}
	envService := envvars.NewEnvVarsService(configDir)
	if err := envService.Load(); err != nil {
		t.Fatalf("load envvars: %v", err)
	}
	usageService := usage.NewService(configDir, logService)
	if err := usageService.Load(); err != nil {
		t.Fatalf("load usage: %v", err)
	}
	t.Cleanup(func() { _ = usageService.Close() })

	return &App{
		configDir:      configDir,
		Config:         configService,
		Secrets:        secretService,
		Settings:       settingsService,
		Paths:          pathsService,
		EnvVars:        envService,
		Usage:          usageService,
		OpenCodeConfig: opencodeconfig.NewService(),
		Log:            logService,
	}
}

func TestCompleteConfigRoundTripRestoresPortableState(t *testing.T) {
	app := newPortableConfigTestApp(t)
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.test/v1", AuthKey: "OPENAI_API_KEY"},
		DefaultModel: "gpt-test",
	}
	if err := app.Config.SaveProvider("example", provider); err != nil {
		t.Fatalf("save provider: %v", err)
	}
	if err := app.Config.SaveTerminalPreset("codex", "example/default", config.TerminalPreset{Name: "Default", Provider: "example", Model: "gpt-test"}); err != nil {
		t.Fatalf("save preset: %v", err)
	}
	if err := app.Secrets.ReplaceAll(map[string]string{"example": "sk-round-trip", "auxiliary": "secret-value"}); err != nil {
		t.Fatalf("save secrets: %v", err)
	}
	settingsSnapshot := *app.Settings.GetSettings()
	settingsSnapshot.GitHubToken = "github-round-trip"
	settingsSnapshot.Terminal.Scrollback = 43210
	if err := app.Settings.ReplaceSettings(settingsSnapshot); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if err := app.Paths.ReplaceConfig(paths.PathsConfig{Paths: []paths.PathEntry{{Path: "/tmp/project", Label: "Project"}}, DefaultPath: "/tmp/project"}); err != nil {
		t.Fatalf("save paths: %v", err)
	}
	if err := app.EnvVars.ReplacePortableConfig(envvars.PortableConfig{EnvVars: []envvars.EnvVar{{Key: "ROUND_TRIP", Value: "yes"}}}); err != nil {
		t.Fatalf("save envvars: %v", err)
	}
	if err := app.OpenCodeConfig.SaveOpenCodeConfig(`{"theme":"round-trip"}`); err != nil {
		t.Fatalf("save OpenCode config: %v", err)
	}

	exported, err := app.buildCompleteExportConfig()
	if err != nil {
		t.Fatalf("build export: %v", err)
	}
	portable, err := decodePortableConfig(exported.Portable)
	if err != nil {
		t.Fatalf("decode portable config: %v", err)
	}

	if err := app.Config.ReplaceProviders(map[string]config.Provider{}); err != nil {
		t.Fatal(err)
	}
	if err := app.Secrets.ReplaceAll(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	emptySettings := *app.Settings.GetSettings()
	emptySettings.GitHubToken = ""
	emptySettings.Terminal.Scrollback = 100000
	if err := app.Settings.ReplaceSettings(emptySettings); err != nil {
		t.Fatal(err)
	}
	if err := app.EnvVars.ReplacePortableConfig(envvars.PortableConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := app.OpenCodeConfig.SaveOpenCodeConfig(`{}`); err != nil {
		t.Fatal(err)
	}

	if err := app.applyCompleteConfig(exported, portable, true); err != nil {
		t.Fatalf("apply complete config: %v", err)
	}
	if _, err := app.Config.GetProvider("example"); err != nil {
		t.Fatalf("provider not restored: %v", err)
	}
	if key, _ := app.Secrets.GetAPIKey("example"); key != "sk-round-trip" {
		t.Fatalf("provider key = %q, want sk-round-trip", key)
	}
	if key, _ := app.Secrets.GetAPIKey("auxiliary"); key != "secret-value" {
		t.Fatalf("auxiliary secret = %q, want secret-value", key)
	}
	if got := app.Settings.GetSettings(); got.GitHubToken != "github-round-trip" || got.Terminal.Scrollback != 43210 {
		t.Fatalf("settings not restored: %+v", got)
	}
	if value, ok := app.EnvVars.Get("ROUND_TRIP"); !ok || value != "yes" {
		t.Fatalf("env var = (%q, %v), want (yes, true)", value, ok)
	}
	openCodeJSON, err := app.OpenCodeConfig.GetOpenCodeConfig()
	if err != nil {
		t.Fatal(err)
	}
	var openCode map[string]any
	if err := json.Unmarshal([]byte(openCodeJSON), &openCode); err != nil || openCode["theme"] != "round-trip" {
		t.Fatalf("OpenCode config not restored: %s (%v)", openCodeJSON, err)
	}
}

func TestCompleteConfigIgnoresRemovedLegacySections(t *testing.T) {
	app := newPortableConfigTestApp(t)
	exported, err := app.buildCompleteExportConfig()
	if err != nil {
		t.Fatalf("build export: %v", err)
	}

	var portable map[string]json.RawMessage
	if err := json.Unmarshal(exported.Portable, &portable); err != nil {
		t.Fatalf("decode portable JSON: %v", err)
	}
	for _, removed := range []string{"workspaces", "proxy"} {
		if _, exists := portable[removed]; exists {
			t.Fatalf("new export still contains removed section %q", removed)
		}
	}

	// json.Unmarshal deliberately ignores these legacy v2 fields. This keeps
	// previously exported complete configs importable without restoring either
	// removed feature or touching its old on-disk artifacts.
	portable["workspaces"] = json.RawMessage(`{"items":[],"global_enabled":[]}`)
	portable["proxy"] = json.RawMessage(`{"rules":[],"backend_url_history":[]}`)
	legacyPortable, err := json.Marshal(portable)
	if err != nil {
		t.Fatalf("marshal legacy portable JSON: %v", err)
	}
	if _, err := decodePortableConfig(legacyPortable); err != nil {
		t.Fatalf("legacy removed sections should be ignored: %v", err)
	}
}

func TestDecodeConfigExportRejectsV2WithoutPortableSnapshot(t *testing.T) {
	_, _, err := decodeConfigExport([]byte(`{"version":"2.0","source":"amagi-codebox","providers":{}}`))
	if err == nil {
		t.Fatal("v2 export without portable snapshot was accepted")
	}
}
