# 内嵌终端

面向 Amagi CodeBox 的终端用户。本篇说明应用内嵌终端（`/terminal` 页）的工作原理、平台差异、可配置项，以及终端会话从启动到退出的行为。终端是 Claude Code / OpenCode / Codex 三种引擎在 `embedded` 启动模式下的承载界面，理解其机制有助于解释视觉抖动、历史回放、跨平台字体渲染等常见现象。

相关参考：

- 启动一个会话（包含引擎、模式、shell 选择）：[./usage.md](./usage.md)
- 后端 API 与 Wails 绑定方法签名：[../api.md](../api.md)
- 远程终端（移动端通过 WebSocket 接入同一 PTY）：[./remote-mobile.md](./remote-mobile.md)
- 安装与首次运行：[./installation.md](./installation.md)

---

## 总览

内嵌终端由三层组成：

| 层 | 实现 | 关键源码 |
|----|------|----------|
| 前端渲染 | xterm.js 6 + Fit / WebGL / Canvas / Web Links addon | `frontend/src/composables/useTerminalEngine.ts` |
| 伪终端宿主 | Windows ConPTY；macOS creack/pty；其他平台为 stub | `internal/pty/service.go`、`internal/pty/service_darwin.go`、`internal/pty/service_other_stub.go` |
| 数据通道 | Wails 事件 + base64 编码的双向流 | 后端 `EventsEmit` / 前端 `EventsOn`，前端→后端走 `PtyWrite` / `PtyWriteLarge` |

工作流（简化）：

1. 用户在 `/`（会话设置）页选定引擎、提供商、预设、工作目录与 shell，点击启动。
2. 后端 `LaunchSession` 解析 provider/preset，按启动模式（`embedded`）在 PTY 服务中创建会话，返回 session ID。
3. 前端在 `/terminal` 页用 session ID 挂载 xterm 实例，订阅 `pty:data:<id>` / `pty:exit:<id>` 事件。
4. 用户键盘输入经 `PtyWrite` 写回 PTY；进程输出经事件推送到前端，渲染到 xterm。
5. 进程退出时触发 `pty:exit:<id>`，前端写入"进程已退出"提示并通知视图层。

> 平台备注：Linux/其他非 Windows、非 macOS 平台的 PTY 后端是占位 stub（`service_other_stub.go`），所有 PTY 操作都会返回 `pty backend is not implemented on this platform yet`。Amagi CodeBox 当前官方支持平台为 Windows 10 1903+ 与 macOS 10.15+。

---

## 平台后端

### Windows：ConPTY

- 依赖：`github.com/UserExistsError/conpty`（见 `internal/pty/service.go`）。
- 会话创建：`Service.StartResolved` 调用 `conpty.Start(commandLine, opts...)`，通过 `ConPtyDimensions` / `ConPtyWorkDir` / `ConPtyEnv` 传入初始尺寸、工作目录与环境。
- 默认尺寸：未指定时为 120 列 × 40 行（`internal/pty/service.go`）。
- Shell 包装：
    - PowerShell / pwsh：以 `-NoProfile -NoLogo -NoExit -ExecutionPolicy Bypass -Command "..."` 形式启动。`-ExecutionPolicy Bypass` 是进程级参数，仅对当前会话生效，不改变系统执行策略，目的是让 npm 全局安装的 `.ps1` shim（如 `opencode.ps1`）在系统策略为 `Restricted` 的机器上也能运行。
    - cmd：以 `/K "chcp 65001 >nul && <命令>"` 形式启动，显式切到 UTF-8 代码页。
    - 直接命令（如 `claude`、`opencode`）：不经过 shell 包装。
- 路径回退：若配置的 PowerShell 7 路径不存在，会按 `C:\Program Files\PowerShell\7\pwsh.exe` → `%ProgramFiles%\PowerShell\7\pwsh.exe` 顺序查找；仍未找到则回退到 `powershell.exe`（Windows PowerShell）。

### macOS：creack/pty

