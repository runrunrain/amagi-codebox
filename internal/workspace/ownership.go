package workspace

import (
	"amagi-codebox/internal/plugin"
	"time"
)

func (s *Service) applyWorkspaceOwnershipMigration(entries []GlobalEnabled) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	updatedWorkspaces := cloneWorkspaces(s.workspaces)
	workspacesChanged := false
	for i := range updatedWorkspaces {
		workspace := updatedWorkspaces[i]
		if !workspaceAffectedByGlobals(workspace, entries) {
			continue
		}
		updatedPlugins, changed, err := s.migrateWorkspacePluginSelections(workspace, entries)
		if err != nil {
			return err
		}
		if changed {
			workspace.Plugins = updatedPlugins
			workspace.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			updatedWorkspaces[i] = workspace
			workspacesChanged = true
		}
		if err := s.updateWorkspaceManifestOwnership(workspace); err != nil {
			return err
		}
	}
	if workspacesChanged {
		s.workspaces = updatedWorkspaces
		if err := writeJSONFile(s.workspacesPath, workspacesFile{Workspaces: updatedWorkspaces}); err != nil {
			return err
		}
	}
	return nil
}

func workspaceAffectedByGlobals(workspace Workspace, entries []GlobalEnabled) bool {
	for _, entry := range entries {
		if toolsOverlap(workspace.Tools, entry.Tools) {
			return true
		}
	}
	return false
}

func (s *Service) migrateWorkspacePluginSelections(workspace Workspace, entries []GlobalEnabled) ([]WorkspacePlugin, bool, error) {
	changed := false
	updated := make([]WorkspacePlugin, 0, len(workspace.Plugins))
	for _, item := range workspace.Plugins {
		detail, err := s.plugins.GetPluginDetail(item.PluginID)
		if err != nil {
			return nil, false, err
		}
		migrated, keep, itemChanged, err := migrateWorkspacePluginSelection(workspace.Tools, item, detail, entries)
		if err != nil {
			return nil, false, err
		}
		if itemChanged {
			changed = true
		}
		if keep {
			updated = append(updated, migrated)
		}
	}
	return normalizeWorkspacePlugins(updated), changed, nil
}

func migrateWorkspacePluginSelection(tools []ToolType, item WorkspacePlugin, detail *plugin.PluginDetail, entries []GlobalEnabled) (WorkspacePlugin, bool, bool, error) {
	owned, err := ownedSubItemRefs(detail, item)
	if err != nil {
		return WorkspacePlugin{}, false, false, err
	}
	remaining := make(map[string]plugin.SubItemRef, len(owned))
	for _, ref := range owned {
		remaining[ref.Key()] = ref
	}
	for _, entry := range entries {
		if entry.PluginID != item.PluginID || !toolsOverlap(tools, entry.Tools) {
			continue
		}
		if entry.EnabledAll {
			remaining = map[string]plugin.SubItemRef{}
			break
		}
		for _, ref := range entry.EnabledSubItems {
			delete(remaining, ref.Key())
		}
	}
	if len(remaining) == 0 {
		return WorkspacePlugin{}, false, true, nil
	}
	remainingRefs := make([]plugin.SubItemRef, 0, len(remaining))
	for _, ref := range remaining {
		remainingRefs = append(remainingRefs, ref)
	}
	remainingRefs = normalizeWorkspaceSubItemRefs(remainingRefs)
	allRefs, err := enabledPluginRefs(detail)
	if err != nil {
		return WorkspacePlugin{}, false, false, err
	}
	updated := item
	updated.EnabledSubItems = remainingRefs
	if sameSubItemRefSet(remainingRefs, allRefs) {
		updated.EnabledSubItems = []plugin.SubItemRef{}
	}
	return updated, true, !sameWorkspacePluginSelection(item, updated), nil
}
func (s *Service) updateWorkspaceManifestOwnership(workspace Workspace) error {
	manifestPath := ManifestPathForWorkspace(workspace.Path)
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		return err
	}
	changed := false
	detailCache := map[string]*plugin.PluginDetail{}
	for i := range manifest.Entries {
		entry := &manifest.Entries[i]
		if entry.SourceScope != SourceScopeWorkspace {
			continue
		}
		detail, ok := detailCache[entry.PluginID]
		if !ok {
			detail, err = s.plugins.GetPluginDetail(entry.PluginID)
			if err != nil {
				return err
			}
			detailCache[entry.PluginID] = detail
		}
		owned, err := workspaceOwnsEntry(workspace, detail, *entry)
		if err != nil {
			return err
		}
		nextStatus := DeploymentStatusOrphaned
		if owned {
			nextStatus = DeploymentStatusActive
		}
		if entry.Status != nextStatus {
			entry.Status = nextStatus
			changed = true
		}
	}
	if changed {
		return WriteManifest(manifestPath, manifest)
	}
	return nil
}

func workspaceOwnsEntry(workspace Workspace, detail *plugin.PluginDetail, entry DeploymentEntry) (bool, error) {
	var selected *WorkspacePlugin
	for i := range workspace.Plugins {
		if workspace.Plugins[i].PluginID == entry.PluginID {
			selected = &workspace.Plugins[i]
			break
		}
	}
	if selected == nil {
		return false, nil
	}
	if entry.SubItemRef.Type == plugin.SubItemTypeClaude {
		return len(selected.EnabledSubItems) == 0, nil
	}
	if entry.SubItemRef.Type == plugin.SubItemTypeHook && len(selected.EnabledSubItems) > 0 && len(entry.SubItemRef.Name) >= len(plugin.HookAssetsSubItemPrefix) && entry.SubItemRef.Name[:len(plugin.HookAssetsSubItemPrefix)] == plugin.HookAssetsSubItemPrefix {
		for _, ref := range selected.EnabledSubItems {
			if ref.Type == plugin.SubItemTypeHook {
				return true, nil
			}
		}
		return false, nil
	}
	ownedRefs, err := ownedSubItemRefs(detail, *selected)
	if err != nil {
		return false, err
	}
	for _, ref := range ownedRefs {
		if ref.Key() == entry.SubItemRef.Key() {
			return true, nil
		}
	}
	return false, nil
}

func ownedSubItemRefs(detail *plugin.PluginDetail, item WorkspacePlugin) ([]plugin.SubItemRef, error) {
	if len(item.EnabledSubItems) == 0 {
		return enabledPluginRefs(detail)
	}
	return normalizeWorkspaceSubItemRefs(item.EnabledSubItems), nil
}

func enabledPluginRefs(detail *plugin.PluginDetail) ([]plugin.SubItemRef, error) {
	items, err := resolveSelectedSubItems(detail, nil, true)
	if err != nil {
		return nil, err
	}
	refs := make([]plugin.SubItemRef, 0, len(items))
	for _, item := range items {
		refs = append(refs, plugin.SubItemRef{Type: item.Type, Name: item.Name})
	}
	return normalizeWorkspaceSubItemRefs(refs), nil
}

func sameWorkspacePluginSelection(left, right WorkspacePlugin) bool {
	return left.PluginID == right.PluginID && sameSubItemRefSet(left.EnabledSubItems, right.EnabledSubItems)
}

func sameSubItemRefSet(left, right []plugin.SubItemRef) bool {
	left = normalizeWorkspaceSubItemRefs(left)
	right = normalizeWorkspaceSubItemRefs(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Key() != right[i].Key() {
			return false
		}
	}
	return true
}
