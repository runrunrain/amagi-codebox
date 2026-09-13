package remote

// webui_proxy_test.go — remote-webui-plane 切片（C1/C2）行为验证：
//
//   - /webui/{sid}/... 反向代理：sid 严格白名单（穿越 404）、device cookie
//     鉴权（未授权 401）、出向头改写（Host=127.0.0.1:port / 删 Origin /
//     Bearer 覆盖式注入）、WS upgrade 子协议透传往返、/api/input 控制门
//     （无控制权 403 / 持有控制权放行 / 桌面持有时 403）、probing→503 与
//     其余非 available→404、legacy 无安全面 fail-closed 404；
//   - GET /api/remote/v1/session/{id}/webui 状态端点：形状（available 携带
//     fragment url / probing 无 url）、405/401/400/403 门、契约校验、冻结
//     10 端点清单不受影响；
//   - 安全红线：capability token 不出现在任何错误体。
//
// 假后端（httptest.Server）模拟 pi webui server：断言收到的 Host 改写正确、
// Origin 缺失、Bearer 正确；/ws/events 用 gorilla/websocket 做 upgrade 并
// 回显子协议（仓库既有 vendored 依赖，复用）。

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/webui"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Fake pi webui backend
// ---------------------------------------------------------------------------

const webuiTestToken = "test-capability-token-123"

type webuiBackendProbe struct {
	path     string
	host     string
	auth     string
	origin   string
	rawQuery string
}

type webuiFakeBackend struct {
	mu            sync.Mutex
	ts            *httptest.Server
	probes        []webuiBackendProbe
	inputCalls    int
	interactCalls int
	wsUpgraded    bool
	wsSubproto    string
}

