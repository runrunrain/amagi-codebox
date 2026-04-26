//go:build windows

package updater

import (
	"os/exec"
	"path/filepath"
	"syscall"
)

func startUpdatedExecutable(exePath string) error {
	cmd := exec.Command(exePath)
	cmd.Dir = filepath.Dir(exePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000010}
	return cmd.Start()
}
