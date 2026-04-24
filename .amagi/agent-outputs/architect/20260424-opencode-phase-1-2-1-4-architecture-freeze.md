# OpenCode 配置体系重构 Phase 1.2-1.4 架构冻结方案

## 一、需求背景

当前配置体系存在四个结构性问题：

1. `Provider` 与 `Preset` 强耦合，导致 Provider 不能成为跨终端复用的通用配置中心。
2. `ProviderDetail.vue` 同时承担 Provider 编辑、Preset 编辑、`preset.opencode_config` 结构化 GUI，职责过重。
3. OpenCode 全局配置虽然已经独立存在于 `~/.config/opencode/opencode.json`，但 UI 入口和数据心智仍然部分绑定在 provider preset 上。
4. `target=codex|opencode` 只能做粗粒度分流，无法支撑 Claude Code / OpenCode / Codex 三套真正独立的预设体系。

本次冻结目标不是继续在旧树形结构上打补丁，而是确定新的稳定边界：

- Provider 负责“通用服务商能力”
- 各 CLI 负责“各自预设与启动体验”
- OpenCode 全局配置继续以 `~/.config/opencode/opencode.json` 为唯一全局源
- GUI 优先，JSON 只作为高级兜底

## 二、方案对比

| 方案 | 描述 | 优点 | 缺点 | 可行性 |
|------|------|------|------|--------|
| A. 保持 `models.json -> provider -> presets` 结构，仅扩展 `target` | 改动小，在旧结构上继续分流 | 迁移成本低 | 页面职责仍混乱；OpenCode 全局与 preset 边界继续模糊；三 CLI 无法真正独立 | 中 |
| B. Provider 与 CLI Preset 解耦，OpenCode 全局配置单独归位 | Provider 成为通用配置中心；CLI 页面清晰；迁移规则可控 | 需要一次结构迁移和 UI 重排 | 高 |

### 决策矩阵

| 维度 | 权重 | 方案A | 方案B |
|------|------|------:|------:|
| 满足新方向 | 35 | 2 | 5 |
| 页面清晰度 | 20 | 2 | 5 |
| 后续扩展性 | 20 | 2 | 5 |
| 迁移风险可控性 | 15 | 4 | 4 |
| 实现复杂度 | 10 | 4 | 3 |
| 总分 | 100 | 2.5 | 4.7 |

结论：冻结采用方案 B。

## 三、新的信息架构（页面级）

### 3.1 一级页面

1. `服务提供商`
2. `终端配置`
   - `Claude Code`
   - `OpenCode`
   - `Codex`
3. `通用设置`
   - 保留现有 General / Shell / Terminal / Remote / Updates / About

### 3.2 页面职责冻结

#### A. 服务提供商页 `/providers`

职责：只管理跨终端复用的 Provider 基础资料。

列表卡片展示：

- provider 名称
- 协议格式：`anthropic | openai`
- base_url
- 默认模型
- 密钥状态
- 支持终端徽标：Claude Code / OpenCode / Codex（按协议推导）

#### B. 服务提供商详情页 `/providers/:id`

只保留三块：

1. 基本信息
2. 认证密钥
3. 模型与兼容性说明

明确删除：

- 预设列表
- 预设弹窗
- `preset.opencode_config` GUI

#### C. 终端配置总入口 `/settings/terminals`

作为新宿主页，包含三个子页签：

1. `Claude Code`
2. `OpenCode`
3. `Codex`

#### D. Claude Code 子页

子页签：

1. `预设`
2. `默认启动`

预设页字段：

- 预设名
- 绑定 Provider
- model
- temperature / top_p / max_tokens / max_context_length
- thinking
- stream

#### E. OpenCode 子页

子页签：

1. `预设`
2. `全局配置`
3. `默认启动`

其中：

- `预设` 只放会话级覆盖，不再承载完整 OpenCode 全局配置
- `全局配置` 对应 `~/.config/opencode/opencode.json`，成为 OpenCode 复杂配置唯一 GUI 入口

