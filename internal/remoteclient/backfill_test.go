// backfill_test.go — ReplayTracker 单测：attach 单调消费/快照 gap 上报、
// live 重复丢弃/出洞缓冲、backfill frames 补洞冲刷、gap 变体前沿推进与追加
// 洞、restart boundary 占据 seq、未知 requestId 忽略、缓冲溢出保守重连。
package remoteclient

import (
	"testing"

	"amagi-codebox/internal/remote/contract"
)

func rf(sid contract.SessionID, seq contract.Seq, tag string) contract.OutputEvent {
	return contract.OutputEvent{Type: contract.ServerEventTypeOutput, SessionID: sid, Seq: seq, Chunk: b64(tag)}
}

func attached(rid contract.RequestID, sid contract.SessionID, history []contract.ReplayFrame, earliest, latest contract.Seq, hist contract.HistorySnapshot) contract.SessionAttachedEvent {
	if history == nil {
		history = []contract.ReplayFrame{}
	}
	return contract.SessionAttachedEvent{
		Type: contract.ServerEventTypeSessionAttached, RequestID: rid, APIVersion: contract.APIVersionV1,
		SessionID: sid, History: history, EarliestSeq: earliest, LatestSeq: latest,
		Snapshot: fiveLayer(contract.SessionStateRunning, contract.ControlStateYou, hist),
	}
}

// TestTrackerAttachedMonotonicAndGap：history 升序入账；快照 gap 原样上报。
func TestTrackerAttachedMonotonicAndGap(t *testing.T) {
	const sid = contract.SessionID("s1")
	tr := NewReplayTracker()
	if tr.AttachWithLastSeq() {
		t.Error("fresh tracker must not carry lastSeq")
	}
	out := tr.OnAttached(attached("r1", sid, []contract.ReplayFrame{rf(sid, 1, "a"), rf(sid, 2, "b")}, 1, 2,
		contract.HistorySnapshot{State: contract.HistoryStateContinuous}))
	if len(out.Frames) != 2 || out.Gap != nil {
		t.Fatalf("attach outcome = %d frames, gap=%v; want 2, nil", len(out.Frames), out.Gap)
	}
	if tr.LastSeq() != 2 {
		t.Fatalf("lastSeq = %d, want 2", tr.LastSeq())
	}
	// 快照 gap：[11,40]，earliest=41 对齐契约。
	gapSnap := contract.HistorySnapshot{State: contract.HistoryStateGap, Gap: &contract.GapRange{Code: contract.ErrorCodeHistoryGap, FromSeq: 11, ToSeq: 40}}
	tr2 := NewReplayTracker()
	out2 := tr2.OnAttached(attached("r2", sid, []contract.ReplayFrame{rf(sid, 41, "x")}, 41, 41, gapSnap))
	if out2.Gap == nil || out2.Gap.From != 11 || out2.Gap.To != 40 || out2.Gap.Source != GapFromAttached {
		t.Fatalf("gap notice = %+v, want attached [11,40]", out2.Gap)
	}
	if tr2.LastSeq() != 41 {
		t.Fatalf("lastSeq after gap attach = %d, want 41", tr2.LastSeq())
	}
}

