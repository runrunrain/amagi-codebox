# T1-remote-webui-proxy 交付报告

> 切片：amagi-codebox remote server 的 pi webui 反向代理 + 会话 webui 状态端点 + /api/input 控制门（冻结契约 C1/C2）。
> 日期：2026-09-13 · 状态：完成（go vet 全绿 / go test 目标包全绿）

## 一、实现摘要

### C1 反向代理 `/webui/{sessionID}/...`（internal/remote/webui_proxy.go + server.go）

- **挂载点**：`buildHandler()` 顶层分发，在 legacy 命名空间判定与静态 SPA fallback 之前、v1 REST/WS 分发之后（前缀互斥，顺序无歧义）。
- **sid 白名单**：`^[A-Za-z0-9_-]+$`、≤256B（覆盖仓库两种 session ID 产生式：remote 22 字符 base64url、desktop 8 字符 hex）；白名单外一律 404（无 oracle，防穿越；`.`/`..`/编码斜杠天然被拒）。
- **鉴权**：复用 `enforceAuthPolicy(deviceCookie)` —— 与 v1 完全相同的 device cookie 凭据、判定与错误映射（未授权 401、存储不可用 503）。`s.v1sec == nil`（legacy 构造）fail-closed 404。
- **出向改写**（Director）：`Host: 127.0.0.1:{port}`（pi webui 校验 `^127\.0\.0\.1(:\d+)?$`，改写后天然通过）；删除 `Origin`（后端白名单语义：头缺失即合法）；`Authorization: Bearer {token}` **覆盖式注入**（客户端自带值被覆盖；token 未学到时删除客户端值——宁紧勿松）。代理 per-request 构建，始终采纳 tracker 最新 port/token（/resume 会话切换后跟随）。
- **WS upgrade**：`/ws/events` 等经同一 `httputil.ReverseProxy`（Go 原生 upgrade 支持），`Sec-WebSocket-Protocol` 子协议请求/响应双向透传（测试实证往返）。
- **/api/input 控制门**：代理路径命中该会话 `/api/input` 时复用 `ControlGate.SnapshotForDevice`（与 `/ws/v1` input 帧同一 authority，即 control_permit/control_lane 背后的仲裁器）：非当前控制者（含桌面持有、无 runtime）→ 403 `control.forbidden` + actionHint `request-control`。
- **状态映射（语义自定，已写入契约文档）**：`probing` → 503 `service.down`（可重试，就绪窗口内）；`unavailable`/`ended`/`unknown`（及 available 但 port 异常）→ 404 `session.not_found`；后端不可达 → 503 固定文案（不回显内部错误）。`FlushInterval:-1` 无缓冲回传（流式响应）。
- **端口/token 来源**：`internal/webui.Service` per-session tracker（新增 `SessionEndpoint` 访问器，仅 available 返回 port/token），经 AppInterface 扩展由 `app.go a.WebUI` 接线（`a.WebUI == nil` 空操作 → unknown）。

### C2 状态端点 `GET /api/remote/v1/session/{id}/webui`

