# BugA 修复报告：后台子进程闪窗（扩展管理页 ×4 终端窗口闪现）

- 任务：fix-extensions-flash-and-wsl-pi-config / BugA
- 日期：2025-08-30
- 范围：Go 后端窗口抑制；不改前端、不改 `internal/launcher`、不碰 `app.go`

## 1. 根因确认（核实任务诊断 + 修正细节）

**诊断采信并核实成立，细节修正一处：**

1. **主根因核实**：`internal/platform/process_policy_windows.go` 的 `applyProcessPolicy` 在 `HideConsoleWindow` 时仅设 `attr.HideWindow = true`（STARTUPINFO `STARTF_USESHOWWINDOW`/`SW_HIDE`）。Windows 11 默认终端为 Windows Terminal 时，该标志无法阻止 console 子系统子进程的新终端窗口弹出，必须叠加 `CREATE_NO_WINDOW (0x08000000)`。核实无误。
2. **触发链核实**：扩展管理页挂载 → `internal/plugin/service.go` 三个查询方法并行执行 `claude plugin` 命令（`GetMarketplaces`:44 `plugin marketplace list --json`、已装列表:99 `plugin list --json`、`GetAvailablePlugins`:251 `plugin list --json --available`）→ `internal/plugin/executor.go:47` `runner.Run(ctx, CommandSpec{..., Policy: DefaultProcessPolicy()})` → `process_runner.go` → `applyProcessPolicy`。3 个命令并行 = 3 个闪窗；第 4 个推测为 envcheck 缓存过期触发的版本探测（`internal/envcheck/checker_*.go` 全部走 `ProcessRunner + DefaultProcessPolicy()`，同根因同修复）或 `claude.cmd` 经 `wrapWindowsScript` 包裹的 `cmd.exe` 载体——两者均已在本修复覆盖内。
3. **Policy 构造点核实**：全仓库唯一构造点是 `process_runner.go:59` `DefaultProcessPolicy()`（`{HideConsoleWindow: true}`）。`Detached` 无任何调用方（grep 全仓库仅类型定义与 applyProcessPolicy 内的读取），因此互斥优先级调整无兼容性风险。
4. **裸 exec 点核实**：任务列出的 8 组裸 `exec.Command` 全部确认存在且无窗口抑制。

## 2. 修复方案

### 2.1 核心：`applyProcessPolicy` 增强 + 跨平台可测内核

- **`internal/platform/process_policy.go`（新增，无 build tag）**：把「策略 → 创建标志」决策拆为纯函数内核 `processPolicyCreationFlags` / `processPolicyHideWindow` + 常量 `flagDetachedProcess(0x10)` / `flagCreateNoWindow(0x08000000)`，任意平台可单测。
- **`process_policy_windows.go`**：`HideConsoleWindow` → `HideWindow + CREATE_NO_WINDOW`；`Detached` 优先 → `DETACHED_PROCESS`。**互斥语义**：`DETACHED_PROCESS` 与 `CREATE_NO_WINDOW` 同属子进程控制台创建方式控制、同时置位非法，按优先级二选一——Detached（完全不创建控制台，最彻底）> HideConsoleWindow（创建但不可见）。已在常量注释与单测断言中固化。
- **`process_policy_nonwindows.go`**：新增导出 `SuppressConsoleWindow(cmd)` no-op，Windows 实现在 windows 版本文件中 = `applyProcessPolicy(cmd, DefaultProcessPolicy())`，供跨平台源文件的裸 exec 点无条件调用（build tag 分平台，无 runtime.GOOS 分支）。

### 2.2 裸 exec 点补齐（9 文件 15 处）

