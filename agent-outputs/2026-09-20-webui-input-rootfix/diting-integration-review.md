# 谛听 · 集成对抗审核：webui 远程输入「invalid protocol response」三路根因修复（full 档）

- 审核日期：2026-09-20
- 审核人：diting（独立检验，只读；未修改任何源码）
- 范围：amagi-codebox 工作树未提交改动（T1 `internal/remote/webui_proxy.go`+test、T3 `mobile/src/views/WorkspacePage.vue`+test）+ amagi-pi 工作树未提交改动（T2 声明路径 + static 产物）。用户既有 wailsjs/app_quota_probe* 改动按任务书排除。
- 基线：两仓 HEAD 未提交工作树；`git -C <repo> diff` + 未跟踪新文件 = 全量改动，与任务书声明逐文件核对。

---

## 一、Findings

### F-1 ｜ Minor ｜ 范围纪律 ｜ amagi-pi `webui/src/protocol.ts` 未列入 T2 声明路径
- 定位：`/Users/maorun/maorun-workpace/amagi-pi/webui/src/protocol.ts:171-176`
- 触发条件：任务书 T2 声明路径清单不含 `webui/src/protocol.ts`，但 `git status` 显示其被修改。
- 实际影响：内容为纯类型放宽（`InputErrorResponse.error` 改可选、新增 `code?` 字段），是 `controller.ts:388` `err.error ?? err.code` 通过 typecheck 的必要配套，无运行时行为；属声明遗漏而非越权改动。消费方仅 controller.ts 一处 cast（已核对无其他使用者）。
- 证据：`git -C amagi-pi diff -- webui/src/protocol.ts`（5 行类型注释+字段）；grep InputErrorResponse 仅 protocol.ts/controller.ts。
- 最小修复方向：在 T2 交付清单/交接记录中补记该文件及其改动性质。

### F-2 ｜ Minor ｜ 测试有效性 ｜ ErrorHandler 的 ACAO 断言为 no-op；镜像鉴权仅 authMissing 一支被锁
- 定位：amagi-codebox `internal/remote/webui_proxy_test.go:1121-1126`（TestWebUIProxy_ErrorEnvelope 第 5 类）；`internal/remote/webui_proxy.go:307-322`
- 触发条件：未来重构从 ReverseProxy ErrorHandler 中移除 `allowOpaqueOriginRead`，或 `webUIProxyEnforceAuth` 的 authExpired/authRevoked/authMalformed/authStoreDown 分支与 canonical 映射漂移。
- 实际影响：opaque iframe 的错误可读性（ACAO:null）在该路径无行为锁——若回归，浏览器会把 503 掩盖成 network_error，重新回到「连接中断」误导（本修复要根治的症状之一）；镜像四分支（`webui_proxy.go:311-322`）未经 plane 面测试，改错码值/漏清 cookie 不会失败。
- 证据：测试第 5 类用 `t.Logf` 代替断言（注释自认 `f.do` 不带 Origin:null）；五类信封断言仅覆盖 `authMissing` 一支鉴权失败。
- 最小修复方向：为不可达后端用例补发 `Origin: null` 的直连请求断言 ACAO:null；将 webUIProxyEnforceAuth 各 fail 分支表驱动（或与 enforceAuthPolicy 加 parity 测试）。

### F-3 ｜ Minor ｜ 可维护性 ｜ 鉴权失败映射被整段复制为本地镜像
- 定位：`internal/remote/webui_proxy.go:298-327`（webUIProxyEnforceAuth）vs `internal/remote/routes_v1.go:546-576`（enforceAuthPolicy deviceCookie 分支）
- 触发条件：后续修改 canonical enforceAuthPolicy（新增 fail 分支、改状态码/码值/清 cookie 策略）而镜像未同步。
- 实际影响：数据面与 v1 面的鉴权错误语义静默分叉（同因异果），且无 parity 测试兜底（见 F-2）。
- 证据：两段 switch 除 writer 外逐行相同（本次 diff 自述“本地镜像”）。
- 最小修复方向：抽取 fail→(status,code,layer,msg,hint,clearCookie) 的共享映射表，writer 作为参数注入。

