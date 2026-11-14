package claudewebui

// server_test.go — Manager/plane 的 HTTP 面契约测试（真实 listener 模式，
// 照 internal/webui/webui_test.go:31 的 startStubInfo 惯例：server 只 bind
// 127.0.0.1）。共享的 testPlane/newTranscriptWriter 辅助供本包各 _test.go 使用。

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/appmeta/claude"
)

const testToken = "0123456789abcdef0123456789abcdef"

// testPlane 是一个已启动的真实 plane（Manager 落临时目录日志）。
type testPlane struct {
	m       *Manager
	cfg     PlaneConfig
	input   *fakeInputPort
	port    int
	baseURL string
}

// newTestPlane 启动一个测试 plane；mutate 允许改写 cfg（Settings/端口等）。
func newTestPlane(t *testing.T, mutate func(*PlaneConfig)) *testPlane {
	t.Helper()
	cfg := PlaneConfig{
		SessionID:       "sess-1",
		ClaudeSessionID: "11111111-2222-3333-4444-555555555555",
		WorkDir:         `C:\work\demo`,
		TranscriptRoot:  t.TempDir(),
		PID:             4321,
		Token:           testToken,
		Settings:        RuntimeSettings{PollInterval: 15 * time.Millisecond, RingBuffer: 4096, HistoryLimit: 500},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	input := &fakeInputPort{}
	m := NewManager(nil) // 测试用 nil 日志：logging 句柄会卡住 Windows 的 t.TempDir 清理
	port, err := m.Start(cfg, input)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { m.Stop(cfg.SessionID) })
	return &testPlane{m: m, cfg: cfg, input: input, port: port, baseURL: fmt.Sprintf("http://127.0.0.1:%d", port)}
}

// transcriptPath 返回本 plane 锁定 sid 的转录文件路径（经 t1 助手构造，
// 测试不重复实现路径编码）。
func (tp *testPlane) transcriptPath() string {
	return claude.SessionJSONLPathInRoot(tp.cfg.TranscriptRoot, tp.cfg.WorkDir, tp.cfg.ClaudeSessionID)
}

// doReq 发送一条 HTTP 请求；headers 可为 nil；body 可为 ""（GET/无体）。
func doReq(t *testing.T, method, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// authed 返回带正确 Bearer 的公共头集合；extra 合并覆盖。
func authed(extra map[string]string) map[string]string {
	h := map[string]string{"Authorization": "Bearer " + testToken, "Content-Type": "application/json"}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

// decodeJSON 解码响应体到 v。
func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode json %q: %v", string(data), err)
	}
}

// waitFor 轮询等待条件成立（上限 d）；返回是否等到。
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// transcriptFixture 读取 t1 的真实转录样本（Claude Code 2.1.319 实测采集
// 脱敏固化；tailer 测试的权威数据源）。
func transcriptFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "appmeta", "claude", "testdata", "transcript.sample.jsonl"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// transcriptWriter 把 jsonl 行写入指定路径并推进 mtime（确定性触发尾随
// 读取——mtime 去重依赖单调递增的 ModTime）。
type transcriptWriter struct {
	t     *testing.T
	path  string
	mtime time.Time
}

func newTranscriptWriter(t *testing.T, path string) *transcriptWriter {
	return newTranscriptWriterAt(t, path, time.Now().Add(-time.Hour))
}

// newTranscriptWriterAt 允许指定基准 mtime（方案 P 回退测试需要两个文件
// mtime 严格有序）。
func newTranscriptWriterAt(t *testing.T, path string, base time.Time) *transcriptWriter {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	return &transcriptWriter{t: t, path: path, mtime: base}
}

func (w *transcriptWriter) write(lines ...string) {
	w.t.Helper()
	var buf strings.Builder
	for _, l := range lines {
		buf.WriteString(l)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		w.t.Fatalf("open transcript: %v", err)
	}
	if _, err := f.WriteString(buf.String()); err != nil {
		_ = f.Close()
		w.t.Fatalf("append transcript: %v", err)
	}
	_ = f.Close()
	w.mtime = w.mtime.Add(time.Second)
	if err := os.Chtimes(w.path, w.mtime, w.mtime); err != nil {
		w.t.Fatalf("chtimes: %v", err)
	}
}

// --- /api/info 探测契约 ------------------------------------------------------

