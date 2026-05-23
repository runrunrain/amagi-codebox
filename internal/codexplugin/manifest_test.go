package codexplugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanCommandsParsesFrontmatterDescription(t *testing.T) {
	dir := t.TempDir()
	commandsDir := filepath.Join(dir, "commands")
	if err := os.MkdirAll(commandsDir, 0755); err != nil {
		t.Fatalf("mkdir commands dir: %v", err)
	}
	commandPath := filepath.Join(commandsDir, "deploy.md")
	content := []byte("---\ndescription: Deploy the plugin to Codex\n---\n\n# Deploy\n\nRuns deployment.\n")
	if err := os.WriteFile(commandPath, content, 0644); err != nil {
		t.Fatalf("write command file: %v", err)
	}

	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	commands, err := s.scanCommands(dir)
	if err != nil {
		t.Fatalf("scan commands: %v", err)
	}
	if len(commands) != 1 {
		t.Fatalf("expected 1 command, got %d: %+v", len(commands), commands)
	}
	if commands[0].Name != "deploy" || commands[0].Description != "Deploy the plugin to Codex" || commands[0].FilePath != commandPath {
		t.Fatalf("unexpected command info: %+v", commands[0])
	}
}

func TestScanCommandsFallsBackToFirstParagraph(t *testing.T) {
	dir := t.TempDir()
	commandsDir := filepath.Join(dir, "commands")
	if err := os.MkdirAll(commandsDir, 0755); err != nil {
		t.Fatalf("mkdir commands dir: %v", err)
	}
	commandPath := filepath.Join(commandsDir, "inspect.md")
	content := []byte("# Inspect\n\nInspect the plugin configuration.\nContinue with normalized details.\n\nSecond paragraph is ignored.\n")
	if err := os.WriteFile(commandPath, content, 0644); err != nil {
		t.Fatalf("write command file: %v", err)
	}

	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	commands, err := s.scanCommands(dir)
	if err != nil {
		t.Fatalf("scan commands: %v", err)
	}
	if len(commands) != 1 {
		t.Fatalf("expected 1 command, got %d: %+v", len(commands), commands)
	}
	expectedDescription := "Inspect the plugin configuration. Continue with normalized details."
	if commands[0].Name != "inspect" || commands[0].Description != expectedDescription || commands[0].FilePath != commandPath {
		t.Fatalf("unexpected command info: %+v", commands[0])
	}
}

func TestScanCommandsIgnoresNonMarkdownFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	commandsDir := filepath.Join(dir, "commands")
	if err := os.MkdirAll(filepath.Join(commandsDir, "nested"), 0755); err != nil {
		t.Fatalf("mkdir commands dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "valid.md"), []byte("Valid command description.\n"), 0644); err != nil {
		t.Fatalf("write valid command: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "ignored.txt"), []byte("Ignored command description.\n"), 0644); err != nil {
		t.Fatalf("write ignored command: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "nested", "ignored.md"), []byte("Nested command description.\n"), 0644); err != nil {
		t.Fatalf("write nested command: %v", err)
	}

	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	commands, err := s.scanCommands(dir)
	if err != nil {
		t.Fatalf("scan commands: %v", err)
	}
	if len(commands) != 1 {
		t.Fatalf("expected only 1 command, got %d: %+v", len(commands), commands)
	}
	if commands[0].Name != "valid" || commands[0].Description != "Valid command description." {
		t.Fatalf("unexpected command info: %+v", commands[0])
	}
}

func TestScanAgentsFallsBackToFileNameForGenericHeading(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir agents dir: %v", err)
	}
	agentPath := filepath.Join(agentsDir, "luban.md")
	content := []byte("# 一、角色定位\n\n编码工匠，负责生产级代码实现。\n")
	if err := os.WriteFile(agentPath, content, 0644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}

	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	agents, err := s.scanAgents(dir)
	if err != nil {
		t.Fatalf("scan agents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d: %+v", len(agents), agents)
	}
	if agents[0].Name != "luban" || agents[0].Description != "编码工匠，负责生产级代码实现。" || agents[0].FilePath != agentPath {
		t.Fatalf("unexpected agent info: %+v", agents[0])
	}
}

func TestScanAgentsUsesFirstMeaningfulHeadingAsName(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir agents dir: %v", err)
	}
	agentPath := filepath.Join(agentsDir, "reviewer.md")
	content := []byte("# 谛听 Reviewer\n\n负责代码审核、安全审计与质量守门。\n")
	if err := os.WriteFile(agentPath, content, 0644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}

	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	agents, err := s.scanAgents(dir)
	if err != nil {
		t.Fatalf("scan agents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d: %+v", len(agents), agents)
	}
	if agents[0].Name != "谛听 Reviewer" || agents[0].Description != "负责代码审核、安全审计与质量守门。" || agents[0].FilePath != agentPath {
		t.Fatalf("unexpected agent info: %+v", agents[0])
	}
}

