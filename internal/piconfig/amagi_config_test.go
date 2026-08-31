package piconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// amagi.json 读写与并发限制（concurrency）配置透传的回归测试。
func TestAmagiConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	s := NewService()

	// 1. 文件缺失：返回默认骨架
	content, err := s.GetAmagiConfig()
	if err != nil {
		t.Fatalf("GetAmagiConfig default: %v", err)
	}
	if !strings.Contains(content, "profile") || !strings.Contains(content, "tiered") {
		t.Fatalf("default skeleton unexpected: %.100s", content)
	}

	// 2. 保存包含 concurrency（default / providers / models）的合法配置
	fixture := `{
  "$schema": "https://raw.githubusercontent.com/runrunrain/amagi-pi/main/schemas/amagi-config.json",
  "profile": "tiered",
  "agents": {
    "baize": { "model": "anthropic/claude-3-7-sonnet:high" }
  },
  "mcp": {
    "default": ["web-search-prime"],
    "agents": {}
  },
  "concurrency": {
    "default": 4,
    "providers": {
      "openrouter": 8,
      "deepseek": 2
    },
    "models": {
      "anthropic/claude-3-7-sonnet": 3
    }
  }
}`
	if err := s.SaveAmagiConfig(fixture); err != nil {
		t.Fatalf("SaveAmagiConfig: %v", err)
	}

	// 3. 权限 0600
	info, err := os.Stat(filepath.Join(dir, "amagi.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("want 0600, got %v", info.Mode().Perm())
	}

	// 4. 回读格式化且 concurrency 字段完整保留
	got, err := s.GetAmagiConfig()
	if err != nil {
		t.Fatalf("GetAmagiConfig: %v", err)
	}

	var root struct {
		Profile     string                       `json:"profile"`
		Agents      map[string]map[string]string `json:"agents"`
		MCP         struct {
			Default []string `json:"default"`
		} `json:"mcp"`
		Concurrency struct {
			Default   int            `json:"default"`
			Providers map[string]int `json:"providers"`
			Models    map[string]int `json:"models"`
		} `json:"concurrency"`
	}
	if err := json.Unmarshal([]byte(got), &root); err != nil {
		t.Fatalf("unmarshal got amagi config: %v\ncontent: %s", err, got)
	}

	if root.Concurrency.Default != 4 {
		t.Errorf("concurrency.default: want 4, got %d", root.Concurrency.Default)
	}
	if root.Concurrency.Providers["openrouter"] != 8 || root.Concurrency.Providers["deepseek"] != 2 {
		t.Errorf("concurrency.providers mismatch: %+v", root.Concurrency.Providers)
	}
	if root.Concurrency.Models["anthropic/claude-3-7-sonnet"] != 3 {
		t.Errorf("concurrency.models mismatch: %+v", root.Concurrency.Models)
	}

	// 5. 非法 JSON 拒绝
	if err := s.SaveAmagiConfig("{invalid"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	// 6. 非对象根拒绝
	if err := s.SaveAmagiConfig("[1, 2, 3]"); err == nil {
		t.Fatal("expected error for array root")
	}
}