- **实现方式（与冻结 10 端点清单的关系，重要）**：wire 契约的 10 端点清单被 `contract/wire_test.go` 冻结并与 `mobile/` 共享 fixture 做全量 parity（元素级相等 + 计数==10），fixture 在 mobile/（本切片白名单外）。因此按任务书给定路径的单数 `session` 命名（区别于 `/sessions/*`），把该端点实现为**冻结清单之外的增量扩展路由**：契约符号（`WebUIStatusEndpoint`/`WebUIStatus`/`WebUIPlaneState` 枚举）新增于 `internal/remote/contract/webui.go`，分发在 `routes_v1.go` 的 `dispatchV1WebUIStatusRoute`——中央门序与主清单完全一致（Host → 空 RawQuery → method(405+Allow) → origin(safeBrowserProof) → auth(deviceCookie) → handler），错误映射复用 `writeV1Error`。冻结清单/fixture/`wire_test.go` 零改动（有回归锚 `TestV1WebUIStatus_FrozenManifestUntouched`）。
- **响应形状**：`{state, url?}`——state 为 5 值闭合枚举 `probing|available|unavailable|ended|unknown`（未注册/非 pi 会话 → `unknown`，端点仍 200，状态机语义优先于 404，避免与删除竞态耦合）；`url` 仅 available 时必填，`/webui/{sid}/[/#/t={token}]`，**token 只经 fragment**（不进请求行/日志/错误体，契约层 `ValidateWebUIStatus` 强制无 query/无 scheme）。经 `contract.MarshalRESTResponse` validate-first 出口。
- **状态机推进**：端点每请求调用 `ProbeSessionWebUI`（AppInterface → `a.WebUI.ProbeWebUI`）——与桌面端轮询同一路径，远程创建的 pi 会话在桌面 UI 未轮询时也能被推进到 available；代理路径则用非阻塞 `GetSessionWebUI`（避免每请求 1.5s 探测延迟）。

### AppInterface 扩展 + app.go 接线

- `AppInterface` 新增 `GetSessionWebUI` / `ProbeSessionWebUI`（`SessionWebUIInfo{State,Port,Token}`，仅 available 携带 Port/Token）；`app.go` 实现转发 `a.WebUI`（nil → unknown/false，对称 `removeWebUITracker` 的空操作模式）。包内两个测试 App（b2aSpyApp/websocketTestApp）同步补齐方法。

### 契约文档（docs/developer/remote-api-v1-contract.md）

- 新增 §3.1（状态端点 + 代理面完整规范：鉴权/白名单/出向改写/WS 透传/控制门/状态映射/token 红线）；更新核实状态行与变更记录（1.3.69）。

## 二、改动文件清单

| 文件 | 类型 | 内容 |
|---|---|---|
| `internal/remote/contract/webui.go` | 新增 | `WebUIProxyPathPrefix`/`WebUIPlaneState` 枚举/`WebUIStatus` DTO/`WebUIStatusEndpoint`/`ValidateWebUIStatus` |
| `internal/remote/contract/rest.go` | 修改 | `ValidateRESTResponse` 增加 `WebUIStatus` case（2 行） |
| `internal/remote/webui_proxy.go` | 新增 | 代理 handler + sid 白名单 + input 控制门 + 状态映射 + v1 扩展路由分发与 handler |
| `internal/remote/routes_v1.go` | 修改 | buildV1Handler 两处 `m==nil` 分支接入扩展路由分发（OPTIONS + 主分类） |
| `internal/remote/server.go` | 修改 | buildHandler 挂载 `/webui/` 前缀（先于 legacy/静态 fallback） |
| `internal/remote/app_interface.go` | 修改 | `SessionWebUIInfo` + `GetSessionWebUI`/`ProbeSessionWebUI` |
| `internal/webui/service.go` | 修改 | `SessionEndpoint`（available 时返回 port/token；亦为 Wails 绑定面，暴露度与既有 `Status.URL` fragment 等价） |
| `app.go` | 修改 | AppInterface 实现（转发 a.WebUI，nil 空操作） |
| `internal/remote/webui_proxy_test.go` | 新增 | 12 个用例（见下） |
| `internal/remote/b2a_legacy_guard_test.go` / `websocket_test.go` | 修改 | 测试 App 补齐新接口方法 |
| `docs/developer/remote-api-v1-contract.md` | 修改 | §3.1 + 核实状态 + 变更记录 |

## 三、验证命令与输出证据

```
$ go vet ./...
（无输出，exit 0）

$ go test ./internal/remote/...
ok  	amagi-codebox/internal/remote	27.451s
ok  	amagi-codebox/internal/remote/contract	1.778s

$ go test ./internal/webui/...
ok  	amagi-codebox/internal/webui	1.499s

$ go test . -run 'WebUI|RemoveSession' -count=1   # 根包 App 面回归
ok  	amagi-codebox	0.683s
```

