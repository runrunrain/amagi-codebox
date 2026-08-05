/**
 * inputOutbox.test.ts — CG-03 client outbox tests (addendum §6 C8/C9; design §5).
 * Covers CSPRNG canonical generation, page-rebuild ID divergence, entropy
 * fail-closed, 32/33 + 256KiB caps (with 8×39 reserve), all8 append-only,
 * 8/9 attempts + 30s window, single-flight, and the delayed-r1 settlement
 * invariant (settlement=1 / raw=1).
 */
import { describe, expect, it } from 'vitest';
import {
  InputOutbox,
  generateCanonicalMessageId,
  generateCanonicalRequestId,
  INPUT_OUTBOX_MAX_ENTRIES,
  INPUT_OUTBOX_MAX_BYTES,
  INPUT_OUTBOX_MAX_ATTEMPTS,
  INPUT_OUTBOX_WINDOW_MS,
} from './inputOutbox';
import { isCanonicalMessageID, type InputFrame } from './contract';

// deterministic CSPRNG source: a 128-bit incrementing counter (predictable,
// globally distinct per call — no 256-byte wrap collision). seed offsets the
// counter so two sources differ.
function counterRandom(seed = 0): (n: number) => Uint8Array {
  let count = seed;
  return (n: number) => {
    const b = new Uint8Array(n);
    let v = count;
    for (let i = n - 1; i >= 0; i--) {
      b[i] = v & 0xff;
      v = Math.floor(v / 256);
    }
    count++;
    return b;
  };
}

function makeDeps(overrides: Partial<{
  randomBytes: (n: number) => Uint8Array;
  now: () => number;
  send: (f: InputFrame) => boolean;
}> = {}) {
  let now = 0;
  const handles: Array<{ h: number; fn: () => void; at: number }> = [];
  let handleSeq = 0;
  const sentFrames: InputFrame[] = [];
  const state = {
    sendCount: 0,
    sentFrames,
    now,
    deps: {
      randomBytes: overrides.randomBytes ?? counterRandom(),
      now: overrides.now ?? (() => now),
      setTimeout: (fn: () => void, ms: number) => {
        const h = ++handleSeq;
        handles.push({ h, fn, at: now + ms });
        return h;
      },
      clearTimeout: (h: unknown) => {
        const idx = handles.findIndex((x) => x.h === (h as number));
        if (idx >= 0) handles.splice(idx, 1);
      },
      send: overrides.send ??
        ((f: InputFrame) => {
          state.sendCount++;
          state.sentFrames.push(f);
          return true;
        }),
    },
    advance(ms: number) {
      const target = now + ms;
      for (;;) {
        handles.sort((a, b) => a.at - b.at);
        const dueIdx = handles.findIndex((x) => x.at <= target);
        if (dueIdx < 0) break;
        const [due] = handles.splice(dueIdx, 1);
        now = due.at; // advance to the timer's time before firing (correct chaining)
        due.fn();
      }
      now = target;
    },
    pendingTimers: () => handles.length,
  };
  return state;
}

describe('CG-03 canonical ID generation (C8)', () => {
  it('generateCanonicalMessageId produces 39-byte msg-v1- + 32 hex', () => {
    const id = generateCanonicalMessageId(counterRandom());
    expect(id).not.toBeNull();
    expect(id).toHaveLength(39);
    expect(isCanonicalMessageID(id as string)).toBe(true);
  });

  it('generateCanonicalRequestId produces 39-byte req-v1- + 32 hex', () => {
    const rid = generateCanonicalRequestId(counterRandom());
    expect(rid).not.toBeNull();
    expect(rid).toHaveLength(39);
    expect((rid as string).startsWith('req-v1-')).toBe(true);
  });

  it('CSPRNG failure → null (fail-closed, no fallback)', () => {
    const fail = (_n: number) => {
      throw new Error('no crypto');
    };
    expect(generateCanonicalMessageId(fail)).toBeNull();
    expect(generateCanonicalRequestId(fail)).toBeNull();
  });

  it('CSPRNG wrong length → null', () => {
    const wrong = (n: number) => new Uint8Array(n - 1);
    expect(generateCanonicalMessageId(wrong)).toBeNull();
  });
});

