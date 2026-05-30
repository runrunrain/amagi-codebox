package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/plugin"
	"amagi-codebox/internal/proxy"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/workspace"
)

type failingSecretStore struct {
	saveCalled bool
}

func (s *failingSecretStore) Load(path string) (map[string]string, error) {
	_ = path
	return nil, errors.New("decrypt failed")
}

func (s *failingSecretStore) Save(path string, values map[string]string) error {
	_ = path
	_ = values
	s.saveCalled = true
	return nil
}

func (s *failingSecretStore) Kind() string { return "failing" }

func (s *failingSecretStore) LegacyImportPath(path string) string { return path }

func TestSaveAllConfigSkipsFailedStartupLoads(t *testing.T) {
	configDir := t.TempDir()
	logSvc := logging.NewService(configDir)
	t.Cleanup(logSvc.Close)

	writeFile(t, filepath.Join(configDir, "settings.json"), `{"dashboard":`)
	writeFile(t, filepath.Join(configDir, "paths.json"), `{"paths":`)
	writeFile(t, filepath.Join(configDir, "workspaces.json"), `{"workspaces":`)
	writeFile(t, filepath.Join(configDir, "global-enabled.json"), `{"entries":[{"pluginId":"keep-me"}]}`)
	writeFile(t, filepath.Join(configDir, "injection-rules.json"), `[{"id":`)
	writeFile(t, filepath.Join(configDir, "proxy-backend-url-history.json"), `["https://keep.example",`)

	cfgSvc := config.NewConfigService(configDir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatalf("load valid config: %v", err)
	}

	settingsSvc := settings.NewService(configDir)
	if err := settingsSvc.Load(); err == nil {
		t.Fatal("expected settings load to fail")
	}

	pathsSvc := paths.NewPathsService(configDir)
	if err := pathsSvc.Load(); err == nil {
		t.Fatal("expected paths load to fail")
	}

	secretStore := &failingSecretStore{}
	secretsSvc := secrets.NewSecretsServiceWithStore(configDir, secretStore)
	if err := secretsSvc.Load(); err == nil {
		t.Fatal("expected secrets load to fail")
	}

	pluginSvc := plugin.NewService("", logSvc)
	workspaceSvc := workspace.NewService(configDir, pluginSvc, logSvc)
	if err := workspaceSvc.Load(); err == nil {
		t.Fatal("expected workspace load to fail")
	}

	proxySvc := proxy.NewProxyService()
	if err := proxySvc.LoadRules(configDir); err == nil {
		t.Fatal("expected proxy rules load to fail")
	}
	if err := proxySvc.LoadBackendURLHistory(configDir); err == nil {
		t.Fatal("expected proxy history load to fail")
	}

	envVarsSvc := envvars.NewEnvVarsService(configDir)
	if err := envVarsSvc.Load(); err != nil {
		t.Fatalf("load env vars: %v", err)
	}

	app := &App{
		Config:     cfgSvc,
		Secrets:    secretsSvc,
		Paths:      pathsSvc,
		Settings:   settingsSvc,
		Workspaces: workspaceSvc,
		Proxy:      proxySvc,
		Log:        logSvc,
		Sessions:   session.NewManager(),
		Launcher:   launcher.NewLauncherService(logSvc, envVarsSvc),
		Pty:        pty.NewService(logSvc),
		EnvVars:    envVarsSvc,
	}
	app.setPersistentLoadState(persistentLoadState{
		initialized:        true,
		configLoaded:       true,
		secretsLoaded:      false,
		pathsLoaded:        false,
		settingsLoaded:     false,
		workspacesLoaded:   false,
		proxyRulesLoaded:   false,
		proxyHistoryLoaded: false,
	})

	err := app.SaveAllConfig()
	if err == nil {
		t.Fatal("expected skipped saves to be reported")
	}
	for _, want := range []string{
		"secrets.enc",
		"paths.json",
		"settings.json",
		"workspaces.json/global-enabled.json",
		"injection-rules.json",
		"proxy-backend-url-history.json",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("SaveAllConfig error should mention %q, got %v", want, err)
		}
	}
	if secretStore.saveCalled {
		t.Fatal("secrets save should be skipped after startup load failure")
	}

	assertFileContent(t, filepath.Join(configDir, "settings.json"), `{"dashboard":`)
	assertFileContent(t, filepath.Join(configDir, "paths.json"), `{"paths":`)
	assertFileContent(t, filepath.Join(configDir, "workspaces.json"), `{"workspaces":`)
	assertFileContent(t, filepath.Join(configDir, "global-enabled.json"), `{"entries":[{"pluginId":"keep-me"}]}`)
	assertFileContent(t, filepath.Join(configDir, "injection-rules.json"), `[{"id":`)
	assertFileContent(t, filepath.Join(configDir, "proxy-backend-url-history.json"), `["https://keep.example",`)
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s content changed\ngot:  %q\nwant: %q", path, string(got), want)
	}
}
