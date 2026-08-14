package remote

// Canonical-input final micro-fix regression tests (3 items, test-first).

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// 1) validDNSLabelHost strict
// ---------------------------------------------------------------------------

func TestValidDNSLabelHostStrict(t *testing.T) {
	cases := []struct {
		name string
		host string
		want bool
	}{
		{"simple", "example.com", true},
		{"single trailing dot", "example.com.", true},
		{"double trailing dot", "example.com..", false},
		{"inner empty label", "example..com", false},
		{"underscore", "foo_bar.example.com", false},
		{"bang", "foo!bar.example.com", false},
		{"space", "foo bar", false},
		{"leading hyphen label", "-bad.example.com", false},
		{"trailing hyphen label", "bad-.example.com", false},
		{"ipv4", "192.168.1.8", true},
		{"64-char label", strings.Repeat("a", 64) + ".example.com", false},
		{"63-char label", strings.Repeat("a", 63) + ".example.com", true},
		{"empty", "", false},
		{"dot only", ".", false},
		{"slash", "foo/bar", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validDNSLabelHost(c.host); got != c.want {
				t.Errorf("validDNSLabelHost(%q)=%v want %v", c.host, got, c.want)
			}
		})
	}
	// Total host length ≤ 253.
	longLabel := strings.Repeat("a", 50)
	longHost := longLabel + "." + longLabel + "." + longLabel + "." + longLabel + "." + longLabel // 250+ dots
	if len(longHost) <= 253 && !validDNSLabelHost(longHost) {
		t.Errorf("host len %d within 253 should be accepted", len(longHost))
	}
	tooLong := strings.Repeat("a", 254)
	if validDNSLabelHost(tooLong) {
		t.Errorf("host len %d should be rejected (>253)", len(tooLong))
	}
}

// ---------------------------------------------------------------------------
// 2) Revoke DeviceID: no surrounding whitespace; exact validated ID; state unchanged
// ---------------------------------------------------------------------------

func TestRevokeDeviceIDRejectsPaddedWhitespace(t *testing.T) {
	srv, _ := newSecServer(t, validHostSummary)
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	t.Cleanup(ts.Close)
	wireTestPort(srv, ts)
	// Pair a real device so we can prove a padded copy of its id does NOT revoke it.
	body, _ := json.Marshal(map[string]string{"code": code, "deviceName": "phone"})
	req := httptest.NewRequest("POST", contract.RESTBasePath+"/pairing/complete", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	req.Host = strings.TrimPrefix(ts.URL, "http://")
	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, req)
	if rr.Code != contract.V1RestEndpoints[0].SuccessStatus {
		t.Fatalf("pair status=%d", rr.Code)
	}
	var resp contract.PairingCompleteResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	deviceID := string(resp.Device.ID)

	// Padded whitespace around the real id is rejected and must NOT revoke.
	padded := " " + deviceID + " "
	if _, err := srv.RevokeDevice(padded, true); err == nil {
		t.Fatal("padded-whitespace id must be rejected")
	}
	list, err := srv.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || string(list[0].ID) != deviceID {
		t.Fatalf("padded revoke must not change device list; got %d devices", len(list))
	}
	if list[0].RevokedAt != nil {
		t.Fatal("device must not be revoked by padded id")
	}

	// The exact canonical id revokes it.
	res, err := srv.RevokeDevice(deviceID, true)
	if err != nil {
		t.Fatalf("canonical revoke: %v", err)
	}
	if res.AlreadyRevoked || res.Device.RevokedAt == nil {
		t.Fatal("canonical revoke did not take effect")
	}
}

// ---------------------------------------------------------------------------
// 3) Canonical base64: re-encode-equal; non-canonical trailing bits rejected
// ---------------------------------------------------------------------------

var (
	rawURLAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	stdAlphabet    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
)

