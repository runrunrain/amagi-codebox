# 插件扩展页修复 - 审核报告

| 项目 | 内容 |
|------|------|
| 审核时间 | 2026-06-22 |
| 审核者 | 谛听（diting） |
| 审核轮次 | 第 1 轮（主体） + 第 2 轮（M-1 收口轻量复审） |
| Task Contract | amagi-codebox-plugin-fix-review-01 / amagi-codebox-plugin-fix-review-02 |
| 上游实现报告 | `X:/WorkSpace/amagi-codebox/frontend/fix-report-plugin-update.md`（洛神，含 M-1 收口小节） |
| 变更范围 | 2 个源码文件 + 2 个 markdown 报告 |
| 审核结论 | 第 1 轮：PASS-WITH-MINOR；第 2 轮（M-1 收口）：PASS |

---

## 一、上游 Artifact 引用

- Task Contract: `amagi-codebox-plugin-fix-review-01` / `amagi-codebox-plugin-fix-review-02`（本任务契约，diting phase P2-质量门控）
- 洛神实现报告: `X:/WorkSpace/amagi-codebox/frontend/fix-report-plugin-update.md`（含末尾「M-1 收口」追加小节）
- 变更文件：
  - `X:/WorkSpace/amagi-codebox/frontend/src/components/extensions/PluginInstalledPanel.vue`
  - `X:/WorkSpace/amagi-codebox/frontend/src/components/extensions/PluginSubItemsPanel.vue`（仅主体轮改动，M-1 轮未改）
- 交叉核实依赖：
  - `X:/WorkSpace/amagi-codebox/frontend/src/components/ui/Badge.vue`（确认 `capability` 配色存在，第 129-132 行）
  - `X:/WorkSpace/amagi-codebox/frontend/src/composables/useToast.ts`（确认 showSuccess/showError/showInfo 三态 API）
  - `X:/WorkSpace/amagi-codebox/frontend/src/components/common/Toast.vue` + `App.vue:5,18`（确认 Toast 容器已挂载根组件）
  - `X:/WorkSpace/amagi-codebox/frontend/src/stores/plugin.ts` 第 439/484/602/615 行（确认 loadPluginDetail / updatePlugin / upgradeCxMarketplace / loadCxPluginDetail 均存在且签名匹配）

---

## 二、代码审核发现（逐项）

### A. handleUpdate 逻辑闭环（修复点 1）

| 检查项 | 结论 | 证据 |
|--------|------|------|
| 防重入守卫 | PASS | `PluginInstalledPanel.vue:679` `if (updating.value) return;` |
| updating 状态正确管理 | PASS | `:681` 进入即置 true，`:709` finally 复位 |
| Codex marketplace 缺失 → showError | PASS | `:686-689` `(plugin as any).marketplace` 缺失 → `showError('该插件缺少市场来源信息，无法更新')` 并 return |
| Codex upgrade 调用参数 | PASS | `:691` `await upgradeCxMarketplace(marketplace)`，参数为 marketplace 字符串，与 store `:602` 签名一致 |
| 成功后 reload detail | PASS | Codex `:693-696` `loadCxPluginDetail(pid, marketplace)`；Claude `:702` `loadPluginDetail(plugin.id)`，刷新右侧详情 |
| Toast 三态 | PASS | showInfo（进行中）/ showSuccess（成功）/ showError（失败），`:690/:697/:699/:703/:707` |
| 错误捕获 + 日志 | PASS | `:705-707` catch + console.error + showError，错误信息区分 Error/message/String |
| 按钮 loading/disabled 绑定 | PASS | `:138-144` 更新与卸载按钮均 `:disabled="updating"`，文案切换 `'更新中…'/'更新'` |

**A 项结论**：修复点 1 完整闭环，无残留空实现，无 console.log 占位。Codex 升级语义对齐"按 marketplace 整体 upgrade"，文案显式提示"正在更新市场源 {marketplace}…"，避免主上误解。

### B. 解构与依赖

