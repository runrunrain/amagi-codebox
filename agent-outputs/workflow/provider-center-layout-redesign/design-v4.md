# Provider Center 布局 — v4 增量规格

> CLI 导航改为上下紧贴的父子两行，第二行明确显示所属 CLI；左栏条目只显示名称；右侧用小型图标矩形归组复合内容，简单记录继续用列表。编辑器左栏切区、右侧单显及文件级保存边界不变。
>
> 本文供洛神实现。未覆盖处依次沿用 [design-v3.md](./design-v3.md)、[design-v2.md](./design-v2.md)、[design.md](./design.md)。覆盖关系以本文为准，尤其替换 v3 的同排二三级导航、左栏可视 meta，以及 v2 对所有内部记录一律去框的规定。
>
> 上游输入：[p1v4-taskbook.md](./p1v4-taskbook.md)（以派发正文冻结要求为边界）；上述设计；[impl-report.md](./impl-report.md) 全文，重点 §9 的 v3 实测。基线 HEAD=8079acb、v1.3.97、工作树干净是任务书事实，本轮未执行 Git 核验。本轮唯一写入是本文件；下文代码指令与验收均为后续实施规格，不是已实施结果。

## 1. 指令解读裁决

### 1.1 逐条裁决

| 主上指令／Leader 解读 | 裁决 | 具体落点 |
|---|---|---|
| ① 内容整理；矩形图标有时候比列表更好 | ✓；补充“收拢”的边界 | MCP、并发按作用范围归组；Registry 每个模型用图标矩形容纳复合字段；OpenCode 的 provider/agent/mcp 复用现成记录图标头，恢复轻矩形边界。全部发生在当前分区内部，不恢复整页分类卡。新归组常开，不引入新的折叠状态 |
| ② 二三级同层且分离，需重设计关系 | ✓；选 N-a 增强方案 | 二级 CLI underline 在上，三级 pill 在其下左对齐并缩进；第二行常显“Pi 配置／OMP 配置／OpenCode 配置”，明确归属。两行由同一 `.pc-subnav` 管理，加载失败时仍在 |
| ③ 左栏条目清除说明文字 | ✓；比预裁更简洁 | 只留名称，无可视 meta、invalid 文字、错误圆点或类型图标。保留原 aria 描述、选中／禁用样式和源码 notice。错误在右侧工具栏定位，风险／类型在右侧常显 |
| 左栏计数倾向保留 | 修正现场前提；本轮不加 | `ConfigSectionItem` 没有 count/badge，`SectionNav` 也没有计数渲染。现有计数都在右侧 `EditorSection`，原样保留，不新增第二份计数或推断口径 |
| Auth 凭据类型块是否矩形化 | 不采用 | 当前一次只显示一条凭据，api_key/oauth/other 三个分支互斥，没有同屏多个类型块需要归组。保留名称＋类型 Badge＋正文，不再套一层重复卡框 |

“常开”指进入该分区后全部子组直接呈现，不能点击矩形头折起，不增加导航层级。OpenCode 记录已有的展开按钮和默认收起行为继续保留，属于既有记录编辑交互。

### 1.2 二三级候选比较

| 候选 | 从属关系与窄窗 | 代价与可逆性 | 裁决 |
|---|---|---|---|
| **N-a 上下紧贴＋所属 CLI 标签** | 上层选 CLI，下层只列其配置类型；缩进与“Pi 配置”等文字同时表达关系。800px 不再让两组争同一行宽度 | 顶部改动仅在 ProviderCenterView；增加约 36px 高度，左栏限高同步减 40px。容易单独回退 | **选定** |
| N-b 同行增加所属前缀／分组框 | 前缀有帮助，但 CLI 与配置类型仍同排；加分组框后窄窗余量更少 | 最省高度，单文件 | 否决：保留了主上点名的同层观感，不能只靠再加标签修饰旧排布 |
| N-c 三级直接挂在选中 Tab 正下方 | 若跟随选中 Tab 横移，OpenCode 选中时子组靠右；要避免出界就需重定位。固定左对齐后接近 N-a | 增加连接线／定位规则，切 CLI 时操作区移动 | 否决：同等归属表达下，N-a 更稳定；不做浏览器式层叠外框 |
| N-d 二级下拉＋三级页签 | 省宽，但隐藏其他 CLI，用户不能直接比较有哪些父级 | 多一次展开点击，改变已保留的顶侧页签形式 | 否决：主上未要求隐藏 CLI，800px 也无此必要 |
| 三级移到编辑器工具栏 | 文件邻近性强 | 必须进入异步子组件；加载失败可能失去切换入口 | 继续否决：文件操作与模式导航职责混淆，破坏可逃逸性 |

二级和三级都留在 ProviderCenterView。`Segmented` 的选项名、v-model、三个 CLI 各自的模式记忆不变。顶侧 DOM 调整不影响指定 Pi e2e；X harness 的几何与分组断言需要更新。

### 1.3 本轮不变量

- **I1**：原生效时机、风险、三态、回落规则、OAuth 管理、压缩后果仍有对应分区的常显承载和原键盘可达全文，逐项见 §5.2。不能把风险只留在隐藏描述或 tooltip。
- **I2/I4/I5**：保存、解析、字段更新、懒加载、按需读取、模式切换生命周期、远程横幅、配置档／导入导出保持；不动 Go、wailsjs、api、stores、依赖、配置格式和真实用户文件。
- **I3**：`e2e/pi-amagi-config.{spec.ts,html}` 零改动；导航按钮精确名、section id、region/h3 名、`.mss-dd` 顺序、原业务断言零破坏。
- **I6**：左栏条目的可视内容只有名称。本轮不利用“状态指示”例外保留 invalid 标记。aria 描述和独立 notice 不计入条目可视内容。
- **I7**：ready＋可视化态的编辑器内仍恰有一个可见 `.editor-section`；源码／loading／error态不强行显示分区。已访问分区 `v-show` 保实例，未访问分区不提前挂载。矩形只归组该分区内的字段／记录，不增加左栏目的地。
- **C1**：external 分支、导航 snapshot、ConfigCategoryCard 等继续保留；主上仍在迭代，本轮不触发清理。

## 2. 目标结构

### 2.1 顶侧导航

```text
一级：[服务提供商 | 预设 | CLI 独立配置]      [配置档][导出完整配置][导入完整配置]

CLI 独立配置：
  Pi    OMP    OpenCode                       二级 underline，通栏底线
  ───────────────────────────────────
    │ Pi 配置  [Agent 配置 | 模型提供商 | 认证登录]  三级 pill，紧贴上行

  左栏（当前文件名称索引） │ 右栏
  角色模型                │ EditorToolbar：路径／状态／文件模式／保存
  上下文压缩              │ 文件级警示
  MCP 路由                │ EditorSection：当前分区 h3／code／计数／meta／说明入口
  并发限制                │   字段列表，或该分区内的图标矩形组

预设：
  Anthropic 格式    OpenAI 格式    用于 Codex · Pi · OMP
  ───────────────────────────────────────────
  筛选／添加 + 原分组列表（无左栏）
```

