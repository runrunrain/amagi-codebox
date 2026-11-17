package sharedext

// toml_mcp_test.go — 覆盖清单 10：TOML set 新段/更新已有段/删除段、
// 混合注释空行 CRLF 保真逐字节断言、env 嵌套表、不支持形态返回错误且
// 原文件不动。

import (
	"errors"
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

// ---------------------------------------------------------------------------
// P5-Fix C2：多行字符串状态跟踪（diting D2/D3 场景不复现）
// ---------------------------------------------------------------------------

// C2-D2：多行字符串内嵌 [mcp_servers.x] 行是字符串内容，不是段边界——
// Remove 幂等原样返回（不再删字符串内部行留下未闭合 """）。
func TestTOMLMultilineStringPhantomIgnored(t *testing.T) {
	input := "[profile]\ntext = \"\"\"\n[mcp_servers.x]\ncommand = \"evil\"\n\"\"\"\nkey = 1\n"
	out, err := RemoveMCPServerSection([]byte(input), "x")
	if err != nil {
		t.Fatalf("字符串内部幻影段应被忽略（幂等删除）: %v", err)
	}
	if string(out) != input {
		t.Fatalf("输出应逐字节原样:\n--- got ---\n%s", string(out))
	}

	// Set 到同文件：追加新段，多行字符串逐字节保留、不产生重复段。
	out2, err := SetMCPServerSection([]byte(input), "real", map[string]any{"command": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out2)
	if !strings.HasPrefix(got, input) {
		t.Fatalf("原内容应前缀保留:\n%s", got)
	}
	if strings.Count(got, "[mcp_servers.real]") != 1 || strings.Contains(got, "[mcp_servers.x]\ncommand = \"ok\"") {
		t.Fatalf("应只追加一个新段（幻影段不受影响）:\n%s", got)
	}
	if strings.Count(got, `"""`) != 2 {
		t.Fatalf("多行字符串开闭定界符应完整保留:\n%s", got)
	}
}

// C2-D3：受管段自身的值是多行字符串（内部含 [xxx] 形态行）→ 段范围完整
// 覆盖字符串行，Set 整段替换后无孤立定界符、无幻影段。
func TestTOMLMultilineStringInManagedSection(t *testing.T) {
	input := "[mcp_servers.x]\ncommand = \"npx\"\nnote = \"\"\"\n[whatever]\n\"\"\"\nother = 1\n"
	out, err := SetMCPServerSection([]byte(input), "x", map[string]any{"command": "npx", "args": []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	want := "[mcp_servers.x]\ncommand = \"npx\"\nargs = [\"a\"]\n"
	if got != want {
		t.Fatalf("整段应被完整替换（含多行字符串行）:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if strings.Contains(got, `"""`) || strings.Contains(got, "[whatever]") || strings.Contains(got, "other = 1") {
		t.Fatalf("不应残留孤立定界符/幻影段/幻影段内容:\n%s", got)
	}
}

// C2：字面多行字符串（'''）同样跟踪；同行闭合（"""abc"""）正常。
func TestTOMLMultilineLiteralAndSameLineClose(t *testing.T) {
	input := "note = '''\n[mcp_servers.ghost]\ncommand = \"x\"\n'''\n\n[mcp_servers.real]\ncommand = \"y\"\n"
	out, err := SetMCPServerSection([]byte(input), "real", map[string]any{"command": "z"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "command = \"z\"") || strings.Contains(got, "command = \"y\"") {
		t.Fatalf("real 段应被替换:\n%s", got)
	}
	if strings.Count(got, "[mcp_servers.ghost]") != 1 || strings.Count(got, "'''") != 2 {
		t.Fatalf("字面多行字符串内容应逐字节保留:\n%s", got)
	}

	// 同行闭合的基本多行串：不进入跨行状态，后续段边界正常识别。
	input2 := "text = \"\"\"abc [not-a-header] def\"\"\"\n\n[mcp_servers.svc]\ncommand = \"old\"\n"
	out2, err := SetMCPServerSection([]byte(input2), "svc", map[string]any{"command": "new"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out2), "command = \"new\"") || !strings.Contains(string(out2), "[not-a-header] def") {
		t.Fatalf("同行闭合多行串应整体保留:\n%s", string(out2))
	}
}

// C2：未闭合的多行字符串（文件本身非法 TOML）→ 拒绝编辑且原文件不动。
func TestTOMLUnterminatedMultilineRejected(t *testing.T) {
	input := "[mcp_servers.x]\ncommand = \"npx\"\nnote = \"\"\"\nnever closed\n"
	for label, fn := range map[string]func() ([]byte, error){
		"Set":    func() ([]byte, error) { return SetMCPServerSection([]byte(input), "x", map[string]any{"command": "y"}) },
		"Remove": func() ([]byte, error) { return RemoveMCPServerSection([]byte(input), "x") },
	} {
		t.Run(label, func(t *testing.T) {
			out, err := fn()
			if err == nil || !errors.Is(err, errTomlUnsupported) {
				t.Fatalf("未闭合多行字符串应拒绝编辑: %v", err)
			}
			if out != nil {
				t.Fatalf("拒绝编辑不应产出新内容: %q", string(out))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// P5-Fix C3：非 bare key 段名引号包裹 + 读回互通（diting D1 场景不复现）
// ---------------------------------------------------------------------------

// C3：中文名/带空格名/=/@ 名 Set → 引号段头（合法 TOML）→ 读回段名精确
// 匹配 → 二次 Set 无重复段 → Remove 干净移除。
func TestTOMLQuotedSectionNameRoundtrip(t *testing.T) {
	names := []string{"搜索服务", "my server", "a=b", "a@b"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			def := map[string]any{
				"command": "npx",
				"env":     map[string]any{"KEY": "V"},
			}
			out, err := SetMCPServerSection(nil, name, def)
			if err != nil {
				t.Fatalf("非 bare key 名应引号包裹写入而非报错: %v", err)
			}
			got := string(out)
			// 段头与 env 子表段头均为引号形态（TOML v1.0 合法）。
			wantHeader := "[mcp_servers.\"" + name + "\"]"
			if !strings.Contains(got, wantHeader) {
				t.Fatalf("应写出引号段头 %q:\n%s", wantHeader, got)
			}
			if !strings.Contains(got, "[mcp_servers.\""+name+"\".env]") {
				t.Fatalf("env 子表段头应为引号路径形态:\n%s", got)
			}

			// 读回：段名精确匹配（不再吞空格/丢引号）。
			doc, err := parseTOMLDoc(out)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := doc.sectionRanges["mcp_servers."+name]; !ok {
				t.Fatalf("读回应命中段 mcp_servers.%s: %+v", name, doc.sectionRanges)
			}
			val, ok := tomlSectionValue(out, name)
			if !ok || val["command"] != "npx" {
				t.Fatalf("冲突检测读回应命中: %+v", val)
			}

			// 二次 Set（更新）：替换而非追加重复段。
			out2, err := SetMCPServerSection(out, name, map[string]any{"command": "changed"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(out2), "[mcp_servers.\""+name+"\"]") != 1 {
				t.Fatalf("二次写入不应产生重复段:\n%s", string(out2))
			}
			if !strings.Contains(string(out2), "command = \"changed\"") {
				t.Fatalf("更新应生效:\n%s", string(out2))
			}

			// Remove：干净移除。
			out3, err := RemoveMCPServerSection(out2, name)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out3), "mcp_servers") || strings.Contains(string(out3), "changed") {
				t.Fatalf("删除后不应残留:\n%s", string(out3))
			}
		})
	}

	// bare key 名保持原样（不引号）。
	out, err := SetMCPServerSection(nil, "plain-name_1", map[string]any{"command": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "[mcp_servers.plain-name_1]\n") {
		t.Fatalf("bare key 名不应引号包裹:\n%s", string(out))
	}

	// 引号内需要转义的 env 键与非法段名仍拒绝。
	if _, err := SetMCPServerSection(nil, "bad\"quote", map[string]any{"command": "x"}); err == nil {
		t.Fatal("含双引号段名仍应拒绝")
	}
	outEnv, err := SetMCPServerSection(nil, "svc", map[string]any{
		"env": map[string]any{"我的键": "V"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(outEnv), "\"我的键\" = \"V\"") {
		t.Fatalf("非 bare key env 键应引号包裹:\n%s", string(outEnv))
	}
}
