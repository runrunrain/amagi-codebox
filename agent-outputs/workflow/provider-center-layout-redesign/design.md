# Provider Center「预设」与「CLI 独立配置」布局重设计

> 角色：fuxi（设计态） · 下游：luoshen 实现、diting 审查
> 上游任务书：`agent-outputs/workflow/provider-center-layout-redesign/p1-taskbook.md`
> 实地读取的输入：`frontend/src/views/ProviderCenterView.vue`；`frontend/src/components/provider/{PresetList,PiAmagiConfig,OmpGlobalConfig,OpenCodePresets,OpenCodeGlobalConfig,ProviderRegistryEditor,AuthConfigEditor,ConfigCategoryCard}.vue`、`icons.ts`；`frontend/src/components/ui/{Segmented,PageHead,EmptyState,StatusBanner,ConfigCard,AppButton,Badge,Chip,Dropdown}.vue`；`frontend/src/styles/tokens.css`、`skin.css`；`demo/前端重构交接说明.md` §5.3；`frontend/src/__tests__/components/provider/*.test.ts`（4 个）；`e2e/pi-amagi-config.{spec.ts,html,playwright.config.ts}`；`docs/user/usage.md` 中 Provider Center 一节；`main.go`（窗口最小宽度）。
> 本文是实现规格。凡是写了「必须」的地方，实现时不需要再回头找设计确认。

---

## 1. 问题定义与设计原则

### 1.1 问题的实质

页面文字多只是表象，问题出在三个结构缺口上：

1. **导航已经说过的话，正文又说了一遍。** 用户切到「CLI 独立配置 → Pi → Agent 配置」后，正文开头还有 h2「Pi 配置（amagi-pi）」和两句 subtitle，等于把导航路径重新念了一遍。同样的页头在 5 个编辑器里各渲染一次。
2. **说明文字没分层。** 生效时机、覆盖风险这类「操作后果」，和「角色名对应 agents/*.md」这类「背景知识」用的是同一种 12px 灰色段落，常驻显示，读者分不出哪句重要。
3. **配置键挤在括号里。** 「Leader 服务器（mcp.leader，主进程加载名单）」这种写法，把显示名、配置键、语义解释塞在同一行，括号又套括号。

### 1.2 「正式应用」的判据（可度量，§7 验收直接引用）

| # | 原则 | 判据 |
|---|---|---|
| P1 | 导航即标题 | 导航路径（Tab 名）或卡片标题已经表达的内容，正文不再重复：编辑器内 h2 + subtitle 为 0 处 |
| P2 | 一屏聚焦 | 第一个内容卡片上方的 chrome 行数 ≤ 3（一级导航 / 二三级合并导航 / 编辑器工具栏），现状是 7–9 行 |
| P3 | 常显只留状态和后果 | 字段下方常驻可见的文字只允许三类：①当前选项决定写入什么（状态语义）；②操作后果（生效时机、覆盖、取消或丢失）；③校验或错误。其余要么删除，要么收进 InfoTip |
| P4 | 微文案字数上限 | 常显说明每块 ≤ 24 个汉字、单行、不嵌套括号；一个字段最多 1 条常显说明 |
| P5 | 键名代码化 | 配置键用等宽小字 code 标注，不再写成「（xxx）」 |
| P6 | 渐进披露可达 | 降级后的文字只放在可聚焦的 InfoTip 按钮后面（键盘 Enter/Space 打开、Esc 关闭），不依赖纯 hover 或 `title` 属性 |
| P7 | 空态 = 标题 + 动作 | 空态描述最多 1 行，而且只在补充非显然事实时才写；引导靠按钮完成，不写「点击右上角…」 |

### 1.3 不变量（实现中任何时候都不能破坏）

- I1 语义不丢：任务书 §6 列出的 6 类语义都有常显或键盘可达的承载（对应关系见 §5.3）。
- I2 行为不回退：懒加载、`presetLoadKey` 重挂载、engine 按需加载 watch、远程横幅、导入/导出/配置档按钮、各编辑器的 `handleSave` 链路、api 层调用全部不动。
- I3 契约不破：4 个 vitest 用例、`e2e/pi-amagi-config.spec.ts` 依赖的选择器和按钮名不变（清单见 §7.3）。

---

## 2. 候选方案与权衡

### 2.1 页架（任务书 §5-1）

| 方案 | 做法 | 优点 | 代价与风险 | 可逆性 |
|---|---|---|---|---|
| A 统一页架上提 | ProviderCenterView 渲染一个 EditorFrame（标题/路径/模式切换/保存），编辑器通过 `defineExpose` 或 provide/inject 暴露 `mode/saving/path/save()` | 只有一处渲染，一致性靠结构保证 | ①状态要跨 `defineAsyncComponent` 边界：Loading 与 PresetLoadError 期间页架没有数据源，`presetLoadKey` 重挂载时引用会失效；②`e2e/pi-amagi-config.html` 单独挂载 PiAmagiConfig，「保存配置」按钮会消失，e2e 全部失效；③6 个组件之间要新增一套隐式接口 | 低：接口一旦横跨 7 处挂载点就很难回退 |
| **B 组件内紧凑工具栏 + 共享基件（选定）** | 删除各编辑器的 h2/subtitle（导航即标题），每个编辑器渲染同一个 `ui/EditorToolbar`（路径胶囊、合法状态、生效标签、模式切换、保存，共一行） | 组件保持独立，懒加载 chunk 不变；ProviderRegistryEditor 双引擎只靠 `engine` prop 区分路径与格式；e2e 宿主不受影响；一致性由共享基件保证 | 每个编辑器各改一处模板；工具栏在 6 个文件里各写一次（只是调用） | 高：每个文件可以单独回退，基件是新增的 |
| C 侧栏源列表（macOS 系统设置风） | 二级（Pi/OMP/OpenCode）和三级合并成左侧分组列表，右侧放编辑器 | 「正式感」最强，导航只占一处 | 窗口最小宽度 800（`main.go`），减去应用侧栏 264、页边距和卡片内边距，内容区只剩约 432px，再放 170px 源列表后编辑器不够用；要做到可用就得写两套响应式布局 | 中 |

**选定 B。** 理由：A 会带来行为回退（e2e 宿主、懒加载状态），违反 I2/I3；C 的空间约束是硬约束，不是偏好问题。B 已经能把 chrome 行从 7–9 行压到 3 行，满足 P1/P2。C 留作日后最小窗口宽度提高后的升级路径。

### 2.2 二三级导航（任务书 §5-5）

| 方案 | 做法 | 结论 |
|---|---|---|
| N1 保持现状：zone-label 行 + underline 行 + pill 行，三行 | — | 否决：一级、三级、模式切换三组 pill 上下堆叠，层级分不清 |
| **N2 合并为一行 subnav（选定）** | 左侧放二级 underline Tab，右侧放三级紧凑 pill，整行底部画一条通栏分隔线；预设页右侧放「适用 CLI」说明 | 少 2 行；三级随二级出现，归属一眼可见；最小宽度下 `flex-wrap` 自动换行 |
| N3 三级下放进编辑器工具栏（slot 注入） | — | 否决：异步组件加载中或加载失败时 slot 不渲染，三级导航跟着消失，用户切不到别的 Tab（行为回退） |

**命名**：所有 Tab 名保持不变（含「模型提供商」）。`docs/user/usage.md` 和 `providers.md` 按这些名字写了用户文档，改名会造成文档漂移。曾考虑把「模型提供商」改成「模型注册表」（可以避免和一级「服务提供商」混淆），列为后续选项，前提是同步更新用户文档。本轮不改。

### 2.3 说明文字承载形态

| 形态 | 代号 | 适用 |
|---|---|---|
| 删除 | L0 | 纯教学内容，或和导航/控件重复 |
| 内化进标签/选项/键名 | L1 | 括号里的键名改成 code；含义已经写在选项 label 里 |
| 常显微文案（≤24 字单行） | L2 | 当前状态语义、必须看到的后果 |
| 生效标签（Badge） | L3 | 生效时机，放在工具栏、卡片头或字段标签右侧，常显 |
| InfoTip（可聚焦按钮 + 浮层） | L4 | 结构性解释（三态对照）、优先级、来源、次要后果 |
| 内联警示（StatusBanner compact） | L5 | 覆盖风险、错误、目录加载失败 |
| 空态 / placeholder | L6 | 下一步引导、输入格式、「留空 = 什么」 |

**判据（按顺序判断，命中即停）：**
1. 内容和导航、卡片标题、控件 label 重复 → **L0**
2. 内容是配置键名或格式示例 → **L1**（code）或 **L6**（placeholder）
3. 内容是生效时机 → **L3**，必要时再挂 **L4** 补充细节
4. 内容是覆盖风险、数据丢失或校验错误 → **L5**
5. 内容是当前选中状态会写入什么 → **L2**，只显示当前态一条，全部状态的对照放进 **L4**
6. 内容是背景、优先级或来源 → **L4**
7. 只在某个状态下才有意义（空列表、留空、某个模式） → 只在该状态出现（**L6/L2**）

**InfoTip 挂载位置的规则：** 挂在离内容最近的有名元素上，字段标签优先，其次卡片标题。一个卡片头最多 1 个 InfoTip，一个字段最多 1 个。

---

## 3. 选定方案：信息架构与视觉规格

### 3.1 预设页信息架构（mainTab = presets，两种格式共用一套布局）

```
PageHead  「Provider Center」 / 「管理服务提供商、公共预设与各 CLI 独立配置」
[远程模式] RemoteScopeBanner(mode=local)                      ← 保持
ConfigCard.pc-card
├─ pc-head ：[服务提供商 | 预设 | CLI 独立配置](pill)      [配置档][导出完整配置][导入完整配置]   ← 保持
└─ pc-panel
   ├─ pc-subnav ：Anthropic 格式 · OpenAI 格式 (underline)  ··········  用于 Claude Code
   │                                                       （OpenAI 时：用于 Codex · Pi · OMP）
   └─ PresetList(format)
      状态：Loading ─ Error(重试) ─ 内容
      ├─ toolbar：[全部][provider…](Chip)                         [+ 添加预设](primary)
      ├─ preset-group（分组列表，单个容器，行间用 1px 分隔线）
      │   └─ preset-row × N：名称(可点击编辑) · provider 标签 · 删除
      │                      徽章行：model · H/S/O 档位 · 识图/识视频 · 同步 Pi/OMP · thinking/effort/T
      └─ EmptyState
          无预设   → 标题「暂无 Anthropic 格式预设」 + [添加预设]
          筛选无果 → 标题「{provider} 下暂无预设」 + [显示全部]
```

### 3.2 CLI 独立配置页信息架构（mainTab = cliconfigs）

```
pc-panel
├─ pc-subnav ：Pi · OMP · OpenCode (underline)   ·····   [Agent 配置|模型提供商|认证登录](紧凑 pill，随二级切换)
└─ 编辑器（defineAsyncComponent，:key=presetLoadKey）
   状态：LoadingState ─ PresetLoadError(重试) ─ 编辑器自身 Loading ─ ErrorState(重试) ─ 内容
   内容统一骨架：
   ├─ EditorToolbar：[📄 …/agent/amagi.json ⧉] [JSON 合法]      [生效标签 ⓘ] [可视化|JSON] [保存配置]
   ├─ 警示区：解析错误(StatusBanner compact error, role=alert) / 目录加载失败(warning)
   └─ 可视化：ConfigCategoryCard × N  |  源码：深色等宽编辑器（保持）
```

各子视图的状态与区块：

| 二级 | 三级 | 组件 | 工具栏生效标签 | 可视化区块（自上而下） |
|---|---|---|---|---|
| Pi | Agent 配置 | PiAmagiConfig | 无（各区块生效时机不同，标签下沉到卡片/字段） | 角色模型（含分层策略）· 上下文压缩〔下次压缩生效〕· MCP 路由（Leader〔重启 pi 生效〕/ 默认〔即时生效〕/ 角色附加）· 并发限制 |
| Pi | 模型提供商 | ProviderRegistryEditor engine=pi | 重新进入后生效 ⓘ | 提供商卡 × N（amagi-* 带〔受管〕标签和警示）· 添加提供商 · 其他顶层键 |
| Pi | 认证登录 | AuthConfigEditor | 无 | 凭据条目 × N（OAuth 行带〔pi CLI 管理〕ⓘ）· 空态 · 添加 API Key |
| OMP | Agent 配置 | OmpGlobalConfig | 新会话生效 | 角色模型 ⓘ · 子代理覆盖 ⓘ |
| OMP | 模型提供商 | ProviderRegistryEditor engine=omp | 重新进入后生效 ⓘ | 同 Pi（YAML） |
| OpenCode | 预设管理 | OpenCodePresets | 无 | 工具栏左侧放搜索 · 分组列表（本机默认行 + 预设行）· 空态 / JSON 只读视图 |
| OpenCode | 全局配置 | OpenCodeGlobalConfig | 即时生效 | （顶层模型，仅在键存在时显示）· 提供商 · Agent · MCP 服务器 · 权限 · 指令文件 · 插件 · 实验特性 ·（其他键） |

### 3.3 视觉规格

**纵向节奏（单位 px）**

| 位置 | 间距 |
|---|---|
| pc-head → pc-subnav | 12（`.pc-panel { margin-top: 12px }`） |
| pc-subnav → 首个内容（PresetList 工具栏 / EditorToolbar） | 14（`.pc-panel` 的 gap） |
| EditorToolbar → 警示区 → 内容 | 12（编辑器根的 gap，由现在的 16 改为 12） |
| 卡片之间 | 10（保持） |
| 卡片内字段之间 | 14 |
| 字段标签 → 控件 | 6 |
| 控件 → 常显微文案 | 4 |

**字号与色彩层级（只用现有 token）**

| 元素 | 字号/字重 | 颜色 |
|---|---|---|
| 一级 pill | 13 / 400（选中态白底） | 保持 |
| subnav underline | 13 / 选中 600 | 选中 `--label`，下划线 `--accent` |
| subnav 右侧说明（适用 CLI） | 12 / 400 | `--tertiary` |
| 三级紧凑 pill、工具栏模式切换 | 12 / 400，seg padding `4px 12px`，容器 padding 2、radius 8 | 保持 Segmented 的配色 |
| 路径胶囊 | 11.5 mono；高 26；radius `--radius-sm` | 底 `--control`；目录 `--tertiary`；文件名 `--secondary` |
| 合法状态 | 12 / 500，前缀 ✓ | `--success-strong` |
| 卡片标题 | 13.5 / 560（保持） | `--label` |
| 卡片/字段 code | 11（卡片）/ 10.5（字段）mono | `--tertiary` |
| 生效标签、卡片 meta 标签 | 11 / 500，padding `1px 7px`，radius 999 | 中性：`--secondary` 字 / `--control` 底；警示：`--warning-strong` 字 / `--warning-tint` 底 |
| 字段标签 | 12 / 500 | `--secondary` |
| 常显微文案 | 12 / 400，line-height 1.5 | 状态语义用 `--tertiary`；后果用 `--secondary` |
| InfoTip 图标 | 14px 描边圆 i，按钮 18×18 | 默认 `--tertiary`；悬停/打开/聚焦时 `--accent` |
| InfoTip 浮层 | 12 / line-height 1.55，最大宽 280 | 底 `--popover`，字 `--label`，`dd` 用 `--secondary` |

**徽章与图标**
- 卡片类别图标改用 `icons.ts` 中已有的 key（现状中的 `profile/agents/modelRoles/overrides` 都会回落到 unknown 灰图标）：角色类 → `agent`，模型类 → `model`，提供商 → `provider`，其余保持。
- 生效标签只用于有证据的生效时机（见 §5.3），不能自行补写。
- 预设徽章：`param.vision` 的文字色改为 `var(--success-strong)`（现状是 #34C759 浅底浅字，对比度不足）；其余徽章配色保持。

### 3.4 新增 token（`styles/tokens.css`，只增不改）

```css
  /* Floating layers (InfoTip)：实色，不受 skin 面板混色影响 */
  --popover: #FFFFFF;
  --shadow-pop: 0 8px 24px rgba(0, 0, 0, .16);
  /* Tinted background for warning-strong text（meta 警示标签） */
  --warning-tint: rgba(255, 149, 0, .12);
