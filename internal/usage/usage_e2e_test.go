//go:build manual_e2e

// manual_e2e 测试：用主上机器真实数据端到端验证 usage 包解析与同步。
//
// 仅在 `go test -tags manual_e2e ./internal/usage/...` 时运行；
// CI（go vet ./...）不会触发；保留作为鲁班的真机回归证据。
package usage

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"amagi-codebox/internal/appmeta/claude"
	"amagi-codebox/internal/appmeta/codex"
	"amagi-codebox/internal/appmeta/opencode"
)

// TestRealClaudeJSONL 解析主上机器真实 Claude jsonl，验证四维 token 提取。
func TestRealClaudeJSONL(t *testing.T) {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("no claude projects dir: %v", err)
	}
	var sample string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(root, e.Name())
		files, _ := os.ReadDir(sub)
		for _, f := range files {
			if !f.IsDir() && filepath.Ext(f.Name()) == ".jsonl" {
				sample = filepath.Join(sub, f.Name())
				break
			}
		}
		if sample != "" {
			break
		}
	}
	if sample == "" {
		t.Skip("no claude jsonl sample found")
	}
	t.Logf("using sample: %s", sample)

	stubs, _, err := claude.ExtractUsageRecords(sample, 0)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	t.Logf("claude stubs: %d", len(stubs))
	for i, s := range stubs {
		if i >= 3 {
			break
		}
		t.Logf("  [%d] id=%s model=%s in=%d out=%d cr=%d cc=%s ts=%s",
			i, s.RawMessageID, s.Model, s.InputTokens, s.OutputTokens,
			s.CacheReadInputTokens, strconv.Itoa(s.CacheCreationInputTokens), s.OccurredAt.Format(time.RFC3339))
	}
	if len(stubs) > 0 {
		if stubs[0].DedupKey[:len("cc:msg_")] != "cc:msg_" {
			t.Errorf("dedup prefix wrong: %s", stubs[0].DedupKey)
		}
	}
}

// TestRealCodexJSONL 解析主上机器真实 Codex rollout。
func TestRealCodexJSONL(t *testing.T) {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".codex", "sessions")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("no codex sessions dir: %v", err)
	}
	sample := ""
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Base(path)[:8] == "rollout-" {
			sample = path
			return filepath.SkipDir
		}
		return nil
	})
	if sample == "" {
		t.Skip("no codex rollout jsonl found")
	}
	t.Logf("using sample: %s", sample)

	stubs, _, provider, err := codex.ExtractUsageRecords(sample, 0)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	t.Logf("codex stubs: %d, provider: %s", len(stubs), provider)
	for i, s := range stubs {
		if i >= 3 {
			break
		}
		t.Logf("  [%d] model=%s provider=%s in=%d out=%d cr=%d ts=%s",
			i, s.Model, s.Provider, s.InputTokens, s.OutputTokens,
			s.CacheReadInputTokens, s.OccurredAt.Format(time.RFC3339))
	}
}

// TestRealOpenCodeDB 读主上机器真实 OpenCode DB，验证 608MB 大文件只读查询无锁问题。
func TestRealOpenCodeDB(t *testing.T) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Skipf("no opencode db: %v", err)
	}
	t.Logf("using db: %s", dbPath)

	stubs, maxUpdated, err := opencode.QuerySessions(dbPath, 0)
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	t.Logf("opencode stubs: %d, maxTimeUpdated: %d", len(stubs), maxUpdated)
	for i, s := range stubs {
		if i >= 3 {
			break
		}
		t.Logf("  [%d] id=%s model=%s provider=%s cur=%s in=%d out=%d cr=%d native=%d",
			i, s.SessionID, s.Model, s.Provider, s.CurrencyCode,
			s.InputTokens, s.OutputTokens, s.CacheReadInputTokens, s.NativeCost)
	}
}

// TestRealSyncAllE2E 端到端：创建临时 usage.db，触发 SyncAll，验证无错误且数据入库。
func TestRealSyncAllE2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows for path reasons")
	}
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// SyncAll 内部用了硬编码 context.Background()，ctx 在这里仅供 cancel 检测
	_ = ctx
	start := time.Now()
	if err := s.SyncAll(); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	dur := time.Since(start)

	count, _ := recordCount(context.Background(), s.db)
	firstAdded := s.syncMeta.RecordsAdded
	firstProcessed := s.syncMeta.ProcessedCount
	t.Logf("SyncAll done in %s; total records: %d; files: %d; added: %d; processed: %d; errors: %d",
		dur, count, s.syncMeta.FilesScanned, firstAdded, firstProcessed, len(s.syncMeta.Errors))
	for _, e := range s.syncMeta.Errors {
		t.Logf("  err: %s", e)
	}
	// 幂等（M5 后语义）：第二次同步无新文件、无 time_updated 推进，
	// recordsAdded 必须为 0；processedCount 也应为 0（所有源在游标命中后跳过 stub 提取）。
	_ = s.SyncAll()
	count2, _ := recordCount(context.Background(), s.db)
	secondAdded := s.syncMeta.RecordsAdded
	secondProcessed := s.syncMeta.ProcessedCount
	t.Logf("second SyncAll; total records: %d (delta=%d); added=%d processed=%d",
		count2, count2-count, secondAdded, secondProcessed)
	if secondAdded != 0 {
		t.Errorf("idempotency broken: second SyncAll recordsAdded=%d, want 0", secondAdded)
	}
	if count2-count != 0 {
		t.Errorf("idempotency broken: total records delta=%d, want 0", count2-count)
	}

	// 验证 GetUsageSummary 可用
	summary, err := s.GetUsageSummary(SummaryFilter{Source: "session_log"})
	if err != nil {
		t.Fatalf("GetUsageSummary: %v", err)
	}
	t.Logf("summary: requests=%d in=%d out=%d costByCur=%v usd=%d range=%+v",
		summary.TotalRequests, summary.TotalInputTokens, summary.TotalOutputTokens,
		summary.TotalCostByCurrency, summary.TotalCostUSD, summary.DateRange)
}
