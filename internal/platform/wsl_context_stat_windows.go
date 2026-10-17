//go:build windows

package platform

import "context"

// WSLFileMTimeContext 以可取消的 wsl.exe stat 探测 Linux 文件时间。
func WSLFileMTimeContext(ctx context.Context, distro, linuxPath string) (int64, bool) {
	return wslFileMTimeContextForOS(ctx, "windows", distro, linuxPath)
}

// WSLFileModeContext 以可取消的 wsl.exe stat 探测 Linux 权限位。
func WSLFileModeContext(ctx context.Context, distro, linuxPath string) (string, bool) {
	return wslFileModeContextForOS(ctx, "windows", distro, linuxPath)
}
