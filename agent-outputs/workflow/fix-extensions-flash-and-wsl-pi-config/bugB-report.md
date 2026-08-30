# Bug B 修复报告：WSL 模式 pi/omp 会话模型与提供商配置失效

**切片**: bugB-wsl-pi-config · **日期**: 2026-08-30 · **状态**: 完成
**关联诊断**: workflow.md §Bug B（运行日志 `~/.amagi-codebox/logs/amagi-codebox-2026-08-30.log`）

## 1. 根因回顾（采信 Leader 诊断 + 本切片复核）

CodeBox 是 Windows 进程，历史上 `LaunchPiSession`/`LaunchOmpSession` 把 models.json/models.yml 写到 **Windows 侧** `C:\Users\<user>\.pi\agent`；WSL 终端模式下 pi/omp 在 distro 内运行，读的是 **WSL 内** `/home/<wsluser>/.pi/agent/models.json` —— 两个文件系统，配置从未到达 CLI，`--provider amagi-glm` 命中 WSL 内旧配置（本机 WSL 侧实测存在 17 个旧 provider、无 amagi-glm）→ 401 `{"code":"1000"}`。

次因（本切片现场复核确认）：
- WSLENV 转发规则转发**所有 `PI_` 前缀**变量；实战环境存在携带 Windows 盘符路径值的 `PI_*` 残留（如 `PI_SESSION_FILE=C:\Users\...`），转发进 WSL 后是非法 Linux 路径，pi 会当相对路径在 cwd 建垃圾目录。
- WSL 内缺 `fd`/`rg`（本机实测 `command -v fd/fdfind/rg` 全空），`PI_OFFLINE=1` 下 pi 打印 "fd not found" 并跳过下载。

## 2. 写入方案选型：UNC 直写（而非 wsl.exe 内联 base64）

**选定：UNC 直写 + wsl.exe chmod 补偿。** 理由：

| 维度 | UNC 直写（选定） | wsl.exe 内联 base64 |
|---|---|---|
| 内容一致性 | 复用 `WritePiAgentConfig`/`WriteOmpAgentConfig` 的既有原子写范式（MkdirAll → Marshal → tmp → Rename），与 Windows 侧产物**同一序列化代码路径，逐字节一致** | 需另写 `echo <b64> \| base64 -d > file` 通道，序列化后绕一道 |
| 命令行长度 | 无限制 | CreateProcess ~32K 上限；多 provider/多模型 preset 配置 base64 膨胀 4/3 后可能超限 |
| merge 读回 | 直接读 UNC 路径现有文件，与非 WSL 路径同一条 `MergePiAgentConfig` 代码 | 需 `wsl.exe -- cat` 或混用 UNC 读 |
| 权限 | 9P 落盘 0755/0644，需 `wsl.exe chmod` 补偿（实测有效） | `umask 077` 可在创建时收紧 |
| 兼容性 | `\\wsl.localhost`（Win10 2004+）+ 旧别名 `\\wsl$` 双探测兜底；distro 在 home 探测阶段已被拉起 | 仅依赖 wsl.exe，兼容面等价 |

实证（本机 WSL2 Ubuntu + Windows Go 进程，见 §4）：UNC 上 `os.MkdirAll/WriteFile/Rename` 全部成功、文件 owner 为 distro 默认用户（chmod 可行）、0600 既有文件可读（merge 不受影响）——UNC 方案无短板，选型成立。

## 3. 改动文件清单

