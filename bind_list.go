package main

// bind_list.go — production Wails Bind list constructor (design §4.1 "Wails Bind
// 冻结边界", §6.3, §10.3 C-01).
//
// buildWailsBindList returns the EXACT slice passed to wails.Run Bind. It is a
// separate function so that bind_manifest_test.go can reflect over it and
// assert that the raw pty.Service / headroom.HeadroomService
// objects and the exported App.StopAllSessions are NOT reachable (T-24).
//
// Excluded (raw, behind the gate):
//   - app.Pty       → terminal writes go through App.PtyWrite/PtyResize (gated)
//   - app.Headroom  → mutations go through App.Headroom* facade (lease-guarded)
//
// Remaining bound objects expose only read/config/gated-facade methods.

func buildWailsBindList(app *App) []any {
	return []any{
		app,
		app.Config,
		app.Secrets,
		app.Paths,
		app.Log,
		app.Settings,
		app.Updater,
		app.Plugins,
		app.CodexPlugins,
		app.OpenCodePlugins,
		app.PiPlugins,
		app.OmpPlugins,
		app.OpenCodeConfig,
		app.PiConfig,
		app.OmpConfig,
		app.EnvCheck,
		app.Usage,
		app.WebUI,
	}
}
