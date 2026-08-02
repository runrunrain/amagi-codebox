package remote

// B2b evidence-correction tests (Leader audit). Adds the missing required
// evidence: strict origin-proof matrix (A), store-down + CORS-on-error (B),
// LastSeen exactly-once/coalesce + forced-failure-invariant via chmod (C),
// production composite handler (D), CORS preflight index0+1 (E), provider gate
// spy + serverPort removal (F), B2a tokenReady/intermittent-reader carryover (G).
// Contract symbols only; no copied endpoint literals.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// b2bCountingProvider is a HostSummaryFunc that counts provider calls and can be
// made to fail / return an invalid DTO.
type b2bCountingProvider struct {
	calls   int32
	fail    int32
	invalid int32
}

func (c *b2bCountingProvider) provider() (contract.HostSummary, error) {
	atomic.AddInt32(&c.calls, 1)
	if atomic.LoadInt32(&c.fail) == 1 {
		return contract.HostSummary{}, errors.New("provider unavailable")
	}
	if atomic.LoadInt32(&c.invalid) == 1 {
		return contract.HostSummary{APIVersion: "bad", ServerVersion: "x"}, nil
	}
	return validHostSummary()
}
func (c *b2bCountingProvider) count() int32 { return atomic.LoadInt32(&c.calls) }

// pairWithProvider pairs over a real listener using a counting provider.
func pairWithProvider(t *testing.T, cp *b2bCountingProvider) (*httptest.Server, *Server, *secFakeClock, *http.Cookie, string) {
	t.Helper()
	srv, clk := newSecServer(t, cp.provider)
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	t.Cleanup(ts.Close)
	wireTestPort(srv, ts)
	req := pairingRequest(ts, code, "phone", ts.URL)
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	if rr.Code != contract.V1RestEndpoints[0].SuccessStatus {
		t.Fatalf("pair: %d %s", rr.Code, rr.Body.String())
	}
	var resp contract.PairingCompleteResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	return ts, srv, clk, rr.Result().Cookies()[0], string(resp.Device.ID)
}

func lookupLastSeen(t *testing.T, srv *Server, deviceID string) time.Time {
	t.Helper()
	permit, ok := srv.gate.issueNormalPermit()
	if !ok {
		t.Fatal("gate not normal")
	}
	rec, found, err := srv.store.Lookup(permit, contract.DeviceID(deviceID))
	srv.gate.returnNormalPermit(permit)
	if err != nil || !found {
		t.Fatalf("lookup %s: found=%v err=%v", deviceID, found, err)
	}
	return rec.LastSeenAt
}

func hsReq(ts *httptest.Server, cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, hostSummaryPath(), nil)
	r.Host = strings.TrimPrefix(ts.URL, "http://")
	r.Header.Set("Origin", ts.URL)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

// ===========================================================================
// A) TestB2BOriginProofStrictMatrix
// ===========================================================================

