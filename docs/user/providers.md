# 提供商与预设配置

面向 Amagi CodeBox 的终端用户与高级配置者。本篇解释 Provider、Preset、模型参数（含思考模式与上下文窗口）等核心概念，说明支持的提供商类型与三种应用引擎（AppType），并给出 `config.json` 的结构示例。

API 密钥的加密存储、传输安全等安全相关内容不在本篇范围，请参考 [../security.md](../security.md)。

相关参考：

- 安装与首次运行：[./installation.md](./installation.md)
- 界面功能总览：[./usage.md](./usage.md)
- 后端 API 详细签名：[../api.md](../api.md)

---

## 核心概念

| 概念 | 类型（Go 包路径） | 含义 |
|------|-------------------|------|
| Provider | `config.Provider` (`internal/config/types.go`) | 一个服务提供商，如 Anthropic、OpenAI、GLM。包含认证格式、BaseURL、默认模型与一组 Preset |
| Preset | `config.Preset` | 归属于某个 Provider 的预设：模型名 + 模型参数。一个 Provider 可有多个 Preset |
| Parameters | `config.Parameters` | 模型运行参数：温度、top_p、max_tokens、思考模式、流式、上下文窗口等 |
| ThinkingConfig | `config.ThinkingConfig` | 思考模式开关（`enabled` / `disabled`）与可选预算 token |
| ContextWindowConfig | `config.ContextWindowConfig` | Codex CLI 风格的上下文窗口配置：窗口大小 + 自动压缩阈值 |
| TerminalPreset | `config.TerminalPreset` | 终端维度的独立预设，按引擎分组（`claude_code` / `opencode` / `codex`） |
| OpenCodePreset | `config.OpenCodePreset` | OpenCode 专用预设：一份完整的 `opencode.json` 配置 + provider 绑定 |

Provider 与 Preset 是"底层资源 → 启动配置"的两层模型。Provider 描述"如何连接到服务商"，Preset 描述"用什么模型 + 什么参数启动"。

---

## AppType：三种应用引擎

`internal/session/types.go` 定义：

```go
type AppType string

const (
    AppTypeClaudeCode AppType = "claudecode" // Claude Code
    AppTypeOpenCode   AppType = "opencode"   // OpenCode
    AppTypeCodex      AppType = "codex"      // Codex CLI
    AppTypePi         AppType = "pi"         // Pi coding agent
    AppTypeOhMyPi     AppType = "omp"        // Oh My Pi (omp)
)
```

每种引擎对 Provider 的格式要求不同：

- **ClaudeCode (`claudecode`)**：要求 Provider 兼容 Anthropic 格式（`Provider.IsAnthropicCompatible()` 为 `true`）。启动入口 `App.LaunchSession`。
- **Codex (`codex`)**：启动入口 `App.LaunchCodexSession`，按"模型 + provider"组合启动。
- **OpenCode (`opencode`)**：启动入口 `App.LaunchOpenCode`，双轨兼容：优先查 `opencode_presets`（新模型），回退到 `terminal_presets.opencode`（旧模型）。
- **Pi (`pi`)**：启动入口 `App.LaunchPiSession`，按「模型 + provider」组合启动，由 `terminal_preset`（`type="pi"`）驱动（对标 Codex）。

---

## Provider 结构

```go
type Provider struct {
    // 双格式支持（新字段，推荐）
    Anthropic *AnthropicFormat `json:"anthropic,omitempty"`
    OpenAI    *OpenAIFormat    `json:"openai,omitempty"`

    // 通用信息
    DefaultModel string   `json:"default_model"`
    UrlHistory   []string `json:"url_history,omitempty"`

    // 废弃字段（保留兼容读取，新数据不再写入）
    Type    string            `json:"type,omitempty"`
    BaseURL string            `json:"base_url,omitempty"`
    AuthKey string            `json:"auth_key,omitempty"`
    Presets map[string]Preset `json:"presets,omitempty"`
}
```

