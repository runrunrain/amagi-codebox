package pty

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
)

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

func setupFakeOpenCode(t *testing.T) (dumpFile string) {
	t.Helper()

	binDir := newASCIIPathTempDir(t, "fake-opencode-bin-")
	dumpDir := newASCIIPathTempDir(t, "fake-opencode-dump-")
	dumpFile = filepath.Join(dumpDir, "opencode-envdump.txt")

	script := "@echo off\r\n" +
		"setlocal enabledelayedexpansion\r\n" +
		"> \"" + dumpFile + "\" (\r\n" +
		"  echo ZHIPU_API_KEY=!ZHIPU_API_KEY!\r\n" +
		"  echo MINIMAX_API_KEY=!MINIMAX_API_KEY!\r\n" +
		"  echo OPENCODE_CONFIG_CONTENT=!OPENCODE_CONFIG_CONTENT!\r\n" +
		")\r\n" +
		"endlocal\r\n" +
		"exit /b 0\r\n"

	opencodePath := filepath.Join(binDir, "opencode.cmd")
	if err := os.WriteFile(opencodePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake opencode.cmd: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dumpFile
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

func TestStart_CmdShellLaunchesOpenCodeWithEmbeddedEnv(t *testing.T) {
	dumpFile := setupFakeOpenCode(t)
	t.Setenv("ZHIPU_API_KEY", "zhipu-test-key")
	t.Setenv("MINIMAX_API_KEY", "minimax-test-key")
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"provider":{"id":"demo"}}`)

	logSvc := logging.NewService(t.TempDir())
	defer logSvc.Close()

	svc := NewService(logSvc)
	workDir := newASCIIPathTempDir(t, "pty-opencode-workdir-")

	_, err := svc.Start("opencode-embedded-env", "cmd.exe", "opencode", workDir, os.Environ(), 120, 40)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = svc.Close("opencode-embedded-env") }()

	waitForDumpFile(t, dumpFile, 10*time.Second)
	env := parseEnvDump(t, dumpFile)

	if env["ZHIPU_API_KEY"] != "zhipu-test-key" {
		t.Fatalf("ZHIPU_API_KEY = %q, want %q", env["ZHIPU_API_KEY"], "zhipu-test-key")
	}
	if env["MINIMAX_API_KEY"] != "minimax-test-key" {
		t.Fatalf("MINIMAX_API_KEY = %q, want %q", env["MINIMAX_API_KEY"], "minimax-test-key")
	}
	if env["OPENCODE_CONFIG_CONTENT"] != `{"provider":{"id":"demo"}}` {
		t.Fatalf("OPENCODE_CONFIG_CONTENT = %q, want injected config content", env["OPENCODE_CONFIG_CONTENT"])
	}
	if !svc.IsRunning("opencode-embedded-env") {
		t.Fatal("expected shell session to remain running after startup command")
	}
}

func TestBuildStartupCommandLine_UsesInlineShellExecution(t *testing.T) {
	cmdLine, sendAuto := buildStartupCommandLine("cmd.exe", "opencode")
	if sendAuto != "" {
		t.Fatal("cmd.exe should launch startup command inline without delayed autoCommand")
	}
	if !strings.Contains(cmdLine, `/K "chcp 65001 >nul && opencode"`) {
		t.Fatalf("unexpected cmd startup command line: %q", cmdLine)
	}

	pwshLine, pwshSendAuto := buildStartupCommandLine("pwsh", "opencode")
	if pwshSendAuto != "" {
		t.Fatal("pwsh should launch startup command inline without delayed autoCommand")
	}
	if !strings.Contains(pwshLine, `-NoExit -Command "opencode"`) {
		t.Fatalf("unexpected pwsh startup command line: %q", pwshLine)
	}
}

func TestResolveStartupPlan_DirectLaunchNeverSchedulesDelayedCommand(t *testing.T) {
	commandLine, sendAuto := resolveStartupPlan("", "opencode")
	if commandLine != "opencode" {
		t.Fatalf("commandLine = %q, want %q", commandLine, "opencode")
	}
	if sendAuto != "" {
		t.Fatalf("sendAuto = %q, want empty for direct launch", sendAuto)
	}

	defaultCommand, defaultSendAuto := resolveStartupPlan("", "")
	if defaultCommand != "claude" {
		t.Fatalf("default commandLine = %q, want %q", defaultCommand, "claude")
	}
	if defaultSendAuto != "" {
		t.Fatalf("default sendAuto = %q, want empty for direct launch", defaultSendAuto)
	}
}

func TestResolveStartupPlan_ShellLaunchRetainsAutoCommandForShellProcessing(t *testing.T) {
	commandLine, sendAuto := resolveStartupPlan("cmd.exe", "opencode")
	if commandLine != "cmd.exe" {
		t.Fatalf("commandLine = %q, want %q", commandLine, "cmd.exe")
	}
	if sendAuto != "opencode" {
		t.Fatalf("sendAuto = %q, want %q", sendAuto, "opencode")
	}
}
