# Claude Code WebUI 视图模式：CodeBox 实现设计

> 修订记录：v1.2（2026-11-14）——独立复审阻断修订：① **SPA 双部署形态**（阻断 2-1）：同一份 webui-claude SPA 必须同时运行于桌面根路径 `http://127.0.0.1:<port>/` 与远程子路径 `https://<remote>/webui/{sid}/`（契约 webui.go:73-107 强制相对代理 URL、statusOf service.go:458 强制回环根路径）——新增 §7.2 双 base 约束（vite `base:'./'`、API/WS 路径运行时从 `location` 推导）与对应验收门禁；② **构建集成可落地化 + CI/release 接入**（阻断 2-2）：Wails preBuildHooks 是平台键→单条命令的 map（`*/*` 已被 build:mobile 占用；本机模块源码核实 hook 经 `shlex.Split` + 直接 exec，`&&` 为字面参数不可用），改为**根 package.json 聚合脚本**方案；新增 t12 覆盖 `.github/workflows/{ci,release}.yml`（现仅构建 frontend/mobile，SPA 无任何 CI 门禁与产物路径）；③ t1 补真实转录采样步骤与 fixture 文件；④ 转录根补 `CLAUDE_CONFIG_DIR` 优先规则（claude_accounts_launch.go:585-590 受管链剥离该键但普通会话不剥离）；⑤ 更正 managedHomeEnvKeys 语义表述（它是检测键表 app.go:2653-2665，HOME 重定向实际来源是用户自定义 env）；⑥ §7.2 补 body 透明约定与 draft 403 静默容错；⑦ R8 笔误（t6→t11）。v1.1（2026-11-14）——评审阻断修订：① 远程 Web 平面输入从 `control.DesktopInput`（take-first 夺权语义）改为**双车道**：本地输入走桌面车道（原语义），经反代的设备输入走新增的**设备平面车道**（无夺权、写时校验持控），反代在写面转发时透传已鉴权设备身份（§5.2/§5.5/§5.6）；② 补移动端入口任务 t10（WorkspacePage 闸门泛化，§7.4）；③ 修订交付边界与 R12 的 HOME 重定向口径矛盾；④ t6/t7 文件清单补全；⑤ 引用精化。v1.0（2026-11-14）——初版。

让 CodeBox 内启动的 Claude Code 会话获得与 pi 会话同构的 Web 平面：`/terminal` 视图内「终端 ⇄ 网页」双平面切换、回环 token 鉴权的 HTTP+WS 数据面、远程反代与写面控制门复用。与 pi 的差别只有一处本质：**pi 的数据面 server 由 amagi-pi 扩展进程实现，Claude Code 没有等价扩展宿主，因此数据面 server 移入 CodeBox 进程内（Go 实现）**——结构化流来自 Claude Code 落盘的会话转录 jsonl（`~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl`，HOME 被重定向时按进程 env 解析），输入面经控制门写回 PTY，与 TUI 平面共享同一进程同一上下文。

**交付边界：v1 覆盖桌面 embedded（原生目标）claudecode 会话的完整链路（视图/历史/输入/中断/远程反代/移动端入口），remote v1 创建/重启链经 `--session-id` 注入同样覆盖；远程输入/中断走设备平面车道（无夺权，写时持控校验，§5.6）。同一份 SPA 以双形态部署（桌面回环根路径 + 远程代理子路径 `/webui/{sid}/`，§7.2 是硬约束），发布链（wails hook 聚合脚本 + CI/release workflow）必须产出真实 dist（§7.3/t12）。WSL 目标与外部终端/VSCode/Zed 模式明确降级为无 Web 平面（状态 unknown，前端隐藏切换控件，与 A-4 同语义）；HOME 重定向（用户自定义 env）与 `CLAUDE_CONFIG_DIR` 自定义会话**支持**：转录根从进程 env 提取（HOME/USERPROFILE/CLAUDE_CONFIG_DIR，§5.1；受管 claude-accounts 会话在最终边界剥离用户自定义 CLAUDE_CONFIG_DIR，回落 home 根）。转录 jsonl 是 Claude Code 的落盘事实而非官方冻结契约，流式粒度是「API message 完成」而非「块级增量」——这是数据源决定的硬边界，见 §13-R1/R3。**

本文为设计产物。以下源码锚点均在本会话亲自读取核实（仓库根 `X:\WorkSpace\amagi-codebox`）；未运行构建、测试或真实 Claude Code 会话（评审方在其报告中运行过 `go vet -mod=vendor` 的分包基线，均 exit 0，属评审方执行而非本文作者）；三路调研摘要中的 GitHub 活跃度数据未复核，仅作选型背景。测试与真机验证为后续实施验收步骤。

---

## 1. 源码核实清单

