//go:build windows

package updater

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// launchWindowsUpdateHelper starts the newly downloaded executable in a
// dedicated helper mode. The helper waits for the current process to exit,
// swaps the locked executable, restarts it, and rolls back on failure.
func launchWindowsUpdateHelper(stagedExePath string, currentExePath string, currentPID int) error {
	cmd := exec.Command(stagedExePath,
		windowsUpdateHelperFlag,
		strconv.Itoa(currentPID),
		currentExePath,
		stagedExePath,
	)
	cmd.Dir = filepath.Dir(stagedExePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000010}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start staged executable: %w", err)
	}
	return nil
}
