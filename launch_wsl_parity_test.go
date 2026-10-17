package main

// launch_wsl_parity_test.go — C1 纵向切片回归：WSL 启动的 cwd/env 在桌面
// （App.Launch* → resolveEmbeddedLaunchSpec）、remote create（planner.BuildPlan
// Origin=Remote）与 restart（Origin=Restart → 同一 BuildPlan + finalizeWorkdir）
// 三入口一致。
//
//   - cwd（纯目标判据：以解析产出的 spec.BootstrapMode 后置定谳，工作目录层
//     零探测）：WSL 目标下显式请求的目录不得静默换工程——Linux 形态
//     （/home/…、~、~/…，含空格中文）原样保留给 wsl.exe --cd，宿主形态
//     （Windows 盘符/相对路径）校验失败直接拒绝启动；空请求 + Linux 形态
//     默认目录同样保留。非 WSL 目标维持既有宿主校验/回退链（对照用例钉住）。
//   - env：planner.resolveLaunch 与桌面 resolveEmbeddedLaunchSpec 消费同一套
//     envvars 转发规则（自定义键进 WSLENV 面、per-key /p 放行、宿主盘符
//     路径值剥离），remote/restart 不再有更宽的泄漏面。
//
// WSL 目标由 resolver 测试替身（piLoginWSLShellResolver 固定产出
// BootstrapWSL spec）驱动；isLinuxStyleWorkDirFn 注入镜像 platform
// wsl_form_windows.go 语义的假实现（生产默认在非 Windows 宿主恒 false）。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
)

// unsetInheritedProxyEnv 移除宿主进程继承的 AI 代理分流环境（marker +
// 代理键；CodeBox 自身代理启动的 shell 里会带上它们）。这些变量会让
// aiproxy.PinProxyEnvironment 对 WSL spec 触发 NAT 网络门，令与代理无关的
// 计划/启动测试误失败。只做测试隔离，不改变产品代理保护语义。
func unsetInheritedProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AMAGI_CODEBOX_AI_PROXY",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "all_proxy", "no_proxy",
	} {
		if orig, ok := os.LookupEnv(key); ok {
			os.Unsetenv(key)
			t.Cleanup(func() { os.Setenv(key, orig) })
		}
	}
}

// stubLinuxStyleWorkDir 注入 Linux 路径形态判定（镜像 platform
// wsl_form_windows.go 语义：/…、~、~/…；生产默认在非 Windows 宿主恒 false）。
func stubLinuxStyleWorkDir(t *testing.T) {
	t.Helper()
	orig := isLinuxStyleWorkDirFn
	isLinuxStyleWorkDirFn = func(p string) bool {
		w := strings.TrimSpace(p)
		return strings.HasPrefix(w, "/") || w == "~" || strings.HasPrefix(w, "~/")
	}
	t.Cleanup(func() { isLinuxStyleWorkDirFn = orig })
}

// stubWSLEnvGate 注入 env 防线的 WSL 目标门（launchTargetsWSLFn 仅服务 env
// 剥离，生产实现含 distro 探测；本接缝使 macOS CI 可驱动同一条纯逻辑）。
func stubWSLEnvGate(t *testing.T) {
	t.Helper()
	orig := launchTargetsWSLFn
	launchTargetsWSLFn = func(string, string) bool { return true }
	t.Cleanup(func() { launchTargetsWSLFn = orig })
}

