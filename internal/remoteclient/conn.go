// conn.go 连接生命周期（蓝图 §3/§5/§6 流程 2）：
//
// 每会话一条连接 goroutine（TerminalSession）。状态机见 ConnState；重连退避
// 1s→2s→…→30s 封顶，rate.limited 使下一轮退避加倍；auth.revoked 事件或 WS
// Close 1008 → 立即置 revoked 态并停止重连（fail-closed，广播 rc:revoked 与
// rc:host-health:revoked）；1000/1002/1009 为契约终态关闭码，同样停止重连。
// 连接状态与远端事件经事件总线回流前端（EventEmitter 注入，App 层桥接
// wails EventsEmit）：
//
//	rc:conn-state      {sessionId, hostId, state, attempt, nextRetryMs, detail}
//	rc:session-state   {sessionId, state, restartBoundary?, seq?, occurredAt}
//	rc:terminal-output {sessionId, seq, data} 或 gap 通知 {sessionId, gap:{fromSeq,toSeq,source}}
//	rc:control-state   {sessionId, state, deviceName?, reason, occurredAt}
//	rc:revoked         {hostId, sessionId?}
//	rc:host-health     {hostId, state}
//
// 输入一律经 outbox（CG-03 幂等）；session restart 边界不重置 outbox（幂等
// scope = SessionID lifetime），仅 Detach/移除终结。

package remoteclient

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"amagi-codebox/internal/remote/contract"
)

// ConnState 是客户端连接状态机的本地投影（蓝图 §5）。远端事实仍以服务端
// 事件为准；本状态机只描述客户端侧连接视图。
type ConnState string

const (
	ConnDisconnected ConnState = "disconnected"
	ConnConnecting   ConnState = "connecting"
	ConnAttached     ConnState = "attached"
	ConnReadonly     ConnState = "readonly"
	ConnDegraded     ConnState = "degraded"
)

// TerminalConfig 是 TerminalSession/TerminalManager 的可注入配置（测试把
// Backoff/时钟缩到可观测窗口；生产用 DefaultTerminalConfig）。
type TerminalConfig struct {
	HandshakeTimeout time.Duration
	BackoffBase      time.Duration // 首轮重连退避（默认 1s）
	BackoffMax       time.Duration // 退避封顶（默认 30s）
	PingInterval     time.Duration // 应用层 ping；0 关闭
	// Now/After 为时钟 seam（测试确定性）。
	Now   func() time.Time
	After func(ctx context.Context, d time.Duration) error
}

// DefaultTerminalConfig 返回生产配置（退避 1s→30s 封顶、ping 30s）。
func DefaultTerminalConfig() TerminalConfig {
	return TerminalConfig{
		HandshakeTimeout: wsHandshakeTimeout,
		BackoffBase:      1 * time.Second,
		BackoffMax:       30 * time.Second,
		PingInterval:     30 * time.Second,
		Now:              time.Now,
		After: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
	}
}

func (c TerminalConfig) withDefaults() TerminalConfig {
	def := DefaultTerminalConfig()
	if c.HandshakeTimeout == 0 {
		c.HandshakeTimeout = def.HandshakeTimeout
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = def.BackoffBase
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = def.BackoffMax
	}
	if c.Now == nil {
		c.Now = def.Now
	}
	if c.After == nil {
		c.After = def.After
	}
	return c
}

// EventEmitter 是前端事件总线 seam：App 层桥接 wails EventsEmit；测试注入
// 录制器。payload 形状见文件头；事件名常量在下方冻结（蓝图 §7）。
type EventEmitter interface {
	Emit(name string, payload any)
}

// 事件名冻结清单（蓝图 §7）。
const (
	EventConnState      = "rc:conn-state"
	EventSessionState   = "rc:session-state"
	EventTerminalOutput = "rc:terminal-output"
	EventControlState   = "rc:control-state"
	EventRevoked        = "rc:revoked"
	EventHostHealth     = "rc:host-health"
)

