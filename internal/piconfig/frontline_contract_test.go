package piconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// amagi-pi 前线契约文件（codex-frontline.json）合并进下拉目录的回归测试。
// 契约背景：amagi-pi refreshModels 运行时注入的新模型（如 gpt-6-astra，pi.dev
// 官方目录未收录）不落盘 models-store.json；契约文件生效时落盘、官方收录后退
// 位删除。展示侧语义：同 id 已存在则跳过（官方优先），坏文件静默忽略。
func TestFrontlineContractMerge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	s := NewService()

	// 内置目录（官方 feed 缓存）：openai-codex 仅 gpt-5.6-sol
	storeFixture := `{"openai-codex":{"models":[{"id":"gpt-5.6-sol","name":"GPT-5.6 Sol","reasoning":true}]}}`
	if err := os.WriteFile(filepath.Join(dir, "models-store.json"), []byte(storeFixture), 0o600); err != nil {
		t.Fatalf("write store fixture: %v", err)
	}
	// 前线契约：gpt-6-astra（新增）+ gpt-5.6-sol（已被官方收录，应跳过）
	contractFixture := `{"provider":"openai-codex","updatedAt":"2026-09-05T00:00:00Z","models":[` +
		`{"id":"gpt-6-astra","name":"GPT-6 Astra","reasoning":true,"thinkingLevelMap":{"high":"high"},"contextWindow":272000},` +
		`{"id":"gpt-5.6-sol","name":"GPT-5.6 Sol","reasoning":true}]}`
	contractDir := filepath.Join(dir, "amagi", "data")
	if err := os.MkdirAll(contractDir, 0o755); err != nil {
		t.Fatalf("mkdir contract dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contractDir, "codex-frontline.json"), []byte(contractFixture), 0o600); err != nil {
		t.Fatalf("write contract fixture: %v", err)
	}

	catalog, err := s.GetPiModelCatalog()
	if err != nil {
		t.Fatalf("GetPiModelCatalog: %v", err)
	}
	var cat struct {
		Providers []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
			Models []struct {
				ID             string `json:"id"`
				Reasoning      bool   `json:"reasoning"`
				ContextWindow  int    `json:"contextWindow"`
				ThinkingLevels []string `json:"thinkingLevels"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(catalog), &cat); err != nil {
		t.Fatalf("parse catalog: %v", err)
	}

	var codex *struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Models []struct {
			ID             string `json:"id"`
			Reasoning      bool   `json:"reasoning"`
			ContextWindow  int    `json:"contextWindow"`
			ThinkingLevels []string `json:"thinkingLevels"`
		} `json:"models"`
	}
	for i := range cat.Providers {
		if cat.Providers[i].Name == "openai-codex" {
			codex = &cat.Providers[i]
		}
	}
	if codex == nil {
		t.Fatal("openai-codex provider 缺席")
	}
	byID := map[string]bool{}
	for _, m := range codex.Models {
		byID[m.ID] = true
		if m.ID == "gpt-6-astra" {
			if !m.Reasoning || m.ContextWindow != 272000 || len(m.ThinkingLevels) == 0 {
				t.Fatalf("gpt-6-astra 元数据不完整: %+v", m)
			}
		}
	}
	if !byID["gpt-6-astra"] {
		t.Fatal("前线模型 gpt-6-astra 未合并进下拉目录")
	}
	if !byID["gpt-5.6-sol"] {
		t.Fatal("官方模型 gpt-5.6-sol 丢失")
	}
	if len(codex.Models) != 2 {
		t.Fatalf("同 id 应去重（官方优先），期望 2 个模型，实际 %d: %v", len(codex.Models), byID)
	}
}

// 负例：契约文件缺失 / 坏 JSON / 空 provider → 静默跳过，目录不受影响。
func TestFrontlineContractNegative(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	s := NewService()

	storeFixture := `{"openai-codex":{"models":[{"id":"gpt-5.6-sol","reasoning":true}]}}`
	if err := os.WriteFile(filepath.Join(dir, "models-store.json"), []byte(storeFixture), 0o600); err != nil {
		t.Fatalf("write store fixture: %v", err)
	}
	contractDir := filepath.Join(dir, "amagi", "data")
	if err := os.MkdirAll(contractDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// 缺失（无契约文件）→ 正常目录
	catalog, err := s.GetPiModelCatalog()
	if err != nil {
		t.Fatalf("no contract: %v", err)
	}
	if catalog == "" || !contains(catalog, "gpt-5.6-sol") {
		t.Fatal("无契约文件时目录应正常")
	}

	// 坏 JSON → 静默跳过
	if err := os.WriteFile(filepath.Join(contractDir, "codex-frontline.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write bad contract: %v", err)
	}
	catalog, err = s.GetPiModelCatalog()
	if err != nil {
		t.Fatalf("bad contract should not error: %v", err)
	}
	if contains(catalog, "frontline-dummy") {
		t.Fatal("坏契约不应产出模型")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
