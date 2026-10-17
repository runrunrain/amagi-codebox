package launcher

// wsl_opencode_mcp_s4_test.go — P3-S4 选择门钉子：AdaptWSLOpenCodeMCPWithOptions
// 的 allowed 过滤与 overrideGlobal 显式覆盖（验收 4：全局与附加层同名冲突遵循
// 选择——保留本地默认 + 显式覆盖可选）+ PreviewWSLOpenCodeMCP 只读分类。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdaptWSLOpenCodeMCPWithOptionsAllowedFilter(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
	cfg := mcpConfig(map[string]any{
		"a": map[string]any{"command": "npx", "args": []any{"-y", "pkg-a"}},
		"b": map[string]any{"command": "npx", "args": []any{"-y", "pkg-b"}},
	})
	out, report := AdaptWSLOpenCodeMCPWithOptions("Ubuntu", cfg, map[string]bool{"a": true}, nil)
	mcp := out["mcp"].(map[string]any)
	if _, ok := mcp["a"]; !ok {
		t.Fatal("selected server must be written")
	}
	if _, ok := mcp["b"]; ok {
		t.Fatal("unselected server must be dropped")
	}
	found := false
	for _, line := range report {
		if strings.Contains(line, "b") && strings.Contains(line, "未选择导入") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unselected drop must be reported: %v", report)
	}
	// allowed==nil 沿用 S1 全量语义（不回归既有调用方）。
	out2, _ := AdaptWSLOpenCodeMCPWithOptions("Ubuntu", cfg, nil, nil)
	if len(out2["mcp"].(map[string]any)) != 2 {
		t.Fatal("nil allowed must keep legacy full semantics")
	}
}

func TestAdaptWSLOpenCodeMCPWithOptionsOverrideGlobal(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
	ft := installFakeWSLTarget(t)
	global := `{"mcp": {"fetch": {"command": "npx", "args": ["-y", "other"]}}}`
	if err := os.MkdirAll(ft.uncPath("/home/wslu/.config/opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ft.uncPath("/home/wslu/.config/opencode"), "opencode.json"), []byte(global), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := mcpConfig(map[string]any{
		"fetch": map[string]any{"command": "npx.cmd", "args": []any{"-y", "pkg"}},
	})

	// 默认（未勾选覆盖）：保留本地，不写入附加层。
	out, report := AdaptWSLOpenCodeMCPWithOptions("Ubuntu", cfg, nil, nil)
	if mcp, ok := out["mcp"].(map[string]any); ok {
		if _, has := mcp["fetch"]; has {
			t.Fatal("default must keep local (not written into layer)")
		}
	}
	keptReported := false
	for _, line := range report {
		if strings.Contains(line, "保留本地") {
			keptReported = true
		}
	}
	if !keptReported {
		t.Fatalf("keep-local must be reported: %v", report)
	}

	// 显式覆盖：写入附加层（shim 经验证映射后）。
	out2, report2 := AdaptWSLOpenCodeMCPWithOptions("Ubuntu", cfg, nil, map[string]bool{"fetch": true})
	entry, ok := out2["mcp"].(map[string]any)["fetch"]
	if !ok {
		t.Fatalf("override selection must write the server: %v report=%v", out2, report2)
	}
	if entry.(map[string]any)["command"] != "npx" {
		t.Fatal("override write must use the mapped native command")
	}
}

