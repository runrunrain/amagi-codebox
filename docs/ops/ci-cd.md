# CI/CD 流程

面向需要读懂或修改 Amagi CodeBox 持续集成与发布流水线的维护者。内容基于 `.github/workflows/ci.yml` 与 `.github/workflows/release.yml` 完整读取核实；所有 step 名称、命令、runner、版本号均与 workflow 原文一致。

相关文档：
- 打包与发布（含 Release workflow 的产物形态与发布步骤）见 `./release.md`。
- 版本号管理与 ldflags 注入细节见 `./versioning.md`。
- 测试约定见 `../developer/testing.md`。
- 本地构建见 `../developer/build-dev.md`。

## 概览

仓库有两条独立流水线，均在 GitHub Actions 上运行：

| 流水线 | workflow 文件 | 触发 | 目的 |
|--------|--------------|------|------|
| CI | `.github/workflows/ci.yml` | **仅 `workflow_dispatch`（手动触发）** | 质量门：前端/移动端构建 + 移动端测试 + go vet + golangci-lint + Go 测试 |
| Release | `.github/workflows/release.yml` | `push` 形如 `v*` 的 tag | 构建 Windows 与 macOS arm64 产物并上传到 GitHub Release |

CI 不监听 push/PR——这是刻意设计（workflow 注释 C-011）：stage 期间的 push 不消耗 GitHub Actions，开发完成后手动跑一次 CI 把关。因此 master 上「CI 常绿」需要提交者自觉手动触发，Release 也不要求 CI 先通过。

## CI 流水线（ci.yml）

### 触发与权限

```yaml
on:
  workflow_dispatch:

permissions:
  contents: read
```

仅手动触发；权限收紧为 `contents: read`。

### 两个 job 与依赖

```yaml
jobs:
  frontend-mobile: ...          # runs-on: windows-latest
  go-quality:
    needs: frontend-mobile      # 复用前端/移动端构建产物
    strategy:
      matrix:
        os: [windows-latest, macos-latest]
```

执行顺序：`frontend-mobile` 先跑，产出 `embedded-web-assets` artifact（`frontend/dist` + `mobile/dist`）；`go-quality` 下载该 artifact 后在 Windows 与 macOS 两条矩阵腿上并行做 Go 侧检查。`fail-fast: false`：一条腿失败不取消另一条。

### frontend-mobile job（windows-latest）

| # | 步骤 | 命令 / action | 说明 |
|---|------|---------------|------|
| 1 | Checkout | `actions/checkout@v4` | — |
| 2 | Setup Node.js | `actions/setup-node@v4`，`node-version: '20.19.0'`（精确版本），npm cache 依赖 `frontend/` 与 `mobile/` 的 `package-lock.json` | — |
| 3 | Install frontend dependencies | `npm ci`（working-directory `frontend`） | lockfile 与 package.json 不一致即失败 |
| 4 | Build frontend | `npm run build` = `vue-tsc --noEmit && vite build` | **桌面前端类型门在这里** |
| 5 | Install mobile dependencies | `npm ci`（working-directory `mobile`） | — |
| 6 | Build mobile | `npm run build` = `vue-tsc -b && vite build` | **移动端类型门在这里** |
| 7 | Test mobile | `npm run test` = `vitest run` | 移动端单元测试在 CI 中执行 |
| 8 | Check contrast ratios | `node scripts/check-contrast.mjs`（working-directory `mobile`） | 移动端颜色对比度校验 |
| 9 | Upload embedded web assets | `actions/upload-artifact@v4`，上传 `frontend/dist` + `mobile/dist`，`if-no-files-found: error` | 供 go-quality job 复用（`main.go` embed 需要 `mobile/dist`） |

### go-quality job（matrix: windows-latest + macos-latest）

