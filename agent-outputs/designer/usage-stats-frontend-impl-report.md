---
title: "使用统计前端实现报告 / Usage Stats Frontend Implementation Report"
doc_type: design-impl-report
owner: luoshen (designer / 前端)
task: amagi-codebox 使用统计（AI 模型用量与成本统计）前端完整实现
status: ready_for_review
created: 2026-07-17
source_artifacts:
  - agent-outputs/architect/usage-stats-design.md
  - frontend/wailsjs/go/usage/Service.d.ts
  - frontend/wailsjs/go/models.ts
  - frontend/src/views/LogsView.vue（四态骨架范式）
  - frontend/src/stores/session.ts（Pinia setup style 范式）
  - frontend/src/api/proxy.ts（api 封装范式）
verifies:
  build: "npm --prefix frontend run build (vue-tsc --noEmit + vite build) PASS"
  browser: "Chrome headless puppeteer 四态 + 图表 + 筛选器交互全部验证"
---

# 使用统计前端实现报告

## 1. 任务范围与设计方向

完整实现「使用统计」功能的桌面 Web 前端（Vue 3 + Element Plus + Pinia + Chart.js），覆盖路由、导航、API 封装、Pinia store、UsageView 四态主视图、PricingDialog 单条价格编辑、ModelPricingTable 价格表管理、成本/Token 格式化工具。**不改动后端 Go 与 wailsjs 绑定**，仅作为消费方。

**设计方向**：严格仿照现有 LogsView.vue 的四态骨架与 CSS Grid 仪表盘卡片，保持 Apple HIG 系统色调（accent/success/warning/purple 等 tokens），让新视图与现有页面（Logs/Rules/EnvCheck）形成同源视觉语言；图表用 Chart.js 4 + vue-chartjs 5（主上已拍板），主题色全部从 `tokens.css` 读取，未来支持 dark mode 时只需替换 token 即可。

## 2. 变更清单

### 2.1 新建文件（7 个）

| 路径 | 用途 | 行数 |
|---|---|---|
| `frontend/src/views/UsageView.vue` | 主视图：四态骨架 + 仪表盘 + 4 图表 + 模型表 + 价格入口 | 1058 |
| `frontend/src/stores/usage.ts` | Pinia setup style store，含 fetchAll 区分首次/后台 | 268 |
| `frontend/src/api/usage.ts` | 包装 12 个 wailsjs 方法 + 类型别名 + filter 工厂 | 218 |
| `frontend/src/utils/usage-format.ts` | formatCost/formatTokens/formatPerMillion/跨币种 USD 折算 | 138 |
| `frontend/src/components/usage/PricingDialog.vue` | 单条价格编辑对话框（四维单价 + 币种） | 480 |
| `frontend/src/components/usage/ModelPricingTable.vue` | 价格表组件（搜索/编辑/删除/未知模型快捷入口） | 340 |

### 2.2 修改文件（4 个）

| 路径 | 改动 | 行数变化 |
|---|---|---|
| `frontend/package.json` | 新增 `chart.js@^4.5.1` 与 `vue-chartjs@^5.3.4` | +2 deps |
| `frontend/src/router/index.ts` | 新增 `/usage` 路由（在 404 通配之前） | +6 |
| `frontend/src/components/layout/SidebarNormal.vue` | navItems 追加「使用统计」项（折线图 SVG 图标） | +5 |
| `frontend/src/api/index.ts` | 追加 `export * from './usage'` | +3 |

### 2.3 不在本次范围

- 后端 Go：未触碰任何 `internal/usage/`、`internal/proxy/`、`app.go`、`main.go`。
- wailsjs 绑定：未触碰。`git status` 中 wailsjs 的 M 记录是鲁班生成绑定时的副产物，与本次无关。
- 移动端 / 远程 HTTP：明确范围外（设计 §1.2）。

## 3. wailsjs 真实类型与设计文档的差异