// errTerminalDetached 是 Detach 后方法的统一错误。
var errTerminalDetached = errors.New("remoteclient: terminal session is detached")

// ---------------------------------------------------------------------------
// TerminalSession
// ---------------------------------------------------------------------------

// TerminalSession 是单会话的 /ws/v1 长连接生命周期：拨号→attach→事件分发→
// 断线退避重连，持 ReplayTracker（前沿/backfill）与 InputOutbox（输入幂等）。
type TerminalSession struct {
	t         *Transport
	hostID    string
	sessionID contract.SessionID
	cfg       TerminalConfig
	emitter   EventEmitter

	tracker *ReplayTracker
	outbox  *InputOutbox

	mu          sync.Mutex
	state       ConnState
	stopped     bool // Detach/终态：停止重连
	revoked     bool
	attempt     int  // 当前重连轮次（attach 成功清零）
	rateLimited bool // 见过 rate.limited：下一轮退避加倍
	inputModeOK bool // session.attached 协商出 session-window-v1
	controlYou  bool
	ws          *websocket.Conn

	writeMu  sync.Mutex
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// newTerminalSession 构造并启动连接循环（run 在新 goroutine）。
func newTerminalSession(t *Transport, hostID string, sessionID contract.SessionID, emitter EventEmitter, cfg TerminalConfig) *TerminalSession {
	cfg = cfg.withDefaults()
	s := &TerminalSession{
		t:         t,
		hostID:    hostID,
		sessionID: sessionID,
		cfg:       cfg,
		emitter:   emitter,
		tracker:   NewReplayTracker(),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	s.outbox = NewInputOutbox(s.sendInputFrame, DefaultInputOutboxConfig())
	go func() {
		defer close(s.done)
		s.run()
	}()
	return s
}

// logf 记录净化诊断（不含凭据/payload；App 未注入 logger 时丢弃）。
func (s *TerminalSession) logf(format string, args ...any) {
	// 预留：App 层后续经 TerminalConfig 注入 logger；当前静默。
	_ = fmt.Sprintf(format, args...)
}

// State 返回当前连接状态投影。
func (s *TerminalSession) State() ConnState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// SessionID 返回会话 ID。
func (s *TerminalSession) SessionID() contract.SessionID { return s.sessionID }

// Detach 终止会话：停重连、关连接、销毁 outbox（幂等 scope 终结），等待
// goroutine 退出。幂等可重入。
func (s *TerminalSession) Detach() error {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.mu.Lock()
		s.stopped = true
		ws := s.ws
		s.mu.Unlock()
		if ws != nil {
			_ = ws.Close()
		}
	})
	<-s.done
	s.mu.Lock()
	s.state = ConnDisconnected
	s.mu.Unlock()
	return nil
}

// SendInput 接收一段终端输入文本（UTF-8），编码 base64 后经 outbox 发送。
// 未 attach / 无 inputAckMode / 控制权不在我 → 明确错误（CG-03：新客户端在
// 能力不可用时不得发 input）。
func (s *TerminalSession) SendInput(text string) error {
	s.mu.Lock()
	state, stopped := s.state, s.stopped
	s.mu.Unlock()
	if stopped {
		return errTerminalDetached
	}
	if state != ConnAttached {
		return fmt.Errorf("remoteclient: terminal input requires attached state (now %s)", state)
	}
	if text == "" {
		return errors.New("remoteclient: terminal input is empty")
	}
	data := base64.StdEncoding.EncodeToString([]byte(text))
	if reason, ok := s.outbox.Accept(data); !ok {
		return fmt.Errorf("remoteclient: input rejected (%s)", string(reason))
	}
	return nil
}

