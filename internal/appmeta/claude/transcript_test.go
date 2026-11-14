package claude

// transcript_test.go 验证转录增量解析器（ReadTranscriptIncrement）与转录根助手
// （ClaudeConfigRoot / SessionJSONLPathInRoot / FindLatestActiveJSONLInRoot）。
//
// fixture 来源声明（设计 §5.1/§11/§13-R1：块级字段名以真实样本回放为单测依据，
// 禁止手写想象样本）：testdata/transcript.sample.jsonl 由本机 Claude Code 2.1.319
// 三个真实会话采集脱敏固化——
//   - -p（print/sdk）模式两轮：ultrathink 读 notes.txt（thinking / tool_use /
//     tool_result 成功 / 最终文本 / usage 四分项 / system stop_hook_summary）、
//     读 missing.txt（is_error=true 的 tool_result）；
//   - winpty 模拟 TTY 的交互式一轮：origin.kind=="human" 的 user 行。
// 脱敏：用户路径 → C:\Users\user\demo；sessionId/uuid/promptId/message.id 等标识符
// → 固定占位值；块结构、文本、时间戳、版本号原样保留。jsonl.go 既有 helper
// （writeJSONL/rawJSONLine/touchWithMTime/unwrapPathErr）在同包复用。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// transcriptFixturePath 是真实样本 fixture 的包内相对路径（go test 工作目录 = 包目录）。
const transcriptFixturePath = "testdata/transcript.sample.jsonl"

