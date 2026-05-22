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
