//go:build windows

package updater

import (
	"errors"

	"golang.org/x/sys/windows"
)

func processExists(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return true
	}
	return exitCode == 259 // STILL_ACTIVE
}

func scheduleDeleteAfterReboot(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(pathPtr, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}