| 锚点 | 核实结论 | 对设计的影响 |
|---|---|---|
| `app.go:2543` LaunchSession、`2756-2845` embedded 分支 | claudecode 唯一桌面 embedded 启动路径：`2778` 生成 `claudeSID := uuid.NewString()`、`2779` `SetClaudeSessionID`、`2782` `resolveEmbeddedLaunchSpec(..., []string{"--session-id", claudeSID})`、`2816` `launchEmbeddedPTYWithClaims`、`2823` `SetPID`、`2832-2843` 退出监控 goroutine | 转录文件路径在启动前即确定（方案 R 锁定）；Web 平面注入点唯一且已有全部所需材料（sid/workDir/pid/env） |
| `internal/appmeta/claude/jsonl.go:38,89,112,173,315` | `pathSepReplacer`（`:\/`→`-`）、`SessionJSONLPath`、`FindLatestActiveJSONL`（mtime 最新回退）、`ExtractFirstUserMessage`、`ExtractUsageRecords`（startOffset 字节断点续传 + `bufio.Reader.ReadBytes` 按需扩容吞超长行）。**注意：`jsonl.go:265-289` 只含 usage 三结构（usageAssistantMessage/usageMessageUse/usageRecord）；`content[].type/id/name/input/tool_use_id/is_error` 等块级字段名不在本仓任何代码中，属 Anthropic API 消息形状的外部知识** | 路径编码、增量读取、容错逐行解析三件套在本仓已有成熟先例；块级字段必须以「实测转录样本回放」为单测依据并落契约文档（t1/t9 验收项，R1 缓解具体化） |
| `internal/session/tracker.go:53-100`、`internal/session/manager.go:124,139,349` | 标题 tracker 方案 R（锁定 sid jsonl，不跟随 /resume）/ 方案 P（mtime 最新回退）；Manager 有 `SetClaudeSessionID`/`GetClaudeSessionID`，List 直读历史 jsonl | Web 平面转录跟随沿用同一判定语义；remote 链补 sid 后 Manager 记录自动受益 |
| `internal/webui/service.go:120,137,147,158,176,204,450` | tracker 五态状态机、`RegisterSession(sessionID,pid,port,token)`、`SessionEndpoint`、`Invalidate`/`RemoveSession`、`ProbeWebUI`/`OpenWebPlane`、`statusOf` 构造 `http://127.0.0.1:<port>/#/t=<token>`；**代码路径按 sessionID 键控，无 appType 判定**（`app.go:879-905` GetSessionWebUI/ProbeSessionWebUI/sessionWebUIInfo 同） | **单服务复用可行**：claudecode 会话注册进同一 `webui.Service`，前端/remote 零改动获得状态机；复用点补一行「pi 与 claudecode 共用」注释即可（不改行为） |
| `internal/webui/probe.go:39-45,51-96,119` | `/api/info` 探测契约：`v/ready/sessionId/pid/port`、`v==1` 强校验、Bearer 携带、503 pid 校验；注册表回退按 pid/token/sessionId 匹配 | 新 server 只要满足 `/api/info` 契约即被现有探测采纳；claude 注入端口直连命中，注册表回退扫描（`~/.pi/...`）因 pid/token 必然失配而天然不误采纳 |
| `internal/webui/port.go:23`、`token.go:24` | `AllocateFreePort`（回环空闲口）、`GenerateToken`（128bit hex） | 两纯函数 CLI 无关，直接复用 |
| `app.go:3930-3951,4012-4028` | pi 的注入时序：env 注入（失败仅 WARN）→ PTY 成功 → `RegisterSession`；退出 goroutine `Invalidate` | claude 平面照抄该时序骨架，仅把「env 注入」换成「进程内起 server」 |
| `internal/remote/control_runtime.go:565-602`、`internal/remote/control_gate.go:190-202` | `DesktopInput→DoDesktopPTY` 是 **take-first**：注释原文 "Intentional input: take desktop first (preempts device holder)"，每次调用先 `TakeDesktop`；`DesktopPasteChunk` 同门（1KB 分块 + Checkpoint） | **v1.1 核心修正依据**：本地 WebUI 输入沿用桌面车道（与 xterm 打字同语义，夺权是本地用户意图，正确）；远程设备输入绝不能走此道（否则第一条输入即夺走设备控制权，与反代写面门自相矛盾） |
| `internal/remote/control_arbiter.go:392-401`、`internal/remote/control_gate.go:384-409,646-675` | arbiter 注释明确 "DoDesktopPTY runs TakeDesktop on every keystroke, preempting any device holder"；`DoDevicePTY` 需**精确 live lease**（`createDevicePTYPermit` 校验 deviceID+connectionID+attachmentGeneration 三元组，且 `phase != deviceConnected → DenyNotController`——grace 期 WS 输入禁止）；ws/v1 输入即走此道（`ws_v1_session.go:1110-1119`） | 设备 WS 车道不可复用（plane server 是 HTTP 请求作用域，拿不到连接租约）→ 需新增**设备平面车道**原语（§5.6）：按 deviceID 校验持控快照（含 grace 宽松，镜像反代写面门）+ `mintOperationPermit(ctx, entry, deviceID, 0)`（该无需租约的铸证原语已存在，`control_gate.go:687`，lifecycle 路径在用） |
| `internal/remote/webui_proxy.go:68-72,334,466-476,499,540-561` | `/webui/{sid}` 反代：写面门按 `SnapshotForDevice` 快照放行（**grace 持有者视为控制者——有意的文档化宽松**，540-546 注释）；出向 Host 重写 127.0.0.1、Bearer 覆盖注入、WS 子协议 `webui,<token>` 换写（587-589）；写面集合 `webuiBackendWriteFaces` 现为 `{POST /api/input, POST /api/agent-interact, PUT /api/draft}` | 反代是设备身份的权威解析点（plane token→deviceID）→ 由它在写面转发时透传设备身份（§5.5）；新增 `POST /api/interrupt` 必须同步加进写面集合；设备平面车道的持控判定必须镜像这里的 grace 宽松，否则代理放行、后端拒绝的口径分裂 |
| `internal/remote/contract/webui.go:28-110` | 五态 wire 契约、`/session/{id}/webui` 端点、URL fragment 承载 token；**远程平面 URL 冻结为相对代理路径 `/webui/{sid}/`（无 query，ValidateWebUIStatus:100-108 强制）**；contract 包仅依赖标准库 | 新 server 响应形状服从同一红线；**远程 iframe 运行于子路径 `/webui/{sid}/`，桌面 iframe 运行于回环根路径（service.go:458 statusOf）——同一 SPA 双 base 是硬约束（§7.2，阻断 2-1）**；设备身份透传头常量落在本包（additive，两侧共同 import 无环） |
| `app.go:6052-6075` | `App.PtyWrite` 经 `control.DesktopInput`（fail-closed） | 桌面车道适配器照抄该口径 |
| `app.go:687-692,747-752,3177-3216,3267,3313` | webui tracker 生命周期三处 commit 点（run 终结清理 / v1 remove GC / Remove·ClearStopped） | claude server 必须同样挂三处，否则 listener 泄漏 |
| `bind_list.go:17-41`、`bind_manifest_test.go` | Bind 清单冻结 21 项；raw `pty.Service` 禁 Bind | **本设计零新增绑定**：新包与新控制原语都不进 Bind |
| `launch_executor.go:49-69,242-279,1063-1083,1085-1095` | remote 链 `webuiLaunchPlane` 接口；`buildPTYStart:257` 以 `spec.AppType == AppTypePi` 门控；`commitWebUIPlane` 在 `MarkCommitted` 后注册；Abort 零残留 | claudecode 接入需新的平面接口（要起 server，不只发 env）；负例测试 `launch_webui_plane_test.go:193` 需同步 |
| `launch_planner.go:258-371`、`internal/platform/resolver.go:91,162,184,237` | `buildClaudePlan` 纯读构建 effects，**未注入 `--session-id`**；`ResolveRequest.CLIArgs` 并入 argv 的消费点共三处：WSL 分支 `:162`、原生分支 `resolveCLIForRequest :184`、attach 分支 `buildAttachStartupCommandForShell :237`（v1.1 精化：三处均生效） | remote 链 claude 会话当前无锁定 jsonl（方案 P 串扰场景）；补 sid 只需 planner 传 CLIArgs + ProcessStartSpec 带出，三分支天然覆盖 |
| `internal/launchplan/types.go:106-110` | `ProcessStartSpec{Mode, Resolved, RequireRunHandle}` | 新增可选 `WebUIPlane` 字段承载计划期生成的 sid（typed spec，符合本仓风格） |
| `claude_accounts_launch.go:585-593`、`app.go:2653-2665` | `managedHomeEnvKeys = {HOME, USERPROFILE}` 是**检测键表**（`managedHomeOverride` 据此检测用户自定义 HOME 覆盖 → 受管 OAuth 启动拒绝/免 lease），claude-accounts 本身不重定向 HOME——HOME 重定向的实际来源是用户自定义 env；`managedClaudeIdentityEnvKeys` 含 `CLAUDE_CONFIG_DIR`（受管链最终边界剥离用户自定义值，普通 API-key 会话不剥离） | 转录根提取规则（v1.2）：spec env `CLAUDE_CONFIG_DIR` 优先，否则 `<HOME/USERPROFILE>/.claude`——两种 HOME 重定向来源与 CLAUDE_CONFIG_DIR 自定义会话都被覆盖；受管会话剥离后自然回落 home 根 |
| `frontend/src/components/terminal/TerminalView.vue:342,349-354,367-373,510-512` | `isPiSession` 与 `webUIToggleVisible` 是切换控件唯一闸门；`preferWebPlane` 自动切 Web；`onMounted` 仅 pi 启动探测 | 闸门改为「支持平面集合」；claude 不自动偏好（§4-D5） |
| `frontend/src/components/terminal/WebPlaneHost.vue:15,130`、`quickPathInsert.ts:26,29,49,94` | iframe `sandbox="allow-scripts allow-forms"`、10s 加载看门狗；宿主桥冻结契约 `amagi:insert-input`/`amagi:open-url`（token 凭证）；`CAPABILITY_PATTERN`、`withWebPlaneSkinParam` | WebPlaneHost CLI 无关直接复用；新 SPA 实现两个桥即可免费获得「快捷功能·输入工作路径」 |
| `frontend/src/stores/session.ts:44-77`、`frontend/src/api/webui.ts:20-32` | 探测轮询按 sessionId 键控，与 appType 无关；api 包装零 appType 判定 | **store 与 api 模块零改动**，只改 TerminalView 的启动条件 |
| `mobile/src/views/WorkspacePage.vue:53-56,60,80-82,307,318-325`、`mobile/src/composables/useSessionWebUI.ts:20-23,43` | 移动端 Web 平面入口被 `isTuiCli = (pi‖omp)` 闸死：探测 enable 条件（:60）、非 TUI CLI 缺省视图固定 timeline（:80-82）、「切换至 Web 平面视图」菜单项在 `v-if="isTuiCli"` 内（:307,318-325）；composable 本体 appType 无关但 `enabledRef=false` 直接不探测（:43）；既有测试 `mobile/src/__tests__/views/WorkspacePage.webplane.test.ts` | **v1.1 补移动端任务 t10**：闸门泛化为 webPlaneCapable（pi/omp/claudecode），claudecode 缺省视图仍 timeline（不劫持），仅显式菜单/深链进入 webplane |
| `internal/settings/service.go:160-187,818-831` | `AgentCLIEnabled *bool` 三态模式 + Get/Set 方法；无 settings Pinia store，视图直调 api | `ClaudeWebUIEnabled` 照抄该模式；settings.Service 新方法 → wailsjs 需再生成（唯一再生成点） |
| `go.mod`（gorilla/websocket v1.5.3 已 vendor）、`.gitignore`（frontend/dist、mobile/dist 均忽略；`git ls-files` 证实 mobile/package-lock.json 已入库，`npm ci` 可用） | WS 库零新增依赖；构建产物策略 = gitignore + 构建钩子先建后编；**独立工程要用 `npm ci` 必须提交 lockfile** | claude SPA 构建照 mobile 模式接入且 **package-lock.json 入库**（t7）；`internal/claudewebui/dist` 需提交占位文件防 `go vet` 空目录 embed 失败（§13-R6） |
| `wails.json:10-12`（preBuildHooks 现为单键 `*/*`→`npm --prefix ../.. run build:mobile`）+ **Wails v2.11.0 模块源码亲自核实**（`pkg/commands/build/build.go:106-111`：`{plat}/{arch}`、`{plat}/*`、`*/*` 三键依次各执行一次；`executeBuildHook`：`shlex.Split` 后 `shell.RunCommand` 直接 exec 单条命令，不经 shell——`&&` 会成字面参数；JSON 同键重复会被解码覆盖） | preBuildHooks 是平台键→**单条命令**的 map，`*/*` 已被 build:mobile 占用，同键并列/&& 串接均不可行（阻断 2-2a） | 采用**根 package.json 聚合脚本**方案：`*/*` 钩子改指 `npm --prefix ../.. run build:embedded-web`（= `npm run build:mobile && npm run build:claude-webui`，npm run 经 shell 执行 && 合法）（§7.3/t12） |
| `.github/workflows/ci.yml`（frontend-mobile job：手动 npm ci+build+test 仅 frontend/mobile，上传 embedded-web-assets artifact 路径仅 frontend/dist、mobile/dist；go-quality 下载后 vet/test；npm cache-dependency-path 仅两 lockfile）、`release.yml`（两个 job 同样手动构建 frontend+mobile 后 `wails build -s`，cache 路径同） | **CI/release 完全不构建不测试 webui-claude，SPA 无任何回归门禁与产物路径**（阻断 2-2b）：照 t1–t11 做完也不构成可发布链路——发布产物 dist 可能只有 .gitkeep 占位 → 用户打开平面见提示页 | 新增 t12：ci.yml frontend-mobile job 补 webui-claude ci/build/test + artifact 路径加 `internal/claudewebui/dist` + cache 路径加 lockfile；release.yml 两个 job 镜像 mobile 步骤补 webui-claude ci+build + cache 路径 |
| `frontend/src/components/terminal/WebPlaneHost.vue:17,150-153`（iframe `@load` → onFrameLoad 置 loaded 并停看门狗） | iframe 的 load 事件在 HTML 本身 200 而子资源 404 时照常触发——宿主无法侦测 SPA 白屏；白屏防线必须在 SPA 内部（资源相对化 + 页内错误态） | §7.2 双 base 约束 + SPA 首屏自带 token 缺失/info 失败的页内错误态（资源相对化后子资源随任一 base 正确加载，白屏根因消除） |

## 2. 目标与非目标

**目标**
1. 桌面 embedded claudecode 会话（原生 Windows/macOS 目标）在 `/terminal` 获得「终端 ⇄ 网页」双平面，交互与 pi 会话一致（切换控件、加载/失败/结束态、皮肤、快捷功能路径插入、外部链接经宿主打开）。
2. Web 平面提供：会话消息流（用户/助手/思考/工具卡配对/用量四分项）、输入（提交/排队语义与 TUI 一致）、中断（Esc）、草稿暂存（服务端内存）、忙闲指示。
3. 复用 `webui.Service` 单实例状态机与 remote `/webui/{sid}` 反代；**远程写面（input/interrupt/draft）受既有控制门约束且不夺权**——设备输入走设备平面车道（§5.6），连发不因夺权自锁。
4. remote v1 创建/重启的 claude 会话同样获得平面（`--session-id` 注入消除同 workDir 串扰）；移动端（companion app）获得 Web 平面入口（t10）。

**非目标（明确出界）**
- 不包装、不代理、不 fork Claude Code 本体；不引入 Node sidecar、Claude Agent SDK、ACP 适配进程（§4-D1 记弃置理由）。
- 不实现块级实时流式（转录落盘粒度 = API message 完成）；不做会话分叉/检查点（opcode 范式，后续可期）。
- 不覆盖 WSL 目标与外部终端/VSCode/Zed 模式（降级为无平面）；不改变移动端 claudecode 会话的缺省视图（timeline 保持缺省，Web 平面仅显式进入）。
- 不改 pi 既有链路的任何行为。

## 3. 总体架构

