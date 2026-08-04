/**
 * WebSocket v1 client frames, server events and production normalizer for the
 * remote contract.
 *
 * Design §8 + addendum §1/§3/§4. URL is SOLELY /ws/v1 (no token/session/mode in
 * URL; Cookie authenticates). Every client frame has a required top-level
 * requestId. There are 5 client frame types and 7 server event type categories;
 * backfill.result (frames|gap) and session.state (normal|restart-boundary) each
 * have two variants.
 *
 * normalizeServerEvent/isClientFrame are PURE production runtime validators:
 * they validate untrusted `unknown`, return sanitized results, never throw, and
 * have no store/network/log side effects. The WebSocket business consumer is
 * NOT wired in M0 (addendum §5.5 honest boundary).
 *
 * This module does NOT reuse the legacy TerminalFrame (mobile/src/api/websocket.ts).
 */
import type {
  APIVersion,
  MessageID,
  RequestID,
  Seq,
  SessionID,
  SessionState,
} from './scalars';
import { MAX_SAFE_SEQ } from './scalars';
import type { ActionHint, ErrorCode, ErrorLayer } from './errors';
import type { ControlSnapshot } from './rest';

// ---------------------------------------------------------------------------
// Client frame type constants
// ---------------------------------------------------------------------------
export const CLIENT_FRAME_TYPE_ATTACH = 'attach' as const;
export const CLIENT_FRAME_TYPE_INPUT = 'input' as const;
export const CLIENT_FRAME_TYPE_RESIZE = 'resize' as const;
export const CLIENT_FRAME_TYPE_BACKFILL = 'backfill' as const;
export const CLIENT_FRAME_TYPE_PING = 'ping' as const;
export const KNOWN_CLIENT_FRAME_TYPES = [
  CLIENT_FRAME_TYPE_ATTACH,
  CLIENT_FRAME_TYPE_INPUT,
  CLIENT_FRAME_TYPE_RESIZE,
  CLIENT_FRAME_TYPE_BACKFILL,
  CLIENT_FRAME_TYPE_PING,
] as const;

// ---------------------------------------------------------------------------
// Server event type constants
// ---------------------------------------------------------------------------
export const SERVER_EVENT_TYPE_SESSION_ATTACHED = 'session.attached' as const;
export const SERVER_EVENT_TYPE_OUTPUT = 'output' as const;
export const SERVER_EVENT_TYPE_BACKFILL_RESULT = 'backfill.result' as const;
export const SERVER_EVENT_TYPE_SESSION_STATE = 'session.state' as const;
export const SERVER_EVENT_TYPE_CONTROL_STATE = 'control.state' as const;
export const SERVER_EVENT_TYPE_AUTH_REVOKED = 'auth.revoked' as const;
export const SERVER_EVENT_TYPE_ERROR = 'error' as const;
export const KNOWN_SERVER_EVENT_TYPES = [
  SERVER_EVENT_TYPE_SESSION_ATTACHED,
  SERVER_EVENT_TYPE_OUTPUT,
  SERVER_EVENT_TYPE_BACKFILL_RESULT,
  SERVER_EVENT_TYPE_SESSION_STATE,
  SERVER_EVENT_TYPE_CONTROL_STATE,
  SERVER_EVENT_TYPE_AUTH_REVOKED,
  SERVER_EVENT_TYPE_ERROR,
] as const;

// ---------------------------------------------------------------------------
// auth.revoked reason manifest + close directive (CG-01 contract addendum).
// The reason is a closed one-value enum for server PRODUCERS; the close code
// is a transport directive, NOT an 8th event type or 13th error code. Unknown
// reasons normalize to a sanitized UnknownServerEvent with force-unauthorized
// (fail-closed). Clients revoke on the event TYPE, never on the reason value.
// ---------------------------------------------------------------------------
export const AUTH_REVOKED_REASON_DEVICE_REVOKED = 'device_revoked' as const;
export const KNOWN_AUTH_REVOKED_REASONS = [
  AUTH_REVOKED_REASON_DEVICE_REVOKED,
] as const;
export type AuthRevokedReason = (typeof KNOWN_AUTH_REVOKED_REASONS)[number];
export const AUTH_REVOKED_CLOSE_CODE = 1008 as const;

