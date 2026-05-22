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
	if plugins[0].ID != "github@openai-curated" || !plugins[0].Enabled || plugins[0].Version != "v1.2.3" || plugins[0].InstallPath != "/tmp/github" {
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
