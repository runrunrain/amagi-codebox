// Package remote: internal/remote/metrics.go — M0-06 timing instrumentation skeleton (Go side)
// ---------------------------------------------------------------------------
// 设计：fuxi/20260802-m0-06-timing-design/design.md §6（设计方案 A）。
//
// 服务端只记录 attach/resync duration，不记内容（技术方案 §11.2、F-03）。
// 两个固定 slot、O(1) 内存、generation 线性化、Timer exactly-once Observe。
// public API 只允许固定 enum/status + time.Time/time.Duration 内部计算；没有
// payload/ID/error/labels/logger/endpoint 入口，没有 float duration 输入。
//
// Production 接线裁定（§7.1）：M0 不接 legacy serveWebSocket；M2（attach，LastSeq==nil）
// / M3（resync，LastSeq!=nil）真实 v1 生命周期出现后再接线。本文件是 production-file
// public entry，由 Go unit/race test 消费，不是 TODO / 死分支。
package remote

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Public types（§6.1）—— 导出面唯一来源。
// ---------------------------------------------------------------------------

// TimingKind 是固定的两种服务端计时操作。
type TimingKind string

const (
	TimingAttach TimingKind = "attach"
	TimingResync TimingKind = "resync"
)

// TimingStatus 只有两种：未发生 / 已观测（服务端无 pending/invalid 概念）。
type TimingStatus string

const (
	TimingStatusNotOccurred TimingStatus = "not_occurred"
	TimingStatusObserved    TimingStatus = "observed"
)

// NowFunc 返回带 monotonic reading 的 time.Time；nil 回退 time.Now（零值安全）。
type NowFunc func() time.Time

// MeasurementSnapshot 是单个 kind 的快照。DurationMS 为指针：未发生=nil(JSON null)。
type MeasurementSnapshot struct {
	Status      TimingStatus `json:"status"`
	SampleCount uint64       `json:"sampleCount"`
	DurationMS  *float64     `json:"durationMs"`
}

// MetricsMeasurements 固定两个 key。
type MetricsMeasurements struct {
	Attach MeasurementSnapshot `json:"attach"`
	Resync MeasurementSnapshot `json:"resync"`
}

// MetricsSnapshot 与 TS 报告共享顶层三字段（schemaVersion/unit/measurements）；
// 不是 wire protocol，不跨网络传输（§4.2）。
type MetricsSnapshot struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Unit          string              `json:"unit"`
	Measurements  MetricsMeasurements `json:"measurements"`
}

// Metrics 是无导出字段的 collector。零值安全（未调用 NewMetrics 也可用）。
type Metrics struct {
	mu         sync.RWMutex
	now        NowFunc
	generation uint64
	agg        [numSlots]aggregate
}

// Timer 持有调用方的一次操作上下文；字段私有，只能由 Metrics.Start 创建。
type Timer struct {
	metrics    *Metrics
	slot       int
	generation uint64
	start      time.Time
	done       atomic.Bool
}

// NewMetrics 创建 collector；now==nil 时使用 time.Now。
func NewMetrics(now NowFunc) *Metrics {
	if now == nil {
		now = time.Now
	}
	return &Metrics{now: now}
}

// Start 创建一个 Timer；只接受两个 known kind，unknown 返回 (nil,false) 且不调用 clock。
// 先锁内捕获 generation，锁外读 clock，再锁内确认 generation 未变；
// zero time 或中途 Reset 都返回 false。禁止在持锁期间调用注入函数（§6.2.2）。
func (m *Metrics) Start(kind TimingKind) (*Timer, bool) {
	slot, ok := slotFor(kind)
	if !ok {
		return nil, false // unknown kind：不调用 clock
	}

	// 锁内捕获 generation。
	m.mu.RLock()
	gen := m.generation
	m.mu.RUnlock()

	// 锁外读 clock（禁止在持锁期间调用注入函数）。
	start := m.clock()

	// 锁内确认 generation 未变 + start 非 zero。
	m.mu.RLock()
	defer m.mu.RUnlock()

	if start.IsZero() {
		return nil, false
	}
	if m.generation != gen {
		return nil, false // 中途 Reset
	}

	return &Timer{
		metrics:    m,
		slot:       slot,
		generation: gen,
		start:      start,
	}, true
}

// Observe 通过 atomic.Bool CAS 获得一次性权利；恰一个并发调用可能成功（§6.2.4）。
// end zero / end.Before(start) / stale generation 均 false，collector 完全不变（§6.2.5、§6.3）。
// 成功时更新 count（饱和不回绕）与 latest（始终更新）。
func (t *Timer) Observe() bool {
	// exactly-once：CAS false→true。
	if !t.done.CompareAndSwap(false, true) {
		return false
	}

	// 锁外读 end clock。
	end := t.metrics.clock()

	t.metrics.mu.Lock()
	defer t.metrics.mu.Unlock()

	// stale timer：Start 与 Observe 之间发生了 Reset（§6.3）。
	if t.metrics.generation != t.generation {
		return false
	}
	// invalid end：zero 或早于 start（§6.2.5）。
	if end.IsZero() || end.Before(t.start) {
		return false
	}

	duration := end.Sub(t.start)

	a := &t.metrics.agg[t.slot]
	// count 饱和到 MaxUint64，不回绕；latest 始终更新（§6.4、G-10）。
	if a.sampleCount < math.MaxUint64 {
		a.sampleCount++
	}
	a.latestDuration = duration
	a.hasSample = true
	return true
}

// Snapshot 在读锁内 O(1) 复制值，返回后与 collector 不共享可变状态（§6.3）。
func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return MetricsSnapshot{
		SchemaVersion: 1,
		Unit:          "ms",
		Measurements: MetricsMeasurements{
			Attach: m.snapshotSlot(slotAttach),
			Resync: m.snapshotSlot(slotResync),
		},
	}
}

// Reset 在写锁内 generation++ 并清空两个 aggregate（§6.3）。
func (m *Metrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	for i := range m.agg {
		m.agg[i] = aggregate{}
	}
}

// ---------------------------------------------------------------------------
// Internal —— 非导出。
// ---------------------------------------------------------------------------

const (
	slotAttach = 0
	slotResync = 1
	numSlots   = 2
)

// aggregate 是单个 kind 的 O(1) 存储（§6.4）：count + latest + hasSample。
type aggregate struct {
	sampleCount    uint64
	latestDuration time.Duration
	hasSample      bool
}

// slotFor 把 TimingKind 映射到固定 slot；unknown 返回 (0,false)。
func slotFor(kind TimingKind) (int, bool) {
	switch kind {
	case TimingAttach:
		return slotAttach, true
	case TimingResync:
		return slotResync, true
	default:
		return 0, false
	}
}

// clock 读取时间；now==nil（零值 Metrics）回退 time.Now，保证零值安全。
func (m *Metrics) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// snapshotSlot 从 aggregate 构造单条快照（持读锁调用）。
func (m *Metrics) snapshotSlot(slot int) MeasurementSnapshot {
	a := m.agg[slot]
	s := MeasurementSnapshot{
		Status:      TimingStatusNotOccurred,
		SampleCount: a.sampleCount,
		DurationMS:  nil,
	}
	if a.hasSample {
		s.Status = TimingStatusObserved
		// time.Duration 是有界整数 int64 ns；float64(nanos)/float64(ms) 全域 finite（§6.2.6）。
		ms := float64(a.latestDuration) / float64(time.Millisecond)
		s.DurationMS = &ms
	}
	return s
}
