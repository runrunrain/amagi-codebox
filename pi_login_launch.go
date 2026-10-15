package main

import (
	"errors"
	"path/filepath"
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
// 非 WSL 执行边界钉住它（R2）并在 POSIX shell startup 后重锚（R3），WSL 执行
// 边界则以它为同步源、锚定 distro 内 $HOME/.pi/agent（A2 解除后的分叉，
// 见 piLoginWSLReAnchorSpec）。它永不携带或复制凭据本体。
type piLoginLaunch struct {
	Preset   *config.PiLoginPreset
	AgentDir string
}

// resolvePiLoginLaunch never lets a login selection fall through to
// API-provider/default-model resolution; a stale login selection fails closed.
//
// A2 闸门解除（主上裁定 2026-10-16）：不再向 piconfig.RequirePiLogin 传
// wsl=true 拒绝 WSL——Windows 侧 auth.json 始终是登录源与同步源，无论会话
// 落在哪侧都先对它校验（退出登录/预设删除仍 fail closed）。WSL 会话凭据
// 的可达性由启动路径的惰性同步（launcher.SyncWSLPiLoginAuth + payload 内
// 同步片段）保障；锚定语义的 WSL/非 WSL 分叉在 spec 层（见
// piLoginWSLReAnchorSpec）而不是这里。
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
func piLoginEnvOverrides(agentDir string) map[string]string {
	out := map[string]string{"PI_CODING_AGENT_DIR": agentDir}
	for _, key := range piLoginCredentialEnvKeys {
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
func piLoginReAnchorSpec(spec platform.ResolvedLaunchSpec, agentDir string) platform.ResolvedLaunchSpec {
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
	parts = append(parts, "PI_CODING_AGENT_DIR="+shellSingleQuote(agentDir))
	prefix := strings.Join(parts, " ") + " "
	spec.StartupCommand = prefix + spec.StartupCommand
	return spec
}

// syncWSLPiLoginAuthFn 是桌面启动闸门调用的惰性同步接缝（单测替换；生产
// 指向 launcher.SyncWSLPiLoginAuth）。
var syncWSLPiLoginAuthFn = launcher.SyncWSLPiLoginAuth

// piLoginWSLReAnchorSpec 是 R3 的 WSL 变体，也是 R2 锚定语义的 WSL/非 WSL
// 分叉点（A2 主上裁定 2026-10-16）：
//   - 非 WSL（piLoginReAnchorSpec）：PI_CODING_AGENT_DIR 钉 CodeBox 进程
//     Home 的 canonical 目录，shell startup 后重钉同值（R2/R3 原文不变）。
//   - WSL（本函数）：不钉 Windows 路径，载荷以 env -u PI_CODING_AGENT_DIR
//     收尾——pi 在 distro 内读取自己的 $HOME/.pi/agent，凭据副本由启动时
//     同步保障。payload 同时内嵌惰性同步片段：在 distro 内以 DrvFS 视图
//     双向比对两侧 auth.json，新者为准（M-1：Windows 新或 distro 缺失→复制
//     进 distro + chmod 600；distro 新或并列→反向回写 Windows 侧，保住
//     WSL 内轮转出的新凭据）。该片段是 remote/v1 重启链路的同步通道
//     ——BuildPlan 契约要求纯读零副作用（launch_planner.go「no process
//     spawn」），Go 侧同步不能进 BuildPlan，载荷片段在执行边界生效，
//     不违反契约。
//
// 载荷落在 spec.StartupCommand；pty 层 buildWSLCommandLine 对非空
// StartupCommand 的 WSL spec 以其为权威 bash payload（替代缺省 CLI 拼装）。
func piLoginWSLReAnchorSpec(spec platform.ResolvedLaunchSpec, windowsAgentDir string) platform.ResolvedLaunchSpec {
	if spec.BootstrapMode != platform.BootstrapWSL {
		return spec
	}
	spec.Env.Variables = piLoginStripAgentDirForWSL(spec.Env.Variables)
	cli := strings.TrimSpace(spec.CLI.Path)
	if cli == "" {
		return spec // 纯终端 WSL 会话：无 CLI 执行边界，无锚定面。
	}
	parts := make([]string, 0, 3)
	if src := launcher.WSLDrvFSMountPath(filepath.Join(windowsAgentDir, "auth.json")); src != "" {
		parts = append(parts, piLoginWSLAuthSyncSnippet(src))
	}
	envTokens := []string{"env"}
	for _, key := range piLoginCredentialEnvKeys {
		envTokens = append(envTokens, "-u", key)
	}
	envTokens = append(envTokens, "-u", "PI_CODING_AGENT_DIR")
	cliTokens := []string{strings.Join(envTokens, " ") + " " + shellSingleQuote(cli)}
	for _, arg := range spec.CLI.Args {
		cliTokens = append(cliTokens, shellSingleQuote(arg))
	}
	parts = append(parts, strings.Join(cliTokens, " "))
	parts = append(parts, "exec bash -li")
	spec.StartupCommand = strings.Join(parts, "; ")
	return spec
}

// piLoginWSLAuthSyncSnippet 组装 WSL 登录载荷内的惰性同步片段（M-1 后
// 双向）：distro 内以 DrvFS 视图（src 为 /mnt/<drive>/... 形态的 Windows 侧
// auth.json）比对 $HOME/.pi/agent/auth.json——
//   - 一致：零拷贝；
//   - Windows 新（stat 秒差 >2，含 distro 侧缺失的首次播种——q2-1：与 Go 侧
//     DistroTieKept 同带 2s 容差，0<Δ≤2 的歧义带内不前向，避免两通道反向
//     分叉摧毁 distro 轮转凭据）：复制进 distro 并 chmod 600；
//   - distro 新或秒差≤2（含精确并列——与 Go 侧「并列取 distro」同向）：
//     反向 cp 回 Windows 侧（DrvFS 写，覆盖已有文件保留其 ACL），保 distro
//     侧轮转出的新凭据不被旧副本摧毁；
//   - src 不存在（启动后退出的极端时序）：不动任何一侧（不复活已退出
//     登录的凭据）。
//
// 任一复制失败不阻断 CLI 启动（echo 提示后继续用 distro 既有副本）；
// 桌面会话（闸门处）已先经 Go 侧双向同步，此片段通常 cmp 即中、零拷贝。
func piLoginWSLAuthSyncSnippet(src string) string {
	q := shellSingleQuote(src)
	dst := `"$HOME/.pi/agent/auth.json"`
	return `mkdir -p -- "$HOME/.pi/agent"; if [ -e ` + q + ` ]; then if ! cmp -s -- ` + q + ` ` + dst + ` 2>/dev/null; then if [ ! -e ` + dst + ` ] || [ $(( $(stat -c %Y -- ` + q + `) - $(stat -c %Y -- ` + dst + `) )) -gt 2 ]; then cp -- ` + q + ` ` + dst + ` && chmod 600 -- ` + dst + `; else cp -- ` + dst + ` ` + q + `; fi; fi; fi` +
		` || echo 'amagi: Pi 登录凭据同步到 WSL 失败，继续使用 WSL 内既有副本'`
}

// piLoginStripAgentDirForWSL 从 WSL spec 的进程环境里移除 PI_CODING_AGENT_DIR
// 及其在 WSLENV 转发清单中的引用。R2 的钉定值是 Windows 路径，跨界后对
// distro 内的 pi 是非法路径（1.3.59 PI_* 泄漏事故形态）；桌面链路通常已在
// resolveEmbeddedLaunchSpec 的 StripWSLHostPathEnv 处剥离（伴随 WARN），
// planner 链路无该前置防线，这里统一兑底。凭据键在 BuildEnv 期已被空值
// 删除，不会进入转发面。
func piLoginStripAgentDirForWSL(env []string) []string {
	const key = "PI_CODING_AGENT_DIR"
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			out = append(out, kv)
			continue
		}
		if strings.EqualFold(k, key) {
			continue
		}
		if strings.EqualFold(k, "WSLENV") {
			if cleaned := stripWSLENVToken(v, key); cleaned != "" {
				out = append(out, "WSLENV="+cleaned)
			}
			continue
		}
		out = append(out, kv)
	}
	return out
}

// stripWSLENVToken 从 WSLENV 冒号清单中移除指定键（兼容 name/flags 形态），
// 清单空时返回空串（变量整体移除）。
func stripWSLENVToken(wslenv, key string) string {
	kept := make([]string, 0, 8)
	for _, name := range strings.Split(wslenv, ":") {
		base := strings.TrimSpace(name)
		if idx := strings.Index(base, "/"); idx >= 0 {
			base = base[:idx]
		}
		if base == "" || strings.EqualFold(base, key) {
			continue
		}
		kept = append(kept, name)
	}
	return strings.Join(kept, ":")
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
	env := launcher.BuildEnv(p.envVars.MergeWithSystem(), piLoginEnvOverrides(login.AgentDir))
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
	// A2 闸门解除（主上裁定 2026-10-16）：WSL spec 不再 fail，改走 WSL 锚定
	// 变体（载荷内嵌惰性同步 + env -u 重锚，见 piLoginWSLReAnchorSpec）。
	// 登录预设 contextWindow 覆写也不在 BuildPlan 写盘：remote adapter 契约
	// 要求 BuildPlan 纯读零副作用（remote_session_adapter.go「BuildPlan
	// (pure read, zero side effects)」、launch_planner.go 头注 no process
	// spawn）。覆写统一在预设保存/删除时同步写入 canonical root 的
	// models.json（ConfigService.SetPiLoginPresetChangeListener + app 装配注入），
	// 桌面 LaunchPiSession 另有启动兜底；本路径（remote/restart）读已落盘的目标态。
	// Shared execution-boundary constraint: pin the canonical root after any
	// shell startup. No ConfigMutation, secret lookup, managed provider or
	// auth write on this path.
	if spec.BootstrapMode == platform.BootstrapWSL {
		spec = piLoginWSLReAnchorSpec(spec, login.AgentDir)
	} else {
		spec = piLoginReAnchorSpec(spec, login.AgentDir)
	}
	effects := []launchplan.EffectSpec{{Kind: launchplan.EffectPTYStart, Process: &launchplan.ProcessStartSpec{
		Mode: launchplan.ModeEmbedded, Resolved: spec, RequireRunHandle: true,
	}}, buildBootstrapEffect(spec)}
	return p.finalizePlan(recipe, nil, effects)
}
