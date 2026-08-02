/**
 * mobile/src/lib/timing.ts — M0-06 timing instrumentation skeleton (TS side)
 * ---------------------------------------------------------------------------
 * 设计：fuxi/20260802-m0-06-timing-design/design.md §5（设计方案 A）。
 *
 * 两端小型强类型计时器：两条正交 lane（T0→T1 / R0→R1），固定 enum + duration，
 * 默认零输出、永不接收敏感上下文，无 singleton/storage/network/label 扩展位。
 * M0 的真实 consumer 是 public production module 经 Vitest 单测 + Vite/Chromium
 * dynamic import（骨架 API probe，非页面 lifecycle performance）。
 *
 * 隐私边界（§4.1）：public API 只允许固定 mark/status/reason enum、单调时钟函数、
 * number duration、固定结构 snapshot/reporter callback。不得出现 string label、
 * URL/path/session/device/request ID、terminal chunk、credential、error text、
 * wall-clock timestamp 或任意 Record/map 扩展位。
 *
 * Production 接线裁定（§7.1）：M0 不接 legacy 页面/WS；M2/M3 真实 v1 生命周期出现
 * 后再接线。本文件是 production-file public entry，由显式 test consumer 闭合，
 * 不是 TODO / 死分支。
 */

// ---------------------------------------------------------------------------
// Public types & constants（§5.1）—— 导出面唯一来源，禁止额外 export。
// ---------------------------------------------------------------------------

export const TIMING_MARKS = ['T0', 'T1', 'R0', 'R1'] as const
export type TimingMark = (typeof TIMING_MARKS)[number]

export const TIMING_BUDGET_MS = Object.freeze({
  T0_T1: 3000,
  R0_R1: 5000,
} as const)

export type TimingStatus =
  | 'not_occurred'
  | 'pending'
  | 'observed'
  | 'invalid'

export type TimingBudgetStatus =
  | 'not_evaluated'
  | 'within_budget'
  | 'over_budget'

export type TimingRejectionReason =
  | 'unknown_mark'
  | 'duplicate_mark'
  | 'missing_predecessor'
  | 'clock_invalid'
  | 'clock_regressed'
  | 'lane_invalid'

export type MonotonicClock = () => number

export interface TimingMeasurement {
  readonly status: TimingStatus
  readonly durationMs: number | null
  readonly budgetMs: 3000 | 5000
  readonly budgetStatus: TimingBudgetStatus
  readonly invalidReason: TimingRejectionReason | null
}

export interface TimingReportV1 {
  readonly schemaVersion: 1
  readonly unit: 'ms'
  readonly measurements: Readonly<{
    T0_T1: TimingMeasurement
    R0_R1: TimingMeasurement
  }>
}

export type TimingTransition =
  | Readonly<{ accepted: true }>
  | Readonly<{ accepted: false; reason: TimingRejectionReason }>

export type TimingReporter = (report: TimingReportV1) => void

export interface TimingRecorder {
  mark(mark: TimingMark): TimingTransition
  snapshot(): TimingReportV1
  report(reporter: TimingReporter): TimingReportV1
  reportToConsole(): TimingReportV1
  reset(): void
}

export function createTimingRecorder(clock?: MonotonicClock): TimingRecorder {
  const resolvedClock: MonotonicClock = clock ?? defaultClock

  // 两条正交 lane（§5.3）：互不施加跨 lane 顺序。
  const lanes: { T0_T1: Lane; R0_R1: Lane } = {
    T0_T1: freshLane(),
    R0_R1: freshLane(),
  }

  function mark(m: TimingMark): TimingTransition {
    const mapping = laneAndRole(m)
    // runtime unknown mark：不读 clock、不污染任何 lane（§5.4 末行）。
    if (mapping === null) {
      return { accepted: false, reason: 'unknown_mark' }
    }
    const lane = lanes[mapping.lane]
    return applyMarkToLane(lane, mapping.role, resolvedClock)
  }

  function snapshot(): TimingReportV1 {
    return buildReport(lanes.T0_T1, lanes.R0_R1)
  }

  function report(reporter: TimingReporter): TimingReportV1 {
    // 先取一次 snapshot，再同步调用 reporter 恰一次，返回同一快照；不自动 reset（§5.6.4）。
    const snap = snapshot()
    reporter(snap)
    return snap
  }

  function reportToConsole(): TimingReportV1 {
    // 显式调用时只执行一次 console.info 固定前缀，返回 report（§5.6.5）。
    const snap = snapshot()
    console.info('TIMING_REPORT_V1', snap)
    return snap
  }

  function reset(): void {
    // 清除两 lane 全部值/原因（§5.4 reset 行）。旧 report 快照已深冻结，不受影响。
    lanes.T0_T1 = freshLane()
    lanes.R0_R1 = freshLane()
  }

  return { mark, snapshot, report, reportToConsole, reset }
}

