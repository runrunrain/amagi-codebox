# 整体架构

> 受众：维护 Amagi CodeBox 后端或前后端桥接的开发者。
> 范围：进程结构、绑定主干、会话生命周期、远程控制与移动端架构。
> 信息来源：`CLAUDE.md`、`main.go`、`app.go`、`internal/session/types.go`、`README.md`（均以当前仓库实际读取为准）。

## 一句话概览

Amagi CodeBox 是一个基于 Wails v2 的桌面应用：Go 后端与 Vue 3/TypeScript 前端编译为**单一二进制**，通过 Wails 的方法绑定实现前后端通信，并额外嵌入一份独立的 Capacitor 移动端构建产物，用于通过 HTTP/WebSocket 远程控制桌面端。

## 技术栈

| 层 | 选型 |
|---|---|
| 桌面框架 | Wails v2.11.0 |
| 后端语言 | Go 1.25.0 |
| 前端 | Vue 3 + TypeScript（Vite 构建） |
| 终端渲染 | xterm.js |
| 伪终端 | Windows ConPTY（`github.com/UserExistsError/conpty`）/ macOS `creack/pty` |
| 远程通信 | `gorilla/websocket` |
| 移动端 | Capacitor 独立构建 |

## 单二进制与嵌入资源

`main.go` 使用 `//go:embed` 嵌入两份静态资源：

```go
//go:embed all:frontend/dist
var assets embed.FS

//go:embed all:mobile/dist
var mobileFS embed.FS
```

- `frontend/dist`：桌面前端的 Vite 产物，由 Wails 的 `AssetServer.Assets` 提供。
- `mobile/dist`：移动端 Capacitor 前端的独立构建产物。**它不是桌面前端**，而是经 `NewApp(mobileFS)` 注入到 `remote.Server`，由远程控制 HTTP 服务器在启用时对外暴露。

`build.sh`（macOS/Linux）和 `build.bat`（Windows）一次性串起 `frontend build → mobile build → wails build`，三者都跑完后才得到完整二进制。

## 模块关系（文字版）

```text
                        +---------------------------+
                        |  main.go (Wails 启动)     |
                        |  - embed frontend/dist    |
                        |  - embed mobile/dist      |
                        |  - CurrentCapabilities()  |
                        |  - EnsureSingleInstance() |
                        +------------+--------------+
                                     |
                                     v
                        +---------------------------+
                        |  app.go: App 枢纽         |
                        |  持有 14 个被绑定服务指针 |
                        |  + 5 个内部服务           |
                        +------------+--------------+
                                     |
          Bind[] 在 Wails 启动时注册 |  实线 = 直接持有/调用
                                     v
+-----------------------+ +--------------------+ +-------------------+
| 桌面前端 Vue 3        | | Wails 绑定桥       | | internal/* 服务包 |
| (frontend/dist)       | | (wailsjs 自动生成) | | config / secrets  |
| Pinia + composables   | |  - App + 14 服务   | | session / pty     |
| Element Plus          | |  方法 → TS 绑定    | | proxy / headroom  |
+-----------+-----------+ +---------+----------+ | plugin / workspace|
            ^                       |            | remote / updater  |
            | EventsEmit("pty:...") | 调用       | envcheck / paths  |
            +-------+---------------+            | settings / log    |
                    |                            | launcher / tray   |
            +-------+--------+                   | opencodeconfig    |
            | internal/pty   |<------ 转发 ------| codexplugin       |
            | (ConPTY/PTY)   |         (远程)    | envvars / appmeta |
            +----------------+                   | platform / proxy  |
                                                 +-------------------+
                                                           ^
                                                           |
                                                  +--------+--------+
                                                  | mobile/dist     |
                                                  | (Capacitor)     |
                                                  | HTTP + WebSocket|
                                                  +-----------------+
```

## main.go 启动流程

`main()` 串起以下步骤（顺序敏感）：

1. `platform.CurrentCapabilities()`：解析当前平台能力（**仅此一次**，详见 [./platform-build-tags.md](./platform-build-tags.md)）。
2. `platform.EnsureSingleInstance(...)`：调用 OS 单实例机制，重复启动时直接 `os.Exit(0)`。
3. `NewApp(mobileFS)`：构造 `App`，注入所有服务（见下节）。
4. `wails.Run(&options.App{...})`：注册 `OnStartup`、`OnShutdown`、`Bind`、窗口参数和 `AssetServer`。

`HideWindowOnClose` 由平台能力运行时决定：

```go
HideWindowOnClose: capabilities.HideOnCloseSupported && capabilities.CloseAction == platform.CloseActionHide,
```

