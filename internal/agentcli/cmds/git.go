package cmds

import "amagi-codebox/internal/agentcli/spec"

// gitCommands 覆盖 git repo-info / branches（设计 §4 git 域，只读面；
// commit/push 等写面按设计 §3.5 明确排除）。
func gitCommands() []spec.Command {
	return []spec.Command{
		{
			Path:    []string{"git", "repo-info"},
			Summary: "查看目录的 git 仓库状态快照（分支/上游/变更计数）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{
					Name: "dir", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "工作目录绝对路径",
				},
			},
			Examples: []string{
				`codebox-agent git repo-info /Users/me/work/proj`,
			},
			Notes: "非 git 仓库不报错，返回 isGitRepo=false；提交/推送不在 CLI 面内。只读查询可安全重试。",
		},
		{
			Path:    []string{"git", "branches"},
			Summary: "列出目录的本地分支（标注当前分支）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{
					Name: "dir", Type: spec.TypeString, Required: true, Positional: true,
					Desc: "工作目录绝对路径",
				},
			},
			Examples: []string{
				`codebox-agent git branches /Users/me/work/proj`,
			},
			Notes: "非 git 仓库返回 not_found。只读查询可安全重试；Handler 入口已检查取消（底层 gitassist 暂无 ctx 传递接口）。",
		},
	}
}
