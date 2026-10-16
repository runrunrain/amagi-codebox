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
