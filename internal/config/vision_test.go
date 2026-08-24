package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// visionTestPath 把导出路径固定到测试临时目录，避免污染真实 ~/.agents。
func visionTestPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "amagi-media-models.json")
}

// fakeKeyResolver 返回固定测试 key（禁止把真实 key 写进测试）。
func fakeKeyResolver(key string) func(string) string {
	return func(string) string { return key }
}

func readVisionExport(t *testing.T, path string) VisionExportFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vision export: %v", err)
	}
	var f VisionExportFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("parse vision export: %v", err)
	}
	return f
}

func openAIProvider() Provider {
	return Provider{
		OpenAI: &OpenAIFormat{
			Enabled: true,
			BaseURL: "http://api.maorun.top/v1",
			AuthKey: "OPENAI_API_KEY",
		},
		DefaultModel: "fallback-model",
	}
}

func anthropicOnlyProvider() Provider {
	return Provider{
		Anthropic: &AnthropicFormat{
			Enabled: true,
			BaseURL: "https://api.anthropic.com",
			AuthKey: "ANTHROPIC_API_KEY",
		},
		DefaultModel: "claude-x",
	}
}

// markedConfig 构造带一个 Vision+Video 标记 preset 的最小 AppConfig。
func markedConfig() *AppConfig {
	return &AppConfig{
		Models: map[string]Provider{"个人版API- Gemini": openAIProvider()},
		TerminalPresets: &TerminalPresetsConfig{
			OpenAI: map[string]TerminalPreset{
				"个人版API- Gemini/gemini-3.7-flash": {
					Name:     "gemini-3.7-flash",
					Provider: "个人版API- Gemini",
					Model:    "gemini-3.7-flash",
					Parameters: Parameters{
						ReasoningEffort: "max",
						MaxTokens:       60000,
						Temperature:     0.7,
						TopP:            0.9,
					},
					Vision:         true,
					Video:          true,
					VisionPriority: 1,
				},
			},
		},
	}
}

func findVisionModel(t *testing.T, f VisionExportFile, id string) VisionExportModel {
	t.Helper()
	for _, m := range f.Models {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("vision model %q not found in %d entries", id, len(f.Models))
	return VisionExportModel{}
}

// 1. 带标记 preset 导出字段正确（契约 §2 全字段）。
func TestExportVisionModels_MarkedPresetFields(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	if err := ExportVisionModels(markedConfig(), fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}

	f := readVisionExport(t, path)
	if f.Version != 1 {
		t.Fatalf("version = %d, want 1", f.Version)
	}
	if _, err := time.Parse(time.RFC3339, f.UpdatedAt); err != nil {
		t.Fatalf("updated_at not RFC3339: %q", f.UpdatedAt)
	}
	if len(f.Models) != 1 {
		t.Fatalf("models = %d, want 1", len(f.Models))
	}
	m := findVisionModel(t, f, "个人版API- Gemini/gemini-3.7-flash")
	if m.Provider != "个人版API- Gemini" || m.Preset != "gemini-3.7-flash" {
		t.Errorf("provider/preset = %q/%q", m.Provider, m.Preset)
	}
	if m.Model != "gemini-3.7-flash" {
		t.Errorf("model = %q", m.Model)
	}
	if m.BaseURL != "http://api.maorun.top/v1" {
		t.Errorf("base_url = %q", m.BaseURL)
	}
	if m.APIKey != "sk-test" {
		t.Errorf("api_key = %q", m.APIKey)
	}
	if m.AuthKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("auth_key_env = %q", m.AuthKeyEnv)
	}
	if m.APIType != "openai" {
		t.Errorf("api_type = %q", m.APIType)
	}
	if len(m.Capabilities) != 2 || m.Capabilities[0] != "image" || m.Capabilities[1] != "video" {
		t.Errorf("capabilities = %v, want [image video]", m.Capabilities)
	}
	if m.Priority != 1 {
		t.Errorf("priority = %d, want 1", m.Priority)
	}
	if m.Parameters == nil ||
		m.Parameters.ReasoningEffort != "max" ||
		m.Parameters.MaxTokens != 60000 ||
		m.Parameters.Temperature != 0.7 ||
		m.Parameters.TopP != 0.9 {
		t.Errorf("parameters = %+v", m.Parameters)
	}

	// 文件权限 0600（契约 §2）。
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", info.Mode().Perm())
	}
}

