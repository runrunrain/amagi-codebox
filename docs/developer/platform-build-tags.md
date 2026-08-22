# 跨平台 build tags 机制

> 受众：修改平台相关代码（窗口、终端、密钥存储、进程、托盘、系统代理、WSL 等）的开发者。
> 范围：`//go:build` 文件分流约定、各域平台文件清单（按当前代码核实）、修改指南。
> 信息来源：实际读取各文件首行 `//go:build` 约束（核实日期 2026-08-22，版本 1.3.50）。

## 核心约定

Amagi CodeBox 的平台差异**在编译期分流**，不通过 `runtime.GOOS` 在业务路径里分支。每个平台相关文件以 `//go:build` 约束声明目标，通常配套 `_<os>.go` 文件名后缀。

```go
//go:build windows

package secrets
```

### 为什么不用 `runtime.GOOS`

- `runtime.GOOS` 分支会把所有平台的死代码编译进每个二进制，Go 工具链无法裁剪。
- 平台独有的 import（Windows 的 `syscall.NewLazyDLL`、macOS cgo 的 `#cgo LDFLAGS: -framework Security`）会拖累其它平台编译，甚至直接失败。
- build tag 让每个目标二进制只包含对应平台的实现。

### 平台能力运行时分流（少数特例）

`internal/platform/` 下有少量共享文件（无 build tag），内部用 `runtime.GOOS` 或 `capabilities.OS` 参数化分流。这些文件不引入平台独占依赖，可安全在所有平台编译：

| 文件 | 分流方式 | 说明 |
|---|---|---|
| `capabilities_runtime.go` | `runtime.GOOS` / `runtime.GOARCH` | `CurrentCapabilities()` 入口 |
| `capabilities.go` | 无 | `PlatformCapabilities` 结构声明与校验 |
| `resolver.go` | 共享 + 接收 OS 参数 | Shell 路径解析 |
| `path_lookup.go` / `path_lookup_cmd.go` | 共享；`resolveWindowsCmdExe` 在非 Windows 提前返回空 | PATH 查询 |
| `shell_catalog.go` | `capabilities.OS` switch | 候选 shell 列表按 OS 分组 |
| `process_runner.go` | 共享 | `CommandSpec` 与跨平台 exec 包装 |
| `wsl.go` | 共享 | WSL 查询抽象 |
| `codex_home.go` | 共享 | Codex 配置目录解析 |

这些是**精心选择的共享层**。新增平台独占逻辑时不要往共享文件里塞平台 import，应该新开 `_<os>.go` 文件。

## 启动时一次性解析能力

`main.go` 在 `wails.Run` 之前调用 `platform.CurrentCapabilities()`，构造 `PlatformCapabilities` 快照：终端模式支持位、系统托盘/文件打开/单实例/窗口激活能力位、关闭行为（`CloseActionHide`/`CloseActionQuit`）、安全密钥存储后端类型（`SecureSecretStoreKind`）、支持的 shell 列表等。快照在 `App` 与前端 `usePlatformCapabilities` 之间共享，运行期不变。

## 平台文件清单（按域）

### `internal/secrets/`：密钥存储后端

| 文件 | build tag | 后端 | 行为 |
|---|---|---|---|
| `store.go` | （共享） | — | `SecretStore` 接口声明 |
| `store_windows.go` | `windows` | DPAPI | `billgraziano/dpapi` 加密落盘 |
| `store_darwin_cgo.go` | `darwin && cgo` | macOS Keychain | cgo 调用 Security/CoreFoundation framework |
| `store_darwin_nocgo.go` | `darwin && !cgo` | 不可用 | `Kind()` 返回 `"keychain"`，但 `Load`/`Save` 返回 `ErrSecretStoreNotReady` |
| `store_other.go` | `!windows && !darwin` | 不支持 | `Kind()` 返回 `"unsupported"`；`Load` 返回空 map、`Save` 静默 no-op |

注意：

- **darwin 按 cgo 开关再分流**。`CGO_ENABLED=0` 构建时 Keychain 后端降级为不可用。
- `store_other.go` 是**静默 no-op 而非明文回退**：Linux 等无系统密钥库的平台上密钥不会被持久化，这是当前有意行为。