// TestTrackerOutputDuplicateDropAndHoleBuffer：重复丢弃；出洞缓冲 + 待补区间；
// 补齐后冲刷。
func TestTrackerOutputDuplicateDropAndHoleBuffer(t *testing.T) {
	const sid = contract.SessionID("s2")
	tr := NewReplayTracker()
	tr.OnAttached(attached("r", sid, []contract.ReplayFrame{rf(sid, 1, "a")}, 1, 1,
		contract.HistorySnapshot{State: contract.HistoryStateContinuous}))
	// 重复：seq1 再来一次。
	if res := tr.OnOutput(rf(sid, 1, "dup")); !res.Duplicate || len(res.Frames) != 0 {
		t.Fatalf("duplicate outcome = %+v, want Duplicate", res)
	}
	// 出洞：seq4 先到（洞 [2,3]），缓冲。
	res := tr.OnOutput(rf(sid, 4, "d"))
	if !res.Buffered || res.NeedBackfill == nil || res.NeedBackfill.From != 2 || res.NeedBackfill.To != 3 {
		t.Fatalf("hole outcome = %+v, want buffered + backfill [2,3]", res)
	}
	// backfill frames 覆盖 [2,3]：入账后缓冲的 4 也一并冲刷。
	tr.RegisterBackfill("rb", 2, 3)
	bf := tr.OnBackfillResult(contract.BackfillFramesResultEvent{
		Type: contract.ServerEventTypeBackfillResult, RequestID: "rb", SessionID: sid,
		FromSeq: 2, ToSeq: 3, EarliestSeq: 2, LatestSeq: 4,
		Frames: []contract.ReplayFrame{rf(sid, 2, "b"), rf(sid, 3, "c")},
	})
	var seqs []contract.Seq
	for _, f := range bf.Frames {
		seqs = append(seqs, f.ReplaySeq())
	}
	if len(seqs) != 3 || seqs[0] != 2 || seqs[1] != 3 || seqs[2] != 4 {
		t.Fatalf("backfill delivered seqs = %v, want [2 3 4] (buffer flushed)", seqs)
	}
	if tr.LastSeq() != 4 {
		t.Fatalf("lastSeq = %d, want 4", tr.LastSeq())
	}
	if tr.InFlightBackfills() != 0 {
		t.Fatalf("in-flight = %d, want 0", tr.InFlightBackfills())
	}
}

// TestTrackerBackfillGapAdjudication：gap 变体 = 服务端裁定不可得——前沿推进
// 跨过区间、如实上报、后方缓冲帧随后冲刷。
func TestTrackerBackfillGapAdjudication(t *testing.T) {
	const sid = contract.SessionID("s3")
	tr := NewReplayTracker()
	tr.OnAttached(attached("r", sid, []contract.ReplayFrame{rf(sid, 1, "a")}, 1, 1,
		contract.HistorySnapshot{State: contract.HistoryStateContinuous}))
	// seq5 先到（洞 [2,4]）。
	tr.OnOutput(rf(sid, 5, "e"))
	tr.RegisterBackfill("rb", 2, 4)
	out := tr.OnBackfillResult(contract.BackfillGapResultEvent{
		Type: contract.ServerEventTypeBackfillResult, RequestID: "rb", SessionID: sid,
		FromSeq: 2, ToSeq: 4, EarliestSeq: 2, LatestSeq: 5,
		Gap: contract.GapRange{Code: contract.ErrorCodeHistoryGap, FromSeq: 2, ToSeq: 4},
	})
	if out.Gap == nil || out.Gap.From != 2 || out.Gap.To != 4 || out.Gap.Source != GapFromBackfill {
		t.Fatalf("gap notice = %+v, want backfill-adjudicated [2,4]", out.Gap)
	}
	if len(out.Frames) != 1 || out.Frames[0].ReplaySeq() != 5 {
		t.Fatalf("frames after gap = %v, want buffered seq5 flushed", out.Frames)
	}
	if tr.LastSeq() != 5 {
		t.Fatalf("lastSeq = %d, want 5 (advanced across adjudicated range)", tr.LastSeq())
	}
}

// TestTrackerRestartBoundaryOccupiesSeq：重启边界占据 seq；乱序边界同样缓冲。
func TestTrackerRestartBoundaryOccupiesSeq(t *testing.T) {
	const sid = contract.SessionID("s4")
	tr := NewReplayTracker()
	tr.OnAttached(attached("r", sid, []contract.ReplayFrame{rf(sid, 1, "a")}, 1, 1,
		contract.HistorySnapshot{State: contract.HistoryStateContinuous}))
	res := tr.OnRestartBoundary(contract.SessionRestartBoundaryEvent{
		Type: contract.ServerEventTypeSessionState, SessionID: sid, State: contract.SessionStateRunning,
		RestartBoundary: true, Seq: 2, OccurredAt: "2026-08-22T00:00:00Z",
	})
	if len(res.Frames) != 1 || res.Duplicate || res.Buffered {
		t.Fatalf("boundary outcome = %+v, want direct consume", res)
	}
	if tr.LastSeq() != 2 {
		t.Fatalf("lastSeq = %d, want 2", tr.LastSeq())
	}
}

