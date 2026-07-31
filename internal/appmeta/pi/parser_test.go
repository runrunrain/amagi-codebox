package pi

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestExtractUsageRecordsPiSample mirrors pi docs/session-format.md: a session
// header, a user message, two assistant turns (one carrying cost), and noise
// lines (toolResult, model_change) that must be ignored as non-assistant.
func TestExtractUsageRecordsPiSample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "1734000000000_0a1b2c3d-4e5f.jsonl")
	content := []byte(
		// header: cwd + session uuid
		`{"type":"session","version":3,"id":"0a1b2c3d-4e5f-6072-8390-abcdef012345","timestamp":"2024-12-12T10:00:00.000Z","cwd":"/home/me/proj"}` + "\n" +
			// user message (skipped)
			`{"type":"message","id":"11aaaaaa","parentId":null,"timestamp":"2024-12-12T10:00:01.000Z","message":{"role":"user","content":"hi"}}` + "\n" +
			// assistant turn 1: full usage + cost (provider uses codebox amagi- namespace)
			`{"type":"message","id":"22bbbbbb","parentId":"11aaaaaa","timestamp":"2024-12-12T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Hi!"}],"provider":"amagi-glm","model":"glm-5","usage":{"input":1200,"output":80,"cacheRead":300,"cacheWrite":0,"totalTokens":1580,"cost":{"input":0.012,"output":0.0048,"cacheRead":0.0003,"cacheWrite":0,"total":0.0171}},"stopReason":"stop","timestamp":1734000002000}}` + "\n" +
			// toolResult message without usage (skipped — no nested LLM work)
			`{"type":"message","id":"33cccccc","parentId":"22bbbbbb","timestamp":"2024-12-12T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false}}` + "\n" +
			// model_change entry (skipped — not a message)
			`{"type":"model_change","id":"44dddddd","parentId":"33cccccc","timestamp":"2024-12-12T10:00:05.000Z","provider":"openai","modelId":"gpt-4o"}` + "\n" +
			// assistant turn 2: different provider/model, zero cost (CostProvided must be false)
			`{"type":"message","id":"55eeeeee","parentId":"44dddddd","timestamp":"2024-12-12T10:00:06.000Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"provider":"anthropic","model":"claude-sonnet-4-5","usage":{"input":500,"output":40,"cacheRead":0,"cacheWrite":0,"totalTokens":540,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1734000006000}}` + "\n",
	)
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

	// turn 1: tokens + provider/model passthrough (amagi- prefix NOT stripped here)
	r1 := records[0]
	if r1.Model != "glm-5" {
		t.Errorf("model1 = %q, want glm-5", r1.Model)
	}
	if r1.Provider != "amagi-glm" {
		t.Errorf("provider1 = %q, want amagi-glm (raw passthrough)", r1.Provider)
	}
	if r1.InputTokens != 1200 {
		t.Errorf("input1 = %d, want 1200", r1.InputTokens)
	}
	if r1.OutputTokens != 80 {
		t.Errorf("output1 = %d, want 80", r1.OutputTokens)
	}
	if r1.CacheReadInputTokens != 300 {
		t.Errorf("cacheRead1 = %d, want 300", r1.CacheReadInputTokens)
	}
	if r1.CacheCreationInputTokens != 0 {
		t.Errorf("cacheWrite1 = %d, want 0", r1.CacheCreationInputTokens)
	}
	if !r1.CostProvided {
		t.Errorf("costProvided1 = false, want true (cost.total > 0)")
	}
	if r1.NativeCost != 17100 { // 0.0171 * 1e6
		t.Errorf("nativeCost1 = %d, want 17100", r1.NativeCost)
	}
	// P1-6: pi native cost is USD — must be tagged explicitly so the usage service
	// never re-infers a domestic currency from the amagi-glm provider.
	if r1.CurrencyCode != "USD" {
		t.Errorf("currencyCode1 = %q, want USD (pi native cost)", r1.CurrencyCode)
	}
	// timestamp from message.timestamp (Unix ms)
	want1 := time.UnixMilli(1734000002000).UTC()
	if !r1.OccurredAt.Equal(want1) {
		t.Errorf("occurredAt1 = %v, want %v", r1.OccurredAt, want1)
	}
	// project dir from header.cwd
	if r1.ProjectDir != "/home/me/proj" {
		t.Errorf("projectDir1 = %q, want /home/me/proj", r1.ProjectDir)
	}
	// session id from header.id
	if r1.SessionID != "0a1b2c3d-4e5f-6072-8390-abcdef012345" {
		t.Errorf("sessionID1 = %q, want header uuid", r1.SessionID)
	}
	// dedup key: pi: prefix + lineageRoot+entry scoped
	if r1.DedupKey == "" || r1.DedupKey[:3] != "pi:" {
		t.Errorf("dedup1 = %q, want pi: prefix", r1.DedupKey)
	}
	if r1.DedupKey == records[1].DedupKey {
		t.Errorf("dedup keys must differ across entries")
	}

	// turn 2: zero cost => CostProvided false, no currency (caller estimates)
	r2 := records[1]
	if r2.Provider != "anthropic" {
		t.Errorf("provider2 = %q, want anthropic", r2.Provider)
	}
	if r2.CostProvided {
		t.Errorf("costProvided2 = true, want false (cost.total == 0)")
	}
	if r2.NativeCost != 0 {
		t.Errorf("nativeCost2 = %d, want 0", r2.NativeCost)
	}
	if r2.CurrencyCode != "" {
		t.Errorf("currencyCode2 = %q, want empty (no native cost)", r2.CurrencyCode)
	}

	// lastOffset must equal file size (every line ends with \n)
	info, _ := os.Stat(path)
	if lastOffset != info.Size() {
		t.Errorf("lastOffset = %d, want file size %d", lastOffset, info.Size())
	}

	// resumable: re-reading from lastOffset yields nothing new
	again, _, err := ExtractUsageRecords(path, lastOffset)
	if err != nil {
		t.Fatalf("re-Extract: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("after lastOffset got %d records, want 0", len(again))
	}
}

