# Remote API v1 Contract

> Normative wire document for the Amagi CodeBox remote REST/WS v1 API.
>
> Design source: `agent-outputs/fuxi/20260802-m0-03-contract-design/design.md` (§6–§9 are the wire truth).
> Machine-readable wire examples: `mobile/src/lib/contract/testdata/v1-wire-fixtures.json` (single shared fixture consumed by both the Go and TS tests).
> Type skeletons: `internal/remote/contract/` (Go) and `mobile/src/lib/contract/` (TS, sole export surface `index.ts`).
>
> Any wire change MUST update this document, both type skeletons, the shared fixture and both tests in the SAME diff.

**核实状态（2026-08-22，版本 1.3.50；2026-09-13 增补 §3.1）**：本协议已对照 `internal/remote/contract/`（Go）与 `mobile/src/lib/contract/testdata/v1-wire-fixtures.json` 逐条复核。事件类型（8 类，含 `input.ack`）、12 个稳定错误码、关闭码（1008/1002/1009）、5 个 CLI 类型、10 个 REST 端点均与代码及共享 fixture 一致（2026-09 增量面 §3.1 的 webui 状态端点/代理面在冻结清单之外，不受 fixture parity 约束）。**M0–M3 各阶段均已在生产落地**：`routes_v1.go` 的 `registerV1Routes` 在 `sessionAdapter` 接线后注册全部 10 个端点，`ws_v1_session.go` 是 `/ws/v1` 的生产消费者，`input.ack` 由 per-session input ledger 在生产中发出。§1 的阶段表保留为设计约束记录，§7 的实现边界已更新为现状。

## 1. Scope and stage boundaries（设计约束记录，各阶段已落地）

| Stage | Allowed | Forbidden |
| --- | --- | --- |
| **M0**（契约包 + 类型骨架 + fixture + 纯 marshal/parse/type 测试） | constants, DTOs, TS discriminated unions, fixture, constants/type parity tests | NO route registration, NO HTTP/WS server start, NO auth/device/session/control/history, NO sample data in production, NO Pinia store, NO mock |
| **M1** | `pairing/complete`, `host/summary`, device Cookie, Origin allowlist + D-004 (empty Origin rejected), revoke closes WS, legacy sensitive admin-face tightening + migration | NO fake session/control/backfill; pairing window params freeze at C-003, Cookie name/TTL at C-008 |
| **M2** | session list/detail/create/stop/restart/delete real adapter; `session.state`/restart boundary; basic output producer | **Control authorization dependency**: M3-A control arbitration MUST run BEFORE M2 lifecycle/input/resize write operations. |
| **M3** | control acquire/release, input/resize authoritative filtering, session-level seq, earliest/latest, gap/backfill, same-connection input ID dedup, fast reconnect | NO v1 field renames; thresholds freeze at C-004/C-005 by measurement |

**落地现状**（核实自 `internal/remote/routes_v1.go:140` 与 `server.go: buildHandler`）：

- v1 流量在 legacy 认证**之前**分流：`/api/remote/v1` 走独立的 `buildV1Handler` 中央派发器（不复用 legacy `auth.go`），`/ws/v1` 在 `sessionAdapter` 接线后才接受 upgrade。
- 会话路由（端点 2–9）仅在 `sessionAdapter != nil` 时激活，否则保持 404（设计 §4A 硬化门）；生产中 `NewApp` 已接线（`app.go:558-559`）。
- 所有会话写副作用（REST 生命周期、WS input/resize、桌面 PTY 写）共享同一 ControlGate 仲裁（M-005，`bind_manifest_test.go` 冻结）。

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
| `MessageID` | string | `input.id`; the idempotency key for a logical input. CG-03 (`contract-addendum-cg03.md`) upgrades the canonical scope to `(SessionID lifetime, authenticated DeviceID)`: session restart does not reset it, only session remove ends it. The canonical producer format is `msg-v1-` + 32 lowercase hex (39 ASCII bytes, 128 random bits, generated once via CSPRNG), which is also the opt-in discriminator for the per-session input ledger + ACK path. The wire consumer still accepts any legacy non-empty opaque ID (per-connection dedupe + silent success); legacy IDs MUST NOT be suppressed across connections. |
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