版本信息通过 ldflags 注入到 `main.Version/BuildTime/GitCommit/GoVersion`，默认值为 `dev`/`unknown`，由 `build.sh`/`build.bat` 读取 `git describe --tags` 后覆盖（详见 `CLAUDE.md` "Version injection"）。

## 绑定主干

### Bind 列表

`main.go` 在 `wails.Run` 中将以下结构体暴露给前端（按声明顺序）：

```go
Bind: []any{
    app,               // *App（枢纽）
    app.Config,        // *config.ConfigService
    app.Secrets,       // *secrets.SecretsService
    app.Proxy,         // *proxy.ProxyService
    app.Headroom,      // *headroom.HeadroomService
    app.Paths,         // *paths.PathsService
    app.Log,           // *logging.Service
    app.Pty,           // *pty.Service
    app.Settings,      // *settings.Service
    app.Updater,       // *updater.Service
    app.Plugins,       // *plugin.Service
    app.CodexPlugins,  // *codexplugin.Service
    app.Workspaces,    // *workspace.Service
    app.OpenCodeConfig,// *opencodeconfig.Service
    app.EnvCheck,      // *envcheck.Service
},
```

实际绑定数量为 **1 个 `App` + 14 个服务 struct = 15 个绑定**。

以下 `App` 字段对应的服务**不直接绑定**，仅通过 `App` 上的方法间接暴露给前端：`Launcher`、`Tray`、`Sessions`、`Remote`、`EnvVars`。

### App 枢纽（`app.go`）

`App` 结构体（约第 94 行起）持有跨服务协调所需的所有依赖：

```go
type App struct {
    ctx context.Context

    Config         *config.ConfigService
    Secrets        *secrets.SecretsService
    Launcher       *launcher.LauncherService
    Proxy          *proxy.ProxyService
    Headroom       *headroom.HeadroomService
    Tray           *tray.Service
    Sessions       *session.Manager
    Paths          *paths.PathsService
    Log            *logging.Service
    Pty            *pty.Service
    Settings       *settings.Service
    Remote         *remote.Server
    EnvVars        *envvars.EnvVarsService
    Updater        *updater.Service
    Plugins        *plugin.Service
    CodexPlugins   *codexplugin.Service
    Workspaces     *workspace.Service
    OpenCodeConfig *opencodeconfig.Service
    EnvCheck       *envcheck.Service

    Capabilities platform.PlatformCapabilities
    CLIResolver  platform.CLIResolver
    FileOpener   platform.FileOpener

    startupWarnings   []string
    startupWarningsMu sync.Mutex

    persistenceMu       sync.RWMutex
    persistentLoadState persistentLoadState
}
```

`App` 同时实现 `remote.AppInterface`（见 `GetSettingsService`/`GetPathsService`/`GetConfigService` 三个 getter），让 `remote.Server` 能反向访问配置层。

### 服务包范式（`internal/*`）

`internal/` 下共 22 个服务包（`ls internal/` 核实），下表列出其中一部分：

| 包 | 主结构 | 构造函数 | 备注 |
|---|---|---|---|
| `internal/config` | `ConfigService` | `NewConfigService(configDir)` | 提供商/预设/`terminal_presets` |
| `internal/secrets` | `SecretsService` | `NewSecretsService(configDir)` | 平台相关后端（见下） |
| `internal/session` | `Manager` | `NewManager()` | 会话生命周期 |
| `internal/pty` | `Service` | `NewService(log)` | 平台相关 PTY |
| `internal/proxy` | `ProxyService` | `NewProxyService()` | Prompt 注入代理 |
| `internal/headroom` | `HeadroomService` | `NewHeadroomService(...)` | 上下文压缩代理 |
| `internal/launcher` | `LauncherService` | `NewLauncherService(log, envVarsSvc)` | 进程启动 + env override |
| `internal/plugin` | `Service` | `NewService("", log)` | Claude Code 插件 |
| `internal/codexplugin` | `Service` | `NewService("", log)` | Codex 插件 |
| `internal/workspace` | `Service` | `NewService(configDir, pluginsSvc, log)` | 多工作空间管理 |
| `internal/envcheck` | `Service` | `NewServiceWithRunner(...)` | CLI 工具检测与一键修复 |
| `internal/envvars` | `EnvVarsService` | `NewEnvVarsService(configDir)` | 自定义环境变量 |
| `internal/settings` | `Service` | `NewService(configDir)` | 应用设置 |
| `internal/paths` | `PathsService` | `NewPathsService(configDir)` | 路径管理 |
| `internal/logging` | `Service` | `NewService(configDir)` | 日志 |
| `internal/updater` | `Service` | `NewService(Version, log)` | 自动更新 |
| `internal/remote` | `Server` | `NewServer(8680, app, log, mobileAssets)` | HTTP + WebSocket |
| `internal/tray` | `Service` | `NewService()` | 系统托盘 |
| `internal/opencodeconfig` | `Service` | `NewService()` | OpenCode 全局 config.json |
| `internal/platform` | （多结构） | `CurrentCapabilities()` 等 | 平台抽象层 |

