# P2 实现任务 — 按 design.md 重构两页布局（luoshen）

角色：luoshen。主特色：审美判断（UI 实现、完整状态覆盖、视觉验证是交付证据）；附带·轻：工匠闭环（构建/测试自验、实现报告）。

## 1. 必读 artifact（缺失即停止并报告，不得凭记忆实现）

**`agent-outputs/workflow/provider-center-layout-redesign/design.md`（全文 961 行，本任务的实现规格）**
凡 design.md 写「必须」之处不需要回头确认；实现细节级偏差可自行决定但须记入报告。设计级偏差（布局结构、承载形态、文案语义变更）不允许。
背景参考（可选）：`agent-outputs/workflow/provider-center-layout-redesign/p1-taskbook.md`。

## 2. 实现范围（只允许改以下文件；diff 越界即验收不过）

**新增**：
- `frontend/src/components/ui/InfoTip.vue`（§6.1 完整规格：toggletip、Teleport、键盘/Esc/外点关闭、fixed 定位测量）
- `frontend/src/components/ui/FieldLabel.vue`（§4.0.2）
- `frontend/src/components/ui/EditorToolbar.vue`（§4.0.3）

**扩展（向后兼容）**：
- `frontend/src/components/ui/StatusBanner.vue`（compact prop，§4.0.4）
- `frontend/src/components/ui/EmptyState.vue`（description 可选 + `v-if`，§4.0.5；action 插槽已存在）
- `frontend/src/components/provider/ConfigCategoryCard.vue`（code/meta/metaTone/tip/tipItems props，§4.0.6）

**重构（按 §4 逐文件骨架）**：
- `frontend/src/views/ProviderCenterView.vue`（§4.1）
- `frontend/src/components/provider/PresetList.vue`（§4.2）
- `frontend/src/components/provider/PiAmagiConfig.vue`（§4.3）
- `frontend/src/components/provider/OmpGlobalConfig.vue`（§4.4）
- `frontend/src/components/provider/OpenCodePresets.vue`（§4.5）
- `frontend/src/components/provider/OpenCodeGlobalConfig.vue`（§4.6）
- `frontend/src/components/provider/ProviderRegistryEditor.vue`（§4.7）
- `frontend/src/components/provider/AuthConfigEditor.vue`（§4.8）
- `frontend/src/styles/tokens.css`（仅按 §3.4 追加 3 个 token，附注释「provider-center-layout-redesign」）

**产物**：
- `agent-outputs/workflow/provider-center-layout-redesign/impl-report.md`（实现报告，模板见 §6）
- `agent-outputs/workflow/provider-center-layout-redesign/screenshots/`（视觉证据，命名 `{view}-{state}.png`）

**禁改**：`frontend/wailsjs/**`、`frontend/src/api/**`、`frontend/src/stores/**`、两页之外的视图与组件、Go 代码、`package.json`（不新增依赖）、`e2e/**` 与 `frontend/src/__tests__/**`（除非 design §7.2 V2 声明的测试同步——本设计不需要）。icons.ts 仅当卡片图标 key 需要时按 §3.3 选用**已有** key，不新增图标。

## 3. 实现切片（每片独立可构建，按 design §7.6）

- **S0 环境**（当前环境未安装依赖，必须先做）：`npm --prefix frontend install`；V3 需要 `npm install`（根，装 @playwright/test）+ `npx playwright install chromium`（网络受限失败则如实记录，V3 标 blocked-environment，不得伪造结果）。
- **S1 基件**：§4.0 全部（3 新 + 3 扩展 + tokens）。
- **S2 容器与预设**：§4.1 ProviderCenterView + §4.2 PresetList。
- **S3 Pi Agent 配置**：§4.3；完成后**立即**跑 V3（e2e 契约早验）。
- **S4 OMP / 注册表 / 认证**：§4.4、§4.7、§4.8。
- **S5 OpenCode ×2**：§4.5、§4.6。
- 收尾：全量 V1/V2/V3 + X 系列。

## 4. 验证要求（证据写入报告：命令、cwd、退出码/结果）

| # | 验证 | 命令（cwd=仓库根） | 层级 |
|---|---|---|---|
| V1 | 类型门+构建 | `npm --prefix frontend run build` | L1 必过 |
| V2 | 逻辑测试 | `npm --prefix frontend run test -- src/__tests__/components/provider` | L1 必过 |
| V3 | Pi 压缩 e2e | `npx playwright test -c e2e/pi-amagi-config.playwright.config.ts` | L2 强烈要求（契约 §7.3） |
| X1–X7 | 视觉与交互 | design §7.4：1280×800 与 800×600 截图 ×9 视图、InfoTip 键盘/裁切、预设行键盘编辑、皮肤模式 | L2 |

X 系列执行方式：`npm --prefix frontend run dev`（后台）后用 `agent-browser` CLI（先 `agent-browser --version` 探测可用性；v0.38.2 已在 PATH）。数据接口在纯 vite 下会失败/为空——用 e2e 夹具同款思路：在页面加载前注入 `window.go` mock（参照 `e2e/pi-amagi-config.html` 的 mock 结构）再访问 hash 路由 `#/provider`（以 `frontend/src/router/index.ts` 实际路由为准，先读再定）。无法注入 mock 时至少覆盖空态/错误态截图并如实标注覆盖缺口。截图存 `screenshots/`。
若你无法读图做视觉判断：照常截图存盘，报告标注「截图未自读，待 Leader 读图复核」——**不得**声称已视觉验证。

## 5. 硬约束（违反即返工）

- design §1.3 不变量 I1（语义承载不丢）/ I2（行为不回退：懒加载、presetLoadKey、engine watch、远程横幅、导入导出、handleSave 链路）/ I3（e2e 契约 §7.3 + 4 个 vitest）。
- design §7.2 S1–S8 结构自查清单逐项核对并写入报告。
- 中文 UI；只用既有 token（新增仅 §3.4 三项）；不引入新依赖。
- 完整状态覆盖：每视图至少 loading/error/empty/有数据四态中可触发者（vitest 不覆盖组件渲染，靠截图+e2e 补）。

## 6. impl-report.md 模板

1. 改动文件清单（含每文件一句话改动摘要）
2. T01–T57 落地核对表（design §5.2，逐条 ✓/偏差说明）
3. V1/V2/V3 证据（命令+退出码）；X1–X7 结果与截图路径
4. design §7.2 S1–S8 自查
5. 偏差与决策记录（实现细节级）
6. 回滚说明（按切片逆序 revert）
7. 覆盖缺口与遗留（如有）
