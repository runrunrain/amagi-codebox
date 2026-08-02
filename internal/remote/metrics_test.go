// internal/remote/metrics_test.go — M0-06 metrics skeleton Go 测试（G-01～G-12）
// ---------------------------------------------------------------------------
// 设计：fuxi/20260802-m0-06-timing-design/design.md §6 + §9.2。
// 只测 public API（除 G-10 饱和用 package-private 构造）；不复制实现算法。
// 非有限 Go case 明确标 not applicable by API type（§9.2 末注）。
package remote

import (
	"encoding/json"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock 按调用顺序返回预设 time.Time；nil 视为 zero time（IsZero()==true）。
type fakeClock struct {
	values []time.Time
	calls  int32
}

func (f *fakeClock) now() time.Time {
	atomic.AddInt32(&f.calls, 1)
	i := int(atomic.LoadInt32(&f.calls)) - 1
	if i < len(f.values) {
		return f.values[i]
	}
	// 越界返回最后一个非 nil 值；无值返回 zero（仅安全兜底，正常 case 不触达）。
	if len(f.values) > 0 {
		return f.values[len(f.values)-1]
	}
	return time.Time{}
}

// testTimeBase 是 deterministic-duration 测试基点：包初始化时由 time.Now() 取得一次，
// 携带 monotonic reading；后续 Add 保留 monotonic 分量（Go time 不变量：Add 调整 mono 读数）。
// 这使 ts() 返回的时间与 production time.Now() 一样可做单调 Sub/Before 比较——不依赖 time.Unix
// （后者不携带 monotonic reading，旧注释的“保证 wall+mono”不成立）。
var testTimeBase = time.Now()

// ts 返回 testTimeBase 加 offsetNs 纳秒偏移的测试时间；保留 monotonic reading，
// 供 deterministic-duration 用例做单调 end.Sub(start)/end.Before(start) 断言。
// offsetNs 是测试可控的相对差值，不改测试语义/阈值/public API。
func ts(offsetNs int64) time.Time {
	return testTimeBase.Add(time.Duration(offsetNs))
}

// msPtr 解引用 *float64，nil 返回 NaN 便于断言区分。
func msPtr(d *float64) float64 {
	if d == nil {
		return math.NaN()
	}
	return *d
}

// ---------------------------------------------------------------------------
// G-01：新 collector — attach/resync not_occurred，count0，duration null
// ---------------------------------------------------------------------------
func TestMetrics_G01_NewCollectorNotOccurred(t *testing.T) {
	m := NewMetrics(nil)
	s := m.Snapshot()
	if s.SchemaVersion != 1 || s.Unit != "ms" {
		t.Fatalf("envelope = %+v", s)
	}
	for name, ms := range map[string]MeasurementSnapshot{
		"attach": s.Measurements.Attach, "resync": s.Measurements.Resync,
	} {
		if ms.Status != TimingStatusNotOccurred {
			t.Errorf("%s status = %s, want not_occurred", name, ms.Status)
		}
		if ms.SampleCount != 0 {
			t.Errorf("%s count = %d, want 0", name, ms.SampleCount)
		}
		if ms.DurationMS != nil {
			t.Errorf("%s duration = %v, want nil", name, ms.DurationMS)
		}
	}
}

// ---------------------------------------------------------------------------
// G-02：attach/resync 正常、duration=0/小数 ms — observed，count 与 latest 精确
// ---------------------------------------------------------------------------
func TestMetrics_G02_ObserveHappyPath(t *testing.T) {
	t.Run("attach duration 0", func(t *testing.T) {
		clk := &fakeClock{values: []time.Time{ts(0), ts(0)}}
		m := NewMetrics(clk.now)
		timer, ok := m.Start(TimingAttach)
		if !ok {
			t.Fatal("Start attach failed")
		}
		if !timer.Observe() {
			t.Fatal("Observe failed")
		}
		s := m.Snapshot().Measurements.Attach
		if s.Status != TimingStatusObserved || s.SampleCount != 1 {
			t.Fatalf("snapshot = %+v", s)
		}
		if msPtr(s.DurationMS) != 0 {
			t.Errorf("durationMs = %v, want 0", *s.DurationMS)
		}
	})

	t.Run("resync fractional ms 12.5", func(t *testing.T) {
		// 12.5ms = 12_500_000 ns
		clk := &fakeClock{values: []time.Time{ts(0), ts(12_500_000)}}
		m := NewMetrics(clk.now)
		timer, ok := m.Start(TimingResync)
		if !ok {
			t.Fatal("Start resync failed")
		}
		if !timer.Observe() {
			t.Fatal("Observe failed")
		}
		s := m.Snapshot().Measurements.Resync
		if s.Status != TimingStatusObserved || s.SampleCount != 1 {
			t.Fatalf("snapshot = %+v", s)
		}
		if msPtr(s.DurationMS) != 12.5 {
			t.Errorf("durationMs = %v, want 12.5", *s.DurationMS)
		}
		// attach 仍未发生
		if m.Snapshot().Measurements.Attach.Status != TimingStatusNotOccurred {
			t.Error("attach should remain not_occurred")
		}
	})

	t.Run("latest is the last accepted sample", func(t *testing.T) {
		clk := &fakeClock{values: []time.Time{
			ts(0), ts(1_000_000), // 1ms
			ts(0), ts(5_000_000), // 5ms
			ts(0), ts(3_000_000), // 3ms (last)
		}}
		m := NewMetrics(clk.now)
		for i := 0; i < 3; i++ {
			timer, ok := m.Start(TimingAttach)
			if !ok {
				t.Fatalf("Start #%d failed", i)
			}
			if !timer.Observe() {
				t.Fatalf("Observe #%d failed", i)
			}
		}
		s := m.Snapshot().Measurements.Attach
		if s.SampleCount != 3 {
			t.Errorf("count = %d, want 3", s.SampleCount)
		}
		if msPtr(s.DurationMS) != 3 {
			t.Errorf("latest = %v, want 3", *s.DurationMS)
		}
	})
}

// ---------------------------------------------------------------------------
// G-03：unknown TimingKind — Start false、clock 不调用、snapshot 不变
// ---------------------------------------------------------------------------
func TestMetrics_G03_UnknownKind(t *testing.T) {
	clk := &fakeClock{values: []time.Time{ts(1), ts(2)}}
	m := NewMetrics(clk.now)
	before := m.Snapshot()
	timer, ok := m.Start("backfill") // unknown
	if ok || timer != nil {
		t.Fatal("unknown kind should return (nil,false)")
	}
	if atomic.LoadInt32(&clk.calls) != 0 {
		t.Errorf("clock called %d times, want 0 (unknown kind must not read clock)", clk.calls)
	}
	if got := m.Snapshot(); got != before {
		t.Errorf("snapshot changed after unknown Start")
	}
}

// ---------------------------------------------------------------------------
// G-04：zero start/end time — false、无样本
// ---------------------------------------------------------------------------
func TestMetrics_G04_ZeroTime(t *testing.T) {
	t.Run("zero start", func(t *testing.T) {
		clk := &fakeClock{values: []time.Time{{}, ts(5)}} // start zero
		m := NewMetrics(clk.now)
		timer, ok := m.Start(TimingAttach)
		if ok || timer != nil {
			t.Fatal("zero start should return (nil,false)")
		}
		if m.Snapshot().Measurements.Attach.SampleCount != 0 {
			t.Error("no sample should exist")
		}
	})
	t.Run("zero end at Observe", func(t *testing.T) {
		clk := &fakeClock{values: []time.Time{ts(0), {}}}
		m := NewMetrics(clk.now)
		timer, ok := m.Start(TimingAttach)
		if !ok {
			t.Fatal("Start failed")
		}
		if timer.Observe() {
			t.Error("zero end Observe should return false")
		}
		if m.Snapshot().Measurements.Attach.SampleCount != 0 {
			t.Error("no sample should exist")
		}
	})
}

// ---------------------------------------------------------------------------
// G-05：end before start — Observe false、无样本
// ---------------------------------------------------------------------------
func TestMetrics_G05_EndBeforeStart(t *testing.T) {
	clk := &fakeClock{values: []time.Time{ts(100), ts(50)}} // end < start
	m := NewMetrics(clk.now)
	timer, ok := m.Start(TimingAttach)
	if !ok {
		t.Fatal("Start failed")
	}
	if timer.Observe() {
		t.Error("Observe should return false when end before start")
	}
	if m.Snapshot().Measurements.Attach.SampleCount != 0 {
		t.Error("no sample should exist")
	}
	if m.Snapshot().Measurements.Attach.DurationMS != nil {
		t.Error("duration should be nil")
	}
}

// ---------------------------------------------------------------------------
// G-06：同 Timer 重复/并发 Observe — 恰一次 true，count 只增 1
// ---------------------------------------------------------------------------
func TestMetrics_G06_ObserveExactlyOnce(t *testing.T) {
	t.Run("sequential repeat", func(t *testing.T) {
		clk := &fakeClock{values: []time.Time{ts(0), ts(1_000_000), ts(2_000_000), ts(3_000_000)}}
		m := NewMetrics(clk.now)
		timer, ok := m.Start(TimingAttach)
		if !ok {
			t.Fatal("Start failed")
		}
		if !timer.Observe() {
			t.Error("first Observe should succeed")
		}
		if timer.Observe() {
			t.Error("second Observe should fail")
		}
		if timer.Observe() {
			t.Error("third Observe should fail")
		}
		if c := m.Snapshot().Measurements.Attach.SampleCount; c != 1 {
			t.Errorf("count = %d, want 1", c)
		}
	})

	t.Run("concurrent Observe exactly one succeeds", func(t *testing.T) {
		clk := &fakeClock{values: []time.Time{ts(0), ts(1_000_000)}}
		m := NewMetrics(clk.now)
		timer, ok := m.Start(TimingAttach)
		if !ok {
			t.Fatal("Start failed")
		}
		var wg sync.WaitGroup
		var successes int32
		const n = 50
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if timer.Observe() {
					atomic.AddInt32(&successes, 1)
				}
			}()
		}
		wg.Wait()
		if successes != 1 {
			t.Errorf("concurrent successes = %d, want 1", successes)
		}
		if c := m.Snapshot().Measurements.Attach.SampleCount; c != 1 {
			t.Errorf("count = %d, want 1", c)
		}
	})
}

