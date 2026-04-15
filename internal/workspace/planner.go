package workspace

import (
	"amagi-codebox/internal/plugin"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		for _, item := range workspace.Plugins {
			detail, err := s.plugins.GetPluginDetail(item.PluginID)
			if err != nil {
				return deploymentPlan{}, warnings, err
			}
			toolPlan, toolWarnings, err := s.buildToolPluginPlan(tool, detail, SourceScopeWorkspace, item.EnabledSubItems, len(item.EnabledSubItems) == 0, true)
			if err != nil {
				return deploymentPlan{}, warnings, err
			}
			plan = appendDeploymentPlan(plan, toolPlan)
			warnings = append(warnings, toolWarnings...)
		}
	}
	return plan, warnings, nil
}

func (s *Service) buildGlobalPlan(entries []GlobalEnabled) (deploymentPlan, []string, error) {
	plan := deploymentPlan{}
	warnings := []string{}
	for _, entry := range entries {
		detail, err := s.plugins.GetPluginDetail(entry.PluginID)
		if err != nil {
			return deploymentPlan{}, warnings, err
		}
		for _, tool := range entry.Tools {
			toolPlan, toolWarnings, err := s.buildToolPluginPlan(tool, detail, SourceScopeGlobal, entry.EnabledSubItems, entry.EnabledAll, false)
			if err != nil {
				return deploymentPlan{}, warnings, err
			}
			plan = appendDeploymentPlan(plan, toolPlan)
			warnings = append(warnings, toolWarnings...)
		}
	}
	return plan, warnings, nil
}

func appendDeploymentPlan(dst, src deploymentPlan) deploymentPlan {
	dst.Files = append(dst.Files, src.Files...)
	dst.Merged = append(dst.Merged, src.Merged...)
	return dst
}

