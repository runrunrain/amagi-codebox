# Provider Center 左栏导航布局 — v2 增量修订

> 结论：两页统一为左栏导航＋右侧内容。CLI 的「引擎／模式」与编辑器分区共用一条左栏；仅展开当前模式的分区，右侧一次显示一个分区。800px 下保留文字左栏，通过局部缩减留白和表单换行保证可用。
>
> 上游：[`p1r-taskbook.md`](./p1r-taskbook.md)、[`design.md`](./design.md)（v1 全文）、[`p1-taskbook.md`](./p1-taskbook.md)。实现基线是本次读取的工作树，非 HEAD 中的旧布局。本文只修订受新指令影响的部分。
>
> 本轮实际产物仅此文档；以下源码改造指令供后续实现使用，不代表已实施。工具面无 shell，本轮未执行 `git diff HEAD -- frontend/src`；已逐一读取当前容器、七个内容组件、ConfigCategoryCard、三个新 UI 基件及相关消费者。构建 exit=0 和 50 张截图来自任务书的上游事实，本轮未重跑、未作视觉复核。具体证据边界见 §9.4。

## 1. 指令解读与范围裁定

### 1.1 两种读法都采纳

主上要求「各个子页中不要是卡片平铺式，而应该是左侧 bar 切换显示不同子页签的内容」。据此作如下裁定：

- **读法 A 采纳**：二级引擎／格式、三级模式从水平 Tab 迁入左栏。一级「服务提供商／预设／CLI 独立配置」保持 pill。
- **读法 B 采纳**：编辑器内的分区卡迁成同一左栏里的子条目；右侧只有当前分区的无卡框表单。
- **预设页不豁免左栏**：左栏只有「Anthropic 格式」「OpenAI 格式」。两个条目会留下较多空白，但主上跨页切换时不必重新学习另一种导航，且窄窗只占 112px。右侧保留 v1 已实现的分组列表和弹窗。
- **数据记录不全部提升为导航**：预设是一组可比较的列表记录；将 N 条预设改为内联详情会改动弹窗的确认／取消与保存边界。本轮不做。注册表提供商、认证条目原本就在同一文件中内联编辑，适合直接迁为动态导航叶子。

这里的「叶子」指下表列出的分区、单个注册表提供商、单个认证条目及预设列表入口；模型行、角色行、权限规则等是分区内的字段记录。字段记录继续列表编辑，不增加导航层级。

### 1.2 选择与代价

| 候选 | 取舍 | 原因 | 代价／可逆性 |
|---|---|---|---|
| 只把水平 Tab 竖排，右侧仍堆卡 | 否决 | 只满足 A，未处理主上明确否决的分区卡平铺 | 改动少，但目标不成立 |
| 全部 CLI、全部模式、全部静态／动态叶子永久展开 | 否决 | 仅静态就有近二十个入口，动态 N 无上限；预加载全部文件才能填齐名称会破坏按需加载 | 导航冗长、加载代价高 |
| **CLI 分组下保留模式入口，仅当前模式展开分区** | **选定** | 一个左栏同时承担 A/B；跨模式到任意叶子最多两次点击；未加载的编辑器不负责渲染模式入口 | 新增轻量展示接口、选择状态；无配置格式迁移，可回退当前 v1 实现态 |
| 两条左栏（页面栏＋编辑器栏） | 否决 | 最小窗口中会连续占用两次宽度；引入多重滚动与重复定位 | 不采用 |
| 窄窗改抽屉／下拉／恢复水平 Tab | 否决 | 抽屉打开＋模式＋叶子会达到三次点击；图标无法区分多个「角色模型」；恢复卡片违背指令 | 采用持续可见的窄文字栏 |

### 1.3 对 v1 的覆盖关系

- 替换 v1 §2.1-C 的否决结论、§2.2-N2、§3.1–3.2、§4.1 及 §4.3–4.8 的分区外壳、§7.3 的卡片选择器契约。
- 继承 v1 文案分层判据、T01–T57 的业务含义、InfoTip、FieldLabel、EditorToolbar、预设分组列表、路径复制、状态与错误处理。只迁移挂载点，文案变动仅见本文 §6。
- I1/I2 继续成立。I3 改为：四个逻辑测试文件及其业务断言不变；指定 e2e 两文件按 §5 适配结构。I4：不加依赖，不动 Go、wailsjs、API、stores。I5：懒加载和按需读取继续成立。
- 本轮只允许布局选择状态的新增、已列出的展示事件和局部样式；`handleSave`、解析／序列化、配置更新规则不重写。涉及新增／删除／重命名的函数，最多在成功分支尾部维护导航选择，不改原有写入和确认逻辑。

## 2. 统一左栏导航模型

### 2.1 信息架构

以下是完整可达树，不要求同时展开全部分支。CLI 名是不可折叠的组标题，模式是可点击入口；只有当前模式挂载其缩进一级的叶子。CLI 标题不占一次点击。

```text
一级：预设
  左栏：格式
    Anthropic 格式 → 右侧：v1 PresetList + 弹窗编辑
    OpenAI 格式    → 右侧：v1 PresetList + 弹窗编辑

一级：CLI 独立配置
  左栏：Pi
    Agent 配置
      角色模型      agents       （含 profile）
      上下文压缩    compaction
      MCP 路由      mcp          （Leader／默认／角色附加）
      并发限制      concurrency  （default／providers／models）
    模型提供商
      <提供商名> × N             （amagi-* 显示「受管」）
      其他顶层键                 （仅存在时）
      添加提供商                 （常在，独立表单叶子）
    认证登录
      <凭据条目名> × N           （类型标记，绝不带密钥／账号／令牌）
      添加 API Key               （常在，独立表单叶子）
  左栏：OMP
    Agent 配置
      角色模型      modelRoles
      子代理覆盖    overrides
    模型提供商
      <提供商名> × N／其他顶层键／添加提供商（同 Pi）
  左栏：OpenCode
    预设管理 → 右侧：v1 分组列表／JSON 只读视图 + 弹窗
    全局配置
      顶层模型      model        （仅 hasTopLevelModel）
      提供商        provider
      Agent         agent
      MCP 服务器    mcp
      权限          permission
      指令文件      instructions
      插件          plugin
      实验特性      experimental
      其他键        unknown      （仅 unknownKeys.length > 0）
```

**现场计数纠正**：OpenCode 全局源码有 7 个常驻分区、1 个条件 model 分区、1 个条件 unknown 分区，最多 9 个，不是固定 8 个。`unknownKeys` 当前排除 `$schema`（它在 KNOWN_KEYS 中）；保留此行为，不借本轮增加 `$schema` 可视化编辑。注册表的 `rootUnknownKeys` 则包含 `$schema`，也保持。

MCP 的三个子段不再拆导航：它们共同决定一次路由结果，保留同页便于比较。并发三个子段同理，需同时看见模型＞服务商＞默认的优先级。子段只用 FieldLabel＋表单和分隔线，没有独立背景、圆角、折叠头。

### 2.2 左栏容量与点击深度

- 固定模式入口共 **7 个**，3 个 CLI 组标题始终渲染。选中 OpenCode 全局时最多增加 9 个叶子，总计 16 个可点击项；Pi Agent 时是 11 个。其余模式的叶子暂不展开，但对应入口一直可用。
- 动态 N 条目只来自当前已加载的注册表／认证编辑器。动态子列表最大高度：宽窗 256px，窄窗 192px；超过后列表内部滚动。其他模式不会被 N 个条目无限推远。外层左栏也可滚动，规格见 §4；不做虚拟列表、不截掉 N 条数据。
- 跨模式：点目标模式 → 等待其原有加载 → 点目标叶子，**最多 2 次点击**。同模式切叶子 1 次。无需先点 CLI，无需展开箭头、抽屉或「更多」。滚动不增加导航层级。
- 从其他一级页到某叶子需先选一级「CLI 独立配置」；≤2 的口径从进入该一级页起算，避免把全应用入口算进内部切换深度。
- 源码模式下，当前编辑器的分区保留显示但禁用；提示「源码模式：返回可视化后切换分区」。返回可视化＋选分区为 2 次。非法源码仍须先修复，这是既有校验前提，不能为满足点击数自动覆盖源码。
- 预设条目不再塞进左栏；左栏切格式 1 次，右侧点击名称打开弹窗 1 次。OpenCode 预设管理也保持同样的列表操作。

