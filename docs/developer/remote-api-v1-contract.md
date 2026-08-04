# Remote API v1 Contract

> Normative wire document for the Amagi CodeBox remote REST/WS v1 API.
>
- Design source: `agent-outputs/fuxi/20260802-m0-03-contract-design/design.md` (§6–§9 are the wire truth).
> Machine-readable wire examples: `mobile/src/lib/contract/testdata/v1-wire-fixtures.json` (single shared fixture consumed by both the Go and TS tests).
> Type skeletons: `internal/remote/contract/` (Go) and `mobile/src/lib/contract/` (TS, sole export surface `index.ts`).
>
> Any wire change MUST update this document, both type skeletons, the shared fixture and both tests in the SAME diff.

## 1. Scope and stage boundaries

| Stage | Allowed | Forbidden |
| --- | --- | --- |
| **M0** (this document + type skeletons + fixture + pure marshal/parse/type tests) | constants, DTOs, TS discriminated unions, fixture, constants/type parity tests | NO route registration, NO HTTP/WS server start, NO auth/device/session/control/history, NO sample data in production, NO Pinia store, NO mock |
| **M1** | `pairing/complete`, `host/summary`, device Cookie, Origin allowlist + D-004 (empty Origin rejected), revoke closes WS, legacy sensitive admin-face tightening + migration | NO fake session/control/backfill; pairing window params freeze at C-003, Cookie name/TTL at C-008 |
| **M2** | session list/detail/create/stop/restart/delete real adapter; `session.state`/restart boundary; basic output producer | **Control authorization dependency**: M3-A control arbitration MUST run BEFORE M2 lifecycle/input/resize write operations. Do NOT open remote write routes with "any paired device may write" as a temporary relaxation. If control authorization is not yet ready, the affected remote write routes stay closed. |
| **M3** | control acquire/release, input/resize authoritative filtering, session-level seq, earliest/latest, gap/backfill, same-connection input ID dedup, fast reconnect | NO v1 field renames; thresholds freeze at C-004/C-005 by measurement, not hardcoded in M0 |

## 2. Wire basics

- REST body and WS application frames are UTF-8 JSON; WS sends only text messages.
- Fields are lowerCamelCase; the frozen `workdir` stays all-lowercase (not `workDir`).
- **R** (required): present, never null. **O** (optional): omitted when N/A, never null when present. **C** (conditional): required when the condition holds, otherwise omitted.
- **v1 has NO nullable fields.** `null`, missing-required, unknown client field, wrong type ⇒ `bad_request`.
- Required arrays are never null; empty encodes as `[]`.
- Known server events may add optional fields; clients ignore unrecognized fields.

### 2.1 Time, ID, seq, encoding

| Type | Wire | Rule |
| --- | --- | --- |
| `APIVersion` | string | Only `"v1"`; case-sensitive. |
| `RequestID` | string | Non-empty opaque. REST header `X-Request-ID`; WS top-level `requestId`. Never carries credentials. Client may provide; server generates+echoes if absent. |
| `SessionID` / `DeviceID` | string | Non-empty opaque; clients MUST NOT parse as UUID/prefix. |
| `MessageID` | string | `input.id`; unique within one WS connection; repeated id is silently dropped (no re-write). |
| time | string | Server UTC, RFC3339Nano, `Z`; clients parse by ISO-8601. |
| `Seq` | integer / TS `number` / Go `uint64` | `0`–`9007199254740991`; 0 = empty-history sentinel; real replay frames start at 1. Server guarantees JS safe integer before encoding. |
| `chunk` / `input.data` | string | RFC 4648 standard padded Base64. Empty input forbidden; empty output produces no frame. |
| `cols`/`rows` | integer | Positive; ceiling is M3 C-005. |

### 2.2 Request correlation and version negotiation

- REST: client may send `X-Request-ID`; every response (success/error) echoes it; REST error body carries the same `requestId`.
- WS: every client frame has required `requestId`. `session.attached` and `backfill.result` echo it; command-derived `error` echoes when possible; broadcast events usually omit it.
- `input.id` is NOT `requestId`; `requestId` has no idempotency semantics.
- HTTP version is selected by `/api/remote/v1`; `HostSummary.apiVersion` = `v1`.
- WS attach carries `apiVersion:"v1"`, echoed by attached. Missing/mismatch ⇒ `error{code:"bad_request", details:{reason:"unsupported_api_version", receivedApiVersion?, supportedApiVersions:["v1"]}}` then WS close 1002.
- `serverVersion` is the host app version only; it does NOT participate in API compatibility. v1 adds no `clientVersion`/`minClientVersion`.

