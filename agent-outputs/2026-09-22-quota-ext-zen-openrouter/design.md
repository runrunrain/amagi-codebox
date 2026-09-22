# 额度查询拓展 · OpenCode Zen Go 与 OpenRouter · 需求分析与实现方案

> 日期 2026-09-22 · Leader 调研定稿（端点契约全部实弹实证，无逆向臆测）
> 上游设计契约：`/Users/maorun/maorun-workpace/Database/01-AI技术与工具/codebox优化/正式化改造/额度管理/额度查询与常显设计方案.md`（本次为其家族拓展，不改其 §5–§8 展示契约）
> 实证样本：主上 Provider Center 真实 provider `opencode-go`（baseURL `https://opencode.ai/zen/go/v1`）与 `openrouter`（baseURL `https://openrouter.ai/api/v1`）；探测全程 GET、凭据仅内存拼装不落日志。

---

## 1. 需求与范围

现有额度查询支持三家族（GLM bigmodel/z.ai、DeepSeek、Codex 订阅本地），两家新服务在
Provider Center 已配置但被判 `unsupported`（实证：`models.json` 的 `provider_quota`
中两键 status=unsupported）。目标：把两家纳入同一探测/缓存/展示契约。

**非目标**：中转站/反代 baseURL 的额度查询（判别按官方域特征，中转域名落 unsupported，
符合「仅发往 provider 自身 baseURL 同源端点」安全边界）；汇率换算；mobile 适配；
OpenRouter free_model_daily_requests/usage_daily 等附加窗口（Phase-1 有意裁剪，见 §3）。

## 2. 端点契约（全部实弹实证）

### 2.1 OpenCode Zen Go

| 项 | 实证结论 |
|---|---|
| 端点 | `GET https://opencode.ai/zen/go/v1/usage`（无效路径 `/v1/credits`、`/v1/quota` 等均 404；`/usage` 401→真路由） |
| 鉴权 | `Authorization: Bearer <key>` |
| 200 响应 | `{"usage":{"rolling":{"status":"ok","percent":<int>,"resetsAt":"<ISO8601>"},"weekly":{…},"monthly":{…}}}` |
| 语义（上游源码定谳） | `percent` = **usagePercent 已用%**（`floor(min(100, usage/limit*100))`）；`status ∈ {"ok","rate-limited"}`（用尽）；`resetsAt` = 窗口重置时刻 |
| 错误形态 | 401 `{"type":"error","error":{"type":"AuthError","message":"Unauthorized"}}`（无效/缺失 key）；403 `EntitlementError "OpenCode Go subscription required."`（key 有效未订 Go 套餐） |

语义证据：`https://github.com/anomalyco/opencode` 仓库
`packages/console/app/src/routes/zen/go/v1/usage.ts`（`formatUsage({status:"ok"|"rate-limited",
resetInSec, usagePercent})`）与 `packages/console/core/src/subscription.ts`
（`analyzeRollingUsage/analyzeWeeklyUsage/analyzeMonthlyUsage`）。
补充事实：`GET /zen/go/v1/models` 无需鉴权即 200（模型表），无余额/credits 类端点。

### 2.2 OpenRouter

| 项 | 实证结论 |
|---|---|
| 主端点 | `GET https://openrouter.ai/api/v1/key`（官方现行文档「Checking your limits」指定端点；旧 `/auth/key` 已不提） |
| 200 响应（实弹） | `{"data":{"label","is_management_key","is_provisioning_key","limit":null\|num,"limit_reset":null\|str,"limit_remaining":null\|num,"include_byok_in_limit","usage":num,"usage_daily","usage_weekly","usage_monthly","byok_usage*","is_free_tier":bool,"expires_at","creator_user_id","organization_id","workspace_id","allowed_data_regions","free_model_daily_requests":{"used","limit","remaining"},"rate_limit":{…已废弃，忽略}}}` |
| 补端点 | `GET /api/v1/credits` → `{"data":{"total_credits":num,"total_usage":num}}`（文档标注 Management key，实弹普通 key 200 可用） |
| 语义 | `usage` = 累计已用 credits；`limit/limit_remaining` = **per-key 额度上限**（非账户余额，null=无限）；`total_credits` = 累计购入，`total_usage` = 累计已用 → **账户剩余 = total_credits − total_usage** |
| 错误形态 | 无效 key → 401 `{"error":{"message":"User not found.","code":401}}` |