| 文件 | 类型 | 内容 |
|---|---|---|
| `internal/platform/wsl_home.go` | **新增** | `WSLUserHome`（`wsl.exe -d <d> -- sh -lc 'printf %s "$HOME"'`，按 distro 缓存含负结果）、`WSLToUNC`（`\\wsl.localhost` 优先/`\\wsl$` 兜底，探测缓存）、`WSLSearchToolStatus`（fd/fdfind/rg 一次探测+缓存，标记行解析）、`WSLChmod`（Linux 权限补偿，mode 白名单校验+路径单引号转义）、`EmbeddedLaunchTargetsWSL`（resolver WSL 分支的镜像判定：Windows + embedded 模式 + 有效 shell key=wsl）。wsl.exe fork 均带 `SuppressConsoleWindow`（与 bugA 切片同策略）。**未改 wsl.go** |
| `internal/platform/wsl_home_test.go` | **新增** | home 解析/缓存/非法值、UNC 映射/不可达、工具探测解析矩阵、chmod 脚本构造+转义+防御、WSL 判定门（跨平台可跑部分）——15 个用例 |
| `internal/launcher/wsl_agent_config.go` | **新增** | `WriteWSLPiAgentConfig`/`WriteWSLOmpAgentConfig`（resolve target → merge → 原子写 → chmod 补偿，chmod 失败 fail-closed）、`resolveWSLAgentTarget`、`StripWSLHostPathPIEnv`（剥离值为 Windows 盘符路径的 `PI_*` 变量）；platform 探测经包内接缝 var 注入（可测） |
| `internal/launcher/wsl_agent_config_test.go` | **新增** | 跨平台单测（fake WSL target 落本地 temp 目录）：merge 保留既有 provider/顶层字段、chmod 调用序列、全新 distro、chmod 失败 fail-closed、target 错误路径、env 剥离矩阵——6 个用例 |
| `internal/launcher/wsl_agent_e2e_windows_test.go` | **新增** | 真实 WSL E2E（`//go:build windows` + `AMAGI_WSL_E2E=1` 门控，CI 不跑）：真实 distro/home/UNC/写/chmod/读回 |
| `app.go` | 修改 | `LaunchPiSession`/`LaunchOmpSession`：启动前 `EmbeddedLaunchTargetsWSL` 判定 → WSL 分支写 WSL 侧（成功 `Provider=amagi-<name>`，失败 WARN+回退内置映射，不阻断）；embedded env 构建后 `StripWSLHostPathPIEnv` + `warnWSLSearchToolsOnce`；新增 `warnWSLSearchToolsOnce`（WARN 每进程一次）。非 WSL 分支逐行保持原状 |
| `docs/developer/architecture.md` | 修改 | 「会话类型与启动入口」下新增 **WSL 终端模式注意事项（pi/omp）** 小节 |

未动：`internal/platform/wsl.go`（bugA 切片在改）、`internal/pty/service.go`（PTY 构造零改动）、前端、`launch_planner.go`（外部终端模式 WSL 本就不启用）。

## 4. 真实 WSL 实证证据（本机 WSL2 Ubuntu，全部实际执行）

### 4.1 基础行为实证（探针程序 + PowerShell + wsl.exe）

```
$ wsl.exe -l -q          → Ubuntu、docker-desktop（后者被既有 reserved 过滤）
$ wsl.exe -d Ubuntu -- sh -lc 'printf %s "$HOME"'   → /home/maorun
$ wsl.exe -d Ubuntu -- sh -lc 'id -un'              → maorun
# PowerShell 经 \\wsl.localhost 写 + Move-Item 重命名：OK；\\wsl$ 别名：OK
# Windows Go 进程（GOOS=windows 交叉编译后运行）对 UNC：
#   MkdirAll/WriteFile/Chmod/Rename 全部 <nil> 成功，ReadFile 内容一致
#   但 Linux 侧 stat：目录 755、文件 644（Go os.Chmod 经 9P 不改 POSIX 位）
#   文件 owner = maorun:maorun（与 wsl.exe chmod 运行身份一致 → 补偿可行）
#   读取既有 0600 auth.json（2742B）：成功（merge 读回无障碍）
# wsl.exe chmod 700/600 补偿后：stat 确认 700/600 ✓
```

### 4.2 生产代码路径 E2E（`AMAGI_WSL_E2E=1 go test -run TestManualRealWSL ./internal/launcher/`，GOOS=windows 测试二进制实跑）

