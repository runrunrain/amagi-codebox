//go:build windows

package platform

import (
	"os/exec"
	"strings"
)

// detectSystemProxy 读取 Windows Internet Settings 注册表代理配置：
//
//	HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings
//	  ProxyEnable  REG_DWORD  0x1
//	  ProxyServer  REG_SZ     host:port（或 "http=host:port;https=host:port" 分协议形式）
//
// 通过 `reg query` 读取，避免引入额外依赖；未启用或解析失败时 ok=false。
func detectSystemProxy() (host, port string, ok bool) {
	out, err := exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable").Output()
	if err != nil || !parseRegDwordEnabled(string(out)) {
		return "", "", false
	}
	out, err = exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyServer").Output()
	if err != nil {
		return "", "", false
	}
	return parseRegProxyServer(string(out))
}

// parseRegDwordEnabled 解析 reg query ProxyEnable 输出（0x1 为启用）。
func parseRegDwordEnabled(output string) bool {
	for _, field := range strings.Fields(output) {
		if field == "0x1" {
			return true
		}
	}
	return false
}

// parseRegProxyServer 解析 reg query ProxyServer 输出。
// 兼容两种值形式："host:port" 与 "http=host:port;https=host:port;..."（取 https 优先）。
func parseRegProxyServer(output string) (host, port string, ok bool) {
	var value string
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "ProxyServer") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			value = fields[len(fields)-1]
		}
	}
	if value == "" {
		return "", "", false
	}
	// 分协议形式：优先 https 段。
	if strings.Contains(value, "=") {
		best := ""
		for _, seg := range strings.Split(value, ";") {
			proto, addr, found := strings.Cut(strings.TrimSpace(seg), "=")
			if !found || addr == "" {
				continue
			}
			if strings.EqualFold(proto, "https") {
				best = addr
				break
			}
			if best == "" && strings.EqualFold(proto, "http") {
				best = addr
			}
		}
		value = best
	}
	h, p, found := strings.Cut(value, ":")
	if !found || h == "" || p == "" {
		return "", "", false
	}
	return h, p, true
}
