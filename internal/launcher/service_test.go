package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/session"
)

func TestLauncherRecoveredProcessIdentitySurvivesInstanceBoundary(t *testing.T) {
	if os.Getenv("AMAGI_LAUNCHER_HELPER_PROCESS") == "1" {
		time.Sleep(30 * time.Second)
		return
	}
	logService := logging.NewService(t.TempDir())
	t.Cleanup(logService.Close)

	cmd := exec.Command(os.Args[0], "-test.run=TestLauncherRecoveredProcessIdentitySurvivesInstanceBoundary")
	cmd.Env = append(os.Environ(), "AMAGI_LAUNCHER_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper child: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	const sessionID = "cross-instance-helper"
	first := NewLauncherService(logService, nil)
	first.processes[sessionID] = cmd
	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		first.mu.Lock()
		delete(first.processes, sessionID)
		first.mu.Unlock()
		close(waitDone)
	}()

	identity, err := first.CaptureProcessIdentity(sessionID)
	if err != nil {
		t.Fatalf("CaptureProcessIdentity: %v", err)
	}
	mismatched := NewLauncherService(logService, nil)
	if running, err := mismatched.RecoverProcess(sessionID, cmd.Process.Pid, identity+":reused"); err != nil || running {
		t.Fatalf("PID-reuse mismatch running=%v err=%v want terminal original identity", running, err)
	}
	if !first.IsRunning(sessionID) {
		t.Fatal("identity mismatch signalled the still-live unrelated process")
	}

	second := NewLauncherService(logService, nil)
	running, err := second.RecoverProcess(sessionID, cmd.Process.Pid, identity)
	if err != nil || !running {
		t.Fatalf("RecoverProcess running=%v err=%v", running, err)
	}
	if err := second.Stop(sessionID); err != nil {
		t.Fatalf("Stop recovered process: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for second.IsRunning(sessionID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if second.IsRunning(sessionID) {
		currentIdentity, running, inspectErr := inspectExternalProcess(cmd.Process.Pid)
		t.Fatalf("identity-verified recovered helper did not reach terminal: identity=%q running=%v err=%v", currentIdentity, running, inspectErr)
	}
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("original child Wait did not observe recovered Stop terminal")
	}
}

func TestLauncherLegacyProcFSRecoveryRetainsLiveProcessWithoutSignal(t *testing.T) {
	if os.Getenv("AMAGI_LAUNCHER_LEGACY_HELPER_PROCESS") == "1" {
		time.Sleep(30 * time.Second)
		return
	}
	logService := logging.NewService(t.TempDir())
	t.Cleanup(logService.Close)

	cmd := exec.Command(os.Args[0], "-test.run=^TestLauncherLegacyProcFSRecoveryRetainsLiveProcessWithoutSignal$")
	cmd.Env = append(os.Environ(), "AMAGI_LAUNCHER_LEGACY_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start legacy helper child: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	const sessionID = "legacy-procfs-helper"
	owner := NewLauncherService(logService, nil)
	owner.processes[sessionID] = cmd
	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		owner.mu.Lock()
		delete(owner.processes, sessionID)
		owner.mu.Unlock()
		close(waitDone)
	}()

	recovered := NewLauncherService(logService, nil)
	running, err := recovered.RecoverProcess(sessionID, cmd.Process.Pid, "procfs:424242")
	if !running || !errors.Is(err, ErrLegacyProcFSIdentity) {
		t.Fatalf("legacy RecoverProcess running=%v err=%v", running, err)
	}
	if err := recovered.Stop(sessionID); !errors.Is(err, ErrLegacyProcFSIdentity) {
		t.Fatalf("legacy Stop error=%v want migration uncertainty", err)
	}
	time.Sleep(50 * time.Millisecond)
	if !owner.IsRunning(sessionID) {
		t.Fatal("legacy Stop signalled the live helper")
	}
	if !recovered.IsRunning(sessionID) {
		t.Fatal("legacy recovery discarded ownership while helper remained live")
	}

	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill helper during cleanup: %v", err)
	}
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("legacy helper Wait did not observe terminal")
	}
	deadline := time.Now().Add(5 * time.Second)
	for recovered.IsRunning(sessionID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if recovered.IsRunning(sessionID) {
		t.Fatal("legacy recovery did not complete after proven process absence")
	}
}