每个平台文件都定义 `NewSecretStore() SecretStore`，由 `secrets.SecretsService` 构造时调用一次。

### `internal/pty/`：伪终端

| 文件 | build tag | 实现 |
|---|---|---|
| `service.go` | `windows` | ConPTY，依赖 `github.com/UserExistsError/conpty` |
| `service_darwin.go` | `darwin` | `creack/pty`，本地 exec + syscall |
| `service_other_stub.go` | `!windows && !darwin` | stub，返回错误 |
| `ansi.go` / `run_sink.go` / `detach_receipt.go` / `readiness.go` | （共享） | ANSI 工具 / 运行事件 sink / 分离回执 / 就绪探测 |

**特例：`service.go` 文件名不带 `_windows` 后缀，但首行是 `//go:build windows`。** 三个平台文件各自定义 `type Service struct`（字段集按平台不同）；修改 Windows PTY 行为时直接编辑 `service.go`，不要因文件名无后缀误以为是共享文件。

### `internal/platform/`：能力与 OS 抽象

| 域 | 文件 | build tag | 实现 |
|---|---|---|---|
| 文件打开 | `file_opener.go` | （共享） | `FileOpener` 接口、`NewFileOpener` 工厂 |
| | `file_opener_darwin.go` | `darwin` | `open <path>` |
| | `file_opener_windows.go` | `windows` | `cmd /c start "" <path>` + `DefaultProcessPolicy()` |
| | `file_opener_other.go` | `!windows && !darwin` | `xdg-open <path>` |
| 单实例锁 | `single_instance_windows.go` | `windows` | `kernel32.CreateMutexW` + `user32.FindWindowW` + `SetForegroundWindow` |
| | `single_instance_nonwindows.go` | `!windows` | 无操作，直接 `return true` |
| 进程策略 | `process_policy_windows.go` | `windows` | `syscall.SysProcAttr` 控制 `HideWindow`、`Detached` 等 |
| | `process_policy_nonwindows.go` | `!windows` | 空实现 |
| 脚本包装 | `process_script_windows.go` | `windows` | `.cmd`/`.bat` 走 `cmd.exe /c`，`.ps1` 走文件关联 |
| | `process_script_nonwindows.go` | `!windows` | no-op：原样返回 `CommandSpec` |
| 系统代理 | `system_proxy.go` | （共享） | 接口与合并逻辑 |
| | `system_proxy_windows.go` | `windows` | Windows 系统代理读取 |
| | `system_proxy_darwin.go` | `darwin` | macOS 系统代理读取 |
| | `system_proxy_other.go` | `!darwin && !windows` | 空实现 |

`shell_catalog.go` 无 build tag，内部用 `capabilities.OS` switch 给出 Windows（`pwsh`/`powershell`/`cmd`）与类 Unix（`zsh`/`bash`/`fish`/`pwsh`）两套候选——共享文件内部分流的合理样例（候选清单不依赖平台独占 import）。

### `internal/launcher/`：进程身份

| 文件 | build tag |
|---|---|
| `process_identity_windows.go` | `windows` |
| `process_identity_darwin.go` | `darwin` |
| `process_identity_other.go` | `!darwin && !windows` |
| `process_identity_common.go` | （共享） |

### `internal/tray/`：系统托盘

| 文件 | build tag | 说明 |
|---|---|---|
| `service.go` | `windows` | 真实托盘实现 |
| `service_stub.go` | `!windows` | stub |

**注意这里同样是 `service.go` 文件名无后缀但为 windows-only。**

### `internal/envvars/`：环境变量平台辅助

| 文件 | build tag |
|---|---|
| `platform_windows.go` | `windows` |
| `platform_unsupported.go` | `!windows` |

### `internal/wslsetup/`：WSL 安装辅助

| 文件 | build tag |
|---|---|
| `service_windows.go` | `windows` |
| `service_other.go` | `!windows` |

### `internal/updater/`：更新与重启

