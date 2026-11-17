# 插件系统

面向 Amagi CodeBox 的终端用户。本篇说明应用内扩展中心的三类对象——五套引擎插件（Claude Code、OpenCode、Codex、Pi、OMP）、`~/.agents` 下的共用技能与共用 MCP（`~/.agents/.mcp.json` 登记 + 四引擎分发）——各自的安装与管理路径，以及跨引擎子项启停的统一入口。Amagi CodeBox 不自行实现插件运行时；安装和更新优先调用各 CLI 的正式命令，并在其上叠加配置读取、资源展示和子项级启停等能力。

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

五套引擎与公共对象在应用内统一通过 `/extensions`（扩展管理）页暴露。页面为 **ZCode 式两栏子页布局**：左侧是子页导航，按「公共 / Agent」两个分组组织——公共组为「共用技能」与「共用 MCP」，Agent 组为 Claude Code / OpenCode / Codex / Pi / OMP 五个引擎子页；右侧为当前子页的内容区。子页选中状态保存在 `frontend/src/stores/plugin.ts` 的 `extSubPage`（`setExtSubPage` 是唯一写入口；切到引擎子页会联动重置插件视图态，切到共用技能子页不影响引擎态）。

每个引擎子页面板自上而下为：诊断横幅（仅在加载出错或存在警告时出现）、资源计数标签行（Claude 另有「MCP」标签，Pi 另有技能 / 扩展 / 提示词 / 主题标签，行尾为当前标签的搜索框）、工具栏（刷新与安装 / 市场入口）和内容区。插件列表为按市场分组的行式条目（Claude / Codex）或单组平铺，Claude / Codex 点选插件后在右侧查看详情，其余引擎则切换到详情页。Claude / Codex 经「市场」标签或工具栏「浏览插件市场」进入市场视图，「添加市场」统一由扩展管理页弹出的对话框处理。前端 API 层分别落在 `frontend/src/api/` 的 `plugin.ts`、`opencodePlugin.ts`、`codexPlugin.ts`、`piPlugin.ts`、`ompPlugin.ts` 与 `sharedSkills.ts`；公共子页另有 `sharedMcp.ts`。

Amagi CodeBox 在 CLI 之上添加的能力：

- **市场聚合视图**（Claude / Codex）：把多个市场的可安装插件聚合到统一列表。
- **子项级启停**（仅 Claude 真正落盘）：在插件内部按 skill / agent / command / hook / mcp / claude 子项粒度启用或禁用。

> 不在范围内：插件本身的开发、签名、运行时沙箱。这些由各 CLI 自身负责。

---

## 共用技能子页

「共用技能」是扩展中心「公共」分组下的子页，管理 `~/.agents/skills`（启用态）与 `~/.agents/skills-disabled`（禁用态）两个目录里的技能目录。这些技能对所有真实读取 `~/.agents/skills` 的 CLI 生效；启停通过**目录移动**实现，立即生效，无需重启。

- **列表与详情**：合并两个根目录并按名称排序；元数据（displayName / description / version / author）取自技能目录内 `SKILL.md` 的 frontmatter，缺失时回退目录名并如实标记（不伪造版本号）。计数标签为「全部 / 已启用 / 已禁用」，行尾带搜索框；工具栏提供「打开技能根目录」与「刷新」。
- **启停与删除**：均先弹确认弹窗（删除为 danger 级确认），确认后执行目录移动或永久删除（`os.RemoveAll`，含禁用态目录），并返回最新列表。删除不可恢复。
- **应用托管技能**：名为 `amagi-codebox` 的技能是应用自带技能（徽章「应用托管」），其启停 / 删除会被后端直接拒绝，并提示前往「设置 → 命令行访问」管理——它由 agent CLI 安装器维护，手动移动 / 删除会在下次启动被自动装回。详情页会展示该技能的安装状态（未安装 / 已是最新 / 有更新 / 用户已修改 / 无效）。
- **打开目录**：可打开单个技能的当前目录或 `~/.agents/skills` 根目录；根目录不存在时如实报错并提示先安装技能（应用不代为创建目录）。