// ---------------------------------------------------------------------------
// Snapshot layers (session.attached). HistorySnapshot is a conditional union
// (addendum §1.4): gap state requires a GapRange; continuous/backfilled forbid it.
// ---------------------------------------------------------------------------
export interface ConnectionSnapshot {
  state: 'connected';
}
export interface AuthSnapshot {
  state: 'authorized';
}
export interface SessionSnapshot {
  state: SessionState;
}
export interface GapRange {
  code: 'history.gap';
  fromSeq: Seq;
  toSeq: Seq;
}
export type HistorySnapshot =
  | { state: 'continuous' | 'backfilled'; gap?: never }
  | { state: 'gap'; gap: GapRange };
export interface FiveLayerSnapshot {
  connection: ConnectionSnapshot;
  auth: AuthSnapshot;
  session: SessionSnapshot;
  control: ControlSnapshot;
  history: HistorySnapshot;
}

// ---------------------------------------------------------------------------
// Client frames — discriminated union on `type`
// ---------------------------------------------------------------------------
export interface AttachFrame {
  type: typeof CLIENT_FRAME_TYPE_ATTACH;
  requestId: RequestID;
  apiVersion: APIVersion;
  sessionId: SessionID;
  lastSeq?: Seq;
}
export interface InputFrame {
  type: typeof CLIENT_FRAME_TYPE_INPUT;
  requestId: RequestID;
  id: MessageID;
  data: string; // non-empty RFC4648 Base64
}
export interface ResizeFrame {
  type: typeof CLIENT_FRAME_TYPE_RESIZE;
  requestId: RequestID;
  cols: number;
  rows: number;
}
export interface BackfillFrame {
  type: typeof CLIENT_FRAME_TYPE_BACKFILL;
  requestId: RequestID;
  fromSeq: Seq;
  toSeq: Seq;
}
export interface PingFrame {
  type: typeof CLIENT_FRAME_TYPE_PING;
  requestId: RequestID;
}

export type ClientFrame = AttachFrame | InputFrame | ResizeFrame | BackfillFrame | PingFrame;

// ---------------------------------------------------------------------------
// Server events — 7 categories
// ---------------------------------------------------------------------------
export interface SessionAttachedEvent {
  type: typeof SERVER_EVENT_TYPE_SESSION_ATTACHED;
  requestId: RequestID;
  apiVersion: APIVersion;
  sessionId: SessionID;
  history: ReplayFrame[];
  earliestSeq: Seq;
  latestSeq: Seq;
  snapshot: FiveLayerSnapshot;
}

export interface OutputEvent {
  type: typeof SERVER_EVENT_TYPE_OUTPUT;
  sessionId: SessionID;
  seq: Seq;
  chunk: string;
  structuredExpected?: boolean;
}

type BackfillCommon = {
  type: typeof SERVER_EVENT_TYPE_BACKFILL_RESULT;
  requestId: RequestID;
  sessionId: SessionID;
  fromSeq: Seq;
  toSeq: Seq;
  earliestSeq: Seq;
  latestSeq: Seq;
};

export type BackfillFramesResultEvent = BackfillCommon & {
  frames: ReplayFrame[];
  gap?: never;
};

export type BackfillGapResultEvent = BackfillCommon & {
  gap: GapRange;
  frames?: never;
};

export type BackfillResultEvent = BackfillFramesResultEvent | BackfillGapResultEvent;

export interface SessionStateEvent {
  type: typeof SERVER_EVENT_TYPE_SESSION_STATE;
  sessionId: SessionID;
  state: SessionState;
  restartBoundary?: never;
  seq?: never;
  occurredAt: string;
}

export interface SessionRestartBoundaryEvent {
  type: typeof SERVER_EVENT_TYPE_SESSION_STATE;
  sessionId: SessionID;
  state: SessionState;
  restartBoundary: true;
  seq: Seq;
  occurredAt: string;
}

type ControlStateEventCommon = {
  type: typeof SERVER_EVENT_TYPE_CONTROL_STATE;
  sessionId: SessionID;
  reason: string;
  occurredAt: string;
};

