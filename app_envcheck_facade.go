package main

// app_envcheck_facade.go — EnvCheck, Claude install/config fix, WSL CLI/DockerWSL,
// and Headroom / Codex-global Headroom App facades.

import (
	"context"
	"fmt"
	"strings"

	"amagi-codebox/internal/dockerwsl"
	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/wslsetup"
)

// getEnvCheckStatus 获取最近一次环境检测结果（可能为缓存）。
func (a *App) getEnvCheckStatus() *envcheck.OverallStatus {
	return a.EnvCheck.GetCachedStatus()
}

// RunEnvCheck 手动触发环境检测。
func (a *App) RunEnvCheck() (*envcheck.OverallStatus, error) {
	return a.EnvCheck.CheckAll()
}

// CheckTool 手动触发单个 CLI 工具检测。
func (a *App) CheckTool(tool string) (*envcheck.CheckStatus, error) {
	t, err := parseCLITool(tool)
	if err != nil {
		return nil, fmt.Errorf("check tool: %w", err)
	}
	return a.EnvCheck.CheckOne(t)
}

// InstallTool 安装指定 CLI 工具（用户确认后调用）。
func (a *App) InstallTool(tool string) (*envcheck.InstallResult, error) {
	t, err := parseCLITool(tool)
	if err != nil {
		return nil, fmt.Errorf("install tool: %w", err)
	}
	return a.EnvCheck.Install(t)
}

// GetWSLCLIStatus 返回 WSL 内托管 CLI 的安装状态快照（发行版、原生 Node 版本、
// 各 CLI 是否已原生安装）。非 Windows 或无可用发行版时 Available=false。
func (a *App) GetWSLCLIStatus() wslsetup.Status {
	return a.WSLSetup.GetStatus()
}

// InstallCLIToWSL 将指定 CLI（claude/opencode/codex）安装到 WSL 发行版内部：
// 确保原生 Node 20 + 用户级 npm 前缀，再 npm i -g 对应包并包并校验。幂等：已原生安装
// 则直接返回 AlreadyOK。Windows 有效。
func (a *App) InstallCLIToWSL(tool string) (*wslsetup.InstallResult, error) {
	a.Log.Info("wslsetup", "安装 CLI 到 WSL 请求", "tool="+tool)
	return a.WSLSetup.InstallTool(tool)
}

// GetDockerWSLHealth 返回 Docker Desktop ↔ WSL 集成的健康快照（挂载字节数/状态、
// 引擎就绪、工具发行版、进程计数、问题清单与自愈建议）。非 Windows 或未安装
// Docker Desktop 时 Available=false。对应 2026-09-25 运维复盘 §1.2 环境面修复。
func (a *App) GetDockerWSLHealth() dockerwsl.HealthReport {
	return a.DockerWSL.GetHealth()
}

// SelfHealDockerWSLIntegration 执行实证自愈序列（全退 Desktop → 仅 terminate
// docker-desktop 工具发行版 → 全新启动 → 等引擎就绪（300s 预算）→ 复核集成挂载）。
// 同步返回步骤回执；Ubuntu 用户发行版与 CodeBox 会话零影响。
func (a *App) SelfHealDockerWSLIntegration() dockerwsl.SelfHealReport {
	a.Log.Info("dockerwsl", "Docker WSL 集成自愈请求")
	return a.DockerWSL.SelfHeal(context.Background())
}

// UpdateTool 更新指定 CLI 工具。
func (a *App) UpdateTool(tool string) (*envcheck.InstallResult, error) {
	t, err := parseCLITool(tool)
	if err != nil {
		return nil, fmt.Errorf("update tool: %w", err)
	}
	return a.EnvCheck.Update(t)
}

// StartInstallToolAsync 异步安装指定 CLI 工具，立即返回操作状态。
// 安装在后台 goroutine 中执行，不受前端页面切换影响。
func (a *App) StartInstallToolAsync(tool string) (*envcheck.OperationState, error) {
	t, err := parseCLITool(tool)
	if err != nil {
		return nil, fmt.Errorf("start install tool: %w", err)
	}
	return a.EnvCheck.StartInstallTool(t)
}

// StartUpdateToolAsync 异步更新指定 CLI 工具，立即返回操作状态。
// 更新在后台 goroutine 中执行，不受前端页面切换影响。
func (a *App) StartUpdateToolAsync(tool string) (*envcheck.OperationState, error) {
	t, err := parseCLITool(tool)
	if err != nil {
		return nil, fmt.Errorf("start update tool: %w", err)
	}
	return a.EnvCheck.StartUpdateTool(t)
}

