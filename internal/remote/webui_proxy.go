package remote

// webui_proxy.go — pi webui 平面反向代理 + v1 webui 状态端点
// （remote-webui-plane 切片）。
//
// 背景：pi webui server 只 bind 127.0.0.1 且页面基址推导冻结为
// http://127.0.0.1:<port>（amagi-pi webui 契约 §6.1/§6.5 v1.0.15），移动端
// 设备物理不可达。本文件在 codebox remote server 上补齐外露面：
//
//   - /webui/{sessionID}/... 反向代理（server.go buildHandler 顶层分发挂载，
//     先于静态 SPA fallback）：出向改写 Host=127.0.0.1:{port}（pi webui 校验
//     ^127\.0\.0\.1(:\d+)?$，改写后天然通过）、Origin 按值分流（"null"
//     透传、其余删除，见 director 注释）、覆盖式注入 Authorization: Bearer {capability token}；
//     WS upgrade（/ws/events 等）经同一代理，httputil.ReverseProxy 原生透传
//     Upgrade/Connection/Sec-WebSocket-Protocol（后端回显的子协议随响应头
//     透传回客户端）。后端无需任何改动。
//   - GET /api/remote/v1/session/{id}/webui 状态端点（分发见 routes_v1.go
//     dispatchV1WebUIStatusRoute）：available 时下发
//     url=/webui/{sid}/#/t={token}（fragment 承载 capability，不进请求行/
//     日志；token 仅下发给已通过 device cookie 鉴权的设备）。
//
// 安全红线：capability token 视同会话权限 —— 不写日志、不进 query/path、
// 错误体不带；代理是它唯一外露面。代理面鉴权与 v1 完全一致（device
// cookie，经 enforceAuthPolicy 同一判定与错误映射；唯一豁免：CORS 预检
// OPTIONS（Origin:null + 非空 ACRM）按规范不携带凭据，本地应答 204，镜像
// v1 中央派发器 OPTIONS 纪律）；会话写面（POST /api/input、POST
// /api/agent-interact、PUT /api/draft）要求当前控制；宁紧勿松。

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"

	"amagi-codebox/internal/remote/contract"
)

// webuiBackendWriteFaces is the pi webui plane's session WRITE surface —
// the (method, path) pairs gated by the session control permit (G2
// Major-02：控制门写面集合化；无控制权设备一律 403 control.forbidden)：
//   - POST /api/input          —— canonical 输入注入；
//   - POST /api/agent-interact —— resume/background 派生新任务，效果等同输入注入；
//   - PUT  /api/draft          —— 平面草稿覆写（低危写面，同样收敛）。
//
// GET /api/fs/dirs（宿主机目录名枚举，读面、暴露度低）经 diting 裁定维持
// 配对设备可读，不在此集合（契约文档 §3.1 记录取舍）。
var webuiBackendWriteFaces = map[webuiWriteFace]bool{
	{http.MethodPost, "/api/input"}:          true,
	{http.MethodPost, "/api/agent-interact"}: true,
	{http.MethodPut, "/api/draft"}:           true,
}

// webuiWriteFace identifies one backend write (method, path) pair.
type webuiWriteFace struct {
	method string
	path   string
}

// webuiProxyLoopbackHost is the fixed outbound target host (the pi webui
// server's Host validation accepts exactly 127.0.0.1[:port]).
const webuiProxyLoopbackHost = "127.0.0.1"

// webUIProxyPlaneInfo returns the cached plane snapshot when available
// (used by the capability-token auth channel; never advances the state
// machine). Second return is false when the plane is not available.
func (s *Server) webUIProxyPlaneInfo(sessionID string) (SessionWebUIInfo, bool) {
	if s.app == nil {
		return SessionWebUIInfo{}, false
	}
	info, _ := s.app.GetSessionWebUI(sessionID)
	if info.State != string(contract.WebUIPlaneStateAvailable) || info.Port <= 0 {
		return SessionWebUIInfo{}, false
	}
	return info, true
}

