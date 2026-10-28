package claudeaccounts

import (
	"os"
	"path/filepath"
)

// DefaultPoolLockPath 返回跨进程锁文件的默认路径
// ~/.amagi-codebox/claude-accounts.lock（design §3.3）。锁文件不含任何秘密。
// 生产接线可覆盖；测试必须注入 tempdir 路径。
func DefaultPoolLockPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".amagi-codebox", "claude-accounts.lock"), nil
}

// crossProcessLock 是进程间互斥锁句柄；获取/释放的平台实现见 lock_unix.go
// 与 lock_windows.go。
type crossProcessLock struct {
	path string
	file fileHandle
}

// release 释放跨进程锁（幂等）。
func (l *crossProcessLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = unlockFile(l.file)
	_ = l.file.Close()
	l.file = nil
}
