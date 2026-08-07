# Oh My Pi (omp) 集成设计

> 状态：待实现。仿照 amagi-codebox 现有 pi（@earendil-works/pi-coding-agent）集成模式，加入 oh-my-pi（@oh-my-pi/pi-coding-agent，CLI 命令 `omp`）支持。
> 调研证据：omp v17.2.10（brew can1357/tap/omp 安装，`/opt/homebrew/bin/omp`）；`PI_CODING_AGENT_DIR` 可 relocate agent 根（实测）；`~/.omp/agent/models.yml` 自定义 provider 注册实测通过（`omp models` 可见 `amagi-test`）；`omp -p --model <provider>/<model>` 请求路由实测生效；会话 JSONL 格式与 pi 同构（docs/session.md + packages/stats/src/parser.ts 双重确认）；cost 为 USD（stats formatter `$` 佐证，pi-ai fork 同源）。

## 背景：omp 与 pi 的关键差异

| 维度 | pi | omp |
|---|---|---|
| CLI 命令 | `pi` | `omp` |
| agent 根 | `~/.pi/agent` | `~/.omp/agent`（同样尊重 `PI_CODING_AGENT_DIR`） |
| 模型配置 | `models.json`（JSON） | `models.yml`（YAML，.json 会一次迁移） |
| 自定义 provider | providers → baseUrl/api/apiKey/headers/authHeader/models[] | 同构（api 枚举更宽：openai-completions/openai-responses/anthropic-messages/google-generative-ai/google-vertex/...） |
| 模型选择 | `--provider` + `--model` | `--model provider/model`（`--provider` 为 legacy 仍可用） |
| 思考 | `--thinking off|minimal|low|medium|high|xhigh|max` | 同值域 + `auto` |
| 会话 JSONL | `sessions/--<cwd>--/<ts>_<uuid>.jsonl` | `sessions/<dir-encoded>/<ts>_<id>.jsonl`（home 内相对编码，旧 `--cwd--` 兼容）；header `type:"session"` 含 cwd/id/parentSession；计费条目 message(assistant/toolResult)/compaction/branch_summary + usage{cost.total: USD}——**与 pi 解析器同构**；另有嵌套子会话目录 `<project>/<session>/<id>.jsonl`（subagent/advisor transcript，同样计费，递归扫描自然覆盖） |
| 插件机制 | `~/.pi/agent/settings.json` packages[]（npm/git/local） | Claude Code 兼容 marketplace（`omp plugin` CLI + `~/.omp/plugins/installed_plugins.json`）——**完全不同，一期不做插件管理 UI** |
| 安装 | npm `@earendil-works/pi-coding-agent` | brew `can1357/tap/omp` / curl install.sh / bun |

## 集成范围（对标 pi 支持，逐点映射）

### Go 后端

1. **`internal/session/types.go`**：新增 `AppTypeOhMyPi AppType = "omp"`。

2. **`internal/launcher/omp_config.go`（新）**：
   - `OmpProviderID(providerName string) string` = `"amagi-" + providerName`（命名隔离，复刻 PiProviderID）。
   - `BuildOmpModelsConfig(providerName, provider, modelName, apiKey, params)` → `map[string]any`：结构复刻 BuildPiModelsConfig，产出 `providers.<amagi-name>` 条目（baseUrl/api/apiKey/headers/authHeader + models[] 单模型：id/name/contextWindow/maxTokens/reasoning + compat）。api 值映射：OpenAI 兼容 → `openai-completions`；否则 `anthropic-messages`（与 piAPIType 同判定）。headers 沿用 `$ENV:VAR` / `${ENV:VAR}` 解析（resolveEnvHeaders 复用）。
   - `WriteOmpAgentConfig(agentDir, cfg)`：原子写 `~/.omp/agent/models.yml`，agentDir 0700 / 文件 0600（复刻 WritePiAgentConfig 的 MkdirAll→tmp→Rename→Chmod 范式）。**输出 YAML**（gopkg.in/yaml.v3 序列化；go.mod 新增依赖 + `go mod vendor`）。
   - `MergeOmpModelsConfig(cfg, agentDir)`：读现有 models.yml（yaml.v3 解析为 map）合并 providers（用户已有 provider 保留，amagi 同名优先），再写回。顶层其他字段（equivalence 等）保留。
   - 复用 `readPiJSONObject`-等价物：新增 yaml 读取辅助（YAML 解析失败按空处理）。

