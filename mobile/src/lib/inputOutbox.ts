/**
 * mobile/src/lib/inputOutbox.ts — CG-03 跨连接输入幂等 outbox（client）
 * ---------------------------------------------------------------------------
 * 设计：contract-addendum-cg03.md §4/§5 + design §5（R3/R2-M02-R1）。
 *
 * 每 session 一个 FIFO outbox：32 entries / 256KiB、single-flight（仅队首有 wire
 * attempt）、每项 ≤8 attempts/30s。每次 logical input 在 accept 前取一次 canonical
 * MessageID（msg-v1- + 32 hex，crypto.getRandomValues 128-bit）并绑定 immutable base64
 * payload；retry 只换 requestId（req-v1- + 32 hex）。all-attempt requestId 集合 ≤8、
 * entry 销毁前不淘汰；ACK 须同时匹配 entry MessageID 且 requestId 属于该 entry 的
 * all-attempt 集合（精确关联到 session+attempt，CG-03 M3-003）；halted（冻结）entry 仍
 * 接受合法迟到 ACK（服务端确已 commit）；issued MessageID 仅在 document/session
 * 销毁（dispose）时清空，settlement 不回收（防同页熵源复用旧 ID）。
 *
 * 边界：无 CSPRNG / 本页 issuedIDs 重复 / 超 8192 issued → zero-wire fail-closed（草稿
 * 保留）；32/256KiB 超 → accepted=false（reason outbox.full），旧项不变；8 attempts 或
 * 30s 到上限只停止 retry，entry 仍接受迟到 ACK；control.forbidden/takeover 等 → halt
 * 冻结整条 flush。route/document 离开 → dispose 销毁，不自动重发。
 *
 * 本模块是纯状态机（注入 clock/CSPRNG/send/setTimeout），由 workspace continuity
 * state 持有而非 socket actor，故 socket replacement 不重置。
 */

import type { InputFrame, MessageID, RequestID } from './contract';
import { CLIENT_FRAME_TYPE_INPUT } from './contract';

export const INPUT_OUTBOX_MAX_ENTRIES = 32;
export const INPUT_OUTBOX_MAX_BYTES = 256 * 1024;
export const INPUT_OUTBOX_MAX_ATTEMPTS = 8;
export const INPUT_OUTBOX_WINDOW_MS = 30_000;
/** 每项最多保留的 requestId 数（与 attempts 上限对齐，entry 销毁前不淘汰）。 */
export const INPUT_OUTBOX_MAX_ATTEMPT_IDS = 8;
/** 单条 requestId canonical reserve（design R3：8×39-byte 预留）。 */
export const CANONICAL_REQUEST_ID_BYTES = 39;
/** 单条 MessageID canonical 字节数。 */
export const CANONICAL_MESSAGE_ID_BYTES = 39;
/** issued set 上限（本页去重 + fail-closed）。 */
export const INPUT_ISSUED_MAX = 8192;

/** attempt 间隔（秒）：1,2,4,5,5,5,5,5（design §5）。 */
const ATTEMPT_DELAYS_SEC = [1, 2, 4, 5, 5, 5, 5, 5];

/** 单帧 base64 data 上限（留余量给 JSON 开销；WS 帧硬上限 64KiB）。 */
const INPUT_FRAME_MAX_BYTES = 60_000;

export type InputOutboxState = 'pending' | 'settled' | 'halted' | 'unknown';
export type InputRejectReason = 'outbox.full' | 'secure_id_unavailable' | 'input.too_large';

export interface InputOutboxEntry {
  readonly id: MessageID;
  readonly data: string;
  readonly allAttemptIds: RequestID[];
  attemptNo: number;
  firstAttemptAt: number | null;
  state: InputOutboxState;
  readonly chargedBytes: number;
}

export interface InputOutboxDeps {
  /** CSPRNG 字节源（crypto.getRandomValues）。 */
  randomBytes: (n: number) => Uint8Array;
  /** 单调时钟（ms），用于 30s 窗口。 */
  now: () => number;
  /** 定时器句柄类型（可注入 fake）。 */
  setTimeout: (fn: () => void, ms: number) => unknown;
  clearTimeout: (handle: unknown) => void;
  /** wire 发送：构造好的 InputFrame；返回是否已发送。 */
  send: (frame: InputFrame) => boolean;
}

export interface AcceptResult {
  accepted: boolean;
  reason?: InputRejectReason;
}

/** 生成 canonical MessageID：msg-v1- + 32 lowercase hex（39 ASCII bytes）。 */
export function generateCanonicalMessageId(randomBytes: (n: number) => Uint8Array): MessageID | null {
  const bytes = safeRandom(randomBytes, 16);
  if (bytes === null) return null;
  return 'msg-v1-' + bytesToHex(bytes);
}

