# 版本号管理

面向需要升级 Amagi CodeBox 版本号或排查「为什么关于对话框显示 dev」的开发者与发布者。内容基于 `main.go`、`app.go`（`GetAppInfo`/`resolveAppVersion`/`readWailsProductVersion`，约 4777 行起）、`build.sh`、`build.bat`、`wails.json`、三处 `package.json` 与 `.github/workflows/release.yml` 核实。

相关文档：
- 打包与发布流程见 `./release.md`。
- CI/CD 流水线见 `./ci-cd.md`。
- 本地构建细节见 `../developer/build-dev.md`。

## 版本变量定义

`main.go`（约 20 行起）定义四个包级变量：

```go
// 版本信息：默认 dev/unknown，由构建脚本通过 -ldflags "-X main.Version=..." 注入。
var (
    Version   = "dev"
    BuildTime = "unknown"
    GitCommit = "unknown"
    GoVersion = "unknown"
)
```

四个变量的角色：
- `Version`：用户可见的版本号，如 `1.3.50` 或 `v1.3.50`。来源链见下文。
- `BuildTime`：构建时刻，UTC ISO 8601（如 `2026-08-01T08:30:00Z`）。
- `GitCommit`：构建对应的 `git rev-parse --short HEAD` 短哈希。
- `GoVersion`：构建用的 Go 工具链版本（`go version` 第三字段或 `go env GOVERSION`，形如 `go1.25.0`）。

未注入时（`go run`、未带 ldflags 的 `go build`），四者保持默认。`GetAppInfo` 会在运行时为 `Version` 与 `GoVersion` 做回退（见下文「运行时回退」）。

## 版本来源链（构建期）

本地构建脚本 `build.sh`（Unix）与 `build.bat`（Windows）对 `main.Version` 的解析顺序一致：

1. **`git describe --tags --abbrev=0`**：取最近的 git tag（仅 tag 名，不含后续 commits 数）。例如最近 tag 是 `v1.3.50`，则 `Version` 被注入为 `v1.3.50`（带 `v` 前缀）。
2. **`wails.json` 的 `info.productVersion`**：上一步为空（仓库无 tag 或 `git` 不可用）时回退。`build.sh` 用 `python3` 解析，`build.bat` 用 `powershell ConvertFrom-Json`。当前值为 `1.3.50`（无 `v` 前缀）。
3. **字符串 `dev`**：上述两步均失败时的最终回退。

其余三个变量的来源：
- `GitCommit`：`git rev-parse --short HEAD`，失败回退 `unknown`。
- `BuildTime`：UTC ISO 时间。`build.sh` 用 `date -u +%Y-%m-%dT%H:%M:%SZ`；`build.bat` 用 `powershell Get-Date -Format yyyy-MM-ddTHH:mm:ssZ`。
- `GoVersion`：`build.sh` 用 `go version | awk '{print $3}'`（只取 `go1.x.y` 字段——`go version` 完整输出含空格，直接注入 `-X` 会被 linker 拆词导致构建失败）；`build.bat` 用 `go env GOVERSION`。失败均回退 `unknown`。

注入方式：`wails build -ldflags "-X main.Version=... -X main.GitCommit=... -X main.BuildTime=... -X main.GoVersion=..."`。

Release workflow 走同一套注入范围（见下文「Release 构建」）。

## 运行时回退（GetAppInfo）

`app.go` 的 `GetAppInfo()` 在运行时拼装版本信息返回给前端（关于对话框等）。版本号与 Go 版本都有运行时回退，确保即便 ldflags 未注入也能给出合理值。

### resolveAppVersion 逻辑（app.go，约 4777 行）

优先级：
1. ldflags 注入的 `main.Version`（去除 `v` 前缀），且非空、非 `dev`。
2. `wails.json` 的 `info.productVersion`（去除 `v` 前缀），由 `readWailsProductVersion` 读取。
3. 字符串 `dev`。

注意「注入但值是 `dev`」也会触发回退：仅靠 `main.Version = "dev"` 的默认值无法区分「未注入」与「显式注入 dev」，所以一律回退到 `wails.json`。

### readWailsProductVersion 查找路径（app.go，约 4791 行）

依次查找 `wails.json` 的位置：
1. 可执行文件所在目录（`filepath.Dir(os.Executable())`）。
2. 当前工作目录（`os.Getwd()`）。

任一位置读到且 `info.productVersion` 非空即返回。开发模式下 `wails.json` 位于源码根目录，运行时 cwd 通常落在那里；安装模式下 `wails.json` 不一定随二进制分发，回退将失败、最终显示 `dev`。

### GoVersion 的运行时回退

`GetAppInfo` 中：若 `GoVersion` 为空或 `unknown`，用 `runtime.Version()`（权威编译器版本）。其余字段（`BuildTime`/`GitCommit`）不做回退，未注入即显示 `unknown`。

## Release 构建的注入

`.github/workflows/release.yml` 的两个 job（`build-windows` / `build-macos-arm64`）在构建前：