| # | 步骤 | 命令 / action | 说明 |
|---|------|---------------|------|
| 1 | Checkout | `actions/checkout@v4` | — |
| 2 | Download embedded web assets | `actions/download-artifact@v4`，还原 `frontend/dist` + `mobile/dist` 到工作区 | Go 编译需要 embed 目标存在 |
| 3 | Setup Go | `actions/setup-go@v5`，`go-version: '1.25.0'`（精确版本），cache | — |
| 4 | Go vet | `go vet ./...` | 两条腿都跑 |
| 5 | golangci-lint | `golangci/golangci-lint-action@v9`，pinned `v2.12.2`，配置 `.golangci.yml` | 两条腿都跑，使 `_windows.go`/`_darwin.go` 平台专属文件均被静态检查 |
| 6 | Go test | `go test ./... -count=1` | **仅 macOS 腿全量跑**（`if: matrix.os == 'macos-latest'`） |
| 7 | Go test (compile only) | `go test ./... -run '^$' -count=1` | **Windows 腿仅编译检查**，不执行测试 |

### CI 的硬门与不跑的内容

**CI 真正执行的质量门**：
- 桌面与移动端的 vue-tsc 类型检查（内嵌于各自的 `npm run build`）。
- 移动端 vitest 单元测试与对比度校验。
- `go vet ./...`（Windows + macOS）。
- `golangci-lint` v2.12.2（Windows + macOS，平台专属文件全覆盖）。
- `go test ./...`（仅 macOS 全量执行；Windows 仅验证编译）。

**CI 不跑的内容**：
- **`go test -race`**（并发包如 `session`/`pty`/`remote` 的竞态检测需本地手动跑）。
- **Windows 上的 Go 测试执行**（仅 `-run '^$'` 编译检查）。
- **`wails build`**：CI 不产出可运行二进制；go-quality 的 Go 编译由 `go vet`/`go test` 隐式覆盖，Wails 打包层面（ldflags、捆绑）的问题只在 Release workflow 或本地构建时暴露。
- **自动触发**：push/PR 不会跑 CI，忘记手动触发就没有门禁。

## Release 流水线（release.yml）

### 触发与权限

```yaml
on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write
```

任何 `v` 开头的 tag 推送即触发（如 `v1.3.50`）。权限放开为 `contents: write`，以允许创建 Release 与上传资产。

### 两个并行的构建 job

| job | runner | 产物 | 资产命名 |
|-----|--------|------|---------|
| `build-windows` | `windows-latest` | `build/bin/amagi-codebox.exe` → 7z zip | `amagi-codebox-<tag>-windows-amd64.zip` |
| `build-macos-arm64` | `macos-latest` | `build/bin/amagi-codebox.app` → ditto zip（经 `release-assets/`） | `amagi-codebox-<tag>-darwin-arm64.zip` |

两个 job **无 `needs` 依赖，并行执行**，结构对称。共享步骤：

1. `actions/checkout@v4`。
2. `actions/setup-go@v5`，`go-version: '1.25.0'`（精确版本，与 CI 一致）。
3. `actions/setup-node@v4`，`node-version: '20.19.0'`（精确版本），npm cache。
4. **Install Wails**：`go install github.com/wailsapp/wails/v2/cmd/wails@latest`（CI 无此步）。
5. frontend：`npm ci` → `npm run build`。
6. mobile：`npm ci` → `npm run build`。
7. Get version：`VERSION=${GITHUB_REF#refs/tags/}`（含 `v` 前缀）。
8. Sync wails.json version：python3 内联脚本把 tag 去 `v` 前缀写入 `wails.json` 的 `info.productVersion`（仅 CI 工作副本，不回写仓库）。
9. Compute build metadata：`git_commit`（短哈希）、`build_time`（UTC ISO）、`go_version`（`go version` 第三字段）。
10. Build：**注入全部 4 个版本变量**：
    - Windows：`wails build -s -ldflags "-X main.Version=... -X main.GitCommit=... -X main.BuildTime=... -X main.GoVersion=..."`
    - macOS：`wails build -clean -platform darwin/arm64 -ldflags "..."`（同上）
11. 打包：Windows 用 `7z a -tzip`（`shell: cmd`）；macOS 用 `ditto -c -k --sequesterRsrc --keepParent` 输出到 `release-assets/`。
12. Upload：`softprops/action-gh-release@v2`；Windows job 带 `generate_release_notes: true`，macOS job 仅上传 zip。
13. （macOS job）Codesign / Notarization placeholder：`if: ${{ false }}` 禁用占位。