3. **`internal/launcher/service.go`**：`LaunchOmp(sessionID, provider, model, thinking, mode, workDir, envOverrides)` + `buildOmpCmd(...)`（复刻 LaunchPi/buildPiCmd：`--provider`、`--model`、`--thinking` 附加，`resolveCLIPath("omp", env)`）。

4. **`app.go`**：
   - `defaultOmpAgentDir()` = `~/.omp/agent`（复刻 defaultPiAgentDir）。
   - `ompProviderMapping(p)` 回退映射（Anthropic→anthropic/ANTHROPIC_API_KEY；OpenAI→openai/OPENAI_API_KEY；复刻 piProviderMapping）。
   - `resolveOmpLaunchSettings(provider, requestedModel, params)`（复刻 resolvePiLaunchSettings：ReasoningEffort→--thinking 级别；Thinking disabled→off）。
   - **`LaunchOmpSession(modelName, providerID, mode, workDir, shellPath)`**（复刻 LaunchPiSession 全链路）：
     - terminal_presets 桥接（`ResolveTerminalPreset("omp", modelName)`）→ provider/model/params。
     - envOverrides `{"PI_CODING_AGENT_DIR": ""}`（清除，强制 ~/.omp/agent 默认根）。
     - 写 models.yml：BuildOmpModelsConfig → MergeOmpModelsConfig → WriteOmpAgentConfig；成功则 Provider=OmpProviderID；失败回退 ompProviderMapping。
     - API key 双路冗余注入（复刻 pi）。
     - embedded：args `--provider/--model/--thinking` → resolveEmbeddedLaunchSpec(AppTypeOhMyPi, ...) → launchEmbeddedPTY。
     - terminal：`a.Launcher.LaunchOmp(...)`。
   - Sessions.Create(AppTypeOhMyPi, "omp", ...)（appName "omp"）。

5. **`internal/envcheck`**：
   - `checker_omp.go`（新）：`checkOmp` 复刻 checkPi（PATH 探测 → npm global 兜底 `ompNPMGlobalExecutableCandidates` → `ompVersion` 跑 `omp --version` → `detectOmpInstallMethod`：路径含 node_modules/npm → NPM，含 `Cellar`/`homebrew` → Brew，否则 Native）。
   - `service.go`：`ToolOmp` 常量 + `case ToolOmp: s.checkOmp()` + SupportedTools/IsValidCLITool 登记。
   - `installer.go`：`case ToolOmp`：Update + 外部托管 → `ompSelfUpdateCommand`（omp 自带更新？未确认——brew 装的可 `brew upgrade`；npm 装的可 `npm i -g @oh-my-pi/pi-coding-agent@latest`。用 npm 命令兜底：`npm install -g @oh-my-pi/pi-coding-agent`；brew 检测到 InstallMethodBrew 时用 `brew upgrade can1357/tap/omp`）——**实现时按现有 installer 平台模式落地，npm 命令与 brew 命令并存**。`displayToolName` → "Oh My Pi (omp)"。
   - 命令名注册表（checker_common.go）："omp"。

6. **`internal/appmeta/omp/parser.go`（新）**：复刻 appmeta/pi/parser.go（ExtractUsageRecords / 内容指纹 dedup / 四类计费条目 / CostProvided=USD）。dedup 前缀 `"omp:"`。差异注释：omp 目录编码 home-relative（header cwd 权威，path 推断仅 fallback）；嵌套子会话 transcript 也会被枚举（递归扫描），内容指纹天然去重。

7. **`internal/usage/types.go`**：`appOmp = "omp"`、`dedupPrefixOmp = "omp:"`。
   **`internal/usage/sync.go`**：
   - `normalizeOmpProvider`（剥 `amagi-` 前缀，复刻 normalizePiProvider）。
   - `enumerateOmpSessionFiles(home)`：单根 `~/.omp/agent/sessions`（递归 walkFiles 复用；无旧版隔离根，pi-runtime 不适用）。
   - `syncOmpJSONL` / `updateSyncStateOmp`（state key `"omp_jsonl"`，复刻 pi 版）。
   - 主同步循环追加 `=== 5. Omp jsonl ===` 段（复刻 pi 段）。

