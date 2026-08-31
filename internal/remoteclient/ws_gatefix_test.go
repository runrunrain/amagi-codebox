// ws_gatefix_test.go — RC3-GATE 审核修复批次（diting MA-1/MA-2/MI-1/MI-2/MI-3）
// 回归锚点：
//
//	· MA-1：输入在途被 control.forbidden 冻结队首后，重新取得控制权的新输入
//	  必须实际发出（冻结头不阻塞 FIFO，对齐 mobile inputOutbox.ts）；
//	· MA-2：backfill 在途断线 → 重连后孤儿登记被清理，输入门不再永久
//	  degraded；backfill 写帧失败同步撤销登记；
//	· MI-1：rc:control-state 携带契约 deviceName（服务端事件与 attach 快照
//	  投影两处）；
//	· MI-2：对端停滞（不读、不回）时数据帧写有界——write deadline 到期按
//	  写失败处理，outbox.mu/writeMu 不被无限拖住；
//	· MI-3：凭据清除后（宿主视图被丢弃）孤儿会话的拨号判终态退出，不再
//	  以已清空凭据无限重连。
package remoteclient

import (
	"net"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// countConnState 统计指定 state 的 rc:conn-state 事件数。
func countConnState(events []emittedEvent, state string) int {
	n := 0
	for _, ev := range events {
		if ev.Name != EventConnState {
			continue
		}
		if m, ok := ev.Payload.(map[string]any); ok && m["state"] == state {
			n++
		}
	}
	return n
}

// gateReopenedAfterReadonly 报告连接状态是否经历过 readonly 后重新回到
// attached（控制权丢失→重取的完整时序）。
func gateReopenedAfterReadonly(events []emittedEvent) bool {
	seenReadonly := false
	for _, ev := range events {
		if ev.Name != EventConnState {
			continue
		}
		m, ok := ev.Payload.(map[string]any)
		if !ok {
			continue
		}
		switch m["state"] {
		case string(ConnReadonly):
			seenReadonly = true
		case string(ConnAttached):
			if seenReadonly {
				return true
			}
		}
	}
	return false
}

// controlStateEvents 收集 rc:control-state 事件载荷。
func controlStateEvents(events []emittedEvent) []map[string]any {
	var out []map[string]any
	for _, ev := range events {
		if ev.Name != EventControlState {
			continue
		}
		if m, ok := ev.Payload.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// MA-1：冻结头不阻塞 FIFO（接管瞬间在途输入被 fence → 重取控制权后再输入）
// ---------------------------------------------------------------------------

// TestTerminalFrozenHeadDoesNotBlockResendAfterReacquire（MA-1 回归）：在途输入
// 被服务端 fence（control.forbidden）冻结为 halted 头后，control.state(you)
// 重开输入门；此时的新输入必须真正发出并结算——而不是静默排在冻结头后。
func TestTerminalFrozenHeadDoesNotBlockResendAfterReacquire(t *testing.T) {
	const sid = contract.SessionID("sess-ma1")
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		serveAttach(t, c, sid, nil, 0, 0, contract.ControlStateYou, true)
		for {
			f := c.readFrame(t, "input")
			in, ok := f.(contract.InputFrame)
			if !ok {
				continue
			}
			if got := c.inputFrames(); len(got) == 1 {
				// 接管瞬间：在途输入被 fence（不 ack），控制权转 other。
				c.writeEvent(t, contract.ErrorEvent{
					Type: contract.ServerEventTypeError, Code: contract.ErrorCodeControlForbidden,
					Layer: contract.ErrorLayerControl, Message: "not controller", ActionHint: contract.ActionHintRetry,
				})
				other := "Desktop Host"
				c.writeEvent(t, contract.ControlStateEvent{
					Type: contract.ServerEventTypeControlState, SessionID: sid,
					State: contract.ControlStateOther, DeviceName: &other, Reason: "desktop-take",
					OccurredAt: "2026-08-22T00:00:00Z",
				})
				// 用户重新 acquire 成功：control.state(you) 重开输入门。
				c.writeEvent(t, contract.ControlStateEvent{
					Type: contract.ServerEventTypeControlState, SessionID: sid,
					State: contract.ControlStateYou, Reason: "rest-acquire",
					OccurredAt: "2026-08-22T00:00:01Z",
				})
				continue
			}
			// 第二条输入（冻结头之后的新输入）必须真正到达：ack 并结束。
			c.writeEvent(t, contract.InputAckEvent{
				Type: contract.ServerEventTypeInputAck, RequestID: in.RequestID, SessionID: sid, ID: in.ID,
			})
			return
		}
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-ma1", em, testTerminalCfg())
	defer m.DetachAll()
	s, err := m.Attach(string(sid))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	em.waitFor(t, "attached", hasConnState(string(ConnAttached)), 2*time.Second)

	// 在途输入（将被 fence 冻结，永不结算）。
	if err := s.SendInput("typed"); err != nil {
		t.Fatalf("SendInput in-flight: %v", err)
	}
	// 等待 fence + takeover + 重取控制权的完整时序（门重新 attached）。
	em.waitFor(t, "gate reopened after readonly", gateReopenedAfterReadonly, 3*time.Second)

	// 冻结头之后的新输入：Accept 成功且必须实际发出（MA-1 核心断言）。
	if err := s.SendInput("after"); err != nil {
		t.Fatalf("SendInput after reacquire: %v", err)
	}
	em.waitFor(t, "second input reaches server (halted head must not block FIFO)", func([]emittedEvent) bool {
		return len(host.conn(0).inputFrames()) >= 2
	}, 3*time.Second)
	inputs := host.conn(0).inputFrames()
	if want := b64("after"); inputs[len(inputs)-1].Data != want {
		t.Errorf("second input data = %q, want %q", inputs[len(inputs)-1].Data, want)
	}
	// 冻结头保留迟到 ACK 资格：结算后仅剩 halted 一项未决。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && s.outbox.PendingCount() != 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if n := s.outbox.PendingCount(); n != 1 {
		t.Fatalf("outbox pending after ack = %d, want 1 (frozen head kept, second settled)", n)
	}
	_ = s.Detach()
}

// ---------------------------------------------------------------------------
// MA-2：backfill 在途登记生命周期绑定连接
// ---------------------------------------------------------------------------

// TestTerminalBackfillOrphanAcrossReconnectReopensInput（MA-2 回归）：backfill
// 请求在途时连接断开（result 不可能到达）→ 重连后孤儿登记被清理，快照
// continuous 无洞时输入门回到 attached，SendInput 可用；而非永久 degraded。
func TestTerminalBackfillOrphanAcrossReconnectReopensInput(t *testing.T) {
	const sid = contract.SessionID("sess-ma2")
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		switch idx {
		case 0:
			// 首连：消费 seq 1..2 后异常断开。
			serveAttach(t, c, sid, []contract.ReplayFrame{outputFrame(sid, 1, "a"), outputFrame(sid, 2, "b")}, 1, 2, contract.ControlStateYou, true)
			_ = c.conn.Close()
		case 1:
			// 重连：lastSeq=2，快照 history 从 10 起 → 内部洞 [3,9] → 客户端发
			// backfill[3,9]；在 result 到达前再次异常断开（在途孤儿登记）。
			af := serveAttach(t, c, sid, []contract.ReplayFrame{outputFrame(sid, 10, "j")}, 10, 10, contract.ControlStateYou, true)
			if af.LastSeq == nil || *af.LastSeq != 2 {
				t.Errorf("conn1 attach lastSeq = %v, want 2", af.LastSeq)
			}
			if f, ok := c.tryReadFrame(t, time.Second); ok {
				if bf, ok := f.(contract.BackfillFrame); ok && (bf.FromSeq != 3 || bf.ToSeq != 9) {
					t.Errorf("backfill range = [%d,%d], want [3,9]", bf.FromSeq, bf.ToSeq)
				}
			}
			_ = c.conn.Close() // 不回 result：在途 rid 成孤儿
		case 2:
			// 再重连：快照 continuous 补齐 3..10，无新洞——孤儿登记必须已被
			// 清理（否则 InFlightBackfills 恒 ≥1 → 永久 degraded 锁输入）。
			hist := make([]contract.ReplayFrame, 0, 8)
			for seq := contract.Seq(3); seq <= 10; seq++ {
				hist = append(hist, outputFrame(sid, seq, "h"))
			}
			serveAttach(t, c, sid, hist, 3, 10, contract.ControlStateYou, true)
			for {
				f := c.readFrame(t, "input")
				if in, ok := f.(contract.InputFrame); ok {
					c.writeEvent(t, contract.InputAckEvent{
						Type: contract.ServerEventTypeInputAck, RequestID: in.RequestID, SessionID: sid, ID: in.ID,
					})
					return
				}
			}
		default:
			_, _, _ = c.conn.ReadMessage()
		}
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-ma2", em, testTerminalCfg())
	defer m.DetachAll()
	s, err := m.Attach(string(sid))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// 走完 首连→断开→重连（在途孤儿）→再断开→再重连 三条连接。
	em.waitFor(t, "third connection", func([]emittedEvent) bool { return host.connCount() >= 3 }, 6*time.Second)
	// 门重开判定必须看「当前状态投影」而非历史事件：历史里的 ConnAttached
	//（首连）会立即满足谓词，而 connCount 在宿主 accept 时即 +1，conn3 的
	// dial→attach 仍在途；负载下 SendInput 会与 attach 竞态误报（now connecting）。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && s.State() != ConnAttached {
		time.Sleep(5 * time.Millisecond)
	}
	if st := s.State(); st != ConnAttached {
		t.Fatalf("conn state after reconnect = %s, want %s (input gate must not stay degraded)", st, ConnAttached)
	}
	// MA-2 核心断言：重连后输入门不再永久 degraded，输入可用且到达服务端。
	if err := s.SendInput("ok"); err != nil {
		t.Fatalf("SendInput after reconnect = %v, want nil (input gate must not stay degraded)", err)
	}
	if n := s.tracker.InFlightBackfills(); n != 0 {
		t.Fatalf("in-flight backfills after reconnect = %d, want 0 (orphan registration cleared)", n)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && s.outbox.PendingCount() != 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if n := s.outbox.PendingCount(); n != 0 {
		t.Fatalf("outbox pending after ack = %d, want 0", n)
	}
	_ = s.Detach()
}

// TestRequestBackfillUnregistersOnWriteFailure（MA-2 回归）：backfill 帧写失败
// （连接已不可写）时同步撤销在途登记——result 不可能到达，不得残留 degraded
// 投影。
func TestRequestBackfillUnregistersOnWriteFailure(t *testing.T) {
	s := &TerminalSession{tracker: NewReplayTracker()} // 无 ws：writeFrame 必失败
	if err := s.requestBackfill(1, 5); err == nil {
		t.Fatal("requestBackfill without a live connection must fail")
	}
	if n := s.tracker.InFlightBackfills(); n != 0 {
		t.Fatalf("in-flight after write failure = %d, want 0 (registration withdrawn)", n)
	}
}

// ---------------------------------------------------------------------------
// MI-1：rc:control-state 携带契约 deviceName
// ---------------------------------------------------------------------------

// TestTerminalControlStateCarriesDeviceName（MI-1 回归）：attach 快照投影与
// 服务端 control.state 事件的 emit 都携带 deviceName（state=other 时契约
// REQUIRED）；state=you 时不携带（契约 MUST omit）。
func TestTerminalControlStateCarriesDeviceName(t *testing.T) {
	const sid = contract.SessionID("sess-mi1")
	desktop := "桌面主机"
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		f := c.readFrame(t, "attach")
		af, ok := f.(contract.AttachFrame)
		if !ok {
			t.Fatalf("first frame is %T, want attach", f)
		}
		ev := attachedEvent(af.RequestID, sid, []contract.ReplayFrame{}, 0, 0, contract.ControlStateOther, true)
		ev.Snapshot.Control.DeviceName = &desktop
		c.writeEvent(t, ev)
		phone := "Pixel 9"
		c.writeEvent(t, contract.ControlStateEvent{
			Type: contract.ServerEventTypeControlState, SessionID: sid,
			State: contract.ControlStateOther, DeviceName: &phone, Reason: "device-take",
			OccurredAt: "2026-08-22T00:00:00Z",
		})
		c.writeEvent(t, contract.ControlStateEvent{
			Type: contract.ServerEventTypeControlState, SessionID: sid,
			State: contract.ControlStateYou, Reason: "rest-acquire",
			OccurredAt: "2026-08-22T00:00:01Z",
		})
		_, _, _ = c.conn.ReadMessage()
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-mi1", em, testTerminalCfg())
	defer m.DetachAll()
	if _, err := m.Attach(string(sid)); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	em.waitFor(t, "control.state(you) event", func(events []emittedEvent) bool {
		for _, p := range controlStateEvents(events) {
			if p["reason"] == "rest-acquire" {
				return true
			}
		}
		return false
	}, 2*time.Second)

	byReason := map[string]map[string]any{}
	for _, p := range controlStateEvents(em.snapshot()) {
		byReason[p["reason"].(string)] = p
	}
	snap := byReason["attach-snapshot"]
	if snap == nil {
		t.Fatalf("attach-snapshot control-state event missing; events=%v", controlStateEvents(em.snapshot()))
	}
	if snap["deviceName"] != desktop {
		t.Errorf("attach-snapshot deviceName = %v, want %q (control=other snapshot projection)", snap["deviceName"], desktop)
	}
	take := byReason["device-take"]
	if take == nil {
		t.Fatal("device-take control.state event missing")
	}
	if take["deviceName"] != "Pixel 9" {
		t.Errorf("control.state(other) deviceName = %v, want %q", take["deviceName"], "Pixel 9")
	}
	you := byReason["rest-acquire"]
	if you == nil {
		t.Fatal("rest-acquire control.state event missing")
	}
	if _, present := you["deviceName"]; present {
		t.Errorf("control.state(you) must omit deviceName, got %v", you["deviceName"])
	}
}

// ---------------------------------------------------------------------------
// MI-2：数据帧写超时
// ---------------------------------------------------------------------------

// TestTerminalWriteDeadlineBoundsStalledWrite（MI-2 回归）：宿主 attach 后停滞
// （缩小接收缓冲、永不读取）——向停滞连接持续写大帧直到 TCP 发送缓冲饱和，
// 阻塞的 WriteMessage 必须在 FrameWriteTimeout 内按写失败返回（而非无限
// 拖住 writeMu）；outbox 首轮 wire attempt 同样有界。修复前该写会无限阻塞。
func TestTerminalWriteDeadlineBoundsStalledWrite(t *testing.T) {
	const sid = contract.SessionID("sess-mi2")
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		serveAttach(t, c, sid, nil, 0, 0, contract.ControlStateYou, true)
		// 停滞宿主：缩小接收缓冲后永不读取（对端停滞 + TCP 缓冲逐步饱和）。
		if tcp, ok := c.conn.NetConn().(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(2048)
		}
		time.Sleep(8 * time.Second)
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	cfg := testTerminalCfg()
	cfg.FrameWriteTimeout = 250 * time.Millisecond
	m := NewTerminalManager(tr, "host-mi2", em, cfg)
	defer m.DetachAll()
	s, err := m.Attach(string(sid))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	em.waitFor(t, "attached", hasConnState(string(ConnAttached)), 2*time.Second)

	// 持续写 ~58KB 帧直到 TCP 发送缓冲饱和（loopback 实测 ~650KB 起阻塞）：
	// 饱和后某一轮 WriteMessage 阻塞——修复前无限阻塞，修复后被 deadline
	// 有界为写失败。
	rid, ridErr := canonicalRequestID(randomBytes)
	mid, midErr := canonicalMessageID(randomBytes)
	if ridErr != nil || midErr != nil {
		t.Fatalf("canonical ids: %v %v", ridErr, midErr)
	}
	big := strings.Repeat("x", 58_600)
	bounded := time.Duration(0)
	for i := 0; i < 24 && bounded < 150*time.Millisecond; i++ {
		ch := make(chan error, 1)
		t0 := time.Now()
		go func() {
			ch <- s.writeFrame(contract.InputFrame{Type: contract.ClientFrameTypeInput, RequestID: rid, ID: mid, Data: big})
		}()
		select {
		case err := <-ch:
			d := time.Since(t0)
			if d > bounded {
				bounded = d
			}
			if d >= 150*time.Millisecond && err == nil {
				t.Fatalf("write stalled %v but returned nil; want deadline failure", d)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("writeFrame blocked >4s on stalled peer: no write deadline applied")
		}
	}
	if bounded < 150*time.Millisecond {
		t.Fatalf("stalled write never materialized (worst write %v); write deadline path not exercised", bounded)
	}
	// outbox 路径同样有界：SendInput 的首轮 wire attempt（同样写入停滞连接）
	// 不得被无限拖住。
	t0 := time.Now()
	if err := s.SendInput("probe"); err != nil {
		t.Fatalf("SendInput on stalled peer: %v", err)
	}
	if d := time.Since(t0); d > 1500*time.Millisecond {
		t.Fatalf("SendInput wedged for %v on stalled peer; outbox wire attempt must be deadline-bounded", d)
	}
	_ = s.Detach()
}

// ---------------------------------------------------------------------------
// MI-3：凭据清除后孤儿会话拨号判终态
// ---------------------------------------------------------------------------

// TestTerminalDialStopsWhenCredentialCleared（MI-3 回归）：宿主视图被丢弃
// （ClearCredential + DetachAll 竞态）后孤儿化的会话，在拨号失败路径凭据
// 缺失即判终态退出（广播 disconnected、停止重连），不再以已清空凭据无限
// 重试制造 goroutine/事件噪声。
func TestTerminalDialStopsWhenCredentialCleared(t *testing.T) {
	// 不可达端口（拨号立即失败）。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	tr, err := NewTransport("http://" + addr)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	if err := tr.SetCredential(deviceWireID(3), deviceWireSecret(5)); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	em := &recordingEmitter{}
	logs := &recordingLogger{}
	cfg := testTerminalCfg()
	cfg.Logger = logs.logf
	m := NewTerminalManager(tr, "host-mi3", em, cfg)
	s, err := m.Attach("sess-mi3")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer m.DetachAll()

	// 先观察到至少两轮拨号失败重试（退避节奏 40ms→200ms）。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && countConnState(em.snapshot(), string(ConnConnecting)) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if countConnState(em.snapshot(), string(ConnConnecting)) < 2 {
		t.Fatal("expected >=2 dial attempts before clearing credential")
	}

	// 宿主视图被丢弃：凭据清除（Attach/DetachAll 竞态下该会话无人 Detach）。
	tr.ClearCredential()
	em.waitFor(t, "disconnected after credential cleared", hasConnState(string(ConnDisconnected)), 3*time.Second)

	// 终态后不再重连：disconnected 之后不得再出现新的 connecting。
	time.Sleep(600 * time.Millisecond)
	events := em.snapshot()
	firstDisc := -1
	for i, ev := range events {
		if ev.Name != EventConnState {
			continue
		}
		if m2, ok := ev.Payload.(map[string]any); ok && m2["state"] == string(ConnDisconnected) {
			firstDisc = i
			break
		}
	}
	if firstDisc < 0 {
		t.Fatal("no disconnected state event found")
	}
	for _, ev := range events[firstDisc+1:] {
		if ev.Name != EventConnState {
			continue
		}
		if m2, ok := ev.Payload.(map[string]any); ok && m2["state"] == string(ConnConnecting) {
			t.Fatalf("orphaned session kept reconnecting after credential cleared (connecting event after disconnected)")
		}
	}
	if st := s.State(); st != ConnDisconnected {
		t.Fatalf("state after credential cleared = %s, want disconnected", st)
	}
}
