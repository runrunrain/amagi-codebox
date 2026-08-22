// backfill.go 历史回填协商 + timeline 前沿（蓝图 §6 流程 3、§8）：
//
// ReplayTracker 维护每会话的 seq 前沿（lastSeq = 已连续消费的最高 seq）：
//   - attach 协商：仅当持有 replay frame（lastSeq>0）时携带 lastSeq（omitted
//     与 0 语义有别，design §7.3）；session.attached 的 history 按 seq 升序
//     单调消费；snapshot.history.state=gap 时把服务端 GapRange 原样上报
//     （不吞不改），并由 conn 层发起 backfill 补洞。
//   - live output：seq==lastSeq+1 直投；seq<=lastSeq 重复丢弃；seq>lastSeq+1
//     出洞——先缓冲乱序帧并发起 backfill [lastSeq+1, seq-1]，洞补齐后按
//     seq 单调冲刷缓冲；洞被服务端裁定不可得（gap 变体）时前沿整体推进
//     跨过被裁定区间并如实上报 GapNotice（契约 ActionHint：continue-from-
//     latest 类决策交上层）。
//   - restart boundary 占据 seq（契约 §4.3.2），与 output 同规则消费。
//
// 缓冲上限：maxBufferedFrames 之外的乱序流视为服务端因果异常，返回
// NeedReconnect 让 conn 层保守重连（携带前沿游标重新对齐 retained window）。

package remoteclient

import (
	"sync"

	"amagi-codebox/internal/remote/contract"
)

// maxBufferedFrames 是乱序 output 缓冲上限（超出 → 保守重连）。
const maxBufferedFrames = 512

// seqRange 是闭区间 seq 范围。
type seqRange struct {
	From, To contract.Seq
}

// GapSource 标记 gap 的来源（attached 快照 / backfill 裁定）。
type GapSource string

const (
	GapFromAttached GapSource = "attached"
	GapFromBackfill GapSource = "backfill"
	GapFromLiveHole GapSource = "live"
)

// GapNotice 是如实上报给上层的缺口（不吞不改）。
type GapNotice struct {
	From, To contract.Seq
	Source   GapSource
}

// AttachOutcome 是 session.attached 的消费结论。
type AttachOutcome struct {
	// Frames 是本次新消费（含缓冲冲刷）的帧，升序；交 conn 层投递。
	Frames []contract.ReplayFrame
	// Gap 是服务端快照/推导出的未得区间（nil = 连续）。
	Gap *GapNotice
}

// OutputOutcome 是一条 live output 的消费结论。
type OutputOutcome struct {
	// Frames 是本条触发的投递（含洞补齐后的缓冲冲刷），升序。
	Frames []contract.ReplayFrame
	// NeedBackfill 非空 → conn 层应发送 backfill 请求补 [From,To]。
	NeedBackfill *seqRange
	// Duplicate 表示本条为重复帧（已消费过），静默丢弃。
	Duplicate bool
	// Buffered 表示本条因洞未补而进入缓冲（暂不投递）。
	Buffered bool
	// NeedReconnect 表示因果异常（重复回退/缓冲溢出），建议保守重连。
	NeedReconnect bool
}

// BackfillOutcome 是 backfill.result 的消费结论。
type BackfillOutcome struct {
	Frames []contract.ReplayFrame
	// Gap 是服务端裁定的不可得区间（如实上报，不吞不改）。
	Gap *GapNotice
	// NextHole 是冲刷后仍留存的下一段待补区间（conn 层续发 backfill）。
	NextHole *seqRange
	// Unknown 表示 requestId 不属于任何在途请求（迟到/错配），忽略。
	Unknown bool
}

// ReplayTracker 是每会话的 seq 前沿 + 乱序缓冲 + 在途 backfill 登记。
// 并发安全：conn 读泵与投递路径共用。
type ReplayTracker struct {
	mu       sync.Mutex
	lastSeq  contract.Seq
	earliest contract.Seq
	latest   contract.Seq
	buffered map[contract.Seq]contract.ReplayFrame
	pending  map[contract.RequestID]seqRange
}

// NewReplayTracker 构造空 tracker（lastSeq=0：尚无 replay frame）。
func NewReplayTracker() *ReplayTracker {
	return &ReplayTracker{buffered: make(map[contract.Seq]contract.ReplayFrame), pending: make(map[contract.RequestID]seqRange)}
}

