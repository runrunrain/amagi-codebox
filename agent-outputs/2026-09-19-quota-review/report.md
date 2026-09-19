# 额度查询与常显 · 合并代码审核报告（P1 后端 + P2 前端）

> 审核：谛听（diting）· 2026-09-19 · 工作树未提交改动（Toast.vue 右下角迁移不在范围）
> 设计契约：`/Users/maorun/maorun-workpace/Database/01-AI技术与工具/codebox优化/正式化改造/额度管理/额度查询与常显设计方案.md`
> 已知验证（采信）：go vet/test 41 包过 · vue-tsc 0 error · vitest 153 用例过 · vite build 过

---

## FINDINGS

### Major-1 · GLM z.ai 通道家族标签丢失，双通道卡片契约被破坏

- **severity**: Major
- **path**: `internal/config/quota_probe.go:179-262`（`ProbeGLMQuota` 全部 Entry 字面量与 `quotaErrorEntry` 调用硬编码 `Family: QuotaFamilyGLMBigmodel`，见 :196/:205/:208/:213 等）；`app_quota_probe.go:154`（`case QuotaFamilyGLMBigmodel, QuotaFamilyGLMZai:` 共用同一函数且返回后不回填 family）
- **触发条件**: 任一 provider 的 baseURL 含 `api.z.ai`（`DetectQuotaFamily` 正确判为 `glm-zai`、origin 正确取 `https://api.z.ai`），执行探测。
- **实际影响**: 落盘 `provider_quota` 条目 `family` 恒为 `glm-bigmodel`。前端 `QuotaPanel.vue` 的「GLM 双通道各一卡」（bigmodel / z.ai，`quotaModel.familySubLabel`）失效——z.ai 卡永不渲染（`QUOTA_FAMILY_GLM_ZAI` 桶内无条目）；若用户同时配置 bigmodel + z.ai 两家，`latestOfFamily('glm-bigmodel')` 会把两家按 `probed_at` 混挑一张展示，副标恒为「bigmodel」，z.ai 额度被错误标注通道。违反设计 §3（Family 值域 `glm-bigmodel | glm-zai`）与 §6 卡片结构。
- **证据**: grep 确认 `QuotaFamilyGLMZai` 在 quota_probe.go 仅出现于 `DetectQuotaFamily`（:135/:141/:143），无任何 Entry 携带该值；`quota_probe_test.go` 的 `TestDetectQuotaFamily` 只测判别函数，无「z.ai → 经 ProbeGLMQuota → entry.family=glm-zai」的断言（探测测试全部直连 httptest URL，未经过家族路由）。
- **最小修复方向**: `executeQuotaProbe` 在 `ProbeGLMQuota` 返回后按已判别的 `family` 回填 `entry.Family`（或给 `ProbeGLMQuota` 增加 family 参数贯穿所有分支）；补一条 z.ai 家族标签的回归测试。

### Minor-1 · strip 首次探测期间误显「额度不可查」（「查询中」分支实际不可达）

- **severity**: Minor
- **path**: `frontend/src/components/terminal/WebPlaneQuotaStrip.vue:88-92` × `frontend/src/stores/quota.ts:119-131`
- **触发条件**: GLM/DeepSeek provider 首次挂载 strip，`ensure()` 探测进行中（≤10s）。
- **实际影响**: 「额度查询中…」分支要求 `!entry`，但 `quotaFor` 对非 codex 的无缓存 provider 返回 `unsupported` 占位（非 null），故该分支只对名为 `codex` 的 provider 生效；其余家族首探期间显示禁用态灰字「额度不可查」，探测完成后才切数据。误导性瞬态（弹层内同理：占位灰卡 + spinner）。
- **证据**: `quotaFor` 实现（`if (direct) return direct; if (name === CODEX_QUOTA_KEY) return null; return createFrom({status:'unsupported'…})`）；WebPlaneQuotaStrip `summary` 的 probing 特判在占位条目下被短路。
- **最小修复方向**: `summary` 计算先判 `probing`（有无占位均显示查询中），或 `quotaFor` 在 in-flight 时返回 null。

