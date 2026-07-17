package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，注册 "sqlite" driverName
)

// schemaDDL 是 usage SQLite 数据库的建表与索引 DDL（设计 3.5）。
//
// 三张表：
//   - usage_records：用量主表（dedup_key UNIQUE 保证幂等入库）
//   - sync_state：增量同步游标（每源一行）
//   - daily_rollup：日聚合（按 day+model+provider+currency 分组）
const schemaDDL = `
CREATE TABLE IF NOT EXISTS usage_records (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    dedup_key               TEXT NOT NULL UNIQUE,
    app_type                TEXT NOT NULL,
    source                  TEXT NOT NULL,
    provider                TEXT NOT NULL,
    model                   TEXT NOT NULL,
    normalized_model        TEXT NOT NULL,
    session_id              TEXT NOT NULL DEFAULT '',
    project_dir             TEXT NOT NULL DEFAULT '',
    preset                  TEXT NOT NULL DEFAULT '',
    input_tokens            INTEGER NOT NULL DEFAULT 0,
    output_tokens           INTEGER NOT NULL DEFAULT 0,
    cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
    billable_input_tokens   INTEGER NOT NULL DEFAULT 0,
    input_cost              INTEGER NOT NULL DEFAULT 0,
    output_cost             INTEGER NOT NULL DEFAULT 0,
    cache_read_cost         INTEGER NOT NULL DEFAULT 0,
    cache_creation_cost     INTEGER NOT NULL DEFAULT 0,
    total_cost              INTEGER NOT NULL DEFAULT 0,
    currency_code           TEXT NOT NULL DEFAULT 'USD',
    occurred_at             INTEGER NOT NULL,
    recorded_at             INTEGER NOT NULL,
    request_id              TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_usage_occurred ON usage_records(occurred_at);
CREATE INDEX IF NOT EXISTS idx_usage_app_model ON usage_records(app_type, normalized_model);
CREATE INDEX IF NOT EXISTS idx_usage_provider ON usage_records(provider);
CREATE INDEX IF NOT EXISTS idx_usage_session ON usage_records(session_id);
CREATE INDEX IF NOT EXISTS idx_usage_source ON usage_records(source);

CREATE TABLE IF NOT EXISTS sync_state (
    source_type      TEXT NOT NULL,
    source_key       TEXT NOT NULL,
    app_type         TEXT NOT NULL,
    last_mtime       INTEGER NOT NULL DEFAULT 0,
    last_line_offset INTEGER NOT NULL DEFAULT 0,
    last_time_updated INTEGER NOT NULL DEFAULT 0,
    last_synced_at   INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    records_added    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (source_type, source_key)
);

CREATE TABLE IF NOT EXISTS daily_rollup (
    day              TEXT NOT NULL,
    app_type         TEXT NOT NULL,
    normalized_model TEXT NOT NULL,
    provider         TEXT NOT NULL,
    currency_code    TEXT NOT NULL,
    input_tokens            INTEGER NOT NULL DEFAULT 0,
    output_tokens           INTEGER NOT NULL DEFAULT 0,
    cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
    billable_input_tokens   INTEGER NOT NULL DEFAULT 0,
    input_cost              INTEGER NOT NULL DEFAULT 0,
    output_cost             INTEGER NOT NULL DEFAULT 0,
    cache_read_cost         INTEGER NOT NULL DEFAULT 0,
    cache_creation_cost     INTEGER NOT NULL DEFAULT 0,
    total_cost              INTEGER NOT NULL DEFAULT 0,
    request_count           INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, app_type, normalized_model, provider, currency_code)
);
CREATE INDEX IF NOT EXISTS idx_rollup_day ON daily_rollup(day);
CREATE INDEX IF NOT EXISTS idx_rollup_model ON daily_rollup(normalized_model);
`

// openDB 打开 SQLite 数据库（WAL 模式，单连接串行写）。
//
// modernc.org/sqlite 通过 DSN 的 _pragma= 传递 PRAGMA：
//   - journal_mode=WAL：并发读写安全
//   - synchronous=NORMAL：WAL 下足够安全且更快
//   - busy_timeout=5000：容忍短暂锁竞争
//   - foreign_keys=ON：启用外键约束（本期未用，留作未来 schema 演进）
func openDB(dbPath string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dbPath, err)
	}
	// 单写连接：modernc 在高并发写时通过串行化避免 SQLITE_BUSY。
	db.SetMaxOpenConns(1)
	return db, nil
}

