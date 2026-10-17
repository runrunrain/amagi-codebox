# C2 供给内核安全/身份/状态修复 — 实现报告

> **后续审查覆盖本报告的完成声明**：`phase-review.md` 判定FIX，C2安全/状态若干子路径尚未闭合，以该审核及后续修复记录为准。下文所列 `verify-staging-window.ps1` 已因PR-10撤回，原文仅存 `verify-staging-window.UNSAFE-RETIRED.txt` 审计证据，禁止执行。本轮从未运行该脚本，其描述不构成真机验证承诺。

- Worktree：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/wsl-goal-core`（分支 `amagi/wsl-goal-core`，基线 `58e7577`，未提交——按任务书留工作区由 Leader 汇总）
- 范围：SA-01/02/06/07/08/09/15/16（协调器/文件提交侧）+ steer#3 必需模型供给分类 + steer#7 闸门收紧
- 上游输入：`supply-audit.md`、`supply-audit-repro.go`、`direction.md`、`workflow.md`（全部完整读取）
- 方法：先写正式回归（`core_security_regression_test.go` 等 12+ 用例对当前代码全部 RED，复现审计反例），再修复至 GREEN；fixture 全程合成字节，不触真实 WSL/凭据

## 一、逐 SA 销项（验收 → 证据 → 状态）

### SA-01 身份 generation 比较 — ✅ 已闭合
- 修复：`types.go` `SameIdentity` 增加 Generation 比较；`coordinator.go` `generationFor` 改为 `sha256(distro|machine-id|uid|home)`（distro 入指纹，同时服务 SA-02 克隆防护）；`applyProjection` 提交前重验同意/停跟随（计划→提交窗口内撤销不得写入）。
- 回归：`TestApplyRejectsPlanAfterGenerationSwap`（修复前 RED：`applied/written`；修复后 blocked `target-changed-since-planning` 且目标零写入）。授权后换代由既有 F3 测试（`TestGrantUnitConsentCrossGeneration`，已更新断言至多分区语义）覆盖。
- 边界：无。

### SA-02 清单按身份分区 / unknown 不继承 / 保守旧记录兼容 — ✅ 已闭合（协调器侧）
- 修复：`state.go` schema v1→v2——`Units` 允许同 unit 多身份分区共存（`upsertEntry` 同代原位替换、异代追加，不再互相覆盖）；`entryFor` 匹配规则：请求代为空/`unknown`/`legacy:` 前缀一律不命中（unknown 永不复用持久授权；v1 记录隔离保留在盘供诊断、不删除）；`entry` 只匹配「当前代」分区；`GrantUnitConsent` 对 unknown 代直接拒绝（错误给出修复方向）；`Inspect`/`GrantUnitConsent` 写入 `Targets` 身份索引（generation → distro/uid/home/最近接触/逐 CLI 检查时间）；`GetStatus` 按请求 distro 的最近一代分区呈现（其它 distro 的分区、legacy 区、unknown 槽不串入）。
- 回归：`TestConsentRefusedForUnknownGeneration`（原 UNKNOWN_CROSS_DISTRO 反例）、`TestMultiTargetEntriesNotOverwritten`（A→B→A，原 RETURN_TO_A 反例）、`TestClonedDistroDoesNotInheritConsent`（同 machine-id 克隆）、`TestLegacyEntriesQuarantined`（v1 空 generation 授权不复用）——全部先 RED 后 GREEN。
- 迁移语义：v1 文件加载时 `migrateLegacyState` 在内存隔离（下次落盘写 v2）；因 v1 指纹不含 distro、无法归属，旧授权/基准全部退为未确认，重新同意后继续——符合 direction.md「不能自动扩大旧授权；保留旧数据用于诊断」。
- 交接 C4（steer#1）：多目标插件登记可复用 `StateFile.Targets map[string]TargetRecord`（键=generation 指纹）与 `latestTargetRecord(st, distro)` 辅助；`PluginStateEntry` 结构与 plugins.go 读取方法完全未动（v1 插件代同样被隔离为 `legacy:` 前缀，永与新代误配）。

### SA-06 目标侧私有 staging 建立验证 — ✅ 已闭合（fixture 级；真机脚本交付）
- 修复：新增窄接口 `Probes.StatMode`（生产 `platform.WSLFileMode`，wsl.exe 内 `stat -c %a`，纯解析面 `parseWSLStatModeOutput` 有单测）；`coordinator.go` `submitSensitive`：`.amagi-staging` 建于目标根 → distro 内 chmod 700 → **StatMode 验证 700**（失败=未验证=未隔离，在任何秘密字节落盘前 fail closed）→ 秘密字节只写 staging 内唯一临时名 → distro 内 chmod 600 并验证 → rename；launcher `writeWSLAgentFileStaged` 同一原语用于 A2（`writeWSLAuthAtomic`）/ pi / omp / opencode 四个敏感 writer（含 agent 目录前置 700 紧固）。
- 零写入路径（steer#4）：`wslRetighten600`——字节一致但权限被改宽（探测到确切非 600）时安全修复；无法重收紧 → 报错阻断（A2 映射 `errWSLAuthLooseLeftover` → NoAuth fail-closed）。探测不可用（ok=false）不视为不安全（无新字节）。
- 回归：`TestSensitiveSubmitUsesVerifiedPrivateStaging`（chmod 700→600 序列与顺序证明、staging 空、最终 0600）、`TestSensitiveSubmitFailsWhenStagingUnverifiable`（零字节落盘）；launcher 侧 `TestWriteWSLAgentFileStagedTightensBeforeRename`、`TestWriteWSLPiAgentConfigFailsWhenStagingUnverifiable`、`TestWriteWSLPiAgentConfigZeroWriteRetightensLooseMode`；既有 chmod 序列断言更新为 `assertStagedChmodCalls`。
- 真机验证（未运行，明确边界）：`internal/wslsupply/core_staging_e2e_windows_test.go`（`//go:build windows` + `AMAGI_WSL_E2E=1`，真实 wsl.exe stat 实证 staging=700/文件=600 + chmod 注入下混合配置保留）；第二 Linux 用户观测整个写入窗口的取证脚本 `agent-outputs/workflow/wsl-goal-closure/verify-staging-window.ps1`（合成秘密、隔离 /tmp 根、观测器循环 cat 探测）。**本机 macOS，两者均未执行**——不宣称 Windows 通过。

