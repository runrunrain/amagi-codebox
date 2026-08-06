# OpenAI Base URL 归一化与 OpenCode npm 注入独立审核报告

> 审核人：谛听（reviewer）  
> 审核时间：2026-08-06  
> 审核基线：`HEAD 71fb3f684e08756eba7a2b6ba8a065d1a73ea209` + 审核开始时未提交工作区  
> 审核范围：本报告“Diff 基线”列出的 4 个源码/测试文件；只读审核，未修改代码、配置或测试  
> 结论：**不通过**

## 1. Diff 基线

### 1.1 Changed files 与变更范围

| 文件 | 变更范围摘要 |
|---|---|
| `internal/config/types.go` | 新增 `NormalizeOpenAIBaseURL`；改写 `Provider.EffectiveBaseURL`，对 OpenAI 分支统一归一化。 |
| `internal/config/types_test.go` | 新增归一化边界、幂等、OpenAI/Anthropic/legacy/空 format 测试。审核开始时为 untracked。 |
| `internal/launcher/opencode_config.go` | `buildOpenCodeProviderMap` 为第三方 OpenAI provider 注入 `npm`；`BuildOpenCodeRuntimeConfigFromPreset` 基于 `entryExisted` 注入 `npm`。 |
| `internal/launcher/opencode_config_test.go` | 追加旧轨道与 preset 轨道 npm 三态及 baseURL 联动测试。 |

已跟踪 diff 统计：`467 insertions, 4 deletions`；另有未跟踪的 `internal/config/types_test.go`（217 行）。

### 1.2 范围偏差说明

审核开始时 `git status --short` 除上述 4 个文件外，还包含下列 required artifacts，故工作区并非字面上的“仅 4 个文件”：

- `agent-outputs/provider-baseurl-consumption.md`
- `agent-outputs/luban/20260806-openai-baseurl-normalize-npm/`

它们仅作为审核输入，不纳入源码发现定级。本报告落盘后新增本审核 artifact，也不属于被审 diff。

### 1.3 审核期间工作区漂移

完成指定验证后再次执行 `git status --short`，发现下列 tracked 改动在审核开始时的状态中不存在：

- `frontend/package-lock.json`（10 行删除）
- `frontend/package.json`（1 行删除）
- `frontend/src/composables/useTerminalEngine.ts`（34 行新增、70 行删除）

这些文件不属于 Task Contract，也不是本审核产生的改动，故未扩展审核范围。它们意味着“当前完整工作区”已不再等同于本报告记录的 diff 基线；本报告结论严格针对上述 4 个 Go 源码/测试文件的捕获基线。由于 4 个 Go 文件中的阻断问题仍在，结论保持“不通过”；后续复审前应先隔离并明确这些并发改动的归属。

## 2. 引用上游 artifacts

- `/Users/maorun/maorun-workpace/amagi-codebox/agent-outputs/provider-baseurl-consumption.md`
- `/Users/maorun/maorun-workpace/amagi-codebox/agent-outputs/luban/20260806-openai-baseurl-normalize-npm/self-test-report.md`
- 审核开始时的完整 `git status` / `git diff` 与 4 个被审文件全文
- OpenCode 官方 Provider 文档：<https://opencode.ai/docs/providers/>（确认自定义 provider 的 `npm` 与 `options` 平级，OpenAI-compatible 使用 `@ai-sdk/openai-compatible`）

## 3. 审核结论

**不通过：4 个 Major，0 个 Critical，0 个 Minor。**

目标场景的简单新建路径已经具备“去除 `/chat/completions` + 注入 `@ai-sdk/openai-compatible`”的基本能力，且指定构建/测试命令全部通过；但现有 terminal preset 自动迁移后的主启动轨道仍会漏注入 npm，保存/导出路径会被运行时归一化污染，归一化函数会破坏合法 query/fragment 或 hostless 输入，官方 OpenAI 判定也可把第三方地址误判为内置 provider。这些问题均与明确验收项直接冲突。

