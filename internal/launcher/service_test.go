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