// webUIProxyTokenAuthed reports whether the request itself carries the
// session capability (constant-time compare, mirroring the backend's timing
// discipline): HTTP API requests via `Authorization: Bearer <token>` (the
// plane page's fetchJson always sends it), WS upgrade requests via the
// `Sec-WebSocket-Protocol: webui, <token>` subprotocol pair (connectWs always
// sends it). An empty tracker token never authenticates (fail-closed).
func webUIProxyTokenAuthed(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(token)) == 1
	}
	proto := r.Header.Get("Sec-WebSocket-Protocol")
	parts := strings.Split(proto, ",")
	if len(parts) == 2 {
		first, second := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if first == "webui" {
			return subtle.ConstantTimeCompare([]byte(second), []byte(token)) == 1
		}
	}
	return false
}

// handleWebUIProxy serves /webui/{sessionID}/<backend-path>. Order:
// security surface → sid whitelist (404, no oracle) → CORS preflight exemption
// (204, no auth) → static-plane exemption (no cookie auth; see below) →
// /api/* no-store → device-cookie auth on data faces (v1-mapped 401/503) →
// write-face control gate (403) → plane state mapping (probing → 503; other
// non-available → 404) → reverse proxy.
func (s *Server) handleWebUIProxy(w http.ResponseWriter, r *http.Request) {
	// Baseline hardening headers (merged with the backend response headers).
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")

	// Fail closed without the v1 security surface (legacy NewServer): the
	// proxy has no auth path there, so it must not serve anything.
	if s.v1sec == nil {
		http.NotFound(w, r)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, contract.WebUIProxyPathPrefix)
	sid := rest
	target := "/"
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		sid, target = rest[:i], rest[i:]
	}
	if !validWebUIProxySessionID(sid) {
		http.NotFound(w, r)
		return
	}

	// CORS preflight exemption (G2 Critical-01-a)：sandbox opaque iframe
	//（Origin: null）的预检 OPTIONS 不携带凭据（CORS 规范），必须在鉴权前本地
	// 应答——否则预检 401，浏览器的实际数据请求（带 Authorization 触发预检）
	// 根本发不出去。镜像 v1 中央派发器「OPTIONS 不做 Cookie 鉴权」纪律
	//（routes_v1.go）。预检不转发后端：本面契约固定头面（ACAO:null +
	// ACACredentials + Vary），不依赖 amagi-pi 后端 CORS 细节（后端语义等价，
	// 但由代理统一应答更稳——两仓升级节奏解耦）。
	if isWebUIProxyPreflight(r) {
		w.Header().Set("Access-Control-Allow-Origin", "null")
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// G2 Minor-04：/api/* 平面数据响应统一 no-store（与 v1 缓存纪律一致；
	// 旱设于鉴权之前，故这些路径上的错误响应同样携带）。静态资源路径保留
	// 后端自身缓存语义。后端 /api 响应不设 Cache-Control，无重复头风险。
	if strings.HasPrefix(target, "/api/") {
		h.Set("Cache-Control", "no-store")
	}

	reqID, idOK := resolveRequestID(r)
	if !idOK {
		// G2 Info-06：对齐 v1 fail-closed —— crypto/rand 失败时 503，不再
		// fallbackRequestID 继续服务（routes_v1.go 同款语义）。
		w.Header().Set(contract.RequestIDHeader, string(fallbackRequestID))
		writeV1Error(w, fallbackRequestID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "security state unavailable", contract.ActionHintCheckDesktop)
		return
	}
	h.Set(contract.RequestIDHeader, string(reqID))

	// Data-face auth — DUAL CHANNEL (browser E2E finding, G2 follow-up):
	//   1. Device cookie (the SAME credential and error mapping as v1) is the
	//      primary channel and the ONLY channel that yields a device principal
	//      (write faces require a control-holding device identity).
	//   2. Capability-token fallback: Chrome suppresses SameSite cookies on
	//      requests from sandboxed (opaque-origin) documents, so the plane
	//      iframe can NEVER carry the device cookie — yet its fetchJson always
	//      sends `Authorization: Bearer <capability>` and connectWs always sends
	//      the `webui, <capability>` subprotocol. Holding the session capability
	//      is equivalent to session-read permission (amagi-pi contract §6.3),
	//      so it authenticates READ faces; the principal stays zero-valued and
	//      the write-face control gate below rejects (403) — writes always need
	//      a control-holding DEVICE.
	// The STATIC plane (HTML entry, ./assets/*, favicon — anything outside
	// /api/* and /ws/*) stays exempt: cross-origin CORS-mode subresource loads
	// (module scripts are ALWAYS CORS mode) never carry cookies, and the static
	// plane carries no session data (backend §6.3: static assets are public).
	var principal v1Principal
	if strings.HasPrefix(target, "/api/") || strings.HasPrefix(target, "/ws/") {
		hasDeviceCookie := false
		for _, c := range r.Cookies() {
			if c.Name == deviceCookieName {
				hasDeviceCookie = true
				break
			}
		}
		if hasDeviceCookie {
			var authOK bool
			principal, authOK = s.enforceAuthPolicy(w, r, deviceCookie, reqID)
			if !authOK {
				return
			}
		} else if info, ok := s.webUIProxyPlaneInfo(sid); ok && webUIProxyTokenAuthed(r, info.Token) {
			// capability-token channel (read faces only; zero principal ⇒ write gate 403)
		} else {
			var authOK bool
			principal, authOK = s.enforceAuthPolicy(w, r, deviceCookie, reqID) // canonical 401/503 mapping
			if !authOK {
				return
			}
		}
	}

	// Write-face control gate (frozen C1 + G2 Major-02 集合化)：平面写面
	//（POST /api/input、POST /api/agent-interact、PUT /api/draft）要求当前
	// 控制（快照判定，grace 持有者视为控制者）；无控制权 → 403。
	if webuiBackendWriteFaces[webuiWriteFace{method: r.Method, path: target}] && !s.webUIProxyControlHeld(principal.DeviceID, sid) {
		writeV1Error(w, reqID, http.StatusForbidden, contract.ErrorCodeControlForbidden,
			contract.ErrorLayerControl, "control required for session write", contract.ActionHintRequestControl)
		return
	}

	// Plane state (cached snapshot; the status endpoint advances the host
	// state machine via ProbeSessionWebUI).
	if s.app == nil {
		http.NotFound(w, r)
		return
	}
	info, _ := s.app.GetSessionWebUI(sid)
	if info.State != string(contract.WebUIPlaneStateAvailable) || info.Port <= 0 {
		s.writeWebUIPlaneUnavailable(w, reqID, sid, info.State)
		return
	}

	s.serveWebUIReverseProxy(w, r, reqID, sid, info.Port, info.Token, target)
}

