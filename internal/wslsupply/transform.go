package wslsupply

// transform.go — 形态转换层（design §五 D3 确定规则，P3-S2 全量落地）。
//
// 转换器是纯函数：输入「原始条目 + 已验证事实（TransformFacts）」，输出候选
// 与诊断；自身不探测、不执行命令、不写文件。事实由调用方（Apply 阶段的
// FactsSource）预先经真实入口验证——登录 shell command -v（platform.
// WSLResolveCommands）、已部署资产前缀、wslpath 实测映射。多 CLI 复用同一套
// 命令/路径规则（Claude mcpServers / Codex mcp_servers / OpenCode mcp 的各自
// 解析在各自适配器，转换内核共享）。
//
// 确定规则（§五表逐行兑现，拒绝项保留脱敏原因与字段路径）：
//   - command 裸名 npx.cmd/npm.cmd → 仅事实中已验证原生 npx/npm 时映射；
//   - command C:\...\npx.cmd → 仅识别为已支持工具 shim（basename 命中）才替换
//     为已验证原生命令，否则拒绝该导入单元；
//   - cmd.exe/PowerShell/ps1/Windows exe/盘符路径 → 拒绝（需手动适配，不生成
//     假可用条目，不自动改 bash、不启用 interop）；
//   - args 中独立盘符路径 → 先匹配已部署资产/已验证映射，未验证即拒绝；
//   - env 仅明确声明为路径的键做转换（含 %USERPROFILE% 等在已确认基目录下
//     展开）；未知键携带 Windows 路径形态 → 阻止该条目并提示键名；
//   - HTTP/远程条目（带 url）保留 URL 与协议字段，localhost 不自动改宿主 IP；
//   - 一个 MCP server 是原子导入单元：转换失败该单元不新增不更新。

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// TransformOutcome 是单个转换单元的结果分类。
type TransformOutcome uint8

const (
	// TransformKept：原样可用（含 HTTP/远程与已验证的原生命令改写后条目）。
	TransformKept TransformOutcome = iota
	// TransformRejected：形态不可安全转换，需手动适配；该单元不新增不更新。
	TransformRejected
)

// TransformResult 是一次转换的产物（候选 + 脱敏诊断）。
type TransformResult struct {
	Outcome TransformOutcome
	// Entry 是转换后的条目（Rejected 时为 nil，调用方保留目标现状）。
	Entry map[string]any
	// Reason 是中文诊断（含字段路径，不含密钥/完整 env 值）。
	Reason string
}

// PathPrefixRule 是「已部署资产」前缀映射：Windows 前缀（任意分隔符形态、
// 大小写不敏感比较）→ 目标 Linux 前缀。例如 Windows ~/.pi/agent → distro 内
// ~/.pi/agent（本供给体系已发布/将发布的资产位置，结构上成立，无需逐文件探测）。
type PathPrefixRule struct {
	WindowsPrefix string // 形如 C:\Users\u\.pi\agent（结尾不带分隔符）
	LinuxPrefix   string // 形如 /home/u/.pi/agent
}

// TransformFacts 携带目标侧已验证事实（由 FactsSource 在 Apply 阶段收集；
// Plan 纯计算不触碰本结构）。
type TransformFacts struct {
	// ResolvedCommands：目标登录 shell 实测可解析的原生命令 → 绝对路径
	//（npx → /usr/bin/npx）。裸 shim 映射仅对在此出现的命令成立。
	ResolvedCommands map[string]string
	// PathMappings：经 wslpath 实测存在、用途允许的 Windows 绝对路径 → Linux
	// 路径（精确匹配；键为规范化形态，见 NormalizeWindowsPath）。
	PathMappings map[string]string
	// PrefixMappings：本供给体系已部署资产的前缀映射（优先于 PathMappings）。
	PrefixMappings []PathPrefixRule
	// HostHomeWindows 是 Windows 用户主目录（%USERPROFILE% 的唯一展开基；
	// %APPDATA%/%LOCALAPPDATA% 由此派生——标准布局，不猜测其它变量）。
	HostHomeWindows string
}

