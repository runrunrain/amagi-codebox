package remote

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	localSessionCookieName = "amagi_codebox_local_session"
	launchGrantTTL         = 2 * time.Minute
	localSessionTTL        = 12 * time.Hour
)

// Auth 管理远程 API 的 Bearer Token 认证
type Auth struct {
	token         string
	launchGrants  map[string]launchGrant
	localSessions map[string]localSession
	mu            sync.RWMutex
}

type launchGrant struct {
	host      string
	expiresAt time.Time
}

type localSession struct {
	host      string
	expiresAt time.Time
}

func newAuth() *Auth {
	a := &Auth{
		launchGrants:  make(map[string]launchGrant),
		localSessions: make(map[string]localSession),
	}
	a.regenerate()
	return a
}

// regenerate 生成新的随机 token（32字节 hex = 64字符）
func (a *Auth) regenerate() {
	buf := make([]byte, 32)
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := rand.Read(buf); err != nil {
		// 极端情况：rand 失败，使用固定占位符（不影响启动，但安全性降低）
		a.token = "insecure-fallback-token-rand-failed"
		return
	}
	a.token = hex.EncodeToString(buf)
}

// GetToken 返回当前 token
func (a *Auth) GetToken() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.token
}

// RegenerateToken 生成新的随机 token 并返回新值
func (a *Auth) RegenerateToken() string {
	a.regenerate()
	return a.GetToken()
}

// validate 校验 Authorization header 或 URL 参数中的 token
func (a *Auth) validate(r *http.Request) bool {
	a.mu.RLock()
	expected := a.token
	a.mu.RUnlock()

	// 检查 Authorization: Bearer <token>
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") && parts[1] == expected {
			return true
		}
	}

	// 检查 URL 参数 ?token=xxx（WebSocket 使用）
	if t := r.URL.Query().Get("token"); t == expected {
		return true
	}

	if a.validateLocalSession(r) {
		return true
	}

	return false
}

func (a *Auth) IssueLaunchGrant(host string) string {
	normalizedHost := normalizeComparableHost(host)
	if normalizedHost == "" {
		normalizedHost = "127.0.0.1"
	}

	grant := randomHexToken(32)
	if grant == "" {
		grant = fmt.Sprintf("launch-fallback-%d", time.Now().UnixNano())
	}

	now := time.Now()
	a.mu.Lock()
	a.pruneExpiredLocked(now)
	a.launchGrants[grant] = launchGrant{
		host:      normalizedHost,
		expiresAt: now.Add(launchGrantTTL),
	}
	a.mu.Unlock()
	return grant
}

func (a *Auth) ConsumeLaunchGrant(r *http.Request, grant string) (*http.Cookie, error) {
	trimmedGrant := strings.TrimSpace(grant)
	if trimmedGrant == "" {
		return nil, fmt.Errorf("missing launch grant")
	}
	if !isTrustedSameOriginBrowserRequest(r) {
		return nil, fmt.Errorf("launch grant requires same-origin browser request")
	}

	requestHost := normalizeComparableHost(hostWithoutPort(r.Host))
	if requestHost == "" {
		return nil, fmt.Errorf("invalid request host")
	}

	now := time.Now()
	sessionID := randomHexToken(32)
	if sessionID == "" {
		sessionID = fmt.Sprintf("session-fallback-%d", now.UnixNano())
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneExpiredLocked(now)

	storedGrant, ok := a.launchGrants[trimmedGrant]
	if !ok {
		return nil, fmt.Errorf("launch grant expired or invalid")
	}
	delete(a.launchGrants, trimmedGrant)

	if !strings.EqualFold(storedGrant.host, requestHost) {
		return nil, fmt.Errorf("launch grant host mismatch")
	}

	a.localSessions[sessionID] = localSession{
		host:      requestHost,
		expiresAt: now.Add(localSessionTTL),
	}

	return &http.Cookie{
		Name:     localSessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(localSessionTTL / time.Second),
		Expires:  now.Add(localSessionTTL),
	}, nil
}

func (a *Auth) validateLocalSession(r *http.Request) bool {
	if !isTrustedSameOriginBrowserRequest(r) {
		return false
	}

	cookie, err := r.Cookie(localSessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return false
	}

	requestHost := normalizeComparableHost(hostWithoutPort(r.Host))
	if requestHost == "" {
		return false
	}

	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneExpiredLocked(now)

	session, ok := a.localSessions[cookie.Value]
	if !ok {
		return false
	}
	return strings.EqualFold(session.host, requestHost)
}

func (a *Auth) pruneExpiredLocked(now time.Time) {
	for key, grant := range a.launchGrants {
		if !grant.expiresAt.After(now) {
			delete(a.launchGrants, key)
		}
	}
	for key, session := range a.localSessions {
		if !session.expiresAt.After(now) {
			delete(a.localSessions, key)
		}
	}
}

func randomHexToken(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

func isTrustedSameOriginBrowserRequest(r *http.Request) bool {
	expectedOrigin := requestOrigin(r)
	if expectedOrigin == "" {
		return false
	}

	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return sameOrigin(origin, expectedOrigin)
	}

	if fetchSite := strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")); fetchSite != "" {
		if !strings.EqualFold(fetchSite, "same-origin") {
			return false
		}
		if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
			return sameOrigin(referer, expectedOrigin)
		}
		return true
	}

	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return sameOrigin(referer, expectedOrigin)
	}

	return false
}

func isAllowedWebSocketOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	return sameOrigin(origin, requestOrigin(r))
}

func isAllowedCORSOrigin(r *http.Request, origin string) bool {
	return sameOrigin(origin, requestOrigin(r))
}

func requestOrigin(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return strings.ToLower(fmt.Sprintf("%s://%s", scheme, host))
}

func sameOrigin(candidate string, expected string) bool {
	trimmedExpected := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(expected)), "/")
	if trimmedExpected == "" {
		return false
	}
	trimmedCandidate := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(candidate)), "/")
	return trimmedCandidate == trimmedExpected || strings.HasPrefix(trimmedCandidate, trimmedExpected+"/")
}

func hostWithoutPort(hostport string) string {
	trimmed := strings.TrimSpace(hostport)
	if trimmed == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		return host
	}
	return trimmed
}

func normalizeComparableHost(host string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(hostWithoutPort(host), "]"), "["))
	if trimmed == "" {
		return ""
	}
	if addr, err := netip.ParseAddr(trimmed); err == nil {
		return addr.String()
	}
	return strings.ToLower(trimmed)
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	trimmed := strings.TrimSpace(remoteAddr)
	if trimmed == "" {
		return false
	}

	if addrPort, err := netip.ParseAddrPort(trimmed); err == nil {
		return addrPort.Addr().IsLoopback()
	}

	trimmed = strings.TrimPrefix(strings.TrimSuffix(trimmed, "]"), "[")
	if zoneIndex := strings.Index(trimmed, "%"); zoneIndex >= 0 {
		trimmed = trimmed[:zoneIndex]
	}

	addr, err := netip.ParseAddr(trimmed)
	if err != nil {
		return false
	}
	return addr.IsLoopback()
}

// Middleware 返回验证 Bearer Token 的 HTTP 中间件
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.validate(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