要点：

- **双格式字段**：`Anthropic` 与 `OpenAI` 描述该 Provider 同时支持的连接格式，每种格式各自带 `enabled`、`base_url`、`auth_key` 等字段。运行时优先读取新字段，旧字段（`Type` / `BaseURL` / `AuthKey`）保留是为了向后兼容。
- **首选格式**：`Provider.PreferredFormat()` 返回 `"openai"` 或 `"anthropic"`。若两种格式都启用，默认 OpenAI 优先。
- **认证类型常量**（`auth_key`）：
    - `ANTHROPIC_API_KEY`
    - `ANTHROPIC_AUTH_TOKEN`
    - `OAUTH`（Anthropic 官方 OAuth）
    - `OPENAI_API_KEY`（OpenAI 格式标识）
- **API 密钥不存储在 Provider 里**。`AnthropicFormat.APIKey` 与 `OpenAIFormat.APIKey` 仅用于导入旧 JSON / 兼容历史导出结构；运行时正式密钥来源始终是 provider 级 secrets（key = providerName）。密钥加密细节见 [../security.md](../security.md)。

## 统一同步到 OpenCode、Pi 与 Oh My Pi

Provider Center 是以下三套 harness provider 配置的统一来源：

| Harness | 同步目标 | CodeBox 所有权范围 |
|---|---|---|
| OpenCode | `~/.config/opencode/opencode.json` 的 `provider` | `amagi-*` provider |
| Pi | `~/.pi/agent/models.json` 的 `providers` | `amagi-*` provider |
| Oh My Pi | `~/.omp/agent/models.yml` 的 `providers` | `amagi-*` provider |

新增、编辑、改名、删除或导入 Provider 后，CodeBox 会重新对账这三处的 `amagi-*` 条目。默认模型和关联的公共预设模型会一起登记；改名或删除后，失效的旧托管条目也会被清除。启动应用和执行“保存全部配置”时会再次对账，从而修复外部删除造成的不一致。

同步只接管 `amagi-` 保留命名空间：其他 provider 和其他顶层配置保持不变。OpenCode 的 `~/.local/share/opencode/auth.json`、Pi 的 `~/.pi/agent/auth.json`、Oh My Pi 的 `agent.db` 等 OAuth/登录认证数据不会被读取或修改。已有配置文件无法解析时，同步会报错并保留原文件，不会以空配置覆盖。

为了让三个 harness 脱离 CodeBox 也能直接使用自定义 provider，CodeBox secrets 中的 API Key 会写入各 harness 支持的 provider 字段；相关配置文件会收紧为 `0600` 权限。没有 Base URL 或没有模型的 Provider 仍保留在 CodeBox 中，但不会发布为无效的 harness 自定义 provider。

---

## Preset 与 Parameters

### Preset

```go
type Preset struct {
    Name           string           `json:"name"`
    Model          string           `json:"model"`
    ModelHaiku     string           `json:"model_haiku,omitempty"`     // Haiku 档位（Claude Code 专用）
    ModelSonnet    string           `json:"model_sonnet,omitempty"`    // Sonnet 档位（Claude Code 专用）
    ModelOpus      string           `json:"model_opus,omitempty"`      // Opus 档位（Claude Code 专用）
    Parameters     Parameters       `json:"parameters"`
    Target         PresetTargetType `json:"target,omitempty"`          // codex（默认）| opencode
    OpenCodeConfig json.RawMessage  `json:"opencode_config,omitempty"` // OpenCode 原始配置片段
}
```

`Target` 表示该 Preset 服务的 CLI 类型（`codex` 或 `opencode`），缺省按 `codex` 处理。

### Parameters

