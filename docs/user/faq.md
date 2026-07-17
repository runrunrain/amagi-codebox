# 常见问题

面向 Amagi CodeBox 的终端用户。本篇按能力与机制回答使用中常见的问题，不编造具体报错文案——实际提示文案以应用内显示为准。每条回答附源码或文档引用，便于深入排查。

相关参考：

- 安装与首次运行：[./installation.md](./installation.md)
- 界面功能总览：[./usage.md](./usage.md)
- 内嵌终端机制：[./terminal.md](./terminal.md)
- 插件系统：[./plugins.md](./plugins.md)
- 远程控制与移动端：[./remote-mobile.md](./remote-mobile.md)
- 安全策略：[../security.md](../security.md)

---

## 环境检测与一键修复

### 应用如何检测本机的 CLI 工具状态？

应用启动后异步扫描 Claude Code、OpenCode、Codex、Headroom 四种 CLI 工具（`internal/envcheck` 的 `CLITool`）。每种工具的检测结果（`CheckStatus`）包含：

| 字段 | 含义 |
|------|------|
| `installed` | 是否已安装 |
| `installMethod` | 安装方式：`native` / `npm` / `pip` / `homebrew` / `codebox-venv` / `unknown` |
| `version` / `latestVersion` / `hasUpdate` | 当前版本、最新版本、是否有更新 |
| `pathOk` / `systemPathOk` | Amagi CodeBox 能否启动该工具 / 系统 shell 能否直接调用 |
| `pathState` | 路径来源：`missing` / `system_path` / `codebox_path` / `shell_fallback` / `outside_path` |
| `pathSource` | 路径来源的人类可读描述 |
| `issues` | 结构化问题列表（含 severity / code / message / detail / solutions） |
| `solutions` | 可执行的修复动作 |
| `canInstall` / `canInstallByMethod` / `installBlockedReason` | 是否可安装，按方法分别报告 |
| `config` | 仅 Claude Code 填充，含配置项检查结果 |

用户入口：

- `/envcheck`（环境检测页）：查看所有工具状态。
- `App.RunEnvCheck()`：手动重新执行全量检测。
- `App.GetEnvCheckStatus()`：获取最近一次缓存结果。
- `App.GetEnvCheckSnapshot()`：获取"工具状态 + 当前异步操作"的合并快照，前端常用此接口轮询。

### "PATH OK" 与 "System PATH OK" 有什么区别？

`pathOk` 表示 Amagi CodeBox 内部能否启动该工具——平台解析器在系统 PATH 之上叠加了 baseline + caller 路径，因此即使系统 shell 看不到某 CLI，Amagi CodeBox 仍可能启动它（此时 `pathState == codebox_path`）。

`systemPathOk` 反映系统 shell 的可见性，等价于 `exec.LookPath` 在原始进程继承的 PATH 下能否找到命令。`pathState` 进一步区分：

| 取值 | 含义 |
|------|------|
| `missing` | 任何地方都找不到 |
| `system_path` | 系统 PATH 中可找到 |
| `codebox_path` | 仅 Amagi CodeBox 平台解析器的增强 PATH 中可找到 |
| `shell_fallback` | 通过 shell 登录探针（如 `zsh -ilc "command -v claude"`）找到 |
| `outside_path` | 找到了可执行文件，但不属于上述任何 PATH 来源 |

典型场景：用 nvm / homebrew 安装 CLI 后，shell 启动时才注入 PATH，GUI 启动的 Amagi CodeBox 进程看不到 → `pathOk=true / systemPathOk=false / pathState=codebox_path 或 shell_fallback`。这通常不影响使用，但若希望系统终端也能直接调用，需要修复 PATH（见下）。

### 一键修复都能做什么？

修复动作由 `App.RunEnvFixAction(action, tool, extraPath)` 触发，`action` 取自 `SolutionType` 白名单：

