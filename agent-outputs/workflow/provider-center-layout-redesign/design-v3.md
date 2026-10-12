# Provider Center 布局 — v3 增量修订（顶侧页签恢复＋左栏收缩到编辑器内）

> **结论**：二级（格式／CLI）与三级（模式）回到 v1 的顶侧单行 subnav，左栏只保留在有分区的编辑器内部，也就是已发布、e2e 已覆盖的 `navigation='local'` 形态。生产代码只改 `views/ProviderCenterView.vue` 一个文件。五个编辑器、`configNavigation.ts`、`SectionNav.vue`、e2e 两文件都不改。external 模式代码这一轮暂时保留，等主上验收 v3 后再按 §3.2 的 C1 清单单独删除。
> 宽度上，800px 档右侧仍是 **356px（Windows 滚动条下 340px）**，与 v2 相同。任务书估的 308px 前提不成立（见 §4.1）。v3 真正多出的代价在纵向：多了一行 subnav，约 52px。
>
> 上游：[`p1v3-taskbook.md`](./p1v3-taskbook.md)、[`design-v2.md`](./design-v2.md)、[`design.md`](./design.md)（v1）、[`impl-report.md`](./impl-report.md)、`v1-snapshot/tracked.patch`（v1 subnav 段）。本文只写受新指令影响的部分；没提到的条款继续按 v2 执行（v2 又继承了 v1 中未被它覆盖的部分）。
>
> 证据边界：本轮工具只有 read、grep、find、write，没有执行 shell、git、构建或浏览器；唯一产物就是本文件。「HEAD = v1.3.96 发布态」取自任务书，本轮没有用 git 核验，实现前须按 §6.3 先做只读核对。

---

## 1. 指令解读确认（逐项对照任务书 §2 预裁）

| # | Leader 预裁 | 判定 | 依据／修正 |
|---|---|---|---|
| 1 | CLI 页二级 Pi \| OMP \| OpenCode 用 v1 underline | ✓ | 主上原话「cli独立配置页的pi，omp的分页……保留顶侧页签」。顺序沿用 v1 §3.2 的 `Pi · OMP · OpenCode` |
| 2 | 三级模式用顶侧 pill：Agent 配置／模型提供商／认证登录；OpenCode 为预设管理／全局配置 | ✓ | 原话「agent配置，模型提供商，认证登录的分页」。和二级放在**同一行，右侧对齐**（v1 N2）。保留 v1 N3 的理由：三级留在容器里，异步编辑器加载中或失败时仍可切换 |
| 3 | Pi Agent 左栏：角色模型／上下文压缩／MCP 路由／并发限制 | ✓ | 主上点名的三项都在这里；并发限制在 v1 也是同页平铺的卡，同样归左栏 |
| 4 | OMP Agent 左栏：角色模型／子代理覆盖 | ✓ | v1 两张卡 |
| 5 | OpenCode 全局左栏 | ✓（顺序修正） | 实际顺序是 `OPENCODE_SECTIONS`：**顶层模型（条件）在最前**，然后是提供商、Agent、MCP 服务器、权限、指令文件、插件、实验特性，**其他键（条件）在最后**。常驻 7 项，最多 9 项 |
| 6 | Registry 左栏：动态提供商＋其他顶层键＋添加提供商 | ✓ | v1 中提供商是逐个折叠卡平铺，属于「页内卡片式平铺」。顺序为提供商（对象原序）→ root（存在时）→ add |
| 7 | Auth 左栏：动态凭据＋添加 API Key | ✓ | v1 的 `.ace-entry` 也是卡片平铺 |
| 8 | 预设页恢复 v1：顶侧 Anthropic \| OpenAI＋右侧分组列表，不设左栏 | ✓ | 格式本来就是页签，列表也不是卡片平铺功能项。`formatConsumers` 说明从右侧列表上方移回 subnav 右端（v1 T02 的位置） |
| 9 | （补充）OpenCode「预设管理」 | ✓ 不设左栏 | 本来就没有内部分区，`OpenCodePresets` 组件没有 local 导航 |
| 10 | （补充）一级 pill「服务提供商／预设／CLI 独立配置」 | ✓ 不变 | 指令没有涉及 |
| 11 | 宽度预估 308px | **修正** | subnav 只占高度，不占宽度。v2 的 340px 来自窄档页边距、卡片内边距的回收和去掉分区卡内边距，与删掉 subnav 行无关。v3 保留这些回收，800 档仍为 356／340，见 §4.1 |

**唯一的解释空间（交给主上做视觉复核，不阻塞实现）**：主上举的例子都来自 Pi Agent 配置。Registry／Auth 的条目是「数据记录」而不是「功能项」，但它们在 v1 里同样以卡片平铺呈现，按「原有的页内的卡片式平铺」这一字面判据应归左栏，因此采纳 Leader 预裁。如果主上看图后认为 Registry／Auth 应回到列表或折叠卡，改动只在这两个编辑器内部，不影响本轮容器结构。

---

## 2. 目标结构

### 2.1 两页 IA