```
┌ desktop frontend (Vue3, /terminal) ┐
│ TerminalView ── TUI plane: xterm.js │
│      ⇅ plane toggle (webPlaneCapable)│
│   Web plane: WebPlaneHost iframe ───┼── http://127.0.0.1:<port>/#/t=<token>   ← 桌面形态（回环根路径）
└──────────────┬─────────────────────┘        │ (opaque sandbox, 直连)
               │ ProbeWebUI/OpenWebPlane      ▼
               │ (wails webui.Service, 复用)  ┌ internal/claudewebui (Go, 新) ─┐
               ▼                              │ net/http 只 bind 127.0.0.1     │
        webui.Service tracker ───────────────►│ /api/info 探测契约 (v1)         │
        (五态状态机, 单实例复用)               │ /api/history /api/input         │
                                             │ /api/interrupt /api/draft       │
                                             │ /ws/events (gorilla, 子协议鉴权)│
                                             │ / 静态 SPA (go:embed dist)      │
                                             └───┬──────────┬─────────────────┘
                                       转录尾随 │          │ 输入面（双车道）
                                 ~/.claude/projects/      ├─ 无 X-Amagi-Plane-Actor-Device 头
                                 <encoded-cwd>/<sid>.jsonl│   → 桌面车道 control.DesktopInput /
                                 (internal/appmeta/claude │     DesktopPasteChunk（take-first，
                                  增量解析, offset 断点;   │     与 xterm 本地打字同语义）
                                  HOME/USERPROFILE 提取)  └─ 有头（仅反代设置）
                                                       → 设备平面车道 control.DevicePlaneInput/
mobile/remote 反代 /webui/{sid} ─► webui_proxy.go ────────  DevicePlanePasteChunk（无夺权，
  · 写面门 {input, agent-interact, draft, interrupt}       写时校验该设备持控，§5.6）
  · plane token→deviceID；写面转发透传                     ▲
    X-Amagi-Plane-Actor-Device（覆盖式，剥离客户端值）      │
  · Host 重写 + Bearer 覆盖 + WS 子协议换写（全部既有）─────┘
  · 移动端 iframe 形态：https://<remote>/webui/{sid}/#/t=<plane-token>
    （同一 SPA 双 base 部署，§7.2 硬约束——资源/ API/WS 全部相对化推导）
```

要点：
- **同一会话双平面共享同一 claude 进程**：Web 输入 → 控制门 → PTY → claude TUI 消费（xterm 平面同步可见）；Web 展示 ← 转录 jsonl ← claude 落盘。无第二进程、无第二上下文。
- **输入双车道**（v1.1）：本地（桌面 iframe 直连回环 server）与远程（经反代）的写入走不同控制语义——本地是桌面用户意图（take-first，与在 xterm 打字一致）；远程是已持控设备的持续输入（无夺权，否则第一条输入即吊销自己的控制权与 ws/v1 输入通道）。
- server 生命周期严格跟随 PTY：启动成功后 Start+Register，PTY 退出 Stop+Invalidate，会话删除 Stop+Remove（三处 commit 点对齐 pi）。

## 4. 关键决策与取舍

| # | 决策 | 选择与理由 | 代价 / 弃置项 |
|---|---|---|---|
| D1 | 结构化流来源 | **CodeBox 进程内 Go server + 转录 jsonl 尾随 + PTY 输入回写**。仓库已有三块基石：`--session-id` 注入（app.go:2778）、jsonl 增量解析先例（jsonl.go:315）、控制门输入口（control_runtime.go:565） | 弃 claude-agent-sdk headless 会话（claudecodeui/happy 路线）：那是**另一个 claude 进程另一份上下文**，与「给本会话加视图」目标直接冲突，且引入 Node sidecar 违反单二进制 Go 体系；弃 ACP（AionUi 路线）：同样是并行进程 + 需要对端适配器。记为后续演进方向 |
| D2 | 数据面宿主位置 | **CodeBox 进程内（internal/claudewebui）**。claudecode 无扩展宿主消费 `AMAGI_WEBUI_*` env（pi 前置事实），env 注入通道对 claude 无意义；tracker 直连注册（端口/token 由我方持有） | 与 pi 的差异写进契约文档；换来无需探测回退注册表、无 bind 失败兜底问题（bind 失败 = 不注册 = unknown，干净降级） |
| D3 | webui.Service 复用 | **复用单实例**。tracker 键 = CodeBox sessionID（同一 ID 空间），探测契约自实现服从；注册表回退对 claude 会话天然不可能误采纳（pid/token 必失配） | 不新建第二 Service/绑定；internal/webui 不进任何 claude 逻辑；复用点（RegisterSession 调用处）补一行「pi 与 claudecode 共用」注释，不改行为 |
| D4 | 新包定位 | `internal/claudewebui`：per-session 回环 server + WS hub + 转录尾随 + 双车道输入端口；`internal/appmeta/claude` 增量扩转录解析器（该域已拥有路径编码/解析） | 域边界：appmeta 只做「解析」，claudewebui 做「服务」 |
| D5 | 前端自动偏好 | claude 会话**不自动切 Web**（`preferWebPlane` 仅 pi；移动端缺省视图同理保持 timeline）。理由：claude 的既有默认平面（桌面 TUI/移动 timeline）不应被内置恒 available 的 webui 劫持；pi 自动切是「装了插件才 available」的前提性需求 | 切换控件、结束态、重试、皮肤、快捷功能桥全部照 pi 语义；仅默认平面取向不同 |
| D6 | 输入语义与车道 | 写 PTY 无条件（与用户在 xterm 打字等价）；**车道按来源分流**：无身份头 = 桌面车道（DesktopInput，take-first——本地用户意图，正确）；有 `X-Amagi-Plane-Actor-Device` 头（仅反代设置）= 设备平面车道（无夺权，写时校验持控，§5.6）。busy 只影响 UI，不做服务端忙闲门控 | TUI 处于菜单/对话框态时 WebUI 输入会选中菜单项——与「共用一个键盘」同构，v1 接受并文档化（§13-R2）；新增一个控制原语（t6） |
| D7 | 静态资产 | `webui-claude/` 独立 Vite 工程（Vue3+TS），outDir 指向 `internal/claudewebui/dist`，`go:embed all:dist`；构建接 preBuildHooks（照 mobile 模式，**含 package-lock.json 入库以支撑 `npm ci`**）；dist 提交 `.gitkeep` 占位 | 构建顺序约束；开发期 `?mock=1` 假数据源免除后端依赖 |
| D8 | WS 选型 | gorilla/websocket（已 vendor）。子协议鉴权 `["webui", token]` 手工校验后仅回显 `webui`（对齐 pi 后端行为，remote 反代依赖该形状） | 不手写 RawWebSocket（pi 零依赖是 Node 侧约束，Go 侧无此约束） |
| D9 | 设备写通道（v1.1 新增） | **新增控制原语 `DoDevicePlanePTY`**（gate）+ `DevicePlaneInput/DevicePlanePasteChunk`（runtime）：不改变控制权归属；写时在 stateMu 下校验 holder == 该 deviceID（connected 或 grace——镜像反代写面门 `SnapshotForDevice` 的文档化宽松，`webui_proxy.go:540-546`），revoked/accepting 校验镜像 `createDevicePTYPermit`；铸证用既有 `mintOperationPermit(ctx, entry, deviceID, 0)`（`control_gate.go:687`，无需连接租约，lifecycle 路径先例） | 不复用 `DoDevicePTY`（需精确 live 连接租约，HTTP 请求作用域拿不到，`control_gate.go:384-409`）；不复用 `DoDesktopPTY`（take-first 夺权，阻断项 1 根因）；grace 宽松与 WS 车道 connected-only 形成有意对照，契约文档记录理由 |

**调研冲突裁定（要求 3）**：开源项目（AionUi/happy 的 ACP+SDK、claudecodeui 的 SDK 驱动、opcode 的进程注册表）一律以 CodeBox 既有体系优先——D1/D3/D9 即裁定记录。唯一采纳的开源共性是「读 `~/.claude/projects` jsonl」这一事实性路线（opcode/claudecodeui 均依赖），它恰好与 CodeBox 已有的 appmeta 域重合。

## 5. 后端设计

### 5.1 转录解析器（internal/appmeta/claude/transcript.go，新）

在既有 `jsonl.go` 旁新增，复用其路径与容错惯例：

```go
// TranscriptRecord 是单条 jsonl 行的中立投影（wire 形状见 §6.3）。
type TranscriptRecord struct {
    Kind        string   // "user" | "assistant" | "system"
    UUID        string
    IsHuman     bool     // user 行 origin.kind=="human"（区分真实输入与 tool_result 回写）
    Text        string   // 文本内容（string content 或 text 块拼接；user-human 行必填）
    Thinking    string   // assistant thinking 块拼接
    ToolUses    []ToolUse      // assistant content 中 type=="tool_use" 块
    ToolResults []ToolResult   // user 行 content 数组中 type=="tool_result" 块
    Usage       *Usage         // assistant 行 message.usage 四分项（字段名对齐 jsonl.go:275-280 usageMessageUse）
    Model       string
    Timestamp   string   // 原样 ISO 串，前端格式化
}

// ReadTranscriptIncrement 从 offset 字节起增量解析（对齐 ExtractUsageRecords 的
// 断点续传与 ReadBytes 扩容惯例），返回新记录、新 offset、文件是否被截断重建。
func ReadTranscriptIncrement(path string, offset int64) (recs []TranscriptRecord, newOffset int64, truncated bool, err error)
```

细则：
- 行级容错：单行 unmarshal 失败跳过（jsonl.go:191 惯例）；`message.content` 二次判型（string vs 数组）。
- `truncated`：新文件 size < offset（claude `/resume` 重写或换文件）→ 调用方从 0 重读全量。
- **转录根解析助手（v1.2 新增）**：`ClaudeConfigRoot(homeDir string, env []string) string`——spec env `CLAUDE_CONFIG_DIR` 非空则以其为根，否则 `<homeDir>/.claude`（覆盖用户自定义 CLAUDE_CONFIG_DIR 的普通会话；受管 claude-accounts 会话在最终边界剥离该键，自然回落 home 根，§1 表）；配套 `SessionJSONLPathInRoot(root, workDir, sessionID)` 与 `FindLatestActiveJSONLInRoot(root, workDir)`（既有 `SessionJSONLPath`/`FindLatestActiveJSONL` 签名不动，既有调用方零改动；新函数内部复用同一 pathSepReplacer 编码）。
- **字段来源声明**：`ToolUse/ToolResult` 的块级字段名（`content[].type/id/name/input/tool_use_id/is_error`）属 Anthropic API 消息形状的外部知识，本仓代码中不存在（§1 表已核实）；t1 必须**先在本机跑一次真实 claude code 会话采集 jsonl 固化为 testdata fixture**（`internal/appmeta/claude/testdata/transcript.sample.jsonl`，采集时脱敏——不含本机路径外的敏感内容，只保留覆盖全部已解析字段的最小轮次），单测以该 fixture 回放为依据，字段清单落 `docs/developer/claude-webui.md`（t9 验收项，R1 缓解具体化；禁止手写想象样本）。
- 不做 toolUse↔toolResult 服务端配对（客户端按 id 配对，§6.3）；isSidechain 行透传标记，v1 折叠展示。