// newWSLWorkdirPlanner 构造一个 claude 计划器（anthropic provider + key +
// 已登记桌面默认引用），resolver 固定产出 BootstrapWSL spec。
func newWSLWorkdirPlanner(t *testing.T, homeDir string) (*appLaunchPlanner, *piLoginWSLShellResolver, string) {
	t.Helper()
	dir := t.TempDir()
	cfgSvc := config.NewConfigService(dir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatal(err)
	}
	secSvc := newTestSecretsService(t, dir)
	if err := cfgSvc.SaveProvider("prov", config.Provider{
		Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://api.test.com"},
		DefaultModel: "test-model",
		Presets: map[string]config.Preset{
			"test-preset": {Name: "test-preset", Model: "test-model"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := secSvc.SetAPIKey("prov", "sk-wsl-parity-1234567890"); err != nil {
		t.Fatal(err)
	}
	sett := settings.NewService(dir)
	if err := sett.Load(); err != nil {
		t.Fatal(err)
	}
	defaults, err := launchplan.NewDefaultStore(sett)
	if err != nil {
		t.Fatal(err)
	}
	if err := defaults.RecordDesktopActivation(launchplan.StableRecipe{
		CLIType: contract.CLITypeClaudeCode, Workdir: dir,
		ProviderRef: "prov", PresetRef: "test-preset", ShellRef: "wsl",
	}); err != nil {
		t.Fatal(err)
	}
	resolver := &piLoginWSLShellResolver{}
	return newAppLaunchPlanner(cfgSvc, secSvc, defaults, resolver, fakePlatformCaps(),
		paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), homeDir), resolver, dir
}

// planPTYSpec 提取计划里 PTY effect 携带的 resolved spec。
func planPTYSpec(t *testing.T, plan *launchplan.Plan) (int, platform.ResolvedLaunchSpec) {
	t.Helper()
	for i, eff := range plan.Effects {
		if eff.Kind == launchplan.EffectPTYStart && eff.Process != nil {
			return i, eff.Process.Resolved
		}
	}
	t.Fatal("plan has no PTY effect")
	return -1, platform.ResolvedLaunchSpec{}
}

// TestBuildPlanPreservesWSLLinuxWorkdir：remote create 与 restart 的真实
// 计划入口（adapter → BuildPlan）下，WSL 目标明确请求的 Linux 形态目录不被
// 宿主校验淘汰成默认目录；明确请求的无效宿主形态目录拒绝启动（不静默换
// 工程）；空请求 + Linux 形态默认目录同样保留；非 WSL 目标维持既有回退。
func TestBuildPlanPreservesWSLLinuxWorkdir(t *testing.T) {
	unsetInheritedProxyEnv(t)
	home := isolatedHome(t)
	stubLinuxStyleWorkDir(t)

	base := t.TempDir()
	defaultDir := filepath.Join(base, "默认 目录")
	mustMkdir(t, defaultDir)

	cases := []struct {
		name    string
		workdir string
		want    string
	}{
		{"linux absolute with spaces and cjk", "/home/u/我的 工程", "/home/u/我的 工程"},
		{"bare tilde", "~", "~"},
		{"tilde prefix", "~/proj", "~/proj"},
		{"surrounding spaces trimmed", "  /home/u/x  ", "/home/u/x"},
	}
	for _, originCase := range []struct {
		label  string
		origin launchplan.Origin
	}{{"remote", launchplan.OriginRemote}, {"restart", launchplan.OriginRestart}} {
		for _, tc := range cases {
			t.Run(originCase.label+" / "+tc.name, func(t *testing.T) {
				planner, _, _ := newWSLWorkdirPlanner(t, home)
				plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
					CLIType: contract.CLITypeClaudeCode, Origin: originCase.origin,
					Mode: launchplan.ModeEmbedded, Workdir: tc.workdir,
				})
				if failure != nil {
					t.Fatalf("BuildPlan failed: %#v", failure)
				}
				defer plan.Secrets.Dispose()
				if plan.Recipe.Workdir != tc.want {
					t.Fatalf("Recipe.Workdir = %q, want %q", plan.Recipe.Workdir, tc.want)
				}
				_, spec := planPTYSpec(t, plan)
				if spec.WorkDir != tc.want {
					t.Fatalf("PTY spec.WorkDir = %q, want %q", spec.WorkDir, tc.want)
				}
			})
		}
	}

	t.Run("explicit invalid windows form rejects launch", func(t *testing.T) {
		// 任务契约：WSL 明确请求的目录不得静默换工程——无效宿主形态直接
		// FailureWorkdir，不回退默认目录。
		planner, _, _ := newWSLWorkdirPlanner(t, home)
		_, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
			CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote,
			Mode: launchplan.ModeEmbedded, Workdir: `C:\missing\目录`,
		})
		if failure == nil || failure.Kind != launchplan.FailureWorkdir {
			t.Fatalf("expected FailureWorkdir, got %#v", failure)
		}
	})

	t.Run("windows form still host-validated when valid", func(t *testing.T) {
		planner, _, _ := newWSLWorkdirPlanner(t, home)
		// Windows 盘符形态（含空格中文）在 WSL 目标下仍走宿主校验，存在即保留
		// Clean+Abs 形态（macOS 宿主上以字面量目录名模拟盘符路径）。
		winProj := filepath.Join(base, `C:\工程 空格`)
		mustMkdir(t, winProj)
		plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
			CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote,
			Mode: launchplan.ModeEmbedded, Workdir: winProj,
		})
		if failure != nil {
			t.Fatalf("BuildPlan failed: %#v", failure)
		}
		defer plan.Secrets.Dispose()
		if plan.Recipe.Workdir != winProj {
			t.Fatalf("existing Windows workdir must survive Clean+Abs: %q", plan.Recipe.Workdir)
		}
	})

	t.Run("empty request preserves linux-form default dir", func(t *testing.T) {
		// 用户配置默认目录 ~/默认 工程：WSL 目标下空请求不得被宿主校验吞掉。
		planner, _, _ := newWSLWorkdirPlanner(t, home)
		if err := planner.paths.Load(); err != nil {
			t.Fatalf("load paths: %v", err)
		}
		if err := planner.paths.SetDefaultPath("~/默认 工程"); err != nil {
			t.Fatalf("set default path: %v", err)
		}
		plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
			CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote,
			Mode: launchplan.ModeEmbedded,
		})
		if failure != nil {
			t.Fatalf("BuildPlan failed: %#v", failure)
		}
		defer plan.Secrets.Dispose()
		if plan.Recipe.Workdir != "~/默认 工程" {
			t.Fatalf("linux-form default must be preserved under WSL target, got %q", plan.Recipe.Workdir)
		}
	})

	t.Run("non-wsl target keeps host validation fallback", func(t *testing.T) {
		// 非 WSL resolver：Linux 形态路径被宿主校验淘汰 → 默认目录；Linux 形态
		// 默认目录同样不保留（原生 macOS/Windows 非 WSL 语义，钉住不回归）。
		dir := t.TempDir()
		sett := settings.NewService(dir)
		if err := sett.Load(); err != nil {
			t.Fatal(err)
		}
		defaults, err := launchplan.NewDefaultStore(sett)
		if err != nil {
			t.Fatal(err)
		}
		cfgSvc := config.NewConfigService(dir)
		if err := cfgSvc.Load(); err != nil {
			t.Fatal(err)
		}
		secSvc := newTestSecretsService(t, dir)
		if err := cfgSvc.SaveProvider("prov", config.Provider{
			Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://api.test.com"},
			DefaultModel: "test-model",
			Presets:      map[string]config.Preset{"test-preset": {Name: "test-preset", Model: "test-model"}},
		}); err != nil {
			t.Fatal(err)
		}
		if err := secSvc.SetAPIKey("prov", "sk-wsl-parity-1234567890"); err != nil {
			t.Fatal(err)
		}
		if err := defaults.RecordDesktopActivation(launchplan.StableRecipe{
			CLIType: contract.CLITypeClaudeCode, Workdir: dir,
			ProviderRef: "prov", PresetRef: "test-preset", ShellRef: "/bin/sh",
		}); err != nil {
			t.Fatal(err)
		}
		pathsSvc := paths.NewPathsService(dir)
		if err := pathsSvc.Load(); err != nil {
			t.Fatal(err)
		}
		if err := pathsSvc.SetDefaultPath(defaultDir); err != nil {
			t.Fatal(err)
		}
		planner := newAppLaunchPlanner(cfgSvc, secSvc, defaults, fakeCLIResolver{}, fakePlatformCaps(),
			pathsSvc, envvars.NewEnvVarsService(dir), home)

		plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
			CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote,
			Mode: launchplan.ModeEmbedded, Workdir: "/home/u/proj",
		})
		if failure != nil {
			t.Fatalf("BuildPlan failed: %#v", failure)
		}
		defer plan.Secrets.Dispose()
		if plan.Recipe.Workdir != filepath.Clean(defaultDir) {
			t.Fatalf("non-WSL target must keep host fallback to default dir, got %q", plan.Recipe.Workdir)
		}
	})
}