- TS distinguishes `KnownServerEvent` (8 frozen categories — CG-03 adds the additive `input.ack`), `UnknownServerEvent` (sanitized: `{type:'unknown', wireType, reason, fallback, metadata}` — NO `raw`, field values or unknown field names), `DecodedServerEvent = KnownServerEvent | UnknownServerEvent`.
- Unknown server `type`: keep connection, ignore business update, record sanitized diagnostic; NEVER throw/terminate.
- Known event unknown optional field: ignored.
- Unknown auth/control state ⇒ safe read-only/unauthorized fallback; unknown history state ⇒ render as possible gap; unknown session state ⇒ render as `unavailable`.
- Unknown client frame / unknown client field ⇒ `bad_request`.
- Removing/renaming required fields, changing meaning, changing paths ⇒ v2. Adding server optional fields / new server events is allowed within v1.

## 3. REST v1

`BASE = /api/remote/v1`（`contract.RESTBasePath`）. All endpoints except `pairing/complete` require a valid device Cookie.

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
| `POST /sessions/{id}/control/acquire` | device Cookie | no body | `200 ControlSnapshot`；另一设备持有 ⇒ 409 `control.busy`；桌面持有 ⇒ 对称接管成功（takeover，单写安全不变） | M3 |
| `POST /sessions/{id}/control/release` | current controller | no body | `200 ControlSnapshot{state:"none"}` | M3 |

`{id}` is a single URL path segment; clients percent-encode, server decodes to opaque SessionID. The canonical 10-endpoint enumeration lives in `contract.V1RestEndpoints`（`internal/remote/contract/version.go`）并镜像在 fixture `manifest.restEndpoints`。

### 3.1 Additive extension: session webui plane（remote-webui-plane，2026-09）

Embedded pi 会话的 Web 平面（结构化工具卡片/思考折叠/输入台）经两个**增量面**暴露，二者均在冻结的 10 端点清单之外（`contract.V1RestEndpoints` 与共享 fixture manifest 保持 10 条不变；符号见 `internal/remote/contract/webui.go`）：

| Method + path | Auth | Request | Success | Notes |
| --- | --- | --- | --- | --- |
| `GET /session/{id}/webui`（单数 `session`，区别于 `/sessions/*`） | device Cookie | — | `200 WebUIStatus` | additive extension endpoint（`contract.WebUIStatusEndpoint`），分发经 `routes_v1.go` 扩展分支，中央门序与主清单一致（Host → query → method(405) → origin → auth） |

`WebUIStatus` = `{state, url?}`，`state` 为 5 值闭合枚举（`KnownWebUIPlaneStates`）：`probing` / `available` / `unavailable` / `ended` / `unknown`（镜像宿主侧 `internal/webui.State`；未注册/非 pi 会话 → `unknown`，端点仍 200，状态机语义优先于 404）。`url` 为条件字段：仅 `available` 时必填且必为同源相对代理路径 `/webui/{sessionId}/[/#/t=<plane-token>]` —— **token 只经 URL fragment 承载**，绝不进请求行/query/日志/错误体；仅下发给已通过 device Cookie 鉴权的设备。**G3：fragment 下发的是代理铸造的 per-(session, device) plane token**（`p`+30hex，绑定当前鉴权设备；同一 (会话, 设备) 在后端 token 未轮换时稳定复用，轮换/平面终态时重铸/回收）——sandbox iframe 永远带不上 device cookie，只有设备绑定 token 才能让写面控制门评估真实控制权归属；raw 后端 capability 不再直接下发（历史已打开平面经兼容只读通道继续可读，见鉴权面）。服务端每请求执行一轮探测（与桌面端轮询同一状态机推进路径）。

#### `/webui/{sessionId}/...` 反向代理面（非 REST 端点）

远程服务器在同一端口顶层分发挂载（先于静态 SPA fallback）的 per-session 反向代理，是 pi webui 平面（仅 bind 127.0.0.1）唯一的外露面：

