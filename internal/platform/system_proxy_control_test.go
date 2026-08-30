package platform

import "testing"

// TestNormalizeProxyEndpoint 规整与校验：去空白/scheme/尾斜杠、内嵌端口拆分
// 与冲突检测、范围检查。
func TestNormalizeProxyEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		port     int
		wantHost string
		wantPort int
		wantErr  string
	}{
		{"plain", "127.0.0.1", 5800, "127.0.0.1", 5800, ""},
		{"trims space and slash", "  127.0.0.1/ ", 5800, "127.0.0.1", 5800, ""},
		{"strips http scheme", "http://127.0.0.1", 5800, "127.0.0.1", 5800, ""},
		{"strips https scheme", "https://proxy.lan", 7890, "proxy.lan", 7890, ""},
		{"strips socks5 scheme", "socks5://127.0.0.1", 1080, "127.0.0.1", 1080, ""},
		{"embedded port adopted", "127.0.0.1:1080", 0, "127.0.0.1", 1080, ""},
		{"embedded port consistent", "127.0.0.1:1080", 1080, "127.0.0.1", 1080, ""},
		{"embedded port conflict", "127.0.0.1:1080", 7890, "", 0, "conflicts"},
		{"embedded port non-numeric", "127.0.0.1:abc", 1080, "", 0, "cannot parse embedded port"},
		{"ipv6 bracketed", "[::1]:5800", 0, "::1", 5800, ""},
		{"empty host", "   ", 5800, "", 0, "host is empty"},
		{"port zero", "127.0.0.1", 0, "", 0, "out of valid range"},
		{"port negative", "127.0.0.1", -1, "", 0, "out of valid range"},
		{"port too large", "127.0.0.1", 65536, "", 0, "out of valid range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, port, err := NormalizeProxyEndpoint(tc.host, tc.port)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("NormalizeProxyEndpoint(%q, %d) error = %v, want nil", tc.host, tc.port, err)
				}
				if host != tc.wantHost || port != tc.wantPort {
					t.Fatalf("NormalizeProxyEndpoint(%q, %d) = (%q, %d), want (%q, %d)",
						tc.host, tc.port, host, port, tc.wantHost, tc.wantPort)
				}
				return
			}
			if err == nil {
				t.Fatalf("NormalizeProxyEndpoint(%q, %d) = nil error, want containing %q", tc.host, tc.port, tc.wantErr)
			}
			if want := tc.wantErr; !contains(err.Error(), want) {
				t.Fatalf("error = %q, want containing %q", err.Error(), want)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestProbeProxyEndpointRequiresBothLayers：TCP 与 HTTP 双探测都通过才判定可达
// （识别"活端口、死代理"）。
func TestProbeProxyEndpointRequiresBothLayers(t *testing.T) {
	origDial, origHTTP := proxyDialReachable, proxyHTTPResponsive
	t.Cleanup(func() {
		proxyDialReachable, proxyHTTPResponsive = origDial, origHTTP
	})

	proxyDialReachable = func(host, port string) bool { return false }
	proxyHTTPResponsive = func(host, port string) bool { return true }
	if ProbeProxyEndpoint("127.0.0.1", 5800) {
		t.Fatal("probe should fail when TCP dial fails even if HTTP layer reports ok")
	}

	proxyDialReachable = func(host, port string) bool { return true }
	proxyHTTPResponsive = func(host, port string) bool { return false }
	if ProbeProxyEndpoint("127.0.0.1", 5800) {
		t.Fatal("probe should fail when HTTP layer unresponsive even if TCP dial succeeds")
	}

	proxyHTTPResponsive = func(host, port string) bool { return true }
	if !ProbeProxyEndpoint("127.0.0.1", 5800) {
		t.Fatal("probe should succeed when both layers pass")
	}
}

// TestSetSystemProxyEnabledUnsupportedPlatformConsistency：不支持平台必须返回
// ErrSystemProxyControlUnsupported（支持平台跳过——真实写注册表的路径由
// AMAGI_SYSTEM_PROXY_WRITE_TEST 显式开启的 Windows 测试覆盖）。
func TestSetSystemProxyEnabledUnsupportedPlatformConsistency(t *testing.T) {
	if systemProxyControlSupported() {
		t.Skip("platform supports system proxy control; unsupported-path check not applicable")
	}
	if err := SetSystemProxyEnabled(true, "127.0.0.1", 5800); err != ErrSystemProxyControlUnsupported {
		t.Fatalf("SetSystemProxyEnabled on unsupported platform = %v, want ErrSystemProxyControlUnsupported", err)
	}
}
