package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacySharedExtensionsSummaryIsReadOnly(t *testing.T) {
	app := &App{configDir: t.TempDir()}
	root := filepath.Join(app.configDir, "shared")
	got, err := app.GetLegacySharedExtensionsSummary()
	if err != nil || got.Skills != 0 || got.MCP != 0 {
		t.Fatalf("empty old depot: %+v, %v", got, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("summary must not create legacy depot: %v", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "registry.json")
	data := `{"skills":{"old":{"sourcePath":"/missing"}},"mcp":{"a":{"config":{"command":"not-executed"}},"b":{}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = app.GetLegacySharedExtensionsSummary()
	if err != nil || got.Skills != 1 || got.MCP != 2 || got.Path != root {
		t.Fatalf("old depot: %+v, %v", got, err)
	}
	if out, err := os.ReadFile(path); err != nil || string(out) != data {
		t.Fatal("summary changed old registry", err)
	}
}
