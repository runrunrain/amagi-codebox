# 后端 API 参考（开发者视角）

面向需要新增、调用或排查后端 API 的开发者。本文档只讲绑定机制与开发流程，**完整方法清单与参数返回值请查 `../api.md`**（已从仓库根的 `API.md` 迁入 `docs/`，按服务分组）。

相关文档：
- 完整方法清单：`../api.md`。
- 构建与绑定再生成：`./build-dev.md`。
- 测试约定：`./testing.md`。

## 绑定生成机制（核心事实）

Amagi CodeBox 的前后端通信基于 Wails v2 的绑定。事实链路：

1. `main.go` 的 `wails.Run` 在 `Bind` 字段里列出所有要暴露给 JS 的 struct：
   ```go
   Bind: []any{
       app,
       app.Config,
       app.Secrets,
       app.Proxy,
       app.Headroom,
       app.Paths,
       app.Log,
       app.Pty,
       app.Settings,
       app.Updater,
       app.Plugins,
       app.CodexPlugins,
       app.Workspaces,
       app.OpenCodeConfig,
       app.EnvCheck,
   },
   ```
   即 `App` 本体加上 14 个服务 struct，共 15 个绑定（核实自 `main.go`）。

2. 这些 struct 的**导出方法**（首字母大写）会被 Wails 在 `wails dev` / `wails build` 时自动生成 TypeScript 绑定，落到：
   ```
   frontend/wailsjs/go/main/App.ts          # App 本体方法
   frontend/wailsjs/go/main/Config.ts       # 各服务（命名按 struct 类型）
   frontend/wailsjs/go/main/models.ts       # 参数/返回值涉及的 Go struct 类型
   ```
3. 前端 `frontend/src/api/*.ts`（共 15 个领域模块：provider/session/plugin/codexPlugin/workspace/proxy/settings/paths/logs/updater/envvars/envcheck/headroom/remote 以及聚合的 index）把这些原始绑定包装为类型化、带错误处理的函数，Pinia store 与 Vue 组件只消费包装层，不直接碰 `wailsjs/`。

数据流：

```
Vue 组件 / composable
      ↓
frontend/src/stores/*      （Pinia）
      ↓
frontend/src/api/*.ts      （类型化包装层）
      ↓
frontend/wailsjs/go/...    （Wails 自动生成，禁止手改）
      ↓
Go: main.go Bind 的 struct 方法
      ↓
internal/<pkg>/*           （服务实现）
```

## 绝对规则：不要手改 `frontend/wailsjs/`

`frontend/wailsjs/` 全部是自动产物：
- 改后端方法签名、新增方法、新增绑定 struct 后，用 `wails dev` 或 `wails build` 重新生成。
- 直接编辑该目录下的文件会被下一次生成覆盖，且会让前端调用与后端真实签名漂移。

如需核实当前前端可用方法，直接看 `frontend/wailsjs/go/main/App.ts` 与同目录其他文件，它们是当前二进制实际暴露方法的事实真相源。

## 如何新增一个后端 API

以"在 `internal/config` 的 `ConfigService` 上新增一个导出方法"为例，正确顺序：

### 第 1 步：在服务 struct 上加导出方法

`internal/config` 的服务 struct 是 `ConfigService`。新增方法必须**首字母大写**（导出），参数与返回值用 Wails 能跨语言序列化的类型（基础类型、struct、slice、map，避免 chan、func、不可导出字段）。

```go
// internal/config/service.go（示意）
func (s *ConfigService) ListProviderTags(providerType string) ([]string, error) {
    // ...
    return tags, nil
}
```

注意：
- Wails 把多返回值中的 `error` 映射为 JS 端 `Promise<T>` 的 reject；无 error 时返回 `(T, nil)` 对应 `Promise<T>`。
- 不可导出字段不会跨边界；如需暴露，加 JSON tag 或用导出 struct。

### 第 2 步：确认 struct 已在 `main.go` 的 `Bind` 列表里

`ConfigService` 已经通过 `app.Config` 绑定。若是**全新服务 struct**，需要：

1. 在 `app.go` 的 `App` struct 上加指针字段。
2. 在 `NewApp` 里构造并赋值。
3. 在 `main.go` 的 `Bind` 列表追加 `app.YourService`。
4. 在 `app.go` 的 `Shutdown` 里加对应清理。

### 第 3 步：重新生成绑定

```bash
wails dev    # 开发时自动生成并热重载
# 或
wails build  # 生产构建时生成
```

生成后 `frontend/wailsjs/go/main/Config.ts` 会多出 `ListProviderTags` 的 TS 声明。

### 第 4 步：在 `frontend/src/api/` 加类型化包装

参考 `frontend/src/api/provider.ts` 的现有范式（核实自该文件）：

```ts
import { ListProviderTags } from '../../wailsjs/go/main/Config';
// 或从 App 转发：import { ListProviderTags } from '../../wailsjs/go/main/App';

export async function listProviderTags(providerType: string): Promise<string[]> {
  try {
    return await ListProviderTags(providerType);
  } catch (error) {
    console.error('[api.provider.listProviderTags]', error);
    throw error;
  }
}
```

包装层职责：
- 加领域化命名（camelCase，符合 TS 习惯）。
- 集中错误日志（前缀 `[api.<module>.<fn>]`）。
- 必要时把 `wailsjs/go/models.ts` 的生成类型重命名为业务别名（`type Provider = config.Provider`）。

### 第 5 步：自检

- `npm --prefix frontend run build`：确保 `vue-tsc --noEmit` 通过（新包装的类型与生成绑定匹配）。
- `wails dev` 在 UI 里调用新方法，确认返回与错误路径都正常。
- 详见 `./build-dev.md` 与 `./testing.md`。

