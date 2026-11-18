package cmds

import (
	"strings"

	"amagi-codebox/internal/agentcli/spec"
)

func newCommand(path, summary string, risk spec.Risk, confirm string, params ...spec.Param) spec.Command {
	return spec.Command{Path: strings.Fields(path), Summary: summary, Risk: risk, Confirm: confirm, Params: params}
}

func positional(name, desc string, choices ...string) spec.Param {
	return spec.Param{Name: name, Type: spec.TypeString, Required: true, Positional: true, Desc: desc, Enum: choices}
}

func stringFlag(name, desc string) spec.Param {
	return spec.Param{Name: name, Type: spec.TypeString, Desc: desc}
}

func boolFlag(name, desc string) spec.Param {
	return spec.Param{Name: name, Type: spec.TypeBool, Desc: desc}
}

func jsonStdin(name, desc string) spec.Param {
	return spec.Param{Name: name, Type: spec.TypeString, Required: true, Secret: true, Stdin: true, Desc: desc}
}

func sharedCommands() []spec.Command {
	skill := positional("name", "共用技能目录名")
	mcp := positional("name", "共用 MCP 登记名")
	engine := positional("engine", "目标引擎", "claude", "codex", "opencode", "pi")
	force := boolFlag("force", "确认处理目标端已被外部修改的托管条目")
	market := positional("market", "共用市场名")
	plugin := positional("plugin", "市场内插件名")
	commands := []spec.Command{
		newCommand("shared skill list", "查看共用技能、启停状态与市场部署来源", spec.RiskRead, ""),
		newCommand("shared skill import", "导入 ZIP 或目录中的技能，默认停用并跳过同名技能", spec.RiskMutate, "", positional("path", "桌面应用所在主机上的 ZIP 或目录绝对路径")),
		newCommand("shared skill enable", "启用共用技能", spec.RiskMutate, "", skill),
		newCommand("shared skill disable", "停用共用技能", spec.RiskMutate, "", skill),
		newCommand("shared skill delete", "删除手动管理的共用技能", spec.RiskDestructive, "DELETE", skill),
		newCommand("shared mcp list", "查看共用 MCP 登记摘要及各引擎的分发状态", spec.RiskRead, ""),
		newCommand("shared mcp preview", "预览 MCP JSON 导入及同名冲突", spec.RiskRead, "", jsonStdin("config", "用 --config - 从 stdin 读取 MCP JSON")),
		newCommand("shared mcp import", "登记 MCP JSON，跳过同名条目，分发由用户另行选择", spec.RiskMutate, "", jsonStdin("config", "用 --config - 从 stdin 读取 MCP JSON")),
		newCommand("shared mcp edit", "替换一个 MCP 条目的已知配置字段，保留未知字段", spec.RiskMutate, "", mcp, jsonStdin("config", "用 --config - 从 stdin 读取完整条目")),
		newCommand("shared mcp distribute", "向指定引擎分发或重新同步一个 MCP 条目", spec.RiskExec, "DISTRIBUTE", mcp, engine, force),
		newCommand("shared mcp undistribute", "撤回指定引擎中的托管 MCP 条目", spec.RiskDestructive, "UNDISTRIBUTE", mcp, engine, force),
		newCommand("shared mcp delete", "删除 MCP 登记并撤回已分发的托管条目", spec.RiskDestructive, "DELETE", mcp, force),
		newCommand("shared market list", "列出共用市场、插件及已部署技能", spec.RiskRead, ""),
		newCommand("shared market claude-sources", "列出 Claude Code 中可以导入的市场名称", spec.RiskRead, ""),
		newCommand("shared market add", "从 Git 地址或本地仓库添加共用市场", spec.RiskExec, "ADD", positional("source", "Git 地址或桌面主机上的仓库路径")),
		newCommand("shared market import-claude", "按名称导入一个 Claude Code 市场", spec.RiskExec, "IMPORT", market),
		newCommand("shared market update", "刷新一个共用市场的目录", spec.RiskExec, "UPDATE", market),
		newCommand("shared market update-plugin", "更新单个市场插件及其已部署技能，保留启停状态", spec.RiskExec, "UPDATE", market, plugin),
		newCommand("shared market deploy", "部署一个市场插件的技能，跳过同名目录", spec.RiskMutate, "", market, plugin),
		newCommand("shared market uninstall", "卸载一个市场插件已部署且来源匹配的技能", spec.RiskDestructive, "UNINSTALL", market, plugin),
		newCommand("shared market remove", "删除没有已部署技能的共用市场", spec.RiskDestructive, "REMOVE", market),
	}
	for i := range commands {
		if commands[i].Risk == spec.RiskExec && strings.HasPrefix(commands[i].Name(), "shared.market.") {
			commands[i].Timeout = 900
		}
	}
	return commands
}

