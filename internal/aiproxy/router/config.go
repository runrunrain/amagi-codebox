package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"amagi-codebox/internal/aiproxy/subscription"
)

type Settings struct {
	ProxyPort     int      `json:"proxy_port"`
	ClaudePort    int      `json:"claude_port"`
	OpenAIPort    int      `json:"openai_port"`
	ClaudeNode    string   `json:"claude_node"`
	OpenAINode    string   `json:"openai_node"`
	ClaudeDomains []string `json:"claude_domains"`
	OpenAIDomains []string `json:"openai_domains"`
	FallbackProxy string   `json:"fallback_proxy"`
	FetchProxy    string   `json:"fetch_proxy"`
}

func Defaults() Settings {
	return Settings{ProxyPort: 18890, ClaudePort: 18892, OpenAIPort: 18893,
		ClaudeDomains: []string{"anthropic.com", "claude.ai", "claude.com", "claudeusercontent.com"},
		OpenAIDomains: []string{"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com"},
		FallbackProxy: ""}
}

func (s Settings) Validate() error {
	seen := map[int]bool{}
	for _, p := range s.Ports() {
		if p < 1024 || p > 65535 || seen[p] {
			return errors.New("代理端口应为互不相同的 1024–65535 端口")
		}
		seen[p] = true
	}
	for _, ds := range [][]string{s.ClaudeDomains, s.OpenAIDomains} {
		for _, d := range ds {
			if d == "" || d != strings.ToLower(d) || strings.ContainsAny(d, " /:*?#@\\\t\n") || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
				return fmt.Errorf("域名后缀 %q 无效，请填写小写域名", d)
			}
		}
	}
	for _, a := range s.ClaudeDomains {
		for _, b := range s.OpenAIDomains {
			if a == b || strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a) {
				return errors.New("Claude 与 OpenAI 域名规则重叠，请使用各自的专用入口")
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

func (s Settings) Ports() []int { return []int{s.ProxyPort, s.ClaudePort, s.OpenAIPort} }

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
	claude, ok := nodes[s.ClaudeNode]
	if !ok {
		return nil, errors.New("请为 Claude 选择有效节点")
	}
	openai, ok := nodes[s.OpenAINode]
	if !ok {
		return nil, errors.New("请为 OpenAI 选择有效节点")
	}
	outbounds := []map[string]any{{"type": "direct", "tag": "direct"}}
	for tag, node := range map[string]subscription.Node{"claude-out": claude, "openai-out": openai} {
		o := map[string]any{}
		for k, v := range node.Outbound {
			o[k] = v
		}
		o["tag"] = tag
		outbounds = append(outbounds, o)
	}
	final := "direct"
	if s.FallbackProxy != "" {
		o, _ := ProxyOutbound(s.FallbackProxy)
		o["tag"] = "fallback"
		outbounds = append(outbounds, o)
		final = "fallback"
	}
	rules := []map[string]any{
		{"inbound": []string{"claude-in"}, "action": "route", "outbound": "claude-out"},
		{"inbound": []string{"openai-in"}, "action": "route", "outbound": "openai-out"},
	}
	if len(s.ClaudeDomains) > 0 {
		rules = append(rules, map[string]any{"domain_suffix": s.ClaudeDomains, "action": "route", "outbound": "claude-out"})
	}
	if len(s.OpenAIDomains) > 0 {
		rules = append(rules, map[string]any{"domain_suffix": s.OpenAIDomains, "action": "route", "outbound": "openai-out"})
	}
	ins := []map[string]any{}
	for i, tag := range []string{"auto-in", "claude-in", "openai-in"} {
		ins = append(ins, map[string]any{"type": "mixed", "tag": tag, "listen": "127.0.0.1", "listen_port": s.Ports()[i]})
	}
	return json.MarshalIndent(map[string]any{
		"log":      map[string]any{"level": "info", "timestamp": true},
		"dns":      map[string]any{"servers": []map[string]any{{"type": "local", "tag": "local"}}},
		"inbounds": ins, "outbounds": outbounds,
		"route": map[string]any{"rules": rules, "final": final, "default_domain_resolver": "local"},
	}, "", "  ")
}
