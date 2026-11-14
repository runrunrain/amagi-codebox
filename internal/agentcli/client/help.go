package client

// help.go — 帮助文本与拼写建议，全部由命令表生成（DESIGN §2）：
// 总览（按首词分组）、组帮助、单命令（用法行/参数表/示例/风险与确认值/Notes）。
// help <命令|组>、<命令> --help、裸组名均可触达。

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
)

const progName = "codebox-agent"

type cmdGroup struct {
	name string
	cmds []spec.Command
}

// groupCommands 按命令首词分组（组名升序，组内保持命令表顺序）。
func groupCommands(table []spec.Command) []cmdGroup {
	byGroup := map[string]*cmdGroup{}
	var names []string
	for _, c := range table {
		g := c.Path[0]
		if _, ok := byGroup[g]; !ok {
			byGroup[g] = &cmdGroup{name: g}
			names = append(names, g)
		}
		byGroup[g].cmds = append(byGroup[g].cmds, c)
	}
	sort.Strings(names)
	out := make([]cmdGroup, 0, len(names))
	for _, n := range names {
		out = append(out, *byGroup[n])
	}
	return out
}

func riskTag(c spec.Command) string {
	switch c.Risk {
	case spec.RiskExec, spec.RiskDestructive:
		return fmt.Sprintf("（风险 %s，需 --yes）", c.Risk)
	default:
		return ""
	}
}

// commandDisplayName 是帮助/建议里的命令名形态（空格分隔的完整路径）。
func commandDisplayName(c spec.Command) string {
	return strings.Join(c.Path, " ")
}

// renderOverview 输出总览帮助：用法、全局旗标、按首词分组的命令清单。
func renderOverview(w io.Writer, table []spec.Command, appVersion string) {
	bw := bestEffortWriter{w: w} // 尽力而为输出（见 besteffort.go）
	bw.printf("%s — Amagi CodeBox 命令行（供 AI agent 使用）v%s\n\n", progName, appVersion)
	bw.printf("用法：\n")
	bw.printf("  %s [全局旗标] <命令> [参数]\n", progName)
	bw.printf("  %s help <命令|组>\n", progName)
	bw.printf("  %s completion bash|zsh|fish\n\n", progName)
	bw.printf("全局旗标：\n")
	bw.printf("  --json             以 JSON 包络输出（stdout 仅一个 Envelope，诊断走 stderr）\n")
	bw.printf("  --yes, -y          确认执行 exec/destructive 命令\n")
	bw.printf("  --home <dir>       配置目录（默认 $AMAGI_CODEBOX_HOME 或 ~/.amagi-codebox）\n")
	bw.printf("  --timeout <秒>     命令超时覆盖（默认 120，探活固定 5）\n")
	bw.printf("  --help, -h         帮助\n")
	bw.printf("  --version          版本\n\n")
	bw.printf("命令（按组；help/completion/--version 为本地命令，不需要应用运行）：\n")
	groups := groupCommands(table)
	// 列宽 = 全部命令名的最大显示宽度（CJK 感知），padding 后固定追加 2 空格，
	// 保证命令名再长也不与说明粘连（回归锚定：plugin uninstall / session clear-stopped）。
	maxw := 0
	for _, g := range groups {
		for _, c := range g.cmds {
			if w := displayWidth(commandDisplayName(c)); w > maxw {
				maxw = w
			}
		}
	}
	for _, g := range groups {
		if len(g.cmds) == 1 && len(g.cmds[0].Path) == 1 {
			c := g.cmds[0]
			// 缩进 2 + 补到 maxw+2，与组内条目（缩进 4 + 补到 maxw）的说明列对齐。
			bw.printf("  %s  %s%s\n", padDisplay(c.Path[0], maxw+2), c.Summary, riskTag(c))
			continue
		}
		bw.printf("  %s\n", g.name)
		for _, c := range g.cmds {
			bw.printf("    %s  %s%s\n", padDisplay(commandDisplayName(c), maxw), c.Summary, riskTag(c))
		}
	}
	bw.printf("\n退出码：0 成功 / 1 业务错误 / 2 用法错误 / 3 应用不可达或未开启 / 4 需要确认（--yes）。\n")
	bw.printf("单命令详情：%s help <命令>；参数旗标为 --kebab-case，`--` 之后全为位置参数。\n", progName)
}

// renderGroupHelp 输出组帮助；组不存在返回 false。
func renderGroupHelp(w io.Writer, table []spec.Command, group, appVersion string) bool {
	bw := bestEffortWriter{w: w} // 尽力而为输出（见 besteffort.go）
	var cmds []spec.Command
	for _, c := range table {
		if c.Path[0] == group {
			cmds = append(cmds, c)
		}
	}
	if len(cmds) == 0 {
		return false
	}
	bw.printf("%s %s — 命令组（%d 条）\n\n", progName, group, len(cmds))
	width := 0
	for _, c := range cmds {
		if w := displayWidth(strings.Join(c.Path, " ")); w > width {
			width = w
		}
	}
	for _, c := range cmds {
		bw.printf("  %s  %s%s\n", padDisplay(strings.Join(c.Path, " "), width), c.Summary, riskTag(c))
	}
	bw.printf("\n单命令详情：%s help %s <子命令>\n", progName, group)
	return true
}

