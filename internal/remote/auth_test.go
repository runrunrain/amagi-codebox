package remote

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthValidateRejectsLoopbackWithoutToken(t *testing.T) {
	auth := newAuth()
	req := httptest.NewRequest("GET", "http://127.0.0.1:8680/api/info", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "127.0.0.1:8680"

	if auth.validate(req) {
		t.Fatal("expected loopback request without token to be rejected")
	}
}

func TestAuthValidateAcceptsBearerForNonLoopback(t *testing.T) {
	auth := newAuth()
	req := httptest.NewRequest("GET", "http://192.168.1.10:8680/api/info", nil)
	req.RemoteAddr = "192.168.1.10:54321"
	req.Host = "192.168.1.10:8680"
	req.Header.Set("Authorization", "Bearer "+auth.GetToken())

	if !auth.validate(req) {
		t.Fatal("expected bearer token to authorize non-loopback request")
	}
}

func TestConsumeLaunchGrantCreatesSameOriginLocalSession(t *testing.T) {
	auth := newAuth()
	grant := auth.IssueLaunchGrant("127.0.0.1")

	consumeReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8680/api/bootstrap/consume", nil)
	consumeReq.Host = "127.0.0.1:8680"
	consumeReq.Header.Set("Origin", "http://127.0.0.1:8680")

	cookie, err := auth.ConsumeLaunchGrant(consumeReq, grant)
	if err != nil {
		t.Fatalf("ConsumeLaunchGrant: %v", err)
	}
	if cookie.Name != localSessionCookieName {
		t.Fatalf("cookie name = %q, want %q", cookie.Name, localSessionCookieName)
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8680/api/info", nil)
	req.Host = "127.0.0.1:8680"
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(cookie)

	if !auth.validate(req) {
		t.Fatal("expected local session cookie to authorize same-origin browser request")
	}
}

func TestAuthValidateRejectsCrossOriginLocalSession(t *testing.T) {
	auth := newAuth()
	grant := auth.IssueLaunchGrant("127.0.0.1")

	consumeReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8680/api/bootstrap/consume", nil)
	consumeReq.Host = "127.0.0.1:8680"
	consumeReq.Header.Set("Origin", "http://127.0.0.1:8680")
	cookie, err := auth.ConsumeLaunchGrant(consumeReq, grant)
	if err != nil {
		t.Fatalf("ConsumeLaunchGrant: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8680/api/info", nil)
	req.Host = "127.0.0.1:8680"
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(cookie)

	if auth.validate(req) {
		t.Fatal("expected cross-origin browser request to be rejected even with local session cookie")
	}
}

func TestConsumeLaunchGrantRejectsHostMismatch(t *testing.T) {
	auth := newAuth()
	grant := auth.IssueLaunchGrant("192.168.31.8")

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8680/api/bootstrap/consume", nil)
	req.Host = "127.0.0.1:8680"
	req.Header.Set("Origin", "http://127.0.0.1:8680")

	if _, err := auth.ConsumeLaunchGrant(req, grant); err == nil {
		t.Fatal("expected launch grant host mismatch to be rejected")
	}
}

func TestWebSocketOriginCheckRejectsCrossOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8680/ws/terminal/demo", nil)
	req.Host = "127.0.0.1:8680"
	req.Header.Set("Origin", "https://evil.example")

	if isAllowedWebSocketOrigin(req) {
		t.Fatal("expected cross-origin websocket origin to be rejected")
	}
}