// ---------------------------------------------------------------------------
// G-07：多 goroutine 多 Timer — race-free，count 精确；latest 属于接受样本集合
// ---------------------------------------------------------------------------
func TestMetrics_G07_ConcurrentTimersRaceFree(t *testing.T) {
	// 每个 goroutine 创建自己的 Timer 并 Observe；共享 collector。
	m := NewMetrics(nil) // 真实 time.Now
	const goroutines = 200
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			timer, ok := m.Start(TimingAttach)
			if !ok {
				return
			}
			// 极短 sleep 制造真实并发与单调推进。
			time.Sleep(time.Microsecond)
			timer.Observe()
		}()
		// 同时混入 resync
		wg.Add(1)
		go func() {
			defer wg.Done()
			timer, ok := m.Start(TimingResync)
			if !ok {
				return
			}
			timer.Observe()
		}()
	}
	wg.Wait()
	s := m.Snapshot()
	if s.Measurements.Attach.SampleCount != uint64(goroutines) {
		t.Errorf("attach count = %d, want %d", s.Measurements.Attach.SampleCount, goroutines)
	}
	if s.Measurements.Resync.SampleCount != uint64(goroutines) {
		t.Errorf("resync count = %d, want %d", s.Measurements.Resync.SampleCount, goroutines)
	}
	// latest duration 必须 finite 且 >=0（真实耗时）
	for name, ms := range map[string]MeasurementSnapshot{
		"attach": s.Measurements.Attach, "resync": s.Measurements.Resync,
	} {
		if ms.DurationMS == nil {
			t.Errorf("%s latest nil", name)
			continue
		}
		v := *ms.DurationMS
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("%s latest = %v, want finite", name, v)
		}
		if v < 0 {
			t.Errorf("%s latest = %v, want >=0", name, v)
		}
	}
}

