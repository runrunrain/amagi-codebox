// fakev1_test.go — v1 契约假宿主（httptest）：配对/摘要/会话域最小真实现。
//
// 纪律：行为镜像服务端 routes_v1.go / session_routes_v1.go 的可观察契约
// （状态码、错误体、Set-Cookie、confirm 校验、Content-Type 要求），但不
// import internal/remote 任何符号（依赖方向硬约束）。错误体一律
// contract.MarshalAPIError + X-Request-ID 回显，镜像 writeV1Error。
package remoteclient

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// fakeDevice 是假宿主签发的一台已配对设备。
type fakeDevice struct {
	id     string // 22 字符 wire 形态
	secret string // 43 字符 wire 形态
}

// capturedRequest 记录假宿主观察到的请求头（供注入断言）。
type capturedRequest struct {
	Method      string
	Path        string
	RequestID   string
	Origin      string
	Cookie      string
	ContentType string
	HasBody     bool
}

// fakeHost 是 v1 REST 假宿主。零值不可用，经 newFakeHost 构建。
type fakeHost struct {
	t    *testing.T
	srv  *httptest.Server
	mu   sync.Mutex
	dev  *fakeDevice     // 最近一次配对签发的设备
	rev  map[string]bool // 已撤销 deviceID
	sess map[string]*contract.SessionDetail
	seq  int
	// 配对行为注入
	pairingCode    string // 合法码
	windowActive   bool   // 窗口开关（false → 410 auth.window_expired）
	revOnIssue     bool   // 签发即撤销（配对验证阶段浮出 auth.revoked）
	noCookie       bool   // 成功但不发 Set-Cookie（契约异常注入）
	cookieOverride string // 覆盖 Cookie 值（格式异常注入）
	bodyDeviceID   string // 覆盖响应体 device.id（不一致注入）
	captured       []capturedRequest
	hang           bool // 挂起处理（超时测试）
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	f := &fakeHost{
		t:            t,
		rev:          map[string]bool{},
		sess:         map[string]*contract.SessionDetail{},
		pairingCode:  "PAIR-CODE-1",
		windowActive: true,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

// baseURL 返回假宿主 origin（Transport 的 BaseURL / 期望 Origin 头同值）。
func (f *fakeHost) baseURL() string { return f.srv.URL }

// hostPort 返回 host:port 形态（登记簿/配对输入用）。
func (f *fakeHost) hostPort() string {
	return strings.TrimPrefix(f.srv.URL, "http://")
}

func (f *fakeHost) capture(r *http.Request, hasBody bool) {
	f.mu.Lock()
	f.captured = append(f.captured, capturedRequest{
		Method:      r.Method,
		Path:        r.URL.Path,
		RequestID:   r.Header.Get(contract.RequestIDHeader),
		Origin:      r.Header.Get("Origin"),
		Cookie:      r.Header.Get("Cookie"),
		ContentType: r.Header.Get("Content-Type"),
		HasBody:     hasBody,
	})
	f.mu.Unlock()
}

func (f *fakeHost) requests() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]capturedRequest, len(f.captured))
	copy(out, f.captured)
	return out
}

// revokeDevice 把指定设备置为已撤销。
func (f *fakeHost) revokeDevice(deviceID string) {
	f.mu.Lock()
	f.rev[deviceID] = true
	f.mu.Unlock()
}

