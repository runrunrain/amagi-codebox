package install

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"amagi-codebox/internal/agentcli/spec"
)

// docs/user/agent-cli.md 生成区边界标记：标记之间由 UpdateDocsGeneratedRegion
// 重写为命令表产物，之外是人工维护的引言/排查内容。命令表是唯一事实源，
// 防漂移测试 TestAgentCLIDocsUpToDate 锚定两者一致。
const (
	DocsBeginMarker = "<!-- BEGIN generated-command-reference：由 install.GenerateCommandReference(cmds.Table()) 生成，勿手改；重生成：go test ./internal/agentcli/install -run TestAgentCLIDocsUpToDate -update -->"
	DocsEndMarker   = "<!-- END generated-command-reference -->"
)

// ExtractDocsGeneratedRegion 提取文档中 BEGIN/END 标记之间的生成区内容；
// 标记缺失、顺序错误或格式异常时返回 ok=false。
func ExtractDocsGeneratedRegion(doc string) (string, bool) {
	begin := strings.Index(doc, DocsBeginMarker)
	if begin < 0 {
		return "", false
	}
	rest := doc[begin+len(DocsBeginMarker):]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return "", false
	}
	region := rest[nl+1:]
	end := strings.Index(region, DocsEndMarker)
	if end < 0 {
		return "", false
	}
	// 逐字节返回生成区（与 UpdateDocsGeneratedRegion 写入的产物一致）。
	return region[:end], true
}

// UpdateDocsGeneratedRegion 把 docPath 中 BEGIN/END 标记之间的生成区替换为
// 当前命令表产物，保留标记之外的人工内容。
func UpdateDocsGeneratedRegion(docPath string, table []spec.Command) error {
	data, err := os.ReadFile(docPath)
	if err != nil {
		return err
	}
	doc := string(data)
	begin := strings.Index(doc, DocsBeginMarker)
	end := strings.Index(doc, DocsEndMarker)
	if begin < 0 || end < begin {
		return fmt.Errorf("%s 缺少生成区标记（%s / %s）", docPath, DocsBeginMarker, DocsEndMarker)
	}
	beginLineEnd := strings.IndexByte(doc[begin:], '\n')
	if beginLineEnd < 0 {
		return fmt.Errorf("%s 的 BEGIN 标记后缺少换行", docPath)
	}
	head := doc[:begin+beginLineEnd+1] // 含 BEGIN 标记行
	tail := doc[end:]                  // END 标记行起
	generated := GenerateCommandReference(table)
	if !strings.HasSuffix(generated, "\n") {
		generated += "\n"
	}
	return os.WriteFile(docPath, []byte(head+generated+tail), 0o644)
}

// GenerateCommandReference 由命令表生成 Markdown 命令参考：按领域分组、
// 每命令一节（用法行、参数表、风险与确认值、示例）。同一函数供技能内
// references/commands.md 与 docs/user/agent-cli.md 生成使用——命令表是唯一
// 事实源，不维护第二份手写命令清单。
func GenerateCommandReference(table []spec.Command) string {
	var b strings.Builder
	b.WriteString("# codebox-agent 命令参考\n\n")
	b.WriteString("由命令表自动生成；以 `codebox-agent --help` 与各命令的 `--help` 为准。\n\n")
	b.WriteString("风险级：`read` 只读 · `mutate` 修改配置/状态（可逆） · `exec` 启动进程/写入会话/安装软件 · `destructive` 删除/清空（不可逆）。`exec` 与 `destructive` 命令需 `--yes` 注入确认值。\n\n")
	groups := groupCommands(table)
	if len(groups) == 0 {
		b.WriteString("（命令表暂为空。）\n")
		return b.String()
	}
	for _, g := range groups {
		b.WriteString("## " + g.title + "\n\n")
		for _, c := range g.commands {
			writeCommandSection(&b, c)
		}
	}
	return b.String()
}

// commandGroup 一个领域分组（title 为呈现标题）。
type commandGroup struct {
	title    string
	commands []spec.Command
}

// domainOrder 已知领域的呈现顺序；domainTitles 为标题。firstWordDomain 把
// 命令首词映射到领域——这是 docgen 的呈现层知识（仅影响分组与顺序），
// 未知首词各自成组、排在已知领域之后（保持首现顺序），不假定命令表内容。
var domainOrder = []string{"general", "system", "config", "session", "usage", "plugin", "git"}

var domainTitles = map[string]string{
	"general": "通用",
	"system":  "系统与状态",
	"config":  "配置",
	"session": "会话",
	"usage":   "用量与日志",
	"plugin":  "插件",
	"git":     "Git",
}

var firstWordDomain = map[string]string{
	"status": "system", "cli": "system", "env": "system", "remote": "system",
	"provider": "config", "preset": "config", "key": "config",
	"settings": "config", "envvars": "config", "workdir": "config",
	"session": "session",
	"usage":   "usage", "logs": "usage",
	"plugin": "plugin",
	"git":    "git",
}

