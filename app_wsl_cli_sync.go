package main

// app_wsl_cli_sync.go — P2-C 安装/升级双侧联动（Windows 统一入口）：
//
//   - App.syncCLIToWSL 是注入 envcheck 的 WSL 同步回调（app.go 装配处一行
//     SetWSLCLISync）。Windows 侧 install/update 成功后由 attachWSLSync 在
//     同一操作内调用；envcheck 不直接依赖 wslsetup（回调注入模式，与
//     SetWSLSearchToolsInstaller 同构）。
//   - 门控（direction §2.3）：仅现有有效 DefaultWSLDistro（空 = 本次不涉及
//     WSL，返回 nil）；该 CLI 的会话 shell 设置为 wsl，或 WSL 内已经原生安装，
//     满足其一才同步，否则返回 skipped；不新增设置项、不操作全部发行版。
//   - 分派：install 且 WSL 未装 → wslsetup.InstallTool（保留 AlreadyOK 幂等
//     语义）；install 且已装、或 update → wslsetup.UpdateTool（P2-A：npm
//     i -g <pkg>@latest，不短路）。
//   - App.UpdateCLIInWSL 是「WSL 内的 CLI」卡片的显式升级绑定（替代名不副实
//     的「重新安装」按钮；按钮与前端接线归 Leader 集成）。
//
// 结果语义：InstallResult.Success / OperationState.Status 仍只表示 Windows
// 侧；WSL 侧结论单独装在 WSLSyncResult 里（succeeded/failed/skipped），
// 部分失败不得伪报全成功（前端横幅按 wsl.status 降级为 warning）。

import (
	"fmt"
	"strings"

	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/wslsetup"
)

// wslCLISetupService 是 App 层触达 wslsetup 的最小面。生产实现是已绑定的
// *wslsetup.Service；根级测试经 wslCLISetupFor 接缝注入假实现（本文件内
// 定义接口，避免给 App 结构体加字段）。
type wslCLISetupService interface {
	GetStatus() wslsetup.Status
	InstallTool(tool string) (*wslsetup.InstallResult, error)
	UpdateTool(tool string) (*wslsetup.InstallResult, error)
}

// wslCLISetupFor 解析当前 App 使用的 wslsetup 服务（nil 安全：WSLSetup 未
// 装配时返回 nil）。包级接缝，镜像 wslDefaultDistroFn 的测试替换模式。
var wslCLISetupFor = func(a *App) wslCLISetupService {
	if a == nil || a.WSLSetup == nil {
		return nil
	}
	return a.WSLSetup
}

// wslToolKeyFor 把 envcheck 的 CLITool 映射为 wslsetup 的规范化工具键
// （GetStatus().Tools 里的 key；claude_code → claude）。
func wslToolKeyFor(tool envcheck.CLITool) (string, bool) {
	switch tool {
	case envcheck.ToolClaudeCode:
		return "claude", true
	case envcheck.ToolOpenCode:
		return "opencode", true
	case envcheck.ToolCodex:
		return "codex", true
	case envcheck.ToolPi:
		return "pi", true
	case envcheck.ToolOmp:
		return "omp", true
	default:
		return "", false
	}
}

// wslShellSettingFor 返回该 CLI 的嵌入式会话 shell 设置（settings.json
// Dashboard.ClaudeShell/OpenCodeShell/CodexShell/PiShell/OmpShell）。空值
// （无设置面/未知工具）按「未使用 WSL 模式」处理。
// 包级接缝（镜像 wslDefaultDistroFn）：settings 持久层在非 Windows 会把
// "wsl" 键归一化掉（sanitizeShellKeyForOS），macOS 测试无法经真实服务呈现
// wsl shell；接缝让门控分支跨平台可测。
var wslShellSettingFor = func(a *App, tool envcheck.CLITool) string {
	if a == nil || a.Settings == nil {
		return ""
	}
	st := a.Settings.GetSettings()
	if st == nil {
		return ""
	}
	switch tool {
	case envcheck.ToolClaudeCode:
		return st.Dashboard.ClaudeShell
	case envcheck.ToolOpenCode:
		return st.Dashboard.OpenCodeShell
	case envcheck.ToolCodex:
		return st.Dashboard.CodexShell
	case envcheck.ToolPi:
		return st.Dashboard.PiShell
	case envcheck.ToolOmp:
		return st.Dashboard.OmpShell
	default:
		return ""
	}
}

// wslToolInstalledIn 报告 key 在 WSL 状态快照内是否已原生安装。
func wslToolInstalledIn(st wslsetup.Status, key string) bool {
	for _, ts := range st.Tools {
		if ts.Tool == key {
			return ts.Installed
		}
	}
	return false
}

