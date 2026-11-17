package sharedext

// toml_mcp_test.go — 覆盖清单 10：TOML set 新段/更新已有段/删除段、
// 混合注释空行 CRLF 保真逐字节断言、env 嵌套表、不支持形态返回错误且
// 原文件不动。

import (
	"strings"
	"testing"
)

// 覆盖 10a：Set 到空数据 → 输出仅含该段（env 嵌套表）。
func TestTOMLSetOnEmpty(t *testing.T) {
	out, err := SetMCPServerSection(nil, "alpha", map[string]any{
		"command": "npx",
		"args":    []any{"-y", "pkg"},
		"env":     map[string]any{"API_KEY": "k1", "DEBUG": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	want := "[mcp_servers.alpha]\ncommand = \"npx\"\nargs = [\"-y\", \"pkg\"]\n\n[mcp_servers.alpha.env]\nAPI_KEY = \"k1\"\nDEBUG = \"true\"\n"
	if got != want {
		t.Fatalf("输出不符:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// 覆盖 10b：更新已有段 + 非目标节逐字节保真（混合注释/空行）。
func TestTOMLUpdatePreservesOthers(t *testing.T) {
	input := `# 顶部注释
model = "gpt-5"
# mcp 段前注释

[mcp_servers.existing]
command = "old-cmd"
args = ["a"]
url = "https://keep"

[profiles.default]  # 行尾注释
model = "gpt-5-mini"

# 尾部注释
`
	out, err := SetMCPServerSection([]byte(input), "existing", map[string]any{
		"command": "new-cmd",
		"env":     map[string]any{"K": "V"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	// 非目标节逐字节保留。
	for _, keep := range []string{
		"# 顶部注释\nmodel = \"gpt-5\"\n",
		"[profiles.default]  # 行尾注释\nmodel = \"gpt-5-mini\"\n",
		"# 尾部注释\n",
	} {
		if !strings.Contains(got, keep) {
			t.Fatalf("非目标内容应逐字节保留，缺失: %q\n实际:\n%s", keep, got)
		}
	}
	// 目标段被替换。
	if strings.Contains(got, "old-cmd") || strings.Contains(got, `"a"`) {
		t.Fatalf("旧段内容应被替换:\n%s", got)
	}
	if !strings.Contains(got, "command = \"new-cmd\"") || !strings.Contains(got, "[mcp_servers.existing.env]") {
		t.Fatalf("新段应写入:\n%s", got)
	}
}

// 覆盖 10c：删除段保真。
func TestTOMLRemovePreservesOthers(t *testing.T) {
	input := "# 头注释\nmodel = \"gpt-5\"\n\n[mcp_servers.gone]\ncommand = \"x\"\n\n[mcp_servers.keep]\ncommand = \"y\"\n\n[other]\nkey = 1\n"
	out, err := RemoveMCPServerSection([]byte(input), "gone")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	want := "# 头注释\nmodel = \"gpt-5\"\n\n[mcp_servers.keep]\ncommand = \"y\"\n\n[other]\nkey = 1\n"
	if got != want {
		t.Fatalf("删除结果不符:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// 删除不存在的段：幂等原样返回。
	same, err := RemoveMCPServerSection([]byte(input), "absent")
	if err != nil || string(same) != input {
		t.Fatalf("幂等删除应原样返回: %v", err)
	}
}

// 覆盖 10d：CRLF 行尾保真。
func TestTOMLCRLFPreservation(t *testing.T) {
	input := "model = \"gpt-5\"\r\n\r\n[mcp_servers.svc]\r\ncommand = \"x\"\r\n\r\n[other]\r\nk = 1\r\n"
	out, err := SetMCPServerSection([]byte(input), "svc", map[string]any{"command": "y"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "command = \"y\"\r\n") {
		t.Fatalf("新增行应沿用 CRLF:\n%q", got)
	}
	if !strings.Contains(got, "[other]\r\nk = 1\r\n") {
		t.Fatalf("其他节 CRLF 保真:\n%q", got)
	}
	if strings.Contains(got, "command = \"x\"") {
		t.Fatalf("旧值应替换:\n%q", got)
	}
}

// 覆盖 10e：追加新段（已有其他段时文末追加，前置空行）。
func TestTOMLAppendNewSection(t *testing.T) {
	input := "model = \"gpt-5\"\n\n[profiles.default]\nmodel = \"mini\"\n"
	out, err := SetMCPServerSection([]byte(input), "fresh", map[string]any{"command": "z"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.HasPrefix(got, input) {
		t.Fatalf("原内容应前缀保留:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n\n[mcp_servers.fresh]\ncommand = \"z\"\n") {
		t.Fatalf("应文末追加新段:\n%q", got)
	}
}

// 覆盖 10f：不支持形态返回错误且不修改原文件。
func TestTOMLUnsupportedFormsRejected(t *testing.T) {
	cases := map[string]string{
		"数组表头":   "[[mcp_servers.bad]]\ncommand = \"x\"\n",
		"裸表":     "[mcp_servers]\nbad = 1\n",
		"点引号段名":   "[mcp_servers.\"a.b\"]\ncommand = \"x\"\n",
		"根级点键":   "mcp_servers.dot = { command = \"x\" }\n",
		"根级整键赋值": "mcp_servers = { dot = { command = \"x\" } }\n",
	}
	for label, input := range cases {
		t.Run(label, func(t *testing.T) {
			if _, err := SetMCPServerSection([]byte(input), "any", map[string]any{"command": "n"}); err == nil {
				t.Fatal("不支持形态应返回错误")
			}
			if _, err := RemoveMCPServerSection([]byte(input), "any"); err == nil {
				t.Fatal("Remove 同样应拒绝")
			}
		})
	}
}

// 覆盖 10g：同名段多次出现 → 替换首个、删除其余。
func TestTOMLDuplicateSectionsConsolidated(t *testing.T) {
	input := "[mcp_servers.dup]\ncommand = \"first\"\n\n[other]\nk = 1\n\n[mcp_servers.dup]\ncommand = \"second\"\n"
	out, err := SetMCPServerSection([]byte(input), "dup", map[string]any{"command": "merged"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "[mcp_servers.dup]") != 1 {
		t.Fatalf("重复段应合并为 1:\n%s", got)
	}
	if !strings.Contains(got, "command = \"merged\"") || strings.Contains(got, "second") {
		t.Fatalf("应替换为新值:\n%s", got)
	}
	if !strings.Contains(got, "[other]\nk = 1") {
		t.Fatalf("非目标节保留:\n%s", got)
	}
}

// 覆盖 10h：tomlSectionValue 读取当前值（冲突检测基础）。
func TestTOMLSectionValueReadback(t *testing.T) {
	input := "[mcp_servers.rd]\ncommand = \"c\"\nargs = [\"x\", \"y\"]\n\n[mcp_servers.rd.env]\nA = \"1\"\n"
	got, ok := tomlSectionValue([]byte(input), "rd")
	if !ok {
		t.Fatal("段应存在")
	}
	if got["command"] != "c" {
		t.Fatalf("command 读取不符: %+v", got)
	}
	args, _ := got["args"].([]any)
	if len(args) != 2 || args[0] != "x" {
		t.Fatalf("args 读取不符: %+v", got["args"])
	}
	env, _ := got["env"].(map[string]any)
	if env["A"] != "1" {
		t.Fatalf("env 嵌套读取不符: %+v", got["env"])
	}
	if _, ok := tomlSectionValue([]byte(input), "absent"); ok {
		t.Fatal("缺失段应返回 false")
	}
}

// 覆盖 10i：值类型序列化（bool/int/float/字符串转义/数组）。
func TestTOMLValueSerialization(t *testing.T) {
	out, err := SetMCPServerSection(nil, "types", map[string]any{
		"flag":   true,
		"count":  3,
		"ratio":  1.5,
		"quoted": "含\"引号\"与\\反斜杠",
		"list":   []any{"a", "b"},
		"strs":   []string{"x", "y"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"flag = true",
		"count = 3",
		"ratio = 1.5",
		`quoted = "含\"引号\"与\\反斜杠"`,
		`list = ["a", "b"]`,
		`strs = ["x", "y"]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q:\n%s", want, got)
		}
	}

	// 不支持的值类型（深层嵌套 map）→ 拒绝。
	if _, err := SetMCPServerSection(nil, "deep", map[string]any{
		"env": map[string]any{"inner": map[string]any{"x": "y"}},
	}); err == nil {
		t.Fatal("两层嵌套 map 应拒绝")
	}
	// 非法段名 → 拒绝。
	if _, err := SetMCPServerSection(nil, "bad.name", map[string]any{"command": "x"}); err == nil {
		t.Fatal("含点段名应拒绝")
	}
}