// TestExecutorConsumesPreservedWSLWorkdir：remote create 的执行段
// （Executor.Prepare → PTY effect）原样消费计划里保留的 Linux 形态目录，
// 不在执行段再次引入宿主校验或改写。
func TestExecutorConsumesPreservedWSLWorkdir(t *testing.T) {
	unsetInheritedProxyEnv(t)
	home := isolatedHome(t)
	stubLinuxStyleWorkDir(t)
	planner, _, _ := newWSLWorkdirPlanner(t, home)
	const want = "/home/u/我的 工程"
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote,
		Mode: launchplan.ModeEmbedded, Workdir: want,
	})
	if failure != nil {
		t.Fatalf("BuildPlan failed: %#v", failure)
	}
	defer plan.Secrets.Dispose()

	exec := newAppLaunchExecutor(launchExecutorDeps{})
	prepared, err := exec.Prepare(context.Background(), plan, launchplan.ExecutionBinding{
		SessionID: "wsl-wd-parity", RunEpoch: 1, RunHandle: struct{}{},
	})
	if err != nil {
		t.Fatalf("executor prepare: %v", err)
	}
	defer prepared.Abort(context.Background())
	ptyIdx, _ := planPTYSpec(t, plan)
	ptyEffect, ok := prepared.Effect(ptyIdx).(*ptyStartEffect)
	if !ok {
		t.Fatalf("PTY effect is %T, want *ptyStartEffect", prepared.Effect(ptyIdx))
	}
	if ptyEffect.spec.WorkDir != want {
		t.Fatalf("executor PTY spec.WorkDir = %q, want %q", ptyEffect.spec.WorkDir, want)
	}
	if ptyEffect.spec.BootstrapMode != platform.BootstrapWSL {
		t.Fatalf("bootstrap mode mutated: %q", ptyEffect.spec.BootstrapMode)
	}
}

