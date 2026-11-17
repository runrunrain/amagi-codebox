package claudewebui

// tailer_test.go — 转录尾随器测试（design §5.2 tailer、测试计划 tailer_test.go
// 行）：真实 fixture 副本的增量不丢不重、mtime 去重、busy 启发式两分支、
// 文件未出现等待、截断全量重读、方案 P（sid 空回退 mtime 最新）。

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/appmeta/claude"
)

// eventLog 收集 tailer 发出的事件（类型保留以便断言）。
type eventLog struct {
	mu     sync.Mutex
	events []loggedEvent
}

type loggedEvent struct {
	kind  string
	event any
}

func (l *eventLog) log(kind string, event any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, loggedEvent{kind: kind, event: event})
}

func (l *eventLog) transcripts() []transcriptEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []transcriptEvent
	for _, e := range l.events {
		if e.kind == "transcript" {
			out = append(out, e.event.(transcriptEvent))
		}
	}
	return out
}

// runTailer 直接驱动 tailer（不经 HTTP 面；轮询周期测试专设）。
func runTailer(t *testing.T, cfg PlaneConfig, interval time.Duration) (*tailer, *eventLog) {
	t.Helper()
	el := &eventLog{}
	tl := newTailer(cfg, RuntimeSettings{PollInterval: interval, HistoryLimit: 500}, nil, el.log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go tl.run(ctx)
	return tl, el
}

// tailerReads 读取诊断计数（mtime 去重的观测量）。
func tailerReads(tl *tailer) int {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	return tl.reads
}

// tailerCfgFor 构造指向 root 下指定 sid 文件的 cfg。
func tailerCfgFor(root, sid string) PlaneConfig {
	return PlaneConfig{
		SessionID: "sess-t", ClaudeSessionID: sid,
		WorkDir: `C:\work\demo`, TranscriptRoot: root,
		Token: testToken,
	}
}

// --- 真实 fixture：增量不丢不重 + title/busy ------------------------------------

func TestTailerRealFixtureIncrement(t *testing.T) {
	root := t.TempDir()
	cfg := tailerCfgFor(root, "11111111-2222-3333-4444-555555555555")
	path := claude.SessionJSONLPathInRoot(cfg.TranscriptRoot, cfg.WorkDir, cfg.ClaudeSessionID)

	w := newTranscriptWriter(t, path)
	w.write(string(transcriptFixture(t)))

	tl, _ := runTailer(t, cfg, 15*time.Millisecond)
	if !waitFor(t, 2*time.Second, func() bool { return len(tl.snapshot().Items) == 9 }) {
		t.Fatalf("fixture 应解析出 9 条记录")
	}
	snap := tl.snapshot()
	st, _ := os.Stat(path)
	if snap.Cursor != st.Size() {
		t.Fatalf("cursor = %d, want 文件大小 %d", snap.Cursor, st.Size())
	}
	if snap.Title != "Please read the file notes.txt and reply with exactly one short sentence describing what it contains." {
		t.Fatalf("title = %q, want 首条 human 文本", snap.Title)
	}
	if snap.Busy {
		t.Fatalf("fixture 尾部为已配对收尾，busy 应为 false")
	}
	if snap.TruncatedHead {
		t.Fatalf("9 条 < 缓冲上限，truncatedHead 应为 false")
	}

	// 增量追加一条 human → 10 条；uuid 集合无重复（不丢不重）。
	w.write(`{"type":"user","uuid":"u-tail-append","timestamp":"2026-11-14T01:00:00.000Z","origin":{"kind":"human"},"message":{"role":"user","content":"appended turn"}}`)
	if !waitFor(t, 2*time.Second, func() bool { return len(tl.snapshot().Items) == 10 }) {
		t.Fatalf("追加后应 10 条记录")
	}
	snap = tl.snapshot()
	seen := make(map[string]bool)
	for _, item := range snap.Items {
		if item.UUID == "" || seen[item.UUID] {
			t.Fatalf("uuid 缺失或重复: %q", item.UUID)
		}
		seen[item.UUID] = true
	}
	if last := snap.Items[9]; last.UUID != "u-tail-append" || !last.IsHuman {
		t.Fatalf("追加记录错位: %+v", last)
	}
	if !snap.Busy {
		t.Fatalf("尾部 human-user 应 busy=true（等首响应分支）")
	}
	if snap.Cursor <= st.Size() {
		t.Fatalf("增量后 cursor 应前进: %d（原 %d）", snap.Cursor, st.Size())
	}
}

// --- mtime 去重 -----------------------------------------------------------------

func TestTailerMTimeDedup(t *testing.T) {
	root := t.TempDir()
	cfg := tailerCfgFor(root, "22222222-0000-0000-0000-000000000002")
	path := claude.SessionJSONLPathInRoot(cfg.TranscriptRoot, cfg.WorkDir, cfg.ClaudeSessionID)
	w := newTranscriptWriter(t, path)
	w.write(`{"type":"user","uuid":"u-d1","origin":{"kind":"human"},"message":{"role":"user","content":"q"}}`)

	const interval = 20 * time.Millisecond
	tl, _ := runTailer(t, cfg, interval)
	if !waitFor(t, 2*time.Second, func() bool { return len(tl.snapshot().Items) == 1 }) {
		t.Fatalf("首批记录未读到")
	}
	reads0 := tailerReads(tl)
	cursor0 := tl.snapshot().Cursor

	// 文件无变化：多轮轮询不得重读（读数与游标稳定）。
	time.Sleep(5 * interval)
	if reads1 := tailerReads(tl); reads1 != reads0 {
		t.Fatalf("无变化仍重读: reads %d → %d", reads0, reads1)
	}
	if c := tl.snapshot().Cursor; c != cursor0 {
		t.Fatalf("无变化游标漂移: %d → %d", cursor0, c)
	}

	// mtime 前进（内容不变也无妨——重读产出 0 记录但读数增加）。
	w.write(`{"type":"assistant","uuid":"a-d1","message":{"role":"assistant","model":"m","content":[{"type":"text","text":"a"}]}}`)
	if !waitFor(t, 2*time.Second, func() bool { return tailerReads(tl) > reads0 }) {
		t.Fatalf("mtime 变化后应触发读取")
	}
	if !waitFor(t, 2*time.Second, func() bool { return len(tl.snapshot().Items) == 2 }) {
		t.Fatalf("追加记录未读入")
	}
}

// --- busy 启发式两分支 ------------------------------------------------------------

func TestTailerBusyBranches(t *testing.T) {
	const (
		human     = `{"type":"user","uuid":"u-b-h","origin":{"kind":"human"},"message":{"role":"user","content":"hi"}}`
		asstText  = `{"type":"assistant","uuid":"a-b-t","message":{"role":"assistant","model":"m","content":[{"type":"text","text":"done"}]}}`
		asstTool  = `{"type":"assistant","uuid":"a-b-u","message":{"role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_b1","name":"Bash","input":{"command":"ls"}}]}}`
		userToolR = `{"type":"user","uuid":"u-b-r","origin":{"kind":"tool"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_b1","content":"out"}]}}`
	)
	cases := []struct {
		name  string
		lines []string
		busy  bool
	}{
		{"最后为 human-user（等首响应）", []string{human}, true},
		{"assistant 无 pending 收尾", []string{human, asstText}, false},
		{"存在未配对 tool_use（执行中）", []string{human, asstTool}, true},
		{"tool_use 已配对后收尾", []string{human, asstTool, userToolR, asstText}, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			sid := "33333333-0000-0000-0000-0000000000" + string(rune('0'+i))
			cfg := tailerCfgFor(root, sid)
			path := claude.SessionJSONLPathInRoot(root, cfg.WorkDir, sid)
			w := newTranscriptWriter(t, path)
			w.write(tc.lines...)

			tl, _ := runTailer(t, cfg, 15*time.Millisecond)
			if !waitFor(t, 2*time.Second, func() bool { return len(tl.snapshot().Items) == len(tc.lines) }) {
				t.Fatalf("记录未读完（%d/%d）", len(tl.snapshot().Items), len(tc.lines))
			}
			if got := tl.snapshot().Busy; got != tc.busy {
				t.Fatalf("busy = %v, want %v", got, tc.busy)
			}
		})
	}
}