- **鉴权**：与 v1 完全一致的 device Cookie（同一 authenticator 与错误映射；未授权 401，存储不可用 503）——**仅作用于数据面 `/api/*` 与 `/ws/*`**。**capability 通道（G2 双通道 + G3 设备绑定）**：无 cookie 请求按其自带的 capability 解析——（a）代理铸造的 per-device plane token（Bearer 或 WS 子协议对第二元素；会话与当前后端 token 快照均匹配，且绑定设备仍有效）→ 解析出绑定设备的 principal，写面控制门据此评估真实控制权；（b）raw 后端 token（历史平面兼容，只读零 principal → 写面恒 403）；（c）其它 → 401。**本地错误可见性（G3）**：代理本地生成的 401/403/503/404 在 `Origin: null` 时携带 `Access-Control-Allow-Origin: null` + `Access-Control-Allow-Credentials: true`——否则浏览器会把响应整个掩盖，平面页 fetch 只能报泛化的 network_error（「连接中断，消息未发送」），真实错误码不可见。**静态面豁免（G2 追补，浏览器 E2E 实证）**：平面页面的 `<script crossorigin>`/CSS 子资源以 CORS 模式加载，而跨源 CORS-mode 请求（module script 恒为 CORS 模式）**永不携带 cookie**——若静态面也设 cookie 门，opaque iframe 只能拿到裸 HTML 骨架（JS/CSS 全 401）；故静态面（HTML 入口、`./assets/*`、favicon 等非 `/api/*`、非 `/ws/*` 路径）豁免 device-cookie 鉴权，对齐 amagi-pi webui 契约 §6.3「静态公开、无会话数据」语义；写面全部位于 `/api/*`，控制门始终在鉴权之后。`sessionId` 严格白名单 `^[A-Za-z0-9_-]+$`（≤256B），白名单外一律 404（无 oracle，防路径穿越）。**CORS 预检豁免（G2）**：sandbox opaque iframe 的预检 OPTIONS（`Origin: null` 且非空 `Access-Control-Request-Method`）按 CORS 规范不携带凭据，代理在鉴权前**本地应答** 204 + `Access-Control-Allow-Origin: null` + `Vary: Origin` + `Access-Control-Allow-Methods: GET, POST, PUT, OPTIONS` + `Access-Control-Allow-Headers: Authorization, Content-Type` + `Access-Control-Allow-Credentials: true`（镜像 v1 中央派发器「OPTIONS 不做 Cookie 鉴权」纪律；不转发后端——本面固定头面，不依赖 amagi-pi 后端 CORS 细节）。非预检 OPTIONS（无 ACRM / Origin 非 null）维持鉴权路径。
- **出向改写**：`Host: 127.0.0.1:{port}`（后端校验 `^127\.0\.0\.1(:\d+)?$`）；**`Origin` 按值分流：`null`（sandbox opaque iframe）透传、其余删除**（G1 集成验证实证：opaque 页面的预检/跨源请求依赖后端对 `Origin: null` 回 `Access-Control-Allow-Origin: null`，删头会让后端按命令行请求处理、不回 CORS 头，预检失败后实际数据请求发不出去；顶层同源 fetch 不需要 CORS 头，删除后走后端「头缺失即合法」语义）；覆盖式注入 `Authorization: Bearer {capability token}`（token 未学到时删除客户端自带值——宁紧勿松）。WS upgrade（`/ws/events` 等）经同一代理：客户端 offered `Sec-WebSocket-Protocol: webui, <token>` 统一**换写为 `webui, {后端 token}`**（G3：客户端 offer 的可能是代理铸造的 plane token，后端按自身 token 校验子协议对；后端回显仅 `webui`，客户端也 offer 过，浏览器侧握手不受影响）；查询串原样透传。
- **会话写面控制门（G2 集合化）**：平面写面集合 `POST /api/input`、`POST /api/agent-interact`（resume/background 派生新任务，放果等同输入注入）、`PUT /api/draft` 均按 `ControlGate.SnapshotForDevice` **快照判定**：非当前控制者 → 403 `control.forbidden`（actionHint `request-control`）。与 `/ws/v1` input 帧的精确活租约 + lane 判定（`DoDevicePTY`）同源但为快照投影——**grace 持有者视为控制者**（合理放宽，见 control_event.go）。取舍（diting 裁定）：`GET /api/fs/dirs`（宿主机目录名枚举，读面、暴露度低）维持配对设备可读，不设控制门。
- **缓存纪律（G2）**：`/api/*` 路径响应统一携带 `Cache-Control: no-store`（与 v1 一致，含本面错误响应）；静态资源路径保留后端自身缓存语义。
- **状态映射**：`probing` → 503 `service.down`（可重试，平面仍在就绪窗口内）；`unavailable` / `ended` / `unknown` → 404 `session.not_found`（该会话平面不存在/已清亡）；后端不可达 → 503（固定文案，不回显内部错误）。
- **安全红线**：webui capability token 视同会话权限——不写日志、不进 query/path、错误体不带；代理是它唯一外露面。

