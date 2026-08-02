/**
 * Error types for the remote REST/WS v1 wire contract.
 *
 * The unified REST error body is a TOP-LEVEL object (no {error:{...}}
 * envelope). REST requestId is REQUIRED; the WS error event (ErrorEvent in
 * ws.ts) has a CONDITIONAL requestId and is a separate type.
 *
 * Design §9: 12 stable codes. session.launch_failed is the base code; the
 * dynamic {cliType} travels in details. Version mismatch is bad_request +
 * details, NOT a new code.
 */
import type { ExtensibleString, RequestID } from './scalars';

export type ErrorLayer = 'connection' | 'auth' | 'session' | 'control' | 'history';

/** Complete set of five error layers (closed enum). */
export const KNOWN_ERROR_LAYERS = ['connection', 'auth', 'session', 'control', 'history'] as const;

// --- 12 stable v1 error codes ---
export const ERROR_CODE_NET_UNREACHABLE = 'net.unreachable' as const;
export const ERROR_CODE_SERVICE_DOWN = 'service.down' as const;
export const ERROR_CODE_AUTH_UNPAIRED = 'auth.unpaired' as const;
export const ERROR_CODE_AUTH_WINDOW_EXPIRED = 'auth.window_expired' as const;
export const ERROR_CODE_AUTH_REVOKED = 'auth.revoked' as const;
export const ERROR_CODE_SESSION_NOT_FOUND = 'session.not_found' as const;
export const ERROR_CODE_SESSION_LAUNCH_FAILED = 'session.launch_failed' as const;
export const ERROR_CODE_CONTROL_BUSY = 'control.busy' as const;
export const ERROR_CODE_CONTROL_FORBIDDEN = 'control.forbidden' as const;
export const ERROR_CODE_HISTORY_GAP = 'history.gap' as const;
export const ERROR_CODE_RATE_LIMITED = 'rate.limited' as const;
export const ERROR_CODE_BAD_REQUEST = 'bad_request' as const;

export type KnownErrorCode =
  | typeof ERROR_CODE_NET_UNREACHABLE
  | typeof ERROR_CODE_SERVICE_DOWN
  | typeof ERROR_CODE_AUTH_UNPAIRED
  | typeof ERROR_CODE_AUTH_WINDOW_EXPIRED
  | typeof ERROR_CODE_AUTH_REVOKED
  | typeof ERROR_CODE_SESSION_NOT_FOUND
  | typeof ERROR_CODE_SESSION_LAUNCH_FAILED
  | typeof ERROR_CODE_CONTROL_BUSY
  | typeof ERROR_CODE_CONTROL_FORBIDDEN
  | typeof ERROR_CODE_HISTORY_GAP
  | typeof ERROR_CODE_RATE_LIMITED
  | typeof ERROR_CODE_BAD_REQUEST;

/** Open error code: known literal or forward-compatible opaque string. */
export type ErrorCode = ExtensibleString<KnownErrorCode>;

/** Complete set of 12 stable error codes in canonical order (manifest parity). */
export const KNOWN_ERROR_CODES = [
  ERROR_CODE_NET_UNREACHABLE,
  ERROR_CODE_SERVICE_DOWN,
  ERROR_CODE_AUTH_UNPAIRED,
  ERROR_CODE_AUTH_WINDOW_EXPIRED,
  ERROR_CODE_AUTH_REVOKED,
  ERROR_CODE_SESSION_NOT_FOUND,
  ERROR_CODE_SESSION_LAUNCH_FAILED,
  ERROR_CODE_CONTROL_BUSY,
  ERROR_CODE_CONTROL_FORBIDDEN,
  ERROR_CODE_HISTORY_GAP,
  ERROR_CODE_RATE_LIMITED,
  ERROR_CODE_BAD_REQUEST,
] as const;

// --- Action hints ---
export type KnownActionHint =
  | 'retry'
  | 'check-desktop'
  | 're-pair'
  | 'request-control'
  | 'continue-from-latest'
  | 'upgrade-client';
/** Open action hint: clients show a generic recovery action for unknown values. */
export type ActionHint = ExtensibleString<KnownActionHint>;

/** Complete set of six known action hints (manifest parity; ActionHint is open). */
export const KNOWN_ACTION_HINTS = [
  'retry', 'check-desktop', 're-pair', 'request-control', 'continue-from-latest', 'upgrade-client',
] as const;

/** Structured detail keys used by v1. */
export const DETAIL_KEY_REASON = 'reason' as const;
export const DETAIL_REASON_UNSUPPORTED_API_VERSION = 'unsupported_api_version' as const;
export const DETAIL_KEY_CLI_TYPE = 'cliType' as const;

/**
 * APIError is the unified REST error body. requestId is REQUIRED and equals
 * the X-Request-ID response header. details carries only safe structured data.
 */
export interface ApiError {
  requestId: RequestID;
  code: ErrorCode;
  layer: ErrorLayer;
  message: string;
  actionHint: ActionHint;
  details?: Record<string, unknown>;
}

/** Version-mismatch detail payload (design I-11): bad_request + details. */
export interface UnsupportedAPIVersionDetails {
  reason: typeof DETAIL_REASON_UNSUPPORTED_API_VERSION;
  receivedApiVersion?: string;
  supportedApiVersions: string[];
}
