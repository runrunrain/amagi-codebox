package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// catalog 测试：GetAvailablePlugins 的本地市场目录直读数据源。
// 回归背景（2026-11）：CLI `plugin list --json --available` 的 available 数组
// 只含未安装插件，导致插件全部装完的市场（如 vexel）显示「0 个可安装」、
// 市场页「已安装」页签永远为空。本地目录直读必须包含已安装条目。

func writeMarketplaceCatalogFixture(t *testing.T, claudeDir, name string, catalog marketplaceCatalogFile) {
	t.Helper()
	dir := filepath.Join(claudeDir, "plugins", "marketplaces", name, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir marketplace fixture: %v", err)
	}
	b, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marketplace.json"), b, 0o644); err != nil {
		t.Fatalf("write marketplace.json: %v", err)
	}
}

func writeKnownMarketplacesFixture(t *testing.T, claudeDir string, entries map[string]marketplaceFileEntry) {
	t.Helper()
	dir := filepath.Join(claudeDir, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugins fixture: %v", err)
	}
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal known marketplaces: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "known_marketplaces.json"), b, 0o644); err != nil {
		t.Fatalf("write known_marketplaces.json: %v", err)
	}
}

func writeSourceManifestFixture(t *testing.T, marketplaceDir, pluginName string, manifest PluginManifest) {
	t.Helper()
	dir := filepath.Join(marketplaceDir, "plugins", pluginName, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir source manifest fixture: %v", err)
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), b, 0o644); err != nil {
		t.Fatalf("write plugin.json: %v", err)
	}
}

// stubCLI 返回固定 CLI 结果的 execCLI 注入替身。
func stubCLI(result *CommandResult, err error) func(args ...string) (*CommandResult, error) {
	return func(args ...string) (*CommandResult, error) {
		return result, err
	}
}

