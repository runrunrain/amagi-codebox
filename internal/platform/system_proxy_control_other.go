//go:build !darwin && !windows

package platform

// Linux 等平台无统一系统代理注册表（桌面环境各异），与 detectSystemProxy 的
// 取舍一致：不探测也不写入。
func systemProxyControlSupported() bool { return false }

func setSystemProxyEnabled(enable bool, host string, port int) error {
	return ErrSystemProxyControlUnsupported
}
