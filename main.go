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

var Version = "dev"

func main() {
	capabilities := platform.CurrentCapabilities()
	if !platform.EnsureSingleInstance("amagi-codebox-single-instance-mutex", "Amagi CodeBox") {
		os.Exit(0)
	}

	app := NewApp()

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
			app.Paths,
			app.Log,
			app.Pty,
			app.Settings,
			app.Updater,
			app.Plugins,
			app.Workspaces,
			app.Amagi,
			app.OpenCodeConfig,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
