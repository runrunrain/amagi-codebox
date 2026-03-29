package main

import (
	"embed"
	"os"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
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

// ensureSingleInstance 创建命名互斥量确保单实例运行。
// 返回 true 表示当前是第一个实例（可继续启动），false 表示已有实例在运行。
func ensureSingleInstance() bool {
	name, _ := syscall.UTF16PtrFromString("amagi-codebox-single-instance-mutex")
	handle, _, err := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return true // 无法创建互斥量，继续启动
	}
	if err.(syscall.Errno) == errorAlreadyExists {
		// 已有实例在运行，尝试激活现有窗口
		activateExistingWindow()
		return false
	}
	return true
}

// activateExistingWindow 查找并激活已有的 Amagi CodeBox 窗口。
func activateExistingWindow() {
	title, _ := syscall.UTF16PtrFromString("Amagi CodeBox")
	hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd == 0 {
		return
	}
	// 如果窗口是最小化状态，先恢复
	minimized, _, _ := procIsIconic.Call(hwnd)
	if minimized != 0 {
		procShowWindow.Call(hwnd, swRestore)
	} else {
		procShowWindow.Call(hwnd, swShow)
	}
	procSetForegroundWindow.Call(hwnd)
}

//go:embed all:frontend/dist
var assets embed.FS

var Version = "dev"

func main() {
	if !ensureSingleInstance() {
		os.Exit(0)
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:             "Amagi CodeBox",
		Width:             1280,
		Height:            800,
		MinWidth:          800,
		MinHeight:         600,
		HideWindowOnClose: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.Startup,
		OnShutdown:       app.Shutdown,
		Bind: []any{
			app,
			app.Config,
			app.Secrets,
			app.Proxy,
			app.Paths,
			app.Log,
			app.Pty,
			app.Settings,
			app.Updater,
			app.Plugins,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
