package remote

// Phase 3 regression tests for the Leader-found gaps (A1-A7). These reproduce
// each gap against the production entry, then assert the fix. Unit doubles
// (countingProvider) verify the production contract only; no fake /ws/v1.

import (
	"context"
	"crypto/rand"
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

// ---------------------------------------------------------------------------
// A1: strict Host gate (effective port == serverPort; reject userinfo/zone/etc)
// ---------------------------------------------------------------------------

func TestPairHostGateMatrix(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	openWindow(t, srv)
	code := lastWindowCode(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	t.Cleanup(ts.Close)
	wireTestPort(srv, ts)
	port := srv.GetPort()

	cases := []struct {
		name string
		host string
		want int
	}{
		{"correct port", mustJoinHostPort("127.0.0.1", port), http.StatusCreated},
		{"wrong port", mustJoinHostPort("127.0.0.1", port+1), http.StatusForbidden},
		{"userinfo", "user@127.0.0.1:" + itoa(port), http.StatusForbidden},
		{"empty host", "", http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"code": code, "deviceName": "phone"})
			req := httptest.NewRequest(http.MethodPost, contract.RESTBasePath+"/pairing/complete", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", ts.URL)
			req.Host = c.host
			rr := httptest.NewRecorder()
			srv.buildV1Handler().ServeHTTP(rr, req)
			// correct-port case redeems the one-time code; re-open for others.
			if rr.Code != c.want {
				t.Fatalf("host=%q: got %d want %d", c.host, rr.Code, c.want)
			}
		})
	}
}

func mustJoinHostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + itoa(port)
	}
	return host + ":" + itoa(port)
}

// lastWindowCode opens a fresh window and returns its code.
func lastWindowCode(t *testing.T, srv *Server) string {
	t.Helper()
	// cancel any prior window then open a fresh one for a deterministic code.
	st, _ := srv.GetPairingWindow()
	if st.Active {
		srv.CancelPairingWindow(st.Generation)
	}
	info, err := srv.CreatePairingWindow(true)
	if err != nil {
		t.Fatal(err)
	}
	return info.Code
}

// ---------------------------------------------------------------------------
// A2: CORS headers echo only on allowlisted origin
// ---------------------------------------------------------------------------

func TestCORSEchoOnlyOnAllowlistedOrigin(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	ts := httptest.NewServer(srv.buildHandler())
	t.Cleanup(ts.Close)
	wireTestPort(srv, ts)

	// Allowlisted origin (wrong code → 401, but CORS headers must still be set).
	srv.CreatePairingWindow(true)
	body, _ := json.Marshal(map[string]string{"code": "BBBBBBBBBBBBBBBBBBBBBBBBBB", "deviceName": "phone"})
	req := httptest.NewRequest(http.MethodPost, contract.RESTBasePath+"/pairing/complete", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	req.Host = strings.TrimPrefix(ts.URL, "http://")
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != ts.URL {
		t.Fatalf("ACAO=%q want %q", got, ts.URL)
	}
	if rr.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("missing ACAC")
	}
	if rr.Header().Get("Access-Control-Expose-Headers") != contract.RequestIDHeader {
		t.Fatal("missing Expose")
	}

	// Disallowed origin: no ACAO echo.
	req2 := httptest.NewRequest(http.MethodPost, contract.RESTBasePath+"/pairing/complete", strings.NewReader(string(body)))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Origin", ts.URL+".evil")
	req2.Host = strings.TrimPrefix(ts.URL, "http://")
	rr2 := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr2, req2)
	if got := rr2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed origin echoed ACAO=%q", got)
	}
}

// ---------------------------------------------------------------------------
// A3: request ID resolved once, header == body
// ---------------------------------------------------------------------------

func TestRequestIDHeaderMatchesBody(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	ts := httptest.NewServer(srv.buildHandler())
	t.Cleanup(ts.Close)
	wireTestPort(srv, ts)

	req := httptest.NewRequest(http.MethodPost, contract.RESTBasePath+"/pairing/complete", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	req.Header.Set(contract.RequestIDHeader, "client-req-id-123")
	req.Host = strings.TrimPrefix(ts.URL, "http://")
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	if got := rr.Header().Get(contract.RequestIDHeader); got != "client-req-id-123" {
		t.Fatalf("header reqID=%q", got)
	}
	var apiErr contract.APIError
	if err := json.Unmarshal(rr.Body.Bytes(), &apiErr); err != nil {
		t.Fatal(err)
	}
	if string(apiErr.RequestID) != "client-req-id-123" {
		t.Fatalf("body reqID=%q want client-req-id-123", apiErr.RequestID)
	}
}

// ---------------------------------------------------------------------------
// A4: HostSummary cache single-flight (one provider call for N concurrent miss)
// ---------------------------------------------------------------------------

type countingProvider struct {
	mu     sync.Mutex
	count  int64
	delay  time.Duration
	result func() (contract.HostSummary, error)
}

func (c *countingProvider) call() (contract.HostSummary, error) {
	atomic.AddInt64(&c.count, 1)
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	return c.result()
}

func TestHostSummaryCacheSingleFlight(t *testing.T) {
	var calls int64
	prov := &countingProvider{
		delay: 50 * time.Millisecond,
		result: func() (contract.HostSummary, error) {
			return validHostSummary()
		},
	}
	cache := newHostSummaryCache(func() (contract.HostSummary, error) { return prov.call() })

	var wg sync.WaitGroup
	const n = 20
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = cache.get()
		}()
	}
	close(start)
	wg.Wait()
	calls = atomic.LoadInt64(&prov.count)
	if calls != 1 {
		t.Fatalf("expected exactly 1 provider call on concurrent miss, got %d", calls)
	}
}

