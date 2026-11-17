package main

// pi_login_wsl_test.go — WSL 内 pi 共享 Windows agent 根（1.3.126）的回归：
// WSL spec 下的登录计划放行（不携带 distro 供给 effect、载荷只剩 env -u
// 重锚）、PI_CODING_AGENT_DIR 经解析漏斗钉定为 Windows canonical 根。Pi 的
// A2 供给闸门/故障矩阵已随共享根退役。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/settings"
)

// piLoginWSLShellResolver 固定产出 BootstrapWSL spec（resolver 的 WSL 分支
// 形态：CLI 裸名 + Args、Shell key=wsl、StartupCommand 空），其余复用
// fakeCLIResolver。跨平台可用——不依赖真实 WSL。
type piLoginWSLShellResolver struct{ request platform.ResolveRequest }

func (r *piLoginWSLShellResolver) Resolve(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	r.request = req
	spec, err := fakeCLIResolver{}.Resolve(req)
	if err != nil {
		return spec, err
	}
	spec.Shell = &platform.ResolvedShell{Key: "wsl", Path: "wsl.exe"}
	spec.BootstrapMode = platform.BootstrapWSL
	spec.CLI.Path = "pi"
	spec.StartupCommand = ""
	return spec, nil
}

func (*piLoginWSLShellResolver) ResolveExecutable(cmd string, args []string, env []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return fakeCLIResolver{}.ResolveExecutable(cmd, args, env)
}

