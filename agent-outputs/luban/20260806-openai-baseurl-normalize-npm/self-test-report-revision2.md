# 修订报告 R2：修复增量复审打回的 2 Major 未闭环 + 1 新 Major + 1 Minor

- 任务：修复谛听增量复审 `20260806-124445-openai-baseurl-normalize-npm-incremental-review.md` 打回项
- Agent：luban (work)
- 日期：2026-08-06
- 验证层级：L2（build + vet + config/launcher 双包测试 + 根包测试 + git diff --check，均实跑通过）
- 基线：HEAD `71fb3f684`，仅对增量复审点位的 revision2 增量修复

## 一、逐项修复说明（file:line）

### Major-2 未闭环：迁移持久化仍用归一化值

- **根因**：`internal/config/service.go:1513`（revision1）`buildMigratedOpenCodeConfig` 写入 `OpenCodePreset.Config` 的 provider entry 时用 `provider.EffectiveBaseURL("")`（归一化值），污染持久化 preset。revision1 报告明确披露未改此点；增量复审 §4.Major-2 / §5 调用方复核表点名阻断。
- **修复**（`internal/config/service.go:1513-1519`）：改为 `provider.EffectiveBaseURLRaw("")`，并补注释说明存储语义（迁移产出落盘/导出必须原值；运行时归一化由 launcher inject 从 `localProvider.EffectiveBaseURL` 重新读取覆盖）。
- **闭环验证**：持久化 `OpenCodePreset.Config` 中 baseURL 保持用户原值 `.../v1/chat/completions`，运行时 `BuildOpenCodeRuntimeConfigFromPreset` 产出归一化为 `.../v1`（真实跨包迁移链测试 `TestMigrationChain_RealLoadMigrationThenRuntimeBuild` 同时断言两侧）。
- **未改 `deriveOpenCodeProviderIDSimple`（service.go:1463）**：该处 `EffectiveBaseURL("")` 仅用于取 baseURL 喂给 `IsOfficialOpenAIBaseURL`（host 判定）和 anthropic 子串判定，归一化只剥离 path 后缀不改变 host，对 provider ID 推导零影响。增量复审 §5 调用方复核表明确标注此处"可接受"。无范围扩张。

### Major-1 证据缺口：真实跨包迁移链测试

- **缺口**：revision1 的 `TestBuildOpenCodeRuntimeConfigFromPreset_MigratedEntryWithoutNPMGetsDefaultNPM`（launcher 测试 3042-3129）手工模拟 `buildMigratedOpenCodeConfig` 输出结构构造 `preset.Config`，未真实调用 config 包迁移函数。增量复审 §4.Major-1 / §6 明确要求补真实 `terminal preset -> Load migration -> actual OpenCodePreset -> launcher runtime config` 集成测试。
- **修复**（`internal/launcher/opencode_config_test.go` 末尾，`TestMigrationChain_RealLoadMigrationThenRuntimeBuild`）：新增真实跨包集成测试，替代手工 fixture。
  - **设计与落点**：放在 launcher 包测试（launcher 已 `import config`，可调用真实 ConfigService API；config 包迁移函数 `migrateTerminalPresetsToOpenCodePresets`/`buildMigratedOpenCodeConfig` 是私有，通过公开 `ConfigService.Load` 间接触发真实迁移链）。
  - **真实链路**：
    1. 构造含第三方 OpenAI provider（baseURL 带后缀 `https://opencode.ai/zen/go/v1/chat/completions`）+ opencode 类型 terminal preset 的 config；
    2. 真实 `SaveProvider` + `SaveTerminalPreset` -> `Save` -> 新建 `ConfigService` `Load`，触发真实迁移 `migrateTerminalPresetsToOpenCodePresets -> buildMigratedOpenCodeConfig`；
    3. 读取真实迁移产出的 `OpenCodePreset`，断言持久化 `Config` 中 baseURL 为用户原始值（Major-2 闭环）+ 迁移 entry 无 npm（Major-1 前提）；
    4. 真实迁移产出的 preset 交给 `BuildOpenCodeRuntimeConfigFromPreset`，`getProvider` 直接走 `ConfigService.GetProvider` 真实读取；
    5. 断言运行时产出：`npm: "@ai-sdk/openai-compatible"`（Major-1）、model `zen/deepseek-v4-flash`、运行时 baseURL 归一化 `https://opencode.ai/zen/go/v1`、apiKey `sk-zen`。
  - **真实性保证**：任何一层结构漂移（config 迁移输出格式、launcher 运行时消费契约、provider ID 推导）都会让该测试失败，不再依赖手工 fixture 对模拟结构的断言。
