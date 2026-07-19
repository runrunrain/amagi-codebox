package usage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// queryModelDailyTrends returns one independent time series per model/provider
// tuple. Unlike queryDailyTrends, it deliberately never sums models together.
func (s *Service) queryModelDailyTrends(ctx context.Context, filter TrendFilter) ([]ModelDailyTrendPoint, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	start, end := resolveTrendRange(filter, time.Now().UTC())
	if filter.Source != "" {
		return s.queryModelDailyTrendsFromMain(ctx, filter, start, end)
	}

	var q strings.Builder
	q.WriteString(`SELECT
		day, normalized_model, provider, currency_code,
		SUM(input_tokens), SUM(output_tokens),
		SUM(cache_read_input_tokens), SUM(cache_creation_input_tokens),
		SUM(billable_input_tokens), SUM(total_cost)
		FROM daily_rollup
		WHERE day >= ? AND day <= ?`)
	args := []any{start, end}
	if filter.AppType != "" {
		q.WriteString(" AND app_type = ?")
		args = append(args, filter.AppType)
	}
	if filter.Provider != "" {
		q.WriteString(" AND provider = ?")
		args = append(args, filter.Provider)
	}
	q.WriteString(` GROUP BY day, normalized_model, provider, currency_code
		ORDER BY normalized_model ASC, provider ASC, currency_code ASC, day ASC`)
	return s.scanAndFillModelTrends(ctx, q.String(), args, start, end)
}

func (s *Service) queryModelDailyTrendsFromMain(ctx context.Context, filter TrendFilter, start, end string) ([]ModelDailyTrendPoint, error) {
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
		normalized_model, provider, currency_code,
		SUM(input_tokens), SUM(output_tokens),
		SUM(cache_read_input_tokens), SUM(cache_creation_input_tokens),
		SUM(billable_input_tokens), SUM(total_cost)
		FROM usage_records %s
		GROUP BY day, normalized_model, provider, currency_code
		ORDER BY normalized_model ASC, provider ASC, currency_code ASC, day ASC`, where.String())
	return s.scanAndFillModelTrends(ctx, q, args, start, end)
}

func (s *Service) scanAndFillModelTrends(ctx context.Context, query string, args []any, start, end string) ([]ModelDailyTrendPoint, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query model daily trends: %w", err)
	}
	defer rows.Close()

	type seriesDescriptor struct {
		normalizedModel string
		provider        string
		currency        string
		displayName     string
	}
	bySeries := make(map[string]map[string]ModelDailyTrendPoint)
	descriptors := make(map[string]seriesDescriptor)
	for rows.Next() {
		var point ModelDailyTrendPoint
		if err := rows.Scan(
			&point.Day, &point.NormalizedModel, &point.Provider, &point.CurrencyCode,
			&point.InputTokens, &point.OutputTokens, &point.CacheRead, &point.CacheCreation,
			&point.BillableInput, &point.TotalCost,
		); err != nil {
			return nil, err
		}
		finalizeModelTrendPoint(s, &point)
		key := modelTrendSeriesKey(point.NormalizedModel, point.Provider, point.CurrencyCode)
		if bySeries[key] == nil {
			bySeries[key] = make(map[string]ModelDailyTrendPoint)
			descriptors[key] = seriesDescriptor{
				normalizedModel: point.NormalizedModel,
				provider:        point.Provider,
				currency:        point.CurrencyCode,
				displayName:     point.DisplayName,
			}
		}
		bySeries[key][point.Day] = point
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(bySeries))
	for key := range bySeries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]ModelDailyTrendPoint, 0, len(keys)*31)
	for _, key := range keys {
		descriptor := descriptors[key]
		for day := start; day <= end; day = nextDay(day) {
			point, ok := bySeries[key][day]
			if !ok {
				point = ModelDailyTrendPoint{
					Day:             day,
					NormalizedModel: descriptor.normalizedModel,
					DisplayName:     descriptor.displayName,
					Provider:        descriptor.provider,
					CurrencyCode:    descriptor.currency,
				}
			}
			result = append(result, point)
		}
	}
	return result, nil
}

func finalizeModelTrendPoint(s *Service, point *ModelDailyTrendPoint) {
	if point == nil {
		return
	}
	stat := ModelStat{
		NormalizedModel: point.NormalizedModel,
		InputTokens:     point.InputTokens,
		OutputTokens:    point.OutputTokens,
		CacheRead:       point.CacheRead,
		CacheCreation:   point.CacheCreation,
		BillableInput:   point.BillableInput,
	}
	if pricing, ok := s.pricing.Resolve(point.NormalizedModel); ok {
		point.DisplayName = pricing.DisplayName
		applyModelPricingMetrics(&stat, pricing)
	} else {
		point.DisplayName = point.NormalizedModel
		applyModelPricingMetrics(&stat, ModelPricing{})
	}
	point.TotalTokens = stat.TotalTokens
	point.CacheAdjustedTokens = stat.CacheAdjustedTokens
	point.TotalCostUSD = convertToUSD(map[string]int64{point.CurrencyCode: point.TotalCost}, s.pricing.CNYToUSDRate())
}

func modelTrendSeriesKey(model, provider, currency string) string {
	return model + "\x00" + provider + "\x00" + currency
}