通用范式：

- 一个 `Service` 或 `ConfigService` 结构体持有配置目录、依赖服务等。
- 一个 `New...(...)` 构造函数注入依赖。
- 所有导出方法都是 Wails 绑定候选；前端经 `frontend/wailsjs/go/<pkg>/` 自动生成的 TS 包装调用（详见 [./frontend-backend.md](./frontend-backend.md)）。

## 生命周期：Startup 与 Shutdown

### Startup（`app.go:647`）

`Startup(ctx context.Context)` 是 Wails 启动钩子，按以下顺序：

1. 注入 `ctx`：`a.Pty.SetContext(ctx)`，便于通过 `wailsRuntime.EventsEmit` 推送事件。
2. 清理旧更新二进制：`a.Updater.CleanupOldBinary()`。
3. 按顺序加载持久化状态（任一失败仅告警，不阻断启动）：`Settings → Config → Secrets → Paths → EnvVars → Proxy 规则与 URL 历史 → Workspaces`。每次成功后置位 `persistentLoadState`，关闭时据此判断是否跳过保存以避免覆盖原文件。
4. Config 加载后自动迁移：`MigrateProviderPresetsToTerminal` 将旧 `provider.presets` 迁到 `terminal_presets`（幂等，失败累计为 startup warning）。
5. Settings 加载后同步远程端口、移动端 Web 根目录、GitHub Token 到 `Remote`/`Updater`。
6. 异步触发环境检测（`go func() { a.EnvCheck.CheckAll() }`），不阻塞启动；失败 issue 转为 startup warning。
7. 启动远程 API：`a.Remote.Start(ctx)`，失败不影响主功能。
8. 条件启动系统托盘：`capabilities.SystemTraySupported && len(trayIcon) > 0`。

`Shutdown(ctx)`（`app.go:768`）按相反顺序释放：保存配置 → 停止托盘 → 停止远程服务器 → 停止 Headroom → 停止 Proxy → `Launcher.StopAll()` → `Pty.CloseAll()` → 关闭日志。

## 会话类型与 LaunchSession 生命周期

### AppType

`internal/session/types.go` 定义四种应用类型：

```go
const (
    AppTypeClaudeCode AppType = "claudecode" // Claude Code 应用
    AppTypeOpenCode   AppType = "opencode"   // Open Code 应用
    AppTypeCodex      AppType = "codex"      // Codex CLI 应用
    // AppTypeAmagiCode 已弃用，仅为读取旧会话保留。
    // 新建 AmagiCode 会话与启动 API 已移除。
    AppTypeAmagiCode AppType = "amagicode"
)
```

实际可启动三种（Claude Code、OpenCode、Codex）；`amagicode` 仅保留读取旧会话能力。

`LaunchMode` 同文件定义：

- `ModeTerminal = "terminal"`：独立终端窗口。
- `ModeEmbedded = "embedded"`：内嵌终端（ConPTY + xterm.js）。

### LaunchSession 主入口

`App.LaunchSession(providerName, presetName, mode, workDir, useProxy, useHeadroom, shellPath string) (string, error)`（`app.go:826`）是 Claude Code 会话的核心入口。关键流程：

1. **terminal_presets 桥接**：先以 `presetName` 作为 stable key 查 `Config.ResolveTerminalPreset("claude_code", presetName)`。命中则用其 provider/model 覆盖参数，并桥接回旧 `provider.Presets` 链路。
2. **提供商校验**：`provider.IsAnthropicCompatible()` 必须 true；OAuth 模式走白板启动（无 API key），否则从 Secrets 取 key。
3. **代理/headroom 编排**（四种组合，见 `switch`）：
   - `useHeadroom && useProxy`：串联 `CLI → 注入代理(:5280) → headroom(:8787) → 真实 API`。
   - `useHeadroom && !useProxy`：仅 headroom：`CLI → headroom → 真实 API`。
   - `!useHeadroom && useProxy`：仅注入代理：`CLI → 注入代理(:5280) → 真实 API`。
   - 两者皆关：`CLI → 真实 API`。