func TestLauncherGuardRejectsClaudeAndCodexBeforeRawStart(t *testing.T) {
	logService := logging.NewService(t.TempDir())
	t.Cleanup(logService.Close)
	svc := NewLauncherService(logService, nil)
	guardErr := errors.New("injected shutdown generation fence")
	guard := func() error { return guardErr }
	workDir := t.TempDir()
	provider := config.Provider{Type: "anthropic", BaseURL: "https://api.anthropic.com"}

	if _, err := svc.LaunchGuarded("guarded-claude", provider, "", "", config.AgentTeamsConfig{}, session.ModeTerminal, workDir, guard); !errors.Is(err, guardErr) {
		t.Fatalf("Claude guarded launch error=%v", err)
	}
	if _, err := svc.LaunchCodexGuarded("guarded-codex", "", session.ModeTerminal, workDir, nil, guard); !errors.Is(err, guardErr) {
		t.Fatalf("Codex guarded launch error=%v", err)
	}
	if got := svc.RunningCount(); got != 0 {
		t.Fatalf("rejected guarded launches registered %d process(es)", got)
	}
}

func TestLauncherStopFailureRetainsProcessForRetryOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("invalid-PID kill semantics are Unix-specific; Windows is cross-compiled")
	}
	logService := logging.NewService(t.TempDir())
	t.Cleanup(logService.Close)
	svc := NewLauncherService(logService, nil)

	// FindProcess succeeds without validating a Unix PID. Kill then fails
	// deterministically, exercising the ownership-preserving error path without
	// starting or signalling a real process.
	process, err := os.FindProcess(1 << 30)
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	t.Cleanup(func() { _ = process.Release() })
	const sessionID = "failed-stop-owner"
	svc.processes[sessionID] = &exec.Cmd{Process: process}

	if err := svc.Stop(sessionID); err == nil {
		t.Fatal("Stop unexpectedly succeeded for invalid PID")
	}
	if !svc.IsRunning(sessionID) {
		t.Fatal("failed Stop discarded process ownership; retry/reaper can no longer address it")
	}
}

func TestLauncherStopAllFailureRetainsProcessForShutdownReaper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("invalid-PID kill semantics are Unix-specific; Windows is cross-compiled")
	}
	logService := logging.NewService(t.TempDir())
	t.Cleanup(logService.Close)
	svc := NewLauncherService(logService, nil)
	process, err := os.FindProcess(1 << 30)
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	t.Cleanup(func() { _ = process.Release() })
	const sessionID = "failed-stop-all-owner"
	svc.processes[sessionID] = &exec.Cmd{Process: process}

	svc.StopAll()
	if !svc.IsRunning(sessionID) {
		t.Fatal("failed StopAll discarded process ownership before shutdown reaper receipt")
	}
}

func TestBuildOverrides_DualFormatProviderUsesAnthropicForClaude(t *testing.T) {
	svc := NewLauncherService(nil, nil)
	provider := config.Provider{
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
	}

	overrides := svc.BuildOverrides(provider, "", "sk-provider-level", config.AgentTeamsConfig{})
	if overrides["ANTHROPIC_API_KEY"] != "sk-provider-level" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want sk-provider-level", overrides["ANTHROPIC_API_KEY"])
	}
	if overrides["ANTHROPIC_BASE_URL"] != "https://anthropic.example.com" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want https://anthropic.example.com", overrides["ANTHROPIC_BASE_URL"])
	}
	if overrides["ANTHROPIC_MODEL"] != "claude-sonnet-4-5" {
		t.Fatalf("ANTHROPIC_MODEL = %q, want claude-sonnet-4-5", overrides["ANTHROPIC_MODEL"])
	}
}

