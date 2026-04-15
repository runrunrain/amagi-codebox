package workspace

import (
	"amagi-codebox/internal/plugin"
	"fmt"
	"time"
)

func (s *Service) BuildScaffold(workspaceID string) (DeployResult, error) {
	workspace, err := s.GetWorkspace(workspaceID)
	if err != nil {
		return DeployResult{}, err
	}
	plan, warnings, err := s.buildWorkspacePlan(workspace)
	if err != nil {
		return DeployResult{}, err
	}
	return s.applyPlan(workspaceID, workspace.Path, ManifestPathForWorkspace(workspace.Path), plan, warnings)
}

func (s *Service) SyncWorkspace(workspaceID string) (SyncResult, error) {
	return s.BuildScaffold(workspaceID)
}

func (s *Service) CleanWorkspace(workspaceID string) (CleanResult, error) {
	workspace, err := s.GetWorkspace(workspaceID)
	if err != nil {
		return CleanResult{}, err
	}
	return s.cleanManifestTargets(workspaceID, workspace.Path, ManifestPathForWorkspace(workspace.Path))
}

func (s *Service) SetGlobalEnabled(entries []GlobalEnabled) (DeployResult, error) {
	normalized, err := s.validateGlobalEnabled(entries)
	if err != nil {
		return DeployResult{}, err
	}
	if err := s.preflightGlobalOwnershipMigration(normalized); err != nil {
		return DeployResult{}, err
	}
	plan, warnings, err := s.buildGlobalPlan(normalized)
	if err != nil {
		return DeployResult{}, err
	}
	result, err := s.applyPlan("global", s.homeDir, s.globalManifestPath, plan, warnings)
	if err != nil {
		return result, err
	}
	if err := s.applyWorkspaceOwnershipMigration(normalized); err != nil {
		return result, err
	}
	s.mu.Lock()
	s.globalEnabled = normalized
	s.mu.Unlock()
	if err := writeJSONFile(s.globalEnabledPath, globalEnabledFile{Entries: normalized}); err != nil {
		return DeployResult{}, err
	}
	return result, nil
}

func (s *Service) validateWorkspacePlugins(items []WorkspacePlugin) ([]WorkspacePlugin, error) {
	out := make([]WorkspacePlugin, 0, len(items))
	for _, item := range normalizeWorkspacePlugins(items) {
		detail, err := s.plugins.GetPluginDetail(item.PluginID)
		if err != nil {
			return nil, err
		}
		if len(item.EnabledSubItems) > 0 {
			if _, err := resolveSelectedSubItems(detail, item.EnabledSubItems, false); err != nil {
				return nil, err
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) preflightGlobalOwnershipMigration(entries []GlobalEnabled) error {
	s.mu.RLock()
	workspaces := cloneWorkspaces(s.workspaces)
	s.mu.RUnlock()
	for _, workspace := range workspaces {
		if !workspaceAffectedByGlobals(workspace, entries) {
			continue
		}
		if err := s.preflightWorkspaceOwnershipMigration(workspace, entries); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) preflightWorkspaceOwnershipMigration(workspace Workspace, entries []GlobalEnabled) error {
	detailCache := map[string]*plugin.PluginDetail{}
	for _, item := range workspace.Plugins {
		detail, err := s.cachedPluginDetail(detailCache, item.PluginID)
		if err != nil {
			return err
		}
		if _, _, _, err := migrateWorkspacePluginSelection(workspace.Tools, item, detail, entries); err != nil {
			return err
		}
	}
	manifest, err := ReadManifest(ManifestPathForWorkspace(workspace.Path))
	if err != nil {
		return err
	}
	for _, entry := range manifest.Entries {
		if entry.SourceScope != SourceScopeWorkspace {
			continue
		}
		detail, err := s.cachedPluginDetail(detailCache, entry.PluginID)
		if err != nil {
			return err
		}
		if _, err := workspaceOwnsEntry(workspace, detail, entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) cachedPluginDetail(cache map[string]*plugin.PluginDetail, pluginID string) (*plugin.PluginDetail, error) {
	if detail, ok := cache[pluginID]; ok {
		return detail, nil
	}
	detail, err := s.plugins.GetPluginDetail(pluginID)
	if err != nil {
		return nil, err
	}
	cache[pluginID] = detail
	return detail, nil
}

func (s *Service) validateGlobalEnabled(items []GlobalEnabled) ([]GlobalEnabled, error) {
	out := make([]GlobalEnabled, 0, len(items))
	for _, item := range items {
		item = normalizeGlobalEnabled(item)
		if item.PluginID == "" {
			return nil, fmt.Errorf("global enabled pluginId is required")
		}
		if len(item.Tools) == 0 {
			item.Tools = []ToolType{ToolTypeClaude}
		}
		detail, err := s.plugins.GetPluginDetail(item.PluginID)
		if err != nil {
			return nil, err
		}
		if !item.EnabledAll {
			if len(item.EnabledSubItems) == 0 {
				return nil, fmt.Errorf("global enabled entry %s must specify enabledSubItems when enabledAll is false", item.PluginID)
			}
			if _, err := resolveSelectedSubItems(detail, item.EnabledSubItems, false); err != nil {
				return nil, err
			}
		}
		if item.DeployedAt == "" {
			item.DeployedAt = time.Now().UTC().Format(time.RFC3339)
		}
		out = append(out, item)
	}
	return out, nil
}

func applyGlobalDisplayContract(detail plugin.PluginDetail, tools []ToolType, entries []GlobalEnabled) (bool, plugin.PluginDetail) {
	updated := detail
	filtered := make([]plugin.SubItem, 0, len(updated.SubItems))
	for _, item := range updated.SubItems {
		if !item.Enabled {
			continue
		}
		item.GloballyEnabled = false
		item.Selectable = true
		filtered = append(filtered, item)
	}
	updated.SubItems = filtered
	for _, entry := range entries {
		if entry.PluginID != detail.ID || !toolsOverlap(tools, entry.Tools) {
			continue
		}
		if entry.EnabledAll {
			return false, updated
		}
		globalSet := map[string]struct{}{}
		for _, ref := range entry.EnabledSubItems {
			globalSet[ref.Key()] = struct{}{}
		}
		for i := range updated.SubItems {
			ref := plugin.SubItemRef{Type: updated.SubItems[i].Type, Name: updated.SubItems[i].Name}
			if _, ok := globalSet[ref.Key()]; ok {
				updated.SubItems[i].GloballyEnabled = true
				updated.SubItems[i].Selectable = false
			}
		}
	}
	return true, updated
}

func toolsOverlap(left, right []ToolType) bool {
	set := map[ToolType]struct{}{}
	for _, item := range left {
		set[item] = struct{}{}
	}
	for _, item := range right {
		if _, ok := set[item]; ok {
			return true
		}
	}
	return false
}