| # | 文件:行号 | 命令 | 处置 |
|---|---|---|---|
| 1 | `internal/platform/system_proxy_windows.go:18-25,62-68` | `reg query` ×3 | ✅ 修复：新增 `regQuery()` helper（内含 `SuppressConsoleWindow`），3 处调用点统一收口 |
| 2 | `internal/platform/wsl.go:20,47` | `wsl.exe -l -q` / `-l -v` | ✅ 修复：两个 lister 闭包内 `SuppressConsoleWindow(cmd)` |
| 3 | `internal/wslsetup/service_windows.go:45,54,67` | `wsl.exe` ×3（wslExec/wslExecRoot/wslExecLogin） | ✅ 修复：`platform.SuppressConsoleWindow(cmd)` |
| 4 | `internal/ompconfig/service.go:352` | `omp models ls --json` | ✅ 修复：`platform.SuppressConsoleWindow(cmd)` |
| 5 | `internal/platform/process_runner.go:253-258` | `taskkill /PID ... /T /F`（killProcessTree） | ✅ 修复：`SuppressConsoleWindow(tk)` |
| 6 | `internal/gitassist/service.go:78,433,473` | `git` ×3（runGit/commitViaStdin/push） | ✅ 修复：`platform.SuppressConsoleWindow(cmd)` |
| 7 | `internal/platform/path_lookup.go:514` | resolveCommandViaShellFallback（`zsh/bash/sh -lc 'command -v'`） | ✅ 修复（统一防御）：实际仅 darwin 路径可达（两处调用点均 `osName=="darwin"` 守卫），但源文件跨平台，统一走 helper 防未来复用 |

### 2.3 豁免点（含理由，均为有意或无关）

| 文件:行号 | 命令 | 豁免理由 |
|---|---|---|
| `internal/updater/restart_windows.go:17` | 启动 staged 新版本 exe | 目标是 CodeBox 自身（Wails GUI 子系统 exe，`wails.json` 无 console 配置、release.yml:73 `wails build` 无 `-console`，产物 `build/bin/amagi-codebox.exe`）→ 无 console 闪窗。且 `DETACHED_PROCESS(0x10)` 为有意行为：helper 必须脱离父进程存活以等待父进程退出后换文件。**不改**。 |
| `internal/updater/windows_helper.go:120` | `startUpdatedExecutableFromHelper` 重启更新后主程序 | 同上：重启的是 GUI 子系统主程序本身，不分配 console。**不改**。 |
| `internal/updater/service.go:47` | `/bin/sh` helper 脚本 | 仅 macOS 更新流程（`downloadAndApplyMacAppBundle`）调用；`/bin/sh` 路径在 Windows 不存在。**darwin-only，不适用**。 |
| `internal/platform/system_proxy_darwin.go:14` | `scutil --proxy` | `//go:build windows` 之外的 darwin 实现，macOS 无控制台闪窗概念。 |
| `internal/envcheck/claude_selfheal.go:419` | `codesign -dv` | `runtimeGOOS != "darwin"` 早退守卫，macOS-only。 |
| `internal/launcher/service.go:419,476,534,548,565` | claude/codex/pi/omp/opencode 会话启动 | **任务边界豁免**（会话启动是另一切片）。核实无路径交叉：launcher/exec 均不引用 ProcessPolicy；嵌入式会话在 Windows 经 `internal/pty/service.go:336`（`//go:build windows`）`conpty.Start(commandLine,...)` 创建，完全不经 `exec.Command`/policy 层；terminal 模式是用户有意的可见终端。 |
| `internal/pty/service_darwin.go:328,353` | darwin PTY（creack/pty） | 伪终端 attach，无窗口概念；交互式会话绝不能隐藏。 |
| `internal/pty/service.go`（conpty.Start） | Windows ConPTY 会话 | CreateProcess + 伪控制台，非 exec.Command；交互式会话。**已验证与本次改动零交叉**（grep `applyProcessPolicy\|ProcessPolicy\|SuppressConsoleWindow` 在 internal/pty、internal/launcher 零命中）。 |
| `internal/headroom/service.go:219` | headroom 代理进程 | 走 `s.runner.Start(spec)`（ProcessRunner.Start → applyProcessPolicy），本次已自动获益，无需改动。 |
| 根目录 `*.go`、`cmd/`、`e2e/harness/` | — | grep `exec.Command` 零命中（根/cmd）；e2e harness 为开发期测试程序，非发布产物，不适用。 |

### 2.4 ExtensionsView 挂载链路静态证据（代替无法执行的 GUI 闪窗复现）

