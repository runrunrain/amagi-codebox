# 前后端桥接

> 受众：同时改 Go 后端方法签名与 Vue 前端的开发者。
> 范围：Wails 自动生成 TS 绑定的机制、`frontend/src/api/*` 包装层（27 个文件）、Pinia store（10 个）、composables（9 个），以及一条完整的会话启动调用链。
> 信息来源：`bind_list.go`、`frontend/wailsjs/go/`、`frontend/src/api/*.ts`、`frontend/src/stores/*.ts`、`frontend/src/composables/*.ts`（核实日期 2026-08-22，版本 1.3.50）。

## 桥接总览

```text
Vue 组件 / 视图
   │  调用
   v
composable (frontend/src/composables/*.ts)
   │  组合多域逻辑，注入 store
   v
Pinia store (frontend/src/stores/*.ts)        ──┐
   │  调用业务 API                               │
   v                                            │ 也可直接被组件使用
包装层 frontend/src/api/*.ts                    │
   │  类型化、错误处理（callApi）、参数整理       │
   v                                            │
Wails 自动生成绑定 (frontend/wailsjs/go/<pkg>/) │
   │  IPC 调用 Go service 方法                   │
   v                                            │
Go: app.go + internal/* Service 方法  <────────┘
```

反向链路（后端推前端）通过 Wails 事件总线：

```text
Go: RunEventProjector → EventsEmit("pty:data:<id>", {s: seq, d: base64})
   → frontend: EventsOn("pty:data:<id>", cb)  // 来自 wailsjs/runtime/runtime
```

> M3-A2 后原始 PTY 输出不再由 `pty.Service` 直接 `EventsEmit`，而是经 `remote.ControlRuntime` 的 RunEventProjector 统一投影（桌面事件与远程 v1 因果流共享同一生产者）。

## 第一层：Wails 自动生成绑定（禁止手改）

### 绑定来源与生成目录

