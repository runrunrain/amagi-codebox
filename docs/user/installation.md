# 下载安装与首次运行

面向 Amagi CodeBox 的终端用户（Windows 与 macOS 桌面使用者）。介绍如何获取、安装并完成首次启动，以及启动过程中发生的目录与进程行为。

相关参考：

- 界面功能总览：[./usage.md](./usage.md)
- 提供商与预设配置：[./providers.md](./providers.md)
- 安全机制（API 密钥加密存储等）：[../security.md](../security.md)

---

## 环境要求

| 平台 | 最低系统版本 | 说明 |
|------|-------------|------|
| Windows | Windows 10 1903+ | 安装包为 MSI / EXE；内嵌终端基于 ConPTY |
| macOS | macOS 10.15+ | 安装包为 DMG；内嵌终端基于 creack/pty |

桌面应用本身不需要用户预装 Go、Node.js 或 Wails CLI，这些仅在使用源码自行构建时需要（详见 `README.md` 的"构建命令"小节）。

环境检测页（应用内 `/envcheck`）会在启动后异步扫描本机已安装的 CLI 工具（如 Claude Code、OpenCode、Codex 等）及其 PATH 配置，详见 [./usage.md](./usage.md#环境检测-envcheck)。

---

## 下载渠道

唯一官方下载渠道为 GitHub Releases：

```text
https://github.com/runrunrain/amagi-codebox/releases
```

各平台产物（按 Release 资产命名约定）：

- Windows：MSI 安装包或 EXE 可执行文件
- macOS：DMG 镜像文件

当前发行版本号以 Releases 页与 `wails.json` 的 `info.productVersion` 为准（撰写本文时为 `1.2.80`）。应用内通过 GitHub Releases 检测新版本，支持 Windows 与 macOS 一键下载安装（自动更新由 `internal/updater` 提供，能力受平台支持度控制）。

> 待核实：各 Release 的具体资产文件名、ARM64 / x86_64 是否分别出包，需以 Releases 页实际公布为准。

---

## Windows 安装

1. 从 Releases 下载 MSI 或 EXE 安装包。
2. 双击运行安装程序，按向导完成安装。
3. 安装完成后，从开始菜单启动 "Amagi CodeBox"。

首次启动时，Windows 可能弹出 SmartScreen / Defender 提示（未签名或未积累声誉时常见）。确认发行方可信后选择"仍要运行"。

---

## macOS 安装

1. 从 Releases 下载 DMG 文件。
2. 双击挂载 DMG，将 "Amagi CodeBox" 拖入"应用程序"文件夹。
3. 在"访达 → 应用程序"中启动 Amagi CodeBox。

首次启动若被 Gatekeeper 拦截（"无法打开，因为它来自身份不明的开发者"），前往"系统设置 → 隐私与安全性"，点击"仍要打开"放行。

> 待核实：是否提供 Apple Developer 签名 / 公证（notarized）。当前未在源码中核实到签名配置。

---

## 单实例保护

应用启动时会调用单实例保护逻辑（`internal/platform.EnsureSingleInstance`，传入互斥名 `amagi-codebox-single-instance-mutex`、窗口标题 `Amagi CodeBox`）。平台行为差异如下：

- **Windows**：通过 Win32 `CreateMutexW` 创建命名互斥体。若互斥体已存在（错误码 `ERROR_ALREADY_EXISTS = 183`），认为已有实例运行，新进程将激活已有窗口（`FindWindowW` + `SetForegroundWindow`，最小化时先恢复），随后自行退出。
- **macOS / 其他平台**：当前实现为占位 stub，直接返回 `true`，不进行互斥检测。也就是说，macOS 上目前可以启动多个实例；多实例行为未做协调。

源码引用：`internal/platform/single_instance_windows.go`、`internal/platform/single_instance_nonwindows.go`。

---

## 系统托盘驻留

应用启动时会读取平台能力（`platform.CurrentCapabilities()`）。仅当 `SystemTraySupported == true` 且托盘图标资源就绪时，才启用系统托盘（`internal/tray.Service`）。

托盘菜单（依据 `internal/tray/service.go`）：

- 状态："状态: 就绪"
- 显示窗口
- 隐藏窗口
- 退出（完全退出应用）

关闭按钮的行为也由平台能力决定：当 `HideOnCloseSupported` 为 `true` 且 `CloseAction == "hide"` 时，点关闭会隐藏窗口而非退出，应用继续在后台运行；否则按正常退出处理。

> 待核实：macOS 平台的 `SystemTraySupported` 与 `HideOnCloseSupported` 当前是否默认启用，需以 `internal/platform/capabilities_runtime.go` 的实际分发为准。

---

## 配置目录

应用配置统一存放在用户主目录下的 `~/.amagi-codebox/`，由 `defaultConfigDir()` 解析（`app.go`）：

```go
func defaultConfigDir() string {
    home, err := os.UserHomeDir()
    if err != nil {
        return ".amagi-codebox"
    }
    return filepath.Join(home, ".amagi-codebox")
}
```

目录生成时机：应用启动钩子 `App.Startup`（`app.go`）依次调用各服务的 `Load()`，包括 Settings、Config、Secrets、Paths、EnvVars、Proxy、Workspaces 等。这些服务首次写入时会在 `~/.amagi-codebox/` 下创建对应文件（首次启动若加载失败，会回退到内置默认配置并记入日志，不阻断启动）。

目录下常见文件（与 `README.md` "配置文件"表一致）：

| 文件 | 用途 |
|------|------|
| `config.json` | 提供商与预设配置 |
| `secrets.json` | 加密存储的 API 密钥 |
| `settings.json` | 应用设置（远程端口、GitHub Token 等） |
| `envvars.json` | 自定义环境变量 |
| `settings_amagi.json` | Amagi 模型配置 |
| `global-enabled.json` | 全局启用的插件列表 |

API 密钥的加密与存储细节见 [../security.md](../security.md)。建议不要手工编辑这些文件；如需导入/导出，请使用应用内"Provider Center"提供的导入与导出功能（详见 [./providers.md](./providers.md)）。

---

## 首次运行验证清单

完成安装后，可通过以下方式确认应用正常：

1. 启动应用，进入默认的"会话设置"页（路由 `/`）。
2. 前往"环境检测"页（`/envcheck`），确认本机 CLI 工具状态。
3. 前往"Provider Center"（`/provider`），查看内置的默认提供商（anthropic、openai、glm、minimax、kimi）。
4. 在选定提供商中填入 API 密钥并保存（密钥会经 OS 加密后写入 `secrets.json`）。
5. 回到"会话设置"页，选择引擎、提供商、预设与工作目录，启动首个会话。

启动会话的具体操作流程见 [./usage.md](./usage.md#启动一个会话)。

---

## 已知限制

- macOS 单实例保护与 Windows 不对等：macOS 当前允许启动多个实例。
- 内嵌终端在平台不支持时会自动回退为外部终端模式（由 `platform.ValidateLaunchRequest` 控制）。
- 应用未携带代码签名（待核实），首次启动需在系统安全设置中放行。
