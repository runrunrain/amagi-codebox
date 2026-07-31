# Pi 与 Pi 插件兼容性差距分析报告

> 调研者：白泽（Explorer，fast 型只读探索）
> 调研对象：amagi-codebox（Wails v2，Go 后端 + Vue3/TS 前端，管理 Claude Code / OpenCode / Codex / Pi 四种 AI CLI）
> 基准：以 **OpenCode 与 Codex 的完整支持链路** 为基准，逐层对比 Pi 缺失/不足之处
> 证据口径：所有结论附 `文件路径:行号`；无法静态确认的标【待核验】

---

## ① 现状总览表（能力 × 4 个 app 的支持矩阵）

图例：✅ 完整支持 ｜ 🟡 部分支持（功能在但薄/有缺口） ｜ ❌ 无 ｜ ➖ 不适用（设计上该引擎不需要）

| 能力层 | Claude Code | OpenCode | Codex | Pi | 证据锚点 |
|---|---|---|---|---|---|
| **CLI 安装/版本检测 (envcheck)** | ✅ `checker_claude.go` 717 行 + selfheal | ✅ `checker_opencode.go` 408 行 | ✅ `checker_codex.go` 177 行 | 🟡 `checker_pi.go` 176 行（版本+安装+npm 兜底+self-update，无 config 校验） | `internal/envcheck/checker_*.go` |
| **安装器 (install/self-update)** | ✅ | ✅ | ✅ | ✅ npm 全局装 + `pi update self` | `internal/envcheck/installer.go:1556-1572` |
| **启动器 CLI 参数构建** | ✅ `buildClaudeCmd` | ✅ `buildOpenCodeCmd` | ✅ `buildCodexCmd`(`-m`) | ✅ `buildPiCmd`(`--provider/--model/--thinking`) | `internal/launcher/service.go:437,467,536` |
| **Provider/配置注入** | ✅ `ANTHROPIC_*` env（`BuildOverrides`） | ✅ `OPENCODE_CONFIG_CONTENT` env + `opencode_config.go` | ✅ `~/.codex/config.toml` 同步 | ✅ `models.json` + `PI_CODING_AGENT_DIR`（`pi_config.go`） | 见 ③ |
| **全局配置文件编辑器 API** | ➖ | ✅ `GetOpenCodeConfig/Save` | 🟡 `config.toml` 同步（非自由编辑） | ❌ 无（pi 无单一全局配置文件） | `app.go:3821-3836` |
| **默认预设种子** | ➖ nil | ➖ nil | ➖ nil | ✅ `DefaultPiPresets()`（5 provider） | `internal/config/defaults.go:94-128` |
| **terminal_preset 桥接** | ✅ | ✅ | ✅ | ✅ `ResolveTerminalPreset("pi",…)` | `app.go:1661` |
| **PresetTarget / TerminalPreset 类型** | ✅ | ✅ codex/opencode/pi | ✅ | ✅ `PresetTargetPi`/`TerminalPresetPi` | `internal/config/types.go:45,133` |
| **插件/扩展管理（后端包）** | ✅ `internal/plugin` | ✅ `internal/opencodeplugin` | ✅ `internal/codexplugin` | ❌ **无 `internal/piplugin`** | `internal/{plugin,codexplugin,opencodeplugin}` |
| **插件/扩展管理（app.go 服务装配）** | ✅ `Plugins` | ✅ `OpenCodePlugins` | ✅ `CodexPlugins` | ❌ 无 `PiPlugins` 字段 | `app.go:150-152,229-231` |
| **插件/扩展管理（前端 API）** | ✅ `api/plugin.ts` | ✅ `api/opencodePlugin.ts` | ✅ `api/codexPlugin.ts` | ❌ 无 `api/piPlugin.ts` | `frontend/src/api/index.ts:16-19` |
| **插件/扩展管理（前端视图）** | ✅ ClaudeCode tab | ✅ OpenCode tab | ✅ Codex tab | ❌ ExtensionsView engineOptions 无 pi | `frontend/src/views/ExtensionsView.vue:141-143` |
| **用量统计（usage 常量）** | ✅ `appClaudeCode` | ✅ `appOpenCode` | ✅ `appCodex` | ❌ **无 `appPi` 常量** | `internal/usage/types.go:25-27` |
| **用量同步（usage/sync）** | ✅ Claude JSONL | ✅ opencode.db | ✅ codex | ❌ 无 pi 同步路径 | `internal/usage/sync.go:112,129` |
| **用量计费规则** | ✅ `ComputeBillableInput` | ✅ | ✅ | ❌ AppType="pi" 无规则 → 零用量记录 | `internal/usage/service.go:186,249` |
| **远程/WebUI 启动端点** | ✅ `/launch` | ✅ `/launch-opencode` | ✅ `/launch-codex` | ❌ **无 `/launch-pi`** | `internal/remote/handlers.go:20-22` |
| **远程启动元数据段** | ✅（Claude） | ✅ `launchMetaOpenCodeSection` | ✅ `launchMetaSection` | ❌ 无 Pi section | `internal/remote/session_launch_types.go:39-40` |
| **代理注入 (proxy)** | ✅ `LaunchSession(useProxy)` | ➖ 无（Claude 专属） | ➖ 无 | ➖ 无（非 pi 特有缺口） | `app.go:1155,1263-1301` |
| **Headroom 压缩** | ✅ per-session | ➖ 无 | 🟡 Codex 全局 headroom | ➖ 无 | `app.go:570-678` |
| **会话类型注册** | ✅ `AppTypeClaudeCode` | ✅ `AppTypeOpenCode` | ✅ `AppTypeCodex` | ✅ `AppTypePi` | `internal/session/types.go:9-12` |
| **前端会话启动 UI** | ✅ | ✅ | ✅ | ✅ `launchPiSession`/`SessionSettingsView` pi 分支齐全 | `frontend/src/api/session.ts:116`、`SessionSettingsView.vue:284` |

