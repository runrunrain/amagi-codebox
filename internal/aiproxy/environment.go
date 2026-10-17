package aiproxy

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"amagi-codebox/internal/platform"
)

var proxyKeys = map[string]bool{"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true}

// aiProxyEnvMarker 是 CodeBox 内部激活标记键：ApplyEnv 启用路径追加它，
// PinProxyEnvironment 以「spec.Env 含该标记=1」判定代理已激活。停用路径必须
// 剥离继承自进程环境的同名键（D4）——嵌套 CodeBox（宿主 CLI 会话本身被
// CodeBox 代理）启动时，旧 marker 会随 MergeWithSystem 流入子 CodeBox 的
// 会话 env，使已停用代理的 WSL 会话仍被 NAT 文案拒绝，且文案指引（「停用
// AI 代理分流」）按提示操作无效。
const aiProxyEnvMarker = "AMAGI_CODEBOX_AI_PROXY"

// stripAIProxyEnvMarker 移除 env 中的内部激活标记，其余条目（含用户自设的
// 普通代理变量）原样保留。
func stripAIProxyEnvMarker(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key == aiProxyEnvMarker {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (s *Service) proxyURLLocked(providers []Provider) (string, error) {
	if !s.loaded {
		return "", errors.New("AI 代理策略尚未加载，请等待应用初始化完成")
	}
	if !s.data.Enabled {
		return "", nil
	}
	if s.loadErr != "" {
		return "", errors.New(s.loadErr)
	}
	if s.manager == nil || !s.manager.Status().Running {
		return "", errors.New("AI 代理分流已启用但内核未运行，请在 AI 代理分流页启动或停用")
	}
	// A new launch/request must not silently use an old domain after its API
	// provider was changed or removed. Existing CLI connections need reapplying.
	resolved, issues := resolveProviders(s.active, providers)
	if len(issues) > 0 {
		return "", errors.New(strings.Join(issues, "；"))
	}
	for i, rule := range s.active.Routing.Rules {
		if rule.Enabled && rule.Provider != "" && strings.Join(rule.Domains, "\n") != strings.Join(resolved.Routing.Rules[i].Domains, "\n") {
			return "", errors.New("服务提供商 API 地址已变更，请在 AI 代理分流页应用更改")
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", s.active.Routing.ProxyPort), nil
}

// ApplyEnv runs after user/system environment merging. Enabled routing owns all
// proxy keys so stale ALL_PROXY or NO_PROXY=* cannot defeat the chosen exits.
// Requests to local relays and Headroom still bypass the forward proxy.
func (s *Service) ApplyEnv(base []string) ([]string, error) {
	providers := s.providers()
	s.mu.Lock()
	defer s.mu.Unlock()
	address, err := s.proxyURLLocked(providers)
	if err != nil {
		return nil, err
	}
	if address == "" {
		// D4：停用路径不得继承进程环境里的内部标记（嵌套 CodeBox 场景旧
		// marker 残留会让 PinProxyEnvironment 误判激活，WSL 会话被 NAT 文案
		// 拒绝）；用户的普通代理变量语义保持原样透传。
		return stripAIProxyEnvMarker(base), nil
	}
	out := make([]string, 0, len(base)+8)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		// 启用路径由本函数唯一重建 marker（先剔旧值再追加新值，D4）；代理键
		// 由启用路由整体接管（既有语义）。
		if !proxyKeys[strings.ToUpper(key)] && key != aiProxyEnvMarker {
			out = append(out, entry)
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		out = append(out, key+"="+address)
	}
	out = append(out, "NO_PROXY=localhost,127.0.0.1,::1", "no_proxy=localhost,127.0.0.1,::1")
	out = append(out, aiProxyEnvMarker+"=1")
	return out, nil
}

// PinProxyEnvironment re-anchors proxy variables after login-shell profiles.
// WSL sessions are only admitted under WSL2 mirrored networking: NAT (the
// default) leaves the host loopback unreachable from the distro, so the
// injected 127.0.0.1 entry would point at the distro itself. Under mirrored
// mode localhost is bidirectional and the WSLENV-forwarded proxy vars reach
// the Windows-side entry as-is.
func PinProxyEnvironment(spec platform.ResolvedLaunchSpec) (platform.ResolvedLaunchSpec, error) {
	active := false
	for _, entry := range spec.Env.Variables {
		if entry == aiProxyEnvMarker+"=1" {
			active = true
			break
		}
	}
	if !active {
		return spec, nil
	}
	if spec.BootstrapMode == platform.BootstrapWSL {
		// mirrored 下回环入口对 distro 可达，spec 照常携带统一分流入口（代理键
		// 经 WSLENV 转发跨界）；NAT/未知模式维持拒绝（空串按 NAT 对待，fail-safe）。
		if platform.WSLNetworkingMode(spec.Env.Variables) != "mirrored" {
			return spec, errors.New(`AI 代理分流的统一入口监听宿主回环（127.0.0.1），当前 WSL 为 NAT 网络模式，发行版内不可达。可在 %USERPROFILE%\.wslconfig 的 [wsl2] 段设置 networkingMode=mirrored，执行 wsl --shutdown 重启 WSL 后重试；或停用 AI 代理分流后启动 WSL 会话`)
		}
		return spec, nil
	}
	if spec.Shell == nil || (spec.BootstrapMode != platform.BootstrapShellInline && spec.BootstrapMode != platform.BootstrapShellAttach) {
		return spec, nil
	}
	switch strings.ToLower(spec.Shell.Key) {
	case "bash", "zsh", "sh", "fish", "":
	default:
		return spec, nil
	}
	parts := []string{"env"}
	for _, entry := range spec.Env.Variables {
		key, _, _ := strings.Cut(entry, "=")
		if proxyKeys[strings.ToUpper(key)] {
			parts = append(parts, "'"+strings.ReplaceAll(entry, "'", `'\''`)+"'")
		}
	}
	spec.StartupCommand = strings.Join(parts, " ") + " " + spec.StartupCommand
	return spec, nil
}

// ProxyForRequest adapts the same routing to CodeBox's own model requests.
// It never changes http.DefaultTransport or the parent process environment.
func (s *Service) ProxyForRequest(req *http.Request) (*url.URL, error) {
	providers := s.providers()
	s.mu.Lock()
	defer s.mu.Unlock()
	address, err := s.proxyURLLocked(providers)
	if err != nil {
		return nil, err
	}
	if address == "" {
		return http.ProxyFromEnvironment(req)
	}
	host := strings.ToLower(req.URL.Hostname())
	ip := net.ParseIP(host)
	if host == "localhost" || ip != nil && ip.IsLoopback() {
		return nil, nil
	}
	return url.Parse(address)
}