func TestBuildClaudeCmdUsesEffectivePATHWithNativeDefaultAfterPathOverride(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin controlled PATH assertions are only defined on macOS")
	}

	homeDir := t.TempDir()
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	nativePath := filepath.Join(nativeDir, "claude")
	if err := os.MkdirAll(nativeDir, 0o755); err != nil {
		t.Fatalf("mkdir native dir: %v", err)
	}
	if err := os.WriteFile(nativePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write native claude: %v", err)
	}

	overriddenPathDir := t.TempDir()
	svc := NewLauncherService(nil, nil)
	cmd := svc.buildClaudeCmd(t.TempDir(), []string{
		"HOME=" + homeDir,
		"PATH=" + overriddenPathDir,
	})

	if cmd.Path != nativePath {
		t.Fatalf("cmd path = %q, want native Claude path %q", cmd.Path, nativePath)
	}
	pathValue := envValueForTest(cmd.Env, "PATH")
	if !pathListContainsForTest(pathValue, nativeDir) {
		t.Fatalf("launcher PATH %q does not include native dir %q", pathValue, nativeDir)
	}
	if !pathListContainsForTest(pathValue, overriddenPathDir) {
		t.Fatalf("launcher PATH %q does not preserve caller override dir %q", pathValue, overriddenPathDir)
	}
}

// TestBuildOmpCmdArgs 验证 buildOmpCmd 的参数拼装（复刻 buildPiCmd 契约）：
// --provider/--model/--thinking 仅在非空时附加；命令经 resolveCLIPath("omp") 解析。
func TestBuildOmpCmdArgs(t *testing.T) {
	svc := NewLauncherService(nil, nil)
	env := []string{"PATH=/usr/bin:/bin"}

	// 全参数：provider + model + thinking
	cmd := svc.buildOmpCmd("amagi-glm", "glm-5", "max", "/work", env)
	if cmd.Path == "omp" || cmd.Path == "" {
		t.Errorf("cmd path = %q, want resolved omp path (not bare name)", cmd.Path)
	}
	if cmd.Dir != "/work" {
		t.Errorf("cmd.Dir = %q, want /work", cmd.Dir)
	}
	wantArgs := []string{"--provider", "amagi-glm", "--model", "glm-5", "--thinking", "max"}
	if strings.Join(cmd.Args[1:], " ") != strings.Join(wantArgs, " ") {
		t.Errorf("args = %v, want %v", cmd.Args[1:], wantArgs)
	}

	// 空 thinking：不附加 --thinking
	cmd2 := svc.buildOmpCmd("amagi-glm", "glm-5", "", "/work", env)
	if strings.Join(cmd2.Args[1:], " ") != "--provider amagi-glm --model glm-5" {
		t.Errorf("args without thinking = %v", cmd2.Args[1:])
	}

	// 全空：无任何附加参数
	cmd3 := svc.buildOmpCmd("", "", "", "/work", env)
	if len(cmd3.Args) != 1 {
		t.Errorf("args with empty inputs = %v, want only the program path", cmd3.Args)
	}

	// 环境变量透传
	cmd4 := svc.buildOmpCmd("", "", "", "/work", []string{"OPENAI_API_KEY=sk-test", "PATH=/usr/bin"})
	if got := envValueForTest(cmd4.Env, "OPENAI_API_KEY"); got != "sk-test" {
		t.Errorf("env OPENAI_API_KEY = %q, want sk-test", got)
	}
}

func envValueForTest(env []string, key string) string {
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && parts[0] == key {
			return parts[1]
		}
	}
	return ""
}

func pathListContainsForTest(pathValue string, want string) bool {
	for _, entry := range filepath.SplitList(pathValue) {
		if filepath.Clean(entry) == filepath.Clean(want) {
			return true
		}
	}
	return false
}

