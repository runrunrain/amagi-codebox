# 调研报告：Web/移动"会话大厅"看不到桌面会话 + Web 启动非真实会话

## 探索目标
追踪 mobile 前端 → internal/remote HTTP/WS → app/session manager 完整调用链，
定位"桌面 3 个活动会话在 Web 显示无会话"与"Web 启动得到假会话"的根因。

## 资料清单（核心发现，按重要性排序）

### 根因 A：两套会话追踪系统完全断开——桌面会话永不进 remote SessionCatalog

桌面侧栏读取的是 `session.Manager`（`app.Sessions`）；Web 大厅读取的是
`SessionCatalog`（进程内内存索引）。二者无任何桥接：

- **桌面启动路径**全部注册进 `session.Manager`：
  - `app.go:1793` `LaunchSession` → `a.Sessions.Create(session.AppTypeClaudeCode, ...)`
  - `app.go:2405` `LaunchCodexSession` → `a.Sessions.Create(session.AppTypeCodex, ...)`
  - `app.go:2716` `LaunchPiSession` → `a.Sessions.Create(session.AppTypePi, ...)`
  - `app.go:2912` `LaunchOmpSession` → `a.Sessions.Create(session.AppTypeOhMyPi, ...)`
  - `app.go:4082` `LaunchOpenCode` → `a.Sessions.Create(session.AppTypeOpenCode, ...)`
  - **均无** `catalog.Activate` 调用——`grep` 全仓确认生产代码中
    `catalog.Activate` 唯一调用点是 `remote_session_adapter.go:293`（远程启动路径）。

- **Web 列表路径**只读 `SessionCatalog`：
  - `mobile/src/stores/lobby.ts` `load()` → `listSessions()`
  - `mobile/src/lib/api.ts:204` `listSessions()` → `GET /api/remote/v1/sessions`
    （`api.ts:109` fetch 基址 `REST_BASE_PATH` = `/api/remote/v1`）
  - `session_routes_v1.go:34` `handleV1SessionsList` → `adapter.ListSessions`
  - `remote_session_adapter.go:153` `ListSessions` → `a.catalog.ListEntries()`
  - `session_catalog.go:180` `ListEntries` 只返回 `removed=false` 的条目

- **结论**：桌面启动的 3 个会话存在于 `session.Manager`（桌面侧栏可见），
  但从未进入 `SessionCatalog`，因此 `GET /sessions` 返回 `[]`，Web 显示"无会话"。
  置信度：高（代码路径唯一，无旁路）。

- 旁证：存在 legacy 路由 `handlers.go:101` `handleGetSessions` →
  `s.app.GetSessions()`（读 `session.Manager`），注册于 `/api/sessions`（非 v1），
  但 mobile 前端只用 v1 端点（`api.ts` 全部走 `REST_BASE_PATH`），不走 legacy 路由。

### 根因 B：远程启动不走 session.Manager，且无 provider/凭据注入——是"裸 CLI"假会话

Web 点击启动 → `POST /api/remote/v1/sessions` → `adapter.CreateSession`
（`remote_session_adapter.go:258`）的执行链：

1. **解析**：`resolver.ResolveCreate`（`remote_launch_resolver_prod.go:86`）
   - app.go 装配时传 `nil` 作为 defaults reader：
     `app.go:418` `m2aResolver := remote.NewProductionRemoteLaunchResolver(
       app.CLIResolver, homeDir, os.Environ(), nil)`
   - 第 4 参数 `nil`（`remoteLaunchDefaultsReader`）→ recipe 的
     ProviderRef/PresetRef/ModelRef 全部为空（`remote_launch_resolver_prod.go:109-120`
     注释："No host default: recipe refs stay empty"）。
   - 解析器只找到 CLI 二进制路径 + 校验 workdir，不注入任何 provider/model。

2. **启动效果**：`gate.DoLaunchEffect` → `appLaunchRaw.StartProcess`
   （`control_wiring.go:201`）
   - 仅调用 `a.pty.StartResolvedWithRun(string(sessionID), resolved, obsPermit)`
     （`control_wiring.go:210`），即只在 PTY 服务内部 map 注册。
   - **不调用** `a.Sessions.Create(...)`——无 `session.Manager` 引用
     （`appLaunchRaw` 结构体 `control_wiring.go:188` 只有 `pty ptyStarter` 字段）。
   - **不写** provider 配置（不像 `LaunchPiSession` 写 `~/.pi/agent/models.json`，
     `LaunchOmpSession` 写 `~/.omp/agent`，`LaunchSession` 设 `ANTHROPIC_BASE_URL`）。
   - **不注入** API key / `--provider` / `--model` 参数。

3. **对比桌面 Pi 启动**（`app.go:2716-2746`）：
   - `a.Sessions.Create(...)` 注册会话
   - `launcher.BuildPiModelsConfig` 写 provider 配置到 `~/.pi/agent/models.json`
   - `getProviderAPIKey` 注入 API key
   - `--provider`/`--model`/`--thinking` 参数注入 PTY 启动命令

- **结论**：Web 启动创建的是一个**真实 PTY 进程**（有 PID），但它是**裸 CLI**
  ——无 provider 配置、无 API key、无 session.Manager 记录。桌面侧栏看不到它，
  它也不携带用户配置的 provider/凭据，因此表现为"不对应宿主 PTY 的假会话"。
  置信度：高（代码路径与字段定义可验证）。