### 2.3 与当前状态的映射

| 导航身份 | 原有状态／入口 | v2 行为 |
|---|---|---|
| `format:anthropic / format:openai` | `formatModel` → `store.setPresetEngine` | 沿用 setter；不新增读取链路 |
| `pi:agents / pi:registry / pi:auth` | `cliModel`＋`piMode` | 一次模式点击同时设置 CLI 和对应 mode |
| `omp:agents / omp:registry` | `cliModel`＋`ompMode` | 同上 |
| `opencode:presets / opencode:global` | `cliModel`＋`openCodeMode` | 同上；默认仍为 global |
| 具体分区 id | 原 `expanded*` | 改为纯 UI 的 `sectionId`，不写 store／文件／localStorage |

容器的 `activePageId` 类型为 `ConfigPageId | 'format:anthropic' | 'format:openai'`，格式切换也获得独立右侧实例。编辑器内使用局部 sectionId（如 `compaction`）；传给页面级 SectionNav 时转换为 `section:${pageId}:${encodeURIComponent(sectionId)}`，通过导航项映射表找回 pageId／sectionId，不对含冒号的用户名称盲目 split。本地 SectionNav 直接使用局部 id。

在同一模式内切叶子，不调用 `store.setPresetEngine`，不递增 `presetLoadKey`，不再执行 initialLoad/loadCatalog。跨模式仍按当前 `v-if` 生命周期卸载旧编辑器；不增加跨文件草稿缓存、自动保存或新确认弹窗。

### 2.4 容器与编辑器的责任边界

ProviderCenterView 负责：一级入口、整个左栏、静态导航元数据、当前模式和期望 sectionId。编辑器负责：文件加载、模式切换、全部草稿、保存、动态叶子与当前可视状态报告。EditorToolbar 继续在编辑器内。

新增 `components/provider/configNavigation.ts`：纯类型、静态描述和 id 辅助函数；不得导入任何编辑器 `.vue`、API、store 或执行 IO。内容包括上述 7 个 page id、静态分区表、中文名和默认叶子。

```ts
type ConfigPageId = 'pi:agents' | 'pi:registry' | 'pi:auth'
  | 'omp:agents' | 'omp:registry' | 'opencode:presets' | 'opencode:global';
interface ConfigSectionItem {
  id: string; label: string;
  meta?: string; metaTone?: 'neutral' | 'warning'; // 只传展示信息
  invalid?: boolean;
}
interface EditorNavigationSnapshot {
  owner: string; // 父级传入的本次挂载标识，回传原值
  phase: 'loading' | 'ready' | 'error';
  mode: 'visual' | 'source'; // 把 Pi/OpenCode 的 json 规范成 source，仅用于导航
  activeId: string;
  items: ConfigSectionItem[];
}
// 五个编辑器的可选 props：
// sectionId?: string; navigation?: 'external' | 'local'（默认 local）; navOwner?: string
// emit('navigation-change', snapshot)
```

- 父级以 `pageId + presetLoadKey + 挂载序号` 生成 owner；仅接受 owner 等于当前挂载标识的事件。序号只在模式／engine／presetLoadKey 改变时更新，**不作为分区切换的 key**。旧请求迟到不得覆盖新左栏。
- 编辑器持有 `activeSection`；监听 sectionId 更新展示选择，选择、成功新增／删除／重命名后报告 snapshot。父级收到后只同步导航数据和期望 id，值相同不重复改写，不能因此触发加载。初始化先应用传入 sectionId，再报告；loading/error 阶段只报告状态，父级不得用默认 activeId 覆盖已排队的静态选择。
- 静态分区在组件 chunk 尚未完成时即可由父级渲染、选择；该选择在组件完成后生效。OpenCode 的 model／unknown 需数据判定，加载前不虚构这两项。
- 动态模式加载时显示非交互状态「加载分区中…」，失败显示「分区不可用」；**7 个模式入口照常可用**。没有为了填齐左栏而提前加载其他编辑器。
- phase 为 ready 且 visual 时才校正失效选择。源码编辑中不因名字暂时消失而改变 sectionId，也不因此序列化。条件分区消失后回退到默认叶子，见 §3。
- 本轮保留 `lazyPreset`、`PresetLoadError`、defineAsyncComponent 和全部 **7 处** `:key="presetLoadKey"`（v1 S6 的「6 处」是计数误差）。右侧外层以 page id 为 key，避免 Pi／OMP 同类型注册表组件在 engine 切换时复用旧文件；内层 key 仍由 presetLoadKey 控制重试和配置档重载。
- 不依赖动态组件 ref、defineExpose、编辑器 slot 或 Teleport 来提供页面级导航；v1 §2.2-N3 所指出的加载失败失去导航问题不会复现。

### 2.5 草稿与保存不变量

1. 保存按钮始终保存当前**整个文件**，所有分区的合法修改一起保存；不改为单分区保存。
2. 分区第一次访问才挂载。访问过的分区在同一可视化会话内用 `v-show` 隐藏，保留组件实例；未访问的用 `v-if` 阻止创建。禁止 `:key="sectionId"` 包住整个编辑器。
3. 这样保留 ModelSpecSelector 的手动输入态、MCP 空白名单编辑态、并发空值中间态，以及 RawJsonEditor 内尚未 emit 的非法文本。当前 RawJsonEditor 在解析失败时只保留局部 text，不写回 modelValue，直接卸载会丢草稿。
4. 隐藏分区是 `display:none`，不参与焦点／可访问树；只有一个 `.editor-section` 可见。模式切源码仍沿用原有可视化／源码生命周期，不在本轮扩大成跨模式草稿系统。
5. 已有错误不能随分区隐藏而无处定位。Pi 的 compactionError 在非压缩分区时由工具栏 status 槽显示「上下文压缩待修正」按钮，点后定位压缩分区；左栏该叶子显示错误标记。源码态 status 只显示文字。原 handleSave 的拒绝保存逻辑和局部 role=alert 不变。
6. 左栏切换只改变显示，不修复、不清理、不写盘。跨模式未保存内容的处理与当前实现一致：卸载后丢弃，不新增静默保存。
7. `v-show` 不会隐藏 Teleport 到 body 的菜单。EditorSection 的 active 状态必须通过 §7.2 的局部可见性上下文让 InfoTip／Dropdown 收起浮层；不靠伪造点击／Escape，不要求用户切区前手动关闭菜单。

## 3. 逐文件增量改造指令

目录前缀均为 `frontend/src/`。下列骨架中的省略处表示保留原有模板及事件，不能换成空实现。

### 3.1 `views/ProviderCenterView.vue`

**保留** PageHead、一级 pill、服务提供商页、详情视图、远程横幅、配置档与导入导出、全部加载／watch。**删除** 两处 `.pc-subnav` 和只服务其布局的 `.pc-subnav-*` 样式；FORMAT/CLI/MODE 的文案不变，导航描述移入 configNavigation.ts 或从其导出引用，避免两份文案常量。

```vue
<ConfigCard class="pc-card" :class="{ 'pc-card-workspace': isWorkspace }">
  <div class="pc-head">原一级导航和动作</div>
  <div v-if="mainTab === 'providers'" class="pc-panel pc-panel-providers">原内容</div>
  <div v-else class="pc-workspace">
    <SectionNav :groups="navGroups" :current-id="currentNavId"
      :branch-id="activePageId" controls-id="pc-editor-detail"
      :label="mainTab === 'presets' ? '预设格式' : 'CLI 配置导航'"
      @select="selectNavigation" />
    <div id="pc-editor-detail" class="pc-detail-content" :key="activePageId">
      <template v-if="mainTab === 'presets'">
        <p class="pc-format-caption">{{ formatConsumers }}</p>
        原两种 PresetList 分支
      </template>
      <template v-else>
        原 Pi／OMP／OpenCode 异步分支（保留 presetLoadKey）
        向五个编辑器传 navigation="external"、section-id、nav-owner，接 navigation-change
        OpenCodePresets 没有内部分区，不需要该展示接口
      </template>
    </div>
  </div>
</ConfigCard>
```