// Resize 发送 resize 帧（attached/degraded 才发；cols/rows 正整数）。
func (s *TerminalSession) Resize(cols, rows int) error {
	s.mu.Lock()
	state, stopped := s.state, s.stopped
	s.mu.Unlock()
	if stopped {
		return errTerminalDetached
	}
	if state != ConnAttached && state != ConnDegraded {
		return fmt.Errorf("remoteclient: resize requires attached state (now %s)", state)
	}
	if cols < 1 || rows < 1 {
		return errors.New("remoteclient: resize requires positive cols/rows")
	}
	return s.writeFrame(contract.ResizeFrame{Type: contract.ClientFrameTypeResize, RequestID: s.newRequestID(), Cols: cols, Rows: rows})
}

// ---------------------------------------------------------------------------
// 连接循环
// ---------------------------------------------------------------------------

func (s *TerminalSession) run() {
	defer func() {
		s.outbox.Dispose()
		s.mu.Lock()
		s.state = ConnDisconnected
		s.mu.Unlock()
	}()
	for {
		s.mu.Lock()
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return
		}
		terminal, backoff := s.runOnce()
		if terminal {
			return
		}
		if s.waitBackoff(backoff) {
			return
		}
	}
}

// runOnce 执行一轮 拨号→attach→读泵。返回 (是否终态停止, 退避时长)。
func (s *TerminalSession) runOnce() (terminal bool, backoff time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	s.mu.Lock()
	s.attempt++
	attempt := s.attempt
	s.mu.Unlock()
	s.setState(ConnConnecting, fmt.Sprintf("attempt %d", attempt))

	conn, err := dialWebSocket(ctx, s.t, s.cfg.HandshakeTimeout)
	if err != nil {
		if ctx.Err() != nil {
			return true, 0 // Detach/取消：静默退出
		}
		s.logf("ws dial failed: %v", err)
		return false, s.nextBackoff()
	}
	s.mu.Lock()
	s.ws = conn
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.ws = nil
		s.mu.Unlock()
		_ = conn.Close()
	}()

	// attach：首帧必须是 attach；lastSeq 仅持有 replay frame 时携带
	//（omitted 与 0 语义有别，design §7.3）。
	attach := contract.AttachFrame{
		Type:       contract.ClientFrameTypeAttach,
		RequestID:  s.newRequestID(),
		APIVersion: contract.APIVersionV1,
		SessionID:  s.sessionID,
	}
	if s.tracker.AttachWithLastSeq() {
		ls := s.tracker.LastSeq()
		attach.LastSeq = &ls
	}
	if err := s.writeFrame(attach); err != nil {
		s.logf("attach send failed: %v", err)
		s.outbox.Pause()
		return false, s.nextBackoff()
	}

	if terminal := s.readPump(conn, attach.RequestID); terminal {
		return true, 0
	}
	// 非终态退出（读错误/非终态关闭）：退避后重连。
	return false, s.nextBackoff()
}