后端机制在 `internal/sharedskills`（枚举 / frontmatter 解析 / 启停 / 删除），前端经 `App` 上的 6 个门面方法访问（`ListSharedSkills` / `RefreshSharedSkills` / `SetSharedSkillEnabled` / `DeleteSharedSkill` / `OpenSharedSkillFolder` / `OpenSharedSkillsRoot`，见 [../api.md](../api.md)）；`sharedskills.Service` 不进 Wails Bind 列表。

---

## 共用 MCP 子页

「共用 MCP」是扩展中心「公共」分组下的另一个子页，管理登记文件 `~/.agents/.mcp.json`（顶层 `mcpServers.<name>`）里的 MCP 服务器条目，并可把条目**分发**到四个 Agent 引擎的真实 MCP 配置。分发只写配置文件，不代表服务器已连通——界面状态只用「已分发 / 未分发 / 有更新」，不做连接探测。

### 登记文件（数据源）

- 数据真相源是 `~/.agents/.mcp.json` 的 `mcpServers.<name>` 对象；文件不存在时，首次「新增服务器」自动创建（`~/.agents` 目录 0700、文件 0600，临时文件 + rename 原子写入）。文件损坏（非法 JSON）时应用拒绝读写并提示先手动修复，绝不覆写。
- 条目形态：`stdio`（本地命令，必填 `command`，可带 `args` / `env`）与 `http`（远程，必填 `url`，可带 `headers`）；`env` / `headers` 的值明文存于登记文件，界面一律脱敏展示。条目中应用未识别的键与值原样保留（详情页显示「未知键 N 个（应用未识别，写入时原样保留）」），改写只重构自己认识的字段。
- 条目名即标识，新建时仅允许字母、数字、`-`、`_`（`.` 与路径分隔符会破坏 Codex 段键，被拒绝）；既有的不合规名字只读展示并标记「名字不兼容分发」，仍可删除。
- 应用自身的勾选与写入跟踪（哪个条目分发到了哪个引擎、上次写入的内容哈希）存在应用配置目录的 `shared-mcp-state.json`，**绝不写入 `~/.agents` 共享文件**。

### 四引擎分发与还原

| 引擎 | 落点 | 条目映射 |
|------|------|----------|
| Claude Code | `~/.claude.json` 顶层 `mcpServers.<name>` | 原样写入（stdio / http 均可，含 description） |
| Codex | `~/.codex/config.toml`（`CODEX_HOME` 优先）的 `[mcp_servers.<name>]` 段 | stdio → `command` + `args` + `env` 子表；http 仅当不带自定义 `headers`（Codex 远程鉴权只支持 OAuth / bearer_token_env_var）；description 丢弃 |
| OpenCode | `~/.config/opencode/opencode.json` 顶层 `mcp`（检测到 V2 `mcp.servers` 对象时写 V2） | stdio → `{type:"local", command:[…]}` + `environment`；http → `{type:"remote", url, headers}`；description 丢弃 |
| Pi | `$PI_CODING_AGENT_DIR`（否则 `~/.pi/agent`）下 `mcp.json` 顶层 `mcpServers.<name>` | 原样写入（同 Claude） |

- **勾选分发**：新增条目默认所有引擎未勾选；在「条目 × 引擎」矩阵逐格开启才写入对应引擎配置，逐引擎独立、互不牵连。写入前做所有权检查：目标已存在同名条目且非本应用写入 → 冲突（conflict），拒绝覆盖并提示先手动处理目标文件。
- **状态矩阵**：每格实测目标文件，取值：已分发（distributed，目标内容与登记一致）/ 有更新（drift，登记改过、目标仍是旧内容，可「重新分发」）/ 未分发（not-distributed）/ 冲突（conflict）/ 不支持（unsupported，该引擎无法表达此形态）。
- **还原语义**：关闭某格开关 = 从该引擎配置移除该条目（带确认弹窗，警示「将移除已写入该引擎配置的托管条目」）；删除登记条目时先逐引擎还原已分发条目，任一引擎失败则整体中止（登记保留、已成功还原的引擎保持已还原、未处理的引擎不再改动）。`mcpServers` / `mcp` 清空后保留空对象，不删键。
- **外部修改保护**：目标引擎中的条目被外部改过（与上次分发的哈希不一致）时，覆盖或移除都会被拒绝，需在 danger 确认弹窗中强制执行（force）。登记条目内容更新后**不自动重分发**——已分发引擎如实转「有更新」，由用户逐引擎「重新分发」（幂等，等于用当前登记内容重新写入）。
- **OMP 排除**：OMP（`config.yml`）没有 MCP 配置面，矩阵中恒为只读的 excluded 行——这是当前事实裁定，不是待办；应用不会写任何 OMP 文件。