1. **Get version**：从 `GITHUB_REF` 剥出 tag 名（含 `v` 前缀，如 `v1.3.50`）。
2. **Sync wails.json version**：python3 内联脚本把 tag 去 `v` 前缀写入 `wails.json` 的 `info.productVersion`——仅 CI 工作副本生效，**不回写仓库**。
3. **Compute build metadata**：采集 `git rev-parse --short HEAD`、UTC 时间、`go version` 第三字段。
4. **Build**：`wails build -ldflags "-X main.Version=<v> -X main.GitCommit=<c> -X main.BuildTime=<t> -X main.GoVersion=<g>"`（Windows 额外 `-s`，macOS 额外 `-clean -platform darwin/arm64`）。

即 **Release 产物与本地脚本一样注入全部 4 个变量**：从 GitHub Release 下载的二进制，关于对话框会显示完整版本、commit、构建时间与 Go 版本。

## 同步多处的版本号

仓库中「硬编码」版本号的位置（核实自当前各文件）：

| 文件 | 字段 | 当前值 | 是否与桌面同步 |
|------|------|--------|----------------|
| `wails.json` | `info.productVersion` | `1.3.50` | 是（桌面版本真相源） |
| `package.json`（根） | `version` | `1.3.50` | 是 |
| `frontend/package.json` | `version` | `1.3.50` | 是 |
| `mobile/package.json` | `version` | `1.0.5` | **否**，移动端独立演进 |
| `README.md` | version 徽章 URL | `1.3.50` | 手工同步，无强制 |

约定：
- **桌面版本号真相源是 `wails.json` 的 `info.productVersion`**（也是无 git tag 时构建脚本的回退源、`GetAppInfo` 运行时的回退源）。
- 根 `package.json` 与 `frontend/package.json` 的 `version` 与桌面同步，三处保持一致。
- `mobile/package.json` 的 `version` 是移动端配套 App 自身的版本号（当前 `1.0.5`），与桌面版本号解耦，按移动端节奏单独演进。

Release workflow 的 python3 脚本只改写 CI 临时工作副本中的 `wails.json`；**根与 `frontend/package.json` 的 `version` 不会被 workflow 自动改写**，需在打 tag 前手工同步。

## 升级版本号操作清单

以从 `1.3.50` 升级到 `1.3.51` 为例：

1. 修改以下文件（桌面三处保持一致）：
   - `wails.json` → `"productVersion": "1.3.51"`
   - `package.json`（根） → `"version": "1.3.51"`
   - `frontend/package.json` → `"version": "1.3.51"`
2. 若移动端配套也要发版，单独评估 `mobile/package.json` 的 `version`（`1.0.5` → `1.0.6` 或其他），不强制与桌面同步。
3. 可选：更新 `README.md` 顶部 version 徽章（硬编码，需手工同步；待核实是否改由 workflow 自动更新）。
4. 归档 [CHANGELOG.md](../../CHANGELOG.md) 的 `[Unreleased]` 段。
5. 本地预校验：
   ```bash
   go vet ./...
   go test ./... -count=1
   npm --prefix frontend run build
   npm --prefix mobile run build && npm --prefix mobile run test
   ./build.sh   # 或 build.bat
   ```
   启动构建产物，确认关于对话框显示 `1.3.51`（而不是 `dev` 或 `1.3.50`）。
6. 提交、打 tag、推送（详见 `./release.md`「发布步骤建议」）：
   ```bash
   git add wails.json package.json frontend/package.json CHANGELOG.md
   git commit -m "chore: bump version to 1.3.51"
   git tag v1.3.51
   git push origin master
   git push origin v1.3.51
   ```
   注意：tag 用 `v` 前缀（`v1.3.51`），`wails.json` 中不带 `v` 前缀（`1.3.51`）。`resolveAppVersion` 与 workflow 的 python3 脚本都会去 `v` 前缀，不会出现双 `v`。

## 排查：为什么显示 dev 或旧版本

- **关于对话框显示 `dev`**：`main.Version` 未被注入且 `readWailsProductVersion` 找不到 `wails.json`。检查：
  - 是否走了 `wails build` 而非带 `-ldflags` 的脚本？
  - 二进制所在目录或 cwd 是否有 `wails.json`？
  - `wails.json` 的 `info.productVersion` 是否非空？
- **关于对话框显示 `unknown` 的 commit/buildTime**：仅当构建未注入对应变量时发生（`go build` 直出、老版本 Release 构建）。当前 `build.sh`/`build.bat` 与 `release.yml` 均注入全部 4 个变量，正常路径不应出现。
- **关于对话框显示旧版本号**：`wails.json` 的 `productVersion` 没改、或 git tag 未更新。`resolveAppVersion` 优先用注入值，注入失败时才看 `wails.json`。
- **Release 资产中版本号与 tag 不一致**：workflow 在构建前会用 python3 改写 `wails.json`，但仅在 CI runner 的临时工作副本中生效，不会回写仓库。本地 `wails.json` 与最近 tag 不一致属于正常现象（本地副本需手工 commit）。

## 待核实项

- README 顶部 version 徽章硬编码（当前 `1.3.50`），发版时需手工同步；是否改由 workflow 在发版时自动更新待确认。
- `mobile/package.json` `version` 与桌面版本的同步策略目前是「解耦」；若未来期望统一为单版本号，需引入脚本或 workflow 协调，待确认。
