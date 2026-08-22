# 下载安装与首次运行

面向 Amagi CodeBox 的终端用户（Windows 与 macOS 桌面使用者）。介绍如何获取、安装并完成首次启动，以及启动过程中发生的目录与进程行为。

相关参考：

- 界面功能总览：[./usage.md](./usage.md)
- 提供商与预设配置：[./providers.md](./providers.md)
- 安全机制（API 密钥加密存储等）：[../security.md](../security.md)

---

## 环境要求

| 平台 | 要求 | 说明 |
|------|------|------|
| Windows | Windows 10 1903+（x86_64） | 内嵌终端基于 ConPTY；默认终端 Shell 为 WSL（无可用 WSL 发行版时回退 pwsh/powershell/cmd，见 `internal/platform/capabilities_runtime.go`） |
| macOS | Apple Silicon（arm64） | 内嵌终端基于 creack/pty；官方 Release 当前只构建 darwin/arm64 包（见 `.github/workflows/release.yml`） |

桌面应用本身不需要用户预装 Go、Node.js 或 Wails CLI，这些仅在使用源码自行构建时需要（详见 `README.md` 的"构建命令"小节）。但请注意：**受管的五种 CLI（Claude Code / OpenCode / Codex / Pi / Oh My Pi）需要另行安装**，应用不内置这些工具。