- 依赖：`github.com/creack/pty`（见 `internal/pty/service_darwin.go`）。
- 会话创建：`exec.Command` 构造子进程，`creackpty.StartWithAttrs` 启动并附加 PTY；通过 `syscall.SysProcAttr{Setsid: true, Setctty: true}` 建立新的会话与控制终端。
- 默认尺寸：未指定时同样为 120 × 40。
- Shell 包装：bash/zsh 使用 `-ilc`（交互式登录 shell），其他 shell（fish/sh 等）使用 `-lc`；启动命令作为参数内联传入。
- 环境补全：若环境变量中未设置，自动补入 `TERM=xterm-256color`、`COLORTERM=truecolor`、`LANG=en_US.UTF-8`。LANG 补入是为了避免从 Finder 等启动时 LANG 未设置导致 CLI 工具（含 OpenCode）输出乱码。

### 其他平台：未实现

`service_other_stub.go` 在 `!windows && !darwin` 构建标签下编译，所有方法返回错误。Amagi CodeBox 不官方支持这些平台；如需了解原因或进展，可参考仓库 issue。

---

## 前端渲染

xterm.js 6 是终端渲染核心。挂载逻辑在 `useTerminalEngine.ts` 的 `mountTerm` 中，所有渲染选项在创建 `Terminal` 实例时一次性确定。

### 默认渲染参数

```typescript
new Terminal({
  cursorBlink: true,
  fontSize: 14,
  scrollback,             // 来自 settings.json 的 terminal.scrollback，默认 100000
  fontFamily: "'SF Mono','JetBrains Mono','Cascadia Code','Consolas','Courier New',monospace",
  macOptionClickForcesSelection: true,
  theme: buildXtermTheme(),
  allowProposedApi: true,
  // Windows 额外配置：
  //   windowsPty: { backend: 'conpty', buildNumber: 19041 }
})
```

> 字体目前在前端硬编码，没有暴露到设置页或 `settings.json`。修改字体需要自行构建（详见 [../developer/build-dev.md](../developer/build-dev.md)）。

### 主题

`buildXtermTheme()` 返回固定的暗色主题，颜色取自应用设计 token，不是 xterm 默认配色：

- 背景 `#1B1B1F`，前景 `#E6E6E6`，光标 `#5EA6FF`
- 16 色 ANSI 调色板（黑红绿黄蓝品青白 + 亮色），完整定义见 `useTerminalEngine.ts` 的 `buildXtermTheme` 函数

### 渲染器选择策略

xterm.js 默认使用 DOM 渲染；为提升高频 TUI 重绘下的性能，Amagi CodeBox 按以下优先级尝试加载更快的渲染器：

| 平台 | 加载的 addon | 原因 |
|------|--------------|------|
| macOS（WKWebView） | `@xterm/addon-canvas`（CanvasAddon） | WebGL 在 WKWebView 中会损坏 scrollback 纹理图集；Canvas 渲染到单一 `<canvas>`，避开 GPU 纹理路径，同时远快于 DOM 渲染 |
| Windows / Linux | `@xterm/addon-webgl`（WebglAddon，需通过 WebGL 探测） | WebGL 是这些平台上最快的渲染器 |
| 任意加载失败 | xterm 内置 DOM 渲染器 | 失败时 fail-open，保证终端可用 |

WebGL addon 还实现了上下文丢失重连：`onContextLoss` 触发后 500ms 重新加载渲染器并强制 fit。

> Canvas addon 不暴露 `onContextLoss`；如果底层 canvas 上下文丢失，xterm 渲染器注册表会在下一次渲染时回退到默认 DOM 渲染器。

### Link 识别

xterm 加载两类链接 provider：

1. **WebLinksAddon**：识别 HTTP/HTTPS URL，点击时调用 `BrowserOpenURL(uri)` 用系统浏览器打开。
2. **自定义文件路径 LinkProvider**：识别带路径分隔符的文件路径（含可选行号 `:42` 或 `:10:5`），如 `src/main.ts:42`、`./lib/util.go`、`C:\path\to\file.go:100`。点击调用 `OpenFileInEditor(filePath, lineNum)` 在编辑器中打开。纯文件名（无分隔符）和 URL 不会被当作文件路径匹配。

