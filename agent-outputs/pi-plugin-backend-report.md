# Pi 插件/包管理后端 — 实现报告

> 任务：pi-compat-execution-plan.md 阶段 A（任务 A）
> 对标基线：`internal/opencodeplugin`
> 调研依据：pi 源码 `dist/core/package-manager.js` + `dist/utils/git.js` + `docs/packages.md`（已端到端核验目录布局）

## 一、变更摘要

为 Pi coding agent 补齐插件/包管理后端全链路，与 Claude/OpenCode/Codex 三个引擎对齐：

1. **新建 `internal/piplugin` 包**：封装 `pi install/remove/update` CLI（写操作）+ 解析 `settings.json` 的 `packages[]` 与扫描 `npm/git` 实体目录（读操作，避免 fork CLI）。可注入基础目录，默认 `$PI_CODING_AGENT_DIR` → `~/.pi/agent`。
2. **app.go 装配 `PiPlugins`**：结构与构造处对标 `Plugins/CodexPlugins/OpenCodePlugins`，经 Wails 绑定直接暴露方法（与现有 plugin 服务同模式）。
3. **P1 增强 `BuildPiModelsConfig`**：可选透传 provider 级 `headers`/`authHeader` 与 model 级 `compat`，仅配置存在时写入，默认行为不变。

## 二、新增文件清单

| 文件 | 职责 |
|------|------|
| `internal/piplugin/types.go` | 数据类型：`Package`/`PackageDetail`/`PackagesData`/`ResourceInfo`/`CommandResult` |
| `internal/piplugin/service.go` | `Service` 主体：list/refresh/details/install/remove/update + settings.json 解析 + 实体目录扫描 |
| `internal/piplugin/source.go` | 源解析（复刻 pi `parseSource`/`parseGitUrl`/`parseNpmSpec`）：npm/git/local → 实体路径 |
| `internal/piplugin/executor.go` | pi CLI 调用（install/remove/update 写操作） |
| `internal/piplugin/service_test.go` | 单测（11 个用例，对标 opencodeplugin/service_test.go） |

## 三、app.go 改动点（4 处，最小化）

```diff
+ "amagi-codebox/internal/piplugin"            // import
  OpenCodePlugins *opencodeplugin.Service
+ PiPlugins       *piplugin.Service             // App 字段（对标 Plugins/CodexPlugins/OpenCodePlugins）
  openCodePluginsSvc := opencodeplugin.NewService("", "", log)
+ piPluginsSvc := piplugin.NewService("", log)  // 构造
  OpenCodePlugins: openCodePluginsSvc,
+ PiPlugins:       piPluginsSvc,                // 装配
```

## 四、API 方法签名列表（前端 api/piPlugin.ts 直接对接）

> 经 Wails 绑定，前端以 `piPlugin.<Method>(...)` 形式调用，返回值与 `opencodePlugin` 同构。

```ts
// 列出 settings.json 中登记的所有包（附实体元数据，按 Name 排序）
piPlugin.ListInstalledPackages(): Promise<Package[]>

// 刷新：返回已装包 + 告警（"已登记但实体目录缺失"等）
piPlugin.RefreshPackages(): Promise<PackagesData>

// 单个包详情（含扫描到的 extensions/skills/prompts/themes 子资源）
piPlugin.GetPackageDetails(source: string): Promise<PackageDetail>

// 通过 pi CLI 安装（写 settings.json + 拉取实体）
piPlugin.InstallPackage(source: string): Promise<CommandResult>

// 通过 pi CLI 移除（从 settings.json packages[] 删除；实体保留以备重装）
piPlugin.RemovePackage(source: string): Promise<CommandResult>

// 通过 pi CLI 更新单个包（未登记返回明确错误）
piPlugin.UpdatePackage(source: string): Promise<CommandResult>
```

**前端类型（与 Package/PackageDetail/ResourceInfo/PackagesData/CommandResult 对应）：**

```ts
interface Package {
  id: string; source: string; sourceType: "npm"|"git"|"local";
  name: string; version?: string; description?: string; author?: string; repository?: string;
  scope: "user"; enabled: boolean;
  installPath?: string; manifestPath?: string; lastUpdated?: string; pinned?: boolean;
  extensions?: string[]; skills?: string[]; prompts?: string[]; themes?: string[];
}
interface ResourceInfo { name: string; filePath: string; type: "extension"|"skill"|"prompt"|"theme" }
interface PackageDetail extends Package { resources: ResourceInfo[]; manifestDeclared: boolean }
interface PackagesData { installed: Package[]; warnings?: string[] }
interface CommandResult { success: boolean; output: string; error?: string }
```

## 五、P1 增强（pi_config.go）

`internal/config/types.go` 新增**可选**字段（omitempty，默认行为不变）：

- `OpenAIFormat.Headers map[string]string` + `OpenAIFormat.AuthHeader *bool`
- `AnthropicFormat.Headers map[string]string` + `AnthropicFormat.AuthHeader *bool`
- `Parameters.PiCompat map[string]any`（model 级 compat，pi 专属命名空间）
- `Provider.EffectiveHeaders(format)` / `Provider.EffectiveAuthHeader(format)`（对标 `EffectiveBaseURL`）