// TestExtractUsageRecordsPiIncrementalSessionContext (P1-3) verifies that an
// incremental resume (startOffset>0) still resolves SessionID and ProjectDir from
// the header instead of degrading to file-basename / encoded-dir fallbacks.
func TestExtractUsageRecordsPiIncrementalSessionContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	first := []byte(`{"type":"session","version":3,"id":"uuid-1","timestamp":"2024-12-12T10:00:00.000Z","cwd":"/proj-alpha"}` + "\n" +
		`{"type":"message","id":"aa000001","parentId":null,"timestamp":"2024-12-12T10:00:01.000Z","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":10,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":11,"cost":{"total":0}},"timestamp":1734000001000}}` + "\n")
	if err := os.WriteFile(path, first, 0o600); err != nil {
		t.Fatalf("write first chunk: %v", err)
	}

	recs, off, err := ExtractUsageRecords(path, 0)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].SessionID != "uuid-1" || recs[0].ProjectDir != "/proj-alpha" {
		t.Fatalf("first batch context: session=%q project=%q, want uuid-1 / /proj-alpha",
			recs[0].SessionID, recs[0].ProjectDir)
	}

	// append a second assistant turn and resume from the committed offset.
	if err := os.WriteFile(path, append(first, []byte(
		`{"type":"message","id":"bb000002","parentId":"aa000001","timestamp":"2024-12-12T10:00:02.000Z","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":20,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":22,"cost":{"total":0}},"timestamp":1734000002000}}`+"\n")...), 0o600); err != nil {
		t.Fatalf("append second chunk: %v", err)
	}

	recs2, off2, err := ExtractUsageRecords(path, off)
	if err != nil {
		t.Fatalf("incremental read: %v", err)
	}
	if len(recs2) != 1 {
		t.Fatalf("expected 1 new record on incremental read, got %d", len(recs2))
	}
	// P1-3 regression assertion: the incremental record must keep the SAME
	// session/project as the first batch — not the file basename / encoded dir.
	if recs2[0].SessionID != "uuid-1" {
		t.Errorf("incremental SessionID = %q, want uuid-1 (header context preserved)", recs2[0].SessionID)
	}
	if recs2[0].ProjectDir != "/proj-alpha" {
		t.Errorf("incremental ProjectDir = %q, want /proj-alpha (header context preserved)", recs2[0].ProjectDir)
	}
	if recs2[0].InputTokens != 20 {
		t.Errorf("incremental record input = %d, want 20", recs2[0].InputTokens)
	}
	info, _ := os.Stat(path)
	if off2 != info.Size() {
		t.Errorf("off2 = %d, want %d", off2, info.Size())
	}
}