- `.pc-workspace` 为 grid；左右间隙／列宽见 §4。右侧 `min-width:0`，不再套第二个 ConfigCard。workspace 状态下 `.pc-head-actions` 增加 `flex-wrap:wrap;min-width:0;max-width:100%`，保证窄窗配置档／导入导出按钮也不撑宽页面。
- `isWorkspace` 仅在两页列表模式为 true；`.view-provider` 可据此挂条件 class 调整窄窗边距。服务提供商网格和详情模式沿用当前样式。
- 格式说明从旧 subnav 右侧迁到右侧列表工具栏之前，12px tertiary；不再渲染格式 h2。
- 模式行被点中但尚未收到 snapshot 时，沿用其默认／本会话记忆的静态选择；动态模式的默认由编辑器读完真实数据决定。模式行本身不作收起开关，再点当前模式不重载、不收起。
- 新导航事件按 §2.3 调用现有 computed setter；跨 CLI 的 mode 与 engine 在同一同步回调设置，中间不 await，使 Vue 在同一轮渲染进入最终目标。不要额外调用 loadPresets，也不要改 store.setPresetEngine 中重置筛选／搜索及按需加载的行为。

### 3.2 `components/provider/PresetList.vue`

分组列表、弹窗、删除确认及脚本全部复用。它是右侧一个列表页，不再加左栏，不改成预设详情表单。

```text
.preset-panel
  Loading / Error
  .pc-toolbar（筛选 Chip + 添加）
  ul.preset-group > li.preset-row × N 或 EmptyState
PresetDialog / ConfirmDialog（原挂载位置）
```

只补收缩样式：`.preset-head{flex-wrap:wrap}`；`.preset-open{flex:1 1 120px}`；provider 标签 `max-width:100%;overflow-wrap:anywhere;white-space:normal`；删除按钮不缩小。保留整行伪元素热区和单独删除焦点。不能因右侧变窄而隐藏徽章或删除入口。

### 3.3 `components/provider/PiAmagiConfig.vue`

- 删除 ConfigCategoryCard import、4 个外壳、expanded；原文案常量、全部字段及更新方法保留。
- 默认 `activeSection='agents'`，分区表从纯导航模块引用。加入 §2.4 的可选导航接口、已访问集合和 snapshot。
- MCP／并发的内部字段顺序原样保留；不把三个子段拆成新的导航项。

```vue
<div class="pac-root" :class="{ 'editor-local-layout': navigation === 'local' }">
  <SectionNav v-if="navigation === 'local'" label="配置分区"
    :groups="localGroups" controls-id="pac-editor-detail" @select="selectSection" />
  <div id="pac-editor-detail" class="editor-detail">
    LoadingState / ErrorState / 原 EditorToolbar
    原全局 jsonError、catalogError 横幅
    <div v-if="mode === 'visual'" class="pac-visual">
      <EditorSection v-if="visited.agents" v-show="activeSection === 'agents'"
        section-id="agents" title="角色模型" code="agents" :badge="agentRows.length">
        原 profile + 角色覆盖字段
      </EditorSection>
      <EditorSection v-if="visited.compaction" v-show="activeSection === 'compaction'"
        section-id="compaction" title="上下文压缩" code="compaction"
        meta="下次压缩生效" :tip="COMPACTION_TIP" :badge="原表达式">
        原压缩字段，DOM 顺序见 §5
      </EditorSection>
      <EditorSection section-id="mcp" title="MCP 路由" code="mcp" ...>原三个子段</EditorSection>
      <EditorSection section-id="concurrency" title="并发限制" code="concurrency"
        :tip="CONCURRENCY_TIP" ...>原三个子段</EditorSection>
    </div>
    <div v-else class="pac-json">原 .pac-json-editor</div>
  </div>
</div>
```

四个分区都遵守 visited＋v-show，并传 `:active="activeSection === 对应id"`；最后两个省略的属性也包括该规则。`navigation=external` 时不创建本地栏，不引入第二次 grid 缩进。

删除原卡片间 gap，分区内部 `.pac-field + .pac-field` 保留 14px；右侧 section 不再有 14px 卡内水平 padding。压缩用例中的 `.pac-json-editor/.pac-json-error/.pac-catalog-warn/.mss-*` 全部保留。

### 3.4 `components/provider/OmpGlobalConfig.vue`

删除 ConfigCategoryCard 和 expanded；默认 `modelRoles`，另一个 id 为 `overrides`。接口／本地独立挂载回退同 Pi。

```text
.ogc-root >（可选本地 SectionNav）+ .editor-detail
  原 Loading / Error / EditorToolbar〔新会话生效〕/ 两类错误横幅
  visual：EditorSection(modelRoles, 角色模型, OMP_ROLES_TIP, roleRows.length)
       或 EditorSection(overrides, 子代理覆盖, OMP_OVERRIDES_TIP, overrideRows.length)
  source：原 .ogc-source-editor
```

现有 `@role` 引用／直接模型切换保留，切叶子不得调用 modeModel setter。两块访问后隐藏保留。源码解析和保存行为不借此重构。

### 3.5 `components/provider/ProviderRegistryEditor.vue`

两处 engine 挂载共用同一实现。删除 ConfigCategoryCard、expandedProviders、expandedRoot、toggleProvider；注册表工具栏、生效标签及 tip 原样复用。

导航项顺序：`providerNames` 原有对象顺序 → `root`（rootUnknownKeys 非空）→ `add`。提供商的 section id 为 `provider:${encodeURIComponent(name)}`，与保留 id 不冲突；DOM 不直接插入未编码名称作 id。默认首个提供商，否则 root，否则 add。

```text
.pre-root >（可选本地 SectionNav）+ .editor-detail
  Loading / Error / 原 EditorToolbar / parseError
  visual：
    EditorSection(provider:<name>, title=<name>, badge=modelCount, meta=受管)
      .pre-managed-warn（amagi-* 常显）
      协议／Base URL／API Key
      其他字段（原 VisualValueEditor）
      模型列表（原专用高级字段编辑器）
      删除当前提供商
    EditorSection(root, 其他顶层键, badge=rootUnknownKeys.length)
      原根键编辑／删除列表
    EditorSection(add, 添加提供商)
      N=0 时 EmptyState「暂无提供商」
      原 newProviderName 输入＋添加按钮
  source：原 .pre-source-editor（整个文件）
```

- 对当前提供商单显。访问过的提供商／root／add 按 §2.5 保留实例；不在父级拆分或复制 providers 数据。
- 成功添加后选中新提供商并聚焦标题；重名／空名时停留添加页，原错误与数据不变。原 addProvider 中 expandedProviders 赋值替换为导航选中，仅此展示尾部变化。
- 确认删除当前提供商后选原列表下一项，无下一项选上一项，无提供商选 root 或 add。取消删除不改变选择。删除最后一个 rootUnknownKey 后按同一默认算法回退，不生成不存在的键。
- 单个提供商内的模型仍为可编辑列表，不上导航。`.pre-model` 改为无边框／无圆角、无独立背景的行组；相邻模型间 1px separator、padding-block 12px，保留模型操作。其他字段／高级字段是标题＋表单段，不恢复小卡片。
- 新增入口是导航叶子，不在每个提供商底部重复添加整块提供商表单。API Key 仍 password；snapshot 只传导航 id／名称／受管标记等展示元数据，连接信息与字段值不得进入。

### 3.6 `components/provider/AuthConfigEditor.vue`

动态条目＋「添加 API Key」。默认第一条，否则 add。类型徽章、OAuth 管理标签、InfoTip、只读状态、未知字段编辑和删除确认原样保留。

