# P3-S0 实施报告：WSL 配置供给体系——安全接入与目标一致性

> 批次：S0（安全接入与目标一致性，P0 前置）· 状态：已实施并验证
> 权威规格：`agent-outputs/workflow/wsl-session-friction/p3-config-supply-design.md`（下称「设计」）
> 验证环境：Windows 宿主（go1.25.5 windows/amd64，经 WSL bash + go.exe）；CI 权威全量在 macOS（见 §5 环境差异说明）

## 1. 实现清单 vs 设计条款对照

| 设计条款 | 产物位置 | 状态 |
|---|---|---|
| §七 wslsupply 最小骨架（策略/清单/计划/状态/调度；不导入 App/Wails/插件 UI） | 新包 `internal/wslsupply/`：`types.go`（TargetRef/Snapshot/SupplyRequest/SupplyPlan/RunResult/TargetStatus）、`coordinator.go`（Inspect/Plan/Apply/RunUnits/MarkDirty/GetStatus/提交原语）、`state.go`（状态清单）、`a2.go`（A2Auth 适配器）、`projection.go`（TrackedProjection 接缝）、`probes.go`（平台探测注入面）、`runner.go`（Runner 接口）、`compensation.go`（条件撤销） | ✅ 仅 import launcher/platform/session 级依赖，无 App/Wails/UI |
| TargetRef（distro+uid/home+CLI+目标根+generation；默认 distro 无选择器；身份不可得按 unknown） | `wslsupply/types.go::TargetRef` + `coordinator.go::ResolveTarget/generationFor`；身份经 `platform.WSLTargetIdentity` 单次批量探测（id -u / $HOME / /etc/machine-id） | ✅ machine-id 缺失 → `GenerationUnknown`（测试 `TestResolveTargetGeneration`） |
| Inspect 批量只读探测→Snapshot（不写） | `coordinator.go::Inspect`：UNC os.Lstat 只读元数据；A2 凭据文件不计算 hash | ✅ `TestInspectReadonlySnapshot` |
| Plan 纯计算（不写盘不 spawn，计划不携带候选正文） | `coordinator.go::Plan`；A2 单元只给 `ActionSyncA2` 描述 | ✅ `TestPlanIsPureComputation`（探测计数器断言零增量） |
| Apply（复核目标与 hash→写入→验证→逐项结果） | `coordinator.go::Apply`：身份重探复核（`SameIdentity`），失配拒绝旧计划；投影单元 CAS 前置复核；逐项 UnitResult | ✅ `TestApplyRefusesStalePlanWhenTargetChanged` |
| MarkDirty（保存后标脏合并，不在配置锁下等 WSL） | `coordinator.go::MarkDirty` → `state.mutate`（只落 desired revision，不探测不调度）；App 挂接点：`syncProvidersToHarnesses`（锁外）+ PiLoginPreset 监听器 | ✅ `TestMarkDirtyMergesAndPersistsWithoutProbing` |
| GetStatus 脱敏（读缓存不触发；无缓存未检查；敏感 hash 不出 API） | `coordinator.go::GetStatus`；A2 单元登记永不写入 sourceHash/lastWrittenHash（全字段省略） | ✅ `TestGetStatusSanitizedAndTargetScoped` + 状态文件字节断言（`TestApplyA2DelegatesAndRecordsState`） |
| 状态清单 `~/.amagi-codebox/wsl-config-supply.json`（schemaVersion/generation/policy/sourceRevision/sourceHash/lastWrittenHash/transformVersion/冻结/lastSuccess；私有原子写） | `wslsupply/state.go`（schemaVersion=1，tmp 0600→rename→chmod 0600，`stateStore.mutate` 串行化） | ✅ 损坏回退未托管（`TestCorruptStateFileFallsBackToUnmanaged`） |
| S0 单元范围：只有 A2 凭据；TrackedProjection 只留接口与测试桩 | 生产注册仅 A2（DefaultA2Adapter→launcher.SyncWSLPiLoginAuth）；`ProjectionAdapter` 接口 + `RegisterProjection`（测试专用，无生产调用方） | ✅ |
| 提交原语：目标内私有 staging + 唯一临时名 + 权限先收紧再 rename + 最终校验；字节走 UNC 不进 wsl.exe 命令行 | `coordinator.go::submitFile`（os.CreateTemp 唯一名→0600→rename→distro 内 chmod 600 补偿失败即清理→读回 hash 校验） | ✅ `TestConcurrentApplyUniqueStagingNames` / `TestSubmitFileChmodFailureFailsClosed` |
| A2 桌面/远程统一执行边界（S0 核心） | 见 §3 | ✅ |
| 停用 ensurePiConfig 整树播种 | `internal/wslsetup/service_windows.go`：函数与调用点删除，注释记录原因与旧副本处置（未托管现状，不迁移不清除）；helper `wslPathFromWindowsHome`（common.go）随之删除 | ✅ `TestInstallToolPiDoesNotSeedAgentTree` / `TestInstallToolPiAlreadyOKIdempotent` |
| App 编排接线（桌面闸门经协调器；保存事件挂 MarkDirty 空实现位） | `app.go`：`WSLSupply wslsupply.Runner` 字段 + 构造器装配（`NewCoordinator(configDir, log, DefaultProbes())`）+ 执行器 deps 注入；`syncWSLPiLoginAuthAtLaunch` 改走 `runWSLLoginSupplyFn`（生产=协调器 RunUnits） | ✅ |
| remote/v1 BuildPlan 纯读；新增 effect 在 Apply 阶段 Inspect/Plan/Apply，顺序在 PTY 之前 | `launchplan`：`EffectWSLConfigSupply`（rank 8：配置变更之后、PTY 之前）+ `WSLConfigSupplySpec`（计划只带描述，Distro 留空 Apply 时与 PTY 构建同源解析）；`launch_executor.go::wslConfigSupplyEffect`；`internal/remote`：`LaunchWSLConfigSupply` gate 类别 + `launchEffectKindForSpec` 映射 | ✅ `TestPlanValidateWSLConfigSupplyEffect` / `TestPiLoginPlannerWSLGateLifted` |
| 载荷退役为 env 重锚片段（env -u 保留，cmp/cp 删除） | `pi_login_launch.go::piLoginWSLReAnchorSpec`（只剩 env -u 前缀+单引号 CLI+exec bash -li）；`piLoginWSLAuthSyncSnippet` 与 `launcher.WSLDrvFSMountPath` 删除（调用方全覆盖） | ✅ 见 §3 |
| A2 判定语义零变化 | 算法本体 `launcher.SyncWSLPiLoginAuth` 一字未动；适配器只做委托与结果分类；底层写入改唯一临时名（提交原语对齐，语义同构） | ✅ launcher 既有矩阵测试全绿 |
| Abort/补偿：普通文件 CAS 撤销；A2 持久凭据不回滚 | `coordinator.go::Compensate`（hash 仍匹配才恢复/删除，外部编辑保留）+ effect `compensate`（A2 无 Undo 句柄）+ `MarkCommitted` 清撤销材料 | ✅ `TestCompensateCAS` 三子测 / `TestA2NeverCompensates` |

