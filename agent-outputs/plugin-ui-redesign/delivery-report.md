# 扩展中心五引擎插件管理重构 · 交付报告

> 契约：`agent-outputs/plugin-ui-redesign/spec.md`（v1，2026-11-15）
> 基线：master 972b77c。本报告全部结论均经本次读契约、读工作区代码与 git diff、并实际运行质量门禁核实。

---

## 1. 迭代目标与最终形态总览

**目标**：将 `/extensions` 扩展中心的五个引擎插件面板（Claude / Codex / OpenCode / Pi / OMP）从各写一套的旧 UI，统一重构为**浅色 Apple HIG token 下的 ZCode 式插件管理**——借鉴 zcode-plugins 市场的「市场 / 来源 / 版本 / 安装量 / 开发者」信息架构，落地为统一的四段式骨架：**① 诊断横幅（仅出错/警告时出现）→ ② 资源计数标签行（CountTabs + 行尾本地搜索）→ ③ 工具栏（「已安装 N」+ 操作组）→ ④ 内容区（行式条目列表 / 详情 / 市场视图 / 空态）**。

关键形态决策（契约 §1，均已在实现中核实）：

- 视觉全部走 `tokens.css` 浅色 token（`--window/--card/--control/--accent/--separator` 等），无新增依赖、无图标资源文件——所有「图标」由 `PluginIcon` 按插件名确定性生成（字母 + 色相圆角方块）。
- 列表一律行式条目、描述单行截断；计数全部来自 store 真实数据表达式，无写死。
- 修复两处已实证的 `add_market` 事件断链（契约 §0/§9 显式变更 ①②④）：`PluginInstalledPanel`/`PluginMarketPanel` 的 emit 统一为 `'add_market'`、转发监听改 `@add_market`，`AddMarketDialog.vue` 退役（git status 显示 D），添加市场收敛为 ExtensionsView 内联 Dialog 单一入口。
- 首屏 Loading/Error 门控由面板本地 `ref(true)` 改为 **store 派生 computed**，SSR 测试（`renderToString` 不执行 `onMounted`）得以断言主内容分支（各面板顶部注释均有标注）。

改动规模（`git diff HEAD`）：14 个文件，+2297/−3075 行；新增未跟踪目录 `frontend/src/components/extensions/ui/`（11 个共享组件）与 `frontend/src/__tests__/components/extensions/`（19 个测试文件）+ `__tests__/stores/ompPlugin.test.ts`。

---

## 2. 五个引擎面板新布局（契约 §6 × 实际实现）

### 2.1 Claude / Codex（共用 `PluginInstalledPanel.vue`，prop `engine`）

四段式齐备（`PluginInstalledPanel.vue:1-5` 头注释、模板 21-67 行）：

- **CountTabs**：Claude 三标签 `插件 N | MCP M | 市场 K`（`:409-411`，MCP 计数来自 store 新增 `ccMcpPluginCount`）；Codex 两标签 `插件 N | 市场 K`（`:415-416`，无 MCP——数据无 `hasMcp/pluginType` 字段，不造假）。行尾搜索框作用于当前标签。
- **工具栏**：`已安装 N` 计数徽章 + 刷新 + 「浏览插件市场」（描边视觉）+ 「添加 ▾」ActionMenu（菜单项：添加市场 / 手动安装，`:464-465`）。
- **列表态**：按市场分组（GroupHeader 带市场名 + 计数 + 「更新市场」小按钮）的 PluginRow，左列 320px + 右侧详情列双栏（`.detail-layout grid-template-columns: 320px 1fr`，`:1005`）。Codex 行无描述行（无字段）、有 source/重复诊断徽章。
- **MCP 态**（Claude 独有）：按所属插件分组（`mcpGroups`，`:77-85`），PluginIcon + 服务器名 + 状态点如实映射所属插件启用态，空态文案齐全。
- **详情态**：面包屑「插件 › 名称」+ DetailHeader + 描述 + 子项列表 + MetaGrid（`:145-199`）。Codex 子项只读（后端 no-op，文档已同步说明）。
- **市场态**：内嵌 `PluginMarketPanel`（`:33-38`），添加市场经 `@add_market` 转发。

Claude⇄Codex 切换：ExtensionsView 两分支挂载点分别带 `key="claude"` / `key="codex"` 强制重建实例，onMounted 按引擎装载（走查修复，见 §6-2）。

### 2.2 OpenCode（`OpenCodePluginPanel.vue`）

