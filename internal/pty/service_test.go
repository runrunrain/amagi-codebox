package pty

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

func TestAttachSessionObserverSnapshotsHistoryAndRegistersCallbacksAtomically(t *testing.T) {
	logSvc := logging.NewService(t.TempDir())
	defer logSvc.Close()

	svc := NewService(logSvc)
	ps := &PtySession{
		outputHistory: []byte("history"),
		currentCols:   120,
		currentRows:   40,
	}
	svc.sessions["demo"] = ps

	var outputTriggered bool
	var resizeTriggered bool
	history, cols, rows, err := svc.AttachSessionObserver("demo", "observer-1", func(data []byte) {
		outputTriggered = string(data) == "live"
	}, func(cols, rows int) {
		resizeTriggered = cols == 80 && rows == 24
	})
	if err != nil {
		t.Fatalf("AttachSessionObserver: %v", err)
	}

	if string(history) != "history" {
		t.Fatalf("history = %q, want %q", string(history), "history")
	}
	if cols != 120 || rows != 40 {
		t.Fatalf("dimensions = %dx%d, want 120x40", cols, rows)
	}
	if svc.outputCBs["demo"] == nil || svc.outputCBs["demo"]["observer-1"] == nil {
		t.Fatal("expected output callback to be registered")
	}
	if svc.resizeCBs["demo"] == nil || svc.resizeCBs["demo"]["observer-1"] == nil {
		t.Fatal("expected resize callback to be registered")
	}

	svc.outputCBs["demo"]["observer-1"]([]byte("live"))
	svc.resizeCBs["demo"]["observer-1"](80, 24)
	if !outputTriggered {
		t.Fatal("expected registered output callback to receive live chunk")
	}
	if !resizeTriggered {
		t.Fatal("expected registered resize callback to receive live dimensions")
	}

	svc.DetachSessionObserver("demo", "observer-1")
	if _, ok := svc.outputCBs["demo"]["observer-1"]; ok {
		t.Fatal("expected output callback to be removed after detach")
	}
	if _, ok := svc.resizeCBs["demo"]["observer-1"]; ok {
		t.Fatal("expected resize callback to be removed after detach")
	}
}

func TestCallbackSnapshotsRemainSafeDuringConcurrentAttachDetachAndDispatch(t *testing.T) {
	logSvc := logging.NewService(t.TempDir())
	defer logSvc.Close()

	svc := NewService(logSvc)
	ps := &PtySession{}
	svc.sessions["demo"] = ps

	var outputCalls atomic.Int64
	var exitCalls atomic.Int64

	const observers = 8
	const iterations = 200

	var wg sync.WaitGroup
	for i := 0; i < observers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "observer-" + strconv.Itoa(i)
			outputCB := func(data []byte) {
				outputCalls.Add(1)
			}
			exitCB := func(exitCode uint32) {
				exitCalls.Add(1)
			}

			for j := 0; j < iterations; j++ {
				if _, _, _, err := svc.AttachSessionObserver("demo", id, outputCB, nil); err != nil {
					t.Errorf("AttachSessionObserver(%s): %v", id, err)
					return
				}
				svc.RegisterExitCallback("demo", id, exitCB)
				svc.DetachSessionObserver("demo", id)
				svc.UnregisterExitCallback("demo", id)
			}
		}(i)
	}

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < observers*iterations; j++ {
				for _, cb := range svc.snapshotOutputCallbacks("demo") {
					cb([]byte("chunk"))
				}
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < observers*iterations; j++ {
				for _, cb := range svc.snapshotExitCallbacks("demo") {
					cb(0)
				}
			}
		}()
	}

	wg.Wait()

	if outputCalls.Load() == 0 {
		t.Fatal("expected output callbacks to be invoked during concurrent dispatch")
	}
	if exitCalls.Load() == 0 {
		t.Fatal("expected exit callbacks to be invoked during concurrent dispatch")
	}
	if len(svc.outputCBs["demo"]) != 0 {
		t.Fatal("expected output callbacks to be fully detached")
	}
	if len(svc.exitCBs["demo"]) != 0 {
		t.Fatal("expected exit callbacks to be fully detached")
	}
}
