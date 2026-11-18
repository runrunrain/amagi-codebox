package sharedmcp

// import_test.go — JSON 导入纵切测试（纯解析预览/批量登记）：临时目录
// fixture，绝不读写真实用户配置。覆盖：三格式兼容解析、预览不回传配置值、
// 同名跳过逐条报告、未知配置值零丢弃原样写入、默认无分发、sse 归一、
// 非法条目显式报告、大小上限、批内重复。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// previewByName 从预览中取名条目。
func previewByName(t *testing.T, pv ImportPreview, name string) ImportPreviewEntry {
	t.Helper()
	for _, row := range pv.Entries {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("预览缺条目 %s: %+v", name, pv.Entries)
	return ImportPreviewEntry{}
}

func TestParseImportText_FormatCompatibility(t *testing.T) {
	cases := []struct {
		name string
		text string
		want map[string]string // name → 归一 type
	}{
		{
			name: "mcpServers 包裹（Claude/.mcp.json 格式）",
			text: `{"mcpServers": {"alpha": {"command": "npx", "args": ["-y", "a"]}, "beta": {"type": "http", "url": "https://b.example.com/mcp"}}}`,
			want: map[string]string{"alpha": "stdio", "beta": "http"},
		},
		{
			name: "opencode 的 mcp 包裹键宽容兼容",
			text: `{"mcp": {"gamma": {"command": "uvx", "args": ["g"]}}}`,
			want: map[string]string{"gamma": "stdio"},
		},
		{
			name: "裸对象（def 含 command 或 url）",
			text: `{"delta": {"url": "https://d.example.com/sse"}, "eps": {"command": "npx"}}`,
			want: map[string]string{"delta": "http", "eps": "stdio"},
		},
		{
			name: "数组混合（元素递归按前两种格式解释）",
			text: `[{"mcpServers": {"zeta": {"command": "npx"}}}, {"eta": {"type": "http", "url": "https://e.example.com/m"}}]`,
			want: map[string]string{"zeta": "stdio", "eta": "http"},
		},
		{
			name: "mcpServers 内 def 自带 type",
			text: `{"mcpServers": {"theta": {"type": "stdio", "command": "npx", "env": {"K": "V"}}}}`,
			want: map[string]string{"theta": "stdio"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidates, err := ParseImportText(tc.text)
			if err != nil {
				t.Fatalf("ParseImportText: %v", err)
			}
			got := map[string]string{}
			for _, c := range candidates {
				if c.InvalidReason != "" {
					t.Fatalf("条目 %s 意外 invalid: %s", c.Name, c.InvalidReason)
				}
				got[c.Name] = c.Type
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("形态归一不符: got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestParseImportText_RejectsWholeInput(t *testing.T) {
	for _, text := range []string{"", "   ", "not json", `{"foo": "bar"}`, `["plain string"]`} {
		if _, err := ParseImportText(text); err == nil {
			t.Fatalf("应整体拒绝 %q", text)
		}
	}
	// 超限。
	big := strings.Repeat("a", MaxImportBytes+1)
	if _, err := ParseImportText(big); err == nil || !strings.Contains(err.Error(), "大小上限") {
		t.Fatalf("超限未拒绝: %v", err)
	}
}

func TestImportRejectsDuplicateKeysAndPreservesNumericValues(t *testing.T) {
	for _, payload := range []string{
		`{"mcpServers":{"x":{"command":"first"},"\u0078":{"command":"second"}}}`,
		`{"x":{"command":"first","command":"second"}}`,
		`{"x":{"command":"stdio","env":{"KEY":"a","KEY":"b"}}}`,
	} {
		if _, err := ParseImportText(payload); err == nil || !strings.Contains(err.Error(), "重复键") {
			t.Fatalf("must reject duplicate key before parsing: %v", err)
		}
	}
	entries, err := ParseImportText(`{"x":{"command":"stdio","big":9007199254740993}}`)
	if err != nil || len(entries) != 1 || gjson.GetBytes(entries[0].Raw, "big").Raw != "9007199254740993" {
		t.Fatalf("numeric extension field must not be rounded: %v", err)
	}
}

func TestImportPreviewDoesNotEchoInvalidURL(t *testing.T) {
	entries, err := ParseImportText(`{"x":{"url":"https://[broken/user:fixture-secret"}}`)
	if err != nil || len(entries) != 1 || entries[0].InvalidReason == "" || strings.Contains(entries[0].InvalidReason, "fixture-secret") {
		t.Fatalf("invalid URL must be rejected without echo: %v", err)
	}
}

func TestParseImportText_SSENormalizedToHTTP(t *testing.T) {
	candidates, err := ParseImportText(`{"mcpServers": {"legacy-sse": {"type": "sse", "url": "https://s.example.com/sse"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Type != "http" {
		t.Fatalf("sse 应归一 http: %+v", candidates)
	}
	// 归一写入 raw：type=sse → http，其余键原样。
	if gjson.GetBytes(candidates[0].Raw, "type").String() != "http" {
		t.Fatalf("raw type 未归一: %s", candidates[0].Raw)
	}
	if !gjson.GetBytes(candidates[0].Raw, "url").Exists() {
		t.Fatalf("raw 丢失 url: %s", candidates[0].Raw)
	}
}

func TestParseImportText_InvalidEntriesReported(t *testing.T) {
	text := `{"mcpServers": {
		"bad.name": {"command": "npx"},
		"weird-type": {"type": "grpc", "command": "npx"},
		"no-shape": {"description": "只有描述"},
		"not-object": "oops",
		"bad-url": {"type": "http", "url": "not-a-url"},
		"good": {"command": "npx"}
	}}`
	candidates, err := ParseImportText(text)
	if err != nil {
		t.Fatal(err)
	}
	invalid := map[string]string{}
	for _, c := range candidates {
		if c.InvalidReason != "" {
			invalid[c.Name] = c.InvalidReason
		}
	}
	for _, name := range []string{"bad.name", "weird-type", "no-shape", "not-object", "bad-url"} {
		if _, ok := invalid[name]; !ok {
			t.Fatalf("条目 %s 应显式 invalid 报告，got: %+v", name, candidates)
		}
	}
	if _, ok := invalid["good"]; ok {
		t.Fatalf("good 不应 invalid: %s", invalid["good"])
	}
	if !strings.Contains(invalid["bad.name"], "不兼容") {
		t.Fatalf("bad.name 原因应引用名称字符集: %s", invalid["bad.name"])
	}
}

func TestBuildImportPreview_NoConfigValuesInPreview(t *testing.T) {
	s, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home) // 登记已有 web-search-prime / zhipu-vision（含密钥值）

	text := `{"mcpServers": {
		"web-search-prime": {"type": "http", "url": "https://mcp.example.com/other", "headers": {"Authorization": "Bearer NEW-SECRET-99"}},
		"fresh-one": {"command": "npx", "env": {"FRESH_KEY": "FRESH-SECRET-77"}}
	}}`
	pv, err := s.BuildImportPreview(text)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(pv)
	preview := string(b)
	for _, secret := range []string{"NEW-SECRET-99", "FRESH-SECRET-77", "SECRET-HEADER", "SECRET-ENV"} {
		if strings.Contains(preview, secret) {
			t.Fatalf("预览泄漏配置值 %s: %s", secret, preview)
		}
	}
	if strings.Contains(preview, "mcp.example.com/other") {
		t.Fatalf("预览泄漏 URL 配置值: %s", preview)
	}
	if row := previewByName(t, pv, "web-search-prime"); row.Status != ImportStatusExists {
		t.Fatalf("登记同名应标 exists: %+v", row)
	}
	if row := previewByName(t, pv, "fresh-one"); row.Status != ImportStatusOk || row.Type != "stdio" {
		t.Fatalf("fresh-one 应 ok/stdio: %+v", row)
	}
}

func TestBuildImportPreview_BatchDuplicateMarked(t *testing.T) {
	s, _, _ := newRegistryTestService(t)
	text := `[{"mcpServers": {"dup": {"command": "npx"}}}, {"dup": {"command": "uvx"}}]`
	pv, err := s.BuildImportPreview(text)
	if err != nil {
		t.Fatal(err)
	}
	dupRows := 0
	for _, row := range pv.Entries {
		if row.Name == "dup" {
			dupRows++
			if row.Status == ImportStatusExists && !strings.Contains(row.Note, "本批") {
				t.Fatalf("批内重复附注不符: %+v", row)
			}
		}
	}
	if dupRows != 2 {
		t.Fatalf("应保留两行 dup 预览: %+v", pv.Entries)
	}
}

func TestImport_SameNameSkippedWithReport(t *testing.T) {
	s, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	before := readRegistry(t, home)

	text := `{"mcpServers": {
		"web-search-prime": {"type": "http", "url": "https://overwrite.example.com/m"},
		"brand-new": {"command": "npx", "args": ["-y", "brand"]}
	}}`
	outcome, err := s.Import(text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outcome.Imported, []string{"brand-new"}) {
		t.Fatalf("Imported 不符: %+v", outcome)
	}
	if len(outcome.Skipped) != 1 || outcome.Skipped[0].Name != "web-search-prime" || !strings.Contains(outcome.Skipped[0].Reason, "同名") {
		t.Fatalf("同名跳过报告不符: %+v", outcome.Skipped)
	}
	// 既有同名条目一字不动（不覆盖）。
	after := readRegistry(t, home)
	if !reflect.DeepEqual(gjson.GetBytes(after, "mcpServers.web-search-prime").Raw,
		gjson.GetBytes(before, "mcpServers.web-search-prime").Raw) {
		t.Fatalf("既有同名条目被改动:\nbefore=%s\nafter=%s",
			gjson.GetBytes(before, "mcpServers.web-search-prime").Raw,
			gjson.GetBytes(after, "mcpServers.web-search-prime").Raw)
	}
}

func TestImport_PreservesUnknownConfigKeys(t *testing.T) {
	s, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home) // 夹具自带未知顶层键 $schema 与 zhipu-vision 的 x-custom

	text := `{"mcpServers": {"rich": {
		"type": "stdio",
		"command": "npx",
		"args": ["-y", "rich"],
		"env": {"K": "V"},
		"envFile": ".env",
		"x-vendor": {"nested": [1, 2, {"deep": true}]},
		"cwd": "/tmp"
	}}}`
	if _, err := s.Import(text); err != nil {
		t.Fatal(err)
	}
	after := string(readRegistry(t, home))
	for _, key := range []string{"envFile", "x-vendor", "cwd", ".env", "nested"} {
		if !strings.Contains(after, key) {
			t.Fatalf("导入条目的未知配置键 %s 被丢弃:\n%s", key, after)
		}
	}
	// 深层结构值语义保留。
	if !gjson.Get(after, "mcpServers.rich.x-vendor.nested.2.deep").Bool() {
		t.Fatalf("未知键深层值丢失: %s", gjson.Get(after, "mcpServers.rich.x-vendor").Raw)
	}
	// 夹具的既有未知顶层键与既有条目原样保留（保真投影语义）。
	if !gjson.Get(after, "$schema").Exists() || !gjson.Get(after, "mcpServers.zhipu-vision.x-custom").Exists() {
		t.Fatalf("登记文件既有内容被扰动:\n%s", after)
	}
	// 已知字段进入 DTO 视图（List 可见，供既有编辑/分发链路消费）。
	entries, _, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "rich" {
			found = true
			if e.Type != "stdio" || e.Command != "npx" || !reflect.DeepEqual(e.Args, []string{"-y", "rich"}) {
				t.Fatalf("导入条目 DTO 视图不符: %+v", e)
			}
			hasUnknown := false
			for _, k := range e.UnknownKeys {
				if k == "envFile" || k == "x-vendor" || k == "cwd" {
					hasUnknown = true
				}
			}
			if !hasUnknown {
				t.Fatalf("未知键应进入 UnknownKeys 信息性字段: %+v", e.UnknownKeys)
			}
		}
	}
	if !found {
		t.Fatal("List 缺导入条目 rich")
	}
}

func TestImport_NoDistributionByDefault(t *testing.T) {
	s, home, cfgDir := newRegistryTestService(t)
	text := `{"mcpServers": {"quiet": {"command": "npx"}}}`
	if _, err := s.Import(text); err != nil {
		t.Fatal(err)
	}
	ov, err := s.Overview()
	if err != nil {
		t.Fatal(err)
	}
	for _, eo := range ov.Entries {
		if eo.Entry.Name != "quiet" {
			continue
		}
		for id, cell := range eo.Engines {
			if id == engineIDOMP {
				continue
			}
			if cell.Status != EngineNotDistributed || cell.Selected {
				t.Fatalf("导入默认无分发: %s=%+v", id, cell)
			}
		}
	}
	// 四引擎目标文件均未被创建；状态库无条目。
	for _, p := range []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".config", "opencode", "opencode.json"),
		filepath.Join(home, ".pi", "agent", "mcp.json"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("导入不应写引擎目标文件: %s（err=%v）", p, err)
		}
	}
	if st := s.loadState(); len(st.Entries) != 0 {
		t.Fatalf("导入不应写状态库: %+v", st.Entries)
	}
	_ = cfgDir
}

func TestImport_InvalidEntriesGoToSkippedReport(t *testing.T) {
	s, home, _ := newRegistryTestService(t)
	text := `{"mcpServers": {
		"ok1": {"command": "npx"},
		"bad.name": {"command": "npx"},
		"no-shape": {"description": "x"}
	}}`
	outcome, err := s.Import(text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outcome.Imported, []string{"ok1"}) {
		t.Fatalf("Imported 不符: %+v", outcome)
	}
	reasons := map[string]string{}
	for _, sk := range outcome.Skipped {
		reasons[sk.Name] = sk.Reason
	}
	if _, ok := reasons["bad.name"]; !ok {
		t.Fatalf("bad.name 应进 skipped: %+v", outcome.Skipped)
	}
	if _, ok := reasons["no-shape"]; !ok {
		t.Fatalf("no-shape 应进 skipped: %+v", outcome.Skipped)
	}
	// 登记文件只落 ok1。
	names := gjson.GetBytes(readRegistry(t, home), "mcpServers").Map()
	if len(names) != 1 {
		t.Fatalf("登记文件条目数不符: %+v", names)
	}
	if _, ok := names["ok1"]; !ok {
		t.Fatalf("登记文件缺 ok1: %+v", names)
	}
}

func TestImport_BatchDuplicateOnlyFirstImported(t *testing.T) {
	s, home, _ := newRegistryTestService(t)
	text := `[{"mcpServers": {"dup": {"command": "npx", "args": ["first"]}}}, {"mcpServers": {"dup": {"command": "uvx", "args": ["second"]}}}]`
	outcome, err := s.Import(text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outcome.Imported, []string{"dup"}) {
		t.Fatalf("批内重复应仅导入首个: %+v", outcome)
	}
	if len(outcome.Skipped) != 1 || !strings.Contains(outcome.Skipped[0].Reason, "本批") {
		t.Fatalf("批内重复应报告: %+v", outcome.Skipped)
	}
	if got := gjson.GetBytes(readRegistry(t, home), "mcpServers.dup.args.0").String(); got != "first" {
		t.Fatalf("应保留首个定义: %s", got)
	}
}

func TestImport_CreatesRegistryWhenMissing(t *testing.T) {
	s, home, _ := newRegistryTestService(t) // 不预置登记文件
	if _, err := s.Import(`{"mcpServers": {"first": {"command": "npx"}}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", ".mcp.json")); err != nil {
		t.Fatalf("登记文件未创建: %v", err)
	}
}

func TestImport_RegistryCorruptRefuses(t *testing.T) {
	s, home, _ := newRegistryTestService(t)
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agents", ".mcp.json"), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(`{"mcpServers": {"x": {"command": "npx"}}}`); err == nil || KindOf(err) != KindRegistryCorrupt {
		t.Fatalf("登记损坏应整体拒绝: %v", err)
	}
}
