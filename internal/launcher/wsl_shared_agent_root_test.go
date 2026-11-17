package launcher

import (
	"strings"
	"testing"
)

func envLookup(env []string, key string) (string, bool) {
	for _, kv := range env {
		k, v := splitEnvKV(kv)
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

// TestApplyWSLSharedPiAgentRoot：缺省钉 Windows agent 根并登记路径放行键；
// 已有值（登录会话钉定）沿用；Linux 路径值不加标志；不修改入参。
func TestApplyWSLSharedPiAgentRoot(t *testing.T) {
	win := `C:\Users\u\.pi\agent`

	in := []string{"LANG=en", "PI_CODING_AGENT_DIR="}
	flags := []string{"MY_KEY"}
	env, got := ApplyWSLSharedPiAgentRoot(in, win, flags)
	if v, ok := envLookup(env, "PI_CODING_AGENT_DIR"); !ok || v != win {
		t.Fatalf("agent root = %q ok=%v, want %q", v, ok, win)
	}
	if len(got) != 2 || got[0] != "MY_KEY" || got[1] != "PI_CODING_AGENT_DIR" {
		t.Fatalf("path flag keys = %v", got)
	}
	if len(flags) != 1 || in[1] != "PI_CODING_AGENT_DIR=" {
		t.Fatal("inputs must not be mutated")
	}

	// 已有非空值沿用（登录会话钉定的 canonical 目录）。
	env, _ = ApplyWSLSharedPiAgentRoot([]string{`pi_coding_agent_dir=D:\x\agent`}, win, nil)
	if v, _ := envLookup(env, "PI_CODING_AGENT_DIR"); v != `D:\x\agent` {
		t.Fatalf("existing value must win: %q", v)
	}
	if n := strings.Count(strings.ToUpper(strings.Join(env, "|")), "PI_CODING_AGENT_DIR="); n != 1 {
		t.Fatalf("duplicate key: %v", env)
	}

	// Linux 路径值：原样透传，不登记 /p 标志。
	env, got = ApplyWSLSharedPiAgentRoot([]string{"PI_CODING_AGENT_DIR=/home/u/.pi/agent"}, win, nil)
	if v, _ := envLookup(env, "PI_CODING_AGENT_DIR"); v != "/home/u/.pi/agent" {
		t.Fatalf("linux value rewritten: %q", v)
	}
	if len(got) != 0 {
		t.Fatalf("linux path must not be path-flagged: %v", got)
	}

	// 无可钉定的根：不凭空造值。
	env, got = ApplyWSLSharedPiAgentRoot([]string{"LANG=en"}, "", nil)
	if _, ok := envLookup(env, "PI_CODING_AGENT_DIR"); ok || len(got) != 0 {
		t.Fatalf("no root available must stay unset: %v %v", env, got)
	}
}

// TestApplyWSLSharedPiAgentRootSurvivesStrip：钉定后的盘符路径经放行键躲过
// StripWSLHostPathEnvWithAllow（1.3.59 垃圾目录防线不再误杀共享根）。
func TestApplyWSLSharedPiAgentRootSurvivesStrip(t *testing.T) {
	win := `C:\Users\u\.pi\agent`
	env, flags := ApplyWSLSharedPiAgentRoot([]string{"PI_OFFLINE=1"}, win, nil)
	kept, stripped, allowed := StripWSLHostPathEnvWithAllow(env, nil, flags)
	if len(stripped) != 0 {
		t.Fatalf("shared root must not be stripped: %v", stripped)
	}
	if len(allowed) != 1 || allowed[0] != "PI_CODING_AGENT_DIR" {
		t.Fatalf("allowed = %v", allowed)
	}
	if v, _ := envLookup(kept, "PI_CODING_AGENT_DIR"); v != win {
		t.Fatalf("kept value = %q", v)
	}
	// 对照：不放行时仍被剥离（默认防泄漏基线不变）。
	_, stripped, _ = StripWSLHostPathEnvWithAllow(env, nil, nil)
	if len(stripped) != 1 {
		t.Fatalf("baseline strip must remain for unflagged key: %v", stripped)
	}
}