## 4. 定级发现

### Major-1：自动迁移的第三方 OpenAI preset 已存在 provider entry，因此新轨道不会注入 npm

- **位置**：
  - `internal/config/service.go:1371-1376,1399-1407`：terminal preset 会被自动迁移为带完整 `Config` 的 `opencode_presets`。
  - `internal/config/service.go:1507-1525,1551`：迁移构造的 provider entry 只有 `options`/`models`，不生成 `npm`。
  - `internal/launcher/opencode_config.go:343-386`：只要 provider key 已存在，`entryExisted=true`，即使 entry 是系统迁移生成且没有 `npm`，也不会补包。
  - `app.go:3693-3713`：启动时优先命中迁移后的 `opencode_presets`，不会回退到已正确注入 npm 的旧轨道。
- **影响**：已有第三方 OpenAI terminal preset 在加载后会进入新轨道，但最终 `OPENCODE_CONFIG_CONTENT.provider.<id>` 缺少 `@ai-sdk/openai-compatible`，目标 provider 仍可能无法被 OpenCode 识别。`entryExisted` 不能等价于“用户手写 entry”。
- **证据**：迁移函数明确把来源标记为 `Source.Kind="migrated-overlay"`，同时生成完整 provider 节点；运行时条件却只看 map key 是否存在。新增测试只覆盖“provider key 缺失”和“用户手写 key 存在”，未覆盖真实迁移链。
- **修复方向**：在迁移构造阶段为第三方 OpenAI entry 生成默认 npm，并让 overlay/deepMerge 保留用户显式 npm；或基于可靠 provenance 区分 `migrated-overlay` 与用户手写配置。补一条 `terminal preset -> migrate -> BuildOpenCodeRuntimeConfigFromPreset` 集成测试，断言 npm、baseURL、model 与 apiKey 注入同时正确。

### Major-2：归一化并非只发生在运行时，SaveProvider/迁移/导出会改写 legacy 存储值

- **位置**：
  - `internal/config/types.go:671-677`：`SyncLegacyFields` 用已归一化的 `EffectiveBaseURL("")` 回填 `p.BaseURL`。
  - `internal/config/service.go:530-554`：`SaveProvider` 落盘前无条件调用 `SyncLegacyFields`。
  - `internal/config/service.go:246-280`：加载迁移也调用同一回填逻辑。
  - `internal/config/types.go:393-400`：`BuildExportProvider` 的 legacy `base_url` 同样来自已归一化的 `EffectiveBaseURL`。
- **影响**：用户输入 `https://host/v1/chat/completions` 后，嵌套 `openai.base_url` 仍可能保留原值，但 legacy 顶层 `base_url` 会变为 `https://host/v1`；导出结构同时携带“嵌套原值 + 顶层归一化值”，破坏存储原样与导出语义一致性，并可能污染仍消费 legacy 字段的版本/工具。
- **证据**：这是直接调用链，不是推测；实施报告所称“读取时纯函数变换”遗漏了 `EffectiveBaseURL -> SyncLegacyFields -> SaveProvider` 的写路径。当前新增测试没有执行 SaveProvider、reload 或 BuildExportProvider。
- **修复方向**：将持久化/导出用的原始值选择与运行时有效值选择分离；`SyncLegacyFields` 和导出应使用未归一化的原始格式字段，归一化只用于 launcher/Codex/Pi 等运行时消费。补 SaveProvider 落盘、reload、export/import round-trip 测试，断言用户原始字符串不变。

### Major-3：NormalizeOpenAIBaseURL 对整个字符串做后缀裁剪，会破坏合法 URL 数据和 hostless 输入

