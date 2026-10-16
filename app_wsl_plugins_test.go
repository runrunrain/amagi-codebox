package main

// app_wsl_plugins_test.go — P3-S5 App 编排面的行为钉子：
//   - 目标化操作每目标独立结果（Windows ok + WSL 失败不合并报喜；
//     Windows 失败时 both 模式 WSL skipped——无锚点不盲装）；
//   - 「同时应用」锚点透传（Windows 实体解析版本 → WSL 重放 AnchorRef）；
//   - 结构性保证：启动闸门单元清单与保存推送路径都不触达插件重放
//     （启动不自动安装；保存只标脏，插件操作只能经显式目标化入口）。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/piplugin"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/wslsupply"
)

// appPluginResolver 复刻 piplugin 测试解析桩（root 测试包独立副本）。
type appPluginResolver struct{}

func (appPluginResolver) Resolve(platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	return platform.ResolvedLaunchSpec{}, nil
}

func (appPluginResolver) ResolveExecutable(name string, args []string, _ []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return platform.ResolvedCLI{Path: "/fake/" + name, Args: append([]string(nil), args...)}, platform.LaunchDiagnostics{}, nil
}

// appPluginRunner 记录 Windows 侧 pi CLI 调用。
type appPluginRunner struct {
	mu     sync.Mutex
	args   []string
	stdout string
	fail   bool
}

func (r *appPluginRunner) Start(platform.CommandSpec) (*exec.Cmd, error) {
	panic("not used")
}

func (r *appPluginRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	r.args = append(r.args, strings.Join(spec.Args, " "))
	r.mu.Unlock()
	if r.fail {
		return &platform.ProcessResult{}, errors.New("pi failed")
	}
	return &platform.ProcessResult{Stdout: r.stdout}, nil
}

func (r *appPluginRunner) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.args...)
}

// stubPluginSupplyRunner 同时实现 wslsupply.Runner 与 wslPluginCoordinator：
// 记录插件重放调用，可注入返回结果。
type stubPluginSupplyRunner struct {
	stubSupplyRunner
	mu        sync.Mutex
	pluginOps []wslsupply.PluginOperation
	pluginRes wslsupply.PluginOpResult
}

func (s *stubPluginSupplyRunner) RunPluginOperation(_ context.Context, _ string, op wslsupply.PluginOperation) wslsupply.PluginOpResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pluginOps = append(s.pluginOps, op)
	return s.pluginRes
}

func (s *stubPluginSupplyRunner) ProbeWSLPlugins(context.Context, string) (wslsupply.WSLPluginSnapshot, error) {
	return wslsupply.WSLPluginSnapshot{Distro: "Ubuntu", Checked: true, Items: []wslsupply.WSLPluginItem{}}, nil
}

func (s *stubPluginSupplyRunner) WSLPluginsCachedSnapshot(distro string) wslsupply.WSLPluginSnapshot {
	return wslsupply.WSLPluginSnapshot{Distro: distro, Items: []wslsupply.WSLPluginItem{}}
}

func (s *stubPluginSupplyRunner) PluginRegistrationSummary() wslsupply.PluginRegistrationSummary {
	return wslsupply.PluginRegistrationSummary{}
}

func (s *stubPluginSupplyRunner) pluginOps_() []wslsupply.PluginOperation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]wslsupply.PluginOperation(nil), s.pluginOps...)
}

// newPluginTargetedTestApp 构造注入桩的 App：PiPlugins 指向真实
// piplugin.Service（实体锚点读取走真逻辑），WSLSupply 用插件记录桩。
func newPluginTargetedTestApp(t *testing.T, cliOK bool) (*App, *appPluginRunner, *stubPluginSupplyRunner, string) {
	t.Helper()
	app := newTestApp(t)
	cliRunner := &appPluginRunner{stdout: "installed", fail: !cliOK}
	agentDir := t.TempDir()
	app.PiPlugins = piplugin.NewServiceWithDeps(agentDir, nil, appPluginResolver{}, cliRunner)
	supply := &stubPluginSupplyRunner{pluginRes: wslsupply.PluginOpResult{
		Source: "npm:foo", Kind: wslsupply.PluginOpInstall, OK: true,
		Outcome: wslsupply.PluginOutcomeInstalled, PinnedRef: "1.3.0", EntityVerified: true,
	}}
	app.WSLSupply = supply
	prev := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = prev })
	return app, cliRunner, supply, agentDir
}

