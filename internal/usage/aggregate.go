package usage

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// querySummary 计算 SummaryFilter 下的多维聚合（设计 11.1）。
func (s *Service) querySummary(ctx context.Context, filter SummaryFilter) (Summary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var (
		where strings.Builder
		args  []any
	)
	where.WriteString("WHERE 1=1")
	filterWhere(&where, &args, filter, "")

	q := fmt.Sprintf(`SELECT
		COUNT(*),
		COALESCE(SUM(billable_input_tokens + output_tokens + cache_read_input_tokens + cache_creation_input_tokens),0),
		COALESCE(SUM(input_tokens),0),
		COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_input_tokens),0),
		COALESCE(SUM(cache_creation_input_tokens),0),
		COALESCE(SUM(billable_input_tokens),0),
		COALESCE(MIN(strftime('%%Y-%%m-%%d', occurred_at / 1000000000, 'unixepoch')),''),
		COALESCE(MAX(strftime('%%Y-%%m-%%d', occurred_at / 1000000000, 'unixepoch')),'')
		FROM usage_records %s`, where.String())

	var sum Summary
	var dateStart, dateEnd string
	err := s.db.QueryRowContext(ctx, q, args...).Scan(
		&sum.TotalRequests,
		&sum.TotalTokens,
		&sum.TotalInputTokens,
		&sum.TotalOutputTokens,
		&sum.TotalCacheRead,
		&sum.TotalCacheCreation,
		&sum.TotalBillableInput,
		&dateStart,
		&dateEnd,
	)
	if err != nil {
		return sum, fmt.Errorf("query summary: %w", err)
	}
	sum.DateRange.Start = dateStart
	sum.DateRange.End = dateEnd

	// 按币种分组求和（用于 TotalCostByCurrency）
	q2 := fmt.Sprintf(`SELECT currency_code, COALESCE(SUM(total_cost),0)
		FROM usage_records %s
		GROUP BY currency_code`, where.String())
	rows, err := s.db.QueryContext(ctx, q2, args...)
	if err != nil {
		return sum, fmt.Errorf("query cost by currency: %w", err)
	}
	defer rows.Close()

	byCurrency := map[string]int64{}
	for rows.Next() {
		var cur string
		var cost int64
		if err := rows.Scan(&cur, &cost); err != nil {
			return sum, err
		}
		byCurrency[cur] += cost
	}
	sum.TotalCostByCurrency = byCurrency
	sum.TotalCostUSD = convertToUSD(byCurrency, s.pricing.CNYToUSDRate())
	return sum, nil
}

// convertToUSD 把按币种分组的成本折算为 USD micro（CNY 用固定汇率）。
func convertToUSD(byCurrency map[string]int64, cnyToUSDRate float64) int64 {
	var total int64
	for cur, v := range byCurrency {
		switch cur {
		case "USD", "":
			total += v
		case "CNY":
			// micro-CNY × rate = micro-USD（rate 是 CNY→USD 标量）
			total += int64(float64(v) * cnyToUSDRate)
		default:
			// 未知币种保守按 USD 处理（不丢数据）
			total += v
		}
	}
	return total
}

// queryDailyTrends 返回日趋势（从 daily_rollup 读，性能优于扫主表）。
//
// 若 daily_rollup 为空（首次未刷新），回退到主表 GROUP BY 计算。
func (s *Service) queryDailyTrends(ctx context.Context, filter TrendFilter) ([]DailyTrendPoint, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	start, end := resolveTrendRange(filter, time.Now().UTC())

	// 先尝试 daily_rollup
	q := `SELECT day,
		SUM(input_tokens), SUM(output_tokens), SUM(request_count),
		currency_code, SUM(total_cost)
		FROM daily_rollup
		WHERE day >= ? AND day <= ?`
	args := []any{start, end}
	if filter.AppType != "" {
		q += " AND app_type = ?"
		args = append(args, filter.AppType)
	}
	if filter.Source != "" {
		// daily_rollup 没有 source 字段，回退到主表
		return s.queryDailyTrendsFromMain(ctx, filter, start, end)
	}
	if filter.Provider != "" {
		q += " AND provider = ?"
		args = append(args, filter.Provider)
	}
	q += " GROUP BY day ORDER BY day ASC"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query daily rollup: %w", err)
	}
	defer rows.Close()

	// 同一天可能有多个 currency，需要合并
	byDay := map[string]*DailyTrendPoint{}
	for rows.Next() {
		var day, cur string
		var in, out, req, cost int64
		if err := rows.Scan(&day, &in, &out, &req, &cur, &cost); err != nil {
			return nil, err
		}
		p, ok := byDay[day]
		if !ok {
			p = &DailyTrendPoint{Day: day, CostByCurrency: map[string]int64{}}
			byDay[day] = p
		}
		p.InputTokens += in
		p.OutputTokens += out
		p.Requests += req
		p.CostByCurrency[cur] += cost
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out2 := make([]DailyTrendPoint, 0, len(byDay))
	// 填充连续日期（即使无数据也要有零点，便于前端画图）
	cnyRate := s.pricing.CNYToUSDRate()
	for d := start; d <= end; d = nextDay(d) {
		p, ok := byDay[d]
		if !ok {
			p = &DailyTrendPoint{Day: d, CostByCurrency: map[string]int64{}}
		}
		if p.CostByCurrency == nil {
			p.CostByCurrency = map[string]int64{}
		}
		p.TotalCostUSD = convertToUSD(p.CostByCurrency, cnyRate)
		out2 = append(out2, *p)
	}
	return out2, nil
}

