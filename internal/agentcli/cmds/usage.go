package cmds

import "amagi-codebox/internal/agentcli/spec"

// usageCommands 覆盖 usage summary/daily/models/providers/requests/sync 与 logs（设计 §4 usage 域）。
// 过滤参数名与 internal/usage/api_types.go 的 Filter 字段一一对应（camelCase）。
func usageCommands() []spec.Command {
	dateDesc := "可选；UTC 日期（YYYY-MM-DD），闭区间，与 endDate 配对使用"
	filterParams := []spec.Param{
		{Name: "startDate", Type: spec.TypeString, Desc: dateDesc},
		{Name: "endDate", Type: spec.TypeString, Desc: "可选；UTC 日期（YYYY-MM-DD），闭区间"},
		{
			Name: "appType", Type: spec.TypeString,
			Enum: agentCLIAppTypeEnum,
			Desc: "可选；按 CLI 应用过滤：claudecode/opencode/codex/pi/omp",
		},
		{Name: "provider", Type: spec.TypeString, Desc: "可选；按提供商名过滤"},
		{Name: "source", Type: spec.TypeString, Desc: "可选；按数据源过滤（如 session_log），缺省全部"},
	}
	return []spec.Command{
		{
			Path:    []string{"usage", "summary"},
			Summary: "用量汇总：请求/token/成本（按币种）与实际日期范围",
			Risk:    spec.RiskRead,
			Params:  filterParams,
			Examples: []string{
				`codebox-agent usage summary`,
				`codebox-agent usage summary --app-type claudecode --start-date 2026-07-01 --end-date 2026-07-31`,
			},
			Notes: "成本单位为 micro-currency（1e-6）；总成本按币种分组并折算 USD 汇总。",
		},
		{
			Path:    []string{"usage", "daily"},
			Summary: "按日聚合的用量趋势",
			Risk:    spec.RiskRead,
			Params: append([]spec.Param{
				{Name: "days", Type: spec.TypeInt, Default: 30, Desc: "最近 N 天；与 startDate/endDate 互斥，缺省 30"},
				{
					Name: "granularity", Type: spec.TypeString, Default: "day",
					Enum: []string{"day", "week"},
					Desc: "聚合粒度，缺省 day",
				},
			}, filterParams...),
			Examples: []string{
				`codebox-agent usage daily`,
				`codebox-agent usage daily --days 7 --app-type codex`,
				`codebox-agent usage daily --start-date 2026-07-01 --end-date 2026-07-07`,
			},
			Notes: "给了 startDate/endDate 时忽略 days。",
		},
		{
			Path:    []string{"usage", "models"},
			Summary: "按模型聚合的用量统计（含缓存命中率与价格覆盖情况）",
			Risk:    spec.RiskRead,
			Params:  filterParams,
			Examples: []string{
				`codebox-agent usage models`,
				`codebox-agent usage models --provider acme`,
			},
		},
		{
			Path:    []string{"usage", "providers"},
			Summary: "按提供商聚合的用量统计",
			Risk:    spec.RiskRead,
			Params:  filterParams,
			Examples: []string{
				`codebox-agent usage providers`,
			},
		},
		{
			Path:    []string{"usage", "requests"},
			Summary: "请求明细日志（分页）",
			Risk:    spec.RiskRead,
			Params: append([]spec.Param{
				{Name: "model", Type: spec.TypeString, Desc: "可选；按模型名（归一化）过滤"},
				{Name: "page", Type: spec.TypeInt, Default: 1, Desc: "页码，1 起，缺省 1"},
				{Name: "pageSize", Type: spec.TypeInt, Default: 50, Desc: "每页条数，缺省 50，上限 500"},
			}, filterParams...),
			Examples: []string{
				`codebox-agent usage requests`,
				`codebox-agent usage requests --model gpt-5 --page 2 --page-size 20`,
			},
		},
		{
			Path:    []string{"usage", "sync"},
			Summary: "立即同步各 CLI 会话日志到本地用量库",
			Risk:    spec.RiskMutate,
			Examples: []string{
				`codebox-agent usage sync`,
			},
			Notes:   "扫描会话日志文件入库；首次或日志量大时耗时较长。同步不可中途取消：超时不等于未完成，重试前先用 usage summary 查询数据是否已入库。",
			Timeout: 600,
		},
		{
			Path:    []string{"logs"},
			Summary: "查询应用运行日志",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{
					Name: "level", Type: spec.TypeString,
					Enum: []string{"DEBUG", "INFO", "WARN", "ERROR"},
					Desc: "可选；最低日志级别（含该级及以上），缺省全部",
				},
				{Name: "source", Type: spec.TypeString, Desc: "可选；按来源过滤（如 app/session/usage）"},
				{Name: "keyword", Type: spec.TypeString, Desc: "可选；消息与详情的子串匹配"},
				{Name: "limit", Type: spec.TypeInt, Default: 100, Desc: "最多返回条数（新→旧），缺省 100"},
			},
			Examples: []string{
				`codebox-agent logs --limit 20`,
				`codebox-agent logs --level ERROR --keyword timeout`,
			},
			Notes: "输出已做凭据清洗：已知密钥的精确值、Authorization/Bearer 值与常见 token 前缀（sk-/ghp_/xox 等）替换为 ***。",
		},
	}
}