// writeEntityInService 在 piplugin 服务的 agent 根下建 npm 实体（锚点读取走
// 服务真实布局 agentDir/npm/node_modules/<name>/package.json）。
func writeEntityInService(t *testing.T, agentDir, name, version string) {
	t.Helper()
	root := filepath.Join(agentDir, "npm", "node_modules", filepath.FromSlash(name))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"name": name, "version": version})
	if err := os.WriteFile(filepath.Join(root, "package.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeAgentSettings 写 pi settings.json 的 packages 登记面。
func writeAgentSettings(t *testing.T, agentDir string, packages []string) {
	t.Helper()
	pkgs := make([]any, 0, len(packages))
	for _, p := range packages {
		pkgs = append(pkgs, p)
	}
	data, _ := json.Marshal(map[string]any{"packages": pkgs})
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallPiPackageTargetedWindowsOnly(t *testing.T) {
	app, cli, supply, _ := newPluginTargetedTestApp(t, true)
	res, err := app.InstallPiPackageTargeted("npm:foo", "windows")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Results[0].Target != "windows" || !res.Results[0].OK {
		t.Fatalf("windows-only result: %+v", res.Results)
	}
	if len(supply.pluginOps_()) != 0 {
		t.Fatalf("windows target must not touch wsl")
	}
	if got := cli.recorded(); len(got) != 1 || !strings.Contains(got[0], "install") {
		t.Fatalf("windows cli must run once: %v", got)
	}
}

func TestInstallPiPackageTargetedIndependentResults(t *testing.T) {
	// 单侧失败独立显示：Windows ok + WSL 失败。
	app, _, supply, _ := newPluginTargetedTestApp(t, true)
	supply.pluginRes = wslsupply.PluginOpResult{
		Source: "npm:foo", Kind: wslsupply.PluginOpInstall, Outcome: wslsupply.PluginOutcomeFailed,
		ErrCode: wslsupply.PluginErrReplayFailed, Detail: "pi install 失败",
	}
	res, err := app.InstallPiPackageTargeted("npm:foo", "both")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("both must produce two independent results: %+v", res.Results)
	}
	if res.Results[0].Target != "windows" || !res.Results[0].OK {
		t.Fatalf("windows outcome: %+v", res.Results[0])
	}
	wsl := res.Results[1]
	if wsl.Target != "wsl" || wsl.OK || wsl.Outcome != "failed" || wsl.ErrCode != wslsupply.PluginErrReplayFailed {
		t.Fatalf("wsl outcome must surface failure independently: %+v", wsl)
	}
}

func TestInstallPiPackageTargetedWindowsFailSkipsWSL(t *testing.T) {
	app, _, supply, _ := newPluginTargetedTestApp(t, false)
	res, err := app.InstallPiPackageTargeted("npm:foo", "both")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("both must still report both targets: %+v", res.Results)
	}
	if res.Results[0].OK {
		t.Fatalf("windows must be failed: %+v", res.Results[0])
	}
	if res.Results[1].Outcome != "skipped" || res.Results[1].ErrCode != "windows-anchor-missing" {
		t.Fatalf("wsl must be skipped without anchor: %+v", res.Results[1])
	}
	if len(supply.pluginOps_()) != 0 {
		t.Fatalf("no wsl replay without anchor")
	}
}

func TestInstallPiPackageTargetedWSLOnlyIndependentResolve(t *testing.T) {
	app, _, supply, _ := newPluginTargetedTestApp(t, true)
	if _, err := app.InstallPiPackageTargeted("npm:foo", "wsl"); err != nil {
		t.Fatal(err)
	}
	ops := supply.pluginOps_()
	if len(ops) != 1 {
		t.Fatalf("wsl target must replay once: %d", len(ops))
	}
	if ops[0].AnchorRef != "" {
		t.Fatalf("wsl-only must not carry a host anchor (distro resolves): %+v", ops[0])
	}
}

func TestInstallPiPackageTargetedBothPassesHostAnchor(t *testing.T) {
	app, _, supply, agentDir := newPluginTargetedTestApp(t, true)
	// Windows 实体存在 1.4.2（锚点读取走真实 piplugin 逻辑）。
	writeEntityInService(t, agentDir, "foo", "1.4.2")
	res, err := app.InstallPiPackageTargeted("npm:foo", "both")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Results[0].OK || !res.Results[1].OK {
		t.Fatalf("both sides ok: %+v", res.Results)
	}
	ops := supply.pluginOps_()
	if len(ops) != 1 || ops[0].AnchorRef != "1.4.2" {
		t.Fatalf("both mode must anchor replay to host-resolved version: %+v", ops)
	}
}

func TestUpdatePiPackageTargetedAnchorFlow(t *testing.T) {
	app, _, supply, agentDir := newPluginTargetedTestApp(t, true)
	writeEntityInService(t, agentDir, "foo", "2.1.0")
	// Windows 侧 settings 需已登记（piplugin UpdatePackage 的前置校验）。
	writeAgentSettings(t, agentDir, []string{"npm:foo"})
	if _, err := app.UpdatePiPackageTargeted("npm:foo", "both"); err != nil {
		t.Fatal(err)
	}
	ops := supply.pluginOps_()
	if len(ops) != 1 || ops[0].Kind != wslsupply.PluginOpUpdate || ops[0].AnchorRef != "2.1.0" {
		t.Fatalf("update must replay with host anchor: %+v", ops)
	}
}

func TestRemovePiPackageTargetedIndependent(t *testing.T) {
	// remove 无锚点语义：两侧独立执行。
	app, _, supply, _ := newPluginTargetedTestApp(t, false) // Windows 失败
	res, err := app.RemovePiPackageTargeted("npm:foo", "both")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("remove both must report both targets: %+v", res.Results)
	}
	if res.Results[0].OK {
		t.Fatalf("windows failed as injected: %+v", res.Results[0])
	}
	if !res.Results[1].OK || res.Results[1].Outcome != "installed" {
		t.Fatalf("wsl remove must run independently of windows outcome: %+v", res.Results[1])
	}
	if len(supply.pluginOps_()) != 1 || supply.pluginOps_()[0].Kind != wslsupply.PluginOpRemove {
		t.Fatalf("wsl remove must replay once")
	}
}

