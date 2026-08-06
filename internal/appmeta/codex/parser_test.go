package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	// 前两行（含换行符）位于增量 offset 前，模拟已同步过的 rollout 元数据。
	line1 := `{"timestamp":"2026-07-16T10:27:44.327Z","type":"session_meta","payload":{"cwd":"/w","model_provider":"openai","model":null}}` + "\n"
	line2 := `{"timestamp":"2026-07-16T10:27:50Z","type":"turn_context","payload":{"cwd":"/w","model":"gpt-5.6-sol"}}` + "\n"
	line3 := `{"timestamp":"2026-07-16T10:27:55.240Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":50,"output_tokens":10,"reasoning_output_tokens":5}}}}` + "\n"
	content := []byte(line1 + line2 + line3)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	offset := int64(len(line1) + len(line2))
	context, err := ReadUsageContext(path, offset)
	if err != nil {
		t.Fatalf("ReadUsageContext: %v", err)
	}
	records, _, nextContext, err := ExtractUsageRecordsWithContext(path, offset, context)
	if err != nil {
		t.Fatalf("resume Extract: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("resume mode: expected 1 record, got %d", len(records))
	}
	if records[0].InputTokens != 100 {
		t.Errorf("resume input = %d, want 100", records[0].InputTokens)
	}
	if records[0].Provider != "openai" || records[0].Model != "gpt-5.6-sol" {
		t.Errorf("resume context = %s/%s, want openai/gpt-5.6-sol", records[0].Provider, records[0].Model)
	}
	if nextContext.Provider != "openai" || nextContext.Model != "gpt-5.6-sol" {
		t.Errorf("next context = %s/%s, want openai/gpt-5.6-sol", nextContext.Provider, nextContext.Model)
	}
}

// --- 超长行 / offset 精确性测试（token too long 修复验证） ---

// mustJSONLine 把 obj 序列化为单行 JSON 并追加换行符（模拟 codex rollout 追加写）。
func mustJSONLine(t *testing.T, obj any) []byte {
	t.Helper()
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal line: %v", err)
	}
	return append(data, '\n')
}

// mustJSONLineNoNL 序列化为单行 JSON 但不追加换行符，用于构造文件末尾无 \n 的尾行。
func mustJSONLineNoNL(t *testing.T, obj any) []byte {
	t.Helper()
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal line: %v", err)
	}
	return data
}

// writeRollout 把若干（已含/不含换行的）行字节依次写入一个 rollout-*.jsonl 文件，
// 返回路径与文件字节大小。
func writeRollout(t *testing.T, lines ...[]byte) (string, int64) {
	t.Helper()
	dir := t.TempDir()
	name := "rollout-2026-07-20T11-40-08-019f7d9c-11c3-7f62-bf22-02c92ae3dcb7.jsonl"
	path := filepath.Join(dir, name)
	var buf bytes.Buffer
	for _, l := range lines {
		buf.Write(l)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat rollout: %v", err)
	}
	return path, info.Size()
}

func codexSessionMetaLine(t *testing.T) []byte {
	return mustJSONLine(t, map[string]any{
		"timestamp": "2026-07-20T11:40:08.000Z",
		"type":      "session_meta",
		"payload": map[string]any{
			"session_id":     "sess-1",
			"id":             "sess-1",
			"cwd":            "/proj",
			"model_provider": "openai",
			"model":          nil,
			"cli_version":    "0.144.5",
		},
	})
}

func codexTurnContextLine(t *testing.T, model string) []byte {
	return mustJSONLine(t, map[string]any{
		"timestamp": "2026-07-20T11:40:09.000Z",
		"type":      "turn_context",
		"payload": map[string]any{
			"turn_id": "t1",
			"cwd":     "/proj",
			"model":   model,
		},
	})
}