### Minor-2 · Popover 定位硬编码 320px 高度假设

- **severity**: Minor
- **path**: `frontend/src/components/terminal/WebPlaneQuotaStrip.vue:111-119`（`rect.top - 8 - 320`）
- **触发条件**: compact QuotaCard 内容高于 320px（双窗口 + 档位 + credits 徽标 + stale 标注 + 卡脚 + 弹层 footer 的组合）。
- **实际影响**: 弹层按固定 320px 向上预留、实际向下生长，超高时压住锚点按钮/strip 本身；不随内容测量，也不随滚动重算（仅 open/resize）。
- **最小修复方向**: 打开后 `nextTick` 量 `popoverRef.offsetHeight` 再定位，或改 bottom 锚定。

### Minor-3 · RemoteScopeBanner subject 不随子页切换

- **severity**: Minor
- **path**: `frontend/src/views/UsageView.vue:26`（固定 `subject="使用统计"`）
- **触发条件**: 远程客户端模式下进入 `/usage/quota`。
- **实际影响**: 提示文案仍称「使用统计为本机功能，不适用于远程主机」，页名与实际子页（额度查询）不符。仅远程模式可见，影响低。
- **最小修复方向**: `:subject="pageTitle"`。

### Minor-4 · stats↔quota 页切换卸载子页，筛选/图表状态丢失

- **severity**: Minor
- **path**: `frontend/src/views/UsageView.vue:38`（`<router-view />` 无 KeepAlive）
- **触发条件**: 在额度查询页与使用统计页之间来回切换。
- **实际影响**: `UsageStatsPanel` 每次切换被卸载重建，统计区间/客户端/数据源/图表视图选择全部重置（原 832 行单页无此行为；30s 定时器在 `onUnmounted` 正确清理，无泄漏）。与设计 §5「纯搬运降低回归面」意图相悖。
- **最小修复方向**: `<router-view v-slot>` + `KeepAlive include="UsageStatsPanel"`，或筛选状态下沉 usage store。

### Minor-5 · Web 平面 strip 状态机缺组件级测试

- **severity**: Minor
- **path**: `frontend/src/__tests__/components/usage/`（无 WebPlaneQuotaStrip 测试）
- **触发条件**: 设计 §9 验收项「Web 平面 strip 状态机（有数据/无数据/不支持/刷新中）」。
- **实际影响**: 现仅 `QuotaCard.test.ts` 覆盖 `quotaStripSummary` 纯函数（有数据/不支持/error/null 四态），「查询中」「刷新中 spinner」「Popover 点外关闭/Escape/Teleport 卸载清理」无任何测试。node 无 DOM 的环境限制已知，但 popover 关闭判定与状态机可抽纯函数覆盖。
- **最小修复方向**: 抽离 popover 可见性状态机/外点判定为纯函数补测。

### Minor-6 · GLM secondary 窗口可产出 >1 条，UI 静默丢弃

- **severity**: Minor
- **path**: `internal/config/quota_probe.go`（`glmQuotaWindows` 把全部非 TIME_LIMIT 且带数值的条目收进 secondary）× `frontend/src/components/usage/QuotaCard.vue`（只渲染首个 secondary）
- **触发条件**: GLM 下发多条带 percentage/remaining 的非 TIME_LIMIT 限额。
- **实际影响**: 数据模型 Windows 可能 >2 条（设计 §3 说 1–2 条），多余 secondary 在卡片上静默丢弃。无害偏差，但与契约文字不一致。
- **最小修复方向**: 后端截断 secondary 至 1 条，或注释明确「多条仅取首条展示」。

---

## 逐项验收结论（对应任务 6 个关注点）

