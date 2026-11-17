package platform

// console_attach.go — Windows GUI 进程附着父控制台的跨平台入口。
//
// 主程序以 `cli` 子命令运行 agent-CLI 客户端（internal/agentcli/client）时，
// 若宿主是控制台进程而主程序按 GUI 子系统链接，stdout/stderr 可能没有继承
// 到有效句柄，CLI 输出会静默丢失。AttachParentConsole 在 Windows 上调用
// AttachConsole(ATTACH_PARENT_PROCESS) 附着父进程控制台，并仅在原 std 句柄
// 无效时用 CONOUT$/CONIN$ 重建 Go 的 os.Stdout/os.Stderr/os.Stdin（附着后
// Win32 句柄可能变为有效控制台句柄，但 Go 在进程初始化时构造的 os.File
// 仍包裹着旧句柄，必须替换，评审 D-05）；非 Windows 无控制台概念，为空实现。
//
// 调用方（root main.go 的 cli 分支、cmd/codebox-agent）必须在把 os.Stdout/
// os.Stderr 交给 client.Run 之前调用本函数。平台实现在 console_attach_windows.go
// / console_attach_nonwindows.go。

// attachAction 是附着后单个标准流的动作。
type attachAction int

const (
	// attachActionKeep 保留现有 Go os.File：句柄本来就有效（重定向管道/
	// 文件或继承的控制台），或附着后也无可用控制台句柄（无从重建）。
	attachActionKeep attachAction = iota
	// attachActionRebuild 原句柄无效且附着后有效：用 CONOUT$/CONIN$ 重建
	// Go os.File，否则输出/输入静默丢失。
	attachActionRebuild
)

// decideAttachAction 是附着父控制台后单个标准流的重建决策（纯函数，跨平台
// 表驱动测试锚定，评审 D-05）：
//   - 附着前 Win32 句柄已有效（父进程显式重定向的管道/文件或继承的控制台）
//     → keep：Go 在进程初始化时构造的 os.File 已可用，覆盖会丢失重定向内容；
//   - 附着前无效（GUI 子系统典型情形：句柄为 NULL，os.File 包裹无效句柄）且
//     附着后有效 → rebuild：用 CONOUT$/CONIN$ 重建，使 CLI 输出/输入可达；
//   - 附着前后都无效 → keep：没有可绑定的控制台句柄，重建必然失败，保持原状。
func decideAttachAction(preValid, postValid bool) attachAction {
	if preValid {
		return attachActionKeep
	}
	if postValid {
		return attachActionRebuild
	}
	return attachActionKeep
}
