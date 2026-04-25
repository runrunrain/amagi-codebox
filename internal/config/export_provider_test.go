package config

import "testing"

func TestBuildExportProvider_UsesTopLevelAPIKeyOnly(t *testing.T) {
	provider := Provider{
		Anthropic: &AnthropicFormat{
			Enabled: true,
			APIKey:  "anth-secret",
			BaseURL: "https://anthropic.example.com",
		},
		OpenAI: &OpenAIFormat{
			Enabled:      true,
			APIKey:       "openai-secret",
			BaseURL:      "https://openai.example.com/v1",
			Organization: "org-test",
		},
		DefaultModel: "model-a",
	}

	ep := BuildExportProvider(provider, "sk-provider-level")
	if ep.APIKey != "sk-provider-level" {
		t.Fatalf("APIKey = %q, want sk-provider-level", ep.APIKey)
	}
	if ep.Anthropic == nil || ep.Anthropic.APIKey != "" {
		t.Fatal("Anthropic.APIKey should be scrubbed in export provider")
	}
	if ep.OpenAI == nil || ep.OpenAI.APIKey != "" {
		t.Fatal("OpenAI.APIKey should be scrubbed in export provider")
	}
}

func TestExportProviderUnifiedAPIKey_PrefersTopLevelThenLegacyFormats(t *testing.T) {
	ep := ExportProvider{
		APIKey: "sk-top-level",
		Anthropic: &AnthropicFormat{
			Enabled: true,
			APIKey:  "sk-anthropic-legacy",
		},
		OpenAI: &OpenAIFormat{
			Enabled: true,
			APIKey:  "sk-openai-legacy",
		},
	}
	if got := ep.UnifiedAPIKey(); got != "sk-top-level" {
		t.Fatalf("UnifiedAPIKey() = %q, want sk-top-level", got)
	}

	ep.APIKey = ""
	if got := ep.UnifiedAPIKey(); got != "sk-openai-legacy" {
		t.Fatalf("UnifiedAPIKey() = %q, want sk-openai-legacy when OpenAI is preferred", got)
	}
}

func TestExportProviderToProvider_StripsNestedAPIKeys(t *testing.T) {
	ep := ExportProvider{
		Anthropic: &AnthropicFormat{
			Enabled: true,
			APIKey:  "sk-anthropic-legacy",
			BaseURL: "https://anthropic.example.com",
		},
		OpenAI: &OpenAIFormat{
			Enabled:      true,
			APIKey:       "sk-openai-legacy",
			BaseURL:      "https://openai.example.com/v1",
			Organization: "org-test",
		},
		DefaultModel: "model-a",
	}

	provider := ep.ToProvider()
	if provider.Anthropic == nil || provider.Anthropic.APIKey != "" {
		t.Fatal("provider.Anthropic.APIKey should be scrubbed")
	}
	if provider.OpenAI == nil || provider.OpenAI.APIKey != "" {
		t.Fatal("provider.OpenAI.APIKey should be scrubbed")
	}
	if provider.OpenAI.Organization != "org-test" {
		t.Fatalf("OpenAI.Organization = %q, want org-test", provider.OpenAI.Organization)
	}
}