// renderCommandHelp 输出单命令帮助：用法行、参数表、风险与确认值、示例、Notes。
func renderCommandHelp(w io.Writer, cmd spec.Command, appVersion string) {
	bw := bestEffortWriter{w: w} // 尽力而为输出（见 besteffort.go）
	bw.printf("%s %s — %s\n\n", progName, strings.Join(cmd.Path, " "), cmd.Summary)

	// 用法行：命令词 + 位置参数（必填 <x> / 可选 [x]）。
	usage := append([]string{}, cmd.Path...)
	for _, p := range cmd.Params {
		if !p.Positional {
			continue
		}
		if p.Required {
			usage = append(usage, "<"+p.Name+">")
		} else {
			usage = append(usage, "[<"+p.Name+">]")
		}
	}
	hasFlags := false
	for _, p := range cmd.Params {
		if !p.Positional {
			hasFlags = true
			break
		}
	}
	if hasFlags {
		usage = append(usage, "[参数旗标]")
	}
	bw.printf("用法：\n  %s %s\n\n", progName, strings.Join(usage, " "))

	if len(cmd.Params) > 0 {
		bw.printf("参数：\n")
		type row struct{ form, typ, req, desc string }
		var rows []row
		for _, p := range cmd.Params {
			form := "--" + kebab(p.Name)
			if p.Positional {
				form = "<" + p.Name + ">"
			}
			req := "可选"
			if p.Required {
				req = "必填"
			}
			desc := p.Desc
			if len(p.Enum) > 0 {
				desc += "（枚举 " + strings.Join(p.Enum, "|") + "）"
			}
			if p.Default != nil {
				desc += fmt.Sprintf("（默认 %v）", p.Default)
			}
			if p.Stdin {
				desc += "（可用 - 从 stdin 读取）"
			}
			if p.Secret {
				desc += "（敏感值，建议用 - 传入）"
			}
			rows = append(rows, row{form, string(p.Type), req, desc})
		}
		wf, wt, wr := 0, 0, 0
		for _, r := range rows {
			wf = max(wf, displayWidth(r.form))
			wt = max(wt, displayWidth(r.typ))
			wr = max(wr, displayWidth(r.req))
		}
		for _, r := range rows {
			bw.printf("  %s  %s  %s  %s\n",
				padDisplay(r.form, wf), padDisplay(r.typ, wt), padDisplay(r.req, wr), r.desc)
		}
		bw.println()
	}

	bw.printf("风险与确认：\n  风险级 %s", cmd.Risk)
	if cmd.Confirm != "" {
		bw.printf(" — 执行需 --yes（确认值 %s）", cmd.Confirm)
	}
	bw.println()

	if len(cmd.Examples) > 0 {
		bw.printf("\n示例：\n")
		for _, e := range cmd.Examples {
			bw.printf("  %s\n", e)
		}
	}
	if cmd.Notes != "" {
		bw.printf("\n说明：\n  %s\n", strings.ReplaceAll(cmd.Notes, "\n", "\n  "))
	}
	if cmd.Timeout > 0 {
		bw.printf("\n超时：本命令默认 %d 秒（--timeout 可覆盖）\n", cmd.Timeout)
	}
}

// suggestCommands 拼写建议：前缀命中或编辑距离 ≤2 的命令/组，按距离升序最多 3 条。
func suggestCommands(table []spec.Command, inputs ...string) []string {
	type cand struct {
		name, summary string
		dist          int
	}
	var out []cand
	seen := map[string]bool{}
	add := func(name, summary string) {
		if seen[name] {
			return
		}
		d := -1
		for _, in := range inputs {
			if in == "" {
				continue
			}
			dd := levenshtein(in, name)
			if strings.HasPrefix(name, in) {
				dd = 0
			}
			if d < 0 || dd < d {
				d = dd
			}
		}
		if d >= 0 && d <= 2 {
			seen[name] = true
			out = append(out, cand{name, summary, d})
		}
	}
	for _, c := range table {
		add(strings.Join(c.Path, " "), c.Summary)
		add(c.Path[0], "命令组 "+c.Path[0])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].dist < out[j].dist })
	if len(out) > 3 {
		out = out[:3]
	}
	res := make([]string, 0, len(out))
	for _, c := range out {
		res = append(res, c.name+"  "+c.summary)
	}
	return res
}

// levenshtein 是 rune 级编辑距离。
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