```
- `skin.css` 不覆盖这三个变量，所以皮肤模式下 InfoTip 仍是不透明底（和 Dropdown 的实色防护等效），不需要改 skin.css。
- 文件头写着「Do not alter without a spec change」，本文就是对应的 spec change，在注释里注明「provider-center-layout-redesign」。

---

## 4. 逐文件改造指令

> 统一约定：
> - 删除的模板节点，对应的 scoped 样式一并删除。
> - 保留现有 class 名作为测试锚点，不要重命名 `.pac-json-editor`、`.pac-catalog-warn`、`.pac-json-error`、`.config-cat-card`、`.mss-*`。
> - 所有 `handleSave / initialLoad / parse* / serialize / update*` 函数体不改，只改模板和展示用常量。

### 4.0 新增与扩展的共享件（先做，S1）

#### 4.0.1 `components/ui/InfoTip.vue`（新增，规格见 §6.1）

#### 4.0.2 `components/ui/FieldLabel.vue`（新增）

```vue
<template>
  <div class="field-label">
    <label v-if="htmlFor" :for="htmlFor" class="fl-text">{{ text }}</label>
    <span v-else class="fl-text">{{ text }}</span>
    <code v-if="code" class="fl-code">{{ code }}</code>
    <InfoTip v-if="tip || items?.length" :label="text" :text="tip" :items="items" />
    <span v-if="effect" class="fl-effect">{{ effect }}</span>
  </div>
</template>
<!-- props: text: string; code?: string; tip?: string | string[];
     items?: { term: string; desc: string }[]; effect?: string; htmlFor?: string -->
```
- 不能用 `<label>` 包住 InfoTip。没有 `for` 的 label 会把内部第一个按钮当作被标注控件，点击标签文字会误开浮层，所以只有传了 `htmlFor` 时文字部分才用 `<label>`。
- 样式：`.field-label{display:flex;align-items:center;gap:6px;min-height:20px}`；`.fl-text{font-size:12px;font-weight:500;color:var(--secondary)}`；`.fl-code{font-family:var(--mono);font-size:10.5px;color:var(--tertiary)}`；`.fl-effect{margin-left:auto;font-size:11px;font-weight:500;color:var(--secondary);background:var(--control);border-radius:999px;padding:1px 7px;white-space:nowrap}`。

#### 4.0.3 `components/ui/EditorToolbar.vue`（新增）

```vue
<template>
  <div class="editor-toolbar">
    <div class="et-lead">
      <slot name="leading" />
      <button v-if="path" type="button" class="et-path" :title="path"
              :aria-label="`复制${pathLabel}路径：${path}`" @click="$emit('copy-path')">
        <svg class="et-ic" aria-hidden="true"><!-- 文档图标 12px --></svg>
        <span class="et-path-dir">{{ dir }}</span><span class="et-path-base">{{ base }}</span>
        <svg class="et-ic et-copy" aria-hidden="true"><!-- 复制图标 12px --></svg>
      </button>
      <span v-if="validLabel" class="et-valid">✓ {{ validLabel }}</span>
      <slot name="status" />
    </div>
    <div class="et-trail">
      <span v-if="effect" class="et-effect">{{ effect }}</span>
      <InfoTip v-if="effectTip" :label="effect || '生效时机'" :text="effectTip" />
      <slot name="actions" />
    </div>
  </div>
</template>
<!-- props: path?: string; pathLabel?: string = '配置文件'; validLabel?: string;
     effect?: string; effectTip?: string | string[]
     emits: 'copy-path'
     dir/base：按最后一个 '/' 或 '\\' 切分（Windows 路径兼容），dir 含末尾分隔符 -->
```
- 样式要点：
  - `.editor-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;min-height:32px}`
  - `.et-lead{display:flex;align-items:center;gap:8px;flex:1 1 240px;min-width:0}`；`.et-trail{display:flex;align-items:center;gap:8px;flex:0 0 auto}`
  - `.et-path{display:inline-flex;align-items:center;gap:6px;height:26px;max-width:min(100%,440px);min-width:0;padding:0 8px;border:none;border-radius:var(--radius-sm);background:var(--control);color:var(--secondary);font-family:var(--mono);font-size:11.5px;cursor:pointer}`，hover 时 `background:var(--controlHover);color:var(--label)`，`:focus-visible{outline:2px solid var(--accent);outline-offset:1px}`
  - `.et-path-dir{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--tertiary)}`；`.et-path-base{flex:0 0 auto;white-space:nowrap}`：文件名永不截断，只截断目录
  - `.et-copy{opacity:.55}`，`.et-path:hover .et-copy, .et-path:focus-visible .et-copy{opacity:1}`
  - `.et-valid{font-size:12px;font-weight:500;color:var(--success-strong)}`；`.et-effect` 和 `.fl-effect` 同规格
  - 紧凑化插槽里的 Segmented：`.et-trail :deep(.segmented){display:inline-flex;padding:2px;border-radius:8px}`、`.et-trail :deep(.seg){flex:0 0 auto;padding:4px 12px;font-size:12px;white-space:nowrap}`
- 复制逻辑留在各编辑器自己的 `copyPath()`（沿用 toast），ui 基件不依赖 useToast。

#### 4.0.4 `components/ui/StatusBanner.vue`（扩展）
- 新增 prop `compact?: boolean`，生成 class `compact`：`padding:6px 10px; border-radius:8px; gap:8px; font-size:12px`。默认值 false，现有调用零变化。
- `role` 等属性通过单根 attrs 透传（Vue 默认行为），不新增 prop。

#### 4.0.5 `components/ui/EmptyState.vue`（扩展）
- `description` 改为可选（默认 `''`），模板改成 `<p v-if="description">`。现有调用都传了值，零变化。

#### 4.0.6 `components/provider/ConfigCategoryCard.vue`（扩展，向后兼容）

新增 props：`code?: string`、`meta?: string`、`metaTone?: 'neutral' | 'warning'`（默认 neutral）、`tip?: string | string[]`、`tipItems?: { term: string; desc: string }[]`。

```vue
<div :class="['config-cat-card', { collapsed: !expanded, 'has-tip': hasTip }]">
  <div class="ccc-head">
    <button type="button" class="ccc-header" :aria-expanded="expanded" @click="$emit('toggle')">
      <div class="ccc-thumb" :style="thumbStyle"><span class="ccc-thumb-icon" v-html="categoryIcon" /></div>
      <div class="ccc-meta">
        <span class="ccc-title">{{ title }}</span>
        <code v-if="code" class="ccc-code">{{ code }}</code>
        <span v-if="badge !== null && badge !== undefined" class="ccc-badge">{{ badge }}</span>
      </div>
      <span v-if="meta" :class="['ccc-tag', `tone-${metaTone}`]">{{ meta }}</span>
      <svg class="ccc-chevron" ...>…</svg>
    </button>
    <InfoTip v-if="hasTip" class="ccc-tip" :label="title" :text="tip" :items="tipItems" />
  </div>
  <div v-if="expanded" class="ccc-content"><slot /></div>
