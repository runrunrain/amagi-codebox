# 构建与本地开发

面向需要在本地编译、调试或发布 Amagi CodeBox 的开发者。内容基于 `build.sh`、`build.bat`、`package.json`、`wails.json`、`.github/workflows/ci.yml` 与 `go.mod` 核实（核实日期 2026-08-22，当前版本 1.3.50）。命令与路径保持英文，说明用中文。

相关文档：
- 测试约定见 `./testing.md`。
- 后端 API 与绑定生成机制见 `./api-reference.md`。
- 完整方法清单见 `../api.md`。

## 工具链基线

| 依赖 | 版本 | 说明 |
|------|------|------|
| Go | CI/release 精确 pin `1.25.0`（`ci.yml`/`release.yml` setup-go） | `go.mod` 声明 `go 1.25.0` 为语言基线；无 `toolchain` 指令、无 `.go-version`，本地装更新版本（如 1.26.x）按原样使用，不强制降级 |
| Node.js | CI 精确 pin `20.19.0`（setup-node） | **注意**：仓库根 `.node-version` 当前为 `22.23.2`（fnm/nodenv 消费），与 CI 的 20.19.0 不一致——遇到本地/CI 行为差异时先怀疑这一点（见"待核实项"） |
| Wails CLI | v2 | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |
| Git | 任意 | 构建脚本读取 `git describe --tags` 注入版本；缺失时回退 `wails.json` 的 `info.productVersion` |

补充说明：
- 仓库 `vendor/` 目录已提交，构建按 `-mod=vendor` 语义进行；新增 Go 依赖需 `go get` 后 `go mod vendor`。
- npm 在 manifest 中保持 `>=10`（非 engine-strict）。
- 目标平台为 Windows 10+ 与 macOS。跨平台差异通过 Go build constraints 处理，**不要**在运行时用 `if runtime.GOOS` 分支；改平台行为时编辑对应的 `_<os>.go` 文件（见 `./platform-build-tags.md`）。

## 三条产物线

一次完整构建产出三套资源：

1. 桌面前端（`frontend/`，Vue 3 + Vite + TypeScript），由 Wails 嵌入主二进制（`//go:embed all:frontend/dist`）。
2. 移动端前端（`mobile/`，Vue 3 + Capacitor），由主二进制单独嵌入（`//go:embed all:mobile/dist`），用于远程控制配套 App。
3. 桌面主二进制（`build/bin/amagi-codebox[.exe]`）。

`mobile/dist` 必须先于 `wails build` 生成，否则 Go 嵌入因目录缺失而失败。这由 `wails.json` 的 `preBuildHooks` 自动保证（见下文"Wails 配置真相源"）。

## 开发模式（热重载）

```bash
wails dev
```

- 启动 Go 后端，并运行 `frontend/` 的 dev server（`wails.json` 的 `frontend:dev:watcher` = `npm run dev`）。
- 前端改动热重载，Go 改动触发重新编译与重启。
- 从绑定的 Go 方法重新生成 `frontend/wailsjs/go/...`（`wails dev` 与 `wails build` 都会生成）。

仅启动桌面前端 dev server（无 Go 绑定可用）：

```bash
npm --prefix frontend run dev
```

## 生产构建

### 单条命令（推荐）

```bash
wails build
```

产物：`build/bin/amagi-codebox`（macOS）或 `build/bin/amagi-codebox.exe`（Windows）。

`wails build` 内部依次：执行 `preBuildHooks`（先构建移动端）→ `frontend:build`（`npm run build` = `vue-tsc --noEmit && vite build`）→ 重新生成 `frontend/wailsjs/` → 编译 Go 并嵌入两套 dist。

### 一键构建脚本

