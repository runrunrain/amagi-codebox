package headroom

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// samplePerfJSON 是 `headroom perf --format json --raw` 的实测样例（脱敏后），
// 混合 codex（OpenAI responses 协议，tok_saved=0）与 claude-code（Anthropic
// messages 协议，tok_saved>0）两类客户端，覆盖前端 codex 卡片突出 cache 命中率
// 的真实数据形态：codex 体积压缩为 0、cache 命中率高；claude-code 双收益。
const samplePerfJSON = `[
  {
    "timestamp": "2026-07-23T10:00:00Z",
    "request_id": "req-codex-1",
    "model": "gpt-5.2",
    "client": "codex",
    "num_messages": 12,
    "tokens_before": 4000,
    "tokens_after": 4000,
    "tokens_saved": 0,
    "cache_read": 3500,
    "cache_write": 200,
    "cache_hit_pct": 87.5,
    "transforms": ["openai:responses:custom_tool_call_output:code_aware"],
    "total_ms": 320,
    "tokens_out": 500,
    "ttfb_ms": 80,
    "stages": {}
  },
  {
    "timestamp": "2026-07-23T10:01:00Z",
    "request_id": "req-codex-2",
    "model": "gpt-5.2",
    "client": "codex",
    "num_messages": 14,
    "tokens_before": 6000,
    "tokens_after": 6000,
    "tokens_saved": 0,
    "cache_read": 5400,
    "cache_write": 300,
    "cache_hit_pct": 90.0,
    "transforms": ["none"],
    "total_ms": 410,
    "tokens_out": 700,
    "ttfb_ms": 95,
    "stages": {}
  },
  {
    "timestamp": "2026-07-23T10:02:00Z",
    "request_id": "req-claude-1",
    "model": "claude-sonnet-4",
    "client": "claude-code",
    "num_messages": 20,
    "tokens_before": 8000,
    "tokens_after": 7000,
    "tokens_saved": 1000,
    "cache_read": 7800,
    "cache_write": 100,
    "cache_hit_pct": 99.0,
    "transforms": ["anthropic:tool_schema_compaction", "excluded:tool"],
    "total_ms": 250,
    "tokens_out": 600,
    "ttfb_ms": 70,
    "stages": {"compaction": {"ms": 30}}
  }
]`

// floatEq 比较两个 float64 在 epsilon 内是否相等，避免浮点聚合误差误报。
func floatEq(a, b float64) bool {
	const eps = 1e-9
	if a-b > eps || b-a > eps {
		return false
	}
	return true
}

// TestPerfRecord_Unmarshal 验证 PerfRecord 能正确反序列化实测样例，
// 关键字段（client、tokens_saved、cache_read、cache_hit_pct、transforms、stages）
// 取值与原始 JSON 一致；stages 作为 json.RawMessage 透传不被强建模。
func TestPerfRecord_Unmarshal(t *testing.T) {
	var records []PerfRecord
	if err := jsonUnmarshalPerf(samplePerfJSON, &records); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("len(records) = %d, want 3", len(records))
	}

	// codex 第一条：体积压缩为 0、cache 命中率 87.5、有 code_aware 转换器。
	c0 := records[0]
	if c0.Client != "codex" {
		t.Errorf("records[0].client = %q, want codex", c0.Client)
	}
	if c0.TokensSaved != 0 {
		t.Errorf("records[0].tokens_saved = %d, want 0 (codex 无体积压缩)", c0.TokensSaved)
	}
	if !floatEq(c0.CacheHitPct, 87.5) {
		t.Errorf("records[0].cache_hit_pct = %v, want 87.5", c0.CacheHitPct)
	}
	if len(c0.Transforms) != 1 || c0.Transforms[0] != "openai:responses:custom_tool_call_output:code_aware" {
		t.Errorf("records[0].transforms mismatch: %v", c0.Transforms)
	}
	if strings.TrimSpace(string(c0.Stages)) != "{}" {
		t.Errorf("records[0].stages = %s, want `{}`", string(c0.Stages))
	}

	// claude-code 第三条：体积压缩 >0、cache 命中率 99、有 compaction+excluded 转换器、
	// stages 为非空对象（透传保留原始 JSON）。
	c2 := records[2]
	if c2.Client != "claude-code" {
		t.Errorf("records[2].client = %q, want claude-code", c2.Client)
	}
	if c2.TokensSaved != 1000 {
		t.Errorf("records[2].tokens_saved = %d, want 1000", c2.TokensSaved)
	}
	if !floatEq(c2.CacheHitPct, 99.0) {
		t.Errorf("records[2].cache_hit_pct = %v, want 99.0", c2.CacheHitPct)
	}
	if len(c2.Transforms) != 2 {
		t.Errorf("records[2].transforms len = %d, want 2", len(c2.Transforms))
	}
	if strings.TrimSpace(string(c2.Stages)) == "{}" {
		t.Errorf("records[2].stages should be a non-empty object, got `{}`")
	}
}

