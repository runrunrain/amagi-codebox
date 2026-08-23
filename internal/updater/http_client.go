package updater

// http_client.go：更新器专用 HTTP 客户端。
//
// 背景：codebox 以 GUI 方式启动时不经过用户 shell，进程环境通常没有
// HTTP_PROXY/HTTPS_PROXY；而 Go 的 http.DefaultTransport 只读进程环境变量
// （ProxyFromEnvironment），看不到 Windows Internet Settings / macOS 系统
// 代理设置。结果是更新检查与更新包下载直连 github.com，在必须走代理的环境
// 里表现为 dial tcp ... connectex 超时（高频安装失败）。
//
// 策略：Transport.Proxy 逐级回退——
//  1. 进程环境变量（http.ProxyFromEnvironment）：用户显式配置优先；
//  2. 操作系统系统代理（platform.SystemProxyEnv，含启用检测 + 可达性探测，
//     系统代理未启用或代理 App 已关闭时返回 nil，回退直连）。
//
// 系统代理解析结果用 sync.OnceValues 缓存：SystemProxyEnv 每次调用会做
// TCP/HTTP 可达性探测，不宜逐请求执行；代理开关变化在进程重启后生效，
// 与 launcher 注入会话环境变量的行为一致（会话启动时快照）。

import (
	"net/http"
	"net/url"
	"sync"

	"amagi-codebox/internal/platform"

	"golang.org/x/net/http/httpproxy"
)

// detectSystemProxyEnv 可替换（测试注入）。默认实现探测操作系统系统代理。
var detectSystemProxyEnv = platform.SystemProxyEnv

// envProxyFunc 可替换（测试注入）。默认实现即 http.ProxyFromEnvironment
// （其内部按进程缓存首次读取的环境变量，测试中需用注入规避缓存污染）。
var envProxyFunc = http.ProxyFromEnvironment

// systemProxyFunc 惰性解析系统代理并缓存（OnceValue：探测只做一次）。
// 返回 nil 表示系统代理不可用，请求直连。
var systemProxyFunc = sync.OnceValue(buildSystemProxyFunc)

func buildSystemProxyFunc() func(*http.Request) (*url.URL, error) {
	env := detectSystemProxyEnv()
	if env == nil {
		return nil
	}
	cfg := &httpproxy.Config{
		HTTPProxy:  firstNonEmpty(env["HTTP_PROXY"], env["http_proxy"]),
		HTTPSProxy: firstNonEmpty(env["HTTPS_PROXY"], env["https_proxy"]),
		NoProxy:    firstNonEmpty(env["NO_PROXY"], env["no_proxy"]),
	}
	proxyForURL := cfg.ProxyFunc()
	return func(req *http.Request) (*url.URL, error) {
		return proxyForURL(req.URL)
	}
}

// updateTransportProxy 是更新器 Transport 的 Proxy 函数：
// 环境变量代理优先，缺失时回退系统代理，两者皆无则直连。
func updateTransportProxy(req *http.Request) (*url.URL, error) {
	if proxyURL, err := envProxyFunc(req); proxyURL != nil || err != nil {
		return proxyURL, err
	}
	if fn := systemProxyFunc(); fn != nil {
		return fn(req)
	}
	return nil, nil
}

// newUpdateHTTPClient 构造更新器专用客户端：克隆 DefaultTransport（保留其
// 超时/连接池参数），仅替换 Proxy 解析链。
func newUpdateHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = updateTransportProxy
	return &http.Client{Transport: transport}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