// LastSeq 返回当前前沿（0 = 尚未持有任何 replay frame）。
func (r *ReplayTracker) LastSeq() contract.Seq {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastSeq
}

// AttachWithLastSeq 报告 attach 是否应携带 lastSeq（仅持有 replay frame 时）。
func (r *ReplayTracker) AttachWithLastSeq() bool {
	return r.LastSeq() > 0
}

// InFlightBackfills 返回在途 backfill 请求数（conn 层判定 degraded 投影）。
func (r *ReplayTracker) InFlightBackfills() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// OnAttached 消费 session.attached：history 升序入账，快照 gap 原样上报。
// 服务端保证 history 为 retained window 内的连续升序段；若仍出现内部跳洞
// （非一致生产者），按 live 出洞同样处理（缓冲 + NeedBackfill 洞区间）。
func (r *ReplayTracker) OnAttached(ev contract.SessionAttachedEvent) AttachOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.earliest, r.latest = ev.EarliestSeq, ev.LatestSeq

	// 锦定点：首连（lastSeq=0，attach 省畴 lastSeq）服务端从 earliest 起返
	// 全量 retained；reconnect 快照 gap 场景（契约 §4.3.5/§4.4：gap.ToSeq+1 ==
	// earliestSeq）首帧即 earliest。两者都从 history 首帧直接对齐前沿，不把
	// 首帧当乱序缓冲；reconnect 且快照 continuous 时严格从 lastSeq+1 消费，
	// 首帧跳前则按内部跳洞处理（防御非一致生产者）。
	anchor := r.lastSeq == 0 || (ev.Snapshot.History.State == contract.HistoryStateGap && ev.Snapshot.History.Gap != nil)

	var out AttachOutcome
	var hole *seqRange
	started := false
	for _, fr := range ev.History {
		seq := fr.ReplaySeq()
		switch {
		case seq <= r.lastSeq:
			continue // 重复（服务端 lastSeq 协商下不应出现，防御丢弃）
		case !started && anchor:
			started = true
			r.lastSeq = seq
			out.Frames = append(out.Frames, fr)
		case seq == r.lastSeq+1:
			started = true
			r.lastSeq = seq
			out.Frames = append(out.Frames, fr)
		default:
			started = true
			// 内部跳洞：登记洞区间并缓冲后续帧
			if hole == nil {
				h := r.lastSeq + 1
				hole = &seqRange{From: h, To: seq - 1}
			}
			r.buffered[seq] = fr
		}
	}
	// 快照 gap：服务端权威裁定的未得区间（attached gap.ToSeq+1 == earliest）。
	if ev.Snapshot.History.State == contract.HistoryStateGap && ev.Snapshot.History.Gap != nil {
		g := ev.Snapshot.History.Gap
		out.Gap = &GapNotice{From: g.FromSeq, To: g.ToSeq, Source: GapFromAttached}
	} else if hole != nil {
		out.Gap = &GapNotice{From: hole.From, To: hole.To, Source: GapFromAttached}
	}
	return out
}

// RegisterBackfill 登记一个在途 backfill 请求（requestId 关联）。登记的
// 生命周期绑定发出请求的那条连接：result 只会在同一连接上返回。
func (r *ReplayTracker) RegisterBackfill(rid contract.RequestID, from, to contract.Seq) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[rid] = seqRange{From: from, To: to}
}

// ResetPendingBackfills 清空全部在途登记（MA-2：新连接建立时调用——旧连接
// 孤儿 rid 的 result 不可能到达，残留会把 InFlightBackfills 恒抬 ≥1 而将
// 输入门永久锁在 degraded；未决缺口由 attach 快照 gap / live 洞路径在新
// 连接上重建请求）。
func (r *ReplayTracker) ResetPendingBackfills() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = make(map[contract.RequestID]seqRange)
}

// UnregisterBackfill 撤销单个在途登记（MA-2：backfill 帧写失败——result
// 不可能到达，同步撤销，不残留 degraded 投影）。
func (r *ReplayTracker) UnregisterBackfill(rid contract.RequestID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, rid)
}

// OnOutput 消费一条 live output（见文件头决策表）。
func (r *ReplayTracker) OnOutput(ev contract.OutputEvent) OutputOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.consumeReplayLocked(ev)
}

