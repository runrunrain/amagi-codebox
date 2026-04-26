//go:build windows

package platform

import (
	"os/exec"
	"syscall"
)

func applyProcessPolicy(cmd *exec.Cmd, policy ProcessPolicy) {
	attr := &syscall.SysProcAttr{}
	if policy.HideConsoleWindow {
		attr.HideWindow = true
	}
	if policy.Detached {
		attr.CreationFlags = 0x00000010
	}
	cmd.SysProcAttr = attr
}
