package proxy

// InjectionRule — a keyword-to-prompt mapping rule
type InjectionRule struct {
	ID          string   `json:"id"`          // UUID
	Name        string   `json:"name"`        // Human-readable name
	Keywords    []string `json:"keywords"`    // Keywords to match (empty rules are ignored)
	Prompt      string   `json:"prompt"`      // Prompt text to inject
	Enabled     bool     `json:"enabled"`     // Active or not
	Priority    int      `json:"priority"`    // Higher = matched first
	EnableCache bool     `json:"enableCache"` // Enable prompt caching (default false)
	CacheTTL    string   `json:"cacheTtl"`    // Cache TTL: "5m" or "1h" (default "5m")
}

// ProxyStatus — current state of the proxy
type ProxyStatus struct {
	Running    bool   `json:"running"`
	Port       int    `json:"port"`
	BackendURL string `json:"backendURL"`
	RuleCount  int    `json:"ruleCount"`
}

// InjectionLog — log entry for an injection event
type InjectionLog struct {
	Time      string   `json:"time"`      // ISO8601 or HH:MM:SS
	RuleNames []string `json:"ruleNames"` // Which rules matched
	Preview   string   `json:"preview"`   // First 100 chars of user message
	Status    int      `json:"status"`    // HTTP response status from backend
}
