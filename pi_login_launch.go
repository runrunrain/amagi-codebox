package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/piconfig"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/wslsupply"
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

// runWSLLoginSupplyFn 是桌面启动闸门调用的供给执行接缝（单测替换；生产经
// wslsupply 协调器 RunUnits 执行 pi/a2-auth 单元，A2 判定语义零变化）。
// P3-S0 取代旧 syncWSLPiLoginAuthFn：桌面与 remote effect 共用同一入口。
var runWSLLoginSupplyFn = defaultRunWSLLoginSupply

// defaultRunWSLLoginSupply 经协调器执行 pi 登录供给（A2 单元必需；distro
// 交由闸门上层解析后传入）。runner 缺位时 fail closed（不得静默放行无凭据
// 会话）。P3-S1：协调器已注册托管面适配器时一并运行 pi/managed-models（登录
// contextWindow 补齐 + amagi-* 期望集随登录启动 lazy 兑现；裸协调器——如单
// 测——自动降级为 A2-only）。P3-S2：三个键级配置投影（settings/amagi/mcp）
// 同批随登录启动补漏（可选偏好面，失败仅告警）。
func defaultRunWSLLoginSupply(ctx context.Context, runner wslsupply.Runner, distro string) (wslsupply.RunResult, error) {
	if runner == nil {
		return wslsupply.RunResult{Results: []wslsupply.UnitResult{{
			Unit: wslsupply.UnitPiA2Auth, Policy: wslsupply.PolicyA2Auth,
			Outcome: wslsupply.UnitFailed, Detail: "supply-runner-unavailable",
			Err: errors.New("wsl supply coordinator unavailable"),
		}}}, nil
	}
	units := wslSupplyUnitsFiltered(runner,
		wslsupply.UnitPiA2Auth, wslsupply.UnitPiManagedModels,
		wslsupply.UnitPiSettings, wslsupply.UnitPiAmagi, wslsupply.UnitPiMCP)
	return runner.RunUnits(ctx, distro, wslsupply.CLIPi, wslsupply.ReasonLaunch, units)
}

// wslLoginSupplyVerdict 是一次 pi 登录供给结果的启动闸门判定（P3-S0 统一
// 执行边界：桌面闸门与 remote effect 共用同一映射，保证两链路结果一致）。
type wslLoginSupplyVerdict struct {
	info      bool
	warn      bool
	message   string // 日志消息（不含凭据内容）
	detail    string
	launchErr error // 非 nil → 拒绝启动（中文文案，与桌面既有文案逐字一致）
}

// verdictForWSLLoginSupply 把供给结果映射为闸门判定。语义与 A2 解除后的桌面
// 闸门一致：Copied/DistroNewer 记 INFO 放行；Fresh 静默放行；TieKept WARN 放行；
// DistroHasCopy WARN 降级放行；DistroNoAuth/阻断 fail closed（源缺失 → 登录
// 失效文案，其余 → 同步失败文案）。
// verdictForWSLLoginSupply 把供给结果映射为闸门判定。语义与 A2 解除后的桌面
// 闸门一致：Copied/DistroNewer 记 INFO 放行；Fresh 静默放行；TieKept WARN 放行；
// DistroHasCopy WARN 降级放行；DistroNoAuth/阻断 fail closed（源缺失 → 登录
// 失效文案，其余 → 同步失败文案）。攻坚契约（启动聚合）：先确认本次 A2 资格，
// 再处理可选项——不能因前面的可选 Failed/awaiting-consent 提前 return 跳过
// A2 安全资格。
func verdictForWSLLoginSupply(res wslsupply.RunResult, runErr error) wslLoginSupplyVerdict {
	if runErr != nil {
		return wslLoginFailClosed(runErr)
	}
	// 1) A2 资格优先。
	a2 := res.A2Unit()
	if a2 == nil || a2.A2 == nil {
		return wslLoginFailClosed(errors.New("supply produced no a2 result"))
	}
	a2Verdict := wslLoginA2Verdict(a2, res.Target.Distro)
	if a2Verdict.launchErr != nil {
		return a2Verdict
	}
	// 2) A2 资格确认后再处理可选单元（首个非 A2 阻断/失败项）。
	if blocked := res.LaunchBlocked(); blocked != nil && blocked.A2 == nil {
		if blocked.Unit == wslsupply.UnitPiA2Auth {
			return wslLoginFailClosed(blocked.Err)
		} else if blocked.Unit == wslsupply.UnitPiManagedModels {
			// P3-S1：托管 models.json 属可选普通托管面——失败/阻断仅告警放行
			//（design §六「可选普通设置同步失败，旧 WSL 配置可读且安全 → 启动
			// 继续」），A2 语义不受影响；未知单元仍 fail closed（保守）。
			return wslLoginSupplyVerdict{warn: true,
				message: "WSL 托管 provider 配置同步未完成，本次沿用 WSL 现有配置",
				detail:  fmt.Sprintf("detail=%s", blocked.Detail)}
		} else if blocked.Unit == wslsupply.UnitPiSettings || blocked.Unit == wslsupply.UnitPiAmagi || blocked.Unit == wslsupply.UnitPiMCP {
			// P3-S2：键级配置投影同为可选偏好面（冻结/未托管/转换受阻均为
			// 预期态）——告警放行，A2 语义不受影响。F-3（S2 diting）：未同意
			//（awaiting-consent）是启用前的预期态，静默放行——授权入口在配置
			// 状态页，不在每次登录刷告警。
			if blocked.Detail == "awaiting-consent" {
				return wslLoginSupplyVerdict{}
			}
			return wslLoginSupplyVerdict{warn: true,
				message: "WSL Pi 配置投影未全部应用（保留 WSL 现状的条目见配置状态页）",
				detail:  fmt.Sprintf("unit=%s detail=%s", blocked.Unit, blocked.Detail)}
		} else {
			return wslLoginFailClosed(blocked.Err)
		}
	}
	return a2Verdict
}

