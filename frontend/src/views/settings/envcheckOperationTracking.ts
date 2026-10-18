/**
 * 环境检测异步操作受理跟踪（可运行状态机，无 Vue/Wails 依赖）。
 *
 * 解决集成批次发现的 Major 1：绑定受理回执（Start*Async 返回的
 * OperationState / uninstall_tool 的 FixActionResult.success）才是本次操作的
 * 身份起点；snapshot 只是观察窗口。快照可能失败、乱序或永远看不到
 * running 态，因此：
 *  - 有受理回执（含 id）→ 以 id 精确匹配终态；
 *  - uninstall_tool 无 OperationState 回执 → 以 tool+kind 匹配，并用
 *    startedAt/finishedAt 时间界防旧终态误报（Leader 明确要求不误报旧卸载终态）；
 *  - 快照观察失败不清除 pending（轮询继续，有界超时兜底）；
 *  - 终态一旦确认不再回退（pending 清空后不再产生事件）。
 *
 * 组件层用法：受理后 beginOperation；每次 fetchSnapshot（无论成败）后
 * observeOperation；outcome.event 驱动横幅，outcome.running 驱动忙碌态。
 */

/** 与 envcheck.OperationState 结构兼容的最小观察值。 */
export interface OperationObs {
  id?: string
  tool?: string
  kind?: string
  status?: string
  progress?: number
  message?: string
  startedAt?: unknown
  finishedAt?: unknown
  updatedAt?: unknown
  /** 终态事件透传给横幅组装器的字段。 */
  error?: string
  result?: { version?: string; message?: string; error?: string } | null
}

export interface PendingOperation {
  /** 受理回执的 operation id；uninstall_tool 受理无回执时为 ''。 */
  id: string
  tool: string
  kind: string
  /** 受理时刻（ms epoch），id-less 匹配的时间界基准。 */
  acceptedAt: number
  /** 请求前已知的旧槽不可认领，即使其时间位于容忍窗口内。 */
  previousId: string
  requestedAt: number
  lastObservedAt: number
  /** 最近一次匹配观察到的进度/文案（快照失败期间沿用旧值）。 */
  lastProgress: number
  lastMessage: string
}

export interface OperationTrackerState {
  pending: PendingOperation | null
}

export type TrackerEvent =
  | { type: 'none' }
  /** 受理的操作已达终态（含没见过 running 直接终态的快照乱序场景）。 */
  | { type: 'terminal'; status: 'succeeded' | 'failed' | 'timeout'; op: OperationObs }
  /** 有界超时仍无法确认（快照持续失败/操作槽被顶替）→ 需用户可见复检入口。 */
  | { type: 'unknown'; tool: string; kind: string }

export interface TrackerOutcome {
  /** 有效运行态 = pending 未终结（本地受理态优先于快照缺口）。 */
  running: boolean
  progress: number
  message: string
  event: TrackerEvent
}

/** 连续失去匹配观察的窗口；正常运行超过此时长不算超时。 */
export const PENDING_OP_TIMEOUT_MS = 45_000

/** 时钟偏移容忍（App 端受理与后端登记 operation 的时差）。 */
const CLOCK_SKEW_MS = 2_500

function isTerminalStatus(status: unknown): status is 'succeeded' | 'failed' | 'timeout' {
  return status === 'succeeded' || status === 'failed' || status === 'timeout'
}

function tsMs(value: unknown): number {
  if (typeof value === 'number' && isFinite(value)) return value
  if (typeof value === 'string') {
    const t = Date.parse(value)
    if (!isNaN(t)) return t
  }
  return NaN
}

/** beginOperation：登记受理回执（受理即跟踪起点；组件层 busy guard 防重入）。 */
export function beginOperation(
  state: OperationTrackerState,
  ident: { id?: string; tool: string; kind: string; previousId?: string; requestedAt?: number },
  now: number,
): OperationTrackerState {
  return {
    pending: {
      id: ident.id || '',
      tool: ident.tool,
      kind: ident.kind,
      acceptedAt: now,
      previousId: ident.previousId || '',
      requestedAt: ident.requestedAt ?? now,
      lastObservedAt: now,
      lastProgress: 0,
      lastMessage: '',
    },
  }
}

