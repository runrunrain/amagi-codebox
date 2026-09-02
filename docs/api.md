# Amagi CodeBox API 参考（Wails 绑定层）

本文档列出前端可调用的全部 Wails 绑定方法。方法清单**以当前代码为准**，生成依据：

- 绑定清单：`bind_list.go` 的 `buildWailsBindList`（共 21 个绑定 = `App` + 20 个服务）
- 方法签名：`app.go`、`headroom_facade.go`、`app_remoteclient.go` 中 `func (a *App) [A-Z]…`，以及 `internal/` 下各服务结构体的导出方法
- 校对日期：2026-08-22（对应版本 1.3.50）

## 调用方式

Wails 把每个绑定对象挂到 `window.go.<包名>.<结构体名>` 命名空间，并自动生成 TypeScript 封装到 `frontend/wailsjs/go/<包名>/`（**自动生成，勿手改**）。前端实际调用应走 `frontend/src/api/*.ts` 的类型化封装模块，而不是直接调用 wailsjs。

| 绑定 | JS 命名空间 | Go 结构体 |
|---|---|---|
| App | `main.App` | `*App`（app.go） |
| Config | `config.ConfigService` | `internal/config` |
| Secrets | `secrets.SecretsService` | `internal/secrets` |
| Paths | `paths.PathsService` | `internal/paths` |
| Log | `logging.Service` | `internal/logging` |
| Settings | `settings.Service` | `internal/settings` |
| Updater | `updater.Service` | `internal/updater` |
| Plugins | `plugin.Service` | `internal/plugin`（Claude Code） |
| CodexPlugins | `codexplugin.Service` | `internal/codexplugin` |
| OpenCodePlugins | `opencodeplugin.Service` | `internal/opencodeplugin` |
| PiPlugins | `piplugin.Service` | `internal/piplugin` |
| OmpPlugins | `ompplugin.Service` | `internal/ompplugin` |
| OpenCodeConfig | `opencodeconfig.Service` | `internal/opencodeconfig` |
| PiConfig | `piconfig.Service` | `internal/piconfig` |
| OmpConfig | `ompconfig.Service` | `internal/ompconfig` |
| AgentProfiles | `agentprofile.Service` | `internal/agentprofile` |
| EnvCheck | `envcheck.Service` | `internal/envcheck` |
| Usage | `usage.Service` | `internal/usage` |
| WebUI | `webui.Service` | `internal/webui` |
| Skins | `skins.Service` | `internal/skins` |
| GitAssist | `gitassist.Service` | `internal/gitassist` |

## 门控说明：pty 与 headroom 不经原始服务绑定

`pty.Service` 与 `headroom.HeadroomService` **被有意排除在 Bind 之外**（见 `bind_list.go` 注释与 `bind_manifest_test.go` 的 T-24 断言）：

- 终端写入/调整尺寸只能走 `App.PtyWrite` / `App.PtyWriteLarge` / `App.PtyResize` 门面；门面在 control-runtime 未就绪时 fail-closed（M-005）。
- Headroom 生命周期只能走 `App.HeadroomStart` / `HeadroomStop` / `HeadroomIsRunning` / `HeadroomGetStatus` / `HeadroomGetPort`（`headroom_facade.go`，lease-guarded）以及 `App.GetHeadroomSavings` / `GetHeadroomPerfByClient` 只读查询。

同样被排除的：`App.StopAllSessions`（仅内部关停路径使用）。

各服务结构体上的 `Load` / `Save` / `Close` / `SetContext` 等方法属于后端内部生命周期钩子，虽然技术上在绑定面内，但前端不应调用；下文以「内部」标注。

## 目录

