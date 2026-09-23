//go:build ignore

// 取证脚本：验证「桌面 List() RFC3339 字符串排序」与「远程 time.Time 排序」
// 在 (a) 同宿主跨 DST 边界 (b) 同秒并列 两个场景下是否分叉。
package main

import (
	"fmt"
	"sort"
	"time"
)

func main() {
	// 场景 a：宿主时区 Europe/Berlin，2026-10-25 03:00 CEST→CST 切换。
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		fmt.Println("load berlin:", err)
		return
	}
	// A：DST 内启动（+02:00），本地 02:30（=00:30 UTC）
	a := time.Date(2026, 10, 25, 2, 30, 0, 0, berlin)
	// B：DST 切换后启动（+01:00），本地 02:15（=01:15 UTC，墙钟回拨后）
	b := time.Date(2026, 10, 25, 2, 15, 0, 500000000, berlin)
	fmt.Printf("A=%s (%s) B=%s (%s)\n", a.Format(time.RFC3339), a.UTC(), b.Format(time.RFC3339), b.UTC())
	fmt.Printf("时间序 B 晚于 A: %v；字符串比较 A>B: %v → 分叉: %v\n",
		b.After(a), a.Format(time.RFC3339) > b.Format(time.RFC3339),
		b.After(a) && a.Format(time.RFC3339) > b.Format(time.RFC3339))

	// 场景 b：同秒并列（远程纳秒精度 vs 桌面秒级字符串）。
	c := time.Date(2026, 9, 1, 10, 0, 5, 900000000, time.UTC) // 秒内晚
	d := time.Date(2026, 9, 1, 10, 0, 5, 100000000, time.UTC) // 秒内早
	sC, sD := c.Format(time.RFC3339), d.Format(time.RFC3339)
	fmt.Printf("C=%s D=%s 字符串相等(桌面并列/顺序任意): %v；远程按纳秒序 C 在前: %v\n",
		sC, sD, sC == sD, c.After(d))

	// 桌面最终排序就是字符串比较（manager.go:325 语义复刻）。
	list := []string{sC, sD}
	sort.Slice(list, func(i, j int) bool { return list[i] > list[j] })
	fmt.Printf("桌面同秒排序结果（sort.Slice 不稳定，输入序 [C,D]）: %v\n", list)
}
