package main

// app_agentcli_install.go — agent-CLI 本机分发面（shim 与 agent 技能）的 App
// 接线与前端门面（DESIGN.md §5 C1/C2）。安装机制本体在 internal/agentcli/install
// （幂等尾标记、冲突保护、用户清理偏好）；本文件只负责：
//   - 持有 Manager（home=配置根、userHome、exePath=当前可执行文件真实路径、
//     table=cmds.Table()）并在 Startup 默认安装（失败只记日志不阻断启动）；
//   - Wails 门面方法（状态查询/安装/清理），全部不含令牌。
//
// 秘密边界与 app_agentcli.go 一致：任何返回值、日志与错误信息都不含令牌；
// 管理面只在桌面 UI/Wails 绑定。

import (
	"fmt"
	"os"
	"path/filepath"

	"amagi-codebox/internal/agentcli/cmds"
	"amagi-codebox/internal/agentcli/install"
)

// AgentCLISkillInstallStatus 是单个技能目标的安装状态。
type AgentCLISkillInstallStatus struct {
	Target string `json:"target"` // agents / claude / codex
	Path   string `json:"path"`
	Status string `json:"status"` // install.State：not-installed/up-to-date/outdated/modified-by-user/invalid
}

// AgentCLIInstallStatus 是安装面状态快照（不含令牌）。
type AgentCLIInstallStatus struct {
	ShimPath     string                      `json:"shimPath"`
	ShimStatus   string                      `json:"shimStatus"`
	Skills       []AgentCLISkillInstallStatus `json:"skills"`
	CommandCount int                         `json:"commandCount"`
	Command      string                      `json:"command"` // shim 调用名（codebox-agent，Windows 为 codebox-agent.cmd）
}

// constructAgentCLIInstall 在 NewApp 构造安装管理器（与 constructAgentCLI 同
// 时机）。纯构造不做文件 IO。
func (a *App) constructAgentCLIInstall() {
	if a.agentCLIInstall != nil {
		return
	}
	a.agentCLIInstall = buildAgentCLIInstallManager(a)
}

// buildAgentCLIInstallManager 构造安装管理器：home=应用配置根（生产
// ~/.amagi-codebox，测试夹具可注入 configDir）、userHome=用户主目录（技能
// 目标所在）、exePath=当前可执行文件真实路径（os.Executable 经
// EvalSymlinks，shim exec 指向它）、table=cmds.Table()（技能
// references/commands.md 的渲染源）。
func buildAgentCLIInstallManager(a *App) *install.Manager {
	home := defaultConfigDir()
	if a != nil && a.configDir != "" {
		home = a.configDir
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		// 降级：技能目标路径不可用时 SkillStatus/InstallSkill 会以 invalid
		// 如实上报，shim（在 home 下）不受影响。
		userHome = home
	}
	return install.New(home, userHome, currentExecutablePath(), cmds.Table())
}

// agentCLIInstaller 返回安装管理器。生产由 NewApp 构造；未构造的 App（测试
// 夹具直接拼 struct）现场兜底构造，不写回字段（避免与生产构造路径竞态）。
func (a *App) agentCLIInstaller() *install.Manager {
	if a != nil && a.agentCLIInstall != nil {
		return a.agentCLIInstall
	}
	return buildAgentCLIInstallManager(a)
}

// currentExecutablePath 返回当前可执行文件的绝对真实路径：os.Executable 经
// EvalSymlinks 解析符号链接（应用更新替换、macOS translocation 等场景下
// 保证 shim 指向真实文件）；解析失败回退原始路径。
func currentExecutablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		return resolved
	}
	return exe
}

