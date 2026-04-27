package remote

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/logging"
)

// testMobileFSSource provides real embedded FS from testdata.
// Keys are "testdata/mobile/dist/...".
//
//go:embed all:testdata/mobile/dist
var testMobileFSSource embed.FS

func newTestServer(t *testing.T, port int) *Server {
	t.Helper()
	logDir := t.TempDir()
	logSvc := logging.NewService(logDir)
	t.Cleanup(logSvc.Close)
	return NewServer(port, nil, logSvc, embed.FS{})
}

func newTestServerWithEmbeddedMobile(t *testing.T, port int) *Server {
	t.Helper()
	logDir := t.TempDir()
	logSvc := logging.NewService(logDir)
	t.Cleanup(logSvc.Close)
	srv := NewServer(port, nil, logSvc, testMobileFSSource)
	// Adjust prefix to match the embed path within the FS
	srv.mobileAssetsPrefix = "testdata/mobile/dist"
	return srv
}

func TestBuildDesktopLaunchURLUsesLoopbackAndLaunchParams(t *testing.T) {
	tests := []struct {
		name         string
		listenHost   string
		expectedHost string
	}{
		{name: "empty host maps to loopback", listenHost: "", expectedHost: "127.0.0.1:8680"},
		{name: "ipv4 wildcard maps to loopback", listenHost: "0.0.0.0", expectedHost: "127.0.0.1:8680"},
		{name: "ipv6 wildcard maps to loopback", listenHost: "::", expectedHost: "127.0.0.1:8680"},
		{name: "localhost maps to loopback", listenHost: "localhost", expectedHost: "127.0.0.1:8680"},
		{name: "specific ipv4 stays reachable", listenHost: "192.168.31.8", expectedHost: "192.168.31.8:8680"},
		{name: "specific hostname stays reachable", listenHost: "example.internal", expectedHost: "example.internal:8680"},
		{name: "specific ipv6 stays reachable", listenHost: "2001:db8::8", expectedHost: "[2001:db8::8]:8680"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t, 8680)
			srv.SetHost(tt.listenHost)
			raw := srv.BuildDesktopLaunchURL()

			parsed, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse launch url: %v", err)
			}

			if parsed.Scheme != "http" {
				t.Fatalf("expected http scheme, got %q", parsed.Scheme)
			}
			if parsed.Host != tt.expectedHost {
				t.Fatalf("expected host %q, got %q", tt.expectedHost, parsed.Host)
			}
			if parsed.Query().Has("token") {
				t.Fatalf("expected launch url without token, got %q", parsed.Query().Get("token"))
			}
			if launch := parsed.Query().Get("launch"); launch == "" {
				t.Fatal("expected launch url to include one-time launch grant")
			}
			if parsed.Query().Get("autoconnect") != "1" {
				t.Fatalf("expected autoconnect=1, got %q", parsed.Query().Get("autoconnect"))
			}
		})
	}
}

// --- User-configured web root tests (no embedded FS) ---

func TestGetMobileWebRootStatusChecksIndexHTML(t *testing.T) {
	srv := newTestServer(t, 8680)
	webRoot := t.TempDir()
	indexPath := filepath.Join(webRoot, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	srv.SetWebRoot(webRoot)

	root, configured, exists := srv.GetMobileWebRootStatus()
	if root != webRoot {
		t.Fatalf("expected root %q, got %q", webRoot, root)
	}
	if !configured {
		t.Fatal("expected configured=true")
	}
	if !exists {
		t.Fatal("expected exists=true")
	}

	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("remove index.html: %v", err)
	}

	_, configured, exists = srv.GetMobileWebRootStatus()
	if !configured {
		t.Fatal("expected configured=true after removing index")
	}
	if exists {
		t.Fatal("expected exists=false after removing index")
	}
}

// --- Embedded FS detection ---

func TestHasEmbeddedMobileWeb_EmptyFS(t *testing.T) {
	srv := newTestServer(t, 8680)
	if srv.HasEmbeddedMobileWeb() {
		t.Fatal("empty embed.FS should not have mobile/dist/index.html")
	}
}

func TestHasEmbeddedMobileWeb_WithTestFixtures(t *testing.T) {
	srv := newTestServerWithEmbeddedMobile(t, 8680)
	if !srv.HasEmbeddedMobileWeb() {
		t.Fatal("testMobileFS should have embedded index.html")
	}
}