```text
.ace-root >（可选本地 SectionNav）+ .editor-detail
  Loading / Error / 原 EditorToolbar / parseError
  visual：
    EditorSection(entry:<uiId>, title=当前条目名)
      原名称输入＋类型徽章＋OAuth 管理标签和 InfoTip＋删除
      api_key：password 输入＋其他字段
      oauth：只读登录／过期状态（不输出令牌）
      other：原 VisualValueEditor
    EditorSection(add, 添加 API Key)
      N=0 时原 EmptyState（OAuth 登录途径保留）
      原 newName / newKey / 添加 / 未认证建议
  source：原 .ace-source-editor
```

- 删除 `.ace-entry/.ace-add` 的卡框、虚线边框、独立背景；正文由 EditorSection 负责。不要重复显示「添加 API Key」两份标题。
- 使用仅存内存的 `entryIdByName` 保持条目 UI id。合法重命名时将旧 id 转移到新名，再更新选中 id；不能每输入一个字符就按新名字重建当前表单，否则焦点会丢。初次读取／源码返回时按相同名称复用 id，新名称分配新 id；不把 id 序列化。
- 成功新增选中新条目；重复名失败保持原页。确认删除后按下一项→上一项→add 回退；取消时不移动。
- snapshot 仅包括 uiId、提供商名、类型标记，不包括 key、access、refresh、accountId、expires。OAuth 原有条目重命名／删除能力不擅自禁用；「只读」仍指令牌和登录状态的可视化呈现。

### 3.7 `components/provider/OpenCodePresets.vue`

属于一个列表叶子，不增加内部分区。保留当前全部行为，包括未搜索时常驻默认行、搜索空态、JSON 只读块与 OpenCodePresetDialog。

```text
.oc-panel
  Loading / Error
  原 EditorToolbar（搜索／路径／模式／添加）
  visual：ul.oc-group > 默认行＋用户行／EmptyState
  json：原 pre.oc-json／EmptyState
OpenCodePresetDialog
```

仅补 `.oc-head{flex-wrap:wrap}`、`.oc-key{max-width:100%;overflow-wrap:anywhere;white-space:normal}` 和窄栏搜索样式（§4）。预设名点击仍打开弹窗，JSON 不能变成可写 textarea。

### 3.8 `components/provider/OpenCodeGlobalConfig.vue`

删除 ConfigCategoryCard、expandedCategories，`.ocg-cards` 更名 `.ocg-visual`；默认 `provider`。静态表顺序按 §2.1，model 与 unknown 的显示条件直接复用现有 computed。选中条件分区消失时回退 provider，空配置也可直接编辑 provider。

```text
.oc-global >（可选本地 SectionNav）+ .editor-detail
  原 Loading / Error / EditorToolbar〔即时生效／空文件〕/ jsonError
  visual：EditorSection × 已访问分区（仅一个可见）
    model → 原 TextInput，MODEL_TIP 迁入标题行
    provider → 原 ProviderMapEditor
    agent → 原 AgentMapEditor
    mcp → 原 McpMapEditor
    permission → 原 PermissionMapEditor
    instructions / plugin → 原 StringListEditor
    experimental → 原 TypedKeyValueEditor
    unknown → 原 RawJsonEditor 列表
  json：原 .ocg-json-editor
```

**内部记录列表去卡化**：当前 ProviderMapEditor／AgentMapEditor／McpMapEditor／PermissionMapEditor 本身还有数据记录折叠卡，不能把外层换掉后继续呈现一堆带背景的小卡。

本轮不改这些子编辑器的字段／展开逻辑，在 OpenCodeGlobalConfig 的 scoped 样式内按明确祖先限定 `:deep`：

- `.provider-editor > .pe-card`、`.agent-editor > .ae-card`、`.mcp-editor > .me-card`、`.perm-editor > .pe-card`：透明底、border 0、radius 0、shadow none；列表 gap 0，相邻记录 border-top 1px separator。
- 对应 `*-thumb-head` 改为列表行，水平 padding 0；`*-expanded` 水平 padding 0，保留记录内部展开／收起和字段。无悬浮阴影、无独立卡边。
- `.provider-editor .pe-model-card` 同样去框，改模型记录之间的分隔线；`.variants-editor .vme-row` 去独立背景／圆角，保留原展开按钮。
- 分类已在左栏单显；记录折叠只是同一分类中的数据列表操作，右侧不再同时铺多个配置分类。记录不伪装成新的页面 Tab。
- `ProviderMapEditor` 等源码不动；这些样式仅作用在当前 OpenCode 全局页，不改变其他消费者。窄栏字段行覆盖见 §4.3。

### 3.9 基件与文件保留表

| 路径 | 本轮动作 | 原因 |
|---|---|---|
| `ui/SectionNav.vue` | 新增，§7.1 | 统一有分组的左栏、焦点和滚动 |
| `ui/EditorSection.vue` | 新增，§7.2 | 统一无卡框分区标题、code、数量、meta、tip |
| `provider/configNavigation.ts` | 新增，§2.4 | 父级无需加载编辑器即可渲染静态导航 |
| `ui/sectionVisibility.ts` | 新增纯 InjectionKey，§7.2 | 让 v-show 隐藏的分区关闭 Teleport 浮层，不传保存或配置状态 |
| `ui/InfoTip.vue`、`ui/Dropdown.vue` | 保留交互；仅消费可选分区可见性并在隐藏时收起 | 未处于 EditorSection 内的消费者行为不变 |
| `ui/FieldLabel.vue` | 仅加 `flex-wrap:wrap`、code 可断行 | 右侧变窄，避免标签挤出 |
| `ui/EditorToolbar.vue` | 保留 DOM／props；加窄容器样式，§4.2 | 路径、生效标签与动作仍由编辑器持有 |
| `ui/Segmented.vue` | 不改 | 继续用于一级和文件模式／三态；不用于新左栏 |
| `ui/StatusBanner.vue`、`ui/EmptyState.vue` | 保留 v1 扩展 | 继续复用 |
| `provider/ConfigCategoryCard.vue` | **保留文件和 v1 新 props，不删除** | 两页停止使用；即使调用数归零也不清理公共组件，便于回退 |
| `styles/tokens.css` | 不改 | v1 新 token 足够 |
| `e2e/pi-amagi-config.{spec.ts,html}` | 按 §5 修改 | 只适配新 DOM／导航，不改 transport 或断言含义 |

## 4. 窄窗适配规格

### 4.1 宽度预算与断点

断点以窗口 CSS px 为准。仅两页的 workspace 状态应用下表；应用侧栏继续 264px，main.go 的 1280×800／MinWidth 800／MinHeight 600 不动。

| 窗口宽度 | 两页左右页边距／外层卡左右 padding | 左栏／列间隙 | 右侧预算 |
|---|---|---|---|
| ≥1200（验收 1280） | 36／16 | 184／16 | 1280−264−72−32−184−16 = **712px** |
| 1024–1199 | 24／16 | 144／12 | 1024 档为 **524px** |
| 800–1023 | 16／12 | 112／12 | 800−264−32−24−112−12 = **356px** |

Windows 非覆盖滚动条再预留 16px，最小档右侧仍约 **340px**。这些数字不依赖隐藏主应用侧栏或抬高最小窗口。

相对任务书 432px 的旧预算，窄档回收了页边距 40px、外层 padding 8px，再消耗左栏 112px＋间隙 12px；右侧不再付每张分区卡 28px 的内边距。网格明确使用 `grid-template-columns: var(--section-nav-width) minmax(0, 1fr)`，后者不能写成裸 `1fr` 后保留子元素 min-content 撑宽。

左栏各档都保留文字，无图标化、抽屉、下拉或水平导航回退。样式变量是 workspace 局部 CSS 变量，不新增全局 token。本地独立挂载模式使用相同列宽规则。

### 4.2 左栏与工具栏