// installAgentCLIDefaults 是 Startup 的默认安装（DESIGN.md §5 C2）：命令行
// 访问开启且用户未清理过时安装 shim 与 agents 目标技能；每次启动都会经
// 幂等安装刷新 shim 指向当前可执行文件（内容不一致即重写）。偏好文件损坏
// 或单项失败只经 onError 记日志，绝不阻断启动。
func (a *App) installAgentCLIDefaults() {
	enabled := true
	if a.Settings != nil {
		enabled = a.Settings.GetAgentCLIEnabled()
	}
	if !enabled {
		return
	}
	m := a.agentCLIInstaller()
	if m == nil {
		return
	}
	m.InstallDefaults(func(err error) {
		if a.Log != nil {
			a.Log.Warn("agent-cli", "默认安装 shim/技能失败（不影响应用启动）", err.Error())
		}
	})
}

// ---- Wails 门面 ----

// agentCLIInstallStatusView 从管理器构建安装面状态视图（不含令牌）。
func agentCLIInstallStatusView(m *install.Manager) AgentCLIInstallStatus {
	view := AgentCLIInstallStatus{
		ShimStatus:   string(install.StateNotInstalled),
		Skills:       []AgentCLISkillInstallStatus{},
		CommandCount: len(cmds.Table()),
	}
	if m == nil {
		return view
	}
	view.ShimPath = m.ShimPath()
	view.ShimStatus = string(m.ShimStatus().State)
	view.Command = filepath.Base(m.ShimPath())
	for _, t := range install.SkillTargets {
		st := m.SkillStatus(t)
		view.Skills = append(view.Skills, AgentCLISkillInstallStatus{
			Target: string(t),
			Path:   st.Path,
			Status: string(st.State),
		})
	}
	return view
}

// parseAgentCLISkillTarget 校验技能目标参数（agents|claude|codex）。
func parseAgentCLISkillTarget(target string) (install.SkillTarget, error) {
	switch install.SkillTarget(target) {
	case install.SkillTargetAgents, install.SkillTargetClaude, install.SkillTargetCodex:
		return install.SkillTarget(target), nil
	}
	return "", fmt.Errorf("未知技能目标 %q（合法值：agents|claude|codex）", target)
}

// GetAgentCLIInstallStatus 返回本机分发状态快照：shim 路径/状态、三个技能
// 目标路径/状态、命令数与 shim 调用名（不含令牌）。
func (a *App) GetAgentCLIInstallStatus() AgentCLIInstallStatus {
	return agentCLIInstallStatusView(a.agentCLIInstaller())
}

// InstallAgentCLIShim 显式安装 shim（幂等；文件被外部改动时报冲突并保留
// 原文件），返回更新后的状态。成功会清除用户清理偏好（恢复启动默认安装）。
func (a *App) InstallAgentCLIShim() (AgentCLIInstallStatus, error) {
	m := a.agentCLIInstaller()
	err := m.InstallShim()
	return agentCLIInstallStatusView(m), err
}

// RemoveAgentCLIShim 清理 shim（外部改动的文件保留），并记录用户清理偏好：
// 应用启动不再自动装回。返回更新后的状态。
func (a *App) RemoveAgentCLIShim() (AgentCLIInstallStatus, error) {
	m := a.agentCLIInstaller()
	err := m.RemoveShim()
	return agentCLIInstallStatusView(m), err
}

// InstallAgentCLISkill 安装指定目标的 agent 技能（target ∈ agents|claude|codex），
// 返回更新后的状态。成功会清除该目标的用户清理偏好。
func (a *App) InstallAgentCLISkill(target string) (AgentCLIInstallStatus, error) {
	m := a.agentCLIInstaller()
	t, terr := parseAgentCLISkillTarget(target)
	if terr != nil {
		return agentCLIInstallStatusView(m), terr
	}
	err := m.InstallSkill(t)
	return agentCLIInstallStatusView(m), err
}

// RemoveAgentCLISkill 清理指定目标的 agent 技能（外部改动的文件保留），并
// 记录用户清理偏好。返回更新后的状态。
func (a *App) RemoveAgentCLISkill(target string) (AgentCLIInstallStatus, error) {
	m := a.agentCLIInstaller()
	t, terr := parseAgentCLISkillTarget(target)
	if terr != nil {
		return agentCLIInstallStatusView(m), terr
	}
	err := m.RemoveSkill(t)
	return agentCLIInstallStatusView(m), err
}
