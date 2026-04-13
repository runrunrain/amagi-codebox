package workspace

import (
	"amagi-codebox/internal/plugin"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type plannedFile struct {
	Entry      DeploymentEntry
	SourcePath string
	Content    []byte
}

type plannedMergedFile struct {
	TargetPath string
	Entries    []DeploymentEntry
	Content    []byte
}

type deploymentPlan struct {
	Files  []plannedFile
	Merged []plannedMergedFile
}

type hookContribution struct {
	Info  plugin.HookInfo
	Entry DeploymentEntry
}

func (s *Service) buildWorkspacePlan(workspace Workspace) (deploymentPlan, []string, error) {
	plan := deploymentPlan{}
	warnings := []string{}
	for _, tool := range workspace.Tools {
		adapter := newAdapterForTool(tool, s.homeDir)
		if adapter.ToolType() != ToolTypeClaude {
			warnings = append(warnings, fmt.Sprintf("工具 %s 适配器边界已保留，当前后端基础阶段仅完成 Claude 实际部署", tool))
			continue
		}
		toolPlan, err := s.buildClaudeWorkspacePlan(workspace)
		if err != nil {
			return deploymentPlan{}, warnings, err
		}
		plan.Files = append(plan.Files, toolPlan.Files...)
		plan.Merged = append(plan.Merged, toolPlan.Merged...)
	}
	return plan, warnings, nil
}

func (s *Service) buildGlobalPlan(entries []GlobalEnabled) (deploymentPlan, []string, error) {
	plan := deploymentPlan{}
	warnings := []string{}
	for _, entry := range entries {
		for _, tool := range entry.Tools {
			adapter := newAdapterForTool(tool, s.homeDir)
			if adapter.ToolType() != ToolTypeClaude {
				warnings = append(warnings, fmt.Sprintf("工具 %s 适配器边界已保留，当前后端基础阶段仅完成 Claude 实际部署", tool))
				continue
			}
			detail, err := s.plugins.GetPluginDetail(entry.PluginID)
			if err != nil {
				return deploymentPlan{}, warnings, err
			}
			toolPlan, err := s.buildClaudePluginPlan(detail, SourceScopeGlobal, entry.EnabledSubItems, entry.EnabledAll, false)
			if err != nil {
				return deploymentPlan{}, warnings, err
			}
			plan.Files = append(plan.Files, toolPlan.Files...)
			plan.Merged = append(plan.Merged, toolPlan.Merged...)
		}
	}
	return plan, warnings, nil
}

func (s *Service) buildClaudeWorkspacePlan(workspace Workspace) (deploymentPlan, error) {
	plan := deploymentPlan{}
	for _, item := range workspace.Plugins {
		detail, err := s.plugins.GetPluginDetail(item.PluginID)
		if err != nil {
			return deploymentPlan{}, err
		}
		toolPlan, err := s.buildClaudePluginPlan(detail, SourceScopeWorkspace, item.EnabledSubItems, len(item.EnabledSubItems) == 0, true)
		if err != nil {
			return deploymentPlan{}, err
		}
		plan.Files = append(plan.Files, toolPlan.Files...)
		plan.Merged = append(plan.Merged, toolPlan.Merged...)
	}
	return plan, nil
}
func (s *Service) buildClaudePluginPlan(detail *plugin.PluginDetail, sourceScope SourceScope, refs []plugin.SubItemRef, includeAll bool, workspaceMode bool) (deploymentPlan, error) {
	selected, err := resolveSelectedSubItems(detail, refs, includeAll)
	if err != nil {
		return deploymentPlan{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	plan := deploymentPlan{}
	var mcpEntries []DeploymentEntry
	mcpServers := map[string]interface{}{}
	var hookEntries []DeploymentEntry
	var hookContribs []hookContribution
	hasHookSelection := false
	for index, item := range selected {
		entry := DeploymentEntry{PluginID: detail.ID, PluginVersion: detail.Version, SubItemRef: plugin.SubItemRef{Type: item.Type, Name: item.Name}, MergeType: MergeTypeExclusive, Status: DeploymentStatusActive, DeployedAt: now, SourceScope: sourceScope}
		sourcePath := filepath.Join(detail.InstallPath, filepath.FromSlash(item.Path))
		targetPath := claudeTargetPath(item, workspaceMode)
		switch item.Type {
		case plugin.SubItemTypeSkill, plugin.SubItemTypeAgent, plugin.SubItemTypeCommand:
			entry.TargetPath = filepath.ToSlash(targetPath)
			plan.Files = append(plan.Files, plannedFile{Entry: entry, SourcePath: sourcePath})
		case plugin.SubItemTypeMCP:
			entry.MergeType = MergeTypeMerged
			entry.TargetPath = filepath.ToSlash(targetPath)
			entry.ContentMarker = "keys:" + item.Name
			entry.MergeOrder = index
			mcpEntries = append(mcpEntries, entry)
			mcpServers[item.Name] = cloneJSONValue(detail.MCPServers[item.Name])
		case plugin.SubItemTypeHook:
			hasHookSelection = true
			info, ok := findHookInfo(detail.Hooks, item.Name)
			if !ok {
				return deploymentPlan{}, fmt.Errorf("hook subitem not found: %s", item.Name)
			}
			entry.MergeType = MergeTypeMerged
			entry.TargetPath = filepath.ToSlash(targetPath)
			entry.ContentMarker = item.Name
			entry.MergeOrder = index
			hookEntries = append(hookEntries, entry)
			hookContribs = append(hookContribs, hookContribution{Info: info, Entry: entry})
		}
	}
	if detail.HasClaudeMd && includeAll {
		plan.Files = append(plan.Files, plannedFile{Entry: DeploymentEntry{PluginID: detail.ID, PluginVersion: detail.Version, SubItemRef: plugin.SubItemRef{Type: plugin.SubItemTypeClaude, Name: plugin.ClaudeBaselineSubItemName}, TargetPath: filepath.ToSlash(claudeBaselineTargetPath()), MergeType: MergeTypeExclusive, Status: DeploymentStatusActive, DeployedAt: now, SourceScope: sourceScope}, SourcePath: filepath.Join(detail.InstallPath, filepath.FromSlash(detail.ClaudeMdPath))})
	}
	if len(mcpEntries) > 0 {
		payload, err := json.MarshalIndent(map[string]interface{}{"mcpServers": mcpServers}, "", "  ")
		if err != nil {
			return deploymentPlan{}, fmt.Errorf("marshal mcp payload: %w", err)
		}
		plan.Merged = append(plan.Merged, plannedMergedFile{TargetPath: filepath.ToSlash(claudeMCPPath()), Entries: mcpEntries, Content: append(payload, '\n')})
	}
	if len(hookEntries) > 0 {
		payload, err := marshalHooksFile(hookContribs)
		if err != nil {
			return deploymentPlan{}, err
		}
		plan.Merged = append(plan.Merged, plannedMergedFile{TargetPath: filepath.ToSlash(claudeHooksConfigPath()), Entries: hookEntries, Content: payload})
	}
	if hasHookSelection {
		assets, err := collectHookAssets(detail, sourceScope, now)
		if err != nil {
			return deploymentPlan{}, err
		}
		plan.Files = append(plan.Files, assets...)
	}
	return plan, nil
}

func resolveSelectedSubItems(detail *plugin.PluginDetail, refs []plugin.SubItemRef, includeAll bool) ([]plugin.SubItem, error) {
	available := make(map[string]plugin.SubItem, len(detail.SubItems))
	if !includeAll && len(refs) == 0 {
		return nil, fmt.Errorf("plugin %s partial selection requires explicit subitems", detail.ID)
	}
	for _, item := range detail.SubItems {
		if !item.Enabled {
			continue
		}
		available[plugin.SubItemRef{Type: item.Type, Name: item.Name}.Key()] = item
	}
	if includeAll {
		out := make([]plugin.SubItem, 0, len(available))
		for _, item := range available {
			out = append(out, item)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Type == out[j].Type {
				return out[i].Name < out[j].Name
			}
			return out[i].Type < out[j].Type
		})
		return out, nil
	}
	out := make([]plugin.SubItem, 0, len(refs))
	for _, ref := range refs {
		item, ok := available[ref.Key()]
		if !ok {
			return nil, fmt.Errorf("plugin %s subitem unavailable: %s", detail.ID, ref.Key())
		}
		out = append(out, item)
	}
	return out, nil
}
func findHookInfo(items []plugin.HookInfo, name string) (plugin.HookInfo, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return plugin.HookInfo{}, false
}

func collectHookAssets(detail *plugin.PluginDetail, sourceScope SourceScope, now string) ([]plannedFile, error) {
	hooksDir := filepath.Join(detail.InstallPath, "hooks")
	if _, err := os.Stat(hooksDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []plannedFile
	err := filepath.WalkDir(hooksDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(hooksDir, path)
		if err != nil {
			return err
		}
		if filepath.ToSlash(relPath) == "hooks.json" {
			return nil
		}
		files = append(files, plannedFile{Entry: DeploymentEntry{PluginID: detail.ID, PluginVersion: detail.Version, SubItemRef: plugin.SubItemRef{Type: plugin.SubItemTypeHook, Name: plugin.HookAssetsSubItemPrefix + filepath.ToSlash(relPath)}, TargetPath: filepath.ToSlash(filepath.Join(claudeHooksAssetsDir(), relPath)), MergeType: MergeTypeExclusive, Status: DeploymentStatusActive, DeployedAt: now, SourceScope: sourceScope}, SourcePath: path})
		return nil
	})
	return files, err
}

func marshalHooksFile(contribs []hookContribution) ([]byte, error) {
	payload := map[string][]map[string]interface{}{}
	for _, contribution := range contribs {
		payload[contribution.Info.Event] = append(payload[contribution.Info.Event], map[string]interface{}{"hooks": []map[string]string{{"type": contribution.Info.Type, "command": contribution.Info.Command}}})
	}
	b, err := json.MarshalIndent(map[string]interface{}{"hooks": payload}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal hooks payload: %w", err)
	}
	return append(b, '\n'), nil
}

func claudeTargetPath(item plugin.SubItem, workspaceMode bool) string {
	switch item.Type {
	case plugin.SubItemTypeSkill:
		if workspaceMode {
			return filepath.Join(".claude", filepath.FromSlash(item.Path))
		}
		return filepath.FromSlash(item.Path)
	case plugin.SubItemTypeAgent, plugin.SubItemTypeCommand:
		return filepath.FromSlash(item.Path)
	case plugin.SubItemTypeHook:
		return claudeHooksConfigPath()
	case plugin.SubItemTypeMCP:
		return claudeMCPPath()
	default:
		return filepath.FromSlash(item.Path)
	}
}

func claudeBaselineTargetPath() string { return "CLAUDE.md" }
func claudeMCPPath() string            { return ".mcp.json" }
func claudeHooksConfigPath() string    { return filepath.Join("hooks", "hooks.json") }
func claudeHooksAssetsDir() string     { return "hooks" }

func cloneJSONValue(value interface{}) interface{} {
	b, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return value
	}
	return out
}
