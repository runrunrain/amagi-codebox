package claudewebui

// ws.go — /ws/events 事件流（design §5.2 ws、§6.2）。
//
// 契约要点：
//   - 升级前手工解析 Sec-WebSocket-Protocol：恰两段且第一段=="webui"、
//     第二段 constant-time 等于 token 才升级（浏览器 WS 无法带 Authorization
//     头，子协议对是唯一浏览器侧凭证通道）；Bearer 等值亦可（remote 反代
//     出向两者都带，webui_proxy.go 的子协议换写依赖本形状）。不用
//     Upgrader.Subprotocols（手工裁决），响应仅回显 "webui"。
//   - 信封 {v:1,seq,ts,kind,event}；seq 服务端单调递增，hello 携带的 seq 是
//     「服务端认定的续传点」（= 客户端 ?since 或 floor-1），随后重放与直播
//     事件的 seq 严格递增——同一连接内单调。
//   - 连接即推 hello {stream,floor,cursor,busy}：stream 是本 plane 实例
//     uuid（重连可识别流重建，pi v1.0.13 教训）；floor 是环形缓冲最旧 seq，
//     since < floor 即缺口信号——客户端全量 refetch /api/history。
//   - 环形缓冲 Settings.RingBuffer（默认 4096），?since=<seq> 重放其后事件。
//   - 纯下行：上行帧一律读丢（读循环只为驱动控制帧与对端关闭感知）。
//   - bye（尽力而为）：plane 停止时广播一次再关连接。

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/logging"

	"github.com/gorilla/websocket"
)

// connSendBuffer 是单连接的发送队列深度；写满视为慢消费者，断开重连
// （重连即重放，无数据损失）。
const connSendBuffer = 256

// writeDeadline 是单帧写超时。
const writeDeadline = 10 * time.Second

// envelope 是全部下行帧的统一信封（design §6.2）。
type envelope struct {
	V     int             `json:"v"`
	Seq   int64           `json:"seq"`
	Ts    int64           `json:"ts"`
	Kind  string          `json:"kind"`
	Event json.RawMessage `json:"event"`
}

// helloEvent 是 kind=="hello" 的载荷（连接即推）。
type helloEvent struct {
	Stream string `json:"stream"`
	Floor  int64  `json:"floor"`
	Cursor int64  `json:"cursor"`
	Busy   bool   `json:"busy"`
}

// byeEvent 是 kind=="bye" 的载荷（plane 停止，尽力而为）。
type byeEvent struct{}

// hub 是 per-plane 的事件广播器（seq 分配 + 环形缓冲 + 连接管理）。
type hub struct {
	cap      int
	log      *logging.Service
	stateFn  func() helloState // tailer 实时游标/忙闲（hello 用）
	now      func() time.Time
	streamID string

	mu    sync.Mutex
	next  int64 // 下一个待分配 seq（已分配的最大 seq = next-1）
	ring  []envelope
	conns map[*wsConn]struct{}
}

func newHub(set RuntimeSettings, log *logging.Service, stateFn func() helloState) *hub {
	return &hub{
		cap: set.RingBuffer, log: log, stateFn: stateFn,
		now: time.Now, streamID: newStreamID(),
		next: 1, // seq 从 1 起：0 保留为「尚无任何事件」的续传点
	}
}

// newStreamID 生成 plane 实例 uuid（hex；仅作流身份标识，非凭证）。
func newStreamID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "stream-unknown" // rand 失败退化为常数身份（可观测非失败）
	}
	return hex.EncodeToString(buf)
}

// floor 返回环形缓冲最旧 seq；缓冲空时返回 next（= 任何更早 seq 均缺口）。
// 调用方必须持有 h.mu。
func (h *hub) floorLocked() int64 {
	if len(h.ring) > 0 {
		return h.ring[0].Seq
	}
	return h.next
}

// publish 分配 seq、入环形缓冲并广播到全部连接（慢消费者断开）。
func (h *hub) publish(kind string, event any) {
	env := envelope{V: 1, Ts: h.now().UnixMilli(), Kind: kind, Event: mustJSON(event)}

	h.mu.Lock()
	env.Seq = h.next
	h.next++
	h.ring = append(h.ring, env)
	if int(len(h.ring)) > h.cap {
		h.ring = h.ring[len(h.ring)-h.cap:]
	}
	var doomed []*wsConn
	for c := range h.conns {
		select {
		case c.send <- env:
		default:
			doomed = append(doomed, c) // 慢消费者：断开（客户端重连重放）
		}
	}
	for _, c := range doomed {
		delete(h.conns, c)
	}
	h.mu.Unlock()

	for _, c := range doomed {
		c.close()
	}
}