Key DTO invariants:
- `ConfirmActionRequest.confirm` MUST be literal `true`.
- `GET /sessions` 列表语义（与桌面端一致，2026-10）：仅返回「打开未关闭」的会话（lifecycle 态 running/stopping；stopped/exited/unavailable 的关闭会话不再出现在列表——关闭即从大厅消失，残留即 bug）；排序固定为 `startedAt` 降序（与桌面端会话列表一致），`sessionId` 升序作并列打破——`startedAt` 不可变，跨轮询稳定，不随活动时间重排。`GET /sessions/{id}` 详情端点不变：关闭会话在被移除（tombstone）前仍可单查。
- `HostSummary.launchSettings` 是可选的增量字段，仅向已配对设备暴露工作目录、Shell、provider/preset/model 的稳定引用与开关；不含 URL、环境变量或密钥。
- `GET /host/summary` 在宿主 CLI 探测失败时返回保守降级体而非 503（2026-09 P2-B）：仍为 `200 HostSummary`，但全部已知 CLI `available:false`、`serverVersion:"unknown"`、`launchSettings` 省略——降级方向 fail-closed（探测不到的 CLI 绝不报可启动），保证已配对设备的会话读面（`/sessions*`，本就不依赖该探测）不被探测失败阻断。`pairing/complete` 在同一失败下保持 503 `service.down`（fail-closed，未配对设备无需保护的会话面）。
- `CreateSessionRequest` 除 `cliType`/`workdir` 外可携带 `providerRef`、`presetRef`、`modelRef`、`shellRef`、`useHeadroom`，宿主必须用本地配置与密钥存储解析这些引用。
- `ControlSnapshot` is a 4-variant union on `state`; `deviceName` present ONLY when `state="other"`.
- `SessionDetail.earliestSeq`/`latestSeq` required even when 0.
- Response bodies NEVER carry `credential`/`token`/`apiKey`/`remoteToken`/`cookie`. Pairing `code` appears ONLY in `PairingCompleteRequest`.
- `cliType` 的闭合枚举是 **5 个**：`claudecode` / `opencode` / `codex` / `pi` / `omp`（`contract.KnownCLITypes`，与 `internal/session/types.go` 的五种 AppType 一一对应）。

## 4. WebSocket v1

- Sole URL: `ws[s]://<host>/ws/v1`（`contract.WebSocketV1Path`）. URL MUST NOT carry token/session/mode/credential. Browser auto-sends the device Cookie; Origin MUST be non-empty and allowlisted (empty Origin ⇒ HTTP 403 before upgrade, D-004).
- One session per connection; switching session opens a new connection. First business frame MUST be `attach` (ping is a liveness hint, not authorization).
- `auth.revoked` ⇒ event then close 1008 (`AuthRevokedCloseCode`; CG-01 canonical reason `device_revoked`); version/protocol mismatch ⇒ close 1002; oversize frame ⇒ close 1009.

### 4.1 Client frames (all: required non-null `requestId`)

- `attach`: `{type, requestId, apiVersion, sessionId, lastSeq?}`. Re-attach after attached ⇒ `bad_request`.
- `input`: `{type, requestId, id, data}`. Controller only; observer gets `control.forbidden`. CG-03: a new producer generates the canonical `id` (`msg-v1-` + 32 lowercase hex, 128-bit CSPRNG) once and binds it to immutable base64 `data` before the outbox accepts the entry; retries reuse the same `id` and append a fresh canonical `requestId` per attempt. Only canonical IDs opt into the per-session input ledger + ACK confirmation; legacy non-empty opaque `id`s keep the per-connection dedupe + silent-success path. No success ACK for legacy IDs.
- `resize`: `{type, requestId, cols, rows}`. Controller only; no JSON ACK.
- `backfill`: `{type, requestId, fromSeq, toSeq}`. Closed range, `1 <= fromSeq <= toSeq`; one correlated `backfill.result`.
- `ping`: `{type, requestId}`. No payload/ACK; cannot substitute for attach/auth.

