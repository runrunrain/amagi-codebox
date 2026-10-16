package platform

// wsl_target.go — WSL 供给目标的批量身份探测（P3-S0 wslsupply TargetRef 数据源）。
//
// 一次 wsl.exe 调用批量解析 distro 默认用户的 uid、$HOME 与 /etc/machine-id：
//   - uid+home 参与目标 generation（同名 distro 重装 / 默认用户或 Home 改变时
//     旧授权不得自动扩散，design p3-config-supply §七「状态落点与提交」）；
//   - machine-id 是当前唯一可得的稳定 distro 身份标识；拿不到时调用方按
//     「未知 generation」处理，不猜测继承。
// 所有探测经包级 var wslScriptRunner（与 wsl_home.go 共享接缝），非 Windows
// 平台直接返回 OK=false（wslsetup/wslsupply 按「不适用」降级）。

import (
	"strconv"
	"strings"
)

// WSLUserIdentity 是一次批量身份探测的结果。OK=false 表示 uid/home 任一
// 不可解析（含非 Windows、distro 不可达、输出形态异常）；MachineID 允许为空
// （部分最小化发行版无 /etc/machine-id），此时调用方按未知 generation 处理。
type WSLUserIdentity struct {
	UID       string
	Home      string
	MachineID string
	OK        bool
}

// wslIdentityScript 输出三行：uid、$HOME、machine-id（可能缺行）。
// machine-id 读失败时输出空行且整体仍 exit 0（|| true），不拖垮前两行。
const wslIdentityScript = `id -u; printf '%s\n' "$HOME"; cat /etc/machine-id 2>/dev/null || true`

// WSLTargetIdentity 批量探测 distro 默认用户身份。不做缓存：generation 失效
// 判定依赖当次真值（home/uid 变化必须被看见），且供给链每次启动至多探测两次
// （ResolveTarget + Apply 复核），量级可接受。
func WSLTargetIdentity(distro string) WSLUserIdentity {
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return WSLUserIdentity{}
	}
	out, err := wslScriptRunner(distro, wslIdentityScript)
	if err != nil {
		return WSLUserIdentity{}
	}
	return parseWSLUserIdentity(decodeWSLListOutput(out))
}

// parseWSLUserIdentity 解析三行探测输出：uid 必须为纯数字，home 必须是绝对
// Linux 路径；machine-id 为可选第三行（去空白，接受小写十六进制，宽松长度）。
// 任何前两行异常 → OK=false，调用方 fail closed 而不是把失败当空值。
func parseWSLUserIdentity(out string) WSLUserIdentity {
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return WSLUserIdentity{}
	}
	uid := strings.TrimSpace(trimWSLProbeLine(lines[0]))
	home := strings.TrimSpace(trimWSLProbeLine(lines[1]))
	if uid == "" || home == "" {
		return WSLUserIdentity{}
	}
	if _, err := strconv.ParseUint(uid, 10, 64); err != nil {
		return WSLUserIdentity{}
	}
	if !strings.HasPrefix(home, "/") {
		return WSLUserIdentity{}
	}
	machineID := ""
	if len(lines) >= 3 {
		machineID = strings.TrimSpace(trimWSLProbeLine(lines[2]))
	}
	return WSLUserIdentity{UID: uid, Home: home, MachineID: machineID, OK: true}
}

// trimWSLProbeLine 去除 wsl.exe 输出常见的 CR/NUL 尾部噪音（与 wsl_home.go
// 的逐行清洗同型，集中一处避免散布）。
func trimWSLProbeLine(line string) string {
	return strings.Trim(line, "\r\x00")
}
