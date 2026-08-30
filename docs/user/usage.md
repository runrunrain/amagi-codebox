# 界面功能总览

面向 Amagi CodeBox 的终端用户。本篇基于前端路由（`frontend/src/router/index.ts`）、侧栏布局（`frontend/src/components/layout/`）与各视图组件（`frontend/src/views/`）描述每个功能页的用途与核心元素，以及启动一个会话的完整流程。

Amagi CodeBox 的桌面前端采用 hash 路由（`createWebHashHistory`），URL 形如 `#/terminal`。左侧导航跳转即对应本文中的路由路径。未匹配的路径会被重定向到 `/`。

相关参考：

- 安装与首次运行：[./installation.md](./installation.md)
- 提供商与预设配置：[./providers.md](./providers.md)
- 后端 API 详细签名：[../api.md](../api.md)

---

## 顶层导航一览

侧栏导航（`SidebarNormal.vue` 的 `navItems`）固定为以下 6 项：

| 路由 | 视图组件 | 页面标题 | 用途 |
|------|----------|----------|------|
| `/` | `SessionSettingsView.vue` | 会话设置 | 配置并启动一个新的 AI 编程会话（应用默认页） |
| `/provider` | `ProviderCenterView.vue` | Provider Center | 统一管理服务提供商、公共预设与各 CLI 独立配置 |
| `/extensions` | `ExtensionsView.vue` | 扩展管理 | 管理五种引擎的插件 / 包、环境变量与辅助工具 |
| `/envcheck` | `EnvCheckView.vue` | 环境检测 | CLI 工具安装状态、版本与 PATH 校验；Windows 含 WSL 安装辅助 |
| `/logs` | `LogsView.vue` | 系统日志 | 查看应用运行日志与 Headroom 压缩统计 |
| `/usage` | `UsageView.vue` | 使用统计 | 五种引擎的 Token 用量、缓存经济性与成本趋势 |

`/terminal`（`TerminalPageView.vue`）不在主导航中，启动内嵌会话或点击侧栏会话条目时进入；该路由由 Vue `KeepAlive` 缓存。

除路由页外，还有两个全局界面层：