```text
ConfigCard.pc-card
├─ pc-head：[服务提供商 | 预设 | CLI 独立配置](pill)        [配置档][导出][导入]      ← 不变
└─ pc-panel
   ├─ pc-subnav（单行，底部通栏分隔线；<1024 收紧，必要时 flex-wrap）
   │    预设页：Anthropic 格式 · OpenAI 格式 (underline) ········ 用于 Claude Code｜用于 Codex · Pi · OMP
   │    CLI 页：Pi · OMP · OpenCode (underline) ···· [Agent 配置|模型提供商|认证登录] (紧凑 pill，随二级切换)
   │                                              OMP：[Agent 配置|模型提供商]  OpenCode：[预设管理|全局配置]
   └─ pc-detail-content（:key = activePageId，作用见 §3.1）
        预设页 → PresetList(format)                                 无左栏，整宽
        OpenCode 预设管理 → OpenCodePresets                          无左栏，整宽
        其余 5 个模式 → 编辑器 local 布局：
          .xxx-root.editor-local-layout（grid：左栏 | 右侧）
          ├─ SectionNav[aria-label="配置分区"]   ← 左栏只在这里出现
          └─ .editor-detail
               EditorToolbar（路径／合法／生效／模式／保存）
               全文件横幅（解析错误、目录失败）
               visual：EditorSection × 已访问（只显示一个）｜source：整文件源码
```

### 2.2 左栏内容（全部由编辑器自己持有，已在 v2 实现）

| 模式 | 左栏条目（id） | 默认 | 条目上的次级标记 |
|---|---|---|---|
| Pi · Agent 配置 | agents／compaction／mcp／concurrency | agents | compaction：meta「下次压缩生效」；有 compactionError 时标「需修正」 |
| OMP · Agent 配置 | modelRoles／overrides | modelRoles | — |
| Pi／OMP · 模型提供商 | `provider:<encoded>` × N → root（条件）→ add | 首个提供商，否则 root，否则 add | `amagi-*`：warning meta「受管」 |
| Pi · 认证登录 | `entry:<n>` × N → add | 首条，否则 add | meta 为类型（api_key／oauth／…），不含任何密钥字段 |
| OpenCode · 全局配置 | model（条件）／provider／agent／mcp／permission／instructions／plugin／experimental／unknown（条件） | provider | — |

### 2.3 层级职责

| 层 | 承载 | 拥有者 |
|---|---|---|
| 一级 | 页面（服务提供商／预设／CLI 独立配置） | ProviderCenterView（不变） |
| 二级 | 格式，或 CLI（决定 `store.presetEngine`） | ProviderCenterView `.pc-subnav-tabs` |
| 三级 | CLI 内的配置文件（模式） | ProviderCenterView `.pc-subnav-modes` |
| 文件级 | 路径、合法状态、生效时机、源码／可视化、保存 | 编辑器 EditorToolbar（不变） |
| 分区 | 文件内原卡片平铺的功能项、动态记录 | 编辑器 local SectionNav（不变） |

不变量：**页面级不再有任何 SectionNav**。每个 `.section-nav` 都必须是编辑器根（`.pac-root/.ogc-root/.pre-root/.ace-root/.oc-global`）的后代。

---

## 3. 逐文件增量指令

目录前缀为 `frontend/src/`。

### 3.1 `views/ProviderCenterView.vue`（唯一的生产改动）

**删除**
- 模板：页面级 `<div class="pc-workspace">`、其中的 `<SectionNav …>`、`id="pc-editor-detail"`；五个编辑器上的 `v-bind="editorNavigation"` 和 `@navigation-change="onNavigationChange"`；`.pc-format-caption` 段落。
- 脚本：`sectionMemory`、`navSnapshot`、`navOwner`、`mountSequence` 及其 `watch([activePageId, presetLoadKey])`、`desiredSection`、`editorNavigation`、`onNavigationChange`、`navGroups`、`navigationTargets`、`currentNavId`、`navigationNotice`、`selectNavigation`。
- `PresetLoadError.setup` 里的 `owner` 常量和 `onMounted` snapshot 上报（local 模式下编辑器自己管导航；加载失败时用户靠顶侧 subnav 离开）。
- import：`SectionNav`、`staticSections`、`pageSectionId`、`type EditorNavigationSnapshot`、`type SectionNavGroup`（`ConfigPageId` 可删，也可留给 activePageId 标注类型）。
- 样式：`.pc-workspace` 及其两档 media 中的 `--section-nav-width`/gap 规则、`.pc-format-caption`。

**保留（逐字不动）**
PageHead、远程横幅、详情视图、一级 pill 与 pc-head 动作、服务提供商面板、`formatModel`／`cliModel` 两个 computed setter、`formatConsumers`、`presetLoadKey`、`lazyPreset`、六个 `lazyPreset(...)` 声明、`PresetLoadError` 的 `onRetry` 整段（含 sessionStorage 整页重载防循环逻辑）、`watch(mainTab)`、`watch(activeProviderId)`、`onProfileApplied`、导入导出、`isWorkspace`、`.pc-workspace-view`／`.pc-card-workspace` 两个窄档边距类及 `.pc-head-actions` 换行规则（§4.1 的宽度靠它们）。`activePageId` 保留，只用作右侧外层 key，不再参与导航。

**模板骨架（面板部分）**

