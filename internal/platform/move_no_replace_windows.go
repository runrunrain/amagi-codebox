//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// moveNoReplaceOS：Windows 用原生 MoveFileExW 且不带 MOVEFILE_REPLACE_EXISTING
// /COPY_ALLOWED 标志——不带替换标志时目标已存在即失败（ERROR_ALREADY_EXISTS，
// Errno 的 Is 面可被 errors.Is(err, os.ErrExist) 判定），同卷内为原子重命名；
// 不带 COPY_ALLOWED 时跨卷移动不被静默降级为复制（跨卷返回错误，符合
// 「不支持就 error，不降级」）。
//
// UNC（\\\\wsl.localhost\\... 等 9P 共享）的错误码形态依赖服务端；已观测到
// 并发冲突返回 ACCESS_DENIED，见下方只读后验分类。真实目录/并发覆盖见
// windows-interop-real-v2.log，不将单一环境结果泛化到所有共享服务端。
func moveNoReplaceOS(source, destination string) error {
	src16, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return fmt.Errorf("move-no-replace encode source: %w", err)
	}
	dst16, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return fmt.Errorf("move-no-replace encode destination: %w", err)
	}
	// flags=0：无 MOVEFILE_REPLACE_EXISTING（不替换目标）、无
	// MOVEFILE_COPY_ALLOWED（跨卷不降级复制）、无 MOVEFILE_DELAY_UNTIL_REBOOT。
	return classifyMoveNoReplaceError(source, destination, windows.MoveFileEx(src16, dst16, 0))
}

// A WSL 9P collision can report ACCESS_DENIED instead of ALREADY_EXISTS.
// Classify only AFTER the native no-replace operation has failed, and only
// when both entries are observable. This never authorizes a retry or overwrite.
// Keep the original denial as well: existence does not prove why the OS refused.
func classifyMoveNoReplaceError(source, destination string, err error) error {
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return err
	}
	if _, sourceErr := os.Lstat(source); sourceErr != nil {
		return err
	}
	if _, targetErr := os.Lstat(destination); targetErr != nil {
		return err
	}
	return errors.Join(os.ErrExist, err)
}
