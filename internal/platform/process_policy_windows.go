//go:build windows

package platform

import (
	"os/exec"
	"syscall"
)

// applyProcessPolicy 把 ProcessPolicy 翻译为 Windows SysProcAttr。
// 标志互斥与优先级决策见 process_policy.go 中 processPolicyCreationFlags。
func applyProcessPolicy(cmd *exec.Cmd, policy ProcessPolicy) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    processPolicyHideWindow(policy),
		CreationFlags: processPolicyCreationFlags(policy),
	}
}

// SuppressConsoleWindow 为不经 ProcessRunner 的裸 exec.Command 调用点统一
// 抑制子进程控制台窗口闪现（等价 DefaultProcessPolicy：HideWindow +
// CREATE_NO_WINDOW）。会整体覆盖 cmd.SysProcAttr，仅供未自定义 SysProcAttr
// 的调用点使用；非 Windows 平台为 no-op（见 process_policy_nonwindows.go），
// 跨平台源文件可无条件调用。
func SuppressConsoleWindow(cmd *exec.Cmd) {
	applyProcessPolicy(cmd, DefaultProcessPolicy())
}
