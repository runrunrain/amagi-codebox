# OpenAI Base URL / npm revision2 增量复审报告

> 审核人：谛听（reviewer）  
> 审核时间：2026-08-06 12:57 +08:00  
> 复审基线：`HEAD 71fb3f684e08756eba7a2b6ba8a065d1a73ea209`；仅审核 revision2 相对上一轮报告点名问题的增量  
> 结论：**通过**  
> 定级发现：**无**

## 1. Diff 基线

当前工作区仍是包含其他任务改动的累计未提交状态，无法直接用单一 Git 对象生成纯 revision2 diff。本次以必读的上一轮复审报告所记录实现为旧基线，结合 revision2 修复报告与当前完整目标区块隔离增量；未复审无关旧 diff。

| revision2 changed file | 本轮增量范围摘要 |
|---|---|
| `internal/config/service.go` | `buildMigratedOpenCodeConfig` 的迁移持久化 baseURL 改用 `EffectiveBaseURLRaw`。 |
| `internal/config/types.go` | `NormalizeOpenAIBaseURL` 改为基于 `EscapedPath()` 裁剪并重建 `Path/RawPath`；官方 OpenAI host 判定剥离单个尾点。 |
| `internal/config/types_test.go` | 增加 `%2F/%2f`、混合真实/转义路径、无变化及幂等用例；增加 FQDN 尾点与 userinfo 用例。 |
| `internal/launcher/opencode_config_test.go` | 增加真实 Load 迁移到 launcher 运行时构建的跨包集成测试。 |

工作区内 frontend、appmeta、opencodeplugin、文档等并发改动不属于本次范围。审核期间未修改代码、配置或测试，仅新增本审核报告。

## 2. 引用上游 artifacts

- `/Users/maorun/maorun-workpace/amagi-codebox/agent-outputs/reviewer/20260806-124445-openai-baseurl-normalize-npm-incremental-review.md`
- `/Users/maorun/maorun-workpace/amagi-codebox/agent-outputs/luban/20260806-openai-baseurl-normalize-npm/self-test-report-revision2.md`

## 3. 逐项闭环确认

### 3.1 Major-2：迁移持久化 Raw 闭环

**通过。**

- `internal/config/service.go:1513-1523`：`buildMigratedOpenCodeConfig` 现在通过 `provider.EffectiveBaseURLRaw("")` 构造将进入 `OpenCodePreset.Config` 的 `options.baseURL`，不再把运行时归一化值写入存储语义。
- `internal/config/service.go:1399-1408,1443-1453`：该配置经 marshal、scrub 后进入迁移生成的 `OpenCodePreset.Config`；中间没有再次调用 `EffectiveBaseURL` 或改写 baseURL。
- `internal/config/service.go:1095-1107`：导入旧 terminal preset 快照的另一条迁移持久化路径复用同一个 `migrateTerminalPresetsToOpenCodePresets -> buildMigratedOpenCodeConfig`，随后 `saveLocked`，因此同样取得 Raw 值。
- `internal/config/service.go:1413-1420`：provider 不存在时的 fallback 直接保留 `tp.OpenCodeCfg`（仅规范化/scrub secret），不存在新增归一化遗漏。
- 全仓生产调用复核仍只有 `service.go:1463` 的 provider-ID host 分类可合理使用 `EffectiveBaseURL`；存储/导出调用为 `types.go:400,753` 与本次 `service.go:1518`，均已使用 Raw。

真实链测试在 `internal/launcher/opencode_config_test.go:3271-3289` 断言迁移产出的 preset Config 保持 `https://opencode.ai/zen/go/v1/chat/completions`，旧的 `EffectiveBaseURL` 实现会得到 `.../v1` 并使该断言失败，不是恒真断言。

### 3.2 Major-1：真实迁移主链证据闭环

**通过。**

`TestMigrationChain_RealLoadMigrationThenRuntimeBuild` 确实执行了要求的公开 API 主链：

1. `internal/launcher/opencode_config_test.go:3227-3242`：新建并 Load `ConfigService`，真实调用 `SaveProvider`；
2. `internal/launcher/opencode_config_test.go:3244-3255`：真实调用 `SaveTerminalPreset` 与显式 `Save`；
3. `internal/launcher/opencode_config_test.go:3257-3269`：新建 service 并重新 `Load`，由 Load 触发私有迁移，再通过 `GetOpenCodePreset` 读取真实迁移结果；
4. `internal/launcher/opencode_config_test.go:3291-3305`：将真实 preset 交给 `BuildOpenCodeRuntimeConfigFromPreset`，provider 回调直接调用 `svc2.GetProvider`；
5. `internal/launcher/opencode_config_test.go:3308-3330`：分别断言 provider/model 契约、默认 npm 注入、运行时 baseURL 归一化和 apiKey 注入。

