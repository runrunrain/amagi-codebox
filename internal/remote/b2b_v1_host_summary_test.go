package remote

// M1-B2b v1 protected host-summary tests (design §B, Leader C-4/C-5). Uses only
// contract symbols for routes/status/errors; no copied endpoint literals except
// negative malformed inputs. Reuses newSecServer/openWindow/pairingRequest/
// wireTestPort from the M1-A v1 test helpers (same package).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// hostSummaryPath is built only from contract symbols (no literal copy).
func hostSummaryPath() string {
	return contract.RESTBasePath + contract.V1RestEndpoints[1].Path
}

// pairAndGetCookie completes a pairing over a real httptest listener and returns
// the device cookie + deviceID + the test server.
func pairAndGetCookie(t *testing.T) (*httptest.Server, *Server, *http.Cookie, string) {
	t.Helper()
	srv, clk := newSecServer(t, validHostSummary)
	_ = clk
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	t.Cleanup(ts.Close)
	wireTestPort(srv, ts)
	req := pairingRequest(ts, code, "phone", ts.URL)
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	if rr.Code != contract.V1RestEndpoints[0].SuccessStatus {
		t.Fatalf("pair: %d", rr.Code)
	}
	var resp contract.PairingCompleteResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(rr.Result().Cookies()) == 0 {
		t.Fatal("no device cookie set")
	}
	return ts, srv, rr.Result().Cookies()[0], string(resp.Device.ID)
}

func hostSummaryReq(ts *httptest.Server, cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, hostSummaryPath(), nil)
	r.Host = strings.TrimPrefix(ts.URL, "http://")
	r.Header.Set("Origin", ts.URL) // same-origin browser proof
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func TestB2BHostSummarySuccessDTOShape(t *testing.T) {
	ts, srv, cookie, _ := pairAndGetCookie(t)
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, hostSummaryReq(ts, cookie))
	if rr.Code != contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("host summary: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	// DTO must carry only apiVersion/serverVersion/cliAvailability.
	body := rr.Body.Bytes()
	for _, bad := range []string{"provider", "token", "apiKey", "basePath", "path", "secret", "env"} {
		if bytes.Contains(bytes.ToLower(body), []byte(bad)) {
			t.Fatalf("host summary body leaked forbidden key %q: %s", bad, body)
		}
	}
	var hs contract.HostSummary
	if err := json.Unmarshal(body, &hs); err != nil {
		t.Fatal(err)
	}
	if hs.APIVersion != contract.APIVersionV1 || hs.ServerVersion == "" || len(hs.CLIAvailability) != len(contract.KnownCLITypes) {
		t.Fatalf("unexpected HostSummary DTO: %+v", hs)
	}
	// ACAO echoed for the accepted same-origin.
	if rr.Header().Get("Access-Control-Allow-Origin") != ts.URL {
		t.Fatal("ACAO not echoed for accepted origin")
	}
}

func TestB2BHostSummaryAuthTable(t *testing.T) {
	ts, srv, cookie, deviceID := pairAndGetCookie(t)
	h := srv.buildV1Handler()

	// missing cookie → 401 auth.unpaired, no clear.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, hostSummaryReq(ts, nil))
	if rr.Code != http.StatusUnauthorized || !bodyHas(rr.Body.Bytes(), "auth.unpaired") {
		t.Fatalf("missing: %d %s", rr.Code, rr.Body.String())
	}

	// malformed cookie → 401 auth.unpaired + clear.
	rm := hostSummaryReq(ts, nil)
	rm.AddCookie(&http.Cookie{Name: deviceCookieName, Value: "garbage"})
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, rm)
	if rr2.Code != http.StatusUnauthorized || !bodyHas(rr2.Body.Bytes(), "auth.unpaired") {
		t.Fatalf("malformed: %d", rr2.Code)
	}
	if !hasClearCookie(rr2) {
		t.Fatal("malformed must clear device cookie")
	}

	// unpaired (well-formed, unknown device) → 401 auth.unpaired + clear.
	id := make([]byte, 16)
	sec := make([]byte, 32)
	unpaired := &http.Cookie{Name: deviceCookieName, Value: deviceCookieValue(rawURLBase64(id), sec)}
	ru := hostSummaryReq(ts, unpaired)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, ru)
	if rr3.Code != http.StatusUnauthorized || !bodyHas(rr3.Body.Bytes(), "auth.unpaired") {
		t.Fatalf("unpaired: %d", rr3.Code)
	}

	// revoked → 401 auth.revoked + clear.
	if _, err := srv.RevokeDevice(deviceID, true); err != nil {
		t.Fatal(err)
	}
	rr4 := httptest.NewRecorder()
	h.ServeHTTP(rr4, hostSummaryReq(ts, cookie))
	if rr4.Code != http.StatusUnauthorized || !bodyHas(rr4.Body.Bytes(), "auth.revoked") {
		t.Fatalf("revoked: %d %s", rr4.Code, rr4.Body.String())
	}
	if !hasClearCookie(rr4) {
		t.Fatal("revoked must clear device cookie")
	}
}