DiagnosticBanner → CountTabs（插件单标签 + 搜索「搜索插件名称或描述」）→ ListToolbar（已安装 N + 刷新 + 安装插件 primary）→ 行式列表（不分组、无开关——有 enabled 字段但无启停 API）→ 详情（面包屑 + DetailHeader + 描述 + **资源汇总徽章行**（`:93-102`）+ 只读 SubItemRow 列表 + MetaGrid）。诊断 errors 来自 store `loadError`（R5a 迁入 store）；首屏门控 store 派生（`:8-9`）。旧 `.scope-note` 说明卡移除，说明文字并入安装对话框。

### 2.3 Pi（`PiPluginPanel.vue`）

DiagnosticBanner → CountTabs（`包 N | 技能 | 扩展 | 提示词 | 主题`，Σ=0 标签隐藏，`:16`）→ ListToolbar（标题+计数 | **目标 Segmented（Windows/WSL/同时应用，保留）** + 刷新 + 安装包）→ 包列表（无开关）→ 详情（DetailHeader + 只读资源列表 + MetaGrid + **切换源卡片**：功能保留、样式已 token 化，`:815` 注释记录原深色 fallback 修复）。资源标签页按所属包分组（GroupHeader=包名+计数，`:167-169`）；搜索 placeholder 按页态切换「搜索包名称或描述 / 搜索资源名称或所属包」（`:320`）。`PiPluginWSLSection` 按契约 §6.4 整体保留未改。

### 2.4 OMP（`OmpPluginPanel.vue`）

DiagnosticBanner → CountTabs（插件单标签 + 搜索）→ ListToolbar（已安装 N + 刷新 + 安装插件）→ **行式列表带行尾启用 Switch**（`:38-50`，`switch-visible` + per-id mutating 禁用，替代原三按钮组）→ 详情（面包屑 + DetailHeader + MetaGrid，无子项区；无二级接口，点行用列表数据渲染）。加载失败双形态按契约 §6.5 落地：首载失败（空列表）整页 ErrorState、有数据后失败走横幅（`:183` 注释）。

### 2.5 市场视图（`PluginMarketPanel.vue`，仅 Claude/Codex）

面包屑「插件 › 插件市场」+ DetailHeader（含「{N} 个可安装」徽章）→ 顶部自带搜索 + 排序（Claude：安装量/名称 A-Z；Codex 仅名称 A-Z）+「添加市场」→ 左窄栏市场源列表（名称 + 一行描述 + 计数徽章 + Codex 删除 ×，删除改走 store `removeCxMarketplace`）+ 右侧插件流行式条目（名称 + 安装量/开发者徽章 + 单行描述 + 行尾「安装」）。已安装判定真实推导（installed 列表比对），命中按钮变禁用「已安装」。安装成功/失败均有 toast 反馈（走查修复，见 §6-3）。

---

## 3. 新增共享组件清单与用途（`frontend/src/components/extensions/ui/`，11 个）

| 组件 | 用途 |
|---|---|
| `CountTabs.vue` | 资源计数标签行：胶囊标签 + 计数徽章 + 行尾搜索框；`hidden` 标签不渲染 |
| `PluginIcon.vue` | 插件名确定性字母 + 五色相圆角方块图标（同文件导出纯函数 `pluginIconStyle`） |
| `PluginRow.vue` | 行式插件条目：图标 + 名称/徽章行 + 单行截断描述 + 行尾可选 Switch |
| `GroupHeader.vue` | 分组小节标题（市场名/包名 + 计数 + actions 插槽） |
| `ListToolbar.vue` | 工具栏：标题 + 灰计数徽章 + actions 插槽（刷新/安装/市场入口） |
| `EmptyStateCard.vue` | 空态卡片：标题/副文 + primary/secondary 双按钮插槽 |
| `DiagnosticBanner.vue` | 诊断横幅：折叠一行「N 个错误，M 个警告」、点击展开逐条；两数组皆空不渲染 |
| `MetaGrid.vue` | 详情两列标签-值网格（值可 mono） |
| `DetailHeader.vue` | 面包屑 + 大图标 + 名称 + 徽章 + 主操作按钮插槽 |
| `SubItemRow.vue` | 详情子项行：类型徽章 + 名称 + 描述 + 可选 Switch（Codex 恒只读） |
| `ActionMenu.vue` | 「添加 ▾」菜单：Teleport 到 body 的受控弹层 |

配套：`pluginViewTypes.ts` 新增 61 行——`extensions/ui` 共享组件类型首建（`PluginBadgeType` 等 Badge type 联合镜像、Codex 详情专有字段 `displayName/longDescription/shortDescription` 等）。`ui/*` 基建零改动（`AppButton.vue` 未动，见 §6-4/§7）。

---

## 4. 新增测试清单（`frontend/src/__tests__/`，20 个文件 142 条用例）

实测命令：`npx vitest run src/__tests__/components/extensions src/__tests__/stores/ompPlugin.test.ts` → **20 files / 142 tests 全部通过**。分布：

