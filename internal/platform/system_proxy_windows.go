//go:build windows

package platform

import (
	"os/exec"
	"strings"
)

// detectSystemProxy 读取 Windows Internet Settings 注册表代理配置：
//
//	HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings
//	  ProxyEnable    REG_DWORD  0x1
//	  ProxyServer    REG_SZ     host:port（或 "http=host:port;https=host:port" 分协议形式）
//	  ProxyOverride  REG_SZ     分号分隔的例外列表（如 "localhost;127.*;<local>"）
//
// 通过 `reg query` 读取，避免引入额外依赖；未启用或解析失败时 ok=false。
// 例外列表（ProxyOverride）一并返回，供 NO_PROXY 同步（<local> 控制标记丢弃）。
func detectSystemProxy() (host, port string, exceptions []string, ok bool) {
	out, err := exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable").Output()
	if err != nil || !parseRegDwordEnabled(string(out)) {
		return "", "", nil, false
	}
	out, err = exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyServer").Output()
	if err != nil {
		return "", "", nil, false
	}
	host, port, ok = parseRegProxyServer(string(out))
	if !ok {
		return "", "", nil, false
	}
	return host, port, readRegProxyOverride(), true
}

// readRegProxyOverride 读取并解析注册表 ProxyOverride 例外列表；
// 读取失败或未配置时返回 nil（例外同步是增强项，不阻断代理注入）。
func readRegProxyOverride() []string {
	out, err := exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyOverride").Output()
	if err != nil {
		return nil
	}
	return parseRegProxyOverride(string(out))
}

// parseRegProxyOverride 是 readRegProxyOverride 的纯函数内核（可测试）。
// 值为分号分隔（如 "localhost;127.*;10.*;<local>"）；<local> 是
// "绕过不含点号的裸主机名"控制标记，环境变量协议无对应语义，丢弃。
func parseRegProxyOverride(output string) []string {
	var value string
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "ProxyOverride") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			value = fields[len(fields)-1]
		}
	}
	var out []string
	for _, entry := range strings.Split(value, ";") {
		if entry = strings.TrimSpace(entry); entry != "" && !strings.EqualFold(entry, "<local>") {
			out = append(out, entry)
		}
	}
	return out
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