8. **`internal/settings/service.go`**：`OmpMode`/`OmpShell` 字段 + 默认（embedded）+ GetDashboardDefaults/SetDashboardDefaults 透传（复刻 PiMode/PiShell 模式）。

9. **`internal/remote`**：
   - `contract/scalars.go`：`CLITypeOmp CLIType = "omp"` + KnownCLITypes。
   - `session_catalog.go`：cliLabel → "Oh My Pi"。
   - `handlers.go`：`POST /api/sessions/launch-omp` → handleLaunchOmp（复刻 handleLaunchPi）；`app_interface.go` 加 `LaunchOmpSession`。

10. **`docs/api.md`**：补 LaunchOmpSession 与远端 launch-omp；CLAUDE.md "三个 AI-CLI 应用" 描述更新。

### 前端（frontend/src）

11. `composables/useDashboardState.ts`：engine 联合类型加 `'omp'`；新增 `ompProvider/ompModel/ompMode/ompShell/ompCustomShellPath`（init/persist/reset 复刻 pi 字段）。
12. `views/SessionSettingsView.vue`：engineOptions 加 `{value:'omp', label:'Oh My Pi'}`；表单区块复刻 pi 区块（provider 选择 getProvidersByType('openai')、model、mode、shell、preset 走 getMergedTerminalPresets('omp')）。
13. `composables/useSessionLaunch.ts`：omp 分支 → `api/session.ts launchOmpSession`。
14. `api/session.ts`：`launchOmpSession` 包装 wailsjs `LaunchOmpSession`。
15. `views/UsageView.vue`：过滤选项加 `omp`（label "Oh My Pi"）。
16. `views/settings/EnvCheckSettings.vue`：TOOL_METAS 加 `{key:'omp', displayName:'Oh My Pi', iconChar:'O'}`。
17. `mobile/src/components/lobby/CliLauncher.vue`：加 omp 卡片。
18. **wailsjs 重新生成**：`LaunchOmpSession` 绑定（wails dev/build 自动；验证期用 `wails generate` 或构建产物核对）。

### 一期不做（边界披露）

- **omp 插件管理 UI**：marketplace 格式与 pi 的 settings.json packages[] 完全不同，piplugin 模式不可复用；`omp plugin` CLI 封装 + 前端面板留待二期。
- 代理注入（proxy headroom）：pi 直连不经 proxy，omp 同处理（不接入 proxy 引擎）。

## 测试清单

- `launcher/omp_config_test.go`：BuildOmpModelsConfig 各 provider 形态（openai/anthropic/headers env 引用/thinkingLevelMap）；WriteOmpAgentConfig 权限（0600/0700）+ YAML 可解析；MergeOmpModelsConfig 保留用户 providers。
- `launcher/service_test.go` 扩展：buildOmpCmd 参数拼装。
- `envcheck/checker_omp_test`：PATH 命中 / npm global 兜底 / 版本解析 / 安装方法判定（brew/npm/native）。
- `appmeta/omp/parser_test.go`：四类计费条目、fork dedup、增量 offset。
- `usage` 同步测试：omp 枚举 + sync 状态键。
- `settings/service_test.go`：OmpMode/OmpShell 默认与透传。
- `internal/remote` m2a 集成测试补 launch-omp（对标 launch-pi）。
- 前端：`npm run build`（vue-tsc typecheck 门禁）。
- 实测：`omp -p --model amagi-<x>/<model>` 全链路（L1 冒烟）。

## 风险与审核

- 新增依赖 `gopkg.in/yaml.v3` + vendor 提交：依赖路径，**diting 审核位必过**。
- 修改 usage/sync.go 主循环（各引擎平行段，加 omp 不触碰现有段）：低风险增量。
- wailsjs 生成物不手改：由 wails 工具链重新生成。
- AppType "omp" 命名与 remote CLIType 对齐；前端 engine 联合类型同步。
