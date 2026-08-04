//go:build !darwin && !windows

package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const procFSBootIDPath = "/proc/sys/kernel/random/boot_id"

func inspectExternalProcess(pid int) (string, bool, error) {
	if pid <= 0 {
		return "", false, nil
	}
	bootID, err := os.ReadFile(procFSBootIDPath)
	if err != nil {
		// Without a boot generation even PID absence is not a durable identity
		// statement. Keep ownership fail-closed and do not authorize a signal.
		return "", true, fmt.Errorf("inspect process %d boot identity: %w", pid, err)
	}
	data, err := os.ReadFile(filepath.Join("/proc", fmt.Sprintf("%d", pid), "stat"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", true, fmt.Errorf("inspect process %d: %w", pid, err)
	}
	identity, err := procFSProcessIdentity(string(bootID), data)
	if err != nil {
		return "", true, fmt.Errorf("inspect process %d: %w", pid, err)
	}
	return identity, true, nil
}

func terminateRecoveredExternalProcess(pid int, expectedIdentity string) (bool, error) {
	identity, running, inspectErr := inspectExternalProcess(pid)
	terminal, signal, err := classifyRecoveredExternalProcess(expectedIdentity, identity, running, inspectErr)
	if err != nil {
		return false, err
	}
	if terminal || !signal {
		return terminal, nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("find recovered process %d: %w", pid, err)
	}
	defer process.Release()
	if err := process.Kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return true, nil
		}
		return false, fmt.Errorf("kill recovered process %d: %w", pid, err)
	}
	return false, nil
}