`全局配置` 页应承接当前 `Settings.vue` 已有的可视化/JSON 双模编辑能力，并从 `ProviderDetail.vue` 迁移原先的 OpenCode 结构化 GUI 思路。

#### F. Codex 子页

子页签：

1. `预设`
2. `默认启动`

预设页字段：

- 预设名
- 绑定 Provider
- model
- context_window.model_context_window
- context_window.model_auto_compact_token_limit
- temperature / top_p / max_tokens
- stream

## 四、新的数据模型草案

## 4.1 根结构

建议保持配置仍落在同一份 `models.json` 中完成 Phase 2+3，降低落地成本；但根结构改为“Provider 中心 + CLI 预设中心”。

```json
{
  "version": "2",
  "providers": {},
  "terminals": {
    "claude_code": { "presets": {}, "defaults": {} },
    "opencode": { "presets": {}, "defaults": {} },
    "codex": { "presets": {}, "defaults": {} }
  },
  "agent_teams": {}
}
```

### 4.2 实体一：ProviderProfile

归属边界：

- 存于 `models.json`
- 负责“服务商连接能力”
- 不再包含任何 CLI preset
- 不存储密钥明文，密钥仍在 SecretsService

关键字段建议：

```json
{
  "id": "openai",
  "display_name": "OpenAI 官方",
  "api_format": "openai",
  "base_url": "https://api.openai.com/v1",
  "default_model": "gpt-5",
  "auth_key": "OPENAI_API_KEY",
  "enabled": true,
  "url_history": [],
  "metadata": {
    "opencode_provider_id": "openai",
    "supports": {
      "claude_code": false,
      "opencode": true,
      "codex": true
    }
  }
}
```

字段冻结说明：

- `api_format`: 取代当前语义较弱的 `type`，固定为 `anthropic | openai`
- `auth_key`: 延续现有枚举，避免 Secret 存储链路重写
- `metadata.opencode_provider_id`: 仅用于 OpenCode provider id 与本地 provider id 解耦；缺省时按规则推导
- `metadata.supports.*`: 可不必落盘，前端可由 `api_format` 推导；若落盘，视为缓存字段，不作权威源

### 4.3 实体二：ClaudeCodePreset

归属边界：

- 存于 `terminals.claude_code.presets`
- 只服务 Claude Code

建议结构：

```json
{
  "preset_id": "anthropic__default",
  "name": "default",
  "provider_id": "anthropic",
  "model": "claude-3-7-sonnet-20250219",
  "parameters": {
    "temperature": 0.2,
    "top_p": 1,
    "max_tokens": 8192,
    "max_context_length": 200000,
    "thinking": { "type": "enabled", "budgetTokens": 16384 },
    "stream": true
  },
  "enabled": true
}
```

### 4.4 实体三：CodexPreset

归属边界：

- 存于 `terminals.codex.presets`
- 只服务 Codex

建议结构：

```json
{
  "preset_id": "openai__balanced",
  "name": "balanced",
  "provider_id": "openai",
  "model": "gpt-5.4",
  "parameters": {
    "temperature": 0.2,
    "top_p": 1,
    "max_tokens": 8192,
    "stream": true,
    "context_window": {
      "model_context_window": 1047576,
      "model_auto_compact_token_limit": 105197
    }
  },
  "enabled": true
}
```

### 4.5 实体四：OpenCodePreset

归属边界：

- 存于 `terminals.opencode.presets`
- 只放“会话级覆盖”
- 不承担 OpenCode 全局配置职责

建议结构：

```json
{
  "preset_id": "openai__research",
  "name": "research",
  "provider_id": "openai",
  "model": "gpt-5",
  "overlay": {
    "model": "openai/gpt-5",
    "permission": {
      "edit": "allow",
      "bash": "allow"
    },
    "mcp": {},
    "agent": {}
  },
  "ui_state": {
    "visual_mode": true,
    "expanded_sections": ["provider", "mcp", "permission"]
  },
  "enabled": true
}
```