断言具备失败区分力：迁移仍写 normalized 会在 `3283-3285` 失败；迁移结构/provider ID 漂移会在 `3276-3289` 或 `3310-3317` 失败；旧的“entry 已存在则不补 npm”会在 `3319-3321` 失败；launcher 未从持久化 provider 注入 Effective baseURL 会在 `3324-3327` 失败。`getAPIKey` 是该函数既定依赖边界的最小 stub，但 provider 与迁移对象均来自真实 service，不构成手工迁移 fixture 自证。

### 3.3 转义路径 Major：EscapedPath 方案闭环

**通过。**

- `internal/config/types.go:563-581`：匹配对象改为 `parsed.EscapedPath()`，只裁剪字面 `/chat/completions` 和字面尾 `/`；`%2F/%2f` 不会被误作路径分隔符。
- `internal/config/types.go:582-585`：无变化时直接返回 TrimSpace 后的原字符串，保留转义大小写及原格式。
- `internal/config/types.go:586-595`：仅在 path 有变化时 `PathUnescape` 重建 decoded `Path`，同时把裁剪后的 escaped path 写入 `RawPath`；失败走保守原值返回。
- `internal/config/types_test.go:151-177`：覆盖 `%2F`、`%2f`、转义路径加真实尾斜杠、转义字符和“转义段 + 真实后缀”混合场景。
- `internal/config/types_test.go:203-219`：覆盖转义路径归一化幂等。
- `internal/config/types_test.go:33-36,190-201`：目标 URL 仍归一化为 `https://opencode.ai/zen/go/v1` 且幂等。
- `internal/config/types_test.go:68-88`：query/fragment 保留回归仍在并通过。

未发现 `%2F/%2f` 大小写丢失、混合段误裁、无变化路径重序列化或二次归一化继续变化的问题。

### 3.4 FQDN 尾点 Minor：闭环

**通过。**

- `internal/config/types.go:615-630`：基于 `Hostname()` 排除 userinfo/port 后，仅用 `TrimSuffix(host, ".")` 去除一个 terminal dot，再做大小写不敏感的精确比较。
- `internal/config/types_test.go:229-245`：`api.openai.com.`（带/不带 scheme）识别官方；官方 userinfo 正例与 `api.openai.com@evil.example` 反例同时覆盖。

单尾点处理没有退化为子串匹配；双尾点、前导点、子域、路径或 userinfo 欺骗仍不能命中官方域名，未发现新误判。

### 3.5 全面回归与既有闭环项

**通过。**

- 存储/导出 Raw：`internal/config/types.go:639-675,740-755` 与 `types_test.go:367-451` 保持通过。
- npm 三态：无 npm 键补默认包、显式 npm 保留、内置 OpenAI/Anthropic 不补 npm 的生产逻辑未被 revision2 改动；launcher 全包及针对性用例通过。
- query/fragment、hostless 输入和目标 URL：config 全包及针对性归一化用例通过。
- 安全/硬编码：revision2 未新增凭据落盘、用户可控执行源、绝对路径、环境耦合或 OWASP Top 10 高置信度问题。
- 测试映射成立：新增用例均对应上一轮打回风险；真实链测试与局部单元测试层次不同，未判定为凑数冗余。

非阻断可维护性附注：`internal/config/types.go:535` 与 `internal/config/types_test.go:7` 的概述仍写“`URL.Path`”，实现已明确使用 `EscapedPath()`；局部实现注释与测试行为准确，不影响本轮功能放行，后续文档整理时可同步措辞。

## 4. 独立复跑结果

环境：`go version go1.26.1 darwin/arm64`；工作目录 `/Users/maorun/maorun-workpace/amagi-codebox`。

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS，exit 0，无输出。 |
| `go vet ./...` | PASS，exit 0，无输出。 |
| `go test ./internal/config/ ./internal/launcher/ -count=1` | PASS；config `1.470s`，launcher `1.928s`。 |
| `go test . -count=1` | PASS；根包 `8.827s`。 |
| `git diff --check` | PASS，exit 0，无输出。 |
| config 归一化/官方 host/Raw 存储导出针对性测试 | PASS；`0.890s`。 |
| launcher npm 三态与真实迁移链针对性测试 | PASS；`0.382s`。 |

## 5. 结论与建议下一步

revision2 对上一轮 2 个未闭环 Major、转义路径新 Major 和尾点 Minor 均形成实现与证据闭环，未发现 revision2 新增的阻断问题。**结论：通过。**

建议由 Leader 决定是否进入后续提交链；若提交前 diff 再变化，本结论仅覆盖上述 revision2 基线，新增 diff 需按增量另行判断。
