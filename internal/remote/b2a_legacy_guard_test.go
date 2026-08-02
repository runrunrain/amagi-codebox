package remote

// M1-B2a legacy loopback / auth hardening tests (design §A/§D, Leader C-1/C-3).
// Production-entry spies verify: LAN legacy 403 with no provider/session/body
// decode call; loopback legacy works + deprecation headers; spoofed XFF /
// malformed RemoteAddr fail; static LAN works but /?token= 401; exact WS carrier
// works loopback and REST/extra/duplicate/non-upgrade reject; Bearer canonical
// constant-time; rand failure fails closed with no fallback substring; sensitive
// inner guard blocks non-loopback.

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
)

// b2aSpyApp records whether sensitive App methods were called.
type b2aSpyApp struct {
	providerExportCalls int
	providerSaveCalls   int
	sessionsCalls       int
	configSaveCalls     int
}

func (a *b2aSpyApp) GetAppInfo() map[string]any { return map[string]any{} }
func (a *b2aSpyApp) GetSessions() []session.SessionInfo {
	a.sessionsCalls++
	return nil
}
func (a *b2aSpyApp) GetSession(string) (session.SessionInfo, error) {
	return session.SessionInfo{}, errors.New("n/a")
}
func (a *b2aSpyApp) LaunchSession(string, string, string, string, bool, bool, string) (string, error) {
	return "", errors.New("n/a")
}
func (a *b2aSpyApp) LaunchCodexSession(string, string, string, string, string) (string, error) {
	return "", errors.New("n/a")
}
func (a *b2aSpyApp) LaunchOpenCode(string, string, string, string, string) (string, error) {
	return "", errors.New("n/a")
}
func (a *b2aSpyApp) LaunchPiSession(string, string, string, string, string) (string, error) {
	return "", errors.New("n/a")
}
func (a *b2aSpyApp) StopSession(string) error         { return nil }
func (a *b2aSpyApp) RemoveSession(string) error       { return nil }
func (a *b2aSpyApp) ClearStoppedSessions() int        { return 0 }
func (a *b2aSpyApp) PtyResize(string, int, int) error { return nil }
func (a *b2aSpyApp) GetProvidersByType(string) map[string]config.Provider {
	return nil
}
func (a *b2aSpyApp) GetProviderExportJSON(string) (string, error) {
	a.providerExportCalls++
	return "", errors.New("n/a")
}
func (a *b2aSpyApp) SaveProviderFromJSON(string, string) error {
	a.providerSaveCalls++
	return nil
}
func (a *b2aSpyApp) SaveAllConfig() error { a.configSaveCalls++; return nil }
func (a *b2aSpyApp) GetKeyDiagnostics() map[string]map[string]string {
	return nil
}
func (a *b2aSpyApp) GetLogs(string, string, string, int) []logging.Entry {
	return nil
}
func (a *b2aSpyApp) GetSettingsService() *settings.Service { return nil }
func (a *b2aSpyApp) GetPathsService() *paths.PathsService  { return nil }
func (a *b2aSpyApp) GetConfigService() *config.ConfigService {
	return nil
}
func (a *b2aSpyApp) SetRemotePort(int) error { return nil }

func newB2AServer(t *testing.T, app *b2aSpyApp) *Server {
	t.Helper()
	return NewServer(0, app, logging.NewService(t.TempDir()), embed.FS{})
}

func loopbackReq(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "127.0.0.1:54321"
	return r
}
func lanReq(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "192.168.1.55:41000"
	return r
}

