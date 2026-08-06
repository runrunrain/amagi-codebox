# OpenAI Base URL / npm 修复增量复审报告

> 审核人：谛听（reviewer）  
> 审核时间：2026-08-06  
> 复审基线：`HEAD 71fb3f684e08756eba7a2b6ba8a065d1a73ea209`；仅复审上一轮报告后与 4 个 Major 直接相关的修复增量  
> 结论：**不通过（对应主上要求的“打回”）**  
> 发现：**3 个 Major（其中 1 个为测试证据缺口）、1 个 Minor**

## 1. Diff 基线

### 1.1 本次复审 changed files 与增量范围

| 文件 | 变更范围摘要 |
|---|---|
| `internal/config/types.go` | `NormalizeOpenAIBaseURL` 改用 `net/url`；新增 `IsOfficialOpenAIBaseURL`、`EffectiveBaseURLRaw`；存储/导出调用切换为 Raw。 |
| `internal/config/service.go` | config 迁移侧官方 OpenAI 判定改为共享 helper。 |
| `internal/config/types_test.go` | 重写归一化边界测试；新增官方 host、Raw、SyncLegacyFields、BuildExportProvider 测试。当前文件仍为 untracked。 |
| `internal/launcher/opencode_config.go` | launcher 官方 OpenAI 判定改为共享 helper；preset npm 条件改为“entry 无 npm 键”。 |
| `internal/launcher/opencode_config_test.go` | 更新用户显式 npm 测试；新增迁移风格 entry、无 npm entry、欺骗 host 等测试。 |

当前 `HEAD -> worktree` 的 tracked 统计（包含上一轮已审基线与本轮修复，不能视为纯修复增量）：`internal/config/service.go` 7+/3-、`types.go` 121+/5-、`opencode_config.go` 32+/5-、`opencode_config_test.go` 555+/0-；`internal/config/types_test.go` 为 401 行 untracked 文件，未计入 `git diff --numstat`。由于上一轮状态没有 Git 对象可直接 diff，本次纯修复增量按原报告记录的旧实现、R1 修复清单及当前对应区块逐项隔离，未重审原 diff 的无关部分。

### 1.2 工作区隔离说明

当前工作区仍有 frontend、appmeta、opencodeplugin、文档等并发改动；它们不属于本 Task Contract，本报告未扩展审核。被审 5 个文件的状态为 4 个 modified + `internal/config/types_test.go` untracked。审核期间未修改源码、配置或测试。

## 2. 引用上游 artifacts

- `/Users/maorun/maorun-workpace/amagi-codebox/agent-outputs/reviewer/20260806-122225-openai-baseurl-normalize-npm-review.md`
- `/Users/maorun/maorun-workpace/amagi-codebox/agent-outputs/luban/20260806-openai-baseurl-normalize-npm/self-test-report-revision1.md`
- 当前 5 个被审文件的完整内容、当前 `git diff`、全部 `EffectiveBaseURL` / `EffectiveBaseURLRaw` 调用点

## 3. 结论摘要

四项修复均有实质进展：npm 的内容判定逻辑正确覆盖迁移风格 entry，SaveProvider/SyncLegacyFields 和 BuildExportProvider 已切到 Raw，常规 query/fragment/hostless 边界已修复，两个官方 OpenAI 调用方也已共享精确 host helper。指定构建、vet、测试及 diff 检查全部通过。

但仍不能放行：

1. 原 Major-2 点名的迁移持久化路径仍在用归一化值，修复报告亦明确披露“未改”；
2. 原 Major-1 明确要求的真实迁移主链测试仍被手工构造的 preset 替代；
3. 新归一化实现基于已解码 `URL.Path` 匹配并清空 `RawPath`，会把 `%2F` 等转义路径当成真实分隔符裁剪，仍不满足“保守、不丢数据”；
4. 官方 host helper 未处理 DNS 等价的单个尾点。

## 4. 原 Major 逐项闭环确认

### Major-1：实现逻辑闭环，真实迁移链证据未闭环

**实现侧确认：通过。**

- `internal/launcher/opencode_config.go:343-350`：不再使用 `entryExisted`，统一取得或创建 entry。
- `internal/launcher/opencode_config.go:383-394`：仅 `format == "openai" && ocProviderID != "openai"` 时检查 npm；entry 无 npm 键才补默认值。
- `internal/launcher/opencode_config_test.go:2873-2936`：用户显式 `npm: "@ai-sdk/glm"` 会保留，且 apiKey 继续注入。
- `internal/launcher/opencode_config_test.go:2938-3040`：内置 `openai` 与 anthropic binding 均不补 npm。
- `internal/launcher/opencode_config_test.go:3131-3179`：用户 entry 无 npm 键时补默认值。

因此，迁移生成的“entry 已存在但无 npm”在运行时函数中确实会得到默认 npm；没有发现 anthropic entry 或内置 `provider.openai` 被该条件误补的新边界。

**测试证据侧：未闭环（Major，测试证据不足）。**