### 2.3 Unknown / enum compatibility

- TS distinguishes `KnownServerEvent` (7 frozen categories), `UnknownServerEvent` (sanitized: `{type:'unknown', wireType, reason, fallback, metadata}` — NO `raw`, field values or unknown field names), `DecodedServerEvent = KnownServerEvent | UnknownServerEvent`.
- Unknown server `type`: keep connection, ignore business update, record sanitized diagnostic; NEVER throw/terminate.
- Known event unknown optional field: ignored.
- Unknown auth/control state ⇒ safe read-only/unauthorized fallback; unknown history state ⇒ render as possible gap; unknown session state ⇒ render as `unavailable`.
- Unknown client frame / unknown client field ⇒ `bad_request`.
- Removing/renaming required fields, changing meaning, changing paths ⇒ v2. Adding server optional fields / new server events is allowed within v1.

## 3. REST v1

`BASE = /api/remote/v1`. All endpoints except `pairing/complete` require a valid device Cookie.

| Method + path | Auth | Request | Success | Stage |
| --- | --- | --- | --- | --- |
| `POST /pairing/complete` | none (valid one-time window) | `PairingCompleteRequest` | `201 PairingCompleteResponse` + Set-Cookie | M1 |
| `GET /host/summary` | device Cookie | — | `200 HostSummary` | M1 |
| `GET /sessions` | device Cookie | — | `200 SessionSummary[]` (`[]` when empty) | M2 |
| `GET /sessions/{id}` | device Cookie | — | `200 SessionDetail` | M2 |
| `POST /sessions` | device Cookie | `CreateSessionRequest` | `201 SessionDetail` | M2 |
| `POST /sessions/{id}/stop` | current controller | `ConfirmActionRequest` | `200 SessionDetail` | M2 + control dep |
| `POST /sessions/{id}/restart` | current controller | `ConfirmActionRequest` | `200 SessionDetail`; boundary via WS | M2 + control dep |
| `DELETE /sessions/{id}` | current controller | `ConfirmActionRequest` | `204` no body; then `removed` broadcast | M2 + control dep |
| `POST /sessions/{id}/control/acquire` | device Cookie | no body | `200 ControlSnapshot`; busy ⇒ 409 | M3 |
| `POST /sessions/{id}/control/release` | current controller | no body | `200 ControlSnapshot{state:"none"}` | M3 |

`{id}` is a single URL path segment; clients percent-encode, server decodes to opaque SessionID. See the fixture `manifest.restEndpoints` for the canonical 10-endpoint enumeration.

Key DTO invariants:
- `ConfirmActionRequest.confirm` MUST be literal `true`.
- `ControlSnapshot` is a 4-variant union on `state`; `deviceName` present ONLY when `state="other"`.
- `SessionDetail.earliestSeq`/`latestSeq` required even when 0.
- Response bodies NEVER carry `credential`/`token`/`apiKey`/`remoteToken`/`cookie`. Pairing `code` appears ONLY in `PairingCompleteRequest`.

## 4. WebSocket v1

- Sole URL: `ws[s]://<host>/ws/v1`. URL MUST NOT carry token/session/mode/credential. Browser auto-sends the device Cookie; Origin MUST be non-empty and allowlisted (empty Origin ⇒ HTTP 403 before upgrade, D-004).
- One session per connection; switching session opens a new connection. First business frame MUST be `attach` (ping is a liveness hint, not authorization).
- `auth.revoked` ⇒ event then close 1008 (`AuthRevokedCloseCode`; CG-01 canonical reason `device_revoked`); version/protocol mismatch ⇒ close 1002; oversize frame ⇒ close 1009.

### 4.1 Client frames (all: required non-null `requestId`)

- `attach`: `{type, requestId, apiVersion, sessionId, lastSeq?}`. Re-attach after attached ⇒ `bad_request`.
- `input`: `{type, requestId, id, data}`. Controller only; observer gets `control.forbidden`; no JSON ACK; repeated `id` silently dropped.
- `resize`: `{type, requestId, cols, rows}`. Controller only; no JSON ACK.
- `backfill`: `{type, requestId, fromSeq, toSeq}`. Closed range, `1 <= fromSeq <= toSeq`; one correlated `backfill.result`.
- `ping`: `{type, requestId}`. No payload/ACK; cannot substitute for attach/auth.

