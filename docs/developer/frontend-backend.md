# 前后端桥接

> 受众：同时改 Go 后端方法签名与 Vue 前端的开发者。
> 范围：Wails 自动生成 TS 绑定的机制、`frontend/src/api/*` 包装层、Pinia store、composables，以及一条完整的会话启动调用链。
> 信息来源：`CLAUDE.md`、`main.go` 的 `Bind` 列表、`frontend/wailsjs/go/`、`frontend/src/api/*.ts`、`frontend/src/stores/*.ts`、`frontend/src/composables/*.ts`、`frontend/wailsjs/runtime/runtime` 的 `EventsOn`。

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
   │  类型化、错误处理、参数整理                  │
   v                                            │
Wails 自动生成绑定 (frontend/wailsjs/go/<pkg>/) │
   │  IPC 调用 Go service 方法                   │
   v                                            │
Go: app.go + internal/* Service 方法  <────────┘
```

反向链路（后端推前端）通过 Wails 事件总线：

```text
Go: wailsRuntime.EventsEmit("pty:data:<id>", {s: seq, d: base64})
   → frontend: EventsOn("pty:data:<id>", cb)  // 来自 wailsjs/runtime/runtime
```

## 第一层：Wails 自动生成绑定（禁止手改）

### 目录结构

`wails dev` / `wails build` 会扫描 `main.go` 的 `Bind` 列表，对每个被绑定的 struct 在 `frontend/wailsjs/go/<go-package>/` 下生成一对文件：

| Go struct | 生成目录 | 文件 |
|---|---|---|
| `*App`（package `main`） | `frontend/wailsjs/go/main/` | `App.js`、`App.d.ts` |
| `*config.ConfigService` | `frontend/wailsjs/go/config/` | `ConfigService.js`、`ConfigService.d.ts` |
| `*secrets.SecretsService` | `frontend/wailsjs/go/secrets/` | `SecretsService.js`、`SecretsService.d.ts` |
| `*proxy.ProxyService` | `frontend/wailsjs/go/proxy/` | `ProxyService.js`、`ProxyService.d.ts` |
| `*headroom.HeadroomService` | `frontend/wailsjs/go/headroom/` | `HeadroomService.js`、`HeadroomService.d.ts` |
| `*paths.PathsService` | `frontend/wailsjs/go/paths/` | `PathsService.js`、`PathsService.d.ts` |
| `*logging.Service` | `frontend/wailsjs/go/logging/` | `Service.js`、`Service.d.ts` |
| `*pty.Service` | `frontend/wailsjs/go/pty/` | `Service.js`、`Service.d.ts` |
| `*settings.Service` | `frontend/wailsjs/go/settings/` | `Service.js`、`Service.d.ts` |
| `*updater.Service` | `frontend/wailsjs/go/updater/` | `Service.js`、`Service.d.ts` |
| `*plugin.Service` | `frontend/wailsjs/go/plugin/` | `Service.js`、`Service.d.ts` |
| `*codexplugin.Service` | `frontend/wailsjs/go/codexplugin/` | `Service.js`、`Service.d.ts` |
| `*workspace.Service` | `frontend/wailsjs/go/workspace/` | `Service.js`、`Service.d.ts` |
| `*opencodeconfig.Service` | `frontend/wailsjs/go/opencodeconfig/` | `Service.js`、`Service.d.ts` |
| `*envcheck.Service` | `frontend/wailsjs/go/envcheck/` | `Service.js`、`Service.d.ts` |

此外 `frontend/wailsjs/go/models.ts` 集中放置 Go 端结构体对应的 TS 类（如 `config.Provider`、`session.SessionInfo`、`updater.UpdateInfo` 等），按命名空间组织（`config`、`session`、`settings`、`envcheck`、`updater` 等）。

### 关键约束

**禁止手改 `frontend/wailsjs/`**。该目录每个文件首行都标注 `PEIDIWISH Â MODIWL`（威尔士语 "DO NOT EDIT"）。改后端方法签名后必须 `wails dev` 或 `wails build` 重新生成。

每个绑定的 struct 方法签名直接映射为同名导出函数。例如 `app.go` 的：

```go
func (a *App) LaunchSession(providerName, presetName string, mode string, workDir string, useProxy bool, useHeadroom bool, shellPath string) (string, error)
```

生成 `frontend/wailsjs/go/main/App.d.ts`：

```ts
export function LaunchSession(arg1:string,arg2:string,arg3:string,arg4:string,arg5:boolean,arg6:boolean,arg7:string):Promise<string>;
```

返回 `error` 的方法在 TS 端是 `Promise<T>`，错误以 rejection 形式抛出。

### 拿到 service 引用再调用

某些方法在 `ConfigService` 上而非 `App` 上。前端通过 `App.GetConfigService()` 拿到引用，再调用其方法：

```ts
// frontend/src/api/provider.ts
async function getService() {
  if (!configService) {
    configService = await GetConfigService();  // GetConfigService 来自 main/App
  }
  return configService;
}

export async function getProvider(id: string): Promise<Provider> {
  const service = await getService();
  return await service.GetProvider(id);  // service 是 ConfigService 引用
}
```

## 第二层：`frontend/src/api/*.ts` 包装层

15 个包装模块，每个对应一个业务域：

```
frontend/src/api/
├── index.ts          # 集中再导出，处理命名冲突
├── session.ts        # 会话/PTY 启停与回调注册
├── provider.ts       # 提供商/预设/terminal_presets/OpenCode config
├── plugin.ts         # Claude Code 插件
├── codexPlugin.ts    # Codex 插件
├── workspace.ts      # 工作空间
├── proxy.ts          # Prompt 注入代理
├── settings.ts       # 应用设置
├── remote.ts         # 远程控制 HTTP API 状态
├── envcheck.ts       # 环境检测与一键修复
├── envvars.ts        # 自定义环境变量
├── headroom.ts       # 上下文压缩
├── updater.ts        # 自动更新
├── paths.ts          # 路径管理
└── logs.ts           # 日志导出
```

### 包装范式

每个模块遵循统一风格（以 `provider.ts` 为例）：

```ts
import {
  GetProvidersByType,
  UpdateProvider,
  GetTerminalPresets,
  ResolveTerminalPreset,
  GetConfigService,
  // ...其它从 wailsjs/go/main/App 导入的函数
} from '../../wailsjs/go/main/App';
import { config } from '../../wailsjs/go/models';

type Provider = config.Provider;
type TerminalPreset = config.TerminalPreset;

export async function getProvidersByType(providerType: string): Promise<Record<string, Provider>> {
  try {
    return await GetProvidersByType(providerType);
  } catch (error) {
    console.error('[api.provider.getProvidersByType]', error);
    throw error;
  }
}
```

要点：

- **类型化**：参数与返回值用 `wailsjs/go/models` 中的 TS 类标注。
- **错误处理**：每个调用包 `try/catch`，`console.error` 后再 `throw`，让上层自行决定提示方式。
- **日志前缀**：统一 `[api.<domain>.<fn>]` 便于追溯。
- **零业务逻辑**：不在此层做状态判断，仅整理参数与类型。

### `index.ts` 的命名冲突处理

`settings.ts` 与 `remote.ts` 都暴露 `setRemoteHost` / `setRemotePort`。`index.ts` 显式命名空间化这两组，避免 `export *` 冲突：

```ts
export * from './session';
export * from './provider';
// ...
export {
  getDashboardDefaults,
  // ...settings.ts 其它函数直接导出
} from './settings';
export {
  getRemoteHost as getSettingsRemoteHost,
  getRemotePort as getSettingsRemotePort,
  setRemoteHost as setSettingsRemoteHost,
  setRemotePort as setSettingsRemotePort,
} from './settings';
export {
  setRemotePort,
  setRemoteHost,
} from './remote';
```

调用方若同时使用 settings 与 remote 的端口设置，建议从具体模块（`api/settings`、`api/remote`）按需导入，避免歧义。

## 第三层：Pinia store

5 个 store 位于 `frontend/src/stores/`：

| Store | 文件 | 主要状态 |
|---|---|---|
| `useSessionStore` | `session.ts` | `sessions`、`activeSessionId`、`isPolling` |
| `useProviderStore` | `provider.ts` | providers map、`terminal_presets`、`activeProviderId`、filter |
| `useWorkspaceStore` | `workspace.ts` | 工作空间列表与当前选中 |
| `usePluginStore` | `plugin.ts` | 插件市场与已安装插件 |
| `useUiStore` | `ui.ts` | 主题、侧栏等 UI 状态 |

### 范式：Composition API store

所有 store 采用 setup 风格（`defineStore('<id>', () => { ... })`），用 `ref`/`computed` 暴露 state 与派生数据。以 `session.ts` 为例：

```ts
export const useSessionStore = defineStore('session', () => {
  const sessions = ref<SessionInfo[]>([]);
  const activeSessionId = ref<string | null>(null);
  const isPolling = ref(false);

  const runningSessions = computed(() =>
    sessions.value.filter(s => s.status === 'running')
  );
  const activeSession = computed(() =>
    sessions.value.find(s => s.id === activeSessionId.value) || null
  );

  function setSessions(newSessions: SessionInfo[]) { sessions.value = newSessions; }
  function setActiveSession(sessionId: string | null) { activeSessionId.value = sessionId; }
  // ...

  return { sessions, activeSessionId, isPolling, runningSessions, activeSession,
           setSessions, setActiveSession, /* ... */ };
});
```

store 自身**不直接发起业务调用**，而是由 composable 调用 `api/*.ts` 后写入 store。这样数据流向单一：API → store → 组件。

## 第四层：composables

8 个 composable 位于 `frontend/src/composables/`：

| Composable | 文件 | 职责 |
|---|---|---|
| `useSessionLaunch` | `useSessionLaunch.ts` | 三引擎统一启动、shell 路径解析、mode 决定是否跳 `/terminal` |
| `useSessionList` | `useSessionList.ts` | 轮询 `GetSessions` 写入 store、管理 activeSessionId |
| `useSessionDetailOutput` | `useSessionDetailOutput.ts` | 订阅 `pty:data:<id>`，合并历史 snapshot 与实时 chunk |
| `useTerminalEngine` | `useTerminalEngine.ts` | xterm.js 终端实例与 `pty:data`/`PtyWrite` 桥接 |
| `usePlatformCapabilities` | `usePlatformCapabilities.ts` | 单例化的平台能力（镜像 Go `PlatformCapabilities`） |
| `useDashboardState` | `useDashboardState.ts` | 仪表盘跨组件共享状态（provider/preset/mode 等） |
| `useTheme` | `useTheme.ts` | 主题切换 |
| `useToast` | `useToast.ts` | 全局提示 |

composable 是组合多个 store 与多个 api 模块的胶水层，承担：

- **流程编排**：启动会话需按引擎分流、决定 shellPath、跳转路由（见 `useSessionLaunch.launchFromSettings`）。
- **副作用管理**：`onUnmounted` 清理轮询定时器、`EventsOn` 监听 dispose。
- **派生状态**：从 store 数据计算视图模型。

## 完整调用链示例：启动 Claude Code 会话

下面是从用户点击"启动"按钮到 Go PTY 进程拉起的真实路径。

### 1. 组件触发

某个仪表盘组件（如 `SidebarNormal.vue` 或 `SessionSettingsView.vue`）持有 `useDashboardState`、`usePlatformCapabilities`、`useSessionStore`、`useSessionList` 句柄，在点击回调中调用：

```ts
const { launchFromSettings, canLaunchFromSettings } = useSessionLaunch();
if (canLaunchFromSettings(dashState)) {
  await launchFromSettings(dashState, {
    platformCaps, sessionStore, refresh, router,
    persistDefaults, showSuccess, showError,
    launchingRef,
  });
}
```

### 2. composable 分流

`useSessionLaunch.launchFromSettings`（`useSessionLaunch.ts:126`）：

```ts
if (dashState.engine === 'claudecode') {
  sessionId = await sessionApi.launchClaudeSession({
    providerName: dashState.provider,
    presetName: dashState.preset,
    mode: dashState.claudeMode,
    workDir: dashState.workDir,
    useProxy: dashState.useProxy,
    useHeadroom: dashState.useHeadroom,
    shellPath: dashState.claudeMode === 'embedded'
      ? resolveShellPath(dashState, platformCaps) : '',
  });
}
```

### 3. 包装层整理参数

`api/session.ts: launchClaudeSession`：

```ts
export async function launchClaudeSession(params: {
  providerName: string; presetName: string; mode: string;
  workDir: string; useProxy: boolean; useHeadroom: boolean;
  shellPath?: string;
}): Promise<string> {
  return await LaunchSession(
    params.providerName, params.presetName, params.mode,
    params.workDir, params.useProxy, params.useHeadroom,
    params.shellPath || '',
  );
}
```

### 4. Wails 生成的 TS 绑定

`frontend/wailsjs/go/main/App.js` 中 `LaunchSession` 是 IPC 调用（实现由 Wails runtime 注入），签名为 7 个位置参数、返回 `Promise<string>`。

### 5. Go 方法

`App.LaunchSession`（`app.go:826`）解析 provider/preset、编排代理与 headroom、注入 `--session-id`、调用 `Pty.StartResolved` 拉起 PTY 进程。详细流程见 [./architecture.md#launchsession-主入口](./architecture.md#launchsession-主入口)。

### 6. 输出回流

Go 端 PTY 进程产生输出后，`pty.Service` 通过两条通道转发：

- **桌面**：`wailsRuntime.EventsEmit("pty:data:<sessionID>", {s: emitSeq, d: base64Data})`。
- **移动端**：`outputCallback` 注册表（`RegisterOutputCallback`），由 `remote.Server` 的 WebSocket 桥接。

前端订阅（`useSessionDetailOutput.ts:49`）：

```ts
disposeDataListener = EventsOn(`pty:data:${nextSessionId}`, (eventData: any) => {
  if (eventData && typeof eventData === 'object' && 's' in eventData && 'd' in eventData) {
    seq = eventData.s as number;
    base64Data = eventData.d as string;
  }
  appendLiveChunk(seq, base64ToUint8(base64Data));
});
```

`useTerminalEngine.ts` 用相同事件名接入 xterm.js 实例。

## 反向链路：前端 → 后端写入

用户在终端输入时，`useTerminalEngine` 调用 `PtyWrite(sessionID, base64Data)`：

```ts
// frontend/src/api/session.ts
import { PtyWrite } from '../../wailsjs/go/main/App';
export async function writeTerminal(sessionId: string, data: string): Promise<void> {
  await PtyWrite(sessionId, data);  // data 为 base64 编码的字节流
}
```

Go 端 `pty.Service.PtyWrite` 解码 base64 后写入对应 `PtySession` 的 PTY stdin。

## 平台能力的前端映射

`usePlatformCapabilities.ts` 持有 `PlatformCapabilities` 单例（镜像 Go `platform.PlatformCapabilities`），UI 根据其中的布尔位决策：

- `embeddedTerminalSupported` → 是否显示内嵌终端 tab。
- `systemTraySupported` → 是否显示"最小化到托盘"。
- `secureSecretStoreKind` → 密钥存储后端类型（DPAPI / keychain / unsupported），影响设置页提示文案。
- `supportedShells` / `defaultShellKey` → Shell 选择下拉项。

这些字段是启动时一次性解析的快照，运行期不变。

## 前端构建

`npm --prefix frontend run build` 执行 `vue-tsc --noEmit && vite build`：

- **`vue-tsc --noEmit` 先行**：类型检查作为构建闸门，任何对 `wailsjs/go/*` 的误用（参数个数/类型不符）都会在此暴露。
- **`vite build`**：产出 `frontend/dist`，由 `main.go` 通过 `//go:embed all:frontend/dist` 嵌入二进制。

因此流程惯例：改 Go 方法签名 → `wails dev`/`wails build` 重新生成 wailsjs → 改 `api/*.ts` 签名 → `npm run build` 类型闸门 → 正常打包。

## 相关文档

- [./architecture.md](./architecture.md)：绑定主干、`App` 枢纽、服务包范式。
- [./platform-build-tags.md](./platform-build-tags.md)：平台能力如何在编译期分流。
- [../api.md](../api.md)：后端绑定方法的完整索引。

## 待核实项

- `frontend/wailsjs/go/main/App.js` 的具体生成实现未在文中展开（Wails runtime 注入），如需了解 IPC 协议细节需查阅 Wails v2 文档。
- `useTerminalEngine.ts` 的完整终端实例管理逻辑（行数较多，本篇仅引用事件名与 `PtyWrite` 入口）。
- 各 store 的完整 actions 清单：本篇仅示例 `session.ts` 与 `provider.ts` 头部，其余 store（`workspace`、`plugin`、`ui`）未逐一展开。