## 3. 数据建模决策（拍板）

### 3.1 OpenCode Zen（family `opencode-zen`）→ 窗口型，3 窗口

- `Windows` 三条：rolling→`kind:"primary"`、weekly→`kind:"secondary"`、monthly→`kind:"tertiary"`（**Kind 值域新增 tertiary**）。
- `Label`：`"滚动"` / `"周"` / `"月"`（前端 windowLabel 拼「 窗口」，windowShortLabel 直用；rolling 时长服务端不下发，**不硬造 window_minutes**）。
- `UsedPercent` = percent（语义与现有 used_percent 一致）；`ResetsAt` = ISO8601 → unix 秒；`Remaining` 不填（无绝对分母，不硬造）。
- `status:"rate-limited"` 的窗口不加新字段——其 percent 恒 100，现有 danger 分档（>90）自然标红，零模型膨胀。
- 状态分类：200→ok；401→no_key；403→no_plan（EntitlementError）；其它 HTTP/网络/JSON 失败→error。
- `Source` = `"opencode-zen-api"`；`Level` 不填（无档位信息）。
- 端点推导：baseURL 归一（剥尾 `/chat/completions`、去尾 `/`；不以 `/v1` 结尾则补 `/v1`）+ `/usage`——
  对 `.../zen/go/v1` 与 `.../zen/go` 两种配置都落到 `.../zen/go/v1/usage`，对未来非 go 的 zen plan 路径自适应。

### 3.2 OpenRouter（family `openrouter`）→ 余额型

- `Balance`：`Currency:"USD"`、`Total` = total_credits、`Used` = total_usage、`Remaining` = total_credits − total_usage。
- **QuotaBalance 扩展**：`Used *float64`、`Remaining *float64`（json `used`/`remaining`，`omitempty`）——指针表达「字段存在性」，
  `Remaining=0`（真花光）不因 omitempty 失踪；DeepSeek 旧条目不填这两字段，展示走既有分支不受影响。
- 主跳 `/credits`；**降级跳**仅当主跳非 200 时打 `/key`：`Used=usage`；`limit_remaining != null` 时 `Remaining=limit_remaining`、`Total=limit`，
  并以 `Message` 注明「余额端点不可用，按 Key 额度上限口径」（诚实标注口径，不冒充余额）。
  主/降级都失败 → error（沿用「未决不覆盖 ok 缓存」）。
- 状态分类：200→ok；401/403→no_key；其它→error。`Source` = `"openrouter-api"`。
- **有意裁剪（Phase-1）**：`usage_daily/weekly/monthly`（无分母，做进度条=造假）、`free_model_daily_requests`、`rate_limit`
  均不进模型；留待后续按需扩展。`is_free_tier` 不入 Level（Level 语义=套餐档位，OpenRouter 无档位）。

### 3.3 家族判别（DetectQuotaFamily 扩展）

- `opencode-zen`：host == `opencode.ai` 且路径含 `/zen`；origin/base 归一见 §3.1。
- `openrouter`：host == `openrouter.ai`；base 归一（剥 `/chat/completions`、去尾 `/`、不以 `/api/v1` 结尾则补）。
- host 匹配一律小写比较（沿用现有归一纪律）。

## 4. 后端改点清单

| 文件 | 改动 |
|---|---|
| `internal/config/types.go` | family 常量 +2（`QuotaFamilyOpenCodeZen`/`QuotaFamilyOpenRouter`）；`QuotaWindow.Kind` 值域注释 + `tertiary`、Windows 注释 1–3 条；`QuotaBalance` +Used/Remaining 指针；`ProviderQuotaEntry.Source` 值域注释 +2 |
| `internal/config/quota_probe.go` | `DetectQuotaFamily`（现判别函数，以实际函数名为准）+2 家族及端点推导；新增 `ProbeOpenCodeZenQuota`、`ProbeOpenRouterQuota`（形态纪律同现有：client/baseURL/key 入参、不依赖 secrets、httptest 可测）；`cloneProviderQuotaEntry` 的 Balance 深拷贝覆盖新指针字段 |
| `app_quota_probe.go` | `executeQuotaProbe` dispatch +2 case；来源文案映射如有 +2 |
| `internal/config/quota_probe_test.go` | 判别表扩充；zen：ok 三窗口映射（Label/Kind/ResetsAt/UsedPercent）、rate-limited 窗口、401→no_key、403→no_plan、坏 JSON→error；openrouter：/credits ok 映射（含 Remaining 计算与指针存在性）、/key 降级（limit_remaining null/非 null 两态）、401→no_key、主跳失败降级失败→error |
| `app_quota_probe_test.go` | 按现有形态补两家族 dispatch/来源断言（如现有测试结构适用） |

