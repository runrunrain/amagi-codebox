package launcher

// wsl_shared_agent_root.go — WSL 内 pi 共享 Windows agent 根（默认且唯一形态）。
//
// 设计（主上裁定 2026-10：WSL 只提供运行环境，配置/插件/凭据全部来自 Windows）：
// 会话落在 WSL distro 内时，pi 的 PI_CODING_AGENT_DIR 指向 Windows 侧
// ~/.pi/agent（经 WSLENV 的 /p 标志由 wsl.exe 翻译为 /mnt/<盘符>/…），因此
// settings.json / models.json / auth.json / npm / git / skills / amagi 扩展与
// webui 注册表全部与 Windows 侧同一份，不再向 distro 重放供给。
//
// 本文件只做 env 层的纯函数：把 PI_CODING_AGENT_DIR 钉到 Windows agent 根，并把
// 该键登记为「路径值放行键」，使其躲过 StripWSLHostPathEnvWithAllow（1.3.59 的
// 垃圾目录防线）且在 WSLENV 清单中获得 /p 翻译。

import (
	"strings"
)

// WSLSharedPiAgentRootKey 是 pi 的 agent 根环境变量名。
const WSLSharedPiAgentRootKey = "PI_CODING_AGENT_DIR"

// ApplyWSLSharedPiAgentRoot 为落在 WSL 内的 pi 会话钉定共享 agent 根：
//   - env 中已有非空 PI_CODING_AGENT_DIR（如登录会话钉定的 canonical 目录）
//     时沿用其值，否则写入 windowsAgentDir；
//   - 值为 Windows 盘符路径时，把该键并入 pathFlagKeys（放行 + /p 翻译）；
//     值已是 Linux 路径（用户自行指向 distro 内目录）时不加标志，原样透传。
//
// 返回新的 env 与 pathFlagKeys 副本，不修改入参。windowsAgentDir 为空且 env
// 无值时原样返回（无可钉定的根）。
func ApplyWSLSharedPiAgentRoot(env []string, windowsAgentDir string, pathFlagKeys []string) ([]string, []string) {
	value := ""
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		key, v := splitEnvKV(kv)
		if strings.EqualFold(key, WSLSharedPiAgentRootKey) {
			if strings.TrimSpace(v) != "" && value == "" {
				value = strings.TrimSpace(v)
			}
			continue
		}
		out = append(out, kv)
	}
	if value == "" {
		value = strings.TrimSpace(windowsAgentDir)
	}
	flags := append([]string(nil), pathFlagKeys...)
	if value == "" {
		return out, flags
	}
	out = append(out, WSLSharedPiAgentRootKey+"="+value)
	if isWindowsDrivePath(value) {
		flags = append(flags, WSLSharedPiAgentRootKey)
	}
	return out, flags
}