**一句话定性**：Pi 的 **启动/配置注入链路（launcher + config + 默认预设 + 会话 UI）已基本对齐**，真正的大缺口集中在三块——**插件/包管理、用量统计、远程启动**，这三块都是 OpenCode 与 Codex 双双具备、Pi 完全空白的「真缺口」。

---

## ② 逐层差距清单（按优先级排序）

### 🔴 P0-T1　插件 / 包管理全链路缺失（后端 + app.go + 前端）

Pi 插件 = pi package（npm 包或 git 仓库），安装写入 `~/.pi/agent/settings.json` 的 `packages` 数组，实体落 `~/.pi/agent/npm/` 或 `~/.pi/agent/git/`，CLI 入口 `pi install / pi list / pi remove / pi update`（见 ③）。Codebox 对此**完全没有封装**。

| # | 缺口 | 证据 |
|---|---|---|
| T1.1 | 后端无 `internal/piplugin` 包。对比：`internal/plugin`（Claude，442 行 service + reader/executor/metadata/state）、`internal/codexplugin`（service 774 行 + manifest/parser/reader/install_verify）、`internal/opencodeplugin`（service 597 行 + update.go 349 行 + executor）。Pi 无任何对应物。 | `internal/{plugin,codexplugin,opencodeplugin}/service.go`；`find internal -name '*piplugin*'` 无结果 |
| T1.2 | `app.go` App 结构体装配了 `Plugins`/`CodexPlugins`/`OpenCodePlugins` 三个服务，**无 `PiPlugins` 字段**；构造处也无 piplugin 服务实例化。 | `app.go:150-152`（字段）、`app.go:229-231`（装配） |
| T1.3 | app.go 暴露的插件 API 仅服务 Claude/Codex：`SetPluginSubItemEnabled` 按 `isClaudePlugin` 二分路由到 `Plugins` 或 `CodexPlugins`，无 pi 分支。（注：完整的 list/install/marketplace API 由各 plugin service 经 Wails 绑定直接暴露，前端通过 `api/plugin.ts`/`codexPlugin.ts`/`opencodePlugin.ts` 调用；pi 无对应绑定。） | `app.go:2673-2682`；`frontend/src/api/{plugin,codexPlugin,opencodePlugin}.ts` |
| T1.4 | 前端 `ExtensionsView.vue` 的引擎 tab `engineOptions` 仅 `claude/opencode/codex` 三项，**无 pi**；页面副标题写死「管理 Claude、OpenCode 与 Codex 插件」。 | `frontend/src/views/ExtensionsView.vue:3,141-143` |
| T1.5 | 前端 `components/extensions/` 有 `OpenCodePluginPanel.vue`、`PluginInstalledPanel.vue`（Claude/Codex 共用）、`PluginMarketPanel.vue`、`PluginSubItemsPanel.vue`，**无 `PiPluginPanel.vue`**。 | `frontend/src/components/extensions/` 目录 |
| T1.6 | 前端 `api/index.ts` 导出 `plugin`/`opencodePlugin`/`codexPlugin`，**无 `piPlugin`**；`stores/` 同理无 `piPlugin.ts`。 | `frontend/src/api/index.ts:16-19`；`frontend/src/stores/` |

