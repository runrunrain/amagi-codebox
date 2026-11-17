package client

// help_test.go — 帮助（总览/组/单命令）、拼写建议、补全（本地命令，不连服务）。

import (
	"bytes"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

func TestHelpOverview(t *testing.T) {
	t.Setenv("AMAGI_CODEBOX_HOME", t.TempDir()) // 本地命令不依赖端点，但保险起见隔离 home
	for _, argv := range [][]string{{}, {"--help"}, {"-h"}, {"help"}} {
		code, out, _ := runTest(t, argv, "")
		if code != 0 {
			t.Fatalf("argv=%v code=%d", argv, code)
		}
		for _, want := range []string{"用法", "全局旗标", "session", "status", "退出码"} {
			if !strings.Contains(out, want) {
				t.Fatalf("argv=%v 总览缺 %q:\n%s", argv, want, out)
			}
		}
	}
}

func TestHelpSingleCommand(t *testing.T) {
	for _, argv := range [][]string{
		{"help", "session", "launch"},
		{"session", "launch", "--help"},
		{"session", "launch", "-h"},
	} {
		code, out, _ := runTest(t, argv, "")
		if code != 0 {
			t.Fatalf("argv=%v code=%d", argv, code)
		}
		for _, want := range []string{"用法", "appType", "LAUNCH", "示例", "claudecode|pi", "风险级 exec"} {
			if !strings.Contains(out, want) {
				t.Fatalf("argv=%v 单命令帮助缺 %q:\n%s", argv, want, out)
			}
		}
	}
}

func TestHelpGroupAndBareGroup(t *testing.T) {
	// help <组> 与裸组名都给组帮助，退出码 0。
	for _, argv := range [][]string{{"help", "session"}, {"session"}} {
		code, out, _ := runTest(t, argv, "")
		if code != 0 {
			t.Fatalf("argv=%v code=%d out=%q", argv, code, out)
		}
		for _, want := range []string{"session list", "session launch", "命令组"} {
			if !strings.Contains(out, want) {
				t.Fatalf("argv=%v 组帮助缺 %q:\n%s", argv, want, out)
			}
		}
	}
	// 子命令错拼 → 2 + 建议。
	code, _, errStr := runTest(t, []string{"session", "bogus"}, "")
	if code != 2 || !strings.Contains(errStr, "未知命令") {
		t.Fatalf("session bogus: code=%d err=%q", code, errStr)
	}
}

func TestUnknownCommandSuggestion(t *testing.T) {
	code, _, errStr := runTest(t, []string{"statuz"}, "")
	if code != 2 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(errStr, "未知命令") || !strings.Contains(errStr, "status") {
		t.Fatalf("建议缺失: %q", errStr)
	}
	// 命令组建议：sessoin → session。
	code, _, errStr = runTest(t, []string{"sessoin", "list"}, "")
	if code != 2 || !strings.Contains(errStr, "session") {
		t.Fatalf("组建议缺失: %q", errStr)
	}
}

func TestOverviewColumnWidthRegression(t *testing.T) {
	// 回归：命令名超出固定列宽时不得与说明粘连（如 plugin uninstall / session
	// clear-stopped / provider key-set）：列宽按全部命令名的最大 CJK 显示宽度
	// 计算，命令名与说明至少 2 个空格间隔，且说明列起始位置对齐。
	table := []spec.Command{
		{Path: []string{"plugin", "uninstall"}, Summary: "卸载插件", Risk: spec.RiskDestructive, Confirm: "UNINSTALL"},
		{Path: []string{"session", "clear-stopped"}, Summary: "清理已停止会话"},
		{Path: []string{"provider", "key-set"}, Summary: "写入密钥"},
		{Path: []string{"status"}, Summary: "应用总览"},
	}
	var buf bytes.Buffer
	renderOverview(&buf, table, "vtest")
	out := buf.String()
	for _, name := range []string{"plugin uninstall", "session clear-stopped", "provider key-set", "status"} {
		if !strings.Contains(out, name+"  ") {
			t.Fatalf("命令 %q 与说明粘连（至少需 2 空格间隔）:\n%s", name, out)
		}
	}
	// 说明列对齐：每个命令条目行的说明（命令名后首个非空格字符）起始列一致。
	gotCols := map[int]bool{}
	for _, ln := range strings.Split(out, "\n") {
		if !strings.HasPrefix(ln, "  ") {
			continue
		}
		var name string
		for _, n := range []string{"plugin uninstall", "session clear-stopped", "provider key-set", "status"} {
			if strings.Contains(ln, n) {
				name = n
				break
			}
		}
		if name == "" {
			continue // 组头行（如 "  provider"）等非条目行
		}
		pos := strings.Index(ln, name) + len(name)
		for pos < len(ln) && ln[pos] == ' ' {
			pos++
		}
		if pos < len(ln) {
			gotCols[pos] = true
		}
	}
	if len(gotCols) != 1 {
		t.Fatalf("说明列未对齐（列位置集合 %v）:\n%s", gotCols, out)
	}
}

func TestHelpUnknownTarget(t *testing.T) {
	code, _, errStr := runTest(t, []string{"help", "bogus"}, "")
	if code != 2 || !strings.Contains(errStr, "未知的帮助目标") {
		t.Fatalf("code=%d err=%q", code, errStr)
	}
}

func TestCompletionLocal(t *testing.T) {
	// 本地命令：无端点也能生成。
	t.Setenv("AMAGI_CODEBOX_HOME", t.TempDir())
	cases := []struct {
		shell    string
		contains []string
	}{
		{"bash", []string{"complete -F _codebox_agent codebox-agent", "session", "launch"}},
		{"zsh", []string{"#compdef codebox-agent", "session", "_describe"}},
		{"fish", []string{"complete -c codebox-agent", "session", "__fish_seen_subcommand_from"}},
	}
	for _, c := range cases {
		code, out, _ := runTest(t, []string{"completion", c.shell}, "")
		if code != 0 {
			t.Fatalf("completion %s: code=%d", c.shell, code)
		}
		for _, want := range c.contains {
			if !strings.Contains(out, want) {
				t.Fatalf("completion %s 缺 %q:\n%s", c.shell, want, out)
			}
		}
	}
	// 不支持的 shell / 缺参 → 2。
	if code, _, _ := runTest(t, []string{"completion", "powershell"}, ""); code != 2 {
		t.Fatalf("completion powershell: code=%d", code)
	}
	if code, _, _ := runTest(t, []string{"completion"}, ""); code != 2 {
		t.Fatalf("completion 缺参: code=%d", code)
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"statuz", "status", 1},
		{"中文", "中国", 1},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Fatalf("levenshtein(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}
