/**
 * Scalar aliases and constants for the remote REST/WS v1 wire contract.
 *
 * Consumers MUST import only from `./index` (the sole public export surface).
 * Do not deep-import these modules.
 *
 * Design §6.2: IDs are opaque to clients (never parse as UUID/prefix). Time is
 * carried as RFC3339 string in DTOs. Seq is a per-session replay cursor.
 */

// --- Opaque ID aliases (lightweight: clients must not parse structure) ---
export type RequestID = string;
export type SessionID = string;
export type DeviceID = string;
/**
 * MessageID is input.id — the idempotency key for a logical input. CG-03
 * (contract-addendum-cg03.md §3) upgrades the canonical scope to (SessionID
 * lifetime, authenticated DeviceID): session restart does not reset it; only
 * session remove ends it. The canonical producer format is "msg-v1-" + 32
 * lowercase hex (39 ASCII bytes, 128 random bits), generated once via
 * crypto.getRandomValues and bound to immutable base64 payload before the
 * outbox accepts the entry; it is also the opt-in discriminator for the session
 * input ledger + ACK path (isCanonicalMessageID). The wire consumer still
 * accepts any legacy non-empty opaque ID (per-connection dedupe + silent
 * success); legacy IDs MUST NOT be suppressed across connections.
 */
export type MessageID = string;

// --- Seq: per-session replay cursor. 0 = "no replay frame yet"; real frames
// start at 1 and increase monotonically. Servers MUST keep seq <= MAX_SAFE_SEQ
// so the value cannot round in a JS number. ---
export type Seq = number;
export const MAX_SAFE_SEQ: Seq = Number.MAX_SAFE_INTEGER; // 9007199254740991

// --- ExtensibleString<T>: base for open enums. Known literal union T plus
// arbitrary string for forward-compatibility (unknown values are handled
// gracefully, never crash). ---
export type ExtensibleString<T extends string> = T | (string & Record<never, never>);

// --- Version / path / header constants ---
export const API_VERSION_V1 = 'v1' as const;
/** The set of API versions; v1 defines only "v1". Future versions extend this union. */
export type APIVersion = typeof API_VERSION_V1;
export const REQUEST_ID_HEADER = 'X-Request-ID' as const;
export const REST_BASE_PATH = '/api/remote/v1' as const;
/** The SOLE v1 WebSocket upgrade path. There is no second "/events" entry point. */
export const WEB_SOCKET_V1_PATH = '/ws/v1' as const;

// --- Known CLI types (five remote-controlled CLIs) ---
export const CLI_TYPE_CLAUDE_CODE = 'claudecode' as const;
export const CLI_TYPE_OPENCODE = 'opencode' as const;
export const CLI_TYPE_CODEX = 'codex' as const;
export const CLI_TYPE_PI = 'pi' as const;
export const CLI_TYPE_OMP = 'omp' as const;
export type CLIType =
  | typeof CLI_TYPE_CLAUDE_CODE
  | typeof CLI_TYPE_OPENCODE
  | typeof CLI_TYPE_CODEX
  | typeof CLI_TYPE_PI
  | typeof CLI_TYPE_OMP;

/** Complete set of five CLI types in canonical order (manifest parity). */
export const KNOWN_CLI_TYPES = [
  CLI_TYPE_CLAUDE_CODE,
  CLI_TYPE_OPENCODE,
  CLI_TYPE_CODEX,
  CLI_TYPE_PI,
  CLI_TYPE_OMP,
] as const;

// --- Session lifecycle states. `removed` is primarily an event state. ---
export type SessionState = 'running' | 'stopped' | 'exited' | 'unavailable' | 'removed';

/** Complete set of five session states. */
export const KNOWN_SESSION_STATES = [
  'running', 'stopped', 'exited', 'unavailable', 'removed',
] as const;

// --- Control states relative to the current device ---
export type ControlState = 'none' | 'you' | 'other' | 'desktop';

/** Complete set of four control states. */
export const KNOWN_CONTROL_STATES = ['none', 'you', 'other', 'desktop'] as const;

// --- History/window states ---
export type HistoryState = 'continuous' | 'backfilled' | 'gap';

/** Complete set of three history states. */
export const KNOWN_HISTORY_STATES = ['continuous', 'backfilled', 'gap'] as const;

// --- Attached-time constrained states (design §8.3) ---
export const ATTACHED_CONNECTION_STATE = 'connected' as const;
export const ATTACHED_AUTH_STATE = 'authorized' as const;
