# Claude Code WebUI Plane Contract

> Claude Code 会话 Web 平面（结构化消息流 + 双车道输入）的规范文档：架构定位、HTTP/WS 数据面契约、双部署形态、输入双车道、转录 jsonl 依赖与宿主桥。
>
> Design source: `agent-outputs/2026-11-14-claude-code-webui/design.md`（§5/§6/§7 是 wire 与行为真源）。
> 实现锚点：`internal/claudewebui/`（数据面 server）、`internal/appmeta/claude/transcript.go`（转录解析）、`app_claudewebui.go`（App 接线）、`internal/remote/`（设备平面车道 + 反代透传）、`webui-claude/`（SPA 工程）。
> 转录字段实测依据：`internal/appmeta/claude/testdata/transcript.sample.jsonl`（Claude Code 2.1.319 本机真实采集脱敏固化，见 §8）。
>
> Any wire change MUST update this document and the corresponding tests in the SAME diff. 与 pi 平面共用的面（tracker/反代/宿主桥）另见 `docs/developer/remote-api-v1-contract.md` §3.1。

**核实状态（2026-11-14）**：本文全部端点/字段/状态机均已对照当前代码逐条复核（`internal/claudewebui/{server,auth,input,ws,tailer,assets}.go`、`internal/remote/{webui_proxy,control_gate,control_runtime}.go`、`internal/remote/contract/webui.go`、`webui-claude/src/`）。默认值：轮询 500ms、环形缓冲 4096、历史上限 500（`RuntimeSettings.normalize`，零值取默认）。

---

## 1. 架构定位（数据面在 CodeBox 进程内）

Claude Code 会话与 pi 会话共享同一套 Web 平面**宿主设施**（`webui.Service` 五态 tracker、`/webui/{sid}` 反代、`WebPlaneHost` iframe 容器、快捷功能宿主桥），但**数据面宿主位置是唯一本质差别**：

| 维度 | pi 会话 | Claude Code 会话 |
| --- | --- | --- |
| 数据面 server 宿主 | amagi-pi 扩展进程（Node，进程外） | **CodeBox 进程内**（`internal/claudewebui`，Go） |
| 平面注入通道 | `AMAGI_WEBUI_*` env 注入（扩展自起 server） | PTY 启动后由 App 直接 `Manager.Start`（claude 无扩展宿主，env 通道无意义） |
| 结构化流来源 | amagi-pi webui 插件的事件流 | **Claude Code 落盘的会话转录 jsonl 尾随**（`~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl`） |
| 流式粒度 | 插件侧事件 | 「API message 完成」级（转录落盘粒度；非块级增量，诚实边界，见 §8） |
| 输入面 | 扩展进程内注入 | **经控制门写回 PTY**（双车道，见 §5；与 TUI 平面共享同一进程同一上下文，xterm 平面同步可见） |
| tracker / 反代 / 宿主桥 | `webui.Service` + `/webui/{sid}` + `WebPlaneHost` | **同一套，零差别复用**（tracker 键 = CodeBox sessionID，同一 ID 空间；`webui.Service` 代码路径无 appType 判定） |
| 端口/token | 扩展自报 | `webui.AllocateFreePort` 预分配 + `webui.GenerateToken`（启动前生成，PTY 成功后注册） |

每会话一个 per-session 回环 server（`Manager` 管理全部 plane；同 sessionID 重复 `Start` 幂等替换先停旧，防 listener 泄漏）。会话双平面共享同一 claude 进程：Web 输入 → 控制门 → PTY → claude TUI 消费；Web 展示 ← 转录 jsonl ← claude 落盘。无第二进程、无第二上下文。

`Manager` 与设备平面车道原语**均不进 Wails Bind**（Bind 清单冻结 21 项不变，`bind_list.go`/`bind_manifest_test.go`）。

## 2. 安全红线（冻结）

