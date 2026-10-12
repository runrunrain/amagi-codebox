# P1 设计任务 — Provider Center「预设」&「CLI 独立配置」两页布局重设计

角色：fuxi（设计态）。产物：一份可直接指导 luoshen 实现的设计方案文档。
写入边界：**仅** `agent-outputs/workflow/provider-center-layout-redesign/design.md`，不改任何项目代码。

## 1. 背景与问题

主上指出：Provider Center 中「预设」页与「CLI 独立配置」页的前端布局存在**大量说明性文字**，不符合正式应用的前端设计。要求重新设计两个页面的**整体内容布局**。

问题本质（现状证据，均已实地核对）：
- 每个配置子视图自带「h2 标题 + 1–2 句 subtitle 段落」的页头，5 处重复（Pi/OMP/OpenCode 全局/注册表/认证）；
- 几乎每个表单字段下挂 `*-hint` 说明段落（12px 灰字），多处 2–3 句长、含括号嵌套的语义解释；
- 区块标签（zone-label）带解释性尾注；空态/默认卡片文案面向「教学读者」而非「使用者」；
- 页头 subtitle、路径行、工具栏在 5 个编辑器里逐个重复渲染，未形成统一页架。

整体观感：像「配置项说明书」而非「正式产品配置界面」。

## 2. 范围

| 页面 | 入口 | 组件（frontend/src/…） |
|---|---|---|
| 预设页 | ProviderCenterView.vue mainTab='presets' | views/ProviderCenterView.vue（预设区段）、components/provider/PresetList.vue |
| CLI 独立配置页 | mainTab='cliconfigs' | views/ProviderCenterView.vue（CLI 区段）、components/provider/{PiAmagiConfig, OmpGlobalConfig, OpenCodePresets, OpenCodeGlobalConfig, ProviderRegistryEditor, AuthConfigEditor}.vue |