func TestB2ALANLegacyIsForbiddenNoOracle(t *testing.T) {
	app := &b2aSpyApp{}
	srv := newB2AServer(t, app)
	h := srv.buildHandler()

	// LAN valid-looking token and invalid token both → 403, same response.
	for _, tok := range []string{srv.GetToken(), "deadbeef", ""} {
		r := lanReq("GET", "/api/providers/someprov?token="+tok)
		r.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("LAN legacy token=%q: code=%d want 403", tok, rr.Code)
		}
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("LAN 403 missing no-store")
		}
		if rr.Header().Get("X-Amagi-Compatibility-Epoch") != "1" {
			t.Fatal("LAN 403 missing epoch header")
		}
	}
	// No provider/session/App call happened (no oracle / no body decode).
	if app.providerExportCalls != 0 || app.sessionsCalls != 0 {
		t.Fatalf("LAN legacy reached App: provider=%d sessions=%d", app.providerExportCalls, app.sessionsCalls)
	}
}

func TestB2ALoopbackLegacyWorksAndHeaders(t *testing.T) {
	app := &b2aSpyApp{}
	srv := newB2AServer(t, app)
	h := srv.buildHandler()
	r := loopbackReq("GET", "/api/sessions")
	r.Header.Set("Authorization", "Bearer "+srv.GetToken())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("loopback legacy sessions: code=%d want 200", rr.Code)
	}
	if rr.Header().Get("Deprecation") != "true" || rr.Header().Get("X-Amagi-Removal-Epoch") != "3" {
		t.Fatal("loopback legacy response missing deprecation headers")
	}
	if app.sessionsCalls != 1 {
		t.Fatal("loopback legacy did not reach App")
	}
}

func TestB2ASpoofedForwardedHeaderFails(t *testing.T) {
	srv := newB2AServer(t, &b2aSpyApp{})
	h := srv.buildHandler()
	// RemoteAddr is LAN, but attacker spoofs X-Forwarded-For: 127.0.0.1.
	r := lanReq("GET", "/api/sessions")
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("Forwarded", "for=127.0.0.1")
	r.Header.Set("Authorization", "Bearer "+srv.GetToken())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("spoofed XFF must not bypass loopback: code=%d", rr.Code)
	}
}

func TestB2AMalformedRemoteAddrFails(t *testing.T) {
	srv := newB2AServer(t, &b2aSpyApp{})
	h := srv.buildHandler()
	for _, addr := range []string{"", "garbage", "127.0.0.1", "[::1]", "127.0.0.1:notaport"} {
		r := loopbackReq("GET", "/api/sessions")
		r.RemoteAddr = addr
		r.Header.Set("Authorization", "Bearer "+srv.GetToken())
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("malformed RemoteAddr %q: code=%d want 403", addr, rr.Code)
		}
	}
}

func TestB2AStaticLANWorksButRootTokenQueryUnauthorized(t *testing.T) {
	srv := newTestServerWithEmbeddedMobile(t, 0)
	h := srv.buildHandler()
	// Static root on LAN works (public).
	r := lanReq("GET", "/")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
		t.Fatalf("static LAN root should be public, got %d", rr.Code)
	}
	// But /?token= is 401 before static (never served/redirected).
	r2 := lanReq("GET", "/?token=abc")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("/?token= must be 401, got %d", rr2.Code)
	}
	if rr2.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("/?token= 401 missing no-store")
	}
}

