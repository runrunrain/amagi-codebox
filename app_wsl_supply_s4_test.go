package main

// app_wsl_supply_s4_test.go — P3-S4 App 编排面钉子：选择存储 round-trip、
// CLI 名校验、S4 适配器装配（真实协调器 + 注入 Home）、导入入口的
// 选择→同意→运行链路。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/wslsupply"
)

func newS4TestApp(t *testing.T) (*App, *wslsupply.Coordinator, string) {
	t.Helper()
	uncRoot := t.TempDir()
	c := wslsupply.NewCoordinator(t.TempDir(), nil, wslsupply.Probes{
		Identity: func(string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m1", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(_, _ string, _ ...string) error { return nil },
	})
	app := &App{configDir: t.TempDir(), WSLSupply: c}
	// 日志服务持有文件句柄（Windows 下 t.TempDir 的 RemoveAll 清理会因句柄
	// 占用深失败）；用不注册清理的临时目录承载日志。
	if logDir, lerr := os.MkdirTemp("", "amagi-s4-test-logs-*"); lerr == nil {
		app.Log = logging.NewService(logDir)
	}
	return app, c, uncRoot
}

func TestWSLCliFromNameValidation(t *testing.T) {
	for _, ok := range []string{"claude", "codex", "Claude", " codex "} {
		if _, err := wslCliFromName(ok); err != nil {
			t.Fatalf("%q must be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"pi", "opencode", "omp", "", "claude/res/x"} {
		if _, err := wslCliFromName(bad); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
}

func TestWSLOpenCodeMCPSelectionStoreRoundTrip(t *testing.T) {
	app, _, _ := newS4TestApp(t)
	// 固定默认 distro（真机探测在并发测试负载下有负缓存，不可依赖）。
	origDistro := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = origDistro })
	if err := app.SetWSLOpenCodeMCPSelection([]string{" b ", "a", ""}); err != nil {
		t.Fatalf("save: %v", err)
	}
	sel := app.loadWSLOpenCodeMCPSelection()
	entry, ok := sel.Distros["Ubuntu"]
	if !ok || len(entry.OverrideGlobal) != 2 || entry.OverrideGlobal[0] != "a" || entry.OverrideGlobal[1] != "b" {
		t.Fatalf("selection not round-tripped: %+v", entry)
	}
	if got := app.wslOpenCodeMCPOverrideSet("Ubuntu"); !got["a"] || !got["b"] || len(got) != 2 {
		t.Fatalf("override set wrong: %v", got)
	}
	// 未登记 distro → nil（prepareWSLOpenCodeLayer 兼容 S1 全量语义）。
	if got := app.wslOpenCodeMCPOverrideSet("Other"); got != nil {
		t.Fatalf("unregistered distro must yield nil, got %v", got)
	}
}

func TestRegisterS4AdaptersWiresUnitsAndResources(t *testing.T) {
	app, c, _ := newS4TestApp(t)
	home := t.TempDir()
	origHome := wslS4HomeDirFn
	wslS4HomeDirFn = func() (string, error) { return home, nil }
	t.Cleanup(func() { wslS4HomeDirFn = origHome })
	registerS4Adapters(c, app)

	for _, unit := range []wslsupply.UnitID{
		wslsupply.UnitClaudeMCP, wslsupply.UnitClaudeSettings, wslsupply.UnitCodexConfig,
		wslsupply.ResourceUnitID(wslsupply.CLIClaude, "CLAUDE.md"),
		wslsupply.ResourceUnitID(wslsupply.CLICodex, "AGENTS.md"),
	} {
		if !c.HasUnit(unit) {
			t.Fatalf("unit %s must be registered", unit)
		}
	}
	// pi 单元不在 claude/codex 面（cliConfigUnits 固定清单）。
	if isCliConfigUnit(wslsupply.CLIClaude, wslsupply.UnitPiMCP) {
		t.Fatal("pi units must not appear in the claude S4 unit list")
	}
}

func TestImportWSLCliConfigKeysEndToEnd(t *testing.T) {
	app, c, uncRoot := newS4TestApp(t)
	home := t.TempDir()
	origHome := wslS4HomeDirFn
	wslS4HomeDirFn = func() (string, error) { return home, nil }
	t.Cleanup(func() { wslS4HomeDirFn = origHome })
	registerS4Adapters(c, app)

	// Windows 侧来源：远程 MCP + 未知键（负面断言对象）。
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(
		`{"mcpServers":{"web":{"url":"https://mcp.example/sse"}},"projects":{"/win/proj":{}}}`), 0o644)

	// 固定默认 distro（真机探测在测试环境不稳定）。
	origDistro := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = origDistro })

	outcomes := app.ImportWSLCliConfigKeys("claude", string(wslsupply.UnitClaudeMCP), []string{"web"})
	if len(outcomes) != 1 || outcomes[0].Outcome != "applied" {
		t.Fatalf("import must apply, got %+v", outcomes)
	}
	data, err := os.ReadFile(filepath.Join(uncRoot, filepath.FromSlash("home/u/.claude.json")))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "mcp.example") {
		t.Fatalf("selected server must land: %s", got)
	}
	if strings.Contains(got, "/win/proj") {
		t.Fatalf("projects must never cross the boundary: %s", got)
	}
	// 无效 CLI/单元被拒。
	if out := app.ImportWSLCliConfigKeys("pi", "pi/mcp", nil); len(out) != 1 || out[0].Outcome != "blocked" {
		t.Fatalf("invalid cli must be blocked, got %+v", out)
	}
	if out := app.ImportWSLCliConfigKeys("claude", "codex/config", nil); len(out) != 1 || out[0].Outcome != "blocked" {
		t.Fatalf("cross-cli unit must be blocked, got %+v", out)
	}
}

func TestGetWSLCliSupplyStatusShape(t *testing.T) {
	app, c, _ := newS4TestApp(t)
	home := t.TempDir()
	origHome := wslS4HomeDirFn
	wslS4HomeDirFn = func() (string, error) { return home, nil }
	t.Cleanup(func() { wslS4HomeDirFn = origHome })
	registerS4Adapters(c, app)
	origDistro := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = origDistro })

	st := app.GetWSLCliSupplyStatus("claude")
	if !st.Available || len(st.Units) != 2 {
		t.Fatalf("claude status must have 2 keyed units, got %+v", st)
	}
	if st.Resources.Distro != "Ubuntu" {
		t.Fatalf("resources status distro: %+v", st.Resources)
	}
	if bad := app.GetWSLCliSupplyStatus("opencode"); bad.Available || bad.Reason == "" {
		t.Fatalf("unsupported cli must be rejected with reason, got %+v", bad)
	}
}

// 确保 wslOpenCodeMCPOverrideSet 空态返回 nil（prepareWSLOpenCodeLayer 兼容）。
func TestWSLOpenCodeMCPOverrideSetEmpty(t *testing.T) {
	app, _, _ := newS4TestApp(t)
	if got := app.wslOpenCodeMCPOverrideSet("Ubuntu"); got != nil {
		t.Fatalf("empty store must return nil, got %v", got)
	}
	_ = context.Background()
}
