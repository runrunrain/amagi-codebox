//go:build windows

package platform

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	user32                  = syscall.NewLazyDLL("user32.dll")
	procCreateMutex         = kernel32.NewProc("CreateMutexW")
	procFindWindow          = user32.NewProc("FindWindowW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procIsIconic            = user32.NewProc("IsIconic")
)

const (
	errorAlreadyExists = 183
	swRestore          = 9
	swShow             = 5
)

func EnsureSingleInstance(mutexName, windowTitle string) bool {
	name, _ := syscall.UTF16PtrFromString(mutexName)
	handle, _, err := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return true
	}
	if err.(syscall.Errno) == errorAlreadyExists {
		activateExistingWindow(windowTitle)
		return false
	}
	return true
}

func activateExistingWindow(windowTitle string) {
	title, _ := syscall.UTF16PtrFromString(windowTitle)
	hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd == 0 {
		return
	}
	minimized, _, _ := procIsIconic.Call(hwnd)
	if minimized != 0 {
		procShowWindow.Call(hwnd, swRestore)
	} else {
		procShowWindow.Call(hwnd, swShow)
	}
	procSetForegroundWindow.Call(hwnd)
}
