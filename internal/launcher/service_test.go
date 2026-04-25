package launcher

import (
	"testing"

	"amagi-codebox/internal/config"
)

func TestBuildOverrides_DualFormatProviderUsesAnthropicForClaude(t *testing.T) {
	svc := NewLauncherService(nil, nil)
	provider := config.Provider{
		Anthropic: &config.AnthropicFormat{
			Enabled: true,
			BaseURL: "https://anthropic.example.com",
			AuthKey: config.AuthTypeAPIKey,
		},
		OpenAI: &config.OpenAIFormat{
			Enabled: true,
			BaseURL: "https://openai.example.com/v1",
			AuthKey: "OPENAI_API_KEY",
		},
		DefaultModel: "claude-sonnet-4-5",
	}

	overrides := svc.BuildOverrides(provider, "", "sk-provider-level", config.AgentTeamsConfig{})
	if overrides["ANTHROPIC_API_KEY"] != "sk-provider-level" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want sk-provider-level", overrides["ANTHROPIC_API_KEY"])
	}
	if overrides["ANTHROPIC_BASE_URL"] != "https://anthropic.example.com" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want https://anthropic.example.com", overrides["ANTHROPIC_BASE_URL"])
	}
	if overrides["ANTHROPIC_MODEL"] != "claude-sonnet-4-5" {
		t.Fatalf("ANTHROPIC_MODEL = %q, want claude-sonnet-4-5", overrides["ANTHROPIC_MODEL"])
	}
}