### 🔴 P0-T2　用量统计与计费全链路缺失

Pi 会话经 `LaunchPiSession` 启动时 `Sessions.Create(..., false)`（useProxy=false），既不走注入代理，usage 包内也无 pi 的常量/同步/计费规则，因此 **Pi 会话产生零用量记录**，用量视图对 Pi 不可见。

| # | 缺口 | 证据 |
|---|---|---|
| T2.1 | `internal/usage/types.go` 仅定义 `appClaudeCode="claudecode"`/`appCodex="codex"`/`appOpenCode="opencode"` 三个常量，**无 `appPi`**。 | `internal/usage/types.go:25-27` |
| T2.2 | `internal/usage/sync.go` 分别从 Claude JSONL、codex、`~/.local/share/opencode/opencode.db` 同步（line 112/129/254/356/437），**无 pi 同步分支**。 | `internal/usage/sync.go:112,129,437-478` |
| T2.3 | `usage/service.go:249` 的 `generateDedupKey` 按 `evt.AppType` switch，`usage/service.go:186` 的 `ComputeBillableInput(evt.AppType,…)` 同理——Pi 的 AppType="pi" 无匹配分支，即便有事件也走不到正确计费。 | `internal/usage/service.go:186,249-266` |
| T2.4 | `internal/proxy/usage.go` 的 `AppType` 注释明确写「claudecode / codex / opencode」，且 Pi 不经代理，实时 usage 钩子对 Pi 不生效。 | `internal/proxy/usage.go:192` |
| T2.5 | `usage/metadata.go:149` 对 `appCodex` 有 unknown-provider 归一化特判，无 pi 对应。 | `internal/usage/metadata.go:149` |

【待核验：Pi 会话以 JSONL 存于 `PI_CODING_AGENT_SESSION_DIR`（默认 `~/.pi/agent/sessions`，见 `docs/session-format.md`），其结构与 Claude JSONL 不同；usage sync 能否复用同一解析器、或需独立 parser，需读 pi 的 `session-format.md` 后确认——本次未展开。】

### 🔴 P0-T3　远程 / WebUI 启动缺失

远程服务器（手机/网页控制端）无法启动 Pi 会话，也无法在启动元数据里展示 Pi 的可用 provider/preset。

| # | 缺口 | 证据 |
|---|---|---|
| T3.1 | `internal/remote/handlers.go` 注册 `/api/sessions/launch`、`/launch-codex`、`/launch-opencode` 三条路由及对应 `handleLaunch*`，**无 `/launch-pi`**。 | `internal/remote/handlers.go:20-22,141,168,193` |
| T3.2 | `internal/remote/session_launch_types.go` 的 `launchMeta` 结构含 `OpenCode`/`Codex` 两个分段（line 39-40），各带 `Providers`/`Presets`，**无 `Pi` 分段**，故远程端无法枚举 Pi 可用预设。 | `internal/remote/session_launch_types.go:27-40` |

### 🟡 P1-T4　全局配置文件编辑器（设计差异，非纯缺口）

