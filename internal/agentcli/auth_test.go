package agentcli

// auth_test.go — 三重校验的精确边界回归（评审 D-09）：
//   - Origin：只要请求头存在 Origin（含空值）即 403——浏览器页面不可达，
//     用 Header map 的存在性判断，而非非空值判断；
//   - Host：host 部分必须是精确允许集合 127.0.0.1/localhost/[::1]（大小写
//     不敏感），端口必须显式携带且等于 listener 实际端口；127/8 其它地址、
//     无端口、端口不符一律 403。

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

// directGet 构造合法回环请求直接打内部 handler（可注入 Origin/Host）。
func directGet(t *testing.T, s *Server, ep spec.Endpoint, header map[string]string, rawHeader map[string][]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, ep.URL+spec.PathPing, nil)
	req.Header.Set(spec.HeaderToken, ep.CLIToken)
	req.RemoteAddr = "127.0.0.1:55555"
	req.Host = hostWithListenerPort(t, ep)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	for k, vs := range rawHeader {
		req.Header[k] = vs // 直接注入（保留空值，Set/Delete 无法表达）
	}
	rec := httptest.NewRecorder()
	s.buildHandler().ServeHTTP(rec, req)
	return rec
}

func hostWithListenerPort(t *testing.T, ep spec.Endpoint) string {
	t.Helper()
	u, err := url.Parse(ep.URL)
	if err != nil {
		t.Fatalf("parse endpoint url: %v", err)
	}
	return u.Host
}

func TestOriginHeaderPresenceRejectedIncludingEmpty(t *testing.T) {
	s, ep := startTestServer(t)
	cases := []struct {
		name string
		// rawHeader 直接注入 Header map（空值 Origin 必须保留为存在键）。
		raw   map[string][]string
		setKV map[string]string
	}{
		{"普通 Origin", nil, map[string]string{"Origin": "http://evil.example"}},
		{"null Origin", nil, map[string]string{"Origin": "null"}},
		{"空值 Origin", map[string][]string{"Origin": {""}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := directGet(t, s, ep, tc.setKV, tc.raw)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("带 Origin 头（含空值）: status=%d, want 403; body=%s", rec.Code, rec.Body.String())
			}
			var env spec.Envelope
			_ = json.Unmarshal(rec.Body.Bytes(), &env)
			if env.Error == nil || env.Error.Code != spec.ErrForbidden {
				t.Fatalf("错误码=%+v, want forbidden", env.Error)
			}
		})
	}
	// 无 Origin 头的合法请求仍放行。
	rec := directGet(t, s, ep, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("无 Origin: status=%d, want 200", rec.Code)
	}
}

func TestHostExactAllowSetAndPortMustMatchListener(t *testing.T) {
	s, ep := startTestServer(t)
	u, err := url.Parse(ep.URL)
	if err != nil {
		t.Fatal(err)
	}
	port := u.Port()

	forbidden := map[string]string{
		"127/8 其它地址":  "127.0.0.2:" + port,
		"127.1 短形态":   "127.1:" + port,
		"前缀伪装":       "localhost.evil.com:" + port,
		"IPv6 其它回环段": "[::2]:" + port,
		"localhost 无端口": "localhost",
		"127.0.0.1 无端口": "127.0.0.1",
		"[::1] 无端口":   "[::1]",
		"端口不符":       "localhost:1",
		"端口前缀拼接":     "127.0.0.1:" + port + "1",
		"外部主机名":      "evil.example.com:" + port,
	}
	for name, host := range forbidden {
		t.Run("拒绝 "+name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, ep.URL+spec.PathPing, nil)
			req.Header.Set(spec.HeaderToken, ep.CLIToken)
			req.RemoteAddr = "127.0.0.1:55555"
			req.Host = host
			rec := httptest.NewRecorder()
			s.buildHandler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("Host=%q: status=%d, want 403", host, rec.Code)
			}
		})
	}
	allowed := map[string]string{
		"IPv4 带端口":    "127.0.0.1:" + port,
		"localhost 带端口": "localhost:" + port,
		"大小写不敏感":     "LOCALHOST:" + port,
		"IPv6 带端口":    "[::1]:" + port,
	}
	for name, host := range allowed {
		t.Run("放行 "+name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, ep.URL+spec.PathPing, nil)
			req.Header.Set(spec.HeaderToken, ep.CLIToken)
			req.RemoteAddr = "127.0.0.1:55555"
			req.Host = host
			rec := httptest.NewRecorder()
			s.buildHandler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("Host=%q: status=%d, want 200; body=%s", host, rec.Code, rec.Body.String())
			}
		})
	}
	// 端口语义锚定：Host 的端口取自 listener 实际端口（net.SplitHostPort 解析）。
	if _, p, err := net.SplitHostPort(u.Host); err != nil || p == "" {
		t.Fatalf("测试前提：endpoint URL 应携带监听端口, url=%q err=%v", ep.URL, err)
	}
}