func TestB2ATokenCarrierRejections(t *testing.T) {
	srv := newB2AServer(t, &b2aSpyApp{})
	tok := srv.GetToken()
	wsUpgrade := func(r *http.Request) *http.Request {
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Connection", "upgrade")
		return r
	}
	// Exact carrier is allowed (loopback + GET + ws/terminal/{id} + Upgrade +
	// Connection: upgrade + token + optional mode controller/observer).
	good := wsUpgrade(loopbackReq("GET", "/ws/terminal/sess1?token="+tok+"&mode=controller"))
	if !isAllowedLegacyTokenCarrier(good) {
		t.Fatal("exact WS carrier (controller) must be allowed")
	}
	goodObs := wsUpgrade(loopbackReq("GET", "/ws/terminal/sess1?token="+tok+"&mode=observer"))
	if !isAllowedLegacyTokenCarrier(goodObs) {
		t.Fatal("exact WS carrier (observer) must be allowed")
	}
	// mode absent → allowed.
	goodNoMode := wsUpgrade(loopbackReq("GET", "/ws/terminal/sess1?token="+tok))
	if !isAllowedLegacyTokenCarrier(goodNoMode) {
		t.Fatal("WS carrier without mode must be allowed")
	}
	// REST path with token → not a carrier.
	rest := loopbackReq("GET", "/api/providers/x?token="+tok)
	if isAllowedLegacyTokenCarrier(rest) {
		t.Fatal("REST path must not be a token carrier")
	}
	// extra query key → not a carrier.
	extra := wsUpgrade(loopbackReq("GET", "/ws/terminal/sess1?token="+tok+"&evil=1"))
	if isAllowedLegacyTokenCarrier(extra) {
		t.Fatal("extra query key must reject carrier")
	}
	// unknown mode → not a carrier.
	badMode := wsUpgrade(loopbackReq("GET", "/ws/terminal/sess1?token="+tok+"&mode=evil"))
	if isAllowedLegacyTokenCarrier(badMode) {
		t.Fatal("unknown mode must reject carrier")
	}
	// missing Connection: upgrade → not a carrier.
	noConn := loopbackReq("GET", "/ws/terminal/sess1?token="+tok)
	noConn.Header.Set("Upgrade", "websocket")
	if isAllowedLegacyTokenCarrier(noConn) {
		t.Fatal("missing Connection: upgrade must reject carrier")
	}
	// duplicate token → not a carrier.
	dup := wsUpgrade(loopbackReq("GET", "/ws/terminal/sess1?token="+tok+"&token="+tok))
	if isAllowedLegacyTokenCarrier(dup) {
		t.Fatal("duplicate token must reject carrier")
	}
	// non-upgrade → not a carrier.
	noup := loopbackReq("GET", "/ws/terminal/sess1?token="+tok)
	if isAllowedLegacyTokenCarrier(noup) {
		t.Fatal("non-upgrade must reject carrier")
	}
	// LAN carrier → not allowed (not loopback).
	lan := wsUpgrade(lanReq("GET", "/ws/terminal/sess1?token="+tok))
	if isAllowedLegacyTokenCarrier(lan) {
		t.Fatal("LAN must reject carrier")
	}
	// Loopback REST with valid token → 401 (Auth-internal query rejection).
	h := srv.buildHandler()
	r := loopbackReq("GET", "/api/providers/x?token="+tok)
	r.Header.Set("Authorization", "Bearer "+tok) // even with valid bearer, query token on REST must 401? No—bearer is valid. Test query-only:
	r2 := loopbackReq("GET", "/api/providers/x?token="+tok)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("loopback REST ?token= (no bearer) must be 401, got %d", rr2.Code)
	}
	_ = r
}

func TestB2ABearerConstantTimeAndCanonical(t *testing.T) {
	srv := newB2AServer(t, &b2aSpyApp{})
	tok := srv.GetToken()
	h := srv.buildHandler()
	// Valid Bearer → 200 (functional path).
	r := loopbackReq("GET", "/api/sessions")
	r.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid bearer: %d", rr.Code)
	}
	// Uppercase hex token → rejected (not canonical lowercase).
	upper := strings.ToUpper(tok)
	r2 := loopbackReq("GET", "/api/sessions")
	r2.Header.Set("Authorization", "Bearer "+upper)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("non-canonical (uppercase) token must be rejected: %d", rr2.Code)
	}
	// Two Authorization values → rejected.
	r3 := loopbackReq("GET", "/api/sessions")
	r3.Header.Add("Authorization", "Bearer "+tok)
	r3.Header.Add("Authorization", "Bearer "+tok)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, r3)
	if rr3.Code != http.StatusUnauthorized {
		t.Fatalf("multiple Authorization values must be rejected: %d", rr3.Code)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("entropy exhausted") }