预设 caption 是当前格式的说明，不是第三层入口。它与格式页签同排、紧邻左侧页签组，取消右端 `space-between` 分离；沿用 `formatConsumers` 的两个原字符串。无需给预设增加一行或给 caption 加 pill／边框。窄窗极端字体下可自然折到页签下方。

### 2.2 右侧内容形态

| 层 | 形态与职责 |
|---|---|
| EditorToolbar | 整文件动作与状态，不包进内容组 |
| EditorSection | 当前分区的唯一 region/h3；无外框，标题与原 code/count/meta/InfoTip 保持 |
| 图标矩形内容组 | 复合配置的一组字段或一个复合模型记录；小图标＋原标签／编辑头，正文不缩进到图标下面。单列排列，不做磁贴网格 |
| 简单记录列表 | 角色→模型、名称→值、路径、权限枚举等直接比较的记录；保留列表与原按钮 |
| InfoTip／错误 | 说明入口留在原标签附近；校验与后果不因归组被折起；文件级错误仍在工具栏下 |

角色模型、上下文压缩、Auth 等已通过左栏隔离成单一任务，不为统一外观强行再套矩形。所有新图标只从 `icons.ts` 读取，不能用 emoji、外部图像或把配置值注入 `v-html`。

## 3. 逐文件增量指令

以下路径前缀均为 `frontend/src/`。预计生产改动为 **5 个既有文件＋1 个新增组件**；Auth、OMP 等虽需回归，不需要为了净化左栏修改其业务脚本。

### 3.1 `views/ProviderCenterView.vue`

仅改 subnav 模板、展示 computed 和局部样式／注释。保留 `.pc-subnav-tabs`、`.pc-subnav-modes`、`.pc-subnav-caption` 三个定位类。

**CLI 模板规则**

- `.pc-subnav` 根据一级页加 `pc-subnav--cli` 或 `pc-subnav--presets`。
- CLI 下第一子节点仍为绑定 `cliModel` 的 underline Segmented。
- 第二子节点新增 `.pc-subnav-context`，内部顺序为 `.pc-subnav-owner` 文本和原三分支模式 Segmented。标签是“{{ currentCliLabel }} 配置”；组设 `role="group"`，aria-label绑定为 `currentCliLabel + ' 配置类型'`。标签非按钮，不可聚焦，不添加 h2。
- `currentCliLabel` 只从现有 `CLI_TABS` 按 `cliModel` 查得，不重复写 Pi/OMP/OpenCode 常量；既有选项仍派生自 CONFIG_PAGES／FORMAT_ITEMS。
- 三级三分支的 v-if、v-model、options 不变。缩进是固定左缩进，不能随选中 CLI 横向移动。
- 两行都在 `.pc-detail-content` 和异步组件边界之外；不把 subnav 放进以 `activePageId` 为 key 的节点。
- 预设分支仍是格式 Segmented＋原 caption，DOM 顺序不变，仅收拢两者间距。

**尺寸与样式**

| 对象 | 规格 |
|---|---|
| `.pc-subnav--cli` | flex column；align-items:stretch；gap:6px；无整个容器的 bottom border；min-width:0 |
| CLI `.pc-subnav-tabs.underline-tabs` | 宽 100%，自身 1px separator 底线；页签 13px，padding `9px 2px`，显式 line-height:16px；正常总高约 36px；gap 宽档24／窄档16 |
| `.pc-subnav-context` | flex，align-items:center，justify-content:flex-start，wrap；gap `6px 8px`；margin-left:12px；border-left:2px solid separator；padding-left:10px；min-width:0；min-height:30px |
| `.pc-subnav-owner` | 12px／18px，secondary，nowrap；内容宽；无底色、图标或额外边框 |
| `.pc-subnav-modes` | margin-bottom:0；inline-flex；容器 padding2、radius8；seg line-height18、padding `4px 12px`（≤1023 时水平10）；12px；正常总高30px。允许整组换行，组内极端放大时也可 wrap；不裁标签 |
| `.pc-subnav--presets` | flex row，align-items:flex-end，justify-content:flex-start；gap `8px 12px`；wrap；容器1px底线，内部 underline 底线为0；caption 保留12px与 padding-bottom8 |
| 上下间距 | pc-head→subnav 12px；subnav→编辑器14px，继续使用原 card/panel gap。两行之间不能再插面板 margin 或文件工具栏 |

确保选择器是根节点组合选择器或正确的 `:deep(.seg)`；不能写 `.pc-subnav-tabs .underline-tabs` 误认其为后代。CLI 的 underline 下边缘对齐**第一行**底线，不能仍对齐整个两行容器底部。

**逐字保留的逻辑**：`formatModel`/`cliModel`、全部现有选项常量、三个 mode ref、`formatConsumers`、`activePageId`、`presetLoadKey`、`isWorkspace`、`lazyPreset`、六个异步声明、完整 PresetLoadError（含 onRetry sessionStorage 重载防循环）、所有 watch、配置档／导入导出函数。7 处 `:key="presetLoadKey"` 和外层 `:key="activePageId"` 不变。服务提供商、详情、远程、一级和动作区模板不动。

### 3.2 `components/ui/SectionNav.vue`

净化在此集中实现，所有现有消费者同时生效，不向每个编辑器加净化开关。

1. 一级 item 与 child 两段模板都只渲染可视 `.sn-name`。原 `.sn-meta` 改成 `.sn-description` 隐藏描述节点：有 `meta || invalid` 时存在，保留原 `descriptionId()` 与 `aria-describedby`。
2. 描述内容为 `[item.meta, item.invalid ? '需修正' : ''].filter(Boolean).join('；')`；child 同理。可以抽一个纯展示 helper，不改变 id、label 或 ariaLabel。不能保留孤立的“ · ”。
3. `.sn-description` 使用标准 visually-hidden CSS：`position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);clip-path:inset(50%);white-space:nowrap;border:0`。**不用** `display:none`、`visibility:hidden` 或 `aria-hidden`，不把描述塞入 aria-label。
4. 删除仅服务可视 meta 的字体／warning 样式；保留 `.sn-notice` 样式和其文字。名称仍可换行，按钮尺寸、选中／branch／disabled／focus-visible 样式及 roving 行为不变；不新增错误图标、红点、计数。
5. `max-height` 从 `max(240px, calc(100vh - 280px))` 改为 `max(240px, calc(100vh - 320px))`。sticky top12、overflow-y:auto、children 和 scrollChildren 继续保留。限高变化只补偿新增导航行，推导见 §6.3。
6. `meta/metaTone/invalid` 数据接口保留，navigationItems、snapshot 和 configNavigation 均不删字段。它们继续用于读屏描述和休眠 external 路径；本轮清的是可视层。

