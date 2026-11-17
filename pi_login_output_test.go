package main

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/piconfig"
)

// Exercise the production Config listener wiring, with no Startup/keychain access.
func TestPiLoginOutputSavedAndRemovedFromCanonicalRoot(t *testing.T) {
	isolatedHome(t)
	app := NewApp(embed.FS{})
	t.Cleanup(app.Log.Close)
	if err := app.Config.Load(); err != nil {
		t.Fatal(err)
	}
	dir, err := piconfig.StandardAgentDir()
	if err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "models.json"))
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		if err = json.Unmarshal(data, &root); err != nil {
			t.Fatal(err)
		}
		return root["providers"].(map[string]any)
	}
	p := config.PiLoginPreset{Name: "A", Provider: "p", Model: "m", MaxTokens: 65536}
	if err := app.Config.SavePiLoginPreset(p); err != nil {
		t.Fatal(err)
	}
	providers := read()
	field := providers["p"].(map[string]any)["modelOverrides"].(map[string]any)["m"].(map[string]any)["maxTokens"]
	if field != float64(65536) {
		t.Fatalf("production listener output: %v", field)
	}
	p.Model = "new"
	if err := app.Config.SavePiLoginPreset(p); err != nil {
		t.Fatal(err)
	}
	overrides := read()["p"].(map[string]any)["modelOverrides"].(map[string]any)
	if _, ok := overrides["m"]; ok {
		t.Fatal("model switch left stale override")
	}
	if overrides["new"].(map[string]any)["maxTokens"] != float64(65536) {
		t.Fatal("new model output not written")
	}
	if err := app.Config.DeletePiLoginPreset(config.PiLoginPresetKey("p", "A")); err != nil {
		t.Fatal(err)
	}
	if _, ok := read()["p"]; ok {
		t.Fatal("last preset delete left override")
	}
}