- **token 视同会话权限**：capability token 只经 URL fragment `#/t=<token>` 下发，绝不进 query/path/请求行/日志/错误体。错误体统一 `{v:1, error:"<code>"}`（错误响应也带 `v`——前端对每个响应硬校验 `json.v===1` 的既有教训）。
- **server 只 bind `127.0.0.1`**（`net.Listen("tcp", "127.0.0.1:<port>")`）；Host 校验 `^127\.0\.0\.1(:\d+)?$`，白名单外一律 403 `bad_request`（remote 反代出向已按此重写 Host，直连绕过即拒）。
- 鉴权永远只认 Bearer / WS 子协议 token 的 constant-time 等值（`subtle.ConstantTimeCompare`）。`X-Amagi-Plane-Actor-Device` 头**只是车道选择信号，不是鉴权**（见 §5）。
- 静态面无鉴权、`ACAO:*`、`X-Content-Type-Options: nosniff`（静态面不含会话数据；跨源模块脚本 CORS-mode 加载需要）。
- 预检 OPTIONS（`/api/*`、`/ws/*` 路径）本地应答 204，不鉴权、不转发（CORS 规范预检不携带凭据；头面 `Access-Control-Allow-Origin: null` + `Vary: Origin` + `Access-Control-Allow-Methods: GET, POST, PUT, OPTIONS` + `Access-Control-Allow-Headers: Authorization, Content-Type` + `Access-Control-Allow-Credentials: true`）。
- API 响应 CORS 按 Origin 分流：`Origin: null`（sandbox opaque iframe）→ `ACAO:null` + credentials（否则浏览器把响应掩盖成 network_error，错误码不可见）；其余/缺失 → 不加 CORS 头（同源无需）。
- 错误码全集（闭合）：`auth.unauthorized`（缺 Authorization 头，401）/ `auth.forbidden`（token 不等值，403）/ `bad_request`（体损坏/超限/非 UTF-8/Host 非法，400 或 403）/ `method_not_allowed`（405）/ `control.forbidden`（设备车道持控失败，403）/ `service.down`（控制服务未就绪，503）/ `input.failed`（其余写失败，500）。
- 未匹配的 `/api`、`/ws` 路径走静态兜底（不给探测面）。

## 3. HTTP 端点契约

全部 `/api/*` 强制 `Authorization: Bearer <token>`：缺头 → 401 `auth.unauthorized`；携带但不等值 → 403 `auth.forbidden`。请求体硬上限 1MiB（超限 400 `bad_request`）。

| Method + path | Auth | Request | Success | 要点 |
| --- | --- | --- | --- | --- |
| `GET /api/info` | Bearer | — | `200 {v:1, ready:true, sessionId, pid, port, appType:"claudecode"}` | 探测契约五字段 `v/ready/sessionId/pid/port`（`internal/webui/probe.go:40-44` 强校验 `v==1`、port 等值）+ 附加 `appType`（前向兼容）。**server 起即 ready——语义 = 服务就绪，而非会话有内容**（转录文件可迟到，§8） |
| `GET /api/history` | Bearer | `?limit=N` | `200 {v:1, items:[TranscriptRecord...], cursor, busy, title, truncatedHead}` | 全量快照 = tailer 当前缓冲投影，返回最新 `limit` 条。limit 钳制：缺省/非法/≤0 → `HistoryLimit`（默认 500）；`> HistoryLimit` → 钳到上限。`cursor` = 字节游标（int64）；`title` = 首条 human 记录文本（粘性，缓冲丢最旧不回退）；`truncatedHead` = 缓冲曾丢最旧（粘性） |
| `POST /api/input` | Bearer | `{text, submit?}` | `200 {v:1, ok:true}` | `text` 须 UTF-8（**在原始体上校验**——`encoding/json` 会把非法 UTF-8 静默替换成 U+FFFD，解码后验已是死代码）且 ≤128KiB（超限 400）；`submit:true` 追加 `\r`（与 TUI 回车一致）；payload ≤1KB 单发、>1KB 走 1KB 分块变体。**车道分流见 §5** |
| `POST /api/interrupt` | Bearer | `{}`（体允许为空，内容一律忽略） | `200 {v:1, ok:true}` | 同车道规则写 `\x1b`（Esc，与 TUI 一致） |
| `GET /api/draft` | Bearer | — | `200 {v:1, draft:string}` | 内存暂存（sandbox iframe 无 localStorage 且宿主 url watch 整页重载——pi 实证需求）。**draft 不写 PTY、无车道概念** |
| `PUT /api/draft` | Bearer | `{draft}` | `200 {v:1, ok:true}` | ≤128KiB（超限 400）；空串即清除 |
| `/` 及静态资源 | 无 | — | embed SPA | hash 路由 `#/t=<token>`；产物缺失降级提示页（§7） |

