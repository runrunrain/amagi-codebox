package piplugin

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// envValue reads a KEY=VALUE entry from an env slice; returns ("", false) if absent.
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range env {
		if len(e) > len(prefix) && e[:len(prefix)] == prefix {
			return e[len(prefix):], true
		}
	}
	return "", false
}

// TestExecutePiCommandInjectsAgentDir verifies that pi CLI write operations
// (install/remove/update) explicitly inject PI_CODING_AGENT_DIR pointing at the
// shared standard user agent root.
func TestExecutePiCommandInjectsAgentDir(t *testing.T) {
	// 用可创建的临时目录：executePiCommand 现在会先确保 agent 根存在
	//（Dir=agentDir 需要 cwd 可用，见 TestExecutePiCommandCreatesMissingAgentDir）。
	agentDir := filepath.Join(t.TempDir(), ".pi", "agent")
	runner := &testRunner{}
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, runner)

	if _, err := svc.InstallPackage("npm:foo"); err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("expected 1 CLI call, got %d", len(runner.specs))
	}
	got, ok := envValue(runner.specs[0].Env, "PI_CODING_AGENT_DIR")
	if !ok {
		t.Fatalf("PI_CODING_AGENT_DIR not injected into pi CLI env: %v", runner.specs[0].Env)
	}
	if got != agentDir {
		t.Errorf("PI_CODING_AGENT_DIR = %q, want %q", got, agentDir)
	}
	// the install subcommand + source are unchanged
	wantArgs := []string{"install", "npm:foo"}
	if !reflect.DeepEqual(runner.specs[0].Args, wantArgs) {
		t.Errorf("CLI args = %#v, want %#v", runner.specs[0].Args, wantArgs)
	}
}

// TestExecutePiCommandCreatesMissingAgentDir 回归验证：executePiCommand 以
// Dir=agentDir 运行 pi CLI，全新机器上 ~/.pi/agent 尚未创建时，必须先建目录，
// 否则 exec 会在 pi CLI 启动前就 chdir 失败（插件面板可能先于首次 pi 会话使用）。
func TestExecutePiCommandCreatesMissingAgentDir(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), ".pi", "agent")
	runner := &testRunner{}
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, runner)

	if _, err := svc.InstallPackage("npm:foo"); err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}
	if info, err := os.Stat(agentDir); err != nil || !info.IsDir() {
		t.Fatalf("expected agent dir to be created before running pi CLI, err=%v", err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("expected 1 CLI call, got %d", len(runner.specs))
	}
	if got, ok := envValue(runner.specs[0].Env, "PI_CODING_AGENT_DIR"); !ok || got != agentDir {
		t.Errorf("PI_CODING_AGENT_DIR = %q (ok=%v), want %q", got, ok, agentDir)
	}
}