- **位置**：`internal/config/types.go:548-567`，尤其 `:555-561`。
- **影响**：实现没有限定“URL path”，而是直接裁剪整个字符串末尾：
  - `https://host/v1?redirect=/` 会变成 `https://host/v1?redirect=`；
  - `https://host/v1?target=/chat/completions/` 会进一步变成 `https://host/v1?target=`；
  - `https://` 会变成 `https:`；
  - hostless `/chat/completions` 会变成空串。
  这与“保守不丢数据”“仅处理路径后缀”冲突，也会让带 query/fragment 的企业网关或签名地址失真。
- **证据**：`internal/config/types_test.go:51-53,76-78` 反而把 `/// -> ""` 和 `/chat/completions -> ""` 固化为期望值；没有 query、fragment、scheme-only、hostless 非 URL 的保守性测试。函数注释 `internal/config/types.go:529-535` 声称“可逆、不丢数据”，与实际行为不符。
- **修复方向**：只修改可确认的 URL path，完整保留 query/fragment；对 parse 失败、缺少 host、scheme-only、普通非 URL 字符串返回 TrimSpace 后原值。若产品确实允许相对 base URL，应先明确相对路径语义再处理。补上述反例及企业网关中段 `/chat/completions/extra` 测试，并保留幂等断言。

### Major-4：官方 OpenAI 判定使用子串匹配，第三方 host/path 可被误判为内置 `openai`

- **位置**：
  - `internal/launcher/opencode_config.go:86-93`：`strings.Contains(baseURL, "api.openai.com")` 决定 provider ID。
  - `internal/launcher/opencode_config.go:120-126`：新增 npm 注入直接依赖该 ID。
  - 同类迁移判定位于 `internal/config/service.go:1457-1465`。
- **影响**：例如 `https://api.openai.com.evil.example/v1` 或 `https://gateway.example/proxy/api.openai.com/v1` 会被当成官方 OpenAI，provider ID 被改为 `openai` 且不注入 npm，第三方配置失效；还可能覆盖/混入用户已有的内置 `openai` entry。
- **证据**：代码比较的是完整小写字符串的任意子串，而仓库已有 `app.go:3401-3419` 使用 `url.Parse` + `Hostname()` 精确判定官方 host 的更稳健实现。新增测试只有标准官方 URL和普通第三方 URL，没有欺骗 host/路径样例。
- **修复方向**：抽取并复用精确 host 判定；只有 `Hostname()` 等于 `api.openai.com` 且路径符合内置 OpenAI 约定时才视为官方，解析失败/不确定时保守按第三方处理。让 launcher 与 config 迁移共用同一 helper/语义，并补 deceptive host/path 测试。

## 5. 验收项映射

| 验收项 | 实现位置 | 审核结果 |
|---|---|---|
| 目标完整端点剥离为 base URL，且幂等 | `types.go:548-567`；`types_test.go:9-108` | 简单目标样例通过；边界保守性失败（Major-3）。 |
| OpenAI 新字段、legacy 回退、空 format 统一归一化 | `types.go:577-603` | 运行时读取生效；写路径被污染（Major-2）。 |
| Anthropic 路径不变 | `types.go:590-603` | 通过；显式 anthropic 分支未归一化。 |
| 旧轨道第三方 OpenAI 补 npm | `opencode_config.go:101-139` | 标准 URL 通过；官方判定不可靠（Major-4）。 |
| preset 轨道第三方 OpenAI 补 npm，用户 entry 优先 | `opencode_config.go:339-388` | 仅缺失 entry 的新建场景通过；迁移主路径失败（Major-1）。 |
| npm 与 options 平级，deepMerge 用户优先 | `opencode_config.go:64-74,124-126` | 结构符合官方 schema；旧轨道用户 overlay 仍可覆盖默认 npm。 |
| API key 路径不变、无新增明文落盘 | 本 diff + `service.go:550-551` | 通过；未发现新增密钥持久化路径。 |

## 6. 全部 EffectiveBaseURL 调用方影响核对