// ---------------------------------------------------------------------------
// G-08：Reset 清空 — 两类归零；snapshot 副本不变
// ---------------------------------------------------------------------------
func TestMetrics_G08_Reset(t *testing.T) {
	clk := &fakeClock{values: []time.Time{ts(0), ts(1_000_000), ts(0), ts(2_000_000)}}
	m := NewMetrics(clk.now)
	for _, kind := range []TimingKind{TimingAttach, TimingResync} {
		timer, ok := m.Start(kind)
		if !ok {
			t.Fatalf("Start %s failed", kind)
		}
		if !timer.Observe() {
			t.Fatalf("Observe %s failed", kind)
		}
	}
	before := m.Snapshot()
	if before.Measurements.Attach.SampleCount != 1 || before.Measurements.Resync.SampleCount != 1 {
		t.Fatalf("pre-reset counts wrong: %+v", before)
	}
	m.Reset()
	after := m.Snapshot()
	if after.Measurements.Attach.SampleCount != 0 || after.Measurements.Resync.SampleCount != 0 {
		t.Errorf("post-reset counts wrong: %+v", after)
	}
	if after.Measurements.Attach.Status != TimingStatusNotOccurred || after.Measurements.Resync.DurationMS != nil {
		t.Errorf("post-reset not clean: %+v", after)
	}
	// before 副本不变（值类型快照，与 collector 解耦）
	if before.Measurements.Attach.SampleCount != 1 {
		t.Error("before snapshot mutated by reset")
	}
}

