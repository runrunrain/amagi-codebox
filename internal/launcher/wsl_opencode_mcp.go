package launcher

// wsl_opencode_mcp.go — OpenCode 附加层 runtime config 的 MCP 段确定性形态检查
//（P3-S1，design §五 D3 确定规则的 S1 子集 + §四 OpenCode 冲突默认）。
//
// 输入是 BuildOpenCodeRuntimeConfig/FromPreset 的产物（内含 provider 与可能的
// mcp 段）；检查只做确定性判定，不执行任何 MCP 命令、不猜测未验证的转换：
//
//   - npx.cmd/npm.cmd（裸名）→ 仅当目标登录 shell 实测解析到原生 npx/npm 时映射
//     为原生命令；否则丢弃该 server 并报告（不生成假可用条目）。
//   - Windows exe/ps1/bat/cmd、盘符路径 command、cmd.exe/PowerShell、参数中的
//     盘符路径 → 丢弃该 server 并标「需手动适配」（不自动改 bash、不启用 interop）。
//   - HTTP/远程（带 url）→ 保留 URL 与协议字段原样。
//   - distro 全局 opencode.json 已有同名 MCP server → 默认保留本地，该 server
//     不写入附加层并报告（覆盖选择 UI 留 S2 预览框架）。
//
// 全部判定只基于 JSON 字段形态与一次批量 command -v 探测；探测失败按「不可
// 确认」处理（保守丢弃 + 报告），不把失败当作已验证。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"amagi-codebox/internal/platform"
)

// wslResolveCommandsFn 是原生命令解析接缝（生产 platform.WSLResolveCommands，
// 单测注入假实现，与 wslUserHomeFn 同型）。
var wslResolveCommandsFn = platform.WSLResolveCommands

// mcpBareShimCommands 是裸名 Windows shim → 目标原生命令的候选映射表。
var mcpBareShimCommands = map[string]string{
	"npx.cmd": "npx",
	"npm.cmd": "npm",
}

// AdaptWSLOpenCodeMCP 对 runtime config 的 mcp 段做确定性形态检查，返回
// 调整后的 config（浅拷贝，非 mcp 字段不动）与中文诊断行（供日志/状态页展示；
// 不含密钥）。mcp 段缺失或非对象时原样返回。
func AdaptWSLOpenCodeMCP(distro string, cfg map[string]any) (map[string]any, []string) {
	return AdaptWSLOpenCodeMCPWithOptions(distro, cfg, nil, nil)
}

// AdaptWSLOpenCodeMCPWithOptions 是带选择门的全量适配（P3-S4，design §四
// OpenCode 行「仅作用于本次显式选择的 preset/已授权导入项」）：
//   - allowed 非空时，仅名字命中选择集的 server 进入附加层（其余丢弃并报
//     告——未选择导入，不写入）；
//   - 与 distro 全局 opencode.json 同名的 server 默认保留本地不写入；仅在
//     overrideGlobal 显式勾选覆盖时才写入（S2 预览框架的显式覆盖选择）；
//   - 形态检查与 S1 相同（裸 shim 仅验证过才映射；Windows 专属形态丢弃；
//     HTTP/远程保留）。
//
// allowed == nil 表示「不过滤」（沿用 S1 全量语义，兼容既有调用方）；空
// map 表示全不写。
func AdaptWSLOpenCodeMCPWithOptions(distro string, cfg map[string]any, allowed, overrideGlobal map[string]bool) (map[string]any, []string) {
	raw, has := cfg["mcp"]
	if !has || raw == nil {
		return cfg, nil
	}
	servers, ok := raw.(map[string]any)
	if !ok || len(servers) == 0 {
		return cfg, nil // 形态未知：留给 opencode 自行处理，不猜测
	}

	globalNames := readWSLOpenCodeGlobalMCPServers(distro)

	// 第一遍：收集需要验证的原生命令（裸 shim 候选）。
	need := map[string]bool{}
	for name, value := range servers {
		if allowed != nil && !allowed[name] {
			continue // 未选择导入：不参与探测
		}
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if hasMCPRemoteURL(entry) {
			continue
		}
		command, _ := entry["command"].(string)
		if native, isBare := mcpBareShimCommands[strings.ToLower(strings.TrimSpace(command))]; isBare {
			need[native] = true
		}
	}
	var resolved map[string]string
	if len(need) > 0 {
		names := make([]string, 0, len(need))
		for name := range need {
			names = append(names, name)
		}
		sort.Strings(names)
		resolved = wslResolveCommandsFn(distro, names...)
	}

	var report []string
	adaptedAny := false
	out := map[string]any{}
	for name, value := range servers {
		if allowed != nil && !allowed[name] {
			report = append(report, fmt.Sprintf("MCP server %q 未写入附加层：未选择导入（在选择面勾选后生效）", name))
			continue
		}
		entry, ok := value.(map[string]any)
		if !ok {
			out[name] = value // 非 server 对象：原样保留，不猜测
			continue
		}
		kept, adapted, reason := classifyWSLMCPServer(entry, resolved)
		if !kept {
			report = append(report, fmt.Sprintf("MCP server %q 未写入附加层：%s", name, reason))
			continue
		}
		if adapted != nil {
			entry = adapted
			adaptedAny = true
		}
		if globalNames[name] && !(overrideGlobal != nil && overrideGlobal[name]) {
			report = append(report, fmt.Sprintf("MCP server %q 与 WSL 全局配置同名，保留本地不写入附加层（覆盖请在选择面勾选）", name))
			continue
		}
		out[name] = entry
	}
	if len(report) == 0 && !adaptedAny && len(out) == len(servers) {
		return cfg, nil // 全部保留且无适配：原 config 引用直接返回
	}
	next := make(map[string]any, len(cfg))
	for k, v := range cfg {
		next[k] = v
	}
	if len(out) > 0 {
		next["mcp"] = out
	} else {
		delete(next, "mcp")
	}
	sort.Strings(report)
	return next, report
}

