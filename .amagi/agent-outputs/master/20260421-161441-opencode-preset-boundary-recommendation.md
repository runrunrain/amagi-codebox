# OpenCode 预设实现边界补强建议

## 问题定义
- 核心挑战：在最小改动下，让 `openai` 类型 Provider 同时承载 Codex 与 OpenCode，但通过 `preset.target` 和原始配置透传把两者运行边界彻底分开，并覆盖 `opencode.json` 的高阶能力。

## 推荐方案
1. 在 `Preset` 上新增最小字段集：`target`, `opencode_model`, `opencode_provider`, `opencode_config`, `runtime_env`。
2. `target` 取值固定为 `codex | opencode | universal`；旧数据默认为 `universal`，但 OpenCode UI 只显示 `opencode|universal`，Codex UI 只显示 `codex|universal`。
3. `opencode_model` 保存最终 `provider/model`；`opencode_provider` 仅在需要把 UI provider 名与 OpenCode provider id 解耦时使用。
4. `opencode_config` 用 `map[string]any`/`Record<string, any>` 原样保存任意高级字段，禁止结构化拆散 `mcp`、`agent`、`command`、`permission`、`plugin`、`watcher`、`compaction`、`experimental` 等开放配置。
5. `runtime_env` 仅保存必须通过环境变量注入且不适合落盘的运行时覆盖，如 `OPENCODE_CONFIG`, `OPENCODE_CONFIG_CONTENT`, `OPENCODE_CONFIG_DIR` 等。
6. LaunchOpenCode 唯一推荐：`runtime opencode.json 合并方案`，不是 env-only。原因是 OpenCode 配置本身按“远程/全局/自定义路径/项目/opencode.json/OPENCODE_CONFIG_CONTENT”分层合并，纯 env 无法覆盖 agents、commands、permissions、mcp、plugin 等完整能力。
7. 实施方式：启动前在会话隔离目录生成临时 `opencode.json`，内容为“Provider 基础认证映射 + Preset.opencode_config + 由 preset/model 推导出的 model/provider 覆盖”；再通过 `OPENCODE_CONFIG=<temp file>` 注入，必要的小型临时差异可继续叠加 `OPENCODE_CONFIG_CONTENT`。

## 精确改动清单
- `internal/config/types.go`
  - 扩展 `Preset`：新增 `Target string`, `OpenCodeModel string`, `OpenCodeProvider string`, `OpenCodeConfig map[string]any`, `RuntimeEnv map[string]string`。
- `internal/config/service.go`
  - 增加 preset 归一化：缺省 `target` 置为 `universal`；保存时保证 map 非 nil。
  - `Load()` 中补一轮 presets migration，避免旧数据无 target。
- `app.go`
  - 修改 `LaunchOpenCode` 签名为至少接收 `presetName`；启动时读取 preset。
  - 新增 `resolveOpenCodeLaunchConfig(...)` / `prepareOpenCodeSessionConfig(...)`：生成隔离 `opencode.json`、返回 `envOverrides`。
  - `buildOpenCodeEnvOverrides(...)` 保留认证职责，不再承担完整功能表达。
  - `GetProvidersByType` 保持 provider 级过滤；新增 preset 级过滤辅助逻辑供前端/remote 使用。
- `internal/launcher/service.go`
  - `LaunchOpenCode(...)` 保持接收 envOverrides，但允许传入 `OPENCODE_CONFIG` / `OPENCODE_CONFIG_CONTENT`。
- `frontend/wailsjs/go/models.ts`
  - 同步生成后的 `config.Preset` 类型字段。
- `frontend/src/views/Dashboard.vue`
  - `openCodeProviders` 保留 provider.type=openai 过滤。
  - 新增 `openCodeAvailablePresets`：只展示 `target in [opencode, universal]`。
  - `codexAvailablePresets` 改为只展示 `target in [codex, universal]`，彻底阻止 Codex 菜单混入 OpenCode 预设。
  - `LaunchOpenCodeWithProvider` 改为传 `providerID + presetName + mode + workDir + shellPath`（或对应新签名）。
- `frontend/src/views/Settings.vue`
  - OpenCode 默认 provider/preset 下拉按 preset.target 过滤。
- `frontend/src/views/ProviderDetail.vue`
  - 预设编辑弹窗新增 `target`、`opencode_model` 两个显式字段。
  - 增加“OpenCode 高级 JSON”编辑区，直接编辑 `opencode_config`；不要把高阶 schema 平铺到表单。
  - `runtime_env` 仅暴露少量高级键值编辑器。
- `API.md`
  - 更新 `LaunchOpenCode` 参数、Preset 新字段、兼容规则。
- `app_test.go`
  - 新增 OpenCode preset 选择、隔离配置生成、旧数据迁移、Codex/OpenCode preset 过滤测试。

## 兼容策略
- 旧 Provider 不变；仍允许 `type=openai` 供 Codex/OpenCode 共用。
- 旧 Preset 无 `target` 时迁移为 `universal`，保证现有 Codex 行为不炸。
- 若旧 preset 只有 `model`，则 OpenCode 默认把它解释为 `opencode_model` 的回退值。
- JSON 导入导出与 JSON 编辑器必须完整保留新增字段，未知的 `opencode_config` 子键不得丢失或重排语义。

## 测试重点
- 旧 `models.json` 读入后自动补 `target=universal`。
- Codex 列表不显示 `target=opencode`；OpenCode 列表不显示 `target=codex`。
- `opencode_config` 中 `agent/command/mcp/plugin/permission/instructions/disabled_providers/enabled_providers/experimental` 往返保存完全保真。
- 启动 OpenCode 时生成的临时 `opencode.json` 与环境变量合并后，`model/provider/options` 能正确覆盖全局配置。
- 会话结束后临时配置目录清理、并发多会话互不串配置。

## 验证结果
- 本次为架构补强建议输出，未修改代码，未执行构建/测试。

## 建议下一步
- 按上述清单先落地 `Preset.target + opencode_config + LaunchOpenCode(presetName)` 三项主线，再补前端过滤与迁移测试。
