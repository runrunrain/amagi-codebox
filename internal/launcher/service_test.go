package launcher

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
)

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