写失败映射：设备车道持控失败 → 403 `control.forbidden`；控制服务未就绪（control nil/未接线，fail-closed 对齐 `App.PtyWrite`）→ 503 `service.down`；其余 → 500 `input.failed`。

## 4. WS `/ws/events`

- **鉴权（升级前）**：客户端 offer `Sec-WebSocket-Protocol: "webui, <token>"`——恰两段、第一段 `webui`、第二段 constant-time 等值 token 才升级；**响应仅回显 `webui`**（remote 反代的子协议换写依赖该形状）。Bearer 等值亦可（反代出向两者都带）。失败 → 401 `auth.unauthorized`。`CheckOrigin` 恒 true——鉴权只认 token（sandbox opaque iframe 的 Origin 是 null，白名单无增量价值）。
- **信封**：`{v:1, seq, ts, kind, event}`；`ts` = Unix 毫秒；`seq` per-plane 单调递增（从 1 起，0 保留为「尚无事件」续传点）。**hello 携带的 seq 是「服务端认定的续传点」**（= 客户端 `?since` 或 floor-1），其后重放与直播事件 seq 严格递增。
- **事件（kind → event）**：
  - `hello {stream, floor, cursor, busy}`——连接即推。`stream` = plane 实例 uuid（**重连必须能识别流重建**——pi v1.0.13 教训）；`floor` = 环形缓冲最旧 seq，`since < floor` 即缺口信号（客户端全量 refetch `/api/history`）。
  - `transcript {items:[TranscriptRecord...], cursor}`——本批新记录（截断/换文件重建时为全量缓冲）。
  - `busy {value}`——忙闲变化才发。
  - `bye {}`——plane 停止时广播一次（尽力而为）。
- **重放**：`?since=<seq>` 重放 seq 更大者；环形缓冲默认 4096。`since` 越过尾部 → 收敛到尾部（不重放）；缺失/非法/负值 → 视同未携带（新鲜连接从 floor 前起步，不重放）。**推荐时序**：先连 WS 后拉 `/api/history`（重复内容按 uuid 幂等合并）。
- **纯下行**：上行帧一律读丢（读循环只驱动控制帧与对端关闭感知）。读上限 1MiB；单连接发送队列 256，写满视为慢消费者断开（重连即重放，无数据损失）；单帧写超时 10s。

### 4.1 TranscriptRecord（wire 形状）

```jsonc
{ "kind": "assistant", "uuid": "...", "ts": "ISO",
  "isHuman": false,            // 仅 user 行：origin.kind=="human"
  "text": "…", "thinking": "…",
  "toolUses":   [{ "id": "toolu_…", "name": "Bash", "input": {/*原样 JSON*/} }],
  "toolResults":[{ "id": "toolu_…", "isError": false, "content": "…" }],
  "usage": { "in": 1, "out": 2, "cacheRead": 3, "cacheWrite": 4 }, "model": "…" }
```

- `kind` ∈ `user | assistant | system`；可选字段（`isHuman/ts/text/thinking/toolUses/toolResults/usage/model`）零值省略。
- **服务端不做 toolUse↔toolResult 配对**——客户端按 `id` 配对成工具卡：无配对 = running；配对 + `isError` = failed；配对成功 = success。
- `busy` 启发式（服务端派生，只影响 UI、不做写入门控）：最后一条有效记录是 human-user（等首响应）或存在未配对 `tool_use`（工具执行中）→ true；无 pending `tool_use` 的 assistant 收尾 → false；无记录 → false。

## 5. 输入双车道（桌面 take-first / 设备平面 no-take）

**车道分流单点**在 `internal/claudewebui/input.go`（分流散落多实现 = 设备输入误走桌面车道的复发面）：