// writeWebUIPlaneUnavailable maps non-available plane states to fixed proxy
// responses: probing → 503 service.down (retryable — the plane may still come
// up); unavailable/ended/unknown → 404 session.not_found (the plane for this
// session does not exist / is gone). Bodies never carry the token.
func (s *Server) writeWebUIPlaneUnavailable(w http.ResponseWriter, reqID contract.RequestID, sessionID, state string) {
	if state == string(contract.WebUIPlaneStateProbing) {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "webui plane not ready", contract.ActionHintRetry)
		return
	}
	writeV1Error(w, reqID, http.StatusNotFound, contract.ErrorCodeSessionNotFound,
		contract.ErrorLayerSession, "webui plane not available", contract.ActionHintRetry)
}

// validWebUIProxySessionID enforces the strict sid whitelist ^[A-Za-z0-9_-]+$
// (≤ MaxV1SessionIDBytes). Covers every producer format in the repo (remote
// sessions: 22-char base64url; desktop sessions: 8-char hex) and rejects
// path-traversal spellings (dots, slashes, percent-decoded separators).
func validWebUIProxySessionID(sid string) bool {
	if sid == "" || len(sid) > MaxV1SessionIDBytes {
		return false
	}
	for i := 0; i < len(sid); i++ {
		c := sid[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// isWebUIProxyPreflight reports the exact CORS preflight shape that is
// exempt from device-cookie auth (G2 Critical-01-a): an OPTIONS request from
// a sandbox opaque origin (Origin: null) carrying a non-empty
// Access-Control-Request-Method. Any other OPTIONS (no ACRM, or a non-null
// Origin) stays on the regular authenticated path — the exemption cannot be
// abused to reach the backend.
func isWebUIProxyPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions &&
		r.Header.Get("Origin") == "null" &&
		r.Header.Get("Access-Control-Request-Method") != ""
}

// webUIProxyControlHeld reports whether deviceID currently holds control of
// the session, per the control gate's SNAPSHOT projection (SnapshotForDevice):
// unlike the /ws/v1 input path's exact live-lease + lane judgment
// （DoDevicePTY），this is a snapshot view in which a grace-phase holder still
// projects as controller — a deliberate, documented relaxation. Fail closed:
// an unwired control runtime or any gate error denies.
func (s *Server) webUIProxyControlHeld(deviceID contract.DeviceID, sessionID string) bool {
	if s.sessionAdapter == nil {
		return false
	}
	snap, err := s.sessionAdapter.Gate().SnapshotForDevice(contract.SessionID(sessionID), deviceID)
	if err != nil {
		return false
	}
	return snap.State == contract.ControlStateYou
}

// serveWebUIReverseProxy forwards one request to the session's loopback pi
// webui server. The proxy is built per request so the freshest port/token
// snapshot is honored (a /resume session switch may move the plane).
func (s *Server) serveWebUIReverseProxy(w http.ResponseWriter, r *http.Request, reqID contract.RequestID, sessionID string, port int, token, targetPath string) {
	hostPort := net.JoinHostPort(webuiProxyLoopbackHost, strconv.Itoa(port))
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = hostPort
			req.URL.Path = targetPath
			req.URL.RawPath = ""
			// Outbound Host rewrite: the pi webui server validates
			// Host ^127\.0\.0\.1(:\d+)?$.
			req.Host = hostPort
			// Origin 按值分流（G1 集成验证实证）：
			//   - "null"（sandbox opaque origin iframe）→ 透传：后端白名单接受
			//     null，且预检(OPTIONS)响应据此回 Access-Control-Allow-Origin:
			//     null——删头会让后端当命令行请求不回 CORS 头，opaque 页面的
			//     预检失败，实际数据请求（带 Authorization 触发预检）发不出去。
			//   - 其他 Origin（移动端顶层同源 fetch）→ 删除：同源 fetch 不需要
			//     CORS 头，后端白名单语义“头缺失即合法”天然通过。
			if req.Header.Get("Origin") != "null" {
				req.Header.Del("Origin")
			}
			if token != "" {
				// 覆盖式注入：任何客户端自带的 Authorization 被覆盖。
				req.Header.Set("Authorization", "Bearer "+token)
			} else {
				// Token unknown (legacy backend without capability auth):
				// strip the client value rather than forwarding it (宁紧勿松).
				req.Header.Del("Authorization")
			}
		},
		// Immediate flush: the plane streams (WS is hijacked separately, but
		// any SSE/streaming API must not be buffered).
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// err may carry loopback details: fixed mapping, never echoed,
			// never logged with content (sid only — no token).
			if s.log != nil {
				s.log.Debug("remote", "webui 代理后端不可达", "session="+sessionID)
			}
			writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
				contract.ErrorLayerConnection, "webui plane unreachable", contract.ActionHintRetry)
		},
	}
	proxy.ServeHTTP(w, r)
}