`BuildPiModelsConfig` 仅在这些字段**非空/非 nil 时**写入 pi provider/model 配置：

```go
if headers := provider.EffectiveHeaders(format); len(headers) > 0 { entry["headers"] = headers }
if authHeader := provider.EffectiveAuthHeader(format); authHeader != nil { entry["authHeader"] = *authHeader }
if len(params.PiCompat) > 0 { m["compat"] = params.PiCompat }
```

## 六、验证结果（全部通过）

| 验证项 | 命令 | 结果 |
|--------|------|------|
| 全量构建 | `go build ./...` | ✅ exit 0 |
| vet（piplugin + 根） | `go vet ./internal/piplugin/ ./` | ✅ exit 0 |
| piplugin 单测 | `go test ./internal/piplugin/...` | ✅ ok（11 用例） |
| 受影响既有测试 | `go test ./internal/config/... ./internal/launcher/... ./internal/opencodeplugin/...` | ✅ 全 ok（类型增强无回归） |
| gofmt | `gofmt -l` | ✅ 无待格式化文件 |

**单测覆盖**：List 读 settings+npm/git 实体、过滤对象解析、GetDetails 子资源发现、Refresh 缺失实体告警、Install/Remove/Update 的 CLI 参数校验、Update 未登记拒绝、parseSource 9 种源形态、fallbackName、defaultAgentDir 环境覆盖。

## 七、关键事实核验（端到端核对 pi 源码，非假设）

读 pi `dist/core/package-manager.js` 实测确认（非文档转述）：

1. **npm 实体路径** = `<agentDir>/npm/node_modules/<name>`（**注意**：是 `npm/node_modules/<name>`，不是 `npm/<name>`；name 含 scope 如 `@foo/bar`）。依据：`getManagedNpmInstallPath` → `join(this.agentDir, "npm", "node_modules", source.name)`。
2. **git 实体路径** = `<agentDir>/git/<host>/<user>/<project>`（ref 与 `.git` 不进路径）。依据：`getGitInstallPath` → `resolveManagedPath(<agentDir>/git, source.host, source.path)`。
3. **`PI_PACKAGE_DIR` 不控制包存储**：它只覆盖 pi CLI **自身二进制位置**（self-update 用，见 config.js `getPackageDir`）。包存储（settings.json + npm/ + git/）始终跟 `agentDir`（=`PI_CODING_AGENT_DIR`/`~/.pi/agent`）。故本服务**不需要**独立 packageDir 概念——这是对 gap-analysis 待核验项②的澄清。
4. `settings.json` 的 `packages[]` 元素 = 字符串源 或 `{source, extensions[], skills[], prompts[], themes[]}` 过滤对象（已实测 `packageEntryFromJSON` 两种形态）。

## 八、回滚说明

- 新增包 `internal/piplugin/` 可整体删除；无外部依赖方。
- app.go 4 处改动为纯增量（字段/import/局部变量/装配），删除即恢复。
- config/types.go 与 pi_config.go 的字段/方法为纯可选增量（omitempty），删除透传分支即恢复——无现有 preset/provider 数据受影响。
- 未执行任何 git commit（Git 操作属 taibai 职责）。

## 九、未覆盖路径披露（诚实披露）

1. **未做真实 `pi install` 端到端**：本环境 pi CLI 存在，但未真实跑 `pi install npm:xxx` 验证 CLI 写回 settings.json 的确切格式与实体落盘（验证范围内为单测桩 + 静态源码核验）。Install/Remove/Update 的写路径完全依赖 pi CLI 自身行为——若 pi CLI 版本变更命令语义，需复核。建议 Leader 安排真实环境冒烟（`pi install npm:@earendil-works/pi-ai` 后查 list）。
2. **glob 过滤不展开**：package.json `pi` manifest 声明的路径若是 glob（`extensions/*.ts`），GetDetails 仅作路径记录、不展开求值；约定目录扫描会补充发现实际文件。若需精确"按 manifest glob 列举"，需后续引入 glob 匹配。
3. **项目级 settings.json（.pi/settings.json）不管理**：amagi 只管用户级 `~/.pi/agent/settings.json`（与 opencodeplugin 只管 global 同构）。项目级 package 管理（`-l` / trust）超出范围。
4. **`pi config` 启用/禁用单资源不封装**：pi 的 enable/disable 走交互式 `pi config`，无脚本化子命令，故未封装（与 opencodeplugin 不封装 `opencode config` 一致）。

## 十、建议下一步

1. **前端**（luoshen，依赖本报告第四节 API 形状）：`api/piPlugin.ts` + `stores/piPlugin.ts` + `PiPluginPanel.vue`（对标 `OpenCodePluginPanel.vue`）+ ExtensionsView 加 Pi tab（gap-analysis T1.4–T1.6）。
2. **真实环境冒烟**：在装了 pi 的环境实测 `Install → List → GetDetails → Remove` 全链路（解披露项1）。
3. （可选）若实际 provider 触发 compat 需求（如某 OpenAI 兼容端点不支持 developer role），可在 preset 配置里填 `pi_compat`，无需再改后端。