| 车道 | 触发条件 | 控制原语 | 控制权语义 |
| --- | --- | --- | --- |
| 桌面车道 | 请求**无** `X-Amagi-Plane-Actor-Device` 头（或值为空） | `control.DesktopInput` / `DesktopPasteChunk` | **take-first**：每次写先 `TakeDesktop` 夺权——本地 WebUI 输入 = 本地用户意图，与在 xterm 打字同语义，夺权是正确行为 |
| 设备平面车道 | 请求携带**非空** `X-Amagi-Plane-Actor-Device` 头（**仅 remote 反代在写面转发时设置**） | `control.DevicePlaneInput` / `DevicePlanePasteChunk`（`internal/remote/control_runtime.go:604-648` → `controlGate.DoDevicePlanePTY`，`control_gate.go:448`） | **no-take**：绝不调用 TakeDesktop/Acquire/Release、不提交任何控制迁移（owner/phase/controlEpoch/holderGeneration 全不动）；写时在 stateMu 下校验 holder 恰为该 deviceID 且处 **connected 或 grace** 相位，否则 `DenyNotController` → 403 `control.forbidden` |

**头规则**（wire 值冻结 `X-Amagi-Plane-Actor-Device`，常量 `contract.WebUIPlaneActorDeviceHeader`，`internal/remote/contract/webui.go:38`）：

- 仅由 remote 反代在**写面**（`POST /api/input`、`POST /api/agent-interact`、`POST /api/interrupt`、`PUT /api/draft`——`webuiBackendWriteFaces`，`webui_proxy.go:73-78`）转发时**覆盖式注入**：先无条件剥离客户端自带值，再注入 plane token 解析出的已鉴权设备 ID（与 Authorization 覆盖注入同一纪律）；同时从 `Connection` 头中剥掉该 token 名（防 hop-by-hop 剥除链绕过注入）。
- 非写面、或零 principal（raw 后端 token 只读兼容通道）→ 只剥离、不注入。
- **该头不是鉴权**：鉴权永远是 Bearer/子协议 token（capability token 视同会话权限，持 token 者本就能经桌面车道写入，伪造头不构成提权）。

**grace 宽松的理由（与 ws/v1 车道的对照）**：ws/v1 输入走 `DoDevicePTY`——有精确 live 连接租约可验（deviceID + connectionID + attachmentGeneration 三元组，connected-only），grace 期禁止是防僵尸连接；平面 HTTP 写是请求作用域，身份由反代在**每次请求**时重新解析（plane token → deviceID → 再校验设备未吊销/未过期），持控判定与反代写面门 `SnapshotForDevice` 的快照投影（grace 持有者视为控制者，`webui_proxy.go` 文档化宽松）保持**同一口径**——收紧只会造成「代理放行、后端拒绝」的口径分裂。吊销设备/不接受状态校验镜像 `createDevicePTYPermit`；铸证用 `mintOperationPermit(ctx, entry, deviceID, 0)`（无需连接租约，lifecycle drain 路径同款先例）。

**TOCTOU（文档化行为）**：反代门放行 → 后端写前控制权被本地按键夺走 → 后端 `DenyNotController` → 403 `control.forbidden`，客户端重新取控后重试。可接受（快照门与写时校验之间本就存在窗口，fail-closed）。

**TUI 状态盲写（已知边界）**：claude TUI 处于菜单/权限对话框态时，WebUI 输入会选中菜单项——与「共用一个键盘」同构，v1 接受并文档化（后续可用 PTY 输出解析 composer 状态改进）。

## 6. 双部署形态（同一 SPA，两种 base）

同一份 `webui-claude` 构建产物必须同时运行于：

| 形态 | URL | 平面 token 来源 |
| --- | --- | --- |
| 桌面（iframe 直连） | `http://127.0.0.1:<port>/#/t=<token>`（回环**根路径**，`internal/webui/service.go:458` statusOf） | 后端 raw capability token |
| 远程（移动端经反代） | `https://<remote>/webui/{sid}/#/t=<plane-token>`（**子路径**，contract 强制相对代理路径带尾斜杠、无 query） | 代理铸造的 per-(session, device) plane token |

**推导规则**（`webui-claude/src/data/basePath.ts` `resolvePlaneBase`，纯函数）：

- `dir` = `pathname` 截至最后一个 `/`（含）——桌面 `/`、远程 `/webui/{sid}/`；
- API 请求 = `dir + 'api/...'`（`/api/info`、`/webui/{sid}/api/info`）；容忍调用侧多余前导 `/`；
- WS = `(protocol==='https:' ? 'wss' : 'ws') + '://' + host + dir + 'ws/events'`；
- query（`?skin=light`）与 fragment（`#/t=`）**不参与**推导（防御性截断混入的 `?`/`#` 段）。

