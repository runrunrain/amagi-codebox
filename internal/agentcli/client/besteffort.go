package client

// besteffort.go — 帮助/补全/人读输出的尽力而为写出。这些输出面向终端、
// 管道与重定向（`help | less` 等），对端提前关闭（EPIPE）时无可处理，也不应
// 中断渲染：统一经 bestEffortWriter 吞掉写错误——与 .golangci.yml 对
// (net/http.ResponseWriter).Write 的裁定同类（客户端已断开时无可处理的
// 尽力而为写）。--json 包络不走本包装（writeEnvelopeJSON 仍返回错误由调用方处置）。

import (
	"fmt"
	"io"
)

// bestEffortWriter 把 io.Writer 包成尽力而为语义。各渲染函数入口对 w 包装
// 一次（内层助手收到已包装的 w 再包一层等价幂等），调用点经 printf/println
// 输出，无需逐条检查返回错误。
type bestEffortWriter struct{ w io.Writer }

// Write 实现 io.Writer：写错误就地吞掉，返回实际写入数与 nil（理由见文件注释）。
func (b bestEffortWriter) Write(p []byte) (int, error) {
	n, _ := b.w.Write(p)
	return n, nil
}

// printf 尽力而为的格式化输出（经 Write 写出，错误已吞）。
func (b bestEffortWriter) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(b, format, args...)
}

// println 尽力而为的单行输出（经 Write 写出，错误已吞）。
func (b bestEffortWriter) println(args ...any) {
	_, _ = fmt.Fprintln(b, args...)
}