- [App（`main.App`）](#appmainapp)
- [Config（提供商与预设）](#config提供商与预设)
- [Secrets（密钥存储）](#secrets密钥存储)
- [Paths（工作目录路径簿）](#paths工作目录路径簿)
- [Log（日志）](#log日志)
- [Settings（应用设置）](#settings应用设置)
- [Updater（自动更新）](#updater自动更新)
- [Plugins（Claude Code 插件）](#pluginsclaude-code-插件)
- [CodexPlugins（Codex 插件）](#codexpluginscodex-插件)
- [OpenCodePlugins（OpenCode 插件）](#opencodepluginsopencode-插件)
- [PiPlugins（Pi 扩展包）](#pipluginspi-扩展包)
- [OmpPlugins（omp 插件）](#omppluginsomp-插件)
- [OpenCodeConfig / PiConfig / OmpConfig（各 CLI 配置文件）](#opencodeconfig--piconfig--ompconfig各-cli-配置文件)
- [AgentProfiles（Agent 配置档）](#agentprofilesagent-配置档)
- [EnvCheck（环境检测与安装）](#envcheck环境检测与安装)
- [Usage（用量统计）](#usage用量统计)
- [WebUI（pi Web UI 壳集成）](#webuipi-web-ui-壳集成)
- [Skins（皮肤）](#skins皮肤)
- [GitAssist（AI 辅助 git 提交）](#gitassistai-辅助-git-提交)

---

## App（`main.App`）

`App` 是中央协调器（`app.go`），持有全部服务指针并暴露编排方法。按主题聚类如下。

### 生命周期与内部桥接

- `Startup(ctx)` / `Shutdown(ctx)` — Wails 生命周期钩子。Startup 加载配置、启动托盘与远程服务；Shutdown 保存配置并停止后台组件。前端不直接调用。
- `GetSettingsService() *settings.Service` / `GetPathsService() *paths.PathsService` / `GetConfigService() *config.ConfigService` — 返回服务实例，供远程层内部桥接，不是常规前端入口。

### 应用信息与系统交互

- `GetAppInfo() map[string]any` — 返回 productName、version（ldflags 注入，回退 wails.json `info.productVersion`，再回退 `dev`）、buildTime、gitCommit、goVersion、configDir、runningCount。
- `GetStartupWarnings() []string` — 启动期间积累的警告（前端 onMounted 拉取一次，toast 展示）。
- `GetPlatformCapabilities() platform.PlatformCapabilities` — 启动时一次解析的平台能力（ConPTY/皮肤/远程等支持矩阵）。
- `BrowseDirectory() (string, error)` — 打开系统目录选择对话框，返回所选路径。
- `OpenFileInEditor(filePath string, line int) error` — 用系统默认程序打开文件；`line` 保留兼容但不使用。文件不存在时报错且不创建空文件。
- `SaveClipboardImage(base64Data string) (string, error)` — 把 base64 PNG 写入私有临时文件（0600，大小上限校验）并返回绝对路径，用于截图粘贴进终端的场景。

### 启动补偿债务（launch plan）

- `GetLaunchCompensationDebts() []launchplan.CompensationDebt` — 未确认完成的启动配置补偿债务投影（owner/effect/step/disposition/错误摘要/尝试次数，不含配置内容或 credential）。
- `RetryLaunchCompensationDebt(owner string) error` — 在 5 秒边界内重试一个 exact owner 的补偿；仅 confirmed 删除债务，unavailable/indeterminate 保守保留。

### 外部清理恢复

- `GetExternalCleanupRecoveryStatus() remote.ExternalCleanupRecoveryStatus` — 隐私最小化的外部进程恢复状态（会话 ID、共享服务类型、恢复原因、是否可安全确认；不暴露 PID/命令/环境/路径/provider/终端内容）。
- `ConfirmExternalCleanupRecovery(sessionID string, confirmed bool) (remote.ExternalCleanupRecoveryResult, error)` — 显式确认旧外部终端已关闭；仅 confirmed=true 且 OS 证明进程不存在且 journal exact completion 成功时清理；无 force-clear；每次接受/拒绝都记录 typed audit event。

### 环境检测与安装（委托 EnvCheck）

- `GetEnvCheckStatus() *envcheck.OverallStatus` — 缓存的整体状态。
- `RunEnvCheck() (*envcheck.OverallStatus, error)` — 全量检测五种 CLI + headroom。
- `CheckTool(tool string) (*envcheck.CheckStatus, error)` — 检测单个工具（`claude_code`/`opencode`/`codex`/`pi`/`omp`/`headroom`）。
- `InstallTool(tool string)` / `UpdateTool(tool string)` → `(*envcheck.InstallResult, error)` — 同步安装/更新。
- `StartInstallToolAsync(tool string)` / `StartUpdateToolAsync(tool string)` → `(*envcheck.OperationState, error)` — 异步安装/更新，后台 goroutine 执行，立即返回操作状态。
- `GetEnvCheckOperationState() *envcheck.OperationState` — 当前异步操作状态（id/tool/kind/status/step/progress/result 等）。
- `GetEnvCheckSnapshot() *envcheck.EnvCheckSnapshot` — 检测快照（供远程 API 与前端复用）。
- `RunEnvFixAction(action, tool, extraPath string) (*envcheck.FixActionResult, error)` — 执行白名单修复动作（fix_path/install_tool/install_node/retry/fix_claude_config/install_wsl_search_tools 等，见 `internal/envcheck/fix_dispatcher.go`）。
- `InstallClaudeWithMethod(method string)` / `StartInstallClaudeWithMethodAsync(method string)` — 按方式安装 Claude Code：`npm`（npm 全局）或 `native`（npm 全局后接 `claude install`）；后者为异步版本。
- `CleanClaudeInstall(method string) (*envcheck.InstallResult, error)` — 清理指定安装方式（`npm`/`native`）的 Claude Code 残留。
- `UninstallClaudeCode(method string) (*envcheck.InstallResult, error)` — 卸载已有安装；method 为空时从缓存状态自动识别安装方式。
- `CheckClaudeConfig() (*envcheck.ClaudeConfigStatus, error)` — 扫描 Claude Code 配置文件，报告各配置项存在/缺失。
- `FixClaudeConfig(key, value, filePath string) (*envcheck.ConfigFixResult, error)` — 修复指定 Claude 配置项。

### WSL 安装辅助（Windows）

- `GetWSLCLIStatus() wslsetup.Status` — WSL 内各 CLI 的安装状态。
- `InstallCLIToWSL(tool string) (*wslsetup.InstallResult, error)` — 把指定 CLI（claude/opencode/codex）安装进 WSL 发行版：确保原生 Node 20 + 用户级 npm 前缀，再 `npm i -g` 并校验；幂等。

### Headroom（含 Codex 全局实例）

- `HeadroomStart(realBackendURL string) error` / `HeadroomStop() error` — 启停 Claude 会话级 headroom（8787，Anthropic 目标，lease-guarded 门面）。
- `HeadroomIsRunning() bool` / `HeadroomGetStatus() headroom.HeadroomStatus` / `HeadroomGetPort() int` — 状态查询。
- `GetHeadroomSavings() (*headroom.SavingsReport, error)` — 查询节省报告（共享 ledger，带超时）。
- `GetHeadroomPerfByClient() ([]headroom.ClientPerfStat, error)` — 按 client 聚合的 perf 统计（请求数、平均 cache 命中率、节省 token 等）；headroom 未安装或失败时返回 error，前端显示空态，绝不伪造数据。
- `GetCodexGlobalHeadroom() CodexGlobalHeadroomStatus` — Codex 全局 headroom 开关快照（enabled/target/port + 实时 running）。
- `SetCodexGlobalHeadroom(enabled bool, target string, port int) (CodexGlobalHeadroomStatus, error)` — 切换 Codex 全局 headroom（独立 8788 实例，OpenAI 目标）。开启时启动实例并把 `openai_base_url` 标记块写入 `~/.codex/config.toml`（带备份与幂等）；关闭时先移除标记块再停实例。与会话级 headroom 完全正交。

### 系统显式代理（System Proxy，仅 Windows）

- `GetSystemProxyStatus() SystemProxyStatus` — 全局设备显式代理快照：`supported`（平台能力）/`enabled`/`host`/`port`（系统实时值，Windows Internet Settings）/`reachable`（启用时对端点做 TCP+HTTP 双探测，识别"活端口死代理"）/`configuredHost`/`configuredPort`（持久化端点，下次开启写入）。
- `SetSystemProxyEnabled(enabled bool) (SystemProxyStatus, error)` — 开启/关闭系统级显式代理。开启：端点优先取持久化配置，为空回落系统现有地址（如代理客户端已写入的 ProxyServer），写入 `ProxyServer`+`ProxyEnable=1`（`ProxyOverride` 缺失时补默认回环绕行）；关闭：仅置 `ProxyEnable=0`，保留地址与例外列表。写入后广播 WinINet 刷新（InternetSetOption SETTINGS_CHANGED+REFRESH），已运行应用免重登生效。与 CLI 会话代理注入（SystemProxyEnv）互不影响。

### 远程服务器（Remote Server）

- `GetRemoteToken() string` — 当前 Bearer Token。
- `GetRemoteStatus() map[string]any` — host/port/token/running。
- `GetRemoteWebUIStatus() RemoteWebUIStatusResult` — 桌面入口 Web UI 可打开状态（mobile web root 配置/存在性、内嵌资源可用性、launch URL 等）。
- `OpenRemoteWebUI() (OpenRemoteWebUIResult, error)` — 确保远程服务运行后在默认浏览器打开移动端 Web UI；未运行时先启动并持久化 enabled=true（持久化失败则回滚停止）。
- `RegenerateRemoteToken() string` — 重新生成并返回新 Token。
- `ListRemoteSecurityEvents(limit int) ([]RemoteSecurityEventRecord, error)` — 脱敏后的持久化安全事件（新→旧，不含原始行/路径/secret）。
- `CreateRemotePairingWindow(confirmTerminalExposure bool) (remote.PairingWindowInfo, error)` — 创建 v1 配对窗口（生成配对码）。
- `GetRemotePairingWindow() (remote.PairingWindowStatus, error)` / `CancelRemotePairingWindow(generation uint64) (bool, error)` — 查询/取消配对窗口。
- `ListRemoteDevices() ([]remote.DeviceInfo, error)` — 已配对设备列表。
- `RevokeRemoteDevice(deviceID string, confirm bool) (remote.RevokeDeviceResult, error)` — 吊销设备（需 confirm 确认）。
- `GetRemoteSecurityHealth() remote.SecurityHealthSnapshot` / `AcknowledgeRemoteSecurityHealth(code string)` — 远程安全健康快照 / 按 code 确认告警。
- `ToggleRemoteServer(enabled bool) error` — 启停远程服务器。
- `SetRemotePort(port int) error` / `SetRemoteHost(host string) error` / `SetRemoteEndpoint(host string, port int) error` — 更新端口/主机/端点，需要时自动重启远程服务并持久化。

### Remote Client（作为客户端连接其他 CodeBox 实例，`app_remoteclient.go`）

主机登记簿与健康：

- `RemoteClientListHosts() []remoteclient.HostEntry` — 登记簿全部条目（id/displayName/hostPort/deviceId/health/lastSeen/hasLegacyToken）。
- `RemoteClientAddHost(displayName, hostPort string) (remoteclient.HostEntry, error)` — 登记新宿主（hostPort 白名单校验后规范化）。
- `RemoteClientUpdateHost(hostID, hostPort string) error` / `RemoteClientRenameHost(hostID, displayName string) error` / `RemoteClientRemoveHost(hostID string) error` — 更新地址 / 改名 / 移除。
- `RemoteClientProbeHost(hostPort string) (remoteclient.ProbeResult, error)` — 探活：GET `host/summary`；200 或契约错误体 → reachable（其中 `auth.revoked` → revoked），网络层失败 → unreachable。

配对与连接：

- `RemoteClientCompletePairing(hostPort, code string) (*remoteclient.PairingResult, error)` — 完整配对流：凭据入 Keychain、登记簿回填；失败零残留。
- `RemoteClientConnect(hostID string) (RemoteClientConnectResult, error)` — 连接已配对宿主（从 Keychain 恢复凭据 → 鉴权验证 host/summary 通过才建立连接；单连接模型，顶替既有连接）。
- `RemoteClientDisconnect(hostID string) error` — 断开。

远端会话域（v1 契约）：

- `RemoteClientListRemoteSessions() (contract.SessionList, error)` / `RemoteClientGetRemoteSession(sessionID string) (contract.SessionDetail, error)` — 远端会话列表/详情。
- `RemoteClientLaunchRemoteSession(cliType, workdir, providerRef, presetRef, modelRef, shellRef string, useHeadroom bool) (contract.SessionDetail, error)` — 在远端启动会话。
- `RemoteClientStopRemoteSession(sessionID string)` / `RemoteClientRestartRemoteSession(sessionID string)` → `(contract.SessionDetail, error)` — 停止/重启远端会话。
- `RemoteClientDeleteRemoteSession(sessionID string) error` — 删除远端会话记录。

legacy 配置面（过渡方案）：

- `RemoteClientSetLegacyToken(hostID, token string) error` / `RemoteClientClearLegacyToken(hostID string) error` — 写入/清除 legacy Bearer token（Keychain 条目，token 本体永不入登记簿）。
- `RemoteClientListRemoteProviders(hostID string) ([]remoteclient.LegacyProviderSummary, error)` / `RemoteClientGetRemoteProvider(hostID, name string) (string, error)` / `RemoteClientPutRemoteProvider(hostID, name, providerJSON string) error` — 远端提供商列表/读取/写入（legacy API）。
- `RemoteClientGetRemoteSettings(hostID string) (string, error)` / `RemoteClientPutRemoteSettings(hostID, settingsJSON string) error` — 远端设置读取/写入（legacy API）。

远端终端与控制权：

- `RemoteClientTerminalAttach(sessionID string) (RemoteClientTerminalAttachResult, error)` — 启动（或复用）远端会话的 `/ws/v1` 终端连接；output/backfill/input.ack 经事件总线回流；幂等。
- `RemoteClientTerminalDetach(sessionID string) error` — 断开终端连接。
- `RemoteClientTerminalSendInput(sessionID, data string) error` / `RemoteClientTerminalResize(sessionID string, cols, rows int) error` — 发送输入 / 调整尺寸。
- `RemoteClientAcquireControl(sessionID string) (remoteclient.ControlView, error)` / `RemoteClientReleaseControl(sessionID string)` — 获取/释放远端会话控制权（ControlView 为服务端快照的只读投影，客户端不得本地推导）。

### 会话启动与管理

五种 AppType（`internal/session/types.go`）：`claudecode` / `opencode` / `codex` / `pi` / `omp`。启动模式 `terminal`（独立终端窗口）或 `embedded`（内嵌 ConPTY/PTY + xterm.js）。

- `LaunchSession(providerName, presetName, mode, workDir string, useHeadroom bool, shellPath string) (string, error)` — 启动 Claude Code 会话：解析 provider/preset，可选启用 headroom，返回会话 ID。
- `LaunchCodexSession(modelName, providerID, mode, workDir, shellPath string) (string, error)` — 启动 Codex 会话。
- `LaunchPiSession(modelName, providerID, mode, workDir, shellPath string) (string, error)` — 启动 Pi 会话（写配置到 `~/.pi/agent`）。
- `LaunchOmpSession(modelName, providerID, mode, workDir, shellPath string) (string, error)` — 启动 omp（Oh My Pi）会话（写配置到 `~/.omp/agent`）。
- `LaunchOpenCode(providerName, presetName, mode, workDir, shellPath string) (string, error)` — 启动 OpenCode 会话；双轨兼容：优先 `opencode_presets`（新模型），回退 `terminal_presets.opencode`（旧模型）。
- `QuickLaunch(providerName, presetName string, useHeadroom bool) error` — 快捷启动 Claude Code：等价于 `LaunchSession(provider, preset, "terminal", "", useHeadroom, "")`。
- `GetSession(sessionID string) (session.SessionInfo, error)` / `GetSessions() []session.SessionInfo` — 单个/全部会话摘要（id/appType/provider/preset/model/mode/workDir/status/pid/startedAt/duration/title/claudeSessionId）。
- `StopSession(sessionID string) error` — 停止会话。
- `RemoveSession(sessionID string) error` — 移除已停止会话的记录。
- `ClearStoppedSessions() int` — 清理全部已停止会话，返回清理数量。
- `ClearStoppedSessionsDetailed() ClearStoppedSessionsResult` — 详细版：cleared/clearedIds/retainedIds/failed 逐项返回。

### PTY 门面（fail-closed）

- `PtyWrite(sessionID, data string) error` — 向内嵌终端写入 base64 数据；control-runtime 未就绪返回 `remote.ErrControlNotReady`。
- `PtyWriteLarge(sessionID, data string) error` — 长文本粘贴：内部拆 1KB 小块逐步写入，避免 ConPTY 缓冲区溢出截断。
- `PtyResize(sessionID string, cols, rows int) error` — 调整内嵌终端尺寸。
- `GetOutputHistorySnapshot(sessionID string) (string, error)` — 输出历史快照（base64 data + seq 的 JSON；control 就绪时经 Projector 格式化）。
- `GetPtyDimensions(sessionID string) (cols, rows int, err error)` — 当前 PTY 尺寸（亦实现 remote.DimensionsProvider 接口）。

### 日志（委托 Log 服务）

- `GetLogs(level, source, keyword string, limit int) []logging.Entry` / `GetLogSources() []string` / `GetLogFiles() []string` / `GetLogFileContent(filename string) (string, error)` / `ClearLogs()` / `ExportLogs() (string, error)` — 与 Log 服务同名方法一一对应。

### 自动更新（委托 Updater）

- `CheckForUpdate() (*updater.UpdateInfo, error)` — 查询 GitHub latest release。
- `DownloadAndApplyUpdate() error` — 下载并应用更新，进度经 Wails 事件 `update:progress`（downloaded/total）推送。
- `GetGitHubToken() string` / `SetGitHubToken(token string) error` — GitHub Token 读写（走 Settings 持久化）。

### 提供商与预设直通

- `GetProvidersByType(providerType string) map[string]config.Provider` — 按类型过滤：`openai`（IsOpenAICompatible）/ `anthropic`（IsAnthropicCompatible）。
- `GetProviderExportJSON(providerName string) (string, error)` — 导出单个 provider 为 JSON。
- `SaveProviderFromJSON(providerName, jsonStr string) error` — 从 JSON 保存 provider。
- `UpdateProvider(oldName, newName, providerJSON string) error` — 更新（可改名）provider。
- `DeleteProvider(name string) error` — 删除 provider（含关联清理）。
- `GetUrlHistory(providerID string)` / `AddUrlToHistory(providerID, url string)` / `RemoveUrlFromHistory(providerID, url string)` — Base URL 历史记录。
- `GetTerminalPresets(terminalType string)` / `SaveTerminalPreset(terminalType, presetName string, preset config.TerminalPreset)` / `DeleteTerminalPreset(terminalType, presetName string)` — 终端预设 CRUD（含 vision/video/vision_priority 视觉能力标记，见 `docs/vision-export-contract.md`）。
- `MigrateProviderPresetsToTerminal() (int, error)` — 旧 provider 内嵌预设迁移到终端预设桶。
- `GetMergedTerminalPresets(terminalType string) ([]config.MergedTerminalPreset, error)` — 合并视图（terminal_preset + provider_preset 两来源，统一 key/label）。
- `ResolveTerminalPreset(terminalType, key string) (string, string, string, bool)` — 按 key 解析预设，返回（来源类型、provider、预设名、是否命中）。
- `SetPluginSubItemEnabled(pluginId, subItemType, subItemId string, enabled bool) error` — 插件子项开关的统一入口：查 Claude 已安装注册表判断归属（`~/.claude/plugins/installed_plugins.json`），命中派 Claude 引擎，否则派 Codex 引擎。

### 配置导入导出与保存

- `SaveAllConfig() error` — 保存配置与密钥到磁盘（仅在对应文件曾成功加载时才写，避免覆盖损坏现场）。
- `ExportConfigToFile() (string, error)` — 弹出保存对话框，把全部可移植配置合并导出为 JSON（原子写入，仅当前用户可读）；用户取消返回 `("", nil)`。
- `ImportConfigFromFile() (string, error)` — 弹出文件选择对话框导入；v2 文件完整快照替换语义，v1 按旧协议兼容导入。

### 环境变量（`envvars.json`）

- `GetEnvVars() ([]envvars.EnvVar, error)` / `SetEnvVar(key, value string) error` / `DeleteEnvVar(key string) error` — 环境变量 CRUD。
- `ImportEnvVars(jsonStr string) error` / `ExportEnvVars() (string, error)` — JSON 字符串导入/导出。
- `GetEnvVarsJSON() (string, error)` / `SaveEnvVarsJSON(jsonStr string) error` — 原文读写。
- `ExportEnvVarsToFile() error` / `ImportEnvVarsFromFile() error` — 经系统对话框导出/导入文件。
- `GetEnvVarsGlobalSyncStatus() envvars.GlobalSyncStatus` / `SetEnvVarsGlobalSyncEnabled(enabled bool) (envvars.GlobalSyncStatus, error)` — 全局同步开关状态/设置。

### 各 CLI 配置文件直通（委托对应 Config 服务）

- OpenCode：`GetOpenCodeConfig()` / `SaveOpenCodeConfig(content)` / `GetOpenCodeConfigPath()`
- Pi：`GetAmagiConfig()` / `SaveAmagiConfig(content)` / `GetAmagiConfigPath()`；`GetPiModelsConfig()` / `SavePiModelsConfig(content)` / `GetPiModelsConfigPath()`；`GetPiAuthConfig()` / `SavePiAuthConfig(content)` / `GetPiAuthConfigPath()`；`GetPiModelCatalog()`
- omp：`GetOmpConfig()` / `SaveOmpConfig(content)` / `GetOmpConfigPath()`；`GetOmpModelsConfig()` / `SaveOmpModelsConfig(content)` / `GetOmpModelsConfigPath()`；`GetOmpModelCatalog()`

语义与各服务同名方法一致，见下文对应服务章节。

### 常用工作目录（委托 Settings）

- `GetSavedWorkDirs() ([]settings.WorkDirEntry, error)` / `AddSavedWorkDir(path, label string) ([]settings.WorkDirEntry, error)` / `RemoveSavedWorkDir(path string) ([]settings.WorkDirEntry, error)` — 收藏工作目录列表（返回更新后的全量列表）。

### 诊断

- `GetKeyDiagnostics() map[string]map[string]string` — 全部 provider 的密钥来源诊断（stored/env/missing 等）。

---

## Config（提供商与预设）

`config.ConfigService`（`internal/config/service.go`），持久化到 `~/.amagi-codebox/models.json`。核心类型：`Provider`（双格式 `anthropic`/`openai` 字段 + defaultModel + urlHistory；`type`/`base_url`/`auth_key`/`presets` 为废弃兼容字段）、`TerminalPreset`（含 `vision`/`video`/`vision_priority` 视觉标记）、`OpenCodePreset`（一份完整 opencode.json + bindings）、`AgentTeamsConfig`。

- `Load() error` / `Save() error` — 内部：加载/落盘。
- `SetAPIKeyResolver(fn func(provider string) string)` — 内部：注入密钥解析回调。
- `GetConfig() *AppConfig` — 总配置（models/agent_teams/terminal_presets/opencode_presets/version）。
- `GetProviders() map[string]Provider` / `GetProviderNames() []string` / `GetProvider(name string) (*Provider, error)` — provider 读取。
- `SnapshotProvider(name string) (*Provider, error)` — 单个 provider 快照副本。
- `ReplaceProviders(providers map[string]Provider) error` — 全量替换（导入用）。
- `SaveProvider(name string, p Provider) error` / `DeleteProvider(name string) error` / `RenameProvider(oldName, newName string) error` — provider 写操作。
- `GetPresets(providerName string) (map[string]Preset, error)` / `SavePreset(providerName, presetName string, p Preset) error` / `DeletePreset(providerName, presetName string) error` — 旧模型 provider 内嵌预设（target：codex/opencode/pi）CRUD。
- `GetTerminalPresets(terminalType string) (map[string]TerminalPreset, error)` / `SaveTerminalPreset(...)` / `DeleteTerminalPreset(terminalType, presetName string) error` — 终端预设 CRUD（terminalType 为桶名，如 anthropic/openai/opencode 等）。
- `GetAllTerminalPresets() *TerminalPresetsConfig` / `SetAllTerminalPresets(tp *TerminalPresetsConfig) error` — 全桶读取/整体替换。
- `MigrateProviderPresetsToTerminal() (int, bool, error)` — 迁移 provider 内嵌预设到终端预设桶，返回（迁移数, 是否有变更, 错误）。
- `GetMergedTerminalPresets(terminalType string) ([]MergedTerminalPreset, error)` — 合并视图（含来源标记与视觉标记）。
- `ResolveTerminalPreset(terminalType, key string) (string, *TerminalPreset, error)` — 按 key 解析到具体预设。
- `GetOpenCodePresets() map[string]OpenCodePreset` / `GetAllOpenCodePresets() map[string]OpenCodePreset` / `GetOpenCodePreset(key string) (*OpenCodePreset, error)` / `SaveOpenCodePreset(key string, preset OpenCodePreset) error` / `DeleteOpenCodePreset(key string) error` — OpenCode 预设（新模型）CRUD。
- `ReplaceImportedPresetSnapshots(terminal *TerminalPresetsConfig, openCode map[string]OpenCodePreset, hasExplicitOpenCodeSnapshot bool) error` — 导入时替换预设快照。
- `GetAgentTeams() AgentTeamsConfig` / `SetAgentTeams(config AgentTeamsConfig) error` — Agent Teams 开关（enabled/teammate_mode）。
- `GetUrlHistory(providerID string) ([]string, error)` / `AddUrlToHistory(providerID, url string) error` / `RemoveUrlFromHistory(providerID, url string) error` — URL 历史。

## Secrets（密钥存储）

`secrets.SecretsService`（`internal/secrets/service.go`），持久化到 `~/.amagi-codebox/secrets.enc`，平台保护：Windows DPAPI（`store_windows.go`）/ macOS Keychain（`store_darwin_*.go`）/ 其他平台为 unsupported no-op（Load 返回空、Save 静默丢弃，**无明文回退**）。

- `Load() error` / `Save() error` — 内部。
- `GetAPIKey(provider string) (string, error)` / `SetAPIKey(provider, apiKey string) error` / `DeleteAPIKey(provider string) error` / `HasAPIKey(provider string) bool` — 密钥 CRUD。
- `GetAllProviders() []string` — 已存密钥的 provider 名列表。
- `GetAPIKeyWithFallback(provider string) (string, string)` — 先查存储再查环境变量，返回 (apiKey, source)；source 为 `"stored"` 或命中的环境变量名，未命中返回 `("", "")`。
- `Snapshot() (map[string]string, error)` / `ReplaceAll(next map[string]string) error` — 全量快照/替换（导入导出用）。
- `GetKeyDiagnostics(providerNames []string) map[string]map[string]string` — 每个 provider 的密钥来源诊断。

## Paths（工作目录路径簿）

`paths.PathsService`（`internal/paths/service.go`），持久化到 `paths.json`。

- `Load() error` / `Save() error` — 内部。
- `GetPaths() []PathEntry` / `GetDefaultPath() string` / `GetConfig() PathsConfig` — 读取。
- `ReplaceConfig(next PathsConfig) error` — 整体替换。
- `SetDefaultPath(path string) error` / `AddPath(entry PathEntry) error` / `RemovePath(path string) error` / `UpdateLabel(path, label string) error` — 写操作。
- `ValidatePath(path string) bool` — 校验目录是否存在。
- `ListDirectories(root string) (string, error)` — 列出 root 下一层子目录（仅目录、跳过 `.` 开头、大小写不敏感排序、上限 500 条超限截断），返回 JSON `"{"root","parent","dirs":[{"name","path"}],"truncated"}"`；root 为空回退用户主目录，文件系统根的 parent 为 JSON `null`。

## Log（日志）

`logging.Service`（`internal/logging/service.go`），内存环形缓冲 + `~/.amagi-codebox/logs/` 文件。

- `Debug(source, message string, detail ...string)` / `Info(...)` / `Warn(...)` / `Error(...)` — 写入日志（主要供后端与其他绑定面调用）。
- `GetEntries(level, source, keyword string, limit int) []Entry` — 过滤查询。
- `GetSources() []string` — 已知 source 列表。
- `GetLogFiles() []string` / `GetLogFileContent(filename string) (string, error)` — 日志文件列表/内容。
- `ClearEntries()` — 清空内存缓冲。
- `ExportJSON() (string, error)` — 导出为 JSON。
- `Close()` — 内部：关闭文件句柄。

## Settings（应用设置）

`settings.Service`（`internal/settings/service.go`），持久化到 `settings.json`。

- `Load() error` / `Save() error` — 内部。
- `GetSettings() *AppSettings` / `ReplaceSettings(next AppSettings) error` — 全量读/替换。
- `GetDashboardDefaults() DashboardDefaults` / `SetDashboardDefaults(d DashboardDefaults) error` — 仪表盘默认启动项（各 CLI 的 provider/preset/mode/shell/useHeadroom/codexGlobalHeadroom 等）。
- `GetCodexGlobalHeadroom() CodexGlobalHeadroomState` / `SetCodexGlobalHeadroom(enabled bool, target string, port int) error` — Codex 全局 headroom 持久化状态。
- `GetShellPaths() []ShellEntry` / `AddShellPath(entry ShellEntry) error` / `RemoveShellPath(path string) error` — 自定义 shell 路径。
- `GetTerminalSettings() TerminalSettings` / `SetTerminalSettings(t TerminalSettings) error` — 终端设置。
- `GetSkinSettings() SkinSettings` / `SetSkinSettings(sk SkinSettings) error` — 皮肤设置（enabled/imageId/dim/blur/opacity/textBoost；数值 clamp 在本层）。
- `GetSystemProxyEndpoint() SystemProxySettings` / `SetSystemProxyEndpoint(host string, port int) error` — 系统显式代理端点持久化（host/port；Set 走 NormalizeProxyEndpoint 校验，事务式保存失败回滚）。
- `GetRemoteEnabled() bool` / `SetRemoteEnabled(enabled bool) error` — 远程服务开关持久化。
- `GetRemoteHost() string` / `SetRemoteHost(host string) error` / `GetRemotePort() int` / `SetRemotePort(port int) error` / `SetRemoteEndpoint(host string, port int) error` — 远程端点。
- `GetMobileWebRoot() string` / `SetMobileWebRoot(path string) error` — 移动端 Web 资源目录。
- `GetGitHubToken() string` / `SetGitHubToken(token string) error` — GitHub Token。
- `GetCommitSummaryPreset() string` / `SetCommitSummaryPreset(v string) error` — GitAssist 提交总结模型引用（`provider/preset` 格式）。
- `GetSavedWorkDirs() []WorkDirEntry` / `AddSavedWorkDir(path, label string) error` / `RemoveSavedWorkDir(path string) error` — 收藏工作目录。
- `GetRemoteLaunchDefaultsV1() map[string]RemoteLaunchDefaultV1` / `RecordRemoteLaunchDefaultV1(cliType string, refs RemoteLaunchDefaultV1) error` — v1 远程启动默认引用（providerRef/presetRef/modelRef/shellRef/useHeadroom），按 CLI 类型记录一次成功激活的启动。

## Updater（自动更新）

`updater.Service`（`internal/updater/service.go`），基于 GitHub Releases。

- `SetToken(token string)` — 内部：注入 GitHub Token。
- `CheckForUpdate() (*UpdateInfo, error)` — 请求 `repos/<owner>/<repo>/releases/latest`，与当前版本比较生成 UpdateInfo。
- `DownloadAndApply(onProgress func(downloaded, total int64)) error` — 下载并自替换应用更新，进度经回调上报（App 层转发为 `update:progress` 事件）。
- `CleanupOldBinary()` — 内部：清理旧二进制。

## Plugins（Claude Code 插件）

`plugin.Service`（`internal/plugin/`），包装 `claude plugin` CLI 并解析 `~/.claude/plugins/` 注册表。`CommandResult` 为命令执行结果（success/stdout/stderr 等）。

市场与插件：

- `GetMarketplaces() ([]Marketplace, error)` — 已添加市场列表（name/source/repo/url/installLocation/lastUpdated/autoUpdate）。
- `AddMarketplace(source string)` / `RemoveMarketplace(name string)` / `UpdateMarketplace(name string)` → `(*CommandResult, error)` — 市场增删/刷新索引。
- `GetInstalledPlugins() ([]InstalledPlugin, error)` — 已安装插件（id 为 `name@marketplace` 格式，含 version/scope/enabled/installPath/gitCommitSha 等）。
- `GetAvailablePlugins() ([]interface{}, error)` — 全部市场可安装插件聚合。
- `GetPluginDetail(pluginID string) (*PluginDetail, error)` — 插件详情（含 pluginType 与 subItems）。
- `InstallPlugin(pluginName string)` / `UninstallPlugin(pluginID string)` / `EnablePlugin(pluginID string)` / `DisablePlugin(pluginID string)` → `(*CommandResult, error)` — 安装（`--scope user`）/卸载/启用/禁用。
- `UpdatePlugin(pluginID string) (*CommandResult, error)` — 更新前先刷新所属市场索引（失败仅告警继续）。
- `RefreshPlugins() error` — 刷新缓存。

子项（skill/hook/command/agent/mcp）：

- `AnalyzePluginType(pluginID string) (PluginType, error)` — 分析插件类型。
- `GetPluginSubItems(pluginID string) ([]SubItem, error)` — 子项清单。
- `GetPluginSubItemStates(pluginID string) (PluginSubItemState, error)` — 子项启用状态。
- `SetSubItemEnabled(pluginID string, subItemRef SubItemRef, enabled bool) error` / `SetPluginSubItemEnabled(pluginId, subItemType, subItemId string, enabled bool) error` — 子项开关（后者为扁平参数版）。

## CodexPlugins（Codex 插件）

`codexplugin.Service`（`internal/codexplugin/service.go`）。

- `ListMarketplaces() ([]CodexMarketplace, error)` / `AddMarketplace(req AddMarketplaceRequest)` / `UpgradeMarketplace(name string)` / `RemoveMarketplace(name string)` — 市场管理。
- `ListPlugins(marketplace string) ([]CodexPlugin, error)` — 指定市场（或全部）插件列表。
- `GetPluginDetails(selector PluginSelector) (*CodexPluginDetail, error)` — 详情。
- `ListAvailablePlugins() ([]CodexAvailablePlugin, error)` — 可安装聚合。
- `RefreshPlugins() (*CodexPluginsData, error)` — 刷新并返回全量数据。
- `InstallPlugin(selector PluginSelector)` / `UninstallPlugin(selector PluginSelector)` / `SetPluginEnabled(selector PluginSelector, enabled bool)` → `(*CommandResult, error)` — 安装/卸载/启停。
- `SetPluginSubItemEnabled(pluginId, subItemType, subItemId string, enabled bool) error` — 子项开关（当前实现仅记日志，见 `app.go` SetPluginSubItemEnabled 注释）。

## OpenCodePlugins（OpenCode 插件）

`opencodeplugin.Service`（`internal/opencodeplugin/service.go`）。

- `ListInstalledPlugins() ([]Plugin, error)` / `RefreshPlugins() (*PluginsData, error)` / `GetPluginDetails(spec string) (*PluginDetail, error)` — 列表/刷新/详情。
- `InstallPlugin(spec string)` / `UpdatePlugin(spec string)` / `UninstallPlugin(spec string)` → `(*CommandResult, error)` — 安装/更新（含 stable 版本比较）/卸载。

## PiPlugins（Pi 扩展包）

`piplugin.Service`（`internal/piplugin/service.go`），管理 `~/.pi/agent/settings.json` 的用户级 `packages[]`（source 形如 `npm:@foo/bar@1.0.0` / `git:github.com/u/r@v1` / 本地绝对路径）。

- `ListInstalledPackages() ([]Package, error)` / `RefreshPackages() (*PackagesData, error)` / `GetPackageDetails(source string) (*PackageDetail, error)` — 列表/刷新/详情。
- `InstallPackage(source string)` / `RemovePackage(source string)` / `UpdatePackage(source string)` → `(*CommandResult, error)` — 安装/移除/更新（pinned 精确版本或 git ref 不会被 update 移动）。
- `SwitchPackageSource(oldSource, newSource string) (*CommandResult, error)` — 原子切换包来源（如 git 发布版 ↔ 本地工作区路径）：校验 old 已登记 → remove（实体保留）→ install new → 失败回滚重装 old；同源为 no-op 成功。

## OmpPlugins（omp 插件）

`ompplugin.Service`（`internal/ompplugin/service.go`）。

- `ListPlugins() ([]Plugin, error)` / `RefreshPlugins() (*PluginsData, error)` — 列表/刷新。
- `InstallPlugin(spec string)` / `UninstallPlugin(name string)` / `SetPluginEnabled(name string, enabled bool)` / `UpgradePlugin(name string)` → `(*CommandResult, error)` — 安装/卸载/启停/升级。

## OpenCodeConfig / PiConfig / OmpConfig（各 CLI 配置文件）

三个服务同构：读取/保存对应 CLI 自己的配置文件原文，并返回路径供前端展示。

**OpenCodeConfig**（`internal/opencodeconfig/service.go`，opencode.json）：

- `GetOpenCodeConfig() (string, error)` / `SaveOpenCodeConfig(content string) error` / `GetOpenCodeConfigPath() (string, error)` — 原文读写/路径。
- `SyncManagedProviders(managed map[string]any) error` — 把托管 provider 段同步进 opencode.json（读-改-写，保留未知字段；文件缺失时新建）。

**PiConfig**（`internal/piconfig/service.go`，`~/.pi/agent/`）：

- `GetAmagiConfig()` / `SaveAmagiConfig(content)` / `GetAmagiConfigPath()` — amagi.json 读写/路径。
- `GetModelsConfig()` / `SaveModelsConfig(content)` / `GetModelsConfigPath()` — models.json 读写/路径。
- `GetAuthConfig()` / `SaveAuthConfig(content)` / `GetAuthConfigPath()` — auth 配置读写/路径。
- `GetPiModelCatalog() (string, error)` — 从 models.json 抽取 provider→models 目录（只读、不含 apiKey 等敏感信息），文件缺失返回空目录。

**OmpConfig**（`internal/ompconfig/service.go`，`~/.omp/agent/`）：

- `GetOmpConfig()` / `SaveOmpConfig(content)` / `GetOmpConfigPath()` — config.yml 读写/路径。
- `GetModelsConfig()` / `SaveModelsConfig(content)` / `GetModelsConfigPath()` — models.yml 读写/路径。
- `GetOmpModelCatalog() (string, error)` — models.yml 抽取的目录 + `omp models ls --json` 内置目录合并（自定义条目优先；文件缺失返回仅内置）。

## AgentProfiles（Agent 配置档）

`agentprofile.Service`（`internal/agentprofile/service.go`），持久化到 `agent-profiles.json`；把 pi 的 `amagi.json` 与 omp 的 `config.yml` 打包为命名配置档，可一键切换。

- `ListAgentProfiles() (string, error)` — 存储全文 JSON（前端解析展示）；文件缺失返回空骨架。
- `GetAgentProfile(name string) (string, error)` — 单个配置档 JSON（`{pi, omp, updatedAt}`，预览用）。
- `CaptureAgentProfile(name string) error` — 把当前 live 配置快照为命名档（存在则覆盖）；pi 的 amagi.json 必须可读，omp 的 config.yml 缺失记空串。
- `SaveAgentProfile(name, piContent, ompContent string) error` — 显式内容保存；pi 侧非空必须是合法 JSON，omp 侧非空必须是根为映射的合法 YAML，非法报错不写。
- `ApplyAgentProfile(name string) error` — 应用配置档到 live 配置（应用前备份，仅保留一份；成功后更新 lastApplied；内容非法不写任何文件）。
- `DeleteAgentProfile(name string) error` — 删除；删除的是 lastApplied 时同步清空。

## EnvCheck（环境检测与安装）

`envcheck.Service`（`internal/envcheck/`）。工具枚举：`claude_code`/`opencode`/`codex`/`pi`/`omp`/`headroom`；安装方式：`native`/`npm`/`pip`/`homebrew`/`codebox-venv`/`unknown`。

- `CheckAll() (*OverallStatus, error)` — 全量检测（含 Checking 进行中标记）。
- `CheckOne(tool CLITool) (*CheckStatus, error)` — 单工具检测（installed/installMethod/version/hasUpdate/pathOk/systemPathOk/pathState/issues/solutions/canInstall/canInstallByMethod 等）。
- `CheckLatestVersion(tool CLITool) (string, error)` — 查询最新版本号。
- `GetCachedStatus() *OverallStatus` — 缓存结果。
- `Install(tool CLITool)` / `Update(tool CLITool)` → `(*InstallResult, error)` — 同步安装/更新。
- `StartInstallTool(tool CLITool)` / `StartUpdateTool(tool CLITool)` / `StartInstallClaudeCodeWithMethod(method ClaudeInstallMethod)` → `(*OperationState, error)` — 异步启动（后台 goroutine）。
- `GetOperationState() *OperationState` / `GetEnvCheckSnapshot() *EnvCheckSnapshot` — 异步操作状态/检测快照。
- `InstallClaudeCodeWithMethod(method ClaudeInstallMethod) (*InstallResult, error)` — 按方式同步安装 Claude Code（`npm`/`native`）。
- `CleanClaudeCode(method InstallMethod) (*InstallResult, error)` / `CleanHeadroom() (*InstallResult, error)` — 清理安装残留。
- `CheckClaudeConfig() (*ClaudeConfigStatus, error)` / `FixClaudeConfig(req ConfigFixRequest) (*ConfigFixResult, error)` — Claude 配置检测/修复。
- `RunFixAction(req FixActionRequest) (*FixActionResult, error)` — 白名单修复动作分发（action/tool/extraPath/method/key/value/filePath；结果含 changed/requiresRestart/nextSteps 等）。
- `SetHeadroomVenvDir(dir string)` / `SetHeadroomStopper(fn func() (error, func()))` — 内部：注入 headroom venv 目录与停止回调。

## Usage（用量统计）

`usage.Service`（`internal/usage/`，SQLite `usage.db` + `usage-pricing.json`）。金额均为 micro-currency（整数）；主展示币种 USD，CNY 按固定汇率折算。

查询 API（`api.go`）：

- `GetUsageSummary(filter SummaryFilter) (Summary, error)` — 仪表盘汇总（请求数、输入/输出/cache token、分币种成本、实际日期范围）。filter：startDate/endDate（UTC 闭区间）、appType、source、provider。
- `GetDailyTrends(filter TrendFilter) ([]DailyTrendPoint, error)` — 日趋势（granularity day/week；days 与起止日期互斥，默认 30 天）。
- `GetModelDailyTrends(filter TrendFilter) ([]ModelDailyTrendPoint, error)` — 按模型×日不聚合的趋势点（每模型一条线）。
- `GetModelStats(filter StatFilter) ([]ModelStat, error)` — 模型维度聚合（含 cacheHitRate、cacheAdjustedTokens、分项成本、cacheHitSavings、hasPrice）。
- `GetProviderStats(filter StatFilter) ([]ProviderStat, error)` — 供应商维度聚合。
- `GetRequestLogs(filter LogFilter) ([]UsageRecord, error)` — 明细日志（model 过滤 + 分页，pageSize 默认 50 上限 500）。
- `GetUnknownModels() ([]UnknownModel, error)` — 未匹配价格表的模型清单。

同步：

- `SyncSessionUsage() (SyncResult, error)` — 「立即同步」：扫描会话日志入库，返回本轮 recordsAdded/processedCount/filesScanned/errors（持锁执行，不受后台轮次覆盖影响）。
- `GetSyncState() []SyncState` — 各来源同步游标。
- `SyncAll() error` / `StartBackgroundSync(interval time.Duration)` — 内部：全量同步/后台周期同步。

价格表：

- `GetModelPricing() []ModelPricing` / `UpsertModelPricing(mp ModelPricing) error` / `DeleteModelPricing(id string) error` / `ResetModelPricing() error` — 价格表读取/增改/删除/重置内置。

内部写入路径（被绑定但主要由后端/远程层调用）：

- `Record(evt UsageEvent) (bool, error)` — 单条事件入库（模型归一化、cache 语义、价格查询、成本计算、dedup_key、INSERT OR IGNORE；返回是否真正新增）。
- `RecordForce(evt UsageEvent) error` — INSERT OR REPLACE（累计语义，如 OpenCode 同 session 更新）。
- `Pricing() *PricingService` — 内部：价格子服务访问器。
- `Load() error` / `Close() error` — 内部：打开/关闭数据库。

## WebUI（pi Web UI 壳集成）

`webui.Service`（`internal/webui/service.go`），按契约发现 embedded pi 会话的 Web UI（AMAGI_WEBUI_PORT/TOKEN 注入 + `~/.pi/agent/amagi/webui-registry` 注册表回退）。

- `GetWebUIStatus(sessionID string) Status` — 状态快照（state/url/port；url 仅 available 时非空，token 走 fragment 不入日志）。
- `ProbeWebUI(sessionID string) Status` — 主动探测一次并返回状态。
- `OpenWebPlane(sessionID string) (string, error)` — 返回可打开的 Web UI URL；未 available 先探测，仍不可用或已结束则报错。
- `RegisterSession(sessionID string, pid, injectedPort int, token string)` — 内部：LaunchPiSession 在 PTY 启动成功后注册。
- `Invalidate(sessionID string)` — 内部：会话退出时落为 ended。
- `RemoveSession(sessionID string)` — 内部：彻底移除 tracker。

## Skins（皮肤）

`skins.Service`（`internal/skins/service.go`），图片库目录 `~/.amagi-codebox/skins/`；皮肤设置持久化委托 Settings。

- `PickSkinImage() (*Skin, error)` — 弹出文件对话框选择并导入图片（依赖 Startup 注入的 ctx）。
- `ImportSkinImage(path string) (*Skin, error)` — 从指定路径导入。
- `ListSkins() ([]Skin, error)` — 皮肤列表（id/fileName/url/bytes/宽高/importedAt）。
- `RemoveSkin(id string) error` — 删除。
- `GetSkinSettings() settings.SkinSettings` / `SetSkinSettings(sk settings.SkinSettings) error` — 皮肤设置读写；Enabled 时校验 ImageID 存在性。
- `SetContext(ctx context.Context)` / `AssetHandler() http.Handler` — 内部：注入 Wails ctx / `/skins/` 资产处理器。

## GitAssist（AI 辅助 git 提交）

`gitassist.Service`（`internal/gitassist/service.go`），支撑前端 AI 辅助 commit/push 面板。所有 git 操作带超时，失败时附带 stderr 便于直接展示。

- `RepoInfo(workDir string) (RepoStatus, error)` — 仓库状态快照（isGitRepo/branch/upstream/ahead/behind/staged/unstaged/untracked/remoteUrl）；非 git 仓库返回 `IsGitRepo=false` 而非 error。
- `ListBranches(workDir string) ([]BranchInfo, error)` / `SwitchBranch(workDir, branch string) error` — 分支列表/切换。
- `SummarizeDiff(workDir string) (string, error)` — AI 生成提交信息：解析 Settings 的提交总结模型引用（`provider/preset`，当前仅支持 OpenAI 兼容 provider），取 diff 调用模型生成。
- `CommitAll(workDir, message string) error` — `git add -A` 后提交（message 经 stdin 传入，不能为空）。
- `CommitStaged(workDir, message string) error` — 仅提交已暂存变更。
- `Push(workDir string) (string, error)` — 推送当前分支；无上游时自动 `git push --set-upstream origin <branch>`；返回 stderr 中的推送摘要（如 `main -> origin/main`）。
