package paths

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"amagi-codebox/internal/platform"
)

// listDirectoriesLimit 是 ListDirectories 单次返回的子目录条数上限，
// 超出即截断并把 truncated 置为 true（测试可临时下调注入以覆盖截断分支）。
var listDirectoriesLimit = 500

// LinuxStylePathMarker 是 Linux 形态错误消息中的稳定标记片段；前端
// pathPickerModel.ts 以同名子串区分「形态声明」与普通失败（两侧成对维护，
// 改动需同步）。
const LinuxStylePathMarker = "WSL/Linux 路径形态"

// LinuxStylePathError 表示请求的 root 为 Linux 形态路径（WSL 会话工作目录），
// Windows 侧目录树不可用。错误消息自带可操作文案，经 Wails 透传到前端展示。
type LinuxStylePathError struct {
	Path string
}

func (e *LinuxStylePathError) Error() string {
	return fmt.Sprintf("%s（%s）：Windows 侧目录树不可用；请在 WSL 终端内操作，或改用 Windows 盘符路径", LinuxStylePathMarker, e.Path)
}

// directoryListingEntry 是 ListDirectories 返回的单条子目录记录。
type directoryListingEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// directoryListing 是 ListDirectories 返回的 JSON 载荷，字段顺序即序列化顺序。
type directoryListing struct {
	Root      string                  `json:"root"`
	Parent    *string                 `json:"parent"`
	Dirs      []directoryListingEntry `json:"dirs"`
	Truncated bool                    `json:"truncated"`
}

// ListDirectories 列出 root 下一层的子目录（仅目录、不含文件），返回 JSON 字符串，
// 供前端路径选择器逐层浏览目录使用。root 为空时回退到用户主目录。
//
// P4-A1（E8）：root 为 Linux 形态（/、~/、~、/mnt/<drive>）时不再走
// filepath.Abs+Stat 静默失败——Windows 上 Abs 会拼当前盘符拼出 X:\mnt\...，
// Stat 失败后前端曾静默降级 Windows home 展示错位目录树。现在显式返回带
// 稳定标记的结构化错误（*LinuxStylePathError，消息含 LinuxStylePathMarker），
// 前端据此展示「WSL 路径形态，Windows 目录树不可用」。/mnt/<drive> 形态同时
// 写入共享 DrvFS advisory（platform.RecordDrvFSAdvisory）。非 Windows 宿主
// / 形态即本机原生路径，判定恒否，维持原有行为。
func (s *PathsService) ListDirectories(root string) (string, error) {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home dir: %w", err)
		}
		root = home
	}

	if platform.IsLinuxStylePath(root) {
		if platform.WorkDirMapsToDrvFS(root) {
			platform.RecordDrvFSAdvisory(platform.DrvFSAdvisory{
				Source:    "paths",
				WorkDir:   root,
				MountPath: platform.DrvFSMountPath(root),
				Detail:    "目录选择器收到 Linux 形态路径，Windows 目录树不可用",
			})
		}
		return "", &LinuxStylePathError{Path: root}
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", root, err)
	}

	info, err := os.Stat(absRoot)
	if err != nil {
		return "", fmt.Errorf("stat directory %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", absRoot)
	}

	entries, err := os.ReadDir(absRoot)
	if err != nil {
		return "", fmt.Errorf("read directory %q: %w", absRoot, err)
	}

	dirs := make([]directoryListingEntry, 0, len(entries))
	truncated := false
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !entry.IsDir() {
			continue
		}
		if len(dirs) >= listDirectoriesLimit {
			truncated = true
			break
		}
		dirs = append(dirs, directoryListingEntry{
			Name: name,
			Path: filepath.Join(absRoot, name),
		})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})

	parent := filepath.Dir(absRoot)
	var parentPtr *string
	if parent != absRoot {
		parentPtr = &parent
	}

	out, err := json.Marshal(directoryListing{
		Root:      absRoot,
		Parent:    parentPtr,
		Dirs:      dirs,
		Truncated: truncated,
	})
	if err != nil {
		return "", fmt.Errorf("marshal directory listing: %w", err)
	}
	return string(out), nil
}
