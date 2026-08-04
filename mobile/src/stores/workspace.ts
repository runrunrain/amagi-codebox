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

import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import {
  ERROR_CODE_AUTH_REVOKED,
  ERROR_CODE_AUTH_UNPAIRED,
  ERROR_CODE_AUTH_WINDOW_EXPIRED,
  type ControlSnapshot,
  type DecodedServerEvent,
  type GapRange,
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
  type WsClientState,
} from '../lib/ws';
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
}

interface UserEntry {
  entryId: string;
  sortKey: number;
  order: number;
  type: 'user';
  text: string;
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
  /** 控制被收回/变化的原因提示（control.state 事件 reason）。 */
  const controlNotice = ref<string | null>(null);
  /** 服务端 error 事件（契约错误形状原样呈现）。 */
  const lastError = ref<{ code: string; message: string; actionHint: string } | null>(null);
  /** unknown 事件导致的保守降级提示（fail-closed 呈现，不伪造细节）。 */
  const degradedNotice = ref<string | null>(null);

  // --- 时间线 ---
  const entries = ref<TimelineEntry[]>([]);
  const latestSeq = ref(0);

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
  const pendingBackfills = new Map<string, string>(); // requestId → gap entryId

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
    if (seq > latestSeq.value) latestSeq.value = seq;
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
    if (frame.seq > latestSeq.value) latestSeq.value = frame.seq;
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

  function addGapEntry(gap: GapRange): void {
    // 去重：同区间缺口不重复插入。
    const dup = entries.value.some((e) => e.type === 'gap' && e.fromSeq === gap.fromSeq && e.toSeq === gap.toSeq);
    if (dup) return;
    insertEntry({
      entryId: `gap-${gap.fromSeq}-${gap.toSeq}`,
      sortKey: gap.fromSeq - 0.5,
      order: nextOrder(),
      type: 'gap',
      fromSeq: gap.fromSeq,
      toSeq: gap.toSeq,
      exhausted: false,
      filling: false,
    });
  }

  function applyReplayFrames(frames: ReplayFrame[]): void {
    for (const frame of frames) {
      if (frame.type === 'output') {
        appendOutputText(frame.seq, decodeChunkToText(frame.chunk));
      } else if (frame.type === 'session.state' && frame.restartBoundary === true) {
        sealAtBoundary(frame);
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
        if (frame.seq > latestSeq.value) latestSeq.value = frame.seq;
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
        attachedOnce.value = true;
        sessionState.value = event.snapshot.session.state;
        control.value = event.snapshot.control;
        historyState.value = event.snapshot.history.state;
        if (event.snapshot.history.state === 'gap' && event.snapshot.history.gap) {
          addGapEntry(event.snapshot.history.gap);
        }
        applyReplayFrames(event.history);
        if (event.latestSeq > latestSeq.value) latestSeq.value = event.latestSeq;
        lastError.value = null;
        break;
      }
      case 'output': {
        appendOutputText(event.seq, decodeChunkToText(event.chunk));
        break;
      }
      case 'backfill.result': {
        const gapEntryId = pendingBackfills.get(event.requestId);
        pendingBackfills.delete(event.requestId);
        const gapEntry = entries.value.find((e) => e.entryId === gapEntryId);
        if ('frames' in event && event.frames) {
          // 补齐：原位替换缺口标记为转化后的内容块。
          if (gapEntry) {
            const idx = entries.value.indexOf(gapEntry);
            if (idx >= 0) entries.value.splice(idx, 1);
          }
          applyBackfillFrames(event.frames);
          historyState.value = 'backfilled';
        } else if (event.gap) {
          // gap 变体：服务端确认该段不可补齐（诚实呈现，不伪造）。
          if (gapEntry && gapEntry.type === 'gap') {
            gapEntry.exhausted = true;
            gapEntry.filling = false;
          }
        }
        break;
      }
      case 'session.state': {
        sessionState.value = event.state;
        if (event.restartBoundary === true) {
          sealAtBoundary(event);
        }
        break;
      }
      case 'control.state': {
        control.value =
          event.state === 'other'
            ? { state: 'other', deviceName: event.deviceName }
            : { state: event.state };
        controlNotice.value = event.state === 'you' ? null : `控制权已变化：${event.reason}`;
        break;
      }
      case 'auth.revoked': {
        authLost.value = 'revoked';
        break;
      }
      case 'error': {
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
    latestSeq.value = 0;
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
      latestSeq.value = d.latestSeq;
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
      getLastSeq: () => (latestSeq.value > 0 ? latestSeq.value : undefined),
      onEvent: handleWsEvent,
      onStateChange: (change) => {
        wsState.value = change.state;
        reconnectAttempt.value = change.attempt;
        reconnectDelayMs.value = change.nextDelayMs;
        if (change.terminalReason !== null) terminalReason.value = change.terminalReason;
      },
    });
    client.connect();
  }

  function close(): void {
    client?.dispose();
    client = null;
    pendingBackfills.clear();
  }

  // -------------------------------------------------------------------------
  // 写操作（全部经控制权过滤）
  // -------------------------------------------------------------------------

  const canWrite = computed(
    () => control.value.state === 'you' && wsState.value === 'attached' && sessionState.value === 'running',
  );

  /** 观察者/不可写原因（Composer 禁用提示）。 */
  const writeBlockReason = computed<string | null>(() => {
    if (control.value.state === 'desktop') return '桌面端正在控制，你可观察但无法输入';
    if (control.value.state === 'other') return `控制权在 ${(control.value as { deviceName: string }).deviceName}，你可观察但无法输入`;
    if (control.value.state === 'none') return '需要先获取控制权才能输入';
    if (wsState.value !== 'attached') return '连接未就绪，恢复后才能输入';
    if (sessionState.value !== 'running') return '会话未在运行，无法输入';
    return null;
  });

  /** 发送草稿（防连点：sending 期间重入直接丢弃；draft 确认发送后才清空/上屏）。 */
  function sendDraft(): boolean {
    const text = draft.value.trim();
    if (sending.value || text.length === 0 || !canWrite.value || !client) return false;
    sending.value = true;
    try {
      const ok = client.sendInput(`${text}\r`);
      if (ok) {
        insertEntry({
          entryId: `user-${nextOrder()}`,
          sortKey: latestSeq.value + 0.25 + entries.value.length * 1e-6,
          order: nextOrder(),
          type: 'user',
          text,
        });
        if (commandHistory.value[0] !== text) commandHistory.value.unshift(text);
        if (commandHistory.value.length > 50) commandHistory.value.length = 50;
        draft.value = '';
      }
      return ok;
    } finally {
      sending.value = false;
    }
  }

  /** 内容转化应答（PromptAction/OptionCard）：input 为完整载荷（契约 input 帧原样承载）。 */
  function sendAnswer(input: string): boolean {
    if (!canWrite.value || !client || input.length === 0) return false;
    return client.sendInput(input);
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
      lastError.value = { code: err.code, message: err.message, actionHint: err.actionHint };
      return false;
    }
  }

  async function release(): Promise<boolean> {
    try {
      control.value = await releaseControl(sessionId.value);
      return true;
    } catch (rawErr) {
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
        const item: UserItem = { id: e.entryId, kind: 'user', text: e.text };
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
    const hist: StatusLayer =
      historyState.value === null
        ? { key: 'history', label: '历史', text: '附着后可见', tone: 'neutral' }
        : historyState.value === 'gap'
          ? { key: 'history', label: '历史', text: '存在缺口', tone: 'warning', detail: '缺口处以标记呈现，可尝试补齐' }
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
