# 调研报告：自定义 Provider（OpenAI 格式）base_url 在各引擎启动链路中的消费方式

> 纯事实整理，不含结论与建议。探索目标：`https://opencode.ai/zen/go/v1/chat/completions`（完整端点，含 /chat/completions 后缀）+ `deepseek-v4-flash` + `sk-xxx` 能否"原样"作为自定义 Provider（OpenAI 格式）的 base_url 使用。
> 生成时间：2026-08-06。来源仓库：amagi-codebox（Wails v2，Go 后端 + Vue 前端）。

## 资料清单（核心发现）

### 1. 全链路无任何 URL 归一化：base_url 一律原样透传（TrimSpace 除外）

- `config.Provider.EffectiveBaseURL(format)`：直接返回 `OpenAI.BaseURL` / `Anthropic.BaseURL` / 回退 `p.BaseURL`，**无去尾斜杠、无拼接 `/chat/completions`、无 URL parse**（`internal/config/types.go:527-544`）。
- OpenCode：`buildOpenCodeProviderMap` 将 `EffectiveBaseURL("")` 原样写入 `options["baseURL"]`（`internal/launcher/opencode_config.go:113-119`）。
- Codex：`syncCodexCustomProviderConfigFile` 将 baseURL 原样 `strconv.Quote` 写入 config.toml（`app.go:3041-3056`）。
- Pi：`BuildPiModelsConfig` 将 `EffectiveBaseURL(format)` TrimSpace 后原样写入 `baseUrl`（`internal/launcher/pi_config.go:67-73`）。
- 测试佐证：`internal/launcher/opencode_config_test.go:295-296`（`https://custom.api.com/v1` 原样进 `options["baseURL"]`）、`:2011-2012`、`:2132-2133`（anthropic baseURL 同样原样）；`app_codex_config_test.go:95/230-232`（config.toml 期望 `base_url = "<原样>"`、`env_key = "OPENAI_API_KEY"`、`wire_api = "responses"`）。

### 2. OpenCode 引擎注入链路

- 入口：`App.LaunchOpenCode`（`app.go:3684`）。
- **轨道 1（新模型 opencode_presets）**：`app.go:3693-3734` → `launcher.BuildOpenCodeEnvOverridesFromPreset`（`opencode_config.go:377-424`）→ `BuildOpenCodeRuntimeConfigFromPreset`（`:276-374`）：
  - 解析 preset.Config 为 map，遍历 `preset.Bindings`，对每个 binding 的 `LocalProvider` 读取本地 provider 与统一 key（`:299-322`）；
  - **inject 列表为空时默认 `["apiKey","baseURL"]`**（`:326-329`）；`baseURL` 注入 `localProvider.EffectiveBaseURL(format)`（`:348-354`），`format` 由 `resolveBindingFormat` 推导（`:239-265`，OpenAI 仅兼容 → "openai"）；
  - 输出 `OPENCODE_CONFIG_CONTENT`（JSON 字符串 env，`:393`）+ 对应 `OPENAI_API_KEY`/`ANTHROPIC_API_KEY` env 冗余（`:415-420`）。
