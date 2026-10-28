//go:build !windows

package claudeaccounts

import (
	"os"
	"path/filepath"
	"syscall"
)

// fileHandle 抽象平台文件句柄。
type fileHandle = *os.File

// acquireFileLock 用 flock 系统调用获取跨进程互斥锁（design §3.3：使用系统
// 调用能力，不引第三方库）。非阻塞；被他人持有时返回 POOL_LOCKED。
func acquireFileLock(path string) (*crossProcessLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, NewErr(ReasonPoolLocked, "账号池锁目录不可用")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, NewErr(ReasonPoolLocked, "账号池锁文件不可用")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, NewErr(ReasonPoolLocked, "账号池锁被其他进程持有")
	}
	return &crossProcessLock{path: path, file: f}, nil
}

// unlockFile 释放 flock。
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