// 2. priority 0 → 100 归一化；model 留空回退 provider 默认值。
func TestExportVisionModels_PriorityZeroNormalized(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	cfg := &AppConfig{
		Models: map[string]Provider{"p1": openAIProvider()},
		TerminalPresets: &TerminalPresetsConfig{
			OpenAI: map[string]TerminalPreset{
				"p1/flash": {Name: "flash", Provider: "p1", Vision: true},
			},
		},
	}
	if err := ExportVisionModels(cfg, fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}
	f := readVisionExport(t, path)
	m := findVisionModel(t, f, "p1/flash")
	if m.Priority != visionDefaultPriority {
		t.Errorf("priority = %d, want %d", m.Priority, visionDefaultPriority)
	}
	if m.Model != "fallback-model" {
		t.Errorf("model = %q, want provider default %q", m.Model, "fallback-model")
	}
	if len(m.Capabilities) != 1 || m.Capabilities[0] != "image" {
		t.Errorf("capabilities = %v, want [image]", m.Capabilities)
	}
}

// 3. 无标记（甚至无 preset）也写文件，models 为 []，区分「未配置」与「文件缺失」。
func TestExportVisionModels_NoMarksWritesEmptyModels(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	cfg := &AppConfig{
		Models: map[string]Provider{"p1": openAIProvider()},
		TerminalPresets: &TerminalPresetsConfig{
			OpenAI: map[string]TerminalPreset{
				"p1/flash": {Name: "flash", Provider: "p1"}, // 无标记
			},
		},
	}
	if err := ExportVisionModels(cfg, fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}
	f := readVisionExport(t, path)
	if f.Models == nil || len(f.Models) != 0 {
		t.Fatalf("models = %#v, want empty non-nil array", f.Models)
	}
}

// 4. anthropic-only provider 的带标记 preset 跳过（v1 边界）；provider 缺失同理。
func TestExportVisionModels_AnthropicOnlySkipped(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	cfg := &AppConfig{
		Models: map[string]Provider{
			"anth-prov": anthropicOnlyProvider(),
			// "ghost" provider 不存在
		},
		TerminalPresets: &TerminalPresetsConfig{
			OpenAI: map[string]TerminalPreset{
				"anth-prov/claude-vision": {Name: "claude-vision", Provider: "anth-prov", Vision: true, Video: true},
				"ghost/lost":              {Name: "lost", Provider: "ghost", Vision: true},
			},
		},
	}
	if err := ExportVisionModels(cfg, fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}
	f := readVisionExport(t, path)
	if len(f.Models) != 0 {
		t.Fatalf("models = %d entries, want 0 (anthropic-only + missing provider skipped)", len(f.Models))
	}
}

// 标记独立于所在桶：anthropic 桶 preset + OpenAI 兼容 provider 同样导出。
func TestExportVisionModels_AnthropicBucketMarkedExports(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	cfg := &AppConfig{
		Models: map[string]Provider{"dual": openAIProvider()},
		TerminalPresets: &TerminalPresetsConfig{
			Anthropic: map[string]TerminalPreset{
				"dual/cc": {Name: "cc", Provider: "dual", Model: "m1", Vision: true},
			},
		},
	}
	if err := ExportVisionModels(cfg, fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}
	f := readVisionExport(t, path)
	if len(f.Models) != 1 {
		t.Fatalf("models = %d, want 1", len(f.Models))
	}
	if m := findVisionModel(t, f, "dual/cc"); m.BaseURL != "http://api.maorun.top/v1" {
		t.Errorf("base_url = %q", m.BaseURL)
	}
}

// 5+6. AMAGI_MEDIA_MODELS_PATH 覆盖生效（含目录自动创建）。
func TestVisionModelsExportPath_EnvOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "nested", "dir", "v.json")
	t.Setenv(VisionModelsPathEnv, want)
	got, err := VisionModelsExportPath()
	if err != nil {
		t.Fatalf("VisionModelsExportPath: %v", err)
	}
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	cfg := markedConfig()
	if err := ExportVisionModels(cfg, fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("export file not written to overridden path: %v", err)
	}
}

