# P1V3 设计修订任务 — 顶侧页签恢复 + 左栏范围收缩（v3）

角色：fuxi（设计态）。产物：`agent-outputs/workflow/provider-center-layout-redesign/design-v3.md`（增量规格，引用 v2 不重写不受影响部分）。写入边界：仅该文件。

## 1. 主上新指令（唯一动因，原文）

> 也不能全部转为左侧bar，如：
> 如 cli独立配置页的pi，omp的分页
> agent配置，模型提供商，认证登录的分页
> 以上还是保留顶侧页签的形式
> 左侧bar只承接原有的页内的卡片式平铺的功能项，如角色模型，上下文压缩配置，mcp路由

## 2. 指令解读（Leader 预裁，fuxi 落规格时确认或修正）

**回退到顶侧页签**：
- CLI 独立配置页二级：Pi | OMP | OpenCode（v1 subnav 的 underline 形态）
- 三级模式：Agent 配置 | 模型提供商 | 认证登录（Pi/OMP）、预设管理 | 全局配置（OpenCode）——顶侧 pill

**左栏收缩为编辑器内部分区导航**（原卡片平铺项的承接者）：
- Pi Agent 配置：角色模型 / 上下文压缩 / MCP 路由 / 并发限制
- OMP Agent 配置：角色模型 / 子代理覆盖
- OpenCode 全局配置：提供商 / Agent / MCP 服务器 / 权限 / 指令文件 / 插件 / 实验特性 /（顶层模型、其他键条件项）
- 模型提供商（Registry）：动态提供商列表 + 其他顶层键 + 添加提供商
- 认证登录（Auth）：动态凭据条目 + 添加 API Key

**预设页**：按指令精神（左栏只承接卡片平铺项，预设页无此项）恢复 v1 形态——顶侧二级 Tab（Anthropic 格式 | OpenAI 格式）+ 右侧分组列表，无左栏。

## 3. 现场事实（v2 已实现并发布 v1.3.96，这是在其上的修订）

- `views/ProviderCenterView.vue`：v2 页面级 SectionNav（CLI 组→模式→分区子叶整树）+ `.pc-workspace` grid + navOwner/desiredSection/selectNavigation 机制。
- **五个编辑器已内置 `navigation?: 'external' | 'local'` 双模式**（v2 §2.4）：`local`（默认）= 编辑器自带 SectionNav + 局部 grid（`*.editor-local-layout`）；`external` = 只输出 `.editor-detail` 由父级包。**e2e 夹具 `pi-amagi-config.html` 挂的就是 `navigation:'local'`，7 用例全绿——本次修订对 e2e 生产契约几乎零影响**。
- 编辑器内部机制全部保留可用：EditorSection、visited+v-show 草稿、动态叶子（Registry/Auth）、sectionVisibility 浮层收起、窄窗 <480px 规则。
- EditorToolbar（路径胶囊/模式切换/保存）在编辑器内，不受导航层级影响。
- v1 的 subnav 实现可从 `v1-snapshot/tracked.patch` 的 ProviderCenterView 段参考（二级 underline + 三级 pill 同行 + 通栏分隔线 + 右侧 caption）。

## 4. 需要落定的规格点

1. **ProviderCenterView 增量改动**：删页面级 SectionNav/navGroups/selectNavigation/navOwner/desiredSection 机制；恢复 v1 式 pc-subnav（二级 underline + 三级 pill 同行；预设页二级 + 格式 caption）。三级 pill 与 subnav 的布局关系（v1 同行右置）。engine/mode 的 computed setter 恢复直接绑定。
2. **编辑器调用**：五编辑器改回 `navigation='local'`（或不传=默认 local），删除 external 模式分支与 `navigation-change` 监听、section-id/nav-owner 透传（若 v3 后无消费者，external 模式代码可删或保留——给出取舍：删则组件更净，保留则机制可复用；考虑最小 diff 与回退性）。`PresetLoadError` 的 mounted owner 校验/静态分区报告在 local 模式下是否还需要（编辑器内部自持）。
3. **`configNavigation.ts`**：页面级静态表若只剩编辑器自用（local 模式的分区表已在编辑器内引用它），保留范围裁定。
4. **宽度预算重算**：恢复 subnav 一行后，800px 档的内容区 = 432px（v1 数字） − 左栏 112 − gap 12 ≈ 308px？v2 曾靠删 subnav 行获得 340px。fuxi 需重算并给出 v3 的窄档规格（左栏宽度是否需在编辑器内再收窄，或接受 308px；给出明确数字与断点）。
5. **harness 适配**（`x-harness/v2-*.mjs`，验证工具）：页面级导航 id（`pi:registry` 等 clickId）改为点击 subnav 顶侧按钮；X 系列验收点更新（顶侧两行 chrome 行数、左栏仅编辑器内）。X1「首个内容上方 chrome ≤3 行」在新结构下的口径。
6. **e2e**：`pi-amagi-config.html` 已是 local——确认零改动或最小注释更新；spec 零改动。
7. **语义承载**：v2 §6.1 迁移表中挂在页面级左栏的项（如「下次压缩生效」的左栏次级 meta、受管标记）在 local 左栏中保留方式（编辑器内已有 meta 通道，确认无丢失）。
8. **回退路径**：v3 不满意回退 v2 已发布态（v1.3.96 = 当前 HEAD）——这次是干净基线，revert 提交即可。

## 5. 硬约束（继承 v2 §1.3，无新增；I3 的 e2e 部分预期零改动）

## 6. design-v3.md 交付结构

1. 指令解读确认（§2 预裁逐项 ✓/修正）
2. 目标结构图（两页 IA：顶侧 chrome 层级 + 编辑器内左栏）
3. 逐文件增量指令（ProviderCenterView 为主 + 五编辑器的 navigation 调用 + harness）
4. 宽度规格（重算）
5. 验收增量（V/X/S 变化点）
6. 风险与回退

## 7. 输入

- `agent-outputs/workflow/provider-center-layout-redesign/{design.md, design-v2.md, impl-report.md}`
- 当前实现树（git HEAD = v1.3.96 发布态）：`frontend/src/views/ProviderCenterView.vue`、五编辑器、`ui/SectionNav.vue`、`provider/configNavigation.ts`、`x-harness/v2-runtime.mjs`（导航驱动方式）
- `v1-snapshot/tracked.patch`（v1 subnav 参考）
- `e2e/pi-amagi-config.html`