</div>
```
- InfoTip 不能放进 header button 里（交互元素嵌套不合法），所以作为兄弟节点绝对定位：`.ccc-head{position:relative}`；`.has-tip .ccc-chevron{margin-left:28px}`（给 tip 预留位置）；`.ccc-tip{position:absolute;right:36px;top:50%;transform:translateY(-50%)}`。视觉顺序是 `标题 code 计数 …… meta ⓘ ⌄`，Tab 顺序是先 header 再 tip。
- `.ccc-code{flex:0 0 auto;font-family:var(--mono);font-size:11px;color:var(--tertiary);white-space:nowrap}`；`.ccc-tag{flex:0 0 auto;font-size:11px;font-weight:500;border-radius:999px;padding:1px 7px;white-space:nowrap}`；`.tone-neutral{color:var(--secondary);background:var(--control)}`；`.tone-warning{color:var(--warning-strong);background:var(--warning-tint)}`。
- header 的可访问名会变成「标题 code 计数 meta」。e2e 用的是 `/上下文压缩/` 正则，仍能命中。
- 现有消费者只有本次范围内的 4 个编辑器（已 grep 确认）。不传新 props 时渲染结果和现在一致。

#### 4.0.7 `styles/tokens.css`：按 §3.4 追加 3 个 token。

---

### 4.1 `views/ProviderCenterView.vue`

**删除：** 两处 `.pc-zone-label` 节点；样式 `.pc-zone-label`、`.zn-sep`、`.pc-engine-group`、`.pc-group-divider`（未使用）、`.pc-engine-tabs`、`.oc-mode-tabs`、`.pc-presets-panel`。

**改动：**
- `<PageHead title="Provider Center" description="管理服务提供商、公共预设与各 CLI 独立配置" />`（理由见 §5.2 T01）。
- 新增 computed：`formatConsumers = store.presetEngine === 'openai' ? '用于 Codex · Pi · OMP' : '用于 Claude Code'`。
- Tab 常量（`MAIN_TABS/FORMAT_TABS/CLI_TABS/PI_MODES/OMP_MODES/OPENCODE_MODES`）文案不改。

**新骨架（只列面板部分；pc-head、横幅、详情模式、对话框保持不动）：**
```vue
<div v-else-if="mainTab === 'presets'" class="pc-panel">
  <div class="pc-subnav">
    <Segmented v-model="formatModel" :options="FORMAT_TABS" variant="underline" class="pc-subnav-tabs" />
    <span class="pc-subnav-caption">{{ formatConsumers }}</span>
  </div>
  <PresetList v-if="store.presetEngine === 'anthropic'" format="anthropic" />
  <PresetList v-else-if="store.presetEngine === 'openai'" format="openai" />
</div>

<div v-else-if="mainTab === 'cliconfigs'" class="pc-panel">
  <div class="pc-subnav">
    <Segmented v-model="cliModel" :options="CLI_TABS" variant="underline" class="pc-subnav-tabs" />
    <Segmented v-if="store.presetEngine === 'pi'" v-model="piMode" :options="PI_MODES" variant="pill" class="pc-subnav-modes" />
    <Segmented v-else-if="store.presetEngine === 'omp'" v-model="ompMode" :options="OMP_MODES" variant="pill" class="pc-subnav-modes" />
    <Segmented v-else-if="store.presetEngine === 'opencode'" v-model="openCodeMode" :options="OPENCODE_MODES" variant="pill" class="pc-subnav-modes" />
  </div>
  <template v-if="store.presetEngine === 'pi'">
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
```
**样式：**
```css
.pc-panel { margin-top: 12px; display: flex; flex-direction: column; gap: 14px; }
/* 服务提供商面板也用 .pc-panel：它只有一个子元素，gap 不起作用，视觉不变 */
.pc-subnav { display: flex; align-items: flex-end; justify-content: space-between;
             gap: 8px 16px; flex-wrap: wrap; border-bottom: 1px solid var(--separator); }
.pc-subnav-tabs :deep(.segmented) { display: inline-flex; border-bottom: none; }
/* underline-seg 自带 margin-bottom:-1px，选中下划线会压在通栏分隔线上 */
.pc-subnav-caption { font-size: 12px; color: var(--tertiary); padding-bottom: 10px; white-space: nowrap; }
.pc-subnav-modes { margin-bottom: 6px; }
.pc-subnav-modes :deep(.segmented) { display: inline-flex; padding: 2px; border-radius: 8px; }
.pc-subnav-modes :deep(.seg) { flex: 0 0 auto; padding: 4px 12px; font-size: 12px; white-space: nowrap; }
```
**不动：** `<script>` 中所有 watch、`lazyPreset`、`PresetLoadError`、`presetLoadKey`、`formatModel/cliModel`、导入导出逻辑。

### 4.2 `components/provider/PresetList.vue`（预设页主体，两种格式共用）

**布局决策（任务书 §5-4）：** 卡片列表改为**分组列表**（一个容器，行间用分隔线，对标 macOS 设置的 inset grouped list）。不做网格：每条预设的徽章数量差别很大，网格会参差不齐；也不做表格：参数是稀疏的。现在每卡 14px 内边距加 10px 间距，改成行内 12px 内边距、0 间距后，同屏能多显示约 30% 的条目。

**删除：** `.preset-list`、`.preset-card*` 样式；`emptyDescription` computed；行级 `@click` 的 div 结构。

**新骨架：**
```vue
<template v-else>
  <div class="pc-toolbar"><!-- Chip 筛选 + 「+ 添加预设」保持 --></div>

  <ul v-if="filteredPresets.length > 0" class="preset-group">
    <li v-for="p in filteredPresets" :key="p.key" class="preset-row">
      <div class="preset-head">
        <button type="button" class="preset-open" :aria-label="`编辑预设 ${p.label || p.key}`" @click="handleEdit(p)">
          <span class="preset-name">{{ p.label || p.key }}</span>
        </button>
        <span v-if="p.provider" class="preset-prov">{{ p.provider }}</span>
        <button type="button" class="preset-delete" :aria-label="`删除预设 ${p.label || p.key}`" @click="handleDelete(p)">删除</button>
      </div>
      <div class="preset-badges"><!-- 徽章保持；CLI 徽章见下 --></div>
    </li>
  </ul>

  <EmptyState v-else icon="≡" :title="emptyTitle" :description="store.loadingPresets ? '正在加载预设列表…' : ''">
    <template #action>
      <AppButton v-if="isFiltered" variant="ghost" size="small" class="empty-action" @click="store.setPresetFilter('all')">显示全部</AppButton>
      <AppButton v-else-if="!store.loadingPresets" variant="primary" size="small" class="empty-action" @click="handleAdd">添加预设</AppButton>
    </template>
  </EmptyState>