### SA-07 敏感 chmod 失败不删混合配置 — ✅ 已闭合
- 修复：敏感路径全部失败点前移到 rename 之前（见 SA-06），既有混合配置（用户自有 provider/字段）原样保留；rename 后唯一故障（读回校验失配）先经同一敏感原语恢复前像，恢复也失败才移除并在错误里双报告（清理/恢复/CAS 可观测）。launcher 侧同步移除「chmod 失败删整文件」分支。
- 回归：`TestSensitiveChmodFailurePreservesMixedConfig`（原 CHMOD_FAILURE 反例：修复前文件被删；修复后混合内容逐字保留）；launcher `TestWriteWSLPiAgentConfigChmodFailurePreservesMixedConfig`、`TestSyncAuthChmodFailureKeepsOldDistroCopy`；`submit_primitive_test.go` 的旧「fail-closed 删除」子测试按新契约改写（失败发生在 rename 前、前像完好——非掩盖，是审计规定的目标行为）。

### SA-08 single-flight 补推死锁 — ✅ 已闭合
- 修复：`pushWSLSupplyOnce` 改为单 worker 循环消费 rerun；工作体抽到 `doPushWSLSupply`；defer 统一释放 running，所有退出路径可达。
- 回归：`TestPushWSLSupplyOnceRerunLoopRecovers`（执行中二次保存 → 补推第二轮真实执行 `calls≥2`；退出后 running 释放，后续推送继续推进）。