export type ControlStateEvent = ControlStateEventCommon & (
  | { state: 'other'; deviceName: string }
  | { state: 'none' | 'you' | 'desktop'; deviceName?: never }
);

export interface AuthRevokedEvent {
  type: typeof SERVER_EVENT_TYPE_AUTH_REVOKED;
  reason: AuthRevokedReason;
  occurredAt: string;
}

export interface ErrorEvent {
  type: typeof SERVER_EVENT_TYPE_ERROR;
  requestId?: RequestID;
  sessionId?: SessionID;
  code: ErrorCode;
  layer: ErrorLayer;
  message: string;
  actionHint: ActionHint;
  details?: Record<string, unknown>;
}

/** ReplayFrame: server events that occupy a seq position in replay history. */
export type ReplayFrame = OutputEvent | SessionRestartBoundaryEvent;

/** KnownServerEvent: the 7 frozen categories. */
export type KnownServerEvent =
  | SessionAttachedEvent
  | OutputEvent
  | BackfillFramesResultEvent
  | BackfillGapResultEvent
  | SessionStateEvent
  | SessionRestartBoundaryEvent
  | ControlStateEvent
  | AuthRevokedEvent
  | ErrorEvent;

// ---------------------------------------------------------------------------
// UnknownServerEvent — sanitized fail-safe (addendum §4.4). NO raw payload,
// field values, unknown field names or sensitive values are retained.
// ---------------------------------------------------------------------------
export type UnknownServerEventReason =
  | 'unknown-type'
  | 'malformed-known-event'
  | 'unknown-enum'
  | 'unsafe-seq';

export type UnknownServerEventFallback =
  | 'ignore'
  | 'force-unauthorized'
  | 'force-read-only'
  | 'mark-history-gap'
  | 'mark-session-unavailable';

export interface UnknownServerEvent {
  type: 'unknown';
  wireType: string; // sanitized: matches [A-Za-z0-9._-]{1,64} else '<invalid>'
  reason: UnknownServerEventReason;
  fallback: UnknownServerEventFallback;
  metadata: Readonly<{
    inputKind: 'object' | 'array' | 'null' | 'scalar';
    hasRequestId: boolean;
    hasSessionId: boolean;
    hasSeq: boolean;
  }>;
}

export type DecodedServerEvent = KnownServerEvent | UnknownServerEvent;

// ===========================================================================
// Production runtime validation (addendum §4). Pure; never throws.
// ===========================================================================

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function isNonEmptyString(v: unknown): v is string {
  return typeof v === 'string' && v.length > 0;
}

function isSafeSeq(v: unknown): v is number {
  return typeof v === 'number' && Number.isSafeInteger(v) && v >= 0 && v <= MAX_SAFE_SEQ;
}

function isReplaySeq(v: unknown): v is number {
  return typeof v === 'number' && Number.isSafeInteger(v) && v >= 1 && v <= MAX_SAFE_SEQ;
}

function isBase64(v: unknown): v is string {
  return typeof v === 'string' && v.length > 0 && v.length % 4 === 0 && /^[A-Za-z0-9+/]+={0,2}$/.test(v);
}

const CLOSED_SESSION_STATES = new Set<string>(['running', 'stopped', 'exited', 'unavailable', 'removed']);
const CLOSED_CONTROL_STATES = new Set<string>(['none', 'you', 'other', 'desktop']);
const CLOSED_HISTORY_STATES = new Set<string>(['continuous', 'backfilled', 'gap']);
const CLOSED_ERROR_LAYERS = new Set<string>(['connection', 'auth', 'session', 'control', 'history']);
const KNOWN_SERVER_TYPES = new Set<string>(KNOWN_SERVER_EVENT_TYPES);

// isKnownAuthRevokedReason: type guard for the CG-01 closed reason enum. An
// empty/future/non-canonical value returns false so normalizeAuthRevoked
// rejects it (the caller then force-unauthorizes via inferFallback).
function isKnownAuthRevokedReason(v: unknown): v is AuthRevokedReason {
  return typeof v === 'string' && (KNOWN_AUTH_REVOKED_REASONS as readonly string[]).includes(v);
}