// TestAggregatePerfByClient_MixedClients 验证按 client 聚合的关键指标：
//   - requests 计数（codex=2、claude-code=1）
//   - avg_cache_hit_pct 算术均值（codex=(87.5+90)/2=88.75、claude-code=99.0）
//   - tokens_saved 求和（codex=0、claude-code=1000）
//   - cache_read_tokens 求和（codex=8900、claude-code=7800）
//   - tokens_before 求和（codex=10000、claude-code=8000）
//   - savings_percent（codex=0、claude-code=1000/8000*100=12.5）
//   - 排序：Requests 降序 → codex 在 claude-code 之前
func TestAggregatePerfByClient_MixedClients(t *testing.T) {
	var records []PerfRecord
	if err := jsonUnmarshalPerf(samplePerfJSON, &records); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	stats := aggregatePerfByClient(records)
	if len(stats) != 2 {
		t.Fatalf("len(stats) = %d, want 2", len(stats))
	}

	// 排序断言：codex (requests=2) 必须排在 claude-code (requests=1) 之前。
	if stats[0].Client != "codex" {
		t.Fatalf("stats[0].client = %q, want codex (requests 降序)", stats[0].Client)
	}
	if stats[1].Client != "claude-code" {
		t.Fatalf("stats[1].client = %q, want claude-code", stats[1].Client)
	}

	codex := stats[0]
	if codex.Requests != 2 {
		t.Errorf("codex.requests = %d, want 2", codex.Requests)
	}
	if !floatEq(codex.AvgCacheHitPct, 88.75) {
		t.Errorf("codex.avg_cache_hit_pct = %v, want 88.75", codex.AvgCacheHitPct)
	}
	if codex.TokensSaved != 0 {
		t.Errorf("codex.tokens_saved = %d, want 0", codex.TokensSaved)
	}
	if codex.CacheReadTokens != 8900 {
		t.Errorf("codex.cache_read_tokens = %d, want 8900", codex.CacheReadTokens)
	}
	if codex.TokensBefore != 10000 {
		t.Errorf("codex.tokens_before = %d, want 10000", codex.TokensBefore)
	}
	if !floatEq(codex.SavingsPercent, 0.0) {
		t.Errorf("codex.savings_percent = %v, want 0", codex.SavingsPercent)
	}

	claude := stats[1]
	if claude.Requests != 1 {
		t.Errorf("claude-code.requests = %d, want 1", claude.Requests)
	}
	if !floatEq(claude.AvgCacheHitPct, 99.0) {
		t.Errorf("claude-code.avg_cache_hit_pct = %v, want 99.0", claude.AvgCacheHitPct)
	}
	if claude.TokensSaved != 1000 {
		t.Errorf("claude-code.tokens_saved = %d, want 1000", claude.TokensSaved)
	}
	if claude.CacheReadTokens != 7800 {
		t.Errorf("claude-code.cache_read_tokens = %d, want 7800", claude.CacheReadTokens)
	}
	if claude.TokensBefore != 8000 {
		t.Errorf("claude-code.tokens_before = %d, want 8000", claude.TokensBefore)
	}
	if !floatEq(claude.SavingsPercent, 12.5) {
		t.Errorf("claude-code.savings_percent = %v, want 12.5", claude.SavingsPercent)
	}
}