- `internal/launcher/opencode_config_test.go:3042-3129` 明确手工构造 `preset.Config` 与 `Bindings`，只调用 `BuildOpenCodeRuntimeConfigFromPreset`；它没有执行 `ConfigService.Load -> migrateTerminalPresetsToOpenCodePresets -> buildMigratedOpenCodeConfig`。
- 仓库已有真实迁移测试 `internal/config/service_test.go:2251-2319`，但使用官方 OpenAI，且断言到迁移后的存储对象即停止，没有把返回的 `OpenCodePreset` 交给 launcher 运行时构建函数。

**影响**：两段各自测试不能证明实际迁移产出的 provider ID、binding key/format、Config 结构与运行时消费始终契合；迁移结构未来漂移时，手工 fixture 可继续通过。这正是上一轮要求“terminal preset -> migrate -> BuildOpenCodeRuntimeConfigFromPreset”真实链路测试要防止的问题，模拟结构的限度不可接受。

**修复方向**：补一条通过公开 `ConfigService` API 触发真实 Load 迁移、读取实际 `OpenCodePreset`，再调用 launcher 构建运行时配置的集成测试；使用第三方 OpenAI 原始完整端点，断言 npm、归一化 baseURL、apiKey、model 与 provider ID。

### Major-2：未闭环——迁移持久化路径仍写归一化值

**已闭环部分：**

- `internal/config/types.go:618-654`：Raw 与运行时 Effective 语义已分离。
- `internal/config/types.go:727-734`：`SyncLegacyFields` 使用 `EffectiveBaseURLRaw`；`SaveProvider` 在 `internal/config/service.go:530-554` 经该函数落盘，因此 provider 的 nested/legacy base URL 可保持原值。
- `internal/config/types.go:386-410`：`BuildExportProvider` 的 legacy `base_url` 使用 Raw，nested clone 也保持原值。
- `internal/config/types_test.go:317-400`：Raw、SyncLegacyFields、BuildExportProvider 均直接调用生产函数并做有效值断言；这些断言本身真实，不是恒真或 mock 自证。

**阻断发现（Major）：**

- **位置**：`internal/config/service.go:1511-1518`；生成迁移用、随后写入 `OpenCodePreset.Config` 的 provider entry 时仍调用 `provider.EffectiveBaseURL("")`。持久化对象的赋值链见 `internal/config/service.go:1399-1408,1443-1453`。
- **影响**：用户原值 `https://opencode.ai/zen/go/v1/chat/completions` 在 terminal preset 自动迁移时仍被写成 `https://opencode.ai/zen/go/v1`。即使正常运行时可从 local provider 再注入，持久化 preset.Config 已不再原值；当 local provider 读取失败、binding 不注入 baseURL，或该 preset 被后续导出/编辑时，归一化值仍会泄漏到存储语义。
- **证据**：这是直接调用链；修复报告 `self-test-report-revision1.md:29,104-107` 也明确承认该存储路径未改。上一轮 Major-2 与本次复审要求均要求排查“无遗漏的存储/导出路径”，不能以运行时通常会覆盖为由豁免。
- **修复方向**：迁移构造持久化 `preset.Config` 时使用 `EffectiveBaseURLRaw`；运行时继续由 launcher 的 `EffectiveBaseURL` 归一化。真实迁移链测试同时断言“存储 Raw、运行时 normalized”。

### Major-3：指定边界已修，转义路径仍有破坏性裁剪

**已闭环部分：**

- `internal/config/types.go:550-582` 已把处理限制到解析后的 path；parse 失败或 `Host==""` 返回 TrimSpace 后原值。
- `internal/config/types_test.go:68-149` 有效覆盖 query、fragment、hostless、scheme-only、普通非 URL 字符串；目标用例 `internal/config/types_test.go:32-36` 正确断言为 `https://opencode.ai/zen/go/v1`。
- `internal/config/types_test.go:162-173` 证明目标用例幂等。

**新发现（Major）：转义路径不保守。**

- **位置**：`internal/config/types.go:555,563-581`。
- **影响**：`net/url.URL.Path` 是解码后的路径。输入如 `https://host/v1%2Fchat%2Fcompletions` 的 `Path` 会呈现 `/v1/chat/completions`，当前代码会误认为它含真实后缀并裁成 `/v1`；随后 `parsed.RawPath = ""` 丢弃原始编码提示。转义斜杠可能是网关单段路由或签名材料，不能等价于真实 `/` 分隔符，当前行为仍会丢数据。
- **证据**：`go doc net/url.URL` 明确说明 `Path` 以 decoded form 保存，无法区分原始 `/` 与 `%2f`，应使用 `EscapedPath` 保留编码；当前代码反而显式清空 `RawPath`。现有 `types_test.go:16-150` 无 percent-encoded path 用例。
- **修复方向**：只对原始 escaped path 中的字面 `/chat/completions` 和字面尾斜杠做裁剪，保留其他转义及 `RawPath`；补 `%2F`、`%2f`、路径内 `%7E`、query/fragment 组合回归和幂等测试。

### Major-4：核心欺骗修复闭环；尾点有 Minor 边界

**核心闭环：通过。**

