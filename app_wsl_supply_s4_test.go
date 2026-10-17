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
	"time"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/wslsupply"
)

func newS4TestApp(t *testing.T) (*App, *wslsupply.Coordinator, string) {
	t.Helper()
	uncRoot := t.TempDir()
	c := wslsupply.NewCoordinator(t.TempDir(), nil, wslsupply.Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m1", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(context.Context, string, string, ...string) error { return nil },
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
	app, c, _ := newS4TestApp(t)
	// 固定默认 distro（真机探测在并发测试负载下有负缓存，不可依赖）。
	origDistro := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = origDistro })
	// SA14：先把目标身份代写入状态清单（生产路径中检查/供给都会写入），
	// 覆盖许可才能绑定目标。
	seedS4Generation(t, c)
	if err := app.SetWSLOpenCodeMCPSelection([]string{" b ", "a", ""}); err != nil {
		t.Fatalf("save: %v", err)
	}
	sel := app.loadWSLOpenCodeMCPSelection()
	entry, ok := sel.Distros["Ubuntu"]
	if !ok || len(entry.OverrideGlobal) != 2 || entry.OverrideGlobal[0] != "a" || entry.OverrideGlobal[1] != "b" {
		t.Fatalf("selection not round-tripped: %+v", entry)
	}
	if entry.Generation == "" {
		t.Fatalf("selection must record the bound generation: %+v", entry)
	}
	if got := app.wslOpenCodeMCPOverrideSet("Ubuntu"); !got["a"] || !got["b"] || len(got) != 2 {
		t.Fatalf("override set wrong: %v", got)
	}
	// 未登记 distro → nil（prepareWSLOpenCodeLayer 兼容 S1 全量语义）。
	if got := app.wslOpenCodeMCPOverrideSet("Other"); got != nil {
		t.Fatalf("unregistered distro must yield nil, got %v", got)
	}
}

// seedS4Generation 经真实 Inspect 把当前目标身份代写入状态清单。
func seedS4Generation(t *testing.T, c *wslsupply.Coordinator) string {
	t.Helper()
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", wslsupply.CLIPi)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := c.Inspect(context.Background(), target); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	return target.Generation
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
}

// ---------------------------------------------------------------------------
// SA14：覆盖许可绑定目标身份代（同名重装不继承）与未知身份拒绝
// ---------------------------------------------------------------------------

// TestWSLOpenCodeMCPSelectionSaveRefusedWhenIdentityUnresolvable：SA14 补全
// ——保存改经实时身份解析：解析失败（探测不可用）或身份代未知（无
// machine-id 的最小发行版）时拒绝保存，不落无法归属的覆盖许可；不依赖
// 状态清单是否已 seed（旧缓存语义已废弃）。
func TestWSLOpenCodeMCPSelectionSaveRefusedWhenIdentityUnresolvable(t *testing.T) {
	uncRoot := t.TempDir()
	makeApp := func(ident platform.WSLUserIdentity) *App {
		app := &App{configDir: t.TempDir(), WSLSupply: wslsupply.NewCoordinator(t.TempDir(), nil, wslsupply.Probes{
			Identity: func(context.Context, string) platform.WSLUserIdentity { return ident },
			ToUNC: func(_, linuxPath string) string {
				return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
			},
			Chmod: func(context.Context, string, string, ...string) error { return nil },
		})}
		if logDir, lerr := os.MkdirTemp("", "amagi-s4-sa14-refuse-*"); lerr == nil {
			app.Log = logging.NewService(logDir)
		}
		origDistro := wslDefaultDistroFn
		wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
		t.Cleanup(func() { wslDefaultDistroFn = origDistro })
		return app
	}
	// 身份探测失败（Identity !OK）→ 拒绝保存。
	appFail := makeApp(platform.WSLUserIdentity{UID: "1000", Home: "/home/u", OK: false})
	if err := appFail.SetWSLOpenCodeMCPSelection([]string{"a"}); err == nil {
		t.Fatal("save with an unresolvable identity must be refused")
	} else if !strings.Contains(err.Error(), "身份") {
		t.Fatalf("refusal must explain the identity resolution failure: %v", err)
	}
	// 探测成功但无 machine-id（身份代 unknown）→ 同样拒绝。
	appUnknown := makeApp(platform.WSLUserIdentity{UID: "1000", Home: "/home/u", OK: true})
	if err := appUnknown.SetWSLOpenCodeMCPSelection([]string{"a"}); err == nil {
		t.Fatal("save with an unknown generation must be refused")
	} else if !strings.Contains(err.Error(), "身份代") {
		t.Fatalf("refusal must explain the generation binding: %v", err)
	}
	for _, app := range []*App{appFail, appUnknown} {
		if sel := app.loadWSLOpenCodeMCPSelection(); len(sel.Distros) != 0 {
			t.Fatalf("refused save must not persist anything: %+v", sel.Distros)
		}
	}
}