// initSchema 执行建表 DDL（幂等）。
func initSchema(db *sql.DB) error {
	_, err := db.Exec(schemaDDL)
	if err != nil {
		return fmt.Errorf("init schema: %w", err)
	}
	return nil
}

// insertRecord 用 INSERT OR IGNORE 插入一条记录（dedup_key 冲突时跳过）。
// 返回是否实际新增（true=新插入；false=dedup_key 已存在）。
func insertRecord(ctx context.Context, db *sql.DB, r UsageRecord) (bool, error) {
	const q = `INSERT OR IGNORE INTO usage_records
		(dedup_key, app_type, source, provider, model, normalized_model,
		 session_id, project_dir, preset,
		 input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens,
		 billable_input_tokens,
		 input_cost, output_cost, cache_read_cost, cache_creation_cost, total_cost, currency_code,
		 occurred_at, recorded_at, request_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	res, err := db.ExecContext(ctx, q,
		r.DedupKey, r.AppType, string(r.Source), r.Provider, r.Model, r.NormalizedModel,
		r.SessionID, r.ProjectDir, r.Preset,
		r.InputTokens, r.OutputTokens, r.CacheReadInputTokens, r.CacheCreationInputTokens,
		r.BillableInputTokens,
		r.InputCost, r.OutputCost, r.CacheReadCost, r.CacheCreationCost, r.TotalCost, r.CurrencyCode,
		r.OccurredAt.UnixNano(), r.RecordedAt.UnixNano(), r.RequestID,
	)
	if err != nil {
		return false, fmt.Errorf("insert usage record: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// upsertRecord 用 INSERT OR REPLACE 插入或替换一条记录（用于累计语义场景，如 OpenCode 同 session 更新）。
//
// 返回 isNew 表示本次是否为真正新增行（设计 M5）：
//   - true：dedup_key 原本不存在，本次 INSERT 生效。
//   - false：dedup_key 已存在，本次为 REPLACE（更新已有行）。
//
// 实现走"先查再写"：单写连接（SetMaxOpenConns(1)）下查询与写入天然串行，
// 无并发竞态；避免依赖 INSERT OR REPLACE 的 changes()（REPLACE 始终报 1 行，无法区分）。
func upsertRecord(ctx context.Context, db *sql.DB, r UsageRecord) (bool, error) {
	// 1. 先查 dedup_key 是否存在，判断 new vs replace。
	var existing int
	probeErr := db.QueryRowContext(ctx,
		`SELECT 1 FROM usage_records WHERE dedup_key=? LIMIT 1`, r.DedupKey).Scan(&existing)
	if probeErr != nil && !errors.Is(probeErr, sql.ErrNoRows) {
		return false, fmt.Errorf("probe dedup_key: %w", probeErr)
	}
	isNew := errors.Is(probeErr, sql.ErrNoRows)

	// 2. 执行 INSERT OR REPLACE。
	const q = `INSERT OR REPLACE INTO usage_records
		(dedup_key, app_type, source, provider, model, normalized_model,
		 session_id, project_dir, preset,
		 input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens,
		 billable_input_tokens,
		 input_cost, output_cost, cache_read_cost, cache_creation_cost, total_cost, currency_code,
		 occurred_at, recorded_at, request_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	_, err := db.ExecContext(ctx, q,
		r.DedupKey, r.AppType, string(r.Source), r.Provider, r.Model, r.NormalizedModel,
		r.SessionID, r.ProjectDir, r.Preset,
		r.InputTokens, r.OutputTokens, r.CacheReadInputTokens, r.CacheCreationInputTokens,
		r.BillableInputTokens,
		r.InputCost, r.OutputCost, r.CacheReadCost, r.CacheCreationCost, r.TotalCost, r.CurrencyCode,
		r.OccurredAt.UnixNano(), r.RecordedAt.UnixNano(), r.RequestID,
	)
	if err != nil {
		return false, fmt.Errorf("upsert usage record: %w", err)
	}
	return isNew, nil
}

// recordCount 返回 usage_records 总行数（测试与状态展示用）。
func recordCount(ctx context.Context, db *sql.DB) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records`).Scan(&n)
	return n, err
}

// SyncState 读写 ------------------------------------------------------------

// getSyncState 读取指定源的同步游标（不存在返回零值 SyncState）。
func getSyncState(ctx context.Context, db *sql.DB, sourceType, sourceKey string) (SyncState, error) {
	var s SyncState
	var lastSynced int64
	q := `SELECT source_type, source_key, app_type, last_mtime, last_line_offset,
	      last_time_updated, last_synced_at, last_error, records_added
	      FROM sync_state WHERE source_type=? AND source_key=?`
	err := db.QueryRowContext(ctx, q, sourceType, sourceKey).Scan(
		&s.SourceType, &s.SourceKey, &s.AppType, &s.LastMTime, &s.LastLineOffset,
		&s.LastTimeUpdated, &lastSynced, &s.LastError, &s.RecordsAdded,
	)
	if err == nil {
		s.LastSyncedAt = time.Unix(0, lastSynced).UTC()
		return s, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return SyncState{SourceType: sourceType, SourceKey: sourceKey}, nil
	}
	return s, err
}

// upsertSyncState 写入或更新同步游标。
func upsertSyncState(ctx context.Context, db *sql.DB, s SyncState) error {
	const q = `INSERT OR REPLACE INTO sync_state
		(source_type, source_key, app_type, last_mtime, last_line_offset,
		 last_time_updated, last_synced_at, last_error, records_added)
		VALUES (?,?,?,?,?,?,?,?,?)`
	_, err := db.ExecContext(ctx, q,
		s.SourceType, s.SourceKey, s.AppType, s.LastMTime, s.LastLineOffset,
		s.LastTimeUpdated, s.LastSyncedAt.UnixNano(), s.LastError, s.RecordsAdded,
	)
	if err != nil {
		return fmt.Errorf("upsert sync_state: %w", err)
	}
	return nil
}

// listSyncStates 返回全部同步游标（前端调试用）。
func listSyncStates(ctx context.Context, db *sql.DB) ([]SyncState, error) {
	q := `SELECT source_type, source_key, app_type, last_mtime, last_line_offset,
	      last_time_updated, last_synced_at, last_error, records_added
	      FROM sync_state ORDER BY source_type, source_key`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncState
	for rows.Next() {
		var s SyncState
		var lastSynced int64
		if err := rows.Scan(&s.SourceType, &s.SourceKey, &s.AppType, &s.LastMTime,
			&s.LastLineOffset, &s.LastTimeUpdated, &lastSynced, &s.LastError, &s.RecordsAdded); err != nil {
			return nil, err
		}
		s.LastSyncedAt = time.Unix(0, lastSynced).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// daily_rollup 维护 ---------------------------------------------------------

// refreshDailyRollup 刷新 daily_rollup（设计 8.5）。
//
// 分区刷新策略（M3）：
//   - len(days)==0：走全量重算（DELETE ALL + INSERT SELECT），用于测试和兜底。
//   - len(days)>0：只重算受影响日期——DELETE 这些 day + 对这些 day 重新聚合。
//
// 受影响日期集合由 sync 阶段维护（每条 INSERT/REPLACE 把 occurred_at 当日加入集合），
// 见 sync.go SyncAll 的 affectedDays。
//
// occurred_at 是 Unix nano，/1e9 转秒；strftime(..., 'unixepoch') 输出 UTC 日期。
// 第一期固定 UTC，避免跨时区漂移。两步操作放同一事务，保证原子可见。
func refreshDailyRollup(ctx context.Context, db *sql.DB, days []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// 受影响日期去重排序，保证 placeholder 与参数顺序一致。
	days = uniqueSortedDays(days)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if len(days) == 0 {
		// 全量重算（测试 / 兜底）。
		if _, err := tx.ExecContext(ctx, `DELETE FROM daily_rollup`); err != nil {
			return fmt.Errorf("delete rollup (full): %w", err)
		}
		const ins = `INSERT INTO daily_rollup
			(day, app_type, normalized_model, provider, currency_code,
			 input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens,
			 billable_input_tokens,
			 input_cost, output_cost, cache_read_cost, cache_creation_cost, total_cost, request_count)
			SELECT
				strftime('%Y-%m-%d', occurred_at / 1000000000, 'unixepoch') AS day,
				app_type, normalized_model, provider, currency_code,
				SUM(input_tokens), SUM(output_tokens),
				SUM(cache_read_input_tokens), SUM(cache_creation_input_tokens),
				SUM(billable_input_tokens),
				SUM(input_cost), SUM(output_cost),
				SUM(cache_read_cost), SUM(cache_creation_cost),
				SUM(total_cost), COUNT(*)
			FROM usage_records
			GROUP BY day, app_type, normalized_model, provider, currency_code`
		if _, err := tx.ExecContext(ctx, ins); err != nil {
			return fmt.Errorf("insert rollup (full): %w", err)
		}
		return tx.Commit()
	}

	// 分区刷新：只动受影响日期。
	// placeholder 数量与 days 严格对齐；days 已是非空去重排序列表。
	placeholders := make([]string, len(days))
	args := make([]any, len(days))
	for i, d := range days {
		placeholders[i] = "?"
		args[i] = d
	}
	inClause := strings.Join(placeholders, ",")

	delQ := `DELETE FROM daily_rollup WHERE day IN (` + inClause + `)`
	if _, err := tx.ExecContext(ctx, delQ, args...); err != nil {
		return fmt.Errorf("delete rollup (partition): %w", err)
	}

	insQ := `INSERT INTO daily_rollup
		(day, app_type, normalized_model, provider, currency_code,
		 input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens,
		 billable_input_tokens,
		 input_cost, output_cost, cache_read_cost, cache_creation_cost, total_cost, request_count)
		SELECT
			strftime('%Y-%m-%d', occurred_at / 1000000000, 'unixepoch') AS day,
			app_type, normalized_model, provider, currency_code,
			SUM(input_tokens), SUM(output_tokens),
			SUM(cache_read_input_tokens), SUM(cache_creation_input_tokens),
			SUM(billable_input_tokens),
			SUM(input_cost), SUM(output_cost),
			SUM(cache_read_cost), SUM(cache_creation_cost),
			SUM(total_cost), COUNT(*)
		FROM usage_records
		WHERE strftime('%Y-%m-%d', occurred_at / 1000000000, 'unixepoch') IN (` + inClause + `)
		GROUP BY day, app_type, normalized_model, provider, currency_code`
	// del 与 ins 复用同一组 days 参数。
	insArgs := make([]any, len(days))
	for i, d := range days {
		insArgs[i] = d
	}
	if _, err := tx.ExecContext(ctx, insQ, insArgs...); err != nil {
		return fmt.Errorf("insert rollup (partition): %w", err)
	}
	return tx.Commit()
}

// uniqueSortedDays 对日期字符串去重并按字典序排序，保证 SQL placeholder 顺序稳定。
func uniqueSortedDays(days []string) []string {
	if len(days) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(days))
	out := make([]string, 0, len(days))
	for _, d := range days {
		if d == "" {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	// 插入排序（日期集合通常很小，避免引入 sort 包）。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// filterWhere 构造 WHERE 子句与参数（聚合/明细查询共用）。
//
// 支持字段（设计 11.1 SummaryFilter）：
//   - startDate / endDate（YYYY-MM-DD，UTC，闭区间；按 occurred_at 转 day 比较）
//   - appType / source / provider（空表示不限）
//   - model（normalized_model 精确匹配）
func filterWhere(where *strings.Builder, args *[]any, f SummaryFilter, model string) {
	if f.StartDate != "" {
		where.WriteString(" AND strftime('%Y-%m-%d', occurred_at / 1000000000, 'unixepoch') >= ?")
		*args = append(*args, f.StartDate)
	}
	if f.EndDate != "" {
		where.WriteString(" AND strftime('%Y-%m-%d', occurred_at / 1000000000, 'unixepoch') <= ?")
		*args = append(*args, f.EndDate)
	}
	if f.AppType != "" {
		where.WriteString(" AND app_type = ?")
		*args = append(*args, f.AppType)
	}
	if f.Source != "" {
		where.WriteString(" AND source = ?")
		*args = append(*args, f.Source)
	}
	if f.Provider != "" {
		where.WriteString(" AND provider = ?")
		*args = append(*args, f.Provider)
	}
	if model != "" {
		where.WriteString(" AND normalized_model = ?")
		*args = append(*args, model)
	}
}