### 已知限制

- Release workflow **不产出 MSI/EXE 安装包或 DMG 镜像**，仅产出两个 zip。
- macOS arm64 产物**未代码签名、未公证**，用户首次打开会被 Gatekeeper 拦截（放行方式见 `./release.md`）。
- **不覆盖 macOS Intel（amd64）**：Intel Mac 用户需自行从源码构建。

## 重要差异：CI vs Release vs 本地脚本

| 维度 | `ci.yml` | `release.yml` | `build.sh`/`build.bat`（本地） |
|------|----------|---------------|-------------------------------|
| 触发 | 手动（workflow_dispatch） | push tag `v*` | 手动 |
| runner | `windows-latest` + `macos-latest` | 同左 | 本机 |
| Go / Node 版本 | 1.25.0 / 20.19.0（精确 pin） | 1.25.0 / 20.19.0 | 本机安装（go.mod 声明 `go 1.25.0` 基线，无 toolchain 强制） |
| `wails build` | 不跑 | 跑（注入全部 4 个版本变量） | 跑（同样注入 4 个变量） |
| `go vet` | 两条腿都跑 | 不跑 | 不跑 |
| golangci-lint | 两条腿都跑（v2.12.2） | 不跑 | 不跑（可本地装同版本） |
| `go test` | macOS 全量；Windows 仅编译 | 不跑 | 不跑（需手动） |
| 移动端测试/对比度 | 跑（vitest + check-contrast） | 不跑 | 不跑（需手动） |
| 版本号来源 | 不涉及 | tag（含 `v`），注入 `main.Version` | `git describe --tags --abbrev=0` → `wails.json` productVersion → `dev` |
| `wails.json` 改写 | 不涉及 | python3 同步 `info.productVersion`（仅 CI 副本） | 不改写 |

关键观察：
- **CI 与 Release 的 npm 依赖安装用 `npm ci`**（要求 lockfile 与 package.json 一致），lockfile 漂移会在 CI 上失败而本地 `npm install` 可能通过。
- **`wails build` 在 CI 中不跑**：PR/提交即便让 `wails build` 失败（如 `preBuildHooks` 失效导致 `mobile/dist` 缺失），CI 也不会发现；但 go-quality 通过 artifact 复用 `frontend/dist`/`mobile/dist`，至少保证 embed 目标存在时 Go 侧可编译。
- **Release workflow 的 `wails.json` 同步不回写仓库**：tag 推送后仓库中的 `wails.json` 保持原值。这是设计预期，不是 bug。
- 工具链版本在 CI/Release 精确 pin（Go `1.25.0`、Node `20.19.0`）；本地 Node 由根 `.node-version`（`20.19.0`，fnm/nodenv 消费）对齐，Go 无本地锁，新工具链按原样使用。

## 修改建议

新增或调整 CI/CD 步骤时的注意事项：
- **加 Windows 上的 `go test` 执行**：去掉 `-run '^$'` 限制即可，但需评估含 PTY/ConPTY 的测试在 Windows runner 上的稳定性。
- **加 `-race`**：Windows 上 `-race` 需要 gcc（`windows-latest` 默认带 mingw，需验证）；macOS 腿可直接加。
- **补 `darwin/amd64` 到 Release**：复制 `build-macos-arm64` job 并改 `-platform darwin/amd64`、资产命名即可。
- **启用 macOS 签名公证**：把 `release.yml` 中两个 placeholder 步骤的 `if: ${{ false }}` 改为合适条件，并准备 secrets（Developer ID 证书、app-specific password 等）。详见 `./release.md`「前置条件」。

## 待核实项

- CI 仅手动触发（C-011 设计），是否计划恢复 push/PR 自动触发待确认。
- Release workflow 不跑 `go test` / `golangci-lint`，质量门完全前置在手动 CI 与本地；是否给 Release 加测试门待确认。
- macOS 签名公证两个占位步骤的启用时间表未公开，待确认。
- README 列出的 MSI/EXE/DMG 安装包形态与 workflow 实际产出的 zip 不一致；是否引入打包工具待确认。