// nonCanonicalRawURL returns a 22-char string that decodes to the same 16 bytes
// as the canonical encoding of b, but is NOT canonical (non-zero trailing bits),
// or ("",false) if no such variant exists.
func nonCanonicalRawURL(b []byte) (string, bool) {
	canon := base64.RawURLEncoding.EncodeToString(b)
	want, _ := base64.RawURLEncoding.DecodeString(canon)
	last := canon[len(canon)-1]
	for i := 0; i < len(rawURLAlphabet); i++ {
		cand := rawURLAlphabet[i]
		if cand == last {
			continue
		}
		probe := canon[:len(canon)-1] + string(cand)
		dec, err := base64.RawURLEncoding.DecodeString(probe)
		if err == nil && len(dec) == 16 && string(dec) == string(want) {
			return probe, true
		}
	}
	return "", false
}

// nonCanonicalPadded returns a 44-char padded-base64 string decoding to the same
// 32 bytes as canon but non-canonical, or ("",false).
func nonCanonicalPadded(b []byte) (string, bool) {
	canon := base64.StdEncoding.EncodeToString(b)
	want, _ := base64.StdEncoding.DecodeString(canon)
	last := canon[42] // last meaningful char before '=' padding
	for i := 0; i < len(stdAlphabet); i++ {
		cand := stdAlphabet[i]
		if cand == last {
			continue
		}
		probe := canon[:42] + string(cand) + canon[43:]
		dec, err := base64.StdEncoding.DecodeString(probe)
		if err == nil && len(dec) == 32 && string(dec) == string(want) {
			return probe, true
		}
	}
	return "", false
}

func TestCanonicalBase64RejectsNonCanonical(t *testing.T) {
	// validRawURLID
	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	nc, ok := nonCanonicalRawURL(idBytes)
	if !ok {
		t.Skip("no non-canonical RawURL variant constructed")
	}
	if validRawURLID(nc) {
		t.Fatal("validRawURLID accepted a non-canonical (trailing-bit) encoding")
	}
	if !validRawURLID(base64.RawURLEncoding.EncodeToString(idBytes)) {
		t.Fatal("canonical RawURL id rejected")
	}

	// validPaddedHash / validPaddedLen (32 bytes)
	hBytes := make([]byte, 32)
	rand.Read(hBytes)
	ncp, ok := nonCanonicalPadded(hBytes)
	if !ok {
		t.Skip("no non-canonical padded variant constructed")
	}
	if validPaddedHash(ncp) {
		t.Fatal("validPaddedHash accepted a non-canonical encoding")
	}
	if validPaddedLen(ncp, 32) {
		t.Fatal("validPaddedLen accepted a non-canonical encoding")
	}
	if !validPaddedHash(base64.StdEncoding.EncodeToString(hBytes)) {
		t.Fatal("canonical padded hash rejected")
	}
}

func TestCookieRejectsNonCanonicalDeviceID(t *testing.T) {
	// Build a cookie value with a non-canonical DeviceID; parseDeviceCookie must
	// reject it (so auth can never succeed). Secret material is never printed.
	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	ncID, ok := nonCanonicalRawURL(idBytes)
	if !ok {
		t.Skip("no non-canonical variant")
	}
	secret := make([]byte, 32)
	rand.Read(secret)
	secretStr := base64.RawURLEncoding.EncodeToString(secret)
	// Malformed cookie (non-canonical id) → present=true, err.
	val := "v1." + ncID + "." + secretStr
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: deviceCookieName, Value: val})
	_, _, present, err := parseDeviceCookie(req)
	if !present {
		t.Fatal("expected cookie present")
	}
	if err == nil {
		t.Fatal("non-canonical device id cookie must be rejected")
	}
	// A canonical cookie parses cleanly.
	canonID := base64.RawURLEncoding.EncodeToString(idBytes)
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(&http.Cookie{Name: deviceCookieName, Value: "v1." + canonID + "." + secretStr})
	_, _, present2, err2 := parseDeviceCookie(req2)
	if !present2 || err2 != nil {
		t.Fatalf("canonical cookie must parse: present=%v err=%v", present2, err2)
	}
}
