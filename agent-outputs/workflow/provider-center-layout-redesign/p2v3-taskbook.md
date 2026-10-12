# P2V3 实现任务 — design-v3 顶侧页签恢复 + 左栏收缩（luoshen）

角色：luoshen。主特色：审美判断（UI 实现、完整状态覆盖、视觉验证是交付证据）；附带·轻：工匠闭环（构建/测试自验、报告）。

## 1. 必读 artifact（按序全文，缺失即停止报告）

1. **`agent-outputs/workflow/provider-center-layout-redesign/design-v3.md`（v3 实现规格，全文）**
2. `agent-outputs/workflow/provider-center-layout-redesign/design-v2.md`（v3 未覆盖处按 v2 执行）
3. 当前实现树（HEAD = v1.3.96 发布态）

v3 规格「必须」条款不回头确认；实现细节级偏差自行决定并记入报告；设计级偏差不允许。

## 2. 开工前只读核对（design-v3 §6.3，不改现场）

1. `git rev-parse HEAD` + `git describe --tags`：确认 HEAD 为 v1.3.96 发布提交；不符则报告 Leader 停止。
2. `git status --porcelain -- frontend/src e2e`：应为空；有他人改动只归因不覆盖并报告。

## 3. 实现范围

**生产代码：仅 `frontend/src/views/ProviderCenterView.vue` 一个文件**（design-v3 §3.1 逐项指令：删/留清单、模板骨架、页签常量派生自 configNavigation、样式与 <1024 窄档规则、PresetLoadError 缩减）。

**五个编辑器、`configNavigation.ts`、`ui/*`、e2e 两文件：零改动**（external 模式休眠保留；`git diff HEAD -- e2e` 必须为空）。

**验证工具（x-harness）**：按 §3.6 新建 `v3-{ab.sh,helpers.js,shots.sh,runtime.mjs,static.mjs}`（v2 文件原样保留为历史证据；transport 复用 v2-mock-wails.js）。

**产物**：
- `agent-outputs/workflow/provider-center-layout-redesign/impl-report.md`（追加「v3 轮」章节，含全部验证证据）
- 截图存 `screenshots/`，`v3-` 前缀（不覆盖既有证据）

## 4. 实现切片（design-v3 §6.3-③）

① ProviderCenterView 改造 → V1 构建 + V3 e2e（应零变化通过）→ ② v3 harness 新建 + v3-static → ③ 七批全跑：drafts / dynamic / lifecycle / narrow / states / coverage / interactions（local 路径首次在页面级验证，**不许沿用 v2 结果**）→ ④ 两档九视图截图（1280×800 + 800×600）+ dynamic/narrow 批截图，自行读图裁决。

## 5. 验证要求（证据入报告：命令 + cwd + 退出码/结果）

| # | 验证 | 命令（cwd=仓库根） |
|---|---|---|
| V1 | 构建 | `npm --prefix frontend run build` exit=0 |
| V2 | 逻辑测试 | `npm --prefix frontend run test -- src/__tests__/components/provider` 全过（测试文件零改动） |
| V3 | Pi e2e | `npx playwright test -c e2e/pi-amagi-config.playwright.config.ts` 7 passed；`git diff HEAD -- e2e` 为空 |
| V4/V5 | harness | `node …/x-harness/v3-runtime.mjs drafts` / `lifecycle` |
| 七批+X | 视觉与结构 | §3.6 各批 + §5.3 X1–X12（X1 条带口径、X2 窄档 grid、X11 subnav 行为、X12 可逃逸等）；dev server 用 `npm --prefix frontend run dev -- --port 5199 --strictPort --host 127.0.0.1`（vite 8 默认只绑 ::1，必须显式 --host 127.0.0.1） |

## 6. 硬约束

- design-v3 §3.1「保留（逐字不动）」清单：PresetLoadError onRetry 整段（含整页重载逻辑）、formatModel/cliModel setter、presetLoadKey、lazyPreset、watch(mainTab)、导入导出、isWorkspace 窄档类。
- `:key="presetLoadKey"` 保持 7 处；外层 `:key="activePageId"` 保留。
- 页签文案与 v1 逐一相同（S13：coverage 批读 DOM 断言）。
- 不新增依赖；中文 UI；token 不动。
- S7 收紧口径：`git diff HEAD --name-only` 中 frontend/ 与 e2e/ 下只有 ProviderCenterView.vue。
- 报告如实记录：local 路径新暴露的问题、X1 的 subnavRows/firstContentTop 实测值、视觉结论（对比 v3-pi-agents-800x600 与 v2/v1 同视图）。

## 7. 报告追加结构（impl-report.md「v3 轮」章节）

1. 改动文件清单（应为生产 1 文件 + harness v3-* 新建）
2. design-v3 §5.2 S 系列逐项自查（S3/S6/S7/S11/S13 重点）
3. 七批 + X1–X12 结果与截图路径
4. 与 v2/v1 同视图视觉对比结论（顶侧层级、左栏密度、纵向留白）
5. 偏差与决策记录、回滚说明（单文件 revert）
6. 覆盖缺口与遗留（含 Registry/Auth 归属左栏待主上看图裁决项）