### 4.2 Server events (7 categories)

- `session.attached`: `{type, requestId, apiVersion, sessionId, history:ReplayFrame[], earliestSeq, latestSeq, snapshot:FiveLayerSnapshot}`. `requestId` = attach `requestId`; at attach time connection=`connected`, auth=`authorized` only. `snapshot.history` is a conditional union (§4.4): `gap` state carries a nested `gap:GapRange`; `continuous`/`backfilled` forbid it.
- `output` (replayable): `{type, sessionId, seq, chunk, structuredExpected?}`.
- `backfill.result` (frames): `{type, requestId, sessionId, fromSeq, toSeq, earliestSeq, latestSeq, frames}`.
- `backfill.result` (gap): `{..., gap:{code:"history.gap", fromSeq, toSeq}}`. Exactly one of `frames`/`gap`.
- `session.state` (normal): `{type, sessionId, state, occurredAt}` — no seq, not replayable.
- `session.state` (restart boundary): `{type, sessionId, state, restartBoundary:true, seq, occurredAt}` — replayable; no separate `session.restartBoundary` type.
- `control.state`: `{type, sessionId, state, deviceName?, reason, occurredAt}`. `deviceName` only when `state="other"`.
- `auth.revoked`: `{type, reason, occurredAt}`. `reason` is a **closed enum** (CG-01): v1 known = `device_revoked` only (`AuthRevokedReasonDeviceRevoked` / `AUTH_REVOKED_REASON_DEVICE_REVOKED`). The event precedes a close **1008** (`AuthRevokedCloseCode` / `AUTH_REVOKED_CLOSE_CODE`). Unknown/missing/null/malformed `reason` MUST be treated as **force-unauthorized** (fail-closed): the client revokes on the event *type*, never on the reason value. 1008 is a generic policy code (not a reason) — other policy closes may also use 1008, so do not infer device-revoke from the close code alone; only the `auth.revoked` event or a subsequent 401 changes persistent authorization presentation.
- `error`: `{type, requestId?, sessionId?, code, layer, message, actionHint, details?}`. `requestId` conditional.

### 4.3 seq / earliest / latest / gap / backfill invariants

1. seq scope is a single session lifetime (not a connection); reconnects/concurrent observers see identical seq for the same frame.
2. Same-entry restart keeps history; cursor continues; the restart boundary occupies a seq.
3. `earliestSeq` = lowest retained; `latestSeq` = highest produced. Empty history ⇒ both 0; else `1 <= earliestSeq <= latestSeq`.
4. attach atomically acquires history/window/snapshot and registers the live producer (no snapshot/live gap).
5. attach with `lastSeq`: `> latestSeq` ⇒ `bad_request`; `== latestSeq` ⇒ `[]`, continuous; `+1 >= earliestSeq` ⇒ retained frames with `seq > lastSeq`; `+1 < earliestSeq` ⇒ frames from earliest + gap `[lastSeq+1, earliestSeq-1]`.
6. backfill closed range; `> latestSeq` ⇒ `bad_request`; fully retained ⇒ frames variant; start before earliest ⇒ gap variant (no mixing).

### 4.4 `snapshot.history` conditional union (attached gap)

`HistorySnapshot` is a discriminated union on `state` (addendum §1.2). `gap` MUST carry a nested `GapRange`; `continuous`/`backfilled` MUST omit it. v1 has no sibling/event-level gap alias.

| `history.state` | `history.gap` | legality |
| --- | --- | --- |
| `continuous` | omitted | valid |
| `backfilled` | omitted | valid |
| `gap` | non-null `GapRange` | valid |
| `continuous`/`backfilled` | present (incl. null) | invalid |
| `gap` | missing or null | invalid |
| unknown/null state | any | invalid known event |

`GapRange` = `{code:"history.gap", fromSeq, toSeq}` with `1 <= fromSeq <= toSeq <= 2^53-1`. In `session.attached` the gap also satisfies `gap.toSeq + 1 == earliestSeq`.

Attached-gap example (fixture `serverEvents.sessionAttachedGap`):