```vue
<div v-if="mainTab === 'providers'" class="pc-panel pc-panel-providers">原内容</div>
<div v-else class="pc-panel">
  <!-- 二三级合并为一行：左 underline（二级），右紧凑 pill（三级，随二级切换）或「用于 …」说明。
       三级留在容器里而不下放进编辑器：异步编辑器加载中/失败时仍可切换。 -->
  <div class="pc-subnav">
    <template v-if="mainTab === 'presets'">
      <Segmented v-model="formatModel" :options="FORMAT_TABS" variant="underline" class="pc-subnav-tabs" />
      <span class="pc-subnav-caption">{{ formatConsumers }}</span>
    </template>
    <template v-else>
      <Segmented v-model="cliModel" :options="CLI_TABS" variant="underline" class="pc-subnav-tabs" />
      <Segmented v-if="store.presetEngine === 'pi'" v-model="piMode" :options="PI_MODES" variant="pill" class="pc-subnav-modes" />
      <Segmented v-else-if="store.presetEngine === 'omp'" v-model="ompMode" :options="OMP_MODES" variant="pill" class="pc-subnav-modes" />
      <Segmented v-else-if="store.presetEngine === 'opencode'" v-model="openCodeMode" :options="OPENCODE_MODES" variant="pill" class="pc-subnav-modes" />
    </template>
  </div>
  <div class="pc-detail-content" :key="activePageId">
    <template v-if="mainTab === 'presets'">
      <PresetList v-if="store.presetEngine === 'anthropic'" format="anthropic" />
      <PresetList v-else-if="store.presetEngine === 'openai'" format="openai" />
    </template>
    <template v-else-if="store.presetEngine === 'pi'">
      <PiAmagiConfig v-if="piMode === 'agents'" :key="presetLoadKey" />
      <ProviderRegistryEditor v-else-if="piMode === 'registry'" :key="presetLoadKey" engine="pi" />
      <AuthConfigEditor v-else :key="presetLoadKey" />
    </template>
    <template v-else-if="store.presetEngine === 'omp'">
      <OmpGlobalConfig v-if="ompMode === 'agents'" :key="presetLoadKey" />
      <ProviderRegistryEditor v-else :key="presetLoadKey" engine="omp" />
    </template>
    <template v-else-if="store.presetEngine === 'opencode'">
      <OpenCodePresets v-if="openCodeMode === 'presets'" :key="presetLoadKey" />
      <OpenCodeGlobalConfig v-else :key="presetLoadKey" />
    </template>
  </div>
</div>
```

- 编辑器**不传** `navigation`，走默认 `'local'`；也不传 `section-id`／`nav-owner`，不监听 `navigation-change`。
- `:key="presetLoadKey"` 仍为 **7 处**。外层 `:key="activePageId"` 沿用 v2 的隔离语义：Pi／OMP 两个 `ProviderRegistryEditor` 在切换 engine 时不复用旧实例。按模式切换本来就会经 v-if 重挂载，所以这个 key 不会额外增加挂载次数。
- 二级、三级直接 `v-model` 到 `cliModel`／`formatModel`（computed setter → `store.setPresetEngine`）和 `piMode`／`ompMode`／`openCodeMode` 三个 ref。三个 mode ref 各自独立，切换 CLI 后会回到该 CLI 上次所在的模式（v1 行为）。

**选项常量：只从纯导航表派生，不复制文案**

```ts
import { CONFIG_PAGES, FORMAT_ITEMS } from '../components/provider/configNavigation';
// 顶侧页签与 v1 常量文案一致，单一来源在 configNavigation.ts
const FORMAT_TABS = FORMAT_ITEMS.map(i => ({ value: i.id.slice('format:'.length), label: i.label }));
const CLI_TABS = [...new Map(CONFIG_PAGES.map(p => [p.id.split(':')[0], p.cli])).entries()]
  .map(([value, label]) => ({ value, label }));                 // pi/Pi, omp/OMP, opencode/OpenCode
const modeTabs = (engine: string) => CONFIG_PAGES.filter(p => p.id.startsWith(`${engine}:`))
  .map(p => ({ value: p.id.slice(engine.length + 1), label: p.label }));
const PI_MODES = modeTabs('pi'), OMP_MODES = modeTabs('omp'), OPENCODE_MODES = modeTabs('opencode');
```

派生出的标签必须与 v1 §3.2 完全一致：`Anthropic 格式/OpenAI 格式`、`Pi/OMP/OpenCode`、`Agent 配置/模型提供商/认证登录`、`Agent 配置/模型提供商`、`预设管理/全局配置`。值与 `PresetEngine` 以及三个 mode 的联合类型字面一致。

**PresetLoadError 缩为**：`setup() { return () => h(ErrorState, { title, message, onRetry: <HEAD 原样> }) }`。

**样式（取自 v1 patch 段，外加一条窄档收紧）**

```css
.pc-subnav { display:flex; align-items:flex-end; justify-content:space-between; gap:8px 16px; flex-wrap:wrap; border-bottom:1px solid var(--separator); }
.pc-subnav-tabs.underline-tabs { border-bottom:none; }            /* 组合选择器压过 Segmented 自带的底线 */
.pc-subnav-caption { font-size:12px; color:var(--tertiary); padding-bottom:8px; white-space:nowrap; }
.pc-subnav-modes { margin-bottom:6px; }
.pc-subnav-modes.segmented { display:inline-flex; padding:2px; border-radius:8px; }
.pc-subnav-modes :deep(.seg) { flex:0 0 auto; padding:4px 12px; font-size:12px; white-space:nowrap; }
.pc-detail-content { min-width:0; }
@media (max-width:1023px) {                                        /* v3 新增：800 档保证单行，见 §4.2 */
  .pc-subnav-tabs.underline-tabs { gap:16px; }
  .pc-subnav-modes :deep(.seg) { padding-inline:10px; }
}
```

间距沿用 v1：pc-head 到 subnav 12px（`.pc-card` gap），subnav 到首个内容 14px（`.pc-panel` gap）。顶部注释改回「二三级合并 subnav＋编辑器内分区导航」，删掉「统一左栏」的说法。

### 3.2 五个编辑器：本轮零改动（external 模式先保留）

| 候选 | 好处 | 代价 | 可逆性 |
|---|---|---|---|
| A 本轮删除 external 分支、`navigation`/`sectionId`/`navOwner` props、snapshot emit | 组件更干净，不留休眠路径 | 五个编辑器＋configNavigation＋SectionNav＋e2e fixture 都要改（删掉 `navigation` prop 后，fixture 的 `{navigation:'local'}` 会变成落到根节点的属性，fixture 也得改），违背「e2e 零改动」；v3 一旦被否，要回到 v2 就得 revert 多文件 | 中 |
| **B 本轮保留，主上验收后用独立提交 C1 清理（选定）** | v3 生产 diff 只有 1 个文件；e2e、fixture、编辑器全部零改动；回退到 v2 只需 revert 一个文件 | 五个编辑器里留着一段没人消费的 external 分支和 `navigation-change` emit（computed 加 watch，成本可忽略），属于明确登记的休眠代码 | 高 |

