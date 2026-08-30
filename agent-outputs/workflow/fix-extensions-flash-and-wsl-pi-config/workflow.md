# Workflow: 扩展管理闪窗修复 + WSL 模式 pi 配置失效修复

**日期**: 2026-08-31 · **自治级别**: interactive · **状态**: ✅ 完成（待主上实机验收）

## 目标与总验收

1. **Bug A**: 桌面端每次点击侧边栏「扩展管理」，不再弹出任何一闪而过的终端窗口（当前每次 4 个）。
2. **Bug B**: 终端 shell 选择 WSL 模式启动 pi 会话后：
   - 模型/提供商配置生效（无 401 身份验证失败）；
   - `fd/ripgrep not found` 警告有明确处置路径（检测+提示/安装引导），不留无解释的裸警告；
   - 不向 WSL 转发无效的 Windows 路径值（PI_CODING_AGENT_DIR）。

## 诊断证据（Leader 侦察结论）

### Bug A
- `frontend/src/views/ExtensionsView.vue` 默认 tab=plugins/engine=claude，挂载 `PluginInstalledPanel` → `loadCcAllData()` 并行 3 个后端命令：
  - `GetInstalledPlugins` → `executeClaudeCommand("plugin","list","--json")`
  - `GetMarketplaces` → `executeClaudeCommand("plugin","marketplace","list","--json")`
  - `GetAvailablePlugins` → `executeClaudeCommand("plugin","list","--json","--available")`
- `internal/platform/process_policy_windows.go` 的 `applyProcessPolicy` 仅设 `HideWindow: true`。**Windows 11 默认终端 = Windows Terminal 时该标志不能阻止新终端窗口**，必须 `CreationFlags |= CREATE_NO_WINDOW (0x08000000)`。
- 另存在多处裸 `exec.Command` 完全无窗口策略（均为 console 子系统 exe，GUI 父进程下必弹窗）：
  - `internal/platform/system_proxy_windows.go` reg query ×3（ProxyEnable/ProxyServer/ProxyOverride）
  - `internal/platform/wsl.go:19/45` wsl.exe 探测（-l -q / -l -v）
  - `internal/wslsetup/service_windows.go:44/51/62` wsl.exe ×3
  - `internal/ompconfig/service.go:348` `omp models ls --json`
  - `internal/platform/process_runner.go:255` killProcessTree 的 taskkill
  - `internal/gitassist/service.go:76/430/469` git 命令（GitAssist 同类闪窗）
  - `internal/platform/path_lookup.go:510` shell fallback（Windows 分支当前不走，统一防御）
  - `internal/updater/windows_helper.go:120`、`restart_windows.go:17`（语义需确认：restart 主程序是 GUI 不闪窗，helper 探测若为 console exe 需处理）

### Bug B（运行日志实锤，`~/.amagi-codebox/logs/amagi-codebox-2026-08-30.log`）
```
[pi] 已写入 pi models.json | provider=amagi-glm baseURL=... -> C:\Users\毛润\.pi\agent/models.json
[session] Pi 会话身份已预留 | id=422aa68e provider=amagi-glm model=glm-5.3 mode=embedded
[pi] envOverrides keys | [PI_CODING_AGENT_DIR PI_OFFLINE ANTHROPIC_API_KEY]
[pty] 创建 ConPTY 会话 | cmd=wsl.exe -d Ubuntu --cd "X:\WorkSpace\amagi-codebox" -- bash -lic "'pi' '--provider' 'amagi-glm' ..."
```
- 根因：CodeBox（Windows 进程）写 **Windows 侧** `~/.pi/agent/models.json`；WSL 模式下 pi 在 Ubuntu 内运行，读的是 **WSL 内** `/home/<user>/.pi/agent/models.json`——两个文件系统，配置从未到达 pi。`--provider amagi-glm` 命中 WSL 内旧配置（key 缺失/失效）→ 401 `{"code":"1000"}`（智谱错误码）。
- 次因：`PI_CODING_AGENT_DIR`（Windows 路径值）经 WSLENV 转发进 WSL 后是非法 Linux 路径（会被当相对路径在 cwd 建垃圾目录）；`PI_OFFLINE=1` 转发生效（warning 佐证 env 链路通畅），WSL 内缺 fd/rg → pi 打印 offline warning。
- omp（`~/.omp/agent`，models.yml）为同构问题，需对称修复。