### 历史回放

每个 PTY 会话在后端维护 1 MB 的环形输出缓冲区（`maxOutputHistorySize = 1024 * 1024`）。

- **作用**：移动端或桌面端后加入的观察者连接到运行中的会话时，可重放最近输出，避免"只看到会话尾部"。
- **trim 算法**：`trimHistoryToFrontier` 在截断时避免从多字节 UTF-8 字符中间或 ANSI 转义序列中间开始，防止回放乱码。
- **seq 去重**：每段输出都附带单调递增的 `emitSeq`。前端挂载时先取一次 `GetOutputHistorySnapshot`（含 `{data, seq}`），把 seq 作为水位线；之后任何 `seq <= 水位线` 的实时事件都被丢弃，避免历史与实时流之间出现重复帧。
- **分块写入**：1 MB 历史不会一次性 `term.write()`，而是按 64 KB 分块、每块之间让出一帧（`requestAnimationFrame`），避免长时间阻塞主线程。

---

## 用户输入与剪贴板

### 键盘

| 快捷键 | 行为 |
|--------|------|
| 普通按键 | 直接发送到 PTY（经 `PtyWrite`） |
| `Ctrl+C`（有选区） | 复制选区到剪贴板，不发送 SIGINT |
| `Ctrl+C`（无选区） | 转发 SIGINT 到 PTY |
| `Ctrl+V` | 阻止默认；粘贴走 xterm textarea 的 paste 事件钩子（见下） |
| `Ctrl+Shift+C` | 强制复制选区 |
| `Ctrl+Shift+V` | 强制粘贴 |
| `Ctrl+Shift+A` | 全选 |
| `Backspace` / `Delete`（有选区） | 等长退格清除选区，避免删除字符数不匹配 |

### 粘贴

粘贴走单一入口：xterm 的 `<textarea>` 上注册了捕获阶段的 `paste` 监听器，阻止 xterm 内置 onData 路径，避免双写。

- 短文本（≤ 1024 字节）：`PtyWrite(sessionID, base64)`。
- 长文本（> 1024 字节）：`PtyWriteLarge(sessionID, base64)`，后端按 1 KB 分块写入，块之间 sleep 10 ms，避免 ConPTY 输入缓冲区溢出导致截断。
- 图片粘贴（如 Windows 截图工具）：
    - 桌面：检测到剪贴板是图片时调用 `SaveClipboardImage(base64)`，把图片保存为本地文件，把文件路径写入 PTY（供支持图片的 CLI 使用）。
    - 移动端 / 不支持 `clipboard.read()`：静默跳过。

### 剪贴板写入的三级降级

1. Wails 原生 `ClipboardSetText`（走 Windows API，不依赖 WebView 权限/焦点）。
2. WebView 异步 `navigator.clipboard.writeText`。
3. 同步 `document.execCommand('copy')`（已废弃，兜底）。

> 这种降级是必须的：WebView2 在焦点落到 xterm canvas/WebGL 元素、或用户激活上下文丢失时，`navigator.clipboard.writeText` 会抛 `NotAllowedError` 静默失败。

### Shift + 拖动强制选择

OpenCode 等 TUI 启用 SGR/1006 鼠标报告后，xterm 会把鼠标事件转发给 PTY，禁用自身的选区层。

- macOS：依赖 xterm 内置的 `macOptionClickForcesSelection: true`，Option + 拖动强制选择。
- Windows / Linux：`useTerminalEngine.ts` 的 `attachForcedSelection` 在容器上注册捕获阶段的 `mousedown`，按住 Shift 拖动时阻止事件传到 xterm，通过纯 DOM 几何 + `term.select()` 合成选区。组件卸载时若拖动仍在进行，会显式清理 window 级 mousemove/mouseup 监听。

---

## 可配置项

### 终端预设（TerminalPreset）

`TerminalPreset`（`internal/config/types.go`）是按终端维度组织的预设容器，独立于 Provider。它只承载模型与参数，不含 shell 或字体。

字段：

