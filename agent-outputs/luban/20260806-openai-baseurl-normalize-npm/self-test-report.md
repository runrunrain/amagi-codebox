# 自测报告：OpenAI base URL 归一化 + 自定义 provider 自动补 npm

- 任务：第三方 OpenAI 兼容端点配入 Provider Center 的两个阻断点改造
- Agent：luban (work)
- 日期：2026-08-06
- 验证层级：L2（build + vet + 双包测试 + 根包测试，均实跑通过）

## 一、实现摘要

围绕"用户把 `https://opencode.ai/zen/go/v1/chat/completions` + model + Bearer key 配入自定义 OpenAI 兼容 provider 直接使用"的两个阻断点，完成三处改动：

1. **base URL 归一化（全局，openai 格式）**：新增 `config.NormalizeOpenAIBaseURL`，在 `EffectiveBaseURL` 的 openai 分支（含新字段、legacy 回退、空 format 推导）统一剥离 `/chat/completions` 后缀与尾斜杠；anthropic 分支一律不动。
2. **opencode 旧轨道 npm 注入**：`buildOpenCodeProviderMap` 对第三方 OpenAI 兼容 provider（推导 id ≠ 内置 `openai`）补 `npm: "@ai-sdk/openai-compatible"`；内置 openai 与 anthropic 不加。
3. **opencode preset 轨道 npm 注入**：`BuildOpenCodeRuntimeConfigFromPreset` 对"代码为缺失 binding 新建"的第三方 OpenAI provider entry 补 npm；用户在 `preset.Config` 手写的 entry 不强行覆盖。

## 二、改动文件清单与要点

| 文件 | 类型 | 要点 |
|---|---|---|
| `internal/config/types.go` | 修改 | 新增导出函数 `NormalizeOpenAIBaseURL`；改造 `EffectiveBaseURL`：openai 格式路径（新字段 `OpenAI.BaseURL`、legacy `p.BaseURL` 回退、空 format 经 `PreferredFormat()` 推导）返回值均经归一化；anthropic 分支原样透传。 |
| `internal/launcher/opencode_config.go` | 修改 | `buildOpenCodeProviderMap`：OpenAI 兼容且 `deriveOpenCodeProviderID != "openai"` 时写 `entry["npm"]="@ai-sdk/openai-compatible"`（与 options 平级）。`BuildOpenCodeRuntimeConfigFromPreset`：记录 `entryExisted`，对 `format=="openai" && ocProviderID!="openai" && !entryExisted` 的代码新建 entry 补 npm。 |
| `internal/config/types_test.go` | 新建 | `NormalizeOpenAIBaseURL` 表驱动（14 例）+ 幂等测试 + `EffectiveBaseURL` 归一化矩阵（openai 剥离 / anthropic 不剥离 / 空 format 双向 / legacy 回退双向）。 |
| `internal/launcher/opencode_config_test.go` | 修改（追加） | npm 注入测试 9 例：旧轨道（第三方含 npm / 官方 openai 不含 / anthropic 不含 / 第三方 anthropic 不含 / baseURL 归一化+npm 联动）+ preset 轨道（代码新建补 npm / 用户手写不补 / 内置 openai 不补 / anthropic binding 不补）。 |

### `NormalizeOpenAIBaseURL` 语义决策

- **重复后缀 `.../chat/completions/chat/completions` 选择循环剥离至不再匹配**（而非单次剥一层）。理由：归一化的不变式是"输出永不含该后缀"，循环保证幂等；单次剥离会残留一层后缀，下游仍需二次处理。重复后缀虽非真实场景，但循环实现语义更确定，已在注释与测试中固定。
- **大小写敏感**：`/Chat/Completions` 不剥离，原样返回（`strings.HasSuffix` 本身大小写敏感，符合"大小写不变原样保留"）。
- **仅处理后缀形式**：后缀出现在路径中间（如 `.../chat/completions/extra`）不剥离。
- **保守不丢数据**：纯后缀字符串匹配，不做 URL parse，避免 parse 失败丢数据；不确定情况返回 TrimSpace 后原值。

### npm 注入在 preset 轨道的处理决策及依据

代码实际行为（`BuildOpenCodeRuntimeConfigFromPreset` 的 binding 循环）：遍历 `preset.Bindings`，对每个 ocProviderID（binding map key）从 `preset.Config` 解析出的 providers map 中取 entry；若 key 不存在则代码新建空 entry 并写入 options。

