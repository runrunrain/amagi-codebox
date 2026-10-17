# G1 按攻坚契约统一供给提交、恢复、风险与取消 — 实施报告

- 任务：`attack-core-assignment.md`（worktree `wsl-goal-transaction`，初始 HEAD `58e7577`）。
- 产物：`attack-core.delta.patch`（37 文件，+4748/−818；仅相对「resumed-input + G0 delta + G0 correction」的净增量，含新文件；与 G0 写面 internal/platform、internal/launcher 零重叠，已在模拟基座上三段连序 `git apply` 全通过）。
- 必读输入完整读取：attack-contract.md、attack-platform-assignment.md、attack-core-assignment.md、resumed-input.patch（机械 check/apply 后保存导入态 tree `9273b7b2`）、phase-rereview.md、phase-rereview-repro.go/.log、review_fixes_test.go。
- 依赖：G0 `attack-platform.delta.patch` + `attack-platform-correction.delta.patch` 已在本树 check/apply（baseline tree `8625ace3` 含二者；本 delta 的基线按「resumed-input+G0+correction」独立重建校验）。
- 测试日志：本树 `.amagi/attack-core/`。主树仅写本报告与 delta 两个产物。

## 一、逐契约条目 met / partial / missed

### 1. 持久事实与状态接口 — **met**（一处机制等价偏离，见注）

| 契约项 | 落点 | 状态 |
|---|---|---|
| state v3（兼容读 v1/v2、迁移幂等、禁止降级读取更高 schema） | `state.go` `stateSchemaVersion=3`；loadLocked 显式分类 fresh/ok/unreadable/corrupt/unsupported | met |
| OperationIntent 不可变磁盘事实（私有 0600、唯一 tmp→Sync→Close→权限检查→读回→rename 唯一名→最终读回；不含正文；路径仅内部生成） | `operation.go` + `intent_store.go` `saveIntent` | met |
| SettledOperations（与 StateEntry 同次主清单保存；RelatedOpIDs；多次操作不互相丢） | `state.go` + `commit.go` `finalizeCommit`/`finishCompensation` | met |
| StateEntry.LastOperationID | `state.go`（仅状态关联，非终结凭据） | met |
| OperationObservation（内存、锁内深复制、重启由 intent+目标重建） | `intent_store.go` `observationRegistry` + Coordinator.observations | met |
| LegacyEvidence（v1 原 entry+顶层线索保真；v2 派生 map 转低可信证据保留） | `state.go` `LegacyEvidenceEntry` + `migrateLegacyState` | met |
| StoreHealth（load 显式返回；读错不得空值覆盖原文件；mutate 拒绝 unhealthy 覆盖） | `state.go` `StoreHealth`/`Health()`/`mutate` 门；RunUnits 入口 `store-unhealthy` 阻断 | met |
| pending 有界扫描（≤512、损坏不自动删、不扫工程根） | `intent_store.go` `loadIntents` | met |
| 全部新增 map 深复制 | `cloneStateFile`（SettledOperations/LegacyEvidence.Resolved） | met |
| A2 不落 auth 正文或 hash；普通敏感 hash 按契约 | A2 摘要无 hash 字段（`a2_consumption_test.go` TestAttackA2SummaryContainsNoSecretMaterial 断言）；普通单元 BeforeHash/AfterHash 照契约 | met |

**注（机制等价偏离）**：intent 的 `ProposedEntry` 字段已声明但未作为恢复源使用；committed-unregistered 的基准重建（`applyIntentBaseline`）由 intent 的 Before/After 指纹 + upsert 实现，授权字段（Consented/Detached/SelectedKeys）绝不从 intent 臆造——契约「PriorEntry/ProposedEntry 不提供授权恢复能力」的语义成立，机制不同。

### 2. 统一文件/目录提交与恢复 — **met**（目录补偿一处 partial，见 §五）