新测试（`internal/remote/webui_proxy_test.go`，httptest 假后端模拟 pi webui——断言 Host 改写/无 Origin/Bearer 覆盖注入，gorilla/websocket 做 upgrade 子协议回显，复用仓库 vendored 依赖）：

| 用例 | 断言 |
|---|---|
| TestWebUIProxy_Unauthorized401 | 无 cookie → 401 `auth.unpaired`，后端零调用，错误体无 token |
| TestWebUIProxy_SessionIDWhitelist | 穿越/空/含点/编码斜杠路径 → 404 后端零调用；合法 sid（`_`/`-`）代理 200 转发 |
| TestWebUIProxy_HeaderRewrite | 后端实测 `Host=127.0.0.1:{port}`、`Origin` 删除、`Authorization=Bearer {token}`（客户端伪造值被覆盖）；`/webui/{sid}/` → 后端 `/`（iframe 入口） |
| TestWebUIProxy_WSUpgradeSubprotocol | WS 经代理 upgrade 成功、客户端/后端两侧子协议均为 `webui`、消息双向回显、upgrade 请求头改写正确 |
| TestWebUIProxy_InputControlGate | 无控制 runtime → 403（后端零调用）；设备 Acquire 控制权后 → 200 放行 |
| TestWebUIProxy_InputControlGate_DesktopHolder | 桌面 TakeDesktop 持有时移动设备 input 仍 403 |
| TestWebUIProxy_StateMapping | probing→503 `service.down`；unavailable/ended/unknown→404；错误体无 token；后端零调用 |
| TestWebUIProxy_LegacyServerFailClosed | legacy NewServer（无安全面）→ 404 fail-closed |
| TestV1WebUIStatus_Shape | available → `{state,url=/webui/{sid}/#/t={token}}` 且过 `ValidateWebUIStatus`；probing → 无 url；未注册 → 200 unknown；端点确实执行了一轮探测 |
| TestV1WebUIStatus_Gates | 405+Allow / 无 cookie 401 / query 400 / bad Host 403 / 未知扩展路径 404 |
| TestV1WebUIStatus_FrozenManifestUntouched | `V1RestEndpoints` 保持 10 条、无 `/session/` 泄入（fixture parity 约束锚） |
| TestWebUIServiceSessionEndpoint | `webui.Service.SessionEndpoint`：未注册 (0,"") → 探测 available 后 返回真实假后端端口+token → Remove 后归零 |

gofmt：新增/修改文件均合规（`gofmt -l` 中仅剩的 4 个文件为改动前既有的历史未格式化文件，stash 前后清单一致，未触碰）。

npm test 基线：本切片纯 Go 改动（未触碰 frontend//mobile/），对 npm 侧零影响；工作区内 mobile 文件为 C4 切片并行改动，未代跑以免把其中间态误归因。

## 四、遗留风险 / Leader 集成注意

1. **mobile TS 契约类型（C4 切片）**：`WebUIStatus`/`WebUIPlaneState` 的 TS 骨架与消费端归 C4；Go 符号已在 `contract/webui.go`，路径/形状以 §3.1 为准（注意端点是单数 `/session/{id}/webui`，url 为相对路径 `/webui/{sid}/#/t={token}`，页面基址改写依赖 amagi-pi v1.0.16 切片的 origin+前缀推导）。
2. **探测驱动力**：远程创建的 pi 会话若移动端停止轮询且桌面 UI 未打开该会话，probing→available 的推进依赖状态端点轮询（与桌面契约 §4.1 同节奏，成本等价）；恶意已配对设备可借轮询触发探测 I/O，受 per-tracker `probeMu` 串行化与 1.5s 单轮超时约束，与桌面端攻击面等价。
3. **token 为空的 available 会话**（legacy 未注入场景）：url 无 fragment、代理出向删除 Authorization——页面在无 capability 校验的旧后端上仍可用；新后端（必带 token）探测期即 401 不会进入该状态，方向 fail-closed。
4. **`webui.Service.SessionEndpoint` 为 Wails 绑定导出方法**：向本地桌面前端暴露 port/token——暴露度与既有 `Status.URL`（fragment 含 token）等价，未突破红线（红线针对日志/query/远程未授权面）。
5. **X-Forwarded-For 透传**：ReverseProxy 默认追加客户端 IP 至出向 `X-Forwarded-For`；pi webui 后端不信任转发头（其 Host/Origin 自校验），无安全影响，如后续要收紧可在 Director 删除。