function sanitizeWireType(t: unknown): string {
  if (typeof t === 'string' && /^[A-Za-z0-9._-]{1,64}$/.test(t)) return t;
  return '<invalid>';
}

function inputKindOf(v: unknown): 'object' | 'array' | 'null' | 'scalar' {
  if (v === null) return 'null';
  if (Array.isArray(v)) return 'array';
  if (typeof v === 'object') return 'object';
  return 'scalar';
}

type Reasoned = { reason: UnknownServerEventReason; fallback: UnknownServerEventFallback };

// Fallback priority: force-unauthorized > force-read-only > mark-history-gap >
// mark-session-unavailable > ignore.
const FALLBACK_RANK: Record<UnknownServerEventFallback, number> = {
  'force-unauthorized': 4,
  'force-read-only': 3,
  'mark-history-gap': 2,
  'mark-session-unavailable': 1,
  ignore: 0,
};

function pickFallback(reason: UnknownServerEventReason, layerFallback: UnknownServerEventFallback): Reasoned {
  return { reason, fallback: layerFallback };
}

function makeUnknown(input: unknown, wireTypeRaw: unknown, r: Reasoned): UnknownServerEvent {
  const obj = isPlainObject(input) ? input : {};
  return {
    type: 'unknown',
    wireType: sanitizeWireType(wireTypeRaw),
    reason: r.reason,
    fallback: r.fallback,
    metadata: {
      inputKind: inputKindOf(input),
      hasRequestId: 'requestId' in obj,
      hasSessionId: 'sessionId' in obj,
      hasSeq: 'seq' in obj,
    },
  };
}

/**
 * normalizeServerEvent validates an untrusted server message and returns either
 * a canonical KnownServerEvent (additive unknown fields discarded) or a
 * sanitized UnknownServerEvent. It never throws.
 */
export function normalizeServerEvent(input: unknown): DecodedServerEvent {
  const obj = isPlainObject(input) ? input : null;
  const wireTypeRaw = obj ? obj.type : undefined;
  if (obj === null || typeof obj.type !== 'string') {
    return makeUnknown(input, wireTypeRaw, pickFallback('malformed-known-event', 'ignore'));
  }
  if (!KNOWN_SERVER_TYPES.has(obj.type)) {
    return makeUnknown(input, wireTypeRaw, pickFallback('unknown-type', 'ignore'));
  }
  const result = normalizeKnown(obj);
  if (result === null) {
    // Determine the safest fallback by the failed layer; default ignore.
    return makeUnknown(input, wireTypeRaw, inferFallback(obj));
  }
  return result;
}

// inferFallback inspects the known event's layer fields to choose the safest
// fallback per addendum §4.3 priority.
function inferFallback(obj: Record<string, unknown>): Reasoned {
  const t = obj.type;
  let best: Reasoned = { reason: 'malformed-known-event', fallback: 'ignore' };
  const consider = (r: Reasoned) => {
    if (FALLBACK_RANK[r.fallback] > FALLBACK_RANK[best.fallback]) best = r;
  };
  if (t === SERVER_EVENT_TYPE_SESSION_ATTACHED) {
    const snap = isPlainObject(obj.snapshot) ? obj.snapshot : {};
    const auth = isPlainObject(snap.auth) ? snap.auth : {};
    if ('state' in auth && typeof (auth as Record<string, unknown>).state === 'string' && (auth as Record<string, unknown>).state !== 'authorized') {
      consider({ reason: 'unknown-enum', fallback: 'force-unauthorized' });
    }
    const hist = isPlainObject(snap.history) ? snap.history : {};
    if ('state' in hist && !CLOSED_HISTORY_STATES.has(String((hist as Record<string, unknown>).state))) {
      consider({ reason: 'unknown-enum', fallback: 'mark-history-gap' });
    }
  }
  if (t === SERVER_EVENT_TYPE_CONTROL_STATE) {
    if ('state' in obj && !CLOSED_CONTROL_STATES.has(String(obj.state))) {
      consider({ reason: 'unknown-enum', fallback: 'force-read-only' });
    }
  }
  if (t === SERVER_EVENT_TYPE_SESSION_STATE) {
    if ('state' in obj && !CLOSED_SESSION_STATES.has(String(obj.state))) {
      consider({ reason: 'unknown-enum', fallback: 'mark-session-unavailable' });
    }
  }
  if (t === SERVER_EVENT_TYPE_AUTH_REVOKED) {
    // CG-01 fail-closed: ANY auth.revoked that fails normalization MUST
    // force-unauthorized. The client revokes on the event type, never on the
    // reason value. reason-field problems are 'unknown-enum'; other malformed
    // fields are 'malformed-known-event' — both sanitize to the same fallback.
    const reasonBad = !isKnownAuthRevokedReason(obj.reason);
    consider({ reason: reasonBad ? 'unknown-enum' : 'malformed-known-event', fallback: 'force-unauthorized' });
  }
  if (t === SERVER_EVENT_TYPE_OUTPUT || t === SERVER_EVENT_TYPE_BACKFILL_RESULT) {
    if ('seq' in obj && !isSafeSeq(obj.seq)) {
      consider({ reason: 'unsafe-seq', fallback: 'mark-history-gap' });
    }
  }
  return best;
}

