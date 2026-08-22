# 打包发布

面向负责发布 Amagi CodeBox 桌面二进制与 GitHub Release 的运维同学与维护者。内容基于仓库现有的 `build.sh`、`build.bat`、`wails.json`、`.github/workflows/release.yml` 核实；凡未由脚本或 workflow 实际执行的步骤均以「待核实」标注。

相关文档：
- 版本号管理与 ldflags 注入细节见 `./versioning.md`。
- CI/CD 流水线总览见 `./ci-cd.md`。
- 本地构建细节与排错见 `../developer/build-dev.md`。
- 测试前置（CI 的 Go 测试覆盖范围）见 `../developer/testing.md`。

## 产物形态

Amagi CodeBox 一次完整构建涉及三条产物线（详见 `../developer/build-dev.md`）：

1. 桌面前端 `frontend/dist`（嵌入主二进制）。
2. 移动端前端 `mobile/dist`（嵌入主二进制，经 `main.go` 的 `//go:embed all:mobile/dist`）。
3. 桌面主二进制 `build/bin/amagi-codebox`（macOS `.app` bundle）或 `build/bin/amagi-codebox.exe`（Windows）。

发布面向终端用户的产物只有第 3 项。当前 `.github/workflows/release.yml` 实际产出的可下载资产为：

| 平台 | runner | 二进制位置 | 打包方式 | 资产命名 |
|------|--------|------------|----------|----------|
| Windows amd64 | `windows-latest` | `build/bin/amagi-codebox.exe` | 7z → zip | `amagi-codebox-<tag>-windows-amd64.zip` |
| macOS arm64 | `macos-latest` | `build/bin/amagi-codebox.app` | ditto → zip（输出到 `release-assets/`） | `amagi-codebox-<tag>-darwin-arm64.zip` |

需要特别注意的事实：
- Release workflow **只产出 zip 包**，不产出 MSI/EXE 安装包或 DMG 镜像（待核实：是否计划由 NSIS / create-dmg 等工具补齐，目前 workflow 未实现）。
- macOS 产物**未代码签名、未公证**：`release.yml` 的 `Codesign placeholder` 与 `Notarization placeholder` 步骤以 `if: ${{ false }}` 禁用，仅作占位。用户首次打开 zip 中的 `.app` 时会遇到 Gatekeeper 拦截，需在「系统设置 → 隐私与安全性」放行，或执行 `xattr -dr com.apple.quarantine /path/to/amagi-codebox.app`（待核实：何时接入 Developer ID 签名与公证）。
- Release workflow **不覆盖 macOS Intel（amd64）**：仅 `darwin/arm64` 一个 job。Intel Mac 用户需自行从源码构建。
- `release-assets/` 是 macOS job 的临时打包目录，不纳入 git 跟踪（本地工作区现存的 zip 为历史构建残留，非构建输入）。

## 本地构建

### 单条命令

```bash
wails build
```

产物路径：`build/bin/amagi-codebox.app`（macOS）或 `build/bin/amagi-codebox.exe`（Windows）。

`wails build` 内部行为（由 `wails.json` 决定）：
1. 触发 `preBuildHooks`（`*/*` 全平台）：在 `frontend/` 目录下执行 `npm --prefix ../.. run build:mobile`（= `npm --prefix mobile ci && npm --prefix mobile run build`），先生成 `mobile/dist`。
2. 执行 `frontend:build`：`npm run build`（= `vue-tsc --noEmit && vite build`，类型门）。
3. 重新生成 `frontend/wailsjs/` 绑定。
4. 编译 Go，嵌入 `frontend/dist` 与 `mobile/dist`。

注意：**直接 `wails build` 不会注入版本信息**（`main.Version` 保持默认 `dev`，运行时由 `GetAppInfo` 回退到 `wails.json` productVersion）。要注入版本号，用下面的脚本，或参考 `./versioning.md` 自行构造 `-ldflags`。

### Unix 一键脚本

```bash
./build.sh
```

`build.sh` 的三步（核实自脚本本身）：
1. `[1/3]` 进入 `frontend/` 执行 `npm ci && npm run build`。
2. `[2/3]` 若根 `package.json` 存在 `build:mobile` 脚本，执行 `npm run build:mobile`。
3. `[3/3]` 解析版本号，调用：
   ```bash
   wails build -ldflags "-X main.Version=<version> -X main.GitCommit=<commit> -X main.BuildTime=<time> -X main.GoVersion=<gover>"
   ```

版本号解析顺序（与 `build.bat` 对齐）：`git describe --tags --abbrev=0` → `wails.json` `info.productVersion`（python3 解析，当前 `1.3.50`）→ 字符串 `dev`。`GoVersion` 取 `go version | awk '{print $3}'`（只取 `go1.x.y` 字段，避免空格拆词导致 linker 失败）。详见 `./versioning.md`。

