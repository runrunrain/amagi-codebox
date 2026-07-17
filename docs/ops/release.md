# 打包发布

面向负责发布 Amagi CodeBox 桌面二进制与 GitHub Release 的运维同学与维护者。内容基于仓库现有的 `build.sh`、`build.bat`、`wails.json`、`.github/workflows/release.yml` 核实；凡未由脚本或 workflow 实际执行的步骤均以"（待核实）"标注。

相关文档：
- 版本号管理见 `./versioning.md`。
- CI/CD 流水线总览见 `./ci-cd.md`。
- 本地构建细节与排错见 `../developer/build-dev.md`。
- 测试前置（CI 不跑 `go test`）见 `../developer/testing.md`。

## 产物形态

Amagi CodeBox 一次完整构建会产出三套资源（详见 `../developer/build-dev.md` "三条产物线"）：

1. 桌面前端 `frontend/dist`（嵌入主二进制）。
2. 移动端前端 `mobile/dist`（嵌入主二进制，供配套移动 App 使用）。
3. 桌面主二进制 `build/bin/amagi-codebox`（macOS）或 `build/bin/amagi-codebox.exe`（Windows）。

发布面向终端用户的产物只有第 3 项。当前 `.github/workflows/release.yml` 实际产出的可下载资产为：

| 平台 | runner | 二进制位置 | CI 打包产物 | 命名 |
|------|--------|------------|-------------|------|
| Windows amd64 | `windows-latest` | `build/bin/amagi-codebox.exe` | zip（7z） | `amagi-codebox-<tag>-windows-amd64.zip` |
| macOS arm64 | `macos-latest` | `build/bin/amagi-codebox.app` | zip（ditto） | `amagi-codebox-<tag>-darwin-arm64.zip` |

需要特别注意的事实：
- Release workflow **只产出 zip 包**，不产出 README 中提到的 MSI/EXE 安装包或 DMG 镜像。README 的"下载安装"段所列形态与实际 workflow 不一致（待核实：是否计划由 NSIS / appinstaller / create-dmg 等工具补齐安装包，目前 workflow 未实现）。
- macOS 产物**未代码签名、未公证**：`release.yml` 的 `Codesign placeholder` 与 `Notarization placeholder` 步骤用 `if: ${{ false }}` 禁用，仅作占位。用户首次打开 macOS arm64 zip 中的 `.app` 时会遇到 Gatekeeper 拦截，需要手动在"系统设置 → 隐私与安全性"中放行，或命令行执行 `xattr -dr com.apple.quarantine /path/to/amagi-codebox.app`（待核实：何时接入 Developer ID 签名与公证）。
- Release workflow **不覆盖 macOS Intel（amd64）产物**：仅 `darwin/arm64` 一个 job。Intel Mac 用户需自行从源码构建（待核实：是否计划补 `darwin/amd64` job）。

## 本地构建

### 单条命令（推荐）

```bash
wails build
```

产物路径：`build/bin/amagi-codebox`（macOS）或 `build/bin/amagi-codebox.exe`（Windows）。

`wails build` 内部行为（由 `wails.json` 决定）：
1. 触发 `preBuildHooks`：在 `frontend/` 目录下执行 `npm --prefix ../.. run build:mobile`，先生成 `mobile/dist`。
2. 执行 `frontend:build`：`npm run build`（含 `vue-tsc --noEmit` 类型门）。
3. 重新生成 `frontend/wailsjs/` 绑定。
4. 编译 Go，嵌入 `frontend/dist` 与 `mobile/dist`。

注意：**直接 `wails build` 不会注入版本信息**（`main.Version` 保持默认 `dev`）。要注入版本号，用下面的脚本，或参考 `./versioning.md` 自行构造 `-ldflags`。

### Unix 一键脚本

```bash
./build.sh
```

`build.sh` 的三步（核实自脚本本身）：
1. `[1/3]` 进入 `frontend/` 执行 `npm install && npm run build`。
2. `[2/3]` 若根 `package.json` 存在 `build:mobile` 脚本，执行 `npm run build:mobile`。
3. `[3/3]` 解析版本号，调用：
   ```bash
   wails build -ldflags "-X main.Version=<version> -X main.GitCommit=<commit> -X main.BuildTime=<time> -X main.GoVersion=<gover>"
   ```

版本号解析顺序（与 `build.bat` 对齐）：`git describe --tags --abbrev=0` → `wails.json` `info.productVersion`（python3 解析）→ 字符串 `dev`。详见 `./versioning.md`。

产物：`build/bin/amagi-codebox`。`build.sh` **不**额外复制产物到任何用户目录。