| 字段 | JSON key | 用途 |
|------|----------|------|
| 名称 | `name` | 预设显示名称 |
| 关联提供商 | `provider` | 如 `anthropic`、`openai` |
| 模型 | `model` | 可覆盖 provider 默认值 |
| Haiku 档位模型 | `model_haiku` | Claude Code 专用 |
| Sonnet 档位模型 | `model_sonnet` | Claude Code 专用 |
| Opus 档位模型 | `model_opus` | Claude Code 专用 |
| 模型参数 | `parameters` | 透传给 CLI |
| OpenCode 配置 | `opencode_cfg` | 仅 `opencode` 类型使用，原始 JSON 对象 |

类型分组（`TerminalPresetsConfig`）：

- `claude_code`：Claude Code 终端预设
- `opencode`：OpenCode 终端预设
- `codex`：Codex 终端预设

> 任务规格曾提到"TerminalPreset（shell/字体等）"。实际代码中，shell 不在 TerminalPreset 内，字体在 `useTerminalEngine.ts` 硬编码——见下一节。

### Shell 选择

Shell 在会话设置页 `/`（`SessionSettingsView`）选定，存储于 `settings.json` 的 `dashboard` 对象：

| 引擎 | settings.json 字段 |
|------|---------------------|
| Claude Code | `dashboard.claudeShell` |
| OpenCode | `dashboard.openCodeShell` |
| Codex | `dashboard.codexShell` |
| 通用（legacy） | `dashboard.shell` |

默认值均为 `pwsh`（Windows 默认 shell）。macOS 默认 shell 通过 `platform.CurrentCapabilities().DefaultShellKey` 解析，通常为 `zsh`。

可选 shell 由 `internal/platform` 的 shell catalog 枚举，并合并用户自定义的 `settings.json` 中 `shellPaths` 条目（每项含 `path` 与 `label`）。新增 / 删除自定义 shell 通过应用内设置页完成，不直接编辑文件。

### 终端设置

`TerminalSettings`（`internal/settings/service.go`）目前只有一个字段：

| 字段 | 取值范围 | 默认值 |
|------|----------|--------|
| `scrollback` | 1000 ~ 10 000 000 | 100000 |

修改路径：`/terminal` 页关联的终端设置入口（`frontend/src/views/settings/TerminalSettings.vue`）。保存后需重新打开终端窗口才生效（已挂载的 xterm 实例不会动态调整 scrollback）。

---

## 后端回调注册

后端 `pty.Service` 暴露三组回调注册接口，供远程 WebSocket 桥接复用同一份 PTY 输出。三个方法签名相同：`(sessionID string, id string, cb ...)`，其中 `id` 是同一 session 下多连接的区分键。

| 方法 | 触发时机 | 回调签名 |
|------|----------|----------|
| `RegisterOutputCallback(sessionID, id, cb)` | 每次 PTY 输出 | `func(data []byte)` |
| `RegisterExitCallback(sessionID, id, cb)` | 进程退出 | `func(exitCode uint32)` |
| `RegisterResizeCallback(sessionID, id, cb)` | 尺寸变化 | `func(cols, rows int)` |

配套的注销方法：`UnregisterOutputCallback` / `UnregisterExitCallback` / `UnregisterResizeCallback`，参数为 `(sessionID, id)`。`App` 在 `app.go` 中以同名方法把这些接口转发给 `Pty` 服务（如 `App.RegisterOutputCallback`）。

原子附加辅助方法：

```go
func (s *Service) AttachSessionObserver(sessionID, id string,
    outputCB func(data []byte),
    resizeCB func(cols, rows int),
) (history []byte, cols, rows int, err error)

func (s *Service) DetachSessionObserver(sessionID, id string)
```

`AttachSessionObserver` 在同一组锁内完成"历史快照 + 注册 live 回调"，避免 history 与 live 之间丢帧。该 API 主要服务于远程 WebSocket，普通桌面用户不会直接接触。

---

## 数据协议

### 后端 → 前端（Wails 事件）

