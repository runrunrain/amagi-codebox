package launcher

// wsl_review_fixes_test.go — wsl-goal-closure 审核 PR02 修复的 launcher 侧
// 回归：零写入路径权限探测不可用（ok=false）不再视为安全（先收紧后复验，
// 仍不可证则 fail closed）。经 WriteWSLOpenCodeAgentConfig 的零写入路径钉住
//（A2 凭据同步的降级复核面已随 Pi 供给退役，1.3.126）。

import (
	"context"
	"os"
	"testing"
)

// TestReviewFixPR02RetightenUnknownModeFailsClosed：权限探测不可用时零写入
// 路径不再静默放行——chmod 后复验仍不可证 → 报错（此前 !ok 直接 return nil）。
func TestReviewFixPR02RetightenUnknownModeFailsClosed(t *testing.T) {
	ft := installFakeWSLTarget(t)
	agentDir := "/home/wslu/.config/opencode"
	if err := os.MkdirAll(ft.uncPath(agentDir), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"model": "openai/gpt-5"}
	if _, err := WriteWSLOpenCodeAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("first write: %v", err)
	}
	before, err := os.ReadFile(ft.uncPath(agentDir + "/amagi-codebox.json"))
	if err != nil {
		t.Fatal(err)
	}
	// 探测面整体不可用（wsl.exe stat 故障形态）：零写入路径必须 fail closed。
	origStatMode := wslStatModeFn
	wslStatModeFn = func(context.Context, string, string) (string, bool) { return "", false }
	t.Cleanup(func() { wslStatModeFn = origStatMode })
	if _, err := WriteWSLOpenCodeAgentConfig("Ubuntu", cfg); err == nil {
		t.Fatal("unknown permission state must not pass the zero-write path")
	}
	after, err := os.ReadFile(ft.uncPath(agentDir + "/amagi-codebox.json"))
	if err != nil || string(before) != string(after) {
		t.Fatalf("identical bytes must not be rewritten, got %q err=%v", after, err)
	}
}

// TestReviewFixPR02RetightenLooseModeVerified：改宽副本被收紧并复验（探测面
// 记录到 600），零字节重写保持。
func TestReviewFixPR02RetightenLooseModeVerified(t *testing.T) {
	ft := installFakeWSLTarget(t)
	agentDir := "/home/wslu/.config/opencode"
	if err := os.MkdirAll(ft.uncPath(agentDir), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"model": "openai/gpt-5"}
	if _, err := WriteWSLOpenCodeAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// 副本被改宽（仅探测面形态——真实文件位由 9P 语义决定，这里钉复核链）。
	ft.modes[agentDir+"/amagi-codebox.json"] = "644"
	if _, err := WriteWSLOpenCodeAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("retighten must recover the loose copy: %v", err)
	}
	if m, ok := ft.modes[agentDir+"/amagi-codebox.json"]; !ok || m != "600" {
		t.Fatalf("loose copy must be re-tightened and verified, got %q ok=%v", m, ok)
	}
}