| # | 现状 | 证据 |
|---|---|---|
| T4.1 | OpenCode 有独立的 `internal/opencodeconfig` 服务 + app.go 暴露 `GetOpenCodeConfig`/`SaveOpenCodeConfig`/`GetOpenCodeConfigPath`，前端设置页可直接编辑 `~/.config/opencode/opencode.json`。Pi 无等效「全局配置文件编辑器」API。 | `internal/opencodeconfig/service.go`（全文）；`app.go:3821-3836` |
| 定性 | Pi **无单一全局配置文件**——配置散在 `~/.pi/agent/settings.json`（包/设置）+ `~/.pi/agent/models.json`（provider/model）。Codebox 当前采取 **每次启动覆盖写隔离目录** `<configDir>/pi-runtime/models.json` + 注入 `PI_CODING_AGENT_DIR`（见 `app.go:1730-1739`、`pi_config.go:WritePiAgentConfig`），完全不碰用户 `~/.pi/agent/`。这是**合理隔离设计**，并非缺口；但若需让用户在 UI 里查看/编辑 Pi 的 settings.json（packages 列表等），则缺一个 piconfig 服务。 | `internal/launcher/pi_config.go:97-146`；`app.go:1710-1748` |

### 🟡 P1-T5　envcheck 检测深度（次要）

| # | 现状 | 证据 |
|---|---|---|
| T5.1 | `checker_pi.go`（176 行）实现：可执行文件解析、版本探测（`--version`/`-v` 双探）、安装方式判定（npm/native）、npm global prefix 兜底。功能上与 `checker_codex.go`（177 行）**基本对等**。 | `internal/envcheck/checker_pi.go`（全文） |
| T5.2 | `checker_opencode.go`（408 行）更厚，但多出来的主要是**版本探测的稳健性兜底**（`openCodeVersionFromNPMList`、`openCodeVersionFromNPMRootManifest`、`readPackageJSONVersion`、多种诊断/回退），**不是配置/鉴权校验**。Pi checker 缺这类兜底，但核心安装/版本检测可用。 | `internal/envcheck/checker_opencode.go:117-349` |
| T5.3 | 配置自愈（`claude_selfheal.go`、`FixClaudeConfig`）是 **Claude 专属**，OpenCode/Codex 同样没有——非 pi 特有缺口。 | `internal/envcheck/claude_selfheal.go`、`app.go:490-496` |
| 备注 | Pi 安装器完备：npm 全局装 `@earendil-works/pi-coding-agent@latest`、self-update 走 `pi update self`，envcheck 设置页已含 pi 项。 | `internal/envcheck/installer.go:1556-1572`；`frontend/src/views/settings/EnvCheckSettings.vue:381` |

### ⚪ 非缺口（澄清：以下看似缺失，实为设计如此或 Pi 已对齐）

- **代理(proxy)注入**：`useProxy`/`useHeadroom` 仅是 `LaunchSession`（Claude）的参数（`app.go:1155`）；`LaunchCodexSession`/`LaunchOpenCode`/`LaunchPiSession` 均 `Sessions.Create(..., false)` 且不经 `a.Proxy`。代理是 Claude 专属能力，**非 Pi 相对 OpenCode/Codex 的缺口**。证据：`app.go:1263-1301`（proxy 仅在 LaunchSession 内 Start/SetProxyPort）。
- **Headroom**：per-session headroom 同为 Claude 专属；Codex 有独立的「Codex 全局 headroom」（`GetCodexGlobalHeadroom`/`SetCodexGlobalHeadroom`，写 `~/.codex/config.toml`，`app.go:570-678`）。Pi/OpenCode 均无。Pi 相对 Codex 缺一个「全局 headroom」等价物，但相对 OpenCode 不缺——归类为 P2 可选增强。
- **CLI 参数注入**：Pi 的 `--provider/--model/--thinking` 注入完整（`buildPiCmd`），甚至比 OpenCode（纯 env 驱动）更显式。
- **默认预设**：Pi 是**唯一**带内置预设种子的引擎（`DefaultPiPresets` 覆盖 anthropic/openai/glm/minimax/kimi，`defaults.go:94-128`），反而领先。

---

## ③ Pi 配置机制调研结论（基于 pi 官方文档）

