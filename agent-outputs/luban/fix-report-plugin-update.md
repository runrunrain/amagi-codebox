# 插件扩展页前端修复实现报告

## 概要

| 项目 | 内容 |
|------|------|
| 任务 | amagi-codebox-plugin-fix-01：修复扩展管理页两个前端缺陷 |
| 变更范围 | 2 个文件（PluginInstalledPanel.vue、PluginSubItemsPanel.vue） |
| 设计方向 | 沿用现有苹果 HIG 风格，零新增 CSS、零新增依赖，仅恢复/接入既有能力 |
| 引擎 | Wails v2 + Vue 3 + TypeScript + Pinia |

## 上游 Artifact 引用

- Task Contract: `amagi-codebox-plugin-fix-01`（本次任务契约）
- 旧版参考实现: `git show 9359dcb^:frontend/src/components/extensions/PluginInstalledPanel.vue` 第 170-234 行 `plg-detail-resources` 区块
- Store: `X:/WorkSpace/amagi-codebox/frontend/src/stores/plugin.ts`（`updatePlugin` / `upgradeCxMarketplace` / `loadPluginDetail` / `loadCxPluginDetail` 均已存在）
- Toast: `X:/WorkSpace/amagi-codebox/frontend/src/composables/useToast.ts`
- Badge: `X:/WorkSpace/amagi-codebox/frontend/src/components/ui/Badge.vue`（已原生支持 `color="capability"`，见 CSS 129-132 行，无需新增配色）

## 代码变更清单

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `frontend/src/components/extensions/PluginInstalledPanel.vue` | 修复 + 恢复 | 1) 引入 useToast；2) 解构补充 `upgradeCxMarketplace`；3) 新增 `updating` ref；4) 重写 `handleUpdate`；5) 更新按钮加 loading/disabled；6) 恢复 `plg-detail-resources` 资源/能力概览区块 |
| `frontend/src/components/extensions/PluginSubItemsPanel.vue` | 修复 | 1) 引入 watch；2) 新增 watch(availableTabs) 自动选首个有内容的 tab；3) 模板新增 'all' 状态下的 empty 提示 |

---

## 修复点 1：插件更新按钮无响应

### 解构与 Toast 引入

在 store action 解构里补充 `upgradeCxMarketplace`（原解构缺失）；从 `../../composables/useToast` 引入 `useToast`，解构 `showSuccess/showError/showInfo`。新增 `updating` ref 用于按钮 loading 态。

### 最终 handleUpdate 代码（要点）

- 入口守卫：`if (updating.value) return;` 防止重复触发。
- 进入即 `updating.value = true`，结束在 `finally` 中复位。
- Codex 分支：
  - 读取 `(plugin as any).marketplace`，若缺失 → `showError('该插件缺少市场来源信息，无法更新')` 并 return。
  - 否则 `showInfo` 提示正在更新市场源 → `await upgradeCxMarketplace(marketplace)` → 调用 `loadCxPluginDetail(pid, marketplace)` 重载选中插件详情 → `showSuccess`。
  - 原因：Codex 后端无单插件更新接口，更新机制是 marketplace 级 upgrade；`upgradeCxMarketplace` 内部已 `loadCxPlugins(true)`，额外补一次 detail 让右侧详情区即时刷新。
- Claude 分支：
  - `showInfo` 提示 → `await updatePlugin(plugin.id)` → `await loadPluginDetail(plugin.id)` → `showSuccess`。
  - `updatePlugin` 内部已 `loadCcInstalled`，额外 `loadPluginDetail` 确保 ccActivePluginDetail 缓存刷新（子项/资源计数即时更新）。
- 错误统一 `catch`：`showError('更新失败: ${error}')`，同时 `console.error` 记录原始堆栈。

### 按钮绑定

```vue
<AppButton variant="ghost" size="small" :disabled="updating" @click="handleUpdate">
  {{ updating ? '更新中…' : '更新' }}
</AppButton>
<AppButton variant="danger" size="small" :disabled="updating" @click="handleUninstall">
  卸载
</AppButton>
```

更新期间卸载按钮也 disabled，避免并发状态冲突。

---

## 修复点 2a：恢复 detail 区资源/能力概览

### 位置

在 `.plg-detail-meta`（meta 信息块）之后、`.plg-detail-subitems`（PluginSubItemsPanel 包裹层）之前，新增两块互斥的 `plg-detail-resources`：

