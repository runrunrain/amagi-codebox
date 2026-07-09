package main

import (
	"amagi-codebox/internal/platform"
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed all:mobile/dist
var mobileFS embed.FS

// 版本信息：默认 dev/unknown，由构建脚本通过 -ldflags "-X main.Version=..." 注入。
// 当未注入（go run / 无 tag 构建）时保持 dev，由 GetAppInfo 在运行时回退到 wails.json productVersion。
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
	GoVersion = "unknown"
)

func main() {
	capabilities := platform.CurrentCapabilities()
	if !platform.EnsureSingleInstance("amagi-codebox-single-instance-mutex", "Amagi CodeBox") {
		os.Exit(0)
	}

	app := NewApp(mobileFS)

	err := wails.Run(&options.App{
		Title:             "Amagi CodeBox",
		Width:             1280,
		Height:            800,
		MinWidth:          800,
		MinHeight:         600,
		HideWindowOnClose: capabilities.HideOnCloseSupported && capabilities.CloseAction == platform.CloseActionHide,
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
			app.Headroom,
			app.Paths,
			app.Log,
			app.Pty,
			app.Settings,
			app.Updater,
			app.Plugins,
			app.CodexPlugins,
			app.Workspaces,
			app.OpenCodeConfig,
			app.EnvCheck,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