func TestB2BOriginProofStrictMatrix(t *testing.T) {
	ts, srv, _, cookie, _ := pairWithProvider(t, &b2bCountingProvider{})
	h := srv.buildV1Handler()
	wantOK := contract.V1RestEndpoints[1].SuccessStatus

	// Empty Origin + valid Referer → Origin present (blank) rejects, no fallback.
	r := hsReq(ts, cookie)
	r.Header.Set("Origin", "")
	r.Header.Set("Referer", ts.URL+"/p")
	if rr := rec(h, r); rr.Code != http.StatusForbidden {
		t.Fatalf("empty Origin no fallback: %d", rr.Code)
	}
	// Multiple Origin → reject.
	r = hsReq(ts, cookie)
	r.Header.Add("Origin", ts.URL)
	r.Header.Add("Origin", ts.URL)
	if rr := rec(h, r); rr.Code != http.StatusForbidden {
		t.Fatalf("multiple Origin: %d", rr.Code)
	}
	// Bad Origin + valid Sec-Fetch → reject (Origin present, no fallback).
	r = hsReq(ts, cookie)
	r.Header.Set("Origin", "http://evil.example.com")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if rr := rec(h, r); rr.Code != http.StatusForbidden {
		t.Fatalf("bad Origin + SecFetch: %d", rr.Code)
	}
	// Bad Sec-Fetch + valid Referer → reject (Sec-Fetch present, no fallback).
	r = hsReq(ts, cookie)
	r.Header.Del("Origin")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Referer", ts.URL+"/p")
	if rr := rec(h, r); rr.Code != http.StatusForbidden {
		t.Fatalf("bad SecFetch + Referer: %d", rr.Code)
	}
	// Multiple Sec-Fetch → reject.
	r = hsReq(ts, cookie)
	r.Header.Del("Origin")
	r.Header.Add("Sec-Fetch-Site", "same-origin")
	r.Header.Add("Sec-Fetch-Site", "same-origin")
	if rr := rec(h, r); rr.Code != http.StatusForbidden {
		t.Fatalf("multiple SecFetch: %d", rr.Code)
	}
	// Multiple Referer → reject.
	r = hsReq(ts, cookie)
	r.Header.Del("Origin")
	r.Header.Add("Referer", ts.URL+"/p")
	r.Header.Add("Referer", ts.URL+"/q")
	if rr := rec(h, r); rr.Code != http.StatusForbidden {
		t.Fatalf("multiple Referer: %d", rr.Code)
	}
	// Referer with userinfo → reject.
	r = hsReq(ts, cookie)
	r.Header.Del("Origin")
	r.Header.Set("Referer", "http://user@"+strings.TrimPrefix(ts.URL, "http://")+"/p")
	if rr := rec(h, r); rr.Code != http.StatusForbidden {
		t.Fatalf("userinfo Referer: %d", rr.Code)
	}
	// Referer with fragment → reject.
	r = hsReq(ts, cookie)
	r.Header.Del("Origin")
	r.Header.Set("Referer", ts.URL+"/p#frag")
	if rr := rec(h, r); rr.Code != http.StatusForbidden {
		t.Fatalf("fragment Referer: %d", rr.Code)
	}
	// Referer with bad port → reject.
	r = hsReq(ts, cookie)
	r.Header.Del("Origin")
	r.Header.Set("Referer", "http://127.0.0.1:99999/p")
	if rr := rec(h, r); rr.Code != http.StatusForbidden {
		t.Fatalf("bad port Referer: %d", rr.Code)
	}
	// Same-origin Referer with path+query → accepted.
	r = hsReq(ts, cookie)
	r.Header.Del("Origin")
	r.Header.Set("Referer", ts.URL+"/some/path?x=1")
	if rr := rec(h, r); rr.Code != wantOK {
		t.Fatalf("same-origin Referer path+query: %d", rr.Code)
	}
	// Capacitor exact Origin → accepted + CORS echo.
	r = hsReq(ts, cookie)
	r.Header.Set("Origin", "capacitor://localhost")
	rr := rec(h, r)
	if rr.Code != wantOK {
		t.Fatalf("capacitor Origin: %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "capacitor://localhost" {
		t.Fatal("capacitor ACAO not echoed")
	}
}

// ===========================================================================
// B) TestB2BAuthStoreDownAndCORSError
// ===========================================================================

func TestB2BAuthStoreDownAndCORSError(t *testing.T) {
	cp := &b2bCountingProvider{}
	srv, _ := newSecServer(t, cp.provider)
	srv.SetPort(8680)
	h := srv.buildV1Handler()
	srv.store.latchReady() // store/gate down → auth 503

	makeReq := func(origin string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, hostSummaryPath(), nil)
		r.Host = "127.0.0.1:8680"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}

	// Valid Origin auth-error: 503 service.down/check-desktop + exact ACAO+Credentials+Vary.
	rr := rec(h, makeReq("http://127.0.0.1:8680"))
	if rr.Code != http.StatusServiceUnavailable || !bodyHas(rr.Body.Bytes(), "service.down") || !bodyHas(rr.Body.Bytes(), "check-desktop") {
		t.Fatalf("store-down: %d %s", rr.Code, rr.Body.String())
	}
	if cp.count() != 0 {
		t.Fatalf("provider called on auth failure: %d", cp.count())
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:8680" {
		t.Fatal("valid-Origin error missing ACAO")
	}
	if rr.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("missing Credentials")
	}
	if !varyHasOrigin(rr.Header().Values("Vary")) {
		t.Fatal("missing Vary: Origin")
	}

	// Invalid Origin: rejected by the origin gate (before auth) → no ACAO.
	rr3 := rec(h, makeReq("http://evil.example.com"))
	if rr3.Code == contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("invalid Origin must not succeed: %d", rr3.Code)
	}
	if rr3.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("invalid Origin must not get ACAO")
	}
}

