package v2rayn

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const databaseFile = "guiConfigs/guiNDB.db"

func Locate(explicit string) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		p, err := filepath.Abs(explicit)
		if err != nil {
			return "", errors.New("v2rayN 数据库路径无效")
		}
		if !isRegularFile(p) {
			return "", errors.New("找不到指定的 v2rayN 数据库")
		}
		return p, nil
	}
	return firstFile(installRoots(), databaseFile), nil
}

// FindBundledCore only probes known installation roots, never directories
// derived from a manually supplied database path.
func FindBundledCore() string {
	return firstFile(installRoots(), bundledCoreFile)
}

func firstFile(roots []string, relativePath string) string {
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		if candidate := filepath.Join(root, relativePath); isRegularFile(candidate) {
			return candidate
		}
	}
	return ""
}

func isRegularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
