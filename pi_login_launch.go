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
// every execution boundary must pin it (R2) and re-anchor it after POSIX shell
// startup (R3). It never carries or copies credentials.
type piLoginLaunch struct {
	Preset   *config.PiLoginPreset
	AgentDir string
}

// resolvePiLoginLaunch never lets a login selection fall through to
// API-provider/default-model resolution; a stale login selection fails closed.
func resolvePiLoginLaunch(resolver piLoginPresetResolver, modelRef, providerRef string, wsl bool) (*piLoginLaunch, error) {
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
	agentDir, err := piconfig.RequirePiLogin(preset.Provider, wsl)
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

func (p *appLaunchPlanner) buildPiLoginPlan(recipe launchplan.StableRecipe, req launchplan.BuildRequest) (*launchplan.Plan, *launchplan.BuildFailure) {
	fail := func() (*launchplan.Plan, *launchplan.BuildFailure) {
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureLaunchContext, CLIType: req.CLIType}
	}
	resolver, ok := p.config.(piLoginPresetResolver)
	if !ok {
		return fail()
	}
	login, err := resolvePiLoginLaunch(resolver, recipe.ModelRef, recipe.ProviderRef,
		platform.EmbeddedLaunchTargetsWSL("embedded", recipe.ShellRef, p.envVars.MergeWithSystem()))
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
	spec, err := p.resolver.Resolve(platform.ResolveRequest{
		AppType: string(session.AppTypePi), LaunchMode: "embedded", RequestedShellPath: recipe.ShellRef,
		WorkDir: recipe.Workdir, Env: env, CLIArgs: args, PTYCols: 120, PTYRows: 40,
	})
	if err != nil {
		return nil, &launchplan.BuildFailure{Kind: launchplan.FailureCapability, CLIType: req.CLIType}
	}
	if spec.BootstrapMode == platform.BootstrapWSL {
		return fail()
	}
	// Shared execution-boundary constraint: pin the canonical root after any
	// shell startup. No ConfigMutation, secret lookup, managed provider or
	// auth write on this path.
	spec = piLoginReAnchorSpec(spec, login.AgentDir)
	effects := []launchplan.EffectSpec{{Kind: launchplan.EffectPTYStart, Process: &launchplan.ProcessStartSpec{
		Mode: launchplan.ModeEmbedded, Resolved: spec, RequireRunHandle: true,
	}}, buildBootstrapEffect(spec)}
	return p.finalizePlan(recipe, nil, effects)
}
