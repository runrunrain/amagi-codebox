package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"amagi-codebox/internal/aiproxy/subscription"
)

// Rules are evaluated in order. Port optionally exposes a dedicated entrypoint.
type Rule struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Domains []string `json:"domains"`
	Mode    string   `json:"mode"`
	Node    string   `json:"node"`
	Port    int      `json:"port"`
}
type Settings struct {
	ProxyPort     int    `json:"proxy_port"`
	Rules         []Rule `json:"rules"`
	FallbackProxy string `json:"fallback_proxy"`
	FetchProxy    string `json:"fetch_proxy"`
}

func Defaults() Settings {
	return Settings{ProxyPort: 18890, Rules: []Rule{
		{ID: "claude", Name: "Claude", Enabled: true, Mode: "proxy", Port: 18892, Domains: []string{"anthropic.com", "claude.ai", "claude.com", "claudeusercontent.com"}},
		{ID: "openai", Name: "OpenAI", Enabled: true, Mode: "proxy", Port: 18893, Domains: []string{"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com"}},
	}}
}

// Clone prevents DTOs and saved drafts from mutating active routing.
func (s Settings) Clone() Settings {
	s.Rules = append([]Rule{}, s.Rules...)
	for i := range s.Rules {
		s.Rules[i].Domains = append([]string{}, s.Rules[i].Domains...)
	}
	return s
}

var ruleID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var domainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validDomain(d string) bool {
	if len(d) == 0 || len(d) > 253 {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if !domainLabel.MatchString(label) {
			return false
		}
	}
	return true
}
func (s Settings) Validate() error {
	seen := map[int]bool{}
	for _, p := range s.Ports() {
		if p < 1024 || p > 65535 || seen[p] {
			return errors.New("已启用的代理入口应使用互不相同的 1024–65535 端口")
		}
		seen[p] = true
	}
	ids := map[string]bool{}
	for _, rule := range s.Rules {
		if !ruleID.MatchString(rule.ID) || ids[rule.ID] {
			return errors.New("规则 ID 无效或重复，请重新添加该规则")
		}
		ids[rule.ID] = true
		if strings.TrimSpace(rule.Name) == "" || utf8.RuneCountInString(rule.Name) > 80 {
			return errors.New("规则名称应为 1–80 个字符")
		}
		if rule.Mode != "proxy" && rule.Mode != "direct" {
			return fmt.Errorf("规则 %q 的出口方式应为代理或直连", rule.Name)
		}
		if rule.Port != 0 && (rule.Port < 1024 || rule.Port > 65535) {
			return fmt.Errorf("规则 %q 的专用端口应留空或使用 1024–65535 端口", rule.Name)
		}
		if rule.Enabled && len(rule.Domains) == 0 && rule.Port == 0 {
			return fmt.Errorf("规则 %q 至少需要匹配域名或专用端口", rule.Name)
		}
		for _, d := range rule.Domains {
			if !validDomain(d) {
				return fmt.Errorf("规则 %q 的域名 %q 无效，请填写小写域名，不含协议、路径或通配符", rule.Name, d)
			}
		}
	}
	for _, raw := range []string{s.FallbackProxy, s.FetchProxy} {
		if raw == "" {
			continue
		}
		if _, err := ProxyOutbound(raw); err != nil {
			return err
		}
		u, _ := url.Parse(raw)
		if u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback() {
			p, _ := strconv.Atoi(u.Port())
			if seen[p] {
				return errors.New("上游/订阅下载代理不能指向应用自身入口，否则会循环")
			}
		}
	}
	return nil
}
func (s Settings) Ports() []int {
	ports := []int{s.ProxyPort}
	for _, rule := range s.Rules {
		if rule.Enabled && rule.Port != 0 {
			ports = append(ports, rule.Port)
		}
	}
	return ports
}

func ProxyOutbound(raw string) (map[string]any, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("上游代理 URL 无效")
	}
	t := u.Scheme
	if t == "socks5" || t == "socks5h" {
		t = "socks"
	}
	if t != "http" && t != "socks" {
		return nil, errors.New("上游代理仅支持 http://、socks5://、socks5h://")
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil || p < 1 || p > 65535 || u.Hostname() == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("上游代理 URL 需要有效主机和显式端口")
	}
	o := map[string]any{"type": t, "server": u.Hostname(), "server_port": p}
	if t == "socks" {
		o["version"] = "5"
	}
	if u.User != nil {
		o["username"] = u.User.Username()
		pass, _ := u.User.Password()
		o["password"] = pass
	}
	return o, nil
}

func Build(s Settings, nodes map[string]subscription.Node) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	outbounds := []map[string]any{{"type": "direct", "tag": "direct"}}
	inbounds := []map[string]any{{"type": "mixed", "tag": "auto-in", "listen": "127.0.0.1", "listen_port": s.ProxyPort}}
	dedicated, domains := []map[string]any{}, []map[string]any{}
	for _, rule := range s.Rules {
		if !rule.Enabled {
			continue
		}
		target := "direct"
		if rule.Mode == "proxy" {
			node, ok := nodes[rule.Node]
			if !ok {
				return nil, fmt.Errorf("请为规则 %q 选择有效节点", rule.Name)
			}
			target = "rule-" + rule.ID + "-out"
			o := map[string]any{}
			for k, v := range node.Outbound {
				o[k] = v
			}
			o["tag"] = target
			outbounds = append(outbounds, o)
		}
		if rule.Port != 0 {
			tag := "rule-" + rule.ID + "-in"
			inbounds = append(inbounds, map[string]any{"type": "mixed", "tag": tag, "listen": "127.0.0.1", "listen_port": rule.Port})
			dedicated = append(dedicated, map[string]any{"inbound": []string{tag}, "action": "route", "outbound": target})
		}
		if len(rule.Domains) > 0 {
			domains = append(domains, map[string]any{"domain_suffix": rule.Domains, "action": "route", "outbound": target})
		}
	}
	final := "direct"
	if s.FallbackProxy != "" {
		o, _ := ProxyOutbound(s.FallbackProxy)
		o["tag"] = "fallback"
		outbounds = append(outbounds, o)
		final = "fallback"
	}
	// Dedicated entrypoints precede domain rules; unified requests follow
	// the user's rule order, including explicit direct rules.
	rules := append(dedicated, domains...)
	return json.MarshalIndent(map[string]any{
		"log":      map[string]any{"level": "info", "timestamp": true},
		"dns":      map[string]any{"servers": []map[string]any{{"type": "local", "tag": "local"}}},
		"inbounds": inbounds, "outbounds": outbounds,
		"route": map[string]any{"rules": rules, "final": final, "default_domain_resolver": "local"},
	}, "", "  ")
}
