package updater

import (
	"net/http"
	"net/url"
	"sync"
	"testing"
)

// stubProxyFuncs 替换 env 代理/系统代理探测与 OnceValue 缓存，返回恢复函数。
func stubProxyFuncs(t *testing.T, envProxy func(*http.Request) (*url.URL, error), systemEnv map[string]string) {
	t.Helper()
	origEnv, origDetect, origOnce := envProxyFunc, detectSystemProxyEnv, systemProxyFunc
	envProxyFunc = envProxy
	detectSystemProxyEnv = func() map[string]string { return systemEnv }
	systemProxyFunc = sync.OnceValue(buildSystemProxyFunc)
	t.Cleanup(func() {
		envProxyFunc, detectSystemProxyEnv, systemProxyFunc = origEnv, origDetect, origOnce
	})
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return u
}

func TestUpdateTransportProxy_EnvProxyWins(t *testing.T) {
	envProxyURL := mustParseURL(t, "http://127.0.0.1:9000")
	stubProxyFuncs(t,
		func(*http.Request) (*url.URL, error) { return envProxyURL, nil },
		map[string]string{"HTTPS_PROXY": "http://127.0.0.1:7890"},
	)

	req, err := http.NewRequest(http.MethodGet, "https://github.com/x/y.zip", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	got, err := updateTransportProxy(req)
	if err != nil {
		t.Fatalf("updateTransportProxy: %v", err)
	}
	if got != envProxyURL {
		t.Errorf("proxy = %v, want env proxy %v", got, envProxyURL)
	}
}

func TestUpdateTransportProxy_SystemProxyFallback(t *testing.T) {
	stubProxyFuncs(t,
		func(*http.Request) (*url.URL, error) { return nil, nil },
		map[string]string{
			"HTTP_PROXY":  "http://127.0.0.1:7890",
			"HTTPS_PROXY": "http://127.0.0.1:7890",
			"NO_PROXY":    "localhost,127.0.0.1,::1,internal.example.com",
		},
	)

	// GitHub 下载请求走系统代理。
	req, _ := http.NewRequest(http.MethodGet, "https://github.com/x/y.zip", nil)
	got, err := updateTransportProxy(req)
	if err != nil {
		t.Fatalf("updateTransportProxy: %v", err)
	}
	if got == nil || got.Host != "127.0.0.1:7890" {
		t.Errorf("proxy = %v, want system proxy 127.0.0.1:7890", got)
	}

	// NO_PROXY 命中的地址直连（系统例外列表生效）。
	reqDirect, _ := http.NewRequest(http.MethodGet, "https://internal.example.com/x", nil)
	gotDirect, err := updateTransportProxy(reqDirect)
	if err != nil {
		t.Fatalf("updateTransportProxy(no_proxy host): %v", err)
	}
	if gotDirect != nil {
		t.Errorf("proxy = %v, want nil (no_proxy bypass)", gotDirect)
	}
}

func TestUpdateTransportProxy_NoProxyAnywhere(t *testing.T) {
	stubProxyFuncs(t,
		func(*http.Request) (*url.URL, error) { return nil, nil },
		nil,
	)

	req, _ := http.NewRequest(http.MethodGet, "https://github.com/x/y.zip", nil)
	got, err := updateTransportProxy(req)
	if err != nil {
		t.Fatalf("updateTransportProxy: %v", err)
	}
	if got != nil {
		t.Errorf("proxy = %v, want nil (direct)", got)
	}
}

func TestNewService_UsesProxyAwareClient(t *testing.T) {
	s := NewService("1.0.0", nil)
	if s.httpClient == nil {
		t.Fatal("httpClient is nil")
	}
	client, ok := s.httpClient.(*http.Client)
	if !ok {
		t.Fatalf("httpClient = %T, want *http.Client", s.httpClient)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.Transport)
	}
	if tr.Proxy == nil {
		t.Error("transport Proxy is nil, want updateTransportProxy chain")
	}
}