**红线**：所有 fetch 与 WS 一律经此推导——**禁止根绝对路径 `/api/...`**（远程子路径形态下打到反代根 404）**与硬编码 `127.0.0.1`**（反代场景下那是移动设备自身；基址推导以路径前缀判定而非 hostname）。构建侧 `vite.config.ts` 固定 `base:'./'`——产物 index.html 全部资源引用相对化（`./assets/...`），两种 base 下浏览器都解析到正确子路径；**禁止默认 `base:'/'`**。

**白屏防线**（iframe 的 load 事件在子资源 404 时照常触发，宿主与移动端均无从侦测——防线必须在 SPA 内部）：

1. 资源相对化（vite `base:'./'`）——子资源随任一 base 正确加载，白屏根因消除；
2. `resolvePlaneBase` 单点推导 API/WS 地址；
3. SPA 首屏自带页内错误态：token 缺失/非法、`/api/info` 失败、history 失败各显独立错误文案 + 重试按钮（`webui-claude/src/App.vue`）；
4. 回归断言：`webui-claude/src/__tests__/basePath.test.ts`（根/子路径/query+fragment 免疫/WS 协议换算）+ `dist-base.test.ts`（构建后断言 `dist/index.html` 无 `="/` 根绝对引用；CI 中 build 先于 test）。

**构建嵌入**：`webui-claude` 独立 Vite 工程（Vue3 + TS，不引 Wails/wailsjs；`package-lock.json` 入库支撑 `npm ci`），`build.outDir='../internal/claudewebui/dist'`、`emptyOutDir`（清空后 vite `closeBundle` 钩子自动恢复入库占位 `.gitkeep`——fresh checkout `go vet` 编译依赖该文件存在，design §13-R6），经 `go:embed all:dist` 嵌入。构建入口：根 `package.json` `build:claude-webui`；`wails.json` preBuildHooks `*/*` 指向聚合脚本 `build:embedded-web`（= `build:mobile && build:claude-webui`——hook 经 `shlex.Split` 直接 exec 单条命令，`&&` 不可用，故聚合在 npm 脚本层）。dist 仅 `.gitkeep` 占位（fresh checkout / 未跑构建）时静态面返回**降级提示页**并 WARN 一次——探测与数据面不受影响（可观测的降级而非失败）。

## 7. 会话生命周期

| 链路 | 行为 |
| --- | --- |
| 桌面 embedded（原生目标） | `LaunchSession` 注入 `--session-id`（既有）→ 门控 `claudeWebPlaneEnabled`（开关开（默认 true）+ 非 WSL + Manager 已接线）放行则预分配 port/token（失败仅 WARN 放弃平面，不阻断启动）→ PTY 成功 `SetPID` 后 `startClaudeWebPlane`：转录根从最终 spec env 解析（§8）→ `ClaudeWebUI.Start` → `WebUI.RegisterSession`（tracker probing → available）。Start 失败 WARN 不注册（状态 unknown → 前端无切换控件） |
| remote v1 create/restart | planner（`launch_planner.go` `buildClaudePlan`）非 WSL 目标生成 sid、`CLIArgs: ["--session-id", sid]`（resolver 三消费点覆盖：WSL/原生/attach）+ `ProcessStartSpec.WebUIPlane{ClaudeSessionID}` 携带材料；executor（`launch_executor.go`）门控 `WebUIPlane != nil && claudeWebUI 已接线` 才注入平面 effect，commit 后 `RecordClaudeSessionID` → `Start` → `RegisterTracker`；Abort 路径零调用零残留 |
| PTY 退出 | 退出 goroutine `stopClaudeWebPlane`：`ClaudeWebUI.Stop`（幂等，listener 立即释放）+ `WebUI.Invalidate`（tracker → ended） |
| 会话删除 / 批量清理 | 三处 commit 点（`removeWebUITracker` / `SetRunTerminalCleanup` / `SetPostRemoveCleanup`）同样 Stop——防 listener 泄漏 |
| WSL 目标 / 外部终端/VSCode/Zed / 开关关闭 | 不 Start 不 Register，状态 unknown，前端无切换控件（与 pi 不可用会话同语义） |
| `/resume`（TUI 内切换会话） | 锁定 jsonl 停滞；v1 不跟随（与标题 tracker 方案 R 同语义）；显示停留在旧转录 |
| 多会话 | per-session 独立 server/port/token；同 workDir 会话靠锁定 sid 隔离转录 |