func TestGetAvailablePluginsIncludesInstalledFromLocalCatalog(t *testing.T) {
	claudeDir := t.TempDir()
	marketplaceDir := filepath.Join(claudeDir, "plugins", "marketplaces", "vexel-plugins-marketplace")

	writeKnownMarketplacesFixture(t, claudeDir, map[string]marketplaceFileEntry{
		"vexel-plugins-marketplace": {
			Source:          marketplaceSource{Source: "git", URL: "https://github.com/runrunrain/vexel-plugins-marketplace.git"},
			InstallLocation: marketplaceDir,
		},
	})
	writeMarketplaceCatalogFixture(t, claudeDir, "vexel-plugins-marketplace", marketplaceCatalogFile{
		Plugins: []marketplacePluginEntry{
			{
				Name:        "writing-skills",
				Source:      "./plugins/writing-skills",
				Version:     "1.2.0",
				Description: "写作方法论通用技能集",
				Author:      map[string]string{"name": "Vexel", "url": "https://github.com/runrunrain"},
			},
			{
				Name:   "hanfeng-mod-development",
				Source: "./plugins/hanfeng-mod-development",
				Author: map[string]string{"name": "Vexel"},
			},
		},
	})
	// 目录条目缺 description 时从 source manifest 补全
	writeSourceManifestFixture(t, marketplaceDir, "hanfeng-mod-development", PluginManifest{
		Name:        "hanfeng-mod-development",
		Version:     "1.3.0",
		Description: "HanFeng 设计验证 Mod",
	})

	// 已安装注册表里 writing-skills 已装——目录直读不得剔除已安装条目
	installed := installedPluginsFile{
		Version: 2,
		Plugins: map[string][]installedPluginEntry{
			"writing-skills@vexel-plugins-marketplace": {{
				Scope:       "user",
				InstallPath: filepath.Join(claudeDir, "plugins", "cache", "vexel-plugins-marketplace", "writing-skills", "1.2.0"),
				Version:     "1.2.0",
			}},
		},
	}
	b, err := json.Marshal(installed)
	if err != nil {
		t.Fatalf("marshal installed plugins: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "plugins", "installed_plugins.json"), b, 0o644); err != nil {
		t.Fatalf("write installed_plugins.json: %v", err)
	}

	svc := NewService(claudeDir, nil)
	// CLI 完全不可用（例如 PATH 中无 claude）：本地目录仍能给出完整目录
	svc.execCLI = stubCLI(nil, os.ErrNotExist)

	raw, err := svc.GetAvailablePlugins()
	if err != nil {
		t.Fatalf("GetAvailablePlugins: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("expected 2 catalog entries (installed plugin must be included), got %d: %+v", len(raw), raw)
	}

	plugins := make(map[string]AvailablePlugin, len(raw))
	for _, item := range raw {
		plugin, ok := item.(AvailablePlugin)
		if !ok {
			t.Fatalf("expected AvailablePlugin item, got %T", item)
		}
		plugins[plugin.PluginID] = plugin
	}

	writing := plugins["writing-skills@vexel-plugins-marketplace"]
	if writing.MarketplaceName != "vexel-plugins-marketplace" || writing.Description == "" {
		t.Fatalf("writing-skills entry lost marketplace linkage or description: %+v", writing)
	}
	if author, ok := writing.Author.(string); !ok || author != "Vexel" {
		t.Fatalf("author object was not normalized to display name: %+v", writing.Author)
	}

	hanfeng := plugins["hanfeng-mod-development@vexel-plugins-marketplace"]
	if hanfeng.Description != "HanFeng 设计验证 Mod" || hanfeng.Version != "1.3.0" {
		t.Fatalf("missing catalog description was not filled from source manifest: %+v", hanfeng)
	}
}

func TestGetAvailablePluginsMergesInstallCountsFromCLI(t *testing.T) {
	claudeDir := t.TempDir()
	marketplaceDir := filepath.Join(claudeDir, "plugins", "marketplaces", "vexel-plugins-marketplace")

	writeKnownMarketplacesFixture(t, claudeDir, map[string]marketplaceFileEntry{
		"vexel-plugins-marketplace": {InstallLocation: marketplaceDir},
	})
	writeMarketplaceCatalogFixture(t, claudeDir, "vexel-plugins-marketplace", marketplaceCatalogFile{
		Plugins: []marketplacePluginEntry{
			{Name: "writing-skills", Source: "./plugins/writing-skills"},
			{Name: "hanfeng-mod-development", Source: "./plugins/hanfeng-mod-development"},
		},
	})

	// CLI envelope 输出：只含未安装子集，但带 installCount
	cliOutput := `{"installed":[{"id":"writing-skills@vexel-plugins-marketplace"}],` +
		`"available":[{"pluginId":"hanfeng-mod-development@vexel-plugins-marketplace",` +
		`"name":"hanfeng-mod-development","marketplaceName":"vexel-plugins-marketplace","installCount":42}]}`
	svc := NewService(claudeDir, nil)
	svc.execCLI = stubCLI(&CommandResult{Success: true, Output: cliOutput}, nil)

	raw, err := svc.GetAvailablePlugins()
	if err != nil {
		t.Fatalf("GetAvailablePlugins: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("expected 2 catalog entries, got %d", len(raw))
	}
	for _, item := range raw {
		plugin := item.(AvailablePlugin)
		switch plugin.Name {
		case "hanfeng-mod-development":
			if plugin.InstallCount != 42 {
				t.Fatalf("installCount not merged from CLI output: %+v", plugin)
			}
		case "writing-skills":
			if plugin.InstallCount != 0 {
				t.Fatalf("installed plugin should keep zero installCount (CLI has no data): %+v", plugin)
			}
		}
	}
}

func TestGetAvailablePluginsFallbackFiltersUnregisteredMarketplaces(t *testing.T) {
	claudeDir := t.TempDir()
	marketplaceDir := filepath.Join(claudeDir, "plugins", "marketplaces", "vexel-plugins-marketplace")

	// 已注册市场存在但其本地目录 clone 丢失 → 本地直读失败，降级走 CLI 输出
	writeKnownMarketplacesFixture(t, claudeDir, map[string]marketplaceFileEntry{
		"vexel-plugins-marketplace": {InstallLocation: marketplaceDir},
	})

	// 旧版 CLI bare-array 形状：混入 claude.ai 插件目录条目（marketplaceName 不属于任何已注册市场）
	cliOutput := `[
		{"pluginId":"writing-skills@vexel-plugins-marketplace","name":"writing-skills","marketplaceName":"vexel-plugins-marketplace","installCount":7},
		{"pluginId":"superpowers@claude-plugin-directory","name":"superpowers","marketplaceName":"claude-plugin-directory","installCount":9001}
	]`
	svc := NewService(claudeDir, nil)
	svc.execCLI = stubCLI(&CommandResult{Success: true, Output: cliOutput}, nil)

	raw, err := svc.GetAvailablePlugins()
	if err != nil {
		t.Fatalf("GetAvailablePlugins: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected unregistered directory entries to be filtered out, got %d: %+v", len(raw), raw)
	}
	plugin := raw[0].(AvailablePlugin)
	if plugin.PluginID != "writing-skills@vexel-plugins-marketplace" {
		t.Fatalf("unexpected surviving entry: %+v", plugin)
	}
}

func TestGetAvailablePluginsToleratesObjectSourceEntries(t *testing.T) {
	// 官方市场真实形状：173/223 条目的 source 是 {source,url,path,ref,sha} 对象
	//（git-subdir 外部引用），不得让整个 catalog 解析失败被跳过
	claudeDir := t.TempDir()
	marketplaceDir := filepath.Join(claudeDir, "plugins", "marketplaces", "claude-plugins-official")

	writeKnownMarketplacesFixture(t, claudeDir, map[string]marketplaceFileEntry{
		"claude-plugins-official": {InstallLocation: marketplaceDir},
	})
	catalogJSON := `{"name":"claude-plugins-official","plugins":[
		{"name":"42crunch-api-security-testing","description":"API security plugin","source":{"source":"git-subdir","url":"https://github.com/42Crunch-AI/claude-plugins.git","path":"plugins/api-security-testing"}},
		{"name":"frontend-design","description":"Frontend design skill","source":"./plugins/frontend-design"}
	]}`
	dir := filepath.Join(marketplaceDir, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marketplace.json"), []byte(catalogJSON), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	svc := NewService(claudeDir, nil)
	svc.execCLI = stubCLI(nil, os.ErrNotExist)

	raw, err := svc.GetAvailablePlugins()
	if err != nil {
		t.Fatalf("GetAvailablePlugins: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("object-source entry must not drop the whole catalog, got %d entries", len(raw))
	}
	for _, item := range raw {
		plugin := item.(AvailablePlugin)
		if plugin.Description == "" || plugin.Name == "" {
			t.Fatalf("entry lost inline fields: %+v", plugin)
		}
	}
}

func TestGetAvailablePluginsMergesFailedMarketsFromCLI(t *testing.T) {
	// F05 回归：市场 A 本地可读、市场 B clone 丢失——B 的条目必须从 CLI
	// available（未安装子集）补齐，不能因其它市场可读而被丢弃退回显示 0
	claudeDir := t.TempDir()
	dirA := filepath.Join(claudeDir, "plugins", "marketplaces", "market-a")
	dirB := filepath.Join(claudeDir, "plugins", "marketplaces", "market-b") // 不创建：模拟 clone 丢失

	writeKnownMarketplacesFixture(t, claudeDir, map[string]marketplaceFileEntry{
		"market-a": {InstallLocation: dirA},
		"market-b": {InstallLocation: dirB},
	})
	writeMarketplaceCatalogFixture(t, claudeDir, "market-a", marketplaceCatalogFile{
		Plugins: []marketplacePluginEntry{
			{Name: "installed-one", Source: "./plugins/installed-one", Version: "1.0.0"},
			{Name: "other-two", Source: "./plugins/other-two"},
		},
	})

	cliOutput := `{"installed":[],"available":[
		{"pluginId":"from-b@market-b","name":"from-b","marketplaceName":"market-b","installCount":9}
	]}`
	svc := NewService(claudeDir, nil)
	svc.execCLI = stubCLI(&CommandResult{Success: true, Output: cliOutput}, nil)

	raw, err := svc.GetAvailablePlugins()
	if err != nil {
		t.Fatalf("GetAvailablePlugins: %v", err)
	}
	seen := map[string]bool{}
	for _, item := range raw {
		plugin := item.(AvailablePlugin)
		seen[plugin.PluginID] = true
		if plugin.PluginID == "from-b@market-b" && plugin.InstallCount != 9 {
			t.Fatalf("failed-market CLI entry must keep its installCount: %+v", plugin)
		}
	}
	// A 的完整目录（含已安装条目）+ B 的 CLI 补齐条目，一个都不能少
	for _, want := range []string{"installed-one@market-a", "other-two@market-a", "from-b@market-b"} {
		if !seen[want] {
			t.Fatalf("missing entry %s in hybrid availability result: %v", want, seen)
		}
	}
	if len(raw) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(raw))
	}
}

func TestParseCLIAvailableResultSupportsBothShapes(t *testing.T) {
	bare := &CommandResult{Success: true, Output: `[{"pluginId":"a@mk","name":"a","marketplaceName":"mk"}]`}
	plugins, err := parseCLIAvailableResult(bare)
	if err != nil || len(plugins) != 1 || plugins[0].PluginID != "a@mk" {
		t.Fatalf("bare array parse failed: %v %+v", err, plugins)
	}

	envelope := &CommandResult{Success: true, Output: `{"installed":[],"available":[{"pluginId":"b@mk","name":"b","marketplaceName":"mk"}]}`}
	plugins, err = parseCLIAvailableResult(envelope)
	if err != nil || len(plugins) != 1 || plugins[0].PluginID != "b@mk" {
		t.Fatalf("envelope parse failed: %v %+v", err, plugins)
	}

	empty := &CommandResult{Success: true, Output: `{"installed":[],"available":[]}`}
	if plugins, err = parseCLIAvailableResult(empty); err != nil || len(plugins) != 0 {
		t.Fatalf("empty envelope should parse to empty list: %v %+v", err, plugins)
	}
}
