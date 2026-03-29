package proxy

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// maxToolNameLen 是工具名的最大字节长度阈值（使用 len() 字节比较）。
// 百炼 API 的限制是字符数 ≤ 64；对纯 ASCII 工具名，字节数 = 字符数，两者等价。
const maxToolNameLen = 64

// toolNameMapping 存储工具名的双向映射（原名↔短名）。
// 用于适配百炼等对工具名长度有限制的 API。
type toolNameMapping struct {
	toShort    map[string]string // 原名 → 短名
	toOriginal map[string]string // 短名 → 原名
}

func newToolNameMapping() *toolNameMapping {
	return &toolNameMapping{
		toShort:    make(map[string]string),
		toOriginal: make(map[string]string),
	}
}

func (m *toolNameMapping) isEmpty() bool {
	return len(m.toShort) == 0
}

// shortenToolName 将超长工具名截断为 64 字节（ASCII字符）。
// 格式：前47字节（按rune边界安全截断）+ "_" + 16位十六进制哈希 = 最多64字节
func shortenToolName(name string) string {
	h := sha256.Sum256([]byte(name))
	suffix := "_" + fmt.Sprintf("%x", h[:8]) // 17字符
	prefix := safeTruncateBytes(name, 47)
	return prefix + suffix
}

// safeTruncateBytes 将字符串截断到不超过 maxBytes 字节，保证不在 rune 中间截断。
func safeTruncateBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// 从 maxBytes 往前找合法的 rune 边界
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// rewriteRequestToolNames 扫描请求体中的 tools 数组和消息历史，
// 将超过 64 字符的工具名替换为短名。返回修改后的请求体和映射表。
func rewriteRequestToolNames(body []byte) ([]byte, *toolNameMapping) {
	mapping := newToolNameMapping()

	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return body, mapping
	}

	modified := body

	// 1. 替换 tools[].name 中超长的名称
	for i, tool := range tools.Array() {
		name := tool.Get("name").String()
		if len(name) <= maxToolNameLen {
			continue
		}
		short := shortenToolName(name)
		mapping.toShort[name] = short
		mapping.toOriginal[short] = name

		var err error
		modified, err = sjson.SetBytes(modified, fmt.Sprintf("tools.%d.name", i), short)
		if err != nil {
			return body, newToolNameMapping()
		}
	}

	if mapping.isEmpty() {
		return body, mapping
	}

	// 2. 替换消息历史中 assistant 消息的 tool_use 块的 name
	messages := gjson.GetBytes(modified, "messages")
	if messages.Exists() && messages.IsArray() {
		for i, msg := range messages.Array() {
			content := msg.Get("content")
			if !content.IsArray() {
				continue
			}
			for j, block := range content.Array() {
				if block.Get("type").String() == "tool_use" {
					name := block.Get("name").String()
					if short, ok := mapping.toShort[name]; ok {
						modified, _ = sjson.SetBytes(modified,
							fmt.Sprintf("messages.%d.content.%d.name", i, j), short)
					}
				}
			}
		}
	}

	fmt.Printf("[amagi-codebox proxy] rewritten %d tool name(s) exceeding %d chars\n",
		len(mapping.toShort), maxToolNameLen)

	return modified, mapping
}

// restoreToolNamesInBytes 在响应数据中将短名替换回原名。
// 通过替换 JSON 中的 "short" → "original" 实现，适用于任意字节切片。
func restoreToolNamesInBytes(data []byte, mapping *toolNameMapping) []byte {
	if mapping.isEmpty() {
		return data
	}
	s := string(data)
	for short, original := range mapping.toOriginal {
		s = strings.ReplaceAll(s, `"`+short+`"`, `"`+original+`"`)
	}
	return []byte(s)
}

// writeResponseWithRestore 将后端响应写回客户端，同时恢复工具名。
// 自动检测 SSE 流式响应和普通 JSON 响应，分别处理。
// onUsage 回调在响应处理完成后调用（可为 nil）。
func writeResponseWithRestore(w http.ResponseWriter, resp *http.Response, mapping *toolNameMapping, onUsage func(*UsageData)) {
	// 复制响应头（排除需要重新计算的头）
	for h, v := range resp.Header {
		lower := strings.ToLower(h)
		if lower == "connection" || lower == "transfer-encoding" || lower == "content-length" {
			continue
		}
		w.Header()[h] = v
	}

	isSSE := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")

	if isSSE {
		// SSE 流式响应：逐行处理，保持流式传输
		w.WriteHeader(resp.StatusCode)
		flusher, canFlush := w.(http.Flusher)
		accumulator := NewSSEUsageAccumulator()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			accumulator.ProcessLine(string(line))
			restored := restoreToolNamesInBytes(line, mapping)
			w.Write(restored)
			w.Write([]byte("\n"))
			if canFlush {
				flusher.Flush()
			}
		}
		if onUsage != nil {
			onUsage(accumulator.GetUsage())
		}
	} else {
		// 普通 JSON 响应：整体缓冲后替换
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `{"error":"read response: %s"}`, err.Error())
			return
		}

		if onUsage != nil {
			onUsage(parseUsageFromJSON(body))
		}
		restored := restoreToolNamesInBytes(body, mapping)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(restored)))
		w.WriteHeader(resp.StatusCode)
		w.Write(restored)
	}
}