| 契约项 | 落点 | 状态 |
|---|---|---|
| CommitRequest/CommitReceipt（applyProjection/TakeOver/资源/插件 settings 共用；error 非 nil 时 receipt 不丢） | `operation.go` + `commit.go` `commitFile`；UnitResult 携带 OperationID/Effect/Registration/Recovery | met |
| 前像在不可逆移动前持久且完整验证（目标 `.amagi-staging/recovery-<opID>/before`；0700 验证、distro chmod600、mode+完整字节复验、最终读回；坏备份原目标不动） | `commit.go` `persistPreimageBackup`；持续污染测试 TestAttackContinuousFaultNotOnlyFirst / TestReviewFixPR03* | met |
| 候选预验证（字节/类型/权限；发布前重读等于构建基准） | `commit.go` `persistCandidateStaging` + `verifyExpectedBefore`/`recheckBeforeIntact`；插件 `commitPluginFile` 以构建基准 hash 做 CAS | met |
| intent 保存读回后才改目标；写前存储不可用不「先写后补记」 | TestAttackPersistentIntentSaveFailureNeverWritesTarget（pending 目录换文件，目标逐字不动） | met |
| no-replace 发布/恢复（G0 `platform.MoveNoReplace`；displaced 写前记录；外部版本保留两处不覆盖） | `commit.go` `publishNoReplaceFn=platform.MoveNoReplace`；TestAttackExternalVersionDuringPublishKeepsBoth | met |
| move/publish 失败重新分类实际目标（不假定未写；no-replace 放回，失败保留 prev） | `classifyAfterMoveError`/`recoverPublishFailure`/`tryDisplaceBack` | met |
| 发布后读回+敏感权限验证；权限失败不视为瞬时成功；首建删除半提交；有 backup 条件恢复 | `verifyPublished` + `recoverUnverifiedCommit`（errVerifyReadback 哨兵区分毛刺/权限） | met |
| final save 失败：`content-committed-state-unpersisted`，intent+observation+磁盘事实保留；首发与第 N 次同义 | TestAttackFirstFinalSaveFailureKeepsFacts（重建 Coordinator 识别 committed-unregistered 并补基准） | met |
| 取消：第一次移动前目标不动；move-aside 后取消独立有限 recoveryCtx 恢复或明确保留位置；空目录不丢 | `recoverAfterCancel`（budget 10s、WithoutCancel）；TestAttackDirMoveAsideCancelRestores、TestReviewFixPR03CanceledAfterSideEffectsKeepsPreimage | met |
| 目录 swap：staging 树验证→intent（prev/staging/displaced）→ctx→移 prev→复验→ctx→no-replace 发布→验证→finalize | `skills_submit.go` `submitDirUnit`（重写为 receipt 生命周期）；resources 共用 | met |
| 撤回 remove：remove intent→条件隔离（保留 displaced）→复验 ENOENT→settled；后改 withdrawal-unconfirmed；absent+save 失败 removed-state-unpersisted；旧 write intent 不复活秘密 | `reconcile.go` `commitRemove`；TestAttackWithdrawalAfterUnregisteredWriteNoFreshLeak | met |
| 不得失败后首次备份；不得递归提交/恢复 | 前像写前保全；恢复全部单次有界（非递归） | met |

### 3. ReconcilePending / 补偿与释放 — **met**