// TestDesktopResolveEmbeddedLaunchSpecWSLWorkdir：桌面统一漏斗（五 CLI
// 内嵌启动共用的 resolveEmbeddedLaunchSpec）以 spec.BootstrapMode 定谳：
// Linux 形态请求/默认目录保留、无效宿主形态拒绝、非 WSL 目标维持宿主链值。
func TestDesktopResolveEmbeddedLaunchSpecWSLWorkdir(t *testing.T) {
	unsetInheritedProxyEnv(t)
	home := isolatedHome(t)
	stubLinuxStyleWorkDir(t)
	app := newTestApp(t)
	app.Capabilities = fakePlatformCaps()
	wslResolver := &piLoginWSLShellResolver{}
	app.CLIResolver = wslResolver
	hostResolved := home // 入口宿主链产物的替身（回退目标）

	spec, err := app.resolveEmbeddedLaunchSpec(session.AppTypePi, string(session.ModeEmbedded), "wsl", hostResolved, "/home/u/我的 工程", nil, nil)
	if err != nil {
		t.Fatalf("resolveEmbeddedLaunchSpec: %v", err)
	}
	if spec.WorkDir != "/home/u/我的 工程" {
		t.Fatalf("WSL target must preserve requested Linux workdir, got %q", spec.WorkDir)
	}

	if _, err := app.resolveEmbeddedLaunchSpec(session.AppTypePi, string(session.ModeEmbedded), "wsl", hostResolved, `C:\missing\目录`, nil, nil); err == nil || !strings.Contains(err.Error(), "拒绝启动") {
		t.Fatalf("explicit invalid windows form must reject the launch, got %v", err)
	}

	if err := app.Paths.Load(); err != nil {
		t.Fatalf("load paths: %v", err)
	}
	if err := app.Paths.SetDefaultPath("~/默认 工程"); err != nil {
		t.Fatalf("set default path: %v", err)
	}
	spec, err = app.resolveEmbeddedLaunchSpec(session.AppTypePi, string(session.ModeEmbedded), "wsl", hostResolved, "", nil, nil)
	if err != nil {
		t.Fatalf("resolveEmbeddedLaunchSpec: %v", err)
	}
	if spec.WorkDir != "~/默认 工程" {
		t.Fatalf("linux-form default must be preserved under WSL target, got %q", spec.WorkDir)
	}

	// 非 WSL 目标：返回入口宿主链产物，原生回退语义不变。
	app.CLIResolver = fakeCLIResolver{}
	spec, err = app.resolveEmbeddedLaunchSpec(session.AppTypePi, string(session.ModeEmbedded), "pwsh", hostResolved, "/home/u/proj", nil, nil)
	if err != nil {
		t.Fatalf("resolveEmbeddedLaunchSpec (native): %v", err)
	}
	if spec.WorkDir != hostResolved {
		t.Fatalf("native target must keep host-resolved workdir, got %q", spec.WorkDir)
	}
}