// GetEnvCheckSnapshot 获取环境检测快照（包含工具状态和当前操作）。
// 前端可轮询此接口以获取最新状态。
func (a *App) GetEnvCheckSnapshot() *envcheck.EnvCheckSnapshot {
	return a.EnvCheck.GetEnvCheckSnapshot()
}

// RunEnvFixAction 执行白名单化的环境修复动作。
// 前端传入 FixActionRequest，后端验证 action 类型后执行对应修复。
func (a *App) RunEnvFixAction(action string, tool string, extraPath string) (*envcheck.FixActionResult, error) {
	req := envcheck.FixActionRequest{
		Action:    envcheck.SolutionType(action),
		Tool:      envcheck.CLITool(tool),
		ExtraPath: extraPath,
	}
	result, err := a.EnvCheck.RunFixAction(req)
	if err != nil {
		return nil, fmt.Errorf("run fix action: %w", err)
	}
	// Best-effort: refresh check after successful fix
	if result != nil && result.Success && result.Changed {
		go func() {
			_, _ = a.EnvCheck.CheckAll()
		}()
	}
	return result, nil
}

// InstallClaudeWithMethod installs Claude Code using the specified method.
// method must be "npm" or "native".
func (a *App) InstallClaudeWithMethod(method string) (*envcheck.InstallResult, error) {
	// Convert frontend method string to ClaudeInstallMethod
	var m envcheck.ClaudeInstallMethod
	switch method {
	case "npm":
		m = envcheck.ClaudeInstallNPM
	case "native":
		m = envcheck.ClaudeInstallNative
	default:
		return nil, fmt.Errorf("不支持的安装方式: %s (支持: npm, native)", method)
	}

	return a.EnvCheck.InstallClaudeCodeWithMethod(m)
}

// StartInstallClaudeWithMethodAsync asynchronously installs Claude Code using
// the specified method and exposes progress through GetEnvCheckSnapshot.
func (a *App) StartInstallClaudeWithMethodAsync(method string) (*envcheck.OperationState, error) {
	var m envcheck.ClaudeInstallMethod
	switch method {
	case "npm":
		m = envcheck.ClaudeInstallNPM
	case "native":
		m = envcheck.ClaudeInstallNative
	default:
		return nil, fmt.Errorf("不支持的安装方式: %s (支持: npm, native)", method)
	}

	return a.EnvCheck.StartInstallClaudeCodeWithMethod(m)
}

// CleanClaudeInstall removes an existing Claude Code installation.
// method should be the current install method ("npm" or "native").
func (a *App) CleanClaudeInstall(method string) (*envcheck.InstallResult, error) {
	var m envcheck.InstallMethod
	switch method {
	case "npm":
		m = envcheck.InstallMethodNPM
	case "native":
		m = envcheck.InstallMethodNative
	default:
		return nil, fmt.Errorf("不支持的安装方式: %s", method)
	}

	return a.EnvCheck.CleanClaudeCode(m)
}

// UninstallClaudeCode removes an existing Claude Code installation without reinstalling.
// If method is empty, it auto-detects the current install method from the latest check.
func (a *App) UninstallClaudeCode(method string) (*envcheck.InstallResult, error) {
	targetMethod := method
	if targetMethod == "" {
		// Auto-detect from cached status
		status := a.EnvCheck.GetCachedStatus()
		if status == nil {
			return nil, fmt.Errorf("尚未执行环境检测，无法确定安装方式")
		}
		claudeStatus, ok := status.Items[string(envcheck.ToolClaudeCode)]
		if !ok || !claudeStatus.Installed {
			return nil, fmt.Errorf("Claude Code 未安装，无需卸载")
		}
		targetMethod = string(claudeStatus.InstallMethod)
	}

	var m envcheck.InstallMethod
	switch envcheck.InstallMethod(targetMethod) {
	case envcheck.InstallMethodNPM:
		m = envcheck.InstallMethodNPM
	case envcheck.InstallMethodNative:
		m = envcheck.InstallMethodNative
	default:
		return nil, fmt.Errorf("无法确定安装方式 (%s)，请手动指定", targetMethod)
	}

	result, err := a.EnvCheck.CleanClaudeCode(m)
	if err != nil {
		return result, err
	}
	return result, nil
}