func (s *Service) buildToolPluginPlan(tool ToolType, detail *plugin.PluginDetail, sourceScope SourceScope, refs []plugin.SubItemRef, includeAll bool, workspaceMode bool) (deploymentPlan, []string, error) {
	adapter := newAdapterForTool(tool, s.homeDir)
	selected, err := resolveSelectedSubItems(detail, refs, includeAll)
	if err != nil {
		return deploymentPlan{}, nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	plan := deploymentPlan{}
	warnings := []string{}
	var mcpEntries []DeploymentEntry
	mcpServers := map[string]interface{}{}
	var hookEntries []DeploymentEntry
	var hookContribs []hookContribution
	for index, item := range selected {
		support := adapter.SupportsType(item.Type)
		if support == SupportLevelUnsupported {
			warnings = append(warnings, unsupportedResourceWarning(tool, detail.ID, item.Type, item.Name))
			continue
		}
		entry := DeploymentEntry{PluginID: detail.ID, PluginVersion: detail.Version, SubItemRef: plugin.SubItemRef{Type: item.Type, Name: item.Name}, MergeType: MergeTypeExclusive, Status: DeploymentStatusActive, DeployedAt: now, SourceScope: sourceScope}
		sourcePath := filepath.Join(detail.InstallPath, filepath.FromSlash(item.Path))
		switch tool {
		case ToolTypeClaude:
			addClaudeItem(detail, item, &entry, sourcePath, index, workspaceMode, &plan, &mcpEntries, mcpServers, &hookEntries, &hookContribs)
		case ToolTypeCursor:
			if item.Type == plugin.SubItemTypeMCP {
				entry.MergeType = MergeTypeMerged
				entry.TargetPath = filepath.ToSlash(cursorMCPPath(workspaceMode))
				entry.ContentMarker = "keys:" + item.Name
				entry.MergeOrder = index
				mcpEntries = append(mcpEntries, entry)
				mcpServers[item.Name] = cloneJSONValue(detail.MCPServers[item.Name])
			}
		case ToolTypeOpenCode:
			switch item.Type {
			case plugin.SubItemTypeSkill:
				entry.TargetPath = filepath.ToSlash(openCodeSkillPath(item, workspaceMode))
				plan.Files = append(plan.Files, plannedFile{Entry: entry, SourcePath: sourcePath})
			case plugin.SubItemTypeAgent:
				entry.TargetPath = filepath.ToSlash(openCodeAgentPath(item, workspaceMode))
				plan.Files = append(plan.Files, plannedFile{Entry: entry, SourcePath: sourcePath})
			case plugin.SubItemTypeMCP:
				entry.MergeType = MergeTypeMerged
				entry.TargetPath = filepath.ToSlash(openCodeMCPPath(workspaceMode))
				entry.ContentMarker = "keys:" + item.Name
				entry.MergeOrder = index
				mcpEntries = append(mcpEntries, entry)
				mcpServers[item.Name] = cloneJSONValue(detail.MCPServers[item.Name])
			}
		case ToolTypeVSCode:
			if item.Type == plugin.SubItemTypeMCP {
				if !workspaceMode {
					warnings = append(warnings, unsupportedResourceWarning(tool, detail.ID, item.Type, item.Name))
					continue
				}
				entry.MergeType = MergeTypeMerged
				entry.TargetPath = filepath.ToSlash(vscodeMCPPath())
				entry.ContentMarker = "keys:" + item.Name
				entry.MergeOrder = index
				mcpEntries = append(mcpEntries, entry)
				mcpServers[item.Name] = cloneJSONValue(detail.MCPServers[item.Name])
			}
		}
	}
	if detail.HasClaudeMd && includeAll {
		baselineWarnings, baselinePlan, err := s.buildToolBaselinePlan(tool, detail, sourceScope, now, workspaceMode)
		if err != nil {
			return deploymentPlan{}, warnings, err
		}
		warnings = append(warnings, baselineWarnings...)
		plan = appendDeploymentPlan(plan, baselinePlan)
	}
	if len(mcpEntries) > 0 {
		payload, err := marshalMCPPayload(mcpServers)
		if err != nil {
			return deploymentPlan{}, warnings, err
		}
		plan.Merged = append(plan.Merged, plannedMergedFile{TargetPath: mcpEntries[0].TargetPath, Entries: mcpEntries, Content: payload})
	}
	if len(hookEntries) > 0 {
		payload, err := marshalHooksFile(hookContribs)
		if err != nil {
			return deploymentPlan{}, warnings, err
		}
		plan.Merged = append(plan.Merged, plannedMergedFile{TargetPath: hookEntries[0].TargetPath, Entries: hookEntries, Content: payload})
		assets, err := collectHookAssets(detail, sourceScope, now)
		if err != nil {
			return deploymentPlan{}, warnings, err
		}
		plan.Files = append(plan.Files, assets...)
	}
	return plan, warnings, nil
}
func addClaudeItem(detail *plugin.PluginDetail, item plugin.SubItem, entry *DeploymentEntry, sourcePath string, index int, workspaceMode bool, plan *deploymentPlan, mcpEntries *[]DeploymentEntry, mcpServers map[string]interface{}, hookEntries *[]DeploymentEntry, hookContribs *[]hookContribution) {
	targetPath := claudeTargetPath(item, workspaceMode)
	switch item.Type {
	case plugin.SubItemTypeSkill, plugin.SubItemTypeAgent, plugin.SubItemTypeCommand:
		entry.TargetPath = filepath.ToSlash(targetPath)
		plan.Files = append(plan.Files, plannedFile{Entry: *entry, SourcePath: sourcePath})
	case plugin.SubItemTypeMCP:
		entry.MergeType = MergeTypeMerged
		entry.TargetPath = filepath.ToSlash(targetPath)
		entry.ContentMarker = "keys:" + item.Name
		entry.MergeOrder = index
		*mcpEntries = append(*mcpEntries, *entry)
		mcpServers[item.Name] = cloneJSONValue(detail.MCPServers[item.Name])
	case plugin.SubItemTypeHook:
		info, ok := findHookInfo(detail.Hooks, item.Name)
		if !ok {
			return
		}
		entry.MergeType = MergeTypeMerged
		entry.TargetPath = filepath.ToSlash(targetPath)
		entry.ContentMarker = item.Name
		entry.MergeOrder = index
		*hookEntries = append(*hookEntries, *entry)
		*hookContribs = append(*hookContribs, hookContribution{Info: info, Entry: *entry})
	}
}

func (s *Service) buildToolBaselinePlan(tool ToolType, detail *plugin.PluginDetail, sourceScope SourceScope, now string, workspaceMode bool) ([]string, deploymentPlan, error) {
	warnings := []string{}
	plan := deploymentPlan{}
	adapter := newAdapterForTool(tool, s.homeDir)
	if adapter.SupportsType(plugin.SubItemTypeClaude) == SupportLevelUnsupported {
		return []string{unsupportedResourceWarning(tool, detail.ID, plugin.SubItemTypeClaude, plugin.ClaudeBaselineSubItemName)}, plan, nil
	}
	entry := DeploymentEntry{PluginID: detail.ID, PluginVersion: detail.Version, SubItemRef: plugin.SubItemRef{Type: plugin.SubItemTypeClaude, Name: plugin.ClaudeBaselineSubItemName}, MergeType: MergeTypeExclusive, Status: DeploymentStatusActive, DeployedAt: now, SourceScope: sourceScope}
	sourcePath := filepath.Join(detail.InstallPath, filepath.FromSlash(detail.ClaudeMdPath))
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return warnings, plan, fmt.Errorf("read baseline source %s: %w", sourcePath, err)
	}
	switch tool {
	case ToolTypeClaude:
		entry.TargetPath = filepath.ToSlash(claudeBaselineTargetPath(workspaceMode))
		plan.Files = append(plan.Files, plannedFile{Entry: entry, SourcePath: sourcePath})
	case ToolTypeCursor:
		if !workspaceMode {
			warnings = append(warnings, unsupportedResourceWarning(tool, detail.ID, plugin.SubItemTypeClaude, plugin.ClaudeBaselineSubItemName))
			break
		}
		entry.TargetPath = filepath.ToSlash(cursorRulesPath(detail))
		plan.Files = append(plan.Files, plannedFile{Entry: entry, SourcePath: sourcePath, Content: content})
	case ToolTypeOpenCode:
		entry.MergeType = MergeTypeMerged
		entry.TargetPath = filepath.ToSlash(openCodeInstructionsPath(workspaceMode))
		entry.ContentMarker = markdownContentMarker(detail.ID)
		plan.Merged = append(plan.Merged, plannedMergedFile{TargetPath: entry.TargetPath, Entries: []DeploymentEntry{entry}, Content: buildMarkedMarkdownFragment(detail.ID, content)})
	case ToolTypeVSCode:
		if !workspaceMode {
			warnings = append(warnings, unsupportedResourceWarning(tool, detail.ID, plugin.SubItemTypeClaude, plugin.ClaudeBaselineSubItemName))
			break
		}
		entry.MergeType = MergeTypeMerged
		entry.TargetPath = filepath.ToSlash(vscodeInstructionsPath())
		entry.ContentMarker = markdownContentMarker(detail.ID)
		plan.Merged = append(plan.Merged, plannedMergedFile{TargetPath: entry.TargetPath, Entries: []DeploymentEntry{entry}, Content: buildMarkedMarkdownFragment(detail.ID, content)})
	}
	return warnings, plan, nil
}

