package main

import "amagi-codebox/internal/agentcli/spec"

// agentCLIHost 把 agent-CLI 命令表里的每条命令绑定到 App 能力。
// 各领域 Handler 分文件（agentcli_h_<domain>.go），键 = spec.Command.Name()（点号形态）。
// root 测试断言 cmds.Table() 与 handlers() 1:1。
type agentCLIHost struct {
	app *App
}

func newAgentCLIHost(app *App) *agentCLIHost { return &agentCLIHost{app: app} }

func (h *agentCLIHost) handlers() map[string]spec.Handler {
	all := map[string]spec.Handler{}
	for _, group := range []map[string]spec.Handler{
		h.systemHandlers(),
		h.configHandlers(),
		h.sessionHandlers(),
		h.usageHandlers(),
		h.pluginHandlers(),
		h.gitHandlers(),
	} {
		for k, v := range group {
			all[k] = v
		}
	}
	return all
}