func TestB2BHostSummaryExpired(t *testing.T) {
	srv, clk := newSecServer(t, validHostSummary)
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	t.Cleanup(ts.Close)
	wireTestPort(srv, ts)
	req := pairingRequest(ts, code, "phone", ts.URL)
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	if rr.Code != contract.V1RestEndpoints[0].SuccessStatus {
		t.Fatalf("pair: %d", rr.Code)
	}
	cookie := rr.Result().Cookies()[0]
	// Advance past the 30-day credential TTL → expired.
	clk.Advance(deviceCredentialTTL + time.Hour)
	h := srv.buildV1Handler()
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, hostSummaryReq(ts, cookie))
	if rr2.Code != http.StatusUnauthorized || !bodyHas(rr2.Body.Bytes(), "auth.window_expired") {
		t.Fatalf("expired: %d %s", rr2.Code, rr2.Body.String())
	}
	if !hasClearCookie(rr2) {
		t.Fatal("expired must clear device cookie")
	}
}

func TestB2BOriginProofPrecedence(t *testing.T) {
	ts, srv, cookie, _ := pairAndGetCookie(t)
	h := srv.buildV1Handler()

	// Sec-Fetch-Site: same-origin (no Origin header) → accepted.
	r := hostSummaryReq(ts, cookie)
	r.Header.Del("Origin")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("Sec-Fetch same-origin: %d", rr.Code)
	}

	// Sec-Fetch-Site: cross-site → rejected.
	r2 := hostSummaryReq(ts, cookie)
	r2.Header.Del("Origin")
	r2.Header.Set("Sec-Fetch-Site", "cross-site")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("Sec-Fetch cross-site: %d", rr2.Code)
	}

	// Referer same-origin (no Origin/Sec-Fetch) → accepted.
	r3 := hostSummaryReq(ts, cookie)
	r3.Header.Del("Origin")
	r3.Header.Set("Referer", ts.URL+"/some/path")
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, r3)
	if rr3.Code != contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("Referer same-origin: %d", rr3.Code)
	}

	// Referer cross-origin → rejected.
	r4 := hostSummaryReq(ts, cookie)
	r4.Header.Del("Origin")
	r4.Header.Set("Referer", "http://evil.example.com/")
	rr4 := httptest.NewRecorder()
	h.ServeHTTP(rr4, r4)
	if rr4.Code != http.StatusForbidden {
		t.Fatalf("Referer cross-origin: %d", rr4.Code)
	}

	// No proof at all → rejected (bare curl).
	r5 := hostSummaryReq(ts, cookie)
	r5.Header.Del("Origin")
	rr5 := httptest.NewRecorder()
	h.ServeHTTP(rr5, r5)
	if rr5.Code != http.StatusForbidden {
		t.Fatalf("no proof: %d", rr5.Code)
	}

	// Spoofed X-Forwarded does not help a cross-origin Origin.
	r6 := hostSummaryReq(ts, cookie)
	r6.Header.Set("Origin", "http://evil.example.com")
	r6.Header.Set("X-Forwarded-Host", strings.TrimPrefix(ts.URL, "http://"))
	rr6 := httptest.NewRecorder()
	h.ServeHTTP(rr6, r6)
	if rr6.Code != http.StatusForbidden {
		t.Fatalf("spoofed XFF+bad Origin: %d", rr6.Code)
	}
}

