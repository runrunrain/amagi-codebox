//go:build darwin

package platform

import (
	"os/exec"
	"strings"
)

// detectSystemProxy 解析 macOS `scutil --proxy` 输出，返回系统 HTTP(S) 代理。
// 优先 HTTPS 代理，其次 HTTP 代理；均未启用时 ok=false。
// SOCKS 代理不注入（undici EnvHttpProxyAgent 不支持 socks 协议）。
func detectSystemProxy() (host, port string, ok bool) {
	out, err := exec.Command("scutil", "--proxy").Output()
	if err != nil {
		return "", "", false
	}
	return parseScutilProxy(string(out))
}

// parseScutilProxy 是 detectSystemProxy 的纯函数内核（可测试）。
// scutil --proxy 输出形如：
//
//	ExceptionsList : <array> { ... }
//	HTTPEnable : 1
//	HTTPPort : 5800
//	HTTPProxy : 127.0.0.1
//	HTTPSEnable : 1
//	HTTPSPort : 5800
//	HTTPSProxy : 127.0.0.1
func parseScutilProxy(output string) (host, port string, ok bool) {
	fields := make(map[string]string, 8)
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), " : ")
		if !found {
			continue
		}
		fields[key] = strings.TrimSpace(value)
	}
	// HTTPS 优先：pi 等 CLI 的出站请求基本全是 https。
	if fields["HTTPSEnable"] == "1" && fields["HTTPSProxy"] != "" && fields["HTTPSPort"] != "" {
		return fields["HTTPSProxy"], fields["HTTPSPort"], true
	}
	if fields["HTTPEnable"] == "1" && fields["HTTPProxy"] != "" && fields["HTTPPort"] != "" {
		return fields["HTTPProxy"], fields["HTTPPort"], true
	}
	return "", "", false
}
