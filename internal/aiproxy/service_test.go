package aiproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

func newFixtureService(t *testing.T) *Service {
	t.Helper()
	s := New(t.TempDir())
	settings := s.State().Settings
	settings.V2rayNDatabase = filepath.Join(t.TempDir(), "missing.db")
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func unusedPorts(t *testing.T) []int {
	t.Helper()
	var listeners []net.Listener
	var ports []int
	for range 3 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, ln)
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	for _, ln := range listeners {
		ln.Close()
	}
	return ports
}
func exitServer(t *testing.T, marker string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			http.Error(w, "CONNECT required", 400)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprint(rw, "HTTP/1.1 200 Connection Established\r\n\r\n")
		rw.Flush()
		request, err := http.ReadRequest(rw.Reader)
		if err != nil {
			return
		}
		request.Body.Close()
		fmt.Fprintf(rw, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(marker), marker)
		rw.Flush()
	}))
}
func TestPrivacyPersistenceAndDisabledEnvironment(t *testing.T) {
	s := newFixtureService(t)
	if err := s.Import(context.Background(), "private group", "", "trojan://private-node-password@node.example:443#Node"); err != nil {
		t.Fatal(err)
	}
	view := s.State()
	settings := view.Settings
	settings.Routing.FetchProxy = "http://user:private-proxy-password@127.0.0.1:5800"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s.State())
	for _, secret := range []string{"private-node-password", "private-proxy-password", "outbound"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("private credentials leaked into desktop DTO")
		}
	}
	info, err := os.Stat(filepath.Join(s.dir, "state.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe state file permissions", err)
	}
	base := []string{"HTTP_PROXY=http://existing:5800", "NO_PROXY=*", "API_KEY=unrelated"}
	got, err := s.ApplyEnv(base)
	if err != nil || !reflect.DeepEqual(got, base) {
		t.Fatal("disabled feature changed existing environment")
	}
	maskedSettings := s.State().Settings
	revision := s.data.Revision
	if err = s.SaveSettings(maskedSettings); err != nil {
		t.Fatal(err)
	}
	if s.data.Revision != revision {
		t.Fatal("identical settings caused revision churn")
	}
	if s.data.Settings.Routing.FetchProxy != settings.Routing.FetchProxy {
		t.Fatal("masked proxy credential not preserved")
	}
	copyService := New(s.dir)
	if err = copyService.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer copyService.Close()
	if len(copyService.State().Groups) != 1 {
		t.Fatal("imported groups not persisted")
	}
}
func TestCorruptStateFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	original := []byte(`{"version":999}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	if err := s.Load(context.Background()); err == nil {
		t.Fatal("future schema accepted")
	}
	if err := s.SaveSettings(s.State().Settings); err == nil {
		t.Fatal("corrupt state overwritten")
	}
	after, _ := os.ReadFile(path)
	if !reflect.DeepEqual(after, original) {
		t.Fatal("original state was overwritten")
	}
}
func TestNativeRoutingLifecycleAndDirectFallback(t *testing.T) {
	core := os.Getenv("SING_BOX_TEST_BINARY")
	if core == "" {
		t.Skip("set SING_BOX_TEST_BINARY for the native core integration")
	}
	s := newFixtureService(t)
	claude := exitServer(t, "CLAUDE")
	defer claude.Close()
	openai := exitServer(t, "OPENAI")
	defer openai.Close()
	if err := s.Import(context.Background(), "exits", "", claude.URL+"#Claude\n"+openai.URL+"#OpenAI"); err != nil {
		t.Fatal(err)
	}
	view := s.State()
	settings := view.Settings
	settings.CorePath = core
	ports := unusedPorts(t)
	settings.Routing.ProxyPort, settings.Routing.Rules[0].Port, settings.Routing.Rules[1].Port = ports[0], ports[1], ports[2]
	settings.Routing.Rules[0].Node = view.Groups[0].Nodes[0].ID
	settings.Routing.Rules[1].Node = view.Groups[0].Nodes[1].ID
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err, s.State().Status.Logs)
	}
	base := []string{"HTTP_PROXY=http://old:5800", "all_proxy=socks5://old:5800", "no_proxy=*", "HOME=/example", "API_KEY=keep",
		// D4：携带继承 marker（嵌套 CodeBox 场景）——启用路径必须剔旧值后唯一
		// 重建，不得出现两条 marker。
		"AMAGI_CODEBOX_AI_PROXY=1"}
	env, err := s.ApplyEnv(base)
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(strings.Join(env, "\n"), "AMAGI_CODEBOX_AI_PROXY="); c != 1 {
		t.Fatalf("enabled path must rebuild the marker exactly once, got %d: %v", c, env)
	}
	m := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	if m["HTTPS_PROXY"] != fmt.Sprintf("http://127.0.0.1:%d", ports[0]) || m["NO_PROXY"] != "localhost,127.0.0.1,::1" || m["API_KEY"] != "keep" || m["all_proxy"] != m["HTTPS_PROXY"] {
		t.Fatal("wrong environment overlay")
	}
	proxyURL, _ := url.Parse(m["HTTPS_PROXY"])
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for target, want := range map[string]string{"http://api.anthropic.com/test": "CLAUDE", "http://api.openai.com/test": "OPENAI"} {
		resp, e := client.Get(target)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != want {
			t.Fatalf("route got %s", body)
		}
	}
	domestic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "DIRECT") }))
	defer domestic.Close()
	resp, err := client.Get(domestic.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "DIRECT" {
		t.Fatal("unmatched requests did not use direct fallback")
	}
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]"} {
		req, _ := http.NewRequest("GET", "http://"+host+":8787/test", nil)
		p, e := s.ProxyForRequest(req)
		if e != nil || p != nil {
			t.Fatal("local relay did not bypass forward proxy", e)
		}
	}
	// Draft port changes must not poison new sessions before apply.
	draft := s.State().Settings
	draft.Routing.ProxyPort = unusedPorts(t)[0]
	if err = s.SaveSettings(draft); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ApplyEnv(base)
	if err != nil || !strings.Contains(strings.Join(pending, "\n"), "HTTPS_PROXY="+m["HTTPS_PROXY"]) {
		t.Fatal("unapplied port used")
	}
	// Reopening restores the enabled core and saved settings after clean close.
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	copyService := New(s.dir)
	if err = copyService.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer copyService.Close()
	if !copyService.State().Status.Running {
		t.Fatal("enabled proxy not restored")
	}
	p, err := os.FindProcess(copyService.State().Status.PID)
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Kill()
	deadline := time.Now().Add(2 * time.Second)
	for copyService.State().Status.Running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = copyService.ApplyEnv(base); err == nil {
		t.Fatal("dead core silently fell back to old system proxy")
	}
	if err = copyService.Stop(); err != nil {
		t.Fatal(err)
	}
	after, err := copyService.ApplyEnv(base)
	if err != nil || !reflect.DeepEqual(after, base) {
		t.Fatal("stop did not restore original launch policy")
	}
}
func TestProxyVariablesPinnedAfterShellProfiles(t *testing.T) {
	spec := platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapShellInline, Shell: &platform.ResolvedShell{Key: "zsh"}, StartupCommand: "pi --model sample", Env: platform.ResolvedEnv{Variables: []string{"AMAGI_CODEBOX_AI_PROXY=1", "HTTPS_PROXY=http://127.0.0.1:18890", "NO_PROXY=localhost,127.0.0.1,::1"}}}
	got, err := PinProxyEnvironment(spec)
	if err != nil || !strings.Contains(got.StartupCommand, "env 'HTTPS_PROXY=http://127.0.0.1:18890'") {
		t.Fatal("login shell could override chosen proxy", err)
	}
	spec.BootstrapMode = platform.BootstrapWSL
	// 钉住 USERPROFILE 到无 .wslconfig 的临时目录：宿主真机可能已切 mirrored，
	// 闸门判定必须与宿主实际配置解耦（WSLNetworkingMode 按 home 缓存，临时目录独立探针）。
	spec.Env.Variables = append(spec.Env.Variables, "USERPROFILE="+t.TempDir())
	if _, err = PinProxyEnvironment(spec); err == nil {
		t.Fatal("assumed WSL host loopback reachability")
	}
}