// TestBuildOverrides_ReasoningEffort 测试 reasoning_effort 字段正确映射到 CLAUDE_CODE_EFFORT_LEVEL
func TestBuildOverrides_ReasoningEffort(t *testing.T) {
	svc := NewLauncherService(nil, nil)

	provider := config.Provider{
		Anthropic: &config.AnthropicFormat{
			Enabled: true,
		},
		DefaultModel: "claude-sonnet-4-20250514",
		Presets: map[string]config.Preset{
			"test-preset": {
				Name:  "Test Preset",
				Model: "claude-sonnet-4-20250514",
				Parameters: config.Parameters{
					ReasoningEffort: "high",
				},
			},
		},
	}

	overrides := svc.BuildOverrides(provider, "test-preset", "sk-test-key", config.AgentTeamsConfig{})

	effort, ok := overrides["CLAUDE_CODE_EFFORT_LEVEL"]
	if !ok {
		t.Fatal("CLAUDE_CODE_EFFORT_LEVEL not found in overrides")
	}
	if effort != "high" {
		t.Fatalf("CLAUDE_CODE_EFFORT_LEVEL = %q, want %q", effort, "high")
	}
}

// TestBuildOverrides_ReasoningEffort_Empty 测试空 reasoning_effort 不设置环境变量
func TestBuildOverrides_ReasoningEffort_Empty(t *testing.T) {
	svc := NewLauncherService(nil, nil)

	provider := config.Provider{
		Anthropic: &config.AnthropicFormat{
			Enabled: true,
		},
		DefaultModel: "claude-sonnet-4-20250514",
		Presets: map[string]config.Preset{
			"test-preset": {
				Name:  "Test Preset",
				Model: "claude-sonnet-4-20250514",
				Parameters: config.Parameters{
					ReasoningEffort: "", // 空值不设置环境变量
				},
			},
		},
	}

	overrides := svc.BuildOverrides(provider, "test-preset", "sk-test-key", config.AgentTeamsConfig{})

	if _, ok := overrides["CLAUDE_CODE_EFFORT_LEVEL"]; ok {
		t.Fatal("CLAUDE_CODE_EFFORT_LEVEL should not be set when reasoning_effort is empty")
	}
}

// TestBuildOverrides_ReasoningEffort_Whitespace 测试纯空白 reasoning_effort 不设置环境变量
func TestBuildOverrides_ReasoningEffort_Whitespace(t *testing.T) {
	svc := NewLauncherService(nil, nil)

	provider := config.Provider{
		Anthropic: &config.AnthropicFormat{
			Enabled: true,
		},
		DefaultModel: "claude-sonnet-4-20250514",
		Presets: map[string]config.Preset{
			"test-preset": {
				Name:  "Test Preset",
				Model: "claude-sonnet-4-20250514",
				Parameters: config.Parameters{
					ReasoningEffort: "   ", // 纯空白
				},
			},
		},
	}

	overrides := svc.BuildOverrides(provider, "test-preset", "sk-test-key", config.AgentTeamsConfig{})

	if _, ok := overrides["CLAUDE_CODE_EFFORT_LEVEL"]; ok {
		t.Fatal("CLAUDE_CODE_EFFORT_LEVEL should not be set when reasoning_effort is whitespace-only")
	}
}