// TestExtractUsageRecordsPiBadJSONContinues verifies a malformed line is skipped.
func TestExtractUsageRecordsPiBadJSONContinues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mix.jsonl")
	content := []byte(`{"type":"session","version":3,"id":"u","timestamp":"2024-12-12T10:00:00.000Z","cwd":"/p"}` + "\n" +
		`{not valid json}` + "\n" +
		`{"type":"message","id":"aa000001","parentId":null,"timestamp":"2024-12-12T10:00:01.000Z","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":5,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":6,"cost":{"total":0}},"timestamp":1734000001000}}` + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	recs, _, err := ExtractUsageRecords(path, 0)
	if err != nil {
		t.Fatalf("ExtractUsageRecords: %v", err)
	}
	if len(recs) != 1 {
		t.Errorf("expected 1 record (bad line skipped), got %d", len(recs))
	}
}

// TestExtractUsageRecordsPiForkNoDoubleCount (P1-4) verifies that history copied
// by pi's fork/clone into a new file dedups against the original (same lineage
// root), while genuinely-new entries in the fork are still counted.
func TestExtractUsageRecordsPiForkNoDoubleCount(t *testing.T) {
	dir := t.TempDir()
	origPath := filepath.Join(dir, "orig.jsonl")
	forkPath := filepath.Join(dir, "fork.jsonl")

	// original: one assistant turn e1.
	orig := []byte(`{"type":"session","version":3,"id":"orig-uuid","timestamp":"2024-12-12T10:00:00.000Z","cwd":"/a"}` + "\n" +
		`{"type":"message","id":"aaaaaaaa","parentId":null,"timestamp":"2024-12-12T10:00:01.000Z","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":10,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":11,"cost":{"total":0}},"timestamp":1734000001000}}` + "\n")
	if err := os.WriteFile(origPath, orig, 0o600); err != nil {
		t.Fatalf("write orig: %v", err)
	}

	// fork: header points back at the original, then COPIES e1 verbatim (pi
	// forkFrom copies all non-header entries with the same entry ids) and adds a
	// brand-new turn e2.
	fork := []byte(`{"type":"session","version":3,"id":"fork-uuid","timestamp":"2024-12-12T11:00:00.000Z","cwd":"/a","parentSession":"` + origPath + `"}` + "\n" +
		`{"type":"message","id":"aaaaaaaa","parentId":null,"timestamp":"2024-12-12T10:00:01.000Z","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":10,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":11,"cost":{"total":0}},"timestamp":1734000001000}}` + "\n" +
		`{"type":"message","id":"bbbbbbbb","parentId":"aaaaaaaa","timestamp":"2024-12-12T11:00:02.000Z","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":40,"output":4,"cacheRead":0,"cacheWrite":0,"totalTokens":44,"cost":{"total":0}},"timestamp":1734000002000}}` + "\n")
	if err := os.WriteFile(forkPath, fork, 0o600); err != nil {
		t.Fatalf("write fork: %v", err)
	}

	origRecs, _, err := ExtractUsageRecords(origPath, 0)
	if err != nil {
		t.Fatalf("read orig: %v", err)
	}
	if len(origRecs) != 1 {
		t.Fatalf("orig: expected 1 record, got %d", len(origRecs))
	}
	forkRecs, _, err := ExtractUsageRecords(forkPath, 0)
	if err != nil {
		t.Fatalf("read fork: %v", err)
	}
	if len(forkRecs) != 2 {
		t.Fatalf("fork: expected 2 records (copied e1 + new e2), got %d", len(forkRecs))
	}

	// The copied e1 in the fork must share the ORIGINAL's dedup key (same
	// lineage root, same entry id) so it is not billed twice.
	copied := forkRecs[0]
	if copied.RawMessageID != "aaaaaaaa" {
		t.Fatalf("first fork record is %q, want copied entry aaaaaaaa", copied.RawMessageID)
	}
	if copied.DedupKey != origRecs[0].DedupKey {
		t.Errorf("fork copied entry dedup %q != orig %q (fork must dedup against source lineage)",
			copied.DedupKey, origRecs[0].DedupKey)
	}
	// The genuinely-new e2 gets its own key.
	newEntry := forkRecs[1]
	if newEntry.RawMessageID != "bbbbbbbb" {
		t.Fatalf("second fork record is %q, want new entry bbbbbbbb", newEntry.RawMessageID)
	}
	if newEntry.DedupKey == origRecs[0].DedupKey {
		t.Errorf("new fork entry must not collide with the copied entry")
	}
}

