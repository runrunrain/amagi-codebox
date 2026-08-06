# 修订报告 R1：修复审核打回的 4 个 Major（OpenAI base URL 归一化 + npm 注入）

- 任务：修复谛听审核 `20260806-122225-openai-baseurl-normalize-npm-review.md` 打回的 4 个 Major
- Agent：luban (work)
- 日期：2026-08-06
- 验证层级：L2（build + vet + config/launcher 双包测试 + 根包测试 + git diff --check，均实跑通过）
- 基线：HEAD `71fb3f684`，仅对增量 diff 复审

## 一、逐项 Major 修复说明

### Major-1：自动迁移的第三方 OpenAI preset 漏注入 npm（迁移链）

- **根因**：`BuildOpenCodeRuntimeConfigFromPreset` 的 npm 注入条件是 `!entryExisted`（entry 是否存在）。但 `migrateTerminalPresetsToOpenCodePresets -> buildMigratedOpenCodeConfig`（service.go:1475）生成的 provider entry 已存在（`entryExisted=true`）却无 npm，导致迁移主启动路径的 `OPENCODE_CONFIG_CONTENT.provider.<id>` 缺 `@ai-sdk/openai-compatible`。
- **修复**（`internal/launcher/opencode_config.go:385-393`）：注入条件从"entry 是否存在"改为"entry 是否已显式含 npm 键"。
  - 第三方 OpenAI（`ocProviderID != "openai"`）且 `provEntry` 无 `npm` 键 → 补默认 `@ai-sdk/openai-compatible`；
  - `provEntry` 已有 `npm` 键（用户显式指定，含自定义包名如 `@ai-sdk/glm`）→ 保留不覆盖。
  - 移除了 `entryExisted` 变量（不再需要）；更新 3e 注释说明 entry 来源不区分。
- **迁移链覆盖**：迁移生成的 entry（已存在、无 npm）→ 运行时层补 npm，与旧轨道 `buildOpenCodeProviderMap` 行为一致。分层清晰：迁移层（service.go）不生成 npm，运行时层（launcher）负责补。

### Major-2：存储/导出被归一化污染

- **根因**：`SyncLegacyFields`（types.go:732）与 `BuildExportProvider`（types.go:400）调用 `EffectiveBaseURL("")`，而后者对 openai 格式做归一化，导致 `SaveProvider`/加载迁移/导出写入的 legacy `base_url` 被改写（用户输入 `.../v1/chat/completions` 存成 `.../v1`）。
- **修复**（`internal/config/types.go`）：
  - 新增 `EffectiveBaseURLRaw(format)`（:618-642）：返回**未归一化**的原始值，供存储/导出路径使用。
  - `EffectiveBaseURL(format)`（:645）重构为：先取 Raw 再按格式归一化；运行时消费端行为不变。
  - `SyncLegacyFields`（:732）改用 `EffectiveBaseURLRaw("")`。
  - `BuildExportProvider`（:400）改用 `provider.EffectiveBaseURLRaw("")`。
  - 归一化现在只发生在 `EffectiveBaseURL`（launcher / codex / pi 等运行时消费端），存储与导出保持用户原始输入值。
- **未改迁移构造**（service.go:1509 `EffectiveBaseURL("")`）：该处写入的是持久化 preset.Config 的 baseURL，严格说也属"存储"。但审核 Major-2 与任务说明均只点 SyncLegacyFields/SaveProvider/BuildExportProvider 三处，且迁移构造的 preset.Config baseURL 会被运行时 `BuildOpenCodeRuntimeConfigFromPreset` 的 inject 覆盖（从 localProvider 重读）。为避免范围扩张，本次不改迁移构造。

### Major-3：归一化损坏 query/fragment 与 hostless 输入

- **根因**：旧 `NormalizeOpenAIBaseURL` 对整个字符串做后缀裁剪（非 URL path 限定），会破坏 `https://host/v1?redirect=/`（裁成 `?redirect=`）、`https://`（裁成 `https:`）、`/chat/completions`（裁成空串）。
- **修复**（`internal/config/types.go:550-588`）：改用 `net/url` 解析。
  - TrimSpace；`url.Parse` 失败或 `Host==""`（hostless / scheme-only / 非 URL）→ 保守返回 TrimSpace 后原值；
  - 仅对 `parsed.Path` 做循环剥离（尾斜杠 + `/chat/completions` 后缀，大小写敏感，幂等）；
  - query/fragment/host/scheme/userinfo 原样保留；path 未变化时零副作用返回原值（多数无后缀的 base URL 命中此分支，完全无影响）。
  - types.go 顶部 import 新增 `net/url`。

### Major-4：官方 OpenAI 判定可被欺骗