// 7. resolver 拿不到 key：api_key 写空串，auth_key_env 兜底为 provider 的 auth_key 标识。
func TestExportVisionModels_MissingAPIKeyFallback(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	if err := ExportVisionModels(markedConfig(), fakeKeyResolver("")); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}
	f := readVisionExport(t, path)
	m := findVisionModel(t, f, "个人版API- Gemini/gemini-3.7-flash")
	if m.APIKey != "" {
		t.Errorf("api_key = %q, want empty string", m.APIKey)
	}
	if m.AuthKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("auth_key_env = %q, want OPENAI_API_KEY", m.AuthKeyEnv)
	}
}

// resolver 为 nil 时同样导出（api_key 空串 + auth_key_env 兜底）。
func TestExportVisionModels_NilResolver(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	if err := ExportVisionModels(markedConfig(), nil); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}
	f := readVisionExport(t, path)
	if len(f.Models) != 1 {
		t.Fatalf("models = %d, want 1", len(f.Models))
	}
	if m := findVisionModel(t, f, "个人版API- Gemini/gemini-3.7-flash"); m.APIKey != "" {
		t.Errorf("api_key = %q, want empty string", m.APIKey)
	}
}

// 幂等：重复导出覆盖旧内容（Delete 联动的基础）。
func TestExportVisionModels_IdempotentOverwrite(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	if err := ExportVisionModels(markedConfig(), fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("first export: %v", err)
	}
	// 第二次导出空配置 → 旧条目被清除。
	if err := ExportVisionModels(&AppConfig{Models: map[string]Provider{}}, fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("second export: %v", err)
	}
	f := readVisionExport(t, path)
	if len(f.Models) != 0 {
		t.Fatalf("models = %d, want 0 after full re-export", len(f.Models))
	}
}

// Service 联动：注入 resolver 后 SaveTerminalPreset 触发导出，
// DeleteTerminalPreset 后条目消失（models: []）；
// resolver 未注入时（单测环境）跳过写盘。
func TestServiceVisionExportTrigger_SaveAndDelete(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// resolver 未注入：Load 完成也不写盘。
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("export file written without resolver, err = %v", err)
	}

	svc.SetAPIKeyResolver(fakeKeyResolver("sk-test"))

	// provider 保存触发（契约 §2）。
	if err := svc.SaveProvider("个人版API- Gemini", openAIProvider()); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("export file not written after SaveProvider: %v", err)
	}
	if f := readVisionExport(t, path); len(f.Models) != 0 {
		t.Fatalf("models = %d, want 0 before marked preset saved", len(f.Models))
	}

	// 带标记 preset 保存触发。
	marked := TerminalPreset{
		Name: "gemini-3.7-flash", Provider: "个人版API- Gemini", Model: "gemini-3.7-flash",
		Parameters: Parameters{ReasoningEffort: "max", MaxTokens: 60000},
		Vision:     true, Video: true, VisionPriority: 1,
	}
	if err := svc.SaveTerminalPreset("openai", "个人版API- Gemini/gemini-3.7-flash", marked); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	f := readVisionExport(t, path)
	if len(f.Models) != 1 {
		t.Fatalf("models = %d, want 1 after SaveTerminalPreset", len(f.Models))
	}
	m := findVisionModel(t, f, "个人版API- Gemini/gemini-3.7-flash")
	if m.APIKey != "sk-test" || m.Priority != 1 {
		t.Errorf("api_key/priority = %q/%d", m.APIKey, m.Priority)
	}

	// 删除 preset → 条目消失。
	if err := svc.DeleteTerminalPreset("openai", "个人版API- Gemini/gemini-3.7-flash"); err != nil {
		t.Fatalf("DeleteTerminalPreset: %v", err)
	}
	f = readVisionExport(t, path)
	if len(f.Models) != 0 {
		t.Fatalf("models = %d, want 0 after DeleteTerminalPreset", len(f.Models))
	}

	// DeleteProvider 同样触发（provider 没了，无带标记条目可导出）。
	if err := svc.SaveTerminalPreset("openai", "个人版API- Gemini/again", marked); err != nil {
		t.Fatalf("SaveTerminalPreset(again): %v", err)
	}
	if f := readVisionExport(t, path); len(f.Models) != 1 {
		t.Fatalf("models = %d, want 1 after re-save", len(f.Models))
	}
	if err := svc.DeleteProvider("个人版API- Gemini"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	f = readVisionExport(t, path)
	if len(f.Models) != 0 {
		t.Fatalf("models = %d, want 0 after DeleteProvider (marked preset unresolvable)", len(f.Models))
	}
}

