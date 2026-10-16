package wslsupply

// claude_adapter.go — Claude Code 用户配置导入的两个键级单元（P3-S4，design
// §四 Claude Code 表逐行）：
//
//   - claude/mcp：Windows ~/.claude.json 的 mcpServers/<name> server 级投影。
//     仅用户选择的 server 进入候选（selectLeaf + StateEntry.SelectedKeys）；
//     server 定义经 transform.go 确定规则转换（复用 S2 mcp 转换器与冲突框架：
//     unmanaged-existing / 手改冻结 / 双侧冲突 / 来源删除）。projects、trust、
//     onboarding、账户状态与一切未知键是 LocalOnly——desired 只从 mcpServers
//     抽取，绝不以「未排除」携带（design：「信任授权和首次使用状态不能从另一
//     环境继承」）。
//   - claude/settings：Windows ~/.claude/settings.json 字段级白名单偏好投影
//     （同 Pi settings 模式）。白名单只收已确认跨平台的标量偏好；自动信任、
//     放宽权限规则（permissions）、hooks、命令、宿主路径 env 不在白名单 =
//     永不导入（「设置可能授权执行」红线）。
//
// 两单元均需 per-target 同意；候选正文只在内存存在（复用 keyedProjectionAdapter
// 骨架：CAS 提交、冻结登记、预览接管）。

import (
	"fmt"
	"sort"
	"strings"
)

// claude 单元的目标文件（相对 distro HOME；CLI 目标根即 HOME，见 types.go）。
const (
	// ClaudeJSONFile：~/.claude.json（HOME 根文件——用户级 mcpServers 所在地）。
	ClaudeJSONFile = ".claude.json"
	// ClaudeSettingsFile：~/.claude/settings.json。
	ClaudeSettingsFile = ".claude/settings.json"

	claudeMCPTransform      = "claude-mcp-v1"
	claudeSettingsTransform = "claude-settings-v1"
)

// ClaudeConfigSource 是 App 注入的 Windows 侧来源（真实文件读取；缺失返回
// nil,nil——来源缺失是确定的「键集为空」，读失败才是 source-unavailable）。
type ClaudeConfigSource interface {
	// ClaudeJSON 返回 Windows ~/.claude.json 字节（可能很大——projects 等字段
	// 只在此解析，永不跨界；缺失返回 nil, nil）。
	ClaudeJSON() ([]byte, error)
	// SettingsJSON 返回 Windows ~/.claude/settings.json 字节。
	SettingsJSON() ([]byte, error)
}

// claudeSettingsWhitelist 是已确认跨平台的 Claude Code settings.json 偏好字段
// 白名单（标量偏好）。显式排除（LocalOnly，永不携带）：
//   - permissions / 自动信任：设置可能授权执行，不能整文件自动复制；
//   - hooks / 命令类字段：Windows 命令形态不可移植；
//   - env：宿主路径 env 不导入（跨平台 env 由 CodeBox 启动链管理）；
//   - 已装插件/主题状态、机器与会话状态、一切未知字段。
var claudeSettingsWhitelist = []string{"model", "outputStyle", "spinnerTipsEnabled", "cleanupPeriodDays", "includeCoAuthoredBy"}

var claudeMCPShape = keyedShape{
	relPath: ClaudeJSONFile,
	// 敏感度取舍：.claude.json 携带大量用户自有状态（projects 等）——chmod
	// 补偿失败时恢复前像（M2 普通单元语义，不整文件删除）；新文件场景的
	// 半提交同样清理，密钥副本不在宽松权限下新落盘。
	sensitive:   false,
	alwaysFacts: true, // 任何写入都需 shim/路径事实
	selectLeaf:  true, // 仅用户选择的 server 导入
	desired: func(obj map[string]any) (map[string]any, []string) {
		out := map[string]any{}
		var notes []string
		servers, ok := obj["mcpServers"].(map[string]any)
		if !ok {
			return out, nil // 无 mcpServers 或非对象：键集为空（目标校验把关）
		}
		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if strings.Contains(name, "/") {
				notes = append(notes, fmt.Sprintf("MCP server 名 %q 含路径分隔符，无法按键路径投影，请改名", name))
				continue
			}
			out["mcpServers/"+name] = servers[name]
		}
		return out, notes
	},
	convert: func(key string, v any, facts TransformFacts) (any, bool, string) {
		entry, ok := v.(map[string]any)
		if !ok {
			return nil, false, "server 定义不是对象，需手动适配"
		}
		result := TransformMCPServer(entry, facts)
		if result.Outcome == TransformRejected {
			return nil, false, result.Reason
		}
		return result.Entry, true, ""
	},
	validateTarget: func(obj map[string]any) error {
		if servers, ok := obj["mcpServers"]; ok {
			if _, isObj := servers.(map[string]any); !isObj {
				return fmt.Errorf(".claude.json mcpServers must be an object")
			}
		}
		return nil
	},
	summarizeEntry: summarizeMCPServerEntry,
}