- **轨道 2（旧模型回退 terminal_presets.opencode）**：`app.go:3740-3799` → `ResolveTerminalPreset("opencode", presetName)`（`:3746`）→ 命中则桥接为 `provider.Presets[presetName]`（`:3764-3778`）→ `launcher.BuildOpenCodeEnvOverrides`（`opencode_config.go:188-219`）→ `BuildOpenCodeRuntimeConfig`（`:19-77`）→ `buildOpenCodeProviderMap`（`:102-132`，OpenAI 类型写 `options["baseURL"]` = 原样）→ `OPENCODE_CONFIG_CONTENT` env（`:205`）+ `OPENAI_API_KEY` env（`:208-211`）。
- **写入载体：环境变量 `OPENCODE_CONFIG_CONTENT`，不写 ~/.config/opencode/opencode.json**。`internal/opencodeconfig` 包仅管理全局配置文件（设置页读写），与 launch 链路无关（`internal/opencodeconfig/service.go:1-10` 包注释、`configFilePath()` 指向 `$HOME/.config/opencode/opencode.json`）。
- **npm 包字段：代码不生成 `npm` 字段**。provider 条目仅 `{ options: { apiKey, baseURL[, organization] } }`（`opencode_config.go:106-129`）。`@ai-sdk/openai-compatible` 等 npm 指定只能由用户在 preset 的 `opencode_config` 中手写，经深度合并保留（`:65-74`，`deepMerge :166-184` 保留未知字段）。
- provider ID 推导：`deriveOpenCodeProviderID`（`:86-99`）——OpenAI 兼容且 baseURL 含 `api.openai.com` → `"openai"`，否则用 **providerName 本身**；`model` 字段为 `<providerID>/<model>`（`:39`）。
- `OpenCodeBinding.Inject` 落点：仅经 `BuildOpenCodeRuntimeConfigFromPreset` 的 inject 循环写入 `config.provider[ocProviderID].options`（`:342-363`），secrets 运行时注入、不写回 preset.Config（`:270-272` 注释）。

### 3. Codex 引擎注入链路

- 入口：`App.LaunchCodexSession`（`app.go:2269`）。
- terminal_presets 桥接（`:2274-2282`）→ `injectProviderEnv`（`:2348-2367`）：
  - OpenAI provider → env `OPENAI_API_KEY` + `OPENAI_BASE_URL`（`provider.EffectiveBaseURL("openai")` 原样，`:2355-2356`）；
  - `isCustomCodexOpenAIBaseURL(baseURL)`（`:3401-3420`）：host ≠ `api.openai.com`（或 path 非空且非 `/v1`）→ true，记录 `codexProviderBaseURL`。
- `syncCodexCustomProviderConfig(model, baseURL)`（`:2803-2810`）→ 写 `~/.codex/config.toml`（`:2808`）→ `syncCodexConfigFile`（`:2831-2868`，EnsureCustomProvider + ForceAPILogin）→ `appendCodexCustomProviderSection`（`:3041-3056`）生成：
  ```toml
  [model_providers.amagi-codebox-provider]
  name = "amagi-codebox-provider"
  base_url = "<baseURL 原样>"
  env_key = "OPENAI_API_KEY"
  requires_openai_auth = false
  wire_api = "responses"
  ```
  并同步顶层 `model = "<model>"`、`model_provider = "amagi-codebox-provider"`、`forced_login_method = "api"`（`:2847-2855`）。
- **wire_api 硬编码 `"responses"`，无 chat 分支**（`:3052`；常量 `codexModelProviderName = "amagi-codebox-provider"`、`codexOfficialOpenAIAPIHost = "api.openai.com"` 见 `app.go:108-109`）。
- **密钥不写入 config.toml**：`env_key = "OPENAI_API_KEY"` 引用进程环境变量（进程 env 由 `launcher.BuildEnv` 注入，`internal/launcher/service.go:719-779`）。

### 4. Pi 引擎注入链路

- 入口：`App.LaunchPiSession`（`app.go:2593`）。
- terminal_presets 桥接（`:2598-2610`）→ `launcher.BuildPiModelsConfig`（`internal/launcher/pi_config.go:54-157`）：
  - provider id = `"amagi-<providerName>"`（`:26-28`）；
  - `baseUrl` = `provider.EffectiveBaseURL(format)` **原样**（`:67-73`），`format` 按 `IsOpenAICompatible()` 取 "openai"（`:62-65`）；
  - `api` = OpenAI 兼容 → `"openai-completions"`（`piAPIType :38-43`）；
  - `apiKey` 内嵌写入（`:76-78`）；可选 headers/authHeader（`:88-96`）；
  - models 注册当前模型（`:99-150`）。