| 检查项 | 结论 | 证据 |
|--------|------|------|
| `upgradeCxMarketplace` 解构 | PASS | `:341` 在 store 解构块中，store 同时 export（`:712`） |
| `loadCxPluginDetail` / `loadPluginDetail` / `updatePlugin` 解构 | PASS | `:347-348`、`:340` |
| `useToast` 引入与解构 | PASS | `:300` import、`:353` 解构 showSuccess/showError/showInfo |
| `updating` ref 声明 | PASS | `:367` `const updating = ref(false);` |
| Toast 容器实际渲染 | PASS | `App.vue:18` `<Toast />`，`Toast.vue` Teleport 到 body，可见性有保证 |

**B 项结论**：所有依赖正确引入，Toast 反馈链路完整可被用户感知。

### C. 资源/能力概览区（修复点 2a）

| 检查项 | 结论 | 证据 |
|--------|------|------|
| 位置正确 | PASS | `:183` Codex 块 / `:219` Claude 块，均位于 `.plg-detail-meta`(`:158-180`) 之后、`.plg-detail-subitems`(`:246-253`) 之前 |
| Codex/Claude 互斥 | PASS | `:183 v-if="engine === 'codex'"` / `:219 v-else`，互斥语义正确 |
| 5 资源 badge 条件渲染 | PASS | `v-if="hasSkills/hasAgents/hasCommands/hasHooks/hasMcp"` 逐项守卫，无内容时不显示该项 |
| Badge color="capability" 支持 | PASS | `Badge.vue:129-132` 已有 `.type-badge.capability` 配色（中性灰），洛神报告"零 CSS 新增"属实 |
| capabilities 数组遍历守卫 | PASS | `:206 v-if="activeCxDetail?.manifest?.interface?.capabilities"` 可选链 + `:208 v-for`，缺失字段安全跳过 |
| computed 引用正确 | PASS | hasSkills/skillsCount 等 `:469-550` 依赖 `currentActivePlugin` 与 `activeCxDetail`，Codex 走 detail 数组长度，Claude 走 pluginType 字符串匹配 |
| MCP 固定显示 count=1 | 可接受 | `:204/:240` 写死 `1`，因 Codex `hasMcp` 为 boolean，无具体服务器数量；属合理简化 |

**C 项结论**：资源/能力概览区恢复正确，与重构前 9359dcb 行为对齐，零 CSS 新增。

### D. PluginSubItemsPanel 默认 tab（修复点 2b）

| 检查项 | 结论 | 证据 |
|--------|------|------|
| watch immediate 触发 | PASS | `PluginSubItemsPanel.vue:108-119` `watch(() => availableTabs.value, ..., { immediate: true })` |
| 选首个 count>0 tab | PASS | `:111` `tabs.find(t => t.value !== 'all' && t.count > 0)` |
| 全空时回退 'all' | PASS | `:114-116` else 分支设为 'all' |
| 'all' 路径空态兜底 | PASS | 模板 `:43-52` `<div v-else>` EmptyState「暂无内容 / 此插件未提供任何可管理的子项」 |
| 监听源切换 | PASS | `availableTabs` 是 computed，依赖 `props.pluginDetail`，父切换插件时 pluginDetail 变化 → 自动重算 → watch 重新触发 |
| 内存泄漏/无限循环 | PASS | watch 仅在 availableTabs 引用变化时触发，赋值 activeTab 不会反激 computed（activeTab 不在 computed 依赖链上） |

**D 项结论**：修复策略正确，无副作用风险。

### E. 类型安全与构建

| 检查项 | 结论 | 证据 |
|--------|------|------|
| vue-tsc 类型检查 | PASS | 洛神报告显示 `npm run build`（含 `vue-tsc --noEmit`）完整通过，1696 模块转换 765ms（主体）/ 695ms（M-1 收口）完成 |
| `(plugin as any)` 用量 | 可接受 | 多处 `as any` 用于访问后端动态字段（marketplace/pluginId/scope/source/installPath/manifestPath/subItems），属合理防御性写法，未掩盖逻辑漏洞 |

### F. 回归核对（既有功能零改动）