func newWebuiFakeBackend(t *testing.T) *webuiFakeBackend {
	t.Helper()
	b := &webuiFakeBackend{}
	upgrader := websocket.Upgrader{
		Subprotocols: []string{"webui"},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/events", func(w http.ResponseWriter, r *http.Request) {
		if !b.assertInbound(w, r) {
			return
		}
		b.record(r)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		b.mu.Lock()
		b.wsUpgraded = true
		b.wsSubproto = conn.Subprotocol()
		b.mu.Unlock()
		// Echo one text frame to prove the proxied stream is bidirectional.
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteMessage(mt, msg)
	})
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		if !b.assertInbound(w, r) {
			return
		}
		b.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"v":1,"ready":true,"sessionId":"pi-sess-1","pid":42,"port":%d}`, b.backendPort())
	})
	mux.HandleFunc("/api/input", func(w http.ResponseWriter, r *http.Request) {
		if !b.assertInbound(w, r) {
			return
		}
		b.record(r)
		b.mu.Lock()
		b.inputCalls++
		b.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	// G2 Major-02：写面集合的其余两条（后端语义等价，供控制门用例断言转发）。
	mux.HandleFunc("/api/agent-interact", func(w http.ResponseWriter, r *http.Request) {
		if !b.assertInbound(w, r) {
			return
		}
		b.record(r)
		b.mu.Lock()
		b.interactCalls++
		b.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/draft", func(w http.ResponseWriter, r *http.Request) {
		if !b.assertInbound(w, r) {
			return
		}
		b.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	// 读面（diting 裁定：配对设备可读，不设控制门）。
	mux.HandleFunc("/api/fs/dirs", func(w http.ResponseWriter, r *http.Request) {
		if !b.assertInbound(w, r) {
			return
		}
		b.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dirs":["~/projects"]}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !b.assertInbound(w, r) {
			return
		}
		b.record(r)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>pi-webui</html>"))
	})
	b.ts = httptest.NewServer(mux)
	t.Cleanup(b.ts.Close)
	return b
}

// assertInbound enforces the pi webui server's own acceptance rules on every
// fake-backend route: strict loopback Host, Origin absent OR "null" (opaque
// sandbox iframe — the backend allowlist accepts null and answers
// Access-Control-Allow-Origin: null on preflights; see amagi-pi webui
// server.ts isAllowedOrigin), correct Bearer token. A violation answers
// 401/403 (never recorded as a legal probe).
func (b *webuiFakeBackend) assertInbound(w http.ResponseWriter, r *http.Request) bool {
	if r.Host != fmt.Sprintf("127.0.0.1:%d", b.backendPort()) {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	if ov := r.Header.Get("Origin"); ov != "" && ov != "null" {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	if r.Header.Get("Authorization") != "Bearer "+webuiTestToken {
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	return true
}

// record snapshots the inbound request headers the proxy must have rewritten.
func (b *webuiFakeBackend) record(r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probes = append(b.probes, webuiBackendProbe{
		path:     r.URL.Path,
		host:     r.Host,
		auth:     r.Header.Get("Authorization"),
		origin:   r.Header.Get("Origin"),
		rawQuery: r.URL.RawQuery,
	})
}

func (b *webuiFakeBackend) backendPort() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ts == nil {
		return 0
	}
	host := strings.TrimPrefix(b.ts.URL, "http://")
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		port := 0
		for _, c := range []byte(host[i+1:]) {
			if c < '0' || c > '9' {
				return 0
			}
			port = port*10 + int(c-'0')
		}
		return port
	}
	return 0
}

func (b *webuiFakeBackend) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.probes)
}

func (b *webuiFakeBackend) inputCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inputCalls
}

func (b *webuiFakeBackend) interactCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.interactCalls
}

func (b *webuiFakeBackend) lastProbe() webuiBackendProbe {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.probes) == 0 {
		return webuiBackendProbe{}
	}
	return b.probes[len(b.probes)-1]
}

func (b *webuiFakeBackend) wsState() (bool, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wsUpgraded, b.wsSubproto
}

// ---------------------------------------------------------------------------
// Test app + fixture
// ---------------------------------------------------------------------------

// webuiProxyTestApp serves per-session SessionWebUIInfo snapshots.
type webuiProxyTestApp struct {
	*b2aSpyApp
	mu     sync.Mutex
	infos  map[string]SessionWebUIInfo
	probes []string
}

func (a *webuiProxyTestApp) setInfo(sid string, info SessionWebUIInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.infos == nil {
		a.infos = map[string]SessionWebUIInfo{}
	}
	a.infos[sid] = info
}

func (a *webuiProxyTestApp) GetSessionWebUI(sid string) (SessionWebUIInfo, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if info, ok := a.infos[sid]; ok {
		return info, true
	}
	return SessionWebUIInfo{State: "unknown"}, true
}

func (a *webuiProxyTestApp) ProbeSessionWebUI(sid string) (SessionWebUIInfo, bool) {
	a.mu.Lock()
	a.probes = append(a.probes, sid)
	info, ok := a.infos[sid]
	a.mu.Unlock()
	if !ok {
		return SessionWebUIInfo{State: "unknown"}, true
	}
	return info, true
}

func (a *webuiProxyTestApp) probeCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.probes)
}

type webuiProxyFixture struct {
	ts       *httptest.Server
	srv      *Server
	backend  *webuiFakeBackend
	app      *webuiProxyTestApp
	cookie   *http.Cookie
	deviceID string
}

func newWebUIProxyFixture(t *testing.T) *webuiProxyFixture {
	t.Helper()
	backend := newWebuiFakeBackend(t)
	app := &webuiProxyTestApp{b2aSpyApp: &b2aSpyApp{}}
	clk := newSecFakeClock(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	opts := newSecurityOptions(t.TempDir(), validHostSummary, clk, rand.Reader, NewVolatileSecurityEventSink())
	srv := NewServerWithSecurity(0, app, logging.NewService(t.TempDir()), embed.FS{}, opts)
	t.Cleanup(srv.log.Close)
	if err := srv.LoadSecurityState(); err != nil {
		t.Fatalf("LoadSecurityState: %v", err)
	}
	srv.pairing.Resume()
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	t.Cleanup(ts.Close)
	wireTestPort(srv, ts)
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, pairingRequest(ts, code, "phone", ts.URL))
	if rr.Code != contract.V1RestEndpoints[0].SuccessStatus {
		t.Fatalf("pair: %d %s", rr.Code, rr.Body.String())
	}
	var resp contract.PairingCompleteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return &webuiProxyFixture{
		ts:       ts,
		srv:      srv,
		backend:  backend,
		app:      app,
		cookie:   rr.Result().Cookies()[0],
		deviceID: string(resp.Device.ID),
	}
}

// markAvailable points the session's plane at the fake backend.
func (f *webuiProxyFixture) markAvailable(sid string) {
	f.app.setInfo(sid, SessionWebUIInfo{State: "available", Port: f.backend.backendPort(), Token: webuiTestToken})
}

// do sends a real proxied request through the live test server with the paired
// device cookie and attacker-supplied Origin/Authorization headers (the proxy
// must delete/overwrite them outbound). Returns (nil, "") on transport error.
func (f *webuiProxyFixture) do(method, path, body string) (*http.Response, string) {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rdr)
	if err != nil {
		return nil, ""
	}
	req.Header.Set("Origin", f.ts.URL)                       // must be deleted outbound
	req.Header.Set("Authorization", "Bearer attacker-value") // must be overwritten outbound
	req.AddCookie(f.cookie)
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		return nil, ""
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(raw)
}

// holdControl wires the session adapter, activates the session, and acquires
// control for the paired device (the same gate the /ws/v1 input path consults).
func (f *webuiProxyFixture) holdControl(t *testing.T, sessionID string) {
	t.Helper()
	adapter, _, _, _ := setupAdapterTest(t)
	f.srv.SetSessionAdapter(adapter)
	activateTestSession(t, adapter, contract.SessionID(sessionID))
	rt := adapter.Runtime()
	lease, _ := rt.Directory().Attach(contract.DeviceID(f.deviceID), "phone", "conn-webui-test", contract.SessionID(sessionID))
	if lease == nil {
		t.Fatal("attach lease nil")
	}
	principal := newTestDevicePrincipal(f.deviceID, "phone")
	if _, gErr := rt.Arbiter().Acquire(principal, lease, contract.SessionID(sessionID)); gErr != nil {
		t.Fatalf("acquire: %v", gErr)
	}
}

// ---------------------------------------------------------------------------
// Proxy behavior
// ---------------------------------------------------------------------------

// TestWebUIProxy_Unauthorized401：无 device cookie 的代理请求 401（v1 错误
// 映射风格），后端零调用，token 不外泄。
func TestWebUIProxy_Unauthorized401(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-a")
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+"/webui/sess-a/api/info", nil)
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", resp.StatusCode)
	}
	if !strings.Contains(string(raw), string(contract.ErrorCodeAuthUnpaired)) {
		t.Fatalf("body must be v1-shaped auth error: %s", raw)
	}
	if strings.Contains(string(raw), webuiTestToken) {
		t.Fatalf("token leaked in error body: %s", raw)
	}
	if f.backend.calls() != 0 {
		t.Fatalf("backend must not be touched, got %d calls", f.backend.calls())
	}
}

// TestWebUIProxy_SessionIDWhitelist：sid 白名单外的路径（穿越/空/含点/
// 编码斜杠）一律 404 且后端零调用；合法 sid（含 _ - 字符）正常代理。
func TestWebUIProxy_SessionIDWhitelist(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-ok_1-2")

	for _, path := range []string{
		"/webui/",
		"/webui/../api/remote/v1/sessions",
		"/webui/./api",
		"/webui/a.b/api/info",
		"/webui/a%2Fb/api/info",
		"/webui/../../etc/passwd",
	} {
		req, _ := http.NewRequest(http.MethodGet, f.ts.URL+path, nil)
		req.AddCookie(f.cookie)
		resp, err := f.ts.Client().Do(req)
		if err != nil {
			t.Fatalf("path %s: %v", path, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("path %s: status=%d want 404 (body=%s)", path, resp.StatusCode, raw)
		}
	}
	if f.backend.calls() != 0 {
		t.Fatalf("backend must not be touched by invalid sids, got %d calls", f.backend.calls())
	}

	resp, body := f.do(http.MethodGet, "/webui/sess-ok_1-2/api/info", "")
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("valid sid proxy: status=%v body=%s", resp, body)
	}
	if !strings.Contains(body, `"ready":true`) {
		t.Fatalf("backend body not forwarded: %s", body)
	}
}

// TestWebUIProxy_HeaderRewrite：后端断言收到 Host=127.0.0.1:{port}、无
// Origin、Authorization 为覆盖注入的 Bearer token（客户端自带值被覆盖）；
// 根路径 /webui/{sid}/ 代理后端 "/"（iframe 入口）。
func TestWebUIProxy_HeaderRewrite(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-hdr")

	resp, body := f.do(http.MethodGet, "/webui/sess-hdr/", "")
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("index proxy: status=%v body=%s", resp, body)
	}
	if !strings.Contains(body, "pi-webui") {
		t.Fatalf("index not served: %s", body)
	}
	probe := f.backend.lastProbe()
	if probe.path != "/" {
		t.Fatalf("outbound path=%q want \"/\"", probe.path)
	}
	if probe.host != fmt.Sprintf("127.0.0.1:%d", f.backend.backendPort()) {
		t.Fatalf("outbound Host=%q want 127.0.0.1:{port}", probe.host)
	}
	if probe.origin != "" {
		t.Fatalf("outbound Origin must be deleted, got %q", probe.origin)
	}
	if probe.auth != "Bearer "+webuiTestToken {
		t.Fatalf("outbound Authorization=%q want overwritten Bearer token", probe.auth)
	}
}

// TestWebUIProxy_TokenChannelDualAuth：capability-token 降级通道（浏览器 E2E
// 实证：sandbox iframe 的 SameSite cookie 被 Chrome 抑制，页面只能凭
// Authorization Bearer / WS 子协议自证）。读面放行、错误 token 401、
// 写面零 principal → 控制门 403；有效 cookie 时不受影响（cookie 优先）。
func TestWebUIProxy_TokenChannelDualAuth(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-tok")

	do := func(path, method, auth string) int {
		req, _ := http.NewRequest(method, f.ts.URL+path, nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		resp, err := f.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := do("/webui/sess-tok/api/info", http.MethodGet, webuiTestToken); code != http.StatusOK {
		t.Fatalf("read face via token: %d want 200", code)
	}
	if code := do("/webui/sess-tok/api/info", http.MethodGet, "wrong-token-aaaaaaaaaaaaaaaaaa"); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d want 401", code)
	}
	if code := do("/webui/sess-tok/api/input", http.MethodPost, webuiTestToken); code != http.StatusForbidden {
		t.Fatalf("write face via token (no device): %d want 403", code)
	}
}

// TestWebUIProxy_StaticPlaneNoCookie：静态面（HTML 入口 / ./assets）豁免
// device-cookie 鉴权（CORS-mode 子资源跨源永不携带 cookie；对齐后端 §6.3
// 静态公开语义），数据面 /api/*、写面仍硬门。浏览器 E2E 发现（G2 追补）。
func TestWebUIProxy_StaticPlaneNoCookie(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-static")

	// 无 cookie 静态入口：200（后端静态页面透传）
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+"/webui/sess-static/", nil)
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "pi-webui") {
		t.Fatalf("static entry: status=%d body=%s", resp.StatusCode, body)
	}
	// 无 cookie assets：200；无 cookie /api/info：仍 401（数据面硬门不豁免）
	req, _ = http.NewRequest(http.MethodGet, f.ts.URL+"/webui/sess-static/assets/app.js", nil)
	resp, err = f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("static asset: status=%d want 200", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, f.ts.URL+"/webui/sess-static/api/info", nil)
	resp, err = f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("data face must stay cookie-gated: status=%d want 401", resp.StatusCode)
	}
}

// TestWebUIProxy_OriginNullPassthrough：sandbox opaque origin（iframe）请求携带
// Origin: null → 出向透传（后端接受 null 且预检据此回 ACAO:null，删头会让
// opaque 页面的 CORS 预检失败、实际数据请求发不出去——G1 集成验证实证）。
func TestWebUIProxy_OriginNullPassthrough(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-opaque")

	req, err := http.NewRequest(http.MethodGet, f.ts.URL+"/webui/sess-opaque/api/info", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "null")
	req.AddCookie(f.cookie)
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	probe := f.backend.lastProbe()
	if probe.origin != "null" {
		t.Fatalf("outbound Origin=%q want passthrough \"null\"", probe.origin)
	}
	if probe.auth != "Bearer "+webuiTestToken {
		t.Fatalf("outbound Authorization=%q want overwritten Bearer token", probe.auth)
	}
}

// TestWebUIProxy_WSUpgradeSubprotocol：/ws/events 经同一代理完成 WS
// upgrade，Sec-WebSocket-Protocol 子协议往返透传，消息双向可通，后端
// upgrade 请求同样完成头改写。
func TestWebUIProxy_WSUpgradeSubprotocol(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-ws")

	wsURL := strings.Replace(f.ts.URL, "http://", "ws://", 1) + "/webui/sess-ws/ws/events"
	dialer := &websocket.Dialer{
		Subprotocols:     []string{"webui"},
		HandshakeTimeout: 5 * time.Second,
	}
	header := http.Header{}
	header.Set("Cookie", f.cookie.Name+"="+f.cookie.Value)
	conn, dialResp, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial proxied ws: %v (resp=%v)", err, dialResp)
	}
	defer conn.Close()
	if sub := conn.Subprotocol(); sub != "webui" {
		t.Fatalf("client subprotocol=%q want webui", sub)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello-plane")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(msg) != "hello-plane" {
		t.Fatalf("echo=%q", msg)
	}
	upgraded, backendSub := f.backend.wsState()
	if !upgraded || backendSub != "webui" {
		t.Fatalf("backend upgrade=%v subprotocol=%q", upgraded, backendSub)
	}
	probe := f.backend.lastProbe()
	if probe.path != "/ws/events" {
		t.Fatalf("ws outbound path=%q", probe.path)
	}
	if probe.origin != "" || probe.auth != "Bearer "+webuiTestToken {
		t.Fatalf("ws outbound headers: origin=%q auth=%q", probe.origin, probe.auth)
	}
}

// TestWebUIProxy_InputControlGate：/api/input 无控制 runtime（宁紧勿松）
// 与无控制权均 → 403 control.forbidden（后端零调用）；持有控制权后放行。
func TestWebUIProxy_InputControlGate(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-ctl")

	// 未接线控制 runtime：宁紧勿松 → 403。
	resp, body := f.do(http.MethodPost, "/webui/sess-ctl/api/input", `{"text":"hi"}`)
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("input without control runtime: status=%v body=%s", resp, body)
	}
	if !strings.Contains(body, string(contract.ErrorCodeControlForbidden)) {
		t.Fatalf("body must carry control.forbidden: %s", body)
	}
	if strings.Contains(body, webuiTestToken) {
		t.Fatalf("token leaked in 403 body: %s", body)
	}
	if f.backend.inputCount() != 0 {
		t.Fatalf("backend input must not be touched, got %d", f.backend.inputCount())
	}

	// 持有控制权：放行转发。
	f.holdControl(t, "sess-ctl")
	resp, body = f.do(http.MethodPost, "/webui/sess-ctl/api/input", `{"text":"hi"}`)
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("input with control: status=%v body=%s", resp, body)
	}
	if f.backend.inputCount() != 1 {
		t.Fatalf("backend input calls=%d want 1", f.backend.inputCount())
	}
}

// TestWebUIProxy_InputControlGate_DesktopHolder：控制权被桌面持有时，
// 移动设备 /api/input 仍 403（与 /ws/v1 input 同一 control_permit 判定）。
func TestWebUIProxy_InputControlGate_DesktopHolder(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-dt")

	adapter, _, _, _ := setupAdapterTest(t)
	f.srv.SetSessionAdapter(adapter)
	activateTestSession(t, adapter, "sess-dt")
	if gErr := adapter.Gate().TakeDesktop(context.Background(), newWailsAuthority(7), "sess-dt"); gErr != nil {
		t.Fatalf("desktop take: %v", gErr)
	}

	resp, body := f.do(http.MethodPost, "/webui/sess-dt/api/input", `{"text":"hi"}`)
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("input under desktop holder: status=%v body=%s", resp, body)
	}
	if f.backend.inputCount() != 0 {
		t.Fatalf("backend input must not be touched, got %d", f.backend.inputCount())
	}
}

// TestWebUIProxy_StateMapping：probing→503（service.down，可重试）；
// unavailable/ended/unknown→404；错误体均不含 token；后端零调用。
func TestWebUIProxy_StateMapping(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.app.setInfo("sess-probe", SessionWebUIInfo{State: "probing"})
	f.app.setInfo("sess-unav", SessionWebUIInfo{State: "unavailable"})
	f.app.setInfo("sess-ended", SessionWebUIInfo{State: "ended", Port: 9999, Token: webuiTestToken})

	resp, body := f.do(http.MethodGet, "/webui/sess-probe/api/info", "")
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("probing: status=%v body=%s", resp, body)
	}
	if !strings.Contains(body, string(contract.ErrorCodeServiceDown)) {
		t.Fatalf("probing body must carry service.down: %s", body)
	}

	for _, sid := range []string{"sess-unav", "sess-ended", "sess-nosuch"} {
		resp, body = f.do(http.MethodGet, "/webui/"+sid+"/api/info", "")
		if resp == nil || resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status=%v body=%s", sid, resp, body)
		}
		if strings.Contains(body, webuiTestToken) {
			t.Fatalf("%s: token leaked: %s", sid, body)
		}
	}
	if f.backend.calls() != 0 {
		t.Fatalf("backend must not be touched, got %d calls", f.backend.calls())
	}
}

// TestWebUIProxy_PreflightExempt_NoCookie（G2 Info-08①，锁死 Critical-01-a）:
// sandbox opaque origin 的 CORS 预检（OPTIONS + Origin:null + 非空 ACRM）
// 按 CORS 规范不携带凭据，代理在鉴权/状态查询之前本地应答 204 + 完整
// CORS 头；不转发后端（会话未注册/后端零调用同样成立）。
func TestWebUIProxy_PreflightExempt_NoCookie(t *testing.T) {
	f := newWebUIProxyFixture(t) // 不注册任何会话：预检先于状态映射

	req, _ := http.NewRequest(http.MethodOptions, f.ts.URL+"/webui/sess-pre/api/info", nil)
	req.Header.Set("Origin", "null")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status=%d want 204 (no cookie, no session)", resp.StatusCode)
	}
	h := resp.Header
	if got := h.Get("Access-Control-Allow-Origin"); got != "null" {
		t.Fatalf("ACAO=%q want null", got)
	}
	if !varyHasOrigin(h.Values("Vary")) {
		t.Fatalf("Vary must carry Origin: %v", h.Values("Vary"))
	}
	if got := h.Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, OPTIONS" {
		t.Fatalf("ACAM=%q", got)
	}
	if got := h.Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type" {
		t.Fatalf("ACAH=%q", got)
	}
	if got := h.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("ACAC=%q want true", got)
	}
	if f.backend.calls() != 0 {
		t.Fatalf("preflight must be answered locally, backend got %d calls", f.backend.calls())
	}
}

// TestWebUIProxy_PreflightExemptionNotAbusable（G2 Info-08②）：预检豁免
// 不可滥用——非 OPTIONS（伪造 ACRM）、ACRM 齐备但 Origin 非 null、Origin
// null 但无 ACRM（非预检 OPTIONS）三类均维持鉴权路径：无 cookie 一律 401。
func TestWebUIProxy_PreflightExemptionNotAbusable(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-ab")

	cases := []struct {
		name   string
		method string
		origin string
		acrm   string
	}{
		{"forged ACRM on GET", http.MethodGet, "null", http.MethodPost},
		{"OPTIONS with non-null origin", http.MethodOptions, f.ts.URL, http.MethodPost},
		{"OPTIONS without ACRM", http.MethodOptions, "null", ""},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(tc.method, f.ts.URL+"/webui/sess-ab/api/info", nil)
		req.Header.Set("Origin", tc.origin)
		if tc.acrm != "" {
			req.Header.Set("Access-Control-Request-Method", tc.acrm)
		}
		resp, err := f.ts.Client().Do(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: status=%d want 401 (body=%s)", tc.name, resp.StatusCode, raw)
		}
	}
	if f.backend.calls() != 0 {
		t.Fatalf("backend must not be touched, got %d calls", f.backend.calls())
	}
}

// TestWebUIProxy_WriteFaces_ControlGate（G2 Info-08③ / Major-02）：观察者
// （已配对、无控制权）POST /api/agent-interact 与 PUT /api/draft 均 403
// control.forbidden（后端零调用，token 不外泄）；GET /api/fs/dirs 读面维持
// 配对设备可读（diting 裁定）；持有控制权后写面放行；/api/* 响应（含
// 错误）携带 Cache-Control: no-store（Minor-04）。
func TestWebUIProxy_WriteFaces_ControlGate(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-wf")

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/agent-interact"},
		{http.MethodPut, "/api/draft"},
	} {
		resp, body := f.do(tc.method, "/webui/sess-wf"+tc.path, `{"x":1}`)
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s observer: status=%v body=%s", tc.method, tc.path, resp, body)
		}
		if !strings.Contains(body, string(contract.ErrorCodeControlForbidden)) {
			t.Fatalf("%s %s: body must carry control.forbidden: %s", tc.method, tc.path, body)
		}
		if strings.Contains(body, webuiTestToken) {
			t.Fatalf("%s %s: token leaked: %s", tc.method, tc.path, body)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("%s %s: Cache-Control=%q want no-store", tc.method, tc.path, cc)
		}
	}
	if f.backend.interactCount() != 0 {
		t.Fatalf("backend agent-interact must not be touched, got %d", f.backend.interactCount())
	}

	// 读面：fs/dirs 维持配对设备可读，200 转发且携带 no-store。
	resp, body := f.do(http.MethodGet, "/webui/sess-wf/api/fs/dirs", "")
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("fs/dirs read face: status=%v body=%s", resp, body)
	}
	if !strings.Contains(body, `"dirs"`) {
		t.Fatalf("fs/dirs body not forwarded: %s", body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("fs/dirs: Cache-Control=%q want no-store", cc)
	}

	// 持有控制权后写面放行。
	f.holdControl(t, "sess-wf")
	resp, body = f.do(http.MethodPost, "/webui/sess-wf/api/agent-interact", `{"text":"go"}`)
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("agent-interact with control: status=%v body=%s", resp, body)
	}
	if f.backend.interactCount() != 1 {
		t.Fatalf("backend agent-interact calls=%d want 1", f.backend.interactCount())
	}
	resp, body = f.do(http.MethodPut, "/webui/sess-wf/api/draft", `{"d":"x"}`)
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("draft with control: status=%v body=%s", resp, body)
	}
}

// TestWebUIProxy_QueryStringPassthrough（G2 Info-08④）：带查询串的代理
// 转发——后端收到原样 path + RawQuery（代理自身不添加/不丢弃 query）。
func TestWebUIProxy_QueryStringPassthrough(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.markAvailable("sess-q")

	resp, body := f.do(http.MethodGet, "/webui/sess-q/api/history?limit=5&since=42", "")
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("query proxy: status=%v body=%s", resp, body)
	}
	probe := f.backend.lastProbe()
	if probe.path != "/api/history" {
		t.Fatalf("backend path=%q want /api/history", probe.path)
	}
	if probe.rawQuery != "limit=5&since=42" {
		t.Fatalf("backend RawQuery=%q want limit=5&since=42", probe.rawQuery)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control=%q want no-store", cc)
	}
}

// TestWebUIProxy_LegacyServerFailClosed：无安全面（legacy NewServer）时
// 代理面 fail-closed 404。
func TestWebUIProxy_LegacyServerFailClosed(t *testing.T) {
	srv := NewServer(0, &b2aSpyApp{}, logging.NewService(t.TempDir()), embed.FS{})
	h := srv.buildHandler()
	r := httptest.NewRequest(http.MethodGet, "/webui/sess/api/info", nil)
	rr := rec(h, r)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("legacy server: status=%d want 404", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// v1 status endpoint
// ---------------------------------------------------------------------------

func webuiStatusPath(sid string) string {
	prefix := contract.RESTBasePath + contract.WebUIStatusEndpoint.Path[:strings.Index(contract.WebUIStatusEndpoint.Path, "{id}")]
	return prefix + sid + "/webui"
}

func webuiStatusReq(f *webuiProxyFixture, method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Host = strings.TrimPrefix(f.ts.URL, "http://")
	r.Header.Set("Origin", f.ts.URL)
	if f.cookie != nil {
		r.AddCookie(f.cookie)
	}
	return r
}

// TestV1WebUIStatus_Shape：available → 200 {state,url}（url 为
// /webui/{sid}/#/t={token}，token 只在 fragment）；probing → 无 url；
// 未注册 → unknown（200，状态机语义优先于 404）；响应通过契约校验。
func TestV1WebUIStatus_Shape(t *testing.T) {
	f := newWebUIProxyFixture(t)
	f.app.setInfo("sess-av", SessionWebUIInfo{State: "available", Port: 4321, Token: webuiTestToken})
	f.app.setInfo("sess-pr", SessionWebUIInfo{State: "probing"})
	h := f.srv.buildV1Handler()

	// available：url 携带 fragment token。
	rr := rec(h, webuiStatusReq(f, http.MethodGet, webuiStatusPath("sess-av")))
	if rr.Code != contract.WebUIStatusEndpoint.SuccessStatus {
		t.Fatalf("available: %d %s", rr.Code, rr.Body.String())
	}
	var st contract.WebUIStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.State != contract.WebUIPlaneStateAvailable {
		t.Fatalf("state=%q", st.State)
	}
	if st.URL != "/webui/sess-av/#/t="+webuiTestToken {
		t.Fatalf("url=%q", st.URL)
	}
	if err := contract.ValidateWebUIStatus(st); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if f.app.probeCalls() != 1 {
		t.Fatalf("endpoint must probe once, got %d", f.app.probeCalls())
	}

	// probing：无 url。
	rr = rec(h, webuiStatusReq(f, http.MethodGet, webuiStatusPath("sess-pr")))
	if rr.Code != contract.WebUIStatusEndpoint.SuccessStatus {
		t.Fatalf("probing: %d %s", rr.Code, rr.Body.String())
	}
	st = contract.WebUIStatus{}
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.State != contract.WebUIPlaneStateProbing || st.URL != "" {
		t.Fatalf("probing shape: %+v", st)
	}

	// 未注册会话 → unknown（200）。
	rr = rec(h, webuiStatusReq(f, http.MethodGet, webuiStatusPath("sess-none")))
	if rr.Code != contract.WebUIStatusEndpoint.SuccessStatus {
		t.Fatalf("unknown: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"unknown"`) {
		t.Fatalf("unknown body: %s", rr.Body.String())
	}
}