```go
type Parameters struct {
    Temperature      float64              `json:"temperature,omitempty"`
    TopP             float64              `json:"top_p,omitempty"`
    MaxTokens        int                  `json:"max_tokens,omitempty"`
    MaxContextLength int                  `json:"max_context_length,omitempty"`
    DoSample         *bool                `json:"do_sample,omitempty"`
    Thinking         *ThinkingConfig      `json:"thinking,omitempty"`
    Stream           *bool                `json:"stream,omitempty"`
    ContextWindow    *ContextWindowConfig `json:"context_window,omitempty"`
    ReasoningEffort  string               `json:"reasoning_effort,omitempty"` // Claude Code：low/medium/high/xhigh/max
}
```

`DoSample` / `Stream` / `Thinking` / `ContextWindow` 使用指针类型以区分"未设置"与"显式 false"。

### ThinkingConfig（思考模式）

```go
type ThinkingConfig struct {
    Type         string `json:"type"`                   // "enabled" | "disabled"
    BudgetTokens int    `json:"budgetTokens,omitempty"` // 可选预算
}
```

兼容 `models.json` 的 `thinking.type` / `thinking.budgetTokens` 字段。

### ContextWindowConfig（上下文窗口，Codex CLI 风格）

```go
type ContextWindowConfig struct {
    ModelContextWindow    int `json:"model_context_window,omitempty"`           // 窗口大小，如 1047576 表示 1M
    AutoCompactTokenLimit int `json:"model_auto_compact_token_limit,omitempty"` // 自动压缩触发阈值
}
```

### ReasoningEffort 取值

Claude Code 支持的 `reasoning_effort`（`config.IsValidClaudeReasoningEffort`）：

```text
""（未设置/默认）| low | medium | high | xhigh | max
```

注意此为 Claude 划分（含 `max`），与 `codexplugin` 的 OpenAI 划分（`none/low/medium/high/xhigh`，无 `max`）不同，两者不要混淆。

---

## 内置默认 Provider

`internal/config/defaults.go` 的 `DefaultConfig()` 在首次启动或配置缺失时提供以下内置 Provider（值摘录自源码）：

| Provider Key | BaseURL | DefaultModel | AuthKey | 默认 Preset |
|--------------|---------|--------------|---------|-------------|
| `anthropic` | `https://api.anthropic.com` | （空，使用 OAuth） | `OAUTH` | `Default` |
| `openai` | `https://api.openai.com/v1` | `codex-mini-latest` | `OPENAI_API_KEY` | `Codex Mini`（model `codex-mini-latest`） |
| `glm` | `https://open.bigmodel.cn/api/anthropic` | `glm-5` | `ANTHROPIC_API_KEY` | `GLM-5`（thinking=enabled, stream=true） |
| `minimax` | `https://api.minimaxi.com/anthropic` | `MiniMax-M2.5` | `ANTHROPIC_API_KEY` | `MiniMax-M2.5`（thinking=enabled, stream=true） |
| `kimi` | `https://api.moonshot.cn/anthropic` | `kimi-k2.5` | `ANTHROPIC_API_KEY` | `Kimi K2.5`（thinking=enabled, stream=true） |

默认 `AgentTeams`：`Enabled=true`、`TeammateMode="in-process"`。默认配置版本字段 `Version: "1.0.1"`。

除上述内置项外，用户可在 Provider Center 中自定义添加 Provider。

---

## `config.json` 结构示例

下面给出一个简化结构示例，字段名严格对应 `internal/config/types.go` 的 JSON tag。示例值并非真实密钥，仅作格式演示。

```json
{
  "models": {
    "anthropic": {
      "default_model": "",
      "auth_key": "OAUTH",
      "presets": {
        "default": { "name": "Default", "model": "" }
      }
    },
    "glm": {
      "anthropic": {
        "enabled": true,
        "base_url": "https://open.bigmodel.cn/api/anthropic",
        "auth_key": "ANTHROPIC_API_KEY"
      },
      "default_model": "glm-5",
      "presets": {
        "default": {
          "name": "GLM-5",
          "model": "glm-5",
          "parameters": {
            "thinking": { "type": "enabled" },
            "stream": true
          }
        }
      }
    },
    "openai": {
      "openai": {
        "enabled": true,
        "base_url": "https://api.openai.com/v1",
        "auth_key": "OPENAI_API_KEY"
      },
      "default_model": "codex-mini-latest",
      "presets": {
        "default": { "name": "Codex Mini", "model": "codex-mini-latest" }
      }
    }
  },
  "agent_teams": { "enabled": true, "teammate_mode": "in-process" },
  "version": "1.0.1"
}
```