**Critical / Major：无。**

---

## 二、逐项审核结论

### 1. 跨仓协议对齐（最高优先）——通过
- T1 信封实际形状（`writeWebUIPlaneError`，webui_proxy.go:273-293）：MarshalAPIError 冻结五字段（requestId/code/layer/message/actionHint，contract/errors.go:109-116）+ sjson 注入 `v:1`、`error==code`。T2 `isErrorEnvelope`（http-source.ts:595-598）认 string `error` 或 string `code`——新代理体双字段命中；**旧代理裸 v1 体（仅 code 字段）同样命中**。
- 码值逐一对齐：T1 注入 `control.forbidden`/`auth.unpaired`/`service.down`/`session.not_found`（contract/errors.go:37-44 字面量）↔ T2 controller.ts:630-633 四条 case 字面完全一致。`auth.window_expired`/`auth.revoked` 无专属文案→default 分支回退到代理 message（真实码可见、不断 WS，不劣化）。
- 取值优先级：`error ?? code`（controller.ts:388-389），测试锁定 error 优先（input-error-protocol.test.ts「error 字段优先于 code」用例）。
- 组合推演：**旧前端×新代理**——旧 sendInput 对所有响应硬校验 `json.v===1`；新错误体带 v:1 → 校验通过 → 旧 `'error' in body` 命中 → 走旧 default 文案（无 WS teardown）。优于旧行为（旧行为 failProtocol+断 WS）。**新前端×旧代理**——裸 v1 体（code string、无 v）命中 isErrorEnvelope → 透传 → `error ?? code` 取到真实码并映射。优于旧行为。两组合均不劣化。
- 2xx 语义：2xx 缺 v 仍 failProtocol（协议校验保留，测试锁定）；2xx 带 v+error 的畸形体不会被判成功（成功门为 `status===202 && 'delivered' in body`，见结论 2）。

### 2. 安全红线——通过
- 错误体泄漏面：所有本地错误为固定常量串（码表+固定 message），不含 token/后端端口/内部地址；`assertWebUIPlaneEnvelope` 对每类信封重查 token 红线（webuiTestToken 在 fixture 中实际使用，webui_proxy_test.go:84/179/345，断言非空转）。ErrorHandler 仅记 sid 不记 err 内容。
- `allowOpaqueOriginRead`：函数未动；所有本地错误写出前调用顺序不变（SetCookie → allowOpaqueOriginRead → writeWebUIPlaneError → WriteHeader，头部先于状态行，正确）；403 control-gate 路径有 ACAO:null 断言（测试第 2 类）。
- v1 REST 面防污染：`dispatchV1WebUIStatusRoute`/`handleV1SessionWebUIStatus` 全部错误仍走冻结 `writeV1Error`+`enforceAuthPolicy`（webui_proxy.go:634-720 未改）；`TestV1WebUIStatus_Gates` 显式断言 v1 401 体**不含** v/error 且五字段非空。
- T2 不误放行：sendInput 成功门=202+delivered；probeReady 成功门=200+v+ready；history 成功门=200+v。4xx/5xx 信封只进错误/重试分支。后端透传错误（v:1+error:snake_case）经新分支提前返回，与旧路径（v 校验通过后同往下传）结果等价，无行为漂移。