/** 生成 canonical RequestID：req-v1- + 32 lowercase hex（39 ASCII bytes）。 */
export function generateCanonicalRequestId(randomBytes: (n: number) => Uint8Array): RequestID | null {
  const bytes = safeRandom(randomBytes, 16);
  if (bytes === null) return null;
  return 'req-v1-' + bytesToHex(bytes);
}

function safeRandom(randomBytes: (n: number) => Uint8Array, n: number): Uint8Array | null {
  try {
    const b = randomBytes(n);
    if (b.length !== n) return null;
    return b;
  } catch {
    return null;
  }
}

function bytesToHex(bytes: Uint8Array): string {
  const hex = '0123456789abcdef';
  let out = '';
  for (let i = 0; i < bytes.length; i++) {
    out += hex[bytes[i] >> 4] + hex[bytes[i] & 0x0f];
  }
  return out;
}

/**
 * InputOutbox — per-session FIFO single-flight canonical input outbox。
 * 由 workspace continuity state 持有；socket replacement 不重置。
 */
export class InputOutbox {
  private entries: InputOutboxEntry[] = [];
  private chargedBytes = 0;
  private timer: unknown = null;
  private halted = false;
  private readonly issued = new Set<MessageID>();
  private readonly deps: InputOutboxDeps;

  constructor(deps: InputOutboxDeps) {
    this.deps = deps;
  }

  /** 当前队首待发项数（pending）。 */
  get pendingCount(): number {
    let n = 0;
    for (const e of this.entries) if (e.state === 'pending') n++;
    return n;
  }

  /** 总 entry 数（含 settled/halted，未释放前）。 */
  get size(): number {
    return this.entries.length;
  }

  /**
   * 接受一个 logical input。CSPRNG 生成 canonical ID + 绑定 immutable data；检查
   * 32/256KiB/issued 上限；原子插入后 kick single-flight。失败 zero-wire（草稿保留）。
   */
  accept(data: string): AcceptResult {
    if (this.halted) return { accepted: false, reason: 'secure_id_unavailable' };
    // 单帧不得超过 WS 64KiB 上限（design：单frame以 JSON.stringify UTF-8实长先过既有上限）。
    if (data.length > INPUT_FRAME_MAX_BYTES) return { accepted: false, reason: 'input.too_large' };
    if (this.entries.length >= INPUT_OUTBOX_MAX_ENTRIES) return { accepted: false, reason: 'outbox.full' };
    const id = generateCanonicalMessageId(this.deps.randomBytes);
    if (id === null) return { accepted: false, reason: 'secure_id_unavailable' };
    if (this.issued.has(id) || this.issued.size >= INPUT_ISSUED_MAX) {
      return { accepted: false, reason: 'secure_id_unavailable' };
    }
    // chargedBytes = data ASCII bytes + MessageID + 8×39 requestId reserve（design R3）。
    const charged = data.length + CANONICAL_MESSAGE_ID_BYTES + INPUT_OUTBOX_MAX_ATTEMPT_IDS * CANONICAL_REQUEST_ID_BYTES;
    if (this.chargedBytes + charged > INPUT_OUTBOX_MAX_BYTES) {
      return { accepted: false, reason: 'outbox.full' };
    }
    this.issued.add(id);
    this.chargedBytes += charged;
    const entry: InputOutboxEntry = {
      id,
      data,
      allAttemptIds: [],
      attemptNo: 0,
      firstAttemptAt: null,
      state: 'pending',
      chargedBytes: charged,
    };
    this.entries.push(entry);
    this.tryStartHead();
    return { accepted: true };
  }

  /**
   * ACK 结算（CG-03 M3-003）：合法 ACK 须同时匹配 entry MessageID 且 requestId 属于
   * 该 entry 的 all-attempt 集合（精确关联到 session+attempt）。错配 ACK（foreign
   * requestId 或 foreign id）绝不结算。halted（冻结）entry 仍接受合法迟到 ACK ——
   * 服务端确已 commit，客户端应如实结算；仅 disposed/settled entry 免疫。返回是否命中。
   */
  onAck(id: MessageID, requestId: RequestID): boolean {
    for (let i = 0; i < this.entries.length; i++) {
      const e = this.entries[i];
      if (e.state !== 'pending' && e.state !== 'halted') continue;
      if (e.id === id && e.allAttemptIds.includes(requestId)) {
        e.state = 'settled';
        this.releaseEntry(i);
        this.clearTimer();
        this.tryStartHead();
        return true;
      }
    }
    return false;
  }