// readPump 消费一条连接上的全部服务端事件，直至读错误/关闭/Detach。
// 返回 true 表示终态（停止重连）。退出时 Pause outbox（重连等待期间不
// 烧 attempt；Resume 由下一轮 attached 确认触发）。
func (s *TerminalSession) readPump(conn *websocket.Conn, attachRid contract.RequestID) bool {
	defer s.outbox.Pause()
	var (
		attachedSeen bool
		pingTicker   *time.Ticker
		pingStop     = make(chan struct{})
	)
	defer func() {
		if pingTicker != nil {
			pingTicker.Stop()
		}
		close(pingStop)
	}()
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return s.handleReadError(err)
		}
		ev := decodeServerEvent(raw)
		if ev.Unknown != nil {
			// 契约 §2.3：未知/畸形事件不终止连接；记净化诊断。畸形关键帧
			//（attached/input.ack）按能力缺失/不结算处理。
			s.logf("unknown server event sanitized: type=%q reason=%s", ev.Unknown.WireType, ev.Unknown.Reason)
			switch ev.Unknown.WireType {
			case contract.ServerEventTypeSessionAttached:
				if !attachedSeen {
					// 畸形 attached：无法证明 inputAckMode，按能力缺失只读。
					s.mu.Lock()
					s.inputModeOK = false
					s.mu.Unlock()
					s.refreshInputGate()
				}
			}
			continue
		}
		switch known := ev.Known.(type) {
		case contract.SessionAttachedEvent:
			if attachedSeen || (attachRid != "" && known.RequestID != attachRid) {
				continue // 重复/不相关 attached：忽略
			}
			attachedSeen = true
			s.onAttached(known)
			if s.cfg.PingInterval > 0 && pingTicker == nil {
				pingTicker = time.NewTicker(s.cfg.PingInterval)
				go s.pingLoop(pingTicker, pingStop)
			}
		case contract.OutputEvent:
			s.onOutput(known)
		case contract.SessionRestartBoundaryEvent:
			res := s.tracker.OnRestartBoundary(known)
			s.applyOutputOutcome(res)
			s.emitter.Emit(EventSessionState, map[string]any{
				"sessionId": string(s.sessionID), "state": string(known.State),
				"restartBoundary": true, "seq": uint64(known.Seq), "occurredAt": known.OccurredAt,
			})
		case contract.SessionStateEvent:
			s.emitter.Emit(EventSessionState, map[string]any{
				"sessionId": string(s.sessionID), "state": string(known.State), "occurredAt": known.OccurredAt,
			})
			// 会话移除：幂等 scope 终结，停止重连（removed broadcast 后服务端
			// 关闭连接）。
			if known.State == contract.SessionStateRemoved {
				s.mu.Lock()
				s.stopped = true
				s.mu.Unlock()
				s.outbox.Dispose()
				return true
			}
		case contract.ControlStateEvent:
			s.mu.Lock()
			s.controlYou = known.State == contract.ControlStateYou
			s.mu.Unlock()
			s.refreshInputGate()
			s.emitter.Emit(EventControlState, map[string]any{
				"sessionId": string(s.sessionID), "state": string(known.State),
				"reason": known.Reason, "occurredAt": known.OccurredAt,
			})
		case contract.BackfillResultEvent:
			s.onBackfillResult(known)
		case contract.InputAckEvent:
			s.outbox.OnAck(known.ID, known.RequestID)
		case contract.AuthRevokedEvent:
			s.onRevoked("auth.revoked event")
			return true
		case contract.ErrorEvent:
			s.onErrorEvent(known)
		}
	}
}

// onAttached 处理 session.attached：重置退避轮次、消费 history、协商 gap、
// 恢复 outbox、刷新输入能力门。
func (s *TerminalSession) onAttached(ev contract.SessionAttachedEvent) {
	s.mu.Lock()
	s.attempt = 0
	s.rateLimited = false
	s.controlYou = ev.Snapshot.Control.State == contract.ControlStateYou
	modeOK := ev.InputAckMode != nil && *ev.InputAckMode == contract.InputAckModeSessionWindowV1
	s.inputModeOK = modeOK
	s.mu.Unlock()

	out := s.tracker.OnAttached(ev)
	for _, fr := range out.Frames {
		s.emitReplayFrame(fr)
	}
	if out.Gap != nil && out.Gap.From >= 1 && out.Gap.To >= out.Gap.From {
		s.emitGapNotice(out.Gap)
		if err := s.requestBackfill(out.Gap.From, out.Gap.To); err != nil {
			s.logf("backfill request (attached gap) failed: %v", err)
		}
	}
	s.outbox.Resume()
	s.refreshInputGate()
	s.emitter.Emit(EventSessionState, map[string]any{
		"sessionId": string(s.sessionID), "state": string(ev.Snapshot.Session.State),
	})
	s.emitter.Emit(EventControlState, map[string]any{
		"sessionId": string(s.sessionID), "state": string(ev.Snapshot.Control.State),
		"reason": "attach-snapshot",
	})
}

// onOutput 处理 live output：单调消费、洞→backfill、重复丢弃。
func (s *TerminalSession) onOutput(ev contract.OutputEvent) {
	s.applyOutputOutcome(s.tracker.OnOutput(ev))
}

