package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeOverrideFixture 写入初始 models.json（损坏态传 raw 非法 JSON）。
func writeOverrideFixture(t *testing.T, agentDir, raw string) {
	t.Helper()
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readOverrideProviders(t *testing.T, agentDir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(agentDir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return root["providers"].(map[string]any)
}

func overrideEntry(t *testing.T, providers map[string]any, provider, model string) map[string]any {
	t.Helper()
	entry := providers[provider].(map[string]any)
	overrides := entry["modelOverrides"].(map[string]any)
	return overrides[model].(map[string]any)
}

// 登录 contextWindow 覆写：全新 agent root 创建 provider.modelOverrides。
func TestUpsertPiLoginModelOverrideCreatesEntry(t *testing.T) {
	dir := t.TempDir()
	if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 400000); err != nil {
		t.Fatal(err)
	}
	entry := overrideEntry(t, readOverrideProviders(t, dir), "zai", "glm-5.2")
	if got := int(entry["contextWindow"].(float64)); got != 400000 {
		t.Fatalf("contextWindow = %d, want 400000", got)
	}
}

// 深合并保留：兄弟 provider、目标 provider 的 baseUrl/models、modelOverrides
// 兄弟键与同键其它字段（compat）全部不动，只写 contextWindow。
func TestUpsertPiLoginModelOverridePreservesSiblings(t *testing.T) {
	dir := t.TempDir()
	writeOverrideFixture(t, dir, `{
  "topLevel": {"keep": true},
  "providers": {
    "amagi-glm": {
      "baseUrl": "https://glm.example",
      "api": "openai-completions",
      "models": [{"id": "glm-5.3", "contextWindow": 400000}]
    },
    "anthropic": {
      "modelOverrides": {
        "claude-sonnet-5": {"compat": {"supportsStrictTools": false}},
        "claude-opus-5-5": {"compat": {"supportsStrictTools": false}}
      }
    }
  }
}`)
	if err := UpsertPiLoginModelOverride(dir, "anthropic", "claude-sonnet-5", 300000); err != nil {
		t.Fatal(err)
	}
	providers := readOverrideProviders(t, dir)

	rootRaw, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	var root map[string]any
	_ = json.Unmarshal(rootRaw, &root)
	if _, ok := root["topLevel"]; !ok {
		t.Fatal("top-level sibling field lost")
	}
	amagi := providers["amagi-glm"].(map[string]any)
	if amagi["baseUrl"] != "https://glm.example" {
		t.Fatal("sibling provider content lost")
	}
	entry := overrideEntry(t, providers, "anthropic", "claude-sonnet-5")
	if int(entry["contextWindow"].(float64)) != 300000 {
		t.Fatalf("contextWindow = %v, want 300000", entry["contextWindow"])
	}
	compat, _ := entry["compat"].(map[string]any)
	if compat == nil || compat["supportsStrictTools"] != false {
		t.Fatal("same-key sibling field (compat) lost")
	}
	brother := overrideEntry(t, providers, "anthropic", "claude-opus-5-5")
	if _, exists := brother["contextWindow"]; exists {
		t.Fatal("sibling model override unexpectedly modified")
	}
}

// 幂等：同值重写不落盘（文件不变与 mtime 未变）；换值更新。
func TestUpsertPiLoginModelOverrideIdempotentAndUpdate(t *testing.T) {
	dir := t.TempDir()
	if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 400000); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "models.json")
	before, _ := os.ReadFile(path)
	beforeInfo, _ := os.Stat(path)
	// mtime 分辨率兜底：不同文件系统粒度不一，同一纳秒内两次写盘可能同 mtime；
	// 以内容不变 + 再次调用成功为下限断言，mtime 仅在可分辨时增强。
	if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 400000); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	afterInfo, _ := os.Stat(path)
	if string(before) != string(after) {
		t.Fatal("same-value upsert rewrote the file")
	}
	if beforeInfo.ModTime() != afterInfo.ModTime() {
		t.Fatal("same-value upsert refreshed file mtime")
	}
	if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 256000); err != nil {
		t.Fatal(err)
	}
	if got := int(overrideEntry(t, readOverrideProviders(t, dir), "zai", "glm-5.2")["contextWindow"].(float64)); got != 256000 {
		t.Fatalf("contextWindow = %d, want 256000", got)
	}
}

// cw<=0 清除：同键其它字段保留；无字段的键连带清理；目标键清除后剩余 compat 保留。
func TestUpsertPiLoginModelOverrideRemoval(t *testing.T) {
	dir := t.TempDir()
	writeOverrideFixture(t, dir, `{
  "providers": {
    "zai": {
      "modelOverrides": {
        "glm-5.2": {"contextWindow": 400000, "compat": {"supportsStrictTools": false}},
        "glm-5": {"contextWindow": 200000}
      }
    }
  }
}`)
	if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5", 0); err != nil {
		t.Fatal(err)
	}
	providers := readOverrideProviders(t, dir)
	entry := providers["zai"].(map[string]any)
	overrides := entry["modelOverrides"].(map[string]any)
	if _, exists := overrides["glm-5"]; exists {
		t.Fatal("empty override key should be pruned")
	}
	kept := overrides["glm-5.2"].(map[string]any)
	if int(kept["contextWindow"].(float64)) != 400000 {
		t.Fatal("sibling key's contextWindow must survive (only the target key is touched)")
	}

	// 目标键清除 contextWindow 后剩余字段保留，modelOverrides 非空不清。
	if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 0); err != nil {
		t.Fatal(err)
	}
	providers = readOverrideProviders(t, dir)
	overrides = providers["zai"].(map[string]any)["modelOverrides"].(map[string]any)
	if _, exists := overrides["glm-5.2"]; !exists {
		t.Fatal("key with remaining fields must survive")
	}
	if _, exists := overrides["glm-5.2"].(map[string]any)["contextWindow"]; exists {
		t.Fatal("contextWindow should be removed for cw<=0 preset")
	}
	if _, exists := overrides["glm-5.2"].(map[string]any)["compat"]; !exists {
		t.Fatal("sibling field (compat) in the same key should survive removal")
	}
}