func TestPiPluginTargetValidation(t *testing.T) {
	app, _, _, _ := newPluginTargetedTestApp(t, true)
	if _, err := app.InstallPiPackageTargeted("npm:foo", "mars"); err == nil {
		t.Fatalf("unknown target must error")
	}
}

func TestGetWSLPluginTargetSupportContract(t *testing.T) {
	app, _, _, _ := newPluginTargetedTestApp(t, true)
	support := app.GetWSLPluginTargetSupport()
	byCLI := map[string]string{}
	for _, e := range support {
		byCLI[e.CLI] = e.Support
	}
	if byCLI["pi"] != "operations" {
		t.Fatalf("pi must be operations: %+v", byCLI)
	}
	for _, cli := range []string{"claude", "codex", "opencode"} {
		if byCLI[cli] != "status-placeholder" {
			t.Fatalf("%s must be status-placeholder: %+v", cli, byCLI)
		}
	}
	if byCLI["omp"] != "excluded" {
		t.Fatalf("omp must be excluded (B7): %+v", byCLI)
	}
}

// ---------------------------------------------------------------------------
// 结构性保证：启动与保存推送路径不触达插件重放
// ---------------------------------------------------------------------------

func TestPluginOpsUnreachableFromLaunchAndSave(t *testing.T) {
	app, _, supply, _ := newPluginTargetedTestApp(t, true)
	// 启动闸门单元清单不含任何插件面（插件不在 RunUnits 词汇表；真协调器
	// 对插件形态单元的拒绝钉在 wslsupply 包 TestRunUnitsRejectsPluginShapedUnits）。
	units := wslSupplyUnitsFiltered(app.WSLSupply,
		wslsupply.UnitPiA2Auth, wslsupply.UnitPiManagedModels, wslsupply.UnitPiSettings, wslsupply.UnitPiAmagi, wslsupply.UnitPiMCP)
	for _, u := range units {
		if strings.HasPrefix(string(u), "pi/plugin") {
			t.Fatalf("launch units must never include plugin ops: %s", u)
		}
	}
	// 保存标脏 + 异步推送（distro 运行中）不触发插件重放。
	if err := app.WSLSupply.MarkDirty(wslsupply.DirtySourceProviders, 1); err != nil {
		t.Fatal(err)
	}
	prevRunning := wslDistroRunningFn
	wslDistroRunningFn = func(string) bool { return true }
	t.Cleanup(func() { wslDistroRunningFn = prevRunning })
	app.pushWSLSupplyOnce()
	if ops := supply.pluginOps_(); len(ops) != 0 {
		t.Fatalf("save/push path must never invoke plugin replay: %+v", ops)
	}
	if calls := supply.calls_(); len(calls) == 0 {
		t.Fatalf("push path still runs its config units (sanity)")
	}
}

func TestGetPiPluginWSLStatusNoWake(t *testing.T) {
	app, _, _, _ := newPluginTargetedTestApp(t, true)
	st := app.GetPiPluginWSLStatus()
	if !st.Available || st.Distro != "Ubuntu" {
		t.Fatalf("status: %+v", st)
	}
	// 缓存面不触发探测（snapshot 来自 CachedSnapshot，不唤醒）。
	if st.Snapshot.Checked {
		t.Fatalf("unchecked snapshot must be honest (Checked=false)")
	}
	if st.Snapshot.Distro != "Ubuntu" {
		t.Fatalf("distro must be carried")
	}
	checked := app.CheckPiPluginWSLStatus()
	if !checked.Snapshot.Checked {
		t.Fatalf("explicit check must return checked snapshot")
	}
}
