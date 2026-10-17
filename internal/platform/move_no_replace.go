package platform

// move_no_replace.go — 原子不替换移动原语（wsl-goal-closure 攻坚 G0）。
//
// 语义（attack-contract「统一文件原语/新增窄 MoveNoReplace」）：
//   - 同文件系统内把 source 原子移动到 destination；
//   - destination 已存在（文件/目录/空目录）绝不替换，返回 os.ErrExist；
//     source 与 destination 双方内容/实体均不丢失；
//   - 文件与目录均支持；不支持（跨设备、OS/文件系统无该原语）返回错误，
//     绝不降级为「先删目标再 Rename」或「Lstat 后普通 Rename」等危险路径
//     （Lstat+Rename 存在 TOCTOU 竞态窗口，禁止冒充 no-replace）；
//   - 成功后 source 不存在、destination 存在——本入口做廉价的落位后验，
//     后验不成立按错误报告，不虚报成功。
//
// 分 OS 实现（build tags，不用 runtime.GOOS 分支）：
//   - darwin:  unix.RenamexNp(..., RENAME_EXCL)；
//   - linux:   unix.Renameat2(..., RENAME_NOREPLACE)；
//   - windows: windows.MoveFileEx 不带 MOVEFILE_REPLACE_EXISTING 标志的原生
//     移动（目标存在 → ERROR_ALREADY_EXISTS）。
//   - 其它 OS：显式不支持错误。
//
// Windows 本地与 WSL 9P 的文件/目录/并发用例已有隔离真机证据（见
// agent-outputs/workflow/wsl-goal-closure/windows-interop-real-v2.log）。
// 该结果不覆盖其它 UNC 服务端；交叉编译本身不证明原子性或错误码形态。
//
// 注意：原子 no-replace 不等于原子内容 CAS——目标在调用前后被外部进程改写
// 不在本原语保证内；调用方须自行复验移动后的实体（契约「外部后改」流程）。

import (
	"errors"
	"fmt"
	"os"
)

// fmtMoveNoReplaceUnsupported 把底层「无此原语」errno 包装为不支持错误
// （保留原始 errno 供诊断）。
func fmtMoveNoReplaceUnsupported(err error) error {
	return fmt.Errorf("%w: %w", ErrMoveNoReplaceUnsupported, err)
}

// ErrMoveNoReplaceUnsupported 报告当前 GOOS/文件系统不支持原子 no-replace
// 移动。调用方必须按「能力不可用」处理：保留 source 与既有目标，不得退化
// 为覆盖式移动。
var ErrMoveNoReplaceUnsupported = errors.New("atomic no-replace move unsupported on this GOOS")

// MoveNoReplace 把 source 原子移动到 destination（同文件系统、不替换既有
// 目标、文件与目录均支持）。destination 已存在时返回可被 errors.Is 判定的
// os.ErrExist，且双方实体保持原样。
func MoveNoReplace(source, destination string) error {
	if source == "" || destination == "" {
		return fmt.Errorf("move-no-replace requires non-empty paths (source=%q destination=%q)", source, destination)
	}
	if source == destination {
		return fmt.Errorf("move-no-replace source and destination are identical: %q", source)
	}
	err := moveNoReplaceOS(source, destination)
	if err == nil {
		// 落位后验：成功移动后 source 必须消失、destination 必须存在。
		if _, serr := os.Lstat(source); serr == nil {
			return fmt.Errorf("move-no-replace reported success but source still exists: %q", source)
		} else if !errors.Is(serr, os.ErrNotExist) {
			return fmt.Errorf("move-no-replace post-check source %q: %w", source, serr)
		}
		if _, derr := os.Lstat(destination); derr != nil {
			return fmt.Errorf("move-no-replace post-check destination %q: %w", destination, derr)
		}
		return nil
	}
	if errors.Is(err, os.ErrExist) {
		// no-replace 冲突后验：双方实体必须在场（任何一方丢失都是深度异常，
		// 不得静默当作普通冲突）。
		if _, serr := os.Lstat(source); serr != nil {
			return fmt.Errorf("move-no-replace conflict post-check source %q: %w", source, serr)
		}
		if _, derr := os.Lstat(destination); derr != nil {
			return fmt.Errorf("move-no-replace conflict post-check destination %q: %w", destination, derr)
		}
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("move-no-replace source missing: %w", err)
	}
	return err
}
