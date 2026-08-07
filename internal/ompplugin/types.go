package ompplugin

// 本包管理 Oh My Pi (omp) 的插件（npm 包 + marketplace 插件）。
//
// omp 与 Claude Code 同源：npm 插件经包管理器安装到 ~/.omp/plugins（npm 源），
// marketplace 插件经 MarketplaceManager 安装到 ~/.omp/plugins 并由
// installed_plugins.json 登记（marketplace 源）。CLI 入口：
//
//	omp plugin list --json         # {npm:[...], marketplace:[...]}
//	omp install <spec>             # npm/github/git/local/marketplace ref，别名天然支持
//	omp plugin uninstall <name>    # npm 与 marketplace 自动路由
//	omp plugin enable|disable <name>
//	omp plugin upgrade <id>        # marketplace 专用
//
// 读写策略（对标 internal/piplugin 的 CLI 驱动模式）：
//   - 读（list）fork omp CLI 取 --json 输出，字段级容错解析 + 多段降级；
//   - 写（install/uninstall/enable/disable/upgrade）走 omp CLI。
//   - 环境差异：omp 插件 CLI 天然操作 ~/.omp/plugins，不注入
//     PI_CODING_AGENT_DIR（omp 不消费它，且旧值会把 omp 会话导向独立副本，
//     LaunchOmpSession 启动时亦显式删除该变量；实测 plugin list 不受其影响）。

// Plugin represents one installed omp plugin.
//
// Plugin 表示一个已安装的 omp 插件（npm 或 marketplace 源）。
type Plugin struct {
	// ID 是去重/前端 key 用的稳定标识：npm 取包名；marketplace 取 pluginId（name@marketplace）。
	ID string `json:"id"`
	// Name 是显示名：npm 为包名；marketplace 与 ID 相同（omp 无独立短名）。
	Name string `json:"name"`
	// Version 取自插件清单版本；marketplace 取 entries[0].version。
	Version string `json:"version,omitempty"`
	// Kind 归类：npm / marketplace。
	Kind string `json:"kind"`
	// Enabled：npm 取自 enabled（缺省 true）；marketplace 取自 entries[0].enabled（缺省 true）。
	Enabled bool `json:"enabled"`
	// EnabledFeatures 是 npm 插件启用的可选特性（omp 的 pkg[feat1,feat2] 语法）。
	EnabledFeatures []string `json:"enabledFeatures,omitempty"`
	// Description 取自 npm 插件清单的 manifest.description。
	Description string `json:"description,omitempty"`
	// Scope 仅 marketplace 有意义：user / project。
	Scope string `json:"scope,omitempty"`
	// InstallPath 是实体目录（npm 插件清单的 path 字段）。
	InstallPath string `json:"installPath,omitempty"`
}

// PluginsData is the aggregate refresh response used by the frontend.
//
// PluginsData 是前端刷新动作的聚合返回。
type PluginsData struct {
	Installed []Plugin `json:"installed"`
	// Warnings 列出解析降级等提示（如旧版 omp 已按空列表处理）。
	Warnings []string `json:"warnings,omitempty"`
}

// CommandResult reports one omp CLI invocation result.
//
// CommandResult 报告一次 omp CLI 调用的结果。
type CommandResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}