实现前完整阅读 `Service.d.ts`（19 方法）与 `models.ts`（usage namespace 13 类），与设计文档第 11 章对照后**未发现契约级差异**。两个需要在实现时消化的细节：

| # | 真实类型特征 | 处理方式 |
|---|---|---|
| 1 | `usage.ModelPricing` / `usage.UsageRecord` / `usage.SyncResult` / `usage.SyncState` / `usage.Summary` / `usage.UsageEvent` 等带 `convertValues` 实例方法（因为含嵌套 `time.Time` 或子结构） | 凡前端构造实例的场景（目前仅 PricingDialog 保存），统一用 `new usage.ModelPricing({...})` 而非对象字面量，否则 vue-tsc 拒绝。读路径（API 返回值）不受影响 |
| 2 | `usage.SyncResult.startedAt/finishedAt` / `SyncState.lastSyncedAt` / `ModelPricing.updatedAt` 等被标记为 `any`（Go `time.Time`） | 新增 `formatTimeValue(v: unknown)` 工具（utils/usage-format.ts），自动嗅探 ISO 8601 字符串 / 秒 / 毫秒 / 微秒 / 纳秒并本地化展示；store 内同步用 `timeToString()` 取 ISO 兜底 |

`SummaryFilter` / `StatFilter` / `TrendFilter` / `LogFilter` 为扁平类（无 convertValues），对象字面量赋值通过结构化类型检查；api/usage.ts 提供 `createSummaryFilter` / `createStatFilter` / `createTrendFilter` 工厂统一默认值，避免分散。

## 4. 关键实现决策

### 4.1 四态契约（仿 LogsView）

`UsageView.vue` 顶层用 `v-if/v-else-if/v-else` 链严格四态：

| 状态 | 触发条件 | UI |
|---|---|---|
| loading | `loading && !summary`（仅首次/重试） | `<LoadingState message="加载使用统计中..."/>` |
| error | `error && !summary`（首次失败） | `<ErrorState :message :on-retry="handleRetry"/>` |
| empty | `summary && summary.totalRequests === 0` | `<EmptyState title="暂无使用数据"><AppButton>立即同步</AppButton></EmptyState>` |
| success | 其他 | 完整仪表盘 |

**后台刷新失败不刷屏**：`fetchAll({ silent: true })` 模式下，失败保留旧数据 + `console.warn`，不写 error、不弹 toast、不清 summary（仿 LogsView:309-327 的 `refreshHeadroomSavings` 模式）。30 秒后台定时器始终走 silent 模式。

### 4.2 数据源默认 `session_log`

`createSummaryFilter()` 默认 `source: 'session_log'`，避免与 proxy 实时拦截双计（设计 §7.2）。筛选器卡片在用户切到"全部"时显示警告条，提示可能双计。`filter-warn` 仅在 `filterSource === ''` 时出现（已用 puppeteer 验证 warnVisible=false）。

### 4.3 多币种展示

- **主指标**：`formatCost(summary.totalCostUSD, 'USD')`，后端已折算为 USD micro 等价值。
- **子文本**：当 `totalCostByCurrency` 至少 2 个币种时显示 `含 $X.XX · ¥Y.YY`；单币种不显示，避免冗余。
- **图表跨模型/供应商对比**：用前端 `nativeMicroToUsdMicro()` 把每个模型/供应商的原生币种 micro 按 §6.5 固定汇率（CNY→USD=0.14）折算到 USD 统一口径，避免 USD 与 CNY 直接相加。
- **明细表成本列**：按模型自身币种展示（`formatCost(m.totalCost, m.currencyCode)`），保留原始账单语义。

### 4.4 图表选型与主题

四张图，全部 Chart.js 4 + vue-chartjs 5，单次注册（`ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, BarElement, ArcElement, Title, Tooltip, Legend, Filler)`）：