环境检测页（应用内 `/envcheck`）会在启动后异步扫描本机已安装的 CLI 工具及其 PATH 配置，详见 [./usage.md](./usage.md#环境检测-envcheck)。

---

## 下载渠道

唯一官方下载渠道为 GitHub Releases：

```text
https://github.com/runrunrain/amagi-codebox/releases
```

Release 资产按以下命名约定出包（见 `.github/workflows/release.yml`）：

- Windows：`amagi-codebox-<版本>-windows-amd64.zip`，内含 `amagi-codebox.exe`
- macOS：`amagi-codebox-<版本>-darwin-arm64.zip`，内含 `amagi-codebox.app`

当前发行版本号以 Releases 页与 `wails.json` 的 `info.productVersion` 为准（撰写本文时为 `1.3.50`）。应用内通过 GitHub Releases 检测新版本（"设置 → 软件更新"页）；Windows 支持应用内一键下载安装，macOS 的一键安装仅在 arm64 构建上启用（`UpdateInstallSupported`，见 `internal/platform/capabilities_runtime.go`；自动更新后端为 `internal/updater`）。

---

## Windows 安装

1. 从 Releases 下载 `amagi-codebox-<版本>-windows-amd64.zip` 并解压。
2. 运行其中的 `amagi-codebox.exe`。
3. 首次启动时，Windows 可能弹出 SmartScreen / Defender 提示（未签名或未积累声誉时常见）。确认发行方可信后选择"仍要运行"。

> 待核实：仓库 `build/windows/installer/project.nsi` 保留了 NSIS 安装包脚本，但当前 Release 工作流只产出 ZIP，不产出 MSI/NSIS 安装器。

### WSL 内的 CLI（Windows 特有）

Windows 上 CodeBox 默认把终端会话启动到 WSL（Linux）环境中。CLI 需要安装在 WSL 内部才能被默认会话使用：

- 打开"环境检测"页（`/envcheck`），其中的 **WSL 内的 CLI** 卡片（`frontend/src/views/settings/WSLCLISettings.vue`）会检测 WSL 状态与发行版版本（WSL1/WSL2），并提供"安装到 WSL"按钮，把 Claude Code / OpenCode / Codex / Pi / OMP 装进 WSL 内部（必要时先安装 Node 运行时）。
- 后端实现为 `internal/wslsetup`（Windows 构建下为 `service_windows.go`，其他平台为 no-op stub）。

## macOS 安装

1. 从 Releases 下载 `amagi-codebox-<版本>-darwin-arm64.zip` 并解压。
2. 将解压出的 `amagi-codebox.app` 拖入"应用程序"文件夹。
3. 在"访达 → 应用程序"中启动 Amagi CodeBox。

首次启动若被 Gatekeeper 拦截（"无法打开，因为它来自身份不明的开发者"），前往"系统设置 → 隐私与安全性"，点击"仍要打开"放行。

> 待核实：是否提供 Apple Developer 签名 / 公证（notarized）。当前未在 Release 工作流中核实到签名步骤。

---

## 单实例保护

应用启动时会调用单实例保护逻辑（`internal/platform.EnsureSingleInstance`，传入互斥名 `amagi-codebox-single-instance-mutex`、窗口标题 `Amagi CodeBox`）。平台行为差异如下：

- **Windows**：通过 Win32 `CreateMutexW` 创建命名互斥体。若互斥体已存在（错误码 `ERROR_ALREADY_EXISTS = 183`），认为已有实例运行，新进程将激活已有窗口（`FindWindowW` + `SetForegroundWindow`，最小化时先恢复），随后自行退出。
- **macOS**：当前实现为占位 stub，直接返回 `true`，不进行互斥检测。也就是说，macOS 上可以启动多个实例；多实例共用同一 `~/.amagi-codebox/` 配置目录，状态未做协调。

源码引用：`internal/platform/single_instance_windows.go`、`internal/platform/single_instance_nonwindows.go`。平台能力位为 `SingleInstanceSupported`（Windows=true，macOS=false）。

---

## 系统托盘驻留与关闭行为

托盘与关闭行为由平台能力（`platform.CurrentCapabilities()`，`internal/platform/capabilities_runtime.go`）决定：

| 能力位 | Windows | macOS |
|--------|---------|-------|
| `SystemTraySupported`（系统托盘） | 支持 | 不支持 |
| `HideOnCloseSupported`（关闭即隐藏） | 支持 | 不支持 |
| `CloseAction` 默认值 | `hide` | `quit` |
| `BackgroundResidentSupported`（后台驻留） | 支持 | 不支持 |

- **Windows**：启用系统托盘（`internal/tray.Service`），托盘菜单含"状态: 就绪 / 显示窗口 / 隐藏窗口 / 退出"。点击窗口关闭按钮默认隐藏窗口而非退出，应用继续在后台运行；彻底退出需从托盘菜单选择"退出"。
- **macOS**：不启用托盘，点击关闭按钮即正常退出。

---

## 配置目录

应用配置统一存放在用户主目录下的 `~/.amagi-codebox/`，由 `defaultConfigDir()` 解析（`app.go`）。目录生成时机：应用启动钩子 `App.Startup` 依次调用各服务的 `Load()`，首次写入时创建对应文件（加载失败会回退到内置默认配置并记入日志，不阻断启动）。

目录下的主要文件与子目录（按各服务源码与实际安装核实）：

| 文件 / 目录 | 写入方 | 用途 |
|-------------|--------|------|
| `models.json` | `internal/config` | 提供商、公共预设（terminal_presets）、OpenCode 预设等核心配置 |
| `secrets.enc` | `internal/secrets` | 经平台机制保护的 API 密钥（Windows DPAPI / macOS Keychain；详见 [../security.md](../security.md)） |
| `settings.json` | `internal/settings` | 应用设置（远程端口、Shell 选择、终端 scrollback 等） |
| `paths.json` | `internal/paths` | 自定义工作路径 |
| `envvars.json` | `internal/envvars` | 用户自定义环境变量 |
| `agent-profiles.json` | `internal/agentprofile` | Agent 配置档（CLI 模型配置多档保存与切换） |
| `devices.json` | `internal/remote` | 远程控制可信设备登记簿（配对设备） |
| `remote-hosts.json` | `app_remoteclient.go` | 远程客户端的主机登记簿（作为客户端连接其他 CodeBox 实例） |
| `usage.db` | `internal/usage` | 用量统计 SQLite 数据库（含 `-wal` / `-shm` 伴生文件） |
| `usage-pricing.json` | `internal/usage` | 模型价格表（输入/输出/缓存单价） |
| `plugin-subitems.json` | `internal/plugin` | Claude 插件子项禁用列表 |
| `injection-rules.json` | 注入规则存储 | Headroom 相关注入规则 |
| `skins/` | `internal/skins` | 外观皮肤资源 |
| `logs/` | `internal/logging` | 应用运行日志（按日期切分） |

> 注意：旧文档中"配置存于 `config.json` + `secrets.json`"的说法已过时——核心配置文件现名 `models.json`，密钥文件现名 `secrets.enc`。

建议不要手工编辑这些文件；如需迁移配置，请使用应用内 Provider Center 的"导出/导入完整配置"功能（详见 [./providers.md](./providers.md#导入--导出)）。注意密钥经平台机制加密、绑定原机器凭据，跨机器迁移后需在目标设备重新录入。

---

## 首次运行验证清单

完成安装后，可通过以下方式确认应用正常：

1. 启动应用，进入默认的"会话设置"页（路由 `/`）。
2. 前往"环境检测"页（`/envcheck`），确认本机 CLI 工具状态；Windows 用户可在同页把 CLI 安装进 WSL。
3. 前往"Provider Center"（`/provider`），新增自己的服务提供商，或导入从旧设备导出的完整配置。**新安装不会预置任何服务提供商或终端预设**（`internal/config/defaults.go` 的 `DefaultConfig()` 返回空的 `Models` 表）。
4. 在选定提供商中填入 API 密钥并保存（密钥经 OS 加密机制保护后写入 `secrets.enc`）。
5. 回到"会话设置"页，选择引擎（Claude Code / Pi / OpenCode / Oh My Pi / Codex）、提供商、预设与工作目录，启动首个会话。

启动会话的具体操作流程见 [./usage.md](./usage.md#启动一个会话)。

---

## 已知限制

- macOS 无单实例保护与系统托盘：允许多实例启动，关闭窗口即退出。
- macOS 官方包仅 arm64；Intel Mac 需自行从源码构建。
- 内嵌终端在平台不支持时会自动回退为外部终端模式（由平台能力位控制；macOS 的 `StandaloneTerminalSupported` 为 false）。
- 应用未携带代码签名（待核实），首次启动需在系统安全设置中放行。
