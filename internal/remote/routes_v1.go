package remote

// V1 route spine (design §6.7/§12). The router is built ONLY from
// contract.V1RestEndpoints / RESTBasePath / DTO / Decode / Validate / Marshal /
// contract error symbols — no copied method/path/status/error strings. M1-A
// registers exactly V1RestEndpoints[0] (pairing/complete). A pair event sink
// failure still yields 201 with the failure visible in SecurityHealth; Cookie /
// network write failures are reported honestly (the commit is not rolled back).

import (
	"bytes"
	"crypto/rand"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// cryptoRandRead is a seam over crypto/rand.Read (production default).
func cryptoRandRead(b []byte) (int, error) { return rand.Read(b) }

// v1RouteSpec describes one active v1 route (design §6.7). The handler receives
// the once-resolved request ID, the CORS-allowed flag, and the extracted
// sessionID (empty for routes without {id}) (no re-resolution).
type v1RouteSpec struct {
	endpointIndex int // indexes contract.V1RestEndpoints
	auth          v1AuthPolicy
	origin        v1OriginPolicy
	handler       func(http.ResponseWriter, *http.Request, contract.RestEndpoint, contract.RequestID, bool, v1Principal, contract.SessionID)
}

// pairingBodyCap bounds the pairing request body (design §4.1: 4 KiB).
const pairingBodyCap = 4 << 10

// hostSummaryCache is the 5s-success / 1s-failure cache over the real
// HostSummaryFunc. It is single-flight: on a cache miss only ONE provider call
// is in flight; concurrent callers wait on inflight and share the resulting
// snapshot/error. It caches the FULL four-CLI snapshot, never a raw error.
type hostSummaryCache struct {
	mu         sync.Mutex
	provider   HostSummaryFunc
	successTTL time.Duration
	failureTTL time.Duration
	cached     contract.HostSummary
	cachedAt   time.Time
	failed     bool
	failUntil  time.Time
	inflight   chan struct{} // non-nil while a provider call is in flight
	result     contract.HostSummary
	err        error
}

func newHostSummaryCache(provider HostSummaryFunc) *hostSummaryCache {
	return &hostSummaryCache{provider: provider, successTTL: 5 * time.Second, failureTTL: 1 * time.Second}
}

// get returns a validated HostSummary or a closed error. Concurrent misses
// share a single provider call (single-flight) without holding the lock across
// the external call.
func (c *hostSummaryCache) get() (contract.HostSummary, error) {
	c.mu.Lock()
	now := time.Now()
	if !c.cachedAt.IsZero() && now.Sub(c.cachedAt) < c.successTTL {
		hs := c.cached
		c.mu.Unlock()
		return hs, nil
	}
	if c.failed && now.Before(c.failUntil) {
		c.mu.Unlock()
		return contract.HostSummary{}, errHostSummaryUnavailable
	}
	// Single-flight: if a provider call is in flight, wait on its channel.
	if c.inflight != nil {
		ch := c.inflight
		c.mu.Unlock()
		<-ch
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.err == nil {
			return c.result, nil
		}
		return contract.HostSummary{}, errHostSummaryUnavailable
	}
	// Start the single in-flight call.
	c.inflight = make(chan struct{})
	c.mu.Unlock()

	hs, err := c.provider()
	ok := err == nil
	if ok {
		if verr := contract.ValidateRESTResponse(hs); verr != nil {
			ok = false
		}
	}

	c.mu.Lock()
	if ok {
		c.cached = hs
		c.cachedAt = time.Now()
		c.failed = false
		c.result = hs
		c.err = nil
	} else {
		c.failed = true
		c.failUntil = time.Now().Add(c.failureTTL)
		c.cachedAt = time.Time{}
		c.result = contract.HostSummary{}
		c.err = errHostSummaryUnavailable
	}
	close(c.inflight)
	c.inflight = nil
	res := c.result
	resErr := c.err
	c.mu.Unlock()
	if resErr == nil {
		return res, nil
	}
	return contract.HostSummary{}, errHostSummaryUnavailable
}

var errHostSummaryUnavailable = closedTextError("security state unavailable")

// v1Security is the Server-held security surface consumed by the v1 routes.
type v1Security struct {
	pairing    *deviceService
	deviceAuth *deviceAuthenticator
	store      *fileDeviceStore
	health     *securityHealthRegister
	hostCache  *hostSummaryCache
}

// registerV1Routes returns the active v1 route specs. M2-A activates
// indices 0-9 (all v1 endpoints) when sessionAdapter is non-nil; otherwise
// only index 0 (pairing/complete) + index 1 (host/summary) are active and the
// session routes stay 404 (design §4A hardening gate). Manifest bounds asserted.
func (s *Server) registerV1Routes() []v1RouteSpec {
	if len(contract.V1RestEndpoints) < 2 {
		panic("security: V1RestEndpoints manifest has fewer than 2 entries")
	}
	specs := []v1RouteSpec{
		{
			endpointIndex: 0,
			auth:          publicPairing,
			origin:        unsafeOriginRequired,
			handler:       s.handleV1PairingComplete,
		},
		{
			endpointIndex: 1,
			auth:          deviceCookie,
			origin:        safeBrowserProof,
			handler:       s.handleV1HostSummary,
		},
	}
	// M2-A session routes: activate only when the adapter is wired (design §4A).
	if s.sessionAdapter != nil {
		specs = append(specs,
			v1RouteSpec{endpointIndex: 2, auth: deviceCookie, origin: safeBrowserProof, handler: s.handleV1SessionsList},
			v1RouteSpec{endpointIndex: 3, auth: deviceCookie, origin: safeBrowserProof, handler: s.handleV1SessionDetail},
			v1RouteSpec{endpointIndex: 4, auth: deviceCookie, origin: unsafeOriginRequired, handler: s.handleV1SessionCreate},
			v1RouteSpec{endpointIndex: 5, auth: deviceCookie, origin: unsafeOriginRequired, handler: s.handleV1SessionStop},
			v1RouteSpec{endpointIndex: 6, auth: deviceCookie, origin: unsafeOriginRequired, handler: s.handleV1SessionRestart},
			v1RouteSpec{endpointIndex: 7, auth: deviceCookie, origin: unsafeOriginRequired, handler: s.handleV1SessionRemove},
			v1RouteSpec{endpointIndex: 8, auth: deviceCookie, origin: unsafeOriginRequired, handler: s.handleV1ControlAcquire},
			v1RouteSpec{endpointIndex: 9, auth: deviceCookie, origin: unsafeOriginRequired, handler: s.handleV1ControlRelease},
		)
	}
	return specs
}

// v1Principal is the authenticated principal passed to a handler (zero-value
// for the public pairing route).
type v1Principal = DevicePrincipal

// buildV1Handler builds the v1 dispatcher. It resolves the request ID ONCE,
// computes CORS once, then centrally enforces — in fixed order — strict Host
// (dynamic s.GetPort(), no stale serverPort), empty RawQuery, method/path spec
// match, origin policy, auth policy, then the handler. OPTIONS resolves by path
// (Host/empty-query/exact-Origin/ACRM==manifest method, no Cookie auth). The
// dispatcher never calls legacy Auth and never re-resolves the request ID.
func (s *Server) buildV1Handler() http.Handler {
	specs := s.registerV1Routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SECURITY HEADERS (set early).
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")

		reqID, idOK := resolveRequestID(r)
		if !idOK {
			reqID = fallbackRequestID
			w.Header().Set(contract.RequestIDHeader, string(reqID))
			writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
				contract.ErrorLayerConnection, "security state unavailable", contract.ActionHintCheckDesktop)
			return
		}
		w.Header().Set(contract.RequestIDHeader, string(reqID))

		if s.v1sec == nil {
			writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
				contract.ErrorLayerConnection, "security state unavailable", contract.ActionHintCheckDesktop)
			return
		}

		// CORS: echo the canonical raw Origin only when it is a real accepted
		// origin; Vary: Origin; Credentials=true; never a wildcard. Set early so
		// every success/error response is readable.
		originVals := r.Header.Values("Origin")
		corsAllowed := false
		if len(originVals) == 1 {
			ov := strings.TrimSpace(originVals[0])
			if ov != "" && canonicalAllowedOrigin(r, ov) {
				corsAllowed = true
				w.Header().Set("Access-Control-Allow-Origin", ov)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Expose-Headers", contract.RequestIDHeader)
				w.Header().Add("Vary", "Origin")
			}
		}

		port := s.GetPort() // dynamic; not a cached serverPort

		// OPTIONS preflight: resolve by path; Host / empty-query / exact-Origin /
		// ACRM==manifest method; no Cookie auth.
		if r.Method == http.MethodOptions {
			m := v1SpecByPath(specs, r.URL.Path)
			if m == nil {
				writeV1Error(w, reqID, http.StatusNotFound, contract.ErrorCodeBadRequest,
					contract.ErrorLayerConnection, "remote endpoint not available", contract.ActionHintRetry)
				return
			}
			if !strictHostValid(r, port) {
				writeV1Error(w, reqID, http.StatusForbidden, contract.ErrorCodeBadRequest,
					contract.ErrorLayerAuth, "request host rejected", contract.ActionHintCheckDesktop)
				return
			}
			if r.URL.RawQuery != "" {
				writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
					contract.ErrorLayerConnection, "query parameters are not allowed", contract.ActionHintRetry)
				return
			}
			if !corsAllowed {
				writeV1Error(w, reqID, http.StatusForbidden, contract.ErrorCodeBadRequest,
					contract.ErrorLayerAuth, "request origin rejected", contract.ActionHintCheckDesktop)
				return
			}
			ep := contract.V1RestEndpoints[m.spec.endpointIndex]
			if r.Header.Get("Access-Control-Request-Method") != ep.Method {
				writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
					contract.ErrorLayerConnection, "request method rejected", contract.ActionHintRetry)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", ep.Method+", "+http.MethodOptions)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+contract.RequestIDHeader)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Path classification FIRST (Major-04): distinguish known vs unknown path
		// before any method/gate work, so a known path with a wrong method returns
		// 405 while an unknown path stays 404 regardless of method/gates.
		m := v1SpecByPath(specs, r.URL.Path)
		if m == nil {
			writeV1Error(w, reqID, http.StatusNotFound, contract.ErrorCodeBadRequest,
				contract.ErrorLayerConnection, "remote endpoint not available", contract.ActionHintRetry)
			return
		}

		// Central Host gate (dynamic port) — frozen gate order: Host → query → method.
		if !strictHostValid(r, port) {
			writeV1Error(w, reqID, http.StatusForbidden, contract.ErrorCodeBadRequest,
				contract.ErrorLayerAuth, "request host rejected", contract.ActionHintCheckDesktop)
			return
		}
		// RawQuery must be empty.
		if r.URL.RawQuery != "" {
			writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
				contract.ErrorLayerConnection, "query parameters are not allowed", contract.ActionHintRetry)
			return
		}
		// Method resolution: resolve the spec matching path AND method. A KNOWN
		// path (m != nil) whose method matches no spec returns 405 (design §12.3);
		// only an exact path+method match proceeds to origin/auth. This correctly
		// disambiguates same-path-different-method routes (GET/POST /sessions,
		// GET/DELETE /sessions/{id}) — v1SpecByPath returns only the first path
		// match, which would otherwise shadow the method-correct sibling.
		mm := v1SpecByPathMethod(specs, r.URL.Path, r.Method)
		if mm == nil {
			// m-001: aggregate ALL active methods for this path (not just the first
			// path match), so a 405 on a shared path like /sessions (GET+POST) or
			// /sessions/{id} (GET+DELETE) lists every allowed method.
			w.Header().Set("Allow", allowedMethodsForPath(specs, r.URL.Path))
			writeV1Error(w, reqID, http.StatusMethodNotAllowed, contract.ErrorCodeBadRequest,
				contract.ErrorLayerConnection, "request method rejected", contract.ActionHintRetry)
			return
		}
		ep := contract.V1RestEndpoints[mm.spec.endpointIndex]
		matched := mm.spec
		// Central origin policy.
		if !enforceOriginPolicy(r, matched.origin, corsAllowed) {
			writeV1Error(w, reqID, http.StatusForbidden, contract.ErrorCodeBadRequest,
				contract.ErrorLayerAuth, "request origin rejected", contract.ActionHintCheckDesktop)
			return
		}
		// Central auth policy.
		principal, authOK := s.enforceAuthPolicy(w, r, matched.auth, reqID)
		if !authOK {
			return
		}

		matched.handler(w, r, ep, reqID, corsAllowed, principal, mm.sessionID)
	})
}

