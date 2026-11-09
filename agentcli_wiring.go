package main

// agentcli_wiring.go — agent-CLI 服务端的 App 生命周期接线（DESIGN.md §5 A1，
// 最小侵入）：NewApp 构造 Server、Startup 按设置启动、Shutdown 停止并撤销
// 端点文件。前端门面见 app_agentcli.go；命令 Handler 见 agentcli_h_*.go。

import (
	"amagi-codebox/internal/agentcli"
	"amagi-codebox/internal/agentcli/cmds"
)

// constructAgentCLI 在 NewApp 构造 App 后调用（Handler 闭包引用 app，须在
// App 字面量装配完成后创建）。命令表 ↔ Handler 1:1 由
// TestAgentCLITableHandlersParity 锚定。
func (a *App) constructAgentCLI() {
	if a.agentCLI != nil {
		return
	}
	a.agentCLI = agentcli.NewServer(
		a.configDir,
		resolveAppVersion(),
		cmds.Table(),
		newAgentCLIHost(a).handlers(),
		a.Log, // *logging.Service 直接满足 agentcli.Logger
	)
}

// startAgentCLI 在 Startup 按设置启动服务（DESIGN.md §3.7）：默认 true（老
// 配置无键即开启）；关闭时不监听，仅清理陈旧端点文件（崩溃残留），让 CLI
// 客户端拿到"应用未运行"的不可达语义。启动失败记警告不阻断应用主流程。
func (a *App) startAgentCLI() {
	if a.agentCLI == nil {
		return
	}
	enabled := true
	if a.Settings != nil {
		enabled = a.Settings.GetAgentCLIEnabled()
	}
	if !enabled {
		agentcli.CleanStaleEndpoint(a.configDir)
		if a.Log != nil {
			a.Log.Info("agent-cli", "命令行访问已关闭（按设置不启动）")
		}
		return
	}
	if err := a.agentCLI.Start(); err != nil {
		if a.Log != nil {
			a.Log.Warn("agent-cli", "agent-CLI 服务启动失败", err.Error())
		}
	}
}

// stopAgentCLI 在 Shutdown 停止服务：关闭监听、令牌作废、撤销端点文件
// （应用正常退出删除 endpoint.json，DESIGN.md §3.1）。
func (a *App) stopAgentCLI() {
	if a.agentCLI == nil {
		return
	}
	a.agentCLI.Stop()
}
