package piconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 临时冒烟测试（验证后删除）：用临时 agent 目录验证 models.json 注册表读写。
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

	// 保存合法配置
	fixture := `{"providers":{"amagi-test":{"api":"openai-completions","apiKey":"sk-x","baseUrl":"https://x/v1","models":[{"id":"m1","reasoning":true,"thinkingLevelMap":{"high":"high"}}]}}}`
	if err := s.SaveModelsConfig(fixture); err != nil {
		t.Fatalf("SaveModelsConfig: %v", err)
	}

	// 权限 0600
	info, err := os.Stat(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("want 0600, got %v", info.Mode().Perm())
	}

	// 回读格式化且内容保留
	got, err := s.GetModelsConfig()
	if err != nil {
		t.Fatalf("GetModelsConfig: %v", err)
	}
	for _, want := range []string{"amagi-test", "sk-x", "https://x/v1", "m1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("round-trip lost %q in:\n%s", want, got)
		}
	}

	// 非法 JSON 拒绝
	if err := s.SaveModelsConfig("{invalid"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	// 非对象根拒绝
	if err := s.SaveModelsConfig("[1,2]"); err == nil {
		t.Fatal("expected error for array root")
	}

	// 目录：从同一注册表抽取（含 thinkingLevels）
	catalog, err := s.GetPiModelCatalog()
	if err != nil {
		t.Fatalf("GetPiModelCatalog: %v", err)
	}
	if !strings.Contains(catalog, `"thinkingLevels"`) {
		t.Fatalf("catalog missing thinkingLevels: %s", catalog)
	}
}