### 5.2 internal/claudewebui 服务包（新）

```go
// PlaneConfig 一次平面的全部材料（App/executor 组装）。
type PlaneConfig struct {
    SessionID       string // CodeBox 会话 ID（tracker 键 / info.sessionId）
    ClaudeSessionID string // --session-id 注入值（锁定转录文件名；空 = 尾随最新 mtime，方案 P 语义）
    WorkDir         string // spec.WorkDir（最终生效目录）
    TranscriptRoot  string // 转录根（ClaudeConfigRoot 解析结果：CLAUDE_CONFIG_DIR 优先，否则 <HomeDir>/.claude；v1.2）
    PID             int    // claude PTY pid（info.pid / ended 存活校验）
    Port            int    // 预分配端口（webui.AllocateFreePort；0 = 自选）
    Token           string // webui.GenerateToken（必填）
    Settings        RuntimeSettings // 轮询周期/环形缓冲上限/历史分页上限（测试 seam）
}

// InputPort 是写回 PTY 的窄端口（App 适配 control 门，双车道——v1.1）。
type InputPort interface {
    // 桌面车道：与 App.PtyWrite/xterm 本地打字同语义（take-first）。
    WriteInput(ctx context.Context, sessionID string, data []byte) error      // ≤1KB 单发
    PasteChunk(ctx context.Context, sessionID string, data []byte) error      // 长文本 1KB 分块
    // 设备平面车道（§5.6）：无夺权；仅当 deviceID 当前持控（connected/grace）才写入，
    // 否则返回控制门 DenyNotController 语义错误（server 映射 403 control.forbidden）。
    DeviceWrite(ctx context.Context, sessionID, deviceID string, data []byte) error
    DevicePasteChunk(ctx context.Context, sessionID, deviceID string, data []byte) error
}

// Manager 管理全部 per-session server（server.go；App 持有；非 Wails 绑定）。
type Manager struct { /* log, mu, planes map[string]*plane */ }
func (m *Manager) Start(cfg PlaneConfig, input InputPort) (port int, err error) // 同 sessionID 幂等替换（先 Stop 旧）
func (m *Manager) Stop(sessionID string)                                        // 幂等
```

plane 内部：
- **server**（server.go）：`net/http.Server` 只 bind `127.0.0.1:<port>`；Host 校验 `^127\.0\.0\.1(:\d+)?$`（对齐 pi，remote 反代已按此重写）；静态 SPA 走 embed FS（assets.go，`//go:embed all:dist`；dist 仅占位时返回提示页并 WARN，探测/数据面不受影响）。
- **auth**（auth.go）：`/api/*` 与 `/ws/*` 强制 `Authorization: Bearer <token>`（`subtle.ConstantTimeCompare`）；CORS：API 响应 Origin==null 时 `ACAO:null`，静态资源 `ACAO:*`；OPTIONS 预检本地 204（Authorization, Content-Type），不携 token——逐条镜像 pi 安全模型。
- **tailer**（tailer.go）：goroutine 周期（默认 500ms）`os.Stat` mtime 变化才读；`ReadTranscriptIncrement` 增量；路径用 `SessionJSONLPathInRoot(cfg.TranscriptRoot, cfg.WorkDir, cfg.ClaudeSessionID)`（sid 空 → `FindLatestActiveJSONLInRoot` 回退，方案 P 语义）；产出 `transcript`（新记录批 + cursor=byteOffset）与派生 `busy`。busy 启发式：最后一条有效记录是 human-user（等首响应）或存在未配对 tool_use（工具执行中）→ true；最后为无 pending tool_use 的 assistant → false；无记录 → false。转录文件未出现（claude 未写首条）→ 空流继续等待，`/api/info` 已 ready（server 起即 ready，语义 = 服务就绪而非会话有内容）。
- **history**：全量快照 = tailer 当前缓冲投影（首启重读文件一次），`GET /api/history?limit=N` 返回最新 N 条 + cursor + busy + title（首条 human 记录直接得出）；条数上限 Settings.HistoryLimit（默认 500，超限丢最旧并附 `truncatedHead:true`）。
- **input**（input.go）：`POST /api/input {text, submit}`：text ≤128KiB；**车道分流**——请求携带非空 `X-Amagi-Plane-Actor-Device` 头（常量 `contract.WebUIPlaneActorDeviceHeader`，仅远程反代在写面设置，见 §5.5）→ 设备车道 `DeviceWrite/DevicePasteChunk`，否则 → 桌面车道 `WriteInput/PasteChunk`；>1KB 走 PasteChunk 变体；`submit=true` 追加写 `\r`。`POST /api/interrupt {}`：同车道写 `\x1b`。车道错误映射：控制门未就绪 → 503 `service.down`（对齐 App.PtyWrite fail-closed 口径）；设备车道持控校验失败 → 403 `control.forbidden`（对齐反代写面门文案）。**头只是车道选择信号，不是鉴权**（鉴权永远是 Bearer token——capability token 视同会话权限，持 token 者本就能经桌面车道写入， spoofing 头不构成提权；反代仍剥离客户端自带值，见 §5.5）。
- **draft**：内存 map[sessionID]string，GET/PUT，128KiB 上限，空串即清除（pi 实证需求：sandbox iframe 无 localStorage 且宿主 url watch 会整页重载；draft 不写 PTY，无车道概念）。
- **ws**（ws.go）：`/ws/events`；升级前手工校验 `Sec-WebSocket-Protocol: "webui, <token>"`（第二段 constant-time 等值），通过则仅回显 `webui`（反代契约依赖）；信封 `{v:1, seq, ts, kind, event}`，seq 单调含 hello/bye；连接即推 `hello {stream:<server实例uuid>, floor, cursor, busy}`（stream 标识 = pi v1.0.13 教训：重连必须能识别流重建）；环形缓冲 4096，`?since=<seq>` 重放；纯下行（上行帧读丢）。

### 5.3 App 生命周期接线（桌面）

App struct 加 `ClaudeWebUI *claudewebui.Manager`（app.go:261-263 处），NewApp 构造（app.go:534 旁）；**不加 Bind**。

`LaunchSession` embedded 分支（app.go:2756-2845）：
1. `resolveEmbeddedLaunchSpec` 之后、`launchEmbeddedPTYWithClaims` 之前：若 `settings.GetClaudeWebUIEnabled()` 且 `spec.BootstrapMode != platform.BootstrapWSL`（WSL 降级）→ `port=webui.AllocateFreePort()`、`token=webui.GenerateToken()`（失败仅 WARN 并放弃平面，不阻断启动，对齐 pi 口径 app.go:3935-3951）；`transcriptRoot := claude.ClaudeConfigRoot(homeDir, spec.Env.Variables)`（homeDir 缺省 `os.UserHomeDir()`；CLAUDE_CONFIG_DIR 优先规则见 §5.1——覆盖用户自定义 HOME/USERPROFILE 与 CLAUDE_CONFIG_DIR 两类重定向）。
2. `SetPID` 之后（app.go:2823 后）：`ClaudeWebUI.Start(PlaneConfig{...claudeSID, workDir=spec.WorkDir, transcriptRoot, pid, port, token}, appInputPort{a})` 成功后 `a.WebUI.RegisterSession(sess.ID, pid, boundPort, token)`（复用点补一行「pi 与 claudecode 共用本服务」注释）；Start 失败 WARN 不注册（状态 unknown → 前端无切换控件）。
3. 退出 goroutine（app.go:2832-2843）：`MarkExited` 后追加 `a.ClaudeWebUI.Stop(id)` + `a.WebUI.Invalidate(id)`（对齐 pi 4025）。
4. `removeWebUITracker`（app.go:3212）扩展为同时 `ClaudeWebUI.Stop`；`SetRunTerminalCleanup`（app.go:687）与 `SetPostRemoveCleanup`（app.go:747）两处回调同样追加 Stop——三处 commit 点全覆盖，防 listener 泄漏（`app_webui_remove_test.go` 为回归基线）。

输入端口适配器 `appInputPort`（app_claudewebui.go 新文件）：
- 桌面车道：`WriteInput→a.control.DesktopInput(a.ctx, ...)`、`PasteChunk→DesktopPasteChunk`（control nil/未就绪返回 `remote.ErrControlNotReady`，对齐 App.PtyWrite app.go:6052-6060）。
- 设备车道：`DeviceWrite→a.control.DevicePlaneInput(a.ctx, ...)`、`DevicePasteChunk→a.control.DevicePlanePasteChunk(...)`（§5.6 新原语）。

### 5.4 remote/restart 计划链接入

- `internal/launchplan/types.go`：`ProcessStartSpec` 增 `WebUIPlane *WebUIPlaneSpec`，`WebUIPlaneSpec{ClaudeSessionID string}`（可选字段，nil = 无平面）。
- `launch_planner.go buildClaudePlan`：非 WSL 目标时 `sid := uuid.NewString()`（纯内存无副作用，符合 planner 纯读纪律），`resolveLaunch` 传 `CLIArgs: ["--session-id", sid]`（resolver 三消费点天然覆盖：WSL:162 / 原生:184 / attach:237，§1 表），`ProcessStartSpec.WebUIPlane = &WebUIPlaneSpec{sid}`。
- `launch_executor.go`：deps 增 `claudeWebUI claudeWebUILaunchPlane` 窄接口（`Start(cfg claudewebui.PlaneConfig) (int, error)` + `RegisterTracker(sessionID, pid, port, token)`——cfg 含全部 PlaneConfig 材料）；`buildPTYStart` 对 `spec.AppType == claudecode && proc.WebUIPlane != nil` 分支：不注 env（D2），把 **sid/workDir/transcriptRoot**（`claude.ClaudeConfigRoot(homeDir, spec.Env.Variables)`，与桌面 §5.3-1 同一助手函数对齐——CLAUDE_CONFIG_DIR/HOME 重定向的 remote 会话转录路径才不会错）存入 effect；`commitWebUIPlane` 的 claude 分支执行 `Start` + `RecordClaudeSessionID`（经接口暴露，App 适配器落 Manager）+ `RegisterTracker`；Abort 路径零调用零残留。
- `launch_webui_plane_test.go`：负例集合保持 codex/omp；新增 claude 正例（argv 含 --session-id、无 AMAGI_WEBUI_* env、commit 后 Start 参数含 sid/workDir/homeDir + 注册 + RecordClaudeSessionID、Abort 无残留）。