// TestV1WebUIStatus_Gates：错误方法 405+Allow；无 cookie 401；query 400；
// bad Host 403；未知扩展路径仍 404。
func TestV1WebUIStatus_Gates(t *testing.T) {
	f := newWebUIProxyFixture(t)
	h := f.srv.buildV1Handler()

	rr := rec(h, webuiStatusReq(f, http.MethodPost, webuiStatusPath("sess-x")))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method: %d", rr.Code)
	}
	if allow := rr.Result().Header.Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Fatalf("Allow=%q", allow)
	}

	noAuth := webuiStatusReq(f, http.MethodGet, webuiStatusPath("sess-x"))
	noAuth.Header.Del("Cookie")
	rr = rec(h, noAuth)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", rr.Code)
	}

	rq := webuiStatusReq(f, http.MethodGet, webuiStatusPath("sess-x")+"?x=1")
	rr = rec(h, rq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("query: %d", rr.Code)
	}

	rh := webuiStatusReq(f, http.MethodGet, webuiStatusPath("sess-x"))
	rh.Host = "127.0.0.1:1"
	rr = rec(h, rh)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bad host: %d", rr.Code)
	}

	rr = rec(h, webuiStatusReq(f, http.MethodGet, contract.RESTBasePath+"/session/sess-x/nope"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown ext path: %d", rr.Code)
	}
}