- 左栏 padding-right 8px、右侧 1px separator，全部计入 184／144／112px；条目 padding `7px 8px`，最小高 32px，窄档最小高 30px；缩进一级 8px。窄档文字 12px，宽档 13px。
- CLI 组标题 11px、secondary、上下间距 8px；不重复为每个条目加 CLI 前缀，但 aria-label 含完整路径。模式入口比子分区字重高一级。
- 静态中文名允许自然换行，不截掉含义；动态长名最多显示两行、`overflow-wrap:anywhere`，完整名称保留在 accessible name，选中后的右侧标题完整换行。受管／OAuth 标签另起一行，不能压住名称。
- 左栏 `align-self:start;position:sticky;top:12px`；外层最大高 `max(240px, calc(100vh - 280px))`（800px 高时 520，600px 高时 320），overflow-y auto；动态子列表高度另按 §2.2 限定。短列表按内容高度，不强行撑满。外层仍由 AppShell `.main` 滚动，不锁页面滚动。
- 加入远程横幅后允许页面向下滚动，不能让横幅消失或压缩到不可读。左栏和右侧均无横向页面滚动；源码 textarea 内部可水平滚动。

EditorToolbar 自身设置命名 inline-size 容器 `editor-toolbar`（不依赖新增库）；使用其子节点的容器查询，避免只凭窗口宽度判断右侧宽度：

| 工具栏实际宽度 | 明确排布 |
|---|---|
| ≥640 | 保留现有 lead／trail 流式布局，必要时整组换行 |
| <640 | lead 占整行并允许换行；trail 占整行、`flex:1 1 100%;min-width:0;flex-wrap:wrap`，effect 与 InfoTip 不消失；模式＋保存可在同一行，不强制所有尾部项同行 |
| <400 | 路径最大宽 100%；合法／状态标签必要时另行；模式按钮 padding `4px 10px`；生效标签可独占动作前一行，保存按钮不缩小、不省字 |

保留 `.et-path-text` 与目录截断／文件名保留的已实现修正。OpenCode 搜索在窄工具栏下 `flex-basis:100%;min-width:0;max-width:none`，搜索一行、路径一行；不让 200px 最小搜索宽度和路径一起挤占 340px。

### 4.3 右侧表单收缩的具体责任

每个 `.editor-detail` 设置命名 inline-size 容器 `editor-detail`；<480px 时在其所属编辑器 scoped 样式内应用下表。不全局重置所有 input，不改共享业务子编辑器源码。

| 消费者 | <480px 的增量规则 |
|---|---|
| Pi | `.pac-agent-spec/.pac-concurrency-model-dd/.pac-concurrency-custom-model/.pac-mcp-servers-input` 设 `min-width:0;flex-basis:100%`；custom key 行 `flex-wrap:wrap`；现有固定输入 `max-width:100%` |
| OMP | `.ogc-spec` `min-width:0;flex-basis:100%`；角色、@role dropdown、形态切换按钮允许分行 |
| Pi／OMP 的 ModelSpecSelector | 通过父级 `:deep(.mss-row)` 改单列；`.mss-dd/.mss-dd-level` width/max-width 100%、min-width 0。DOM 顺序仍 provider→model→level |
| Registry | `.pre-fields-grid/.pre-model-grid/.pre-thinking/.pre-cost-grid` 全部 `grid-template-columns:minmax(0,1fr)`；模型 ID、添加名称输入 `max-width:100%`；按钮组换行 |
| Auth | 名称、添加密钥 input `width:100%;min-width:0;max-width:100%`；添加行改纵排，类型／管理标记可换行 |
| Registry／Auth 的递归值编辑器 | scoped deep `.vve-entry-value/.vve-item-value` `min-width:0;flex-basis:100%`；`.vve-key` max-width 100%；深层值不继续硬占 220px。`.tlm-actions` 加 wrap |
| OpenCode 全局 | deep `.pe-card-head/.ae-card-head/.me-card-head/.pe-row/.tke-row/.vme-row-head/.me-header-row` 允许换行；`.tke-key/.me-header-key/.pe-key` max-width 100%；`.tke-value/.pe-value/.me-header-val` 必要时独占整行、min-width 0；深层 RawJson textarea max-width 100% |
| 所有分区 | section/header/field `min-width:0`；code 和长用户名称 `overflow-wrap:anywhere`；StatusBanner 文本允许换行；表单按钮和输入不裁切 |

右侧分区无外框、无阴影、无独立圆角背景。EditorSection 标题行到表单 12px，字段段间 14px，列表记录间 separator；不为每个字段再创建卡片。v1「常显微文案单行」在窄档修订为**允许自然换行，不省略后果和错误**，字数规范继续有效。

## 5. e2e 契约适配清单

### 5.1 新 DOM 契约

- 无卡框容器统一 `.editor-section`，带 `data-section="compaction"` 等稳定 id。Pi 压缩容器为唯一 `.editor-section[data-section="compaction"]`（访问后存在，隐藏时不可访问）。
- 容器为 `<section role="region" aria-labelledby="…">`；标题是独立 `<h3>`，压缩标题的文本及可访问名精确为「上下文压缩」。code、badge、meta、InfoTip 都是标题的兄弟，不混入 region 名称。
- 本地 Pi 导航是 `nav[aria-label="配置分区"]`；其中压缩按钮 accessible name 精确为「上下文压缩」。meta 另作描述，不能把「下次压缩生效」串进按钮名称。
- 压缩分区字段顺序：FieldLabel → 三态 Segmented → 当前态句 →（custom 时）ModelSpecSelector → 后果句 → 复制 luban 按钮／当前模型 → compactionError。
- custom 的列表态中，ModelSpecSelector 的三个 `.mss-dd` 根节点 DOM 顺序仍是 provider／model／thinking level。已有合法 provider 且该 provider 有模型选项时，`.dropdown-trigger` 恰好 3 个，原 nth(0)、nth(2) 不变。**尚未选 provider 时，model 的 options 为空，现有 Dropdown 渲染兼容占位 div，只有 2 个 trigger**；不能为布局改动给它添加假选项。catalog 失败／手动输入态 input 恰好 1 个。导航／新增输入不能放入这个 region。
- 分区内 role=alert 仅来自 compactionError；InfoTip 保持 Teleport、role=tooltip。页面「保存配置」「JSON」「可视化」三个按钮仍唯一；三态名称和 aria-pressed 保持。
- `.pac-json-editor/.mss-spec/.pac-catalog-warn`、错误原文、成功 toast 不变。不得保留空 `.config-cat-card` 或隐形标题按钮来欺骗旧测试。

### 5.2 `e2e/pi-amagi-config.spec.ts` 旧→新

| 位置 | 旧 | 新／执行要求 |
|---|---|---|
| `card(page)` | `.config-cat-card` filter 内含 `/上下文压缩/` 按钮 | 改名 `compaction(page)`，定位 `.editor-section[data-section="compaction"]`，并校验其 region／标题。全文件对该 helper 的调用等量替换 |
| `open()` | 等待「保存配置」后直接检查卡内内容 | 保留原前三参 `page, config=fixture, query=''`，新增第四参 `options: {section?: 'compaction' \| null}={}`；仅 `options.section !== null` 时导航压缩。invalid-root 用 `open(page, ['preserve source'], '', {section:null})`，不能强点可视化 |
| 新 `showCompaction()` | 无 | 在 `getByRole('navigation',{name:'配置分区',exact:true})` 内点精确「上下文压缩」按钮；expect region visible，expect h3 精确标题可见；不能用 evaluate 改 Vue 状态 |
| 所有 reload 后检查 | reload 后压缩卡默认可见 | 现有 **3 处** `page.reload()` 后先 `showCompaction(page)`，再执行原断言；不依赖记忆 localStorage／默认切到压缩 |
| `select()` | `card(...).locator('.dropdown-trigger').nth(index)` | 仅换作用域为 compaction，保留 index 和原 option 名／精确匹配逻辑。可额外断言 `.mss-row > .mss-dd` 为3；调用 index=2 时才断言 trigger 为3（此时原测试已选 provider）。不对未选 provider 的初始态强断言3个 trigger |
| 第 1–3 用例 | 默认展开卡，直接点击／保存 | 由 open helper 进入压缩；原完整对象相等、null／删键、冒号模型、level 重置断言原样保留 |
| JSON 同步用例 | 卡一直存在，切回可视化再查 | open 先选压缩；源码↔可视化保留 activeSection，断言可视化回来仍是该 region。非法源码不得调用 showCompaction 绕过校验；原 broken 文本、save-count、完整 fixture 相等断言不动 |
| invalid-root 用例 | `open(page, ['preserve source'])` 后操作源码 | 用 `{section:null}`；保留非法根原文检查。填入可解析但 compaction.model=42 后点可视化，**此时**显式 showCompaction，再检查原 alert／阻止保存／复制修复 |
| catalogue failure 用例 | 卡内 `locator('input').fill(...)` | 换为 compaction 内定位，填前 expect input count=1；原 catalog 错误原文和落盘精确模型 spec 断言不变 |
| 截图 | 在旧卡结构上截图 | 保留截图动作和路径，图中须能看见左栏选中「上下文压缩」与无卡框右侧 |

