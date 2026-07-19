package usage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"amagi-codebox/internal/appmeta/opencode"
)

// metadataBackfillRow is the smallest projection needed to repair attribution
// without changing a record's original token or cost values.
type metadataBackfillRow struct {
	id              int64
	appType         string
	source          string
	sessionID       string
	model           string
	normalizedModel string
	provider        string
	currencyCode    string
	totalCost       int64
	occurredAt      int64
}

// backfillUsageMetadata repairs records written by pre-v1.2.83 collectors.
// Claude session logs do not contain a provider field, and newer OpenCode
// versions put model metadata on assistant messages instead of session.model.
// The work is idempotent and only touches incomplete records.
func (s *Service) backfillUsageMetadata(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	days := make([]string, 0)
	updated := 0

	// Resolve OpenCode's NULL session.model first; the generic model-family
	// fallback below can then handle any residual unknown rows safely.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
		if _, statErr := os.Stat(dbPath); statErr == nil {
			n, affectedDays, err := s.backfillOpenCodeMetadata(ctx, dbPath)
			if err != nil {
				// OpenCode can briefly hold an exclusive migration lock. Keep the
				// generic provider/model repair available and retry targeted metadata
				// resolution at the next startup instead of abandoning all backfill.
				s.logWarn("usage", "OpenCode 历史元数据回填延后", err.Error())
			} else {
				updated += n
				days = append(days, affectedDays...)
			}
		}
	}

	n, affectedDays, err := s.backfillGenericMetadata(ctx)
	if err != nil {
		return updated, err
	}
	updated += n
	days = append(days, affectedDays...)

	if len(days) > 0 {
		if err := refreshDailyRollup(ctx, s.db, days); err != nil {
			return updated, fmt.Errorf("refresh usage rollup after metadata backfill: %w", err)
		}
	}
	return updated, nil
}

// backfillOpenCodeMetadata repairs only the records that need metadata from
// OpenCode's source database. It is kept separate to make the source lookup
// directly testable without a real user home directory.
func (s *Service) backfillOpenCodeMetadata(ctx context.Context, sourceDBPath string) (int, []string, error) {
	rows, err := s.incompleteMetadataRows(ctx, `
		app_type = ? AND source = ?
		AND (TRIM(model) = '' OR TRIM(provider) = '' OR LOWER(TRIM(provider)) = 'unknown')`, appOpenCode, string(SourceSessionLog))
	if err != nil {
		return 0, nil, err
	}
	if len(rows) == 0 {
		return 0, nil, nil
	}

	ids := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.sessionID == "" {
			continue
		}
		if _, ok := seen[row.sessionID]; ok {
			continue
		}
		seen[row.sessionID] = struct{}{}
		ids = append(ids, row.sessionID)
	}
	metadataBySession, err := opencode.LookupSessionMetadata(sourceDBPath, ids)
	if err != nil {
		return 0, nil, fmt.Errorf("lookup OpenCode metadata: %w", err)
	}

	updates := make([]metadataBackfillRow, 0, len(rows))
	for _, row := range rows {
		before := row
		metadata, ok := metadataBySession[row.sessionID]
		if !ok || metadata.Model == "" || metadata.Model == "unknown" {
			continue
		}
		if strings.TrimSpace(row.model) == "" {
			row.model = metadata.Model
		}
		row.normalizedModel = NormalizeModelID(row.model)
		row.provider = s.resolveProvider(metadata.Provider, row.normalizedModel)
		// OpenCode's session.cost is native to the resolved provider. A legacy
		// row with a NULL model was previously defaulted to USD, so correct it.
		row.currencyCode = currencyForProvider(row.provider)
		if metadataChanged(before, row) {
			updates = append(updates, row)
		}
	}
	return s.applyMetadataUpdates(ctx, updates)
}

// backfillGenericMetadata fills missing provider/model values from the pricing
// table and stable model-family conventions. It intentionally preserves all
// historical cost integers; only labels and a missing currency are repaired.
func (s *Service) backfillGenericMetadata(ctx context.Context) (int, []string, error) {
	rows, err := s.incompleteMetadataRows(ctx, `
		TRIM(model) = '' OR TRIM(normalized_model) = ''
		OR TRIM(provider) = '' OR LOWER(TRIM(provider)) = 'unknown'`)
	if err != nil {
		return 0, nil, err
	}

	updates := make([]metadataBackfillRow, 0, len(rows))
	for _, row := range rows {
		before := row
		if strings.TrimSpace(row.model) == "" {
			row.model = "unknown"
		}
		row.normalizedModel = NormalizeModelID(row.model)
		row.provider = s.resolveProvider(row.provider, row.normalizedModel)
		if row.provider == "unknown" && row.appType == appCodex {
			// A Codex rollout without model metadata is still an OpenAI-family
			// session; this is more useful than a blank dashboard bucket.
			row.provider = "openai"
		}
		if strings.TrimSpace(row.currencyCode) == "" {
			row.currencyCode = currencyForProvider(row.provider)
		} else if row.totalCost == 0 {
			// Unknown models have zero estimated cost. Reflect a known native
			// currency even before the user adds a pricing row for that model.
			if _, hasPrice := s.pricing.Resolve(row.normalizedModel); !hasPrice {
				row.currencyCode = currencyForProvider(row.provider)
			}
		}
		if metadataChanged(before, row) {
			updates = append(updates, row)
		}
	}
	return s.applyMetadataUpdates(ctx, updates)
}

