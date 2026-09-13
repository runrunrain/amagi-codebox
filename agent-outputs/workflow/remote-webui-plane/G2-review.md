# G2 阶段集成审核 — remote-webui-plane（远程 Web 会话平面）

> 审核：谛听（diting）· 2026-09-13 · 范围：两仓未提交工作区 diff（amagi-codebox 16 文件 + amagi-pi 6 文件），对照冻结契约 C1–C4、workflow.md 总验收与 G1 记录。
> 方法：diff 逐文件审读 + 高风险调用链（buildHandler→handleWebUIProxy→ReverseProxy、routes_v1 扩展分发、webui.Service 状态机、mobile 视图链、amagi-pi main.ts/transport/server.ts CORS 面）+ 验证命令复跑。只读，未修改任何实现文件。

---

## Findings

### Critical-01 · 远程 Web 平面 REST 数据链路在真实鉴权面下不可用（G1 端到端绕过了鉴权层，该路径从未被真实验证）

- **位置**：
  - `internal/remote/webui_proxy.go:81` — 代理对所有方法（含 CORS 预检 OPTIONS）先做 device cookie 鉴权，无预检豁免路径，401 前不回任何 CORS 头；
  - `mobile/src/components/workspace/WebPlaneView.vue:112` — `sandbox="allow-scripts allow-forms"`（无 `allow-same-origin`）→ iframe 页面 origin 为 opaque；
  - amagi-pi `webui/src/transport.ts:56-64`、`webui/src/draft.ts:34,49` — 页面 fetch 未设 `credentials: 'include'`（默认 `same-origin`）；
  - amagi-pi `extensions/amagi-core/webui/server.ts:152-158` — `apiCorsHeaders` 无 `access-control-allow-credentials`（全文件 grep 无此头）。
- **触发条件**：任意已配对设备在真实 remote server（T1 代码）上打开 pi/omp 会话的 Web 平面（真机场景即触发，无条件回避）。
- **实际影响**：平面页面的所有 `/api/*` 请求（opaque origin → 跨源请求）逐层失败：
  1. 带 `Authorization` 头触发 CORS 预检；预检 OPTIONS 不携带凭据，且页面 fetch 默认 credentials 为 `same-origin`（跨源请求不附 cookie）→ 代理 `enforceAuthPolicy` 无 cookie → 401 → **预检失败，实际数据请求根本不发出**；
  2. 即便补 `credentials:'include'`，后端 CORS 响应缺 `Access-Control-Allow-Credentials: true` → 响应被浏览器拦截。
  页面启动链 `probeReady`（amagi-pi `http-source.ts:208-245`）连续失败 2 次即 `setState('ended', unreachable)` → 平面呈「会话已结束/不可达」空壳；WS 因 `/api/info` 前置门槛同样永不建立。**阶段总验收第 1 条（远程默认进入 Web 平面、历史/事件流/输入台可用）在真实设备上不可达**。WS 握手本身（默认带凭据 + SameSite=Strict 同站可附 cookie）本可工作，但被 info 门槛挡住。失效方向 fail-closed，无安全外泄。
- **证据**：桌面端同样 sandbox 的页面能工作，恰因直连后端对预检不鉴权（server.ts:613-618 OPTIONS 在 `hasBearerCapability` 之前直接 204）且无需 cookie；G1 记录明言「**临时子路径反代（等价 T1 Director）**」——只复刻出向改写、**不含 device cookie 鉴权**，故「预检 204 → /api/info 200」不能外推到真实 T1 面。G1 观察到“必须 ACAO:null（opaque 页面跨源）”本身即证明这些 fetch 是跨源请求，而跨源 fetch 默认不附 cookie——两条结论自洽互证。
- **最小修复方向**（三处协同，缺一不可；修复后须用**带真实 T1 鉴权**的反代重跑浏览器端到端，而非 Director 等价物）：
  1. codebox `webui_proxy.go`：为 `/webui/{sid}/...` 增加 CORS 预检分支（镜像 v1 中央派发器「OPTIONS 不做 Cookie 鉴权」纪律，routes_v1.go:243-269）：`Origin=="null"` 且 ACRM 非空 → 204 + `ACAO: null` + `Vary: Origin` + Allow-Methods + `Allow-Headers: Authorization, Content-Type` + `Allow-Credentials: true`；
  2. amagi-pi `transport.ts`/`draft.ts`：fetch 增加 `credentials: 'include'`；
  3. amagi-pi `server.ts apiCorsHeaders`：`origin === "null"` 时补 `access-control-allow-credentials: true`。
  （备选：WebPlaneView 放开 `allow-same-origin` 可零 CORS 改动直通，但实质放弃沙箱跨源隔离，不建议。）

