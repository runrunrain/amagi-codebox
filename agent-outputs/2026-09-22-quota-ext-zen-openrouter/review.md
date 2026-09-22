# 额度查询拓展（OpenCode Zen / OpenRouter）· 阶段 diff 快审（quick）

> 谛听 · 2026-09-22 · 范围：任务书所列工作树 diff + 新文件（HEAD=854693e 未提交）
> 契约：`agent-outputs/2026-09-22-quota-ext-zen-openrouter/design.md`
> 已采信（未重跑）：go vet / go test 41 包 / vue-tsc build / vitest 174 / 双真实端点冒烟。

## VERDICT: PASS_WITH_MINOR

2 条 Minor，无 Critical/Major。设计决策无实质漂移，零假数据红线、既有三家族零变化、
并发深拷贝、安全边界（仅 GET / 同源 / Entry 无凭据）全部核过通过。

## Findings

### F-1｜Minor｜internal/config/quota_probe.go:172,174 — 家族判别为全 URL 子串匹配，未按 design §3.3 锚定 host

- **触发条件**：baseURL 的 host 内嵌官方域子串——如 `https://openrouter.aimirror.com`
  （host 前缀恰含 "openrouter.ai"）、`https://opencode.ai.relay.cn/zen`、
  `https://sub.bigmodel.cn.evil.tld`。`strings.Contains(lower, "openrouter.ai")` 对整条
  URL 小写串做匹配（lower 含 scheme+host+path），非 `u.Host` 锚定。
- **实际影响**：此类中转/仿冒域被误判为官方家族 → 探测 GET + Bearer key 发往该 host。
  key 仅发往用户自己配置的 provider baseURL 同源端点（该 host 本就持有此 key），**无凭据
  外溢扩大**；实际后果是该 host 收到一次 `/usage` 或 `/credits|/key` 请求、大概率非 200 →
  error 未决条目（不覆盖 ok 缓存），每轮自动探测重复发生。违背 design §1 非目标
  「中转域名落 unsupported」中「按官方域特征判别」的意图（该子集未落 unsupported）。
- **证据**：quota_probe.go:162-176（DetectQuotaFamily switch 全 Contains）；design.md §3.3
  「host == opencode.ai」「host == openrouter.ai」；quota_probe_test.go:391 近邻负例仅覆盖
  `openrouter-ai.example.com`（连字符阻断子串）形态，未覆盖内嵌子串形态。
- **最小修复方向**：`url.Parse` 后锚定 `u.Host` 判别（`== "opencode.ai"` /
  `== "openrouter.ai"` 或 `HasSuffix(".opencode.ai")` 等），至少覆盖两新家族；GLM/DeepSeek
  既有同款为历史行为，可列 follow-up，勿借本任务扩改伪装成回归修复。
- **定性**：静态路径证实，未动态复现（无命令工具）；危害上限为浪费请求 + 未决缓存抖动。

### F-2｜Minor｜frontend/src/components/usage/quotaModel.ts:362 — strip 余额摘要对「降级仅 used」形态退化为占位符

- **触发条件**：OpenRouter 主跳 `/credits` 非 200 且降级 `/key` 返回 `limit_remaining: null`
  （余额端点故障 + 该 key 无 per-key 上限）→ Balance 仅含 Used。
- **实际影响**：Web 平面常显 strip 摘要渲染为 `OpenRouter · —`（formatBalanceHeadline
  265-275 对该形态诚实返回 '—'），已有的 used 信息不呈现；额度卡面正常（headline '—' +
  明细「已用 USD X」，quotaModel.test.ts:126-129 锚定）。无假数据问题，纯信息利用率缺失。
- **证据**：quotaModel.ts:362 直接拼 `formatBalanceHeadline`；quotaModel.test.ts:108-110 仅
  断言卡面明细形态，strip 该形态无断言。
- **最小修复方向**：strip 余额分支在 headline 为 '—' 且 used 存在时改拼 `已用 <amount>`
  （复用 formatBalanceAmount）。

## 核对结论（按 quick 重点）

1. **设计一致性 ✓**：Zen 三窗口 rolling/weekly/monthly → primary/secondary/tertiary、Label
   「滚动/周/月」、percent=已用%、resetsAt ISO→unix、不造 WindowMin/Remaining、rate-limited
   复用 >90 danger 档（无模型膨胀）、401/403/其它状态矩阵、Source、Level 不填——全部与
   §3.1 一致。OpenRouter 主跳 /credits、仅主跳非 200 才打 /key、降级 Message 原文
   「余额端点不可用，按 Key 额度上限口径」、Used/Remaining 指针存在性（Remaining=0 不失踪，
   Go 测试 TestProbeOpenRouterQuota_RemainingZeroKept + 前端 remaining=0 用例双锚）——与 §3.2
   一致。端点推导（/v1、/api/v1、/chat/completions、/responses 尾缀归一）与测试表
   （quota_probe_test.go:369-402）一致；/responses 剥离为设计文本外加分项，有锚。
2. **零假数据 ✓**：余额分支无进度条（QuotaCard 无 QuotaBar 渲染）；无分母不造「共」
   （formatBalanceDetail 的 total>0 守卫）；zen 缺 percent 窗口跳过；降级口径 Message 诚实标注。
3. **既有三家族零变化 ✓**（按当前代码 + design 授权清单 + 既有测试全绿交叉判定，见盲区）：
   GLM/DeepSeek/Codex 探测函数与 DetectQuotaFamily 早返回分支行为不变；唯一授权行为改动
   grayCardText no_plan 通用化已按 §5 同步测试断言。
4. **并发安全 ✓**：cloneProviderQuotaEntry（quota_probe.go 末段）覆盖 Windows 切片 +
   Balance 结构体 + Used/Remaining 指针解引用，无共享写穿路径；RecordProviderQuota/
   GetProviderQuotaCache 锁语义不变。
5. **安全边界 ✓**：全程 http.MethodGet；探测端点由 provider 自身 baseURL 的 scheme://host
   派生（同源）；Entry 值域无凭据；日志仅 provider/family/status。（F-1 为判别宽松边界，
   不破坏同源底线。）

## 观察（不构成 finding）

- OpenRouter no_key 需两跳均 401/403（quota_probe.go ProbeOpenRouterQuota 尾部）——design
  §3.2 状态分类措辞未定谳 per-leg；实现偏保守（单跳 401 + 对端网络失败 → error 未决，不覆盖
  ok），方向安全，无后果。
- /credits 平铺形态兼容为 §2.2 data 包裹之外的宽松项，有测试锚定，无害。

## 盲区与验证可信度

- 本会话无命令执行工具，无法 byte 级 `git diff`：「既有三家族零变化」由当前代码通读 +
  design 授权改动清单 + 既有测试断言语义（含任务书采信的全绿结果）交叉判定，非逐行 diff 比对。
- vitest 为 node 环境纯逻辑测试，未做 DOM/视觉渲染复核；真实端点行为采信任务书冒烟记录。
- F-1/F-2 均为静态路径证实 + 触发条件可构造，未运行时复现（无命令工具）。