> 文档源：`@earendil-works/pi-coding-agent` 的 `README.md` + `docs/{models,custom-provider,environment-variables,packages}.md`（路径见每条脚注）

### 3.1 配置目录与文件布局

- 默认配置目录 `~/.pi/agent`，可用环境变量 **`PI_CODING_AGENT_DIR`** 整体覆盖（`docs/environment-variables.md`「Pi Process Configuration」）。
- 会话目录 `PI_CODING_AGENT_SESSION_DIR`（默认 `~/.pi/agent/sessions`），包目录 `PI_PACKAGE_DIR`。
- 两类配置文件：
  - **`models.json`**（provider/model 注册）——位于配置目录根。
  - **`settings.json`**（包列表、设置）——用户级 `~/.pi/agent/settings.json`，项目级 `.pi/settings.json`。

### 3.2 Provider / Model 注入（`docs/models.md:23-376`）

`models.json` 顶层 `providers.{name}` 字段：

| 字段 | 说明 | 证据 |
|---|---|---|
| `baseUrl` | API endpoint URL（自定义 provider 必填） | `models.md:136` |
| `api` | `openai-completions` / `openai-responses` / `anthropic-messages` / `google-generative-ai` | `models.md:130` |
| `apiKey` | 支持字面量 / `$ENV` / `${A}_${B}` / `!shell-cmd`；可省略（改用 `/login`/`auth.json`/`--api-key`） | `models.md:138,149-172` |
| `authHeader` | `true` 时自动加 `Authorization: Bearer <apiKey>` | `models.md:140` |
| `headers` | 自定义请求头（同样支持 `$ENV`/`!cmd`） | `models.md:184-188` |
| `models[]` | 模型声明：`id`/`name`/`api`(覆盖)/`contextWindow`/`maxTokens`/`thinkingLevelMap`/`compat` | `models.md:200-210` |
| `compat` | `{supportsDeveloperRole, supportsReasoningEffort, forceAdaptiveThinking, allowEmptySignature}` | `models.md:39-41,370-376` |
| `modelOverrides` | 对内置/扩展注册模型的逐项覆盖，不改写整份 model 列表 | `models.md:320-366` |
| `oauth` | 动态 OAuth（如 `"radius"`，需配合 gateway `baseUrl`） | `models.md:140` |

**自定义命名 provider**：pi 允许任意 provider id（不限于内置的 anthropic/openai/google/…），用 `--provider <id>` 引用——这正是 Codebox 采用 `amagi-<providerName>` 命名隔离的依据（`pi_config.go:PiProviderID`）。

### 3.3 CLI 参数（`README.md` CLI Reference + `models.md`）

- `--provider <id>`、`--model <pattern>`、`--thinking <level>`（值域 `off/minimal/low/medium/high/xhigh/max`，与 `PI_REASONING_LEVEL` 一致，`environment-variables.md`）、`--api-key`。
- `pi update --models` 强刷 provider 模型目录；`pi update --self` 自更新。

### 3.4 包管理（`docs/packages.md`，全篇）

```
pi install npm:@foo/bar@1.0.0        # 写入 settings.json packages[]，实体落 ~/.pi/agent/npm/
pi install git:github.com/user/repo@v1   # 落 ~/.pi/agent/git/<host>/<path>
pi install /abs/path | ./rel/path    # 本地路径，不拷贝
pi remove npm:@foo/bar
pi list                              # 读 settings.json 展示已装包
pi update [--all|--extensions|--self|--models]
```
- `settings.json` 的 `packages` 数组支持字符串或 `{source, extensions[], skills[], prompts[], themes[]}` 过滤对象。
- 包 manifest：`package.json` 的 `pi` 键声明 `extensions/skills/prompts/themes` 路径，或约定目录自动发现（`extensions/`、`skills/`、`prompts/`、`themes/`）。
- 启用/禁用单资源：`pi config`（交互式，全局/项目切换）。

### 3.5 关键环境变量（`docs/environment-variables.md`）

