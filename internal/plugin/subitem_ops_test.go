package plugin

// subitem_ops_test.go — 子项启停「真实生效」机制回归（2026-11 修复）：
// 此前开关只写私有记录（Claude 不读），必然不生效且状态与文件系统脱钩。
// 现模型：文件系统即真相——installPath 在位 = 启用，stash 在位 = 禁用。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newSubItemTestService 构造注入 claudeDir/configDir 的服务 + CLI 恒败替身
//（GetInstalledPlugins 走 installed_plugins.json 文件回退，测试无 CLI 依赖）。
func newSubItemTestService(t *testing.T) (*Service, string) {
	t.Helper()
	claudeDir := t.TempDir()
	configDir := t.TempDir()
	svc := NewServiceWithConfigDir(claudeDir, configDir, nil)
	svc.execCLI = func(args ...string) (*CommandResult, error) {
		return nil, os.ErrProcessDone // 任意非 nil 错误 → 文件回退
	}
	return svc, claudeDir
}

// fixtureSubItemPlugin 预置一个已安装插件：2 skills + 1 command + 1 agent +
// hooks.json + .mcp.json（2 servers），并登记 installed_plugins.json。
func fixtureSubItemPlugin(t *testing.T, claudeDir string) string {
	t.Helper()
	installPath := filepath.Join(claudeDir, "plugins", "cache", "mk", "demo", "1.0.0")
	write := func(rel, content string) {
		p := filepath.Join(installPath, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir fixture: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", p, err)
		}
	}
	write(".claude-plugin/plugin.json", `{"name":"demo","version":"1.0.0"}`)
	write("skills/skill-a/SKILL.md", "---\nname: skill-a\ndescription: A\n---\n# a\n")
	write("skills/skill-b/SKILL.md", "---\nname: skill-b\ndescription: B\n---\n# b\n")
	write("commands/do-thing.md", "---\ndescription: do\n---\n# do\n")
	// F-01 场景：agent 显示名（frontmatter name: Helper）与文件名（helper.md）不同
	write("agents/helper.md", "---\nname: Helper\ndescription: agent\n---\n# h\n")
	write("hooks/hooks.json", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`)
	write(".mcp.json", `{"mcpServers":{"server-one":{"command":"npx","args":["-y","one"]},"server-two":{"type":"http","url":"https://example.com/mcp"}}}`)

	pluginsDir := filepath.Join(claudeDir, "plugins")
	reg := map[string]interface{}{
		"version": 2,
		"plugins": map[string]interface{}{
			"demo@mk": []map[string]interface{}{{
				"scope":       "user",
				"installPath": installPath,
				"version":     "1.0.0",
				"installedAt": "2026-11-17T00:00:00Z",
			}},
		},
	}
	b, _ := json.Marshal(reg)
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), b, 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	// settings.json（enabled 回读用）
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"enabledPlugins":{"demo@mk":true}}`), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return installPath
}

func findSubItem(t *testing.T, detail *PluginDetail, typ SubItemType, name string) *SubItem {
	t.Helper()
	for i := range detail.SubItems {
		if detail.SubItems[i].Type == typ && detail.SubItems[i].Name == name {
			return &detail.SubItems[i]
		}
	}
	t.Fatalf("subitem %s:%s not found in detail", typ, name)
	return nil
}

func TestSubItemDisableSkillMovesFileToStash(t *testing.T) {
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)

	if err := svc.SetPluginSubItemEnabled("demo@mk", "skill", "skill-a", false); err != nil {
		t.Fatalf("disable skill-a: %v", err)
	}

	// 真实生效：installPath 下文件消失（Claude 扫描不到）
	if _, err := os.Lstat(filepath.Join(installPath, "skills", "skill-a")); !os.IsNotExist(err) {
		t.Fatalf("skill-a should be moved out of installPath, stat err=%v", err)
	}
	stashFile := filepath.Join(svc.stashRoot(), "demo@mk", "skill", "skill-a")
	if _, err := os.Lstat(stashFile); err != nil {
		t.Fatalf("skill-a should exist in stash: %v", err)
	}

	// 真实状态体现：详情仍列出该项（禁用态），其余子项不受影响
	detail, err := svc.GetPluginDetail("demo@mk")
	if err != nil {
		t.Fatalf("GetPluginDetail: %v", err)
	}
	if item := findSubItem(t, detail, SubItemTypeSkill, "skill-a"); item.Enabled || !item.Selectable {
		t.Fatalf("stashed skill must show Enabled=false and stay operable: %+v", item)
	}
	if item := findSubItem(t, detail, SubItemTypeSkill, "skill-b"); !item.Enabled {
		t.Fatalf("untouched skill must stay enabled: %+v", item)
	}

	// 记录文件同步（对账）
	state, err := svc.readSubItemStateFile()
	if err != nil || len(state) != 1 || len(state[0].DisabledSubItems) != 1 {
		t.Fatalf("record file should track the disable: %v %+v", err, state)
	}
}

