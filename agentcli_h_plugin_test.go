package main

// agentcli_h_plugin_test.go — 插件域 Handler 测试：
//   - 命令表 ↔ Handler 1:1（session/plugin 两域）；
//   - list 统一 DTO（pi 引擎走真实文件读取；其余引擎注入失败 resolver 验证聚合 errors）；
//   - enable/disable 对无启停面引擎 → bad_args；
//   - codex plugin@marketplace 预检 → bad_args（不触碰引擎）；
//   - agentPluginCommandOutcome 收口语义。
// 不安装/卸载真实插件、不执行各 CLI 二进制（codex/opencode/omp 注入失败 resolver）。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/cmds"
	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/codexplugin"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/ompplugin"
	"amagi-codebox/internal/opencodeplugin"
	"amagi-codebox/internal/piplugin"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/plugin"
)

// TestAgentCLIDomainHandlers_MatchCommandTable 断言 session/plugin 两域
// 命令表条目与 Handler 一一对应（A1 的全表测试未落地前的本域锚点）。
func TestAgentCLIDomainHandlers_MatchCommandTable(t *testing.T) {
	h := newAgentCLIHost(newTestApp(t))
	handlers := h.handlers()
	for _, c := range cmds.Table() {
		name := c.Name()
		if !strings.HasPrefix(name, "session.") && !strings.HasPrefix(name, "plugin.") {
			continue
		}
		if _, ok := handlers[name]; !ok {
			t.Errorf("命令 %s 缺少 Handler", name)
		}
	}
	// 反向：session/plugin Handler 没有表外命令。
	for name := range h.sessionHandlers() {
		if _, ok := cmds.Find(cmds.Table(), name); !ok {
			t.Errorf("Handler %s 不在命令表中", name)
		}
	}
	for name := range h.pluginHandlers() {
		if _, ok := cmds.Find(cmds.Table(), name); !ok {
			t.Errorf("Handler %s 不在命令表中", name)
		}
	}
}

// writePiPackagesFixture 写一个最小 pi settings.json（packages[]）。
func writePiPackagesFixture(t *testing.T, agentDir, source string) {
	t.Helper()
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir pi agentDir: %v", err)
	}
	body := fmt.Sprintf(`{"packages": [%q]}`, source)
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write pi settings.json: %v", err)
	}
}

// newPluginTestApp 构造一个仅含插件服务的 App（五引擎全部指向临时目录）。
// codex/opencode/omp 注入恒失败 resolver，保证不执行任何真实 CLI 二进制。
func newPluginTestApp(t *testing.T, piAgentDir string) *App {
	t.Helper()
	logSvc := logging.NewService(t.TempDir())
	t.Cleanup(logSvc.Close)
	app := newTestApp(t)
	app.Plugins = plugin.NewService(t.TempDir(), logSvc)
	resolver, runner := agentFailingResolver{}, agentFailingRunner{}
	app.CodexPlugins = codexplugin.NewServiceWithDeps(t.TempDir(), logSvc, resolver, runner)
	app.OpenCodePlugins = opencodeplugin.NewServiceWithDeps(t.TempDir(), t.TempDir(), logSvc, resolver, runner)
	app.OmpPlugins = ompplugin.NewServiceWithDeps(logSvc, resolver, runner)
	app.PiPlugins = piplugin.NewService(piAgentDir, logSvc)
	return app
}

type agentFailingResolver struct{}

func (agentFailingResolver) Resolve(platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	return platform.ResolvedLaunchSpec{}, errors.New("test: resolver 未配置")
}

func (agentFailingResolver) ResolveExecutable(command string, _ []string, _ []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return platform.ResolvedCLI{}, platform.LaunchDiagnostics{}, fmt.Errorf("test: %s 不可用", command)
}

type agentFailingRunner struct{}

func (agentFailingRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, errors.New("test: runner 未配置")
}

func (agentFailingRunner) Run(_ context.Context, _ platform.CommandSpec) (*platform.ProcessResult, error) {
	return nil, errors.New("test: runner 未配置")
}

