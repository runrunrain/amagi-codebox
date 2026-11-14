# 扩展中心五引擎插件管理页面 · 设计契约

> 版本：v1（2026-11-15）。施工者只读本文即可动工；本文引用的行号以当前 master（972b77c）为准。
> 范围：仅 `frontend/src/**` 与 `docs/user/plugins.md`。后端 Go / wails 生成物一律不动；字段以现有 API 为准，**不得假设后端新增任何字段**。
> 术语借鉴 zai-org/zcode-plugins 市场（marketplace / category / version / 安装量 / 开发者）；本应用数据无 category 字段，故不引入分类，仅沿用「市场、来源、版本、安装量、开发者（author）」术语。

---

## 0. 现状事实（已核实，施工前不必复读）

| 事实 | 出处 |
|---|---|
| 入口 `/extensions`：主 Segmented（插件管理/环境变量/其他工具）+ 引擎 Segmented（Claude/OpenCode/Codex/Pi/OMP）；添加市场 Dialog 内联在视图层 | `frontend/src/views/ExtensionsView.vue:9-123` |
| Claude 与 Codex 共用面板（prop `engine:'claude'|'codex'`）；**事件现状（已实证断链）**：两面板声明/触发的是 camelCase `emit('addMarket')`（`PluginInstalledPanel.vue:329,844-846`），而 ExtensionsView 监听 `@add_market`（`ExtensionsView.vue:36,68`）。本机实测（仓库 vue，`@vue/compiler-dom` 编译 + `renderToString` 运行时）：模板 `@add_market` 编译为 prop `onAdd_market`；`emit('addMarket')` 只命中 `onAddMarket`、**不命中下划线监听**；仅 `emit('add_market')` 命中。且 `PluginMarketPanel` 声明的 `addMarket` emit 从未触发（其按钮只打开面板内 AddMarketDialog，`PluginMarketPanel.vue:374-377`）→ ExtensionsView 内联添加市场 Dialog **经面板事件当前不可达**。**第二处同类断链（已实证）**：市场视图的转发监听是 `@add-market`（`PluginInstalledPanel.vue:287`），编译为 prop `onAddMarket`，本机实测匹配矩阵：`emit('addMarket')`/`emit('add-market')` 命中 `onAddMarket`、**`emit('add_market')` 不命中**——故 §6.6 收敛为 `emit('add_market')` 时必须同时改此转发监听（§9 显式变更 ④），否则市场视图「添加市场」按钮死链。修复方案见 §3.3/§9 | `frontend/src/components/extensions/PluginInstalledPanel.vue:320-330,844-846,287`、`PluginMarketPanel.vue:196-206,374-377`、`frontend/src/views/ExtensionsView.vue:36,68` |
| 视图类型（大小写双兼容） | `frontend/src/components/extensions/pluginViewTypes.ts` |
| plugin store（claude+codex）：`ccInstalled/ccMarkets/ccAvailable`、`cxInstalled/cxMarkets/cxAvailable/cxWarnings`、`cxDuplicates`、既有计数 computed | `frontend/src/stores/plugin.ts:54-104,159-169` |
| 数据形状：`plugin.InstalledPlugin`（含 `scope/gitCommitSha`）、`plugin.PluginDetail`（含 `subItems/mcpServers/pluginType`）、`codexplugin.CodexPlugin`（**无 scope、无 gitCommitSha**）、`CodexPluginDetail`（含 `manifest.interface.capabilities`）、`AvailablePlugin`（Go 侧含 `installCount`，`internal/plugin/types.go:242`） | `frontend/wailsjs/go/models.ts:302,460,4298,4418` |
| OpenCode/Pi/OMP 独立 store 与 API 形状 | `frontend/src/stores/opencodePlugin.ts:11-103`、`stores/piPlugin.ts:11-111`、`stores/ompPlugin.ts:16-81`、`api/opencodePlugin.ts:16-49`、`api/piPlugin.ts:17-47`、`api/ompPlugin.ts:18-33` |
| Codex 子项禁用是 no-op；OpenCode 无市场无子项；Claude 安装强制 user scope | `docs/user/plugins.md:90,104,180,183` |
| UI 基建与 token | `frontend/src/components/ui/*`、`frontend/src/styles/tokens.css:6-65` |
| 测试范式：`renderToString`（vue/server-renderer）+ `createPinia` + `vi.mock` api 层，**禁新增 DOM 测试库** | `frontend/src/__tests__/components/provider/claudeAccountsFix.test.ts:22-42` |

---

## 1. 设计原则与视觉基线

1. **视觉**：沿用浅色 Apple HIG token（`--window/--card/--control/--accent/--label/--secondary/--tertiary/--separator/--success/--warning/--danger` 等，`tokens.css`）。不新增依赖、不新增 UI 库、不做深色主题。
2. **图标禁令**：不引入任何图片/SVG 资源文件。所有「图标」由 `PluginIcon`（插件名确定性生成字母 + 色相）或文字徽章承担。工具栏按钮用文字按钮（AppButton），不用图形图标。
3. **行式条目**：列表一律为行式（列表内单行高度 ≤ 64px），禁止重卡片堆叠。描述单行截断（`text-overflow: ellipsis; white-space: nowrap`）。
4. **计数真实**：所有用户可见计数必须来自 store 数据表达式（§3 给出），禁止写死。
5. **开关诚实**：只有存在真实启停 API 的引擎才显示 iOS 风格开关（复用 `ui/Switch.vue`）。Codex 子项开关因后端 no-op（`docs/user/plugins.md:104`）一律不显示。
6. **文案**：中文，与现有口吻一致（「已安装 N」「浏览插件市场」「添加市场」「卸载」「更新」等）。

---

## 2. 页面总体骨架

每个引擎面板统一为四段式（纵向）：

```
┌ ① 诊断横幅（DiagnosticBanner，有错误/警告才出现，可折叠）
├ ② 资源计数标签行（CountTabs：胶囊标签 + 计数徽章；行尾=当前资源的搜索框）
├ ③ 工具栏（ListToolbar：分组标题「已安装 N」灰计数徽章 | 右侧操作组）
└ ④ 内容区
     ├ 列表态：按分组渲染 PluginRow 行（分组头 GroupHeader）
     ├ 详情态：面包屑（插件 › 名称，可点击返回）+ DetailHeader + 子项列表 + MetaGrid
     ├ 市场态（仅 Claude/Codex）：面包屑（插件 › 插件市场 › 市场名）+ 市场视图
     └ 空态：EmptyStateCard
```

- 视图状态机：`list ⇄ detail`、`list ⇄ market`（市场引擎才有）、`list ⇄ mcp`（仅 Claude）与 `list ⇄ 资源页`（仅 Pi）。**存储位置**：plugin store 的 `pluginView` 值域保持 `'installed'|'market'` 两值不变（`stores/plugin.ts:51,241-243`，不扩值域）、`activePluginId` 照旧；Claude 的 `mcp` 态与 Pi 的资源标签页态（`skills/extensions/prompts/themes`）为**面板本地 `resourceTab` ref**，面板切到市场标签时同步 `setPluginView('market')` 以保持既有 store 兼容；OpenCode/OMP 仅 `list ⇄ detail`，亦为面板本地 ref。
- 引擎切换时重置到列表态（`setPluginEngine` 已做 `activePluginId=null`，`stores/plugin.ts:235-239`；面板本地 `resourceTab` 需在 `watch(pluginEngine)` 中一并重置）。