// normalizeKnown returns a canonical KnownServerEvent or null when validation
// fails. Additive unknown fields are discarded.
function normalizeKnown(obj: Record<string, unknown>): KnownServerEvent | null {
  switch (obj.type) {
    case SERVER_EVENT_TYPE_SESSION_ATTACHED:
      return normalizeAttached(obj);
    case SERVER_EVENT_TYPE_OUTPUT:
      return normalizeOutput(obj);
    case SERVER_EVENT_TYPE_BACKFILL_RESULT:
      return normalizeBackfill(obj);
    case SERVER_EVENT_TYPE_SESSION_STATE:
      return normalizeSessionState(obj);
    case SERVER_EVENT_TYPE_CONTROL_STATE:
      return normalizeControlState(obj);
    case SERVER_EVENT_TYPE_AUTH_REVOKED:
      return normalizeAuthRevoked(obj);
    case SERVER_EVENT_TYPE_ERROR:
      return normalizeError(obj);
    default:
      return null;
  }
}

function reqStr(obj: Record<string, unknown>, key: string): string | null {
  const v = obj[key];
  return isNonEmptyString(v) ? v : null;
}

// optNonEmptyString: absent => undefined (legal); present-but-null/empty/wrong-type => null (invalid).
function optNonEmptyString(obj: Record<string, unknown>, key: string): string | undefined | null {
  if (!(key in obj)) return undefined;
  return isNonEmptyString(obj[key]) ? (obj[key] as string) : null;
}

// optPlainObject: absent => undefined (legal); present-but-null/non-object => null (invalid).
function optPlainObject(obj: Record<string, unknown>, key: string): Record<string, unknown> | undefined | null {
  if (!(key in obj)) return undefined;
  return isPlainObject(obj[key]) ? (obj[key] as Record<string, unknown>) : null;
}

// replayFrameSeq returns the occupied seq of a ReplayFrame concrete value.
function replayFrameSeq(fr: ReplayFrame): number {
  return fr.seq;
}

function normalizeOutput(obj: Record<string, unknown>): OutputEvent | null {
  const sessionId = reqStr(obj, 'sessionId');
  if (!sessionId) return null;
  if (!isReplaySeq(obj.seq)) return null;
  if (!isBase64(obj.chunk)) return null;
  const out: OutputEvent = { type: SERVER_EVENT_TYPE_OUTPUT, sessionId, seq: obj.seq, chunk: obj.chunk };
  if ('structuredExpected' in obj) {
    if (typeof obj.structuredExpected !== 'boolean') return null;
    out.structuredExpected = obj.structuredExpected;
  }
  return out;
}

function normalizeReplayFrame(v: unknown): ReplayFrame | null {
  if (!isPlainObject(v)) return null;
  if (v.type === SERVER_EVENT_TYPE_OUTPUT) {
    const o = normalizeOutput(v);
    return o;
  }
  if (v.type === SERVER_EVENT_TYPE_SESSION_STATE && v.restartBoundary === true) {
    return normalizeRestartBoundary(v);
  }
  return null;
}

