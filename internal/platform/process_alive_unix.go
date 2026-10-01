//go:build !windows

package platform

import (
	"errors"
	"os"
	"syscall"
)

// processAliveOS 以信号 0 探测存活（kill -0 语义）：Signal 返回 nil 存活；
// EPERM 表示进程存在但不属于当前用户，同样视为存活；ESRCH/进程已结束
// （os.ErrProcessDone）视为不存活。注意 darwin 的 os.FindProcess 对不存在的
// pid 也返回成功，判定以 Signal 结果为准。
func processAliveOS(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		// Linux：pid 不存在时 FindProcess 直接报错。
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
