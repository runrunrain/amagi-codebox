# Amagi CodeBox API 参考（Wails 绑定层）

本文档列出前端可调用的全部 Wails 绑定方法。方法清单**以当前代码为准**，生成依据：

- 绑定清单：`bind_list.go` 的 `buildWailsBindList`（共 15 个绑定 = `App` + 14 个服务）
- 方法签名：`App` 导出方法（`app.go`、`app_remoteclient.go`、`app_agentcli.go`、`app_agentcli_install.go`、`app_sharedskills.go` 等），以及 `internal/` 下各已绑定服务结构体的导出方法（以 `frontend/wailsjs/go/` 为生成物事实源）

## 调用方式

Wails 把每个绑定对象挂到 `window.go.<包名>.<结构体名>` 命名空间，并自动生成 TypeScript 封装到 `frontend/wailsjs/go/<包名>/`（**自动生成，勿手改**）。前端实际调用应走 `frontend/src/api/*.ts` 的类型化封装模块，而不是直接调用 wailsjs。

| 绑定 | JS 命名空间 | Go 结构体 |
|---|---|---|
| App | `main.App` | `*App` |
| Config | `config.ConfigService` | `internal/config` |
| Paths | `paths.PathsService` | `internal/paths` |
| Settings | `settings.Service` | `internal/settings` |
| Plugins | `plugin.Service` | `internal/plugin`（Claude Code） |
| CodexPlugins | `codexplugin.Service` | `internal/codexplugin` |
| OpenCodePlugins | `opencodeplugin.Service` | `internal/opencodeplugin` |
| PiPlugins | `piplugin.Service` | `internal/piplugin` |
| OmpPlugins | `ompplugin.Service` | `internal/ompplugin` |
| PiConfig | `piconfig.Service` | `internal/piconfig` |
| AgentProfiles | `agentprofile.Service` | `internal/agentprofile` |
| Usage | `usage.Service` | `internal/usage` |
| WebUI | `webui.Service` | `internal/webui` |
| Skins | `skins.Service` | `internal/skins` |
| GitAssist | `gitassist.Service` | `internal/gitassist` |

## 门控与收口说明：不进入 Bind 列表的 7 个原始服务

以下 7 个服务结构体**被有意排除在 `buildWailsBindList()`（`bind_list.go`）之外**（由 `bind_manifest_test.go` 的 T-24 断言守护）：

- `pty.Service`：终端写入/调整尺寸只能走 `App.PtyWrite` / `App.PtyWriteLarge` / `App.PtyResize` 门面；门面在 control-runtime 未就绪时 fail-closed（M-005）。
- `secrets.SecretsService` / `envcheck.Service` / `logging.Service` / `updater.Service` / `opencodeconfig.Service` / `ompconfig.Service`：已从 Bind 列表移除，仅保留为内部服务，前端通过 `App` 上的窄门面方法访问（`secrets.SecretsService.GetAllProviders` 仅保留给 `agentcli` 调用方内部使用）。

另有 `sharedskills.Service`（公共技能，`internal/sharedskills`）、`sharedmcp.Service`（共用 MCP，`internal/sharedmcp`）与 `sharedmarket.Service`（共用插件市场，`internal/sharedmarket`）同样不进 Bind 列表，前端经 `App` 门面访问（见下文「公共技能」「共用 MCP」与「共用插件市场」节）。

同样被排除的：

- `App.StopAllSessions`（仅内部关停路径使用）以及 `GetSettingsService` / `GetPathsService` / `GetConfigService` / `SaveAllConfig` / `GetKeyDiagnostics` / `GetSessionWebUI` / `ProbeSessionWebUI` 等旧服务 getter / 远程内部方法（`remote.AppInterface` 由内部 `remoteAppAdapter` 实现，不再暴露在 `*App` 绑定面上）。
- 已退役方法：`App.QuickLaunch`、`App.ClearStoppedSessionsDetailed`、`App.GetEnvCheckStatus`、`App.GetEnvCheckOperationState`、`App.GetLaunchCompensationDebts`、`App.RetryLaunchCompensationDebt`；`skins.Service.SetContext`（改为构造时通过 `skins.NewServiceWithContext` 注入只读上下文回调）；`config.ConfigService.SetAllTerminalPresets` / `DeleteOpenCodePreset`、`piconfig.Service.GetAmagiConfigAt` / `SaveAmagiConfigAt`、`piplugin.Service.WindowsEntryFilters`、`usage.Service.RecordForce`。

各服务结构体上的 `Load` / `Save` / `Close` 等方法属于后端内部生命周期钩子，虽然技术上在部分绑定结构体上可见，但前端不应调用；下文以「内部」标注。

## 目录

