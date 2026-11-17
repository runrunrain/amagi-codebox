package claudewebui

// tailer.go — Claude Code 会话转录 jsonl 的尾随器（design §5.2 tailer）。
//
// 生命周期与数据流：
//   - 周期（默认 500ms）os.Stat 当前转录文件，mtime/size 变化才真正读取
//     （mtime 去重——纯轮询不重读）；
//   - 增量解析用 internal/appmeta/claude.ReadTranscriptIncrement（offset
//     断点续传，追加不丢不重；单行损坏跳过）；
//   - 路径解析：ClaudeSessionID 非空 → SessionJSONLPathInRoot（锁定 sid，
//     方案 R）；空 → FindLatestActiveJSONLInRoot 回退（mtime 最新，方案 P）；
//   - 文件未出现（claude 尚未写首条）→ 空流继续等待（/api/info 已 ready，
//     语义 = 服务就绪而非会话有内容）；
//   - 截断（size < offset，/resume 重写或换文件）→ 缓冲全量重建并整体重发；
//   - 缓冲上限 Settings.HistoryLimit（默认 500），超限丢最旧并置
//     truncatedHead（粘性——缓冲一旦丢头即如实上报，design §5.2 history）；
//   - busy 派生（design §5.2）：最后有效记录是 human-user（等首响应）或存在
//     未配对 tool_use（工具执行中）→ true；无 pending tool_use 的 assistant
//     收尾 → false；无记录 → false。busy 只影响 UI，不做写入门控（D6）。
//
// 并发纪律：状态更新在 t.mu 内完成，事件 emit 在锁外（emit 进 WS hub 的
// publish 会再取 hub 锁；锁内回调会构成锁序耦合）。

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"amagi-codebox/internal/appmeta/claude"
	"amagi-codebox/internal/logging"
)

// wireRecord 是 TranscriptRecord 的 wire 投影（design §6.3；json 字段名冻结，
// 服务端不做 toolUse↔toolResult 配对——客户端按 id 配对成工具卡）。
type wireRecord struct {
	Kind        string           `json:"kind"`
	UUID        string           `json:"uuid"`
	IsHuman     bool             `json:"isHuman,omitempty"`
	Ts          string           `json:"ts,omitempty"`
	Text        string           `json:"text,omitempty"`
	Thinking    string           `json:"thinking,omitempty"`
	ToolUses    []wireToolUse    `json:"toolUses,omitempty"`
	ToolResults []wireToolResult `json:"toolResults,omitempty"`
	Usage       *wireUsage       `json:"usage,omitempty"`
	Model       string           `json:"model,omitempty"`
}

type wireToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

type wireToolResult struct {
	ID      string `json:"id"`
	IsError bool   `json:"isError,omitempty"`
	Content string `json:"content,omitempty"`
}

