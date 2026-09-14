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
//     url=/webui/{sid}/#/t={plane-token}（fragment 承载代理铸造的
//     per-(session, device) capability，不进请求行/日志；token 仅下发给
//     已通过 device cookie 鉴权的设备，且绑定该设备——写面控制门据此
//     评估真实控制权归属，G3 修复输入台「连接中断，消息未发送」）。
//
// 安全红线：capability token（plane token 与后端 token 同权对待）视同会话
// 权限 —— 不写日志、不进 query/path、错误体不带；代理是它唯一外露面。
// 代理面鉴权与 v1 完全一致（device cookie，经 enforceAuthPolicy 同一判定
// 与错误映射；唯一豁免：CORS 预检 OPTIONS（Origin:null + 非空 ACRM）
// 按规范不携带凭据，本地应答 204，镜像 v1 中央派发器 OPTIONS 纪律）；
// 会话写面（POST /api/input、POST /api/agent-interact、PUT /api/draft）
// 要求当前控制（plane token 解析出的绑定设备即请求者身份）；宁紧勿松。
// 本地生成的错误响应（401/403/503）在 Origin:null 时携带 ACAO:null，
// 确保 opaque iframe 能读到真实错误码而非被浏览器掩盖成 network_error。

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"

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

// webuiPlaneGrantStore mints per-DEVICE plane capability tokens (G3 修复：
// Web 平面输入台 403）。sandbox iframe 永远带不上 device cookie（Chrome 对
// opaque origin 抑制 SameSite cookie），若 fragment 只发 raw 后端 token，
// 数据面就是零 principal——写面控制门恒 403，输入台永远发不出去。
// 状态端点（device cookie 鉴权后）改为下发代理铸造的 per-(session,
// device) plane token：解析命中即得到绑定的 device principal，写面控制门
// 因此能评估真实的控制权归属（持控制 → 放行；未持/他人持有 → 403，语义
// 对齐 TUI 平面「先获取控制权」）。出向（Authorization 与 WS 子协议）
// 仍统一换写为后端 token，后端零改动。
type webuiPlaneGrantStore struct {
	mu      sync.Mutex
	byToken map[string]webuiPlaneGrant
	byOwner map[string]string // sessionID + "\x00" + deviceID -> plane token
}

// webuiPlaneGrant is the binding carried by one minted plane token.
type webuiPlaneGrant struct {
	sessionID    string
	deviceID     string
	backendToken string // snapshot: rotation auto-invalidates at resolve time
}

// webuiPlaneTokenPrefix distinguishes proxy-minted plane tokens from the raw
// backend capability token: "p"+30 hex = 31 chars. It matches the amagi-pi
// page fragment pattern ^[A-Za-z0-9_-]{22,}$ yet can never equal a backend
// 32-hex token (fail-closed if a plane token ever reaches the backend
// directly — the backend rejects it).
const webuiPlaneTokenPrefix = "p"

// plane returns the stable per-(session, device) token, minting on first use
// and re-minting only when the backend token rotated (the /resume plane move).
func (g *webuiPlaneGrantStore) plane(sessionID, deviceID, backendToken string) (string, error) {
	owner := sessionID + "\x00" + deviceID
	g.mu.Lock()
	defer g.mu.Unlock()
	if tok, ok := g.byOwner[owner]; ok {
		if gr, ok2 := g.byToken[tok]; ok2 && gr.backendToken == backendToken {
			return tok, nil
		}
		// rotated backend token: drop the stale grant from both maps.
		delete(g.byToken, tok)
		delete(g.byOwner, owner)
	}
	buf := make([]byte, 15) // 120bit; token total 31 chars
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := webuiPlaneTokenPrefix + hex.EncodeToString(buf)
	if g.byToken == nil {
		g.byToken = map[string]webuiPlaneGrant{}
		g.byOwner = map[string]string{}
	}
	g.byToken[tok] = webuiPlaneGrant{sessionID: sessionID, deviceID: deviceID, backendToken: backendToken}
	g.byOwner[owner] = tok
	return tok, nil
}

// resolve maps a plane token back to its bound device when it matches the
// session AND the current backend token snapshot (rotation auto-invalidates).
func (g *webuiPlaneGrantStore) resolve(token, sessionID, backendToken string) (string, bool) {
	if token == "" {
		return "", false
	}
	g.mu.Lock()
	gr, ok := g.byToken[token]
	g.mu.Unlock()
	if !ok || gr.sessionID != sessionID || gr.backendToken != backendToken {
		return "", false
	}
	return gr.deviceID, true
}

