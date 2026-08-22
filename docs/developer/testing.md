# 测试约定

面向为 Amagi CodeBox 添加或修改测试的开发者。内容基于 `.github/workflows/ci.yml`、`playwright.config.ts`、`mobile/vitest.config.ts`、`frontend/vitest.config.ts`、`frontend/package.json`，以及对仓库 `*_test.go` 的 glob 核实（核实日期 2026-08-22，版本 1.3.50）。

相关文档：
- 构建与本地开发见 `./build-dev.md`。
- 后端 API 与绑定生成见 `./api-reference.md`。

## 总体形状

| 子项目 | 测试/门类型 | 工具 | CI 覆盖 |
|---------|------------|------|----------|
| Go 后端（`internal/`、根目录、`cmd/codebox/`） | 单元 + 集成 + 回归系列测试 | Go 自带 `testing` | macos 腿全量 `go test ./... -count=1`；windows 腿仅编译检查（`-run '^$'`）；两腿均 `go vet` + `golangci-lint` |
| 桌面前端（`frontend/`） | 单元测试（Vitest，`src/__tests__/`）+ 类型检查门 | Vitest + `vue-tsc --noEmit`（内嵌于 `build`） | 均进 CI（`npm run build` + `npm run test`） |
| 移动端前端（`mobile/`） | 单元测试 + 类型检查 | Vitest + `vue-tsc -b` | 均进 CI（`npm run build` + `npm run test` + 对比度检查） |
| E2E（`e2e/`） | Playwright 端到端 + 性能套件 | `@playwright/test`（Chromium-only） | **不进 CI**，手动运行 |

注意：CI 由 `workflow_dispatch` 手动触发（C-011：stage push 不消耗 Actions），不是 push 自动跑。

## 测试文件位置与命名规范

新增测试按子项目对号入座，禁止第三处散落：

| 子项目 | 位置 | 命名 | 理由 |
|--------|------|------|------|
| Go 单测 | 与被测代码**同包同目录**（语言强制，无法集中） | 被测文件同名 `_test.go` 优先；主题/回归测试用语义化 `<topic>_test.go`；`internal/remote/` 既有里程碑系列沿用 `<系列>_<编号>_<主题>_test.go` | Go 工具链强制；语义化文件名可检索，编号锚点放文件头注释 |
| 桌面前端单测 | `frontend/src/__tests__/`（镜像 src 目录结构） | `<模块名>.test.ts` | 新建体系（原为零单测）；与 mobile 集中模式对齐 |
| 移动端单测 | `mobile/src/__tests__/`（集中，维持现状） | 既有 `*.test.ts` | 历史已确立（61 个测试文件），不动 |
| E2E 主套件 | 根 `e2e/`（`playwright.config.ts`） | `*.spec.ts` | 跨端/remote/集成主入口 |
| E2E 专项套件 | `frontend/e2e/`（`frontend/playwright.stopping.config.ts`） | `*.spec.ts` | stopping config 专属（stopping-session / terminal-rendering 专项），不另设入口 |

细则：

- **禁止新增批次编号前缀文件名**（如 `m005_`、`r5_002_` 这类新批次编号前缀）：新系列/新批次一律语义化 `<topic>_test.go`，编号锚点（设计/缺陷编号）放文件头注释。既有文件不追溯改名：`internal/remote/` 里程碑系列（m001~m011、b2a/b2c 等）保留原名并可沿用系列命名续写；根目录 R 系列已语义化重命名，文件头注释保留原编号锚点。
- 根目录 `package main` 新增测试仅限 **App 绑定面 / 进程级行为**；能下沉 `internal/` 包的应下沉。
- frontend 测试文件显式 `import { describe, it, expect } from 'vitest'`（`frontend/vitest.config.ts` 不开 `globals`，与 mobile 不同），保证 `vue-tsc --noEmit` 类型门通过。

## Go 测试

### 规模与分布

仓库现有 **217 个 `*_test.go`**（glob 核实，不含 vendor）。分布：

- **根目录**：`app_test.go`、`app_persistence_test.go`、`app_envcheck_test.go`、`app_codex_config_test.go`、`app_remoteclient_test.go`、`app_terminal_windows_test.go` 等，以及大量**回归系列测试**（见下）。
- **`internal/remote/`**：测试最密集的领域（m001–m011、m2a、b2a/b2b/b2c1/b2c2、c5b 等系列 + 大量领域测试），对应远程 v1 契约的逐条硬化。
- 其余热点：`internal/session/`、`internal/pty/`、`internal/usage/`、`internal/config/`、`internal/platform/`、`internal/envcheck/` 等。

### 回归系列命名约定

仓库存在按里程碑/缺陷编号命名的回归测试系列，文件名即追溯锚点：

