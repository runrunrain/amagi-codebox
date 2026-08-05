/**
 * M0-03 wire producer/consumer parity + production-runtime test (TS side).
 *
 * Reads the SAME shared fixture as the Go test. Uses ONLY production APIs
 * (normalizeServerEvent, isClientFrame, manifests) — no test-local decoder,
 * no double cast, no path mirror. No WebSocket/fetch/store/mock.
 *
 * Addendum §3.5 (compile negatives) + §4 (runtime normalization) + §6 (parity).
 */
import { describe, expect, expectTypeOf, it } from 'vitest';
import type {
  AuthRevokedEvent,
  AuthRevokedReason,
  BackfillResultEvent,
  ClientFrame,
  ConfirmActionRequest,
  ControlSnapshot,
  ControlStateEvent,
  DecodedServerEvent,
  HistorySnapshot,
  InputAckEvent,
  KnownServerEvent,
  SessionStateEvent,
  UnknownServerEvent,
} from './index';
import {
  AUTH_REVOKED_CLOSE_CODE,
  AUTH_REVOKED_REASON_DEVICE_REVOKED,
  INPUT_ACK_MODE_SESSION_WINDOW_V1,
  isCanonicalMessageID,
  isCanonicalRequestID,
  isClientFrame,
  KNOWN_ACTION_HINTS,
  KNOWN_AUTH_REVOKED_REASONS,
  KNOWN_CLI_TYPES,
  KNOWN_CLIENT_FRAME_TYPES,
  KNOWN_CONTROL_STATES,
  KNOWN_ERROR_CODES,
  KNOWN_ERROR_LAYERS,
  KNOWN_HISTORY_STATES,
  KNOWN_SERVER_EVENT_TYPES,
  KNOWN_SESSION_STATES,
  normalizeServerEvent,
  SERVER_EVENT_TYPE_INPUT_ACK,
  V1_REST_ENDPOINTS,
} from './index';
import fixture from './testdata/v1-wire-fixtures.json';

const m = fixture.manifest;