不得修改 onKey、activate、preferred、focusId watch 与 `scrollIntoView`；精确可访问名仍来自 `ariaLabel || label`。Auth 类型字符串、受管描述依旧只能是已有展示元数据，不能带 key/access/refresh/accountId/expires。

### 3.3 新增 `components/provider/ConfigContentGroup.vue`

这是无状态的内容归组外壳，用于 Pi 的六个字段组与 Registry 的模型记录，避免复制多份图标／边框／窄窗规则。现有 `ConfigCategoryCard` 带折叠按钮、36px 缩略图和 `v-if` 卸载；本轮不需要这些行为。不能把它固定 `expanded=true` 后留下无效折叠按钮，也不扩展 EditorSection 来混用 region 与字段组。

**接口**

```ts
interface Props {
  icon: string;
  label: string;
}
```

两个属性均必填，不接受配置对象，不产生事件。具名slot `header` 放原FieldLabel或模型编辑头，默认slot放原字段内容；两槽均由调用方提供。Pi的label精确采用原FieldLabel.text（如“Leader 服务器”“默认并发”）；Registry使用下文规定的模型序号。

```text
div.config-content-group[role=group][aria-label=label]
  div.ccg-head
    span.ccg-icon[aria-hidden=true]  ← getIcon(icon) 的静态 SVG
    div.ccg-heading                ← header slot
  div.ccg-body                     ← default slot，始终渲染
```

- `getIcon/getAccent` 引用 `./icons`；默认图标兜底沿用库。icon 只能由调用模板中的常量指定，不从用户字段取 SVG。背景用已有 `--control`，图标前景用 getAccent；不新增 token。
- 外壳1px separator、radius8、背景 `--card`、shadow none；padding12；header/body 间距8。无 hover 抬升或边框变色、无 pointer cursor、无 tabindex、无 chevron、无 expanded prop。
- icon 为24×24、radius6，SVG16×16；header flex 对齐中心、gap8，heading `flex:1;min-width:0`。原 FieldLabel 保留 text/code/tip/items/effect，宽度100%，可换行。
- body 不跟随图标缩进，`min-width:0;display:flex;flex-direction:column;gap:6px`；Registry 可在宿主内把其 body gap 设10。
- `@container editor-detail (max-width:479px)` 外壳 padding8；无容器查询支持时也用 padding8。所有层 `min-width:0`，不设固定高度或 overflow:hidden。
- 不提供任何 section-active 上下文，后代继续继承外层 EditorSection 的可见性；默认 slot 没有额外 v-if/v-show。该组件不使用 `.editor-section`、h3 或 `role=region`。

### 3.4 `components/provider/PiAmagiConfig.vue`

只增加 ConfigContentGroup import、MCP／并发模板归组及样式；脚本其余业务、navigationItems、文案常量全部不变。

- 在 mcp 和 concurrency 的 EditorSection 默认 slot 内各加 `.pac-content-groups`（flex column、gap12、min-width0）。各有3个 ConfigContentGroup，顺序及 icon 映射见 §4.2。
- 把原6个 `.pac-field` 的 FieldLabel 移入各组 header slot；其后的原控件原顺序移入 body。只改变容器，不拷贝字段／tip，不增加第二个同名标签。
- 新组之间使用 gap12，不再吃 `.pac-field + .pac-field` 的14px margin。该旧规则继续服务未改的 agents 等字段；不要全局移除。
- 组内列表、TextInput、Dropdown、三态 Segmented、placeholder、添加／删除及所有事件保持。默认并发的180px输入不缩窄，确保“留空 = 内置默认 4”可读。
- 保留并补齐现有 <480px 收缩规则：每个组 body 内 input 最大宽100%、min-width0；服务商/模型 custom 行换行；模型 dropdown 与 servers input 可独占整行。三态按钮在330px正文宽内可用，不能为了卡头加入额外 Dropdown。
- 角色模型／profile、上下文压缩的模板完全不改；压缩 code/meta/InfoTip/计数与 DOM 顺序原样。`nav[aria-label="配置分区"]` 内按钮accessible name精确为“上下文压缩”；`.editor-section[data-section="compaction"]` 的region/h3同名；custom列表态 `.mss-row > .mss-dd` 仍按provider→model→level排列，未选provider时不伪造第三个trigger，手动态input仍恰1个。旧注释中“卡片”可改为“分区”，不得借注释整理重写逻辑。

### 3.5 `components/provider/ProviderRegistryEditor.vue`

同一修改覆盖 Pi JSON 与 OMP YAML。脚本只增组件 import，其他函数、navigationItems、key、事件不动。

**提供商分区正文次序改为：**

1. 原受管 StatusBanner（有则常显、始终在最前）。
2. 协议／Base URL／API Key 的原 `.pre-fields-grid`。
3. 原 `.pre-models`，模型条目归组。
4. 原“其他字段”块，作为 provider 级扩展字段，保留所有添加／删除／递归编辑入口。
5. 删除当前提供商。

将 provider 级“其他字段”从模型前移到模型后，使常用连接和模型连续；这是本轮明确的阅读／Tab 顺序变化，字段值和保存行为不变。模型内“高级字段”仍紧随该模型基础字段，不能移到提供商级。

**模型归组**

- 原 `v-for="(m, idx) in providerModels(name)" :key="idx"` 的 `.pre-model` 外壳替换为 ConfigContentGroup，保留 class `pre-model`；`icon="model"`，`label` 用“模型 ${idx + 1}”，不依赖可变 model id 作为 key。
- header slot 放完整原 `.pre-model-head`：id输入→空id警告→推理 Switch→删除。移位时不让 header 变成 button；不再重复输出一份模型名／标题。
- body 放原 `.pre-model-grid` 和该模型 `.pre-unknown` 高级字段块。thinkingLevelMap、thinking、input、cost、未知字段全部保留原专用编辑器；不能回退 JSON 或删除空态操作入口。
- 移除原 `.pre-model` 的无框、padding和布局重置，外壳样式交给新组件；移除 `.pre-model + .pre-model` 的列表分隔线，模型之间沿用父级10px gap，不叠双线。模型内部高级字段仍用分隔线，无第二层矩形。
- `.pre-model-head` 保留flex-wrap/gap，补 min-width0、width100%；id input 在窄档可独占整行。移除高级字段 head 操作组对横宽的隐性最小要求，按钮可换行、不减字。
- provider“其他字段”、root 的未知键、add 表单继续无框，现有空态和后继／聚焦规则不变。保留 modelCount 在右侧 section 标题中的计数。

