package remote

// Strict Origin/Host parser + v1 allowlist (design §10.1). Empty Origin is
// rejected. The canonical allowlist is exactly: (1) same-host browser origin
// with effective scheme/port matching the listener, (2) exact capacitor://
// localhost, (3) effective https://localhost:443. No origin-prefix matching, no
// X-Forwarded-* trust. Legacy WS shares this canonical allowlist but keeps its
// own token/query auth path.

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// v1AuthPolicy enumerates the route auth policies.
type v1AuthPolicy uint8

const (
	publicPairing v1AuthPolicy = iota + 1
	deviceCookie
)

// v1OriginPolicy enumerates the route origin policies.
type v1OriginPolicy uint8

const (
	unsafeOriginRequired v1OriginPolicy = iota + 1
	safeBrowserProof
)

// canonicalOrigin is a parsed Origin header.
type canonicalOrigin struct {
	scheme string
	host   string
	port   int // explicit port; 0 if absent
	path   string
}

// parseCanonicalOrigin strictly parses a single Origin header value. It rejects
// empty, "null", comma-lists, userinfo, opaque, query, fragment, non-empty
// path, percent-encoded host and control/non-ASCII host. scheme/host are
// lowercased; a single trailing DNS dot is stripped from the host.
func parseCanonicalOrigin(raw string) (canonicalOrigin, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return canonicalOrigin{}, errOriginRejected
	}
	if strings.Contains(raw, ",") {
		return canonicalOrigin{}, errOriginRejected // comma-list
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return canonicalOrigin{}, errOriginRejected
	}
	if u.Opaque != "" {
		return canonicalOrigin{}, errOriginRejected
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" && scheme != "capacitor" {
		return canonicalOrigin{}, errOriginRejected
	}
	if u.User != nil {
		return canonicalOrigin{}, errOriginRejected // userinfo forbidden
	}
	if u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return canonicalOrigin{}, errOriginRejected
	}
	host := u.Hostname()
	if host == "" {
		return canonicalOrigin{}, errOriginRejected
	}
	// Reject percent-encoded / control / non-ASCII host.
	if strings.ContainsAny(host, "%\t\r\n") {
		return canonicalOrigin{}, errOriginRejected
	}
	for _, r := range host {
		if r < 0x20 || r == 0x7f {
			return canonicalOrigin{}, errOriginRejected
		}
	}
	// IPv6 zone is forbidden.
	if strings.HasPrefix(host, "[") && strings.Contains(host, "%") {
		return canonicalOrigin{}, errOriginRejected
	}
	host = strings.ToLower(host)
	host = strings.TrimSuffix(host, ".") // single trailing dot
	// Path must be empty or "/".
	p := u.EscapedPath()
	if p != "" && p != "/" {
		return canonicalOrigin{}, errOriginRejected
	}
	portStr := u.Port()
	port := 0
	if portStr != "" {
		var num int
		if err := parseIntStrict(portStr, &num); err != nil {
			return canonicalOrigin{}, errOriginRejected
		}
		port = num
	}
	return canonicalOrigin{scheme: scheme, host: host, port: port, path: p}, nil
}

// effectivePort returns the default port for a scheme when port==0.
func effectivePort(scheme string, port int) int {
	if port != 0 {
		return port
	}
	switch scheme {
	case "https", "capacitor":
		// capacitor has no numeric port; callers treat capacitor separately.
		if scheme == "https" {
			return 443
		}
		return 0
	case "http":
		return 80
	}
	return port
}

// requestEffectiveScheme returns the transport scheme (ignores forwarded headers).
func requestEffectiveScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// requestHostPort splits r.Host into host + explicit port.
func requestHostPort(r *http.Request) (string, int) {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return "", 0
	}
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(host, "]"), "["), ".")), 0
	}
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	var port int
	if err := parseIntStrict(p, &port); err != nil {
		return h, 0
	}
	return h, port
}