// codexOversizedEventLine 构造一条 event_msg 行，其 payload.data 为 sizeBytes 量级的字符串，
// 使整行远超旧 16MiB bufio.Scanner 上限（用于验证 ReadBytes 无上限方案）。
func codexOversizedEventLine(t *testing.T, sizeBytes int) []byte {
	return mustJSONLine(t, map[string]any{
		"timestamp": "2026-07-20T11:40:10.000Z",
		"type":      "event_msg",
		"payload": map[string]any{
			"type": "reasoning_text_delta", // 非 token_count，整行会被读入但跳过
			"data": strings.Repeat("a", sizeBytes),
		},
	})
}

func codexTokenCountLine(t *testing.T) []byte {
	return mustJSONLine(t, map[string]any{
		"timestamp": "2026-07-20T11:41:00.000Z",
		"type":      "event_msg",
		"payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens":            100,
					"cached_input_tokens":     20,
					"output_tokens":           50,
					"reasoning_output_tokens": 10,
					"total_tokens":            160,
				},
			},
		},
	})
}

// TestExtractUsageRecordsHandlesOversizedLine 验证单行 >16MiB 时不再报
// bufio.Scanner: token too long，且超长行之后的 token_count 记录被正确提取、
// offset 精确等于文件大小。
func TestExtractUsageRecordsHandlesOversizedLine(t *testing.T) {
	path, size := writeRollout(t,
		codexSessionMetaLine(t),
		codexTurnContextLine(t, "gpt-5.6-sol"),
		codexOversizedEventLine(t, 20*1024*1024), // 20MB，远超旧 16MiB 上限
		codexTokenCountLine(t),
	)

	records, lastOffset, ctx, err := ExtractUsageRecordsWithContext(path, 0, UsageContext{})
	if err != nil {
		t.Fatalf("超长行应被完整读入而不报错: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("期望 1 条 token_count 记录，得到 %d", len(records))
	}
	r := records[0]
	// reasoning_output_tokens 归入 output：50 + 10 = 60
	if r.InputTokens != 100 || r.OutputTokens != 60 || r.CacheReadInputTokens != 20 {
		t.Fatalf("token 维度不符: in=%d out=%d cr=%d", r.InputTokens, r.OutputTokens, r.CacheReadInputTokens)
	}
	if r.Model != "gpt-5.6-sol" || r.Provider != "openai" || r.SessionID != "sess-1" || r.ProjectDir != "/proj" {
		t.Fatalf("元信息提取不符: %#v", r)
	}
	if lastOffset != size {
		t.Fatalf("lastOffset=%d 应等于文件字节大小 %d", lastOffset, size)
	}
	if ctx.Provider != "openai" || ctx.Model != "gpt-5.6-sol" || ctx.SessionID != "sess-1" || ctx.ProjectDir != "/proj" {
		t.Fatalf("nextContext 不符: %#v", ctx)
	}
}

// TestExtractUsageRecordsOffsetMatchesFileSizeNoTrailingNewline 验证文件末尾无 \n 时，
// lastOffset 仍精确等于文件字节大小（旧 +1 假设会在此场景多算一字节）。
func TestExtractUsageRecordsOffsetMatchesFileSizeNoTrailingNewline(t *testing.T) {
	path, size := writeRollout(t,
		codexSessionMetaLine(t),
		codexTurnContextLine(t, "gpt-5.6-sol"),
		// 末行无换行符
		mustJSONLineNoNL(t, map[string]any{
			"timestamp": "2026-07-20T11:41:00.000Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{
					"last_token_usage": map[string]any{
						"input_tokens": 7, "output_tokens": 3, "total_tokens": 10,
					},
				},
			},
		}),
	)

	records, lastOffset, _, err := ExtractUsageRecordsWithContext(path, 0, UsageContext{})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("期望 1 条记录，得到 %d", len(records))
	}
	if lastOffset != size {
		t.Fatalf("无尾换行场景 lastOffset=%d 应等于文件大小 %d", lastOffset, size)
	}
}