// TestReadTranscriptIncrementSampleFixture 以真实样本回放断言全部已解析字段。
//
// fixture 9 行的期望投影（脱敏后固定值）：
//  1. user      origin.kind=human（交互式）、string content
//  2. assistant thinking 块 + usage 四分项（input 17480 / output 282 / read 6400 / write 0）
//  3. assistant tool_use(Read notes.txt)，与行 2 共享 message.id（同 API message 拆行）
//  4. user      tool_result 成功（无 is_error 字段 → false）
//  5. assistant 最终文本 + 第二轮 usage（2168 / 63 / 23872 / 0）
//  6. system    stop_hook_summary（无 message 字段）
//  7. assistant tool_use(Read missing.txt)
//  8. user      tool_result is_error=true
//  9. user      -p/sdk 模式 human 输入（无 origin 字段——2.1.319 实测漂移形态）
func TestReadTranscriptIncrementSampleFixture(t *testing.T) {
	recs, newOffset, truncated, err := ReadTranscriptIncrement(transcriptFixturePath, 0)
	if err != nil {
		t.Fatalf("ReadTranscriptIncrement fixture: %v", err)
	}
	if truncated {
		t.Fatalf("全量读取不应置 truncated")
	}
	if len(recs) != 9 {
		t.Fatalf("期望 9 条记录（fixture 全部为消息行），得到 %d", len(recs))
	}
	info, statErr := os.Stat(transcriptFixturePath)
	if statErr != nil {
		t.Fatalf("stat fixture: %v", statErr)
	}
	if newOffset != info.Size() {
		t.Fatalf("newOffset=%d 应等于文件大小 %d", newOffset, info.Size())
	}

	// 行 1：交互式 human 输入。
	r1 := recs[0]
	if r1.Kind != "user" || !r1.IsHuman {
		t.Errorf("行1: Kind=%q IsHuman=%v，期望 user/human", r1.Kind, r1.IsHuman)
	}
	if r1.UUID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("行1 UUID=%q", r1.UUID)
	}
	wantText1 := "Please read the file notes.txt and reply with exactly one short sentence describing what it contains."
	if r1.Text != wantText1 {
		t.Errorf("行1 Text:\n got=%q\nwant=%q", r1.Text, wantText1)
	}
	if r1.Timestamp != "2026-11-13T21:22:15.901Z" {
		t.Errorf("行1 Timestamp=%q", r1.Timestamp)
	}
	if r1.Usage != nil || len(r1.ToolUses) != 0 || len(r1.ToolResults) != 0 || r1.Model != "" {
		t.Errorf("行1 不应有 usage/toolUses/toolResults/model: %+v", r1)
	}

	// 行 2：assistant thinking 块 + usage 四分项。
	r2 := recs[1]
	if r2.Kind != "assistant" || r2.Model != "glm-5.3" {
		t.Errorf("行2: Kind=%q Model=%q", r2.Kind, r2.Model)
	}
	if r2.Text != "" {
		t.Errorf("行2 无 text 块，Text 应为空，得到 %q", r2.Text)
	}
	if !strings.HasPrefix(r2.Thinking, "The user asks me to read notes.txt") ||
		!strings.HasSuffix(r2.Thinking, "Let me read the file first.") ||
		!strings.Contains(r2.Thinking, "ultrathink") {
		t.Errorf("行2 Thinking 抽取不符（前缀/后缀/关键词）: %q…", clipStr(r2.Thinking, 80))
	}
	if r2.Usage == nil {
		t.Fatalf("行2 应有 usage")
	}
	if r2.Usage.In != 17480 || r2.Usage.Out != 282 || r2.Usage.CacheRead != 6400 || r2.Usage.CacheWrite != 0 {
		t.Errorf("行2 usage 四分项不符: %+v", r2.Usage)
	}

	// 行 3：assistant tool_use（与行 2 同一 message.id，各有不同行 uuid）。
	r3 := recs[2]
	if len(r3.ToolUses) != 1 {
		t.Fatalf("行3 应有 1 个 tool_use，得到 %d", len(r3.ToolUses))
	}
	tu := r3.ToolUses[0]
	if tu.ID != "call_37b3dd771ef74b8d868cdc16" || tu.Name != "Read" {
		t.Errorf("行3 tool_use id/name 不符: %q / %q", tu.ID, tu.Name)
	}
	var input struct {
		FilePath string `json:"file_path"`
	}
	if jsonErr := json.Unmarshal(tu.Input, &input); jsonErr != nil {
		t.Fatalf("行3 tool_use input 不是合法 JSON: %v", jsonErr)
	}
	if want := `C:\Users\user\demo\notes.txt`; input.FilePath != want {
		t.Errorf("行3 tool_use input.file_path=%q want %q", input.FilePath, want)
	}
	if len(r3.ToolResults) != 0 || r3.Text != "" || r3.Thinking != "" {
		t.Errorf("行3 不应有 toolResults/text/thinking: %+v", r3)
	}

	// 行 4：user tool_result 成功（无 is_error 字段 → false）。
	r4 := recs[3]
	if r4.Kind != "user" || r4.IsHuman {
		t.Errorf("行4: tool_result 回写行 IsHuman 应为 false（无 origin.kind=human）")
	}
	if len(r4.ToolResults) != 1 {
		t.Fatalf("行4 应有 1 个 tool_result，得到 %d", len(r4.ToolResults))
	}
	tr := r4.ToolResults[0]
	if tr.ID != "call_37b3dd771ef74b8d868cdc16" {
		t.Errorf("行4 tool_result ID=%q（应与行 3 tool_use 配对）", tr.ID)
	}
	if tr.IsError {
		t.Errorf("行4 成功结果 IsError 应为 false")
	}
	if want := "1\tThe quick brown fox jumps over the lazy dog.\n2\t"; tr.Content != want {
		t.Errorf("行4 tool_result content:\n got=%q\nwant=%q", tr.Content, want)
	}

	// 行 5：assistant 最终文本 + 第二轮 usage。
	r5 := recs[4]
	if !strings.Contains(r5.Text, "pangram") || !strings.Contains(r5.Text, "The quick brown fox") {
		t.Errorf("行5 最终文本应含 pangram 句: %q", r5.Text)
	}
	if r5.Usage == nil || r5.Usage.In != 2168 || r5.Usage.Out != 63 || r5.Usage.CacheRead != 23872 || r5.Usage.CacheWrite != 0 {
		t.Errorf("行5 usage 四分项不符: %+v", r5.Usage)
	}

	// 行 6：system（stop_hook_summary，无 message 字段）。
	r6 := recs[5]
	if r6.Kind != "system" || r6.UUID != "00000000-0000-0000-0000-000000000006" {
		t.Errorf("行6: Kind=%q UUID=%q", r6.Kind, r6.UUID)
	}
	if r6.Text != "" || r6.Usage != nil || len(r6.ToolUses) != 0 {
		t.Errorf("行6 无 message，内容字段应全空: %+v", r6)
	}
	if r6.Timestamp != "2026-11-13T21:12:22.494Z" {
		t.Errorf("行6 Timestamp=%q", r6.Timestamp)
	}

	// 行 7/8：错误轮 tool_use + is_error=true 的 tool_result。
	r7, r8 := recs[6], recs[7]
	if len(r7.ToolUses) != 1 || r7.ToolUses[0].ID != "call_2b654718bf474e3ba68cacb0" || r7.ToolUses[0].Name != "Read" {
		t.Errorf("行7 tool_use 不符: %+v", r7.ToolUses)
	}
	if len(r8.ToolResults) != 1 || !r8.ToolResults[0].IsError {
		t.Fatalf("行8 应有 is_error=true 的 tool_result: %+v", r8.ToolResults)
	}
	if r8.ToolResults[0].ID != "call_2b654718bf474e3ba68cacb0" {
		t.Errorf("行8 tool_result ID 应与行 7 tool_use 配对，得到 %q", r8.ToolResults[0].ID)
	}
	if want := "File does not exist. Note: your current working directory is C:\\Users\\user\\demo."; r8.ToolResults[0].Content != want {
		t.Errorf("行8 tool_result content:\n got=%q\nwant=%q", r8.ToolResults[0].Content, want)
	}

	// 行 9：-p/sdk 模式 human 输入——2.1.319 实测无 origin 字段，IsHuman=false。
	r9 := recs[8]
	if r9.Kind != "user" || r9.IsHuman {
		t.Errorf("行9: -p 模式无 origin，IsHuman 应为 false: Kind=%q IsHuman=%v", r9.Kind, r9.IsHuman)
	}
	if !strings.HasPrefix(r9.Text, "ultrathink: Read the file notes.txt") {
		t.Errorf("行9 Text 不符: %q", r9.Text)
	}
}

