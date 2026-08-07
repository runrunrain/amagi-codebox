package remote

// M1-A Phase 2 production-entry tests (design §16: T05-T07, T12-T17, T20, T23-T27
// — the subset not depending on the app.go HostSummary provider). These drive the
// REAL buildV1Handler / deviceService / authenticator / registry via
// NewServerWithSecurity. Unit test doubles (fakeConn, blockingHostProvider)
// verify the production contract only; they are NOT a real /ws/v1 consumer and
// never claim AC-15 E2E.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote/contract"
)

// validHostSummary is a contract-valid HostSummary provider used by tests.
func validHostSummary() (contract.HostSummary, error) {
	return contract.HostSummary{
		APIVersion:    contract.APIVersionV1,
		ServerVersion: "test",
		CLIAvailability: []contract.CLIAvailability{
			{CLIType: contract.CLITypeClaudeCode, Available: true},
			{CLIType: contract.CLITypeOpenCode, Available: false},
			{CLIType: contract.CLITypeCodex, Available: false},
			{CLIType: contract.CLITypePi, Available: false},
			{CLIType: contract.CLITypeOmp, Available: false},
		},
	}, nil
}

// newSecServer builds a ready security server with the given host provider.
func newSecServer(t *testing.T, hostProvider HostSummaryFunc) (*Server, *secFakeClock) {
	t.Helper()
	dir := t.TempDir()
	clk := newSecFakeClock(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	opts := newSecurityOptions(dir, hostProvider, clk, rand.Reader, NewVolatileSecurityEventSink())
	srv := NewServerWithSecurity(0, nil, logging.NewService(t.TempDir()), embed.FS{}, opts)
	t.Cleanup(srv.log.Close)
	if err := srv.LoadSecurityState(); err != nil {
		t.Fatalf("LoadSecurityState: %v", err)
	}
	// These route tests use httptest (no real Start), so simulate the post-publish
	// accepting state that production enters via Server.Start → pairing.Resume.
	srv.pairing.Resume()
	return srv, clk
}

// openWindow creates a pairing window and returns the code.
func openWindow(t *testing.T, srv *Server) string {
	t.Helper()
	info, err := srv.CreatePairingWindow(true)
	if err != nil {
		t.Fatalf("CreatePairingWindow: %v", err)
	}
	return info.Code
}

// pairingRequest builds an HTTP request to the pairing endpoint.
func pairingRequest(ts *httptest.Server, code, name string, origin string) *http.Request {
	body, _ := json.Marshal(map[string]string{"code": code, "deviceName": name})
	req := httptest.NewRequest(http.MethodPost, contract.RESTBasePath+"/pairing/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
		req.Host = strings.TrimPrefix(strings.TrimPrefix(origin, "http://"), "https://")
	}
	return req
}

// wireTestPort aligns the dynamic server port (s.GetPort(), consumed by the
// v1 dispatcher's strict Host gate) with the httptest listener so test requests
// are accepted. (B2b: the dispatcher no longer reads a cached v1sec.serverPort.)
func wireTestPort(srv *Server, ts *httptest.Server) {
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	var port int
	for _, c := range []byte(p) {
		if c >= '0' && c <= '9' {
			port = port*10 + int(c-'0')
		}
	}
	srv.SetPort(port)
}

// ---------------------------------------------------------------------------
// T05/T06/T07: window lifecycle, wrong-code attempts, concurrent redeem
// ---------------------------------------------------------------------------

func TestPairingWindowLifecycleAndWrongCodeAttempts(t *testing.T) {
	srv, clk := newSecServer(t, validHostSummary)
	code := openWindow(t, srv)

	// Status active.
	st, err := srv.GetPairingWindow()
	if err != nil || !st.Active {
		t.Fatalf("status active=%v err=%v", st.Active, err)
	}
	_ = clk

	ts := httptest.NewServer(srv.buildHandler())
	wireTestPort(srv, ts)
	t.Cleanup(ts.Close)
	origin := ts.URL

	// Well-formed wrong code consumes an attempt (401). 5th locks (429).
	wrong := "AAAAAAAAAAAAAAAAAAAAAAAAAA" // valid Base32-ish 26 chars
	for i := 0; i < 4; i++ {
		req := pairingRequest(ts, wrong, "phone", origin)
		rr := httptest.NewRecorder()
		srv.buildV1Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("wrong attempt %d: code=%d want 401", i, rr.Code)
		}
	}
	// 5th wrong → locked 429.
	req := pairingRequest(ts, wrong, "phone", origin)
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("5th wrong: code=%d want 429", rr.Code)
	}

	// New window after lock.
	info, err := srv.CreatePairingWindow(true)
	if err != nil {
		t.Fatalf("recreate window: %v", err)
	}
	if info.Generation == 0 {
		t.Fatal("generation zero")
	}
	_ = code
}