func (s *Service) incompleteMetadataRows(ctx context.Context, where string, args ...any) ([]metadataBackfillRow, error) {
	q := `SELECT id, app_type, source, session_id, model, normalized_model,
		provider, currency_code, total_cost, occurred_at
		FROM usage_records WHERE ` + where
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query incomplete usage metadata: %w", err)
	}
	defer rows.Close()

	var result []metadataBackfillRow
	for rows.Next() {
		var row metadataBackfillRow
		if err := rows.Scan(
			&row.id, &row.appType, &row.source, &row.sessionID, &row.model, &row.normalizedModel,
			&row.provider, &row.currencyCode, &row.totalCost, &row.occurredAt,
		); err != nil {
			return nil, fmt.Errorf("scan incomplete usage metadata: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *Service) applyMetadataUpdates(ctx context.Context, updates []metadataBackfillRow) (int, []string, error) {
	if len(updates) == 0 {
		return 0, nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("begin metadata backfill: %w", err)
	}
	defer tx.Rollback()

	updated := 0
	days := make([]string, 0, len(updates))
	for _, row := range updates {
		res, err := tx.ExecContext(ctx, `UPDATE usage_records
			SET model=?, normalized_model=?, provider=?, currency_code=?
			WHERE id=?`, row.model, row.normalizedModel, row.provider, row.currencyCode, row.id)
		if err != nil {
			return updated, days, fmt.Errorf("update usage metadata id=%d: %w", row.id, err)
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			updated += int(n)
			days = append(days, dayFromUnixNano(row.occurredAt))
		}
	}
	if err := tx.Commit(); err != nil {
		return updated, days, fmt.Errorf("commit metadata backfill: %w", err)
	}
	return updated, days, nil
}

func dayFromUnixNano(nano int64) string {
	return time.Unix(0, nano).UTC().Format("2006-01-02")
}

func metadataChanged(before, after metadataBackfillRow) bool {
	return before.model != after.model ||
		before.normalizedModel != after.normalizedModel ||
		before.provider != after.provider ||
		before.currencyCode != after.currencyCode
}

// correctLegacyDeepSeekV4ProCurrency fixes records written while the OpenCode
// collector assumed every domestic provider billed in CNY. DeepSeek V4 Pro's
// OpenCode session.cost is USD-denominated, including its cache-hit component.
// Scope this correction to the affected model so custom DeepSeek resellers are
// never rewritten implicitly.
func (s *Service) correctLegacyDeepSeekV4ProCurrency(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	const where = `app_type=? AND source=? AND provider=? AND normalized_model=? AND currency_code='CNY'`
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT
		strftime('%Y-%m-%d', occurred_at / 1000000000, 'unixepoch')
		FROM usage_records WHERE `+where,
		appOpenCode, string(SourceSessionLog), "deepseek", "deepseek-v4-pro")
	if err != nil {
		return 0, fmt.Errorf("query legacy DeepSeek V4 Pro days: %w", err)
	}
	days := make([]string, 0)
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan legacy DeepSeek V4 Pro day: %w", err)
		}
		days = append(days, day)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterate legacy DeepSeek V4 Pro days: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close legacy DeepSeek V4 Pro days: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE usage_records SET currency_code='USD' WHERE `+where,
		appOpenCode, string(SourceSessionLog), "deepseek", "deepseek-v4-pro")
	if err != nil {
		return 0, fmt.Errorf("update legacy DeepSeek V4 Pro currency: %w", err)
	}
	updated64, _ := result.RowsAffected()
	updated := int(updated64)
	if updated > 0 && len(days) > 0 {
		if err := refreshDailyRollup(ctx, s.db, days); err != nil {
			return updated, fmt.Errorf("refresh DeepSeek V4 Pro rollup: %w", err)
		}
	}
	return updated, nil
}