// TestExtractUsageRecordsResumesFromOffset 验证断点续传：首次全量读得到的 lastOffset
// 作为下次 startOffset 时，不再产生重复记录（offset 落在有效行边界，resume 正确）。
func TestExtractUsageRecordsResumesFromOffset(t *testing.T) {
	path, _ := writeRollout(t,
		codexSessionMetaLine(t),
		codexTurnContextLine(t, "gpt-5.6-sol"),
		codexTokenCountLine(t),
	)

	first, lastOffset, ctx, err := ExtractUsageRecordsWithContext(path, 0, UsageContext{})
	if err != nil {
		t.Fatalf("首次解析: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("首次应得到 1 条记录，得到 %d", len(first))
	}
	// 从 lastOffset 续读：文件已读完，不应再产出记录，offset 不变。
	again, againOffset, _, err := ExtractUsageRecordsWithContext(path, lastOffset, ctx)
	if err != nil {
		t.Fatalf("续读解析: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("续读应得到 0 条记录（已读完），得到 %d", len(again))
	}
	if againOffset != lastOffset {
		t.Fatalf("续读 offset 应不变: again=%d first=%d", againOffset, lastOffset)
	}
}

// TestReadUsageContextHandlesOversizedLine 验证 ReadUsageContext 同样能吞下超长行，
// 且超长行不阻断其后 turn_context 等元信息的提取。
func TestReadUsageContextHandlesOversizedLine(t *testing.T) {
	path, size := writeRollout(t,
		codexSessionMetaLine(t),
		codexOversizedEventLine(t, 20*1024*1024),
		codexTurnContextLine(t, "gpt-5.6-sol"),
	)

	ctx, err := ReadUsageContext(path, size)
	if err != nil {
		t.Fatalf("ReadUsageContext 应处理超长行: %v", err)
	}
	if ctx.Provider != "openai" {
		t.Fatalf("Provider 应来自 session_meta: %#v", ctx)
	}
	if ctx.Model != "gpt-5.6-sol" {
		t.Fatalf("Model 应来自超长行之后的 turn_context: %#v", ctx)
	}
	if ctx.ProjectDir != "/proj" || ctx.SessionID != "sess-1" {
		t.Fatalf("上下文元信息不符: %#v", ctx)
	}
}

// --- Major-1：兼容旧 Scanner 产生的 size+1 历史游标 ---
//
// 旧实现对无尾 \n 的末行执行 len(line)+1，sync_state.last_line_offset 可能等于
// 当时 fileSize+1。新实现必须把这种游标安全迁移为 fileSize，避免永久空扫与追加
// 首字节错位。下列两组测试用真实 size+1 游标构造。

// codexNoTrailingNewlineRollout 构造末行无 \n 的 rollout，返回路径与文件大小。
// 旧 Scanner 会把 lastOffset 记为 size+1（多算 1 字节）。
func codexNoTrailingNewlineRollout(t *testing.T) (string, int64) {
	t.Helper()
	return writeRollout(t,
		codexSessionMetaLine(t),
		codexTurnContextLine(t, "gpt-5.6-sol"),
		mustJSONLineNoNL(t, map[string]any{
			"timestamp": "2026-07-20T11:41:00.000Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{
					"last_token_usage": map[string]any{
						"input_tokens": 7, "output_tokens": 3, "total_tokens": 10,
					},
				},
			},
		}),
	)
}

