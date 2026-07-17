# 构建与本地开发

面向需要在本地编译、调试或发布 Amagi CodeBox 的开发者。内容基于仓库现有的 `build.sh`、`build.bat`、`package.json`、`wails.json` 与 `CLAUDE.md` 的"Common commands"段核实。命令与路径保持英文，说明用中文。

相关文档：
- 测试约定见 `./testing.md`。
- 后端 API 与绑定生成机制见 `./api-reference.md`。
- 完整方法清单见 `../api.md`。

## 前置依赖

| 依赖 | 版本 | 用途 | 安装/说明 |
|------|------|------|-----------|
| Go | >= 1.25.0 | 后端编译、Wails CLI 安装 | https://go.dev/dl/ |
| Node.js | >= 18 | 桌面前端与移动端前端构建、npm 包管理 | https://nodejs.org/ |
| Wails CLI | v2 | 一站式 dev/build，绑定生成 | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |
| Git | 任意 | 构建脚本读取 `git describe --tags` 注入版本 | 可选；缺失时回退到 `wails.json` 的 `info.productVersion` |

补充说明：
- 仓库 `vendor/` 目录已提交，构建按 `-mod=vendor` 语义进行；新增 Go 依赖需 `go get` 后 `go mod vendor`。
- CI（`.github/workflows/ci.yml`）固定 `go-version: '1.25'` 与 `node-version: '20'`，本地建议对齐。
- 目标平台为 Windows 10 1903+ 与 macOS 10.15+。跨平台差异通过 Go build constraints 处理，**不要**在运行时用 `if runtime.GOOS` 分支；改平台行为时编辑对应的 `_<os>.go` 文件。

## 三条产物线

Amagi CodeBox 一次完整构建会产出三套前端资源：

1. 桌面前端（`frontend/`，Vue 3 + Vite + TypeScript），由 Wails 嵌入主二进制（`//go:embed all:frontend/dist`）。
2. 移动端前端（`mobile/`，Vue 3 + Capacitor），由主二进制单独嵌入（`//go:embed all:mobile/dist`），用于远程控制配套 App。
3. 桌面主二进制（`build/bin/amagi-codebox[.exe]`），Go 后端加两套嵌入资源打包而成。

`mobile/dist` 必须先于 `wails build` 生成，否则 Go 嵌入会因目录缺失而失败。这一点通过 `wails.json` 的 `preBuildHooks` 自动处理（见下文"Wails 配置真相源"）。

## 开发模式（热重载）

```bash
wails dev
```

行为：
- 启动 Go 后端，自动运行 `frontend/` 的 dev server（Wails 通过 `wails.json` 的 `frontend:dev:watcher` 指定 `npm run dev`）。
- 前端改动热重载，Go 改动触发重新编译与重启。
- 从绑定的 Go 方法重新生成 `frontend/wailsjs/go/...`（`wails dev` 与 `wails build` 都会生成）。

仅启动桌面前端 dev server（不走 Wails，无 Go 绑定可用）：

```bash
npm --prefix frontend run dev
```

Wails dev 模式下前端资源的服务地址由 `wails.json` 的 `frontend:dev:serverUrl: "auto"` 决定，Wails 会自动探测 Vite dev server 的端口。

## 生产构建

### 单条命令（推荐）

```bash
wails build
```

产物：`build/bin/amagi-codebox`（macOS）或 `build/bin/amagi-codebox.exe`（Windows）。

`wails build` 内部会：
1. 执行 `wails.json` 中 `preBuildHooks` 配置的钩子（先构建移动端）。
2. 执行 `frontend:build`（即 `npm run build` = `vue-tsc --noEmit && vite build`）。
3. 重新生成 `frontend/wailsjs/`。
4. 编译 Go，把 `frontend/dist` 与 `mobile/dist` 嵌入二进制。

### 一键构建脚本