---

## 3. 信息架构总表

### 3.1 资源计数标签（CountTabs）

| 引擎 | 标签（label + 计数表达式） | 降级规则 |
|---|---|---|
| **Claude** | `插件 N`：`ccInstalledCount`；`MCP M`：`ccInstalled.filter(p => p.hasMcp \|\| p.pluginType==='mcp').length`（新增 getter `ccMcpPluginCount`）；`市场 K`：`ccMarkets.length` | `hasMcp` 列表级常缺失（`loadCcInstalled` 只补 `pluginType`，`stores/plugin.ts:306-328`），计数以 `pluginType==='mcp'` 为主、`hasMcp` 兜底 |
| **Codex** | `插件 N`：`cxInstalledCount`；`市场 K`：`cxMarkets.length` | **不设 MCP 标签**：`CodexPlugin` 无 `hasMcp/pluginType` 字段（`models.ts:302`），列表级无法推导 |
| **OpenCode** | `插件 N`：`installed.length` | 无市场概念、无列表级资源数据，仅单标签 |
| **Pi** | `包 N`：`installed.length`；`技能 Σskills`：`installed.reduce((s,p)=>s+(p.skills?.length??0),0)`；同式 `扩展 Σextensions`、`提示词 Σprompts`、`主题 Σthemes`（`PiPackage` 自带四个可选数组，`api/piPlugin.ts:32-35`） | 某类总和为 0 时**隐藏该标签**（后端可能未在列表级填充） |
| **OMP** | `插件 N`：`installed.length` | 无市场、无子项数据，仅单标签 |

搜索框（CountTabs 行尾，作用于当前标签，本地过滤，不发后端请求）placeholder：

| 引擎/标签 | placeholder |
|---|---|
| Claude/Codex · 插件 | 「搜索插件名称或描述」 |
| Claude · MCP | 「搜索服务器或所属插件」 |
| 任意 · 市场 | 「搜索名称、描述或开发者」 |
| Pi · 技能/扩展/提示词/主题 | 「搜索资源名称或所属包」 |

### 3.2 列表分组

| 引擎 | 分组键 | 分组头（GroupHeader） |
|---|---|---|
| Claude / Codex | `plugin.marketplace \|\| 'Unknown'`（沿用 `groupedPlugins` 逻辑，`PluginInstalledPanel.vue:463-479`） | 市场名 + 计数徽章 + 「更新市场」小按钮（Unknown 组不显示按钮） |
| OpenCode | 不分组（单组） | 无 |
| Pi | 不分组（单组）；技能/扩展/提示词/主题资源页按**所属包**分组 | 资源页分组头=包名 + 计数 |
| OMP | 不分组（单组） | 无 |

### 3.3 工具栏操作组（ListToolbar actions，自左向右）

| 操作 | 形态 | 可见条件 | 行为 |
|---|---|---|---|
| 刷新 | AppButton ghost small，文字「刷新/刷新中…」 | 全部引擎 | 强制 reload（见 §6 各引擎） |
| 浏览插件市场 | AppButton **outline** small（描边） | 仅 Claude / Codex | 切到「市场」标签（`setPluginView('market')`） |
| 添加 ▾ | **ActionMenu**（§4 #11，新共享组件；不用 ui/Dropdown——其无 options 模式仅是带 caret 的视觉容器、不渲染菜单项，`ui/Dropdown.vue:48-56`，且 ui/* 除 AppButton 外禁改） | 菜单项 ≥2 时：Claude、Codex（菜单：`添加市场`、`手动安装`）；单项时退化为普通 AppButton | 添加市场→`emit('add_market', engine)`——**事件名必须带下划线**：ExtensionsView 监听 `@add_market` 编译为 prop `onAdd_market`，本机实测仅 `emit('add_market')` 命中、`emit('addMarket')` 不命中（§0）；手动安装→打开按名称安装对话框（Claude `installCcPlugin(name)`；Codex `installCxPlugin(id, marketplaceName?)`） |
| 安装插件 / 安装包 | AppButton primary small | OpenCode / Pi / OMP（无市场概念，不显示「浏览插件市场」与「添加 ▾」） | 打开安装对话框（沿用各面板现有 Dialog） |

---

## 4. 共享组件清单（新目录 `frontend/src/components/extensions/ui/`，共 11 个）

由**组件阶段（R0）统一落地**；类型首建改 `pluginViewTypes.ts`（R0 白名单；施工期缺口的兜底归属见 §9 施工顺序）。**可测试性契约**（§8 交互断言依赖，node 环境无 DOM）：下列标注 ★ 的组件必须 `defineExpose` 指定触发器（先例：`PluginSubItemsPanel.vue:305-307`），触发器内部走与模板事件完全相同的 emit 路径；`PluginIcon` 的确定性算法以**同文件导出的纯函数**交付。

