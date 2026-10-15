package aiproxy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"amagi-codebox/internal/aiproxy/router"
)

func TestMissingRulesCannotSilentlyBecomeDirect(t *testing.T) {
	for name, routing := range map[string]string{
		"absent": `{"proxy_port":18890}`,
		"null":   `{"proxy_port":18890,"rules":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.json")
			original := []byte(`{"version":2,"enabled":true,"settings":{"routing":` + routing + `}}`)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			s := New(dir)
			defer s.Close()
			if err := s.Load(context.Background()); err == nil {
				t.Fatal("incomplete enabled policy silently became direct")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(original) {
				t.Fatal("incomplete policy was overwritten")
			}
		})
	}
}

func TestLegacyConfigurationMigration(t *testing.T) {
	source := newFixtureService(t)
	if err := source.Import(context.Background(), "原订阅", "", "trojan://private-password@node.example:443#Original"); err != nil {
		t.Fatal(err)
	}
	node := source.State().Groups[0].Nodes[0].ID
	original := map[string]any{
		"version": 1, "enabled": false, "revision": 7, "groups": source.data.Groups,
		"settings": map[string]any{"corePath": "/custom/sing-box", "v2raynDatabase": source.data.Settings.V2rayNDatabase,
			"routing": map[string]any{
				"proxy_port": 19890, "claude_port": 19892, "openai_port": 19893,
				"claude_node": node, "openai_node": node,
				"claude_domains": []string{"anthropic.custom.example"}, "openai_domains": []string{"openai.custom.example"},
				"fallback_proxy": "socks5://user:secret@127.0.0.1:5800", "fetch_proxy": "http://127.0.0.1:5800",
			}},
	}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	defer s.Close()
	if err = s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.data.Version != 2 || s.data.Revision != 8 {
		t.Fatal("schema migration was not persisted exactly once")
	}
	got := s.data.Settings
	if got.CorePath != "/custom/sing-box" || got.Routing.ProxyPort != 19890 || got.Routing.FetchProxy != "http://127.0.0.1:5800" || got.Routing.FallbackProxy != "socks5://user:secret@127.0.0.1:5800" {
		t.Fatal("common settings changed")
	}
	want := []router.Rule{
		{ID: "claude", Name: "Claude", Enabled: true, Mode: "proxy", Node: node, Port: 19892, Domains: []string{"anthropic.custom.example"}},
		{ID: "openai", Name: "OpenAI", Enabled: true, Mode: "proxy", Node: node, Port: 19893, Domains: []string{"openai.custom.example"}},
	}
	if !reflect.DeepEqual(got.Routing.Rules, want) {
		t.Fatalf("migration lost provider selections: got %#v, want %#v", got.Routing.Rules, want)
	}
	originalGroups, _ := json.Marshal(source.data.Groups)
	migratedGroups, _ := json.Marshal(s.data.Groups)
	if string(originalGroups) != string(migratedGroups) {
		t.Fatal("migration changed stored node credentials")
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "claude_node") || !strings.Contains(string(saved), "private-password") {
		t.Fatal("new schema missing or private data lost")
	}
	again := New(dir)
	defer again.Close()
	if err = again.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again.data.Revision != 8 || !reflect.DeepEqual(again.data.Settings, got) {
		t.Fatal("second startup changed migrated settings")
	}
}

func TestEditableRulesPersistAndSnapshotsStayIsolated(t *testing.T) {
	s := newFixtureService(t)
	if err := s.Import(context.Background(), "nodes", "", "socks5://127.0.0.1:18001#Node"); err != nil {
		t.Fatal(err)
	}
	view := s.State()
	node := view.Groups[0].Nodes[0].ID
	settings := view.Settings
	for i := range settings.Routing.Rules {
		settings.Routing.Rules[i].Enabled = false
	}
	settings.Routing.Rules = append(settings.Routing.Rules,
		router.Rule{ID: "gemini", Name: "Gemini", Enabled: true, Mode: "proxy", Node: node, Domains: []string{"generativelanguage.googleapis.com"}},
		router.Rule{ID: "domestic", Name: "国内模型", Enabled: true, Mode: "direct", Domains: []string{"domestic.example"}},
	)
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	// Neither caller-owned slices nor returned DTOs may mutate persisted data.
	settings.Routing.Rules[2].Name = "mutated"
	settings.Routing.Rules[2].Domains[0] = "mutated.example"
	view = s.State()
	view.Settings.Routing.Rules[2].Domains[0] = "dto-mutation.example"
	if s.State().Settings.Routing.Rules[2].Domains[0] != "generativelanguage.googleapis.com" {
		t.Fatal("routing snapshot aliases saved state")
	}
	reopened := New(s.dir)
	defer reopened.Close()
	if err := reopened.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	rules := reopened.State().Settings.Routing.Rules
	if len(rules) != 4 || rules[2].Name != "Gemini" || rules[2].Node != node || rules[3].Mode != "direct" {
		t.Fatal("custom routing was not restored")
	}
	if err := s.Delete(view.Groups[0].ID); err != nil {
		t.Fatal(err)
	}
	rules = s.State().Settings.Routing.Rules
	if rules[2].Node != "" || rules[3].Mode != "direct" {
		t.Fatal("deleted subscription still selected or direct rule lost")
	}
	settings = s.State().Settings
	settings.Routing.Rules = []router.Rule{} // deletion of every rule is intentional
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	empty := New(s.dir)
	defer empty.Close()
	if err := empty.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(empty.State().Settings.Routing.Rules) != 0 {
		t.Fatal("default providers reappeared after removing all rules")
	}
}
