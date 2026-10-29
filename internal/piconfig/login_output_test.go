package piconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoginCatalogMaximumIgnoresOutputOverride(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{
		"models-store.json": `{"p":{"models":[{"id":"m","maxTokens":65536,"contextWindow":200000},{"id":"unknown"},{"id":"invalid","maxTokens":1.5}]},"other":{"models":[{"id":"m","maxTokens":999}]}}`,
		"models.json":       `{"providers":{"p":{"modelOverrides":{"m":{"maxTokens":1024,"contextWindow":100000}}}}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	models := loginModels(dir, "p")
	if len(models) != 3 || models[0].MaxTokens != 65536 || models[1].MaxTokens != 0 || models[2].MaxTokens != 0 {
		t.Fatalf("catalog maximum: %+v", models)
	}
	raw, _ := json.Marshal(models)
	if string(raw) == "" || !json.Valid(raw) {
		t.Fatalf("invalid JSON: %s", raw)
	}
	if err := os.WriteFile(filepath.Join(dir, "models-store.json"), []byte(`broken`), 0600); err != nil {
		t.Fatal(err)
	}
	if models = loginModels(dir, "p"); len(models) != 0 {
		t.Fatalf("corrupt catalog invented entries: %+v", models)
	}
}

func TestFrontlineCatalogIncludesOutputMaximum(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "amagi", "data")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "codex-frontline.json"), []byte(`{"provider":"p","models":[{"id":"new","maxTokens":128000}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	models := loginModels(dir, "p")
	if len(models) != 1 || models[0].ID != "new" || models[0].MaxTokens != 128000 {
		t.Fatalf("frontline: %+v", models)
	}
}