- 写入：`MergePiAgentConfig`（`:212-242`，保留用户已有 providers）→ `WritePiAgentConfig`（`:170-207`，原子写 `~/.pi/agent/models.json`，0600/0700 权限）。
- 启动参数 `--provider amagi-<name> --model <model>`（`app.go:2718-2726`）；同时移除 `PI_CODING_AGENT_DIR`（`:2659-2662`）。
- 冗余兜底：`piProviderMapping`（`app.go:3583-3591`，OpenAI → 内置 "openai" + `OPENAI_API_KEY` env）仅当 models.json 写入失败时使用（会丢失自定义 baseURL，注释明示"不应作为常态路径"）。

### 5. ClaudeCode 引擎注入链路

- 入口：`App.LaunchSession`（`app.go:1601`）→ terminal_presets 桥接（`:1604-1638`）→ **硬拦截**：`if !provider.IsAnthropicCompatible()` 直接报错拒绝启动（`:1640-1643`）。
- `IsAnthropicCompatible`（`internal/config/types.go:474-482`）：新字段 `Anthropic.Enabled == true`；回退旧字段 `!strings.EqualFold(p.Type,"openai") && p.AuthKey != "OPENAI_API_KEY"`。
- env 注入：`launcher.BuildOverrides`（`internal/launcher/service.go:77-181`）：非 Anthropic 兼容 → 清空 `ANTHROPIC_BASE_URL`/`ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN`（`:90-95`）；兼容时 `ANTHROPIC_BASE_URL` = proxy 地址（headroom 开启时，`:101-102`）或 `EffectiveBaseURL("anthropic")` 原样（`:104`）。

### 6. 前端与后端校验规则（base_url 无任何格式校验）

- `frontend/src/components/provider/AddProviderDialog.vue`：base_url 为纯文本 input（`:43-62`，placeholder 仅为提示）；`canSave` 只要求"至少启用一种格式 + 名称合法"（`:205-208`）；名称校验：非空、无前后空白、不含 `/`（`:190-203`）。**任意 URL 字符串均可保存**。
- 后端 `ConfigService.SaveProvider`（`internal/config/service.go:530-555`）：仅 name 非空、`SyncLegacyFields`、`normalizeProviderType`、`scrubProviderAPIKeys`（清 APIKey 明文）；**无 URL parse/校验**。
- ProviderCenter 相关展示：`frontend/src/views/ProviderDetailView.vue:281-293`、`frontend/src/components/provider/ProviderGrid.vue:228-235`（有效 baseURL = anthropic/openai 子块优先，回退 legacy `base_url`，纯展示逻辑）。

### 7. secrets 存储与注入方式

- 存储：`SecretsService`（`internal/secrets/service.go:13-125`）内存 cache `map[string]string`，**key = providerName**（`SetAPIKey :101-112`、`GetAPIKey :84-99`）；持久化 `secrets.enc` 经 OS keychain（`store_darwin_cgo.go` Keychain / `store_windows.go` DPAPI；`store_other.go` 为 no-op）。
- 读取优先级：`getProviderAPIKeyWithLegacyCandidates`（`app.go:3479-3492`）——先 `providerName`，再 legacy `providerName:anthropic` / `providerName:openai`（`:3484`）；`getProviderAPIKey`（`:3497-3499`）按 provider 格式生成 legacy candidates（`:3451-3477`）。
- 注入方式（按引擎）：OpenCode → `OPENCODE_CONFIG_CONTENT` 内 `options.apiKey` + `OPENAI_API_KEY` env 冗余；Codex → 仅 `OPENAI_API_KEY` env（config.toml 引用 env_key，不含明文）；Pi → 内嵌 `apiKey` 写入 `~/.pi/agent/models.json`（0600）+ env 冗余；Claude → `ANTHROPIC_API_KEY` env。

## 相关文件