// shutdown 广播 bye（尽力而为）后关闭全部连接（plane 停止路径）。
func (h *hub) shutdown() {
	h.publish("bye", byeEvent{})
	h.mu.Lock()
	conns := make([]*wsConn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.conns = make(map[*wsConn]struct{})
	h.mu.Unlock()
	for _, c := range conns {
		c.close()
	}
}

// serveWS 处理 /ws/events：鉴权（子协议对 / Bearer）→ 升级 → 注册 → 读写循环。
func (h *hub) serveWS(w http.ResponseWriter, r *http.Request, token string) {
	if !wsAuthorized(r, token) {
		writeAPIError(w, r, http.StatusUnauthorized, errUnauthorized)
		return
	}
	since, hasSince := parseSince(r)

	// 子协议已手工裁决通过，仅回显 "webui"（remote 反代依赖该形状；不用
	// Upgrader.Subprotocols）。CheckOrigin 放开：鉴权只认 token，Origin 白
	// 名单在此无增量价值（sandbox opaque iframe 的 Origin 是 null）。
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(*http.Request) bool { return true },
	}
	responseHeader := http.Header{}
	responseHeader.Set("Sec-Websocket-Protocol", "webui")
	conn, err := upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		return // Upgrade 已写握手错误响应
	}
	c := &wsConn{conn: conn, send: make(chan envelope, connSendBuffer), done: make(chan struct{})}

	prefix := h.register(c, since, hasSince)
	go c.readLoop()        // 上行帧读丢
	go c.writeLoop(prefix) // 先 hello+重放，后直播流
}

// register 原子完成「续传点裁决 + hello/重放快照 + 连接登记」——与 publish
// 同锁临界区，杜绝快照与登记之间的事件丢失。
func (h *hub) register(c *wsConn, since int64, hasSince bool) []envelope {
	h.mu.Lock()
	defer h.mu.Unlock()

	resume := h.floorLocked() - 1 // 新鲜连接（无 since）：从 floor 前起步，不重放
	if resume < 0 {
		resume = 0
	}
	if hasSince {
		resume = since
		if resume > h.next-1 {
			resume = h.next - 1 // 越过尾部的 since 收敛到尾部（不重放）
		}
		if resume < 0 {
			resume = 0
		}
	}
	st := h.stateFn()
	prefix := []envelope{{
		V: 1, Seq: resume, Ts: h.now().UnixMilli(), Kind: "hello",
		Event: mustJSON(helloEvent{Stream: h.streamID, Floor: h.floorLocked(), Cursor: st.Cursor, Busy: st.Busy}),
	}}
	for _, env := range h.ring {
		if env.Seq > resume {
			prefix = append(prefix, env)
		}
	}
	if h.conns == nil {
		h.conns = make(map[*wsConn]struct{})
	}
	h.conns[c] = struct{}{}
	return prefix
}

// wsAuthorized：子协议对 `webui, <token>`（constant-time）或 Bearer 等值。
func wsAuthorized(r *http.Request, token string) bool {
	if token == "" {
		return false // fail-closed（Manager.Start 已强制非空，双保险）
	}
	if bearerAuthorized(r, token) {
		return true
	}
	parts := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")
	if len(parts) != 2 {
		return false
	}
	first, second := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if first != "webui" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(second), []byte(token)) == 1
}

// parseSince 解析 ?since=<seq>；缺失/非法/负值视同未携带（不重放）。
func parseSince(r *http.Request) (int64, bool) {
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// mustJSON 序列化事件载荷；本包事件结构不可失败，异常输入退化为 "{}"。
func mustJSON(event any) json.RawMessage {
	data, err := json.Marshal(event)
	if err != nil {
		return json.RawMessage("{}")
	}
	return data
}

// wsConn 是一条已升级连接（发送队列 + 关闭信号）。
//
// 关闭纪律：close() 只发 done 信号；底层 conn 由 writeLoop 在排空发送队列
// （含停止前入队的 bye）之后自行 Close——否则 shutdown 的 bye 会在写出去
// 之前被 TCP 关闭吞掉。
type wsConn struct {
	conn      *websocket.Conn
	send      chan envelope
	done      chan struct{}
	closeOnce sync.Once
}

func (c *wsConn) close() {
	c.closeOnce.Do(func() { close(c.done) })
}

// writeLoop 先发 hello+重放前缀，再转发直播事件；done 关闭后尽力清空队列
// （含 bye）再退出并关闭底层连接。
func (c *wsConn) writeLoop(prefix []envelope) {
	defer func() {
		c.close()
		_ = c.conn.Close()
	}()
	for _, env := range prefix {
		if !c.write(env) {
			return
		}
	}
	for {
		select {
		case env := <-c.send:
			if !c.write(env) {
				return
			}
		case <-c.done:
			for { // 尽力而为：把队列里已入队的尾部事件（含 bye）发完
				select {
				case env := <-c.send:
					if !c.write(env) {
						return
					}
				default:
					return
				}
			}
		}
	}
}

func (c *wsConn) write(env envelope) bool {
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
	return c.conn.WriteJSON(env) == nil
}

// readLoop 纯读丢（驱动控制帧处理与对端关闭感知）。
func (c *wsConn) readLoop() {
	defer c.close()
	c.conn.SetReadLimit(1 << 20)
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}