### Major-02 · 控制门只覆盖 `/api/input`；`/api/agent-interact` 是未设防的会话写面（无控制权设备可驱动 agent）

- **位置**：`internal/remote/webui_proxy.go:40,88`（仅 `target == "/api/input"` 进控制门）；amagi-pi `extensions/amagi-core/webui/server.ts:710-740`（`POST /api/agent-interact` → `orch.interact` → resume background 派生新任务，实为向会话注入输入）；同文件 `:672-684`（`PUT /api/draft` 写面）、`:686-689`（`GET /api/fs/dirs` 宿主机目录名枚举）亦未设门。
- **触发条件**：任意已配对但**无控制权**的设备（观察者 / 桌面持有时）直接 `POST https://{remote}/webui/{sid}/api/agent-interact`（device cookie 过鉴权，代理注入 Bearer 放行到后端，后端无第二道控制判定）。
- **实际影响**：控制平面完整性被绕过——观察者可对 blocked/已结算子 Agent 发任意文本续跑（spawn resume 任务），效果等同输入注入，违背 C1「无控制权 → 拒绝，语义对齐现有移动端输入面」的意图。冻结契约只点名 `/api/input`，漏掉了 amagi-pi v1.0.15（2026-09-11）新增的 agent-interact。次级：`/api/fs/dirs` 向任何已配对设备暴露宿主机目录名枚举（读面，暴露度较低）；`PUT /api/draft` 可覆写平面草稿（低危写面）。pi webui WS 已核对为纯下行事件流，无其他写帧。
- **证据**：代理门为严格路径相等；后端 `#handleRequest` 对 `/api/agent-interact` 仅做 Bearer（由代理注入）+ 方法/参数校验。测试 `TestWebUIProxy_InputControlGate` 只覆盖 `/api/input`。
- **最小修复方向**：写面集合化——`POST /api/input`、`POST /api/agent-interact`、`PUT /api/draft` 全部过 `SnapshotForDevice` 门（403 control.forbidden）；对 `/api/fs/dirs` 评估是否限控制者或维持配对即可；同步更新 §3.1 与 webui_proxy_test（观察者 403 用例）。

### Minor-03 · 契约文档 §3.1 与实现漂移：G1 直修「Origin: null 透传」后文档未同步

- **位置**：`docs/developer/remote-api-v1-contract.md:102`（「删除 `Origin`（后端白名单语义：头缺失即合法）」）；实现 `webui_proxy.go:225-234`（null 透传、其余删除）。
- **触发条件/影响**：§3.1 是本切片新增的规范性章节（文件头声明 wire 变更必须同步本文档），G1 的行为修订只落了代码+测试+workflow 记录，文档仍描述旧行为，误导后续实现与复审。另 `:103`「与 /ws/v1 input 帧同一 authority」措辞过强：/ws/v1 走精确活租约 + lane（`DoDevicePTY`，ws_v1_session.go:1124-1131），代理走快照判定（grace 持有者投影为 "you"，control_event.go:58-62 注释明示含 grace）——合理放宽但应写明。
- **最小修复**：§3.1 出向改写条目改写为「`Origin: null`（sandbox opaque iframe）透传、其余删除」并附 G1 理由；控制门条目补「快照判定，grace 持有者视为控制者」。假后端断言（Origin ∈ {"", "null"}）与真实后端 `isAllowedOrigin`（server.ts:149）已一致，无需改。

### Minor-04 · 代理面响应缺 `Cache-Control: no-store`（与 v1 面缓存纪律不一致）