| 事件名 | Payload | 说明 |
|--------|---------|------|
| `pty:data:<sessionID>` | `{ "s": <seq>, "d": "<base64>" }` | PTY 输出，`s` 为单调递增序列号 |
| `pty:exit:<sessionID>` | `{ "exitCode": <int>, "error": "<err>" }` | 进程退出 |

> 历史兼容：前端解析 `pty:data` 时同时接受裸字符串（不带 seq 的旧协议），此时 seq 视为 0，不参与去重。

### 前端 → 后端

| 方法 | 参数 | 用途 |
|------|------|------|
| `PtyWrite(sessionID, base64)` | session ID + base64 数据 | 普通输入（≤ 1 KB） |
| `PtyWriteLarge(sessionID, base64)` | 同上 | 长文本粘贴（> 1 KB 自动走此路径） |
| `PtyResize(sessionID, cols, rows)` | session ID + 列/行 | 通知后端调整 PTY 尺寸 |

Wails 自动生成 `frontend/wailsjs/go/main/App.ts` 中的类型化包装；前端 `src/api/` 层进一步封装。`frontend/wailsjs/` 为自动生成内容，**不要手工编辑**。

---

## 尺寸调整与多 Tab

### Fit

`@xterm/addon-fit` 的 `FitAddon` 负责根据容器尺寸计算合适的列/行：

- `fitTerminal(sessionId, force, containerEl)` 是统一入口。
- 维度未变时跳过 `fit()`，避免触发不必要的屏幕缓冲区重绘。
- 用户上翻查看历史时，`fit()` 后会恢复滚动位置，避免视口被瞬间拽回底部。

### Resize

- 尺寸变化通过 `PtyResize` 同步到后端 PTY（Windows 调 `ConPty.Resize`，macOS 调 `creackpty.Setsize`）。
- 后端收到新尺寸后，遍历所有 `RegisterResizeCallback` 注册的回调（供远程 observer 同步 dimensions 帧）。
- macOS 路径会拒绝 `cols <= 0` 或 `rows <= 0`。

### 多 Tab 并发

Amagi CodeBox 支持同时运行多个会话（多 Tab）。每个会话在后端独立持有 `PtySession`，前端以 session ID 为 key 维护独立的 `TerminalInstance`（xterm 实例 + addon + 监听器 + 状态）。切换 Tab 时：

- 已挂载的会话不会被销毁，只是从 DOM 上卸载或隐藏。
- 切回原 Tab 时，通过历史回放 + 实时事件续流，恢复到当前最新输出。
- 进程退出会触发 `pty:exit`，但 xterm 实例继续保留退出提示，用户仍可查看末尾输出。

---

## 进程退出与会话清理

- 进程退出时，后端 `waitLoop` 发出 `pty:exit:<sessionID>`，并调用所有 `RegisterExitCallback` 注册的回调。
- 前端在 xterm 中追加黄色提示：`[amagi-codebox] 进程已退出 (exit code: N)`。
- `Service.Close(sessionID)`：
    - Windows：取消读取 goroutine 上下文，关闭 ConPTY，等待 readLoop 退出。
    - macOS：先 `SIGTERM`，2 秒后仍未退出则 `SIGKILL`，再关闭 ptmx。
- `Service.CloseAll()` 在应用退出时统一清理所有会话。

---

## 已知限制与注意事项

- 字体硬编码：当前没有 UI 修改入口；如需自定义字体需修改源码（`useTerminalEngine.ts`）。
- macOS WebGL 不可用：WKWebView 下 WebGL addon 会损坏 scrollback 纹理图集，因此 macOS 强制使用 Canvas addon。
- Linux/其他平台未实现 PTY 后端：所有 PTY 操作返回 `pty backend is not implemented on this platform yet`。
- 长粘贴有节流：> 1 KB 的粘贴会被切成 1 KB 块、块间 10 ms 延迟，超大粘贴的体验是渐进式的。
- 历史缓冲上限固定为 1 MB：会话输出极长时，更早的内容会被裁剪到安全边界（不切断多字节 UTF-8 或 ANSI 转义）。

> 待核实：Windows 与 macOS 之外平台是否在路线图上提供 PTY 后端；字体设置是否计划暴露到 UI。