// ---------------------------------------------------------------------------
// v1 extension route: GET {RESTBasePath}/session/{id}/webui
// ---------------------------------------------------------------------------

// dispatchV1WebUIStatusRoute resolves the additive v1 extension route
// GET {RESTBasePath}/session/{id}/webui (remote-webui-plane slice). The route
// is deliberately OUTSIDE the frozen V1RestEndpoints manifest (fixture
// parity); the gate order mirrors the central dispatcher exactly:
// Host → query → method (405 for a known path) → origin → auth → handler.
// Returns false when the path matches no extension route (caller continues
// with its own 404 classification).
func (s *Server) dispatchV1WebUIStatusRoute(w http.ResponseWriter, r *http.Request, reqID contract.RequestID, corsAllowed bool, port int) bool {
	if s.app == nil {
		return false // webui plane requires the App wiring; stays 404
	}
	sid, ok := matchV1Path(contract.RESTBasePath+contract.WebUIStatusEndpoint.Path, r.URL.Path)
	if !ok {
		return false
	}
	if r.Method == http.MethodOptions {
		if !strictHostValid(r, port) {
			writeV1Error(w, reqID, http.StatusForbidden, contract.ErrorCodeBadRequest,
				contract.ErrorLayerAuth, "request host rejected", contract.ActionHintCheckDesktop)
			return true
		}
		if r.URL.RawQuery != "" {
			writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
				contract.ErrorLayerConnection, "query parameters are not allowed", contract.ActionHintRetry)
			return true
		}
		if !corsAllowed {
			writeV1Error(w, reqID, http.StatusForbidden, contract.ErrorCodeBadRequest,
				contract.ErrorLayerAuth, "request origin rejected", contract.ActionHintCheckDesktop)
			return true
		}
		if r.Header.Get("Access-Control-Request-Method") != contract.WebUIStatusEndpoint.Method {
			writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
				contract.ErrorLayerConnection, "request method rejected", contract.ActionHintRetry)
			return true
		}
		w.Header().Set("Access-Control-Allow-Methods", contract.WebUIStatusEndpoint.Method+", "+http.MethodOptions)
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+contract.RequestIDHeader)
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	if !strictHostValid(r, port) {
		writeV1Error(w, reqID, http.StatusForbidden, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "request host rejected", contract.ActionHintCheckDesktop)
		return true
	}
	if r.URL.RawQuery != "" {
		writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerConnection, "query parameters are not allowed", contract.ActionHintRetry)
		return true
	}
	if r.Method != contract.WebUIStatusEndpoint.Method {
		w.Header().Set("Allow", contract.WebUIStatusEndpoint.Method+", "+http.MethodOptions)
		writeV1Error(w, reqID, http.StatusMethodNotAllowed, contract.ErrorCodeBadRequest,
			contract.ErrorLayerConnection, "request method rejected", contract.ActionHintRetry)
		return true
	}
	if !enforceOriginPolicy(r, safeBrowserProof, corsAllowed) {
		writeV1Error(w, reqID, http.StatusForbidden, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "request origin rejected", contract.ActionHintCheckDesktop)
		return true
	}
	principal, authOK := s.enforceAuthPolicy(w, r, deviceCookie, reqID)
	if !authOK {
		return true
	}
	s.handleV1SessionWebUIStatus(w, r, reqID, principal, sid)
	return true
}