</template>
```
- `isFiltered = store.presetFilter && store.presetFilter !== 'all'`；`emptyTitle` 在筛选态改为 `` `${store.presetFilter} 下暂无预设` ``，非筛选态保持 `` `暂无 ${engineLabel} 预设` ``。
- 「同步」徽章文字由 `CLI` 改为 `同步 Pi/OMP`，删除 `title` 属性（不再依赖 hover 才能看懂）。
- **整行可点、语义正确：** `.preset-row{position:relative}`，`.preset-open::after{content:'';position:absolute;inset:0}` 把点击区域撑满整行；`.preset-delete{position:relative;z-index:1}` 让删除按钮浮在上层。焦点框画在伪元素上：`.preset-open:focus-visible{outline:none}`、`.preset-open:focus-visible::after{outline:2px solid var(--accent);outline-offset:-2px;border-radius:inherit}`。这样行可以用 Tab 聚焦、Enter 编辑（现状 div 无法用键盘操作），也避免把交互元素嵌进 `role=button`。
- 样式：
  - `.preset-group{list-style:none;margin:0;padding:0;background:var(--card);border:1px solid var(--separator);border-radius:12px;overflow:hidden}`
  - `.preset-row{padding:12px 16px;display:flex;flex-direction:column;gap:8px;transition:background .15s}`、`.preset-row + .preset-row{border-top:1px solid var(--separator)}`、`.preset-row:hover{background:var(--control)}`
  - `.preset-head{display:flex;align-items:center;gap:10px}`；`.preset-open{flex:1 1 auto;min-width:0;text-align:left;background:none;border:none;padding:0;font:inherit;cursor:pointer}`；`.preset-name{font-size:14px;font-weight:600;color:var(--label);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;display:block}`
  - `.preset-delete:hover{color:var(--danger-strong);background:var(--danger-tint, rgba(255,59,48,.08))}`（没有 `--danger-tint` token，用回退值，不新增 token）
  - `.param.vision{color:var(--success-strong)}`
  - `.empty-action{margin-top:14px}`
- 不改：脚本中的加载、删除、弹窗逻辑。

### 4.3 `components/provider/PiAmagiConfig.vue`

**删除：** `.pac-header`（h2 + subtitle）；`.pac-path-row`；`.pac-json-valid`；所有 `<p class="pac-hint">`、`<p class="pac-subhint">`、`.pac-hint-inline` 长文；常量 `MCP_MODE_HINTS`；样式 `.pac-header/.pac-title/.pac-subtitle/.pac-toolbar/.pac-path-*/.pac-copy-btn/.pac-hint/.pac-subhint/.pac-json-valid/.pac-mcp-mode-row .pac-hint-inline`。
**改名：** `COMPACTION_MODE_HINTS` 改为 `COMPACTION_MODE_CAPTIONS`（短）和 `COMPACTION_MODE_ITEMS`（全文），内容见 §5。
**区块调整：** Profile 卡并入「角色模型」卡顶部，卡片顺序改为 角色模型 → 上下文压缩 → MCP 路由 → 并发限制；`expanded` 初值改为 `{ agents: true, compaction: true, mcp: false, concurrency: false }`。

**新骨架：**
```vue
<div class="pac-root"><!-- gap: 12px -->
  <LoadingState v-if="loading" message="加载配置中..." />
  <ErrorState v-else-if="error" title="加载失败" :message="error" :on-retry="initialLoad" />
  <template v-else>
    <EditorToolbar :path="configPath" path-label="配置文件"
                   :valid-label="mode === 'json' && !jsonError ? 'JSON 合法' : ''" @copy-path="copyPath">
      <template #actions>
        <Segmented v-model="modeModel" :options="MODE_OPTIONS" variant="pill" />
        <AppButton variant="primary" size="small" :disabled="saving" @click="handleSave">{{ saving ? '保存中...' : '保存配置' }}</AppButton>
      </template>
    </EditorToolbar>
    <StatusBanner v-if="jsonError" compact type="error" role="alert" class="pac-json-error" :message="jsonError" />
    <StatusBanner v-if="catalogError" compact type="warning" class="pac-catalog-warn" :message="`模型目录加载失败，下拉不可用：${catalogError}`" />

    <div v-if="mode === 'visual'" class="pac-visual">
      <!-- ① 角色模型 -->
      <ConfigCategoryCard title="角色模型" code="agents" category="agent" :badge="agentRows.length"
                          :expanded="expanded.agents" @toggle="expanded.agents = !expanded.agents">
        <div class="pac-field">
          <FieldLabel text="分层策略" code="profile" :items="PROFILE_ITEMS" />
          <Dropdown class="pac-profile-dd" :model-value="profileValue" :options="PROFILE_OPTIONS" @update:model-value="updateProfile" />
        </div>
        <div class="pac-field">
          <FieldLabel text="角色覆盖" code="agents.<角色>.model" :tip="AGENTS_TIP" />
          <div class="pac-agent-rows"><!-- 行结构保持 --></div>
          <div class="pac-actions"><!-- 添加角色按钮保持 --></div>
        </div>
      </ConfigCategoryCard>

      <!-- ② 上下文压缩（e2e 锚点卡：标题须含「上下文压缩」；卡内只允许 ModelSpecSelector 的 3 个 dropdown 与 1 个 input） -->
      <ConfigCategoryCard title="上下文压缩" code="compaction" category="model" meta="下次压缩生效"
                          :tip="COMPACTION_TIP" :badge="compactionMode === 'unset' ? null : 1"
                          :expanded="expanded.compaction" @toggle="expanded.compaction = !expanded.compaction">
        <div class="pac-field">
          <FieldLabel text="压缩模型" code="compaction.model" :items="COMPACTION_MODE_ITEMS" />
          <Segmented v-model="compactionModeModel" :options="COMPACTION_MODE_OPTIONS" variant="pill" class="pac-inline-seg" />
          <p class="pac-caption">{{ COMPACTION_MODE_CAPTIONS[compactionMode] }}</p>
          <template v-if="compactionMode === 'custom'">
            <ModelSpecSelector :model-value="compactionModel" :catalog="catalog" @update:model-value="updateCompactionModel" />
            <p class="pac-caption is-consequence">窗口不足或调用失败时取消本次压缩，历史保留</p>
          </template>
          <div class="pac-actions pac-actions-inline">
            <AppButton variant="ghost" size="small" :disabled="!lubanModel" @click="copyLubanCompactionModel">复制 luban 当前模型</AppButton>
            <span class="pac-caption" :class="{ mono: lubanModel }">{{ lubanModel ? `luban：${lubanModel}` : 'luban 未配置模型' }}</span>
          </div>
          <StatusBanner v-if="compactionError" compact type="error" role="alert" class="pac-json-error" :message="compactionError" />
        </div>
      </ConfigCategoryCard>

      <!-- ③ MCP 路由 -->
      <ConfigCategoryCard title="MCP 路由" code="mcp" category="mcp" :badge="mcpBadge"
                          :expanded="expanded.mcp" @toggle="expanded.mcp = !expanded.mcp">
        <div class="pac-field">
          <FieldLabel text="Leader 服务器" code="mcp.leader" effect="重启 pi 生效" :tip="MCP_TIPS.leader" :items="MCP_MODE_ITEMS.leader" />
          <Segmented v-model="mcpLeaderModeModel" :options="MCP_MODE_OPTIONS" variant="pill" class="pac-inline-seg" />
          <StringListEditor :model-value="mcpLeader" item-placeholder="服务器名（如 web-search-prime）" add-label="添加服务器"
                            :empty-text="MCP_EMPTY_TEXT[mcpLeaderUiMode]" mono @update:model-value="updateMcpLeader" />
        </div>
        <div class="pac-field">
          <FieldLabel text="默认服务器" code="mcp.default" effect="即时生效" :tip="MCP_TIPS.default" :items="MCP_MODE_ITEMS.default" />
          <Segmented v-model="mcpDefaultModeModel" :options="MCP_MODE_OPTIONS" variant="pill" class="pac-inline-seg" />
          <StringListEditor :model-value="mcpDefault" … :empty-text="MCP_EMPTY_TEXT[mcpDefaultUiMode]" @update:model-value="updateMcpDefault" />
        </div>
        <div class="pac-field">
          <FieldLabel text="角色附加服务器" code="mcp.agents" tip="为指定角色追加服务器，与默认服务器取并集。" />
          <div class="pac-mcp-agents"><!-- 行结构保持 --></div>
        </div>
      </ConfigCategoryCard>

      <!-- ④ 并发限制 -->
      <ConfigCategoryCard title="并发限制" code="concurrency" category="concurrency" :tip="CONCURRENCY_TIP"
                          :badge="concurrencyBadge" :expanded="expanded.concurrency" @toggle="expanded.concurrency = !expanded.concurrency">
        <div class="pac-field">
          <FieldLabel text="默认并发" code="default" :tip="CONCURRENCY_DEFAULT_TIP" />
          <TextInput :model-value="concurrencyDefault" type="number" placeholder="留空 = 内置默认 4" mono class="pac-limit-input" @update:model-value="updateConcurrencyDefault" />
        </div>
        <div class="pac-field">
          <FieldLabel text="服务商并发" code="providers" />
          <!-- 行 + 添加按钮保持 -->
        </div>
        <div class="pac-field">
          <FieldLabel text="模型并发" code="models" />
          <!-- 行 + 添加按钮保持 -->
        </div>
      </ConfigCategoryCard>
    </div>
    <div v-else class="pac-json"><textarea v-model="jsonContent" class="pac-json-editor" … /></div>
  </template>
</div>
```
**新增样式：**
- `.pac-root{gap:12px}`；`.pac-field{gap:6px}`；`.pac-field + .pac-field{margin-top:14px}`
- `.pac-caption{margin:0;font-size:12px;line-height:1.5;color:var(--tertiary)}`；`.pac-caption.is-consequence{color:var(--secondary)}`；`.pac-caption.mono{font-family:var(--mono);font-size:11.5px}`
- `.pac-inline-seg{align-self:flex-start}`、`.pac-inline-seg :deep(.segmented){display:inline-flex}`、`.pac-inline-seg :deep(.seg){flex:0 0 auto;padding:5px 14px;white-space:nowrap}`
- `.pac-actions-inline{align-items:center;margin-top:4px}`；`.pac-profile-dd{max-width:280px}`
- `.pac-json-error`、`.pac-catalog-warn` 原有的背景和内边距样式删除，交给 StatusBanner，只保留 class 作为测试锚点。
- 新增 import：`EditorToolbar`、`FieldLabel`、`StatusBanner`（均来自 `../ui/`）。

### 4.4 `components/provider/OmpGlobalConfig.vue`

**删除：** `.ogc-header`；`.ogc-path-row`；`.ogc-yaml-valid`；两处 `<p class="ogc-hint">`；对应样式。

**新骨架：**
```vue
<EditorToolbar :path="configPath" path-label="配置文件" effect="新会话生效"
               :valid-label="mode === 'source' && !yamlError ? 'YAML 合法' : ''" @copy-path="copyPath">
  <template #actions><Segmented v-model="modeModel" :options="MODE_OPTIONS" variant="pill" /><AppButton primary small>保存配置</AppButton></template>
</EditorToolbar>
<StatusBanner v-if="yamlError" compact type="error" role="alert" class="ogc-yaml-error" :message="yamlError" />
<StatusBanner v-if="catalogError" compact type="warning" class="ogc-catalog-warn" :message="`模型目录加载失败，下拉不可用：${catalogError}`" />
<div v-if="mode === 'visual'" class="ogc-visual">
  <ConfigCategoryCard title="角色模型" code="modelRoles" category="model" :tip="OMP_ROLES_TIP" :badge="roleRows.length" …>
    <div class="ogc-rows">…（保持）</div><div class="ogc-actions">…（保持）</div>
  </ConfigCategoryCard>
  <ConfigCategoryCard title="子代理覆盖" code="task.agentModelOverrides" category="agent" :tip="OMP_OVERRIDES_TIP" :badge="overrideRows.length" …>
    …（保持）
  </ConfigCategoryCard>
</div>
```
- 根容器 `.ogc-root` 的 gap 改为 12；子代理覆盖行中的「改为直接模型 / 改为 @role 引用」按钮保持不变。

### 4.5 `components/provider/OpenCodePresets.vue`

**删除：** 两处 `.oc-path-row`；默认卡里的 `oc-key: (本机)` 行；`.oc-card*` 卡片样式（改为分组列表行）；可视化空态的长描述。
**新增：** `useToast` 与 `copyPath()`（写法同其他编辑器，复制 `store.ocConfigPath`）；computed `searchActive = store.presetSearch.trim() !== ''`。
**行为调整（只增不减）：** 「本机默认配置」行在**未搜索时始终显示**（现状只在有用户预设时显示，没有预设时连默认项也一起消失，与「启动时若未指定预设即使用此项」的语义矛盾）。

**新骨架：**
```vue
<EditorToolbar :path="store.ocConfigPath" path-label="配置文件" @copy-path="copyPath">
  <template #leading><div class="pc-search">…搜索框保持…</div></template>
  <template #actions>
    <Segmented v-model="modeModel" :options="MODE_OPTIONS" variant="pill" />
    <AppButton variant="primary" size="small" @click="handleAdd">+ 添加预设</AppButton>
  </template>
</EditorToolbar>

