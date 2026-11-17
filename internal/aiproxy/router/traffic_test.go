package router

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/aiproxy/subscription"
)

func TestTrafficPersistenceAndJudgment(t *testing.T) {
	dir := t.TempDir()
	var store trafficStore
	store.open(dir)
	defer func() {
		if store.db != nil {
			store.db.Close()
		}
	}()
	policy := TrafficPolicy{Settings: Settings{Rules: []Rule{
		{ID: "a", Name: "A", Provider: "Provider A", Enabled: true, Domains: []string{"a.example"}, Mode: "proxy", Node: "n"},
		{ID: "b", Name: "B", Enabled: true, Domains: []string{"b.example"}, Mode: "proxy", Node: "n"},
	}}, Nodes: map[string]subscription.Node{"n": {Name: "出口一"}}}
	w := &trafficWriter{logs: &logBuffer{}, store: &store, session: "run1", revision: 3, policy: policy}
	write := func(id int, kind, tag, dest string) {
		line := fmt.Sprintf("INFO [ %d 0ms] %s/mixed[%s]: %s connection to %s\n", id, kind, tag, kind, dest)
		line = strings.Replace(line, "[ ", "[", 1)
		for _, chunk := range []string{line[:17], line[17:]} {
			w.Write([]byte(chunk))
		}
	}
	write(1, "inbound", "auto-in", "api.a.example:443")
	p, err := store.query(TrafficQuery{})
	if err != nil || p.Total != 1 || p.Records[0].Verdict != "unknown" {
		t.Fatalf("pending: %+v %v", p, err)
	}
	write(1, "outbound", "rule-b-out", "api.a.example:443") // different rule, same node: normal
	write(2, "inbound", "rule-b-in", "api.a.example:443")
	write(2, "outbound", "direct", "api.a.example:443")
	write(3, "outbound", "direct", "evila.example:443")
	p, err = store.query(TrafficQuery{Verdict: "mismatch"})
	if err != nil || p.Total != 1 || p.Records[0].Provider != "Provider A" {
		t.Fatalf("mismatch: %+v %v", p, err)
	}
	if p.Records[0].Revision != 3 || p.Records[0].Inbound != "rule-b-in" {
		t.Fatal(p.Records)
	}
	// Outbound events survive the old 32 KiB diagnostic buffer and paging.
	for i := 4; i < 220; i++ {
		write(i, "outbound", "rule-a-out", "api.a.example:443")
	}
	p, err = store.query(TrafficQuery{Provider: "Provider A", Verdict: "match", Offset: 100})
	if err != nil || p.Total != 217 || len(p.Records) != 100 || len(p.Groups) != 1 || p.Groups[0].Count != 217 {
		t.Fatalf("pagination: %+v %v", p, err)
	}
	store.db.Close()
	store.db = nil
	store.open(dir)
	p, err = store.query(TrafficQuery{Search: "evila"})
	if err != nil || p.Total != 1 || p.Records[0].Verdict != "unknown" {
		t.Fatalf("reopen: %+v %v", p, err)
	}
	if _, err = store.query(TrafficQuery{Since: "invalid"}); err == nil {
		t.Fatal("invalid time accepted")
	}
	policy.Settings.Rules = append(policy.Settings.Rules, Rule{ID: "c", Name: "C", Enabled: true, Domains: []string{"a.example"}, Mode: "direct"})
	w.policy = policy
	write(220, "outbound", "direct", "api.a.example:443")
	p, _ = store.query(TrafficQuery{Search: "域名重叠"})
	if p.Total != 1 || p.Records[0].Verdict != "unknown" {
		t.Fatal(p)
	}
}

func TestTrafficWithRealCore(t *testing.T) {
	core := os.Getenv("SING_BOX_TEST_BINARY")
	if core == "" {
		t.Skip("set SING_BOX_TEST_BINARY")
	}
	a := mockExit(t, "A")
	defer a.Close()
	b := mockExit(t, "B")
	defer b.Close()
	ports := freePorts(t, 3)
	s := Defaults()
	s.ProxyPort = ports[0]
	s.Rules[0].Port = ports[1]
	s.Rules[1].Port = ports[2]
	s.Rules[0].Node = "a"
	s.Rules[1].Node = "b"
	nodes := map[string]subscription.Node{"a": proxyNode(t, a.URL, "A"), "b": proxyNode(t, b.URL, "B")}
	config, err := Build(s, nodes)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(core, t.TempDir())
	defer m.Close()
	if err = m.Apply(context.Background(), config, s.Ports(), 7, TrafficPolicy{Settings: s, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{ports[0], ports[2]} {
		proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
		tr := &http.Transport{Proxy: http.ProxyURL(proxy)}
		client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
		resp, err := client.Get("http://api.anthropic.com/test")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		tr.CloseIdleConnections()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		p, err := m.Traffic(TrafficQuery{Verdict: "mismatch"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total == 1 {
			if p.Records[0].Domain != "api.anthropic.com" || p.Records[0].Actual != "B [b]" {
				t.Fatal(p)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected mismatch: %+v\n%s", p, m.Status().Logs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	p, err := m.Traffic(TrafficQuery{Verdict: "match"})
	if err != nil || p.Total != 1 {
		t.Fatalf("normal: %+v %v\n%s", p, err, m.Status().Logs)
	}
}