**明确不做（S1+）**：provider/models.json 托管分发、视觉清单、OpenCode 扩展、Pi settings/amagi/MCP 导入、skills、插件重放、状态 UI、B5 distro 选择——均未触碰。GetStatus 只返回最小脱敏快照（未绑前端）。

## 2. 接口签名（新公开面）

```go
// internal/wslsupply
type CLI string                 // CLIPi（S0 唯一带单元 CLI）
type UnitID string              // UnitPiA2Auth = "pi/a2-auth"
type Policy string              // PolicyA2Auth / PolicyTrackedProjection
type TargetRef struct{ Distro string; CLI CLI; UID, Home, LinuxRoot, UNCRoot, Generation string }
func (t TargetRef) SameIdentity(other TargetRef) bool

type Coordinator struct{ /* configDir/log/probes/state/a2/projections */ }
func NewCoordinator(configDir string, log Logger, probes Probes) *Coordinator
func (c *Coordinator) SetA2Adapter(a A2Adapter)                                   // 测试接缝
func (c *Coordinator) RegisterProjection(a ProjectionAdapter)                      // S0 测试桩；S1 真实分发
func (c *Coordinator) ResolveTarget(ctx, distro string, cli CLI) (TargetRef, error)
func (c *Coordinator) Inspect(ctx, target TargetRef) (*Snapshot, error)            // 只读
func (c *Coordinator) Plan(req SupplyRequest, snap *Snapshot) (*SupplyPlan, error) // 纯计算
func (c *Coordinator) Apply(ctx, plan *SupplyPlan) (RunResult, error)              // 复核+写入+逐项结果
func (c *Coordinator) RunUnits(ctx, distro, cli, reason, units) (RunResult, error) // Inspect+Plan+Apply 单入口
func (c *Coordinator) MarkDirty(source string, revision uint64) error              // 只落 revision
func (c *Coordinator) GetStatus(distro string, cli CLI) TargetStatus               // 脱敏读缓存
func (c *Coordinator) Compensate(ctx, handles ...*CompensationHandle) []CompensationOutcome // CAS 条件撤销

type Runner interface { RunUnits(...); MarkDirty(...); GetStatus(...); Compensate(...) } // App/executor 消费面
type A2Adapter interface { Files() []string; Sync(distro string) (launcher.SyncWSLPiLoginAuthOutcome, error) }
type ProjectionAdapter interface { Unit() UnitID; Files() []string; Plan(...) (PlannedUnit, error); Candidate(ctx, target) ([]byte, string, error) }

// internal/launchplan
type EffectKind // + EffectWSLConfigSupply（rank 8，PTY 之前）
type WSLConfigSupplySpec struct{ Distro, CLI, Reason string; Units []string } // Distro 空=Apply 时解析

// internal/platform
func WSLTargetIdentity(distro string) WSLUserIdentity // 一次 wsl.exe 批量取 uid/$HOME/machine-id
```