- `ReconcilePending(ctx,target)`：RunUnits 在 Plan 前持 applyMu 调用；只处理请求目标登记路径；D/T 矩阵（not-applied/committed-unregistered/commit-unconfirmed/freeze-conflict/cleanup-residual/removed/withdrawal-unconfirmed）由 `reconcile.go` 实现；崩溃矩阵四态钉在 TestAttackReconcileCrashMatrix（公开入口、新 Coordinator 无旧 Undo）。
- settled+intent 并存 → 只清材料不重复业务；本进程活跃租用（leaseBook）不被清理；重启无租用时完成终结顺序（材料→intent→settled 墓碑）。
- Compensate：持 applyMu、先存 compensation intent（ParentOpID）、`context.WithTimeout(WithoutCancel,10s)` 独立预算、displaced/no-replace 恢复流程、旧句柄遇后一次内容 skipped（TestAttackReleaseAndOldHandleSkipsNewerContent）、取消后仍实际恢复（TestAttackCompensateCanceledCtxStillRestores）。
- `ReleaseCompensations(handles...)`：仅解除已终结（settled committed）操作的租用并清材料/墓碑；未终结材料保留。remote effect Commit→Release 已接 `launch_executor.go` `MarkCommitted`（类型断言，不强改 Runner 接口）；desktop/manual/save 成功路径由 effect Commit 同一入口覆盖。
- write未登记→撤回→重启：Parent/RelatedOpIDs 记录于 settled；旧 write intent 不复活（reconcile 优先撤回结论）。

### 4. PR01 legacy — **met**

- `TargetRef.LegacyGeneration`：同次 Identity 导出旧公式 `sha256(machine-id|uid|home)`（不重走 cached Home）；`TargetRecord.LegacyGeneration` 持久供 distro 级归属。
- A 发布→只检查 B→升级：**A 阻（指纹匹配+文件在）、B 放（指纹不匹配+文件不在；顶层线索不继承）** — TestAttackLegacyABMismatchBlocksOwnerNotChecker（原 LEGACY_MISMATCH 反例转正）。
- 同指纹克隆：A/B 都是候选，A 解除后 B 证据仍在 — TestAttackLegacyCloneBothCandidatesResolvedPerTarget。
- Detached/确切 withdrawn（整串相等，前缀不吞并）/consent-only 不伪造风险 — TestAttackLegacyTerminalStatesNoFabricatedRisk。
- 无归属/unknown：证据保真保留、不绑任何 distro、不全局误阻（TestReviewFixPR01LegacySensitiveRiskPending/v1 without distro…断言证据保留）。
- `AssessSensitiveConsumption(ctx,target,unit,runFacts)`：只核验当前目标固定视觉路径，判定矩阵（指纹/文件 hash/absent/不可读/目标不可解析退持久事实面）；`ConfirmLegacyVisionAbsent(ctx,distro)`：文件在则拒绝并给精确 distro/Home/文件名；absent 则持久 Resolved（保存失败返回未持久确认、证据不删）— TestAttackLegacyConfirmAbsentBoundaries。
- 手动处理后「检查/启动」重新核验即真实解除（gate 收到 legacy-clue-present-copy-absent 时自动落 absent 确认）；不写不存在的 takeover 提示（UI 面仅新增 `pending-op` 状态，无新按钮）。
- 兼容只读包装 `SensitiveCopyPending`/`LegacySensitiveCopyPending` 保留并改为证据/指纹语义。

### 5. A2 ctx 与聚合 — **met**（G0 集成后）

- `Probes` Identity/Chmod/StatMode 全部 ctx 形态；`DefaultProbes` = `platform.WSLTargetIdentityContext/WSLChmodContext/WSLFileModeContext`（真实 exec.CommandContext；无过渡接线残留，grep INTERIM-PRE-G0/TODO-G0 为零）。
- `A2Adapter.Sync(ctx, target TargetRef)`；`DefaultA2Adapter` 从 Coordinator 完整目标构造 `launcher.WSLAgentTarget{Distro,Home,LinuxDir,UNCDir,UID,MachineID}` 调 `launcher.SyncWSLPiLoginAuthContext`（不重选 cachedHome；身份核验与统一权限门在 G0 侧）。
- 无秘密 pending 摘要（Kind=a2-sync，OpID/完整目标/阶段；无 hash/token）：首个改 auth/权限动作前保存；摘要写失败不开始同步写（TestAttackA2SummaryWriteFailureBlocksSync，Sync 零调用）；结束 settled+清理（TestAttackA2PendingSummaryAroundSync）。
- `classifyA2(ctx,...)`：cancellation/deadline 一律 Failed（成功 outcome+取消 ctx 不是 Applied）— TestAttackA2CanceledCtxNeverApplied 全出口枚举。
- `verdictForWSLLoginSupply` 重排：先确认 A2 资格再处理可选项（不能因可选 Failed/awaiting-consent 早退跳过 A2）— TestAttackLoginVerdictOptionalFailureDoesNotSkipA2。
- recordA2State 保存失败 → observation 保留（finishA2Summary settled 失败路径）。

