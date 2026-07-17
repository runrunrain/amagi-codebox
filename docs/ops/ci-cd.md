# CI/CD 流程

面向需要读懂或修改 Amagi CodeBox 持续集成与发布流水线的维护者。内容基于 `.github/workflows/ci.yml` 与 `.github/workflows/release.yml` 完整读取核实；所有 step 名称、命令、runner、版本号均与 workflow 原文一致。

相关文档：
- 打包与发布（含 Release workflow 的产物形态与发布步骤）见 `./release.md`。
- 版本号管理与 ldflags 注入细节见 `./versioning.md`。
- 测试约定（CI 不跑 `go test` 的背景）见 `../developer/testing.md`。
- 本地构建见 `../developer/build-dev.md`。

## 概览

仓库有两条独立流水线，均在 GitHub Actions 上运行：

| 流水线 | workflow 文件 | 触发 | 目的 |
|--------|--------------|------|------|
| CI | `.github/workflows/ci.yml` | `push` 到 `master`、`pull_request` 到 `master` | 静态质量门（go vet + 前端/移动端构建） |
| Release | `.github/workflows/release.yml` | `push` 形如 `v*` 的 tag | 构建 Windows 与 macOS arm64 产物并上传到 GitHub Release |

两者**互不依赖**：CI 不会阻塞 Release，Release 也不要求 CI 先通过。提交者需自行保证 CI 在 master 上常绿，再考虑打 tag 发版。

## CI 流水线（ci.yml）

### 触发条件

```yaml
on:
  push:
    branches: [master]
  pull_request:
    branches: [master]

permissions:
  contents: read
```

仅 `master` 分支的 push 与 PR 触发。权限收紧为 `contents: read`。

### runner 与 job

只有一个 job：

```yaml
jobs:
  build:
    runs-on: windows-latest
```

**CI 固定在 `windows-latest` 上运行**：macOS 与 Linux 路径在本仓库（如 `_darwin.go`、`_windows.go` build tag 文件）在 CI 中无 macOS runner 覆盖。macOS 专属代码需在本地 macOS 上手动验证（详见 `../developer/testing.md` "CI 实际执行的内容"）。

### 步骤详解

| # | 步骤 | 命令 / action | working-directory |
|---|------|---------------|-------------------|
| 1 | Checkout code | `actions/checkout@v4` | 仓库根 |
| 2 | Setup Go | `actions/setup-go@v5`，`go-version: '1.25'`，`cache: true` | — |
| 3 | Setup Node.js | `actions/setup-node@v4`，`node-version: '20'`，`cache: 'npm'`，`cache-dependency-path` 含 `frontend/package-lock.json` 与 `mobile/package-lock.json` | — |
| 4 | Install frontend dependencies | `npm ci` | `frontend` |
| 5 | Build frontend | `npm run build`（= `vue-tsc --noEmit && vite build`） | `frontend` |
| 6 | Install mobile dependencies | `npm ci` | `mobile` |
| 7 | Build mobile | `npm run build`（= `vue-tsc -b && vite build`） | `mobile` |
| 8 | Go vet | `go vet ./...` | 仓库根 |

### CI 的硬门与不跑的内容

**CI 真正执行的质量门**：
- 桌面前端的 `vue-tsc --noEmit` 类型检查（通过 `npm run build` 内嵌）。
- 移动端的 `vue-tsc -b` 类型检查（通过 `npm run build` 内嵌）。
- `go vet ./...` 静态检查。

**CI 不跑的内容**（核实自 workflow 与 `../developer/testing.md`）：
- **`go test ./...` 完全不在 CI 中**。Go 单元测试、集成测试、真实样本测试都是提交前的手动责任。
- **`go test -race`** 不在 CI 中（并发包如 `session`/`pty`/`proxy`/`remote` 的竞态检测需本地跑）。
- **移动端 `vitest run` 不在 CI 中**（`mobile/package.json` 配置了 vitest，但 CI 只跑 `build`）。
- **`wails build` 不在 CI 中**。CI 只做静态检查与前端/移动端构建，**不产出可运行二进制**。要发布二进制需打 tag 触发 Release workflow。
- **平台覆盖不全**：CI 仅 Windows runner，`_darwin.go` 文件不参与 Windows 编译，macOS 路径在 CI 中无验证。

