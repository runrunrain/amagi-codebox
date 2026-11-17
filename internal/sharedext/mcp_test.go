package sharedext

// mcp_test.go — 覆盖清单 8-9：MCP JSON 三格式解析与无法识别报错、批量导入
// 同名 skipped、写入/移除（claude 大 JSON 保留其他键、opencode mcp 键、
// pi/omp mcp.json）与备份产生及轮转。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 覆盖 8：三种格式解析。
func TestParseMCPJSONFormats(t *testing.T) {
	cases := []struct {
		label string
		text  string
		want  []string
	}{
		{
			label: "mcpServers 包裹",
			text:  `{"mcpServers": {"ctx": {"command": "npx", "args": ["-y", "@ctx/mcp"]}}}`,
			want:  []string{"ctx"},
		},
		{
			label: "裸对象 command",
			text:  `{"fetch": {"command": "uvx", "args": ["mcp-server-fetch"], "env": {"K": "V"}}}`,
			want:  []string{"fetch"},
		},
		{
			label: "裸对象 url",
			text:  `{"remote": {"url": "https://example.com/mcp"}}`,
			want:  []string{"remote"},
		},
		{
			label: "数组单条粘贴",
			text:  `[{"mcpServers": {"a": {"command": "x"}}}, {"b": {"url": "https://b"}}]`,
			want:  []string{"a", "b"},
		},
		{
			label: "mcp 键包裹（opencode 形态宽容）",
			text:  `{"mcp": {"oc": {"command": "bun"}}}`,
			want:  []string{"oc"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			servers, err := ParseMCPJSON(tc.text)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			names := make([]string, 0, len(servers))
			for _, s := range servers {
				names = append(names, s.Name)
			}
			sort.Strings(names)
			if !reflect.DeepEqual(names, tc.want) {
				t.Fatalf("解析名 %v，期望 %v", names, tc.want)
			}
			// Config 应含 def 内容。
			if len(servers[0].Config) == 0 {
				t.Fatalf("Config 不应为空: %+v", servers[0])
			}
		})
	}
}

// 覆盖 8：完全无法识别报错（中文说明支持格式）。
func TestParseMCPJSONUnrecognized(t *testing.T) {
	bad := []string{
		``,
		`not json at all`,
		`{"foo": "bar"}`,
		`{"obj": {"noCommandOrURL": true}}`,
		`[1, 2, 3]`,
	}
	for _, text := range bad {
		if _, err := ParseMCPJSON(text); err == nil {
			t.Fatalf("应报无法识别: %q", text)
		} else if !strings.Contains(err.Error(), "格式") {
			t.Fatalf("错误应说明支持格式: %v", err)
		}
	}
}

// 覆盖 8：批量导入同名 skipped。
func TestImportMCPServersConflictSkipped(t *testing.T) {
	env := newTestService(t)
	def := map[string]any{"command": "npx"}
	imported, skipped, err := env.svc.ImportMCPServers([]SharedMCPServer{
		{Name: "one", Config: def},
		{Name: "two", Config: map[string]any{"url": "https://two"}},
	})
	if err != nil || len(imported) != 2 || len(skipped) != 0 {
		t.Fatalf("首次导入应全部成功: %v %v %v", imported, skipped, err)
	}

	imported2, skipped2, err2 := env.svc.ImportMCPServers([]SharedMCPServer{
		{Name: "one", Config: map[string]any{"command": "changed"}},
		{Name: "three", Config: def},
	})
	if err2 != nil {
		t.Fatalf("批量导入应部分成功: %v", err2)
	}
	if len(imported2) != 1 || imported2[0].Name != "three" {
		t.Fatalf("three 应导入: %+v", imported2)
	}
	if len(skipped2) != 1 || !strings.Contains(skipped2[0], "one") {
		t.Fatalf("one 应 skipped: %v", skipped2)
	}

	// 深拷贝验证：导入后改 def 不影响落库内容。
	def["command"] = "mutated"
	list, err := env.svc.ListSharedMCPServers()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.Name == "three" && s.Config["command"] != "npx" {
			t.Fatalf("Config 应深拷贝隔离外部修改: %+v", s.Config)
		}
	}
}