// queryDailyTrendsFromMain 从主表 GROUP BY 计算（daily_rollup 不可用时的回退）。
func (s *Service) queryDailyTrendsFromMain(ctx context.Context, filter TrendFilter, start, end string) ([]DailyTrendPoint, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var where strings.Builder
	var args []any
	where.WriteString("WHERE 1=1")
	where.WriteString(" AND strftime('%Y-%m-%d', occurred_at / 1000000000, 'unixepoch') >= ?")
	args = append(args, start)
	where.WriteString(" AND strftime('%Y-%m-%d', occurred_at / 1000000000, 'unixepoch') <= ?")
	args = append(args, end)
	if filter.AppType != "" {
		where.WriteString(" AND app_type = ?")
		args = append(args, filter.AppType)
	}
	if filter.Source != "" {
		where.WriteString(" AND source = ?")
		args = append(args, filter.Source)
	}
	if filter.Provider != "" {
		where.WriteString(" AND provider = ?")
		args = append(args, filter.Provider)
	}

	q := fmt.Sprintf(`SELECT
		strftime('%%Y-%%m-%%d', occurred_at / 1000000000, 'unixepoch') AS day,
		SUM(input_tokens), SUM(output_tokens), COUNT(*),
		currency_code, SUM(total_cost)
		FROM usage_records %s
		GROUP BY day, currency_code
		ORDER BY day ASC`, where.String())

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query trends from main: %w", err)
	}
	defer rows.Close()

	byDay := map[string]*DailyTrendPoint{}
	for rows.Next() {
		var day, cur string
		var in, out, req, cost int64
		if err := rows.Scan(&day, &in, &out, &req, &cur, &cost); err != nil {
			return nil, err
		}
		p, ok := byDay[day]
		if !ok {
			p = &DailyTrendPoint{Day: day, CostByCurrency: map[string]int64{}}
			byDay[day] = p
		}
		p.InputTokens += in
		p.OutputTokens += out
		p.Requests += req
		p.CostByCurrency[cur] += cost
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	cnyRate := s.pricing.CNYToUSDRate()
	out2 := make([]DailyTrendPoint, 0, len(byDay))
	for d := start; d <= end; d = nextDay(d) {
		p, ok := byDay[d]
		if !ok {
			p = &DailyTrendPoint{Day: d, CostByCurrency: map[string]int64{}}
		}
		if p.CostByCurrency == nil {
			p.CostByCurrency = map[string]int64{}
		}
		p.TotalCostUSD = convertToUSD(p.CostByCurrency, cnyRate)
		out2 = append(out2, *p)
	}
	return out2, nil
}