// clipStr 截断长字符串用于错误信息。
func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// TestReadTranscriptIncrementContentShapes 覆盖 content 判型与块拼接的机制边案
// （字段形状以 fixture 为锚，此处只测组合逻辑）：
// string content、数组多 text 块 \n 拼接、text+thinking 混合、
// tool_result content 为块数组的降级拼接、未知块类型忽略。
func TestReadTranscriptIncrementContentShapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shapes.jsonl")
	writeJSONL(t, path, []string{
		// string content 直接取值。
		rawJSONLine(t, map[string]any{
			"type": "user", "uuid": "s1",
			"message": map[string]any{"role": "user", "content": "plain string"},
			"origin":  map[string]any{"kind": "human"},
		}),
		// 数组 content：两个 text 块以 \n 拼接 + 一个未知块类型被忽略。
		rawJSONLine(t, map[string]any{
			"type": "user", "uuid": "s2",
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "part-a"},
					map[string]any{"type": "future_block", "x": 1},
					map[string]any{"type": "text", "text": "part-b"},
				},
			},
		}),
		// assistant：text + thinking 混合块。
		rawJSONLine(t, map[string]any{
			"type": "assistant", "uuid": "s3",
			"message": map[string]any{
				"role":  "assistant",
				"model": "m",
				"content": []any{
					map[string]any{"type": "thinking", "thinking": "ponder"},
					map[string]any{"type": "text", "text": "answer"},
				},
				"usage": map[string]any{
					"input_tokens": 1, "output_tokens": 2,
					"cache_read_input_tokens": 3, "cache_creation_input_tokens": 4,
				},
			},
		}),
		// tool_result content 为块数组（Anthropic 消息形状允许）：拼接 text 块。
		rawJSONLine(t, map[string]any{
			"type": "user", "uuid": "s4",
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "call_x",
						"content": []any{
							map[string]any{"type": "text", "text": "line-1"},
							map[string]any{"type": "text", "text": "line-2"},
						},
					},
				},
			},
		}),
	})

	recs, _, _, err := ReadTranscriptIncrement(path, 0)
	if err != nil {
		t.Fatalf("ReadTranscriptIncrement: %v", err)
	}
	if len(recs) != 4 {
		t.Fatalf("期望 4 条记录，得到 %d", len(recs))
	}
	if recs[0].Text != "plain string" {
		t.Errorf("string content: got %q", recs[0].Text)
	}
	if recs[1].Text != "part-a\npart-b" {
		t.Errorf("多 text 块拼接: got %q", recs[1].Text)
	}
	if recs[2].Text != "answer" || recs[2].Thinking != "ponder" {
		t.Errorf("text+thinking 混合: text=%q thinking=%q", recs[2].Text, recs[2].Thinking)
	}
	if recs[2].Usage == nil || recs[2].Usage.In != 1 || recs[2].Usage.Out != 2 || recs[2].Usage.CacheRead != 3 || recs[2].Usage.CacheWrite != 4 {
		t.Errorf("usage 四分项: %+v", recs[2].Usage)
	}
	if len(recs[3].ToolResults) != 1 || recs[3].ToolResults[0].Content != "line-1\nline-2" {
		t.Errorf("tool_result 块数组降级拼接: %+v", recs[3].ToolResults)
	}
}

