package remote

// Device Cookie + authenticator (design §9). The Cookie is the SOLE device
// credential carrier; Bearer/query/body/legacy cookies are never accepted for
// v1. Authentication samples the clock ONCE, verifies the credential via a
// domain-separated SHA-256 + constant-time compare (with a dummy-hash path for
// unknown DeviceIDs to avoid a timing oracle), enforces strict expiry, then
// applies the LEDGER-DERIVED revoked state (ledger is the revocation authority).
// No credential/secret/code/Cookie value is ever logged.

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// deviceCookieName is the provisional Cookie name (O-3, M1-INT freeze).
const deviceCookieName = "amagi_codebox_device"

// deviceCredentialTTL is the fixed 30-day server/Cookie expiry (no sliding).
const deviceCredentialTTL = 30 * 24 * time.Hour

// deviceAuthFailure is the closed authenticator failure category.
type deviceAuthFailure uint8

const (
	authMissing deviceAuthFailure = iota + 1
	authMalformed
	authUnpaired
	authRevoked
	authExpired
	authStoreDown
)

// issueDeviceCookie builds the device Cookie. Value is exactly
// "v1.<22-char DeviceID>.<43-char secret>" (69 bytes). Secure only under TLS.
func issueDeviceCookie(r *http.Request, deviceID string, secret []byte, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     deviceCookieName,
		Value:    deviceCookieValue(deviceID, secret),
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(deviceCredentialTTL / time.Second),
		Expires:  expiresAt.UTC(),
	}
}

// clearDeviceCookie builds a deletion Cookie. Same name/path/HttpOnly/SameSite/
// Secure policy; empty value, MaxAge=-1, Unix-epoch Expires, no Domain. Only the
// device cookie is cleared (never the legacy local-session cookie).
func clearDeviceCookie(r *http.Request) *http.Cookie {
	return &http.Cookie{
		Name:     deviceCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	}
}

// deviceCookieValue composes the exact-69-byte cookie value.
func deviceCookieValue(deviceID string, secret []byte) string {
	return "v1." + deviceID + "." + base64.RawURLEncoding.EncodeToString(secret)
}

// parseDeviceCookie locates exactly one device cookie and strictly parses its
// value into deviceID (16 raw bytes) + secret (32 raw bytes). Duplicate target
// cookies, wrong length, bad prefix or non-canonical encoding are malformed.
func parseDeviceCookie(r *http.Request) (deviceID string, secret []byte, present bool, err error) {
	var found *http.Cookie
	for _, c := range r.Cookies() {
		if c.Name == deviceCookieName {
			if found != nil {
				return "", nil, true, errMalformedCookie
			}
			found = c
		}
	}
	if found == nil {
		return "", nil, false, nil
	}
	v := found.Value
	if len(v) != 69 {
		return "", nil, true, errMalformedCookie
	}
	dot1 := strings.IndexByte(v, '.')
	if dot1 != 2 || v[:dot1] != "v1" {
		return "", nil, true, errMalformedCookie
	}
	rest := v[3:]
	dot2 := strings.LastIndexByte(rest, '.')
	if dot2 < 0 {
		return "", nil, true, errMalformedCookie
	}
	idPart := rest[:dot2]
	secretPart := rest[dot2+1:]
	id, ierr := base64.RawURLEncoding.DecodeString(idPart)
	if ierr != nil || len(id) != 16 {
		return "", nil, true, errMalformedCookie
	}
	sec, serr := base64.RawURLEncoding.DecodeString(secretPart)
	if serr != nil || len(sec) != 32 {
		return "", nil, true, errMalformedCookie
	}
	// Re-encode to reject non-canonical encodings.
	if base64.RawURLEncoding.EncodeToString(id) != idPart || base64.RawURLEncoding.EncodeToString(sec) != secretPart {
		return "", nil, true, errMalformedCookie
	}
	return idPart, sec, true, nil
}

var errMalformedCookie = errors.New("malformed device cookie")

// deviceAuthenticator authenticates device Cookies against the store.
type deviceAuthenticator struct {
	store *fileDeviceStore
	gate  *securityMaintenanceGate
	clock Clock
}

// newDeviceAuthenticator constructs an authenticator bound to the live store.
func newDeviceAuthenticator(store *fileDeviceStore, gate *securityMaintenanceGate, clock Clock) *deviceAuthenticator {
	return &deviceAuthenticator{store: store, gate: gate, clock: clock}
}

// AuthenticateRequest validates the device cookie and returns the principal or a
// closed failure category. It samples the clock once; unknown DeviceIDs traverse
// the same SHA-256 + constant-time path via a dummy hash (no timing oracle). The
// ledger-derived revoked state is applied last (ledger is the authority).
func (a *deviceAuthenticator) AuthenticateRequest(r *http.Request) (DevicePrincipal, deviceAuthFailure) {
	now := a.clock.Now().UTC()

	if !a.store.Ready() {
		return DevicePrincipal{}, authStoreDown
	}

	deviceIDStr, secret, present, err := parseDeviceCookie(r)
	if secret != nil {
		defer zeroBytes(secret) // wipe parsed secret on every return; never logged/cached
	}
	if !present {
		return DevicePrincipal{}, authMissing
	}
	if err != nil {
		return DevicePrincipal{}, authMalformed
	}

	// Lookup via a normal permit (fails closed if the gate is not normal).
	permit, ok := a.gate.issueNormalPermit()
	if !ok {
		return DevicePrincipal{}, authStoreDown
	}
	rec, found, lerr := a.store.Lookup(permit, contract.DeviceID(deviceIDStr))
	a.gate.returnNormalPermit(permit)
	if lerr != nil {
		return DevicePrincipal{}, authStoreDown
	}

	// Constant-time digest verification. Unknown IDs use a dummy salt/hash so the
	// SHA-256 + compare path is identical; the result is always a mismatch.
	salt := dummySalt
	storedHash := dummyHash
	if found {
		salt = rec.CredentialSalt
		storedHash = rec.CredentialHash
	}
	computed := computeDeviceDigest(salt, secret)
	if subtle.ConstantTimeCompare(computed, storedHash) != 1 {
		return DevicePrincipal{}, authUnpaired
	}
	if !found {
		return DevicePrincipal{}, authUnpaired
	}

	// Strict expiry: exact expiry rejects.
	if !now.Before(rec.CredentialExpiresAt) {
		return DevicePrincipal{}, authExpired
	}

	// Ledger-first revoke: a record carrying RevokedAt (set from the ledger
	// authority) means revoked.
	if rec.RevokedAt != nil {
		return DevicePrincipal{}, authRevoked
	}

	return DevicePrincipal{
		DeviceID:            rec.ID,
		DeviceName:          rec.Name,
		AuthenticatedAt:     now,
		CredentialExpiresAt: rec.CredentialExpiresAt,
	}, 0
}

// dummySalt/dummyHash give unknown-ID lookups the same crypto path. They are
// never a real credential (a 256-bit random secret has negligible probability
// of matching a fixed dummy digest).
var (
	dummySalt = make([]byte, 16)
	dummyHash = make([]byte, 32)
)