// canonicalAllowedOrigin reports whether origin is allowed for this request per
// the design §10.1 allowlist. Empty/invalid origin is rejected.
func canonicalAllowedOrigin(r *http.Request, origin string) bool {
	co, err := parseCanonicalOrigin(origin)
	if err != nil {
		return false
	}
	// Case 2: exact capacitor://localhost (no port/path/query/fragment).
	if co.scheme == "capacitor" {
		return co.host == "localhost" && co.port == 0 && co.path == ""
	}
	// Case 3: effective https://localhost:443.
	if co.scheme == "https" && co.host == "localhost" && effectivePort(co.scheme, co.port) == 443 && co.path == "" {
		return true
	}
	// Case 1: same-host browser origin. scheme/host/effective-port must match.
	if co.scheme != "http" && co.scheme != "https" {
		return false
	}
	wantScheme := requestEffectiveScheme(r)
	if co.scheme != wantScheme {
		return false
	}
	reqHost, reqPort := requestHostPort(r)
	if reqHost == "" {
		return false
	}
	if co.host != reqHost {
		return false
	}
	if effectivePort(co.scheme, co.port) != effectivePort(wantScheme, reqPort) {
		return false
	}
	return co.path == ""
}

// strictHostValid validates the Host header for a v1 pairing/unsafe request:
// non-empty, no userinfo ('@') / IPv6 zone ('%') / non-ASCII / malformed labels,
// and effective port == serverPort. SplitHostPort errors are NOT treated as a
// no-port host: only a colon-free DNS/IPv4 name or a bracketed [IPv6] is
// accepted without a port; bare IPv6 / `foo:bad` / `foo/bar` / query/hash are
// rejected.
func strictHostValid(r *http.Request, serverPort int) bool {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return false
	}
	if strings.ContainsAny(host, "@") {
		return false
	}
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		// No-port form.
		if strings.HasPrefix(host, "[") {
			// Bracketed [IPv6] without port.
			if !strings.HasSuffix(host, "]") {
				return false
			}
			inner := host[1 : len(host)-1]
			if strings.Contains(inner, "%") { // zone
				return false
			}
			if net.ParseIP(inner) == nil {
				return false
			}
			return effectivePort(requestEffectiveScheme(r), 0) == serverPort
		}
		// Colon-free DNS/IPv4 host only; bare IPv6 / slash / query / hash rejected.
		if strings.Contains(host, ":") || strings.ContainsAny(host, "/?#") {
			return false
		}
		if !validDNSLabelHost(host) {
			return false
		}
		return effectivePort(requestEffectiveScheme(r), 0) == serverPort
	}
	// With-port form.
	if strings.Contains(h, "%") || strings.ContainsAny(h, "/?#") { // zone / path / query
		return false
	}
	var port int
	if e := parseIntStrict(p, &port); e != nil {
		return false
	}
	if port < 1 || port > 65535 {
		return false
	}
	if !validHostLabel(h) {
		return false
	}
	return effectivePort(requestEffectiveScheme(r), port) == serverPort
}

// validHostLabel validates a host (DNS/IPv4 or IPv6). IPv6 (contains ':') must
// parse; DNS/IPv4 must pass label rules.
func validHostLabel(h string) bool {
	if h == "" {
		return false
	}
	if strings.Contains(h, ":") {
		return net.ParseIP(h) != nil
	}
	return validDNSLabelHost(h)
}

// validDNSLabelHost validates a DNS/IPv4 host strictly: a single optional
// trailing dot (stripped before validation), total length ≤ 253, each label
// 1..63 chars of ASCII letter/digit/hyphen only, no leading/trailing hyphen,
// no inner empty label, no path/query/space.
func validDNSLabelHost(h string) bool {
	if h == "" {
		return false
	}
	// Allow exactly one trailing dot.
	if strings.HasSuffix(h, ".") {
		h = h[:len(h)-1]
		if h == "" || strings.HasSuffix(h, ".") {
			return false // "." alone or multiple trailing dots
		}
	}
	if len(h) > 253 {
		return false
	}
	if strings.ContainsAny(h, "/?# ") {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if len(label) < 1 || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return false
			}
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	return true
}

// errOriginRejected is the fixed closed-text origin rejection error.
var errOriginRejected = originError{}

type originError struct{}

func (originError) Error() string { return "request origin rejected" }

// parseIntStrict parses a base-10 integer with no surrounding sign/space beyond
// the digits (used for ports). Returns an error on overflow/invalid.
func parseIntStrict(s string, out *int) error {
	if s == "" {
		return errOriginRejected
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return errOriginRejected
		}
		n = n*10 + int(c-'0')
		if n > 65535 {
			return errOriginRejected
		}
	}
	*out = n
	return nil
}