| 既有路径 | 结论 |
|---------|------|
| 卸载（handleUninstall → confirmUninstall → uninstallCxPlugin/uninstallPlugin） | 未触动，PASS |
| 启用开关（handleToggle → toggleCxPlugin/togglePlugin） | 未触动，PASS |
| 子项开关（handleToggleSubItem → SetPluginSubItemEnabled） | 未触动，PASS |
| 市场安装入口（PluginMarketPanel + handleAddMarket） | 未触动，PASS |
| Codex 重复诊断（hasDuplicates/duplicateCount/duplicateNames/hasDuplicateWarning/getDuplicateWarning） | 未触动，PASS |
| selectPlugin 流程（setActivePlugin + loadCxPluginDetail/loadPluginDetail） | 未触动，PASS |

---

## 三、浏览器交互验证证据（含限制披露）

### 已执行的客观证据

1. 启动 vite dev server：`cd "X:/WorkSpace/amagi-codebox/frontend && npm run dev"`
   - 输出：`VITE v8.0.8 ready in 268 ms`，Local URL `http://localhost:5173/`
2. HTTP 探测：`curl http://localhost:5173/` → HTTP 200，返回标准 `<div id="app">` + main.ts 入口的 SPA 骨架
3. 洛神提供的构建证据：`npm run build`（含 vue-tsc）PASS，1696 模块转换，无类型错误

### 限制披露（必须声明）

**本次浏览器交互验证未覆盖真实后端调用路径**，原因：

1. **本审核环境无 Browser/Playwright 工具**（工具清单仅含 Read/Write/Bash/Grep/Glob），无法实际驱动页面交互、读取渲染结果或捕获 Toast。
2. **Wails 桌面应用特性**：纯浏览器（vite dev）模式下 `window.go` 未绑定，`pluginApi` / `codexPluginApi` 调用会失败，无法验证 `upgradeCxMarketplace` / `loadCxPluginDetail` / `updatePlugin` 的真实数据流。即使 wails dev 可启动，其自动打开的 GUI webview 也无法被本环境工具自动化读取。
3. **未采用 mock 注入**：Task Contract B2 允许临时 mock，但既然无 Browser 工具可读取渲染结果，mock 注入只能让代码"跑过"而不能产生可观测证据，等同伪验证，故不采用。

### 因限制而采取的补强

为弥补浏览器交互不可达，已额外执行：
- **绑定路径核对**：逐一比对 `handleUpdate` 调用链（`upgradeCxMarketplace` → store `:602` → `codexPluginApi.upgradeCodexMarketplace`；`loadCxPluginDetail(pid, marketplace)` → store `:615` 签名匹配；`updatePlugin` → store `:484`；`loadPluginDetail` → store `:439`）。所有调用签名与参数类型一致。
- **Toast 渲染挂载核对**：确认 `App.vue:18 <Toast />` 已挂载根组件，`Toast.vue` 通过 `<Teleport to="body">` + fixed 定位渲染，showSuccess/showError/showInfo 三态配色完整（success 蓝 / error 红 / info 蓝），不会被 z-index 遮挡。
- **资源概览渲染路径核对**：Codex 块依赖 `activeCxDetail`（`:448-451` computed，从 `activeCxPluginDetail.value[activePluginId]` 取），与 `loadCxPluginDetail` 写入缓存（store `:622` `cxActivePluginDetail.value[pluginId] = detail`）路径闭环；Claude 块依赖 `currentActivePlugin`(`:444-446`) 的 pluginType 字符串匹配。
- **构建证据**：vue-tsc + vite build 全绿。

**审核者诚实结论**：代码层面的功能闭环、依赖引入、回归边界均已系统核对且通过；但 UI 视觉呈现（如 badge 实际配色、Toast 动画、tab 切换动画、按钮 loading 文案的实际像素、子项面板隐藏后的视觉空隙）**未通过工具可视化验证**，需主上启动 `wails dev` 后人工目测一次。

---

## 四、重点判断项 C：Claude 引擎下子项面板的误导性 UI（已在第 2 轮 M-1 收口）

### 现象（第 1 轮发现）

- `PluginSubItemsPanel.vue:83` `if (props.engine === 'codex' && props.pluginDetail)` 守卫导致 Claude 引擎下 `availableTabs` 永远只有 `[{value:'all', label:'全部', count:0}]`。
- 父组件 `:249` 在 Claude 场景下传入 `activePlugin`（Claude 插件对象），其 `skills/agents/commands/hooks` 字段在 Claude 数据结构中通常不存在（Claude 按 pluginType 字符串标记）。
- 结果：Claude 插件详情页上方"资源"区可能显示「Skill / Agent」badge（如 hybrid/integration 类型），但下方子项面板显示「暂无内容 - 此插件未提供任何可管理的子项」。