<div v-if="mode === 'visual'" class="oc-view">
  <ul v-if="!searchActive || visualItems.length > 0" class="oc-group">
    <li v-if="!searchActive" class="oc-row is-default">
      <div class="oc-head"><span class="oc-name">本机默认配置</span><span class="oc-default-tag">默认</span></div>
      <div class="oc-desc">未选择预设时使用本机 ~/.config/opencode 配置</div>
    </li>
    <li v-for="item in visualItems" :key="item.key" class="oc-row">
      <div class="oc-head">
        <button type="button" class="oc-open" :aria-label="`编辑预设 ${item.name}`" @click="handleEdit(item)">
          <span class="oc-name">{{ item.name }}</span>
        </button>
        <code class="oc-key">{{ item.key }}</code>
      </div>
      <div v-if="item.desc" class="oc-desc">{{ item.desc }}</div>
      <div v-if="item.bindCount || item.provider || item.model" class="oc-meta">…标签保持…</div>
    </li>
  </ul>
  <EmptyState v-if="visualItems.length === 0" icon="≡"
              :title="store.loadingPresets ? '正在加载 OpenCode 配置...' : (searchActive ? '无匹配预设' : '暂无 OpenCode 预设')">
    <template #action>
      <AppButton v-if="searchActive" variant="ghost" size="small" class="empty-action" @click="searchModel = ''">清除搜索</AppButton>
      <AppButton v-else-if="!store.loadingPresets" variant="primary" size="small" class="empty-action" @click="handleAdd">添加预设</AppButton>
    </template>
  </EmptyState>
</div>
<div v-else class="oc-view">
  <pre v-if="store.ocConfigContent" class="oc-json" v-html="highlightedJson"></pre>
  <EmptyState v-else icon="≡" title="未读取到 OpenCode 配置" description="文件为空或读取失败" />
</div>
```
- 分组列表的样式与 §4.2 一致：`.oc-group` 对应 `.preset-group`，`.oc-row` 对应 `.preset-row`，`.oc-open` 对应 `.preset-open`（含 `::after` 整行点击和焦点框）。另外：`.oc-row.is-default{background:var(--sidebar)}`（不可点，无 hover）；`.oc-key{font-family:var(--mono);font-size:11px;color:var(--tertiary);flex:0 0 auto}`；`.oc-desc{font-size:12px;color:var(--secondary);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}`。
- 去掉原 `.pc-toolbar` 和 `.pc-actions` 包裹层，`.pc-search` 的样式保留。

### 4.6 `components/provider/OpenCodeGlobalConfig.vue`

**删除：** `.ocg-header`；`.ocg-path-row`；`.ocg-json-valid`；`.ocg-empty-hint` 整块（空配置 EmptyState）；`ocg-info-text` 段落；对应样式。

**新骨架：**
```vue
<EditorToolbar :path="configPath" path-label="配置文件" effect="即时生效"
               :valid-label="mode === 'json' && !jsonError && !saving ? 'JSON 合法' : ''" @copy-path="copyPath">
  <template #status><span v-if="mode === 'visual' && isConfigEmpty" class="ocg-note">空文件</span></template>
  <template #actions><Segmented …/><AppButton primary small>保存配置</AppButton></template>
</EditorToolbar>
<StatusBanner v-if="jsonError" compact type="error" role="alert" class="ocg-json-error" :message="jsonError" />
<div v-if="mode === 'visual'" class="ocg-cards">
  <ConfigCategoryCard v-if="hasTopLevelModel" title="顶层模型" code="model" category="model" :badge="1"
                      tip="OpenCode 通常在 agent 级配置模型（agent.<名称>.model），顶层 model 较少使用。" …>
    <TextInput :model-value="String(configData.model ?? '')" placeholder="未设置" @update:model-value="updateField('model', $event)" />
  </ConfigCategoryCard>
  <ConfigCategoryCard title="提供商"   code="provider"     category="provider"     …>…</ConfigCategoryCard>
  <ConfigCategoryCard title="Agent"    code="agent"        category="agent"        …>…</ConfigCategoryCard>
  <ConfigCategoryCard title="MCP 服务器" code="mcp"        category="mcp"          …>…</ConfigCategoryCard>
  <ConfigCategoryCard title="权限"     code="permission"   category="permission"   …>…</ConfigCategoryCard>
  <ConfigCategoryCard title="指令文件" code="instructions" category="instructions" …>…</ConfigCategoryCard>
  <ConfigCategoryCard title="插件"     code="plugin"       category="plugin"       …>…</ConfigCategoryCard>
  <ConfigCategoryCard title="实验特性" code="experimental" category="experimental" …>…</ConfigCategoryCard>
  <ConfigCategoryCard v-if="unknownKeys.length > 0" title="其他键" category="unknown" …>…</ConfigCategoryCard>
</div>
```
- `.ocg-note{font-size:12px;color:var(--tertiary)}`。各卡片内的子编辑器（ProviderMapEditor 等）保持不变。
- 「顶层模型」卡在 `hasTopLevelModel` 为 false 时不渲染。现状下这张卡也只有在键存在时才有输入框，所以编辑能力不受影响。

### 4.7 `components/provider/ProviderRegistryEditor.vue`（Pi/OMP 双引擎）

**删除：** `.pre-header`；`.pre-path-row`；`.pre-parse-valid`；「暂无其他字段」「暂无高级字段」两处空行；对应样式。

**新骨架（顶部）：**
```vue
<EditorToolbar :path="configPath" path-label="注册表文件" effect="重新进入后生效" :effect-tip="REGISTRY_EFFECT_TIP"
               :valid-label="mode === 'source' && !parseError ? (isPi ? 'JSON 合法' : 'YAML 合法') : ''" @copy-path="copyPath">
  <template #actions><Segmented v-model="modeModel" :options="modeOptions" variant="pill" /><AppButton primary small>保存配置</AppButton></template>
</EditorToolbar>
<StatusBanner v-if="parseError" compact type="error" role="alert" class="pre-parse-error"
              :message="`${parseError}，可视化不可用，请切换到 ${isPi ? 'JSON' : 'YAML'} 模式修复`" />
```
**提供商卡：**
```vue
<ConfigCategoryCard v-for="name in providerNames" :key="name" :title="name" category="provider" :badge="modelCount(name)"
                    :meta="name.startsWith('amagi-') ? '受管' : undefined" meta-tone="warning" …>
  <StatusBanner v-if="name.startsWith('amagi-')" compact type="warning" class="pre-managed-warn"
                message="由 CodeBox 提供商中心同步管理，手动修改可能在下次同步时被覆盖" />
  <div class="pre-fields pre-fields-grid">
    <div class="pre-field"><FieldLabel text="协议" code="api" /> Dropdown/TextInput（保持）</div>
    <div class="pre-field"><FieldLabel text="Base URL" code="baseUrl" /> TextInput placeholder="https://..."</div>
    <div class="pre-field"><FieldLabel text="API Key" code="apiKey" /> TextInput type=password placeholder="留空表示未设置"</div>
  </div>
  <div class="pre-unknown">
    <div class="pre-section-head">
      <span class="pre-unknown-title">其他字段</span>
      <div class="pre-actions">（+ 字符串字段 / + 布尔字段 / + 数字字段 / + 对象字段，保持）</div>
    </div>
    <div v-for="k in providerUnknownKeys(name)" …>（保持）</div>
  </div>
  <div class="pre-models">
    <div class="pre-models-head"><span class="pre-label">模型</span></div>
    <div v-for="(m, idx) in providerModels(name)" class="pre-model">
      <!-- model-head 保持（id 输入 / id 为空警示 / 推理开关 / 删除） -->
      <div class="pre-model-grid">
        <FieldLabel text="显示名" code="name" /> …  <FieldLabel text="上下文窗口" code="contextWindow" /> …  <FieldLabel text="最大输出" code="maxTokens" /> …
      </div>
      <div class="pre-unknown">
        <div class="pre-section-head"><span class="pre-unknown-title">高级字段</span><div class="pre-actions">（6 个添加按钮保持）</div></div>
        thinkingLevelMap → 键名行文字「级别映射」+ code「thinkingLevelMap」
        thinking         → 「思考模式」+ code「thinking」
        input            → 「输入能力」+ code「input」
        cost             → 「费用」+ code「cost」+ 尾注 <span class="pre-unit">USD / 百万 token</span>
        （其余未知字段保持）
      </div>
    </div>
    <div class="pre-actions"><AppButton ghost small>+ 添加模型</AppButton></div>
  </div>
  <div class="pre-actions pre-provider-actions">
    <AppButton variant="danger" size="small" @click="removeProvider(name)">删除提供商 {{ name }}</AppButton>
  </div>
</ConfigCategoryCard>
```
- 空态：`<EmptyState title="暂无提供商" />`（不传 description），外层的 `.pre-empty` 虚线框保留。
- 样式：`.pre-fields-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:10px}`；`.pre-section-head{display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap}`；`.pre-unit{font-size:11px;color:var(--tertiary)}`；高级字段键名行（`.pre-unknown-key`）的格式改为「中文名 + code」，code 沿用 `.fl-code` 的规格（mono 10.5px，`--tertiary`）。
- 双引擎差异只出现在 `isPi` 分支（文件格式和标签文字），模板不再分叉。

### 4.8 `components/provider/AuthConfigEditor.vue`

**删除：** `.ace-header`；`.ace-path-row`；`.ace-parse-valid`；`.ace-oauth-note` 文本；对应样式。

**新骨架：**
```vue
<EditorToolbar :path="configPath" path-label="凭据文件"
               :valid-label="mode === 'source' && !parseError ? 'JSON 合法' : ''" @copy-path="copyPath">
  <template #actions><Segmented …/><AppButton primary small>保存配置</AppButton></template>
</EditorToolbar>
<StatusBanner v-if="parseError" compact type="error" role="alert" class="ace-parse-error"
              :message="`${parseError}，可视化不可用，请切换到 JSON 模式修复`" />
<div v-if="mode === 'visual'" class="ace-visual">
  <div v-if="entries.length === 0" class="ace-empty">
    <EmptyState title="暂无凭据" description="可在下方添加 API Key；OAuth 需通过 pi CLI 登录" />
  </div>
  <div v-for="row in entries" class="ace-entry">
    <div class="ace-entry-head">
      <TextInput …name… /> <Badge :type="row.kind === 'oauth' ? 'source' : 'tag'" :text="row.typeLabel" />
      <template v-if="row.kind === 'oauth'">
        <span class="ace-managed">🔒 由 pi CLI 管理</span>
        <InfoTip label="OAuth 凭据" :text="OAUTH_TIP" />
      </template>
      <AppButton variant="icon" …>×</AppButton>
    </div>
    <!-- api_key 区块保持，placeholder 保持「sk-...（留空将移除密钥字段）」 -->
    <div v-if="row.kind === 'oauth'" class="ace-oauth">
      <Badge type="ver" :text="`已登录${row.accountId ? ' · ' + row.accountId : ''}`" />
      <span v-if="row.expiresText" class="ace-oauth-expires">过期时间：{{ row.expiresText }}</span>
    </div>
  </div>
  <div class="ace-add">
    <div class="ace-add-title">添加 API Key</div>
    <div class="ace-add-row"><TextInput v-model="newName" placeholder="提供商名（与注册表一致）" …/> …</div>
    <div v-if="unauthedProviders.length" class="ace-suggest"><span class="ace-suggest-label">未认证：</span> …</div>
  </div>
