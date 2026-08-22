// outbox.go 输入发件箱（蓝图 §6 流程 3、§8；语义对齐 contract-addendum-cg03.md
// §4/§5 与 mobile inputOutbox.ts 的状态机语义，Go 原生实现）：
//
// 每会话一条 FIFO outbox，single-flight（仅队首有 wire attempt）。每条 logical
// input 在 Accept 前取一次 canonical MessageID（`msg-v1-` + 32 hex，CSPRNG
// 128-bit）并绑定 immutable base64 payload；重试复用同一 MessageID、每次 wire
// attempt 追加全新 canonical RequestID（`req-v1-` + 32 hex）。ACK 结算要求
// MessageID 命中且 RequestID 属于该 entry 的 all-attempt 集合（CG-03 M3-003
// 精确关联）；错配 ACK 绝不结算。断线 Pause（暂停出队），重连 Resume（换新
// RequestID 续发，队首继续计数 attempt）；服务端 per-session input ledger 的
// 幂等语义保证重发窗口安全。
//
// 幂等 scope（CG-03 §3）：(SessionID lifetime, DeviceID)。session restart 不
// 重置 outbox——重启边界事件到来时 pending 保留，reattach 后 Resume 续发；
// 只有 Detach/会话移除才 Dispose 终结 scope。
//
// 关闭顺序（蓝图 §8）：outbox Pause/Dispose → WS close → REST idle。

package remoteclient

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// InputRejectReason 是 Accept 拒绝的稳定原因。
type InputRejectReason string

const (
	InputRejectedOutboxFull     InputRejectReason = "outbox.full"
	InputRejectedTooLarge       InputRejectReason = "input.too_large"
	InputRejectedSecureIDFailed InputRejectReason = "secure_id_unavailable"
	InputRejectedHalted         InputRejectReason = "outbox.halted"
)

// InputOutboxConfig 是发件箱限额（默认值对齐 mobile 端 CG-03 冻结参数：
// 32 entries / 256KiB / 每项 8 attempts / 30s 窗口 / issued 8192 / 单帧 ≤60KB）。
type InputOutboxConfig struct {
	MaxEntries    int
	MaxTotalBytes int64
	MaxAttempts   int
	AttemptWindow time.Duration
	MaxIssued     int
	FrameMaxBytes int
	// AttemptDelays 是相邻 wire attempt 的间隔（下标=已发 attempt 数-1；
	// 超出取最后一项）。
	AttemptDelays []time.Duration
	// Clock/Random 为可注入 seam（测试确定性）；缺省用系统时钟与 crypto/rand。
	Clock  func() time.Time
	Random func(n int) ([]byte, error)
}

// DefaultInputOutboxConfig 返回生产默认限额。
func DefaultInputOutboxConfig() InputOutboxConfig {
	return InputOutboxConfig{
		MaxEntries:    32,
		MaxTotalBytes: 256 * 1024,
		MaxAttempts:   8,
		AttemptWindow: 30 * time.Second,
		MaxIssued:     8192,
		FrameMaxBytes: 60_000,
		AttemptDelays: []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second},
		Clock:         time.Now,
		Random:        randomBytes,
	}
}

// randomBytes 是 CSPRNG 适配器：crypto/rand → func(n int) ([]byte, error)。
func randomBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// chargedBytes 计费：payload + MessageID + 8×RequestID canonical 预留（39B）。
const (
	canonicalMessageIDBytes = 39
	canonicalRequestIDBytes = 39
)

// inputEntryState 是单条 entry 的状态：pending（待确认/重试中）、settled（ACK
// 已结算）、halted（冻结：不再重试但保留迟到 ACK 资格）。
type inputEntryState string

const (
	entryPending inputEntryState = "pending"
	entrySettled inputEntryState = "settled"
	entryHalted  inputEntryState = "halted"
)

type inputEntry struct {
	id         contract.MessageID
	data       string // immutable base64 payload
	attemptIDs []contract.RequestID
	attempts   int
	firstAt    time.Time
	state      inputEntryState
	charged    int64
}

// InputSendFunc 是 wire 发送 seam：返回是否已发出（false = 连接不可写，
// attempt 仍计数，等 Pause/Resume 或定时重试）。
type InputSendFunc func(frame contract.InputFrame) bool

// InputOutbox 是每会话的 canonical 输入发件箱（纯状态机，send/clock/random
// 注入；由 TerminalSession 持有，连接替换不新建）。
type InputOutbox struct {
	cfg  InputOutboxConfig
	send InputSendFunc

	mu      sync.Mutex
	entries []*inputEntry // FIFO；settled 后移除
	charged int64
	issued  map[contract.MessageID]struct{}
	halted  bool
	paused  bool
	timer   *time.Timer
}