// handleV1SessionWebUIStatus implements the extension endpoint body: run ONE
// probe round (advancing the host webui state machine, the same path the
// desktop frontend polls) and project the frozen {state, url?} shape. The
// token reaches the client ONLY inside the URL fragment.
func (s *Server) handleV1SessionWebUIStatus(w http.ResponseWriter, r *http.Request, reqID contract.RequestID, principal v1Principal, sessionID contract.SessionID) {
	info, _ := s.app.ProbeSessionWebUI(string(sessionID))
	state := contract.WebUIPlaneState(info.State)
	if !contract.IsKnownWebUIPlaneState(state) {
		state = contract.WebUIPlaneStateUnknown // fail-safe downgrade
	}
	resp := contract.WebUIStatus{State: state}
	if state == contract.WebUIPlaneStateAvailable && info.Port > 0 {
		resp.URL = contract.WebUIProxyPathPrefix + string(sessionID) + "/"
		if info.Token != "" {
			// fragment 承载 capability：不进请求行/日志/错误体。
			resp.URL += "#/t=" + info.Token
		}
	}
	body, merr := contract.MarshalRESTResponse(resp)
	if merr != nil {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "service unavailable", contract.ActionHintCheckDesktop)
		return
	}
	// Side effect AFTER the full body is prepared; outcome ignored for the
	// response (same discipline as handleV1HostSummary).
	_, _ = s.v1sec.pairing.RecordDeviceSeen(principal)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(contract.WebUIStatusEndpoint.SuccessStatus)
	_, _ = w.Write(body)
}