// TestWSLOpenCodeMCPOverrideNotInheritedAfterReinstall：同名 distro 重装
// 换代（machine-id 变化）后，旧覆盖许可不再生效（需重新勾选）。
func TestWSLOpenCodeMCPOverrideNotInheritedAfterReinstall(t *testing.T) {
	uncRoot := t.TempDir()
	stateDir := t.TempDir()
	makeCoordinator := func(machineID string) *wslsupply.Coordinator {
		return wslsupply.NewCoordinator(stateDir, nil, wslsupply.Probes{
			Identity: func(context.Context, string) platform.WSLUserIdentity {
				return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: machineID, OK: true}
			},
			ToUNC: func(_, linuxPath string) string {
				return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
			},
			Chmod: func(context.Context, string, string, ...string) error { return nil },
		})
	}
	app := &App{configDir: t.TempDir(), WSLSupply: makeCoordinator("m-original")}
	if logDir, lerr := os.MkdirTemp("", "amagi-s4-sa14-logs-*"); lerr == nil {
		app.Log = logging.NewService(logDir)
	}
	origDistro := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = origDistro })

	seedS4Generation(t, app.WSLSupply.(*wslsupply.Coordinator))
	if err := app.SetWSLOpenCodeMCPSelection([]string{"fetch"}); err != nil {
		t.Fatalf("save with bound generation: %v", err)
	}
	if got := app.wslOpenCodeMCPOverrideSet("Ubuntu"); !got["fetch"] {
		t.Fatalf("override must apply on the same generation: %v", got)
	}

	// 同名 distro 重装：machine-id 变化 → 真实身份已换代。SA14 补全：
	// 生效面实时解析当前身份——旧状态缓存仍记旧代（未再检查）也不得让旧
	// 许可继续生效（这正是旧缓存语义的回归钉子）；条目留盘仅供诊断。
	app.WSLSupply = makeCoordinator("m-reinstalled")
	if got := app.wslOpenCodeMCPOverrideSet("Ubuntu"); got != nil {
		t.Fatalf("override grant must not survive a same-name reinstall even before re-inspection: %v", got)
	}
	// 身份解析彻底不可用（探测失败）同样返回空覆盖（保守保留本地）。
	app.WSLSupply = wslsupply.NewCoordinator(stateDir, nil, wslsupply.Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", OK: false}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(context.Context, string, string, ...string) error { return nil },
	})
	if got := app.wslOpenCodeMCPOverrideSet("Ubuntu"); got != nil {
		t.Fatalf("unresolvable identity must yield an empty override set: %v", got)
	}
	// 存储里的旧条目仍在（诊断面保留），只是不再生效。
	if entry, ok := app.loadWSLOpenCodeMCPSelection().Distros["Ubuntu"]; !ok || len(entry.OverrideGlobal) != 1 {
		t.Fatalf("stale entry must stay on disk for diagnosis: %+v", entry)
	}
}

// TestWSLOpenCodeMCPPreviewRowUnknownConflict：全局配置不可读（冲突未知）
// 映射为「不写入附加层」的可见行（needsAdapt + reason；不扩 Wails DTO）。
func TestWSLOpenCodeMCPPreviewRowUnknownConflict(t *testing.T) {
	row := wslOpenCodeMCPPreviewRow(launcher.OpenCodeMCPPreviewEntry{
		Name: "fetch", NativeCommand: "npx", GlobalConflictUnknown: true,
	}, false)
	if !row.NeedsAdapt || row.Reason == "" || !strings.Contains(row.Reason, "同名冲突无法确认") {
		t.Fatalf("unknown conflict must surface as needs-adapt with reason: %+v", row)
	}
	// 形态不可逆（NeedsAdapt）+ 未知冲突叠加时两条原因都保留。
	row2 := wslOpenCodeMCPPreviewRow(launcher.OpenCodeMCPPreviewEntry{
		Name: "win", NeedsAdapt: true, Reason: "命令为 Windows 专属形态", GlobalConflictUnknown: true,
	}, true)
	if !row2.NeedsAdapt || !strings.Contains(row2.Reason, "Windows 专属") || !strings.Contains(row2.Reason, "同名冲突无法确认") {
		t.Fatalf("both reasons must be preserved: %+v", row2)
	}
}

