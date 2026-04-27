package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/session"
)

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

	secretsSvc := secrets.NewSecretsService(configDir)
	if err := secretsSvc.Load(); err != nil {
		t.Fatalf("load secrets: %v", err)
	}

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

func newASCIIPathTempDir(t *testing.T, pattern string) string {
	t.Helper()
	root := filepath.Join("X:/WorkSpace/amagi-codebox", ".tmp-tests")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir temp root: %v", err)
	}
	dir, err := os.MkdirTemp(root, pattern)
	if err != nil {
		t.Fatalf("mktemp under ascii root: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

// envDumpKey is the env var the fake codex script uses for its output file path.
const envDumpKey = "__AMAGI_TEST_ENV_DUMP_FILE"

// setupFakeCodex creates a temporary directory containing a fake "codex.cmd" batch
// script that writes selected environment variables to a dump file and exits
// immediately. It prepends this directory to the current process PATH so that
// exec.Command("codex") resolves to the fake.
//
// Returns:
//   - binDir:  the directory containing codex.cmd (for cleanup)
//   - dumpFile: the path where the fake will write KEY=VALUE lines
//   - origPATH: the original PATH value (to restore after test)
//
// The caller should defer a restore function.
func setupFakeCodex(t *testing.T) (binDir string, dumpFile string, origPATH string) {
	t.Helper()

	binDir = newASCIIPathTempDir(t, "fake-codex-bin-")
	dumpDir := newASCIIPathTempDir(t, "fake-codex-dump-")
	dumpFile = filepath.Join(dumpDir, "envdump.txt")

	// Build the batch script content.
	// We write a selection of env vars that we care about, one per line as KEY=VALUE.
	// Using delayed expansion so %CODEX_HOME% etc. are resolved at runtime.
	// We cannot use fmt.Sprintf here because batch %var% syntax conflicts with Go format verbs.
	envDumpRef := "%" + envDumpKey + "%"
	script := "@echo off\r\n" +
		"setlocal enabledelayedexpansion\r\n" +
		"> \"" + dumpFile + "\" (\r\n" +
		"  echo CODEX_HOME=!CODEX_HOME!\r\n" +
		"  echo OPENAI_API_KEY=!OPENAI_API_KEY!\r\n" +
		"  echo OPENAI_BASE_URL=!OPENAI_BASE_URL!\r\n" +
		"  echo ANTHROPIC_API_KEY=!ANTHROPIC_API_KEY!\r\n" +
		"  echo ANTHROPIC_BASE_URL=!ANTHROPIC_BASE_URL!\r\n" +
		"  echo " + envDumpKey + "=" + envDumpRef + "\r\n" +
		")\r\n" +
		"endlocal\r\n" +
		"exit /b 0\r\n"

	codexPath := filepath.Join(binDir, "codex.cmd")
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex.cmd: %v", err)
	}

	origPATH = os.Getenv("PATH")
	newPATH := binDir + string(os.PathListSeparator) + origPATH
	if err := os.Setenv("PATH", newPATH); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	// Also set the dump file path into the process env so the fake script can read it.
	if err := os.Setenv(envDumpKey, dumpFile); err != nil {
		t.Fatalf("set %s: %v", envDumpKey, err)
	}

	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPATH)
		_ = os.Unsetenv(envDumpKey)
	})

	return binDir, dumpFile, origPATH
}

