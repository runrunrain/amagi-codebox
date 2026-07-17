package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExtractUsageRecordsClaudeSample 用主上机器真实样本结构构造测试数据。
//
// 样本来自实测（task brief 17 章与代码探查）：
//
//	{"parentUuid":"...","type":"assistant","message":{
//	  "id":"msg_xxx","model":"glm-5-turbo","usage":{
//	    "input_tokens":48386,"cache_read_input_tokens":2240,
//	    "cache_creation_input_tokens":0,"output_tokens":168}},
//	 "timestamp":"2026-07-17T02:42:42.807Z","session_id":"abc","cwd":"/work"}
func TestExtractUsageRecordsClaudeSample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-session.jsonl")
	// 构造一个混合 jsonl：1 行 system init + 1 行 user + 1 行 assistant + 1 行 assistant
	content := []byte(`{"type":"system","subtype":"init","cwd":"/work"}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"hi"},"origin":{"kind":"human"}}` + "\n" +
		`{"type":"assistant","message":{"id":"msg_aaa","model":"glm-5-turbo","usage":{"input_tokens":48386,"cache_read_input_tokens":2240,"cache_creation_input_tokens":0,"output_tokens":168}},"timestamp":"2026-07-17T02:42:42.807Z","session_id":"sess-a","cwd":"/work"}` + "\n" +
		`{"type":"assistant","message":{"id":"msg_bbb","model":"claude-sonnet-4-20250514","usage":{"input_tokens":100,"cache_read_input_tokens":10,"cache_creation_input_tokens":5,"output_tokens":50}},"timestamp":"2026-07-17T03:00:00.000Z","cwd":"/work"}` + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, lastOffset, err := ExtractUsageRecords(path, 0)
	if err != nil {
		t.Fatalf("ExtractUsageRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 assistant records, got %d", len(records))
	}

	// 验证第一条
	r := records[0]
	if r.DedupKey != "cc:msg_msg_aaa" {
		t.Errorf("dedup = %q, want cc:msg_msg_aaa", r.DedupKey)
	}
	if r.Model != "glm-5-turbo" {
		t.Errorf("model = %q, want glm-5-turbo", r.Model)
	}
	if r.InputTokens != 48386 {
		t.Errorf("input = %d, want 48386", r.InputTokens)
	}
	if r.CacheReadInputTokens != 2240 {
		t.Errorf("cache_read = %d, want 2240", r.CacheReadInputTokens)
	}
	want, _ := time.Parse(time.RFC3339Nano, "2026-07-17T02:42:42.807Z")
	if !r.OccurredAt.Equal(want) {
		t.Errorf("occurred_at = %v, want %v", r.OccurredAt, want)
	}
	if r.SessionID != "sess-a" {
		t.Errorf("session_id = %q, want sess-a", r.SessionID)
	}

	// 第二条应解析 claude-sonnet-4-20250514
	if records[1].Model != "claude-sonnet-4-20250514" {
		t.Errorf("2nd model = %q", records[1].Model)
	}

	// 验证 lastOffset 在文件总长范围内
	info, _ := os.Stat(path)
	if lastOffset != info.Size() {
		t.Errorf("lastOffset = %d, want file size %d", lastOffset, info.Size())
	}

	// 断点续传：从 lastOffset 再读应得 0 条（EOF）
	records2, _, err := ExtractUsageRecords(path, lastOffset)
	if err != nil {
		t.Fatalf("re-Extract: %v", err)
	}
	if len(records2) != 0 {
		t.Errorf("after lastOffset, got %d records, want 0", len(records2))
	}
}

// TestExtractUsageRecordsClaudeBadJSONContinues 验证单行解析失败不中断。
func TestExtractUsageRecordsClaudeBadJSONContinues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mix.jsonl")
	content := []byte(`{"type":"assistant","message":{"id":"m1","model":"x","usage":{"input_tokens":1,"output_tokens":2}},"timestamp":"2026-07-17T02:42:42.807Z"}` + "\n" +
		`{not valid json}` + "\n" +
		`{"type":"assistant","message":{"id":"m2","model":"y","usage":{"input_tokens":3,"output_tokens":4}},"timestamp":"2026-07-17T02:43:42.807Z"}` + "\n")
	os.WriteFile(path, content, 0o600)

	records, _, err := ExtractUsageRecords(path, 0)
	if err != nil {
		t.Fatalf("ExtractUsageRecords: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records (bad line skipped), got %d", len(records))
	}
}