绑定列表的唯一事实源是 `bind_list.go` 的 `buildWailsBindList`（21 个绑定 = App + 20 个服务，精确清单与 pty/headroom 排除说明见 [./architecture.md](./architecture.md#绑定主干-21-个绑定)）。`wails dev` / `wails build` 扫描该列表，对每个被绑定的 struct 在 `frontend/wailsjs/go/<go-package>/` 下生成一对 `<Struct>.js` / `<Struct>.d.ts`。

当前实际生成目录（`ls frontend/wailsjs/go/`，21 个目录 + `models.ts`）：

| 生成目录 | Go struct |
|---|---|
| `main/` | `*App`（package main） |
| `config/` | `*config.ConfigService` |
| `secrets/` | `*secrets.SecretsService` |
| `paths/` | `*paths.PathsService` |
| `logging/` | `*logging.Service` |
| `settings/` | `*settings.Service` |
| `updater/` | `*updater.Service` |
| `plugin/` | `*plugin.Service` |
| `codexplugin/` | `*codexplugin.Service` |
| `opencodeplugin/` | `*opencodeplugin.Service` |
| `piplugin/` | `*piplugin.Service` |
| `ompplugin/` | `*ompplugin.Service` |
| `opencodeconfig/` | `*opencodeconfig.Service` |
| `piconfig/` | `*piconfig.Service` |
| `ompconfig/` | `*ompconfig.Service` |
| `agentprofile/` | `*agentprofile.Service` |
| `envcheck/` | `*envcheck.Service` |
| `usage/` | `*usage.Service` |
| `webui/` | `*webui.Service` |
| `skins/` | `*skins.Service` |
| `gitassist/` | `*gitassist.Service` |

注意：**没有 `pty/` 和 `headroom/` 目录**——这两个原始服务被有意排除在 Bind 列表外，前端只能经 `main/App.*` 上的门控门面（`PtyWrite`/`PtyResize`/`HeadroomStart` 等）访问（冻结边界 T-24，见 [./api-reference.md](./api-reference.md)）。

`frontend/wailsjs/go/models.ts` 集中放置 Go 端结构体对应的 TS 类（`config.Provider`、`session.SessionInfo` 等），按命名空间组织。

### 关键约束

**禁止手改 `frontend/wailsjs/`**。改后端方法签名后必须 `wails dev` 或 `wails build` 重新生成。

每个绑定 struct 的导出方法直接映射为同名导出函数。返回 `error` 的方法在 TS 端是 `Promise<T>`，错误以 rejection 形式抛出。

### 拿到 service 引用再调用

某些方法在服务 struct 上而非 `App` 上。前端通过 `App.GetConfigService()` 拿到代理句柄，再调用其方法（`frontend/src/api/provider.ts` 的现行范式）：

```ts
// frontend/src/api/provider.ts（节选）
interface ConfigServiceHandle {
  GetProvider(name: string): Promise<Provider>;
  GetPreset(providerName: string, presetName: string): Promise<config.Preset>;
  SavePreset(providerName: string, presetName: string, preset: config.Preset): Promise<void>;
  DeletePreset(providerName: string, presetName: string): Promise<void>;
}

let configService: ConfigServiceHandle | null = null;

async function getService(): Promise<ConfigServiceHandle> {
  if (!configService) {
    // GetConfigService 返回的是 wails 绑定的 ConfigService 代理实例；
    // models.ts 中 config.ConfigService 类不携带方法签名，故按实际调用面显式声明句柄类型
    configService = (await GetConfigService()) as unknown as ConfigServiceHandle;
  }
  return configService;
}
```

## 第二层：`frontend/src/api/` 包装层（27 个文件）

按业务域组织，当前清单（`find frontend/src/api -type f`）：

| 模块 | 覆盖域 |
|---|---|
| `provider.ts` | 提供商/预设/terminal_presets/OpenCode config |
| `session.ts` | 会话/PTY 启停与写终端 |
| `settings.ts` | 应用设置 |
| `plugin.ts` / `codexPlugin.ts` / `opencodePlugin.ts` / `piPlugin.ts` / `ompPlugin.ts` | 五种 CLI 插件 |
| `piConfig.ts` / `ompConfig.ts` | Pi / OMP 原生配置 |
| `agentProfile.ts` | Agent 档案 |
| `envcheck.ts` | 环境检测与一键修复 |
| `envvars.ts` | 自定义环境变量 |
| `headroom.ts` / `headroomGlobal.ts` | 会话级 headroom / Codex 全局 headroom |
| `gitassist.ts` | AI 辅助 git commit/push |
| `usage.ts` | 用量统计 |
| `webui.ts` | pi Web UI 壳 |
| `skins.ts` | 皮肤 |
| `wslsetup.ts` | WSL 安装辅助 |
| `remote.ts` | 本机远程服务器状态/开关 |
| `remoteClient.ts` | 作为客户端连接其他 CodeBox 宿主 |
| `updater.ts` / `paths.ts` / `logs.ts` | 更新 / 路径 / 日志 |
| `index.ts` | 集中再导出，处理命名冲突 |
| `internal/call.ts` | 共享包装器 `callApi`（仅 api 内部使用，勿向 views/stores 暴露） |

### 包装范式与 `callApi`

统一的错误处理语义由 `api/internal/call.ts` 的 `callApi` 提供（"log + rethrow"：失败时打印带 `[api.<module>.<fn>]` 上下文的日志，然后原样 rethrow，不包装不替换错误对象）：

```ts
export async function callApi<T>(context: string, fn: () => Promise<T>): Promise<T> {
  try {
    return await fn();
  } catch (error) {
    console.error(context, error);
    throw error;
  }
}
```

> 例外：`ompPlugin.ts` 的 `callOmp` 是另一种契约（用中文操作上下文包装 `error.message` 供视图直接展示，且不打印日志），属该模块私有实现，有意不合并。

各模块惯例：类型来自 `wailsjs/go/models`（常用 `type Provider = config.Provider` 别名）；不在此层做业务状态判断，仅整理参数与类型。

### `index.ts` 的命名冲突处理

`settings.ts` 与 `remote.ts` 都暴露 `setRemoteHost` / `setRemotePort` 等同名函数，`index.ts` 用命名空间化导出消解。调用方若需要无歧义的版本，建议直接从具体模块（`api/settings`、`api/remote`）按需导入。

## 第三层：Pinia store（10 个）

`frontend/src/stores/` 当前清单：

| Store | 文件 | 主要状态 |
|---|---|---|
| `useSessionStore` | `session.ts` | `sessions`、`activeSessionId`、轮询状态 |
| `useProviderStore` | `provider.ts` | providers map、terminal_presets、选中项 |
| `usePluginStore` | `plugin.ts` | Claude Code 插件 |
| — | `opencodePlugin.ts` / `piPlugin.ts` / `ompPlugin.ts` | OpenCode / Pi / OMP 插件 |
| `useRemoteClientStore` | `remoteClient.ts` | 远程宿主连接状态 |
| `useSkinStore` | `skin.ts` | 皮肤 |
| `useUIStore` | `ui.ts` | 主题、侧栏等 UI 状态 |
| `useUsageStore` | `usage.ts` | 用量统计数据 |

所有 store 采用 setup 风格（`defineStore('<id>', () => { ... })`），用 `ref`/`computed` 暴露 state 与派生数据。store 自身**不直接发起业务调用**，由 composable 或视图调用 `api/*.ts` 后写入 store，数据流向单一：API → store → 组件。

## 第四层：composables（9 个）

`frontend/src/composables/` 当前清单：

| Composable | 职责 |
|---|---|
| `useSessionLaunch` | 多引擎统一启动、shell 路径解析、mode 决定是否跳 `/terminal` |
| `useSessionList` | 轮询 `GetSessions` 写入 store、管理 activeSessionId |
| `useSessionDetailOutput` | 订阅 `pty:data:<id>`，合并历史 snapshot 与实时 chunk |
| `useTerminalEngine` | xterm.js 终端实例与 `pty:data`/`PtyWrite` 桥接 |
| `usePlatformCapabilities` | 单例化的平台能力（镜像 Go `PlatformCapabilities`） |
| `useDashboardState` | 仪表盘跨组件共享状态 |
| `useTheme` / `useToast` | 主题切换 / 全局提示 |
| `remoteTerminalTransport` | 远程宿主终端传输（RemoteClient 域） |

composable 承担流程编排、副作用清理（`onUnmounted` 停轮询/dispose 监听）与派生状态计算。

## 完整调用链示例：启动 Claude Code 会话

### 1. 组件触发 → 2. composable 分流

`useSessionLaunch.launchFromSettings`（`useSessionLaunch.ts`）按 `dashState.engine` 分流到 `sessionApi.launchClaudeSession` / `launchOpenCodeSession` / `launchCodexSession` / `launchPiSession` / `launchOmpSession`。

### 3. 包装层整理参数 → 4. Wails 生成绑定

`api/session.ts` 把命名参数对象展开为位置参数，调用 `wailsjs/go/main/App` 的 `LaunchSession(...)`（IPC，返回 `Promise<string>` = session ID）。

### 5. Go 方法

`App.LaunchSession`（`app.go:1891`）解析 provider/preset、编排 headroom、注入 `--session-id`、经 `pty.StartResolved` 拉起 PTY 进程。详见 [./architecture.md#五个启动入口appgo](./architecture.md#五个启动入口appgo)。

### 6. 输出回流

PTY 输出经 RunEventProjector 投影为桌面事件 `pty:data:<sessionID>`（`{s: seq, d: base64}`），前端 `useSessionDetailOutput.ts` / `useTerminalEngine.ts` 用 `EventsOn` 订阅；移动端/远程客户端经 `/ws/v1` 因果流接收。

### 反向链路：前端 → 后端写入

终端输入经 `api/session.ts` 的 `ptyWrite`/`ptyWriteLarge` 包装 → `PtyWrite(sessionID, base64Data)`（`App` 门控门面，经 ControlGate 仲裁后写入 PTY stdin）。

## 平台能力的前端映射

`usePlatformCapabilities.ts` 持有 `PlatformCapabilities` 单例（镜像 Go `platform.PlatformCapabilities`），UI 按布尔位决策：`embeddedTerminalSupported`、`systemTraySupported`、`secureSecretStoreKind`、`supportedShells`/`defaultShellKey` 等。该快照启动时一次性解析，运行期不变。

## 前端构建

`npm --prefix frontend run build` 执行 `vue-tsc --noEmit && vite build`：`vue-tsc` 先行做类型闸门（任何对 `wailsjs/go/*` 的误用都会在此暴露），通过后才产出 `frontend/dist` 供 `main.go` 嵌入。

流程惯例：改 Go 方法签名 → `wails dev`/`wails build` 重新生成 wailsjs → 改 `api/*.ts` → `npm run build` 类型闸门 → 打包。

## 相关文档

- [./architecture.md](./architecture.md)：绑定主干、`App` 枢纽、internal 包分组。
- [./api-reference.md](./api-reference.md)：新增绑定方法的开发流程。
- [./platform-build-tags.md](./platform-build-tags.md)：平台能力如何在编译期分流。
- [../api.md](../api.md)：绑定方法全量清单。

## 待核实项

- 各 store/composable 的完整 actions 清单未逐一展开，本文只登记文件级职责；需要逐方法清单时以源码为准。
- `remoteTerminalTransport.ts` 的详细传输语义（RC2 `/ws/v1` 终端域）建议与 `internal/remoteclient/ws.go` 对照阅读，本文未展开。