| 平台 | 脚本 | 额外步骤 |
|------|------|----------|
| macOS / Linux | `./build.sh` | 仅构建并把产物留在 `build/bin/` |
| Windows | `build.bat` | 构建后额外把 `build\bin\amagi-codebox.exe` 复制到项目根目录与 `%USERPROFILE%\.amagi-codebox\` |

`build.sh` 的三步：
1. `[1/3]` 进入 `frontend/` 执行 `npm install && npm run build`。
2. `[2/3]` 若根 `package.json` 存在 `build:mobile` 脚本，执行 `npm run build:mobile`。
3. `[3/3]` 解析版本号，调用 `wails build -ldflags "..."` 注入版本信息。

`build.bat` 的五步：环境检查 → 移动端构建 → Wails 构建 → 复制到项目根 → 复制到用户目录。Windows 脚本会自动尝试安装缺失的 Wails CLI。

两个脚本对齐的部分：版本号解析顺序一致（见下文"版本注入"），都用 `wails build -ldflags` 注入相同变量集。

## 分别构建前端与移动端

Wails 已在 `wails build` 内部调度前端构建，通常无需手工分别执行；下列命令用于局部调试、CI 复现或绕开 Wails 的场景。

### 桌面前端

```bash
npm --prefix frontend install      # 安装依赖
npm --prefix frontend run dev      # 仅 Vite dev server
npm --prefix frontend run build    # vue-tsc --noEmit && vite build
npm --prefix frontend run preview  # 预览构建产物
```

关键点：`npm run build` 内部先跑 `vue-tsc --noEmit` 做类型检查，**类型错误会阻塞构建**。这是前端唯一的静态质量门（前端无 vitest 单元测试，见 `./testing.md`）。

### 移动端前端

```bash
npm --prefix mobile ci              # 安装依赖（CI 用 ci，本地可用 install）
npm --prefix mobile run build       # vue-tsc -b && vite build
npm --prefix mobile run test        # vitest run（单元测试）
```

移动端 `build` 脚本是 `vue-tsc -b && vite build`（带 project references 的增量类型检查）。

### 根 package.json 的聚合脚本

仓库根 `package.json` 提供聚合脚本，便于从仓库根一次性构建：

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

- `npm run build`（根）：先 `build:mobile`，再构建 `frontend/`。等价于 Wails 内部调度顺序，可用于在 Wails 之外预先生成两套 dist。
- `npm run build:mobile`（根）：移动端依赖安装 + 构建。

## Wails 配置真相源

`wails.json` 决定 dev/build 的行为，关键字段：

```jsonc
{
  "frontenddir": "frontend",
  "frontend:install": "npm install",
  "frontend:build": "npm run build",
  "frontend:dev:watcher": "npm run dev",
  "frontend:dev:serverUrl": "auto",
  "preBuildHooks": {
    "*/*": "npm --prefix ../.. run build:mobile"
  },
  "info": {
    "productName": "Amagi CodeBox",
    "productVersion": "1.2.80"
  }
}
```

- `frontend:dev:serverUrl: "auto"`：dev 模式自动探测 Vite 端口。
- `preBuildHooks`：`wails build` 前在 `frontend/` 目录下执行 `npm --prefix ../.. run build:mobile`，确保 `mobile/dist` 先生成。**这是移动端构建先于桌面构建的事实保障**；手工绕过 `wails build` 时需自行保证顺序。
- `info.productVersion`：版本号的最终回退源（无 git tag 时使用）。

## 版本注入

`main.go` 定义四个包级变量（默认值 `dev` / `unknown`）：

```go
var (
    Version   = "dev"
    BuildTime = "unknown"
    GitCommit = "unknown"
    GoVersion = "unknown"
)
```

构建脚本通过 `-ldflags "-X main.Version=... ..."` 在链接期注入。版本号解析顺序（两个脚本一致）：

1. `git describe --tags --abbrev=0`（最近的 tag）。
2. 若上一步为空，回退到 `wails.json` 的 `info.productVersion`（通过 python3 或 powershell 解析）。
3. 最终回退字符串 `dev`。

`GitCommit` 来自 `git rev-parse --short HEAD`，`BuildTime` 为 UTC ISO 时间，`GoVersion` 来自 `go version` 或 `go env GOVERSION`。

升级版本号时需同步修改：`wails.json` 的 `info.productVersion`、根 `package.json` 的 `version`、`frontend/package.json` 的 `version`（当前均为 `1.2.80`，核实自 `wails.json`、根 `package.json`、`frontend/package.json`）。

## 常见问题与排查

- **`mobile/dist` 不存在导致 `wails build` 失败**：确认 `preBuildHooks` 生效；若手工分步执行，先 `npm run build:mobile`。
- **前端构建报类型错误**：`vue-tsc --noEmit` 是硬门，修复类型后再继续；可单独 `npm --prefix frontend run build` 复现。
- **`wails: command not found`**：执行 `go install github.com/wailsapp/wails/v2/cmd/wails@latest`，并确认 `$GOPATH/bin`（或 `%USERPROFILE%\go\bin`）在 `PATH` 中。Windows 的 `build.bat` 会尝试自动安装。
- **`vendor/` 与新依赖**：新增 Go 依赖必须 `go get` + `go mod vendor`，否则 `-mod=vendor` 构建找不到包。
- **手工修改 `frontend/wailsjs/`**：禁止手改，该目录由 Wails 自动生成；改后端方法签名后用 `wails dev` 或 `wails build` 重新生成（详见 `./api-reference.md`）。

## 待核实项

- `build.bat` 末尾会把产物复制到 `%USERPROFILE%\.amagi-codebox\`；macOS / Linux 的 `build.sh` 无对应复制步骤。若 macOS 需要类似的用户目录部署，需主上确认是否期望补齐（目前脚本未实现）。
- README 列出的最低 macOS 版本为 10.15+，Wails v2.11.0 的实际最低要求（待核实），如遇构建报缺系统 API，请以 Wails 官方文档为准。
