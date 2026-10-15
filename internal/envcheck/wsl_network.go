package envcheck

import (
	"amagi-codebox/internal/platform"
)

// WSL 网络模式信息条目（P2-T1）：Windows 上内嵌会话默认落在 WSL（resolver 的
// BootstrapWSL 分支），而 AI 代理分流、Headroom、系统代理的统一入口都监听宿主
// 回环（127.0.0.1）。WSL2 NAT 模式（默认）下 distro 内的 127.0.0.1 指向发行版
// 自身，宿主回环不可达；mirrored 模式下 localhost 双向互通，上述功能对 WSL 会
// 话自然可用。本文件在 envcheck 收尾时读取 %USERPROFILE%\.wslconfig 的
// networkingMode（platform.WSLNetworkingMode，进程级缓存、无 wsl.exe fork），
// 向 CheckStatus.Issues 追加一条信息级条目。severity-only：与 wsl_hybrid.go 的
// warning 注入同模式，不影响 Installed/PATHOk/Error 判定，故不会翻转
// OverallStatus.AllOK；前端 issue 渲染通用（severity 徽标 + message + detail），
// 无需前端改动。

// wslNetworkingMode resolves the current WSL2 networking mode for the info
// card. Package var so tests on any OS can inject a mode without planting a
// real .wslconfig (mirrors wslHybridDistro).
var wslNetworkingMode = func() string {
	return platform.WSLNetworkingMode(nil)
}

// wslNetworkingModeSwitchGuide mirrors the aiproxy WSL gate's rejection text
// (C5：中文、可操作) so both surfaces give the same fix instructions.
const wslNetworkingModeSwitchGuide = `可在 %USERPROFILE%\.wslconfig 的 [wsl2] 段设置 networkingMode=mirrored，执行 wsl --shutdown 重启 WSL 后生效`

// appendWSLNetworkingModeInfo appends one info issue describing the current
// WSL2 networking mode when WSL is a live launch path on this host (Windows +
// at least one usable distro). No-op otherwise — the card carries no signal on
// hosts where no session can land in WSL.
func (s *Service) appendWSLNetworkingModeInfo(status *CheckStatus) {
	if status == nil {
		return
	}
	if wslSessionCommandFor(status.Tool) == "" {
		return // 该工具不会在 WSL 内启动（如 Headroom），不产出条目
	}
	if runtimeGOOS != "windows" {
		return
	}
	if wslHybridDistro() == "" {
		return
	}
	var message, detail string
	switch mode := wslNetworkingMode(); mode {
	case "mirrored":
		message = "WSL 网络模式：mirrored"
		detail = "localhost 与 Windows 宿主双向互通，AI 代理分流与 Headroom 对 WSL 会话可用。"
	case "nat":
		message = "WSL 网络模式：NAT"
		detail = "发行版内访问不到宿主回环（127.0.0.1），AI 代理分流与 Headroom 对 WSL 会话不可用。" + wslNetworkingModeSwitchGuide + "。"
	default:
		message = "WSL 网络模式：未知（按 NAT 处理）"
		detail = "未检测到 %USERPROFILE%\\.wslconfig 的 networkingMode 配置，按默认 NAT 对待：AI 代理分流与 Headroom 对 WSL 会话不可用。" + wslNetworkingModeSwitchGuide + "。"
	}
	status.Issues = append(status.Issues, CheckIssue{
		Severity: SeverityInfo,
		Code:     "wsl_networking_mode",
		Message:  message,
		Detail:   detail,
	})
}
