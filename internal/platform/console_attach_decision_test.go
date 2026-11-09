package platform

// console_attach_decision_test.go — 附着父控制台后标准流重建决策的表驱动
// 测试（评审 D-05）。决策内核是纯函数，跨平台锚定；Windows 实机行为
//（AttachConsole/CONOUT$/CONIN$ 句柄语义）未在实机验证，见实现注释。

import "testing"

func TestDecideAttachAction(t *testing.T) {
	cases := []struct {
		name     string
		preValid bool
		postValid bool
		want     attachAction
	}{
		// 附着前已有效 = 父进程显式重定向（管道/文件）或继承控制台：
		// Go os.File 已可用，重建会覆盖有效重定向句柄 → 保留。
		{"重定向句柄：附着后仍有效", true, true, attachActionKeep},
		{"重定向句柄：附着后意外失效", true, false, attachActionKeep},
		// GUI 子系统典型情形：附着前 NULL，附着后变为有效控制台句柄，
		// 但 Go os.File 仍包裹旧无效句柄 → 必须重建。
		{"原无效且附着后有效：重建", false, true, attachActionRebuild},
		// 附着前后都无效（无控制台可绑定）→ 保持原状。
		{"原无效且附着后仍无效：保留", false, false, attachActionKeep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideAttachAction(tc.preValid, tc.postValid); got != tc.want {
				t.Fatalf("decideAttachAction(%v, %v)=%d, want %d", tc.preValid, tc.postValid, got, tc.want)
			}
		})
	}
}
