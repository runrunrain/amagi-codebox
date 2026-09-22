# 额度查询拓展 · OpenCode Zen Go 与 OpenRouter · 实现报告

> 日期 2026-09-22 · 实现：Leader 直做（子 Agent 节点无命令执行能力，写码与 wails 生成/验证须同会话闭环；预检贡献由 luban 70f174da/05ed6551 提供并全部采纳）
> 契约：`design.md`（同目录，端点实证与建模决策）；基线 HEAD=854693e，改动未提交。

## 1. 改动清单

| 文件 | 改动 |
|---|---|
| `internal/config/types.go` | family 常量 +2（`opencode-zen`/`openrouter`）；`QuotaWindow.Kind` 值域 +`tertiary`；`QuotaBalance` +`Used/Remaining *float64`（指针表达存在性）；注释同步 |
| `internal/config/quota_probe.go` | Source 常量 +2；`QuotaWindowTertiary`；`DetectQuotaFamily` +2 家族判别与端点归一（`quotaBasePath`/`zenV1Path`/`openRouterAPIPath`）；`ProbeOpenCodeZenQuota`（GET `{base}/usage`，3 窗口映射）+ `ProbeOpenRouterQuota`（主跳 `/credits`、降级 `/key` 口径标注）；`cloneProviderQuotaEntry` 指针解引用深拷贝 |
| `app_quota_probe.go` | dispatch 抽出 `probeQuotaByFamily`（+2 case，可 httptest 直测）；`quotaSourceForFamily` +2 |
| `internal/config/quota_probe_test.go` | 判别表 +10 行；Zen ok 三窗口全量断言/缺省窗口跳过/状态矩阵；OpenRouter 主跳两形态/Remaining=0 存在/降级两口径/失败矩阵；双路径假端点 |
| `app_quota_probe_test.go` | `TestQuotaSourceForFamily` 全映射；`TestProbeQuotaByFamily_Dispatch` 分发归属 |
| `frontend/src/components/usage/quotaModel.ts` | 家族常量/集合/显示名/短名/来源 +2；`tertiaryWindowOf`；`formatBalanceHeadline`/`formatBalanceDetail`（降级口径不硬造总额）；no_plan 文案通用化「该 Key 未开通对应套餐」；strip 摘要余额口径走 headline |
| `frontend/src/components/usage/QuotaCard.vue` | 第三窗口渲染槽；余额明细行；headline 切换 |
| `frontend/src/components/usage/QuotaPanel.vue` | 新家族单卡排入；空态文案 |
| `frontend/src/__tests__/components/usage/quotaModel.test.ts`（新） | 家族映射/第三窗口/余额排版（remaining=0、降级口径）/strip 摘要口径 |
| `frontend/src/__tests__/components/usage/QuotaCard.test.ts` | no_plan 断言同步 |
| `frontend/wailsjs/go/models.ts` | `wails generate module` 再生（未手编） |

## 2. 验证结果（原文摘要）

- `go vet ./...`：通过（0 输出）。
- `go test ./... -count=1`：全 41 包 `ok`（含 internal/config 4.3s、根包 10.2s），exit=0。
- `npm --prefix frontend run build`：vue-tsc 门禁 0 error + vite `✓ built in 617ms`。
- `npm --prefix frontend test`：`Test Files 16 passed (16) / Tests 174 passed (174)`。

## 3. 真实端点冒烟（Leader 验收执行，临时探针已删，不进 CI）

凭据经 keychain 内存传递（全程未打印），GET 仅发往两家官方同源端点：

- Zen（`https://opencode.ai/zen/go/v1/usage`，真实 provider `opencode-go`）：`status=ok family=opencode-zen source=opencode-zen-api windows=3`
  - `kind=primary label=滚动 used=12.0`；`kind=secondary label=周 used=4.0`；`kind=tertiary label=月 used=76.0`，resetsAt 均转换成功。
- OpenRouter（`https://openrouter.ai/api/v1/credits`，真实 provider `openrouter`）：`status=ok family=openrouter`
  - `currency=USD total=30.00 used=9.86 remaining=20.14`（差值自洽；未触发降级跳）。

## 4. 有意裁剪与遗留项

- OpenRouter `usage_daily/weekly/monthly`、`free_model_daily_requests`、`rate_limit`：无分母/非主诉求，Phase-1 不入模型（design §3.2）。
- Zen `status:"rate-limited"` 不加窗口状态字段：percent 恒 100，现有 danger 分档自然标红。
- 中转站/反代 baseURL 不判为新家族（仅官方域特征），落 unsupported——安全边界「仅发往 provider 自身 baseURL 同源端点」的既定语义。
- 无功能遗留；变更未提交（遵守「不自行提交」），待主上裁决。

## 5. 谛听复审与修复记录（quick，VERDICT: PASS_WITH_MINOR）

复审报告：同目录 `review.md`（2 Minor，无 Critical/Major）。按 findings 分流规则由 Leader 直修：

- **F-1（Minor）家族判别 host 锚定**：`DetectQuotaFamily` 新家族判别从全 URL 子串匹配改为
  `u.Host` 锚定（`host==opencode.ai` 且路径含 `/zen` / `host==openrouter.ai`），防
  `openrouter.aimirror.com` 类内嵌子串的中转/仿冒域误判；GLM/DeepSeek 历史同款按复审建议
  未混改（测试表以注释锚定现状，列 follow-up）。回归用例 +4（含内嵌子串负例）。
- **F-2（Minor）strip 降级口径信息利用**：降级仅 used 形态的 strip 摘要从 `OpenRouter · —`
  改拼 `OpenRouter · 已用 X`（卡面口径不变）；回归用例 +1。

修复后定向验证全绿：go vet 过、internal/config + 根包相关用例过、vitest 175/175、
npm run build（vue-tsc 门禁）过；另补跑全量 `go test ./... -count=1` 回归（结果见交付消息）。