func TestB2ARandFailureFailsClosedNoFallback(t *testing.T) {
	a := newAuthWithReader(failingReader{})
	if a.GetToken() != "" {
		t.Fatalf("rand failure must disable token (empty), got %q", a.GetToken())
	}
	if a.IssueLaunchGrant("127.0.0.1") != "" {
		t.Fatal("rand failure must return empty grant")
	}
	// validate must never succeed.
	r := loopbackReq("GET", "/api/sessions")
	r.Header.Set("Authorization", "Bearer anything")
	if a.validate(r) {
		t.Fatal("disabled auth must never validate")
	}
	// No insecure/fallback/time substring in any produced material.
	if strings.Contains(a.GetToken(), "fallback") || strings.Contains(a.GetToken(), "insecure") {
		t.Fatal("rand failure produced a fallback token")
	}
	// A working auth produces canonical lowercase hex (no fallback substring).
	good := newAuth()
	if !isCanonicalHex64(good.GetToken()) {
		t.Fatalf("production token not canonical hex64: %q", good.GetToken())
	}
}

func TestB2ASensitiveInnerGuardBlocksNonLoopback(t *testing.T) {
	srv := newB2AServer(t, &b2aSpyApp{})
	// Direct invocation of the inner guard with a LAN peer.
	guarded := srv.requireLoopbackPeer(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler must not run for non-loopback")
	})
	r := lanReq("GET", "/api/providers/x")
	rr := httptest.NewRecorder()
	guarded.ServeHTTP(rr, r)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("inner guard LAN: %d want 403", rr.Code)
	}
	// Loopback passes through.
	called := false
	guarded2 := srv.requireLoopbackPeer(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	guarded2.ServeHTTP(httptest.NewRecorder(), loopbackReq("GET", "/api/providers/x"))
	if !called {
		t.Fatal("inner guard must pass loopback through")
	}
}

func TestB2ABuildDesktopLaunchURLFailsClosedOnRandFailure(t *testing.T) {
	srv := newB2AServer(t, &b2aSpyApp{})
	srv.auth = newAuthWithReader(failingReader{})
	if url := srv.BuildDesktopLaunchURL(); url != "" {
		t.Fatalf("rand failure must yield empty URL, got %q", url)
	}
	// Working auth yields a non-empty loopback URL.
	srv.auth = newAuth()
	u := srv.BuildDesktopLaunchURL()
	if u == "" || !strings.HasPrefix(u, "http://127.0.0.1:") {
		t.Fatalf("launch URL unexpected: %q", u)
	}
}

// keep io import used (failingReader asserts Read contract).
var _ io.Reader = failingReader{}

// ===========================================================================
// B2a final-defense microfix tests
// ===========================================================================

// Point 1: direct auth.Middleware on REST with ?token=valid must 401 (Auth-internal,
// not only the top middleware).
func TestB2AAuthMiddlewareRejectsRESTQueryToken(t *testing.T) {
	srv := newB2AServer(t, &b2aSpyApp{})
	tok := srv.GetToken()
	mw := srv.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("REST handler must not run for query token")
	}))
	r := loopbackReq("GET", "/api/providers/x?token="+tok)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, r)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("auth.Middleware REST ?token=valid must be 401, got %d", rr.Code)
	}
	// But a valid carrier query token IS accepted by auth.validate directly.
	carrier := loopbackReq("GET", "/ws/terminal/sess1?token="+tok)
	carrier.Header.Set("Upgrade", "websocket")
	carrier.Header.Set("Connection", "upgrade")
	if !srv.auth.validate(carrier) {
		t.Fatal("auth.validate must accept the exact carrier query token")
	}
}