---

# G2 修复批（2026-09-13，resume T1）

> 依据：`agent-outputs/workflow/remote-webui-plane/G2-review.md`（VERDICT: FIX）。本仓修复 Critical-01-a / Major-02 / Minor-03 / Minor-04 / Info-06 / Info-08，另交付手动浏览器 E2E 工具。G1 已直修的 Origin-null 透传（webui_proxy.go Director + 对应测试）保持不动；amagi-pi 侧修复（transport credentials / server ACACredentials）不属本仓。验证：`go vet ./...` 0 · `go test ./internal/remote/... ./internal/webui/... -count=1` 全绿 · 根包 `go test . -run 'WebUI|RemoveSession'` ok · E2E 工具冒烟（`-tags browser_e2e`，HOLD=2）PASS。

## 逐项 findings → 改动 → 测试证据

### Critical-01-a · /webui 代理面 CORS 预检豁免（鉴权前本地应答）

- **改动**：`internal/remote/webui_proxy.go` 新增 `isWebUIProxyPreflight`（OPTIONS ∧ `Origin: "null"` ∧ 非空 ACRM 三条件同时成立）与 handleWebUIProxy 内豁免分支——不做 device cookie 鉴权，本地应答 204 + `Access-Control-Allow-Origin: null` + `Vary: Origin` + `Access-Control-Allow-Methods: GET, POST, PUT, OPTIONS` + `Access-Control-Allow-Headers: Authorization, Content-Type` + `Access-Control-Allow-Credentials: true`。非预检 OPTIONS（无 ACRM / Origin 非 null）维持既有鉴权路径。分支位于 sid 白名单之后（垃圾路径仍 404 无 oracle）、状态查询之前（未注册会话的预检也正确应答）。
- **方案选择（本地应答 vs 转发后端）**：选择**服务端本地应答**。理由：① 预检按 CORS 规范不携带凭据，代理层是唯一持有 cookie 判定权的一层，鉴权前必须有人应答，转发并不能省掉该分支；② 本地应答把预检头面固定为本面契约（ACAO:null + ACACredentials + Vary），与 amagi-pi 后端 CORS 细节解耦——后端语义虽等价（server.ts OPTIONS 在 bearer 校验前 204），但两仓升级节奏独立，代理统一应答更稳；③ 镜像 v1 中央派发器「OPTIONS 不做 Cookie 鉴权、本地应答」纪律（routes_v1.go），面内一致性最好。
- **测试**：新增 `TestWebUIProxy_PreflightExempt_NoCookie`（Info-08①）——无 cookie、无会话注册的预检 → 204 + 六个 CORS 头逐一断言 + 后端零调用（证明本地应答）；`TestWebUIProxy_PreflightExemptionNotAbusable`（Info-08②）——伪造 ACRM 的 GET、ACRM 齐备但 Origin 非 null、Origin null 但无 ACRM 三类全部 401，后端零调用。

### Major-02 · 控制门写面集合化（agent-interact / draft 设防）

