/**
 * REST v1 DTO types for the remote contract.
 *
 * Design §7: BASE = /api/remote/v1. All endpoints except pairing/complete
 * require a valid device Cookie. Response bodies NEVER carry credential,
 * token, apiKey, RemoteToken or cookie fields — the Cookie is the sole device
 * credential carrier. Pairing code appears ONLY in PairingCompleteRequest.
 */
import type { APIVersion, CLIType, DeviceID, Seq, SessionID, SessionState } from './scalars';

/** PairingCompleteRequest — the only unauthenticated endpoint body. */
export interface PairingCompleteRequest {
  code: string; // one-time pairing material; never logged
  deviceName: string;
}

export interface DeviceSummary {
  id: DeviceID;
  name: string;
  pairedAt: string; // RFC3339 UTC 'Z'
}

export interface CLIAvailability {
  cliType: CLIType;
  available: boolean; // required; false is meaningful (no omitempty)
}

export interface HostSummary {
  apiVersion: APIVersion;
  serverVersion: string;
  cliAvailability: CLIAvailability[]; // one entry per frozen CLI type
}

export interface PairingCompleteResponse {
  device: DeviceSummary;
  host: HostSummary;
}

/**
 * ControlSnapshot — four-variant discriminated union on `state`. deviceName is
 * REQUIRED only when state === 'other' and MUST be absent otherwise.
 */
export type ControlSnapshot =
  | { state: 'other'; deviceName: string }
  | { state: 'none' | 'you' | 'desktop'; deviceName?: never };

export interface SessionSummary {
  id: SessionID;
  title: string;
  cliType: CLIType;
  state: SessionState;
  control: ControlSnapshot;
  lastActivityAt: string; // RFC3339 UTC 'Z'
}

/** SessionDetail extends the summary with authorized-device detail fields. */
export interface SessionDetail extends SessionSummary {
  workdir: string; // lowercase per P6 freeze (NOT workDir)
  startedAt: string; // RFC3339 UTC 'Z'
  earliestSeq: Seq; // required even when 0 (empty-history sentinel)
  latestSeq: Seq; // required even when 0
}

/** CreateSessionRequest — cliType required; workdir optional (host default). */
export interface CreateSessionRequest {
  cliType: CLIType;
  workdir?: string;
}

/**
 * ConfirmActionRequest — confirm MUST be literal true; false/omitted/null is
 * bad_request. Protocol-level intent; does not replace frontend PG-06 confirm.
 */
export interface ConfirmActionRequest {
  confirm: true;
}

// ---------------------------------------------------------------------------
// Production endpoint manifest (addendum §6.1). The sole consumer-importable
// 10-endpoint surface; consumers must NOT copy path strings.
// ---------------------------------------------------------------------------
export type RestMethod = 'GET' | 'POST' | 'DELETE';

export interface RestEndpoint {
  readonly method: RestMethod;
  readonly path: string; // relative to REST_BASE_PATH
  readonly successStatus: 200 | 201 | 204;
}

export const V1_REST_ENDPOINTS = [
  { method: 'POST', path: '/pairing/complete', successStatus: 201 },
  { method: 'GET', path: '/host/summary', successStatus: 200 },
  { method: 'GET', path: '/sessions', successStatus: 200 },
  { method: 'GET', path: '/sessions/{id}', successStatus: 200 },
  { method: 'POST', path: '/sessions', successStatus: 201 },
  { method: 'POST', path: '/sessions/{id}/stop', successStatus: 200 },
  { method: 'POST', path: '/sessions/{id}/restart', successStatus: 200 },
  { method: 'DELETE', path: '/sessions/{id}', successStatus: 204 },
  { method: 'POST', path: '/sessions/{id}/control/acquire', successStatus: 200 },
  { method: 'POST', path: '/sessions/{id}/control/release', successStatus: 200 },
] as const satisfies readonly RestEndpoint[];
