package usage

import (
	"context"
	"fmt"
)

type repricedUsageRecord struct {
	id                int64
	appType           string
	normalizedModel   string
	inputTokens       int
	outputTokens      int
	cacheRead         int
	cacheCreation     int
	billableInput     int
	inputCost         int64
	outputCost        int64
	cacheReadCost     int64
	cacheCreationCost int64
	totalCost         int64
	currencyCode      string
	occurredAt        int64
}

// repriceEstimatedUsageForPattern applies the currently configured pricing to
// locally estimated records matching a model pattern. Source-provided OpenCode
// bills are deliberately excluded via cost_provided=0.
func (s *Service) repriceEstimatedUsageForPattern(ctx context.Context, pattern string) (int, error) {
	pattern = NormalizeModelID(pattern)
	if pattern == "" {
		return 0, nil
	}
	return s.repriceEstimatedUsage(ctx, "(normalized_model = ? OR normalized_model LIKE ?)", pattern, pattern+"%")
}

func (s *Service) repriceAllEstimatedUsage(ctx context.Context) (int, error) {
	return s.repriceEstimatedUsage(ctx, "1=1")
}

func (s *Service) repriceEstimatedUsage(ctx context.Context, where string, args ...any) (int, error) {
	if s == nil || s.db == nil || s.pricing == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin repricing transaction: %w", err)
	}
	defer tx.Rollback()

	q := `SELECT id, app_type, normalized_model,
		input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens,
		billable_input_tokens, input_cost, output_cost, cache_read_cost,
		cache_creation_cost, total_cost, currency_code, occurred_at
		FROM usage_records
		WHERE cost_provided=0 AND ` + where
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("query repricing candidates: %w", err)
	}
	var candidates []repricedUsageRecord
	for rows.Next() {
		var record repricedUsageRecord
		if err := rows.Scan(
			&record.id, &record.appType, &record.normalizedModel,
			&record.inputTokens, &record.outputTokens, &record.cacheRead, &record.cacheCreation,
			&record.billableInput, &record.inputCost, &record.outputCost, &record.cacheReadCost,
			&record.cacheCreationCost, &record.totalCost, &record.currencyCode, &record.occurredAt,
		); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan repricing candidate: %w", err)
		}
		candidates = append(candidates, record)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterate repricing candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close repricing candidates: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `UPDATE usage_records
		SET billable_input_tokens=?, input_cost=?, output_cost=?, cache_read_cost=?,
			cache_creation_cost=?, total_cost=?, currency_code=?
		WHERE id=?`)
	if err != nil {
		return 0, fmt.Errorf("prepare repricing update: %w", err)
	}
	defer stmt.Close()

	updated := 0
	days := make([]string, 0)
	for _, record := range candidates {
		pricing, hasPrice := s.pricing.Resolve(record.normalizedModel)
		if !hasPrice {
			continue
		}
		billable := ComputeBillableInput(record.appType, record.inputTokens, record.cacheRead)
		inputCost, outputCost, cacheReadCost, cacheCreationCost, totalCost := ComputeCost(
			pricing, billable, record.outputTokens, record.cacheRead, record.cacheCreation,
		)
		if billable == record.billableInput &&
			inputCost == record.inputCost && outputCost == record.outputCost &&
			cacheReadCost == record.cacheReadCost && cacheCreationCost == record.cacheCreationCost &&
			totalCost == record.totalCost && pricing.CurrencyCode == record.currencyCode {
			continue
		}
		if _, err := stmt.ExecContext(ctx,
			billable, inputCost, outputCost, cacheReadCost, cacheCreationCost, totalCost, pricing.CurrencyCode, record.id,
		); err != nil {
			return updated, fmt.Errorf("update repriced usage id=%d: %w", record.id, err)
		}
		updated++
		days = append(days, dayFromUnixNano(record.occurredAt))
	}
	if err := tx.Commit(); err != nil {
		return updated, fmt.Errorf("commit repricing: %w", err)
	}
	if len(days) > 0 {
		if err := refreshDailyRollup(ctx, s.db, days); err != nil {
			return updated, fmt.Errorf("refresh rollup after repricing: %w", err)
		}
	}
	return updated, nil
}
