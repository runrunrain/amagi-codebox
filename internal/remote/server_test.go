package remote

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/logging"
)

func newTestServer(t *testing.T, port int) *Server {
	t.Helper()
	logDir := t.TempDir()
	logSvc := logging.NewService(logDir)
	t.Cleanup(logSvc.Close)
	return NewServer(port, nil, logSvc)
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