**决策**：以 `entryExisted`（providers map 中该 key 是否存在）区分两种来源——
- **代码新建**（`!entryExisted`，即 `preset.Config` 的 provider 节点不含该 id）：补 npm，与旧轨道 `buildOpenCodeProviderMap` 行为对齐，保证生成的 opencode.json 可被运行时识别。
- **用户手写**（`entryExisted`，即用户在 `preset.Config` 写了该 provider，哪怕空对象 `{}`）：不强行注入 npm，遵循 deepMerge"用户手写优先、未知字段保留"语义；用户若需自定义 npm（如指定 `@ai-sdk/glm` 等专用包）自行在 `preset.Config` 写入。

**判断条件**：`format == "openai" && ocProviderID != "openai" && !entryExisted`。
- preset 轨道下 ocProviderID 是 binding 的 map key（用户定义的 opencode provider id），不是 `deriveOpenCodeProviderID` 推导值；若用户把第三方 openai provider 绑定到内置 `"openai"` key，走 opencode 预置实现，不加 npm。
- npm 与 options 平级，符合 opencode.json schema（`provider.<id>.npm`）。

## 三、引用上游 artifact

- `agent-outputs/provider-baseurl-consumption.md`（前期调研，全链路 file:line 证据）
- `internal/config/types.go`（EffectiveBaseURL/IsOpenAICompatible 现状）
- `internal/launcher/opencode_config.go`（buildOpenCodeProviderMap/deriveOpenCodeProviderID/BuildOpenCodeRuntimeConfigFromPreset/deepMerge 现状）
- `app_codex_config_test.go`、`internal/launcher/opencode_config_test.go`（回归基线）

## 四、验收映射

| Contract 验收条件 | 实现 | 验证 |
|---|---|---|
| 新增 `NormalizeOpenAIBaseURL`：TrimSpace/循环剥离尾斜杠/精确后缀剥离/空串返回/保守返回 | types.go 新增 | TestNormalizeOpenAIBaseURL 14 例 + 幂等 |
| `EffectiveBaseURL` openai 分支（含 OpenAI 新字段、legacy 回退）归一化 | types.go 改造 | TestEffectiveBaseURL_* 6 例 |
| anthropic 分支不动 | types.go（isOpenAIFormat 守卫） | TestEffectiveBaseURL_AnthropicNotNormalized 等 |
| 空 format 下 openai 兼容走归一化 | PreferredFormat() 推导 | TestEffectiveBaseURL_EmptyFormatOpenAINormalized |
| `buildOpenCodeProviderMap` 第三方 OpenAI 补 npm，内置 openai 不加 | opencode_config.go | TestBuildOpenCodeRuntimeConfig_ThirdPartyOpenAIInjectsNPM / OfficialOpenAINoNPM |
| preset 轨道按代码实际行为补 npm / 用户手写优先 | opencode_config.go（entryExisted） | TestBuildOpenCodeRuntimeConfigFromPreset_ThirdPartyOpenAIAutoNPM / UserWrittenEntryPreservesNPMDecision |
| 生成条目 npm 与 options 平级 | entry["npm"] 与 entry["options"] 平级 | TestBuildOpenCodeRuntimeConfig_ThirdPartyOpenAIInjectsNPM 断言平级 |
| NormalizeOpenAIBaseURL 表驱动测试（含重复后缀语义固定） | types_test.go | 14 例含 duplicate suffix → 循环全剥 |
| opencode_config_test npm 三态测试 | opencode_config_test.go 追加 | 9 例 |
| EffectiveBaseURL 归一化单测（openai 剥/anthropic 不剥） | types_test.go | 6 例 |
| 不破坏现有测试（含 codex 侧） | — | 4 条验证命令全 PASS |

## 五、验证结果（实际执行，原文见 logs/test-results.log）

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS（无输出） |
| `go vet ./...` | PASS（无输出） |
| `go test ./internal/config/ ./internal/launcher/ -count=1` | PASS — `ok amagi-codebox/internal/config 1.012s` / `ok amagi-codebox/internal/launcher 1.414s` |
| `go test . -count=1` | PASS — `ok amagi-codebox 8.969s`（含 app_codex_config_test.go 等） |

新增测试统计：config 包 8 个测试函数（含 14 子用例的表驱动）、launcher 包追加 9 个测试函数。全部通过。