// TestExtractUsageRecordsHandlesLegacySizePlusOneCursorNoGrowth 验证旧 size+1 游标
// 在文件未增长时：不报错、无 records、返回 offset==fileSize（使 sync 层快速跳过生效）。
func TestExtractUsageRecordsHandlesLegacySizePlusOneCursorNoGrowth(t *testing.T) {
	path, size := codexNoTrailingNewlineRollout(t)
	legacyCursor := size + 1 // 模拟旧 Scanner 写入的越界游标

	records, lastOffset, _, err := ExtractUsageRecordsWithContext(path, legacyCursor, UsageContext{})
	if err != nil {
		t.Fatalf("legacy size+1 游标在文件未增长时不应报错: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("文件未增长时应返回 0 records，得到 %d", len(records))
	}
	// offset 应被 clamp 为 fileSize，使 sync 层 offset==size 快速跳过条件成立
	if lastOffset != size {
		t.Fatalf("lastOffset=%d 应被收敛为 fileSize=%d（消除 size+1 残留）", lastOffset, size)
	}
}

// TestExtractUsageRecordsHandlesLegacySizePlusOneCursorAfterAppend 验证旧 size+1 游标
// 在文件后续追加一行合法记录时：正确解析追加行、offset 推进到新文件大小。
func TestExtractUsageRecordsHandlesLegacySizePlusOneCursorAfterAppend(t *testing.T) {
	path, size := codexNoTrailingNewlineRollout(t)

	// 模拟 codex 追加写：先补齐原末行换行，再写新行（含自身 \n）。
	appended := mustJSONLine(t, map[string]any{
		"timestamp": "2026-07-20T11:42:00.000Z",
		"type":      "event_msg",
		"payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens": 20, "output_tokens": 5, "total_tokens": 25,
				},
			},
		},
	})
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.Write([]byte("\n")); err != nil { // 补齐原末行 \n
		t.Fatalf("write newline: %v", err)
	}
	if _, err := f.Write(appended); err != nil {
		t.Fatalf("write appended: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	newSize := size + 1 + int64(len(appended))

	legacyCursor := size + 1 // 旧游标；追加后恰好对齐新行起点（size 位置是补的 \n）
	records, lastOffset, _, err := ExtractUsageRecordsWithContext(path, legacyCursor, UsageContext{})
	if err != nil {
		t.Fatalf("追加后续读不应报错: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("应解析出追加的 1 条记录，得到 %d", len(records))
	}
	if records[0].InputTokens != 20 || records[0].OutputTokens != 5 {
		t.Fatalf("追加记录 token 不符: in=%d out=%d", records[0].InputTokens, records[0].OutputTokens)
	}
	if lastOffset != newSize {
		t.Fatalf("lastOffset=%d 应等于新文件大小 %d", lastOffset, newSize)
	}
}

// --- Minor-1：ReadUsageContext 不得吞非 EOF I/O 错误 ---

// failingReader 读完全部 data 后下一轮返回预设的非 EOF 错误，模拟底层 I/O 故障。
// 用于触发「ReadBytes 返回 data + 非 EOF 错误」场景。
type failingReader struct {
	data []byte
	err  error
	read int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.read >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.read:])
	r.read += n
	// 读到行尾（无 \n）时本轮一并返回错误，模拟读到末行时的底层故障
	if r.read >= len(r.data) {
		return n, r.err
	}
	return n, nil
}

// TestReadUsageContextPropagatesNonEOFReaderError 验证 ReadBytes 返回 data + 非 EOF
// 错误且达到 endOffset 时，错误被传播而非被 endOffset break 吞掉（Minor-1）。
//
// 修复前：`if offset >= endOffset { break }` 在错误检查之前，达到 endOffset 时
// 静默成功；修复后：先传播非 EOF 错误，EOF/无错再按 endOffset 结束。
func TestReadUsageContextPropagatesNonEOFReaderError(t *testing.T) {
	// 一行无尾换行的 session_meta + 非 EOF 错误：ReadBytes 返回 (data, err)。
	data := []byte(`{"type":"session_meta","payload":{"model_provider":"openai","model":"gpt-x","cwd":"/p","id":"s1"}}`)
	sentinel := errors.New("simulated read failure")
	r := &failingReader{data: data, err: sentinel}
	// endOffset 设为 len(data)：修复前 offset>=endOffset 命中 break 吞错误，
	// 修复后先传播 readErr。
	_, err := readUsageContextFromReader(r, int64(len(data)), "fallback-sess", "test-source")
	if err == nil {
		t.Fatalf("应传播非 EOF I/O 错误，得到 nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("错误应包装 sentinel，得到: %v", err)
	}
}