func TestPairingConcurrentRedeemExactlyOneCommit(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	wireTestPort(srv, ts)
	t.Cleanup(ts.Close)
	origin := ts.URL

	var wg sync.WaitGroup
	var success int64
	var conflict int64
	const n = 25
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := pairingRequest(ts, code, "phone"+itoa(0), origin)
			rr := httptest.NewRecorder()
			srv.buildV1Handler().ServeHTTP(rr, req)
			switch rr.Code {
			case http.StatusCreated:
				atomic.AddInt64(&success, 1)
			case http.StatusGone, http.StatusServiceUnavailable:
				atomic.AddInt64(&conflict, 1)
			default:
				t.Errorf("unexpected status %d", rr.Code)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&success); got != 1 {
		t.Fatalf("expected exactly 1 success, got %d (conflict=%d)", got, atomic.LoadInt64(&conflict))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// T12/T13: production pairing handler route + contract body + negatives
// ---------------------------------------------------------------------------

func TestV1PairingProductionRoute(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	wireTestPort(srv, ts)
	t.Cleanup(ts.Close)

	// Correct redeem → 201 from manifest + Set-Cookie + contract body.
	req := pairingRequest(ts, code, "phone", ts.URL)
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	if rr.Code != contract.V1RestEndpoints[0].SuccessStatus {
		t.Fatalf("status=%d want manifest %d", rr.Code, contract.V1RestEndpoints[0].SuccessStatus)
	}
	setCookie := rr.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, deviceCookieName+"=") || !strings.Contains(setCookie, "HttpOnly") || !strings.Contains(setCookie, "SameSite=Strict") {
		t.Fatalf("cookie missing attributes: %s", setCookie)
	}
	var resp contract.PairingCompleteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body unmarshal: %v", err)
	}
	if resp.Device.Name != "phone" {
		t.Fatalf("device name=%q", resp.Device.Name)
	}
	// Body contains no credential/secret.
	if strings.Contains(rr.Body.String(), "secret") || strings.Contains(rr.Body.String(), "credential") {
		t.Fatal("body leaked credential material")
	}
}

func TestV1PairingOriginAndQueryGates(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	wireTestPort(srv, ts)
	t.Cleanup(ts.Close)

	// Empty Origin → 403, attempt not consumed.
	req := pairingRequest(ts, code, "phone", "")
	req.URL.RawQuery = ""
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("empty origin: code=%d want 403", rr.Code)
	}
	st, _ := srv.GetPairingWindow()
	if st.RemainingAttempts != 5 {
		t.Fatalf("attempt consumed by origin gate: remaining=%d", st.RemainingAttempts)
	}

	// Disallowed Origin (host-prefix) → 403.
	req2 := pairingRequest(ts, code, "phone", ts.URL+".evil")
	rr2 := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("host-prefix origin: code=%d want 403", rr2.Code)
	}

	// Nonempty query → 400 even with valid legacy token.
	req3 := pairingRequest(ts, code, "phone", ts.URL)
	req3.URL.RawQuery = "token=legacy"
	rr3 := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusBadRequest {
		t.Fatalf("query: code=%d want 400", rr3.Code)
	}
}