// OpenCodeMCPPreviewEntry 是预览面上单个 preset MCP server 的脱敏分类
// （GetWSLOpenCodeMCPPreview 消费；不含 env 值与密钥）。
type OpenCodeMCPPreviewEntry struct {
	// Name 是 server 名。
	Name string
	// Remote：远程/HTTP 形态（保留 URL）。
	Remote bool
	// NativeCommand：形态检查通过后目标将使用的命令（裸 shim 验证映射后）。
	NativeCommand string
	// NeedsAdapt：形态不可自动转换（Windows 专属/未验证 shim/盘符参数），
	// 该 server 不会被写入附加层。
	NeedsAdapt bool
	// Reason 是中文原因（NeedsAdapt 时非空；不含密钥）。
	Reason string
	// GlobalConflict：distro 全局 opencode.json 已有同名 server（默认保留
	// 本地；勾选覆盖才写入）。
	GlobalConflict bool
}

// PreviewWSLOpenCodeMCP 对 runtime config 的 mcp 段做只读分类预览（不写、
// 不改 cfg）：逐 server 给出形态结论与全局同名冲突，供选择面展示。探测与
// S1 同源（一次批量 command -v + 全局文件读取）。
func PreviewWSLOpenCodeMCP(distro string, cfg map[string]any) []OpenCodeMCPPreviewEntry {
	raw, has := cfg["mcp"]
	if !has || raw == nil {
		return nil
	}
	servers, ok := raw.(map[string]any)
	if !ok || len(servers) == 0 {
		return nil
	}
	globalNames := readWSLOpenCodeGlobalMCPServers(distro)
	need := map[string]bool{}
	for _, value := range servers {
		entry, ok := value.(map[string]any)
		if !ok || hasMCPRemoteURL(entry) {
			continue
		}
		command, _ := entry["command"].(string)
		if native, isBare := mcpBareShimCommands[strings.ToLower(strings.TrimSpace(command))]; isBare {
			need[native] = true
		}
	}
	var resolved map[string]string
	if len(need) > 0 {
		names := make([]string, 0, len(need))
		for name := range need {
			names = append(names, name)
		}
		sort.Strings(names)
		resolved = wslResolveCommandsFn(distro, names...)
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]OpenCodeMCPPreviewEntry, 0, len(names))
	for _, name := range names {
		preview := OpenCodeMCPPreviewEntry{Name: name, GlobalConflict: globalNames[name]}
		server, ok := servers[name].(map[string]any)
		if !ok {
			preview.NeedsAdapt = true
			preview.Reason = "server 定义不是对象"
			out = append(out, preview)
			continue
		}
		if hasMCPRemoteURL(server) {
			preview.Remote = true
			out = append(out, preview)
			continue
		}
		kept, adapted, reason := classifyWSLMCPServer(server, resolved)
		if kept {
			if adapted != nil {
				preview.NativeCommand, _ = adapted["command"].(string)
			} else {
				preview.NativeCommand, _ = server["command"].(string)
			}
		} else {
			preview.NeedsAdapt = true
			preview.Reason = reason
		}
		out = append(out, preview)
	}
	return out
}

