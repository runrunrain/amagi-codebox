//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ensurePrivateDirOS（POSIX）：逐级创建目录（新建级 0700），随后对目标目录
// 本身收紧 0700 并读回复验——验证而非只设置（umask 可能宽于请求位）。
func ensurePrivateDirOS(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private dir: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("tighten private dir 0700: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("verify private dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s", ErrPrivatePathNotDir, path)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		return fmt.Errorf("private dir %s mode = %o, want 700 (isolation unverified)", path, got)
	}
	return nil
}

// ensurePrivateFileOS（POSIX）：不存在则以 O_CREATE|O_EXCL 0600 创建（EXCL
// 使并发创建竞争失败者拿到明确错误而非复用他人文件）；已存在则收紧 0600
// 并读回复验。符号链接在共享入口前后双查，此处 chmod 落到的即是受管文件
// 本体（若在窗口内被换成链接，收尾 Lstat 复检会拒绝）。
func ensurePrivateFileOS(path string) error {
	if _, err := os.Lstat(path); err != nil && errors.Is(err, fs.ErrNotExist) {
		// 父目录缺失时按私有目录语义建立（0700），私有文件不落在未验证的
		// 父级里；父级已存在则顺带收紧验证。
		if err := ensurePrivateDirOS(filepath.Dir(path)); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create private file: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close new private file: %w", err)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("tighten private file 0600: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("verify private file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s", ErrPrivatePathNotRegular, path)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		return fmt.Errorf("private file %s mode = %o, want 600 (isolation unverified)", path, got)
	}
	return nil
}