// TestExtractUsageRecordsPiDedupCollisionSafe (P1-4) verifies that the SAME 8-hex
// entry id in two UNRELATED sessions (no parentSession link) yields distinct
// dedup keys — i.e. dedup is not naively global, which would undercount.
// TestExtractUsageRecordsPiDedupCollisionSafe verifies that entries sharing a
// coincidental 8-hex id but differing in content (timestamp/usage) keep
// distinct dedup keys — i.e. dedup is content-scoped, not entry-id-global.
func TestExtractUsageRecordsPiDedupCollisionSafe(t *testing.T) {
	dir := t.TempDir()
	mk := func(name, entryID, ts string, input int) string {
		p := filepath.Join(dir, name)
		content := []byte(`{"type":"session","version":3,"id":"u","cwd":"/p"}` + "\n" +
			`{"type":"message","id":"` + entryID + `","parentId":null,"timestamp":"2024-12-12T10:00:01.000Z","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":` + strconv.Itoa(input) + `,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":1,"cost":{"total":0}},"timestamp":` + ts + `}}` + "\n")
		if err := os.WriteFile(p, content, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}
	// Same entry id, but different timestamp/usage → distinct records.
	p1 := mk("a.jsonl", "aaaaaaaa", "1734000001000", 1)
	p2 := mk("b.jsonl", "aaaaaaaa", "1734000002000", 7)
	r1, _, err := ExtractUsageRecords(p1, 0)
	if err != nil {
		t.Fatalf("read p1: %v", err)
	}
	r2, _, err := ExtractUsageRecords(p2, 0)
	if err != nil {
		t.Fatalf("read p2: %v", err)
	}
	if len(r1) != 1 || len(r2) != 1 {
		t.Fatalf("expected 1 record each, got %d / %d", len(r1), len(r2))
	}
	if r1[0].DedupKey == r2[0].DedupKey {
		t.Errorf("entries with a coincidental 8-hex id collision but distinct content must keep distinct dedup keys; both = %q", r1[0].DedupKey)
	}
	// re-reading the same file is stable.
	r1again, _, _ := ExtractUsageRecords(p1, 0)
	if r1again[0].DedupKey != r1[0].DedupKey {
		t.Errorf("dedup key must be stable across reads of the same file")
	}

	// Byte-identical entry content across files (the fork/clone shape) dedups
	// together even when no ancestor file is resolvable (Major-1).
	p3 := mk("c.jsonl", "aaaaaaaa", "1734000001000", 1)
	r3, _, err := ExtractUsageRecords(p3, 0)
	if err != nil {
		t.Fatalf("read p3: %v", err)
	}
	if r3[0].DedupKey != r1[0].DedupKey {
		t.Errorf("copied entry must share the original dedup key regardless of ancestor availability; got %q, want %q", r3[0].DedupKey, r1[0].DedupKey)
	}
}

// TestExtractUsageRecordsPiNonAssistantUsage (P1-5) verifies toolResult,
// compaction and branch_summary usages are all counted (pi includes them in
// session token/cost totals), each with a stable dedup key and USD native cost.
func TestExtractUsageRecordsPiNonAssistantUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rich.jsonl")
	content := []byte(`{"type":"session","version":3,"id":"sess","timestamp":"2024-12-12T10:00:00.000Z","cwd":"/p"}` + "\n" +
		// assistant turn (counted)
		`{"type":"message","id":"aa000001","parentId":null,"timestamp":"2024-12-12T10:00:01.000Z","message":{"role":"assistant","provider":"anthropic","model":"claude-x","usage":{"input":100,"output":10,"cacheRead":0,"cacheWrite":0,"totalTokens":110,"cost":{"total":0}},"timestamp":1734000001000}}` + "\n" +
		// toolResult WITHOUT usage (skipped)
		`{"type":"message","id":"bb000002","parentId":"aa000001","timestamp":"2024-12-12T10:00:02.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false}}` + "\n" +
		// toolResult WITH nested usage (counted — inner LLM work)
		`{"type":"message","id":"cc000003","parentId":"bb000002","timestamp":"2024-12-12T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"c2","toolName":"image-gen","content":[{"type":"text","text":"done"}],"isError":false,"usage":{"input":50,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":55,"cost":{"input":0.001,"output":0.0005,"cacheRead":0,"cacheWrite":0,"total":0.0015}}}}` + "\n" +
		// compaction WITH entry-level usage (counted)
		`{"type":"compaction","id":"dd000004","parentId":"cc000003","timestamp":"2024-12-12T10:10:00.000Z","summary":"Earlier...","tokensBefore":50000,"usage":{"input":200,"output":20,"cacheRead":0,"cacheWrite":0,"totalTokens":220,"cost":{"input":0.002,"output":0.001,"cacheRead":0,"cacheWrite":0,"total":0.003}}}` + "\n" +
		// branch_summary WITH entry-level usage (counted)
		`{"type":"branch_summary","id":"ee000005","parentId":"aa000001","timestamp":"2024-12-12T10:15:00.000Z","fromId":"dd000004","summary":"Branch explored...","usage":{"input":80,"output":8,"cacheRead":0,"cacheWrite":0,"totalTokens":88,"cost":{"input":0.0008,"output":0.0004,"cacheRead":0,"cacheWrite":0,"total":0.0012}}}` + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	recs, _, err := ExtractUsageRecords(path, 0)
	if err != nil {
		t.Fatalf("ExtractUsageRecords: %v", err)
	}
	// 1 assistant + 1 toolResult(with usage) + 1 compaction + 1 branch_summary = 4
	if len(recs) != 4 {
		t.Fatalf("expected 4 billable records, got %d", len(recs))
	}

	byID := make(map[string]UsageEventStub, len(recs))
	for _, r := range recs {
		byID[r.RawMessageID] = r
	}

	assistant := byID["aa000001"]
	if assistant.Model != "claude-x" || assistant.Provider != "anthropic" {
		t.Errorf("assistant attribution: model=%q provider=%q", assistant.Model, assistant.Provider)
	}

	tool := byID["cc000003"]
	if tool.InputTokens != 50 || tool.OutputTokens != 5 {
		t.Errorf("toolResult usage tokens: in=%d out=%d, want 50/5", tool.InputTokens, tool.OutputTokens)
	}
	// toolResult carries no provider/model — attribution unknown is honest.
	if tool.Provider != "" || tool.Model != "" {
		t.Errorf("toolResult should have empty provider/model, got provider=%q model=%q", tool.Provider, tool.Model)
	}
	if !tool.CostProvided || tool.CurrencyCode != "USD" || tool.NativeCost != 1500 {
		t.Errorf("toolResult cost: provided=%v currency=%q native=%d, want true/USD/1500", tool.CostProvided, tool.CurrencyCode, tool.NativeCost)
	}

	comp := byID["dd000004"]
	if comp.InputTokens != 200 || comp.OutputTokens != 20 {
		t.Errorf("compaction usage tokens: in=%d out=%d, want 200/20", comp.InputTokens, comp.OutputTokens)
	}
	if !comp.CostProvided || comp.NativeCost != 3000 {
		t.Errorf("compaction cost: provided=%v native=%d, want true/3000", comp.CostProvided, comp.NativeCost)
	}

	branch := byID["ee000005"]
	if branch.InputTokens != 80 {
		t.Errorf("branch_summary input = %d, want 80", branch.InputTokens)
	}
	if branch.OutputTokens != 8 {
		t.Errorf("branch_summary output = %d, want 8", branch.OutputTokens)
	}
	if branch.CacheCreationInputTokens != 0 {
		t.Errorf("branch_summary cacheWrite = %d, want 0", branch.CacheCreationInputTokens)
	}

	// All four dedup keys must be distinct.
	seen := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		if _, dup := seen[r.DedupKey]; dup {
			t.Errorf("dedup key collision on entry %s: %s", r.RawMessageID, r.DedupKey)
		}
		seen[r.DedupKey] = struct{}{}
	}
}

