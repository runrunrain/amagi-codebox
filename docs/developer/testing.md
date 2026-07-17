# 测试约定

面向为 Amagi CodeBox 添加或修改测试的开发者。内容基于 `.github/workflows/ci.yml`、`mobile/vitest.config.ts`、`mobile/package.json`、`frontend/package.json`、`CLAUDE.md`，以及对仓库 `*_test.go` 与 `mobile/src/__tests__` 的 glob 核实。

相关文档：
- 构建与本地开发见 `./build-dev.md`。
- 后端 API 与绑定生成见 `./api-reference.md`。

## 总体形状

Amagi CodeBox 有三条测试/质量线，分属不同子项目：

| 子项目 | 测试/门类型 | 工具 | 是否进 CI |
|---------|------------|------|----------|
| Go 后端（`internal/`、根目录、`cmd/codebox/`） | 单元测试 + 集成测试 + 真实样本测试 | Go 自带 `testing` | **不进 CI**，手动前置 |
| 桌面前端（`frontend/`） | 类型检查门 | `vue-tsc --noEmit`（内嵌于 `build`） | 进 CI（通过 `npm run build`） |
| 移动端前端（`mobile/`） | 单元测试 + 类型检查 | Vitest + `vue-tsc -b` | 类型检查进 CI（通过 `npm run build`）；`vitest run` **不进 CI** |

关键事实：**`.github/workflows/ci.yml` 只运行 `go vet ./...` 加 frontend/mobile 的 `npm run build`，`go test` 不在 CI 流程中**。也就是说 Go 测试是提交前的手动责任，类型检查是 CI 的硬门。

## Go 测试

### 测试文件分布

仓库共 59 个 `*_test.go`（glob 核实，CLAUDE.md 概括为"约 60 个"）。覆盖范围：

- 根目录：`app_test.go`、`app_envcheck_test.go`、`app_codex_config_test.go`、`app_persistence_test.go`、`app_terminal_windows_test.go`、`app_update_provider_test.go`。
- `cmd/codebox/main_test.go`：CLI 工具入口测试。
- `internal/` 下 22 个服务包，测试热点集中在 `internal/envcheck/`（13 个测试文件，含 checker、installer、selfheal、integration 等多种类型）、`internal/codexplugin/`（5 个）、`internal/remote/`（3 个）、`internal/session/`、`internal/proxy/`、`internal/pty/`、`internal/headroom/`、`internal/platform/` 等。

### 运行命令

```bash
# 全量（提交前手动跑）
go test ./...

# 单包
go test ./internal/config

# 单包单测
go test ./internal/config -run TestServiceName

# 竞态检测（推荐用于 session、pty、remote、proxy 等并发包）
go test -race ./internal/session

# 详细输出
go test ./internal/envcheck -v

# CI 实际执行的静态检查（与测试不同，但是唯一的 Go 质量门）
go vet ./...
```

CLAUDE.md 特别提示：仓库根的 `envcheck.test` 是一个陈旧的、被误提交的测试二进制，**不是源文件**，请忽略。

### Go 测试模式（重要）

仓库存在多种测试组织方式，新增测试前先确认是否命中既有模式：

#### 1. 普通单元测试

最常见形式。例如 `internal/config/service_test.go`、`internal/secrets/service_test.go`、`internal/proxy/usage_test.go`。直接 `go test ./internal/<pkg>` 即可运行。

#### 2. 集成测试（环境门控）

通过环境变量开启，默认 `t.Skip()`。代表：`internal/codexplugin/install_integration_test.go`。

```go
func TestActualInstallPluginResolvesCodexFromLocalNodeBinWithGUIPATH(t *testing.T) {
    if os.Getenv("AMAGI_CODEBOX_ACTUAL_CODEX_INSTALL_TEST") != "1" {
        t.Skip("set AMAGI_CODEBOX_ACTUAL_CODEX_INSTALL_TEST=1 to run actual Codex install validation")
    }
    if runtime.GOOS != "darwin" {
        t.Skip("actual GUI PATH validation is only defined for darwin")
    }
    // ...
}
```

跑法：

```bash
AMAGI_CODEBOX_ACTUAL_CODEX_INSTALL_TEST=1 go test ./internal/codexplugin -run TestActualInstallPluginResolvesCodexFromLocalNodeBinWithGUIPATH -v
```

适用场景：依赖真实本机环境（真实的 codex 可执行文件、真实的 `~/.codex/config.toml`）的端到端校验，不适合在 CI 或其他开发者机器上无条件运行。

#### 3. Build-tag 真实样本测试

用 Go build constraints 隔离，默认不编译入测试二进制。代表：`internal/session/tracker_realfixture_test.go`。

```go
//go:build realfixture

package session

func TestRealFixture_MasterJSONL(t *testing.T) {
    // 读 ~/.claude/projects/X--WorkSpace/...jsonl 验证 truncateFirstLine 的真实样本效果
    // 若 fixture 不存在则 t.Skipf
}
```

跑法（必须显式 `-tags realfixture`）：

```bash
go test -tags realfixture ./internal/session/... -run TestRealFixture_MasterJSONL -v
```

