package router

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/aiproxy/subscription"
)

func freePorts(t *testing.T, count ...int) []int {
	t.Helper()
	n := 3
	if len(count) > 0 {
		n = count[0]
	}
	var ls []net.Listener
	var ports []int
	for range n {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ls = append(ls, ln)
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	for _, ln := range ls {
		ln.Close()
	}
	return ports
}
func mockExit(t *testing.T, marker string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("expected opaque CONNECT, got %s", r.Method)
			http.Error(w, "CONNECT expected", 400)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(rw, "HTTP/1.1 200 Connection Established\r\n\r\n")
		rw.Flush()
		req, err := http.ReadRequest(rw.Reader)
		if err != nil {
			return
		}
		defer req.Body.Close()
		if req.URL.Path == "/stream" {
			fmt.Fprint(rw, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n")
			rw.Flush()
			for i := range 3 {
				chunk := fmt.Sprintf("data: %s-%d\n\n", marker, i)
				fmt.Fprintf(rw, "%x\r\n%s\r\n", len(chunk), chunk)
				rw.Flush()
				time.Sleep(120 * time.Millisecond)
			}
			fmt.Fprint(rw, "0\r\n\r\n")
			rw.Flush()
			return
		}
		body := marker + " " + r.Host
		fmt.Fprintf(rw, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
		rw.Flush()
	}))
}
func proxyNode(t *testing.T, raw, name string) subscription.Node {
	t.Helper()
	o, err := ProxyOutbound(raw)
	if err != nil {
		t.Fatal(err)
	}
	n, err := subscription.NewNode(name, o)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func TestCoreRoutesAndStreaming(t *testing.T) {
	core := os.Getenv("SING_BOX_TEST_BINARY")
	if core == "" {
		t.Skip("set SING_BOX_TEST_BINARY to run real-core integration test")
	}
	claude := mockExit(t, "CLAUDE")
	defer claude.Close()
	openai := mockExit(t, "OPENAI")
	defer openai.Close()
	fallback := mockExit(t, "FALLBACK")
	defer fallback.Close()
	ports := freePorts(t)
	s := Defaults()
	s.ProxyPort, s.Rules[0].Port, s.Rules[1].Port = ports[0], ports[1], ports[2]
	s.Rules[0].Node = "c"
	s.Rules[1].Node = "o"
	s.FallbackProxy = fallback.URL
	nodes := map[string]subscription.Node{"c": proxyNode(t, claude.URL, "Claude"), "o": proxyNode(t, openai.URL, "OpenAI")}
	config, err := Build(s, nodes)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(core, t.TempDir())
	defer m.Stop()
	if err = m.Apply(context.Background(), config, ports, 1); err != nil {
		t.Fatalf("%v\n%s", err, m.Status().Logs)
	}
	request := func(port int, target string) (string, error) {
		p, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
		tr := &http.Transport{Proxy: http.ProxyURL(p)}
		defer tr.CloseIdleConnections()
		client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
		resp, err := client.Get(target)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return string(body), err
	}
	cases := []struct {
		port         int
		target, want string
	}{
		{s.ProxyPort, "http://api.anthropic.com/test", "CLAUDE"},
		{s.ProxyPort, "http://api.openai.com/test", "OPENAI"},
		{s.ProxyPort, "http://chatgpt.com/test", "OPENAI"},
		{s.ProxyPort, "http://api.openai.com.evil.example/test", "FALLBACK"},
		{s.Rules[0].Port, "http://same.third-party.example/test", "CLAUDE"},
		{s.Rules[1].Port, "http://same.third-party.example/test", "OPENAI"},
		{s.Rules[0].Port, "http://api.openai.com/test", "CLAUDE"},
	}
	for _, c := range cases {
		got, err := request(c.port, c.target)
		if err != nil || !strings.HasPrefix(got, c.want) {
			t.Fatalf("%s through %d: %q %v", c.target, c.port, got, err)
		}
	}
	var wg sync.WaitGroup
	for range 10 {
		for _, c := range cases[:2] {
			wg.Add(1)
			go func(c struct {
				port         int
				target, want string
			}) {
				defer wg.Done()
				got, err := request(c.port, c.target)
				if err != nil || !strings.HasPrefix(got, c.want) {
					t.Errorf("concurrent route leaked: %q %v", got, err)
				}
			}(c)
		}
	}
	wg.Wait()
	// Observe chunks as they arrive, rather than accepting a buffered response.
	p, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.Rules[0].Port))
	tr := &http.Transport{Proxy: http.ProxyURL(p)}
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, Timeout: 5 * time.Second}
	started := time.Now()
	resp, err := client.Get("http://stream.example/stream")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "CLAUDE-0") {
		t.Fatal(line, err)
	}
	if time.Since(started) > 300*time.Millisecond {
		t.Fatal("first SSE event was buffered")
	}
	rest, err := io.ReadAll(reader)
	resp.Body.Close()
	if err != nil || !strings.Contains(string(rest), "CLAUDE-2") {
		t.Fatal("stream truncated", err)
	}
	before := m.Status()
	if err = m.Apply(context.Background(), []byte(`{"inbounds":[{"type":"not-real"}]}`), ports, 2); err == nil {
		t.Fatal("invalid config accepted")
	}
	after := m.Status()
	if !after.Running || after.PID != before.PID || after.Revision != 1 {
		t.Fatal("invalid check stopped active core")
	}
	// A port conflict during a replacement must recover the active old config.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	newSettings := s.Clone()
	newSettings.Rules[1].Port = occupied.Addr().(*net.TCPAddr).Port
	newConfig, _ := Build(newSettings, nodes)
	if err = m.Apply(context.Background(), newConfig, newSettings.Ports(), 3); err == nil {
		t.Fatal("occupied port accepted")
	}
	if !m.Status().Running || m.Status().Revision != 1 {
		t.Fatal("old config not recovered")
	}
	got, err := request(s.ProxyPort, "http://api.openai.com/test")
	if err != nil || !strings.HasPrefix(got, "OPENAI") {
		t.Fatal("recovered routing failed", got, err)
	}
	// Explicit restart applies a revision and stopping releases all listeners.
	if err = m.Apply(context.Background(), config, ports, 4); err != nil {
		t.Fatal(err)
	}
	if m.Status().Revision != 4 {
		t.Fatal("revision not updated")
	}
	if err = m.Stop(); err != nil {
		t.Fatal(err)
	}
	if m.Status().Running {
		t.Fatal("core did not stop")
	}
	for _, p := range ports {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			t.Fatal("port not released", err)
		}
		ln.Close()
	}
}
func TestSettingsValidation(t *testing.T) {
	for _, change := range []func(*Settings){func(s *Settings) { s.Rules[0].Port = s.ProxyPort }, func(s *Settings) { s.FetchProxy = fmt.Sprintf("socks5://localhost:%d", s.ProxyPort) }, func(s *Settings) { s.Rules[0].Domains = []string{"https://anthropic.com"} }, func(s *Settings) { s.FallbackProxy = "http://127.0.0.1" }} {
		s := Defaults()
		change(&s)
		if err := s.Validate(); err == nil {
			t.Fatal("invalid settings accepted", s)
		}
	}
	s := Defaults()
	s.Rules[0].Node = "missing"
	s.Rules[1].Node = "missing"
	if _, err := Build(s, nil); err == nil {
		t.Fatal("missing nodes accepted")
	}
	b, err := Build(Settings{ProxyPort: 17001, Rules: []Rule{{ID: "c", Name: "C", Enabled: true, Mode: "proxy", Port: 17002, Node: "c"}, {ID: "o", Name: "O", Enabled: true, Mode: "proxy", Port: 17003, Node: "o"}}}, map[string]subscription.Node{"c": proxyNode(t, "http://127.0.0.1:8001", "c"), "o": proxyNode(t, "http://127.0.0.1:8002", "o")})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if json.Unmarshal(b, &parsed) != nil {
		t.Fatal("invalid generated JSON")
	}
}
