package codexplugin

import "testing"

func TestParsePluginListOutputTableAndAnsi(t *testing.T) {
	plugins, err := parsePluginListOutput(&CommandResult{Output: "\x1b[32mPlugin Marketplace Version Status Path\x1b[0m\n github@openai-curated openai-curated v1.2.3 enabled /tmp/github"})
	if err != nil {
		t.Fatalf("parse plugin list: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	if plugins[0].ID != "github@openai-curated" || !plugins[0].Enabled || plugins[0].Version != "v1.2.3" || plugins[0].InstallPath != "/tmp/github" || plugins[0].ManifestPath != "" {
		t.Fatalf("unexpected plugin parse: %+v", plugins[0])
	}
}

func TestParsePluginListOutputJSON(t *testing.T) {
	plugins, err := parsePluginListOutput(&CommandResult{Output: `[{"id":"github@openai-curated","version":"1.0.0","enabled":false,"installPath":"/tmp/github"}]`})
	if err != nil {
		t.Fatalf("parse json plugin list: %v", err)
	}
	if len(plugins) != 1 || plugins[0].Enabled || plugins[0].Name != "github" || plugins[0].Marketplace != "openai-curated" {
		t.Fatalf("unexpected plugins: %+v", plugins)
	}
}

func TestParseMarketplaceListOutputBlock(t *testing.T) {
	marketplaces, err := parseMarketplaceListOutput(&CommandResult{Output: `Name: openai-curated
Source: https://github.com/openai/plugins
Snapshot: /tmp/snapshot
Last Updated: 2026-05-22`})
	if err != nil {
		t.Fatalf("parse marketplace list: %v", err)
	}
	if len(marketplaces) != 1 || marketplaces[0].Name != "openai-curated" || marketplaces[0].SnapshotPath != "/tmp/snapshot" {
		t.Fatalf("unexpected marketplaces: %+v", marketplaces)
	}
}

func TestParseMarketplaceListOutputCodexV0132TSV(t *testing.T) {
	path := "/Users/maorun/.codex/.tmp/marketplaces/amagi-codex-marketplace"
	marketplaces, err := parseMarketplaceListOutput(&CommandResult{Output: "amagi-codex-marketplace\t" + path})
	if err != nil {
		t.Fatalf("parse codex v0.132 marketplace list: %v", err)
	}
	if len(marketplaces) != 1 {
		t.Fatalf("expected 1 marketplace, got %d", len(marketplaces))
	}
	if marketplaces[0].Name != "amagi-codex-marketplace" || marketplaces[0].SnapshotPath != path || marketplaces[0].InstallLocation != path {
		t.Fatalf("unexpected marketplace parse: %+v", marketplaces[0])
	}
}

func TestParsePluginListOutputCodexV0132Grouped(t *testing.T) {
	output := "Marketplace `amagi-codex-marketplace`\n" +
		"Path: /Users/maorun/.codex/.tmp/marketplaces/amagi-codex-marketplace/.agents/plugins/marketplace.json\n" +
		"  amagi@amagi-codex-marketplace (installed, enabled)\n" +
		"  preview@amagi-codex-marketplace (not installed)\n" +
		"Marketplace `openai-curated`\n" +
		"Path: /Users/maorun/.codex/.tmp/plugins/.agents/plugins/marketplace.json\n" +
		"  linear@openai-curated (not installed)"
	plugins, err := parsePluginListOutput(&CommandResult{Output: output})
	if err != nil {
		t.Fatalf("parse codex v0.132 plugin list: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("expected only installed plugin, got %d: %+v", len(plugins), plugins)
	}
	plugin := plugins[0]
	if plugin.ID != "amagi@amagi-codex-marketplace" || plugin.Name != "amagi" || plugin.Marketplace != "amagi-codex-marketplace" || !plugin.Enabled {
		t.Fatalf("unexpected plugin parse: %+v", plugin)
	}
	if plugin.ManifestPath == "" {
		t.Fatalf("expected grouped Path line to populate ManifestPath: %+v", plugin)
	}
}

func TestParsePluginListOutputCodexV0132GroupedNameOnly(t *testing.T) {
	output := "Marketplace `amagi-codex-marketplace`\n" +
		"Path: /Users/maorun/.codex/.tmp/marketplaces/amagi-codex-marketplace/.agents/plugins/marketplace.json\n" +
		"  amagi (installed, disabled)"
	plugins, err := parsePluginListOutput(&CommandResult{Output: output})
	if err != nil {
		t.Fatalf("parse grouped name-only plugin list: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d: %+v", len(plugins), plugins)
	}
	if plugins[0].ID != "amagi@amagi-codex-marketplace" || plugins[0].Enabled {
		t.Fatalf("unexpected plugin parse: %+v", plugins[0])
	}
}

func TestParsePluginListOutputPreservesDuplicateRowsForServiceDiagnosis(t *testing.T) {
	plugins, err := parsePluginListOutput(&CommandResult{Output: "amagi@market installed enabled /tmp/amagi\namagi@market installed disabled /tmp/amagi"})
	if err != nil {
		t.Fatalf("parse duplicate plugin list: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("parser should preserve duplicate rows for service-level diagnosis, got %+v", plugins)
	}
}
