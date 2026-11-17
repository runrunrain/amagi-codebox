package agentcli

// auth.go — agent-CLI 服务端的三重校验 + 令牌校验（DESIGN §3.2）。
//
// 校验顺序（先于一切业务路由，包括未知路径，避免路由差异泄漏）：
//  1. 对端（RemoteAddr）必须回环 —— 阻断跨机直连；
//  2. 带 Origin 头即拒绝 —— 浏览器页面不可达（防 CSRF/页面脚本探测）；
//  3. Host 头（剥端口）必须是 127.0.0.1/localhost/[::1] —— 阻断 DNS rebinding；
//  4. 禁用态 → 503 cli_disabled（先于令牌：禁用后旧令牌已作废，若先查令牌
//     会把"被关闭"误报成"未授权"）；
//  5. X-Cli-Token 常量时间比较 —— 令牌缺失/错误 → 401 unauthorized。

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"strconv"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
)

// loopbackPeer 判定 TCP 对端是否回环（算法对齐 internal/remote/server.go
// isLoopbackPeer：SplitHostPort 成功 + 合法数字端口 + 无 IPv6 zone +
// ParseIP 成功 + IsLoopback）。
func loopbackPeer(r *http.Request) bool {
	host, portStr, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	if portStr == "" {
		return false
	}
	port, perr := strconv.Atoi(portStr)
	if perr != nil || port < 1 || port > 65535 {
		return false
	}
	if strings.ContainsAny(host, "%") { // reject IPv6 zone
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// 精确允许的 Host host 部分（评审 D-09：不接受 127/8 其它地址等宽回环段）。
var (
	allowedHostLoopbackV4 = net.ParseIP("127.0.0.1")
	allowedHostLoopbackV6 = net.ParseIP("::1")
)

// validLoopbackHost 校验 Host 头（评审 D-09 精确边界）：host 部分必须是
// 精确允许集合 127.0.0.1/localhost/[::1]（大小写不敏感；IPv6 字面量按地址
// 等价归一），端口必须显式携带且等于 listener 实际端口。127/8 其它地址
// （如 127.0.0.2）、无端口、端口不符一律拒绝——阻断 DNS rebinding 与宽松
// 回环段带来的意外面。本服务永远监听非默认的随机端口，合法客户端必然携带
// 显式端口。
func validLoopbackHost(r *http.Request, listenPort int) bool {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		return false // Host 必须带端口
	}
	if listenPort <= 0 || port != strconv.Itoa(listenPort) {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.Equal(allowedHostLoopbackV4) || ip.Equal(allowedHostLoopbackV6))
}

// tokenEqual 常量时间令牌比较：先做 SHA-256 摘要消除长度差异的时序泄漏，
// 再 ConstantTimeCompare。空 want（禁用/停止态）恒拒绝。
func tokenEqual(got, want string) bool {
	if want == "" {
		return false
	}
	gh := sha256.Sum256([]byte(got))
	wh := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gh[:], wh[:]) == 1
}

// gate 执行三重校验 + 禁用/令牌检查；未通过时写响应并返回 false。
func (s *Server) gate(w http.ResponseWriter, r *http.Request) bool {
	if !loopbackPeer(r) {
		s.writeError(w, http.StatusForbidden, spec.Errorf(spec.ErrForbidden, "对端必须为本机回环"))
		return false
	}
	// Origin 只看头是否存在（评审 D-09）：空值 Origin 同样是浏览器形态的
	// 请求，用 Header map 存在性判断，而非非空值判断。
	if _, ok := r.Header["Origin"]; ok {
		s.writeError(w, http.StatusForbidden, spec.Errorf(spec.ErrForbidden, "拒绝携带 Origin 的请求（浏览器页面不可达）"))
		return false
	}
	if !validLoopbackHost(r, s.listenPort()) {
		s.writeError(w, http.StatusForbidden, spec.Errorf(spec.ErrForbidden, "Host 头必须是 127.0.0.1/localhost/[::1] 且端口与监听端口一致"))
		return false
	}
	s.mu.Lock()
	enabled, token := s.enabled, s.token
	s.mu.Unlock()
	if !enabled {
		s.writeError(w, http.StatusServiceUnavailable, &spec.Error{
			Code:    spec.ErrDisabled,
			Message: "命令行访问已被关闭",
			Hint:    "可在桌面应用设置中重新开启",
		})
		return false
	}
	if !tokenEqual(r.Header.Get(spec.HeaderToken), token) {
		s.writeError(w, http.StatusUnauthorized, spec.Errorf(spec.ErrUnauthorized, "令牌缺失或无效"))
		return false
	}
	return true
}

// listenPort 返回 listener 实际端口（未运行为 0，Host 校验自然拒绝）。
func (s *Server) listenPort() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}
