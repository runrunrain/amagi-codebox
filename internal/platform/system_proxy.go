package platform

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// system_proxy.go：把操作系统级代理（macOS 系统设置 / Windows Internet Settings）
// 转换为进程环境变量（HTTP_PROXY/HTTPS_PROXY/NO_PROXY）。
//
// 背景：codebox 以 GUI 方式启动时不经过用户 shell，os.Environ 拿不到用户在
// shell rc 里配置的代理变量；Node 系 CLI（pi 等）访问 chatgpt.com 这类被墙
// 站点会报 "fetch failed"。macOS/Windows 的系统代理对 GUI 应用可见，故在启动
// CLI 会话时探测并注入。
//
// 保护（四重）：
//  1. 用户显式设置的代理变量（env/overrides 中已有的 HTTP_PROXY 等）优先，不覆盖；
//  2. 仅当系统代理启用时才注入；
//  3. 注入前做 TCP/HTTP 可达性探测（代理 App 关闭时 scutil 仍可能显示启用），
//     不可达则不注入，避免把原本直连可用的会话打进死代理；
//  4. NO_PROXY 同步系统例外列表（macOS ExceptionsList / Windows ProxyOverride），
//     保证「系统设置里加了例外直连的站点」在 CLI 会话里同样绕开代理，
//     避免 GUI 与终端行为分叉（内网域名被注入代理后报 503/ECONNRESET）。
//
// 注入时 NO_PROXY 以回环绕行（localhost/127.0.0.1/::1）打底：codebox 本地回环服务
// （headroom 代理 8787/8788、本地 relay）不能被外部代理接管；其上合并系统例外项。

// proxyDialTimeout 是系统代理可达性探测的超时。保持很小（本地代理应瞬时响应），
// 避免在每次会话启动时引入可感知延迟。
const proxyDialTimeout = 300 * time.Millisecond

// proxyHTTPProbeTimeout 是 HTTP 级代理健康探测的超时。部分代理 App 在异常
// 状态下 TCP 端口仍接受连接但不转发任何流量（"活端口、死代理"），仅靠 TCP
// 探测会把会话全部流量打进死代理，表现为 CLI 启动期网络操作长时间挂起。
const proxyHTTPProbeTimeout = 1500 * time.Millisecond

// proxyDialReachable 可替换（测试注入）。默认实现做 TCP 探测。
var proxyDialReachable = func(host, port string) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), proxyDialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// proxyHTTPResponsive 可替换（测试注入）。默认实现向代理监听端口直接发一个
// HTTP 请求：健康的 HTTP 代理（Surge/Clash/sing-box 等混合端口）会对 origin-form
// 请求快速返回 4xx/200 等任何 HTTP 响应；TCP 通了但内核/进程卡死不会应答，
// 超时即判定为不可用。
var proxyHTTPResponsive = func(host, port string) bool {
	client := &http.Client{Timeout: proxyHTTPProbeTimeout}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/")
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return true
}

// defaultNoProxy 是注入 NO_PROXY 的回环绕行打底清单；系统例外列表经
// mergeNoProxy 追加在其后（用户显式 NO_PROXY 仍然整体优先）。
const defaultNoProxy = "localhost,127.0.0.1,::1"

// SystemProxyEnv 返回系统代理对应的环境变量集；系统代理未启用、不支持的平台
// 或代理不可达时返回 nil。返回值含大写与小写键（不同 HTTP 客户端读取习惯不同）。
// NO_PROXY = defaultNoProxy + 系统代理例外列表（见 mergeNoProxy）。
func SystemProxyEnv() map[string]string {
	host, port, exceptions, ok := detectSystemProxy()
	if !ok {
		return nil
	}
	if !proxyDialReachable(host, port) {
		return nil
	}
	if !proxyHTTPResponsive(host, port) {
		return nil
	}
	proxyURL := fmt.Sprintf("http://%s", net.JoinHostPort(host, port))
	noProxy := mergeNoProxy(exceptions)
	return map[string]string{
		"HTTP_PROXY":  proxyURL,
		"HTTPS_PROXY": proxyURL,
		"http_proxy":  proxyURL,
		"https_proxy": proxyURL,
		"NO_PROXY":    noProxy,
		"no_proxy":    noProxy,
	}
}

// mergeNoProxy 合并回环绕行清单与系统代理例外列表：defaultNoProxy 打底，
// 例外项去重追加。注意 macOS/Windows 例外可能含前缀通配（127.*、192.168.*），
// 这不是标准 no_proxy 后缀语法，注入后无害（永远不会匹配成域名后缀），
// 实际绕行主要靠其中的域名项（如 *.vx.net / router.ai.vx.net）。
func mergeNoProxy(exceptions []string) string {
	seen := make(map[string]struct{}, len(exceptions)+4)
	out := make([]string, 0, len(exceptions)+4)
	add := func(entry string) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return
		}
		if _, dup := seen[entry]; dup {
			return
		}
		seen[entry] = struct{}{}
		out = append(out, entry)
	}
	for _, base := range strings.Split(defaultNoProxy, ",") {
		add(base)
	}
	for _, entry := range exceptions {
		add(entry)
	}
	return strings.Join(out, ",")
}

// proxyEnvKeys 是 SystemProxyEnv 注入的全部键（含大小写）。launcher.BuildEnv
// 据此判断用户是否已显式配置代理（存在任意一个即视为已配置）。
var proxyEnvKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy",
}

// HasProxyEnv 判断环境变量列表中是否已存在代理相关键（Windows 大小写不敏感）。
func HasProxyEnv(env []string) bool {
	caseInsensitive := currentOS() == "windows"
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		for _, pk := range proxyEnvKeys {
			if key == pk || (caseInsensitive && strings.EqualFold(key, pk)) {
				return true
			}
		}
	}
	return false
}
