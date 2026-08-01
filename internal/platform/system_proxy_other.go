//go:build !darwin && !windows

package platform

// detectSystemProxy：Linux 等平台无统一系统代理注册表（桌面环境各异），
// 依赖用户在 shell 中配置的代理变量；GUI 启动场景暂不探测。
func detectSystemProxy() (host, port string, ok bool) {
	return "", "", false
}