| 消费方 | 位置 | 结论 |
|---|---|---|
| OpenCode 旧轨道/ID 推导 | `internal/launcher/opencode_config.go:87,104` | 目标 URL得到改善；受 Major-4 影响。 |
| OpenCode preset 注入 | `internal/launcher/opencode_config.go:363` | baseURL 可归一化；受 Major-1 影响。 |
| Codex env/config.toml | `app.go:2353-2360` | OpenAI baseURL 会归一化；未改 API key；外部 `wire_api=responses` 的端到端兼容性仍未验证。 |
| Pi models.json | `internal/launcher/pi_config.go:62-74` | OpenAI baseUrl 会归一化；现有 key/文件权限逻辑未变。 |
| Claude/Headroom | `app.go:1717`、`internal/launcher/service.go:104` | 显式 anthropic 分支不变。 |
| Config 迁移/保存 | `internal/config/types.go:676`、`internal/config/service.go:279,548` | 不符合“只在运行时消费端归一化”（Major-2）。 |
| Export | `internal/config/types.go:399` | legacy export 值被归一化（Major-2）。 |
| OpenCode preset 迁移 | `internal/config/service.go:1460,1509` | baseURL 归一化，但迁移 entry 漏 npm（Major-1）。 |

## 7. 测试质量审核

- 新测试均实际调用生产函数并做值断言，未发现恒真断言或纯 mock 自证。
- 三态基础覆盖存在：第三方 OpenAI、官方 OpenAI、Anthropic；npm 与 options 平级也有断言。
- 关键缺口：
  1. 没有 terminal preset 自动迁移到 runtime config 的链路测试；
  2. 没有 SaveProvider/reload/export/import 原值保持测试；
  3. 没有 query/fragment/hostless/非 URL 保守性测试；
  4. 没有伪装 `api.openai.com` 子串的 host/path 测试；
  5. “仅后缀/仅斜杠归空”测试把需求冲突行为固定为正确结果。
- 新增测试代码明显超过实现代码 2 倍；多数用例能映射真实边界或三态风险，不单独按“凑数”定级，但当前投入仍未覆盖上述最高风险链路。

## 8. 独立复跑验证

环境：`go version go1.26.1 darwin/arm64`；工作目录为项目根目录。

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS，exit 0，无输出。 |
| `go vet ./...` | PASS，exit 0，无输出。 |
| `go test ./internal/config/ ./internal/launcher/ -count=1` | PASS；config `1.501s`，launcher `2.044s`。 |
| `go test . -count=1` | PASS；根包 `14.645s`。 |
| `git diff --check` | PASS，exit 0，无输出。 |

复跑结果与实施报告“全部通过”一致；耗时差异属于正常运行波动。测试通过不能覆盖本报告列出的静态调用链缺口。

## 9. 安全核对

- 本 diff 未修改 API key 获取、keychain、scrub 或文件权限逻辑。
- `npm` 为固定的 `@ai-sdk/openai-compatible`，未引入用户输入形成包名的路径。
- 未发现新增密钥明文落盘路径或 OWASP Top 10 定级问题。
- Major-4 是功能分类错误；当前证据不足以将其定为凭据泄露或 SSRF 安全漏洞，故未作安全定级。

## 10. 打回修复要求

1. 修复 migrated-overlay 第三方 OpenAI provider 的 npm 生成/注入，并补“迁移后真实运行时配置”集成测试。
2. 将原始存储/导出值与运行时归一化值分离，证明 SaveProvider、reload、export/import 不改变用户输入。
3. 将归一化限制在 URL path；保留 query/fragment，对 hostless、非法/非 URL 输入保守返回，并修正现有错误期望测试。
4. 用精确 hostname/path 语义统一官方 OpenAI 判定，覆盖欺骗 host/path。
5. 修复后仅对增量 diff 复审；重新提供上述 4 条命令的验证结果。

## 11. 建议下一步

建议 Leader 将 Major-1～4 作为功能问题回流 luban；修复后按增量 diff 复审。是否放行与后续分流由 Leader 决定。
