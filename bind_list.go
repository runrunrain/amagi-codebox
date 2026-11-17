package main

// bind_list.go — production Wails Bind list constructor (design §4.1 "Wails Bind
// 冻结边界", §6.3, §10.3 C-01).
//
// buildWailsBindList returns the EXACT slice passed to wails.Run Bind. It is a
// separate function so that bind_manifest_test.go can reflect over it and
// assert that the raw pty.Service objects and the exported
// App.StopAllSessions are NOT reachable (T-24).
//
// Excluded (raw, behind the gate or App facades):
//   - app.Pty            → terminal writes go through App.PtyWrite/PtyResize (gated)
//   - app.Secrets        → key checks go through App.HasProviderAPIKey/GetProviderAPIKey
//   - app.EnvCheck       → checks/fixes/cleanup go through App.* EnvCheck facades
//   - app.Log            → log queries go through App.GetLogs / App.ClearLogs
//   - app.Updater        → updates go through App.CheckForUpdate / DownloadAndApplyUpdate
//   - app.OmpConfig      → config goes through App.*Omp* facades
//   - app.OpenCodeConfig → config goes through App.*OpenCodeConfig* facades
//
// Remaining bound objects expose only read/config/gated-facade methods.

func buildWailsBindList(app *App) []any {
	return []any{
		app,
		app.Config,
		app.Paths,
		app.Settings,
		app.Plugins,
		app.CodexPlugins,
		app.OpenCodePlugins,
		app.PiPlugins,
		app.OmpPlugins,
		app.PiConfig,
		app.AgentProfiles,
		app.Usage,
		app.WebUI,
		app.Skins,
		app.GitAssist,
		// 公共扩展中心（sharedext）：公共 skill / 公共 MCP 简单 CRUD 直接走服务
		// 绑定；对话框 / 聚合 / MCP 导入信封等需要 App 上下文的门面在 App 上
		//（app.go 尾部 Shared Extensions 段）。注意服务层 ImportMCPServers 为
		// 三返回值，Wails v2 不支持绑定，导入统一走 App.ImportSharedMCPServers。
		app.sharedExt,
	}
}
