package plugin

import "testing"

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