func marshalMCPPayload(mcpServers map[string]interface{}) ([]byte, error) {
	b, err := json.MarshalIndent(map[string]interface{}{"mcpServers": mcpServers}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal mcp payload: %w", err)
	}
	return append(b, '\n'), nil
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
		files = append(files, plannedFile{Entry: DeploymentEntry{PluginID: detail.ID, PluginVersion: detail.Version, SubItemRef: plugin.SubItemRef{Type: plugin.SubItemTypeHook, Name: plugin.HookAssetsSubItemPrefix + filepath.ToSlash(relPath)}, TargetPath: filepath.ToSlash(filepath.Join(claudeHooksAssetsDir(workspaceScopePath(sourceScope)), relPath)), MergeType: MergeTypeExclusive, Status: DeploymentStatusActive, DeployedAt: now, SourceScope: sourceScope}, SourcePath: path})
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
		return filepath.Join(globalRelativeRoot(ToolTypeClaude), filepath.FromSlash(item.Path))
	case plugin.SubItemTypeAgent, plugin.SubItemTypeCommand:
		if workspaceMode {
			return filepath.FromSlash(item.Path)
		}
		return filepath.Join(globalRelativeRoot(ToolTypeClaude), filepath.FromSlash(item.Path))
	case plugin.SubItemTypeHook:
		return claudeHooksConfigPath(workspaceMode)
	case plugin.SubItemTypeMCP:
		return claudeMCPPath(workspaceMode)
	default:
		return filepath.FromSlash(item.Path)
	}
}

