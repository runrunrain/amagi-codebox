package cmds

import (
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

// d1Commands 聚合 D1 负责的四个领域（system/config/usage/git）命令组。
// 会话与插件域由并行任务填充，其形状约束由各自测试与 root 的 1:1 断言覆盖；
// 命令名唯一性是对全表的契约，在本文件锚定。
func d1Commands() []spec.Command {
	var all []spec.Command
	all = append(all, systemCommands()...)
	all = append(all, configCommands()...)
	all = append(all, usageCommands()...)
	all = append(all, gitCommands()...)
	return all
}

// TestTableNameUnique 全表命令名必须唯一（点号形态是 Handler 注册键）。
func TestTableNameUnique(t *testing.T) {
	table := Table()
	if len(table) == 0 {
		t.Fatal("命令表为空")
	}
	seen := map[string]bool{}
	for _, c := range table {
		name := c.Name()
		if name == "" {
			t.Fatalf("存在空命令名: %+v", c)
		}
		if seen[name] {
			t.Fatalf("命令名重复: %s", name)
		}
		seen[name] = true
	}
}

// TestD1CommandCoverage D1 四域命令集与设计 §4 清单一一对应（防拼写漂移）。
func TestD1CommandCoverage(t *testing.T) {
	want := map[string]bool{
		"status": true, "cli.info": true, "env.status": true, "env.check": true,
		"env.install": true, "remote.status": true,
		"provider.list": true, "provider.get": true, "provider.save": true,
		"provider.key-set": true, "provider.delete": true, "key.status": true,
		"preset.list": true, "preset.save": true, "preset.delete": true,
		"settings.get": true, "settings.set": true,
		"envvars.list": true, "envvars.set": true, "envvars.delete": true,
		"workdir.list": true, "workdir.add": true, "workdir.remove": true,
		"usage.summary": true, "usage.daily": true, "usage.models": true,
		"usage.providers": true, "usage.requests": true, "usage.sync": true,
		"logs":          true,
		"git.repo-info": true, "git.branches": true,
	}
	got := map[string]bool{}
	for _, c := range d1Commands() {
		got[c.Name()] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("缺少命令 %s", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("出现设计清单之外的命令 %s", name)
		}
	}
}

// TestD1CommandShape D1 命令的表级硬约束：确认值、示例、参数形状、
// 位置参数顺序、必填/默认互斥、枚举类型。
func TestD1CommandShape(t *testing.T) {
	for _, c := range d1Commands() {
		if c.Summary == "" {
			t.Errorf("%s: 缺少 Summary", c.Name())
		}
		if len(c.Examples) == 0 {
			t.Errorf("%s: 缺少 Examples", c.Name())
		}
		switch c.Risk {
		case spec.RiskExec, spec.RiskDestructive:
			if c.Confirm == "" {
				t.Errorf("%s: %s 级命令必须登记 Confirm", c.Name(), c.Risk)
			}
		case spec.RiskRead, spec.RiskMutate:
			if c.Confirm != "" {
				t.Errorf("%s: %s 级命令不应登记 Confirm", c.Name(), c.Risk)
			}
		default:
			t.Errorf("%s: 未知风险级 %q", c.Name(), c.Risk)
		}
		if c.Timeout < 0 {
			t.Errorf("%s: Timeout 不应为负", c.Name())
		}

		paramNames := map[string]bool{}
		sawOptionalPositional := false
		for i, p := range c.Params {
			if p.Name == "" {
				t.Errorf("%s: 参数 %d 缺少 Name", c.Name(), i)
			}
			if paramNames[p.Name] {
				t.Errorf("%s: 参数名重复 %s", c.Name(), p.Name)
			}
			paramNames[p.Name] = true
			if p.Desc == "" {
				t.Errorf("%s: 参数 %s 缺少中文 Desc", c.Name(), p.Name)
			}
			if len(p.Enum) > 0 && p.Type != spec.TypeString {
				t.Errorf("%s: 参数 %s 声明 Enum 但类型不是 string", c.Name(), p.Name)
			}
			if p.Required && p.Default != nil {
				t.Errorf("%s: 必填参数 %s 不应设置 Default", c.Name(), p.Name)
			}
			if p.Positional {
				if p.Type == spec.TypeStrings && i != len(c.Params)-1 {
					t.Errorf("%s: string[] 位置参数 %s 必须收尾", c.Name(), p.Name)
				}
				if p.Required && sawOptionalPositional {
					t.Errorf("%s: 必填位置参数 %s 出现在可选位置参数之后", c.Name(), p.Name)
				}
				if !p.Required {
					sawOptionalPositional = true
				}
			}
		}
		// 命令词与参数名遵循 kebab/camel 规范（Name camelCase，CLI 侧转 kebab）。
		for _, p := range c.Params {
			if strings.ContainsAny(p.Name, " -") {
				t.Errorf("%s: 参数名 %s 含空格或连字符", c.Name(), p.Name)
			}
		}
		for _, w := range c.Path {
			if w == "" || strings.ContainsAny(w, " .") {
				t.Errorf("%s: 非法命令词 %q", c.Name(), w)
			}
		}
	}
}

// TestD1ExamplesUseCommandName 示例必须包含真实命令词，防示例漂移。
func TestD1ExamplesUseCommandName(t *testing.T) {
	for _, c := range d1Commands() {
		spaced := strings.Join(c.Path, " ")
		for _, ex := range c.Examples {
			if ex != "codebox-agent "+spaced && !strings.Contains(ex, "codebox-agent "+spaced+" ") {
				t.Errorf("%s: 示例未包含命令词: %s", c.Name(), ex)
			}
		}
	}
}

// TestD1StdinParamsAreSecret 允许 stdin 的参数必须同时标记 Secret，
// 保证审计与错误回显脱敏（密钥写入路径的纪律锚点）。
func TestD1StdinParamsAreSecret(t *testing.T) {
	for _, c := range d1Commands() {
		for _, p := range c.Params {
			if p.Stdin && !p.Secret {
				t.Errorf("%s: 参数 %s 声明 Stdin 但未标记 Secret", c.Name(), p.Name)
			}
		}
	}
}