### Windows 一键脚本

```bat
build.bat
```

`build.bat` 的五步（核实自脚本本身）：
1. `[1/5]` 检查环境；若 `wails` 缺失则尝试 `go install ... wails@latest` 自动安装。
2. `[2/5]` 进入 `mobile/` 执行 `npm ci --prefer-offline && npm run build`。
3. `[3/5]` 解析版本号，调用：
   ```bat
   wails build -ldflags "-X main.Version=<version> -X main.GitCommit=<commit> -X main.BuildTime=<time> -X main.GoVersion=<gover>"
   ```
4. `[4/5]` 把 `build\bin\amigi-codebox.exe` 复制到项目根目录。
5. `[5/5]` 复制到 `%USERPROFILE%\.amagi-codebox\amagi-codebox.exe`（若目标正在运行会警告但不会终止脚本）。

版本号解析顺序：`git describe --tags --abbrev=0` → `wails.json` `info.productVersion`（powershell `ConvertFrom-Json`）→ 字符串 `dev`。

与 `build.sh` 的差异：
- `build.bat` 额外做两步复制（项目根 + 用户目录）；`build.sh` 无复制步骤。
- `build.bat` 不显式构建 `frontend/`，依赖 `wails build` 内部的 `frontend:build`；`build.sh` 在脚本层显式先构建 `frontend/` 再调 `wails build`。
- `build.bat` 用 `npm ci --prefer-offline`（要求 `mobile/package-lock.json` 存在）；`build.sh` 用 `npm install`。

## GitHub Releases（自动发布）

`.github/workflows/release.yml` 是发布流水线的真相源。

### 触发条件

```yaml
on:
  push:
    tags:
      - 'v*'
```

只要推送 `v` 开头的 tag（如 `v1.2.80`）即触发。权限：`permissions: contents: write`，允许 workflow 创建 Release 与上传资产。

### build-windows job

runner：`windows-latest`。关键步骤：

1. Checkout（`actions/checkout@v4`）。
2. Setup Go 1.25（`actions/setup-go@v5`，带 cache）。
3. Setup Node.js 20（`actions/setup-node@v4`，带 npm cache，依赖 `frontend/package-lock.json` 与 `mobile/package-lock.json`）。
4. Install Wails：`go install github.com/wailsapp/wails/v2/cmd/wails@latest`。
5. `npm ci`（frontend）→ `npm run build`（frontend）。
6. `npm ci`（mobile）→ `npm run build`（mobile）。
7. Get version：从 `GITHUB_REF` 解析 tag 名称（含 `v` 前缀），输出到 step output `VERSION`。
8. Sync wails.json version：用 python3 内联脚本，将 tag 去掉 `v` 前缀写入 `wails.json` 的 `info.productVersion`。
9. Build：
   ```bash
   wails build -s -ldflags "-X main.Version=${VERSION}"
   ```
   `-s` 表示静默输出。**Release workflow 只注入 `main.Version`**，不注入 `GitCommit/BuildTime/GoVersion`（与 `build.sh`/`build.bat` 不同，详见 `./versioning.md`）。
10. Create ZIP archive（`shell: cmd`）：
    ```bat
    cd build/bin
    7z a -tzip ../../amagi-codebox-<VERSION>-windows-amd64.zip amagi-codebox.exe
    ```
11. Upload Release Asset：`softprops/action-gh-release@v2`，`generate_release_notes: true`（GitHub 根据 commits 自动生成发行说明）。

### build-macos-arm64 job

runner：`macos-latest`。关键步骤与 Windows 对称，差异在第 9、10 步：

9. Build macOS arm64 bundle：
   ```bash
   wails build -clean -platform darwin/arm64 -ldflags "-X main.Version=${VERSION}"
   ```
   `-clean` 清理构建缓存，`-platform darwin/arm64` 显式指定目标。
10. Prepare macOS arm64 artifact：用 `ditto` 打包 `.app` bundle：
    ```bash
    mkdir -p release-assets
    ditto -c -k --sequesterRsrc --keepParent build/bin/amagi-codebox.app release-assets/amagi-codebox-<VERSION>-darwin-arm64.zip
    ```
11. Codesign placeholder / Notarization placeholder：均以 `if: ${{ false }}` 禁用，仅占位。
12. Upload Release Asset：同 Windows，上传 zip 到 Release。

两个 job **并行执行**（无 `needs` 依赖），共同挂到同一个 Release（由 tag 决定）。

## 发布步骤建议

以发布 `v1.2.80` 为例：

