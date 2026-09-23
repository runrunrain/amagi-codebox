//go:build ignore
package main

import (
	"fmt"
	"time"
)

func main() {
	a, _ := time.Parse(time.RFC3339, "2026-10-25T02:30:00+02:00")
	b, _ := time.Parse(time.RFC3339, "2026-10-25T02:15:00+01:00")
	fmt.Printf("After: B after A = %v（远程序 B 前）；字符串: A>B = %v（桌面序 A 前）→ 分叉=%v\n",
		b.After(a), "2026-10-25T02:30:00+02:00" > "2026-10-25T02:15:00+01:00", b.After(a))
}
