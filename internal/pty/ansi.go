package pty

import (
	"regexp"
	"strings"
)

var (
	// csiRe 清洗 CSI 序列（如颜色、光标控制）：ESC [ 参数 中间字节 终止字节。
	csiRe = regexp.MustCompile(`\x1B\[[0-?]*[ -/]*[@-~]`)
	// oscRe 清洗 OSC 序列（如窗口标题、超链接）：ESC ] ... BEL 或 ESC ] ... ESC \。
	oscRe = regexp.MustCompile(`\x1B\][^\x07\x1B]*(?:\x07|\x1B\\)`)
	// 其他单字符 ESC 转义（如 ESC =、ESC >、ESC ? 等私有 Fe/Fp 序列，及 ESC @ ... ESC _ 标准 Fe 序列）。
	// 覆盖 ESC 后跟任意 ASCII 可打印字节（0x20~0x7E）。
	escSingleRe = regexp.MustCompile(`\x1B[\x20-\x7E]`)
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
