//go:build windows

package claudeaccounts

import "os"

// fileHandle 抽象平台文件句柄。
type fileHandle = *os.File

// acquireFileLock 在 Windows 上返回 UNSUPPORTED_PLATFORM：设计 §8 规定 v1
// macOS only，Windows 的账号池 mutation 一律 unsupported。
func acquireFileLock(path string) (*crossProcessLock, error) {
	_ = path
	return nil, NewErr(ReasonUnsupportedPlatform, "账号池跨进程锁仅支持 macOS/Unix")
}

// unlockFile 在 Windows 上不可达。
func unlockFile(f *os.File) error {
	_ = f
	return NewErr(ReasonUnsupportedPlatform, "账号池跨进程锁仅支持 macOS/Unix")
}