选 B 的根据：v1 → v2 → v3 在短时间内两次改版，主上的布局偏好还没稳定，保持最小可逆面最划算。休眠代码不是新增抽象，C1 清单列在下面，债务有界。

**C1 清理切片**（触发条件：主上验收 v3 视觉；须单独提交，不与 v3 合并）
1. 五个编辑器：`SectionNav` 与 `editor-local-layout` 去掉 `v-if`／条件 class，改为无条件；删除 `navigation`、`sectionId`、`navOwner` props，删除 `navigation-change` emit 与 `navigationSnapshot` 的 computed 和 watch，删除 `watch(() => props.sectionId)`；Registry／Auth 的 `focusSelection` 去掉 `.closest('.pc-workspace')` 和 `pageSectionId` 分支，直接用 `editorRoot` 和 `activeSection`。
2. `configNavigation.ts`：删除 `EditorNavigationSnapshot`、`EditorNavigationProps`、`staticSections`、`pageSectionId`，以及 `CONFIG_PAGES` 的 `defaultId`／`dynamic` 字段（`CONFIG_PAGES` 和 `FORMAT_ITEMS` 继续供 §3.1 派生页签）。
3. `SectionNav.vue`：用 grep 确认没有消费者后，删掉 `children`、`scrollChildren`、`branchId` 及对应模板和样式。
4. `e2e/pi-amagi-config.html`：`h(PiAmagiConfig, { navigation: 'local' })` 改为 `h(PiAmagiConfig)`，注释改成「挂载生产组件（含内置分区导航）」；spec 不动。
5. 验证：V1、V2、V3 以及 v3 harness 的 drafts、dynamic 两批。

**PresetLoadError 的 owner 校验和静态分区上报是否还需要**：不需要，在 §3.1 删除。原因是 local 模式下 chunk 没加载完时编辑器根本不存在，也就没有分区可以报；可逃逸性改由顶侧 subnav 保证，它不在异步边界之内。

### 3.3 `components/provider/configNavigation.ts`：零改动，保留范围裁定

| 导出 | v3 状态 |
|---|---|
| `PI_SECTIONS`／`OMP_SECTIONS`／`OPENCODE_SECTIONS`、`providerSectionId`、`ConfigSectionItem`、`SectionNavItem`／`SectionNavGroup` | **在用**（编辑器 local 分区表、SectionNav 类型） |
| `CONFIG_PAGES`、`FORMAT_ITEMS` | **在用**（§3.1 页签选项的单一来源） |
| `EditorNavigationProps`／`EditorNavigationSnapshot`、`pageSectionId` | 编辑器的 external 分支还在引用（休眠），C1 时删除 |
| `staticSections`、`CONFIG_PAGES.defaultId/dynamic` | 无消费者（休眠），C1 时删除 |

文件头「纯展示元数据：不加载组件、不读取配置文件」的约束不变。

### 3.4 其他前端文件：零改动

`ui/SectionNav.vue`、`ui/EditorSection.vue`、`ui/sectionVisibility.ts`、`InfoTip`、`Dropdown`、`EditorToolbar`、`FieldLabel`、`PresetList`、`OpenCodePresets`、`ConfigCategoryCard`、tokens 全部不动。SectionNav 的 `max-height:max(240px, calc(100vh - 280px))` 和 sticky 在 v3 中仍然成立，核算见 §4.3。

### 3.5 e2e：零改动

- `e2e/pi-amagi-config.html` 本来就挂 `navigation:'local'`。v3 里生产嵌入用的也是 local，于是 fixture 与生产的结构从「回退形态」变成了「同一形态」，证据力反而增强。注释「独立挂载生产导航与分区外壳；未挂 ProviderCenterView」仍然准确。
- `e2e/pi-amagi-config.spec.ts`：零改动。它只依赖 `nav[aria-label="配置分区"]`、`.editor-section[data-section]`、按钮名，这些都在编辑器内，v3 不碰。
- 这套 e2e 不覆盖顶侧 subnav，subnav 由 §3.6 的 harness 验证；不为 subnav 新开第三个 e2e 目录或新的 spec。

### 3.6 harness 适配（`agent-outputs/.../x-harness/`，属验证工具，不是生产代码）

**做法**：新建 `v3-*` 文件，v2 文件原样保留作历史证据。v2 文件已经不能对 v3 运行：它们依赖页面级 `data-nav-id`，而 `v2-static.mjs` 是用「HEAD＋v1 patch」重建基线的，HEAD 已前移，重建会失败。截图前缀改为 `v3-`。

| 文件 | 来源 | 差异 |
|---|---|---|
| `v3-ab.sh` | 复制 v2-ab.sh | `AGENT_BROWSER_SESSION` 换成新的唯一值；INIT_SCRIPTS 改为 `v2-mock-wails.js,v3-helpers.js`（transport 与布局无关，直接复用） |
| `v3-helpers.js` | 重写导航部分 | 见下 |
| `v3-shots.sh` | 复制 v2-shots.sh | 改用 `v3-ab.sh`；等待条件改为 `.pc-detail-content .editor-toolbar, .pc-detail-content .pc-toolbar`；输出 `v3-<view>-WxH.png` |
| `v3-runtime.mjs` | 复制 v2-runtime.mjs | 注入 `v3-helpers.js`；按下表修改各批 |
| `v3-static.mjs` | 重写 | 基线是 `git show HEAD:<file>`（v1.3.96），不再从 v1 patch 重建，见 §5.2 |

