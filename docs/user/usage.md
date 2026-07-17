# 界面功能总览

面向 Amagi CodeBox 的终端用户。本篇基于前端路由（`frontend/src/router/index.ts`）与各视图组件（`frontend/src/views/`）描述每个功能页的用途与核心元素，以及启动一个会话的完整流程。

Amagi CodeBox 的桌面前端采用 hash 路由（`createWebHashHistory`），URL 形如 `#/terminal`。左侧导航跳转即对应本文中的路由路径。

相关参考：

- 安装与首次运行：[./installation.md](./installation.md)
- 提供商与预设配置：[./providers.md](./providers.md)
- 后端 API 详细签名：[../api.md](../api.md)

---

## 顶层导航一览

| 路由 | 视图组件 | 页面标题 | 用途 |
|------|----------|----------|------|
| `/` | `SessionSettingsView.vue` | 会话设置 | 配置并启动一个新的 AI 编程会话（应用默认页） |
| `/terminal` | `TerminalPageView.vue` | 终端 | 显示当前选中会话的内嵌 xterm 终端 |
| `/provider` | `ProviderCenterView.vue` | Provider Center | 统一管理服务提供商与各引擎预设 |
| `/extensions` | `ExtensionsView.vue` | 扩展管理 | 管理 Claude / Codex 插件、工作区、环境变量 |
| `/rules` | `RulesView.vue` | 注入规则 | 管理 API 注入规则与代理状态 |
| `/envcheck` | `EnvCheckView.vue` | 环境检测 | CLI 工具安装状态、版本与 PATH 校验 |
| `/logs` | `LogsView.vue` | 系统日志 | 查看应用运行日志与 Headroom 压缩统计 |

未匹配的路径（如手动输入不存在的 hash）会被重定向到 `/`。

---

## 会话设置 `/`（SessionSettingsView）

应用默认页，用于配置并启动一个新的会话。顶部页面描述："配置并启动一个新的 AI 编程会话"。

页面核心元素（按视觉顺序）：

1. **引擎切换（Segmented）**：在 ClaudeCode、OpenCode、Codex 三种引擎间切换。每种引擎的后续可选项不同。
2. **服务提供商**：下拉选择当前引擎对应的 provider。
3. **预设配置**：下拉选择当前 provider 下的 preset。
4. **启动模式**：下拉选择启动方式。常见取值包括：
    - `embedded`：内嵌终端（xterm.js + ConPTY/PTY）
    - `terminal`：独立终端窗口（外部进程）
    - 其他外部模式（如外部窗口 / webui 等，具体可选项随引擎而变）
5. **终端 Shell**（仅 `embedded` 模式可见）：可选"直接启动"、内置 Shell（如 PowerShell、zsh、bash 等，取决于平台能力）或"自定义路径"。选择自定义路径时，下方出现"Shell 路径"输入框。
6. **工作目录**：本次会话的实际 `cwd`。可通过"浏览"按钮选择，或直接输入路径。OpenCode 引擎要求必填，未填写时会给出"尚未选择启动目录"的提示。
7. **启动按钮**：触发会话启动逻辑（详见下文"启动一个会话"）。

> 字段实际名称、可选项与平台分发以应用内显示为准。本篇不罗列具体下拉项取值，避免与运行时能力产生偏差。

---

## 终端 `/terminal`（TerminalPageView）

承载内嵌终端会话的页面。

- **空态**：未选中任何会话时，显示"尚未选择会话 / 请从左侧选择一个运行中的会话，或点击『新建会话』开始"。
- **挂载终端**：选中会话后，使用 `TerminalView` 组件（基于 xterm.js）挂载真实终端。组件以会话 ID 为 `key`，切换会话时会强制重建，保证每个会话的终端生命周期干净。

终端后端由 `internal/pty` 提供（macOS 用 creack/pty，Windows 用 ConPTY）。会话输出通过 Wails 注册的回调流式推送到前端（详见 `app.go` 的 `RegisterOutputCallback` 等方法）。

