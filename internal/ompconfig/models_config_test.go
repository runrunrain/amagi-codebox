package ompconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 临时冒烟测试（验证后删除）：用临时 agent 目录验证 models.yml 注册表读写。
func TestModelsConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	s := NewService()

	// 文件缺失：返回默认骨架
	content, err := s.GetModelsConfig()
	if err != nil {
		t.Fatalf("GetModelsConfig default: %v", err)
	}
	if !strings.Contains(content, "providers") {
		t.Fatalf("default skeleton unexpected: %.100s", content)
	}

	// 保存合法 YAML（含 auth: none 非常规字段）
	fixture := "providers:\n  amagi-test:\n    api: openai-completions\n    auth: none\n    baseUrl: https://x/v1\n    models:\n      - id: m1\n        reasoning: true\n        thinking:\n          mode: effort\n          levels:\n            - low\n            - high\n"
	if err := s.SaveModelsConfig(fixture); err != nil {
		t.Fatalf("SaveModelsConfig: %v", err)
	}

	// 权限 0600
	info, err := os.Stat(filepath.Join(dir, "models.yml"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("want 0600, got %v", info.Mode().Perm())
	}

	// 回读保留全部字段
	got, err := s.GetModelsConfig()
	if err != nil {
		t.Fatalf("GetModelsConfig: %v", err)
	}
	for _, want := range []string{"amagi-test", "auth: none", "https://x/v1", "m1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("round-trip lost %q in:\n%s", want, got)
		}
	}

	// 非法 YAML 拒绝
	if err := s.SaveModelsConfig("a:\n\t- b: [unclosed"); err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	// 非映射根拒绝
	if err := s.SaveModelsConfig("- 1\n- 2\n"); err == nil {
		t.Fatal("expected error for sequence root")
	}

	// 目录：thinking.levels 数组形态
	catalog, err := s.GetOmpModelCatalog()
	if err != nil {
		t.Fatalf("GetOmpModelCatalog: %v", err)
	}
	if !strings.Contains(catalog, `"thinkingLevels"`) {
		t.Fatalf("catalog missing thinkingLevels: %s", catalog)
	}
}