- **根因**：`strings.Contains(baseURL, "api.openai.com")` 子串匹配，`https://api.openai.com.evil.example/v1`、`https://gateway.example/proxy/api.openai.com/v1` 会被误判为官方，provider ID 被改为内置 `openai` 且不注入 npm。
- **修复**（`internal/config/types.go:596-615`）：新增 `IsOfficialOpenAIBaseURL(baseURL)` helper。
  - net/url 解析后 `Hostname()` 精确比较 `api.openai.com`（`strings.EqualFold`，大小写不敏感）；
  - 无 scheme 输入补 `https://` 后再解析；解析失败/host 缺失保守返回 false（按第三方处理）；
  - 复用方：`opencode_config.go:93 deriveOpenCodeProviderID`、`service.go:1465 deriveOpenCodeProviderIDSimple` 共用同一 helper，保证 launcher 与 config 迁移语义一致。
  - anthropic 判定（`strings.Contains "api.anthropic.com"`）不在 Major-4 范围，保持原样。

## 二、新增 / 更新测试清单

### `internal/config/types_test.go`（重写）

- `TestNormalizeOpenAIBaseURL`：28 例表驱动。**修正** hostless 用例期望（`///` → `///`、`/chat/completions` → `/chat/completions`，不再被裁空）；**新增** query 保留（`?redirect=/`、`?target=/chat/completions/`）、fragment 保留、query+fragment、port、host-root（`https://host/` → `https://host`）、scheme-only（`https://`）、non-url、no-scheme-hostless 反例。
- `TestNormalizeOpenAIBaseURL_Idempotent`：幂等。
- `TestIsOfficialOpenAIBaseURL`：14 例。官方（含大写 host、无 scheme、port）+ 欺骗 host（`api.openai.com.evil.example`、path 含子串、path 等于 host）+ 第三方 + 空/hostless/scheme-only/non-url。
- `TestEffectiveBaseURL_*`：6 个原有测试（openai 归一化 / anthropic 不归一化 / 空 format 双向 / legacy 回退双向），确认 EffectiveBaseURL 重构后运行时行为不变。
- `TestEffectiveBaseURLRaw_NotNormalized` / `TestEffectiveBaseURLRaw_LegacyFallback`：Raw 返回原始值不归一化。
- `TestSyncLegacyFields_PreservesRawBaseURL`：带后缀 provider，SyncLegacyFields 后 legacy BaseURL 保留原始带后缀值，运行时 EffectiveBaseURL 仍归一化。
- `TestBuildExportProvider_PreservesRawBaseURL`：导出 legacy base_url 与嵌套 openai.base_url 都保留原始值。

### `internal/launcher/opencode_config_test.go`（追加 + 改 1 个）

- **改** `TestBuildOpenCodeRuntimeConfigFromPreset_UserWrittenExplicitNPMPreserved`（原 `UserWrittenEntryPreservesNPMDecision`）：新语义下"用户手写但没写 npm"会补 npm，故此测试改为验证"用户**显式**写 npm 键（自定义包 `@ai-sdk/glm`）→ 保留不覆盖"。
- **新增** `TestBuildOpenCodeRuntimeConfigFromPreset_MigratedEntryWithoutNPMGetsDefaultNPM`：迁移链核心测试。preset.Config 模拟 `buildMigratedOpenCodeConfig` 真实输出（entry 已存在、有 options、无 npm），断言运行时层补 npm + baseURL 归一化 + apiKey 注入 + model 保留。
- **新增** `TestBuildOpenCodeRuntimeConfigFromPreset_UserEntryWithoutNPMGetsDefaultNPM`：用户手写 entry 无 npm 键 → 补默认 npm。
- **新增** `TestDeriveOpenCodeProviderID_DeceptiveHost`：欺骗 host（trailing subdomain / path 含子串 / path 等于 host）用 providerName，官方精确判 `openai`。

## 三、引用上游 artifact

- `agent-outputs/reviewer/20260806-122225-openai-baseurl-normalize-npm-review.md`（审核报告，逐条对照修复）
- `agent-outputs/luban/20260806-openai-baseurl-normalize-npm/self-test-report.md`（原实施报告）
- `agent-outputs/provider-baseurl-consumption.md`（调研报告）

## 四、验证结果（实际执行）

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS — `=== BUILD OK ===`，无输出 |
| `go vet ./...` | PASS — `=== VET OK ===`，无输出 |
| `go test ./internal/config/ ./internal/launcher/ -count=1` | PASS — `ok amagi-codebox/internal/config 0.812s` / `ok amagi-codebox/internal/launcher 1.205s` |
| `go test . -count=1` | PASS — `ok amagi-codebox 23.041s`（含 app_codex_config_test.go 等 app.go 消费方回归） |
| `git diff --check` | PASS — `=== GIT DIFF CHECK CLEAN ===`，无空白错误 |

## 五、改动文件清单

| 文件 | 类型 | 要点 |
|---|---|---|
| `internal/config/types.go` | 修改 | import net/url；重写 NormalizeOpenAIBaseURL（Major-3）；新增 EffectiveBaseURLRaw（Major-2）；EffectiveBaseURL 重构；SyncLegacyFields/BuildExportProvider 改用 Raw（Major-2）；新增 IsOfficialOpenAIBaseURL（Major-4） |
| `internal/config/service.go` | 修改 | deriveOpenCodeProviderIDSimple 改用 IsOfficialOpenAIBaseURL（Major-4 迁移侧） |
| `internal/launcher/opencode_config.go` | 修改 | deriveOpenCodeProviderID 改用 IsOfficialOpenAIBaseURL（Major-4）；npm 注入条件改为"entry 无 npm 键"、移除 entryExisted（Major-1） |
| `internal/config/types_test.go` | 重写 | 见第二节 |
| `internal/launcher/opencode_config_test.go` | 追加+改1 | 见第二节 |