// applyOutputOutcome 把 tracker 结论落到投递/重连/状态投影。
func (s *TerminalSession) applyOutputOutcome(res OutputOutcome) {
	for _, fr := range res.Frames {
		s.emitReplayFrame(fr)
	}
	if res.NeedBackfill != nil {
		if err := s.requestBackfill(res.NeedBackfill.From, res.NeedBackfill.To); err != nil {
			s.logf("backfill request failed: %v", err)
		}
	}
	if res.NeedReconnect {
		s.logf("replay tracker requested conservative reconnect")
		s.forceReconnect()
	}
	s.refreshInputGate()
}

// onBackfillResult 处理 backfill.result：frames 入账投递；gap 如实上报并把
// 追加洞续发。
func (s *TerminalSession) onBackfillResult(ev contract.BackfillResultEvent) {
	out := s.tracker.OnBackfillResult(ev)
	if out.Unknown {
		return
	}
	for _, fr := range out.Frames {
		s.emitReplayFrame(fr)
	}
	if out.Gap != nil {
		s.emitGapNotice(out.Gap)
	}
	if out.NextHole != nil {
		if err := s.requestBackfill(out.NextHole.From, out.NextHole.To); err != nil {
			s.logf("follow-up backfill request failed: %v", err)
		}
	}
	s.refreshInputGate()
}

// onErrorEvent 处理 error 事件：auth.revoked 家族 → fail-closed；rate.limited
// → 下一轮退避加倍；control.forbidden → 冻结 pending（权威丢失，迟到 ACK
// 仍可结算）。
func (s *TerminalSession) onErrorEvent(ev contract.ErrorEvent) {
	switch ev.Code {
	case contract.ErrorCodeAuthRevoked:
		s.onRevoked("auth.revoked error event")
	case contract.ErrorCodeRateLimited:
		s.mu.Lock()
		s.rateLimited = true
		s.mu.Unlock()
	case contract.ErrorCodeControlForbidden:
		s.outbox.FreezePending()
		s.refreshInputGate()
	}
	s.logf("server error event: code=%s layer=%s hint=%s", ev.Code, ev.Layer, ev.ActionHint)
}

// handleReadError 分类读错误：终态关闭码 → 停止；1008 → revoked fail-closed；
// 其余 → 重连退避。返回 true 表示终态。
func (s *TerminalSession) handleReadError(err error) bool {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		switch closeErr.Code {
		case contract.AuthRevokedCloseCode:
			s.onRevoked(fmt.Sprintf("ws close %d", closeErr.Code))
			return true
		case 1000, 1002, 1009:
			s.logf("terminal ws close %d: %s", closeErr.Code, closeErr.Text)
			s.mu.Lock()
			s.stopped = true
			s.mu.Unlock()
			s.outbox.Halt()
			s.setState(ConnDisconnected, fmt.Sprintf("closed %d", closeErr.Code))
			return true
		}
		s.logf("ws closed %d: %s", closeErr.Code, closeErr.Text)
		return false
	}
	s.logf("ws read error: %v", err)
	return false
}

// onRevoked 是 fail-closed 撤销路径：停重连、halt outbox、广播 revoked。
// 注意：emitter 回调里不得同步 Detach 本会话（读泵 goroutine 正在栈上）。
func (s *TerminalSession) onRevoked(cause string) {
	s.mu.Lock()
	s.stopped = true
	s.revoked = true
	s.mu.Unlock()
	s.outbox.Halt()
	s.emitter.Emit(EventRevoked, map[string]any{"hostId": s.hostID, "sessionId": string(s.sessionID)})
	s.emitter.Emit(EventHostHealth, map[string]any{"hostId": s.hostID, "state": string(HealthRevoked)})
	s.setState(ConnDisconnected, "revoked: "+cause)
}

// ---------------------------------------------------------------------------
// 状态投影与工具
// ---------------------------------------------------------------------------