| 文件 | build tag | 实现 |
|---|---|---|
| `process_windows.go` | `windows` | 进程替换辅助 |
| `process_nonwindows.go` | `!windows` | stub |
| `restart_windows.go` | `windows` | `exec.Command(exePath)` + `DETACHED_PROCESS` |
| `restart_nonwindows.go` | `!windows` | 返回未实现错误 |
| `windows_helper.go` | （共享，入口在 `main.go` 短路） | Windows 更新助手模式 |

下载并应用更新后仅 Windows 支持自动重启新版本；`main()` 首行 `updater.MaybeRunWindowsUpdateHelper(os.Args)` 处理更新助手启动模式。

### 仓库根：托盘图标与配置同步

| 文件 | build tag | 内容 |
|---|---|---|
| `tray_icon_windows.go` | `windows` | `//go:embed build/windows/icon.ico`，导出 `trayIcon []byte` |
| `tray_icon_nonwindows.go` | `!windows` | 空切片 |
| `config_sync_windows.go` | `windows` | 配置目录同步的 Windows 实现 |
| `config_sync_unix.go` | `!windows` | 类 Unix 实现 |

`app.go:Startup` 检查 `capabilities.SystemTraySupported && len(trayIcon) > 0` 后才启动托盘；空切片在非 Windows 自动跳过。

## build tag 语法

现代 Go 使用：

```go
//go:build windows

//go:build !windows && !darwin

//go:build darwin && cgo
```

旧式 `// +build windows` 已弃用，本仓库新增文件只用 `//go:build`。`//go:build` 行必须是文件首行（前面不能有空行或其它注释），否则约束不生效。

## 修改指南

### 选择正确的文件

**先看 build tag，再看文件名。** 多数情况下两者一致（`foo_windows.go` + `//go:build windows`），但存在反例：`internal/pty/service.go`、`internal/tray/service.go` 文件名无后缀实为 windows-only。改之前务必打开文件确认首行。

### 改一个平台时同步另一平台的 stub

同一符号在不同 build tag 文件间必须保持签名一致（Go 编译器强制）。修改 `process_policy_windows.go` 的字段若影响接口，`process_policy_nonwindows.go` 也要相应调整（即便是 no-op）。

### 新增平台独占依赖

新 import 若是平台独占（Windows DLL、macOS framework），必须把整个文件加 build tag，不能塞进共享文件，否则其它平台 `go build` / `wails build` 失败。

### 新增平台能力位

向 `PlatformCapabilities` 增加字段后：

1. 在 `capabilities_runtime.go` 为每个目标平台填值。
2. 前端 `usePlatformCapabilities.ts` 的接口同步加字段。
3. UI 按新字段做条件渲染。

### 交叉编译验证

- Windows：`GOOS=windows go build ./...`
- macOS cgo：`CGO_ENABLED=1 go build ./...`（默认）
- macOS nocgo：`CGO_ENABLED=0 GOOS=darwin go build ./...`（Keychain 降级路径）
- Linux（验证 stub 健全性）：`GOOS=linux go build ./...`

CI（`.github/workflows/ci.yml`）的 `go-quality` job 跑 windows-latest + macos-latest 双腿：两条腿都执行 `go vet ./...` 和 `golangci-lint`（pinned v2.12.2），因此 `_windows.go` 与 `_darwin.go` 文件均被静态检查；完整 `go test ./...` 只在 macos 腿运行，windows 腿做编译检查（`go test ./... -run '^$'`）。本地改平台代码后仍建议手动 `go build` 目标平台。

## 相关文档

- [./architecture.md](./architecture.md)：平台能力如何注入 `App` 与启动流程。
- [./frontend-backend.md](./frontend-backend.md)：`usePlatformCapabilities` 如何映射到 UI。
- [../security.md](../security.md)：DPAPI/Keychain 后端的安全细节。

## 待核实项

- `system_proxy_*.go` 三平台文件的具体读取机制（注册表/scutil 等）未逐一展开，按 build tag 清单登记；改系统代理行为前请读对应文件实现。
- macOS 托盘：`tray_icon_nonwindows.go` 为空切片，Startup 判定自动跳过；若要在 macOS 启用托盘需补 darwin 图标分支（产品规划待确认）。
