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
  BackfillResultEvent,
  ClientFrame,
  ConfirmActionRequest,
  ControlSnapshot,
  ControlStateEvent,
  DecodedServerEvent,
  HistorySnapshot,
  KnownServerEvent,
  SessionStateEvent,
  UnknownServerEvent,
} from './index';
import {
  isClientFrame,
  KNOWN_ACTION_HINTS,
  KNOWN_CLI_TYPES,
  KNOWN_CLIENT_FRAME_TYPES,
  KNOWN_CONTROL_STATES,
  KNOWN_ERROR_CODES,
  KNOWN_ERROR_LAYERS,
  KNOWN_HISTORY_STATES,
  KNOWN_SERVER_EVENT_TYPES,
  KNOWN_SESSION_STATES,
  normalizeServerEvent,
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
    'output', 'backfillResultFrames', 'backfillResultGap',
    'sessionStateExited', 'sessionStateRestartBoundary',
    'controlStateOther', 'controlStateYou', 'controlStateNone', 'controlStateDesktop',
    'authRevoked', 'error',
  ] as const;

  it('normalizes every known fixture event to KnownServerEvent', () => {
    for (const key of knownKeys) {
      const ev = normalizeServerEvent(fixture.serverEvents[key]);
      expect(ev.type, `${key} should be known`).not.toBe('unknown');
    }
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
    ] as const;
    for (const key of serverInvalid) {
      const ev = normalizeServerEvent(fixture.invalid[key]);
      expect(ev.type, `${key} must normalize to unknown`).toBe('unknown');
    }
  });

  it('fallback maps enum failures to safe layers', () => {
    expect((normalizeServerEvent(fixture.invalid.knownAttachedUnknownAuthState) as UnknownServerEvent).fallback).toBe('force-unauthorized');
    expect((normalizeServerEvent(fixture.invalid.knownControlUnknownState) as UnknownServerEvent).fallback).toBe('force-read-only');
    expect((normalizeServerEvent(fixture.invalid.knownAttachedUnknownHistoryState) as UnknownServerEvent).fallback).toBe('mark-history-gap');
    expect((normalizeServerEvent(fixture.invalid.knownSessionUnknownState) as UnknownServerEvent).fallback).toBe('mark-session-unavailable');
    expect((normalizeServerEvent(fixture.invalid.unsafeSeqAboveMax) as UnknownServerEvent).fallback).toBe('mark-history-gap');
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
