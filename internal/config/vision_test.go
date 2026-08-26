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

// 5. 收录回归手动标记（契约 §2 v1.4）：未手动标记的 preset 即使模型 id
// 命中知识库多模态模型族、或探测缓存已实证，也不导出；capabilities 精确
// 等于手动标记（不做并集扩充）。
func TestExportVisionModels_UnmarkedPresetNotExported(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	cfg := &AppConfig{
		Models: map[string]Provider{"kimi": openAIProvider()},
		TerminalPresets: &TerminalPresetsConfig{
			OpenAI: map[string]TerminalPreset{
				// 未标记，但 k3 是已知多模态模型族 → v1.4 不导出。
				"kimi/k3": {Name: "k3", Provider: "kimi", Model: "k3"},
				// 未标记，gemini 族推断 image+video → 同样不导出。
				"kimi/gemini": {Name: "gemini", Provider: "kimi", Model: "gemini-3.7-flash"},
				// 手动 video（未标 vision）→ 导出且仅 [video]，不与知识库并集。
				"kimi/k3-video": {Name: "k3-video", Provider: "kimi", Model: "k3", Video: true},
				// 探测缓存已实证 vision=true 但未手动标记 → 同样不导出。
				"kimi/probed": {Name: "probed", Provider: "kimi", Model: "acme-text-9"},
			},
		},
		ModalityProbe: map[string]ModalityProbeEntry{
			"kimi/acme-text-9": {Vision: true, Source: "image-probe"},
		},
	}
	if err := ExportVisionModels(cfg, fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}
	f := readVisionExport(t, path)
	if len(f.Models) != 1 {
		t.Fatalf("models = %d, want 1 (manual-only; auto/probed/unknown excluded): %v", len(f.Models), f.Models)
	}

	manual := findVisionModel(t, f, "kimi/k3-video")
	if len(manual.Capabilities) != 1 || manual.Capabilities[0] != "video" {
		t.Errorf("kimi/k3-video capabilities = %v, want [video] (manual mark only, no KB union)", manual.Capabilities)
	}
	// 手动标记条目的字段完整性不变（base_url / auth_key_env / api_type）。
	if manual.BaseURL != "http://api.maorun.top/v1" || manual.AuthKeyEnv != "OPENAI_API_KEY" || manual.APIType != "openai" {
		t.Errorf("manual entry lost endpoint fields: %+v", manual)
	}
}

// 5b. 跨桶去重（契约 §2 v1.4）：同一 provider/短名在 anthropic 与 openai
// 两桶同时标记时仅导出一条，openai 桶条目（api_type=openai 语义）胜出。
func TestExportVisionModels_DuplicateIDAcrossBucketsDeduped(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	cfg := &AppConfig{
		Models: map[string]Provider{"dual": openAIProvider()},
		TerminalPresets: &TerminalPresetsConfig{
			Anthropic: map[string]TerminalPreset{
				// anthropic 桶同名条目：无参数，不应胜出。
				"dual/vision": {Name: "vision", Provider: "dual", Model: "gemini-3.7-flash", Vision: true},
			},
			OpenAI: map[string]TerminalPreset{
				"dual/vision": {Name: "vision", Provider: "dual", Model: "gemini-3.7-flash", Vision: true,
					Parameters: Parameters{ReasoningEffort: "max"}},
			},
		},
	}
	if err := ExportVisionModels(cfg, fakeKeyResolver("sk-test")); err != nil {
		t.Fatalf("ExportVisionModels: %v", err)
	}
	f := readVisionExport(t, path)
	if len(f.Models) != 1 {
		t.Fatalf("models = %d, want 1 (cross-bucket duplicate deduped): %v", len(f.Models), f.Models)
	}
	m := f.Models[0]
	if m.ID != "dual/vision" {
		t.Fatalf("id = %q, want dual/vision", m.ID)
	}
	if m.Parameters == nil || m.Parameters.ReasoningEffort != "max" {
		t.Errorf("dedup kept wrong bucket entry: %+v (want openai-bucket with reasoning_effort=max)", m)
	}
}

// 回归（密钥面安全）：启动顺序 Config.Load（尾部即触发首轮导出）先于
// Secrets.Load 就绪。复现真实接线顺序——resolver/probe 注入先于 Load：门禁
// （SetAPIKeyReadyProbe 返回 false）期间 Load/SaveProvider/SaveTerminalPreset/
// ReexportVisionModels 的所有导出触发都不得写盘，防止用空密钥缓存把带真实
// key 的 ~/.agents/amagi-media-models.json 覆盖成无 api_key 版本；就绪后
// ReexportVisionModels 补发正确产物并自愈历史文件。
func TestServiceVisionExport_SecretsNotReadyGate(t *testing.T) {
	path := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, path)

	// 预置一份带 key 的“历史正确导出”，模拟用户已有文件，先于一切服务操作。
	if err := ExportVisionModels(markedConfig(), fakeKeyResolver("sk-real-key")); err != nil {
		t.Fatalf("seed export: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seeded export: %v", err)
	}
	assertUnchanged := func(stage string) {
		t.Helper()
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read export after %s: %v", stage, err)
		}
		if string(after) != string(before) {
			t.Fatalf("export file overwritten at %s while secrets not ready (would wipe real api_key)", stage)
		}
	}

	ready := false
	dir := t.TempDir()
	svc := NewConfigService(dir)
	// 复现 App 组装顺序：注入先于 Load，Config.Load 尾部即触发首轮导出。
	svc.SetAPIKeyResolver(fakeKeyResolver("sk-test"))
	svc.SetAPIKeyReadyProbe(func() bool { return ready })

	// 未就绪：Load 尾部的首轮导出（原故障点）被门禁跳过，历史文件字节级不变
	//（updated_at 也不会重写）。
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertUnchanged("Load")

	// 未就绪：SaveProvider/SaveTerminalPreset 的导出触发同样被门禁跳过。
	if err := svc.SaveProvider("个人版API- Gemini", openAIProvider()); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	assertUnchanged("SaveProvider")
	marked := TerminalPreset{
		Name: "gemini-3.7-flash", Provider: "个人版API- Gemini", Model: "gemini-3.7-flash",
		Vision: true, VisionPriority: 1,
	}
	if err := svc.SaveTerminalPreset("openai", "个人版API- Gemini/gemini-3.7-flash", marked); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	assertUnchanged("SaveTerminalPreset")

	// 未就绪：ReexportVisionModels 明确报错且不写盘。
	if err := svc.ReexportVisionModels(); err == nil {
		t.Fatal("ReexportVisionModels should fail while secrets not ready")
	}
	assertUnchanged("ReexportVisionModels(not ready)")

	// 就绪：补发导出写盘，api_key 来自 resolver。
	ready = true
	if err := svc.ReexportVisionModels(); err != nil {
		t.Fatalf("ReexportVisionModels: %v", err)
	}
	f := readVisionExport(t, path)
	if len(f.Models) != 1 {
		t.Fatalf("models = %d, want 1", len(f.Models))
	}
	m := findVisionModel(t, f, "个人版API- Gemini/gemini-3.7-flash")
	if m.APIKey != "sk-test" {
		t.Errorf("api_key = %q, want sk-test", m.APIKey)
	}
}
