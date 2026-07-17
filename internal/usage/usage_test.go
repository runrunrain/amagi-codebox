package usage

import (
	"path/filepath"
	"testing"
	"time"
)

// TestNormalizeModelID 覆盖设计 6.4 全部示例。
func TestNormalizeModelID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"anthropic/claude-sonnet-4-20250514", "claude-sonnet-4-20250514"},
		{"claude-sonnet-4:latest", "claude-sonnet-4"},
		{"claude-3-5-sonnet:20241022", "claude-3-5-sonnet:20241022"}, // 保留日期
		{"gpt-4@2024-08-06", "gpt-4-2024-08-06"},
		{"openai/gpt-4o", "gpt-4o"},
		{"glm-4.6", "glm-4.6"},
		{"deepseek-chat", "deepseek-chat"},
		{"moonshot-v1-128k", "moonshot-v1-128k"},
		{"Claude-Sonnet-4", "claude-sonnet-4"},   // 全小写
		{"claude-sonnet-4:free", "claude-sonnet-4"}, // 字母标签去除
		{"GLM-5-Turbo", "glm-5-turbo"},
		{"", ""},
		{"gpt-5.6-sol", "gpt-5.6-sol"}, // codex 真实模型名
	}
	for _, c := range cases {
		got := NormalizeModelID(c.in)
		if got != c.want {
			t.Errorf("NormalizeModelID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestComputeBillableInput 覆盖 cache 语义分叉（设计 6.2）。
func TestComputeBillableInput(t *testing.T) {
	// claudecode：input_tokens 不含 cache_read，不扣减
	if got := ComputeBillableInput(appClaudeCode, 1000, 200); got != 1000 {
		t.Errorf("claudecode billable = %d, want 1000 (no subtraction)", got)
	}
	// codex：input_tokens 含 cache_read，必须 saturating_sub 扣减
	if got := ComputeBillableInput(appCodex, 1000, 200); got != 800 {
		t.Errorf("codex billable = %d, want 800", got)
	}
	// codex：cache_read > input 时饱和为 0（不计负数）
	if got := ComputeBillableInput(appCodex, 100, 200); got != 0 {
		t.Errorf("codex billable saturating = %d, want 0", got)
	}
	// opencode：不参与（直接用 session.cost），返回原值
	if got := ComputeBillableInput(appOpenCode, 1000, 200); got != 1000 {
		t.Errorf("opencode billable = %d, want 1000", got)
	}
}

// TestComputeCost 验证四维成本公式（设计 6.1）。
func TestComputeCost(t *testing.T) {
	// Claude Sonnet 4：input 3.00 USD/M, output 15.00, cache_read 0.30, cache_creation 3.75
	mp := ModelPricing{
		InputPerMillion:         3_000_000,
		OutputPerMillion:        15_000_000,
		CacheReadPerMillion:     300_000,
		CacheCreationPerMillion: 3_750_000,
	}
	// 1000 input × 3.00 / 1M = 0.003 USD = 3000 micro-USD
	// 500 output × 15.00 / 1M = 0.0075 USD = 7500 micro-USD
	// 100 cache_read × 0.30 / 1M = 0.00003 USD = 30 micro-USD
	// 50 cache_creation × 3.75 / 1M = 0.0001875 USD = 187 micro-USD（int 取整）
	in, out, cr, cc, total := ComputeCost(mp, 1000, 500, 100, 50)
	if in != 3000 {
		t.Errorf("input cost = %d, want 3000", in)
	}
	if out != 7500 {
		t.Errorf("output cost = %d, want 7500", out)
	}
	if cr != 30 {
		t.Errorf("cache_read cost = %d, want 30", cr)
	}
	if cc != 187 {
		t.Errorf("cache_creation cost = %d, want 187", cc)
	}
	wantTotal := in + out + cr + cc
	if total != wantTotal {
		t.Errorf("total = %d, want %d", total, wantTotal)
	}
}

// TestPricingResolveExactAndPrefix 验证精确匹配 + 前缀匹配（设计 6.6）。
func TestPricingResolveExactAndPrefix(t *testing.T) {
	p := NewPricingService(t.TempDir())
	if err := p.Load(); err != nil {
		t.Fatalf("pricing load: %v", err)
	}

	// 精确匹配（builtin seed 含 claude-sonnet-4）
	mp, ok := p.Resolve("claude-sonnet-4")
	if !ok {
		t.Errorf("expected exact match for claude-sonnet-4")
	} else if mp.DisplayName == "" {
		t.Errorf("expected non-empty DisplayName")
	}

	// 前缀匹配：表里有 "claude-sonnet-4"，原始模型 "claude-sonnet-4-20991231"（seed 里没的日期戳）
	// 应通过前缀匹配命中 claude-sonnet-4
	mp2, ok2 := p.Resolve("claude-sonnet-4-20991231")
	if !ok2 {
		t.Errorf("expected prefix match for claude-sonnet-4-20991231")
	} else if mp2.ModelPattern != "claude-sonnet-4" {
		t.Errorf("prefix match returned %q, want claude-sonnet-4", mp2.ModelPattern)
	}

	// 失配兜底
	_, ok3 := p.Resolve("totally-unknown-model-xyz")
	if ok3 {
		t.Errorf("expected unknown to be unresolved")
	}
}

// TestDedupInsertIdempotent 验证 SQLite UNIQUE + INSERT OR IGNORE 保证幂等。
// 设计 16.1 #5：同步两次 count 不变。
func TestDedupInsertIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	evt := UsageEvent{
		AppType:                  appClaudeCode,
		Source:                   SourceSessionLog,
		Model:                    "claude-sonnet-4",
		SessionID:                "test-session",
		InputTokens:              1000,
		OutputTokens:             500,
		CacheReadInputTokens:     100,
		CacheCreationInputTokens: 50,
		OccurredAt:               time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
		DedupKey:                 "cc:msg_test-idempotent",
	}

	// 第一次：新增
	isNew, err := s.Record(evt)
	if err != nil {
		t.Fatalf("first Record: %v", err)
	}
	if !isNew {
		t.Errorf("first Record should be new")
	}

	// 第二次：dedup_key 冲突，跳过
	isNew2, err := s.Record(evt)
	if err != nil {
		t.Fatalf("second Record: %v", err)
	}
	if isNew2 {
		t.Errorf("second Record should be skipped (dedup)")
	}

	// 验证 count == 1
	count, err := recordCount(nil, s.db)
	if err != nil {
		t.Fatalf("recordCount: %v", err)
	}
	if count != 1 {
		t.Errorf("after two Records, count = %d, want 1", count)
	}
}

// TestEventToRecordCacheSemantics 验证 codex 路径的 BillableInput 扣减与 claudecode 不扣减。
func TestEventToRecordCacheSemantics(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	// codex：input=1000, cache_read=200 → billable=800
	codexEvt := UsageEvent{
		AppType:              appCodex,
		Source:               SourceSessionLog,
		Model:                "gpt-4o",
		InputTokens:          1000,
		OutputTokens:         300,
		CacheReadInputTokens: 200,
		OccurredAt:           time.Now().UTC(),
	}
	rec := s.eventToRecord(codexEvt)
	if rec.BillableInputTokens != 800 {
		t.Errorf("codex billable = %d, want 800", rec.BillableInputTokens)
	}

	// claudecode：input=1000, cache_read=200 → billable=1000
	ccEvt := codexEvt
	ccEvt.AppType = appClaudeCode
	ccEvt.Model = "claude-sonnet-4"
	rec2 := s.eventToRecord(ccEvt)
	if rec2.BillableInputTokens != 1000 {
		t.Errorf("claudecode billable = %d, want 1000", rec2.BillableInputTokens)
	}
}

// TestEventToRecordOpenCodeCostDirect 验证 OpenCode 路径直接用 NativeCost（不走价格表）。
func TestEventToRecordOpenCodeCostDirect(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	evt := UsageEvent{
		AppType:       appOpenCode,
		Source:        SourceSessionLog,
		Model:         "glm-5.2",
		InputTokens:   1000,
		OutputTokens:  200,
		OccurredAt:    time.Now().UTC(),
		CostProvided:  true,
		NativeCost:    193251, // 0.193251 CNY → micro-CNY
		CurrencyCode:  "CNY",
	}
	rec := s.eventToRecord(evt)
	if rec.TotalCost != 193251 {
		t.Errorf("opencode total cost = %d, want 193251 (use native)", rec.TotalCost)
	}
	if rec.CurrencyCode != "CNY" {
		t.Errorf("opencode currency = %q, want CNY", rec.CurrencyCode)
	}
	if rec.InputCost != 0 || rec.OutputCost != 0 {
		t.Errorf("opencode should not split cost into four dimensions")
	}
}

// TestGenerateDedupKey 验证按 AppType + Source 生成 dedup_key。
func TestGenerateDedupKey(t *testing.T) {
	// opencode: "oc:" + session.id
	oc := generateDedupKey(UsageEvent{AppType: appOpenCode, SessionID: "ses_abc"})
	if oc != "oc:ses_abc" {
		t.Errorf("opencode dedup = %q, want oc:ses_abc", oc)
	}
	// proxy: "px:" + SessionID + ":" + RequestID
	px := generateDedupKey(UsageEvent{
		AppType:   appClaudeCode,
		Source:    SourceProxy,
		SessionID: "sess1",
		RequestID: "req1",
	})
	if px != "px:sess1:req1" {
		t.Errorf("proxy dedup = %q, want px:sess1:req1", px)
	}
	// codex: "cx:" + 16hex
	cx := generateDedupKey(UsageEvent{
		AppType:              appCodex,
		Model:                "gpt-4o",
		InputTokens:          100,
		CacheReadInputTokens: 20,
		OccurredAt:           time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	})
	if len(cx) != len("cx:0123456789abcdef") {
		t.Errorf("codex dedup len = %d, want %d", len(cx), len("cx:0123456789abcdef"))
	}
}

// TestDailyRollupRefresh 验证 daily_rollup 全量刷新后能查到。
func TestDailyRollupRefresh(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	// 插入 2 条同一天不同模型的记录
	for _, m := range []string{"claude-sonnet-4", "gpt-4o"} {
		_, err := s.Record(UsageEvent{
			AppType:      appClaudeCode,
			Source:       SourceSessionLog,
			Model:        m,
			InputTokens:  100,
			OutputTokens: 50,
			OccurredAt:   time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
			DedupKey:     "test:" + m,
		})
		if err != nil {
			t.Fatalf("Record %s: %v", m, err)
		}
	}

	// 刷新 rollup（传 nil 走全量刷新，等价于旧行为）
	if err := refreshDailyRollup(nil, s.db, nil); err != nil {
		t.Fatalf("refreshDailyRollup: %v", err)
	}

	// 查询日趋势
	points, err := s.queryDailyTrends(nil, TrendFilter{Days: 7})
	if err != nil {
		t.Fatalf("queryDailyTrends: %v", err)
	}
	found := false
	for _, p := range points {
		if p.Day == "2026-07-17" && p.Requests == 2 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 2026-07-17 with 2 requests in trend; got %v", points)
	}
}

// TestDailyRollupPartitionRefresh 验证 M3 分区刷新：
//   - 只重算指定日期，其它日期保持不变。
//   - 结果与全量刷新一致（数据视角等价）。
func TestDailyRollupPartitionRefresh(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	// 插入跨两天的记录：day1（claude）+ day2（gpt-4o）。
	if _, err := s.Record(UsageEvent{
		AppType: appClaudeCode, Source: SourceSessionLog,
		Model: "claude-sonnet-4", InputTokens: 100, OutputTokens: 50,
		OccurredAt: time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC),
		DedupKey:   "p1",
	}); err != nil {
		t.Fatalf("Record day1: %v", err)
	}
	if _, err := s.Record(UsageEvent{
		AppType: appClaudeCode, Source: SourceSessionLog,
		Model: "gpt-4o", InputTokens: 200, OutputTokens: 80,
		OccurredAt: time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
		DedupKey:   "p2",
	}); err != nil {
		t.Fatalf("Record day2: %v", err)
	}

	// 先全量刷新建立基线。
	if err := refreshDailyRollup(nil, s.db, nil); err != nil {
		t.Fatalf("baseline refresh: %v", err)
	}

	// 新增一条 day2 记录后，只对 day2 做分区刷新；day1 应保持不变。
	if _, err := s.Record(UsageEvent{
		AppType: appClaudeCode, Source: SourceSessionLog,
		Model: "gpt-4o", InputTokens: 50, OutputTokens: 20,
		OccurredAt: time.Date(2026, 7, 17, 22, 0, 0, 0, time.UTC),
		DedupKey:   "p3",
	}); err != nil {
		t.Fatalf("Record day2 add: %v", err)
	}
	if err := refreshDailyRollup(nil, s.db, []string{"2026-07-17"}); err != nil {
		t.Fatalf("partition refresh: %v", err)
	}

	// 查询：day2 应聚合成 1 行（同 model+provider+currency），requests=2。
	points, err := s.queryDailyTrends(nil, TrendFilter{Days: 30})
	if err != nil {
		t.Fatalf("queryDailyTrends: %v", err)
	}
	want := map[string]int64{"2026-07-16": 1, "2026-07-17": 2}
	got := map[string]int64{}
	for _, p := range points {
		if _, ok := want[p.Day]; ok {
			got[p.Day] = p.Requests
		}
	}
	for day, n := range want {
		if got[day] != n {
			t.Errorf("day %s requests = %d, want %d (partition refresh missed)", day, got[day], n)
		}
	}

	// 等价性：再做一次全量刷新，结果应与分区刷新完全一致。
	if err := refreshDailyRollup(nil, s.db, nil); err != nil {
		t.Fatalf("equivalence refresh: %v", err)
	}
	points2, err := s.queryDailyTrends(nil, TrendFilter{Days: 30})
	if err != nil {
		t.Fatalf("queryDailyTrends after full: %v", err)
	}
	got2 := map[string]int64{}
	for _, p := range points2 {
		if _, ok := want[p.Day]; ok {
			got2[p.Day] = p.Requests
		}
	}
	for day, n := range want {
		if got2[day] != n {
			t.Errorf("after full refresh day %s = %d, want %d", day, got2[day], n)
		}
	}
}

// TestRecordForceIsNewSemantic 验证 M5：RecordForce 内部能区分真正新增 vs REPLACE 更新。
func TestRecordForceIsNewSemantic(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	evt := UsageEvent{
		AppType:      appOpenCode,
		Source:       SourceSessionLog,
		Model:        "glm-5.2",
		SessionID:    "ses-force-1",
		InputTokens:  100,
		OutputTokens: 20,
		OccurredAt:   time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
		DedupKey:     "oc:ses-force-1",
		CostProvided: true, NativeCost: 1000, CurrencyCode: "CNY",
	}

	// 第一次：dedup_key 不存在 → 真正新增。
	isNew1, err := s.recordForceInternal(evt)
	if err != nil {
		t.Fatalf("first recordForceInternal: %v", err)
	}
	if !isNew1 {
		t.Errorf("first RecordForce isNew = false, want true (new row)")
	}

	// 第二次：dedup_key 存在 → REPLACE 更新，isNew=false。
	evt.InputTokens = 999 // 改点数据触发 REPLACE
	isNew2, err := s.recordForceInternal(evt)
	if err != nil {
		t.Fatalf("second recordForceInternal: %v", err)
	}
	if isNew2 {
		t.Errorf("second RecordForce isNew = true, want false (REPLACE existing)")
	}

	// 公共 API RecordForce 签名未变（error 单返回值）。
	if err := s.RecordForce(evt); err != nil {
		t.Errorf("public RecordForce returned err: %v", err)
	}

	// 行数仍为 1。
	count, _ := recordCount(nil, s.db)
	if count != 1 {
		t.Errorf("count = %d, want 1 (REPLACE does not add rows)", count)
	}
}

// TestPricingSeedCNY 验证国产模型币种是 CNY。
func TestPricingSeedCNY(t *testing.T) {
	data := defaultPricingData()
	cnyCount, usdCount := 0, 0
	glmFound := false
	for _, m := range data.Models {
		switch m.CurrencyCode {
		case "CNY":
			cnyCount++
		case "USD":
			usdCount++
		}
		if m.ModelPattern == "glm-5.2" {
			glmFound = true
			if m.CurrencyCode != "CNY" {
				t.Errorf("glm-5.2 currency = %s, want CNY", m.CurrencyCode)
			}
		}
	}
	if cnyCount == 0 {
		t.Error("expected some CNY models in seed")
	}
	if usdCount == 0 {
		t.Error("expected some USD models in seed")
	}
	if !glmFound {
		t.Error("expected glm-5.2 in seed (user machine has this model)")
	}
}

// TestPricingSeedOpenAIGPT56 验证 M2：5 个 OpenAI 新模型已补入 seed，
// 且 ModelPattern 与 NormalizeModelID 输出一致。
func TestPricingSeedOpenAIGPT56(t *testing.T) {
	data := defaultPricingData()
	want := map[string]int64{
		// ModelPattern → InputPerMillion（用于断言价格落位 + NormalizeModelID 自洽）。
		"gpt-5.6-sol":   5_000_000,
		"gpt-5.6-terra": 2_500_000,
		"gpt-5.6-luna":  1_000_000,
		"gpt-5.5":       5_000_000,
		"gpt-5.3-codex": 1_750_000,
	}
	for _, m := range data.Models {
		wantIn, ok := want[m.ModelPattern]
		if !ok {
			continue
		}
		if m.CurrencyCode != "USD" {
			t.Errorf("%s currency = %s, want USD", m.ModelPattern, m.CurrencyCode)
		}
		if m.Provider != "openai" {
			t.Errorf("%s provider = %s, want openai", m.ModelPattern, m.Provider)
		}
		if !m.IsBuiltin {
			t.Errorf("%s isBuiltin = false, want true", m.ModelPattern)
		}
		if m.InputPerMillion != wantIn {
			t.Errorf("%s inputPerMillion = %d, want %d", m.ModelPattern, m.InputPerMillion, wantIn)
		}
		if m.OutputPerMillion == 0 {
			t.Errorf("%s outputPerMillion = 0, want non-zero", m.ModelPattern)
		}
		// NormalizeModelID 必须与 ModelPattern 完全一致（价格表精确匹配的前提）。
		if got := NormalizeModelID(m.ModelPattern); got != m.ModelPattern {
			t.Errorf("NormalizeModelID(%q) = %q, want %q", m.ModelPattern, got, m.ModelPattern)
		}
		delete(want, m.ModelPattern)
	}
	if len(want) > 0 {
		t.Errorf("missing OpenAI GPT-5.x models in seed: %v", want)
	}
}

// 防止 filepath 在测试中被报告为 unused（部分测试可能未直接使用）。
var _ = filepath.Join