### 3.6 `components/provider/OpenCodeGlobalConfig.vue`

只改本文件内作用于子编辑器的 scoped deep CSS；不改4个 MapEditor、ModelSubEditor、递归编辑器的源码或生命周期。

| 精确范围 | 增量样式 |
|---|---|
| `.provider-editor > .pe-card`、`.agent-editor > .ae-card`、`.mcp-editor > .me-card` | 恢复轻矩形：1px separator、radius8、`--card`底、shadow none、min-width0；列表gap10；删除针对这三类相邻记录的border-top规则；hover边框仍separator |
| 上述三类的 `*-thumb-head` | 保留原真实展开button／aria-expanded；padding `8px 12px`（collapsed相同）；hover用controlHover；图标缩略块24、SVG16，取消抬升transform；不得复制新增一份图标头 |
| 上述三类的 `*-expanded` | padding `10px 12px 12px`；保留头／内容分隔线；<480时左右8。卡框不裁字段焦点环，必要时将记录 overflow:hidden 改为visible并给头部圆角 |
| `.perm-editor` 及其直接 `.pe-card` | **继续无框列表**，gap0、相邻1px separator、head/body水平padding0；权限是名称→枚举，矩形会放大一行规则 |
| `.provider-editor .pe-model-card` | **继续无框行组**，padding `10px 0`、相邻separator；不恢复原灰底模型小卡，避免 provider矩形里再套模型矩形 |
| `.variants-editor .vme-row` | 继续透明底、无圆角，不套卡 |

拆开目前合并的 deep 选择器，不能简单删除全部 v2 覆盖使 permission 和嵌套模型一并恢复卡片。加明确的 `.oc-global` 宿主约束／当前 scoped 祖先，不影响其他消费者。

窄容器除保留现有收缩外，补 `.ae-field-row` 单列、`.ae-tool-row/.pe-model-head` 可换行、其输入 min-width0/max-width100%；MCP 的 `.me-reveal-btn` 与其他敏感值动作保持不缩小、不竖排。缩略图头的 `*-thumb-meta` 允许换行，名称可断行，badge/chevron不缩小；过长模型摘要允许沿用原省略，不截风险／错误全文。以上在 `@supports not (container-type: inline-size)` 同样落地。

### 3.7 明确零改动文件

| 文件 | 处理 |
|---|---|
| `provider/AuthConfigEditor.vue` | 原右侧类型Badge、OAuth管理／InfoTip、名称编辑和互斥正文已经足够；左栏通过SectionNav统一净化。navigationItems.meta保留为隐藏描述 |
| `provider/OmpGlobalConfig.vue` | 角色／覆盖保持可比较列表；左栏通过共享件生效；原新会话标签不动 |
| `provider/configNavigation.ts` | 类型、静态meta、顺序、id与休眠exports均保留；不加count |
| `ui/Segmented.vue` | 继续提供原按钮和选中样式；不把本轮扩成全应用tablist/键盘模型改造 |
| `ui/EditorSection.vue`、`EditorToolbar.vue`、`FieldLabel.vue` | DOM、props、样式不改，原h3／计数／meta／字段语义复用 |
| `provider/icons.ts`、`ConfigCategoryCard.vue` | 只引用既有图标；库和休眠卡组件不改、不删、不在两页重新引入ConfigCategoryCard |
| `provider/PresetList.vue`、`OpenCodePresets.vue` | 继续分组列表和原弹窗／JSON只读；不改交易边界和工具栏 |
| `InfoTip/Dropdown/sectionVisibility`、所有Map/Value子编辑器、tokens | 保持既有行为与资源；所需局部覆盖写在本轮宿主，不做全局重构 |
| `e2e/pi-amagi-config.{spec.ts,html}`、4个provider逻辑测试及其逻辑模块 | 零改动，只复跑 |

## 4. 内容收拢判据与逐块应用

### 4.1 判据

图标矩形用于以下两种情况：同一分区有作用范围不同的复合字段组；或者同类记录各自包含基础字段、子记录和扩展字段，用户容易看错字段属于哪条记录。图标帮助识别对象，边框确定其内容范围。

名称→值的短记录、需要逐行比较的规则、单一流程、已经由左栏选出的唯一对象，继续用列表／普通表单。字段多本身不等于每个字段都要套框。每条内容路径最多一层新增矩形；分区外壳和内部简单列表不再套框。

新矩形常开，既有折叠交互不新增、不扩散。这样不产生新的点击成本、草稿卸载或隐藏错误问题；长配置仍需页面滚动，本轮不承诺缩短所有表单。

### 4.2 应用点清单

| 分区／内容块 | 形态、图标 | 保留与理由 |
|---|---|---|
| Pi 角色模型：profile | 普通FieldLabel＋选择器 | 单字段，不套框 |
| Pi 角色覆盖 | 原角色行列表 | role→model映射需要比较；ModelSpecSelector保留完整形态 |
| Pi 上下文压缩 | 原常开流程表单 | 只有一个压缩模型决策；三态→当前态→选择器→后果→复制→错误顺序不变，I3强约束 |
| Pi MCP：Leader服务器 | 常开矩形，`mcp` | header复用含“重启 pi 生效”的FieldLabel；三态和名单放body |
| Pi MCP：默认服务器 | 常开矩形，`mcp` | header复用含“即时生效”的FieldLabel，与Leader作用范围区分 |
| Pi MCP：角色附加服务器 | 常开矩形，`agent` | 包住原角色→服务器列表；列表行本身不套框 |
| Pi 并发：默认并发 | 常开矩形，`concurrency` | 与另外两个覆盖范围构成同级配置组；保留原placeholder和tip |
| Pi 并发：服务商并发 | 常开矩形，`provider` | 组内每个服务商限制仍为列表 |
| Pi 并发：模型并发 | 常开矩形，`model` | 组内每个模型限制仍为列表；整体顺序默认→服务商→模型不变，优先级在原section tip |
| OMP 角色模型 | 原行列表 | 角色与spec映射不需要重复标题 |
| OMP 子代理覆盖 | 原行列表 | 保留@role／直接spec切换和删除；不把两种形态变成新类型卡 |
| Registry 提供商连接字段 | 原字段网格，窄档单列 | 当前提供商已被左栏单独选中，外层不套框 |
| Registry 每条模型 | 常开矩形，`model` | id／推理／基础字段／高级字段形成独立记录；多个模型不再靠一条细线区分全部字段 |
| Registry 模型高级字段／费用／级别映射／未知值 | 矩形内部原分隔列表与编辑器 | 禁止二次套框，单位、字段类型、全部操作不动 |
| Registry provider级其他字段 | 原列表，移到模型之后 | 降低扩展入口打断常用字段的程度；未隐藏、未折叠 |
| Registry root／add | 原未知键列表／添加表单 | 单一任务；保留真实空态与操作 |
| Auth api_key | 原密码表单＋extra字段列表 | 已是唯一当前凭据；类型Badge常显，不再造“API Key卡” |
| Auth oauth | 原管理标签／InfoTip／只读状态 | 不引入令牌摘要或自造登录能力；现有名称编辑／删除保留 |
| Auth other／add | 原递归编辑／添加表单 | 未知类型保真；OAuth登录途径和未认证建议不动 |
| OpenCode provider记录 | 轻矩形，复用现有provider图标头 | 复杂对象，原折叠可快速浏览；内部模型不再套矩形 |
| OpenCode agent记录 | 轻矩形，复用现有agent图标头 | prompt/tools等归属清楚，原折叠不变 |
| OpenCode mcp记录 | 轻矩形，复用现有mcp图标头 | remote/local异构字段属于各自服务器，原折叠不变 |
| OpenCode permission | 原无框折叠列表行 | 名称→枚举，保持紧凑；本轮不借整理取消原展开行为 |
| OpenCode model／instructions／plugin | 原输入／字符串列表 | 单值或同质短条目 |
| OpenCode experimental／unknown | 原键值／RawJsonEditor列表 | 兜底数据不可推断出新的语义类型；保留非法局部草稿边界 |
| 两格式预设／OpenCode预设 | 原分组列表 | 主上已认可；点击打开原弹窗，JSON仍只读 |