// ---------------------------------------------------------------------------
// G-09：Reset 前 Timer 在 Reset 后 Observe — false，不污染新 generation
// ---------------------------------------------------------------------------
func TestMetrics_G09_StaleTimerAfterReset(t *testing.T) {
	clk := &fakeClock{values: []time.Time{ts(0), ts(1_000_000)}}
	m := NewMetrics(clk.now)
	timer, ok := m.Start(TimingAttach)
	if !ok {
		t.Fatal("Start failed")
	}
	// 先 Reset（generation 变化），再 Observe 旧 timer
	m.Reset()
	if timer.Observe() {
		t.Error("stale Observe should return false")
	}
	// 新 generation 干净
	if c := m.Snapshot().Measurements.Attach.SampleCount; c != 0 {
		t.Errorf("stale sample polluted new generation: count=%d", c)
	}
	// 新 Timer 可正常 Observe（generation 已推进，clock 越界兜底返回最后值）
	timer2, ok := m.Start(TimingAttach)
	if !ok {
		t.Fatal("second Start failed")
	}
	if !timer2.Observe() {
		t.Error("second Observe should succeed")
	}
	if c := m.Snapshot().Measurements.Attach.SampleCount; c != 1 {
		t.Errorf("new generation count=%d, want 1", c)
	}
}

// ---------------------------------------------------------------------------
// G-10：count 饱和边界 — 不回绕；latest 仍更新（package-private 构造）
// ---------------------------------------------------------------------------
func TestMetrics_G10_CountSaturation(t *testing.T) {
	clk := &fakeClock{values: []time.Time{ts(0), ts(7_000_000)}} // 7ms
	m := NewMetrics(clk.now)
	// 用 package-private 直接把 attach slot 设到 MaxUint64
	m.agg[slotAttach] = aggregate{sampleCount: math.MaxUint64, latestDuration: time.Millisecond, hasSample: true}
	timer, ok := m.Start(TimingAttach)
	if !ok {
		t.Fatal("Start failed")
	}
	if !timer.Observe() {
		t.Fatal("Observe should succeed at saturation")
	}
	s := m.Snapshot().Measurements.Attach
	// 不回绕：仍是 MaxUint64
	if s.SampleCount != math.MaxUint64 {
		t.Errorf("count = %d, want MaxUint64 (no wraparound)", s.SampleCount)
	}
	// latest 仍更新为 7ms
	if s.DurationMS == nil || *s.DurationMS != 7 {
		t.Errorf("latest = %v, want 7 (latest still updates at saturation)", s.DurationMS)
	}
}

