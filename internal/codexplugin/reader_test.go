package codexplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdatePluginEnabledInTomlInsertsAndUpdatesOnlyEnabled(t *testing.T) {
	input := `model = "gpt-5"

[plugins]
"github@openai-curated" = { enabled = false, source = "local" }

[profiles.default]
model = "gpt-5"
`
	updated, err := updatePluginEnabledInToml(input, "github@openai-curated", true)
	if err != nil {
		t.Fatalf("update existing plugin: %v", err)
	}
	if !strings.Contains(updated, `"github@openai-curated" = { enabled = true, source = "local" }`) {
		t.Fatalf("enabled field not updated while preserving inline table: %s", updated)
	}
	if !strings.Contains(updated, `[profiles.default]`) {
		t.Fatalf("other sections must be preserved: %s", updated)
	}

	updated, err = updatePluginEnabledInToml(updated, "notion@openai-curated", false)
	if err != nil {
		t.Fatalf("insert plugin: %v", err)
	}
	if !strings.Contains(updated, `"notion@openai-curated" = { enabled = false }`) {
		t.Fatalf("new plugin entry missing: %s", updated)
	}
}

func TestUpdatePluginEnabledInTomlCreatesPluginsSection(t *testing.T) {
	updated, err := updatePluginEnabledInToml(`model = "gpt-5"`, "github@openai-curated", true)
	if err != nil {
		t.Fatalf("create plugins section: %v", err)
	}
	if !strings.Contains(updated, "[plugins]") || !strings.Contains(updated, `"github@openai-curated" = { enabled = true }`) {
		t.Fatalf("plugins section not created: %s", updated)
	}
}

func TestParsePluginEntriesMergesDuplicateInlineAndDottedSection(t *testing.T) {
	entries, err := parsePluginEntries(`[plugins."github@openai-curated"]
enabled = false

[plugins]
"github@openai-curated" = { enabled = true }
`)
	if err != nil {
		t.Fatalf("parse duplicate plugin entries: %v", err)
	}
	entry, ok := entries["github@openai-curated"]
	if !ok {
		t.Fatalf("plugin entry missing: %+v", entries)
	}
	if entry.Enabled || entry.Format != codexPluginConfigSection || len(entry.Locations) != 2 {
		t.Fatalf("expected explicit section to be retained while merging duplicates, got %+v", entry)
	}
}

func TestUpdatePluginEnabledInTomlUpdatesDottedSectionWithoutInlineDuplicate(t *testing.T) {
	input := `[plugins."github@openai-curated"]
enabled = false

[profiles.default]
model = "gpt-5"
`
	updated, err := updatePluginEnabledInToml(input, "github@openai-curated", true)
	if err != nil {
		t.Fatalf("update dotted section plugin: %v", err)
	}
	if !strings.Contains(updated, `[plugins."github@openai-curated"]`) || !strings.Contains(updated, `enabled = true`) {
		t.Fatalf("dotted section not updated: %s", updated)
	}
	if strings.Contains(updated, `[plugins]
"github@openai-curated"`) {
		t.Fatalf("must not add inline duplicate for dotted section: %s", updated)
	}
}

func TestRemovePluginEntryFromTomlRemovesInlineAndDottedSection(t *testing.T) {
	input := `[plugins."github@openai-curated"]
enabled = true

[plugins]
"github@openai-curated" = { enabled = true }
"linear@openai-curated" = { enabled = false }
`
	updated, err := removePluginEntryFromToml(input, "github@openai-curated")
	if err != nil {
		t.Fatalf("remove duplicate plugin entries: %v", err)
	}
	if strings.Contains(updated, "github@openai-curated") {
		t.Fatalf("target plugin entries not fully removed: %s", updated)
	}
	if !strings.Contains(updated, "linear@openai-curated") {
		t.Fatalf("unrelated plugin entry must remain: %s", updated)
	}
}

func TestUpdatePluginEnabledInTomlDeduplicatesCurrentMixedFormat(t *testing.T) {
	input := `[plugins."amagi@amagi-codex-marketplace"]
enabled = true

[plugins]
"amagi@amagi-codex-marketplace" = { enabled = false }
`
	updated, err := updatePluginEnabledInToml(input, "amagi@amagi-codex-marketplace", true)
	if err != nil {
		t.Fatalf("deduplicate mixed format plugin entries: %v", err)
	}
	if strings.Count(updated, "amagi@amagi-codex-marketplace") != 1 || !strings.Contains(updated, `[plugins."amagi@amagi-codex-marketplace"]`) {
		t.Fatalf("expected only dotted section to remain: %s", updated)
	}
}

func TestServiceSetPluginEnabledWritesBackup(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[plugins]\n"), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s := NewServiceWithDeps(dir, nil, nil, nil)
	if err := s.setPluginEnabled("github@openai-curated", true); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	b, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	if !strings.Contains(string(b), `"github@openai-curated" = { enabled = true }`) {
		t.Fatalf("updated config missing plugin: %s", string(b))
	}
	backups, err := filepath.Glob(configPath + ".bak.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected one backup, got %v err=%v", backups, err)
	}
}

func TestReadConfigPluginsFallbackResolvesVersionInstallPath(t *testing.T) {
	codexDir := t.TempDir()
	configPath := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(`[plugins]
"amagi@amagi-codex-marketplace" = { enabled = true }
`), 0600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	pluginRoot := filepath.Join(codexDir, "plugins", "cache", "amagi-codex-marketplace", "amagi", "1.5.116")
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0755); err != nil {
		t.Fatalf("mkdir plugin root: %v", err)
	}
	manifestPath := filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")
	if err := os.WriteFile(manifestPath, []byte(`{"name":"amagi","version":"1.5.116"}`), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	s := NewServiceWithDeps(codexDir, nil, nil, nil)
	plugins, err := s.readConfigPluginsFallback()
	if err != nil {
		t.Fatalf("read fallback plugins: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("expected one plugin, got %d: %+v", len(plugins), plugins)
	}
	if plugins[0].InstallPath != pluginRoot || plugins[0].ManifestPath != manifestPath {
		t.Fatalf("fallback did not resolve version root: %+v", plugins[0])
	}
}