- [App（`main.App`）](#appmainapp)
- [Config（提供商与预设）](#config提供商与预设)
- [Paths（工作目录路径簿）](#paths工作目录路径簿)
- [Settings（应用设置）](#settings应用设置)
- [Plugins（Claude Code 插件）](#pluginsclaude-code-插件)
- [CodexPlugins（Codex 插件）](#codexpluginscodex-插件)
- [OpenCodePlugins（OpenCode 插件）](#opencodepluginsopencode-插件)
- [PiPlugins（Pi 扩展包）](#pipluginspi-扩展包)
- [OmpPlugins（omp 插件）](#omppluginsomp-插件)
- [PiConfig（Pi 配置文件）](#piconfigpi-配置文件)
- [AgentProfiles（Agent 配置档）](#agentprofilesagent-配置档)
- [Usage（用量统计）](#usage用量统计)
- [WebUI（pi Web UI 壳集成）](#webuipi-web-ui-壳集成)
- [Skins（皮肤）](#skins皮肤)
- [GitAssist（AI 辅助 git 提交）](#gitassistai-辅助-git-提交)
- [已从 Bind 列表移除的内部服务](#已从-bind-列表移除的内部服务)

---

## App（`main.App`）

`App` 是中央协调器（`app.go` 及 `app_*.go`），持有全部服务指针并暴露编排方法。按主题聚类如下。

### 生命周期与内部桥接

- `Startup(ctx)` / `Shutdown(ctx)` — Wails 生命周期钩子。Startup 加载配置、启动托盘与远程服务；Shutdown 保存配置并停止后台组件。前端不直接调用。
- 远程服务器内部桥接（`remote.AppInterface`）不再挂在 `*App` 导出方法上，而是由未导出类型 `remoteAppAdapter`（`a.remoteAdapter()`）实现。

### 应用信息与系统交互

- `GetAppInfo() map[string]any` — 返回 productName、version（ldflags 注入，回退 wails.json `info.productVersion`，再回退 `dev`）、buildTime、gitCommit、goVersion、configDir、runningCount。
- `GetStartupWarnings() []string` — 启动期间积累的警告（前端 onMounted 拉取一次，toast 展示）。
- `GetPlatformCapabilities() platform.PlatformCapabilities` — 启动时一次解析的平台能力（ConPTY/皮肤/远程等支持矩阵）。
- `BrowseDirectory() (string, error)` — 打开系统目录选择对话框，返回所选路径。
- `OpenFileInEditor(filePath string, line int) error` — 用系统默认程序打开文件；`line` 保留兼容但不使用。文件不存在时报错且不创建空文件。
- `OpenExternalURL(url string) error` — 使用系统默认浏览器打开经白名单协议校验的外链 URL。
- `SaveClipboardImage(base64Data string) (string, error)` — 把 base64 PNG 写入私有临时文件（0600，大小上限校验）并返回绝对路径，用于截图粘贴进终端的场景。

### 外部清理恢复

- `GetExternalCleanupRecoveryStatus() remote.ExternalCleanupRecoveryStatus` — 隐私最小化的外部进程恢复状态（会话 ID、共享服务类型、恢复原因、是否可安全确认；不暴露 PID/命令/环境/路径/provider/终端内容）。
- `ConfirmExternalCleanupRecovery(sessionID string, confirmed bool) (remote.ExternalCleanupRecoveryResult, error)` — 显式确认旧外部终端已关闭；仅 confirmed=true 且 OS 证明进程不存在且 journal exact completion 成功时清理；无 force-clear；每次接受/拒绝都记录 typed audit event。

### 环境检测与安装（App 窄门面，委托 `envcheck.Service`）

- `RunEnvCheck() (*envcheck.OverallStatus, error)` — 全量检测五种 CLI（`claude_code`/`opencode`/`codex`/`pi`/`omp`）。
- `CheckTool(tool string) (*envcheck.CheckStatus, error)` — 检测单个工具（`claude_code`/`opencode`/`codex`/`pi`/`omp`）。
- `InstallTool(tool string)` / `UpdateTool(tool string)` → `(*envcheck.InstallResult, error)` — 同步安装/更新。
- `StartInstallToolAsync(tool string)` / `StartUpdateToolAsync(tool string)` → `(*envcheck.OperationState, error)` — 异步安装/更新，后台 goroutine 执行，立即返回操作状态。
- `GetEnvCheckSnapshot() *envcheck.EnvCheckSnapshot` — 检测快照（含 `status` 与当前异步操作 `operation`，供远程 API 与前端轮询）。
- `RunEnvFixAction(action, tool, extraPath string) (*envcheck.FixActionResult, error)` — 执行白名单修复动作（fix_path/install_tool/install_node/retry/fix_claude_config/install_wsl_search_tools/uninstall_tool 等，见 `internal/envcheck/fix_dispatcher.go`）。`uninstall_tool` 仅用于 opencode/codex/pi/omp，`Success=true` 表示已受理；真实结果须读取快照中的 `operation.kind=uninstall` 终态。restart_app/manual_command 属前端提示，不是后台执行动作。
- `InstallClaudeWithMethod(method string)` / `StartInstallClaudeWithMethodAsync(method string)` — 按方式安装 Claude Code：`npm`（npm 全局）或 `native`（npm 全局后接 `claude install`）；后者为异步版本。
- `CleanClaudeInstall(method string) (*envcheck.InstallResult, error)` — 清理指定安装方式（`npm`/`native`）的 Claude Code 残留。
- `UninstallClaudeCode(method string) (*envcheck.InstallResult, error)` — 卸载已有安装；method 为空时从缓存状态自动识别安装方式。
- `CheckClaudeConfig() (*envcheck.ClaudeConfigStatus, error)` — 扫描 Claude Code 配置文件，报告各配置项存在/缺失。
- `FixClaudeConfig(key, value, filePath string) (*envcheck.ConfigFixResult, error)` — 修复指定 Claude 配置项。

### WSL 安装辅助（Windows）

- `GetWSLCLIStatus() wslsetup.Status` — WSL 内各 CLI 的安装状态。
- `InstallCLIToWSL(tool string) (*wslsetup.InstallResult, error)` — 把指定 CLI（claude/opencode/codex）安装进 WSL 发行版：确保原生 Node 20 + 用户级 npm 前缀，再 `npm i -g` 并校验；幂等。
- `GetDockerWSLHealth() dockerwsl.HealthReport` — Docker Desktop ↔ WSL 集成健康快照：共享挂载（docker-desktop-user-distro）字节数与状态、引擎就绪、docker-desktop 工具发行版、Desktop 进程计数、问题清单与自愈建议（2026-09-25 运维复盘 §1.2）。
- `SelfHealDockerWSLIntegration() dockerwsl.SelfHealReport` — 一键自愈：全退 Docker Desktop → 仅 `wsl --terminate docker-desktop`（用户发行版零影响）→ 全新启动 → 等引擎就绪（300s 冷启动预算）→ 复核集成挂载；返回分步回执与前后健康对照。

### 系统显式代理（System Proxy，仅 Windows）

- `GetSystemProxyStatus() SystemProxyStatus` — 全局设备显式代理快照：`supported`（平台能力）/`enabled`/`host`/`port`（系统实时值，Windows Internet Settings）/`reachable`（启用时对端点做 TCP+HTTP 双探测，识别"活端口死代理"）/`configuredHost`/`configuredPort`（持久化端点，下次开启写入）。
- `SetSystemProxyEnabled(enabled bool) (SystemProxyStatus, error)` — 开启/关闭系统级显式代理。开启：端点优先取持久化配置，为空回落系统现有地址（如代理客户端已写入的 ProxyServer），写入 `ProxyServer`+`ProxyEnable=1`（`ProxyOverride` 缺失时补默认回环绕行）；关闭：仅置 `ProxyEnable=0`，保留地址与例外列表。写入后广播 WinINet 刷新（InternetSetOption SETTINGS_CHANGED+REFRESH），已运行应用免重登生效。与 CLI 会话代理注入（SystemProxyEnv）互不影响。

### Agent CLI（命令行访问，`app_agentcli.go` / `app_agentcli_install.go`）

- `GetAgentCLIStatus() AgentCLIStatus` — 命令行访问状态快照：enabled/running/url/endpointPath/pid/startedAt（**不含令牌**；URL 本身不含令牌）。
- `SetAgentCLIEnabled(enabled bool) error` — 开关命令行访问：先持久化设置，再联动运行态（关闭 = 令牌作废 + 撤销端点文件，监听保留并对请求返回 503 cli_disabled）。
- `RotateAgentCLIToken() error` — 轮换 CLI 令牌（旧令牌立即失效并重写端点文件）；仅运行且启用时可用。
- `GetAgentCLIInstallStatus() AgentCLIInstallStatus` — 本机分发状态：shimPath/shimStatus、三个技能目标（agents/claude/codex）各自的 path/status、commandCount 与 shim 调用名 command。状态枚举 not-installed/up-to-date/outdated/modified-by-user/invalid（不含令牌）。
- `InstallAgentCLIShim() (AgentCLIInstallStatus, error)` / `RemoveAgentCLIShim() (AgentCLIInstallStatus, error)` — 安装/清理 `~/.amagi-codebox/bin/codebox-agent`（Windows 为 `.cmd`）shim；清理会记录用户偏好，应用启动不再自动装回。均返回更新后的状态。
- `InstallAgentCLISkill(target string) (AgentCLIInstallStatus, error)` / `RemoveAgentCLISkill(target string) (AgentCLIInstallStatus, error)` — 安装/清理 agent 技能（target ∈ agents|claude|codex），返回更新后的状态。被外部改动的托管文件一律保留并报冲突，不覆盖。

### 公共技能（共用技能，`app_sharedskills.go`）

管理 `~/.agents/skills`（启用态）与 `~/.agents/skills-disabled`（禁用态）下的共用技能目录（机制本体在 `internal/sharedskills`，不进 Bind 列表）。视图类型 `SharedSkillView` 在 `sharedskills.Skill`（name/displayName/description/version/author/enabled/path/hasSkillMd）之上附加 managed 标记：名为 `amagi-codebox` 的自带技能联动 `agentcli` install 的五态（not-installed/up-to-date/outdated/modified-by-user/invalid）。

- `ListSharedSkills() ([]SharedSkillView, error)` — 枚举两个根目录下的全部共用技能（按名称排序；元数据取自 SKILL.md frontmatter，缺失时如实回退目录名并标记 `hasSkillMd=false`）。
- `RefreshSharedSkills() ([]SharedSkillView, error)` — 显式刷新；服务层无缓存，语义等同 `ListSharedSkills`（为契约点名的 Refresh 显式提供）。
- `SetSharedSkillEnabled(name string, enabled bool) ([]SharedSkillView, error)` — 启用/停用：目录在 `skills` ⇄ `skills-disabled` 间移动，对真实读取 `~/.agents/skills` 的 CLI 立即生效；返回最新列表（前端免二次请求）。managed 技能（`amagi-codebox`）拒绝启停（与 InstallDefaults 启动自动装回冲突）。
- `DeleteSharedSkill(name string) ([]SharedSkillView, error)` — 永久删除手动技能目录（含禁用态）；应用托管与有共用市场来源记录的技能拒绝单独删除。
- `PickSharedSkillsZip()` / `PickSharedSkillsDir()` — 系统文件/目录选择器。`ImportSharedSkillsZip(path)` / `ImportSharedSkillsDir(path)` — 导入到禁用根，不覆盖同名技能、不分发到引擎私有目录。
- `OpenSharedSkillFolder(name string) error` — 用系统文件管理器打开指定技能的当前目录（先校验存在再打开）。
- `OpenSharedSkillsRoot() error` — 打开 `~/.agents/skills` 根目录；根目录不存在时如实报错并提示先安装技能（不自动创建目录）。

### 共用 MCP（`app_sharedmcp.go`）

管理 `~/.agents/.mcp.json`（顶层 `mcpServers.<name>`）里的共用 MCP 服务器登记，并分发 / 还原到四个引擎的真实 MCP 配置——Claude `~/.claude.json` 顶层 `mcpServers`；Codex `~/.codex/config.toml`（`CODEX_HOME` 优先）的 `[mcp_servers.<name>]` 段；OpenCode `~/.config/opencode/opencode.json` 顶层 `mcp`（V2 `mcp.servers` 形态检测）；Pi `$PI_CODING_AGENT_DIR`（否则 `~/.pi/agent`）下 `mcp.json` 顶层 `mcpServers`。OMP 无 MCP 配置面，恒为 excluded 只读行。机制本体在 `internal/sharedmcp`，不进 Bind 列表；勾选与写入跟踪存应用配置目录 `shared-mcp-state.json`（选择状态不写 `~/.agents`）。视图类型 `SharedMcpOverview` 内嵌 `sharedmcp.Overview`：`entries[]`（entry + `nameDistributable` + 每引擎一格 status/selected/reason）、`registryExists` / `registryPath` 与 `enginesMeta[]`（id/name/support/targetPath/managedCount，含 omp excluded 行）。

- `ListSharedMcp() (*SharedMcpOverview, error)` — 全量状态视图：登记条目 × 各引擎的分发矩阵（distributed / drift / not-distributed / conflict / unsupported / excluded，逐格实测目标文件）与引擎元数据；服务层无缓存，同时清理状态库中登记外的孤儿条目。
- `RefreshSharedMcp() (*SharedMcpOverview, error)` — 显式刷新；服务层无缓存，语义等同 `ListSharedMcp`。
- `CreateSharedMcpEntry(entry sharedmcp.SharedMcpEntry) (*SharedMcpOverview, error)` — 在登记文件新增条目（stdio → command 必填、http → 合法 URL 必填；名字限 `^[A-Za-z0-9_-]+$`；同名已存在拒绝；文件缺失自动以 0600 原子创建；条目未知键不参与）。新条目默认全部引擎未勾选。
- `UpdateSharedMcpEntry(name string, entry sharedmcp.SharedMcpEntry) (*SharedMcpOverview, error)` — 更新登记条目内容（name 是标识不可改；条目不存在拒绝；未知键 raw 值原样合并保留）。只改登记文件，不自动重分发——已分发引擎如实转 drift，由用户逐引擎重新分发。
- `DeleteSharedMcpEntry(name string, force bool) (*SharedMcpOverview, error)` — 删除登记条目并先逐引擎还原：Applied 非空时逐引擎 Undistribute（force 透传），任一失败整体失败返回（登记保留、已还原引擎保持已还原），全部成功才删登记 + 清该条目状态。
- `SetSharedMcpEngineSelection(name, engine string, selected, force bool) (*SharedMcpOverview, error)` — 设置「条目 × 引擎」一格勾选：selected=true → Distribute（幂等 resync，drift 格的「重新分发」即本方法；目标被外部改过返回 ErrForeignModified，force 确认后才覆盖）；false → Undistribute（外改同样需 force）。逐引擎独立成败，互不牵连。

- `ParseSharedMcpImportText(text)` / `PickSharedMcpImportFile()` — 粘贴 JSON 或选择文件后预览，预览表只展示名称、类型和校验状态。`ImportSharedMcpEntries(text)` — 显式提交批量登记，不覆盖同名、不自动分发。

列表和写入方法返回最新 Overview（前端免二次请求）；错误文案与日志不含 env / headers 明文。「打开登记目录」复用通用门面 `OpenFileInEditor`。

### 共用插件市场（`app_sharedmarket.go`）

把 Claude Code 插件市场（或任意 git 市场源）以引擎中立形式纳入统一管理：浅 clone 到 `~/.agents/marketplaces/<name>`，登记状态存 `~/.agents/marketplaces.json`（tmp+rename 原子写）；市场内插件的 skills 可一键部署到 `~/.agents/skills/<技能名>` 成为共用技能（跨 CLI 生效，由「共用技能」门面统一启停管理）。机制本体在 `internal/sharedmarket`（git 经注入点执行，不感知来源引擎），不进 Bind 列表。设计边界：部署只覆盖 skills——commands/agents/hooks/MCP 是 Claude 插件系统语义、无 `~/.agents` 对应物，外部 git 引用条目（官方市场 git-subdir 形态）如实标记不可部署；部署不覆盖已存在技能目录（防丢用户改动），复制走技能根下隐藏暂存目录 `.deploy-staging`，全部就绪后逐个原子发布，失败清理可重试。

- `ListSharedMarkets() ([]sharedmarket.Marketplace, error)` — 全部共用市场（状态 + 实时目录扫描；单市场 clone/目录不可读时置 `missing` 如实呈现）。
- `RefreshSharedMarkets() ([]sharedmarket.Marketplace, error)` — 显式刷新；服务层无缓存，语义等同 `ListSharedMarkets`。
- `AddSharedMarketplace(source string) ([]sharedmarket.Marketplace, error)` — 从 git URL/本地路径新增：浅 clone → 校验 `.claude-plugin/marketplace.json`（非法市场回滚清理）→ 落状态（失败回滚 clone）；市场名从源地址 basename 推导，同名拒绝。
- `ListClaudeMarketplacesForImport() ([]ClaudeMarketplaceForImport, error)` — Claude Code 已注册市场 + 已导入标记（源转换：URL > github repo 合成 > 本地 installLocation）。
- `ImportSharedMarketplacesFromClaude(names []string) ([]sharedmarket.Marketplace, error)` — 按名从 Claude 注册表导入（保持同名）；单市场原子，批量不回滚、失败即中止并携带市场名。
- `UpdateSharedMarketplace(name string) ([]sharedmarket.Marketplace, error)` — `git pull --ff-only`；clone 目录缺失时重新克隆恢复。
- `RemoveSharedMarketplace(name string) ([]sharedmarket.Marketplace, error)` — 删除 clone 目录与登记；有来源记录的部署须先卸载。
- `DeploySharedMarketplacePlugin(marketName, pluginName string) (*sharedmarket.DeployReport, error)` — 部署插件 skills 到 `~/.agents/skills`（名称段/符号链接祖先/最终目标三层安全校验；报告 `deployed`/`skipped` 恒为数组）。
- `UninstallSharedMarketplacePlugin(marketName, pluginName)` — 只删除匹配来源记录的技能（含停用态）；保留旧版无来源记录的条目、手动技能和市场 clone。
- `GetSharedSkillOrigin(name)` / `ListSharedSkillOrigins()` — 读取共用市场部署来源记录；不遍历引擎私有插件。
- `OpenSharedMarketplaceFolder(name string) error` — 系统文件管理器打开市场 clone 目录。

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
- `RemoteClientLaunchRemoteSession(cliType, workdir, providerRef, presetRef, modelRef, shellRef string) (contract.SessionDetail, error)` — 在远端启动会话。
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

- `LaunchSession(providerName, presetName, mode, workDir, shellPath string) (string, error)` — 启动 Claude Code 会话：解析 provider/preset，返回会话 ID。
- `LaunchCodexSession(modelName, providerID, mode, workDir, shellPath string) (string, error)` — 启动 Codex 会话。
- `LaunchPiSession(modelName, providerID, mode, workDir, shellPath string) (string, error)` — 启动 Pi 会话（写配置到 `~/.pi/agent`）。
- `LaunchOmpSession(modelName, providerID, mode, workDir, shellPath string) (string, error)` — 启动 omp（Oh My Pi）会话（写配置到 `~/.omp/agent`）。
- `LaunchOpenCode(providerName, presetName, mode, workDir, shellPath string) (string, error)` — 启动 OpenCode 会话；双轨兼容：优先 `opencode_presets`（新模型），回退 `terminal_presets.opencode`（旧模型）。
- `GetSession(sessionID string) (session.SessionInfo, error)` / `GetSessions() []session.SessionInfo` — 单个/全部会话摘要（id/appType/provider/preset/model/mode/workDir/status/pid/startedAt/duration/title/claudeSessionId）。
- `StopSession(sessionID string) error` — 停止会话。
- `RemoveSession(sessionID string) error` — 移除已停止会话的记录。
- `ClearStoppedSessions() int` — 清理全部已停止会话，返回清理数量。

### PTY 门面（fail-closed）

- `PtyWrite(sessionID, data string) error` — 向内嵌终端写入 base64 数据；control-runtime 未就绪返回 `remote.ErrControlNotReady`。
- `PtyWriteLarge(sessionID, data string) error` — 长文本粘贴：内部拆 1KB 小块逐步写入，避免 ConPTY 缓冲区溢出截断。
- `PtyResize(sessionID string, cols, rows int) error` — 调整内嵌终端尺寸。
- `GetOutputHistorySnapshot(sessionID string) (string, error)` — 输出历史快照（base64 data + seq 的 JSON；control 就绪时经 Projector 格式化）。
- `GetPtyDimensions(sessionID string) (cols, rows int, err error)` — 当前 PTY 尺寸（亦实现 remote.DimensionsProvider 接口）。

### 日志（App 窄门面，委托内部 logging.Service）

- `GetLogs(level, source, keyword string, limit int) []logging.Entry` / `GetLogSources() []string` / `GetLogFiles() []string` / `GetLogFileContent(filename string) (string, error)` / `ClearLogs()` / `ExportLogs() (string, error)` — 经 `App` 暴露前端所需的日志查询、清理与导出（`logging.Service` 本身未进入 Bind 列表）。

### 自动更新（App 窄门面，委托内部 updater.Service，未直接绑定）

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

### 配置导入导出

- `ExportConfigToFile() (string, error)` — 弹出保存对话框，把全部可移植配置合并导出为 JSON（原子写入，仅当前用户可读）；用户取消返回 `("", nil)`。
- `ImportConfigFromFile() (string, error)` — 弹出文件选择对话框导入；v2 文件完整快照替换语义，v1 按旧协议兼容导入。

### 环境变量（`envvars.json`）

- `GetEnvVars() ([]envvars.EnvVar, error)` / `SetEnvVar(key, value string) error` / `DeleteEnvVar(key string) error` — 环境变量 CRUD。
- `ImportEnvVars(jsonStr string) error` / `ExportEnvVars() (string, error)` — JSON 字符串导入/导出。
- `GetEnvVarsJSON() (string, error)` / `SaveEnvVarsJSON(jsonStr string) error` — 原文读写。
- `ExportEnvVarsToFile() error` / `ImportEnvVarsFromFile() error` — 经系统对话框导出/导入文件。
- `GetEnvVarsGlobalSyncStatus() envvars.GlobalSyncStatus` / `SetEnvVarsGlobalSyncEnabled(enabled bool) (envvars.GlobalSyncStatus, error)` — 全局同步开关状态/设置。

### 各 CLI 配置文件直通（App 门面）

- OpenCode：`GetOpenCodeConfig()` / `SaveOpenCodeConfig(content)` / `GetOpenCodeConfigPath()`
- Pi：`GetAmagiConfig()` / `SaveAmagiConfig(content)` / `GetAmagiConfigPath()`；`GetPiModelsConfig()` / `SavePiModelsConfig(content)` / `GetPiModelsConfigPath()`；`GetPiAuthConfig()` / `SavePiAuthConfig(content)` / `GetPiAuthConfigPath()`；`GetPiModelCatalog()`
- omp：`GetOmpConfig()` / `SaveOmpConfig(content)` / `GetOmpConfigPath()`；`GetOmpModelsConfig()` / `SaveOmpModelsConfig(content)` / `GetOmpModelsConfigPath()`；`GetOmpModelCatalog()`

说明：Pi 配置文件亦由 `PiConfig` 直接绑定；`opencodeconfig.Service` 与 `ompconfig.Service` 已从 Bind 列表移除，前端统一通过上述 `App` 窄门面读写 OpenCode 与 omp 配置。

### 常用工作目录（委托 Settings）

- `GetSavedWorkDirs() ([]settings.WorkDirEntry, error)` / `AddSavedWorkDir(path, label string) ([]settings.WorkDirEntry, error)` / `RemoveSavedWorkDir(path string) ([]settings.WorkDirEntry, error)` — 收藏工作目录列表（返回更新后的全量列表）。

### 密钥窄门面（替代 Secrets 服务绑定）

- `HasProviderAPIKey(provider string) bool` / `GetProviderAPIKey(provider string) (string, error)` — 按 provider 查询密钥是否存在/读取密钥（`secrets.SecretsService` 已从 Bind 列表移除，`GetKeyDiagnostics` 仅由内部 `remoteAppAdapter` 使用）。

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
- `GetAllTerminalPresets() *TerminalPresetsConfig` — 全桶读取（批量写回 `SetAllTerminalPresets` 已退役/内部化，未保留在公开绑定面）。
- `MigrateProviderPresetsToTerminal() (int, bool, error)` — 迁移 provider 内嵌预设到终端预设桶，返回（迁移数, 是否有变更, 错误）。
- `GetMergedTerminalPresets(terminalType string) ([]MergedTerminalPreset, error)` — 合并视图（含来源标记与视觉标记）。
- `ResolveTerminalPreset(terminalType, key string) (string, *TerminalPreset, error)` — 按 key 解析到具体预设。
- `GetOpenCodePresets() map[string]OpenCodePreset` / `GetAllOpenCodePresets() map[string]OpenCodePreset` / `GetOpenCodePreset(key string) (*OpenCodePreset, error)` / `SaveOpenCodePreset(key string, preset OpenCodePreset) error` — OpenCode 预设（新模型）读取与保存（删除统一经 `App.DeleteTerminalPreset`）。
- `ReplaceImportedPresetSnapshots(terminal *TerminalPresetsConfig, openCode map[string]OpenCodePreset, hasExplicitOpenCodeSnapshot bool) error` — 导入时替换预设快照。
- `GetAgentTeams() AgentTeamsConfig` / `SetAgentTeams(config AgentTeamsConfig) error` — Agent Teams 开关（enabled/teammate_mode）。
- `GetUrlHistory(providerID string) ([]string, error)` / `AddUrlToHistory(providerID, url string) error` / `RemoveUrlFromHistory(providerID, url string) error` — URL 历史。


## Paths（工作目录路径簿）

`paths.PathsService`（`internal/paths/service.go`），持久化到 `paths.json`。

- `Load() error` / `Save() error` — 内部。
- `GetPaths() []PathEntry` / `GetDefaultPath() string` / `GetConfig() PathsConfig` — 读取。
- `ReplaceConfig(next PathsConfig) error` — 整体替换。
- `SetDefaultPath(path string) error` / `AddPath(entry PathEntry) error` / `RemovePath(path string) error` / `UpdateLabel(path, label string) error` — 写操作。
- `ValidatePath(path string) bool` — 校验目录是否存在。
- `ListDirectories(root string) (string, error)` — 列出 root 下一层子目录（仅目录、跳过 `.` 开头、大小写不敏感排序、上限 500 条超限截断），返回 JSON `"{"root","parent","dirs":[{"name","path"}],"truncated"}"`；root 为空回退用户主目录，文件系统根的 parent 为 JSON `null`。


## Settings（应用设置）

`settings.Service`（`internal/settings/service.go`），持久化到 `settings.json`。

- `Load() error` / `Save() error` — 内部。
- `GetSettings() *AppSettings` / `ReplaceSettings(next AppSettings) error` — 全量读/替换。
- `GetDashboardDefaults() DashboardDefaults` / `SetDashboardDefaults(d DashboardDefaults) error` — 仪表盘默认启动项（各 CLI 的 provider/preset/mode/shell 等）。
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
- `GetRemoteLaunchDefaultsV1() map[string]RemoteLaunchDefaultV1` / `RecordRemoteLaunchDefaultV1(cliType string, refs RemoteLaunchDefaultV1) error` — v1 远程启动默认引用（providerRef/presetRef/modelRef/shellRef），按 CLI 类型记录一次成功激活的启动。


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
- `UpdatePlugin(selector PluginSelector) (*CommandResult, error)` — 单插件更新（模拟实现：市场刷新 → 卸载重装 → 恢复 enabled 状态；CLI 0.169 无原生 update 子命令）。
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
- `SetPackageEnabled(source string, enabled bool) (*CommandResult, error)` — 包级启停（模拟：pi 无原生 disable，用原生 `!**` 四键全排除过滤标记，与 `pi config` TUI 语义互通，完全可逆）。
- `SetPackageSkillEnabled(source, skillName string, enabled bool) (*CommandResult, error)` — 单 skill 启停（原生：packages 条目 `skills[]` 过滤数组写 `±rel` 模式，算法与 `pi config` TUI togglePackageResource 对齐）。
- `ResolvedEntityVersion(source string)` / `ResolvedEntityCommit(source string)` — 解析已安装包实体的版本与 Git commit（包内原 `WindowsEntryFilters` 已退役为非绑定包级辅助 `EntryFiltersFor`）。

## OmpPlugins（omp 插件）

`ompplugin.Service`（`internal/ompplugin/service.go`）。

- `ListPlugins() ([]Plugin, error)` / `RefreshPlugins() (*PluginsData, error)` — 列表/刷新。
- `InstallPlugin(spec string)` / `UninstallPlugin(name string)` / `SetPluginEnabled(name string, enabled bool)` / `UpgradePlugin(name string)` → `(*CommandResult, error)` — 安装/卸载/启停/升级。
- `InspectPlugin(plugin Plugin) *PluginDetail` — 零 CLI fork 的本地扫描详情（含 skills，供聚合批量取用）；`GetPluginDetails(name string) (*PluginDetail, error)` — CLI 定位+扫描详情。

## 旧版共用数据

`GetLegacySharedExtensionsSummary()` 只返回已退役的 `~/.amagi-codebox/shared/registry.json` 登记数量与目录，不返回 MCP 配置，也不枚举插件或写入旧仓库。

## PiConfig（Pi 配置文件）

`piconfig.Service`（`internal/piconfig/service.go`，`~/.pi/agent/`）。原同系列的 `opencodeconfig.Service` 与 `ompconfig.Service` 已从 Bind 列表移除，前端统一通过 `App` 门面读写 OpenCode 与 omp 配置。


- `GetAmagiConfig()` / `SaveAmagiConfig(content)` / `GetAmagiConfigPath()` — amagi.json 读写/路径；保存时校验 `compaction` 为仅含 `model` 的对象，`model` 可省略、为 `null` 或完整 `provider/model[:thinking]`。校验失败不改原文件；读取仍保留无效配置供源码修复。
- `GetModelsConfig()` / `SaveModelsConfig(content)` / `GetModelsConfigPath()` — models.json 读写/路径。
- `GetAuthConfig()` / `SaveAuthConfig(content)` / `GetAuthConfigPath()` — auth 配置读写/路径。
- `GetPiModelCatalog() (string, error)` — 从 models.json 抽取 provider→models 目录（只读、不含 apiKey 等敏感信息），文件缺失返回空目录。
- `GetPiLoginProviders()` / `ListCodexAdvisorChats(string)` / `ReadAgentFileAt(string, string)` — 登录 provider 列表、Codex 顾问会话列举与代理目录受控文件读取（原路径注入变体 `GetAmagiConfigAt` / `SaveAmagiConfigAt` 已退役）。


## AgentProfiles（Agent 配置档）

`agentprofile.Service`（`internal/agentprofile/service.go`），持久化到 `agent-profiles.json`；把 pi 的 `amagi.json` 与 omp 的 `config.yml` 打包为命名配置档，可一键切换。

- `ListAgentProfiles() (string, error)` — 存储全文 JSON（前端解析展示）；文件缺失返回空骨架。
- `GetAgentProfile(name string) (string, error)` — 单个配置档 JSON（`{pi, omp, updatedAt}`，预览用）。
- `CaptureAgentProfile(name string) error` — 把当前 live 配置快照为命名档（存在则覆盖）；pi 的 amagi.json 必须可读，omp 的 config.yml 缺失记空串。
- `SaveAgentProfile(name, piContent, ompContent string) error` — 显式内容保存；pi 侧非空必须是合法 JSON，omp 侧非空必须是根为映射的合法 YAML，非法报错不写。
- `ApplyAgentProfile(name string) error` — 应用配置档到 live 配置（应用前备份，仅保留一份；成功后更新 lastApplied；内容非法不写任何文件）。
- `DeleteAgentProfile(name string) error` — 删除；删除的是 lastApplied 时同步清空。



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

- `PickSkinImage() (*Skin, error)` — 弹出文件对话框选择并导入图片（通过构造期 `skins.NewServiceWithContext` 传入的 context 回调获取 Wails ctx）。
- `ImportSkinImage(path string) (*Skin, error)` — 从指定路径导入。
- `ListSkins() ([]Skin, error)` — 皮肤列表（id/fileName/url/bytes/宽高/importedAt）。
- `RemoveSkin(id string) error` — 删除。
- `GetSkinSettings() settings.SkinSettings` / `SetSkinSettings(sk settings.SkinSettings) error` — 皮肤设置读写；Enabled 时校验 ImageID 存在性。
- `AssetHandler() http.Handler` — 内部：`/skins/` 资产处理器（Wails ctx 在构造期经 `skins.NewServiceWithContext` 绑定，`SetContext` 已移除）。

## GitAssist（AI 辅助 git 提交）

`gitassist.Service`（`internal/gitassist/service.go`），支撑前端 AI 辅助 commit/push 面板。所有 git 操作带超时，失败时附带 stderr 便于直接展示。

- `RepoInfo(workDir string) (RepoStatus, error)` — 仓库状态快照（isGitRepo/branch/upstream/ahead/behind/staged/unstaged/untracked/remoteUrl）；非 git 仓库返回 `IsGitRepo=false` 而非 error。
- `ListBranches(workDir string) ([]BranchInfo, error)` / `SwitchBranch(workDir, branch string) error` — 分支列表/切换。
- `SummarizeDiff(workDir string) (string, error)` — AI 生成提交信息：解析 Settings 的提交总结模型引用（`provider/preset`，当前仅支持 OpenAI 兼容 provider），取 diff 调用模型生成。
- `CommitAll(workDir, message string) error` — `git add -A` 后提交（message 经 stdin 传入，不能为空）。
- `CommitStaged(workDir, message string) error` — 仅提交已暂存变更。
- `Push(workDir string) (string, error)` — 推送当前分支；无上游时自动 `git push --set-upstream origin <branch>`；返回 stderr 中的推送摘要（如 `main -> origin/main`）。

## 已从 Bind 列表移除的内部服务

以下 6 个服务曾直接列入 Wails Bind 列表，现已统一从 `bind_list.go` 移出，仅作为 Go 后端内部服务运行（与 `pty.Service` 共 7 个未绑定服务，见 `bind_manifest_test.go`）：

- **Secrets**（`secrets.SecretsService`，`internal/secrets/service.go`，Windows DPAPI / macOS Keychain 加密存储）：前端统一经 `App.HasProviderAPIKey` / `App.GetProviderAPIKey` 查询；密钥诊断由内部 `remoteAppAdapter.GetKeyDiagnostics` 调用；`GetAllProviders()` 仅保留给 `agentcli` 调用方（`agentcli_h_config.go`、`agentcli_h_usage.go`）内部使用。
- **EnvCheck**（`envcheck.Service`，`internal/envcheck/`）：前端统一经 `App` 窄门面访问（`RunEnvCheck`、`CheckTool`、`GetEnvCheckSnapshot`、`InstallTool`、`UpdateTool`、`StartInstallToolAsync`、`StartUpdateToolAsync`、`InstallClaudeWithMethod`、`StartInstallClaudeCodeWithMethodAsync`、`CleanClaudeInstall`、`UninstallClaudeCode`、`CheckClaudeConfig`、`FixClaudeConfig`、`RunEnvFixAction`）。
- **Log**（`logging.Service`，`internal/logging/service.go`）：前端统一经 `App` 窄门面访问（`GetLogs`、`GetLogSources`、`GetLogFiles`、`GetLogFileContent`、`ClearLogs`、`ExportLogs`）。
- **Updater**（`updater.Service`，`internal/updater/service.go`）：前端统一经 `App` 窄门面访问（`CheckForUpdate`、`DownloadAndApplyUpdate`、`GetGitHubToken`、`SetGitHubToken`）。
- **OpenCodeConfig**（`opencodeconfig.Service`，`internal/opencodeconfig/service.go`）：前端统一经 `App` 窄门面访问（`GetOpenCodeConfig`、`SaveOpenCodeConfig`、`GetOpenCodeConfigPath`）。
- **OmpConfig**（`ompconfig.Service`，`internal/ompconfig/service.go`）：前端统一经 `App` 窄门面访问（`GetOmpConfig`、`SaveOmpConfig`、`GetOmpConfigPath`、`GetOmpModelsConfig`、`SaveOmpModelsConfig`、`GetOmpModelsConfigPath`、`GetOmpModelCatalog`）。