字段冻结说明：

- `provider_id`: 允许为空；为空表示启动时沿用本机 OpenCode 登录 / 全局 provider 解析
- `model`: 业务字段，供表单编辑与启动时快速覆盖
- `overlay`: 原 `opencode_config` 的新归属名，仍保持任意 JSON 对象保真
- `ui_state`: 纯前端体验字段，可落盘也可本地缓存；不是运行时权威配置

### 4.6 实体五：TerminalDefaults

归属边界：

- 存于 `terminals.<cli>.defaults`
- 只管理默认启动选择，不混入 Provider 结构

字段建议：

```json
{
  "default_provider_id": "openai",
  "default_preset_id": "openai__balanced",
  "default_mode": "terminal",
  "default_shell": "",
  "use_proxy": false
}
```

### 4.7 OpenCodeGlobalConfig

归属边界：

- 唯一存储位置：`~/.config/opencode/opencode.json`
- 不写回 `models.json`
- GUI 编辑入口在 `终端配置 -> OpenCode -> 全局配置`

该文件负责的字段范围冻结为：

- `provider`
- `agent`
- `mcp`
- `permission`
- `instructions`
- `plugin`
- `experimental`
- 其他 OpenCode 原生顶层字段

## 五、迁移规则

### 5.1 Provider 迁移

旧：

```json
models[providerName] = {
  type,
  base_url,
  default_model,
  auth_key,
  presets
}
```

新：

```json
providers[providerName] = {
  id: providerName,
  display_name: providerName,
  api_format: normalize(type, auth_key),
  base_url,
  default_model,
  auth_key,
  url_history
}
```

规则：

1. `type=openai` 或 `auth_key=OPENAI_API_KEY` -> `api_format=openai`
2. 其他全部 -> `api_format=anthropic`
3. `presets` 不再写入 Provider

### 5.2 Preset 迁移总则

旧结构：`models[providerName].presets[presetName]`

新结构：迁移到 `terminals.<cli>.presets[preset_id]`

`preset_id` 生成规则冻结为：

```text
{providerName}__{presetName}
```

这样可避免不同 Provider 下同名 preset 冲突。

### 5.3 旧 target 到新 CLI 的映射规则

按以下顺序判定：

1. `target == "opencode"` -> `terminals.opencode.presets`
2. 否则若 provider `api_format == "anthropic"` -> `terminals.claude_code.presets`
3. 否则若 provider `api_format == "openai"` -> `terminals.codex.presets`

说明：

- 这是与当前 Dashboard 真实过滤逻辑一致的无损迁移
- 旧的 `target=codex` 在 Anthropic Provider 下实际上服务 Claude Code，在 OpenAI Provider 下服务 Codex

### 5.4 `opencode_config` 迁移规则

#### 情况 A：旧 preset 判定为 OpenCode preset

```json
oldPreset.opencode_config -> newOpenCodePreset.overlay
```

同时：

- 若 `overlay.model` 缺失且旧 `preset.model` 非空，则写入运行时派生字段，不强制回写到 `overlay`
- 保持原始 JSON 对象保真，不做 schema 收缩

#### 情况 B：旧 preset 判定为 Claude/Codex preset，但仍带有 `opencode_config`

迁移规则冻结为：

1. 不丢弃原数据
2. 不自动塞入 Claude/Codex preset
3. 在迁移结果中生成一条同名 OpenCode preset：

```text
preset_id = {providerName}__{presetName}__migrated_oc
name = {presetName}-opencode
overlay = oldPreset.opencode_config
provider_id = providerName
```

原因：这比静默丢数据更安全，也比塞进错误终端更可追踪。

### 5.5 OpenCode 全局配置迁移规则

不迁移、不复制、不反写。

即：

- `~/.config/opencode/opencode.json` 保持原位
- 旧 `preset.opencode_config` 不再承担“全局配置子集”的角色
- 全局 GUI 以后只读写 `~/.config/opencode/opencode.json`