### 第 1 轮结论

Minor（建议修复，不阻断本次提交）。判定理由：
1. 非本次回归；
2. 影响有限；
3. 修复成本低（隐藏包裹层或改 EmptyState 文案）。

### 第 2 轮：M-1 收口已落实

详见第十二章「M-1 收口复审」。

---

## 五、质量对比结论

| 维度 | 基准状态（修复前） | 新状态（修复后，含 M-1） | 对比结论 |
|------|---------|--------|---------|
| 功能正确性 | handleUpdate Codex 空实现 / detail 区资源概览丢失 / 默认 tab 空白 / Claude 子项面板误导 | 完整闭环、资源恢复、默认 tab 智能、误导消除 | 提升 |
| 安全风险 | 无 | 无 | 无新增（OWASP Top10 不适用：纯前端 UI 修复，无新输入/网络/凭据面） |
| 性能 | - | watch(computed) 轻量，无重复触发；新增一个 computed，开销可忽略 | 维持 |
| 可维护性 | 大量 `(plugin as any)` 防御性写法 | 沿用既有风格；新增 computed 复用既有数据源 | 维持 |
| 一致性 | 与项目苹果 HIG 风格、既有 Badge 颜色一致 | 零 CSS 新增 | 维持 |
| 硬编码/伪实现 | 无 | 无 | 维持（仅 MCP count=1 属合理业务简化） |

---

## 六、安全审计结果

| OWASP 分类 | 发现 | 置信度 | 严重性 |
|-----------|------|--------|--------|
| 注入 / XSS / 访问控制 / 认证 / 数据暴露 | 本次变更为纯前端 UI 修复，无新增网络调用、无新增用户输入处理、无凭据操作、无 DOM innerHTML 注入（Toast 文案通过 Vue `{{ }}` 模板插值自动转义） | N/A | 无 |

无安全发现，置信度均 <80% 阈值，不予报告。

---

## 七、硬编码排查

| 检测维度 | 发现 | 处理 |
|---------|------|------|
| 凭据 | 无 | - |
| 文件路径 / IP / 设备绑定 | 无 | - |
| 魔法数字 | `res-count` 文案中 MCP count 写死为 1（`:204/:240`） | 业务语义合理（Codex hasMcp 为 boolean），可接受，非伪实现 |
| 伪实现 | 无 | handleUpdate 两分支均接入真实 store action，无 mock 数据 |

---

## 八、正向反馈

1. **修复闭环度高**：handleUpdate 两分支 Toast 三态 + loading + reload detail 一次到位，无半成品。
2. **零 CSS 新增策略**：复用既有 `.type-badge.capability` 配色，避免冗余，体现了对既有设计系统的尊重。
3. **回归边界清晰**：所有改动集中在两个文件，未触动卸载/开关/安装/重复诊断等既有路径。
4. **风险与回滚章节完备**：洛神报告明确披露 Codex marketplace 整体 upgrade 语义、pluginDetail 异步延迟风险，并给出单文件回滚命令。
5. **Codex 文案对齐后端语义**：将"更新插件"显式化为"正在更新市场源 {marketplace}…"，避免主上误解。
6. **M-1 收口设计精准**：新增 `hasManageableSubItems` computed 严格复用既有数据源（Codex 走 `activeCxDetail`、Claude 走 `currentActivePlugin.subItems`），与 `disabledSubItems`/`availableTabs` 的取值方式 1:1 对齐，未引入新假设、新字段、新 CSS，符合"最小改动"约束。

---

## 九、问题清单（合并第 1、2 轮）

### Critical（必须修复，阻断发布）
无。

### Major（必须修复）
无。

### Minor（建议修复，可后续处理）

| 编号 | 状态 | 说明 |
|------|------|------|
| M-1 | 已在第 2 轮收口 PASS | Claude 引擎下子项面板误导 UI；详见第十二章 |

---

