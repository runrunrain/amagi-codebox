// ws_client_test.go — /ws/v1 客户端（ws.go/conn.go/backfill.go/outbox.go）集成
// 测试：httptest + gorilla Upgrader 假宿主（参考 e2e/harness/remote-server 的
// 装配形态，但不 import internal/remote）。覆盖：
//
//	· 线缆纪律：URL 无凭据参数、Origin/device Cookie 注入、契约帧对
//	  v1-wire-fixtures.json WS 家族的编解码一致性；
//	· attach → output → input.ack → resize 全链；
//	· 断线重连：attach 携带 lastSeq、backfill 补历史无缺口、已 ack 输入不
//	  重发、未 ack 输入同 MessageID 新 RequestID 重发并结算；
//	· auth.revoked / Close 1008 fail-closed（停止重连 + rc:revoked 广播）；
//	· 重连退避节奏（1s→…→30s 封顶的缩比验证 + rate.limited 加倍）。
package remoteclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// 录制式 EventEmitter
// ---------------------------------------------------------------------------

type emittedEvent struct {
	Name    string
	Payload any
}

type recordingEmitter struct {
	mu     sync.Mutex
	events []emittedEvent
}

func (e *recordingEmitter) Emit(name string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, emittedEvent{Name: name, Payload: payload})
}

func (e *recordingEmitter) snapshot() []emittedEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]emittedEvent, len(e.events))
	copy(out, e.events)
	return out
}

// waitFor 轮询等待谓词成立（超时 Fatal）。
func (e *recordingEmitter) waitFor(t *testing.T, what string, pred func([]emittedEvent) bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred(e.snapshot()) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; events=%s", what, e.dump())
}

func (e *recordingEmitter) dump() string {
	var sb strings.Builder
	for _, ev := range e.snapshot() {
		raw, _ := json.Marshal(ev.Payload)
		fmt.Fprintf(&sb, "\n  %s %s", ev.Name, raw)
	}
	return sb.String()
}

// outputSeqs 收集 rc:terminal-output 的 seq 序（升序到达序）。
func outputSeqs(events []emittedEvent) []uint64 {
	var out []uint64
	for _, ev := range events {
		if ev.Name != EventTerminalOutput {
			continue
		}
		m, ok := ev.Payload.(map[string]any)
		if !ok {
			continue
		}
		if seq, ok := m["seq"].(uint64); ok {
			out = append(out, seq)
		}
	}
	return out
}

func gapNotices(events []emittedEvent) []map[string]any {
	var out []map[string]any
	for _, ev := range events {
		if ev.Name != EventTerminalOutput {
			continue
		}
		if m, ok := ev.Payload.(map[string]any); ok {
			if g, ok := m["gap"].(map[string]any); ok {
				out = append(out, g)
			}
		}
	}
	return out
}

func hasConnState(state string) func([]emittedEvent) bool {
	return func(events []emittedEvent) bool {
		for _, ev := range events {
			if ev.Name != EventConnState {
				continue
			}
			if m, ok := ev.Payload.(map[string]any); ok && m["state"] == state {
				return true
			}
		}
		return false
	}
}

// ---------------------------------------------------------------------------
// 假 WS 宿主（可脚本化连接行为）
// ---------------------------------------------------------------------------

// fakeWSConn 是一次已完成/进行中的 WS 连接的服务端观察。
type fakeWSConn struct {
	t      *testing.T
	conn   *websocket.Conn
	mu     sync.Mutex
	frames []contract.ClientFrame // 严格解码后的客户端帧
	// 握手观察（由 handler 记录）。
	Cookie string
	Origin string
	Path   string
	Query  string
}

func (c *fakeWSConn) addFrame(f contract.ClientFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames = append(c.frames, f)
}

func (c *fakeWSConn) frameList() []contract.ClientFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]contract.ClientFrame, len(c.frames))
	copy(out, c.frames)
	return out
}

func (c *fakeWSConn) inputFrames() []contract.InputFrame {
	var out []contract.InputFrame
	for _, f := range c.frameList() {
		if in, ok := f.(contract.InputFrame); ok {
			out = append(out, in)
		}
	}
	return out
}