// TestAggregatePerfByClient_EmptyInput 验证空输入返回非 nil 空 slice：
// 前端 JSON 序列化应得到 `[]` 而非 `null`，便于空态判断。
func TestAggregatePerfByClient_EmptyInput(t *testing.T) {
	stats := aggregatePerfByClient(nil)
	if stats == nil {
		t.Fatal("aggregatePerfByClient(nil) = nil, want non-nil empty slice")
	}
	if len(stats) != 0 {
		t.Fatalf("len(stats) = %d, want 0", len(stats))
	}

	stats = aggregatePerfByClient([]PerfRecord{})
	if stats == nil {
		t.Fatal("aggregatePerfByClient([]) = nil, want non-nil empty slice")
	}
	if len(stats) != 0 {
		t.Fatalf("len(stats) = %d, want 0", len(stats))
	}
}

// TestAggregatePerfByClient_MissingFields 验证缺字段容错：record 仅有 client 与
// 部分字段时，缺失数值字段按零值参与聚合，不报错、不静默丢弃。
// 这覆盖实际 ledger 中可能出现的历史 record 字段不全的情况。
func TestAggregatePerfByClient_MissingFields(t *testing.T) {
	const partial = `[
	  {"client": "codex", "cache_read": 100},
	  {"client": "codex", "cache_hit_pct": 50.0, "tokens_before": 200, "tokens_saved": 20}
	]`
	var records []PerfRecord
	if err := jsonUnmarshalPerf(partial, &records); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	stats := aggregatePerfByClient(records)
	if len(stats) != 1 {
		t.Fatalf("len(stats) = %d, want 1", len(stats))
	}
	got := stats[0]
	if got.Client != "codex" {
		t.Errorf("client = %q, want codex", got.Client)
	}
	if got.Requests != 2 {
		t.Errorf("requests = %d, want 2", got.Requests)
	}
	// avg_cache_hit_pct = (0 + 50.0) / 2 = 25.0（第一条缺失按 0）。
	if !floatEq(got.AvgCacheHitPct, 25.0) {
		t.Errorf("avg_cache_hit_pct = %v, want 25.0", got.AvgCacheHitPct)
	}
	// cache_read_tokens = 100 + 0 = 100（第二条缺失按 0）。
	if got.CacheReadTokens != 100 {
		t.Errorf("cache_read_tokens = %d, want 100", got.CacheReadTokens)
	}
	// tokens_before = 0 + 200 = 200。
	if got.TokensBefore != 200 {
		t.Errorf("tokens_before = %d, want 200", got.TokensBefore)
	}
	// tokens_saved = 0 + 20 = 20。
	if got.TokensSaved != 20 {
		t.Errorf("tokens_saved = %d, want 20", got.TokensSaved)
	}
	// savings_percent = 20 / 200 * 100 = 10.0。
	if !floatEq(got.SavingsPercent, 10.0) {
		t.Errorf("savings_percent = %v, want 10.0", got.SavingsPercent)
	}
}

// TestAggregatePerfByClient_ZeroTokensBeforeNoDivByZero 验证 tokens_before 合计
// 为 0 时 savings_percent 退化为 0 而非 NaN/Inf，避免前端展示异常。
func TestAggregatePerfByClient_ZeroTokensBeforeNoDivByZero(t *testing.T) {
	const zeroBefore = `[
	  {"client": "codex", "tokens_before": 0, "tokens_saved": 0, "cache_hit_pct": 99.0}
	]`
	var records []PerfRecord
	if err := jsonUnmarshalPerf(zeroBefore, &records); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	stats := aggregatePerfByClient(records)
	if len(stats) != 1 {
		t.Fatalf("len(stats) = %d, want 1", len(stats))
	}
	if !floatEq(stats[0].SavingsPercent, 0.0) {
		t.Errorf("savings_percent = %v, want 0 (no divide-by-zero)", stats[0].SavingsPercent)
	}
	if !floatEq(stats[0].AvgCacheHitPct, 99.0) {
		t.Errorf("avg_cache_hit_pct = %v, want 99.0", stats[0].AvgCacheHitPct)
	}
}

