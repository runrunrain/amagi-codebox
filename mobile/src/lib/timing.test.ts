/**
 * mobile/src/lib/timing.test.ts — M0-06 timing skeleton TS 测试（T-01～T-14）
 * ---------------------------------------------------------------------------
 * 设计：fuxi/20260802-m0-06-timing-design/design.md §5 + §9.1。
 * 只测 public API；不复制实现算法（防镜像假绿）。每条 case 映射真实失败。
 * 不 mock server、不用假性能结果、不绕状态机。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  TIMING_BUDGET_MS,
  TIMING_MARKS,
  createTimingRecorder,
  type MonotonicClock,
  type TimingMark,
  type TimingMeasurement,
  type TimingReportV1,
  type TimingTransition,
} from './timing'

// not_occurred 模板（§5.7）：duration null、budgetStatus not_evaluated。
function notOccurred(budgetMs: 3000 | 5000): TimingMeasurement {
  return {
    status: 'not_occurred',
    durationMs: null,
    budgetMs,
    budgetStatus: 'not_evaluated',
    invalidReason: null,
  }
}

/** 顺序时钟：按调用顺序返回预设值（越界回退 0，仅用于安全兜底，不应触达）。 */
function seqClock(...values: number[]): { clock: MonotonicClock; calls: number } {
  let i = 0
  let calls = 0
  return {
    clock: () => {
      calls++
      const v = values[i++]
      return v === undefined ? 0 : v
    },
    get calls() {
      return calls
    },
  }
}

/** 计数时钟：每次返回递增值，用于验证 clock 调用次数。 */
function countingClock(start = 100): { clock: MonotonicClock; calls: number } {
  let calls = 0
  let v = start
  return {
    clock: () => {
      calls++
      return v++
    },
    get calls() {
      return calls
    },
  }
}

afterEach(() => {
  vi.restoreAllMocks()
})

// ---------------------------------------------------------------------------
// T-01：无 mark snapshot — 两项 not_occurred、duration null、预算精确 3000/5000
// ---------------------------------------------------------------------------
describe('T-01 fresh recorder snapshot', () => {
  it('both lanes not_occurred with exact budgets', () => {
    const r = createTimingRecorder()
    const snap = r.snapshot()
    expect(snap.schemaVersion).toBe(1)
    expect(snap.unit).toBe('ms')
    expect(snap.measurements.T0_T1).toEqual(notOccurred(3000))
    expect(snap.measurements.R0_R1).toEqual(notOccurred(5000))
    expect(TIMING_BUDGET_MS.T0_T1).toBe(3000)
    expect(TIMING_BUDGET_MS.R0_R1).toBe(5000)
  })
})

// ---------------------------------------------------------------------------
// T-02：正常 mark 序列；duration=0 合法；预算边界恰等于=within；超预算=over
// ---------------------------------------------------------------------------
describe('T-02 happy path + budget boundary', () => {
  it('observes both lanes with real durations', () => {
    const { clock } = seqClock(1000, 1250, 2000, 2300)
    const r = createTimingRecorder(clock)
    expect(r.mark('T0')).toEqual({ accepted: true })
    expect(r.mark('T1')).toEqual({ accepted: true })
    expect(r.mark('R0')).toEqual({ accepted: true })
    expect(r.mark('R1')).toEqual({ accepted: true })
    const snap = r.snapshot()
    expect(snap.measurements.T0_T1).toEqual({
      status: 'observed', durationMs: 250, budgetMs: 3000, budgetStatus: 'within_budget', invalidReason: null,
    })
    expect(snap.measurements.R0_R1).toEqual({
      status: 'observed', durationMs: 300, budgetMs: 5000, budgetStatus: 'within_budget', invalidReason: null,
    })
  })

  it('duration=0 is legal (start==end)', () => {
    const { clock } = seqClock(500, 500)
    const r = createTimingRecorder(clock)
    r.mark('T0'); r.mark('T1')
    const m = r.snapshot().measurements.T0_T1
    expect(m.status).toBe('observed')
    expect(m.durationMs).toBe(0)
    expect(m.budgetStatus).toBe('within_budget')
  })

  it('budget boundary: exactly equal => within; just over => over', () => {
    // T0_T1 budget 3000
    const at = seqClock(0, 3000) // 3000 == budget
    const rAt = createTimingRecorder(at.clock)
    rAt.mark('T0'); rAt.mark('T1')
    expect(rAt.snapshot().measurements.T0_T1.budgetStatus).toBe('within_budget')

    const over = seqClock(0, 3001) // 3001 > budget
    const rOver = createTimingRecorder(over.clock)
    rOver.mark('T0'); rOver.mark('T1')
    const m = rOver.snapshot().measurements.T0_T1
    expect(m.budgetStatus).toBe('over_budget')
    expect(m.durationMs).toBe(3001)

    // R0_R1 budget 5000
    const atR = seqClock(0, 5000)
    const rAtR = createTimingRecorder(atR.clock)
    rAtR.mark('R0'); rAtR.mark('R1')
    expect(rAtR.snapshot().measurements.R0_R1.budgetStatus).toBe('within_budget')
  })
})