### 6. 插件真实 ctx — **met**

- runScript：取 opMu 后查 ctx；runner 返回 nil 但 ctx 已结束 → canceled，不执行后继 remove/install/filter（mid-flight 取消测试）。
- `applyDistroFilters`：读后/解析后/发布前 ctx 检查；最终发布经统一提交回调 `commitPluginFile`（构建基准 CAS、前像备份/intent/no-replace/恢复；发布后才取消 → `filter-committed-verification-pending` 不声称零写）。禁 tmp+无条件 rename 旁路。
- Coordinator `RunPluginOperation` 持 applyMu（锁序 applyMu→opMu→短 state.mu；state 锁不跨网络/UNC I/O）。
- 插件 pending 摘要（完整目标/source/anchorRef/过滤形态，无秘密）：首个改变状态命令前保存；首次失败也保留（TestAttackPluginInstallThenCancelKeepsPendingNoFilters：canceled 非 OK/Installed、settings 无 filters、重启摘要仍在且不自动重放）；成功后收敛清理同目标旧 pending。
- `RegistrationSummary.PendingOps`：未决摘要优先于旧 installed 展示。
- nil/[]/全 nil 过滤三态语义原样保留（既有 PR08 回归全绿）。

### 7. shared gate / 状态公开 — **met**

- `gateWSLSharedVisionConsumption`：真实 assessment/result facts（Consumption 枚举 + PendingSensitiveOperation 持久事实），不靠 LastOutcome 字符串或 bool 历史；unsafe/unknown/pending 不放行；Applied/Fresh/WarnDegraded 不跳过已知风险；runErr 也经资格核验（无「无历史即安全」放行）；确无副本线索的普通可选失败不误阻（TestAttackSharedGateManagedCopyFailedRunBlocks）。
- 5CLI 统一入口不变（desktop `runWSLSharedFilesAtLaunch`/`runWSLManagedModelsAtLaunch`、remote/restart `wslConfigSupplyEffect` 同一 gate/verdict 面）；既有 TestWSLSharedGateBeforeDesktopAndRemotePTYForEveryCLI 等全绿。
- 无资格核验面的测试 stub 运行器退回持久事实谓词（SensitiveCopyProber）；生产协调器恒走真实 assessment 面（capability 缺失=false 的生产旁路不存在）。
- GetStatus 合并：UnitStatus.PendingOperation/StateUnpersisted/LastAttemptOutcome + TargetStatus.StoreHealth/PendingIntents；状态页 `pending-op`（待确认）优先于「已同步」（frontend WSLCLISettings.vue 映射；无新绑定方法 → 无 wailsjs 再生需求；face 结构未加字段）。
- dirty 进度：`clearSatisfiedDirtySources` 不再把 WarnDegraded 一律计成功、携带未确认操作事实的结果不计入（UnitResult.PendingOperationFact）。

## 二、失败矩阵真实命令与结果

全部 cwd=worktree；日志在 `.amagi/attack-core/`：