  /** reattach：取消待执行 timer，立即重试队首（仍计数 attempt）。 */
  onReattach(): void {
    this.clearTimer();
    if (!this.halted) this.retryHead();
  }

  /** 冻结整条 flush（control.forbidden/takeover/revoke/remove/terminal/indeterminate）。 */
  halt(): void {
    this.halted = true;
    this.clearTimer();
    for (const e of this.entries) {
      if (e.state === 'pending') e.state = 'halted';
    }
  }

  /**
   * M3-002：冻结当前 pending entries（清 timer、标记 halted、不自动重发），但不永久
   * 禁用 outbox（不置 this.halted）——允许 authority 恢复 / 新 run 后接受新 input。
   * 冻结 entry 仍接合法迟到 ACK。区别于 halt()：halt() 永久禁用（control 终态 /
   * revoke / terminal session）；freezePending 仅冻结已有 pending（reattach 权威丢失 /
   * restart 新 run：pending 不跨 holder/run 重发，但新 input 不被阻断）。
   */
  freezePending(): void {
    this.clearTimer();
    for (const e of this.entries) {
      if (e.state === 'pending') e.state = 'halted';
    }
  }

  /** route/document 离开：销毁全部 entry、释放 budget、停止 timer（不自动重发）。 */
  dispose(): void {
    this.clearTimer();
    this.entries = [];
    this.chargedBytes = 0;
    this.issued.clear();
    this.halted = false;
  }

  // -------------------------------------------------------------------------
  // 内部
  // -------------------------------------------------------------------------

  /** single-flight：accept/onAck 只启动「从未发过」的队首（attemptNo==0）。 */
  private tryStartHead(): void {
    if (this.halted) return;
    const head = this.entries.find((e) => e.state === 'pending');
    if (!head || head.attemptNo > 0) return; // 队首已在发或无队首
    this.fireAttempt(head);
  }

  /** timer 驱动的重试：为队首发下一轮 attempt（仍计数、受 8/30s 上限）。 */
  private retryHead(): void {
    if (this.halted) return;
    const head = this.entries.find((e) => e.state === 'pending');
    if (!head) return;
    if (head.attemptNo >= INPUT_OUTBOX_MAX_ATTEMPTS) return;
    if (head.firstAttemptAt !== null && this.deps.now() - head.firstAttemptAt >= INPUT_OUTBOX_WINDOW_MS) return;
    this.fireAttempt(head);
  }

  /** 发一轮 attempt：追加 canonical requestId（all8 不淘汰）、计数、发送、排 timer。 */
  private fireAttempt(entry: InputOutboxEntry): void {
    const rid = generateCanonicalRequestId(this.deps.randomBytes);
    if (rid === null) return; // 熵失败：停止本轮 retry，entry 留 pending 接受迟到 ACK
    entry.allAttemptIds.push(rid);
    entry.attemptNo += 1;
    if (entry.firstAttemptAt === null) entry.firstAttemptAt = this.deps.now();
    const frame: InputFrame = {
      type: CLIENT_FRAME_TYPE_INPUT,
      requestId: rid,
      id: entry.id,
      data: entry.data,
    };
    this.deps.send(frame);
    this.scheduleNext(entry);
  }

  /** 安排下次重试（delay schedule；reattach 可取消）。 */
  private scheduleNext(entry: InputOutboxEntry): void {
    this.clearTimer(); // 取消任何 stale timer 再排新的
    if (entry.attemptNo >= INPUT_OUTBOX_MAX_ATTEMPTS) return;
    if (this.deps.now() - (entry.firstAttemptAt as number) >= INPUT_OUTBOX_WINDOW_MS) return;
    const delayIdx = Math.min(entry.attemptNo - 1, ATTEMPT_DELAYS_SEC.length - 1);
    const delayMs = ATTEMPT_DELAYS_SEC[delayIdx] * 1000;
    this.timer = this.deps.setTimeout(() => {
      this.timer = null;
      this.retryHead();
    }, delayMs);
  }

  private releaseEntry(index: number): void {
    const e = this.entries[index];
    this.chargedBytes -= e.chargedBytes;
    // CG-03 M3-003: issued MessageIDs 在 settlement 时不回收 —— 它们存活到
    // document/session 销毁（dispose），以防同页熵源复用旧 ID（server 会把新
    // payload 当旧 committed key re-ACK）。outbox 容量（chargedBytes）随 entry
    // 离开释放；issued 容量由 INPUT_ISSUED_MAX fail-closed 独立约束。
    this.entries.splice(index, 1);
  }

  private clearTimer(): void {
    if (this.timer !== null) {
      this.deps.clearTimeout(this.timer);
      this.timer = null;
    }
  }
}
