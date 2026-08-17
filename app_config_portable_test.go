package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/ompconfig"
	"amagi-codebox/internal/opencodeconfig"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/piconfig"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/usage"
)

func newPortableConfigTestApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// pi 与 omp 的 agentDir 都优先读 PI_CODING_AGENT_DIR；指向同一临时目录
	// 不会冲突（文件名互不重叠：models.json/auth.json/amagi.json vs
	// config.yml/models.yml）。
	piAgentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", piAgentDir)
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
		PiConfig:       piconfig.NewService(),
		OmpConfig:      ompconfig.NewService(),
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

func cliTestAgentDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("PI_CODING_AGENT_DIR")
	if dir == "" {
		t.Fatal("PI_CODING_AGENT_DIR not set by test app helper")
	}
	return dir
}

func writeCLITestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readCLITestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func seedCLIConfigFiles(t *testing.T, agentDir string) {
	t.Helper()
	writeCLITestFile(t, filepath.Join(agentDir, "models.json"),
		`{"providers":{"custom-pi":{"api":"openai","apiKey":"pi-sk-secret","models":[{"id":"pi-model"}]}}}`)
	writeCLITestFile(t, filepath.Join(agentDir, "auth.json"),
		`{"auth-provider":{"type":"api_key","api_key":"pi-auth-token"}}`)
	writeCLITestFile(t, filepath.Join(agentDir, "amagi.json"),
		`{"profile":"tiered","agents":{"coder":{"model":"pi-model"}}}`)
	writeCLITestFile(t, filepath.Join(agentDir, "config.yml"),
		"modelRoles:\n  default: custom-omp-model\n")
	writeCLITestFile(t, filepath.Join(agentDir, "models.yml"),
		"providers:\n  custom-omp:\n    api: openai\n    apiKey: omp-sk-secret\n")
}

func TestCompleteConfigRoundTripRestoresCLIConfigs(t *testing.T) {
	app := newPortableConfigTestApp(t)
	agentDir := cliTestAgentDir(t)
	seedCLIConfigFiles(t, agentDir)

	exported, err := app.buildCompleteExportConfig()
	if err != nil {
		t.Fatalf("build export: %v", err)
	}
	portable, err := decodePortableConfig(exported.Portable)
	if err != nil {
		t.Fatalf("decode portable config: %v", err)
	}
	if len(portable.PiModelsConfig) == 0 || len(portable.PiAuthConfig) == 0 || len(portable.PiAmagiConfig) == 0 {
		t.Fatalf("pi sections missing from export: models=%d auth=%d amagi=%d",
			len(portable.PiModelsConfig), len(portable.PiAuthConfig), len(portable.PiAmagiConfig))
	}
	if portable.OmpConfig == "" || portable.OmpModelsConfig == "" {
		t.Fatalf("omp sections missing from export: config=%q models=%q", portable.OmpConfig, portable.OmpModelsConfig)
	}
	if !strings.Contains(string(portable.PiAuthConfig), "pi-auth-token") {
		t.Fatalf("pi auth token not exported verbatim: %s", portable.PiAuthConfig)
	}

	// Simulate a different device: replace on-disk CLI configs, then restore
	// via the complete import path.
	writeCLITestFile(t, filepath.Join(agentDir, "models.json"), `{"providers":{}}`)
	writeCLITestFile(t, filepath.Join(agentDir, "config.yml"), "modelRoles: {}\n")

	if err := app.applyCompleteConfig(exported, portable, true); err != nil {
		t.Fatalf("apply complete config: %v", err)
	}

	// Save* re-formats content, so compare semantics rather than bytes.
	var piModels map[string]any
	if err := json.Unmarshal([]byte(readCLITestFile(t, filepath.Join(agentDir, "models.json"))), &piModels); err != nil {
		t.Fatal(err)
	}
	providers, _ := piModels["providers"].(map[string]any)
	if _, ok := providers["custom-pi"]; !ok {
		t.Fatalf("pi models config not restored: %v", piModels)
	}
	var piAuth map[string]any
	if err := json.Unmarshal([]byte(readCLITestFile(t, filepath.Join(agentDir, "auth.json"))), &piAuth); err != nil {
		t.Fatal(err)
	}
	if _, ok := piAuth["auth-provider"]; !ok {
		t.Fatalf("pi auth config not restored: %v", piAuth)
	}
	var piAmagi map[string]any
	if err := json.Unmarshal([]byte(readCLITestFile(t, filepath.Join(agentDir, "amagi.json"))), &piAmagi); err != nil {
		t.Fatal(err)
	}
	if piAmagi["profile"] != "tiered" {
		t.Fatalf("pi amagi config not restored: %v", piAmagi)
	}
	ompConfig := readCLITestFile(t, filepath.Join(agentDir, "config.yml"))
	if !strings.Contains(ompConfig, "custom-omp-model") {
		t.Fatalf("omp config not restored: %s", ompConfig)
	}
	ompModels := readCLITestFile(t, filepath.Join(agentDir, "models.yml"))
	if !strings.Contains(ompModels, "custom-omp") || !strings.Contains(ompModels, "omp-sk-secret") {
		t.Fatalf("omp models config not restored: %s", ompModels)
	}
}