// NewInputOutbox 构造发件箱；send 不可为 nil。cfg 零值字段回退默认。
func NewInputOutbox(send InputSendFunc, cfg InputOutboxConfig) *InputOutbox {
	def := DefaultInputOutboxConfig()
	if cfg.MaxEntries == 0 {
		cfg.MaxEntries = def.MaxEntries
	}
	if cfg.MaxTotalBytes == 0 {
		cfg.MaxTotalBytes = def.MaxTotalBytes
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = def.MaxAttempts
	}
	if cfg.AttemptWindow == 0 {
		cfg.AttemptWindow = def.AttemptWindow
	}
	if cfg.MaxIssued == 0 {
		cfg.MaxIssued = def.MaxIssued
	}
	if cfg.FrameMaxBytes == 0 {
		cfg.FrameMaxBytes = def.FrameMaxBytes
	}
	if len(cfg.AttemptDelays) == 0 {
		cfg.AttemptDelays = def.AttemptDelays
	}
	if cfg.Clock == nil {
		cfg.Clock = def.Clock
	}
	if cfg.Random == nil {
		cfg.Random = def.Random
	}
	return &InputOutbox{cfg: cfg, send: send, issued: make(map[contract.MessageID]struct{})}
}

// canonicalMessageID 生成 canonical MessageID：`msg-v1-` + 32 lowercase hex。
func canonicalMessageID(random func(n int) ([]byte, error)) (contract.MessageID, error) {
	s, err := canonicalHexID("msg-v1-", random)
	return contract.MessageID(s), err
}

// canonicalRequestID 生成 canonical RequestID：`req-v1-` + 32 lowercase hex。
func canonicalRequestID(random func(n int) ([]byte, error)) (contract.RequestID, error) {
	s, err := canonicalHexID("req-v1-", random)
	return contract.RequestID(s), err
}

func canonicalHexID(prefix string, random func(n int) ([]byte, error)) (string, error) {
	buf, err := random(16)
	if err != nil || len(buf) != 16 {
		return "", fmt.Errorf("random source failed (%v)", err)
	}
	return prefix + hex.EncodeToString(buf), nil
}

// Accept 接受一条 logical input（base64 data，绑定后不可变）。上限/熵失败
// zero-wire（不入队、不发送）；成功后 kick single-flight。
func (o *InputOutbox) Accept(data string) (InputRejectReason, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.halted {
		return InputRejectedHalted, false
	}
	if len(data) > o.cfg.FrameMaxBytes {
		return InputRejectedTooLarge, false
	}
	if len(o.entries) >= o.cfg.MaxEntries {
		return InputRejectedOutboxFull, false
	}
	id, err := canonicalMessageID(o.cfg.Random)
	if err != nil || contract.IsCanonicalMessageID(id) != true {
		return InputRejectedSecureIDFailed, false
	}
	if _, dup := o.issued[id]; dup || len(o.issued) >= o.cfg.MaxIssued {
		return InputRejectedSecureIDFailed, false
	}
	charged := int64(len(data)) + canonicalMessageIDBytes + int64(o.cfg.MaxAttempts)*canonicalRequestIDBytes
	if o.charged+charged > o.cfg.MaxTotalBytes {
		return InputRejectedOutboxFull, false
	}
	o.issued[id] = struct{}{}
	o.charged += charged
	o.entries = append(o.entries, &inputEntry{id: id, data: data, state: entryPending})
	o.kickHeadLocked()
	return "", true
}

// OnAck 结算一条 ACK：要求 id 命中且 requestId 属于该 entry 的 all-attempt
// 集合（错配绝不结算）。halted entry 仍接受合法迟到 ACK（服务端确已 commit）。
func (o *InputOutbox) OnAck(id contract.MessageID, rid contract.RequestID) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, e := range o.entries {
		if e.state == entrySettled || e.id != id {
			continue
		}
		for _, a := range e.attemptIDs {
			if a == rid {
				e.state = entrySettled
				o.releaseEntryLocked(i)
				o.stopTimerLocked()
				o.kickHeadLocked()
				return true
			}
		}
		return false // id 命中但 requestId 不属于该 entry：错配，不结算
	}
	return false
}

// Pause 断线暂停：取消待执行重试 timer，不再出队（pending 保留，attempt
// 计数不重置）。
func (o *InputOutbox) Pause() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.paused = true
	o.stopTimerLocked()
}

// Resume 重连恢复：清除暂停标记并立即重试队首（计数 attempt、换新
// RequestID——服务端 per-session ledger 幂等兜底）。
func (o *InputOutbox) Resume() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.paused = false
	o.retryHeadLocked()
}

