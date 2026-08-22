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

应用启动后异步扫描六种 CLI 工具（`internal/envcheck/types.go` 的 `CLITool` 枚举）：**Claude Code（`claude_code`）、OpenCode、Codex、Pi、OMP、Headroom**。每种工具的检测结果（`CheckStatus`）包含：

| 字段 | 含义 |
|------|------|
| `installed` | 是否已安装 |
| `installMethod` | 安装方式：`native` / `npm` / `pip` / `homebrew` / `codebox-venv` / `unknown` |
| `version` / `latestVersion` / `hasUpdate` | 当前版本、最新版本、是否有更新 |
| `pathOk` / `systemPathOk` | Amagi CodeBox 能否启动该工具 / 系统 shell 能否直接调用 |
| `pathState` | 路径来源：`missing` / `system_path` / `codebox_path` / `shell_fallback` / `outside_path` |
| `issues` | 结构化问题列表（含 severity / code / message / solutions） |
| `solutions` | 可执行的修复动作 |
| `canInstall` / `canInstallByMethod` / `installBlockedReason` | 是否可安装，按方法分别报告 |
| `config` | 仅 Claude Code 填充，含配置项检查结果 |

用户入口：

- `/envcheck`（环境检测页）：查看所有工具状态；Windows 用户还可看到"WSL 内的 CLI"卡片（把 CLI 安装进 WSL，见 [./installation.md](./installation.md#wsl-内的-cliwindows-特有)）。
- `App.RunEnvCheck()`：手动重新执行全量检测。
- `App.GetEnvCheckSnapshot()`：获取"工具状态 + 当前异步操作"的合并快照，前端常用此接口轮询。

### "PATH OK" 与 "System PATH OK" 有什么区别？

`pathOk` 表示 Amagi CodeBox 内部能否启动该工具——平台解析器在系统 PATH 之上叠加了 baseline + caller 路径，因此即使系统 shell 看不到某 CLI，Amagi CodeBox 仍可能启动它（此时 `pathState == codebox_path`）。

`systemPathOk` 反映系统 shell 的可见性，等价于 `exec.LookPath` 在原始进程继承的 PATH 下能否找到命令。`pathState` 进一步区分：`missing`（任何地方都找不到）、`system_path`（系统 PATH 可找到）、`codebox_path`（仅 CodeBox 增强 PATH 可找到）、`shell_fallback`（通过 shell 登录探针找到）、`outside_path`（找到可执行文件但不属于上述任何来源）。

典型场景：用 nvm / homebrew 安装 CLI 后，shell 启动时才注入 PATH，GUI 启动的 Amagi CodeBox 进程看不到 → `pathOk=true / systemPathOk=false`。这通常不影响使用，但若希望系统终端也能直接调用，需要修复 PATH。

### 一键修复都能做什么？

修复动作由 `App.RunEnvFixAction(action, tool, extraPath)` 触发，`action` 取自 `SolutionType` 白名单：`install_tool`（安装指定工具）、`install_node`（安装 Node.js）、`fix_path`（修复 PATH）、`restart_app`（提示重启）、`retry`（重试检测）、`manual_command`（给出需手动执行的命令）、`install_claude_method`（按 method 安装 Claude Code）、`clean_claude_install`（清理 Claude Code 安装）、`fix_claude_config`（修复 Claude Code 单个配置项）。

修复结果（`FixActionResult`）会报告是否成功、是否真的发生变更（`Changed`）；变更成功后后台自动触发一次 `CheckAll` 刷新缓存。

### 工具能否自动安装？

可以走异步安装：`App.StartInstallToolAsync(tool)` / `App.StartUpdateToolAsync(tool)` 立即返回 `OperationState`，安装在后台 goroutine 中执行。前端通过 `App.GetEnvCheckOperationState()` 或快照轮询进度（`status`：`idle/running/succeeded/failed/timeout`；`step`：`precheck/prepare/run_command/verify/refresh_cache/completed`）。同步版 `InstallTool(tool)` / `UpdateTool(tool)` 仍保留，但前端建议使用异步版本避免阻塞 UI。

---

## Claude Code 安装

### 为什么检测不到已安装的 Claude Code？

可能原因（按 `PathState` 与 `PathSource` 排查）：

1. **PATH 未注入 GUI 进程**：从 Finder / Dock 启动时不会执行 shell 启动脚本，nvm / homebrew 的 PATH 注入失效。修复：`fix_path` 动作会把已知 CLI 路径写入 CodeBox 的 baseline PATH。
2. **Shell fallback**：macOS 上检测器会额外用 `zsh -ilc "command -v claude"` 探针兜底；命中后 `pathState=shell_fallback`，工具可用但系统终端需另行配置。
3. **安装方式与 PATH 不匹配**：用 npm 安装但 npm 全局目录不在 PATH 中；或 native 安装路径不在 PATH 中。
4. **配置项缺失**：Claude Code 自身的配置文件缺少必要项；`config.missingRequired` 标识必要项缺失，`fix_claude_config` 动作可单项修复。

### npm 与 native 两种方式可以混装吗？

不推荐。`App.InstallClaudeWithMethod(method)` 接受 `"npm"` 或 `"native"`：npm 走全局 npm 安装；native 在 npm 安装后再执行 `claude install` 切换到原生安装（更快启动、不依赖 Node.js 运行时）。`clean_claude_install` 动作会清理已有安装以便换方式重装，其 `method` 字段显式指定要清理的通道（避免误清另一个通道）。

> 重要：UI 触发清理时按 `solutions[].method` 而不是 `CheckStatus.installMethod` 决定清理通道，因为后者可能为空或陈旧。

---

## 单实例与窗口行为

### 为什么打不开第二个窗口？

仅 **Windows** 有单实例保护（`internal/platform/single_instance_windows.go`）：启动时 `CreateMutexW` 创建命名互斥体 `amagi-codebox-single-instance-mutex`；若已存在，新进程激活已有窗口（`FindWindowW` 按标题 `Amagi CodeBox` 查找，最小化时先恢复，再 `SetForegroundWindow`），随后自行退出。

### macOS 上为什么能打开第二个实例？

`internal/platform/single_instance_nonwindows.go` 在 `!windows` 构建标签下是 stub，`EnsureSingleInstance` 直接返回 `true`。macOS 当前允许启动多个实例，多实例共用同一 `~/.amagi-codebox/` 配置目录，并发写入可能出现竞争——请自行避免双击多次启动。

---

## 系统托盘与退出

### 关闭按钮为什么有时是"隐藏"，有时是"退出"？

行为由平台能力决定（`internal/platform/capabilities_runtime.go`）：

- **Windows**：`SystemTraySupported=true`、`HideOnCloseSupported=true`、`CloseAction=hide`——关闭按钮隐藏窗口，应用继续后台运行，托盘菜单提供"显示窗口 / 隐藏窗口 / 退出"。
- **macOS**：上述能力位均为 false、`CloseAction=quit`——无托盘，关闭按钮即正常退出。

### 怎样彻底退出应用？

Windows 上仅靠关闭按钮（hide 模式）不会退出，需从托盘菜单选择"退出"。macOS 上关闭窗口即退出。

---

## 配置目录与密钥

### 配置文件都在哪里？

统一存放在 `~/.amagi-codebox/`（`app.go` 的 `defaultConfigDir()`）。主要文件：

| 文件 / 目录 | 用途 |
|-------------|------|
| `models.json` | 提供商与公共预设配置（注意：不叫 `config.json`） |
| `secrets.enc` | 平台保护的 API 密钥（注意：不叫 `secrets.json`） |
| `settings.json` | 应用设置（远程端口、shell 选择、终端 scrollback 等） |
| `paths.json` / `envvars.json` | 工作路径 / 自定义环境变量 |
| `agent-profiles.json` | Agent 配置档 |
| `devices.json` / `remote-hosts.json` | 远程可信设备登记簿 / 远程客户端主机登记簿 |
| `usage.db` / `usage-pricing.json` | 用量统计 SQLite 库 / 模型价格表 |
| `plugin-subitems.json` | Claude 插件子项禁用列表 |
| `skins/` / `logs/` | 皮肤资源 / 运行日志 |

首次启动若加载失败，应用回退到内置默认配置并记入日志，不阻断启动。**新安装不会预置任何提供商或终端预设**。

### API 密钥是怎么保存的？

密钥不在源码、日志或明文配置中。存储方式由平台决定（`internal/secrets/`）：

- Windows：DPAPI 加密（`store_windows.go`）。
- macOS：Keychain（`store_darwin_cgo.go` / `store_darwin_nocgo.go`）。
- **其他平台（Linux 等）：不支持密钥存储**（`store_other.go` 是 no-op stub：`Load` 返回空表、`Save` 静默丢弃，**没有**明文 fallback）。在这些平台上保存的密钥不会持久化。

`/api/secrets/diagnostics`（legacy，仅 loopback）与桌面端的密钥诊断入口可以查看密钥存储是否正常，但不暴露密钥本身。

### 可以手工编辑配置文件吗？

不推荐。这些文件由对应 Service 层维护，手工编辑可能破坏配置结构或原子写入约定。建议用应用内入口：Provider Center（`/provider`）的导入/导出、扩展管理页（`/extensions`）、设置页。

> 备份与迁移：复制整个 `~/.amagi-codebox/` 目录即可，但 `secrets.enc` 的加密绑定原机器的 OS 凭据，跨机器迁移后密钥需要重新录入。推荐使用 Provider Center 的"导出完整配置"做迁移。

---

## 终端相关

### 切换会话时为什么有短暂"卡顿"或"黑屏"？

历史回放机制导致的正常现象。每次切换会话时，前端向后端请求最多 1 MB 的输出历史，按 64 KB 分块、每块让出一帧写入 xterm，避免阻塞主线程。1 MB 历史的回放约需数百毫秒，期间视觉上可能表现为"渐入"。

> 切换回来时若出现持续黑屏或撕裂，可能是渲染器的纹理图集在中间态尺寸下构建。`useTerminalEngine.ts` 在 renderer 加载与历史回放完成两个时机都会显式 `clearTextureAtlas` 并强制 fit 以纠正；仍异常时建议重新打开终端页。

### 为什么 macOS 下 WebGL 不可用？

WKWebView 下 xterm.js 的 WebGL addon 会损坏 scrollback 纹理图集（历史回放后花屏），而已发布的 CanvasAddon 只声明兼容 xterm 5（本项目用 xterm 6）。因此 macOS 使用 xterm 6 内置 DOM 渲染器。详见 [./terminal.md](./terminal.md#渲染器选择策略)。

### 为什么粘贴长文本时是"一段一段"出现的？

大于 1024 字节的粘贴走 `PtyWriteLarge`，后端按 1 KB 分块写入、块间 sleep 10 ms，避免 ConPTY 输入缓冲区溢出导致截断。这是有意的限流，不是 bug。

### Windows 上终端为什么进的是 Linux 环境？

Windows 平台默认终端 Shell 为 WSL（`DefaultShellKey = "wsl"`）。如需在 Windows 原生环境运行，可在会话设置页把 Shell 切到 PowerShell/cmd，或直接用"直接启动"。CLI 未装进 WSL 时，用"环境检测"页的"WSL 内的 CLI"卡片一键安装。

---

## 远程控制相关

### 启用远程控制后，其他设备如何接入？

详见 [./remote-mobile.md](./remote-mobile.md)。要点：

1. 桌面端"设置 → 远程控制"启用远程服务。默认监听 `127.0.0.1:8680`（仅本机）；局域网接入需在 LAN 暴露确认卡中确认后把 host 切为 `0.0.0.0`。
2. 发起短时配对窗口，用手机系统相机扫描桌面端二维码；页面自动提交一次性配对码完成设备配对。
3. 配对成功后设备获得 HttpOnly Cookie，进入会话大厅。设备登记在"可信设备卡"可见，可随时撤销。

注意：局域网设备**不能**再用 Bearer Token 访问 legacy `/api/*` 端点——这些端点现在只接受本机回环连接（非回环一律 403），LAN 设备必须走 v1 配对流程。

### Token 丢失怎么办？

legacy Token 只能通过桌面端重新生成（`App.RegenerateRemoteToken()`），没有 REST 端点可远程重置。重新生成后旧 Token 立即失效。注意该 Token 仅对 loopback 本机访问有效；移动端设备凭据与 Token 无关，设备丢失应在"可信设备卡"撤销对应设备。

### 远程端能看到桌面终端的全部输出吗？

能看到最近 1 MB 的历史输出 + 后续实时输出；更早的内容被环形缓冲区裁剪。移动端通过 `/ws/v1` 流式解码 UTF-8 输出，字符跨 WebSocket 帧也能完整显示。

---

## 已知限制汇总

- macOS 没有单实例保护与系统托盘，可以多实例启动，多实例共用配置目录有写入竞争风险。
- macOS 官方 Release 仅 arm64 包；Intel Mac 需自行构建。
- Linux 等其他平台：PTY 后端未实现、密钥存储为 no-op（不持久化）。
- Codex 插件不支持子项级禁用（UI 上展示但实际 no-op），详见 [./plugins.md](./plugins.md#codex-子项禁用当前限制)。
- 远程 API 当前不提供 HTTPS，跨公网部署必须在反向代理层启用 TLS。
- 终端字体硬编码在前端，没有 UI 修改入口（待核实是否会开放）。