/**
 * id-less（uninstall_tool 受理）候选是否属于本次受理：
 * running 态必然是新操作（旧终态不会变 running）；终态必须能证明发生在
 * 受理之后（startedAt/finishedAt ≥ acceptedAt − 时差容忍），证不出来就不认
 * （宁走超时兜底也不误报旧卸载终态）。
 */
function candidateIsOurs(op: OperationObs, pending: PendingOperation): boolean {
  if (op.id === pending.previousId) return false
  if (op.status === 'running' || op.status === 'idle') return true
  if (!isTerminalStatus(op.status)) return false
  const started = tsMs(op.startedAt)
  if (!isNaN(started)) return started >= pending.requestedAt - CLOCK_SKEW_MS
  const finished = tsMs(op.finishedAt)
  if (!isNaN(finished)) return finished >= pending.requestedAt - CLOCK_SKEW_MS
  return false
}

function outcome(pending: PendingOperation | null, event: TrackerEvent): TrackerOutcome {
  if (!pending) return { running: false, progress: 0, message: '', event }
  return {
    running: true,
    progress: pending.lastProgress,
    message: pending.lastMessage || '操作进行中（等待快照确认…）',
    event,
  }
}

/**
 * observeOperation：把一次快照观察喂给跟踪器。
 * 快照为 null（拉取失败/无 operation）不清 pending；匹配到终态清 pending
 * 并产出 terminal 事件；超时产出 unknown 事件。
 */
export function observeOperation(
  state: OperationTrackerState,
  op: OperationObs | null | undefined,
  now: number,
): { state: OperationTrackerState; outcome: TrackerOutcome } {
  const pending = state.pending
  if (!pending) {
    return { state, outcome: { running: false, progress: 0, message: '', event: { type: 'none' } } }
  }

  if (op && op.id) {
    if (pending.id !== '' && op.id === pending.id) {
      pending.lastObservedAt = now
      if (typeof op.progress === 'number') pending.lastProgress = op.progress
      if (op.message) pending.lastMessage = op.message
      if (isTerminalStatus(op.status)) {
        return { state: { pending: null }, outcome: { running: false, progress: 0, message: '', event: { type: 'terminal', status: op.status, op } } }
      }
      return { state, outcome: outcome(pending, { type: 'none' }) }
    }
    if (pending.id === '' && op.tool === pending.tool && op.kind === pending.kind && candidateIsOurs(op, pending)) {
      pending.id = op.id
      pending.lastObservedAt = now
      if (typeof op.progress === 'number') pending.lastProgress = op.progress
      if (op.message) pending.lastMessage = op.message
      if (isTerminalStatus(op.status)) {
        return { state: { pending: null }, outcome: { running: false, progress: 0, message: '', event: { type: 'terminal', status: op.status, op } } }
      }
      return { state, outcome: outcome(pending, { type: 'none' }) }
    }
    // 快照是别的操作（旧槽/他窗操作）：pending 保持，等待后续快照。
  }

  if (now - pending.lastObservedAt > PENDING_OP_TIMEOUT_MS) {
    return {
      state: { pending: null },
      outcome: {
        running: false,
        progress: 0,
        message: '',
        event: { type: 'unknown', tool: pending.tool, kind: pending.kind },
      },
    }
  }

  return { state, outcome: outcome(pending, { type: 'none' }) }
}

/**
 * 乱序防护：running 视图不应回退到已终结的操作。
 * 组件层维护 resolvedIds；此纯函数判断某快照 operation 是否应被视为运行中。
 */
export function isEffectivelyRunning(
  op: OperationObs | null | undefined,
  resolvedOpIds: ReadonlySet<string>,
): boolean {
  if (!op || op.status !== 'running') return false
  if (op.id && resolvedOpIds.has(op.id)) return false
  return true
}