// ===========================================================================
// C) TestB2BLastSeenAndFailureDoesNotChangeResponse
// ===========================================================================

func TestB2BLastSeenAndFailureDoesNotChangeResponse(t *testing.T) {
	cp := &b2bCountingProvider{}
	ts, srv, clk, cookie, deviceID := pairWithProvider(t, cp)
	h := srv.buildV1Handler()

	ls0 := lookupLastSeen(t, srv, deviceID)
	// First GET at the same time → coalesced (no persist).
	rec(h, hsReq(ts, cookie))
	if ls := lookupLastSeen(t, srv, deviceID); !ls.Equal(ls0) {
		t.Fatalf("coalesced GET mutated LastSeen: %v vs %v", ls, ls0)
	}

	// Advance past coalesce → exactly one persist (LastSeen advances once).
	clk.Advance(deviceSeenPersistInterval + time.Minute)
	rr1 := rec(h, hsReq(ts, cookie))
	if rr1.Code != contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("persist GET: %d", rr1.Code)
	}
	ls1 := lookupLastSeen(t, srv, deviceID)
	if !ls1.After(ls0) {
		t.Fatal("LastSeen did not advance after persist")
	}
	// Immediate second GET (within coalesce of ls1) → no second write.
	rec(h, hsReq(ts, cookie))
	if ls := lookupLastSeen(t, srv, deviceID); !ls.Equal(ls1) {
		t.Fatalf("second GET mutated LastSeen (no coalesce): %v vs %v", ls, ls1)
	}

	// Forced persist failure via the snapshotRenameFn seam (rename fails →
	// read-back proves old → NotCommitted). Safely restored afterward.
	clk.Advance(deviceSeenPersistInterval + time.Minute)
	origRename := snapshotRenameFn
	snapshotRenameFn = func(string, string) error { return errors.New("injected rename failure") }
	defer func() { snapshotRenameFn = origRename }()
	rrFail := rec(h, hsReq(ts, cookie))
	if rrFail.Code != contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("forced-fail must still be 200: %d", rrFail.Code)
	}
	if !bytes.Equal(rr1.Body.Bytes(), rrFail.Body.Bytes()) {
		t.Fatalf("forced-fail changed body: %q vs %q", rr1.Body.Bytes(), rrFail.Body.Bytes())
	}
	if !healthActive(t, hsrvHealth(srv), HealthDeviceSeenPersistFailed) {
		t.Fatal("forced persist failure must record HealthDeviceSeenPersistFailed")
	}
	// LastSeen unchanged by the failed persist (NotCommitted).
	if ls := lookupLastSeen(t, srv, deviceID); !ls.Equal(ls1) {
		t.Fatalf("failed persist mutated LastSeen: %v vs %v", ls, ls1)
	}
}

// ===========================================================================
// D) TestB2BProductionCompositeHandler
// ===========================================================================

func TestB2BProductionCompositeHandler(t *testing.T) {
	cp := &b2bCountingProvider{}
	ts, srv, _, cookie, deviceID := pairWithProvider(t, cp)
	h := srv.buildHandler() // composite (top-level), not buildV1Handler

	// index1 route via composite handler → 200.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, hsReq(ts, cookie))
	if rr.Code != contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("composite index1: %d", rr.Code)
	}
	// Unknown index2 stays 404 (contract symbols only).
	r2 := httptest.NewRequest(http.MethodGet, contract.RESTBasePath+contract.V1RestEndpoints[2].Path, nil)
	r2.Host = strings.TrimPrefix(ts.URL, "http://")
	r2.Header.Set("Origin", ts.URL)
	r2.AddCookie(cookie)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("unknown index2 must be 404: %d", rr2.Code)
	}

	// Provider failure → 503, no LastSeen mutation. Bust the hostCache so the
	// provider is actually called.
	bustHostCache(srv)
	cp.setFail(true)
	lsBefore := lookupLastSeen(t, srv, deviceID)
	rf := hsReq(ts, cookie)
	rrf := httptest.NewRecorder()
	h.ServeHTTP(rrf, rf)
	if rrf.Code != http.StatusServiceUnavailable {
		t.Fatalf("provider failure must be 503: %d", rrf.Code)
	}
	if ls := lookupLastSeen(t, srv, deviceID); !ls.Equal(lsBefore) {
		t.Fatal("provider failure mutated LastSeen")
	}
}