// --- Embedded FS serving (priority 2: no user config, embedded available) ---

func TestEmbeddedFS_ServesIndexHTML(t *testing.T) {
	srv := newTestServerWithEmbeddedMobile(t, 0)
	// No SetWebRoot: exercises the real embedded path
	handler := srv.buildHandler()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Mobile Test") {
		t.Fatalf("GET / body should contain 'Mobile Test', got: %s", body)
	}
}

func TestEmbeddedFS_ServesStaticAsset(t *testing.T) {
	srv := newTestServerWithEmbeddedMobile(t, 0)
	handler := srv.buildHandler()

	req := httptest.NewRequest("GET", "/assets/style.css", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /assets/style.css status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "font-family") {
		t.Fatalf("GET /assets/style.css body = %q, want CSS content", body)
	}
}

func TestEmbeddedFS_SPAFallback(t *testing.T) {
	srv := newTestServerWithEmbeddedMobile(t, 0)
	handler := srv.buildHandler()

	// SPA route that doesn't exist as a file
	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("SPA fallback GET /dashboard status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Mobile Test") {
		t.Fatalf("SPA fallback should return index.html, got: %s", body)
	}
}

// --- User override priority ---

func TestUserWebRootOverridesEmbedded(t *testing.T) {
	userDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDir, "index.html"), []byte("<html>user-override</html>"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	srv := newTestServerWithEmbeddedMobile(t, 0)
	srv.SetWebRoot(userDir)
	handler := srv.buildHandler()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "user-override") {
		t.Fatalf("expected user override content, got: %s", body)
	}
}

func TestUserWebRootInvalid_FallsBackToEmbedded(t *testing.T) {
	userDir := t.TempDir()
	// No index.html in userDir -> user config invalid, should fall back to embedded

	srv := newTestServerWithEmbeddedMobile(t, 0)
	srv.SetWebRoot(userDir)
	handler := srv.buildHandler()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (embedded fallback)", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Mobile Test") {
		t.Fatalf("expected embedded content (user dir invalid), got: %s", body)
	}
}

// --- No resources at all ---

func TestNoWebRootNoEmbedded_FallsBackToAPI(t *testing.T) {
	srv := newTestServer(t, 0)
	handler := srv.buildHandler()

	req := httptest.NewRequest("GET", "/something", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (unauthorized API fallback)", w.Code)
	}
}

// --- Static file detection (not SPA fallback for real files) ---

func TestServeStaticFile_NotSPAFallback(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>index</html>"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "test.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatalf("write css: %v", err)
	}

	srv := newTestServer(t, 0)
	srv.SetWebRoot(webDir)
	handler := srv.buildHandler()

	req := httptest.NewRequest("GET", "/test.css", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /test.css status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if body != "body{}" {
		t.Fatalf("GET /test.css body = %q, want body{}}", body)
	}
}

// --- MobileWebAvailable status semantics ---

func TestMobileWebStatus_EmptyFS_NoUserConfig(t *testing.T) {
	srv := newTestServer(t, 8680)
	if srv.HasEmbeddedMobileWeb() {
		t.Fatal("empty FS should not report embedded mobile web")
	}
	_, configured, exists := srv.GetMobileWebRootStatus()
	if configured {
		t.Fatal("should not be configured with empty FS and no user config")
	}
	if exists {
		t.Fatal("should not exist with empty FS and no user config")
	}
}

func TestMobileWebStatus_WithEmbedded_NoUserConfig(t *testing.T) {
	srv := newTestServerWithEmbeddedMobile(t, 8680)
	if !srv.HasEmbeddedMobileWeb() {
		t.Fatal("should report embedded mobile web available")
	}
	_, configured, _ := srv.GetMobileWebRootStatus()
	if configured {
		t.Fatal("user config should not be reported as configured")
	}
}

func TestMobileWebStatus_WithEmbedded_AndValidUserConfig(t *testing.T) {
	userDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDir, "index.html"), []byte("<html>user</html>"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	srv := newTestServerWithEmbeddedMobile(t, 8680)
	srv.SetWebRoot(userDir)

	if !srv.HasEmbeddedMobileWeb() {
		t.Fatal("embedded should still be available")
	}
	_, configured, exists := srv.GetMobileWebRootStatus()
	if !configured {
		t.Fatal("user config should be configured")
	}
	if !exists {
		t.Fatal("user config index.html should exist")
	}
}
