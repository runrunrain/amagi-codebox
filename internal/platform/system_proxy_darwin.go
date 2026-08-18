//go:build darwin

package platform

import (
	"os/exec"
	"strings"
)

// detectSystemProxy 解析 macOS `scutil --proxy` 输出，返回系统 HTTP(S) 代理及
// 例外列表（绕行规则）。优先 HTTPS 代理，其次 HTTP 代理；均未启用时 ok=false。
// SOCKS 代理不注入（undici EnvHttpProxyAgent 不支持 socks 协议）。
func detectSystemProxy() (host, port string, exceptions []string, ok bool) {
	out, err := exec.Command("scutil", "--proxy").Output()
	if err != nil {
		return "", "", nil, false
	}
	return parseScutilProxy(string(out))
}

// parseScutilProxy 是 detectSystemProxy 的纯函数内核（可测试）。
// scutil --proxy 输出形如：
//
//	ExceptionsList : <array> {
//	  0 : localhost;127.*;192.168.*
//	}
//	HTTPEnable : 1
//	HTTPPort : 5800
//	HTTPProxy : 127.0.0.1
//	HTTPSEnable : 1
//	HTTPSPort : 5800
//	HTTPSProxy : 127.0.0.1
func parseScutilProxy(output string) (host, port string, exceptions []string, ok bool) {
	fields := make(map[string]string, 8)
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), " : ")
		if !found {
			continue
		}
		fields[key] = strings.TrimSpace(value)
	}
	exceptions = parseScutilExceptions(output)
	// HTTPS 优先：pi 等 CLI 的出站请求基本全是 https。
	if fields["HTTPSEnable"] == "1" && fields["HTTPSProxy"] != "" && fields["HTTPSPort"] != "" {
		return fields["HTTPSProxy"], fields["HTTPSPort"], exceptions, true
	}
	if fields["HTTPEnable"] == "1" && fields["HTTPProxy"] != "" && fields["HTTPPort"] != "" {
		return fields["HTTPProxy"], fields["HTTPPort"], exceptions, true
	}
	return "", "", exceptions, false
}

// parseScutilExceptions 提取 scutil --proxy 输出中 ExceptionsList 数组的例外项。
// macOS 把整串例外存进单个数组元素（分号分隔），也兼容多元素形式；
// 元素行形如 "  0 : localhost;127.*"，冒号前是下标。数组块之外的行忽略。
func parseScutilExceptions(output string) []string {
	var out []string
	inArray := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inArray {
			if strings.HasPrefix(trimmed, "ExceptionsList") && strings.Contains(trimmed, "<array>") {
				inArray = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "}") {
			break // 数组结束
		}
		_, value, found := strings.Cut(trimmed, " : ")
		if !found {
			continue
		}
		for _, entry := range strings.Split(value, ";") {
			if entry = strings.TrimSpace(entry); entry != "" {
				out = append(out, entry)
			}
		}
	}
	return out
}
