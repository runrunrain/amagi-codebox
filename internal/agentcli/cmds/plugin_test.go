package cmds

// plugin_test.go — 插件域命令表自检：名称集合、五引擎枚举、Risk/Confirm 配对、
// 表聚合一致性。

import (
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

func TestPluginCommands_NameSet(t *testing.T) {
	want := map[string]bool{
		"plugin.list":      false,
		"plugin.enable":    false,
		"plugin.disable":   false,
		"plugin.install":   false,
		"plugin.uninstall": false,
	}
	for _, c := range pluginCommands() {
		if _, ok := want[c.Name()]; !ok {
			t.Fatalf("pluginCommands 出现未预期命令: %s", c.Name())
		}
		want[c.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("pluginCommands 缺少命令: %s", name)
		}
	}
}

func TestPluginCommands_Invariants(t *testing.T) {
	for _, c := range pluginCommands() {
		checkCommandInvariants(t, c)
	}
}

func TestPluginCommands_InTable(t *testing.T) {
	table := Table()
	for _, c := range pluginCommands() {
		if _, ok := Find(table, c.Name()); !ok {
			t.Fatalf("命令 %s 未进入 Table()", c.Name())
		}
	}
}

// TestPluginCommands_AppEnum 校验所有带 app 参数的命令都覆盖五引擎枚举。
func TestPluginCommands_AppEnum(t *testing.T) {
	for _, c := range pluginCommands() {
		app, ok := paramOf(c, "app")
		if !ok {
			t.Errorf("%s: 缺少 app 参数", c.Name())
			continue
		}
		if !containsAll(app.Enum, "claudecode", "codex", "opencode", "pi", "omp") {
			t.Errorf("%s: app 枚举缺少五引擎: %v", c.Name(), app.Enum)
		}
	}
}

// TestPluginCommands_ConfirmValues 校验确认常量与设计一致。
func TestPluginCommands_ConfirmValues(t *testing.T) {
	expect := map[string]string{
		"plugin.list":      "",
		"plugin.enable":    "",
		"plugin.disable":   "",
		"plugin.install":   "INSTALL",
		"plugin.uninstall": "UNINSTALL",
	}
	for name, confirm := range expect {
		c, ok := Find(pluginCommands(), name)
		if !ok {
			t.Fatalf("缺少 %s", name)
		}
		if c.Confirm != confirm {
			t.Errorf("%s Confirm = %q, want %q", name, c.Confirm, confirm)
		}
	}
	_ = spec.RiskExec // 保持与 spec 的引用一致性（枚举来自 spec）
}
