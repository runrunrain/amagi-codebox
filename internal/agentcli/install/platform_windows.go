//go:build windows

package install

// currentShimPlatform 当前构建目标的 shim 风格（CLAUDE.md 约定：平台差异
// 用 build tag 分文件，不做 runtime.GOOS 分支）。
const currentShimPlatform = shimWindows