// TestReadTranscriptIncrementOriginKinds 区分 origin.kind 三形态：
// human=true；tool（含 string content 的回写）=false；缺 origin=false。
func TestReadTranscriptIncrementOriginKinds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "origin.jsonl")
	writeJSONL(t, path, []string{
		rawJSONLine(t, map[string]any{
			"type": "user", "uuid": "o1",
			"message": map[string]any{"role": "user", "content": "human typed"},
			"origin":  map[string]any{"kind": "human"},
		}),
		rawJSONLine(t, map[string]any{
			"type": "user", "uuid": "o2",
			"message": map[string]any{"role": "user", "content": "tool ack"},
			"origin":  map[string]any{"kind": "tool"},
		}),
		rawJSONLine(t, map[string]any{
			"type":    "user",
			"uuid":    "o3",
			"message": map[string]any{"role": "user", "content": "no origin"},
		}),
		// assistant 行带 origin（异常形态）：IsHuman 不受影响（仅看 user 行）。
		rawJSONLine(t, map[string]any{
			"type": "assistant", "uuid": "o4",
			"message": map[string]any{"role": "assistant", "content": "hi"},
			"origin":  map[string]any{"kind": "human"},
		}),
	})

	recs, _, _, err := ReadTranscriptIncrement(path, 0)
	if err != nil {
		t.Fatalf("ReadTranscriptIncrement: %v", err)
	}
	if len(recs) != 4 {
		t.Fatalf("期望 4 条记录，得到 %d", len(recs))
	}
	if !recs[0].IsHuman {
		t.Errorf("origin.kind=human → IsHuman 应为 true")
	}
	if recs[1].IsHuman {
		t.Errorf("origin.kind=tool → IsHuman 应为 false")
	}
	if recs[2].IsHuman {
		t.Errorf("缺 origin → IsHuman 应为 false")
	}
	if recs[3].IsHuman {
		t.Errorf("assistant 行即使带 origin.kind=human，IsHuman 也应为 false（仅 user 行取值）")
	}
}

// TestReadTranscriptIncrementSkipsUnknownAndDamagedLines 验证单行损坏与
// 未知行类型（queue-operation/attachment 等记账行）跳过后不中断、offset 照常推进。
func TestReadTranscriptIncrementSkipsUnknownAndDamagedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.jsonl")
	writeJSONL(t, path, []string{
		rawJSONLine(t, map[string]any{"type": "queue-operation", "queued": []any{}}),
		`{this is not valid json`,
		rawJSONLine(t, map[string]any{
			"type": "user", "uuid": "ok1",
			"message": map[string]any{"role": "user", "content": "after damage"},
			"origin":  map[string]any{"kind": "human"},
		}),
		rawJSONLine(t, map[string]any{"type": "attachment", "uuid": "att"}),
		rawJSONLine(t, map[string]any{"type": "api-request", "timestamp": "t"}),
		// message 存在但形态异常（非对象）：仍产出记录、内容留空。
		rawJSONLine(t, map[string]any{"type": "user", "uuid": "ok2", "message": "not-an-object"}),
	})

	recs, newOffset, _, err := ReadTranscriptIncrement(path, 0)
	if err != nil {
		t.Fatalf("坏行不应导致文件级错误: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("期望 2 条记录（坏行与记账行全部跳过），得到 %d", len(recs))
	}
	if recs[0].UUID != "ok1" || recs[0].Text != "after damage" {
		t.Errorf("坏行后的合法记录应正常解析: %+v", recs[0])
	}
	if recs[1].UUID != "ok2" || recs[1].Text != "" || recs[1].IsHuman {
		t.Errorf("message 形态异常的行应产出空内容记录: %+v", recs[1])
	}
	info, _ := os.Stat(path)
	if newOffset != info.Size() {
		t.Errorf("跳过的行也应推进 offset: newOffset=%d size=%d", newOffset, info.Size())
	}
}