// --- 文件未出现：等待 ---------------------------------------------------------------

func TestTailerWaitsForFile(t *testing.T) {
	root := t.TempDir()
	cfg := tailerCfgFor(root, "44444444-0000-0000-0000-000000000004")
	path := claude.SessionJSONLPathInRoot(root, cfg.WorkDir, cfg.ClaudeSessionID)

	const interval = 20 * time.Millisecond
	tl, _ := runTailer(t, cfg, interval)
	time.Sleep(5 * interval)
	snap := tl.snapshot()
	if len(snap.Items) != 0 || snap.Busy || snap.Cursor != 0 || snap.Title != "" {
		t.Fatalf("文件未出现应为空流: %+v", snap)
	}
	if reads := tailerReads(tl); reads != 0 {
		t.Fatalf("文件未出现不应执行读: reads=%d", reads)
	}

	w := newTranscriptWriter(t, path)
	w.write(
		`{"type":"user","uuid":"u-w1","origin":{"kind":"human"},"message":{"role":"user","content":"late"}}`,
		`{"type":"assistant","uuid":"a-w1","message":{"role":"assistant","model":"m","content":[{"type":"text","text":"ok"}]}}`,
	)
	if !waitFor(t, 2*time.Second, func() bool { return len(tl.snapshot().Items) == 2 }) {
		t.Fatalf("文件出现后应读入 2 条")
	}
}

