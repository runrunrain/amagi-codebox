package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// App-layer mock for platform.ProcessRunner (envcheck integration)
// ---------------------------------------------------------------------------

// appEnvCheckRunner is a test-double for platform.ProcessRunner used in
// App-layer envcheck integration tests. It returns appropriate responses
// based on the command path to simulate a fully healthy environment.
type appEnvCheckRunner struct {
	calls []platform.CommandSpec
}

func (r *appEnvCheckRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.calls = append(r.calls, spec)
	path := strings.ToLower(spec.Path)

	// Version check commands
	if strings.Contains(path, "claude") {
		return &platform.ProcessResult{Stdout: "Claude Code v1.0.0"}, nil
	}
	if strings.Contains(path, "opencode") {
		return &platform.ProcessResult{Stdout: "opencode v1.0.0"}, nil
	}
	if strings.Contains(path, "codex") {
		return &platform.ProcessResult{Stdout: "codex-cli 1.0.0"}, nil
	}
	if strings.Contains(path, "pi") {
		return &platform.ProcessResult{Stdout: "0.81.1"}, nil
	}
	if strings.Contains(path, "headroom") {
		return &platform.ProcessResult{Stdout: "headroom 1.0.0"}, nil
	}

	// npm commands (version check, package view)
	if strings.Contains(path, "npm") {
		if len(spec.Args) > 0 && spec.Args[0] == "view" {
			return &platform.ProcessResult{Stdout: "1.0.0"}, nil
		}
		return &platform.ProcessResult{Stdout: "10.0.0"}, nil
	}

	return &platform.ProcessResult{}, nil
}

func (r *appEnvCheckRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// appEnvCheckRunnerWithFailure simulates an environment where one tool
// is broken (claude) and the others are healthy.
type appEnvCheckRunnerWithFailure struct {
	calls []platform.CommandSpec
}

func (r *appEnvCheckRunnerWithFailure) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.calls = append(r.calls, spec)
	path := strings.ToLower(spec.Path)

	// Claude version check fails
	if strings.Contains(path, "claude") {
		return &platform.ProcessResult{Stdout: ""}, exec.ErrNotFound
	}
	if strings.Contains(path, "opencode") {
		return &platform.ProcessResult{Stdout: "opencode v1.0.0"}, nil
	}
	if strings.Contains(path, "codex") {
		return &platform.ProcessResult{Stdout: "codex-cli 1.0.0"}, nil
	}
	if strings.Contains(path, "pi") {
		return &platform.ProcessResult{Stdout: "0.81.1"}, nil
	}
	if strings.Contains(path, "headroom") {
		return &platform.ProcessResult{Stdout: "headroom 1.0.0"}, nil
	}
	if strings.Contains(path, "npm") {
		if len(spec.Args) > 0 && spec.Args[0] == "view" {
			return &platform.ProcessResult{Stdout: "1.0.0"}, nil
		}
		return &platform.ProcessResult{Stdout: "10.0.0"}, nil
	}

	return &platform.ProcessResult{}, nil
}

func (r *appEnvCheckRunnerWithFailure) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// newTestAppWithEnvCheck creates a minimal App with an EnvCheck service
// backed by the given ProcessRunner. Temp executables are created so
// exec.LookPath finds the tools.
func newTestAppWithEnvCheck(t *testing.T, runner platform.ProcessRunner) *App {
	t.Helper()

	// Create temp executables so exec.LookPath finds them
	tmpDir := t.TempDir()
	for _, name := range []string{"claude", "opencode", "codex", "pi", "headroom"} {
		ext := ""
		content := "#!/bin/sh\nexit 0\n"
		if runtime.GOOS == "windows" {
			ext = ".cmd"
			content = "@echo off\r\nexit /b 0\r\n"
		}
		path := filepath.Join(tmpDir, name+ext)
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatalf("write test executable %s: %v", name, err)
		}
	}
	t.Setenv("PATH", tmpDir)

	app := newTestApp(t)
	app.EnvCheck = envcheck.NewServiceWithRunner(runner)
	return app
}

