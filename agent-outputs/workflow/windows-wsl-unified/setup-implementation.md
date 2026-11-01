# P2-A wslsetup 五 CLI 安装/升级完整切片：实现报告

> 角色：luban（P2-A）。作业 worktree：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/windows-wsl-setup`（分支 `amagi/windows-wsl-setup`，未提交，按任务书留给 Leader 汇总）。
> 上游输入：`agent-outputs/workflow/windows-wsl-unified/research.md`（P1 调查，主树）+ `agent-outputs/workflow/windows-wsl-unified/workflow.md` + 任务书（P2-A：仅 `internal/wslsetup/**`，不改 root/envcheck/frontend）。

## 1. 需求 → 产物映射

| 任务书要求 | 落点 | 状态 |
|---|---|---|
| 补 omp npm 包与 status 支持 | `service.go`：`cliPackages["omp"] = "@oh-my-pi/pi-coding-agent"`（与 envcheck `installer.go:73 ompNPMPackageName` 对齐）；新增 `cliToolOrder` 五键；`service_flow.go GetStatus` 循环 `cliToolOrder`（自动含 omp） | ✅ |
| 新增 `UpdateTool(tool string) (*InstallResult, error)`，Windows 真实执行升级；已装不短路、未装可安装 | `service_flow.go`：`UpdateTool` → 共享脊柱 `installOrUpdate(tool, opUpdate)`，npm 步骤固定 `npm i -g <pkg>@latest`，无 AlreadyOK 短路 | ✅ |
| 保持 InstallTool 签名与幂等语义 | `InstallTool` 签名不变；AlreadyOK 短路 + dirty-snapshot 修复块逐字保留（`TestInstallTool*` 移植测试钉住） | ✅ |
| 其它平台一致 stub | 非 Windows `wslRunnerImpl == nil` → `InstallTool`/`UpdateTool`/`InstallSearchTools`/`GetStatus` 返回结构化失败（沿用旧 `service_other.go` 的错误串契约；`TestUnsupportedPlatformContract` 钉住）。`service_other.go` 删除（行为并入 nil-runner 路径，避免双 Service 定义） | ✅ |
| 复用 ensureNode/npm prefix/login resolution，不重复流程 | 单一脊柱 `installOrUpdate` 复用 `ensureNode`/`ensureUserNpmPrefix`/`checkLoginResolution`；安装/升级仅差 npm spec（`pkg` vs `pkg@latest`）、日志标签与失败前缀 | ✅ |
| 同一 distro 保持身份；防并发冲突；不用全局锁 | distro 在操作开始解析一次，全操作（含锁键）用同一名字（`TestOperationHoldsOneDistroIdentity`）；`distroLocks sync.Map` per-distro 互斥（`TestSameDistroOperationsSerialize`、`TestDifferentDistrosDoNotBlock` 验证同 distro 串行、跨 distro 不阻塞） | ✅ |
| 可移植测试接缝（skipWithoutWSLDistro 不能是唯一证据） | 新增 `wslRunner` 接缝（`runner.go`）：Windows `init()` 接真实 wsl.exe（`service_windows.go`），测试任意 OS 注入 `fakeWSL` 驱动真实 `InstallTool/UpdateTool/GetStatus` 业务流——原 windows-only + skip 测试全部移植为无 skip 可移植测试 | ✅ |
| 五 CLI 映射验证 | `TestPackageForTool`（含 omp）、`TestAllFiveCLIsMapped`、`TestGetStatusReportsFiveCLIs` | ✅ |
| 已装升级不跳过 | `TestUpdateToolUpgradesInstalledCLI`：npm 脚本必含 `@latest`、AlreadyOK=false、验证链仍跑 | ✅ |
| 安装已装保持语义 | `TestInstallToolPiAlreadyOKIdempotent`、`TestInstallToolEffectiveShortCircuitUnchanged`（含"短路不跑 npm"断言） | ✅ |
| 命令失败/登录 shell 解析失败真实报错 | `TestCommandFailuresReported`（npm install/upgrade 失败、NodeSource 失败三路）；`TestInstallToolEffectivenessFailureReported`、`TestInstallToolAlreadyOKUnrepairableReportsFailure`、`TestCheckLoginResolution`（4 分支，探针错误=不确定不误伤） | ✅ |
| 非 Windows stub | `TestUnsupportedPlatformContract`（显式注入 nil runner，在 Windows 主机上也确定） | ✅ |
| macOS 按 runner 注入跑实际业务流 | 全部业务流测试即为注入 runner 的 macOS 可运行测试（本机已跑）；边界：真实 wsl.exe 装配（`service_windows.go`）仅 Windows 编译验证 + `TestRealRunnerWiredOnInit` 结构断言，真机行为归 P3/W 阶段 | ✅（边界已写明） |
| 禁止真实安装/升级/触碰用户目录 | 测试全部走 fake runner，零 wsl.exe 调用、零用户目录读写；本会话未安装/升级任何 CLI | ✅ |
| 安装 timeout 原有问题若影响升级需处理 | 新增 `npmOpTimeout`(10m)/`nodeInstallTimeout`(15m)/`aptOpTimeout`(10m)；npm 步、NodeSource 链、apt 步走 `exec.CommandContext` 超时（`service_windows.go runWsl`），超时返回真实错误（防止挂死操作且永久占用 distro 锁）；探针保持不限时。`TestLongStepsBoundedByTimeouts` + `TestInstallSearchToolsInstallFlow` 钉住超时接线 | ✅ |
| 新需求包含 omp，历史 B7 不阻断 | 未做任何 B7 条件判断；omp 直进安装面（`service.go` 注释注明 superseding） | ✅ |
| 不改 root/envcheck/frontend、worktree 不提交 | git status：改动仅 `internal/wslsetup/**`（见 §3）+ 本报告 + gitignore 构建产物（见 §5）；无 commit | ✅ |

## 2. 架构变化（包内重构，对外零契约变化）

```
service.go          类型 + cliPackages(+omp) + cliToolOrder + packageForTool   [可移植]
runner.go  (新)     wslRunner 接缝：defaultDistro/distroVersion/execUser/
                    execRoot/execLogin/searchStatus/resetSearchCache          [可移植]
service_flow.go(新) 全部业务流：Service/NewService、GetStatus、InstallTool、
                    UpdateTool、installOrUpdate 脊柱、ensureNode、
                    ensureUserNpmPrefix、checkLoginResolution、
                    InstallSearchTools、distro 锁、超时常量                    [可移植]
service_windows.go  缩为真实装配：init() 接线 + runWslUser/Root/Login +
                    runWsl(CommandContext 超时) + userBinPathPrefix           [windows]
service_other.go    删除（nil-runner 路径覆盖其契约）
common.go           不变
```

对外方法面（root `app.go` 消费的 `NewService/GetStatus/InstallTool/InstallSearchTools`）签名与语义不变；`UpdateTool` 为新增（本轮不接 App/envcheck——那是切片 3 的工作面）。`InstallResult` 结构体未改字段。

## 3. 改动文件清单（worktree，未提交）

- 修改：`internal/wslsetup/service.go`、`internal/wslsetup/service_windows.go`（483→90 行）、`internal/wslsetup/service_test.go`
- 新增：`internal/wslsetup/runner.go`、`internal/wslsetup/service_flow.go`、`internal/wslsetup/service_flow_test.go`、`internal/wslsetup/service_concurrency_test.go`、`internal/wslsetup/service_search_tools_test.go`
- 重写：`internal/wslsetup/service_windows_test.go`（385→24 行，仅留 init 装配断言）
- 删除：`internal/wslsetup/service_other.go`、`internal/wslsetup/install_search_tools_windows_test.go`（两者行为/测试均已移植到可移植文件）
- 新增报告：本文件

净变化（git diff HEAD -- internal/wslsetup）：+151 / −1016 行；测试由"仅 Windows 且需真实 distro 才跑"变为任意 OS 全量可跑。

## 4. 验证证据（真实命令与结果）

| 验证 | 命令（cwd=worktree） | 结果 |
|---|---|---|
| 包全量测试（macOS，无 skip） | `go test ./internal/wslsetup/ -v -count=1` | ok（29 个 PASS 条目含子测试全过） |
| 竞态 | `go test ./internal/wslsetup/ -race -count=1` | ok |
| CI 同款静态检查 | `go vet ./...`（先 `gofmt -l internal/wslsetup/`） | exit 0 / 无输出 |
| 全模块构建 macOS | `go build ./...` | ok |
| 全模块构建 Windows | `GOOS=windows go build ./...` | ok |
| Windows 包+测试编译 | `GOOS=windows go vet ./internal/wslsetup/`、`GOOS=windows go build ./internal/wslsetup/` | ok |
| root 消费面测试 | `go test . -run 'WSL|InstallCLIToWSL' -count=1` | ok（2.74s） |

未运行项：真机 Windows/WSL 行为（本会话 macOS，且任务书禁止真实安装/升级）——归 P3 集成验证与 W 阶段真机用例；`go test ./...` 全量留给 harness 结算基线（我已确保 worktree 内可编译，见 §5）。

## 5. 环境侧说明（非源码改动）

worktree 缺 `frontend/dist`/`mobile/dist`（gitignore 的构建产物，主树有），root `main.go` 的 `//go:embed` 会因此编译失败——这是先决状态，与本任务无关。为使结算基线 `go test ./...` 可执行，已从主树拷贝两份 dist 到 worktree（gitignore 内，不入 git；embed 不接受符号链接，实测后退回拷贝）。

## 6. 未覆盖项 / 剩余风险

1. **真机 WSL 行为全部标 U**（macOS 只读环境惯例）：omp 实装、`@latest` 升级幂等、超时后 wsl.exe 杀进程语义（kill wsl.exe 断开会话，distro 内 bash 可能短暂残留）、npm 无网失败语义——均需 P3/Windows 真机验证。
2. **UpdateTool 尚无 App/envcheck 接线**：按任务书切片边界，`App.UpdateCLIToWSL` 包装、envcheck 成功路径注入回调、前端按钮与 wailsjs 再生属切片 3（P2-B/C），本轮不碰。
3. **锁粒度**：per-distro 互斥意味着同一 distro 内"装 A 工具"会等待"升级 B 工具"完成——这是 npm 共享 `~/.npm-global` 前缀的必然约束（并发 `npm i -g` 会损坏前缀）；跨 distro 与其它工作完全不受阻。`InstallSearchTools`（apt，root）未纳入锁：apt 自带锁且与 npm 前缀无冲突。
4. **超时值**（npm 10m / node 链 15m / apt 10m）为工程判断（envcheck Windows 侧是 120s，WSL 内链路更长），真机慢网下可能需要调参。
5. `cliToolOrder`/`cliPackages` 与前端 `WSLCLISettings.vue` 的卡片渲染：GetStatus 多出 omp 行后前端应自动出卡（遍历 `status.tools`），但该文件本轮未读未改，前端适配归切片 3。

## 7. 回滚方式

全部改动位于 worktree 未提交状态：`git -C <worktree> checkout -- internal/wslsetup && git clean -fd internal/wslsetup agent-outputs/workflow/windows-wsl-unified/setup-implementation.md`（或 Leader 直接丢弃该 worktree 分支合并）。环境侧拷贝的 `frontend/dist`/`mobile/dist` 为 gitignore 产物，删除无影响。

## 8. 建议下一步（供 Leader 参考）

1. 派发切片 3（envcheck 成功路径注入 WSL 同步回调，复用 `SetWSLSearchToolsInstaller` 模式 + 本轮新增的 `UpdateTool`）。
2. P3 集成时补 Windows 真机用例：omp 首装、已装升级 `@latest`、断网失败、并发安装/升级同 distro 串行。
3. 前端 `WSLCLISettings.vue` 核对 omp 词条与"重新安装"按钮文案（现状后端短路 AlreadyOK、更新语义应指向 UpdateTool）。