删除的公开符号（仓库内调用方全部同步）：`launcher.WSLDrvFSMountPath`（旧载荷专用）、`wslsetup.wslPathFromWindowsHome`（ensurePiConfig 专用，包内）、`piLoginWSLAuthSyncSnippet`/`syncWSLPiLoginAuthFn`（包内，退役）。

## 3. A2 统一执行边界与旧载荷退役证据

**退役前差异（设计 §一/§九点名）**：remote/v1 重启链路的凭据同步是 bash 载荷片段（`piLoginWSLAuthSyncSnippet`：`cmp -s`/`cp`/`stat -c %Y`），无原子写、无 fail-closed 分类、复制失败仅 echo 后继续；桌面走 Go 通道（`launcher.SyncWSLPiLoginAuth`：UNC 原子写+chmod 补偿+fail-closed 分类）。

**退役后**：
1. 载荷退役——`pi_login_launch.go::piLoginWSLReAnchorSpec` 不再内嵌同步片段，StartupCommand 只剩 `env -u <凭据键> -u PI_CODING_AGENT_DIR '<cli>' '<args>'; exec bash -li`。回归钉：`TestPiLoginPlannerWSLGateLifted` 断言载荷不含 `cmp -s`/`cp --`/`stat -c %Y`/`chmod 600`/`auth.json`/`mkdir -p`。函数 `piLoginWSLAuthSyncSnippet` 与 `launcher.WSLDrvFSMountPath` 物理删除（`git diff` 可证：调用方只剩测试，测试同步改写）。
2. remote/restart 链路改走 effect——`buildPiLoginPlan` 对 WSL spec 产出 `EffectWSLConfigSupply{CLI:"pi", Units:["pi/a2-auth"]}`（Distro 留空）；`launch_executor.go::wslConfigSupplyEffect.Apply` → 协调器 `RunUnits`（Inspect→Plan→Apply），顺序在 PTY 之前（rank 8 < 16，`Plan.Validate` 强制）；gate 检查点类别 `LaunchWSLConfigSupply`（`launchEffectKindForSpec`）。旧载荷调用方（桌面 `LaunchPiSession` 闸门 + remote/restart 计划 + `internal/pty` 载荷消费 + restart 测试）全覆盖切换，无残留路径。
3. 桌面闸门——`syncWSLPiLoginAuthAtLaunch` 经 `runWSLLoginSupplyFn`（生产=协调器）消费同一 verdict 映射 `verdictForWSLLoginSupply`（桌面日志文案/中文错误逐字保留；remote effect 用同一函数 → 两链路结果一致由 `TestWSLLoginSupplyFailureMatrixBothLinks` 钉住）。
4. Abort/补偿——A2 凭据刷新是持久认证状态：`UnitResult.Undo` 恒 nil（`TestA2NeverCompensates`），effect Abort 只对投影写入走 `Compensate`（hash 仍匹配才恢复；外部编辑保留）。
5. A2 算法零变化——`launcher.SyncWSLPiLoginAuth` 判定/分类/错误语义未改；唯一实现级变化：`writeWSLAuthAtomic`/`writeWindowsAuthAtomic` 的 staging 从固定名 `.amagi-tmp` 改为 `os.CreateTemp` 唯一名（设计 §七「唯一临时名」提交原语；tmp+rename+chmod 补偿+双失败 fail-closed 序列不变，`wslAuthCreateTempFn` 接缝注入替代旧固定名碰撞注入法）。

