package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Service) readDisabledSubItems(pluginID string) ([]SubItemRef, error) {
	stateFile, err := s.readSubItemStateFile()
	if err != nil {
		return nil, err
	}
	for _, state := range stateFile {
		if state.PluginID == pluginID {
			return normalizeSubItemRefs(state.DisabledSubItems), nil
		}
	}
	return []SubItemRef{}, nil
}

func (s *Service) readSubItemStateFile() ([]PluginSubItemState, error) {
	path := defaultSubItemStatePath(defaultConfigDir())
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []PluginSubItemState{}, nil
		}
		return nil, fmt.Errorf("read plugin subitems: %w", err)
	}

	var raw pluginSubItemStateFile
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse plugin subitems: %w", err)
	}
	if raw.Plugins == nil {
		return []PluginSubItemState{}, nil
	}
	for i := range raw.Plugins {
		raw.Plugins[i].DisabledSubItems = normalizeSubItemRefs(raw.Plugins[i].DisabledSubItems)
	}
	sort.Slice(raw.Plugins, func(i, j int) bool {
		return raw.Plugins[i].PluginID < raw.Plugins[j].PluginID
	})
	return raw.Plugins, nil
}

func (s *Service) writeSubItemStateFile(states []PluginSubItemState) error {
	path := defaultSubItemStatePath(defaultConfigDir())
	states = normalizeStateEntries(states)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir plugin subitems dir: %w", err)
	}
	payload := pluginSubItemStateFile{Plugins: states}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plugin subitems: %w", err)
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp plugin subitems: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace plugin subitems: %w", err)
	}
	return nil
}

func upsertSubItemState(states []PluginSubItemState, pluginID string, ref SubItemRef, enabled bool) []PluginSubItemState {
	updated := false
	for i := range states {
		if states[i].PluginID != pluginID {
			continue
		}
		states[i].DisabledSubItems = setDisabledSubItem(states[i].DisabledSubItems, ref, !enabled)
		updated = true
		if len(states[i].DisabledSubItems) == 0 {
			states = append(states[:i], states[i+1:]...)
		}
		break
	}
	if !updated && !enabled {
		states = append(states, PluginSubItemState{PluginID: pluginID, DisabledSubItems: []SubItemRef{ref}})
	}
	return states
}

func normalizeStateEntries(states []PluginSubItemState) []PluginSubItemState {
	filtered := make([]PluginSubItemState, 0, len(states))
	for _, state := range states {
		if strings.TrimSpace(state.PluginID) == "" {
			continue
		}
		state.DisabledSubItems = normalizeSubItemRefs(state.DisabledSubItems)
		if len(state.DisabledSubItems) == 0 {
			continue
		}
		filtered = append(filtered, state)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].PluginID < filtered[j].PluginID
	})
	return filtered
}

func normalizeSubItemRefs(refs []SubItemRef) []SubItemRef {
	if len(refs) == 0 {
		return []SubItemRef{}
	}
	seen := make(map[string]SubItemRef, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Name) == "" || strings.TrimSpace(string(ref.Type)) == "" {
			continue
		}
		seen[ref.Key()] = SubItemRef{Type: ref.Type, Name: strings.TrimSpace(ref.Name)}
	}
	out := make([]SubItemRef, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type == out[j].Type {
			return out[i].Name < out[j].Name
		}
		return out[i].Type < out[j].Type
	})
	return out
}

func setDisabledSubItem(refs []SubItemRef, ref SubItemRef, disabled bool) []SubItemRef {
	current := make(map[string]SubItemRef, len(refs))
	for _, item := range normalizeSubItemRefs(refs) {
		current[item.Key()] = item
	}
	if disabled {
		current[ref.Key()] = ref
	} else {
		delete(current, ref.Key())
	}
	out := make([]SubItemRef, 0, len(current))
	for _, item := range current {
		out = append(out, item)
	}
	return normalizeSubItemRefs(out)
}

func defaultSubItemStatePath(configDir string) string {
	return filepath.Join(configDir, "plugin-subitems.json")
}

func defaultConfigDir() string {
	homeDir, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(homeDir) != "" {
		return filepath.Join(homeDir, ".amagi-codebox")
	}
	return filepath.Join(".", ".amagi-codebox")
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if len(input) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