| 分组 | 文件（用例数） |
|---|---|
| 共享组件 ui/（11 文件 52 例） | pluginIcon 8 · pluginRow 7 · countTabs 6 · detailHeader 5 · diagnosticBanner 4 · groupHeader 4 · listToolbar 4 · emptyStateCard 4 · subItemRow 4 · metaGrid 3 · actionMenu 3（另 `testUtils.ts` 辅助） |
| 面板 panels/ | pluginInstalledPanel 14 · pluginMarketPanel 7 · piPluginPanel 15 · pluginSubItemsPanel 3 |
| 面板（直挂目录） | openCodePluginPanel 17 · ompPluginPanel 17 |
| store | `components/extensions/stores/pluginStore.test.ts` 6 · `stores/ompPlugin.test.ts` 4 |
| 集成 | `components/extensions/integration/extensionsView.test.ts` 7（含 add_market 链路端到端） |

范式遵守契约 §8：`renderToString` + `createPinia` + vi.mock api 层，无 DOM 测试库；Dialog/弹层经 `ctx.teleports.body` 断言；交互走 `defineExpose` 触发器。目录为 `__tests__/components/extensions/**`（R0 实际落地约定，非契约 §8 字面的 `__tests__/extensions/`，各角色 notes 已声明该偏离并获白名单允许）。

---

## 5. 质量门禁结果（本次全量实跑，均通过）

| 门禁 | 命令 | 结果 |
|---|---|---|
| Lint | `npm --prefix frontend run lint`（eslint .） | **通过**：exit 0，零输出（无 error / 无 warning） |
| Test | `npm --prefix frontend run test`（vitest run） | **通过**：exit 0，**67 个测试文件 / 598 条用例全部通过**（3.97s） |
| Build | `npm --prefix frontend run build`（vue-tsc --noEmit && vite build） | **通过**：exit 0，类型检查零错误，vite 构建 `✓ built in 840ms` |

> 说明：契约 §8 给出的 Windows 命令形态 `cmd /c npm --prefix frontend run lint` 在本机 Git Bash 下未正确执行（打印了交互式 cmd 横幅），改用等价的 `npm --prefix frontend run <script>` 直跑，三者均为全仓全量运行，非抽样。角色交付时按编排层要求未跑全量（各 notes 已声明），本报告的全量结果为交付前最终核实。角色 notes 中提到的「vue-tsc 全仓仍有错误（其他并行角色在途文件）」在当前工作树已不存在——build 门禁含 `vue-tsc --noEmit` 且通过。

---

## 6. 走查发现与处置（7 项：3 medium 已修复，4 low 遗留/接受偏离）

### ① medium · Badge `type='tag'` 白字无底色，浅色背景上不可见 — **已修复**

原发现：市场视图「{N} 个可安装」、OpenCode 资源汇总、Pi「pinned」三处徽章用 tag 类型（`color:#fff` 无背景），用户实际看不到。
处置核实：三处均已改为带底色类型——`PluginMarketPanel.vue:18` 用 `type:'scope'`；`OpenCodePluginPanel.vue:95-101` 用 `type="scope"`（`:94` 注释记录原因）；`PiPluginPanel.vue:558/570` pinned 徽章用 `type:'scope'`（`:552` 注释记录原因）。

### ② medium · Claude⇄Codex 直切后目标引擎数据永不加载（实例复用，onMounted 不再执行）— **已修复**

处置核实：`ExtensionsView.vue:40-46/75-79` 两分支的 `PluginInstalledPanel` 分别加 `key="claude"` / `key="codex"` 强制重建实例（`:40-42` 注释记录该走查修复）；`PluginInstalledPanel.vue:949-951` 原「引擎切换时 ExtensionsView 重建面板实例」的错误注释已改为如实描述 key 重建机制；`watch(() => props.engine)` 仍负责重置 resourceTab/searchText（`:359-365`）。

### ③ medium · 市场安装失败静默无提示 — **已修复**

处置核实：`PluginMarketPanel.vue:384-399` `handleInstall` 成功侧 `toastSuccess`、catch 侧 `toastError('安装失败：…')`（`:395-398` 注释标明「走查修复」，与 OpenCode 面板对齐）。

### ④ low · 契约 §4「AppButton 新增 outline variant」未执行 — **接受偏离（记录在案）**

