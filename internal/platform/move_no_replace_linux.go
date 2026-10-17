//go:build linux

package platform

import (
	"golang.org/x/sys/unix"
)

// moveNoReplaceOS：Linux 以 renameat2(RENAME_NOREPLACE) 提供同文件系统原子
// no-replace rename（文件与目录均支持）。目标存在返回 EEXIST；内核不支持
// renameat2 时返回 ENOSYS/EINVAL——按不支持错误上抛，调用方保留双方实体，
// 不降级覆盖。
func moveNoReplaceOS(source, destination string) error {
	err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
	if err == unix.ENOSYS || err == unix.EINVAL || err == unix.ENOTTY {
		return fmtMoveNoReplaceUnsupported(err)
	}
	return err
}