func TestPreviewWSLOpenCodeMCPClassifies(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
	ft := installFakeWSLTarget(t)
	global := `{"mcp": {"remote": {"url": "https://g.example/sse"}}}`
	if err := os.MkdirAll(ft.uncPath("/home/wslu/.config/opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ft.uncPath("/home/wslu/.config/opencode"), "opencode.json"), []byte(global), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := mcpConfig(map[string]any{
		"remote":  map[string]any{"url": "https://mcp.example/sse"},
		"shim":    map[string]any{"command": "npx.cmd", "args": []any{"-y", "pkg"}},
		"windows": map[string]any{"command": "setup.bat"},
	})
	entries := PreviewWSLOpenCodeMCP("Ubuntu", cfg)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %v", entries)
	}
	byName := map[string]OpenCodeMCPPreviewEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if !byName["remote"].Remote || !byName["remote"].GlobalConflict {
		t.Fatalf("remote entry must be remote + global conflict: %+v", byName["remote"])
	}
	if byName["shim"].NeedsAdapt || byName["shim"].NativeCommand != "npx" {
		t.Fatalf("verified shim must map to native npx: %+v", byName["shim"])
	}
	if !byName["windows"].NeedsAdapt || byName["windows"].Reason == "" {
		t.Fatalf("windows-only form must be needs-adapt with reason: %+v", byName["windows"])
	}
	// 预览必须只读：cfg 不被改写。
	if _, ok := cfg["mcp"].(map[string]any)["windows"]; !ok {
		t.Fatal("preview must not mutate the config")
	}
}

// ---------------------------------------------------------------------------
// SA14：全局配置读取边界——jsonc、尾逗号、不可读不当空、双入口并集
// ---------------------------------------------------------------------------