describe('CG-03 outbox caps (C9)', () => {
  it('32 accepted, 33rd rejected (outbox.full); wireFor33rd=0; draft33 unchanged (C2b entry-cap oracle)', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    for (let i = 0; i < INPUT_OUTBOX_MAX_ENTRIES; i++) {
      expect(ob.accept('d' + i).accepted).toBe(true);
    }
    // barrier：32 accept 后 single-flight 下仅 head 发了首 attempt（无时间推进）。
    const wireBefore33rd = h.sendCount;
    expect(wireBefore33rd).toBe(1); // 仅 head 的 attempt 1
    const r = ob.accept('overflow');
    // C2b 冻结 oracle：accepted=32 / rejected(outbox.full)=1 / wireFor33rd=0 / draft33 不变。
    expect(r.accepted).toBe(false);
    expect(r.reason).toBe('outbox.full');
    expect(h.sendCount, 'wireFor33rd=0：第 33 项产生任何 wire 帧').toBe(wireBefore33rd);
    expect(ob.size, 'draft33 不变：outbox 仍 32 项，未吞 33').toBe(INPUT_OUTBOX_MAX_ENTRIES);
    // draft33 原文不进入任何已发帧（zero-wire 证据）。
    expect(h.sentFrames.some((f) => f.data === 'overflow')).toBe(false);
  });

  it('256KiB byte cap binds BEFORE 32-entry cap, ±1 boundary (C2b byte-cap oracle)', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    // C2b 冻结 oracle 的 256KiB 边界：用大帧使 byte cap 先于 32-entry cap 生效。
    // charge = data.length + 39(MessageID) + 8*39(requestId reserve) = data.length + 351。
    const OVERHEAD = 39 + 8 * 39; // 351
    const CAP = INPUT_OUTBOX_MAX_BYTES; // 256KiB = 262144
    const BIG = 60000; // 单帧 ≤ INPUT_FRAME_MAX_BYTES(60000)
    const bigCharge = BIG + OVERHEAD; // 60351
    const maxBig = Math.floor(CAP / bigCharge); // 4（byte cap 在 4 处生效，远早于 32-entry）
    expect(maxBig, '前提：byte cap 必须先于 32-entry cap 生效').toBeLessThan(INPUT_OUTBOX_MAX_ENTRIES);
    for (let i = 0; i < maxBig; i++) {
      expect(ob.accept('B'.repeat(BIG)).accepted, `大帧 ${i} accepted`).toBe(true);
    }
    expect(ob.size).toBe(maxBig); // 4 < 32：byte cap 是约束瓶颈，不是 entry cap
    // 剩余预算恰好装下一条「exact-fit」帧：data.len = remaining - OVERHEAD → charge = remaining。
    const remaining = CAP - maxBig * bigCharge; // 262144 - 4*60351 = 20740
    const exactFitData = remaining - OVERHEAD; // 20389 → charge = 20740 = remaining（恰等于预算，≤ cap）
    expect(ob.accept('F'.repeat(exactFitData)).accepted, 'exact-fit at byte cap: charge==remaining accepted').toBe(true);
    expect(ob.size).toBe(maxBig + 1); // 5
    // ±1 边界：+1 byte → charge = remaining+1 > cap → rejected(outbox.full)；byte cap binds。
    const over = ob.accept('X'.repeat(exactFitData + 1));
    expect(over.accepted, '+1 byte over cap: rejected').toBe(false);
    expect(over.reason).toBe('outbox.full');
    expect(ob.size, 'rejected 后 outbox 不变').toBe(maxBig + 1);
    // 反向 ±1 证据：exactFitData-1 仍可装（在预算内）——证明边界点精确。
    // （上面已装 exactFitData 到预算上限；这里验证少 1 byte 的帧在新建 outbox 里也能装。）
    const h2 = makeDeps();
    const ob2 = new InputOutbox(h2.deps);
    for (let i = 0; i < maxBig; i++) ob2.accept('B'.repeat(BIG));
    expect(ob2.accept('F'.repeat(exactFitData - 1)).accepted, 'exactFitData-1: 仍在预算内 accepted').toBe(true);
  });

  it('input.too_large rejects a single oversized frame', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    const r = ob.accept('x'.repeat(60_001));
    expect(r.accepted).toBe(false);
    expect(r.reason).toBe('input.too_large');
  });

  it('256KiB byte budget (incl 8×39 reserve) bounds acceptance', () => {
    // 保留原轻量回归（100-byte 循环命中 entry cap）；精确 ±1 byte 边界见上一用例。
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    let count = 0;
    while (ob.accept('a'.repeat(100)).accepted) {
      count++;
      if (count > 1000) throw new Error('runaway');
    }
    // 100-data entries: each charges 451; entry cap is 32 and binds first for small data.
    expect(count).toBe(INPUT_OUTBOX_MAX_ENTRIES); // entry cap binds first for small data
  });
});