// ---------------------------------------------------------------------------
// Internal —— 非导出。lane 状态机、时钟读取、snapshot 构造、深冻结。
// ---------------------------------------------------------------------------

/**
 * 默认 clock 仅调用 globalThis.performance.now()；不得 fallback 到 Date.now()（§5.2.1）。
 * 浏览器/jsdom/Chromium 均提供 performance.now；本 skeleton 不支持无 performance 的环境。
 */
const defaultClock: MonotonicClock = () => globalThis.performance.now()

/** lane 内部状态（§5.3）：idle | started | complete | faulted，映射报告 4 态。 */
type LaneState = 'idle' | 'started' | 'complete' | 'faulted'

interface Lane {
  state: LaneState
  startedAtMs: number | null
  durationMs: number | null
  invalidReason: TimingRejectionReason | null
}

function freshLane(): Lane {
  return { state: 'idle', startedAtMs: null, durationMs: null, invalidReason: null }
}

interface MarkMapping {
  lane: 'T0_T1' | 'R0_R1'
  role: 'start' | 'end'
}

/** 把 mark 映射到 (lane, role)；运行时非法值返回 null（不依赖 TS 类型保证）。 */
function laneAndRole(mark: TimingMark): MarkMapping | null {
  switch (mark) {
    case 'T0':
      return { lane: 'T0_T1', role: 'start' }
    case 'T1':
      return { lane: 'T0_T1', role: 'end' }
    case 'R0':
      return { lane: 'R0_R1', role: 'start' }
    case 'R1':
      return { lane: 'R0_R1', role: 'end' }
    default:
      return null
  }
}

/**
 * 安全读 clock 一次（§5.2）：throw / 非 number / NaN / ±Inf → ok:false，不保存/回显 thrown error。
 * 调用方在可能接受的转换中恰调用一次；已知非法转换不进入此函数。
 */
function readClock(clock: MonotonicClock): { ok: true; value: number } | { ok: false } {
  let value: number
  try {
    value = clock()
  } catch {
    return { ok: false }
  }
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    return { ok: false }
  }
  return { ok: true, value }
}

/** lane 进入 faulted：清空工作值（fail-closed），记录原因。已 faulted 再 mark 不走这里。 */
function faultLane(lane: Lane, reason: TimingRejectionReason): void {
  lane.state = 'faulted'
  lane.startedAtMs = null
  lane.durationMs = null
  lane.invalidReason = reason
}

/**
 * 精确状态转换（§5.4 表）。已知非法转换不读 clock；可能接受的转换恰读一次。
 * 已 faulted 的 lane 再收到同 lane mark → lane_invalid，但保留首个 invalidReason（不恢复）。
 */