// ---------------------------------------------------------------------------
// T-03：只有 T0/R0 — pending、duration null、not_evaluated
// ---------------------------------------------------------------------------
describe('T-03 started-only pending', () => {
  it('T0 alone => pending', () => {
    const { clock } = seqClock(42)
    const r = createTimingRecorder(clock)
    expect(r.mark('T0')).toEqual({ accepted: true })
    const m = r.snapshot().measurements.T0_T1
    expect(m.status).toBe('pending')
    expect(m.durationMs).toBeNull()
    expect(m.budgetStatus).toBe('not_evaluated')
  })
  it('R0 alone => pending; T lane untouched', () => {
    const { clock } = seqClock(7)
    const r = createTimingRecorder(clock)
    r.mark('R0')
    expect(r.snapshot().measurements.R0_R1.status).toBe('pending')
    expect(r.snapshot().measurements.T0_T1).toEqual(notOccurred(3000))
  })
})

// ---------------------------------------------------------------------------
// T-04：T1/R1 缺前置 — rejected missing_predecessor；lane invalid；不读 clock
// ---------------------------------------------------------------------------
describe('T-04 missing predecessor', () => {
  it('T1 without T0 => missing_predecessor, lane invalid, clock not called', () => {
    const spy = countingClock()
    const r = createTimingRecorder(spy.clock)
    const t = r.mark('T1')
    expect(t).toEqual({ accepted: false, reason: 'missing_predecessor' })
    expect(spy.calls).toBe(0) // 不读 clock
    const m = r.snapshot().measurements.T0_T1
    expect(m.status).toBe('invalid')
    expect(m.invalidReason).toBe('missing_predecessor')
    expect(m.durationMs).toBeNull()
  })
  it('R1 without R0 => missing_predecessor', () => {
    const r = createTimingRecorder(countingClock().clock)
    expect(r.mark('R1')).toEqual({ accepted: false, reason: 'missing_predecessor' })
    expect(r.snapshot().measurements.R0_R1.status).toBe('invalid')
  })
})

