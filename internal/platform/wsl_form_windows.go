//go:build windows

package platform

import "strings"

// wsl_form_windows.go — workDir 路径形态判定（共享事实源）。
//
// 判定逻辑自 internal/pty/service.go 提升而来（原 isLinuxStylePath /
// workDirMapsToDrvFS / drvfsMountPath），三处消费方共用同一语义：
//   - internal/pty：ConPTY lpCurrentDirectory 不可消费 Linux 形态（B4，
//     err=267 定谳）；DrvFS 告警与 advisory 记录；
//   - internal/gitassist：Windows 侧 git.exe 无法消费 Linux 形态 workDir
//     （E7：/mnt 形态实测 `fatal: cannot change to ...`），前置拦截；
//   - internal/paths：Windows 目录树对 Linux 形态 root 不可用（E8：Abs 拼
//     当前盘符后 Stat 失败，曾静默降级 home 造成错位目录树）。
//
// 仅 Windows 宿主成立：macOS 上 / 形态即本机原生形态，非 Windows 构建的
// no-op 实现返回一律否定（见 wsl_form_other.go）。

// IsLinuxStylePath 报告 workDir 是否为 Linux 形态路径：以 / 开头的绝对路径、
// 以 ~/ 开头的 Home 相对路径，或裸 ~（m-6：wsl.exe --cd ~ 是映射 Linux
// home 的合法形态，但 CreateProcess 同样无法把它当 lpCurrentDirectory 消费
// ——err=267 同型）。与 WorkDirMapsToDrvFS 的形态判定互补：那里识别「落在
// DrvFS 上的两类形态」，这里识别「Windows 进程侧 API 无法消费的 Linux 形态」。
// ~user/ 别家 Home 形态为明确声明的范围外。
func IsLinuxStylePath(workDir string) bool {
	w := strings.TrimSpace(workDir)
	return strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~/") || w == "~"
}

// WorkDirMapsToDrvFS reports whether workDir lands on a DrvFS/9P mount inside
// the distro, in either form: a Windows drive-letter path (X:\repo, which
// wsl.exe --cd maps onto /mnt/<drive>) or the Linux-form mount point itself
// (/mnt/<drive>/...). Linux-style paths that stay on ext4 (~/... or /home/...)
// return false (evidence T12: the /mnt form used to bypass the advisory).
func WorkDirMapsToDrvFS(workDir string) bool {
	w := strings.TrimSpace(workDir)
	if len(w) >= 2 && w[1] == ':' && isDriveLetter(w[0]) {
		return true
	}
	return isLinuxDrvFSMount(w)
}

// isLinuxDrvFSMount reports whether p is a /mnt/<single-drive-letter> path
// (/mnt/d, /mnt/c/repo...). Only a single-character drive segment matches:
// /mnt/wsl, /mnt/docker-desktop and other multi-letter mounts are NOT drive
// mounts and are not the source of the DrvFS slowdown.
func isLinuxDrvFSMount(p string) bool {
	const prefix = "/mnt/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	rest := p[len(prefix):]
	if rest == "" || !isDriveLetter(rest[0]) {
		return false
	}
	return len(rest) == 1 || rest[1] == '/'
}

// DrvFSMountPath converts a workdir that maps to DrvFS into its WSL mount
// point for advisory detail: X:\a\b → /mnt/x/a/b; a Linux-form DrvFS path
// (/mnt/d/...) is already at its mount point and returns unchanged.
// Best-effort, used for advisory detail only; non-DrvFS paths return "".
func DrvFSMountPath(winPath string) string {
	w := strings.TrimSpace(winPath)
	if !WorkDirMapsToDrvFS(w) {
		return ""
	}
	if isLinuxDrvFSMount(w) {
		return w
	}
	return "/mnt/" + strings.ToLower(w[:1]) + strings.ReplaceAll(w[2:], `\`, "/")
}

func isDriveLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