function normalizeRestartBoundary(obj: Record<string, unknown>): SessionRestartBoundaryEvent | null {
  const sessionId = reqStr(obj, 'sessionId');
  if (!sessionId) return null;
  const state = obj.state;
  if (typeof state !== 'string' || !CLOSED_SESSION_STATES.has(state)) return null;
  if (obj.restartBoundary !== true) return null;
  if (!isReplaySeq(obj.seq)) return null;
  const occurredAt = reqStr(obj, 'occurredAt');
  if (!occurredAt) return null;
  return { type: SERVER_EVENT_TYPE_SESSION_STATE, sessionId, state: state as SessionState, restartBoundary: true, seq: obj.seq, occurredAt };
}

function normalizeReplayArray(v: unknown): ReplayFrame[] | null {
  if (!Array.isArray(v)) return null;
  const out: ReplayFrame[] = [];
  for (const item of v) {
    const rf = normalizeReplayFrame(item);
    if (rf === null) return null;
    out.push(rf);
  }
  return out;
}

function normalizeGapRange(v: unknown): GapRange | null {
  if (!isPlainObject(v)) return null;
  if (v.code !== 'history.gap') return null;
  if (!isReplaySeq(v.fromSeq)) return null;
  if (!isSafeSeq(v.toSeq)) return null;
  if ((v.toSeq as number) < (v.fromSeq as number)) return null;
  return { code: 'history.gap', fromSeq: v.fromSeq as number, toSeq: v.toSeq as number };
}

function normalizeHistorySnapshot(v: unknown): HistorySnapshot | null {
  if (!isPlainObject(v)) return null;
  const state = v.state;
  if (state === 'continuous' || state === 'backfilled') {
    if (!('gap' in v) || v.gap === undefined) return { state };
    return null; // gap must be absent
  }
  if (state === 'gap') {
    const gap = normalizeGapRange(v.gap);
    if (!gap) return null;
    return { state: 'gap', gap };
  }
  return null;
}

function normalizeControlSnapshot(v: unknown): ControlSnapshot | null {
  if (!isPlainObject(v)) return null;
  const state = v.state;
  if (state === 'other') {
    const dn = reqStr(v, 'deviceName');
    if (!dn) return null;
    return { state: 'other', deviceName: dn };
  }
  if (state === 'none' || state === 'you' || state === 'desktop') {
    if ('deviceName' in v && v.deviceName !== undefined) return null;
    return { state };
  }
  return null;
}

function normalizeFiveLayer(v: unknown): FiveLayerSnapshot | null {
  if (!isPlainObject(v)) return null;
  const conn = v.connection;
  if (!isPlainObject(conn) || conn.state !== 'connected') return null;
  const auth = v.auth;
  if (!isPlainObject(auth) || auth.state !== 'authorized') return null;
  const sess = v.session;
  if (!isPlainObject(sess) || typeof sess.state !== 'string' || !CLOSED_SESSION_STATES.has(sess.state)) return null;
  const ctrl = normalizeControlSnapshot(v.control);
  if (!ctrl) return null;
  const hist = normalizeHistorySnapshot(v.history);
  if (!hist) return null;
  return {
    connection: { state: 'connected' },
    auth: { state: 'authorized' },
    session: { state: sess.state as SessionState },
    control: ctrl,
    history: hist,
  };
}

function normalizeAttached(obj: Record<string, unknown>): SessionAttachedEvent | null {
  const requestId = reqStr(obj, 'requestId');
  if (!requestId) return null;
  if (obj.apiVersion !== 'v1') return null;
  const sessionId = reqStr(obj, 'sessionId');
  if (!sessionId) return null;
  const history = normalizeReplayArray(obj.history);
  if (history === null) return null;
  if (!isSafeSeq(obj.earliestSeq)) return null;
  if (!isSafeSeq(obj.latestSeq)) return null;
  if ((obj.earliestSeq as number) > (obj.latestSeq as number)) return null;
  const snapshot = normalizeFiveLayer(obj.snapshot);
  if (!snapshot) return null;
  // Attached gap relation (addendum §1.2): gap.ToSeq+1 must equal earliestSeq.
  if (snapshot.history.state === 'gap' && snapshot.history.gap) {
    if (snapshot.history.gap.toSeq + 1 !== (obj.earliestSeq as number)) return null;
  }
  return { type: SERVER_EVENT_TYPE_SESSION_ATTACHED, requestId, apiVersion: 'v1', sessionId, history, earliestSeq: obj.earliestSeq as number, latestSeq: obj.latestSeq as number, snapshot };
}

