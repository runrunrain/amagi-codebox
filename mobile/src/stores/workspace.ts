/**
 * stores/workspace.ts — PG-03 会话工作区状态（M2-C）
 * ---------------------------------------------------------------------------
 * 权威依据：Task Contract M2-C；contract/ws.ts（帧/normalize fail-closed）；
 * M2-A design §6/§7（attach/backfill/边界/Seq/重连恢复语义）。
 * 职责：
 *   · REST 加载 SessionDetail → WS attach → snapshot/history/live 帧订阅；
 *   · TimelineEntry 有序维护：输出段（内容转化）、重启边界（PR-05 原位）、
 *     GapMarker 缺口/补齐原位、用户指令（draft 确认发送语义）；
 *   · 控制权过滤写操作（观察者禁用并明示；control.state 被收回原因提示）；
 *   · 五层状态条投影（复用 M2-B StatusBar 形状）；
 *   · 断线重连恢复呈现（expectedRunPosition 由服务端保证，客户端不伪造连续）。
 * 本 store 不持有任何凭据；Cookie 是唯一凭据载体。
 * ---------------------------------------------------------------------------
 */

import { computed, nextTick, ref } from 'vue';
import { defineStore } from 'pinia';
import {
  ERROR_CODE_AUTH_REVOKED,
  ERROR_CODE_AUTH_UNPAIRED,
  ERROR_CODE_AUTH_WINDOW_EXPIRED,
  ERROR_CODE_CONTROL_FORBIDDEN,
  type ControlSnapshot,
  type DecodedServerEvent,
  type GapRange,
  type InputFrame,
  type ReplayFrame,
  type SessionDetail,
  type SessionID,
  type SessionState,
} from '../lib/contract';
import {
  acquireControl,
  getSessionDetail,
  releaseControl,
  stopSession,
  toApiRequestError,
} from '../lib/api';
import { classifyLobbyError, type ClassifiedError } from './lobby';
import {
  SessionWsClient,
  decodeChunkToText,
  encodeUtf8ToBase64,
  type WsClientState,
} from '../lib/ws';
import { InputOutbox, INPUT_OUTBOX_MAX_ATTEMPTS } from '../lib/inputOutbox';
import { createTimingRecorder } from '../lib/timing';
import {
  transformOutputLines,
  type BoundaryItem,
  type ContentItem,
  type GapItem,
  type TimelineItem,
  type UserItem,
} from '../lib/timeline';
import { RawTranscript } from '../lib/rawTerminal';
import type { AppType } from '../types/terminal';
import type { StatusLayer } from '../components/lobby/StatusBar.vue';
import type { ControlNoticeView } from '../components/workspace/ControlBar.vue';

// ---------------------------------------------------------------------------
// E-06 控制权收回提示文案（design §7 [R2/M-04]）：takeover 映射「桌面端已收回」
// /「由{deviceName}取得」；已知 reason 映射安全文案；unknown reason 不直出。
// ---------------------------------------------------------------------------

const E06_REASON_COPY: Readonly<Record<string, string>> = {
  connection_expired: '控制连接已过期，控制权已收回',
  device_revoked: '设备授权已变更，控制权已收回',
  service_stopped: '桌面服务已停止，控制权已收回',
  security_unavailable: '安全状态不可用，控制权已收回',
  session_removed: '会话已移除，控制权随之失效',
};

function e06NoticeText(state: 'desktop' | 'other' | 'none', deviceName: string | null, reason: string): string {
  if (reason === 'takeover') {
    return state === 'other' && deviceName ? `控制权已由 ${deviceName} 取得` : '桌面端已收回控制权';
  }
  return E06_REASON_COPY[reason] ?? '控制权已收回';
}

// ---------------------------------------------------------------------------
// E-07 恢复回合（design §7）：reconnecting（退避倒计时）→ restored（≥3s）。
// ---------------------------------------------------------------------------

export interface RecoveryEpisode {
  generation: number;
  state: 'reconnecting' | 'restored';
  attempt: number;
  nextDelayMs: number | null;
  /** restored 时仍有可见缺口：文案「已恢复，部分历史不可用」（E-07 引用 E-08）。 */
  withGap: boolean;
  /** 缺口中仍有可补齐项（决定 detail 文案是否承诺「可尝试补齐」）。 */
  gapFillable?: boolean;
}

/** NoticeStack 优先级（design §7）：P0 fatal > P1 actionable lastError > P2 E-07 > P3 degraded。 */
export type PrimaryNoticeLevel = 'fatal' | 'error' | 'recovery' | 'degraded' | null;

// ---------------------------------------------------------------------------
// outbox 可视镜像（M3-C）：InputOutbox 纯状态机不改实现，store 经构造注入的
// send 回调观察每次 wire attempt（含 timer 重试），ACK/halt 在事件分支同步。
// MessageID/requestId 仅存内存映射，绝不进 DOM/快照（隐私 fail-closed）。
// ---------------------------------------------------------------------------

export type OutboxDelivery = 'sending' | 'settled' | 'halted';

interface OutboxMirrorEntry {
  key: string;
  state: OutboxDelivery;
  attemptNo: number;
  userEntryId: string | null;
}

// ---------------------------------------------------------------------------
// TimelineEntry：时间线条目的有序源（sortKey 升序；tie 按插入序）
// ---------------------------------------------------------------------------

interface SegmentEntry {
  entryId: string;
  sortKey: number;
  order: number;
  type: 'segment';
  sealed: boolean;
  lines: string[];
  partial: string;
  /** sealed 段的转化缓存（避免每次全量重算）。 */
  blocksCache: ContentItem[] | null;
}

interface BoundaryEntry {
  entryId: string;
  sortKey: number;
  order: number;
  type: 'boundary';
  seq: number;
  state: SessionState;
  occurredAt: string;
}

interface GapEntry {
  entryId: string;
  sortKey: number;
  order: number;
  type: 'gap';
  fromSeq: number;
  toSeq: number;
  exhausted: boolean;
  filling: boolean;
  /** 瞬时 live-reorder 缺口（design §3：seq>F+1 缓冲时显示）。被填满后由 frontier 剪除；
   * authoritative attached/backfill 缺口 liveReorder=false，永不被 frontier 剪除。 */
  liveReorder: boolean;
}

interface UserEntry {
  entryId: string;
  sortKey: number;
  order: number;
  type: 'user';
  text: string;
  /** M3-C outbox 结算状态（canonical capability；legacy 直发为 null 不显示 chip）。 */
  delivery: OutboxDelivery | null;
  attemptNo: number;
}

type TimelineEntry = SegmentEntry | BoundaryEntry | GapEntry | UserEntry;

let entryCounter = 0;
function nextOrder(): number {
  return ++entryCounter;
}

/** 历史快照 state（null = 尚未 attach）。 */
export type WorkspaceHistoryState = 'continuous' | 'backfilled' | 'gap' | null;

function cliToAppType(cliType: string | undefined): AppType {
  if (cliType === 'claudecode' || cliType === 'opencode' || cliType === 'codex') return cliType;
  return 'generic';
}

// M3-004 live-gap pending 双界（design §3）：帧数 + 字节，与服务端保留窗同阶。
// 超界或同 Seq 冲突 → fail-closed 关闭当前 socket 保守 reattach。
const LIVE_PENDING_MAX_ENTRIES = 4096;
const LIVE_PENDING_MAX_BYTES = 1 << 20;

