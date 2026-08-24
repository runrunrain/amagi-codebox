package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
)

func TestReconcilePiAgentConfigDoesNotOverwriteInvalidOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	original := []byte(`{"providers":`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePiAgentConfig(map[string]any{"providers": map[string]any{}}, dir); err == nil {
		t.Fatal("expected invalid Pi config to fail reconciliation")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("invalid Pi config was overwritten: %q", got)
	}
}

func TestReconcileOmpAgentConfigDoesNotOverwriteInvalidOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yml")
	original := []byte("providers:\n  - invalid\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileOmpAgentConfig(map[string]any{"providers": map[string]any{}}, dir); err == nil {
		t.Fatal("expected invalid OMP config to fail reconciliation")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("invalid OMP config was overwritten: %q", got)
	}
}

func TestManagedPresetModelsBucketSelection(t *testing.T) {
	// Pi/OMP 只消费 openai 公共预设桶：anthropic 桶专属模型（Claude Code 专用）
	// 不得进入 pi/omp 的托管注册；同模型 id 双桶都配时按传入桶序后者覆盖前者。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "k3",
	}
	presets := &config.TerminalPresetsConfig{
		Anthropic: map[string]config.TerminalPreset{
			"kimi/default": {Provider: "kimi", Model: "k3", Parameters: config.Parameters{MaxTokens: 111}},
		},
		OpenAI: map[string]config.TerminalPreset{
			"kimi/default":   {Provider: "kimi", Model: "k3-256k", Parameters: config.Parameters{MaxTokens: 222}},
			"kimi/k3-256k":   {Provider: "kimi", Model: "k3-256k", Parameters: config.Parameters{MaxTokens: 333}},
			"other/preset":   {Provider: "other", Model: "x", Parameters: config.Parameters{MaxTokens: 999}},
			"kimi/kimi-k2.5": {Provider: "kimi", Model: "kimi-k2.5"},
		},
	}

	openAIOnly := ManagedPresetModels("kimi", provider, presets, nil, config.TerminalPresetOpenAI)
	var ids []string
	byID := map[string]config.Parameters{}
	for _, m := range openAIOnly {
		ids = append(ids, m.ID)
		byID[m.ID] = m.Parameters
	}
	wantIDs := []string{"k3", "k3-256k", "kimi-k2.5"} // k3 仅来自 DefaultModel，anthropic 桶预设不混入
	if strings.Join(ids, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("openai-only models = %v, want %v", ids, wantIDs)
	}
	if byID["k3"].MaxTokens != 0 {
		t.Errorf("k3 params leaked from anthropic bucket: %+v", byID["k3"])
	}
	if byID["k3-256k"].MaxTokens != 333 {
		t.Errorf("k3-256k maxTokens = %d, want 333 (openai bucket later preset wins)", byID["k3-256k"].MaxTokens)
	}

	both := ManagedPresetModels("kimi", provider, presets, nil, config.ValidTerminalPresetTypes()...)
	bothByID := map[string]config.Parameters{}
	for _, m := range both {
		bothByID[m.ID] = m.Parameters
	}
	// OpenCode 双桶语义保持不变：anthropic 模型参与，且 openai 桶（后序）覆盖同 id。
	if _, ok := bothByID["k3"]; !ok {
		t.Fatalf("both-bucket collection must keep anthropic model k3: %v", both)
	}
	if bothByID["k3"].MaxTokens != 111 {
		t.Errorf("k3 maxTokens = %d, want 111 (anthropic preset)", bothByID["k3"].MaxTokens)
	}
	if bothByID["k3-256k"].MaxTokens != 333 {
		t.Errorf("k3-256k maxTokens = %d, want 333", bothByID["k3-256k"].MaxTokens)
	}
}

func TestBuildManagedModelsConfigKeepsEveryCollectedModelParameters(t *testing.T) {
	// 回归：统一同步此前按单模型多次调用 builder 再 first-seen 去重合并，早轮
	// 尾部的裸 DefaultModel 注册抢坑 → DefaultModel 的预设参数被剥掉（实测
	// ~/.omp/agent/models.yml 的 glm-5.3 裸注册，丢失 contextWindow/maxTokens/reasoning）。
	// 现在 buildManagedModelsConfig 单趟注册，逐模型参数必须完整保留。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "glm-5.3",
	}
	models := []ManagedProviderModel{
		{ID: "glm-5-turbo", Parameters: config.Parameters{ReasoningEffort: "max"}},
		{ID: "glm-5.3", Parameters: config.Parameters{
			MaxTokens:       65536,
			ContextWindow:   &config.ContextWindowConfig{ModelContextWindow: 1000000},
			ReasoningEffort: "max",
		}},
		{ID: "glm-5.3[1m]", Parameters: config.Parameters{MaxTokens: 131072}},
	}
	cfg, err := BuildPiManagedProviderConfig("glm", provider, "key", models)
	if err != nil {
		t.Fatalf("BuildPiManagedProviderConfig: %v", err)
	}
	entry := cfg["providers"].(map[string]map[string]any)["amagi-glm"]
	got := entry["models"].([]map[string]any)
	if len(got) != len(models) {
		t.Fatalf("models len = %d, want %d: %v", len(got), len(models), got)
	}
	byID := make(map[string]map[string]any, len(got))
	for _, m := range got {
		byID[m["id"].(string)] = m
	}
	def := byID["glm-5.3"]
	if def["maxTokens"] != 65536 || def["contextWindow"] != 1000000 || def["reasoning"] != true {
		t.Errorf("default model lost collected preset params: %v", def)
	}
	if byID["glm-5-turbo"]["reasoning"] != true {
		t.Errorf("glm-5-turbo reasoning = %#v, want true", byID["glm-5-turbo"]["reasoning"])
	}
	if byID["glm-5.3[1m]"]["maxTokens"] != 131072 {
		t.Errorf("glm-5.3[1m] maxTokens = %#v, want 131072", byID["glm-5.3[1m]"]["maxTokens"])
	}

	ompCfg, err := BuildOmpManagedProviderConfig("glm", provider, "key", models)
	if err != nil {
		t.Fatalf("BuildOmpManagedProviderConfig: %v", err)
	}
	ompEntry := ompCfg["providers"].(map[string]map[string]any)["amagi-glm"]
	ompGot := ompEntry["models"].([]map[string]any)
	ompByID := make(map[string]map[string]any, len(ompGot))
	for _, m := range ompGot {
		ompByID[m["id"].(string)] = m
	}
	if ompByID["glm-5.3"]["maxTokens"] != 65536 || ompByID["glm-5.3"]["reasoning"] != true {
		t.Errorf("omp default model lost collected preset params: %v", ompByID["glm-5.3"])
	}
}

