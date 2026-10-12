# P1R 设计修订任务 — 左侧栏导航布局（v2 修订）

角色：fuxi（设计态）。产物：`agent-outputs/workflow/provider-center-layout-redesign/design-v2.md`（修订设计，增量引用 v1，不重写已定且不受影响的部分）。写入边界：仅该文件。

## 1. 主上新指令（本次修订的唯一动因）

> 各个子页中不要是卡片平铺式，而应该是左侧bar切换显示不同子页签的内容

对 v1 方案的直接影响：v1 §4.3–§4.8 中各编辑器内容采用 **ConfigCategoryCard 折叠卡纵向堆叠（卡片平铺）**——该形态被否决。两页整体需改为**左侧栏（bar）+ 右侧内容区**的切换式布局。

## 2. 现场事实（必须基于此设计，不是基于 v1 设计时假设的旧代码）

v1 方案**已由 luoshen 基本实现并构建通过**（`npm --prefix frontend run build` exit=0，2026-10-11 核实）：
- 已新增基件：`ui/InfoTip.vue`（toggletip）、`ui/FieldLabel.vue`、`ui/EditorToolbar.vue`（路径胶囊/合法状态/生效标签/模式切换/保存）；`StatusBanner` compact、`EmptyState` 可选 description、`ConfigCategoryCard` 扩展 props（code/meta/metaTone/tip/tipItems）均已落地。
- ProviderCenterView 已改为 pc-subnav（二级 underline + 三级紧凑 pill 同行）；五个编辑器已删 h2/subtitle/hint 段落并接入 EditorToolbar；PresetList/OpenCodePresets 已改分组列表行。
- T01–T57 文案处置已落地；InfoTip 键盘交互、截图证据 50 张已产出（`agent-outputs/workflow/provider-center-layout-redesign/screenshots/`）。
- **v2 修订是在已实现树上的增量重构**：不推翻已完成且与新方向不冲突的部分（文案降级矩阵、InfoTip、EditorToolbar、预设分组列表、e2e 修复责任等继续有效）。

## 3. 需要你解决的核心设计问题

### 3.1 「子页签」进左栏的层级归并（最重要）

现状导航三层：一级 pill（服务提供商/预设/CLI 独立配置）→ 二级 underline（Anthropic/OpenAI 格式；Pi/OMP/OpenCode）→ 三级 pill（Agent 配置/模型提供商/认证登录；预设管理/全局配置）。编辑器内部再叠分区卡（如 Pi 的 角色模型/上下文压缩/MCP 路由/并发限制）。

主上的「子页签」两种读法都可能：
- 读法 A：二级+三级水平 Tab 进左栏（页面级 master-detail）；
- 读法 B：编辑器内部分区卡进左栏（编辑器级 master-detail）。

**你要给出一个统一模型**（可以是归并两者的单层左栏，如 VS Code 设置页：左栏分组条目 = 三级模式 + 编辑器分区拍平，或分组两级缩进），判据：
1. 左栏同时容纳 CLI 独立配置页全部可达叶子（最坏情形：3 CLI × 最多 3 模式 + Pi 编辑器 4 分区 + OpenCode 全局 8 分区）时的信息量与可扫读性；
2. 预设页（仅 2 个格式 Tab）是否也上左栏、还是维持 v1 subnav（两个条目不值得左栏时可论证豁免——但需主上可读的理由）；
3. 切换深度：任何叶子 ≤2 次点击；
4. 编辑器内部分区在右栏内的呈现（一次全显滚动 vs 左栏切换单显）与「不要卡片平铺」的关系——若分区进左栏则右栏单分区平铺表单；若分区保留在右栏，须论证其为何不算「卡片平铺」（例如改为无卡框的分区标题+表单段）。

### 3.2 最小窗口 800px 适配（v1 §2.1-C 的否决理由已被主上指令覆盖，但约束本身仍在）

内容区可用宽度约 432px（800 − 侧栏 264 − 页边距 72 − 卡片内边距 32）。v1 曾据此否决 170px 侧栏方案。v2 必须正面解决：
- 左栏宽度预算与形态（全宽文字 / 图标+短名 / 窄窗图标化折叠 / 窄窗降级为下拉或回退 v1 subnav——你来定，给出断点与各断点下的具体尺寸）；
- 常规窗口（1280+）与最小窗口各给一档明确规格；
- `main.go` 最小宽度改动**不在本轮范围**（Go 层不动）。

### 3.3 e2e 契约适配（v1 §7.3 的处理方式需要修订）