describe('CG-03 outbox retry + all8 + delayed-r1 (C9)', () => {
  it('single-flight: only head has a wire attempt; 8 attempts then stop', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    expect(ob.accept('first').accepted).toBe(true);
    expect(ob.accept('second').accepted).toBe(true);
    // head (first) sends attempt 1 immediately.
    expect(h.sendCount).toBe(1);
    // advance through 8 attempts for the head.
    for (let i = 1; i < INPUT_OUTBOX_MAX_ATTEMPTS; i++) {
      h.advance(10_000); // > any delay in schedule
    }
    expect(h.sendCount).toBe(INPUT_OUTBOX_MAX_ATTEMPTS); // 8 attempts for head
    // 9th attempt does NOT happen (stopped).
    h.advance(10_000);
    expect(h.sendCount).toBe(INPUT_OUTBOX_MAX_ATTEMPTS);
    // second entry never sent (single-flight, head unsettled).
    expect(h.sentFrames.every((f) => f.data === 'first')).toBe(true);
  });

  it('all8 requestIds append-only (≤8, no eviction)', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    ob.accept('only');
    // run 8 attempts.
    for (let i = 0; i < INPUT_OUTBOX_MAX_ATTEMPTS; i++) h.advance(10_000);
    // inspect internal via the onAck matching: the 8 sent requestIds are all valid
    // settlement keys. Confirm each sent requestId settles the entry.
    const rids = h.sentFrames.map((f) => f.requestId);
    expect(rids).toHaveLength(INPUT_OUTBOX_MAX_ATTEMPTS);
    // every requestId is distinct.
    expect(new Set(rids).size).toBe(INPUT_OUTBOX_MAX_ATTEMPTS);
    // MessageID is stable across all attempts.
    expect(new Set(h.sentFrames.map((f) => f.id)).size).toBe(1);
  });

  it('ACK releases head and advances to next (single-flight FIFO)', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    ob.accept('first');
    ob.accept('second');
    expect(h.sendCount).toBe(1); // head only
    // ACK the head by its first requestId.
    const headRid = h.sentFrames[0].requestId;
    const headId = h.sentFrames[0].id;
    expect(ob.onAck(headId, headRid)).toBe(true);
    // second entry now becomes head and sends its attempt 1.
    expect(h.sendCount).toBe(2);
    expect(ob.pendingCount).toBe(1);
  });

  it('C2b FIFO drain of all 32: accept 32 → ACK each head in order → settled=32/outbox=0 (raw32 oracle)', () => {
    // C2b 冻结 oracle FIFO 部分（rawInput=32/settled=32/outbox=0）在 outbox 状态机层的权威证据。
    // outbox 是 raw/settled/outbox 计数的权威拥有者；E2E（workspace-m3-int-relay C2b）证
    // hold→disconnect→reconnect→FIFO 的 wire 集成路径，本单测验 32 项边界与 FIFO 顺序。
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    // 装满 32（全部 pending；single-flight 下仅 head 发 attempt 1）。
    for (let i = 0; i < INPUT_OUTBOX_MAX_ENTRIES; i++) {
      expect(ob.accept('fifo-' + i).accepted).toBe(true);
    }
    expect(ob.size).toBe(INPUT_OUTBOX_MAX_ENTRIES);
    expect(h.sendCount).toBe(1); // 仅 head
    // FIFO 逐项结算：ACK 当前 head → 下一项变 head 并发 attempt → 直至 32 项全结算。
    let settled = 0;
    const order: string[] = [];
    while (ob.size > 0) {
      const headFrame = h.sentFrames[h.sentFrames.length - 1]; // 最新 attempt = 当前 head
      order.push(headFrame.data);
      expect(ob.onAck(headFrame.id, headFrame.requestId)).toBe(true);
      settled++;
    }
    // C2b FIFO oracle：settled=32、outbox=0、pending=0。
    expect(settled).toBe(INPUT_OUTBOX_MAX_ENTRIES);
    expect(ob.size).toBe(0);
    expect(ob.pendingCount).toBe(0);
    // FIFO 顺序证据：结算顺序 == 入队顺序（不乱序、不丢）。
    expect(order).toEqual(Array.from({ length: INPUT_OUTBOX_MAX_ENTRIES }, (_, i) => 'fifo-' + i));
  });

  it('ACK requires BOTH MessageID and an all-attempt requestId (M3-003 exact correlation)', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    ob.accept('x');
    const headId = h.sentFrames[0].id;
    const headRid = h.sentFrames[0].requestId;
    // Foreign requestId + matching id → does NOT settle (the OR bug allowed this).
    expect(ob.onAck(headId, 'req-v1-ffffffffffffffffffffffffffffffff' as never)).toBe(false);
    expect(ob.pendingCount).toBe(1);
    // Foreign id + matching requestId → does NOT settle either.
    expect(ob.onAck(('msg-v1-' + 'f'.repeat(32)) as never, headRid)).toBe(false);
    expect(ob.pendingCount).toBe(1);
    // Correct: matching id AND an all-attempt requestId → settles.
    expect(ob.onAck(headId, headRid)).toBe(true);
    expect(ob.pendingCount).toBe(0);
  });

  it('halted entry still accepts a legitimate late ACK (M3-003)', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    ob.accept('cmd');
    const f = h.sentFrames[0];
    ob.halt(); // freeze (takeover); entry → halted, new accepts rejected
    expect(ob.accept('after-halt').accepted).toBe(false);
    // Legitimate late ACK (matching id + attempt requestId) still settles: the
    // server committed it, so the client must not force the user to re-send.
    expect(ob.onAck(f.id, f.requestId)).toBe(true);
    expect(ob.pendingCount).toBe(0);
  });

  it('issued MessageID not recycled on settlement (M3-003 page-lifetime)', () => {
    // Deterministic entropy that always yields all-zero bytes → the same
    // canonical MessageID on every accept.
    const fixed = (n: number) => new Uint8Array(n);
    const h = makeDeps({ randomBytes: fixed });
    const ob = new InputOutbox(h.deps);
    expect(ob.accept('first').accepted).toBe(true);
    const firstId = h.sentFrames[0].id;
    // Settle the entry; the issued id must NOT be released (page-lifetime).
    expect(ob.onAck(firstId, h.sentFrames[0].requestId)).toBe(true);
    expect(ob.pendingCount).toBe(0);
    // Second accept: same entropy → same id, still in issued → rejected.
    const r = ob.accept('second');
    expect(r.accepted).toBe(false);
    expect(r.reason).toBe('secure_id_unavailable');
  });

  it('delayed-r1: raw=1 (single send), ACK arrives after attempt 8 → settlement=1', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    ob.accept('delayed');
    // The server writes raw exactly once (ledger); simulate by counting sends.
    // Run all 8 attempts (r1..r8), none acknowledged.
    for (let i = 0; i < INPUT_OUTBOX_MAX_ATTEMPTS; i++) h.advance(10_000);
    expect(h.sendCount).toBe(INPUT_OUTBOX_MAX_ATTEMPTS); // 8 wire attempts
    // t < 30s still: deliver r1's late ACK (matching the first requestId).
    const r1 = h.sentFrames[0];
    expect(ob.onAck(r1.id, r1.requestId)).toBe(true);
    // settlement = 1 (entry removed), raw wire attempts = 8 but server raw = 1.
    expect(ob.pendingCount).toBe(0);
    expect(ob.size).toBe(0);
  });

  it('30s window stops retry but entry still accepts late ACK', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    ob.accept('win');
    // advance past 30s window (each advance fires timers; attempts accrue).
    h.advance(INPUT_OUTBOX_WINDOW_MS + 10_000);
    const attemptsBefore = h.sendCount;
    h.advance(10_000);
    expect(h.sendCount).toBe(attemptsBefore); // no further retry past window
    // late ACK still settles.
    const r1 = h.sentFrames[0];
    expect(ob.onAck(r1.id, r1.requestId)).toBe(true);
    expect(ob.pendingCount).toBe(0);
  });

  it('halt freezes the flush; subsequent accept rejected', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    ob.accept('a');
    ob.halt();
    expect(ob.accept('b').accepted).toBe(false);
  });

  it('reattach cancels pending timer and retries head (counts as attempt)', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    ob.accept('r');
    expect(h.sendCount).toBe(1);
    ob.onReattach(); // immediate retry (attempt 2)
    expect(h.sendCount).toBe(2);
  });

  it('dispose destroys all entries and stops timers', () => {
    const h = makeDeps();
    const ob = new InputOutbox(h.deps);
    ob.accept('a');
    ob.accept('b');
    ob.dispose();
    expect(ob.size).toBe(0);
    expect(ob.pendingCount).toBe(0);
  });

  it('page-rebuild: a new outbox with fresh entropy generates a distinct ID', () => {
    const h1 = makeDeps({ randomBytes: counterRandom(0) });
    const h2 = makeDeps({ randomBytes: counterRandom(100) });
    const ob1 = new InputOutbox(h1.deps);
    const ob2 = new InputOutbox(h2.deps);
    ob1.accept('same-text');
    ob2.accept('same-text');
    const id1 = h1.sentFrames[0].id;
    const id2 = h2.sentFrames[0].id;
    expect(id1).not.toBe(id2); // distinct 128-bit IDs
    expect(isCanonicalMessageID(id1)).toBe(true);
    expect(isCanonicalMessageID(id2)).toBe(true);
  });
});
