# 后端 API 参考（开发者视角）

面向需要新增、调用或排查后端 API 的开发者。本文档只讲绑定机制与开发流程；**完整方法清单与参数返回值请查 `../api.md`**（按服务分组的全量索引）。

相关文档：
- 完整方法清单：`../api.md`。
- 架构与绑定主干：`./architecture.md`。
- 构建与绑定再生成：`./build-dev.md`。
- 测试约定（含绑定冻结测试）：`./testing.md`。

核实日期 2026-08-22，版本 1.3.50。

## 绑定生成机制（核心事实）

Amagi CodeBox 的前后端通信基于 Wails v2 绑定。事实链路：

1. `main.go` 的 `wails.Run` 在 `Bind` 字段调用 `buildWailsBindList(app)`（`bind_list.go`），返回 **21 个绑定 = App + 20 个服务**（Config、Secrets、Paths、Log、Settings、Updater、Plugins、CodexPlugins、OpenCodePlugins、PiPlugins、OmpPlugins、OpenCodeConfig、PiConfig、OmpConfig、AgentProfiles、EnvCheck、Usage、WebUI、Skins、GitAssist）。精确清单见 [./architecture.md#绑定主干-21-个绑定](./architecture.md#绑定主干-21-个绑定)。
2. 被绑定 struct 的**导出方法**（首字母大写）在 `wails dev` / `wails build` 时自动生成 TS 绑定，落到 `frontend/wailsjs/go/<go-package>/<Struct>.{js,d.ts}`；参数/返回值涉及的 Go struct 类型集中到 `frontend/wailsjs/go/models.ts`。
3. 前端 `frontend/src/api/*.ts`（27 个文件）把这些原始绑定包装为类型化、带 `callApi` 错误处理的函数；Pinia store（10 个）与 Vue 组件只消费包装层，不直接碰 `wailsjs/`。

数据流：

```
Vue 组件 / composable
      ↓
frontend/src/stores/*      （Pinia）
      ↓
frontend/src/api/*.ts      （类型化包装层，callApi 统一 log+rethrow）
      ↓
frontend/wailsjs/go/...    （Wails 自动生成，禁止手改）
      ↓
Go: bind_list.go 绑定的 struct 方法
      ↓
internal/<pkg>/*           （服务实现）
```

## 绑定冻结边界（T-24，改表面前必读）

绑定表面不是自由演进的——`bind_manifest_test.go` 用反射把它冻成了硬断言：

- **原始 `pty.Service` / `headroom.HeadroomService` 永不入 Bind 列表**（设计 §6.3 C-01）。终端写/缩放走 `App.PtyWrite`/`PtyWriteLarge`/`PtyResize`，headroom 变更走 `App.Headroom*` 门面（lease 守卫）。
- **`App` 上不得出现原始旁路**：`StopAllSessions`、`GetOutputHistory`、`Register/Unregister*Callback`，以及任何含 `WriteRaw`/`ResizeRaw`/`CloseSession`/`CloseAll` 片段的方法名。
- **门控门面必须恰好存在**：`appGatedMutationMethods` 清单（`PtyWrite`/`PtyResize`/`StopSession`/`RemoveSession`/`ClearStoppedSessions` 等）缺失即测试失败（M-005）。
- **新增非会话变更方法**（如 `ConfirmExternalCleanupRecovery`、`SetCodexGlobalHeadroom`）必须在 `appClassifiedNonSessionMutations` 表中登记门控语义，使表面漂移可评审。

因此新增/删除/重命名绑定方法时，`go test . -run TestBindManifest` 是必须过的门；需要扩面时同步更新该测试的登记表。

## 绝对规则：不要手改 `frontend/wailsjs/`

- 改后端方法签名、新增方法、新增绑定 struct 后，用 `wails dev` 或 `wails build` 重新生成。
- 直接编辑会被下一次生成覆盖，并让前端调用与后端真实签名漂移。
- 核实当前前端可用方法时，直接看 `frontend/wailsjs/go/` 下生成文件——它们是当前二进制实际暴露方法的事实源。

## 如何新增一个后端 API

以"在 `internal/config` 的 `ConfigService` 上新增导出方法"为例：

### 第 1 步：在服务 struct 上加导出方法

方法必须**首字母大写**；参数与返回值用 Wails 可序列化的类型（基础类型、struct、slice、map；避免 chan、func、不可导出字段）。

```go
// internal/config/service.go（示意）
func (s *ConfigService) ListProviderTags(providerType string) ([]string, error) {
    // ...
    return tags, nil
}
```

注意：多返回值中的 `error` 映射为 JS 端 `Promise<T>` 的 reject。

### 第 2 步：确认 struct 已在 `bind_list.go` 的绑定列表里

`ConfigService` 已经通过 `app.Config` 绑定。若是**全新服务 struct**：

1. 在 `app.go` 的 `App` struct 上加指针字段。
2. 在 `NewApp` 里构造并赋值。
3. 在 `bind_list.go` 的 `buildWailsBindList` 返回切片中追加 `app.YourService`。
4. 在 `Shutdown` 里加对应清理。
5. 评估是否触及 T-24 冻结边界（原始服务能力不得直接绑定，需走 App 门控门面）。

### 第 3 步：重新生成绑定

```bash
wails dev    # 开发时自动生成并热重载
# 或
wails build  # 生产构建时生成
```

### 第 4 步：在 `frontend/src/api/` 加类型化包装

现行范式（核实自 `provider.ts` 与 `internal/call.ts`）：

```ts
import { ListProviderTags } from '../../wailsjs/go/config/ConfigService';
import { callApi } from './internal/call';

export async function listProviderTags(providerType: string): Promise<string[]> {
  return callApi('[api.provider.listProviderTags]', () => ListProviderTags(providerType));
}
```

包装层职责：领域化命名（camelCase）；经 `callApi` 统一 "log + rethrow" 错误语义；必要时把 `wailsjs/go/models` 的生成类型重命名为业务别名（`type Provider = config.Provider`）。`callApi` 仅供 `api/` 内部使用，勿向 views/stores 暴露。

### 第 5 步：自检

- `go test . -run TestBindManifest`：绑定冻结断言全绿。
- `npm --prefix frontend run build`：`vue-tsc --noEmit` 通过。
- `wails dev` 在 UI 里实际调用新方法，确认返回与错误路径。

## 内部桥接方法：GetSettingsService / GetConfigService / GetPathsService

`App` 上这三个 getter **主要供远程层内部桥接**（`remote.AppInterface`，让 `remote.Server` 在进程内直接持有服务实例，不走 JS 绑定往返）。

副作用：它们是 App 导出方法，Wails 也会生成 TS 绑定。前端确实可以用它们拿服务对象句柄再调方法——`frontend/src/api/provider.ts` 就用 `GetConfigService()` 拿代理句柄并缓存，且按实际调用面显式声明 `ConfigServiceHandle` 接口（生成的 `models.ts` 中服务类不携带方法签名）。

开发指引：
- 常规新增前端能力**优先**直接在服务 struct 上加导出方法走标准绑定。
- 只在确实需要把整个服务对象交给 JS 复用时（如批量 CRUD）才用 getter 模式。

## 远程控制 API（HTTP/WebSocket，独立通道）

另一条不走 Wails 绑定的后端 API 线：`internal/remote/` 的 HTTP + WebSocket 服务器，双栈：

- **Legacy 栈**：`/api/...` token 认证端点（`handlers.go` 注册），在 `buildHandler` 派发层面对非 loopback 一律 403（认证前）。
- **v1 栈**：`/api/remote/v1` 10 个 REST 端点 + `/ws/v1`（设备配对 + Cookie 认证 + 控制授权），线协议规范见 [./remote-api-v1-contract.md](./remote-api-v1-contract.md)。

远程服务的开关、端口、Token 通过 `App` 的 `ToggleRemoteServer`、`SetRemotePort`、`RegenerateRemoteToken`、`GetRemoteStatus` 等方法控制（清单见 `../api.md`）。Token 重置无远程端点，只能走桌面端。

## 服务的组织约定

每个 `internal/<pkg>/` 服务包遵循统一形态：一个 `Service`/`ConfigService` struct + `New...()` 构造函数；导出方法供前端/远程层调用；跨平台差异用 `_<os>.go` + build constraints 分流（见 `./platform-build-tags.md`）。34 个一级包的分组概述见 [./architecture.md#internal-包分组-34-个一级包](./architecture.md#internal-包分组-34-个一级包)。

## 与 `docs/api.md` 的分工

| 文档 | 内容 |
|---|---|
| 本文档 | 绑定机制、冻结边界、新增 API 的开发流程、前端包装范式 |
| `../api.md` | 全部绑定方法的分组清单（方法名、参数、返回值） |

新增绑定方法后需要同步更新 `../api.md` 的方法清单。

## 待核实项

- `../api.md` 的方法清单由另一文档切片维护；本文仅承诺机制层事实，方法级漂移以 `frontend/wailsjs/go/` 生成物为准。