注意：外部模式（`terminal`、外部窗口、webui 等）启动的会话不会显示在此页 —— 此时进程已在外部打开，留在当前页避免展示空终端。

---

## Provider Center `/provider`（ProviderCenterView）

统一管理服务提供商与各引擎预设。顶部页面描述："统一管理服务提供商与各引擎预设"。

页面结构：

- **一级 Pill 导航**（`Segmented`）：
    - **服务提供商**：以网格展示所有 provider，进入详情可编辑单个 provider。详见 [./providers.md](./providers.md)。
    - **预设**（启动配置）：按引擎管理预设，下方提供二级下划线 Tab。
- **顶部右侧操作**：
    - **导出配置**：导出整个 `config.json` 为 JSON。
    - **JSON 导入**：从 JSON 导入配置。
- **预设区二级 Tab**：Claude Code / Codex / OpenCode。其中 OpenCode 预设较特殊，再下设"预设管理 / 全局配置"三级切换。

Provider 与 Preset 的概念、字段含义与 `config.json` 结构详见 [./providers.md](./providers.md)。

---

## 扩展管理 `/extensions`（ExtensionsView）

管理 Claude 与 Codex 插件、工作区与环境变量。顶部页面描述："管理 Claude 与 Codex 插件、工作区与环境变量"。

页面结构：

- **一级 Pill 导航**：
    - **Plugins**：下方再有 ClaudeCode / Codex 二级下划线 Tab，分别展示已安装插件，支持添加市场（"Add Marketplace"对话框）。
    - **Workspaces**：工作区面板（多工作空间创建、插件部署、冲突检测）。
    - **Environment**：环境变量面板（用户自定义环境变量，写入 `~/.amagi-codebox/envvars.json`）。

插件系统对应后端 `internal/plugin` 与 `internal/codexplugin`；工作区对应 `internal/workspace`。

---

## 注入规则 `/rules`（RulesView）

管理 API 注入规则与代理状态。顶部页面描述："管理 API 注入规则与代理状态"。

页面核心元素：

- **代理控制卡片**：
    - **状态指示**：运行中 / 已停止（彩色圆点）。
    - **规则数量**：当前已配置的注入规则总数。
    - **本地端口**：注入代理监听的本地端口（默认 `5280`）。
    - **目标后端 URL**：下拉选择（来自历史记录）或输入，点击"保存"加入历史。
- **规则列表**：维护具体的关键字匹配规则与待注入的 Prompt 内容。

代理运行时不允许编辑端口与后端 URL，需先停止代理。后端实现位于 `internal/proxy`（"代理注入引擎"）。

---

## 环境检测 `/envcheck`（EnvCheckView）

CLI 工具安装状态、版本与 PATH 校验。顶部页面描述："CLI 工具安装状态、版本与 PATH 校验"。

视图本身是一个轻量包装器，主要内容来自子组件 `EnvCheckSettings.vue`。后端 `App.Startup` 会异步触发首次全量检测（`a.EnvCheck.CheckAll()`），检测结果缓存并由本页展示。检测到问题时，启动过程中会把问题条目作为 warning 上报。

页面通常提供：

- 各 CLI 工具的安装状态、版本号、可执行路径
- PATH 配置校验
- 一键修复（修复 PATH、安装工具、安装 Node.js 等，能力以平台与工具实际支持为准）

> 待核实：一键修复按钮的可见性条件与具体动作清单以应用内实际显示为准。

---

## 系统日志 `/logs`（LogsView）

查看应用运行日志与调试信息。顶部页面描述："查看应用运行日志与调试信息"。

页面另含一张 **Headroom 上下文压缩统计** 卡片，展示累计、全局 ledger 数据，每 10 秒自动刷新。指标包括：

- 累计压缩次数
- 累计节省 Token
- 累计节省比例