// 导出内容不落盘到 models.json（密钥只进 0600 的 amagi-media-models.json）。
func TestVisionExportDoesNotTouchModelsJSON(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc.SetAPIKeyResolver(fakeKeyResolver("sk-test-secret"))
	if err := svc.SaveProvider("p1", openAIProvider()); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := svc.SaveTerminalPreset("openai", "p1/flash", TerminalPreset{
		Name: "flash", Provider: "p1", Vision: true,
	}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}
	if strings.Contains(string(b), "sk-test-secret") {
		t.Fatal("fake key leaked into models.json")
	}
	var stored AppConfig
	if err := json.Unmarshal(b, &stored); err != nil {
		t.Fatalf("parse models.json: %v", err)
	}
	tp := stored.TerminalPresets.GetMap(TerminalPresetOpenAI)["p1/flash"]
	if !tp.Vision || tp.VisionPriority != 0 {
		t.Errorf("stored preset vision/priority = %v/%d, want true/0", tp.Vision, tp.VisionPriority)
	}
}

// 5. 自动发现（契约 §2 v1.1）：未手动标记但模型 id 命中已知多模态模型族的
// preset 自动导出，capabilities 来自推断；手动标记与推断取并集。
func TestExportVisionModels_AutoDiscoveryIncludesUnmarkedPreset(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	cfg := &AppConfig{
		Models: map[string]Provider{"kimi": openAIProvider()},
		TerminalPresets: &TerminalPresetsConfig{
			OpenAI: map[string]TerminalPreset{
				// 未标记，但 k3 是已知多模态模型族 → 自动收录 image。
				"kimi/k3": {Name: "k3", Provider: "kimi", Model: "k3"},
				// 未标记，gemini 族推断 image+video。
				"kimi/gemini": {Name: "gemini", Provider: "kimi", Model: "gemini-3.7-flash"},
				// 手动 video + 推断 vision → 并集 image+video。
				"kimi/k3-video": {Name: "k3-video", Provider: "kimi", Model: "k3", Video: true},
				// 未标记且未知族 → 不导出（负向）。
				"kimi/plain": {Name: "plain", Provider: "kimi", Model: "acme-text-9"},
			},
		},
	}
	if err := ExportVisionModels(cfg, fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}
	f := readVisionExport(t, path)
	if len(f.Models) != 3 {
		t.Fatalf("models = %d, want 3 (auto-discovered + marked; unknown excluded): %v", len(f.Models), f.Models)
	}

	k3 := findVisionModel(t, f, "kimi/k3")
	if len(k3.Capabilities) != 1 || k3.Capabilities[0] != "image" {
		t.Errorf("kimi/k3 capabilities = %v, want [image] (auto-discovered)", k3.Capabilities)
	}
	gemini := findVisionModel(t, f, "kimi/gemini")
	if len(gemini.Capabilities) != 2 || gemini.Capabilities[0] != "image" || gemini.Capabilities[1] != "video" {
		t.Errorf("kimi/gemini capabilities = %v, want [image video] (auto-discovered)", gemini.Capabilities)
	}
	union := findVisionModel(t, f, "kimi/k3-video")
	if len(union.Capabilities) != 2 || union.Capabilities[0] != "image" || union.Capabilities[1] != "video" {
		t.Errorf("kimi/k3-video capabilities = %v, want [image video] (manual video ∪ inferred vision)", union.Capabilities)
	}
	// 自动收录条目的字段完整性与手动标记一致（base_url / auth_key_env / api_type）。
	if k3.BaseURL != "http://api.maorun.top/v1" || k3.AuthKeyEnv != "OPENAI_API_KEY" || k3.APIType != "openai" {
		t.Errorf("auto-discovered entry lost endpoint fields: %+v", k3)
	}
}