建议 helper 形态（测试实际保存断言不在此重写）：

```ts
const compaction = (page: Page) => page.locator('.editor-section[data-section="compaction"]');
async function showCompaction(page: Page) {
  await page.getByRole('navigation', { name: '配置分区', exact: true })
    .getByRole('button', { name: '上下文压缩', exact: true }).click();
  await expect(compaction(page)).toBeVisible();
  await expect(page.getByRole('region', { name: '上下文压缩', exact: true })).toBeVisible();
  await expect(compaction(page).getByRole('heading', { name: '上下文压缩', exact: true, level: 3 })).toBeVisible();
}
```

在同一 spec 增补一个结构回归用例：用 `open(page, {agents:{}, concurrency:{default:4}}, '', {section:null})` 加载无压缩值 fixture；角色模型默认可见 → 进入压缩 → 选择空的统一模型 → 切并发再回来 → custom 仍选中、错误仍在、点击保存后 save-count 仍为 null；检查可见 `.editor-section` 只有 1 个，未选分区控件不在 Tab 路径。不能使用带 compaction.model 的默认 fixture 来期待「空 custom」。它补测新行为，不替换原 6 个用例。再对导航 Up/Down/Home/End、Enter 做键盘流程检查（可在该用例中分段）。

### 5.3 `e2e/pi-amagi-config.html` 旧→新

- `h(PiAmagiConfig)` → `h(PiAmagiConfig, { navigation: 'local' })`，明确采用生产组件内的本地导航回退。
- 注释补充「独立挂载生产导航与分区外壳；未挂 ProviderCenterView」。不在 fixture 手写一份导航表、卡片包装或伪造 activeSection。
- Wails wrapper 的浏览器隔离 transport、catalog 数据、localStorage 键、save-count 累加、错误 query、toast output 全部不变。不得新增自动保存／预选压缩／吞错。
- 此 fixture 只证明 Pi 组件的真实结构和写入契约；不冒充整页 800px 布局、跨 CLI 懒加载或真实磁盘集成证据，后者按 §8 走查。

`.config-cat-card` 保留给组件本身及未来／范围外消费者，不再是两页的契约。不得为让 grep 通过删除 ConfigCategoryCard.vue。

## 6. 语义承载迁移表

「常显」指用户进入对应分区后无需额外展开说明；全文件级后果在 EditorToolbar 常显。左栏内只留必要的小标记，不塞说明段落。

### 6.1 v1 §5.3 逐项映射

| v1 语义／编号 | v2 常显挂载点 | 完整键盘承载 | 不可丢失的细节 |
|---|---|---|---|
| MCP Leader 重启生效 T19 | MCP 分区的 Leader FieldLabel effect | 原字段 InfoTip | 装配读一次、与子 Agent 独立 |
| OMP 新会话生效 T31 | OMP EditorToolbar effect | 无需展开 | 不移到仅某一个角色行 |
| MCP 默认即时生效 T20 | MCP 分区默认 FieldLabel effect | 原字段 InfoTip | 每次派发读盘，与角色附加取并集 |
| OpenCode 即时生效 T41 | 全局 EditorToolbar effect | 无需展开 | 所有分区与源码模式都在 |
| 下次压缩读取 T12 | 压缩左栏条目的次级 meta＋右侧 EditorSection meta | 右侧标题 InfoTip | 项目优先、目录继承、新凭据需重新派发 |
| 注册表重新进入生效 T46 | Registry EditorToolbar effect | 原工具栏 InfoTip | 重进 Agent 配置刷新模型候选 |
| amagi-* 覆盖风险 T47 | 提供商左栏「受管」＋右侧标题 meta＋`.pre-managed-warn` | 横幅全文直接可读 | 下次同步可能覆盖手动修改；不降为只 hover |
| MCP 三态／空列表 T18/T21 | 三态按钮＋原 StringListEditor 空态 | 原 FieldLabel 三态对照 | 未设置=全部；[] 不加载；白名单空保存等同不加载；角色附加例外保留 |
| default 清空回落 4 T24 | 并发分区原 placeholder | 原 default FieldLabel tip | 移除键、应用预填10、内置4三者不混淆 |
| OAuth 只读／pi CLI 管理 T53–55 | 当前凭据标题后的管理标签；add 空态保留登录途径 | 原 OAuth InfoTip | access／refresh 原样保留；导航绝不泄露令牌 |
| 压缩窗口不足取消 T13 | 压缩 custom 后果句 | EditorSection 标题 InfoTip 第4段 | 配置／认证／摘要失败也取消，历史保留 |

未证实的角色模型、profile、并发、auth 生效时机仍不添加标签。

### 6.2 其余卡片头与文案迁移

| 原挂载／文案 | v2 挂载／改动 |
|---|---|
| 所有 ConfigCategoryCard title/code/badge | EditorSection 的 h3＋相邻 code／数量；数量表达式原样复用，0 不自动隐藏 |
| Pi 并发卡 tip T23、OMP 两卡 tip T32/T33、OpenCode model tip T43 | 各自 EditorSection 标题旁 InfoTip，按钮不在 h3／导航按钮里 |
| profile、角色覆盖、压缩三态的 FieldLabel tips | 原位置保留，重排外壳不动内容 |
| T02「用于 …」 | 预设右侧列表工具栏前 `.pc-format-caption`，不再依赖 subnav |
| T04「合并水平 Tab」 | 被本文 §2 的左栏完全替换，选项名称继续不变 |
| T54「可在下方添加 API Key；OAuth 需通过 pi CLI 登录」 | 放在 add 叶子、表单上方的空态；「下方」仍对应真实位置 |
| 错误／目录不可用 T28/T29/T34/T51/T57 | 编辑器右侧工具栏下，全文件级；不塞进某个分区导致失联 |
| OpenCode 默认行 T35、预设徽章 T06、字段 code T48 | 原分组列表／字段行保持 |

T01–T57 中未列项继续按 v1 已实施结果保留，不能把删过的教学段落重新加回。

## 7. 新增／扩展基件规格

### 7.1 `ui/SectionNav.vue`

这是带分组和缩进条目的导航组件，使用 `<nav>`＋列表＋原生 button，**不用 tablist/tree 角色**：模式入口和具体叶子控制同一右侧区域，且包含异步动态内容，不强装成一组同级 tabs。

```ts
interface SectionNavItem {
  id: string;
  label: string;
  ariaLabel?: string; // 页面级：Pi · Agent 配置；子叶：Pi · Agent 配置 · 上下文压缩
  meta?: string;
  metaTone?: 'neutral' | 'warning';
  invalid?: boolean;
  disabled?: boolean;
  children?: SectionNavItem[]; // 只展开当前模式，最多一级
  scrollChildren?: boolean;   // 动态 N 使用限高滚动
}
interface SectionNavGroup { id: string; label?: string; items: SectionNavItem[] }
// props: groups, currentId, branchId?, label, controlsId
// emit: select(id)
```

