package aiproxy

import (
	"encoding/json"
	"errors"

	"amagi-codebox/internal/aiproxy/router"
)

type legacyRouting struct {
	ClaudePort    int      `json:"claude_port"`
	OpenAIPort    int      `json:"openai_port"`
	ClaudeNode    string   `json:"claude_node"`
	OpenAINode    string   `json:"openai_node"`
	ClaudeDomains []string `json:"claude_domains"`
	OpenAIDomains []string `json:"openai_domains"`
}

// Version 1 had two fixed providers. Preserve their ports, nodes and domains,
// and all common settings/groups. Version 3 adds provider links and NIC binding.
func decodeStoredState(b []byte) (storedState, bool, error) {
	var data storedState
	if err := json.Unmarshal(b, &data); err != nil {
		return data, false, err
	}
	migrated := false
	switch data.Version {
	case 1:
		var old struct {
			Settings struct{ Routing legacyRouting }
		}
		if err := json.Unmarshal(b, &old); err != nil {
			return data, false, err
		}
		r := old.Settings.Routing
		data.Settings.Routing.Rules = []router.Rule{
			{ID: "claude", Name: "Claude", Enabled: true, Mode: "proxy", Port: r.ClaudePort, Node: r.ClaudeNode, Domains: r.ClaudeDomains},
			{ID: "openai", Name: "OpenAI", Enabled: true, Mode: "proxy", Port: r.OpenAIPort, Node: r.OpenAINode, Domains: r.OpenAIDomains},
		}
		migrated = true
	case 2:
		migrated = true
		fallthrough
	case 3:
		if data.Settings.Routing.Rules == nil {
			return data, false, errors.New("missing AI proxy rules")
		}
	default:
		return data, false, errors.New("unsupported AI proxy state version")
	}
	if migrated {
		data.Version = 3
		data.Revision++
		data.Settings.Routing.EgressMode = "auto"
	}
	if err := data.Settings.Routing.Validate(); err != nil {
		return data, false, err
	}
	return data, migrated, nil
}
