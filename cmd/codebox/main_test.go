package main

import (
	"encoding/json"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
)

func newTestCLIState(t *testing.T) *cliState {
	t.Helper()
	configDir := t.TempDir()
	return &cliState{
		configDir: configDir,
		claudeDir: configDir,
	}
}

func TestBuildExportConfigReadsAnthropicLegacyProviderAPIKey(t *testing.T) {
	state := newTestCLIState(t)
	configSvc, err := state.getConfigService()
	if err != nil {
		t.Fatalf("getConfigService: %v", err)
	}
	secretsSvc, err := state.getSecretsService()
	if err != nil {
		t.Fatalf("getSecretsService: %v", err)
	}

	const providerName = "export-legacy-anthropic"
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://openai.example.com/v1"},
	}
	if err := configSvc.SaveProvider(providerName, provider); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := secretsSvc.SetAPIKey(providerName+":anthropic", "sk-legacy-anthropic"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	exportCfg, err := buildExportConfig(state)
	if err != nil {
		t.Fatalf("buildExportConfig: %v", err)
	}

	exported, ok := exportCfg.Providers[providerName]
	if !ok {
		t.Fatalf("exported provider %q missing", providerName)
	}
	if exported.APIKey != "sk-legacy-anthropic" {
		t.Fatalf("APIKey = %q, want %q", exported.APIKey, "sk-legacy-anthropic")
	}
	if exported.Anthropic != nil && exported.Anthropic.APIKey != "" {
		t.Fatalf("Anthropic.APIKey = %q, want empty", exported.Anthropic.APIKey)
	}
	if exported.OpenAI != nil && exported.OpenAI.APIKey != "" {
		t.Fatalf("OpenAI.APIKey = %q, want empty", exported.OpenAI.APIKey)
	}
	assertExportedProviderHasSingleTopLevelAPIKey(t, exported)
}

func TestBuildExportConfigReadsOpenAILegacyProviderAPIKey(t *testing.T) {
	state := newTestCLIState(t)
	configSvc, err := state.getConfigService()
	if err != nil {
		t.Fatalf("getConfigService: %v", err)
	}
	secretsSvc, err := state.getSecretsService()
	if err != nil {
		t.Fatalf("getSecretsService: %v", err)
	}

	const providerName = "export-legacy-openai"
	provider := config.Provider{
		Anthropic: &config.AnthropicFormat{Enabled: true, BaseURL: "https://anthropic.example.com"},
	}
	if err := configSvc.SaveProvider(providerName, provider); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := secretsSvc.SetAPIKey(providerName+":openai", "sk-legacy-openai"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	exportCfg, err := buildExportConfig(state)
	if err != nil {
		t.Fatalf("buildExportConfig: %v", err)
	}

	exported, ok := exportCfg.Providers[providerName]
	if !ok {
		t.Fatalf("exported provider %q missing", providerName)
	}
	if exported.APIKey != "sk-legacy-openai" {
		t.Fatalf("APIKey = %q, want %q", exported.APIKey, "sk-legacy-openai")
	}
	if exported.Anthropic != nil && exported.Anthropic.APIKey != "" {
		t.Fatalf("Anthropic.APIKey = %q, want empty", exported.Anthropic.APIKey)
	}
	if exported.OpenAI != nil && exported.OpenAI.APIKey != "" {
		t.Fatalf("OpenAI.APIKey = %q, want empty", exported.OpenAI.APIKey)
	}
	assertExportedProviderHasSingleTopLevelAPIKey(t, exported)
}

func TestBuildExportConfigPrefersProviderLevelAPIKey(t *testing.T) {
	state := newTestCLIState(t)
	configSvc, err := state.getConfigService()
	if err != nil {
		t.Fatalf("getConfigService: %v", err)
	}
	secretsSvc, err := state.getSecretsService()
	if err != nil {
		t.Fatalf("getSecretsService: %v", err)
	}

	const providerName = "export-provider-level"
	provider := config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://openai.example.com/v1"},
	}
	if err := configSvc.SaveProvider(providerName, provider); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := secretsSvc.SetAPIKey(providerName, "sk-provider-level"); err != nil {
		t.Fatalf("Set provider-level API key: %v", err)
	}
	if err := secretsSvc.SetAPIKey(providerName+":openai", "sk-legacy-openai"); err != nil {
		t.Fatalf("Set legacy API key: %v", err)
	}
	if err := secretsSvc.SetAPIKey(providerName+":anthropic", "sk-legacy-anthropic"); err != nil {
		t.Fatalf("Set second legacy API key: %v", err)
	}

	exportCfg, err := buildExportConfig(state)
	if err != nil {
		t.Fatalf("buildExportConfig: %v", err)
	}

	if got := exportCfg.Providers[providerName].APIKey; got != "sk-provider-level" {
		t.Fatalf("APIKey = %q, want %q", got, "sk-provider-level")
	}
	assertExportedProviderHasSingleTopLevelAPIKey(t, exportCfg.Providers[providerName])
}

func assertExportedProviderHasSingleTopLevelAPIKey(t *testing.T, provider config.ExportProvider) {
	t.Helper()
	data, err := json.Marshal(provider)
	if err != nil {
		t.Fatalf("marshal export provider: %v", err)
	}
	if count := strings.Count(string(data), "\"api_key\""); count != 1 {
		t.Fatalf("expected exactly one api_key in exported provider JSON, got %d\n%s", count, string(data))
	}
}