// TestReadTranscriptIncrementOffsetResume 验证断点续传不丢不重：
// 全量读 → 追加 → 续读只得多余新行；尾行无 \n 的中间态续读不丢字节。
func TestReadTranscriptIncrementOffsetResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume.jsonl")

	lineUser := func(id, text string) string {
		return rawJSONLine(t, map[string]any{
			"type": "user", "uuid": id,
			"message": map[string]any{"role": "user", "content": text},
			"origin":  map[string]any{"kind": "human"},
		})
	}

	// 场景 A：常规追加。
	writeJSONL(t, path, []string{lineUser("u1", "one"), lineUser("u2", "two")})
	recs1, off1, truncated, err := ReadTranscriptIncrement(path, 0)
	if err != nil || truncated {
		t.Fatalf("首次全量读: err=%v truncated=%v", err, truncated)
	}
	if len(recs1) != 2 {
		t.Fatalf("首次应读出 2 条，得到 %d", len(recs1))
	}
	appendLine := func(target, content string) {
		t.Helper()
		f, openErr := os.OpenFile(target, os.O_WRONLY|os.O_APPEND, 0o644)
		if openErr != nil {
			t.Fatalf("open append: %v", openErr)
		}
		if _, wErr := f.WriteString(content); wErr != nil {
			t.Fatalf("append: %v", wErr)
		}
		if cErr := f.Close(); cErr != nil {
			t.Fatalf("close: %v", cErr)
		}
	}
	appendLine(path, lineUser("u3", "three")+"\n")

	recs2, off2, truncated2, err := ReadTranscriptIncrement(path, off1)
	if err != nil || truncated2 {
		t.Fatalf("续读: err=%v truncated=%v", err, truncated2)
	}
	if len(recs2) != 1 || recs2[0].UUID != "u3" {
		t.Fatalf("续读应只得到新增 1 条 u3，得到 %+v", recs2)
	}
	full, offFull, _, err := ReadTranscriptIncrement(path, 0)
	if err != nil {
		t.Fatalf("二次全量读: %v", err)
	}
	if len(full) != 3 || full[0].UUID != "u1" || full[1].UUID != "u2" || full[2].UUID != "u3" {
		t.Fatalf("全量读应不丢不重: %+v", full)
	}
	if off2 != offFull {
		t.Errorf("续读终点 %d 应等于全量读终点 %d", off2, offFull)
	}
	recsEof, offEof, truncatedEof, err := ReadTranscriptIncrement(path, offFull)
	if err != nil || truncatedEof || len(recsEof) != 0 || offEof != offFull {
		t.Errorf("EOF 续读应为空且不截断: recs=%d off=%d truncated=%v err=%v", len(recsEof), offEof, truncatedEof, err)
	}

	// 场景 B：尾行无 \n 的中间态（文件被逐行追加过程中的真实形态）。
	path2 := filepath.Join(dir, "resume2.jsonl")
	if wErr := os.WriteFile(path2, []byte(lineUser("v1", "one")+"\n"+lineUser("v2", "two")), 0o644); wErr != nil {
		t.Fatalf("write: %v", wErr)
	}
	info2, _ := os.Stat(path2)
	recsB, offB, _, err := ReadTranscriptIncrement(path2, 0)
	if err != nil {
		t.Fatalf("中间态读: %v", err)
	}
	if len(recsB) != 2 || recsB[1].UUID != "v2" {
		t.Fatalf("无尾换行的末行应被解析: %+v", recsB)
	}
	if offB != info2.Size() {
		t.Fatalf("无尾换行 offset=%d 应等于文件大小 %d", offB, info2.Size())
	}
	appendLine(path2, "\n"+lineUser("v3", "three")+"\n")
	recsB2, offB2, _, err := ReadTranscriptIncrement(path2, offB)
	if err != nil {
		t.Fatalf("中间态续读: %v", err)
	}
	if len(recsB2) != 1 || recsB2[0].UUID != "v3" {
		t.Fatalf("中间态续读应只得到 v3（v2 不重不丢）: %+v", recsB2)
	}
	fullB, _, _, err := ReadTranscriptIncrement(path2, 0)
	if err != nil || len(fullB) != 3 {
		t.Fatalf("中间态全量对账: err=%v recs=%d", err, len(fullB))
	}
	if offB2 != mustSize(t, path2) {
		t.Errorf("中间态续读终点 %d 应等于新文件大小 %d", offB2, mustSize(t, path2))
	}
}