| 验证 | 命令 | 结果 |
|---|---|---|
| 目标包普通 | `go test . ./internal/wslsupply ./internal/launcher ./internal/piplugin -count=1` | ok×4（final-target-tests.log） |
| 目标包 race | 同上 `-race` | ok×4（final-target-race.log） |
| 全仓 vet | `go vet ./...` | exit 0（vet-final.log） |
| Windows 编译 | `GOOS=windows GOARCH=amd64 go build ./...` | exit 0（windows-compile-final.log） |
| Windows 测试编译 | `GOOS=windows GOARCH=amd64 go test ./... -run '^$' -exec /usr/bin/true` | exit 0（windows-compile-test-final.log；仅编译，未执行） |
| G0 原语集成面 | `go test ./internal/launcher ./internal/platform -run 'TestA2|TestWSLContext|TestMoveNoReplace|TestEnsurePrivate'` | ok（final-g0-integration.log） |
| frontend | `npm --prefix frontend run build` / `run test` | build ok；31 files/277 tests 全过（frontend-build.log / frontend-test.log；本树 node_modules 符号链接复用主树真实依赖） |
| delta round-trip | 模拟基座（resumed-input tree + G0 delta + correction）上 `git apply --check`+`git apply attack-core.delta.patch` | 成功（FULL_STACK_APPLY_OK；三段连序） |
| 已知基线失败 | `go test ./internal/platform` | `TestDarwinResolveExecutableFindsCodexInLocalNodeBin` 失败——hermetic 任务写面（resolver_test.go），Leader 已在主树合入修复、我的基线不含；非本 delta 内容（G0 报告 §3.2 同记） |

### 复审反例转正与契约纠正断言

- LEGACY/LEGACY_MISMATCH → TestAttackLegacyABMismatchBlocksOwnerNotChecker（A 阻 B 放 + 手动处理+确认 absent 解除）。
- STATE_FAIL/STATE_FAIL_THEN_WITHDRAW → TestAttackFirstFinalSaveFailureKeepsFacts（intent 在盘、目标新内容、重建 Coordinator 补基准；撤回后不 Fresh 漏副本 — TestAttackWithdrawalAfterUnregisteredWriteNoFreshLeak）。
- RECOVERY（持续污染/取消）→ TestReviewFixPR03ReadbackMismatchBoundedRecovery（持续污染：目标逐字不变、无 preserved 假声明、无失败后备份）+ TestReviewFixPR03CanceledAfterSideEffectsKeepsPreimage（契约纠正版：提交后取消，原目标或有效持久前像至少一处保留原字节）。
- DIR_CANCEL → TestAttackDirMoveAsideCancelRestores（move-aside 后取消：恢复或明确保留，空目录不丢）。
- 契约点名纠正的旧断言已改写：stagingSabotage 持续污染（每次命中）；PR01 without-distro 改为「证据保留不绑 distro」；TestCorruptStateFileFallsBackToUnmanaged 改为「store-unhealthy 停新写、原文件不被空清单覆盖、修复后重记录」。

## 三、新增/变更公共接口完整列表

**internal/wslsupply（新增）**：`OpKind`(write/remove/dir-swap/compensate/a2-sync/plugin-op)、`OpEffect`、`OpRegistration`、`OpRecovery`、`Consumption`、`OperationIntent`、`SettledOperation`、`OperationObservation`、`CommitRequest`、`CommitReceipt`、`StoreHealth(Kind)`、`LegacyEvidenceEntry.TerminalRiskCleared`、`SensitiveAssessment`、`ReconcileDisposition/ReconcileReport`、`PendingIntentSummary`、`Coordinator.ReconcilePending`、`Coordinator.AssessSensitiveConsumption`、`Coordinator.AssessWSLSensitiveConsumption`、`Coordinator.ConfirmLegacyVisionAbsent`、`Coordinator.LegacyEvidenceSnapshot`、`Coordinator.ReleaseCompensations`、`Coordinator.PendingSensitiveOperation`、`Coordinator.StoreHealthSnapshot`、`PluginScope.PluginCommitFn`、`PluginRegistrationSummary.PendingOps`、`UnitResult.PendingOperationFact`。
**internal/wslsupply（变更）**：`A2Adapter.Sync(ctx, TargetRef)`（原 `Sync(distro)`）；`Probes.Identity/Chmod/StatMode` 带 ctx；`StateFile.SettledOperations/LegacyEvidence`、`StateEntry.LastOperationID`、`TargetRecord.LegacyGeneration`、`TargetRef.MachineID/LegacyGeneration`、`UnitResult.OperationID/Effect/Registration/Recovery/Consumption`、`UnitStatus.PendingOperation/StateUnpersisted/RecoveryPending/LastAttemptOutcome`、`TargetStatus.StoreHealth/PendingIntents`。
**root**：`gateWSLSharedVisionConsumption`/`wslSensitiveAssessor`（私有）；`verdictForWSLLoginSupply` 判定序重排（新增 `wslLoginA2Verdict`/`wslLoginFailClosed` 私有助手）；launch_executor Commit→Release 接线；frontend `pending-op` 状态映射。**无新增 Wails 绑定方法、无 face 结构字段变更 → 无 wailsjs 再生需求。**

