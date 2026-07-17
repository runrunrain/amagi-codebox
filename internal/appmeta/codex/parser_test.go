package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExtractUsageRecordsCodexSample 用实测样本结构构造测试数据。
//
// 样本（实测主上机器 0.144.5）：
//   - session_meta 行：含 cwd, model_provider, model=null
//   - turn_context 行：含 model（真实名）
//   - event_msg token_count 行：payload.info.last_token_usage.{input_tokens, cached_input_tokens,
//     output_tokens, reasoning_output_tokens}
//   - 根级 timestamp（ISO8601）
func TestExtractUsageRecordsCodexSample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-07-16T18-27-44-test.jsonl")
	content := []byte(`{"timestamp":"2026-07-16T10:27:44.327Z","type":"session_meta","payload":{"id":"019f6a77-ce0a-76d0-8853-39bacabb5d00","cwd":"/Users/test/work","model_provider":"openai","model":null}}` + "\n" +
		`{"timestamp":"2026-07-16T10:27:45Z","type":"turn_context","payload":{"turn_id":"t1","cwd":"/Users/test/work","model":"gpt-5.6-sol"}}` + "\n" +
		`{"timestamp":"2026-07-16T10:27:55.240Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":21172,"cached_input_tokens":7552,"output_tokens":354,"reasoning_output_tokens":284,"total_tokens":21526}}}}` + "\n" +
		`{"timestamp":"2026-07-16T10:29:03.466Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":30791,"cached_input_tokens":20864,"output_tokens":143,"reasoning_output_tokens":68,"total_tokens":30934}}}}` + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, lastOffset, provider, err := ExtractUsageRecords(path, 0)
	if err != nil {
		t.Fatalf("ExtractUsageRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 token_count records, got %d", len(records))
	}

	// provider 应从 session_meta.model_provider 提取
	if provider != "openai" {
		t.Errorf("provider = %q, want openai", provider)
	}

	// 第一条：input=21172, cached=7552（作为 cache_read）, output=354+284=638（reasoning 归 output）
	r1 := records[0]
	if r1.InputTokens != 21172 {
		t.Errorf("input1 = %d, want 21172", r1.InputTokens)
	}
	if r1.CacheReadInputTokens != 7552 {
		t.Errorf("cache_read1 = %d, want 7552", r1.CacheReadInputTokens)
	}
	if r1.OutputTokens != 354+284 {
		t.Errorf("output1 = %d, want %d (reasoning folded in)", r1.OutputTokens, 354+284)
	}
	if r1.Model != "gpt-5.6-sol" {
		t.Errorf("model1 = %q, want gpt-5.6-sol", r1.Model)
	}
	if r1.Provider != "openai" {
		t.Errorf("provider1 = %q, want openai", r1.Provider)
	}
	want, _ := time.Parse(time.RFC3339Nano, "2026-07-16T10:27:55.240Z")
	if !r1.OccurredAt.Equal(want) {
		t.Errorf("occurred_at1 = %v, want %v", r1.OccurredAt, want)
	}
	if r1.DedupKey == "" || r1.DedupKey[:3] != "cx:" {
		t.Errorf("dedup1 = %q, want cx: prefix", r1.DedupKey)
	}

	// 两条 dedup_key 应不同（timestamp 不同）
	if records[0].DedupKey == records[1].DedupKey {
		t.Errorf("dedup keys should differ across turns")
	}

	// lastOffset 在文件大小范围内
	info, _ := os.Stat(path)
	if lastOffset != info.Size() {
		t.Errorf("lastOffset = %d, want %d", lastOffset, info.Size())
	}
}

// TestExtractUsageRecordsCodexResume 验证断点续传。
func TestExtractUsageRecordsCodexResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-test.jsonl")
	// 第一行（含换行符）的长度
	line1 := `{"timestamp":"2026-07-16T10:27:44.327Z","type":"session_meta","payload":{"cwd":"/w","model_provider":"openai","model":null}}` + "\n"
	line2 := `{"timestamp":"2026-07-16T10:27:55.240Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":50,"output_tokens":10,"reasoning_output_tokens":5}}}}` + "\n"
	content := []byte(line1 + line2)
	os.WriteFile(path, content, 0o600)

	// 从 line1 末尾续传：仅能读到 line2，且 provider/model 从已扫过的 session_meta 状态会丢失
	// （因为我们从头扫才能拿到 session_meta）。验证：续传模式下 records 数正确。
	records, _, _, err := ExtractUsageRecords(path, int64(len(line1)))
	if err != nil {
		t.Fatalf("resume Extract: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("resume mode: expected 1 record, got %d", len(records))
	}
	if records[0].InputTokens != 100 {
		t.Errorf("resume input = %d, want 100", records[0].InputTokens)
	}
}