// consumeReplayLocked 是 output/restart boundary 共用的消费规则。
func (r *ReplayTracker) consumeReplayLocked(fr contract.ReplayFrame) OutputOutcome {
	var out OutputOutcome
	seq := fr.ReplaySeq()
	switch {
	case seq <= r.lastSeq:
		out.Duplicate = true
	case seq == r.lastSeq+1:
		r.lastSeq = seq
		out.Frames = append(out.Frames, fr)
		// 洞补齐后冲刷缓冲（若有）；若冲刷又停在洞上且后方仍有缓冲帧，
		// 继续登记下一段待补区间。
		out.Frames, out.NeedBackfill = r.flushLocked(out.Frames)
	case len(r.buffered) >= maxBufferedFrames:
		out.NeedReconnect = true
	default:
		from := r.lastSeq + 1
		r.buffered[seq] = fr
		out.Buffered = true
		out.NeedBackfill = &seqRange{From: from, To: seq - 1}
	}
	return out
}

// flushLocked 从前沿+1 起冲刷缓冲中连续的帧；若停在洞上且后方仍有缓冲帧，
// 返回下一段待补区间 [frontier+1, 最小缓冲 seq-1]。
func (r *ReplayTracker) flushLocked(delivered []contract.ReplayFrame) ([]contract.ReplayFrame, *seqRange) {
	for {
		next, ok := r.buffered[r.lastSeq+1]
		if !ok {
			break
		}
		delete(r.buffered, r.lastSeq+1)
		r.lastSeq++
		delivered = append(delivered, next)
	}
	if len(r.buffered) == 0 {
		return delivered, nil
	}
	// 后方仍有缓冲帧：找出最小 seq，登记下一洞。
	minSeq := contract.MaxSeqSafeInteger
	for s := range r.buffered {
		if s < minSeq {
			minSeq = s
		}
	}
	from := r.lastSeq + 1
	if minSeq <= from { // 防御：不应发生（否则上面已冲刷）
		return delivered, nil
	}
	return delivered, &seqRange{From: from, To: minSeq - 1}
}

// OnBackfillResult 消费 backfill.result：frames 变体按连续覆盖入账并冲刷
// 缓冲；gap 变体 = 服务端权威裁定区间不可得——前沿推进跨过区间、上报
// GapNotice（不吞不改）；requestId 未登记 → Unknown 忽略。
func (r *ReplayTracker) OnBackfillResult(ev contract.BackfillResultEvent) BackfillOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out BackfillOutcome
	var rid contract.RequestID
	switch v := ev.(type) {
	case contract.BackfillFramesResultEvent:
		rid = v.RequestID
	case contract.BackfillGapResultEvent:
		rid = v.RequestID
	}
	req, known := r.pending[rid]
	if !known {
		out.Unknown = true // 迟到/错配：不属于任何在途请求，忽略
		return out
	}
	delete(r.pending, rid)
	var gotFrom, gotTo contract.Seq
	switch v := ev.(type) {
	case contract.BackfillFramesResultEvent:
		gotFrom, gotTo = v.FromSeq, v.ToSeq
	case contract.BackfillGapResultEvent:
		gotFrom, gotTo = v.FromSeq, v.ToSeq
	}
	if gotFrom != req.From || gotTo != req.To { // 非一致生产者防御
		out.Unknown = true
		return out
	}
	switch v := ev.(type) {
	case contract.BackfillFramesResultEvent:
		for _, fr := range v.Frames {
			res := r.consumeReplayLocked(fr)
			out.Frames = append(out.Frames, res.Frames...)
		}
	case contract.BackfillGapResultEvent:
		out.Gap = &GapNotice{From: v.Gap.FromSeq, To: v.Gap.ToSeq, Source: GapFromBackfill}
		// 前沿推进跨过被裁定不可得的区间（契约：gap 覆盖完整请求区间）。
		if v.Gap.ToSeq > r.lastSeq {
			r.lastSeq = v.Gap.ToSeq
		}
		// 冲刷缓冲中已可连续的部分；后方仍留洞则登记下一段（由 conn 层续发）。
		out.Frames, out.NextHole = r.flushLocked(out.Frames)
	}
	return out
}

// OnRestartBoundary 消费 live restart boundary（占据 seq）。
func (r *ReplayTracker) OnRestartBoundary(ev contract.SessionRestartBoundaryEvent) OutputOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.consumeReplayLocked(ev)
}