// TestImportWSLCliConfigKeysFullPathAndWithdraw（SA-03 App 贯通）：UI 形态的
// 全路径键经 App 门真实写入目标文件；空选择显式撤回已导入条目；全路径键
// 与同名叶双义时不误选。
func TestImportWSLCliConfigKeysFullPathAndWithdraw(t *testing.T) {
	app, c, uncRoot := newS4TestApp(t)
	home := t.TempDir()
	origHome := wslS4HomeDirFn
	wslS4HomeDirFn = func() (string, error) { return home, nil }
	t.Cleanup(func() { wslS4HomeDirFn = origHome })
	registerS4Adapters(c, app)
	origDistro := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = origDistro })

	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(
		`{"mcpServers":{"demo":{"url":"https://example.invalid/mcp"}}}`), 0o644)

	// UI 传预览全路径键（修复前叶名匹配落空 → fresh 虚报）。
	outcomes := app.ImportWSLCliConfigKeys("claude", string(wslsupply.UnitClaudeMCP), []string{"mcpServers/demo"})
	if len(outcomes) != 1 || outcomes[0].Outcome != "applied" {
		t.Fatalf("full-path key import must apply, got %+v", outcomes)
	}
	data, err := os.ReadFile(filepath.Join(uncRoot, filepath.FromSlash("home/u/.claude.json")))
	if err != nil || !strings.Contains(string(data), "example.invalid") {
		t.Fatalf("full-path key must reach target file: %q %v", data, err)
	}

	// 空选择显式撤回：托管条目按来源删除语义移除。
	outcomes = app.ImportWSLCliConfigKeys("claude", string(wslsupply.UnitClaudeMCP), nil)
	if len(outcomes) != 1 || outcomes[0].Outcome != "applied" {
		t.Fatalf("empty selection withdrawal must apply removal, got %+v", outcomes)
	}
	data, _ = os.ReadFile(filepath.Join(uncRoot, filepath.FromSlash("home/u/.claude.json")))
	if strings.Contains(string(data), "example.invalid") {
		t.Fatalf("withdrawal must remove managed server:\n%s", data)
	}

	// 未知全路径键：零导入（fresh），目标不再被改写。
	outcomes = app.ImportWSLCliConfigKeys("claude", string(wslsupply.UnitClaudeMCP), []string{"mcpServers/ghost"})
	if len(outcomes) != 1 || outcomes[0].Outcome != "fresh" {
		t.Fatalf("unknown key must yield fresh (no write), got %+v", outcomes)
	}
}

// TestCheckWSLCliResourcesForcesRescan（SA-17）：资源扫描完成后 Get 不重扫，
// 显式 Check 强制重扫并收敛新增的顶层 Markdown 命令文件。
func TestCheckWSLCliResourcesForcesRescan(t *testing.T) {
	app, c, _ := newS4TestApp(t)
	home := t.TempDir()
	origHome := wslS4HomeDirFn
	wslS4HomeDirFn = func() (string, error) { return home, nil }
	t.Cleanup(func() { wslS4HomeDirFn = origHome })
	registerS4Adapters(c, app)
	origDistro := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = origDistro })

	cmdDir := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "review.md"), []byte("# /review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Get 首启扫描并等待收敛。
	st := app.GetWSLCliSupplyStatus("claude")
	if !st.Available {
		t.Fatalf("status must be available: %+v", st)
	}
	deadline := time.Now().Add(10 * time.Second)
	for app.GetWSLCliSupplyStatus("claude").Resources.ScanRunning {
		if time.Now().After(deadline) {
			t.Fatal("initial scan did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	hasReview := func() bool {
		s := app.GetWSLCliSupplyStatus("claude")
		for _, sk := range s.Resources.Skills {
			if sk.Name == "commands/review.md" {
				return true
			}
		}
		return false
	}
	if !hasReview() {
		t.Fatal("initial scan must list commands/review.md (SA-10)")
	}

	// 扫描后新增文件：多次 Get 不重扫（新文件不可见）。
	if err := os.WriteFile(filepath.Join(cmdDir, "deploy.md"), []byte("# /deploy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if s := app.GetWSLCliSupplyStatus("claude"); s.Resources.ScanRunning {
			t.Fatal("Get must not restart a completed scan")
		}
	}
	hasDeploy := func() bool {
		s := app.GetWSLCliSupplyStatus("claude")
		for _, sk := range s.Resources.Skills {
			if sk.Name == "commands/deploy.md" {
				return true
			}
		}
		return false
	}
	deadline = time.Now().Add(10 * time.Second)
	for !hasDeploy() {
		app.CheckWSLCliResourcesSupply("claude")
		if hasDeploy() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Check must force rescan and converge deploy.md: %+v", app.GetWSLCliSupplyStatus("claude").Resources.Skills)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