| 文件 | 作用 | 重要性 |
|---|---|---|
| `internal/config/types.go:474-544` | IsAnthropicCompatible / IsOpenAICompatible / PreferredFormat / EffectiveBaseURL（无归一化源头） | 高 |
| `internal/launcher/opencode_config.go` | OpenCode 运行时配置生成（provider map / inject / env overrides） | 高 |
| `app.go:3684` LaunchOpenCode | OpenCode 启动编排（新模型 + terminal_preset 回退两轨道） | 高 |
| `app.go:2269` LaunchCodexSession + `:2803/:3041` | Codex env 注入 + config.toml 写入（wire_api="responses"） | 高 |
| `app.go:2593` LaunchPiSession + `internal/launcher/pi_config.go` | Pi models.json 生成与写入（api="openai-completions"） | 高 |
| `app.go:1601/:1640` LaunchSession | Claude 启动 + Anthropic 兼容硬拦截 | 高 |
| `internal/launcher/service.go:77-181` | Claude env 覆盖（ANTHROPIC_BASE_URL） | 高 |
| `internal/secrets/service.go` | providerName → apikey 存取 | 中 |
| `frontend/src/components/provider/AddProviderDialog.vue:190-208` | 前端校验（无 URL 校验） | 中 |
| `internal/config/service.go:530-555` | 后端 SaveProvider（无 URL 校验） | 中 |
| `internal/opencodeconfig/service.go` | 全局 opencode.json 设置页读写（与 launch 链路无关） | 低 |

## 依赖关系

```
前端 AddProviderDialog.vue → SaveProvider / SetAPIKey+Save
      ↓
internal/config Provider（openai{enabled,base_url} | anthropic{enabled,base_url}）+ secrets（providerName→key）
      ↓
App.LaunchOpenCode ──┬─ opencode_presets ──→ BuildOpenCodeEnvOverridesFromPreset ──→ OPENCODE_CONFIG_CONTENT env
                     └─ terminal_presets.opencode ─→ BuildOpenCodeEnvOverrides ──→ OPENCODE_CONFIG_CONTENT env
App.LaunchCodexSession ──→ env OPENAI_BASE_URL/OPENAI_API_KEY + syncCodexCustomProviderConfig ──→ ~/.codex/config.toml（base_url 原样, wire_api=responses, env_key=OPENAI_API_KEY）
App.LaunchPiSession ──→ BuildPiModelsConfig ──→ ~/.pi/agent/models.json（baseUrl 原样, api=openai-completions, apiKey 内嵌）──→ --provider amagi-<name>
App.LaunchSession(Claude) ──→ !IsAnthropicCompatible() 拒绝；否则 ANTHROPIC_BASE_URL=EffectiveBaseURL("anthropic") env
```

## 已覆盖 / 未覆盖范围

- 已覆盖：四引擎启动链路全部入口、URL 透传与归一化检查（无）、opencode inject 机制、codex config.toml 生成全文、pi models.json 生成全文、claude 拦截与 env、secrets 存取、前后端校验、npm 字段生成情况、测试期望输出佐证。
- 未覆盖：opencode / codex / pi **运行时**对 baseURL 的消费行为（`@ai-sdk/openai-compatible` 的 baseURL 语义、codex wire_api=responses 对 chat-completions 端点的兼容性、pi 对完整端点 URL 的拼接行为）——均在外部 CLI / SDK 侧，本代码库内无对应逻辑；`normalizeCodexModelName`（app.go:2783）与 baseURL 无关，未展开。

## 待核验项

- 【待核验：`https://opencode.ai/zen/go/v1/chat/completions` 原样作为 baseURL 时，opencode 运行时（@ai-sdk/openai-compatible）是否自动追加 /chat/completions 或接受完整路径——静态搜索无法覆盖外部 SDK 行为】
- 【待核验：codex `wire_api = "responses"` 硬编码（app.go:3052）对仅提供 chat/completions 的端点是否可用——codex CLI 上游行为，代码库无判断】
- 【待核验：pi `api = "openai-completions"` 对完整端点 URL（含 /chat/completions 后缀）的请求路径构造——pi CLI 上游行为】
- 【待核验：`OPENAI_BASE_URL` env 对 codex 的生效语义（是否被 codex 读取为 base_url 或 openai_base_url）——代码库只负责注入，不消费】