不改：`RecordProviderQuota` 未决语义、单飞/冷却/超时（10s）、`bind_list.go`（无新绑定面）。

## 5. 前端改点清单

| 文件 | 改动 |
|---|---|
| `frontend/src/components/usage/quotaModel.ts` | 家族常量 +2 进 `SUPPORTED_QUOTA_FAMILIES`；`familyDisplayName`（'OpenCode Zen'/'OpenRouter'）；`familyShortName`（'Zen'/'OpenRouter'）；`SOURCE_LABELS` +2（均 'API'）；`tertiaryWindowOf`；`formatBalanceAmount` 扩展：`balance.remaining != null` 时 headline 显示剩余金额，新增明细文案（「已用 X / 共 Y」，沿用现有币种排版规则 CNY→¥、其余币种码，**不扩汇率/前缀映射**）；`grayCardText` 的 no_plan 文案通用化（「该 Key 未开通对应套餐」——注意同步更新既有测试断言） |
| `frontend/src/components/usage/QuotaCard.vue` | 窗口模板支持第三窗口（zen 三条渲染）；余额分支：`remaining != null` 时大数字=剩余 + 明细行（复用 `.q-balance-line`），否则走现有 DeepSeek 排版 |
| `frontend/src/components/usage/QuotaPanel.vue` | 新家族单卡排入（GLM 双通道特判不扩）；聚合/分组逻辑核对 |
| `frontend/src/components/terminal/SessionQuotaStrip.vue`（及 WebPlaneQuotaStrip 如需） | 核对摘要分支：zen 走窗口摘要（短标签「滚动」）、openrouter 走余额摘要（显示剩余），无需新分支则不动 |
| `frontend/wailsjs/go/models.ts` | **生成物，禁止手编**——Go struct 变更后用 wails CLI 再生（`wails generate module`，第一步先验证本机 wails 可用；这是本任务首要风险点，若 wails 不可用立即上报，不得手改绕过） |

测试：`frontend/src/__tests__/components/usage/`（quotaModel/QuotaCard/QuotaBar 相关既有用例更新 + 新家族用例：
zen 三窗口渲染与 30%/70%/90 分档沿用、openrouter 剩余/已用/共排版、remaining=0 存在性、familyDisplayName/shortName 映射）。

## 6. 验收标准

1. `go vet ./...` 通过；`go test ./... -count=1` 全绿（重点 internal/config 与根包）。
2. `npm --prefix frontend run build`（vue-tsc 门禁 + vite）通过；`npm --prefix frontend run test`（vitest）全绿。
3. 既有三家族行为零变化（现有测试守护，不得改既有断言语义——唯一例外：grayCardText no_plan 文案通用化同步改断言）。
4. 真实端点冒烟由 Leader 验收阶段执行（主上 Provider Center 真实 provider 探测），不进 CI。

## 7. 风险与边界

- **wailsjs 再生**（§5）：无 wails CLI 则前端类型面无法更新——先验证，失败即停并上报。
- **端点推导**：baseURL 归一必须覆盖带/不带 `/v1`、带 `/chat/completions` 尾缀的形态（repo 已有同类先例：launcher 的
  opencode_config 测试见 `internal/launcher/opencode_config_test.go:2665-2805`）。
- **指针字段序列化**：`Used/Remaining` 用指针 + omitempty，`cloneProviderQuotaEntry` 必须解引用拷贝，防止并发写穿。
- **零假数据**：无分母不画进度条、无信息不填字段、降级口径用 Message 标注——任何「看起来完整」的造数都不接受。
