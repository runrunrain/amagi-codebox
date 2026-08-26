package remote

import (
	"testing"

	"amagi-codebox/internal/config"
)

// handleGetLaunchMeta 的 Pi section 预设合并（buildPiOmpLaunchPresetOptions）：
// openai 公共桶全量在前，anthropic 桶中 HarnessSync 标记预设附加在后；
// unmarked anthropic 预设不进入；撞 key 时 openai 先注册胜出。
// 直接以真实 ConfigService 驱动 helper，不搭建 Server/handler 重基建。

func newLaunchMetaConfigService(t *testing.T) *config.ConfigService {
	t.Helper()
	svc := config.NewConfigService(t.TempDir())
	if err := svc.Load(); err != nil {
		t.Fatalf("config Load: %v", err)
	}
	return svc
}

func presetOptionKeys(options []launchPresetOption) map[string]launchPresetOption {
	byKey := make(map[string]launchPresetOption, len(options))
	for _, opt := range options {
		byKey[opt.Key] = opt
	}
	return byKey
}

func TestBuildPiOmpLaunchPresetOptionsMergesMarkedAnthropicPresets(t *testing.T) {
	svc := newLaunchMetaConfigService(t)

	if err := svc.SaveTerminalPreset("openai", "openai-prov/native", config.TerminalPreset{
		Name: "Native", Provider: "openai-prov", Model: "gpt-native",
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTerminalPreset("claude_code", "anthropic-only/marked", config.TerminalPreset{
		Name: "Marked", Provider: "anthropic-only", Model: "claude-marked", HarnessSync: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTerminalPreset("anthropic", "anthropic-only/plain", config.TerminalPreset{
		Name: "Plain", Provider: "anthropic-only", Model: "claude-plain",
	}); err != nil {
		t.Fatal(err)
	}

	options := buildPiOmpLaunchPresetOptions(svc)
	byKey := presetOptionKeys(options)
	if _, ok := byKey["openai-prov/native"]; !ok {
		t.Errorf("openai bucket preset missing: %v", options)
	}
	if opt, ok := byKey["anthropic-only/marked"]; !ok {
		t.Errorf("marked anthropic preset missing: %v", options)
	} else if opt.Model != "claude-marked" || opt.Provider != "anthropic-only" {
		t.Errorf("marked anthropic preset projected incorrectly: %+v", opt)
	}
	if _, ok := byKey["anthropic-only/plain"]; ok {
		t.Errorf("unmarked anthropic preset leaked: %v", options)
	}
	if len(options) != 2 {
		t.Errorf("option count = %d, want 2 (dedup by key): %v", len(options), options)
	}
}

func TestBuildPiOmpLaunchPresetOptionsOpenAIBucketWinsOnKeyCollision(t *testing.T) {
	svc := newLaunchMetaConfigService(t)

	if err := svc.SaveTerminalPreset("openai", "glm/dual", config.TerminalPreset{
		Name: "OpenAI Side", Provider: "glm", Model: "glm-openai",
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTerminalPreset("anthropic", "glm/dual", config.TerminalPreset{
		Name: "Anthropic Side", Provider: "glm", Model: "glm-marked", HarnessSync: true,
	}); err != nil {
		t.Fatal(err)
	}

	options := buildPiOmpLaunchPresetOptions(svc)
	if len(options) != 1 {
		t.Fatalf("collision must dedupe to one entry, got %v", options)
	}
	if options[0].Key != "glm/dual" || options[0].Model != "glm-openai" {
		t.Fatalf("collision winner = %+v, want openai bucket entry with model glm-openai", options[0])
	}
}

func TestBuildPiOmpLaunchPresetOptionsNilService(t *testing.T) {
	if options := buildPiOmpLaunchPresetOptions(nil); len(options) != 0 {
		t.Fatalf("nil config service must yield empty options, got %v", options)
	}
}
