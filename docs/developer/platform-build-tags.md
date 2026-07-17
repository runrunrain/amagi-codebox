# 跨平台 build tags 机制

> 受众：修改平台相关代码（窗口、终端、密钥存储、进程、托盘等）的开发者。
> 范围：`//go:build` 文件分流约定、各域平台文件清单、与 `runtime.GOOS` 的取舍、修改指南。
> 信息来源：实际读取 `internal/platform/*.go`、`internal/secrets/*.go`、`internal/pty/*.go`、`internal/updater/*.go`、`tray_icon_*.go` 的 `//go:build` 行与实现。

## 核心约定

Amagi CodeBox 的平台差异**在编译期分流**，不通过 `runtime.GOOS` 在业务路径里分支。每个平台相关文件以 `//go:build` 约束声明目标，并用 `_<os>.go` 后缀命名以便识别。

```go
//go:build windows

package secrets
```

### 为什么不用 `runtime.GOOS`

- `runtime.GOOS` 分支会编译进所有平台的死代码，Go 工具链无法裁剪。
- 平台独有的 import（如 Windows 的 `syscall.NewLazyDLL("kernel32.dll")`、macOS cgo 的 `#cgo LDFLAGS: -framework Security`）会拖累其它平台的编译，甚至直接失败。
- build tag 让每个目标二进制只包含对应平台的实现，符号表干净。

### 平台能力运行时分流（少数特例）

`internal/platform/` 下有少量共享文件（无 build tag），在内部用 `runtime.GOOS` 或 `capabilities.OS` 参数化分流。这些文件本身可在所有平台编译（不引入平台独占依赖），用 `runtime` 包做切换：

| 文件 | 分流方式 | 说明 |
|---|---|---|
| `capabilities_runtime.go` | `runtime.GOOS` / `runtime.GOARCH` | `CurrentCapabilities()` 入口 |
| `capabilities.go` | 无 | 仅声明 `PlatformCapabilities` 结构与校验 |
| `resolver.go` | 共享 + 接收 OS 参数 | Shell 路径解析 |
| `path_lookup.go` | 共享 | PATH 查询，darwin 有 baseline PATH |
| `path_lookup_cmd.go` | `runtime.GOOS != "windows"` 提前返回 | `resolveWindowsCmdExe` 在非 Windows 直接返回空 |
| `shell_catalog.go` | `capabilities.OS` switch | 候选 shell 列表按 OS 分组 |
| `process_runner.go` | 共享 | `CommandSpec` 与跨平台 exec 包装 |

这些是**精心选择的共享层**，仅做与 OS 相关的查询/枚举，不引入平台独占 import。新增平台独占逻辑时不要继续往这些共享文件里塞，应该新开 `_<os>.go` 文件。

## 启动时一次性解析能力

`main.go` 在 `wails.Run` 之前调用：

```go
capabilities := platform.CurrentCapabilities()
```

`CurrentCapabilities()`（`internal/platform/capabilities_runtime.go`）根据 `runtime.GOOS` / `runtime.GOARCH` 构造一份 `PlatformCapabilities`，包含：

- 支持的终端模式（`EmbeddedTerminalSupported` / `StandaloneTerminalSupported`）
- 系统托盘、文件打开、自动启动、单实例、窗口激活等能力位
- 关闭行为（`CloseActionHide` / `CloseActionQuit`）
- 安全密钥存储后端类型（`SecureSecretStoreKind`）
- 支持的 shell 列表（`SupportedShells`）

这份快照在 `App` 与前端 `usePlatformCapabilities` 之间共享，运行期不变。UI 与业务路径只读取能力位，不再做 OS 判断。

## 平台文件清单

### `internal/secrets/`：密钥存储后端

| 文件 | build tag | 后端 | 行为 |
|---|---|---|---|
| `store.go` | （共享） | — | `SecretStore` 接口声明 |
| `store_windows.go` | `windows` | DPAPI | `billgraziano/dpapi` 加密落盘 |
| `store_darwin_cgo.go` | `darwin && cgo` | macOS Keychain | cgo 调用 Security/CoreFoundation framework |
| `store_darwin_nocgo.go` | `darwin && !cgo` | 不可用 | `Kind()` 返回 `"keychain"`，但 `Load`/`Save` 返回 `ErrSecretStoreNotReady` |
| `store_other.go` | `!windows && !darwin` | 不支持 | `Kind()` 返回 `"unsupported"`；`Load` 返回空 map、`Save` 静默 no-op |

