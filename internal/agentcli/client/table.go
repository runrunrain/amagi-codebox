package client

// table.go — 人类模式的对象数组表格渲染：CJK 宽度对齐、单元格截断、嵌套值
// 显示 {…}/[n项]、末尾提示用 --json 看全量（对齐 feishu-sync table.ts 的形态
// 识别，按 Go 数据模型重写）。

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	maxTableColumns = 8
	maxCellWidth    = 40
)

// renderTable 把对象数组渲染为对齐表格。列取标量值出现频率最高的前 8 个
// （同频按首次出现顺序），显示顺序按首次出现保持稳定。
func renderTable(w io.Writer, rows []map[string]any) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "（空列表，0 项）")
		return
	}
	// 列首现顺序：按首行键字典序 + 后续行新键字典序（map 迭代序不确定，
	// 不能直接作顺序依据）。
	var colOrder []string
	colIdx := map[string]int{}
	for _, row := range rows {
		var keys []string
		for k := range row {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, ok := colIdx[k]; !ok {
				colIdx[k] = len(colOrder)
				colOrder = append(colOrder, k)
			}
		}
	}
	scalarCount := map[string]int{}
	for _, row := range rows {
		for k, v := range row {
			if isScalar(v) {
				scalarCount[k]++
			}
		}
	}
	// 选列：标量频率降序、同频首现升序；输出按首现升序。
	sel := append([]string{}, colOrder...)
	sort.SliceStable(sel, func(i, j int) bool {
		if scalarCount[sel[i]] != scalarCount[sel[j]] {
			return scalarCount[sel[i]] > scalarCount[sel[j]]
		}
		return colIdx[sel[i]] < colIdx[sel[j]]
	})
	if len(sel) > maxTableColumns {
		sel = sel[:maxTableColumns]
	}
	sort.SliceStable(sel, func(i, j int) bool { return colIdx[sel[i]] < colIdx[sel[j]] })
	cols := sel

	cell := func(row map[string]any, key string) string {
		v, ok := row[key]
		if !ok || v == nil {
			return ""
		}
		return truncateCell(cellText(v))
	}
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = displayWidth(c)
	}
	for _, row := range rows {
		for i, c := range cols {
			if w := displayWidth(cell(row, c)); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var head strings.Builder
	for i, c := range cols {
		head.WriteString(padDisplay(c, widths[i]))
		head.WriteString("  ")
	}
	fmt.Fprintln(w, strings.TrimRight(head.String(), " "))
	for _, row := range rows {
		var b strings.Builder
		for i, c := range cols {
			b.WriteString(padDisplay(cell(row, c), widths[i]))
			b.WriteString("  ")
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	fmt.Fprintf(w, "（共 %d 项；截断/省略字段可用 --json 查看全量）\n", len(rows))
}

// cellText 单元格文本：标量直接呈现，嵌套显示 {…}/[n项]。
func cellText(v any) string {
	switch t := v.(type) {
	case map[string]any:
		return "{…}"
	case []any:
		return fmt.Sprintf("[%d项]", len(t))
	default:
		return scalarText(v)
	}
}

// truncateCell 把单元格截断到 maxCellWidth 显示列宽（含结尾 …）。
func truncateCell(s string) string {
	if displayWidth(s) <= maxCellWidth {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > maxCellWidth-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

// padDisplay 按显示宽度右侧补空格对齐。
func padDisplay(s string, width int) string {
	pad := width - displayWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// displayWidth 计算显示宽度（CJK 记 2 列）。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// runeWidth 简化的 East Asian Wide/Fullwidth 宽度表（覆盖 CJK 常用区段与 emoji）。
func runeWidth(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK 部首/符号
		r >= 0x3041 && r <= 0x33FF, // 假名/注音/CJK 扩展 A 兼容
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF, // CJK 统一表意
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3, // Hangul 音节
		r >= 0xF900 && r <= 0xFAFF, // CJK 兼容表意
		r >= 0xFE10 && r <= 0xFE19,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60, // 全角形式
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji 常用区
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD: // CJK 扩展 B 及以后
		return 2
	default:
		return 1
	}
}
