# Amagi CodeBox API Reference

本文档整理了 `main.go` 中通过 Wails `Bind` 暴露的全部公开方法，按服务分组列出。

## Table of Contents

- [App (`app`)](#app-app)
- [Plugin Service (`app.Plugins`)](#plugin-service-appplugins)
- [OpenCode Plugin Service (`app.OpenCodePlugins`)](#opencode-plugin-service-appopencodeplugins)
- [Codex Plugin Service (`app.CodexPlugins`)](#codex-plugin-service-appcodexplugins)
- [Pi Plugin Service (`app.PiPlugins`)](#pi-plugin-service-apppiplugins)
- [Omp Plugin Service (`app.OmpPlugins`)](#omp-plugin-service-appompplugins)
- [Config Service (`app.Config`)](#config-service-appconfig)
- [Secrets Service (`app.Secrets`)](#secrets-service-appsecrets)
- [Paths Service (`app.Paths`)](#paths-service-apppaths)
- [Logging Service (`app.Log`)](#logging-service-applog)
- [Settings Service (`app.Settings`)](#settings-service-appsettings)
- [Updater Service (`app.Updater`)](#updater-service-appupdater)
- [OpenCode Config Service (`app.OpenCodeConfig`)](#opencode-config-service-appopencodeconfig)
- [Pi Config Service (`app.PiConfig`)](#pi-config-service-apppiconfig)
- [Omp Config Service (`app.OmpConfig`)](#omp-config-service-appompconfig)
- [Agent Profiles Service (`app.AgentProfiles`)](#agent-profiles-service-appagentprofiles)
- [EnvCheck Service (`app.EnvCheck`)](#envcheck-service-appenvcheck)
- [Usage Service (`app.Usage`)](#usage-service-appusage)
- [Skins Service (`app.Skins`)](#skins-service-appskins)

## App (`app`)

### GetSettingsService
**Service**: App
**Parameters**: none
**Returns**: `*settings.Service`
**Description**: 返回设置服务实例，主要供远程层内部桥接使用，不是常规前端调用入口。

### GetPathsService
**Service**: App
**Parameters**: none
**Returns**: `*paths.PathsService`
**Description**: 返回路径服务实例，主要供远程层内部桥接使用。

### GetConfigService
**Service**: App
**Parameters**: none
**Returns**: `*config.ConfigService`
**Description**: 返回配置服务实例，主要供远程层内部桥接使用。

### GetSession
**Service**: App
**Parameters**: `sessionID (string)`
**Returns**: `session.SessionInfo`, `error`
**Description**: 按会话 ID 查询会话信息。

### GetLaunchCompensationDebts
**Service**: App
**Parameters**: none
**Returns**: `[]launchplan.CompensationDebt`
**Description**: 返回未能确认完成的启动配置补偿债务。投影仅包含 exact owner、effect、step、typed disposition、错误摘要、尝试次数与更新时间，不包含配置内容或 credential。

### RetryLaunchCompensationDebt
**Service**: App
**Parameters**: `owner (string)`
**Returns**: `error`
**Description**: 在 5 秒边界内重试一个 exact owner 的补偿。仅 confirmed 会删除该债务；unavailable/indeterminate 继续保守保留。

### GetExternalCleanupRecoveryStatus
**Service**: App
**Parameters**: none
**Returns**: `remote.ExternalCleanupRecoveryStatus`
**Description**: 返回隐私最小化的外部进程恢复状态。仅包含会话 ID、共享服务类型、恢复原因与是否已可安全确认；不暴露 PID、命令、环境变量、路径、provider 或终端内容。

### ConfirmExternalCleanupRecovery
**Service**: App
**Parameters**: `sessionID (string)`, `confirmed (bool)`
**Returns**: `remote.ExternalCleanupRecoveryResult`, `error`
**Description**: 显式确认旧外部终端已关闭。仅在 `confirmed=true`、OS 已证明进程不存在且 journal exact completion 成功时清理 owner/admission 并重算 Headroom 恢复 fence；不提供 force-clear。每次接受或拒绝都会记录 typed audit event。

### GetRemoteToken
**Service**: App
**Parameters**: none
**Returns**: `string`
**Description**: 返回远程 API 服务器当前 Bearer Token。

### GetRemoteStatus
**Service**: App
**Parameters**: none
**Returns**: `map[string]any`
**Description**: 返回远程服务器状态，包括端口、令牌和运行状态。

### RegenerateRemoteToken
**Service**: App
**Parameters**: none
**Returns**: `string`
**Description**: 重新生成远程 API Token 并返回新值。

### ToggleRemoteServer
**Service**: App
**Parameters**: `enabled (bool)`
**Returns**: `error`
**Description**: 根据布尔值启动或停止远程服务器。

### SetRemotePort
**Service**: App
**Parameters**: `port (int)`
**Returns**: `error`
**Description**: 更新远程服务器端口，并在需要时自动重启远程服务。

### Startup
**Service**: App
**Parameters**: `ctx (context.Context)`
**Returns**: `void`
**Description**: Wails 生命周期启动钩子，负责加载配置、启动托盘与远程服务。

### Shutdown
**Service**: App
**Parameters**: `ctx (context.Context)`
**Returns**: `void`
**Description**: Wails 生命周期关闭钩子，负责保存配置并停止相关后台组件。

### LaunchSession
**Service**: App
**Parameters**: `providerName (string)`, `presetName (string)`, `mode (string)`, `workDir (string)`, `useHeadroom (bool)`, `shellPath (string)`
**Returns**: `string`, `error`
**Description**: 按 provider/preset 启动 Claude Code 会话，返回会话 ID。

### StopSession
**Service**: App
**Parameters**: `sessionID (string)`
**Returns**: `error`
**Description**: 停止指定会话，兼容 PTY 会话和外部启动器会话。

### GetSessions
**Service**: App
**Parameters**: none
**Returns**: `[]session.SessionInfo`
**Description**: 返回全部会话列表。

### RemoveSession
**Service**: App
**Parameters**: `sessionID (string)`
**Returns**: `error`
**Description**: 删除已结束会话的记录。

### ClearStoppedSessions
**Service**: App
**Parameters**: none
**Returns**: `int`
**Description**: 清除所有已停止会话，并返回清理数量。

### LaunchCodexSession
**Service**: App
**Parameters**: `modelName (string)`, `providerID (string)`, `mode (string)`, `workDir (string)`, `shellPath (string)`
**Returns**: `string`, `error`
**Description**: 启动 Codex CLI 会话，可注入 provider 对应的认证信息。

### LaunchPiSession
**Service**: App
**Parameters**: `modelName (string)`, `providerID (string)`, `mode (string)`, `workDir (string)`, `shellPath (string)`
**Returns**: `string`, `error`
**Description**: 启动 Pi coding agent 会话。modelName 可为 terminal_preset 的 stable key（命中时用预设的 provider/model 覆盖参数）；providerID 非空时把 amagi Provider 翻译为 Pi 自定义 provider，合并写入 `~/.pi/agent/models.json`（保留已有 provider 与顶层配置），并以 `--provider`/`--model`/`--thinking` 参数启动；写入失败时回退内置 provider。

### GetProvidersByType
**Service**: App
**Parameters**: `providerType (string)`
**Returns**: `map[string]config.Provider`
**Description**: 返回指定类型的 provider 集合。

### LaunchOpenCode
**Service**: App
**Parameters**: `providerName (string)`, `mode (string)`, `workDir (string)`, `shellPath (string)`
**Returns**: `string`, `error`
**Description**: 启动 OpenCode 会话，并按 provider 类型注入相应环境变量。

### LaunchOmpSession
**Service**: App
**Parameters**: `modelName (string)`, `providerID (string)`, `mode (string)`, `workDir (string)`, `shellPath (string)`
**Returns**: `string`, `error`
**Description**: 启动 Oh My Pi (omp) 会话。providerID 非空时把 amagi Provider 翻译为 omp 自定义 provider（"amagi-<name>"）写入 ~/.omp/agent/models.yml，并以 --provider/--model/--thinking 参数启动；modelName 可为 terminal_preset 的 stable key。

### BrowseDirectory
**Service**: App
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 打开系统目录选择对话框并返回所选目录。

### QuickLaunch
**Service**: App
**Parameters**: `providerName (string)`, `presetName (string)`, `useHeadroom (bool)`
**Returns**: `error`
**Description**: 使用兼容接口以终端模式快速启动 Claude 会话。

### SaveAllConfig
**Service**: App
**Parameters**: none
**Returns**: `error`
**Description**: 将配置、密钥、路径和设置全部持久化到磁盘。

### GetAppInfo
**Service**: App
**Parameters**: none
**Returns**: `map[string]any`
**Description**: 返回应用版本、配置目录和运行中会话数。

### CheckForUpdate
**Service**: App
**Parameters**: none
**Returns**: `*updater.UpdateInfo`, `error`
**Description**: 调用更新服务检查是否存在新版本。

### DownloadAndApplyUpdate
**Service**: App
**Parameters**: none
**Returns**: `error`
**Description**: 下载并应用更新，并通过 Wails 事件上报下载进度。

### GetGitHubToken
**Service**: App
**Parameters**: none
**Returns**: `string`
**Description**: 返回当前保存的 GitHub Token。

### SetGitHubToken
**Service**: App
**Parameters**: `token (string)`
**Returns**: `error`
**Description**: 保存 GitHub Token，并同步到更新服务。

### GetLogs
**Service**: App
**Parameters**: `level (string)`, `source (string)`, `keyword (string)`, `limit (int)`
**Returns**: `[]logging.Entry`
**Description**: 返回日志列表，支持级别、来源和关键字过滤。

### GetLogSources
**Service**: App
**Parameters**: none
**Returns**: `[]string`
**Description**: 返回所有日志来源。

### GetLogFiles
**Service**: App
**Parameters**: none
**Returns**: `[]string`
**Description**: 返回磁盘日志文件列表。

### GetLogFileContent
**Service**: App
**Parameters**: `filename (string)`
**Returns**: `string`, `error`
**Description**: 读取指定日志文件的内容。

### ClearLogs
**Service**: App
**Parameters**: none
**Returns**: `void`
**Description**: 清空内存中的日志条目。

### ExportLogs
**Service**: App
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 以 JSON 字符串形式导出当前内存日志。

### PtyWrite
**Service**: App
**Parameters**: `sessionID (string)`, `data (string)`
**Returns**: `error`
**Description**: 向指定 PTY 会话写入 base64 编码输入。

### PtyWriteLarge
**Service**: App
**Parameters**: `sessionID (string)`, `data (string)`
**Returns**: `error`
**Description**: 向指定 PTY 会话分块写入大段 base64 编码输入。

### SaveClipboardImage
**Service**: App
**Parameters**: `base64Data (string)`
**Returns**: `string`, `error`
**Description**: 将 base64 图片保存为临时 PNG 文件并返回绝对路径。

### PtyResize
**Service**: App
**Parameters**: `sessionID (string)`, `cols (int)`, `rows (int)`
**Returns**: `error`
**Description**: 调整指定 PTY 会话的尺寸。

### GetOutputHistorySnapshot
**Service**: App
**Parameters**: `sessionID (string)`
**Returns**: `string`, `error`
**Description**: 返回指定 PTY 会话的输出历史快照。返回 JSON：`{"data": "<base64>", "seq": <uint64>}`（启用 run-scoped 过滤时含 `runToken`/`runVersion`）。前端用 `seq` 对实时事件去重：任何 `seq <= 快照 seq` 的实时事件已包含在快照内。

### GetPtyDimensions
**Service**: App
**Parameters**: `sessionID (string)`
**Returns**: `cols (int)`, `rows (int)`, `err (error)`
**Description**: 返回指定 PTY 会话的当前尺寸。

### OpenFileInEditor
**Service**: App
**Parameters**: `filePath (string)`, `line (int)`
**Returns**: `error`
**Description**: 使用系统默认程序打开指定文件；`line` 仅保留兼容位。

### GetKeyDiagnostics
**Service**: App
**Parameters**: none
**Returns**: `map[string]map[string]string`
**Description**: 汇总所有 provider 的密钥来源诊断信息。

### ExportConfigToFile
**Service**: App
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 打开保存对话框，将可移植的完整配置导出到 JSON 文件。v2 快照包含 provider/全部密钥、各引擎 preset、应用设置、路径、自定义环境变量、价格表、OpenCode 全局配置，以及 CLI 独立配置全文快照（pi 的 `models.json`/`auth.json`/`amagi.json`、omp 的 `config.yml`/`models.yml`；仅当源设备对应文件存在且内容合法时导出，缺失或损坏的字段记 Warn 跳过、不阻断导出）。导出文件包含明文敏感信息（provider API key、pi auth token、models 内联 apiKey 等），写入权限为当前用户可读写。

### ImportConfigFromFile
**Service**: App
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 打开文件选择对话框导入配置。v2 完整快照采用替换语义并在失败时尽力回滚，成功后需重启应用；快照中存在的 CLI 独立配置段会整体替换目标设备对应文件（与 codebox 管理的 provider 配置共存，后续启动 pi/omp 会话时仍会合并写入 `amagi-<name>` 托管条目），缺失段（含旧版 v2 导出文件）自动跳过；v1 文件继续兼容原有 provider/preset 导入。日志、会话与用量数据库、远程配对设备、插件实体、运行时缓存不属于可移植配置。

### GetProviderExportJSON
**Service**: App
**Parameters**: `providerName (string)`
**Returns**: `string`, `error`
**Description**: 返回单个 provider 的格式化导出 JSON，包含 API Key。

### SaveProviderFromJSON
**Service**: App
**Parameters**: `providerName (string)`, `jsonStr (string)`
**Returns**: `error`
**Description**: 从 JSON 字符串解析并保存指定 provider 配置，同时更新 API Key。

### UpdateProvider
**Service**: App
**Parameters**: `oldName (string)`, `newName (string)`, `providerJSON (string)`
**Returns**: `error`
**Description**: 统一编辑提供商入口，支持改名与属性更新。

  - `oldName == newName`：复用 `SaveProviderFromJSON` 路径（属性覆盖 + API Key 同步），零副作用。
  - `oldName != newName`（改名）：
    1. 迁移 config 内所有引用（Models key、TerminalPresets 的 stable key + Provider 字段、OpenCodePresets 的 bindings LocalProvider）。
    2. 覆盖新属性。
    3. secrets 密钥迁移：JSON 含新 API Key 则写入新 name + 删旧；不含则迁移旧密钥到新 name + 删旧；都没有则跳过。

  **校验**：`newName` 非空、不含 `/`（会破坏 stable key）；`oldName` 必须存在；`newName` 不得与其他 provider 重名（oldName 自身除外）。

  **错误**：
  - `provider name is required`
  - `invalid provider name: must not contain '/'`
  - `provider not found: {oldName}`
  - `provider already exists: {newName}`
  - `config renamed but secrets save failed: ...; please re-enter API key for {newName}`（降级提示）

  **原子性**：config 单文件原子写（.tmp + rename）；secrets 迁移在 config 写盘成功后进行，失败不回滚 config（降级为提示用户重填密钥）。

### GetUrlHistory
**Service**: App
**Parameters**: `providerID (string)`
**Returns**: `[]string`, `error`
**Description**: 返回指定 provider 的 URL 历史。

### AddUrlToHistory
**Service**: App
**Parameters**: `providerID (string)`, `url (string)`
**Returns**: `error`
**Description**: 将 URL 添加到指定 provider 的历史记录中，并自动去重。

### RemoveUrlFromHistory
**Service**: App
**Parameters**: `providerID (string)`, `url (string)`
**Returns**: `error`
**Description**: 从指定 provider 的 URL 历史中移除指定项。

### GetEnvVars
**Service**: App
**Parameters**: none
**Returns**: `[]envvars.EnvVar`, `error`
**Description**: 返回所有自定义环境变量。

### SetEnvVar
**Service**: App
**Parameters**: `key (string)`, `value (string)`
**Returns**: `error`
**Description**: 设置单个自定义环境变量。

### DeleteEnvVar
**Service**: App
**Parameters**: `key (string)`
**Returns**: `error`
**Description**: 删除指定自定义环境变量。

### ImportEnvVars
**Service**: App
**Parameters**: `jsonStr (string)`
**Returns**: `error`
**Description**: 从 JSON 字符串全量导入环境变量。

### ExportEnvVars
**Service**: App
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 将环境变量导出为 JSON 字符串。

### GetEnvVarsJSON
**Service**: App
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 返回供 JSON 编辑器使用的环境变量 JSON。

### SaveEnvVarsJSON
**Service**: App
**Parameters**: `jsonStr (string)`
**Returns**: `error`
**Description**: 从 JSON 字符串保存环境变量，等同于导入。

### ExportEnvVarsToFile
**Service**: App
**Parameters**: none
**Returns**: `error`
**Description**: 打开保存对话框，将环境变量导出到文件。

### ImportEnvVarsFromFile
**Service**: App
**Parameters**: none
**Returns**: `error`
**Description**: 打开文件选择对话框，从文件导入环境变量。

## Plugin Service (`app.Plugins`)

### GetMarketplaces
**Service**: Plugin Service
**Parameters**: none
**Returns**: `[]Marketplace`, `error`
**Description**: 返回已知插件市场列表，优先读取 `claude plugin marketplace list --json`，失败时回退到本地文件。

### GetInstalledPlugins
**Service**: Plugin Service
**Parameters**: none
**Returns**: `[]InstalledPlugin`, `error`
**Description**: 返回已安装插件列表，优先读取 `claude plugin list --json`。

### GetPluginDetail
**Service**: Plugin Service
**Parameters**: `pluginID (string)`
**Returns**: `*PluginDetail`, `error`
**Description**: 返回指定插件的详细信息，包括 manifest、skills、agents、commands、hooks 和 MCP 配置。

### InstallPlugin
**Service**: Plugin Service
**Parameters**: `pluginName (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 使用 Claude CLI 安装用户级插件。

### UninstallPlugin
**Service**: Plugin Service
**Parameters**: `pluginID (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 卸载指定插件。

### EnablePlugin
**Service**: Plugin Service
**Parameters**: `pluginID (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 启用指定插件。

### DisablePlugin
**Service**: Plugin Service
**Parameters**: `pluginID (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 禁用指定插件。

### UpdatePlugin
**Service**: Plugin Service
**Parameters**: `pluginID (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 更新指定插件。

### UpdateMarketplace
**Service**: Plugin Service
**Parameters**: `name (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 更新指定插件市场。

### AddMarketplace
**Service**: Plugin Service
**Parameters**: `source (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 添加插件市场源。

### RemoveMarketplace
**Service**: Plugin Service
**Parameters**: `name (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 删除指定插件市场。

### GetAvailablePlugins
**Service**: Plugin Service
**Parameters**: none
**Returns**: `[]interface{}`, `error`
**Description**: 返回可安装插件列表。

### RefreshPlugins
**Service**: Plugin Service
**Parameters**: none
**Returns**: `error`
**Description**: 刷新市场、已安装插件和可用插件缓存。

## OpenCode Plugin Service (`app.OpenCodePlugins`)

### ListInstalledPlugins
**Service**: OpenCode Plugin Service
**Parameters**: none
**Returns**: `[]opencodeplugin.Plugin`, `error`
**Description**: 从 OpenCode 全局 JSON/JSONC 配置读取已启用插件，并补充本地缓存中的包元数据。

### RefreshPlugins
**Service**: OpenCode Plugin Service
**Parameters**: none
**Returns**: `*opencodeplugin.PluginsData`, `error`
**Description**: 返回已安装插件和缓存缺失警告。

### GetPluginDetails
**Service**: OpenCode Plugin Service
**Parameters**: `spec (string)`
**Returns**: `*opencodeplugin.PluginDetail`, `error`
**Description**: 返回包信息、server / tui target 与可发现的 skills、agents、commands、hooks 和 MCP 资源。

### InstallPlugin
**Service**: OpenCode Plugin Service
**Parameters**: `spec (string)`
**Returns**: `*opencodeplugin.CommandResult`, `error`
**Description**: 执行 `opencode plugin <spec> --global`。

### UpdatePlugin
**Service**: OpenCode Plugin Service
**Parameters**: `spec (string)`
**Returns**: `*opencodeplugin.CommandResult`, `error`
**Description**: GitHub 插件查询远端稳定 SemVer tags，npm 插件查询 registry `latest`，生成不可变 `#tag` 或精确 `@version` spec 后执行 `opencode plugin <target> --global --force`；随后校验全局配置、缓存路径和包版本。`file://` 本地插件直接从源路径加载，不执行远端更新。

### UninstallPlugin
**Service**: OpenCode Plugin Service
**Parameters**: `spec (string)`
**Returns**: `*opencodeplugin.CommandResult`, `error`
**Description**: 从严格 JSON 全局配置的 `plugin` 数组移除指定项并保留缓存；JSONC 配置不会自动改写。

## Codex Plugin Service (`app.CodexPlugins`)

管理 Codex CLI 的插件市场与插件（`internal/codexplugin`）。

### ListMarketplaces
**Service**: Codex Plugin Service
**Parameters**: none
**Returns**: `[]CodexMarketplace`, `error`
**Description**: 返回已注册市场列表：合并 config.toml 注册与 `codex plugin marketplace list` CLI 结果；CLI 失败时从配置插件与本地缓存推断，全部失败才返回错误。

### AddMarketplace
**Service**: Codex Plugin Service
**Parameters**: `req (AddMarketplaceRequest)`
**Returns**: `*CommandResult`, `error`
**Description**: 执行 `codex plugin marketplace add <source>`。`AddMarketplaceRequest` 仅含 `Source` 字段。

### UpgradeMarketplace
**Service**: Codex Plugin Service
**Parameters**: `name (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 执行 `codex plugin marketplace upgrade <name>`。

### RemoveMarketplace
**Service**: Codex Plugin Service
**Parameters**: `name (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 执行 `codex plugin marketplace remove <name>`。

### ListPlugins
**Service**: Codex Plugin Service
**Parameters**: `marketplace (string)`
**Returns**: `[]CodexPlugin`, `error`
**Description**: 列出已安装插件（按市场过滤，空串返回全部）。优先读取 `codex plugin list` CLI 输出，失败时回退 config.toml；含重复安装诊断。

### InstallPlugin
**Service**: Codex Plugin Service
**Parameters**: `selector (PluginSelector)`
**Returns**: `*CommandResult`, `error`
**Description**: 执行 `codex plugin add <pluginID>`，随后同步启用状态并做安装后校验。`PluginSelector` 以 `PluginID` 优先，未传时由 `Name` + `Marketplace` 组合定位。

### UninstallPlugin
**Service**: Codex Plugin Service
**Parameters**: `selector (PluginSelector)`
**Returns**: `*CommandResult`, `error`
**Description**: 卸载指定插件。

### SetPluginEnabled
**Service**: Codex Plugin Service
**Parameters**: `selector (PluginSelector)`, `enabled (bool)`
**Returns**: `*CommandResult`, `error`
**Description**: 启用/禁用指定插件。

### GetPluginDetails
**Service**: Codex Plugin Service
**Parameters**: `selector (PluginSelector)`
**Returns**: `*CodexPluginDetail`, `error`
**Description**: 返回指定插件详情：manifest 内容与扫描到的 skills、agents、commands、hooks、MCP 资源。

### ListAvailablePlugins
**Service**: Codex Plugin Service
**Parameters**: none
**Returns**: `[]CodexAvailablePlugin`, `error`
**Description**: 从已注册市场枚举可安装插件。

### RefreshPlugins
**Service**: Codex Plugin Service
**Parameters**: none
**Returns**: `*CodexPluginsData`, `error`
**Description**: 聚合市场、已安装与可安装列表；CLI 错误全部收纳进 `Warnings` 字段，仅完全无数据时才向上抛错。

### SetPluginSubItemEnabled
**Service**: Codex Plugin Service
**Parameters**: `pluginId (string)`, `subItemType (string)`, `subItemId (string)`, `enabled (bool)`
**Returns**: `error`
**Description**: 兼容接口：Codex 插件暂不支持子项级禁用，记录日志后返回 nil。

## Pi Plugin Service (`app.PiPlugins`)

管理 pi 的包（登记于 `~/.pi/agent/settings.json` 的 `packages[]`，`internal/piplugin`）。

### ListInstalledPackages
**Service**: Pi Plugin Service
**Parameters**: none
**Returns**: `[]Package`, `error`
**Description**: 列出 settings.json 中登记的全部包，附实体元数据。

### RefreshPackages
**Service**: Pi Plugin Service
**Parameters**: none
**Returns**: `*PackagesData`, `error`
**Description**: 刷新并返回聚合数据，含"已登记但实体目录缺失"告警列表。

### GetPackageDetails
**Service**: Pi Plugin Service
**Parameters**: `source (string)`
**Returns**: `*PackageDetail`, `error`
**Description**: 返回单个包的详情（含扫描到的子资源）。未登记返回明确错误。

### InstallPackage
**Service**: Pi Plugin Service
**Parameters**: `source (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 通过 pi CLI 安装包（写 settings.json + 拉取实体）。

### RemovePackage
**Service**: Pi Plugin Service
**Parameters**: `source (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 通过 pi CLI 移除包（从 settings.json `packages[]` 删除；实体目录保留）。

### UpdatePackage
**Service**: Pi Plugin Service
**Parameters**: `source (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 通过 pi CLI 更新单个包；仅允许更新已登记的包（未登记返回明确错误，避免 CLI 静默无操作）。

## Omp Plugin Service (`app.OmpPlugins`)

管理 omp 的插件（npm + marketplace，`internal/ompplugin`）。

### ListPlugins
**Service**: Omp Plugin Service
**Parameters**: none
**Returns**: `[]Plugin`, `error`
**Description**: 执行 `omp plugin list --json` 列出已安装插件。

### RefreshPlugins
**Service**: Omp Plugin Service
**Parameters**: none
**Returns**: `*PluginsData`, `error`
**Description**: 返回聚合数据（列表 + 解析降级 Warnings 信封）。

### InstallPlugin
**Service**: Omp Plugin Service
**Parameters**: `spec (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 执行 `omp install <spec>`；支持 npm/git/local/marketplace ref，含命令行元字符的 spec 会被拒绝（防注入）。

### UninstallPlugin
**Service**: Omp Plugin Service
**Parameters**: `name (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 执行 `omp plugin uninstall <name>`（npm 与 marketplace 由 CLI 自动路由）。

### SetPluginEnabled
**Service**: Omp Plugin Service
**Parameters**: `name (string)`, `enabled (bool)`
**Returns**: `*CommandResult`, `error`
**Description**: 执行 `omp plugin enable|disable <name>`。

### UpgradePlugin
**Service**: Omp Plugin Service
**Parameters**: `name (string)`
**Returns**: `*CommandResult`, `error`
**Description**: 升级单个插件：marketplace 插件走 `omp plugin upgrade <id>`；npm 或未识别目标走 `omp install <name> --force` 重装升级。

## Config Service (`app.Config`)

### Load
**Service**: Config Service
**Parameters**: none
**Returns**: `error`
**Description**: 从 `models.json` 加载配置，不存在时使用默认配置。

### Save
**Service**: Config Service
**Parameters**: none
**Returns**: `error`
**Description**: 将当前配置原子写回磁盘。

### GetConfig
**Service**: Config Service
**Parameters**: none
**Returns**: `*AppConfig`
**Description**: 返回完整配置对象的副本。

### GetProviders
**Service**: Config Service
**Parameters**: none
**Returns**: `map[string]Provider`
**Description**: 返回全部 provider 的副本映射。

### GetProviderNames
**Service**: Config Service
**Parameters**: none
**Returns**: `[]string`
**Description**: 返回全部 provider 名称列表。

### GetProvider
**Service**: Config Service
**Parameters**: `name (string)`
**Returns**: `*Provider`, `error`
**Description**: 返回指定 provider 配置。

### SaveProvider
**Service**: Config Service
**Parameters**: `name (string)`, `p (Provider)`
**Returns**: `error`
**Description**: 保存或覆盖一个 provider，并立即写入磁盘。

### DeleteProvider
**Service**: Config Service
**Parameters**: `name (string)`
**Returns**: `error`
**Description**: 删除指定 provider，并立即持久化。

### RenameProvider
**Service**: Config Service
**Parameters**: `oldName (string)`, `newName (string)`
**Returns**: `error`
**Description**: [内部方法] 重命名 provider，迁移 config 内所有引用（Models key、TerminalPresets stable key + Provider 字段、OpenCodePresets bindings LocalProvider），单次原子写盘。不含 secrets（由 `App.UpdateProvider` 在 App 层编排）。此方法主要供 `App.UpdateProvider` 内部调用，前端不直接通过 ConfigService 调用。

### GetPresets
**Service**: Config Service
**Parameters**: `providerName (string)`
**Returns**: `map[string]Preset`, `error`
**Description**: 返回指定 provider 的 preset 列表。

### SnapshotProvider
**Service**: Config Service
**Parameters**: `name (string)`
**Returns**: `*Provider`, `error`
**Description**: [内部方法] 单次读锁内返回 provider 深快照（结构体字段 + Presets 外层 map 一起拷贝，Presets 为 nil 时返回空 map）。供 `App.LaunchSession` / `LaunchOpenCode` 的 terminal_preset 桥接使用，保证 provider 与 presets 同配置代际，避免 GetProvider + GetPresets 两次独立加锁之间并发保存造成的跨代混合。前端不直接调用。

### SavePreset
**Service**: Config Service
**Parameters**: `providerName (string)`, `presetName (string)`, `p (Preset)`
**Returns**: `error`
**Description**: 保存指定 provider 下的一个 preset。

### DeletePreset
**Service**: Config Service
**Parameters**: `providerName (string)`, `presetName (string)`
**Returns**: `error`
**Description**: 删除指定 provider 下的一个 preset。

### GetAgentTeams
**Service**: Config Service
**Parameters**: none
**Returns**: `AgentTeamsConfig`
**Description**: 返回 Agent Teams 配置。

### SetAgentTeams
**Service**: Config Service
**Parameters**: `config (AgentTeamsConfig)`
**Returns**: `error`
**Description**: 更新 Agent Teams 配置并立即保存。

### GetUrlHistory
**Service**: Config Service
**Parameters**: `providerID (string)`
**Returns**: `[]string`, `error`
**Description**: 返回指定 provider 的 URL 历史记录。

### AddUrlToHistory
**Service**: Config Service
**Parameters**: `providerID (string)`, `url (string)`
**Returns**: `error`
**Description**: 添加 URL 到历史，自动去重、限制最多 20 条并立即保存。

### RemoveUrlFromHistory
**Service**: Config Service
**Parameters**: `providerID (string)`, `url (string)`
**Returns**: `error`
**Description**: 从指定 provider 的 URL 历史中删除一项并保存。

## Secrets Service (`app.Secrets`)

### Load
**Service**: Secrets Service
**Parameters**: none
**Returns**: `error`
**Description**: 从加密的 `secrets.enc` 文件加载密钥缓存。

### Save
**Service**: Secrets Service
**Parameters**: none
**Returns**: `error`
**Description**: 使用 DPAPI 加密并保存当前密钥缓存。

### GetAPIKey
**Service**: Secrets Service
**Parameters**: `provider (string)`
**Returns**: `string`, `error`
**Description**: 返回指定 provider 的已存储 API Key。

### SetAPIKey
**Service**: Secrets Service
**Parameters**: `provider (string)`, `apiKey (string)`
**Returns**: `error`
**Description**: 在内存中设置指定 provider 的 API Key。

### DeleteAPIKey
**Service**: Secrets Service
**Parameters**: `provider (string)`
**Returns**: `error`
**Description**: 从内存缓存中删除指定 provider 的 API Key。

### HasAPIKey
**Service**: Secrets Service
**Parameters**: `provider (string)`
**Returns**: `bool`
**Description**: 检查指定 provider 是否存在已存储 API Key。

### GetAllProviders
**Service**: Secrets Service
**Parameters**: none
**Returns**: `[]string`
**Description**: 返回所有已存储密钥的 provider 名称列表。

### GetAPIKeyWithFallback
**Service**: Secrets Service
**Parameters**: `provider (string)`
**Returns**: `string`, `string`
**Description**: 先查存储密钥，再查环境变量，返回 `(apiKey, source)`。

### GetKeyDiagnostics
**Service**: Secrets Service
**Parameters**: `providerNames ([]string)`
**Returns**: `map[string]map[string]string`
**Description**: 返回每个 provider 的密钥来源、掩码值、长度和环境变量诊断信息。

## Paths Service (`app.Paths`)

### Load
**Service**: Paths Service
**Parameters**: none
**Returns**: `error`
**Description**: 从 `paths.json` 加载路径配置。

### Save
**Service**: Paths Service
**Parameters**: none
**Returns**: `error`
**Description**: 将路径配置写回磁盘。

### GetPaths
**Service**: Paths Service
**Parameters**: none
**Returns**: `[]PathEntry`
**Description**: 返回全部已保存路径。

### GetDefaultPath
**Service**: Paths Service
**Parameters**: none
**Returns**: `string`
**Description**: 返回默认工作路径。

### SetDefaultPath
**Service**: Paths Service
**Parameters**: `path (string)`
**Returns**: `error`
**Description**: 更新默认工作路径。

### AddPath
**Service**: Paths Service
**Parameters**: `entry (PathEntry)`
**Returns**: `error`
**Description**: 添加一个新的保存路径。

### RemovePath
**Service**: Paths Service
**Parameters**: `path (string)`
**Returns**: `error`
**Description**: 删除指定保存路径；如果该路径是默认路径则同时清空默认值。

### UpdateLabel
**Service**: Paths Service
**Parameters**: `path (string)`, `label (string)`
**Returns**: `error`
**Description**: 更新指定路径的显示标签。

### ValidatePath
**Service**: Paths Service
**Parameters**: `path (string)`
**Returns**: `bool`
**Description**: 检查给定路径是否存在且为目录。

## Logging Service (`app.Log`)

### Debug
**Service**: Logging Service
**Parameters**: `source (string)`, `message (string)`, `detail (...string)`
**Returns**: `void`
**Description**: 写入一条 DEBUG 级日志。

### Info
**Service**: Logging Service
**Parameters**: `source (string)`, `message (string)`, `detail (...string)`
**Returns**: `void`
**Description**: 写入一条 INFO 级日志。

### Warn
**Service**: Logging Service
**Parameters**: `source (string)`, `message (string)`, `detail (...string)`
**Returns**: `void`
**Description**: 写入一条 WARN 级日志。

### Error
**Service**: Logging Service
**Parameters**: `source (string)`, `message (string)`, `detail (...string)`
**Returns**: `void`
**Description**: 写入一条 ERROR 级日志。

### GetEntries
**Service**: Logging Service
**Parameters**: `level (string)`, `source (string)`, `keyword (string)`, `limit (int)`
**Returns**: `[]Entry`
**Description**: 按条件过滤并返回内存日志。

### GetSources
**Service**: Logging Service
**Parameters**: none
**Returns**: `[]string`
**Description**: 返回所有出现过的日志来源。

### GetLogFiles
**Service**: Logging Service
**Parameters**: none
**Returns**: `[]string`
**Description**: 返回日志目录中的日志文件名列表。

### GetLogFileContent
**Service**: Logging Service
**Parameters**: `filename (string)`
**Returns**: `string`, `error`
**Description**: 读取指定日志文件内容，并阻止目录穿越。

### ClearEntries
**Service**: Logging Service
**Parameters**: none
**Returns**: `void`
**Description**: 清空内存中的日志条目。

### ExportJSON
**Service**: Logging Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 将当前内存日志导出为 JSON 字符串。

### Close
**Service**: Logging Service
**Parameters**: none
**Returns**: `void`
**Description**: 关闭当前日志文件句柄。

## PTY 绑定说明（`app.Pty` 已下线）

> **迁移警示**：raw `app.Pty`（`internal/pty.Service`）**不在 Wails 绑定面内**，请勿在绑定面中查找 `Pty` 服务。
>
> `bind_list.go`（C-01/T-24 gate）与 `bind_manifest_test.go` 断言原始 `pty.Service` / `headroom.HeadroomService` 对象不可达；PTY 读写请一律走 `App` 上的门面方法：
>
> - `App.PtyWrite(sessionID, data)` / `App.PtyWriteLarge(sessionID, data)` — 写入终端输入（base64）
> - `App.PtyResize(sessionID, cols, rows)` — 调整终端尺寸
> - `App.GetPtyDimensions(sessionID)` — 查询当前尺寸
> - `App.GetOutputHistorySnapshot(sessionID)` — 原子 `{data, seq}` 回放快照
> - `App.SaveClipboardImage(base64Data)` — 保存剪贴板图片
>
> 旧文档中的 `RegisterOutputCallback` / `RegisterExitCallback` / `RegisterResizeCallback`（及对应注销方法）、`StopAllSessions`、`GetOutputHistory` 均**不存在于绑定面**（`bind_manifest_test.go` 的 `appForbiddenMethods` 断言它们不得导出）。回调注册是 Go 内部（`internal/remote` / `internal/pty`）的桥接职责；前端实时输出走 Wails runtime 事件（`pty:data:<sessionID>` / `pty:exit:<sessionID>`，见 `frontend/src/composables/useTerminalEngine.ts`），历史回放走 `App.GetOutputHistorySnapshot`。

## Settings Service (`app.Settings`)

### Load
**Service**: Settings Service
**Parameters**: none
**Returns**: `error`
**Description**: 从 `settings.json` 加载应用设置。

### Save
**Service**: Settings Service
**Parameters**: none
**Returns**: `error`
**Description**: 将当前设置原子写回磁盘。

### GetDashboardDefaults
**Service**: Settings Service
**Parameters**: none
**Returns**: `DashboardDefaults`
**Description**: 返回仪表盘默认值配置。

### SetDashboardDefaults
**Service**: Settings Service
**Parameters**: `d (DashboardDefaults)`
**Returns**: `error`
**Description**: 更新仪表盘默认值并立即保存。

### GetShellPaths
**Service**: Settings Service
**Parameters**: none
**Returns**: `[]ShellEntry`
**Description**: 返回已配置的 shell 路径列表。

### AddShellPath
**Service**: Settings Service
**Parameters**: `entry (ShellEntry)`
**Returns**: `error`
**Description**: 添加一个 shell 路径并保存。

### RemoveShellPath
**Service**: Settings Service
**Parameters**: `path (string)`
**Returns**: `error`
**Description**: 删除指定 shell 路径并保存。

### GetTerminalSettings
**Service**: Settings Service
**Parameters**: none
**Returns**: `TerminalSettings`
**Description**: 返回终端相关设置。

### GetSkinSettings
**Service**: Settings Service
**Parameters**: none
**Returns**: `SkinSettings`
**Description**: 返回皮肤设置（`{enabled, imageId, dim, blur, opacity, textBoost}`；默认 `{false, "", 35, 0, 70, 0}`）。`opacity` 为内容面板（窗口/侧栏/卡片等）不透明度百分比 0..100（0=面板全透出皮肤图片，100=面板不透明）。`textBoost` 为前景文字加深（字体浓度）强度百分比 0..100，作用于前景文字加深与底衬强度（0=不增强、保持现状），与背景调光 `dim`（背景蒙版层）、面板透明度 `opacity`（内容面板本体）三者独立解耦。老 settings.json 无 `skin` 键时 Load 合并为默认值；含 `skin` 键但缺 `opacity` 子键的老文件按 0 读入（0 为合法档位，不回填默认）；缺 `textBoost` 子键同样读入 0，且 0 即默认值，无突变。

### SetSkinSettings
**Service**: Settings Service
**Parameters**: `sk (SkinSettings)`
**Returns**: `error`
**Description**: 更新皮肤设置并保存。`dim`/`opacity`/`textBoost` clamp 到 [0,100]、`blur` clamp 到 [0,40]，越界取边界值而非报错。ImageID 存在性校验在 Skins 服务层（`app.Skins.SetSkinSettings`）。

### SetTerminalSettings
**Service**: Settings Service
**Parameters**: `t (TerminalSettings)`
**Returns**: `error`
**Description**: 更新终端设置并保存。

### GetRemotePort
**Service**: Settings Service
**Parameters**: none
**Returns**: `int`
**Description**: 返回远程 API 端口，未设置时返回默认值 `8680`。

### SetRemotePort
**Service**: Settings Service
**Parameters**: `port (int)`
**Returns**: `error`
**Description**: 更新远程 API 端口并保存。

### GetMobileWebRoot
**Service**: Settings Service
**Parameters**: none
**Returns**: `string`
**Description**: 返回移动端 Web 根目录。

### SetMobileWebRoot
**Service**: Settings Service
**Parameters**: `path (string)`
**Returns**: `error`
**Description**: 更新移动端 Web 根目录并保存。

### GetGitHubToken
**Service**: Settings Service
**Parameters**: none
**Returns**: `string`
**Description**: 返回 GitHub Token。

### SetGitHubToken
**Service**: Settings Service
**Parameters**: `token (string)`
**Returns**: `error`
**Description**: 更新 GitHub Token 并保存。

### GetSettings
**Service**: Settings Service
**Parameters**: none
**Returns**: `*AppSettings`
**Description**: 返回完整设置对象的副本。

## Updater Service (`app.Updater`)

### SetToken
**Service**: Updater Service
**Parameters**: `token (string)`
**Returns**: `void`
**Description**: 设置 GitHub Personal Access Token，以访问私有仓库 Release。

### CheckForUpdate
**Service**: Updater Service
**Parameters**: none
**Returns**: `*UpdateInfo`, `error`
**Description**: 查询 GitHub 最新 Release，并返回版本差异信息。

### DownloadAndApply
**Service**: Updater Service
**Parameters**: `onProgress (func(downloaded, total int64))`
**Returns**: `error`
**Description**: 下载并替换当前可执行文件，成功后重启应用。

### CleanupOldBinary
**Service**: Updater Service
**Parameters**: none
**Returns**: `void`
**Description**: 清理上次更新留下的旧版本备份文件。

## OpenCode Config Service (`app.OpenCodeConfig`)

管理全局 OpenCode 配置文件（`$HOME/.config/opencode/opencode.json`），为前端设置页提供读写能力。

### GetOpenCodeConfig
**Service**: OpenCode Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 读取全局 OpenCode 配置文件，返回格式化后的 JSON 字符串。文件不存在时返回默认空对象 `{}`；文件存在但内容非合法 JSON 时原样返回原始内容，便于用户在编辑器中修正。

### SaveOpenCodeConfig
**Service**: OpenCode Config Service
**Parameters**: `content (string)` -- 必须为根节点为对象的合法 JSON
**Returns**: `error`
**Description**: 校验并保存全局 OpenCode 配置。传入内容必须是合法 JSON 且根节点为对象（`{}`），数组、字符串、数字和 null 均会被拒绝。写入采用原子方式（先写 `.tmp` 临时文件再 rename），父目录不存在时自动创建。

### GetOpenCodeConfigPath
**Service**: OpenCode Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 返回全局 OpenCode 配置文件的绝对路径（`$HOME/.config/opencode/opencode.json`），供前端展示。

## Pi Config Service (`app.PiConfig`)

读写 pi 的 agent 目录（`~/.pi/agent`）下的 `amagi.json` / `models.json` / `auth.json`（`internal/piconfig`）。

### GetAmagiConfig
**Service**: Pi Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 读取 `amagi.json` 内容。文件缺失时返回默认骨架；JSON 非法或根不是对象时原样返回内容，供用户在源码模式修复。

### SaveAmagiConfig
**Service**: Pi Config Service
**Parameters**: `content (string)` -- 必须为根节点为对象的合法 JSON
**Returns**: `error`
**Description**: 校验并保存 `amagi.json`。写入采用原子方式（临时文件 0600 + rename，rename 失败回退直接覆盖）。

### GetAmagiConfigPath
**Service**: Pi Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 返回 `amagi.json` 的绝对路径，供前端展示/复制。

### GetModelsConfig
**Service**: Pi Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 读取 `models.json`（provider 注册表）内容。文件缺失时返回只含空 `providers` 的骨架；非法 JSON 原样返回。

### SaveModelsConfig
**Service**: Pi Config Service
**Parameters**: `content (string)` -- 必须为根节点为对象的合法 JSON
**Returns**: `error`
**Description**: 校验并保存 `models.json`。文件含 `apiKey` 等敏感信息，原子写入（0600）。

### GetModelsConfigPath
**Service**: Pi Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 返回 `models.json` 的绝对路径，供前端展示。

### GetAuthConfig
**Service**: Pi Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 读取 `auth.json`（提供商凭据）内容。文件缺失时返回空对象骨架；非法 JSON 原样返回。文件含明文凭据，仅本地读取展示。

### SaveAuthConfig
**Service**: Pi Config Service
**Parameters**: `content (string)` -- 必须为根节点为对象的合法 JSON
**Returns**: `error`
**Description**: 校验并保存 `auth.json`。含明文凭据，原子写入（0600）。

### GetAuthConfigPath
**Service**: Pi Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 返回 `auth.json` 的绝对路径，供前端展示。

### GetPiModelCatalog
**Service**: Pi Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 读取 `models.json` 抽取 provider→models 目录（只读，不含 `apiKey` 等敏感字段），序列化为 JSON 返回，供前端下拉。文件缺失时返回空目录。

## Omp Config Service (`app.OmpConfig`)

读写 omp 的 agent 目录（`~/.omp/agent`）下的 `config.yml` / `models.yml`（`internal/ompconfig`）。

### GetOmpConfig
**Service**: Omp Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 读取 `config.yml` 内容。文件缺失时返回 `modelRoles: {}` 最小骨架；YAML 非法或根不是映射时原样返回内容，供用户在源码模式修复。

### SaveOmpConfig
**Service**: Omp Config Service
**Parameters**: `content (string)` -- 必须为根节点为映射的合法 YAML
**Returns**: `error`
**Description**: 校验并保存 `config.yml`。写入采用原子方式（临时文件 0600 + rename）。

### GetOmpConfigPath
**Service**: Omp Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 返回 `config.yml` 的绝对路径，供前端展示/复制。

### GetModelsConfig
**Service**: Omp Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 读取 `models.yml`（provider 注册表）内容。文件缺失时返回只含空 `providers` 的骨架；非法 YAML 原样返回。

### SaveModelsConfig
**Service**: Omp Config Service
**Parameters**: `content (string)` -- 必须为根节点为映射的合法 YAML
**Returns**: `error`
**Description**: 校验并保存 `models.yml`。文件含 `apiKey` 等敏感信息，原子写入（0600）。

### GetModelsConfigPath
**Service**: Omp Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 返回 `models.yml` 的绝对路径，供前端展示。

### GetOmpModelCatalog
**Service**: Omp Config Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 读取 `models.yml` 抽取 provider→models 目录（不含 `apiKey` 等敏感字段），并追加 `omp models ls --json` 返回的内置目录提供商（自定义条目优先），序列化为 JSON 返回。

## Agent Profiles Service (`app.AgentProfiles`)

命名 agent 配置档（公司/家一键切换）：把当前 live 配置快照为命名配置档并一键应用（`internal/agentprofile`）。管理 pi 的 `~/.pi/agent/amagi.json`（JSON）与 omp 的 `~/.omp/agent/config.yml`（YAML）两侧全文。配置档存储于 `~/.amagi-codebox/agent-profiles.json`（0600，原子写入）；Apply 覆盖已有 live 文件前生成 `<file>.bak-<epoch ms>` 备份（仅保留一份，新覆盖旧）。agentDir 解析优先 `$PI_CODING_AGENT_DIR`。

### ListAgentProfiles
**Service**: Agent Profiles Service
**Parameters**: none
**Returns**: `string`, `error`
**Description**: 返回存储全文 JSON（`{version,profiles:{<name>:{pi,omp,updatedAt}},lastApplied}`）。文件缺失时返回空骨架。

### GetAgentProfile
**Service**: Agent Profiles Service
**Parameters**: `name (string)`
**Returns**: `string`, `error`
**Description**: 返回单个配置档的 JSON（`{pi,omp,updatedAt}`，pi 为 amagi.json 全文、omp 为 config.yml 全文），预览用。

### CaptureAgentProfile
**Service**: Agent Profiles Service
**Parameters**: `name (string)`
**Returns**: `error`
**Description**: 把当前 live 配置快照为命名配置档（同名覆盖）。pi 的 `amagi.json` 必须存在且可读；omp 的 `config.yml` 缺失时记空串（该侧不管理）。

### SaveAgentProfile
**Service**: Agent Profiles Service
**Parameters**: `name (string)`, `piContent (string)` -- 非空时必须为合法 JSON, `ompContent (string)` -- 非空时必须为根节点为映射的合法 YAML
**Returns**: `error`
**Description**: 显式内容保存（前端编辑后落档）。空串表示“该侧不管理”；非法内容报错不写。

### ApplyAgentProfile
**Service**: Agent Profiles Service
**Parameters**: `name (string)`
**Returns**: `error`
**Description**: 应用命名配置档到 live 文件：pi 内容非空写 `~/.pi/agent/amagi.json`（原子写 + 0600）；omp 内容非空且 `~/.omp/agent` 存在时写 `~/.omp/agent/config.yml`。内容非法（pi 非合法 JSON / omp 非映射根 YAML）时报错且不写任何文件；成功后更新 lastApplied。

### DeleteAgentProfile
**Service**: Agent Profiles Service
**Parameters**: `name (string)`
**Returns**: `error`
**Description**: 删除命名配置档；删除的是 lastApplied 时同步清空。

## EnvCheck Service (`app.EnvCheck`)

CLI 工具环境检测、问题诊断与一键修复，以及 Claude Code 安装/卸载生命周期管理（`internal/envcheck`）。

### SetHeadroomVenvDir
**Service**: EnvCheck Service
**Parameters**: `dir (string)`
**Returns**: `void`
**Description**: [内部方法] 注入 CodeBox 管理的 headroom venv 目录（app.go 接线时调用），使检测/安装/启动/卸载指向同一 venv。须在首次 `CheckOne`/`Install` 前调用。

### SetHeadroomStopper
**Service**: EnvCheck Service
**Parameters**: `fn (func() (error, func()))`
**Returns**: `void`
**Description**: [内部方法] 注入 `CleanHeadroom` 删除 venv 前停止 headroom 代理子进程的回调（Windows 上运行中的 headroom.exe 会被 OS 锁住）。回调返回 `(stopErr, releaseDrain)`。

### CheckAll
**Service**: EnvCheck Service
**Parameters**: none
**Returns**: `*OverallStatus`, `error`
**Description**: 检测全部受支持的 CLI 工具并更新内存缓存。

### CheckOne
**Service**: EnvCheck Service
**Parameters**: `tool (CLITool)`
**Returns**: `*CheckStatus`, `error`
**Description**: 检测单个工具（`claude_code` / `opencode` / `codex` / `pi` / `omp` / `headroom`）。

### CheckLatestVersion
**Service**: EnvCheck Service
**Parameters**: `tool (CLITool)`
**Returns**: `latestVersion (string)`, `err (error)`
**Description**: 查询指定工具的最新可用版本。成功结果内存缓存 24 小时，避免重复请求 registry/包管理器。

### Install
**Service**: EnvCheck Service
**Parameters**: `tool (CLITool)`
**Returns**: `*InstallResult`, `error`
**Description**: 同步安装指定工具。

### Update
**Service**: EnvCheck Service
**Parameters**: `tool (CLITool)`
**Returns**: `*InstallResult`, `error`
**Description**: 同步更新指定工具。

### GetCachedStatus
**Service**: EnvCheck Service
**Parameters**: none
**Returns**: `*OverallStatus`
**Description**: 返回最近一次环境检测结果（可能为缓存）。

### StartInstallTool
**Service**: EnvCheck Service
**Parameters**: `tool (CLITool)`
**Returns**: `*OperationState`, `error`
**Description**: 启动异步安装操作，立即返回初始状态；实际工作在后台 goroutine 执行，可跨前端页面导航存活。同一 tool+kind 已在运行则返回当前状态；其他操作运行中返回 `ErrBusy`。

### StartUpdateTool
**Service**: EnvCheck Service
**Parameters**: `tool (CLITool)`
**Returns**: `*OperationState`, `error`
**Description**: 启动异步更新操作，并发语义同 `StartInstallTool`。

### StartInstallClaudeCodeWithMethod
**Service**: EnvCheck Service
**Parameters**: `method (ClaudeInstallMethod)`
**Returns**: `*OperationState`, `error`
**Description**: 按用户选择的渠道（`npm` / `native`）异步安装 Claude Code，操作状态生命周期同 `StartInstallTool`，但不回退自动渠道链。

### GetOperationState
**Service**: EnvCheck Service
**Parameters**: none
**Returns**: `*OperationState`
**Description**: 返回当前异步操作状态；无操作时返回 nil。

### GetEnvCheckSnapshot
**Service**: EnvCheck Service
**Parameters**: none
**Returns**: `*EnvCheckSnapshot`
**Description**: 返回组合快照（工具状态 + 当前操作），是前端的主轮询端点。

### RunFixAction
**Service**: EnvCheck Service
**Parameters**: `req (FixActionRequest)`
**Returns**: `*FixActionResult`, `error`
**Description**: 白名单修复动作的单一入口（`fix_path` / `install_tool` / `install_node` / `retry` / `manual_command` / `fix_claude_config` / `install_claude_method` / `clean_claude_install`）。不接受前端任意命令，白名单外的 action 返回错误。

### CheckClaudeConfig
**Service**: EnvCheck Service
**Parameters**: none
**Returns**: `*ClaudeConfigStatus`, `error`
**Description**: 扫描 Claude Code 配置文件，报告必需配置项是否存在。

### FixClaudeConfig
**Service**: EnvCheck Service
**Parameters**: `req (ConfigFixRequest)`
**Returns**: `*ConfigFixResult`, `error`
**Description**: 向目标文件写入单个配置项。仅写缺失的 key，已存在的 key 绝不覆盖。

### CleanClaudeCode
**Service**: EnvCheck Service
**Parameters**: `method (InstallMethod)`
**Returns**: `*InstallResult`, `error`
**Description**: 移除指定渠道（`npm` / `native`）的 Claude Code 安装，完成后校验其不再存在。与安装/更新共用同一操作门，重叠请求返回 `ErrBusy`。

### CleanHeadroom
**Service**: EnvCheck Service
**Parameters**: none
**Returns**: `*InstallResult`, `error`
**Description**: 移除 CodeBox 管理的 Headroom venv 目录。删除前调用注入的 stopper 停止代理；若代理仍被活跃会话依赖（`ErrHeadroomInUse`），在删除前中止并返回该错误。

### InstallClaudeCodeWithMethod
**Service**: EnvCheck Service
**Parameters**: `method (ClaudeInstallMethod)`
**Returns**: `*InstallResult`, `error`
**Description**: 按指定渠道同步安装 Claude Code，与异步操作共用序列化门。

## Usage Service (`app.Usage`)

用量聚合、成本统计与价格表管理（`internal/usage`，SQLite）。

### GetUsageSummary
**Service**: Usage Service
**Parameters**: `filter (SummaryFilter)`
**Returns**: `Summary`, `error`
**Description**: 返回仪表盘汇总：请求数、Token 总量（不重叠口径）、按币种成本等。`SummaryFilter` 含 `StartDate`/`EndDate`（UTC 闭区间，空=不限）、`AppType`、`Source`、`Provider`。

### GetDailyTrends
**Service**: Usage Service
**Parameters**: `filter (TrendFilter)`
**Returns**: `[]DailyTrendPoint`, `error`
**Description**: 返回日趋势折线图数据。`TrendFilter` 在 `SummaryFilter` 基础上增加 `Granularity`（day/week）与 `Days`（最近 N 天，与日期区间互斥）。

### GetModelDailyTrends
**Service**: Usage Service
**Parameters**: `filter (TrendFilter)`
**Returns**: `[]ModelDailyTrendPoint`, `error`
**Description**: 返回按模型分线的日趋势（不聚合模型），避免价格/Token 量级差异混入同一条折线。

### GetModelStats
**Service**: Usage Service
**Parameters**: `filter (StatFilter)`
**Returns**: `[]ModelStat`, `error`
**Description**: 返回按模型聚合的统计（Token、缓存命中率、成本等）。

### GetProviderStats
**Service**: Usage Service
**Parameters**: `filter (StatFilter)`
**Returns**: `[]ProviderStat`, `error`
**Description**: 返回按供应商聚合的统计。

### GetRequestLogs
**Service**: Usage Service
**Parameters**: `filter (LogFilter)`
**Returns**: `[]UsageRecord`, `error`
**Description**: 返回分页明细日志。`LogFilter` 增加 `Model`、`Page`（1 起）、`PageSize`（默认 50，上限 500）。

### SyncSessionUsage
**Service**: Usage Service
**Parameters**: none
**Returns**: `SyncResult`, `error`
**Description**: 阻塞执行一次同步（前端"立即同步"按钮），返回起止时间、新增记录数、处理数、扫描文件数与错误列表。

### GetSyncState
**Service**: Usage Service
**Parameters**: none
**Returns**: `[]SyncState`
**Description**: 返回所有来源的同步游标。

### GetModelPricing
**Service**: Usage Service
**Parameters**: none
**Returns**: `[]ModelPricing`
**Description**: 返回价格表全量。

### UpsertModelPricing
**Service**: Usage Service
**Parameters**: `mp (ModelPricing)`
**Returns**: `error`
**Description**: 新增或更新价格表条目；更新后按匹配到的模型重算历史估算成本。

### DeleteModelPricing
**Service**: Usage Service
**Parameters**: `id (string)`
**Returns**: `error`
**Description**: 删除自定义价格条目（内置条目不可删）。

### ResetModelPricing
**Service**: Usage Service
**Parameters**: none
**Returns**: `error`
**Description**: 重置为内置 seed 价格表，并重算全部历史估算成本。

### GetUnknownModels
**Service**: Usage Service
**Parameters**: none
**Returns**: `[]UnknownModel`, `error`
**Description**: 返回价格表未匹配的模型列表（含样本原始名称、请求数与最后出现时间）。

### Load
**Service**: Usage Service
**Parameters**: none
**Returns**: `error`
**Description**: [内部方法] 启动时加载 SQLite 数据库（app.go 接线调用）。

### Close
**Service**: Usage Service
**Parameters**: none
**Returns**: `error`
**Description**: [内部方法] 关闭数据库连接（app.go 接线调用）。

### SyncAll
**Service**: Usage Service
**Parameters**: none
**Returns**: `error`
**Description**: [内部方法] 执行全量同步（`SyncSessionUsage` 与启动/后台同步的内部实现）。

### StartBackgroundSync
**Service**: Usage Service
**Parameters**: `interval (time.Duration)`
**Returns**: `void`
**Description**: [内部方法] 启动后台周期同步（app.go 接线调用）。

### Record
**Service**: Usage Service
**Parameters**: `evt (UsageEvent)`
**Returns**: `bool`, `error`
**Description**: [内部方法] 记录一条用量事件（含去重，返回是否新增）。

### RecordForce
**Service**: Usage Service
**Parameters**: `evt (UsageEvent)`
**Returns**: `error`
**Description**: [内部方法] 强制记录一条用量事件。

### Pricing
**Service**: Usage Service
**Parameters**: none
**Returns**: `*PricingService`
**Description**: [内部方法] 返回价格表子服务实例（App 层内部使用）。

## Skins Service (`app.Skins`)

本地图片皮肤（皮肤/壁纸）管理服务：导入即拷贝到 `~/.amagi-codebox/skins/`（文件名 `<id>.<ext>`，id 为 16 字节随机 hex），源文件不受影响。前端通过 assetserver 自定义 Handler 以 `/skins/<file>` 只读访问图片。

### PickSkinImage
**Service**: Skins Service
**Parameters**: none
**Returns**: `*Skin`
**Description**: 弹出文件选择对话框（png/jpg/jpeg/webp）并导入所选图片。用户取消返回 `nil`。导入校验：魔数（防改后缀）、单文件 ≤ 20MB；png/jpeg 用标准库解析尺寸，webp 接受但尺寸记 0。返回 `Skin{id, fileName, url("/skins/<file>"), bytes, width, height, importedAt}`。

### ImportSkinImage
**Service**: Skins Service
**Parameters**: `path (string)`
**Returns**: `*Skin`
**Description**: 从本地路径导入皮肤图片（`PickSkinImage` 的无对话框内层，校验与拷贝逻辑与之一致）。

### ListSkins
**Service**: Skins Service
**Parameters**: none
**Returns**: `[]Skin`
**Description**: 枚举皮肤库（只认 png/jpg/jpeg/webp 扩展名，导入是唯一写入口），按导入时间降序。皮肤库不存在时返回空列表。

### RemoveSkin
**Service**: Skins Service
**Parameters**: `id (string)`
**Returns**: `error`
**Description**: 删除指定皮肤图片。当前皮肤已启用且正是该图片时返回错误（前端引导先停用）。

### GetSkinSettings
**Service**: Skins Service
**Parameters**: none
**Returns**: `SkinSettings`
**Description**: 返回皮肤设置（委托 Settings 服务）。

### SetSkinSettings
**Service**: Skins Service
**Parameters**: `sk (SkinSettings)`
**Returns**: `error`
**Description**: 更新皮肤设置（clamp 由 Settings 层完成）。`enabled=true` 时校验 ImageID 对应文件存在于皮肤库，不存在返回错误。

### SetContext
**Service**: Skins Service
**Parameters**: `ctx (context.Context)`
**Returns**: `void`
**Description**: [内部方法] 注入 Wails app context（Startup 接线调用，`PickSkinImage` 依赖它弹对话框）。

### AssetHandler
**Service**: Skins Service
**Parameters**: none
**Returns**: `http.Handler`
**Description**: [内部方法] 返回 `/skins/` 只读静态资源 Handler（main.go assetserver 装配：仅 GET、路径清洗防穿越、按扩展名 Content-Type、404 兕底、无目录列表）。