func wslCommands() []spec.Command {
	cli := positional("cli", "CLI 用户配置或资源的所属引擎", "claude", "codex")
	unit := positional("unit", "预览中返回的配置单元 ID")
	resource := positional("resource", "预览中的资源名称")
	hash := positional("targetHash", "同一目标最新预览返回的 targetHash")
	keys := spec.Param{Name: "key", Type: spec.TypeStrings, Desc: "所选键，可重复传入；省略表示撤回该单元的全部选择"}
	resources := spec.Param{Name: "resource", Type: spec.TypeStrings, Required: true, Desc: "资源名称，可重复传入"}
	servers := spec.Param{Name: "server", Type: spec.TypeStrings, Desc: "所选服务器名称，可重复传入；省略表示清空覆盖选择"}
	commands := []spec.Command{
		newCommand("wsl status", "查看 WSL CLI 与统一配置同步状态", spec.RiskRead, ""),
		newCommand("wsl enable", "启用当前 WSL 发行版的统一配置管理", spec.RiskExec, "ENABLE"),
		newCommand("wsl disable", "停止自动纳入新条目，保留已有授权和文件", spec.RiskMutate, ""),
		newCommand("wsl skills-link", "检查并建立当前发行版的共用技能连接", spec.RiskExec, "LINK"),
		newCommand("wsl vision-consent", "授权向当前 WSL 目标同步视觉配置", spec.RiskExec, "CONSENT"),
		newCommand("wsl config status", "查看目标引擎的 WSL 配置及资源状态", spec.RiskRead, "", cli),
		newCommand("wsl config preview", "预览每个 WSL 配置单元的同步差异", spec.RiskRead, "", cli),
		newCommand("wsl config select", "设置配置单元的同步选择并立即应用", spec.RiskExec, "SELECT", cli, unit, keys),
		newCommand("wsl config apply", "应用已选条目的安全更新", spec.RiskExec, "APPLY", cli),
		newCommand("wsl config keep-local", "停止跟随一个配置单元并保留 WSL 当前内容", spec.RiskMutate, "", cli, unit),
		newCommand("wsl config takeover", "按预览指纹接管一个 WSL 配置单元", spec.RiskDestructive, "TAKEOVER", cli, unit, hash),
		newCommand("wsl resource check", "扫描目标引擎可同步的本机资源", spec.RiskRead, "", cli),
		newCommand("wsl resource preview", "查看 WSL 资源的移植性、同步差异及目标指纹", spec.RiskRead, "", cli),
		newCommand("wsl resource apply", "导入显式选中的本机资源到 WSL", spec.RiskExec, "APPLY", cli, resources),
		newCommand("wsl resource keep-local", "停止跟随单项资源并保留 WSL 当前内容", spec.RiskMutate, "", cli, resource),
		newCommand("wsl resource takeover", "按预览指纹接管一个 WSL 资源", spec.RiskDestructive, "TAKEOVER", cli, resource, hash),
		newCommand("wsl opencode mcp-preview", "查看 OpenCode 在 WSL 的 MCP 覆盖选择", spec.RiskRead, ""),
		newCommand("wsl opencode mcp-select", "设置 OpenCode 的 WSL MCP 覆盖选择", spec.RiskExec, "SELECT", servers),
		newCommand("wsl pi status", "查看 Pi 插件的 WSL 重放状态", spec.RiskRead, ""),
		newCommand("wsl pi check", "复核 WSL 中 Pi 插件的实际状态", spec.RiskRead, ""),
	}
	for i := range commands {
		if commands[i].Name() == "wsl.pi.check" || commands[i].Risk == spec.RiskExec || commands[i].Risk == spec.RiskDestructive {
			commands[i].Timeout = 300
		}
	}
	return commands
}

func recentCommands() []spec.Command {
	id := positional("sessionId", "会话 ID")
	return []spec.Command{
		newCommand("env claude-config", "查看 Claude Code 配置检测结果及建议值", spec.RiskRead, ""),
		newCommand("env apply-claude-config", "向配置文件添加一个缺失的 Claude Code 建议值", spec.RiskExec, "APPLY", positional("key", "配置检测结果中的键名")),
		newCommand("session target", "查看会话的实际文件系统目标及工作目录", spec.RiskRead, "", id),
		newCommand("session directories", "在会话实际的文件系统中列出一级子目录", spec.RiskRead, "", id, stringFlag("root", "目录路径；省略默认从目标主目录浏览")),
		newCommand("session web-status", "查看会话 Web 视图状态，不含访问链接或令牌", spec.RiskRead, "", id, boolFlag("probe", "实时探测；省略时读取缓存")),
		newCommand("proxy status", "查看 AI 代理运行状态、分流规则及节点摘要", spec.RiskRead, ""),
		newCommand("proxy traffic", "查询 AI 代理连接日志和按域名汇总", spec.RiskRead, "",
			stringFlag("search", "匹配域名、提供商或实际出口"), stringFlag("provider", "提供商 ID"),
			stringFlag("verdict", "出口判断状态"), stringFlag("exit", "出口标识"),
			stringFlag("since", "起始时间，RFC3339"), stringFlag("until", "截止时间，RFC3339"),
			spec.Param{Name: "offset", Type: spec.TypeInt, Desc: "分页偏移，默认 0"}),
		newCommand("proxy save", "保存完整的 AI 分流配置", spec.RiskMutate, "", jsonStdin("settings", "先读取 proxy status 中的 settings，再用 --settings - 从 stdin 传入完整对象")),
		newCommand("proxy start", "启动 AI 代理或应用已保存的分流配置", spec.RiskExec, "START"),
		newCommand("proxy stop", "停止 AI 代理", spec.RiskExec, "STOP"),
	}
}