- `internal/config/types.go:596-610`：解析后用 `Hostname()` + `EqualFold` 精确比较；端口被 Hostname 排除，大小写正确处理，userinfo 不参与 host 比较。
- `internal/launcher/opencode_config.go:89-102` 与 `internal/config/service.go:1457-1475` 均调用同一 helper，语义一致。
- `internal/config/types_test.go:177-205` 与 `internal/launcher/opencode_config_test.go:3181-3208` 对标准官方、大小写、端口、欺骗子域/路径做了有效断言。
- userinfo 语义从实现可确认：`https://api.openai.com@evil.example/v1` 的 Hostname 为 `evil.example`，不会误判；`https://user@api.openai.com/v1` 的目标 host 为官方域名，会判官方。未发现子串欺骗回归。

**新发现（Minor）：DNS 尾点未规范化。**

- **位置**：`internal/config/types.go:609`。
- **影响**：`https://api.openai.com./v1` 的 Hostname 为 `api.openai.com.`，当前返回 false；该 FQDN 与无尾点域名 DNS 等价，却会按自定义 provider 分类。常规 provider 名恰为 `openai` 时影响有限，但自定义名称的官方 provider 会生成不同 provider ID/npm 语义。
- **证据**：比较前没有移除单个 terminal dot；`internal/config/types_test.go:183-196` 也没有尾点或 userinfo 用例。
- **修复方向**：对解析后的 hostname 仅去除一个 DNS terminal dot 后再 `EqualFold`，并补“官方尾点 / 官方 userinfo / 官方域名仅出现在 userinfo”测试。

## 5. EffectiveBaseURL 全调用方复核

| 调用方 | 分类 | 结论 |
|---|---|---|
| `app.go:1717,2355,2363,2680` | Claude/Codex/Pi 运行时 | 使用 Effective 合理。 |
| `internal/launcher/opencode_config.go:90,108,366` | OpenCode 运行时 | 使用 Effective 合理。 |
| `internal/launcher/pi_config.go:67` | Pi 运行时 | 使用 Effective 合理。 |
| `internal/launcher/service.go:104` | Claude 运行时 | anthropic 路径不归一化，合理。 |
| `internal/config/service.go:1463` | 迁移时 provider ID 推导 | 仅取 hostname 做分类，后缀归一化不改变 host；可接受。 |
| `internal/config/service.go:1513` | **迁移持久化 preset.Config** | **不应使用 Effective；Major-2 未闭环。** |
| `internal/config/types.go:400,732` | 导出 / legacy 存储 | 已改 Raw，正确。 |

全仓生产代码仅上述调用；除 `service.go:1513` 外，没有发现其他存储/导出路径继续直接使用归一化值。

## 6. 新增测试真实性与冗余审核

- `types_test.go` 的 query/fragment/hostless、Raw、SyncLegacyFields、BuildExportProvider 均直接调用生产函数，断言能在旧实现上失败，真实性成立。
- launcher 的显式 npm、无 npm、官方/Anthropic、欺骗 host 测试均直接调用生产函数，真实性成立。
- 所谓“迁移链核心测试”只模拟迁移输出，不满足真实主链证据要求，见 Major-1。
- 原值测试没有真实执行 `SaveProvider -> disk -> reload` 或 export/import round trip；就 SaveProvider 而言，当前直接调用链可证明已改 Raw，但由于迁移存储路径仍失败，整体存储验收仍不成立。
- 测试代码量显著高于实现增量，但现有新增用例大多能映射原 4 个风险；未发现可单独定级的凑数测试。应优先把重复 fixture 投入改为一条真实跨包集成链，而不是继续追加模拟测试。

## 7. 独立复跑结果

环境：`go version go1.26.1 darwin/arm64`；工作目录 `/Users/maorun/maorun-workpace/amagi-codebox`。

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS，exit 0，无输出。 |
| `go vet ./...` | PASS，exit 0，无输出。 |
| `go test ./internal/config/ ./internal/launcher/ -count=1` | PASS；config `0.817s`，launcher `1.267s`。 |
| `go test . -count=1` | PASS；根包 `12.808s`。 |
| `git diff --check` | PASS，exit 0，无输出。 |

命令通过证明当前已有测试与构建稳定，但不覆盖上述真实迁移链、迁移 Raw 存储和 percent-encoded path 缺口。

## 8. 安全与硬编码核对

- 本增量没有新增凭据落盘、用户可控 npm 包注入或 OWASP Top 10 定级问题。
- 默认 npm 字符串为固定包名，用户显式 npm 仅保留既有配置，不新增执行来源。
- userinfo/欺骗 host 未形成官方 host 判定绕过；尾点问题是功能分类边界，不作安全定级。
- 未发现新增绝对路径、网络地址、凭据、设备绑定或伪实现。

## 9. 建议下一步

1. 回流 luban：修正迁移持久化 Raw 路径和 percent-encoded path 保守处理；尾点可同轮小修。
2. 回流 wukong 或由实现侧补证：增加真实 `terminal preset -> Load migration -> actual OpenCodePreset -> launcher runtime config` 集成测试，避免继续使用手工迁移 fixture 代替主链。
3. 修复后仅复审本报告列出的增量；是否放行与最终分流由 Leader 决定。