后端机制在 `internal/sharedmcp`（登记 CRUD / 引擎写入器 / 状态跟踪），前端经 `App` 上的 6 个门面方法访问（`ListSharedMcp` / `RefreshSharedMcp` / `CreateSharedMcpEntry` / `UpdateSharedMcpEntry` / `DeleteSharedMcpEntry` / `SetSharedMcpEngineSelection`，见 [../api.md](../api.md)）；`sharedmcp.Service` 不进 Wails Bind 列表。工具栏「打开登记目录」复用通用的 `OpenFileInEditor` 门面。

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

## 共用中心（公共 skill / 公共 MCP）

扩展管理页的「共用」tab 管理跨 CLI 共享的两类资源：

- **公共 skill**：从 zip 压缩包或本地文件夹导入（单一事实源 `~/.amagi-codebox/shared/skills/`）。每个 skill 可按需勾选分发目标 CLI（Claude/Codex/OpenCode/Pi/OMP），启用时复制分发到对应 CLI 的 skills 目录，禁用/删除时按部署清单回收。导入有安全防护（路径穿越/超大文件/符号链接拒绝）。
- **公共 MCP**：从 JSON 导入（支持粘贴或选文件，兼容 `{"mcpServers":{}}`/裸对象/单条三种格式，导入前有解析预览）。启用时写入各 CLI 原生 MCP 配置（Claude `~/.claude.json`、Codex `config.toml` 的 `[mcp_servers.*]` TOML 保真编辑、OpenCode `opencode.json` 的 `mcp` 键、Pi/OMP `<agentDir>/mcp.json`），禁用/删除时移除。写入前自动备份（保留最近 3 份）。

公共技能页同时聚合展示**插件来源的 skill**（标有「插件·名」徽章）：这类条目不可单独删除，卸载/更新统一在插件管理页的对应插件处操作；启停也走插件自身的子项开关。插件更新、卸载、启停成功后聚合视图自动失效刷新。

各引擎子项/skill 级启停能力（如实对齐 CLI 实际能力）：

| 引擎 | 插件级启停 | skill 级启停 |
|---|---|---|
| Claude | ✅ 原生 | ✅ 原生（plugin-subitems.json） |
| Codex | ✅ 原生 | ❌ CLI 无机制（不提供假开关） |
| OpenCode | ❌ 无原生 disable | ❌ 无子项概念（降级为只读展示） |
| Pi | 模拟（`!**` 四键全排除过滤标记，与 `pi config` TUI 互通可逆） | ✅ 原生（packages `skills[]` 过滤，与 TUI 算法对齐） |
| OMP | ✅ 原生 | ❌ 无子项机制（只读展示） |

单插件更新：Claude/OpenCode/Pi/OMP 用各自 CLI 原生命令；Codex CLI 无原生 update，采用「市场刷新 → 卸载重装 → 恢复启用状态」的模拟实现（重装失败会如实提示旧版已卸载需手动重装）。

---

## 配置文件汇总