- **保留** revision1 的 `TestBuildOpenCodeRuntimeConfigFromPreset_MigratedEntryWithoutNPMGetsDefaultNPM`：它仍覆盖运行时层对"迁移风格 entry 无 npm"的补 npm 单元语义，与真实链路集成测试互补，不删除。

### 新 Major：转义路径被破坏

- **根因**：revision1 `NormalizeOpenAIBaseURL`（types.go:555-581）基于解码后 `parsed.Path` 做后缀剥离，并 `parsed.RawPath = ""` 清空编码提示。`parsed.Path` 无法区分字面 `/` 与 `%2F`（`net/url` 解码后均呈现 `/`），输入 `https://host/v1%2Fchat%2Fcompletions` 的 `Path` 为 `/v1/chat/completions`，会被误当真实后缀裁剪成 `/v1`，且清空 `RawPath` 永久丢失编码（增量复审 §4.Major-3 阻断）。
- **修复**（`internal/config/types.go:563-593`）：改基于 `parsed.EscapedPath()`（编码形式）做后缀剥离，转义斜杠 `%2F`/`%2f` 不受影响。
  - 取 `escapedOrig := parsed.EscapedPath()`，循环剥离字面 `/chat/completions` 后缀与尾斜杠（只匹配字面 `/`，`%2F` 不是字面 `/`，不被误裁）；
  - `escaped == escapedOrig` 时原样返回 `s`（零副作用，绝大多数无后缀/无转义的 base URL 命中此分支）；
  - 变化时重建：`url.PathUnescape(escaped)` 解码新 path，`parsed.RawPath = escaped` 显式保留转义，`parsed.String()` 还原编码；
  - `PathUnescape` 失败保守返回原值（理论不会发生，escaped 来自合法 URL）。
- **转义路径处理方案小结**：剥离子串只看 EscapedPath 字面字符，`%2F`/`%2f`/混合转义输入往返无损；`escaped == escapedOrig` 短路保证无后缀场景零副作用。
- **实测验证（4.7 死代码预防）**：修复前用独立 Go 程序探测 `net/url` 对 `%2F`/`%2f`/尾点/userinfo 的真实行为，确认 `EscapedPath()` 保留原始转义大小写、`%2F` 不等于字面 `/`，避免基于文档假设写死代码。

### Minor：尾点域名

- **根因**：`IsOfficialOpenAIBaseURL`（types.go:609 revision1）`strings.EqualFold(parsed.Hostname(), "api.openai.com")`，`Hostname()` 对 `https://api.openai.com./v1` 返回 `api.openai.com.`（含单个尾点），FQDN DNS 等价却被判第三方（增量复审 §4.Major-4 Minor）。
- **修复**（`internal/config/types.go:620-622`）：`host := parsed.Hostname(); host = strings.TrimSuffix(host, ".")` 剥离单个 DNS 尾点后再 `EqualFold`。`TrimSuffix` 只剥一个尾点，安全；userinfo 不参与比较（`Hostname()` 已排除，实测 `https://api.openai.com@evil.example/v1` Hostname 为 `evil.example`）。

## 二、新增 / 更新测试清单

### `internal/config/types_test.go`

- `TestNormalizeOpenAIBaseURL`：新增 5 个转义路径用例（`%2F`/`%2f` 大小写、转义+尾斜杠、转义 tilde `%7E`、混合真实/转义段），断言转义路径不被误裁剪。
- `TestNormalizeOpenAIBaseURL_EscapedPathIdempotent`（新）：4 例转义路径再次归一化结果不变（含剥离了尾斜杠的转义路径）。
- `TestIsOfficialOpenAIBaseURL`：新增 4 例（官方 FQDN 尾点带/不带 scheme、官方 userinfo、欺骗 userinfo hosts 官方域名）。