### 3. 行为完整性——通过
- probeReady：错误信封→retryProbe，与既有 catch（网络失败）/非 200 语义一致；本仓后端 /api/info 503 为带 v 的 infoBody（server.ts:669，无 error 字段）不命中新分支，注释声明与代码事实一致。8 次重试耗尽→ended(unreachable)+10s 复探，既有降级不变。
- history：首页信封→retryHistory（与既有 503/非 200 同路）；次页/翻页显式排除 410——后端 cursor_expired 体带 v:1+error，跳过新分支后通过 v 校验进入既有 `handleCursorExpired()` 专用路径（loadLatestFirst:457-459 / fetchEarlierPage:519-521），语义完整保留；翻页信封→静默结束，与既有非 200 路径（非 200 不写 prepend，仅复位 loadingHistory）一致。
- T3 条件完备性：`isWebPlaneView && webuiUrl && running && control!=='you'` 四项与测试负例一一对应（terminal 视图/probing 无 URL/unavailable/非 running 均断不出现）；`control.state==='other'` 时 deviceName 为必有 string（ws.ts:261 wire 契约），副行文案无 undefined 风险；store 初始 `{state:'none'}` 合理。
- acquire 失败路径：409→controlNotice conflict（NoticeStack 全局渲染，WorkspacePage.vue:386，webplane 视图内可见）；非 409→lastError banner（:417，全局）。成功后 WS control.state 事件驱动 `state==='you'`，提示条条件自然失效（测试锁定真实事件链）。
- 无自动抢占：`store.acquire()` 仅出现在显式 @click（:480）与终端视图既有显式入口（:388）；无 watcher/onMounted 自动触发（grep 全文确认）。

### 4. 测试有效性——通过（含 F-2 弱点）
- T1 五类断言真锁行为：信封 helper 断 v==1/error==code/code==wantCode/四 v1 字段非空/不含 token——非空转；五类覆盖 401(无鉴权)/403(raw token 写面)/503(probing)/404(unavailable)/503(ErrorHandler) 全部本地错误出口。
- T2「403 信封不断 WS」：FakeTransport 记 wsCloseCalls，断言 0 + states 为空 + 原样返回；409 无 v 信封→steer 全链路断言两次 POST body 序列 + connText 仍「已连接」+ inputError 隐藏——覆盖任务书指定场景。200 无 v→failProtocol 保留用例锁定协议校验语义未被误删。
- T3 acquire spy：`vi.spyOn(store,'acquire')` 断 calledTimes(1)；you 态消失与 running 消失均经真实 WS onEvent 驱动，非直接改 store。

### 5. 范围纪律——通过（含 F-1 遗漏）
- amagi-codebox：`git status` 改动 = T1 两文件 + T3 两文件 + 范围外用户既有（frontend/wailsjs/runtime*、app_quota_probe*）——声明路径外零改动。
- amagi-pi：改动 = T2 声明全量 + protocol.ts（F-1）+ static（旧 hash 删除/新 hash 新增/index.html 引用更新，CSS hash 未变与无样式改动一致）；新 bundle 含 `control.forbidden`/`需要会话控制权` 修复串（grep 验证）。

---

## 三、验证记录（本轮独立复核）

| 验证 | cwd | 结果 |
| --- | --- | --- |
| `go vet ./...` | amagi-codebox | exit 0 |
| `go test ./internal/remote -count=1`（含 browser e2e） | amagi-codebox | ok 26.566s |
| `npm test`（webui vitest） | amagi-pi/webui | 24 files / 223 tests 全过 |
| `node --test tests/webui-server.test.mjs` | amagi-pi | 26/26 全过 |
| `npx vitest run src/__tests__/views/WorkspacePage.webplane.test.ts` | amagi-codebox/mobile | 18/18 全过 |

## 四、盲区与未覆盖项

- amagi-pi 根 `npm test`（2755）、typecheck、depcruise 未独立重跑（任务书称已绿；本轮复核收敛至直接相关三套）。
- static bundle 字节级可复现未验证（重建会写工作树，越只读边界；仅做修复串存在性验证）。
- Origin:null 真机 iframe 手工链路未执行（以静态路径推演 + 代理测试 ACAO 断言覆盖）。
- mobile 全量套件（webplane 单文件外）未重跑。
- probeReady 将 401/404 等永久性错误与 503 同样按 retryProbe→ended 处理（错误码未在探测路径透出）——与 docs §13 G3 声明语义一致，记录为观察项，非偏差。

## 五、结论

三路修复目标（错误码不被「invalid protocol response」掩盖、移动端 webplane 有控制权获取入口、跨版本组合不劣化）全部达成，未发现 Critical/Major 问题；三条 Minor 均为测试锁面/声明完整性/双维护点，不阻断本阶段。

VERDICT: PASS_WITH_MINOR
