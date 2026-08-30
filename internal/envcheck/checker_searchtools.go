package envcheck

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"amagi-codebox/internal/platform"
)

// pi/omp 搜索工具（fd/ripgrep）环境检测（产品化自 2026-08 Leader 在用户 WSL 的
// 手动 apt 安装）：CodeBox 向 pi/omp 会话注入 PI_OFFLINE=1，pi 不会自动下载
// fd/rg；WSL 模式下 pi 在 distro 内运行，distro 缺 fd/fdfind/rg 时启动打印两行
// Warning 且文件搜索能力降级。本文件在 checkPi/checkOmp 收尾时探测两侧：
//
//   - WSL 侧（Windows 主机且有可用 distro）：platform.WSLSearchToolStatus
//     （包级缓存，一次 fork wsl.exe）；缺失 → warning issue + 一键 apt 安装
//     （SolutionInstallWslSearchTools，经注入回调由 CodeBox 以 root 执行）
//     + manual_command 兜底。
//   - Windows 原生侧（无可用 distro，pi 必然原生运行）：exec.LookPath 探测
//     fd/rg，另检查 pi/omp 自管工具目录 ~/.pi|omp/agent/bin（pi 在线时曾把
//     fd/rg 下载到那里）。缺失 → warning issue + winget manual_command。
//     不做 Windows 自动安装：winget 触发 UAC/源协议交互且自身可能缺失，
//     复杂度超出本切片范围，只给手动指引。
//
// 两侧互斥（WSL 优先）：Windows 上会话默认内嵌 WSL 运行（resolver 的 WSL
// 分支是默认路径），distro 可用时原生侧缺失不构成会话实际遇到的问题，补一份
// issue 只会制造噪音。仅在 status.Installed 时检测——工具本体没装时 fd/rg
// issue 是噪音。

// wslSearchToolStatusProbe / searchToolsDistroProbe 包级 var：生产分别指向
// platform 的缓存探测与默认 distro 解析；测试注入假实现（runtimeGOOS 亦可
// 模拟 windows），与 wsl_hybrid.go 的 wslHybridDistro 同一模式。
var (
	wslSearchToolStatusProbe = platform.WSLSearchToolStatus
	searchToolsDistroProbe   = func() string { return platform.DefaultWSLDistro(nil) }
)

// appendSearchToolsIssues 探测 fd/ripgrep 并在缺失时向 status.Issues 追加
// warning（WSL 侧或 Windows 原生侧，见文件头注释）。仅在 pi/omp 已安装时生效。
func (s *Service) appendSearchToolsIssues(status *CheckStatus) {
	if status == nil || !status.Installed {
		return
	}
	switch status.Tool {
	case ToolPi, ToolOmp:
	default:
		return
	}
	if runtimeGOOS != "windows" {
		return
	}
	if distro := searchToolsDistroProbe(); distro != "" {
		appendWSLSearchToolsIssue(status, distro)
		return
	}
	appendNativeSearchToolsIssue(status)
}

// appendWSLSearchToolsIssue 在 distro 内 fd/fdfind 或 ripgrep 缺失时追加
// warning issue。探测结果由 platform 按 distro 缓存（首次 fork wsl.exe），
// 一次 CheckAll 的 pi+omp 两次检查只探测一次。
func appendWSLSearchToolsIssue(status *CheckStatus, distro string) {
	tools := wslSearchToolStatusProbe(distro)
	if tools.FD && tools.Ripgrep {
		return
	}
	missing := wslMissingSearchToolNames(tools)
	status.Issues = append(status.Issues, CheckIssue{
		Severity: SeverityWarning,
		Code:     fmt.Sprintf("%s_wsl_missing_search_tools", status.Tool),
		Message:  "WSL 内缺少 fd/ripgrep，pi/omp 的文件搜索能力受限（PI_OFFLINE 下不会自动下载）",
		Detail: fmt.Sprintf("发行版 %s 缺少：%s。CodeBox 向 pi/omp 会话注入 PI_OFFLINE=1，pi 不会自动下载搜索工具；缺失时启动打印 Warning 且文件搜索降级。",
			distro, strings.Join(missing, "、")),
		Solutions: []ResolutionAction{
			{
				Type:            SolutionInstallWslSearchTools,
				Description:     "在 WSL 内安装 fd-find 与 ripgrep（apt，需 sudo，由 CodeBox 以 root 执行）",
				Tool:            status.Tool,
				RequiresConfirm: true,
				IsPrimary:       true,
			},
			{
				Type:        SolutionManualCommand,
				Command:     "sudo apt-get install -y fd-find ripgrep",
				Description: "在 WSL 终端内手动安装",
				Tool:        status.Tool,
			},
		},
	})
}