- 每个实例有且只有一个可 Tab 进入的有效按钮（roving tabindex）；初始优先 currentId，其次 branchId，再次首个可用按钮。disabled 项不能被激活，方向键跳过；组标题不进入焦点序列。
- Up／Down 按可见 DOM 顺序移动焦点，不自动切换文件；Home／End 移到首尾。Enter／Space 才 emit select。不要在 arrow-key 时读配置，避免连续焦点移动触发 IO 和丢弃草稿。
- Tab 离开导航进入右侧；Shift+Tab 返回。方向键移动时 `scrollIntoView({block:'nearest'})` 使目标可见，包括动态子列表；只在键盘导航或当前项被删除时纠正焦点，不在普通 snapshot 更新后抢焦点。
- 当前叶子 `aria-current="page"`，父模式用 branch 样式标记，不同时冒充第二个 current。source／loading／error 没有可显示叶子时 current 落在模式入口。本地 source 模式全部叶子禁用时 nav 不设置假 tabindex，Tab 直接进入工具栏。
- 组标题关联其列表；模式按钮有 children 时可设 aria-expanded=true，其他模式=false，但**点击是选择模式，不是收起当前分支**。所有按钮 aria-controls 指向宿主始终存在的右侧容器，而不是未挂载的 section。
- meta 用描述节点或 aria-describedby，不改变 label；invalid 标记有可读「需修正」说明，不能仅靠红点。徽章不是独立按钮。
- 宽窗选中底用 `--control`，文字用 `--accent-strong`，左侧 2px accent；hover 用 controlHover；focus-visible 2px accent、内缩1px。没有卡片阴影。其余尺寸见 §4。
- 删除动态当前条目：数据层先确定后继选择，nextTick 后仅当焦点原来就在被删条目／已移除的表单节点，才聚焦后继导航按钮；从删除确认取消回来不移动。
- 切换分区时必须关闭属于旧分区的 InfoTip／Dropdown 浮层，使用 §7.2 的 active 上下文；不把关闭菜单作为额外一次点击。现场 Dropdown 只有 document mousedown／Escape 关闭，没有 blur 关闭，单靠焦点移出不足以保证此项。键盘与程序切换都必须覆盖。

### 7.2 `ui/EditorSection.vue`

只处理标题、语义挂载和后代浮层可见性，不管保存、数据加载或选择。props：`sectionId`、`title`、`active?:boolean`（默认 true）、`code?`、`badge?:number|null`、`meta?`、`metaTone?`、`tip?`、`tipItems?`；默认 slot 放原表单。宿主负责 visited／v-show，并让 active 与 v-show 使用同一表达式。

```vue
<section class="editor-section" :data-section="sectionId" role="region" :aria-labelledby="titleId">
  <header class="es-header">
    <h3 :id="titleId" class="es-title" tabindex="-1">{{ title }}</h3>
    <code v-if="code" class="es-code">{{ code }}</code>
    <span v-if="badge !== null && badge !== undefined" class="es-count">{{ badge }}</span>
    <span v-if="meta" :class="['es-meta', metaTone]">{{ meta }}</span>
    <InfoTip v-if="tip || tipItems?.length" :label="title" :text="tip" :items="tipItems" />
  </header>
  <div class="es-body"><slot /></div>
</section>
```

新增 `ui/sectionVisibility.ts` 仅导出 `EDITOR_SECTION_ACTIVE: InjectionKey<Readonly<Ref<boolean>>> = Symbol('editor-section-active')`（Vue 类型用 type import）。EditorSection provide `computed(() => props.active)`；InfoTip／Dropdown 可选 inject，无上下文时视为 true。watch 到 false 时 InfoTip 调用原 close（同时清除 hover 定时器），Dropdown 设置 open=false；不可见时不再接受延迟打开。无需改 ModelSpecSelector、FieldLabel 或其他中间业务组件的 props。该上下文只向下传一个展示布尔值，与异步组件导航接口／保存状态无关。

titleId 使用模块级计数生成，沿用 InfoTip 的 Vue 版本兼容方式；不用 Vue 3.5 useId。h3 仅在程序新增后按需聚焦，不加入日常 Tab 序列。`es-header` flex-wrap、gap 6px；标题 14px/600；code 11px mono；meta／数量沿用 v1 样式，数量不得混进 h3。section 无 border/radius/shadow/background，es-body 无水平 padding。InfoTip 永远是兄弟按钮，无交互元素嵌套。

### 7.3 本地挂载与共享样式边界

五个编辑器 navigation 默认 local 时，自带同一个 SectionNav 和局部 grid，便于独立挂载；external 时只输出 `.editor-detail`，由 ProviderCenterView 包住它。静态表共用 configNavigation.ts，不能为 fixture 硬编码压缩页。

局部 grid／列宽变量写在五个宿主各自的 scoped 样式中，以 `.pac-root.editor-local-layout`（其他宿主同理）明确覆盖自己的 flex 根样式；断点数值统一按 §4，不依赖全局 CSS 注入顺序。SectionNav 只负责自身列表，不反向给父节点注入布局样式。external 时 `.editor-detail` 和宿主根都设置 min-width:0，但宿主保持原 flex 流，不再创建网格。EditorToolbar／FieldLabel 的新增 CSS 都是收缩能力，默认宽窗不改外观，不改 props。无需额外页架组件或跨页面状态服务。

## 8. 验收清单修订

以下替换同号旧条目，并增补新项。未覆盖的 v1 验收继续有效；截图需要洛神实际打开查看，不能只存盘。

### 8.1 V：执行与证据

| 编号 | 验收 | 真实执行方式／期望 |
|---|---|---|
| V1 保留 | 类型门＋构建 | 仓库根：`npm --prefix frontend run build`，exit=0 |
| V2 保留 | 四个逻辑测试文件 | 仓库根：`npm --prefix frontend run test -- src/__tests__/components/provider`，全部通过；不改 mcpListMode/piCompaction/piConcurrency/useModelCatalog 的规则和测试断言 |
| V3 修订为必跑 L2 | Pi 结构适配和原保存契约 | 仓库根：`npx playwright test -c e2e/pi-amagi-config.playwright.config.ts`；原6用例＋新增结构用例全部通过，报告实际数量；不删除失败用例 |
| V4 新增 | 跨分区草稿 | Pi：空 custom、MCP 空白名单、并发空值来回保留；OpenCode：unknown RawJsonEditor 输入非法文本，切区再回来文本仍在；记录真实步骤与结果 |
| V5 新增 | 懒加载与生命周期 | 浏览器延迟／失败当前 chunk 后仍可点击全部模式；重试与配置档应用保留 presetLoadKey 行为；同文件切区 API 调用数不增加；Pi→OMP 注册表路径／格式正确，不能复用旧 engine 数据 |

V4/V5 可在真实浏览器或既有隔离测试设施下执行，必须区分隔离 transport 与真实后端。只为验证不要新增第三个 e2e 目录。本设计阶段未运行上述命令。

### 8.2 S：结构与不变量

| 编号 | 修订 |
|---|---|
| S1 | 不再要求编辑器内所有标题为0；要求旧编辑器 h2/subtitle/hint 为0，允许且要求当前 EditorSection 有一个 h3。不恢复长说明 |
| S2 | 原6个 EditorToolbar 使用者保持，路径行不回归 |
| S3 | 两页无 pc-subnav；每页恰好一条左栏。CLI 7模式入口由父级渲染，编辑器卡住时仍存在 |
| S4 | T01–T57 依 v1 保留，本文 §6 的挂载差异逐项核对 |
| S5 | v1 §5.3 全部语义按本文 §6.1 核对；受管风险、OAuth管理、取消压缩后果不能仅在 tooltip 中 |
| S6 | lazyPreset、PresetLoadError、7个 presetLoadKey 挂载、watch(mainTab)、engine按需加载、全部保存函数体不变；展示接口不能额外读取或写盘 |
| S7 | diff 仅限本文列出的前端文件、新基件／纯导航模块及指定 e2e 两文件；不碰 Go、wailsjs、API、stores、其他视图 |
| S8 | package manifests／lockfile 无新依赖 |
| S9 新增 | 两页对 ConfigCategoryCard 的 import／使用为0，但组件文件及其v1扩展保留；没有伪卡片供旧e2e定位 |
| S10 新增 | 只有一个 section 可见；切分区不卸载编辑器、不修改源文本、不清洗数据。已访问表单保留，未访问表单不提前实例化 |
| S11 新增 | nav snapshot 无任何密钥／OAuth令牌；过期 owner 的报告被忽略；无缓存未访问 engine 的动态数据 |
| S12 新增 | 配置页右侧无分类卡堆叠；OpenCode 内部对象表现为分隔列表行，Registry模型为无框行组；不以“只改class名”冒充去卡化 |