func TestCompleteConfigExportSkipsMissingAndBrokenCLIConfigs(t *testing.T) {
	app := newPortableConfigTestApp(t)
	agentDir := cliTestAgentDir(t)

	// Fresh device: no CLI config files at all → no placeholder skeletons are
	// exported and no files are created by an import round-trip.
	exported, err := app.buildCompleteExportConfig()
	if err != nil {
		t.Fatalf("build export: %v", err)
	}
	portable, err := decodePortableConfig(exported.Portable)
	if err != nil {
		t.Fatalf("decode portable config: %v", err)
	}
	if len(portable.PiModelsConfig) > 0 || len(portable.PiAuthConfig) > 0 || len(portable.PiAmagiConfig) > 0 ||
		portable.OmpConfig != "" || portable.OmpModelsConfig != "" {
		t.Fatalf("empty agent dir produced CLI sections: %+v", portable)
	}
	if err := app.applyCompleteConfig(exported, portable, true); err != nil {
		t.Fatalf("apply complete config: %v", err)
	}
	for _, name := range []string{"models.json", "auth.json", "amagi.json", "config.yml", "models.yml"} {
		if _, err := os.Stat(filepath.Join(agentDir, name)); !os.IsNotExist(err) {
			t.Fatalf("import created skeleton %s on a device without CLI configs", name)
		}
	}

	// Broken sources: a directory where models.json belongs (read failure) and
	// malformed YAML in config.yml (shape failure). Only the broken fields are
	// skipped; export still succeeds with the healthy sections.
	if err := os.MkdirAll(filepath.Join(agentDir, "models.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(agentDir, "auth.json"), `{"auth-provider":{"type":"api_key","api_key":"k"}}`)
	writeCLITestFile(t, filepath.Join(agentDir, "config.yml"), ":::not yaml")
	writeCLITestFile(t, filepath.Join(agentDir, "models.yml"), "providers: {}\n")

	exported, err = app.buildCompleteExportConfig()
	if err != nil {
		t.Fatalf("build export with broken CLI configs: %v", err)
	}
	portable, err = decodePortableConfig(exported.Portable)
	if err != nil {
		t.Fatalf("decode portable config: %v", err)
	}
	if len(portable.PiModelsConfig) > 0 {
		t.Fatal("pi models section should be skipped for unreadable file")
	}
	if portable.OmpConfig != "" {
		t.Fatal("omp config section should be skipped for malformed YAML")
	}
	if len(portable.PiAuthConfig) == 0 || portable.OmpModelsConfig == "" {
		t.Fatal("healthy sections should still be exported")
	}
}

func TestCompleteConfigImportWithoutCLISectionsKeepsLocalFiles(t *testing.T) {
	app := newPortableConfigTestApp(t)
	agentDir := cliTestAgentDir(t)
	seedCLIConfigFiles(t, agentDir)

	exported, err := app.buildCompleteExportConfig()
	if err != nil {
		t.Fatalf("build export: %v", err)
	}
	// Simulate an old v2 export file: strip every CLI section from the
	// portable snapshot. Importing it must leave local CLI files untouched.
	var portableMap map[string]json.RawMessage
	if err := json.Unmarshal(exported.Portable, &portableMap); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"pi_models_config", "pi_auth_config", "pi_amagi_config", "omp_config", "omp_models_config"} {
		delete(portableMap, field)
	}
	legacyPortableJSON, err := json.Marshal(portableMap)
	if err != nil {
		t.Fatal(err)
	}
	legacyPortable, err := decodePortableConfig(legacyPortableJSON)
	if err != nil {
		t.Fatalf("legacy portable snapshot rejected: %v", err)
	}

	before := readCLITestFile(t, filepath.Join(agentDir, "models.json"))
	if err := app.applyCompleteConfig(exported, legacyPortable, true); err != nil {
		t.Fatalf("apply legacy complete config: %v", err)
	}
	if after := readCLITestFile(t, filepath.Join(agentDir, "models.json")); after != before {
		t.Fatalf("legacy import modified pi models config: %s", after)
	}
	if got := readCLITestFile(t, filepath.Join(agentDir, "config.yml")); !strings.Contains(got, "custom-omp-model") {
		t.Fatalf("legacy import modified omp config: %s", got)
	}
}

func TestCompleteConfigImportRejectsMalformedCLISections(t *testing.T) {
	app := newPortableConfigTestApp(t)
	agentDir := cliTestAgentDir(t)
	seedCLIConfigFiles(t, agentDir)

	exported, err := app.buildCompleteExportConfig()
	if err != nil {
		t.Fatalf("build export: %v", err)
	}
	portable, err := decodePortableConfig(exported.Portable)
	if err != nil {
		t.Fatal(err)
	}
	portable.PiModelsConfig = json.RawMessage(`[1,2]`)
	portable.OmpModelsConfig = "- a\n- list\n"

	before := readCLITestFile(t, filepath.Join(agentDir, "models.json"))
	err = app.applyCompleteConfig(exported, portable, true)
	if err == nil {
		t.Fatal("malformed CLI sections were accepted")
	}
	if !strings.Contains(err.Error(), "pi models config") || !strings.Contains(err.Error(), "omp models config") {
		t.Fatalf("error does not identify malformed sections: %v", err)
	}
	// Up-front validation must fail before any live write or rollback.
	if after := readCLITestFile(t, filepath.Join(agentDir, "models.json")); after != before {
		t.Fatalf("validation failure mutated pi models config: %s", after)
	}
}

func TestCompleteConfigImportCLIWriteFailureAggregates(t *testing.T) {
	app := newPortableConfigTestApp(t)
	agentDir := cliTestAgentDir(t)
	seedCLIConfigFiles(t, agentDir)

	exported, err := app.buildCompleteExportConfig()
	if err != nil {
		t.Fatalf("build export: %v", err)
	}
	portable, err := decodePortableConfig(exported.Portable)
	if err != nil {
		t.Fatal(err)
	}

	// Sabotage two targets so that both writes fail: a directory cannot be
	// replaced by writePrivateFile's rename/overwrite path.
	if err := os.RemoveAll(filepath.Join(agentDir, "models.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "models.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(agentDir, "config.yml")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "config.yml"), 0o755); err != nil {
		t.Fatal(err)
	}

	err = app.applyCompleteConfig(exported, portable, true)
	if err == nil {
		t.Fatal("import succeeded despite CLI write failures")
	}
	// Both failures must be aggregated in a single error (errors.Join), not
	// fail fast on the first one.
	if !strings.Contains(err.Error(), "replace pi models config") {
		t.Fatalf("error missing pi models failure: %v", err)
	}
	if !strings.Contains(err.Error(), "replace omp config") {
		t.Fatalf("error missing omp config failure: %v", err)
	}
	// Healthy sections that were written before the failure stay written only
	// if rollback skipped them (their pre-import content matches the snapshot
	// anyway); the important contract is the aggregated error and rollback of
	// codebox-owned state, verified by other tests.
}
