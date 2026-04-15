package workspace

import (
	"fmt"
)

func (s *Service) CheckConflicts(pluginID, scope, target string) ([]Conflict, error) {
	switch scope {
	case string(SourceScopeGlobal):
		tool := ToolTypeClaude
		if target != "" {
			tool = ToolType(target)
		}
		plan, _, err := s.buildGlobalPlan([]GlobalEnabled{{PluginID: pluginID, EnabledAll: true, Tools: []ToolType{tool}}})
		if err != nil {
			return nil, err
		}
		manifest, err := ReadManifest(s.globalManifestPath)
		if err != nil {
			return nil, err
		}
		return collectPlanConflicts(s.homeDir, manifest, plan)
	case string(SourceScopeWorkspace):
		workspace, err := s.GetWorkspace(target)
		if err != nil {
			return nil, err
		}
		plan, _, err := s.buildWorkspacePlan(Workspace{ID: workspace.ID, Path: workspace.Path, Tools: workspace.Tools, Plugins: []WorkspacePlugin{{PluginID: pluginID, DeployScope: string(SourceScopeWorkspace)}}})
		if err != nil {
			return nil, err
		}
		manifest, err := ReadManifest(ManifestPathForWorkspace(workspace.Path))
		if err != nil {
			return nil, err
		}
		return collectPlanConflicts(workspace.Path, manifest, plan)
	default:
		return nil, fmt.Errorf("unsupported conflict scope: %s", scope)
	}
}

func collectPlanConflicts(root string, manifest DeploymentManifest, plan deploymentPlan) ([]Conflict, error) {
	activeEntries, _ := splitManifestEntriesByStatus(manifest.Entries)
	activeManifest := defaultManifest()
	activeManifest.Entries = activeEntries
	conflicts := checkPlanConflicts(root, activeManifest, plan)
	touchedTargets := map[string]struct{}{}
	for _, file := range plan.Files {
		touchedTargets[file.Entry.TargetPath] = struct{}{}
	}
	for _, merged := range plan.Merged {
		touchedTargets[merged.TargetPath] = struct{}{}
	}
	for target, entries := range groupEntriesByTarget(activeEntries) {
		if _, ok := touchedTargets[target]; !ok {
			continue
		}
		changed, err := managedTargetModified(root, target, entries)
		if err != nil {
			return nil, err
		}
		if changed {
			conflicts = append(conflicts, Conflict{Type: ConflictTypeModifiedFile, PluginID: entries[0].PluginID, TargetPath: target, Message: fmt.Sprintf("托管文件 %s 已被手动修改，当前操作不会覆盖", target), Blocking: true})
		}
	}
	return conflicts, nil
}