`e2e/pi-amagi-config.spec.ts` 依赖 `.config-cat-card` 内含 `/上下文压缩/` 按钮、卡内 dropdown/input 顺序契约。分区若进左栏/去卡化，该契约必然变化。v2 需给出：
- 新 DOM 契约（右栏分区容器类名、标题可定位方式、字段顺序不变量）；
- 对应 e2e spec 的最小改动清单（本轮允许改 `e2e/pi-amagi-config.spec.ts` 与 `e2e/pi-amagi-config.html`，改动必须是「适配新结构」而非「放松断言」——每处改动列明旧→新）；
- `.config-cat-card` 是否保留给未受影响的场景（若左栏化后无人使用折叠形态，可声明该组件本轮保留不删、仅停止在两页使用——避免扩大爆炸半径）。

### 3.4 各编辑器分区清单（左栏条目的实际数据）

- Pi Agent 配置：角色模型（含分层策略）/ 上下文压缩 / MCP 路由（Leader、默认、角色附加三个子段，是否再拆？）/ 并发限制（default/providers/models 三子段）
- OMP Agent 配置：角色模型 / 子代理覆盖
- Pi/OMP 模型提供商（ProviderRegistryEditor）：**动态 N 个提供商**——左栏动态列表（提供商名 + 受管标记）+ 右栏单提供商编辑，是 master-detail 的自然形态；「其他顶层键」的安置；
- Pi 认证登录（AuthConfigEditor）：动态 N 个凭据条目——同上评估；
- OpenCode 预设管理：分组列表（v1 已列表化）——是否维持或也入左栏；
- OpenCode 全局配置：8 个静态分区（顶层模型仅键存在时显示、其他键动态）；
- 预设页 PresetList：动态 N 条预设——维持 v1 分组列表 + 弹窗编辑，还是左栏预设名 + 右栏详情（弹窗改内联编辑是行为变化，需明确论证与成本）。

### 3.5 状态承载迁移

v1 的生效标签/InfoTip/meta 挂载点部分绑在卡片头（ConfigCategoryCard meta/tip props）。分区去卡化后这些语义的挂载点要重新定义（左栏条目上的徽章？右栏分区标题行？EditorToolbar？），§5.3 语义核对表须逐项重新映射，语义不得丢失。

## 4. 硬约束（继承 v1 §1.3/§6，修订处如下）

- I3 修订：e2e 允许按 §3.3 适配性修改，vitest 4 个逻辑测试仍不可破；
- 新增 I4：不引入新依赖；Go/后端/wailsjs/api/stores 不动；
- 新增 I5：左栏切换不得破坏懒加载（defineAsyncComponent + presetLoadKey）与 engine 按需加载；编辑器未加载完成时左栏可用性（左栏若在 ProviderCenterView 层渲染三级条目则不受影响——v1 §2.2-N3 曾以此否决三级进编辑器，注意一致性）；
- 其余（语义不丢、行为不回退、中文 UI、token 延续）不变。

## 5. design-v2.md 交付结构

1. 指令解读与范围裁定（§3.1 的读法裁决 + 预设页是否豁免的结论与理由）
2. 统一左栏导航模型（信息架构树、与现有三级导航的映射、ProviderCenterView 结构改动）
3. 逐文件增量改造指令（相对**当前已实现树**：哪些 v1 产物保留/复用/删除；每个文件新 DOM 骨架）
4. 窄窗适配规格（断点、尺寸、降级形态）
5. e2e 契约适配清单（旧→新，逐条）
6. 语义承载迁移表（v1 §5.3 → v2 挂载点）
7. 新增/扩展基件规格（如有；预计需要 SectionNav 类基件——给出键盘可达规格：roving tabindex 或 arrow-key 导航）
8. 验收清单修订（V/X/S 系列的增量）
9. 风险与回滚（含「v2 不满意回退 v1 已实现态」的路径——当前树可直接 diff 回退）

## 6. 输入（必读）

- `agent-outputs/workflow/provider-center-layout-redesign/design.md`（v1 全文）
- `agent-outputs/workflow/provider-center-layout-redesign/p1-taskbook.md`（原始约束）
- 当前实现树（以 `git diff HEAD -- frontend/src` 的实际状态为准，重点读已改造的 9 个文件与 3 个新基件）
- `frontend/src/components/ui/Segmented.vue`、`frontend/src/components/provider/ConfigCategoryCard.vue`（现状）
- `e2e/pi-amagi-config.spec.ts`、`e2e/pi-amagi-config.html`（契约现状）
- `main.go`（仅读窗口尺寸约束）

## 7. 验收（Leader 依据）

- §3.1–§3.5 每个问题有明确结论与理由，不留「实现时再定」；
- 主上指令的两种读法都有显式裁决（不是隐含）；
- 800px 最小窗口有具体可用规格；
- e2e 适配逐条可执行；
- luoshen 可不回头问设计问题地直接实现。