## 5. 左栏迁移与语义承载

### 5.1 每项 meta 的去向

| 现有左栏来源 | v4可视处理 | 右侧常显落点 | 无障碍／失败定位 |
|---|---|---|---|
| PI_SECTIONS compaction.meta「下次压缩生效」 | 删除可视meta | 压缩EditorSection的同名meta，已经存在 | 原压缩标题InfoTip全文；左栏隐藏描述保留 |
| Pi navigationItems compaction.invalid「需修正」 | 删除可视文字，不留红点 | 当前为压缩：原局部StatusBanner alert；当前为其他区：EditorToolbar中“上下文压缩待修正”定位按钮；源码态：原status文字 | 左栏aria-describedby仍读“需修正”；保存拒绝逻辑不变；e2e未断言左栏invalid字样，但断言了右侧定位按钮 |
| Registry amagi-* meta「受管」、metaTone warning | 删除可视meta | 原section标题“受管”＋`.pre-managed-warn`全文，均常显，且正文排序仍警告优先 | 隐藏描述保留“受管”；不能用图标矩形替换警告横幅 |
| Auth row.typeLabel（api_key/oauth/未知类型或自定义类型） | 删除可视meta | 原`.ace-entry-head`中的source型Badge；OAuth的管理标签与InfoTip仍相邻 | 隐藏描述保留typeLabel；不复制到EditorSection形成第二份Badge |
| 右侧计数 | 不迁入左栏 | 原EditorSection.es-count，包含0，表达式不变 | 不新增count API或修改导航按钮名称 |
| 源码notice、loading/error | notice不动，条目禁用语义不动 | 原文件级LoadingState/ErrorState和源码编辑器 | 本指令只净化条目；不删除独立源码notice，不借此改加载骨架 |

### 5.2 I1 逐项复核

“常显”沿用 v2 定义：进入对应分区后无需展开说明。文件级状态在该文件的工具栏常显，不要求用户未进入压缩分区时仍在左栏重复看到压缩生效时机。

| 原语义／编号 | v4右侧常显承载 | 键盘可达全文与不可丢细节 |
|---|---|---|
| MCP Leader重启 T19 | Leader矩形header内原FieldLabel effect“重启 pi 生效” | 原tip：装配读一次、与子Agent路由独立 |
| OMP新会话 T31 | OMP EditorToolbar“新会话生效”，源码也在 | 不移入某个角色行 |
| MCP默认即时 T20 | 默认服务器矩形header内原FieldLabel effect“即时生效” | 原tip：每次派发读盘、与角色附加取并集 |
| OpenCode即时 T41 | OpenCode全局EditorToolbar“即时生效” | 原全部分区／源码有效 |
| 下次压缩 T12 | 压缩EditorSection meta“下次压缩生效” | COMPACTION_TIP第2/3段：项目优先、目录继承、新环境凭据需重派发、pi管理认证 |
| 注册表重进 T46 | Registry EditorToolbar“重新进入后生效” | REGISTRY_EFFECT_TIP：重进Agent配置刷新候选 |
| amagi-*覆盖风险 T47 | 提供商section的“受管”及原warning横幅 | 手动修改可能下次同步被覆盖，全文不折起 |
| MCP三态／空列表 T18/T21 | Leader/default三态控件与各自StringListEditor空态文字 | 原FieldLabel三态对照；未设置=全部、[]=不加载、空白名单保存等同不加载、default的角色附加例外 |
| 默认并发清空回落4 T24 | 默认矩形body的原180px输入placeholder | 原FieldLabel tip：清空移除键；应用预填10与内置4分清 |
| OAuth只读／CLI管理 T53–55 | 当前OAuth正文的“由 pi CLI 管理”；无凭据时add空态的pi CLI登录途径 | 原OAUTH_TIP：此处只读，access/refresh保存时原样保留；不改变名称编辑／删除权限 |
| 压缩窗口不足／调用失败 T13 | custom态原后果句“窗口不足或调用失败时取消本次压缩，历史保留” | 原标题tip第4段仍含配置／认证／摘要失败；不能只保留tooltip |

T02 caption随格式更新；T04选项名不变；T09/T15 profile与角色覆盖说明原位；T23并发优先级留在section tip；T28/T29/T34/T51/T57错误仍为文件级。T01–T57其余条目按上游保留，不恢复已删除教学段落。角色模型、profile、并发、Auth生效时间仍未知，不补造标签。

## 6. 宽度与纵向规格

### 6.1 宽度预算

v3报告已纠正滚动条前提：base.css的WebKit滚动条宽8px；隐藏滚动条的浏览器测量不能冒充真实占位宽。下表中的s是页面实际占用的滚动条宽（正常有滚动时按8；无滚动时0；16只作保守探针）。