> 示例中省略了 `terminal_presets`、`opencode_presets`、`url_history` 等可选字段。完整结构定义见 `internal/config/types.go` 的 `AppConfig`。

```go
type AppConfig struct {
    Models          map[string]Provider       `json:"models"`
    AgentTeams      AgentTeamsConfig          `json:"agent_teams"`
    TerminalPresets *TerminalPresetsConfig    `json:"terminal_presets,omitempty"`
    OpenCodePresets map[string]OpenCodePreset `json:"opencode_presets,omitempty"`
    Version         string                    `json:"version"`
}
```

---

## TerminalPreset：可复用的格式预设

公共预设按 API 协议格式分组，不再为每个 CLI 维护一份重复配置：

```go
type TerminalPresetsConfig struct {
    Anthropic map[string]TerminalPreset `json:"anthropic,omitempty"`
    OpenAI    map[string]TerminalPreset `json:"openai,omitempty"`
}
```

- Claude Code 读取 Anthropic 格式预设。
- Codex、Pi、OMP 以及后续 OpenAI-compatible CLI 读取同一份 OpenAI 格式预设。
- OpenCode 使用独立的 `opencode_presets`完整配置模型。

旧 `claude_code` / `codex` / `pi` / `omp` 分组会在加载时自动合并。同 key 且内容相同的条目去重；同 key 但内容不同的条目会保留为带来源后缀的预设，避免迁移时丢失配置。

每个 `TerminalPreset` 关联一个 provider 名称，可覆盖 provider 的默认模型：

```go
type TerminalPreset struct {
    Name        string          `json:"name"`
    Provider    string          `json:"provider"`                 // 关联的 provider 名称
    Model       string          `json:"model"`                    // 可覆盖 provider 默认值
    ModelHaiku  string          `json:"model_haiku,omitempty"`    // Claude Code 专用
    ModelSonnet string          `json:"model_sonnet,omitempty"`   // Claude Code 专用
    ModelOpus   string          `json:"model_opus,omitempty"`     // Claude Code 专用
    Parameters  Parameters      `json:"parameters"`
    OpenCodeCfg json.RawMessage `json:"opencode_cfg,omitempty"`   // OpenCode 运行时 overlay
}
```

启动时（`App.LaunchSession` 等）会按 CLI 所属格式查找 preset key。应用还会将旧的 `provider.presets` 幂等迁移到对应公共格式，OpenCode 目标预设则迁移到 `opencode_presets`。

---

## OpenCodePreset：完整的 opencode.json

OpenCode 引擎的预设是一份完整的 `opencode.json` 配置（不含 secrets），并附带 provider 绑定：

```go
type OpenCodePreset struct {
    ID          string                     `json:"id"`
    Name        string                     `json:"name"`
    Description string                     `json:"description,omitempty"`
    Config      json.RawMessage            `json:"config"`            // 完整 opencode.json
    Bindings    map[string]OpenCodeBinding `json:"bindings,omitempty"`
    Source      *OpenCodePresetSource      `json:"source,omitempty"`
}

type OpenCodeBinding struct {
    LocalProvider string   `json:"local_provider"`           // 本地 Provider 名
    Format        string   `json:"format,omitempty"`         // openai / anthropic / auto
    Inject        []string `json:"inject,omitempty"`         // apiKey / baseURL / organization
    EnvFallback   bool     `json:"env_fallback,omitempty"`
}
```

启动 OpenCode 会话时（`App.LaunchOpenCode`）优先匹配 `opencode_presets`。历史 `terminal_presets.opencode` 数据会自动转换并从新存储中移除。