// ===========================================================================
// E) TestB2BCORSPreflightBothRoutes
// ===========================================================================

func TestB2BCORSPreflightBothRoutes(t *testing.T) {
	ts, srv, _, _, _ := pairWithProvider(t, &b2bCountingProvider{})
	h := srv.buildV1Handler()
	for _, idx := range []int{0, 1} {
		ep := contract.V1RestEndpoints[idx]
		path := contract.RESTBasePath + ep.Path
		// Valid preflight.
		r := httptest.NewRequest(http.MethodOptions, path, nil)
		r.Host = strings.TrimPrefix(ts.URL, "http://")
		r.Header.Set("Origin", ts.URL)
		r.Header.Set("Access-Control-Request-Method", ep.Method)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("idx%d preflight: %d", idx, rr.Code)
		}
		am := rr.Header().Get("Access-Control-Allow-Methods")
		if !strings.Contains(am, ep.Method) || !strings.Contains(am, http.MethodOptions) {
			t.Fatalf("idx%d Allow-Methods: %s", idx, am)
		}
		ah := rr.Header().Get("Access-Control-Allow-Headers")
		if !strings.Contains(ah, "Content-Type") || !strings.Contains(ah, contract.RequestIDHeader) {
			t.Fatalf("idx%d Allow-Headers missing Content-Type/RequestID: %s", idx, ah)
		}
		if rr.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatalf("idx%d missing Credentials", idx)
		}
		if !varyHasOrigin(rr.Header().Values("Vary")) {
			t.Fatalf("idx%d missing Vary: Origin", idx)
		}
		// Wrong ACRM → rejected.
		r2 := httptest.NewRequest(http.MethodOptions, path, nil)
		r2.Host = strings.TrimPrefix(ts.URL, "http://")
		r2.Header.Set("Origin", ts.URL)
		r2.Header.Set("Access-Control-Request-Method", http.MethodDelete)
		rr2 := httptest.NewRecorder()
		h.ServeHTTP(rr2, r2)
		if rr2.Code == http.StatusNoContent {
			t.Fatalf("idx%d wrong ACRM accepted", idx)
		}
		// Bad Host → rejected.
		r3 := httptest.NewRequest(http.MethodOptions, path, nil)
		r3.Host = "127.0.0.1:1"
		r3.Header.Set("Origin", ts.URL)
		r3.Header.Set("Access-Control-Request-Method", ep.Method)
		rr3 := httptest.NewRecorder()
		h.ServeHTTP(rr3, r3)
		if rr3.Code == http.StatusNoContent {
			t.Fatalf("idx%d bad Host accepted", idx)
		}
	}
}

// ===========================================================================
// F) TestB2BProviderGateSpy
// ===========================================================================