type wireUsage struct {
	In         int `json:"in"`
	Out        int `json:"out"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

func toWireRecord(rec claude.TranscriptRecord) wireRecord {
	w := wireRecord{
		Kind: rec.Kind, UUID: rec.UUID, IsHuman: rec.IsHuman, Ts: rec.Timestamp,
		Text: rec.Text, Thinking: rec.Thinking, Model: rec.Model,
	}
	if rec.Usage != nil {
		w.Usage = &wireUsage{In: rec.Usage.In, Out: rec.Usage.Out,
			CacheRead: rec.Usage.CacheRead, CacheWrite: rec.Usage.CacheWrite}
	}
	for _, tu := range rec.ToolUses {
		w.ToolUses = append(w.ToolUses, wireToolUse{ID: tu.ID, Name: tu.Name, Input: tu.Input})
	}
	for _, tr := range rec.ToolResults {
		w.ToolResults = append(w.ToolResults, wireToolResult{ID: tr.ID, IsError: tr.IsError, Content: tr.Content})
	}
	return w
}

func toWireRecords(recs []claude.TranscriptRecord) []wireRecord {
	out := make([]wireRecord, 0, len(recs))
	for _, rec := range recs {
		out = append(out, toWireRecord(rec))
	}
	return out
}

// transcriptEvent 是 kind=="transcript" 的事件载荷：items 为本批新记录
// （截断/换文件重建时为全量缓冲），cursor 为字节游标。
type transcriptEvent struct {
	Items  []wireRecord `json:"items"`
	Cursor int64        `json:"cursor"`
}

// busyEvent 是 kind=="busy" 的事件载荷（值变化才发）。
type busyEvent struct {
	Value bool `json:"value"`
}

// tailerSnapshot 是缓冲的一致性投影（/api/history 全量快照）。
type tailerSnapshot struct {
	Items         []wireRecord
	Cursor        int64
	Busy          bool
	Title         string
	TruncatedHead bool
}

// helloState 是 WS hello 事件所需的实时游标/忙闲（ws.go）。
type helloState struct {
	Cursor int64
	Busy   bool
}

// pendingEvent 是一轮轮询产生的待发事件（锁外按序 emit）。
type pendingEvent struct {
	kind  string
	event any
}

// tailer 尾随单个会话的转录文件并把增量推给 emit（→ WS hub）。
type tailer struct {
	cfg      PlaneConfig
	interval time.Duration
	cap      int
	log      *logging.Service
	emit     func(kind string, event any)

	mu            sync.Mutex
	items         []wireRecord
	cursor        int64
	truncatedHead bool
	busy          bool
	title         string
	path          string // 当前读的文件（方案 P 回退时可能切换）
	mtime         time.Time
	size          int64
	started       bool
	reads         int // 诊断/测试钩子：实际执行读的次数（mtime 去重观测量）
}

func newTailer(cfg PlaneConfig, set RuntimeSettings, log *logging.Service, emit func(kind string, event any)) *tailer {
	return &tailer{
		cfg: cfg, interval: set.PollInterval, cap: set.HistoryLimit,
		log: log, emit: emit,
	}
}

// run 阻塞尾随直到 ctx 取消（首启立即读一次）。
func (t *tailer) run(ctx context.Context) {
	for {
		for _, ev := range t.pollOnce() {
			t.emit(ev.kind, ev.event)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(t.interval):
		}
	}
}

// resolvePath 解析当前应读的转录文件路径。文件未出现/目录不存在不是错误：
// 返回空路径表示继续等待（design §5.2：server 起即 ready，转录可迟到）。
func (t *tailer) resolvePath() string {
	if t.cfg.ClaudeSessionID != "" {
		return claude.SessionJSONLPathInRoot(t.cfg.TranscriptRoot, t.cfg.WorkDir, t.cfg.ClaudeSessionID)
	}
	path, _, err := claude.FindLatestActiveJSONLInRoot(t.cfg.TranscriptRoot, t.cfg.WorkDir)
	if err != nil {
		return "" // 目录不存在 / 尚无 jsonl：等待
	}
	return path
}

// pollOnce 执行一轮「Stat → 变化才读 → 更新缓冲」，返回待发事件（锁外
// 由调用方按序 emit）。
func (t *tailer) pollOnce() []pendingEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pollLocked()
}

func (t *tailer) pollLocked() []pendingEvent {
	path := t.resolvePath()
	if path == "" {
		return nil // 文件未出现：空流等待
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil // 未出现或暂不可读：等待（重现后经 truncated/路径切换兜底）
	}
	// 方案 P 回退：目标文件切换（mtime 最新换人）→ 从 0 全量重建（两个文件
	// 的记录不可拼接）。
	pathSwitch := t.started && path != t.path
	if pathSwitch {
		t.cursor = 0
		t.path = path
	} else if t.started && !info.ModTime().After(t.mtime) && info.Size() == t.size {
		return nil // mtime/size 均未变：去重，不重读
	}
	t.reads++

	recs, newOffset, truncated, err := claude.ReadTranscriptIncrement(path, t.cursor)
	if err != nil {
		return nil // stat 与 open 间消失等瞬时态：下一轮重试
	}
	t.path = path
	t.mtime = info.ModTime()
	t.size = info.Size()
	t.cursor = newOffset
	t.started = true

	if truncated || pathSwitch {
		// 文件比游标短（/resume 重写）或目标文件切换：从 0 重读全量 →
		// 缓冲全量重建并整体重发（design §5.2/§5.1 truncated 语义）。
		t.rebuildLocked(recs)
		return []pendingEvent{
			{kind: "transcript", event: transcriptEvent{Items: t.copyItemsLocked(), Cursor: t.cursor}},
			{kind: "busy", event: busyEvent{Value: t.busy}},
		}
	}
	if len(recs) == 0 {
		return nil
	}
	t.appendLocked(recs)
	prevBusy := t.busy
	t.busy = deriveBusy(t.items)
	// title 粘性：会话标题取「首条 human 记录」，缓冲丢最旧不得回退成后续
	// 轮次的文本（design §5.2 history）。
	if t.title == "" {
		for _, rec := range recs {
			if rec.Kind == "user" && rec.IsHuman && rec.Text != "" {
				t.title = rec.Text
				break
			}
		}
	}
	events := []pendingEvent{
		{kind: "transcript", event: transcriptEvent{Items: toWireRecords(recs), Cursor: t.cursor}},
	}
	if t.busy != prevBusy {
		events = append(events, pendingEvent{kind: "busy", event: busyEvent{Value: t.busy}})
	}
	return events
}

// appendLocked 追加新记录并施缓冲上限（丢最旧置 truncatedHead 粘性标记）。
// 调用方必须持有 t.mu。
func (t *tailer) appendLocked(recs []claude.TranscriptRecord) {
	t.items = append(t.items, toWireRecords(recs)...)
	if len(t.items) > t.cap {
		t.items = append(t.items[:0:0], t.items[len(t.items)-t.cap:]...)
		t.truncatedHead = true
	}
}

// rebuildLocked 以全量记录重建缓冲（截断/换文件场景）；truncatedHead 按新
// 缓冲重新计算。调用方必须持有 t.mu。
func (t *tailer) rebuildLocked(recs []claude.TranscriptRecord) {
	t.items = nil
	t.truncatedHead = false
	t.appendLocked(recs)
	t.busy = deriveBusy(t.items)
	t.title = deriveTitle(t.items)
}

func (t *tailer) copyItemsLocked() []wireRecord {
	items := append(t.items[:0:0], t.items...)
	if items == nil {
		items = []wireRecord{}
	}
	return items
}

// snapshot 返回一致性全量快照（/api/history 用；items 为副本）。
func (t *tailer) snapshot() tailerSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return tailerSnapshot{
		Items: t.copyItemsLocked(), Cursor: t.cursor, Busy: t.busy,
		Title: t.title, TruncatedHead: t.truncatedHead,
	}
}

// helloSnapshot 返回 WS hello 所需的实时游标/忙闲。
func (t *tailer) helloSnapshot() helloState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return helloState{Cursor: t.cursor, Busy: t.busy}
}

// deriveBusy 实现 busy 启发式（design §5.2）：最后一条有效记录是
// human-user，或存在未配对 tool_use → true；否则 false（含无记录）。
// tool_use↔tool_result 配对按 id：assistant 行记 pending，user 行抵销。
func deriveBusy(items []wireRecord) bool {
	if len(items) == 0 {
		return false
	}
	pending := make(map[string]struct{})
	for i := range items {
		for _, tu := range items[i].ToolUses {
			pending[tu.ID] = struct{}{}
		}
		for _, tr := range items[i].ToolResults {
			delete(pending, tr.ID)
		}
	}
	last := items[len(items)-1]
	if last.Kind == "user" && last.IsHuman {
		return true
	}
	return len(pending) > 0
}

// deriveTitle 取首条 human 记录的文本（design §5.2 history：首条 human
// 记录直接得出）。
func deriveTitle(items []wireRecord) string {
	for i := range items {
		if items[i].Kind == "user" && items[i].IsHuman && items[i].Text != "" {
			return items[i].Text
		}
	}
	return ""
}