export const useWorkspaceStore = defineStore('remote-workspace', () => {
  // --- 会话投影 ---
  const sessionId = ref<SessionID>('');
  const detail = ref<SessionDetail | null>(null);
  const loading = ref(true);
  const loadError = ref<ClassifiedError | null>(null);
  const authLost = ref<'revoked' | 'expired' | null>(null);

  // --- WS 连接态 ---
  const wsState = ref<WsClientState>('idle');
  const reconnectAttempt = ref(0);
  const reconnectDelayMs = ref<number | null>(null);
  const terminalReason = ref<string | null>(null);
  /** 曾经 attached：其后的 reconnect 属于「恢复」语义（AC-02 呈现）。 */
  const attachedOnce = ref(false);

  // --- 附着快照层 ---
  const sessionState = ref<SessionState>('stopped');
  const control = ref<ControlSnapshot>({ state: 'none' });
  const historyState = ref<WorkspaceHistoryState>(null);
  /** 控制被收回/冲突提示（E-06；design §7 映射文案，unknown reason 不直出）。 */
  const controlNotice = ref<ControlNoticeView | null>(null);
  /** 服务端 error 事件（契约错误形状原样呈现）。 */
  const lastError = ref<{ code: string; message: string; actionHint: string } | null>(null);
  /** unknown 事件导致的保守降级提示（fail-closed 呈现，不伪造细节）。 */
  const degradedNotice = ref<string | null>(null);

  // --- 时间线 ---
  const entries = ref<TimelineEntry[]>([]);
  // 连续 possession frontier（design §3）：最高 Seq 使 [1,F] 每位都已①合法帧恰好
  // 应用一次，或②被权威裁定不可恢复且 GapMarker 仍保留。它是 attach 游标
  // （首次 omit；重连显式发送 F，含 0）与 live 帧投影门。detail.latestSeq /
  // latestSeenSeq / REST bounds / CausalWatermark 均不得赋给 F。
  const settledFrontier = ref(0);
  // 最高已见 Seq（含缓冲高帧；用于 UserEntry 排序，不作游标、不越洞推进 F）。
  const latestSeenSeq = ref(0);
  // 首次 attach 是否完成（区分首次 wire omit cursor / 重连显式发送 frontier）。
  const hasCompletedAttach = ref(false);
  // 有界 pending（M3-004）：seq > F+1 的乱序/越洞 live 帧缓冲。帧数+字节双界，
  // 同 Seq 冲突 fail-closed；overflow 关闭 socket 保守 reattach（design §3）。
  const pendingBySeq = new Map<number, ReplayFrame>();
  let pendingBytes = 0;

  // --- 原始输出流（PG-04 诊断视图消费；M2-D） ---
  // 有界滚动缓冲：诊断视图打开时回放，其后经 subscribeRawOutput 续写。
  // 只存解码后原文（ANSI 原样），不做行处理；重连不清空（attach 携带 lastSeq
  // 只回补增量，流连续；open() 新会话才清空）。backfill 帧是乱序旧历史，
  // 不注入本流（缺口由状态条历史层诚实呈现，不在网格内伪造内容）。
  const rawTranscript = new RawTranscript({ maxChars: 256_000 });
  const rawSubscribers = new Set<(text: string) => void>();

  function notifyRaw(text: string): void {
    for (const cb of rawSubscribers) cb(text);
  }

  // --- Composer ---
  const draft = ref('');
  const sending = ref(false);
  const commandHistory = ref<string[]>([]);
  /** 停止运行提交态（防连点）。 */
  const stopping = ref(false);

  let client: SessionWsClient | null = null;
  let outbox: InputOutbox | null = null;
  /** CG-03 input-ack capability active（attached.inputAckMode === canonical）。 */
  const hasInputAckCapability = ref(false);
  // M3-C outbox 可视镜像：attemptNo/状态经 send 回调与 ACK/halt 同步。
  const outboxMirror = ref<OutboxMirrorEntry[]>([]);
  const mirrorByMsgId = new Map<string, OutboxMirrorEntry>();
  const mirrorByRequestId = new Map<string, OutboxMirrorEntry>();
  let outboxKeyCounter = 0;
  let outboxSettledCount = 0;
  // M3-B timing（design §6）：recovery episode R0/R1 + 隐私 fail-closed 内存快照。
  const timing = createTimingRecorder();
  /** recovery episode 代际（每次断线→重连回合递增；去重 duplicate R0/R1）。 */
  let recoveryGeneration = 0;
  let r0MarkedForGen = -1;
  let r1ArmedForGen = -1;
  /** E-07 恢复回合（ContinuityBanner 投影源）。 */
  const recoveryEpisode = ref<RecoveryEpisode | null>(null);
  let restoredTimer: ReturnType<typeof setTimeout> | null = null;
  /** 本回合是否处于恢复（reconnecting→reattach）；区分首次 attach 与恢复 attach。 */
  let inRecovery = false;
  /** 本地 release intent（R4-001）：发起 release REST 前登记 generation（>0=在途），
   *  仅由 correlated control.state(state!=you, reason=released) 事件消费；REST 失败撤销。
   *  服务端仅本设备 Release（holder→none）能产生 prevControl=you 的 reason=released 事件
   *  （control_arbiter.go Release 要求 owner==请求设备；desktop release 时本端 prevControl
   *  为 desktop 而非 you），故 reason 匹配即精确关联，不会误抑制被动收回（takeover/
   *  connection_expired 等 reason 不匹配，E-06 照常显示）。 */
  let localReleaseSeq = 0;
  let pendingLocalReleaseGen = 0;
  /** attach 计数（design §8 C1 oracle：attached 总数）。 */
  let attachedCount = 0;
  let onlineHandler: (() => void) | null = null;
  const pendingBackfills = new Map<string, string>(); // requestId → gap entryId

  // continuitySnapshot 机器 oracle（design §8 [R2/M-05]）：seq 多重集计数，
  // ≤4096 项（超出标 truncated）；无 ID/payload/URL。
  const appliedSeqCounts = new Map<number, number>();
  let appliedSeqTruncated = false;

  function noteAppliedSeq(seq: number): void {
    if (!appliedSeqCounts.has(seq) && appliedSeqCounts.size >= 4096) {
      appliedSeqTruncated = true;
      return;
    }
    appliedSeqCounts.set(seq, (appliedSeqCounts.get(seq) ?? 0) + 1);
  }

  // --- outbox 镜像辅助（M3-C） ---

  /** send 回调观察点：每次 wire attempt（含 timer 重试/reattach 立即重试）计数。 */
  function noteOutboxAttempt(frame: InputFrame): void {
    let m = mirrorByMsgId.get(frame.id);
    if (!m) {
      m = { key: `ob-${++outboxKeyCounter}`, state: 'sending', attemptNo: 0, userEntryId: null };
      mirrorByMsgId.set(frame.id, m);
      outboxMirror.value.push(m);
    }
    m.attemptNo += 1;
    mirrorByRequestId.set(frame.requestId, m);
    syncUserDelivery(m);
  }

  /** 把镜像状态同步到关联的用户指令卡（不含 ID，卡片只显示状态文案）。 */
  function syncUserDelivery(m: OutboxMirrorEntry): void {
    if (!m.userEntryId) return;
    const ue = entries.value.find((e) => e.entryId === m.userEntryId);
    if (ue && ue.type === 'user') {
      ue.delivery = m.state;
      ue.attemptNo = m.attemptNo;
    }
  }

  /** 冻结 flush：outbox halt + 镜像/用户卡同步（控制权变化/terminal/授权失效）。 */
  function haltOutboxMirror(): void {
    outbox?.halt();
    for (const m of outboxMirror.value) {
      if (m.state === 'sending') {
        m.state = 'halted';
        syncUserDelivery(m);
      }
    }
  }

  /** M3-002 非永久冻结：outbox freezePending + 镜像/用户卡同步。用于 restart boundary /
   *  reattach 权威丢失——冻结 pending 不重发（不跨 holder/run），但保留 authority 恢复后
   *  接受新 input 的能力（区别于 haltOutboxMirror 的永久 halt）。 */
  function freezeOutboxMirror(): void {
    outbox?.freezePending();
    for (const m of outboxMirror.value) {
      if (m.state === 'sending') {
        m.state = 'halted';
        syncUserDelivery(m);
      }
    }
  }

  const appType = computed<AppType>(() => cliToAppType(detail.value?.cliType));

  // -------------------------------------------------------------------------
  // Entry 维护
  // -------------------------------------------------------------------------

  function insertEntry(entry: TimelineEntry): void {
    const list = entries.value;
    let idx = list.length;
    for (let i = list.length - 1; i >= 0; i--) {
      const e = list[i];
      if (e.sortKey < entry.sortKey || (e.sortKey === entry.sortKey && e.order < entry.order)) {
        idx = i + 1;
        break;
      }
      if (i === 0) idx = 0;
    }
    list.splice(idx, 0, entry);
  }

  /** 当前活跃输出段（最后一个未 sealed segment；无则创建）。 */
  function activeSegment(firstSeq: number): SegmentEntry {
    for (let i = entries.value.length - 1; i >= 0; i--) {
      const e = entries.value[i];
      if (e.type === 'segment' && !e.sealed) return e;
      if (e.type === 'boundary') break;
    }
    const seg: SegmentEntry = {
      entryId: `seg-${firstSeq}-${nextOrder()}`,
      sortKey: firstSeq,
      order: nextOrder(),
      type: 'segment',
      sealed: false,
      lines: [],
      partial: '',
      blocksCache: null,
    };
    insertEntry(seg);
    return seg;
  }

  function appendOutputText(seq: number, text: string): void {
    rawTranscript.append(text);
    notifyRaw(text);
    const seg = activeSegment(seq);
    // 按行切分；保留未完成的尾行为 partial（实时呈现）。
    const merged = seg.partial + text;
    const parts = merged.split('\n');
    seg.partial = parts.pop() ?? '';
    for (const p of parts) seg.lines.push(p.replace(/\r$/, ''));
  }

  function sealAtBoundary(frame: { seq: number; state: SessionState; occurredAt: string }): void {
    for (let i = entries.value.length - 1; i >= 0; i--) {
      const e = entries.value[i];
      if (e.type === 'segment' && !e.sealed) {
        e.sealed = true;
        e.blocksCache = transformOutputLines([...e.lines, ...(e.partial ? [e.partial] : [])], {
          idPrefix: e.entryId,
          appType: appType.value,
        });
        break;
      }
      if (e.type === 'boundary') break;
    }
    insertEntry({
      entryId: `boundary-${frame.seq}`,
      sortKey: frame.seq + 0.5,
      order: nextOrder(),
      type: 'boundary',
      seq: frame.seq,
      state: frame.state,
      occurredAt: frame.occurredAt,
    });
  }

  function addGapEntry(gap: GapRange, liveReorder = false, exhausted = false): void {
    // 去重：同区间缺口不重复插入；已存在且新信息裁定为不可恢复时升级为 exhausted。
    const dup = entries.value.find((e) => e.type === 'gap' && e.fromSeq === gap.fromSeq && e.toSeq === gap.toSeq);
    if (dup) {
      if (exhausted && dup.type === 'gap') dup.exhausted = true;
      return;
    }
    insertEntry({
      entryId: `gap-${gap.fromSeq}-${gap.toSeq}`,
      sortKey: gap.fromSeq - 0.5,
      order: nextOrder(),
      type: 'gap',
      fromSeq: gap.fromSeq,
      toSeq: gap.toSeq,
      exhausted,
      filling: false,
      liveReorder,
    });
  }

  /** Apply a single replay frame to the timeline unconditionally (no frontier gate). */
  function applySingleReplayFrame(frame: ReplayFrame): void {
    noteAppliedSeq(frame.seq);
    if (frame.type === 'output') {
      appendOutputText(frame.seq, decodeChunkToText(frame.chunk));
    } else if (frame.type === 'session.state' && frame.restartBoundary === true) {
      sealAtBoundary(frame);
    }
  }

  /** Apply attached/backfill history frames directly (server guarantees order). */
  function applyReplayFrames(frames: ReplayFrame[]): void {
    for (const frame of frames) applySingleReplayFrame(frame);
  }

  /**
   * settledFrontier projector for LIVE replay frames (design §3). Only seq == F+1
   * is projected and advances F; seq > F+1 is buffered in bounded pending and a
   * recoverable GapRange [F+1, seq-1] is shown; seq <= F is a duplicate/late drop.
   * Absorbs contiguous pending after each advance and prunes filled gaps.
   */
  function projectLiveReplayFrame(frame: ReplayFrame): void {
    const seq = frame.seq;
    if (seq > latestSeenSeq.value) latestSeenSeq.value = seq;
    if (seq <= settledFrontier.value) return; // duplicate/late already-settled
    if (seq === settledFrontier.value + 1) {
      applySingleReplayFrame(frame);
      settledFrontier.value = seq;
      absorbPending();
      pruneFilledGaps();
      return;
    }
    // seq > F+1: bounded buffer (M3-004) + recoverable gap [F+1, seq-1].
    if (pendingBySeq.has(seq)) {
      // 同 Seq 冲突（同一未来 seq 两个不同帧）= 流不一致 → fail-closed 重连。
      haltOverflowAndReconnect('duplicate-live-seq');
      return;
    }
    const frameBytes = liveFrameBytes(frame);
    if (pendingBySeq.size >= LIVE_PENDING_MAX_ENTRIES || pendingBytes + frameBytes > LIVE_PENDING_MAX_BYTES) {
      haltOverflowAndReconnect('live-pending-overflow');
      return;
    }
    pendingBySeq.set(seq, frame);
    pendingBytes += frameBytes;
    addGapEntry({ code: 'history.gap', fromSeq: settledFrontier.value + 1, toSeq: seq - 1 }, true);
  }

  /** Absorb contiguous pending frames after the frontier advanced. */
  function absorbPending(): void {
    while (pendingBySeq.has(settledFrontier.value + 1)) {
      const f = pendingBySeq.get(settledFrontier.value + 1) as ReplayFrame;
      pendingBySeq.delete(settledFrontier.value + 1);
      pendingBytes -= liveFrameBytes(f);
      applySingleReplayFrame(f);
      settledFrontier.value = f.seq;
    }
  }

  /** 近似单帧内存占用（chunk base64 长度 + 固定帧开销），用于 live pending 字节界。 */
  function liveFrameBytes(frame: ReplayFrame): number {
    const chunk = 'chunk' in frame && typeof frame.chunk === 'string' ? frame.chunk.length : 0;
    return chunk + 256;
  }

  /** M3-004：live pending 越界/同 Seq 冲突 → 清空 pending、关闭当前 socket 触发保守
   *  reattach（reattach 携带 frontier 游标，服务端按保留窗重新对齐）。 */
  function haltOverflowAndReconnect(_reason: string): void {
    pendingBySeq.clear();
    pendingBytes = 0;
    client?.forceReconnect();
  }

  /** Remove live-reorder (transient) gap entries fully behind the frontier.
   * Authoritative attached/backfill gaps are never pruned (liveReorder=false). */
  function pruneFilledGaps(): void {
    const list = entries.value;
    for (let i = list.length - 1; i >= 0; i--) {
      const e = list[i];
      if (e.type === 'gap' && e.liveReorder && !e.exhausted && e.toSeq < settledFrontier.value) {
        list.splice(i, 1);
      }
    }
  }

  /** backfill 补齐帧：专属段原位插入（不混入活跃段；补齐帧为完整历史，即封即算）。 */
  function applyBackfillFrames(frames: ReplayFrame[]): void {
    let seg: SegmentEntry | null = null;
    const finalize = () => {
      if (!seg) return;
      seg.sealed = true;
      seg.blocksCache = transformOutputLines([...seg.lines, ...(seg.partial ? [seg.partial] : [])], {
        idPrefix: seg.entryId,
        appType: appType.value,
      });
      seg = null;
    };
    for (const frame of frames) {
      noteAppliedSeq(frame.seq);
      if (frame.type === 'output') {
        if (!seg) {
          seg = {
            entryId: `seg-bf-${frame.seq}-${nextOrder()}`,
            sortKey: frame.seq,
            order: nextOrder(),
            type: 'segment',
            sealed: false,
            lines: [],
            partial: '',
            blocksCache: null,
          };
          insertEntry(seg);
        }
        const text = decodeChunkToText(frame.chunk);
        const merged = seg.partial + text;
        const parts = merged.split('\n');
        seg.partial = parts.pop() ?? '';
        for (const p of parts) seg.lines.push(p.replace(/\r$/, ''));
      } else if (frame.type === 'session.state' && frame.restartBoundary === true) {
        finalize();
        insertEntry({
          entryId: `boundary-${frame.seq}`,
          sortKey: frame.seq + 0.5,
          order: nextOrder(),
          type: 'boundary',
          seq: frame.seq,
          state: frame.state,
          occurredAt: frame.occurredAt,
        });
      }
    }
    finalize();
  }

  // -------------------------------------------------------------------------
  // WS 事件
  // -------------------------------------------------------------------------

  function handleWsEvent(event: DecodedServerEvent): void {
    switch (event.type) {
      case 'session.attached': {
        // M3-002：reattach 续传决策须在 attached snapshot 原子应用之后（见本块末尾）。
        const isReattach = hasCompletedAttach.value;
        attachedOnce.value = true;
        attachedCount += 1;
        sessionState.value = event.snapshot.session.state;
        control.value = event.snapshot.control;
        historyState.value = event.snapshot.history.state;
        // design §3：attached 的 earliestSeq>F+1 可把范围改为 unavailable——
        // 缺口整体低于服务端保留窗（toSeq < earliestSeq）即裁定 settled-unavailable，
        // GapMarker 以 exhausted 原位保留（显式不可恢复提示，不再提供补齐按钮）。
        const prevFrontier = settledFrontier.value;
        if (event.snapshot.history.state === 'gap' && event.snapshot.history.gap) {
          const g = event.snapshot.history.gap;
          addGapEntry(g, false, g.toSeq < event.earliestSeq && event.earliestSeq > prevFrontier + 1);
        } else if (event.earliestSeq > prevFrontier + 1) {
          // 快照未携带 gap 但 attach bounds 证明 [F+1, earliest-1] 已逐出：原位 exhausted 标记。
          addGapEntry({ code: 'history.gap', fromSeq: prevFrontier + 1, toSeq: event.earliestSeq - 1 }, false, true);
        }
        // CG-03：inputAckMode === canonical 时激活 canonical input outbox + ACK 结算。
        hasInputAckCapability.value = event.inputAckMode === 'session-window-v1';
        // 旧连接的 stale pending 作废：attached history 是权威因果切面。
        pendingBySeq.clear();
        pendingBytes = 0;
        applyReplayFrames(event.history);
        // 结算 frontier 至 attached latestSeq：tail 已全量应用；被逐出的 gap 是
        // settled-unavailable（允许 F 跨过——design §3）。detail.latestSeq 不作游标。
        settledFrontier.value = event.latestSeq;
        hasCompletedAttach.value = true;
        if (event.latestSeq > latestSeenSeq.value) latestSeenSeq.value = event.latestSeq;
        pruneFilledGaps();
        lastError.value = null;
        // E-07：恢复 attach（非首次）→ restored（含 gap 时诚实标注部分历史不可用），
        // ≥3s 后自动消退；terminal/P0 覆盖时由 primaryNotice 隐藏。
        if (inRecovery) {
          inRecovery = false;
          const generation = recoveryGeneration;
          recoveryEpisode.value = {
            generation,
            state: 'restored',
            attempt: 0,
            nextDelayMs: null,
            withGap: entries.value.some((e) => e.type === 'gap'),
            gapFillable: entries.value.some((e) => e.type === 'gap' && !e.exhausted),
          };
          if (restoredTimer) clearTimeout(restoredTimer);
          restoredTimer = setTimeout(() => {
            restoredTimer = null;
            const ep = recoveryEpisode.value;
            if (ep && ep.generation === generation && ep.state === 'restored' && wsState.value === 'attached') {
              recoveryEpisode.value = null;
            }
          }, 3000);
          // R1（design §6）：仅当本回合已有合格 R0 才结算 R lane；否则该 episode
          // 无 R 样本（不补造 R0/R1），仍恢复业务。
          if (r1ArmedForGen !== generation && r0MarkedForGen === generation) {
            r1ArmedForGen = generation;
            nextTick(() => timing.mark('R1'));
          }
        }
        // M3-002：reattach 续传决策（snapshot 已原子应用：control/run/history 全部落
        // store）。仅当 authority 仍为 you、run 连续（attached 权威历史无 restart
        // boundary）且会话 running 时重发队首；否则冻结（design §5：pending 绝不
        // 自动落到新 holder/run）。
        if (isReattach) {
          const restartInHistory = event.history.some(
            (fr) => fr.type === 'session.state' && fr.restartBoundary === true,
          );
          if (control.value.state === 'you' && sessionState.value === 'running' && !restartInHistory) {
            outbox?.onReattach();
          } else {
            // 权威丢失 / 新 run：冻结 pending（不重发），但非永久——authority 恢复后
            // 仍可接受新 input（design §5：pending 绝不自动落到新 holder/run）。
            freezeOutboxMirror();
          }
        }
        break;
      }
      case 'output': {
        projectLiveReplayFrame(event);
        break;
      }
      case 'input.ack': {
        // CG-03 M3-003：canonical input 结算。精确关联当前 session；onAck 内部要求
        // entry MessageID 且 requestId 属于 all-attempt 集合（halted entry 仍接迟到 ACK）。
        if (event.sessionId !== sessionId.value) break;
        const settled = outbox?.onAck(event.id, event.requestId) ?? false;
        if (settled) {
          const m = mirrorByMsgId.get(event.id) ?? mirrorByRequestId.get(event.requestId);
          if (m) {
            m.state = 'settled';
            outboxSettledCount += 1;
            syncUserDelivery(m);
            outboxMirror.value = outboxMirror.value.filter((x) => x !== m);
            mirrorByMsgId.delete(event.id);
            for (const [rid, entry] of mirrorByRequestId) if (entry === m) mirrorByRequestId.delete(rid);
          }
        }
        break;
      }
      case 'backfill.result': {
        // M3-004：session + requestId + range exact correlation。无匹配请求或 session/
        // 范围不一致 → fail-closed 丢弃（不应用未经请求的补齐帧）。
        if (event.sessionId !== sessionId.value) break;
        const gapEntryId = pendingBackfills.get(event.requestId);
        if (!gapEntryId) break;
        pendingBackfills.delete(event.requestId);
        const gapEntry = entries.value.find((e) => e.entryId === gapEntryId);
        const rangeMatch =
          !!gapEntry &&
          gapEntry.type === 'gap' &&
          gapEntry.fromSeq === event.fromSeq &&
          gapEntry.toSeq === event.toSeq;
        // M3-004：范围错配（服务端返回的范围与请求的 marker 不一致）→ 不应用任何
        // 结果，但必须恢复 filling=false，绝不留下永久 filling marker（用户可重新
        // 请求或重连恢复）。
        if (!rangeMatch) {
          if (gapEntry && gapEntry.type === 'gap') gapEntry.filling = false;
          break;
        }
        if ('frames' in event && event.frames) {
          // 补齐：原位替换缺口标记为转化后的内容块。
          if (gapEntry) {
            const idx = entries.value.indexOf(gapEntry);
            if (idx >= 0) entries.value.splice(idx, 1);
          }
          applyBackfillFrames(event.frames);
          // M3-004：correlated backfill 推进 frontier 并吸收缓冲高帧。仅当补齐范围与
          // 当前 frontier 邻接（fromSeq <= F+1 且 toSeq > F）时推进（非 frontier 的
          // 权威缺口不推进 F）。单事务：应用 frames → 推进 F → 吸收 pending → 剪枝。
          if (event.fromSeq <= settledFrontier.value + 1 && event.toSeq > settledFrontier.value) {
            settledFrontier.value = event.toSeq;
            absorbPending();
            pruneFilledGaps();
          }
          historyState.value = 'backfilled';
        } else if (event.gap) {
          // gap 变体：服务端权威裁定该段不可补齐（诚实呈现，不伪造）。
          if (gapEntry && gapEntry.type === 'gap') {
            gapEntry.exhausted = true;
            gapEntry.filling = false;
            // M3-004 (R3)：authoritative gap 结算 —— 推进 frontier 跨过 settled-
            // unavailable 范围并吸收缓冲高帧（design §3：权威裁定不可恢复后 F 允许
            // 跨过，gap marker 原位保留为不可恢复提示；liveReorder=false 的
            // authoritative marker 不被 pruneFilledGaps 剪除）。使用权威 gap 范围结算：
            // v1 contract（validator）保证 gap 覆盖整段请求范围（gap.fromSeq==
            // fromSeq && gap.toSeq==toSeq；无 partial gap 表示），故与 outer 等价但
            // 语义明确——只跨过「已裁定不可恢复」的位置。仅当范围与当前 frontier
            // 邻接（gap.fromSeq <= F+1 且 gap.toSeq >= F+1）时推进。
            const gapFrom = event.gap.fromSeq;
            const gapTo = event.gap.toSeq;
            if (gapFrom <= settledFrontier.value + 1 && gapTo >= settledFrontier.value + 1) {
              settledFrontier.value = gapTo;
              absorbPending();
              pruneFilledGaps();
            }
          }
        }
        break;
      }
      case 'session.state': {
        sessionState.value = event.state;
        if (event.restartBoundary === true) {
          // 重启边界是 live replay 帧（占 seq），经 frontier 投影门。
          projectLiveReplayFrame(event);
          // M3-002：restart = 新 run，冻结 pending input（不跨 run 重发）。非永久冻结：
          // 新 run 仍可接受新 input（区别于 terminal 会话态的永久 halt）。
          freezeOutboxMirror();
        } else if (event.state === 'exited' || event.state === 'removed' || event.state === 'unavailable') {
          // M3-002：terminal 会话态 → 永久冻结 pending input。
          haltOutboxMirror();
        }
        break;
      }
      case 'control.state': {
        const prevControl = control.value;
        control.value =
          event.state === 'other'
            ? { state: 'other', deviceName: event.deviceName }
            : { state: event.state };
        // E-06（design §7）：仅 previous=you → other|desktop|none 且非本地 correlated
        // release 时显示当次 control notice；初始 observer 与旁观态变迁不伪称「被收回」。
        // R4-001：intent 在发起 release 前已 armed（见 release()），WS 事件先于 REST
        // response 到达（server 在 release 事务内同步入队事件）也能正确抑制；只消费
        // correlated reason=released 事件，无关 control 事件不清除 intent。
        const localRelease =
          pendingLocalReleaseGen > 0 && event.state !== 'you' && event.reason === 'released';
        if (localRelease) {
          pendingLocalReleaseGen = 0;
        }
        if (event.state === 'you') {
          controlNotice.value = null;
        } else if (prevControl.state === 'you' && !localRelease) {
          controlNotice.value = {
            kind: 'lost',
            controlState: event.state,
            deviceName: event.state === 'other' ? (event.deviceName ?? null) : null,
            text: e06NoticeText(event.state, event.state === 'other' ? (event.deviceName ?? null) : null, event.reason),
          };
        }
        // R2-001 / R3-001：所有离开 previous=you 的 authority transition（含 none、
        // 含本地 release 的 correlated none）都冻结旧 pending（freezePending，非永久）：
        // design §7 E-06 将 you→other|desktop|none 统一为失权；you→none
        // (connection_expired) 与 takeover 同等失权语义——旧 entry 停止自动重发，
        // 不跨失权窗口继续 retry，重新获权后按 design §5 决定 pending 结算/重发（旧
        // entry 绝不自动落到新 holder；只有全新 input 在新 authority 下可发送）。
        // R4-001：intent 只控制 E-06 notice 显示，不绕过安全冻结（本地 release
        // 的 control.value 已由 release() 急切置 none 并显式冻结；此处覆盖事件驱动的
        // desktop/other/none，含 connection_expired）。区别于 revoke/remove/terminal
        // 会话态的永久 halt。
        if (prevControl.state === 'you' && event.state !== 'you') {
          freezeOutboxMirror();
        }
        break;
      }
      case 'auth.revoked': {
        authLost.value = 'revoked';
        break;
      }
      case 'error': {
        // NoticeStack P1=actionable（design §7）：control≠you 时系统写操作（如附着后
        // 自动 resize）被 exact authority 拒绝是预期背压，非用户可操作错误——不入
        // P1 lastError（否则常驻错误条会永久压制 E-07 恢复条）。用户发起写已被
        // canWrite 前置过滤；权威 desync（自以为 you 却被拒）仍按 P1 呈现。
        if (event.code === ERROR_CODE_CONTROL_FORBIDDEN && control.value.state !== 'you') {
          break;
        }
        lastError.value = { code: event.code, message: event.message, actionHint: event.actionHint };
        if (
          event.code === ERROR_CODE_AUTH_REVOKED ||
          event.code === ERROR_CODE_AUTH_UNPAIRED ||
          event.code === ERROR_CODE_AUTH_WINDOW_EXPIRED
        ) {
          authLost.value = 'revoked';
        }
        break;
      }
      case 'unknown': {
        // fail-closed（addendum §4.4）：按 fallback 保守降级，不读取任何原始字段。
        if (event.fallback === 'force-unauthorized') {
          authLost.value = 'revoked';
        } else if (event.fallback === 'force-read-only') {
          control.value = { state: 'none' };
          degradedNotice.value = '收到无法识别的控制信息，已按只读观察处理。';
        } else if (event.fallback === 'mark-history-gap') {
          historyState.value = 'gap';
          degradedNotice.value = '部分历史可能不完整（收到无法识别的历史信息）。';
        } else if (event.fallback === 'mark-session-unavailable') {
          sessionState.value = 'unavailable';
          degradedNotice.value = '会话状态暂不可信，已按不可用处理。';
        }
        // ignore：静默丢弃。
        break;
      }
    }
  }

  // -------------------------------------------------------------------------
  // 生命周期
  // -------------------------------------------------------------------------

  async function open(id: SessionID): Promise<void> {
    // 复用 store 跨会话时先清态（不残留上一会话的时间线/连接态）。
    close();
    sessionId.value = id;
    detail.value = null;
    loadError.value = null;
    authLost.value = null;
    wsState.value = 'idle';
    reconnectAttempt.value = 0;
    reconnectDelayMs.value = null;
    terminalReason.value = null;
    attachedOnce.value = false;
    control.value = { state: 'none' };
    historyState.value = null;
    controlNotice.value = null;
    lastError.value = null;
    degradedNotice.value = null;
    entries.value = [];
    settledFrontier.value = 0;
    latestSeenSeq.value = 0;
    hasCompletedAttach.value = false;
    pendingBySeq.clear();
    pendingBytes = 0;
    timing.reset();
    recoveryGeneration = 0;
    r0MarkedForGen = -1;
    r1ArmedForGen = -1;
    recoveryEpisode.value = null;
    if (restoredTimer) {
      clearTimeout(restoredTimer);
      restoredTimer = null;
    }
    inRecovery = false;
    localReleaseSeq = 0;
    pendingLocalReleaseGen = 0;
    attachedCount = 0;
    hasInputAckCapability.value = false;
    outboxMirror.value = [];
    mirrorByMsgId.clear();
    mirrorByRequestId.clear();
    outboxSettledCount = 0;
    appliedSeqCounts.clear();
    appliedSeqTruncated = false;
    rawTranscript.clear();
    draft.value = '';
    sending.value = false;
    stopping.value = false;
    loading.value = true;
    try {
      const d = await getSessionDetail(id);
      detail.value = d;
      sessionState.value = d.state;
      control.value = d.control;
      // detail.latestSeq 是 REST 边界（advisory），不作 attach 游标（design §3：
      // possession ≠ bound）。首次 attach omit lastSeq，由服务端返回全量 retained tail。
    } catch (rawErr) {
      const err = toApiRequestError(rawErr);
      if (err.code === ERROR_CODE_AUTH_REVOKED || err.code === ERROR_CODE_AUTH_UNPAIRED || err.code === ERROR_CODE_AUTH_WINDOW_EXPIRED) {
        authLost.value = err.code === ERROR_CODE_AUTH_REVOKED ? 'revoked' : 'expired';
      } else {
        loadError.value = classifyLobbyError(err);
      }
      loading.value = false;
      return;
    }
    loading.value = false;

    client = new SessionWsClient({
      sessionId: id,
      getLastSeq: () => (hasCompletedAttach.value ? settledFrontier.value : undefined),
      onEvent: handleWsEvent,
      onStateChange: (change) => {
        const prev = wsState.value;
        wsState.value = change.state;
        reconnectAttempt.value = change.attempt;
        reconnectDelayMs.value = change.nextDelayMs;
        if (change.terminalReason !== null) terminalReason.value = change.terminalReason;
        // M3-B timing（design §6）：断线→重连回合。reconnecting 开启新 episode。
        if (change.state === 'reconnecting') {
          if (prev !== 'reconnecting') {
            recoveryGeneration += 1;
            inRecovery = true;
            recoveryEpisode.value = {
              generation: recoveryGeneration,
              state: 'reconnecting',
              attempt: change.attempt,
              nextDelayMs: change.nextDelayMs,
              withGap: false,
            };
          } else {
            // 退避推进：更新尝试序号/下次重试倒计时（E-07 reconnecting 呈现）。
            const ep = recoveryEpisode.value;
            if (ep && ep.state === 'reconnecting') {
              ep.attempt = change.attempt;
              ep.nextDelayMs = change.nextDelayMs;
            }
          }
        }
        // M3-002：reattach 续传决策已移入 session.attached 处理（snapshot 原子应用
        // 之后，按 authority/run 续传条件决定重发/冻结）。terminal/授权失效 → halt。
        if (change.state === 'closed') {
          inRecovery = false;
          recoveryEpisode.value = null;
          if (restoredTimer) {
            clearTimeout(restoredTimer);
            restoredTimer = null;
          }
        }
        if (change.state === 'closed' || authLost.value !== null) {
          haltOutboxMirror();
        }
      },
    });
    // CG-03 canonical input outbox（workspace continuity state，socket replacement 不重置）。
    outbox = new InputOutbox({
      randomBytes: (n: number) => crypto.getRandomValues(new Uint8Array(n)),
      now: () => Date.now(),
      setTimeout: (fn: () => void, ms: number) => setTimeout(fn, ms),
      clearTimeout: (h: unknown) => clearTimeout(h as ReturnType<typeof setTimeout>),
      send: (frame) => {
        const sent = client?.sendInputFrame(frame) ?? false;
        // M3-C 镜像：attempt 已发生（outbox 内部计数），与 wire 成败无关。
        noteOutboxAttempt(frame);
        return sent;
      },
    });
    client.connect();
    // M3-B timing R0（design §6）：合格浏览器 online 事件（断线后）打 R0；
    // design §4：合格 online 取消待执行 timer 立即重试（同一 episode 内重复 online 忽略）。
    onlineHandler = () => {
      if (wsState.value === 'reconnecting' && r0MarkedForGen !== recoveryGeneration) {
        r0MarkedForGen = recoveryGeneration;
        timing.mark('R0');
        client?.connect();
      }
    };
    if (typeof window !== 'undefined' && window.addEventListener) {
      window.addEventListener('online', onlineHandler);
    }
  }

  function close(): void {
    client?.dispose();
    client = null;
    outbox?.dispose();
    outbox = null;
    if (restoredTimer) {
      clearTimeout(restoredTimer);
      restoredTimer = null;
    }
    recoveryEpisode.value = null;
    inRecovery = false;
    if (onlineHandler && typeof window !== 'undefined' && window.removeEventListener) {
      window.removeEventListener('online', onlineHandler);
    }
    onlineHandler = null;
    pendingBackfills.clear();
  }

  // -------------------------------------------------------------------------
  // 写操作（全部经控制权过滤）
  // -------------------------------------------------------------------------

  const canWrite = computed(
    () =>
      control.value.state === 'you' &&
      wsState.value === 'attached' &&
      sessionState.value === 'running' &&
      // CG-03 M3-001：服务端未协商 inputAckMode（absent/unknown）= 只读。新客户端
      // 绝不发送 legacy input；capability 缺失时写操作一律前置过滤。
      hasInputAckCapability.value,
  );

  /** 观察者/不可写原因（Composer 禁用提示）。 */
  const writeBlockReason = computed<string | null>(() => {
    if (control.value.state === 'desktop') return '桌面端正在控制，你可观察但无法输入';
    if (control.value.state === 'other') return `控制权在 ${(control.value as { deviceName: string }).deviceName}，你可观察但无法输入`;
    if (control.value.state === 'none') return '需要先获取控制权才能输入';
    if (wsState.value !== 'attached') return '连接未就绪，恢复后才能输入';
    if (sessionState.value !== 'running') return '会话未在运行，无法输入';
    // CG-03 M3-001：capability 缺失（absent/unknown inputAckMode）= 只读，提示升级桌面端。
    if (!hasInputAckCapability.value) return '当前服务端不支持输入确认，请升级桌面端后重试';
    return null;
  });

  /** 发送草稿（防连点：sending 期间重入直接丢弃；draft 确认发送后才清空/上屏）。 */
  function sendDraft(): boolean {
    const text = draft.value.trim();
    // CG-03 M3-001：所有 product input 统一经 canonical outbox（CSPRNG MessageID +
    // session ledger + ACK + 幂等）。canWrite 已保证 capability 激活；不再 legacy 直发。
    if (sending.value || text.length === 0 || !canWrite.value || !client || !outbox) return false;
    sending.value = true;
    try {
      const r = outbox.accept(encodeUtf8ToBase64(`${text}\r`));
      if (r.accepted) {
        const ue: UserEntry = {
          entryId: `user-${nextOrder()}`,
          sortKey: latestSeenSeq.value + 0.25 + entries.value.length * 1e-6,
          order: nextOrder(),
          type: 'user',
          text,
          delivery: 'sending',
          attemptNo: 0,
        };
        insertEntry(ue);
        // 关联镜像（队尾最新 pending 镜像即本条；accept 同步触发首次 attempt）。
        const m = outboxMirror.value[outboxMirror.value.length - 1];
        if (m && m.userEntryId === null) {
          m.userEntryId = ue.entryId;
          ue.attemptNo = m.attemptNo;
        }
        if (commandHistory.value[0] !== text) commandHistory.value.unshift(text);
        if (commandHistory.value.length > 50) commandHistory.value.length = 50;
        draft.value = '';
        return true;
      }
      return false;
    } finally {
      sending.value = false;
    }
  }

  /** 内容转化应答（PromptAction/OptionCard）：input 为完整载荷（契约 input 帧原样承载）。
   * CG-03 M3-001：结构化应答同样经 canonical outbox（CSPRNG MessageID + ledger +
   * ACK + 幂等），不再 legacy 直发。canWrite 已保证 capability 激活。 */
  function sendAnswer(input: string): boolean {
    if (!canWrite.value || !outbox || input.length === 0) return false;
    return outbox.accept(encodeUtf8ToBase64(input)).accepted;
  }

  /** 历史指令复用：回填草稿（不直接发送）。 */
  function reuseCommand(text: string): void {
    draft.value = text;
  }

  /** 停止运行（显式按钮；控制者；协议 confirm 在 api 层固定）。 */
  async function stopRunning(): Promise<boolean> {
    if (stopping.value || control.value.state !== 'you') return false;
    stopping.value = true;
    try {
      const d = await stopSession(sessionId.value);
      detail.value = d;
      sessionState.value = d.state;
      control.value = d.control;
      return true;
    } catch (rawErr) {
      const err = toApiRequestError(rawErr);
      if (err.code === ERROR_CODE_AUTH_REVOKED || err.code === ERROR_CODE_AUTH_UNPAIRED) {
        authLost.value = 'revoked';
      } else {
        lastError.value = { code: err.code, message: err.message, actionHint: err.actionHint };
      }
      return false;
    } finally {
      stopping.value = false;
    }
  }

  /** 获取/释放控制权（REST；冲突如实呈现，不静默覆盖）。 */
  async function acquire(): Promise<boolean> {
    try {
      control.value = await acquireControl(sessionId.value);
      controlNotice.value = null;
      return true;
    } catch (rawErr) {
      const err = toApiRequestError(rawErr);
      // E-06 conflict（design §7）：acquire 409 在同一区域标 conflict，不改权威 state。
      if (err.status === 409) {
        controlNotice.value = {
          kind: 'conflict',
          controlState: control.value.state,
          deviceName: null,
          text: '控制权冲突：另一设备刚取得控制权，请稍后重试',
        };
      } else {
        lastError.value = { code: err.code, message: err.message, actionHint: err.actionHint };
      }
      return false;
    }
  }

  async function release(): Promise<boolean> {
    // R4-001：REST 发起前登记 intent generation——server 在 release 事务内同步把
    // control.state(reason=released) 入 subscriber FIFO，WS 事件可先于 HTTP response
    // 到达；响应后才置标记的旧实现会把主动释放误显为 E-06「控制权已收回」。
    const generation = ++localReleaseSeq;
    pendingLocalReleaseGen = generation;
    try {
      control.value = await releaseControl(sessionId.value);
      // intent 保持 armed 直至 correlated control.state(none, released) 事件消费
      // （WS 事件丢失时残留 intent 无害：后续 reason=released+prevControl=you 只能
      // 来自本设备自己的 release，语义仍正确）。
      // R3-001：本地 release = 显式放弃控制权（you→none），冻结旧 pending（design §7
      // E-06：失权即停发；intent 只抑制 E-06 notice，不绕过安全冻结）。
      // control.value 已急切置 none，correlated 事件到达时 prevControl 已非 you，
      // 故在此显式冻结（freezePending 幂等，事件若再次触发亦无害）。
      freezeOutboxMirror();
      return true;
    } catch (rawErr) {
      // REST 失败：撤销本次 intent（本代仍 pending 才撤，避免误撤销更新一代）。
      if (pendingLocalReleaseGen === generation) {
        pendingLocalReleaseGen = 0;
      }
      const err = toApiRequestError(rawErr);
      lastError.value = { code: err.code, message: err.message, actionHint: err.actionHint };
      return false;
    }
  }

  /** GapMarker 补齐：发送 backfill；结果原位替换（见 backfill.result 分支）。 */
  function requestGapFill(gapEntryId: string): void {
    const gapEntry = entries.value.find((e) => e.entryId === gapEntryId);
    if (!gapEntry || gapEntry.type !== 'gap' || gapEntry.filling || gapEntry.exhausted || !client) return;
    const requestId = client.requestBackfill(gapEntry.fromSeq, gapEntry.toSeq);
    if (requestId !== null) {
      pendingBackfills.set(requestId, gapEntry.entryId);
      gapEntry.filling = true;
    }
  }

  function sendResize(cols: number, rows: number): void {
    client?.sendResize(cols, rows);
  }

  // -------------------------------------------------------------------------
  // PG-04 诊断视图订阅（M2-D）：回放快照 + 直播续写；返回退订函数。
  // -------------------------------------------------------------------------

  function getRawTranscript(): string {
    return rawTranscript.text();
  }

  function subscribeRawOutput(cb: (text: string) => void): () => void {
    rawSubscribers.add(cb);
    return () => {
      rawSubscribers.delete(cb);
    };
  }

  function dismissError(): void {
    lastError.value = null;
  }

  function dismissDegraded(): void {
    degradedNotice.value = null;
  }

  // -------------------------------------------------------------------------
  // 渲染投影
  // -------------------------------------------------------------------------

  /** 时间线条目 → 渲染项（sealed 段用缓存，活跃段实时转化）。 */
  const timelineItems = computed<TimelineItem[]>(() => {
    const out: TimelineItem[] = [];
    for (const e of entries.value) {
      if (e.type === 'segment') {
        const blocks =
          e.blocksCache ??
          transformOutputLines([...e.lines, ...(e.partial ? [e.partial] : [])], {
            idPrefix: e.entryId,
            appType: appType.value,
          });
        out.push(...blocks);
      } else if (e.type === 'boundary') {
        const item: BoundaryItem = { id: e.entryId, kind: 'boundary', seq: e.seq, state: e.state, occurredAt: e.occurredAt };
        out.push(item);
      } else if (e.type === 'gap') {
        const item: GapItem = {
          id: e.entryId,
          kind: 'gap',
          fromSeq: e.fromSeq,
          toSeq: e.toSeq,
          fillable: !e.exhausted && wsState.value === 'attached',
          exhausted: e.exhausted,
        };
        out.push(item);
      } else {
        const item: UserItem = { id: e.entryId, kind: 'user', text: e.text, delivery: e.delivery, attemptNo: e.attemptNo };
        out.push(item);
      }
    }
    return out;
  });

  /** 五层状态条投影（复用 M2-B StatusBar 形状）。 */
  const statusLayers = computed<StatusLayer[]>(() => {
    const conn: StatusLayer =
      wsState.value === 'attached'
        ? { key: 'connection', label: '连接', text: '已连接', tone: 'ok' }
        : wsState.value === 'reconnecting'
          ? {
              key: 'connection',
              label: '连接',
              text: attachedOnce.value ? `恢复中（第 ${reconnectAttempt.value} 次重连）` : '连接中断，重连中',
              tone: 'warning',
              detail: reconnectDelayMs.value !== null ? `${Math.round(reconnectDelayMs.value / 100) / 10}s 后重试（≤5s 自动恢复）` : undefined,
            }
          : wsState.value === 'closed'
            ? { key: 'connection', label: '连接', text: terminalReason.value ?? '已断开', tone: 'danger' }
            : { key: 'connection', label: '连接', text: '连接中…', tone: 'neutral' };
    const auth: StatusLayer = authLost.value
      ? { key: 'auth', label: '授权', text: '已失效', tone: 'danger', detail: '请重新配对恢复访问' }
      : attachedOnce.value
        ? { key: 'auth', label: '授权', text: '已授权', tone: 'ok' }
        : { key: 'auth', label: '授权', text: '验证中…', tone: 'neutral' };
    const sessionText: Record<SessionState, string> = {
      running: '运行中',
      stopped: '已停止',
      exited: '已退出',
      unavailable: '不可用',
      removed: '已移除',
    };
    const sess: StatusLayer = {
      key: 'session',
      label: '会话',
      text: sessionText[sessionState.value],
      tone: sessionState.value === 'running' ? 'ok' : sessionState.value === 'unavailable' || sessionState.value === 'removed' ? 'danger' : 'neutral',
    };
    const ctrl: StatusLayer =
      control.value.state === 'you'
        ? { key: 'control', label: '控制', text: '你正在控制', tone: 'ok' }
        : control.value.state === 'desktop'
          ? { key: 'control', label: '控制', text: '桌面端控制中', tone: 'warning', detail: '你可观察，无法输入' }
          : control.value.state === 'other'
            ? { key: 'control', label: '控制', text: `由 ${control.value.deviceName} 控制`, tone: 'warning', detail: '你可观察，无法输入' }
            : { key: 'control', label: '控制', text: '无人控制', tone: 'neutral', detail: '获取控制权后可输入' };
    // E-08（design §7）：历史层 warning 覆盖一切可见缺口（含 live reorder 原位标记，
    // 不仅是 attached/backfill 裁定的 historyState=gap）。detail 按是否仍有可补齐
    // 缺口区分文案：仅 exhausted 时不再暗示可补齐（诚实能力边界）。
    const hasVisibleGap = entries.value.some((e) => e.type === 'gap');
    const hasFillableGap = entries.value.some((e) => e.type === 'gap' && !e.exhausted);
    const hist: StatusLayer =
      historyState.value === null
        ? { key: 'history', label: '历史', text: '附着后可见', tone: 'neutral' }
        : historyState.value === 'gap' || hasVisibleGap
          ? {
              key: 'history',
              label: '历史',
              text: '存在缺口',
              tone: 'warning',
              detail: hasFillableGap ? '缺口处以标记呈现，可尝试补齐' : '不可恢复的缺口以标记原位保留，已从最新继续',
            }
          : { key: 'history', label: '历史', text: historyState.value === 'backfilled' ? '已补齐' : '连续', tone: 'ok' };
    return [conn, auth, sess, ctrl, hist];
  });

  const fillingGapIds = computed(() => {
    const set = new Set<string>();
    for (const e of entries.value) {
      if (e.type === 'gap' && e.filling) set.add(e.entryId);
    }
    return set;
  });

  // 向后兼容别名：UI（WorkspacePage output-version 信号）仍读 latestSeq。
  const latestSeq = computed(() => latestSeenSeq.value);

  /** NoticeStack 优先级（design §7）：P0 fatal > P1 lastError > P2 E-07 > P3 degraded。 */
  const primaryNotice = computed<PrimaryNoticeLevel>(() => {
    if (authLost.value !== null || sessionState.value === 'removed' || loadError.value !== null || wsState.value === 'closed') {
      return 'fatal';
    }
    if (lastError.value !== null) return 'error';
    if (recoveryEpisode.value !== null) return 'recovery';
    if (degradedNotice.value !== null) return 'degraded';
    return null;
  });

  /** outbox 可视投影（M3-C）：发送中/重试次数/停发态 + 恢复后自动重发反馈。 */
  const outboxView = computed(() => {
    const pending = outboxMirror.value.filter((m) => m.state === 'sending');
    const halted = outboxMirror.value.filter((m) => m.state === 'halted');
    return {
      pendingCount: pending.length,
      haltedCount: halted.length,
      maxAttemptNo: pending.reduce((acc, m) => Math.max(acc, m.attemptNo), 0),
      /** 恢复后仍有未确认项：正在自动重发反馈。 */
      resending: recoveryEpisode.value?.state === 'restored' && pending.length > 0,
      /** 队首已达重试上限仍未 ACK：terminal 未确认态（design §5：不伪造 confirmed）。 */
      exhaustedUnconfirmed: pending.some((m) => m.attemptNo >= INPUT_OUTBOX_MAX_ATTEMPTS),
    };
  });

  /** 首个可见缺口 entryId（E-07 restored-with-gap「跳到缺口」目标）。 */
  const firstGapEntryId = computed(() => entries.value.find((e) => e.type === 'gap')?.entryId ?? null);

  /** 显式消退 restored 恢复条（用户手动关闭；timer 3s 自动消退之外的手动路径）。 */
  function dismissRecovery(): void {
    if (restoredTimer) {
      clearTimeout(restoredTimer);
      restoredTimer = null;
    }
    recoveryEpisode.value = null;
  }

  /**
   * continuitySnapshot（design §8 [R2/M-05] 机器 oracle 内存 seam）：
   * 只给 frontier / appliedSeq 多重集计数（≤4096）/ gapRanges / outbox 与 mark 计数；
   * 无 ID / payload / URL / 时间戳。Vitest/Playwright 动态读取。
   */
  function continuitySnapshot() {
    const seqCounts: Record<string, number> = {};
    for (const [seq, count] of appliedSeqCounts) seqCounts[String(seq)] = count;
    const gaps = entries.value
      .filter((e): e is GapEntry => e.type === 'gap')
      .map((g) => ({
        fromSeq: g.fromSeq,
        toSeq: g.toSeq,
        state: (g.exhausted ? 'exhausted' : g.filling ? 'filling' : 'recoverable') as 'exhausted' | 'filling' | 'recoverable',
      }));
    const snap = timing.snapshot();
    return {
      frontier: settledFrontier.value,
      latestSeenSeq: latestSeenSeq.value,
      attachedCount,
      recoveryGeneration,
      appliedSeqCounts: seqCounts,
      appliedSeqTruncated,
      gapRanges: gaps,
      outbox: {
        pending: outboxMirror.value.filter((m) => m.state === 'sending').length,
        halted: outboxMirror.value.filter((m) => m.state === 'halted').length,
        settled: outboxSettledCount,
      },
      recoveryEpisode: recoveryEpisode.value ? { state: recoveryEpisode.value.state, generation: recoveryEpisode.value.generation } : null,
      timing: {
        R0_R1: { status: snap.measurements.R0_R1.status, durationMs: snap.measurements.R0_R1.durationMs, budgetStatus: snap.measurements.R0_R1.budgetStatus },
      },
    };
  }

  return {
    // 投影
    sessionId,
    detail,
    loading,
    loadError,
    authLost,
    wsState,
    reconnectAttempt,
    reconnectDelayMs,
    terminalReason,
    attachedOnce,
    sessionState,
    control,
    historyState,
    controlNotice,
    lastError,
    degradedNotice,
    timelineItems,
    statusLayers,
    latestSeq,
    settledFrontier,
    hasCompletedAttach,
    recoveryEpisode,
    primaryNotice,
    outboxView,
    firstGapEntryId,
    dismissRecovery,
    continuitySnapshot,
    // M3-B timing snapshot（design §6：固定 schema、无 payload/ID；Vitest/Playwright 读）。
    timingSnapshot: () => timing.snapshot(),
    timingMark: (m: 'T0' | 'T1' | 'R0' | 'R1') => timing.mark(m),
    // composer
    draft,
    sending,
    commandHistory,
    stopping,
    canWrite,
    writeBlockReason,
    // 行为
    open,
    close,
    sendDraft,
    sendAnswer,
    reuseCommand,
    stopRunning,
    acquire,
    release,
    requestGapFill,
    fillingGapIds,
    sendResize,
    getRawTranscript,
    subscribeRawOutput,
    dismissError,
    dismissDegraded,
  };
});