// TestDesktopLaunchPreservesWSLLinuxWorkdir：桌面真实入口（App.LaunchPiSession）
// 在 WSL 目标下不被工作目录回退拦截——Linux 形态请求一路到达 PTY 层（错误
// 为 PTY 启动失败而非目录回退/拒绝），无效宿主形态请求直接拒绝；非 WSL
// resolver 对照维持既有回退（resolver 收到宿主链回退值，仍到达 PTY）。
func TestDesktopLaunchPreservesWSLLinuxWorkdir(t *testing.T) {
	unsetInheritedProxyEnv(t)
	stubLinuxStyleWorkDir(t)

	launchWith := func(t *testing.T, wslTarget bool, workdir string) (string, string, string) {
		t.Helper()
		home := isolatedHome(t)
		app := newTestApp(t)
		app.ctx = t.Context()
		app.Capabilities = fakePlatformCaps()
		wslRecorder := &piLoginWSLShellResolver{}
		nativeRecorder := &recordingNativeResolver{}
		if wslTarget {
			app.CLIResolver = wslRecorder
		} else {
			app.CLIResolver = nativeRecorder
		}
		if err := app.Config.SaveProvider("prov", config.Provider{
			OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example/v1"},
			DefaultModel: "m1",
		}); err != nil {
			t.Fatal(err)
		}
		if err := app.Secrets.SetAPIKey("prov", "sk-wsl-wd-1234567890"); err != nil {
			t.Fatal(err)
		}
		_, err := app.LaunchPiSession("m1", "prov", "embedded", workdir, "")
		if err == nil {
			t.Fatal("expected PTY-layer failure in test environment, got success")
		}
		requestWorkDir := wslRecorder.request.WorkDir
		if !wslTarget {
			requestWorkDir = nativeRecorder.request.WorkDir
		}
		return err.Error(), requestWorkDir, home
	}

	t.Run("wsl target preserves linux workdir", func(t *testing.T) {
		errText, _, _ := launchWith(t, true, "/home/u/我的 工程")
		if !strings.Contains(errText, "start pi pty") {
			t.Fatalf("expected launch to reach the PTY layer (no workdir rejection/fallback), got: %s", errText)
		}
	})

	t.Run("wsl target rejects invalid windows workdir", func(t *testing.T) {
		errText, _, _ := launchWith(t, true, `C:\missing\目录`)
		if !strings.Contains(errText, "拒绝启动") {
			t.Fatalf("expected invalid explicit windows workdir to reject the launch, got: %s", errText)
		}
	})

	t.Run("non-wsl target keeps host fallback", func(t *testing.T) {
		// 非 WSL resolver：Linux 形态路径被宿主链淘汰 → 回退 Home（既有行为，
		// 不随 WSL 修复改变）。
		errText, gotWorkDir, home := launchWith(t, false, "/home/u/proj")
		if !strings.Contains(errText, "start pi pty") {
			t.Fatalf("expected launch to reach the PTY layer, got: %s", errText)
		}
		if gotWorkDir != home {
			t.Fatalf("non-WSL target must fall back to home %q, got %q", home, gotWorkDir)
		}
	})
}

