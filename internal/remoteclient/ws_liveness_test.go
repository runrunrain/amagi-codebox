// ws_liveness_test.go — RC3 修复锚点测试：
//   · F-2 收口：attach(control=none) 只读 → control.state(you) 事件开输入门
//     → input.ack 全链 → control.state(none) 关门（服务端事件为写权威）；
//   · F-4：读超时+pong 检测——无响应宿主（SIGSTOP 半开注入）在 PongWait 内
//     浮出为读超时并触发重连；正常回 pong 的宿主跨多个窗口不误判；
//   · F-3：可注入净化日志——正向断言（诊断确实落日志）+ 负向断言（日志不含
//     设备 secret、不含终端字节，蓝图 §9）。
package remoteclient

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// recordingLogger 录制净化日志 seam 的已格式化文本。
type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordingLogger) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *recordingLogger) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.lines))
	copy(out, l.lines)
	return out
}

func (l *recordingLogger) joined() string { return strings.Join(l.snapshot(), "\n") }

// TestTerminalControlStateOpensInputGate（F-2 收口）：attach 快照 control=none
// → 只读；服务端 control.state(you)（acquire 成功的服务端推送）→ 输入门开；
// input 发送+ack 结算；control.state(none)（release/被接管）→ 关门只读。
func TestTerminalControlStateOpensInputGate(t *testing.T) {
	const sid = contract.SessionID("sess-gate")
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		serveAttach(t, c, sid, nil, 0, 0, contract.ControlStateNone, true) // inputAckMode 有、控制权 none → 只读
		for {
			f := c.readFrame(t, "gate frames")
			switch v := f.(type) {
			case contract.InputFrame:
				c.writeEvent(t, contract.InputAckEvent{
					Type: contract.ServerEventTypeInputAck, RequestID: v.RequestID, SessionID: sid, ID: v.ID,
				})
				// 输入被受理即意味着门开：回 none 关门并结束。
				c.writeEvent(t, contract.ControlStateEvent{
					Type: contract.ServerEventTypeControlState, SessionID: sid,
					State: contract.ControlStateNone, Reason: "released", OccurredAt: "2026-08-22T00:00:00Z",
				})
				_, _, _ = c.conn.ReadMessage()
				return
			}
		}
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-gate", em, testTerminalCfg())
	defer m.DetachAll()
	s, err := m.Attach(string(sid))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// 快照 none → 只读：输入被客户端拒绝。
	em.waitFor(t, "readonly state", hasConnState(string(ConnReadonly)), 2*time.Second)
	if err := s.SendInput("blocked\n"); err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("SendInput in readonly = %v, want readonly rejection", err)
	}

	// 服务端 control.state(you)（acquire 推送）→ 输入门开。
	host.conn(0).writeEvent(t, contract.ControlStateEvent{
		Type: contract.ServerEventTypeControlState, SessionID: sid,
		State: contract.ControlStateYou, Reason: "rest-acquire", OccurredAt: "2026-08-22T00:00:01Z",
	})
	em.waitFor(t, "attached state after control.state(you)", hasConnState(string(ConnAttached)), 2*time.Second)

	// 输入可发且被服务端受理（ack 结算）。
	if err := s.SendInput("now allowed\n"); err != nil {
		t.Fatalf("SendInput after control.state(you): %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && s.outbox.PendingCount() != 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if n := s.outbox.PendingCount(); n != 0 {
		t.Fatalf("outbox pending after ack = %d, want 0", n)
	}

	// control.state(none)（服务端 ack 后回推）→ 关门只读。
	em.waitFor(t, "readonly again after control.state(none)", func(events []emittedEvent) bool {
		last := ""
		for _, ev := range events {
			if ev.Name == EventControlState {
				if mm, ok := ev.Payload.(map[string]any); ok {
					last, _ = mm["state"].(string)
				}
			}
		}
		return last == string(contract.ControlStateNone)
	}, 2*time.Second)
	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) && s.State() != ConnReadonly {
		time.Sleep(5 * time.Millisecond)
	}
	if s.State() != ConnReadonly {
		t.Fatalf("state after control.state(none) = %s, want readonly", s.State())
	}
	if err := s.SendInput("blocked again\n"); err == nil {
		t.Fatal("SendInput after control.state(none) must be rejected")
	}
}

// TestTerminalReadDeadlineDetectsDeadPeer（F-4）：宿主升级后完全无响应（不读
// ——永不回 pong；不写；SIGSTOP 半开注入）→ PongWait 内读超时浮出，触发既有
// 重连状态机；第二连接正常服务。净化日志含 deadline 分类。
func TestTerminalReadDeadlineDetectsDeadPeer(t *testing.T) {
	const sid = contract.SessionID("sess-dead")
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		if idx == 0 {
			// 无响应宿主：不读（不回 pong）、不写。
			time.Sleep(3 * time.Second)
			return
		}
		serveAttach(t, c, sid, nil, 0, 0, contract.ControlStateYou, true)
		for { // 持续读：自动回 pong，模拟恢复后的活宿主。
			if _, _, err := c.conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	logs := &recordingLogger{}
	cfg := TerminalConfig{
		BackoffBase: 20 * time.Millisecond, BackoffMax: 80 * time.Millisecond,
		PingInterval: 25 * time.Millisecond, PongWait: 150 * time.Millisecond,
		Logger: logs.logf,
	}
	m := NewTerminalManager(tr, "host-dead", em, cfg)
	defer m.DetachAll()
	if _, err := m.Attach(string(sid)); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// 半开在 ~PongWait 内被检出：重连拨第二条连接并 attach 成功。
	em.waitFor(t, "reconnect after read deadline", func([]emittedEvent) bool {
		return host.connCount() >= 2
	}, 3*time.Second)
	em.waitFor(t, "attached on second connection", hasConnState(string(ConnAttached)), 3*time.Second)

	// F-3 正向断言：deadline 分类确实落日志。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(logs.joined(), "read deadline exceeded") {
		time.Sleep(5 * time.Millisecond)
	}
	if j := logs.joined(); !strings.Contains(j, "read deadline exceeded") {
		t.Errorf("deadline classification missing from sanitized logs:\n%s", j)
	}
	// 重连后不再判死：观察窗口内无第三次拨号（第二连接 pong 正常续期）。
	time.Sleep(400 * time.Millisecond)
	if n := host.connCount(); n != 2 {
		t.Fatalf("connection count = %d after recovery, want 2 (live peer must not be judged dead)", n)
	}
}