// TestReadTranscriptIncrementTruncatedRebuild 验证 size<offset 时 truncated=true
// 并从 0 重读全量（/resume 重写或换文件的语义）。
func TestReadTranscriptIncrementTruncatedRebuild(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trunc.jsonl")
	line := func(id string) string {
		return rawJSONLine(t, map[string]any{
			"type": "user", "uuid": id,
			"message": map[string]any{"role": "user", "content": "c-" + id},
			"origin":  map[string]any{"kind": "human"},
		})
	}
	writeJSONL(t, path, []string{line("a"), line("b"), line("c")})
	_, off1, _, err := ReadTranscriptIncrement(path, 0)
	if err != nil {
		t.Fatalf("首次读: %v", err)
	}

	// 重写为更短的文件（模拟 /resume 重写）。
	writeJSONL(t, path, []string{line("x")})
	if mustSize(t, path) >= off1 {
		t.Fatalf("测试前提：新文件应短于旧 offset（%d < %d）", mustSize(t, path), off1)
	}
	recs, newOffset, truncated, err := ReadTranscriptIncrement(path, off1)
	if err != nil {
		t.Fatalf("截断重读: %v", err)
	}
	if !truncated {
		t.Fatalf("size<offset 应置 truncated=true")
	}
	if len(recs) != 1 || recs[0].UUID != "x" {
		t.Fatalf("截断后应从 0 重读出全量新内容: %+v", recs)
	}
	if newOffset != mustSize(t, path) {
		t.Errorf("截断重读 newOffset=%d 应等于新文件大小 %d", newOffset, mustSize(t, path))
	}

	// size==offset：正常 EOF 空读，不截断。
	recs2, off2, truncated2, err := ReadTranscriptIncrement(path, newOffset)
	if err != nil || truncated2 || len(recs2) != 0 || off2 != newOffset {
		t.Errorf("size==offset 应为空读且不截断: recs=%d truncated=%v err=%v", len(recs2), truncated2, err)
	}
}

// TestReadTranscriptIncrementEmptyFile 验证空文件：零记录、offset=0、无错误。
func TestReadTranscriptIncrementEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if wErr := os.WriteFile(path, []byte{}, 0o644); wErr != nil {
		t.Fatalf("write empty: %v", wErr)
	}
	recs, newOffset, truncated, err := ReadTranscriptIncrement(path, 0)
	if err != nil {
		t.Fatalf("空文件不应报错: %v", err)
	}
	if len(recs) != 0 || newOffset != 0 || truncated {
		t.Errorf("空文件: recs=%d offset=%d truncated=%v", len(recs), newOffset, truncated)
	}
}

// TestReadTranscriptIncrementFileNotExist 验证文件不存在返回可判定错误。
func TestReadTranscriptIncrementFileNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such.jsonl")
	recs, _, _, err := ReadTranscriptIncrement(path, 0)
	if err == nil {
		t.Fatalf("文件不存在应报错（recs=%d）", len(recs))
	}
	if !os.IsNotExist(unwrapPathErr(err)) {
		t.Errorf("err 应包装 os.ErrNotExist: %v", err)
	}
}

