# 插件系统

面向 Amagi CodeBox 的终端用户。本篇说明应用内插件管理的三类引擎（Claude Code、OpenCode 与 Codex）、各自的安装与管理路径，以及跨引擎子项启停的统一入口。Amagi CodeBox 不自行实现插件运行时；安装和更新优先调用各 CLI 的正式命令，并在其上叠加配置读取、资源展示和子项级启停等能力。

相关参考：

- 界面入口与导航：[./usage.md](./usage.md)
- 提供商与预设配置：[./providers.md](./providers.md)
- 后端 API 与 Wails 绑定方法签名：[../api.md](../api.md)
- 配置目录总览：[./installation.md](./installation.md#配置目录)

---

## 总览

Amagi CodeBox 同时管理三套独立的插件生态：

| 引擎 | 后端包 | CLI | 数据真相源 | 配置目录 |
|------|--------|-----|------------|----------|
| Claude Code | `internal/plugin` | `claude plugin ...` | `~/.claude/plugins/installed_plugins.json`（CLI 写入） | `~/.claude/` |
| OpenCode | `internal/opencodeplugin` | `opencode plugin <module> --global` | 全局 `opencode.json` / `opencode.jsonc` 的 `plugin` 数组 | `~/.config/opencode/` |
| Codex | `internal/codexplugin` | `codex plugin ...` | `~/.codex/` 下的状态与缓存 | `~/.codex/` |

三套引擎在应用内统一通过 `/extensions`（扩展管理）页暴露。前端 API 层分别落在 `frontend/src/api/plugin.ts`（Claude）、`frontend/src/api/opencodePlugin.ts`（OpenCode）与 `frontend/src/api/codexPlugin.ts`（Codex），各自包裹对应 Wails 绑定。

Amagi CodeBox 在 CLI 之上添加的能力：

- **市场聚合视图**：把多个市场的可安装插件聚合到统一列表。
- **子项级启停**（仅 Claude 真正落盘）：在插件内部按 skill / agent / command / hook / mcp / claude 子项粒度启用或禁用。

> 不在范围内：插件本身的开发、签名、运行时沙箱。这些由各 CLI 自身负责。

---

## Claude Code 插件

### 工作原理

`internal/plugin.Service` 把所有插件操作翻译成对 `claude` CLI 的调用，并以 `--json` 输出解析。CLI 解析由 `internal/platform.CLIResolver` 完成（先按 PATH 查找 `claude`，再按平台能力解析包装命令）。命令超时统一为 60 秒。

主要命令映射：

| 服务方法 | 实际命令 | 备注 |
|----------|----------|------|
| `GetMarketplaces()` | `claude plugin marketplace list --json` | CLI 失败时回退到本地 marketplaces 文件 |
| `GetInstalledPlugins()` | `claude plugin list --json` | CLI 失败时回退到本地 installed_plugins 文件 |
| `GetAvailablePlugins()` | `claude plugin list --json --available` | 兼容裸数组与 `{installed, available}` 信封两种返回格式 |
| `GetPluginDetail(id)` | 读 installed_plugins + 解析插件目录下的 `.claude-plugin/plugin.json` 与各子项文件 | 不调 CLI，纯本地 |
| `InstallPlugin(name)` | `claude plugin install <name> --scope user` | 强制 user scope |
| `UninstallPlugin(id)` | `claude plugin uninstall <id> --scope user` | 强制 user scope |
| `EnablePlugin(id)` | `claude plugin enable <id>` | |
| `DisablePlugin(id)` | `claude plugin disable <id>` | |
| `UpdatePlugin(id)` | 先 `claude plugin marketplace update <mp>` 刷新市场索引，再 `claude plugin update <id>` | |
| `AddMarketplace(source)` | `claude plugin marketplace add <source>` | |
| `RemoveMarketplace(name)` | `claude plugin marketplace remove <name>` | |
| `UpdateMarketplace(name)` | `claude plugin marketplace update <name>` | |
| `RefreshPlugins()` | 依次调用 GetMarketplaces / GetInstalledPlugins / GetAvailablePlugins | 聚合错误 |

### 插件 ID 格式

插件 ID 采用 `name@marketplace`，由 `splitPluginID`（`internal/plugin/reader.go`）按最后一个 `@` 切分。例如 `reviewer@anthropos-marketplace` 中 `reviewer` 是插件名，`anthropos-marketplace` 是市场名。

> 重要：Claude 与 Codex 两套引擎都使用 `name@marketplace` 格式，因此**不能**用 `strings.Contains("@")` 判断一个 pluginId 属于哪个引擎。统一入口见下文"子项启停"。

### 插件类型

`internal/plugin/types.go` 定义自动分析得到的 `PluginType`：

- `integration`、`hybrid`、`skill`、`hook`、`agent`、`command`、`mcp`、`unknown`

类型由 `analyzePluginType(detail)` 根据插件包含的子项组合推断，UI 展示用。`AnalyzePluginType(pluginID)` 是对外入口。

### 子项（SubItem）

一个插件内部可独立启停的子项类型（`SubItemType`）：

| 类型 | 含义 |
|------|------|
| `skill` | 技能 |
| `hook` | Hook 事件 |
| `command` | 命令 |
| `agent` | Agent |
| `mcp` | MCP server 配置 |
| `claude` | Claude 基线项（保留标识 `__claude__`） |

另有保留前缀 `__assets__:` 用于 hook 资产子项。

子项状态以"禁用列表"形式存储在 `~/.amagi-codebox/plugin-subitems.json`：

```json
{
  "plugins": [
    {
      "pluginId": "reviewer@anthropos-marketplace",
      "disabledSubItems": [
        { "type": "skill", "name": "review" }
      ]
    }
  ]
}
```

文件由 `pluginSubItemStateFile` 序列化，写入采用 `tmp + rename` 原子替换；空 `disabledSubItems` 的条目会自动剔除。

---

## OpenCode 插件

OpenCode 当前提供安装命令，但没有 marketplace、list 或 uninstall 子命令。CodeBox 因此按 OpenCode 的实际能力实现：

| 服务方法 | 行为 |
|----------|------|
| `RefreshPlugins()` / `ListInstalledPlugins()` | 读取全局配置的 `plugin` 数组，并结合 `~/.cache/opencode/packages/` 中的包元数据 |
| `GetPluginDetails(spec)` | 读取 `package.json`，展示版本、作者、仓库、server / tui target 与可发现资源 |
| `InstallPlugin(spec)` | `opencode plugin <spec> --global` |
| `UpdatePlugin(spec)` | 发现最新稳定 GitHub tag 或 npm latest，切换为不可变 spec 后执行官方 `--force` 安装并校验 |
| `UninstallPlugin(spec)` | 仅从全局 `plugin` 数组移除目标项；缓存包保留 |

模块地址支持 npm 包、GitHub spec 和 `file://` 本地插件。配置目录优先遵循 `OPENCODE_CONFIG_DIR`、`XDG_CONFIG_HOME`；缓存目录遵循 `XDG_CACHE_HOME`。列表可读取 JSON 与 JSONC。为避免破坏注释和排版，JSONC 配置暂不自动卸载，界面会提示用户从对应配置文件手工移除该项。

更新不会对相同的 `#main` 或 npm `latest` 缓存重复执行 `--force`。GitHub 插件会从远端稳定 SemVer tag 中选择最高版本，例如把 `github:owner/repo#main` 替换为 `github:owner/repo#v1.2.3`；npm 插件会固定为 `package-name@1.2.3`。新 spec 具有独立缓存键，OpenCode 官方 CLI 会安装它并替换配置中的旧引用。更新完成后 CodeBox 必须确认新引用已写入配置、缓存包存在且 `package.json.version` 与目标版本一致，任一步不一致都会报告失败。`file://` 插件直接读取源路径，不参与远端版本更新。

OpenCode 没有 marketplace 概念，也没有子项启停 API，因此 OpenCode 页不显示“添加市场”或子项开关。

---

## Codex 插件

### 工作原理

`internal/codexplugin.Service` 把操作翻译成对 `codex` CLI 的调用。与 Claude 不同的是，Codex 服务对市场信息采用三级推断（`inferMarketplacesFromConfigPlugins` / `inferMarketplacesFromInstalledPlugins` / `inferMarketplacesFromCache`），用于在 CLI 输出不完整时补齐市场元数据。

主要方法：

| 方法 | 用途 |
|------|------|
| `ListMarketplaces()` | 列出已注册市场 |
| `AddMarketplace(req)` | 添加市场，`req.Source` 必须通过 `validateSource` |
| `UpgradeMarketplace(name)` | 升级指定市场快照 |
| `RemoveMarketplace(name)` | 移除市场 |
| `ListPlugins(marketplace)` | 列出指定市场下的插件 |
| `InstallPlugin(selector)` | 按选择器安装 |
| `UninstallPlugin(selector)` | 按选择器卸载 |
| `SetPluginEnabled(selector, enabled)` | 整体启用 / 禁用 |
| `GetPluginDetails(selector)` | 详情，含本地 skills / agents / commands / hooks / mcpServers 与 manifest |
| `ListAvailablePlugins()` | 已注册市场中的可安装插件 |
| `RefreshPlugins()` | 返回聚合结构 `CodexPluginsData`：marketplaces + installed + available + warnings |

### 插件选择器

`PluginSelector` 标识一个 Codex 插件，`PluginID` 优先；未传时由 `Name` 与 `Marketplace` 组合：

```go
type PluginSelector struct {
    PluginID    string `json:"pluginId"`
    ID          string `json:"id,omitempty"`
    Name        string `json:"name,omitempty"`
    Marketplace string `json:"marketplace,omitempty"`
}
```

### Codex 子项禁用：当前限制

`CodexPlugins.SetPluginSubItemEnabled(pluginId, subItemType, subItemId, enabled)` 当前是 **no-op**：只记录日志，不报错、也不落盘。也就是说，Codex 插件目前不支持子项级禁用，只能整体启用 / 禁用（`SetPluginEnabled`）。

调用方（特别是 `App.SetPluginSubItemEnabled` 的分派逻辑）必须能容忍这种静默行为；日志记录是为了在误派到 Codex 路径时保留可观测性。

---

## 子项启停统一入口

由于两个引擎的 pluginId 都形如 `name@marketplace`，不能通过字符特征区分。`App.SetPluginSubItemEnabled` 采用"查 Claude 注册表"的方式分派：

```go
func (a *App) SetPluginSubItemEnabled(pluginId, subItemType, subItemId string, enabled bool) error {
    if a.isClaudePlugin(pluginId) {
        return a.Plugins.SetPluginSubItemEnabled(...)   // Claude：落盘到 plugin-subitems.json
    }
    return a.CodexPlugins.SetPluginSubItemEnabled(...)  // Codex：当前 no-op
}
```

`isClaudePlugin(pluginId)`：

1. `a.Plugins.GetInstalledPlugins()` 读 Claude 注册表（`~/.claude/plugins/installed_plugins.json`）。
2. 命中 → 走 Claude 路径。
3. 未命中 → 走 Codex 路径。
4. 注册表读取失败 → 保守按 Codex 分派，并 `Warn` 日志告警。

> 风险：若实际是 Claude 插件而注册表读取失败，开关会静默不生效。这种情况下查看应用日志的 `plugin` 来源记录可以定位。

前端推荐使用 `frontend/src/api/plugin.ts` 中的 `setPluginSubItemEnabled`（注意是带 `Plugin` 中缀的统一入口，而不是 `setSubItemEnabled`，后者直接调 `plugin.Service` 仅适用于 Claude）。

---

## 配置文件汇总

| 文件 | 写入方 | 用途 |
|------|--------|------|
| `~/.claude/plugins/installed_plugins.json` | `claude` CLI | Claude 已安装插件注册表（真相源） |
| `~/.claude/plugins/marketplaces.json`（待核实：确切文件名） | `claude` CLI | Claude 市场注册 |
| `~/.amagi-codebox/plugin-subitems.json` | Amagi CodeBox（`plugin.Service`） | Claude 插件子项禁用列表 |
| `~/.config/opencode/opencode.json` / `opencode.jsonc` | `opencode` CLI；卸载时可能由 CodeBox 更新 | OpenCode 全局插件列表 |
| `~/.cache/opencode/packages/` | `opencode` CLI | OpenCode 插件包缓存 |
| `~/.codex/` 下的状态与缓存（待核实：具体文件名） | `codex` CLI | Codex 插件状态 |

> 这些文件建议通过应用 UI 维护，不要手工编辑。手工编辑可能破坏 `tmp + rename` 原子写入约定或不同插件引擎的 ID 对齐。

---

## 已知限制与注意事项

- **Codex 子项禁用不可用**：当前是 no-op，UI 上展示的子项开关对 Codex 引擎实际不生效。
- **OpenCode JSONC 卸载需手工处理**：列表读取支持 JSONC，但自动卸载只修改严格 JSON，防止注释和原有格式丢失。
- **子项启停分派依赖 Claude 注册表**：注册表读取失败时按 Codex 分派，Claude 插件开关会静默不生效；需要看日志定位。
- **`--scope user` 固定**：Claude 插件安装/卸载强制使用 user scope，不支持 project / local scope 的管理 UI。
- **依赖外部 CLI**：`claude`、`opencode` 或 `codex` 未安装、PATH 未配置时，对应引擎的安装与更新功能不可用。环境检测（`/envcheck`）会标记这些问题并提供一键修复入口，详见 [./faq.md](./faq.md)。
- **`RefreshPlugins` 聚合错误**：Claude 的 `RefreshPlugins` 把 GetMarketplaces / GetInstalledPlugins / GetAvailablePlugins 的错误用 `errors.Join` 聚合返回；调用方应同时处理"部分成功"。

> 待核实：Codex `~/.codex/` 下市场与状态文件的确切命名；Claude `~/.claude/plugins/marketplaces.json` 的实际文件名；`marketplaceSource` 在 Codex 路径下的 `Source` / `Repo` / `URL` 三元组优先级。
