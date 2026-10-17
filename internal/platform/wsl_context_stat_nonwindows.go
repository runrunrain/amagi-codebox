//go:build !windows

package platform

import "context"

// WSLFileMTimeContext 在非 Windows 宿主不探测 WSL。
func WSLFileMTimeContext(ctx context.Context, distro, linuxPath string) (int64, bool) {
	return 0, false
}

// WSLFileModeContext 在非 Windows 宿主不探测 WSL。
func WSLFileModeContext(ctx context.Context, distro, linuxPath string) (string, bool) {
	return "", false
}