// v1PathMatch holds the result of matching a request path against v1 routes.
type v1PathMatch struct {
	spec      *v1RouteSpec
	sessionID contract.SessionID // extracted from {id}, empty if none
}

// v1SpecByPath finds the active spec whose full path matches path, extracting
// the {id} segment if the manifest pattern contains one. Returns nil if no
// active route matches. NOTE: when multiple specs share a path with different
// methods (GET/POST /sessions, GET/DELETE /sessions/{id}), this returns the
// FIRST path match regardless of method — callers needing method-correct
// resolution MUST use v1SpecByPathMethod. v1SpecByPath is retained for the
// path-known 404 classification and OPTIONS preflight (method-agnostic).
func v1SpecByPath(specs []v1RouteSpec, path string) *v1PathMatch {
	for i := range specs {
		ep := contract.V1RestEndpoints[specs[i].endpointIndex]
		fullPath := contract.RESTBasePath + ep.Path
		if sid, ok := matchV1Path(fullPath, path); ok {
			return &v1PathMatch{spec: &specs[i], sessionID: sid}
		}
	}
	return nil
}

// v1SpecByPathMethod resolves the active spec matching path AND method. It
// returns the method-correct match when one exists, or nil when the path is
// known but no spec matches the method (caller emits 405). This fixes the
// same-path-different-method ambiguity that v1SpecByPath cannot resolve alone.
func v1SpecByPathMethod(specs []v1RouteSpec, path, method string) *v1PathMatch {
	for i := range specs {
		ep := contract.V1RestEndpoints[specs[i].endpointIndex]
		fullPath := contract.RESTBasePath + ep.Path
		if sid, ok := matchV1Path(fullPath, path); ok && ep.Method == method {
			return &v1PathMatch{spec: &specs[i], sessionID: sid}
		}
	}
	return nil
}

