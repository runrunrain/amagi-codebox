package platform

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"
)

// WSLResolveDirectoryContext expands ~ / the distro's HOME or converts a
// Windows/UNC directory with that distro's wslpath. It requires successful cd
// and an explicit framed result, not wsl.exe's potentially misleading exit=0.
func WSLResolveDirectoryContext(ctx context.Context, distro, root string) (string, error) {
	if strings.TrimSpace(distro) == "" {
		return "", fmt.Errorf("未指定 WSL 发行版")
	}
	script, err := buildWSLWorkDirCheck(root)
	if err != nil {
		return "", err
	}
	const marker = "\x00__AMAGI_RESOLVED_DIRECTORY__\x00"
	script += `printf '\000__AMAGI_RESOLVED_DIRECTORY__\000%s\000' "$PWD"` + "\n"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	raw, err := wslScriptRunnerContext(ctx, distro, script)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("WSL %s 无法解析目录 %q: %w", distro, root, err)
	}
	at := bytes.Index(raw, []byte(marker))
	if at < 0 {
		return "", fmt.Errorf("WSL %s 目录探测未取得成功标记", distro)
	}
	path := string(raw[at+len(marker):])
	if !strings.HasSuffix(path, "\x00") {
		return "", fmt.Errorf("WSL 目录探测输出不完整")
	}
	path = strings.TrimSuffix(path, "\x00")
	if !strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("WSL 目录探测返回无效路径")
	}
	return path, nil
}