产物：`build/bin/amagi-codebox.app`。`build.sh` **不**额外复制产物到任何用户目录。

### Windows 一键脚本

```bat
build.bat
```

`build.bat` 的五步（核实自脚本本身）：
1. `[1/5]` 检查环境；若 `wails` 缺失则尝试 `go install github.com/wailsapp/wails/v2/cmd/wails@latest` 自动安装。
2. `[2/5]` 进入 `mobile/` 执行 `npm ci --prefer-offline && npm run build`。
3. `[3/5]` 解析版本号，调用：
   ```bat
   wails build -ldflags "-X main.Version=<version> -X main.GitCommit=<commit> -X main.BuildTime=<time> -X main.GoVersion=<gover>"
   ```
4. `[4/5]` 把 `build\bin\amagi-codebox.exe` 复制到项目根目录。
5. `[5/5]` 复制到 `%USERPROFILE%\.amagi-codebox\amagi-codebox.exe`（若目标正在运行会警告但不会终止脚本）。

版本号解析顺序：`git describe --tags --abbrev=0` → `wails.json` `info.productVersion`（powershell `ConvertFrom-Json`）→ 字符串 `dev`。`GoVersion` 用 `go env GOVERSION`，`BuildTime` 用 `powershell Get-Date -Format yyyy-MM-ddTHH:mm:ssZ`。

与 `build.sh` 的差异：
- `build.bat` 额外做两步复制（项目根 + 用户目录）；`build.sh` 无复制步骤。
- `build.bat` 不显式构建 `frontend/`，依赖 `wails build` 内部的 `frontend:build`；`build.sh` 在脚本层显式先构建 `frontend/` 再调 `wails build`。
- `build.bat` 用 `npm ci --prefer-offline`（要求 `mobile/package-lock.json` 存在）；`build.sh` 前端用 `npm ci`。

## GitHub Releases（自动发布）

`.github/workflows/release.yml` 是发布流水线的真相源。与 CI（仅 `workflow_dispatch` 手动触发）不同，Release 由 tag 推送自动触发。

### 触发条件

```yaml
on:
  push:
    tags:
      - 'v*'
```

推送 `v` 开头的 tag（如 `v1.3.50`）即触发。权限：`permissions: contents: write`，允许 workflow 创建 Release 与上传资产。

### 两个并行 job

`build-windows`（`windows-latest`）与 `build-macos-arm64`（`macos-latest`），**无 `needs` 依赖，并行执行**，共同挂到同一个 Release。两个 job 结构对称：

1. Checkout（`actions/checkout@v4`）。
2. Setup Go `1.25.0`（`actions/setup-go@v5`，精确版本，带 cache）。
3. Setup Node.js `20.19.0`（`actions/setup-node@v4`，npm cache，依赖 `frontend/package-lock.json` 与 `mobile/package-lock.json`）。
4. Install Wails：`go install github.com/wailsapp/wails/v2/cmd/wails@latest`。
5. `npm ci` → `npm run build`（frontend，working-directory `frontend/`）。
6. `npm ci` → `npm run build`（mobile，working-directory `mobile/`）。
7. Get version：从 `GITHUB_REF` 剥出 tag 名（**含 `v` 前缀**）输出为 `VERSION`。
8. Sync wails.json version：python3 内联脚本将 tag 去 `v` 前缀写入 `wails.json` 的 `info.productVersion`（仅 CI 工作副本生效，不回写仓库）。
9. Compute build metadata：采集 `git rev-parse --short HEAD`、UTC 构建时间、`go version` 第三字段，输出为 `git_commit` / `build_time` / `go_version`。
10. Build：**注入全部 4 个版本变量**（与本地脚本一致）：
    - Windows：`wails build -s -ldflags "-X main.Version=<v> -X main.GitCommit=<c> -X main.BuildTime=<t> -X main.GoVersion=<g>"`（`-s` 静默）。
    - macOS：`wails build -clean -platform darwin/arm64 -ldflags "-X main.Version=... ..."`（`-clean` 清缓存，显式指定 Apple Silicon）。
11. 打包：
    - Windows（`shell: cmd`）：`7z a -tzip ../../amagi-codebox-<VERSION>-windows-amd64.zip amagi-codebox.exe`。
    - macOS：`ditto -c -k --sequesterRsrc --keepParent build/bin/amagi-codebox.app release-assets/amagi-codebox-<VERSION>-darwin-arm64.zip`（保留 `.app` 资源 fork 与权限）。
12. Upload Release Asset：`softprops/action-gh-release@v2`，Windows job 带 `generate_release_notes: true`（GitHub 根据 commits 自动生成发行说明）；macOS job 仅上传 zip。
13. （macOS job 额外）Codesign / Notarization placeholder：`if: ${{ false }}` 禁用占位。

