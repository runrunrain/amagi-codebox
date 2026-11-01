# P2-C 安装/升级双侧联动 — 实现报告

- Worktree：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/windows-wsl-install`（分支 `amagi/windows-wsl-install`，未提交，按任务书不 commit）
- 上游：`agent-outputs/workflow/windows-wsl-unified/{workflow,research,direction}.md`（全文已读）；按 direction §2 + §4 S-C 实现；steer #1/#2/#3 修正已吸收。
- 基线：worktree HEAD `6d1c7f5`，`internal/envcheck` 与 `app.go` 装配段起点干净（本任务前无未提交改动落在本写面）。

## 自己产出（Leader 只集成此清单）

| 文件 | 内容 |
|---|---|
| `internal/envcheck/types.go` | `WSLSyncResult` 结构 + 状态常量（succeeded/failed/skipped）；`InstallResult.WSL *WSLSyncResult`（omitempty，nil=本次不涉及 WSL）；`OperationStepWSLSync "wsl_sync"` |
| `internal/envcheck/service.go` | `wslCLISync` 字段 + `SetWSLCLISync`（s.mu setter 范式，与 SetWSLSearchToolsInstaller 同构）；`attachWSLSync`（门控：Windows 成功 + kind∈{install,update} + tool≠headroom + 回调非nil；reporter 先报 wsl_sync 步骤再执行）；三个调用点：`runOperation`（CheckOne 刷新后、取 opMu 前，带 reporter——覆盖 StartInstall/StartUpdate/StartClaudeWithMethod/fix install_tool）、`serializedInstallOrUpdate` 与 `serializedInstallOrUpdateWithMethod`（成功后、清槽前，不传 reporter） |
| `internal/envcheck/installer.go` | `progressWSLSync = 92` 常量（verify 85 与 refresh 95 之间；monotonic 上报保证不回退） |
| `internal/envcheck/fix_dispatcher.go` | `install_claude_method` 分支：`result.WSL.Status==failed` 时错误写入 `FixActionResult.Error` 并追加 Message（Success 仍只表示 Windows 侧） |
| `app_wsl_cli_sync.go`（新） | `syncCLIToWSL`（envcheck 回调实现：DefaultWSLDistro 空→nil；shell=wsl 或 WSL 已装才同步否则 skipped；install未装→InstallTool、install已装或update→UpdateTool；claude_code→claude、omp→omp 键映射；快照只在 Available 且 Distro==当前目标时才采信 installed——steer#2）；`wslSyncResultFrom`（err!=nil 强制 failed 且保留 err/res.Error 原文——steer#2）；`UpdateCLIInWSL` 绑定（WSL 卡片升级按钮用，按钮/api/wslsetup.ts 归 Leader）；接缝 `wslCLISetupFor`/`wslShellSettingFor`（包级 var，镜像 wslDefaultDistroFn 测试范式） |
| `app.go` | 装配段一行（SetWSLSearchToolsInstaller 块后）：`envCheckSvc.SetWSLCLISync(app.syncCLIToWSL)`（+3 行注释） |
| `frontend/src/views/settings/envcheckSolutions.ts` | `WSLSyncLike` 类型；`OperationLike.result.wsl`；`ResultBanner.type` 增加 `'warning'`；`operationCompletionBanner` succeeded 分支按 wsl.status 分流：failed→warning 横幅「Windows 成功，WSL（{distro}）同步失败」（绝不显示纯成功）、succeeded→追加「WSL 已同步 v…」、skipped→追加原因、缺失→与旧行为逐字节一致 |
| `frontend/src/views/settings/EnvCheckSettings.vue` | 仅 `.op-result.result-warning` CSS 5 行（OperationResult 接口已含 'warning'；未动 WSLCLISettings.vue——另一 Agent 写面） |
| `internal/envcheck/wsl_cli_sync_test.go`（新） | 见下 |
| `app_wsl_cli_sync_test.go`（新） | 见下 |
| `frontend/src/__tests__/views/settings/envcheckSolutions.test.ts` | 新增 describe：wsl failed→warning（断言非 success）/succeeded→追加/skipped→原因/缺失→旧行为/Windows 失败终态不受 wsl 影响 |
| `docs/developer/wsl-cli-install-sync.md`（新） | 链路目标、触发条件、分派表、结果语义、部分失败重试、消费者审计 |

## 依赖带入（非本任务产出，编译/测试所需）

- `internal/wslsetup/**` 全目录：先从 P2-A worktree（`../windows-wsl-setup`）复制，steer#2 后改从**主树** `/Users/run.mao/workpace/amagi-codebox/internal/wslsetup` 复制（含 Leader 的 30s 默认 probe 预算补丁），`diff -rq` 与主树逐一相同，目录 sha256 汇总 `9ae986699f3e13326ecc1daa5bfc11dad954492dfa23524d6a8c2715f453fa13`。删除的旧文件：`install_search_tools_windows_test.go`、`service_other.go`。**Leader 集成时以主树/P2-A 合并版为准，不采用本副本。**
- 环境配置（gitignored，非源码）：`frontend/dist`、`mobile/dist` 从主树 `cp -R`（go:embed 需要；符号链接不被 embed 接受）。

## 验证证据

| 命令（cwd=worktree） | 结果 |
|---|---|
| `go test ./internal/envcheck/ -run TestAttachWSLSync -count=1` | ok（含 -race 再跑 ok） |
| `go test ./internal/envcheck/ -count=1` 全包 | ok 38.1s（无回归） |
| `go test ./internal/wslsetup/ -count=1`（依赖副本） | ok |
| `go test . -run 'TestSyncCLIToWSL\|TestUpdateCLIInWSL\|TestApp\|TestBind\|TestEnvCheck\|TestRunEnvFix' -count=1` | ok（含 -race 于 TestSyncCLIToWSL） |
| `go vet ./...` | clean |
| `GOOS=windows go build ./...` | clean |
| `npx vue-tsc --noEmit`（frontend） | exit 0 |
| `npx vitest run src/__tests__/views/settings/` | 3 files / 53 tests 全过 |

入口级测试覆盖（任务书要求逐项）：

- **StartInstall/StartUpdate**（async）：成功后 `final.Result.WSL` 已填、kind 正确、回调内观察 `Step==wsl_sync`（步骤可见性）。
- **sync Install/Update/WithMethod**：三入口结果都带 WSL（WithMethod 用 nativeBootstrap 夹具真实成功链）。
- **fix**：`install_claude_method` WSL 失败→ `FixActionResult.Error` 非空且追加 Message；`install_tool` 经 runOperation 由 async 用例覆盖。
- **双侧失败**：async Windows 成功+WSL failed → `Status==succeeded`（Windows 语义不变）且 `Result.WSL.Status==failed` 错误原文保留；App 层 res+err 双非 nil → 强制 failed（steer#2 回归）。
- **门控**：非 wsl shell 且未装→skipped（零安装器调用）；已装（可信快照）→UpdateTool；未装+shell=wsl→InstallTool；无 distro→nil；headroom/uninstall kind→nil；快照不可用/异发行版→不判已装（steer#2 回归）；omp 键映射。
- **Windows 失败不触发**：回调零调用、WSL 保持 nil；未注入回调全入口行为不变（nil 安全）。

## 消费者审计（R3 不漏报）

- 桌面前端：唯一安装横幅路径 `operationCompletionBanner`（已改）；sync `installTool/updateTool` API 桌面 UI 未直接调用（仅异步链）。
- `internal/remote`、`mobile/`：grep `StartInstall|InstallResult|OperationState|envcheck` 无安装面消费（2026-10 审计），无漏报面；新增 `wsl` 字段 omitempty 对旧消费者零影响。
- fix 面：`install_claude_method` 已传播（见上）；`install_tool` 转异步链由横幅覆盖。

## 未覆盖 / 已知边界

- Windows + WSL2 真机行为（npm 网络失败、升级幂等、WSL1）标 U，归 P3 集成验证；本会话 macOS 仅单测面。
- bindings 未生成（按任务书 Leader 统一做）：前端逻辑用本地兼容类型 `WSLSyncLike`，不依赖生成产物即编译通过；`UpdateCLIInWSL` 的 wailsjs 绑定与 WSL 卡片「升级」按钮、`api/wslsetup.ts` 归 Leader 集成。
- e2e fixture（wsl-success/failure/skipped）与 browser smoke 由 Leader 负责（steer#1）。

## 回滚

全部为增量改动：revert 上述「自己产出」文件清单即可；依赖副本 `internal/wslsetup` 回滚为主树版本即恢复原状。运行时无状态迁移、无配置格式变化；旧版本二进制读新 `InstallResult`（多一个 omitempty `wsl` 字段）无影响。
