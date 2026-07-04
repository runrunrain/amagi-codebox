package pty

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// firstOutputMaxRunes 首输出摘要的 rune 计数上限（侧栏单行展示）。
const firstOutputMaxRunes = 60

// 首输出最小有效行长度（trim 后）。
const firstOutputMinLineRunes = 4

// firstOutputByteThreshold 触发首输出提取的累积字节量阈值（先到先触发）。
const firstOutputByteThreshold = 512

// firstOutputTimeout 触发首输出提取的超时阈值，距会话启动超过此时长即提取（先到先触发）。
const firstOutputTimeout = 3 * time.Second

var (
	// csiRe 清洗 CSI 序列（如颜色、光标控制）：ESC [ 参数 中间字节 终止字节。
	csiRe = regexp.MustCompile(`\x1B\[[0-?]*[ -/]*[@-~]`)
	// oscRe 清洗 OSC 序列（如窗口标题、超链接）：ESC ] ... BEL 或 ESC ] ... ESC \。
	oscRe = regexp.MustCompile(`\x1B\][^\x07\x1B]*(?:\x07|\x1B\\)`)
	// 其他单字符 ESC 转义（如 ESC =、ESC >、ESC ? 等私有 Fe/Fp 序列，及 ESC @ ... ESC _ 标准 Fe 序列）。
	// 覆盖 ESC 后跟任意 ASCII 可打印字节（0x20~0x7E）。
	escSingleRe = regexp.MustCompile(`\x1B[\x20-\x7E]`)
	// shell 提示符特征行：PS <路径>>、裸 >、$、# 等；同时容忍行尾残留空白。
	shellPromptRe = regexp.MustCompile(`^(?:PS\s+.*>\s*$|>{1,3}\s*$|[$#]\s*$)`)
)

// StripAnsi 清洗字符串中的 ANSI 转义序列并将 \r\n、\r 规范为 \n。
// 序列分类参考 ECMA-48 / xterm 文档：
//   - CSI: Control Sequence Introducer
//   - OSC: Operating System Command
//   - 其他单字节 ESC 转义（Fe/Escape 序列）
func StripAnsi(s string) string {
	s = oscRe.ReplaceAllString(s, "")
	s = csiRe.ReplaceAllString(s, "")
	s = escSingleRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// isBlankLine 判断字符串是否仅包含空白字符（含 Unicode 空白）。
func isBlankLine(s string) bool {
	return strings.TrimSpace(s) == ""
}

// truncateRunes 按 rune 截断字符串到 max；超出部分以省略号结尾。
// 输入必须为有效 UTF-8；非法字节会被 utf8.RuneCountInString 当作单 rune。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	// 保留前 max-1 个 rune，末尾追加 …
	runes := []rune(s)
	if max-1 <= 0 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

// ExtractFirstMeaningfulLine 从 PTY 输出字节缓冲中提取首条有效文本行。
//
// 处理流程：
//  1. UTF-8 解码（非法字节按原样保留为 ReplacementChar）
//  2. StripAnsi 清洗 ANSI 转义与 \r
//  3. 按行扫描，跳过：trim 后长度 < 4、纯空白、shell 提示符特征行
//  4. 命中第一条合格行 → trim + 截断到 60 rune 返回
//  5. 全无合格行 → 返回最后一段非空 trim 文本截断（兜底，避免空字段）
func ExtractFirstMeaningfulLine(buf []byte) string {
	if len(buf) == 0 {
		return ""
	}
	text := StripAnsi(string(buf))

	var fallback string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if isBlankLine(trimmed) {
			continue
		}
		// 跳过 shell 提示符行（既不算合格行，也不作为兜底候选）。
		if shellPromptRe.MatchString(trimmed) {
			continue
		}
		// 记录最新的非空、非 prompt 行作为兜底。
		fallback = trimmed
		if utf8.RuneCountInString(trimmed) < firstOutputMinLineRunes {
			continue
		}
		return truncateRunes(trimmed, firstOutputMaxRunes)
	}
	if fallback == "" {
		return ""
	}
	return truncateRunes(fallback, firstOutputMaxRunes)
}