### `internal/launcher/opencode_config_test.go`

- `TestMigrationChain_RealLoadMigrationThenRuntimeBuild`（新）：真实跨包迁移链集成测试（见第一节 Major-1 设计），替代手工 fixture 的主链证据缺口。

## 三、引用上游 artifact

- `agent-outputs/reviewer/20260806-124445-openai-baseurl-normalize-npm-incremental-review.md`（增量复审报告，逐条对照修复）
- `agent-outputs/luban/20260806-openai-baseurl-normalize-npm/self-test-report-revision1.md`（revision1 报告）
- `agent-outputs/luban/20260806-openai-baseurl-normalize-npm/self-test-report.md`（原实施报告）

## 四、验证结果（实际执行）

环境：`go version go1.26.1 darwin/arm64`；工作目录 `/Users/maorun/maorun-workpace/amagi-codebox`。

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS — exit 0，无输出 |
| `go vet ./...` | PASS — exit 0，无输出 |
| `go test ./internal/config/ ./internal/launcher/ -count=1` | PASS — `ok amagi-codebox/internal/config 0.425s` / `ok amagi-codebox/internal/launcher 0.802s` |
| `go test . -count=1` | PASS — `ok amagi-codebox 8.102s`（含 app.go 消费方回归） |
| `git diff --check` | PASS — exit 0，无空白错误 |

针对性回归（`-run` 精确跑修复点测试）：
- `TestNormalizeOpenAIBaseURL`（含 5 转义用例）/`TestNormalizeOpenAIBaseURL_EscapedPathIdempotent`：PASS
- `TestIsOfficialOpenAIBaseURL`（含 4 尾点/userinfo 用例）：PASS
- `TestMigrationChain_RealLoadMigrationThenRuntimeBuild`（真实跨包迁移链）：PASS

## 五、改动文件清单（revision2 增量）

| 文件 | 类型 | revision2 增量要点 |
|---|---|---|
| `internal/config/service.go` | 修改 | `buildMigratedOpenCodeConfig` baseURL 改 `EffectiveBaseURLRaw`（Major-2）+ 注释 |
| `internal/config/types.go` | 修改 | `NormalizeOpenAIBaseURL` 改基于 `EscapedPath`（新 Major）+ 重建 RawPath；`IsOfficialOpenAIBaseURL` 剥 DNS 尾点（Minor） |
| `internal/config/types_test.go` | 追加 | 5 转义路径用例 + 转义幂等测试 + 4 尾点/userinfo 用例 |
| `internal/launcher/opencode_config_test.go` | 追加 | `TestMigrationChain_RealLoadMigrationThenRuntimeBuild` 真实跨包迁移链集成测试 |

## 六、自查清单

1. 行动兑现：4 项（Major-2 Raw 持久化 / Major-1 真实链路证据 / 新 Major 转义路径 / Minor 尾点）全部按增量复审修复方向落地。PASS
2. 构建与验证跑过且通过，L1 冒烟（build+vet）未省，L2 config/launcher 双包 + 根包实跑，证据与改动相称。PASS
3. 无骨架残留：无 TODO/空实现/假数据/固定返回值/日志式错误处理。PASS
4. 一次一功能：4 项是同一增量复审打回的紧耦合修复，范围严格限定在审核点名位置 + 直接关联测试；未动 frontend、未动 codex wire_api、未动 anthropic 判定。PASS
5. Bug 修复回归证据：真实迁移链测试同时断言持久化原值（Major-2）+ 运行时归一化 + npm 注入（Major-1），转义路径/尾点表驱动覆盖。PASS
6. hook/toolInput/schema：转义路径处理涉及 `net/url` 的 EscapedPath/RawPath 行为，已用独立探测程序实测确认（非纯文档假设），4.7 死代码预防已过。PASS
7. 报告含 changed files + 回滚说明 + 未覆盖路径披露 + file:line。PASS

## 七、未覆盖路径与限度披露