func TestManagedPresetModelsCarriesVisionFlag(t *testing.T) {
	// 多模态标记透传回归：模型接受图片输入的判定 = 手动标记 ∨ 自动发现，
	// 供 pi/omp 托管条目声明 input=["text","image"]。缺失该字段会导致下游
	//（amagi-pi 守卫）按默认 ["text"] 误判模型不支持图片输入，拦截 read
	// 图片（实战：amagi-kimi/k3 被误拦）。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "acme-default",
	}
	presets := &config.TerminalPresetsConfig{
		OpenAI: map[string]config.TerminalPreset{
			"kimi/marked-vision": {Provider: "kimi", Model: "acme-v9", Vision: true},
			"kimi/marked-video":  {Provider: "kimi", Model: "acme-vid", Video: true},
			"kimi/plain":         {Provider: "kimi", Model: "acme-plain"},
			"kimi/auto-k3":       {Provider: "kimi", Model: "k3"}, // 未标记，自动发现
			"kimi/auto-gemini":   {Provider: "kimi", Model: "gemini-3.7-flash"},
		},
	}
	models := ManagedPresetModels("kimi", provider, presets, nil, config.TerminalPresetOpenAI)
	visionByID := map[string]bool{}
	for _, m := range models {
		visionByID[m.ID] = m.Vision
	}
	if !visionByID["acme-v9"] {
		t.Errorf("acme-v9 vision = false, want true (manual Vision mark)")
	}
	if !visionByID["acme-vid"] {
		t.Errorf("acme-vid vision = false, want true (manual Video mark accepts image frames)")
	}
	if visionByID["acme-plain"] {
		t.Errorf("acme-plain vision = true, want false (unmarked and unknown family)")
	}
	if visionByID["acme-default"] {
		t.Errorf("acme-default vision = true, want false (DefaultModel unknown family)")
	}
	// 正向：自动发现——未手动标记的已知多模态模型族同样置位。
	if !visionByID["k3"] {
		t.Errorf("k3 vision = false, want true (auto-discovered kimi k3 family)")
	}
	if !visionByID["gemini-3.7-flash"] {
		t.Errorf("gemini-3.7-flash vision = false, want true (auto-discovered gemini family)")
	}
}

func TestManagedPresetModelsVisionLastWins(t *testing.T) {
	// 覆盖语义与 Parameters 一致：同 id 预设后序（键序）覆盖前序——
	// 后序预设未标记时手动标记重置为 false，不允许前序标记残留。
	// 用未知模型族 id（acme-text-9 不可推断），隔离自动发现的干扰。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "acme-text-9",
	}
	presets := &config.TerminalPresetsConfig{
		OpenAI: map[string]config.TerminalPreset{
			"kimi/a-marked":   {Provider: "kimi", Model: "acme-text-9", Vision: true},
			"kimi/b-unmarked": {Provider: "kimi", Model: "acme-text-9"},
		},
	}
	models := ManagedPresetModels("kimi", provider, presets, nil, config.TerminalPresetOpenAI)
	for _, m := range models {
		if m.ID == "acme-text-9" && m.Vision {
			t.Errorf("acme-text-9 vision = true, want false (later unmarked preset resets)")
		}
	}
}

func TestManagedPresetModelsProbeCacheUnion(t *testing.T) {
	// 三层并集回归：探测缓存（实证）与手动标记/KB 并列——缓存判定为视觉的
	// 未知族模型必须置位 Vision；nil lookup（未注入探测）不影响既有行为。
	provider := config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example.com"},
		DefaultModel: "acme-default",
	}
	presets := &config.TerminalPresetsConfig{
		OpenAI: map[string]config.TerminalPreset{
			"acme/v9":    {Provider: "acme", Model: "acme-v9"},    // 仅探测缓存确认
			"acme/plain": {Provider: "acme", Model: "acme-plain"}, // 三层皆无
		},
	}
	probeCache := config.ModalityProbeSnapshot{
		"acme/acme-v9": {Vision: true, Source: config.ModalityProbeSourceImageProbe},
	}
	models := ManagedPresetModels("acme", provider, presets, probeCache, config.TerminalPresetOpenAI)
	visionByID := map[string]bool{}
	for _, m := range models {
		visionByID[m.ID] = m.Vision
	}
	if !visionByID["acme-v9"] {
		t.Errorf("acme-v9 vision = false, want true (probe cache conclusive)")
	}
	if visionByID["acme-plain"] {
		t.Errorf("acme-plain vision = true, want false (no mark/probe/KB)")
	}
	if visionByID["acme-default"] {
		t.Errorf("acme-default vision = true, want false")
	}
}
