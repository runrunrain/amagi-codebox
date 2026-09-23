//go:build ignore

// 取证 v2：宿主进程跨 DST 边界长驻时，旧会话(+02:00)与新会话(+01:00)的
// StartedAt 偏移不同——桌面 RFC3339 字符串排序 vs 远程 time.Time 排序分叉。
package main

import "fmt"

func main() {
	// 同一宿主（Europe/Berlin 语义），DST 切换 2026-10-25 03:00 CEST→02:00 CST。
	// 旧会话 A 在切换前启动：本地 02:30 CEST = 00:30Z
	// 新会话 B 在切换后启动：本地 02:15 CST = 01:15Z（墙钟回拨，B 实际更晚）
	aStr := "2026-10-25T02:30:00+02:00"
	bStr := "2026-10-25T02:15:00+01:00"
	fmt.Printf("桌面字符串比较 %s > %s : %v → 桌面序 [A, B]（A 在前）\n", aStr, bStr, aStr > bStr)
	// time.Time 比较按绝对时刻：B(01:15Z) 晚于 A(00:30Z) → 远程序 [B, A]
	fmt.Println("绝对时刻：A=00:30Z, B=01:15Z → B 更晚 → 远程序 [B, A]（B 在前）")
	fmt.Println("结论：两端首序分叉 =", aStr > bStr)
}