// TransformFactsSource 在 Apply 阶段收集目标事实（真实探测：登录 shell
// command -v + wslpath；Plan 不调用）。探测失败返回错误——调用方按「不可
// 确认」处理，不把失败当作已验证。
type TransformFactsSource interface {
	TransformFacts(ctx context.Context, target TargetRef) (TransformFacts, error)
}

// ---------------------------------------------------------------------------
// Windows 路径形态识别（纯函数）
// ---------------------------------------------------------------------------

// IsWindowsDrivePath 报告 s 是否为 Windows 盘符绝对路径（C:\... 或 C:/...）。
func IsWindowsDrivePath(s string) bool {
	if len(s) < 3 {
		return false
	}
	c := s[0]
	if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
		return false
	}
	return s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

// ContainsWindowsDriveRef 报告 s 是否内嵌盘符路径形态（如 "--config=C:\x"）。
// F-2（S2 diting）：排除合法 scheme 前缀（^[a-zA-Z][a-zA-Z0-9+.-]*://）——
// https:// / postgres:// 等 scheme 的尾字母+冒号+斜杠不是盘符（设计 §五
// 「不把 URL、token 或普通含冒号字符串当路径」）。实现为词首锚定：候选盘符
// 字母的前一字符若属 scheme 续集（字母/数字/+/-/.）则跳过。
func ContainsWindowsDriveRef(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			continue
		}
		if s[i+1] == ':' && (s[i+2] == '\\' || s[i+2] == '/') {
			if i > 0 && isSchemeContinuation(s[i-1]) {
				continue // scheme:// 的尾部（如 https: 的 s）——不是盘符
			}
			return true
		}
	}
	return false
}

// isSchemeContinuation 报告字符是否属于 scheme 名称的延续集（字母/数字/+/-/.）。
func isSchemeContinuation(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'
}

// containsWindowsVarPathRef 报告 s 是否含「%VAR% 后跟路径分隔符」的 Windows
// 变量路径形态（如 `%USERPROFILE%\\x`）。只认定 %…% 语法；${VAR} 占位符
// （运行时解析）不算。
func containsWindowsVarPathRef(s string) bool {
	for {
		open := strings.IndexByte(s, '%')
		if open < 0 {
			return false
		}
		close := strings.IndexByte(s[open+1:], '%')
		if close < 0 {
			return false
		}
		varName := s[open+1 : open+1+close]
		rest := s[open+1+close+1:]
		if varName != "" && isSchemeContinuation(varName[0]) && len(rest) > 0 && (rest[0] == '\\' || rest[0] == '/') {
			return true
		}
		s = s[open+1+close+1:]
	}
}

// NormalizeWindowsPath 规范化 Windows 路径用于映射查找：统一反斜杠、去结尾
// 分隔符、小写（NTFS 大小写不敏感）。保留原串另作前缀拆分。
func NormalizeWindowsPath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "\\/")
	// 相对段统一（./x → x）：仅影响查找键，不改写原值。
	s = strings.TrimPrefix(s, ".\\")
	s = strings.TrimPrefix(s, "./")
	return strings.ToLower(strings.ReplaceAll(s, "/", "\\"))
}