```
--- PASS: TestManualRealWSLUserHome (0.24s)
    distro=Ubuntu home=/home/maorun unc=\\wsl.localhost\Ubuntu\home\maorun
--- PASS: TestManualRealWSLWritePiConfig (0.35s)
    written to /home/maorun/.pi/agent (distro Ubuntu)
    models.json content OK (15882 bytes, providers: [amagi-glm amagi-公司中转站
    opencode-go zhipuai amagi-Grok amagi-anthropic ... amagi-wsltest anthropic-relay
    tbrouter])   ← 17 个既有 provider 全保留（merge 语义在真实数据上验证）+ 新增 amagi-wsltest
--- PASS: TestManualRealWSLSearchTools (0.14s)
    distro=Ubuntu fd=false ripgrep=false   ← 与用户日志 "fd not found" 吻合
# WSL 侧核验：stat → 700 maorun:maorun /home/maorun/.pi/agent；600 models.json
# 内容核验：amagi-wsltest.baseUrl=https://open.bigmodel.cn/api/coding/paas/v4，
#           api=anthropic-messages，apiKey 已内嵌；顶层字段保留
# 测试后快照恢复：md5 a367ec17e7485e2a83cd6db8322483c5 与测试前一致，权限还原 755
```

### 4.3 引号注入

写入内容经 UNC 文件 API 传递（不经 shell），无引号面；仅 chmod 走 `sh -c`，路径用 POSIX 单引号 + `'\''` 转义（单测覆盖含空格/单引号路径）；mode 经白名单字符校验（含 `; & | ' " $` 即拒绝，单测覆盖 `600; rm -rf /`）。

## 5. 冒烟推演：LaunchPiSession（shell=WSL, mode=embedded）决策链前后对比

| 决策点 | 修复前 | 修复后 |
|---|---|---|
| WSL 判定 | 无（写侧不感知 shell） | `EmbeddedLaunchTargetsWSL("embedded","wsl")` → true；`DefaultWSLDistro` → Ubuntu |
| models.json 写入 | `C:\Users\毛润\.pi\agent\models.json`（WSL 内 pi 不可见） | `WriteWSLPiAgentConfig` → merge 读 `\\wsl.localhost\Ubuntu\home\maorun\.pi\agent\models.json`（17 个既有 provider 保留）→ 原子写 → chmod 700/600 |
| merge 语义 | 仅 Windows 侧文件 | WSL 侧文件同样「保留已有 providers/顶层字段，amagi-* 当次优先」 |
| `launchSettings.Provider` | amagi-glm（但配置未达 WSL → 401） | amagi-glm（配置已在 WSL 侧生效）；WSL 写失败 → WARN + 回退内置映射（env 兜底经 WSLENV 有效） |
| `PI_CODING_AGENT_DIR` | override `""` 删除（BuildEnv 层） | 同左（保持）；且 `StripWSLHostPathPIEnv` 兜住系统环境残留的 Windows 路径值 `PI_*`（如 `PI_SESSION_FILE=C:\...`） |
| `PI_OFFLINE` | `1`（默认） | `1`（保持转发，避免启动期联网卡住） |
| WSLENV 清单（进入 wsl.exe 前） | `ANTHROPIC_API_KEY:PI_OFFLINE` + 可能泄漏的 Windows 路径值 `PI_*` | `ANTHROPIC_API_KEY:PI_OFFLINE`（Windows 路径值变量已被剥离，名字亦不入 WSLENV） |
| fd/rg | 无处置（裸警告无解释） | 一次探测+缓存；缺失 → 日志 WARN `[CodeBox] distro=Ubuntu 缺少 fd/ripgrep… sudo apt install fd-find ripgrep`（每进程一次）+ docs/developer/architecture.md WSL 小节 |
| PTY 命令行 | `wsl.exe -d Ubuntu --cd "X:\..." -- bash -lic "'pi' '--provider' 'amagi-glm' ..."` | 完全相同（pty/service.go 零改动） |
| omp | 同构问题（models.yml 写 Windows 侧） | `WriteWSLOmpAgentConfig` 对称修复（.omp/agent/models.yml） |

非 WSL 模式（Windows pwsh/cmd、macOS、外部终端/VSCode/Zed）：`EmbeddedLaunchTargetsWSL` 为 false → 走原 else 分支，行为逐行不变（对照实现：WSL 写分支整体包裹在 `if wslDistro != ""` 内）。

## 6. 验证证据汇总

