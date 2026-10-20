package launcher

import (
	"testing"

	"amagi-codebox/internal/config"
)

// Regression: OpenCode rejects context-only and output-only limit objects.
// An unspecified capacity must not be replaced with an invented model limit.
func TestBuildOpenCodeManagedProviderEntryLimits(t *testing.T) {
	for _, tt := range []struct {
		name    string
		context *config.ContextWindowConfig
		output  int
		want    bool
	}{
		{name: "neither"},
		{name: "context only", context: &config.ContextWindowConfig{ModelContextWindow: 200000}},
		{name: "output only", output: 8192},
		{name: "both", context: &config.ContextWindowConfig{ModelContextWindow: 200000}, output: 8192, want: true},
		{name: "zero context", context: &config.ContextWindowConfig{}, output: 8192},
		{name: "negative context", context: &config.ContextWindowConfig{ModelContextWindow: -1}, output: 8192},
		{name: "negative output", context: &config.ContextWindowConfig{ModelContextWindow: 200000}, output: -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			id, entry, err := BuildOpenCodeManagedProviderEntry("custom", config.Provider{
				OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://example.com/v1"},
			}, "", []ManagedProviderModel{{ID: "custom-model", Parameters: config.Parameters{
				ContextWindow: tt.context,
				MaxTokens:     tt.output,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			if id != "amagi-custom" {
				t.Fatalf("provider ID = %q", id)
			}
			model := entry["models"].(map[string]any)["custom-model"].(map[string]any)
			if model["name"] != "custom-model" {
				t.Fatalf("model was lost: %#v", model)
			}
			limit, present := model["limit"]
			if present != tt.want {
				t.Fatalf("limit presence = %v, want %v: %#v", present, tt.want, model)
			}
			if tt.want {
				limits := limit.(map[string]any)
				if len(limits) != 2 || limits["context"] != tt.context.ModelContextWindow || limits["output"] != tt.output {
					t.Fatalf("incorrect complete limit: %#v", limits)
				}
			}
			if tt.output > 0 {
				if model["options"].(map[string]any)["maxTokens"] != tt.output {
					t.Fatalf("explicit output option was lost: %#v", model)
				}
			}
		})
	}
}
