//go:build darwin

package platform

// macOS 的系统代理写入需经 networksetup 按网络服务逐个设置（-setwebproxy/
// -setsecurewebproxy/-setwebproxystate），涉及服务枚举与权限交互，暂未实现。
// 读取侧（scutil --proxy）仍可用；UI 依赖 SystemProxyControlSupported 隐藏开关。
func systemProxyControlSupported() bool { return false }

func setSystemProxyEnabled(enable bool, host string, port int) error {
	return ErrSystemProxyControlUnsupported
}