func setupFakeClaude(t *testing.T) (binDir string, dumpFile string, origPATH string) {
	t.Helper()

	binDir = newASCIIPathTempDir(t, "fake-claude-bin-")
	dumpDir := newASCIIPathTempDir(t, "fake-claude-dump-")
	dumpFile = filepath.Join(dumpDir, "claude-envdump.txt")

	envDumpRef := "%" + envDumpKey + "%"
	script := "@echo off\r\n" +
		"setlocal enabledelayedexpansion\r\n" +
		"> \"" + dumpFile + "\" (\r\n" +
		"  echo ANTHROPIC_API_KEY=!ANTHROPIC_API_KEY!\r\n" +
		"  echo ANTHROPIC_BASE_URL=!ANTHROPIC_BASE_URL!\r\n" +
		"  echo ANTHROPIC_MODEL=!ANTHROPIC_MODEL!\r\n" +
		"  echo ANTHROPIC_AUTH_TOKEN=!ANTHROPIC_AUTH_TOKEN!\r\n" +
		"  echo " + envDumpKey + "=" + envDumpRef + "\r\n" +
		")\r\n" +
		"endlocal\r\n" +
		"exit /b 0\r\n"

	claudePath := filepath.Join(binDir, "claude.cmd")
	if err := os.WriteFile(claudePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude.cmd: %v", err)
	}

	origPATH = os.Getenv("PATH")
	newPATH := binDir + string(os.PathListSeparator) + origPATH
	if err := os.Setenv("PATH", newPATH); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	if err := os.Setenv(envDumpKey, dumpFile); err != nil {
		t.Fatalf("set %s: %v", envDumpKey, err)
	}

	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPATH)
		_ = os.Unsetenv(envDumpKey)
	})

	return binDir, dumpFile, origPATH
}

// waitForDumpFile polls for dumpFile to exist and have non-zero content,
// with a short timeout.
func waitForDumpFile(t *testing.T, dumpFile string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fi, err := os.Stat(dumpFile)
		if err == nil && fi.Size() > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for env dump file %s", dumpFile)
}

// parseEnvDump reads the dump file and returns a map of key->value.
// Lines are in KEY=VALUE format. Missing keys (empty lines) are omitted.
func parseEnvDump(t *testing.T, dumpFile string) map[string]string {
	t.Helper()
	f, err := os.Open(dumpFile)
	if err != nil {
		t.Fatalf("open dump file: %v", err)
	}
	defer f.Close()

	result := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		result[k] = v
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan dump file: %v", err)
	}
	return result
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
// A. Terminal mode -- real LaunchCodexSession via fake codex.cmd
// ============================================================================

