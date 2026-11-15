package sharedmcp

// toml_test.go — 移植版 TOML 引擎的行为快照测试（spec §2.2「TOML 引擎
// 复用裁定」）。wslsupply 侧经既有导出面无法在本包测试中直接对照，故按
// 契约以固定用例固化「与 wslsupply 现行为一致」的快照（同一输入 → 同一
// 输出）；后续漂移由 wslsupply 自身测试兜底。

import (
	"strings"
	"testing"
)

func TestTomlEngine_UpsertAppendAndReplaceSnapshot(t *testing.T) {
	src := "# head\r\nmodel = \"gpt-5\"\r\n\r\n[mcp_servers.old]\r\ncommand = \"a\"\r\n\r\n[mcp_servers.old.env]\r\nK = \"V\"\r\n"
	doc, _, err := parseTOMLManaged([]byte(src), []string{"mcp_servers"})
	if err != nil {
		t.Fatal(err)
	}
	// 追加新段。
	if err := doc.UpsertSection("mcp_servers.new", map[string]any{
		"command": "npx",
		"args":    []any{"-y", "x"},
		"env":     map[string]any{"K2": "V2"},
	}); err != nil {
		t.Fatal(err)
	}
	got := string(doc.Bytes())
	want := "# head\r\nmodel = \"gpt-5\"\r\n\r\n[mcp_servers.old]\r\ncommand = \"a\"\r\n\r\n[mcp_servers.old.env]\r\nK = \"V\"\r\n\r\n[mcp_servers.new]\r\ncommand = \"npx\"\r\nargs = [\"-y\", \"x\"]\r\n\r\n[mcp_servers.new.env]\r\nK2 = \"V2\"\r\n"
	if got != want {
		t.Fatalf("追加快照不符:\ngot:  %q\nwant: %q", got, want)
	}
	// 原位替换既有段（含子表段整体替换）。
	if err := doc.UpsertSection("mcp_servers.old", map[string]any{"command": "b"}); err != nil {
		t.Fatal(err)
	}
	got = string(doc.Bytes())
	if !strings.Contains(got, "[mcp_servers.old]\r\ncommand = \"b\"\r\n") {
		t.Fatalf("替换快照不符: %q", got)
	}
	if strings.Contains(got, "[mcp_servers.old.env]") {
		t.Fatalf("旧子表段应被整体替换: %q", got)
	}
}

func TestTomlEngine_SectionValueAndRemove(t *testing.T) {
	src := "[mcp_servers.x]\r\ncommand = \"c\"\r\nargs = [\"1\", \"2\"]\r\n\r\n[mcp_servers.x.env]\r\nA = \"1\"\r\nB = \"2\"\r\n"
	doc, _, err := parseTOMLManaged([]byte(src), []string{"mcp_servers"})
	if err != nil {
		t.Fatal(err)
	}
	val, ok := doc.SectionValue("mcp_servers.x")
	if !ok || val["command"] != "c" {
		t.Fatalf("SectionValue 不符: %#v", val)
	}
	env, _ := val["env"].(map[string]any)
	if env == nil || env["A"] != "1" || env["B"] != "2" || len(env) != 2 {
		t.Fatalf("env 子表不符: %#v", env)
	}
	args, _ := val["args"].([]any)
	if len(args) != 2 || args[1] != "2" {
		t.Fatalf("args 不符: %#v", args)
	}
	doc.RemoveSection("mcp_servers.x")
	if doc.Has("mcp_servers.x") {
		t.Fatal("RemoveSection 后段仍在")
	}
	if string(doc.Bytes()) != "" {
		t.Fatalf("全删后应剩空文档: %q", string(doc.Bytes()))
	}
}

func TestTomlEngine_UnsupportedFormsRefused(t *testing.T) {
	for _, src := range []string{
		"[[mcp_servers.arr]]\r\ncommand = \"x\"\r\n",           // 数组表
		"[mcp_servers]\r\nbare = 1\r\n",                        // 裸表
		"mcp_servers.root = \"x\"\r\n",                         // 顶层作用域标量赋值（F3）
		"[mcp_servers.ok]\r\nvalue = 1979-05-27T07:32:00Z\r\n", // 日期时间值
	} {
		if _, _, err := parseTOMLManaged([]byte(src), []string{"mcp_servers"}); err == nil {
			t.Fatalf("不支持形态应报错: %q", src)
		}
	}
	// 根级点分段键（mcp_servers.a.b = 1）是引擎接受的嵌套子表形态（归属
	// mcp_servers.a 段），非不支持形态——名字级隔离由 validName 保证不与
	// 我方段冲突。
	if _, _, err := parseTOMLManaged([]byte("mcp_servers.a.b = 1\r\n"), []string{"mcp_servers"}); err != nil {
		t.Fatalf("根级点分段键不应报错: %v", err)
	}
	// 引号段起点回退回归（移植修正）：`mcp_servers."a.b" = 1` 在 wslsupply
	// 原版 splitTOMLKeyPath 的 `i = next` 索引回退下死循环；本包修正后
	// splitTOMLKeyPath 对该键报「trailing dot」→ parseTOMLKV 按非受管根键
	// 跳过 → 解析成功且该行逐字节保留（非受管内容）。
	quoted := "mcp_servers.\"a.b\" = 1\r\n[mcp_servers.ok]\r\ncommand = \"c\"\r\n"
	doc, _, err := parseTOMLManaged([]byte(quoted), []string{"mcp_servers"})
	if err != nil {
		t.Fatalf("引号段名键不应使解析失败: %v", err)
	}
	if got := string(doc.Bytes()); got != quoted {
		t.Fatalf("引号段名行应逐字节保留:\ngot:  %q\nwant: %q", got, quoted)
	}
	// 非受管区畸形行：不解析、保留字节。
	benign := "[other]\r\nwhat = @@@\r\n[mcp_servers.ok]\r\ncommand = \"c\"\r\n"
	if _, _, err := parseTOMLManaged([]byte(benign), []string{"mcp_servers"}); err != nil {
		t.Fatalf("非受管区不应报错: %v", err)
	}
}

func TestTomlEngine_HandwrittenSectionBytePreserved(t *testing.T) {
	src := "# c1\r\n[mcp_servers.hand]\r\nenv_vars = [\"PATH\"]\r\nstartup_timeout_sec = 30\r\n\r\n[mcp_servers.hand.env]\r\nK = \"V\"\r\n# tail\r\n"
	doc, _, err := parseTOMLManaged([]byte(src), []string{"mcp_servers"})
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.UpsertSection("mcp_servers.mine", map[string]any{"command": "x"}); err != nil {
		t.Fatal(err)
	}
	out := string(doc.Bytes())
	hand := "[mcp_servers.hand]\r\nenv_vars = [\"PATH\"]\r\nstartup_timeout_sec = 30\r\n\r\n[mcp_servers.hand.env]\r\nK = \"V\"\r\n"
	if !strings.Contains(out, hand) {
		t.Fatalf("手写段逐字节保留失败:\n%q", out)
	}
	doc2, _, err := parseTOMLManaged([]byte(out), []string{"mcp_servers"})
	if err != nil {
		t.Fatal(err)
	}
	doc2.RemoveSection("mcp_servers.mine")
	// 追加段的前置换行分隔符不属段区间，Remove 后残留一个空行——这是移植
	// 引擎（与 wslsupply 一致）的既定语义：手写内容本体逐字节保留。
	if got := string(doc2.Bytes()); got != src+"\r\n" {
		t.Fatalf("删除我方段后应还原原文（+追加分隔空行）:\ngot:  %q\nwant: %q", got, src+"\r\n")
	}
}