// CleanHeadroom removes the CodeBox-managed Headroom venv directory via the
// lease-guarded EnvCheck service.
func (a *App) CleanHeadroom() (*envcheck.InstallResult, error) {
	return a.EnvCheck.CleanHeadroom()
}

// CheckClaudeConfig scans Claude Code configuration files and reports
// which configuration items are present or missing.
func (a *App) CheckClaudeConfig() (*envcheck.ClaudeConfigStatus, error) {
	return a.EnvCheck.CheckClaudeConfig()
}

// FixClaudeConfig writes a single configuration item to Claude Code settings.
// Only missing items are added; existing values are never overwritten.
func (a *App) FixClaudeConfig(key string, value string, filePath string) (*envcheck.ConfigFixResult, error) {
	// Defense-in-depth: validate file path at the binding layer too
	if filePath != "" {
		expanded := envcheck.ExpandTilde(filePath)
		if !envcheck.IsConfigPathAllowed(expanded) {
			return nil, fmt.Errorf("目标路径 %s 不在允许的配置文件列表中，拒绝写入", expanded)
		}
	}
	return a.EnvCheck.FixClaudeConfig(envcheck.ConfigFixRequest{
		Key:      key,
		Value:    value,
		FilePath: filePath,
	})
}

// parseCLITool 将前端传入的字符串转为 CLITool 枚举。
func parseCLITool(tool string) (envcheck.CLITool, error) {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "claude-code", "claude_code", "claude":
		return envcheck.ToolClaudeCode, nil
	case "opencode", "open-code", "open_code":
		return envcheck.ToolOpenCode, nil
	case "codex":
		return envcheck.ToolCodex, nil
	case "pi":
		return envcheck.ToolPi, nil
	case "omp":
		return envcheck.ToolOmp, nil
	case "headroom":
		return envcheck.ToolHeadroom, nil
	default:
		return "", fmt.Errorf("unknown CLI tool: %s", tool)
	}
}

// GetHeadroomSavings 查询 headroom 上下文压缩节省统计（压缩次数、节省 token 等）。
// headroom 未安装或查询失败时返回 error，前端据此显示空态；绝不返回伪造零值报告冒充"有数据"。
//
// 注意：savings 读取的是 headroom 共享 ledger（~/.headroom/savings_events.jsonl），
// claude 会话级（8787）与 codex 全局（8788）实例的节省数据均写入同一文件，靠
// SavingsReport.ByClient（client 维度，如 "claude-code" / "codex"）区分来源。
// 前端按 by_client 分项展示即可，后端无需为第二实例改动 savings 逻辑。
func (a *App) GetHeadroomSavings() (*headroom.SavingsReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), headroom.SavingsTimeout)
	defer cancel()
	return a.Headroom.GetSavings(ctx)
}

// GetHeadroomPerfByClient 查询 headroom perf 按 client 聚合的性能统计
// （请求数、平均 cache 命中率、累计节省/读取 token 数、节省百分比），供前端
// codex 卡片突出 cache 命中率。实测（headroom 0.32.x）codex 经 headroom 的
// 实际收益是 prefix cache 稳定（cache 命中率高 → cached token 计费约为新的 1/5），
// 而非输入体积压缩（OpenAI responses 协议 tok_saved 多为 0）；故前端 codex 卡片
// 应突出 AvgCacheHitPct，而非 tokens_saved。
//
// headroom 未安装或查询失败时返回 error，前端据此显示空态；绝不返回伪造数据冒充"有数据"。
// 与 GetHeadroomSavings 一样读取共享 ledger（`headroom perf --format json --raw`），
// 按 record.client 区分来源（codex / claude-code 等），后端无需为第二实例改动 perf 逻辑。
func (a *App) GetHeadroomPerfByClient() ([]headroom.ClientPerfStat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), headroom.PerfTimeout)
	defer cancel()
	return a.Headroom.GetPerfByClient(ctx)
}

// CodexGlobalHeadroomStatus is the frontend-facing snapshot of the codex-global
// headroom toggle. It augments the persisted settings state with the live
// running flag so the UI can render the actual proxy state in one round-trip.
type CodexGlobalHeadroomStatus struct {
	Enabled bool   `json:"enabled"`
	Target  string `json:"target"`
	Port    int    `json:"port"`
	Running bool   `json:"running"`
}