// TestAggregatePerfByClient_TieBreakOrder 验证 Requests 相同时按 Client 升序
// 排序，保证聚合输出确定性。
func TestAggregatePerfByClient_TieBreakOrder(t *testing.T) {
	const tie = `[
	  {"client": "zzz", "cache_hit_pct": 10.0},
	  {"client": "aaa", "cache_hit_pct": 20.0},
	  {"client": "mmm", "cache_hit_pct": 30.0}
	]`
	var records []PerfRecord
	if err := jsonUnmarshalPerf(tie, &records); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	stats := aggregatePerfByClient(records)
	if len(stats) != 3 {
		t.Fatalf("len(stats) = %d, want 3", len(stats))
	}
	wantOrder := []string{"aaa", "mmm", "zzz"}
	for i, w := range wantOrder {
		if stats[i].Client != w {
			t.Errorf("stats[%d].client = %q, want %q (Client 升序 tie-break)", i, stats[i].Client, w)
		}
	}
}

// cannedRunner 是一个最小 platform.ProcessRunner，其 Run 返回固定 stdout 与
// 可选 error，用于在不依赖真实 headroom 二进制的前提下驱动 GetPerfByClient
// 的端到端解析路径（runner.Run → JSON 解析 → 聚合）。Start 未使用。
type cannedRunner struct {
	stdout string
	stderr string
	runErr error
}

func (c *cannedRunner) Start(spec platform.CommandSpec) (*exec.Cmd, error) {
	return nil, errors.New("Start not implemented for canned runner")
}

func (c *cannedRunner) Run(ctx context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	return &platform.ProcessResult{Stdout: c.stdout, Stderr: c.stderr}, c.runErr
}