### SA-09 共享小文件启动补漏/撤回 — ✅ 已闭合（闸门侧；C1 接线待集成）
- 修复：`shared_files.go` 视觉导出撤回判定——宿主导出文件删除（`os.ErrNotExist`）且本目标曾发布：目标仍等于上次写入 → `ActionRemoveProjection` 删除副本（登记 `withdrawn`，保留同意/选择集）；被手改 → `withdrawal-unconfirmed-frozen`；瞬时读错误 → 维持 `source-unavailable` 不动副本；从未发布 → ActionNone。`coordinator.go` `applyWithdrawal` 执行 CAS 删除（remove 失败/删除后仍存在 → `withdrawal-remove-failed` UnitFailed，不产生 Undo）。状态面 `wslVisionFace` 撤回终态诚实显示（不虚报已同步）。
- 启动闸门（冻结契约，steer#2/#7 收紧版）：

```go
// app_wsl_supply.go（内部 Go API，不进 Wails 绑定）
func runWSLSharedFilesAtLaunch(ctx context.Context, runner wslsupply.Runner, distro string) error
```
  - 语义：只运行 `shared/vision-export` + `shared/modality-kb`（`CLICommon`，`ReasonLaunch`），不扫 skills、不联网、不装插件；runner 为 nil/distro 为空/无 shared 适配器 → nil；
  - 返回 error（阻断会话消费旧副本）的条件：① ctx 已取消（返回 ctx.Err()）；② vision `withdrawal-unconfirmed-*`；③ vision `UnitFailed`（含 withdrawal-remove-failed、staging unverified、写失败）且状态面显示本目标可能仍持有受管敏感副本（`visionManagedCopyPossible`：登记 Consented 且非 withdrawn*）；④ `RunUnits` error 且受管副本可能存在；
  - 返回 nil（不阻断普通启动）：awaiting-consent / unmanaged-existing / 非撤回类 frozen（从未授权或副本不归本目标受管）；modality-kb（非敏感）任何失败；vision Applied/Fresh/WarnDegraded；已证明无受管副本时的一切失败；
  - 调用时机：**已解析 distro、PTY 启动之前**；建议在其它 supply 效果之后（本闸门幂等，只处理两个小文件单元）。无事务/撤回安全方面的反对——撤回删除本身是 CAS 保护的单文件操作，失败即返回 error 交由闸门阻断。
- 回归：`TestRunWSLSharedFilesAtLaunchGate` 全组（只跑 shared 单元/不扫 skills、unconfirmed 阻断、无授权不阻断、撤回成功放行、run error + managed copy 阻断、withdrawal-remove-failed + managed copy 阻断、unverified + managed copy 阻断、无副本失败放行、ctx 取消传播）；撤回面 `TestVisionExportWithdrawalRemovesCopy` / `TestVisionExportWithdrawalUnconfirmedFrozen` / `TestVisionExportTransientSourceErrorKeepsCopy`（先 RED 后 GREEN）。
- 剩余：C1 接线五 CLI desktop（launchEmbeddedPTYWithAdmission）与 remote（ptyStartEffect.Apply）——本 worktree 未改根 launch 文件。

### SA-15 失败登记 — ✅ 已闭合
- 修复：`recordProjectionFailure`（candidate-unavailable/target-corrupt/CAS 失配/target-unreadable/write-failed 全部逐单元落 LastOutcome=failed + LastErrorCode，LastSuccess 保留为上次成功时间，同代基准字段保留）；`recordProjectionBlocked`（source-unavailable 等非冻结阻断也登记）；A2 失败本就登记（`recordA2State`）。
- 回归：`TestFailureRecordedNotStaleSynced`（原 FAILURE_STATUS 反例：修复前 lastOutcome=written/err 空；修复后 failed + errCode 非空 + LastSuccess 保留）。
- 决策说明：partial 保持 `UnitApplied + Detail="partial;…"`（LastOutcome=partial 已结构化）——新增独立 Outcome 枚举会波及 remote/executor 消费面（非本节点文件），状态页已可区分；列为已知设计取舍。

### SA-16 已取消 ctx 不得继续写 — ✅ 已闭合（协调器/提交侧）
- 修复：`RunUnits` 入口、`Apply` 等锁前/取得锁后/逐单元前、`submitFileCtx` 写前/rename 前共 6 处 ctx 检查；取消结果逐单元 `context-canceled`；`submitFile`（无 ctx 旧签名）保留为兼容入口（resources.go 等 C3 文件调用面不变，其取消保护由 Apply 循环逐单元检查承担）。
- 回归：`TestCanceledContextNeverWrites`（修复前 `applied/written` 且文件落盘；修复后 blocked + 零写入）。
- 剩余（明确边界）：`Probes.Identity/Chmod/StatMode` 为同步窄接口、不可取消（探测中的 wsl.exe 无法中止）——写安全契约已闭合，探测取消属延迟问题；插件超时归类/子进程清理在 plugins.go（C4 范围），本节点未动。