1. **同步版本号**（详见 `./versioning.md` "升级版本号操作清单"）：
   - `wails.json` 的 `info.productVersion` → `1.2.80`。
   - 根 `package.json` 的 `version` → `1.2.80`。
   - `frontend/package.json` 的 `version` → `1.2.80`。
   - `mobile/package.json` 的 `version`（当前 `1.0.5`）按移动端自身节奏独立演进，不强制与桌面同步。
2. **本地预校验**：
   - `go vet ./...`（CI 会跑）。
   - `go test ./...`（CI 不跑，手动跑；详见 `../developer/testing.md`）。
   - `npm --prefix frontend run build`（含 `vue-tsc --noEmit` 类型门）。
   - `npm --prefix mobile run build` 与 `npm --prefix mobile run test`。
   - 目标平台上 `./build.sh` 或 `build.bat` 冒烟一次，确认能产出可运行二进制。
3. **提交变更**：
   ```bash
   git add wails.json package.json frontend/package.json
   git commit -m "chore: bump version to 1.2.80"
   git push origin master
   ```
4. **打 tag 并推送**（触发 Release workflow）：
   ```bash
   git tag v1.2.80
   git push origin v1.2.80
   ```
5. **观察 CI**：在 GitHub Actions 页面等待 `Release` workflow 的 `build-windows` 与 `build-macos-arm64` 两个 job 全部通过。若失败，修复后**删除 tag 并重打**：
   ```bash
   git tag -d v1.2.80
   git push origin :refs/tags/v1.2.80
   # 修复后重新执行第 3-4 步
   ```
6. **校验 Release**：进入 GitHub Releases 页面，确认：
   - 资产 `amagi-codebox-v1.2.80-windows-amd64.zip` 与 `amagi-codebox-v1.2.80-darwin-arm64.zip` 已上传。
   - 发行说明由 `generate_release_notes: true` 自动生成（基于 commits）。
   - 至少在一个 Windows 与一台 macOS（Apple Silicon）上下载、解压、运行冒烟。
7. **更新 README 徽章**（可选）：README 顶部的 version 徽章目前硬编码 `1.2.80`，需手工同步（待核实：是否计划由 workflow 自动更新）。

## 前置条件（首次发布前）

仓库维护者首次跑通 Release workflow 前需确认：
- 仓库 Settings → Actions → Workflow permissions 允许 `contents: write`（默认通常允许，但企业组织策略可能收紧）。
- `softprops/action-gh-release@v2` 在仓库内的 GitHub App / `GITHUB_TOKEN` 权限范围内可创建 Release。
- 若计划启用 macOS 签名与公证（当前禁用），需准备：Developer ID Application 证书、App-specific password、entitlements plist，并把 `release.yml` 中 `Codesign placeholder` / `Notarization placeholder` 的 `if: ${{ false }}` 改为合适条件（待核实：何时接入）。

## 常见问题

- **`wails build` 报 `mobile/dist` 不存在**：确认 `wails.json` 的 `preBuildHooks` 生效；手工绕过 `wails build` 时先 `npm run build:mobile`。详见 `../developer/build-dev.md`。
- **Release workflow 版本号没写进 `wails.json`**：workflow 内有 python3 内联脚本会在构建前同步 `info.productVersion`，无需手工干预；若 python3 缺失（Windows runner 已预装）该步会失败。
- **macOS zip 解压后 `.app` 无法打开**："已损坏"或"无法验证开发者"提示源自未签名；执行 `xattr -dr com.apple.quarantine /path/to/amagi-codebox.app` 放行。
- **tag 已推但 Release 没生成**：检查 Actions 页面 workflow 是否被禁用、权限是否足够、`build-windows` 与 `build-macos-arm64` 是否都成功（任意一个 upload 步骤失败都不会回滚已上传的资产，但 Release 自身需要至少一个 job 走完 `softprops/action-gh-release`）。

## 待核实项

- README 列出的 Windows MSI/EXE 安装包与 macOS DMG 镜像，在 `release.yml` 中无对应步骤；当前实际产物仅为 zip。是否计划补齐安装包生成（NSIS / create-dmg 等）待确认。
- macOS 产物的代码签名与公证步骤已在 workflow 中预留占位（`if: ${{ false }}`），启用时间待确认。
- 是否计划补 `darwin/amd64` job 覆盖 Intel Mac，待确认。
- README 顶部 version 徽章硬编码，是否改由 workflow 动态更新，待确认。
- `build.sh` 无 macOS 用户目录复制步骤（`build.bat` 有对应 `%USERPROFILE%\.amagi-codebox\`），macOS 是否需要补齐待主上确认。