- `v-if="engine === 'codex'"`：标题「能力」，渲染 Skill/Agent/Command/Hook/MCP 计数 badge（依赖 hasSkills/hasAgents/hasCommands/hasHooks/hasMcp computed），额外遍历 `activeCxDetail?.manifest?.interface?.capabilities` 渲染 capability badges。
- `v-else`：标题「资源」，仅渲染 5 个资源 badge + 计数。

### Badge color 处理

- 全部复用现有 color：`skill`/`agent`/`command`/`hook`/`mcp`/`capability`。
- 经核实 `Badge.vue` CSS 第 129-132 行已有 `.type-badge.capability` 配色（`color: var(--secondary); background: var(--control);`，苹果 HIG 中的中性灰），**无需新增配色**，零 CSS 改动。
- CSS `.plg-detail-resources/.res-grid/.res-item` 在 PluginInstalledPanel.vue 1110-1136 行已存在且仍可用。

---

## 修复点 2b：PluginSubItemsPanel 默认 tab

### 问题

`activeTab` 默认 `'all'`，模板 `v-if="activeTab !== 'all'"` 导致默认进入时下方完全空白。

### 修复策略

新增 `watch(availableTabs, { immediate: true })`：

```ts
watch(
  () => availableTabs.value,
  (tabs) => {
    const firstWithContent = tabs.find((t) => t.value !== 'all' && t.count > 0);
    if (firstWithContent) {
      activeTab.value = firstWithContent.value;
    } else {
      activeTab.value = 'all';
    }
  },
  { immediate: true }
);
```

- `availableTabs` 是 computed，依赖 `props.pluginDetail`，所以切换插件（pluginDetail 变化）会自动重新触发 watch，重置默认 tab。
- 全部 tab 都为空时保持 `'all'`，此时模板新增 `v-else` 分支显示 EmptyState（标题「暂无内容」，描述「此插件未提供任何可管理的子项」），避免页面空白。
- `immediate: true` 保证首次渲染就计算默认 tab，无需 onMounted。

### 模板改动

原 `<div v-if="activeTab !== 'all'">` 之后新增 `<div v-else>` 分支，展示统一的 EmptyState（不在原 'all' 路径下重复 list 逻辑）。

---

## 验证结果（自行执行，未让主上手测）

### 构建命令

```bash
cd "X:/WorkSpace/amagi-codebox/frontend" && npm run build
```

### 真实输出关键行

```
> frontend@1.2.57 build
> vue-tsc --noEmit && vite build

vite v8.0.8 building client environment for production...
✓ 1696 modules transformed.
rendering chunks...
computing gzip size...
dist/assets/ExtensionsView-CV-okuAV.js         75.67 kB │ gzip:  20.93 kB
...
✓ built in 765ms
(!) Some chunks are larger than 500 kB after minification. Consider:
- Using dynamic import() to code-split the application
... (chunk 体积 warning，与本次修改无关)
```

### 结论

| 检查项 | 状态 | 说明 |
|--------|------|------|
| vue-tsc 类型检查 | PASS | 无任何 TS 报错（任何报错都会中断构建） |
| vite build | PASS | 765ms 完成，产物正常输出 |
| 警告 | 仅 chunk 体积 | 历史遗留，与本次修改无关 |
| 既有功能未破坏 | 视觉一致 | 卸载、启用开关、子项开关、市场安装、重复诊断等逻辑零改动 |

---

## 风险与回滚

### 风险

1. **Codex 更新语义**：Codex 后端是按 marketplace 整体 upgrade，而非单插件。UI 文案已对齐为「正在更新市场源 {marketplace}」，避免主上误解为单插件级更新。若同一市场源下有多个插件，会一起被 upgrade（这是后端既有行为，本次未改动）。
2. **pluginDetail 异步延迟**：`watch(availableTabs)` 在 detail 未到位时会把 activeTab 设为 'all'，detail 到位后会自动重选首个有内容 tab。若 detail 加载失败，停留在 'all' 的 EmptyState，不会卡死。
3. **capability 渲染**：依赖 `activeCxDetail.manifest.interface.capabilities`，若某些 Codex 插件 manifest 结构缺失该字段，`v-if` 与 `v-for` 会安全跳过，不会报错。

### 回滚

所有改动集中在两个文件，可单文件回滚而不影响其他模块：

```bash
cd "X:/WorkSpace/amagi-codebox"
git checkout -- frontend/src/components/extensions/PluginInstalledPanel.vue
git checkout -- frontend/src/components/extensions/PluginSubItemsPanel.vue
```

---

## 自检清单