// refreshInputGate 依据 inputModeOK/controlYou/在途 backfill 重算状态投影：
// degraded（历史洞未决）> readonly（输入能力缺失或控制权不在我）> attached。
// 仅在状态变化时广播（输出高频路径不刷事件总线）。
func (s *TerminalSession) refreshInputGate() {
	s.mu.Lock()
	if s.stopped || s.state == ConnDisconnected {
		s.mu.Unlock()
		return
	}
	next := s.state
	if s.tracker.InFlightBackfills() > 0 {
		next = ConnDegraded
	} else if s.inputModeOK && s.controlYou {
		next = ConnAttached
	} else {
		next = ConnReadonly
	}
	changed := next != s.state
	s.state = next
	s.mu.Unlock()
	if changed {
		s.setState(next, "")
	}
}

// setState 广播连接状态（自身加锁；不得在持 s.mu 时调用）。
func (s *TerminalSession) setState(state ConnState, detail string) {
	if s.emitter == nil {
		return
	}
	s.mu.Lock()
	s.state = state
	attempt := s.attempt
	next := s.nextBackoffLocked()
	s.mu.Unlock()
	s.emitter.Emit(EventConnState, map[string]any{
		"sessionId":   string(s.sessionID),
		"hostId":      s.hostID,
		"state":       string(state),
		"attempt":     attempt,
		"nextRetryMs": next.Milliseconds(),
		"detail":      detail,
	})
}

// nextBackoffLocked 计算下一轮退避：base<<(attempt-1)，封顶 Max；rate.limited
// 再加倍（同样封顶）。调用方持锁。
func (s *TerminalSession) nextBackoffLocked() time.Duration {
	d := s.cfg.BackoffBase
	for i := 1; i < s.attempt && d < s.cfg.BackoffMax; i++ {
		d *= 2
	}
	if d > s.cfg.BackoffMax {
		d = s.cfg.BackoffMax
	}
	if s.rateLimited {
		d *= 2
		if d > s.cfg.BackoffMax {
			d = s.cfg.BackoffMax
		}
	}
	return d
}

func (s *TerminalSession) nextBackoff() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextBackoffLocked()
}

// waitBackoff 等待退避；返回 true 表示被取消（Detach）。
func (s *TerminalSession) waitBackoff(d time.Duration) bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return s.cfg.After(ctx, d) != nil
}

// forceReconnect 关闭当前连接（读泵将以错误退出并按退避重连）。
func (s *TerminalSession) forceReconnect() {
	s.mu.Lock()
	ws := s.ws
	s.mu.Unlock()
	if ws != nil {
		_ = ws.Close()
	}
}

// pingLoop 周期发送应用层 ping（仅刷新 liveness，无业务载荷）。
func (s *TerminalSession) pingLoop(ticker *time.Ticker, stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-s.done:
			return
		case <-ticker.C:
			if err := s.writeFrame(contract.PingFrame{Type: contract.ClientFrameTypePing, RequestID: s.newRequestID()}); err != nil {
				return
			}
		}
	}
}