// 全链清空：仅剩目标键且无其它字段时，cw<=0 连 provider 键一起清理——
// pi 对空 provider overlay（无 baseUrl/models/compat/modelOverrides）报
// composition error，恢复内置窗口不应留下触发诊断错误的空壳。
func TestUpsertPiLoginModelOverrideRemovalPrunesEmptyOverrides(t *testing.T) {
	dir := t.TempDir()
	writeOverrideFixture(t, dir, `{"providers":{"zai":{"modelOverrides":{"glm-5.2":{"contextWindow":400000}}}}}`)
	if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 0); err != nil {
		t.Fatal(err)
	}
	providers := readOverrideProviders(t, dir)
	if _, exists := providers["zai"]; exists {
		t.Fatalf("empty provider entry should be pruned, got %v", providers["zai"])
	}
}

// provider 有其它字段时清除覆写不删 provider（保留 baseUrl 等）。
func TestUpsertPiLoginModelOverrideRemovalKeepsNonEmptyProvider(t *testing.T) {
	dir := t.TempDir()
	writeOverrideFixture(t, dir, `{"providers":{"zai":{"baseUrl":"https://zai.example","modelOverrides":{"glm-5.2":{"contextWindow":400000}}}}}`)
	if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 0); err != nil {
		t.Fatal(err)
	}
	entry := readOverrideProviders(t, dir)["zai"].(map[string]any)
	if entry["baseUrl"] != "https://zai.example" {
		t.Fatal("non-empty provider must survive override removal")
	}
	if _, exists := entry["modelOverrides"]; exists {
		t.Fatal("empty modelOverrides should be pruned")
	}
}

// 清除路径幂等：键存在但无 contextWindow 字段（仅 compat）时 cw<=0 不写盘。
func TestUpsertPiLoginModelOverrideRemovalIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeOverrideFixture(t, dir, `{"providers":{"zai":{"modelOverrides":{"glm-5.2":{"compat":{"supportsStrictTools":false}}}}}}`)
	path := filepath.Join(dir, "models.json")
	beforeInfo, _ := os.Stat(path)
	before, _ := os.ReadFile(path)
	if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 0); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	afterInfo, _ := os.Stat(path)
	if string(before) != string(after) {
		t.Fatal("no-op removal rewrote the file")
	}
	if beforeInfo.ModTime() != afterInfo.ModTime() {
		t.Fatal("no-op removal refreshed file mtime")
	}
}

// cw<=0 且无既有覆写：文件不动（原本无文件时也不创建）。
func TestUpsertPiLoginModelOverrideNoop(t *testing.T) {
	dir := t.TempDir()
	if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "models.json")); !os.IsNotExist(err) {
		t.Fatal("noop upsert must not create models.json")
	}
}

// JSON 语法损坏：报错且不覆写（不静默重置用户文件）。
func TestUpsertPiLoginModelOverrideCorruptFileFails(t *testing.T) {
	dir := t.TempDir()
	writeOverrideFixture(t, dir, `{ not json`)
	err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 400000)
	if err == nil {
		t.Fatal("expected error on corrupt models.json")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if string(raw) != `{ not json` {
		t.Fatal("corrupt file must not be overwritten")
	}
}

// 嵌套结构损坏（语法合法但类型错误）：目标路径上任一层非对象时报错不覆写，
// 而非静默替换用户手写内容。
func TestUpsertPiLoginModelOverrideNestedShapeCorruptionFails(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"provider not object", `{"providers":{"zai":["manual"]}}`},
		{"modelOverrides not object", `{"providers":{"zai":{"modelOverrides":[{"keep":"manual"}]}}}`},
		{"model key not object", `{"providers":{"zai":{"modelOverrides":{"glm-5.2":["manual"]}}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeOverrideFixture(t, dir, tc.raw)
			if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 300000); err == nil {
				t.Fatal("expected error on nested shape corruption")
			}
			if err := UpsertPiLoginModelOverride(dir, "zai", "glm-5.2", 0); err == nil {
				t.Fatal("expected error on nested shape corruption (removal path)")
			}
			raw, _ := os.ReadFile(filepath.Join(dir, "models.json"))
			if string(raw) != tc.raw {
				t.Fatalf("corrupt content silently replaced: %s", raw)
			}
		})
	}
}

// 参数校验。
func TestUpsertPiLoginModelOverrideArgumentValidation(t *testing.T) {
	dir := t.TempDir()
	if err := UpsertPiLoginModelOverride("", "zai", "m", 1); err == nil {
		t.Fatal("expected error for empty agentDir")
	}
	if err := UpsertPiLoginModelOverride(dir, " ", "m", 1); err == nil {
		t.Fatal("expected error for empty provider")
	}
	if err := UpsertPiLoginModelOverride(dir, "zai", "", 1); err == nil {
		t.Fatal("expected error for empty model")
	}
}