| 文件 | 写入方 | 用途 |
|------|--------|------|
| `~/.claude/plugins/installed_plugins.json` | `claude` CLI | Claude 已安装插件注册表（真相源） |
| `~/.amagi-codebox/plugin-subitems.json` | Amagi CodeBox（`internal/plugin`） | Claude 插件子项禁用列表 |
| `~/.config/opencode/opencode.json` / `opencode.jsonc` | `opencode` CLI；卸载与共用 MCP 分发时可能由 CodeBox 更新 | OpenCode 全局插件列表（`plugin` 数组）与共用 MCP（顶层 `mcp`） |
| `~/.cache/opencode/packages/` | `opencode` CLI | OpenCode 插件包缓存 |
| `~/.codex/` 下的状态与缓存（待核实：具体文件名） | `codex` CLI | Codex 插件状态 |
| `~/.pi/agent/settings.json` 的 `packages[]` | `pi` CLI | Pi 已登记包（真相源） |
| `~/.omp/agent/` 下的插件状态 | `omp` CLI | OMP 已安装插件 |
| `~/.amagi-codebox/shared/skills/` | Amagi CodeBox（`internal/sharedext`） | 共用中心公共 skill 单一事实源 |
| `~/.amagi-codebox/shared/registry.json` | Amagi CodeBox（`internal/sharedext`） | 公共 skill/MCP 状态与部署清单（启停/目标/已部署引擎） |
| `~/.agents/skills/` 与 `~/.agents/skills-disabled/` | Amagi CodeBox（`internal/sharedskills`） | 共用技能目录（启用态 / 禁用态） |
| `~/.agents/.mcp.json` | Amagi CodeBox（`internal/sharedmcp`） | 共用 MCP 登记文件（真相源；未知键原样保留） |
| `~/.amagi-codebox/shared-mcp-state.json` | Amagi CodeBox（`internal/sharedmcp`） | 共用 MCP 勾选与写入跟踪（选择状态不写 `~/.agents`） |
| `~/.claude.json` | `claude` CLI；共用 MCP 分发时由 CodeBox 更新顶层 `mcpServers` | Claude Code 全局配置（含 MCP 服务器） |
| `~/.codex/config.toml` | `codex` CLI；共用 MCP 分发时由 CodeBox 更新 `[mcp_servers.*]` 段 | Codex 配置（`CODEX_HOME` 可重定向） |
| `~/.pi/agent/mcp.json` | Pi（agent 目录）；共用中心/共用 MCP 分发时由 CodeBox 更新顶层 `mcpServers` | Pi 的 MCP 服务器配置 |
| `~/.omp/agent/mcp.json` | OMP（agent 目录）；共用中心 MCP 分发时由 CodeBox 编辑 | OMP 的 MCP server 配置（mcpServers 对象） |

> 这些文件建议通过应用 UI 维护，不要手工编辑。手工编辑可能破坏 `tmp + rename` 原子写入约定或不同插件引擎的 ID 对齐。

---

## 已知限制与注意事项

- **Codex 子项禁用不可用**：CLI 无子项机制，UI 不提供假开关（插件级启停可用）；子项级启停仅对 Claude 引擎真正落盘（skill/command/agent stash 搬移、MCP 条目移出/写回）。
- **OpenCode 无子项/启停**：CLI 无 disable/子项概念，共用中心对 OpenCode 来源 skill 只读展示。
- **Codex 单插件更新为模拟实现**：市场刷新+卸载重装；重装失败时旧版已移除，需手动重装（UI 会如实提示）。
- **OpenCode JSONC 卸载需手工处理**：列表读取支持 JSONC，但自动卸载只修改严格 JSON，防止注释和原有格式丢失。
- **子项启停分派依赖 Claude 注册表**：注册表读取失败时按 Codex 分派，Claude 插件开关会静默不生效；需要看日志定位。
- **共用 MCP 不自动重分发**：编辑登记条目后已分发引擎只显示「有更新」，需在矩阵逐引擎「重新分发」；http 形态带自定义 `headers` 无法分发到 Codex（该格显示「不支持」）。
- **`--scope user` 固定**：Claude 插件安装/卸载强制使用 user scope，不支持 project / local scope 的管理 UI。
- **依赖外部 CLI**：`claude`、`opencode`、`codex`、`pi` 或 `omp` 未安装、PATH 未配置时，对应引擎的安装与更新功能不可用。环境检测（`/envcheck`）会标记这些问题并提供一键修复入口，详见 [./faq.md](./faq.md)。
- **`RefreshPlugins` 聚合错误**：Claude 的 `RefreshPlugins` 把各子调用错误用 `errors.Join` 聚合返回；调用方应同时处理"部分成功"。

> 待核实：Codex `~/.codex/` 下市场与状态文件的确切命名。