</div>
```
- `.ace-managed{font-size:11px;font-weight:500;color:var(--secondary);background:var(--control);border-radius:999px;padding:1px 7px;white-space:nowrap}`。🔒 换成 12px 内联 SVG 锁图标（`aria-hidden="true"`，`icons.ts` 规定禁用 emoji），文字保留。
- `.ace-entry-head` 的右侧删除按钮加 `margin-left:auto`，让管理标签贴在类型徽章后面。

---

## 5. 说明文字处置表

### 5.1 承载形态代号
L0 删除｜L1 内化（标签/选项/code）｜L2 常显微文案｜L3 生效标签｜L4 InfoTip｜L5 内联警示｜L6 空态/placeholder（判据见 §2.3）

### 5.2 逐条处置（覆盖任务书 §3 全部条目，另含 3 条范围内文件的同类文字）

| ID | 来源 | 原文（摘要） | 处置 | 处置后文案 |
|---|---|---|---|---|
| T01 | 3.1 PageHead | 统一管理服务提供商与可跨 CLI 复用的公共预设 | 改写（三个 Tab 共用，现文案漏掉了 CLI 独立配置）。保留描述行是为了和其他页面一致（`EnvCheckView`、`ExtensionsView` 等都有一行描述） | 管理服务提供商、公共预设与各 CLI 独立配置 |
| T02 | 3.1 预设 zone-label | 格式预设 · 跨 CLI 复用的公共协议格式 | L0 删除整行；把「对谁有用」换成更具体的信息，放在 subnav 右侧作 L2 | 用于 Claude Code ／ 用于 Codex · Pi · OMP |
| T03 | 3.1 CLI zone-label | CLI 专属配置 · 各 CLI 专属配置文件，互不共享 | L0（「独立」已经在一级 Tab 名里） | — |
| T04 | 3.1 二三级 Tab | 两行分别渲染 | 合并为一行 subnav（§2.2 N2），文案不变 | — |
| T05 | 3.2 空态描述 | 点击右上角「添加预设」创建第一个启动配置 | L6：描述删除，改为动作按钮；筛选态给出「显示全部」 | 标题「暂无 {格式} 预设」+［添加预设］；「{provider} 下暂无预设」+［显示全部］ |
| T06 | 3.2 徽章 CLI | `CLI`（title：已标记同步到 CLI 独立配置（Pi/OMP）） | L1：语义写进徽章文字，去掉 hover 依赖 | 同步 Pi/OMP |
| T07 | 3.3 Pi h2 | Pi 配置（amagi-pi） | L0（导航即标题） | — |
| T08 | 3.3 Pi subtitle | 编辑全局 amagi.json…各项生效时间见卡片说明 | 拆开处理：文件名交给路径胶囊；区块名交给卡片标题；「模型下拉来自 pi 模型目录」放进 L4（T15）；「生效时间」由各处 L3 标签承载；其余 L0 | — |
| T09 | 3.3 Profile hint | tiered：按 leader/fast/work/expert…；inherit：全部继承… | L1（选项 label 已有「分层默认 / 继承 pi 默认」）+ L4 术语对照；卡片并入「角色模型」 | items：tiered — 按 leader / fast / work / expert 分层套用 profiles/tiered.json；inherit — 全部继承 pi 默认模型 |
| T10 | 3.3 压缩卡 hint | Leader 与 JSON/RPC 子 Agent 共用压缩模型… | L4（卡片 tip 第 1 段） | Leader 与 JSON/RPC 子 Agent 共用压缩模型，用于 /compact、自动压缩与上下文溢出恢复。 |
| T11 | 3.3 COMPACTION_MODE_HINTS ×3 | unset/custom/native 长句 | L2 只显示当前态短句，全文放 L4 术语对照 | 短句：未指定「删除该键，由 pi 使用当前会话模型」／统一模型「所有 Agent 用此模型生成压缩摘要」／pi 原生模型「写入 null，各会话使用当前模型」。items：未指定 — 删除 compaction.model；全局未指定时 pi 使用当前会话模型，项目仍可单独指定；统一模型 — 写入 provider/model[:thinking]，所有 Agent 用该模型生成压缩摘要；pi 原生模型 — 写入显式 null，使用各会话当前模型；项目设为 null 可禁用全局统一模型 |
| T12 | 3.3 长段 1 | 项目 amagi.json 优先…保存后下次压缩读取…重新派发子 Agent…认证由 pi 管理 | 生效时机 → L3 卡片 meta「下次压缩生效」；其余 → L4 卡片 tip 第 2、3 段 | 项目 amagi.json 优先于此全局配置；子 Agent 沿用派发者的配置目录。／保存后下次压缩读取；新增服务商环境凭据引用后需重新派发子 Agent。模型认证由 pi 管理。 |
| T13 | 3.3 长段 2 | 请选上下文窗口足够的已注册模型…取消本次压缩并保留历史 | 后果语义：custom 态 L2 常显短句 + L4 卡片 tip 第 4 段写全文 | 常显：窗口不足或调用失败时取消本次压缩，历史保留／tip：请选上下文窗口足够的已注册模型：配置、认证、摘要失败或窗口不足都会取消本次压缩并保留历史。 |
| T14 | 3.3 luban 行内 | {model}（复制后独立保存）／请先配置 luban 的角色模型 | L2 精简；「独立保存」放进 L4 卡片 tip 第 5 段 | luban：{model}／luban 未配置模型；tip：「复制 luban 当前模型」写入独立副本，之后修改 luban 不会联动。 |
| T15 | 3.3 角色模型 hint | 按角色覆盖模型，优先级高于 profile。角色名与 agents/*.md 对应 | L4，挂在「角色覆盖」字段 | 按角色覆盖模型，优先于分层策略。／角色名对应 amagi-pi agents/*.md；模型候选来自 pi 模型目录。 |
| T16 | 3.3 MCP leader label | Leader 服务器（mcp.leader，主进程加载名单） | L1：键名改为 code；「主进程加载名单」放进 L4 | 「Leader 服务器」 + `mcp.leader` |
| T17 | 3.3 MCP default label | 默认服务器（mcp.default，所有子 Agent 基线） | 同上 | 「默认服务器」 + `mcp.default` |
| T18 | 3.3 MCP_MODE_HINTS ×6 | 各三态长句（leader/default） | 删除常量和行内显示；全文放 L4 术语对照；当前态语义交给 T21 空态文本（全部/不加载态下名单必然为空，所以一定可见） | items（leader）：全部 — 未设置该键，加载全部（保存时删除 mcp.leader）；白名单 — 仅加载列表内服务器，* 表示全部；不加载 — 保存为空列表 []，Leader 不加载任何 MCP。items（default）：全部 — 未设置该键，子 Agent 默认加载全部（保存时删除 mcp.default）；白名单 — 仅加载列表内服务器，作为子 Agent 基线，* 表示全部；不加载 — 保存为空列表 []，子 Agent 默认不加载（角色附加服务器仍生效） |
| T19 | 3.3 leader 尾注 | Leader 主进程加载哪些 MCP server（v2.8.2+）：装配时读一次，改动需重启 pi… | L3 字段标签「重启 pi 生效」+ L4 | tip：Leader 主进程加载的 MCP 服务器名单（amagi-pi v2.8.2+）。／装配时读取一次，改动需重启 pi 生效；与子 Agent 路由相互独立。 |
| T20 | 3.3 default 尾注 | 与 mcp.agents 取并集；每次派发现读盘，改动即时生效 | L3「即时生效」+ L4 | tip：所有子 Agent 的基线名单，与 mcp.agents 各角色名单取并集。／每次派发时读盘，改动即时生效。 |
| T21 | 3.3 MCP_EMPTY_TEXT ×3 | 三条空态引导 | L6 保留（三态语义的常显承载），措辞微调 | all：未设置 = 加载全部；添加服务器即切换为白名单／none：[] = 不加载任何 MCP；添加服务器即切换为白名单／list：名单为空，保存后等同「不加载」 |
| T22 | 3.3 角色附加服务器 | 角色附加服务器（mcp.agents） | L1 + L4 | 「角色附加服务器」+ `mcp.agents`；tip：为指定角色追加服务器，与默认服务器取并集。 |
| T23 | 3.3 并发卡 hint | 按 provider/model 分池…三项全部可选，未匹配时回退默认容量 | L4（卡片 tip）；「三项全部可选」L0 | 按服务商 / 模型分池限制并发请求数；未匹配时使用默认并发。／优先级：模型 > 服务商 > 默认。 |
| T24 | 3.3 default 行内长文 | 未单独指定…正整数；应用默认 10，清空保存则移除键、回落内置默认 4 | 硬约束语义：L6 placeholder（清空时立刻可见）+ L4 写全文 | placeholder：留空 = 内置默认 4；tip：未单独指定服务商或模型时的并发池大小，正整数。／应用预填 10；清空后保存将移除该键，回落 amagi-pi 内置默认 4。 |
| T25 | 3.3 providers subhint | 按服务商限制并发容量，覆盖默认并发数 | 优先级已在 T23 卡片 tip 中说明 → L0 | 「服务商并发」+ `providers` |
| T26 | 3.3 models subhint | 按精确 provider/model 限制…优先级高于服务商限制 | 同上 → L0 | 「模型并发」+ `models` |
| T27 | 3.3 路径行 | 配置文件：{path} [复制路径] | 改为 EditorToolbar 路径胶囊（点击复制） | 胶囊 aria-label：复制配置文件路径：{path} |
| T28 | 3.3 合法/错误条 | JSON 合法／JSON 格式错误… | 合法 → 工具栏状态（✓ JSON 合法）；错误 → L5 compact + role=alert | 文案不变 |
| T29 | 3.3 目录警告 | 模型目录加载失败，下拉不可用：{err} | L5 compact warning（保留 `.pac-catalog-warn`） | 文案不变 |
| T30 | 3.4 OMP h2 | OMP 配置（oh-my-pi） | L0 | — |
| T31 | 3.4 OMP subtitle | 编辑 ~/.omp/agent/config.yml：modelRoles…下拉来自 models.yml…新会话生效 | 文件 → 胶囊；键名 → 卡片 code；生效 → L3 工具栏「新会话生效」；下拉来源 → L4（T32） | — |
| T32 | 3.4 modelRoles hint | 每个 worker 角色绑定一个 provider/model:level spec，供 task 子代理通过 @role 引用 | L4 卡片 tip | 每个 worker 角色绑定一个 provider/model:level，供 task 子代理以 @role 引用。／模型候选来自「模型提供商」中的 models.yml。 |
| T33 | 3.4 overrides hint | 将子代理映射到 modelRoles 角色（@role 引用）或直接绑定模型 spec | L4 卡片 tip | 将子代理映射到 modelRoles 角色（@role 引用），或直接绑定模型。 |
| T34 | 3.4 路径/合法/错误/警告 | 同 Pi | 同 T27–T29 | YAML 合法 |
| T35 | 3.5 默认卡描述 | 读取本地 ~/.config/opencode 配置，未启用受管预设。启动时若未指定预设即使用此项。 | L2 单行压缩；删除伪 key 行「key: (本机)」；默认行常驻 | 未选择预设时使用本机 ~/.config/opencode 配置 |
| T36 | 3.5 空态描述 | 在 config.json 的 preset 段中定义预设，或点击右上角「添加预设」 | L6：描述删除，改为动作按钮；搜索无结果给「清除搜索」 | 暂无 OpenCode 预设 +［添加预设］；无匹配预设 +［清除搜索］ |
| T37 | 3.5 路径行 ×2 | 配置文件：{path}（两种模式底部各一份） | 改为胶囊（只渲染一次，新增复制） | — |
| T38 | 3.5 JSON 空态 | config.json 内容为空或加载失败 | L6 精简 | 文件为空或读取失败 |
| T39 | 3.5 工具栏 | 搜索 + 双模式 + 添加 | 收进 EditorToolbar：leading 放搜索，actions 放双模式和添加 | — |
| T40 | 3.6 h2 | OpenCode 全局配置 | L0 | — |
| T41 | 3.6 subtitle | 编辑全局 opencode.json 配置文件。修改后保存立即生效。 | 文件 → 胶囊；生效 → L3 工具栏 | 即时生效 |
| T42 | 3.6 ocg-empty-hint | 全局配置为空／当前 opencode.json 暂无可视化配置项，可切换到 JSON 模式直接编辑。 | L0 大块空态（下方分类卡本身就能编辑）；保留一个状态词 | 工具栏状态：空文件 |
| T43 | 3.6（同文件）Model 卡说明 | OpenCode 模型在 agent 级配置…顶层通常无 model 字段 | 卡片只在键存在时渲染；说明放 L4 | OpenCode 通常在 agent 级配置模型（agent.<名称>.model），顶层 model 较少使用。 |
| T44 | 3.6（同文件）卡片标题 | Provider / Agent / MCP Servers / Permission / Instructions / Plugin / Experimental / 其他键（兜底 JSON） | L1：中文标题 + code 键名（和 Pi/OMP 统一） | 提供商·Agent·MCP 服务器·权限·指令文件·插件·实验特性·其他键 |
| T45 | 3.7 h2 | Pi 模型提供商（models.json）／OMP…（models.yml） | L0（三级 Tab「模型提供商」+ 胶囊文件名） | — |
| T46 | 3.7 subtitle | 编辑模型注册表…Agent 配置中的下拉即来自此文件，保存后重新进入配置页生效 | 生效 → L3 + L4；其余 L0 | 标签：重新进入后生效；tip：Agent 配置的 provider / model 下拉读取此文件；保存后重新进入 Agent 配置页即可看到更新。 |
| T47 | 3.7 amagi-* 警告 | amagi-* 提供商由 CodeBox 提供商中心同步管理，手工修改可能在下次同步时被覆盖。 | **保留常显**：L5 compact warning；另在卡片头加 meta「受管」（警示色，折叠时也可见） | 由 CodeBox 提供商中心同步管理，手动修改可能在下次同步时被覆盖 |
| T48 | 3.7（同文件）字段括注 | api（协议类型）/apiKey（留空表示未设置）/显示名（name）/上下文窗口（contextWindow）/最大输出（maxTokens）/cost（USD / 百万 token）等 | L1 code 化；「留空表示未设置」改为 L6 placeholder；费用单位留作尾注 | 协议 `api`、Base URL `baseUrl`、API Key `apiKey`（placeholder：留空表示未设置）、显示名 `name`、上下文窗口 `contextWindow`、最大输出 `maxTokens`、级别映射 `thinkingLevelMap`、思考模式 `thinking`、输入能力 `input`、费用 `cost`（USD / 百万 token） |
| T49 | 3.7（同文件）空态 | 注册表为空／尚无提供商，添加一个开始配置模型。 | L6 精简（紧接其下就是添加行） | 暂无提供商 |
| T50 | 3.7（同文件）空行 | 暂无其他字段／暂无高级字段 | L0 | — |
| T51 | 3.7（同文件）解析错误后缀 | （可视化不可用，请切到源码模式修复） | L5，括号去掉，模式名和按钮对齐 | ，可视化不可用，请切换到 JSON/YAML 模式修复 |
| T52 | 3.8 h2 | Pi 认证登录（auth.json） | L0 | — |
| T53 | 3.8 subtitle | API Key 可在此直接填写；OAuth 凭据由 pi CLI 登录产生，此处仅展示状态。Agent 配置下拉中以 ✓ 标注已认证… | 「API Key 直接填写」L0（输入框已说明）；OAuth 只读 → 每个 OAuth 行加常显标签 + L4；「✓ 标注」L0（属于另一页面的教学内容） | 标签：由 pi CLI 管理；tip：OAuth 凭据由 pi CLI 登录产生，此处只读；access / refresh 令牌在保存时原样保留。 |
| T54 | 3.8 空态 | 暂无认证条目／为提供商添加 API Key，或通过 pi CLI 登录 OAuth 提供商。 | L6 精简，保留 OAuth 途径（没有 OAuth 条目时，这是唯一的承载位置） | 暂无凭据／可在下方添加 API Key；OAuth 需通过 pi CLI 登录 |
| T55 | 3.8（同文件）OAuth 行注 | OAuth 凭据由 pi CLI 管理，access/refresh 令牌保存时原样保留。 | 合并进 T53 的标签 + tip | 同 T53 |
| T56 | 3.8（同文件）路径/添加区 | 凭据文件：／添加 API Key 认证／提供商名（与 models.json 中一致）／注册表中未认证的提供商： | 胶囊 + 文案精简 | 胶囊 aria：复制凭据文件路径：…；添加 API Key；提供商名（与注册表一致）；未认证： |
| T57 | 3.8（同文件）解析错误后缀 | 同 T51 | 同 T51 | ，可视化不可用，请切换到 JSON 模式修复 |

### 5.3 §6 硬约束语义的承载核对

| 硬约束语义 | 常显承载（不需要任何交互） | 键盘可达的完整承载 |
|---|---|---|
| 生效时机：重启生效 | `mcp.leader` 字段标签「重启 pi 生效」（T19） | 该字段 InfoTip |
| 生效时机：新会话生效 | OMP 工具栏「新会话生效」（T31） | — |
| 生效时机：即时生效 | `mcp.default` 字段标签（T20）；OpenCode 全局工具栏（T41） | `mcp.default` InfoTip |
| 生效时机：下次压缩读取 | 上下文压缩卡 meta「下次压缩生效」（T12），折叠时也可见 | 卡片 InfoTip |
| 生效时机：注册表重新进入后生效 | 注册表工具栏标签（T46） | 工具栏 InfoTip |
| amagi-* 受管覆盖警告 | 卡片头 meta「受管」+ 卡内 warning 横幅（T47） | — |
| MCP 三态 + 空列表即不加载 | Segmented 三个选项名；名单空态文本（三态都可见，T21） | 字段 InfoTip 三态对照（T18） |
| 并发 default 清空回落内置 4 | 输入框清空时 placeholder「留空 = 内置默认 4」（T24） | 字段 InfoTip（含「移除键」「应用预填 10」） |
| OAuth 只读、由 pi CLI 管理 | 每个 OAuth 行标签「由 pi CLI 管理」；空态文案（T53/T54） | 行 InfoTip |
| 压缩窗口不足取消压缩 | custom 态常显后果句（T13） | 卡片 InfoTip 第 4 段 |

**生效标签的证据边界：** 只使用原文明确写出的生效时机。角色模型、分层策略、并发、凭据原文没有说明生效时机，**不加标签，也不能自行补写**（见 §8 未知项 U1）。

---

## 6. 新基件规格

### 6.1 `components/ui/InfoTip.vue`（toggletip 模式）

**Props**

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `label` | `string` | 必填 | 主题名，用于按钮 `aria-label="说明：{label}"` |
| `text` | `string \| string[]` | — | 段落，数组时每项一段 |
| `items` | `{ term: string; desc: string }[]` | — | 术语对照（用于三态等），渲染为 `<dl>` |
| `placement` | `'bottom' \| 'top'` | `'bottom'` | 首选方向，空间不足时自动翻转 |

另有默认 slot，供富内容使用（本轮不用）。

**DOM**
```html
<span class="infotip">
  <button ref="anchor" type="button" class="infotip-btn" :class="{ open }"
          :aria-label="`说明：${label}`" :aria-expanded="open" :aria-describedby="popId"
          @click="togglePinned" @mouseenter="hoverIn" @mouseleave="hoverOut" @blur="onBlur">
    <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
      <circle cx="8" cy="8" r="6.5"/><line x1="8" y1="7.2" x2="8" y2="11.2" stroke-linecap="round"/>
      <circle cx="8" cy="4.9" r=".85" fill="currentColor" stroke="none"/>
    </svg>
  </button>
  <Teleport to="body">
    <div v-show="open" :id="popId" ref="pop" class="infotip-pop" role="tooltip" :style="popStyle"
         @mouseenter="cancelClose" @mouseleave="hoverOut">
      <p v-for="(t, i) in paragraphs" :key="i">{{ t }}</p>
      <dl v-if="items?.length"><template v-for="it in items" :key="it.term"><dt>{{ it.term }}</dt><dd>{{ it.desc }}</dd></template></dl>
      <slot />
    </div>
  </Teleport>
