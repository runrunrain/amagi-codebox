package usage

import (
	"context"
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
		{"Claude-Sonnet-4", "claude-sonnet-4"},      // 全小写
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
	count, err := recordCount(context.TODO(), s.db)
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
		AppType:      appOpenCode,
		Source:       SourceSessionLog,
		Model:        "glm-5.2",
		InputTokens:  1000,
		OutputTokens: 200,
		OccurredAt:   time.Now().UTC(),
		CostProvided: true,
		NativeCost:   193251, // 0.193251 CNY → micro-CNY
		CurrencyCode: "CNY",
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

	today := time.Now().UTC().Truncate(24 * time.Hour)
	todayKey := today.Format("2006-01-02")

	// 插入 2 条同一天不同模型的记录
	for _, m := range []string{"claude-sonnet-4", "gpt-4o"} {
		_, err := s.Record(UsageEvent{
			AppType:      appClaudeCode,
			Source:       SourceSessionLog,
			Model:        m,
			InputTokens:  100,
			OutputTokens: 50,
			OccurredAt:   today.Add(10 * time.Hour),
			DedupKey:     "test:" + m,
		})
		if err != nil {
			t.Fatalf("Record %s: %v", m, err)
		}
	}

	// 刷新 rollup（传 nil 走全量刷新，等价于旧行为）
	if err := refreshDailyRollup(context.TODO(), s.db, nil); err != nil {
		t.Fatalf("refreshDailyRollup: %v", err)
	}

	// 查询日趋势
	points, err := s.queryDailyTrends(context.TODO(), TrendFilter{Days: 7})
	if err != nil {
		t.Fatalf("queryDailyTrends: %v", err)
	}
	found := false
	for _, p := range points {
		if p.Day == todayKey && p.Requests == 2 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %s with 2 requests in trend; got %v", todayKey, points)
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

	day2 := time.Now().UTC().Truncate(24 * time.Hour)
	day1 := day2.AddDate(0, 0, -1)
	day1Key := day1.Format("2006-01-02")
	day2Key := day2.Format("2006-01-02")

	// 插入跨两天的记录：day1（claude）+ day2（gpt-4o）。
	if _, err := s.Record(UsageEvent{
		AppType: appClaudeCode, Source: SourceSessionLog,
		Model: "claude-sonnet-4", InputTokens: 100, OutputTokens: 50,
		OccurredAt: day1.Add(10 * time.Hour),
		DedupKey:   "p1",
	}); err != nil {
		t.Fatalf("Record day1: %v", err)
	}
	if _, err := s.Record(UsageEvent{
		AppType: appClaudeCode, Source: SourceSessionLog,
		Model: "gpt-4o", InputTokens: 200, OutputTokens: 80,
		OccurredAt: day2.Add(10 * time.Hour),
		DedupKey:   "p2",
	}); err != nil {
		t.Fatalf("Record day2: %v", err)
	}

	// 先全量刷新建立基线。
	if err := refreshDailyRollup(context.TODO(), s.db, nil); err != nil {
		t.Fatalf("baseline refresh: %v", err)
	}

	// 新增一条 day2 记录后，只对 day2 做分区刷新；day1 应保持不变。
	if _, err := s.Record(UsageEvent{
		AppType: appClaudeCode, Source: SourceSessionLog,
		Model: "gpt-4o", InputTokens: 50, OutputTokens: 20,
		OccurredAt: day2.Add(22 * time.Hour),
		DedupKey:   "p3",
	}); err != nil {
		t.Fatalf("Record day2 add: %v", err)
	}
	if err := refreshDailyRollup(context.TODO(), s.db, []string{day2Key}); err != nil {
		t.Fatalf("partition refresh: %v", err)
	}

	// 查询：day2 应聚合成 1 行（同 model+provider+currency），requests=2。
	points, err := s.queryDailyTrends(context.TODO(), TrendFilter{Days: 30})
	if err != nil {
		t.Fatalf("queryDailyTrends: %v", err)
	}
	want := map[string]int64{day1Key: 1, day2Key: 2}
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
	if err := refreshDailyRollup(context.TODO(), s.db, nil); err != nil {
		t.Fatalf("equivalence refresh: %v", err)
	}
	points2, err := s.queryDailyTrends(context.TODO(), TrendFilter{Days: 30})
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

// TestDailyTrendsSplitCostByCurrency 验证同一天多币种用量在日趋势中按币种分开汇总。
// 回归：rollup 分支的聚合查询曾只 GROUP BY day 而裸选 currency_code，
// 把 USD/CNY 的 total_cost 混加进单一币种桶，CostByCurrency 与 TotalCostUSD 均失真
// （主表回退分支 queryDailyTrendsFromMain 一直是 GROUP BY day, currency_code 的正确口径）。
func TestDailyTrendsSplitCostByCurrency(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	day := time.Now().UTC().Truncate(24 * time.Hour).Add(10 * time.Hour)
	events := []UsageEvent{
		{
			AppType: appOpenCode, Source: SourceSessionLog,
			Model: "glm-5.2", Provider: "glm",
			InputTokens: 100, OutputTokens: 10,
			CostProvided: true, NativeCost: 1000, CurrencyCode: "USD",
			OccurredAt: day, DedupKey: "trend-usd",
		},
		{
			AppType: appOpenCode, Source: SourceSessionLog,
			Model: "glm-5.2", Provider: "glm",
			InputTokens: 100, OutputTokens: 10,
			CostProvided: true, NativeCost: 2000, CurrencyCode: "CNY",
			OccurredAt: day, DedupKey: "trend-cny",
		},
	}
	for _, evt := range events {
		if _, err := s.Record(evt); err != nil {
			t.Fatalf("Record %s: %v", evt.DedupKey, err)
		}
	}
	if err := refreshDailyRollup(context.TODO(), s.db, nil); err != nil {
		t.Fatalf("refreshDailyRollup: %v", err)
	}

	points, err := s.queryDailyTrends(context.TODO(), TrendFilter{Days: 7})
	if err != nil {
		t.Fatalf("queryDailyTrends: %v", err)
	}
	dayKey := day.Format("2006-01-02")
	var point *DailyTrendPoint
	for i := range points {
		if points[i].Day == dayKey {
			point = &points[i]
			break
		}
	}
	if point == nil {
		t.Fatalf("day %s not found in trends: %v", dayKey, points)
	}
	if got := point.CostByCurrency["USD"]; got != 1000 {
		t.Errorf("CostByCurrency[USD] = %d, want 1000 (full map: %v)", got, point.CostByCurrency)
	}
	if got := point.CostByCurrency["CNY"]; got != 2000 {
		t.Errorf("CostByCurrency[CNY] = %d, want 2000 (full map: %v)", got, point.CostByCurrency)
	}
	// 默认固定汇率 0.14：1000 + 2000*0.14 = 1280。
	if point.TotalCostUSD != 1280 {
		t.Errorf("TotalCostUSD = %d, want 1280", point.TotalCostUSD)
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
	count, _ := recordCount(context.TODO(), s.db)
	if count != 1 {
		t.Errorf("count = %d, want 1 (REPLACE does not add rows)", count)
	}
}

// TestPricingSeedCNY 验证国产模型币种是 CNY。
func TestPricingSeedCNY(t *testing.T) {
	data := defaultPricingData()
	cnyCount, usdCount := 0, 0
	glmFound, glm53Found, glm51Found := false, false, false
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
		if m.ModelPattern == "glm-5.3" {
			glm53Found = true
			if m.CurrencyCode != "CNY" {
				t.Errorf("glm-5.3 currency = %s, want CNY", m.CurrencyCode)
			}
		}
		if m.ModelPattern == "glm-5.1" {
			glm51Found = true
			if m.CurrencyCode != "CNY" {
				t.Errorf("glm-5.1 currency = %s, want CNY", m.CurrencyCode)
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
	if !glm53Found {
		t.Error("expected glm-5.3 in seed (temporary GLM-5.2 rates, editable in pricing UI)")
	}
	if !glm51Found {
		t.Error("expected glm-5.1 in seed (was priced via glm-5 prefix fallback)")
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

// TestUpsertModelPricingNormalizesPattern 验证 Upsert 把用户输入的
// ModelPattern 归一化为匹配键（Resolve 只对小写标准化键做精确/前缀匹配）。
// 回归：前端校验允许大写，旧实现原样存入，导致该价格行永远无法命中。
func TestUpsertModelPricingNormalizesPattern(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	if err := s.UpsertModelPricing(ModelPricing{
		ID:               "user-custom-1",
		ModelPattern:     "GLM-5.9",
		DisplayName:      "Custom GLM",
		Provider:         "glm",
		CurrencyCode:     "CNY",
		InputPerMillion:  2_000_000,
		OutputPerMillion: 8_000_000,
	}); err != nil {
		t.Fatalf("UpsertModelPricing: %v", err)
	}

	// 归一化后 Resolve 必须能命中精确匹配。
	pricing, ok := s.pricing.Resolve("glm-5.9")
	if !ok {
		t.Fatal("Resolve(glm-5.9) miss after upsert with raw-case pattern")
	}
	if pricing.InputPerMillion != 2_000_000 {
		t.Fatalf("matched inputPerMillion = %d, want 2_000_000 (custom row)", pricing.InputPerMillion)
	}
	// 表内存储的也应是归一化后的键。
	found := false
	for _, m := range s.pricing.List() {
		if m.ID == "user-custom-1" {
			found = true
			if m.ModelPattern != "glm-5.9" {
				t.Fatalf("stored ModelPattern = %q, want normalized glm-5.9", m.ModelPattern)
			}
		}
	}
	if !found {
		t.Fatal("custom pricing row missing after upsert")
	}

	// 大写变体应原位更新内置行（旧行为会追加一行永远无法命中的死行）。
	if err := s.UpsertModelPricing(ModelPricing{
		ID:               "ignored-id",
		ModelPattern:     "Claude-Sonnet-4",
		DisplayName:      "Claude Sonnet 4 (edited)",
		Provider:         "anthropic",
		CurrencyCode:     "USD",
		InputPerMillion:  2_500_000,
		OutputPerMillion: 10_000_000,
	}); err != nil {
		t.Fatalf("UpsertModelPricing builtin: %v", err)
	}
	matches := 0
	for _, m := range s.pricing.List() {
		if m.ModelPattern == "claude-sonnet-4" {
			matches++
			if m.InputPerMillion != 2_500_000 {
				t.Fatalf("builtin row not updated in place: %+v", m)
			}
			if !m.IsBuiltin || m.ID != "builtin-claude-sonnet-4" {
				t.Fatalf("builtin identity lost: %+v", m)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("pattern claude-sonnet-4 stored in %d rows, want exactly 1 (no dead duplicate)", matches)
	}
}

// TestReplaceSnapshotNormalizesPatterns 验证快照导入路径同样把
// ModelPattern 归一化，避免导入大写/带 vendor 前缀的表后出现死行。
func TestReplaceSnapshotNormalizesPatterns(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	if err := s.pricing.ReplaceSnapshot(PricingData{
		Version: 1,
		Models: []ModelPricing{{
			ID:              "snap-1",
			ModelPattern:    "GLM-5.2",
			DisplayName:     "GLM 5.2",
			Provider:        "glm",
			CurrencyCode:    "CNY",
			InputPerMillion: 1_111_111,
		}},
	}); err != nil {
		t.Fatalf("ReplaceSnapshot: %v", err)
	}
	pricing, ok := s.pricing.Resolve("glm-5.2")
	if !ok {
		t.Fatal("Resolve(glm-5.2) miss after ReplaceSnapshot with raw-case pattern")
	}
	if pricing.InputPerMillion != 1_111_111 {
		t.Fatalf("inputPerMillion = %d, want 1_111_111", pricing.InputPerMillion)
	}
}

func TestDeepSeekV4ProCachePricingAndMetrics(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	pricing, ok := s.pricing.Resolve("deepseek-v4-pro")
	if !ok {
		t.Fatal("deepseek-v4-pro should be a built-in price")
	}
	if pricing.CurrencyCode != "USD" || pricing.InputPerMillion != 435_000 ||
		pricing.OutputPerMillion != 870_000 || pricing.CacheReadPerMillion != 3_625 {
		t.Fatalf("unexpected DeepSeek V4 Pro pricing: %#v", pricing)
	}

	event := UsageEvent{
		AppType:              appClaudeCode,
		Source:               SourceSessionLog,
		Provider:             "deepseek",
		Model:                "deepseek-v4-pro",
		InputTokens:          1_000_000,
		CacheReadInputTokens: 1_000_000,
		OccurredAt:           time.Now().UTC(),
		DedupKey:             "test:deepseek-v4-pro-cache",
	}
	record := s.eventToRecord(event)
	if record.TotalCost != 438_625 || record.CacheReadCost != 3_625 || record.CurrencyCode != "USD" {
		t.Fatalf("DeepSeek V4 Pro cost = total=%d cache=%d currency=%s, want 438625/3625/USD", record.TotalCost, record.CacheReadCost, record.CurrencyCode)
	}
	if _, err := s.Record(event); err != nil {
		t.Fatalf("Record: %v", err)
	}

	stats, err := s.queryModelStats(context.Background(), StatFilter{})
	if err != nil {
		t.Fatalf("queryModelStats: %v", err)
	}
	for _, stat := range stats {
		if stat.NormalizedModel != "deepseek-v4-pro" {
			continue
		}
		if stat.TotalTokens != 2_000_000 || stat.CacheHitRate != 0.5 {
			t.Fatalf("cache totals = tokens=%d hitRate=%f, want 2000000/0.5", stat.TotalTokens, stat.CacheHitRate)
		}
		if stat.CacheAdjustedTokens != 1_008_333 || stat.CacheReadEstimatedCost != 3_625 || stat.CacheHitSavings != 431_375 {
			t.Fatalf("cache economics = adjusted=%d cost=%d savings=%d", stat.CacheAdjustedTokens, stat.CacheReadEstimatedCost, stat.CacheHitSavings)
		}
		return
	}
	t.Fatal("DeepSeek V4 Pro stats were not returned")
}

func TestRepriceEstimatedUsagePreservesSourceProvidedCosts(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	event := UsageEvent{
		AppType:              appClaudeCode,
		Source:               SourceSessionLog,
		Model:                "deepseek-v4-pro",
		InputTokens:          1_000_000,
		CacheReadInputTokens: 1_000_000,
		OccurredAt:           time.Now().UTC(),
		DedupKey:             "test:reprice-deepseek-v4-pro",
	}
	if _, err := s.Record(event); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE usage_records SET input_cost=0, output_cost=0,
		cache_read_cost=0, cache_creation_cost=0, total_cost=0, currency_code='CNY'
		WHERE dedup_key=?`, event.DedupKey); err != nil {
		t.Fatalf("clear estimated cost: %v", err)
	}
	updated, err := s.repriceEstimatedUsageForPattern(context.Background(), "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("repriceEstimatedUsageForPattern: %v", err)
	}
	if updated != 1 {
		t.Fatalf("repriced records = %d, want 1", updated)
	}
	var total int64
	var currency string
	if err := s.db.QueryRow(`SELECT total_cost, currency_code FROM usage_records WHERE dedup_key=?`, event.DedupKey).Scan(&total, &currency); err != nil {
		t.Fatalf("query repriced record: %v", err)
	}
	if total != 438_625 || currency != "USD" {
		t.Fatalf("repriced record = total=%d currency=%s, want 438625/USD", total, currency)
	}

	provided := UsageEvent{
		AppType: appOpenCode, Source: SourceSessionLog, Model: "deepseek-v4-pro",
		OccurredAt: time.Now().UTC(), DedupKey: "test:provided-deepseek-v4-pro",
		CostProvided: true, NativeCost: 7_253, CurrencyCode: "USD",
	}
	if _, err := s.Record(provided); err != nil {
		t.Fatalf("Record source-provided: %v", err)
	}
	if updated, err := s.repriceEstimatedUsageForPattern(context.Background(), "deepseek-v4-pro"); err != nil || updated != 0 {
		t.Fatalf("source-provided record should not reprice: updated=%d err=%v", updated, err)
	}
}

func TestCorrectLegacyDeepSeekV4ProCurrency(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	occurredAt := time.Now().UTC()
	event := UsageEvent{
		AppType: appOpenCode, Source: SourceSessionLog,
		Provider: "deepseek", Model: "deepseek-v4-pro",
		OccurredAt: occurredAt, DedupKey: "test:legacy-deepseek-v4-pro-currency",
		CostProvided: true, NativeCost: 7_253, CurrencyCode: "CNY",
	}
	if _, err := s.Record(event); err != nil {
		t.Fatalf("Record legacy DeepSeek V4 Pro: %v", err)
	}
	day := occurredAt.Format("2006-01-02")
	if err := refreshDailyRollup(context.Background(), s.db, []string{day}); err != nil {
		t.Fatalf("refresh legacy rollup: %v", err)
	}

	updated, err := s.correctLegacyDeepSeekV4ProCurrency(context.Background())
	if err != nil {
		t.Fatalf("correctLegacyDeepSeekV4ProCurrency: %v", err)
	}
	if updated != 1 {
		t.Fatalf("corrected records = %d, want 1", updated)
	}
	var currency string
	if err := s.db.QueryRow(`SELECT currency_code FROM usage_records WHERE dedup_key=?`, event.DedupKey).Scan(&currency); err != nil {
		t.Fatalf("query corrected record: %v", err)
	}
	if currency != "USD" {
		t.Fatalf("corrected record currency = %s, want USD", currency)
	}
	if err := s.db.QueryRow(`SELECT currency_code FROM daily_rollup
		WHERE day=? AND normalized_model='deepseek-v4-pro'`, day).Scan(&currency); err != nil {
		t.Fatalf("query corrected rollup: %v", err)
	}
	if currency != "USD" {
		t.Fatalf("corrected rollup currency = %s, want USD", currency)
	}
}

func TestModelDailyTrendsRemainSeparate(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	dayOne := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	dayTwo := dayOne.AddDate(0, 0, 1)
	for _, event := range []UsageEvent{
		{AppType: appClaudeCode, Source: SourceSessionLog, Model: "claude-sonnet-4", InputTokens: 100, OccurredAt: dayOne, DedupKey: "test:trend:claude:one"},
		{AppType: appClaudeCode, Source: SourceSessionLog, Model: "claude-sonnet-4", InputTokens: 200, OccurredAt: dayTwo, DedupKey: "test:trend:claude:two"},
		{AppType: appClaudeCode, Source: SourceSessionLog, Model: "deepseek-v4-pro", InputTokens: 300, OccurredAt: dayOne, DedupKey: "test:trend:deepseek:one"},
	} {
		if _, err := s.Record(event); err != nil {
			t.Fatalf("Record trend fixture: %v", err)
		}
	}
	if err := refreshDailyRollup(context.Background(), s.db, []string{dayOne.Format("2006-01-02"), dayTwo.Format("2006-01-02")}); err != nil {
		t.Fatalf("refreshDailyRollup: %v", err)
	}
	points, err := s.queryModelDailyTrends(context.Background(), TrendFilter{
		SummaryFilter: SummaryFilter{StartDate: dayOne.Format("2006-01-02"), EndDate: dayTwo.Format("2006-01-02")},
	})
	if err != nil {
		t.Fatalf("queryModelDailyTrends: %v", err)
	}
	byModel := map[string][]ModelDailyTrendPoint{}
	for _, point := range points {
		byModel[point.NormalizedModel] = append(byModel[point.NormalizedModel], point)
	}
	if len(byModel["claude-sonnet-4"]) != 2 || len(byModel["deepseek-v4-pro"]) != 2 {
		t.Fatalf("expected a complete two-day series per model, got %#v", byModel)
	}
	if byModel["claude-sonnet-4"][0].TotalTokens == byModel["deepseek-v4-pro"][0].TotalTokens {
		t.Fatal("model curves were unexpectedly aggregated together")
	}
}

func TestResolveTrendRangeWithOneSidedDate(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	start, end := resolveTrendRange(TrendFilter{SummaryFilter: SummaryFilter{StartDate: "2026-07-01"}, Days: 7}, now)
	if start != "2026-07-01" || end != "2026-07-20" {
		t.Fatalf("start-only range = %s..%s, want 2026-07-01..2026-07-20", start, end)
	}
	start, end = resolveTrendRange(TrendFilter{SummaryFilter: SummaryFilter{EndDate: "2026-07-10"}, Days: 7}, now)
	if start != "2026-07-04" || end != "2026-07-10" {
		t.Fatalf("end-only range = %s..%s, want 2026-07-04..2026-07-10", start, end)
	}
}

// 防止 filepath 在测试中被报告为 unused（部分测试可能未直接使用）。
var _ = filepath.Join