## 六、自查清单

1. 行动兑现：NormalizeOpenAIBaseURL / EffectiveBaseURL 归一化 / 双轨道 npm 注入 / 测试，均已实现且验证。PASS
2. 构建与验证跑过且通过，L1 冒烟（build+vet）未省，证据与改动相称。PASS
3. 无骨架残留：无 TODO/空实现/假数据/固定返回值/日志式错误处理。PASS
4. 一次一功能：三处改动是同一阻断点（自定义 OpenAI provider 可用性）的紧耦合三方面，作为一个完整目标交付，无范围外改动。PASS
5. Bug 修复回归证据：N/A（功能任务）。
6. hook/toolInput/schema：N/A（不涉及 hook；opencode.json schema 已核对 npm/options 平级）。
7. 报告含 changed files + 回滚说明 + 未覆盖路径披露；本次为 medium tier。PASS

## 七、未覆盖路径与限度披露

- **opencode/codex/pi 运行时对 baseURL 的消费行为未端到端验证**：归一化保证送入各引擎的 baseURL 不含 `/chat/completions` 后缀，但各 CLI/SDK（`@ai-sdk/openai-compatible` 是否自动追加路径、codex `wire_api=responses` 对 chat 端点的兼容性、pi `api=openai-completions` 的路径拼接）均在外部 CLI 侧，代码库内无对应逻辑，本次未做运行时端到端（需真实启动 CLI）。与调研报告"待核验项"一致。
- **codex `wire_api` 未改**（Contract 明确不做）：codex 侧 base URL 经 `EffectiveBaseURL("openai")` 也会被归一化，但 `wire_api="responses"` 硬编码不动。
- **前端未改**：用户仍可粘贴任意 URL 字符串（前端无 URL 校验，调研报告 §6 已确认），归一化在后端读取时生效。

## 八、回滚说明

改动均为增量且局部，回滚方式：
- `internal/config/types.go`：删除 `NormalizeOpenAIBaseURL` 函数，将 `EffectiveBaseURL` 恢复为原始 switch+直接 return（`git checkout -- internal/config/types.go`）。
- `internal/launcher/opencode_config.go`：删除 `buildOpenCodeProviderMap` 中 npm 注入块、`BuildOpenCodeRuntimeConfigFromPreset` 中 `entryExisted` 记录与 npm 注入块（`git checkout -- internal/launcher/opencode_config.go`）。
- 测试文件：`internal/config/types_test.go` 为新建可直接删除；`internal/launcher/opencode_config_test.go` 追加段可 `git checkout` 恢复。
- 未做任何 git commit/push/reset/stash/clean（git 纪律遵守）。

## 九、【待反思】

- **preset 轨道 entry 来源判定**：用 `providers[ocProviderID]` key 是否存在区分"代码新建"vs"用户手写"，而非类型断言结果。用户手写空对象 `{}` 算"用户手写"不补 npm——这是 deepMerge 语义的延伸（用户显式写了就尊重），但可能让"用户写了空 provider 节点想依赖代码补全"的预期落空。当前选择"用户手写优先"更保守、可预测，符合 Contract 指示；若后续发现用户普遍期望代码补全空 entry，可重审。
- **归一化是全局生效**：`EffectiveBaseURL` 被 codex/pi/opencode/export/sync 等多处消费，openai 格式统一归一化。已确认现有测试 baseURL 均不带后缀（等价 unaffected），但这是一处"读取时纯函数变换"的隐性全局行为，后续若有消费方依赖原始带后缀 URL 需注意。

## 十、建议下一步

1. **建议 diting 审核**：本次改动触及 opencode.json schema 生成（npm 字段）与全局 base URL 归一化语义（影响 codex/pi/opencode 多引擎 baseURL），属"接口/schema"风险路径，建议经 diting 审核位。
2. **可选端到端验证**：若需闭环"待核验项"，建议用真实 opencode CLI 启动一个绑定该 zen 端点的 provider，确认 `@ai-sdk/openai-compatible` + 归一化 baseURL 能正常发起请求（这超出代码库范围，需主上手动或 wenqu 外部调研）。
3. **前端可选增强（非本次范围）**：前端 AddProviderDialog 的 base_url 输入可加一个"检测到 /chat/completions 后缀将自动剥离"的提示，提升用户感知；当前后端已透明处理。