### 4.2 Server events (8 categories)

Frozen set（`contract.KnownServerEventTypes`，核实 2026-08-22）: `session.attached`, `output`, `backfill.result`, `session.state`, `control.state`, `auth.revoked`, `error`, `input.ack`.

- `session.attached`: `{type, requestId, apiVersion, sessionId, history:ReplayFrame[], earliestSeq, latestSeq, snapshot:FiveLayerSnapshot, inputAckMode?}`. `requestId` = attach `requestId`; at attach time connection=`connected`, auth=`authorized` only. `snapshot.history` is a conditional union (§4.4): `gap` state carries a nested `gap:GapRange`; `continuous`/`backfilled` forbid it. `inputAckMode` (CG-03) is optional: absent = input-ack capability unavailable (read-only for new clients); present = the sole canonical value `session-window-v1`（`contract.InputAckModeSessionWindowV1`）. An unknown value is treated as absent (forward-compat, read projection preserved). A new client MUST NOT send input when the mode is absent/unknown.
- `output` (replayable): `{type, sessionId, seq, chunk, structuredExpected?}`.
- `backfill.result` (frames): `{type, requestId, sessionId, fromSeq, toSeq, earliestSeq, latestSeq, frames}`.
- `backfill.result` (gap): `{..., gap:{code:"history.gap", fromSeq, toSeq}}`. Exactly one of `frames`/`gap`. v1 has **no partial-gap representation**: the gap MUST cover the full requested range (`gap.fromSeq == fromSeq && gap.toSeq == toSeq`, enforced by the production validator).
- `session.state` (normal): `{type, sessionId, state, occurredAt}` — no seq, not replayable.
- `session.state` (restart boundary): `{type, sessionId, state, restartBoundary:true, seq, occurredAt}` — replayable; no separate `session.restartBoundary` type.
- `control.state`: `{type, sessionId, state, deviceName?, reason, occurredAt}`. `deviceName` only when `state="other"`.
- `auth.revoked`: `{type, reason, occurredAt}`. `reason` is a **closed enum** (CG-01): v1 known = `device_revoked` only (`AuthRevokedReasonDeviceRevoked` / `AUTH_REVOKED_REASON_DEVICE_REVOKED`). The event precedes a close **1008** (`AuthRevokedCloseCode` / `AUTH_REVOKED_CLOSE_CODE`). Unknown/missing/null/malformed `reason` MUST be treated as **force-unauthorized** (fail-closed): the client revokes on the event *type*, never on the reason value. 1008 is a generic policy code (not a reason) — other policy closes may also use 1008, so do not infer device-revoke from the close code alone; only the `auth.revoked` event or a subsequent 401 changes persistent authorization presentation.
- `error`: `{type, requestId?, sessionId?, code, layer, message, actionHint, details?}`. `requestId` conditional.
- `input.ack` (CG-03, additive 8th event): `{type, requestId, sessionId, id}`. Confirms a canonical input `MessageID` was committed by the per-session ledger — exactly-once raw PTY write, or a duplicate of an already-committed `(DeviceID, MessageID)` key (re-ACK). Carries NO seq/status/time/data/details; its sole meaning is "the key is committed". It is never broadcast, never enters replay history/backfill, and is delivered only to the requesting connection's sole outbound writer. `requestId` equals the triggering wire attempt; `id` is the stable canonical `MessageID`. Only canonical IDs (`msg-v1-` + 32 hex) produce an ACK; legacy non-empty opaque IDs keep the per-connection dedupe + silent-success path and produce NO ACK. A malformed ACK is sanitized to Unknown + force-read-only (no settlement, no raw ID retained).

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