## 4. 独立验收与故障矩阵输出（验收 1–8）

| # | 验收 | 证据 | 结果 |
|---|---|---|---|
| 1 | 首装不带入 sessions/auth/plugin 实体 | `internal/wslsetup`：`TestInstallToolPiDoesNotSeedAgentTree`（脚本级捕获断言无 `cp -r`/`.pi/agent` 触碰；Success+版本语义保留；日志无 `[pi-config]`） | ✅ PASS |
| 2 | AlreadyOK 不阻止检查（重复 InstallTool 幂等） | `TestInstallToolPiAlreadyOKIdempotent`（二次 AlreadyOK 成功，两次均零播种脚本） | ✅ PASS |
| 3 | A2 故障矩阵，桌面与 remote 同一组用例一致 | `TestWSLLoginSupplyFailureMatrixBothLinks`（9 用例 ×2 链路，真协调器+A2 适配器接缝注入；逐用例断言两链路 block/放行 与中文文案一致）+ 算法级矩阵（launcher 既有 `TestSyncWSLPiLoginAuth*`：Fresh/Copied/DistroNewer/TieKept/探测失败零写入/源缺失不复活/写失败分类/双失败拒绝） | ✅ PASS（矩阵明细：两侧相同→放行；宿主新→放行；distro 新→放行；2s 容差并列取 distro→放行；探测失败→两侧零写入 WARN 降级；退出登录→fail-closed「Pi 登录已失效」；写失败→HasCopy 降级/NoAuth 拒绝同旧分类；DistroHasCopy→WARN 降级；chmod+清理双失败→fail-closed 拒绝） |
| 4 | BuildPlan 零副作用 | `TestPlanIsPureComputation`（Plan 期间探测计数零增量、无候选/hash）+ `TestPiLoginPlannerWSLGateLifted`（供给 spec Distro 为空 → 计划期不解析 distro 不 spawn） | ✅ PASS |
| 5 | 并发不撞临时名 | `TestConcurrentApplyUniqueStagingNames`（8 并发 RunUnits 各自文件全部成功、无 `.amagi-supply-*` 残留）+ `TestWriteWSLAuthAtomicUniqueStagingNames`（launcher 层串行化写入序列同样无残留） | ✅ PASS |
| 6 | remote effect 权限/门控/补偿 | 门控：effect Apply 消费统一 verdict（矩阵用例覆盖 fail-closed/WARN）；Prepare 无协调器即失败（`buildWSLConfigSupply` deps 校验）；补偿：`TestCompensateCAS`（恢复前像/外部编辑保留/删除新增）+ A2 不回滚（`TestA2NeverCompensates`） | ✅ PASS（Prepare-fail 门控由 builder 防御校验钉住，未单列测试） |
| 7 | build/vet/test/gofmt | `go build ./...` ✅；`go vet ./...` ✅（改动包+全仓）；`go test`（wslsupply/wslsetup/launchplan/platform-WSL 域/launcher-WSL 域/根包 PiLogin+供给域/remote 重启与启动域）全绿；gofmt：全部改动文件 CR 剥离后 diff=0；注释字节复核：diff 中「「」」引号原样保留、零弯引号引入（curly-count=0） | ✅ PASS |
| 8 | 既有 restart/remote 测试零回归 | 旧载荷断言测试（`TestPiLoginPlannerWSLGateLifted` 载荷段/`TestPiLoginWSLPayloadSkipsAgentDirPin`/`TestPiLoginDesktopWSLGateSync`）全部改写为新契约；remote 包 restart/adapter/gate/fence 域 `-run 'TestM004Restart|TestAuthorityRestart|TestLaunch|TestM2A|TestComposite|TestControlGate|...'` 全绿；`TestBuildPlanFiveCLICanonicalStructure` 等五 CLI 结构测试不受影响 | ✅ PASS |