**`v3-helpers.js` 接口**（只点真实按钮、只读 DOM，不改 Vue 状态）

```js
const VIEW = { 'presets-anthropic':['预设','Anthropic 格式'], 'presets-openai':['预设','OpenAI 格式'],
  'pi-agents':['CLI 独立配置','Pi','Agent 配置'], 'pi-registry':['CLI 独立配置','Pi','模型提供商'], 'pi-auth':['CLI 独立配置','Pi','认证登录'],
  'omp-agents':['CLI 独立配置','OMP','Agent 配置'], 'omp-registry':['CLI 独立配置','OMP','模型提供商'],
  'opencode-presets':['CLI 独立配置','OpenCode','预设管理'], 'opencode-global':['CLI 独立配置','OpenCode','全局配置'] };
// clickText(scope, text)：轮询 scope 内文本精确等于 text 的启用按钮再点击；超时抛错
// seq(view)：.pc-main-tabs → .pc-subnav-tabs → （可选）.pc-subnav-modes，依次 clickText
// mode(cli, label)：clickText('.pc-subnav-tabs', cli) 后 clickText('.pc-subnav-modes', label)
// section(id)：在 nav.section-nav[aria-label="配置分区"] 内按 dataset.navId === id 找按钮并点击（id 即编辑器局部 id）
// localNavButtons()：上述 nav 内的 button[data-nav-id]
// overflow()：选择器集合换成 .main,.view-provider,.pc-card,.pc-panel,.pc-subnav,.pc-detail-content,.editor-local-layout,.editor-detail,.editor-toolbar,.editor-section,.preset-group,.oc-group
// metrics()：{ overflow, side, grid:.editor-local-layout 的 gridTemplateColumns（没有时为 null）,
//   navs:.view-provider .section-nav 的数量, navsInEditor:其中 closest('.pac-root,.ogc-root,.pre-root,.ace-root,.oc-global') 非空的数量,
//   pageNav:.pc-workspace 的数量, subnavRows:1 或 2（第二个子元素 top ≥ .pc-subnav-tabs bottom 记 2）,
//   toolbarRows:.editor-toolbar|.pc-toolbar 内可见叶子（.et-path,.et-valid,.et-effect,.segmented,.app-button,.chip,input）按 top 取整（±4px）后的不同行数,
//   firstContentTop:首个可见 .editor-section h3 / .preset-group / .oc-group / .empty-state 的 top,
//   sections:可见 .editor-section 的 data-section }
```

**各批次 v2 → v3 差异**

| batch | 改动 |
|---|---|
| drafts | `section()` 改用局部 id；`nav(oc,'section:opencode:global:unknown')` 改为局部 `unknown` 按钮，断言源码态下 disabled。其余断言照旧（草稿保留、零新增 API、隐藏错误定位、浮层收起） |
| dynamic | `.sn-children button` 改为 `localNavButtons()`，数量仍为 N+1。限高断言从 `.sn-scroll ≤192` 改为：local `.section-nav` 的 `clientHeight ≤ parseFloat(maxHeight)`，N=20 时 `scrollHeight > clientHeight`，并且按 End 键后 `add` 按钮进入可视区。删除后继的期望值直接取局部 `data-nav-id`，不再剥 `section:<page>:` 前缀，也不再替换 `%3A`。新增标题聚焦、重名停留、取消、重命名焦点稳定、保存后无 uiId 等断言不变。**v2 的 dynamic 是在 external 模式下跑的，local 路径在页面级从未执行过，所以这批必跑** |
| lifecycle | `clickId('pi:registry'/'omp:registry'/'omp:agents')` 改为 `__ab.mode(...)`。chunk 延迟：把「7 个左栏模式＋排队静态选择」改为「subnav 三个 CLI 页签和当前 pill 可点；Pi chunk 挂起时切到 OMP · Agent 配置能出 OMP 编辑器；放行 Pi chunk 后视图仍是 OMP」（排队选择这一能力已随页面级导航删除，属预期变化）。chunk 失败：断言 subnav 在场，并保留 HEAD 的整页重载恢复断言。late-pi：「加载分区中…」改为断言 Pi 注册表 `.loading-state` 可见，之后 OMP 的 local 导航按钮数量为 2，路径为 models.yml |
| narrow | 800 档各分区逐一展开，无横溢（`section()` 改局部）。1024 档：`pi-registry` 断言 grid `144px 524px`；`opencode-presets` 断言 grid 为 null、`.pc-toolbar` 宽度 ≥ 660（整宽 680，见 §4.1）。X9 的 `.sn-children` 计数改用 `localNavButtons()`，仍为 7／9 |
| states | 7 模式的 loading/error 不再断言「左栏 7 模式」，改为：`.pc-subnav` 可见、3 个 CLI 页签和当前 CLI 的 pill 都可用；抽一例（pi-registry/loading）点 OMP 页签后能进入 OMP 编辑器。empty 与保存反馈不变 |
| coverage | 1280 九视图：`pageNav=0`；`.pc-subnav` 数量为 1；有分区的 5 个视图 `navs=1=navsInEditor`、grid `184px 712px`；预设 ×2 与 opencode-presets `navs=0`、grid null；`.pc-detail-content h2, .config-cat-card` 数量为 0；X1 计量见 §5.3。点击深度改为按 §5.3 X8 统计。底部 tip、后果句两项不变 |
| interactions | `section()` 改局部，其余不变。皮肤 RangeError 是 v2 报告的范围外既存问题，原样记录，不吞 pageerror |

---

