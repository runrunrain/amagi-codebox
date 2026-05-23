package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeAvailablePluginsKeepsMarketplaceLinkage(t *testing.T) {
	raw := []interface{}{
		map[string]interface{}{
			"name":         "amagi",
			"marketplace":  "amagi-marketplace",
			"description":  "Amagi plugin",
			"installCount": float64(42),
		},
		map[string]interface{}{
			"pluginId": "tools@tools-marketplace",
			"repo":     "owner/tools",
		},
		"plain@string-marketplace",
	}

	available := normalizeAvailablePlugins(raw)
	if len(available) != 3 {
		t.Fatalf("expected 3 normalized plugins, got %d: %+v", len(available), available)
	}

	plugins := make(map[string]AvailablePlugin, len(available))
	for _, item := range available {
		plugin, ok := item.(AvailablePlugin)
		if !ok {
			t.Fatalf("expected AvailablePlugin item, got %T", item)
		}
		plugins[plugin.PluginID] = plugin
	}

	if plugin := plugins["amagi@amagi-marketplace"]; plugin.Name != "amagi" || plugin.MarketplaceName != "amagi-marketplace" || plugin.InstallCount != 42 {
		t.Fatalf("available plugin did not derive stable id/marketplace: %+v", plugin)
	}
	if plugin := plugins["tools@tools-marketplace"]; plugin.Name != "tools" || plugin.MarketplaceName != "tools-marketplace" || plugin.Repository != "owner/tools" {
		t.Fatalf("available plugin did not derive fields from pluginId: %+v", plugin)
	}
	if plugin := plugins["plain@string-marketplace"]; plugin.Name != "plain" || plugin.MarketplaceName != "string-marketplace" {
		t.Fatalf("string available plugin was not normalized: %+v", plugin)
	}
}

func TestBuildSubItemsUsesStableDetailOrder(t *testing.T) {
	detail := &PluginDetail{
		InstalledPlugin: InstalledPlugin{InstallPath: "/tmp/plugin"},
		Skills: []SkillInfo{
			{Name: "alpha", FilePath: "/tmp/plugin/skills/alpha/SKILL.md"},
		},
		Agents: []AgentInfo{
			{Name: "baize", FilePath: "/tmp/plugin/agents/baize.md"},
		},
		Commands: []CommandInfo{
			{Name: "deploy", FilePath: "/tmp/plugin/commands/deploy.md"},
		},
		Hooks: []HookInfo{
			{Name: "SessionStart:command", Event: "SessionStart", Type: "command", FilePath: "/tmp/plugin/hooks/hooks.json"},
		},
		MCPServers: map[string]interface{}{
			"memory": map[string]interface{}{"type": "stdio"},
		},
	}

	items := buildSubItems(detail, nil)
	if len(items) != 5 {
		t.Fatalf("expected 5 subitems, got %d: %+v", len(items), items)
	}
	for i, expected := range []SubItemType{SubItemTypeSkill, SubItemTypeAgent, SubItemTypeCommand, SubItemTypeHook, SubItemTypeMCP} {
		if items[i].Type != expected {
			t.Fatalf("expected subitem %d to be %s, got %+v", i, expected, items)
		}
	}
	if items[0].Name != "alpha" || items[0].Path != "skills/alpha/SKILL.md" {
		t.Fatalf("skill should remain first with relative path for default detail selection: %+v", items[0])
	}
}

func TestReadPluginManifestForInstalledReadsPluginInstallPathManifest(t *testing.T) {
	dir := t.TempDir()
	manifestDir := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), []byte(`{"name":"amagi","version":"1.5.117","description":"Amagi plugin manifest description"}`), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	s := NewService(filepath.Join(t.TempDir(), ".claude"), nil)
	manifest, err := s.readPluginManifestForInstalled(InstalledPlugin{Name: "amagi", Marketplace: "amagi-plugins-marketplace", InstallPath: dir})
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.Description != "Amagi plugin manifest description" || manifest.Version != "1.5.117" {
		t.Fatalf("unexpected manifest from install path: %+v", manifest)
	}
}

