package remote

// routes_v1_dispatcher_test.go — Major-04: the v1 dispatcher classifies the
// path FIRST (known vs unknown), then runs the gates in the frozen order
// Host → query → method → origin → auth. A KNOWN path with the wrong method
// returns 405 (contract bad_request body + Allow header); an UNKNOWN path
// stays 404 regardless of method/Host/query. Gate precedence is observable:
// Host/query failures beat the method gate, so a wrong-method request with a
// bad Host is 403, not 405. A provider spy proves the handler is never reached.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// dispReq builds a v1 request to path with method/Host/Origin/cookie wired like
// the production browser proof, plus optional query and Host overrides.
func dispReq(ts *httptest.Server, cookie *http.Cookie, method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Host = strings.TrimPrefix(ts.URL, "http://")
	r.Header.Set("Origin", ts.URL)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

// TestDispatcher_KnownPathWrongMethod_405: POST to /host/summary (a known GET
// path) returns 405 with a contract bad_request body and an Allow header listing
// GET+OPTIONS — not 404.
func TestDispatcher_KnownPathWrongMethod_405(t *testing.T) {
	ts, srv, _, cookie, _ := pairWithProvider(t, &b2bCountingProvider{})
	h := srv.buildV1Handler()

	rr := rec(h, dispReq(ts, cookie, http.MethodPost, hostSummaryPath()))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("known path + wrong method: status=%d want 405", rr.Code)
	}
	if !bodyHas(rr.Body.Bytes(), "request method rejected") {
		t.Fatalf("405 body: %s", rr.Body.String())
	}
	if !bodyHas(rr.Body.Bytes(), string(contract.ErrorCodeBadRequest)) {
		t.Fatalf("405 body must carry the bad_request code: %s", rr.Body.String())
	}
	allow := rr.Result().Header.Get("Allow")
	if !strings.Contains(allow, http.MethodGet) || !strings.Contains(allow, http.MethodOptions) {
		t.Fatalf("Allow header must list GET+OPTIONS: %q", allow)
	}
}

// TestDispatcher_UnknownPath_404: an unknown /v1 path returns 404 for any method,
// including the correct GET method and a wrong method.
func TestDispatcher_UnknownPath_404(t *testing.T) {
	ts, srv, _, cookie, _ := pairWithProvider(t, &b2bCountingProvider{})
	h := srv.buildV1Handler()

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		rr := rec(h, dispReq(ts, cookie, method, contract.RESTBasePath+"/nope"))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("unknown path method=%s: status=%d want 404", method, rr.Code)
		}
	}
}

// TestDispatcher_GatePrecedence: Host/query gates run BEFORE the method gate, so
// a wrong-method request with a bad Host is 403 (not 405), and with a non-empty
// query is 400 (not 405). A clean wrong-method request is 405.
func TestDispatcher_GatePrecedence(t *testing.T) {
	ts, srv, _, cookie, _ := pairWithProvider(t, &b2bCountingProvider{})
	h := srv.buildV1Handler()

	// Bad Host + wrong method → Host gate wins → 403.
	r := dispReq(ts, cookie, http.MethodPost, hostSummaryPath())
	r.Host = "127.0.0.1:1"
	rr := rec(h, r)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bad Host + wrong method: status=%d want 403 (Host gate before method)", rr.Code)
	}

	// Non-empty query + wrong method → query gate wins → 400.
	rq := dispReq(ts, cookie, http.MethodPost, hostSummaryPath())
	rq.URL.RawQuery = "x=1"
	rrq := rec(h, rq)
	if rrq.Code != http.StatusBadRequest {
		t.Fatalf("query + wrong method: status=%d want 400 (query gate before method)", rrq.Code)
	}

	// Clean wrong method → 405.
	rrm := rec(h, dispReq(ts, cookie, http.MethodPost, hostSummaryPath()))
	if rrm.Code != http.StatusMethodNotAllowed {
		t.Fatalf("clean wrong method: status=%d want 405", rrm.Code)
	}
}

// TestDispatcher_WrongMethodAndUnknown_NeverTouchProvider: a provider spy proves
// the handler (and thus the HostSummary provider/cache) is never reached for a
// wrong method, an unknown path, a bad Host, or a bad query. The 405/404/403/400
// responses are produced entirely by the dispatcher gates.
func TestDispatcher_WrongMethodAndUnknown_NeverTouchProvider(t *testing.T) {
	cp := &b2bCountingProvider{}
	ts, srv, _, cookie, _ := pairWithProvider(t, cp)
	h := srv.buildV1Handler()
	bustHostCache(srv)
	base := cp.count()

	// Wrong method on known path → 405, no provider call.
	rec(h, dispReq(ts, cookie, http.MethodPost, hostSummaryPath()))
	// Unknown path → 404, no provider call.
	rec(h, dispReq(ts, cookie, http.MethodGet, contract.RESTBasePath+"/nope"))
	// Bad Host + wrong method → 403, no provider call.
	rh := dispReq(ts, cookie, http.MethodPost, hostSummaryPath())
	rh.Host = "127.0.0.1:1"
	rec(h, rh)
	// Bad query + wrong method → 400, no provider call.
	rq := dispReq(ts, cookie, http.MethodPost, hostSummaryPath())
	rq.URL.RawQuery = "x=1"
	rec(h, rq)

	if got := cp.count() - base; got != 0 {
		t.Fatalf("dispatcher gates must not touch the provider: %d provider calls observed", got)
	}

	// Sanity: a valid GET host/summary DOES reach the provider (proves the spy
	// is wired and the handler is reachable only on a full match).
	bustHostCache(srv)
	rec(h, hsReq(ts, cookie))
	if got := cp.count() - base; got != 1 {
		t.Fatalf("valid request must call the provider exactly once: got %d", got)
	}
}