| 命令 | 链路（文件:行号） | 策略 |
|---|---|---|
| `claude plugin marketplace list --json` | plugin/service.go:44 → executor.go:47 `runner.Run` + `DefaultProcessPolicy()` | process_runner.go:94 `applyProcessPolicy` → **CREATE_NO_WINDOW + HideWindow** |
| `claude plugin list --json` | plugin/service.go:99 → 同上 | 同上 |
| `claude plugin list --json --available` | plugin/service.go:251 → 同上 | 同上 |
| `claude.cmd`（如解析到 .cmd/.bat/.ps1） | process_runner.go:71 `wrapWindowsScript` → `cmd.exe /c`（同一 cmd，policy 先 wrap 后应用，process_script_windows.go 保留 Policy 字段） | 同一 `applyProcessPolicy` → **CREATE_NO_WINDOW**（子进程继承不可见控制台） |
| envcheck 版本探测（若缓存过期触发） | envcheck/checker_*.go（claude:404,703 / codex:128 / opencode:130,214,246 / pi:132 / omp:140 / headroom:126 / fix_dispatcher:604）全部 `Policy: platform.DefaultProcessPolicy()` | 同上 |

## 3. 改动文件清单

修改（9）：
- `internal/platform/process_policy_windows.go` —— CREATE_NO_WINDOW + 互斥优先级 + `SuppressConsoleWindow`
- `internal/platform/process_policy_nonwindows.go` —— `SuppressConsoleWindow` no-op
- `internal/platform/system_proxy_windows.go` —— `regQuery` helper 收口 3 处
- `internal/platform/wsl.go` —— 2 个 wsl.exe lister
- `internal/platform/process_runner.go` —— killProcessTree 的 taskkill
- `internal/platform/path_lookup.go` —— shell fallback 统一防御
- `internal/wslsetup/service_windows.go` —— 3 处 wsl.exe
- `internal/ompconfig/service.go` —— runOmpModelsList
- `internal/gitassist/service.go` —— 3 处 git

新增（3）：
- `internal/platform/process_policy.go` —— 跨平台决策内核（常量 + 纯函数）
- `internal/platform/process_policy_test.go` —— 决策内核单测（任意平台可执行）
- `internal/platform/process_policy_windows_test.go` —— SysProcAttr 落位断言（`//go:build windows`；Linux 无法执行，已在文件头注明需 Windows 侧运行；Windows 构建编译已由 `GOOS=windows go vet` 验证，本次更在 Windows 上实际执行通过）

## 4. 验证证据

环境说明：WSL(Ubuntu) 内无 Linux Go（`go: command not found`），Windows 侧装有 Go 1.25.5（`/mnt/c/Program Files/Go/bin/go.exe`，经 WSLENV 传 GOCACHE/GOPATH）。因此「GOOS=windows 交叉编译」用原生 Windows go 等价完成（更强：原生目标平台）；「Linux 可跑测试」以 Windows 原生执行替代（严格超集：额外执行了 windows-tagged 测试）。验证末尾（清理 gofmt 副作用与并发切片共存后）又全量复跑一次 build/vet/test，结果不变。