### steer#3 必需模型供给严谨分类 — ✅ 已闭合
- `verdictForWSLPiConfigSupply`：`pi/managed-models` UnitFailed/UnitBlocked → 阻断（target-corrupt 保留专门中文错误；其余统一「未落地…本次启动未执行」）；Fresh（确认内容一致的安全无动作）放行；键级可选单元失败仍告警放行；预期态静默。桌面与 remote（`launch_executor.go` 消费）同判定。
- 回归：`TestVerdictForWSLPiConfigSupplyRequiredUnits` + 既有 `TestRunWSLManagedModelsAtLaunchVerdicts` 子测试改写（managed-models 失败 → 阻断；键级失败 → 放行）。`TestWSLPiConfigSupplyDualLinkDesktopVsRemote` 双链路一致（fixture 补 StatMode 后通过）。

## 二、接口变更（全部向后兼容，无破坏性签名变化）

| 变更 | 类型 | 兼容性 |
|---|---|---|
| `wslsupply.Probes` 新增 `StatMode StatModeProbe` 字段 | 新增 | `NewCoordinator` 对 nil 单独补齐 DefaultProbes；未注入的生产装配自动获得 |
| `wslsupply.ActionRemoveProjection` 枚举 | 追加（置于 ActionBlocked 之后，不改既有值） | 现有 switch 走 default 分支不受影响；`piPreviewActionName` 已补 "remove" |
| `Coordinator.submitFile`（旧签名）保留 + 新增 `submitFileCtx(ctx,…)` | 内部 | resources.go/skills 调用面零改动 |
| `StateFile` 新增 `Targets map[string]TargetRecord`；schema v2 | 持久化 | v1 文件加载即隔离迁移；Plugins 数组与 plugins.go 读取方法零改动 |
| `platform.WSLFileMode(distro, linuxPath) (string, bool)` | 新增 | 纯新增（`stat -c %a`，与 WSLFileMTime 同族） |
| `launcher.wslStatModeFn` 注入接缝 | 新增 | 测试接缝 |
| 退役：`launcher.wslAuthRemoveFn`/`wslAuthCreateTempFn`（A2 staging 重构后无调用点） | 移除 | 仅测试曾引用，已同步更新 |
| `runWSLSharedFilesAtLaunch(ctx, runner, distro) error`（app 包内部） | 新增 | 冻结契约签名（steer#2），待 C1 接线 |

## 三、changed files

生产（10）：
- `internal/wslsupply/types.go`（SameIdentity+Generation；ActionRemoveProjection）
- `internal/wslsupply/state.go`（schema v2 多分区 + Targets 索引 + legacy 隔离迁移 + entry/entryFor/upsertEntry 重定义）
- `internal/wslsupply/coordinator.go`（generationFor 入 distro；Inspect/GetStatus 分区化；Apply ctx×3 + 撤回分发 + 同意/停跟随重验；applyProjection 失败登记；applyWithdrawal/recordProjectionWithdrawn/Blocked/Failure；submitFileCtx/submitSensitive/ensurePrivateStaging；GrantUnitConsent unknown 拒绝；StatMode 补齐）
- `internal/wslsupply/probes.go`（StatModeProbe 接缝）
- `internal/wslsupply/shared_files.go`（视觉导出撤回判定）
- `internal/launcher/wsl_agent_config.go`（wslStatModeFn、wslEnsurePrivateStaging、writeWSLAgentFileStaged、wslRetighten600；pi/omp/opencode 三 writer 重构；移除 writeOpenCodeConfigFile）
- `internal/launcher/wsl_pi_login_sync.go`（A2 走 staging 原语；Fresh 零写入重收紧；接缝退役）
- `internal/platform/wsl.go`（WSLFileMode + parseWSLStatModeOutput——必要生产探测的窄接口实现）
- `app_wsl_supply.go`（SA-08 循环；runWSLSharedFilesAtLaunch + visionManagedCopyPossible；verdictForWSLPiConfigSupply 收紧；wslVisionFace 撤回态；piPreviewActionName remove）
- `internal/wslsupply/managed_models.go` / `a2.go` / `runner.go` / `compensation.go` / `projection.go`：无改动需要（提交原语在 coordinator/launcher 层统一覆盖）