// classifyWSLMCPServer 判定单个 server：(kept, adaptedEntry, dropReason)。
func classifyWSLMCPServer(entry map[string]any, resolved map[string]string) (bool, map[string]any, string) {
	if hasMCPRemoteURL(entry) {
		return true, nil, "" // HTTP/远程：保留 URL 与协议字段
	}
	command, _ := entry["command"].(string)
	trimmed := strings.TrimSpace(command)
	lower := strings.ToLower(trimmed)
	// 裸名 Windows shim → 原生命令（仅目标验证过才映射）。
	if native, isBare := mcpBareShimCommands[lower]; isBare {
		if path, ok := resolved[native]; ok && strings.HasPrefix(path, "/") {
			adapted := shallowCopyMCPServer(entry)
			adapted["command"] = native
			return true, adapted, ""
		}
		return false, nil, fmt.Sprintf("命令 %s 未在 WSL 内验证到原生 %s，需手动适配", trimmed, native)
	}
	if trimmed == "" {
		return true, nil, "" // 无 command 也无 url：opencode 自行处理，不猜测
	}
	// Windows 专属形态：盘符路径 / .exe/.ps1/.bat/.cmd / cmd.exe / PowerShell。
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(trimmed, `\`, "/")))
	if isWindowsDrivePath(trimmed) ||
		strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".ps1") ||
		strings.HasSuffix(lower, ".bat") || strings.HasSuffix(lower, ".cmd") ||
		base == "cmd.exe" || base == "cmd" || base == "powershell" || base == "powershell.exe" ||
		base == "pwsh" || base == "pwsh.exe" {
		return false, nil, fmt.Sprintf("命令 %q 为 Windows 专属形态，需手动适配为 Linux 命令", trimmed)
	}
	// 参数中的独立盘符路径：未验证的挂载映射不做，不生成假可用条目。
	if args, ok := entry["args"].([]any); ok {
		for _, arg := range args {
			if s, ok := arg.(string); ok && isWindowsDrivePath(strings.TrimSpace(s)) {
				return false, nil, "启动参数携带 Windows 盘符路径（C:\\...），需手动适配为 WSL 内路径"
			}
		}
	}
	return true, nil, ""
}

// hasMCPRemoteURL 报告 server 是否为远程/HTTP 形态（带 url 字段即视为远程，
// command 缺席时 opencode 走 HTTP/SSE/WebSocket 传输）。
func hasMCPRemoteURL(entry map[string]any) bool {
	url, _ := entry["url"].(string)
	return strings.TrimSpace(url) != ""
}

func shallowCopyMCPServer(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry)+1)
	for k, v := range entry {
		out[k] = v
	}
	return out
}

// readWSLOpenCodeGlobalMCPServers 读取 distro 全局 ~/.config/opencode/opencode.json
// 的 mcp server 名集合（冲突检查用）。文件缺失/不可读/不可解析（含 JSONC 注释
// 剥离后仍失败）一律返回空集合——冲突不可确认时不因读取失败丢弃 server。
func readWSLOpenCodeGlobalMCPServers(distro string) map[string]bool {
	names := map[string]bool{}
	if strings.TrimSpace(distro) == "" {
		return names
	}
	home := wslUserHomeFn(distro)
	if home == "" {
		return names
	}
	unc := wslToUNCFn(distro, strings.TrimRight(home, "/")+"/.config/opencode/opencode.json")
	if unc == "" {
		return names
	}
	data, err := os.ReadFile(unc)
	if err != nil || len(data) == 0 {
		return names
	}
	var parsed map[string]any
	if json.Unmarshal(data, &parsed) != nil {
		if json.Unmarshal(stripJSONCComments(data), &parsed) != nil {
			return names
		}
	}
	mcp, _ := parsed["mcp"].(map[string]any)
	for name := range mcp {
		names[name] = true
	}
	return names
}

// stripJSONCComments 剥离 // 行注释与 /* */ 块注释（字符串字面量外的启发式
// 处理：逐字符扫描跟踪是否在字符串内；足以服务冲突名提取，不追求完整 JSONC）。
func stripJSONCComments(data []byte) []byte {
	var out []byte
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return out
}