## Phase 表

| Phase | 目标 | 切片 | Agent | 依赖 | 验收 | 状态 |
|---|---|---|---|---|---|---|
| P1-A | 消除所有后台子进程闪窗 | bugA-flash-windows | luban | 无 | 全量 Windows exec 点带窗口抑制策略；GOOS=windows go build ./... 通过；go test 相关包通过 | ✅ done 验收通过 |
| P1-B | WSL 模式 pi/omp 配置生效 | bugB-wsl-pi-config | luban | 无 | WSL 侧 models.json/models.yml 写入+合并语义正确；PI_CODING_AGENT_DIR 不再转发 Windows 路径；fd/rg 缺失有检测+提示；构建+单测通过 | ✅ done Leader 抽查通过 |
| P2 | 阶段集成审核 | gate-review | diting | P1-A+P1-B | diff 级审核两切片 | ✅ CONCERNS→修复闭环（M1/m1/m2/m4/m5 已修，m3 暂缓记录） |
| P2R | 事故恢复 | resume×2 | luban×2 | P2 | 丢失的 6 文件重建与上轮逐字一致；全量集成验证通过 | ✅ done |

## 并行与隔离
- P1-A / P1-B 文件集基本不相交，**并行派发**。唯一软冲突：`internal/platform/wsl.go` —— A 改（补 HideWindow），B 只读复用；B 需要新增 WSL 探测能力时放**新文件**（如 `internal/platform/wsl_home.go`），禁止改 wsl.go。
- 两者都可能碰 `app.go`：A 原则上不碰 app.go（若发现闪窗源头在 App 编排层，报告而非改）；B 允许改 app.go 的 LaunchPiSession/LaunchOmpSession 链路。

## 风险与决策
- CREATE_NO_WINDOW 与现有 Detached(0x10) 组合语义需厘清；不得影响 PTY/launcher 交互式会话（那些不走 ProcessRunner）。
- WSL 写入用 `\\wsl.localhost\<distro>...` UNC 还是 `wsl.exe sh -c` 内联写：由 B 按权限（0600/0700 Linux 语义）与可靠性取舍，需在 artifact 记录理由。
- 真实 WSL 环境可直接实证（本机即 WSL2 Ubuntu），B 必须用真实环境验证 UNC 写入/权限行为。

## 修订日志
- 2026-08-31 创建。Leader 侦察完成诊断，双切片并行派发。
- 2026-08-31 P1-A 回流验收通过；P1-B 回流验收通过；diting 集成审核 CONCERNS（M1 CI 阻断 + m1-m5 Minor）。
- 2026-08-31 **事故恢复闭环**：两个 resume 重建与上轮逐字一致（diff-stat 逐文件行数比对）；最终集成验证：Windows 全量 build ✅、vet（Windows+Linux+Darwin 三目标）✅、platform/gitassist/wslsetup 测试全绿、launcher 133 PASS（4 个预存 NTFS 权限断言失败与本次改动无关，三方交叉证实）、ompconfig 同类预存 1 例。终态 12 files 196+/37- 与事故前对齐（含 .gitignore）。M1 修复经 Windows 实跑 PASS + 非 Windows vet 编译验证 + 守卫逻辑静态判定三方闭合。
- 2026-08-31 P1-B 回流验收通过：UNC 直写+chmod 补偿（fail-closed）、PI_* 盘符路径值剥离、fd/rg 探测+WARN+docs；真实 WSL2 E2E（17 provider 保留、权限 700/600、测试痕迹 md5 级还原）。与 P1-A 集成共存构建通过（wsl_home.go 复用 SuppressConsoleWindow）。进入 P2 diting。
