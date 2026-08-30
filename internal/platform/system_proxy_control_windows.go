//go:build windows

package platform

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const internetSettingsKeyPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// defaultProxyOverride 是首次开启时若注册表尚无例外列表则补写的默认回环绕行
// （对齐 v2rayN 等客户端默认值），避免本机/内网地址被送进代理。已存在的
// ProxyOverride 一律不碰。
const defaultProxyOverride = "localhost;127.*;192.168.*;<local>"

func systemProxyControlSupported() bool { return true }

// setSystemProxyEnabled 写 HKCU Internet Settings：
//   - 开启：host:port 有效时写 ProxyServer；ProxyOverride 缺失时补默认；置
//     ProxyEnable=1。地址为空时要求现有 ProxyServer 可用（无则报错）。
//   - 关闭：仅置 ProxyEnable=0，保留 ProxyServer/ProxyOverride。
//
// 写入后 notifyWininetSettingsChanged 广播刷新。读取侧（detectSystemProxy）沿用
// reg query 实现；写入用 registry API 保证单键原子性与类型正确（REG_DWORD）。
func setSystemProxyEnabled(enable bool, host string, port int) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("open internet settings: %w", err)
	}
	defer k.Close()

	if enable {
		if host != "" && port > 0 {
			if err := k.SetStringValue("ProxyServer", fmt.Sprintf("%s:%d", host, port)); err != nil {
				return fmt.Errorf("write ProxyServer: %w", err)
			}
		} else if _, _, err := k.GetStringValue("ProxyServer"); err != nil {
			// 没有可写回的端点：宁可不开，避免启用一个空地址代理。
			return fmt.Errorf("proxy address is empty; configure host/port before enabling")
		}
		if _, _, err := k.GetStringValue("ProxyOverride"); err != nil {
			if err := k.SetStringValue("ProxyOverride", defaultProxyOverride); err != nil {
				return fmt.Errorf("write ProxyOverride: %w", err)
			}
		}
		if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
			return fmt.Errorf("write ProxyEnable: %w", err)
		}
	} else {
		if err := k.SetDWordValue("ProxyEnable", 0); err != nil {
			return fmt.Errorf("write ProxyEnable: %w", err)
		}
	}
	notifyWininetSettingsChanged()
	return nil
}

// notifyWininetSettingsChanged 广播 WinINet 配置变更（INTERNET_OPTION_SETTINGS_CHANGED
// + INTERNET_OPTION_REFRESH），让已运行的应用（浏览器/遵循系统代理的 GUI）无需
// 重新登录立即感知代理开关。通知失败不影响注册表写入结果（应用下次读取配置时
// 自然生效），故忽略返回值。
func notifyWininetSettingsChanged() {
	internetSetOption := windows.NewLazyDLL("wininet.dll").NewProc("InternetSetOptionW")
	const (
		internetOptionSettingsChanged = 39
		internetOptionRefresh         = 37
	)
	_, _, _ = internetSetOption.Call(0, internetOptionSettingsChanged, 0, 0)
	_, _, _ = internetSetOption.Call(0, internetOptionRefresh, 0, 0)
}
