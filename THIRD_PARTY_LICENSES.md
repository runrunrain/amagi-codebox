# Third-Party Licenses

> Amagi CodeBox 直接依赖许可登记。核验日期：2026-08-02（版本与许可均取自官方 npm registry 与官方 GitHub 仓库 LICENSE 原文双源，详见 wenqu C-002 资料包 `agent-outputs/wenqu/20260802-m0-02-dependency-research/research-report.md`）。
>
> 本表登记 C-002 冻结的 5 个直接依赖（2 新增 + 1 新增 devDep + 2 复用）。传递依赖的许可由各包自带 LICENSE 文件承载，不在此逐一登记。

## 直接依赖

| # | 包名 | 精确版本 | 许可 | 使用端 | 用途 | npm registry | 官方 LICENSE |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | `pinia` | 3.0.4 | MIT | mobile（远程 Web 端） | 状态管理（Vue 3 store） | https://registry.npmjs.org/pinia/3.0.4 | https://github.com/vuejs/pinia/blob/master/LICENSE |
| 2 | `@tanstack/vue-virtual` | 3.13.35 | MIT | mobile（远程 Web 端） | 虚拟滚动（headless Virtualizer adapter） | https://registry.npmjs.org/@tanstack/vue-virtual/3.13.35 | https://github.com/TanStack/virtual/blob/master/LICENSE |
| 3 | `@playwright/test` | 1.58.2 | Apache-2.0 | root（devDependency） | E2E 测试 runner（M0-05 基建） | https://registry.npmjs.org/@playwright/test/1.58.2 | https://github.com/microsoft/playwright/blob/master/LICENSE |
| 4 | `qrcode` | 1.5.4 | MIT | frontend（桌面端，复用） | 二维码生成（`QRCode.toCanvas`） | https://registry.npmjs.org/qrcode/1.5.4 | https://github.com/soldair/node-qrcode/blob/master/license |
| 5 | `html5-qrcode` | 2.3.8 | Apache-2.0 | mobile（远程 Web 端，复用） | 二维码扫码（`Html5QrcodeScanner` 相机扫码） | https://registry.npmjs.org/html5-qrcode/2.3.8 | https://github.com/mebjas/html5-qrcode/blob/master/LICENSE |

### 版本钉定方式

- `pinia`、`@tanstack/vue-virtual`：mobile `package.json` 中为**精确版本**（无 caret，如 `"pinia": "3.0.4"`）。
- `@playwright/test`：root `package.json` devDependency 中为**精确版本**（`"@playwright/test": "1.58.2"`）。
- `qrcode`、`html5-qrcode`：复用既有声明范围（`^1.5.4` / `^2.3.8`），lockfile resolved 锁定为 1.5.4 / 2.3.8（C-002 冻结值要求不新增/升级/移除）。

## 许可类型说明

- **MIT**（pinia、@tanstack/vue-virtual、qrcode）：宽松许可，允许商用、修改、分发，需保留版权声明。
- **Apache-2.0**（@playwright/test、html5-qrcode）：宽松许可，含专利授权条款，允许商用、修改、分发，需保留 NOTICE 与版权声明。

> Apache-2.0 与 MIT 同属宽松许可，可混用。wenqu 资料包已登记：技术方案 TD-15/§13 原假设“均 MIT”与官方许可不符（@playwright/test、html5-qrcode 实为 Apache-2.0），本表按官方双源核验结果登记。

## Playwright 浏览器二进制许可（披露 / M0-05 实际 runtime）

`@playwright/test` 的 runtime 依赖 `playwright` 会在常规安装时额外下载浏览器二进制（如 Chromium / WebKit / Firefox），其许可与本表的 npm 包许可不同。本轮（M0-02）仅锁 npm 依赖与许可登记，不安装、不核验、不登记任何浏览器二进制许可。

- **本任务安装行为（可归属 M0-02）**：M0-02 的 root 安装命令使用 `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` + `--ignore-scripts`，且本任务**未执行** `playwright install`。因此 M0-02 的安装流程未主动下载浏览器二进制。M0-02 不运行 Playwright E2E（属 M0-05）。
- **机器全局 cache 状态（不归属 M0-02，不作 M0-02 证据）**：本机 macOS 默认 Playwright cache 路径 `~/Library/Caches/ms-playwright` **已存在**并含多个浏览器二进制（chromium / chromium_headless_shell / ffmpeg / webkit 等多个 revision）。这些二进制早于本任务、来自其他 Playwright 安装，**不能**归因于 M0-02，也**不能**用作“本机无浏览器二进制”的证明。本轮对其存在既不否认、也不登记其许可。
- **M0-05 实际 runtime（2026-08-02）**：9 个 smoke 用例只启动 **Chrome Headless Shell 145.0.7632.6 / Playwright revision 1208**。`DEBUG=pw:browser` 日志中的实际 executable 为 `~/Library/Caches/ms-playwright/chromium_headless_shell-1208/chrome-headless-shell-mac-arm64/chrome-headless-shell`（SHA-256 `a46b3b1e63163fa2d2437fb6ae967cb5a73b50050bca32f1964e6129b6228244`）；版本/revision 映射来自已锁定 `playwright-core@1.58.2` 的 `browsers.json`。随包 `LICENSE.headless_shell`（同目录，SHA-256 `0f7eb0bbe8a864c61984caec3a3e94ad4abfaf7c143c1214ca9f05455f1b621b`）**以 Chromium BSD-3-Clause 文本开头，随后包含该 executable 实际 bundle 的第三方组件 notices/credits**（例如 `@bufbuild/protobuf`/Apache-2.0、`@vscode/web-custom-data`/MIT 等，并非单一 BSD-3 文本）；官方第一源为 [Chromium LICENSE](https://chromium.googlesource.com/chromium/src/+/refs/heads/main/LICENSE)。执行证据见 `agent-outputs/wukong/20260802-m0-05-playwright-implementation/logs/01-update-snapshots.log`。
- **使用与分发边界**：该 executable 来自机器既有共享 Playwright cache；M0-05 **使用**了它，但未执行 `playwright install`，不得把 cache 的创建/下载归因于本任务。浏览器只在 CI/开发机测试运行；cache 不提交、也不随 Amagi CodeBox release 分发。若未来把 binary 纳入产品分发，必须重新核验 notice/credits 与再分发义务。
- **本轮未使用对象**：`playwright.config.ts` 仅配置 Chromium projects 且 `video: 'off'`，所以 full Chrome for Testing、WebKit、Firefox 与 Playwright FFmpeg 均未被本轮 smoke 启动；共享 cache 中即使存在这些 binary，也不构成本轮实际使用或许可登记。后续实际启用时再按 executable、随附 license/credits 与 hash 增量登记。

## 许可来源与核验方法

- 版本与 `license` 字段：npm registry `<pkg>/<version>` JSON manifest。
- 许可第二源：官方 GitHub 仓库 LICENSE 文件原文（URL 见上表）。
- 双源一致方登记为本表“许可”列；存在偏差时以官方 LICENSE 原文为准。
- 详细逐包证据（peer/runtime deps、包体、维护者、发布日期、官方 URL）见 wenqu 资料包 §2。
