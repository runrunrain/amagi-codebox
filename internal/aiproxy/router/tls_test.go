package router

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/aiproxy/subscription"
)

func TestHTTPSRemainsEndToEnd(t *testing.T) {
	core := os.Getenv("SING_BOX_TEST_BINARY")
	if core == "" {
		t.Skip("set SING_BOX_TEST_BINARY to run real-core TLS test")
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := range 3 {
			fmt.Fprintf(w, "data: encrypted-%d\n\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(80 * time.Millisecond)
		}
	}))
	defer origin.Close()
	var connects atomic.Int32
	exit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", 400)
			return
		}
		upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			http.Error(w, "dial failed", 502)
			return
		}
		defer upstream.Close()
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		connects.Add(1)
		fmt.Fprint(rw, "HTTP/1.1 200 Connection Established\r\n\r\n")
		rw.Flush()
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, rw)
			if tcp, ok := upstream.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
			}
			close(done)
		}()
		_, _ = io.Copy(conn, upstream)
		_ = conn.Close()
		<-done
	}))
	defer exit.Close()
	s := Defaults()
	ports := freePorts(t)
	s.ProxyPort, s.ClaudePort, s.OpenAIPort = ports[0], ports[1], ports[2]
	s.ClaudeNode = "c"
	s.OpenAINode = "o"
	s.FallbackProxy = ""
	node := proxyNode(t, exit.URL, "TLS relay")
	config, err := Build(s, map[string]subscription.Node{"c": node, "o": node})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(core, t.TempDir())
	defer m.Stop()
	if err = m.Apply(context.Background(), config, ports, 1); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	p, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.ClaudePort))
	tr := &http.Transport{Proxy: http.ProxyURL(p), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TLS == nil || len(resp.TLS.VerifiedChains) == 0 || connects.Load() != 1 || string(body) != "data: encrypted-0\n\ndata: encrypted-1\n\ndata: encrypted-2\n\n" {
		t.Fatal("TLS tunnel did not preserve validated TLS and complete SSE")
	}
}