// resolveTrendRange 解析 TrendFilter 到 [start, end] UTC 日期字符串。
//
// 优先级：StartDate+EndDate > Days > 默认最近 30 天。
func resolveTrendRange(filter TrendFilter, now time.Time) (string, string) {
	if filter.StartDate != "" && filter.EndDate != "" {
		if filter.StartDate > filter.EndDate {
			return filter.EndDate, filter.StartDate
		}
		return filter.StartDate, filter.EndDate
	}
	days := filter.Days
	if days <= 0 {
		days = 30
	}
	if filter.StartDate != "" {
		return filter.StartDate, now.UTC().Format("2006-01-02")
	}
	if filter.EndDate != "" {
		if endDate, err := time.Parse("2006-01-02", filter.EndDate); err == nil {
			return endDate.AddDate(0, 0, -days+1).Format("2006-01-02"), filter.EndDate
		}
		return now.UTC().AddDate(0, 0, -days+1).Format("2006-01-02"), filter.EndDate
	}
	end := now.UTC().Format("2006-01-02")
	start := now.UTC().AddDate(0, 0, -days+1).Format("2006-01-02")
	return start, end
}

// nextDay 把 "YYYY-MM-DD" 加一天；非法输入返回原值+1（不会无限循环）。
func nextDay(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day + "z" // 让循环终止
	}
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