## 六、兼容规则

### 6.1 Claude Code 消费规则

消费来源：

1. `providers[provider_id]`
2. `terminals.claude_code.presets[preset_id]`

选择规则：

- 只展示 `api_format=anthropic` 的 Provider
- 只展示 `provider_id` 命中的 Claude Code preset
- 忽略 OpenCode 全局文件
- 忽略 OpenCode overlay

### 6.2 Codex 消费规则

消费来源：

1. `providers[provider_id]`
2. `terminals.codex.presets[preset_id]`

选择规则：

- 只展示 `api_format=openai` 的 Provider
- 只展示 `provider_id` 命中的 Codex preset
- 忽略 OpenCode 全局文件
- 忽略 OpenCode overlay

### 6.3 OpenCode 消费规则

消费来源：

1. `~/.config/opencode/opencode.json`
2. `providers[provider_id]` 生成的 provider 运行时补丁
3. `terminals.opencode.presets[preset_id].overlay`
4. 启动时临时 env 覆盖

优先级冻结为：

```text
OpenCode 全局文件
  < Provider 运行时补丁
  < OpenCode preset.overlay
  < 本次启动临时覆盖
```

### 6.4 OpenCode 如何选择 provider 格式

#### 当 `api_format = anthropic`

- 若 `base_url` 包含 `api.anthropic.com` -> OpenCode provider id 固定为 `anthropic`
- 否则 provider id 使用 `metadata.opencode_provider_id ?? provider.id`
- `options.baseURL` 仅在非官方地址时注入
- 认证使用 Anthropic 对应密钥环境变量/运行时 options.apiKey

#### 当 `api_format = openai`

- 若 `base_url` 包含 `api.openai.com` -> OpenCode provider id 固定为 `openai`
- 否则 provider id 使用 `metadata.opencode_provider_id ?? provider.id`
- `options.baseURL` 注入为 `provider.base_url`
- 认证使用 `OPENAI_API_KEY`

### 6.5 OpenCode “不指定 Provider”兼容规则

- 允许继续保留
- 此时只消费：`~/.config/opencode/opencode.json` + 选中的 OpenCode preset.overlay
- 不叠加 Provider 运行时补丁

## 七、第一批实现建议（今天先做 Phase 2+3 最小闭环）

### 7.1 必须先落地

#### M1. 数据结构拆分

必须先把 `Provider.presets` 从主模型中拆出去，至少在内存模型与前端消费模型上完成解耦。

最小要求：

- 新增 `providers`
- 新增 `terminals.claude_code.presets`
- 新增 `terminals.opencode.presets`
- 新增 `terminals.codex.presets`
- 保留旧 `models` 读入迁移，不要求长期双写

#### M2. ProviderDetail 职责收缩

必须先把 `ProviderDetail.vue` 的预设编辑能力下掉，至少不再作为唯一入口。

否则后续无论新数据结构还是新页面都会反复返工。

#### M3. OpenCode 全局配置归位

必须先把当前 `Settings.vue` 中的 OpenCode 编辑能力确认为“OpenCode 全局配置唯一入口”，并在信息架构上前置。

最小要求：

- 页面命名明确为“OpenCode 全局配置”
- 文案明确说明：这是 `~/.config/opencode/opencode.json`
- 不再把 `preset.overlay` 描述成全局配置的一部分

#### M4. 三终端独立预设页

必须先让 Claude Code / OpenCode / Codex 分别拥有独立 preset 列表和编辑入口。

哪怕第一版仍共用部分表单组件，也必须先拆页面边界。

#### M5. Dashboard 读取新来源

必须先把 Dashboard 的三个下拉框改成读取各自 CLI 的 preset 源，而不是继续从 `provider.presets` 过滤。

#### M6. OpenCode provider builder 同时支持 Anthropic / OpenAI

必须先把 OpenCode provider 列表从“只看 openai provider”改为“支持 anthropic + openai 两种格式”。

