package main

// app_envcheck_facade.go — EnvCheck, Claude install/config fix, and WSL CLI/DockerWSL
// App facades.

import (
	"context"
	"fmt"
	"strings"

	"amagi-codebox/internal/dockerwsl"
	"amagi-codebox/internal/envcheck"
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
	if action == string(envcheck.SolutionFixClaudeConfig) {
		a.sharedMcpWrites.Lock()
		defer a.sharedMcpWrites.Unlock()
	}
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

// CheckClaudeConfig scans Claude Code configuration files and reports
// which configuration items are present or missing.
func (a *App) CheckClaudeConfig() (*envcheck.ClaudeConfigStatus, error) {
	return a.EnvCheck.CheckClaudeConfig()
}

// FixClaudeConfig writes a single configuration item to Claude Code settings.
// Only missing items are added; existing values are never overwritten.
func (a *App) FixClaudeConfig(key string, value string, filePath string) (*envcheck.ConfigFixResult, error) {
	a.sharedMcpWrites.Lock()
	defer a.sharedMcpWrites.Unlock()
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
	default:
		return "", fmt.Errorf("unknown CLI tool: %s", tool)
	}
}
