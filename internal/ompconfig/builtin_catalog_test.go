package ompconfig

import (
	"testing"
)

// `omp models ls --json` 输出解析的封闭测试（不依赖 CLI）。
func TestParseOmpModelsList(t *testing.T) {
	fixture := []byte(`{"models":[
		{"provider":"amagi-glm","id":"glm-5.2","name":"GLM 5.2","reasoning":true,"contextWindow":128000,"thinking":null},
		{"provider":"openai-codex","id":"gpt-5.6-sol","name":"GPT-5.6 Sol","reasoning":true,"contextWindow":1000000,
		 "thinking":{"mode":"effort","levels":["low","high","max"]}},
		{"provider":"kimi-code","id":"k3-256k","reasoning":false,"thinking":{"levels":["high"]}},
		{"provider":"amagi-glm","id":"glm-5.2-air","reasoning":false}
	]}`)

	out, err := parseOmpModelsList(fixture, []string{"amagi-glm"})
	if err != nil {
		t.Fatalf("parseOmpModelsList: %v", err)
	}
	// amagi-glm 是自定义注册表提供商，应被跳过；openai-codex / kimi-code 为内置
	if len(out) != 2 {
		t.Fatalf("want 2 builtin providers, got %d: %+v", len(out), out)
	}
	if out[0].Name != "kimi-code" || out[1].Name != "openai-codex" {
		t.Fatalf("builtin order wrong: %+v", out)
	}
	codex := out[1]
	if codex.Source != "builtin" || len(codex.Models) != 1 {
		t.Fatalf("openai-codex entry wrong: %+v", codex)
	}
	if codex.Models[0].ID != "gpt-5.6-sol" || !codex.Models[0].Reasoning || codex.Models[0].ContextWindow != 1000000 {
		t.Fatalf("openai-codex model wrong: %+v", codex.Models[0])
	}
	if len(codex.Models[0].ThinkingLevels) != 3 {
		t.Fatalf("thinking levels not extracted: %+v", codex.Models[0])
	}
	if len(out[0].Models) != 1 || out[0].Models[0].ThinkingLevels[0] != "high" {
		t.Fatalf("kimi-code model wrong: %+v", out[0].Models)
	}

	// 非法 JSON 拒绝
	if _, err := parseOmpModelsList([]byte("not json"), nil); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