4. **会话记录**：`Sessions.Create(...)` 返回 session ID。
5. **embedded 启动**：
   - `Launcher.BuildOverrides(...)` 构造环境变量覆盖（含 `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN`）。
   - `EnvVars.MergeWithSystem()` 合并自定义环境变量。
   - 注入 `--session-id <uuid>`（方案 R），让 Claude Code 按指定 uuid 写 jsonl，tracker 锁定该文件消除同 workDir 串扰。
   - `pty.StartResolved(sess.ID, spec)` 启动 PTY 进程，PID 写回 `Sessions.SetPID`。

### 会话输出回流

`internal/pty.Service` 同时支持两种输出通道：

- **Wails 事件**：`EventsEmit("pty:data:<sessionID>", {s: emitSeq, d: base64Data})`，桌面前端订阅。
- **注册回调**：`RegisterOutputCallback` / `RegisterExitCallback` / `RegisterResizeCallback`，供 `remote.Server` 的 WebSocket 转发到移动端。

`PtySession` 内置 1MB 环形缓冲区（`maxOutputHistorySize`），后加入的 WebSocket 客户端可回放历史输出。前端→后端通过 `PtyWrite(sessionID, base64Data)` 写入。

## 远程控制与移动端

`internal/remote/Server` 在启用时启动 HTTP + WebSocket（默认端口 8680，可由 Settings 持久化覆盖）。核心端点（核实自 `internal/remote/handlers.go`）：

- `GET /api/info` — 服务信息
- `GET /api/sessions`、`POST /api/sessions/launch`（含 `launch-codex`、`launch-opencode`）、`DELETE /api/sessions/{id}` 等 — 会话管理
- `GET|PUT /api/providers`、`GET|PUT /api/providers/{name}`、`GET /api/providers-by-type/{type}` — 提供商读写
- `GET|PUT /api/settings`、`GET /api/logs`、`GET /api/paths`、`GET /api/secrets/diagnostics` — 设置与诊断
- `POST /api/bootstrap/consume` — 移动端引导
- `WebSocket /ws/terminal/{sessionID}` — 终端桥接

所有请求需携带 `Authorization` Token；Token 重新生成只能通过桌面端 `App.RegenerateRemoteToken`（无远程端点）。完整端点表见 [../user/remote-mobile.md](../user/remote-mobile.md)，权威来源为 `internal/remote/handlers.go`。

Server 接收 `mobileAssets embed.FS`，对外提供 `mobile/dist` 作为移动端 Web UI（`MobileWebRoot` 也可由 Settings 配置为外部目录）。`RemoteWebUIStatusResult` 描述当前是否可打开、运行状态、嵌入可用性等。

`App` 通过实现 `remote.AppInterface` 让 Server 反向访问配置层；启动会话等动作由 Server 委托回 `App`（如启动请求最终走到 `LaunchSession` 等方法）。

## 跨平台机制（简述）

平台差异通过 Go `//go:build` 约束在编译期分流，**不使用 `runtime.GOOS` 在业务路径里分支**。能力集合在启动时由 `platform.CurrentCapabilities()` 一次性解析，运行期只读。详细文件清单与各平台实现差异见 [./platform-build-tags.md](./platform-build-tags.md)。

## 配置文件

均位于 `~/.amagi-codebox/`：

| 文件 | 用途 |
|---|---|
| `config.json` | 提供商/预设（含 `terminal_presets`） |
| `secrets.json` | 加密 API 密钥 |
| `settings.json` | 应用设置（远程端口、移动端 Web 根、GitHub Token 等） |
| `envvars.json` | 自定义环境变量 |
| `settings_amagi.json` | Amagi 模型配置 |
| `global-enabled.json` | 全局启用插件 |

仓库惯例：JSON 的局部编辑使用 `tidwall/gjson` + `tidwall/sjson`，避免 unmarshal-mutate-marshal。修改配置时遵循服务层 API，不要直接解析文件。

## 相关文档

- [./frontend-backend.md](./frontend-backend.md)：Wails 自动生成绑定、`frontend/src/api/*` 包装层、Pinia store 与完整调用链。
- [./platform-build-tags.md](./platform-build-tags.md)：`//go:build` 文件分流约定与各平台实现清单。
- [../api.md](../api.md)：后端绑定方法索引。
- [../security.md](../security.md)：密钥加密与传输安全。

## 待核实项

- `internal/` 下服务包总数经核实为 22 个（`ls internal/`）。本表列出其中 20 个；未列入的辅助包为 `internal/appmeta`、`internal/structured`。如需机器可读清单，运行 `go list ./internal/...`。
- `AppTypeAmagiCode` 仍可被哪些旧路径读取：仅注释说明"为旧会话保留"，未在 `app.go` 检索所有读取点。
- `remote.Server` 的端点完整集合与鉴权细节：以 `../api.md` 和 `internal/remote/server.go` 为权威来源，本篇仅摘要。