// TestV1WebUIStatus_FrozenManifestUntouched：扩展路由不进入冻结的 10 端点
// 清单（fixture parity 约束，wire_test.go 冻结计数）。
func TestV1WebUIStatus_FrozenManifestUntouched(t *testing.T) {
	if len(contract.V1RestEndpoints) != 10 {
		t.Fatalf("V1RestEndpoints must stay at 10, got %d", len(contract.V1RestEndpoints))
	}
	for _, ep := range contract.V1RestEndpoints {
		if strings.HasPrefix(ep.Path, "/session/") {
			t.Fatalf("extension route leaked into the frozen manifest: %+v", ep)
		}
	}
}

// ---------------------------------------------------------------------------
// webui.Service.SessionEndpoint（internal/webui 联动锚）
// ---------------------------------------------------------------------------

// TestWebUIServiceSessionEndpoint：远端切片依赖的端口/token 访问器语义 ——
// 未注册 (0,"")；RegisterSession+探测 available 后返回端口与 token；
// RemoveSession 后归零。假后端即上方 pi webui 模拟。
func TestWebUIServiceSessionEndpoint(t *testing.T) {
	backend := newWebuiFakeBackend(t)
	svc := webui.NewService(logging.NewService(t.TempDir()), t.TempDir())

	if port, token := svc.SessionEndpoint("s1"); port != 0 || token != "" {
		t.Fatalf("unregistered: got (%d,%q)", port, token)
	}
	svc.RegisterSession("s1", 42, backend.backendPort(), webuiTestToken)
	if st := svc.ProbeWebUI("s1"); st.State != webui.StateAvailable {
		t.Fatalf("probe state=%s", st.State)
	}
	port, token := svc.SessionEndpoint("s1")
	if port != backend.backendPort() || token != webuiTestToken {
		t.Fatalf("available: got (%d,%q)", port, token)
	}
	svc.RemoveSession("s1")
	if port, token := svc.SessionEndpoint("s1"); port != 0 || token != "" {
		t.Fatalf("removed: got (%d,%q)", port, token)
	}
}
