package claudewebui

// ws_test.go — /ws/events 契约测试（design §5.2 ws、§6.2、测试计划 ws_test.go 行）：
// 子协议对校验/拒绝、hello（stream/floor/cursor/busy）、seq 单调、
// ?since 环形重放、缺口信号（floor 抬升）、停止时 bye。

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func wsURL(tp *testPlane, query string) string {
	url := "ws://127.0.0.1:" + strconv.Itoa(tp.port) + "/ws/events"
	if query != "" {
		url += "?" + query
	}
	return url
}

// dialWS 以给定子协议/头拨号。
func dialWS(t *testing.T, url string, subprotocols []string, headers map[string]string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	hdr := http.Header{}
	for k, v := range headers {
		hdr.Set(k, v)
	}
	d := websocket.Dialer{Subprotocols: subprotocols, HandshakeTimeout: 5 * time.Second}
	conn, resp, err := d.Dial(url, hdr)
	if conn != nil {
		t.Cleanup(func() { _ = conn.Close() })
	}
	return conn, resp, err
}

// readEnvelope 读一帧信封（带超时）。
func readEnvelope(t *testing.T, c *websocket.Conn, d time.Duration) envelope {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(d))
	var env envelope
	if err := c.ReadJSON(&env); err != nil {
		t.Fatalf("读 WS 帧: %v", err)
	}
	return env
}

// readKind 读到首个 kind 命中的帧（其余帧丢弃，带总超时）。
func readKind(t *testing.T, c *websocket.Conn, d time.Duration, kinds ...string) envelope {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		rem := time.Until(deadline)
		if rem <= 0 {
			t.Fatalf("等待 %v 帧超时", kinds)
		}
		env := readEnvelope(t, c, rem)
		for _, k := range kinds {
			if env.Kind == k {
				return env
			}
		}
	}
}

const (
	wsHumanLine     = `{"type":"user","uuid":"u-ws-h","timestamp":"2026-11-14T00:00:01.000Z","origin":{"kind":"human"},"message":{"role":"user","content":"ws turn"}}`
	wsAssistantLine = `{"type":"assistant","uuid":"a-ws-t","timestamp":"2026-11-14T00:00:02.000Z","message":{"role":"assistant","model":"glm-5.3","content":[{"type":"text","text":"ws reply"}]}}`
)

// --- 子协议鉴权 ---------------------------------------------------------------

func TestWSSubprotocolAuth(t *testing.T) {
	tp := newTestPlane(t, nil)

	// token 不等值 → 拒绝升级（401）。
	_, resp, err := dialWS(t, wsURL(tp, ""), []string{"webui", strings.Repeat("ee", 16)}, nil)
	if err == nil {
		t.Fatalf("错误子协议 token 竟升级成功")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误子协议 token resp = %v, want 401", resp)
	}
	var eb errorBody
	decodeJSON(t, resp, &eb)
	if eb.V != 1 || strings.Contains(eb.Error, testToken) {
		t.Fatalf("WS 错误体应含 v 无 token: %+v", eb)
	}

	// 无子协议无 Bearer → 401。
	_, resp, err = dialWS(t, wsURL(tp, ""), nil, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("裸连接应 401: err=%v resp=%v", err, resp)
	}

	// 子协议段数/首段形状错误 → 401。
	_, resp, err = dialWS(t, wsURL(tp, ""), []string{"webui", testToken, "extra"}, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("三段子协议应 401: err=%v resp=%v", err, resp)
	}
	_, resp, err = dialWS(t, wsURL(tp, ""), []string{"other", testToken}, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("首段非 webui 应 401: err=%v resp=%v", err, resp)
	}

	// Bearer 等值（无子协议）亦可升级（remote 反代出向两者都带）。
	conn, _, err := dialWS(t, wsURL(tp, ""), nil, map[string]string{"Authorization": "Bearer " + testToken})
	if err != nil {
		t.Fatalf("Bearer 升级失败: %v", err)
	}
	if proto := conn.Subprotocol(); proto != "" && proto != "webui" {
		t.Fatalf("Bearer 通道不应协商出子协议: %q", proto)
	}

	// 正确子协议对 → 升级且仅回显 "webui"（反代换写依赖该形状）。
	conn, _, err = dialWS(t, wsURL(tp, ""), []string{"webui", testToken}, nil)
	if err != nil {
		t.Fatalf("正确子协议对升级失败: %v", err)
	}
	if proto := conn.Subprotocol(); proto != "webui" {
		t.Fatalf("回显子协议 = %q, want webui", proto)
	}
	hello := readKind(t, conn, 2*time.Second, "hello")
	var he helloEvent
	if err := json.Unmarshal(hello.Event, &he); err != nil {
		t.Fatalf("hello 事件解码: %v", err)
	}
	if he.Stream == "" {
		t.Fatalf("hello.stream 为空")
	}
}

