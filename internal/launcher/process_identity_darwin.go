//go:build darwin

package launcher

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func inspectExternalProcess(pid int) (string, bool, error) {
	if pid <= 0 {
		return "", false, nil
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		// kern.proc.pid returns EIO (rather than ESRCH) after the process has
		// disappeared on current macOS releases.
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EIO) {
			return "", false, nil
		}
		return "", true, fmt.Errorf("inspect process %d: %w", pid, err)
	}
	if info == nil || info.Proc.P_pid != int32(pid) {
		return "", false, nil
	}
	started := info.Proc.P_starttime
	return fmt.Sprintf("darwin:%d:%d", started.Sec, started.Usec), true, nil
}

func terminateRecoveredExternalProcess(pid int, expectedIdentity string) (bool, error) {
	identity, running, inspectErr := inspectExternalProcess(pid)
	terminal, signal, err := classifyRecoveredExternalProcess(expectedIdentity, identity, running, inspectErr)
	if err != nil {
		return false, err
	}
	if terminal || !signal {
		// The original identity is terminal, or its schema is not signal-safe.
		return terminal, nil
	}
	if err := unix.Kill(pid, unix.SIGKILL); err != nil {
		if errors.Is(err, unix.ESRCH) {
			return true, nil
		}
		return false, fmt.Errorf("kill recovered process %d: %w", pid, err)
	}
	return false, nil // terminal is confirmed by a later identity inspection
}