func TestServerInfoContract(t *testing.T) {
	tp := newTestPlane(t, nil)
	resp := doReq(t, http.MethodGet, tp.baseURL+"/api/info", "", authed(nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("info status = %d, want 200", resp.StatusCode)
	}
	var info infoResponse
	decodeJSON(t, resp, &info)
	if info.V != 1 || !info.Ready {
		t.Fatalf("info v/ready = %d/%v, want 1/true", info.V, info.Ready)
	}
	if info.SessionID != tp.cfg.SessionID || info.PID != tp.cfg.PID || info.Port != tp.port {
		t.Fatalf("info 契约字段错位: sessionId=%q pid=%d port=%d（want %q/%d/%d）",
			info.SessionID, info.PID, info.Port, tp.cfg.SessionID, tp.cfg.PID, tp.port)
	}
	if info.AppType != "claudecode" {
		t.Fatalf("info appType = %q, want claudecode", info.AppType)
	}
}

// --- Bearer 强制与错误信封 ---------------------------------------------------

func TestServerBearerAuth(t *testing.T) {
	tp := newTestPlane(t, nil)

	// 缺 Authorization → 401。
	resp := doReq(t, http.MethodGet, tp.baseURL+"/api/info", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无鉴权 status = %d, want 401", resp.StatusCode)
	}
	var eb errorBody
	decodeJSON(t, resp, &eb)
	if eb.V != 1 || eb.Error == "" {
		t.Fatalf("错误体缺 v/error: %+v", eb)
	}

	// token 不等值 → 403（opaque iframe 需能读到：ACAO null）。
	resp = doReq(t, http.MethodGet, tp.baseURL+"/api/info", "", map[string]string{
		"Authorization": "Bearer " + strings.Repeat("ff", 16), "Origin": "null",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("错误 token status = %d, want 403", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "null" {
		t.Fatalf("Origin:null 的错误响应 ACAO = %q, want null", got)
	}
	data, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(data), testToken) {
		t.Fatalf("错误体泄漏 token: %q", string(data))
	}
}

// --- Host 校验 --------------------------------------------------------------

func TestServerHostValidation(t *testing.T) {
	tp := newTestPlane(t, nil)
	for _, host := range []string{"127.0.0.1", "127.0.0.1:8080"} {
		req, _ := http.NewRequest(http.MethodGet, tp.baseURL+"/api/info", nil)
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("host %q: %v", host, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("合法 host %q status = %d, want 200", host, resp.StatusCode)
		}
	}
	for _, host := range []string{"evil.example", "localhost", "127.0.0.2", "127.0.0.1:port"} {
		req, _ := http.NewRequest(http.MethodGet, tp.baseURL+"/api/info", nil)
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("host %q: %v", host, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("非法 host %q status = %d, want 403", host, resp.StatusCode)
		}
	}
}

// --- OPTIONS 预检本地 204 ----------------------------------------------------

func TestServerOptionsPreflight(t *testing.T) {
	tp := newTestPlane(t, nil)
	resp := doReq(t, http.MethodOptions, tp.baseURL+"/api/input", "", map[string]string{
		"Origin": "null", "Access-Control-Request-Method": "POST",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("预检 status = %d, want 204（且无需鉴权）", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "null" {
		t.Fatalf("预检 ACAO = %q, want null", got)
	}
	if hdr := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(hdr, "Authorization") || !strings.Contains(hdr, "Content-Type") {
		t.Fatalf("预检 ACRH 缺 Authorization/Content-Type: %q", hdr)
	}
}

// --- history limit 钳制与 truncatedHead --------------------------------------

func TestServerHistoryLimit(t *testing.T) {
	tp := newTestPlane(t, func(cfg *PlaneConfig) { cfg.Settings.HistoryLimit = 3 })
	w := newTranscriptWriter(t, tp.transcriptPath())
	w.write(
		`{"type":"user","uuid":"u-1","timestamp":"2026-11-14T00:00:01.000Z","origin":{"kind":"human"},"message":{"role":"user","content":"turn one"}}`,
		`{"type":"assistant","uuid":"a-1","timestamp":"2026-11-14T00:00:02.000Z","message":{"role":"assistant","model":"glm-5.3","content":[{"type":"text","text":"ok one"}]}}`,
		`{"type":"user","uuid":"u-2","timestamp":"2026-11-14T00:00:03.000Z","origin":{"kind":"human"},"message":{"role":"user","content":"turn two"}}`,
		`{"type":"assistant","uuid":"a-2","timestamp":"2026-11-14T00:00:04.000Z","message":{"role":"assistant","model":"glm-5.3","content":[{"type":"text","text":"ok two"}]}}`,
		`{"type":"assistant","uuid":"a-3","timestamp":"2026-11-14T00:00:05.000Z","message":{"role":"assistant","model":"glm-5.3","content":[{"type":"text","text":"ok three"}]}}`,
	)
	if !waitFor(t, 2*time.Second, func() bool {
		resp := doReq(t, http.MethodGet, tp.baseURL+"/api/history?limit=500", "", authed(nil))
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var h historyResponse
		decodeJSON(t, resp, &h)
		return len(h.Items) == 3
	}) {
		t.Fatalf("history 未收敛到缓冲上限 3")
	}

	resp := doReq(t, http.MethodGet, tp.baseURL+"/api/history", "", authed(nil))
	var h historyResponse
	decodeJSON(t, resp, &h)
	if len(h.Items) != 3 {
		t.Fatalf("缺省 limit items = %d, want 3（钳到 HistoryLimit）", len(h.Items))
	}
	if !h.TruncatedHead {
		t.Fatalf("缓冲丢最旧后 truncatedHead = false, want true")
	}
	if h.Items[0].UUID != "u-2" || h.Items[2].UUID != "a-3" {
		t.Fatalf("丢最旧后头部错位: [0]=%s want u-2", h.Items[0].UUID)
	}
	if h.Title != "turn one" {
		t.Fatalf("title = %q, want 首条 human 文本", h.Title)
	}
	if h.Busy {
		t.Fatalf("尾部为无 pending tool_use 的 assistant，busy 应为 false")
	}
	if h.Cursor <= 0 {
		t.Fatalf("cursor = %d, want >0", h.Cursor)
	}

	resp = doReq(t, http.MethodGet, tp.baseURL+"/api/history?limit=2", "", authed(nil))
	decodeJSON(t, resp, &h)
	if len(h.Items) != 2 || h.Items[0].UUID != "a-2" {
		t.Fatalf("limit=2 items 错位: n=%d [0]=%s", len(h.Items), h.Items[0].UUID)
	}
	resp = doReq(t, http.MethodGet, tp.baseURL+"/api/history?limit=0", "", authed(nil))
	decodeJSON(t, resp, &h)
	if len(h.Items) != 3 {
		t.Fatalf("limit=0 应取默认（HistoryLimit）: n=%d", len(h.Items))
	}
	resp = doReq(t, http.MethodGet, tp.baseURL+"/api/history?limit=abc", "", authed(nil))
	decodeJSON(t, resp, &h)
	if len(h.Items) != 3 {
		t.Fatalf("非法 limit 应取默认: n=%d", len(h.Items))
	}
}

// --- draft 界限 ---------------------------------------------------------------

func TestServerDraft(t *testing.T) {
	tp := newTestPlane(t, nil)

	resp := doReq(t, http.MethodGet, tp.baseURL+"/api/draft", "", authed(nil))
	var d draftResponse
	decodeJSON(t, resp, &d)
	if d.V != 1 || d.Draft != "" {
		t.Fatalf("初始 draft = %q, want 空", d.Draft)
	}

	resp = doReq(t, http.MethodPut, tp.baseURL+"/api/draft", `{"draft":"hello draft"}`, authed(nil))
	var ok okResponse
	decodeJSON(t, resp, &ok)
	if resp.StatusCode != http.StatusOK || !ok.OK || ok.V != 1 {
		t.Fatalf("PUT draft resp=%d body=%+v", resp.StatusCode, ok)
	}
	resp = doReq(t, http.MethodGet, tp.baseURL+"/api/draft", "", authed(nil))
	decodeJSON(t, resp, &d)
	if d.Draft != "hello draft" {
		t.Fatalf("draft 回读 = %q", d.Draft)
	}

	// 128KiB 界限：恰好允许，超一字节拒绝。
	big := strings.Repeat("x", maxTextBytes)
	resp = doReq(t, http.MethodPut, tp.baseURL+"/api/draft", `{"draft":"`+big+`"}`, authed(nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("128KiB draft status = %d, want 200", resp.StatusCode)
	}
	resp = doReq(t, http.MethodPut, tp.baseURL+"/api/draft", `{"draft":"`+big+`x"}`, authed(nil))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("超限 draft status = %d, want 400", resp.StatusCode)
	}

	// 空串清除。
	resp = doReq(t, http.MethodPut, tp.baseURL+"/api/draft", `{"draft":""}`, authed(nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("清空 draft status = %d", resp.StatusCode)
	}
	resp = doReq(t, http.MethodGet, tp.baseURL+"/api/draft", "", authed(nil))
	decodeJSON(t, resp, &d)
	if d.Draft != "" {
		t.Fatalf("清除后 draft = %q, want 空", d.Draft)
	}
}

// --- 静态面（dist 仅 .gitkeep 的降级提示页 / 有产物时的 SPA）-------------------

func TestServerStaticPlane(t *testing.T) {
	tp := newTestPlane(t, nil)
	req, _ := http.NewRequest(http.MethodGet, tp.baseURL+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200（无鉴权）", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("静态 ACAO = %q, want *", got)
	}
	data, _ := io.ReadAll(resp.Body)
	if !distHasIndex() {
		// fresh checkout / 未构建：降级提示页（可观测非失败，R11）。
		if !strings.Contains(string(data), "Web 平面资源未构建") {
			t.Fatalf("占位提示页内容异常: %q", firstN(string(data), 120))
		}
	} else {
		// 已构建（CI artifact / 本地 build:claude-webui 后）：真实 SPA 入口。
		if !strings.Contains(string(data), "<!doctype html") && !strings.Contains(string(data), "<!DOCTYPE html") {
			t.Fatalf("dist 已含产物时 / 应回 index.html: %q", firstN(string(data), 120))
		}
	}
}

// --- Manager 幂等替换 / Stop 幂等 / 参数校验 -----------------------------------

// freePort 预留一个空闲端口（listen 后立即释放；与 webui.AllocateFreePort
// 同法）。并发抢占窗口见 port.go 注释，本测试仅本机串行使用。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// refused 断言端口当前无 listener。
func refused(t *testing.T, port int) bool {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false // 端口仍被占用
	}
	_ = ln.Close()
	return true
}

func TestManagerStartIdempotentReplace(t *testing.T) {
	root := t.TempDir()
	base := PlaneConfig{
		SessionID: "sess-r", ClaudeSessionID: "aaaaaaaa-0000-0000-0000-000000000001",
		WorkDir: `C:\w`, TranscriptRoot: root, PID: 100,
		Token:    testToken,
		Settings: RuntimeSettings{PollInterval: 20 * time.Millisecond},
	}
	m := NewManager(nil) // 测试用 nil 日志：logging 句柄会卡住 Windows 的 t.TempDir 清理

	p1, p2 := freePort(t), freePort(t)
	cfg1, cfg2 := base, base
	cfg1.Port, cfg2.Port = p1, p2

	if port, err := m.Start(cfg1, &fakeInputPort{}); err != nil || port != p1 {
		t.Fatalf("Start#1 = (%d, %v), want (%d, nil)", port, err, p1)
	}
	if port, err := m.Start(cfg2, &fakeInputPort{}); err != nil || port != p2 {
		t.Fatalf("Start#2 = (%d, %v), want (%d, nil)", port, err, p2)
	}
	// 旧 listener 应已释放（幂等替换先 Stop 旧，R9）。
	if !waitFor(t, 2*time.Second, func() bool { return refused(t, p1) }) {
		t.Fatalf("替换后旧端口 %d 仍被占用（listener 泄漏）", p1)
	}
	// 新平面继续服务。
	resp := doReq(t, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/info", p2), "", authed(nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("新平面 info status = %d", resp.StatusCode)
	}

	m.Stop("sess-r")
	m.Stop("sess-r") // 幂等
	if !waitFor(t, 2*time.Second, func() bool { return refused(t, p2) }) {
		t.Fatalf("Stop 后端口 %d 仍被占用", p2)
	}
}

func TestManagerStartValidation(t *testing.T) {
	m := NewManager(nil) // 测试用 nil 日志：logging 句柄会卡住 Windows 的 t.TempDir 清理
	cfg := PlaneConfig{SessionID: "s", Token: "t", TranscriptRoot: t.TempDir()}
	if _, err := m.Start(PlaneConfig{Token: "t"}, &fakeInputPort{}); err == nil {
		t.Fatalf("缺 SessionID 应报错")
	}
	if _, err := m.Start(PlaneConfig{SessionID: "s"}, &fakeInputPort{}); err == nil {
		t.Fatalf("缺 Token 应报错")
	}
	if _, err := m.Start(cfg, nil); err == nil {
		t.Fatalf("缺 InputPort 应报错")
	}
	bad := cfg
	bad.Port = 70000
	if _, err := m.Start(bad, &fakeInputPort{}); err == nil {
		t.Fatalf("非法端口应报错")
	}
	m.Stop("no-such") // 未注册：零操作不 panic
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
