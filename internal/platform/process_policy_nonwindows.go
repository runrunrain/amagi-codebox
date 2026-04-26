//go:build !windows

package platform

import "os/exec"

func applyProcessPolicy(cmd *exec.Cmd, policy ProcessPolicy) {
	_ = cmd
	_ = policy
}
