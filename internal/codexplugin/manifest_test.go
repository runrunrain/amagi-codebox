package codexplugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPluginManifestPrefersCodexManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".codex-plugin"), 0755); err != nil {
		t.Fatalf("mkdir codex manifest dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0755); err != nil {
		t.Fatalf("mkdir claude manifest dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte(`{"name":"claude"}`), 0644); err != nil {
		t.Fatalf("write claude manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".codex-plugin", "plugin.json"), []byte(`{"name":"codex","version":"1.0.0"}`), 0644); err != nil {
		t.Fatalf("write codex manifest: %v", err)
	}
	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	manifest, path, err := s.readPluginManifest(dir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.Name != "codex" || filepath.Base(filepath.Dir(path)) != ".codex-plugin" {
		t.Fatalf("unexpected manifest=%+v path=%s", manifest, path)
	}
}

func TestFindAvailablePluginsFallsBackToDefaultMarketplaceSnapshotPath(t *testing.T) {
	codexDir := t.TempDir()
	manifestDir := filepath.Join(codexDir, ".tmp", "marketplaces", "amagi-codex-marketplace", "plugins", "amagi", ".codex-plugin")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("mkdir marketplace manifest dir: %v", err)
	}
	manifestPath := filepath.Join(manifestDir, "plugin.json")
	if err := os.WriteFile(manifestPath, []byte(`{"name":"amagi","version":"1.2.3","description":"Amagi Codex plugin"}`), 0644); err != nil {
		t.Fatalf("write marketplace manifest: %v", err)
	}

	s := NewServiceWithDeps(codexDir, nil, nil, nil)
	available, err := s.findAvailablePlugins([]CodexMarketplace{{Name: "amagi-codex-marketplace"}})
	if err != nil {
		t.Fatalf("find available plugins: %v", err)
	}
	if len(available) != 1 {
		t.Fatalf("expected 1 available plugin, got %d: %+v", len(available), available)
	}
	plugin := available[0]
	if plugin.PluginID != "amagi@amagi-codex-marketplace" || plugin.SnapshotPath != filepath.Join(codexDir, ".tmp", "marketplaces", "amagi-codex-marketplace") || plugin.ManifestPath != manifestPath {
		t.Fatalf("unexpected available plugin: %+v", plugin)
	}
}