// TestPlannerResolveLaunchEnvForwardParity：remote/restart 计划链与桌面
// resolveEmbeddedLaunchSpec 消费同一套 env 转发规则——自定义标量保留、
// allowPathValue 键豁免剥离（转发侧获得 /p 翻译标志）、未放行的宿主盘符
// 路径值剥离；两入口对同一输入产出完全相同的进程环境。
func TestPlannerResolveLaunchEnvForwardParity(t *testing.T) {
	unsetInheritedProxyEnv(t)
	isolatedHome(t)
	stubLinuxStyleWorkDir(t)
	stubWSLEnvGate(t)
	dir := t.TempDir()
	envSvc := envvars.NewEnvVarsService(dir)
	if err := envSvc.Load(); err != nil {
		t.Fatal(err)
	}
	if err := envSvc.BatchSet([]envvars.EnvVar{
		{Key: "TAVILY_API_KEY", Value: "tk-123"},
		{Key: "UE_ROOT", Value: `C:\UE\Engine`, AllowPathValue: true},
		{Key: "MY_TOOL_HOME", Value: `D:\tools\home`},
	}); err != nil {
		t.Fatal(err)
	}
	baseEnv := []string{
		"PATH=/usr/bin:/bin",
		"TAVILY_API_KEY=tk-123",
		"UE_ROOT=" + `C:\UE\Engine`,
		"MY_TOOL_HOME=" + `D:\tools\home`,
		"PI_OFFLINE=1",
		"PI_SESSION_FILE=" + `C:\Users\u\pi-session.json`,
		"OPENCODE_CONFIG=/home/u/.config/opencode/amagi-codebox.json",
		"WSLENV=PATH/u",
	}

	// remote/restart 链：planner.resolveLaunch（五个 buildXPlan 的公共入口）。
	resolver := &piLoginWSLShellResolver{}
	var observed []string
	p := &appLaunchPlanner{resolver: resolver, envVars: envSvc, envObserver: func(level, message, detail string) {
		observed = append(observed, level+"|"+message+"|"+detail)
	}}
	spec, err := p.resolveLaunch(platform.ResolveRequest{
		AppType: string(session.AppTypePi), LaunchMode: string(session.ModeEmbedded),
		RequestedShellPath: "wsl", Env: append([]string(nil), baseEnv...),
	})
	if err != nil {
		t.Fatalf("planner resolveLaunch: %v", err)
	}
	// 转发面接线：自定义键 + per-key 放行清单原样传给解析器。
	if got := strings.Join(resolver.request.EnvForwardKeys, ","); got != "TAVILY_API_KEY,UE_ROOT,MY_TOOL_HOME" {
		t.Fatalf("EnvForwardKeys = %q", got)
	}
	if got := strings.Join(resolver.request.EnvPathFlagKeys, ","); got != "UE_ROOT" {
		t.Fatalf("EnvPathFlagKeys = %q", got)
	}
	kept := spec.Env.Variables
	if readEnvValueForTest(kept, "MY_TOOL_HOME") != "" {
		t.Fatalf("unallowed drive-path value leaked: MY_TOOL_HOME=%q", readEnvValueForTest(kept, "MY_TOOL_HOME"))
	}
	if readEnvValueForTest(kept, "PI_SESSION_FILE") != "" {
		t.Fatalf("forwarded PI_* drive-path value leaked: PI_SESSION_FILE=%q", readEnvValueForTest(kept, "PI_SESSION_FILE"))
	}
	if got := readEnvValueForTest(kept, "UE_ROOT"); got != `C:\UE\Engine` {
		t.Fatalf("allowed path key must be exempt from strip, got %q", got)
	}
	if got := readEnvValueForTest(kept, "TAVILY_API_KEY"); got != "tk-123" {
		t.Fatalf("custom scalar must be kept, got %q", got)
	}
	if got := readEnvValueForTest(kept, "OPENCODE_CONFIG"); got != "/home/u/.config/opencode/amagi-codebox.json" {
		t.Fatalf("linux path value must be kept, got %q", got)
	}
	if got := readEnvValueForTest(kept, "PI_OFFLINE"); got != "1" {
		t.Fatalf("scalar PI_* must be kept, got %q", got)
	}
	// 可观测性：与桌面同文案同顺序（INFO 放行在前，WARN 剥离在后）。
	if len(observed) != 2 ||
		!strings.Contains(observed[0], "INFO") || !strings.Contains(observed[0], "UE_ROOT") ||
		!strings.Contains(observed[1], "WARN") || !strings.Contains(observed[1], "MY_TOOL_HOME,PI_SESSION_FILE") {
		t.Fatalf("env observer records = %v", observed)
	}

	// 桌面链：resolveEmbeddedLaunchSpec（同一 env 门接缝），同一输入必须产出
	// 完全相同的进程环境（键序一致）。
	app := newTestApp(t)
	app.Capabilities = fakePlatformCaps()
	app.EnvVars = envSvc
	desktopResolver := &piLoginWSLShellResolver{}
	app.CLIResolver = desktopResolver
	desktopSpec, err := app.resolveEmbeddedLaunchSpec(session.AppTypePi, string(session.ModeEmbedded), "wsl", "", "", append([]string(nil), baseEnv...), nil)
	if err != nil {
		t.Fatalf("desktop resolveEmbeddedLaunchSpec: %v", err)
	}
	if strings.Join(desktopSpec.Env.Variables, "\n") != strings.Join(kept, "\n") {
		t.Fatalf("desktop/planner env diverge:\ndesktop=%v\nplanner=%v", desktopSpec.Env.Variables, kept)
	}
}