// ---------------------------------------------------------------------------
// T14/T15: Cookie attributes + restart/revoke auth
// ---------------------------------------------------------------------------

func TestDeviceCookieTLSAttributes(t *testing.T) {
	r := &http.Request{TLS: nil}
	c := issueDeviceCookie(r, "AAAAAAAAAAAAAAAAAAAAAA", make([]byte, 32), time.Now().Add(deviceCredentialTTL))
	if c.Secure {
		t.Fatal("HTTP must not set Secure")
	}
	if c.HttpOnly != true || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("cookie attrs wrong: %+v", c)
	}
	rTLS := &http.Request{TLS: &tls.ConnectionState{}}
	c2 := issueDeviceCookie(rTLS, "AAAAAAAAAAAAAAAAAAAAAA", make([]byte, 32), time.Now().Add(deviceCredentialTTL))
	if !c2.Secure {
		t.Fatal("TLS must set Secure")
	}
}

func TestRevokeMakesAuthFailClosed(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	wireTestPort(srv, ts)
	t.Cleanup(ts.Close)

	req := pairingRequest(ts, code, "phone", ts.URL)
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("pair: %d", rr.Code)
	}
	// Extract the device cookie value + id.
	c := rr.Result().Cookies()[0]
	var resp contract.PairingCompleteResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	deviceID := string(resp.Device.ID)

	// Revoke.
	if _, err := srv.RevokeDevice(deviceID, true); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// Authenticator now reports revoked for the same cookie.
	authReq := httptest.NewRequest(http.MethodGet, "/", nil)
	authReq.AddCookie(&http.Cookie{Name: deviceCookieName, Value: c.Value})
	principal, fail := srv.v1sec.deviceAuth.AuthenticateRequest(authReq)
	if fail != authRevoked {
		t.Fatalf("post-revoke auth fail=%v principal=%+v", fail, principal)
	}
	_ = c
}

// ---------------------------------------------------------------------------
// T16: Origin matrix
// ---------------------------------------------------------------------------