// syncCLIToWSL 是注入 envcheck 的安装/升级双侧联动回调。返回 nil 表示
// 「本次不涉及 WSL」（非 Windows、无有效默认发行版、非五 CLI 工具或非
// install/update）——envcheck 据此保持 result.WSL=nil，前端表现与联动
// 存在之前完全一致。其余情况返回 succeeded/failed/skipped 之一的真实结论。
func (a *App) syncCLIToWSL(tool envcheck.CLITool, kind envcheck.OperationKind) *envcheck.WSLSyncResult {
	if kind != envcheck.OperationKindInstall && kind != envcheck.OperationKindUpdate {
		return nil
	}
	key, ok := wslToolKeyFor(tool)
	if !ok {
		return nil
	}
	setup := wslCLISetupFor(a)
	if setup == nil {
		return nil
	}
	// 仅现有有效 DefaultWSLDistro（与 wslsetup/供给推送同一目标语义）；
	// 空串 = 非 Windows 或无可用发行版 → 不出 WSL 行，无需 GOOS 分支。
	distro := wslDefaultDistroFn(nil)
	if distro == "" {
		return nil
	}

	usesWSLShell := strings.EqualFold(strings.TrimSpace(wslShellSettingFor(a, tool)), "wsl")
	// 门控只用「可用且与当前目标同一发行版」的快照判已装（steer 抽查 #2）：
	// GetStatus 不可用（探测失败/非 Windows）或返回了其它发行版的快照时，
	// 其 installed 位不可信，按未装处理（保守方向：误判未装只会触发幂等的
	// InstallTool AlreadyOK 短路或无害的 UpdateTool，不会误跳过已装工具的
	// 升级之外的风险；wslsetup 内部会自行重解析目标并按 distro 加锁）。
	st := setup.GetStatus()
	installed := false
	if st.Available && st.Distro == distro {
		installed = wslToolInstalledIn(st, key)
	}
	if !usesWSLShell && !installed {
		// 未使用 WSL 的用户不会被塞进 Node 和 CLI：明确 skip 而不是静默 nil，
		// 让「为什么没同步」在结果里可观察。
		return &envcheck.WSLSyncResult{
			Status:  envcheck.WSLSyncSkipped,
			Distro:  distro,
			Message: "该 CLI 未使用 WSL 模式且 WSL 内未安装，已跳过同步",
		}
	}

	// install 且 WSL 未装 → InstallTool；install 且已装、或 update →
	// UpdateTool（@latest，未装时同一条命令会补装，调用方无需预判）。
	var res *wslsetup.InstallResult
	var err error
	if kind == envcheck.OperationKindInstall && !installed {
		res, err = setup.InstallTool(key)
	} else {
		res, err = setup.UpdateTool(key)
	}
	out := wslSyncResultFrom(distro, res, err)
	if a != nil && a.Log != nil {
		if out.Status == envcheck.WSLSyncFailed {
			a.Log.Warn("wslsetup", "CLI 同步到 WSL 失败", fmt.Sprintf("tool=%s kind=%s distro=%s", key, kind, distro), out.Error)
		} else {
			a.Log.Info("wslsetup", "CLI 同步到 WSL 完成", fmt.Sprintf("tool=%s kind=%s distro=%s", key, kind, distro), out.Status)
		}
	}
	return out
}

// wslSyncResultFrom 把 wslsetup 的安装/升级结果映射为 envcheck.WSLSyncResult。
// Log 只留在日志里，不进结果。错误语义（steer 抽查 #2）：err != nil 时无论
// res 是否非nil、是否 Success，一律强制 failed 并保留 err 原文（res.Error
// 不同文时追加），不允许「带 error 的成功」漏过门。
func wslSyncResultFrom(distro string, res *wslsetup.InstallResult, err error) *envcheck.WSLSyncResult {
	if err != nil {
		msg := err.Error()
		if res != nil && res.Error != "" && !strings.Contains(msg, res.Error) {
			msg = fmt.Sprintf("%s: %s", msg, res.Error)
		}
		return &envcheck.WSLSyncResult{
			Status: envcheck.WSLSyncFailed,
			Distro: distro,
			Error:  msg,
		}
	}
	if res == nil {
		return &envcheck.WSLSyncResult{
			Status: envcheck.WSLSyncFailed,
			Distro: distro,
			Error:  "WSL 同步未返回结果",
		}
	}
	out := &envcheck.WSLSyncResult{
		Status:    envcheck.WSLSyncFailed, // 默认失败；Success 才翻成功
		Distro:    distro,
		Version:   res.Version,
		AlreadyOK: res.AlreadyOK,
		Message:   res.Message,
		Error:     res.Error,
	}
	if res.Distro != "" {
		out.Distro = res.Distro
	}
	if res.Success {
		out.Status = envcheck.WSLSyncSucceeded
	}
	return out
}

// UpdateCLIInWSL 显式升级 WSL 内的指定 CLI（wslsetup.UpdateTool：npm
// i -g <pkg>@latest，不短路 AlreadyOK；未装时同命令会补装）。供「WSL 内的
// CLI」卡片的「升级」按钮使用（research #4：原「重新安装」按钮走
// InstallTool 对已装 CLI 短路 AlreadyOK，名不副实）。按钮与 api/wslsetup.ts
// 接线由 Leader 统一集成。
func (a *App) UpdateCLIInWSL(tool string) (*wslsetup.InstallResult, error) {
	if a == nil || a.WSLSetup == nil {
		return nil, fmt.Errorf("wslsetup 服务不可用")
	}
	if a.Log != nil {
		a.Log.Info("wslsetup", "WSL 内 CLI 升级请求", "tool="+tool)
	}
	return a.WSLSetup.UpdateTool(tool)
}