// TestBuildOverrides_ReasoningEffort_AllLevels 测试所有合法的 reasoning_effort 级别
func TestBuildOverrides_ReasoningEffort_AllLevels(t *testing.T) {
	svc := NewLauncherService(nil, nil)

	levels := []string{"low", "medium", "high", "xhigh", "max"}

	for _, level := range levels {
		provider := config.Provider{
			Anthropic: &config.AnthropicFormat{
				Enabled: true,
			},
			DefaultModel: "claude-sonnet-4-20250514",
			Presets: map[string]config.Preset{
				"test-preset": {
					Name:  "Test Preset",
					Model: "claude-sonnet-4-20250514",
					Parameters: config.Parameters{
						ReasoningEffort: level,
					},
				},
			},
		}

		overrides := svc.BuildOverrides(provider, "test-preset", "sk-test-key", config.AgentTeamsConfig{})

		effort, ok := overrides["CLAUDE_CODE_EFFORT_LEVEL"]
		if !ok {
			t.Fatalf("CLAUDE_CODE_EFFORT_LEVEL not found in overrides for level %q", level)
		}
		if effort != level {
			t.Fatalf("CLAUDE_CODE_EFFORT_LEVEL = %q, want %q", effort, level)
		}
	}
}

// TestBuildEnvInjectsSystemProxy 验证：环境无代理键且系统代理可用时，BuildEnv
// 尾部注入 HTTP_PROXY/HTTPS_PROXY/NO_PROXY；用户显式代理配置优先，不被覆盖；
// 系统代理不可用（返回 nil）时不注入。
func TestBuildEnvInjectsSystemProxy(t *testing.T) {
	orig := systemProxyEnvFn
	defer func() { systemProxyEnvFn = orig }()

	envHas := func(env []string, key string) (string, bool) {
		prefix := key + "="
		for _, kv := range env {
			if strings.HasPrefix(kv, prefix) {
				return kv[len(prefix):], true
			}
		}
		return "", false
	}

	// 1) 无代理键 + 系统代理可用 -> 注入。
	systemProxyEnvFn = func() map[string]string {
		return map[string]string{
			"HTTP_PROXY": "http://127.0.0.1:5800", "HTTPS_PROXY": "http://127.0.0.1:5800",
			"http_proxy": "http://127.0.0.1:5800", "https_proxy": "http://127.0.0.1:5800",
			"NO_PROXY": "localhost,127.0.0.1,::1", "no_proxy": "localhost,127.0.0.1,::1",
		}
	}
	env := BuildEnv([]string{"PATH=/usr/bin"}, nil)
	if v, ok := envHas(env, "HTTPS_PROXY"); !ok || v != "http://127.0.0.1:5800" {
		t.Errorf("HTTPS_PROXY = %q,%v, want injected", v, ok)
	}
	if v, ok := envHas(env, "https_proxy"); !ok || v != "http://127.0.0.1:5800" {
		t.Errorf("https_proxy = %q,%v, want injected", v, ok)
	}
	if v, ok := envHas(env, "NO_PROXY"); !ok || !strings.Contains(v, "127.0.0.1") {
		t.Errorf("NO_PROXY = %q,%v, want localhost bypass", v, ok)
	}

	// 2) base 已有代理键 -> 不注入（用户显式配置优先）。
	env = BuildEnv([]string{"PATH=/usr/bin", "HTTPS_PROXY=http://10.0.0.1:7890"}, nil)
	if v, _ := envHas(env, "HTTPS_PROXY"); v != "http://10.0.0.1:7890" {
		t.Errorf("user HTTPS_PROXY overridden: %q", v)
	}
	if _, ok := envHas(env, "NO_PROXY"); ok {
		t.Errorf("NO_PROXY must not be injected when user proxy present")
	}

	// 3) overrides 显式代理键 -> 不注入。
	env = BuildEnv([]string{"PATH=/usr/bin"}, map[string]string{"http_proxy": "http://10.0.0.2:8080"})
	if _, ok := envHas(env, "HTTPS_PROXY"); ok {
		t.Errorf("HTTPS_PROXY must not be injected when override proxy present")
	}

	// 4) 系统代理不可用（nil） -> 不注入。
	systemProxyEnvFn = func() map[string]string { return nil }
	env = BuildEnv([]string{"PATH=/usr/bin"}, nil)
	if _, ok := envHas(env, "HTTPS_PROXY"); ok {
		t.Errorf("HTTPS_PROXY must not be injected when system proxy unavailable")
	}
}