// wslLoginFailClosed 构造登录供给的 fail-closed 判定（统一中文文案）。
func wslLoginFailClosed(err error) wslLoginSupplyVerdict {
	return wslLoginSupplyVerdict{launchErr: fmt.Errorf("WSL 内没有可用的 Pi 登录凭据，且同步失败：%v；请确认 WSL 发行版状态后重试", err)}
}

// wslLoginA2Verdict 把 A2 结果映射为闸门判定（Copied/DistroNewer 记 INFO
// 放行；Fresh 静默放行；TieKept WARN 放行；HasCopy WARN 降级放行；
// NoAuth/未知 fail closed——源缺失 → 登录失效文案）。
func wslLoginA2Verdict(a2 *wslsupply.UnitResult, distro string) wslLoginSupplyVerdict {
	switch a2.A2.Outcome {
	case launcher.SyncWSLPiLoginAuthCopied:
		return wslLoginSupplyVerdict{info: true, message: "已同步 Pi 登录凭据到 WSL", detail: fmt.Sprintf("distro=%s", distro)}
	case launcher.SyncWSLPiLoginAuthFresh:
		return wslLoginSupplyVerdict{}
	case launcher.SyncWSLPiLoginAuthDistroNewer:
		// M-1：WSL 内 pi 轮转出的新凭据已回同步 Windows 侧。
		return wslLoginSupplyVerdict{info: true, message: "已从 WSL 回同步登录凭据", detail: fmt.Sprintf("distro=%s", distro)}
	case launcher.SyncWSLPiLoginAuthDistroTieKept:
		return wslLoginSupplyVerdict{warn: true, message: "两侧 Pi 登录凭据时间戳不可判，保守按 WSL 侧同步", detail: fmt.Sprintf("distro=%s", distro)}
	case launcher.SyncWSLPiLoginAuthDistroHasCopy:
		return wslLoginSupplyVerdict{warn: true, message: "WSL Pi 登录凭据同步失败，降级使用 WSL 内既有副本", detail: errStringForLog(a2.A2.Err)}
	default: // DistroNoAuth/未知：fail closed
		if a2.A2.Err != nil && errors.Is(a2.A2.Err, launcher.ErrWSLPiLoginSourceMissing) {
			return wslLoginSupplyVerdict{launchErr: errors.New("Pi 登录已失效：Windows 侧未找到登录凭据，请刷新服务提供商并重新登录 Pi")}
		}
		if a2.A2.Err != nil {
			return wslLoginFailClosed(a2.A2.Err)
		}
		return wslLoginFailClosed(errors.New("no usable credentials on either side"))
	}
}