// TestPlannerResolveLaunchNoStripWithoutWSLTarget：非 WSL 目标（显式 pwsh
// shell）不触发剥离，也不接转发面（原生语义对照；生产默认接缝在非 Windows
// 宿主恒 false，这里直接用默认接缝验证）。
func TestPlannerResolveLaunchNoStripWithoutWSLTarget(t *testing.T) {
	dir := t.TempDir()
	envSvc := envvars.NewEnvVarsService(dir)
	if err := envSvc.Load(); err != nil {
		t.Fatal(err)
	}
	if err := envSvc.BatchSet([]envvars.EnvVar{{Key: "MY_TOOL_HOME", Value: `D:\tools\home`}}); err != nil {
		t.Fatal(err)
	}
	resolver := &piLoginWSLShellResolver{}
	p := &appLaunchPlanner{resolver: resolver, envVars: envSvc}
	spec, err := p.resolveLaunch(platform.ResolveRequest{
		AppType: string(session.AppTypePi), LaunchMode: string(session.ModeEmbedded),
		RequestedShellPath: "pwsh", Env: []string{"MY_TOOL_HOME=" + `D:\tools\home`},
	})
	if err != nil {
		t.Fatalf("resolveLaunch: %v", err)
	}
	if got := readEnvValueForTest(spec.Env.Variables, "MY_TOOL_HOME"); got != `D:\tools\home` {
		t.Fatalf("non-WSL target must not strip, got %q", got)
	}
	if len(resolver.request.EnvForwardKeys) != 1 || resolver.request.EnvForwardKeys[0] != "MY_TOOL_HOME" {
		t.Fatalf("EnvForwardKeys = %v (wiring must stay on for resolver-side WSLENV build)", resolver.request.EnvForwardKeys)
	}
}

func readEnvValueForTest(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// recordingNativeResolver 记录解析请求并产出原生 attach spec（非 WSL 目标
// 对照用）。
type recordingNativeResolver struct{ request platform.ResolveRequest }

func (r *recordingNativeResolver) Resolve(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	r.request = req
	return fakeCLIResolver{}.Resolve(req)
}

func (r *recordingNativeResolver) ResolveExecutable(command string, args []string, env []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return fakeCLIResolver{}.ResolveExecutable(command, args, env)
}