## 前端调用范式

`frontend/src/api/provider.ts` 示例（节选自该文件）：

```ts
import { GetProvidersByType, GetConfigService } from '../../wailsjs/go/main/App';
import { config } from '../../wailsjs/go/models';

type Provider = config.Provider;

export async function getProvidersByType(
  providerType: string,
): Promise<Record<string, Provider>> {
  try {
    return await GetProvidersByType(providerType);
  } catch (error) {
    console.error('[api.provider.getProvidersByType]', error);
    throw error;
  }
}
```

约定：
- 所有领域 API 集中在 `frontend/src/api/`，按领域分文件（`provider.ts`、`session.ts`、`plugin.ts` 等）。
- `frontend/src/api/index.ts` 做聚合 re-export，注意处理命名冲突（如 `settings` 与 `remote` 都暴露 `setRemoteHost/setRemotePort`，index 用命名空间化导出）。
- 组件不直接 import `wailsjs/go/...`，只从 `@/api` 消费。
- 路由用 hash history（`createWebHashHistory`，核实自 `CLAUDE.md` 架构段）。

前端桥接的完整说明（绑定目录结构、Pinia store、composables、端到端调用链）见 [./frontend-backend.md](./frontend-backend.md)。

## 内部桥接方法：GetSettingsService / GetConfigService / GetPathsService

`docs/api.md` 顶部明确说明，App 上的这三个方法**主要供远程层内部桥接使用，不是常规前端调用入口**：

| 方法 | 返回 | 用途（核实自 `docs/api.md`） |
|------|------|------------------------------|
| `GetSettingsService` | `*settings.Service` | 返回设置服务实例，供远程层内部桥接 |
| `GetConfigService` | `*config.ConfigService` | 返回配置服务实例，供远程层内部桥接 |
| `GetPathsService` | `*paths.PathsService` | 返回路径服务实例，供远程层内部桥接 |

机制：远程控制（`internal/remote/` 的 HTTP + WebSocket 服务器，供移动端 `mobile/` 使用）运行在 Go 进程内，它需要直接持有这些服务 struct 来执行操作，而不是走 Wails 的 JS 绑定往返。这些 getter 把服务实例暴露给远程层。

副作用：因为它们是 App 的导出方法，Wails 也会为前端生成 TS 绑定。前端确实**可以**调用它们拿到服务对象，再调用服务对象上的方法（`frontend/src/api/provider.ts` 就用 `GetConfigService()` 拿到实例后缓存复用）：

```ts
// 节选自 frontend/src/api/provider.ts
let configService: any = null;

async function getService() {
  if (!configService) {
    configService = await GetConfigService();
  }
  return configService;
}
```

开发指引：
- 常规新增前端能力时，**优先**直接在服务 struct 上加导出方法（走标准绑定），不要依赖 `GetXxxService` 拿实例后再在 JS 侧反射式调用。
- 只在确实需要把整个服务对象交给 JS 复用时（如 `provider.ts` 的批量 CRUD），才用 getter 模式；并注意拿到的 service 实例上的方法变化同样要靠 `wails build` 重新生成绑定。

## 远程控制 API（HTTP/WebSocket）

另一条独立的后端 API 线：`internal/remote/` 启用的 HTTP + WebSocket 服务器，供移动端使用。实际注册端点（核实自 `internal/remote/handlers.go`）包括 `GET /api/info`、`GET|POST|DELETE /api/sessions[...]`、`GET|PUT /api/providers[...]`、`GET|PUT /api/settings`、`GET /api/logs`、`GET /api/paths`、`GET /api/secrets/diagnostics`、`WebSocket /ws/terminal/{sessionID}` 等；所有请求需在 `Authorization` 头携带 Token。完整端点表见 [../user/remote-mobile.md](../user/remote-mobile.md)。

这些 HTTP 端点**不走 Wails 绑定**，与本文档的 JS 绑定机制是两条独立通道。开启/关闭、改端口、换 Token 通过 App 的 `ToggleRemoteServer`、`SetRemotePort`、`RegenerateRemoteToken`、`GetRemoteStatus` 等方法控制（清单见 `../api.md`）。注意 Token 重置无远程端点，只能通过桌面端 `RegenerateRemoteToken`。

## 服务的组织约定

每个 `internal/<pkg>/` 服务包遵循统一形态（核实自 `CLAUDE.md`）：

- 一个 `Service` 或 `ConfigService` struct。
- 一个 `New...()` 构造函数（如 `NewConfigService(...)`）。
- 导出方法供前端/远程层调用。
- 跨平台差异通过 `_<os>.go` 文件 + build constraints 处理，不用运行时 `if runtime.GOOS`。

服务包示例：`internal/config`（providers/presets）、`internal/secrets`（keychain）、`internal/session`（CLI 会话）、`internal/plugin` + `internal/codexplugin`、`internal/envcheck`、`internal/remote`、`internal/pty`、`internal/updater`、`internal/workspace`、`internal/headroom`、`internal/proxy`（prompt 注入引擎）。

## 待核实项

- `docs/api.md` 当前 Table of Contents 列出 11 个服务分组，而 `main.go` 的 `Bind` 列表实际有 14 个服务 struct。差异：Headroom、CodexPlugins、Workspaces、EnvCheck 这 4 个服务的导出方法**是否已全部收录**进 `../api.md`（待核实，建议补齐 TOC 与方法清单）。
- `frontend/src/api/headroom.ts`、`envcheck.ts`、`codexPlugin.ts`、`workspace.ts` 已存在（glob 核实），说明前端包装层已覆盖；缺失的是 `docs/api.md` 的分组叙述，不是绑定缺失。