## 发布步骤建议

以发布 `v1.3.51` 为例：

1. **同步版本号**（详见 `./versioning.md`「升级版本号操作清单」）：
   - `wails.json` 的 `info.productVersion` → `1.3.51`。
   - 根 `package.json` 的 `version` → `1.3.51`。
   - `frontend/package.json` 的 `version` → `1.3.51`。
   - `mobile/package.json` 的 `version`（当前 `1.0.5`）按移动端自身节奏独立演进，不强制与桌面同步。
2. **归档 CHANGELOG**：把 [CHANGELOG.md](../../CHANGELOG.md) 中 `[Unreleased]` 的内容归入 `1.3.51` 并填写日期。
3. **本地预校验**：
   - `go vet ./...` 与 `golangci-lint run`（CI 两条平台腿都会跑；本地可用 `.golangci.yml` 配置）。
   - `go test ./... -count=1`（CI 在 macOS 腿上全量跑；Windows 腿仅编译检查）。
   - `npm --prefix frontend run build`（含 `vue-tsc --noEmit` 类型门）。
   - `npm --prefix mobile run build`、`npm --prefix mobile run test`（vitest）、`node mobile/scripts/check-contrast.mjs`。
   - 目标平台上 `./build.sh` 或 `build.bat` 冒烟一次，确认能产出可运行二进制。
4. **手动触发一次 CI**（可选但推荐）：CI 为 `workflow_dispatch`，stage 期间不自动消耗 Actions；发布前在 Actions 页面手动跑一次确认绿。
5. **提交变更**：
   ```bash
   git add wails.json package.json frontend/package.json CHANGELOG.md
   git commit -m "chore: bump version to 1.3.51"
   git push origin master
   ```
6. **打 tag 并推送**（触发 Release workflow）：
   ```bash
   git tag v1.3.51
   git push origin v1.3.51
   ```
7. **观察 CI**：在 GitHub Actions 页面等待 `Release` workflow 的 `build-windows` 与 `build-macos-arm64` 全部通过。若失败，修复后**删除 tag 并重打**：
   ```bash
   git tag -d v1.3.51
   git push origin :refs/tags/v1.3.51
   # 修复后重新执行第 5-6 步
   ```
8. **校验 Release**：进入 GitHub Releases 页面，确认：
   - 资产 `amagi-codebox-v1.3.51-windows-amd64.zip` 与 `amagi-codebox-v1.3.51-darwin-arm64.zip` 已上传。
   - 发行说明由 `generate_release_notes: true` 自动生成。
   - 至少在一台 Windows 与一台 macOS（Apple Silicon）上下载、解压、运行冒烟；关于对话框应显示版本号、commit、构建时间与 Go 版本（Release 构建已注入全部 4 个变量）。
9. **更新 README 徽章**（手工）：README 顶部 version 徽章硬编码，发版时需手工同步（待核实：是否计划由 workflow 自动更新）。

## 前置条件（首次发布前）

仓库维护者首次跑通 Release workflow 前需确认：
- 仓库 Settings → Actions → Workflow permissions 允许 `contents: write`。
- `softprops/action-gh-release@v2` 在 `GITHUB_TOKEN` 权限范围内可创建 Release。
- 若计划启用 macOS 签名与公证（当前禁用），需准备 Developer ID Application 证书、App-specific password、entitlements plist，并把 `release.yml` 中两个 placeholder 步骤的 `if: ${{ false }}` 改为合适条件（待核实：何时接入）。

## 常见问题

- **`wails build` 报 `mobile/dist` 不存在**：确认 `wails.json` 的 `preBuildHooks` 生效；手工绕过 `wails build` 时先 `npm run build:mobile`。详见 `../developer/build-dev.md`。
- **macOS zip 解压后 `.app` 无法打开**：「已损坏」或「无法验证开发者」提示源自未签名；执行 `xattr -dr com.apple.quarantine /path/to/amagi-codebox.app` 放行。
- **tag 已推但 Release 没生成**：检查 Actions 页面 workflow 是否被禁用、权限是否足够、两个 job 是否都成功（任一 upload 失败不回滚已上传资产）。
- **关于对话框显示 `dev`**：构建时未注入 ldflags 且运行目录找不到 `wails.json`，详见 `./versioning.md`「排查」。

## 待核实项

- README 列出的 Windows MSI/EXE 安装包与 macOS DMG 镜像在 `release.yml` 中无对应步骤；当前实际产物仅为 zip。是否计划补齐安装包生成待确认。
- macOS 产物的代码签名与公证步骤已在 workflow 中预留占位（`if: ${{ false }}`），启用时间待确认。
- 是否计划补 `darwin/amd64` job 覆盖 Intel Mac，待确认。
- README 顶部 version 徽章硬编码（当前 `1.3.50`），是否改由 workflow 动态更新待确认。
