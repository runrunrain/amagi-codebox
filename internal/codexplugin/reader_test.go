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

func TestParsePluginEntriesRejectsDuplicateKeys(t *testing.T) {
	_, err := parsePluginEntries(`[plugins]
"github@openai-curated" = { enabled = true }
"github@openai-curated" = { enabled = false }
`)
	if err == nil {
		t.Fatal("expected duplicate key error")
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
