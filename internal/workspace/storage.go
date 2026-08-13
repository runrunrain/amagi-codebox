package workspace

import (
	"amagi-codebox/internal/plugin"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Service) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspaces, err := s.readWorkspacesFile()
	if err != nil {
		return err
	}
	entries, err := s.readGlobalEnabledFile()
	if err != nil {
		return err
	}
	s.workspaces = workspaces
	s.globalEnabled = entries
	return nil
}

func (s *Service) Save() error {
	s.mu.RLock()
	workspaces := cloneWorkspaces(s.workspaces)
	globalEnabled := cloneGlobalEnabled(s.globalEnabled)
	s.mu.RUnlock()
	if err := writeJSONFile(s.workspacesPath, workspacesFile{Workspaces: workspaces}); err != nil {
		return err
	}
	return writeJSONFile(s.globalEnabledPath, globalEnabledFile{Entries: globalEnabled})
}

// GetPortableConfig returns a deep copy of workspace selections.
func (s *Service) GetPortableConfig() PortableConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return PortableConfig{
		Workspaces:    cloneWorkspaces(s.workspaces),
		GlobalEnabled: cloneGlobalEnabled(s.globalEnabled),
	}
}

// ReplacePortableConfig replaces workspace bookkeeping without deploying or
// deleting any files on the destination machine.
func (s *Service) ReplacePortableConfig(next PortableConfig) error {
	workspaces := cloneWorkspaces(next.Workspaces)
	if workspaces == nil {
		workspaces = []Workspace{}
	}
	seenIDs := make(map[string]struct{}, len(workspaces))
	seenPaths := make(map[string]struct{}, len(workspaces))
	for i := range workspaces {
		workspaces[i] = normalizeWorkspace(workspaces[i])
		if workspaces[i].ID == "" || workspaces[i].Path == "" || workspaces[i].Path == "." {
			return errors.New("workspace id and path are required")
		}
		if _, exists := seenIDs[workspaces[i].ID]; exists {
			return fmt.Errorf("duplicate workspace id: %s", workspaces[i].ID)
		}
		if _, exists := seenPaths[workspaces[i].Path]; exists {
			return fmt.Errorf("duplicate workspace path: %s", workspaces[i].Path)
		}
		seenIDs[workspaces[i].ID] = struct{}{}
		seenPaths[workspaces[i].Path] = struct{}{}
	}
	globalEnabled := cloneGlobalEnabled(next.GlobalEnabled)
	if globalEnabled == nil {
		globalEnabled = []GlobalEnabled{}
	}

	s.mu.Lock()
	previousWorkspaces := s.workspaces
	previousGlobal := s.globalEnabled
	s.workspaces = workspaces
	s.globalEnabled = globalEnabled
	s.mu.Unlock()
	if err := s.Save(); err != nil {
		s.mu.Lock()
		s.workspaces = previousWorkspaces
		s.globalEnabled = previousGlobal
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *Service) readWorkspacesFile() ([]Workspace, error) {
	var raw workspacesFile
	if err := readJSONFile(s.workspacesPath, &raw); err != nil {
		return nil, err
	}
	if raw.Workspaces == nil {
		return []Workspace{}, nil
	}
	for i := range raw.Workspaces {
		raw.Workspaces[i] = normalizeWorkspace(raw.Workspaces[i])
	}
	sort.Slice(raw.Workspaces, func(i, j int) bool { return raw.Workspaces[i].CreatedAt < raw.Workspaces[j].CreatedAt })
	return raw.Workspaces, nil
}

func (s *Service) readGlobalEnabledFile() ([]GlobalEnabled, error) {
	var raw globalEnabledFile
	if err := readJSONFile(s.globalEnabledPath, &raw); err != nil {
		return nil, err
	}
	if raw.Entries == nil {
		return []GlobalEnabled{}, nil
	}
	for i := range raw.Entries {
		raw.Entries[i] = normalizeGlobalEnabled(raw.Entries[i])
	}
	sort.Slice(raw.Entries, func(i, j int) bool { return raw.Entries[i].PluginID < raw.Entries[j].PluginID })
	return raw.Entries, nil
}

func writeJSONFile(path string, value interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func readJSONFile(path string, target interface{}) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, target); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func normalizeWorkspace(workspace Workspace) Workspace {
	workspace.Path = filepath.Clean(strings.TrimSpace(workspace.Path))
	workspace.Name = strings.TrimSpace(workspace.Name)
	workspace.Tools = normalizeTools(workspace.Tools)
	workspace.Plugins = normalizeWorkspacePlugins(workspace.Plugins)
	if workspace.CreatedAt == "" {
		workspace.CreatedAt = workspace.UpdatedAt
	}
	return workspace
}

func normalizeWorkspacePlugins(items []WorkspacePlugin) []WorkspacePlugin {
	if items == nil {
		return []WorkspacePlugin{}
	}
	out := make([]WorkspacePlugin, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.PluginID) == "" {
			continue
		}
		item.PluginID = strings.TrimSpace(item.PluginID)
		item.EnabledSubItems = normalizeWorkspaceSubItemRefs(item.EnabledSubItems)
		if item.DeployScope == "" {
			item.DeployScope = string(SourceScopeWorkspace)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PluginID < out[j].PluginID })
	return out
}

func normalizeGlobalEnabled(entry GlobalEnabled) GlobalEnabled {
	entry.PluginID = strings.TrimSpace(entry.PluginID)
	entry.Tools = normalizeTools(entry.Tools)
	entry.EnabledSubItems = normalizeWorkspaceSubItemRefs(entry.EnabledSubItems)
	return entry
}

func normalizeWorkspaceSubItemRefs(refs []plugin.SubItemRef) []plugin.SubItemRef {
	if refs == nil {
		return []plugin.SubItemRef{}
	}
	seen := make(map[string]plugin.SubItemRef, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Name) == "" || strings.TrimSpace(string(ref.Type)) == "" {
			continue
		}
		seen[ref.Key()] = plugin.SubItemRef{Type: ref.Type, Name: strings.TrimSpace(ref.Name)}
	}
	out := make([]plugin.SubItemRef, 0, len(seen))
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

func normalizeTools(tools []ToolType) []ToolType {
	if tools == nil {
		return []ToolType{}
	}
	seen := map[ToolType]struct{}{}
	out := make([]ToolType, 0, len(tools))
	for _, tool := range tools {
		switch tool {
		case ToolTypeClaude, ToolTypeOpenCode, ToolTypeCursor, ToolTypeVSCode:
			if _, ok := seen[tool]; ok {
				continue
			}
			seen[tool] = struct{}{}
			out = append(out, tool)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func cloneWorkspaces(items []Workspace) []Workspace {
	out := make([]Workspace, len(items))
	copy(out, items)
	for i := range out {
		out[i].Tools = append([]ToolType{}, items[i].Tools...)
		out[i].Plugins = append([]WorkspacePlugin{}, items[i].Plugins...)
		for j := range out[i].Plugins {
			out[i].Plugins[j].EnabledSubItems = append([]plugin.SubItemRef{}, items[i].Plugins[j].EnabledSubItems...)
		}
	}
	return out
}

func cloneGlobalEnabled(items []GlobalEnabled) []GlobalEnabled {
	out := make([]GlobalEnabled, len(items))
	copy(out, items)
	for i := range out {
		out[i].Tools = append([]ToolType{}, items[i].Tools...)
		out[i].EnabledSubItems = append([]plugin.SubItemRef{}, items[i].EnabledSubItems...)
	}
	return out
}
