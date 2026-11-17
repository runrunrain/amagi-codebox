package cmds

import "amagi-codebox/internal/agentcli/spec"

// agentCLIProviderTypeEnum：provider 域的 type 过滤值与 App.GetProvidersByType 的
// 真实语义对齐（anthropic/openai 兼容格式），不是 CLI 应用名。
var agentCLIProviderTypeEnum = []string{"anthropic", "openai"}

// agentCLIPresetTypeEnum：终端预设桶的合法拼写（含历史 CLI 名称，服务端会归一化：
// claude_code→anthropic，codex/pi/omp→openai，opencode 保持自身）。
var agentCLIPresetTypeEnum = []string{"claude_code", "opencode", "codex", "pi", "omp", "anthropic", "openai"}

// agentCLIAppTypeEnum：用量统计按 CLI 应用过滤的取值（session.AppType）。
var agentCLIAppTypeEnum = []string{"claudecode", "opencode", "codex", "pi", "omp"}

// configCommands 覆盖 provider / preset / key / settings / envvars / workdir（设计 §4 config 域）。
func configCommands() []spec.Command {
	return []spec.Command{
		{
			Path:    []string{"provider", "list"},
			Summary: "列出提供商配置（脱敏视图，含密钥状态摘要）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{
					Name: "type", Type: spec.TypeString,
					Enum: agentCLIProviderTypeEnum,
					Desc: "可选；按兼容格式过滤：anthropic 或 openai，缺省返回全部",
				},
			},
			Examples: []string{
				`codebox-agent provider list`,
				`codebox-agent provider list --type openai`,
			},
			Notes: "输出不含任何明文/密钥字段；key.maskedKey 为掩码摘要；认证类 header（authorization/x-api-key/token/secret/cookie 等）的值一律返回掩码并附 headersSet 布尔。",
		},
		{
			Path:    []string{"provider", "get"},
			Summary: "查看单个提供商配置（脱敏视图）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{
					Name: "name", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "提供商名称",
				},
			},
			Examples: []string{
				`codebox-agent provider get acme`,
			},
			Notes: "输出结构可直接修改后用于 provider save --json（在现有配置上合并，未提及字段保留）；认证类 header 值已掩码，传回掩码占位即保留现有值；密钥请用 provider key-set 单独写入。",
		},
		{
			Path:    []string{"provider", "save"},
			Summary: "新建或覆盖保存提供商配置（JSON 与 provider get 输出同构）",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{
					Name: "name", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "提供商名称（不可含 /，首尾不可有空白）",
				},
				{
					Name: "json", Type: spec.TypeJSON, Required: true,
					Desc: "提供商配置 JSON（结构与 provider get 输出一致）；任何位置出现 apiKey/authKey 等密钥字段会被拒绝",
				},
			},
			Examples: []string{
				`codebox-agent provider save acme --json '{"defaultModel":"m-1","openai":{"enabled":true,"baseUrl":"https://api.test/v1"}}'`,
			},
			Notes: "在现有配置上合并：DTO 出现的字段覆盖，未提及的字段（含 url_history、preset 的 opencodeConfig、未列出的 header/preset）保留。认证类 header（authorization/x-api-key/token/secret/cookie 等）输出已掩码，save 传回掩码占位即保留现有值；明文认证 header 会被拒绝（改用桌面应用或 $ENV: 引用）；密钥只能经 provider key-set（stdin）写入。",
		},
		{
			Path:    []string{"provider", "key-set"},
			Summary: "写入提供商 API 密钥（key 建议经 stdin 传入）",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{
					Name: "name", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "提供商名称",
				},
				{
					Name: "key", Type: spec.TypeString, Required: true,
					Stdin: true, Secret: true,
					Desc: "API 密钥；传 - 表示从 stdin 读取，避免出现在 argv/审计日志中",
				},
			},
			Examples: []string{
				`codebox-agent provider key-set acme --key - <<< "$ACME_KEY"`,
				`echo "$ACME_KEY" | codebox-agent provider key-set acme --key -`,
			},
			Notes: "Secret 参数：审计日志与错误回显均脱敏；成功后返回掩码确认，绝不回显明文。",
		},
		{
			Path:    []string{"provider", "delete"},
			Summary: "删除提供商配置及其密钥（不可逆）",
			Risk:    spec.RiskDestructive, Confirm: "DELETE",
			Params: []spec.Param{
				{
					Name: "name", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "提供商名称",
				},
			},
			Examples: []string{
				`codebox-agent provider delete acme --yes`,
			},
			Notes: "连带删除密钥存储条目并同步各 harness 配置。",
		},
		{
			Path:    []string{"key", "status"},
			Summary: "密钥状态诊断（掩码输出，支持按提供商过滤）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{
					Name: "provider", Type: spec.TypeString,
					Desc: "可选；只看该提供商，缺省返回全部",
				},
			},
			Examples: []string{
				`codebox-agent key status`,
				`codebox-agent key status --provider acme`,
			},
			Notes: "输出为 GetKeyDiagnostics 的脱敏投影：maskedKey/stored_masked 均 已掩码。",
		},
		{
			Path:    []string{"preset", "list"},
			Summary: "列出终端预设（合并视图，按类型桶）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{
					Name: "type", Type: spec.TypeString, Required: true,
					Enum: agentCLIPresetTypeEnum,
					Desc: "预设类型桶；claude_code 归入 anthropic 桶，codex/pi/omp 归入 openai 桶，opencode 独立",
				},
			},
			Examples: []string{
				`codebox-agent preset list --type claude_code`,
				`codebox-agent preset list --type opencode`,
			},
		},
		{
			Path:    []string{"preset", "save"},
			Summary: "新建或更新终端预设",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{
					Name: "type", Type: spec.TypeString, Required: true, Positional: true,
					Enum: agentCLIPresetTypeEnum,
					Desc: "预设类型桶（同 preset list --type）",
				},
				{
					Name: "name", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "预设名（桶内唯一键）",
				},
				{
					Name: "json", Type: spec.TypeJSON,
					Desc: "可选；完整预设 JSON（结构与 preset list 条目一致），未给字段保留原值",
				},
				{
					Name: "provider", Type: spec.TypeString,
					Desc: "可选；关联提供商名，覆盖 json 中的值",
				},
				{
					Name: "model", Type: spec.TypeString,
					Desc: "可选；模型名，覆盖 json 中的值",
				},
				{
					Name: "vision", Type: spec.TypeBool,
					Desc: "可选；标记该模型可识图",
				},
				{
					Name: "video", Type: spec.TypeBool,
					Desc: "可选；标记该模型可识视频",
				},
			},
			Examples: []string{
				`codebox-agent preset save claude_code daily --provider acme --model m-daily`,
				`codebox-agent preset save opencode oc1 --json '{"provider":"acme","model":"m-1"}'`,
			},
			Notes: "json 与旗标可组合（旗标优先）；两者都未提供 provider 时报参数错误。",
		},
		{
			Path:    []string{"preset", "delete"},
			Summary: "删除终端预设（不可逆）",
			Risk:    spec.RiskDestructive, Confirm: "DELETE",
			Params: []spec.Param{
				{
					Name: "type", Type: spec.TypeString, Required: true, Positional: true,
					Enum: agentCLIPresetTypeEnum,
					Desc: "预设类型桶",
				},
				{
					Name: "name", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "预设名",
				},
			},
			Examples: []string{
				`codebox-agent preset delete claude_code daily --yes`,
			},
		},
		{
			Path:    []string{"settings", "get"},
			Summary: "读取应用设置（脱敏：凭据字段只返回是否已设置）",
			Risk:    spec.RiskRead,
			Examples: []string{
				`codebox-agent settings get`,
			},
			Notes: "githubToken 等凭据不回显明文，仅返回 githubTokenSet 布尔值。",
		},
		{
			Path:    []string{"settings", "set"},
			Summary: "修改白名单内的应用设置项",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{
					Name: "key", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "设置键（白名单见 Notes；remote*/cli*/含 token 的键一律拒绝）",
				},
				{
					Name: "value", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "设置值（terminal.scrollback 为整数）",
				},
			},
			Examples: []string{
				`codebox-agent settings set dashboard.provider acme`,
				`codebox-agent settings set terminal.scrollback 50000`,
			},
			Notes: "白名单：dashboard.provider/preset/opencodeprovider/mode/shell/claudemode/claudeshell/" +
				"opencodemode/opencodeshell/codexmode/codexshell/pimode/pishell/ompmode/ompshell、" +
				"terminal.scrollback、mobilewebroot。命中禁改面（remote*/cli*/token）返回 forbidden_command。",
		},
		{
			Path:    []string{"envvars", "list"},
			Summary: "列出应用注入各 CLI 的环境变量（敏感名脱敏）",
			Risk:    spec.RiskRead,
			Examples: []string{
				`codebox-agent envvars list`,
			},
			Notes: "名称含 KEY/TOKEN/SECRET/PASSWORD/PASSWD/CREDENTIAL/AUTH 的变量值一律返回掩码并附 valueSet 布尔；其余原样回显。",
		},
		{
			Path:    []string{"envvars", "set"},
			Summary: "设置一个环境变量（key=value）",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{
					Name: "key", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "环境变量名（保留键 WSLENV/PATH/PATHEXT 等会被服务端拒绝）",
				},
				{
					Name: "value", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "环境变量值",
				},
			},
			Examples: []string{
				`codebox-agent envvars set ANTHROPIC_LOG debug`,
			},
			Notes: "敏感名（含 KEY/TOKEN/SECRET/PASSWORD/PASSWD/CREDENTIAL/AUTH）的回显与后续 list 输出均为掩码，底层存储保留真实值。",
		},
		{
			Path:    []string{"envvars", "delete"},
			Summary: "删除一个环境变量（不可逆）",
			Risk:    spec.RiskDestructive, Confirm: "DELETE",
			Params: []spec.Param{
				{
					Name: "key", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "环境变量名",
				},
			},
			Examples: []string{
				`codebox-agent envvars delete ANTHROPIC_LOG --yes`,
			},
		},
		{
			Path:    []string{"workdir", "list"},
			Summary: "列出已保存的工作目录",
			Risk:    spec.RiskRead,
			Examples: []string{
				`codebox-agent workdir list`,
			},
		},
		{
			Path:    []string{"workdir", "add"},
			Summary: "保存一个工作目录（按路径去重）",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{
					Name: "path", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "目录绝对路径",
				},
				{
					Name: "label", Type: spec.TypeString,
					Desc: "可选；显示标签，缺省用路径末段",
				},
			},
			Examples: []string{
				`codebox-agent workdir add /Users/me/work/proj`,
				`codebox-agent workdir add /Users/me/work/proj --label 项目A`,
			},
		},
		{
			Path:    []string{"workdir", "remove"},
			Summary: "移除一个已保存的工作目录",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{
					Name: "path", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "目录路径（须与保存时一致）",
				},
			},
			Examples: []string{
				`codebox-agent workdir remove /Users/me/work/proj`,
			},
		},
	}
}