// allowedMethodsForPath aggregates every active route method whose full path
// matches `path`, sorted and de-duplicated, then appends OPTIONS. It is the
// source of the 405 Allow header so a shared path (GET+POST /sessions,
// GET+DELETE /sessions/{id}) advertises all its legal methods, not just the
// first registered one (m-001).
func allowedMethodsForPath(specs []v1RouteSpec, path string) string {
	seen := make(map[string]struct{}, 4)
	var methods []string
	for i := range specs {
		ep := contract.V1RestEndpoints[specs[i].endpointIndex]
		fullPath := contract.RESTBasePath + ep.Path
		if _, ok := matchV1Path(fullPath, path); ok {
			if _, dup := seen[ep.Method]; !dup {
				seen[ep.Method] = struct{}{}
				methods = append(methods, ep.Method)
			}
		}
	}
	sort.Strings(methods)
	methods = append(methods, http.MethodOptions)
	return strings.Join(methods, ", ")
}

// matchV1Path matches a request path against a manifest pattern containing an
// optional {id} segment. If the pattern has {id}, the corresponding request
// segment is extracted and validated (design §5.1). Returns (sessionID, true)
// on match; ("", false) on no match or invalid ID.
func matchV1Path(pattern, actual string) (contract.SessionID, bool) {
	// Fast path: exact match (no {id}).
	if pattern == actual {
		return "", true
	}
	// Check for {id} in the pattern.
	idMarker := "{id}"
	idIdx := strings.Index(pattern, idMarker)
	if idIdx < 0 {
		return "", false // no {id} and not exact match
	}
	// Split pattern into prefix (before {id}) and suffix (after {id}).
	// The prefix includes the trailing '/' separator before {id}.
	prefix := pattern[:idIdx]
	suffix := pattern[idIdx+len(idMarker):]
	if !strings.HasPrefix(actual, prefix) {
		return "", false
	}
	if !strings.HasSuffix(actual, suffix) {
		return "", false
	}
	// Extract the {id} segment from between prefix and suffix.
	idPart := actual[len(prefix) : len(actual)-len(suffix)]
	if len(idPart) == 0 {
		return "", false
	}
	// Validate: single segment (no extra /), and PathUnescape exactly once.
	decoded, err := pathUnescapeSegment(idPart)
	if err != nil {
		return "", false
	}
	if !validV1SessionID(decoded) {
		return "", false
	}
	return contract.SessionID(decoded), true
}

