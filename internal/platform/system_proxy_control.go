package platform

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// system_proxy_control.go：系统级显式代理开关（「全局设备显式代理」）的跨平台
// 门面。读取侧复用各 OS 的 detectSystemProxy（Windows Internet Settings 注册表
// / macOS scutil）；写入侧由各平台 system_proxy_control_<os>.go 提供
// setSystemProxyEnabled 实现，未实现平台返回 ErrSystemProxyControlUnsupported。
//
// 语义对齐主流代理客户端（v2rayN/Clash 等）：
//   - 开启 = 写入地址（host:port）并置启用位（Windows ProxyServer+ProxyEnable；
//     macOS 对应 networksetup，暂未实现）；
//   - 关闭 = 仅摘启用位，保留地址与例外列表——下次开启免重填，也不破坏用户
//     手工配置的 ProxyOverride；
//   - 变更后广播系统配置通知（WinINet InternetSetOption），已运行的应用
//     （浏览器等）无需重新登录即可感知。
//
// 该开关只影响「显式系统代理」（WinINet/系统设置层），不改路由/TUN，也不注入
// 本进程环境变量——CLI 会话的代理注入仍由 SystemProxyEnv 按探测结果独立决定。

// ErrSystemProxyControlUnsupported 表示当前平台未实现系统代理写入。
var ErrSystemProxyControlUnsupported = fmt.Errorf("system proxy control is not supported on this platform")

// SystemProxyControlState 是系统显式代理的实时快照（不做可达性探测）。
type SystemProxyControlState struct {
	// Supported 当前平台是否支持写入开关（决定 UI 是否展示）。
	Supported bool
	// Enabled 系统显式代理当前是否启用（Windows ProxyEnable / macOS HTTP(S)Enable）。
	Enabled bool
	// Host/Port 当前生效地址；未启用且从未配置过时为空。
	Host string
	Port string
}

// ReadSystemProxyControlState 读取系统显式代理实时状态。
func ReadSystemProxyControlState() SystemProxyControlState {
	host, port, _, ok := detectSystemProxy()
	return SystemProxyControlState{
		Supported: systemProxyControlSupported(),
		Enabled:   ok,
		Host:      host,
		Port:      port,
	}
}

// SetSystemProxyEnabled 开启/关闭系统显式代理。开启且 host/port 有效时同步写入
// 地址；开启而地址为空时沿用系统现有地址（完全没有则报错，由调用方引导先配置）。
// 关闭只摘启用位不动地址。
func SetSystemProxyEnabled(enable bool, host string, port int) error {
	if !systemProxyControlSupported() {
		return ErrSystemProxyControlUnsupported
	}
	return setSystemProxyEnabled(enable, host, port)
}

// ProbeProxyEndpoint 对代理端点做 TCP+HTTP 双重可达性探测（复用 SystemProxyEnv
// 的注入保护探测），供 UI 展示「代理进程是否在跑」：端口被监听不代表能转发，
// HTTP 探测可识别「活端口、死代理」。
func ProbeProxyEndpoint(host string, port int) bool {
	p := strconv.Itoa(port)
	return proxyDialReachable(host, p) && proxyHTTPResponsive(host, p)
}

// NormalizeProxyEndpoint 规整并校验显式代理地址：去掉空白、误粘的 scheme 与
// 尾部斜杠；host 内嵌端口（"127.0.0.1:5800"）时自动拆出（与显式 port 不一致
// 报错，一致则拆开）；最终 host 非空、port ∈ [1,65535]。返回规整后的 (host, port)。
func NormalizeProxyEndpoint(host string, port int) (string, int, error) {
	host = strings.TrimSpace(host)
	for _, scheme := range []string{"http://", "https://", "socks5://", "HTTP://", "HTTPS://", "SOCKS5://"} {
		if strings.HasPrefix(host, scheme) {
			host = host[len(scheme):]
			break
		}
	}
	host = strings.TrimSuffix(host, "/")
	host = strings.TrimSpace(host)
	if h, p, splitErr := net.SplitHostPort(host); splitErr == nil {
		embedded, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, fmt.Errorf("invalid proxy address %q: cannot parse embedded port", host)
		}
		if port != 0 && port != embedded {
			return "", 0, fmt.Errorf("proxy host %q contains port %d which conflicts with port %d; keep the port only in the port field", host, embedded, port)
		}
		host, port = h, embedded
	}
	if host == "" {
		return "", 0, fmt.Errorf("proxy host is empty")
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("proxy port %d out of valid range [1, 65535]", port)
	}
	return host, port, nil
}
