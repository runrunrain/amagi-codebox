# P1V4 设计修订任务 — 内容整理 + 二三级关系重设计 + 左栏纯净化（v4）

角色：fuxi（设计态）。产物：`agent-outputs/workflow/provider-center-layout-redesign/design-v4.md`（增量规格，未覆盖处按 v3/v2/v1 执行）。写入边界：仅该文件。

## 1. 主上新指令（唯一动因，原文三条）

> ① 页面内容部分，可以对内容进行整理，避免所有内容的杂乱呈现，矩形图标收拢内容有时候比列表更好
> ② agent配置，模型提供商，认证登录和pi，omp，opencode在同一层级，并且分离显示，让我难以理解其关系，需要重新设计显示逻辑
> ③ 上下文压缩的左侧bar不该有说明文字显示在其中

## 2. Leader 解读（fuxi 确认或修正，逐条裁决）

**① 内容整理 + 图标矩形收拢**：针对右侧分区内容的呈现质量。当前形态盘点（实读为准）：
- Pi Agent 各分区：角色覆盖=行列表；压缩=三态+选择器+按钮；MCP=三组 StringListEditor/行表；并发=三组行表。v1 曾用 ConfigCategoryCard（Finder 缩略图风）收拢，v2/v3 改无框分区后部分内容呈「平铺列表」观感，字段多时显杂。
- Registry：提供商卡内字段网格 + 模型行组 + 未知字段列表；Auth：条目表单 + 递归值编辑器；OpenCode 全局：各类 Map 编辑器的记录列表。
- 主上用语「**有时候**比列表更好」→ 不是全面回退卡片平铺，而是给出**适用判据 + 具体应用点**：哪些内容块适合用带图标的矩形容器收拢（如 MCP 三个子段、并发三个子段、Registry 的模型条目、Auth 的凭据类型块？），哪些保持列表（如预设列表——主上上轮已认可分组列表）。
- 注意约束：分区仍是右栏单显 + 左栏切换（主上未推翻 v3 骨架）；「收拢」是分区**内部**的分组呈现手段，不是恢复整页卡平铺。icons.ts 已有语义图标库（ConfigCategoryCard 在用）。

**② 二三级关系重设计**：现状 v3 = 同一行：左 underline（Pi·OMP·OpenCode）+ 右 pill（Agent 配置·模型提供商·认证登录），主上反馈「同一层级且分离显示，难以理解从属关系」。候选方向（fuxi 权衡后定，或提出更优）：
- N-a 两行紧贴：二级 underline 一行，其正下方三级 pill 一行（左对齐、视觉缩进/连接），父子关系靠垂直紧贴 + 缩进表达；
- N-b 分组标签：三级 pill 组前加所属 CLI 标签（「Pi ·」前缀或分组框），一行但关系显式；
- N-c 嵌套 tab：三级 pill 直接挂在选中二级 Tab 下方（类似浏览器/VS Code 设置的 tab-in-tab）；
- N-d 二级下拉/分页 + 三级页签；
- 判据：从属关系一眼可读、800px 可用、与左栏/工具栏的层级不混淆、实现代价（ProviderCenterView 单文件 vs 动编辑器）、e2e 影响（顶侧导航不在 e2e 契约内）。
- 预设页两格式 Tab 与「用于 …」caption 的关系同步检查。

**③ 左栏纯净化**：SectionNav 条目只保留名称（+计数徽章？fuxi 裁决计数是否保留——计数是状态非说明文字，倾向保留）。现挂条目上的次级文字全部迁移：
- 「下次压缩生效」meta（压缩条目）→ 右侧 EditorSection 标题行已有同名 meta，左栏侧删除即可；
- 「受管」warning meta（Registry amagi-* 条目）→ 右侧标题行已有 meta + 常显警示横幅，左栏侧删除；
- Auth 条目类型标记（api_key/oauth）→ 迁移方案由 fuxi 定（右侧标题行加类型徽章？已有类型 Badge 在正文，可能直接删左栏侧）；
- 「需修正」invalid 标记 → 保留与否（错误状态指示 vs 说明文字）由 fuxi 裁决（e2e 契约依赖？检查 spec 是否断言 invalid 标记——据知只断言按钮名与分区 region）。
- SectionNav 的 notice/aria 描述不受影响（可访问性描述保留，仅去可视说明文字）。

## 3. 现场基线

HEAD=8079acb（v1.3.97 发布态），工作树干净。必读：
- `design-v3.md`（当前形态规格）+ `impl-report.md` §9（v3 实测数据：348px/349px/工具栏行数）
- `frontend/src/views/ProviderCenterView.vue`（subnav 现状）
- `frontend/src/components/ui/SectionNav.vue`（meta/metaTone/invalid 渲染现状）
- `frontend/src/components/provider/{PiAmagiConfig,ProviderRegistryEditor,AuthConfigEditor,OmpGlobalConfig,OpenCodeGlobalConfig}.vue`（分区内容呈现现状 + navigationItems 的 meta 构造）
- `frontend/src/components/provider/icons.ts`、`ConfigCategoryCard.vue`（图标库与矩形收拢的既有形态参考）
- `frontend/src/components/ui/{Segmented,EditorSection,EditorToolbar,FieldLabel}.vue`
- e2e/pi-amagi-config.spec.ts（契约不变量：导航按钮精确名「上下文压缩」、`.editor-section[data-section]`、`.mss-dd` 顺序、region/标题）

## 4. 硬约束（继承 v3，增量如下）

- I3 增量：e2e 契约零破坏——注意左栏条目名、分区 id、按钮可访问名不变；若②的方案改变顶侧 DOM，顶侧不在 e2e 契约内但 X 系列 harness 需适配。
- 新增 I6：左栏条目可视层只允许「名称 + 计数（若保留）」，无任何说明/状态文字（若 fuxi 裁决保留 invalid 指示，须论证它属状态指示而非说明文字且形态极简）。
- 新增 I7：①的收拢不恢复整页卡片平铺；分区单显 + 左栏骨架不变（除非主上指令推翻——本指令未推翻）。
- 800px 预算沿用 v3 实测修正（348px/滚动条 8px 占布局）；纵向预算注意②若增行，需给出 800×600 的 firstContentTop 估算（v3 实测 349 为基线）。
- C1 休眠代码继续保留（主上仍在迭代布局）。

## 5. design-v4.md 交付结构

1. 三条指令解读裁决（§2 逐项 ✓/修正；②选定方案 + 否决理由）
2. 目标结构（顶侧导航新形态 + 分区内收拢形态总览）
3. 逐文件增量指令（预计：ProviderCenterView + SectionNav + 若干编辑器的分区内呈现 + icons 引用；明确每文件改什么）
4. 内容收拢适用判据 + 应用点清单（哪些块收拢/哪些保持列表，逐块列出）
5. 左栏纯净化迁移表（每条 meta → 迁移落点）
6. 宽度/纵向规格（800×600 预算复核）
7. 验收增量（V/X/S 变化点；harness v4 适配要点）
8. 风险与回退（单文件/多文件粒度）

## 6. 验收（Leader 依据）

- 三条指令每条有明确设计落点与可执行规格；②有候选对比与裁决理由；
- 左栏净化后 I1 语义表逐项仍有常显承载（右侧落点核对）；
- luoshen 可不回头问设计问题地实现。