// --- 截断：全量重读重发 ---------------------------------------------------------------

func TestTailerTruncatedRebuild(t *testing.T) {
	root := t.TempDir()
	cfg := tailerCfgFor(root, "55555555-0000-0000-0000-000000000005")
	path := claude.SessionJSONLPathInRoot(root, cfg.WorkDir, cfg.ClaudeSessionID)

	w := newTranscriptWriter(t, path)
	w.write(string(transcriptFixture(t)))
	tl, el := runTailer(t, cfg, 15*time.Millisecond)
	if !waitFor(t, 2*time.Second, func() bool { return len(tl.snapshot().Items) == 9 }) {
		t.Fatalf("fixture 首读应 9 条")
	}

	// 重写为更短的 2 行文件（size < offset → truncated → 从 0 全量重读）。
	newLines := `{"type":"user","uuid":"u-new-1","origin":{"kind":"human"},"message":{"role":"user","content":"rewritten"}}
{"type":"assistant","uuid":"a-new-1","message":{"role":"assistant","model":"m","content":[{"type":"text","text":"rebuilt"}]}}
`
	if err := os.WriteFile(path, []byte(newLines), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	w.mtime = w.mtime.Add(time.Second)
	if err := os.Chtimes(path, w.mtime, w.mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if !waitFor(t, 2*time.Second, func() bool { return len(tl.snapshot().Items) == 2 }) {
		t.Fatalf("截断后应全量重建为 2 条，实际 %d", len(tl.snapshot().Items))
	}
	snap := tl.snapshot()
	if snap.Items[0].UUID != "u-new-1" || snap.Items[1].UUID != "a-new-1" {
		t.Fatalf("重建内容错位: %s, %s", snap.Items[0].UUID, snap.Items[1].UUID)
	}
	if st, _ := os.Stat(path); snap.Cursor != st.Size() {
		t.Fatalf("重建后 cursor = %d, want %d", snap.Cursor, st.Size())
	}
	if snap.Busy {
		t.Fatalf("重建后尾部 assistant 收尾，busy 应为 false")
	}
	// 全量重发：最后一条 transcript 事件载荷即完整新缓冲。
	trs := el.transcripts()
	if len(trs) == 0 {
		t.Fatalf("未发出 transcript 事件")
	}
	last := trs[len(trs)-1]
	if len(last.Items) != 2 || last.Items[1].UUID != "a-new-1" {
		t.Fatalf("截断重发应为全量 2 条: %+v", last.Items)
	}
}

// --- 方案 P 回退：sid 空 → mtime 最新，文件切换全量重建 -------------------------------

func TestTailerLatestFallback(t *testing.T) {
	root := t.TempDir()
	cfg := tailerCfgFor(root, "") // ClaudeSessionID 空 → FindLatestActiveJSONLInRoot
	dir := filepath.Dir(claude.SessionJSONLPathInRoot(root, cfg.WorkDir, "x"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	oldW := newTranscriptWriterAt(t, filepath.Join(dir, "old-sess.jsonl"), time.Now().Add(-2*time.Hour))
	newW := newTranscriptWriterAt(t, filepath.Join(dir, "new-sess.jsonl"), time.Now().Add(-1*time.Hour))
	oldW.write(`{"type":"user","uuid":"u-old1","origin":{"kind":"human"},"message":{"role":"user","content":"old first"}}`)
	newW.write(`{"type":"user","uuid":"u-new1","origin":{"kind":"human"},"message":{"role":"user","content":"new first"}}`)

	tl, _ := runTailer(t, cfg, 15*time.Millisecond)
	if !waitFor(t, 2*time.Second, func() bool { return len(tl.snapshot().Items) == 1 }) {
		t.Fatalf("应尾随 mtime 最新文件")
	}
	snap := tl.snapshot()
	if snap.Items[0].UUID != "u-new1" {
		t.Fatalf("应选中 new-sess.jsonl: %+v", snap.Items[0])
	}
	if snap.Title != "new first" {
		t.Fatalf("title = %q, want %q", snap.Title, "new first")
	}

	// old 文件更新（mtime 越过 new）→ 切换目标，全量重建为 old 的 2 条。
	// 用基准 mtime 更新的 writer 追加（原基准晚于 new 一小时，+1s 追不上）。
	oldW2 := newTranscriptWriterAt(t, filepath.Join(dir, "old-sess.jsonl"), time.Now())
	oldW2.write(`{"type":"user","uuid":"u-old2","origin":{"kind":"human"},"message":{"role":"user","content":"old second"}}`)
	if !waitFor(t, 2*time.Second, func() bool {
		s := tl.snapshot()
		return len(s.Items) == 2 && s.Items[0].UUID == "u-old1" && s.Items[1].UUID == "u-old2"
	}) {
		t.Fatalf("文件切换后应全量重建 old-sess 的 2 条，实际 %+v", tl.snapshot().Items)
	}
}