// enforceOriginPolicy applies the route's origin policy centrally. No forwarded
// trust; no fallback on a present-but-invalid signal.
func enforceOriginPolicy(r *http.Request, policy v1OriginPolicy, corsAllowed bool) bool {
	switch policy {
	case unsafeOriginRequired:
		// Pairing: exactly one nonempty allowlisted Origin.
		return len(r.Header.Values("Origin")) == 1 && corsAllowed
	case safeBrowserProof:
		return safeBrowserProofOK(r, corsAllowed)
	}
	return false
}

// safeBrowserProofOK implements the design §B.2 fixed precedence for safe GET.
func safeBrowserProofOK(r *http.Request, corsAllowed bool) bool {
	// 1. Origin header present (even blank/multiple): exactly one + allowlist, no fallback.
	if ov := r.Header.Values("Origin"); len(ov) > 0 {
		return len(ov) == 1 && corsAllowed
	}
	// 2. Sec-Fetch-Site present: exactly "same-origin".
	if sfs := r.Header.Values("Sec-Fetch-Site"); len(sfs) > 0 {
		if len(sfs) != 1 {
			return false
		}
		return strings.EqualFold(strings.TrimSpace(sfs[0]), "same-origin")
	}
	// 3. Referer: exactly one absolute URL, strict same scheme/host/port.
	if rv := r.Header.Values("Referer"); len(rv) > 0 {
		if len(rv) != 1 {
			return false
		}
		return refererSameOrigin(r, rv[0])
	}
	return false
}