12 stable codes（`contract.KnownErrorCodes`，核实 2026-08-22）: `net.unreachable`, `service.down`, `auth.unpaired`, `auth.window_expired`, `auth.revoked`, `session.not_found`, `session.launch_failed` (+`details.cliType`), `control.busy`, `control.forbidden`, `history.gap`, `rate.limited`, `bad_request`. Version mismatch = `bad_request` + `details.reason:"unsupported_api_version"` (no new code). `net.unreachable` is client-synthesized (no server requestId). HTTP status is transport info; clients parse the classified body first.

## 6. Security invariants

1. All REST/WS except `pairing/complete` require a valid device Cookie; "has Cookie" ≠ "may write" — lifecycle/input/resize also require current control.
2. Cookie/device credential/API Key/RemoteToken/pairing code NEVER enter JSON response, WS frame, localStorage, IndexedDB, Cache Storage, URL, log or request details.
3. WS Origin MUST be non-empty and allowlisted; empty Origin fails closed (D-004).**（已落地）** v1 不复用 legacy `auth.go`：`buildV1Handler`（`internal/remote/routes_v1.go`）在独立派发器中中央执行严格 Host、空 RawQuery、Origin 策略（`unsafeOriginRequired`/`safeBrowserProof` 按端点分类）与设备 Cookie 认证；legacy 命名空间则由 loopback 守卫保护（非 loopback 认证前即 403）。
4. On revoke: REST 401 `auth.revoked`; existing WS sends event then close 1008; control released.
5. `requestId`/`messageId`/`sessionId`/`deviceName` are NOT authorization basis; server resolves device identity from Cookie/connection context.
6. Unknown control/auth enum ⇒ read-only/unauthorized fallback only; never optimistic enable.
7. Message/frame bodies, terminal chunks, input data are never logged; error message never concatenates raw Go error/credential.
8. Base64 decode failure, non-integer seq/size, unknown client field ⇒ reject; dirty values never reach the PTY.
9. `structuredExpected` is a hint; absence/false does not affect raw output visibility; structured failure falls back to MonoBlock/diagnostic, never drops a chunk.
10. Fixed samples exist only in testdata and cannot be returned by production code.

## 7. Production validation boundary（现状：已接线）

契约包提供**纯校验函数**，由测试与生产 v1 producer/consumer 共同调用。M0 时期「无 handler/route 接线」的边界声明**已不再成立**——当前实现状态（核实 2026-08-22）：

- **REST ingress**：`internal/remote/routes_v1.go` + `session_routes_v1.go` 的 10 个 handler 经 `contract.DecodePairingCompleteRequest`/`DecodeCreateSessionRequest`/`DecodeConfirmActionRequest` 严格解码（JSON object-ness、presence、null、unknown field、trailing JSON、类型、闭合枚举、条件/XOR、安全 seq、Base64）。
- **WS ingress/egress**：`internal/remote/ws_v1_session.go` 是 `/ws/v1` 的生产消费者，客户端帧经 `contract.DecodeClientFrame`，服务端事件经 `Validate*` + `Marshal*`（validate-first，失败不产字节）；`input.ack` 由 per-session `SessionInputLedger`（`server.go: LedgerForSession`）在生产中发出，`session.attached` 携带 `inputAckMode` 声明能力。
- **Required slices reject nil**（addendum §5.4，仍有效）：`HostSummary.CLIAvailability`（必须恰好 **5** 个唯一 CLI 类型，对应 `contract.KnownCLITypes`）、`SessionList`（非 nil 空 → `[]`）、`SessionAttachedEvent.History`（非 nil 空 ok）、`BackfillFramesResultEvent.Frames`（非 nil 非空）。遗漏初始化必需数组的 producer 得到显式错误而非静默 `null`。
- **TS production runtime**（`mobile/src/lib/contract/ws.ts`）：`normalizeServerEvent(unknown)` 与 `isClientFrame(unknown)` 是纯函数、从不抛异常、完整校验不可信输入。Malformed/unknown 服务端消息归一化为**脱敏** `UnknownServerEvent`（无 `raw`、无字段值、无未知字段名）。Fallback 优先级：`force-unauthorized` > `force-read-only` > `mark-history-gap` > `mark-session-unavailable` > `ignore`。
- **桌面端 RemoteClient**（`internal/remoteclient/`）是同一契约的第二消费者：配对、宿主登记、`/ws/v1` 客户端、出站队列与回填均按本契约实现。