// MapWindowsPath 用已验证事实解析一个 Windows 路径：先已部署资产前缀，再
// wslpath 实测精确映射。未命中返回空串（调用方必须拒绝，不盲信 /mnt 挂载）。
func MapWindowsPath(raw string, facts TransformFacts) string {
	s := strings.TrimSpace(raw)
	if s == "" || !IsWindowsDrivePath(s) {
		return ""
	}
	// 前缀规则保留原大小写拆相对部分（Linux 侧大小写敏感）。
	for _, rule := range facts.PrefixMappings {
		win := strings.TrimRight(strings.ReplaceAll(rule.WindowsPrefix, "/", "\\"), "\\")
		if len(s) >= len(win) {
			head := strings.ReplaceAll(s[:len(win)], "/", "\\")
			if strings.EqualFold(head, win) && (len(s) == len(win) || s[len(win)] == '\\' || s[len(win)] == '/') {
				rel := strings.TrimLeft(s[len(win):], "\\/")
				if rel == "" {
					return strings.TrimRight(rule.LinuxPrefix, "/")
				}
				return strings.TrimRight(rule.LinuxPrefix, "/") + "/" + strings.ReplaceAll(rel, "\\", "/")
			}
		}
	}
	if facts.PathMappings != nil {
		if mapped, ok := facts.PathMappings[NormalizeWindowsPath(s)]; ok && mapped != "" {
			return mapped
		}
	}
	return ""
}

// expandKnownPathVars 展开 %USERPROFILE%/%APPDATA%/%LOCALAPPDATA%（仅这三个
// 标准基目录，且仅当 HostHomeWindows 已确认）。其它 %VAR% 返回 ok=false——
// 无法确定则要求用户提供 Linux 值，不执行 shell 展开。
func expandKnownPathVars(s string, facts TransformFacts) (string, bool) {
	home := strings.TrimSpace(facts.HostHomeWindows)
	if home == "" {
		if strings.Contains(s, "%") {
			return s, false
		}
		return s, true
	}
	home = strings.TrimRight(strings.ReplaceAll(home, "/", "\\"), "\\")
	appdata := home + "\\AppData\\Roaming"
	localappdata := home + "\\AppData\\Local"
	expanded := s
	for _, kv := range [][2]string{
		{"%USERPROFILE%", home},
		{"%APPDATA%", appdata},
		{"%LOCALAPPDATA%", localappdata},
	} {
		if strings.EqualFold(kv[0], "%USERPROFILE%") {
			expanded = replaceFoldVar(expanded, "%USERPROFILE%", kv[1])
		} else {
			expanded = replaceFoldVar(expanded, kv[0], kv[1])
		}
	}
	if strings.Contains(expanded, "%") {
		// 展开后仍有 %VAR% 残留（大小写变体之外的未知变量或嵌套）。
		return expanded, false
	}
	return expanded, true
}

// replaceFoldVar 大小写不敏感地替换 %var% 形态标记。
func replaceFoldVar(s, token, value string) string {
	lower := strings.ToLower(s)
	lt := strings.ToLower(token)
	out := s
	for {
		idx := strings.Index(lower, lt)
		if idx < 0 {
			return out
		}
		out = out[:idx] + value + out[idx+len(token):]
		lower = strings.ToLower(out)
	}
}

// ---------------------------------------------------------------------------
// MCP server 转换（§五表主体）
// ---------------------------------------------------------------------------

// mcpBareShimMap 是裸名 Windows shim → 目标原生命令的候选映射（仅此两个；
// 不把任意 .cmd 去后缀）。
var mcpBareShimMap = map[string]string{
	"npx.cmd": "npx",
	"npm.cmd": "npm",
}

// knownEnvPathKeys 是「明确声明为路径」的 env 键注册表（值类型）。
// 仅这些键做路径转换；注册表外携带 Windows 路径形态的值一律阻止该条目并
// 提示键名（不静默删 env 后声称 server 可用）。S2 收录 NODE_PATH（Windows
// `;` 分隔 ↔ Linux `:` 分隔的既有约定）；后续按需增补，不做启发式猜测。
var knownEnvPathKeys = map[string]string{
	"NODE_PATH": "list",
}

