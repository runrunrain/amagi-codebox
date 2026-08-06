package usage

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFilterWhereUsesIndexableUnixNanoDateBounds(t *testing.T) {
	var where strings.Builder
	where.WriteString("WHERE 1=1")
	args := []any{}
	filterWhere(&where, &args, SummaryFilter{
		StartDate: "2026-07-08",
		EndDate:   "2026-08-06",
		Source:    string(SourceSessionLog),
	}, "")

	query := where.String()
	if strings.Contains(query, "strftime") {
		t.Fatalf("valid date bounds must not wrap occurred_at in strftime: %s", query)
	}
	if !strings.Contains(query, "occurred_at >= ?") || !strings.Contains(query, "occurred_at < ?") {
		t.Fatalf("missing half-open timestamp range: %s", query)
	}
	if len(args) != 3 {
		t.Fatalf("args=%v, want start/end/source", args)
	}
	start := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC).UnixNano()
	endExclusive := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC).UnixNano()
	if args[0] != start || args[1] != endExclusive || args[2] != string(SourceSessionLog) {
		t.Fatalf("args=%v, want [%d %d %q]", args, start, endExclusive, SourceSessionLog)
	}
}

func TestFilterWhereKeepsLegacySemanticsForInvalidDates(t *testing.T) {
	var where strings.Builder
	where.WriteString("WHERE 1=1")
	args := []any{}
	filterWhere(&where, &args, SummaryFilter{StartDate: "not-a-date"}, "")
	if !strings.Contains(where.String(), "strftime") {
		t.Fatalf("invalid date should retain the legacy string comparison: %s", where.String())
	}
}

func TestDashboardReadPoolDoesNotWaitForWriterConnection(t *testing.T) {
	s := NewService(t.TempDir(), nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	if s.readDB == nil {
		t.Fatal("read-only dashboard pool was not initialized")
	}
	if got := s.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("writer max connections=%d, want 1", got)
	}
	if got := s.readDB.Stats().MaxOpenConnections; got != 4 {
		t.Fatalf("reader max connections=%d, want 4", got)
	}

	_, err := s.Record(UsageEvent{
		AppType:     appCodex,
		Source:      SourceSessionLog,
		Provider:    "openai",
		Model:       "gpt-5.6",
		SessionID:   "read-pool-regression",
		InputTokens: 42,
		OccurredAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	// A transaction reserves the service's only writer connection. Before the
	// independent read pool, the dashboard query waited here until its context
	// expired without ever reaching SQLite.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("begin writer transaction: %v", err)
	}
	defer tx.Rollback()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stats, err := s.queryProviderStats(ctx, StatFilter{SummaryFilter: SummaryFilter{Source: string(SourceSessionLog)}})
	if err != nil {
		t.Fatalf("provider stats should bypass the occupied writer connection: %v", err)
	}
	if len(stats) != 1 || stats[0].Provider != "openai" || stats[0].Requests != 1 {
		t.Fatalf("unexpected provider stats: %+v", stats)
	}

	if _, err := s.readDB.Exec(`DELETE FROM usage_records`); err == nil {
		t.Fatal("dashboard pool unexpectedly allowed a write")
	}
}

func TestDashboardDateQueryUsesSourceOccurredIndex(t *testing.T) {
	s := NewService(t.TempDir(), nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	rows, err := s.queryDB().Query(`EXPLAIN QUERY PLAN
		SELECT provider, COUNT(*)
		FROM usage_records
		WHERE source=? AND occurred_at>=? AND occurred_at<?
		GROUP BY provider`, string(SourceSessionLog), int64(0), time.Now().AddDate(1, 0, 0).UnixNano())
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}
	if !strings.Contains(plan.String(), "idx_usage_source_occurred") {
		t.Fatalf("query plan does not use source+date index:\n%s", plan.String())
	}
}