// ---------------------------------------------------------------------------
// T-05：duplicate start — 旧 start 被清除；lane invalid；不能随后补 end 产出值
// ---------------------------------------------------------------------------
describe('T-05 duplicate start clears old start', () => {
  it('double T0 => duplicate_mark; subsequent T1 cannot produce value', () => {
    const { clock } = seqClock(100, 200, 300)
    const r = createTimingRecorder(clock)
    r.mark('T0') // started @100
    const dup = r.mark('T0') // duplicate start on started lane
    expect(dup).toEqual({ accepted: false, reason: 'duplicate_mark' })
    // 尝试补 end —— lane 已 faulted，应 lane_invalid，无 duration
    const after = r.mark('T1')
    expect(after).toEqual({ accepted: false, reason: 'lane_invalid' })
    const m = r.snapshot().measurements.T0_T1
    expect(m.status).toBe('invalid')
    expect(m.invalidReason).toBe('duplicate_mark') // 首个原因保留
    expect(m.durationMs).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// T-06：complete 后重复 start/end — 旧 duration 被清除；lane invalid
// ---------------------------------------------------------------------------
describe('T-06 duplicate after complete clears old duration', () => {
  it('duplicate start after complete => invalid, old duration gone', () => {
    const { clock } = seqClock(0, 100, 200)
    const r = createTimingRecorder(clock)
    r.mark('T0'); r.mark('T1') // complete, duration 100
    expect(r.snapshot().measurements.T0_T1.status).toBe('observed')
    const dup = r.mark('T0') // complete + start => duplicate
    expect(dup).toEqual({ accepted: false, reason: 'duplicate_mark' })
    const m = r.snapshot().measurements.T0_T1
    expect(m.status).toBe('invalid')
    expect(m.durationMs).toBeNull()
  })
  it('duplicate end after complete => invalid', () => {
    const { clock } = seqClock(0, 100, 200)
    const r = createTimingRecorder(clock)
    r.mark('T0'); r.mark('T1') // complete
    const dup = r.mark('T1') // complete + end => duplicate
    expect(dup).toEqual({ accepted: false, reason: 'duplicate_mark' })
    expect(r.snapshot().measurements.T0_T1.status).toBe('invalid')
    expect(r.snapshot().measurements.T0_T1.durationMs).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// T-07：clock 返回 NaN/±Inf/wrong type/throw — 不 throw；clock_invalid；不保留数值/error
// ---------------------------------------------------------------------------
describe('T-07 clock anomalies => clock_invalid', () => {
  const badClocks: Array<{ name: string; clock: MonotonicClock; stage: 'start' | 'end' }> = [
    { name: 'NaN at start', clock: () => NaN, stage: 'start' },
    { name: '+Inf at start', clock: () => Infinity, stage: 'start' },
    { name: '-Inf at start', clock: () => -Infinity, stage: 'start' },
    { name: 'string at start', clock: (() => 'oops') as unknown as MonotonicClock, stage: 'start' },
    { name: 'throw at start', clock: () => { throw new Error('SECRET_CLOCK_BLOWUP') }, stage: 'start' },
    { name: 'NaN at end', clock: seqClock(10, NaN).clock, stage: 'end' },
    { name: 'Inf at end', clock: seqClock(10, Infinity).clock, stage: 'end' },
    { name: 'throw at end', clock: (() => { let i = 0; return () => { if (i++ === 0) return 10; throw new Error('END_BLOWUP') } })(), stage: 'end' },
  ]

  for (const { name, clock, stage } of badClocks) {
    it(name, () => {
      const r = createTimingRecorder(clock)
      let result!: TimingTransition
      if (stage === 'start') {
        // 恰调用一次：不 throw 且返回 clock_invalid
        expect(() => { result = r.mark('T0') }).not.toThrow()
      } else {
        r.mark('T0') // start ok
        expect(() => { result = r.mark('T1') }).not.toThrow()
      }
      expect(result).toEqual({ accepted: false, reason: 'clock_invalid' })
      const m = r.snapshot().measurements.T0_T1
      expect(m.status).toBe('invalid')
      expect(m.invalidReason).toBe('clock_invalid')
      expect(m.durationMs).toBeNull()
      // error text 不泄露
      const json = JSON.stringify(r.snapshot())
      expect(json).not.toContain('SECRET_CLOCK_BLOWUP')
      expect(json).not.toContain('END_BLOWUP')
    })
  }
})

// ---------------------------------------------------------------------------
// T-08：两个有限值相减溢出为 Inf — clock_invalid
// ---------------------------------------------------------------------------
describe('T-08 finite endpoints overflow to Inf', () => {
  it('MAX - (-MAX) = Inf => clock_invalid', () => {
    const { clock } = seqClock(-Number.MAX_VALUE, Number.MAX_VALUE)
    const r = createTimingRecorder(clock)
    expect(r.mark('T0')).toEqual({ accepted: true }) // start finite
    const end = r.mark('T1')
    expect(end).toEqual({ accepted: false, reason: 'clock_invalid' })
    expect(r.snapshot().measurements.T0_T1.status).toBe('invalid')
    expect(r.snapshot().measurements.T0_T1.invalidReason).toBe('clock_invalid')
  })
})

// ---------------------------------------------------------------------------
// T-09：end < start — clock_regressed；duration null
// ---------------------------------------------------------------------------
describe('T-09 regressed clock', () => {
  it('end < start => clock_regressed', () => {
    const { clock } = seqClock(100, 50)
    const r = createTimingRecorder(clock)
    r.mark('T0')
    const t = r.mark('T1')
    expect(t).toEqual({ accepted: false, reason: 'clock_regressed' })
    const m = r.snapshot().measurements.T0_T1
    expect(m.status).toBe('invalid')
    expect(m.invalidReason).toBe('clock_regressed')
    expect(m.durationMs).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// T-10：unknown runtime mark — unknown_mark；两 lane 不变；clock 未调用
// ---------------------------------------------------------------------------
describe('T-10 unknown runtime mark', () => {
  it('rejects unknown mark without touching clock or lanes', () => {
    const spy = countingClock()
    const r = createTimingRecorder(spy.clock)
    const before = r.snapshot()
    const t = r.mark('ZZ' as unknown as TimingMark)
    expect(t).toEqual({ accepted: false, reason: 'unknown_mark' })
    expect(spy.calls).toBe(0) // clock 未调用
    // 两 lane byte-for-byte 不变
    expect(r.snapshot()).toEqual(before)
    expect(r.snapshot().measurements.T0_T1).toEqual(notOccurred(3000))
    expect(r.snapshot().measurements.R0_R1).toEqual(notOccurred(5000))
  })
  it('unknown mark does not pollute a partially-started lane', () => {
    const { clock } = seqClock(10, 20, 30)
    const r = createTimingRecorder(clock)
    r.mark('T0') // started
    r.mark('BOGUS' as unknown as TimingMark) // unknown
    r.mark('T1') // should still complete T lane
    expect(r.snapshot().measurements.T0_T1.status).toBe('observed')
  })
})

// ---------------------------------------------------------------------------
// T-11：一条 lane faulted，另一条正常 — 正交；正常 lane 仍可 observed
// ---------------------------------------------------------------------------
describe('T-11 lane orthogonality', () => {
  it('T faulted does not block R lane', () => {
    const { clock } = seqClock(1, 2, 3)
    const r = createTimingRecorder(clock)
    r.mark('T1') // T faulted (missing_predecessor)
    // T 进一步 mark => lane_invalid（保留首因）
    expect(r.mark('T0')).toEqual({ accepted: false, reason: 'lane_invalid' })
    // R lane 仍正常
    r.mark('R0'); r.mark('R1')
    const snap = r.snapshot()
    expect(snap.measurements.T0_T1.status).toBe('invalid')
    expect(snap.measurements.T0_T1.invalidReason).toBe('missing_predecessor')
    expect(snap.measurements.R0_R1.status).toBe('observed')
  })
})

// ---------------------------------------------------------------------------
// T-12：reset — 两 lane 恢复 not_occurred；旧 report 快照不被后续改变
// ---------------------------------------------------------------------------
describe('T-12 reset', () => {
  it('clears complete/started/invalid lanes; old snapshot immutable', () => {
    const { clock } = seqClock(0, 100, 200)
    const r = createTimingRecorder(clock)
    r.mark('T0'); r.mark('T1') // T complete
    r.mark('R0') // R started
    const oldSnap = r.snapshot()
    expect(oldSnap.measurements.T0_T1.status).toBe('observed')

    r.reset()
    const fresh = r.snapshot()
    expect(fresh.measurements.T0_T1).toEqual(notOccurred(3000))
    expect(fresh.measurements.R0_R1).toEqual(notOccurred(5000))

    // 旧快照不被后续 reset/mark 改变（深冻结独立对象）
    expect(oldSnap.measurements.T0_T1.status).toBe('observed')
    expect(oldSnap.measurements.R0_R1.status).toBe('pending')

    // reset 后可重新测量
    r.mark('T0'); r.mark('T1')
    expect(r.snapshot().measurements.T0_T1.status).toBe('observed')
  })
})

// ---------------------------------------------------------------------------
// T-13：reporter — mark/snapshot/reset 0 console；report(spy) 恰一次；console 恰一次
// ---------------------------------------------------------------------------
describe('T-13 reporter semantics', () => {
  it('mark/snapshot/reset produce no console output', () => {
    const info = vi.spyOn(console, 'info').mockImplementation(() => {})
    const { clock } = seqClock(0, 10)
    const r = createTimingRecorder(clock)
    r.mark('T0'); r.snapshot(); r.mark('T1'); r.snapshot(); r.reset(); r.snapshot()
    expect(info).not.toHaveBeenCalled()
  })

  it('report(spy) calls reporter exactly once with the snapshot', () => {
    const { clock } = seqClock(0, 10)
    const r = createTimingRecorder(clock)
    r.mark('T0'); r.mark('T1')
    const spy = vi.fn()
    const returned = r.report(spy)
    expect(spy).toHaveBeenCalledTimes(1)
    const arg = spy.mock.calls[0][0] as TimingReportV1
    expect(arg).toEqual(returned)
    expect(arg.measurements.T0_T1.status).toBe('observed')
  })

  it('reporter throwing propagates but recorder unchanged', () => {
    const { clock } = seqClock(0, 10)
    const r = createTimingRecorder(clock)
    r.mark('T0'); r.mark('T1')
    const boom = (): void => { throw new Error('REPORTER_BLOWUP') }
    expect(() => r.report(boom)).toThrow('REPORTER_BLOWUP')
    // recorder 不变：snapshot 仍是 observed
    expect(r.snapshot().measurements.T0_T1.status).toBe('observed')
  })

  it('reportToConsole calls console.info once with fixed prefix', () => {
    const info = vi.spyOn(console, 'info').mockImplementation(() => {})
    const { clock } = seqClock(0, 10)
    const r = createTimingRecorder(clock)
    r.mark('T0'); r.mark('T1')
    const returned = r.reportToConsole()
    expect(info).toHaveBeenCalledTimes(1)
    expect(info.mock.calls[0][0]).toBe('TIMING_REPORT_V1')
    expect(info.mock.calls[0][1]).toEqual(returned)
  })
})

// ---------------------------------------------------------------------------
// T-14：privacy/schema — deep key allowlist 精确；无 extension；JSON 不含 clock 闭包 secret
// ---------------------------------------------------------------------------
describe('T-14 privacy & schema', () => {
  it('snapshot deep-frozen at every level', () => {
    const r = createTimingRecorder()
    const snap = r.snapshot()
    expect(Object.isFrozen(snap)).toBe(true)
    expect(Object.isFrozen(snap.measurements)).toBe(true)
    expect(Object.isFrozen(snap.measurements.T0_T1)).toBe(true)
    expect(Object.isFrozen(snap.measurements.R0_R1)).toBe(true)
    // strict-mode mutation throws
    expect(() => { (snap as any).schemaVersion = 2 }).toThrow(TypeError)
    expect(() => { (snap.measurements.T0_T1 as any).durationMs = 5 }).toThrow(TypeError)
  })

  it('exact key allowlist — no extension fields', () => {
    const r = createTimingRecorder()
    const snap = r.snapshot()
    expect(Object.keys(snap).sort()).toEqual(['measurements', 'schemaVersion', 'unit'])
    expect(Object.keys(snap.measurements).sort()).toEqual(['R0_R1', 'T0_T1'])
    const allowedMeasurementKeys = ['budgetMs', 'budgetStatus', 'durationMs', 'invalidReason', 'status']
    expect(Object.keys(snap.measurements.T0_T1).sort()).toEqual(allowedMeasurementKeys)
    expect(Object.keys(snap.measurements.R0_R1).sort()).toEqual(allowedMeasurementKeys)
  })

  it('observed measurement still has exact key set', () => {
    const { clock } = seqClock(0, 10)
    const r = createTimingRecorder(clock)
    r.mark('T0'); r.mark('T1')
    const m = r.snapshot().measurements.T0_T1
    expect(Object.keys(m).sort()).toEqual(['budgetMs', 'budgetStatus', 'durationMs', 'invalidReason', 'status'])
  })

  it('JSON of report contains no secret sentinel from injected clock closure', () => {
    // clock 闭包持有 secret 字符串；snapshot 只输出 duration 差值，不得泄露闭包内容。
    const SECRET_SENTINEL = 'LEAK_CANARY_a1b2c3d4_token_url_credential'
    const holder = { secret: SECRET_SENTINEL }
    const clock: MonotonicClock = () => {
      void holder.secret // 闭包引用 secret
      return 10
    }
    const r = createTimingRecorder(clock)
    r.mark('T0'); r.mark('R0')
    r.mark('T1'); r.mark('R1')
    const json = JSON.stringify(r.snapshot())
    expect(json).not.toContain(SECRET_SENTINEL)
    expect(json).not.toContain('token')
    expect(json).not.toContain('credential')
    expect(json).not.toContain('url')
  })

  it('TIMING_MARKS is exactly the four fixed marks', () => {
    expect([...TIMING_MARKS]).toEqual(['T0', 'T1', 'R0', 'R1'])
  })

  it('TIMING_BUDGET_MS is frozen', () => {
    expect(Object.isFrozen(TIMING_BUDGET_MS)).toBe(true)
    expect(() => { (TIMING_BUDGET_MS as any).T0_T1 = 1 }).toThrow(TypeError)
  })
})

// ---------------------------------------------------------------------------
// 补充：default clock 仅 performance.now（无 Date.now fallback）
// ---------------------------------------------------------------------------
describe('default clock source', () => {
  it('default recorder uses performance.now and produces finite nonnegative duration', () => {
    const r = createTimingRecorder()
    r.mark('T0')
    // 让 performance.now 推进一点
    r.mark('T1')
    const m = r.snapshot().measurements.T0_T1
    expect(m.status).toBe('observed')
    expect(typeof m.durationMs).toBe('number')
    expect(Number.isFinite(m.durationMs)).toBe(true)
    expect(m.durationMs as number).toBeGreaterThanOrEqual(0)
  })
})