// TestClaudeConfigRoot 覆盖三分支（CLAUDE_CONFIG_DIR 优先 / HOME 值 /
// 缺省 homeDir/.claude）与大小写不敏感、空值穿透、USERPROFILE 回退。
func TestClaudeConfigRoot(t *testing.T) {
	cases := []struct {
		name    string
		homeDir string
		env     []string
		want    string
	}{
		{
			name:    "claude_config_dir_priority",
			homeDir: `C:\Users\default`,
			env:     []string{"HOME=/h", "USERPROFILE=C:\\Users\\up", "CLAUDE_CONFIG_DIR=D:\\cc-root"},
			want:    `D:\cc-root`,
		},
		{
			name:    "claude_config_dir_case_insensitive",
			homeDir: "/def",
			env:     []string{"claude_config_dir=/cc", "HOME=/h"},
			want:    "/cc",
		},
		{
			name:    "claude_config_dir_empty_falls_through",
			homeDir: "/def",
			env:     []string{"CLAUDE_CONFIG_DIR=", "HOME=/h"},
			want:    filepath.Join("/h", ".claude"),
		},
		{
			name:    "claude_config_dir_blank_falls_through",
			homeDir: "/def",
			env:     []string{"CLAUDE_CONFIG_DIR=   ", "HOME=/h"},
			want:    filepath.Join("/h", ".claude"),
		},
		{
			name:    "home_value_branch",
			homeDir: "/def",
			env:     []string{"HOME=X:/redirect"},
			want:    filepath.Join("X:/redirect", ".claude"),
		},
		{
			name:    "home_beats_userprofile",
			homeDir: "/def",
			env:     []string{"USERPROFILE=C:\\Users\\up", "HOME=/h"},
			want:    filepath.Join("/h", ".claude"),
		},
		{
			name:    "userprofile_branch",
			homeDir: "/def",
			env:     []string{"USERPROFILE=C:\\Users\\up"},
			want:    filepath.Join(`C:\Users\up`, ".claude"),
		},
		{
			name:    "default_home_dir",
			homeDir: "/def",
			env:     nil,
			want:    filepath.Join("/def", ".claude"),
		},
		{
			name:    "default_when_env_has_no_known_keys",
			homeDir: "/def",
			env:     []string{"PATH=/bin", "SHELL=/bin/sh"},
			want:    filepath.Join("/def", ".claude"),
		},
		{
			name:    "value_with_equals_sign_kept",
			homeDir: "/def",
			env:     []string{"CLAUDE_CONFIG_DIR=/a=b"},
			want:    "/a=b",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClaudeConfigRoot(c.homeDir, c.env); got != c.want {
				t.Errorf("ClaudeConfigRoot(%q, %v)\n got=%q\nwant=%q", c.homeDir, c.env, got, c.want)
			}
		})
	}
}

// TestSessionJSONLPathInRoot 验证 InRoot 路径拼接与既有 SessionJSONLPath 的等价关系
// （root=homeDir/.claude 时两函数产出一致——既有调用方零改动的契约锚）。
func TestSessionJSONLPathInRoot(t *testing.T) {
	got := SessionJSONLPathInRoot(`/data/cc-config`, `X:/WorkSpace`, "abc-123")
	wantSuffix := filepath.Join("projects", "X--WorkSpace", "abc-123.jsonl")
	if !strings.HasSuffix(filepath.ToSlash(got), filepath.ToSlash(wantSuffix)) {
		t.Errorf("SessionJSONLPathInRoot 后缀不符:\n got=%q\nwant suffix=%q", got, wantSuffix)
	}
	if !strings.HasPrefix(filepath.ToSlash(got), "/data/cc-config/") {
		t.Errorf("root 前缀丢失: %q", got)
	}

	// 等价关系：SessionJSONLPath(homeDir,…) == SessionJSONLPathInRoot(homeDir/.claude,…)。
	const homeDir = "C:/Users/u"
	const workDir = `X:\WorkSpace\proj`
	const sid = "sess-uuid"
	legacy := SessionJSONLPath(homeDir, workDir, sid)
	inRoot := SessionJSONLPathInRoot(filepath.Join(homeDir, ".claude"), workDir, sid)
	if legacy != inRoot {
		t.Errorf("InRoot 与既有 SessionJSONLPath 应等价:\n legacy=%q\n inRoot=%q", legacy, inRoot)
	}
}

