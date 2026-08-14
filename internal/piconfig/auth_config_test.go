package piconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auth.json 读写与目录认证状态标注的回归测试（临时目录，不碰真实文件）。
func TestAuthConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	s := NewService()

	// 文件缺失：返回空对象骨架
	content, err := s.GetAuthConfig()
	if err != nil {
		t.Fatalf("GetAuthConfig default: %v", err)
	}
	if strings.TrimSpace(content) != "{\n}" {
		t.Fatalf("default skeleton unexpected: %q", content)
	}

	// 写入凭据 + 注册表，验证目录 hasAuth 标注
	authFixture := `{"relay":{"type":"api_key","key":"sk-abc"},"openai-codex":{"type":"oauth","access":"a","refresh":"r","expires":1755000000000,"accountId":"acc1"}}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(authFixture), 0o600); err != nil {
		t.Fatalf("write auth fixture: %v", err)
	}
	modelsFixture := `{"providers":{"relay":{"api":"openai-completions","models":[{"id":"m1"}]},"inline-key":{"api":"openai-completions","apiKey":"sk-inline","models":[{"id":"m2"}]},"no-auth":{"api":"openai-completions","models":[{"id":"m3"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsFixture), 0o600); err != nil {
		t.Fatalf("write models fixture: %v", err)
	}

	catalog, err := s.GetPiModelCatalog()
	if err != nil {
		t.Fatalf("GetPiModelCatalog: %v", err)
	}
	var cat struct {
		Providers []struct {
			Name    string `json:"name"`
			HasAuth bool   `json:"hasAuth"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(catalog), &cat); err != nil {
		t.Fatalf("catalog unmarshal: %v", err)
	}
	hasAuthByName := map[string]bool{}
	for _, p := range cat.Providers {
		hasAuthByName[p.Name] = p.HasAuth
	}
	// hasAuth 断言：relay(auth.json)=true, inline-key(内联 apiKey)=true, no-auth=false
	if !hasAuthByName["relay"] {
		t.Fatalf("relay should have hasAuth=true:\n%s", catalog)
	}
	if !hasAuthByName["inline-key"] {
		t.Fatalf("inline-key should have hasAuth=true:\n%s", catalog)
	}
	if hasAuthByName["no-auth"] {
		t.Fatalf("no-auth should have hasAuth=false:\n%s", catalog)
	}
	// 目录不得泄露凭据内容
	if strings.Contains(catalog, "sk-abc") || strings.Contains(catalog, "sk-inline") || strings.Contains(catalog, `"refresh"`) {
		t.Fatalf("catalog leaked credentials:\n%s", catalog)
	}

	// 读写往返 + 0600 权限
	got, err := s.GetAuthConfig()
	if err != nil {
		t.Fatalf("GetAuthConfig: %v", err)
	}
	if !strings.Contains(got, "sk-abc") || !strings.Contains(got, "accountId") {
		t.Fatalf("auth round-trip lost fields:\n%s", got)
	}
	if err := s.SaveAuthConfig(got); err != nil {
		t.Fatalf("SaveAuthConfig: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("want 0600, got %v", info.Mode().Perm())
	}

	// 非法输入拒绝
	if err := s.SaveAuthConfig("{invalid"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if err := s.SaveAuthConfig("[]"); err == nil {
		t.Fatal("expected error for array root")
	}
}