| 变量 | 作用 |
|---|---|
| `PI_CODING_AGENT_DIR` | 覆盖配置目录（Codebox 注入隔离目录的关键变量） |
| `PI_CODING_AGENT_SESSION_DIR` | 覆盖会话存储 |
| `PI_PACKAGE_DIR` | 覆盖包目录 |
| `PI_OFFLINE` | 关闭启动期网络操作（更新检查/包更新/遥测） |
| `PI_SKIP_VERSION_CHECK` | 关闭 pi.dev 版本请求 |
| `PI_TELEMETRY` | 覆盖遥测与 provider 归因头 |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` 等 | provider 凭据（见 `docs/providers.md`） |

### 3.6 结论：Codebox 现有 pi 注入方式正确，方向对齐官方机制

- Codebox 的 `BuildPiModelsConfig`→`WritePiAgentConfig`→注入 `PI_CODING_AGENT_DIR`（`pi_config.go` + `app.go:1710-1748`）**完全符合** pi 文档的「自定义 provider via models.json + `PI_CODING_AGENT_DIR` 覆盖」机制，且用 `amagi-<name>` 命名隔离避免与内置 provider 冲突，设计正确。
- 现注入字段（`baseUrl`/`api`/`apiKey`/`models[]`/`contextWindow`/`maxTokens`/`reasoning`）覆盖了 pi 文档中自定义 provider 的**核心字段**；**未透传**的 pi 能力字段：`headers`、`authHeader`、`compat`（`supportsDeveloperRole`/`supportsReasoningEffort`）、`modelOverrides`、`thinkingLevelMap`、`oauth`。这些对部分第三方 OpenAI 兼容端点（如不支持 developer role 的服务）可能需要【待核验：是否有实际 provider 触发 compat 需求】。
- 因此 **provider/model 注入层无需大改**；真正待补的是「包管理」（应封装 `pi install/list/remove` 读写 `settings.json`，对标 opencodeplugin 封装 `opencode plugin`）与「用量同步」（pi 无 usage DB，需评估 JSONL 解析可行性）。

---

## ④ 实施项清单（按依赖关系排序，每项=对标既有基线实现）

> 以下为「补齐 Pi 至 OpenCode/Codex 同等支持」所需的工作项，按依赖前置关系排序；每项注明对标对象与落点，供下游（luban/fuxi）据以设计实现。

### 阶段 A：插件 / 包管理（P0-T1，依赖最少，可独立先行）

1. **新建 `internal/piplugin` 包**——对标 `internal/opencodeplugin`（service + types + executor + update）。封装 `pi install/list/remove/update` CLI，读写 `~/.pi/agent/settings.json` 的 `packages` 数组；实体扫描 `~/.pi/agent/npm/`、`~/.pi/agent/git/`。落点：`internal/piplugin/service.go`（参考 `opencodeplugin/service.go:597`）。
2. **app.go 装配 `PiPlugins` 服务**——在 App 结构体加字段、构造处实例化。落点：`app.go:150-152`（加字段）、`app.go:229-231`（加装配）。
3. **前端 `api/piPlugin.ts` + `stores/piPlugin.ts`**——对标 `api/opencodePlugin.ts`、`stores/opencodePlugin.ts`；在 `api/index.ts` 导出。落点：`frontend/src/api/index.ts:16-19`。
4. **ExtensionsView 增加 Pi engine tab**——`engineOptions` 加 `{value:'pi',label:'Pi'}`，新增 `PiPluginPanel.vue`（对标 `OpenCodePluginPanel.vue`），更新页面副标题。落点：`frontend/src/views/ExtensionsView.vue:3,141-143`。

### 阶段 B：远程 / WebUI 启动（P0-T3，依赖 app.go 已有 `LaunchPiSession`，独立可做）

5. **新增 `/api/sessions/launch-pi` 路由 + `handleLaunchPi`**——对标 `handleLaunchCodex`/`handleLaunchOpenCode`，调用 `a.app.LaunchPiSession`。落点：`internal/remote/handlers.go:20-22,168-205`。
6. **`launchMeta` 增加 Pi 分段**——`launchMetaPiSection{Providers,Presets}`，并在构建元数据处填充（读 `DefaultPiPresets` + 用户 terminal_presets.pi）。落点：`internal/remote/session_launch_types.go:27-40`、`handlers.go:130`（buildLaunchMeta）。

### 阶段 C：用量统计（P0-T2，需先确认 pi 会话格式，含【待核验】）

7. **`internal/usage/types.go` 增加 `appPi="pi"` 常量**——对标 line 25-27。落点：`internal/usage/types.go:25-27`。
8. **`generateDedupKey` / `ComputeBillableInput` 增加 pi 分支**——`service.go:249` switch、`service.go:186` 调用。落点：`internal/usage/service.go:186,249-266`。
9. **【前置调研】pi 会话 JSONL 解析可行性**——读 `docs/session-format.md`，判断能否复用 Claude JSONL parser 或需独立 parser；确定 usage 事件来源（pi 无 usage DB，事件只能从会话日志或代理钩子来）。**此项阻塞 T2 的 sync 实现**。
10. **（条件性）`usage/sync.go` 增加 pi 同步分支**——若步骤 9 可行，对标 `sync.go:437`（opencode.db 分支）新增 pi JSONL 同步。落点：`internal/usage/sync.go`。

### 阶段 D：增强项（P1，非阻塞，按需）

11. **`BuildPiModelsConfig` 透传 compat/headers/authHeader**——解决部分 OpenAI 兼容端点不支持 developer role / reasoning_effort 的问题（`models.md:39-41`）。落点：`internal/launcher/pi_config.go:BuildPiModelsConfig`（当前仅透传 contextWindow/maxTokens/reasoning）。
12. **（可选）`internal/piconfig` 全局 settings.json 查看器**——若需在设置页展示 Pi packages/设置；对标 `internal/opencodeconfig`。落点：新建包 + `app.go:3821` 风格 API。**优先级低**，因当前隔离注入设计已满足启动需求。

---

## 待核验项汇总

1. **【T2 阻塞】pi 会话 JSONL 结构**：`docs/session-format.md` 未读，无法确认 usage sync 能否复用 Claude 解析器；pi 无 usage DB，事件来源待定。
2. **piplugin ↔ opencodeplugin 结构映射**：opencodeplugin 走「全局 plugin 数组 + 包缓存」模型，pi 走「settings.json packages[] + npm/git 实体目录」模型，结构相近但实体路径与 enable/disable 语义（`pi config`）不同，移植时需逐项核对。
3. **compat 字段实际触发场景**：是否有 Codebox 已接入的 provider（如某些 OpenAI 兼容端点）确实需要 `supportsDeveloperRole:false`，需结合 provider 清单确认（`internal/config/defaults.go`）。
4. **`checker_opencode.go` 的版本兜底是否值得回移到 pi**：pi 仅 `--version`/`-v` 双探；若 pi 的 `--version` 在某些 npm 安装下不稳定，可借鉴 `openCodeVersionFromNPMList`/`readPackageJSONVersion` 兜底——需实测 pi 安装稳定性后决定。

---

## 已覆盖 / 未覆盖范围

- **已覆盖**：`internal/` 全部目录结构扫描；`launcher`/`config`/`envcheck`/`opencodeconfig`/`session`/`proxy`/`usage`/`remote` 关键文件逐行/抽样阅读；`app.go` 全部 App 方法签名 + 三个 Launch* 方法正文 + proxy/headroom 装配；前端 `api/session.ts`、`views/ExtensionsView.vue`、`api/index.ts`、`stores/` 目录；pi 官方 `README.md` + `docs/{environment-variables,packages}.md` 全文 + `docs/models.md` 关键段。
- **未覆盖**：`docs/custom-provider.md`（772 行）仅看标题未逐行读（其内容与 `models.md` provider 段重叠，核心结论已由 `models.md` 覆盖）；`docs/session-format.md`（usage sync 可行性依赖，标待核验）；`internal/headroom`/`internal/structured`/`internal/pty` 内部实现（经 grep 确认不按 app type 分支，非缺口）；前端 `stores/usage.ts`、`UsageView.vue` 内部实现（usage 缺口已由后端常量缺失坐实，前端必然无 pi 数据）。