func TestSubItemEnableRestoresFileFromStash(t *testing.T) {
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)

	if err := svc.SetPluginSubItemEnabled("demo@mk", "skill", "skill-a", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := svc.SetPluginSubItemEnabled("demo@mk", "skill", "skill-a", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	target := filepath.Join(installPath, "skills", "skill-a", "SKILL.md")
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("skill-a should be restored to installPath: %v", err)
	}
	detail, err := svc.GetPluginDetail("demo@mk")
	if err != nil {
		t.Fatalf("GetPluginDetail: %v", err)
	}
	if item := findSubItem(t, detail, SubItemTypeSkill, "skill-a"); !item.Enabled {
		t.Fatalf("restored skill must show enabled: %+v", item)
	}
	// 记录清零（无禁用项时不留空条目）
	state, err := svc.readSubItemStateFile()
	if err != nil || len(state) != 0 {
		t.Fatalf("record file should be empty after re-enable: %v %+v", err, state)
	}
}

func TestSubItemDisableCommandAndAgent(t *testing.T) {
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)

	if err := svc.SetPluginSubItemEnabled("demo@mk", "command", "do-thing", false); err != nil {
		t.Fatalf("disable command: %v", err)
	}
	// F-01：按显示名 Helper 禁用，实际搬走的是物理文件 helper.md
	if err := svc.SetPluginSubItemEnabled("demo@mk", "agent", "Helper", false); err != nil {
		t.Fatalf("disable agent: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(installPath, "commands", "do-thing.md")); !os.IsNotExist(err) {
		t.Fatalf("command file should be stashed, stat err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(installPath, "agents", "helper.md")); !os.IsNotExist(err) {
		t.Fatalf("agent file (helper.md) should be stashed, stat err=%v", err)
	}
	// 启用恢复：显示名操作 → 物理文件回位（.md 形态）
	if err := svc.SetPluginSubItemEnabled("demo@mk", "command", "do-thing", true); err != nil {
		t.Fatalf("enable command: %v", err)
	}
	if err := svc.SetPluginSubItemEnabled("demo@mk", "agent", "Helper", true); err != nil {
		t.Fatalf("enable agent: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(installPath, "commands", "do-thing.md")); err != nil {
		t.Fatalf("command file should be restored: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(installPath, "agents", "helper.md")); err != nil {
		t.Fatalf("agent file should be restored to physical path: %v", err)
	}
}

func TestSubItemDisplayNameMapsToPhysicalPath(t *testing.T) {
	// F-01 回归：展示名（frontmatter name）与物理名不同时，禁用/启用都必须
	// 定位到真实物理文件——禁用后物理文件消失、详情仍以展示名列出禁用项。
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)

	if err := svc.SetPluginSubItemEnabled("demo@mk", "agent", "Helper", false); err != nil {
		t.Fatalf("disable by display name: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(installPath, "agents", "helper.md")); !os.IsNotExist(err) {
		t.Fatalf("physical file helper.md must be moved out, stat err=%v", err)
	}
	stashEntry := filepath.Join(svc.stashRoot(), "demo@mk", "agent", "helper.md")
	if _, err := os.Lstat(stashEntry); err != nil {
		t.Fatalf("stash entry should keep physical name: %v", err)
	}

	detail, err := svc.GetPluginDetail("demo@mk")
	if err != nil {
		t.Fatalf("GetPluginDetail: %v", err)
	}
	if item := findSubItem(t, detail, SubItemTypeAgent, "Helper"); item.Enabled {
		t.Fatalf("stashed agent must show disabled under display name: %+v", item)
	}

	if err := svc.SetPluginSubItemEnabled("demo@mk", "agent", "Helper", true); err != nil {
		t.Fatalf("enable by display name: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(installPath, "agents", "helper.md")); err != nil {
		t.Fatalf("physical file must be restored: %v", err)
	}
}

func TestSubItemMCPServerEntryDisableAndEnable(t *testing.T) {
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)
	mcpPath := filepath.Join(installPath, ".mcp.json")

	if err := svc.SetPluginSubItemEnabled("demo@mk", "mcp", "server-two", false); err != nil {
		t.Fatalf("disable mcp server: %v", err)
	}

	// 真实生效：.mcp.json 不再含 server-two，其余 server 原样保留
	b, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	servers := doc["mcpServers"].(map[string]interface{})
	if _, exists := servers["server-two"]; exists {
		t.Fatal("server-two must be removed from .mcp.json")
	}
	if _, exists := servers["server-one"]; !exists {
		t.Fatal("server-one must remain in .mcp.json")
	}

	// 状态体现：详情里 server-two 显示禁用、server-one 启用
	detail, err := svc.GetPluginDetail("demo@mk")
	if err != nil {
		t.Fatalf("GetPluginDetail: %v", err)
	}
	if item := findSubItem(t, detail, SubItemTypeMCP, "server-two"); item.Enabled {
		t.Fatalf("disabled mcp server must show Enabled=false: %+v", item)
	}
	if item := findSubItem(t, detail, SubItemTypeMCP, "server-one"); !item.Enabled {
		t.Fatalf("enabled mcp server must show Enabled=true: %+v", item)
	}

	// 启用恢复条目（配置原样写回）
	if err := svc.SetPluginSubItemEnabled("demo@mk", "mcp", "server-two", true); err != nil {
		t.Fatalf("enable mcp server: %v", err)
	}
	b, _ = os.ReadFile(mcpPath)
	doc = map[string]interface{}{}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("re-parse .mcp.json: %v", err)
	}
	servers = doc["mcpServers"].(map[string]interface{})
	entry, ok := servers["server-two"].(map[string]interface{})
	if !ok || entry["url"] != "https://example.com/mcp" {
		t.Fatalf("server-two entry must be restored verbatim: %+v", servers["server-two"])
	}
	// stash 清空（空 stash 文件删除）
	if _, err := os.Lstat(svc.mcpStashPath("demo@mk")); !os.IsNotExist(err) {
		t.Fatalf("mcp stash should be removed when empty, stat err=%v", err)
	}
}

func TestSubItemHookNotSupported(t *testing.T) {
	svc, claudeDir := newSubItemTestService(t)
	fixtureSubItemPlugin(t, claudeDir)

	detail, err := svc.GetPluginDetail("demo@mk")
	if err != nil {
		t.Fatalf("GetPluginDetail: %v", err)
	}
	if len(detail.Hooks) == 0 {
		t.Fatal("fixture should have hooks")
	}
	for _, item := range detail.SubItems {
		if item.Type == SubItemTypeHook && item.Selectable {
			t.Fatalf("hook subitem must be non-selectable: %+v", item)
		}
	}
	err = svc.SetPluginSubItemEnabled("demo@mk", "hook", detail.Hooks[0].Name, false)
	if err == nil || !strings.Contains(err.Error(), "不支持单独启停") {
		t.Fatalf("hook subitem toggle must fail loudly: %v", err)
	}
}

func TestSubItemUpdateReinstallConflictPrefersNewFile(t *testing.T) {
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)

	if err := svc.SetPluginSubItemEnabled("demo@mk", "skill", "skill-a", false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// 模拟 plugin update 重装恢复：installPath 重新出现同名 skill
	restored := filepath.Join(installPath, "skills", "skill-a")
	if err := os.MkdirAll(restored, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(restored, "SKILL.md"), []byte("---\nname: skill-a\n---\n# new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 状态体现：孤儿 stash 不重复入列，以新文件为准 → 显示启用
	detail, err := svc.GetPluginDetail("demo@mk")
	if err != nil {
		t.Fatalf("GetPluginDetail: %v", err)
	}
	count := 0
	for _, item := range detail.SubItems {
		if item.Type == SubItemTypeSkill && item.Name == "skill-a" {
			count++
			if !item.Enabled {
				t.Fatalf("reinstalled skill must show enabled (new file wins): %+v", item)
			}
		}
	}
	if count != 1 {
		t.Fatalf("skill-a should appear exactly once after reinstall, got %d", count)
	}

	// 启用操作：目标已存在 → 弃置 stash 副本保新文件（幂等成功）
	if err := svc.SetPluginSubItemEnabled("demo@mk", "skill", "skill-a", true); err != nil {
		t.Fatalf("enable after reinstall: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(svc.stashRoot(), "demo@mk", "skill", "skill-a")); !os.IsNotExist(err) {
		t.Fatalf("orphan stash copy should be discarded, stat err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(restored, "SKILL.md")); err != nil {
		t.Fatalf("new file must be preserved: %v", err)
	}
}

func TestSubItemDisableIsIdempotent(t *testing.T) {
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)

	for i := 0; i < 2; i++ {
		if err := svc.SetPluginSubItemEnabled("demo@mk", "skill", "skill-a", false); err != nil {
			t.Fatalf("disable #%d: %v", i, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(installPath, "skills", "skill-a")); !os.IsNotExist(err) {
		t.Fatalf("skill-a must stay stashed, stat err=%v", err)
	}
	// 启用不存在于 stash 的子项（从未禁用）→ 幂等成功
	if err := svc.SetPluginSubItemEnabled("demo@mk", "skill", "skill-b", true); err != nil {
		t.Fatalf("enable never-disabled skill should be idempotent: %v", err)
	}
}

func TestSubItemMCPEnableDoesNotOverwriteNewDefinition(t *testing.T) {
	// F-02 回归：禁用 server → 插件更新产生新版同名定义 → 启用不得用孤儿
	// stash 覆盖新版，只应弃置 stash 旧条目。
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)
	mcpPath := filepath.Join(installPath, ".mcp.json")

	if err := svc.SetPluginSubItemEnabled("demo@mk", "mcp", "server-one", false); err != nil {
		t.Fatalf("disable server-one: %v", err)
	}
	// 模拟更新重装：新版同名定义在位
	newDef := `{"mcpServers":{"server-one":{"command":"new-cmd","args":["v2"]},"server-two":{"url":"https://example.com/mcp"}}}`
	if err := os.WriteFile(mcpPath, []byte(newDef), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := svc.SetPluginSubItemEnabled("demo@mk", "mcp", "server-one", true); err != nil {
		t.Fatalf("enable server-one: %v", err)
	}
	b, _ := os.ReadFile(mcpPath)
	if strings.Contains(string(b), "old") || !strings.Contains(string(b), "new-cmd") {
		t.Fatalf("new definition must survive enable, got: %s", b)
	}
	if _, err := os.Lstat(svc.mcpStashPath("demo@mk")); !os.IsNotExist(err) {
		t.Fatalf("stash should be dropped after conflict-resolved enable, stat err=%v", err)
	}
}

func TestSubItemMCPNullJSONDoesNotPanic(t *testing.T) {
	// F-03 回归：.mcp.json / stash 为合法 JSON null 时不得 panic。
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)
	mcpPath := filepath.Join(installPath, ".mcp.json")

	// 先禁用一个 server 建立 stash，再把 .mcp.json 置为 null
	if err := svc.SetPluginSubItemEnabled("demo@mk", "mcp", "server-one", false); err != nil {
		t.Fatalf("disable server-one: %v", err)
	}
	if err := os.WriteFile(mcpPath, []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPluginSubItemEnabled("demo@mk", "mcp", "server-one", true); err != nil {
		t.Fatalf("enable under null .mcp.json must not panic: %v", err)
	}
	b, _ := os.ReadFile(mcpPath)
	if !strings.Contains(string(b), "server-one") {
		t.Fatalf("entry should be restored into empty doc: %s", b)
	}

	// stash 为 null + 禁用现存 server（此时在位的是 server-one）：同样不得
	// panic、不得静默覆盖损坏文件（null 按空对象策略处理）
	if err := os.WriteFile(svc.mcpStashPath("demo@mk"), []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPluginSubItemEnabled("demo@mk", "mcp", "server-one", false); err != nil {
		t.Fatalf("disable under null stash must not panic: %v", err)
	}
	// 禁用成功：server-one 进入 stash（null 被规范化为空对象后写入）
	names := svc.stashedMCPServerNames("demo@mk")
	if len(names) != 1 || names[0] != "server-one" {
		t.Fatalf("server-one should be stashed: %v", names)
	}
}

func TestSubItemMCPCorruptedStashFailsLoudly(t *testing.T) {
	// F-07 回归：stash 损坏时禁用必须报错，不得静默覆盖丢掉恢复线索。
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)

	if err := svc.SetPluginSubItemEnabled("demo@mk", "mcp", "server-one", false); err != nil {
		t.Fatalf("disable server-one: %v", err)
	}
	if err := os.WriteFile(svc.mcpStashPath("demo@mk"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := svc.SetPluginSubItemEnabled("demo@mk", "mcp", "server-two", false)
	if err == nil || !strings.Contains(err.Error(), "损坏") {
		t.Fatalf("disable under corrupted stash must fail loudly: %v", err)
	}
	// 原文件保留（未被覆盖）
	b, _ := os.ReadFile(svc.mcpStashPath("demo@mk"))
	if string(b) != "{bad" {
		t.Fatalf("corrupted stash must be preserved, got: %s", b)
	}
	// .mcp.json 未被改动
	if _, err := os.Lstat(filepath.Join(installPath, ".mcp.json")); err != nil {
		t.Fatalf(".mcp.json must remain: %v", err)
	}
}

func TestSubItemStatesMatchesDetailAfterReinstall(t *testing.T) {
	// F-05 回归：禁用后重装恢复同名文件 → 详情显示 enabled，状态投影
	// GetPluginSubItemStates 不得再包含该项（两视图一致）。
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)

	if err := svc.SetPluginSubItemEnabled("demo@mk", "mcp", "server-one", false); err != nil {
		t.Fatalf("disable server-one: %v", err)
	}
	// 模拟重装恢复：新版定义在位
	newDef := `{"mcpServers":{"server-one":{"command":"new"},"server-two":{"url":"https://x"}}}`
	if err := os.WriteFile(filepath.Join(installPath, ".mcp.json"), []byte(newDef), 0o644); err != nil {
		t.Fatal(err)
	}

	detail, err := svc.GetPluginDetail("demo@mk")
	if err != nil {
		t.Fatalf("GetPluginDetail: %v", err)
	}
	if item := findSubItem(t, detail, SubItemTypeMCP, "server-one"); !item.Enabled {
		t.Fatalf("reinstalled server must show enabled: %+v", item)
	}
	state, err := svc.GetPluginSubItemStates("demo@mk")
	if err != nil {
		t.Fatalf("GetPluginSubItemStates: %v", err)
	}
	for _, ref := range state.DisabledSubItems {
		if ref.Name == "server-one" {
			t.Fatalf("states must match detail (server-one reinstalled): %+v", state)
		}
	}
}

func TestSubItemSkillDirWithMdSuffixKeepsIdentity(t *testing.T) {
	// F-06 回归：skill 目录名以 .md 结尾（非常规但可扫描）→ stash 身份不与
	// 去后缀后的同名 skill 碰撞，禁用项仍可列出与启用。
	svc, claudeDir := newSubItemTestService(t)
	installPath := fixtureSubItemPlugin(t, claudeDir)
	weird := filepath.Join(installPath, "skills", "weird.md")
	if err := os.MkdirAll(weird, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(weird, "SKILL.md"), []byte("---\nname: weird.md\ndescription: W\n---\n# w\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := svc.SetPluginSubItemEnabled("demo@mk", "skill", "weird.md", false); err != nil {
		t.Fatalf("disable weird.md skill dir: %v", err)
	}
	detail, err := svc.GetPluginDetail("demo@mk")
	if err != nil {
		t.Fatalf("GetPluginDetail: %v", err)
	}
	found := false
	for _, item := range detail.SubItems {
		if item.Type == SubItemTypeSkill && item.Name == "weird.md" {
			found = true
			if item.Enabled {
				t.Fatalf("stashed skill must keep identity weird.md (no trim collision)")
			}
		}
	}
	if !found {
		t.Fatal("stashed weird.md skill must still be listed")
	}
	if err := svc.SetPluginSubItemEnabled("demo@mk", "skill", "weird.md", true); err != nil {
		t.Fatalf("enable weird.md: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(weird, "SKILL.md")); err != nil {
		t.Fatalf("weird.md dir must be restored: %v", err)
	}
}