func TestAgentCLIPluginList_SingleAppPI(t *testing.T) {
	piDir := t.TempDir()
	writePiPackagesFixture(t, piDir, "npm:@foo/bar@1.0.0")
	h := newAgentCLIHost(newPluginTestApp(t, piDir))

	res, err := h.pluginList(context.Background(), spec.Args{"app": "pi"})
	if err != nil {
		t.Fatalf("pluginList: %v", err)
	}
	resp := res.(map[string]any)
	if _, hasErrs := resp["errors"]; hasErrs {
		t.Errorf("单引擎列表不应有 errors: %+v", resp["errors"])
	}
	plugins := resp["plugins"].([]agentPluginSummary)
	if len(plugins) != 1 {
		t.Fatalf("pi 列表长度 = %d, want 1: %+v", len(plugins), plugins)
	}
	got := plugins[0]
	if got.App != "pi" || got.ID != "npm:@foo/bar@1.0.0" || got.Source != "npm:@foo/bar@1.0.0" || !got.Enabled {
		t.Errorf("统一 DTO 映射异常: %+v", got)
	}
}

// TestAgentCLIPluginList_Aggregate 聚合五引擎：pi 走 fixture 成功，
// codex/opencode/omp（失败 resolver）进入 errors 字段且不中断聚合。
// claudecode 引擎受环境影响（本机装有 claude CLI 时走真实 CLI），
// 不对其结果做断言；PATH/HOME 指向临时目录以尽量隔离解析。
func TestAgentCLIPluginList_Aggregate(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	piDir := t.TempDir()
	writePiPackagesFixture(t, piDir, "npm:@foo/bar@1.0.0")
	h := newAgentCLIHost(newPluginTestApp(t, piDir))

	res, err := h.pluginList(context.Background(), spec.Args{})
	if err != nil {
		t.Fatalf("pluginList aggregate: %v", err)
	}
	resp := res.(map[string]any)
	plugins := resp["plugins"].([]agentPluginSummary)
	foundPI := false
	for _, p := range plugins {
		if p.App == "pi" && p.ID == "npm:@foo/bar@1.0.0" {
			foundPI = true
		}
	}
	if !foundPI {
		t.Errorf("聚合列表缺少 pi fixture 条目: %+v", plugins)
	}
	errs, ok := resp["errors"].(map[string]string)
	if !ok {
		t.Fatalf("聚合 errors 类型 %T", resp["errors"])
	}
	// codex/omp：CLI 不可用且临时目录无 config → 进入 errors。
	// opencode：readConfiguredSpecs 对缺失 config 容忍（返回空列表，不算错误），
	// 因此 opencode 不在 errors 中是合法结果，不做断言。
	for _, app := range []string{"codex", "omp"} {
		if _, has := errs[app]; !has {
			t.Errorf("聚合 errors 缺少 %s: %+v", app, errs)
		}
	}
}

func TestAgentCLIPluginList_UnknownApp(t *testing.T) {
	h := newAgentCLIHost(newPluginTestApp(t, t.TempDir()))
	if _, err := h.pluginList(context.Background(), spec.Args{"app": "vscode"}); specErrorCode(t, err) != spec.ErrBadArgs {
		t.Errorf("未知 app 应 bad_args，得到 %v", err)
	}
}

// TestAgentCLIPluginEnable_UnsupportedEngines opencode/pi 无启停面 → bad_args，
// 且消息说明原因（引擎字段为 nil 也应安全返回，证明未触碰引擎）。
func TestAgentCLIPluginEnable_UnsupportedEngines(t *testing.T) {
	h := newAgentCLIHost(newTestApp(t)) // 五引擎服务均为 nil
	ctx := context.Background()
	for _, app := range []string{"opencode", "pi"} {
		args := spec.Args{"app": app, "id": "x"}
		if _, err := h.pluginEnable(ctx, args); specErrorCode(t, err) != spec.ErrBadArgs {
			t.Errorf("enable %s 应 bad_args，得到 %v", app, err)
		}
		if _, err := h.pluginDisable(ctx, args); specErrorCode(t, err) != spec.ErrBadArgs {
			t.Errorf("disable %s 应 bad_args，得到 %v", app, err)
		}
	}
}