function applyMarkToLane(
  lane: Lane,
  role: 'start' | 'end',
  clock: MonotonicClock,
): TimingTransition {
  // 已 faulted：不读 clock、不恢复，保留首个 invalidReason（§5.4 faulted 行）。
  if (lane.state === 'faulted') {
    return { accepted: false, reason: 'lane_invalid' }
  }

  // complete 后同 lane 任一 mark：结果有歧义，删除旧 duration → faulted/duplicate（§5.4 complete 行）。
  if (lane.state === 'complete') {
    faultLane(lane, 'duplicate_mark')
    return { accepted: false, reason: 'duplicate_mark' }
  }

  if (lane.state === 'started') {
    if (role === 'start') {
      // duplicate start：清除已存 start → faulted/duplicate（§5.4 started/start 行）。
      faultLane(lane, 'duplicate_mark')
      return { accepted: false, reason: 'duplicate_mark' }
    }
    // started + end：读 clock 并算差（§5.4 started/end 行）。
    const end = readClock(clock)
    if (!end.ok) {
      faultLane(lane, 'clock_invalid')
      return { accepted: false, reason: 'clock_invalid' }
    }
    const duration = end.value - (lane.startedAtMs as number)
    if (!Number.isFinite(duration)) {
      // 两有限端点相减溢出 → clock_invalid（§5.2.5、T-08）。
      faultLane(lane, 'clock_invalid')
      return { accepted: false, reason: 'clock_invalid' }
    }
    if (duration < 0) {
      faultLane(lane, 'clock_regressed')
      return { accepted: false, reason: 'clock_regressed' }
    }
    // 有限且 >=0（0 合法）→ complete/observed。
    lane.durationMs = duration
    lane.state = 'complete'
    return { accepted: true }
  }

  // idle
  if (role === 'start') {
    // idle + start：读 clock；有限则只存 start（§5.4 idle/start 行）。
    const start = readClock(clock)
    if (!start.ok) {
      faultLane(lane, 'clock_invalid')
      return { accepted: false, reason: 'clock_invalid' }
    }
    lane.startedAtMs = start.value
    lane.state = 'started'
    return { accepted: true }
  }
  // idle + end：不读 clock；缺前置 → faulted/missing_predecessor（§5.4 idle/end 行）。
  faultLane(lane, 'missing_predecessor')
  return { accepted: false, reason: 'missing_predecessor' }
}

/** 从 lane 构造单条 measurement（§5.6）。无 raw timestamp；预算比较用同一 duration 不舍入。 */
function buildMeasurement(lane: Lane, budgetMs: 3000 | 5000): TimingMeasurement {
  switch (lane.state) {
    case 'idle':
      return Object.freeze({
        status: 'not_occurred',
        durationMs: null,
        budgetMs,
        budgetStatus: 'not_evaluated',
        invalidReason: null,
      })
    case 'started':
      return Object.freeze({
        status: 'pending',
        durationMs: null,
        budgetMs,
        budgetStatus: 'not_evaluated',
        invalidReason: null,
      })
    case 'complete': {
      const d = lane.durationMs as number
      return Object.freeze({
        status: 'observed',
        durationMs: d,
        budgetMs,
        // 预算比较用同一实际 duration，不舍入；<= 边界为 within（§5.6.3）。
        budgetStatus: d <= budgetMs ? 'within_budget' : 'over_budget',
        invalidReason: null,
      })
    }
    case 'faulted':
      return Object.freeze({
        status: 'invalid',
        durationMs: null,
        budgetMs,
        budgetStatus: 'not_evaluated',
        invalidReason: lane.invalidReason,
      })
  }
}

/** 构造深冻结、无共享可变引用的 TimingReportV1（§5.6.1）。 */
function buildReport(tLane: Lane, rLane: Lane): TimingReportV1 {
  const measurements = {
    T0_T1: buildMeasurement(tLane, TIMING_BUDGET_MS.T0_T1),
    R0_R1: buildMeasurement(rLane, TIMING_BUDGET_MS.R0_R1),
  }
  Object.freeze(measurements)
  return deepFreeze({
    schemaVersion: 1 as const,
    unit: 'ms' as const,
    measurements,
  })
}

/** 递归深冻结：snapshot 无可变引用，调用方无法篡改（§5.6.1）。 */
function deepFreeze<T>(value: T): T {
  if (value === null || typeof value !== 'object') {
    return value
  }
  if (Array.isArray(value)) {
    for (const item of value) deepFreeze(item)
  } else {
    const record = value as Record<string, unknown>
    for (const key of Object.keys(record)) deepFreeze(record[key])
  }
  return Object.freeze(value)
}