// TransformMCPServer 转换单个 MCP server 定义（纯函数）。远程/HTTP 条目（带
// url）原样保留；stdio 条目按 §五确定规则处理 command/args/env。转换被拒绝
// 时 Entry 为 nil——调用方保持该单元目标现状（不新增不更新）。
func TransformMCPServer(entry map[string]any, facts TransformFacts) TransformResult {
	if entry == nil {
		return TransformResult{Outcome: TransformKept, Entry: entry}
	}
	if url, _ := entry["url"].(string); strings.TrimSpace(url) != "" {
		// HTTP/远程：保留 URL、认证与协议字段；localhost 不自动改宿主 IP，
		// 网络可达性是独立问题。
		return TransformResult{Outcome: TransformKept, Entry: entry}
	}
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		out[k] = v
	}
	command, _ := entry["command"].(string)
	trimmed := strings.TrimSpace(command)

	// command 处理。
	switch {
	case trimmed == "":
		// 无 command 也无 url：交给消费方自行处理，不猜测。
	case isBareShim(trimmed):
		native := mcpBareShimMap[strings.ToLower(trimmed)]
		if path, ok := facts.ResolvedCommands[native]; ok && strings.HasPrefix(path, "/") {
			out["command"] = native
		} else {
			return TransformResult{Outcome: TransformRejected, Reason: fmt.Sprintf(
				"命令 %s 未在 WSL 登录 shell 验证到原生 %s，需手动适配", trimmed, native)}
		}
	case IsWindowsDrivePath(trimmed):
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(trimmed, `\`, "/")))
		if native, isShim := mcpBareShimMap[base]; isShim && isRecognizedShimLocation(trimmed, facts) {
			// 已支持工具 shim 的标准安装位置（nodejs 目录 / npm 全局 bin）：仅目标
			// 验证过原生命令才替换。其它位置的同名 .cmd 不信任（文件名相同不证明
			// 行为等价，design §五）。
			if path, ok := facts.ResolvedCommands[native]; ok && strings.HasPrefix(path, "/") {
				out["command"] = native
			} else {
				return TransformResult{Outcome: TransformRejected, Reason: fmt.Sprintf(
					"命令 %s 识别为 %s shim，但 WSL 未验证到原生 %s，需手动适配", filepath.Base(trimmed), native, native)}
			}
		} else {
			return TransformResult{Outcome: TransformRejected, Reason: fmt.Sprintf(
				"命令 %q 为 Windows 路径且非已支持工具 shim 的标准位置，需手动适配", filepath.Base(trimmed))}
		}
	case isWindowsShellForm(trimmed):
		return TransformResult{Outcome: TransformRejected, Reason: fmt.Sprintf(
			"命令 %q 为 Windows 专属形态（cmd/PowerShell/exe/脚本），需手动适配为 Linux 命令", trimmed)}
	default:
		// 非 Windows 形态命令（裸名或 Linux 路径）：原样保留（存在性验证属
		// 运行态，不在静态转换面内冒充）。
	}

	// args 处理：独立盘符路径经已验证映射转换；内嵌路径形态拒绝。
	if args, ok := entry["args"].([]any); ok {
		nextArgs := make([]any, len(args))
		for i, raw := range args {
			s, isStr := raw.(string)
			if !isStr {
				nextArgs[i] = raw
				continue
			}
			t := strings.TrimSpace(s)
			if IsWindowsDrivePath(t) {
				mapped := MapWindowsPath(t, facts)
				if mapped == "" {
					return TransformResult{Outcome: TransformRejected, Reason: fmt.Sprintf(
						"参数 args[%d] 的 Windows 路径（%s）未经 WSL 验证映射，需手动适配", i, filepath.Base(t))}
				}
				nextArgs[i] = mapped
				continue
			}
			if ContainsWindowsDriveRef(t) {
				return TransformResult{Outcome: TransformRejected, Reason: fmt.Sprintf(
					"参数 args[%d] 内嵌 Windows 盘符路径，需手动适配", i)}
			}
			nextArgs[i] = raw
		}
		out["args"] = nextArgs
	}

	// env 处理：仅声明为路径的键转换；未知键携带 Windows 路径形态则阻止。
	if env, ok := entry["env"].(map[string]any); ok && len(env) > 0 {
		nextEnv := make(map[string]any, len(env))
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := env[k]
			s, isStr := v.(string)
			if !isStr {
				nextEnv[k] = v
				continue
			}
			kind, declared := knownEnvPathKeys[strings.ToUpper(k)]
			if declared {
				converted, ok := convertEnvPathValue(s, kind, facts)
				if !ok {
					return TransformResult{Outcome: TransformRejected, Reason: fmt.Sprintf(
						"env 键 %s 是已声明路径键，但其值无法映射到 WSL 路径，需手动适配", k)}
				}
				nextEnv[k] = converted
				continue
			}
			t := strings.TrimSpace(s)
			if IsWindowsDrivePath(t) || ContainsWindowsDriveRef(t) {
				return TransformResult{Outcome: TransformRejected, Reason: fmt.Sprintf(
					"env 键 %s 的值携带 Windows 路径形态（该键未声明为路径键），需手动适配或提供 Linux 值", k)}
			}
			// F-5（S2 diting）：未声明键的 %VAR%+分隔符形态（如 %USERPROFILE%\\x）
			// 同样是运行时不可用的 Windows 变量路径——无法确定则要求用户提供
			// Linux 值（§五），点名键名拒绝该条目。${VAR} 占位符（运行时解析）
			// 与裸 %VAR%（无路径形态）不受影响。
			if containsWindowsVarPathRef(t) {
				return TransformResult{Outcome: TransformRejected, Reason: fmt.Sprintf(
					"env 键 %s 的值携带 %%VAR%% 变量路径形态（该键未声明为路径键），无法确定 Linux 值，需手动适配", k)}
			}
			nextEnv[k] = v
		}
		out["env"] = nextEnv
	}
	return TransformResult{Outcome: TransformKept, Entry: out}
}

// convertEnvPathValue 转换一个已声明路径键的值：展开已知变量 → 逐段映射。
// list 语义（如 NODE_PATH）：`;` 分隔逐段转换后以 `:` 连接。
func convertEnvPathValue(s, kind string, facts TransformFacts) (string, bool) {
	expanded, ok := expandKnownPathVars(s, facts)
	if !ok {
		return "", false
	}
	if kind == "list" {
		parts := strings.Split(expanded, ";")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			mapped := convertSinglePathSegment(p, facts)
			if mapped == "" {
				return "", false
			}
			out = append(out, mapped)
		}
		if len(out) == 0 {
			return "", false
		}
		return strings.Join(out, ":"), true
	}
	return convertSinglePathSegment(expanded, facts), true
}

// convertSinglePathSegment 转换单段路径值：Linux 路径原样；Windows 路径须
// 经已验证映射，否则返回空串（拒绝）。
func convertSinglePathSegment(s string, facts TransformFacts) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	if !IsWindowsDrivePath(t) {
		return t // 已是 Linux 形态：原样
	}
	return MapWindowsPath(t, facts)
}

// isRecognizedShimLocation 报告盘符路径命令是否位于已支持工具 shim 的标准
// 安装位置：nodejs 目录（npm 安装器标准位置）或 npm 全局 bin（%APPDATA%\npm）。
// 其它位置的同名文件不信任。
func isRecognizedShimLocation(path string, facts TransformFacts) bool {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "/", `\`))
	if strings.Contains(norm, `\nodejs\`) {
		return true
	}
	home := strings.ToLower(strings.TrimSpace(facts.HostHomeWindows))
	if home != "" {
		home = strings.TrimRight(strings.ReplaceAll(home, "/", `\`), `\`)
		if strings.HasPrefix(norm, home+`\appdata\roaming\npm`) {
			return true
		}
	}
	return false
}

// isBareShim 报告命令是否为已支持裸名 shim（npx.cmd/npm.cmd，大小写不敏感）。
func isBareShim(cmd string) bool {
	_, ok := mcpBareShimMap[strings.ToLower(strings.TrimSpace(cmd))]
	return ok
}

// isWindowsShellForm 报告命令是否为 Windows 专属形态：cmd/PowerShell 家族
// 基名、.exe/.ps1/.bat/.cmd 后缀、盘符路径由调用方先行分支。
func isWindowsShellForm(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(lower, `\`, "/")))
	switch base {
	case "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return true
	}
	return strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".ps1") ||
		strings.HasSuffix(lower, ".bat") || strings.HasSuffix(lower, ".cmd")
}

// ---------------------------------------------------------------------------
// 设置字段值转换（白名单字段内的绝对路径：经映射转换或剔除）
// ---------------------------------------------------------------------------

// TransformSettingValue 转换白名单设置字段的值子树：字符串叶子若为独立
// Windows 路径则经已验证映射改写；内嵌路径形态或不可映射 → ok=false（调用方
// 剔除该字段并记诊断——不在任意正文中做全局替换，也不静默保留 Windows 路径）。
func TransformSettingValue(v any, facts TransformFacts) (any, bool) {
	switch t := v.(type) {
	case string:
		trimmed := strings.TrimSpace(t)
		if !IsWindowsDrivePath(trimmed) {
			if ContainsWindowsDriveRef(trimmed) {
				return nil, false // 内嵌路径：不猜测改写，剔除
			}
			return t, true
		}
		mapped := MapWindowsPath(trimmed, facts)
		if mapped == "" {
			return nil, false
		}
		return mapped, true
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			converted, ok := TransformSettingValue(child, facts)
			if !ok {
				return nil, false
			}
			out[k] = converted
		}
		return out, true
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			converted, ok := TransformSettingValue(child, facts)
			if !ok {
				return nil, false
			}
			out[i] = converted
		}
		return out, true
	default:
		return v, true
	}
}

// ---------------------------------------------------------------------------
// 事实收集辅助（供 FactsSource 预扫描：纯函数，探测由调用方执行）
// ---------------------------------------------------------------------------

// CollectTransformNeeds 扫描一组 MCP server 定义，返回需要探测的原生命令与
// 需要映射验证的 Windows 路径（args/env 值中独立盘符路径；已由 PrefixMappings
// 覆盖的路径仍会列出——探测幂等，重复验证无害）。
func CollectTransformNeeds(entries []map[string]any) (commands []string, paths []string) {
	cmdSet := map[string]bool{}
	pathSet := map[string]bool{}
	addPath := func(s string) {
		t := strings.TrimSpace(s)
		if IsWindowsDrivePath(t) {
			pathSet[t] = true
		}
	}
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if url, _ := entry["url"].(string); strings.TrimSpace(url) != "" {
			continue
		}
		command, _ := entry["command"].(string)
		trimmed := strings.TrimSpace(command)
		switch {
		case isBareShim(trimmed):
			cmdSet[mcpBareShimMap[strings.ToLower(trimmed)]] = true
		case IsWindowsDrivePath(trimmed):
			if native, ok := mcpBareShimMap[strings.ToLower(filepath.Base(strings.ReplaceAll(trimmed, `\`, "/")))]; ok {
				cmdSet[native] = true
			}
		}
		if args, ok := entry["args"].([]any); ok {
			for _, raw := range args {
				if s, isStr := raw.(string); isStr {
					addPath(s)
				}
			}
		}
		if env, ok := entry["env"].(map[string]any); ok {
			for k, v := range env {
				s, isStr := v.(string)
				if !isStr {
					continue
				}
				if _, declared := knownEnvPathKeys[strings.ToUpper(k)]; declared {
					for _, part := range strings.Split(s, ";") {
						addPath(part)
					}
				}
			}
		}
	}
	commands = sortedKeys(cmdSet)
	paths = sortedKeys(pathSet)
	return commands, paths
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