// wslMissingSearchToolNames 把缺失的探测位转成展示名（fd 覆盖 fd/fdfind 两个
// 二进制名，Debian/Ubuntu 的包名是 fd-find）。
func wslMissingSearchToolNames(tools platform.WSLSearchTools) []string {
	missing := make([]string, 0, 2)
	if !tools.FD {
		missing = append(missing, "fd")
	}
	if !tools.Ripgrep {
		missing = append(missing, "ripgrep")
	}
	return missing
}

// nativeSearchToolBinaryNames 是每个工具的候选文件名：LookPath 在 Windows 上
// 对无后缀名也会按 PATHEXT 命中 fd.exe/rg.exe，但显式列出 .exe 使语义不依赖
// PATHEXT（也覆盖非 Windows 宿主上同逻辑的单测）。
var nativeSearchToolBinaryNames = map[string][]string{
	"fd": {"fd", "fd.exe"},
	"rg": {"rg", "rg.exe"},
}

// appendNativeSearchToolsIssue 在 Windows 原生运行场景（无可用 WSL distro）
// 下检测 PATH 与 pi/omp 自管工具目录（~/.pi/agent/bin、~/.omp/agent/bin）中的
// fd/rg，缺失时追加 warning issue + winget 手动指引。
// 不做 Windows 自动安装（winget/UAC 复杂度超范围，理由见文件头注释）。
func appendNativeSearchToolsIssue(status *CheckStatus) {
	fd, rg := nativeSearchToolsPresent(status.Tool)
	if fd && rg {
		return
	}
	missing := make([]string, 0, 2)
	if !fd {
		missing = append(missing, "fd")
	}
	if !rg {
		missing = append(missing, "ripgrep")
	}
	status.Issues = append(status.Issues, CheckIssue{
		Severity: SeverityWarning,
		Code:     fmt.Sprintf("%s_missing_search_tools_native", status.Tool),
		Message:  "Windows 原生环境缺少 fd/ripgrep，pi/omp 的文件搜索能力受限（PI_OFFLINE 下不会自动下载）",
		Detail: fmt.Sprintf("PATH 与 %s 均未找到：%s。pi 在线时会自动把搜索工具下载到自管目录；PI_OFFLINE=1 下不会下载。",
			nativeSearchToolsBinDirLabel(status.Tool), strings.Join(missing, "、")),
		Solutions: []ResolutionAction{
			{
				Type:        SolutionManualCommand,
				Command:     "winget install sharkdp.fd BurntSushi.ripgrep.MSVC",
				Description: "在 Windows 终端（PowerShell/CMD）内通过 winget 手动安装",
				Tool:        status.Tool,
				IsPrimary:   true,
			},
		},
	})
}

// nativeSearchToolsPresent 报告 Windows 原生侧 fd 与 rg 是否可用：先查进程
// PATH（LookPath），再查该工具自管的 agent bin 目录（~/.pi/agent/bin 或
// ~/.omp/agent/bin，pi 曾在线下载时会有）。
func nativeSearchToolsPresent(tool CLITool) (fd, rg bool) {
	fd = nativeLookPathAny(nativeSearchToolBinaryNames["fd"])
	rg = nativeLookPathAny(nativeSearchToolBinaryNames["rg"])
	if fd && rg {
		return fd, rg
	}
	if binDir := nativeAgentBinDir(tool); binDir != "" {
		if !fd {
			fd = anyFileExists(binDir, nativeSearchToolBinaryNames["fd"])
		}
		if !rg {
			rg = anyFileExists(binDir, nativeSearchToolBinaryNames["rg"])
		}
	}
	return fd, rg
}

// nativeLookPathAny 报告任一候选名能否被 LookPath 命中。
func nativeLookPathAny(names []string) bool {
	for _, name := range names {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

// anyFileExists 报告 dir 下是否存在任一候选文件。
func anyFileExists(dir string, names []string) bool {
	for _, name := range names {
		if fileExists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

// nativeAgentBinDir 返回 tool 自管的 agent bin 目录（Windows 原生路径），
// home 解析失败时返回 ""。
func nativeAgentBinDir(tool CLITool) string {
	root := ".pi"
	if tool == ToolOmp {
		root = ".omp"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, root, "agent", "bin")
}

// nativeSearchToolsBinDirLabel 是 Detail 里展示的自管目录文案。
func nativeSearchToolsBinDirLabel(tool CLITool) string {
	if dir := nativeAgentBinDir(tool); dir != "" {
		return dir
	}
	if tool == ToolOmp {
		return `~/.omp/agent/bin`
	}
	return `~/.pi/agent/bin`
}