</span>
```
- `popId`：模块级计数器生成 `infotip-${++seq}`。项目 `vue ^3.2.37`，不能依赖 3.5 的 `useId`。
- 用 `v-show` 而不是 `v-if`：这样 `aria-describedby` 指向的节点始终存在，屏幕阅读器在按钮获得焦点时就能读到说明。浮层必须 Teleport 到 body，因为 `.config-cat-card` 设置了 `overflow:hidden`，会把浮层裁掉。

**交互状态机**

| 触发 | 行为 |
|---|---|
| 指针进入按钮 | 250ms 后以 `hover` 来源打开（已 pinned 时忽略） |
| 指针离开按钮或浮层 | 来源为 `hover` 时 120ms 后关闭；进入浮层会取消关闭 |
| 点击 / Enter / Space（原生 button） | 已以 pinned 打开 → 关闭；否则以 `pinned` 打开 |
| Esc（打开期间在 document 上监听 keydown，capture） | 关闭，焦点回到按钮 |
| 按钮外的 pointerdown（capture，排除按钮和浮层自身） | 关闭 |
| 按钮 blur | pinned 来源时关闭（浮层内容不可聚焦，不存在把焦点移进浮层的情况） |
| window `resize` / `scroll`（capture） | 关闭（避免浮层和锚点错位） |
| 组件卸载 | 移除全部监听（对齐 `SessionQuotaStrip.vue` 的写法） |

只聚焦不自动打开。键盘用户聚焦时先听到 aria-label 和 describedby 的说明文本，按 Enter 才展开浮层。这满足 §6「不能只靠 hover」的要求。

**定位：** 打开后在 `nextTick` 中测量。`left = clamp(rect.left + rect.width/2 - popW/2, 8, innerWidth - popW - 8)`；默认 `top = rect.bottom + 6`；如果 `top + popH > innerHeight - 8`，改为 `rect.top - 6 - popH`（`placement='top'` 时反过来）。使用 `position: fixed`。

**样式**
```css
.infotip { display: inline-flex; vertical-align: middle; }
.infotip-btn { width: 18px; height: 18px; padding: 0; border: none; border-radius: 50%;
  background: transparent; color: var(--tertiary); display: inline-flex; align-items: center;
  justify-content: center; cursor: pointer; flex: 0 0 auto; }
