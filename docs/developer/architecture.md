# 整体架构

> 受众：维护 Amagi CodeBox 后端或前后端桥接的开发者。
> 范围：进程结构、绑定主干、internal 包分组、会话生命周期、远程控制（legacy + v1）与移动端架构。
> 信息来源：`main.go`、`bind_list.go`、`app.go`、`internal/session/types.go`、`internal/remote/`（均以当前仓库实际读取为准，核实日期 2026-08-22，版本 1.3.50）。

## 一句话概览

Amagi CodeBox 是一个基于 Wails v2 的桌面应用：Go 后端与 Vue 3/TypeScript 前端编译为**单一二进制**，通过 Wails 方法绑定实现前后端通信；内嵌一份独立的 Capacitor 移动端构建产物，并内置 HTTP/WebSocket 远程控制服务器（legacy token API + v1 配对契约 API 双栈），供移动端或其他 CodeBox 实例远程控制本机。

## 技术栈

| 层 | 选型 |
|---|---|
| 桌面框架 | Wails v2 |
| 后端语言 | Go（`go.mod` 基线 `go 1.25.0`） |
| 前端 | Vue 3 + TypeScript（Vite 构建），自有 `frontend/src/components/ui/` 组件 kit + `tokens.css` 设计令牌（Element Plus 已移除，勿再引入） |
| 终端渲染 | xterm.js |
| 伪终端 | Windows ConPTY（`github.com/UserExistsError/conpty`）/ macOS `creack/pty` |
| 远程通信 | 标准库 `net/http` + `gorilla/websocket` |
| 用量存储 | SQLite（`usage.db`，`internal/usage/store_sqlite.go`） |
| 移动端 | Capacitor 独立构建（`mobile/`） |

## 单二进制与嵌入资源

`main.go` 使用 `//go:embed` 嵌入两份静态资源：

```go
//go:embed all:frontend/dist
var assets embed.FS

//go:embed all:mobile/dist
var mobileFS embed.FS
```

- `frontend/dist`：桌面前端的 Vite 产物，由 Wails 的 `AssetServer.Assets` 提供；`AssetServer.Handler` 同时挂了 `app.Skins.AssetHandler()`，把 `/skins/<file>` 回落到本地皮肤图片库（`~/.amagi-codebox/skins/`）的只读静态服务。
- `mobile/dist`：移动端 Capacitor 前端的独立构建产物。**它不是桌面前端**，而是经 `NewApp(mobileFS)` 注入到 `remote.Server`，由远程控制 HTTP 服务器在启用时对外暴露。

`build.sh`（macOS/Linux）和 `build.bat`（Windows）一次性串起 `frontend build → mobile build → wails build`；`wails.json` 的 `preBuildHooks` 保证 `wails build` 前先生成 `mobile/dist`（详见 [./build-dev.md](./build-dev.md)）。

## main.go 启动流程

`main()` 串起以下步骤（顺序敏感）：

1. `updater.MaybeRunWindowsUpdateHelper(os.Args)`：Windows 更新助手模式短路（以更新助手参数启动时执行替换逻辑后直接退出，不进入 GUI）。
2. `platform.CurrentCapabilities()`：解析当前平台能力（**仅此一次**，详见 [./platform-build-tags.md](./platform-build-tags.md)）。
3. `platform.EnsureSingleInstance(...)`：调用 OS 单实例机制，重复启动时直接 `os.Exit(0)`。
4. `NewApp(mobileFS)`：构造 `App`，注入所有服务（见下节）。
5. `wails.Run(&options.App{...})`：注册 `OnStartup`、`OnShutdown`、窗口参数、`AssetServer`（含 Skins Handler），`Bind` 字段直接调用 `buildWailsBindList(app)`。

`HideWindowOnClose` 由平台能力运行时决定：

```go
HideWindowOnClose: capabilities.HideOnCloseSupported && capabilities.CloseAction == platform.CloseActionHide,
```