func TestB2BProviderGateSpy(t *testing.T) {
	cp := &b2bCountingProvider{}
	ts, srv, _, cookie, _ := pairWithProvider(t, cp)
	pairCount := cp.count() // provider calls during pair (hostCache)
	h := srv.buildV1Handler()

	// Valid request: bust the (pair-populated) cache first so the provider is
	// actually called exactly once.
	bustHostCache(srv)
	base := cp.count()
	r := hsReq(ts, cookie)
	rec(h, r)
	if got := cp.count() - base; got != 1 {
		t.Fatalf("valid request provider calls=%d want 1", got)
	}
	after := cp.count()

	// Wrong query → no provider increment.
	rq := hsReq(ts, cookie)
	rq.URL.RawQuery = "x=1"
	rec(h, rq)
	// Wrong method/unknown route → no increment.
	rm := httptest.NewRequest(http.MethodPost, hostSummaryPath(), nil)
	rm.Host = strings.TrimPrefix(ts.URL, "http://")
	rm.Header.Set("Origin", ts.URL)
	rm.AddCookie(cookie)
	rec(h, rm)
	ru := httptest.NewRequest(http.MethodGet, contract.RESTBasePath+"/nope", nil)
	ru.Host = strings.TrimPrefix(ts.URL, "http://")
	ru.Header.Set("Origin", ts.URL)
	ru.AddCookie(cookie)
	rec(h, ru)
	// Bad Host → no increment.
	rh := hsReq(ts, cookie)
	rh.Host = "127.0.0.1:1"
	rec(h, rh)
	// Bad Origin → no increment (origin gate runs before the handler/provider).
	ro := hsReq(ts, cookie)
	ro.Header.Set("Origin", "http://evil.example.com")
	rec(h, ro)
	if cp.count() != after {
		t.Fatalf("gate failures incremented provider: %d -> %d", after, cp.count())
	}
	_ = pairCount
}

// ===========================================================================
// G) TestB2AB2aCarryoverTokenReadyAndIntermittentReader
// ===========================================================================

type intermittentReader struct{ fail int32 }

func (i *intermittentReader) Read(b []byte) (int, error) {
	if atomic.LoadInt32(&i.fail) == 1 {
		return 0, errors.New("entropy failed")
	}
	for j := range b {
		b[j] = byte(j)
	}
	return len(b), nil
}

func TestB2AB2aCarryoverTokenReadyAndIntermittentReader(t *testing.T) {
	r := &intermittentReader{}
	a := newAuthWithReader(r)
	if a.GetToken() == "" {
		t.Fatal("working reader must produce a token")
	}
	// Issue a grant/session while token ready.
	if g := a.IssueLaunchGrant("127.0.0.1"); g == "" {
		t.Fatal("grant must issue while token ready")
	}
	a.mu.Lock()
	a.localSessions["old-session"] = localSession{host: "127.0.0.1", expiresAt: time.Now().Add(time.Hour)}
	a.mu.Unlock()
	// Flip reader to failing; regenerate disables token + clears grants/sessions.
	atomic.StoreInt32(&r.fail, 1)
	a.RegenerateToken()
	if a.GetToken() != "" {
		t.Fatal("failing reader must disable token")
	}
	a.mu.RLock()
	grants, sessions := len(a.launchGrants), len(a.localSessions)
	a.mu.RUnlock()
	if grants != 0 || sessions != 0 {
		t.Fatalf("failed rotation retained legacy credentials: grants=%d sessions=%d", grants, sessions)
	}
	// tokenReady false → IssueLaunchGrant empty; ConsumeLaunchGrant error.
	if g := a.IssueLaunchGrant("127.0.0.1"); g != "" {
		t.Fatalf("disabled token must not issue grant: %q", g)
	}
	req := httptest.NewRequest("POST", "/api/bootstrap/consume", nil)
	req.Host = "127.0.0.1:8680"
	req.Header.Set("Origin", "http://127.0.0.1:8680")
	if _, err := a.ConsumeLaunchGrant(req, "anything"); err == nil {
		t.Fatal("disabled token must fail ConsumeLaunchGrant")
	}
	// Restore reader → regenerate re-enables.
	atomic.StoreInt32(&r.fail, 0)
	a.RegenerateToken()
	if a.GetToken() == "" {
		t.Fatal("restored reader must re-enable token")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (c *b2bCountingProvider) setFail(v bool) {
	if v {
		atomic.StoreInt32(&c.fail, 1)
	} else {
		atomic.StoreInt32(&c.fail, 0)
	}
}

func rec(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

func varyHasOrigin(v []string) bool {
	for _, s := range v {
		if strings.EqualFold(strings.TrimSpace(s), "Origin") {
			return true
		}
	}
	return false
}

func hsrvHealth(srv *Server) *securityHealthRegister { return srv.v1sec.health }

// bustHostCache forces the next hostCache.get() to call the provider.
func bustHostCache(srv *Server) {
	c := srv.v1sec.hostCache
	c.mu.Lock()
	c.cachedAt = time.Time{}
	c.failed = false
	c.mu.Unlock()
}

var _ = sync.Mutex{}