func TestB2BCORSPreflightMatrix(t *testing.T) {
	ts, srv, _, _ := pairAndGetCookie(t)
	h := srv.buildV1Handler()

	// Valid preflight for host summary → 204 + Allow-Methods manifest+OPTIONS.
	r := httptest.NewRequest(http.MethodOptions, hostSummaryPath(), nil)
	r.Host = strings.TrimPrefix(ts.URL, "http://")
	r.Header.Set("Origin", ts.URL)
	r.Header.Set("Access-Control-Request-Method", contract.V1RestEndpoints[1].Method)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("preflight: %d", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Access-Control-Allow-Methods"), contract.V1RestEndpoints[1].Method) ||
		!strings.Contains(rr.Header().Get("Access-Control-Allow-Methods"), http.MethodOptions) {
		t.Fatalf("Allow-Methods wrong: %s", rr.Header().Get("Access-Control-Allow-Methods"))
	}
	if rr.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("preflight missing Credentials=true")
	}

	// Preflight with wrong ACRM → rejected.
	r2 := httptest.NewRequest(http.MethodOptions, hostSummaryPath(), nil)
	r2.Host = strings.TrimPrefix(ts.URL, "http://")
	r2.Header.Set("Origin", ts.URL)
	r2.Header.Set("Access-Control-Request-Method", http.MethodDelete)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, r2)
	if rr2.Code == http.StatusNoContent {
		t.Fatal("preflight wrong ACRM must be rejected")
	}

	// Preflight with disallowed Origin → rejected (no wildcard).
	r3 := httptest.NewRequest(http.MethodOptions, hostSummaryPath(), nil)
	r3.Host = strings.TrimPrefix(ts.URL, "http://")
	r3.Header.Set("Origin", "http://evil.example.com")
	r3.Header.Set("Access-Control-Request-Method", contract.V1RestEndpoints[1].Method)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, r3)
	if rr3.Code == http.StatusNoContent || rr3.Header().Get("Access-Control-Allow-Origin") == "*" {
		t.Fatal("disallowed origin preflight must be rejected, no wildcard")
	}
}

func TestB2BDynamicPortRegression(t *testing.T) {
	ts, srv, cookie, _ := pairAndGetCookie(t)
	_ = ts
	srv.SetPort(9001) // dynamic; not the cached v1sec.serverPort
	h := srv.buildV1Handler()

	// Host:9001 + matching Origin accepted.
	r := httptest.NewRequest(http.MethodGet, hostSummaryPath(), nil)
	r.Host = "127.0.0.1:9001"
	r.Header.Set("Origin", "http://127.0.0.1:9001")
	r.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("port 9001 accepted: %d %s", rr.Code, rr.Body.String())
	}
	// Stale Host:8680 rejected by the Host gate (port != dynamic 9001).
	r2 := httptest.NewRequest(http.MethodGet, hostSummaryPath(), nil)
	r2.Host = "127.0.0.1:8680"
	r2.Header.Set("Origin", "http://127.0.0.1:8680")
	r2.AddCookie(cookie)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("stale port 8680 must be rejected: %d", rr2.Code)
	}
}

func TestB2BGatesRejectBeforeProviderAuth(t *testing.T) {
	ts, srv, cookie, _ := pairAndGetCookie(t)
	h := srv.buildV1Handler()

	// Non-empty query → 400 (before auth/handler). Provider/auth spies: the host
	// summary has no provider call, but a valid cookie must NOT reach the handler.
	r := hostSummaryReq(ts, cookie)
	r.URL.RawQuery = "x=1"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("query must be 400: %d", rr.Code)
	}

	// Unknown v1 route → 404.
	r2 := httptest.NewRequest(http.MethodGet, contract.RESTBasePath+"/sessions", nil)
	r2.Host = strings.TrimPrefix(ts.URL, "http://")
	r2.Header.Set("Origin", ts.URL)
	r2.AddCookie(cookie)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("unknown route must be 404: %d", rr2.Code)
	}

	// Wrong method on a KNOWN host summary path → 405 method-not-allowed (Major-04):
	// path is classified known before the method gate, so a wrong method returns
	// 405 (not 404) with a contract bad_request body + Allow header.
	r3 := httptest.NewRequest(http.MethodPost, hostSummaryPath(), nil)
	r3.Host = strings.TrimPrefix(ts.URL, "http://")
	r3.Header.Set("Origin", ts.URL)
	r3.AddCookie(cookie)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, r3)
	if rr3.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method on known path must be 405: %d", rr3.Code)
	}
	if !bodyHas(rr3.Body.Bytes(), "request method rejected") {
		t.Fatalf("405 body missing method-rejected message: %s", rr3.Body.String())
	}
	if allow := rr3.Result().Header.Get("Allow"); !strings.Contains(allow, http.MethodGet) || !strings.Contains(allow, http.MethodOptions) {
		t.Fatalf("405 Allow header must list GET+OPTIONS: %q", allow)
	}
}

// bodyHas reports whether the JSON body contains the given substring.
func bodyHas(b []byte, sub string) bool { return bytes.Contains(b, []byte(sub)) }

// hasClearCookie reports whether the response carries a device-cookie deletion.
func hasClearCookie(rr *httptest.ResponseRecorder) bool {
	for _, c := range rr.Result().Cookies() {
		if c.Name == deviceCookieName && c.MaxAge < 0 {
			return true
		}
	}
	return false
}