// refererSameOrigin reports whether ref is one absolute URL strictly same-origin
// (scheme/host/effective-port) with the request. userinfo/opaque/bad host/port
// are rejected; path/query are allowed.
func refererSameOrigin(r *http.Request, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	u, err := url.Parse(ref)
	if err != nil || u == nil || u.Host == "" {
		return false
	}
	if u.Opaque != "" || u.User != nil {
		return false
	}
	// Reject any fragment (strict same-origin proof allows path/query only).
	if u.Fragment != "" || u.RawFragment != "" {
		return false
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return false
	}
	if scheme != requestEffectiveScheme(r) {
		return false
	}
	rh, rp := requestHostPort(r)
	if rh == "" {
		return false
	}
	fh := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if fh != rh || !canonicalRefererHost(fh, u.Host) {
		return false
	}
	fp := 0
	if portStr := u.Port(); portStr != "" {
		if e := parseIntStrict(portStr, &fp); e != nil {
			return false
		}
	}
	return effectivePort(scheme, fp) == effectivePort(scheme, rp)
}

// canonicalRefererHost reports whether the Referer host is canonical: no
// percent-encoding, no control/non-ASCII bytes, no IPv6 zone, and the raw host
// round-trips through validHostLabel (DNS/IPv4/IPv6). This rejects spoofed or
// noncanonical host spellings in the same-origin proof.
func canonicalRefererHost(loweredHost, rawHost string) bool {
	if strings.ContainsAny(loweredHost, "%\t\r\n") {
		return false
	}
	for _, c := range loweredHost {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	if strings.HasPrefix(rawHost, "[") && strings.Contains(rawHost, "%") {
		return false // IPv6 zone
	}
	return validHostLabel(loweredHost)
}

// enforceAuthPolicy applies the route's auth policy centrally and writes the
// §B.3 error on failure (clearing the device cookie where specified). It never
// reflects raw cookie/deviceID/error material.
func (s *Server) enforceAuthPolicy(w http.ResponseWriter, r *http.Request, policy v1AuthPolicy, reqID contract.RequestID) (v1Principal, bool) {
	if policy == publicPairing {
		return v1Principal{}, true
	}
	principal, fail := s.v1sec.deviceAuth.AuthenticateRequest(r)
	if fail == 0 {
		return principal, true
	}
	switch fail {
	case authMissing:
		writeV1Error(w, reqID, http.StatusUnauthorized, contract.ErrorCodeAuthUnpaired,
			contract.ErrorLayerAuth, "device not paired", contract.ActionHintRePair)
	case authExpired:
		http.SetCookie(w, clearDeviceCookie(r))
		writeV1Error(w, reqID, http.StatusUnauthorized, contract.ErrorCodeAuthWindowExpired,
			contract.ErrorLayerAuth, "device credential expired", contract.ActionHintRePair)
	case authRevoked:
		http.SetCookie(w, clearDeviceCookie(r))
		writeV1Error(w, reqID, http.StatusUnauthorized, contract.ErrorCodeAuthRevoked,
			contract.ErrorLayerAuth, "device revoked", contract.ActionHintRePair)
	case authMalformed, authUnpaired:
		http.SetCookie(w, clearDeviceCookie(r))
		writeV1Error(w, reqID, http.StatusUnauthorized, contract.ErrorCodeAuthUnpaired,
			contract.ErrorLayerAuth, "device not paired", contract.ActionHintRePair)
	default: // authStoreDown
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "security state unavailable", contract.ActionHintCheckDesktop)
	}
	return v1Principal{}, false
}

