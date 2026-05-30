package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/plugin"
)

func setWorkspaceTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("NPM_CONFIG_PREFIX", filepath.Join(home, "npm-prefix"))
	if volume := filepath.VolumeName(home); volume != "" {
		t.Setenv("HOMEDRIVE", volume)
		t.Setenv("HOMEPATH", strings.TrimPrefix(home, volume))
	}
	return home
}

func writeWorkspaceTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setupWorkspaceTestServices(t *testing.T) (*Service, *plugin.Service, string, string, string) {
	t.Helper()
	home := setWorkspaceTestHome(t)
	configDir := filepath.Join(home, ".amagi-codebox")
	pluginDir := filepath.Join(home, ".claude", "plugins", "sample-plugin")
	workspaceDir := filepath.Join(home, "workspace")
	now := time.Now().UTC().Format(time.RFC3339)
	pluginID := "sample-plugin@market"

	installed := map[string]interface{}{
		"version": 1,
		"plugins": map[string][]map[string]string{
			pluginID: {{
				"scope":       "user",
				"installPath": pluginDir,
				"version":     "1.0.0",
				"installedAt": now,
				"lastUpdated": now,
			}},
		},
	}
	settings := map[string]interface{}{"enabledPlugins": map[string]bool{pluginID: true}}
	pluginManifest := map[string]interface{}{"name": "sample-plugin", "version": "1.0.0", "description": "sample"}

	mustJSONWrite := func(path string, value interface{}) {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", path, err)
		}
		writeWorkspaceTestFile(t, path, append(b, '\n'))
	}
	mustJSONWrite(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), installed)
	mustJSONWrite(filepath.Join(home, ".claude", "settings.json"), settings)
	mustJSONWrite(filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), pluginManifest)
	writeWorkspaceTestFile(t, filepath.Join(pluginDir, "skills", "review", "SKILL.md"), []byte("---\nname: review\n---\nReview skill\n"))
	writeWorkspaceTestFile(t, filepath.Join(pluginDir, "commands", "run.md"), []byte("# run\n"))
	mustJSONWrite(filepath.Join(pluginDir, ".mcp.json"), map[string]interface{}{"mcpServers": map[string]interface{}{"sample": map[string]interface{}{"command": "node", "args": []string{"server.js"}}}})
	writeWorkspaceTestFile(t, filepath.Join(pluginDir, "CLAUDE.md"), []byte("workspace baseline\n"))
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	pluginSvc := plugin.NewService(filepath.Join(home, ".claude"), nil)
	workspaceSvc := NewService(configDir, pluginSvc, nil)
	if err := workspaceSvc.Load(); err != nil {
		t.Fatalf("load workspace service: %v", err)
	}
	return workspaceSvc, pluginSvc, pluginID, pluginDir, workspaceDir
}

func TestBuildScaffoldPartialSelectionDoesNotDeployClaudeMd(t *testing.T) {
	workspaceSvc, _, pluginID, _, workspaceDir := setupWorkspaceTestServices(t)
	ws, err := workspaceSvc.CreateWorkspace("test", workspaceDir, []ToolType{ToolTypeClaude})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workspaceSvc.SetWorkspacePlugins(ws.ID, []WorkspacePlugin{{PluginID: pluginID, EnabledSubItems: []plugin.SubItemRef{{Type: plugin.SubItemTypeCommand, Name: "run"}}, DeployScope: string(SourceScopeWorkspace)}}); err != nil {
		t.Fatalf("set workspace plugins: %v", err)
	}
	result, err := workspaceSvc.BuildScaffold(ws.ID)
	if err != nil {
		t.Fatalf("build scaffold: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %#v", result.Conflicts)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "commands", "run.md")); err != nil {
		t.Fatalf("expected command file, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("partial selection should not deploy CLAUDE.md, stat err=%v", err)
	}
	manifest, err := workspaceSvc.GetDeploymentManifest(ws.ID)
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	for _, entry := range manifest.Entries {
		if entry.SubItemRef.Type == plugin.SubItemTypeClaude {
			t.Fatalf("partial selection should not contain claude manifest entry: %#v", entry)
		}
	}
}