### 5.5 remote 反代与写面（internal/remote）

反代大部分**零改动自动覆盖** claude 平面（sessionID 空间一致、契约服从）：状态端点 `/session/{id}/webui`、per-device plane token 铸造、Host 重写、Bearer 覆盖、WS 子协议换写。**三处小改（v1.1）**：
1. `webuiBackendWriteFaces`（webui_proxy.go:68-72）追加 `{POST, "/api/interrupt"}`（宁紧勿松）。
2. **设备身份透传**：`handleWebUIProxy` 在写面（input/interrupt）转发前，若 principal 来自 plane token 解析（`principal.DeviceID != ""`），在 Director 中**先剥离客户端自带的 `X-Amagi-Plane-Actor-Device` 再覆盖式注入** `principal.DeviceID`（与 Authorization 覆盖注入同一纪律，webui_proxy.go:581-594）；非写面或零 principal（raw token 只读兼容通道）一律剥离该头不注入。
3. 头常量 `WebUIPlaneActorDeviceHeader = "X-Amagi-Plane-Actor-Device"` 落 `internal/remote/contract/webui.go`（additive wire 符号；claudewebui 与 remote 共同 import，contract 仅依赖标准库无环）。

### 5.6 设备平面车道（新控制原语，v1.1 阻断项 1 修复）

**问题回顾**（评审阻断 1，锚点见 §1 表）：远程设备经反代写面门（快照持控放行）转发 `/api/input` → 后端若走 `DesktopInput`，`DoDesktopPTY` 先 `TakeDesktop` 夺权（control_gate.go:197-201）→ 第二条输入起写面门判 control.forbidden 403，且该设备 ws/v1 输入通道被静默吊销——自相矛盾。pi 不受此累因其数据面在扩展进程内自行注入、不经 CodeBox 控制门。

**设计**：在 `internal/remote` 新增无夺权的设备写口，结构镜像 `DoDevicePTY`（control_gate.go:384-409：checkReady → entry public → acquireLaneOrQuarantine → 铸证 → runBoundedRawEffect → finishOperation），差异仅在身份校验：

```go
// control_gate.go
// DoDevicePlanePTY 执行一次设备来源的 Web 平面写（input/interrupt），
// 不改变控制权归属（no-take）。允许条件：holder 恰为该 deviceID 且处于
// connected 或 grace 相位——与反代写面门 SnapshotForDevice 的持控投影
// （grace 宽松，webui_proxy.go:540-546）保持同一口径；否则
// DenyNotController（fail-closed）。revokedDevices/acceptingRemote 校验
// 镜像 createDevicePTYPermit（control_gate.go:664-675）。铸证用
// mintOperationPermit(ctx, entry, deviceID, 0)（无需连接租约；
// lifecycle drain 路径的同款先例，control_gate.go:687/376）。
func (g *controlGate) DoDevicePlanePTY(ctx context.Context, deviceID contract.DeviceID, sessionID contract.SessionID, op PTYOperation, mutate RawEffect) error

// control_runtime.go —— 对称 DesktopInput/DesktopPasteChunk（565-602）：
// DevicePlaneInput  单发（≤1KB，PTYInput op）
// DevicePlanePasteChunk  1KB 分块逐 permit + Checkpoint（对齐 DesktopPasteChunk 栅栏语义）
```

- **grace 宽松的理由（与 WS 车道 connected-only 的对照，写进契约文档）**：WS 车道有精确 live 租约可验，grace 期禁止是防僵尸连接；平面 HTTP 写是请求作用域，身份由反代在每次请求时重新解析（plane token→deviceID→再校验设备未吊销/未过期，webui_proxy.go:433-447），持控判定与反代写面门同一快照口径即可，收紧只会造成「代理放行、后端拒绝」的口径分裂。
- **TOCTOU**：反代门放行 → 后端写前控制权被本地按键夺走 → 后端 DenyNotController → 403，客户端重新取控后重试。可接受，契约文档记录。
- 该原语**不进 Bind、不经 ws/v1**；仅 claudewebui 的设备车道消费。
- App 适配（§5.3 appInputPort 设备车道）。

## 6. 传输契约 v0.1（claude webui；实施时落 docs/developer/claude-webui.md）

冻结红线（继承 pi 契约纪律）：token 视同会话权限——只经 URL fragment `#/t=<token>` 下发，不进 query/path/请求行/日志/错误体；server 只 bind 127.0.0.1；错误体统一 `{v:1, error:"<code>"}`（错误响应也带 v）。

### 6.1 HTTP

| 端点 | 方法 | 鉴权 | 请求/响应要点 |
|---|---|---|---|
| `/api/info` | GET | Bearer | `200 {v:1, ready:true, sessionId:<codebox sid>, pid, port, appType:"claudecode"}`（五契约字段 + 附加字段，probe.go §2 前向兼容） |
| `/api/history` | GET | Bearer | `{v:1, items:[TranscriptRecord...], cursor, busy, title, truncatedHead}`；`?limit` 钳制 ≤ Settings.HistoryLimit |
| `/api/input` | POST | Bearer | `{text, submit?}` → `{v:1, ok:true}`；text >128KiB → 400；**车道**：携带非空 `X-Amagi-Plane-Actor-Device` 头（仅反代设置）→ 设备车道（§5.6），否则桌面车道；车道错误 → 403 `control.forbidden` / 503 `service.down` |
| `/api/interrupt` | POST | Bearer | `{}` → `{v:1, ok:true}`（同车道规则写 Esc） |
| `/api/draft` | GET/PUT | Bearer | `{v:1, draft:string}` / PUT body `{draft}`，128KiB 上限，空串清除（不写 PTY，无车道） |
| `/` 及静态资源 | GET | 无（ACAO:*） | embed SPA；hash 路由 `#/t=<token>` |

### 6.2 WS `/ws/events`

- 子协议：客户端 offer `["webui", "<token>"]`；第二段等值才升级，仅回显 `webui`。
- 信封 `{v:1, seq, ts, kind, event}`；`hello {stream, floor, cursor, busy}` → `transcript {items, cursor}` → `busy {value}` → `bye`（尽力而为）；`?since=` 环形重放（4096），缺口 → 客户端全量 refetch `/api/history`（先连 WS 后拉 history 的 pi §10 时序照抄：重复内容按 uuid 幂等合并）。

### 6.3 TranscriptRecord（wire）

```jsonc
{ "kind": "assistant", "uuid": "...", "ts": "ISO",
  "text": "…", "thinking": "…",
  "toolUses": [{ "id": "toolu_…", "name": "Bash", "input": {/*原样*/} }],
  "toolResults": [{ "id": "toolu_…", "isError": false, "content": "…" }],
  "usage": { "in": 1, "out": 2, "cacheRead": 3, "cacheWrite": 4 }, "model": "…" }
```
user 行 `toolResults` 与 assistant 行 `toolUses` 由前端按 `id` 配对成工具卡（无配对=running，配对+isError=failed，配对成功=success）。

## 7. 前端设计

### 7.1 桌面视图（/terminal）

- 新纯逻辑模块 `frontend/src/components/terminal/webPlaneGate.ts`：`webPlaneCapable(appType)`（`pi`、`claudecode`）、`autoPreferWeb(appType)`（仅 `pi`，D5）、`planeToggleTitle(appType)`；TerminalView 的判定改为 `webPlaneCapable(session.appType)`（TerminalView.vue:342/349-354 的闸门 + 510-512 的 onMounted 探测启动条件同改）；`preferWebPlane`（367-373）加 `autoPreferWeb` 守卫。额度条 `SessionQuotaStrip` 仍仅 pi 渲染（WebPlaneHost 增可选 prop `quotaStrip:boolean`，claude 传 false）。
- `WebPlaneHost.vue` 文案泛化（title「Web 会话平面」、错误提示去 pi 字样），其余零改动。
- stores/session.ts、api/webui.ts **零改动**。
- 新增测试 `frontend/src/__tests__/components/terminal/webPlaneGate.test.ts` + 既有 `quickMenuWebPlane.test.ts` 回归不破。

### 7.2 webui-claude SPA（新工程）

- **双部署形态（v1.2 硬约束，阻断 2-1）**：同一份构建产物必须同时运行于——桌面：`http://127.0.0.1:<port>/`（回环根路径，service.go:458）；远程：`https://<remote-host>/webui/{sid}/`（代理子路径，contract webui.go:73-82/100-108 强制相对路径带尾斜杠、无 query）。约束清单：
  1. `vite.config.ts` 固定 `base: './'`——产物 index.html 全部资源引用相对化（`./assets/...`），两种 base 下浏览器都解析到正确子路径（两种形态 URL 均以 `/` 结尾）；**禁止默认 `base:'/'`**（绝对 `/assets/...` 在远程形态 404 → HTML 200 但白屏，且 iframe load 事件照常触发、宿主与移动端均无从侦测，§1 表 WebPlaneHost 行）。
  2. 运行时 base 推导纯函数 `resolvePlaneBase(loc: {protocol, host, pathname}): {apiBase, wsUrl}`：`dir = pathname 截至最后一个 '/'（含）`；API 请求 = `dir + 'api/...'`（如 `/api/info`、`/webui/{sid}/api/info`）；WS = `(protocol==='https:'?'wss':'ws') + '://' + host + dir + 'ws/events'`；query（`?skin=light`）与 fragment（`#/t=`）不参与。**所有 fetch 与 WS 一律经此推导，禁止根绝对路径 `/api/...` 与硬编码 `127.0.0.1`**（反代场景下 `127.0.0.1` 是移动设备自身——pi 同款教训：基址推导以路径前缀判定而非 hostname）。
  3. SPA 首屏自带页内错误态：token 缺失/非法、`/api/info` 失败时显示页内错误与重试按钮（白屏防线的另一半——宿主 load 事件不可信）。