| 动作 | 含义 |
|------|------|
| `install_tool` | 安装指定工具（按 `tool` 字段） |
| `install_node` | 安装 Node.js（npm/native 安装 Claude Code 的前置） |
| `fix_path` | 修复 PATH 配置，让系统 shell 能直接找到 CLI |
| `restart_app` | 提示重启应用以使环境变更生效 |
| `retry` | 重试上一次失败的检测 |
| `manual_command` | 给出需要用户手动执行的命令（不自动执行） |
| `install_claude_method` | 按用户选择的 method 安装 Claude Code（npm 或 native） |
| `clean_claude_install` | 清理 Claude Code 安装（卸载） |
| `fix_claude_config` | 修复 Claude Code 单个配置项 |

修复结果（`FixActionResult`）会报告是否成功、是否真的发生变更（`Changed`）；变更成功后后台会自动触发一次 `CheckAll` 刷新缓存。

### Claude Code 有哪两种安装方式？怎么选？

`App.InstallClaudeWithMethod(method)` 接受 `"npm"` 或 `"native"`：

| method | 行为 | 适用 |
|--------|------|------|
| `npm` | `npm install -g @anthropic-ai/claude-code`（或类似 npm 全局安装） | 已有 Node.js 与 npm；想最简安装 |
| `native` | npm 全局安装后再执行 `claude install` 切换到原生安装 | 希望获得更快的启动速度与独立运行时（不依赖 Node.js） |

检测面板会按本机条件在 `canInstallByMethod` 中分别给出每种方法是否可用；前端按此启用/禁用对应按钮。如果某种方法不可用，`installBlockedReason` 会说明原因（如"未检测到 Node.js"）。

### 工具能否自动安装？

可以走异步安装：`App.StartInstallToolAsync(tool)` 或 `App.StartUpdateToolAsync(tool)` 立即返回 `OperationState`，安装在后台 goroutine 中执行，不受前端页面切换影响。前端通过 `App.GetEnvCheckOperationState()` 或快照中的 `operation` 字段轮询进度：

- `status`：`idle` / `running` / `succeeded` / `failed` / `timeout`
- `step`：`precheck` / `prepare` / `run_command` / `verify` / `refresh_cache` / `completed`
- `progress`：百分比
- `result`：成功后的 `InstallResult`（含版本号）
- `error`：失败原因

同步版 `InstallTool(tool)` / `UpdateTool(tool)` 仍保留，但前端建议使用异步版本避免阻塞 UI。

---

## Claude Code 安装

### 为什么检测不到已安装的 Claude Code？

可能原因（按 `PathState` 与 `PathSource` 排查）：

1. **PATH 未注入 GUI 进程**：从 Finder / Dock 启动 Amagi CodeBox 时不会执行 shell 启动脚本，nvm / homebrew 的 PATH 注入失效。修复：`fix_path` 动作会把已知 CLI 路径写入 Amagi CodeBox 的 baseline PATH，或按提示手动调整系统级 PATH。
2. **Shell fallback**：macOS 上检测器会额外用 `zsh -ilc "command -v claude"` 探针兜底；命中后 `pathState=shell_fallback`，工具可用但系统终端需另行配置。
3. **安装方式与 PATH 不匹配**：用 npm 安装但 npm 全局目录不在 PATH 中；或用 native 安装但 Claude 的安装路径不在 PATH 中。修复：重新通过检测面板的修复入口处理，或卸载后用同一 method 重装。
4. **配置项缺失**：Claude Code 自身的 `~/.claude/settings.json` 等配置文件缺少必要项（`ClaudeConfigItem`）。`config.missingRequired` 标识必要项缺失；`fix_claude_config` 动作可单项修复。

### npm 与 native 两种方式可以混装吗？

不推荐。检测面板会标记安装方式（`installMethod`），并在 `solutions` 中给出针对性建议。`clean_claude_install` 动作会清理已有安装以便换方式重装；该动作的 `method` 字段会显式指定要清理的通道（避免误清另一个通道）。

> 重要：UI 中触发清理时，前端按 `solutions[].method` 而不是按 `CheckStatus.installMethod` 决定清理通道，因为后者可能为空或陈旧。这一约定见 `ResolutionAction.Method` 字段注释。

