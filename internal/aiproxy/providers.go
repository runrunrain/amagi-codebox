package aiproxy

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"

	"amagi-codebox/internal/aiproxy/router"
	"amagi-codebox/internal/config"
	"golang.org/x/net/idna"
)

// Provider contains routing metadata only. Never expose API URLs, headers or keys.
type Provider struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Domains  []string `json:"domains"`
	Formats  []string `json:"formats"`
	Warnings []string `json:"warnings"`
}

func ProvidersFromConfig(providers map[string]config.Provider) []Provider {
	result := []Provider{}
	for name, p := range providers {
		view := Provider{ID: name, Name: name, Domains: []string{}, Formats: []string{}, Warnings: []string{}}
		formats := []string{}
		if p.Anthropic != nil && p.Anthropic.Enabled {
			formats = append(formats, "anthropic")
		}
		if p.OpenAI != nil && p.OpenAI.Enabled {
			formats = append(formats, "openai")
		}
		if p.Anthropic == nil && p.OpenAI == nil {
			formats = append(formats, p.PreferredFormat())
		}
		for _, format := range formats {
			raw := strings.TrimSpace(p.EffectiveBaseURLRaw(format))
			host, err := apiHost(raw)
			if err != nil {
				view.Warnings = append(view.Warnings, format+" API 地址无效或未配置，请在服务提供商页面补全")
				continue
			}
			view.Formats = append(view.Formats, format)
			if !contains(view.Domains, host) {
				view.Domains = append(view.Domains, host)
			}
			if host == "localhost" || net.ParseIP(host).IsLoopback() {
				view.Warnings = append(view.Warnings, "本地回环 API 始终直连")
			}
		}
		sort.Strings(view.Domains)
		result = append(result, view)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	for i := range result {
		for j := range result {
			if i == j {
				continue
			}
			for _, host := range result[i].Domains {
				if contains(result[j].Domains, host) {
					result[i].Warnings = append(result[i].Warnings, fmt.Sprintf("与 %s 共用 %s；同域名无法按服务提供商区分出口", result[j].Name, host))
				}
			}
		}
	}
	return result
}
func contains(all []string, value string) bool {
	for _, s := range all {
		if s == value {
			return true
		}
	}
	return false
}
func apiHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("invalid API URL")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	return idna.Lookup.ToASCII(host)
}

// Resolve on save/start, rather than trusting client-supplied domains. A removed
// source fails explicitly; disabling/unlinking the rule remains possible.
func resolveProviders(settings Settings, providers []Provider) (Settings, []string) {
	settings = settings.clone()
	issues := []string{}
	byID := map[string]Provider{}
	for _, p := range providers {
		byID[p.ID] = p
	}
	for i := range settings.Routing.Rules {
		rule := &settings.Routing.Rules[i]
		if rule.Provider == "" {
			continue
		}
		p, ok := byID[rule.Provider]
		if !ok || len(p.Domains) == 0 {
			rule.Domains = []string{}
			if rule.Enabled {
				issues = append(issues, fmt.Sprintf("规则 %q 关联的服务提供商不存在或没有有效 API 地址，请重新关联或停用", rule.Name))
			}
			continue
		}
		rule.Domains = append([]string{}, p.Domains...)
	}
	// Domain routing cannot separate two providers sharing the same host. Reject
	// contradictory linked exits; manual rules retain explicit first-match order.
	for i, a := range settings.Routing.Rules {
		if !a.Enabled {
			continue
		}
		for _, b := range settings.Routing.Rules[i+1:] {
			if !b.Enabled || a.Provider == "" || b.Provider == "" || sameExit(a, b) {
				continue
			}
			for _, ah := range a.Domains {
				for _, bh := range b.Domains {
					if overlaps(ah, bh) {
						issues = append(issues, fmt.Sprintf("规则 %q 与 %q 的域名重叠（%s / %s），请使用相同出口或停用其中一条", a.Name, b.Name, ah, bh))
					}
				}
			}
		}
	}
	return settings, issues
}
func sameExit(a, b router.Rule) bool {
	return a.Mode == b.Mode && (a.Mode == "direct" || a.Node == b.Node)
}
func overlaps(a, b string) bool {
	if net.ParseIP(a) != nil || net.ParseIP(b) != nil {
		return a == b
	}
	return a == b || strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a)
}

func (s *Service) SetProviderSource(source func() []Provider) {
	s.mu.Lock()
	s.providerSource = source
	s.mu.Unlock()
}
func (s *Service) providers() []Provider {
	s.mu.Lock()
	source := s.providerSource
	s.mu.Unlock()
	if source == nil {
		return []Provider{}
	}
	return source()
}