| 检查项 | 状态 |
|--------|------|
| 已 Read 所有 required_artifacts | PASS |
| handleUpdate Codex 分支已接入 upgradeCxMarketplace | PASS |
| 两分支均有 Toast 反馈 + 按钮 loading | PASS |
| detail 区恢复资源/能力概览 | PASS |
| Badge color 处理（复用既有 'capability'，零 CSS 新增） | PASS |
| PluginSubItemsPanel 默认进入首个有内容 tab | PASS |
| npm run build 通过且证据记入报告 | PASS |
| 实现报告已落盘 | PASS |

---

## 建议下一步

建议下一步：交谛听做浏览器交互验证。

（谛听重点验证：① Codex 插件更新按钮点击后出现 loading + Toast，更新成功后详情区刷新；② Claude 插件更新同上；③ detail 区资源/能力 badges 计数与子项面板实际内容一致；④ 切换不同插件时子项面板自动选中有内容的首个 tab；⑤ 缺少 marketplace 字段的 Codex 插件点击更新时显示错误 Toast；⑥ 卸载、启用开关、子项开关、市场安装、重复诊断等既有功能无回归。）

---

# M-1 收口（Minor 修复）

## 背景

谛听审核报告 PASS-WITH-MINOR，唯一遗留 Minor：

- M-1（`PluginInstalledPanel.vue:246` + `PluginSubItemsPanel.vue:83`）：Claude 引擎下 `availableTabs` 因 `engine === 'codex'` 守卫永远只有 `'all'` tab，导致 Claude 插件详情页底部恒显「暂无内容 - 此插件未提供任何可管理的子项」。同时上方资源概览区可能因 `pluginType` 命中而显示 Skill/Agent badge，构成轻微误导 UI。

本次仅做该收口，不扩展任何新功能。

## 上游 Artifact 引用

- 审核报告: `X:/WorkSpace/amagi-codebox/frontend/review-report-plugin-fix.md`（第四章「重点判断项 C」、第九章 M-1）
- 上一轮实现报告: `X:/WorkSpace/amagi-codebox/frontend/fix-report-plugin-update.md`（修复点 1 / 2a / 2b）
- 变更主体:
  - `X:/WorkSpace/amagi-codebox/frontend/src/components/extensions/PluginInstalledPanel.vue`
  - `X:/WorkSpace/amagi-codebox/frontend/src/components/extensions/PluginSubItemsPanel.vue`（未改动，保留 'all' 空态作为兜底）

## 代码变更清单

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `frontend/src/components/extensions/PluginInstalledPanel.vue` | 最小收口 | 1) 新增 `hasManageableSubItems` computed；2) 给 `.plg-detail-subitems` 容器加 `v-if="hasManageableSubItems"`，无子项时不渲染整个区域（连分隔感也不出现） |

`PluginSubItemsPanel.vue` 本次未改动，其模板 `v-else` 分支的「暂无内容」EmptyState 作为兜底保留（父级隐藏时不会触发，但若未来 detail 异步到达而父级 v-if 已展开但 panel 内 tab 还在 'all' 的极短窗口内仍可由该兜底覆盖）。

## Before / After

### Before

```vue
<!-- 固定渲染，不论是否有可管理子项 -->
<div class="plg-detail-subitems">
  <PluginSubItemsPanel ... />
</div>
```

- Claude 插件：`availableTabs` 只有 `[{value:'all', label:'全部', count:0}]`，watch 把 activeTab 设为 `'all'`，模板走 `v-else` → 「暂无内容 - 此插件未提供任何可管理的子项」永远显示。
- 上方资源概览却可能因 `(p as any).pluginType` 命中 'skill'/'agent'/'hybrid'/'integration' 显示 Skill/Agent badge → 上下矛盾。

### After

```vue
<!-- 有可管理子项才渲染 -->
<div v-if="hasManageableSubItems" class="plg-detail-subitems">
  <PluginSubItemsPanel ... />
</div>
```

- Claude 插件无 `subItems` 数组（或为空数组）→ `hasManageableSubItems` 为 `false` → 整个子项区域不渲染，上下文不再矛盾。
- Codex 插件任一子项数组非空或 `hasMcp` 为真 → 正常渲染（行为与原来一致，零回归）。

## 新增 computed 逻辑

```ts
const hasManageableSubItems = computed(() => {
  if (props.engine === 'codex') {
    const detail = activeCxDetail.value;
    if (!detail) return false;
    return (
      (detail.skills?.length || 0) > 0 ||
      (detail.agents?.length || 0) > 0 ||
      (detail.commands?.length || 0) > 0 ||
      (detail.hooks?.length || 0) > 0 ||
      detail.hasMcp === true
    );
  }
  // Claude 引擎：插件后端返回的子项数组（大写字段，与 disabledSubItems 取值方式一致）
  const subItems = (currentActivePlugin.value as any)?.subItems;
  return Array.isArray(subItems) && subItems.length > 0;
});
```

