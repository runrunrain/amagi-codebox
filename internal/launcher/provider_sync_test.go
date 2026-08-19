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

	openAIOnly := ManagedPresetModels("kimi", provider, presets, config.TerminalPresetOpenAI)
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

	both := ManagedPresetModels("kimi", provider, presets, config.ValidTerminalPresetTypes()...)
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
