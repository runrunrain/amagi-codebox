package platform

// wsl_commands.go — distro 登录 shell 内的批量命令解析（P3-S1 OpenCode MCP
// 形态检查的数据源：npx.cmd → npx 映射仅在目标真实 PATH 能解析原生命令时成立，
// design §五 D3 确定规则）。
//
// 单次 wsl.exe 批量探测（`for c in ...; command -v`），与 envcheck 的登录 shell
// 解析探测同型；不缓存——调用方（启动链）按需探测，量级为每次启动至多一次。

import (
	"runtime"
	"strings"
)

// WSLResolveCommands 在 distro 默认用户的登录 shell 内批量解析命令的绝对路径
// （sh -lc 'command -v'，无人工 PATH 前缀——镜像被启动会话实际解析结果）。
// 返回 command → 绝对路径；未解析到或非绝对路径的命令不在结果中。探测失败
// （非 Windows / distro 不可达）返回空 map——调用方按「原生命令不可确认」处理，
// 不把失败当作存在。
func WSLResolveCommands(distro string, commands ...string) map[string]string {
	paths := map[string]string{}
	distro = strings.TrimSpace(distro)
	if distro == "" || len(commands) == 0 || runtime.GOOS != "windows" {
		return paths
	}
	var script strings.Builder
	script.WriteString("for c in")
	wrote := false
	for _, c := range commands {
		if c = strings.TrimSpace(c); c == "" {
			continue
		}
		script.WriteString(" " + wslShSingleQuote(c))
		wrote = true
	}
	if !wrote {
		return paths // 全部为空命令：无探测面
	}
	script.WriteString(`; do printf '%s\t' "$c"; command -v "$c" 2>/dev/null || true; printf '\n'; done`)

	out, err := wslScriptRunner(distro, script.String())
	if err != nil {
		return paths
	}
	for _, line := range strings.Split(decodeWSLListOutput(out), "\n") {
		line = strings.TrimSpace(strings.Trim(line, "\r\x00"))
		if line == "" {
			continue
		}
		cmd, path, ok := strings.Cut(line, "\t")
		cmd = strings.TrimSpace(cmd)
		path = strings.TrimSpace(strings.Trim(path, "\r\x00"))
		if !ok || cmd == "" || path == "" || !strings.HasPrefix(path, "/") {
			continue
		}
		paths[cmd] = path
	}
	return paths
}