## 十、建议下一步（最终版，覆盖第 1+2 轮）

- **整体审核结论：PASS**（M-1 已收口，无残留 Minor）。
- **建议分派**：
  - 太白金星（taibai）：执行 Git 提交，最终 changed files 清单为：
    1. `frontend/src/components/extensions/PluginInstalledPanel.vue`
    2. `frontend/src/components/extensions/PluginSubItemsPanel.vue`
    3. `frontend/fix-report-plugin-update.md`
    4. `frontend/review-report-plugin-fix.md`
- **建议主上**：在 `wails dev` 完整后端环境下目测一次（重点看 ① Codex 插件更新按钮 Toast；② 资源概览 badge 实际渲染；③ tab 默认选中；④ Claude 无 subItems 插件详情页子项区域是否完全隐藏且不残留分隔感），以补足本审核者无法用工具验证的可视化环节。

---

## 十一、自检清单（第 1 轮）

| 自检项 | 状态 |
|--------|------|
| 已读 fix-report + 两个变更文件 + Badge.vue | PASS |
| 已读 useToast.ts + 确认 Toast 容器挂载 | PASS |
| 已核对 store 中 4 个 action 签名与调用一致 | PASS |
| 代码审核逐项有结论（A/B/C/D/E/F） | PASS |
| 浏览器验证：已执行 vite dev 启动 + HTTP 探测 | PASS（部分覆盖） |
| 浏览器验证限制已明确披露（无 Browser 工具 + Wails 桌面限制） | PASS |
| 重点判断项 C 已给出明确结论（Minor，非本次回归） | PASS |
| 安全审计 OWASP Top10 已执行，无可报告发现 | PASS |
| 硬编码/伪实现已排查，无非问题 | PASS |
| 问题分级（Critical/Major/Minor）已标注 | PASS |
| 正向反馈已提供 | PASS |
| 报告已落盘到规定路径 | PASS |

---

## 十二、M-1 收口复审（第 2 轮 · 轻量复审）

### 审核概要

| 项目 | 内容 |
|------|------|
| 审核时间 | 2026-06-22 |
| 审核轮次 | 第 2 轮（仅复审 M-1 收口这一处改动） |
| Task Contract | amagi-codebox-plugin-fix-review-02 |
| 上游实现报告 | `fix-report-plugin-update.md` 末尾「M-1 收口（Minor 修复）」小节 |
| 本次变更范围 | 1 个源码文件（`PluginInstalledPanel.vue`）；`PluginSubItemsPanel.vue` 未改 |
| 审核结论 | **PASS** |

### A. hasManageableSubItems computed 正确性

| 检查项 | 结论 | 证据 |
|--------|------|------|
| Codex 分支数据源与 availableTabs 一致 | PASS | `PluginInstalledPanel.vue:556-565` Codex 分支取 `activeCxDetail.value` 的 `skills/agents/commands/hooks/hasMcp`；`PluginSubItemsPanel.vue:83-99` `availableTabs` codex 守卫取 `props.pluginDetail` 的相同五项；父级 `:249` 在 Codex 场景传入的就是 `activeCxDetail`，二者数据源等价 |
| 五项判定 1:1 对齐 | PASS | 两侧均为：skills/agents/commands/hooks 任一 `?.length > 0` + hasMcp 为真。父级用 `detail.hasMcp === true`（严格相等），子级 `if (detail.hasMcp)`（真值判定），在 hasMcp 为 boolean 时语义等价；无「渲染了但子级 tab 空」或「有内容却不渲染」的错位 |
| Claude 分支取值与 disabledSubItems 一致 | PASS | `:567-569` 取 `(currentActivePlugin.value as any)?.subItems`，`Array.isArray(...) && length > 0`；`:734-749` 既有 `disabledSubItems` 在 Claude 分支亦取 `(plugin as any).subItems`（plugin 即 `currentActivePlugin.value`），字段名（小写 `subItems`、大写 S）、取值对象完全一致 |
| `!detail` 守卫 | PASS | `:557-558` Codex 分支 detail 未到位时返回 false，子项区暂不渲染；detail 到位后 computed 重算自动展开，不闪烁 EmptyState |
| Claude subItems 非 array 安全 | PASS | `:568-569` `Array.isArray` 守卫，`undefined`/`null`/非数组都安全返回 false |
| computed 引用闭环 | PASS | `activeCxDetail`(`:448-451`) 与 `currentActivePlugin`(`:444-446`) 均为既有 computed，无新数据源引入 |