## 4. 宽度与高度规格（重算）

### 4.1 横向：仍是 v2 的数字

断点、页边距、卡片内边距沿用 v2 §4.1，由 `isWorkspace` 两类控制，v3 保留。左栏宽度和列间隙由编辑器 local 样式提供（`.xxx-root.editor-local-layout` 的三档 media），v2 已实现，数值与页面级完全相同。

| 窗口 | 页边距／卡片内边距 | 内容区（overlay 滚动条） | 有分区的编辑器：左栏／间隙 → 右侧 | 无左栏页（预设 ×2、OpenCode 预设） |
|---|---|---|---|---|
| ≥1200（1280） | 36／16 | 1280−264−72−32 = **912** | 184／16 → **712** | 912 |
| 1024–1199（1024） | 24／16 | 1024−264−48−32 = **680** | 144／12 → **524** | 680 |
| 800–1023（800） | 16／12 | 800−264−32−24 = **480** | 112／12 → **356**（Windows 非覆盖滚动条 −16 → **340**） | 480（464） |

**任务书 308px 估算的纠正**：432 是 v1 在 36／16 边距下的内容区。v2 在窄档把页边距从 36 收到 16、卡片内边距从 16 收到 12，内容区回到 480，所以 480−112−12 = 356。subnav 是纵向占用，不吃横向预算。所以 v3 **不需要**再收窄左栏，也不新增断点。顺带一个净收益：预设页在 800 档从 v2 的 356 恢复到整宽 480。

### 4.2 subnav 单行预算

按 13px／12px 字号和 Segmented 的实际 padding 估算（只是估算，以实现后的测量为准）：
- 宽档：underline 约 164（三个标签加 2×24 的 gap），pill 约 248（`4px 12px`，容器 2＋gap 2），中间 gap 16，合计约 428，远小于 912 或 680。
- 800 档启用 §3.1 的窄档规则：underline 约 148（gap 16），pill 约 236（`4px 10px`），合计约 400，小于 464。留出约 60px 余量，用来吸收 Windows 字体和 DPI 差异。
- 预设页：underline 约 204，caption 约 130（nowrap），合计约 350，小于 464。
- 极端字体或缩放下仍允许 `flex-wrap` 折成两行，pill 落到左侧第二行，分隔线仍压在整组下方；这是兜底，不作为目标形态。

### 4.3 纵向（v3 的真实代价）

800×600、无远程横幅时，Pi Agent 首个分区 h3 的 top 估算：main 上内边距 32 ＋ PageHead 约 46 ＋ 22 ＋ 卡片 14 ＋ pc-head 约 36 ＋ 12 ＋ **subnav 约 38 ＋ 14** ＋ 工具栏两行约 76（容器 340 时按 v2 §4.2 规则分为 lead 一行、trail 一行）＋ 12 ≈ **302px**。v2 约 250。多出的约 52px 就是恢复 subnav 行的成本，主上指令要求保留顶侧页签，这部分不可规避。

不采纳的回收手段：
- **把 EditorToolbar 移到左栏上方、横跨整宽**（工具栏宽度 340 → 464）：五个编辑器都要搬 DOM（Loading、Error、工具栏、横幅移出 `.editor-detail`），fixture 结构也随之改变。而 <640 容器的工具栏规则本来就是 lead 和 trail 各占一行，464 宽依然是两行，收益接近 0。否决。日后如果要做，可作为独立议题与 EditorToolbar 断点一起评估。
- 收紧 pc-head 或 PageHead：超出本次指令范围。

local SectionNav 在 v3 中的位置：初始 top 约为 h3 的 top 减去工具栏高度，约 214–226。`max-height = max(240, 100vh−280)`，在 600 高时为 320，底边约 546 < 600；在 800 高时为 520，底边约 746 < 800。随 `.main` 滚动后 sticky 停在 top:12，没有被裁掉的风险，**不改**。

---

## 5. 验收增量

没有列出的条目继续按 v2 §8 执行。被替换的条目以本节为准。

### 5.1 V（命令均在仓库根执行）

| 编号 | v3 要求 |
|---|---|
| V1 | `npm --prefix frontend run build`，exit=0（vue-tsc 作为门禁；重点看 Segmented v-model 绑定 mode 联合类型这一处，v1 已在同样的写法下构建通过） |
| V2 | `npm --prefix frontend run test -- src/__tests__/components/provider`，4 files／31 tests 全过，测试文件零改动 |
| V3 | `npx playwright test -c e2e/pi-amagi-config.playwright.config.ts`，**7 passed**，`git diff HEAD -- e2e` 为空 |
| V4 | `node …/x-harness/v3-runtime.mjs drafts`：草稿跨分区保留、同文件切区零新增 API 调用（在 local 路径下复跑） |
| V5 | `node …/x-harness/v3-runtime.mjs lifecycle`：配置档应用重挂载、Pi→OMP 注册表不复用实例、读取错误重试、chunk 延迟或失败时 subnav 可逃逸、late-pi 不污染 OMP |

### 5.2 S（结构与不变量，主要由 `v3-static.mjs` 只读核对）