- **设置模式**：点击侧栏底部齿轮按钮进入。设置页（`frontend/src/views/settings/SettingsView.vue`）不走路由，直接替换主内容区。子页包括：常规设置、Shell、终端设置、外观（皮肤背景/调光/模糊，`internal/skins`）、远程控制、Agent 配置档（`internal/agentprofile`）、软件更新、关于。
- **主机切换器**（`HostScopeSwitcher.vue`）：位于侧栏顶部，快捷键 `Cmd/Ctrl+Shift+H`。可在"本机"与已登记的远程 CodeBox 主机之间切换；切到远程主机后，会话设置页替换为远程会话视图（`RemoteSessionsView.vue`），可查看并操作远端会话。详见 [./remote-mobile.md](./remote-mobile.md#桌面端互联remote-client)。

---

## 会话设置 `/`（SessionSettingsView）

应用默认页，页面描述："配置并启动一个新的 AI 编程会话"。

页面核心元素（按视觉顺序）：

1. **引擎方块选择（engine tiles）**：在五种引擎间切换——**ClaudeCode、Pi、OpenCode、Oh My Pi、Codex**。每种引擎的后续可选项不同。
2. **服务提供商**：下拉选择当前引擎对应的 provider。
3. **预设配置**：下拉选择当前 provider 下的 preset。
4. **启动模式**：下拉选择启动方式。常见取值包括：
    - `embedded`：内嵌终端（xterm.js + ConPTY/PTY），会话显示在 `/terminal` 页。
    - `terminal`：独立终端窗口（外部进程）。
    - 其他外部模式（如外部窗口 / webui 等，具体可选项随引擎而变）。
5. **终端 Shell**（仅 `embedded` 模式可见）：可选"直接启动"、内置 Shell（Windows 默认 WSL，macOS 默认 zsh，另含 PowerShell、bash 等，取决于平台能力）或"自定义路径"。
6. **工作目录**：本次会话的实际 `cwd`。OpenCode 引擎要求必填，未填写时给出"尚未选择启动目录"的提示。
7. **启用 Headroom 上下文压缩**（仅 Claude Code）：开启后经 Headroom 代理压缩上下文（`internal/headroom`）。
8. **启动按钮**：触发会话启动逻辑（详见下文"启动一个会话"）。

> 当主机切换器处于远程模式时，本页内容被 `RemoteSessionsView` 替换。字段实际名称、可选项与平台分发以应用内显示为准。

### 工作目录选型：DrvFS(/mnt/*) 与 ext4(~/)

Windows 上内嵌会话默认落在 WSL，工作目录的选择直接决定会话的 I/O 性能档位：

| 工作目录位置 | 文件系统 | I/O 特征 |
|--------------|----------|----------|
| WSL 发行版内（`~/projects` 等 ext4 路径） | ext4 | 原生 Linux 文件系统，小文件、git、npm 等密集 I/O 全速 |
| Windows 盘符路径（`D:\repo` 等） | DrvFS（WSL2 上经 9P 协议桥接，挂载为 `/mnt/d/repo`） | 跨操作系统边界的文件访问，小文件读写 / 目录扫描 / 文件监听（inotify）显著变慢，git 仓库操作尤甚 |

经验量级：在 DrvFS 上做 `git status` / `npm install` / 大量小文件读写的耗时通常是 ext4 的数倍到数十倍；会话内的 AI 工具频繁扫描代码库时差距会被放大。（WSL1 的 `/mnt/*` 是内核级翻译层，代价小于 WSL2 的 9P，但同样慢于 ext4。）

**选型建议：**

- 长期开发 / 重度会话：把仓库 clone 进 WSL 内（如 `~/projects/<repo>`），工作目录填 Linux 路径，获得完整 I/O 性能；
- 只是临时看 / 改几个 Windows 文件：用 `/mnt/<盘符>/...` 路径可以接受，避免来回搬迁；
- 仓库必须留在 Windows 侧（Windows 专属工具链、VS 调试等）：要么接受 DrvFS 慢档，要么改选 Windows 原生 Shell 会话（见 [./terminal.md](./terminal.md#何时选择-windows-原生-shell-会话)）。

> CodeBox 启动 WSL 会话时，若 `--cd` 工作目录映射到 `/mnt/<盘符>`（DrvFS/9P），后端会记一条 warn 日志「DrvFS 工作区 I/O 显著慢于 ext4」作为提示（`/logs` 页可查）；这只是建议，不会阻止会话启动。

---

## 终端 `/terminal`（TerminalPageView）

承载内嵌终端会话的页面。

- **空态**：未选中任何会话时，显示"尚未选择会话 / 请从左侧选择一个运行中的会话，或点击『新建会话』开始"。
- **挂载终端**：选中会话后，使用 `TerminalView` 组件（基于 xterm.js 6）挂载真实终端。组件以会话 ID 为 `key`，切换会话时强制重建，保证终端生命周期干净。
- **工具栏**：含"提交/推送"按钮，打开 **GitAssist 面板**（`frontend/src/components/terminal/GitPanel.vue`，后端 `internal/gitassist`）——查看仓库状态与分支、切换分支、由 LLM 生成 commit message、执行 commit 与 push。生成提交信息所用的模型来自当前配置的终端预设；未配置可用模型时后端返回中文错误提示。
- **WebUI 面板**：以 webui 模式启动的会话通过 `WebPlaneHost` 组件内嵌展示对应 CLI 的 Web 界面（`internal/webui` 负责探测与接入）。

终端后端由 `internal/pty` 提供（Windows ConPTY / macOS creack/pty）。会话输出通过 Wails 事件流式推送到前端。注意：原始 `pty.Service` **不直接绑定到前端**，所有终端读写经 `App` 门面方法门控（见 `bind_list.go` 注释），详见 [./terminal.md](./terminal.md)。

外部模式启动的会话不会显示终端内容——进程已在外部打开，留在当前页避免展示空终端。

---

## Provider Center `/provider`（ProviderCenterView）

统一管理服务提供商、可跨 CLI 复用的公共预设，以及各 CLI 的独立配置文件。

- **一级 Pill 导航**（`MAIN_TABS`）：
    - **服务提供商**：网格展示所有 provider，进入详情可编辑。
    - **预设**：按协议格式管理公共预设，二级 Tab 为 **Anthropic 格式 / OpenAI 格式**。Claude Code 使用 Anthropic 格式；Codex、Pi、OMP 共享 OpenAI 格式。预设编辑弹窗（`PresetDialog.vue`）中含 **视觉能力标记**（识图 Vision / 识视频 Video / 优先级），被标记的预设会导出到 `~/.agents/amagi-media-models.json` 供 amagi-media-understanding 等 skill 消费（契约见 `docs/vision-export-contract.md`）。
    - **CLI 独立配置**：直接编辑各 CLI 自己的配置文件，二级 Tab 为 **Pi / OMP / OpenCode**：
        - Pi 三级 Tab：Agent 配置（`~/.pi/agent/amagi.json`）/ 模型提供商（`~/.pi/agent/models.json`）/ 认证登录（`~/.pi/agent/auth.json`）。
        - OMP 三级 Tab：Agent 配置（`~/.omp/agent/config.yml`）/ 模型提供商（`~/.omp/agent/models.yml`）。
        - OpenCode 三级 Tab：预设管理 / 全局配置（`~/.config/opencode/opencode.json`）。
- **顶部右侧操作**（仅本机模式可见）：
    - **导出完整配置**：导出可迁移到新设备的完整 JSON 快照，包含明文密钥、环境变量和 CLI 独立配置，请妥善保管。
    - **导入完整配置**：从快照替换当前配置（含 CLI 独立配置）；旧版 v1 Provider 配置仍可兼容导入。成功后请重启应用。

Provider 与 Preset 的概念、字段含义与 `models.json` 结构详见 [./providers.md](./providers.md)。

---

## 扩展管理 `/extensions`（ExtensionsView）

页面描述："管理 Claude、OpenCode、Codex、Pi 与 OMP 插件和环境变量"。

- **一级 Pill 导航**：
    - **Plugins**：二级 Tab 为 ClaudeCode / OpenCode / Codex / Pi / OMP。Claude 与 Codex 支持 marketplace；OpenCode 直接按模块地址安装；Pi 管理 agent 目录下的 packages；OMP 通过 `omp plugin` 子命令管理。
    - **Environment**：环境变量面板（写入 `~/.amagi-codebox/envvars.json`）。
    - **Other tools**：当前为 Headroom 全局压缩卡片（Codex 全局 Headroom 开关 + 按客户端区分的压缩统计）。

插件系统对应后端 `internal/plugin`、`internal/opencodeplugin`、`internal/codexplugin`、`internal/piplugin` 与 `internal/ompplugin`，能力差异详见 [./plugins.md](./plugins.md)。

---

## 环境检测 `/envcheck`（EnvCheckView）

页面描述："CLI 工具安装状态、版本与 PATH 校验"。检测对象为六种工具：Claude Code、OpenCode、Codex、Pi、OMP 与 Headroom（`internal/envcheck/types.go` 的 `CLITool` 枚举）。

`App.Startup` 异步触发首次全量检测（`CheckAll`），结果缓存并由本页展示。页面通常提供：

- 各 CLI 工具的安装状态、版本号、可执行路径与 PATH 校验
- 一键修复（修复 PATH、安装工具、安装 Node.js 等，能力以平台与工具实际支持为准）
- **WSL 内的 CLI** 卡片（仅 Windows）：检测 WSL 环境并把 CLI 安装进 WSL（`internal/wslsetup`）

---

## 系统日志 `/logs`（LogsView）

页面描述："查看应用运行日志与调试信息"。日志文件落在 `~/.amagi-codebox/logs/`。

页面另含一张 **Headroom 上下文压缩统计** 卡片，展示累计、全局 ledger 数据，每 10 秒自动刷新。指标包括累计压缩次数、累计节省 Token、累计节省比例。空态与错误态均以内联提示展示。Headroom 后端位于 `internal/headroom`。

---

## 使用统计 `/usage`（UsageView）

页面描述："模型用量、缓存经济性与成本趋势；默认只统计会话日志"。覆盖五种引擎：Claude Code、OpenCode、Codex、Pi、Oh My Pi（应用筛选器中的五个选项）。数据源为各 CLI 的会话日志（`~/.claude/projects`、`~/.codex/sessions`、OpenCode 存储、`~/.pi/agent/sessions`、`~/.omp/agent/sessions` 等，枚举逻辑见 `internal/usage/sync.go`），汇总进本地 `usage.db`。

- **时间范围**：今日、近 7 天、近 30 天、本月，或自定义起止日期。默认"近 30 天 + 仅会话日志"，避免代理实时记录与会话日志重复计算。
- **Token 用量**：以 `m`（百万 Token）展示新输入、输出、缓存读取和缓存写入；对 Codex 的缓存读取会先从原始输入中拆出，避免重复统计。
- **缓存经济性**：缓存命中率、缓存折算 Token、缓存命中成本及相对全价输入节省的成本。
- **趋势**：成本与 Token 独立切换；每条曲线对应一个模型/供应商组合。
- **价格表**：查看、编辑、重置模型的输入、输出、缓存读取与缓存写入单价（存于 `usage-pricing.json`）；保存后重算本地估算记录，OpenCode 已提供的原始账单金额会保留。

---

## 启动一个会话

流程基于前端 `useSessionLaunch` composable 与后端 `app.go` 的五个启动入口。

### 前置条件

- 已选定引擎对应的 provider 与 preset（OpenCode 引擎允许 preset 为空，表示使用全局 `opencode.json`）。
- OpenCode 引擎要求工作目录必填；其他引擎未指定时使用默认路径。
- 内嵌模式（`embedded`）需要平台支持（`EmbeddedTerminalSupported`，Windows/macOS 均为 true）。

### 操作步骤

1. 进入"会话设置"页（路由 `/`）。
2. 在顶部引擎方块中选择：ClaudeCode / Pi / OpenCode / Oh My Pi / Codex。
3. 选择服务提供商与预设。ClaudeCode 要求所选 provider 兼容 Anthropic 格式。
4. 选择启动模式（`embedded` 或外部模式）；（仅 `embedded`）按需选择终端 Shell。
5. 设置工作目录（OpenCode 必填）；（仅 ClaudeCode）按需开启 Headroom。
6. 点击启动按钮。

### 启动后的行为

- **embedded 模式**：启动成功后自动跳转到 `/terminal`，新会话被设为活动会话并挂载 xterm 终端。
- **外部模式**：启动成功后留在当前页，提示"会话已在外部启动"。
- **启动失败**：弹出错误提示，不跳转。

### 五引擎对应的后端入口

| 引擎 | 后端方法（`app.go`） | 关键参数 |
|------|---------------------|----------|
| ClaudeCode | `LaunchSession` | `providerName, presetName, mode, workDir, useHeadroom, shellPath` |
| Codex | `LaunchCodexSession` | `modelName, providerID, mode, workDir, shellPath` |
| OpenCode | `LaunchOpenCode` | `providerName, presetName, mode, workDir, shellPath` |
| Pi | `LaunchPiSession` | `modelName, providerID, mode, workDir, shellPath` |
| Oh My Pi | `LaunchOmpSession` | `modelName, providerID, mode, workDir, shellPath` |

返回值为会话 ID（字符串）。会话状态、PID、启动时间等字段由 `internal/session.Session` 与 `SessionInfo` 承载。

---

## AppType 与会话状态

后端 `internal/session/types.go` 定义五种应用类型：

```go
const (
    AppTypeClaudeCode AppType = "claudecode"
    AppTypeOpenCode   AppType = "opencode"
    AppTypeCodex      AppType = "codex"
    AppTypePi         AppType = "pi"
    AppTypeOhMyPi     AppType = "omp"
)
```

> 历史类型 `amagicode`（内部 CLI）已移除，不再出现在任何启动入口中。

会话状态（`SessionStatus`）：`running` / `stopped` / `exited` / `failed`。

---

## 已知限制与注意事项

- 外部模式启动的会话不在 `/terminal` 页显示终端，需通过外部窗口或会话列表查看。
- 设置页与远程会话视图不占用路由，刷新页面后回到本机会话设置页。
- 视图层字段实际可选项与平台能力、当前配置密切相关，本篇不穷举具体取值。
- 远程控制 API（HTTP + WebSocket）独立于桌面 UI，由 `internal/remote` 提供，详见 [./remote-mobile.md](./remote-mobile.md)。
