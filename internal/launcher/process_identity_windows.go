//go:build windows

package launcher

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

func inspectExternalProcess(pid int) (string, bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_FOUND) {
			return "", false, nil
		}
		return "", true, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)
	return inspectWindowsProcessHandle(handle)
}

func inspectWindowsProcessHandle(handle windows.Handle) (string, bool, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", true, fmt.Errorf("get process times: %w", err)
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return "", true, fmt.Errorf("get process exit code: %w", err)
	}
	identity := fmt.Sprintf("windows:%d", uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime))
	return identity, exitCode == windowsStillActive, nil
}

func terminateRecoveredExternalProcess(pid int, expectedIdentity string) (bool, error) {
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE,
		false,
		uint32(pid),
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_FOUND) {
			return true, nil
		}
		return false, fmt.Errorf("open recovered process %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)
	identity, running, inspectErr := inspectWindowsProcessHandle(handle)
	terminal, signal, err := classifyRecoveredExternalProcess(expectedIdentity, identity, running, inspectErr)
	if err != nil {
		return false, err
	}
	if terminal || !signal {
		return terminal, nil // original identity is gone or schema is not signal-safe
	}
	if err := windows.TerminateProcess(handle, 1); err != nil {
		return false, fmt.Errorf("terminate recovered process %d: %w", pid, err)
	}
	return false, nil
}
