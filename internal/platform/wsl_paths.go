package platform

// wsl_paths.go — Windows 路径 → distro 内 Linux 路径的批量实测映射
//（P3-S2 转换器事实源：design §五「经目标 wslpath/挂载验证映射，须存在」）。
//
// 单次 wsl.exe 批量执行：wslpath -u 转换 + test -e 存在性验证，只返回两者都
// 成立的映射；失败/不存在/非绝对路径的输入不进结果（调用方按「未验证」拒绝，
// 不盲信所有 distro 都挂 /mnt）。非 Windows 平台返回空 map。

import (
	"runtime"
	"sort"
	"strings"
)

// WSLTranslatePaths 在 distro 内批量把 Windows 绝对路径映射为 Linux 路径。
// 返回「规范化 Windows 路径（小写、反斜杠、去结尾分隔符）→ Linux 绝对路径」；
// 只包含目标内 wslpath 可转换且实际存在的条目。脚本每行输出
// 「原始输入<TAB>Linux 路径」，对齐不依赖输入顺序。
func WSLTranslatePaths(distro string, windowsPaths ...string) map[string]string {
	out := map[string]string{}
	distro = strings.TrimSpace(distro)
	if distro == "" || runtime.GOOS != "windows" || len(windowsPaths) == 0 {
		return out
	}
	seen := map[string]bool{}
	inputs := make([]string, 0, len(windowsPaths))
	for _, p := range windowsPaths {
		p = strings.TrimSpace(p)
		if len(p) < 3 || p[1] != ':' {
			continue
		}
		key := normalizeWindowsPathKey(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		inputs = append(inputs, p)
	}
	if len(inputs) == 0 {
		return out
	}
	sort.Strings(inputs)

	var script strings.Builder
	script.WriteString(`for p in`)
	for _, p := range inputs {
		script.WriteString(" " + wslShSingleQuote(p))
	}
	script.WriteString(`; do u="$(wslpath -u "$p" 2>/dev/null)"; if [ -n "$u" ] && [ -e "$u" ]; then printf '%s\t%s\n' "$p" "$u"; fi; done`)

	raw, err := wslScriptRunner(distro, script.String())
	if err != nil {
		return out
	}
	for _, line := range strings.Split(decodeWSLListOutput(raw), "\n") {
		line = strings.Trim(strings.TrimSpace(line), "\r\x00")
		if line == "" {
			continue
		}
		input, linuxPath, ok := strings.Cut(line, "\t")
		input = strings.TrimSpace(input)
		linuxPath = strings.TrimSpace(strings.Trim(linuxPath, "\r\x00"))
		if !ok || input == "" || !strings.HasPrefix(linuxPath, "/") {
			continue
		}
		out[normalizeWindowsPathKey(input)] = linuxPath
	}
	return out
}

// normalizeWindowsPathKey 规范化 Windows 路径为映射查找键（与 wslsupply 的
// NormalizeWindowsPath 同规则：小写、反斜杠、去结尾分隔符）。
func normalizeWindowsPathKey(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)
	return strings.ToLower(p)
}