适用场景：用真实用户数据（主上机器上的 jsonl）验证修复效果，普通环境下无该 fixture 会自动 skip。

#### 4. 平台特定测试

通过文件名后缀隔离，例如：

- `internal/pty/service_darwin_test.go`：仅 macOS 编译。
- `app_terminal_windows_test.go`、`internal/envvars/platform_windows_test.go`：仅 Windows 编译。
- `internal/platform/capabilities_runtime_test.go`：跨平台能力。

在非目标平台上这些测试自动不参与编译，不需要手工跳过。

### 新增 Go 测试的建议

- 优先放在被测包内（`internal/<pkg>/<file>_test.go`），用同一 package 做"白盒"测试。
- 跨包的端到端场景才放根目录（`app_*_test.go`）或 `cmd/codebox/`。
- 涉及并发（session、pty、proxy、remote、envcheck 异步操作）默认加 `-race` 复跑一次。
- 避免误提交测试二进制（参考 `envcheck.test` 的前车之鉴）；不要把 `go test -c` 产物纳入 git。
- 新增依赖真实环境的测试时，**必须**用环境变量或 build tag 默认跳过，并在文件头注释里写明跑法。

## 桌面前端测试与类型门

`frontend/package.json` 只有：

```jsonc
{
  "scripts": {
    "dev": "vite",
    "build": "vue-tsc --noEmit && vite build",
    "preview": "vite preview"
  }
}
```

事实：
- **前端没有单元测试框架**（无 vitest/jest 依赖、无 `*.test.ts` 文件，glob 核实）。
- 唯一的静态质量门是 `vue-tsc --noEmit`，内嵌于 `npm run build`，类型错误会阻塞构建。
- 该门在 CI 中通过 `npm run build`（`.github/workflows/ci.yml`）执行。

跑法：

```bash
npm --prefix frontend run build     # 类型检查 + 生产构建
# 只想做类型检查（不产出 dist）：
npx --prefix frontend vue-tsc --noEmit
```

如需新增前端单元测试，需先评估引入 vitest 配置与依赖，不在本仓库当前约定范围内（待核实：是否计划引入）。

## 移动端测试

`mobile/package.json` 配置了 vitest：

```jsonc
{
  "scripts": {
    "test": "vitest run",
    "test:watch": "vitest",
    "build": "vue-tsc -b && vite build"
  }
}
```

`mobile/vitest.config.ts`：

```ts
export default mergeConfig(viteConfig, defineConfig({
  test: {
    globals: true,
    environment: 'jsdom',
    include: ['src/**/*.{test,spec}.ts'],
  },
}))
```

测试文件位于 `mobile/src/__tests__/`（glob 核实约 40 个业务测试，覆盖 parser、composables、components、views、utils、types、api 等），匹配模式 `src/**/*.{test,spec}.ts`。

跑法：

```bash
npm --prefix mobile run test         # 单次运行（vitest run）
npm --prefix mobile run test:watch   # watch 模式（vitest）
```

事实：
- `vitest run` 不在 CI 中执行（`.github/workflows/ci.yml` 只跑 `npm run build`）。
- 移动端 `build` 脚本里的 `vue-tsc -b` 是 project references 增量类型检查，这是移动端的 CI 硬门。
- 移动端的 jsdom 环境与 globals API 已开启，测试文件可直接用 `describe/it/expect` 无需显式 import。

## CI 实际执行的内容

`.github/workflows/ci.yml`（runs-on: `windows-latest`）的步骤：

1. Checkout。
2. Setup Go 1.25（带 cache）。
3. Setup Node.js 20（带 npm cache，依赖 `frontend/package-lock.json` 与 `mobile/package-lock.json`）。
4. `npm ci`（frontend）→ `npm run build`（frontend）。
5. `npm ci`（mobile）→ `npm run build`（mobile）。
6. `go vet ./...`。

不在 CI 中、需手动跑的：`go test ./...`、`go test -race ./...`、`npm --prefix mobile run test`。

## 提交前的最小自检清单

改 Go 代码：
- `go vet ./...`（CI 会跑）。
- `go test ./...`（CI 不跑，但请手动跑）。
- 涉及并发包额外 `go test -race ./internal/<并发包>`。

改桌面前端：
- `npm --prefix frontend run build`（含 `vue-tsc --noEmit`，CI 会跑）。

改移动端：
- `npm --prefix mobile run build`（含 `vue-tsc -b`，CI 会跑）。
- `npm --prefix mobile run test`（CI 不跑，但有测试就手动跑）。

改平台特定代码：
- 在目标 OS 上验证（macOS 测 `_darwin` 文件，Windows 测 `_windows` 文件），CI 仅在 `windows-latest` 上运行，macOS 专属路径需要本地复测。

## 待核实项

- 前端目前无单元测试体系；是否计划引入 vitest（待核实，当前 `frontend/package.json` 无相关依赖）。
- CI 仅 Windows runner；macOS 专属构建与测试（如 `_darwin` build tag 文件）在 CI 中无覆盖，发布前请在 macOS 上手动 `wails build` 与 `go test` 验证。
- 根目录 `envcheck.test` 二进制是否可以从仓库删除（待主上确认；CLAUDE.md 仅要求"忽略"，未授权删除）。
