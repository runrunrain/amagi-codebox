package wslsupply

// toml_edit_test.go — TOML 保真编辑器钉子（P3-S4 验收 3：非目标节与注释保留
// ——含注释的 config.toml 改 mcp_servers 后其余内容字节不变）+ 值解析/序列化
// round-trip + 不支持形态拒绝。

import (
	"strings"
	"testing"
)

const tomlFidelitySample = `# Codex 配置（用户自有注释必须保留）
model = "gpt-5" # 行尾注释
model_provider = "openai"

# 信任决定：绝不跨界（非受管节字节不动）
[projects."myproj"]
trust_level = "trusted"

[profiles.fast]
model = "gpt-5-mini"
effort = "low"

[mcp_servers.fetch]
command = "npx"
args = [
  "-y",  # 行内注释
  "fetch-mcp",
]

[mcp_servers.fetch.env]
FETCH_TOKEN = "secret"

[mcp_servers.win-only]
command = "setup.bat"
`

func TestTOMLEditPreservesUnmanagedBytes(t *testing.T) {
	doc, _, err := parseTOMLManaged([]byte(tomlFidelitySample), codexTOMLScopes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 零编辑 round-trip：字节完全一致。
	if got := string(doc.Bytes()); got != tomlFidelitySample {
		t.Fatalf("no-edit round-trip must be byte-identical")
	}

	// 删除 mcp_servers.fetch（含其尾随空行）：其余内容整文确定性期望（F4）。
	const afterRemove = `# Codex 配置（用户自有注释必须保留）
model = "gpt-5" # 行尾注释
model_provider = "openai"

# 信任决定：绝不跨界（非受管节字节不动）
[projects."myproj"]
trust_level = "trusted"

[profiles.fast]
model = "gpt-5-mini"
effort = "low"

[mcp_servers.win-only]
command = "setup.bat"
`
	doc2, _, err := parseTOMLManaged([]byte(tomlFidelitySample), codexTOMLScopes)
	if err != nil {
		t.Fatal(err)
	}
	doc2.RemoveSection("mcp_servers.fetch")
	if got := string(doc2.Bytes()); got != afterRemove {
		t.Fatalf("remove must preserve all unmanaged bytes verbatim; got:\n%s\nwant:\n%s", got, afterRemove)
	}

	// 新增段：整文确定性期望（原样内容 + 文末空行分隔追加）。
	const afterAppend = `# Codex 配置（用户自有注释必须保留）
model = "gpt-5" # 行尾注释
model_provider = "openai"

# 信任决定：绝不跨界（非受管节字节不动）
[projects."myproj"]
trust_level = "trusted"

[profiles.fast]
model = "gpt-5-mini"
effort = "low"

[mcp_servers.fetch]
command = "npx"
args = [
  "-y",  # 行内注释
  "fetch-mcp",
]

[mcp_servers.fetch.env]
FETCH_TOKEN = "secret"

[mcp_servers.win-only]
command = "setup.bat"

[mcp_servers.new]
command = "npx"
args = ["-y", "pkg"]
`
	doc3, _, err := parseTOMLManaged([]byte(tomlFidelitySample), codexTOMLScopes)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc3.UpsertSection("mcp_servers.new", map[string]any{
		"command": "npx",
		"args":    []any{"-y", "pkg"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := string(doc3.Bytes()); got != afterAppend {
		t.Fatalf("append must preserve all existing bytes and append deterministically; got:\n%s\nwant:\n%s", got, afterAppend)
	}
}

func TestTOMLEditReplaceSectionInPlace(t *testing.T) {
	doc, _, err := parseTOMLManaged([]byte(tomlFidelitySample), codexTOMLScopes)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.UpsertSection("mcp_servers.fetch", map[string]any{
		"command": "npx",
		"args":    []any{"-y", "other-pkg"},
		"env":     map[string]any{"K": "v"},
	}); err != nil {
		t.Fatal(err)
	}
	// 原位替换 + 其余行区间删除：整文确定性期望（F4；替换后受管段以序列化
	// 形态出现，其余字节逐字保留）。
	const afterReplace = `# Codex 配置（用户自有注释必须保留）
model = "gpt-5" # 行尾注释
model_provider = "openai"

# 信任决定：绝不跨界（非受管节字节不动）
[projects."myproj"]
trust_level = "trusted"

[profiles.fast]
model = "gpt-5-mini"
effort = "low"

[mcp_servers.fetch]
command = "npx"
args = ["-y", "other-pkg"]

[mcp_servers.fetch.env]
K = "v"
[mcp_servers.win-only]
command = "setup.bat"
`
	if got := string(doc.Bytes()); got != afterReplace {
		t.Fatalf("replace must preserve unmanaged bytes verbatim; got:\n%s\nwant:\n%s", got, afterReplace)
	}
}

func TestTOMLSectionValueParsing(t *testing.T) {
	doc, _, err := parseTOMLManaged([]byte(tomlFidelitySample), codexTOMLScopes)
	if err != nil {
		t.Fatal(err)
	}
	fetch, ok := doc.SectionValue("mcp_servers.fetch")
	if !ok {
		t.Fatal("mcp_servers.fetch missing")
	}
	if fetch["command"] != "npx" {
		t.Fatalf("command = %v", fetch["command"])
	}
	args, ok := fetch["args"].([]any)
	if !ok || len(args) != 2 || args[0] != "-y" || args[1] != "fetch-mcp" {
		t.Fatalf("multi-line array parse: %#v", fetch["args"])
	}
	env, ok := fetch["env"].(map[string]any)
	if !ok || env["FETCH_TOKEN"] != "secret" {
		t.Fatalf("env sub-table parse: %#v", fetch["env"])
	}
	profile, ok := doc.SectionValue("profiles.fast")
	if !ok || profile["model"] != "gpt-5-mini" || profile["effort"] != "low" {
		t.Fatalf("profile parse: %#v", profile)
	}
	if _, ok := doc.SectionValue("mcp_servers.absent"); ok {
		t.Fatal("absent section must not resolve")
	}
}

func TestTOMLEditValueRoundTripThroughSectionValue(t *testing.T) {
	doc, _, err := parseTOMLManaged([]byte("# head\n\n[mcp_servers.x]\ncommand = \"npx\"\ncount = 42\nratio = 1.5\nok = true\n"), codexTOMLScopes)
	if err != nil {
		t.Fatal(err)
	}
	val, ok := doc.SectionValue("mcp_servers.x")
	if !ok {
		t.Fatal("section missing")
	}
	if val["count"] != int64(42) || val["ratio"] != 1.5 || val["ok"] != true {
		t.Fatalf("scalar parse: %#v", val)
	}
	// 序列化/再解析 round-trip 值相等（verifyTOMLCandidate 的核心承诺）。
	if err := doc.UpsertSection("mcp_servers.x", val); err != nil {
		t.Fatal(err)
	}
	got, ok := doc.SectionValue("mcp_servers.x")
	if !ok || keyedValueHash(got) != keyedValueHash(val) {
		t.Fatalf("round-trip drift: %#v vs %#v", got, val)
	}
}

func TestTOMLEditRootDottedInlineTable(t *testing.T) {
	// 根级点键内联表（mcp_servers.x = { … }）：段本体取内联表值（F3）。
	src := "# c\nmcp_servers.dotted = { command = \"npx\", args = [\"-y\"] }\n\n[other]\nx = 1\n"
	doc, _, err := parseTOMLManaged([]byte(src), codexTOMLScopes)
	if err != nil {
		t.Fatalf("root dotted inline table must parse: %v", err)
	}
	val, ok := doc.SectionValue("mcp_servers.dotted")
	if !ok {
		t.Fatal("dotted section missing")
	}
	if val["command"] != "npx" {
		t.Fatalf("inline table body must surface as section value, got %#v", val)
	}
	if args, ok := val["args"].([]any); !ok || len(args) != 1 || args[0] != "-y" {
		t.Fatalf("inline table args: %#v", val["args"])
	}
	// 非表值赋给受管段：明确「不支持形态」拒绝（不产生空段误导诊断）。
	if _, _, err := parseTOMLManaged([]byte("mcp_servers.x = \"scalar\"\n"), codexTOMLScopes); err == nil {
		t.Fatal("non-table root assignment to managed section must be rejected")
	} else if !strings.Contains(err.Error(), "unsupported form") {
		t.Fatalf("rejection must cite unsupported form, got: %v", err)
	}
}

func TestTOMLEditDottedRootKeys(t *testing.T) {
	src := "# c\nmcp_servers.dotted.command = \"npx\"\nmcp_servers.dotted.args = [\"a\"]\n\n[other]\nx = 1\n"
	doc, _, err := parseTOMLManaged([]byte(src), codexTOMLScopes)
	if err != nil {
		t.Fatalf("dotted root keys must parse: %v", err)
	}
	val, ok := doc.SectionValue("mcp_servers.dotted")
	if !ok || val["command"] != "npx" {
		t.Fatalf("dotted section value: %#v", val)
	}
	doc.RemoveSection("mcp_servers.dotted")
	got := string(doc.Bytes())
	if strings.Contains(got, "dotted") {
		t.Fatalf("dotted lines must be removed:\n%s", got)
	}
	if !strings.Contains(got, "[other]\nx = 1") {
		t.Fatalf("other section preserved:\n%s", got)
	}
}

func TestTOMLEditCRLF(t *testing.T) {
	src := "# c\r\n[a]\r\nx = 1\r\n\r\n[mcp_servers.s]\r\ncommand = \"npx\"\r\n"
	doc, _, err := parseTOMLManaged([]byte(src), codexTOMLScopes)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.UpsertSection("mcp_servers.s", map[string]any{"command": "npm"}); err != nil {
		t.Fatal(err)
	}
	got := string(doc.Bytes())
	if !strings.Contains(got, "\r\n") || !strings.Contains(got, "command = \"npm\"\r\n") {
		t.Fatalf("crlf style must be preserved for new lines:\n%q", got)
	}
	if !strings.Contains(got, "[a]\r\nx = 1") {
		t.Fatalf("unmanaged crlf content preserved:\n%q", got)
	}
}

func TestTOMLEditUnsupportedForms(t *testing.T) {
	cases := map[string]string{
		"array of tables":      "[[mcp_servers.list]]\ncommand = \"x\"\n",
		"bare managed table":   "[mcp_servers]\nfoo = { command = \"x\" }\n",
		"top-level assignment": "mcp_servers = { a = { command = \"x\" } }\n",
		"unparseable value":    "[mcp_servers.a]\ncommand = @bad\n",
		"datetime value":       "[profiles.p]\nwhen = 1979-05-27T07:32:00Z\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseTOMLManaged([]byte(src), codexTOMLScopes); err == nil {
				t.Fatalf("unsupported form must fail: %s", name)
			}
		})
	}
	// 非受管节的「怪」内容不解析、不报错（字节保留）。
	okSrc := "[weird]\nvalue = @strange\n[mcp_servers.a]\ncommand = \"npx\"\n"
	if _, _, err := parseTOMLManaged([]byte(okSrc), codexTOMLScopes); err != nil {
		t.Fatalf("unmanaged section must not be parsed: %v", err)
	}
}
