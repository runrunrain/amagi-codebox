package cmds

import "amagi-codebox/internal/agentcli/spec"

// pluginCommands 插件域命令表（纯数据）。Handler 见 root agentcli_h_plugin.go。
// 五引擎（claudecode/codex/opencode/pi/omp）统一抽象；各引擎"标识"含义不同，
// 详见各命令 Notes。
func pluginCommands() []spec.Command {
	const appsDesc = "插件引擎：claudecode|codex|opencode|pi|omp"
	target := spec.Param{Name: "target", Type: spec.TypeString, Enum: []string{"windows", "wsl", "both"}, Desc: "仅 Pi 支持；省略时沿用本机操作"}
	commands := []spec.Command{
		{
			Path:    []string{"plugin", "list"},
			Summary: "列出已安装插件（五引擎统一 DTO）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{Name: "app", Type: spec.TypeString, Enum: []string{"claudecode", "codex", "opencode", "pi", "omp"}, Desc: appsDesc + "（省略则聚合全部引擎，失败引擎记入 errors 字段）"},
			},
			Examples: []string{
				`codebox-agent plugin list --app claudecode`,
				`codebox-agent plugin list --json`,
			},
			Notes: "统一字段：app/id/name/version/enabled/description/scope/source/installPath。source 按引擎取值：claudecode/codex=市场名、opencode=模块 spec、pi=包源字符串（npm:/git:/本地路径）、omp=类别（npm/marketplace）。",
		},
		{
			Path:    []string{"plugin", "enable"},
			Summary: "启用插件",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{Name: "app", Type: spec.TypeString, Required: true, Positional: true, Enum: []string{"claudecode", "codex", "opencode", "pi", "omp"}, Desc: appsDesc},
				{Name: "id", Type: spec.TypeString, Required: true, Positional: true, Desc: "claudecode/codex 为 name@marketplace；Pi 为包源；OMP 为包名或 pluginId"},
			},
			Examples: []string{`codebox-agent plugin enable claudecode myplugin@mymarket`},
			Notes:    "OpenCode 没有包级启停入口；Pi 使用与原生配置兼容的资源过滤。",
		},
		{
			Path:    []string{"plugin", "disable"},
			Summary: "禁用插件",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{Name: "app", Type: spec.TypeString, Required: true, Positional: true, Enum: []string{"claudecode", "codex", "opencode", "pi", "omp"}, Desc: appsDesc},
				{Name: "id", Type: spec.TypeString, Required: true, Positional: true, Desc: "插件标识（同 plugin enable）"},
			},
			Examples: []string{`codebox-agent plugin disable claudecode myplugin@mymarket`},
			Notes:    "OpenCode 没有包级启停入口，Pi 按包停用资源。",
		},
		{
			Path:    []string{"plugin", "install"},
			Summary: "安装插件（网络操作，写各 CLI 插件目录）",
			Risk:    spec.RiskExec,
			Confirm: "INSTALL",
			Params: []spec.Param{
				{Name: "app", Type: spec.TypeString, Required: true, Positional: true, Enum: []string{"claudecode", "codex", "opencode", "pi", "omp"}, Desc: appsDesc},
				{Name: "name", Type: spec.TypeString, Required: true, Positional: true, Desc: "安装标识：claudecode=插件名（用户级安装）；codex=plugin@marketplace；opencode=模块地址 spec；pi=包源（npm:…/git:…/本地路径）；omp=包名或 spec"},
				target,
			},
			Examples: []string{
				`codebox-agent plugin install claudecode myplugin@mymarket --yes`,
				`codebox-agent plugin install pi npm:@foo/bar@1.0.0 --yes`,
			},
			Notes:   "安装经网络/包管理器，不可中途取消：超时不等于未安装，先用 plugin list 查询结果再决定是否重试。",
			Timeout: 600,
		},
		{
			Path:    []string{"plugin", "uninstall"},
			Summary: "卸载插件",
			Risk:    spec.RiskDestructive,
			Confirm: "UNINSTALL",
			Params: []spec.Param{
				{Name: "app", Type: spec.TypeString, Required: true, Positional: true, Enum: []string{"claudecode", "codex", "opencode", "pi", "omp"}, Desc: appsDesc},
				{Name: "id", Type: spec.TypeString, Required: true, Positional: true, Desc: "卸载标识：claudecode/codex=plugin@marketplace；opencode=模块 spec；pi=包源字符串；omp=插件名"},
				target,
			},
			Examples: []string{`codebox-agent plugin uninstall claudecode myplugin@mymarket --yes`},
			Notes:    "卸载不可中途取消：超时不等于未卸载，先用 plugin list 查询结果再决定是否重试。",
			Timeout:  600,
		},
		{
			Path: []string{"plugin", "update"}, Summary: "更新指定引擎的单个插件", Risk: spec.RiskExec, Confirm: "UPDATE", Timeout: 600,
			Params:   []spec.Param{positional("app", "支持单插件更新的引擎", "claudecode", "codex", "opencode", "pi"), positional("id", "插件标识（同 plugin uninstall）"), target},
			Examples: []string{`codebox-agent plugin update codex myplugin@mymarket --yes`, `codebox-agent plugin update pi npm:my-package --target both --yes`},
			Notes:    "Pi 的 both 请求按本机解析出的版本或 ref 重放，分别报告两侧结果。",
		},
		{
			Path: []string{"plugin", "markets"}, Summary: "查看 Claude Code 或 Codex 的插件市场", Risk: spec.RiskRead,
			Params:   []spec.Param{positional("app", "插件引擎", "claudecode", "codex")},
			Examples: []string{`codebox-agent plugin markets claudecode`},
		},
		{
			Path: []string{"plugin", "catalog"}, Summary: "查看插件市场目录，含已安装条目", Risk: spec.RiskRead,
			Params:   []spec.Param{positional("app", "插件引擎", "claudecode", "codex"), stringFlag("market", "只列出指定市场内的条目")},
			Examples: []string{`codebox-agent plugin catalog claudecode --market mymarket`},
		},
		{
			Path: []string{"plugin", "subitems"}, Summary: "查看插件子项及其启停状态", Risk: spec.RiskRead,
			Params:   []spec.Param{positional("app", "支持读取子项启停状态的引擎", "claudecode", "pi"), positional("id", "Claude 插件 ID 或 Pi 包源")},
			Examples: []string{`codebox-agent plugin subitems claudecode myplugin@mymarket`},
		},
		{
			Path: []string{"plugin", "set-subitem"}, Summary: "启用或停用一个受支持的插件子项", Risk: spec.RiskMutate,
			Params:   []spec.Param{positional("app", "插件引擎", "claudecode", "pi"), positional("id", "Claude 插件 ID 或 Pi 包源"), positional("type", "子项类型，Pi 仅支持 skill", "skill", "agent", "command", "mcp"), positional("name", "plugin subitems 返回的子项名称"), positional("state", "子项目标状态", "enabled", "disabled")},
			Examples: []string{`codebox-agent plugin set-subitem pi npm:my-package skill my-skill disabled`},
		},
	}
	return commands
}
