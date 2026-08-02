package remote

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
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
	// M1-B2a: fixed caps; prune 后满则签发/consume 失败，绝不 drop-oldest。
	maxLaunchGrants  = 64
	maxLocalSessions = 64
)

// Auth 管理远程 API 的 Bearer Token 认证
type Auth struct {
	token         [32]byte // raw primary token; zeroed on rotation
	tokenReady    bool
	launchGrants  map[string]launchGrant
	localSessions map[string]localSession
	reader        io.Reader
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
	return newAuthWithReader(rand.Reader)
}

// newAuthWithReader constructs an Auth with an injectable random source (tests).
func newAuthWithReader(r io.Reader) *Auth {
	a := &Auth{
		launchGrants:  make(map[string]launchGrant),
		localSessions: make(map[string]localSession),
		reader:        r,
	}
	a.regenerate()
	return a
}

// regenerate 生成新的随机 token（32 字节）。先清零旧 token（安全旋转），随机源失败
// → tokenReady=false（禁用），绝不 fallback。同时清旧 launch grants / local sessions。
func (a *Auth) regenerate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	zeroBytes(a.token[:]) // zero previous secret before overwriting
	var fresh [32]byte
	if _, err := io.ReadFull(a.reader, fresh[:]); err != nil {
		a.token = [32]byte{}
		a.tokenReady = false
		zeroBytes(fresh[:])
		// A failed rotation disables every legacy carrier and drops credentials
		// bound to the old token; they must not survive in memory for a later
		// successful regeneration.
		a.launchGrants = make(map[string]launchGrant)
		a.localSessions = make(map[string]localSession)
		return
	}
	a.token = fresh
	a.tokenReady = true
	fresh = [32]byte{} // zero stack copy
	// Security rotation: drop outstanding grants/sessions bound to the old token.
	a.launchGrants = make(map[string]launchGrant)
	a.localSessions = make(map[string]localSession)
}

// GetToken 返回当前 token 的 canonical hex（按需生成）。禁用时返回 ""。
func (a *Auth) GetToken() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.tokenReady {
		return ""
	}
	return hex.EncodeToString(a.token[:])
}

// RegenerateToken 生成新的随机 token 并返回新值（清旧 grants/sessions）。
func (a *Auth) RegenerateToken() string {
	a.regenerate()
	return a.GetToken()
}

// validate 校验 Authorization: Bearer 或冻结的 WS carrier query token。supplied
// token 必须 canonical 64 位 lowercase hex，decode 到 32-byte buffer 后与 owned
// expected [32]byte 用 subtle.ConstantTimeCompare（buffer 均请零）。恰好一个
// Authorization/query 值。query token 仅当 isAllowedLegacyTokenCarrier(r) 为真。
// 随机源失败（tokenReady=false）永不鉴权。REST ?token= 永远 401。
// validateWithCarrier validates and returns which legacy carrier authenticated
// the request (0 if not authenticated). Used by MiddlewareWithObserver.
func (a *Auth) validateWithCarrier(r *http.Request) (bool, LegacyAuthCarrier) {
	a.mu.RLock()
	ready := a.tokenReady
	expected := a.token // copy under lock
	a.mu.RUnlock()
	defer zeroBytes(expected[:])
	if !ready {
		return false, 0
	}

	authValues := r.Header.Values("Authorization")
	if len(authValues) > 1 {
		return false, 0
	}
	if len(authValues) == 1 {
		tok, ok := parseBearer(authValues[0])
		if !ok || !isCanonicalHex64(tok) {
			return false, 0
		}
		if matchHexToken(tok, expected) {
			return true, CarrierBearer
		}
		return false, 0
	}

	if isAllowedLegacyTokenCarrier(r) {
		tokenVals := r.URL.Query()["token"]
		if len(tokenVals) != 1 {
			return false, 0
		}
		tok := tokenVals[0]
		if !isCanonicalHex64(tok) {
			return false, 0
		}
		if matchHexToken(tok, expected) {
			return true, CarrierQueryToken
		}
		return false, 0
	}

	if a.validateLocalSession(r) {
		return true, CarrierLocalSession
	}
	return false, 0
}