// Point 2: malformed / percent-encoded token key fails closed before static.
func TestB2AMalformedAndEncodedQueryFailClosed(t *testing.T) {
	srv := newTestServerWithEmbeddedMobile(t, 0)
	h := srv.buildHandler()
	// Percent-encoded `token` key (%74oken) → recognized as token → 401.
	r := lanReq("GET", "/?%74oken=abc")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("percent-encoded token key must be 401, got %d", rr.Code)
	}
	// Malformed query (invalid percent encoding) → parse error → 401 fail-closed.
	r2 := lanReq("GET", "/?%")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("malformed query must fail-closed 401, got %d", rr2.Code)
	}
	// Malformed query on legacy LAN → still rejected (no serve/oracle).
	r3 := lanReq("GET", "/api/sessions?%zz")
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, r3)
	if rr3.Code != http.StatusForbidden && rr3.Code != http.StatusUnauthorized {
		t.Fatalf("malformed legacy LAN query must be rejected, got %d", rr3.Code)
	}
}

// partialFailReader writes some bytes then errors, modeling a partial entropy read.
type partialFailReader struct{ written int }

func (p *partialFailReader) Read(b []byte) (int, error) {
	if p.written < 4 && len(b) > 0 {
		n := 4 - p.written
		if n > len(b) {
			n = len(b)
		}
		for i := 0; i < n; i++ {
			b[i] = 0x01
		}
		p.written += n
		return n, nil
	}
	return 0, errors.New("partial entropy failure")
}

// Point 3: partial failing reader → token disabled, grant empty, no fallback.
func TestB2APartialFailReaderDisablesToken(t *testing.T) {
	a := newAuthWithReader(&partialFailReader{})
	if a.GetToken() != "" {
		t.Fatalf("partial-fail reader must disable token, got %q", a.GetToken())
	}
	if a.IssueLaunchGrant("127.0.0.1") != "" {
		t.Fatal("partial-fail reader must yield empty grant")
	}
	if strings.Contains(a.GetToken(), "fallback") || strings.Contains(a.GetToken(), "insecure") {
		t.Fatal("partial-fail reader produced fallback material")
	}
}

// Point 6: launch grant / local session caps; Regenerate clears them.
func TestB2AGrantSessionCapsAndRotation(t *testing.T) {
	a := newAuth()
	// Fill grants to the cap.
	var last string
	for i := 0; i < maxLaunchGrants; i++ {
		g := a.IssueLaunchGrant("127.0.0.1")
		if g == "" {
			t.Fatalf("grant %d unexpectedly empty (cap %d)", i, maxLaunchGrants)
		}
		last = g
	}
	// One over the cap → fails (no drop-oldest).
	if over := a.IssueLaunchGrant("127.0.0.1"); over != "" {
		t.Fatalf("grant over cap must be empty, got %q", over)
	}
	// Regenerate clears outstanding grants (security rotation): the previously
	// issued grant is no longer present.
	a.RegenerateToken()
	// After rotation, the old grant must not be consumable. Build a same-origin
	// request and confirm consume fails (grant cleared).
	r := httptest.NewRequest("POST", "/api/bootstrap/consume", nil)
	r.RemoteAddr = "127.0.0.1:8680"
	r.Host = "127.0.0.1:8680"
	r.Header.Set("Origin", "http://127.0.0.1:8680")
	if _, err := a.ConsumeLaunchGrant(r, last); err == nil {
		t.Fatal("Regenerate must clear outstanding launch grants (old grant consumed)")
	}
	// Local session cap: pre-fill to cap, then consume must fail.
	a2 := newAuth()
	now := time.Now()
	a2.mu.Lock()
	for i := 0; i < maxLocalSessions; i++ {
		a2.localSessions[fmt.Sprintf("pre-%d", i)] = localSession{host: "127.0.0.1", expiresAt: now.Add(time.Hour)}
	}
	a2.mu.Unlock()
	grant := a2.IssueLaunchGrant("127.0.0.1")
	r2 := httptest.NewRequest("POST", "/api/bootstrap/consume", nil)
	r2.RemoteAddr = "127.0.0.1:8680"
	r2.Host = "127.0.0.1:8680"
	r2.Header.Set("Origin", "http://127.0.0.1:8680")
	if _, err := a2.ConsumeLaunchGrant(r2, grant); err == nil {
		t.Fatal("consume over local-session cap must fail (no drop-oldest)")
	}
}