```json
{
  "type": "session.attached", "requestId": "req_attach_gap", "apiVersion": "v1",
  "sessionId": "sess_opaque_1",
  "history": [
    {"type":"output","sessionId":"sess_opaque_1","seq":41,"chunk":"YQ=="},
    {"type":"output","sessionId":"sess_opaque_1","seq":42,"chunk":"Yg=="}
  ],
  "earliestSeq": 41, "latestSeq": 42,
  "snapshot": {
    "connection": {"state":"connected"}, "auth": {"state":"authorized"},
    "session": {"state":"running"}, "control": {"state":"none"},
    "history": {"state":"gap", "gap":{"code":"history.gap","fromSeq":11,"toSeq":40}}
  }
}
```

## 5. Errors

Unified REST error body (top-level, NO `{error:{...}}` envelope):
`{requestId, code, layer, message, actionHint, details?}`.

12 stable codes: `net.unreachable`, `service.down`, `auth.unpaired`, `auth.window_expired`, `auth.revoked`, `session.not_found`, `session.launch_failed` (+`details.cliType`), `control.busy`, `control.forbidden`, `history.gap`, `rate.limited`, `bad_request`. Version mismatch = `bad_request` + `details.reason:"unsupported_api_version"` (no new code). `net.unreachable` is client-synthesized (no server requestId). HTTP status is transport info; clients parse the classified body first.

## 6. Security invariants

1. All REST/WS except `pairing/complete` require a valid device Cookie; "has Cookie" ≠ "may write" — lifecycle/input/resize also require current control.
2. Cookie/device credential/API Key/RemoteToken/pairing code NEVER enter JSON response, WS frame, localStorage, IndexedDB, Cache Storage, URL, log or request details.
3. WS Origin MUST be non-empty and allowlisted; empty Origin fails closed (D-004). (Current `auth.go` behavior MUST be fixed in M1; v1 does not reuse it.)
4. On revoke: REST 401 `auth.revoked`; existing WS sends event then close 1008; control released.
5. `requestId`/`messageId`/`sessionId`/`deviceName` are NOT authorization basis; server resolves device identity from Cookie/connection context.
6. Unknown control/auth enum ⇒ read-only/unauthorized fallback only; never optimistic enable.
7. Message/frame bodies, terminal chunks, input data are never logged; error message never concatenates raw Go error/credential.
8. Base64 decode failure, non-integer seq/size, unknown client field ⇒ reject; dirty values never reach the PTY.
9. `structuredExpected` is a hint; absence/false does not affect raw output visibility; structured failure falls back to MonoBlock/diagnostic, never drops a chunk.
10. M0 forbids fake handler/store/mock; fixed samples exist only in testdata and cannot be returned by production code.

## 7. Production validation boundary (honest M0 scope)

The contract package provides PURE validation functions callable from tests and future v1 producers/consumers — but **no handler, WebSocket consumer, store or route is wired in M0**. The runtime enforcement boundary is the contract package only; M1/M2/M3 producers MUST call these functions rather than `json.Marshal`/`json.Unmarshal` on raw DTOs.

- **Go production API** (`internal/remote/contract`): `DecodePairingCompleteRequest`/`DecodeCreateSessionRequest`/`DecodeConfirmActionRequest` (REST ingress); `DecodeClientFrame`/`DecodeKnownServerEvent` (WS ingress); `ValidateRESTResponse`/`ValidateAPIError`/`ValidateServerEvent` + `Marshal*` (egress). Decoders check JSON object-ness, presence, null, unknown field (strict for client/request; additive allowed for known server events), trailing JSON, type errors, closed enums, conditional/XOR, safe seq and Base64. Marshals validate first and produce no bytes on failure.
- **Required slices reject nil** (addendum §5.4): `HostSummary.CLIAvailability` (must be exactly 4 unique CLI types), `SessionList` (non-nil empty → `[]`), `SessionAttachedEvent.History` (non-nil empty ok), `BackfillFramesResultEvent.Frames` (non-nil non-empty). Producers that forget to initialize a required array get an explicit error rather than a silent `null`.
- **TS production runtime** (`mobile/src/lib/contract/ws.ts`): `normalizeServerEvent(unknown)` and `isClientFrame(unknown)` are pure, never throw, and fully validate untrusted input. Malformed/unknown server messages normalize to a **sanitized** `UnknownServerEvent` (no `raw`, no field values, no unknown field names; only sanitized `wireType`, reason, fallback and boolean shape metadata). Fallback priority: `force-unauthorized` > `force-read-only` > `mark-history-gap` > `mark-session-unavailable` > `ignore`.
- **Honest boundary**: statements like “Go handler uses the strict decoder” or “the TS normalizer is wired into the WebSocket client” are NOT true in M0 — only the contract-layer functions exist and are tested.