var claudeSettingsShape = keyedShape{
	relPath:   ClaudeSettingsFile,
	sensitive: false,
	desired: func(obj map[string]any) (map[string]any, []string) {
		out := map[string]any{}
		for _, field := range claudeSettingsWhitelist {
			if v, ok := obj[field]; ok {
				out[field] = v
			}
		}
		return out, nil
	},
	convert: func(key string, v any, facts TransformFacts) (any, bool, string) {
		converted, ok := TransformSettingValue(v, facts)
		if !ok {
			return nil, false, "白名单字段携带不可映射的 Windows 路径值，本次剔除该字段"
		}
		return converted, true, ""
	},
	validateTarget: func(obj map[string]any) error {
		// 根为对象即可；白名单外字段形态属用户域，不做假设。
		return nil
	},
}

// NewClaudeMCPAdapter 创建 ~/.claude.json 的 mcpServers server 级投影单元
// （需 per-target 同意；仅选择集内的 server 导入）。
func NewClaudeMCPAdapter(source ClaudeConfigSource, facts TransformFactsSource) *keyedProjectionAdapter {
	return &keyedProjectionAdapter{
		unitID: UnitClaudeMCP, facts: facts,
		shape: claudeMCPShape, requiresConsent: true,
		readSource: source.ClaudeJSON,
	}
}

// NewClaudeSettingsAdapter 创建 ~/.claude/settings.json 字段级白名单投影单元
// （需 per-target 同意）。
func NewClaudeSettingsAdapter(source ClaudeConfigSource, facts TransformFactsSource) *keyedProjectionAdapter {
	return &keyedProjectionAdapter{
		unitID: UnitClaudeSettings, facts: facts,
		shape: claudeSettingsShape, requiresConsent: true,
		readSource: source.SettingsJSON,
	}
}

// summarizeMCPServerEntry 生成 MCP server 定义的脱敏单行摘要（S4 预览
// 「转换前→转换后」对照）：仅命令形态与参数个数，不含 URL 之外的 env 值与
// 密钥。after 侧基于给定 facts 复算转换（facts 为空时 shim 映射等不可确定，
// 返回「需实测」占位由调用方替换）。
func summarizeMCPServerEntry(key string, v any, facts TransformFacts) (before, after string) {
	entry, ok := v.(map[string]any)
	if !ok {
		return "server 定义非对象", "需手动适配"
	}
	before = summarizeMCPServerForm(entry)
	result := TransformMCPServer(shallowCopyAnyMap(entry), facts)
	if result.Outcome == TransformRejected {
		return before, result.Reason
	}
	return before, summarizeMCPServerForm(result.Entry)
}

// summarizeMCPServerForm 单行摘要：remote（域名不带 query）/ stdio 命令。
func summarizeMCPServerForm(entry map[string]any) string {
	if url, _ := entry["url"].(string); strings.TrimSpace(url) != "" {
		if u := strings.TrimSpace(url); u != "" {
			if i := strings.IndexAny(u, "?#"); i > 0 {
				u = u[:i]
			}
			return "远程 " + u
		}
	}
	command, _ := entry["command"].(string)
	args := 0
	if list, ok := entry["args"].([]any); ok {
		args = len(list)
	}
	kind := "stdio"
	if strings.TrimSpace(command) == "" {
		kind = "无命令"
	}
	if command == "" {
		return fmt.Sprintf("%s（参数 %d 个）", kind, args)
	}
	return fmt.Sprintf("%s %s（参数 %d 个）", kind, command, args)
}

// shallowCopyAnyMap 复制一个 map[string]any（转换摘要不污染原值）。
func shallowCopyAnyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
