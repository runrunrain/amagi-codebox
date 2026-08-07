package ompplugin

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

// pluginListEnvelope 是 `omp plugin list --json` 输出（首个 JSON 段）的顶层结构。
// 两个桶均可能缺失或为 null（旧版本），字段级容错由 gjson 解析兜底。
type pluginListEnvelope struct {
	Npm         []json.RawMessage `json:"npm"`
	Marketplace []json.RawMessage `json:"marketplace"`
}

// 插件源类型常量（与 omp 输出语义一致）。
const (
	pluginKindNPM         = "npm"
	pluginKindMarketplace = "marketplace"
)

// parsePluginList 解析 `omp plugin list --json` 输出为插件列表（四段降级）：
//
//  1. 截取首个 `{` 到末个 `}` 的 JSON 段并 unmarshal 到 npm/marketplace 两桶；
//  2. npm 条目用 gjson 字段级容错：name/version/enabled(缺省 true)/enabledFeatures/
//     manifest.description/path；
//  3. marketplace 条目：id/scope/shadowedBy/entries[0].version/entries[0].enabled(缺省 true)；
//  4. 降级：空输出 → 空列表；无 JSON 段且含 "No plugins installed" → 空列表 + Warning
//     （旧版 omp 不支持 --json 或输出人类文本）；解析失败 → error 携带截断 500
//     字符的原始输出。
//
// 排序：按 name 升序（npm 在前，marketplace 在后）。
func parsePluginList(output string) ([]Plugin, []string, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return []Plugin{}, nil, nil
	}

	start := strings.IndexByte(trimmed, '{')
	end := strings.LastIndexByte(trimmed, '}')
	if start < 0 || end < start {
		// 无 JSON 段：命中空态人类输出则降级为空列表，否则按解析失败处理。
		if strings.Contains(trimmed, "No plugins installed") {
			return []Plugin{}, []string{"omp 版本较旧，已按空列表处理"}, nil
		}
		return nil, nil, fmt.Errorf("解析 omp plugin list 输出失败（非 JSON）: %s", truncateOutput(trimmed))
	}
	segment := trimmed[start : end+1]
	var envelope pluginListEnvelope
	if err := json.Unmarshal([]byte(segment), &envelope); err != nil {
		if strings.Contains(trimmed, "No plugins installed") {
			return []Plugin{}, []string{"omp 版本较旧，已按空列表处理"}, nil
		}
		return nil, nil, fmt.Errorf("解析 omp plugin list 输出失败: %s", truncateOutput(segment))
	}

	plugins := make([]Plugin, 0, len(envelope.Npm)+len(envelope.Marketplace))
	for _, raw := range envelope.Npm {
		if p, ok := parseNpmPlugin(raw); ok {
			plugins = append(plugins, p)
		}
	}
	for _, raw := range envelope.Marketplace {
		if p, ok := parseMarketplacePlugin(raw); ok {
			plugins = append(plugins, p)
		}
	}
	sort.Slice(plugins, func(i, j int) bool {
		if plugins[i].Kind != plugins[j].Kind {
			return plugins[i].Kind == pluginKindNPM // npm 在前
		}
		return strings.ToLower(plugins[i].Name) < strings.ToLower(plugins[j].Name)
	})
	return plugins, nil, nil
}

// parseNpmPlugin 字段级容错解析一个 npm 插件条目。
// name 缺失即视为非法条目跳过（无 id 无法展示/操作）。
func parseNpmPlugin(raw json.RawMessage) (Plugin, bool) {
	item := gjson.ParseBytes(raw)
	name := strings.TrimSpace(item.Get("name").String())
	if name == "" {
		return Plugin{}, false
	}
	enabled := item.Get("enabled")
	p := Plugin{
		ID:              name,
		Name:            name,
		Version:         strings.TrimSpace(item.Get("version").String()),
		Kind:            pluginKindNPM,
		Enabled:         enabledDefaultTrue(enabled),
		EnabledFeatures: toStringSlice(item.Get("enabledFeatures")),
		Description:     strings.TrimSpace(item.Get("manifest.description").String()),
		InstallPath:     strings.TrimSpace(item.Get("path").String()),
	}
	return p, true
}

// parseMarketplacePlugin 字段级容错解析一个 marketplace 插件条目。
// 每个插件仅取 entries[0]（omp 的 entries 首个即当前生效版本）。
func parseMarketplacePlugin(raw json.RawMessage) (Plugin, bool) {
	item := gjson.ParseBytes(raw)
	id := strings.TrimSpace(item.Get("id").String())
	if id == "" {
		return Plugin{}, false
	}
	enabled := item.Get("entries.0.enabled")
	p := Plugin{
		ID:      id,
		Name:    id, // marketplace 无独立短名，展示与操作均用 pluginId
		Version: strings.TrimSpace(item.Get("entries.0.version").String()),
		Kind:    pluginKindMarketplace,
		Enabled: enabledDefaultTrue(enabled),
		Scope:   strings.TrimSpace(item.Get("scope").String()),
	}
	// shadowedBy 字段（被更高优先级条目遮蔽）当前仅容错跳过：types.go 契约
	// 无对应字段，前端不消费，缺失或任意值均不影响解析。
	return p, true
}

// enabledDefaultTrue 解析布尔开关字段：缺失或显式 null 均视为启用（缺省 true），
// 与 omp 输出"未记录即默认启用"的语义一致。
func enabledDefaultTrue(result gjson.Result) bool {
	return !result.Exists() || result.Type == gjson.Null || result.Bool()
}

// toStringSlice 把 gjson 数组转为 []string（非数组返回 nil）。
func toStringSlice(result gjson.Result) []string {
	if !result.IsArray() {
		return nil
	}
	arr := result.Array()
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if v.Type == gjson.String {
			if s := strings.TrimSpace(v.String()); s != "" {
				out = append(out, s)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// truncateOutput 截断原始输出到 500 字符，避免错误信息携带超长 CLI 输出。
func truncateOutput(s string) string {
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(截断)"
}
