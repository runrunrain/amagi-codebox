//go:build windows

package platform

import (
	"errors"

	"golang.org/x/sys/windows"
)

// processAliveOS 经 OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION) +
// GetExitCodeProcess 判定存活（同 internal/updater 的 processExists 模式）：
// 打开失败时除 ERROR_INVALID_PARAMETER（pid 不存在/非法）外按存活保守处理
// （如 ACCESS_DENIED：进程存在但属他人）；打开成功则以退出码
// STILL_ACTIVE(259) 为准。
func processAliveOS(pid int) bool {
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
