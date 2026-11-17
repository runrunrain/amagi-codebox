package cmds

import "amagi-codebox/internal/agentcli/spec"

// pluginCommands 插件域命令表（纯数据）。Handler 见 root agentcli_h_plugin.go。
// 五引擎（claudecode/codex/opencode/pi/omp）统一抽象；各引擎"标识"含义不同，
// 详见各命令 Notes。
func pluginCommands() []spec.Command {
	const appsDesc = "插件引擎：claudecode|codex|opencode|pi|omp"
	return []spec.Command{
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
				{Name: "id", Type: spec.TypeString, Required: true, Positional: true, Desc: "插件标识（claudecode/codex 为 name@marketplace；omp 为包名或 pluginId）"},
			},
			Examples: []string{`codebox-agent plugin enable claudecode myplugin@mymarket`},
			Notes:    "opencode 与 pi 引擎不支持启停：opencode 无启停面（安装/卸载即开关），pi 的 packages[] 存在即启用——对这两者调用返回 bad_args。",
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
			Notes:    "opencode 与 pi 引擎不支持启停（见 plugin enable Notes）。",
		},
		{
			Path:    []string{"plugin", "install"},
			Summary: "安装插件（网络操作，写各 CLI 插件目录）",
			Risk:    spec.RiskExec,
			Confirm: "INSTALL",
			Params: []spec.Param{
				{Name: "app", Type: spec.TypeString, Required: true, Positional: true, Enum: []string{"claudecode", "codex", "opencode", "pi", "omp"}, Desc: appsDesc},
				{Name: "name", Type: spec.TypeString, Required: true, Positional: true, Desc: "安装标识：claudecode=插件名（用户级安装）；codex=plugin@marketplace；opencode=模块地址 spec；pi=包源（npm:…/git:…/本地路径）；omp=包名或 spec"},
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
			},
			Examples: []string{`codebox-agent plugin uninstall claudecode myplugin@mymarket --yes`},
			Notes:    "卸载不可中途取消：超时不等于未卸载，先用 plugin list 查询结果再决定是否重试。",
			Timeout:  600,
		},
	}
}