func groupCommands(table []spec.Command) []commandGroup {
	buckets := map[string][]spec.Command{}
	unknownOrder := []string{}
	for _, c := range table {
		key := "general" // 单词命令（如 status/logs）归入通用组
		if len(c.Path) > 1 {
			key = c.Path[0]
			if mapped, ok := firstWordDomain[key]; ok {
				key = mapped
			}
		}
		if _, seen := buckets[key]; !seen {
			if _, known := domainTitles[key]; !known {
				unknownOrder = append(unknownOrder, key)
			}
		}
		buckets[key] = append(buckets[key], c)
	}
	order := make([]string, 0, len(domainOrder)+len(unknownOrder))
	order = append(order, domainOrder...)
	order = append(order, unknownOrder...)
	groups := make([]commandGroup, 0, len(buckets))
	for _, key := range order {
		cmds, ok := buckets[key]
		if !ok || len(cmds) == 0 {
			continue
		}
		sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name() < cmds[j].Name() })
		title := domainTitles[key]
		if title == "" {
			title = key
		}
		groups = append(groups, commandGroup{title: title, commands: cmds})
	}
	return groups
}

func writeCommandSection(b *strings.Builder, c spec.Command) {
	b.WriteString("### " + c.Name() + "\n\n")
	if c.Summary != "" {
		b.WriteString(escapePipe(c.Summary) + "\n\n")
	}
	b.WriteString("```\n" + commandUsage(c) + "\n```\n\n")
	if len(c.Params) > 0 {
		b.WriteString("| 参数 | 形态 | 类型 | 必填 | 说明 |\n")
		b.WriteString("|---|---|---|---|---|\n")
		for _, p := range c.Params {
			b.WriteString(paramRow(p) + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(riskLine(c) + "\n\n")
	if c.Timeout > 0 {
		b.WriteString(fmt.Sprintf("超时 %d 秒。\n\n", c.Timeout))
	}
	if c.Notes != "" {
		b.WriteString(escapePipe(c.Notes) + "\n\n")
	}
	if len(c.Examples) > 0 {
		b.WriteString("示例：\n\n")
		for _, ex := range c.Examples {
			b.WriteString("```\n" + ex + "\n```\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
}

// commandUsage 生成用法行：codebox-agent <命令词> [位置参数] [旗标]。
// 位置参数按声明顺序：必填 <name>、可选 [name]；旗标为 --kebab-case，
// bool 旗标不带值；可选旗标加方括号。
func commandUsage(c spec.Command) string {
	parts := []string{"codebox-agent"}
	parts = append(parts, c.Path...)
	for _, p := range c.Params {
		if !p.Positional {
			continue
		}
		if p.Required {
			parts = append(parts, "<"+p.Name+">")
		} else {
			parts = append(parts, "["+p.Name+"]")
		}
	}
	for _, p := range c.Params {
		if p.Positional {
			continue
		}
		flag := "--" + kebabCase(p.Name)
		if p.Type != spec.TypeBool {
			flag = flag + " <" + p.Name + ">"
		}
		if !p.Required {
			flag = "[" + flag + "]"
		}
		parts = append(parts, flag)
	}
	return strings.Join(parts, " ")
}

func paramRow(p spec.Param) string {
	form, name := "旗标", "--"+kebabCase(p.Name)
	if p.Positional {
		form, name = "位置", p.Name
	}
	required := "否"
	if p.Required {
		required = "是"
	}
	return fmt.Sprintf("| `%s` | %s | `%s` | %s | %s |", name, form, string(p.Type), required, paramDesc(p))
}

// paramDesc 汇总参数说明与机器可读属性（枚举/默认/stdin/敏感）。
func paramDesc(p spec.Param) string {
	var parts []string
	if p.Desc != "" {
		parts = append(parts, escapePipe(p.Desc))
	}
	if len(p.Enum) > 0 {
		escaped := make([]string, len(p.Enum))
		for i, v := range p.Enum {
			escaped[i] = escapePipe(v)
		}
		parts = append(parts, "可选值："+strings.Join(escaped, " / "))
	}
	if p.Default != nil {
		parts = append(parts, fmt.Sprintf("默认 %v", p.Default))
	}
	if p.Stdin {
		parts = append(parts, "支持 `-` 从 stdin 读取")
	}
	if p.Secret {
		parts = append(parts, "敏感值，日志与回显脱敏")
	}
	return strings.Join(parts, "；")
}

var riskText = map[spec.Risk]string{
	spec.RiskRead:        "只读",
	spec.RiskMutate:      "修改配置/状态，可逆",
	spec.RiskExec:        "启动进程/写入会话/安装软件",
	spec.RiskDestructive: "删除/清空，不可逆",
}

func riskLine(c spec.Command) string {
	text := riskText[c.Risk]
	if text == "" {
		text = string(c.Risk)
	}
	if c.Confirm != "" {
		return fmt.Sprintf("风险：`%s`（%s）；确认值 `%s`——执行需 `--yes`。", c.Risk, text, c.Confirm)
	}
	return fmt.Sprintf("风险：`%s`（%s）。", c.Risk, text)
}

// kebabCase camelCase → kebab-case（buildId → build-id），与 CLI 旗标命名一致。
func kebabCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapePipe 转义 Markdown 表格单元格中的竖线。
func escapePipe(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
