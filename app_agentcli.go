package main

// app_agentcli.go — agent-CLI 的前端门面（Wails 绑定面，DESIGN.md §5 A1）。
//
// 秘密边界：CLI 令牌只存在于 0600 端点文件与请求头中，任何门面返回值、
// 日志与错误信息都不含令牌（对齐 feishu-sync CliAccessSettingsCard 的
// 头注约定）。管理面（开关/轮换）只在桌面 UI/Wails 绑定，不经 CLI 自身
//（DESIGN.md §3.5：CLI 不能自我提权）。

import "errors"

// AgentCLIStatus 是 GetAgentCLIStatus 的返回结构（不含令牌；URL 本身不含
// 令牌）。
type AgentCLIStatus struct {
	Enabled      bool   `json:"enabled"`
	Running      bool   `json:"running"`
	URL          string `json:"url"` // 形如 http://127.0.0.1:53211；未运行为空
	EndpointPath string `json:"endpointPath"`
	PID          int    `json:"pid"`
	StartedAt    string `json:"startedAt"` // RFC3339；未运行为空
	LastError    string `json:"lastError"` // 最近一次启动/发布失败原因（如端点被其它实例占用）；空=无异常
}

// GetAgentCLIStatus 返回命令行访问状态快照（不含令牌）。
func (a *App) GetAgentCLIStatus() AgentCLIStatus {
	enabled := true
	if a.Settings != nil {
		enabled = a.Settings.GetAgentCLIEnabled()
	}
	if a.agentCLI == nil {
		return AgentCLIStatus{Enabled: enabled}
	}
	st := a.agentCLI.Status()
	return AgentCLIStatus{
		Enabled:      enabled,
		Running:      st.Running,
		URL:          st.URL,
		EndpointPath: st.EndpointPath,
		PID:          st.PID,
		StartedAt:    st.StartedAt,
		LastError:    st.LastError,
	}
}

// SetAgentCLIEnabled 开关命令行访问：先持久化设置（真值），再联动运行态
// （开启 = 启动/恢复 + 发布端点；关闭 = 令牌作废 + 撤销端点文件，监听保留
// 并对请求返回 503 cli_disabled，DESIGN.md §3.7）。运行态联动失败时设置已
// 持久化，下次启动按新值恢复；错误如实返回给前端。
func (a *App) SetAgentCLIEnabled(enabled bool) error {
	if a.Settings == nil {
		return errors.New("settings service unavailable")
	}
	if err := a.Settings.SetAgentCLIEnabled(enabled); err != nil {
		return err
	}
	if a.agentCLI != nil {
		return a.agentCLI.SetEnabled(enabled)
	}
	return nil
}

// RotateAgentCLIToken 轮换 CLI 令牌：旧令牌立即失效并重写端点文件。仅在
// 服务运行且启用时可用；未运行/已禁用返回错误。
func (a *App) RotateAgentCLIToken() error {
	if a.agentCLI == nil {
		return errors.New("agent CLI service unavailable")
	}
	return a.agentCLI.RotateToken()
}
