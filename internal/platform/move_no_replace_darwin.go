//go:build darwin

package platform

import (
	"golang.org/x/sys/unix"
)

// moveNoReplaceOS：macOS 以 renamex_np(RENAME_EXCL) 提供同文件系统原子
// no-replace rename（文件与目录均支持）。目标存在返回 EEXIST（syscall.Errno
// 的 Is 面即可被 errors.Is(err, os.ErrExist) 判定）。
func moveNoReplaceOS(source, destination string) error {
	return unix.RenamexNp(source, destination, unix.RENAME_EXCL)
}
