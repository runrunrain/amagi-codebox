package main

import (
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/remote/contract"
)

// buildRemoteLaunchSettings 的 HarnessSync opt-in 扩面（v1 移动端 launch
// settings）：Pi/Omp 的 Presets/Providers 面向 marked anthropic 预设开放，
// ClaudeCode/Codex/OpenCode 分支保持零变化。

func findLaunchSettingsCLI(settings *contract.LaunchSettings, cliType contract.CLIType) *contract.CLILaunchSettings {
	for i := range settings.CLIs {
		if settings.CLIs[i].CLIType == cliType {
			return &settings.CLIs[i]
		}
	}
	return nil
}

func launchPresetRefs(entry *contract.CLILaunchSettings) map[string]bool {
	refs := make(map[string]bool, len(entry.Presets))
	for _, preset := range entry.Presets {
		refs[preset.Ref] = true
	}
	return refs
}

func launchProviderRefs(entry *contract.CLILaunchSettings) map[string]bool {
	refs := make(map[string]bool, len(entry.Providers))
	for _, provider := range entry.Providers {
		refs[provider.Ref] = true
	}
	return refs
}

func TestBuildRemoteLaunchSettingsPiOmpIncludeMarkedAnthropicPresets(t *testing.T) {
	app := newTestApp(t)

	// anthropic-only provider：非 OpenAI 兼容，但其 anthropic 桶预设被标记。
	if err := app.Config.SaveProvider("anthropic-only", config.Provider{
		Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://anthropic.example"},
		DefaultModel: "claude-default",
	}); err != nil {
		t.Fatal(err)
	}
	// openai provider：原生可选项，用于验证排序与撞 key 语义。
	if err := app.Config.SaveProvider("openai-prov", config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://openai.example/v1"},
		DefaultModel: "gpt-default",
	}); err != nil {
		t.Fatal(err)
	}

	if err := app.Config.SaveTerminalPreset("anthropic", "anthropic-only/marked", config.TerminalPreset{
		Name: "Marked", Provider: "anthropic-only", Model: "claude-marked", HarnessSync: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.Config.SaveTerminalPreset("anthropic", "anthropic-only/unmarked", config.TerminalPreset{
		Name: "Unmarked", Provider: "anthropic-only", Model: "claude-plain",
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.Config.SaveTerminalPreset("openai", "openai-prov/native", config.TerminalPreset{
		Name: "Native", Provider: "openai-prov", Model: "gpt-native",
	}); err != nil {
		t.Fatal(err)
	}

	settings := app.buildRemoteLaunchSettings()

	for _, cliType := range []contract.CLIType{contract.CLITypePi, contract.CLITypeOmp} {
		entry := findLaunchSettingsCLI(settings, cliType)
		if entry == nil {
			t.Fatalf("%s launch settings entry missing", cliType)
		}
		presets := launchPresetRefs(entry)
		if !presets["anthropic-only/marked"] {
			t.Errorf("%s presets missing marked anthropic item: %v", cliType, entry.Presets)
		}
		if presets["anthropic-only/unmarked"] {
			t.Errorf("%s presets leaked unmarked anthropic item: %v", cliType, entry.Presets)
		}
		if !presets["openai-prov/native"] {
			t.Errorf("%s presets missing openai bucket item: %v", cliType, entry.Presets)
		}
		providers := launchProviderRefs(entry)
		if !providers["anthropic-only"] {
			t.Errorf("%s providers missing marked-preset anthropic-only provider: %v", cliType, entry.Providers)
		}
		if !providers["openai-prov"] {
			t.Errorf("%s providers missing openai provider: %v", cliType, entry.Providers)
		}
	}

	// Codex 分支零变化：预设仍只来自 openai 桶，providers 仍要求 OpenAI 兼容。
	codex := findLaunchSettingsCLI(settings, contract.CLITypeCodex)
	if codex == nil {
		t.Fatal("codex launch settings entry missing")
	}
	codexPresets := launchPresetRefs(codex)
	if codexPresets["anthropic-only/marked"] {
		t.Errorf("codex presets must not include anthropic bucket items: %v", codex.Presets)
	}
	if !codexPresets["openai-prov/native"] {
		t.Errorf("codex presets missing openai bucket item: %v", codex.Presets)
	}
	if codexProviders := launchProviderRefs(codex); codexProviders["anthropic-only"] {
		t.Errorf("codex providers must exclude non-OpenAI-compatible provider: %v", codex.Providers)
	}
}

func TestBuildRemoteLaunchSettingsPiOmpOpenAIBucketWinsOnKeyCollision(t *testing.T) {
	app := newTestApp(t)

	if err := app.Config.SaveProvider("glm", config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://glm.example/v1"},
		DefaultModel: "glm-5.3",
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.Config.SaveTerminalPreset("openai", "glm/dual", config.TerminalPreset{
		Name: "OpenAI Side", Provider: "glm", Model: "glm-openai",
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.Config.SaveTerminalPreset("anthropic", "glm/dual", config.TerminalPreset{
		Name: "Anthropic Side", Provider: "glm", Model: "glm-marked", HarnessSync: true,
	}); err != nil {
		t.Fatal(err)
	}

	settings := app.buildRemoteLaunchSettings()
	pi := findLaunchSettingsCLI(settings, contract.CLITypePi)
	if pi == nil {
		t.Fatal("pi launch settings entry missing")
	}
	if len(pi.Presets) != 1 {
		t.Fatalf("collision must dedupe to one entry, got %v", pi.Presets)
	}
	if pi.Presets[0].Ref != "glm/dual" || pi.Presets[0].ModelRef != "glm-openai" {
		t.Fatalf("collision winner = %+v, want openai bucket entry with model glm-openai", pi.Presets[0])
	}
}

func TestBuildRemoteLaunchSettingsPiOmpWithoutMarkedPresetsUnchanged(t *testing.T) {
	app := newTestApp(t)

	// 仅有 unmarked anthropic 预设：Pi/Omp 面不得扩容（回归基线）。
	if err := app.Config.SaveProvider("anthropic-only", config.Provider{
		Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://anthropic.example"},
		DefaultModel: "claude-default",
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.Config.SaveTerminalPreset("anthropic", "anthropic-only/plain", config.TerminalPreset{
		Name: "Plain", Provider: "anthropic-only", Model: "claude-plain",
	}); err != nil {
		t.Fatal(err)
	}

	settings := app.buildRemoteLaunchSettings()
	for _, cliType := range []contract.CLIType{contract.CLITypePi, contract.CLITypeOmp} {
		entry := findLaunchSettingsCLI(settings, cliType)
		if entry == nil {
			t.Fatalf("%s launch settings entry missing", cliType)
		}
		if presets := launchPresetRefs(entry); presets["anthropic-only/plain"] {
			t.Errorf("%s presets leaked unmarked anthropic item: %v", cliType, entry.Presets)
		}
		if providers := launchProviderRefs(entry); providers["anthropic-only"] {
			t.Errorf("%s providers leaked non-OpenAI provider without marked presets: %v", cliType, entry.Providers)
		}
	}
}
