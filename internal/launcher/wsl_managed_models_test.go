package launcher

// wsl_managed_models_test.go — BuildWSLPiManagedModelsCandidate 的行为钉子
//（P3-S1 验收 2/3：非 amagi 保留、两级损坏保护、构建失败保留、登录窄字段）。

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func parseJSONMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse json: %v\n%s", err, b)
	}
	return m
}

func TestBuildWSLPiManagedModelsCandidateMerge(t *testing.T) {
	existing := `{
  "providers": {
    "user-custom": {"baseUrl": "https://user.example", "api": "anthropic-messages"},
    "amagi-gone": {"baseUrl": "https://gone.example"},
    "amagi-keep": {"baseUrl": "https://old.example"}
  },
  "equivalence": {"note": "keep-me"}
}`
	// amagi-keep 是「本轮构建失败」的 retain 提供者：不在期望集内（业务源
	// 不会同时把失败者放进 Providers），其既有条目原样保留。
	desired := map[string]any{
		"providers": map[string]any{
			"amagi-new": map[string]any{"baseUrl": "https://new.example"},
		},
	}
	out, err := BuildWSLPiManagedModelsCandidate([]byte(existing), desired, []string{"amagi-keep"}, nil)
	if err != nil {
		t.Fatalf("build candidate: %v", err)
	}
	got := parseJSONMap(t, out)
	providers := got["providers"].(map[string]any)
	if _, ok := providers["user-custom"]; !ok {
		t.Fatal("non-amagi provider must be preserved")
	}
	if _, ok := providers["amagi-gone"]; ok {
		t.Fatal("removed provider (not retained) must be pruned from managed namespace")
	}
	if providers["amagi-keep"].(map[string]any)["baseUrl"] != "https://old.example" {
		t.Fatal("failed-build provider (retainIDs) must keep its existing entry, not be pruned nor replaced")
	}
	if providers["amagi-new"].(map[string]any)["baseUrl"] != "https://new.example" {
		t.Fatal("desired provider must be written")
	}
	if got["equivalence"].(map[string]any)["note"] != "keep-me" {
		t.Fatal("unknown top-level fields must be preserved")
	}
	if !strings.HasSuffix(string(out), "\n") || !strings.Contains(string(out), "\n  \"providers\"") {
		t.Fatalf("serialization must match WritePiAgentConfig format (indent 2 + trailing newline): %q", string(out)[:40])
	}
}

func TestBuildWSLPiManagedModelsCandidateEmptyExisting(t *testing.T) {
	// 目标缺失（nil existing）：全新建，仅含期望集。
	out, err := BuildWSLPiManagedModelsCandidate(nil, map[string]any{
		"providers": map[string]any{"amagi-x": map[string]any{"baseUrl": "https://x"}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := parseJSONMap(t, out)
	if _, ok := got["providers"].(map[string]any)["amagi-x"]; !ok {
		t.Fatal("fresh target must contain desired providers")
	}
}

func TestBuildWSLPiManagedModelsCandidateCorruptRefused(t *testing.T) {
	for name, content := range map[string]string{
		"syntax corrupt":  `{"providers": {"a": 1`,
		"empty file":      "   ",
		"root not object": `["providers"]`,
		"providers shape": `{"providers": ["not", "object"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := BuildWSLPiManagedModelsCandidate([]byte(content), map[string]any{
				"providers": map[string]any{"amagi-x": map[string]any{}},
			}, nil, nil)
			if err == nil {
				t.Fatal("corrupt existing content must be refused")
			}
			if !errors.Is(err, ErrWSLManagedModelsCorrupt) {
				t.Fatalf("error must wrap ErrWSLManagedModelsCorrupt, got %v", err)
			}
		})
	}
}

func TestBuildWSLPiManagedModelsCandidateLoginOverrides(t *testing.T) {
	existing := `{"providers": {"openai": {"baseUrl": "https://builtin", "models": {"gpt-x": {}}},
  "other": {"modelOverrides": {"m1": {"compat": {"keep": true}}}}}}`
	out, err := BuildWSLPiManagedModelsCandidate([]byte(existing), map[string]any{}, nil, []PiLoginModelOverride{
		{Provider: "openai", Model: "gpt-5.2", ContextWindow: 400000},
		{Provider: "other", Model: "m1", ContextWindow: 256000},
		{Provider: "openai", Model: "ignored", ContextWindow: 0}, // cw<=0：跳过（补齐语义）
	})
	if err != nil {
		t.Fatal(err)
	}
	providers := parseJSONMap(t, out)["providers"].(map[string]any)

	openai := providers["openai"].(map[string]any)
	if openai["baseUrl"] != "https://builtin" {
		t.Fatal("builtin provider siblings must be preserved")
	}
	if _, ok := openai["models"]; !ok {
		t.Fatal("builtin provider models must be preserved")
	}
	ov := openai["modelOverrides"].(map[string]any)["gpt-5.2"].(map[string]any)
	if ov["contextWindow"] != float64(400000) {
		t.Fatalf("contextWindow = %v, want 400000", ov["contextWindow"])
	}

	other := providers["other"].(map[string]any)
	m1 := other["modelOverrides"].(map[string]any)["m1"].(map[string]any)
	if m1["contextWindow"] != float64(256000) {
		t.Fatalf("existing override key contextWindow = %v", m1["contextWindow"])
	}
	if _, ok := m1["compat"]; !ok {
		t.Fatal("sibling fields in the same override key must be preserved")
	}
	if _, ok := other["modelOverrides"].(map[string]any)["ignored"]; ok {
		t.Fatal("cw<=0 override must not be written (S1 fill-in semantics)")
	}
}

func TestBuildWSLPiManagedModelsCandidateNestedShapeCorrupt(t *testing.T) {
	existing := `{"providers": {"openai": {"modelOverrides": "not-an-object"}}}`
	_, err := BuildWSLPiManagedModelsCandidate([]byte(existing), map[string]any{}, nil, []PiLoginModelOverride{
		{Provider: "openai", Model: "m", ContextWindow: 1},
	})
	if err == nil || !errors.Is(err, ErrWSLManagedModelsCorrupt) {
		t.Fatalf("nested shape corruption must be refused: %v", err)
	}
}
