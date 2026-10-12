# P2R 实现任务 — design-v2 左栏导航增量实现 + 实现报告（luoshen 接力）

角色：luoshen。主特色：审美判断（UI 实现、完整状态覆盖、视觉验证是交付证据）；附带·轻：工匠闭环（构建/测试自验、实现报告）。

本任务是 P2 的接力（原会话因 429 限流 + 上下文超窗不可续接）。**前一轮 v1 实现已落盘且构建通过**（Leader 核实 `npm --prefix frontend run build` exit=0），当前工作树 = v1 实现态，不是 HEAD。不要重放或盲改已完成工作，先通读现场再动手。

## 0. 现场核对与回滚快照（开工第一步）

1. `git status --short -- frontend/src e2e` + `git diff HEAD --stat -- frontend/src e2e` 确认 v1 写入在位（约 12 文件改动 + 未跟踪 InfoTip/FieldLabel/EditorToolbar + e2e 是否被触碰）。
2. 按 design-v2 §9.2 在 `agent-outputs/workflow/provider-center-layout-redesign/v1-snapshot/` 落回滚快照：`git diff HEAD -- frontend/src e2e` 全文补丁、3 个未跟踪基件完整副本、`git status` 输出。快照只用于归因/回退。
3. 上一轮的视觉验证工具链在 `agent-outputs/workflow/provider-center-layout-redesign/x-harness/`（mock 注入 + 截图脚本），可复用改造，不必重建。

## 1. 必读 artifact（按序全文读，缺失即停止报告）

1. **`agent-outputs/workflow/provider-center-layout-redesign/design-v2.md`（669 行，本轮实现规格）**
2. `agent-outputs/workflow/provider-center-layout-redesign/design.md`（v1，文案处置 T01–T57 与 §5.3 语义表仍有效，被 v2 §6 迁移引用）
3. 当前实现树中已改造的 9 个文件 + 3 个新基件（以实际代码为准）

v2 规格「必须」条款不回头确认；实现细节级偏差自行决定并记入报告；设计级偏差（布局结构、承载形态、文案语义、e2e 断言含义）不允许。

## 2. 本轮工作内容

### 2a. v2 增量实现（design-v2 §2–§7 逐文件指令）

要点（详见规格，此处仅导航）：
- 统一左栏：`ui/SectionNav.vue`（§7.1，roving tabindex、Up/Down/Home/End、Enter/Space 激活）+ `provider/configNavigation.ts`（纯静态导航元数据，不导入 vue/api/store）+ `ui/EditorSection.vue`（§7.2，无卡框分区壳、h3+code+badge+meta+InfoTip、provide 可见性）+ `ui/sectionVisibility.ts`（InjectionKey，InfoTip/Dropdown 隐藏时收浮层）。
- ProviderCenterView：删 pc-subnav，两页改 `.pc-workspace` grid（左 SectionNav + 右 `.pc-detail-content`；列宽/断点按 §4.1）；CLI 7 模式入口由父级渲染（懒加载不回退）；编辑器接 `navigation="external"` + section-id + nav-owner + navigation-change（§2.3/§2.4 映射，同文件切区不调 setPresetEngine、不递增 presetLoadKey）。
- 五个编辑器（Pi/OMP/Registry/Auth/OpenCode 全局）：ConfigCategoryCard 外壳 → EditorSection 分区；visited + v-show 草稿保留（§2.5，禁止 :key=sectionId 卸载）；Registry/Auth 动态条目进左栏（master-detail，§3.5/§3.6 选择后继规则与 uiId 稳定性）；OpenCode 全局内部记录列表去卡化（§3.8 scoped deep 样式清单）；PresetList/OpenCodePresets 保持分组列表仅补收缩样式（§3.2/§3.7）。
- 窄窗适配：§4.2 EditorToolbar 容器查询三档 + §4.3 各编辑器 <480px 单列规则 + `@supports not (container-type: inline-size)` 回退（§9.3）。
- e2e 适配（允许且仅允许改这两个文件）：`e2e/pi-amagi-config.spec.ts` 按 §5.2 旧→新（card→compaction helper、showCompaction、3 处 reload 后导航、新增结构回归用例）；`e2e/pi-amagi-config.html` 挂 `navigation: 'local'`。只适配新 DOM，不放松断言含义。

### 2b. 补齐 impl-report.md（上轮缺失，最终交付）

`agent-outputs/workflow/provider-center-layout-redesign/impl-report.md`，覆盖 v1+v2 两轮：
1. 改动文件清单（每文件一句话；v1/v2 分列）
2. T01–T57 落地核对（v1 design §5.2 + v2 §6 迁移差异）
3. design-v2 §8.2 S1–S12 逐项自查
4. V1–V5 证据（命令+cwd+退出码）：V1 `npm --prefix frontend run build`；V2 `npm --prefix frontend run test -- src/__tests__/components/provider`；V3 `npx playwright test -c e2e/pi-amagi-config.playwright.config.ts`（原 6 用例+新增结构用例）；V4 跨分区草稿保留；V5 懒加载与生命周期（§8.1 执行方式）
5. X1–X10 结果与截图路径（§8.3；截图存 `screenshots/`，v2 相关 `v2-` 前缀，不覆盖 v1 证据）；你上一轮能读图，继续自行读图裁决，不能读的项标注「待 Leader 复核」——不得声称未做的验证
6. 偏差与决策记录（实现细节级）
7. 回滚说明（引用 v1-snapshot，按 design-v2 §9.2 路径）
8. 覆盖缺口与遗留

## 3. 实现切片（每片可构建；Pi 完成立即跑 V3）

基件与 configNavigation → ProviderCenterView+两类预设 → Pi+e2e（立即 V3）→ OMP/Registry/Auth → OpenCode 全局及嵌套列表样式 → 全量 V1–V5 + X 矩阵。

## 4. 硬约束

- design-v2 §1.3：I1 语义不丢（§6.1 表逐项）；I2 行为不回退（lazyPreset、PresetLoadError、7 处 `:key="presetLoadKey"`、watch(mainTab)、engine 按需加载、全部 handleSave 函数体、api 层）；I3 修订（vitest 4 逻辑测试不破 + e2e 仅按 §5 适配）；I4 不加依赖、不动 Go/wailsjs/api/stores/package manifests；I5 懒加载与按需读取（左栏不预加载未选 engine）。
- 中文 UI；token 延续（v2 不改 tokens.css）；不引入新依赖。
- 环境提示：node_modules 前后端已安装、playwright chromium 已就绪（上轮完成）；如环境异常如实记录，不伪造验证结果。
- X8 动态 N 项用隔离 fixture 数据（N=0/1/20），不截真实凭据。
