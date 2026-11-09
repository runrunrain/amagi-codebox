package cmds

// session_test.go — 会话域命令表自检：名称集合、Risk/Confirm 配对、参数约束、
// 表聚合一致性。纯数据不变量，不依赖 App。

import (
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

// checkCommandInvariants 校验单条命令的结构不变量（供各域命令表自检复用）。
func checkCommandInvariants(t *testing.T, c spec.Command) {
	t.Helper()
	name := c.Name()
	if name == "" || strings.Contains(name, " ") {
		t.Errorf("命令名非法: %q", name)
	}
	if strings.TrimSpace(c.Summary) == "" {
		t.Errorf("%s: Summary 为空", name)
	}
	if len(c.Examples) == 0 {
		t.Errorf("%s: 缺少 Examples", name)
	}
	// Risk 与 Confirm 配对：exec/destructive 必须登记确认值；read/mutate 不得登记。
	switch c.Risk {
	case spec.RiskExec, spec.RiskDestructive:
		if c.Confirm == "" {
			t.Errorf("%s: Risk=%s 但 Confirm 为空", name, c.Risk)
		}
	case spec.RiskRead, spec.RiskMutate:
		if c.Confirm != "" {
			t.Errorf("%s: Risk=%s 不应登记 Confirm（得到 %q）", name, c.Risk, c.Confirm)
		}
	default:
		t.Errorf("%s: 未知 Risk %q", name, c.Risk)
	}
	if c.Timeout < 0 {
		t.Errorf("%s: Timeout 为负", name)
	}
	seen := map[string]bool{}
	flagSeen := false
	for _, p := range c.Params {
		if p.Name == "" || strings.Contains(p.Name, "-") {
			t.Errorf("%s: 参数名须为非空 camelCase，得到 %q", name, p.Name)
		}
		if seen[p.Name] {
			t.Errorf("%s: 参数 %s 重复", name, p.Name)
		}
		seen[p.Name] = true
		if p.Positional && flagSeen {
			t.Errorf("%s: 位置参数 %s 声明在旗标参数之后（位置参数应连续前置）", name, p.Name)
		}
		if !p.Positional {
			flagSeen = true
		}
		if p.Default != nil && len(p.Enum) > 0 {
			found := false
			for _, e := range p.Enum {
				if e == p.Default.(string) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: 参数 %s 默认值 %v 不在枚举内", name, p.Name, p.Default)
			}
		}
		if p.Type == "" {
			t.Errorf("%s: 参数 %s 缺少 Type", name, p.Name)
		}
	}
}

func TestSessionCommands_NameSet(t *testing.T) {
	want := map[string]bool{
		"session.list":          false,
		"session.get":           false,
		"session.output":        false,
		"session.launch":        false,
		"session.send":          false,
		"session.stop":          false,
		"session.remove":        false,
		"session.clear-stopped": false,
	}
	for _, c := range sessionCommands() {
		if _, ok := want[c.Name()]; !ok {
			t.Fatalf("sessionCommands 出现未预期命令: %s", c.Name())
		}
		want[c.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("sessionCommands 缺少命令: %s", name)
		}
	}
}

func TestSessionCommands_Invariants(t *testing.T) {
	for _, c := range sessionCommands() {
		checkCommandInvariants(t, c)
	}
}

// TestSessionCommands_InTable 证明会话域命令进入了聚合命令表且内容一致。
func TestSessionCommands_InTable(t *testing.T) {
	table := Table()
	for _, c := range sessionCommands() {
		got, ok := Find(table, c.Name())
		if !ok {
			t.Fatalf("命令 %s 未进入 Table()", c.Name())
		}
		if got.Risk != c.Risk || got.Confirm != c.Confirm || len(got.Params) != len(c.Params) {
			t.Errorf("命令 %s 在 Table() 中的 Risk/Confirm/Params 不一致", c.Name())
		}
	}
}

// TestSessionCommands_LaunchShape 校验 launch 的关键契约：exec+LAUNCH、
// appType 枚举五值、mode 默认 embedded。
func TestSessionCommands_LaunchShape(t *testing.T) {
	c, ok := Find(sessionCommands(), "session.launch")
	if !ok {
		t.Fatal("缺少 session.launch")
	}
	if c.Risk != spec.RiskExec || c.Confirm != "LAUNCH" {
		t.Errorf("launch Risk/Confirm = %s/%q", c.Risk, c.Confirm)
	}
	appType, _ := paramOf(c, "appType")
	if !containsAll(appType.Enum, "claudecode", "opencode", "codex", "pi", "omp") {
		t.Errorf("appType 枚举缺少五种 AppType: %v", appType.Enum)
	}
	if !appType.Required || !appType.Positional {
		t.Errorf("appType 应为必填位置参数")
	}
	mode, _ := paramOf(c, "mode")
	if mode.Default != "embedded" {
		t.Errorf("mode 默认值应为 embedded，得到 %v", mode.Default)
	}
	// send 的 text 支持 stdin（agent 发送多行文本的推荐通道）。
	send, ok := Find(sessionCommands(), "session.send")
	if !ok {
		t.Fatal("缺少 session.send")
	}
	text, _ := paramOf(send, "text")
	if !text.Stdin {
		t.Error("session.send 的 text 参数应支持 stdin（Stdin=true）")
	}
}

func paramOf(c spec.Command, name string) (spec.Param, bool) {
	for _, p := range c.Params {
		if p.Name == name {
			return p, true
		}
	}
	return spec.Param{}, false
}

func containsAll(list []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, v := range list {
			if v == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
