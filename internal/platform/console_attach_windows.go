//go:build windows

package platform

import (
	"os"
	"syscall"
)

// Windows 常量（值以 DWORD 传入，负值取补码的低 32 位）。
const (
	attachParentProcess = ^uintptr(0)  // ATTACH_PARENT_PROCESS == (DWORD)-1
	stdInputHandle      = ^uintptr(9)  // STD_INPUT_HANDLE  == (DWORD)-10
	stdOutputHandle     = ^uintptr(10) // STD_OUTPUT_HANDLE == (DWORD)-11
	stdErrorHandle      = ^uintptr(11) // STD_ERROR_HANDLE  == (DWORD)-12
	invalidHandleValue  = ^uintptr(0)
)

// AttachParentConsole 附着父进程控制台（仅 Windows，评审 D-05）：
//
// 附着前先记录各 Win32 标准句柄的有效性——有效句柄意味着父进程显式重定向
// 的管道/文件（或继承的控制台），Go 在进程初始化时构造的 os.Stdout/
// os.Stderr/os.Stdin 已可用，绝不能覆盖。附着成功后，对“附着前无效”的流按
// decideAttachAction 决策用 CONOUT$/CONIN$ 重建 Go os.File：AttachConsole
// 不会修改已构造的 Go File，若只看附着后句柄有效性就会跳过重建，CLI 对旧
// 无效句柄写输出，调用方拿到成功退出码却看不到任何内容（评审 D-05 根因）。
//
// 父进程没有控制台（GUI 启动等）或任一步失败都保持原状，绝不报错中断 CLI。
func AttachParentConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	attachConsole := kernel32.NewProc("AttachConsole")
	getStdHandle := kernel32.NewProc("GetStdHandle")

	handleValid := func(id uintptr) bool {
		h, _, _ := getStdHandle.Call(id)
		return h != 0 && h != invalidHandleValue
	}

	// 附着前的有效性 = 是否存在可用的重定向/继承句柄。
	preOut := handleValid(stdOutputHandle)
	preErr := handleValid(stdErrorHandle)
	preIn := handleValid(stdInputHandle)

	r1, _, _ := attachConsole.Call(attachParentProcess)
	if r1 == 0 {
		return // 父进程无控制台：保持现状
	}

	rebuild := func(preValid bool, handleID uintptr, dev string, mode int, dst **os.File) {
		if decideAttachAction(preValid, handleValid(handleID)) != attachActionRebuild {
			return
		}
		if f, err := os.OpenFile(dev, mode, 0); err == nil {
			*dst = f
		}
	}
	rebuild(preOut, stdOutputHandle, "CONOUT$", os.O_WRONLY, &os.Stdout)
	rebuild(preErr, stdErrorHandle, "CONOUT$", os.O_WRONLY, &os.Stderr)
	rebuild(preIn, stdInputHandle, "CONIN$", os.O_RDONLY, &os.Stdin)
}
