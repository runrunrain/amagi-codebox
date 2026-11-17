package settings

// claude_webui_settings_test.go — ClaudeWebUIEnabled 设置项（claude-webui 设计
// §8）：指针三态实现"默认 true"——老 settings.json 无该键 → true；显式 false →
// false 且重载后保持；显式 true → true。照 agent_cli_settings_test.go 的锚定
// 方式（三态默认 / 持久化回读 / 落盘形态）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeWebUIEnabled_DefaultTrueForMissingKey(t *testing.T) {
	dir := t.TempDir()
	// 老文件：无 claudeWebUIEnabled 键（模拟升级前的 settings.json）。
	old := `{"dashboard":{"mode":"embedded"},"remotePort":8680}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(old), 0o600); err != nil {
		t.Fatalf("write old settings: %v", err)
	}

	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !svc.GetClaudeWebUIEnabled() {
		t.Fatal("老 settings.json 无 claudeWebUIEnabled 键时应默认 true")
	}
	// 默认 true 不应被意外持久化为显式值（nil 保持 nil）。
	got := svc.GetSettings().ClaudeWebUIEnabled
	if got != nil {
		t.Fatalf("未显式设置时 ClaudeWebUIEnabled 应保持 nil（默认 true），got %v", *got)
	}
}

func TestClaudeWebUIEnabled_SetFalseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := svc.SetClaudeWebUIEnabled(false); err != nil {
		t.Fatalf("set false: %v", err)
	}
	if svc.GetClaudeWebUIEnabled() {
		t.Fatal("显式关闭后 GetClaudeWebUIEnabled 应为 false")
	}

	// 落盘重载：显式 false 必须存活（指针三态存在的全部理由——开关只在启动时
	// 读取，跨进程重启后必须仍读到用户关闭态）。
	svc2 := NewService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if svc2.GetClaudeWebUIEnabled() {
		t.Fatal("显式 false 持久化后重载应为 false")
	}

	// settings.json 里必须是显式 false 而非缺键。
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	v, ok := raw["claudeWebUIEnabled"]
	if !ok || v != false {
		t.Fatalf("settings.json 应含显式 \"claudeWebUIEnabled\": false，got %v (present=%v)", v, ok)
	}
}

func TestClaudeWebUIEnabled_SetTrueRoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := svc.SetClaudeWebUIEnabled(false); err != nil {
		t.Fatalf("set false: %v", err)
	}
	if err := svc.SetClaudeWebUIEnabled(true); err != nil {
		t.Fatalf("set true: %v", err)
	}
	if !svc.GetClaudeWebUIEnabled() {
		t.Fatal("重新开启后应为 true")
	}
	svc2 := NewService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !svc2.GetClaudeWebUIEnabled() {
		t.Fatal("显式 true 持久化后重载应为 true")
	}
}
