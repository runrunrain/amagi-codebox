package usage

import "strings"

// canonicalProviderID turns known provider aliases into one stable identifier.
//
// Session logs and OpenCode use provider IDs such as "zhipuai" and "zhipu",
// while the pricing table uses "glm". Keeping a single identifier prevents one
// provider from being split across several rows in the dashboard.
func canonicalProviderID(raw string) string {
	provider := strings.ToLower(strings.TrimSpace(raw))
	switch provider {
	case "", "unknown", "unattributed":
		return ""
	case "zhipuai", "zhipu", "bigmodel", "glm":
		return "glm"
	case "moonshot", "kimi":
		return "moonshot"
	case "volcengine", "bytedance", "doubao":
		return "doubao"
	case "dashscope", "aliyun", "alibaba", "alicloud", "qwen":
		return "qwen"
	case "x.ai", "xai", "grok":
		return "xai"
	default:
		return provider
	}
}

// inferProviderFromModel gives session-log records a best-effort provider when
// their source format does not carry one. Explicit provider IDs always win;
// this fallback is only used for missing or unknown attribution.
func inferProviderFromModel(normalizedModel string) string {
	model := strings.ToLower(strings.TrimSpace(normalizedModel))
	switch {
	case strings.HasPrefix(model, "claude-"):
		return "anthropic"
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "codex"):
		return "openai"
	case strings.HasPrefix(model, "glm-"):
		return "glm"
	case strings.HasPrefix(model, "deepseek-"):
		return "deepseek"
	case strings.HasPrefix(model, "moonshot-"), strings.HasPrefix(model, "kimi-"):
		return "moonshot"
	case strings.HasPrefix(model, "minimax-"), strings.HasPrefix(model, "abab"):
		return "minimax"
	case strings.HasPrefix(model, "doubao-"):
		return "doubao"
	case strings.HasPrefix(model, "qwen-"):
		return "qwen"
	case strings.HasPrefix(model, "gemini-"):
		return "google"
	case strings.HasPrefix(model, "mistral-"):
		return "mistral"
	case strings.HasPrefix(model, "llama-"):
		return "meta"
	default:
		return ""
	}
}

// currencyForProvider is used only when a source reports a native aggregate
// cost but omits its currency. Unknown providers deliberately fall back to USD
// so a non-zero cost is never silently dropped.
func currencyForProvider(provider string) string {
	switch canonicalProviderID(provider) {
	case "glm", "moonshot", "minimax", "doubao", "qwen", "baichuan", "01ai", "lingyiwanxiang":
		return "CNY"
	default:
		return "USD"
	}
}

// resolveProvider keeps explicit attribution, otherwise resolves from the
// matched pricing entry and finally from model-family conventions.
func (s *Service) resolveProvider(explicitProvider, normalizedModel string) string {
	if provider := canonicalProviderID(explicitProvider); provider != "" {
		return provider
	}
	if s != nil && s.pricing != nil {
		if pricing, ok := s.pricing.Resolve(normalizedModel); ok {
			if provider := canonicalProviderID(pricing.Provider); provider != "" {
				return provider
			}
		}
	}
	if provider := inferProviderFromModel(normalizedModel); provider != "" {
		return provider
	}
	return "unknown"
}