// queryModelStats 模型维度聚合（设计 11.3）。
func (s *Service) queryModelStats(ctx context.Context, filter StatFilter) ([]ModelStat, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var where strings.Builder
	var args []any
	where.WriteString("WHERE 1=1")
	filterWhere(&where, &args, filter.SummaryFilter, "")

	q := fmt.Sprintf(`SELECT
		normalized_model,
		COALESCE(MAX(provider),''),
		COALESCE(MAX(currency_code),'USD'),
		COALESCE(MAX(app_type),''),
		COUNT(*),
		COALESCE(SUM(input_tokens),0),
		COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_input_tokens),0),
		COALESCE(SUM(cache_creation_input_tokens),0),
		COALESCE(SUM(billable_input_tokens),0),
		COALESCE(SUM(input_cost),0),
		COALESCE(SUM(output_cost),0),
		COALESCE(SUM(cache_read_cost),0),
		COALESCE(SUM(cache_creation_cost),0),
		COALESCE(SUM(total_cost),0)
		FROM usage_records %s
		GROUP BY normalized_model, provider, currency_code, app_type
		ORDER BY SUM(total_cost) DESC, normalized_model ASC`, where.String())

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query model stats: %w", err)
	}
	defer rows.Close()

	var out []ModelStat
	for rows.Next() {
		var m ModelStat
		if err := rows.Scan(
			&m.NormalizedModel, &m.Provider, &m.CurrencyCode, &m.AppType,
			&m.Requests, &m.InputTokens, &m.OutputTokens,
			&m.CacheRead, &m.CacheCreation, &m.BillableInput,
			&m.InputCost, &m.OutputCost, &m.CacheReadCost, &m.CacheCreationCost,
			&m.TotalCost,
		); err != nil {
			return nil, err
		}
		// 显示名 + 是否有价格
		if mp, ok := s.pricing.Resolve(m.NormalizedModel); ok {
			m.DisplayName = mp.DisplayName
			m.HasPrice = true
			applyModelPricingMetrics(&m, mp)
		} else {
			m.DisplayName = m.NormalizedModel
			m.HasPrice = false
			applyModelPricingMetrics(&m, ModelPricing{})
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// queryProviderStats 供应商维度聚合（设计 11.4）。
func (s *Service) queryProviderStats(ctx context.Context, filter StatFilter) ([]ProviderStat, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var where strings.Builder
	var args []any
	where.WriteString("WHERE 1=1")
	filterWhere(&where, &args, filter.SummaryFilter, "")

	q := fmt.Sprintf(`SELECT
		provider,
		COUNT(*),
		COALESCE(SUM(billable_input_tokens+output_tokens+cache_read_input_tokens+cache_creation_input_tokens),0),
		COUNT(DISTINCT normalized_model)
		FROM usage_records %s
		GROUP BY provider
		ORDER BY provider ASC`, where.String())

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query provider stats: %w", err)
	}
	defer rows.Close()

	byProvider := map[string]*ProviderStat{}
	for rows.Next() {
		var provider string
		var req, tokens int64
		var modelCount int
		if err := rows.Scan(&provider, &req, &tokens, &modelCount); err != nil {
			return nil, err
		}
		p, ok := byProvider[provider]
		if !ok {
			p = &ProviderStat{Provider: provider, CostByCurrency: map[string]int64{}}
			byProvider[provider] = p
		}
		p.Requests = req
		p.TotalTokens = tokens
		p.ModelCount = modelCount
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Cost needs a second grouping by currency. Keeping it separate from the
	// provider aggregate makes modelCount a true distinct count even when the
	// same provider has records in multiple native currencies.
	costQ := fmt.Sprintf(`SELECT provider, currency_code, COALESCE(SUM(total_cost),0)
		FROM usage_records %s
		GROUP BY provider, currency_code`, where.String())
	costRows, err := s.db.QueryContext(ctx, costQ, args...)
	if err != nil {
		return nil, fmt.Errorf("query provider costs by currency: %w", err)
	}
	defer costRows.Close()
	for costRows.Next() {
		var provider, currency string
		var cost int64
		if err := costRows.Scan(&provider, &currency, &cost); err != nil {
			return nil, err
		}
		p, ok := byProvider[provider]
		if !ok {
			p = &ProviderStat{Provider: provider, CostByCurrency: map[string]int64{}}
			byProvider[provider] = p
		}
		p.CostByCurrency[currency] += cost
	}
	if err := costRows.Err(); err != nil {
		return nil, err
	}

	cnyRate := s.pricing.CNYToUSDRate()
	out := make([]ProviderStat, 0, len(byProvider))
	for _, p := range byProvider {
		p.TotalCostUSD = convertToUSD(p.CostByCurrency, cnyRate)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalCostUSD == out[j].TotalCostUSD {
			return out[i].Provider < out[j].Provider
		}
		return out[i].TotalCostUSD > out[j].TotalCostUSD
	})
	return out, nil
}

// queryRequestLogs 明细日志（带分页）。
func (s *Service) queryRequestLogs(ctx context.Context, filter LogFilter) ([]UsageRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 500 {
		pageSize = 500
	}
	offset := (page - 1) * pageSize

	var where strings.Builder
	var args []any
	where.WriteString("WHERE 1=1")
	filterWhere(&where, &args, filter.SummaryFilter, filter.Model)

	q := fmt.Sprintf(`SELECT
		id, dedup_key, app_type, source, provider, model, normalized_model,
		session_id, project_dir, preset,
		input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens, billable_input_tokens,
		input_cost, output_cost, cache_read_cost, cache_creation_cost, total_cost, currency_code,
		cost_provided, occurred_at, recorded_at, request_id
		FROM usage_records %s
		ORDER BY occurred_at DESC
		LIMIT ? OFFSET ?`, where.String())
	args = append(args, pageSize, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query request logs: %w", err)
	}
	defer rows.Close()

	var out []UsageRecord
	for rows.Next() {
		var r UsageRecord
		var occurredNano, recordedNano int64
		var source string
		if err := rows.Scan(
			&r.ID, &r.DedupKey, &r.AppType, &source, &r.Provider, &r.Model, &r.NormalizedModel,
			&r.SessionID, &r.ProjectDir, &r.Preset,
			&r.InputTokens, &r.OutputTokens, &r.CacheReadInputTokens, &r.CacheCreationInputTokens, &r.BillableInputTokens,
			&r.InputCost, &r.OutputCost, &r.CacheReadCost, &r.CacheCreationCost, &r.TotalCost, &r.CurrencyCode,
			&r.CostProvided, &occurredNano, &recordedNano, &r.RequestID,
		); err != nil {
			return nil, err
		}
		r.Source = Source(source)
		r.OccurredAt = time.Unix(0, occurredNano).UTC()
		r.RecordedAt = time.Unix(0, recordedNano).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// queryUnknownModels 返回 usage_records 中存在但价格表未匹配的模型（设计 11.8）。
func (s *Service) queryUnknownModels(ctx context.Context) ([]UnknownModel, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	q := `SELECT normalized_model,
		COALESCE(MAX(model),''),
		COUNT(*),
		COALESCE(MAX(strftime('%Y-%m-%dT%H:%M:%SZ', occurred_at / 1000000000, 'unixepoch')),'')
		FROM usage_records
		GROUP BY normalized_model
		ORDER BY COUNT(*) DESC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query unknown models: %w", err)
	}
	defer rows.Close()

	var out []UnknownModel
	for rows.Next() {
		var u UnknownModel
		if err := rows.Scan(&u.NormalizedModel, &u.SampleRaw, &u.Requests, &u.LastSeen); err != nil {
			return nil, err
		}
		// 只保留价格表未匹配的
		if _, ok := s.pricing.Resolve(u.NormalizedModel); ok {
			continue
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

var _ = sql.Open // 显式保留 database/sql 引用（部分平台可能被 lint 警告 unused）