| # | 组件 | Props | Emits | 复用 ui/* | 说明 |
|---|---|---|---|---|---|
| 1 | `CountTabs.vue` ★ | `tabs: { key: string; label: string; count: number; hidden?: boolean }[]`；`modelValue: string`；`searchPlaceholder?: string`；`searchValue?: string` | `update:modelValue`、`update:searchValue` | TextInput（搜索框，prefix 插槽放「⌕」文字） | 选中态胶囊=白底浮起（同 Segmented pill 视觉）；计数为胶囊内灰色小徽章；`hidden:true` 的标签不渲染；expose `select(key)`（emit update:modelValue）与 `search(v)`（emit update:searchValue） |
| 2 | `PluginIcon.vue` | `name: string`；`size?: 'sm'\|'md'\|'lg'`（28/36/56px，默认 md） | — | — | 确定性算法以**同文件导出纯函数** `pluginIconStyle(name): { letter, color }` 交付（组件消费同一函数）：`letter = name.trim()[0]?.toUpperCase() ?? '#'`；`hash = Σ name.charCodeAt(i) * 31^i (mod 997)`；`color = PALETTE[hash % 5]`，`PALETTE = ['var(--accent)','var(--success)','var(--warning)','var(--purple)','var(--danger)']`；实色圆角方块（`--radius-md`）+ 白字居中 |
| 3 | `PluginRow.vue` ★ | `name: string`；`description?: string`；`badges?: { text: string; type?: Badge['type']; color?: string; variant?: 'muted'\|'mono' }[]`；`enabled?: boolean`；`switchVisible?: boolean`（默认 false）；`switchDisabled?: boolean`；`active?: boolean` | `click`、`toggle(value: boolean)` | PluginIcon、Badge、Switch | 行式条目：左 PluginIcon(sm) → 中（名称一行 + badges 行内 + 描述单行截断灰色）→ 行尾 Switch（`switchVisible` 才渲染）；整行可点击；expose `toggleWith(v)`（内部即 Switch 的 update 处理器，emit toggle） |
| 4 | `GroupHeader.vue` | `title: string`；`count?: number` | — | — | 具名插槽 `actions`；粘性小节标题（沿用 `.plg-group-header` 视觉，11px/tertiary/大写） |
| 5 | `ListToolbar.vue` | `title: string`；`count: number`；`loading?: boolean` | — | — | 具名插槽 `actions`；标题 17px/600 + 灰色计数徽章（`--control` 底胶囊） |
| 6 | `EmptyStateCard.vue` | `title: string`；`description?: string` | — | AppButton | 具名插槽 `primary`、`secondary`（各放一个 AppButton）；圆角卡片（`--card` + `--separator` + `--radius-card`），居中排版 |
| 7 | `DiagnosticBanner.vue` ★ | `errors: string[]`；`warnings: string[]` | — | — | 两数组皆空则整条不渲染（`v-if` 由父层或组件内保证）。折叠态一行：「加载诊断：N 个错误，M 个警告」（N=errors.length，M=warnings.length，真实计数）；点击展开逐条 `<p>` 列出；error/warning 色条沿用 StatusBanner 配色（`rgba(255,59,48,.08)` / `rgba(255,149,0,.08)`，与 `ui/StatusBanner.vue:54-64` 一致）；expose `toggleExpand()` |
| 8 | `MetaGrid.vue` | `items: { label: string; value: string; mono?: boolean }[]` | — | — | 两列标签-值网格（`grid-template-columns: repeat(2, 1fr)`），值可 `mono`（`var(--mono)`），空 value 项由父层过滤 |
| 9 | `DetailHeader.vue` ★ | `breadcrumb: { label: string; clickable?: boolean }[]`；`title: string`；`badges?: 同 PluginRow.badges` | `back` | PluginIcon(lg)、Badge | 面包屑行（「插件 › 名称」，clickable 段为 accent 色可点，尾段不可点）；下方大图标 + 名称 + badges；具名插槽 `actions` 放主操作按钮组；expose `goBack()`（emit back） |
| 10 | `SubItemRow.vue` ★ | `name: string`；`description?: string`；`typeBadge?: string`（skill/agent/command/hook/mcp 徽章文案）；`enabled?: boolean`；`toggleable?: boolean`（默认 false） | `toggle(value: boolean)` | Badge、Switch | 详情页子项行：类型徽章 + 名称 + 单行描述 + 行尾 Switch（`toggleable` 才渲染；Codex 一律 false）；expose `toggleWith(v)` |
| 11 | `ActionMenu.vue` ★ | `triggerText: string`；`items: { key: string; label: string; disabled?: boolean }[]`；`disabled?: boolean` | `select(key: string)` | AppButton（触发器） | 「添加 ▾」菜单的归属组件：受控展开状态 + Teleport 到 body + `position:fixed` 弹层（实现参照 `ui/Dropdown.vue` 的 Teleport 模式，但为菜单行为、属 extensions/ui 新文件，不改 ui/*）；expose `choose(key)`（emit select 并收起） |

**ui/* 基础件变更（仅此一项，归 R0）**：`ui/AppButton.vue` 新增 `variant: 'outline'`（透明底、`1px solid var(--separator)`、hover `var(--control)`），供「浏览插件市场」描边按钮使用；不改动既有 variant。其余 ui/* 一律不改。

既有组件继续直接复用（不改）：`Dialog`（`v-model:open`/`title`/`description`/`#footer`）、`ConfirmDialog`（`v-model:open`/`title`/`message`/`danger`/`confirm-text`/`@confirm`）、`LoadingState`（`message`）、`ErrorState`（`message`/`on-retry`）、`Segmented`、`Dropdown`、`TextInput`、`Badge`、`Switch`、`PageHead`。

---

## 5. 视图字段映射（通用约定）

行/详情字段必须按下表映射；**缺数据的字段不显示**（不造占位假值）。

### 5.1 行字段映射（PluginRow）

| 视图字段 | Claude | Codex | OpenCode | Pi | OMP |
|---|---|---|---|---|---|
| 图标 name | `name` | `name` | `name` | `name` | `name` |
| 名称 | `name` | `name` | `name` | `name` | `name` |
| 描述（单行截断） | `description` | —（列表无 description，`CodexPlugin` 无此字段，行内不显示描述行） | `description` | `description` | `description` |
| 类型徽章 | `pluginTypeLabel(pluginType)`（集成/混合/Skill/…，沿用 `PluginInstalledPanel.vue:625-642` 映射） | 不显示（无 pluginType） | targets 徽章（`targets[]`） | `sourceType` 徽章（npm/Git/本地） | `kind` 徽章（npm/marketplace） |
| 来源徽章 | `marketplace`（muted） | `source`（muted，有则显示） | `sourceLabel(source)` | `source`（mono muted，超长截断 title 提示） | 不显示（无来源字段） |
| 版本徽章 | `v{version}`（有则显示） | `v{version}` | `v{version}` | `v{version}` | `v{version}` |
| 警告徽章 | — | `warning \|\| duplicateOf` → 「重复诊断」（warning 色） | — | — | — |
| 开关 | `enabled`，可切换 | `enabled`，可切换 | **不显示**（有 enabled 字段但无启停 API） | **不显示**（同左） | `enabled`，可切换 |

### 5.2 详情字段映射（DetailHeader + MetaGrid）

| 视图字段 | Claude（`plugin.PluginDetail`） | Codex（`CodexPluginDetail`） | OpenCode（`OpenCodePluginDetail`） | Pi（`PiPackageDetail`） | OMP（`OmpPlugin`，无详情接口） |
|---|---|---|---|---|---|
| 标题 | `name` | `displayName \|\| name` | `name` | `name` | `name` |
| 描述 | `description` | `longDescription \|\| shortDescription \|\| description` | `description` | `description` | `description` |
| 徽章 | 类型 + `v{version}` + scope（global→「全局」） | `v{version}` + `source` + 重复诊断（**PID 短码徽章删除**：`CodexPlugin/CodexPluginDetail` 只有 `id` 无 `pluginId` 字段，`models.ts:302,460`，现有徽章读 `activePlugin.pluginId` 恒 undefined 为死 UI，`PluginInstalledPanel.vue:151`；PID 信息由 MetaGrid「Plugin ID」行承担） | 来源 + `v{version}` + targets | 来源类型 + `v{version}` + `pinned` | kind + `v{version}` + `scope` |
| **MetaGrid** | 来源市场=`marketplace`；版本=`version`；作用域=`scope`；安装时间=`installedAt`（zh-CN 日期）；最后更新=`lastUpdated`；提交=`gitCommitSha`（short 8 位，mono） | 状态=`enabled ? '已启用' : '已禁用'`；来源市场=`marketplace`；版本=`version`；安装时间=`installedAt`；最后更新=`lastUpdated`；Plugin ID=`id`（mono）；**scope/gitCommitSha 无字段不显示** | 模块 Spec=`spec`（mono）；来源=`source`；作用域=`scope`；安装路径=`installPath`；开发者=`author`；仓库=`repository`；缓存更新时间=`lastUpdated`。**installedAt 无字段不显示** | 包源=`source`（mono）；来源类型=`sourceType`；作用域=`scope`；安装路径=`installPath`；开发者=`author`；仓库=`repository`；更新时间=`lastUpdated`；Manifest=`manifestDeclared ? '已声明' : '未声明（按目录扫描）'` | 引用 ID=`id`（mono）；类型=`kind`；版本=`version`；作用域=`scope`；安装路径=`installPath`；启用特性=`enabledFeatures.join('、')`（空则不显示） |

### 5.3 子项列表（详情页中部，SubItemRow）

| 引擎 | 数据源 | 可切换 |
|---|---|---|
| Claude | `detail.subItems[]`（后端聚合，含 `enabled`；类型单数 skill/agent/command/hook/mcp） | 是（`setPluginSubItemEnabled`，唯一真正落盘路径） |
| Codex | 详情 `skills/agents/commands/hooks` + `mcpServers` keys 合成（沿用 `PluginSubItemsPanel.vue:99-169` 归一化逻辑） | **否**（后端 no-op，隐藏开关，只读展示） |
| OpenCode | 详情 `skills/agents/commands/hooks`（`OpenCodeResourceInfo[]`）+ `hasMcp` 汇总行 | 否（无 API，只读） |
| Pi | 详情 `resources[]`（type: extension/skill/prompt/theme，typeBadge 用对应中文：扩展/技能/提示词/主题） | 否（无 API，只读） |
| OMP | 无子项数据，不渲染该区 | — |

### 5.4 主操作按钮（DetailHeader actions，语义按数据实情）

| 引擎 | `enabled=false` | `enabled=true` | 次操作 |
|---|---|---|---|
| Claude | 「启用」（primary，`togglePlugin(id, false)`——**第二参是「当前态」语义**：`true→disablePlugin、false→enablePlugin`，`stores/plugin.ts:423-427`；启用即传 `false`（等价 `!目标态`，与现有面板 `togglePlugin(plugin.id, !value)` 用法一致） | 「更新」（primary，`updatePlugin(id)`，完成后 reload detail） | 「卸载」（danger，ConfirmDialog → `uninstallPlugin(id)`） |
| Codex | 「启用」（primary，`toggleCxPlugin(id, true)`——**第二参是「目标态」语义**，`stores/plugin.ts:565-575`，与 togglePlugin 相反） | 「更新」（primary，= `upgradeCxMarketplace(marketplace)` + reload detail；缺 marketplace 时按钮禁用并提示） | 「卸载」（danger → `uninstallCxPlugin(id, marketplace)`） |
| OpenCode | 无启停概念：主操作恒为「检查更新」（`update(spec)`；`source==='file'` 禁用且文案「本地直载」） | 同左 | 「卸载」（danger → `uninstall(spec)`，提示缓存保留） |
| Pi | 无启停概念：主操作恒为「更新」（`update(source)`；`sourceType==='local'` 禁用且文案「本地直载」） | 同左 | 「移除」（danger → `remove(source)`）；「切换源」卡片**功能与交互保留、样式必须 token 化**（`SwitchPackageSource`，`PiPluginPanel.vue:124-174`；其现用 `--border/--bg-elevated/--text-muted` 在全前端无定义——本机 grep 无任何定义处、tokens.css 亦无——fallback 渲染为深色卡片 `#171a21/#2a2f3a/#8b93a3`，`PiPluginPanel.vue:876-921`，与 §1 浅色基线冲突 → 改用 `--separator/--control/--secondary`） |
| OMP | 「启用」（primary，`setEnabled(id, true)`） | 「升级」（primary，`upgrade(id)`） | 「卸载」（danger → `uninstall(id)`） |

> 语义警示：`togglePlugin`（当前态）与 `toggleCxPlugin`（目标态）同名风格相反参数语义，§8 要求面板测试对二者参数分别断言（防「照抄启用」回归）。

---

## 6. 每引擎小节

### 6.1 Claude（PluginInstalledPanel engine='claude'）

**布局（自上而下）**：DiagnosticBanner（仅错误，见下）→ CountTabs（插件 N | MCP M | 市场 K + 搜索）→ ListToolbar（「已安装 {filtered.length}」+ 刷新 + 浏览插件市场 + 添加▾）→ 内容区：
- 列表态：按市场分组的 PluginRow 列表（左列 320px）+ 右侧详情列（选中后）；空列表 → EmptyStateCard。
- MCP 态：MCP 资源页（§6.1.1）。
- 市场态：PluginMarketPanel（§6.6）。
- 详情态：面包屑「插件 › {name}」+ DetailHeader + 描述 + 子项列表（PluginSubItemsPanel 改用 SubItemRow 渲染）+ MetaGrid。

**加载/重试（门控实现变更，语义保留）**：三态语义沿用 `loadCcAllData` 容错（`PluginInstalledPanel.vue:400-422,849-874`），但**首屏 Loading/Error 门控必须由本地 `ref(true)` 改为 store 派生**——现实现 `initialLoading=ref(true)` 仅在 `onMounted` 装载后置 false（`PluginInstalledPanel.vue:379,849`；OpenCode/Pi/OMP 同型，`OpenCodePluginPanel.vue:238,337`、`PiPluginPanel.vue:299,579`、`OmpPluginPanel.vue:144,232`），SSR 测试（`renderToString` 不执行 `onMounted`）下主内容分支不可达，§8 面板断言全部写不出。契约门控：`showInitialLoading = computed(() => loadingCC && ccInstalled.length===0 && !ccLoadError)`、整页 ErrorState 条件 `= ccLoadError && ccInstalled.length===0`（数据字段来自 R5a store 成员）；面板本地仅保留 mutating/对话框等状态。重试按钮仍调 `loadCcAllData`。

**操作→store 动作**（签名全部不变）：
- 刷新：`loadCcAllData()`（带 5min 缓存；工具栏刷新走 **R5a 在 `stores/plugin.ts` 新增的 `refreshCc()`**（§7 已列，内部失效缓存+三 load）——注意归属是 R5a（store 白名单），不是集成角色 R5b。
- 启用/禁用：`togglePlugin(id, currentState)`；更新：`updatePlugin(id)`；卸载：`uninstallPlugin(id)`。
- 安装（市场/手动）：`installCcPlugin(name)`；添加市场：`addCcMarketplace(source)`（经 `emit('add_market','claude')` 走 ExtensionsView 内联 Dialog——**R1 必须把 emit 声明与触发从 'addMarket' 改为 'add_market'**，恢复监听可达，见 §0/§9）；更新市场：`updateCcMarketplace(name)`。
- 子项：`setPluginSubItemEnabled(pluginId, type, id, enabled)` + `loadPluginDetail(id)` 回刷。

**空态文案**（EmptyStateCard）：标题「暂无已安装插件」；副文「从插件市场安装 Claude 插件后将显示在此处」；primary=「浏览插件市场」（切市场标签）、secondary=「添加市场」（emit add_market）。

**诊断横幅数据源**：errors=[store 新增 `ccLoadError`]（由 `loadCcAllData` 的 `track()` 在子任务失败时写入，§7——外层 catch 仅非预期异常可达）；warnings=**无来源，恒空**（`loadCcAllData` 只收集错误文本、无警告通道）→ Claude 面板通常不出现横幅，仅子任务失败/致命错误时显示。

#### 6.1.1 MCP 资源页（Claude 独有）
- 分组：按所属插件分组。候选=`ccInstalled.filter(p => p.enabled && (p.pluginType==='mcp' || p.hasMcp))`；条目=详情 `Object.keys(detail.mcpServers)`（`mcpServers` 的 value 结构未知，**不解析、不虚构连接状态**，条目描述固定「Model Context Protocol 服务器」）。
- 数据获取：进入 MCP 标签时，对候选中无缓存详情的插件逐个 `loadPluginDetail(id)`（后端纯本地读、不调 CLI，`docs/user/plugins.md:50`）；已缓存的直接用 `ccActivePluginDetail`。
- 条目：PluginIcon(type 色相 mcp) + 服务器名 + 单行描述 + 状态点：**绿点=`所属插件 enabled`，灰点=禁用**（如实映射插件启用态，非连接状态）；分组头（GroupHeader）=插件名+计数，点击跳转该插件详情。
- 空态：「未发现 MCP 服务器 / 已启用插件中没有可展示的 MCP 服务器」。

### 6.2 Codex（PluginInstalledPanel engine='codex'）

**布局**：同 Claude，差异点：
- CountTabs 仅「插件 N | 市场 K」（无 MCP，见 §3.1）。
- 列表行无描述行（无字段）；行内 source/重复诊断徽章照 §5.1。
- 不再渲染独立 `.stat-bar` 统计条：工具栏标题徽章只显示「已安装 N」；启用态的展示落点为详情 MetaGrid「状态」行（§5.2）。**`cxEnabledCount` getter 保留但新布局无消费位**（如实收窄，不假造落点）。
- 详情：capabilities 徽章区（`manifest.interface.capabilities`，`PluginInstalledPanel.vue:223-231`）并入 DetailHeader badges 或子项区上方徽章行。

**操作→store**：刷新 `loadCxPlugins(true)`；启用/禁用 `toggleCxPlugin(pluginId, enabled)`；「更新」=`upgradeCxMarketplace(marketplace)` + `loadCxPluginDetail(pluginId, marketplace)`（Codex 无单插件更新接口，`PluginInstalledPanel.vue:744-758`）；卸载 `uninstallCxPlugin(pluginId, marketplace)`；安装 `installCxPlugin(pluginId, marketplaceName?)`；添加/删除/升级市场 `addCxMarketplace/removeCxMarketplace/upgradeCxMarketplace`。

**空态**：「暂无已安装 Codex 插件 / 从插件市场安装 Codex 插件后将显示在此处」+ 浏览插件市场 + 添加市场。

**诊断横幅**：errors=[`cxLoadError`]；warnings=`cxWarnings`（逐条列出）+ 重复诊断摘要（`cxDuplicates` 汇总为一条：「检测到 {n} 组重复插件：{names}」）。CLI 加载失败降级横幅语义保持（不吞整页，`PluginInstalledPanel.vue:862-870`）。

**子项**：只读（toggleable=false，§5.3）。

### 6.3 OpenCode（OpenCodePluginPanel）

**布局**：DiagnosticBanner → CountTabs（插件 N + 搜索）→ ListToolbar（「已安装 N」+ 刷新 + 安装插件 primary）→ 行式列表（不分组，无开关）→ 详情（面包屑 + DetailHeader + 描述 + 资源汇总徽章行 + 只读子项列表 + MetaGrid）。移除现有 `.scope-note` 说明卡（其说明文字移入安装对话框 hint）。
**操作→store**：`refresh(true)` / `install(spec)` / `update(spec)` / `uninstall(spec)`（`stores/opencodePlugin.ts:27-87`）。
**空态**：「暂无 OpenCode 全局插件 / 点击『安装插件』，输入 npm、GitHub 或本地模块地址」+ primary=「安装插件」（无 secondary）。
**诊断**：errors=[store `loadError`（**R5a 新增**于 `stores/opencodePlugin.ts`，`refresh` 失败写入/成功清空；现实现为面板本地 `initialError`，`OpenCodePluginPanel.vue:239`，一并迁移）]；warnings=`store.warnings`（JSONC 卸载需手工处理的提示会出现在此，`docs/user/plugins.md:86`）。首屏 Loading/Error 门控按 §6.1 同样改为 store 派生（`loading && installed.length===0 && !loadError`）。
**WSL**：`WSLPluginTargetPlaceholder cli="opencode"` 挂载点保持在 ExtensionsView，不动。

### 6.4 Pi（PiPluginPanel + PiPluginWSLSection）

**布局**：DiagnosticBanner → CountTabs（包 N | 技能 | 扩展 | 提示词 | 主题，零计数标签隐藏 + 搜索）→ ListToolbar（「已安装 N」+ 目标 Segmented（Windows/WSL/同时应用，保留）+ 刷新 + 安装包 primary）→ 行式列表（无开关）→ 详情（面包屑 + DetailHeader + 描述 + 只读资源列表 + MetaGrid + 切换源卡片：**功能与交互保留，样式 token 化**——替换未定义的 `--border/--bg-elevated/--text-muted` 深色 fallback 为 `--separator/--control/--secondary`，见 §5.4 Pi 行）。资源标签页（技能等）：按包分组的行式条目（名称=资源名，描述缺省不显示），页态存面板本地 `resourceTab` ref（§2）。
**操作→store / api**：`refresh(true)` / `install(source)` / `update(source)` / `remove(source)`；目标化操作走 `installPiPackageTargeted` 等（`PiPluginPanel.vue:349-378` 逻辑保留）；切换源 `SwitchPackageSource`。
**空态**：「暂无 Pi 全局包 / 点击『安装包』，输入 npm 包名、git 地址或本地路径」+ primary=「安装包」。
**诊断**：errors=[store `loadError`（**R5a 新增**于 `stores/piPlugin.ts`，`refresh` 失败写入/成功清空；现实现为面板本地 `initialError`，`PiPluginPanel.vue:300`，一并迁移）]；warnings=`store.warnings`。首屏门控同 §6.1 改 store 派生。
**WSL 区**：`PiPluginWSLSection` 整体保留（面板底部），不改契约。

### 6.5 OMP（OmpPluginPanel）

**布局**：DiagnosticBanner → CountTabs（插件 N + 搜索）→ ListToolbar（「已安装 N」+ 刷新 + 安装插件 primary）→ **行式列表带 Switch**（§5.1 唯一新增开关位：行尾启用开关替代原三按钮组；「升级/卸载」移入详情）→ 详情（面包屑 + DetailHeader + 描述 + MetaGrid；无子项区）。列表空态同理。OMP 无详情二级接口，点行即用列表数据渲染详情。
**操作→store**：`refresh(true)` / `install(spec)` / `setEnabled(id, enabled)` / `upgrade(id)` / `uninstall(id)`（`stores/ompPlugin.ts:41-67`）。
**空态**：「暂无 OMP 插件 / 点击『安装插件』，输入 npm 包名、GitHub 源、marketplace 引用或本地路径」+ primary=「安装插件」。
**加载失败双形态（裁定）**：首次加载失败（列表为空且无缓存数据）→ **整页 ErrorState 替换内容区**（保持现行为，`OmpPluginPanel.vue:4-8`）；已有数据后的 `refresh` 失败 → `loadError` 并入 DiagnosticBanner errors；`warnings` 恒走 DiagnosticBanner（`stores/ompPlugin.ts:20,34-36`）。首屏门控改 store 派生（`loading/loadError/installed` 均已在 store，无需 R5a 新增）：`ErrorState 条件 = loadError && installed.length===0`，横幅条件 = 有数据后 `loadError`。两形态均为可断言行为（§8，经 store 状态种子驱动）。

### 6.6 市场视图（PluginMarketPanel，仅 Claude/Codex）

- **进入**：CountTabs「市场」标签或工具栏「浏览插件市场」；**返回**：面包屑「插件 › 插件市场（› {activeMarketId}）」点击「插件」回列表态。
- 结构：顶部搜索 + 排序（Claude：安装量/名称 A-Z；Codex：仅名称 A-Z——`CodexAvailablePlugin` 无 installCount，`models.ts:248`）+「添加市场」；左窄栏市场源列表（名称 + 一行描述 + 「{n} 个可安装」计数徽章 + Codex 删除 ×）+ 右侧插件流行式条目（PluginRow 变体：名称 + 开发者/安装量徽章 + 单行描述 + 行尾「安装」primary small）。
- 安装量徽章：Claude `formatInstallCount(installCount)`（K/M 缩写，沿用 `PluginMarketPanel.vue:363-368`）；Codex 显示 `author` 徽章。
- **已安装判定**（真实推导，无后端字段）：Claude `ccInstalled.some(p => p.name === a.name)`；Codex `cxInstalled.some(p => p.id === a.pluginId || p.name === a.name)`。命中→按钮变禁用文字「已安装」（无版本比较数据，「更新」态不实现，见 §10 风险 7）。
- 安装动作：`installCcPlugin(name)` / `installCxPlugin(id, marketplaceName)`；添加市场：**统一收敛为 `emit('add_market', engine)` 走 ExtensionsView 内联 Dialog（单一入口），端到端事件名恒为 `'add_market'`**：`PluginMarketPanel` 按钮 → `emit('add_market', engine)` → `PluginInstalledPanel` 转发监听改为 `@add_market`（现 `@add-market` 编译为 `onAddMarket`，实测不匹配 `emit('add_market')`，§0 第二处断链）→ 再 `emit('add_market', engine)` → ExtensionsView `@add_market`。面板内 `AddMarketDialog.vue` 退役（R1 删除挂载与文件）——现双轨且面板声明的 camelCase emit 从未触发（§0），收敛后不再存在死 emit。

---

## 7. store 归一化需求（保持既有动作签名不变，只新增 state/computed）

| store | 新增 | 说明 |
|---|---|---|
| `stores/plugin.ts` | `ccMcpPluginCount = computed(() => ccInstalled.value.filter(p => p.hasMcp \|\| p.pluginType==='mcp').length)` | Claude MCP 标签计数 |
| | `ccLoadError = ref('')`、`cxLoadError = ref('')` | 写入点（可测性关键）：`cxLoadError` 由 `loadCxPlugins` 既有 catch 写入/成功清空（该动作 rethrow，`stores/plugin.ts:414`，vi.mock 拒绝即可触发）；`ccLoadError` 由 `loadCcAllData` 的 `track()` 收集子任务失败时写入（现 `track` 只 console.error，`stores/plugin.ts:364-381`，子任务被 `.catch(track)` 吞掉、外层 catch 仅非预期异常可达——若只在外层 catch 写入则 cc 侧不可测）；两者成功时清空。动作签名不变；诊断横幅数据源 |
| | `refreshCc()` 组合函数（内部失效缓存 + 三 load） | 工具栏「刷新」用；不改 `loadCcAllData` 签名 |
| | `ccMarketAvailableCount = computed(() => ccAvailable.value.length)`、Codex 用既有 `cxAvailableCount`；修正既有 `resourceFilters` 的 MCP 计数为 `p.hasMcp \|\| p.pluginType==='mcp'`（现仅查 `p.hasMcp`，列表级恒 undefined → 恒 0，`stores/plugin.ts:193`） | 计数诚实化 |
| | `filteredBySearch` 不进 store | 搜索为面板本地 computed（避免污染共享态） |
| `stores/opencodePlugin.ts` | `installedCount = computed(() => installed.value.length)`（可选，面板直接 `.length` 亦可）；`loadError = ref('')`（`refresh` 失败写入/成功清空——现失败只抛给面板本地 `initialError`，迁移入 store 以支撑 §6.3 诊断与 store 派生门控） | 面板门控与诊断数据源 |
| `stores/piPlugin.ts` | `skillCount/extensionCount/promptCount/themeCount` 四个 Σ computed（§3.1 表达式）；`loadError = ref('')`（同上，支撑 §6.4） | Pi 资源标签 + 诊断数据源 |
| `stores/ompPlugin.ts` | `enabledCount = computed(() => installed.value.filter(p => p.enabled).length)` | 可选统计 |

> **交付顺序约束**：本节 store 新增（§9 的 R5a）必须先于面板（R1-R4）合入——均为纯新增 state/computed，无消费方，可独立过 §8 基线；面板随后消费。若面板因排期先行，允许面板内以本地 computed **按本节同名表达式一字不差地临时实现**，R5a 合入后替换为 store 成员并删除本地副本；最终态一律以 store 为准（避免「R1 引用不存在成员编译失败」或「R5 新增成死代码」两种失败模式）。

---

## 8. 测试计划（Vitest，目录 `frontend/src/__tests__/extensions/`）

**范式约束（已核实 `vitest.config.ts:11` environment 'node'；仓库无 @vue/test-utils / jsdom 且禁新增 DOM 测试库；既有范式测试 `claudeAccountsFix.test.ts` 全为渲染分支断言）**：
- **渲染断言**：`renderToString` 输出做包含/不包含断言（scoped 样式不进 SSR 输出，样式相关只断言 class 名存在，颜色正确性由代码评审 + eslint 保证）。
- **面板门控前提**：四个面板现以本地 `initialLoading=ref(true)` + `onMounted` 装载为门控（`PluginInstalledPanel.vue:379,849`、`OpenCodePluginPanel.vue:238,337`、`PiPluginPanel.vue:299,579`、`OmpPluginPanel.vue:144,232`），SSR 不执行 `onMounted` → 主内容分支在输出中不可达。**契约（§6.1/§6.3-§6.5）：门控一律改为 store 派生 computed**；测试以 **store 状态种子**驱动分支（直接写 store state 或经 vi.mock api + 真实 load 动作），不用也不要求 DOM 事件。
- **Teleport/Dialog 断言（已实证机制）**：`ui/Dialog.vue:2` 即 `<Teleport to="body">`，`renderToString(app, ctx)` 主输出只含 `<!--teleport start--><!--teleport end-->` 锚点，**Dialog 内容在 `ctx.teleports.body`**（本机实测：`open=false` → teleports 为空注释、`open=true` → 内容出现）。凡 Dialog/弹层断言一律查 `ctx.teleports`，不断言主输出字符串。
- **交互断言**：不派发 DOM 事件——调用 §4 契约的 `defineExpose` 触发器（★ 组件）、面板 expose 的 handler，或 **ExtensionsView expose 的 `handleAddMarket/submitAddMarket`**（R5b 测试契约，见 §9），断言 emit 副作用（`h()` 传 `onXxx` spy prop）或 mocked store/api 动作被以正确参数调用；`PluginIcon` 确定性算法直接单测其导出纯函数 `pluginIconStyle`。
- **store 断言**：`createPinia` + 直接构造 state 的纯单测（无渲染）。

**共享组件（R0 交付，每个至少）**：
- `PluginIcon`：纯函数 `pluginIconStyle`——同名同字母同色（确定性）、不同名分布不同色相、空名/空串兜底「#」；组件渲染断言 letter 出现在输出。
- `CountTabs`：渲染断言——计数徽章显示真实数字、`hidden` 标签不在输出、选中标签高亮类名；expose `select(key)` → `onUpdate:modelValue` spy 命中、`search(v)` → `onUpdate:searchValue` 命中。
- `PluginRow`：渲染断言——`switchVisible=false` 时输出无 switch 标记、badges 文本出现、描述行截断类名存在；expose `toggleWith(v)` → `onToggle` spy 命中且参数透传。
- `SubItemRow`：`toggleable=false` 无 switch；expose `toggleWith` 同上。
- `DetailHeader`：面包屑各段渲染、clickable 段类名；expose `goBack()` → `onBack` spy。
- `ActionMenu`：折叠态菜单项不在输出中；expose `choose(key)` → `onSelect` spy。
- `DiagnosticBanner`：两数组皆空 → 输出为空；折叠态文案含「N 个错误，M 个警告」且 N/M 为传入数组真实长度；expose `toggleExpand()` 后逐条文本出现。
- `GroupHeader`：title/count 渲染（count 未传则无计数徽章）、`actions` 插槽内容出现。
- `ListToolbar`：标题 + 计数徽章真实数字渲染、`loading=true` 时刷新态类名/禁用标记、`actions` 插槽内容出现。
- `MetaGrid`：label/value 成对渲染、mono class；`EmptyStateCard`：插槽按钮文案渲染。

**面板（R1-R4 交付；渲染断言 + mock api/store；行为断言一律走面板 `defineExpose` 的 handler——各面板须按此契约 expose：`handleToggle`、`handleAddMarket`、`handleUpdate` 等，先例 `PluginSubItemsPanel.vue:305-307`）**：
- Claude/Codex：分组渲染（多市场多行、分组头计数）；Codex 行无描述行、子项区无 switch 标记；`cxWarnings` 非空时横幅出现且计数正确；空列表渲染 EmptyStateCard 双按钮文案；市场条目「已安装」推导渲染正确。行为断言：expose `handleToggle` 调用后 `togglePlugin(id, false)`（启用=当前态语义）与 `toggleCxPlugin(id, true)`（目标态语义）参数分别正确（§5.4 语义警示的回归测试）；expose `handleAddMarket` 后断言收到的事件名为 **'add_market'**（camelCase 断链回归测试，§0）。
- OpenCode：空态文案渲染（store 种子：`installed=[]`）；安装对话框**经 `ctx.teleports.body` 断言**（`showInstallDialog` 状态在面板本地，expose 该 ref 或以 store 种子+expose `openInstallDialog` 后重渲——二选一，R2 实现时在 expose 契约中声明）；warnings 横幅；行无开关。
- Pi：资源标签 Σ 计数（store 种子带 skills/extensions 数组）；零计数标签隐藏；切换源卡片存在（class 断言）。
- OMP：**双形态**——store 种子 `loadError 非空且 installed=[]` → ErrorState 整页渲染；`installed 非空 + loadError 非空` → DiagnosticBanner；行开关经 expose handler 调 `setEnabled(id, 目标态)`。
- store（R5a 交付，纯 pinia 单测）：`ccMcpPluginCount`、Pi 四个 Σ computed、`omp enabledCount` 计数正确；**`cxLoadError`**：vi.mock `api/codexPlugin` 拒绝 → `loadCxPlugins` rethrow 路径写入、成功清空；**`ccLoadError`**：vi.mock `api/plugin` 单个子任务拒绝 → `loadCcAllData` 的 `track()` 写入（外层 catch 不可达，`stores/plugin.ts:364-381`，故写入点必须落在 track——§7），成功清空。
- 集成（R5b 交付）：ExtensionsView 渲染五引擎 pane 与两个 Segmented；**ExtensionsView 须 `defineExpose({ handleAddMarket, submitAddMarket })`（R5b 测试契约）**，直调 `handleAddMarket(engine)` 后重渲，**内联 Dialog 打开经 `ctx.teleports.body` 断言**（标题按引擎出现），`submitAddMarket` 调用后按引擎分派 `addCcMarketplace/addCxMarketplace`（`ExtensionsView.vue:176-205` 流程）；面板链路回归：PluginMarketPanel→PluginInstalledPanel 转发（`@add_market`）→ ExtensionsView Dialog 打开（§9 显式变更 ④ 的端到端验证）。

**质量基线（每角色交付前自跑，Windows 命令形态）**：
- `cmd /c npm --prefix frontend run lint`（eslint 含 warning 零告警）
- `cmd /c npm --prefix frontend run test`（vitest 全绿）
- `cmd /c npm --prefix frontend run build`（vue-tsc --noEmit && vite build 通过）

---

## 9. 文件白名单总表与施工顺序

| 角色 | 可动文件 |
|---|---|
| **R0 组件阶段** | `frontend/src/components/extensions/ui/**`（新建 11 组件）；`frontend/src/components/extensions/pluginViewTypes.ts`（类型首建）；`frontend/src/components/ui/AppButton.vue`（仅新增 outline variant）；`frontend/src/__tests__/extensions/ui/**` |
| **R1 Claude+Codex 面板** | `frontend/src/components/extensions/PluginInstalledPanel.vue`、`PluginMarketPanel.vue`、`PluginSubItemsPanel.vue`、`AddMarketDialog.vue`（本次退役：删除挂载与文件，§6.6）；`frontend/src/__tests__/extensions/panels/**`（本面板测试） |
| **R2 OpenCode** | `frontend/src/components/extensions/OpenCodePluginPanel.vue` + `__tests__/extensions/panels/**` 中本面板测试 |
| **R3 Pi** | `frontend/src/components/extensions/PiPluginPanel.vue`、`PiPluginWSLSection.vue` + 本面板测试 |
| **R4 OMP** | `frontend/src/components/extensions/OmpPluginPanel.vue` + 本面板测试 |
| **R5a store 归一化** | `frontend/src/stores/plugin.ts`、`opencodePlugin.ts`、`piPlugin.ts`、`ompPlugin.ts`；`frontend/src/components/extensions/pluginViewTypes.ts`（**施工期类型缺口兜底**：R1-R4 发现缺口不得自行改文件，上报后由 R5a 补）；`frontend/src/__tests__/extensions/stores/**` |
| **R5b 集成+文档** | `frontend/src/views/ExtensionsView.vue`（含新增 `defineExpose({ handleAddMarket, submitAddMarket })` 测试契约，§8）；`docs/user/plugins.md`；`frontend/src/__tests__/extensions/integration/**` |
| **禁动** | `frontend/wailsjs/**`、任何 Go 文件、`mobile/**`、`WSLPluginTargetPlaceholder.vue`、`EnvVarsPanel.vue`、`OtherToolsPanel.vue`、其余 `ui/*` |

**施工顺序（解决 R5 store ↔ R1-R4 面板的编译依赖）**：
1. **R0**（共享组件 + 类型首建 + AppButton outline）→ 2. **R5a**（§7 store 归一化，纯新增无消费方，独立过基线）→ 3. **R1-R4**（面板，消费 R0 组件与 R5a 成员；R1 同时完成 emit 名修正 ①②④ 与 AddMarketDialog 退役）→ 4. **R5b**（ExtensionsView 集成验证 + 文档 + 集成测试）。
   排期倒置时按 §7 末尾的「本地临时实现 → R5a 合入后替换」规则执行。

**ExtensionsView 集成不变量与唯一显式变更**（R5b 负责验证）：
- **不变**：五个引擎面板的组件名与挂载关系、`PluginInstalledPanel` 的 `engine` prop、监听器名 `@add_market`（模板与 handler `ExtensionsView.vue:176-180` 不动）、内联添加市场 Dialog 流程（`ExtensionsView.vue:88-123,183-205`）、`WSLPluginTargetPlaceholder` 挂载、主/引擎两个 Segmented。
- **显式变更（修复断链，R1 实施、R5b 验证）**：① `PluginInstalledPanel` 的 emit 声明与触发由 `'addMarket'` 改为 **`'add_market'`**（实测依据见 §0：模板 `@add_market` 编译为 prop `onAdd_market`，仅 `emit('add_market')` 命中）；② `PluginMarketPanel` 的 emit 声明由 `'addMarket'` 改为 **`'add_market'`** 并让「添加市场」按钮真实触发（现为从未触发的死声明，`PluginMarketPanel.vue:204-206,374-377`）；③ `AddMarketDialog.vue` 退役（§6.6）；④ `PluginInstalledPanel.vue:287` 的转发监听由 `@add-market` 改为 **`@add_market`**（实测：`@add-market` 编译为 `onAddMarket`，`emit('add_market')` 不命中——不改此行则 ② 造成市场视图按钮死链）。集成验证步骤：a) 工具栏/空态「添加市场」→ ① 链路 → 内联 Dialog 打开并按引擎提交；b) 市场视图「添加市场」→ ②→④ 链路 → 同一 Dialog 打开（R5b 集成测试两条入口都覆盖）。

---

## 10. 风险与开放问题（默认决策加粗）

1. **add_market 事件断链（两处，已实证）**：外层面板 emit camelCase `'addMarket'` 而视图监听 `@add_market`；市场视图转发监听 `@add-market`（`PluginInstalledPanel.vue:287`）编译为 `onAddMarket`，实测匹配矩阵：`emit('addMarket')`/`emit('add-market')` 命中 `onAddMarket`、`emit('add_market')` 不命中（反之 `@add_market` 只命中 `emit('add_market')`）。**默认：端到端统一事件名 `'add_market'`（R1 改 ①②④，见 §9 显式变更），AddMarketDialog 退役收敛单一入口（§6.6）；R5b 集成测试覆盖工具栏与市场视图两条入口。**
2. **Claude MCP 计数口径**：`hasMcp` 列表级缺失，只能以 `pluginType==='mcp'` 推断，可能低估含 MCP 的 hybrid/integration 插件。**默认：接受 pluginType 口径，MCP 资源页内条目以详情 `mcpServers` 为准（进入时惰性加载），页头计数标注为「含 MCP 的插件数」。**
3. **MCP 惰性加载成本**：进入 MCP 页对 N 个插件逐个 `loadPluginDetail`（纯本地读）。**默认：仅对 `enabled` 候选加载并并发 `Promise.all`；失败单项静默跳过并计入诊断 warnings。**
4. **Codex 无 MCP 标签**（数据缺失）。**默认：不显示，不做假数据；文档注明。**
5. **Pi 列表级资源数组可能未填充**。**默认：Σ=0 隐藏标签，宁可少显示不显示假零。**
6. **移除五类资源筛选 chips（resFilter）**：现有 Claude chips 与 Codex 恒真 no-op chips 被「MCP 标签 + 类型徽章 + 搜索」替代，属功能收窄。**默认：按目标模式收窄；`resFilter` state 保留在 store 不删（避免破坏序列化），面板不再消费。**
7. **市场「更新」态**：无版本比较数据（available.version 与 installed.version 可比但更新动作语义=重装/市场升级）。**默认：只做「已安装」禁用态，不做「可更新」徽章。**
8. **OMP 行内开关 mutating 粒度**：现 `mutating` 全局禁用所有按钮。**默认：行级开关仅禁用当前行（面板内 per-id loading ref），详情按钮全局禁用保持。**
9. **AppButton outline** 为 ui 共享件变更。**默认：R0 统一落地并补 class 断言（含 btn-outline），其余角色不得改 ui/*。**
10. **市场删除（Codex）直接调 wailsjs**（`PluginMarketPanel.vue:190` 绕过 store）。**默认：改走 store `removeCxMarketplace`，签名已存在（`stores/plugin.ts:624`），R1 顺带修正。**
11. **Pi 切换源卡片深色 fallback**：`--border/--bg-elevated/--text-muted` 全前端无定义（本机 grep 验证），现渲染深色卡片与浅色基线冲突。**默认：功能保留、样式 token 化为 `--separator/--control/--secondary`（§5.4/§6.4，R3 白名单内完成）。**
12. **OMP loadError 双形态**：整页 ErrorState 与 DiagnosticBanner 并存语义。**默认：首次失败=整页 ErrorState（保持现行为），刷新失败=横幅（§6.5）；§8 分别断言。**