注意：

- **darwin 按 cgo 开关再分流**。CGO_ENABLED=0 构建时（如交叉编译或纯静态）Keychain 后端降级为不可用。
- `store_other.go` 是**静默 no-op 而非明文回退**：`Load` 返回空 map、`Save` 不做任何持久化。在 Linux 等无系统密钥库的平台上密钥不会被持久化，这是当前有意行为；若需要真正的明文回退需在此文件补充实现。

每个平台文件都定义 `NewSecretStore() SecretStore`，由 `secrets.SecretsService` 在构造时调用一次。

### `internal/pty/`：伪终端

| 文件 | build tag | 实现 |
|---|---|---|
| `service.go` | `windows` | ConPTY，依赖 `github.com/UserExistsError/conpty` |
| `ansi.go` | （共享） | ANSI 序列工具 |
| `service_darwin.go` | `darwin` | `creack/pty`，本地 exec + syscall |
| `service_other_stub.go` | `!windows && !darwin` | stub，返回错误 |

**特例：`service.go` 文件名不带 `_windows` 后缀，但首行是 `//go:build windows`。** 这是少数文件名与 build tag 不一致的情况。修改 Windows PTY 行为时直接编辑 `service.go`；不要因为文件名无后缀就误以为它是跨平台共享文件。

`PtySession` 在 Windows 下持有 `*conpty.ConPty`，在 macOS 下持有 `io.Reader`/`io.Writer` 与 `*exec.Cmd`，跨平台字段集合在共享的 `Service` 与回调类型上（`outputCallback`、`exitCallback`、`resizeCallback`）。

### `internal/platform/`：能力与 OS 抽象

#### 文件打开（`file_opener_*`）

| 文件 | build tag | 命令 |
|---|---|---|
| `file_opener.go` | （共享） | `FileOpener` 接口、`NewFileOpener` 工厂、`openWithRunner` 辅助 |
| `file_opener_darwin.go` | `darwin` | `open <path>` |
| `file_opener_windows.go` | `windows` | `cmd /c start "" <path>`，并应用 `DefaultProcessPolicy()` |
| `file_opener_other.go` | `!windows && !darwin` | `xdg-open <path>` |

每个平台文件实现 `newFileOpener(runner) FileOpener`，由共享的 `NewFileOpener` 转调。

#### 单实例锁（`single_instance_*`）

| 文件 | build tag | 实现 |
|---|---|---|
| `single_instance_windows.go` | `windows` | `kernel32.CreateMutexW` + `user32.FindWindowW` + `SetForegroundWindow` |
| `single_instance_nonwindows.go` | `!windows` | 无操作，直接 `return true` |

非 Windows 平台不做单实例保护（macOS 由系统机制或用户习惯处理）。

#### 进程策略（`process_policy_*`）

| 文件 | build tag | 实现 |
|---|---|---|
| `process_policy_windows.go` | `windows` | 通过 `syscall.SysProcAttr` 控制 `HideWindow`、`Detached` 等 |
| `process_policy_nonwindows.go` | `!windows` | 空实现（`_ = cmd; _ = policy`） |

#### 脚本包装（`process_script_*`）

| 文件 | build tag | 实现 |
|---|---|---|
| `process_script_windows.go` | `windows` | `wrapWindowsScript`：`.cmd`/`.bat` 走 `cmd.exe /c`，`.ps1` 走文件关联 |
| `process_script_nonwindows.go` | `!windows` | no-op：原样返回 `CommandSpec` |

#### Shell 目录（`shell_catalog.go`）

无 build tag，共享。内部用 `capabilities.OS` switch 给出 Windows（`pwsh`/`powershell`/`cmd`）与类 Unix（`zsh`/`bash`/`fish`/`pwsh`）两套候选。这是"共享文件内部分流"的合理样例：候选清单不依赖平台独占 import，可安全在所有平台编译。

### 仓库根：托盘图标（`tray_icon_*.go`）

