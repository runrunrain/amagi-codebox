//go:build linux

package platform

import (
	"fmt"

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

// fmtMoveNoReplaceUnsupported 把底层「无此原语」errno 包装为不支持错误
// （保留原始 errno 供诊断）。仅 Linux 实现需要（darwin/windows 有原生原语），
// 随本文件按平台持有，避免在未使用它的 GOOS 上成为死代码。
func fmtMoveNoReplaceUnsupported(err error) error {
	return fmt.Errorf("%w: %w", ErrMoveNoReplaceUnsupported, err)
}
