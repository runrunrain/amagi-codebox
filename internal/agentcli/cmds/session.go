package cmds

import "amagi-codebox/internal/agentcli/spec"

// sessionCommands 会话域命令表（纯数据）。Handler 见 root agentcli_h_session.go。
//
// 参数映射依据 app.go 真实签名：
//   - LaunchSession(provider, preset, mode, workDir, useHeadroom, shell)（claudecode）
//   - LaunchOpenCode(provider, preset, mode, workDir, shell)
//   - LaunchCodexSession / LaunchPiSession / LaunchOmpSession(model, provider, mode, workDir, shell)
//
// 注意：LaunchSession 自身把空 mode 视为 terminal，本命令表把默认值定为 embedded
// 并由 Handler 显式传 "embedded"，避免默认语义在两层之间漂移。
func sessionCommands() []spec.Command {
	return []spec.Command{
		{
			Path:    []string{"session", "list"},
			Summary: "列出全部会话（可按状态过滤）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{
					Name: "status",
					Type: spec.TypeString,
					Enum: []string{"running", "stopping", "stopped", "exited", "failed"},
					Desc: "按状态过滤（省略则返回全部）",
				},
			},
			Examples: []string{
				`codebox-agent session list`,
				`codebox-agent session list --status running --json`,
			},
		},
		{
			Path:    []string{"session", "get"},
			Summary: "查看单个会话摘要",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{Name: "sessionId", Type: spec.TypeString, Required: true, Positional: true, Desc: "会话 ID"},
			},
			Examples: []string{`codebox-agent session get abc123`},
		},
		{
			Path:    []string{"session", "output"},
			Summary: "读取内嵌会话的输出历史（默认剥离 ANSI/控制序列）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{Name: "sessionId", Type: spec.TypeString, Required: true, Positional: true, Desc: "会话 ID"},
				{Name: "tailBytes", Type: spec.TypeInt, Desc: "仅返回最后 N 字节（按 UTF-8 字符边界安全截断；作用于处理后的文本；--raw 模式作用于原始字节）"},
				{Name: "sinceSeq", Type: spec.TypeInt, Desc: "上一轮返回的 seq：seq 未前进时返回空文本，前进时返回完整快照供整体重算（seq 是输出分块计数，非字节偏移，不支持精确增量）"},
				{Name: "raw", Type: spec.TypeBool, Desc: "返回未剥离控制序列的原始文本"},
			},
			Examples: []string{
				`codebox-agent session output abc123`,
				`codebox-agent session output abc123 --tail-bytes 4096 --since-seq 42 --json`,
			},
			Notes:   "默认剥离 ANSI 与控制序列：CSI/OSC 转义序列整段丢弃，回车(\\r)按行首覆盖、退格(\\b)按光标左移+覆盖写处理；CSI 光标移动/清屏等序列按丢弃处理（不模拟光标移动）。terminal 模式会话的输出进独立终端窗口，本命令对其无内容（仅 embedded 会话有内嵌输出历史）。输出可能含终端回显的敏感信息（如粘贴过的密钥），注意不要写入日志。",
			Timeout: 30,
		},
		{
			Path:    []string{"session", "launch"},
			Summary: "启动一个 CLI 会话（claudecode/opencode/codex/pi/omp）",
			Risk:    spec.RiskExec,
			Confirm: "LAUNCH",
			Params: []spec.Param{
				{Name: "appType", Type: spec.TypeString, Required: true, Positional: true, Enum: []string{"claudecode", "opencode", "codex", "pi", "omp"}, Desc: "CLI 类型"},
				{Name: "provider", Type: spec.TypeString, Desc: "提供商名（claudecode 必填；codex/pi/omp 可选，与 preset 二选一时优先显式 model）"},
				{Name: "model", Type: spec.TypeString, Desc: "模型名（codex/pi/omp 的第一参数；也可传 terminal preset 键）"},
				{Name: "preset", Type: spec.TypeString, Desc: "预设名（claudecode/opencode 用 preset 名；codex/pi/omp 未给 model 时作为 preset 键传入）"},
				{Name: "workDir", Type: spec.TypeString, Desc: "工作目录：必须是已存在的目录（校验时归一化为绝对路径）；省略则走应用默认目录"},
				{Name: "mode", Type: spec.TypeString, Enum: []string{"embedded", "terminal"}, Default: "embedded", Desc: "启动模式，默认 embedded"},
				{Name: "headroom", Type: spec.TypeBool, Default: false, Desc: "启用上下文压缩代理（仅 claudecode 支持）"},
				{Name: "shell", Type: spec.TypeString, Desc: "覆盖启动 shell 路径（可选）"},
			},
			Examples: []string{
				`codebox-agent session launch claudecode --provider myprov --preset default --work-dir ~/proj --yes`,
				`codebox-agent session launch codex --provider myprov --model gpt-5 --work-dir ~/proj --yes --json`,
			},
			Notes: "默认 embedded（内嵌终端，输出可用 session output 读取）。terminal 模式也允许，但输出进独立终端窗口，agent 无法读取其输出。headroom 仅 claudecode 支持（其他类型传 true 返回 bad_args）。启动入口不可中途取消：超时不等于未生效，先用 session list/get 查询是否已启动，再决定是否重试。启动前应向用户确认工作目录与提供商（传 --shell 或 --work-dir 时必须一并确认）。返回 sessionId 与会话摘要。",
		},
		{
			Path:    []string{"session", "send"},
			Summary: "向运行中的内嵌会话发送输入",
			Risk:    spec.RiskExec,
			Confirm: "SEND",
			Params: []spec.Param{
				{Name: "sessionId", Type: spec.TypeString, Required: true, Positional: true, Desc: "会话 ID"},
				{Name: "text", Type: spec.TypeString, Required: true, Positional: true, Stdin: true, Desc: "要发送的文本（可用 - 从 stdin 读取）"},
				{Name: "enter", Type: spec.TypeBool, Desc: "在文本末尾追加回车（\\r）"},
			},
			Examples: []string{
				`codebox-agent session send abc123 "claude --help" --enter --yes`,
			},
			Notes: "会话必须处于 running（不存在→not_found，非 running→conflict，控制运行时未就绪→not_ready）。≤1KB 文本走键盘输入通道，更长文本走分块粘贴通道（等价于桌面终端粘贴）。写入不可中途取消：超时不等于未送达，重试前先用 session output 查看回显，避免重复输入。",
		},
		{
			Path:    []string{"session", "stop"},
			Summary: "停止一个会话",
			Risk:    spec.RiskDestructive,
			Confirm: "STOP",
			Params: []spec.Param{
				{Name: "sessionId", Type: spec.TypeString, Required: true, Positional: true, Desc: "会话 ID"},
			},
			Examples: []string{`codebox-agent session stop abc123 --yes`},
		},
		{
			Path:    []string{"session", "remove"},
			Summary: "移除已停止会话的记录",
			Risk:    spec.RiskDestructive,
			Confirm: "REMOVE",
			Params: []spec.Param{
				{Name: "sessionId", Type: spec.TypeString, Required: true, Positional: true, Desc: "会话 ID"},
			},
			Examples: []string{`codebox-agent session remove abc123 --yes`},
			Notes:    "会话仍在运行/停止中返回 conflict，先 stop 再 remove。",
		},
		{
			Path:     []string{"session", "clear-stopped"},
			Summary:  "清理全部已停止会话（含控制面墓碑）",
			Risk:     spec.RiskDestructive,
			Confirm:  "CLEAR",
			Params:   []spec.Param{},
			Examples: []string{`codebox-agent session clear-stopped --yes --json`},
			Notes:    "返回逐项结果：cleared/clearedIds/retainedIds/failed（部分清理失败不影响其余条目）。",
		},
	}
}
