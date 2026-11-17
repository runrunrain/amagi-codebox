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
