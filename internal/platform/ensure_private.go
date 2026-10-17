package platform

// ensure_private.go — 宿主本地受管路径的私有化原语（wsl-goal-closure 攻坚 G0）。
//
// 用途边界：服务宿主本地配置/状态/pending 目录（如 ~/.amagi-codebox/ 下
// wsl-config-supply-pending/）的「当前用户私有」语义——目录 0700 / 文件 0600。
// 它不冒充 WSL POSIX 权限：distro 内文件的 mode 位必须走 WSLChmod/
// WSLFileMode（9P 语义见 wsl_home.go 实证注释）。
//
// 语义（attack-contract「私有目录/文件」）：
//   - 建立（不存在则创建）并收紧（已存在则收窄）到当前用户私有；
//   - 验证而非只设置：设置后必须重新读回权限/ACL 证明隔离成立，验证不可
//     证即返回错误（fail closed，调用方不得当作已私有化）；
//   - 拒绝符号链接：入口与收尾各做一次 Lstat 符号链接检查（chmod/ACL 按
//     名字操作会跟随链接打到别处；首尾双查把竞态窗口压到最小并如实报错）；
//   - 仅修改传入的受管路径本身，不改系统/全局配置。
//
// 威胁模型（明示）：允许的访问主体只有当前用户、LOCAL SYSTEM 与内建
// Administrators 组（SYSTEM/管理员是 Windows 上文件所有权与备份语义的
// 标准伴生主体）；其它本机用户不得读/写。POSIX 侧以 owner-only mode 位
// 表达同一语义。错误信息不携带 SID/ACL 原始内容（不含任何凭据性材料）。
//
// Windows 分文件实现真实 DACL（SetNamedSecurityInfo + 读回验证），不以
// os.Chmod(0600) 冒充（os.Chmod 在 Windows 只拨只读属性）；Windows 实际
// ACL 行为未真机验证，交叉编译通过只证明可编译。

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrPrivatePathNotRegular / ErrPrivatePathNotDir 报告受管路径类型不符。
var (
	ErrPrivatePathNotDir     = errors.New("managed private path is not a directory")
	ErrPrivatePathNotRegular = errors.New("managed private path is not a regular file")
	ErrPrivatePathSymlink    = errors.New("managed private path is or became a symlink")
)

// validatePrivatePath 检查受管路径形态：非空、绝对路径（相对路径随进程
// cwd 漂移，不作为受管锚点）。
func validatePrivatePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("private path is empty")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("private path must be absolute: %q", path)
	}
	return nil
}

// rejectPrivateSymlink 在检查点拒绝符号链接（含 junction/其它 reparse 形态：
// os.Lstat 在 Windows 对 symlink 与 mount point 均报 ModeSymlink）。
func rejectPrivateSymlink(path string, info os.FileInfo, err error) error {
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // 不存在是「建立」分支的合法状态
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrPrivatePathSymlink, path)
	}
	return nil
}

// EnsurePrivateDir 建立/收紧并验证当前用户私有目录（0700 语义，Windows 为
// 等价 owner-only DACL）。目录不存在时逐级创建（新建级按私有语义落位）；
// 已存在时收紧并复验；任何一步验证不可证即失败，不虚报已私有化。
func EnsurePrivateDir(path string) error {
	if err := validatePrivatePath(path); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	} else if err := rejectPrivateSymlink(path, info, nil); err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("%w: %s", ErrPrivatePathNotDir, path)
	}
	if err := ensurePrivateDirOS(path); err != nil {
		return err
	}
	// 收尾复验：类型仍为目录且未被换成符号链接（类型/权限细节由 per-OS
	// ensurePrivateDirOS 内部验证，这里只做跨竞态的类型复检）。
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("verify private dir %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrPrivatePathSymlink, path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s", ErrPrivatePathNotDir, path)
	}
	return nil
}

// EnsurePrivateFile 建立/收紧并验证当前用户私有文件（0600 语义，Windows 为
// 等价 owner-only DACL）。文件不存在时以私有语义创建（空文件，调用方后续
// 以原子写填充）；已存在时收紧并复验；既有的非普通文件（目录/符号链接/
// 设备等）一律拒绝。
func EnsurePrivateFile(path string) error {
	if err := validatePrivatePath(path); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	} else if err := rejectPrivateSymlink(path, info, nil); err != nil {
		return err
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s", ErrPrivatePathNotRegular, path)
	}
	if err := ensurePrivateFileOS(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("verify private file %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrPrivatePathSymlink, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s", ErrPrivatePathNotRegular, path)
	}
	return nil
}