空态与错误态均以内联提示展示（不刷屏弹 toast）。Headroom 后端位于 `internal/headroom`。

---

## 启动一个会话

本节描述从"会话设置"页启动一个会话的标准流程。流程基于前端 `useSessionLaunch` composable 与后端 `app.go` 的 `LaunchSession` / `LaunchCodexSession` / `LaunchOpenCode` 三个入口方法。

### 前置条件

- 已选定引擎对应的 provider 与 preset（OpenCode 引擎允许 preset 为空，表示使用全局 `opencode.json`）。
- OpenCode 引擎要求工作目录必填；其他引擎未指定时使用默认路径（`internal/paths` 提供的默认路径或用户主目录）。
- 内嵌模式（`embedded`）需要平台支持（`EmbeddedTerminalSupported`），否则应改用外部模式。

### 操作步骤

1. 进入"会话设置"页（路由 `/`）。
2. 在顶部 Segmented 切换引擎：ClaudeCode / OpenCode / Codex。
3. 选择服务提供商。ClaudeCode 要求所选 provider 必须兼容 Anthropic 格式（`Provider.IsAnthropicCompatible()`）。
4. 选择预设。预设决定模型名与运行参数。
5. 选择启动模式：
    - `embedded`：会话将在 `/terminal` 页内嵌显示。
    - 外部模式（`terminal` 等）：会话在外部窗口或进程启动。
6. （仅 `embedded`）按需选择终端 Shell。
7. 设置工作目录（OpenCode 必填）。
8. 点击启动按钮。

### 启动后的行为

- **embedded 模式**：启动成功后自动跳转到 `/terminal`，新会话被设为活动会话并挂载 xterm 终端。
- **外部模式**：启动成功后留在当前页，并提示"会话已在外部启动"（不跳转 `/terminal`，避免展示空终端）。
- **启动失败**：弹出错误提示，不跳转。

### 三引擎对应的后端入口

| 引擎 | 后端方法（`app.go`） | 关键参数 |
|------|---------------------|----------|
| ClaudeCode | `LaunchSession` | `providerName, presetName, mode, workDir, useProxy, useHeadroom, shellPath` |
| Codex | `LaunchCodexSession` | `modelName, providerID, mode, workDir, shellPath` |
| OpenCode | `LaunchOpenCode` | `providerName, presetName, mode, workDir, shellPath` |

返回值为会话 ID（字符串）。会话状态、PID、启动时间等字段由 `internal/session.Session` 与 `SessionInfo` 承载（见 `internal/session/types.go`）。

---

## AppType 与会话状态

后端 `internal/session/types.go` 定义了应用类型与会话状态：

```go
type AppType string

const (
    AppTypeClaudeCode AppType = "claudecode"
    AppTypeOpenCode   AppType = "opencode"
    AppTypeCodex      AppType = "codex"
    // AppTypeAmagiCode is deprecated and retained only for reading legacy sessions.
    AppTypeAmagiCode AppType = "amagicode"
)
```

`AppTypeAmagiCode` 已废弃，仅用于读取历史会话，新创建会话不再使用。

会话状态（`SessionStatus`）：

- `running`：运行中
- `stopped`：已停止
- `exited`：已退出
- `failed`：启动/运行失败

启动模式（`LaunchMode`）：

- `terminal`：独立终端窗口
- `embedded`：内嵌终端（ConPTY + xterm.js）

---

## 已知限制与注意事项

- 外部模式启动的会话不在 `/terminal` 页显示，需通过外部窗口或会话列表查看。
- 视图层字段实际可选项与平台能力、当前配置密切相关，本篇不穷举具体取值。
- 远程控制 API（HTTP + WebSocket）独立于桌面 UI，由 `internal/remote` 提供，详见 `README.md` 的"远程控制 API"小节；端点签名与契约以 [../api.md](../api.md) 为准。