func TestReadPluginManifestForInstalledFallsBackToMarketplaceSourceManifest(t *testing.T) {
	claudeDir := t.TempDir()
	marketplaceDir := filepath.Join(claudeDir, "plugins", "marketplaces", "amagi-plugins-marketplace")
	if err := os.MkdirAll(filepath.Join(claudeDir, "plugins"), 0755); err != nil {
		t.Fatalf("mkdir claude plugins dir: %v", err)
	}
	knownMarketplaces := `{
  "amagi-plugins-marketplace": {
    "source": {"source":"local", "url":"/tmp/amagi-plugins-marketplace"},
    "installLocation": ` + quoteJSON(marketplaceDir) + `
  }
}`
	if err := os.WriteFile(filepath.Join(claudeDir, "plugins", "known_marketplaces.json"), []byte(knownMarketplaces), 0644); err != nil {
		t.Fatalf("write known marketplaces: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(marketplaceDir, ".claude-plugin"), 0755); err != nil {
		t.Fatalf("mkdir marketplace metadata: %v", err)
	}
	marketplaceJSON := `{
  "metadata": {"pluginRoot":"./plugins"},
  "plugins": [{"name":"amagi", "source":"./plugins/amagi", "description":"Marketplace entry description", "version":"1.5.116"}]
}`
	if err := os.WriteFile(filepath.Join(marketplaceDir, ".claude-plugin", "marketplace.json"), []byte(marketplaceJSON), 0644); err != nil {
		t.Fatalf("write marketplace catalog: %v", err)
	}
	pluginManifestDir := filepath.Join(marketplaceDir, "plugins", "amagi", ".claude-plugin")
	if err := os.MkdirAll(pluginManifestDir, 0755); err != nil {
		t.Fatalf("mkdir source plugin manifest dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginManifestDir, "plugin.json"), []byte(`{"name":"amagi","version":"1.5.117","description":"Source manifest description","repository":"https://example.test/amagi"}`), 0644); err != nil {
		t.Fatalf("write source plugin manifest: %v", err)
	}

	s := NewService(claudeDir, nil)
	manifest, err := s.readPluginManifestForInstalled(InstalledPlugin{
		Name:        "amagi",
		Marketplace: "amagi-plugins-marketplace",
		Version:     "1.5.117",
		InstallPath: filepath.Join(claudeDir, "plugins", "cache", "amagi-plugins-marketplace", "amagi", "1.5.117"),
	})
	if err != nil {
		t.Fatalf("read fallback manifest: %v", err)
	}
	if manifest.Description != "Source manifest description" || manifest.Repository != "https://example.test/amagi" || manifest.Version != "1.5.117" {
		t.Fatalf("unexpected fallback manifest: %+v", manifest)
	}
}

func TestReadPluginManifestForInstalledFallsBackToMarketplaceEntryDescription(t *testing.T) {
	claudeDir := t.TempDir()
	marketplaceDir := filepath.Join(claudeDir, "plugins", "marketplaces", "amagi-plugins-marketplace")
	if err := os.MkdirAll(filepath.Join(claudeDir, "plugins"), 0755); err != nil {
		t.Fatalf("mkdir claude plugins dir: %v", err)
	}
	knownMarketplaces := `{
  "amagi-plugins-marketplace": {
    "source": {"source":"local", "url":"/tmp/amagi-plugins-marketplace"},
    "installLocation": ` + quoteJSON(marketplaceDir) + `
  }
}`
	if err := os.WriteFile(filepath.Join(claudeDir, "plugins", "known_marketplaces.json"), []byte(knownMarketplaces), 0644); err != nil {
		t.Fatalf("write known marketplaces: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(marketplaceDir, ".claude-plugin"), 0755); err != nil {
		t.Fatalf("mkdir marketplace metadata: %v", err)
	}
	marketplaceJSON := `{
  "metadata": {"pluginRoot":"./plugins"},
  "plugins": [{"name":"amagi", "source":"./plugins/amagi", "description":"Marketplace entry description", "version":"1.5.116"}]
}`
	if err := os.WriteFile(filepath.Join(marketplaceDir, ".claude-plugin", "marketplace.json"), []byte(marketplaceJSON), 0644); err != nil {
		t.Fatalf("write marketplace catalog: %v", err)
	}

	s := NewService(claudeDir, nil)
	manifest, err := s.readPluginManifestForInstalled(InstalledPlugin{
		Name:        "amagi",
		Marketplace: "amagi-plugins-marketplace",
		Version:     "1.5.116",
		InstallPath: filepath.Join(claudeDir, "plugins", "cache", "amagi-plugins-marketplace", "amagi", "1.5.116"),
	})
	if err != nil {
		t.Fatalf("read fallback entry: %v", err)
	}
	if manifest.Description != "Marketplace entry description" || manifest.Version != "1.5.116" {
		t.Fatalf("unexpected marketplace entry fallback: %+v", manifest)
	}
}

func quoteJSON(value string) string {
	b, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(b)
}