// writeErr 镜像服务端 writeV1Error：契约错误体 + requestId 回显。
func (f *fakeHost) writeErr(w http.ResponseWriter, reqID string, status int, code contract.ErrorCode, layer contract.ErrorLayer, msg string, hint contract.ActionHint) {
	body, err := contract.MarshalAPIError(contract.APIError{
		RequestID: contract.RequestID(reqID), Code: code, Layer: layer, Message: msg, ActionHint: hint,
	})
	if err != nil {
		f.t.Errorf("fakeHost MarshalAPIError: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeOK 镜像服务端成功路径：契约校验 + MarshalRESTResponse。
func (f *fakeHost) writeOK(w http.ResponseWriter, status int, body contract.RESTResponse) {
	raw, err := contract.MarshalRESTResponse(body)
	if err != nil {
		f.t.Errorf("fakeHost MarshalRESTResponse(%T): %v", body, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// fakeSummary 构造五 CLI 全量 HostSummary。
func fakeSummary() contract.HostSummary {
	hs := contract.HostSummary{
		APIVersion:    contract.APIVersionV1,
		ServerVersion: "fake-1.0.0",
	}
	for _, cli := range contract.KnownCLITypes {
		hs.CLIAvailability = append(hs.CLIAvailability, contract.CLIAvailability{CLIType: cli, Available: true})
	}
	return hs
}

// deviceWireID 生成 22 字符 wire 形态 DeviceID。
func deviceWireID(seed byte) string {
	buf := make([]byte, 16)
	for i := range buf {
		buf[i] = seed + byte(i)
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, 22)
	for i := range out {
		out[i] = alphabet[(int(buf[i%len(buf)])+i*7)%len(alphabet)]
	}
	return string(out)
}

// deviceWireSecret 生成 43 字符 wire 形态 secret。
func deviceWireSecret(seed byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, 43)
	for i := range out {
		out[i] = alphabet[(int(seed)+i*13)%len(alphabet)]
	}
	return string(out)
}

// handle 是假宿主路由：/api/remote/v1 前缀 + 静态 /healthz（探活反例用）。
func (f *fakeHost) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	hang := f.hang
	f.mu.Unlock()
	if hang {
		time.Sleep(500 * time.Millisecond)
	}
	reqID := r.Header.Get(contract.RequestIDHeader)
	if reqID == "" {
		reqID = "req_fake_unknown"
	}
	w.Header().Set(contract.RequestIDHeader, reqID)
	f.capture(r, r.Body != nil && r.ContentLength != 0)

	if !strings.HasPrefix(r.URL.Path, contract.RESTBasePath+"/") {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.URL.Path == contract.RESTBasePath+"/pairing/complete" && r.Method == http.MethodPost:
		f.handlePairing(w, r, reqID)
	case r.URL.Path == contract.RESTBasePath+"/host/summary" && r.Method == http.MethodGet:
		f.handleSummary(w, r, reqID)
	case r.URL.Path == contract.RESTBasePath+"/sessions" && r.Method == http.MethodGet:
		f.handleList(w, r, reqID)
	case r.URL.Path == contract.RESTBasePath+"/sessions" && r.Method == http.MethodPost:
		f.handleCreate(w, r, reqID)
	case r.URL.Path == contract.RESTBasePath+"/sessions" && (r.Method == http.MethodGet || r.Method == http.MethodDelete):
		f.handleSessionByID(w, r, reqID)
	default:
		f.handleSessionByID(w, r, reqID)
	}
}

// authenticate 镜像服务端设备 Cookie 鉴权：缺失/未知 → auth.unpaired；
// 已撤销 → auth.revoked；成功返回 deviceID。
func (f *fakeHost) authenticate(w http.ResponseWriter, r *http.Request, reqID string) (string, bool) {
	c, err := r.Cookie(deviceCookieName)
	if err != nil || c.Value == "" {
		f.writeErr(w, reqID, http.StatusUnauthorized, contract.ErrorCodeAuthUnpaired,
			contract.ErrorLayerAuth, "device not paired", contract.ActionHintRePair)
		return "", false
	}
	id, _, perr := parseDeviceCookieValue(c.Value)
	if perr != nil {
		f.writeErr(w, reqID, http.StatusUnauthorized, contract.ErrorCodeAuthUnpaired,
			contract.ErrorLayerAuth, "device not paired", contract.ActionHintRePair)
		return "", false
	}
	f.mu.Lock()
	revoked := f.rev[id]
	f.mu.Unlock()
	if revoked {
		f.writeErr(w, reqID, http.StatusUnauthorized, contract.ErrorCodeAuthRevoked,
			contract.ErrorLayerAuth, "device revoked", contract.ActionHintRePair)
		return "", false
	}
	return id, true
}

func (f *fakeHost) handleSummary(w http.ResponseWriter, r *http.Request, reqID string) {
	if _, ok := f.authenticate(w, r, reqID); !ok {
		return
	}
	f.writeOK(w, http.StatusOK, fakeSummary())
}

func (f *fakeHost) handlePairing(w http.ResponseWriter, r *http.Request, reqID string) {
	if ct := strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]); ct != "application/json" {
		f.writeErr(w, reqID, http.StatusUnsupportedMediaType, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "invalid pairing request", contract.ActionHintRetry)
		return
	}
	raw, rerr := io.ReadAll(r.Body)
	if rerr != nil {
		f.t.Errorf("fakeHost read pairing body: %v", rerr)
	}
	req, derr := contract.DecodePairingCompleteRequest(raw)
	if derr != nil {
		f.writeErr(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "invalid pairing request", contract.ActionHintRetry)
		return
	}
	f.mu.Lock()
	active, code := f.windowActive, f.pairingCode
	f.mu.Unlock()
	if !active {
		f.writeErr(w, reqID, http.StatusGone, contract.ErrorCodeAuthWindowExpired,
			contract.ErrorLayerAuth, "pairing window is not active", contract.ActionHintCheckDesktop)
		return
	}
	if req.Code != code {
		f.writeErr(w, reqID, http.StatusUnauthorized, contract.ErrorCodeAuthUnpaired,
			contract.ErrorLayerAuth, "pairing code rejected", contract.ActionHintRePair)
		return
	}
	f.mu.Lock()
	dev := &fakeDevice{id: deviceWireID(7), secret: deviceWireSecret(11)}
	f.dev = dev
	if f.revOnIssue {
		f.rev[dev.id] = true
	}
	bodyID := f.bodyDeviceID
	noCookie, override := f.noCookie, f.cookieOverride
	f.mu.Unlock()
	if bodyID == "" {
		bodyID = dev.id
	}
	resp := contract.PairingCompleteResponse{
		Device: contract.DeviceSummary{ID: contract.DeviceID(bodyID), Name: req.DeviceName, PairedAt: "2026-08-02T01:02:03Z"},
		Host:   fakeSummary(),
	}
	if !noCookie {
		value := buildDeviceCookieValue(dev.id, dev.secret)
		if override != "" {
			value = override
		}
		http.SetCookie(w, &http.Cookie{Name: deviceCookieName, Value: value, Path: "/", HttpOnly: true})
	}
	f.writeOK(w, http.StatusCreated, resp)
}

func (f *fakeHost) handleList(w http.ResponseWriter, r *http.Request, reqID string) {
	if _, ok := f.authenticate(w, r, reqID); !ok {
		return
	}
	f.mu.Lock()
	list := make(contract.SessionList, 0, len(f.sess)) // 非nil空列表
	for _, d := range f.sess {
		list = append(list, d.SessionSummary)
	}
	f.mu.Unlock()
	f.writeOK(w, http.StatusOK, list)
}

func (f *fakeHost) handleCreate(w http.ResponseWriter, r *http.Request, reqID string) {
	if _, ok := f.authenticate(w, r, reqID); !ok {
		return
	}
	raw, rerr := io.ReadAll(r.Body)
	if rerr != nil {
		f.t.Errorf("fakeHost read create body: %v", rerr)
	}
	req, derr := contract.DecodeCreateSessionRequest(raw)
	if derr != nil {
		f.writeErr(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
		return
	}
	f.mu.Lock()
	f.seq++
	id := fmt.Sprintf("sess-%03d", f.seq)
	now := "2026-08-02T01:02:03Z"
	wd := "/tmp/work"
	detail := &contract.SessionDetail{
		SessionSummary: contract.SessionSummary{
			ID: contract.SessionID(id), Title: "fake " + string(req.CLIType), CLIType: req.CLIType,
			State: contract.SessionStateRunning, Control: contract.ControlSnapshot{State: contract.ControlStateNone},
			LastActivityAt: now,
		},
		Workdir: wd, StartedAt: now,
	}
	f.sess[id] = detail
	f.mu.Unlock()
	f.writeOK(w, http.StatusCreated, *detail)
}

// handleSessionByID 处理 /sessions/{id} 的 GET/POST(lifecycle)/DELETE。
func (f *fakeHost) handleSessionByID(w http.ResponseWriter, r *http.Request, reqID string) {
	if _, ok := f.authenticate(w, r, reqID); !ok {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, contract.RESTBasePath+"/sessions/")
	id, action := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		id, action = rest[:i], rest[i+1:]
	}
	f.mu.Lock()
	d, found := f.sess[id]
	f.mu.Unlock()
	if !found {
		f.writeErr(w, reqID, http.StatusNotFound, contract.ErrorCodeSessionNotFound,
			contract.ErrorLayerSession, "session not found", contract.ActionHintRetry)
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		f.writeOK(w, http.StatusOK, *d)
	case action == "stop" && r.Method == http.MethodPost,
		action == "restart" && r.Method == http.MethodPost,
		action == "" && r.Method == http.MethodDelete:
		if !f.consumeConfirm(r) {
			f.writeErr(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
				contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
			return
		}
		f.mu.Lock()
		switch {
		case action == "stop":
			d.State = contract.SessionStateStopped
		case action == "restart":
			d.State = contract.SessionStateRunning
		default:
			delete(f.sess, id)
		}
		snapshot := *d
		f.mu.Unlock()
		if action == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		f.writeOK(w, http.StatusOK, snapshot)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE, OPTIONS")
		f.writeErr(w, reqID, http.StatusMethodNotAllowed, contract.ErrorCodeBadRequest,
			contract.ErrorLayerConnection, "request method rejected", contract.ActionHintRetry)
	}
}

// consumeConfirm 镜像 stop/restart/delete 的 confirm:true 协议校验。
func (f *fakeHost) consumeConfirm(r *http.Request) bool {
	raw, rerr := io.ReadAll(r.Body)
	if rerr != nil {
		f.t.Errorf("fakeHost read confirm body: %v", rerr)
	}
	_, err := contract.DecodeConfirmActionRequest(raw)
	return err == nil
}
