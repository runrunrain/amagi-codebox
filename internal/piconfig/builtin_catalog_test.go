package piconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 内置目录（models-store.json）合并进下拉目录的回归测试。
func TestBuiltinCatalogMerge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	s := NewService()

	modelsFixture := `{"providers":{"relay":{"api":"openai-completions","apiKey":"sk-inline","models":[{"id":"m1"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsFixture), 0o600); err != nil {
		t.Fatalf("write models fixture: %v", err)
	}
	// 内置目录：openai-codex（OAuth 登录）+ relay（与自定义重名，应被自定义覆盖）
	storeFixture := `{"openai-codex":{"models":[{"id":"gpt-5.6-sol","name":"GPT-5.6 Sol","reasoning":true,"thinkingLevelMap":{"high":"high","max":"max"}},{"id":"gpt-5.3-codex-spark","reasoning":true}]},"relay":{"models":[{"id":"builtin-only-model"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "models-store.json"), []byte(storeFixture), 0o600); err != nil {
		t.Fatalf("write store fixture: %v", err)
	}
	authFixture := `{"openai-codex":{"type":"oauth","access":"a","refresh":"r","expires":1}}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(authFixture), 0o600); err != nil {
		t.Fatalf("write auth fixture: %v", err)
	}

	catalog, err := s.GetPiModelCatalog()
	if err != nil {
		t.Fatalf("GetPiModelCatalog: %v", err)
	}
	var cat struct {
		Providers []struct {
			Name    string `json:"name"`
			Source  string `json:"source"`
			HasAuth bool   `json:"hasAuth"`
			Models  []struct {
				ID             string   `json:"id"`
				ThinkingLevels []string `json:"thinkingLevels"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(catalog), &cat); err != nil {
		t.Fatalf("catalog unmarshal: %v", err)
	}
	byName := map[string]int{}
	for i, p := range cat.Providers {
		byName[p.Name] = i
	}

	// 内置 openai-codex：source=builtin、带 OAuth 认证状态、模型列表完整
	idx, ok := byName["openai-codex"]
	if !ok {
		t.Fatalf("builtin openai-codex missing:\n%s", catalog)
	}
	p := cat.Providers[idx]
	if p.Source != "builtin" || !p.HasAuth {
		t.Fatalf("openai-codex source/hasAuth wrong: %+v", p)
	}
	if len(p.Models) != 2 || p.Models[0].ID != "gpt-5.6-sol" {
		t.Fatalf("openai-codex models wrong: %+v", p.Models)
	}
	if len(p.Models[0].ThinkingLevels) != 6 {
		t.Fatalf("thinkingLevels 应为 pi 支持集（标准档默认支持+max 显式，无 xhigh）: %+v", p.Models[0])
	}
	byLevel := map[string]bool{}
	for _, l := range p.Models[0].ThinkingLevels {
		byLevel[l] = true
	}
	for _, want := range []string{"off", "minimal", "low", "medium", "high", "max"} {
		if !byLevel[want] {
			t.Fatalf("档位 %s 缺席: %+v", want, p.Models[0].ThinkingLevels)
		}
	}
	if byLevel["xhigh"] {
		t.Fatal("xhigh 无显式映射不应出现（pi 语义：扩展档需显式声明）")
	}

	// 与内置重名的 relay：自定义优先，不重复出现且无 builtin 标记
	relayCount := 0
	for _, pp := range cat.Providers {
		if pp.Name == "relay" {
			relayCount++
			if pp.Source == "builtin" || len(pp.Models) != 1 || pp.Models[0].ID != "m1" {
				t.Fatalf("relay should stay custom-only: %+v", pp)
			}
		}
	}
	if relayCount != 1 {
		t.Fatalf("relay duplicated: %d entries", relayCount)
	}
}
