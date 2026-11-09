package client

// output.go — 输出层：--json 包络（stdout 仅单个 Envelope JSON，诊断走 stderr）、
// 人类模式（对象数组表格 / 其余键值缩进）、错误渲染（带 hint）与退出码映射。
// 令牌绝不出现在任何输出。

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
)

// writeEnvelopeJSON 把单个 Envelope JSON 写入 w（--json 模式的唯一 stdout 输出）。
func writeEnvelopeJSON(w io.Writer, env spec.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// printHumanError 人类模式渲染错误（含错误码与 hint）。
func printHumanError(w io.Writer, e *spec.Error) {
	if e == nil {
		return
	}
	fmt.Fprintf(w, "错误: %s\n", e.Message)
	if e.Code != "" {
		fmt.Fprintf(w, "错误码: %s\n", e.Code)
	}
	if e.Hint != "" {
		fmt.Fprintf(w, "提示: %s\n", e.Hint)
	}
}

// exitCodeFor 按稳定错误码映射退出码（DESIGN §2）：
// bad_args/unknown_command/forbidden_command→2；unauthorized/forbidden/cli_disabled→3；
// confirmation_required→4；其余业务错误→1。
func exitCodeFor(code string) int {
	switch code {
	case spec.ErrBadArgs, spec.ErrUnknownCommand, spec.ErrForbiddenCommand:
		return spec.ExitUsage
	case spec.ErrUnauthorized, spec.ErrForbidden, spec.ErrDisabled:
		return spec.ExitUnreachable
	case spec.ErrConfirmationRequired:
		return spec.ExitConfirm
	default:
		return spec.ExitBusiness
	}
}

// renderData 人类模式渲染成功数据。
func renderData(w io.Writer, data any) {
	switch v := data.(type) {
	case nil:
		// 无数据：不输出
	case string:
		fmt.Fprintln(w, v)
	case bool, float64:
		fmt.Fprintln(w, scalarText(v))
	case []any:
		renderSlice(w, v)
	case map[string]any:
		renderMap(w, v, "")
	default:
		b, _ := json.Marshal(v)
		fmt.Fprintln(w, string(b))
	}
}

func renderSlice(w io.Writer, items []any) {
	if len(items) == 0 {
		fmt.Fprintln(w, "（空列表，0 项）")
		return
	}
	allObjects := true
	for _, it := range items {
		if _, ok := it.(map[string]any); !ok {
			allObjects = false
			break
		}
	}
	if allObjects {
		rows := make([]map[string]any, 0, len(items))
		for _, it := range items {
			rows = append(rows, it.(map[string]any))
		}
		renderTable(w, rows)
		return
	}
	for _, it := range items {
		switch t := it.(type) {
		case map[string]any:
			b, _ := json.Marshal(t)
			fmt.Fprintln(w, string(b))
		case []any:
			fmt.Fprintf(w, "[%d项]\n", len(t))
		default:
			fmt.Fprintln(w, scalarText(t))
		}
	}
}

// renderMap 键值缩进渲染；对象第一层唯一数组字段命中表格形态时先打标量键再渲染表格。
func renderMap(w io.Writer, m map[string]any, indent string) {
	if key, rows, ok := soleObjectArray(m); ok {
		for _, k := range sortedMapKeys(m) {
			if k == key {
				continue
			}
			fmt.Fprintf(w, "%s%s: %s\n", indent, k, valueSummary(m[k]))
		}
		renderTable(w, rows)
		return
	}
	for _, k := range sortedMapKeys(m) {
		v := m[k]
		if t, ok := v.(map[string]any); ok {
			fmt.Fprintf(w, "%s%s:\n", indent, k)
			renderMap(w, t, indent+"  ")
			continue
		}
		fmt.Fprintf(w, "%s%s: %s\n", indent, k, valueSummary(v))
	}
}

// soleObjectArray 检测“对象第一层唯一数组字段”表格形态：恰好一个字段是
// 非空对象数组。
func soleObjectArray(m map[string]any) (string, []map[string]any, bool) {
	var key string
	var rows []map[string]any
	for k, v := range m {
		arr, ok := v.([]any)
		if !ok {
			continue
		}
		if key != "" {
			return "", nil, false // 多个数组字段：不命中
		}
		if len(arr) == 0 {
			continue
		}
		ok = true
		for _, it := range arr {
			if _, isObj := it.(map[string]any); !isObj {
				ok = false
				break
			}
		}
		if !ok {
			return "", nil, false
		}
		key = k
		for _, it := range arr {
			rows = append(rows, it.(map[string]any))
		}
	}
	return key, rows, key != ""
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// scalarText 标量文本（JSON 数字为 float64；整数不带 .0）。
func scalarText(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return formatNumber(t)
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func formatNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func isScalar(v any) bool {
	switch v.(type) {
	case nil, bool, float64, string:
		return true
	}
	return false
}

// valueSummary 键值模式的值摘要：标量直陈；短标量数组内联；其余 {…}/[n项]。
func valueSummary(v any) string {
	switch t := v.(type) {
	case map[string]any:
		return "{…}"
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		allScalar := true
		for _, it := range t {
			if !isScalar(it) {
				allScalar = false
				break
			}
		}
		if allScalar && len(t) <= 5 {
			parts := make([]string, 0, len(t))
			for _, it := range t {
				parts = append(parts, scalarText(it))
			}
			return strings.Join(parts, ", ")
		}
		return fmt.Sprintf("[%d项]", len(t))
	default:
		return scalarText(v)
	}
}
