# Amagi CodeBox API Reference

本文档整理了 `main.go` 中通过 Wails `Bind` 暴露的全部公开方法，按服务分组列出。

## Table of Contents

- [App (`app`)](#app-app)
- [Plugin Service (`app.Plugins`)](#plugin-service-appplugins)
- [Config Service (`app.Config`)](#config-service-appconfig)
- [Secrets Service (`app.Secrets`)](#secrets-service-appsecrets)
- [Proxy Service (`app.Proxy`)](#proxy-service-appproxy)
- [Paths Service (`app.Paths`)](#paths-service-apppaths)
- [Logging Service (`app.Log`)](#logging-service-applog)
- [PTY Service (`app.Pty`)](#pty-service-apppty)
- [Settings Service (`app.Settings`)](#settings-service-appsettings)
- [Updater Service (`app.Updater`)](#updater-service-appupdater)
- [OpenCode Config Service (`app.OpenCodeConfig`)](#opencode-config-service-appopencodeconfig)

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

### RegisterOutputCallback
**Service**: App  
**Parameters**: `sessionID (string)`, `id (string)`, `cb (func(data []byte))`  
**Returns**: `void`  
**Description**: 为指定 PTY 会话注册输出回调，供远程桥接层使用。

### UnregisterOutputCallback
**Service**: App  
**Parameters**: `sessionID (string)`, `id (string)`  
**Returns**: `void`  
**Description**: 注销 PTY 输出回调。

### RegisterExitCallback
**Service**: App  
**Parameters**: `sessionID (string)`, `id (string)`, `cb (func(exitCode uint32))`  
**Returns**: `void`  
**Description**: 为指定 PTY 会话注册退出回调。

### UnregisterExitCallback
**Service**: App  
**Parameters**: `sessionID (string)`, `id (string)`  
**Returns**: `void`  
**Description**: 注销 PTY 退出回调。

### RegisterResizeCallback
**Service**: App  
**Parameters**: `sessionID (string)`, `id (string)`, `cb (func(cols, rows int))`  
**Returns**: `void`  
**Description**: 为指定 PTY 会话注册尺寸变化回调。

### UnregisterResizeCallback
**Service**: App  
**Parameters**: `sessionID (string)`, `id (string)`  
**Returns**: `void`  
**Description**: 注销 PTY 尺寸变化回调。

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
**Parameters**: `providerName (string)`, `presetName (string)`, `mode (string)`, `workDir (string)`, `useProxy (bool)`, `shellPath (string)`  
**Returns**: `string`, `error`  
**Description**: 按 provider/preset 启动 Claude Code 会话，返回会话 ID。

### StopSession
**Service**: App  
**Parameters**: `sessionID (string)`  
**Returns**: `error`  
**Description**: 停止指定会话，兼容 PTY 会话和外部启动器会话。

### StopAllSessions
**Service**: App  
**Parameters**: none  
**Returns**: `void`  
**Description**: 停止所有运行中的会话。

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

### BrowseDirectory
**Service**: App  
**Parameters**: none  
**Returns**: `string`, `error`  
**Description**: 打开系统目录选择对话框并返回所选目录。

### QuickLaunch
**Service**: App  
**Parameters**: `providerName (string)`, `presetName (string)`, `useProxy (bool)`  
**Returns**: `error`  
**Description**: 使用兼容接口以终端模式快速启动 Claude 会话。

### SaveAllConfig
**Service**: App  
**Parameters**: none  
**Returns**: `error`  
**Description**: 将配置、密钥、路径、设置和代理规则全部持久化到磁盘。

### GetAppInfo
**Service**: App  
**Parameters**: none  
**Returns**: `map[string]any`  
**Description**: 返回应用版本、配置目录、运行中会话数和代理状态。

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

### GetOutputHistory
**Service**: App  
**Parameters**: `sessionID (string)`  
**Returns**: `[]byte`, `error`  
**Description**: 返回指定 PTY 会话的输出历史。

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
**Description**: 打开保存对话框，将全部 provider、preset 和 API Key 导出到 JSON 文件。

### ImportConfigFromFile
**Service**: App  
**Parameters**: none  
**Returns**: `string`, `error`  
**Description**: 打开文件选择对话框，从导出的 JSON 文件导入 provider 和 Agent Teams 配置。

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

### GetProxyBackendURLHistory
**Service**: App  
**Parameters**: none  
**Returns**: `[]string`  
**Description**: 返回代理后端 URL 历史记录。

### AddProxyBackendURL
**Service**: App  
**Parameters**: `url (string)`  
**Returns**: `error`  
**Description**: 向代理后端 URL 历史添加一项并立即保存。

### RemoveProxyBackendURL
**Service**: App  
**Parameters**: `url (string)`  
**Returns**: `error`  
**Description**: 从代理后端 URL 历史中删除一项并立即保存。

### SetProxyBackendURL
**Service**: App  
**Parameters**: `url (string)`  
**Returns**: `error`  
**Description**: 设置当前代理后端 URL，并将其加入历史记录。

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

### GetPresets
**Service**: Config Service  
**Parameters**: `providerName (string)`  
**Returns**: `map[string]Preset`, `error`  
**Description**: 返回指定 provider 的 preset 列表。

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

### GetZhipuAPIKey
**Service**: Secrets Service  
**Parameters**: none  
**Returns**: `string`  
**Description**: 返回 `zhipu` provider 的 API Key。

### SetZhipuAPIKey
**Service**: Secrets Service  
**Parameters**: `key (string)`  
**Returns**: `error`  
**Description**: 设置 `zhipu` provider 的 API Key。

### GetMinimaxAPIKey
**Service**: Secrets Service  
**Parameters**: none  
**Returns**: `string`  
**Description**: 返回 `minimax_codex` provider 的 API Key。

### SetMinimaxAPIKey
**Service**: Secrets Service  
**Parameters**: `key (string)`  
**Returns**: `error`  
**Description**: 设置 `minimax_codex` provider 的 API Key。

### GetKeyDiagnostics
**Service**: Secrets Service  
**Parameters**: `providerNames ([]string)`  
**Returns**: `map[string]map[string]string`  
**Description**: 返回每个 provider 的密钥来源、掩码值、长度和环境变量诊断信息。

## Proxy Service (`app.Proxy`)

### GetRules
**Service**: Proxy Service  
**Parameters**: none  
**Returns**: `[]InjectionRule`  
**Description**: 返回当前注入规则列表。

### SetRules
**Service**: Proxy Service  
**Parameters**: `rules ([]InjectionRule)`  
**Returns**: `void`  
**Description**: 用给定规则集替换当前规则集。

### AddRule
**Service**: Proxy Service  
**Parameters**: `rule (InjectionRule)`  
**Returns**: `error`  
**Description**: 添加一条注入规则，并在配置目录已知时自动保存。

### UpdateRule
**Service**: Proxy Service  
**Parameters**: `rule (InjectionRule)`  
**Returns**: `error`  
**Description**: 更新一条已存在规则，并在需要时自动保存。

### DeleteRule
**Service**: Proxy Service  
**Parameters**: `id (string)`  
**Returns**: `error`  
**Description**: 删除指定规则并自动保存。

### LoadRules
**Service**: Proxy Service  
**Parameters**: `configDir (string)`  
**Returns**: `error`  
**Description**: 从 `injection-rules.json` 加载规则。

### LoadBackendURLHistory
**Service**: Proxy Service  
**Parameters**: `configDir (string)`  
**Returns**: `error`  
**Description**: 从 `proxy-backend-url-history.json` 加载后端 URL 历史。

### SaveBackendURLHistory
**Service**: Proxy Service  
**Parameters**: `configDir (string)`  
**Returns**: `error`  
**Description**: 将后端 URL 历史保存到磁盘。

### GetBackendURLHistory
**Service**: Proxy Service  
**Parameters**: none  
**Returns**: `[]string`  
**Description**: 返回后端 URL 历史的副本。

### AddBackendURL
**Service**: Proxy Service  
**Parameters**: `url (string)`  
**Returns**: `error`  
**Description**: 将 URL 添加到后端历史，自动去重并限制最多 20 条。

### RemoveBackendURL
**Service**: Proxy Service  
**Parameters**: `url (string)`  
**Returns**: `error`  
**Description**: 从后端 URL 历史中移除指定项。

### SetBackendURL
**Service**: Proxy Service  
**Parameters**: `url (string)`  
**Returns**: `error`  
**Description**: 设置当前后端 URL，并自动写入历史。

### SaveRules
**Service**: Proxy Service  
**Parameters**: `configDir (string)`  
**Returns**: `error`  
**Description**: 将当前规则列表保存到磁盘。

### Start
**Service**: Proxy Service  
**Parameters**: `port (int)`, `backendURL (string)`  
**Returns**: `error`  
**Description**: 启动本地注入代理服务。

### Stop
**Service**: Proxy Service  
**Parameters**: none  
**Returns**: `error`  
**Description**: 停止本地注入代理服务。

### IsRunning
**Service**: Proxy Service  
**Parameters**: none  
**Returns**: `bool`  
**Description**: 返回代理服务是否正在运行。

### GetStatus
**Service**: Proxy Service  
**Parameters**: none  
**Returns**: `ProxyStatus`  
**Description**: 返回代理当前状态，包括运行状态、端口、后端 URL 和规则数量。

### GetLogs
**Service**: Proxy Service  
**Parameters**: none  
**Returns**: `[]InjectionLog`  
**Description**: 返回注入代理的命中日志。

### GetPort
**Service**: Proxy Service  
**Parameters**: none  
**Returns**: `int`  
**Description**: 返回当前代理监听端口。

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

## PTY Service (`app.Pty`)

### RegisterOutputCallback
**Service**: PTY Service  
**Parameters**: `sessionID (string)`, `id (string)`, `cb (func(data []byte))`  
**Returns**: `void`  
**Description**: 注册 PTY 输出回调，供远程层实时转发输出。

### UnregisterOutputCallback
**Service**: PTY Service  
**Parameters**: `sessionID (string)`, `id (string)`  
**Returns**: `void`  
**Description**: 注销 PTY 输出回调。

### RegisterExitCallback
**Service**: PTY Service  
**Parameters**: `sessionID (string)`, `id (string)`, `cb (func(exitCode uint32))`  
**Returns**: `void`  
**Description**: 注册 PTY 退出回调。

### UnregisterExitCallback
**Service**: PTY Service  
**Parameters**: `sessionID (string)`, `id (string)`  
**Returns**: `void`  
**Description**: 注销 PTY 退出回调。

### RegisterResizeCallback
**Service**: PTY Service  
**Parameters**: `sessionID (string)`, `id (string)`, `cb (func(cols, rows int))`  
**Returns**: `void`  
**Description**: 注册 PTY 尺寸变化回调。

### UnregisterResizeCallback
**Service**: PTY Service  
**Parameters**: `sessionID (string)`, `id (string)`  
**Returns**: `void`  
**Description**: 注销 PTY 尺寸变化回调。

### SetContext
**Service**: PTY Service  
**Parameters**: `ctx (context.Context)`  
**Returns**: `void`  
**Description**: 设置 Wails 应用上下文，供事件发射使用。

### Start
**Service**: PTY Service  
**Parameters**: `sessionID (string)`, `shellPath (string)`, `autoCommand (string)`, `workDir (string)`, `env ([]string)`, `cols (int)`, `rows (int)`  
**Returns**: `int`, `error`  
**Description**: 创建一个新的 ConPTY 会话并返回进程 PID。

### Write
**Service**: PTY Service  
**Parameters**: `sessionID (string)`, `data (string)`  
**Returns**: `error`  
**Description**: 向 PTY 写入 base64 编码数据。

### WriteLarge
**Service**: PTY Service  
**Parameters**: `sessionID (string)`, `data (string)`  
**Returns**: `error`  
**Description**: 以分块方式向 PTY 写入大段 base64 数据。

### Resize
**Service**: PTY Service  
**Parameters**: `sessionID (string)`, `cols (int)`, `rows (int)`  
**Returns**: `error`  
**Description**: 调整 PTY 会话尺寸。

### GetPtyDimensions
**Service**: PTY Service  
**Parameters**: `sessionID (string)`  
**Returns**: `cols (int)`, `rows (int)`, `err (error)`  
**Description**: 返回 PTY 当前列数和行数。

### Close
**Service**: PTY Service  
**Parameters**: `sessionID (string)`  
**Returns**: `error`  
**Description**: 关闭指定 PTY 会话。

### CloseAll
**Service**: PTY Service  
**Parameters**: none  
**Returns**: `void`  
**Description**: 关闭全部 PTY 会话。

### IsRunning
**Service**: PTY Service  
**Parameters**: `sessionID (string)`  
**Returns**: `bool`  
**Description**: 检查指定会话是否仍存在。

### GetOutputHistory
**Service**: PTY Service  
**Parameters**: `sessionID (string)`  
**Returns**: `[]byte`, `error`  
**Description**: 返回指定 PTY 会话的输出历史，用于重放。

### RunningCount
**Service**: PTY Service  
**Parameters**: none  
**Returns**: `int`  
**Description**: 返回当前运行中的 PTY 会话数量。

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