| 窗口 | 内容整宽 | 编辑器左栏／gap | 右侧宽度 | 新矩形正文净宽 |
|---|---|---|---|---|
| 1280 | 912−s | 184／16 | 712−s；s=8时704 | padding12＋border1两侧：686−s，实为678 |
| 1024 | 680−s | 144／12 | 524−s；s=8时516 | 498−s，实为490 |
| 800 | 480−s | 112／12 | 356−s；s=8时**348** | padding8＋border1两侧：338−s，实为**330** |
| 800保守16px | 464 | 112／12 | 340 | 322 |

无左栏的预设×2／OpenCode预设仍用整宽。新矩形不改变外层grid，不可把330当作“右栏宽度”；它只是局部内容净宽。外层仍 `minmax(0,1fr)`，卡正文不继续为图标缩进。

CLI两行各自用整宽：第一行三个标签约150–170px；第二行按缩进24＋所属文字约44–100＋gap8＋Pi模式组约236–248估算，保守不超过380px，小于464。OpenCode标签较长但只有两个模式。预设页签＋caption约350px以内。以上是预算，Windows字体和放大必须实测。

- 800档新矩形全部单列，不尝试把三个MCP组／并发组并排。
- <480的字段收缩按**右侧容器**触发，进一步减少的18px卡内宽由组内wrap/min-width0承担；不要只测试348px外壳而不检查330px内层。
- 不改应用侧栏264、main最小800×600、PageHead、一级动作区、EditorToolbar断点。
- 不为压缩纵向而让风格字号低于原12px，也不隐藏保存、生效或风险文字。

### 6.2 高度预算与验收上限

引用 `impl-report.md §9.3.3–9.3.4` 的真实基线：800×600、无远程横幅、正常ready态，Pi首个h3≈348.5（取整349）；PageHead65.5、pc-head76、旧subnav36、工具栏65。不得再用v3早期302px估算。

v4 CLI subnav目标高为 **36＋6＋30＝72px**，相对v3增加36px。其他上方间距不变：

```text
32 + 65.5 + 22 + 14 + 76 + 12 + 72 + 14 + 65 + 12
= 384.5px ≈ 385px（Pi Agent firstContentTop）
```

| 场景 | v3实测 | v4估算／要求 |
|---|---|---|
| Pi Agent @800×600 | h3 top349；工具栏2行 | h3 top约385，基准fixture上限400；首个profile字段原约391→约427，首屏仍有实际输入，不只有导航 |
| OMP／Registry／Auth／OpenCode全局 @800 | 基准首内容约349 | 同一新增36px预算；基准页目标≤400。长名称、多行warning、错误另测实际增量，不裁文案凑线 |
| OpenCode预设 @800 | firstContentTop389，工具栏3行／105px | 约425，上限440；明确接受既有搜索／路径／动作三行，本轮不改成2行 |
| 公共预设×2 @800 | 首内容313 | 一行格式导航不增高，目标≤330 |
| CLI七视图 @1280×800 | 首内容276，工具栏1行 | 约312，基准目标≤330；公共预设仍约273，目标≤290 |

新矩形位于h3之后，不增加 `firstContentTop`，但其padding/header会增加实际表单高度。验收同时记录 `firstEditableTop`（首个正文input/textarea/字段选择器位置），不能靠h3达标掩盖空卡头。Pi默认角色页首个字段保持原形态；新MCP／并发组首个操作目标在800档首屏内可见（无错误基准下底边≤600）。

正常字体下CLI `subnavRows=2`、预设=1；不再沿用“二三级必须同排”或“可视chrome最多3行”的旧验收。结构容器仍是pc-head/subnav/toolbar三块，但CLI subnav实际是两行，报告须同时写物理行数。字体／DPI放大或远程横幅可导致更多行，须记录成因、保持可滚动与无横溢，不挪走风险文字或放宽正常基准来掩盖问题。

### 6.3 左栏限高补偿

v3 800×600的编辑器／左栏自然top约为 `349−65−12=272`；新增36后约308。若仍用320px限高，底边约628，首屏End目标可能落到视口外。

采用 `max(240px, 100vh−320px)`：600高时限高280，底边约588，余12px；相比旧值减少40px，覆盖36px增量。800高时限高480。保持sticky top12；页面滚动后不动态放大左栏，也不锁右栏滚动。动态N=20时必测nav自身滚动、End后add可见、焦点和页面滚动，不以“名称变短了”推断通过。远程横幅使自然位置下移时允许主滚动容器滚动到导航，不新增JS定位逻辑。

## 7. 验收增量与 harness v4

本节是实现后的L2要求。本设计阶段未运行命令、未执行浏览器、未审图。视觉裁决由洛神承担。

### 7.1 V：命令与行为

命令cwd为仓库根；后续harness落在现有 `agent-outputs/workflow/provider-center-layout-redesign/x-harness/`，不新增第三处e2e目录。

| 编号 | v4要求 |
|---|---|
| V1 | `npm --prefix frontend run build` exit0；重点检查新组件slot、图标import和导航模板 |
| V2 | `npm --prefix frontend run test -- src/__tests__/components/provider`；原4文件/31测试全过，文件与业务断言不动。若基线数量变化，先归因，不硬凑数字 |
| V3 | `npx playwright test -c e2e/pi-amagi-config.playwright.config.ts`；7 passed，e2e两文件相对冻结基线零diff |
| V4 | v4-runtime `drafts`：原草稿／同文件零新增API／隐藏压缩错误定位／跨区关闭Teleport浮层全部复跑，新增在MCP矩形内打开tip再切区；OpenCode非法RawJson草稿仍保留 |
| V5 | v4-runtime `lifecycle`：模式记忆、配置档重挂载、Pi→OMP Registry不复用、chunk延迟／失败与读取错误可逃逸、late-pi不污染。操作作用域改为新父子组，不靠Vue状态注入 |
| V6 新增 | v4-runtime `content`：Pi新增／改动MCP及并发行后源码／保存值与原契约相符；Registry双引擎模型新增、改id、增高级字段、删除、源码往返；OpenCode三类记录原展开／新增／删除操作。只用隔离fixture，核对完整值及未知字段保留，无新增自动保存／请求 |

fixture仅隔离Wails transport，不冒充真实磁盘／后端证据。新常开组不引入UI展开状态，因此不存在“收起后保存”的新路径；OpenCode已有记录折叠的局部草稿边界仍按既有行为，不扩称全面修复。

### 7.2 S：结构与变更边界

