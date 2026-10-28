package config

// rotation_config_test.go — ClaudeAccountRotation 配置持久化测试（design §3.2）：
// 加载/保存往返、旧配置无字段=关闭、未知 strategy 拒绝不默默回退、
// GetConfig 深拷贝指针、导入导出不携带该字段。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/claudeaccounts"
)

func writeRotationConfigFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write models.json: %v", err)
	}
}

func TestRotationConfig_AbsentMeansDisabled(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := svc.GetClaudeAccountRotation(); got != nil {
		t.Fatalf("absent field must read as nil (disabled), got %+v", got)
	}
	cfg := svc.GetConfig()
	if cfg.ClaudeAccountRotation != nil {
		t.Fatalf("AppConfig.ClaudeAccountRotation must be nil for legacy config")
	}
}

func TestRotationConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	ack := "claude-rotation-v1"
	cfg := claudeaccounts.DefaultRotationConfig()
	cfg.Enabled = true
	cfg.AcknowledgedRiskVersion = &ack
	if err := svc.SetClaudeAccountRotation(cfg); err != nil {
		t.Fatalf("set: %v", err)
	}

	// 重新加载服务验证持久化往返。
	svc2 := NewConfigService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := svc2.GetClaudeAccountRotation()
	if got == nil || !got.Enabled || got.Strategy != claudeaccounts.StrategyBest ||
		got.ThresholdPercent != 90 || got.AcknowledgedRiskVersion == nil || *got.AcknowledgedRiskVersion != ack {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestRotationConfig_LoadRejectsUnknownStrategy(t *testing.T) {
	dir := t.TempDir()
	writeRotationConfigFile(t, dir, `{"models":{},"claude_account_rotation":{"schema_version":1,"enabled":true,"strategy":"greedy","threshold_percent":90,"cooldown_seconds":300,"hysteresis_points":10,"manual_respect_seconds":3600}}`)
	svc := NewConfigService(dir)
	err := svc.Load()
	if err == nil || !strings.Contains(err.Error(), "claude_account_rotation") {
		t.Fatalf("unknown strategy must be rejected at load with field-scoped error, got %v", err)
	}
}

func TestRotationConfig_LoadRejectsOutOfRangeThreshold(t *testing.T) {
	dir := t.TempDir()
	writeRotationConfigFile(t, dir, `{"models":{},"claude_account_rotation":{"schema_version":1,"enabled":false,"strategy":"best","threshold_percent":50,"cooldown_seconds":300,"hysteresis_points":10,"manual_respect_seconds":3600}}`)
	svc := NewConfigService(dir)
	if err := svc.Load(); err == nil {
		t.Fatal("out-of-range threshold must be rejected at load, not silently clamped")
	}
}

func TestRotationConfig_SetValidatesBeforeSave(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	bad := claudeaccounts.DefaultRotationConfig()
	bad.Strategy = "consume-first"
	if err := svc.SetClaudeAccountRotation(bad); err == nil {
		t.Fatal("Set must reject unknown strategy")
	}
	if svc.GetClaudeAccountRotation() != nil {
		t.Fatal("rejected config must not be saved")
	}
}

func TestRotationConfig_GetConfigDeepCopiesPointer(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg := claudeaccounts.DefaultRotationConfig()
	cfg.Enabled = true
	ack := "claude-rotation-v1"
	cfg.AcknowledgedRiskVersion = &ack
	if err := svc.SetClaudeAccountRotation(cfg); err != nil {
		t.Fatalf("set: %v", err)
	}
	snapshot := svc.GetConfig()
	if snapshot.ClaudeAccountRotation == nil {
		t.Fatal("GetConfig must expose ClaudeAccountRotation")
	}
	snapshot.ClaudeAccountRotation.Enabled = false
	*snapshot.ClaudeAccountRotation.AcknowledgedRiskVersion = "tampered"
	if svc.GetConfig().ClaudeAccountRotation.Enabled != true {
		t.Fatal("mutating GetConfig snapshot must not leak into service state")
	}
	if *svc.GetConfig().ClaudeAccountRotation.AcknowledgedRiskVersion == "tampered" {
		t.Fatal("inner AcknowledgedRiskVersion pointer must be deep-copied")
	}
}

func TestRotationConfig_ExportExcludesField(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg := claudeaccounts.DefaultRotationConfig()
	cfg.Enabled = true
	if err := svc.SetClaudeAccountRotation(cfg); err != nil {
		t.Fatalf("set: %v", err)
	}
	// ExportConfig 是独立结构（types.go），按字段组装——验证序列化产物不含
	// claude_account_rotation（导入导出不携带本机账号设置，design §3.2）。
	exported := ExportConfig{Version: "v-test", Source: "test", Providers: map[string]ExportProvider{}}
	data, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "claude_account_rotation") {
		t.Fatal("ExportConfig must never carry claude_account_rotation")
	}
}
