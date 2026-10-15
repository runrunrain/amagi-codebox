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
		return base, nil
	}
	out := make([]string, 0, len(base)+8)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !proxyKeys[strings.ToUpper(key)] {
			out = append(out, entry)
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		out = append(out, key+"="+address)
	}
	out = append(out, "NO_PROXY=localhost,127.0.0.1,::1", "no_proxy=localhost,127.0.0.1,::1")
	out = append(out, "AMAGI_CODEBOX_AI_PROXY=1")
	return out, nil
}

// PinProxyEnvironment re-anchors proxy variables after login-shell profiles.
// WSL's host loopback reachability is not assumed: fail before spawning there.
func PinProxyEnvironment(spec platform.ResolvedLaunchSpec) (platform.ResolvedLaunchSpec, error) {
	active := false
	for _, entry := range spec.Env.Variables {
		if entry == "AMAGI_CODEBOX_AI_PROXY=1" {
			active = true
			break
		}
	}
	if !active {
		return spec, nil
	}
	if spec.BootstrapMode == platform.BootstrapWSL {
		return spec, errors.New("AI 代理分流暂不支持 WSL 的宿主回环转发，请停用分流后启动 WSL 会话")
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