**真实入口冒烟（只读、零凭据接触）**：`AMAGI_WSL_E2E=1 go test ./internal/wslsupply/ -run TestManualRealWSLSupplyTarget` 实测 PASS——真机 Ubuntu：`uid=1000 home=/home/maorun linuxRoot=/home/maorun/.pi/agent uncRoot=\\wsl.localhost\Ubuntu\...`，generation 由真实 machine-id 派生；Inspect 探测真实 auth.json（exists=true size=2741，凭据 hash 未计算）；状态清单记录 checked/generation。写入路径的 A2 真机 e2e（`TestManualRealWSLPiLoginSync`，双侧快照恢复）未在本轮执行——算法未变且其前置批次已实证；设计 §八真机验证规约要求凭据 e2e 须双侧保护+授权，本任务书未授权，列为待 Leader 决定项。

## 5. 验证记录与环境差异说明

- 执行命令（cwd=仓库根，WSL bash 调 Windows go.exe，GOCACHE/GOPATH 经 WSLENV 转发）：`go build ./...`、`go vet ./...`、`go test <改动包> -count=1`、gofmt 往返比对、`git diff` 注释字节复核——全部实际运行，输出见上文。
- 本机（Windows）全量 `go test .`/`./internal/remote`/`./internal/launcher` 存在与本改动无关的环境性失败（POSIX 权限位断言 0666≠0600、日志句柄锁 TempDir 清理、缺 Unix `sleep`、`SyncConfigDirectory` Access Denied、fnm 符号链接需特权——后两类与 P2T2 报告记载的本机失败一致；CI 在 macOS 跑全量为权威）。针对本改动的全部定向测试集（含上述包的 WSL/供给/计划域）均实际运行且全绿；`launch_executor.go` diff 纯增量（0 删除行）。
- 前端/mobile 未触碰（npm test 由 harness 结算）。

## 6. 设计未尽细节的微观决策记录

