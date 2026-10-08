package piconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"amagi-codebox/internal/config"
)

// StandardAgentDir deliberately ignores PI_CODING_AGENT_DIR: CodeBox launches
// Pi with that override removed. Discovery and launch must use the same account.
func StandardAgentDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("Pi home directory unavailable")
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// PiLoginProvider is a credential-free dynamic projection, not a config.Provider.
type PiLoginProvider struct {
	ID       string              `json:"id"`
	Provider string              `json:"provider"`
	CLI      string              `json:"cli"`
	Source   string              `json:"source"`
	Models   []ModelCatalogEntry `json:"models"`
}

func (s *Service) GetPiLoginProviders() ([]PiLoginProvider, error) {
	dir, err := StandardAgentDir()
	if err != nil {
		return nil, err
	}
	return readLoginProviders(dir)
}

func readLoginProviders(dir string) ([]PiLoginProvider, error) {
	out := []PiLoginProvider{}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, errors.New("cannot read Pi login metadata")
	}
	// Only type is decoded. Never extract, copy, return, or log credential fields.
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || root == nil {
		return nil, errors.New("invalid Pi login metadata JSON")
	}
	names := []string{}
	for name, raw := range root {
		var entry struct {
			Type string `json:"type"`
		}
		if name != "" && strings.TrimSpace(name) == name && !strings.ContainsAny(name, "\x00\r\n") && json.Unmarshal(raw, &entry) == nil && entry.Type == "oauth" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, PiLoginProvider{ID: config.PiLoginProviderID(name), Provider: name, CLI: "pi", Source: "~/.pi/agent/auth.json", Models: loginModels(dir, name)})
	}
	return out, nil
}

// RequirePiLogin re-reads the source at every launch; stale UI or saved presets
// cannot silently launch a default account after logout. Host credentials are
// not portable to a WSL process. On success it returns the canonical agent
// directory the check actually validated — callers must pin that exact
// directory into the final Pi execution boundary (PI_CODING_AGENT_DIR plus a
// post-shell-startup re-anchor) so custom HOME/USERPROFILE in user env config
// or shell startup files cannot redirect Pi to a different auth.json.
func RequirePiLogin(provider string, wsl bool) (string, error) {
	if wsl {
		return "", errors.New("Pi host login credentials are not available in WSL")
	}
	dir, err := StandardAgentDir()
	if err != nil {
		return "", err
	}
	providers, err := readLoginProviders(dir)
	if err != nil {
		return "", err
	}
	for _, p := range providers {
		if p.Provider == provider {
			return dir, nil
		}
	}
	return "", errors.New("Pi login is no longer available; refresh providers and log in with Pi")
}

// Optional model hints must not gate login discovery. Read only model metadata,
// using the standard directory, not the editor's environment override.
func loginModels(dir, provider string) []ModelCatalogEntry {
	models := []ModelCatalogEntry{}
	seen := map[string]bool{}
	for _, file := range []string{"models.json", "models-store.json"} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			continue
		}
		var root map[string]json.RawMessage
		if json.Unmarshal(data, &root) != nil {
			continue
		}
		if file == "models.json" {
			if json.Unmarshal(root["providers"], &root) != nil {
				continue
			}
		}
		var entry struct {
			Models []map[string]any `json:"models"`
		}
		if json.Unmarshal(root[provider], &entry) != nil {
			continue
		}
		for _, raw := range entry.Models {
			if m, ok := extractModelEntry(raw); ok && !seen[m.ID] {
				models = append(models, m)
				seen[m.ID] = true
			}
		}
	}
	catalog := ModelCatalog{Providers: []ModelCatalogProvider{{Name: provider, Models: models}}}
	mergeFrontlineContractAt(&catalog, dir)
	return catalog.Providers[0].Models
}