// TestTerminalPongKeepsAlive（F-4 正向对照）：正常读泵的宿主自动回 pong，
// 连接跨多个 PongWait 窗口存活，不触发重连、无 deadline 日志。
func TestTerminalPongKeepsAlive(t *testing.T) {
	const sid = contract.SessionID("sess-alive")
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		serveAttach(t, c, sid, nil, 0, 0, contract.ControlStateYou, true)
		for { // 持续读：协议 ping 自动回 pong；应用层 ping 帧被读出并忽略。
			if _, _, err := c.conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	logs := &recordingLogger{}
	cfg := TerminalConfig{
		BackoffBase: 20 * time.Millisecond, BackoffMax: 80 * time.Millisecond,
		PingInterval: 25 * time.Millisecond, PongWait: 120 * time.Millisecond,
		Logger: logs.logf,
	}
	m := NewTerminalManager(tr, "host-alive", em, cfg)
	defer m.DetachAll()
	if _, err := m.Attach(string(sid)); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	em.waitFor(t, "attached", hasConnState(string(ConnAttached)), 2*time.Second)
	time.Sleep(500 * time.Millisecond) // > 4×PongWait：任何误判都会在此窗口重连
	if n := host.connCount(); n != 1 {
		t.Fatalf("connection count = %d, want 1 (responsive peer must stay connected)", n)
	}
	if j := logs.joined(); j != "" {
		t.Errorf("unexpected diagnostics on live peer:\n%s", j)
	}
}

// TestTerminalLoggerSanitized（F-3 负向断言）：诊断日志不含设备 secret 与
// 终端字节（蓝图 §9）；正向断言断线诊断确实落日志。
func TestTerminalLoggerSanitized(t *testing.T) {
	const sid = contract.SessionID("sess-log")
	const secretChunk = "TOPSECRET-TERMINAL-CHUNK-9f3a"
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		switch idx {
		case 0:
			serveAttach(t, c, sid, []contract.ReplayFrame{outputFrame(sid, 1, secretChunk)}, 1, 1, contract.ControlStateYou, true)
			_ = c.conn.Close() // 异常断开 → 客户端记 ws read error
		default:
			_, _, _ = c.conn.ReadMessage()
		}
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	logs := &recordingLogger{}
	cfg := testTerminalCfg()
	cfg.Logger = logs.logf
	m := NewTerminalManager(tr, "host-log", em, cfg)
	defer m.DetachAll()
	if _, err := m.Attach(string(sid)); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// 正向：断线诊断落日志。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(logs.snapshot()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	j := logs.joined()
	if j == "" {
		t.Fatal("no sanitized diagnostics recorded (logger not wired)")
	}
	// 负向：不含设备 secret（wire 形态与 cookie 值均不得出现）。
	secret := host.device.secret
	if strings.Contains(j, secret) || strings.Contains(j, buildDeviceCookieValue(host.device.id, secret)) {
		t.Errorf("sanitized logs leak device credential:\n%s", j)
	}
	// 负向：不含终端字节（明文与 base64 形态均不得出现）。
	if strings.Contains(j, secretChunk) || strings.Contains(j, b64(secretChunk)) {
		t.Errorf("sanitized logs leak terminal bytes:\n%s", j)
	}
}

// TestTerminalConfigPongWaitDefaults：PongWait 缺省 = 2×PingInterval；PingInterval
// 关闭（0）时读超时同样关闭（静默连接不误判）。
func TestTerminalConfigPongWaitDefaults(t *testing.T) {
	cfg := TerminalConfig{PingInterval: 30 * time.Second}.withDefaults()
	if cfg.PongWait != 60*time.Second {
		t.Errorf("PongWait default = %v, want 60s (2×PingInterval)", cfg.PongWait)
	}
	off := TerminalConfig{}.withDefaults()
	if off.PongWait != 0 || off.PingInterval != 0 {
		t.Errorf("zero config = ping %v pongWait %v, want both off (explicit zero disables)", off.PingInterval, off.PongWait)
	}
	def := DefaultTerminalConfig()
	if def.PingInterval != 30*time.Second || def.PongWait != 60*time.Second {
		t.Errorf("DefaultTerminalConfig = ping %v pongWait %v, want 30s/60s", def.PingInterval, def.PongWait)
	}
	// 测试配置（ping 关闭）不得启用 deadline。
	tc := testTerminalCfg().withDefaults()
	if tc.PongWait != 0 {
		t.Errorf("testTerminalCfg PongWait = %v, want 0 (ping disabled)", tc.PongWait)
	}
}