修改 Go 或前端代码时，提交前请按 `../developer/testing.md` "提交前的最小自检清单" 跑手动测试。

## Release 流水线（release.yml）

### 触发条件

```yaml
on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write
```

任何 `v` 开头的 tag 推送即触发（如 `v1.2.80`、`v2.0.0-rc1`）。权限放开为 `contents: write`，以允许 workflow 创建 Release 与上传资产。

### 两个并行的构建 job

Release workflow 有两个 job，**无 `needs` 依赖，并行执行**：

| job | runner | 产物 | 资产命名 |
|-----|--------|------|---------|
| `build-windows` | `windows-latest` | `build/bin/amagi-codebox.exe` 打包为 zip | `amagi-codebox-<tag>-windows-amd64.zip` |
| `build-macos-arm64` | `macos-latest` | `build/bin/amagi-codebox.app` 打包为 zip | `amagi-codebox-<tag>-darwin-arm64.zip` |

两个 job 共享前 10 步（Checkout → Setup Go 1.25 → Setup Node 20 → Install Wails → `npm ci`+`npm run build` for frontend → `npm ci`+`npm run build` for mobile → Get version → Sync wails.json version）。差异仅在构建步骤与打包步骤。

### build-windows job 关键步骤

前 6 步（与 CI 对称，额外多一步 Install Wails）：
1. `actions/checkout@v4`。
2. `actions/setup-go@v5`，`go-version: '1.25'`，`cache: true`。
3. `actions/setup-node@v4`，`node-version: '20'`，npm cache。
4. **Install Wails**：`go install github.com/wailsapp/wails/v2/cmd/wails@latest`（CI 无此步）。
5. frontend：`npm ci` → `npm run build`。
6. mobile：`npm ci` → `npm run build`。

后续步骤：
- **Get version**：
  ```bash
  echo "VERSION=${GITHUB_REF#refs/tags/}" >> $GITHUB_OUTPUT
  ```
  从 `GITHUB_REF`（如 `refs/tags/v1.2.80`）剥出 `v1.2.80`（**含 `v` 前缀**）。
- **Sync wails.json version**（`shell: bash`，python3 内联）：把 tag 去掉 `v` 前缀写入 `wails.json` 的 `info.productVersion`。仅在 CI 工作副本生效，不回写仓库。
- **Build**：
  ```bash
  wails build -s -ldflags "-X main.Version=${{ steps.get_version.outputs.VERSION }}"
  ```
  `-s` 静默。**只注入 `main.Version`**，不注入 `GitCommit`/`BuildTime`/`GoVersion`（详见 `./versioning.md` "ldflags 注入的两种形态"）。
- **Create ZIP archive**（`shell: cmd`）：
  ```bat
  cd build/bin
  7z a -tzip ../../amagi-codebox-<VERSION>-windows-amd64.zip amagi-codebox.exe
  ```
- **Upload Release Asset**：`softprops/action-gh-release@v2`，`generate_release_notes: true`（GitHub 根据 commits 自动生成发行说明）。

### build-macos-arm64 job 关键步骤

前 6 步与 `build-windows` 完全对称（仅 runner 不同）。差异部分：
- **Build macOS arm64 bundle**：
  ```bash
  wails build -clean -platform darwin/arm64 -ldflags "-X main.Version=${{ steps.get_version.outputs.VERSION }}"
  ```
  `-clean` 清理缓存；`-platform darwin/arm64` 显式指定 Apple Silicon 目标。
- **Prepare macOS arm64 artifact**：
  ```bash
  mkdir -p release-assets
  ditto -c -k --sequesterRsrc --keepParent build/bin/amagi-codebox.app release-assets/amagi-codebox-<VERSION>-darwin-arm64.zip
  ```
  `ditto` 是 macOS 专用打包工具，保留 `.app` bundle 的资源 fork 与权限。
- **Codesign placeholder** / **Notarization placeholder**：均以 `if: ${{ false }}` 禁用，仅占位（待核实：何时接入 Developer ID 签名与公证）。
- **Upload Release Asset**：`softprops/action-gh-release@v2`，上传 macOS zip。

### 已知限制