func (c *fakeWSConn) resizeFrames() []contract.ResizeFrame {
	var out []contract.ResizeFrame
	for _, f := range c.frameList() {
		if r, ok := f.(contract.ResizeFrame); ok {
			out = append(out, r)
		}
	}
	return out
}

func (c *fakeWSConn) backfillFrames() []contract.BackfillFrame {
	var out []contract.BackfillFrame
	for _, f := range c.frameList() {
		if b, ok := f.(contract.BackfillFrame); ok {
			out = append(out, b)
		}
	}
	return out
}

// writeEvent 编码并写一个服务端事件（MarshalServerEvent 校验后出帧）。
func (c *fakeWSConn) writeEvent(t *testing.T, ev contract.KnownServerEvent) {
	t.Helper()
	raw, err := contract.MarshalServerEvent(ev)
	if err != nil {
		c.t.Fatalf("fakeWSHost marshal event %T: %v", ev, err)
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		c.t.Fatalf("fakeWSHost write event %T: %v", ev, err)
	}
}

func (c *fakeWSConn) readFrame(t *testing.T, what string) contract.ClientFrame {
	t.Helper()
	_, raw, err := c.conn.ReadMessage()
	if err != nil {
		c.t.Fatalf("fakeWSHost read %s: %v", what, err)
	}
	f, derr := contract.DecodeClientFrame(raw)
	if derr != nil {
		c.t.Fatalf("fakeWSHost decode %s: %v\nraw=%s", what, derr, raw)
	}
	c.addFrame(f)
	return f
}

// tryReadFrame 非阻塞读（短窗口）；无帧返回 false。
func (c *fakeWSConn) tryReadFrame(t *testing.T, wait time.Duration) (contract.ClientFrame, bool) {
	type result struct {
		f  contract.ClientFrame
		ok bool
	}
	ch := make(chan result, 1)
	go func() {
		_ = c.conn.SetReadDeadline(time.Now().Add(wait))
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			ch <- result{}
			return
		}
		f, derr := contract.DecodeClientFrame(raw)
		if derr != nil {
			c.t.Errorf("fakeWSHost decode frame: %v", derr)
			ch <- result{}
			return
		}
		c.addFrame(f)
		ch <- result{f: f, ok: true}
	}()
	select {
	case r := <-ch:
		return r.f, r.ok
	case <-time.After(wait + time.Second):
		return nil, false
	}
}

// fakeWSHost 是可脚本化的 /ws/v1 假宿主。script 按连接序号驱动一条连接的
// 全生命周期；升级前校验路径/查询/Origin/Cookie。
type fakeWSHost struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	conns  []*fakeWSConn
	script func(idx int, c *fakeWSConn)
	device *fakeDevice
}

func newFakeWSHost(t *testing.T, script func(idx int, c *fakeWSConn)) *fakeWSHost {
	t.Helper()
	f := &fakeWSHost{
		t:      t,
		script: script,
		device: &fakeDevice{id: deviceWireID(7), secret: deviceWireSecret(11)},
	}
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != contract.WebSocketV1Path {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		idx := len(f.conns)
		f.mu.Unlock()
		c := &fakeWSConn{t: t, Path: r.URL.Path, Query: r.URL.RawQuery, Origin: r.Header.Get("Origin")}
		if ck, err := r.Cookie(deviceCookieName); err == nil {
			c.Cookie = ck.Value
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c.conn = conn
		f.mu.Lock()
		f.conns = append(f.conns, c)
		f.mu.Unlock()
		f.script(idx, c)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeWSHost) baseURL() string { return f.srv.URL }

// transport 为假宿主构造已装载凭据的 Transport。
func (f *fakeWSHost) transport(t *testing.T) *Transport {
	t.Helper()
	tr, err := NewTransport(f.baseURL())
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	if err := tr.SetCredential(f.device.id, f.device.secret); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	return tr
}

func (f *fakeWSHost) connCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.conns)
}

func (f *fakeWSHost) conn(idx int) *fakeWSConn {
	f.mu.Lock()
	defer f.mu.Unlock()
	if idx >= len(f.conns) {
		return nil
	}
	return f.conns[idx]
}

