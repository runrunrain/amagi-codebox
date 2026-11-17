// Package install 负责 agent-CLI 的本机分发面：shim（<home>/bin/codebox-agent）
// 的生成/安装/清理、agent 技能（SKILL.md + references/commands.md）在
// agents/claude/codex 三个目标的安装/更新/清理/状态查询、用户清理偏好的
// 持久记忆（<home>/agent-cli-install.json），以及由命令表生成的命令参考
// 文档（docgen.go）。
//
// 幂等机制对齐 feishu-sync（R1 §5）：安装体 = body + 托管尾标记
// （managed sha256:<hash>），标记携带 body 哈希；内容一致不写盘；标记缺失
// 或哈希不符一律视为外部改动，保留文件并报冲突（*ConflictedError），绝不覆盖。
package install

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
)

// State 是安装面（shim / 技能目标）的状态机。
type State string

const (
	// StateNotInstalled 目标文件不存在。
	StateNotInstalled State = "not-installed"
	// StateUpToDate 内容与当前期望逐字节一致。
	StateUpToDate State = "up-to-date"
	// StateOutdated 托管标记有效但内容是旧版本（可安全覆盖更新）。
	StateOutdated State = "outdated"
	// StateModifiedByUser 文件被外部改动（无有效标记/哈希不符）或非常规
	// 文件/超大文件：一律保留，不覆盖、不删除。
	StateModifiedByUser State = "modified-by-user"
	// StateInvalid 安装路径本身不可用（根缺失、组件是链接或非目录、IO 错误）。
	StateInvalid State = "invalid"
)

// Status 描述一个安装面的当前状态；Message 仅在 StateInvalid 时携带原因。
type Status struct {
	Path    string
	State   State
	Message string
}

// ConflictedError 表示托管文件被外部改动，已保留不覆盖、不删除。
type ConflictedError struct{ Path string }

func (e *ConflictedError) Error() string {
	return "检测到同名文件或外部修改，已保留：" + e.Path
}

// maxManagedFileSize 托管文件大小上限：超出视为外部改动（防误吞大文件）。
const maxManagedFileSize = 64 * 1024

// Manager 负责 shim 与 agent 技能的本机安装/更新/清理/状态查询。
type Manager struct {
	home     string         // 应用数据根（~/.amagi-codebox）：bin/ 与 agent-cli-install.json 所在
	userHome string         // 用户主目录：技能目标（.agents/.claude/.codex）所在
	exePath  string         // 应用可执行文件绝对路径（os.Executable 的解析由 root 完成）
	platform shimPlatform   // shim 平台风格（默认取当前构建目标，测试可注入）
	table    []spec.Command // 命令表：技能 references/commands.md 的渲染源
}

// New 创建当前构建平台的管理器。home/userHome/exePath 均由调用方解析传入
// （便于测试注入临时目录）；table 通常是 cmds.Table()。
func New(home, userHome, exePath string, table []spec.Command) *Manager {
	return newManager(home, userHome, exePath, table, currentShimPlatform)
}

// newManager 显式注入平台风格：Windows shim 行为可在任意 OS 上测试，
// 不做 runtime.GOOS 分支（CLAUDE.md 平台差异约定：运行时取值由 build tag
// 文件提供，见 platform_unix.go / platform_windows.go）。
func newManager(home, userHome, exePath string, table []spec.Command, platform shimPlatform) *Manager {
	return &Manager{
		home:     absClean(home),
		userHome: absClean(userHome),
		exePath:  exePath,
		platform: platform,
		table:    table,
	}
}