1. **effect 的 distro 解析时机**：规格只说「顺序在 PTY 之前」。取 Distro 留空、Apply 时 `platform.DefaultWSLDistro`（与 pty.buildWSLCommandLine 同源）——保证供给目标与实际 PTY 目标一致，且 BuildPlan 零 spawn。
2. **gate 检查点类别**：`internal/remote` 未在写面清单内，但 remote 链路切换必需最小触达——新增枚举值 `LaunchWSLConfigSupply`（uint8 纯增量，无 switch/String 消费方）+ `launchEffectKindForSpec` 一行映射；未复用 `LaunchConfigMutation` 以免收据类别混淆。
3. **MarkDirty 的 revision 来源**：无现成配置版本计数器，取 `time.Now().UnixNano()`（单调、可合并、不回退）；S1 接真实分发时可换业务 revision。
4. **Apply 目标复核成本**：RunUnits 全链 = 2 次身份 wsl.exe（ResolveTarget+Apply 复核）+ A2 适配器自带探测（设计 §三明确不计入预算）；设计「合并调用须先证明语义不变」，故不与 A2 内部探测合并。
5. **GetStatus 的待同步来源分面**：`PendingSources/DesiredRevision` 是宿主侧状态，目标不匹配时也返回（标脏早于首次检查是常态）；`Checked/身份/单元登记` 仅清单属于该目标时返回。
6. **A2 的 Inspect hash**：凭据文件在快照中恒不计算内容 hash（比设计字面更严：不进内存快照）；归属判定不依赖它，A2 判定全部在适配器内。
7. **投影视件 hash 的内存约束**：`FileMeta.Hash` 仅对 TrackedProjection 单元文件计算（S1 分发消费）；A2 文件排除。
8. **wslsetup 旧副本**：不迁移不清除（设计 §十）；测试断言新装流程零触碰，存量副本按未托管现状由 S1+ 归属登记处理。

## 7. 未覆盖项 / 风险 / 回滚

- **未覆盖**：① A2 真机写入 e2e 未跑（需授权，见 §4）；② `docs/developer/pi-login-providers.md` §「payload 内嵌同步片段」段已过时（docs 不在本批写面，建议 S1 批次随状态 UI 一并更新）；③ `internal/pty/service.go:949` 注释仍描述「载荷内嵌惰性同步片段」（注释级陈旧，代码不受影响）；④ GetStatus 未暴露到任何绑定/API（任务书限定仅供测试与后续 UI）。
- **风险**：Windows 同目标并发 rename 会 Access Denied——生产由协调器 applyMu 串行化（桌面+remote 共用 App 实例）；跨进程（两个 CodeBox）竞态沿用单实例假设。`~/.amagi-codebox/wsl-config-supply.json` 首次生成无害（schemaVersion=1，损坏自动回退未托管）。
- **回滚**：还原本批 19 个文件改动即可（`git checkout` 定向还原；无数据迁移、无 CLI 行为变化）。回滚后旧 shell 载荷路径随 `pi_login_launch.go` 一并恢复；A2 已同步过的凭据字节无需处理（算法两侧收敛语义不变）。**不得以重启用 ensurePiConfig 整树播种作为回滚替代**（设计 §八）。

## 8. 改动文件清单（19 + 报告）

新增：`internal/wslsupply/{types,coordinator,state,a2,projection,probes,runner,compensation}.go` + `coordinator_test.go`/`projection_test.go`/`wsl_supply_e2e_windows_test.go`、`internal/platform/wsl_target.go`/`wsl_target_test.go`
修改：`internal/launchplan/types.go`/`types_test.go`、`internal/launcher/wsl_pi_login_sync.go`/`wsl_pi_login_sync_test.go`、`internal/remote/control_types.go`/`remote_session_adapter.go`、`internal/wslsetup/service_windows.go`/`common.go`/`service_test.go`/`service_windows_test.go`、`app.go`、`launch_executor.go`、`pi_login_launch.go`、`pi_login_wsl_test.go`、`provider_harness_sync.go`