// piLoginWSLReAnchorSpec 是 R3 的 WSL 变体，也是 R2 锚定语义的 WSL/非 WSL
// 分叉点（A2 主上裁定 2026-10-16；P3-S0 起凭据同步片段退役）：
//   - 非 WSL（piLoginReAnchorSpec）：PI_CODING_AGENT_DIR 钉 CodeBox 进程
//     Home 的 canonical 目录，shell startup 后重钉同值（R2/R3 原文不变）。
//   - WSL（本函数）：不钉 Windows 路径，载荷以 env -u PI_CODING_AGENT_DIR
//     收尾——pi 在 distro 内读取自己的 $HOME/.pi/agent，凭据副本由启动时
//     同步保障。P3-S0 统一执行边界：凭据同步不再内嵌 bash cmp/cp 片段
//     （旧 shell 实现无原子写、无 fail-closed 分类，设计 §一/§九点名），改由
//     wslsupply 协调器的 EffectWSLConfigSupply 在 PTY 之前执行同一 Go 侧
//     A2 适配器（launcher.SyncWSLPiLoginAuth，判定语义零变化）；本函数只
//     保留 agent root 与凭据 env 重锚（env -u 剥离误用面）。
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
	_ = windowsAgentDir // 同步源不再进入载荷（凭据字节永不跨界进命令行）；参数保留以钉住调用契约。
	envTokens := []string{"env"}
	for _, key := range piLoginCredentialEnvKeys {
		envTokens = append(envTokens, "-u", key)
	}
	envTokens = append(envTokens, "-u", "PI_CODING_AGENT_DIR")
	cliTokens := []string{strings.Join(envTokens, " ") + " " + shellSingleQuote(cli)}
	for _, arg := range spec.CLI.Args {
		cliTokens = append(cliTokens, shellSingleQuote(arg))
	}
	spec.StartupCommand = strings.Join(cliTokens, " ") + "; exec bash -li"
	return spec
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
	// 变体（P3-S0 起仅 env -u 重锚，凭据同步片段退役）+ 前置供给 effect。
	// 登录预设 contextWindow 覆写也不在 BuildPlan 写盘：remote adapter 契约
	// 要求 BuildPlan 纯读零副作用（remote_session_adapter.go「BuildPlan
	// (pure read, zero side effects)」、launch_planner.go 头注 no process
	// spawn）。WSL 凭据同步同样不进 BuildPlan：计划只携带待供给描述
	//（EffectWSLConfigSupply，distro 留空到 Apply 阶段解析，与 PTY 构建同源），
	// Inspect/Plan/Apply 全部在执行器 Apply 阶段经 wslsupply 协调器执行。
	// 覆写统一在预设保存/删除时同步写入 canonical root 的
	// models.json（ConfigService.SetPiLoginPresetChangeListener + app 装配注入），
	// 桌面 LaunchPiSession 另有启动兜底；本路径（remote/restart）读已落盘的目标态。
	// Shared execution-boundary constraint: pin the canonical root after any
	// shell startup. No ConfigMutation, secret lookup, managed provider or
	// auth write on this path.
	var effects []launchplan.EffectSpec
	var loginAdmissions []launchplan.SharedAdmissionSpec
	// Claude 订阅账号轮换（design §6.3 + 任务 F）：先取凭据 admission 描述
	//（blocked → 计划构建失败，入口关闭）；受管状态下 WSL 登录会话消费
	// canonical 凭据的 distro 副本，v1 不支持——明确拒绝，不动现有 WSL
	// 同步逻辑。非 WSL 登录会话消费 canonical auth.json，携带 admission。
	if credSpec, ok, credErr := p.claudeCredentialAdmissionSpec(); credErr != nil {
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureLaunchContext, CLIType: req.CLIType}
	} else if ok {
		if spec.BootstrapMode == platform.BootstrapWSL {
			return nil, &launchplan.BuildFailure{Kind: launchplan.FailureLaunchContext, CLIType: req.CLIType}
		}
		loginAdmissions = append(loginAdmissions, credSpec)
	}
	if spec.BootstrapMode == platform.BootstrapWSL {
		spec = piLoginWSLReAnchorSpec(spec, login.AgentDir)
		// P3-S1：登录供给一并携带 pi/managed-models（登录 contextWindow 补齐 +
		// amagi-* 期望集；执行器按协调器能力过滤——裸协调器自动降级 A2-only）。
		// P3-S2：三个键级配置投影同批补漏（可选偏好面，失败告警放行）。
		effects = append(effects, launchplan.EffectSpec{
			Kind: launchplan.EffectWSLConfigSupply,
			WSLSupply: &launchplan.WSLConfigSupplySpec{
				CLI:    string(wslsupply.CLIPi),
				Reason: string(wslsupply.ReasonLaunch),
				Units: []string{
					string(wslsupply.UnitPiA2Auth),
					string(wslsupply.UnitPiManagedModels),
					string(wslsupply.UnitPiSettings),
					string(wslsupply.UnitPiAmagi),
					string(wslsupply.UnitPiMCP),
				},
			},
		})
	} else {
		spec = piLoginReAnchorSpec(spec, login.AgentDir)
	}
	effects = append(effects, launchplan.EffectSpec{Kind: launchplan.EffectPTYStart, Process: &launchplan.ProcessStartSpec{
		Mode: launchplan.ModeEmbedded, Resolved: spec, RequireRunHandle: true,
	}}, buildBootstrapEffect(spec))
	return p.finalizePlan(recipe, loginAdmissions, effects)
}
