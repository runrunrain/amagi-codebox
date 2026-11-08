package main

import (
	"errors"
	"strings"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/piconfig"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/session"
)

// piLoginPresetResolver is the config-facing slice the login launch chain needs.
type piLoginPresetResolver interface {
	ResolvePiLoginLaunch(modelRef, providerRef string) (*config.PiLoginPreset, bool, error)
}

// piLoginLaunch is the resolved login launch contract shared by desktop,
// v1/restart planner and (via env overrides) the external launcher path.
// AgentDir is the canonical standard root the login check actually validated;
// 执行边界钉住它（R2）并在 shell startup 后重锚（R3；WSL 变体见
// piLoginWSLReAnchorSpec——PI_CODING_AGENT_DIR 由解析漏斗指向 Windows canonical
// 根，不再向 distro 同步）。它永不携带或复制凭据本体。
type piLoginLaunch struct {
	Preset   *config.PiLoginPreset
	AgentDir string
}

// resolvePiLoginLaunch never lets a login selection fall through to
// API-provider/default-model resolution; a stale login selection fails closed.
//
// A2 闸门解除（主上裁定 2026-10-16）+ WSL 共享 agent 根（2026-10）：不再向
// piconfig.RequirePiLogin 传 wsl=true 拒绝 WSL——Windows 侧 auth.json 始终是
// 登录源，无论会话落在哪侧都先对它校验（退出登录/预设删除仍 fail closed）。
// WSL 会话经 PI_CODING_AGENT_DIR /p 翻译直接消费同一份 canonical auth.json
// （启动期 distro 凭据同步已退役）；锚定语义的 WSL/非 WSL 分叉在 spec 层
// （见 piLoginWSLReAnchorSpec）而不是这里。
func resolvePiLoginLaunch(resolver piLoginPresetResolver, modelRef, providerRef string) (*piLoginLaunch, error) {
	if resolver == nil {
		return nil, errors.New("Pi login resolver unavailable")
	}
	preset, isLogin, err := resolver.ResolvePiLoginLaunch(modelRef, providerRef)
	if !isLogin {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if preset == nil {
		return nil, errors.New("Pi login preset unavailable")
	}
	agentDir, err := piconfig.RequirePiLogin(preset.Provider, false)
	if err != nil {
		return nil, err
	}
	return &piLoginLaunch{Preset: preset, AgentDir: agentDir}, nil
}

// piLoginCredentialEnvKeys are inherited API credentials that must not reach a
// native-login Pi process: they could make Pi authenticate against a different
// account than the checked OAuth source.
var piLoginCredentialEnvKeys = []string{
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
	"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY",
	"GEMINI_API_KEY", "GOOGLE_API_KEY", "GITHUB_TOKEN", "GH_TOKEN",
	"COPILOT_GITHUB_TOKEN",
}

// piLoginEnvOverrides pins the canonical agent root explicitly instead of
// deleting PI_CODING_AGENT_DIR: user env config may override HOME/USERPROFILE
// for child processes, and Pi falls back to homedir() when the variable is
// unset (R2). Inherited API credentials are dropped (BuildEnv deletes on empty).
// No OAuth token is ever injected — Pi reads its own auth.json under AgentDir.
func piLoginEnvOverrides(agentDir string, extraKeys ...string) map[string]string {
	out := map[string]string{"PI_CODING_AGENT_DIR": agentDir}
	for _, key := range piLoginCredentialEnvKeys {
		out[key] = ""
	}
	for _, key := range extraKeys {
		out[key] = ""
	}
	return out
}

// piLoginWSLEnvOverrides 已并入 piLoginWSLReAnchorSpec 的统一后置处理：env
// 构建期不再分叉（spec 生成前 WSL 与否只能靠镜像判定，与 resolver 实际
// 产出可能不一致；以 spec.BootstrapMode 为唯一权威可避免镜像/真实分叉时
// 环境语义不确定）。

// piLoginPosixShellKeys are the shells whose startup files (-ilc/-lc) run
// before the startup command and may re-export PI_CODING_AGENT_DIR or API
// credentials. Windows shells (pwsh -NoProfile, cmd /K) do not source user
// startup files, so the pinned spec.Env already holds at their boundary.
func piLoginPosixShell(shell *platform.ResolvedShell) bool {
	if shell == nil {
		return false
	}
	switch strings.ToLower(shell.Key) {
	case "bash", "zsh", "sh", "fish", "":
		return true
	default:
		return false
	}
}

// shellSingleQuote POSIX 单引号包裹（'…' + '\” 转义），用于命令前缀中的路径。
func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// piLoginReAnchorSpec 在最终执行边界重新施加登录约束（R3）：
// POSIX shell 的 startup 文件在 CLI 之前运行，可能覆盖已清洗的环境；
// 因此把启动命令本身包上 env 前缀，在 shell 初始化之后重新钉住 canonical
// agent root 并剥离会被登录会话误用的凭据变量。direct/Windows shell 无
// startup 干预，spec.Env 已生效，保持原样。不改普通 API 会话的任何语义。
func piLoginReAnchorSpec(spec platform.ResolvedLaunchSpec, agentDir string, extraUnsetKeys ...string) platform.ResolvedLaunchSpec {
	if spec.BootstrapMode != platform.BootstrapShellInline && spec.BootstrapMode != platform.BootstrapShellAttach {
		return spec
	}
	if !piLoginPosixShell(spec.Shell) {
		return spec
	}
	parts := []string{"env"}
	for _, key := range piLoginCredentialEnvKeys {
		parts = append(parts, "-u", key)
	}
	for _, key := range extraUnsetKeys {
		parts = append(parts, "-u", key)
	}
	parts = append(parts, "PI_CODING_AGENT_DIR="+shellSingleQuote(agentDir))
	prefix := strings.Join(parts, " ") + " "
	spec.StartupCommand = prefix + spec.StartupCommand
	return spec
}

// piLoginWSLReAnchorSpec 是 R3 的 WSL 变体（pi 共享 Windows agent 根，主上
// 裁定 2026-10：WSL 只提供运行环境）：
//   - WSL 内 pi 与 Windows 侧同一 agent 根——PI_CODING_AGENT_DIR 由解析漏斗
//     （resolveEmbeddedLaunchSpec / appLaunchPlanner.resolveLaunch）钉为 Windows
//     canonical 目录，经 WSLENV /p 由 wsl.exe 翻译为 /mnt/<盘符>/…；auth.json
//     就是 Windows 侧那份，不再有 distro 凭据副本与启动期同步（旧 A2 同步退役）。
//   - 本函数只在最终执行边界剥离会被登录会话误用的凭据变量（distro 的 bash
//     startup 文件可能重导出 API key；受管 Claude 订阅时追加身份覆盖键）。
//
// 载荷落在 spec.StartupCommand；pty 层 buildWSLCommandLine 对非空
// StartupCommand 的 WSL spec 以其为权威 bash payload（替代缺省 CLI 拼装）。
func piLoginWSLReAnchorSpec(spec platform.ResolvedLaunchSpec, windowsAgentDir string, extraUnsetKeys ...string) platform.ResolvedLaunchSpec {
	if spec.BootstrapMode != platform.BootstrapWSL {
		return spec
	}
	cli := strings.TrimSpace(spec.CLI.Path)
	if cli == "" {
		return spec // 纯终端 WSL 会话：无 CLI 执行边界，无锚定面。
	}
	_ = windowsAgentDir // 根由解析漏斗经 env 钉定；参数保留以钉住调用契约。
	envTokens := []string{"env"}
	for _, key := range piLoginCredentialEnvKeys {
		envTokens = append(envTokens, "-u", key)
	}
	for _, key := range extraUnsetKeys {
		envTokens = append(envTokens, "-u", key)
	}
	cliTokens := []string{strings.Join(envTokens, " ") + " " + shellSingleQuote(cli)}
	for _, arg := range spec.CLI.Args {
		cliTokens = append(cliTokens, shellSingleQuote(arg))
	}
	spec.StartupCommand = strings.Join(cliTokens, " ") + "; exec bash -li"
	return spec
}

func (p *appLaunchPlanner) buildPiLoginPlan(recipe launchplan.StableRecipe, req launchplan.BuildRequest) (*launchplan.Plan, *launchplan.BuildFailure) {
	fail := func() (*launchplan.Plan, *launchplan.BuildFailure) {
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureLaunchContext, CLIType: req.CLIType}
	}
	resolver, ok := p.config.(piLoginPresetResolver)
	if !ok {
		return fail()
	}
	// A2 闸门解除（主上裁定 2026-10-16）：不再镜像预判 WSL——env 构建与
	// 非 WSL 完全一致（R2 钉定 canonical），spec 生成后以
	// spec.BootstrapMode 为唯一权威分叉重锚，保证镜像/真实判定不一致时
	// 环境语义仍确定。
	login, err := resolvePiLoginLaunch(resolver, recipe.ModelRef, recipe.ProviderRef)
	if err != nil || login == nil {
		return fail()
	}
	preset := login.Preset
	recipe.ProviderRef = config.PiLoginProviderID(preset.Provider)
	recipe.PresetRef = recipe.ModelRef
	// Claude 订阅账号轮换（design §6.3 + 任务 F，F07 提前到 env 构建前）：先取
	// 凭据 admission 描述（blocked → 计划构建失败，入口关闭）。登录会话（含 WSL，
	// 共享 Windows agent 根）消费 canonical auth.json，携带 admission，并在 env
	// 构建与最终执行边界同步剥离身份/认证覆盖变量（与桌面 LaunchPiSession 同一
	// 键集，remote create/restart 不能漏）。
	managedExtra := []string(nil)
	credSpec, credManaged, credErr := p.claudeCredentialAdmissionSpec()
	if credErr != nil {
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureLaunchContext, CLIType: req.CLIType}
	}
	if credManaged {
		managedExtra = managedClaudeIdentityEnvKeys
	}
	env := launcher.BuildEnv(p.envVars.MergeWithSystem(), piLoginEnvOverrides(login.AgentDir, managedExtra...))
	args := []string{"--provider", preset.Provider, "--model", preset.Model}
	if preset.Thinking != "" {
		args = append(args, "--thinking", preset.Thinking)
	}
	spec, err := p.resolveLaunch(platform.ResolveRequest{
		AppType: string(session.AppTypePi), LaunchMode: "embedded", RequestedShellPath: recipe.ShellRef,
		WorkDir: recipe.Workdir, Env: env, CLIArgs: args, PTYCols: 120, PTYRows: 40,
	})
	if err != nil {
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureCapability, CLIType: req.CLIType}
	}
	// WSL 内 pi 共享 Windows agent 根（主上裁定 2026-10）：WSL spec 与原生同样
	// 消费 Windows canonical root 的 auth.json（PI_CODING_AGENT_DIR 由解析漏斗经
	// /p 翻译钉定），不再携带 distro 供给 effect，也不再拒绝受管 Claude 订阅。
	// 登录预设 contextWindow 覆写不在 BuildPlan 写盘（BuildPlan 纯读零副作用）：
	// 统一在预设保存/删除时同步写入 canonical root 的 models.json
	// （ConfigService.SetPiLoginPresetChangeListener + app 装配注入），桌面
	// LaunchPiSession 另有启动兜底；本路径（remote/restart）读已落盘的目标态。
	// Shared execution-boundary constraint: pin the canonical root after any
	// shell startup. No ConfigMutation, secret lookup, managed provider or
	// auth write on this path.
	var effects []launchplan.EffectSpec
	var loginAdmissions []launchplan.SharedAdmissionSpec
	if credManaged {
		loginAdmissions = append(loginAdmissions, credSpec)
	}
	if spec.BootstrapMode == platform.BootstrapWSL {
		spec = piLoginWSLReAnchorSpec(spec, login.AgentDir, managedExtra...)
	} else {
		spec = piLoginReAnchorSpec(spec, login.AgentDir, managedExtra...)
	}
	effects = append(effects, launchplan.EffectSpec{Kind: launchplan.EffectPTYStart, Process: &launchplan.ProcessStartSpec{
		Mode: launchplan.ModeEmbedded, Resolved: spec, RequireRunHandle: true,
	}}, buildBootstrapEffect(spec))
	return p.finalizePlan(recipe, loginAdmissions, effects)
}