1. **凭据安全 — PASS**：key 仅探测瞬间进请求头（GLM 原样 `Authorization`、DeepSeek `Bearer`，测试断言了头形态）；`client.Do` 错误消息只含 URL 不含请求头，Go stdlib 跨 host 重定向自动剥 Authorization；`ProviderQuotaEntry` 值域无凭据字段（types.go:470-499），落盘无需 scrub；日志只记 provider/family/status（commitQuotaProbe）；前端 store/api 全链无 key 流转；Codex 路径仅 `WalkDir` 过滤 `rollout-*.jsonl`、`SkipDir archived_sessions`，无任何 `auth.json` 触碰。
2. **探测契约保真 — PASS 除 Major-1**：GLM 四态映射与 §4 一致（HTTP 401/403 与 code=1001→no_key；code!=200/msg 关键词→no_plan；网络/解析→error；空 limits 防空 ok）；TIME_LIMIT→primary 优先且数值宽松解析（flexNumber 数字/字符串/null）；DeepSeek 字符串数字解析 + is_available=false 标注；Codex 尾部 256KB 倒序取最新 token_count、跨文件 mtime 回退、ProbedAt=事件时间戳，均有真实 fixture 测试。
3. **并发正确性 — PASS**：单飞为「等待复用」变体（`f.entry` 先写、`close(done)` 后唤醒，happens-before 正确，槽位用后释放）；`probeAllQuotas` WaitGroup 的 Add 均在主协程循环内先于 Wait；`RecordProviderQuota` 与 `SaveProvider`/`Save` 同走 `s.mu` 写锁 + `saveLocked`（service.go:347-357/861+），models.json 落盘无竞态；error 不覆盖 ok 在锁内判定。
4. **前端重构回归面 — 基本完整（Minor-3/4）**：图表四视图（trend/model/provider/tokens）、区间/客户端/数据源/供应商筛选、价格表 + PricingDialog、30s 静默刷新（onUnmounted 清理）、Empty/Loading/Error 全部在 UsageStatsPanel（782 行）；`PageHead.vue` 本无 actions slot（旧 `#actions` 确为死代码），新 `.head-actions` 以 flex space-between 右置，720px 断点降级堆叠，无布局回归；SidebarNormal 前缀匹配 `path + '/'` 且 `/` 排除前缀逻辑、六个顶级路径互不为前缀，无误高亮。
5. **WebPlaneQuotaStrip — PASS 除 Minor-1/2**：iframe `flex:1 + min-height:0`、strip `flex:0 0 30px` 都在 `web-plane-host`（absolute inset:0）内部，xterm 的 term-body 高度链路不受影响；Popover 点外关闭（pointerdown+mousedown capture、锚点/弹层豁免）+ Escape + watch(false)/onBeforeUnmount 双清理齐备；ended 态取舍（ended-bar 为右上浮动胶囊、strip 常驻保留缓存额度）有注释论证，避免双条堆叠的设计意图达成。
6. **测试真实性 — 总体真实（缺口见 Minor-5、Major-1）**：Go 侧 httptest 断言鉴权头、>300KB 尾部截断 fixture、models.json 往返/深拷贝/error 不覆盖均为实打实断言；前端阈值边界（70/90/90.1/NaN/clamp）与六态文案矩阵真断言，非空跑。

## 覆盖范围与盲区

- 覆盖：任务列出的全部后端/前端/wailsjs 文件与 app.go 挂载点、bind_manifest_test.go 注入面清单、三处 Go 测试与三处前端测试、设计契约全文对照。
- 未覆盖/盲区：无 bash 环境，未亲跑 git diff/go test/vitest（采信任务给出的验证结果）；旧版 UsageView 832 行原文未取（以功能清单逐项在新面板核实替代）；GLM `percentage` 字段的服务端语义（已用/剩余）无法离线实证，按设计文档自洽采信；Popover 视觉表现与三家真实端点的手测不在静态审核能力内。
- 验证可信度：高（代码级证据充分，Major-1 有确定性复现路径）。

## VERDICT: CONCERNS

1 个 Major（z.ai 家族标签，建议修复后做一次增量复审）+ 6 个 Minor（不阻断合并，可随下个迭代处理）。无 Critical：凭据安全、并发、Codex 本地读路径均符合契约。
