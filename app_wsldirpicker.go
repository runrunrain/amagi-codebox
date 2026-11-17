package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
)

// Paired with pathPickerModel.ts: session-target failures must not silently
// replace the requested tree with a different home directory.
const sessionDirectoryErrorMarker = "会话目录不可用"

var (
	// Use the actual process target, never replay the requested ShellRef:
	// availability/defaults can change after a session has started.
	sessionWSLTargetFn = func(a *App, id string, mode launchplan.Mode) (bool, string, error) {
		if mode != launchplan.ModeEmbedded {
			return false, "", nil
		}
		if a.Pty == nil {
			return false, "", fmt.Errorf("PTY 服务未就绪，无法确认会话文件系统")
		}
		return a.Pty.SessionWSLTarget(id)
	}
	isLinuxStylePathFn = platform.IsLinuxStylePath
	// Kept injectable for facade tests; production conversion is bounded and
	// verifies the directory in the exact captured distro, including UNC paths.
	wslTranslateWindowsRootFn = func(distro string, roots ...string) map[string]string {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result := map[string]string{}
		for _, root := range roots {
			if path, err := platform.WSLResolveDirectoryContext(ctx, distro, root); err == nil {
				result[normalizeWindowsRootKey(root)] = path
			}
		}
		return result
	}
	listWSLDirectoriesFn = func(svc *paths.PathsService, distro, root string) (string, error) {
		return svc.ListWSLDirectories(distro, root)
	}
)

// SessionListDirectories lists the filesystem of this session's actual PTY.
// External/native sessions retain the native PathsService behavior. WSL results
// contain Linux paths, so both Web and TUI insertions can use them unchanged.
func (a *App) SessionListDirectories(sessionID string, root string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("%s：缺少会话 ID，无法确定目录上下文", sessionDirectoryErrorMarker)
	}
	if a.Sessions == nil || a.Paths == nil {
		return "", fmt.Errorf("%s：会话或路径服务未就绪", sessionDirectoryErrorMarker)
	}
	launchCtx, err := a.Sessions.LaunchContextByID(sessionID)
	if err != nil {
		return "", fmt.Errorf("%s：会话 %s 不存在或已移除：%w", sessionDirectoryErrorMarker, sessionID, err)
	}
	wsl, distro, err := sessionWSLTargetFn(a, sessionID, launchCtx.Mode)
	if err != nil {
		return "", fmt.Errorf("%s：无法确认会话实际目录上下文：%w", sessionDirectoryErrorMarker, err)
	}
	if !wsl {
		return a.Paths.ListDirectories(root)
	}
	if distro == "" {
		return "", fmt.Errorf("%s：会话运行在 WSL 内，但未找到可用发行版记录，无法列出目录", sessionDirectoryErrorMarker)
	}
	linuxRoot, err := wslPickerRoot(distro, root)
	if err != nil {
		return "", fmt.Errorf("%s：%w", sessionDirectoryErrorMarker, err)
	}
	result, err := listWSLDirectoriesFn(a.Paths, distro, linuxRoot)
	if err != nil {
		return "", fmt.Errorf("%s：%w", sessionDirectoryErrorMarker, err)
	}
	return result, nil
}

func wslPickerRoot(distro, root string) (string, error) {
	if strings.IndexFunc(root, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", fmt.Errorf("工作路径含不支持的控制字符")
	}
	// Directory names supplied by the Linux listing are byte-exact; a final
	// space is a legal part of the name, not UI padding to strip.
	if root == "" || isLinuxStylePathFn(root) {
		return root, nil
	}
	root = strings.TrimSpace(root)
	if (len(root) >= 2 && root[1] == ':' && isDriveLetterByte(root[0])) || strings.HasPrefix(root, `\\`) {
		if linux, ok := wslTranslateWindowsRootFn(distro, root)[normalizeWindowsRootKey(root)]; ok {
			return linux, nil
		}
		return "", fmt.Errorf("Windows 路径 %s 无法在 WSL 发行版（%s）内定位（目录不存在、不可达或校验超时）；可点「从主目录开始」从发行版主目录浏览", root, distro)
	}
	return "", fmt.Errorf("WSL 会话的目录需为 Linux 路径（/…、~ 或留空表示主目录）或 Windows 盘符/UNC路径，收到 %q", root)
}

func isDriveLetterByte(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func normalizeWindowsRootKey(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)
	return strings.ToLower(p)
}