// TestFindLatestActiveJSONLInRoot 验证 mtime 最新回退（方案 P 语义）与错误分支，
// 并断言与既有 FindLatestActiveJSONL 在 root=homeDir/.claude 下等价。
func TestFindLatestActiveJSONLInRoot(t *testing.T) {
	const workDir = "X:/WorkSpace/demo"
	root := t.TempDir()
	encoded := pathSepReplacer.Replace(workDir)
	dir := filepath.Join(root, "projects", encoded)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		t.Fatalf("mkdir: %v", mkErr)
	}
	base := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	paths := []struct {
		name string
		off  time.Duration
	}{
		{"old.jsonl", 0},
		{"mid.jsonl", 10 * time.Second},
		{"newest.jsonl", 20 * time.Second},
	}
	for _, p := range paths {
		fp := filepath.Join(dir, p.name)
		if wErr := os.WriteFile(fp, []byte("{}\n"), 0o644); wErr != nil {
			t.Fatalf("write %s: %v", p.name, wErr)
		}
		touchWithMTime(t, fp, base.Add(p.off))
	}
	// 非 jsonl 噪音（mtime 更新）应被忽略。
	noise := filepath.Join(dir, "noise.log")
	if wErr := os.WriteFile(noise, []byte("x\n"), 0o644); wErr != nil {
		t.Fatalf("write noise: %v", wErr)
	}
	touchWithMTime(t, noise, base.Add(time.Minute))

	path, sid, err := FindLatestActiveJSONLInRoot(root, workDir)
	if err != nil {
		t.Fatalf("FindLatestActiveJSONLInRoot: %v", err)
	}
	if sid != "newest" {
		t.Errorf("sessionID=%q, want newest", sid)
	}
	if !strings.HasSuffix(filepath.ToSlash(path), "newest.jsonl") {
		t.Errorf("path=%q 应指向 newest.jsonl", path)
	}

	// 与既有 FindLatestActiveJSONL 的等价关系（root=homeDir/.claude）。
	homeDir := t.TempDir()
	legacyDir := filepath.Join(homeDir, ".claude", "projects", encoded)
	if mkErr := os.MkdirAll(legacyDir, 0o755); mkErr != nil {
		t.Fatalf("mkdir legacy: %v", mkErr)
	}
	if wErr := os.WriteFile(filepath.Join(legacyDir, "only.jsonl"), []byte("{}\n"), 0o644); wErr != nil {
		t.Fatalf("write legacy: %v", wErr)
	}
	p1, s1, err1 := FindLatestActiveJSONL(homeDir, workDir)
	p2, s2, err2 := FindLatestActiveJSONLInRoot(filepath.Join(homeDir, ".claude"), workDir)
	if err1 != nil || err2 != nil {
		t.Fatalf("等价对账 err1=%v err2=%v", err1, err2)
	}
	if p1 != p2 || s1 != s2 {
		t.Errorf("InRoot 与既有 FindLatestActiveJSONL 应等价:\n legacy=(%q,%q)\n inRoot=(%q,%q)", p1, s1, p2, s2)
	}

	// 错误分支：目录不存在 / 目录为空。
	if _, _, err := FindLatestActiveJSONLInRoot(t.TempDir(), workDir); err == nil {
		t.Errorf("目录不存在应报错")
	}
	emptyRoot := t.TempDir()
	if mkErr := os.MkdirAll(filepath.Join(emptyRoot, "projects", encoded), 0o755); mkErr != nil {
		t.Fatalf("mkdir empty: %v", mkErr)
	}
	if _, _, err := FindLatestActiveJSONLInRoot(emptyRoot, workDir); err == nil {
		t.Errorf("空目录应报错（无 .jsonl）")
	}
}

// mustSize 返回文件大小（测试断言用，失败即 Fatal）。
func mustSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return info.Size()
}