核实：`ui/AppButton.vue:15` 仍为 `'primary' | 'ghost' | 'icon' | 'danger'`，git status 确认该文件未改（ui/* 不在本 run 各角色白名单）。描边视觉由 `PluginInstalledPanel.vue:993-1000`（`.btn-outline-market`）与 `PluginMarketPanel.vue:481` 起（`.sort-btn`）的 scoped class 等价实现，视觉等价、功能无损害；两处实现重复。建议后续按契约补 variant 并收敛。

### ⑤ low · 硬编码色值绕过 token — **遗留未处置**

核实：`PluginMarketPanel.vue:524` 仍 `border-color:#c5c5cc`（发现时行号 516，因 §6-③ 修复插入代码后移至 524）；`DiagnosticBanner.vue:82/86/111/116` 的 `#a02620/#9a6200` 与 `PluginInstalledPanel.vue:1068/1074` 的 `#9a6200` 仍在（后者系契约 §4#7 明示沿用 StatusBanner 配色）。建议改用 `var(--controlHover)` 与 `var(--danger-strong)/var(--warning-strong)`。

### ⑥ low · Pi「本地路径」placeholder 含开发者个人目录 — **遗留未处置**

核实：`PiPluginPanel.vue:474` 仍为「例：/Users/maorun/maorun-workpace/amagi-pi」（旧版逐字保留，非本次引入）。建议改中性示例。

### ⑦ low · `pluginTypeLabel` 的 codex 分支为死代码 — **遗留未处置**

核实：`PluginInstalledPanel.vue:602` 仍 `if (props.engine === 'codex') return 'Plugin';`（该函数仅 claude 路径调用）。建议删除该 early-return。

---

## 7. 遗留事项与建议

**未落地/偏离项（来自各引擎角色 notes，经本次核实分类）**：

1. **AppButton outline variant（同 §6-④）**：契约 §3.3/风险 9 的 R0 项未落地，两面板以透传 class + scoped 样式等价实现。建议下一迭代补 `variant:'outline'` 并删除两处 scoped 覆盖，或在契约层面正式记录豁免。
2. **MCP 条目图标色相（Codex 角色偏离 2）**：契约 §6.1.1「PluginIcon(type 色相 mcp)」与 §4#2 确定性五色算法冲突，实现取 `PluginIcon(serverName)`、状态点如实映射插件启用态——合理偏离，建议回写契约。
3. **PluginRow 来源徽章 title 悬浮提示（Pi 角色偏离 1）**：共享组件 API 无 title 透传（extensions/ui 非该角色白名单），以既有 `shortenSource` 文本截断落地。建议 R0 补 title 透传。
4. **store 归一化归属（Codex 角色偏离 3）**：§7 归一化直接落在 `plugin.ts`（R5a 未合入避免编译失败），并顺带补导出 `cxActivePluginDetail`。最终态已以 store 为准，符合 §7 交付顺序约束的精神。
5. **§6-⑤⑥⑦ 三处 low 遗留**：硬编码色值（`#c5c5cc`/`#a02620`/`#9a6200`）、Pi 个人目录 placeholder、pluginTypeLabel 死代码——均为小改动，建议随手清理。
6. **契约自身待回写项**：§3.1 搜索 placeholder 表未列 OpenCode/OMP 行（实现均类推采用「搜索插件名称或描述」，已核实 `OpenCodePluginPanel.vue:18`、`OmpPluginPanel.vue:20`）；§8 测试目录字面 `__tests__/extensions/` 与实际 `__tests__/components/extensions/**` 不一致；Pi 工具栏标题在资源页动态显示资源名+计数（契约仅钉死包页「已安装 N」）；市场态隐藏 CountTabs 搜索框（市场视图自带顶部搜索，避免双搜索框）；OMP `enabledCount` 已落 store 但面板无消费位（契约标注可选）。以上建议随契约 v2 回写固化。
7. **既有沿袭缺陷（本 run 范围外）**：§6-② 修复前同型问题在旧版即存在（git show HEAD 核实）；`cxEnabledCount` getter 保留但新布局无消费位（契约 §6.2 明示如实收窄）。

**建议优先级**：§6-⑤⑥⑦ 与 placeholder 属一次小 PR 可清完；AppButton outline + PluginRow title 属共享件升级，建议独立 R0' 迭代并回归 52 条 ui 测试。

---

## 附 · 核实方法

- 契约通读：`agent-outputs/plugin-ui-redesign/spec.md`（338 行全文）。
- 实现核对：五面板模板/脚本关键行、`extensions/ui/` 11 组件清单、四 store diff（`git diff HEAD --numstat`：plugin.ts +41/−102、piPlugin.ts +19、opencodePlugin.ts +8、ompPlugin.ts +5）、`AddMarketDialog.vue` 删除、`docs/user/plugins.md` 两处更新（五引擎面板形态说明 + Codex 子项只读说明）。
- 门禁：§5 三条命令全量实跑（Git Bash 直跑 npm script，见 §5 说明）。
- 走查处置：7 项发现逐条按原引用行号在当前工作树复查（部分行号因修复插入代码而偏移，报告内已标注实际行号）。
