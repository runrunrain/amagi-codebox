//go:build !windows

package platform

import "os/exec"

func applyProcessPolicy(cmd *exec.Cmd, policy ProcessPolicy) {
	_ = cmd
	_ = policy
}

// SuppressConsoleWindow 非 Windows 平台 no-op（Windows 实现在
// process_policy_windows.go），供跨平台源文件无条件调用。
func SuppressConsoleWindow(cmd *exec.Cmd) {
	_ = cmd
}