新 v1 producer/consumer **必须**调用契约包的 Decode*/Validate*/Marshal* 入口，不得对原始 DTO 直接 `json.Marshal`/`json.Unmarshal`。

## 变更记录

| 日期 | 版本 | 变更 |
|---|---|---|
| 2026-09-22 | 1.3.85（未发布） | 控制权对称接管（无死锁修复）：`control/acquire` 对桌面持有者从 DenyBusy(409) 改为镜像 `TakeDesktop` 的对称接管（reason takeover，旧持有者事件可见，在逾桌面操作被 fence，holderGeneration/controlEpoch 照常推进）——此前桌面内嵌终端 take-first 输入（每次按键 TakeDesktop）叠加桌面持有无空闲超时，一次按键即把远程平面永久锁死（输入 403 + acquire 409 死路，仅桌面手点「收回控制」可解）。修复后最后意图方持有、两侧均可随时重取（桌面：下一次按键；设备：acquire），设备间互斥（DenyBusy）与桌面权威收回（ForceReleaseControl）不变；端点表同步更新。 |
| 2026-09-15 | 1.3.73+（未发布） | G3 修复批（Web 平面输入台「连接中断，消息未发送」）：§3.1 修订——fragment 改发代理铸造的 per-(session, device) plane token（绑定设备；后端 token 轮换自动失效、平面终态回收）；capability 通道升级为三支（plane token → 绑定设备 principal，raw 后端 token → 只读零 principal，其它 → 401），写面控制门据此评估真实控制权（sandbox iframe 无 cookie 场景下输入台首次真正可用）；出向 WS 子协议对统一换写为后端 token；本地错误响应（401/403/503/404）在 `Origin:null` 时携带 ACAO:null+ACAC（真实错误码不再被 CORS 掩盖成 network_error）。 |
| 2026-09-13 | 1.3.70 | G2 修复批（remote-webui-plane）：§3.1 代理面增补——CORS 预检豁免（OPTIONS+`Origin:null`+ACRM 本地应答 204+完整 CORS 头，镜像 v1 OPTIONS 纪律，不转发后端）；出向 `Origin` 语义修订为「null 透传、其余删除」（G1 实证，同步此前文档漂移）；控制门写面集合化 `POST /api/input`+`POST /api/agent-interact`+`PUT /api/draft`（快照判定，grace 持有者视为控制者），`GET /api/fs/dirs` 维持配对可读（diting 裁定）；`/api/*` 响应统一 `Cache-Control: no-store`；reqID 解析失败对齐 v1 fail-closed 503。 |
| 2026-09-13 | 1.3.69 | remote-webui-plane 切片：新增增量面 §3.1 —— `GET /session/{id}/webui` 状态端点（`WebUIStatus{state,url?}`，5 值闭合枚举；token 只经 fragment）与 `/webui/{sid}/...` 反向代理面（device Cookie 鉴权、sid 白名单、出向 Host/Origin/Authorization 改写、WS 子协议透传、`/api/input` 控制门 403、probing→503/其余→404）。两者均在冻结 10 端点清单之外（`contract.V1RestEndpoints`/fixture manifest 不变）；Go 侧符号在 `internal/remote/contract/webui.go`。 |
| 2026-09-10 | 1.3.65+ | P2-B 读面解耦：`GET /host/summary` provider/探测失败从 503 改为保守降级 200（全部 CLI `available:false`，`launchSettings` 省略；降级方向 fail-closed）。wire DTO/错误码/路径无变更；`pairing/complete` 保持 fail-closed 503；会话列表/详情端点从未依赖该探测。Go 骨架/TS 骨架/fixture 无需同步（无 wire 变更），新增 Go 回归锚 `host_summary_degrade_test.go`。 |
| 2026-08-22 | 1.3.50 | 对照 `internal/remote/contract` 复核：协议本体（8 事件、12 错误码、关闭码、seq 不变式）无漂移；修正三处过时表述——§1 阶段表标注已全部落地、§6.3 删除「auth.go 待 M1 修复」并描述现行 v1 派发器、§7 从「M0 未接线」更新为已接线现状，`HostSummary.CLIAvailability` 从 4 个 CLI 类型更正为 5 个（补 `omp`）。 |