.infotip-btn:hover, .infotip-btn.open { color: var(--accent); }
.infotip-btn:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; color: var(--accent); }
.infotip-pop { position: fixed; z-index: 2000; min-width: 160px; max-width: 280px;
  padding: 10px 12px; background: var(--popover); border: 1px solid var(--separator);
  border-radius: var(--radius-md); box-shadow: var(--shadow-pop);
  font-size: 12px; line-height: 1.55; color: var(--label); }
.infotip-pop p { margin: 0; } .infotip-pop p + p { margin-top: 6px; }
.infotip-pop dl { margin: 6px 0 0; display: grid; grid-template-columns: auto 1fr; gap: 4px 10px; }
.infotip-pop dl:first-child { margin-top: 0; }
.infotip-pop dt { font-weight: 600; white-space: nowrap; }
.infotip-pop dd { margin: 0; color: var(--secondary); }
```
- z-index 2000 与 Dropdown 菜单同级：高于 Dialog（1000），低于 GitPanel（3000）和 Toast（9999）。
- 深浅色：应用目前只有浅色 token（`body.dark-theme` 没有任何样式覆盖），所以浮层完全使用 token。以后加深色主题时，只需在深色 token 集里定义 `--popover / --shadow-pop`，组件不用改。皮肤模式下 `--popover` 不参与混色，浮层保持不透明。
- 不加入场动画（降低实现复杂度，也不需要处理 reduced-motion）。

### 6.2 `FieldLabel`、`EditorToolbar`、`StatusBanner compact`、`EmptyState` 可选描述、`ConfigCategoryCard` 扩展
规格见 §4.0.2–4.0.6。

---

## 7. 验收清单

### 7.1 构建与测试（luoshen 自验，记录命令、cwd 和退出码）

| # | 验收 | 命令（cwd = 仓库根） | 期望 |
|---|---|---|---|
| V1 | 类型门 + 构建 | `npm --prefix frontend run build` | 退出码 0 |
| V2 | 4 个逻辑测试不受影响 | `npm --prefix frontend run test -- src/__tests__/components/provider` | 4 个文件全部通过。这些测试只 import `mcpListMode.ts / piCompaction.ts / piConcurrency.ts / useModelCatalog.ts` 的纯函数，本设计**不修改**这 4 个文件，**测试无需同步调整** |
| V3 | Pi 压缩 e2e（L2，强烈建议跑） | `npx playwright test -c e2e/pi-amagi-config.playwright.config.ts` | 6 个用例全部通过 |

### 7.2 结构验收（diting 可以静态核查）

| # | 验收 | 核查方式 |
|---|---|---|
| S1 | 编辑器 h2/subtitle 和 hint 段落全部移除（P1/P3） | 在 `frontend/src` 中 `rg "pac-hint\|pac-subhint\|pac-subtitle\|pac-title\|ogc-hint\|ogc-subtitle\|ocg-subtitle\|ocg-empty-hint\|ocg-info-text\|pre-subtitle\|ace-subtitle\|ace-oauth-note\|pc-zone-label\|MCP_MODE_HINTS\|COMPACTION_MODE_HINTS"` 结果为 0 |
| S2 | 5 个编辑器 + OpenCodePresets 都使用 EditorToolbar，且不再有 `*-path-row` | `rg "<EditorToolbar" frontend/src/components/provider` 命中 6 个文件；`rg "path-row"` 为 0 |
| S3 | 二三级导航合并为一行 | ProviderCenterView 模板中两个面板各只有 1 个 `.pc-subnav`，三级 Segmented 在 `.pc-subnav` 内部 |
| S4 | §5.2 的 T01–T57 逐条落地 | 按表比对处置后文案 |
| S5 | §5.3 每项硬约束语义都有常显承载和键盘承载 | 按表逐项检查 |
| S6 | 行为不回退 | `lazyPreset`、6 处 `:key="presetLoadKey"`、`engine="pi"/"omp"`、`watch(mainTab)`、StatusBanner/RemoteScopeBanner、`AgentProfileQuickSwitch` 与导入导出按钮、各 `handleSave` 函数体与 api import 都保持不变（diff 中这些部分无改动） |
| S7 | 只改允许改的范围 | diff 只涉及 §4 列出的文件和 3 个新 ui 基件；不碰 `wailsjs/**`、`src/api/**`、`stores/**`、其他视图和 Go |
| S8 | 不引入新依赖 | `frontend/package.json` 无变化 |

### 7.3 e2e 选择器与名称契约（来自 `e2e/pi-amagi-config.spec.ts`，改动后必须仍成立）

- `.config-cat-card` 中存在一个名称匹配 `/上下文压缩/` 的按钮。卡片标题保留「上下文压缩」。
- 该卡片内有 Segmented 按钮「未指定 / 统一模型 / pi 原生模型」（`aria-pressed`），以及按钮「复制 luban 当前模型」。
- 该卡片内的 `.dropdown-trigger` 第 0 个和第 2 个属于 ModelSpecSelector：**不能在卡片内 ModelSpecSelector 之前加入任何 Dropdown**。
- catalog-error 场景下，该卡片内 `locator('input')` 必须只有 1 个：**卡片内不能新增 input**。
- 卡片内 `getByRole('alert')` 只来自 `compactionError`：**InfoTip 浮层用 `role=tooltip`，而且已 Teleport 出卡片**。
- 页面级的「保存配置」「JSON」「可视化」按钮名必须唯一且精确。InfoTip 的 aria-label 都以「说明：」开头，路径胶囊以「复制」开头，不会冲突。
- `.pac-json-editor`、`.mss-spec`、`.pac-catalog-warn`（文本包含原始错误）保留；toast 文案「配置已保存」保留。

### 7.4 视觉与交互验收（luoshen 截图自看，视觉裁决归洛神）

| # | 验收 | 方法 |
|---|---|---|
| X1 | 首个内容卡片上方 chrome ≤ 3 行（P2） | 截图：预设页 ×2 格式、CLI 页 7 个子视图（Pi×3、OMP×2、OpenCode×2），窗口 1280×800 |
| X2 | 最小窗口 800×600 下 subnav、工具栏换行后不重叠、不溢出 | 同上 9 个视图各截一张 |
| X3 | 可视化默认展开态下，常显说明块 ≤ 3 个/编辑器，每块 ≤ 24 字（P4） | 截图人工计数（Pi 默认：压缩当前态句、luban 行，custom 态再加后果句） |
| X4 | InfoTip 键盘可达 | Tab 聚焦到 ⓘ → Enter 打开 → Esc 关闭并回焦；在卡片头的 ⓘ 上按 Enter 不会切换卡片折叠 |
| X5 | InfoTip 不被裁切 | 在折叠卡片头和卡片底部字段处打开，浮层完整；视口底部自动向上翻转 |
| X6 | 预设行键盘编辑 | Tab 到行 → Enter 打开编辑弹窗；删除按钮单独可聚焦，不会误触发编辑 |
| X7 | 皮肤模式下浮层不透明 | 开启皮肤后打开 InfoTip |

### 7.5 子视图覆盖矩阵（任务书 §9）

| 子视图 | 布局指令 |
|---|---|
| 预设 · Anthropic 格式 | §4.1 + §4.2 |
| 预设 · OpenAI 格式 | §4.1 + §4.2（subnav 说明文字不同） |
| Pi · Agent 配置 | §4.3 |
| Pi · 模型提供商 | §4.7（engine=pi） |
| Pi · 认证登录 | §4.8 |
| OMP · Agent 配置 | §4.4 |
| OMP · 模型提供商 | §4.7（engine=omp） |
| OpenCode · 预设管理 | §4.5 |
| OpenCode · 全局配置 | §4.6 |

### 7.6 建议的实现切片（每片都能单独构建）
S1 基件（§4.0）→ S2 容器与预设（§4.1、§4.2）→ S3 Pi Agent（§4.3，跑 V3）→ S4 OMP / 注册表 / 认证（§4.4、§4.7、§4.8）→ S5 OpenCode ×2（§4.5、§4.6）→ 全量 V1/V2/V3 + X1–X7。

---

## 8. 风险、未知项与回滚

### 8.1 风险

| # | 风险 | 影响 | 缓解 |
|---|---|---|---|
| R1 | e2e 选择器被布局改动打破 | V3 失败 | §7.3 契约清单；S3 完成后立即跑 V3 |
| R2 | 卡片头里绝对定位的 InfoTip 遮住长标题或 meta | 视觉重叠 | `.has-tip .ccc-chevron{margin-left:28px}` 预留位置；`.ccc-title` 已设置 ellipsis；X1/X2 截图核查 |
| R3 | InfoTip 与 Dropdown 菜单（同为 z-index 2000）同时打开 | 层叠混乱 | InfoTip 监听按钮外的 pointerdown 后关闭，打开 Dropdown 时 tip 必然先关闭 |
| R4 | 合并后的 subnav 在最小宽度下换行 | 三级 pill 落到第二行 | 已用 `flex-wrap` + `align-items:flex-end`，换行后仍在分隔线上方；X2 核查 |
| R5 | OpenCode 默认行常驻 | 行为变化（只增不减） | 只在未搜索时显示，不参与编辑，不影响已有数据 |
| R6 | Pi 卡片合并与重排（Profile 并入角色模型） | 用户的视觉记忆需要重新适应 | 字段与写入逻辑零变化；`updateProfile` 和 Dropdown 选项不变 |
| R7 | 用户文档措辞 | `docs/user/usage.md` 描述的是「二级/三级 Tab」，结构上仍然成立；Tab 名未改 | 不需要改文档；如果日后把「模型提供商」改为「模型注册表」，必须同步文档 |

### 8.2 未知项

| # | 未知 | 影响 | 判别办法 | 不可逆风险 |
|---|---|---|---|---|
| U1 | 角色模型、分层策略、并发、auth.json 的真实生效时机 | 这些区块暂时没有生效标签 | 查 amagi-pi 的配置读取时机（装配读一次还是派发时读盘） | 无：标签只是展示，后续确认后补加即可 |
| U2 | 视觉最终观感（密度、留白） | 可能需要微调间距 | luoshen 截图后由洛神裁决 | 无 |
| U3 | 深色主题 | 应用目前没有深色 token | 不在本轮范围；新基件已经完全 token 化 | 无 |

### 8.3 代价、可逆性与回滚

- 代价：新增 3 个 ui 基件（约 250 行），扩展 3 个共享件（向后兼容），9 个文件做模板级改动；不改 api、store、Go、依赖。
- 可逆性高：所有改动都在前端模板和样式层，没有数据格式或持久化变化，写入 JSON/YAML 的逻辑零改动。
- 回滚方式：按切片逆序 revert 实现提交即可。只回退单个编辑器时，revert 该文件，新基件可以保留（未被引用也无副作用）；基件最后回退。tokens.css 只新增了变量，保留也无害。