- 技术栈对齐主前端（Vue3 + TS + Vite），但不引 Wails/wailsjs——纯浏览器态，token 取自 `location.hash` `#/t=`；设计令牌复制主仓 `frontend/src/styles/tokens.css` 为 1:1 值快照（零手写色值）；`?skin=light` query 叠加浅色；**body 背景透明**（内嵌态透出宿主皮肤层，与 pi 页面同约定——WebPlaneHost.vue:194-196 注释链路）。
- 结构：`src/data/source.ts`（Transport 抽象：`fetchJson/connectWs/schedule/now` 可注入）+ `HttpDataSource` / `MockDataSource`（`?mock=1` 同回调面）；`src/state/doc.ts`（记录按 uuid 幂等 upsert + 工具卡配对 + busy）；`src/views/Stream.vue`（消息流/思考折叠/工具卡四态/用量行）、`Composer.vue`（输入/提交/中断 Esc/草稿）、`host-bridge.ts`（`amagi:insert-input` 接收：嵌入态 + token 与自身 fragment 严格相等校验后追加进输入框；`amagi:open-url` 上抛）。
- 重连：退避 1s/2s/5s 封顶；hello.stream 变化或 seq 缺口 → 全量 refetch；ended → 结束横幅 + 「切回终端」由宿主 WebPlaneHost 提供。
- 输入错误分流：403 `control.forbidden`（设备失持控/远程无控制权）行内提示「需先获取控制权」；503 `service.down` 提示桌面控制服务不可用；**草稿保存（PUT /api/draft）失败一律静默容错**（含远程未持控设备经写面门的 403——与 pi 语义一致；本地回显不受影响，恢复持控后下一次变更自动重写）。
- 测试（Vitest，`webui-claude/src/__tests__/`）：doc 配对/幂等、composer 状态机、host-bridge 凭证校验、mock 剧本回放、**resolvePlaneBase 双形态推导**（根路径/子路径/带 query+fragment/WS 协议换算）、**构建产物 base 检查**（构建后断言 dist/index.html 无 `="/`（绝对根引用）——CI 中 build 先于 test，测试可读 dist）。

### 7.3 构建与嵌入（v1.2 重写：可落地的 hook 接法 + CI/release）

- `webui-claude/package.json`（`build` = `vue-tsc --noEmit && vite build`）+ **`package-lock.json` 入库**（构建脚本用 `npm ci`，无 lockfile 直接失败——mobile 模式的前提即 lockfile 入库，§1 表核实）；`vite.config.ts` `build.outDir='../internal/claudewebui/dist'`、`emptyOutDir=true`、**`base:'./'`**（§7.2）。
- **wails preBuildHooks 接法（阻断 2-2a 修复）**：hooks 是平台键→单条命令的 map（`wails.json:10-12` 现为 `"*/*": "npm --prefix ../.. run build:mobile"`；Wails v2.11.0 `pkg/commands/build/build.go:106-111` 三键依次执行、`executeBuildHook` 经 `shlex.Split` + 直接 exec **单条命令不经 shell**——本机模块源码亲自核实）。因此**不在 hook 里并列第二条命令、不用 `&&`**（同键重复 JSON 解码被覆盖、`&&` 成字面参数），改为聚合脚本：
  - 根 `package.json`：新增 `"build:claude-webui": "npm --prefix webui-claude ci && npm --prefix webui-claude run build"` 与 `"build:embedded-web": "npm run build:mobile && npm run build:claude-webui"`（npm run 经 shell 执行，`&&` 合法）。
  - `wails.json`：`"*/*"` 钩子值从 `build:mobile` 改为 `npm --prefix ../.. run build:embedded-web`（单一职责：本地 `wails build`/`wails dev` 一次产出 mobile + claude 两个 dist；hook 相对路径写法保持既有 `../..` 约定）。
  - `build.sh`（30-31 行现按 `grep build:mobile` 触发 `npm run build:mobile`）改为触发 `npm run build:embedded-web`；`build.bat` 的手工 mobile 构建段后补 `call npm --prefix ..\ run build:claude-webui`（或整段改调聚合脚本——以最小 diff 为准）。
- **CI/release 接入（阻断 2-2b，t12）**：`ci.yml` frontend-mobile job 补 webui-claude 的 `npm ci`/`npm run build`/`npm run test` 三步 + `setup-node` 的 `cache-dependency-path` 加 `webui-claude/package-lock.json` + 上传 artifact 路径加 `internal/claudewebui/dist`（go-quality 下载后 vet/test 需要 dist 存在）；`release.yml` 两个 job（行 36-64、145-173 一带的手工 frontend+mobile 构建段）镜像 mobile 步骤补 webui-claude `npm ci`+`npm run build`，cache 路径同步。没有 t12，发布产物 `internal/claudewebui/dist` 只有 `.gitkeep` 占位 → 用户打开平面见降级提示页。
- `.gitignore` 增 `internal/claudewebui/dist/*` + `!internal/claudewebui/dist/.gitkeep`（提交空占位，防 fresh checkout `go vet ./...` 空目录 embed 失败；CI artifact 与本地构建覆盖为真实产物）。

### 7.4 移动端入口（t10，v1.1 补）

移动端探测与入口被 `isTuiCli = (pi‖omp)` 闸死（WorkspacePage.vue:53-56/60/307/318-325；useSessionWebUI.ts:43 enabled=false 不探测），composable 本体 appType 无关。改动：
- WorkspacePage.vue 新增 `webPlaneCapable = isTuiCli || cliType === 'claudecode'` computed：`useSessionWebUI(sessionId, webPlaneCapable)` 的 enable 条件、「切换至 Web 平面视图」菜单项与 Web 平面 badge 的可见性、`?view=webplane` 深链放行均改用该值。
- **缺省视图策略不变**（D5）：claudecode 缺省仍 timeline（`!isTuiCli → timeline` 分支保持）；仅显式菜单/深链进入 webplane；pi/omp 的「available→默认 webplane」策略原样。
- 测试：扩展 `mobile/src/__tests__/views/WorkspacePage.webplane.test.ts`（claudecode 会话：探测 enable、菜单入口 available 后可见、缺省视图仍 timeline）；`WorkspacePage.tui-session.test.ts` 回归（isTuiCli 语义不变）。

## 8. 配置项

- `settings.json` 新键 `claudeWebUIEnabled`（`*bool` 三态，nil=默认 true，照 `AgentCLIEnabled` 模式）：`GetClaudeWebUIEnabled()/SetClaudeWebUIEnabled(bool)`。
- 设置页 `TerminalSettings.vue` 增开关「Claude Code Web 视图」（说明：为内嵌 Claude Code 会话提供网页视图平面；WSL/外部终端会话不适用）。
- 该开关只在启动时读取；运行中翻转不影响已起平面（文档注明）。

## 9. 会话生命周期 × 五 AppType 体系

| 生命周期 | claudecode 行为 | 依据/对齐 |
|---|---|---|
| 启动（桌面 embedded） | 注入 `--session-id`（既有）→ 预分配 port/token → PTY 起后 Start server + Register tracker | pi 时序 app.go:3930-4014 |
| 启动（remote v1 create/restart） | planner 生成 sid 注入 argv + WebUIPlane spec（含 homeDir 提取）→ executor commit 后 Start + RecordClaudeSessionID + Register | §5.4 |
| 启动（WSL / external / 关闭开关） | 不 Start 不 Register，状态 unknown，前端无切换控件 | A-4 同语义（service.go:43） |
| 输入（本地：桌面 iframe 直连） | `/api/input`（无身份头）→ 桌面车道 `DesktopInput/PasteChunk`（take-first，与 xterm 打字同语义；claude TUI 排队/提交语义原样继承） | §5.2/§5.3 |
| 输入（远程：设备经反代） | 反代写面门持控校验 + 透传身份头 → 设备车道 `DevicePlaneInput/PasteChunk`（**无夺权**，连发不吊销自身控制权与 ws/v1 通道） | §5.5/§5.6 |
| 中断 | 同车道规则写 Esc；远程经写面门 + 设备车道 | §5.5/§5.6 |
| 停止（StopSession） | 既有控制门停 PTY → 退出 goroutine Stop server + Invalidate → ended | app.go:3053 起 + §5.3-3 |
| 多会话 | per-session 独立 server/port/token；同 workDir 会话靠锁定 sid 隔离转录 | D2/D3；R9 |
| 会话删除/批量清理 | 三处 commit 点 Stop + RemoveSession | §5.3-4 |
| /resume（TUI 内切换会话） | 锁定 jsonl 停滞；v1 不跟随（tracker.go:58-62 同语义）；显示停留在旧转录 + 提示为后续项 | 非目标 |

## 10. 绑定与 wailsjs 产物

- **Bind 清单不变（21 项）**：`internal/claudewebui.Manager` 与新控制原语（`DevicePlaneInput` 等）都不 Bind；`bind_manifest_test.go` 不需改、必须仍绿。
- **唯一 wailsjs 再生成点**：`settings.Service` 新增 `GetClaudeWebUIEnabled/SetClaudeWebUIEnabled` → 运行 `wails dev` 或 `wails build` 再生成 `frontend/wailsjs/go/settings/Service.js|d.ts` 与 `frontend/wailsjs/go/models.ts`（CLAUDE.md:71 禁手改）；前端 `api/settings.ts` 补两个包装函数。
- 验证：`go vet ./...` + `go test ./... -count=1`（至少 root + internal/claudewebui + internal/appmeta/claude + internal/settings + internal/remote + internal/launchplan）+ `bind_manifest_test` 反射仍 21 项 + `npm --prefix frontend run build` + `npm --prefix webui-claude ci && npm --prefix webui-claude run build` + 移动端 `npm --prefix mobile run test`。

## 11. 测试计划（落位遵守 CLAUDE.md:37-41）