### 7.2 可以暂缓到 Phase 4/5

1. OpenCode 临时 `OPENCODE_CONFIG` 文件方案
2. OpenCode 全局配置更完整的 schema 校验
3. Provider 模型目录、模型拉取、模型探测
4. 预设导入导出细分到每个 CLI
5. OpenCode preset 的低频字段 GUI（plugin / experimental / compaction / command）
6. 预设复制、比较、批量迁移 UI
7. OpenCode 全局配置与 Provider 中心的双向同步

### 7.3 Phase 2+3 最小闭环推荐范围

建议今天只做以下闭环：

1. 数据迁移：旧 `models -> providers + terminals.*.presets`
2. 页面迁移：ProviderDetail 去 preset；新增三终端 preset 管理页
3. Dashboard 切新数据源
4. OpenCode 全局配置页保留现有 Visual/JSON 双模
5. OpenCode provider builder 支持 anthropic/openai

这样已经能完成：

- Provider 成为通用配置中心
- 三终端预设完全独立
- OpenCode 全局配置脱离 provider preset
- 用户高频配置走 GUI

## 八、风险控制点

### R1. 必须先定“Preset 主键策略”

若不先冻结 `preset_id={providerId}__{presetName}`，后续迁移、路由、默认项引用都会返工。

### R2. 必须先定“OpenCode 全局 vs Preset Overlay”的归属边界

若不先定，`agent/mcp/permission/plugin/instructions` 到底放全局页还是 preset 页会持续摇摆，ProviderDetail、Settings、Dashboard 三处都要改两遍。

冻结结论：

- 全局长期配置 -> `~/.config/opencode/opencode.json`
- 会话差异 -> `OpenCodePreset.overlay`

### R3. 必须先定“Provider 格式字段”

若继续混用 `type`、`auth_key`、页面过滤规则推断 provider 能力，OpenCode 支持 Anthropic / OpenAI 后会迅速失控。

冻结结论：

- 权威字段统一为 `api_format`
- 页面是否支持某 CLI 由 `api_format` 推导

### R4. 必须先定“Secrets 不回写全局文件”

否则 Provider 中心与 `opencode.json` 会互相覆盖，且容易把密钥意外落盘。

冻结结论：

- Provider 密钥继续留在 SecretsService
- OpenCode 全局文件允许用户手填，但系统不自动反写密钥进去

### R5. 必须先定“旧 codex target 的迁移语义”

若不提前明确，旧 anthropic preset 会被错误迁到 Codex 页面。

冻结结论：

- 旧非 `opencode` preset 按 Provider `api_format` 分流

### R6. 必须先定“OpenCode Phase 2+3 仍使用哪条启动链”

冻结结论：

- Phase 2+3 继续使用现有 `OPENCODE_CONFIG_CONTENT` 叠加链即可
- 仅在 Phase 4/5 再评估是否升级为会话隔离 `OPENCODE_CONFIG` 临时文件

原因：当前全局配置已经在真实文件中，preset 只剩会话级 overlay，env 方案足够支撑最小闭环。

## 九、最终冻结结论

1. Provider 从“带 preset 的树节点”升级为“跨终端通用配置中心”。
2. Claude Code / OpenCode / Codex 必须拆成三套独立 preset 体系。
3. OpenCode 全局配置的唯一权威源是 `~/.config/opencode/opencode.json`。
4. `ProviderDetail.vue` 不再承载 preset 与 OpenCode GUI。
5. OpenCode 同时支持 `anthropic` 与 `openai` 两种 provider 格式。
6. Phase 2+3 先做“结构拆分 + 页面拆分 + 读取链切换 + OpenCode 全局归位”的最小闭环；更重的运行时隔离能力放到 Phase 4/5。

## 十、建议下一步

按冻结结论直接进入实现拆分：先改数据模型与迁移，再改页面入口与 Dashboard 读取链，最后补 OpenCode provider builder 的双格式支持。