测试（9 修改 + 4 新增）：
- 新增：`internal/wslsupply/core_security_regression_test.go`（12 反例/回归）、`internal/wslsupply/core_staging_e2e_windows_test.go`（真机 gated）、`internal/launcher/wsl_supply_core_regression_test.go`、`app_wsl_supply_core_test.go`
- 更新 fixture/断言（随受测契约同步，非掩盖）：`coordinator_test.go`（fakeProbes +StatMode）、`submit_primitive_test.go`（敏感 chmod 失败=前像保留）、`s1_fix_round_test.go`（F3 多分区断言）、`s1_e2e_windows_test.go`（StatMode）、`internal/launcher/wsl_agent_config_test.go`、`wsl_pi_login_sync_test.go`、`app_wsl_supply_test.go`、`internal/platform/wsl_test.go`（+parse 单测）

产物（agent-outputs/workflow/wsl-goal-closure/）：`core-implementation.md`（本报告）、`verify-staging-window.ps1`（真机取证脚本）

## 四、验证账（真实命令与结果）

- `go test ./internal/wslsupply ./internal/launcher -count=1`：launcher ok；wslsupply 仅剩 3 条**基线失败**（`TestPluginDefaultRunnerHonorsCanceledContext` / `TestScanSkillCompatChecks` / `TestScanSkillLinkNameExcluded`——C3/C4 负责，本节点零改动、断言未动）
- `go test . -count=1`（root/App 绑定面）：ok（6.0s）
- `go test ./internal/remote/... ./internal/launchplan -count=1`：ok（闸门收紧的下游消费面回归）
- `go vet ./internal/wslsupply ./internal/launcher ./internal/platform .`：干净
- `GOOS=windows GOARCH=amd64 go build ./...` + `go vet`（含 windows-gated 测试编译）：exit 0
- `gofmt -l`（仅本批变更文件）：干净；未触碰仓内其它既有未格式化文件
- 反例证明：12 个新回归用例在修复前运行全部 FAIL（输出与审计 repro 一致：`applied/written`、`unmanaged-existing`、`fresh/no-change`、克隆/unknown/legacy 继承、混合配置被删、`outcome="written" err=""`、`source-unavailable` 等），修复后全部 PASS
- 未运行：全仓并发 suite（按任务书）；真机 Windows/WSL（本机 macOS）

## 五、剩余 / 风险 / 建议

1. **C1 接线**（SA-09）：`runWSLSharedFilesAtLaunch(ctx, a.WSLSupply, distro)` 于五 CLI desktop/remote/restart 的「已解析 distro、PTY 前」调用（建议置于其它 supply 效果后）；阻断语义见 §一 SA-09——普通启动不受影响，敏感副本状态未确认才 error。
2. **C4**（SA-02 插件侧）：多目标插件登记槽仍单 identity（plugins.go 未动，legacy 代已隔离不误配）；可复用 `Targets`/`latestTargetRecord`。
3. **真机取证**：`core_staging_e2e_windows_test.go` + `verify-staging-window.ps1` 需 Windows 机器执行后才能关闭 SA-06 真机部分；第二用户窗口观测结论按脚本声明记「采样观测」。
4. **升级影响**：v1→v2 迁移使既有授权/基准退为未确认（重新同意/首次发布即重建）——方向报告裁定的保守代价；清单 Units 随身份代数增长（无淘汰），当前量级可忽略。
5. **partial 未升级为独立 Outcome**（§一 SA-15 决策说明）；`Probes` 不可取消（§一 SA-16 边界）。
6. 回滚：全部改动在工作区未提交，`git checkout -- <files>` + 删除 4 个新增测试文件即回基线；无配置/凭据/全局设置变更。