| 层 | 文件 | 覆盖 |
|---|---|---|
| Go·解析器 | `internal/appmeta/claude/transcript_test.go`（fixture：`internal/appmeta/claude/testdata/transcript.sample.jsonl`，本机真实采集脱敏） | **真实转录样本回放**（块级字段以样本为准，禁止手写想象样本）+ string/array content 判型、tool_use/tool_result 抽取、usage 四分项、单行损坏跳过、offset 断点/截断重建、ClaudeConfigRoot 三分支（CLAUDE_CONFIG_DIR 优先/HOME/USERPROFILE/缺省） |
| Go·服务包 | `internal/claudewebui/server_test.go`（真实 listener，照 webui_test.go:31 模式） | /api/info 契约（v/ready/port 等值）、Bearer 401/403、Host 校验、CORS/OPTIONS 204、history limit、错误体含 v:1 且不含 token |
| Go·服务包 | `internal/claudewebui/input_test.go` | **车道分流**：无头 → fake InputPort.WriteInput（含 \r/\x1b/128KiB/分块）；有头 → DeviceWrite；DeviceWrite 返回持控错误 → 403 control.forbidden；InputPort 未就绪 → 503 |
| Go·服务包 | `internal/claudewebui/ws_test.go` | 子协议对校验/拒绝、hello/stream/seq、since 重放、缺口信号 |
| Go·服务包 | `internal/claudewebui/tailer_test.go` | mtime 去重、增量 offset、busy 启发式两分支、文件未出现等待、truncated 重读 |
| Go·控制原语 | `internal/remote/control_device_plane_test.go`（新，照既有 control gate 测试文件模式） | DoDevicePlanePTY：持控（connected/grace）放行且**不改变 holder**（写前后 SnapshotForDevice 不变）、非持控/他设备/desktop 持有 → DenyNotController、吊销设备/不接受 → 拒绝、DesktopInput take-first 对照断言（写后 holder=desktop）——阻断项 1 的回归锚 |
| Go·App | `app_claudewebui_test.go`（root） | LaunchSession embedded 后 Start+Register 参数（sid/workDir/homeDir/pid/port/token）、WSL/关开关不注册、退出路径 Stop+Invalidate、removeWebUITracker 三 commit 点（扩展 app_webui_remove_test.go 模式）、appInputPort 双车道转发（fake control runtime） |
| Go·remote 链 | `launch_webui_plane_test.go` 扩展 | claude 正例（argv 含 --session-id、无 AMAGI_WEBUI_* env、Start cfg 含 homeDir、commit 后注册 + RecordClaudeSessionID）、Abort 无残留、codex/omp 负例保留 |
| Go·remote 反代 | `internal/remote/webui_proxy_test.go` 扩展 | `/api/interrupt` 入写面集合（无控制 403/持控制 200）；**写面转发透传身份头**（plane token 设备 → 头= deviceID 且客户端自带值被剥离；raw token 只读通道 → 头被剥离不注入） |
| Go·settings | `internal/settings/claude_webui_settings_test.go` | *bool 三态默认、Set 持久化回读 |
| 前端 | `frontend/src/__tests__/components/terminal/webPlaneGate.test.ts` | 集合判定、autoPrefer 仅 pi、title |
| 前端 | 既有 `quickMenuWebPlane.test.ts` 等回归 | 闸门改动不破 pi 行为 |
| 移动端 | `mobile/src/__tests__/views/WorkspacePage.webplane.test.ts` 扩展 | claudecode：探测 enable、菜单入口 available 后可见、缺省视图仍 timeline；tui-session 回归 |
| 前端·SPA | `webui-claude/src/__tests__/*.test.ts` | doc 幂等/配对、composer（含 403/503 错误分流与 draft 静默）、host-bridge 凭证、mock 回放、**resolvePlaneBase 双形态推导**（根/子路径/query+fragment 免疫/WS 协议换算）、**dist/index.html 无根绝对引用断言**（build 先于 test） |
| CI 集成 | `.github/workflows/ci.yml`、`.github/workflows/release.yml`（t12） | frontend-mobile job 新增 webui-claude 三步与 artifact/cache 路径；release 两 job 新增构建步骤——CI 上 `embedded-web-assets` artifact 含 `internal/claudewebui/dist` 且非空（.gitkeep 之外有 index.html） |
| E2E（可选） | 根 `e2e/` 冒烟 | 启动 claude 会话→切网页平面→发送一轮（真机验收步骤，非 CI 阻塞） |

## 12. 任务分解

（与交付的 tasks JSON 一致；文件集尽量不相交，相交处用 dependsOn 串行。）

