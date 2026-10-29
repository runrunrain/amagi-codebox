package piconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Regression: OAuth providers with only modelOverrides in models.json must not
// shadow their cached models (anthropic/ and openai-codex/ in the role editor).
func TestCatalogProviderOverridesKeepBuiltinModels(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	fixtures := map[string]string{
		"auth.json": `{"anthropic":{"type":"oauth","access":"fixture-secret"},"openai-codex":{"type":"oauth","access":"fixture-secret"}}`,
		"models-store.json": `{
			"anthropic":{"models":[{"id":"claude-test","name":"Original","reasoning":true,"contextWindow":1000,"thinkingLevelMap":{"low":null,"xhigh":"xhigh"}},{"id":"untouched","reasoning":false}]},
			"openai-codex":{"models":[{"id":"codex-test","reasoning":true}]}
		}`,
		"models.json": `{"providers":{
			"anthropic":{"modelOverrides":{"claude-test":{"name":"Overridden","contextWindow":2000,"thinkingLevelMap":{"high":null,"max":"max"}},"unknown":{"name":"Must not fabricate"}}},
			"openai-codex":{"modelOverrides":{"codex-test":{"reasoning":false}}}
		}}`,
	}
	for name, fixture := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(fixture), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := NewService().GetPiModelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "fixture-secret") {
		t.Fatal("credential leaked into catalog")
	}
	var catalog ModelCatalog
	if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Providers) != 2 {
		t.Fatalf("expected unique providers, got %s", raw)
	}
	anthropic, codex := catalog.Providers[0], catalog.Providers[1]
	if anthropic.Name != "anthropic" || !anthropic.HasAuth || len(anthropic.Models) != 2 || !codex.HasAuth || len(codex.Models) != 1 {
		t.Fatalf("authenticated models disappeared: %s", raw)
	}
	m := anthropic.Models[0]
	if m.ID != "claude-test" || m.Name != "Overridden" || m.ContextWindow != 2000 || !m.Reasoning {
		t.Fatalf("metadata overrides not applied: %+v", m)
	}
	if !reflect.DeepEqual(m.ThinkingLevels, []string{"off", "minimal", "medium", "xhigh", "max"}) {
		t.Fatalf("thinking maps must merge per key, preserving null exclusions: %+v", m)
	}
	if codex.Models[0].Reasoning || !reflect.DeepEqual(codex.Models[0].ThinkingLevels, []string{"off"}) {
		t.Fatalf("reasoning=false override ignored: %+v", codex.Models[0])
	}
	if anthropic.Models[1].ID != "untouched" || anthropic.Models[1].Reasoning {
		t.Fatalf("unmatched model changed: %+v", anthropic.Models[1])
	}
}

func TestCatalogModelUpsertsAndProviderOnlyOverrides(t *testing.T) {
	for _, provider := range []string{
		`{"baseUrl":"https://fixture.invalid"}`,
		`{"headers":{"X-Test":"fixture"},"models":[]}`,
		`{"models":[{"id":"same","name":"Custom replacement","reasoning":false},{"id":"added"}],"modelOverrides":{"same":{"name":"Final override"}}}`,
	} {
		t.Run(provider, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PI_CODING_AGENT_DIR", dir)
			for name, fixture := range map[string]string{
				"models-store.json": `{"p":{"models":[{"id":"same","reasoning":true,"contextWindow":999,"thinkingLevelMap":{"max":"max"}},{"id":"keep"}]}}`,
				"models.json":       `{"providers":{"p":` + provider + `}}`,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(fixture), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := NewService().GetPiModelCatalog()
			if err != nil {
				t.Fatal(err)
			}
			var catalog ModelCatalog
			if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
				t.Fatal(err)
			}
			if len(catalog.Providers) != 1 || len(catalog.Providers[0].Models) < 2 {
				t.Fatalf("provider config hid cached models: %s", raw)
			}
			models := catalog.Providers[0].Models
			if models[1].ID != "keep" {
				t.Fatalf("unrelated builtin model lost: %s", raw)
			}
			if strings.Contains(provider, "Custom replacement") {
				if len(models) != 3 || models[0].Name != "Final override" || models[0].Reasoning || models[0].ContextWindow == 999 || models[2].ID != "added" {
					t.Fatalf("custom model should replace by ID, then receive overrides: %s", raw)
				}
			}
		})
	}
}

func TestCatalogUnavailableBuiltinCacheDoesNotInventModels(t *testing.T) {
	for _, cache := range []string{"", `{broken`, `[]`, `null`} {
		t.Run("cache="+cache, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PI_CODING_AGENT_DIR", dir)
			fixtures := map[string]string{
				"auth.json":   `{"openai-codex":{"type":"oauth"}}`,
				"models.json": `{"providers":{"openai-codex":{"modelOverrides":{"unknown":{"reasoning":true}}},"relay":{"models":[{"id":"custom"}]}}}`,
			}
			if cache != "" {
				fixtures["models-store.json"] = cache
			}
			for name, fixture := range fixtures {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(fixture), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := NewService().GetPiModelCatalog()
			if err != nil {
				t.Fatal(err)
			}
			var catalog ModelCatalog
			if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
				t.Fatal(err)
			}
			if len(catalog.Providers) != 2 || !catalog.Providers[0].HasAuth || len(catalog.Providers[0].Models) != 0 || catalog.Providers[0].Models == nil || len(catalog.Providers[1].Models) != 1 {
				t.Fatalf("missing cache should keep empty provider and explicit custom model: %s", raw)
			}
		})
	}
}