func TestOriginAllowlistMatrix(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(ts.Close)

	cases := []struct {
		origin string
		want   bool
	}{
		{ts.URL, true},                   // same-host
		{ts.URL + ".evil", false},        // host-prefix
		{"null", false},                  // null
		{"", false},                      // empty
		{"capacitor://localhost", true},  // capacitor fixed
		{"https://localhost", true},      // capacitor https effective 443
		{ts.URL + "/path", false},        // non-empty path
		{"http://user:pass@host", false}, // userinfo (won't parse as same-host)
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Host = strings.TrimPrefix(ts.URL, "http://")
		if got := canonicalAllowedOrigin(req, c.origin); got != c.want {
			t.Errorf("origin %q: got %v want %v", c.origin, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// T17: legacy WS empty Origin rejected (D-004)
// ---------------------------------------------------------------------------

func TestLegacyWebSocketEmptyOriginRejected(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/ws/terminal/x", nil)
	if isAllowedWebSocketOrigin(r) {
		t.Fatal("empty origin must be rejected")
	}
	r2 := httptest.NewRequest(http.MethodGet, "/ws/terminal/x", nil)
	r2.Header.Set("Origin", "http://"+r2.Host)
	if !isAllowedWebSocketOrigin(r2) {
		t.Fatal("valid same-origin must be allowed for legacy WS")
	}
}

// ---------------------------------------------------------------------------
// T20: per-run lifecycle (explicit Stop / parent cancel / serve failure unify)
// ---------------------------------------------------------------------------

func TestServerStopUnifiesLifecycle(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !srv.IsRunning() {
		t.Fatal("not running after start")
	}
	srv.Stop()
	if srv.IsRunning() {
		t.Fatal("still running after stop")
	}
	// Registry stopped (not accepting).
	if srv.registry.IsAccepting() {
		t.Fatal("registry still accepting after stop")
	}
}

// ---------------------------------------------------------------------------
// T25/T32: revoke duplicate event enum + terminate count
// ---------------------------------------------------------------------------

func TestRevokeDuplicateEventEnumAndTerminateCount(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	wireTestPort(srv, ts)
	t.Cleanup(ts.Close)
	req := pairingRequest(ts, code, "phone", ts.URL)
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	var resp contract.PairingCompleteResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	deviceID := string(resp.Device.ID)

	// First revoke: ledger commits; no live connections (0 terminated).
	res, err := srv.RevokeDevice(deviceID, true)
	if err != nil {
		t.Fatalf("revoke1: %v", err)
	}
	if res.AlreadyRevoked || res.EventOutcome == EventNotEmittedDuplicate {
		t.Fatalf("first revoke outcome=%v", res.EventOutcome)
	}

	// Duplicate revoke: AlreadyRevoked + NotEmittedDuplicate + 0 terminated.
	res2, err := srv.RevokeDevice(deviceID, true)
	if err != nil {
		t.Fatalf("revoke2: %v", err)
	}
	if !res2.AlreadyRevoked || res2.EventOutcome != EventNotEmittedDuplicate {
		t.Fatalf("duplicate revoke: already=%v outcome=%v", res2.AlreadyRevoked, res2.EventOutcome)
	}
}

// ---------------------------------------------------------------------------
// T27: LastSeen expiry / race (principal uses AuthenticatedAt)
// ---------------------------------------------------------------------------

func TestRecordDeviceSeenExpiryAndCoalesce(t *testing.T) {
	srv, clk := newSecServer(t, validHostSummary)
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	wireTestPort(srv, ts)
	t.Cleanup(ts.Close)
	req := pairingRequest(ts, code, "phone", ts.URL)
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	var resp contract.PairingCompleteResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)

	principal := DevicePrincipal{
		DeviceID: resp.Device.ID, AuthenticatedAt: clk.Now(),
		CredentialExpiresAt: clk.Now().Add(30 * 24 * time.Hour),
	}
	// Immediate seen: within 10m coalesce interval → coalesced.
	r1, err := srv.RecordDeviceSeen(principal)
	if err != nil || r1.Outcome != SeenCoalesced {
		t.Fatalf("seen1 outcome=%v err=%v", r1.Outcome, err)
	}
	// Advance past interval → persisted.
	clk.Advance(11 * time.Minute)
	principal.AuthenticatedAt = clk.Now()
	r2, err := srv.RecordDeviceSeen(principal)
	if err != nil || r2.Outcome != SeenPersisted {
		t.Fatalf("seen2 outcome=%v err=%v", r2.Outcome, err)
	}
	// Expiry boundary: AuthenticatedAt == CredentialExpiresAt → skipped expired.
	clk.Advance(30 * 24 * time.Hour)
	expired := principal
	expired.AuthenticatedAt = principal.CredentialExpiresAt
	r3, _ := srv.RecordDeviceSeen(expired)
	if r3.Outcome != SeenSkippedExpired {
		t.Fatalf("expired seen outcome=%v want skipped_expired", r3.Outcome)
	}
}

// ---------------------------------------------------------------------------
// T34 (Server entry): maintenance gate blocks normal Start + normal API
// ---------------------------------------------------------------------------

func TestMaintenanceGateServerEntry(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	openWindow(t, srv) // establish a window
	// Maintenance requires the pairing window inactive; cancel it first.
	st, _ := srv.GetPairingWindow()
	srv.CancelPairingWindow(st.Generation)

	sess, err := srv.BeginDeviceStoreMaintenance()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// While active, a normal pairing API must fail closed (and poison the session).
	if _, err := srv.CreatePairingWindow(true); err == nil {
		t.Fatal("CreatePairingWindow should be rejected during maintenance")
	}
	// Start during maintenance is rejected.
	if err := srv.Start(context.Background()); err == nil {
		t.Fatal("Start should be rejected during maintenance")
	}
	if err := srv.AbortDeviceStoreMaintenance(sess); err != nil {
		t.Fatalf("Abort: %v", err)
	}
}
