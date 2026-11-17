package sharedext

// json_edit.go — MCP 目标 JSON 配置的精准编辑（gjson/sjson），以及 def 的
// canonical 比较 / 深拷贝辅助。~/.claude.json 是 MB 级大文件，必须用
// SetBytes/DeleteBytes 原地编辑，不得 unmarshal 整文件。

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// gjsonGetServer 读取 JSON 配置中 <key>.<name> 的 server def；
// 键不存在返回 nil。
func gjsonGetServer(data []byte, key, name string) map[string]any {
	path := key + "." + escapeJSONKey(name)
	result := gjson.GetBytes(data, path)
	if !result.Exists() || !result.IsObject() {
		return nil
	}
	var def map[string]any
	if err := json.Unmarshal([]byte(result.Raw), &def); err != nil {
		return nil
	}
	return def
}

// sjsonSetServer 在 JSON 配置中精准写入 <key>.<name> = def（原地字节编辑）。
func sjsonSetServer(data []byte, key, name string, def map[string]any) ([]byte, error) {
	updated, err := sjson.SetBytes(data, key+"."+escapeJSONKey(name), def)
	if err != nil {
		return nil, fmt.Errorf("写入 MCP server 键: %w", err)
	}
	return updated, nil
}

// sjsonDeleteServer 从 JSON 配置中精准删除 <key>.<name>。
func sjsonDeleteServer(data []byte, key, name string) ([]byte, error) {
	updated, err := sjson.DeleteBytes(data, key+"."+escapeJSONKey(name))
	if err != nil {
		return nil, fmt.Errorf("删除 MCP server 键: %w", err)
	}
	return updated, nil
}

// escapeJSONKey 为含点/反斜杠的 server 名生成 gjson/sjson 安全路径段：
// 点号会被解析为嵌套路径，必须转义为 \.，反斜杠转义为 \\。
func escapeJSONKey(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch r {
		case '.', '#', '@', '|', ':', '?', '*', '>' :
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case '\\':
			sb.WriteString(`\\`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// mcpDefEqual 以 canonical JSON 比较（键序无关）两个 server def。
func mcpDefEqual(a, b map[string]any) bool {
	ab, errA := json.Marshal(canonicalize(a))
	bb, errB := json.Marshal(canonicalize(b))
	if errA != nil || errB != nil {
		return false
	}
	return string(ab) == string(bb)
}

// canonicalize 递归规范化 map 键序（json.Marshal 已按键序输出 map，此处
// 主要把 float64/int 等数值形态统一）。
func canonicalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = canonicalize(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = canonicalize(val)
		}
		return out
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case float32:
		return float64(t)
	default:
		return v
	}
}

// deepCopyMCPConfig 深拷贝 server def（防外泄引用：导入后调用方改原 map
// 不影响 registry）。
func deepCopyMCPConfig(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMCPConfig(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = deepCopyValue(item)
		}
		return out
	case []string:
		return append([]string(nil), t...)
	default:
		return v
	}
}

// nowUnix 返回当前 Unix 秒（备份文件命名用；变量化供测试注入不同时间戳）。
var nowUnix = func() int64 { return time.Now().Unix() }
