package main

// ListCodexAdvisorChats 仅探测本机桌面共享连接并读取最近会话，不会发送咨询。
func (a *App) ListCodexAdvisorChats(socketPath string) (string, error) {
	return a.PiConfig.ListCodexAdvisorChats(socketPath)
}
