//go:build windows

package platform

import (
	"os"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestSystemProxyToggleRoundTripWindows：真实注册表往返验证（默认跳过，设
// AMAGI_SYSTEM_PROXY_WRITE_TEST=1 显式开启）。先快照 Internet Settings 的
// ProxyEnable/ProxyServer/ProxyOverride，关闭→读、开启→读，最后无论成败都
// 恢复原值——测试期间系统代理短暂切换，仅应在受控环境手动运行。
func TestSystemProxyToggleRoundTripWindows(t *testing.T) {
	if os.Getenv("AMAGI_SYSTEM_PROXY_WRITE_TEST") != "1" {
		t.Skip("set AMAGI_SYSTEM_PROXY_WRITE_TEST=1 to run the real-registry round trip")
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKeyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("open for snapshot: %v", err)
	}
	origEnable, _, err := k.GetIntegerValue("ProxyEnable")
	origEnableOK := err == nil
	origServer, _, err := k.GetStringValue("ProxyServer")
	origServerOK := err == nil
	origOverride, _, err := k.GetStringValue("ProxyOverride")
	origOverrideOK := err == nil
	k.Close()
	t.Cleanup(func() {
		rk, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKeyPath, registry.SET_VALUE)
		if err != nil {
			t.Logf("cleanup: open: %v (manual restore needed)", err)
			return
		}
		defer rk.Close()
		if origEnableOK {
			_ = rk.SetDWordValue("ProxyEnable", uint32(origEnable))
		}
		if origServerOK {
			_ = rk.SetStringValue("ProxyServer", origServer)
		}
		if origOverrideOK {
			_ = rk.SetStringValue("ProxyOverride", origOverride)
		}
	})

	// 关闭：仅摘 ProxyEnable。
	if err := SetSystemProxyEnabled(false, "", 0); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if state := ReadSystemProxyControlState(); state.Enabled {
		t.Fatalf("after disable, Enabled = true, want false")
	}

	// 开启：写地址 + 启用；若原本无例外列表应补默认回环绕行。
	if err := SetSystemProxyEnabled(true, "127.0.0.1", 5800); err != nil {
		t.Fatalf("enable: %v", err)
	}
	state := ReadSystemProxyControlState()
	if !state.Enabled || state.Host != "127.0.0.1" || state.Port != "5800" {
		t.Fatalf("after enable, state = %+v, want enabled 127.0.0.1:5800", state)
	}

	vk, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKeyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("open for verify: %v", err)
	}
	defer vk.Close()
	if override, _, err := vk.GetStringValue("ProxyOverride"); err != nil || override == "" {
		t.Fatalf("ProxyOverride after enable = %q (err %v), want non-empty default bypass list", override, err)
	}
}