- 旁证：`control_wiring.go:184-186` 注释自述为"Residual (disclosed)"——
  "obs permit 未贯穿 LaunchRawPort 签名，远程启动的 PTY 输出尚未走 run-scoped
  projector"，说明远程启动链路是已知未完成接线。

## 相关文件

| 文件 | 作用 | 重要性 |
|------|------|--------|
| `app.go:415-426` | M2-A adapter 装配，catalog/stream/resolver/raw 注入 | 核心 |
| `app.go:1793,2405,2716,2912,4082` | 5 个桌面启动入口，均只调 Sessions.Create | 核心 |
| `control_wiring.go:188-212` | appLaunchRaw——远程启动 raw port，只调 pty.StartResolved | 核心 |
| `control_wiring.go:216-252` | appSessionRaw——远程 stop/remove，有 sessionLifecycle | 相关 |
| `remote_session_adapter.go:153-178` | ListSessions——只读 catalog | 核心 |
| `remote_session_adapter.go:258-305` | CreateSession——远程启动全链路 | 核心 |
| `session_catalog.go:55-208` | SessionCatalog——进程内索引，仅 Activate 入口 | 核心 |
| `session_routes_v1.go:34-52` | GET /sessions handler | 相关 |
| `remote_launch_resolver_prod.go:86-142` | 生产解析器，nil defaults → 空 recipe refs | 核心 |
| `internal/session/manager.go` | session.Manager——桌面会话追踪 | 相关 |
| `mobile/src/stores/lobby.ts` load() | 前端列表加载逻辑 | 相关 |
| `mobile/src/lib/api.ts:204,109` | listSessions → GET /api/remote/v1/sessions | 相关 |

## 依赖关系

```
桌面启动:  LaunchXxxSession → Sessions.Create(session.Manager) → Pty.Start
                                      ↓ 桌面侧栏读取
                                  [可见]
                                      ↓ ❌ 无桥接
                           SessionCatalog(空) ← Web GET /sessions 读这里 → []

Web 启动:  POST /sessions → adapter.CreateSession
             → resolver.ResolveCreate(nil defaults → 空 recipe)
             → appLaunchRaw.StartProcess → pty.StartResolvedWithRun (裸CLI)
             → catalog.Activate
           ❌ 不调 Sessions.Create → 桌面侧栏不可见
           ❌ 不写 provider 配置 / 不注入 API key → 无凭据的假会话
```

## 已覆盖 / 未覆盖范围

**已覆盖**：mobile 前端列表/启动调用链、v1 REST handler、adapter List/Create
全链路、SessionCatalog 入口/出口、appLaunchRaw raw port、resolver 装配与
defaults reader 为 nil、5 个桌面启动入口的 Sessions.Create 调用、legacy
/api/sessions 路由存在但 mobile 不消费。

**未覆盖**【待核验】：
- WebSocket attach 路径（`ws_v1_session.go`）是否能附带远程启动的会话——
  静态读到 PTY 服务 `s.sessions` 与 catalog 均有条目，但 session.Manager 无条目，
  WS 输出流是否完整路由到前端【待核验：需动态验证 output 回传是否到达 mobile】。
- control gate/runtime 对远程启动会话的状态投影是否自洽（`sessionState` 回退
  到 catalog presence 假定 running）【待核验：`remote_session_adapter.go:445-455`
  fallback 逻辑在 session.Manager 无记录时的实际行为】。
- `remoteLaunchDefaultsReader` 接口是否有任何实现类存在但未被装配
  【待核验：全仓未搜到 HostDefaultRefs 的非测试实现，但静态搜索可能遗漏】。

## 测试缺口

1. **无桌面→catalog 桥接测试**：`grep` 全仓无任何测试验证"桌面 LaunchXxx 后
   remote GET /sessions 返回该会话"（搜索 `desktop.*catalog` /
   `LaunchSession.*catalog` / `GetSessions.*catalog` 均无命中）。
2. **无远程启动→session.Manager 注册测试**：搜索 `appLaunchRaw.*Sessions` /
   `StartProcess.*Sessions.Create` 均无命中——没有任何测试验证远程启动的会话
   出现在 `session.Manager` 中。
3. **无远程启动 provider 注入测试**：`m2a_adapter_test.go` 等测试用 noop/fake
   launchRaw，不验证真实 provider 配置写入或 API key 注入。
4. 现有 `m2a_adapter_test.go:128` 测试手动调 `adapter.Catalog().Activate(...)`
   模拟桌面会话进 catalog——这恰好反证生产代码中桌面路径缺少此调用。

## 待核验项

- 【待核验：WS attach 对远程裸 CLI 会话的输出路由是否完整——静态读到 PTY 输出
  经 RunEventProjector → stream pump，但 session.Manager 无记录时 H1 committer
  的 run-scoped 行为需动态验证】
- 【待核验：`remoteLaunchDefaultsReader` 接口（`remote_launch_resolver_prod.go:53`）
  的 `HostDefaultRefs` 方法是否有未被装配的实现——全仓 grep 仅命中接口定义，
  未发现生产实现类，但不排除通过 settings service 间接提供的路径】
- 【待核验：远程启动的裸 CLI 进程退出后，catalog 是否会被正确清理——
  `session_catalog.go` 的 Remove 只由 adapter lifecycle 调用，PTY 进程自然退出
  是否触发 catalog 更新需追踪 waitLoop → projector 链路】