// ---------------------------------------------------------------------------
// G-11：privacy/schema — fake time.Location 名含 secret sentinel，JSON 仍只有 duration
// ---------------------------------------------------------------------------
func TestMetrics_G11_PrivacySchema(t *testing.T) {
	// 构造一个 Location 名含敏感 sentinel；Observe 用它，snapshot JSON 不应泄露。
	secretLoc := time.FixedZone("SECRET_SENTINEL_token_credential_url", 0)
	start := time.Unix(1_700_000_000, 0).In(secretLoc)
	end := time.Unix(1_700_000_000, 5_000_000).In(secretLoc) // 5ms later
	clk := &fakeClock{values: []time.Time{start, end}}
	m := NewMetrics(clk.now)
	timer, ok := m.Start(TimingAttach)
	if !ok {
		t.Fatal("Start failed")
	}
	if !timer.Observe() {
		t.Fatal("Observe failed")
	}
	s := m.Snapshot()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	str := string(raw)
	for _, forbidden := range []string{"SECRET", "SENTINEL", "token", "credential", "url", "sessionId", "error", "payload"} {
		if strings.Contains(strings.ToLower(str), strings.ToLower(forbidden)) {
			t.Errorf("JSON contains forbidden token %q: %s", forbidden, str)
		}
	}
	// 精确 key 检查
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	topKeys := sortedKeys(decoded)
	if len(topKeys) != 3 || topKeys[0] != "measurements" || topKeys[1] != "schemaVersion" || topKeys[2] != "unit" {
		t.Errorf("top keys = %v", topKeys)
	}
	msr := decoded["measurements"].(map[string]any)
	if len(msr) != 2 {
		t.Errorf("measurement keys = %v", len(msr))
	}
	attach := msr["attach"].(map[string]any)
	attachKeys := sortedKeys(attach)
	wantAttach := []string{"durationMs", "sampleCount", "status"}
	if len(attachKeys) != 3 || attachKeys[0] != wantAttach[0] || attachKeys[1] != wantAttach[1] || attachKeys[2] != wantAttach[2] {
		t.Errorf("attach keys = %v, want %v", attachKeys, wantAttach)
	}
}

// ---------------------------------------------------------------------------
// G-12：test report — json.Marshal 后 t.Logf 固定前缀；日志含两 fixed keys
// ---------------------------------------------------------------------------
func TestMetrics_G12_TestReport(t *testing.T) {
	clk := &fakeClock{values: []time.Time{ts(0), ts(3_500_000), ts(0), ts(8_000_000)}}
	m := NewMetrics(clk.now)
	for _, kind := range []TimingKind{TimingAttach, TimingResync} {
		timer, ok := m.Start(kind)
		if !ok {
			t.Fatalf("Start %s failed", kind)
		}
		if !timer.Observe() {
			t.Fatalf("Observe %s failed", kind)
		}
	}
	raw, err := json.Marshal(m.Snapshot())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	// 固定前缀报告行（§9.2 G-12）：go test -v 日志含两 fixed keys。
	t.Logf("TIMING_REPORT_V1 %s", raw)
	str := string(raw)
	if !strings.Contains(str, `"attach"`) || !strings.Contains(str, `"resync"`) {
		t.Errorf("report missing fixed keys: %s", str)
	}
	if !strings.Contains(str, `"schemaVersion":1`) || !strings.Contains(str, `"unit":"ms"`) {
		t.Errorf("report missing envelope: %s", str)
	}
}

// ---------------------------------------------------------------------------
// 补充：NewMetrics(nil) 与零值 Metrics 都可工作；不增加 export surface
// ---------------------------------------------------------------------------
func TestMetrics_NilAndZeroValueSafe(t *testing.T) {
	// NewMetrics(nil) => time.Now
	m1 := NewMetrics(nil)
	if _, ok := m1.Start(TimingAttach); !ok {
		t.Error("NewMetrics(nil) Start failed")
	}
	// 零值 Metrics（未调用 NewMetrics）
	var m2 Metrics
	s := m2.Snapshot()
	if s.Measurements.Attach.Status != TimingStatusNotOccurred {
		t.Error("zero-value Metrics not clean")
	}
	m2.Reset() // 不 panic
}

// ---------------------------------------------------------------------------
// 补充：Start 与 Observe 之间 Reset 线性化（Observe 拿锁先则样本可被随后 Reset 清）
// ---------------------------------------------------------------------------
func TestMetrics_LinearizationObserveThenReset(t *testing.T) {
	clk := &fakeClock{values: []time.Time{ts(0), ts(1_000_000)}}
	m := NewMetrics(clk.now)
	timer, ok := m.Start(TimingAttach)
	if !ok {
		t.Fatal("Start failed")
	}
	if !timer.Observe() {
		t.Fatal("Observe failed")
	}
	// Observe 先拿锁写入；随后 Reset 清空新 generation
	m.Reset()
	if m.Snapshot().Measurements.Attach.SampleCount != 0 {
		t.Error("sample not cleared by reset")
	}
}

// sortedKeys 返回 map key 的排序切片（测试 helper）。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