// TestGetPerfByClient_ParsesRawArray 端到端验证 GetPerfByClient：cannedRunner
// 返回 --raw 数组样例 → 走完整解析与聚合 → 断言 codex 排在首位且 cache 命中率均值正确。
func TestGetPerfByClient_ParsesRawArray(t *testing.T) {
	runner := &cannedRunner{stdout: samplePerfJSON}
	svc := NewHeadroomService(runner, nil)

	stats, err := svc.GetPerfByClient(context.Background())
	if err != nil {
		t.Fatalf("GetPerfByClient: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("len(stats) = %d, want 2", len(stats))
	}
	if stats[0].Client != "codex" {
		t.Errorf("stats[0].client = %q, want codex", stats[0].Client)
	}
	if !floatEq(stats[0].AvgCacheHitPct, 88.75) {
		t.Errorf("stats[0].avg_cache_hit_pct = %v, want 88.75", stats[0].AvgCacheHitPct)
	}
	if stats[1].Client != "claude-code" {
		t.Errorf("stats[1].client = %q, want claude-code", stats[1].Client)
	}
}

// TestGetPerfByClient_EmptyArrayReturnsEmpty 端到端验证空 JSON 数组返回非 nil
// 空 slice（不报错），前端可据此渲染空态。
func TestGetPerfByClient_EmptyArrayReturnsEmpty(t *testing.T) {
	runner := &cannedRunner{stdout: "[]"}
	svc := NewHeadroomService(runner, nil)

	stats, err := svc.GetPerfByClient(context.Background())
	if err != nil {
		t.Fatalf("GetPerfByClient on `[]`: %v", err)
	}
	if stats == nil {
		t.Fatal("stats = nil, want non-nil empty slice")
	}
	if len(stats) != 0 {
		t.Fatalf("len(stats) = %d, want 0", len(stats))
	}
}

// TestGetPerfByClient_IllegalJSONReturnsError 验证非法 JSON 返回明确 error，
// 不返回伪造数据冒充"有数据"。
func TestGetPerfByClient_IllegalJSONReturnsError(t *testing.T) {
	runner := &cannedRunner{stdout: "{not valid json"}
	svc := NewHeadroomService(runner, nil)

	stats, err := svc.GetPerfByClient(context.Background())
	if err == nil {
		t.Fatal("GetPerfByClient on illegal JSON should error")
	}
	if stats != nil {
		t.Errorf("stats = %v, want nil on parse error", stats)
	}
	if !strings.Contains(err.Error(), "parse headroom perf json") {
		t.Errorf("err = %v, want error containing 'parse headroom perf json'", err)
	}
}

// TestGetPerfByClient_EmptyOutputReturnsError 验证空 stdout 返回明确 error，
// 与 GetSavings 的容错范式一致。
func TestGetPerfByClient_EmptyOutputReturnsError(t *testing.T) {
	runner := &cannedRunner{stdout: "", stderr: "no perf data"}
	svc := NewHeadroomService(runner, nil)

	stats, err := svc.GetPerfByClient(context.Background())
	if err == nil {
		t.Fatal("GetPerfByClient on empty stdout should error")
	}
	if stats != nil {
		t.Errorf("stats = %v, want nil on empty output", stats)
	}
}

// TestGetPerfByClient_RunErrorPropagates 验证 runner.Run 的错误被包裹后传播，
// 且 stderr 摘要进入错误信息便于诊断。
func TestGetPerfByClient_RunErrorPropagates(t *testing.T) {
	runner := &cannedRunner{
		stdout: "",
		stderr: "headroom: perf subcommand requires ledger",
		runErr: errors.New("exit status 1"),
	}
	svc := NewHeadroomService(runner, nil)

	stats, err := svc.GetPerfByClient(context.Background())
	if err == nil {
		t.Fatal("GetPerfByClient on run error should error")
	}
	if stats != nil {
		t.Errorf("stats = %v, want nil on run error", stats)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("err = %v, want error wrapping 'exit status 1'", err)
	}
	if !strings.Contains(err.Error(), "headroom: perf subcommand requires ledger") {
		t.Errorf("err = %v, want error containing stderr summary", err)
	}
}

// TestGetPerfByClient_NilRunnerReturnsError 验证未配置 runner 时返回明确 error，
// 而非 panic。
func TestGetPerfByClient_NilRunnerReturnsError(t *testing.T) {
	svc := NewHeadroomService(nil, nil)
	stats, err := svc.GetPerfByClient(context.Background())
	if err == nil {
		t.Fatal("GetPerfByClient with nil runner should error")
	}
	if stats != nil {
		t.Errorf("stats = %v, want nil on nil runner", stats)
	}
}

// TestGetPerfByClient_LiveIntegration 端到端验证：真正调用 GetPerfByClient 走
// ProcessRunner → `headroom perf --format json --raw` → 解析聚合全链路。
// 当前环境无 headroom 时 Skip（不 fail），保证 CI 可移植。
func TestGetPerfByClient_LiveIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live integration test in short mode")
	}
	s := NewHeadroomService(platform.NewProcessRunner(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), PerfTimeout)
	defer cancel()
	stats, err := s.GetPerfByClient(ctx)
	if err != nil {
		t.Skipf("headroom not available in this environment, skipping live parse: %v", err)
	}
	// 有数据时断言聚合结构合法；无数据（空 ledger）时仅断言非 nil 空 slice。
	if stats == nil {
		t.Fatal("live stats = nil, want non-nil slice")
	}
	for _, st := range stats {
		if strings.TrimSpace(st.Client) == "" {
			t.Errorf("live stat has empty client: %+v", st)
		}
		if st.Requests <= 0 {
			t.Errorf("live stat requests = %d, want > 0", st.Requests)
		}
	}
	t.Logf("live perf OK: clients=%d", len(stats))
}

// jsonUnmarshalPerf 是 encoding/json.Unmarshal 的测试辅助薄封装，统一调用点
// 便于在解析路径上做断言。
func jsonUnmarshalPerf(s string, out *[]PerfRecord) error {
	return json.Unmarshal([]byte(s), out)
}