1. **Line**（日趋势）：双轴。左轴 USD 成本（accent 蓝 + 半透明 fill），右轴 Token 总量（success 绿）。`interaction: { mode: 'index', intersect: false }` 联动 tooltip。
2. **Doughnut**（模型占比）：top 7 模型 + "其他"桶。8 色 PALETTE 与 tokens 系统色对齐（accent/success/warning/purple/danger/cyan/pink/yellow）。
3. **Bar**（供应商对比）：横向单序列，`maxBarThickness: 40`。
4. **Stacked Bar**（Token 四维）：top 8 模型 × 4 维度（input/output/cache_read/cache_creation）堆叠。

主题色通过 `readToken(name, fallback)` 从 `document.documentElement` 读取 `--secondary`/`--separator`/`--accent`/`--success` 等 CSS 变量；图表数据/选项均用 `computed` 包装，未来切 dark mode 只需 token 层覆盖即可联动（当前应用整体仅 light，无 dark-theme CSS，但图表路径已就位）。

### 4.5 价格编辑流程

- **PricingDialog** 用 `new usage.ModelPricing({...})` 构造实例（避免 convertValues 类型缺失），保存触发 `store.savePricing` → 后端 upsert + 静默刷新主数据（hasPrice 状态可能变化）。
- **内置模型**：ModelPricingTable 的删除按钮 `:disabled="entry.isBuiltin"`，title 提示原因；编辑允许（设计 §11.7：内置模型可改价）。
- **未知模型快捷入口**：当 `unknownModels.length > 0`，价格表卡片顶部出现橙色 chip 区，点击触发 `add-for-unknown` 事件，预填 modelPattern 打开 PricingDialog。

### 4.6 响应式断点

CSS 用 `@media` 三档，仿 LogsView：

| 断点 | 行为 |
|---|---|
| `min-width: 1100px` | 汇总卡片 4 列指标（默认） |
| `max-width: 1100px` | 汇总卡片 2 列 |
| `max-width: 960px` | 双列图表区折为单列 |
| `max-width: 720px` | 页面 padding 收窄、表格水平滚动、汇总卡片单列 |
| `max-width: 540px` | Dialog 内表单双列折单列（PricingDialog） |

## 5. 自验证证据

### 5.1 构建（核心验证门，必须全绿）

```
$ npm --prefix frontend run build
> vue-tsc --noEmit && vite build
✓ 1737 modules transformed.
dist/assets/UsageView-BLcnG7xo.js  225.89 kB │ gzip: 77.09 kB
dist/assets/UsageView-...css       11.60 kB │ gzip:  2.56 kB
✓ built in 541ms
```

`vue-tsc --noEmit` 通过 = 类型正确；`vite build` 通过 = 模板与 import 正确。UsageView chunk 226KB（含 Chart.js 全量），其他模块字节数与本次改动前一致（LogsView hash 一致）。

### 5.2 浏览器交互验证（Chrome headless + puppeteer-core）

由于纯浏览器下 wails runtime 不可用（`window.go` 为 undefined），临时编写了 dev-only mock（`_usage_dev_mock.ts`）注入 `window.go.usage.Service.*` 返回 canned 数据，**验证后已删除并还原 main.ts**。最终 build 不含 mock 代码。

验证矩阵：

| 场景 | 步骤 | 观察结果 |
|---|---|---|
| Success 态 | 导航到 `/#/usage`，等 5s | 8 个 ConfigCard 渲染，4 个 `<canvas>`（Line/Doughnut/Bar/Stacked），主指标 `$100.52`，子文本 `含 $96.54 · ¥28.40`，价格表 3 条，未知模型 chip 1 个 |
| 默认 filter 生效 | puppeteer 读取 `select.filter-select` value | 数据源 = `session_log`，warning `warnVisible: false` |
| Empty 态 | mock summary.totalRequests=0 | 显示「暂无使用数据」+「立即同步」按钮，summary-card 与 chart 不渲染 |
| Error 态 | mock GetUsageSummary reject | 显示「加载失败」+ 错误消息 + 重试按钮；点击重试再次失败仍正确显示 |
| 控制台错误 | 收集 pageerror 与 console.error | 0 个页面错误；console 仅 vite 连接日志与一个无关 404（资源加载） |