### 8.3 X：交互与窗口覆盖

| 编号 | 修订 |
|---|---|
| X1 | 1280×800 两种预设＋CLI七模式：左栏／单内容区清楚，宽档右侧不恢复重复h2；导航、工具栏、分区标题按本文层级，无水平二三级Tab |
| X2 | 800×600 两种预设＋七模式：应用侧栏仍264；左栏112；右侧≥340（计滚动条）；无页面横溢、操作遮挡或截断重要后果。1024档抽测工具栏与注册表 |
| X3 | 说明文案继续降级；最小档允许换行，不能以单行上限裁掉错误／覆盖风险 |
| X4 | 原InfoTip键盘规格保持；新导航Tab单入口、上下/Home/End移动、Enter/Space激活；重复名称可通过完整路径分辨 |
| X5 | InfoTip在右侧标题、窄栏字段和视口底部完整展示；切分区后没有残留浮层 |
| X6/X7 | 预设行键盘开弹窗、删除焦点、皮肤浮层不透明继续验收 |
| X8 新增 | 从任意模式到所有静态叶子≤2点击；Registry/Auth 用N=0、1、20的隔离数据验证动态滚动、添加、删除后继和长名（明确标注fixture，不用真实凭据截图） |
| X9 新增 | OpenCode model存在／不存在、unknown存在／消失，导航与正文一致；最多9分区；空文件能从provider开始编辑 |
| X10 新增 | 源码模式不伪装成分区源码；切回可视化保留有效选择，非法Pi源码不被覆盖；隐藏错误有定位入口 |

实现切片建议：基件与纯导航描述 → ProviderCenterView＋两类预设 → Pi＋e2e → OMP／Registry／Auth → OpenCode全局及嵌套列表样式 → V1–V5与X矩阵。每片构建，Pi结构完成立即跑V3，避免最后才发现选择器失效。

## 9. 风险与回滚

### 9.1 风险、代价与限制

| 风险 | 影响 | 本轮处理 |
|---|---|---|
| 112px文字栏和滚动密度 | 长动态名称需要更多行 | 保留完整可访问名、详情完整标题、动态限高滚动；800px实际观感由洛神复核 |
| 新导航展示接口与异步状态错配 | 旧注册表名称可能短暂出现在新模式 | owner校验；切模式即清动态快照；不缓存跨engine数据 |
| 为单显直接v-if卸载 | 丢手动输入态／非法局部JSON | visited＋v-show，专门V4验证 |
| 已访问动态实例积累 | 同一编辑器中内存随访问条目增加 | 只挂载已访问项；跨模式按现状卸载；不为未经证实规模引入虚拟化／LRU |
| 嵌套子编辑器固定宽度 | 单测通过但最小窗溢出 | scoped deep定向收缩，X2覆盖权限／实验字段／递归对象等复杂分区 |
| 跨文件草稿仍在离开时丢弃 | 左栏让跨模式更易点到 | 不扩大为自动保存或缓存；保持已有边界，在交付中明确说明 |
| 当前源码既存解析／局部草稿校验差异 | 不能宣称所有编辑器与Pi同样严格 | 本轮不重写解析和保存；例如RawJsonEditor非法文本原本不emit，保留该行为并列为既存边界，不把它视为新增成功保存 |
| 旧文档仍称二／三级Tab | 术语与形态可能不再一致 | 导航名不改；后续文档跟进由Leader决定，本轮不越界写docs |

代价集中在导航元数据、两个轻量UI组件、五个编辑器的展示接口／选择状态和窄栏样式；API、配置格式及数据迁移成本为零。OpenCodePresets／PresetList 不改弹窗交易流程，避免扩大行为面。

### 9.2 回退到已实现 v1 的路径

当前 v1 含未提交修改及新增基件，**HEAD 不等于 v1 实现态**。不能用 restore/reset 到 HEAD 作为回滚。

后续实现开始前，由获授权的实现者在其产物区记录只读 `git status`、`git diff HEAD -- frontend/src`、指定e2e差异，并保存本轮即将触碰文件的 v1 内容副本／精确补丁（包括未跟踪的 InfoTip／FieldLabel／EditorToolbar；普通git diff不包含未跟踪文件）。快照只用于归因，不授予覆盖共享树的权限。

若主上否决 v2：

1. 只反向应用 **v2相对该v1快照** 的改动，先恢复容器与编辑器调用，再恢复e2e。已经有并行修改的文件按hunk合并，不能整文件覆盖他人内容。
2. 撤销v2专用SectionNav／EditorSection／configNavigation及sectionVisibility引用，回退InfoTip／Dropdown的可选可见性消费。新增文件只有确认无人引用且获授权后才删除；保留不用的文件不影响数据。
3. 保留v1的InfoTip／FieldLabel／EditorToolbar、ConfigCategoryCard扩展、tokens、文案矩阵和分组列表。不存在需要撤销的数据格式升级，也不触碰用户配置文件。
4. 重跑v1的构建／逻辑测试和与v1快照匹配的e2e，恢复截图只作为对照，不能冒充本次回退验证。

若v2已独立提交，可经Leader授权反向应用该独立提交的相关hunk；本设计不授权commit、revert或任何破坏性Git操作。

### 9.3 未知项与裁决边界

- 实际渲染的密度、字重和多层滚动体验：本文给出尺寸与失败判据，尚无v2截图；需实现后由洛神在1280／800窗口看图裁决，无数据不可逆风险。
- 非覆盖滚动条和原生WebView上的容器查询表现：以两目标平台验证。实现同时写 `@supports not (container-type: inline-size)` 回退：仅对本轮编辑器无条件应用 §4.2 的窄工具栏排布和 §4.3 的单列表单规则；列宽仍按 §4.1 的窗口 media query。旧WebView可牺牲宽窗并排密度，但不退回卡片或隐藏文字左栏，无需另加JS测宽。
- 角色模型、profile、并发、auth的生效时机仍未证实，沿用v1未知边界，不编造标签。
- 本轮没有可执行shell，无法提供当前工作树相对HEAD的归因结论；设计以实际读取的当前文件为基线。实现启动前的差异核对与v1快照是必要检查，不需要重新决定设计。

### 9.4 设计阶段验证记录

- 工作目录：`/Users/run.mao/workpace/amagi-codebox`。实际使用read／grep／find，只写本文件；未执行构建、测试、浏览器或Git命令，退出码不适用。
- 完整读取：v1 design、p1、p1r；ProviderCenterView；PresetList、PiAmagiConfig、OmpGlobalConfig、OpenCodePresets、OpenCodeGlobalConfig、ProviderRegistryEditor、AuthConfigEditor；EditorToolbar、InfoTip、FieldLabel。内联读取的Segmented、ConfigCategoryCard、e2e两文件和main.go纳入证据，不重复读取。
- 补充核对：ModelSpecSelector、RawJsonEditor、ProviderMapEditor；Dropdown的空选项渲染、Teleport和关闭监听；AppShell/Sidebar与box-sizing；相关递归／键值子编辑器样式；四个逻辑测试全文；e2e Playwright配置；tokens与v1共享件；demo §5.3。`grep ConfigCategoryCard frontend/src`确认当前模板消费者为Pi、OMP、Registry、OpenCode全局四处。
- 静态自检：A/B裁决、预设页结论、七模式完整树、OpenCode条件项、动态N选择规则、800px预算、加载失败可导航、同文件草稿保留、e2e旧→新、v1语义表逐项迁移和v1回滚均有明确落点。
- 未覆盖：真实Git差异归因、运行时交互、构建／测试结果、v2视觉审查。以上均未写成已通过；下游按§8验收，视觉质量由洛神裁决。