function normalizeBackfill(obj: Record<string, unknown>): BackfillResultEvent | null {
  const requestId = reqStr(obj, 'requestId');
  if (!requestId) return null;
  const sessionId = reqStr(obj, 'sessionId');
  if (!sessionId) return null;
  if (!isReplaySeq(obj.fromSeq)) return null;
  if (!isSafeSeq(obj.toSeq)) return null;
  if ((obj.toSeq as number) < (obj.fromSeq as number)) return null;
  if (!isSafeSeq(obj.earliestSeq)) return null;
  if (!isSafeSeq(obj.latestSeq)) return null;
  const hasFrames = 'frames' in obj;
  const hasGap = 'gap' in obj;
  const common = { type: SERVER_EVENT_TYPE_BACKFILL_RESULT, requestId, sessionId, fromSeq: obj.fromSeq as number, toSeq: obj.toSeq as number, earliestSeq: obj.earliestSeq as number, latestSeq: obj.latestSeq as number };
  if (hasFrames && !hasGap) {
    const frames = normalizeReplayArray(obj.frames);
    if (frames === null || frames.length === 0) return null;
    for (let i = 0; i < frames.length; i++) {
      const s = replayFrameSeq(frames[i]);
      if (s < common.fromSeq || s > common.toSeq) return null;
      if (i > 0 && s <= replayFrameSeq(frames[i - 1])) return null;
    }
    return { ...common, frames };
  }
  if (hasGap && !hasFrames) {
    const gap = normalizeGapRange(obj.gap);
    if (!gap) return null;
    return { ...common, gap };
  }
  return null; // both or neither
}

function normalizeSessionState(obj: Record<string, unknown>): SessionStateEvent | SessionRestartBoundaryEvent | null {
  const sessionId = reqStr(obj, 'sessionId');
  if (!sessionId) return null;
  const state = obj.state;
  if (typeof state !== 'string' || !CLOSED_SESSION_STATES.has(state)) return null;
  const occurredAt = reqStr(obj, 'occurredAt');
  if (!occurredAt) return null;
  if (obj.restartBoundary === true) {
    if (!isReplaySeq(obj.seq)) return null;
    return { type: SERVER_EVENT_TYPE_SESSION_STATE, sessionId, state: state as SessionState, restartBoundary: true, seq: obj.seq, occurredAt };
  }
  if ('restartBoundary' in obj || 'seq' in obj) return null;
  return { type: SERVER_EVENT_TYPE_SESSION_STATE, sessionId, state: state as SessionState, occurredAt };
}

function normalizeControlState(obj: Record<string, unknown>): ControlStateEvent | null {
  const sessionId = reqStr(obj, 'sessionId');
  if (!sessionId) return null;
  const state = obj.state;
  const reason = reqStr(obj, 'reason');
  if (!reason) return null;
  const occurredAt = reqStr(obj, 'occurredAt');
  if (!occurredAt) return null;
  const common = { type: SERVER_EVENT_TYPE_CONTROL_STATE, sessionId, reason, occurredAt };
  if (state === 'other') {
    const dn = reqStr(obj, 'deviceName');
    if (!dn) return null;
    return { ...common, state: 'other', deviceName: dn };
  }
  if (state === 'none' || state === 'you' || state === 'desktop') {
    if ('deviceName' in obj && obj.deviceName !== undefined) return null;
    return { ...common, state };
  }
  return null;
}

function normalizeAuthRevoked(obj: Record<string, unknown>): AuthRevokedEvent | null {
  // CG-01: reason MUST be a known canonical value. Unknown/missing/null/empty
  // returns null; the caller maps the failure to force-unauthorized.
  if (!isKnownAuthRevokedReason(obj.reason)) return null;
  const occurredAt = reqStr(obj, 'occurredAt');
  if (!occurredAt) return null;
  return { type: SERVER_EVENT_TYPE_AUTH_REVOKED, reason: obj.reason, occurredAt };
}