// writeWSLOpenCodeGlobal 写 distro 全局配置文件（name 相对 ~/.config/opencode）。
func writeWSLOpenCodeGlobal(t *testing.T, ft *fakeWSLTarget, name, content string) {
	t.Helper()
	dir := ft.uncPath("/home/wslu/.config/opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAdaptWSLOpenCodeMCPJSONCAndTrailingCommas(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
	ft := installFakeWSLTarget(t)
	// 合法 JSONC：行注释 + 块注释 + 尾逗号（旧实现解析失败回退空集 → 同名
	// preset 覆盖本地选择）。
	writeWSLOpenCodeGlobal(t, ft, "opencode.jsonc", `{
		// my global servers
		"mcp": {
			"fetch": {"command": "npx", "args": ["-y", "other"],}, /* block */
		},
	}`)
	cfg := mcpConfig(map[string]any{
		"fetch":  map[string]any{"command": "npx.cmd", "args": []any{"-y", "pkg"}},
		"other2": map[string]any{"command": "npx", "args": []any{"pkg2"}},
	})
	out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
	mcp := out["mcp"].(map[string]any)
	if _, ok := mcp["fetch"]; ok {
		t.Fatal("jsonc trailing-comma global conflict must keep local (not written)")
	}
	if _, ok := mcp["other2"]; !ok {
		t.Fatal("non-conflicting server must be kept")
	}
	found := false
	for _, line := range report {
		if strings.Contains(line, "fetch") && strings.Contains(line, "全局") {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflict must be reported: %v", report)
	}
}

func TestAdaptWSLOpenCodeMCPJSONAndJSONCUnion(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
	ft := installFakeWSLTarget(t)
	// 两个入口都可能被 CLI 消费：并集判定冲突（保守）。
	writeWSLOpenCodeGlobal(t, ft, "opencode.json", `{"mcp": {"a": {"command": "npx"}}}`)
	writeWSLOpenCodeGlobal(t, ft, "opencode.jsonc", `{"mcp": {"b": {"command": "npx"}}}`)
	cfg := mcpConfig(map[string]any{
		"a": map[string]any{"command": "npx"},
		"b": map[string]any{"command": "npx"},
		"c": map[string]any{"command": "npx"},
	})
	out, _ := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
	mcp := out["mcp"].(map[string]any)
	if _, ok := mcp["a"]; ok {
		t.Fatal("json-file conflict must keep local")
	}
	if _, ok := mcp["b"]; ok {
		t.Fatal("jsonc-file conflict must keep local")
	}
	if _, ok := mcp["c"]; !ok {
		t.Fatal("unconflicted server must be written")
	}
}

// TestAdaptWSLOpenCodeMCPUnreadableGlobalDropsAdditiveWrites：全局文件存在
// 但不可解析 → 「未知」≠「不冲突」：任何 preset MCP 都不写入附加层（默认
// 保留本地选择；启动继续，逐条报告可见）。旧实现回退空集把同名 server 写入
// 高优先级附加层覆盖本地。
func TestAdaptWSLOpenCodeMCPUnreadableGlobalDropsAdditiveWrites(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
	ft := installFakeWSLTarget(t)
	writeWSLOpenCodeGlobal(t, ft, "opencode.json", `{"mcp": {broken`)
	cfg := mcpConfig(map[string]any{
		"fetch":  map[string]any{"command": "npx.cmd", "args": []any{"-y", "pkg"}},
		"other2": map[string]any{"command": "npx", "args": []any{"pkg2"}},
	})
	out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
	if _, ok := out["mcp"]; ok {
		t.Fatalf("unknown conflict state must not write any additive-layer mcp: %v", out["mcp"])
	}
	if len(report) != 2 {
		t.Fatalf("every dropped server must be reported: %v", report)
	}
	for _, line := range report {
		if !strings.Contains(line, "不可读") || !strings.Contains(line, "同名冲突无法确认") {
			t.Fatalf("report must state the unknown-conflict reason: %v", line)
		}
	}
	// 覆盖勾选也不能越过「未知」状态（不知道本地是否有同名项）。
	out2, report2 := AdaptWSLOpenCodeMCPWithOptions("Ubuntu", cfg, nil, map[string]bool{"fetch": true})
	if _, ok := out2["mcp"]; ok {
		t.Fatalf("override must not bypass unknown conflict state: %v", out2["mcp"])
	}
	if len(report2) != 2 {
		t.Fatalf("override attempt must still report drops: %v", report2)
	}
}

// TestAdaptWSLOpenCodeMCPHomeProbeFailureIsUnknown：目标探测失败（HOME 不可
// 得）不再回退「无冲突」空集——同样按未知处理。
func TestAdaptWSLOpenCodeMCPHomeProbeFailureIsUnknown(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
	installFakeWSLTarget(t)
	origHome := wslUserHomeFn
	wslUserHomeFn = func(string) string { return "" }
	t.Cleanup(func() { wslUserHomeFn = origHome })
	cfg := mcpConfig(map[string]any{
		"fetch": map[string]any{"command": "npx", "args": []any{"pkg"}},
	})
	out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
	if _, ok := out["mcp"]; ok {
		t.Fatalf("probe failure must not write additive-layer mcp: %v", out["mcp"])
	}
	if len(report) != 1 || !strings.Contains(report[0], "无法探测") {
		t.Fatalf("probe failure must be reported as unknown: %v", report)
	}
}

// TestPreviewWSLOpenCodeMCPUnknownConflictFlag：预览面携带结构化的「冲突未
// 知」标记与原因（s4 选择面据此隐藏覆盖勾选并展示不写入原因）。
func TestPreviewWSLOpenCodeMCPUnknownConflictFlag(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
	ft := installFakeWSLTarget(t)
	writeWSLOpenCodeGlobal(t, ft, "opencode.jsonc", `{"mcp": [`)
	cfg := mcpConfig(map[string]any{
		"fetch": map[string]any{"command": "npx.cmd", "args": []any{"-y", "pkg"}},
	})
	previews := PreviewWSLOpenCodeMCP("Ubuntu", cfg)
	if len(previews) != 1 {
		t.Fatalf("one preview entry expected: %+v", previews)
	}
	if !previews[0].GlobalConflictUnknown || previews[0].GlobalConflict {
		t.Fatalf("unknown state must be flagged structurally: %+v", previews[0])
	}
	if !strings.Contains(previews[0].Reason, "同名冲突无法确认") {
		t.Fatalf("reason must explain unknown conflict: %+v", previews[0])
	}
}

// TestStripJSONCTrailingCommas：尾逗号剥离的确定性钉子（字符串字面量内的
// 逗号/花括号不受影响）。
func TestStripJSONCTrailingCommas(t *testing.T) {
	in := `{"a": [1, 2, ], "b": {"c": "x,y}", }, "s": "not,a}bracket"}`
	want := `{"a": [1, 2 ], "b": {"c": "x,y}" }, "s": "not,a}bracket"}`
	if got := string(stripJSONCTrailingCommas([]byte(in))); got != want {
		t.Fatalf("strip mismatch:\n got %s\nwant %s", got, want)
	}
}