- **`deriveOpenCodeProviderIDSimple`（service.go:1463）仍用 `EffectiveBaseURL("")`**：仅用于 host 分类（喂 `IsOfficialOpenAIBaseURL` 与 anthropic 子串判定），归一化只剥 path 后缀不改 host，对 provider ID 推导零影响；增量复审 §5 调用方复核表明确标注"可接受"。如后续要求该路径也走 Raw 以保持调用风格统一，可单独处理（不影响正确性）。
- **anthropic 官方判定（`strings.Contains "api.anthropic.com"`）未改**：不在本任务范围（增量复审未点名），存在同类欺骗 host 理论风险，但 anthropic 判定不影响 npm 注入。
- **codex `wire_api` / 前端**：按约束未动。工作区 frontend 并发改动为先前已存在，非本次会话引入，未触碰。
- **真实迁移链测试未跑 SaveProvider -> disk -> reload 完整原值 round trip**：Major-2 的存储原值已由"真实迁移产出 Config 中 baseURL == 原值"断言覆盖（真实 Load 迁移读取持久化 provider）；SaveProvider/BuildExportProvider 的原值保持由 revision1 的 `TestSyncLegacyFields_PreservesRawBaseURL`/`TestBuildExportProvider_PreservesRawBaseURL` 直接调用生产函数覆盖。

## 八、回滚说明

revision2 增量均为局部改动，回滚方式：
- `internal/config/service.go`：`buildMigratedOpenCodeConfig` 中 `EffectiveBaseURLRaw("")` 改回 `EffectiveBaseURL("")`（删除新增注释）。
- `internal/config/types.go`：`NormalizeOpenAIBaseURL` 恢复基于 `parsed.Path` 的实现 + `RawPath=""`；`IsOfficialOpenAIBaseURL` 去掉 `TrimSuffix(host, ".")`。
- 测试文件：`types_test.go` 删除转义路径用例 + `TestNormalizeOpenAIBaseURL_EscapedPathIdempotent` + 尾点/userinfo 用例；`opencode_config_test.go` 删除 `TestMigrationChain_RealLoadMigrationThenRuntimeBuild`。
- 未做任何 git commit/push/reset/stash/clean（git 纪律遵守）。

## 九、【待反思】

- **`net/url` Path vs EscapedPath 的解码陷阱**：`URL.Path` 是解码值，无法区分字面 `/` 与 `%2F`；`URL.RawPath` 是编码提示。任何对 URL path 做子串匹配/裁剪的逻辑，必须基于 `EscapedPath()`（保留编码），并在重建时显式设 `RawPath` + 解码 `Path`，否则会破坏转义序列（网关单段路由、签名材料）。清空 `RawPath` 是数据丢失操作，应避免。本次踩坑点：revision1 基于文档假设 `URL.Path` 可安全裁剪 + 清空 `RawPath`，被增量复审抓出转义路径破坏。教训：涉及 `net/url` 的行为应先用探测程序实测，不依赖文档字面描述。
- **持久化 vs 运行时归一化的调用点全量核对**：revision1 漏改 `buildMigratedOpenCodeConfig` 的持久化路径（service.go:1513），以"运行时通常覆盖"为由豁免，被增量复审 §5 调用方复核表系统抓出。教训：归一化分离改造必须对所有 `EffectiveBaseURL` 调用点做存储/运行时分类核对，不得以"通常被覆盖"豁免任何存储/导出路径。
- **真实跨包集成测试 vs 手工 fixture**：手工模拟迁移输出结构的测试能通过，但无法捕获迁移结构与运行时消费契约的漂移。审核要求真实主链测试正是要防止这种"模拟结构可继续通过"的风险。教训：跨包契约的测试应优先走真实公开 API 触发的集成链，手工 fixture 仅作单元语义补充。

## 十、建议下一步

1. **建议 diting 增量复审**：本次修复触及 `net/url` path 编码语义（新 Major 转义路径）+ 迁移持久化 Raw 化（Major-2）+ 真实跨包集成测试，建议对 revision2 增量复审，重点核对转义路径 EscapedPath 方案的正确性与幂等性、Major-2 持久化 Raw 闭环、真实迁移链测试的有效性。
2. 复审通过后由 Leader 调度 taibai 提交（git 操作不在本 Agent 职责内）。