- **位置**：`webui_proxy.go:53-58`（仅 nosniff + Referrer-Policy）；对照 v1 中央派发器 routes_v1.go:196-199 全响应 no-store。代理转发的 `/api/history` 等会话内容响应的缓存头取决于后端（server.ts `jsonResponse` 不设 cache-control）。
- **触发条件/影响**：GET 会话数据在浏览器后退/共享缓存场景理论上可缓存（HTTPS 同站、无前置共享代理的部署下实际风险低）；与 v1 纪律不一致。
- **最小修复**：`writeV1Error` 出口与代理 `/api/*` 路径补 no-store；静态资源路径保留后端自身缓存语义。

### Minor-05 · amagi-pi `deriveBase` 死分支 `'https'`（笔误）

- **位置**：amagi-pi `webui/src/main.ts:46` — `loc.protocol === 'https:' || loc.protocol === 'https'`，第二条件恒假（`Location.protocol` 恒带冒号）。
- **影响**：无行为影响，纯可读性/维护性。
- **最小修复**：删除第二个条件。

### Info-06 · reqID 解析失败时代理继续服务，v1 同情形 fail-closed 503

- `webui_proxy.go:74-76`（fallbackRequestID 后继续）vs `routes_v1.go:205-211`（503 fail-closed）。仅 crypto/rand 失败触发，不可利用；建议对齐 v1 纪律。

### Info-07 · X-Forwarded-For 出向追加（T1 已自报）

- ReverseProxy 默认追加客户端 IP；pi webui 后端不信任转发头，无安全影响。如需收紧可在 Director 删除。已知已记录，无需本批处理。

### Info-08 · 测试盲区（非缺陷，供后续补强）

- `webui_proxy_test.go` 未覆盖：① 带查询串的代理转发（`/api/history?since=…`——RawQuery 透传路径无断言）；② **无 cookie 的 OPTIONS 预检行为**——若有此用例即可在 Go 层提前暴露 Critical-01；③ amagi-pi 侧缺「经带鉴权代理」的浏览器级测试（proxy-smoke.mjs 无鉴权层，正是 G1 盲区）。
- `useSessionWebUI.refresh()` 清零 `inFlight` 可能与在途轮询并发（幂等 GET，无实害）。

---

## 已核对无发现的面