func TestSetGlobalEnabledMigratesWorkspaceOwnershipToOrphaned(t *testing.T) {
	workspaceSvc, _, pluginID, _, workspaceDir := setupWorkspaceTestServices(t)
	ws, err := workspaceSvc.CreateWorkspace("test", workspaceDir, []ToolType{ToolTypeClaude})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workspaceSvc.SetWorkspacePlugins(ws.ID, []WorkspacePlugin{{PluginID: pluginID, DeployScope: string(SourceScopeWorkspace)}}); err != nil {
		t.Fatalf("set workspace plugins: %v", err)
	}
	if _, err := workspaceSvc.BuildScaffold(ws.ID); err != nil {
		t.Fatalf("build scaffold: %v", err)
	}
	_, err = workspaceSvc.SetGlobalEnabled([]GlobalEnabled{{PluginID: pluginID, EnabledAll: false, EnabledSubItems: []plugin.SubItemRef{{Type: plugin.SubItemTypeSkill, Name: "review"}}, Tools: []ToolType{ToolTypeClaude}}})
	if err != nil {
		t.Fatalf("set global enabled: %v", err)
	}
	updated, err := workspaceSvc.GetWorkspace(ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if len(updated.Plugins) != 1 {
		t.Fatalf("expected one remaining workspace plugin, got %#v", updated.Plugins)
	}
	expectedRefs := []plugin.SubItemRef{{Type: plugin.SubItemTypeCommand, Name: "run"}, {Type: plugin.SubItemTypeMCP, Name: "sample"}}
	if !sameSubItemRefSet(updated.Plugins[0].EnabledSubItems, expectedRefs) {
		t.Fatalf("workspace plugin selection should retain command+mcp after skill migration, got %#v", updated.Plugins[0].EnabledSubItems)
	}
	manifest, err := workspaceSvc.GetDeploymentManifest(ws.ID)
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	statuses := map[string]DeploymentStatus{}
	for _, entry := range manifest.Entries {
		statuses[entry.SubItemRef.Key()] = entry.Status
	}
	if statuses[plugin.SubItemRef{Type: plugin.SubItemTypeSkill, Name: "review"}.Key()] != DeploymentStatusOrphaned {
		t.Fatalf("skill entry should be orphaned, got %#v", statuses)
	}
	if statuses[plugin.SubItemRef{Type: plugin.SubItemTypeCommand, Name: "run"}.Key()] != DeploymentStatusActive {
		t.Fatalf("command entry should remain active, got %#v", statuses)
	}
	if statuses[plugin.SubItemRef{Type: plugin.SubItemTypeClaude, Name: plugin.ClaudeBaselineSubItemName}.Key()] != DeploymentStatusOrphaned {
		t.Fatalf("claude baseline should become orphaned after whole->partial migration, got %#v", statuses)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, ".claude", "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("orphaned skill file should remain on disk, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "CLAUDE.md")); err != nil {
		t.Fatalf("orphaned CLAUDE.md should remain on disk, stat err=%v", err)
	}
	if _, err := workspaceSvc.SyncWorkspace(ws.ID); err != nil {
		t.Fatalf("sync workspace: %v", err)
	}
	manifest, err = workspaceSvc.GetDeploymentManifest(ws.ID)
	if err != nil {
		t.Fatalf("get manifest after sync: %v", err)
	}
	statuses = map[string]DeploymentStatus{}
	for _, entry := range manifest.Entries {
		statuses[entry.SubItemRef.Key()] = entry.Status
	}
	if statuses[plugin.SubItemRef{Type: plugin.SubItemTypeSkill, Name: "review"}.Key()] != DeploymentStatusOrphaned || statuses[plugin.SubItemRef{Type: plugin.SubItemTypeClaude, Name: plugin.ClaudeBaselineSubItemName}.Key()] != DeploymentStatusOrphaned {
		t.Fatalf("orphaned entries should survive sync, got %#v", statuses)
	}
}

func TestSetGlobalEnabledRejectsEmptyPartialSelection(t *testing.T) {
	workspaceSvc, _, pluginID, _, _ := setupWorkspaceTestServices(t)
	_, err := workspaceSvc.SetGlobalEnabled([]GlobalEnabled{{PluginID: pluginID, EnabledAll: false, Tools: []ToolType{ToolTypeClaude}}})
	if err == nil {
		t.Fatal("expected partial global selection without subitems to fail")
	}
}

func TestGetAvailablePluginsForWorkspaceSkipsBrokenPlugin(t *testing.T) {
	workspaceSvc, _, pluginID, pluginDir, workspaceDir := setupWorkspaceTestServices(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("get user home dir: %v", err)
	}
	brokenPluginID := "broken-plugin@market"
	brokenPluginDir := filepath.Join(home, ".claude", "plugins", "broken-plugin")
	now := time.Now().UTC().Format(time.RFC3339)

	mustJSONWrite := func(path string, value interface{}) {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", path, err)
		}
		writeWorkspaceTestFile(t, path, append(b, '\n'))
	}

	installed := map[string]interface{}{
		"version": 1,
		"plugins": map[string][]map[string]string{
			pluginID: {{
				"scope":       "user",
				"installPath": pluginDir,
				"version":     "1.0.0",
				"installedAt": now,
				"lastUpdated": now,
			}},
			brokenPluginID: {{
				"scope":       "user",
				"installPath": brokenPluginDir,
				"version":     "1.0.0",
				"installedAt": now,
				"lastUpdated": now,
			}},
		},
	}
	settings := map[string]interface{}{
		"enabledPlugins": map[string]bool{
			pluginID:       true,
			brokenPluginID: true,
		},
	}
	mustJSONWrite(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), installed)
	mustJSONWrite(filepath.Join(home, ".claude", "settings.json"), settings)

	ws, err := workspaceSvc.CreateWorkspace("test", workspaceDir, []ToolType{ToolTypeClaude})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	available, err := workspaceSvc.GetAvailablePluginsForWorkspace(ws.ID)
	if err != nil {
		t.Fatalf("get available plugins for workspace: %v", err)
	}
	if len(available) != 1 {
		t.Fatalf("expected one available plugin after skipping broken plugin, got %#v", available)
	}
	if available[0].ID != pluginID {
		t.Fatalf("expected available plugin %s, got %#v", pluginID, available[0])
	}
	if len(available[0].SubItems) == 0 {
		t.Fatalf("expected surviving plugin to retain subitems, got %#v", available[0])
	}
}