| 编号 | v4增量 |
|---|---|
| S1/S2 | 旧h2/教学hint不恢复；当前EditorSection保有独立h3；6个EditorToolbar使用者不变。新group不产生region/h3 |
| S3 替换 | 每页1个subnav；CLI上下两行，三级在context内；父级无SectionNav。6个有分区的视图（5编辑器，Registry复用两次）各1个local nav；其余3视图无nav |
| S4/S5 | 按§5逐项核对原语义的右侧常显与键盘全文；隐藏aria描述不计作可视风险承载 |
| S6 | 视图既有业务／loader脚本、五编辑器全部保存／解析／字段更新函数与冻结基线相同；Pi/Registry仅新增组件import；OpenCode脚本不变。7个presetLoadKey与1个activePageId key不变 |
| S7 替换 | 生产白名单：ProviderCenterView、SectionNav、PiAmagiConfig、ProviderRegistryEditor、OpenCodeGlobalConfig、新ConfigContentGroup。禁区与§3.7零改动清单均核对，含未跟踪文件；不因v3只改单文件而误判v4，也不扩大到全部ui/provider文件 |
| S8/S9 | 无依赖；ConfigCategoryCard与icons字节不变；两页无ConfigCategoryCard import/实例，不伪造旧选择器 |
| S10/S11 | 单显／visited／active上下文与密钥隔离不变；父级仍不消费snapshot，C1保留 |
| S12 替换 | 允许§4白名单内图标矩形；EditorSection无框、没有整页分类卡网格。permission／嵌套模型／variants继续无框；MCP并发每区3组、Registry每模型1组；未访问分区不为统计提前挂载 |
| S13 | 顶部原选项字符串不变；所属CLI标签派生自同一导航表，与选中CLI一致；preset caption仍为原两个值 |
| S14 新增 | SectionNav的item与child可视内容均仅名称；无sn-meta、invalid可视文字／图标／count。aria-describedby指向真实且只读屏可见的描述；accessible name仍精确。notice独立保留 |

S6以冻结的v4实施基线提交内容为比较对象（任务书8079acb；开工先解析完整SHA并记录）。不要再用v3-static写死的v1.3.96 tag断言或“删除14项／新增6项”清单。ProviderCenterView脚本唯一预期新增为currentCliLabel；其余白名单是组件import和SectionNav展示helper，不放行其他业务更改。

### 7.3 X：行为、窗口与视觉

| 编号 | v4口径 |
|---|---|
| X1 替换 | 九视图按§6记录subnavRows、toolbarRows、firstContentTop、firstEditableTop；CLI两行／预设一行是正常形态。采用385／425估算与400／440基准上限，保留原PageHead/pc-head实际分层，不再沿用v3的340上限 |
| X2 | 800×600、1024×768抽测、1280×800；s=0/8/16分开记录。800有滚动时右侧348、矩形正文330；无横溢（新增检查group/head/body和OC展开区），不能只测外壳 |
| X3/X5 | 风险、压缩后果、MCP空名单、默认4可完整读取；矩形内FieldLabel的InfoTip可键盘打开、视口内定位、Escape回焦；切区后无残留 |
| X4 | 原顶侧按钮Tab顺序先CLI再模式；新增所属标签不进Tab。左栏roving/Enter/Space/精确名不变；隐藏描述不增加高度或焦点。新矩形头不变成额外按钮 |
| X6/X7 | 三种预设列表／原弹窗交易、JSON只读、皮肤下tip实底继续验证；已有范围外错误如实报告，不吞pageerror |
| X8 | 同CLI换模式到分区≤2次、跨CLI最坏≤3次；新归组0次展开点击。动态N=0/1/20，nav新限高、End可见、新增聚焦、删除后继、Auth改名稳定和保存不带uiId |
| X9/X10 | OpenCode 7/9条件分区及回退、整文件源码／导航禁用／有效分区返回不变；隐藏压缩错误仍可从右侧工具栏定位 |
| X11 替换 | 三级group位于父级行下面，owner文字与当前CLI一致，左缩进固定，切CLI不横移；三CLI各模式记忆与预设caption更新不变 |
| X12 | chunk/loading/error期间CLI行和当前context全部可用；当前owner不能短暂变成上一CLI或空字符串 |
| X13 新增 | 图标矩形归组严格对应§4；普通列表无多余卡框，原信息／动作未被新容器隐藏。至少查看Pi MCP/并发、双Registry多模型、OpenCode provider/agent/mcp展开态两档截图 |
| X14 新增 | 对压缩合法/invalid、受管长名、Auth三类型分别截图：左栏只有名称；切到右侧能看见对应meta/警告/类型/错误。截图与DOM描述检查分开记证据 |

### 7.4 harness适配要点

新建 `v4-{helpers.js,ab.sh,shots.sh,runtime.mjs,static.mjs}`，保留所有v2/v3历史文件。复用 `v2-mock-wails.js` 的隔离transport；需要内容数据时新增v4专用fixture，不能覆盖旧证据或接入真实凭据。每个运行批次≤3分钟，批间输出进度。

1. `seq/mode`可继续按原 `.pc-main-tabs/.pc-subnav-tabs/.pc-subnav-modes` 真按钮点击；补断言当前模式组位于 `.pc-subnav-context`，读取owner。导航helper按data-nav-id／role精确名定位，不按左栏button.textContent匹配（隐藏描述仍在textContent）。
2. `metrics`不再通过“subnav第二子元素与第一个交叠”推断意外折行；CLI正常2行、presets正常1行。增加 `contextRows`（owner/modes中心聚类，正常1）、`contextOwner`、`contextIndent`、`firstEditableTop`。
3. `underlineDelta`：CLI以 `.pc-subnav-tabs` 底边比较active按钮；presets仍用整个subnav底边。不能继续拿两行容器底边减第一行页签底边。
4. `toolbarRows`沿用v3已纠正的纵向中心±4px聚类。OpenCode预设@800恰3行是本轮批准的基线，不再记录成v4新增失败；意外多于3仍失败。正常CLI context出现额外折行也须失败并定位。
5. 宽度期望使用实际占位s推导；保留不带 `--hide-scrollbars` 的独立8px浏览器与16px保守探针。矩形使页面更长，之前没有滚动条的视图可能现在有，不写死视图→s映射。
6. overflow集合加入 `.pc-subnav-context,.config-content-group,.ccg-head,.ccg-heading,.ccg-body,.pre-model-head` 和OpenCode三类记录的head/expanded；过滤不可见祖先。增加内层input/button外接框不越界检查，不能只看scrollWidth。
7. `dynamic`按计算样式的新max-height断言，600高应280；每个N20案例实际按End并检查add在视口与nav交集内。sticky沿用 `max(12,自然top−scrollTop)`，不假设短页一定能滚够300px。
8. `content`核对当前分区group数量与icon；不要求全页面始终只有3个group，因为visited隐藏分区仍保实例。OpenCode断言computed border/background的选择性变化，permission和内层模型必须仍透明无框。
9. 左栏净化检查不能简单断言textContent不含meta，也不能以Playwright `toBeHidden`判断sr-only（裁切元素可能仍被判可见）。核对可视子节点白名单、描述CSS裁切／脱离流、按钮accessible name/description，并由洛神实读截图。
10. `static`重设§7.2白名单，检查修改文件含未跟踪的新组件；生产函数比较不再沿用“所有编辑器零改动”。`drafts/dynamic/lifecycle/narrow/states/coverage/interactions`原断言含义保持，新增content批；新布局不应降低原业务门禁。

