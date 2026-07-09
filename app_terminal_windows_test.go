//go:build windows

package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/config"
)

// newASCIIPathTempDir creates a temporary directory with an ASCII-only path.
// On Windows, t.TempDir() may return a path with non-ASCII characters
// depending on the user profile directory. This function uses the Windows
// short path (8.3 name) via `fsutil file setshortname` when needed,
// falling back to os.TempDir() which is typically C:\Users\...\AppData\Local\Temp.
func newASCIIPathTempDir(t *testing.T, pattern string) string {
	t.Helper()
	// Try os.TempDir() first - it's typically ASCII-safe on Windows
	base := os.TempDir()
	dir, err := os.MkdirTemp(base, pattern)
	if err != nil {
		t.Fatalf("mktemp in %s: %v", base, err)
	}
	// Verify the path is ASCII-safe (no multi-byte characters)
	for _, c := range dir {
		if c > 127 {
			// Path contains non-ASCII characters, skip the test
			_ = os.RemoveAll(dir)
			t.Skipf("temp dir %q contains non-ASCII characters, cannot reliably test batch scripts with this path", dir)
		}
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
func setupFakeCodex(t *testing.T) (binDir string, dumpFile string, origPATH string) {
	t.Helper()
	setupTestCodexHome(t)

	binDir = newASCIIPathTempDir(t, "fake-codex-bin-")
	dumpDir := newASCIIPathTempDir(t, "fake-codex-dump-")
	dumpFile = filepath.Join(dumpDir, "envdump.txt")

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
	if err := os.Setenv(envDumpKey, dumpFile); err != nil {
		t.Fatalf("set %s: %v", envDumpKey, err)
	}

	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPATH)
		_ = os.Unsetenv(envDumpKey)
	})

	return binDir, dumpFile, origPATH
}

func setupTestCodexHome(t *testing.T) string {
	t.Helper()
	home := newASCIIPathTempDir(t, "fake-codex-home-")
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir test codex home: %v", err)
	}
	configPath := filepath.Join(codexDir, "config.toml")
	content := "model = \"codex-mini-latest\"\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write test codex config: %v", err)
	}
	setTestUserHome(t, home)
	return home
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

// ============================================================================
// Terminal mode tests via fake codex.cmd (Windows only)
// ============================================================================

func TestLaunchCodexSession_Terminal_NoProvider_PreservesCODEXHOME(t *testing.T) {
	_, dumpFile, _ := setupFakeCodex(t)

	const origCodexHome = `C:\Users\test\original-codex-home`
	if err := os.Setenv("CODEX_HOME", origCodexHome); err != nil {
		t.Fatalf("set CODEX_HOME: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("CODEX_HOME") })

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
	t.Logf("session created: %s", sessionID)

	waitForDumpFile(t, dumpFile, 10*time.Second)
	env := parseEnvDump(t, dumpFile)

	if env["CODEX_HOME"] != origCodexHome {
		t.Fatalf("CODEX_HOME = %q, want %q (original value preserved)", env["CODEX_HOME"], origCodexHome)
	}
	if env["OPENAI_API_KEY"] == "sk-test-openai-key-123" {
		t.Fatal("OPENAI_API_KEY should not be injected by the test override (no provider set)")
	}

	app.StopSession(sessionID)
}

func TestLaunchCodexSession_Terminal_OpenAI_InjectsEnvVars(t *testing.T) {
	_, dumpFile, _ := setupFakeCodex(t)

	const origCodexHome = `C:\Users\test\my-codex-home`
	if err := os.Setenv("CODEX_HOME", origCodexHome); err != nil {
		t.Fatalf("set CODEX_HOME: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("CODEX_HOME") })

	app := newTestApp(t)
	app.ctx = t.Context()

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
		"gpt-5",
		providerID,
		"terminal",
		newASCIIPathTempDir(t, "codex-workdir-"),
		"",
	)
	if err != nil {
		t.Fatalf("LaunchCodexSession failed: %v", err)
	}
	t.Logf("session created: %s", sessionID)

	waitForDumpFile(t, dumpFile, 10*time.Second)
	env := parseEnvDump(t, dumpFile)

	if env["OPENAI_API_KEY"] != "sk-test-openai-key-123" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", env["OPENAI_API_KEY"], "sk-test-openai-key-123")
	}
	if env["OPENAI_BASE_URL"] != "https://api.test.example.com/v1" {
		t.Fatalf("OPENAI_BASE_URL = %q, want %q", env["OPENAI_BASE_URL"], "https://api.test.example.com/v1")
	}
	if env["ANTHROPIC_API_KEY"] == "sk-ant-test-key-456" {
		t.Fatal("ANTHROPIC_API_KEY should not be set to the Anthropic test value by OpenAI overrides")
	}
	if env["CODEX_HOME"] != origCodexHome {
		t.Fatalf("CODEX_HOME = %q, want %q (original preserved)", env["CODEX_HOME"], origCodexHome)
	}

	app.StopSession(sessionID)
}

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
	if env["OPENAI_API_KEY"] == "sk-test-openai-key-123" {
		t.Fatal("OPENAI_API_KEY should not be set to the OpenAI test value by Anthropic overrides")
	}
	if env["CODEX_HOME"] != origCodexHome {
		t.Fatalf("CODEX_HOME = %q, want %q (original preserved)", env["CODEX_HOME"], origCodexHome)
	}

	app.StopSession(sessionID)
}

func TestLaunchCodexSession_Terminal_NoProvider_NoPreExistingCODEXHOME(t *testing.T) {
	_, dumpFile, _ := setupFakeCodex(t)

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

	if env["CODEX_HOME"] != "" {
		t.Fatalf("CODEX_HOME should be empty when not pre-set and no provider, got %q", env["CODEX_HOME"])
	}

	app.StopSession(sessionID)
}

func TestFakeCodexIsDiscoverable(t *testing.T) {
	binDir, _, _ := setupFakeCodex(t)

	path, err := exec.LookPath("codex")
	if err != nil {
		t.Fatalf("LookPath(codex) failed: %v", err)
	}
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

	sessionID, err := app.LaunchSession(providerID, "", "terminal", newASCIIPathTempDir(t, "claude-workdir-"), false, false, "")
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
