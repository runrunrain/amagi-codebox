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

func TestNetworkModesAndLoopbackWithRealCore(t *testing.T) {
	core := os.Getenv("SING_BOX_TEST_BINARY")
	if core == "" {
		t.Skip("requires sing-box")
	}
	nic := ""
	for _, n := range Interfaces() {
		if n.Selectable {
			nic = n.Name
			break
		}
	}
	exit := mockExit(t, "EXIT")
	defer exit.Close()
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "LOCAL") }))
	defer local.Close()
	for _, mode := range []string{"auto", "interface", "system"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "interface" && nic == "" {
				t.Skip("no physical interface")
			}
			s := Defaults()
			s.ProxyPort = freePorts(t, 1)[0]
			s.EgressMode = mode
			s.BindInterface = nic
			s.DNSServer = "127.0.0.1"
			s.Rules = []Rule{{ID: "service", Name: "Service", Enabled: true, Mode: "proxy", Node: "exit", Domains: []string{"service.example", "2001:db8::1"}}}
			b, err := Build(s, map[string]subscription.Node{"exit": proxyNode(t, exit.URL, "Exit")})
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Route     map[string]any
				Outbounds []map[string]any
				DNS       struct{ Servers []map[string]any }
			}
			if err = json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			if mode == "auto" && doc.Route["auto_detect_interface"] != true {
				t.Fatal("auto binding missing")
			}
			if mode == "interface" && doc.Route["default_interface"] != nic {
				t.Fatal("manual binding missing")
			}
			if mode == "system" && (doc.Route["default_interface"] != nil || doc.Route["auto_detect_interface"] != nil) {
				t.Fatal("system mode unexpectedly bound")
			}
			if doc.DNS.Servers[0]["inet4_bind_address"] != "127.0.0.1" {
				t.Fatal("local DNS inherited physical binding")
			}
			m := NewManager(core, t.TempDir())
			defer m.Stop()
			if err = m.Apply(context.Background(), b, s.Ports(), 1); err != nil {
				t.Fatal(err, m.Status().Logs)
			}
			p, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.ProxyPort))
			tr := &http.Transport{Proxy: http.ProxyURL(p)}
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
			for target, want := range map[string]string{"http://service.example": "EXIT", local.URL: "LOCAL", strings.Replace(local.URL, "127.0.0.1", "localhost", 1): "LOCAL"} {
				resp, err := client.Get(target)
				if err != nil {
					t.Fatal(target, err, m.Status().Logs)
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if !strings.HasPrefix(string(body), want) {
					t.Fatal(target, string(body))
				}
			}
		})
	}
	s := Defaults()
	s.Rules = []Rule{}
	s.EgressMode = "interface"
	s.BindInterface = "does-not-exist"
	if _, err := Build(s, nil); err == nil || !strings.Contains(err.Error(), "不可用") {
		t.Fatal("missing NIC silently fell back to TUN")
	}
	s.EgressMode = "auto"
	s.DNSServer = "https://dns.example/dns-query"
	if s.Validate() == nil {
		t.Fatal("unsupported DNS form accepted")
	}
}

// Explicit opt-in host probe: use a public URL while another app owns TUN.
// It changes no system network configuration and uses no model credentials.
func TestBoundEgressOnHost(t *testing.T) {
	target := os.Getenv("AI_PROXY_EGRESS_PROBE_URL")
	core := os.Getenv("SING_BOX_TEST_BINARY")
	if target == "" || core == "" {
		t.Skip("opt-in live egress probe")
	}
	for _, mode := range []string{"auto", "interface"} {
		t.Run(mode, func(t *testing.T) {
			s := Defaults()
			s.ProxyPort = freePorts(t, 1)[0]
			s.Rules = []Rule{}
			s.EgressMode = mode
			s.BindInterface = os.Getenv("AI_PROXY_EGRESS_PROBE_NIC")
			s.DNSServer = os.Getenv("AI_PROXY_EGRESS_PROBE_DNS")
			b, err := Build(s, nil)
			if err != nil {
				t.Fatal(err)
			}
			m := NewManager(core, t.TempDir())
			defer m.Stop()
			if err = m.Apply(context.Background(), b, s.Ports(), 1); err != nil {
				t.Fatal(err, m.Status().Logs)
			}
			p, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.ProxyPort))
			tr := &http.Transport{Proxy: http.ProxyURL(p)}
			defer tr.CloseIdleConnections()
			resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Get(target)
			if err != nil {
				t.Fatal(err, m.Status().Logs)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatal(resp.Status)
			}
			t.Logf("%s egress HTTP %d; core: %s", mode, resp.StatusCode, m.Status().Logs)
		})
	}
}
