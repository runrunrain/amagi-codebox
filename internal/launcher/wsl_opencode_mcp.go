package launcher

// wsl_opencode_mcp.go — OpenCode 附加层 runtime config 的 MCP 段确定性形态检查
//（P3-S1，design §五 D3 确定规则的 S1 子集 + §四 OpenCode 冲突默认）。
//
// 输入是 BuildOpenCodeRuntimeConfig/FromPreset 的产物（内含 provider 与可能的
// mcp 段）；检查只做确定性判定，不执行任何 MCP 命令、不猜测未验证的转换：
//
//   - npx.cmd/npm.cmd（裸名）→ 仅当目标登录 shell 实测解析到原生 npx/npm 时映射
//     为原生命令；否则丢弃该 server 并报告（不生成假可用条目）。
//   - Windows exe/ps1/bat/cmd、盘符路径 command、cmd.exe/PowerShell、参数/env
//     值中的盘符路径 → 丢弃该 server 并标「需手动适配」（不自动改 bash、不启用 interop）。
//   - HTTP/远程（带 url）→ 保留 URL 与协议字段原样。
//   - distro 全局 opencode.json 已有同名 MCP server → 默认保留本地，该 server
//     不写入附加层并报告（覆盖选择 UI 留 S2 预览框架）。
//
// 全部判定只基于 JSON 字段形态与一次批量 command -v 探测；探测失败按「不可
// 确认」处理（保守丢弃 + 报告），不把失败当作已验证。

import (
	"bytes"
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

	globalNames, globalState, globalReason := readWSLOpenCodeGlobalMCPServers(distro)

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
		if globalState == wslGlobalUnknown {
			// SA14：全局配置不可读/不可解析——同名冲突无法确认，「未知」≠「不
			// 冲突」，任何名字都可能覆盖本地选择；一律不写入，启动继续（可选
			// 配置失败可见不阻断），修复全局文件后恢复。
			report = append(report, fmt.Sprintf("MCP server %q 未写入附加层：WSL 全局 opencode 配置不可读（%s），同名冲突无法确认，为不覆盖本地选择未写入", name, globalReason))
			continue
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
	// GlobalConflict：distro 全局 opencode.json/jsonc 已有同名 server（默认
	// 保留本地；勾选覆盖才写入）。
	GlobalConflict bool
	// GlobalConflictUnknown：全局配置存在但不可读/不可解析（或目标探测失
	// 敗）——同名冲突无法确认（SA14）。该状态下所有 preset MCP 都不会写入
	// 附加层，也不提供覆盖勾选（先修复全局配置）。
	GlobalConflictUnknown bool
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
	globalNames, globalState, globalReason := readWSLOpenCodeGlobalMCPServers(distro)
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
		preview := OpenCodeMCPPreviewEntry{Name: name, GlobalConflict: globalNames[name], GlobalConflictUnknown: globalState == wslGlobalUnknown}
		if preview.GlobalConflictUnknown {
			preview.Reason = "WSL 全局 opencode 配置不可读（" + globalReason + "），同名冲突无法确认；不会写入附加层"
		}
		server, ok := servers[name].(map[string]any)
		if !ok {
			preview.NeedsAdapt = true
			preview.Reason = joinPreviewReason(preview.Reason, "server 定义不是对象")
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
			preview.Reason = joinPreviewReason(preview.Reason, reason)
		}
		out = append(out, preview)
	}
	return out
}