// TestLaunchCodexSession_Terminal_NoProvider_PreservesCODEXHOME verifies that when
// no provider is set and the user's environment already has CODEX_HOME=<orig>,
// LaunchCodexSession does NOT overwrite or inject a different CODEX_HOME.
// The fake codex.cmd captures the actual environment and writes it to a file.
func TestLaunchCodexSession_Terminal_NoProvider_PreservesCODEXHOME(t *testing.T) {
	_, dumpFile, _ := setupFakeCodex(t)

	// Set a pre-existing CODEX_HOME in the process environment.
	const origCodexHome = `C:\Users\test\original-codex-home`
	if err := os.Setenv("CODEX_HOME", origCodexHome); err != nil {
		t.Fatalf("set CODEX_HOME: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("CODEX_HOME") })

	app := newTestApp(t)
	app.ctx = t.Context()

	// No provider registered. Call the real LaunchCodexSession in terminal mode.
	sessionID, err := app.LaunchCodexSession(
		"gpt-5",                                  // modelName
		"",                                       // providerID -- none
		"terminal",                               // mode
		newASCIIPathTempDir(t, "codex-workdir-"), // workDir
		"",                                       // shellPath -- unused in terminal mode
	)
	if err != nil {
		t.Fatalf("LaunchCodexSession failed: %v", err)
	}
	t.Logf("session created: %s", sessionID)

	// Wait for the fake codex.cmd to write the dump file.
	waitForDumpFile(t, dumpFile, 10*time.Second)

	env := parseEnvDump(t, dumpFile)

	// CODEX_HOME must be the original value, not rewritten.
	if env["CODEX_HOME"] != origCodexHome {
		t.Fatalf("CODEX_HOME = %q, want %q (original value preserved)", env["CODEX_HOME"], origCodexHome)
	}

	// No API keys should be injected via envOverrides since no provider was specified.
	// However, keys that already exist in the system environment are inherited unchanged.
	// We verify that LaunchCodexSession did not ADD any new API key values via overrides.
	// The test only asserts that the launch path does not synthesize new values.
	// (We do not assert "ANTHROPIC_API_KEY == empty" because the developer's system
	// environment may have pre-existing keys that pass through via MergeWithSystem.)
	if env["OPENAI_API_KEY"] == "sk-test-openai-key-123" {
		t.Fatal("OPENAI_API_KEY should not be injected by the test override (no provider set)")
	}

	// Clean up session.
	app.StopSession(sessionID)
}

// TestLaunchCodexSession_Terminal_OpenAI_InjectsEnvVars verifies the full launch
// chain with an OpenAI provider: LaunchCodexSession -> Launcher.LaunchCodex ->
// exec.Command("codex") with the correct env overrides reaching the child process.
func TestLaunchCodexSession_Terminal_OpenAI_InjectsEnvVars(t *testing.T) {
	_, dumpFile, _ := setupFakeCodex(t)

	// Set a pre-existing CODEX_HOME.
	const origCodexHome = `C:\Users\test\my-codex-home`
	if err := os.Setenv("CODEX_HOME", origCodexHome); err != nil {
		t.Fatalf("set CODEX_HOME: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("CODEX_HOME") })

	app := newTestApp(t)
	app.ctx = t.Context()

	// Register an OpenAI provider.
	const providerID = "test-openai"
	if err := app.Config.SaveProvider(providerID, config.Provider{
		Type:         "openai",
		BaseURL:      "https://api.test.example.com/v1",
		AuthKey:      "OPENAI_API_KEY",
		DefaultModel: "gpt-5",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey(providerID, "sk-test-openai-key-123"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	sessionID, err := app.LaunchCodexSession(
		"gpt-5",                                  // modelName
		providerID,                               // providerID
		"terminal",                               // mode
		newASCIIPathTempDir(t, "codex-workdir-"), // workDir
		"",                                       // shellPath
	)
	if err != nil {
		t.Fatalf("LaunchCodexSession failed: %v", err)
	}
	t.Logf("session created: %s", sessionID)

	waitForDumpFile(t, dumpFile, 10*time.Second)
	env := parseEnvDump(t, dumpFile)

	// OpenAI API key must be present.
	if env["OPENAI_API_KEY"] != "sk-test-openai-key-123" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", env["OPENAI_API_KEY"], "sk-test-openai-key-123")
	}
	// OpenAI base URL must be injected.
	if env["OPENAI_BASE_URL"] != "https://api.test.example.com/v1" {
		t.Fatalf("OPENAI_BASE_URL = %q, want %q", env["OPENAI_BASE_URL"], "https://api.test.example.com/v1")
	}
	// Anthropic key must NOT be injected by our overrides.
	// Note: if the system environment already has ANTHROPIC_API_KEY, it passes through
	// via MergeWithSystem. We only assert our overrides did not set it.
	if env["ANTHROPIC_API_KEY"] == "sk-ant-test-key-456" {
		t.Fatal("ANTHROPIC_API_KEY should not be set to the Anthropic test value by OpenAI overrides")
	}
	// CODEX_HOME must preserve the original value.
	if env["CODEX_HOME"] != origCodexHome {
		t.Fatalf("CODEX_HOME = %q, want %q (original preserved)", env["CODEX_HOME"], origCodexHome)
	}

	app.StopSession(sessionID)
}

// TestLaunchCodexSession_Terminal_Anthropic_InjectsEnvVars verifies the full launch
// chain with an Anthropic provider.
func TestLaunchCodexSession_Terminal_Anthropic_InjectsEnvVars(t *testing.T) {
	_, dumpFile, _ := setupFakeCodex(t)

	const origCodexHome = `C:\Users\test\anthropic-codex-home`
	if err := os.Setenv("CODEX_HOME", origCodexHome); err != nil {
		t.Fatalf("set CODEX_HOME: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("CODEX_HOME") })

	app := newTestApp(t)
	app.ctx = t.Context()

	const providerID = "test-anthropic"
	if err := app.Config.SaveProvider(providerID, config.Provider{
		Type:         "anthropic",
		BaseURL:      "https://api.anthropic.com",
		AuthKey:      "ANTHROPIC_API_KEY",
		DefaultModel: "claude-sonnet-4-20250514",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey(providerID, "sk-ant-test-key-456"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	sessionID, err := app.LaunchCodexSession(
		"claude-sonnet-4-20250514",
		providerID,
		"terminal",
		newASCIIPathTempDir(t, "codex-workdir-"),
		"",
	)
	if err != nil {
		t.Fatalf("LaunchCodexSession failed: %v", err)
	}

	waitForDumpFile(t, dumpFile, 10*time.Second)
	env := parseEnvDump(t, dumpFile)

	if env["ANTHROPIC_API_KEY"] != "sk-ant-test-key-456" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want %q", env["ANTHROPIC_API_KEY"], "sk-ant-test-key-456")
	}
	if env["ANTHROPIC_BASE_URL"] != "https://api.anthropic.com" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want %q", env["ANTHROPIC_BASE_URL"], "https://api.anthropic.com")
	}
	// OPENAI_API_KEY should not be injected by our overrides. It may be inherited
	// from the system environment via MergeWithSystem, so we only check our override value.
	if env["OPENAI_API_KEY"] == "sk-test-openai-key-123" {
		t.Fatal("OPENAI_API_KEY should not be set to the OpenAI test value by Anthropic overrides")
	}
	// CODEX_HOME preserved.
	if env["CODEX_HOME"] != origCodexHome {
		t.Fatalf("CODEX_HOME = %q, want %q (original preserved)", env["CODEX_HOME"], origCodexHome)
	}

	app.StopSession(sessionID)
}

// TestLaunchCodexSession_Terminal_NoProvider_NoPreExistingCODEXHOME verifies that
// when no provider is set and there is no pre-existing CODEX_HOME in the environment,
// LaunchCodexSession does not inject one. This is the "no CODEX_HOME at all" case.
func TestLaunchCodexSession_Terminal_NoProvider_NoPreExistingCODEXHOME(t *testing.T) {
	_, dumpFile, _ := setupFakeCodex(t)

	// Ensure CODEX_HOME is not set.
	_ = os.Unsetenv("CODEX_HOME")

	app := newTestApp(t)
	app.ctx = t.Context()

	sessionID, err := app.LaunchCodexSession(
		"gpt-5",
		"",
		"terminal",
		newASCIIPathTempDir(t, "codex-workdir-"),
		"",
	)
	if err != nil {
		t.Fatalf("LaunchCodexSession failed: %v", err)
	}

	waitForDumpFile(t, dumpFile, 10*time.Second)
	env := parseEnvDump(t, dumpFile)

	// CODEX_HOME should remain empty -- not injected.
	if env["CODEX_HOME"] != "" {
		t.Fatalf("CODEX_HOME should be empty when not pre-set and no provider, got %q", env["CODEX_HOME"])
	}

	app.StopSession(sessionID)
}

// ============================================================================
// B. Unit-level helpers (unchanged behavior, keep for fast feedback)
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
	// Simulate the overrides map that LaunchCodexSession builds for OpenAI.
	overrides := map[string]string{
		"OPENAI_API_KEY":  "sk-key",
		"OPENAI_BASE_URL": "https://api.test.example.com/v1",
	}

	result := launcher.BuildEnv(base, overrides)

	if envHasKeySet(result, "CODEX_HOME") {
		t.Fatal("BuildEnv should not contain CODEX_HOME when overrides don't set it and base doesn't have it")
	}
}

// --- Verify fake codex.cmd is discoverable via PATH ---

func TestFakeCodexIsDiscoverable(t *testing.T) {
	binDir, _, _ := setupFakeCodex(t)

	// exec.LookPath should find our codex.cmd.
	path, err := exec.LookPath("codex")
	if err != nil {
		t.Fatalf("LookPath(codex) failed: %v", err)
	}
	// On Windows, LookPath may return the full path or relative path.
	// Verify it's in our binDir.
	if !strings.EqualFold(filepath.Dir(path), binDir) {
		t.Fatalf("found codex at %q, expected in %q", path, binDir)
	}
}

func TestLaunchSession_Terminal_DualFormatProvider_UsesUnifiedProviderKey(t *testing.T) {
	_, dumpFile, _ := setupFakeClaude(t)

	app := newTestApp(t)
	app.ctx = t.Context()

	const providerID = "dual-provider"
	if err := app.Config.SaveProvider(providerID, config.Provider{
		Anthropic: &config.AnthropicFormat{
			Enabled: true,
			BaseURL: "https://anthropic.example.com",
			AuthKey: config.AuthTypeAPIKey,
		},
		OpenAI: &config.OpenAIFormat{
			Enabled: true,
			BaseURL: "https://openai.example.com/v1",
			AuthKey: "OPENAI_API_KEY",
		},
		DefaultModel: "claude-sonnet-4-5",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey(providerID, "sk-provider-level-claude"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	sessionID, err := app.LaunchSession(providerID, "", "terminal", newASCIIPathTempDir(t, "claude-workdir-"), false, "")
	if err != nil {
		t.Fatalf("LaunchSession failed: %v", err)
	}

	waitForDumpFile(t, dumpFile, 10*time.Second)
	env := parseEnvDump(t, dumpFile)
	if env["ANTHROPIC_API_KEY"] != "sk-provider-level-claude" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want sk-provider-level-claude", env["ANTHROPIC_API_KEY"])
	}
	if env["ANTHROPIC_BASE_URL"] != "https://anthropic.example.com" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want https://anthropic.example.com", env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_MODEL"] != "claude-sonnet-4-5" {
		t.Fatalf("ANTHROPIC_MODEL = %q, want claude-sonnet-4-5", env["ANTHROPIC_MODEL"])
	}

	app.StopSession(sessionID)
}

func TestLaunchCodexSession_Terminal_DualFormatProvider_UsesUnifiedProviderKey(t *testing.T) {
	_, dumpFile, _ := setupFakeCodex(t)

	app := newTestApp(t)
	app.ctx = t.Context()

	const providerID = "dual-provider"
	if err := app.Config.SaveProvider(providerID, config.Provider{
		Anthropic: &config.AnthropicFormat{
			Enabled: true,
			BaseURL: "https://anthropic.example.com",
			AuthKey: config.AuthTypeAPIKey,
		},
		OpenAI: &config.OpenAIFormat{
			Enabled: true,
			BaseURL: "https://openai.example.com/v1",
			AuthKey: "OPENAI_API_KEY",
		},
		DefaultModel: "gpt-5",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey(providerID, "sk-provider-level-codex"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	sessionID, err := app.LaunchCodexSession("gpt-5", providerID, "terminal", newASCIIPathTempDir(t, "codex-workdir-"), "")
	if err != nil {
		t.Fatalf("LaunchCodexSession failed: %v", err)
	}

	waitForDumpFile(t, dumpFile, 10*time.Second)
	env := parseEnvDump(t, dumpFile)
	if env["OPENAI_API_KEY"] != "sk-provider-level-codex" {
		t.Fatalf("OPENAI_API_KEY = %q, want sk-provider-level-codex", env["OPENAI_API_KEY"])
	}
	if env["OPENAI_BASE_URL"] != "https://openai.example.com/v1" {
		t.Fatalf("OPENAI_BASE_URL = %q, want https://openai.example.com/v1", env["OPENAI_BASE_URL"])
	}

	app.StopSession(sessionID)
}

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
// C. Startup warnings mechanism
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