// 覆盖 9：MCP 写入/移除——claude（大 JSON 保留其他键）、opencode mcp 键、
// pi/omp mcp.json。
func TestMCPServerDeployAndRemoveAllEngines(t *testing.T) {
	env := newTestService(t)

	// claude 目标预置一个 MB 级大 JSON（含其他顶层键）。
	claudeJSON := filepath.Join(env.roots[EngineClaude], ".claude.json")
	bigPadding := strings.Repeat(`"x",`, 20000) // 数组形态的大体量填充
	if err := os.MkdirAll(filepath.Dir(claudeJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeJSON, []byte(`{"numRequests":{"a":1},"big":[`+bigPadding+`0],"mcpServers":{"existing":{"command":"keep"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// opencode 目标预置其他键。
	ocJSON := filepath.Join(env.roots[EngineOpenCode], "opencode.json")
	if err := os.MkdirAll(filepath.Dir(ocJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ocJSON, []byte(`{"theme":"dark","mcp":{"old":{"command":"bun"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	servers, err := ParseMCPJSON(`{"mcpServers": {"shared-one": {"command": "npx", "args": ["-y", "pkg"], "env": {"A": "B"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.svc.ImportMCPServers(servers); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedMCPServerTargets("shared-one", AllEngines()); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedMCPServerEnabled("shared-one", true); err != nil {
		t.Fatal(err)
	}

	// claude：其他键与大数组逐字节保留，已有 server 不动，新键写入。
	claudeData := mustReadFile(t, claudeJSON)
	if !strings.Contains(claudeData, `"numRequests":{"a":1}`) || !strings.Contains(claudeData, bigPadding) {
		t.Fatal("claude.json 其他键/大体量内容应保留")
	}
	if !strings.Contains(claudeData, `"existing":{"command":"keep"}`) {
		t.Fatal("claude.json 已有 server 应保留")
	}
	if !strings.Contains(claudeData, `"shared-one"`) || !strings.Contains(claudeData, `"command":"npx"`) {
		t.Fatal("claude.json 应写入 shared-one")
	}

	// opencode：写入 mcp 键，theme 保留。
	ocData := mustReadFile(t, ocJSON)
	if !strings.Contains(ocData, `"theme":"dark"`) || !strings.Contains(ocData, `"old":{"command":"bun"}`) {
		t.Fatal("opencode.json 其他键应保留")
	}
	if !strings.Contains(ocData, `"shared-one"`) {
		t.Fatal("opencode.json mcp 键应写入 shared-one")
	}

	// pi / omp：mcp.json 建文件。
	for _, engine := range []Engine{EnginePi, EngineOmp} {
		mcpJSON := filepath.Join(env.roots[engine], "mcp.json")
		data := mustReadFile(t, mcpJSON)
		if !strings.Contains(data, `"shared-one"`) || !strings.Contains(data, "mcpServers") {
			t.Fatalf("%s mcp.json 应含 mcpServers.shared-one: %s", engine, data)
		}
	}

	// codex：config.toml 段写入。
	codexTOML := mustReadFile(t, filepath.Join(env.roots[EngineCodex], "config.toml"))
	if !strings.Contains(codexTOML, "[mcp_servers.shared-one]") {
		t.Fatalf("codex config.toml 应写入段: %s", codexTOML)
	}

	// 移除（禁用 → 回收全部）。
	if err := env.svc.SetSharedMCPServerEnabled("shared-one", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mustReadFile(t, claudeJSON), `"shared-one"`) {
		t.Fatal("回收后 claude.json 不应再含 shared-one")
	}
	if !strings.Contains(mustReadFile(t, claudeJSON), `"existing"`) {
		t.Fatal("回收不应影响其他 server")
	}
	if strings.Contains(mustReadFile(t, ocJSON), `"shared-one"`) {
		t.Fatal("回收后 opencode.json 不应再含 shared-one")
	}
	if strings.Contains(mustReadFile(t, filepath.Join(env.roots[EngineCodex], "config.toml")), "shared-one") {
		t.Fatal("回收后 codex config.toml 不应再含 shared-one")
	}

	// 删除：registry 清理。
	if err := env.svc.DeleteSharedMCPServer("shared-one"); err != nil {
		t.Fatal(err)
	}
	list, err := env.svc.ListSharedMCPServers()
	if err != nil || len(list) != 0 {
		t.Fatalf("删除后 registry 应清空: %v %v", list, err)
	}
}

// 覆盖 9：备份文件产生与轮转（≤3 份）。
func TestMCPClaudeBackupRotation(t *testing.T) {
	env := newTestService(t)
	claudeJSON := filepath.Join(env.roots[EngineClaude], ".claude.json")
	if err := os.MkdirAll(filepath.Dir(claudeJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeJSON, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 注入递增时间戳，连续写入 5 次。
	original := nowUnix
	defer func() { nowUnix = original }()
	base := int64(1_700_000_000)
	for i := 0; i < 5; i++ {
		counter := i
		nowUnix = func() int64 { return base + int64(counter) }
		servers, err := ParseMCPJSON(fmt.Sprintf(`{"mcpServers": {"srv": {"command": "n%d"}}}`, i))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, _, err := env.svc.ImportMCPServers(servers); err != nil {
				t.Fatal(err)
			}
			if err := env.svc.SetSharedMCPServerTargets("srv", []Engine{EngineClaude}); err != nil {
				t.Fatal(err)
			}
			if err := env.svc.SetSharedMCPServerEnabled("srv", true); err != nil {
				t.Fatal(err)
			}
			continue
		}
		// 直接走底层写入（绕过 registry，验证备份轮转本身）。
		target, err := env.svc.engineMCPTarget(EngineClaude)
		if err != nil {
			t.Fatal(err)
		}
		if err := env.svc.writeMCPServer(target, "srv", servers[0].Config); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(filepath.Dir(claudeJSON))
	if err != nil {
		t.Fatal(err)
	}
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".claude.json.bak-sharedext-") {
			backups = append(backups, e.Name())
		}
	}
	if len(backups) != backupKeep {
		t.Fatalf("备份应轮转保留 %d 份，实际 %d: %v", backupKeep, len(backups), backups)
	}
	sort.Strings(backups)
	// 保留的应是最新的 3 份（时间戳 base+2..base+4）。
	want := []string{
		".claude.json.bak-sharedext-" + fmt.Sprint(base+2),
		".claude.json.bak-sharedext-" + fmt.Sprint(base+3),
		".claude.json.bak-sharedext-" + fmt.Sprint(base+4),
	}
	if !reflect.DeepEqual(backups, want) {
		t.Fatalf("保留的备份应为最新 %d 份: %v", backupKeep, backups)
	}

	// 备份内容是写入前的文件内容。
	first := mustReadFile(t, filepath.Join(filepath.Dir(claudeJSON), want[0]))
	var parsed map[string]any
	if err := json.Unmarshal([]byte(first), &parsed); err != nil {
		t.Fatalf("备份应是合法 JSON: %v", err)
	}
}

// 覆盖 6 对应 MCP：targets diff 与 disabled 不分发。
func TestMCPTargetsDiffAndDisabled(t *testing.T) {
	env := newTestService(t)
	imported, _, err := env.svc.ImportMCPServers([]SharedMCPServer{
		{Name: "diff-srv", Config: map[string]any{"command": "x"}},
	})
	if err != nil || len(imported) != 1 {
		t.Fatal(err)
	}

	// disabled 时改 targets 不分发。
	if err := env.svc.SetSharedMCPServerTargets("diff-srv", []Engine{EnginePi}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.roots[EnginePi], "mcp.json")); !os.IsNotExist(err) {
		t.Fatal("disabled 状态改 targets 不应写文件")
	}

	// 启用 → 分发 pi；加 omp → diff 分发；减 pi → 回收。
	if err := env.svc.SetSharedMCPServerEnabled("diff-srv", true); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedMCPServerTargets("diff-srv", []Engine{EnginePi, EngineOmp}); err != nil {
		t.Fatal(err)
	}
	piJSON := filepath.Join(env.roots[EnginePi], "mcp.json")
	ompJSON := filepath.Join(env.roots[EngineOmp], "mcp.json")
	if !strings.Contains(mustReadFile(t, piJSON), "diff-srv") || !strings.Contains(mustReadFile(t, ompJSON), "diff-srv") {
		t.Fatal("两引擎都应写入")
	}
	if err := env.svc.SetSharedMCPServerTargets("diff-srv", []Engine{EngineOmp}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(piJSON); err == nil {
		if strings.Contains(mustReadFile(t, piJSON), "diff-srv") {
			t.Fatal("移除的目标引擎不应再含该 server")
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if !strings.Contains(mustReadFile(t, ompJSON), "diff-srv") {
		t.Fatal("omp 保留目标不应回收")
	}
}

// ---------------------------------------------------------------------------
// P5-Fix：C1 吞读错误 / C3 导入名拒绝 / M1 部分失败落盘 / m1 非法 JSON 拒写
// ---------------------------------------------------------------------------

// C3(c)：含双引号/控制字符的名字在解析预览阶段直接丢弃（不可入库）。
func TestParseMCPJSONInvalidNamesDropped(t *testing.T) {
	servers, err := ParseMCPJSON(`{"mcpServers": {
		"good": {"command": "npx"},
		"a\"b": {"command": "x"},
		"ctl\u0001name": {"command": "y"}
	}}`)
	if err != nil {
		t.Fatalf("合法条目应可解析: %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "good" {
		t.Fatalf("非法名条目应被丢弃: %+v", servers)
	}

	// 全部非法 → 识别为空（拒绝导入）。
	if _, err := ParseMCPJSON(`{"a\"b": {"command": "x"}}`); err == nil {
		t.Fatal("全部名字非法时应报无法识别")
	}
}

// C3(c)：ImportMCPServers 对含引号/控制字符的名字记 skipped「名称非法」。
func TestImportMCPServersInvalidNameSkipped(t *testing.T) {
	env := newTestService(t)
	imported, skipped, err := env.svc.ImportMCPServers([]SharedMCPServer{
		{Name: "ok-one", Config: map[string]any{"command": "npx"}},
		{Name: "a\"b", Config: map[string]any{"command": "x"}},
		{Name: "ctl\x01", Config: map[string]any{"command": "y"}},
	})
	if err != nil {
		t.Fatalf("部分成功不报错: %v", err)
	}
	if len(imported) != 1 || imported[0].Name != "ok-one" {
		t.Fatalf("合法条目应导入: %+v", imported)
	}
	if len(skipped) != 2 {
		t.Fatalf("两条非法名应 skipped: %v", skipped)
	}
	for _, s := range skipped {
		if !strings.Contains(s, "名称非法") {
			t.Fatalf("skipped 应注明名称非法: %v", skipped)
		}
	}
}

// C1：config.toml 不可读（读错误非 ErrNotExist）→ 写入报错且原文件字节
// 不变，绝不被静默替换为仅含 MCP 段的内容（diting D7 场景不复现）。
func TestMCPTOMLReadErrorNoClobber(t *testing.T) {
	env := newTestService(t)
	servers, err := ParseMCPJSON(`{"mcpServers": {"foo": {"command": "npx"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.svc.ImportMCPServers(servers); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedMCPServerTargets("foo", []Engine{EngineCodex}); err != nil {
		t.Fatal(err)
	}

	// 形态一（跨平台）：config.toml 是目录 → ReadFile 返回 EISDIR。
	if err := os.MkdirAll(filepath.Join(env.roots[EngineCodex], "config.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	err = env.svc.SetSharedMCPServerEnabled("foo", true)
	if err == nil || !strings.Contains(err.Error(), "读取") {
		t.Fatalf("读错误应立即返回而非按空文件覆盖: %v", err)
	}
	info, serr := os.Lstat(filepath.Join(env.roots[EngineCodex], "config.toml"))
	if serr != nil || !info.IsDir() {
		t.Fatalf("目标应保持目录原样未被写成文件: %v", serr)
	}

	// 形态二（diting D7 原取证）：chmod 000 的真实文件 → 报错且字节不变。
	if os.Geteuid() == 0 {
		t.Skip("root 用户 chmod 000 无法触发 permission denied")
	}
	env2 := newTestService(t)
	if _, _, err := env2.svc.ImportMCPServers(servers); err != nil {
		t.Fatal(err)
	}
	if err := env2.svc.SetSharedMCPServerTargets("foo", []Engine{EngineCodex}); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(env2.roots[EngineCodex], "config.toml")
	original := "model = \"gpt-5\"\nprofile = \"work\"\n[profiles.work]\nfoo = 1\n"
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfgPath, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := env2.svc.SetSharedMCPServerEnabled("foo", true); err == nil || !strings.Contains(err.Error(), "读取") {
		t.Fatalf("不可读文件上启用应报读错误: %v", err)
	}
	if err := os.Chmod(cfgPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, cfgPath); got != original {
		t.Fatalf("原文件字节应不变:\n--- got ---\n%s", got)
	}
}

// M1：多引擎部分失败（codex TOML 拒绝编辑、claude 成功）→ 返回聚合错误
// 但 registry 已记录成功引擎；后续禁用能把已写入的回收干净（diting D6
// 场景不复现）。
func TestMCPPartialDeployRecordsSuccess(t *testing.T) {
	env := newTestService(t)
	// codex config.toml 含受管作用域不支持形态（数组表头）→ 写入报错。
	if err := os.MkdirAll(env.roots[EngineCodex], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.roots[EngineCodex], "config.toml"),
		[]byte("[[mcp_servers.x]]\ncommand = \"a\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	servers, err := ParseMCPJSON(`{"mcpServers": {"foo": {"command": "npx", "args": ["x"]}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.svc.ImportMCPServers(servers); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedMCPServerTargets("foo", []Engine{EngineClaude, EngineCodex}); err != nil {
		t.Fatal(err)
	}

	// 启用：codex 失败、claude 成功 → 返回错误但 registry 记录 claude。
	enableErr := env.svc.SetSharedMCPServerEnabled("foo", true)
	if enableErr == nil {
		t.Fatal("预期 codex 部分失败返回聚合错误")
	}
	claudeJSON := filepath.Join(env.roots[EngineClaude], ".claude.json")
	if !strings.Contains(mustReadFile(t, claudeJSON), `"foo"`) {
		t.Fatal("claude 目标应已写入（磁盘实况）")
	}
	list, err := env.svc.ListSharedMCPServers()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sv := range list {
		if sv.Name == "foo" {
			found = true
			if !sv.Enabled || !reflect.DeepEqual([]Engine(sv.Deployed), []Engine{EngineClaude}) {
				t.Fatalf("registry 应记录 enabled=true deployed=[claude]: %+v", sv)
			}
		}
	}
	if !found {
		t.Fatal("条目应存在")
	}

	// 禁用：按 Deployed 回收 claude 写入（不再永久残留）。
	if err := env.svc.SetSharedMCPServerEnabled("foo", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mustReadFile(t, claudeJSON), `"foo"`) {
		t.Fatalf("禁用后 claude 写入应被回收: %s", mustReadFile(t, claudeJSON))
	}
}

// m1：目标 JSON 文件非法（用户手改坏）→ 写入与移除均拒绝且文件字节不变。
func TestMCPInvalidJSONTargetRefused(t *testing.T) {
	env := newTestService(t)
	claudeJSON := filepath.Join(env.roots[EngineClaude], ".claude.json")
	if err := os.MkdirAll(filepath.Dir(claudeJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := `{"broken":`
	if err := os.WriteFile(claudeJSON, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	servers, err := ParseMCPJSON(`{"mcpServers": {"foo": {"command": "npx"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.svc.ImportMCPServers(servers); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedMCPServerTargets("foo", []Engine{EngineClaude}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedMCPServerEnabled("foo", true); err == nil || !strings.Contains(err.Error(), "不是合法 JSON") {
		t.Fatalf("非法 JSON 目标应拒绝写入: %v", err)
	}
	if got := mustReadFile(t, claudeJSON); got != broken {
		t.Fatalf("原文件字节应不变（不写垃圾字节）:\n%s", got)
	}

	// 移除路径同样拒绝（不能假装回收成功）。
	target, err := env.svc.engineMCPTarget(EngineClaude)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.svc.removeMCPServer(target, "foo"); err == nil || !strings.Contains(err.Error(), "不是合法 JSON") {
		t.Fatalf("非法 JSON 目标应拒绝编辑: %v", err)
	}
	if got := mustReadFile(t, claudeJSON); got != broken {
		t.Fatalf("原文件字节应不变:\n%s", got)
	}
}