1. **capability token 生命周期**：tracker（internal/webui/service.go:129-138，仅 available 出端口/token）→ 状态端点仅经 fragment 下发（webui_proxy.go:299-303；`ValidateWebUIStatus` 强制无 query/userinfo/scheme，contract/webui.go:75-97）→ 代理出向覆盖式注入、未学到即删客户端值（webui_proxy.go:238-249）；ErrorHandler 仅记 sid 不记 err 细节（webui_proxy.go:216-218）；12 个 Go 用例均带 token 不外泄断言。**无发现**。
2. **代理面鉴权与 v1 一致性**：同一 `enforceAuthPolicy`/deviceCookie/错误映射（webui_proxy.go:81 ↔ routes_v1.go:546+）。device cookie 为 **SameSite=Strict** + HttpOnly + host-only 无 Domain（device_auth.go:42-53）——比 workflow 风险假设的 Lax 更强：跨站嵌套 iframe/跨站顶层导航均无法附 cookie，**SameSite=Strict 足以兜底跨站嵌套攻击面**（sandbox opaque iframe 的 same-site 判定随创建链取顶层站点，同站合法使用不受影响）。**无发现**。
3. **sid 白名单防穿越**：代理独立重解析 + `^[A-Za-z0-9_-]+$` ≤256B（webui_proxy.go:126-142）；状态端点 `matchV1Path` 的 `pathUnescapeSegment` 拒解码后 `/ \` 与控制字节（routes_v1.go:800-816）；编码斜杠/点/穿越路径测试覆盖且后端零调用。**无发现**。
4. **fail-closed 完备性**：`v1sec==nil` → 404（legacy NewServer，含测试）；`s.app==nil` → 404/unknown；`a.WebUI==nil` → unknown 空操作（对称 removeWebUITracker 模式）；未接线控制 runtime 的 `/api/input` → 403。**无发现**。
5. **V1RestEndpoints 冻结清单 / fixture parity**：清单仍 10 条（`TestV1WebUIStatus_FrozenManifestUntouched` 锚 + wire_test 冻结计数），fixture 文件未改；扩展端点以独立符号+单数路径隔离。**无发现**。
6. **桌面端 WebPlaneHost 直连零回归**：amagi-pi `deriveBase` 127.0.0.1/localhost 分支与旧行为逐字段等价（base.test.ts「行为锁死」用例）；WebPlaneHost.vue、`webui.Status.URL`、桌面轮询路径未动；codebox 侧 app.go/webui.Service 均纯增量。**无发现**。
7. **mobile 非 TUI 路径零变化**：默认 timeline、`?view=terminal|timeline` 语义保留（WorkspacePage.cliType.test.ts 4 用例回归绿）。**无发现**。
8. **routes_v1 中央门序未被旁路**：扩展路由 OPTIONS 与主路径均按 Host → 空 RawQuery → method(405+Allow) → origin(safeBrowserProof) → auth 镜像中央派发器（webui_proxy.go:174-278 ↔ routes_v1.go:243-330）；未知路径仍走中央 404。**无发现**。
9. **契约两端一致性（代码层面）**：Go `WebUIStatusEndpoint`（`/session/{id}/webui`、GET、200）≡ TS `V1_ENDPOINT_SESSION_WEBUI`；5 值枚举两端一致；`{state,url?}` 形状一致；TS 的 plural 容错端点仅客户端回退、服务端无对应面（404→回退→再 404→`unavailable` 降级，行为安全）。amagi-pi v1.0.16 §6.5 与 `deriveBase`/`derivePathPrefix` 实现一致（含 opaque origin 回退、末尾无斜杠、根路径空前缀）。文档层漂移见 Minor-03。**代码层无发现**。
10. **测试质量**：全部新增 Go 用例与 mobile 4 个测试文件、amagi-pi base.test.ts（19 用例）抽查，断言均对应被测行为，无空断言/恒真断言；假后端 `assertInbound` 建模与真实后端 Host/Origin/Bearer 校验一致（G1 修正后）。**无发现**。

---

## 覆盖范围 / 盲区 / 验证可信度

- **已覆盖**：两仓全部 diff 文件逐行审读（codebox 16 文件 + amagi-pi 6 文件，与三份切片报告清单核对一致）；关键调用链与安全面（CORS/cookie/沙箱/控制门/白名单/冻结清单）交叉验证；验证命令复跑：`go vet ./...`（0）、`go test ./internal/remote/... ./internal/webui/... -count=1`（全 ok，27.6s）、`go test . -run 'WebUI|RemoveSession'`（ok）、mobile Vitest 新增 4 件（46 用例全过）、amagi-pi `tsc --noEmit`（0 错）+ `vitest tests/base.test.ts`（19 过）+ `node --test tests/webui-server.test.mjs`（全过，静态产物可服务）。gofmt：app.go 的未格式化区为 HEAD 既有（stash 对照确认），新代码区格式合规；静态产物 index.html ↔ bundle 引用一致且含 deriveBase 逻辑。工作区状态未受审核影响（审核期间的 stash 对照已干净弹出，`git stash list` 剩余两条均为用户历史 stash，7 月日期）。
- **未覆盖盲区**：① 真实浏览器/真机复测——Critical-01 失效链为「规范 + 代码」推演，每步均有锚点，但最终确认依赖修复后带鉴权面的真实端到端（正是 G1 缺失的一环）；② amagi-pi 生产 bundle 仅抽查关键逻辑与引用一致性，未逐字节审计；③ mobile 全量 638 用例与 amagi-pi 207+59 全量未逐条复跑（新增文件全绿，其余为既有回归面）；④ Capacitor WebView 的 CORS/cookie 细节差异未实测。
- **验证可信度**：高——三份切片报告声明的验证命令与输出全部复现一致；Critical-01 的三层证据（代理无预检豁免+cookie 鉴权、页面 fetch 无 credentials、后端无 ACA-Credentials）中任一层单独成立即足以判定 REST 链路不通。

---

## VERDICT: FIX

Critical-01（阶段核心验收在真实鉴权面下不可达，且 G1 验证方法学上未覆盖该路径）与 Major-02（控制门写面缺口）需修复后复审；Minor-03/04/05 建议随修复批合并处理（§3.1 文档漂移与实现强相关，宜同批）。修复涉及两仓协同（代理预检豁免 + amagi-pi credentials/ACACredentials + 写面集合化），建议由 Leader 单批调度并在验证环节加入「带真实鉴权面的浏览器端到端」。