---

## 导入 / 导出

Provider Center 顶部提供两个针对整个 `config.json` 的操作（详见 [./usage.md](./usage.md#provider-center-provider-providercenterview)）：

- **导出完整配置**：基于 v2 `ExportConfig` 生成可移植 JSON 快照，除 providers、agent_teams、terminal_presets、opencode_presets 外，还包含全部密钥、应用设置、路径、自定义环境变量、价格表、OpenCode 全局配置，以及 CLI 独立配置全文快照：pi 的 `~/.pi/agent/models.json`、`auth.json`、`amagi.json` 与 omp 的 `~/.omp/agent/config.yml`、`models.yml`。CLI 独立配置仅在源设备对应文件存在且内容合法时导出；缺失或损坏（非法 JSON/YAML、无法读取）的字段会记 Warn 并跳过，不会阻断导出。Anthropic / OpenAI 内嵌的 `api_key` 仍会清空，当前 provider 统一密钥写在顶层；`portable.secrets` 保存全部其余密钥。导出文件含明文凭据，包括 pi `auth.json` 的 auth token、pi/omp models 配置中的内联 `apiKey`，与 provider API key 明文导出语义一致，请妥善保管。
- **导入完整配置**：v2 快照按整体替换语义还原并在失败时尽力回滚，成功后需重启；v1 文件保持旧协议兼容。导入 provider 时通过 `ExportProvider.UnifiedAPIKey()` 解析统一密钥：优先顶层 `api_key`，否则按首选格式回退到 legacy `api_key`。CLI 独立配置按“完整快照替换”语义恢复：快照中存在的字段整体替换目标设备对应文件，缺失字段（含旧版 v2 导出文件）跳过不写入，导入行为与旧文件完全一致；内容非法会在写入前整体报错并触发回滚。注意与 codebox 管理配置的共存关系：pi/omp 会话启动时仍会以合并方式把 `amagi-<name>` 托管条目写入同一文件（不会覆盖导入的自定义 provider），导入的快照仅在导入时整体替换一次。

导入/导出涉及的密钥同步会经过 secrets 服务（加密存储），明文不会进入 `config.json`。安全机制详见 [../security.md](../security.md)。

---

## 常见操作

### 添加新的自定义 Provider

1. 进入 `/provider`，确保一级导航在"服务提供商"。
2. 通过网格视图新增 provider，在详情页填写 BaseURL、认证格式（Anthropic / OpenAI）、`auth_key`、默认模型等。
3. 保存后，在对应 provider 下添加 Preset（模型 + 参数）。
4. 在该 provider 中填入 API 密钥（密钥经 OS Keychain / DPAPI 加密后写入 `secrets.json`）。

### 为 Claude Code 启用思考模式

在 ClaudeCode 预设的 `parameters.thinking` 中设置：

```json
{
  "parameters": {
    "thinking": { "type": "enabled", "budgetTokens": 8000 }
  }
}
```

或在 `parameters.reasoning_effort` 中设置 `low` / `medium` / `high` / `xhigh` / `max`（仅 Claude Code；Codex 的同名参数取值集不同）。

### 为 Codex 配置大上下文窗口

```json
{
  "parameters": {
    "context_window": {
      "model_context_window": 1047576,
      "model_auto_compact_token_limit": 1000000
    }
  }
}
```

---

## 已知限制与注意事项

- 直接手工编辑 `~/.amagi-codebox/config.json` 不被禁止，但应用启动时若加载失败会回退到默认配置并记日志；推荐使用应用内的 Provider Center 进行编辑。
- `anthropic` / `openai` 的双格式 `api_key` 字段在导入旧 JSON 时会被读取并迁移到 provider 级 secrets，新写入不会再保留这些明文字段。
- 旧 `provider.presets` 会在启动时自动迁移到 `terminal_presets`，迁移是幂等的；若迁移失败，应用仍可启动并给出 warning。