// TestPiLoginPlannerWSLGateLifted：WSL spec 的登录计划共享 Windows agent 根——
// 计划不再携带 EffectWSLConfigSupply（无 distro 凭据副本/同步），
// PI_CODING_AGENT_DIR 钉为 Windows canonical 目录并登记为路径放行键（躲过剥离 +
// WSLENV /p 翻译），StartupCommand 只剩 env -u 凭据键前缀 + 单引号 CLI +
// exec bash -li（凭据字节永不进入命令行）。
func TestPiLoginPlannerWSLGateLifted(t *testing.T) {
	unsetInheritedProxyEnv(t) // 宿主代理 marker 会经 MergeWithSystem 触发 NAT 门，与本用例无关
	stubWSLEnvGate(t)
	home := isolatedHome(t)
	dir := t.TempDir()
	cfg := config.NewConfigService(dir)
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	preset, key, _ := piLoginFixture(t, cfg)
	sett := settings.NewService(dir)
	if err := sett.Load(); err != nil {
		t.Fatal(err)
	}
	defaults, err := launchplan.NewDefaultStore(sett)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &piLoginWSLShellResolver{}
	planner := newAppLaunchPlanner(cfg, newTestSecretsService(t, dir), defaults, resolver, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
		StableRefs: &launchplan.StableLaunchRefs{ProviderRef: config.PiLoginProviderID(preset.Provider), ModelRef: key},
	})
	if failure != nil {
		t.Fatalf("WSL login plan failed (gate not lifted?): %#v", failure)
	}
	defer plan.Secrets.Dispose()
	if plan.Recipe.ProviderRef != config.PiLoginProviderID(preset.Provider) || plan.Recipe.ModelRef != key {
		t.Fatalf("WSL login recipe lost: %+v", plan.Recipe)
	}

	// 共享 Windows agent 根：计划不携带任何 distro 供给 effect。
	ptyIdx := -1
	for i, eff := range plan.Effects {
		if eff.Kind == launchplan.EffectWSLConfigSupply {
			t.Fatalf("WSL pi login plan must not carry distro supply (shared Windows root): effect %d", i)
		}
		if eff.Kind == launchplan.EffectPTYStart {
			ptyIdx = i
		}
	}
	if ptyIdx < 0 {
		t.Fatal("plan missing PTY effect")
	}

	spec := plan.Effects[ptyIdx].Process.Resolved
	if spec.BootstrapMode != platform.BootstrapWSL {
		t.Fatalf("bootstrap mode mutated: %q", spec.BootstrapMode)
	}
	// 共享根：PI_CODING_AGENT_DIR 钉为 Windows canonical 目录，且登记为路径放行键。
	envMap := map[string]string{}
	for _, kv := range spec.Env.Variables {
		k, v, _ := strings.Cut(kv, "=")
		envMap[k] = v
	}
	if got, want := envMap["PI_CODING_AGENT_DIR"], filepath.Join(home, ".pi", "agent"); got != want {
		t.Fatalf("PI_CODING_AGENT_DIR = %q, want Windows canonical root %q", got, want)
	}
	pathFlagged := false
	for _, k := range resolver.request.EnvPathFlagKeys {
		if strings.EqualFold(k, "PI_CODING_AGENT_DIR") {
			pathFlagged = true
		}
	}
	// 路径放行标志只对 Windows 盘符值生效（macOS/Linux CI 的 home 是 POSIX 路径，
	// 值原样透传不加标志；盘符形态见 launcher.TestApplyWSLSharedPiAgentRoot）。
	if isWin := strings.Contains(envMap["PI_CODING_AGENT_DIR"], `:\`); isWin && !pathFlagged {
		t.Fatalf("PI_CODING_AGENT_DIR must be path-flagged for WSLENV /p translation: %v", resolver.request.EnvPathFlagKeys)
	}
	// 载荷：只剩 env -u 凭据键前缀 → 单引号 CLI 与参数 → exec bash -li；
	// 不得含 cmp/cp/stat/chmod 同步逻辑与任何 auth.json 路径字节。
	cmd := spec.StartupCommand
	for _, banned := range []string{"cmp -s", "cp --", "stat -c %Y", "chmod 600", "auth.json", "mkdir -p", "PI_CODING_AGENT_DIR"} {
		if strings.Contains(cmd, banned) {
			t.Fatalf("unexpected machinery in payload (%q): %s", banned, cmd)
		}
	}
	envPrefix := "env"
	for _, k := range piLoginCredentialEnvKeys {
		envPrefix += " -u " + k
	}
	if !strings.Contains(cmd, envPrefix+" 'pi' '--provider' 'openai-codex' '--model' 'native-model' '--thinking' 'max'") {
		t.Fatalf("payload missing re-anchor prefix + quoted cli: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "; exec bash -li") {
		t.Fatalf("payload missing exec bash tail: %s", cmd)
	}
}

// TestPiLoginWSLPayloadSkipsAgentDirPin：非 WSL spec 的载荷语义不变
// （不携带 env -u PI_CODING_AGENT_DIR 重锚）；纯终端 WSL spec 无载荷。
func TestPiLoginWSLPayloadSkipsAgentDirPin(t *testing.T) {
	home := isolatedHome(t)
	canonical := filepath.Join(home, ".pi", "agent")
	spec := piLoginWSLReAnchorSpec(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapShellAttach,
		Shell:         &platform.ResolvedShell{Key: "bash", Path: "/bin/bash"},
		CLI:           platform.ResolvedCLI{Path: "pi", Args: []string{"--provider", "x"}},
	}, canonical)
	if strings.Contains(spec.StartupCommand, "env -u PI_CODING_AGENT_DIR") {
		t.Fatalf("non-WSL spec got WSL payload: %s", spec.StartupCommand)
	}
	// WSL spec 但 CLI 为空（纯终端）：无锚定面，载荷不变。
	terminal := piLoginWSLReAnchorSpec(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		Env:           platform.ResolvedEnv{Variables: []string{"PI_CODING_AGENT_DIR=" + canonical, "WSLENV=PI_CODING_AGENT_DIR/u:PATH"}},
	}, canonical)
	if terminal.StartupCommand != "" {
		t.Fatalf("pure terminal WSL spec must not gain a payload: %q", terminal.StartupCommand)
	}
	// 共享 Windows agent 根：PI_CODING_AGENT_DIR 与 WSLENV 条目原样保留（不再剥离）。
	if !hasEnvEntryForTest(terminal.Env.Variables, "PI_CODING_AGENT_DIR") {
		t.Fatalf("shared agent root must be kept for WSL spec: %v", terminal.Env.Variables)
	}
	if wslenv := envValue(terminal.Env.Variables, "WSLENV"); wslenv != "PI_CODING_AGENT_DIR/u:PATH" {
		t.Fatalf("WSLENV must be preserved: %q", wslenv)
	}
}

func hasEnvEntryForTest(env []string, key string) bool {
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// TestPiLoginDesktopWSLGateSync：WSL 内 pi 共享 Windows agent 根——桌面登录
// 启动不再有 distro 凭据/托管配置供给闸门，直接放行到 PTY 层（control 未接线
// 自然失败，不触达 PTY spawn）。
func TestPiLoginDesktopWSLGateSync(t *testing.T) {
	unsetInheritedProxyEnv(t) // 桌面链 env 合入宿主环境；隔离代理 marker，保留产品门语义
	home := isolatedHome(t)
	app := newTestApp(t)
	app.ctx = t.Context()
	app.Capabilities = fakePlatformCaps()
	_, key, _ := piLoginFixture(t, app.Config)
	app.CLIResolver = &piLoginWSLShellResolver{}
	_, err := app.LaunchPiSession(key, config.PiLoginProviderID("openai-codex"), "embedded", home, "")
	if err == nil || !strings.Contains(err.Error(), "start pi pty") {
		t.Fatalf("expected launch to pass through to PTY layer, got %v", err)
	}
}