**前端闸门**（桌面 `webPlaneGate.ts` / 移动 `WorkspacePage.vue`）：`webPlaneCapable = {pi, claudecode}`（切换控件 + 探测前提）；`autoPreferWeb` 仅 pi——claude 的 Web 平面是内置恒 available，自动切会劫持其既有默认平面（桌面 TUI / 移动 timeline），故 claude 会话**只提供切换控件，由用户显式进入**；移动端 `?view=webplane` 深链按 `webPlaneCapable` 放行，缺省视图仍 timeline。

## 8. 转录 jsonl 依赖声明（非官方冻结契约）

`~/.claude/projects/` 下的会话转录 jsonl 是 Claude Code 的**落盘事实**，不是官方冻结契约；块级字段名属 Anthropic API 消息形状。schema 随 Claude Code 版本演进。**影响面仅 Web 平面**（TUI 主平面不经此路径）；解析器行级容错保证坏行不中断数据流。

### 8.1 实测字段清单（fixture 依据）

以下清单提炼自 `internal/appmeta/claude/testdata/transcript.sample.jsonl`——**Claude Code 2.1.319 本机真实会话采集后脱敏固化**（`-p` 模式两轮 + winpty 交互式一轮；thinking / tool_use / tool_result / usage 四分项 / origin.kind 全部来自实测，非手写想象）。单测以该 fixture 回放为依据（`transcript_test.go` `TestReadTranscriptIncrementSampleFixture`）。

**根级行字段（被消费的）**：

| 字段 | 实测形态 |
| --- | --- |
| `type` | `"user"` / `"assistant"` / `"system"` 产出记录；**其余实测行类型一律忽略**（offset 照常推进）：`attachment`、`queue-operation`、`api-request*`、`last-prompt`、`tools-used`、`cost-state`、`mode`、`permission-mode`、`atis-latch`、`file-history-snapshot`（Claude Code 内部记账行） |
| `uuid` | 行级唯一。**同一 API message 的 thinking / tool_use 块拆成多行**、共享 `message.id` 但各有不同 `uuid`（实测形态） |
| `timestamp` | ISO 串原样透传（如 `"2026-11-13T21:12:17.110Z"`），前端格式化 |
| `message` | `{role, model, content, usage}`；`system` 行（实测 `stop_hook_summary`）可无 message，仍产出记录 |
| `origin` | user 行 `{kind:"human"|"tool"}`；**2.1.319 的 `-p`/sdk 模式 user 行没有 origin 字段**（取而代之 `promptSource:"sdk"`）——此时 `IsHuman=false` |

**`message.content` 块类型**（string 直取文本；块数组按下表投影；未知块类型如 `redacted_thinking` 忽略）：

| 块 type | 消费字段 |
| --- | --- |
| `text` | `text` |
| `thinking` | `thinking` |
| `tool_use` | `id` / `name` / `input`（原样 JSON 保留，不解释） |
| `tool_result` | `tool_use_id` / `is_error`（**实测成功结果无该字段**，零值 false）/ `content`（实测 string；块数组形态做判型降级拼接 text 块） |

**`message.usage` 四分项**（字段名对齐既有 `jsonl.go` usageMessageUse）：`input_tokens` / `output_tokens` / `cache_read_input_tokens` / `cache_creation_input_tokens`。其余 usage 内字段（`output_tokens_details.thinking_tokens`、`server_tool_use`、`cache_creation.{ephemeral_*}` 等）不消费。`message.model` 实测合成错误行可为 `"<synthetic>"`。

### 8.2 容错与升级策略

- **行级容错**：单行 unmarshal 失败跳过、未知行类型忽略、offset 照常推进（坏一行不中断数据流）；`message.content` 二次判型（string vs 块数组）；块级坏数据跳过不影响同数组其余块。
- **增量读取**：`ReadTranscriptIncrement(path, offset)` 字节断点续传（`bufio.Reader.ReadBytes` 按需扩容，可吞含 base64 图片或超长 tool_result 的单行）；文件 size < offset（`/resume` 重写或换文件）→ `truncated=true` 从 0 重读全量、缓冲整体重建重发。
- **升级风险**：Claude Code 新版本新增行类型/块类型/字段 → 按未知忽略（记录不产出或字段留空），不报错不中断；删除/改名既有字段 → 对应投影留空（如 thinking 消失），平面仍可用。破坏性变更需随版本更新 fixture 与本清单。