## 六、自查清单

1. 行动兑现：4 个 Major 均按审核修复方向实现，迁移链/npm 注入条件/存储导出分离/URL path 限定/host 精确判定全部落地。PASS
2. 构建与验证跑过且通过，L1 冒烟（build+vet）未省，证据与改动相称。PASS
3. 无骨架残留：无 TODO/空实现/假数据/固定返回值/日志式错误处理。PASS
4. 一次一功能：4 项 Major 是同一审核打回的紧耦合修复，范围严格限定在审核点名位置 + 直接相关测试，无范围外改动。PASS
5. Bug 修复回归证据：N/A（审核回流修复，测试覆盖各 Major 的失败场景）。
6. hook/toolInput/schema：N/A（不涉及 hook；opencode.json schema npm/options 平级已核对）。
7. 报告含 changed files + 回滚说明 + 未覆盖路径披露 + file:line。PASS

## 七、未覆盖路径与限度披露

- **迁移链测试的迁移层侧**：`TestBuildOpenCodeRuntimeConfigFromPreset_MigratedEntryWithoutNPMGetsDefaultNPM` 用模拟 `buildMigratedOpenCodeConfig` 输出结构的 preset.Config（注释引用 service.go:1507-1551），验证运行时层补 npm。未在测试中真实调用 `migrateTerminalPresetsToOpenCodePresets`（config 包内小写函数，跨包调用受限）；迁移层行为未改动，由现有迁移幂等性测试覆盖。运行时层"迁移风格 entry → 补 npm"的端到端断言已由本测试固定。
- **迁移构造 preset.Config 的 baseURL**（service.go:1509 仍用 `EffectiveBaseURL`）：未改（见 Major-2 说明），该值会被运行时 inject 从 localProvider 重读覆盖。如后续要求 preset.Config 持久化层也存原始值，可单独处理。
- **anthropic 官方判定**（`strings.Contains "api.anthropic.com"`）未改：不在 Major-4 范围（审核与任务说明仅点 openai）。存在同类欺骗 host 理论风险，但 anthropic 判定不影响 npm 注入，且当前无证据触发。
- **codex `wire_api`**：按约束未动。
- **前端**：未动（工作区 frontend/appmeta 并发改动与本任务无关，未触碰）。

## 八、回滚说明

改动均为增量且局部，回滚方式：
- `internal/config/types.go`：删除 IsOfficialOpenAIBaseURL/EffectiveBaseURLRaw，NormalizeOpenAIBaseURL 恢复旧实现，SyncLegacyFields/BuildExportProvider 改回 EffectiveBaseURL，移除 net/url import（`git checkout -- internal/config/types.go`）。
- `internal/config/service.go`：deriveOpenCodeProviderIDSimple 改回 strings.Contains（`git checkout -- internal/config/service.go`）。
- `internal/launcher/opencode_config.go`：deriveOpenCodeProviderID 改回 strings.Contains，npm 注入条件改回 `!entryExisted`（`git checkout -- internal/launcher/opencode_config.go`）。
- 测试文件：`internal/config/types_test.go` 直接删除；`internal/launcher/opencode_config_test.go` 追加段 `git checkout` 恢复。
- 未做任何 git commit/push/reset/stash/clean（git 纪律遵守）。

## 九、【待反思】

- **存储/运行时归一化分离的 API 设计**：引入 `EffectiveBaseURLRaw` 与 `EffectiveBaseURL` 并存，调用方需明确选择。存储/导出路径必须用 Raw，运行时消费用 EffectiveBaseURL。这是一处易误用的隐性约定——后续若新增消费方，需注意区分。已通过注释 + 测试（SyncLegacyFields/BuildExportProvider 原值保持）固化语义。
- **npm 注入判定从"entry 来源"改为"entry 内容"**：`entryExisted`（来源）不可靠（迁移生成的 entry 也是"已存在"），改为查 `npm` 键（内容）更鲁棒。教训：provenance 标记（migrated-overlay）若不可靠，应以内容特征作为判定依据。

## 十、建议下一步

1. **建议 diting 增量复审**：本次修复触及 opencode.json schema 生成（npm 注入语义变更）+ 全局 base URL 归一化（NormalizeOpenAIBaseURL 重写）+ 官方判定 helper（跨包共用），属接口/schema 风险路径。建议对增量 diff 复审，重点核对 Major-1 迁移链测试与 Major-2 存储/运行时分离。
2. **可选**：若主上希望迁移层 preset.Config 也持久化原始 baseURL，可单独委派（改 service.go:1509 用 EffectiveBaseURLRaw）。