// validate is kept for backward compatibility; it delegates to validateWithCarrier.
func (a *Auth) validate(r *http.Request) bool {
	ok, _ := a.validateWithCarrier(r)
	return ok
}

// matchHexToken decodes supplied canonical hex into a 32-byte buffer and
// constant-time-compares it against the owned expected bytes; all buffers are
// zeroed (defer) even on the comparison path.
func matchHexToken(suppliedHex string, expected [32]byte) bool {
	var supplied [32]byte
	defer zeroBytes(supplied[:])
	defer zeroBytes(expected[:])
	dec, err := hex.DecodeString(suppliedHex)
	if err != nil || len(dec) != 32 {
		if dec != nil {
			zeroBytes(dec)
		}
		return false
	}
	copy(supplied[:], dec)
	zeroBytes(dec)
	return subtle.ConstantTimeCompare(supplied[:], expected[:]) == 1
}

// parseBearer 解析单个 "Bearer <token>"，严格 scheme。
func parseBearer(h string) (string, bool) {
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	return strings.TrimSpace(parts[1]), true
}

// isCanonicalHex64 reports whether s is exactly 64 lowercase hex chars.
func isCanonicalHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func (a *Auth) IssueLaunchGrant(host string) string {
	normalizedHost := normalizeComparableHost(host)
	if normalizedHost == "" {
		normalizedHost = "127.0.0.1"
	}

	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.tokenReady {
		return "" // token disabled (rand failure) → carrier disabled
	}
	grant := a.randomHexToken(32)
	if grant == "" {
		return "" // 随机源失败 → 禁用 carrier，不返回 fallback grant
	}
	a.pruneExpiredLocked(now)
	if len(a.launchGrants) >= maxLaunchGrants {
		return "" // 满 cap → 拒绝签发，绝不 drop-oldest
	}
	a.launchGrants[grant] = launchGrant{
		host:      normalizedHost,
		expiresAt: now.Add(launchGrantTTL),
	}
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
	sessionID := a.randomHexToken(32)
	if sessionID == "" {
		return nil, fmt.Errorf("could not create local session") // 随机源失败 → 失败闭合，不 fallback
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.tokenReady {
		return nil, fmt.Errorf("security state unavailable") // token disabled
	}
	a.pruneExpiredLocked(now)
	if len(a.localSessions) >= maxLocalSessions {
		return nil, fmt.Errorf("local session capacity reached") // 满 cap → 失败，绝不 drop-oldest
	}

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

// randomHexToken 用注入随机源生成 size 字节 hex；defer 清零，即使 ReadFull partial-error
// 也清。失败返回 ""。
func (a *Auth) randomHexToken(size int) string {
	buf := make([]byte, size)
	defer zeroBytes(buf)
	if _, err := io.ReadFull(a.reader, buf); err != nil {
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
		// D-004: empty Origin is not a browser client this phase; reject before
		// Upgrade. Legacy token/query auth is untouched.
		return false
	}
	return canonicalAllowedOrigin(r, origin)
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
	return a.MiddlewareWithObserver(next, nil)
}

// MiddlewareWithObserver authenticates and, on success, invokes observer with
// the legacy carrier and request (so the caller can derive routeClass). The
// observer records the legacy_auth_deprecated event (server-side dedup). nil
// observer = no recording.
func (a *Auth) MiddlewareWithObserver(next http.Handler, observer func(LegacyAuthCarrier, *http.Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Direct defense: reject non-loopback BEFORE validate/observer (the global
		// buildHandler guard is authoritative, but this prevents a mis-mounted
		// observer from validating/observing a LAN request).
		if !isLoopbackPeer(r) {
			setLegacyCompatibilityHeaders(w.Header())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"forbidden"}`))
			return
		}
		ok, carrier := a.validateWithCarrier(r)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		if observer != nil {
			observer(carrier, r)
		}
		next.ServeHTTP(w, r)
	})
}