func TestSetGlobalEnabledPreflightPreventsHalfSuccessOnBrokenWorkspacePlugin(t *testing.T) {
	workspaceSvc, _, pluginID, _, workspaceDir := setupWorkspaceTestServices(t)
	ws, err := workspaceSvc.CreateWorkspace("test", workspaceDir, []ToolType{ToolTypeClaude})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workspaceSvc.SetWorkspacePlugins(ws.ID, []WorkspacePlugin{{PluginID: pluginID, DeployScope: string(SourceScopeWorkspace)}}); err != nil {
		t.Fatalf("set workspace plugins: %v", err)
	}
	workspaceSvc.mu.Lock()
	for i := range workspaceSvc.workspaces {
		if workspaceSvc.workspaces[i].ID == ws.ID {
			workspaceSvc.workspaces[i].Plugins = append(workspaceSvc.workspaces[i].Plugins, WorkspacePlugin{PluginID: "broken-plugin@market", DeployScope: string(SourceScopeWorkspace)})
		}
	}
	workspaceSvc.mu.Unlock()

	_, err = workspaceSvc.SetGlobalEnabled([]GlobalEnabled{{PluginID: pluginID, EnabledAll: true, Tools: []ToolType{ToolTypeClaude}}})
	if err == nil {
		t.Fatal("expected global enabled preflight to fail on broken workspace plugin detail")
	}

	globalManifest, err := ReadManifest(workspaceSvc.globalManifestPath)
	if err != nil {
		t.Fatalf("read global manifest: %v", err)
	}
	if len(globalManifest.Entries) != 0 {
		t.Fatalf("global manifest should stay empty when preflight fails, got %#v", globalManifest.Entries)
	}
	if _, err := os.Stat(workspaceSvc.globalEnabledPath); !os.IsNotExist(err) {
		t.Fatalf("global enabled file should not be written on preflight failure, stat err=%v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("global CLAUDE.md should not be deployed on preflight failure, stat err=%v", err)
	}
}

func TestBuildScaffoldCursorDeploysRulesAndMCPWithWarnings(t *testing.T) {
	workspaceSvc, _, pluginID, _, workspaceDir := setupWorkspaceTestServices(t)
	ws, err := workspaceSvc.CreateWorkspace("cursor", workspaceDir, []ToolType{ToolTypeCursor})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workspaceSvc.SetWorkspacePlugins(ws.ID, []WorkspacePlugin{{PluginID: pluginID, DeployScope: string(SourceScopeWorkspace)}}); err != nil {
		t.Fatalf("set workspace plugins: %v", err)
	}
	result, err := workspaceSvc.BuildScaffold(ws.ID)
	if err != nil {
		t.Fatalf("build scaffold: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %#v", result.Conflicts)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected warnings for unsupported cursor resources")
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, ".cursor", "rules", "sample-plugin.md")); err != nil {
		t.Fatalf("expected cursor rules file, stat err=%v", err)
	}
	mcpPath := filepath.Join(workspaceDir, ".cursor", "mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read cursor mcp file: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal cursor mcp payload: %v", err)
	}
	servers, ok := payload["mcpServers"].(map[string]interface{})
	if !ok || servers["sample"] == nil {
		t.Fatalf("cursor mcp should contain sample server, got %#v", payload)
	}
	manifest, err := workspaceSvc.GetDeploymentManifest(ws.ID)
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	foundRule := false
	foundMCP := false
	for _, entry := range manifest.Entries {
		if entry.TargetPath == filepath.ToSlash(filepath.Join(".cursor", "rules", "sample-plugin.md")) {
			foundRule = true
		}
		if entry.TargetPath == filepath.ToSlash(filepath.Join(".cursor", "mcp.json")) {
			foundMCP = true
		}
	}
	if !foundRule || !foundMCP {
		t.Fatalf("expected cursor manifest entries, got %#v", manifest.Entries)
	}
}
