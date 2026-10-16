//go:build !windows

package platform

// wsl_form_other.go — 非 Windows 宿主的路径形态判定 no-op（与
// wsl_form_windows.go 成对）。
//
// macOS/Linux 上 / 开头即本机原生绝对路径，不存在「WSL/Linux 形态 vs
// Windows 形态」的区分：判定一律 false，DrvFS 挂载点映射一律空串。
// gitassist / paths 等跨平台包因此在本机路径上维持原有行为不变。

// IsLinuxStylePath 非 Windows 宿主恒为 false（/ 形态即本机原生形态）。
func IsLinuxStylePath(workDir string) bool { return false }

// WorkDirMapsToDrvFS 非 Windows 宿主恒为 false（无 DrvFS 挂载语义）。
func WorkDirMapsToDrvFS(workDir string) bool { return false }

// DrvFSMountPath 非 Windows 宿主恒为空串。
func DrvFSMountPath(winPath string) string { return "" }