// handleV1HostSummary implements design §B success order: HostSummary
// provider/cache → contract marshal (full body) → RecordDeviceSeen once → 200.
// RecordDeviceSeen outcome/error never changes the prepared 200/status/body
// (its health issues enter security health only). No CLI paths/provider/key/env.
func (s *Server) handleV1HostSummary(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, corsAllowed bool, principal v1Principal, sessionID contract.SessionID) {
	sec := s.v1sec
	host, herr := sec.hostCache.get()
	if herr != nil {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "security state unavailable", contract.ActionHintCheckDesktop)
		return
	}
	body, merr := contract.MarshalRESTResponse(host)
	if merr != nil {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "security state unavailable", contract.ActionHintCheckDesktop)
		return
	}
	// Side effect AFTER the full body is prepared; its outcome is ignored for the
	// response (health/store authority handles any failure).
	_, _ = sec.pairing.RecordDeviceSeen(principal)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ep.SuccessStatus)
	_, _ = w.Write(body)
}

// handleV1PairingComplete implements the design §12.2 fixed order. The request
// ID is resolved once by the dispatcher and passed in; the handler never
// re-resolves it, so the header and the APIError body always share the same ID.
func (s *Server) handleV1PairingComplete(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, corsAllowed bool, principal v1Principal, sessionID contract.SessionID) {
	_ = principal // public route; no authenticated principal
	sec := s.v1sec

	// Content-Type + body cap (method/Host/origin/query gates are central).
	ct := strings.TrimSpace(r.Header.Get("Content-Type"))
	media := strings.SplitN(ct, ";", 2)[0]
	if media != "application/json" {
		writeV1Error(w, reqID, http.StatusUnsupportedMediaType, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "invalid pairing request", contract.ActionHintRetry)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, pairingBodyCap)
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(r.Body); err != nil {
		writeV1Error(w, reqID, http.StatusRequestEntityTooLarge, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "invalid pairing request", contract.ActionHintRetry)
		return
	}

	// Strict decode + business validation (no attempt consumed on failure).
	req, derr := contract.DecodePairingCompleteRequest(raw.Bytes())
	if derr != nil {
		writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "invalid pairing request", contract.ActionHintRetry)
		return
	}
	if _, ferr := decodePairingCode(req.Code); ferr != nil {
		writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "invalid pairing request", contract.ActionHintRetry)
		return
	}
	if _, nerr := canonicalDeviceName(req.DeviceName); nerr != nil {
		writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "invalid pairing request", contract.ActionHintRetry)
		return
	}

	// Cheap window gate (no code compare, no attempt).
	status, _ := sec.pairing.WindowStatus()
	if !status.Active {
		writeV1Error(w, reqID, http.StatusGone, contract.ErrorCodeAuthWindowExpired,
			contract.ErrorLayerAuth, "pairing window is not active", contract.ActionHintCheckDesktop)
		return
	}

	// HostSummary (outside pair/store locks; failure does not consume attempt).
	host, herr := sec.hostCache.get()
	if herr != nil {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "security state unavailable", contract.ActionHintCheckDesktop)
		return
	}

	// Complete pairing (serializes concurrent redeemers; exactly one commits).
	grant, gerr := sec.pairing.CompletePairing(req.Code, req.DeviceName, host)
	if gerr != nil {
		s.mapPairingError(w, reqID, gerr)
		return
	}

	// Record pair event health BEFORE responding.
	if grant.eventOutcome == EventFailed {
		sec.health.Record(HealthEventAppendFailed, "", time.Now())
	}
	if grant.mutation.DurabilityDegraded {
		sec.health.Record(HealthStoreDurabilityDegraded, "", time.Now())
	}

	// Set the device cookie + write the precompiled body at manifest status.
	http.SetCookie(w, issueDeviceCookie(r, grant.deviceID, grant.secret, grant.expiresAt))
	zeroBytes(grant.secret) // wipe the private grant secret ASAP after Cookie issue
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ep.SuccessStatus)
	_, _ = w.Write(grant.responseBody)
}

