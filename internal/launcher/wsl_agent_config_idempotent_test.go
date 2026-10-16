package launcher

// wsl_agent_config_idempotent_test.go — P3-S1 零写入路径：内容一致时
// WriteWSLPiAgentConfig / WriteWSLOpenCodeAgentConfig 跳过落盘与 chmod 补偿
//（验收 6：启动两次第二次零写入的 overlay 半边；托管单元半边由 wslsupply
// ActionNone 钉住）。

import (
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/config"
)

func TestWriteWSLPiAgentConfigSkipsIdenticalContent(t *testing.T) {
	ft := installFakeWSLTarget(t)
	provider := config.Provider{Anthropic: &config.AnthropicFormat{Enabled: true, BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4"}}
	cfg, err := BuildPiModelsConfig("glm", provider, "glm-5.3", "sk-test", config.Parameters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first := ft.chmodCalls
	if len(first) == 0 {
		t.Fatal("first write must run chmod compensation")
	}
	target := ft.uncPath("/home/wslu/.pi/agent/models.json")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	// 第二次写入完全相同配置：跳过落盘与 chmod（零写入路径）。
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if len(ft.chmodCalls) != len(first) {
		t.Fatalf("identical content must skip chmod compensation: %d -> %d", len(first), len(ft.chmodCalls))
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("identical content must not rewrite the target")
	}
}

func TestWriteWSLOpenCodeAgentConfigSkipsIdenticalContent(t *testing.T) {
	ft := installFakeWSLTarget(t)
	cfg := map[string]any{"provider": map[string]any{"openai": map[string]any{"options": map[string]any{"apiKey": "sk-x"}}}}
	if _, err := WriteWSLOpenCodeAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first := ft.chmodCalls
	target := filepath.Join(ft.uncPath("/home/wslu/.config/opencode"), openCodeWSLConfigFile)
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteWSLOpenCodeAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if len(ft.chmodCalls) != len(first) {
		t.Fatalf("identical content must skip chmod compensation: %d -> %d", len(first), len(ft.chmodCalls))
	}
	after, _ := os.ReadFile(target)
	if string(before) != string(after) {
		t.Fatal("identical content must not rewrite the target")
	}
	// 内容变化：走完整写入路径（chmod 补偿恢复）。
	cfg["model"] = "openai/gpt-x"
	if _, err := WriteWSLOpenCodeAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("changed write: %v", err)
	}
	if len(ft.chmodCalls) <= len(first) {
		t.Fatal("changed content must run full write + chmod compensation")
	}
}