// pruneSession drops every grant bound to a session (plane gone for good).
func (g *webuiPlaneGrantStore) pruneSession(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for tok, gr := range g.byToken {
		if gr.sessionID == sessionID {
			delete(g.byToken, tok)
			delete(g.byOwner, gr.sessionID+"\x00"+gr.deviceID)
		}
	}
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

// webUIProxyOfferedToken extracts the capability the request itself carries:
// the Bearer header (the plane page's fetchJson always sends one), or the WS
// subprotocol second element (connectWs always offers the `webui, <token>`
// pair). Empty when the request carries neither channel.
func webUIProxyOfferedToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	parts := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")
	if len(parts) == 2 && strings.TrimSpace(parts[0]) == "webui" {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// webUIProxyGrantDeviceValid re-validates the grant-bound device against the
// live store (revoked/expired devices must not keep their plane identity).
// Store/gate-down fails closed: the grant channel degrades to 401 (the raw
// backend token read channel is unaffected).
func (s *Server) webUIProxyGrantDeviceValid(deviceID string) bool {
	auth := s.v1sec.deviceAuth
	if auth == nil || !auth.store.Ready() {
		return false
	}
	permit, ok := auth.gate.issueNormalPermit()
	if !ok {
		return false
	}
	rec, found, lerr := auth.store.Lookup(permit, contract.DeviceID(deviceID))
	auth.gate.returnNormalPermit(permit)
	if lerr != nil || !found || rec.RevokedAt != nil {
		return false
	}
	return auth.clock.Now().UTC().Before(rec.CredentialExpiresAt)
}

// allowOpaqueOriginRead lets the sandboxed (opaque-origin) plane iframe READ
// locally-generated proxy errors. Without ACAO:null the browser hides the
// response entirely — the webui's fetch then reports a generic
// network_error (「连接中断，消息未发送」), masking the real 401/403/503.
// Only ever set on LOCAL error paths: success responses get their CORS
// headers from the backend (setting them here would duplicate ACAO and
// break the response read for the opaque origin).
func allowOpaqueOriginRead(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") == "null" {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "null")
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Add("Vary", "Origin")
	}
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

	// Data-face auth — DUAL CHANNEL + per-device plane tokens (G3):
	//   1. Device cookie (the SAME credential and error mapping as v1) is the
	//      primary channel and yields the full device principal.
	//   2. Capability-token fallback: Chrome suppresses SameSite cookies on
	//      requests from sandboxed (opaque-origin) documents, so the plane
	//      iframe can NEVER carry the device cookie — yet its fetchJson always
	//      sends `Authorization: Bearer <capability>` and connectWs always
	//      offers the `webui, <capability>` subprotocol. The status endpoint
	//      (cookie-authenticated) hands out a proxy-minted per-(session,
	//      device) plane token; resolving it yields the BOUND device
	//      principal, so the write-face control gate below evaluates real
	//      control ownership (G3 fix: previously every plane write was a
	//      zero-principal 403 — the input stand could never send).
	//      The RAW backend capability still authenticates READ faces with a
	//      zero-valued principal (compat for planes opened before this
	//      rotation; writes stay 403 there — writes always need a
	//      control-holding DEVICE).
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
		} else if offered := webUIProxyOfferedToken(r); offered != "" {
			resolved := false
			if info, ok := s.webUIProxyPlaneInfo(sid); ok {
				if deviceID, gOK := s.webuiGrants.resolve(offered, sid, info.Token); gOK && s.webUIProxyGrantDeviceValid(deviceID) {
					// per-device plane token → bound device principal
					// (write faces gated by REAL control ownership).
					principal = v1Principal{DeviceID: contract.DeviceID(deviceID)}
					resolved = true
				} else if webUIProxyTokenAuthed(r, info.Token) {
					// raw backend capability: read faces only (zero principal)
					resolved = true
				}
			}
			if !resolved {
				// Neither channel authenticates → canonical 401/503. The error
				// must stay readable for the opaque iframe (no CORS masking).
				allowOpaqueOriginRead(w, r)
				var authOK bool
				principal, authOK = s.enforceAuthPolicy(w, r, deviceCookie, reqID)
				if !authOK {
					return
				}
			}
		} else {
			allowOpaqueOriginRead(w, r)
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
		// 本地 403 必须可被 opaque iframe 读到（无 CORS 头会被浏览器掩盖成
		// network_error，用户看到的是误导性的「连接中断，消息未发送」）。
		allowOpaqueOriginRead(w, r)
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
		allowOpaqueOriginRead(w, r)
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
				// WS upgrade：offered 子协议对换写为后端 token。平面页现在 offer
				// `webui, <plane token>`（代理铸造）；后端按自身 token 校验子协议
				// 对，且只回显 "webui"（客户端也 offer 过），浏览器侧握手不受影响。
				if req.Header.Get("Sec-WebSocket-Protocol") != "" {
					req.Header.Set("Sec-WebSocket-Protocol", "webui, "+token)
				}
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
			allowOpaqueOriginRead(w, r)
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
			// fragment 承载 capability：不进请求行/日志/错误体。G3：下发的是
			// 代理铸造的 per-(session, device) plane token（绑定当前鉴权设备；
			// 出向统一换写为后端 token）——sandbox iframe 带不上 device cookie，
			// 只有设备绑定 token 才能让写面控制门评估真实控制权归属。raw
			// 后端 token 不再直接下发。铸造失败（rand）→ 不带 token fail-closed
			// （数据面 401，下次状态轮询恢复）。
			if tok, err := s.webuiGrants.plane(string(sessionID), string(principal.DeviceID), info.Token); err == nil {
				resp.URL += "#/t=" + tok
			}
		}
	}
	// 平面终态（unavailable/ended/unknown）→ 回收该会话的全部 plane token
	// grant（探测中 probing 不回收：平面可能原 token 回来）。
	switch state {
	case contract.WebUIPlaneStateUnavailable, contract.WebUIPlaneStateEnded, contract.WebUIPlaneStateUnknown:
		s.webuiGrants.pruneSession(string(sessionID))
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