func absClean(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// Home 返回应用数据根。
func (m *Manager) Home() string { return m.home }

// ---- 托管文件核心机制（shim 与技能共用） ----

// managedFile 描述一份托管文件：期望 body、尾标记函数（吃 body 哈希）与权限。
type managedFile struct {
	path string
	body string
	mark func(hash string) string
	mode fs.FileMode
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// inspectManaged 检查托管文件状态：末行必须是托管标记且其哈希与前面的
// 内容一致才视为"本应用管理"；与当前期望逐字节一致为 up-to-date，标记有效
// 但内容不同为 outdated；其余文件一律 modified-by-user（保留不覆盖）。
func inspectManaged(f managedFile) Status {
	fi, err := os.Lstat(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Status{Path: f.path, State: StateNotInstalled}
	}
	if err != nil {
		return Status{Path: f.path, State: StateInvalid, Message: err.Error()}
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxManagedFileSize {
		return Status{Path: f.path, State: StateModifiedByUser}
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		return Status{Path: f.path, State: StateInvalid, Message: err.Error()}
	}
	text := string(data)
	// 定位末行起点（text 以换行结尾时排除最后的换行再找前一换行，
	// 与 feishu-sync lastIndexOf 语义一致，兼容 CRLF 标记行）。
	start := 0
	if n := len(text); n > 0 {
		start = strings.LastIndex(text[:n-1], "\n") + 1
	}
	if text[start:] != f.mark(sha256Hex(text[:start])) {
		return Status{Path: f.path, State: StateModifiedByUser}
	}
	if text == f.body+f.mark(sha256Hex(f.body)) {
		return Status{Path: f.path, State: StateUpToDate}
	}
	return Status{Path: f.path, State: StateOutdated}
}

// installManaged 幂等落盘一份托管文件（内容一致不写；外部改动报冲突）。
func installManaged(f managedFile) error {
	st := inspectManaged(f)
	switch st.State {
	case StateUpToDate:
		return nil
	case StateNotInstalled, StateOutdated:
		return atomicWrite(f.path, f.body+f.mark(sha256Hex(f.body)), f.mode)
	case StateModifiedByUser:
		return &ConflictedError{Path: f.path}
	default:
		return fmt.Errorf("托管文件状态不可用：%s", st.Message)
	}
}

// removeManaged 删除一份托管文件（外部改动或路径不可用时拒绝）。
func removeManaged(f managedFile) error {
	st := inspectManaged(f)
	switch st.State {
	case StateNotInstalled:
		return nil
	case StateUpToDate, StateOutdated:
		return os.Remove(f.path)
	case StateModifiedByUser:
		return &ConflictedError{Path: f.path}
	default:
		return fmt.Errorf("托管文件状态不可用：%s", st.Message)
	}
}

// assertManageable 拒绝在任一外部改动的托管文件上安装/清理（先全量检查
// 再动作，避免多文件场景下部分落盘后才撞冲突）。
func assertManageable(sts []Status) error {
	for _, st := range sts {
		switch st.State {
		case StateModifiedByUser:
			return &ConflictedError{Path: st.Path}
		case StateInvalid:
			return fmt.Errorf("安装路径状态不可用：%s", st.Message)
		}
	}
	return nil
}

// atomicWrite 原子写：同目录独占临时文件（写满 + chmod + Sync）后 rename
// 替换；rename 是最后一个可失败步（同 internal/settings saveLocked 的提交
// 边界约定，Windows 下 Sync 走 FlushFileBuffers 需写句柄，同一句柄完成）。
func atomicWrite(path, text string, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	name := tmp.Name()
	werr := func() error {
		if _, err := tmp.WriteString(text); err != nil {
			return err
		}
		if err := tmp.Chmod(mode); err != nil {
			return err
		}
		return tmp.Sync()
	}()
	cerr := tmp.Close()
	if werr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write temp for %s: %w", path, werr)
	}
	if cerr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close temp for %s: %w", path, cerr)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

// ensureDirPath 逐级校验 root 下的目录路径：任何已存在组件必须是真实目录
// （拒绝符号链接与非目录项）；组件缺失时仅当 create 才创建（0o755），且
// 不再检查更深层（调用方随后对目标文件 inspect 会得到 not-installed）。
// 与 feishu-sync checkDirectory 同语义。
func ensureDirPath(root string, segments []string, create bool) error {
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("安装根目录不可用：%s", root)
	}
	dir := root
	for _, seg := range segments {
		dir = filepath.Join(dir, seg)
		fi, err := os.Lstat(dir)
		switch {
		case err == nil:
			if !fi.IsDir() {
				return fmt.Errorf("安装路径包含链接或非目录项：%s", dir)
			}
		case errors.Is(err, fs.ErrNotExist):
			if !create {
				return nil
			}
			if err := os.Mkdir(dir, 0o755); err != nil {
				return fmt.Errorf("create dir %s: %w", dir, err)
			}
		default:
			return err
		}
	}
	return nil
}
