package main

// agentcli_host_test.go — 命令表 ↔ Handler 1:1 平价测试（DESIGN.md §2：
// "命令表条目 ↔ Handler 键（Command.Name()）必须 1:1，root 测试断言"）。

import (
	"sort"
	"testing"

	"amagi-codebox/internal/agentcli/cmds"
)

// TestAgentCLITableHandlersParity 断言 cmds.Table() 的命令名集合与
// agentCLIHost.handlers() 的键集合严格相等（双向无缺漏、无重复）。
//
// 注意：D1/D2（system/config/usage/git/session/plugin 六个领域）填充完成前，
// 任意一侧先落地时本测试会失败——这是预期行为，由 Leader 在合并后验证；
// 两侧均为空桩时平凡通过。
func TestAgentCLITableHandlersParity(t *testing.T) {
	app := newTestApp(t)
	handlers := newAgentCLIHost(app).handlers()

	table := cmds.Table()
	tableNames := make(map[string]bool, len(table))
	for _, c := range table {
		name := c.Name()
		if tableNames[name] {
			t.Errorf("命令表存在重复命令 %q", name)
			continue
		}
		tableNames[name] = true
	}

	handlerNames := make(map[string]bool, len(handlers))
	for name := range handlers {
		handlerNames[name] = true
	}

	var missingHandlers, missingCommands []string
	for name := range tableNames {
		if !handlerNames[name] {
			missingHandlers = append(missingHandlers, name)
		}
	}
	for name := range handlerNames {
		if !tableNames[name] {
			missingCommands = append(missingCommands, name)
		}
	}
	sort.Strings(missingHandlers)
	sort.Strings(missingCommands)
	for _, name := range missingHandlers {
		t.Errorf("命令 %q 已登记命令表但没有 Handler（agentcli_h_*.go 缺失）", name)
	}
	for _, name := range missingCommands {
		t.Errorf("Handler 键 %q 没有对应命令表条目（cmds/*.go 缺失）", name)
	}

	if len(tableNames) == 0 && len(handlerNames) == 0 {
		t.Log("命令表与 Handler 当前均为空桩（D1/D2 未填充），1:1 平凡成立")
	}
}
