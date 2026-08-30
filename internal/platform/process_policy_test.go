package platform

import "testing"

// TestProcessPolicyCreationFlags 验证策略 → Windows 创建标志的决策内核
// （跨平台可执行；applyProcessPolicy 本体的 SysProcAttr 落位断言见
// process_policy_windows_test.go，仅能在 Windows 上运行）。
//
// 关键断言：
//   - DefaultProcessPolicy（HideConsoleWindow）必须产出 CREATE_NO_WINDOW：
//     Windows 11 默认终端为 Windows Terminal 时，仅靠 HideWindow 无法阻止
//     新终端窗口闪现（扩展管理页闪窗 Bug 的根因）；
//   - DETACHED_PROCESS 与 CREATE_NO_WINDOW 互斥，组合时 Detached 优先。
func TestProcessPolicyCreationFlags(t *testing.T) {
	cases := []struct {
		name      string
		policy    ProcessPolicy
		wantFlags uint32
		wantHide  bool
	}{
		{"默认隐藏策略产出 CREATE_NO_WINDOW", DefaultProcessPolicy(), flagCreateNoWindow, true},
		{"仅 Detached 产出 DETACHED_PROCESS", ProcessPolicy{Detached: true}, flagDetachedProcess, false},
		{"互斥组合 Detached 优先", ProcessPolicy{HideConsoleWindow: true, Detached: true}, flagDetachedProcess, true},
		{"零值策略无标志", ProcessPolicy{}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := processPolicyCreationFlags(tc.policy); got != tc.wantFlags {
				t.Errorf("processPolicyCreationFlags(%+v) = %#x, want %#x", tc.policy, got, tc.wantFlags)
			}
			if got := processPolicyHideWindow(tc.policy); got != tc.wantHide {
				t.Errorf("processPolicyHideWindow(%+v) = %v, want %v", tc.policy, got, tc.wantHide)
			}
		})
	}

	// 互斥不变量：任何策略组合都不得同时置位两个标志。
	for _, p := range []ProcessPolicy{
		{},
		{HideConsoleWindow: true},
		{Detached: true},
		{HideConsoleWindow: true, Detached: true},
	} {
		flags := processPolicyCreationFlags(p)
		if flags&flagDetachedProcess != 0 && flags&flagCreateNoWindow != 0 {
			t.Errorf("policy %+v yields mutually exclusive flags %#x", p, flags)
		}
	}
}
