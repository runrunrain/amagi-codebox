package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
)

func TestIsCustomCodexOpenAIBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "empty", baseURL: "", want: false},
		{name: "official root", baseURL: "https://api.openai.com", want: false},
		{name: "official v1", baseURL: "https://api.openai.com/v1", want: false},
		{name: "official v1 trailing slash", baseURL: "https://api.openai.com/v1/", want: false},
		{name: "official without scheme", baseURL: "api.openai.com/v1", want: false},
		{name: "proxy host", baseURL: "https://proxy.example.com/v1", want: true},
		{name: "localhost proxy", baseURL: "http://localhost:11434/v1", want: true},
		{name: "openai host non-default path", baseURL: "https://api.openai.com/custom/v1", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCustomCodexOpenAIBaseURL(tt.baseURL); got != tt.want {
				t.Fatalf("isCustomCodexOpenAIBaseURL(%q) = %v, want %v", tt.baseURL, got, tt.want)
			}
		})
	}
}

// --- Codex global headroom openai_base_url marker block tests ---

// TestRemoveCodexLegacyHeadroomMarkerConfig 验证启动善后链路（R4 §4.5）：
// 用户 config.toml 中遗留的 amagi-headroom-global 标记块被一次性移除；
// 无标记 / 文件不存在为 no-op；其余内容逐字保留。
func TestRemoveCodexLegacyHeadroomMarkerConfig(t *testing.T) {
	isolatedHome := t.TempDir()
	t.Setenv("HOME", isolatedHome)
	t.Setenv("USERPROFILE", isolatedHome)
	t.Setenv("CODEX_HOME", filepath.Join(isolatedHome, ".codex"))
	configDir := filepath.Join(isolatedHome, ".codex")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	configPath := filepath.Join(configDir, "config.toml")
	legacy := "model = \"gpt-5.6\"\n" +
		"# === amagi-headroom-global-start ===\n" +
		"openai_base_url = \"http://127.0.0.1:8788/v1\"\n" +
		"# === amagi-headroom-global-end ===\n" +
		"\n[profiles.minimal]\nmodel = \"mini\"\n"
	if err := os.WriteFile(configPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile legacy config: %v", err)
	}

	if err := removeCodexLegacyHeadroomMarkerConfig(); err != nil {
		t.Fatalf("removeCodexLegacyHeadroomMarkerConfig: %v", err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(got), "amagi-headroom-global") || strings.Contains(string(got), "openai_base_url") {
		t.Fatalf("marker block survived cleanup:\n%s", got)
	}
	if !strings.Contains(string(got), "model = \"gpt-5.6\"") || !strings.Contains(string(got), "[profiles.minimal]") {
		t.Fatalf("unrelated config lines were damaged:\n%s", got)
	}

	// 幂等：清除后再跑一次 no-op（内容不变）。
	before := string(got)
	if err := removeCodexLegacyHeadroomMarkerConfig(); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != before {
		t.Fatalf("idempotent rerun changed content:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// 文件不存在 → no-op。
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := removeCodexLegacyHeadroomMarkerConfig(); err != nil {
		t.Fatalf("missing config must be a no-op, got: %v", err)
	}
}

// TestRemoveCodexGlobalHeadroomBlockPureFunction 冻结纯函数语义：标记块
// （含首尾标记行与块内全部行）被剥离，无标记时原样返回。
func TestRemoveCodexGlobalHeadroomBlockPureFunction(t *testing.T) {
	lines := []string{"a = 1", "# === amagi-headroom-global-start ===", "openai_base_url = \"http://127.0.0.1:8788/v1\"", "# === amagi-headroom-global-end ===", "b = 2"}
	got := removeCodexGlobalHeadroomBlock(lines)
	if len(got) != 2 || got[0] != "a = 1" || got[1] != "b = 2" {
		t.Fatalf("removeCodexGlobalHeadroomBlock = %v, want [a = 1 b = 2]", got)
	}
	same := removeCodexGlobalHeadroomBlock([]string{"a = 1"})
	if len(same) != 1 || same[0] != "a = 1" {
		t.Fatalf("no-marker input was modified: %v", same)
	}
}

// TestBuildCodexCLIArgsWireAPI 验证 -c 进程级覆盖的 wire_api 动态化：
// chat → wire_api="chat"；未设置/"responses"/非法值 → wire_api="responses"（legacy 默认回归）。
func TestBuildCodexCLIArgsWireAPI(t *testing.T) {
	base := codexLaunchSettings{
		Model:           "gpt-5.6-luna",
		ProviderBaseURL: "https://proxy.example.com/v1",
	}

	cases := []struct {
		name    string
		wireAPI string
		want    string
		forbid  string
	}{
		{"chat", "chat", `wire_api="chat"`, `wire_api="responses"`},
		{"unset keeps legacy responses", "", `wire_api="responses"`, `wire_api="chat"`},
		{"explicit responses", "responses", `wire_api="responses"`, `wire_api="chat"`},
		{"illegal value falls back to responses", "grpc", `wire_api="responses"`, `wire_api="chat"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := base
			settings.WireAPI = tc.wireAPI
			args := buildCodexCLIArgs(settings)
			joined := strings.Join(args, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("Codex args missing %q: %v", tc.want, args)
			}
			if strings.Contains(joined, tc.forbid) {
				t.Fatalf("Codex args should not contain %q: %v", tc.forbid, args)
			}
		})
	}
}

// TestResolveCodexLaunchSettings_WireAPI 验证 wire_api 从 provider.OpenAI 透传：
// 双格式 provider 取 EffectiveWireAPI 归一化值；legacy 单格式（无 OpenAI 子块）为空。
func TestResolveCodexLaunchSettings_WireAPI(t *testing.T) {
	cases := []struct {
		name   string
		openAI *config.OpenAIFormat
		want   string
	}{
		{"responses passthrough", &config.OpenAIFormat{Enabled: true, WireAPI: "responses"}, "responses"},
		{"chat passthrough", &config.OpenAIFormat{Enabled: true, WireAPI: "chat"}, "chat"},
		{"uppercase normalized", &config.OpenAIFormat{Enabled: true, WireAPI: " CHAT "}, "chat"},
		{"unset empty", &config.OpenAIFormat{Enabled: true}, ""},
		{"illegal normalized to empty", &config.OpenAIFormat{Enabled: true, WireAPI: "grpc"}, ""},
		{"nil openai block empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := config.Provider{
				Type:         "openai",
				DefaultModel: "gpt-5.5",
				OpenAI:       tc.openAI,
			}
			settings := resolveCodexLaunchSettings(provider, "")
			if settings.WireAPI != tc.want {
				t.Fatalf("WireAPI = %q, want %q", settings.WireAPI, tc.want)
			}
		})
	}
}