| 项 | 命令 | 结果 |
|---|---|---|
| Windows 构建 | `go build ./...`（Windows Go 工具链，GOOS=windows 隐含） | ✅ BUILD-OK |
| vet | `go vet ./internal/launcher/ ./internal/piconfig/ ./internal/platform/ .` | ✅ VET-OK |
| 根包测试编译（=CI windows-latest 形态） | `go test -run '^$' ./` | ✅ ok |
| launcher WSL 单测（跨平台） | `go test -run 'TestWriteWSL\|TestResolveWSLAgentTarget\|TestStripWSL' ./internal/launcher/` | ✅ 6/6 PASS |
| platform 全量（含 bugA 在制 wsl.go 改动共存） | `go test ./internal/platform/` | ✅ ok（44+ 用例，含既有 WSL/resolver 用例） |
| pty WSL 相关 | `go test -run 'TestBuildWSL\|TestWorkDir\|TestBashSingleQuote\|TestDrvFS' ./internal/pty/` | ✅ ok |
| 真实 WSL E2E | §4.2 | ✅ 3/3 PASS |
| 回归隔离证明 | 独立 worktree 检出 HEAD(1344732) 跑 `TestWrite.*Perms` + piconfig RoundTrip | ❌ 同样 6 项失败 → **预存 Windows 宿主产物**（Go 在 Windows 报 0666/0777 mode 位；CI 在 macOS 跑这些用例通过），与本切片无关 |

**注**：`go test ./internal/launcher/` 在 Windows 宿主整体报 FAIL，即上表最后一行的 4 个预存权限断言用例（`TestWrite{Pi,Omp}AgentConfig{TightPerms,UpgradesLegacyPerms}`）+ piconfig 2 个 —— 已用 HEAD worktree 证明先于本切片存在。macOS CI（真正跑全量的平台）不受影响；本切片新增用例在两平台均可跑（Linux CI 验证过 GOOS 无关性——单测不依赖 runtime.GOOS=windows 的分支）。

## 7. 边界与已知限制

- **用法统计不跨边界**：WSL 会话的 session JSONL 落在 WSL 内 `~/.pi/agent/sessions`，`internal/usage` 仍读 Windows 侧（范围外，如需 WSL 用量摄取另立切片）。
- **后台 provider sync 仍写 Windows 侧**：`syncProvidersToHarnessesLocked` 维护 Windows 侧 `~/.pi/agent`；WSL 侧只在启动时合并写入（与修复前 Windows 侧的启动写入语义对等）。
- **chmod 失败 fail-closed**：WSL 写入的 chmod 补偿失败即整体失败并回退内置 provider（0600 契约优先于 amagi 条目可用性）；Windows 侧 `WritePiAgentConfig` 对 chmod 错误同语义。
- **fd/rg 提示通道**：无会话启动期 warning UI 通道（前端 attach 晚于启动、PTY 输入注入会打字进用户终端），按任务允许的降级路径采用日志 WARN（每进程一次）+ developer 文档；探测按 distro 缓存不重复 fork。
- **与 bugA 切片集成**：`wsl_home.go` 调用了其新增的 `platform.SuppressConsoleWindow`（同工作树实测共存构建/测试通过）；若该切片被回滚，需同步移除这一调用（唯一交叉点）。

## 8. 回滚说明

全部改动为**新增文件 + app.go 两处方法内分支 + 文档小节**，无数据迁移、无契约变更：

1. 还原 `app.go`（`git checkout -- app.go` 或摘除 `wslDistro` 判定/WSL 写分支/env 剥离三块）即回到旧行为（写 Windows 侧）；
2. 删除 5 个新增 `.go` 文件与 `docs/developer/architecture.md` 的 WSL 小节；
3. WSL 侧写入物（`~/.pi/agent/models.json` 等）按 merge 语义可向后兼容：回滚后旧版 CodeBox 只写 Windows 侧，WSL 侧文件成为无人维护的陈旧配置（对 pi 自身无害，可手工删除 `amagi-*` 条目或整文件）；
4. `internal/platform/wsl.go` 与 PTY 层零改动，无连带回滚。