允许触碰共享件：components/provider/ConfigCategoryCard.vue、components/ui/*（仅按需扩展，如新增 tooltip/infotip 基件）；styles/tokens.css（仅按需补充 token）。
禁止：`frontend/wailsjs/**`、api 层（frontend/src/api/*）、stores、两页之外的任何视图、Go 后端。
PageHead（页面级标题+描述）被三个 tab 共享——是否调整其 description 由你论证并给出方案。

## 3. 现状证据清单（逐条给出处置决策）

### 3.1 ProviderCenterView.vue（两级导航容器）
- PageHead description：「统一管理服务提供商与可跨 CLI 复用的公共预设」；
- 预设区 zone-label：「格式预设 · 跨 CLI 复用的公共协议格式」；
- CLI 区 zone-label：「CLI 专属配置 · 各 CLI 专属配置文件，互不共享」；
- 二级 underline Tab（Anthropic/OpenAI 格式；Pi/OMP/OpenCode）+ 各自三级 pill Tab（Agent 配置/模型提供商/认证登录/预设管理/全局配置）。

### 3.2 PresetList.vue（预设页主体）
- 相对克制：工具栏（Provider 筛选 Chip + 添加预设）+ 卡片列表 + 空态。
- 空态描述：「点击右上角『添加预设』创建第一个启动配置」。
- 预设卡片：名称 + provider 标签 + 参数徽章（model/tier/vision/CLI/thinking/effort/T/来源）。

### 3.3 PiAmagiConfig.vue（1367 行，hint 最密集）
- 页头 subtitle：「编辑全局 amagi.json：角色模型、统一压缩模型、MCP 路由与并发限制。模型下拉来自 pi 模型目录；各项生效时间见卡片说明。」
- Profile 卡 hint：「tiered：按 leader/fast/work/expert 分层套用 profiles/tiered.json；inherit：全部继承 pi 默认模型。」
- 压缩卡 hint：「Leader 与 JSON/RPC 子 Agent 共用压缩模型，适用于 /compact、自动压缩与上下文溢出恢复。」+ COMPACTION_MODE_HINTS 三条模式长文案 + 两条长段（「项目 amagi.json 优先于此全局配置；子 Agent 沿用派发者的配置目录。保存后下次压缩读取…模型认证由 pi 管理。」「请选上下文窗口足够的已注册模型。配置、认证、摘要失败或窗口不足会取消本次压缩并保留历史。」）+ 复制 luban 行内提示。
- 角色模型卡 hint：「按角色覆盖模型，优先级高于 profile。角色名与 amagi-pi agents/*.md 对应。」
- MCP 卡：两组三态（leader/default）各带 label 括注、MCP_MODE_HINTS 每态长文案、尾注（「Leader 主进程加载哪些 MCP server（amagi-pi v2.8.2+）：装配时读一次，改动需重启 pi 生效…」「与 mcp.agents 各角色名单取并集；每次派发现读盘，改动即时生效。」）、MCP_EMPTY_TEXT 三条、角色附加服务器行。
- 并发卡：卡级 hint + default 行内长文（「未单独指定 provider 或 model 时的默认并发池大小（正整数；应用默认 10，清空保存则移除键、回落 amagi-pi 内置默认 4）」）+ providers/models 两组 subhint。
- 配置文件路径行 + 复制按钮；JSON 合法/错误条；目录加载失败警告条。

### 3.4 OmpGlobalConfig.vue（592 行）
- 页头 subtitle：「编辑 ~/.omp/agent/config.yml：modelRoles 角色模型与 task 子代理覆盖。模型下拉来自 models.yml 注册表，保存后新会话生效。」
- 两处卡级 hint（「每个 worker 角色绑定一个 provider/model:level spec…」「将子代理映射到 modelRoles 角色…」）。
- 其余同构：路径行、合法/错误条、警告条、可视化/源码切换、保存。

### 3.5 OpenCodePresets.vue（542 行）
- 「本机默认配置」虚线卡片长描述：「读取本地 ~/.config/opencode 配置，未启用受管预设。启动时若未指定预设即使用此项。」
- 空态描述：「在 config.json 的 preset 段中定义预设，或点击右上角『添加预设』」。
- 路径行；JSON 模式空态；搜索框 + 可视化/JSON 切换。

### 3.6 OpenCodeGlobalConfig.vue（720 行）
- 页头 subtitle：「编辑全局 opencode.json 配置文件。修改后保存立即生效。」（该组件曾于 2026-06 经一轮 fuxi 重设计，其设计文档已不在库内，以现状为准）
- ocg-empty-hint 空配置提示；内部为 ConfigCategoryCard 分区编辑器（结构相对成熟）。

### 3.7 ProviderRegistryEditor.vue（1077 行，Pi/OMP 双引擎复用）
- 页头 subtitle：「编辑模型注册表：提供商连接信息与模型元数据。Agent 配置中的 provider/model 下拉即来自此文件，保存后重新进入配置页生效。」
- amagi-* 受管警告：「amagi-* 提供商由 CodeBox 提供商中心同步管理，手工修改可能在下次同步时被覆盖。」（**语义关键，不可裸删**）

### 3.8 AuthConfigEditor.vue（600 行）
- 页头 subtitle：「管理提供商凭据：API Key 可在此直接填写；OAuth 凭据由 pi CLI 登录产生，此处仅展示状态。Agent 配置下拉中以 ✓ 标注已认证的提供商。」
- 空态：「为提供商添加 API Key，或通过 pi CLI 登录 OAuth 提供商。」

## 4. 设计语言现状（必须延续，不得另起炉灶）

- tokens.css：Apple HIG 克制风（--window/--card/--sidebar/--control 表面层；--accent 007AFF；--label/--secondary/--tertiary 三级文字；radius 6/10/14；mono 字体 token）。强语义色 token（*-strong）用于浅底文字。
- ui 基件：AppButton / Segmented（pill 一级、underline 二级两级导航形态）/ ConfigCard / ConfigCategoryCard（Finder 缩略图风折叠卡）/ Chip / Badge / EmptyState / LoadingState / ErrorState / StatusBanner / Dropdown / TextInput / MaskedValue。
- ConfigCategoryCard 已提供折叠分区 + 计数徽章 + 语义图标；这是五个编辑器的主结构。
- UI 文案语言：中文。

## 5. 必须决策的问题（候选方案 + 权衡，写入 design.md）

1. **页架统一**：5 个编辑器各自的「标题+subtitle+工具栏+路径行」重复渲染。候选：上提到 ProviderCenterView 统一页架（各区一次） / 组件内保留但压缩为单行紧凑工具栏（路径转 hover/capsule/角落信息） / 混合。权衡：组件独立性（懒加载 chunk）、双引擎复用（ProviderRegistryEditor 供 Pi/OMP 两处挂载）、实现成本。
2. **说明文字降级矩阵**：对 §3 全部条目逐条给出处置：裸删 / 缩短为 ≤N 字 micro-copy / info-icon + tooltip（键盘可达、可聚焦）/ 折叠进「高级说明」/ 移入空态或首次使用引导 / 保留为字段 placeholder。给出选择判据（如：纯教学性→删；操作后果语义（生效时机、覆盖风险）→保留但降级；结构性解释（三态语义）→就近轻承载）。
3. **zone-label 与 PageHead**：解释性尾注去留；PageHead description 三 tab 共享的处理。
4. **预设页布局**：PresetList 是否需要列表态/网格/密度升级，还是仅微调（对照「正式应用」标杆）。
5. **两级/三级导航**：presets 与 cliconfigs 两区的一二级 tab + 各编辑器三级 tab 层级呈现是否优化（命名、形态、与页架的合并机会）。
6. **新基件**：若引入 tooltip/infotip 等新 ui 基件，给出规格（触发方式、焦点可达、深浅色 token、最大宽度）。

## 6. 硬约束

- **语义不丢**：以下语义承载不可消失，只可降级形态——生效时机（重启生效/新会话生效/即时生效/下次压缩读取）、amagi-* 受管覆盖警告、MCP 三态（未设置=全部 / []=不加载 / 白名单）与「保存为空列表即不加载」语义、并发 default 清空保存移除键回退内置默认、OAuth 凭据只读由 pi CLI 管理、压缩模型窗口不足会取消压缩。降级后的承载形态必须可被键盘访问（tooltip 若仅 hover 不可接受则说明替代）。
- **行为不回退**：懒加载（defineAsyncComponent + presetLoadKey 重挂载）、engine 按需加载 watch、远程模式横幅（StatusBanner/RemoteScopeBanner）、导入导出/配置档按钮、各编辑器保存链路（不改 api 层）全部保持。
- 既有 vitest 套件（frontend/src/__tests__/components/provider/{mcpListMode,piCompaction,piConcurrency,useModelCatalog}.test.ts）不得破坏；若文案常量迁移导致测试需同步调整，在方案中列出（实现者执行）。
- 中文 UI；延续 tokens 与既有基件形态；不引入新依赖包。
- 可验证性：方案落地后需可跑 `npm --prefix frontend run build`（vue-tsc 门）与 vitest 验证。

## 7. design.md 交付结构（必需节）

1. 问题定义与设计原则（「正式应用」的可操作判据：信息密度、一次聚焦、渐进披露…）
2. 候选方案与权衡（≥2 个整体布局候选 + 选定理由；含可逆性与代价）
3. 选定方案：两页信息架构图（区块→组件→状态）与视觉规格（层级、间距、字号、徽章/图标用法）
4. **逐文件改造指令**（§3 全部 8 个文件 + 触碰的共享件；每文件列出：删除/新增/改动结构、新布局 DOM 骨架示意、需要的新样式 token）
5. **说明文字处置表**（§3 每一条 → 处置方式 + 处置后文案；降级矩阵判据）
6. 新基件规格（若有）
7. 验收清单（映射到 workflow 总验收，供 luoshen 自验与 diting 审查）
8. 风险与回滚说明

## 8. 输入文件（必读，实地读全）

- frontend/src/views/ProviderCenterView.vue
- frontend/src/components/provider/{PresetList, PiAmagiConfig, OmpGlobalConfig, OpenCodePresets, OpenCodeGlobalConfig, ProviderRegistryEditor, AuthConfigEditor, ConfigCategoryCard}.vue
- frontend/src/components/ui/（至少 Segmented.vue、PageHead.vue、EmptyState.vue、StatusBanner.vue、ConfigCard.vue、AppButton.vue）
- frontend/src/styles/tokens.css
- demo/前端重构交接说明.md §5.3（原始 IA 意图）
- frontend/src/__tests__/components/provider/（4 个逻辑测试，了解不可破契约）

## 9. 验收（Leader 依据）

- §7 全节齐备；§3 清单 100% 覆盖处置；两页全部子视图（presets×2 格式 + cliconfigs×3 CLI×5 编辑器）均有布局指令；
- 硬约束 §6 每条在方案中有落点；
- luoshen 可不回头问设计问题地直接实现（任务书即规格）。
