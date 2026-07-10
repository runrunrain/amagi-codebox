package headroom

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// sampleSavingsJSON 是 `headroom savings --json` 的实测真实输出样例
// （schema_version 1），用于验证 SavingsReport 的反序列化逻辑。
const sampleSavingsJSON = `{
  "schema_version": 1,
  "path": "C:\\Users\\Administrator\\.headroom\\savings_events.jsonl",
  "top_model": "glm-5.2",
  "lifetime": { "tokens_saved": 995, "tokens_before": 142826, "cost_usd": 0.002985, "calls": 3, "savings_percent": 0.7 },
  "windows": {
    "today":       { "tokens_saved": 995, "tokens_before": 142826, "cost_usd": 0.002985, "calls": 3, "savings_percent": 0.7 },
    "last_7_days": { "tokens_saved": 995, "tokens_before": 142826, "cost_usd": 0.002985, "calls": 3, "savings_percent": 0.7 },
    "all_time":    { "tokens_saved": 995, "tokens_before": 142826, "cost_usd": 0.002985, "calls": 3, "savings_percent": 0.7 }
  },
  "by_model": [ { "model": "glm-5.2", "tokens_saved": 995, "tokens_before": 142826, "cost_usd": 0.002985, "calls": 3, "savings_percent": 0.7 } ],
  "by_client": [ { "client": "claude-code", "tokens_saved": 995, "tokens_before": 142826, "cost_usd": 0.002985, "calls": 3, "savings_percent": 0.7 } ]
}`

// TestSavingsReport_Unmarshal 验证 SavingsReport 能正确反序列化真实样例，
// 断言当前 ledger 数据的关键指标（calls=3、tokens_saved=995）。
func TestSavingsReport_Unmarshal(t *testing.T) {
	var report SavingsReport
	if err := json.Unmarshal([]byte(sampleSavingsJSON), &report); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if report.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", report.SchemaVersion)
	}
	if report.TopModel != "glm-5.2" {
		t.Errorf("top_model = %q, want glm-5.2", report.TopModel)
	}
	if report.Lifetime.Calls != 3 {
		t.Errorf("lifetime.calls = %d, want 3", report.Lifetime.Calls)
	}
	if report.Lifetime.TokensSaved != 995 {
		t.Errorf("lifetime.tokens_saved = %d, want 995", report.Lifetime.TokensSaved)
	}
	if report.Lifetime.TokensBefore != 142826 {
		t.Errorf("lifetime.tokens_before = %d, want 142826", report.Lifetime.TokensBefore)
	}
	if report.Windows.Today.Calls != 3 {
		t.Errorf("windows.today.calls = %d, want 3", report.Windows.Today.Calls)
	}
	if report.Windows.Last7Days.TokensSaved != 995 {
		t.Errorf("windows.last_7_days.tokens_saved = %d, want 995", report.Windows.Last7Days.TokensSaved)
	}
	if report.Windows.AllTime.CostUSD <= 0 {
		t.Errorf("windows.all_time.cost_usd = %v, want > 0", report.Windows.AllTime.CostUSD)
	}
	if len(report.ByModel) != 1 || report.ByModel[0].Model != "glm-5.2" || report.ByModel[0].Calls != 3 {
		t.Errorf("by_model mismatch: %+v", report.ByModel)
	}
	if len(report.ByClient) != 1 || report.ByClient[0].Client != "claude-code" || report.ByClient[0].TokensSaved != 995 {
		t.Errorf("by_client mismatch: %+v", report.ByClient)
	}
}

// TestSavingsReport_Roundtrip 验证序列化→反序列化的值等价性，并确认内嵌
// SavingsBucket 的字段在 by_model/by_client 元素中被正确平铺（与 schema 一致）。
func TestSavingsReport_Roundtrip(t *testing.T) {
	var original SavingsReport
	if err := json.Unmarshal([]byte(sampleSavingsJSON), &original); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var again SavingsReport
	if err := json.Unmarshal(data, &again); err != nil {
		t.Fatalf("re-unmarshal failed: %v", err)
	}
	if again.Lifetime.TokensSaved != original.Lifetime.TokensSaved {
		t.Errorf("roundtrip tokens_saved drift: %d vs %d", again.Lifetime.TokensSaved, original.Lifetime.TokensSaved)
	}
	// 内嵌桶字段必须平铺在元素同一层，而非嵌套成 "SavingsBucket": {...}。
	raw := string(data)
	if !strings.Contains(raw, "\"model\":\"glm-5.2\"") {
		t.Errorf("by_model entry did not serialize with flat model field: %s", raw)
	}
	if strings.Contains(raw, "\"SavingsBucket\"") {
		t.Errorf("embedded SavingsBucket leaked as nested object: %s", raw)
	}
	if !strings.Contains(raw, "\"client\":\"claude-code\"") {
		t.Errorf("by_client entry did not serialize with flat client field: %s", raw)
	}
}

// TestGetSavings_LiveIntegration 端到端验证：真正调用 GetSavings 走
// ProcessRunner → `headroom savings --json` → SavingsReport 全链路。
// 当前环境无 headroom 时 Skip（不 fail），保证 CI 可移植。
func TestGetSavings_LiveIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live integration test in short mode")
	}
	s := NewHeadroomService(platform.NewProcessRunner(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), SavingsTimeout)
	defer cancel()
	report, err := s.GetSavings(ctx)
	if err != nil {
		t.Skipf("headroom not available in this environment, skipping live parse: %v", err)
	}
	if report.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", report.SchemaVersion)
	}
	if report.Lifetime.Calls <= 0 {
		t.Errorf("lifetime.calls = %d, want > 0", report.Lifetime.Calls)
	}
	if report.Lifetime.TokensSaved <= 0 {
		t.Errorf("lifetime.tokens_saved = %d, want > 0", report.Lifetime.TokensSaved)
	}
	t.Logf("live savings OK: top_model=%s lifetime.calls=%d tokens_saved=%d tokens_before=%d today.calls=%d by_model=%d by_client=%d",
		report.TopModel, report.Lifetime.Calls, report.Lifetime.TokensSaved,
		report.Lifetime.TokensBefore, report.Windows.Today.Calls, len(report.ByModel), len(report.ByClient))
}