版本信息通过 ldflags 注入到 `main.Version/BuildTime/GitCommit/GoVersion`，默认值为 `dev`/`unknown`；未注入时由 `GetAppInfo` 在运行时回退到 `wails.json` 的 `info.productVersion`。注入链详见 [./build-dev.md](./build-dev.md#版本注入)。

## 绑定主干（21 个绑定）

### Bind 列表的唯一事实源：`bind_list.go`

`main.go` 的 `Bind` 字段不再内联手写切片，而是调用 `buildWailsBindList(app)`（`bind_list.go`）。当前精确清单（**App + 20 个服务 = 21 个绑定**）：

```go
func buildWailsBindList(app *App) []any {
	return []any{
		app,               // *App（枢纽门面）
		app.Config,        // *config.ConfigService
		app.Secrets,       // *secrets.SecretsService
		app.Paths,         // *paths.PathsService
		app.Log,           // *logging.Service
		app.Settings,      // *settings.Service
		app.Updater,       // *updater.Service
		app.Plugins,       // *plugin.Service（Claude Code 插件）
		app.CodexPlugins,  // *codexplugin.Service
		app.OpenCodePlugins,// *opencodeplugin.Service
		app.PiPlugins,     // *piplugin.Service
		app.OmpPlugins,    // *ompplugin.Service
		app.OpenCodeConfig,// *opencodeconfig.Service
		app.PiConfig,      // *piconfig.Service
		app.OmpConfig,     // *ompconfig.Service
		app.AgentProfiles, // *agentprofile.Service
		app.EnvCheck,      // *envcheck.Service
		app.Usage,         // *usage.Service
		app.WebUI,         // *webui.Service
		app.Skins,         // *skins.Service
		app.GitAssist,     // *gitassist.Service
	}
}
```

### 有意排除的原始服务（门控门面，设计 §4.1/§6.3 C-01）

以下两个原始服务对象**不进入 Bind 列表**，全部读写经 `App` 上的门控门面方法：

| 原始服务 | 排除原因 | 前端可达的门面 |
|---|---|---|
| `app.Pty`（`*pty.Service`） | 原始终端写/缩放必须经 ControlGate 仲裁 | `App.PtyWrite` / `App.PtyWriteLarge` / `App.PtyResize` / `App.GetOutputHistorySnapshot` |
| `app.Headroom`（`*headroom.HeadroomService`） | 变更必须经 lease 守卫 | `App.HeadroomStart` / `App.HeadroomStop` / `App.HeadroomGetStatus` 等 |

这一冻结边界由 `bind_manifest_test.go`（T-24）用反射断言守住：Bind 列表中不得出现 `pty.Service` / `headroom.HeadroomService` 类型；`App` 上不得存在 `StopAllSessions`、`RegisterOutputCallback` 等原始旁路方法；导出的会话/PTY 变更方法必须恰好是已登记的门控门面（M-005）。修改绑定表面前先读该测试（详见 [./testing.md](./testing.md)）。

### 不直接绑定的内部组件

以下 `App` 字段不绑定到前端，由 `App` 门面或远程层内部使用：`Launcher`（进程启动）、`CodexHeadroom`（Codex 全局第二 headroom 实例，8788 端口/OpenAI 目标）、`Tray`、`Sessions`（会话管理器）、`Remote`（远程服务器）、`EnvVars`，以及 RemoteClient 域（`rcRegistry`/`rcCreds`/`rcPairing`/`rcConn`/`rcTerminals`，绑定方法集中在 `app_remoteclient.go`）。

### App 枢纽（`app.go`）

`App` 结构体（`app.go:201` 起）持有全部服务指针与跨服务协调状态：

- **绑定服务**：上表 20 个服务指针。
- **控制运行时**（M3-A2）：`control *remote.ControlRuntime` 是所有会话写副作用的仲裁机构；`sessionAdapter`、`sharedCoord`、`processRegistry`、`remotePlanner`、`compensationDebts` 支撑远程 v1 会话适配与启动计划/补偿债务。
- **RemoteClient 域**：`rcRegistry`（宿主登记簿，构造时从 `configDir/remote-hosts.json` 装载）、`rcCreds`（凭据存储，复用 `App.Secrets` 的 DPAPI/Keychain，条目 `codebox-remoteclient/<DeviceID>`）、`rcPairing`、`rcConn`（当前至多一条已连接宿主）、`rcTerminals`（已连接宿主的 `/ws/v1` 终端长连接管理器）。
- **平台能力快照**：`Capabilities platform.PlatformCapabilities`、`CLIResolver`、`FileOpener`，启动时一次性注入，运行期只读。

`App` 同时实现 `remote.AppInterface`（`GetSettingsService`/`GetConfigService`/`GetPathsService` 三个 getter），让 `remote.Server` 反向访问配置层；这三个方法也会被 Wails 生成绑定，前端 `provider.ts` 用 `GetConfigService()` 拿服务句柄缓存复用（详见 [./frontend-backend.md](./frontend-backend.md)）。

## internal/ 包分组（34 个一级包）

`internal/` 现有 **34 个一级包**（`go list ./internal/...` 计 39 个 Go package，含 `appmeta` 的 5 个子包与 `remote/contract`）。按职责分组：

| 分组 | 包 | 职责 |
|---|---|---|
| 配置族 | `config` / `paths` / `settings` / `envvars` / `secrets` | `models.json` 提供商/预设/terminal_presets；路径管理；应用设置与远程安全状态迁移；自定义环境变量；平台保护密钥（`secrets.enc`） |
| 会话族 | `session` / `launcher` / `launchplan` / `pty` / `processcap` | 会话生命周期与 tracker；进程启动 + 各 CLI 配置写入（`pi_config.go`/`omp_config.go`/`opencode_config.go`）+ 提供商同步（`provider_sync.go`）；远程启动计划与补偿债务；伪终端；进程能力登记 |
| 应用配置族 | `piconfig` / `ompconfig` / `opencodeconfig` | Pi / Oh My Pi / OpenCode 各自的 CLI 原生配置文件读写 |
| 插件族 | `plugin` / `codexplugin` / `opencodeplugin` / `piplugin` / `ompplugin` | 五种 CLI 的插件/扩展管理 |
| 远程族 | `remote` / `remoteclient` | 远程控制服务器（legacy + v1 双栈，含 `contract` 子包）；作为 v1 客户端连接其他 CodeBox 实例（配对、登记簿、终端管理） |
| 功能服务 | `agentprofile` / `gitassist` / `headroom` / `usage` / `webui` / `skins` / `wslsetup` / `envcheck` / `updater` | Agent 档案；AI 辅助 git commit/push；上下文压缩代理；用量统计（SQLite）；pi Web UI 壳探测；皮肤图片库；Windows WSL 安装辅助；CLI 环境检测与一键修复；自动更新 |
| 平台与基础设施 | `platform` / `logging` / `tray` / `structured` / `appmeta` | 平台能力/文件打开/单实例/进程策略/系统代理/WSL 查询；日志；系统托盘；终端结构化输出分类；五个 CLI 的元数据（`claude`/`codex`/`omp`/`opencode`/`pi` 子包） |

通用范式：每个包一个 `Service`/`ConfigService` 结构体 + `New...()` 构造函数；导出方法即 Wails 绑定候选；跨平台差异用 `//go:build` + `_<os>.go` 文件分流（见 [./platform-build-tags.md](./platform-build-tags.md)）。

## 生命周期：Startup 与 Shutdown

### Startup（`app.go:1530`）

`Startup(ctx)` 的实际顺序（核实自当前代码）：

1. `a.Skins.SetContext(ctx)`：皮肤服务依赖 Wails ctx 弹原生文件选择对话框。
2. `a.recoverExternalCleanups()`：恢复持久的外部进程清理所有权；失败转 startup warning 并 fail-close Headroom。
3. **控制运行时接线**（M3-A2）：`Pty.SetRunEventSink(a.control.Projector())`（原始 PTY 输出/退出经 RunEventProjector，不再直接 EventsEmit）、`control.SetPTYRawPort/SetPTYLifecycleRawPort`、`control.SetWailsContext(ctx)`、`control.MarkReady()`；再挂 `Remote.SetControlLifecycleHook(...)`（fence-first：Stop/revoke/安全闩锁先冻结远程控制）。
4. `a.Updater.CleanupOldBinary()`。
5. **远程安全迁移 gate**（M1-B3c）：`runRemoteSecurityMigrationGate()` 在 `Settings.Load` 前对 raw bytes 完成 v0→v1 迁移；任何失败/ManualRepair/Future 路径记固定警告且不启动远程。
6. 顺序加载持久化状态（任一失败仅告警，不阻断）：`Settings → Config → Secrets`。Settings 加载后同步远程 host/port、移动端 Web 根目录、GitHub Token 到 `Remote`/`Updater`；Config 加载后自动迁移 `MigrateProviderPresetsToTerminal`（旧 `provider.presets` → `terminal_presets`，幂等）。
7. Secrets 就绪后 `a.initRemoteClientServices()` 补齐 RemoteClient 域（RC1-5）；Config+Secrets 均就绪后 `syncProvidersToHarnesses()` 把提供商配置同步到 OpenCode/Pi/OMP 的原生配置。
8. `Paths.Load` → `EnvVars.Load`，随后置位 `persistentLoadState`（Shutdown 据此判断是否跳过保存以避免覆盖原文件）。
9. **Usage**：`Usage.Load()` 加载 `usage.db`，注入应用级 ctx，异步首次 `SyncAll()` 并启动 5 分钟周期的后台同步。
10. 异步 `EnvCheck.CheckAll()`，失败 issue 转 startup warning；异步 `restoreCodexGlobalHeadroomOnStartup()`（仅当上次退出前开启）。
11. `applyRemoteGateResult(ctx, ...)`：远程 API 仅在用户显式启用且迁移 gate 放行时启动（默认 loopback 不监听）。
12. 条件启动系统托盘：`capabilities.SystemTraySupported && len(trayIcon) > 0`。

`Shutdown(ctx)` 反向释放：保存配置 → 停止托盘 → 停止远程服务器 → 停止 Headroom → `Launcher.StopAll()` → `Pty.CloseAll()` → 关闭日志。

## 会话类型与启动入口

### AppType

`internal/session/types.go` 定义五种应用类型（`amagicode` 已移除，commit `ef1f54e`）：

```go
const (
    AppTypeClaudeCode AppType = "claudecode"
    AppTypeOpenCode   AppType = "opencode"
    AppTypeCodex      AppType = "codex"
    AppTypePi         AppType = "pi"
    AppTypeOhMyPi     AppType = "omp"
)
```

与远程 v1 契约 `KnownCLITypes`（`internal/remote/contract/scalars.go`）一一对应，`HostSummary.cliAvailability` 必须恰好覆盖这五种。

`LaunchMode`：`ModeTerminal = "terminal"`（独立终端窗口）/ `ModeEmbedded = "embedded"`（内嵌 ConPTY/PTY + xterm.js）。

### 五个启动入口（`app.go`）

| 方法 | 位置 | 目标 CLI | 说明 |
|---|---|---|---|
| `LaunchSession(providerName, presetName, mode, workDir, useHeadroom, shellPath)` | `app.go:1891` | Claude Code | 核心入口：解析 provider/preset、可选 Headroom 编排（`CLI → headroom(:8787) → 真实 API`）、注入 `--session-id <uuid>` 锁定 jsonl |
| `LaunchCodexSession(modelName, providerID, mode, workDir, shellPath)` | `app.go:2600` | Codex | 可走 Codex 全局 headroom（8788，OpenAI 目标） |
| `LaunchPiSession(modelName, providerID, mode, workDir, shellPath)` | `app.go:2969` | Pi | 由 launcher 把提供商配置写入 `~/.pi/agent` |
| `LaunchOmpSession(modelName, providerID, mode, workDir, shellPath)` | `app.go:3238` | Oh My Pi | 同上，写入 `~/.omp/agent` |
| `LaunchOpenCode(providerName, presetName, mode, workDir, shellPath)` | `app.go:4456` | OpenCode | 共享 Anthropic/OpenAI 格式预设桶 |

公共机制：embedded 模式下 `Launcher.BuildOverrides(...)` 构造环境变量覆盖，`EnvVars.MergeWithSystem()` 合并自定义环境变量，`pty.StartResolved(sess.ID, spec)` 拉起 PTY 进程并回写 PID。所有会话写副作用（输入、缩放、停止、删除）经 ControlGate 仲裁，远程 v1 与桌面操作共享同一仲裁（M-005）。

### 会话输出回流

`internal/pty.Service` 的输出经 `RunEventProjector`（M3-A2 后不再直接 EventsEmit）分发到两类消费者：

- **桌面 Wails 事件**：`pty:data:<sessionID>`，载荷 `{s: emitSeq, d: base64Data}`。
- **远程 v1 因果流**：`/ws/v1` 是唯一远程输出消费者（`internal/remote/websocket.go` 头注）；legacy `/ws/terminal/{id}` 环回路径只保留输入分发。

`PtySession` 内置 1MB 环形缓冲区（`maxOutputHistorySize`），后加入的客户端可回放历史输出。

### WSL 终端模式注意事项（pi/omp）

Windows 宿主的 embedded 会话在终端 shell 为 WSL 时跑在 distro 内（`internal/platform/resolver.go` 的 WSL 分支：`wsl.exe -d <distro> --cd <win> -- bash -lic '<cli>...'`）。两个文件系统边界决定了以下约定（2026-08 Bug B 修复引入）：

- **配置写入落 WSL 侧**：`LaunchPiSession`/`LaunchOmpSession` 在启动前用 `platform.EmbeddedLaunchTargetsWSL`（resolver 判定的镜像）探测 WSL 模式，命中时经 `launcher.WriteWSLPiAgentConfig`/`WriteWSLOmpAgentConfig` 把 models.json/models.yml 写到 **distro 内** `$HOME/.pi/agent` / `$HOME/.omp/agent`（`platform.WSLUserHome` 解析 + 缓存；UNC `\\wsl.localhost\\<distro>\\...` 直写，旧别名 `\\wsl$` 兜底），写后经 `wsl.exe -- chmod` 补偿 0700/0600（Windows `os.Chmod` 经 9P 不改 POSIX mode 位）。合并语义与 Windows 侧一致（保留已有 providers/顶层字段，amagi-* 当次条目优先）。非 WSL 模式行为不变（Windows/macOS 路径照旧）。
- **环境变量防线**：`PI_CODING_AGENT_DIR` 在启动时被显式删除（既有行为），WSL 模式下另经 `launcher.StripWSLHostPathPIEnv` 剥离**值为 Windows 盘符路径**的 `PI_*` 变量（WSLENV 会转发所有 `PI_` 前缀变量，Windows 路径值在 Linux 侧非法，会让 pi 在 cwd 建垃圾目录）。`PI_OFFLINE=1` 保留转发。
- **fd/ripgrep 检测**：WSL 模式启动时探测一次（`platform.WSLSearchToolStatus`，按 distro 缓存）`fd`/`fdfind`/`rg`；缺失时应用日志打 WARN（含 `sudo apt install fd-find ripgrep` 引导）。`PI_OFFLINE=1` 下 pi 不联网自下载，缺工具即文件搜索能力降级；CodeBox 不在启动路径做 apt 安装（需 sudo）。
- **退化路径**：WSL 写入失败（无 distro / home 不可解析 / UNC 不可达 / chmod 失败）时回退到内置 provider 映射（`piProviderMapping`，经 WSLENV 转发的 `ANTHROPIC_API_KEY` 等环境变量兑底），不阻断启动。

## 远程控制：legacy + v1 双栈

`internal/remote/Server` 在启用时启动 HTTP + WebSocket 服务器（默认端口 8680，Settings 可持久化覆盖）。当前是**双栈**架构（`server.go: buildHandler`）：

### Legacy 栈（token 认证，仅 loopback）

`internal/remote/handlers.go` 注册的 `/api/...` 端点（`GET /api/info`、`GET|POST /api/sessions/...`（含 `launch-codex`/`launch-opencode`/`launch-pi`/`launch-omp`）、`GET|PUT /api/providers[...]`、`GET|PUT /api/settings`、`GET /api/logs|paths|secrets/diagnostics`、`POST /api/bootstrap/consume`、`/ws/terminal/{sessionID}` 等）。legacy 命名空间在 `buildHandler` 派发层面对非 loopback 请求一律 403（认证前，无 oracle），多数写操作 handler 另有 `requireLoopbackPeer` 内层守卫（纵深防御）。Token 重新生成只能走桌面端 `App.RegenerateRemoteToken`。

### v1 栈（设备配对 + Cookie 认证）

`/api/remote/v1` 下的 10 个 REST 端点与 `/ws/v1` WebSocket 在 legacy 认证**之前**分流（`buildHandler`），由独立的 `buildV1Handler` 中央派发器统一执行 Host 校验、Origin 策略、设备 Cookie 认证与控制授权（`internal/remote/routes_v1.go`、`session_routes_v1.go`、`ws_v1_session.go`）。会话路由（端点 2–9）仅在 `sessionAdapter` 接线后激活，否则保持 404（设计 §4A 硬化门）。线协议规范见 [./remote-api-v1-contract.md](./remote-api-v1-contract.md)。

### RemoteClient（桌面端互联）

`internal/remoteclient/` 实现 v1 契约的**客户端**侧：配对（`pairing.go`）、宿主登记簿（`hosts.go`）、会话/控制/回填/出站队列（`sessions.go`/`control.go`/`backfill.go`/`outbox.go`）、`/ws/v1` 客户端（`ws.go`）。前端 `RemoteSessionsView.vue` + `api/remoteClient.ts` + `stores/remoteClient.ts` 消费；绑定方法集中在 `app_remoteclient.go`。当前至多一条已连接宿主。

### 移动端 Web 资源优先级

`buildHandler` 对非 API 路径按优先级服务移动端 Web UI：① Settings 配置的 `MobileWebRoot` 外部目录 → ② 内嵌 `mobile/dist` → ③ 回退 API handler（需认证）。

## 跨平台机制（简述）

平台差异通过 Go `//go:build` 约束在编译期分流，**不在业务路径用 `runtime.GOOS` 分支**。能力集合在启动时由 `platform.CurrentCapabilities()` 一次性解析，运行期只读。详细文件清单见 [./platform-build-tags.md](./platform-build-tags.md)。

## 配置文件

均位于 `~/.amagi-codebox/`：

| 文件 | 用途 | 负责服务 |
|---|---|---|
| `models.json` | 提供商/预设/terminal_presets | `internal/config` |
| `secrets.enc` | 平台保护的 API 密钥 | `internal/secrets` |
| `settings.json` | 应用设置（远程端口、Web 根、GitHub Token 等） | `internal/settings` |
| `paths.json` | 路径管理 | `internal/paths` |
| `envvars.json` | 自定义环境变量 | `internal/envvars` |
| `agent-profiles.json` | Agent 档案 | `internal/agentprofile` |
| `devices.json` | v1 配对设备登记 | `internal/remote` |
| `remote-hosts.json` | RemoteClient 宿主登记簿 | `internal/remoteclient` |
| `usage.db` | 用量统计（SQLite） | `internal/usage` |
| `usage-pricing.json` | 用量计价 | `internal/usage` |
| `workspaces.json` | 工作区 | — |
| `injection-rules.json` | 注入规则 | — |
| `skins/`、`logs/` | 皮肤图片库、日志目录 | `internal/skins` / `internal/logging` |

> 注：旧文档中「`config.json` + `secrets.json`」的说法已过时——当前文件名是 `models.json` 与 `secrets.enc`（核实自 `internal/config/service.go:73`、`internal/secrets/service.go:30`）。

仓库惯例：JSON 局部编辑使用 `tidwall/gjson` + `tidwall/sjson`，避免 unmarshal-mutate-marshal。修改配置一律走服务层 API，不要直接解析文件。

## 相关文档

- [./frontend-backend.md](./frontend-backend.md)：Wails 绑定生成、`frontend/src/api/*` 包装层、Pinia store 与调用链。
- [./platform-build-tags.md](./platform-build-tags.md)：`//go:build` 文件分流约定与各平台实现清单。
- [./api-reference.md](./api-reference.md)：新增绑定方法的开发流程与冻结边界。
- [./remote-api-v1-contract.md](./remote-api-v1-contract.md)：远程 v1 线协议规范。
- [../api.md](../api.md)：绑定方法全量清单。
- [../security.md](../security.md)：密钥加密与传输安全。

## 待核实项

- `workspaces.json` 与 `injection-rules.json` 的负责服务未在本次重写中逐一核实到具体包，按共享上下文登记；需要精确归属时以 `grep -rn "workspaces.json\|injection-rules.json" internal/` 为准。
- `Shutdown` 的逐行顺序未逐行重读（概要来自既有文档与代码抽查）；修改关机路径前请以 `app.go` 当前实现为准。
