package cmds

import "amagi-codebox/internal/agentcli/spec"

// agentCLIToolEnum 是 env 域工具名的合法拼写（与服务端 parseCLITool 接受集一致）。
var agentCLIToolEnum = []string{"claude-code", "opencode", "codex", "pi", "omp", "headroom"}

// systemCommands 覆盖 status / cli info / env / remote status（设计 §4 system 域）。
func systemCommands() []spec.Command {
	return []spec.Command{
		{
			Path:    []string{"status"},
			Summary: "应用总览：版本/平台/环境检测摘要/会话计数/远程开关/agent-cli 状态",
			Risk:    spec.RiskRead,
			Examples: []string{
				`codebox-agent status`,
				`codebox-agent status --json`,
			},
			Notes: "只读聚合视图，不含任何令牌或密钥；会话计数按状态分组。",
		},
		{
			Path:    []string{"cli", "info"},
			Summary: "agent-CLI 自身信息：shim 路径、endpoint 文件路径、skill 安装状态",
			Risk:    spec.RiskRead,
			Examples: []string{
				`codebox-agent cli info`,
			},
			Notes: "只报告文件存在性，绝不读取/回显 endpoint.json 内容（其中含令牌）。",
		},
		{
			Path:    []string{"env", "status"},
			Summary: "最近一次环境检测结果（缓存快照，不实时探测）",
			Risk:    spec.RiskRead,
			Examples: []string{
				`codebox-agent env status`,
			},
			Notes: "尚未检测过时 checked=false；需要实时结论请用 env check。",
		},
		{
			Path:    []string{"env", "check"},
			Summary: "实时环境检测：全部工具或指定工具",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{
					Name: "tool", Type: spec.TypeString,
					Enum: agentCLIToolEnum,
					Desc: "可选；只检测该工具（claude-code/opencode/codex/pi/omp/headroom），缺省检测全部",
				},
			},
			Examples: []string{
				`codebox-agent env check`,
				`codebox-agent env check --tool codex`,
			},
			Timeout: 300,
		},
		{
			Path:    []string{"env", "install"},
			Summary: "安装指定 CLI 工具（npm 等外部安装器，可能改动 PATH）",
			Risk:    spec.RiskExec, Confirm: "INSTALL",
			Params: []spec.Param{
				{
					Name: "tool", Type: spec.TypeString, Required: true,
					Enum: agentCLIToolEnum,
					Desc: "要安装的工具名（claude-code/opencode/codex/pi/omp/headroom）",
				},
			},
			Examples: []string{
				`codebox-agent env install --tool opencode --yes`,
			},
			Notes:   "exec 级操作，需 --yes 确认；安装耗时可达数分钟。外部安装器不可中途取消：超时不等于未安装，先用 env status / env check 查询结果再决定是否重试。",
			Timeout: 900,
		},
		{
			Path:    []string{"remote", "status"},
			Summary: "远程控制服务状态（开关/端口/运行态，不含令牌）",
			Risk:    spec.RiskRead,
			Examples: []string{
				`codebox-agent remote status`,
			},
			Notes: "脱敏视图：永不返回 token 字段，仅返回 tokenConfigured 布尔值。",
		},
	}
}