// TestAgentCLIPluginInstall_CodexPrecheck codex 非 plugin@marketplace 形态在
// 预检层拒绝（CodexPlugins 为 nil 也不会被触碰——否则将 panic）。
func TestAgentCLIPluginInstall_CodexPrecheck(t *testing.T) {
	h := newAgentCLIHost(newTestApp(t)) // CodexPlugins nil
	ctx := context.Background()
	for _, bad := range []string{"no-at-sign", "a@b@c", "a b@m", "@market", "plugin@"} {
		if _, err := h.pluginInstall(ctx, spec.Args{"app": "codex", "id": bad}); specErrorCode(t, err) != spec.ErrBadArgs {
			t.Errorf("codex install %q 应 bad_args，得到 %v", bad, err)
		}
		if _, err := h.pluginUninstall(ctx, spec.Args{"app": "codex", "id": bad}); specErrorCode(t, err) != spec.ErrBadArgs {
			t.Errorf("codex uninstall %q 应 bad_args，得到 %v", bad, err)
		}
		if _, err := h.pluginEnable(ctx, spec.Args{"app": "codex", "id": bad}); specErrorCode(t, err) != spec.ErrBadArgs {
			t.Errorf("codex enable %q 应 bad_args，得到 %v", bad, err)
		}
	}
}

// TestAgentCLIPluginArgs 校验位置参数缺失场景。
func TestAgentCLIPluginArgs(t *testing.T) {
	h := newAgentCLIHost(newPluginTestApp(t, t.TempDir()))
	ctx := context.Background()
	if _, err := h.pluginEnable(ctx, spec.Args{"app": "claudecode"}); specErrorCode(t, err) != spec.ErrBadArgs {
		t.Errorf("缺 id 应 bad_args，得到 %v", err)
	}
	if _, err := h.pluginEnable(ctx, spec.Args{"id": "x"}); specErrorCode(t, err) != spec.ErrBadArgs {
		t.Errorf("缺 app 应 bad_args，得到 %v", err)
	}
}

// TestAgentPluginCommandOutcome 收口语义：err/Success=false → internal（消息含引擎输出）；
// 成功 → {app, success, output}。
func TestAgentPluginCommandOutcome(t *testing.T) {
	if _, err := agentPluginCommandOutcome("pi", pluginOutcome{success: false, output: "boom"}); specErrorCode(t, err) != spec.ErrInternal {
		t.Errorf("Success=false 应 internal，得到 %v", err)
	} else if !strings.Contains(err.Error(), "boom") {
		t.Errorf("错误消息应含引擎输出: %v", err)
	}
	if _, err := agentPluginCommandOutcome("pi", pluginOutcome{err: errors.New("net down")}); specErrorCode(t, err) != spec.ErrInternal {
		t.Errorf("引擎 err 应 internal，得到 %v", err)
	}
	res, err := agentPluginCommandOutcome("pi", pluginOutcome{success: true, output: " installed ok\n"})
	if err != nil {
		t.Fatalf("成功路径: %v", err)
	}
	m := res.(map[string]any)
	if m["app"] != "pi" || m["success"] != true || m["output"] != "installed ok" {
		t.Errorf("成功返回异常: %+v", m)
	}
	// nil 结果 + nil error 的防御分支。
	if _, err := agentPluginCommandOutcome("pi", pluginOutcome{}); specErrorCode(t, err) != spec.ErrInternal {
		t.Errorf("无结果无错误应 internal（引擎未返回结果），得到 %v", err)
	}
}

// TestAgentCLIPluginInstallCanceledBeforeSideEffect D-06 回归：已取消 ctx 的
// plugin install 必须在触达引擎前返回 not_ready。修复前会调用引擎安装链路
// （本夹具注入恒失败 resolver，返回 internal 引擎错误）。
func TestAgentCLIPluginInstallCanceledBeforeSideEffect(t *testing.T) {
	h := newAgentCLIHost(newPluginTestApp(t, t.TempDir()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.pluginInstall(ctx, spec.Args{"app": "pi", "name": "npm:@foo/bar@1.0.0"})
	if code := specErrorCode(t, err); code != spec.ErrNotReady {
		t.Errorf("已取消 ctx 应在安装副作用前返回 not_ready，得到 %s（%v）", code, err)
	}
}