// GetCodexGlobalHeadroom returns the persisted codex-global headroom toggle plus
// the live running state of the second headroom instance (port 8788).
func (a *App) GetCodexGlobalHeadroom() CodexGlobalHeadroomStatus {
	state := a.Settings.GetCodexGlobalHeadroom()
	running := false
	if a.CodexHeadroom != nil {
		running = a.CodexHeadroom.IsRunning()
	}
	return CodexGlobalHeadroomStatus{
		Enabled: state.Enabled,
		Target:  state.Target,
		Port:    state.Port,
		Running: running,
	}
}

// SetCodexGlobalHeadroom toggles the codex-global headroom (independent 8788
// instance with an OpenAI target). It is fully orthogonal to the claude
// session-level headroom (a.Headroom, 8787): this method never starts, stops or
// reconfigures the claude headroom.
//
// enabled=true:
//   - Starts the second headroom instance (StartForOpenAI) if it is not already
//     running. target falls back to codexGlobalHeadroomDefaultTarget when empty;
//     port falls back to CodexGlobalHeadroomDefaultPort when <= 0.
//   - Writes the openai_base_url marker block into ~/.codex/config.toml so codex
//     routes its traffic through the local proxy (with backup + idempotency).
//   - Persists the toggle so Startup can restore it on next launch.
//
// enabled=false:
//   - Removes the openai_base_url marker block first (so codex stops routing
//     through the proxy before the proxy goes away), then stops the instance.
//   - Clears the persisted toggle.
//
// Returns the resulting status. Errors from the proxy start are returned, but
// config/persistence cleanup on disable is best-effort: a failure to rewrite
// config.toml is surfaced as an error so the user knows codex may still point at
// a stopping proxy.
func (a *App) SetCodexGlobalHeadroom(enabled bool, target string, port int) (CodexGlobalHeadroomStatus, error) {
	if a.CodexHeadroom == nil {
		return CodexGlobalHeadroomStatus{}, fmt.Errorf("codex global headroom service is not initialized")
	}
	if a.Settings == nil {
		return CodexGlobalHeadroomStatus{}, fmt.Errorf("settings service is not initialized")
	}

	// Serialize against the startup restore goroutine: the Start/Stop +
	// config-sync + persistence sequence must be atomic with respect to
	// restoreCodexGlobalHeadroomOnStartup, otherwise a UI disable racing the
	// restore can leave Enabled=false with the proxy running and config.toml
	// still carrying the marker block.
	a.codexGlobalMu.Lock()
	defer a.codexGlobalMu.Unlock()

	// M-006: the Codex global headroom is a shared singleton. Acquire/release is
	// session-scoped (Codex launches hold a lease while the toggle is on); the
	// toggle itself is a Start/Stop mutation that MUST consult the coordinator so
	// it cannot reconfigure/stop the singleton out from under active sessions.
	mutation := remote.MutationStartDifferentConfig
	if !enabled {
		mutation = remote.MutationStop
	}
	admission, err := a.acquireSharedMutation(remote.SharedServiceCodexHeadroom, mutation)
	if err != nil {
		return a.GetCodexGlobalHeadroom(), err
	}
	defer a.sharedCoord.ReleaseMutationAdmission(admission)

	if enabled {
		target = strings.TrimSpace(target)
		if target == "" {
			target = codexGlobalHeadroomDefaultTarget
		}
		if port <= 0 {
			port = CodexGlobalHeadroomDefaultPort
		}

		// Start the proxy first; if it fails, do not rewrite config.toml to point
		// codex at a dead proxy. StartForOpenAI is a no-op when already running
		// on this instance.
		if !a.CodexHeadroom.IsRunning() {
			if err := a.CodexHeadroom.StartForOpenAI(target); err != nil {
				return CodexGlobalHeadroomStatus{}, fmt.Errorf("start codex global headroom: %w", err)
			}
			a.Log.Info("headroom", "codex 全局上下文压缩已启用",
				fmt.Sprintf("codex → headroom:127.0.0.1:%d → %s", a.CodexHeadroom.GetPort(), target))
		}

		if err := syncCodexGlobalHeadroomConfig(true, port); err != nil {
			a.Log.Warn("headroom", "写入 codex 全局 openai_base_url 失败", err.Error())
			return CodexGlobalHeadroomStatus{}, fmt.Errorf("write codex openai_base_url: %w", err)
		}

		if err := a.Settings.SetCodexGlobalHeadroom(true, target, port); err != nil {
			a.Log.Warn("headroom", "持久化 codex 全局 headroom 开关失败", err.Error())
			return CodexGlobalHeadroomStatus{}, fmt.Errorf("persist codex global headroom: %w", err)
		}
		return a.GetCodexGlobalHeadroom(), nil
	}

	// Disable path: remove the config marker first (so codex stops targeting the
	// proxy before it stops), then stop the proxy, then clear persistence.
	if err := syncCodexGlobalHeadroomConfig(false, 0); err != nil {
		a.Log.Warn("headroom", "移除 codex 全局 openai_base_url 失败", err.Error())
		return CodexGlobalHeadroomStatus{}, fmt.Errorf("remove codex openai_base_url: %w", err)
	}
	if a.CodexHeadroom.IsRunning() {
		if err := a.CodexHeadroom.Stop(); err != nil {
			a.Log.Warn("headroom", "停止 codex 全局 headroom 失败", err.Error())
		}
	}
	if err := a.Settings.SetCodexGlobalHeadroom(false, "", 0); err != nil {
		a.Log.Warn("headroom", "清除 codex 全局 headroom 持久化失败", err.Error())
		return CodexGlobalHeadroomStatus{}, fmt.Errorf("clear codex global headroom persistence: %w", err)
	}
	return a.GetCodexGlobalHeadroom(), nil
}