func TestScanAgentsDescriptionSkipsMarkdownScaffolding(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir agents dir: %v", err)
	}
	agentPath := filepath.Join(agentsDir, "baize.md")
	content := []byte(`---
name: baize
---

# 白泽 Explorer

| 项目 | 说明 |
|------|------|
---
-

白泽负责代码探索、依赖分析与信息收集。

第二段忽略。
`)
	if err := os.WriteFile(agentPath, content, 0644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}

	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	agents, err := s.scanAgents(dir)
	if err != nil {
		t.Fatalf("scan agents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d: %+v", len(agents), agents)
	}
	if agents[0].Description != "白泽负责代码探索、依赖分析与信息收集。" || agents[0].FilePath != agentPath {
		t.Fatalf("unexpected agent info: %+v", agents[0])
	}
}

func TestScanAgentsDescriptionEmptyWhenNoNaturalParagraph(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir agents dir: %v", err)
	}
	content := []byte("# 角色定位\n\n| 项目 | 说明 |\n|------|------|\n---\n-\n```\nnot a description\n```\n")
	if err := os.WriteFile(filepath.Join(agentsDir, "empty.md"), content, 0644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}

	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	agents, err := s.scanAgents(dir)
	if err != nil {
		t.Fatalf("scan agents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d: %+v", len(agents), agents)
	}
	if agents[0].Name != "empty" || agents[0].Description != "" {
		t.Fatalf("unexpected agent info: %+v", agents[0])
	}
}

func TestScanAgentsPreservesFrontmatterName(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir agents dir: %v", err)
	}
	agentPath := filepath.Join(agentsDir, "baize.md")
	content := []byte("---\nname: baize-explorer\ndescription: Explorer agent for code research\n---\n\n# 一、角色定位\n\nFallback paragraph.\n")
	if err := os.WriteFile(agentPath, content, 0644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}

	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	agents, err := s.scanAgents(dir)
	if err != nil {
		t.Fatalf("scan agents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d: %+v", len(agents), agents)
	}
	if agents[0].Name != "baize-explorer" || agents[0].Description != "Explorer agent for code research" || agents[0].FilePath != agentPath {
		t.Fatalf("unexpected agent info: %+v", agents[0])
	}
}

func TestReadPluginManifestPrefersCodexManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".codex-plugin"), 0755); err != nil {
		t.Fatalf("mkdir codex manifest dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0755); err != nil {
		t.Fatalf("mkdir claude manifest dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte(`{"name":"claude"}`), 0644); err != nil {
		t.Fatalf("write claude manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".codex-plugin", "plugin.json"), []byte(`{"name":"codex","version":"1.0.0"}`), 0644); err != nil {
		t.Fatalf("write codex manifest: %v", err)
	}
	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	manifest, path, err := s.readPluginManifest(dir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.Name != "codex" || filepath.Base(filepath.Dir(path)) != ".codex-plugin" {
		t.Fatalf("unexpected manifest=%+v path=%s", manifest, path)
	}
}

func TestResolvePluginRootFromParentDir(t *testing.T) {
	codexDir := t.TempDir()
	pluginRoot := filepath.Join(codexDir, "plugins", "cache", "amagi-codex-marketplace", "amagi", "1.5.116")
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0755); err != nil {
		t.Fatalf("mkdir plugin root: %v", err)
	}
	manifestPath := filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")
	if err := os.WriteFile(manifestPath, []byte(`{"name":"amagi","version":"1.5.116"}`), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "skills", "demo"), 0755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}

	s := NewServiceWithDeps(codexDir, nil, nil, nil)
	root, resolvedManifestPath := s.resolvePluginRoot(filepath.Dir(pluginRoot), "", "amagi", "amagi-codex-marketplace")
	if root != pluginRoot || resolvedManifestPath != manifestPath {
		t.Fatalf("resolvePluginRoot did not select version root, root=%s manifest=%s", root, resolvedManifestPath)
	}
}

func TestResolvePluginRootFromManifestPath(t *testing.T) {
	dir := t.TempDir()
	pluginRoot := filepath.Join(dir, "amagi", "1.5.116")
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	manifestPath := filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")
	if err := os.WriteFile(manifestPath, []byte(`{"name":"amagi"}`), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	root, resolvedManifestPath := s.resolvePluginRoot("", manifestPath, "amagi", "amagi-codex-marketplace")
	if root != pluginRoot || resolvedManifestPath != manifestPath {
		t.Fatalf("resolvePluginRoot did not normalize manifest path, root=%s manifest=%s", root, resolvedManifestPath)
	}
}

func TestReadMCPConfigSupportsTopLevelServerMap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{"memory":{"type":"stdio"},"web":{"type":"http"}}`), 0644); err != nil {
		t.Fatalf("write mcp config: %v", err)
	}
	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	servers, err := s.readMCPConfig(dir)
	if err != nil {
		t.Fatalf("read mcp config: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("expected top-level mcp server map, got %+v", servers)
	}
}

func TestFindAvailablePluginsFallsBackToDefaultMarketplaceSnapshotPath(t *testing.T) {
	codexDir := t.TempDir()
	manifestDir := filepath.Join(codexDir, ".tmp", "marketplaces", "amagi-codex-marketplace", "plugins", "amagi", ".codex-plugin")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("mkdir marketplace manifest dir: %v", err)
	}
	manifestPath := filepath.Join(manifestDir, "plugin.json")
	if err := os.WriteFile(manifestPath, []byte(`{"name":"amagi","version":"1.2.3","description":"Amagi Codex plugin"}`), 0644); err != nil {
		t.Fatalf("write marketplace manifest: %v", err)
	}

	s := NewServiceWithDeps(codexDir, nil, nil, nil)
	available, err := s.findAvailablePlugins([]CodexMarketplace{{Name: "amagi-codex-marketplace"}})
	if err != nil {
		t.Fatalf("find available plugins: %v", err)
	}
	if len(available) != 1 {
		t.Fatalf("expected 1 available plugin, got %d: %+v", len(available), available)
	}
	plugin := available[0]
	if plugin.PluginID != "amagi@amagi-codex-marketplace" || plugin.SnapshotPath != filepath.Join(codexDir, ".tmp", "marketplaces", "amagi-codex-marketplace") || plugin.ManifestPath != manifestPath {
		t.Fatalf("unexpected available plugin: %+v", plugin)
	}
}

func TestFindAvailablePluginsEmptyMarketplacesReturnsEmptyNil(t *testing.T) {
	s := NewServiceWithDeps(t.TempDir(), nil, nil, nil)
	available, err := s.findAvailablePlugins(nil)
	if err != nil {
		t.Fatalf("find available plugins with empty marketplaces: %v", err)
	}
	if available == nil {
		t.Fatalf("expected non-nil empty available slice")
	}
	if len(available) != 0 {
		t.Fatalf("expected empty available plugins, got %+v", available)
	}
}