// testTerminalCfg 是缩比测试配置：退避 40ms→200ms 封顶，关闭 ping。
func testTerminalCfg() TerminalConfig {
	return TerminalConfig{BackoffBase: 40 * time.Millisecond, BackoffMax: 200 * time.Millisecond, PingInterval: 0}
}

// outputFrame 构造一条 output replay 帧。
func outputFrame(sid contract.SessionID, seq contract.Seq, text string) contract.OutputEvent {
	return contract.OutputEvent{Type: contract.ServerEventTypeOutput, SessionID: sid, Seq: seq, Chunk: b64(text)}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// fiveLayer 构造合法 attach 快照。
func fiveLayer(session contract.SessionState, control contract.ControlState, history contract.HistorySnapshot) contract.FiveLayerSnapshot {
	return contract.FiveLayerSnapshot{
		Connection: contract.ConnectionSnapshot{State: contract.AttachedConnectionState},
		Auth:       contract.AuthSnapshot{State: contract.AttachedAuthState},
		Session:    contract.SessionSnapshot{State: session},
		Control:    contract.ControlSnapshot{State: control},
		History:    history,
	}
}

// attachedEvent 构造 session.attached（inputAckMode 由 ackMode 开关）。
func attachedEvent(rid contract.RequestID, sid contract.SessionID, history []contract.ReplayFrame, earliest, latest contract.Seq, control contract.ControlState, ackMode bool) contract.SessionAttachedEvent {
	ev := contract.SessionAttachedEvent{
		Type: contract.ServerEventTypeSessionAttached, RequestID: rid, APIVersion: contract.APIVersionV1,
		SessionID: sid, History: history, EarliestSeq: earliest, LatestSeq: latest,
		Snapshot: fiveLayer(contract.SessionStateRunning, control, contract.HistorySnapshot{State: contract.HistoryStateContinuous}),
	}
	if ackMode {
		m := contract.InputAckModeSessionWindowV1
		ev.InputAckMode = &m
	}
	return ev
}

// serveAttach 读 attach 帧并按参数回复 session.attached；返回 attach 帧。
// history 可为 nil（自动归一为空非 nil 切片，契约要求）。
func serveAttach(t *testing.T, c *fakeWSConn, sid contract.SessionID, history []contract.ReplayFrame, earliest, latest contract.Seq, control contract.ControlState, ackMode bool) contract.AttachFrame {
	t.Helper()
	if history == nil {
		history = []contract.ReplayFrame{}
	}
	f := c.readFrame(t, "attach")
	af, ok := f.(contract.AttachFrame)
	if !ok {
		t.Fatalf("first frame is %T, want attach", f)
	}
	if af.SessionID != sid {
		t.Fatalf("attach sessionId = %q, want %q", af.SessionID, sid)
	}
	c.writeEvent(t, attachedEvent(af.RequestID, sid, history, earliest, latest, control, ackMode))
	return af
}

// ---------------------------------------------------------------------------
// 线缆纪律与 fixture 家族
// ---------------------------------------------------------------------------

// TestWSURLCarriesNoCredentials：wsURL 只产生 ws(s)://host/ws/v1，无查询参数。
func TestWSURLCarriesNoCredentials(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://127.0.0.1:8680", "ws://127.0.0.1:8680/ws/v1"},
		{"https://box.example.com", "wss://box.example.com/ws/v1"},
	} {
		if got := wsURL(tc.in); got != tc.want {
			t.Errorf("wsURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// loadFixtureDoc 装载共享 wire fixture（transport_test.go 同源）。
func loadFixtureDoc(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, wireFixturePath))
	if err != nil {
		t.Fatalf("read wire fixtures: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse wire fixtures: %v", err)
	}
	return doc
}

// TestEncodeClientFrameMatchesFixtures：5 类客户端帧经 encodeClientFrame 编码
// 后与 fixture 语义一致（map 级 JSON 等价），且严格解码回验通过。
func TestEncodeClientFrameMatchesFixtures(t *testing.T) {
	doc := loadFixtureDoc(t)
	var cf struct {
		ClientFrames map[string]json.RawMessage `json:"clientFrames"`
	}
	if err := json.Unmarshal(doc["clientFrames"], &cf.ClientFrames); err != nil {
		t.Fatalf("parse clientFrames: %v", err)
	}
	if len(cf.ClientFrames) == 0 {
		t.Fatal("clientFrames fixtures empty")
	}
	for name, raw := range cf.ClientFrames {
		f, err := contract.DecodeClientFrame(raw)
		if err != nil {
			t.Errorf("fixture %s: decode: %v", name, err)
			continue
		}
		enc, err := encodeClientFrame(f)
		if err != nil {
			t.Errorf("fixture %s: encode: %v", name, err)
			continue
		}
		var wantMap, gotMap map[string]any
		if err := json.Unmarshal(raw, &wantMap); err != nil {
			t.Fatalf("fixture %s: want map: %v", name, err)
		}
		if err := json.Unmarshal(enc, &gotMap); err != nil {
			t.Fatalf("fixture %s: got map: %v", name, err)
		}
		if !reflect.DeepEqual(wantMap, gotMap) {
			t.Errorf("fixture %s: encoded frame mismatch:\n want %v\n got  %v", name, wantMap, gotMap)
		}
	}
	// 非法帧必须编码失败（fail-closed，不上线）。
	bad := contract.ResizeFrame{Type: contract.ClientFrameTypeResize, RequestID: contract.RequestID("req-v1-" + strings.Repeat("0", 32)), Cols: 0, Rows: 10}
	if _, err := encodeClientFrame(bad); err == nil {
		t.Error("resize cols=0 must fail encode")
	}
}

// TestDecodeServerEventFixtures：8 类服务端事件 fixture 全部解码为正确具体
// 类型；unknownEvent 归 Unknown(unknown-type) 不终止；畸形已知事件归
// Unknown(malformed)。
func TestDecodeServerEventFixtures(t *testing.T) {
	doc := loadFixtureDoc(t)
	var se map[string]json.RawMessage
	if err := json.Unmarshal(doc["serverEvents"], &se); err != nil {
		t.Fatalf("parse serverEvents: %v", err)
	}
	wantType := map[string]string{
		"sessionAttachedEmptyHistory":        "contract.SessionAttachedEvent",
		"sessionAttachedWithHistory":         "contract.SessionAttachedEvent",
		"sessionAttachedGap":                 "contract.SessionAttachedEvent",
		"sessionAttachedWithInputAckMode":    "contract.SessionAttachedEvent",
		"sessionAttachedUnknownInputAckMode": "contract.SessionAttachedEvent",
		"output":                             "contract.OutputEvent",
		"backfillResultFrames":               "contract.BackfillFramesResultEvent",
		"backfillResultGap":                  "contract.BackfillGapResultEvent",
		"sessionStateExited":                 "contract.SessionStateEvent",
		"sessionStateRestartBoundary":        "contract.SessionRestartBoundaryEvent",
		"controlStateOther":                  "contract.ControlStateEvent",
		"controlStateYou":                    "contract.ControlStateEvent",
		"controlStateNone":                   "contract.ControlStateEvent",
		"controlStateDesktop":                "contract.ControlStateEvent",
		"authRevoked":                        "contract.AuthRevokedEvent",
		"error":                              "contract.ErrorEvent",
		"inputAck":                           "contract.InputAckEvent",
		"inputAckLegacyId":                   "contract.InputAckEvent",
	}
	for name, raw := range se {
		if name == "unknownEvent" {
			continue
		}
		ev := decodeServerEvent(raw)
		if ev.Unknown != nil {
			t.Errorf("fixture %s: decoded as unknown(%s)", name, ev.Unknown.Reason)
			continue
		}
		got := fmt.Sprintf("%T", ev.Known)
		if want, ok := wantType[name]; ok && got != want {
			t.Errorf("fixture %s: decoded %s, want %s", name, got, want)
		}
	}
	// 未知类型：净化 Unknown，不终止。
	ev := decodeServerEvent(se["unknownEvent"])
	if ev.Known != nil || ev.Unknown == nil || ev.Unknown.Reason != unknownReasonType || ev.Unknown.WireType != "session.metrics" {
		t.Fatalf("unknownEvent decode = %+v, want sanitized unknown-type", ev)
	}
	// 已知类型畸形（output chunk 非 base64）：净化 Unknown(malformed)。
	malformed := []byte(`{"type":"output","sessionId":"s","seq":1,"chunk":"!!!not-base64!!!"}`)
	ev = decodeServerEvent(malformed)
	if ev.Known != nil || ev.Unknown == nil || ev.Unknown.Reason != unknownReasonMalformed {
		t.Fatalf("malformed output decode = %+v, want sanitized malformed", ev)
	}
}

// TestDialWebSocketInjectsHeaders：升级请求带单 Origin + device Cookie、
// 路径 /ws/v1 且查询为空。
func TestDialWebSocketInjectsHeaders(t *testing.T) {
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		// 仅持有连接等待客户端关闭。
		_, _, _ = c.conn.ReadMessage()
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-test", em, testTerminalCfg())
	defer m.DetachAll()
	s, err := m.Attach("sess-hdr")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	em.waitFor(t, "first connection", func([]emittedEvent) bool { return host.connCount() >= 1 }, 2*time.Second)
	c := host.conn(0)
	if c.Path != contract.WebSocketV1Path || c.Query != "" {
		t.Errorf("upgrade path=%q query=%q, want %q empty", c.Path, c.Query, contract.WebSocketV1Path)
	}
	wantOrigin := host.baseURL()
	if c.Origin != wantOrigin {
		t.Errorf("Origin = %q, want %q", c.Origin, wantOrigin)
	}
	wantCookie := buildDeviceCookieValue(host.device.id, host.device.secret)
	if c.Cookie != wantCookie {
		t.Errorf("device cookie = %q, want %q", c.Cookie, wantCookie)
	}
	_ = s.Detach()
}

// ---------------------------------------------------------------------------
// attach → output → input.ack → resize 全链
// ---------------------------------------------------------------------------

// TestTerminalFullChain：attached(inputAckMode+control you) → 历史 output →
// live output → SendInput（canonical ID + base64）→ 服务端 input.ack 结算 →
// Resize 到达 → Detach。
func TestTerminalFullChain(t *testing.T) {
	const sid = contract.SessionID("sess-chain")
	var (
		ackSeen = make(chan contract.InputFrame, 1)
	)
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		serveAttach(t, c, sid, []contract.ReplayFrame{
			outputFrame(sid, 1, "hist-1"), outputFrame(sid, 2, "hist-2"),
		}, 1, 2, contract.ControlStateYou, true)
		c.writeEvent(t, outputFrame(sid, 3, "live-3"))
		// 循环处理 input（回 ack）与 resize（记录后收尾）。
		for {
			f := c.readFrame(t, "input/resize")
			switch v := f.(type) {
			case contract.InputFrame:
				c.writeEvent(t, contract.InputAckEvent{
					Type: contract.ServerEventTypeInputAck, RequestID: v.RequestID, SessionID: sid, ID: v.ID,
				})
				select {
				case ackSeen <- v:
				default:
				}
			case contract.ResizeFrame:
				return
			}
		}
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-1", em, testTerminalCfg())
	defer m.DetachAll()
	s, err := m.Attach(string(sid))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	em.waitFor(t, "attached state", hasConnState(string(ConnAttached)), 2*time.Second)

	// 历史与 live 输出按 seq 顺序到达。
	em.waitFor(t, "outputs 1..3", func(events []emittedEvent) bool {
		seqs := outputSeqs(events)
		return len(seqs) >= 3 && seqs[0] == 1 && seqs[1] == 2 && seqs[2] == 3
	}, 2*time.Second)

	// 输入：canonical ID + base64 载荷。
	if err := s.SendInput("ls -la\n"); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	select {
	case in := <-ackSeen:
		if !contract.IsCanonicalMessageID(in.ID) {
			t.Errorf("input id %q not canonical", in.ID)
		}
		if want := b64("ls -la\n"); in.Data != want {
			t.Errorf("input data = %q, want %q", in.Data, want)
		}
		if !contract.IsCanonicalRequestID(in.RequestID) {
			t.Errorf("input requestId %q not canonical", in.RequestID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never received input")
	}
	// ack 结算：PendingCount 归零。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.outbox.PendingCount() == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := s.outbox.PendingCount(); n != 0 {
		t.Fatalf("outbox pending after ack = %d, want 0", n)
	}

	// resize 到达服务端。
	if err := s.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rs := host.conn(0).resizeFrames(); len(rs) == 1 && rs[0].Cols == 120 && rs[0].Rows == 40 {
			return // 全链通过
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("resize never reached server; frames=%v", host.conn(0).frameList())
}

// ---------------------------------------------------------------------------
// 断线重连：backfill 补历史无缺口；已 ack 输入不重发；未 ack 输入重发同 ID 新 rid
// ---------------------------------------------------------------------------

// TestTerminalReconnectBackfillNoGap：conn1 推 seq1..3 后异常断开；conn2
// attach 携带 lastSeq=3，服务端 attached 快照 gap[4..9]+history[10,11]；客户端
// 发 backfill[4,9]，服务端回 frames 变体 4..9；最终输出 1..11 无缺口，且已
// ack 输入在 conn2 零重发。
func TestTerminalReconnectBackfillNoGap(t *testing.T) {
	const sid = contract.SessionID("sess-reconn")
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		switch idx {
		case 0:
			serveAttach(t, c, sid, []contract.ReplayFrame{outputFrame(sid, 1, "a")}, 1, 1, contract.ControlStateYou, true)
			c.writeEvent(t, outputFrame(sid, 2, "b"))
			c.writeEvent(t, outputFrame(sid, 3, "c"))
			time.Sleep(150 * time.Millisecond) // 等客户端消费 + 输入
			// 读取输入并 ack（已 ack 输入不得重发）。
			if f, ok := c.tryReadFrame(t, 500*time.Millisecond); ok {
				if in, ok := f.(contract.InputFrame); ok {
					c.writeEvent(t, contract.InputAckEvent{Type: contract.ServerEventTypeInputAck, RequestID: in.RequestID, SessionID: sid, ID: in.ID})
				}
			}
			_ = c.conn.Close() // 异常断开（无 close 帧）
		case 1:
			af := serveAttach(t, c, sid, []contract.ReplayFrame{
				outputFrame(sid, 10, "j"), outputFrame(sid, 11, "k"),
			}, 10, 11, contract.ControlStateYou, true)
			if af.LastSeq == nil || *af.LastSeq != 3 {
				t.Errorf("conn2 attach lastSeq = %v, want 3", af.LastSeq)
			}
			// 等客户端 backfill[4,9]。
			for {
				f := c.readFrame(t, "backfill")
				if bf, ok := f.(contract.BackfillFrame); ok {
					if bf.FromSeq != 4 || bf.ToSeq != 9 {
						t.Errorf("backfill range = [%d,%d], want [4,9]", bf.FromSeq, bf.ToSeq)
					}
					frames := make([]contract.ReplayFrame, 0, 6)
					for seq := contract.Seq(4); seq <= 9; seq++ {
						frames = append(frames, outputFrame(sid, seq, fmt.Sprintf("f%d", seq)))
					}
					c.writeEvent(t, contract.BackfillFramesResultEvent{
						Type: contract.ServerEventTypeBackfillResult, RequestID: bf.RequestID, SessionID: sid,
						FromSeq: 4, ToSeq: 9, EarliestSeq: 4, LatestSeq: 11, Frames: frames,
					})
					break
				}
			}
			// 保持连接，观察窗口内断言无 input 重发。
			_, _, _ = c.conn.ReadMessage()
		default:
			_, _, _ = c.conn.ReadMessage()
		}
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-1", em, testTerminalCfg())
	defer m.DetachAll()
	s, err := m.Attach(string(sid))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	em.waitFor(t, "attached", hasConnState(string(ConnAttached)), 2*time.Second)
	if err := s.SendInput("echo ok\n"); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	// 等待重连完成 + backfill 消费。
	em.waitFor(t, "second connection", func([]emittedEvent) bool { return host.connCount() >= 2 }, 4*time.Second)
	em.waitFor(t, "outputs 1..11", func(events []emittedEvent) bool {
		seqs := outputSeqs(events)
		if len(seqs) < 11 {
			return false
		}
		for i, sq := range seqs {
			if sq != uint64(i+1) {
				return false
			}
		}
		return true
	}, 4*time.Second)
	// attached gap 如实上报（[4,9] 来自 attached 快照）。
	if gaps := gapNotices(em.snapshot()); len(gaps) == 0 {
		t.Error("attached history gap not surfaced (must be reported, not swallowed)")
	} else {
		g := gaps[0]
		if g["fromSeq"] != uint64(4) || g["toSeq"] != uint64(9) {
			t.Errorf("gap notice = %v, want [4,9]", g)
		}
	}
	// 已 ack 输入在 conn2 不重发。
	time.Sleep(300 * time.Millisecond)
	if inputs := host.conn(1).inputFrames(); len(inputs) != 0 {
		t.Errorf("conn2 received %d input frames, want 0 (acked input must not resend)", len(inputs))
	}
	_ = s.Detach()
}

// TestTerminalReconnectResendsUnackedInput：未 ack 输入在重连 attach 后以同
// MessageID、新 RequestID 重发；ACK 后结算。
func TestTerminalReconnectResendsUnackedInput(t *testing.T) {
	const sid = contract.SessionID("sess-resend")
	var conn0Input = make(chan contract.InputFrame, 1)
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		switch idx {
		case 0:
			serveAttach(t, c, sid, nil, 0, 0, contract.ControlStateYou, true)
			if f, ok := c.tryReadFrame(t, 2*time.Second); ok {
				if in, ok := f.(contract.InputFrame); ok {
					conn0Input <- in
				}
			}
			_ = c.conn.Close() // 不 ack，异常断开
		case 1:
			serveAttach(t, c, sid, nil, 0, 0, contract.ControlStateYou, true)
			for {
				f := c.readFrame(t, "resent input")
				if in, ok := f.(contract.InputFrame); ok {
					c.writeEvent(t, contract.InputAckEvent{Type: contract.ServerEventTypeInputAck, RequestID: in.RequestID, SessionID: sid, ID: in.ID})
					return
				}
			}
		default:
			_, _, _ = c.conn.ReadMessage()
		}
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-1", em, testTerminalCfg())
	defer m.DetachAll()
	s, err := m.Attach(string(sid))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	em.waitFor(t, "attached", hasConnState(string(ConnAttached)), 2*time.Second)
	if err := s.SendInput("pending\n"); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	var first contract.InputFrame
	select {
	case first = <-conn0Input:
	case <-time.After(2 * time.Second):
		t.Fatal("conn0 never saw input")
	}
	em.waitFor(t, "reconnect", func([]emittedEvent) bool { return host.connCount() >= 2 }, 4*time.Second)
	// conn2 收到重发：同 ID、新 rid、随后结算。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		inputs := host.conn(1).inputFrames()
		if len(inputs) >= 1 {
			got := inputs[0]
			if got.ID != first.ID {
				t.Errorf("resent input id = %q, want same %q", got.ID, first.ID)
			}
			if got.RequestID == first.RequestID {
				t.Error("resent input reused requestId; each wire attempt must use a fresh canonical requestId")
			}
			if got.Data != first.Data {
				t.Errorf("resent input data changed: %q vs %q", got.Data, first.Data)
			}
			if n := s.outbox.PendingCount(); n != 0 {
				t.Fatalf("outbox pending after re-ack = %d, want 0", n)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("input never resent on conn2")
}

// ---------------------------------------------------------------------------
// revoked fail-closed / 退避节奏
// ---------------------------------------------------------------------------

// TestTerminalRevokedFailClosed：auth.revoked 事件 + Close 1008 → 广播
// rc:revoked / rc:host-health、状态 disconnected，且观察窗口内零重连。
func TestTerminalRevokedFailClosed(t *testing.T) {
	const sid = contract.SessionID("sess-revoked")
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		serveAttach(t, c, sid, nil, 0, 0, contract.ControlStateYou, true)
		c.writeEvent(t, contract.AuthRevokedEvent{
			Type: contract.ServerEventTypeAuthRevoked, Reason: contract.AuthRevokedReasonDeviceRevoked,
			OccurredAt: "2026-08-22T00:00:00Z",
		})
		_ = c.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(contract.AuthRevokedCloseCode, "revoked"),
			time.Now().Add(time.Second))
		time.Sleep(100 * time.Millisecond)
		_ = c.conn.Close()
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-1", em, testTerminalCfg())
	defer m.DetachAll()
	if _, err := m.Attach(string(sid)); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	em.waitFor(t, "rc:revoked", func(events []emittedEvent) bool {
		for _, ev := range events {
			if ev.Name == EventRevoked {
				return true
			}
		}
		return false
	}, 2*time.Second)
	em.waitFor(t, "rc:host-health revoked", func(events []emittedEvent) bool {
		for _, ev := range events {
			if ev.Name != EventHostHealth {
				continue
			}
			if m, ok := ev.Payload.(map[string]any); ok && m["state"] == string(HealthRevoked) {
				return true
			}
		}
		return false
	}, 2*time.Second)
	time.Sleep(400 * time.Millisecond) // 观察窗口：不得重连
	if n := host.connCount(); n != 1 {
		t.Fatalf("reconnected %d times after revoke, want exactly 1 connection (fail-closed)", n)
	}
	em.waitFor(t, "disconnected state", hasConnState(string(ConnDisconnected)), time.Second)
}

// TestTerminalBackoffPacing：拨号失败按 base→2×base→4×base 退避；封顶
// max；rate.limited 使退避加倍。
func TestTerminalBackoffPacing(t *testing.T) {
	// 记录重试时刻的时钟。
	var mu sync.Mutex
	var waits []time.Duration
	cfg := testTerminalCfg()
	cfg.BackoffBase = 30 * time.Millisecond
	cfg.BackoffMax = 120 * time.Millisecond
	cfg.After = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	// 关闭中的端口（拨号立即失败）。
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
	_ = tr.SetCredential(deviceWireID(3), deviceWireSecret(5))
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-1", em, cfg)
	s, err := m.Attach("sess-backoff")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer m.DetachAll()
	// 等待 4 轮退避记录。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(waits)
		mu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = s.Detach()
	mu.Lock()
	defer mu.Unlock()
	if len(waits) < 4 {
		t.Fatalf("only %d backoff waits recorded, want >=4", len(waits))
	}
	want := []time.Duration{30 * time.Millisecond, 60 * time.Millisecond, 120 * time.Millisecond, 120 * time.Millisecond}
	for i, w := range want {
		if waits[i] != w {
			t.Fatalf("backoff[%d] = %v, want %v (all: %v)", i, waits[i], w, waits)
		}
	}
}

// TestTerminalRateLimitedDoublesBackoff：服务端连发 rate.limited error 后断
// 开，下一轮退避 = 2×基础节奏。
func TestTerminalRateLimitedDoublesBackoff(t *testing.T) {
	var mu sync.Mutex
	var waits []time.Duration
	cfg := testTerminalCfg()
	cfg.BackoffBase = 30 * time.Millisecond
	cfg.BackoffMax = 240 * time.Millisecond
	cfg.After = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	const sid = contract.SessionID("sess-rate")
	host := newFakeWSHost(t, func(idx int, c *fakeWSConn) {
		serveAttach(t, c, sid, nil, 0, 0, contract.ControlStateYou, true)
		c.writeEvent(t, contract.ErrorEvent{
			Type: contract.ServerEventTypeError, Code: contract.ErrorCodeRateLimited,
			Layer: contract.ErrorLayerConnection, Message: "too many requests", ActionHint: contract.ActionHintRetry,
		})
		_ = c.conn.Close()
	})
	tr := host.transport(t)
	em := &recordingEmitter{}
	m := NewTerminalManager(tr, "host-1", em, cfg)
	if _, err := m.Attach(string(sid)); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer m.DetachAll()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(waits)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(waits) < 2 {
		t.Fatalf("only %d waits recorded", len(waits))
	}
	// conn1 断开后 rateLimited=true：第一轮等待 = 2×30ms。
	if waits[0] != 60*time.Millisecond {
		t.Fatalf("first wait after rate.limited = %v, want 60ms (doubled)", waits[0])
	}
}