---

## 单实例与窗口行为

### 为什么打不开第二个窗口？

仅 **Windows** 有单实例保护（`internal/platform/single_instance_windows.go`）：

- 启动时调用 `CreateMutexW` 创建命名互斥体 `amagi-codebox-single-instance-mutex`。
- 若互斥体已存在（`ERROR_ALREADY_EXISTS = 183`），新进程认为已有实例运行，转而激活已有窗口：
    - `FindWindowW` 按窗口标题 `Amagi CodeBox` 查找。
    - 最小化时先 `ShowWindow(SW_RESTORE)`，否则 `ShowWindow(SW_SHOW)`。
    - `SetForegroundWindow` 提到前台。
- 新进程随后自行退出。

### macOS 上为什么能打开第二个实例？

`internal/platform/single_instance_nonwindows.go` 在 `!windows` 构建标签下是 stub，`EnsureSingleInstance` 直接返回 `true`，不做互斥检测。也就是说 macOS 当前允许启动多个实例，多实例之间的状态协调未实现（共用同一 `~/.amagi-codebox/` 配置目录，并发写入可能出现竞争）。

> 如不希望 macOS 多开，请自行避免双击多次启动；这一限制详见 [./installation.md#单实例保护](./installation.md#单实例保护)。

---

## 系统托盘与退出

### 关闭按钮为什么有时是"隐藏"，有时是"退出"？

行为由平台能力 `HideOnCloseSupported` 与设置项 `CloseAction` 共同决定（`internal/platform/capabilities*.go` 与 `internal/tray`）：

- `HideOnCloseSupported == true` 且 `CloseAction == "hide"`：关闭按钮隐藏窗口，应用继续在后台运行。
- 否则：关闭按钮触发正常退出。

### 怎样彻底退出应用？

当系统托盘被启用时（`SystemTraySupported == true` 且托盘图标资源就绪），托盘菜单提供：

- 状态："状态: 就绪"
- 显示窗口
- 隐藏窗口
- **退出**（完全退出应用）

仅靠关闭按钮（在 hide 模式下）不会退出，需要从托盘菜单选择"退出"。如果当前平台未启用托盘，关闭按钮即等同于退出。

> 待核实：macOS 平台的 `SystemTraySupported` 与 `HideOnCloseSupported` 当前默认值，需以 `internal/platform/capabilities_runtime.go` 实际分发为准。

---

## 配置目录与密钥

### 配置文件都在哪里？

统一存放在用户主目录下的 `~/.amagi-codebox/`（`app.go` 的 `defaultConfigDir()`）。常见文件：

| 文件 | 用途 |
|------|------|
| `config.json` | 提供商与预设配置 |
| `secrets.json` | 加密存储的 API 密钥 |
| `settings.json` | 应用设置（远程端口、shell 路径、GitHub Token 等） |
| `envvars.json` | 自定义环境变量 |
| `settings_amagi.json` | Amagi 模型配置 |
| `global-enabled.json` | 全局启用的插件列表 |
| `plugin-subitems.json` | Claude 插件子项禁用列表 |
| `workspaces.json` | 工作空间列表 |
| `global-deploy-manifest.json` | 全局部署清单 |

首次启动若加载失败，应用回退到内置默认配置并记入日志，不阻断启动。

### API 密钥是怎么保存的？

密钥不在源码、日志或明文配置中。具体存储方式由平台决定，详见 [../security.md](../security.md)：

- Windows：DPAPI 加密。
- macOS：Keychain。
- 其他平台：明文 fallback（不推荐用于生产）。

`/api/secrets/diagnostics`（远程）与桌面端的密钥诊断入口可以查看密钥存储是否正常、是否能正确解密，但不暴露密钥本身。

### 可以手工编辑配置文件吗？

不推荐。这些文件由对应 Service 层维护，部分文件（如 `global-enabled.json`、`workspaces.json`、`global-deploy-manifest.json`）之间存在契约（部署清单的 checksum、`GlobalEnabled` 与工作空间归属关系等），手工编辑可能破坏一致性。建议：

- 提供商/预设：用"Provider Center"页（`/provider`）的导入/导出功能。
- 插件子项：用扩展管理页（`/extensions`）。
- 工作空间：用对应 UI。
- 远程端口/host：用设置页或远程 API `PUT /api/settings`。

> 备份与迁移：复制整个 `~/.amagi-codebox/` 目录即可（但 `secrets.json` 的加密绑定到原机器的 OS 凭据，跨机器迁移后密钥需要重新录入）。

---

## 终端相关

### 切换会话时为什么有短暂"卡顿"或"黑屏"？

历史回放机制导致的正常现象。每次切换会话时，前端会向后端请求最多 1 MB 的输出历史，按 64 KB 分块、每块让出一帧写入 xterm，避免阻塞主线程。1 MB 历史的回放约需数百毫秒，期间视觉上可能表现为"渐入"。

> 切换回来时若出现持续黑屏或撕裂，可能是 Canvas/WebGL 渲染器的纹理图集在中间态尺寸下构建。`useTerminalEngine.ts` 在 renderer 加载与历史回放完成两个时机都会显式 `clearTextureAtlas` 并强制 fit 以纠正；仍异常时建议重新打开终端页。

### 为什么 macOS 下 WebGL 不可用？

WKWebView 下 xterm.js 的 WebGL addon 会损坏 scrollback 纹理图集（历史回放后出现花屏）。因此 Amagi CodeBox 在 macOS 强制使用 Canvas addon：它渲染到单一 `<canvas>`，避开 GPU 纹理路径，同时远快于默认 DOM 渲染。详见 [./terminal.md#渲染器选择策略](./terminal.md#渲染器选择策略)。

### 为什么粘贴长文本时是"一段一段"出现的？

大于 1024 字节的粘贴走 `PtyWriteLarge`，后端按 1 KB 分块写入、块间 sleep 10 ms，避免 ConPTY 输入缓冲区溢出导致截断。这是有意的限流，不是 bug。

---

## 远程控制相关

### 启用远程控制后，其他设备如何接入？

详见 [./remote-mobile.md](./remote-mobile.md)。要点：

1. 桌面端启用远程控制（默认 `0.0.0.0:8680`）。
2. 其他设备打开移动端 Web UI 或 Android App，填入桌面端的局域网 IP 与 Token。
3. Token 在桌面端的远程设置页查看（`App.GetRemoteToken()`）。

桌面浏览器可在桌面端通过 `App.OpenRemoteWebUI()` 直接打开内置 Web UI，自动通过 launch grant 换 cookie，无需手工输入 Token。

### Token 丢失怎么办？

Token 只能通过桌面端重新生成（`App.RegenerateRemoteToken()`），没有 REST 端点可远程重置。重新生成后旧 Token 立即失效，所有已连接客户端需要更新。

### 远程端能看到桌面终端的全部输出吗？

能看到最近 1 MB 的历史输出 + 后续实时输出。1 MB 之前的更早内容会被环形缓冲区裁剪（在 UTF-8 / ANSI 安全边界处切断）。移动端 Observer 模式不会改变桌面端 PTY 尺寸。

---

## 已知限制汇总

- macOS 没有单实例保护，可以多实例启动，多实例共用配置目录有写入竞争风险。
- Codex 插件不支持子项级禁用（UI 上展示但实际 no-op），详见 [./plugins.md](./plugins.md#codex-子项禁用当前限制)。
- 远程 API 当前不提供 HTTPS，跨公网部署必须在反向代理层启用 TLS。
- Linux/其他非 Windows、非 macOS 平台未实现 PTY 后端。
- 字体硬编码在前端，没有 UI 修改入口（待核实是否会开放）。
- macOS 单实例保护与 Windows 不对等，详见 [./installation.md#已知限制](./installation.md#已知限制)。