// writeFrame 编码并写一帧（写互斥；编码失败 fail-closed 不发送）。
func (s *TerminalSession) writeFrame(f contract.ClientFrame) error {
	raw, err := encodeClientFrame(f)
	if err != nil {
		return err
	}
	s.mu.Lock()
	ws := s.ws
	s.mu.Unlock()
	if ws == nil {
		return errors.New("remoteclient: no websocket connection")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return ws.WriteMessage(websocket.TextMessage, raw)
}

// sendInputFrame 是 outbox 的 wire seam。
func (s *TerminalSession) sendInputFrame(f contract.InputFrame) bool {
	return s.writeFrame(f) == nil
}

// requestBackfill 发送 backfill 请求并登记在途（供 tracker 关联 result）。
func (s *TerminalSession) requestBackfill(from, to contract.Seq) error {
	if from < 1 || to < from {
		return fmt.Errorf("invalid backfill range [%d,%d]", from, to)
	}
	rid := s.newRequestID()
	s.tracker.RegisterBackfill(rid, from, to)
	return s.writeFrame(contract.BackfillFrame{Type: contract.ClientFrameTypeBackfill, RequestID: rid, FromSeq: from, ToSeq: to})
}

// emitReplayFrame 把已消费的 replay 帧投递到事件总线（output 解码交给前端
// 流式 TextDecoder；这里只透传 base64 与 seq；restart boundary 不产终端
// 输出，由 rc:session-state 单独广播）。
func (s *TerminalSession) emitReplayFrame(fr contract.ReplayFrame) {
	if out, ok := fr.(contract.OutputEvent); ok {
		s.emitter.Emit(EventTerminalOutput, map[string]any{
			"sessionId": string(s.sessionID), "seq": uint64(out.Seq), "data": out.Chunk,
		})
	}
}

// emitGapNotice 如实上报缺口（不吞不改；上层决策 continue-from-latest）。
func (s *TerminalSession) emitGapNotice(g *GapNotice) {
	s.emitter.Emit(EventTerminalOutput, map[string]any{
		"sessionId": string(s.sessionID),
		"gap":       map[string]any{"fromSeq": uint64(g.From), "toSeq": uint64(g.To), "source": string(g.Source)},
	})
}

// newRequestID 生成本连接帧的 canonical requestId（req-v1- + 32 hex；熵失败
// 退化为全零确定性 canonical——crypto/rand 失败极罕见）。
func (s *TerminalSession) newRequestID() contract.RequestID {
	if rid, err := canonicalRequestID(randomBytes); err == nil && contract.IsCanonicalRequestID(rid) {
		return rid
	}
	return "req-v1-00000000000000000000000000000000"
}

// ---------------------------------------------------------------------------
// TerminalManager
// ---------------------------------------------------------------------------

// TerminalManager 管理当前已连接宿主的全部终端会话连接（单宿主连接模型，
// 蓝图 §13；Connect 顶替时 DetachAll）。
type TerminalManager struct {
	mu       sync.Mutex
	t        *Transport
	hostID   string
	emitter  EventEmitter
	cfg      TerminalConfig
	sessions map[contract.SessionID]*TerminalSession
}

// NewTerminalManager 构造管理器（cfg 零值回退默认）。
func NewTerminalManager(t *Transport, hostID string, emitter EventEmitter, cfg TerminalConfig) *TerminalManager {
	return &TerminalManager{
		t: t, hostID: hostID, emitter: emitter, cfg: cfg,
		sessions: make(map[contract.SessionID]*TerminalSession),
	}
}

// Attach 启动（或复用）一个会话的终端连接。幂等：已 attach 返回原会话。
func (m *TerminalManager) Attach(sessionID string) (*TerminalSession, error) {
	sid := contract.SessionID(sessionID)
	if sid == "" {
		return nil, errors.New("remoteclient: sessionID is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.sessions[sid]; ok {
		return existing, nil
	}
	s := newTerminalSession(m.t, m.hostID, sid, m.emitter, m.cfg)
	m.sessions[sid] = s
	return s, nil
}

// Get 返回已 attach 的会话（未 attach 返回错误）。
func (m *TerminalManager) Get(sessionID string) (*TerminalSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[contract.SessionID(sessionID)]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("remoteclient: session %q is not attached", sessionID)
}

// Detach 终止并移除一个会话连接。
func (m *TerminalManager) Detach(sessionID string) error {
	m.mu.Lock()
	s, ok := m.sessions[contract.SessionID(sessionID)]
	if ok {
		delete(m.sessions, contract.SessionID(sessionID))
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("remoteclient: session %q is not attached", sessionID)
	}
	return s.Detach()
}

// DetachAll 终止全部会话连接（断开宿主/顶替连接时）。
func (m *TerminalManager) DetachAll() {
	m.mu.Lock()
	sessions := make([]*TerminalSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = make(map[contract.SessionID]*TerminalSession)
	m.mu.Unlock()
	for _, s := range sessions {
		_ = s.Detach()
	}
}

// Count 返回当前 attach 的会话数。
func (m *TerminalManager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}
