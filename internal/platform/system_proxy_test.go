//go:build darwin

package platform

import "testing"

// TestParseScutilProxy 覆盖 scutil --proxy 输出解析：HTTPS 优先、HTTP 回退、
// 未启用返回 false。
func TestParseScutilProxy(t *testing.T) {
	cases := []struct {
		name string
		in   string
		host string
		port string
		ok   bool
	}{
		{
			name: "https enabled",
			in: "ExceptionsList : <array> {\n  0 : *.local\n}\n" +
				"HTTPEnable : 1\nHTTPPort : 5800\nHTTPProxy : 127.0.0.1\n" +
				"HTTPSEnable : 1\nHTTPSPort : 5800\nHTTPSProxy : 127.0.0.1\n" +
				"SOCKSEnable : 1\nSOCKSPort : 5800\nSOCKSProxy : 127.0.0.1\n",
			host: "127.0.0.1", port: "5800", ok: true,
		},
		{
			name: "http only fallback",
			in:   "HTTPEnable : 1\nHTTPPort : 7890\nHTTPProxy : 10.0.0.2\nHTTPSEnable : 0\n",
			host: "10.0.0.2", port: "7890", ok: true,
		},
		{
			name: "disabled",
			in:   "HTTPEnable : 0\nHTTPSEnable : 0\nSOCKSEnable : 1\nSOCKSPort : 1080\nSOCKSProxy : 127.0.0.1\n",
			ok:   false,
		},
		{
			name: "empty",
			in:   "",
			ok:   false,
		},
	}
	for _, c := range cases {
		host, port, ok := parseScutilProxy(c.in)
		if ok != c.ok || host != c.host || port != c.port {
			t.Errorf("%s: parseScutilProxy = (%q,%q,%v), want (%q,%q,%v)",
				c.name, host, port, ok, c.host, c.port, c.ok)
		}
	}
}

// TestHasProxyEnv 覆盖代理键检测（含小写与 ALL_PROXY/NO_PROXY）。
func TestHasProxyEnv(t *testing.T) {
	if HasProxyEnv([]string{"PATH=/usr/bin", "HOME=/x"}) {
		t.Error("no proxy keys -> want false")
	}
	if !HasProxyEnv([]string{"https_proxy=http://127.0.0.1:5800"}) {
		t.Error("lowercase https_proxy -> want true")
	}
	if !HasProxyEnv([]string{"ALL_PROXY=socks5://127.0.0.1:1080"}) {
		t.Error("ALL_PROXY -> want true")
	}
	if !HasProxyEnv([]string{"NO_PROXY=localhost"}) {
		t.Error("NO_PROXY -> want true")
	}
}
