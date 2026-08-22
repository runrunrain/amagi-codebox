# 插件系统

面向 Amagi CodeBox 的终端用户。本篇说明应用内插件管理的五套引擎（Claude Code、OpenCode、Codex、Pi、OMP）、各自的安装与管理路径，以及跨引擎子项启停的统一入口。Amagi CodeBox 不自行实现插件运行时；安装和更新优先调用各 CLI 的正式命令，并在其上叠加配置读取、资源展示和子项级启停等能力。

相关参考：

- 界面入口与导航：[./usage.md](./usage.md)
- 提供商与预设配置：[./providers.md](./providers.md)
- 后端 API 与 Wails 绑定方法签名：[../api.md](../api.md)
- 配置目录总览：[./installation.md](./installation.md#配置目录)

---

## 总览

Amagi CodeBox 同时管理五套独立的插件/包生态：

| 引擎 | 后端包 | CLI / 数据真相源 | 配置目录 |
|------|--------|------------------|----------|
| Claude Code | `internal/plugin` | `claude plugin ...`；真相源 `~/.claude/plugins/installed_plugins.json` | `~/.claude/` |
| OpenCode | `internal/opencodeplugin` | `opencode plugin <module> --global`；真相源全局 `opencode.json` / `opencode.jsonc` 的 `plugin` 数组 | `~/.config/opencode/` |
| Codex | `internal/codexplugin` | `codex plugin ...`；真相源 `~/.codex/` 下的状态与缓存 | `~/.codex/`（或 `CODEX_HOME`） |
| Pi | `internal/piplugin` | `pi install/remove/update <source>`；真相源 `~/.pi/agent/settings.json` 的 `packages[]` 数组 | `~/.pi/agent/`（或 `PI_CODING_AGENT_DIR`） |
| OMP | `internal/ompplugin` | `omp plugin list/install/uninstall/enable/disable/upgrade`（CLI `--json` 输出） | `~/.omp/agent/` |

五套引擎在应用内统一通过 `/extensions`（扩展管理）页暴露：Plugins Tab 下再按 ClaudeCode / OpenCode / Codex / Pi / OMP 五个二级 Tab 切换。前端 API 层分别落在 `frontend/src/api/` 的 `plugin.ts`、`opencodePlugin.ts`、`codexPlugin.ts`、`piPlugin.ts` 与 `ompPlugin.ts`。

Amagi CodeBox 在 CLI 之上添加的能力：

- **市场聚合视图**（Claude / Codex）：把多个市场的可安装插件聚合到统一列表。
- **子项级启停**（仅 Claude 真正落盘）：在插件内部按 skill / agent / command / hook / mcp / claude 子项粒度启用或禁用。

> 不在范围内：插件本身的开发、签名、运行时沙箱。这些由各 CLI 自身负责。

---

## Claude Code 插件

### 工作原理

`internal/plugin.Service` 把所有插件操作翻译成对 `claude` CLI 的调用，并以 `--json` 输出解析。CLI 解析由 `internal/platform.CLIResolver` 完成。命令超时统一为 60 秒。

主要命令映射：

| 服务方法 | 实际命令 | 备注 |
|----------|----------|------|
| `GetMarketplaces()` | `claude plugin marketplace list --json` | CLI 失败时回退到本地 marketplaces 文件 |
| `GetInstalledPlugins()` | `claude plugin list --json` | CLI 失败时回退到本地 installed_plugins 文件 |
| `GetAvailablePlugins()` | `claude plugin list --json --available` | 兼容裸数组与 `{installed, available}` 信封两种返回格式 |
| `GetPluginDetail(id)` | 读 installed_plugins + 解析插件目录下的 `.claude-plugin/plugin.json` 与各子项文件 | 不调 CLI，纯本地 |
| `InstallPlugin(name)` | `claude plugin install <name> --scope user` | 强制 user scope |
| `UninstallPlugin(id)` | `claude plugin uninstall <id> --scope user` | 强制 user scope |
| `EnablePlugin(id)` / `DisablePlugin(id)` | `claude plugin enable/disable <id>` | |
| `UpdatePlugin(id)` | 先 `claude plugin marketplace update <mp>` 刷新市场索引，再 `claude plugin update <id>` | |
| `AddMarketplace(source)` / `RemoveMarketplace(name)` / `UpdateMarketplace(name)` | `claude plugin marketplace add/remove/update` | |
| `RefreshPlugins()` | 依次调用 GetMarketplaces / GetInstalledPlugins / GetAvailablePlugins | 聚合错误 |

### 插件 ID 格式

插件 ID 采用 `name@marketplace`，由 `splitPluginID`（`internal/plugin/reader.go`）按最后一个 `@` 切分。

> 重要：Claude 与 Codex 两套引擎都使用 `name@marketplace` 格式，因此**不能**用 `strings.Contains("@")` 判断一个 pluginId 属于哪个引擎。统一入口见下文"子项启停"。

### 插件类型与子项

`internal/plugin/types.go` 定义自动分析得到的 `PluginType`：`integration`、`hybrid`、`skill`、`hook`、`agent`、`command`、`mcp`、`unknown`，由 `analyzePluginType(detail)` 根据插件包含的子项组合推断。

可独立启停的子项类型（`SubItemType`）：`skill`、`hook`、`command`、`agent`、`mcp`、`claude`（Claude 基线项，保留标识 `__claude__`）。另有保留前缀 `__assets__:` 用于 hook 资产子项。

子项状态以"禁用列表"形式存储在 `~/.amagi-codebox/plugin-subitems.json`，写入采用 `tmp + rename` 原子替换；空 `disabledSubItems` 的条目自动剔除。

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

更新不会对相同的 `#main` 或 npm `latest` 缓存重复执行 `--force`。GitHub 插件会从远端稳定 SemVer tag 中选择最高版本；npm 插件会固定为 `package-name@1.2.3` 形态。更新完成后 CodeBox 必须确认新引用已写入配置、缓存包存在且 `package.json.version` 与目标版本一致，任一步不一致都报告失败。`file://` 插件不参与远端版本更新。

OpenCode 没有 marketplace 概念，也没有子项启停 API，因此 OpenCode 页不显示"添加市场"或子项开关。

---

## Codex 插件

`internal/codexplugin.Service` 把操作翻译成对 `codex` CLI 的调用。Codex 服务对市场信息采用三级推断（`inferMarketplacesFromConfigPlugins` / `inferMarketplacesFromInstalledPlugins` / `inferMarketplacesFromCache`），用于在 CLI 输出不完整时补齐市场元数据。

主要方法：`ListMarketplaces()`、`AddMarketplace(req)`（`req.Source` 必须通过 `validateSource`）、`UpgradeMarketplace(name)`、`RemoveMarketplace(name)`、`ListPlugins(marketplace)`、`InstallPlugin(selector)`、`UninstallPlugin(selector)`、`SetPluginEnabled(selector, enabled)`、`GetPluginDetails(selector)`、`ListAvailablePlugins()`、`RefreshPlugins()`（聚合返回 `CodexPluginsData`：marketplaces + installed + available + warnings）。

插件选择器 `PluginSelector` 以 `PluginID` 优先，未传时由 `Name` 与 `Marketplace` 组合。

### Codex 子项禁用：当前限制

`CodexPlugins.SetPluginSubItemEnabled(pluginId, subItemType, subItemId, enabled)` 当前是 **no-op**：只记录日志，不报错、也不落盘。Codex 插件目前不支持子项级禁用，只能整体启用 / 禁用（`SetPluginEnabled`）。

---

## Pi 包管理

Pi 的扩展单位是 **package**（包），真相源为 `~/.pi/agent/settings.json` 的 `packages[]` 数组（元素可为字符串源或 `{source, extensions[], skills[], prompts[], themes[]}` 过滤对象）。`internal/piplugin.Service` 的行为：

| 服务方法 | 行为 |
|----------|------|
| `ListInstalledPackages()` / `RefreshPackages()` | 读取 `settings.json` 的 `packages[]`，去重后结合实体目录扫描补全元数据 |
| `GetPackageDetails(source)` | 展示包内可发现的 extensions / skills / prompts / themes 等子资源 |
| `InstallPackage(source)` | `pi install <source>`（写 settings.json + 拉取实体）；source 支持 npm / git / 本地路径 |
| `RemovePackage(source)` | `pi remove <source>`：从 `settings.json` 的 `packages[]` 删除登记，实体目录保留 |
| `UpdatePackage(source)` | `pi update <source>`；仅允许更新已登记的包，未登记返回明确错误 |
| `SwitchPackageSource(oldSource, newSource)` | 切换包来源，失败时回滚重装旧源 |

安全闸门：所有包源字符串经 `validateSource` 校验，拒绝 cmd.exe 元字符（`&|<>^%()` 等），阻断 Windows `.cmd` wrapper 的命令注入路径。

agent 目录解析复刻 pi CLI：优先 `$PI_CODING_AGENT_DIR`，否则 `~/.pi/agent`。

## OMP 插件

OMP 的插件管理完全委托给 `omp` CLI（`internal/ompplugin.Service`）：

| 服务方法 | 实际命令 |
|----------|----------|
| `ListPlugins()` / `RefreshPlugins()` | `omp plugin list --json`（解析失败的条目降级为 Warnings 信封） |
| `InstallPlugin(spec)` | `omp install <spec>`（npm / git / local / marketplace ref 均支持） |
| `UninstallPlugin(name)` | `omp plugin uninstall <name>`（npm 与 marketplace 由 CLI 自动路由） |
| `SetPluginEnabled(name, enabled)` | `omp plugin enable/disable <name>` |
| `UpgradePlugin(name)` | marketplace 插件 → `omp plugin upgrade <id>`；npm 或未识别 → `omp install <name> --force`（重装即升级） |

与 Pi 一样，所有 spec 经 `validatePluginSpec` 校验，拒绝命令行元字符。OMP 配置目录优先 `$PI_CODING_AGENT_DIR`，否则 `~/.omp/agent`。

---

## 子项启停统一入口

由于 Claude 与 Codex 两个引擎的 pluginId 都形如 `name@marketplace`，不能通过字符特征区分。`App.SetPluginSubItemEnabled` 采用"查 Claude 注册表"的方式分派：

```go
func (a *App) SetPluginSubItemEnabled(pluginId, subItemType, subItemId string, enabled bool) error {
    if a.isClaudePlugin(pluginId) {
        return a.Plugins.SetPluginSubItemEnabled(...)   // Claude：落盘到 plugin-subitems.json
    }
    return a.CodexPlugins.SetPluginSubItemEnabled(...)  // Codex：当前 no-op
}
```

`isClaudePlugin(pluginId)`：读 Claude 注册表（`~/.claude/plugins/installed_plugins.json`），命中走 Claude 路径，未命中走 Codex 路径；注册表读取失败保守按 Codex 分派并 `Warn` 日志告警。

> 风险：若实际是 Claude 插件而注册表读取失败，开关会静默不生效。这种情况下查看应用日志的 `plugin` 来源记录可以定位。

前端推荐使用 `frontend/src/api/plugin.ts` 中的 `setPluginSubItemEnabled`（带 `Plugin` 中缀的统一入口），而不是 `setSubItemEnabled`（后者直接调 `plugin.Service`，仅适用于 Claude）。

---

## 配置文件汇总

| 文件 | 写入方 | 用途 |
|------|--------|------|
| `~/.claude/plugins/installed_plugins.json` | `claude` CLI | Claude 已安装插件注册表（真相源） |
| `~/.amagi-codebox/plugin-subitems.json` | Amagi CodeBox（`internal/plugin`） | Claude 插件子项禁用列表 |
| `~/.config/opencode/opencode.json` / `opencode.jsonc` | `opencode` CLI；卸载时可能由 CodeBox 更新 | OpenCode 全局插件列表 |
| `~/.cache/opencode/packages/` | `opencode` CLI | OpenCode 插件包缓存 |
| `~/.codex/` 下的状态与缓存（待核实：具体文件名） | `codex` CLI | Codex 插件状态 |
| `~/.pi/agent/settings.json` 的 `packages[]` | `pi` CLI | Pi 已登记包（真相源） |
| `~/.omp/agent/` 下的插件状态 | `omp` CLI | OMP 已安装插件 |

> 这些文件建议通过应用 UI 维护，不要手工编辑。手工编辑可能破坏 `tmp + rename` 原子写入约定或不同插件引擎的 ID 对齐。

---

## 已知限制与注意事项

- **Codex 子项禁用不可用**：当前是 no-op，UI 上展示的子项开关对 Codex 引擎实际不生效。
- **OpenCode JSONC 卸载需手工处理**：列表读取支持 JSONC，但自动卸载只修改严格 JSON，防止注释和原有格式丢失。
- **子项启停分派依赖 Claude 注册表**：注册表读取失败时按 Codex 分派，Claude 插件开关会静默不生效；需要看日志定位。
- **`--scope user` 固定**：Claude 插件安装/卸载强制使用 user scope，不支持 project / local scope 的管理 UI。
- **依赖外部 CLI**：`claude`、`opencode`、`codex`、`pi` 或 `omp` 未安装、PATH 未配置时，对应引擎的安装与更新功能不可用。环境检测（`/envcheck`）会标记这些问题并提供一键修复入口，详见 [./faq.md](./faq.md)。
- **`RefreshPlugins` 聚合错误**：Claude 的 `RefreshPlugins` 把各子调用错误用 `errors.Join` 聚合返回；调用方应同时处理"部分成功"。

> 待核实：Codex `~/.codex/` 下市场与状态文件的确切命名。