function normalizeError(obj: Record<string, unknown>): ErrorEvent | null {
  const code = reqStr(obj, 'code');
  if (!code) return null;
  const layer = obj.layer;
  if (typeof layer !== 'string' || !CLOSED_ERROR_LAYERS.has(layer)) return null;
  const message = reqStr(obj, 'message');
  if (!message) return null;
  const actionHint = reqStr(obj, 'actionHint');
  if (!actionHint) return null;
  const out: ErrorEvent = { type: SERVER_EVENT_TYPE_ERROR, code: code as ErrorCode, layer: layer as ErrorLayer, message, actionHint: actionHint as ActionHint };
  const rid = optNonEmptyString(obj, 'requestId');
  if (rid === null) return null;
  if (rid !== undefined) out.requestId = rid;
  const sid = optNonEmptyString(obj, 'sessionId');
  if (sid === null) return null;
  if (sid !== undefined) out.sessionId = sid;
  const det = optPlainObject(obj, 'details');
  if (det === null) return null;
  if (det !== undefined) out.details = det;
  return out;
}

// ---------------------------------------------------------------------------
// isClientFrame — production type guard for untrusted client frames. Validates
// exact allowed keys, required/non-null, literal type, base64, positive sizes
// and safe seq/range. Used at the contract/type boundary only (it does not send).
// ---------------------------------------------------------------------------
export function isClientFrame(input: unknown): input is ClientFrame {
  if (!isPlainObject(input)) return false;
  switch (input.type) {
    case CLIENT_FRAME_TYPE_ATTACH:
      return isAttachFrame(input);
    case CLIENT_FRAME_TYPE_INPUT:
      return isInputFrame(input);
    case CLIENT_FRAME_TYPE_RESIZE:
      return isResizeFrame(input);
    case CLIENT_FRAME_TYPE_BACKFILL:
      return isBackfillFrame(input);
    case CLIENT_FRAME_TYPE_PING:
      return isPingFrame(input);
    default:
      return false;
  }
}

function exactKeys(obj: Record<string, unknown>, allowed: readonly string[]): boolean {
  for (const k of Object.keys(obj)) {
    if (!allowed.includes(k)) return false;
  }
  return true;
}

function isAttachFrame(o: Record<string, unknown>): boolean {
  if (!exactKeys(o, ['type', 'requestId', 'apiVersion', 'sessionId', 'lastSeq'])) return false;
  if (!reqStr(o, 'requestId')) return false;
  if (o.apiVersion !== 'v1') return false;
  if (!reqStr(o, 'sessionId')) return false;
  if ('lastSeq' in o && !isSafeSeq(o.lastSeq)) return false;
  return true;
}

function isInputFrame(o: Record<string, unknown>): boolean {
  if (!exactKeys(o, ['type', 'requestId', 'id', 'data'])) return false;
  if (!reqStr(o, 'requestId')) return false;
  if (!reqStr(o, 'id')) return false;
  return isBase64(o.data);
}

function isResizeFrame(o: Record<string, unknown>): boolean {
  if (!exactKeys(o, ['type', 'requestId', 'cols', 'rows'])) return false;
  if (!reqStr(o, 'requestId')) return false;
  if (typeof o.cols !== 'number' || !Number.isSafeInteger(o.cols) || o.cols < 1) return false;
  if (typeof o.rows !== 'number' || !Number.isSafeInteger(o.rows) || o.rows < 1) return false;
  return true;
}

function isBackfillFrame(o: Record<string, unknown>): boolean {
  if (!exactKeys(o, ['type', 'requestId', 'fromSeq', 'toSeq'])) return false;
  if (!reqStr(o, 'requestId')) return false;
  if (!isReplaySeq(o.fromSeq)) return false;
  if (!isSafeSeq(o.toSeq)) return false;
  return (o.toSeq as number) >= (o.fromSeq as number);
}

function isPingFrame(o: Record<string, unknown>): boolean {
  if (!exactKeys(o, ['type', 'requestId'])) return false;
  return !!reqStr(o, 'requestId');
}