- Release workflow **不产出 README 中提到的 MSI/EXE 安装包或 DMG 镜像**，仅产出两个 zip（详见 `./release.md` "产物形态"）。
- macOS arm64 产物**未代码签名、未公证**，用户首次打开会被 Gatekeeper 拦截。
- **不覆盖 macOS Intel（amd64）**：Intel Mac 用户需自行从源码构建。

## 重要差异：CI vs 本地脚本

修改 workflow 或脚本时需注意的偏差：

| 维度 | `ci.yml` | `release.yml` | `build.sh`/`build.bat`（本地） |
|------|----------|---------------|-------------------------------|
| 触发 | push/PR to master | push tag `v*` | 手动 |
| runner | `windows-latest` | `windows-latest` + `macos-latest` | 本机 |
| `wails build` | 不跑 | 跑（仅注入 `main.Version`） | 跑（注入 4 个变量） |
| `go vet` | 跑 | 不跑 | 不跑 |
| `go test` | 不跑 | 不跑 | 不跑（需手动） |
| 移动端构建 | `npm ci` + `npm run build` | 同 CI | `build.sh`：根 `npm run build:mobile`；`build.bat`：`mobile/` 下 `npm ci --prefer-offline` + `npm run build` |
| 桌面前端构建 | `npm ci` + `npm run build`（working-directory `frontend`） | 同 CI | `build.sh`：`frontend/` 下 `npm install` + `npm run build`；`build.bat`：依赖 `wails build` 内部 |
| 版本号来源 | 不涉及 | `${GITHUB_REF#refs/tags/}`（tag，含 `v`） | `git describe --tags --abbrev=0` → `wails.json` productVersion → `dev` |
| `wails.json` 改写 | 不涉及 | python3 同步 `info.productVersion`（去 `v` 前缀） | 不改写 |

关键观察：
- **CI 与 Release 的 npm 依赖安装用 `npm ci`**（要求 lockfile 与 package.json 一致），本地 `build.sh` 用 `npm install`（更宽松）。lockfile 漂移会在 CI 上失败而本地通过。
- **`wails build` 在 CI 中不跑**，意味着 PR 即便让 `wails build` 失败（如 `preBuildHooks` 失效导致 `mobile/dist` 缺失），CI 也不会发现。这类问题只能在 Release workflow 或本地构建时暴露。
- **Release workflow 的 `wails.json` 同步不回写仓库**：tag 推送后，仓库中的 `wails.json` 保持原值；CI 仅在临时工作副本中改写。这是设计预期，不是 bug。

## 修改建议

新增或调整 CI/CD 步骤时的注意事项：
- **加 `go test`**：若计划让 CI 跑 Go 测试，需考虑 Windows-only runner 会跳过 `_darwin`/`_linux` 测试文件；并发包建议额外加 `-race`（但 Windows 上 `-race` 需要 gcc，`windows-latest` 默认带 mingw，需验证）。
- **加 macOS runner 到 CI**：若要覆盖 `_darwin.go` 路径，可在 `ci.yml` 加 matrix（`runs-on: [windows-latest, macos-latest]`），但需评估构建时长与配额成本。
- **补 `darwin/amd64` 到 Release**：复制 `build-macos-arm64` job 并改 `-platform darwin/amd64`、资产命名即可；需在 Intel Mac 或 x86 runner 上验证（待核实：macos-latest runner 是否仍支持交叉编译 amd64）。
- **启用 macOS 签名公证**：把 `release.yml` 中 `Codesign placeholder` / `Notarization placeholder` 的 `if: ${{ false }}` 改为合适条件，并准备 secrets（`DEVELOPER_ID_APPLICATION`、`AC_PASSWORD` 等）。详见 `./release.md` "前置条件"。

## 待核实项

- Release workflow 仅注入 `main.Version`，是否计划扩展为与本地脚本一致的 4 变量注入（待确认）。
- CI 仅 `windows-latest` runner，macOS 与 `_darwin.go` 路径在 CI 中无覆盖；是否计划加 macOS matrix（待确认）。
- `release.yml` 中两个 macOS 占位步骤（Codesign / Notarization）的启用时间表未公开（待确认）。
- README 列出的 MSI/EXE/DMG 安装包形态与 workflow 实际产出的 zip 不一致；是否计划引入打包工具（待确认）。