| 文件 | build tag | 内容 |
|---|---|---|
| `tray_icon_windows.go` | `windows` | `//go:embed build/windows/icon.ico`，导出 `trayIcon []byte` |
| `tray_icon_nonwindows.go` | `!windows` | 仅声明 `var trayIcon []byte`（空切片） |

`app.go:Startup` 检查 `capabilities.SystemTraySupported && len(trayIcon) > 0` 后才启动托盘。空切片在非 Windows 自动跳过启动。

### `internal/updater/`：更新后重启

| 文件 | build tag | 实现 |
|---|---|---|
| `restart_windows.go` | `windows` | `exec.Command(exePath)` + `CreationFlags: 0x00000010`（DETACHED_PROCESS） |
| `restart_nonwindows.go` | `!windows` | 返回 `fmt.Errorf("starting updated executable is not implemented for %s", exePath)` |

下载并应用更新后，仅 Windows 支持自动重启新版本；非 Windows 返回未实现错误。

## build tag 语法

现代 Go 使用：

```go
//go:build windows

//go:build !windows && !darwin

//go:build darwin && cgo
```

旧式 `// +build windows` 注释已弃用，本仓库新增文件应只用 `//go:build`。注意 `//go:build` 行必须是文件首行（前面不能有空行或其它注释），否则约束不生效。

## 修改指南

### 选择正确的文件

**先看 build tag，再看文件名。** 多数情况下两者一致（`foo_windows.go` + `//go:build windows`），但有少数例外（如 `internal/pty/service.go` 实际是 windows only）。改之前务必打开文件确认首行。

### 改一个平台时同步另一平台的 stub

修改 `process_policy_windows.go` 的策略字段后，如果新增字段影响接口签名，`process_policy_nonwindows.go` 也需要相应调整（即便是 no-op）。Go 编译器会强制同名类型/函数在不同 build tag 文件间签名一致。

### 新增平台独占依赖

新加的 import 若是平台独占（Windows DLL、macOS framework），必须把整个文件加 build tag，不能塞进共享文件。否则其它平台的 `go build` / `wails build` 会失败。

### 新增平台能力位

向 `PlatformCapabilities` 增加字段后：

1. 在 `capabilities_runtime.go` 的 `capabilitiesForTarget` 为每个目标平台填值。
2. 前端 `usePlatformCapabilities.ts` 的 `PlatformCapabilities` 接口同步加字段。
3. UI 按新字段做条件渲染。

### 交叉编译验证

- Windows：`GOOS=windows go build ./...`
- macOS cgo：`CGO_ENABLED=1 go build ./...`（默认）
- macOS nocgo：`CGO_ENABLED=0 GOOS=darwin go build ./...`（Keychain 降级路径）
- Linux（仅用于发现 stub 是否健全）：`GOOS=linux go build ./...`

CI（`.github/workflows/ci.yml`）只跑 `go vet ./...` 加前端/移动端构建，不跑 `go test`，也不跑多平台构建矩阵。本地改平台代码后必须手动 `go build` 各目标平台。

## 相关文档

- [./architecture.md](./architecture.md)：平台能力如何注入 `App` 与启动流程。
- [./frontend-backend.md](./frontend-backend.md)：`usePlatformCapabilities` 如何映射到 UI。
- [../security.md](../security.md)：DPAPI/Keychain 后端的安全细节。

## 待核实项

- `store_other.go` 当前实现是 `unsupportedSecretStore`（`Kind: "unsupported"`，`Load` 返回空 map、`Save` 静默 no-op）。在 Linux 等无系统密钥库的平台上密钥不会被持久化，这是当前有意行为；若计划支持明文回退需在此文件补充实现。
- `tray_icon_*.go`：仅 Windows 真正嵌入图标；macOS 走 `tray_icon_nonwindows.go` 的空切片，但 `app.go:Startup` 判定 `capabilities.SystemTraySupported && len(trayIcon) > 0`。若要在 macOS 启用托盘，需补一个 darwin 分支并嵌入对应图标格式（待核实 macOS 托盘是否仍在产品规划内）。
- `internal/envvars/` 也存在平台文件（`platform_windows.go`、`platform_unsupported.go`），本篇未展开；其模式与上述一致，需要补充时再单独说明。