// --- hello → transcript 流与 seq 单调 ------------------------------------------

func TestWSHelloAndStream(t *testing.T) {
	tp := newTestPlane(t, nil)
	conn, _, err := dialWS(t, wsURL(tp, ""), []string{"webui", testToken}, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	hello := readKind(t, conn, 2*time.Second, "hello")
	var he helloEvent
	if err := json.Unmarshal(hello.Event, &he); err != nil {
		t.Fatalf("hello 解码: %v", err)
	}
	if hello.V != 1 || he.Cursor != 0 || he.Busy {
		t.Fatalf("空转录 hello 应 cursor=0 busy=false: %+v", hello)
	}
	lastSeq := hello.Seq

	// 追加两行（human + assistant 收尾）→ 一条 transcript 批事件。
	w := newTranscriptWriter(t, tp.transcriptPath())
	w.write(wsHumanLine, wsAssistantLine)

	env := readKind(t, conn, 2*time.Second, "transcript")
	if env.Seq <= lastSeq {
		t.Fatalf("seq 应单调递增: hello=%d transcript=%d", lastSeq, env.Seq)
	}
	lastSeq = env.Seq
	var te transcriptEvent
	if err := json.Unmarshal(env.Event, &te); err != nil {
		t.Fatalf("transcript 解码: %v", err)
	}
	if len(te.Items) != 2 || te.Items[0].UUID != "u-ws-h" || te.Items[1].UUID != "a-ws-t" {
		t.Fatalf("transcript items 错位: %+v", te.Items)
	}
	if te.Cursor <= 0 {
		t.Fatalf("transcript cursor = %d, want >0", te.Cursor)
	}
	if te.Items[0].IsHuman != true || te.Items[1].IsHuman != false {
		t.Fatalf("isHuman 投影错位")
	}

	// 再追加一行 → 新事件 seq 继续单调。
	w.write(`{"type":"user","uuid":"u-ws-h2","origin":{"kind":"human"},"message":{"role":"user","content":"second"}}`)
	env = readKind(t, conn, 2*time.Second, "transcript")
	if env.Seq <= lastSeq {
		t.Fatalf("追加后 seq 应递增: %d → %d", lastSeq, env.Seq)
	}
}

// --- ?since 重放 --------------------------------------------------------------

func TestWSSinceReplay(t *testing.T) {
	tp := newTestPlane(t, nil)

	conn, _, err := dialWS(t, wsURL(tp, ""), []string{"webui", testToken}, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	hello := readKind(t, conn, 2*time.Second, "hello")

	// 写入第一批并读到（hello 之后、断开前的最后 seq）。
	w := newTranscriptWriter(t, tp.transcriptPath())
	w.write(wsHumanLine, wsAssistantLine)
	tr := readKind(t, conn, 2*time.Second, "transcript")
	lastSeq := tr.Seq
	if lastSeq <= hello.Seq {
		t.Fatalf("重放基准 seq 异常: hello=%d tr=%d", hello.Seq, lastSeq)
	}
	_ = conn.Close()

	// 离线追加（无连接时发布进环形缓冲）。
	w.write(`{"type":"user","uuid":"u-ws-off","origin":{"kind":"human"},"message":{"role":"user","content":"offline"}}`)
	time.Sleep(150 * time.Millisecond)

	// 重连 ?since=<lastSeq>：hello.seq == since，随后重放缺失事件。
	conn2, _, err := dialWS(t, wsURL(tp, "since="+strconv.FormatInt(lastSeq, 10)), []string{"webui", testToken}, nil)
	if err != nil {
		t.Fatalf("re-dial: %v", err)
	}
	h2 := readKind(t, conn2, 2*time.Second, "hello")
	if h2.Seq != lastSeq {
		t.Fatalf("重连 hello.seq = %d, want == since %d", h2.Seq, lastSeq)
	}
	var he2 helloEvent
	_ = json.Unmarshal(h2.Event, &he2)
	if he2.Stream == "" {
		t.Fatalf("重连 hello 仍需 stream 身份")
	}

	// 重放应包含离线追加的记录，且 seq > since、单调。
	maxSeq := h2.Seq
	replayed := 0
	deadline := time.Now().Add(2 * time.Second)
	sawOffline := false
	for time.Now().Before(deadline) {
		env := readEnvelope(t, conn2, time.Until(deadline))
		if env.Seq <= maxSeq {
			t.Fatalf("重放 seq 非单调: %d after %d", env.Seq, maxSeq)
		}
		maxSeq = env.Seq
		if env.Kind == "transcript" {
			replayed++
			var te transcriptEvent
			_ = json.Unmarshal(env.Event, &te)
			for _, item := range te.Items {
				if item.UUID == "u-ws-off" {
					sawOffline = true
				}
			}
		}
		if sawOffline && replayed >= 1 {
			break
		}
	}
	if !sawOffline {
		t.Fatalf("?since 重放未见离线事件（缺失）")
	}
}

// --- 缺口信号（floor 抬升）------------------------------------------------------

func TestWSGapSignal(t *testing.T) {
	tp := newTestPlane(t, func(cfg *PlaneConfig) { cfg.Settings.RingBuffer = 2 })
	w := newTranscriptWriter(t, tp.transcriptPath())

	// 4 轮 human/assistant 交替写入；每轮间隔远大于轮询周期，确保每轮恰好
	// 产出一条 transcript 事件（≥4 个事件），环形缓冲（cap=2）溢出丢弃头部。
	for i := 0; i < 4; i++ {
		n := strconv.Itoa(i)
		w.write(`{"type":"user","uuid":"u-g` + n + `","origin":{"kind":"human"},"message":{"role":"user","content":"g"}}`)
		w.write(`{"type":"assistant","uuid":"a-g` + n + `","message":{"role":"assistant","model":"m","content":[{"type":"text","text":"r"}]}}`)
		time.Sleep(80 * time.Millisecond)
	}

	conn, _, err := dialWS(t, wsURL(tp, "since=0"), []string{"webui", testToken}, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	hello := readKind(t, conn, 2*time.Second, "hello")
	var he helloEvent
	_ = json.Unmarshal(hello.Event, &he)
	if he.Floor <= 1 {
		t.Fatalf("环形缓冲溢出后 floor = %d, want >1（缺口信号）", he.Floor)
	}
	if hello.Seq != 0 {
		t.Fatalf("since=0 时 hello.seq = %d, want 0", hello.Seq)
	}
	// 首个重放帧 seq 必然越过 1（被丢弃的头部）——客户端据此判定缺口并全量
	// refetch /api/history。
	env := readEnvelope(t, conn, 2*time.Second)
	if env.Seq <= 1 {
		t.Fatalf("缺口后首帧 seq = %d, want >1", env.Seq)
	}
}

// --- 停止时 bye（尽力而为）------------------------------------------------------

func TestWSByeOnStop(t *testing.T) {
	tp := newTestPlane(t, nil)
	conn, _, err := dialWS(t, wsURL(tp, ""), []string{"webui", testToken}, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readKind(t, conn, 2*time.Second, "hello")

	tp.m.Stop(tp.cfg.SessionID) // t.Cleanup 的 Stop 幂等

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var env envelope
	if err := conn.ReadJSON(&env); err != nil {
		t.Fatalf("等待 bye: %v", err)
	}
	if env.Kind != "bye" {
		t.Fatalf("停止后首帧 kind = %q, want bye", env.Kind)
	}
}
