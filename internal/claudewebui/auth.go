package claudewebui

// auth.go — /api/* 的 Bearer 强制、CORS/预检与统一错误信封（design §5.2 auth、§6 冻结红线）。
//
// 逐条镜像 pi 安全模型（与 remote 反代 webui_proxy.go 的预检豁免/CORS 头面
// 对齐）：
//   - /api/* 强制 `Authorization: Bearer <token>`，token 等值用
//     subtle.ConstantTimeCompare（计时纪律与反代 webUIProxyTokenAuthed 一致）；
//   - API 响应在 Origin==null（sandbox opaque iframe）时带 ACAO:null——没有
//     它浏览器会把错误响应整个掩盖成 network_error，前端读不到真实错误码；
//   - 静态资源 ACAO:*（assets.go）；
//   - OPTIONS 预检本地 204（Authorization, Content-Type），不携 token；
//   - 错误体统一 {v:1,error:"<code>"}（错误响应也带 v——pi 前端对每个响应
//     硬校验 json.v===1 的教训，webui_proxy.go 根因修复 A 同款）；
//   - token 绝不进日志/query/path/错误体。

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// 错误码（claudewebui 数据面自己的最小码集；control.forbidden /
// service.down 与 remote 契约码值对齐，design §6.1）。
const (
	errUnauthorized     = "auth.unauthorized" // 缺 Authorization 头
	errForbidden        = "auth.forbidden"    // token 不等值
	errBadRequest       = "bad_request"       // 体损坏/超限/非 UTF-8/参数非法
	errMethodNotAllowed = "method_not_allowed"
)

// bearerAuthorized 校验 `Authorization: Bearer <token>` 与 plane token
// constant-time 等值。空 token 永不通过（fail-closed）。
func bearerAuthorized(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	rest, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(rest), []byte(token)) == 1
}

// applyAPICors 在 API 响应上按 Origin 分流：==null（sandbox opaque iframe）→
// ACAO:null（+credentials，镜像 allowOpaqueOriginRead），让 opaque 页面能读到
// 响应体（含错误码）；其余/缺失 → 不加 CORS 头（同源请求无需 CORS）。
func applyAPICors(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") == "null" {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "null")
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Add("Vary", "Origin")
	}
}

// writePreflight 本地应答 CORS 预检 204（Authorization, Content-Type），
// 不转发、不鉴权——预检按规范不携带凭据（镜像 webui_proxy.go
// isWebUIProxyPreflight 的头面）。
func writePreflight(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "null")
	h.Add("Vary", "Origin")
	h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	h.Set("Access-Control-Allow-Credentials", "true")
	w.WriteHeader(http.StatusNoContent)
}

// errorBody 是统一错误信封（错误响应也带 v）。
type errorBody struct {
	V     int    `json:"v"`
	Error string `json:"error"`
}

// writeAPIError 写出统一错误信封（含 CORS 分流）。错误体绝不携带 token。
func writeAPIError(w http.ResponseWriter, r *http.Request, status int, code string) {
	applyAPICors(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{V: 1, Error: code})
}

// writeJSON 写出 2xx JSON 响应（含 CORS 分流）。
func writeJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	applyAPICors(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