## 四、未覆盖项（如实列出，不删矩阵凑 met）

1. **Windows/WSL 真机全部 pending**：MoveNoReplace 在 UNC/9P 的目录移动/错误码、EnsurePrivate 真实 ACL、真实持续 I/O 故障与取消前像、外部改写竞争、wsl.exe 取消后 Linux 子进程存活/后继写、desktop/remote/restart 实际 CLI 消费、AMAGI_WSL_E2E（任务禁跑）。交叉编译/vet 只证明编译面。
2. **崩溃测试为确定性构造**：intent/目标状态手工构造模拟崩溃窗口（非真实进程 kill）；契约允许的可证识别范围内。
3. **目录撤销句柄补偿**（compensateSkillDir）保留既有 staging 快照恢复（F2/F7 语义：rename 失败保留 staging、树 hash 复验），未升级为 displaced/no-replace 流程——主提交路径（submitDirUnit）与取消恢复已用新生命周期；该函数是 **partial**。
4. **并发插件写 × Apply 竞争**：靠 applyMu 串行化保证（契约设计），未新增专属 race 测试；既有全包 -race 绿。
5. **前端待确认状态**仅有状态映射与文案，未新增组件级测试（既有 31 文件/277 测试全绿含设置页）。
6. `VisionExportAdapter.Plan` 判定逻辑未改（reconcile 前置处理后 Plan 语义成立——契约「Coordinator 在规划前处理 pending」的接入方式）。
7. G0 报告 §一.2 四条接线全部完成（A2Adapter.Sync/pending 摘要/classify 兼容面/verdict+gate 聚合）；PR02/PR07 的全产品关闭以 Leader 集成与独立审核为准，本报告不自证放行。

## 五、迁移与回滚约束

- v3 兼容读 v1/v2、迁移幂等；**禁止用旧二进制降级读取 v3**（更高 schema → unsupported，不覆盖原文件）。回滚顺序：先停受影响自动写 → 保全 `wsl-config-supply.json`（v3）、`wsl-config-supply-pending/`、目标 `.amagi-staging/recovery-*`/`.amagi-supply-displaced-*` → 无未决操作后经明确授权导出旧格式。删除字段/还原旧 state 快照不消除已发生写入；代码回滚不等于秘密撤回；A2 token 禁止回旧快照。
- 本 delta 回滚：在已应用树上 `git apply -R --check attack-core.delta.patch` 通过后 `git apply -R`（不触碰 G0/resumed-input 内容；新增文件随 -R 删除）。

## 六、验证自检

- 生产与测试均无 goroutine 假超时（恢复预算为 ctx.WithTimeout+WithoutCancel 同步预算；取消的 wsl.exe 经真实 CommandContext）。
- 无新增第三方依赖；未 commit/push；未全仓 gofmt（仅本次触碰文件）；未读真实 auth（全部合成 fixture）；未跑 AMAGI_WSL_E2E；未清理用户数据/远端/旧 worktree；主树仅写两约定产物。
- hermetic 任务写面（envcheck、platform resolver_test）零改动；frontend/dist、mobile/dist 为复用主树真实构建产物（.gitignore 覆盖，不入 delta）。