// restoreCodexGlobalHeadroomOnStartup re-establishes the codex-global headroom
// proxy (8788) when it was enabled at last shutdown. It must run AFTER
// Settings.Load has completed (Startup calls it asynchronously, well after the
// synchronous settings load). Every failure is best-effort: a broken proxy or
// config write only downgrades compression, it never blocks app startup or
// affects the claude session headroom. The marker block is re-synced so a config
// rewrite by another tool (or a prior crash mid-write) cannot leave codex
// pointing at the wrong URL.
func (a *App) restoreCodexGlobalHeadroomOnStartup() {
	if a.Settings == nil || a.CodexHeadroom == nil {
		return
	}
	if a.isExternalCleanupRecoveryBlocked() {
		if a.Log != nil {
			a.Log.Warn("headroom", "跳过 codex 全局 headroom 恢复：外部进程清理登记未完成")
		}
		return
	}
	// Hold codexGlobalMu across the read + orchestration so a concurrent UI
	// SetCodexGlobalHeadroom(false) cannot slip in between the Enabled read and
	// the StartForOpenAI / config-sync below (which would otherwise re-enable a
	// proxy the user just disabled). SetCodexGlobalHeadroom takes the same lock,
	// so the two paths are fully serialized.
	a.codexGlobalMu.Lock()
	defer a.codexGlobalMu.Unlock()

	state := a.Settings.GetCodexGlobalHeadroom()
	if !state.Enabled {
		return
	}
	target := strings.TrimSpace(state.Target)
	if target == "" {
		target = codexGlobalHeadroomDefaultTarget
	}
	port := state.Port
	if port <= 0 {
		port = CodexGlobalHeadroomDefaultPort
	}

	// R5-002: startup restoration is a real Headroom Start mutation too. Keep an
	// exact coordinator token live across Start + config sync + persistence so a
	// concurrent uninstall drain cannot observe an empty dependency set midway.
	if a.sharedCoord != nil {
		admission, err := a.sharedCoord.AcquireMutationAdmission(remote.SharedServiceCodexHeadroom, remote.MutationStartDifferentConfig)
		if err != nil {
			a.Log.Warn("headroom", "恢复 codex 全局上下文压缩被共享服务门禁拒绝", err.Error())
			return
		}
		defer a.sharedCoord.ReleaseMutationAdmission(admission)
	}

	if !a.CodexHeadroom.IsRunning() {
		if err := a.CodexHeadroom.StartForOpenAI(target); err != nil {
			a.Log.Warn("headroom", "启动时恢复 codex 全局 headroom 失败", err.Error())
			return
		}
	}
	if err := syncCodexGlobalHeadroomConfig(true, port); err != nil {
		a.Log.Warn("headroom", "启动时校验 codex 全局 openai_base_url 失败", err.Error())
		return
	}
	a.Log.Info("headroom", "codex 全局 headroom 已在启动时恢复",
		fmt.Sprintf("port=%d target=%s", port, target))
}