// ---------------------------------------------------------------------------
// 1. parseCLITool
// ---------------------------------------------------------------------------

func TestParseCLITool(t *testing.T) {
	tests := []struct {
		input   string
		want    envcheck.CLITool
		wantErr bool
	}{
		// Claude Code aliases
		{"claude-code", envcheck.ToolClaudeCode, false},
		{"claude_code", envcheck.ToolClaudeCode, false},
		{"claude", envcheck.ToolClaudeCode, false},
		{"Claude-Code", envcheck.ToolClaudeCode, false},
		{"CLAUDE", envcheck.ToolClaudeCode, false},
		{"  claude  ", envcheck.ToolClaudeCode, false},

		// OpenCode aliases
		{"opencode", envcheck.ToolOpenCode, false},
		{"open-code", envcheck.ToolOpenCode, false},
		{"open_code", envcheck.ToolOpenCode, false},
		{"OpenCode", envcheck.ToolOpenCode, false},

		// Codex alias
		{"codex", envcheck.ToolCodex, false},
		{"Codex", envcheck.ToolCodex, false},
		{"CODEX", envcheck.ToolCodex, false},

		// Invalid inputs
		{"unknown-tool", "", true},
		{"", "", true},
		{"  ", "", true},
		{"npm", "", true},
		{"git", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseCLITool(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseCLITool(%q) expected error, got nil", tc.input)
				}
				if !strings.Contains(err.Error(), "unknown CLI tool") {
					t.Errorf("error should mention 'unknown CLI tool', got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCLITool(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("parseCLITool(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. GetEnvCheckStatus returns correct cached status
// ---------------------------------------------------------------------------

func TestGetEnvCheckStatus_ReturnsCachedStatus(t *testing.T) {
	// Arrange: create App with healthy env
	runner := &appEnvCheckRunner{}
	app := newTestAppWithEnvCheck(t, runner)

	// Act: run env check first
	overall, err := app.RunEnvCheck()
	if err != nil {
		t.Fatalf("RunEnvCheck() error = %v", err)
	}
	if overall == nil {
		t.Fatal("RunEnvCheck() returned nil")
	}

	// Act: get cached status
	cached := app.GetEnvCheckStatus()

	// Assert: cached status should be non-nil and contain all tools
	if cached == nil {
		t.Fatal("GetEnvCheckStatus() returned nil")
	}
	if len(cached.Items) != len(envcheck.SupportedTools()) {
		t.Errorf("cached Items count = %d, want %d", len(cached.Items), len(envcheck.SupportedTools()))
	}
	for _, tool := range envcheck.SupportedTools() {
		if _, ok := cached.Items[string(tool)]; !ok {
			t.Errorf("cached status missing %q", tool)
		}
	}
}

func TestGetEnvCheckStatus_ReturnsNilBeforeAnyCheck(t *testing.T) {
	// Arrange: create App with envcheck service but no checks run
	runner := &appEnvCheckRunner{}
	app := newTestAppWithEnvCheck(t, runner)

	// Act: get cached status without running any check
	cached := app.GetEnvCheckStatus()

	// Assert: should return non-nil but empty (per Service.GetCachedStatus contract)
	if cached == nil {
		t.Fatal("GetEnvCheckStatus() should return non-nil even before checks")
	}
	if len(cached.Items) != 0 {
		t.Errorf("expected 0 items before any check, got %d", len(cached.Items))
	}
}

// ---------------------------------------------------------------------------
// 3. RunEnvCheck with mock envcheck service
// ---------------------------------------------------------------------------

func TestRunEnvCheck_WithMockService_AllToolsHealthy(t *testing.T) {
	// Arrange
	runner := &appEnvCheckRunner{}
	app := newTestAppWithEnvCheck(t, runner)

	// Act
	overall, err := app.RunEnvCheck()

	// Assert
	if err != nil {
		t.Fatalf("RunEnvCheck() error = %v", err)
	}
	if overall == nil {
		t.Fatal("RunEnvCheck() returned nil")
	}
	if len(overall.Items) != len(envcheck.SupportedTools()) {
		t.Errorf("overall.Items count = %d, want %d", len(overall.Items), len(envcheck.SupportedTools()))
	}

	// Assert: all tools should be installed
	for _, tool := range envcheck.SupportedTools() {
		status, ok := overall.Items[string(tool)]
		if !ok {
			t.Errorf("missing tool %q in results", tool)
			continue
		}
		if !status.Installed {
			t.Errorf("%q should be installed", tool)
		}
		if !status.PATHOk {
			t.Errorf("%q should have PATHOk=true", tool)
		}
	}
}

func TestRunEnvCheck_WithMockService_PartialFailure(t *testing.T) {
	// Arrange: runner where Claude is broken
	runner := &appEnvCheckRunnerWithFailure{}
	app := newTestAppWithEnvCheck(t, runner)

	// Act
	overall, err := app.RunEnvCheck()

	// Assert: should succeed even with partial failure
	if err != nil {
		t.Fatalf("RunEnvCheck() error = %v, want nil (partial failure is acceptable)", err)
	}
	if overall == nil {
		t.Fatal("RunEnvCheck() returned nil")
	}

	// Assert: Claude should be missing or have error
	claudeStatus, claudeOK := overall.Items[string(envcheck.ToolClaudeCode)]
	if claudeOK {
		if claudeStatus.Installed && claudeStatus.Error == "" {
			t.Error("Claude should either not be installed or have an error with the failing runner")
		}
	}

	// Assert: OpenCode and Codex should be healthy
	ocStatus := overall.Items[string(envcheck.ToolOpenCode)]
	if !ocStatus.Installed {
		t.Error("OpenCode should be installed")
	}
	codexStatus := overall.Items[string(envcheck.ToolCodex)]
	if !codexStatus.Installed {
		t.Error("Codex should be installed")
	}
}

// ---------------------------------------------------------------------------
// 4. TestGetStartupEnv - Startup environment detection flow
// ---------------------------------------------------------------------------

// TestGetStartupEnv verifies the full startup environment detection flow:
// the App can perform environment checks and the results are accessible
// through both GetEnvCheckStatus and the startup warnings mechanism.
func TestGetStartupEnv(t *testing.T) {
	// Arrange: all tools healthy
	runner := &appEnvCheckRunner{}
	app := newTestAppWithEnvCheck(t, runner)

	// Act: simulate the startup env check (same as Startup goroutine does)
	status, err := app.EnvCheck.CheckAll()

	// Assert: check completed successfully
	if err != nil {
		t.Fatalf("startup env check failed: %v", err)
	}
	if status == nil {
		t.Fatal("startup env check returned nil status")
	}

	// Assert: all tools detected
	if len(status.Items) != len(envcheck.SupportedTools()) {
		t.Errorf("startup env check found %d tools, want %d",
			len(status.Items), len(envcheck.SupportedTools()))
	}

	// Assert: cached status is accessible via app API
	cached := app.GetEnvCheckStatus()
	if cached == nil {
		t.Fatal("GetEnvCheckStatus() returned nil after startup check")
	}
	if len(cached.Items) != len(status.Items) {
		t.Errorf("cached items = %d, want %d matching CheckAll result",
			len(cached.Items), len(status.Items))
	}

	// Assert: when all tools are healthy, no startup warnings needed
	if !status.AllOK {
		t.Errorf("status.AllOK = false, want true when all tools healthy; issues: %v", status.Issues)
	}
}

// TestGetStartupEnv_WithIssues verifies that when the env check detects
// problems, the status reflects them correctly through the app API.
func TestGetStartupEnv_WithIssues(t *testing.T) {
	// Arrange: one tool broken
	runner := &appEnvCheckRunnerWithFailure{}
	app := newTestAppWithEnvCheck(t, runner)

	// Act: simulate startup env check
	status, _ := app.EnvCheck.CheckAll()

	// Assert: check completed (may or may not have error, but status is non-nil)
	if status == nil {
		t.Fatal("startup env check returned nil status even with issues")
	}

	// Assert: issues are detected
	if len(status.Issues) == 0 {
		t.Error("expected issues to be detected for the broken Claude tool")
	}

	// Assert: the status is available through the app API
	cached := app.GetEnvCheckStatus()
	if cached == nil {
		t.Fatal("GetEnvCheckStatus() should return non-nil even when tools have issues")
	}
	if len(cached.Issues) == 0 {
		t.Error("cached status should also reflect the detected issues")
	}

	// Simulate what Startup does: convert issues to warnings
	if !status.AllOK {
		for _, issue := range status.Issues {
			app.addStartupWarning("[环境检测] " + issue)
		}
	}

	warnings := app.GetStartupWarnings()
	if len(warnings) == 0 {
		t.Fatal("expected startup warnings for env check issues")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "环境检测") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a warning containing '环境检测', got: %v", warnings)
	}
}

// TestGetStartupEnv_WarningsNotAddedWhenAllOK verifies that startup warnings
// are NOT generated when all tools are healthy.
func TestGetStartupEnv_WarningsNotAddedWhenAllOK(t *testing.T) {
	// Arrange: all tools healthy
	runner := &appEnvCheckRunner{}
	app := newTestAppWithEnvCheck(t, runner)

	// Act: run startup env check
	status, _ := app.EnvCheck.CheckAll()

	// Simulate startup warning logic
	if !status.AllOK {
		for _, issue := range status.Issues {
			app.addStartupWarning("[环境检测] " + issue)
		}
	}

	// Assert: no warnings when all tools are OK
	warnings := app.GetStartupWarnings()
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when all tools healthy, got: %v", warnings)
	}
}

// ---------------------------------------------------------------------------
// 5. CheckTool / InstallTool / UpdateTool API tests
// ---------------------------------------------------------------------------

func TestCheckTool_ParseAndDelegate(t *testing.T) {
	// Arrange
	runner := &appEnvCheckRunner{}
	app := newTestAppWithEnvCheck(t, runner)

	// Act & Assert: valid tool names
	for _, tc := range []struct {
		input string
		tool  envcheck.CLITool
	}{
		{"claude", envcheck.ToolClaudeCode},
		{"opencode", envcheck.ToolOpenCode},
		{"codex", envcheck.ToolCodex},
	} {
		t.Run(tc.input, func(t *testing.T) {
			status, err := app.CheckTool(tc.input)
			if err != nil {
				t.Fatalf("CheckTool(%q) error = %v", tc.input, err)
			}
			if status == nil {
				t.Fatalf("CheckTool(%q) returned nil status", tc.input)
			}
			if status.Tool != tc.tool {
				t.Errorf("status.Tool = %q, want %q", status.Tool, tc.tool)
			}
		})
	}
}

func TestCheckTool_InvalidTool(t *testing.T) {
	// Arrange
	runner := &appEnvCheckRunner{}
	app := newTestAppWithEnvCheck(t, runner)

	// Act
	_, err := app.CheckTool("nonexistent")

	// Assert
	if err == nil {
		t.Fatal("expected error for invalid tool name")
	}
	if !strings.Contains(err.Error(), "unknown CLI tool") {
		t.Errorf("error should mention 'unknown CLI tool', got: %v", err)
	}
}

func TestInstallTool_InvalidTool(t *testing.T) {
	runner := &appEnvCheckRunner{}
	app := newTestAppWithEnvCheck(t, runner)

	_, err := app.InstallTool("invalid")
	if err == nil {
		t.Fatal("expected error for invalid tool")
	}
}

func TestUpdateTool_InvalidTool(t *testing.T) {
	runner := &appEnvCheckRunner{}
	app := newTestAppWithEnvCheck(t, runner)

	_, err := app.UpdateTool("invalid")
	if err == nil {
		t.Fatal("expected error for invalid tool")
	}
}