- **改动**：`webuiBackendInputPath` 单常量替换为 `webuiBackendWriteFaces` 集合——`(POST, /api/input)`、`(POST, /api/agent-interact)`、`(PUT, /api/draft)` 三条写面全部过 `webUIProxyControlHeld`（原 `webUIProxyInputAllowed` 更名，语义泛化）；无控制权 → 403 `control.forbidden`（actionHint `request-control`），错误消息改为 "control required for session write"。`GET /api/fs/dirs` 按裁定维持配对设备可读（不进集合），取舍已写入契约文档 §3.1。判定函数仍是 `ControlGate.SnapshotForDevice` 快照（grace 持有者投影为控制者），注释已按 Minor-03 措辞修正「与 /ws/v1 精确活租约+lane 判定同源但为快照投影」。
- **测试**：新增 `TestWebUIProxy_WriteFaces_ControlGate`（Info-08③）——观察者 POST agent-interact / PUT draft 均 403（错误体无 token、携带 no-store、后端零调用）；GET fs/dirs 读面 200 转发；持控制权后 agent-interact/draft 放行（后端计数 1）。既有 `TestWebUIProxy_InputControlGate`（input 门）与 `..._DesktopHolder` 不变仍绿。

### Minor-03 · 契约文档 §3.1 与实现对齐

- **改动**：`docs/developer/remote-api-v1-contract.md` §3.1 代理面条目重写：① 出向 Origin 语义改为「`null`（sandbox opaque iframe）透传、其余删除」并附 G1 理由（opaque 预检依赖后端回 ACAO:null，删头会让后端按命令行请求处理不回 CORS 头）；② 控制门改为「会话写面控制门（G2 集合化）」：写面集合三元组 + 快照判定措辞（SnapshotForDevice，grace 持有者视为控制者，与 /ws/v1 精确租约判定的关系）+ fs/dirs 取舍说明；③ 同步新增预检豁免与缓存纪律条目；变更记录追加 1.3.70 行。

### Minor-04 · /api/* 响应 Cache-Control: no-store

- **改动**：`handleWebUIProxy` 在预检分支之后、鉴权之前对 `target` 前缀 `/api/` 早设 `Cache-Control: no-store`——这些路径上的所有响应（401/403/404/503 错误与后端转发）统一携带（后端 /api 响应不设该头，无重复头风险）；静态资源路径（`/`、`/assets/*`）不设，保留后端自身缓存语义。`writeV1Error` 出口经此早设覆盖（与 v1「派发器早设 no-store」同款机制，非 writeV1Error 自身职责）。
- **测试**：`TestWebUIProxy_WriteFaces_ControlGate`（403 与 fs/dirs 200 两类 /api 响应断言 no-store）+ `TestWebUIProxy_QueryStringPassthrough`（/api 转发响应断言 no-store）。

### Info-06 · reqID 解析失败对齐 v1 fail-closed

- **改动**：`handleWebUIProxy` 中 `resolveRequestID` 失败（仅 crypto/rand 失败可达）不再以 `fallbackRequestID` 继续服务，改为镜像 routes_v1.go 同款语义：回 `X-Request-ID: 0000000000000000` 头 + 503 `service.down`（"security state unavailable"）。

### Info-08 · 测试补强（四项全落地）

| 项 | 用例 | 断言 |
|---|---|---|
| ① 无 cookie 预检 | `TestWebUIProxy_PreflightExempt_NoCookie` | 204 + ACAO:null + Vary:Origin + Allow-Methods/Headers 精确值 + ACAC:true + 后端零调用（Go 层锁死 Critical-01-a） |
| ② 豁免不被滥用 | `TestWebUIProxy_PreflightExemptionNotAbusable` | 伪造 ACRM 的非 OPTIONS / 非 null Origin 的 OPTIONS / 无 ACRM 的 OPTIONS 三类全部 401，后端零调用 |
| ③ 观察者写面 403 | `TestWebUIProxy_WriteFaces_ControlGate` | agent-interact / draft 403（无 token 外泄 + no-store + 后端零调用）；fs/dirs 读面 200；持权放行 |
| ④ 查询串透传 | `TestWebUIProxy_QueryStringPassthrough` | `/api/history?limit=5&since=42` → 后端实测 path=`/api/history`、RawQuery=`limit=5&since=42`（假后端 probe 增记 rawQuery） |