- 根目录：`bind_failclosed_test.go`、`shared_coordinator_test.go`、`desktop_ledger_destroy_test.go`、`clear_stopped_failclosed_test.go` / `clear_stopped_manager_sync_test.go` / `clear_stopped_manager_failure_test.go`、`launch_admission_test.go`、`external_headroom_lease_test.go`、`shutdown_persistence_stop_gate_test.go`、`durable_ownership_r9_test.go` / `durable_ownership_r10_test.go`、`external_recovery_test.go`、`raw_port_gap_test.go`、`remote_security_migration_gate_test.go` 等（R 系列已语义化重命名，文件头注释保留原缺陷/设计编号锚点）。
- `internal/remote/`：`m001_allow_header_test.go` ~ `m011_realpath_test.go`、`m2a_*`（M2-A 适配器）、`b2a/b2b/b2c1/b2c2_*`（B2 阶段证据修正）、`c5b_checkpoint_desktop_take_test.go` 等。

新增回归测试时：`internal/remote/` 既有系列沿用 `<系列>_<编号>_<主题>_test.go` 续写；其余一律语义化命名并在文件头注释标注设计/缺陷锚点（详见上文「测试文件位置与命名规范」）。

### 绑定冻结断言：`bind_manifest_test.go`（T-24）

根目录 `bind_manifest_test.go` 用反射冻结 Wails 绑定表面，属于每次 `go test` 都会执行的硬断言：

- **禁止原始服务入列**：Bind 列表中不得出现 `pty.Service` / `headroom.HeadroomService`（C-01）。
- **禁止 App 旁路方法**：`StopAllSessions`、`GetOutputHistory`、`Register/Unregister{Output,Exit,Resize}Callback` 不得存在于 `App` 导出方法面。
- **门控门面必须可达**：`PtyWrite`、`PtyWriteLarge`、`PtyResize`、`StopSession`、`RemoveSession`、`ClearStoppedSessions`、`GetOutputHistorySnapshot`、`HeadroomStart/Stop/GetStatus` 必须存在。
- **原始变更名片段禁漏**：任何导出 App 方法名不得含 `WriteRaw`/`ResizeRaw`/`CloseSession`/`CloseAll`。
- **已分类非会话变更登记**：`ConfirmExternalCleanupRecovery`、`SetCodexGlobalHeadroom` 等在 `appClassifiedNonSessionMutations` 表中显式登记门控语义，漂移可评审。
- **Secrets 死绑定方法冻结**：`GetZhipuAPIKey` 等已删除的 shim 不得复活；`GetAllProviders` 必须保留（`cmd/codebox` 有真实调用）。

改动绑定表面（增删绑定对象、增删 App 导出方法）前先读这个文件。

### 运行命令

```bash
go test ./...                                    # 全量
go test ./internal/config                        # 单包
go test ./internal/config -run TestServiceName   # 单包单测
go test -race ./internal/session                 # 竞态检测（并发包推荐）
go test ./internal/remote -v                     # 详细输出
go vet ./...                                     # CI 静态检查（两腿都跑）
```

### Go 测试模式（重要）

#### 1. 普通单元测试

最常见形式，直接 `go test ./internal/<pkg>`。

#### 2. 集成测试（环境门控）

通过环境变量开启，默认 `t.Skip()`。代表：`internal/codexplugin/install_integration_test.go`（`AMAGI_CODEBOX_ACTUAL_CODEX_INSTALL_TEST=1`，且限 darwin）：

```bash
AMAGI_CODEBOX_ACTUAL_CODEX_INSTALL_TEST=1 go test ./internal/codexplugin -run TestActualInstallPluginResolvesCodexFromLocalNodeBinWithGUIPATH -v
```

适用：依赖真实本机环境的端到端校验，不适合无条件运行。

#### 3. Build-tag 真实样本测试

用 build constraints 隔离，默认不编译。代表：`internal/session/tracker_realfixture_test.go`（`//go:build realfixture`，用真实 jsonl 样本验证修复效果，fixture 不存在则 skip）：

```bash
go test -tags realfixture ./internal/session/... -run TestRealFixture_MasterJSONL -v
```

#### 4. 平台特定测试

文件名后缀隔离，非目标平台自动不参与编译：`internal/pty/service_darwin_test.go`（仅 macOS）、`app_terminal_windows_test.go` / `internal/envvars/platform_windows_test.go`（仅 Windows）、`internal/platform/capabilities_runtime_test.go`（跨平台）等。

### 新增 Go 测试的建议

- 优先放在被测包内做同包白盒测试；跨包端到端场景才放根目录。
- 涉及并发（session、pty、remote、usage 同步）默认加 `-race` 复跑。
- 依赖真实环境的测试**必须**用环境变量或 build tag 默认跳过，并在文件头写明跑法。
- 不要提交 `go test -c` 产物（仓库根曾有误提交的测试二进制，已清理）。

## 桌面前端（Vitest，进 CI）

`frontend/package.json`：`"test": "vitest run"`，`"build": "vue-tsc --noEmit && vite build"`。`frontend/vitest.config.ts` 为独立配置：`environment: 'node'`、`include: ['src/__tests__/**/*.test.ts']`——不引 jsdom，避免新依赖面；未来出现组件级测试需求再评估 DOM 环境。