func claudeBaselineTargetPath(workspaceMode bool) string {
	if workspaceMode {
		return "CLAUDE.md"
	}
	return filepath.Join(globalRelativeRoot(ToolTypeClaude), "CLAUDE.md")
}

func claudeMCPPath(workspaceMode bool) string {
	if workspaceMode {
		return ".mcp.json"
	}
	return filepath.Join(globalRelativeRoot(ToolTypeClaude), ".mcp.json")
}

func claudeHooksConfigPath(workspaceMode bool) string {
	if workspaceMode {
		return filepath.Join("hooks", "hooks.json")
	}
	return filepath.Join(globalRelativeRoot(ToolTypeClaude), "hooks", "hooks.json")
}

func claudeHooksAssetsDir(workspaceMode bool) string {
	if workspaceMode {
		return "hooks"
	}
	return filepath.Join(globalRelativeRoot(ToolTypeClaude), "hooks")
}

func workspaceScopePath(scope SourceScope) bool { return scope == SourceScopeWorkspace }
func cursorRulesPath(detail *plugin.PluginDetail) string {
	return filepath.Join(".cursor", "rules", safePluginFileName(detail)+".md")
}

func cursorMCPPath(workspaceMode bool) string {
	if workspaceMode {
		return filepath.Join(".cursor", "mcp.json")
	}
	return filepath.Join(globalRelativeRoot(ToolTypeCursor), "mcp.json")
}

func openCodeSkillPath(item plugin.SubItem, workspaceMode bool) string {
	base := filepath.FromSlash(item.Path)
	if workspaceMode {
		return filepath.Join(".opencode", base)
	}
	return filepath.Join(globalRelativeRoot(ToolTypeOpenCode), base)
}

func openCodeAgentPath(item plugin.SubItem, workspaceMode bool) string {
	base := filepath.Join("agents", filepath.Base(filepath.FromSlash(item.Path)))
	if workspaceMode {
		return filepath.Join(".opencode", base)
	}
	return filepath.Join(globalRelativeRoot(ToolTypeOpenCode), base)
}

func openCodeInstructionsPath(workspaceMode bool) string {
	if workspaceMode {
		return "AGENTS.md"
	}
	return filepath.Join(globalRelativeRoot(ToolTypeOpenCode), "AGENTS.md")
}

func openCodeMCPPath(workspaceMode bool) string {
	if workspaceMode {
		return "opencode.json"
	}
	return filepath.Join(globalRelativeRoot(ToolTypeOpenCode), "opencode.json")
}

func vscodeInstructionsPath() string { return filepath.Join(".github", "copilot-instructions.md") }
func vscodeMCPPath() string          { return filepath.Join(".vscode", "mcp.json") }

func safePluginFileName(detail *plugin.PluginDetail) string {
	name := strings.TrimSpace(detail.Manifest.Name)
	if name == "" {
		name = strings.TrimSpace(detail.Name)
	}
	if name == "" {
		name = strings.TrimSpace(detail.ID)
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", "@", "-", ":", "-", " ", "-")
	name = replacer.Replace(name)
	name = strings.Trim(name, "-._")
	if name == "" {
		return "plugin"
	}
	return name
}

func markdownContentMarker(pluginID string) string {
	return "markdown:" + pluginID
}

func buildMarkedMarkdownFragment(pluginID string, body []byte) []byte {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		trimmed = "# Instructions"
	}
	content := fmt.Sprintf("<!-- amagi:%s:start -->\n%s\n<!-- amagi:%s:end -->\n", pluginID, trimmed, pluginID)
	return []byte(content)
}

func unsupportedResourceWarning(tool ToolType, pluginID string, itemType plugin.SubItemType, itemName string) string {
	return fmt.Sprintf("工具 %s 暂不支持部署插件 %s 的 %s 子项 %s，已跳过", tool, pluginID, itemType, itemName)
}

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