| # | 验收项 | 命令 | 结果 |
|---|---|---|---|
| 1 | Windows 全量构建 | `go.exe build ./...` | ✅ `===BUILD-WINDOWS-OK===`（零输出退出 0） |
| 2 | vet 目标包（含测试文件类型检查） | `go.exe vet ./internal/platform/ ./internal/ompconfig/ ./internal/gitassist/ ./internal/wslsetup/` | ✅ `===VET-OK===` |
| 3 | 测试 | `go.exe test -count=1 ./internal/platform/... ./internal/ompconfig/... ./internal/gitassist/... ./internal/wslsetup/...` | ✅ platform 1.691s / gitassist 7.271s / wslsetup 1.581s；❌ ompconfig：`TestModelsConfigRoundTrip`「want 0600, got -rw-rw-rw-」——**预先存在的 Windows 环境失败，与本改动无关**（断言的是 `writePrivateFile` 的 POSIX 权限，Windows/NTFS 上 `os.Chmod` 无法表达 0600，Go 报告 0666；本任务对该文件的 diff 仅 import + runOmpModelsList 两行，未触碰权限逻辑；CI 全量 `go test ./...` 仅在 macOS 跑故从未暴露）。同包 `TestParseOmpModelsList`（覆盖本次触碰的 runOmpModelsList 输出解析）PASS。 |
| 4 | flags 单测 | `go.exe test -count=1 -v -run 'TestProcessPolicyCreationFlags\|TestApplyProcessPolicySysProcAttr' ./internal/platform/` | ✅ 全部 PASS（4 子用例：默认策略→CREATE_NO_WINDOW、仅 Detached→DETACHED_PROCESS、互斥组合 Detached 优先、零值无标志；SysProcAttr 落位断言在 Windows 真实执行） |
| 5 | 闪窗复现 | 无法在无 GUI 会话完成 | 以 §2.4 静态证据链代替：挂载链路所有命令均带 CREATE_NO_WINDOW 或等价抑制 |
| 6 | 最终态复验（清理后） | 重跑 `go.exe build ./...` / `vet`（4 包）/ `test -count=1`（4 包） | ✅ build OK；vet OK；platform/gitassist/wslsetup ok；ompconfig 仍仅预存失败 TestModelsConfigRoundTrip（同 §4-3） |

### 4.1 过程事件与清理（透明记录）

- **gofmt 误伤与清理**：中途误用 `go fmt ./internal/platform/ ./internal/wslsetup/ ...` 批量重写了 35 个文件（Windows gofmt 对含 CJK 注释文件的列对齐重排 + 行尾归一化）。逐一核对 diff：仅 `internal/wslsetup/service_test.go` 有真实内容变化（8 行 map 字面量纯空白重排，无功能内容）；其余 34 个均为零内容差的幻影 M 标记。已全部 `git checkout --` 还原到 HEAD（还原前已确认这些文件在本任务开始时无任何用户未提交修改，且还原的 diff 仅为 gofmt 产物）——恢复的是用户原始状态，未覆盖任何范围外改动。本任务 9+3 个文件的最终 diff 与还原前一致（已用 `git diff --stat` 复核，10 文件 66+/23- 全部属于本任务 + app.go 属并发切片）。
- **并发切片观察（按边界仅报告不改）**：验证期间工作区出现 `app.go` 与 `internal/launcher/wsl_agent_config.go` 的 WSL pi-config 功能改动（本 workflow 同名另一切片「wsl-pi-config」的在途工作）。本任务未触碰 app.go/launcher；两者的改动与本次共存下全量 `go build ./...` 仍通过。

## 5. 回滚说明

- 9 个修改文件均为纯增量（helper 调用 + import），`git checkout -- <files>` 即可回滚单点；3 个新增文件直接删除。
- 整体回滚：`git checkout -- internal/platform/ internal/wslsetup/ internal/ompconfig/ internal/gitassist/ && rm internal/platform/process_policy.go internal/platform/process_policy_test.go internal/platform/process_policy_windows_test.go`。
- 行为回退影响：回滚后恢复闪窗 Bug，无其他功能耦合（`SuppressConsoleWindow` 无既有调用方依赖；`Detached` 无调用方）。
- 副产物：`.tmp-tests/go-cache/`（Windows Go 构建缓存，目录 `.tmp-tests/` 任务前已存在且未跟踪）可整目录删除，无副作用。

## 6. 未覆盖项 / 剩余风险

- GUI 实机闪窗复现需用户在 Windows 桌面会话点击「扩展管理」目视确认（静态证据链已闭合，但按任务约定此处为唯一无法机器验证项）。
- `internal/launcher` 的 terminal 模式会话（独立可见终端）与嵌入式 ConPTY 会话属任务边界外，未做任何改动（已核实零交叉）。
- ompconfig `TestModelsConfigRoundTrip` 的 Windows 0600 断言失败为预先存在的环境性问题，建议后续单独切片处理（如 Windows 下断言降级或改用 ACL）。
- 本工作区为多 Agent 共享：app.go/launcher 的 WSL pi-config 切片在并发推进，合并时无需为本切片处理其改动。