| 平台 | 脚本 | 行为 |
|------|------|------|
| macOS / Linux | `./build.sh` | 三步：`[1/3]` `(cd frontend && npm ci && npm run build)` → `[2/3]` `npm run build:mobile`（根 `package.json` 存在该脚本时）→ `[3/3]` 解析版本号并 `wails build -ldflags "..."` |
| Windows | `build.bat` | 环境检查（缺 Wails CLI 时尝试自动安装）→ 移动端构建 → Wails 构建 → 复制 exe 到项目根与 `%USERPROFILE%\.amagi-codebox\` |

两脚本对齐：版本号解析顺序一致，都用 `wails build -ldflags` 注入相同变量集。`build.sh` 前置检查 `wails` 与 `go` 命令存在，缺失即报错退出。

## 分别构建前端与移动端

Wails 已在 `wails build` 内部调度前端构建；下列命令用于局部调试或 CI 复现。

### 桌面前端

```bash
npm --prefix frontend install      # 安装依赖（wails.json 的 frontend:install 用 npm ci）
npm --prefix frontend run dev      # 仅 Vite dev server
npm --prefix frontend run build    # vue-tsc --noEmit && vite build
npm --prefix frontend run preview  # 预览构建产物
```

关键点：`npm run build` 先跑 `vue-tsc --noEmit` 做类型检查，**类型错误会阻塞构建**。这是桌面前端唯一的静态质量门（前端无单元测试，见 `./testing.md`）。

### 移动端前端

```bash
npm --prefix mobile ci              # 安装依赖
npm --prefix mobile run build       # vue-tsc -b && vite build
npm --prefix mobile run test        # vitest run（单元测试，CI 会执行）
```

### 根 package.json 聚合脚本

```jsonc
{
  "scripts": {
    "build:mobile": "npm --prefix mobile ci && npm --prefix mobile run build",
    "build": "npm run build:mobile && npm --prefix frontend run build",
    "dev": "npm --prefix frontend run dev",
    "install-frontend": "npm --prefix frontend install"
  }
}
```

根 `package.json` 另有 devDependencies `@playwright/test` 与 `@axe-core/playwright`（E2E 基建，见 `./testing.md`），但**没有** e2e 聚合脚本——E2E 直接 `npx playwright test` 运行。

## Wails 配置真相源

`wails.json` 当前关键字段：

```jsonc
{
  "frontenddir": "frontend",
  "frontend:install": "npm ci",
  "frontend:build": "npm run build",
  "frontend:dev:watcher": "npm run dev",
  "frontend:dev:serverUrl": "auto",
  "preBuildHooks": {
    "*/*": "npm --prefix ../.. run build:mobile"
  },
  "info": {
    "productName": "Amagi CodeBox",
    "productVersion": "1.3.50"
  }
}
```

- `frontend:dev:serverUrl: "auto"`：dev 模式自动探测 Vite 端口。
- `preBuildHooks`：`wails build` 前在 `frontend/` 目录下执行 `npm --prefix ../.. run build:mobile`，**这是移动端构建先于桌面构建的事实保障**；手工绕过 `wails build` 时需自行保证顺序。
- `info.productVersion`：版本号的最终回退源。

## 版本注入

`main.go` 定义四个包级变量（默认 `dev` / `unknown`）：

```go
var (
    Version   = "dev"
    BuildTime = "unknown"
    GitCommit = "unknown"
    GoVersion = "unknown"
)
```

构建脚本通过 `-ldflags "-X main.Version=... ..."` 链接期注入。版本号解析顺序（`build.sh`/`build.bat` 一致）：

1. `git describe --tags --abbrev=0`（最近的 tag）。
2. 为空则回退 `wails.json` 的 `info.productVersion`（python3 / powershell 解析）。
3. 最终回退字符串 `dev`。

`GitCommit` 来自 `git rev-parse --short HEAD`；`BuildTime` 为 UTC ISO 时间；`GoVersion` 取 `go version` 输出的版本号字段（`go1.x.y`，不含空格——含空格会被 linker 拆词导致构建失败，`build.sh` 用 `awk '{print $3}'` 截取）。

未注入时（`go run` / 无 tag 构建）保持 `dev`，由 `GetAppInfo` 在运行时回退到 `wails.json` 的 `productVersion`。

升级版本号需同步修改：`wails.json` 的 `info.productVersion`、根 `package.json` 的 `version`、`frontend/package.json` 的 `version`（当前三者均为 `1.3.50`）。注意 `mobile/package.json` 版本独立演进（当前 `1.0.5`）。

## 常见问题与排查

- **`mobile/dist` 不存在导致 `wails build` 失败**：确认 `preBuildHooks` 生效；手工分步执行时先 `npm run build:mobile`。
- **前端构建报类型错误**：`vue-tsc --noEmit` 是硬门，修复类型后再继续；可单独 `npm --prefix frontend run build` 复现。
- **`wails: command not found`**：`go install github.com/wailsapp/wails/v2/cmd/wails@latest`，并确认 `$GOPATH/bin`（或 `%USERPROFILE%\go\bin`）在 `PATH` 中。Windows 的 `build.bat` 会尝试自动安装。
- **`vendor/` 与新依赖**：新增 Go 依赖必须 `go get` + `go mod vendor`。
- **手工修改 `frontend/wailsjs/`**：禁止；该目录由 Wails 自动生成，改后端签名后用 `wails dev`/`wails build` 重新生成（见 `./api-reference.md`）。

## 待核实项

- **Node 版本漂移**：CI/release pin `20.19.0`，但根 `.node-version` 当前为 `22.23.2`。二者谁是有意目标未在仓库内声明，已向主上报告；对齐前本地大版本差异引起的构建问题优先怀疑此处。
- `build.bat` 末尾把产物复制到 `%USERPROFILE%\.amagi-codebox\`；`build.sh` 无对应步骤。macOS 是否需要类似的用户目录部署待主上确认。