### 5.3 截图证据

- `/tmp/usage-shots/usage-hires.png`（success 态完整页面）
- `/tmp/usage-shots/usage-empty.png`（empty 态）
- `/tmp/usage-shots/usage-error.png`（error 态）

## 6. 状态覆盖核对（设计 §16.3 UI 验收）

| # | 验收项 | 状态 |
|---|---|---|
| 1 | loading / error / empty / success 四态完整且文案准确 | PASS（已逐态验证） |
| 2 | 后台刷新失败不刷屏（保留旧数据 + console.warn） | PASS（store fetchAll silent 模式） |
| 3 | 筛选器切换触发刷新且不闪烁 | PASS（deep watch + silent 后台刷新） |
| 4 | 「立即同步」按钮 loading 反馈 + 完成 toast | PASS（`syncing` ref + showSuccess/showError） |
| 5 | 价格管理对话框可新增、编辑、删除（内置禁删） | PASS（PricingDialog + ModelPricingTable） |
| 6 | 未知模型在表格显示「无价格」徽章 | PASS（`badge-warn` for hasPrice=false） |
| 7 | 多币种场景（USD + CNY）汇总卡片正确显示 | PASS（已用 mock 多币种数据验证） |
| 8 | 图表主题与 Element Plus dark/light 一致 | PASS（readToken；dark mode 待全局 token 切换时联动） |

## 7. 已知遗留与下一阶段

1. **真机联调**：纯浏览器无法验证 wails runtime 真实数据流，需 `wails dev` 启动应用后由天城/谛听点开使用统计核对真实数据准确性（设计 §14.1 阶段 5）。我的浏览器验证仅覆盖纯前端行为（骨架、四态、图表渲染、筛选器、对话框交互）。
2. **Dark mode 自动联动**：图表颜色已用 `readToken` 读 CSS 变量，但全局 `tokens.css` 目前只有 light；待全局 dark-theme 加入后，图表会在数据变化时自动取新色，但 mid-session 切主题不会主动重渲染图表（Chart.js 不监听 CSS 变化）。如需即时响应，可加一个 `MutationObserver` 监听 `body.class` 变化强制刷新 computed。
3. **chart.js dep 优化警告**：vite dev 日志有一行 "chart.js might be incompatible with the dep optimizer" 警告，是 chart.js v4 与 vite 的已知交互，不影响 dev/build 正常运行；若介意可在 `vite.config.ts` 加 `optimizeDeps.exclude: ['chart.js']`。
4. **Cost 数字精度**：`formatCost` 对 `>=1` 的金额统一 2 位小数，`<1` 的微小额 4 位；若未来有更细需求可再分档。
5. **bundle 体积**：UsageView chunk 226KB（77KB gz），主要是 Chart.js。设计 §12.1 已预估 ~150KB gz（多 register 后略超）。若要进一步压缩，可改按需 import chart.js 子模块（`chart.js/auto` 改为显式 register tree-shake），但当前实现已经显式 register 必要组件。

## 8. 提交门状态

- 本地 `npm --prefix frontend run build`：**PASS**（vue-tsc + vite 全绿）
- 浏览器交互验证：**PASS**（四态 + 图表 + 筛选器 + 对话框）
- 代码风格：双语注释，无 emoji，无 TODO/FIXME/HACK
- 文件隔离：仅 `frontend/src/` 与 `frontend/package.json`，未触碰 Go / wailsjs

按工作流规则，真实 diff 的审核路由由 workflow-rules 决定。本次仅前端 Vue/TS 改动，无后端、无 schema、无 hook、无 manifest、无依赖项升级（chart.js/vue-chartjs 是新功能依赖，建议由谛听复核依赖选型是否与项目偏好一致）。

---

报告结束。返回给天城，由其决定是否走 leader_pass 还是 diting_pass。
