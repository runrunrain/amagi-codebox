package platform

// Windows 进程创建标志（winbase.h 的 DETACHED_PROCESS / CREATE_NO_WINDOW），
// 以纯 uint32 常量放在跨平台内核中：策略 → 标志的决策逻辑由此可在任意
// 平台单测（applyProcessPolicy 本体是 windows-only，其他平台无法执行，
// 见 process_policy_test.go / process_policy_windows_test.go）。
const (
	flagDetachedProcess uint32 = 0x00000010 // DETACHED_PROCESS
	flagCreateNoWindow  uint32 = 0x08000000 // CREATE_NO_WINDOW
)

// processPolicyCreationFlags 返回策略对应的 Windows 进程创建标志。
//
// DETACHED_PROCESS 与 CREATE_NO_WINDOW 互斥：两者都指定子进程控制台的创建
// 方式，同时置位是非法组合，必须按优先级二选一：
//   - Detached 优先：完全不创建控制台（最彻底，供需脱离父进程存活的子进程，
//     当前无调用方构造该策略，保留语义兼容）；
//   - HideConsoleWindow：CREATE_NO_WINDOW——创建控制台但不可见。GUI 父进程
//     （-H windowsgui）下 console 子系统 exe 会分配新控制台；Windows 11 默认
//     终端为 Windows Terminal 时，仅靠 STARTUPINFO 的 HideWindow 无法阻止
//     新终端窗口闪现，必须叠加 CREATE_NO_WINDOW。
func processPolicyCreationFlags(policy ProcessPolicy) uint32 {
	switch {
	case policy.Detached:
		return flagDetachedProcess
	case policy.HideConsoleWindow:
		return flagCreateNoWindow
	default:
		return 0
	}
}

// processPolicyHideWindow 返回是否置位 STARTUPINFO 的 HideWindow
// （SW_HIDE，隐藏首个显示的窗口；对无 GUI 窗口的进程无副作用）。
func processPolicyHideWindow(policy ProcessPolicy) bool {
	return policy.HideConsoleWindow
}