// TestExtractUsageRecordsPiTruncatedTail (P2-5) verifies that an incomplete
// trailing line (no newline) is NOT committed: its offset is preserved so the
// completed line is picked up on the next sync instead of being skipped.
func TestExtractUsageRecordsPiTruncatedTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grow.jsonl")
	header := `{"type":"session","version":3,"id":"u","timestamp":"2024-12-12T10:00:00.000Z","cwd":"/p"}` + "\n"
	turn1 := `{"type":"message","id":"aa000001","parentId":null,"timestamp":"2024-12-12T10:00:01.000Z","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":10,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":11,"cost":{"total":0}},"timestamp":1734000001000}}` + "\n"
	// A half-written second line (no terminating newline) — simulates a writer
	// mid-append. It is deliberately a prefix of a valid assistant entry.
	turn2Prefix := `{"type":"message","id":"bb000002","parentId":"aa000001","timestamp":"2024-12-12T10:00:02.000Z","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":20,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":22`
	if err := os.WriteFile(path, []byte(header+turn1+turn2Prefix), 0o600); err != nil {
		t.Fatalf("write partial: %v", err)
	}

	recs, off, err := ExtractUsageRecords(path, 0)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record (turn1 only; partial tail withheld), got %d", len(recs))
	}
	// committed offset must stop at turn1's newline — NOT include the partial tail.
	wantOff := int64(len(header + turn1))
	if off != wantOff {
		t.Fatalf("offset = %d, want %d (partial tail must not be committed)", off, wantOff)
	}
	info, _ := os.Stat(path)
	if off == info.Size() {
		t.Fatalf("offset must be < file size so the completed line is re-read next sync")
	}

	// The writer now finishes turn2 (append the rest + newline).
	turn2Rest := `,"cost":{"total":0}},"timestamp":1734000002000}}` + "\n"
	if err := os.WriteFile(path, []byte(header+turn1+turn2Prefix+turn2Rest), 0o600); err != nil {
		t.Fatalf("complete turn2: %v", err)
	}
	recs2, off2, err := ExtractUsageRecords(path, off)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(recs2) != 1 {
		t.Fatalf("expected 1 record on resume (completed turn2), got %d", len(recs2))
	}
	if recs2[0].InputTokens != 20 {
		t.Errorf("resumed turn2 input = %d, want 20", recs2[0].InputTokens)
	}
	info2, _ := os.Stat(path)
	if off2 != info2.Size() {
		t.Errorf("final offset = %d, want file size %d", off2, info2.Size())
	}
}