// ---------------------------------------------------------------------------
// (1) Production manifest parity — full endpoint objects + every enum array.
// ---------------------------------------------------------------------------
describe('production manifests vs fixture', () => {
  it('endpoint manifest compares full method/path/status objects', () => {
    expect(V1_REST_ENDPOINTS).toHaveLength(10);
    // Full-object equality (not path-only).
    expect([...V1_REST_ENDPOINTS]).toEqual(m.restEndpoints);
  });

  it('enum tuples match fixture exactly', () => {
    expect([...KNOWN_CLIENT_FRAME_TYPES]).toEqual(m.clientFrameTypes);
    expect([...KNOWN_SERVER_EVENT_TYPES]).toEqual(m.serverEventTypes);
    expect([...KNOWN_ERROR_CODES]).toEqual(m.errorCodes);
    expect([...KNOWN_CLI_TYPES]).toEqual(m.cliTypes);
    expect([...KNOWN_SESSION_STATES]).toEqual(m.sessionStates);
    expect([...KNOWN_CONTROL_STATES]).toEqual(m.controlStates);
    expect([...KNOWN_HISTORY_STATES]).toEqual(m.historyStates);
    expect([...KNOWN_ERROR_LAYERS]).toEqual(m.errorLayers);
    expect([...KNOWN_ACTION_HINTS]).toEqual(m.actionHints);
  });

  it('CG-01 auth.revoked reason manifest + close code match fixture (C1)', () => {
    expect([...KNOWN_AUTH_REVOKED_REASONS]).toEqual(m.authRevokedReasons);
    expect(AUTH_REVOKED_REASON_DEVICE_REVOKED).toBe('device_revoked');
    expect(AUTH_REVOKED_CLOSE_CODE).toBe(m.authRevokedCloseCode);
    expect(AUTH_REVOKED_CLOSE_CODE).toBe(1008);
    expect(KNOWN_AUTH_REVOKED_REASONS).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// (2) Client frames: producer literals satisfy ClientFrame; isClientFrame guard.
// ---------------------------------------------------------------------------
describe('client frames', () => {
  it('five literals satisfy ClientFrame (producer)', () => {
    const attach = { type: 'attach', requestId: 'req_attach_1', apiVersion: 'v1', sessionId: 'sess_opaque_1' } satisfies ClientFrame;
    const input = { type: 'input', requestId: 'req_input_1', id: 'msg_1', data: '5L2g5aW9' } satisfies ClientFrame;
    const resize = { type: 'resize', requestId: 'req_resize_1', cols: 120, rows: 40 } satisfies ClientFrame;
    const backfill = { type: 'backfill', requestId: 'req_backfill_1', fromSeq: 41, toSeq: 50 } satisfies ClientFrame;
    const ping = { type: 'ping', requestId: 'req_ping_1' } satisfies ClientFrame;
    for (const f of [attach, input, resize, backfill, ping]) {
      expect(isClientFrame(f)).toBe(true);
    }
  });

  it('isClientFrame accepts the canonical input frame (CG-03 producer)', () => {
    expect(isClientFrame(fixture.clientFrames.inputCanonical)).toBe(true);
  });

  it('isClientFrame rejects invalid client frames (production guard)', () => {
    expect(isClientFrame(fixture.invalid.nullRequiredField)).toBe(false);
    expect(isClientFrame(fixture.invalid.missingRequiredField)).toBe(false);
    expect(isClientFrame(fixture.invalid.unknownClientFrame)).toBe(false);
    expect(isClientFrame(fixture.invalid.unknownClientField)).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// (3) Server events: production normalizeServerEvent consumer + sanitization.
// ---------------------------------------------------------------------------
describe('normalizeServerEvent (production runtime)', () => {
  const knownKeys = [
    'sessionAttachedEmptyHistory', 'sessionAttachedWithHistory', 'sessionAttachedGap',
    'sessionAttachedWithInputAckMode',
    'sessionAttachedUnknownInputAckMode',
    'output', 'backfillResultFrames', 'backfillResultGap',
    'sessionStateExited', 'sessionStateRestartBoundary',
    'controlStateOther', 'controlStateYou', 'controlStateNone', 'controlStateDesktop',
    'authRevoked', 'error',
    'inputAck', 'inputAckLegacyId',
  ] as const;

  it('normalizes every known fixture event to KnownServerEvent', () => {
    for (const key of knownKeys) {
      const ev = normalizeServerEvent(fixture.serverEvents[key]);
      expect(ev.type, `${key} should be known`).not.toBe('unknown');
    }
    // serverEvents consumed-set parity (CG-01 §5.1): the known normalize keys
    // plus the existing unknownEvent must cover every fixture serverEvents
    // root key — no orphan case, no untested key.
    const consumed = new Set<string>([...knownKeys, 'unknownEvent']);
    expect([...consumed].sort()).toEqual(Object.keys(fixture.serverEvents).sort());
    // unknownEvent normalizes to sanitized UnknownServerEvent (not a known type).
    const unk = normalizeServerEvent(fixture.serverEvents.unknownEvent) as UnknownServerEvent;
    expect(unk.type).toBe('unknown');
    expect(unk.reason).toBe('unknown-type');
  });

  it('attached-gap normalizes with the nested GapRange', () => {
    const ev = normalizeServerEvent(fixture.serverEvents.sessionAttachedGap) as Extract<DecodedServerEvent, { type: 'session.attached' }>;
    expect(ev.snapshot.history.state).toBe('gap');
    expect(ev.snapshot.history.gap?.code).toBe('history.gap');
  });

  it('unknown event normalizes to sanitized UnknownServerEvent without throwing', () => {
    const ev = normalizeServerEvent(fixture.serverEvents.unknownEvent) as UnknownServerEvent;
    expect(ev.type).toBe('unknown');
    expect(ev.reason).toBe('unknown-type');
    expect(ev.fallback).toBe('ignore');
    // Sanitization: NO raw field; secret values / unknown field names absent.
    expect('raw' in ev).toBe(false);
    const serialized = JSON.stringify(ev);
    expect(serialized).not.toContain('sess_secret_value_xyz');
    expect(serialized).not.toContain('cpu');
    // wireType retains the matched type only.
    expect(ev.wireType).toBe('session.metrics');
  });

  it('malformed known / null / XOR / unknown-enum / unsafe-seq all normalize to Unknown', () => {
    const serverInvalid = [
      'framesAndGapBothPresent', 'backfillNeitherFramesNorGap',
      'controlOtherMissingDeviceName', 'controlNoneWithDeviceName', 'controlYouWithDeviceName', 'controlDesktopWithDeviceName',
      'knownOutputNullChunk', 'knownSessionUnknownState',
      'knownAttachedUnknownAuthState', 'knownAttachedUnknownHistoryState', 'knownControlUnknownState',
      'historyGapMissingRange', 'historyGapNullRange', 'historyContinuousWithRange', 'historyBackfilledWithRange',
      'unsafeSeqAboveMax', 'unsafeSeqFractional', 'unsafeSeqNegative', 'replaySeqZero',
      'attachedGapRangeMismatch',
      'outputStructuredExpectedNull', 'errorRequestIdNull', 'errorDetailsNull',
      'sessionStateRestartFalse', 'sessionStateSeqAlone',
      'backfillFrameOutOfRange', 'backfillFrameNonAscending',
      'authRevokedUnknownReason', 'authRevokedNullReason',
      'inputAckMissingId', 'inputAckNullId',
    ] as const;
    for (const key of serverInvalid) {
      const ev = normalizeServerEvent(fixture.invalid[key]);
      expect(ev.type, `${key} must normalize to unknown`).toBe('unknown');
    }
  });

  it('M3-004: backfill frames must contiguously and exhaustively cover [fromSeq, toSeq] (partial → unknown)', () => {
    const mk = (fromSeq: number, toSeq: number, seqs: number[]) => ({
      type: 'backfill.result',
      requestId: 'r',
      sessionId: 's',
      fromSeq,
      toSeq,
      earliestSeq: 1,
      latestSeq: 9,
      frames: seqs.map((s) => ({ type: 'output', sessionId: 's', seq: s, chunk: 'YQ==' })),
    });
    // Valid contiguous full covers.
    for (const ev of [mk(2, 2, [2]), mk(2, 4, [2, 3, 4]), mk(7, 8, [7, 8])]) {
      const out = normalizeServerEvent(ev);
      expect(out.type, `valid cover ${ev.fromSeq}-${ev.toSeq} must normalize to backfill.result`).toBe('backfill.result');
    }
    // Partial covers (server must use the gap variant instead) → unknown (fail-closed).
    for (const [name, ev] of [
      ['missing first', mk(2, 3, [3])],
      ['missing last', mk(2, 3, [2])],
      ['gap in middle', mk(2, 4, [2, 4])],
      ['single subrange of multi-point', mk(2, 4, [3])],
    ] as const) {
      const out = normalizeServerEvent(ev);
      expect(out.type, `${name}: partial cover must normalize to unknown`).toBe('unknown');
    }
  });

  it('M3-004 (R3): backfill gap variant must cover the full requested range (partial gap → unknown)', () => {
    const mk = (fromSeq: number, toSeq: number, gapFrom: number, gapTo: number) => ({
      type: 'backfill.result',
      requestId: 'r',
      sessionId: 's',
      fromSeq,
      toSeq,
      earliestSeq: 1,
      latestSeq: 9,
      gap: { code: 'history.gap', fromSeq: gapFrom, toSeq: gapTo },
    });
    // Valid: gap covers the full requested range (whole-range gap; v1 has no partial).
    for (const ev of [mk(2, 2, 2, 2), mk(2, 4, 2, 4), mk(7, 8, 7, 8)]) {
      const out = normalizeServerEvent(ev);
      expect(out.type, `whole-range gap ${ev.fromSeq}-${ev.toSeq} must normalize to backfill.result`).toBe('backfill.result');
    }
    // Partial gap (inner != outer) must be rejected → unknown (fail-closed), so the
    // store cannot advance its frontier past positions never held nor adjudicated.
    for (const [name, ev] of [
      ['partial covers start only', mk(2, 4, 2, 2)],
      ['partial covers end only', mk(2, 4, 4, 4)],
      ['partial covers middle', mk(2, 6, 3, 4)],
      ['gap shifted below request', mk(2, 4, 1, 4)],
      ['gap shifted above request', mk(2, 4, 2, 5)],
    ] as const) {
      const out = normalizeServerEvent(ev);
      expect(out.type, `${name}: partial gap must normalize to unknown`).toBe('unknown');
    }
  });

  it('fallback maps enum failures to safe layers', () => {
    expect((normalizeServerEvent(fixture.invalid.knownAttachedUnknownAuthState) as UnknownServerEvent).fallback).toBe('force-unauthorized');
    expect((normalizeServerEvent(fixture.invalid.knownControlUnknownState) as UnknownServerEvent).fallback).toBe('force-read-only');
    expect((normalizeServerEvent(fixture.invalid.knownAttachedUnknownHistoryState) as UnknownServerEvent).fallback).toBe('mark-history-gap');
    expect((normalizeServerEvent(fixture.invalid.knownSessionUnknownState) as UnknownServerEvent).fallback).toBe('mark-session-unavailable');
    expect((normalizeServerEvent(fixture.invalid.unsafeSeqAboveMax) as UnknownServerEvent).fallback).toBe('mark-history-gap');
    // CG-01 fail-closed: unknown/null auth.revoked reason → force-unauthorized.
    expect((normalizeServerEvent(fixture.invalid.authRevokedUnknownReason) as UnknownServerEvent).fallback).toBe('force-unauthorized');
    expect((normalizeServerEvent(fixture.invalid.authRevokedNullReason) as UnknownServerEvent).fallback).toBe('force-unauthorized');
    // CG-03 fail-closed: malformed input.ack (missing/null id) → force-read-only.
    expect((normalizeServerEvent(fixture.invalid.inputAckMissingId) as UnknownServerEvent).fallback).toBe('force-read-only');
    expect((normalizeServerEvent(fixture.invalid.inputAckNullId) as UnknownServerEvent).fallback).toBe('force-read-only');
  });

  it('never throws on arbitrary input', () => {
    for (const v of [null, undefined, 42, 'x', [], {}, { type: 7 }, { type: 'nope' }]) {
      expect(() => normalizeServerEvent(v)).not.toThrow();
    }
  });
});

// ---------------------------------------------------------------------------
// (4) Compile-time conditional negatives (@ts-expect-error).
// ---------------------------------------------------------------------------
describe('conditional type compile negatives', () => {
  const gap = { code: 'history.gap' as const, fromSeq: 1, toSeq: 5 };
  const commonBackfill = { type: 'backfill.result' as const, requestId: 'r', sessionId: 's', fromSeq: 1, toSeq: 5, earliestSeq: 1, latestSeq: 9 };
  const commonControl = { type: 'control.state' as const, sessionId: 's', reason: 'acquired', occurredAt: 't' };
  const commonState = { type: 'session.state' as const, sessionId: 's', occurredAt: 't' };

  it('backfill frames/gap are mutually exclusive and one is required', () => {
    // @ts-expect-error frames and gap are mutually exclusive
    const badBoth: BackfillResultEvent = { ...commonBackfill, frames: [], gap };
    // @ts-expect-error one branch is required (neither present)
    const badNeither: BackfillResultEvent = { ...commonBackfill };
    expectTypeOf<BackfillResultEvent>().toEqualTypeOf<BackfillResultEvent>();
    void badBoth;
    void badNeither;
  });

  it('control other requires deviceName; non-other forbids it', () => {
    // @ts-expect-error other requires deviceName
    const badOther: ControlStateEvent = { ...commonControl, state: 'other' };
    // @ts-expect-error none forbids deviceName
    const badNone: ControlStateEvent = { ...commonControl, state: 'none', deviceName: 'x' };
    // @ts-expect-error you forbids deviceName
    const badYou: ControlStateEvent = { ...commonControl, state: 'you', deviceName: 'x' };
    // @ts-expect-error desktop forbids deviceName
    const badDesktop: ControlStateEvent = { ...commonControl, state: 'desktop', deviceName: 'x' };
    void badOther;
    void badNone;
    void badYou;
    void badDesktop;
  });

  it('history gap requires GapRange; continuous/backfilled forbid gap', () => {
    // @ts-expect-error gap state requires GapRange
    const badGap: HistorySnapshot = { state: 'gap' };
    // @ts-expect-error continuous forbids gap
    const badContinuous: HistorySnapshot = { state: 'continuous', gap };
    // @ts-expect-error backfilled forbids gap
    const badBackfilled: HistorySnapshot = { state: 'backfilled', gap };
    void badGap;
    void badContinuous;
    void badBackfilled;
  });

  it('normal session state forbids restart fields', () => {
    // @ts-expect-error normal state event forbids restartBoundary/seq
    const badNormal: SessionStateEvent = { ...commonState, state: 'running', restartBoundary: false, seq: 1 };
    void badNormal;
  });

  it('confirm must be literal true (compile reject of false/null/missing via fixture values)', () => {
    // @ts-expect-error confirm:false not assignable to {confirm:true}
    const badFalse: ConfirmActionRequest = fixture.invalid.confirmFalse;
    // @ts-expect-error confirm:null not assignable to {confirm:true}
    const badNull: ConfirmActionRequest = fixture.invalid.confirmNull;
    // @ts-expect-error confirm missing not assignable to {confirm:true}
    const badMissing: ConfirmActionRequest = fixture.invalid.confirmMissing;
    void badFalse;
    void badNull;
    void badMissing;
  });
});

// ---------------------------------------------------------------------------
// (5) Consumed-set parity: every fixture invalid key is asserted by a test.
// ---------------------------------------------------------------------------
describe('invalid fixture consumption', () => {
  it('every fixture invalid key is consumed by some test (no orphan negative)', () => {
    const consumed = new Set<string>([
      // runtime isClientFrame
      'nullRequiredField', 'missingRequiredField', 'unknownClientFrame', 'unknownClientField',
      // runtime normalizeServerEvent (unknown)
      'framesAndGapBothPresent', 'backfillNeitherFramesNorGap',
      'controlOtherMissingDeviceName', 'controlNoneWithDeviceName', 'controlYouWithDeviceName', 'controlDesktopWithDeviceName',
      'knownOutputNullChunk', 'knownSessionUnknownState',
      'knownAttachedUnknownAuthState', 'knownAttachedUnknownHistoryState', 'knownControlUnknownState',
      'historyGapMissingRange', 'historyGapNullRange', 'historyContinuousWithRange', 'historyBackfilledWithRange',
      'unsafeSeqAboveMax', 'unsafeSeqFractional', 'unsafeSeqNegative', 'replaySeqZero',
      'attachedGapRangeMismatch',
      'outputStructuredExpectedNull', 'errorRequestIdNull', 'errorDetailsNull',
      'sessionStateRestartFalse', 'sessionStateSeqAlone',
      'backfillFrameOutOfRange', 'backfillFrameNonAscending',
      'authRevokedUnknownReason', 'authRevokedNullReason',
      'inputAckMissingId', 'inputAckNullId',
      // compile @ts-expect-error
      'confirmFalse', 'confirmNull', 'confirmMissing',
    ]);
    expect([...consumed].sort()).toEqual(Object.keys(fixture.invalid).sort());
  });
});

// ---------------------------------------------------------------------------
// (6) Compile union shape checks.
// ---------------------------------------------------------------------------
describe('union shapes', () => {
  it('DecodedServerEvent covers known + unknown', () => {
    expectTypeOf<KnownServerEvent | UnknownServerEvent>().toEqualTypeOf<DecodedServerEvent>();
  });

  it('ControlSnapshot is the four-variant conditional union', () => {
    expectTypeOf<{ state: 'other'; deviceName: string }>().toMatchTypeOf<ControlSnapshot>();
    expectTypeOf<{ state: 'none' }>().toMatchTypeOf<ControlSnapshot>();
    expectTypeOf<{ state: 'you' }>().toMatchTypeOf<ControlSnapshot>();
    expectTypeOf<{ state: 'desktop' }>().toMatchTypeOf<ControlSnapshot>();
    expectTypeOf<{ state: 'other' }>().not.toMatchTypeOf<ControlSnapshot>();
  });
});

// ---------------------------------------------------------------------------
// (7) CG-01 auth.revoked canonical reason + close directive contract.
// Addendum §2.2 symbols, §3.2 TS enforcement, §4 compatibility, §7 C1-C6.
// C7/C8 (producer fence→event→close sequencing) require the future producer /
// close writer (M2-A scope) and are NOT tested here: this module only provides
// the contract symbols and the production normalizer.
// ---------------------------------------------------------------------------
describe('CG-01 auth.revoked contract', () => {
  it('C1: canonical reason + close code are the sole symbols (parity with fixture)', () => {
    expect(AUTH_REVOKED_REASON_DEVICE_REVOKED).toBe('device_revoked');
    expect([...KNOWN_AUTH_REVOKED_REASONS]).toEqual(['device_revoked']);
    expect(AUTH_REVOKED_CLOSE_CODE).toBe(1008);
    // The reason tuple is distinct from the event-type/error tuples.
    expect(KNOWN_SERVER_EVENT_TYPES).not.toContain('device_revoked');
    expect(KNOWN_ERROR_CODES).not.toContain('device_revoked');
  });

  it('C2: normalize accepts the canonical fixture event (valid producer bytes)', () => {
    const ev = normalizeServerEvent(fixture.serverEvents.authRevoked) as AuthRevokedEvent;
    expect(ev.type).toBe('auth.revoked');
    expect(ev.reason).toBe(AUTH_REVOKED_REASON_DEVICE_REVOKED);
    expect(ev.reason).toBe('device_revoked');
  });

  it('C3+C4: unknown / null / missing / empty / wrong-type reason all fail-closed to force-unauthorized', () => {
    const cases: Array<{ label: string; input: unknown }> = [
      { label: 'unknown', input: fixture.invalid.authRevokedUnknownReason },
      { label: 'null', input: fixture.invalid.authRevokedNullReason },
      { label: 'missing', input: { type: 'auth.revoked', occurredAt: 't' } },
      { label: 'empty', input: { type: 'auth.revoked', reason: '', occurredAt: 't' } },
      { label: 'wrong-type', input: { type: 'auth.revoked', reason: 123, occurredAt: 't' } },
    ];
    for (const c of cases) {
      const ev = normalizeServerEvent(c.input) as UnknownServerEvent;
      expect(ev.type, `${c.label} must normalize to unknown`).toBe('unknown');
      expect(ev.fallback, `${c.label} must force-unauthorized`).toBe('force-unauthorized');
      // Sanitization: raw reason value is NOT retained.
      const serialized = JSON.stringify(ev);
      expect(serialized, `${c.label} must not retain raw reason`).not.toContain('future_reason_fixture_only');
    }
    // Unknown reason is classified as unknown-enum; null/missing as malformed/unknown.
    const unkReason = normalizeServerEvent(fixture.invalid.authRevokedUnknownReason) as UnknownServerEvent;
    expect(unkReason.reason).toBe('unknown-enum');
  });

  it('legacy open-string consumer probe: a synthetic future reason still forces unauthorized by event TYPE', () => {
    // Simulates an OLD consumer that still treated reason as an open string.
    // The reducer MUST force-unauthorized on type==='auth.revoked' regardless of
    // the reason value; it must never ignore/default on an unknown reason.
    const syntheticFuture = { type: 'auth.revoked', reason: 'session_killed_future', occurredAt: '2099-01-01T00:00:00Z' };
    const ev = normalizeServerEvent(syntheticFuture) as UnknownServerEvent;
    expect(ev.type).toBe('unknown'); // new consumer sanitizes unknown reason
    expect(ev.fallback).toBe('force-unauthorized'); // but still revokes
    expect(ev.wireType).toBe('auth.revoked'); // the wire type is retained for diagnostics
    // The raw future reason value must NOT leak into the sanitized event.
    expect(JSON.stringify(ev)).not.toContain('session_killed_future');
  });

  it('version semantics: old/new/future reason matrix is compatible and fail-closed', () => {
    // old server (fixture bytes) → new consumer: device_revoked is known.
    const oldBytes = fixture.serverEvents.authRevoked;
    const oldDecoded = normalizeServerEvent(oldBytes);
    expect(oldDecoded.type).toBe('auth.revoked');
    // new server → old consumer (open-string reason): the same three fields are
    // accepted by a legacy normalizer that only checked non-empty reason. This
    // is the compatibility obligation: old readers accept the canonical bytes.
    const legacyOpenStringAccept = (input: unknown): boolean => {
      const o = input as Record<string, unknown>;
      return o?.type === 'auth.revoked' && typeof o.reason === 'string' && (o.reason as string).length > 0;
    };
    expect(legacyOpenStringAccept(oldBytes)).toBe(true);
    // future unknown reason → old consumer: must still revoke by TYPE (fail-closed).
    const futureReason = { type: 'auth.revoked', reason: 'device_killed_future', occurredAt: '2099-01-01T00:00:00Z' };
    expect(legacyOpenStringAccept(futureReason)).toBe(true); // old accepts the string
    // future noncanonical → new consumer: sanitized Unknown + force-unauthorized.
    expect((normalizeServerEvent(futureReason) as UnknownServerEvent).fallback).toBe('force-unauthorized');
    // Wire shape unchanged: still the same three fields, no v2/negotiation.
    expect(Object.keys(oldBytes as object).sort()).toEqual(['occurredAt', 'reason', 'type']);
  });

  it('C6: producer narrowing — reason literal satisfies AuthRevokedReason, arbitrary string does not (compile barrier)', () => {
    // The canonical constant is assignable to the closed type.
    const r: AuthRevokedReason = AUTH_REVOKED_REASON_DEVICE_REVOKED;
    expect(r).toBe('device_revoked');
    // @ts-expect-error an arbitrary string literal is NOT assignable to the closed union
    const bad: AuthRevokedReason = 'session_killed';
    void bad;
    // The AuthRevokedEvent.reason field is the closed type, not open string.
    expectTypeOf<AuthRevokedEvent['reason']>().toEqualTypeOf<AuthRevokedReason>();
  });
});

// ---------------------------------------------------------------------------
// (8) CG-03 input.ack contract (contract-addendum-cg03.md §3/§6).
// C4/C5/C6/C8/C9 require the server ledger + client outbox (Step 3); this
// module only provides the contract symbols + the production normalizer, so the
// tested assertions are the symbol/normalize/classifier facts (C1-C3, C7).
// ---------------------------------------------------------------------------
describe('CG-03 input.ack contract', () => {
  it('C1: event/mode symbols + counts (8 server / 10 concrete; frozen rest)', () => {
    expect(SERVER_EVENT_TYPE_INPUT_ACK).toBe('input.ack');
    expect(INPUT_ACK_MODE_SESSION_WINDOW_V1).toBe('session-window-v1');
    expect([...KNOWN_SERVER_EVENT_TYPES]).toEqual(m.serverEventTypes);
    expect(KNOWN_SERVER_EVENT_TYPES).toHaveLength(8);
    expect(KNOWN_SERVER_EVENT_TYPES[7]).toBe('input.ack');
    // frozen counts unchanged.
    expect(V1_REST_ENDPOINTS).toHaveLength(10);
    expect(KNOWN_CLIENT_FRAME_TYPES).toHaveLength(5);
    expect(KNOWN_ERROR_CODES).toHaveLength(12);
    // inputAckMode manifest parity.
    expect(m.inputAckModes).toEqual(['session-window-v1']);
  });

  it('C2: canonical ACK normalizes; InputAckEvent is not a ReplayFrame shape', () => {
    const ev = normalizeServerEvent(fixture.serverEvents.inputAck) as InputAckEvent;
    expect(ev.type).toBe('input.ack');
    expect(isCanonicalMessageID(ev.id)).toBe(true);
    expect(isCanonicalRequestID(ev.requestId)).toBe(true);
    // ACK has no seq/status/data/details fields.
    expect('seq' in ev).toBe(false);
    expect('data' in ev).toBe(false);
    expect('details' in ev).toBe(false);
  });

  it('C3: legacy non-empty opaque id normalizes (per-connection path stays)', () => {
    const ev = normalizeServerEvent(fixture.serverEvents.inputAckLegacyId) as InputAckEvent;
    expect(ev.type).toBe('input.ack');
    expect(isCanonicalMessageID(ev.id)).toBe(false); // legacy, not canonical
  });

  it('C7: malformed ACK normalizes to Unknown + force-read-only (no raw id leak)', () => {
    for (const key of ['inputAckMissingId', 'inputAckNullId'] as const) {
      const ev = normalizeServerEvent(fixture.invalid[key]) as UnknownServerEvent;
      expect(ev.type, key).toBe('unknown');
      expect(ev.fallback, key).toBe('force-read-only');
      // raw id value must not leak into the sanitized event.
      expect(JSON.stringify(ev)).not.toContain('msg-v1-');
    }
    // unknown inputAckMode on attached → forward-compat: normalizes to valid
    // attached with inputAckMode absent (read projection preserved, input disabled).
    const modeFuture = normalizeServerEvent(fixture.serverEvents.sessionAttachedUnknownInputAckMode) as Extract<DecodedServerEvent, { type: 'session.attached' }>;
    expect(modeFuture.type).toBe('session.attached');
    expect(modeFuture.inputAckMode).toBeUndefined();
  });

  it('classifier: exact 39-byte msg-v1-/req-v1- + 32 lower-hex only', () => {
    expect(isCanonicalMessageID('msg-v1-22222222222222222222222222222222')).toBe(true);
    expect(isCanonicalMessageID('msg-v1-0123456789abcdef0123456789abcdef')).toBe(true);
    for (const bad of ['', 'msg_1', 'msg-v1-222', 'msg-v1-2222222222222222222222222222222X', 'MSG-V1-22222222222222222222222222222222', 'msg-v1-222222222222222222222222222222222']) {
      expect(isCanonicalMessageID(bad), bad).toBe(false);
    }
    expect(isCanonicalRequestID('req-v1-11111111111111111111111111111111')).toBe(true);
    expect(isCanonicalRequestID('req-v1-short')).toBe(false);
  });

  it('attached.inputAckMode is optional and additive (absent = old server)', () => {
    // absent (old server) still normalizes.
    const absent = normalizeServerEvent(fixture.serverEvents.sessionAttachedEmptyHistory) as Extract<DecodedServerEvent, { type: 'session.attached' }>;
    expect(absent.inputAckMode).toBeUndefined();
    // present canonical = capability available.
    const present = normalizeServerEvent(fixture.serverEvents.sessionAttachedWithInputAckMode) as Extract<DecodedServerEvent, { type: 'session.attached' }>;
    expect(present.inputAckMode).toBe('session-window-v1');
  });
});