// FreezePending 冻结当前 pending（不再自动重发，但接受迟到 ACK；outbox 未
// 永久禁用、后续 Accept 的新 entry 照常出队——control.forbidden / 他人接管
// 等权威丢失场景；MA-1：冻结头不阻塞 FIFO）。
func (o *InputOutbox) FreezePending() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopTimerLocked()
	for _, e := range o.entries {
		if e.state == entryPending {
			e.state = entryHalted
		}
	}
}

// Halt 永久禁用（revoke/移除/终态）：清 timer、冻结全部 pending。
func (o *InputOutbox) Halt() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.halted = true
	o.stopTimerLocked()
	for _, e := range o.entries {
		if e.state == entryPending {
			e.state = entryHalted
		}
	}
}

// Dispose 销毁全部 entry 与 issued 集合（session remove/Detach：幂等 scope 终结）。
func (o *InputOutbox) Dispose() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopTimerLocked()
	o.entries = nil
	o.charged = 0
	o.issued = make(map[contract.MessageID]struct{})
	o.halted = false
	o.paused = false
}

// PendingCount 返回仍待结算（pending/halted 未销毁）的 entry 数。
func (o *InputOutbox) PendingCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, e := range o.entries {
		if e.state != entrySettled {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// 内部（mu 已持有）
// ---------------------------------------------------------------------------

// headLocked 返回最靠前的待发 entry（首个 pending；MA-1 对齐 mobile
// inputOutbox.ts 的 tryStartHead/retryHead——settled 已出队、halted 保留迟到
// ACK 资格但不参与出队，冻结头不阻塞 FIFO 后续重发）。
func (o *InputOutbox) headLocked() *inputEntry {
	for _, e := range o.entries {
		if e.state == entryPending {
			return e
		}
	}
	return nil
}

// kickHeadLocked 只启动「从未发过」的队首（single-flight 起点；headLocked
// 已保证返回 pending）。
func (o *InputOutbox) kickHeadLocked() {
	if o.halted || o.paused {
		return
	}
	head := o.headLocked()
	if head == nil || head.attempts > 0 {
		return
	}
	o.fireAttemptLocked(head)
}

// retryHeadLocked 为队首发下一轮 attempt（计数、受上限约束；halted 冻结头
// 不在候选内，其后 pending 照常重发）。
func (o *InputOutbox) retryHeadLocked() {
	if o.halted || o.paused {
		return
	}
	head := o.headLocked()
	if head == nil {
		return
	}
	if head.attempts >= o.cfg.MaxAttempts {
		return // 停止重试；entry 保留接受迟到 ACK
	}
	if !head.firstAt.IsZero() && o.cfg.Clock().Sub(head.firstAt) >= o.cfg.AttemptWindow {
		return
	}
	o.fireAttemptLocked(head)
}

// fireAttemptLocked 发一轮 wire attempt：新 canonical requestId 入 all-attempt
// 集合（不淘汰）、计数、发送、排下一轮 timer。
func (o *InputOutbox) fireAttemptLocked(e *inputEntry) {
	rid, err := canonicalRequestID(o.cfg.Random)
	if err != nil || contract.IsCanonicalRequestID(rid) != true {
		return // 熵失败：本轮不重试，entry 留 pending 接受迟到 ACK
	}
	e.attemptIDs = append(e.attemptIDs, rid)
	e.attempts++
	now := o.cfg.Clock()
	if e.firstAt.IsZero() {
		e.firstAt = now
	}
	o.send(contract.InputFrame{Type: contract.ClientFrameTypeInput, RequestID: rid, ID: e.id, Data: e.data})
	o.scheduleNextLocked(e, now)
}

// scheduleNextLocked 按间隔表排下次重试；Pause/Dispose/Halt 经 stopTimer 取消。
func (o *InputOutbox) scheduleNextLocked(e *inputEntry, now time.Time) {
	o.stopTimerLocked()
	if e.attempts >= o.cfg.MaxAttempts {
		return
	}
	if now.Sub(e.firstAt) >= o.cfg.AttemptWindow {
		return
	}
	idx := e.attempts - 1
	if idx >= len(o.cfg.AttemptDelays) {
		idx = len(o.cfg.AttemptDelays) - 1
	}
	delay := o.cfg.AttemptDelays[idx]
	o.timer = time.AfterFunc(delay, func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.timer != nil {
			o.timer = nil
		}
		o.retryHeadLocked()
	})
}

func (o *InputOutbox) stopTimerLocked() {
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
}

// releaseEntryLocked 移除已结算 entry 并释放计费。issued MessageID 不回收
// （防同页熵源复用旧 ID 被服务端当已提交 key re-ACK；容量由 MaxIssued 兜底）。
func (o *InputOutbox) releaseEntryLocked(i int) {
	e := o.entries[i]
	o.charged -= e.charged
	o.entries = append(o.entries[:i], o.entries[i+1:]...)
}
