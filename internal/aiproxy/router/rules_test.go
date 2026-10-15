package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/aiproxy/subscription"
)

func TestRuleValidationAndOptionalPorts(t *testing.T) {
	cases := map[string]func(*Settings){
		"duplicate id":      func(s *Settings) { s.Rules[1].ID = s.Rules[0].ID },
		"invalid id":        func(s *Settings) { s.Rules[0].ID = "../../config" },
		"blank name":        func(s *Settings) { s.Rules[0].Name = " " },
		"invalid mode":      func(s *Settings) { s.Rules[0].Mode = "system" },
		"missing matcher":   func(s *Settings) { s.Rules[0].Domains = nil; s.Rules[0].Port = 0 },
		"bad optional port": func(s *Settings) { s.Rules[0].Enabled = false; s.Rules[0].Port = 1023 },
		"wildcard":          func(s *Settings) { s.Rules[0].Domains = []string{"*.example.com"} },
		"url":               func(s *Settings) { s.Rules[0].Domains = []string{"https://example.com/a"} },
		"uppercase":         func(s *Settings) { s.Rules[0].Domains = []string{"API.Example.com"} },
		"bad label":         func(s *Settings) { s.Rules[0].Domains = []string{"api..example.com"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := Defaults()
			change(&s)
			if s.Validate() == nil {
				t.Fatal("invalid rule accepted")
			}
		})
	}
	s := Defaults()
	s.Rules[0].Enabled = false
	s.Rules[1].Port = s.Rules[0].Port // disabled rules reserve no listeners
	s.Rules = append(s.Rules, Rule{ID: "local", Name: "直连", Enabled: true, Mode: "direct", Domains: []string{"local.example"}})
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(s.Ports()) != 2 {
		t.Fatal("disabled or optional port was bound")
	}
	s.Rules[1].Mode = "direct"
	b, err := Build(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Outbounds []map[string]any
		Inbounds  []map[string]any
	}
	if err = json.Unmarshal(b, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Outbounds) != 1 || len(document.Inbounds) != 2 {
		t.Fatal("direct routing depended on subscription nodes")
	}
	s.Rules = nil
	if _, err = Build(s, nil); err != nil {
		t.Fatal("empty rules should use the unmatched-domain policy", err)
	}
}

func TestDynamicRulesWithRealCore(t *testing.T) {
	core := os.Getenv("SING_BOX_TEST_BINARY")
	if core == "" {
		t.Skip("set SING_BOX_TEST_BINARY for routing integration")
	}
	c := mockExit(t, "CLAUDE")
	defer c.Close()
	o := mockExit(t, "OPENAI")
	defer o.Close()
	g := mockExit(t, "GEMINI")
	defer g.Close()
	f := mockExit(t, "FALLBACK")
	defer f.Close()
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "DIRECT") }))
	defer direct.Close()
	ports := freePorts(t, 4)
	s := Defaults()
	s.ProxyPort, s.Rules[0].Port, s.Rules[1].Port = ports[0], ports[1], ports[2]
	s.Rules[0].Node, s.Rules[1].Node = "c", "o"
	s.FallbackProxy = f.URL
	s.Rules = append(s.Rules,
		Rule{ID: "gemini", Name: "Gemini", Enabled: true, Mode: "proxy", Node: "g", Port: ports[3], Domains: []string{"google.example", "api.shared.example"}},
		Rule{ID: "shared", Name: "中转服务", Enabled: true, Mode: "proxy", Node: "o", Domains: []string{"shared.example"}},
		Rule{ID: "local", Name: "本地直连", Enabled: true, Mode: "direct", Domains: []string{"localhost"}},
		Rule{ID: "disabled", Name: "停用规则", Enabled: false, Mode: "proxy", Node: "removed-node", Port: ports[3], Domains: []string{"other.example"}},
	)
	nodes := map[string]subscription.Node{"c": proxyNode(t, c.URL, "Claude"), "o": proxyNode(t, o.URL, "OpenAI"), "g": proxyNode(t, g.URL, "Gemini")}
	m := NewManager(core, t.TempDir())
	defer m.Stop()
	apply := func(revision uint64) {
		t.Helper()
		b, err := Build(s, nodes)
		if err != nil {
			t.Fatal(err)
		}
		if err = m.Apply(context.Background(), b, s.Ports(), revision); err != nil {
			t.Fatal(err, m.Status().Logs)
		}
	}
	request := func(port int, target, want string) {
		t.Helper()
		proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
		tr := &http.Transport{Proxy: http.ProxyURL(proxy)}
		defer tr.CloseIdleConnections()
		resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get(target)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || !strings.HasPrefix(string(body), want) {
			t.Fatalf("%s: %q %v, want %s", target, body, err, want)
		}
	}
	apply(1)
	request(s.ProxyPort, "http://api.anthropic.com/test", "CLAUDE")
	request(s.ProxyPort, "http://api.openai.com/test", "OPENAI")
	request(s.ProxyPort, "http://api.google.example/test", "GEMINI")
	request(s.ProxyPort, "http://api.shared.example/test", "GEMINI")
	request(s.ProxyPort, "http://api.google.example.evil.test/test", "FALLBACK")
	request(s.ProxyPort, "http://other.example/test", "FALLBACK")
	request(ports[3], "http://api.openai.com/test", "GEMINI")
	localURL := strings.Replace(direct.URL, "127.0.0.1", "localhost", 1)
	request(s.ProxyPort, localURL, "DIRECT")
	s.Rules[2], s.Rules[3] = s.Rules[3], s.Rules[2]
	apply(2)
	request(s.ProxyPort, "http://api.shared.example/test", "OPENAI") // first matching rule wins
	s.Rules[3].Enabled = false
	apply(3)
	request(s.ProxyPort, "http://api.google.example/test", "FALLBACK")
	// Removing all provider rules is valid and retains the explicit fallback.
	s.Rules = nil
	apply(4)
	request(s.ProxyPort, "http://api.anthropic.com/test", "FALLBACK")
}