### 新增 · 手动浏览器端到端工具（Leader 复审用）

- **文件**：`internal/remote/webui_proxy_browser_e2e_test.go`（`//go:build browser_e2e`，CI 不编译不运行；`go vet -tags browser_e2e ./internal/remote/` 通过）。
- **行为**：起完整安全面 Server（fixture 模式，真实监听 `0.0.0.0:8644`，`WEBUI_E2E_PORT` 可换），后端目标支持 `WEBUI_E2E_BACKEND_PORT`/`WEBUI_E2E_BACKEND_TOKEN` 注入真实 pi webui server（缺省起内置 fake backend）；真监听器上完成配对，打印 device cookie 值、平面入口 URL 与三条 curl/浏览器验证配方（预检豁免 / cookie+入口 / 观察者写面 403），保持 `WEBUI_E2E_HOLD` 秒（默认 90）供外部浏览器/真机访问；hold 结束做最小自检（代理入口 200 + 预检 204+CORS 头）后退出，自检失败即测试失败。
- **冒烟证据**：`WEBUI_E2E_HOLD=2 WEBUI_E2E_PORT=18644 go test ./internal/remote/ -run TestWebUIProxyBrowserE2E -tags browser_e2e -v` → PASS（配方打印完整、自检通过）。

## 本批改动文件清单

| 文件 | 类型 | 内容 |
|---|---|---|
| `internal/remote/webui_proxy.go` | 修改 | 预检豁免分支 + `isWebUIProxyPreflight`；写面集合 `webuiBackendWriteFaces` + `webUIProxyControlHeld` 更名；/api/* no-store 早设；reqID fail-closed 503；文件头/注释同步 |
| `internal/remote/webui_proxy_test.go` | 修改 | 假后端增 agent-interact/draft/fs/dirs 路由与 rawQuery/interactCalls 记录；新增 4 个用例（上表） |
| `internal/remote/webui_proxy_browser_e2e_test.go` | 新增 | 手动浏览器 E2E 工具（build tag browser_e2e） |
| `docs/developer/remote-api-v1-contract.md` | 修改 | §3.1 三处修订（Origin 语义/控制门写面集合+grace 措辞/fs-dirs 取舍）+ 预检豁免与缓存纪律条目 + 变更记录 1.3.70 |

## 本批验证证据

```
$ go vet ./...                                          → 0（另 go vet -tags browser_e2e ./internal/remote/ → 0）
$ go test ./internal/remote/... ./internal/webui/... -count=1
ok  	amagi-codebox/internal/remote          26.794s   （13 个 proxy 用例 + 既有全量）
ok  	amagi-codebox/internal/remote/contract  0.496s
ok  	amagi-codebox/internal/webui            1.618s
$ go test . -run 'WebUI|RemoveSession' -count=1          → ok（根包回归）
$ WEBUI_E2E_HOLD=2 ... -tags browser_e2e -v              → PASS（工具冒烟）
```

## 本批遗留 / 交 Leader

- Critical-01 的另两处协同修复在 amagi-pi 仓（transport.ts/draft.ts `credentials:'include'`；server.ts `origin==="null"` 时补 `access-control-allow-credentials: true`）——本批未触碰，需 amagi-pi 侧切片落地后用上述 E2E 工具（`WEBUI_E2E_BACKEND_PORT/TOKEN` 指向真实 pi webui server）做带真实鉴权面的浏览器端到端复审。
- 契约文档已声明预检「不转发后端」的取舍；若 amagi-pi 侧后续调整后端预检行为，本面不受影响（本地应答为准）。