// mapPairingError maps a deviceService error to the §12.3 error table.
func (s *Server) mapPairingError(w http.ResponseWriter, reqID contract.RequestID, err error) {
	switch {
	case err == errWindowNotActive:
		writeV1Error(w, reqID, http.StatusGone, contract.ErrorCodeAuthWindowExpired,
			contract.ErrorLayerAuth, "pairing window is not active", contract.ActionHintCheckDesktop)
	case err == errWrongCode:
		writeV1Error(w, reqID, http.StatusUnauthorized, contract.ErrorCodeAuthUnpaired,
			contract.ErrorLayerAuth, "pairing code rejected", contract.ActionHintRePair)
	case err == errAttemptsExhausted:
		writeV1Error(w, reqID, http.StatusTooManyRequests, contract.ErrorCodeRateLimited,
			contract.ErrorLayerAuth, "pairing attempts exhausted", contract.ActionHintCheckDesktop)
	case err == errBadCodeFormat:
		writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerAuth, "invalid pairing request", contract.ActionHintRetry)
	case err == errStoreNotCommitted:
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "security state unavailable", contract.ActionHintCheckDesktop)
	case isCapacityErr(err):
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "security capacity reached", contract.ActionHintCheckDesktop)
	default:
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "security state unavailable", contract.ActionHintCheckDesktop)
	}
}

// resolveRequestID returns a valid request ID (generating a fresh one for an
// invalid client-supplied value; the raw value is never reflected). The second
// return is false only when a fresh ID was required and crypto/rand failed
// (caller fails the request closed with a fixed fallback ID).
func resolveRequestID(r *http.Request) (contract.RequestID, bool) {
	v := strings.TrimSpace(r.Header.Get(contract.RequestIDHeader))
	if v != "" && len(v) <= 128 {
		ok := true
		for _, c := range v {
			if c < 0x21 || c > 0x7e {
				ok = false
				break
			}
		}
		if ok {
			return contract.RequestID(v), true
		}
	}
	id := generateRequestID()
	if id == "" {
		return "", false // crypto/rand failure
	}
	return contract.RequestID(id), true
}

// fallbackRequestID is the fixed safe-ASCII ID used only on crypto/rand failure
// (fail-closed path); it is never a raw reflection of client input.
const fallbackRequestID contract.RequestID = "0000000000000000"

// writeV1Error validates + marshals a contract APIError and writes it.
func writeV1Error(w http.ResponseWriter, reqID contract.RequestID, status int, code contract.ErrorCode, layer contract.ErrorLayer, msg string, hint contract.ActionHint) {
	body, err := contract.MarshalAPIError(contract.APIError{
		RequestID: reqID, Code: code, Layer: layer, Message: msg, ActionHint: hint,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// generateRequestID returns a fresh 16-byte RawURL request ID.
func generateRequestID() string {
	return newRequestIDBytes()
}

// newRequestIDBytes reads 16 random bytes and returns the RawURL encoding.
func newRequestIDBytes() string {
	buf := make([]byte, 16)
	if _, err := cryptoRandRead(buf); err != nil {
		return ""
	}
	return rawURLBase64(buf)
}

// MaxV1SessionIDBytes is the private cap on session ID byte length (design §5.1).
const MaxV1SessionIDBytes = 256

// pathUnescapeSegment decodes exactly one percent-encoded path segment. It
// rejects multi-segment values, decoded slashes/backslashes, NUL/control bytes,
// and invalid UTF-8 (design §5.1).
func pathUnescapeSegment(raw string) (string, error) {
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	// Reject decoded slashes/backslashes (path traversal).
	if strings.ContainsAny(decoded, "/\\") {
		return "", pathSegmentErr
	}
	// Reject NUL and control bytes.
	for _, c := range decoded {
		if c == 0 || c < 0x20 || c == 0x7f {
			return "", pathSegmentErr
		}
	}
	return decoded, nil
}

// validV1SessionID validates a decoded session ID (design §5.1).
func validV1SessionID(id string) bool {
	if id == "" || len(id) > MaxV1SessionIDBytes {
		return false
	}
	return true
}

var pathSegmentErr = closedTextError("invalid path segment")