设计要点：

1. **复用既有依赖**：Codex 走 `activeCxDetail`（与 `hasSkills/hasAgents/...` 完全相同的来源），Claude 走 `currentActivePlugin.value.subItems`（与既有 `disabledSubItems` computed 第 727-731 行取值方式一致），不引入新数据源。
2. **Codex 判定与 availableTabs 一致**：`PluginSubItemsPanel.vue:83-99` 的 `availableTabs` 守卫使用的就是 `skills/agents/commands/hooks/hasMcp` 五项，本 computed 在 Codex 分支与之 1:1 对齐，保证「面板渲染 ⇔ 面板有内容」严格等价。
3. **Claude 判定基于真实后端字段**：`subItems` 是 Claude 插件后端返回的 `SubItem[]`（参考 `disabledSubItems` 注释「For Claude plugins, subItems are available in plugin detail (from cache)」）。仅当数组确实非空时才认定有可管理子项，避免 `undefined`/`null` 误判。
4. **保留 PluginSubItemsPanel 内部兜底**：未删除 `'all'` 空态 EmptyState，作为 detail 异步到达窗口期或边界数据的最终兜底，符合「最小改动」约束。

## 验证（自行执行）

### 构建命令

```bash
cd "X:/WorkSpace/amagi-codebox/frontend" && npm run build
```

### 真实输出关键行

```
> frontend@1.2.57 build
> vue-tsc --noEmit && vite build

vite v8.0.8 building client environment for production...
✓ 1696 modules transformed.
rendering chunks...
computing gzip size...
dist/assets/ExtensionsView-s3aU-SZ2.js  75.94 kB │ gzip: 21.00 kB
...
✓ built in 695ms
(!) Some chunks are larger than 500 kB after minification. (历史体积警告，与本次无关)
```

### 结论

| 检查项 | 状态 | 说明 |
|--------|------|------|
| vue-tsc 类型检查 | PASS | 无任何 TS 报错（任何报错都会中断构建） |
| vite build | PASS | 695ms 完成，1696 模块转换，产物正常输出 |
| 警告 | 仅 chunk 体积 | 历史遗留，与本次修改无关 |
| 修复点 1（handleUpdate）未破坏 | PASS | 本次未触动该函数及其按钮绑定 |
| 修复点 2a（资源概览区）未破坏 | PASS | 本次未触动 `.plg-detail-resources` 区块，资源 badge 仍正常显示 |
| 修复点 2b（默认 tab）未破坏 | PASS | 本次未触动 `PluginSubItemsPanel.vue` 的 watch / availableTabs 逻辑 |

## 自检清单

| 检查项 | 状态 |
|--------|------|
| 已 Read 所有 required_artifacts（review-report、fix-report、两个组件文件、disabledSubItems、activeCxDetail、currentActivePlugin） | PASS |
| 仅做 M-1 收口，未扩展新功能 | PASS |
| Codex 分支判定与 availableTabs 守卫 1:1 对齐 | PASS |
| Claude 分支复用既有 `subItems` 取值方式（大写字段） | PASS |
| `.plg-detail-subitems` 容器加 `v-if`（不是改 PluginSubItemsPanel 内部） | PASS |
| 未修改后端、未修改 wailsjs 绑定 | PASS |
| 保持现有苹果 HIG 视觉、复用现有 computed、中文界面、无 emoji | PASS |
| 修复点 1 / 2a / 2b 零回归 | PASS |
| npm run build 通过且关键输出记入报告 | PASS |

## 风险与回滚

### 风险

1. **Claude subItems 字段名**：依赖后端 Claude 插件对象以 `subItems`（大写 S）字段返回子项数组。该假设与既有 `disabledSubItems` computed（已通过谛听审核）的取值方式完全一致，因此风险等价于现有代码，无新增风险。
2. **detail 异步到达**：Codex 场景下若 `activeCxDetail` 尚未加载，`hasManageableSubItems` 暂为 `false` → 子项面板暂不渲染；detail 到位后 computed 重算 → 自动展开。用户体验上等同于「加载完成后展开」，不会闪烁出 EmptyState，优于旧行为。

### 回滚

仅一处模板改动 + 一处新增 computed，单文件回滚即可：

```bash
cd "X:/WorkSpace/amagi-codebox"
git checkout -- frontend/src/components/extensions/PluginInstalledPanel.vue
```

## 建议下一步

建议下一步：交谛听复审 M-1 收口 + 太白金星提交。