// TestTrackerUnknownBackfillRequestIgnored：未登记的 requestId 被忽略（不冲
// 前沿、不投递）。
func TestTrackerUnknownBackfillRequestIgnored(t *testing.T) {
	const sid = contract.SessionID("s5")
	tr := NewReplayTracker()
	tr.OnAttached(attached("r", sid, nil, 0, 0, contract.HistorySnapshot{State: contract.HistoryStateContinuous}))
	out := tr.OnBackfillResult(contract.BackfillFramesResultEvent{
		Type: contract.ServerEventTypeBackfillResult, RequestID: "nope", SessionID: sid,
		FromSeq: 1, ToSeq: 1, EarliestSeq: 1, LatestSeq: 1,
		Frames: []contract.ReplayFrame{rf(sid, 1, "ghost")},
	})
	if !out.Unknown || len(out.Frames) != 0 || tr.LastSeq() != 0 {
		t.Fatalf("unknown-request outcome = %+v lastSeq=%d, want ignored", out, tr.LastSeq())
	}
}

// TestTrackerBufferOverflowForcesReconnect：乱序帧超过缓冲上限 → 保守重连。
func TestTrackerBufferOverflowForcesReconnect(t *testing.T) {
	const sid = contract.SessionID("s6")
	tr := NewReplayTracker()
	tr.OnAttached(attached("r", sid, []contract.ReplayFrame{rf(sid, 1, "a")}, 1, 1,
		contract.HistorySnapshot{State: contract.HistoryStateContinuous}))
	// 填满缓冲（seq3..，洞在 seq2）。
	for i := 0; i < maxBufferedFrames; i++ {
		tr.OnOutput(rf(sid, contract.Seq(3+i), "b"))
	}
	res := tr.OnOutput(rf(sid, contract.Seq(3+maxBufferedFrames), "overflow"))
	if !res.NeedReconnect {
		t.Fatalf("overflow outcome = %+v, want NeedReconnect", res)
	}
}

// TestTrackerAttachLastSeqNegotiation：重连 attach 仅在持有 replay frame 时
// 携带 lastSeq；携带后 history 为 seq>lastSeq 的增量。
func TestTrackerAttachLastSeqNegotiation(t *testing.T) {
	const sid = contract.SessionID("s7")
	tr := NewReplayTracker()
	tr.OnAttached(attached("r", sid, []contract.ReplayFrame{rf(sid, 3, "c")}, 3, 3,
		contract.HistorySnapshot{State: contract.HistoryStateContinuous}))
	if !tr.AttachWithLastSeq() || tr.LastSeq() != 3 {
		t.Fatalf("AttachWithLastSeq=%v lastSeq=%d, want true 3", tr.AttachWithLastSeq(), tr.LastSeq())
	}
	// 重连 history 覆盖 [4,5]。
	out := tr.OnAttached(attached("r2", sid, []contract.ReplayFrame{rf(sid, 4, "d"), rf(sid, 5, "e")}, 4, 5,
		contract.HistorySnapshot{State: contract.HistoryStateContinuous}))
	if len(out.Frames) != 2 || out.Frames[0].ReplaySeq() != 4 {
		t.Fatalf("reconnect attach frames = %v, want [4 5]", out.Frames)
	}
	// 旧帧重复（服务端防御性重放）：丢弃不回退。
	if res := tr.OnOutput(rf(sid, 4, "dup")); !res.Duplicate {
		t.Fatalf("stale frame outcome = %+v, want duplicate drop", res)
	}
	if tr.LastSeq() != 5 {
		t.Fatalf("lastSeq = %d, want 5", tr.LastSeq())
	}
}
