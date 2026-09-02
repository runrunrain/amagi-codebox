package paths

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// listDirectoriesLimit 是 ListDirectories 单次返回的子目录条数上限，
// 超出即截断并把 truncated 置为 true（测试可临时下调注入以覆盖截断分支）。
var listDirectoriesLimit = 500

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
func (s *PathsService) ListDirectories(root string) (string, error) {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home dir: %w", err)
		}
		root = home
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