// ---------------------------------------------------------------------------
// A6: Begin preconditions + Start/maintenance race + poison
// ---------------------------------------------------------------------------

func TestBeginRequiresServerStoppedAndPairInactive(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	openWindow(t, srv) // active window
	// Begin must reject while a window is active.
	if _, err := srv.BeginDeviceStoreMaintenance(); err == nil {
		t.Fatal("Begin must reject when pairing window active")
	}
	// Cancel window → Begin succeeds.
	st, _ := srv.GetPairingWindow()
	srv.CancelPairingWindow(st.Generation)
	sess, err := srv.BeginDeviceStoreMaintenance()
	if err != nil {
		t.Fatalf("Begin after cancel: %v", err)
	}
	// Start during active maintenance is rejected + poisons.
	if err := srv.Start(context.Background()); err == nil {
		t.Fatal("Start must be rejected during maintenance")
	}
	if err := srv.AbortDeviceStoreMaintenance(sess); err != nil {
		t.Fatalf("Abort: %v", err)
	}
}

func TestStartAcquiresGatePermit(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(srv.Stop)
	// While running, a maintenance Begin must be rejected (server running).
	if _, err := srv.BeginDeviceStoreMaintenance(); err == nil {
		t.Fatal("Begin must reject while server running")
	}
}

// ---------------------------------------------------------------------------
// A7: BaseURL matrix
// ---------------------------------------------------------------------------

func TestPairingBaseURLMatrix(t *testing.T) {
	cases := []struct {
		host           string
		wantBase       string
		wantAddressReq bool
	}{
		{"", "", true},
		{"0.0.0.0", "", true},
		{"::", "", true},
		{"127.0.0.1", "", true},
		{"localhost", "", true},
		{"192.168.1.8", "http://192.168.1.8:8680", false},
		{"2001:db8::8", "http://[2001:db8::8]:8680", false},
	}
	for _, c := range cases {
		base, addr := buildPairingBaseURL(c.host, 8680)
		if base != c.wantBase || addr != c.wantAddressReq {
			t.Errorf("host=%q: base=%q addr=%v want base=%q addr=%v", c.host, base, addr, c.wantBase, c.wantAddressReq)
		}
	}
}

func TestCreatePairingWindowBaseURL(t *testing.T) {
	dir := t.TempDir()
	clk := newSecFakeClock(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	opts := newSecurityOptions(dir, validHostSummary, clk, rand.Reader, NewVolatileSecurityEventSink())
	srv := NewServerWithSecurity(0, nil, logging.NewService(t.TempDir()), embed.FS{}, opts)
	t.Cleanup(srv.log.Close)
	srv.LoadSecurityState()
	srv.pairing.Resume() // simulate post-publish accepting state (no real Start)

	srv.SetHost("192.168.31.8")
	srv.SetPort(8680)
	info, err := srv.CreatePairingWindow(true)
	if err != nil {
		t.Fatal(err)
	}
	if info.BaseURL != "http://192.168.31.8:8680" {
		t.Fatalf("BaseURL=%q", info.BaseURL)
	}
	if info.AddressRequired {
		t.Fatal("AddressRequired should be false for LAN IP")
	}
	if strings.Contains(info.BaseURL, info.Code) {
		t.Fatal("code leaked into BaseURL")
	}

	srv.SetHost("0.0.0.0")
	srv.interfaceAddrs = func() ([]net.Addr, error) { return nil, nil }
	info2, _ := srv.CreatePairingWindow(true)
	if info2.BaseURL != "" || !info2.AddressRequired {
		t.Fatalf("wildcard: BaseURL=%q AddressRequired=%v", info2.BaseURL, info2.AddressRequired)
	}
}

func TestCreatePairingWindowWildcardAdvertisesReachableLANAddress(t *testing.T) {
	dir := t.TempDir()
	clk := newSecFakeClock(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	opts := newSecurityOptions(dir, validHostSummary, clk, rand.Reader, NewVolatileSecurityEventSink())
	srv := NewServerWithSecurity(0, nil, logging.NewService(t.TempDir()), embed.FS{}, opts)
	t.Cleanup(srv.log.Close)
	srv.LoadSecurityState()
	srv.pairing.Resume()
	srv.SetHost("0.0.0.0")
	srv.SetPort(8680)
	srv.interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("203.0.113.8"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("192.168.31.9"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}

	info, err := srv.CreatePairingWindow(true)
	if err != nil {
		t.Fatal(err)
	}
	if info.BaseURL != "http://192.168.31.9:8680" || info.AddressRequired {
		t.Fatalf("BaseURL=%q AddressRequired=%v", info.BaseURL, info.AddressRequired)
	}
	if strings.Contains(info.BaseURL, info.Code) {
		t.Fatal("code leaked into server-observed BaseURL")
	}
}

// (context.Background() is used directly for Start in tests.)