### 8.3 转录根解析与已知边界

路径规则：`<root>/projects/<encoded-cwd>/<session-uuid>.jsonl`；`encoded-cwd` 复用 `pathSepReplacer`（`: \ /` → `-`）。

转录根 `ClaudeConfigRoot(homeDir, env)`（`transcript.go:336`，覆盖用户自定义 HOME 与 CLAUDE_CONFIG_DIR 两类重定向）：

1. env `CLAUDE_CONFIG_DIR` 非空 → 直接以其值为根（**受管 claude-accounts 会话在最终边界剥离用户自定义值**，自然回落后续分支）；
2. env `HOME` 非空 → `<HOME>/.claude`；
3. env `USERPROFILE` 非空 → `<USERPROFILE>/.claude`（Windows）；
4. 皆缺 → `<homeDir>/.claude`（`os.UserHomeDir()`，调用方传入）。

键查找大小写不敏感（Windows 环境变量语义）；空串/纯空白视为未设置。`ClaudeSessionID` 非空 → 锁定该文件（方案 R）；空 → 目录内 mtime 最新回退（方案 P）。

**已知边界**（如实声明）：

- 提取规则未覆盖的其余重定向形态（未来新增重定向键）→ 表现为**空转录 + busy=false**（非错误、不误显内容）；
- **非 ASCII cwd 编码分歧**（实测 2.1.319）：Claude Code 对含非 ASCII 字符的 cwd 做逐字符替换，与本仓 `pathSepReplacer` 的「保留原样」编码在非 ASCII 路径上有分歧——锁定 sid 的链路 workDir 为项目目录、通常纯 ASCII，不受影响；既有编码行为保持不动。

## 9. 宿主桥契约（引用）

SPA ↔ 宿主 `WebPlaneHost` 的 postMessage 桥**复用 pi 平面的跨仓冻结契约**（与 `frontend/src/components/terminal/quickPathInsert.ts` 同源，勿改消息形状）：

- 宿主 → SPA：`{type:'amagi:insert-input', token, text}`——快捷功能「输入工作路径」插入。接收守卫：嵌入态 + token 与自身 fragment `#/t=` **严格相等**（凭证校验）+ text 非 string 空 UTF-8 ≤128KiB；通过后追加进输入框并聚焦、**不自动发送**；任一不满足静默忽略。
- SPA → 宿主：`{type:'amagi:open-url', url, token}`——sandbox（`allow-scripts allow-forms`，无 `allow-popups`）内 `a[target=_blank]` 点击被浏览器静默阻断，改为拦截上抛、宿主校验 token 后走系统浏览器。url 须 http(s) 白名单且 ≤2048。
- **绝不以 `event.origin` 判宿主**（pi 实证陷阱：origin 是发送方 origin，非 `null`）；来源校验只认 token 凭证，`targetOrigin` 固定 `'*'`（opaque origin 无从指定宿主 origin，安全由宿主侧 token 守卫兜底）。

## 10. 设置项

`settings.json` 新键 `claudeWebUIEnabled`（`*bool` 三态，`nil` = 默认 true，照 `agentCLIEnabled` 模式；`internal/settings/service.go` `GetClaudeWebUIEnabled`/`SetClaudeWebUIEnabled`）。设置页入口：终端设置 → 「启用 Claude Code Web 视图」（说明口径：为内嵌 Claude Code 会话提供网页视图平面；WSL/外部终端会话不适用）。**该开关只在会话启动时读取**；运行中翻转不影响已起平面。

## 11. 移动端与远程入口（引用）

移动端与远程反代面完整契约见 `docs/developer/remote-api-v1-contract.md` §3.1（五态 `WebUIStatus`、per-device plane token 铸造、反代改写与写面门）。claude 平面对该面零差别复用：`GET /session/{id}/webui` 状态端点、`/webui/{sid}/...` 反代、写面集合与设备身份透传（§5）均按同一契约工作。