// joinPreviewReason 拼接预览原因（空段跳过）。
func joinPreviewReason(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "；" + b
	}
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
	// env 值中的独立盘符路径（P4-A3 对称补齐：其余 CLI 的 WSL MCP 落地面经
	// wslsupply.TransformMCPServer 全检，opencode 启动层原独漏 env——Windows
	// 路径值原样落附加层后在 Linux 侧失效，用户无从归因）。启动层保持轻量：
	// 只拒配不做映射（可信位置/已验证路径映射是 wslsupply 落地层的职责）；
	// 键名入诊断（键名不是密钥），值不回显；键序排序保证诊断确定性。
	if env, ok := entry["env"].(map[string]any); ok {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v, ok := env[k].(string); ok && isWindowsDrivePath(strings.TrimSpace(v)) {
				return false, nil, fmt.Sprintf("环境变量 %s 的值为 Windows 盘符路径，需手动适配为 WSL 内路径", k)
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

// wslGlobalReadState 是 distro 全局 OpenCode 配置的读取三态（SA14）。
type wslGlobalReadState int

const (
	// wslGlobalAbsent：opencode.json 与 opencode.jsonc 都不存在 → 无同名冲
	// 突可能（正常空集）。
	wslGlobalAbsent wslGlobalReadState = iota
	// wslGlobalReadable：存在且全部可解析 → 冲突名集可信。
	wslGlobalReadable
	// wslGlobalUnknown：存在但不可读/不可解析，或目标探测失败 → 冲突不可确
	// 认（「未知」≠「不冲突」，调用方保守处理）。
	wslGlobalUnknown
)

// readWSLOpenCodeGlobalMCPServers 读取 distro 全局 OpenCode 配置的 mcp
// server 名集合（冲突检查用）。SA14：
//   - 同时探测 ~/.config/opencode/opencode.json 与 opencode.jsonc（两个入口
//     都可能被 CLI 消费；并集对「保留本地」判定保守）；
//   - 完整 JSONC 解析（行/块注释与合法尾逗号都接受）；
//   - 读取/解析失败不再回退空集合冒充「无冲突」——返回 wslGlobalUnknown，
//     调用方按未知保守处理（默认不写入附加层）。
func readWSLOpenCodeGlobalMCPServers(distro string) (map[string]bool, wslGlobalReadState, string) {
	names := map[string]bool{}
	if strings.TrimSpace(distro) == "" {
		return names, wslGlobalUnknown, "WSL 发行版未知"
	}
	home := wslUserHomeFn(distro)
	if home == "" {
		return names, wslGlobalUnknown, "无法探测 distro 默认用户 HOME"
	}
	base := strings.TrimRight(home, "/") + "/.config/opencode"
	foundAny := false
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		unc := wslToUNCFn(distro, base+"/"+name)
		if unc == "" {
			return names, wslGlobalUnknown, name + " 的 WSL UNC 路径不可映射"
		}
		data, err := os.ReadFile(unc)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return names, wslGlobalUnknown, name + " 读取失败（" + err.Error() + "）"
		}
		if len(bytes.TrimSpace(data)) == 0 {
			continue // 空白文件不携带任何登记（与缺失同权重）
		}
		foundAny = true
		var parsed map[string]any
		if json.Unmarshal(data, &parsed) != nil {
			if json.Unmarshal(stripJSONCTrailingCommas(stripJSONCComments(data)), &parsed) != nil {
				return names, wslGlobalUnknown, name + " 不是可解析的 JSON/JSONC（注释与尾逗号剥离后仍失败）"
			}
		}
		mcp, _ := parsed["mcp"].(map[string]any)
		for n := range mcp {
			names[n] = true
		}
	}
	if !foundAny {
		return names, wslGlobalAbsent, ""
	}
	return names, wslGlobalReadable, ""
}

// stripJSONCComments 剥离 // 行注释与 /* */ 块注释（字符串字面量外的启发式
// 处理：逐字符扫描跟踪是否在字符串内；与 stripJSONCTrailingCommas 配合服务
// 冲突名提取，不追求完整 JSONC）。
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

// stripJSONCTrailingCommas 删除字符串字面量外的尾逗号（其后第一个非空白字
// 符为 } 或 ] 的逗号）——JSONC 合法形态（SA14：旧实现只剥注释，尾逗号使
// 解析失败回退空集，同名冲突被当「无冲突」）。
func stripJSONCTrailingCommas(data []byte) []byte {
	var out []byte
	out = make([]byte, 0, len(data))
	inString, escaped := false, false
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
		case c == ',':
			j := i + 1
			for j < len(data) {
				switch data[j] {
				case ' ', '\t', '\n', '\r':
					j++
					continue
				}
				break
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue // 尾逗号丢弃
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