### B. v-if 绑定位置

| 检查项 | 结论 | 证据 |
|--------|------|------|
| v-if 在 `.plg-detail-subitems` 容器本体 | PASS | `:246` `<div v-if="hasManageableSubItems" class="plg-detail-subitems">`，整个包裹层（含其内部 PluginSubItemsPanel）一并隐藏，无残留分隔感 |
| 仅一处 v-if，未误改其他元素 | PASS | grep 确认 `plg-detail-subitems` 在模板中仅出现一次（`:246`），CSS 定义在 `:1251` 不变 |

### C. 无回归确认

| 既有路径 | 结论 |
|---------|------|
| 修复点 1（handleUpdate + updating ref + Toast 三态 + 按钮 loading） | 未触动，PASS |
| 修复点 2a（`.plg-detail-resources` 资源/能力概览区块，`:183/:219`） | 未触动，PASS |
| 修复点 2b（`PluginSubItemsPanel.vue` 的 watch + 'all' 空态兜底） | 整个文件未改动，PASS |
| `disabledSubItems` computed（`:734-749`） | 未触动，PASS |
| 卸载 / 启用开关 / 子项开关 / 市场安装 / Codex 重复诊断 | 未触动，PASS |

### D. 构建证据核对

| 检查项 | 结论 | 证据 |
|--------|------|------|
| vue-tsc 类型检查 | PASS | 洛神报告显示 `npm run build`（含 `vue-tsc --noEmit`）PASS；任何类型错误都会中断构建 |
| vite build | PASS | 695ms 完成，1696 模块转换（与第 1 轮主体构建的 765ms / 1696 模块规模一致，差异在毫秒级属正常波动） |
| 警告 | 仅 chunk 体积历史警告 | 与本次修改无关 |

### E. M-1 收口问题清单

#### Critical / Major
无。

#### Minor
无。（M-1 已闭环；PluginSubItemsPanel 内部 'all' 空态 EmptyState 文案保留作为兜底，不视为新问题）

### F. M-1 收口建议下一步

- **本次变更审核结论：PASS**。
- **无回流点**。
- **整体（第 1+2 轮）审核结论：PASS**，可进入提交流程。
- **建议太白金星（taibai）执行 Git 提交**，最终 changed files 清单（共 4 项）：
  1. `X:/WorkSpace/amagi-codebox/frontend/src/components/extensions/PluginInstalledPanel.vue`
  2. `X:/WorkSpace/amagi-codebox/frontend/src/components/extensions/PluginSubItemsPanel.vue`
  3. `X:/WorkSpace/amagi-codebox/frontend/fix-report-plugin-update.md`
  4. `X:/WorkSpace/amagi-codebox/frontend/review-report-plugin-fix.md`

### G. M-1 收口自检清单

| 自检项 | 状态 |
|--------|------|
| 已读 `PluginInstalledPanel.vue` hasManageableSubItems + v-if 改动（`:246` / `:552-570`） | PASS |
| 已读 `PluginSubItemsPanel.vue` availableTabs codex 守卫（`:80-103`）做 1:1 对齐核对 | PASS |
| 已读 `disabledSubItems` computed（`:734-749`）做 Claude 字段名一致性核对 | PASS |
| Codex 判定逻辑对齐结论：1:1 等价 | PASS |
| Claude 判定逻辑对齐结论：字段名/取值对象一致 | PASS |
| v-if 绑定在 `.plg-detail-subitems` 容器（连分隔线一起隐藏）核对通过 | PASS |
| 修复点 1 / 2a / 2b 无回归确认 | PASS |
| 构建证据（695ms / 1696 模块 / vue-tsc 通过）合理可信 | PASS |
| review-report 已更新、结论明确（PASS） | PASS |

---

**审核者**：谛听（diting）
**报告路径**：`X:/WorkSpace/amagi-codebox/frontend/review-report-plugin-fix.md`
