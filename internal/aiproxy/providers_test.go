package aiproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"amagi-codebox/internal/aiproxy/router"
	"amagi-codebox/internal/config"
)

func TestProviderDomainMetadata(t *testing.T) {
	catalog := ProvidersFromConfig(map[string]config.Provider{
		"dual":     {Anthropic: &config.AnthropicFormat{Enabled: true, BaseURL: "https://user:secret@API.Example.com.:8443/secret-path?token=hidden", Headers: map[string]string{"Authorization": "private-header"}}, OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://second.example/v1"}, UrlHistory: []string{"https://old.example"}},
		"shared":   {Type: "openai", BaseURL: "https://api.example.com/v1", AuthKey: "private-key"},
		"local":    {Type: "openai", BaseURL: "http://[::1]:1234/v1"},
		"idn":      {Type: "openai", BaseURL: "https://例子.测试/v1"},
		"disabled": {OpenAI: &config.OpenAIFormat{Enabled: false, BaseURL: "https://ignored.example"}},
		"bad":      {Type: "openai", BaseURL: "not a URL /secret"},
	})
	encoded, _ := json.Marshal(catalog)
	for _, secret := range []string{"secret", "hidden", "private", "old.example", "ignored.example", ":8443", "/v1"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("provider DTO leaked %s", secret)
		}
	}
	byID := map[string]Provider{}
	for _, p := range catalog {
		byID[p.ID] = p
	}
	if !reflect.DeepEqual(byID["dual"].Domains, []string{"api.example.com", "second.example"}) || len(byID["dual"].Warnings) == 0 {
		t.Fatal("dual format or shared host metadata lost", byID["dual"])
	}
	if !reflect.DeepEqual(byID["local"].Domains, []string{"::1"}) || len(byID["local"].Warnings) == 0 {
		t.Fatal("IPv6 loopback host not recognized")
	}
	if !reflect.DeepEqual(byID["idn"].Domains, []string{"xn--fsqu00a.xn--0zwm56d"}) {
		t.Fatal("IDN host not normalized", byID["idn"])
	}
	if len(byID["bad"].Domains) != 0 || len(byID["bad"].Warnings) != 1 || len(byID["disabled"].Domains) != 0 {
		t.Fatal("invalid/disabled API URL used")
	}
}

func TestLinkedProviderResolutionAndConflicts(t *testing.T) {
	s := newFixtureService(t)
	providers := []Provider{{ID: "source", Domains: []string{"api.source.example"}}}
	s.SetProviderSource(func() []Provider { return providers })
	settings := s.State().Settings
	settings.Routing.Rules = []router.Rule{{ID: "linked", Name: "Linked", Provider: "source", Enabled: true, Mode: "direct", Domains: []string{"forged.example"}}}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if got := s.State().Settings.Routing.Rules[0]; !reflect.DeepEqual(got.Domains, providers[0].Domains) || got.Provider != "source" {
		t.Fatal("client overrode source domains", got)
	}
	revision := s.data.Revision
	providers[0].Domains = []string{"updated.example"}
	if got := s.State().Settings.Routing.Rules[0]; got.Domains[0] != "updated.example" || got.Mode != "direct" {
		t.Fatal("live source was not read")
	}
	if s.data.Revision != revision {
		t.Fatal("read-only state poll wrote configuration")
	}
	settings = s.State().Settings
	settings.Routing.Rules = append(settings.Routing.Rules, router.Rule{ID: "shared", Name: "Shared", Provider: "other", Enabled: true, Mode: "proxy", Domains: []string{"forged.example"}})
	providers = append(providers, Provider{ID: "other", Domains: []string{"updated.example"}})
	if err := s.SaveSettings(settings); err == nil || !strings.Contains(err.Error(), "重叠") {
		t.Fatal("shared-host contradictory exits accepted", err)
	}
	settings.Routing.Rules[1].Mode = "direct"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal("same-host same-exit should be allowed", err)
	}
	providers = nil
	if err := s.SaveSettings(settings); err == nil {
		t.Fatal("removed source silently retained stale domains")
	}
	for i := range settings.Routing.Rules {
		settings.Routing.Rules[i].Enabled = false
	}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal("cannot disable removed source", err)
	}
	settings.Routing.Rules[0].Provider = ""
	settings.Routing.Rules[0].Enabled = true
	settings.Routing.Rules[0].Domains = []string{"manual.example"}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal("cannot unlink removed source", err)
	}
}

func TestLinkedProvidersRestartAndPendingWithRealCore(t *testing.T) {
	core := os.Getenv("SING_BOX_TEST_BINARY")
	if core == "" {
		t.Skip("requires sing-box")
	}
	s := newFixtureService(t)
	providers := []Provider{{ID: "source", Domains: []string{"one.example"}}}
	s.SetProviderSource(func() []Provider { return providers })
	settings := s.State().Settings
	settings.CorePath = core
	settings.Routing.ProxyPort = unusedPorts(t)[0]
	settings.Routing.Rules = []router.Rule{{ID: "linked", Name: "Linked", Provider: "source", Enabled: true, Mode: "direct", Domains: []string{"one.example"}}}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	providers[0].Domains = []string{"two.example"}
	if !s.State().Pending {
		t.Fatal("changed API URL not marked pending")
	}
	if _, err := s.ApplyEnv(nil); err == nil {
		t.Fatal("new launch silently used stale provider domains")
	}
	req, _ := http.NewRequest("GET", "https://two.example", nil)
	if _, err := s.ProxyForRequest(req); err == nil {
		t.Fatal("model request silently used stale provider domains")
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.State().Pending {
		t.Fatal("applying did not resolve provider revision")
	}
	if _, err := s.ApplyEnv(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// A provider address changed while CodeBox was closed: restore must use the
	// loaded CodeBox catalog before starting its persisted enabled proxy.
	providers[0].Domains = []string{"three.example"}
	restored := New(s.dir)
	defer restored.Close()
	restored.SetProviderSource(func() []Provider { return providers })
	if err := restored.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "runtime", "core.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "three.example") || strings.Contains(string(b), "two.example") {
		t.Fatal("restart used stale provider API address")
	}
}

func TestVersionTwoMigrationPreservesEmptyRules(t *testing.T) {
	data, migrated, err := decodeStoredState([]byte(`{"version":2,"revision":8,"settings":{"routing":{"proxy_port":18890,"rules":[]}}}`))
	if err != nil || !migrated || data.Version != 3 || data.Revision != 9 || data.Settings.Routing.Rules == nil || data.Settings.Routing.EgressMode != "auto" {
		t.Fatal("v2 policy migration failed", data, err)
	}
}