测试集中在 `frontend/src/__tests__/` 镜像 src 目录结构（与 mobile 对齐），命名 `<模块名>.test.ts`；测试文件显式 `import { describe, it, expect } from 'vitest'`（不开 `globals`，保证 vue-tsc 类型门通过）。

```bash
npm --prefix frontend run test     # vitest run
npm --prefix frontend run build    # 类型检查 + 生产构建
```

CI frontend 腿：`npm ci` → `npm run build` → `npm run test`。

## 移动端测试（Vitest，进 CI）

`mobile/package.json`：`"test": "vitest run"`，`"build": "vue-tsc -b && vite build"`。`mobile/vitest.config.ts` 开启 `globals: true`、`environment: 'jsdom'`、`include: ['src/**/*.{test,spec}.ts']`。

测试文件 61 个（glob 核实），覆盖 `mobile/src/__tests__/`（parser、composables、components、views、utils、types、api）与 `mobile/src/lib/contract/wire.test.ts`（v1 线契约，与 Go 侧共享 `testdata/v1-wire-fixtures.json`）。

```bash
npm --prefix mobile run test         # vitest run
npm --prefix mobile run test:watch   # watch 模式
```

CI 中移动端腿执行 `npm run build`（`vue-tsc -b` 硬门）+ `npm run test` + `node scripts/check-contrast.mjs`（对比度检查）。

## E2E（Playwright，不进 CI）

E2E 基建位于仓库根：`playwright.config.ts` + `e2e/` 目录。冻结边界（C-009）：

- **Chromium-only**，三视口 project；不配置 WebKit/Firefox；`video: 'off'`。
- 隔离端口 `E2E_PORT ?? 4317`；CI 环境单 worker + 2 retries。
- smoke 走**真实 mobile Vite dev server**（webServer），无 mock server/假数据。
- 截图基线在 `e2e/baselines/<platform>/<project>/`。

当前 spec 清单：`smoke`、`network`、`network-relay`、`timing`、`lobby-timing-m3`、`a11y-m4`（axe 无障碍）、`connect-pg01[-real]`、`sessions-pg02`、`workspace-m3*`（int-multidevice / int-relay / m3c[-relay] / notice-stack / pg03 / pg04 / real）。`e2e/harness/remote-server/main.go` 提供测试用远程服务器 harness；`e2e/helpers/` 提供共享 helper。

### 运行

```bash
npx playwright test                    # 默认入口（本地需先装 Chromium：npx playwright install chromium）
```

### 性能套件（独立 runner，默认排除）

`e2e/perf/`（M4-B）自带环境契约（`M4B_ROUND=N`）与独立入口：

```bash
e2e/perf/run-perf.sh                   # 或 npx playwright test -c e2e/perf/playwright.perf.config.ts
```

默认入口的每个 project 都 `testIgnore: '**/perf/**'`——不带 `M4B_ROUND` 时性能用例会确定性抛错，故全局排除，只经独立 perf config 显式运行。

E2E 不在 `ci.yml` 中执行，属手动/专项运行。

## CI 实际执行的内容

`.github/workflows/ci.yml`（`workflow_dispatch` 手动触发）两个 job：

**frontend-mobile**（windows-latest）：
1. Setup Node `20.19.0`（npm cache 依赖两个 lock 文件）。
2. frontend：`npm ci` → `npm run build` → `npm run test`。
3. mobile：`npm ci` → `npm run build` → `npm run test` → `node scripts/check-contrast.mjs`。
4. 上传 `frontend/dist` + `mobile/dist` 为 artifact（`embedded-web-assets`）。

**go-quality**（needs: frontend-mobile，matrix windows-latest + macos-latest）：
1. 下载嵌入资源 artifact。
2. Setup Go `1.25.0`。
3. `go vet ./...`（两腿）。
4. `golangci-lint`（pinned `v2.12.2`，两腿——使 `_windows.go`/`_darwin.go` 平台文件均被静态检查）。
5. macos 腿：`go test ./... -count=1`（全量）；windows 腿：`go test ./... -run '^$' -count=1`（仅编译检查）。

不在 CI 中的：`go test -race`、E2E/性能套件、macOS 的 `wails build` 端到端验证（release 走 `release.yml`）。

## 提交前的最小自检清单

改 Go 代码：
- `go vet ./...`（CI 两腿）。
- `go test ./...`（CI macos 腿；windows 专属路径本地用 `GOOS=windows go build ./...` 补验证）。
- 并发包额外 `go test -race ./internal/<并发包>`。
- 动绑定表面时确认 `go test . -run TestBindManifest` 全绿。

改桌面前端：`npm --prefix frontend run build && npm --prefix frontend run test`（CI）。

改移动端：`npm --prefix mobile run build && npm --prefix mobile run test`（CI）。

改远程 v1 线协议：同一 diff 内同步更新 `docs/developer/remote-api-v1-contract.md`、Go/TS 契约骨架、`mobile/src/lib/contract/testdata/v1-wire-fixtures.json` 与两侧 wire 测试（契约文档头部规范）。

## 待核实项

- CI 仅 workflow_dispatch 手动触发；stage 期间的回归验证依赖本地手动跑测试。