截图前缀 `v4-`。基础两档各9视图；追加MCP／并发、Registry多模型与高级字段、OpenCode三种展开记录与permission对照、纯净左栏invalid／受管／OAuth、N20限高、chunk失败导航、1024抽样。洛神须实际打开关键图；本设计未读v3截图，不将报告中的审美结论作为主上本轮认可。

## 8. 风险、实施切片与回退

### 8.1 风险与未知项

| 项目 | 已知代价／边界 | 判别与处理 |
|---|---|---|
| 导航增加约36px | Pi首屏h3约385，OpenCode预设约425；后者继续最紧。接受这项代价来直接表达父子关系 | 实测高度与首个输入，不为凑线顺手改PageHead／一级动作区／工具栏；超出§6基准先报告成因 |
| 矩形吃掉局部横宽与增加纵向 | 800实际字段净宽从348降到330；组头和padding增加页面长度 | narrow/content测内层；需要自然换行，禁止隐藏字段。默认4的placeholder与敏感值按钮特别抽查 |
| 归组是否符合主上观感 | “有时候”的具体边界通过§4固定，尚未有v4视觉证据 | 洛神实读两档对照；主上仍可逐块撤销。不能把本设计自检等同视觉通过 |
| 隐藏meta被误删语义 | 若顺手删navigationItems或右侧meta，可能失去aria描述／常显风险 | §5与S14逐项核对；只换可视渲染，数据接口保留 |
| 隐藏invalid降低左栏提示度 | 用户不再从目录看见错误文字 | 工具栏已有定位按钮，当前压缩有alert；原e2e证明此契约，后续仍须复跑 |
| Registry重新排序 | 扩展字段的阅读／Tab位置移到模型之后；大量模型时扩展字段更靠下 | 有意取舍：常用字段连续。扩展入口仍常开、可滚到，不新增折叠／丢失字段 |
| OpenCode局部deep选择器 | 改错合并选择器会连permission／内层模型一起恢复框 | 明确三类直接子记录白名单，computed样式＋截图核对；共享子编辑器零diff |
| 新group组件 | 增加一个轻展示组件，但没有事件、状态、IO或配置接口 | 仅服务本轮重复归组；不扩成可折叠卡系统 |
| 真实平台／读屏／远程横幅 | 本轮只有静态推导；v3宽度证据是Chromium隔离仿真，非真实WKWebView/WebView2 | 后续按实际平台与远程横幅抽查；无法触达则报告未验证。Segmented underline缺少选中态aria是既存项，本轮group标签补归属但不宣称已完整修复 |
| 原草稿／错误边界 | 跨模式照旧卸载、非法局部RawJson不emit、Auth源码可编辑等均未重做 | 不扩大为自动保存／跨文件缓存／全字段校验。范围外错误独立报告 |

没有配置数据迁移或不可逆写入。本轮不写用户配置、不提交／推送、不授权任何Git破坏性动作。

### 8.2 建议实施切片

1. **导航关系＋左栏**：ProviderCenterView、SectionNav；先跑V1/V3，拍800父子导航与invalid左栏，测新nav限高。
2. **Pi字段组＋Registry模型**：新增ConfigContentGroup，接入Pi/Registry；先保住V3，再跑drafts/content双引擎、内层宽度。所有保存与字段函数须逐字不变。
3. **OpenCode记录样式**：只改宿主deep；用provider/agent/mcp和permission反例验证选择范围，复跑unknown非法草稿。
4. **完成L2与读图**：v4-static、V1–V3和分批runtime；两档九视图及增量关键图；报告尺寸实测、失败、不可达项。该顺序供Leader采用，不由本节点调度。

### 8.3 回退粒度

- 顶侧关系可以单独反向应用ProviderCenterView的v4 hunk；SectionNav的meta净化仍可保留。若回到单行顶部，同时建议撤回限高减40px那一条，避免无必要地缩短左栏。
- 左栏纯净化／限高在SectionNav单文件内，各自独立hunk；恢复可视meta无需修改各编辑器的数据构造。原右侧语义一直保留，不需迁回。
- Pi与Registry分别可撤回调用／模板／样式；只有两处消费者都撤完并确认无其他引用后，才可另获授权删除ConfigContentGroup。可暂留未引用组件，不擅自清理。
- OpenCode仅回退OpenCodeGlobalConfig内v4样式，不覆盖MapEditor。Auth／OMP／预设／e2e不存在待回退代码。
- 若形成独立提交，经Leader授权可反向提交；未提交时按本轮归因hunk撤回，保留共享树其他改动。C1仍未执行，不能顺手删external或旧卡。回退后复验V1–V3与受影响harness；旧截图只作对照。
- 本设计文档的回退只涉及本文件，经授权撤回其新增内容；不触碰历史v1/v2/v3设计及证据。

### 8.4 本设计阶段验证记录

- cwd：`/Users/run.mao/workpace/amagi-codebox`。实际工具为read/grep/find/write；没有shell、Git、构建、测试或浏览器运行，退出码不适用。写入范围仅本文，不写metadata／索引／harness。
- 必读材料已完整读取：p1v4-taskbook、design-v3、design-v2（含截断续读）、impl-report（含§9续读）、ProviderCenterView、五个编辑器、Segmented/EditorSection/EditorToolbar/FieldLabel、ConfigCategoryCard、e2e spec；SectionNav与icons使用任务内联完整正文，没有重复读取。
- 补充证据：configNavigation全文确认左栏没有计数字段；`design.md §5.3`逐项核对I1；grep确认SectionNav的生产模板消费者只有五编辑器；读取四个MapEditor的相关模板、记录样式和局部字段样式，核对可复用图标头及permission反例；读取ModelSpecSelector/StringListEditor相关模板；base.css滚动条定义；v3-helpers/v3-static全文及v3-runtime相关度量断言。
- 静态自检结果：三条指令均有裁决与文件落点；四种导航候选比较完整；计数／invalid／Auth类型已定；I1逐项有右侧承载；I3选择器与生命周期不变；800宽度、新增高度、左栏限高相互一致；归组白名单及其列表反例、harness改造、回退粒度明确。
- 未验证：现场SHA／diff的独立复核、任何运行时尺寸、真实保存／平台行为、读屏器、v4视觉效果。本文数值明确区分v3上游实测与v4预算；视觉与实现完成状态留给后续真实证据。