| id | 任务 | 主要文件 | 依赖 |
|---|---|---|---|
| t1 | 转录增量解析器 + 转录根助手 | `internal/appmeta/claude/transcript.go`、`internal/appmeta/claude/transcript_test.go`、`internal/appmeta/claude/testdata/transcript.sample.jsonl`（真实采集 fixture） | — |
| t2 | claudewebui 服务包（双车道输入端口 + 身份头常量） | `internal/claudewebui/{server,auth,ws,tailer,input,assets}.go`（Manager 落 server.go）、`internal/claudewebui/dist/.gitkeep`、包内 4 个测试 `server_test.go/input_test.go/ws_test.go/tailer_test.go`、`internal/remote/contract/webui.go`（addive：`WebUIPlaneActorDeviceHeader` 常量）、`.gitignore` | t1 |
| t3 | App 接线 + settings 键（Go 侧，含双车道适配器） | `app.go`、`app_claudewebui.go`、`app_claudewebui_test.go`、`internal/settings/service.go`、`internal/settings/claude_webui_settings_test.go`、`app_webui_remove_test.go`（扩展） | t2、t6 |
| t4 | 桌面前端闸门 | `frontend/src/components/terminal/TerminalView.vue`、`WebPlaneHost.vue`、`webPlaneGate.ts`、`frontend/src/__tests__/components/terminal/webPlaneGate.test.ts` | —（与 t3 无文件交集，可并行；联调需 t3） |
| t5 | 配置项前端 + wailsjs 再生成 | `frontend/src/api/settings.ts`、`frontend/src/views/settings/TerminalSettings.vue`、`frontend/wailsjs/go/settings/Service.js|d.ts`、`frontend/wailsjs/go/models.ts`（均再生成）、`frontend/src/__tests__/api/settings.claudeWebUI.test.ts` | t3 |
| t6 | 设备平面车道原语 + 反代写面透传 | `internal/remote/control_gate.go`、`internal/remote/control_runtime.go`、`internal/remote/control_device_plane_test.go`（新）、`internal/remote/webui_proxy.go`、`internal/remote/webui_proxy_test.go`（扩展） | —（独立于 t1–t5；t3 的设备车道适配器需它先行） |
| t7 | webui-claude SPA 骨架与消息流（双 base） | `webui-claude/`（package.json、**package-lock.json**、vite.config.ts（**base:'./'**）、index.html、tsconfig、src/{main,App,tokens.css,data/source.ts,data/httpSource.ts,data/mockSource.ts,data/basePath.ts,state/doc.ts,views/Stream.vue,components/*}、`src/__tests__/`）、根 `package.json`（build:claude-webui 脚本） | t2 |
| t8 | SPA 输入面与宿主桥 | `webui-claude/src/views/Composer.vue`、`src/host-bridge.ts`、`src/api/draft.ts`、对应 `src/__tests__/` 扩展 | t7 |
| t9 | 文档与变更记录 | `docs/developer/claude-webui.md`、`CHANGELOG.md`、本设计文档验证记录补记 | t1–t8、t10–t12 |
| t10 | 移动端 Web 平面入口 | `mobile/src/views/WorkspacePage.vue`、`mobile/src/__tests__/views/WorkspacePage.webplane.test.ts`（扩展）、`mobile/src/__tests__/views/WorkspacePage.tui-session.test.ts`（回归） | t3、t6 |
| t11 | remote/restart 计划链接入（planner/executor） | `internal/launchplan/types.go`、`launch_planner.go`、`launch_executor.go`、`launch_webui_plane_test.go`（扩展）、`app.go`（executor deps 接线） | t3、t6 |
| t12 | wails hook 聚合 + CI/release 接入 | `wails.json`（`*/*` 改指聚合脚本）、根 `package.json`（build:embedded-web）、`build.sh`、`build.bat`、`.github/workflows/ci.yml`、`.github/workflows/release.yml` | t7 |

## 13. 风险清单

- **R1 转录 schema 漂移**：`~/.claude/projects` jsonl 非官方冻结契约，且块级字段名属外部知识（§1 表）。缓解：行级容错跳过 + **t1 以真实样本回放为单测依据 + t9 把实测字段清单落契约文档**；影响面仅 Web 平面（TUI 主平面不受累）。
- **R2 TUI 状态盲写**：claude 处于权限对话框/菜单态时 WebUI 输入会触达选项（与共用键盘同构）。缓解：D6 文档化；后续可用 PTY 输出解析 composer 状态（agent-orchestrator 先例）。
- **R3 流式粒度粗**：转录按 API message 完成落盘，长生成期间 Web 平面静默数秒至数十秒。缓解：busy 态动效 + 「生成中」提示；TUI 平面始终实时；不做虚假增量（诚实边界）。
- **R4 busy 启发式误判**：纯文件侧推导可能短暂错误（如排队消息场景）。影响仅 UI 显隐，不做写入门控，错误不放大。
- **R5 写面遗漏**：`/api/interrupt` 忘加入 `webuiBackendWriteFaces` → 远程设备免控制中断。t6 显式断言（无控制 403）。
- **R6 embed 空目录破坏 `go vet`**：`internal/claudewebui/dist` 若无任何提交文件，fresh checkout `go vet ./...` 直接失败。缓解：提交 `.gitkeep` + gitignore 负例外（t2）。
- **R7 双车道语义混用（v1.1 升级自原「写面遗漏」风险）**：设备输入误走桌面车道 → 首条输入夺权、第二条 403（评审阻断 1 的复发形态）。缓解：车道分流在 server input.go 单点实现 + `control_device_plane_test.go` 的 take-first 对照断言 + 反代头注入测试（透传/剥离两分支）；新控制原语的 grace 宽松必须与反代写面门同步（口径分裂 = 代理放行后端拒绝）。
- **R8 同 workDir 多会话串扰**：desktop 已由锁定 sid 消除；remote 链在 t11 落地前不得注册平面（executor 门控要求 `WebUIPlane != nil`，由 t11 引入，天然满足）。
- **R9 listener/goroutine 泄漏**：三处 commit 点遗漏 Stop（restart 路径尤其）。t3 用 fake Manager 断言全部路径；Manager.Start 幂等替换旧平面。
- **R10 token/端口泄漏面**：日志与错误体红线；设备身份头只是车道信号非鉴权（§5.2），反代仍剥离客户端自带值；t2/t6 测试断言错误体无 token、头透传仅在写面。
- **R11 构建顺序与 lockfile**：wails build 依赖聚合 hook（build:embedded-web）产出 mobile + claude 两个 dist；`npm ci` 要求 `webui-claude/package-lock.json` 入库（t7 验收项），否则 fresh checkout 构建即断；本地裸跑 `go build` 前需先 `npm run build:claude-webui`（否则 embed 占位页降级，可观测非失败）。
- **R12 转录根解析口径（v1.2 澄清）**：HOME 重定向的实际来源是用户自定义 env（managedHomeEnvKeys 只是检测键表，§1 表已更正表述）；提取规则 `ClaudeConfigRoot`（CLAUDE_CONFIG_DIR 优先 → HOME/USERPROFILE → os.UserHomeDir）覆盖用户自定义 HOME 与 CLAUDE_CONFIG_DIR 两类；**提取规则未覆盖的其余自定义形态**（未来新增重定向键）表现为空转录 + busy=false（非错误、不误显内容），契约文档记录已知边界。
- **R14 SPA 双 base 回归（v1.2 新增，阻断 2-1）**：任何根绝对引用（`base:'/'` 产物、`fetch('/api/...')`、硬编码 127.0.0.1/WS 绝对地址）都只在桌面形态通过联调、移动端首开白屏且宿主无从侦测（iframe load 对子资源 404 照常触发）。缓解：vite base 固化 + resolvePlaneBase 单点推导 + 双形态单测 + dist/index.html 无根绝对引用断言 + §14-6 远程形态真机验证。
- **R15 hook/CI 集成破坏面（v1.2 新增，阻断 2-2）**：wails.json 同键重复会把 build:mobile 顶掉（JSON 解码覆盖）——本地 mobile dist 停更；hook 内 `&&` 是字面参数（shlex + 直接 exec，已核实）——命令静默失效；CI/release 漏接则发布产物 dist 只有占位 → 平面恒降级提示页。缓解：t12 聚合脚本方案（单一 `*/*` 钩子）+ CI artifact 非空断言 + §14-7 产物验证。
- **R13 移动端缺省视图回归**：闸门泛化误伤 pi/omp 的「available→默认 webplane」或缺省 timeline 策略。t10 用既有 `WorkspacePage.webplane.test.ts` / `tui-session.test.ts` 双向回归锚死。

## 14. 实施后验证清单

1. `go vet ./...`（win+mac 口径）与 `go test -mod=vendor ./... -count=1`（至少 root、internal/claudewebui、internal/appmeta/claude、internal/settings、internal/launchplan、internal/remote）。
2. `bind_manifest_test` 反射仍 21 项（无新绑定）。
3. `wails build`（或 dev）后 `git diff frontend/wailsjs` 仅 settings Service 两方法新增。
4. `npm --prefix frontend run build`（vue-tsc 门禁）、`npm --prefix webui-claude ci && npm --prefix webui-claude run build && npm --prefix webui-claude run test`、`npm --prefix mobile run test`。
5. 真机桌面：embedded claude 会话 → 工具栏出现「终端/网页」切换 → 网页平面显示转录 → 输入一轮（TUI 同步可见）→ Esc 中断 → 停止会话（ended 横幅）→ 删除会话（`netstat` 无残留 listener）。WSL 会话确认无切换控件。**观测项**：预分配端口到 bind 之间存在抢占窗口（port.go:19-23 注释自认，与 pi 同口径），偶发无平面（无切换控件、状态 unknown）属已接受降级，重开会话即恢复——验证时如遇，记录频次而非视为缺陷。
6. 真机远程（移动端）：经 `/webui/{sid}` 打开 claude 平面——**首先确认非白屏**（页面渲染出消息流或页内错误态；网络面板中 assets 与 /api/* 均落在 `/webui/{sid}/` 前缀下 200——R14 双 base 回归）；设备**先取控制权**后**连发 ≥3 条输入**（v1.1 阻断回归：不得第 2 条起 403）、期间桌面 `/terminal` 输入一次后移动端需重取控制（对称夺权语义）；无控制权设备发输入/中断 → 403、草稿保存静默；桌面不被远程平面输入静默夺权（远程输入期间桌面 holder 不变）。
7. 移动端入口：claudecode 会话详情出现「切换至 Web 平面视图」（available 后）；缺省视图仍为 timeline；pi/omp 行为不回归。
8. 发布产物：CI `embedded-web-assets` artifact 含非空 `internal/claudewebui/dist`（index.html + assets，非仅 .gitkeep）；本地 `wails build` 日志显示聚合 hook 一次执行 `build:embedded-web`（mobile 与 claude 两个 dist 均更新）；release 产物二进制中打开 claude 平面为真实界面而非占位提示页。

---

## 15. 实施验证记录（2026-11-14，t9 补记）

本节如实记录本特性（t1–t12）实际执行过的验证与结果；**未执行的项目明确声明未执行**。除「t9 收尾自跑」三行外，门禁结果由编排脚本统一执行并向 t9 提供（本节按提供值记录，t9 未逐条复跑）。

### 15.1 编排门禁结果（全部 exit 0）

| 验证 | 命令/形态 | 结果 |
|---|---|---|
| webui-claude 依赖安装 | `npm --prefix webui-claude ci`（lockfile 入库） | exit 0 |
| webui-claude 构建 | `npm --prefix webui-claude run build`（vue-tsc --noEmit + vite build，含 `base:'./'` 产物） | exit 0 |
| webui-claude 测试 | `npm --prefix webui-claude run test`（vitest run：basePath 双形态推导 / doc 幂等配对 / composer / hostBridge / dist-base 无根绝对引用断言） | exit 0 |
| Go 静态检查 | `go vet ./...` | exit 0 |
| Go 定向包测试 | `go test`（定向）：`internal/appmeta/claude`（真实 fixture 回放/ClaudeConfigRoot 三分支）+ `internal/claudewebui`（info 契约/Bearer/Host/CORS/history/双车道分流/WS 子协议/hello/since 重放/缺口/tailer）+ `internal/settings`（*bool 三态默认与持久化回读）+ `internal/launchplan` | exit 0 |
| root 分支测试 | `go test`（root 包定向）：平面生命周期（启动注册/退出 Stop+Invalidate/三 commit 点清理）、App 接线（WSL/关开关不注册、双车道 InputPort 适配）、Bind 清单（`bind_manifest_test` 反射仍 21 项）、双车道输入端口 | exit 0 |
| remote 分支测试 | `go test`（`internal/remote` 定向）：设备平面车道（`control_device_plane_test.go`：connected/grace 持控放行且不改变 holder、非持控/desktop 持有/吊销拒绝、take-first 对照断言、PasteChunk、fail-closed）+ 反代写面与身份透传（`webui_proxy_test.go` 扩展：`/api/interrupt` 写面门、plane token 设备 → `X-Amagi-Plane-Actor-Device` 注入且客户端自带值被剥离、raw token 只读通道头被剥离不注入） | exit 0 |
| 前端构建 | `npm --prefix frontend run build`（vue-tsc --noEmit 门禁 + vite build） | exit 0 |
| 前端测试 | `npm --prefix frontend run test`（vitest run：`webPlaneGate.test.ts` 新增 + 既有 webui/quickMenu 回归） | exit 0 |
| 移动端测试 | `npm --prefix mobile run test`（vitest run：`WorkspacePage.webplane.test.ts` 扩展 claudecode 探测 enable/菜单入口/缺省仍 timeline + `tui-session` 回归） | exit 0 |
| 全仓编译检查 | `go test ./... -run '^$'`（Windows CI 同款编译检查口径） | exit 0 |

### 15.2 wailsjs 再生成 diff 范围（t9 于本会话用 `git diff` 核实）

- `frontend/wailsjs/go/settings/Service.d.ts`（+4 行）与 `Service.js`（+8 行）：**仅** `GetClaudeWebUIEnabled` / `SetClaudeWebUIEnabled` 两方法——本特性唯一预期再生点，与 §10 预期一致。
- 同次再生带出的 `main/App.d.ts`、`App.js`、`models.ts` 变化**非本特性新增绑定**：内容为此前版本累积、待下次 `wails build` 重生的绑定面（移除 1.3.127 已退役的 WSL Pi Config Supply 相关 App 绑定、并入 1.3.131 分流连接日志的 `GetAIProxyTraffic` 等及模型排序变化）。Bind 清单冻结 21 项不变（`bind_manifest_test` 在 root 分支测试中通过）。

### 15.3 t9 收尾自跑验证（本会话实际执行）

| 命令 | 结果 |
|---|---|
| `go vet ./...` | exit 0（无输出） |
| `npm --prefix frontend run build` | exit 0（vue-tsc 通过，vite `✓ built in 809ms`） |
| `npm --prefix webui-claude run build` | exit 0（vue-tsc 通过，产物落 `../internal/claudewebui/dist`：index.html 0.46 kB + assets（css 6.60 kB / js 78.48 kB），`✓ built in 145ms`） |

### 15.4 未执行的验证（如实声明）

- **§14-5 桌面真机冒烟**：未执行（需 `wails dev`/`wails build` 起真实应用 + 真实 Claude Code 会话交互：切换控件出现、转录显示、输入 TUI 同步、Esc 中断、ended 横幅、删除会话后 `netstat` 无残留 listener、WSL 会话无切换控件）。
- **§14-6 远程真机验证**：未执行（需移动端真机 + 远程服务：远程形态**非白屏**确认、设备取控后连发 ≥3 条输入不 403、对称夺权、无控制权设备 403、草稿静默）。双 base 正确性目前由 `resolvePlaneBase` 双形态单测 + dist 无根绝对引用断言 + 契约文档 §6 防线背书，真机确认留待发布前。
- **§14-7 移动端入口真机确认**：未执行（仅 vitest 覆盖：claudecode 探测 enable、菜单入口 available 后可见、缺省视图仍 timeline；pi/omp 回归）。桌面 `wails build`/`wails dev` 本任务未运行（编排统一门禁不含 wails 构建）。
- **§14-8 发布产物验证**：未执行——CI `embedded-web-assets` artifact 含非空 dist 需推送后在 GitHub Actions 运行确认；本地 `wails build` 聚合 hook 日志与 release 二进制平面抽验同未执行。已落地的替代保障：ci.yml artifact 路径含 `internal/claudewebui/dist` 且 webui-claude build 先于 test（dist-base 断言在 CI 内生效）、release.yml 两 job 镜像构建步骤已接入（t12，本会话核实文件内容）。
- **macOS 侧全量 `go test ./...`**：未执行（CI macos-latest 职责；本机为 Windows）。

### 15.5 编排者收尾补记（同日，工作流结束后）

编排者对实施者自报的两处跨任务接缝逐一核对并收口（对照核对员按文件覆盖核对，未覆盖到这两处功能级接缝）：

- **Composer 接入 App.vue**：t8 按并行纪律未改 t7 文件集的 App.vue，SPA 此前只有读路径。已接入（import + `planeToken` ref + 非错误相位渲染 `<Composer>`）；宿主桥随 Composer 挂载（`installHostBridge` 在其 setup 内）；mock 模式以占位 token 运行（提交仅命中 401 行内提示，预期开发态表现）。
- **身份头常量合流**：`webui_proxy.go` 包私有常量改为引用 `contract.WebUIPlaneActorDeviceHeader`（t6 注释预告的两侧合流，wire 值不变，单一事实源）。
- **`.gitkeep` 持久恢复**：对照核对的修复只恢复了一次文件，根因是 vite `emptyOutDir` 每次构建删除 `.gitkeep`（R6 缓解会随任何一次本地构建失效）。已加 `webui-claude` 的 `postbuild` 脚本在每次构建后重建占位文件。

收尾复验（同机实跑，均 exit 0）：`npm --prefix webui-claude run build`（vue-tsc 门禁 + vite + postbuild，`.gitkeep` 存活）；`npm --prefix webui-claude run test`（5 文件 / 68 测试全过）；`go vet ./internal/remote`；`go test -count=1 -run "DevicePlane|DeviceIdentityHeader|InterruptWriteFace|WriteFaces" ./internal/remote`（ok）。