| 编号 | v3 要求 |
|---|---|
| S3（替换） | 两页各有恰好 1 个 `.pc-subnav`；页面级 SectionNav 为 0（视图文件中不含 `<SectionNav`、`pc-workspace"`、`navOwner`、`navigation-change`）。运行时 `.section-nav` 只出现在 5 个有分区的视图中，每次恰好 1 个且在编辑器根内 |
| S6（基线改为 HEAD） | `lazyPreset`、`onProfileApplied`、`handleExport`、`handleImport` 函数文本与 `HEAD:` 逐字相同；`PresetLoadError` 的 `onRetry` 箭头函数体与 HEAD 相同；`:key="presetLoadKey"` 恰好 7 处；`watch(mainTab, (tab) => {` 存在 |
| S7（收紧） | `git diff HEAD --name-only` 中 `frontend/` 与 `e2e/` 下**只有** `frontend/src/views/ProviderCenterView.vue`。Go、wailsjs、api、stores、manifests、lockfile 均为 0；五个编辑器、configNavigation、ui/* 也为 0 |
| S11（替换） | 父级不消费 `navigation-change`；snapshot 不离开编辑器（编辑器内的密钥约束沿用 v2，代码没变） |
| S13（新增） | 页签文案派生自 `CONFIG_PAGES`／`FORMAT_ITEMS`，渲染出的文本与 §3.1 列出的 v1 文案逐一相等（coverage 批次读取 DOM 断言） |
| S1／S2／S4／S5／S9／S10／S12 | 不变；编辑器零改动，按 HEAD 状态成立，S5 的落点见 §5.4 |

### 5.3 X（交互与窗口）

| 编号 | v3 口径 |
|---|---|
| X1（替换） | 「首个内容上方 chrome ≤3 条带」：条带是 `.pc-head`、`.pc-subnav`、`.editor-toolbar\|.pc-toolbar`（远程横幅只在远程模式出现，单独计数）。左栏不算条带，它和内容并列，不在内容上方。**1280×800**：九视图都恰好 3 条带，`subnavRows=1`，`toolbarRows=1`。**800×600**：仍是 3 条带，`subnavRows=1`（兜底允许 2，但必须记录），`toolbarRows ≤2`；Pi Agent 的 `firstContentTop ≤ 340`（§4.3 估算 302，再留 38 容差；超出视为 subnav 或工具栏意外折行，需定位原因） |
| X2 | 800×600：应用侧栏 264；有分区的视图 grid 为 `112px 356px`；预设页与 OpenCode 预设内容整宽；全部无横溢（`overflow=[]`，包括 `.pc-subnav`）。1024 档按 §3.6 narrow |
| X4（修订） | subnav 是原生按钮，逐个 Tab 可达（v1 行为）；local 导航单 Tab 入口，上下、Home、End 移动，Enter／Space 激活（由 e2e 结构用例覆盖）。重复名称问题已不存在：同一时刻只挂载一个编辑器的导航 |
| X8（替换） | 点击深度：同一 CLI 换模式再到叶子 ≤2（pill＋叶子）；跨 CLI 到叶子 ≤3（CLI 页签＋pill＋叶子；若目标 CLI 上次就停在目标模式则 ≤2）。动态 N=0/1/20 按 §3.6 dynamic。v2 承诺的「任意 ≤2」是页面级统一左栏的性质，按指令放弃 |
| X11（新增） | subnav 行为：切 CLI 时 pill 组随之切换；Pi 选「模型提供商」→ 切 OMP → 回 Pi，仍停在「模型提供商」；预设页 caption 随格式在「用于 Claude Code」和「用于 Codex · Pi · OMP」之间切换；选中的 underline 压在通栏分隔线上（截图核） |
| X12（新增） | 可逃逸：chunk 延迟、chunk 失败、编辑器 loading 或 error 时，subnav 全部可用（替代 v2 S3 的「左栏 7 模式常驻」） |
| X3／X5／X6／X7／X9／X10 | 不变，在 local 路径下复跑 |

截图：`v3-shots.sh 1280 800` 与 `800 600` 各出 9 张，加 dynamic 和 narrow 批次的截图。**视觉裁决由洛神负责**，至少对比 `v3-pi-agents-800x600` 与 `v2-pi-agents-800x600`、`v1 pi-agents-long-800x600`，关注顶侧层级、左栏密度和纵向留白。

### 5.4 语义承载核对（任务书 §4-7）

v2 §6.1 中挂在页面级左栏上的项，在 local 左栏中的去向：

| 语义 | v2 挂载 | v3 挂载 | 依据 |
|---|---|---|---|
| T12「下次压缩生效」 | 页面级左栏压缩叶子的 meta，加右侧 section meta | local 左栏压缩条目的 meta（来自 `PI_SECTIONS.meta`），加右侧 section meta | `PiAmagiConfig` 的 `navigationItems` 展开 `PI_SECTIONS`，`localGroups` 带上了 meta |
| 压缩「需修正」标记 | 叶子 invalid | local 条目 invalid，工具栏还有「上下文压缩待修正」定位按钮 | 同一个 `navigationItems` 计算 invalid |
| T47「受管」 | 提供商叶子的 warning meta | local 条目的 warning meta，`.pre-managed-warn` 常显 | Registry `navigationItems` 自带 meta 和 metaTone |
| Auth 类型标记 | 叶子 meta | local 条目 meta（只有 `typeLabel`） | Auth `navigationItems` |
| 完整路径 ariaLabel（「Pi · Agent 配置 · 上下文压缩」） | 页面级子叶 | **不再需要**：local 导航只服务当前文件，所属 CLI 和模式由 subnav 的选中态表达 | e2e 依赖的正是精确名「上下文压缩」，保持一致 |
| 「加载分区中…」／「分区不可用」 | 页面级 notice | 编辑器自己的 LoadingState／ErrorState，local 条目同时禁用 | 这是导航状态而不是配置语义，没有 I1 损失 |
| T02「用于 …」 | 右侧 `.pc-format-caption` | subnav 右端 `.pc-subnav-caption`（回到 v1 位置） | §3.1 |
| T04 二三级选项名 | 左栏模式入口 | 顶侧页签，文案不变 | §3.1 派生 |

结论：I1 没有丢失项。T01–T57 的其余挂载点都在编辑器内，没有变化。

---

## 6. 风险与回退

### 6.1 风险与代价

| 风险／代价 | 影响 | 处理 |
|---|---|---|
| 纵向多约 52px（subnav 行） | 800×600 下首屏表单少约两行 | 指令的必然代价。用窄档 subnav 规则防止再折行，X1 用 `firstContentTop` 守住 |
| 跨 CLI 到叶子最多 3 次点击（v2 为 2） | 跨 CLI 深链多一步 | 指令选择的层级结构。各 CLI 记忆自己的模式，缓解常见路径 |
| 不再记忆分区选择（v2 的 `sectionMemory` 删除） | 切模式或应用配置档重挂载后，分区回到默认（agents、首个提供商、provider 等），**这是相对 v2 的可观察行为变化** | 与 v1 和 fixture 行为一致，指令不要求记忆。编辑器仍有 `sectionId` prop，以后要恢复记忆只需父级传值，不必改编辑器（C1 之前） |
| local 路径在页面嵌入下没有执行过（v2 页面验收都走 external） | Registry／Auth／OMP／OpenCode 的 local 聚焦、限高滚动、删除后继可能有未暴露的问题 | §3.6 的 dynamic、narrow、drafts、coverage 批次是**必跑**，不能拿 v2 的结果顶替 |
| 动态 N 不再有子列表限高（v2 的 192／256） | N 大时 add 条目在滚动底部 | 左栏只剩当前文件的条目，没有其他模式入口会被挤走；外层 nav 限高并可滚动，End 键可达，在 dynamic 批次中验证 |
| external 分支与 snapshot emit 休眠 | 维护时可能被误读 | §3.2 C1 清单登记，触发条件明确；本文与实现报告都注明 |
| 顶侧 underline 页签没有选中态 aria（Segmented 只给 pill 设 `aria-pressed`） | 读屏用户分不清当前 CLI 或格式 | v1 的既存问题，本轮不扩大范围，作为未覆盖项上报 Leader |
| CHANGELOG（v1.3.96 条目描述的是 v2 左栏） | 发布说明与形态不一致 | 发布文档由 Leader 决定，本文不写 |
| 皮肤 RangeError、chunk 负缓存等 v2 报告中的既存项 | 与 v3 无关 | 照原样记录，不在 v3 中修 |

### 6.2 回退路径

- **v3 → v2**：v3 的生产 diff 只有 `ProviderCenterView.vue`。经 Leader 授权后，对 v3 的独立提交执行 `git revert <v3-commit>`；如果还没提交，就反向应用这一个文件的 hunk。数据、配置格式、e2e 全部不涉及。回退后用 v2 的 harness 在 HEAD=v1.3.96 下复验 V1–V3（v2-static 依赖 v1 patch 重建，不适用，以 V3 和 coverage 为准）。
- **C1 已提交后再回退**：先 revert C1，再 revert v3，顺序不能反。C1 必须单独提交，正是为了保住这个回退粒度。
- 本设计不授权任何 commit、revert、restore 或 reset。

### 6.3 实现前只读核对（实现者执行，不改现场）

1. `git rev-parse HEAD`、`git describe --tags`：确认就是 v1.3.96 发布提交。如果不是，先报告 Leader，不要动手。
2. `git status --porcelain -- frontend/src e2e`：应当为空。若有他人未提交的改动，只做归因、不覆盖，并报告 Leader。
3. 实现切片：① ProviderCenterView → V1 → V3（应零变化地通过）；② 新建 v3 harness → `v3-static.mjs`；③ 跑 drafts、dynamic、lifecycle、narrow、states、coverage、interactions 七批，每批不超过 3 分钟，批间输出进度；④ 两档九视图截图，交洛神。

### 6.4 未知项

| 未知 | 影响 | 判别办法 | 不可逆风险 |
|---|---|---|---|
| subnav 在真实 WebView2 或 WKWebView 字体下 800 档是否单行 | 折行会多约 36px 纵向 | X1 `subnavRows` 与 `firstContentTop` 实测；两个平台抽查 | 无（纯样式） |
| 主上对 Registry／Auth 进左栏的认可程度 | 可能要求改回列表或卡片形态 | 洛神出图后由主上裁决 | 无，改动在编辑器内部 |
| local 路径的潜在缺陷 | 见 6.1 | §3.6 必跑批次 | 无 |

### 6.5 本设计阶段的验证记录

- cwd `/Users/run.mao/workpace/amagi-codebox`。只用 read、grep、find，只写本文件，没有执行任何命令，不涉及退出码。
- 已读：任务书；design.md §1–3（v1 IA、N2/N3、432 估算）；design-v2.md 全文；impl-report.md 全文；`ProviderCenterView.vue` 全文；`configNavigation.ts`、`SectionNav.vue`、`Segmented.vue`、`EditorToolbar.vue` 全文；五个编辑器中 navigation、local 布局、snapshot、focusSelection、media 相关段落（grep 和分段读取）；`v1-snapshot/tracked.patch` 的 ProviderCenterView 段；`x-harness/v2-{helpers.js,runtime.mjs,static.mjs,shots.sh,ab.sh}`；e2e fixture（已内联）及 spec 中的导航选择器。
- grep 结论：`navigation-change`、`navOwner`、`SectionNav` 在生产代码中的消费者只有 ProviderCenterView 和五个编辑器；`frontend/src/__tests__`、`e2e/`、`frontend/e2e/` 都不引用 `pc-subnav`、`pc-workspace` 或页面级导航文案，因此 v3 对测试文件零影响。
- 没有核实的：git 现场与 HEAD 归属、构建和测试结果、subnav 的实际像素宽度（§4.2 是估算），以及 v3 的视觉效果。
