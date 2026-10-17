# 持久化与目录恢复一致性修正 — 实施报告

- 任务：`consistency-fixes-assignment.md`（worktree `wsl-goal-transaction`，基线 = 已交 G1 净增量 + G0 + correction，HEAD `58e7577`）。
- 产物：`consistency-fixes.delta.patch`（14 文件，+1314/−78 净增量，含新回归测试 `internal/wslsupply/consistency_fixes_test.go` 781 行）。
- 起步核对：worktree 与「resumed-input + G0 delta + G0 correction + attack-core delta」重建基线 `diff -rq` 逐字节一致（仅 gitignored 构建产物 `frontend/dist`、`mobile/dist`、`amagi-codebox` 二进制，即 G1 报告声明的真实 embed 产物复用）——无额外写入。完整前像已保存（本树 `.amagi/consistency-fixes/preimage-full-worktree.patch`，11668 行）。
- 本轮不是独立审核，不输出审核 PASS；以下 12 项观测逐条「先建真实失败断言 → 修生产 → 正反回归」。夹具核对结论：**12 项夹具全部对应生产可达形态，无迎合性修正**；仅 1 处测试面增强（见 §fake Chmod）。

## 一、逐项观测 → 根因 → 修复 → 回归

| # | 观测 | 根因（源码核对） | 修复 | 回归测试（全部先红后绿） |
|---|---|---|---|---|
| 1 backup-path | 正常已有文件敏感更新返回 backup-unavailable；chmod 目标 `.amagi-staging/op-…/before` 不存在 | `commit.go::persistPreimageBackup` 实际落盘 `.amagi-staging/recovery-<opID>/before`，但 chmod/StatMode 用调用方拼的 `rel` 参数（缺 `recovery-` 前缀）——路径错位，生产真实可达（探测面打在不存在路径上必失败）。夹具真实 | chmod/验证路径改由 `relPathOf(target, finalPath)` 与落盘路径同源派生（backup 与 candidate staging 两处统一）；fake `Chmod` 增加真实文件存在性校验（不再对不存在文件无条件成功，mode/chmod 全部基于真实临时文件状态） | `TestConsistencyBackupPathSensitiveUpdateSucceeds`：正向更新成功、`BackupRel == .amagi-staging/recovery-<opID>/before`、Recovery=available、敏感 chmod 落在真实路径（mode=600 有记录即证 chmod 打在真实文件上——错位路径在存在性校验下必 ENOENT） |
| 2 legacy-unknown-and-hash | 文件内容与历史 hash 一致但指纹 unknown/不匹配 → user-owned/risk=false | `legacy_assess.go` 分支 4 读出 `data` 后丢弃（`_ = data`），内容正证据从未参与判定；契约矩阵「旧指纹不同、文件 hash 匹配 → 只关联本目标风险」未实现 | 分支 4 增加 `contentMatch`（`EntryLastHash != "" && fileHash == EntryLastHash`）→ unsafe+pending（reason `legacy-content-hash-match`）；指纹与内容双弱才 user-owned | `TestConsistencyLegacyUnknownFingerprintHashMatchKeepsRisk`：unknown/不匹配指纹 × 内容匹配 → unsafe+pending；内容指纹双不匹配 → user-owned（解除）；B 目标（文件不在）不受 A 线索全局误阻 |
| 3 v2-migration | 真实 v2 形态（schema2+Targets+新公式 gen 的受管 Units）迁移后被遗忘成 user-owned/never-managed | `state.go::migrateLegacyState` 对 schema<3 一律隔离 v1 路径——但 v2 的 Units/Targets 已用新公式指纹 `sha256(distro\|machine-id\|uid\|home)`（与 v3 同构，经 git 历史核对 v2 生产者），可归属记录被误隔离 | 迁移按 schema 分叉：`migrateV2ToV3`（v2：Units/Targets 原样保留继续受管；legacy:- 前缀敏感 entry 提取 v1 证据；LegacySensitiveRisk 转低可信证据）；v1 路径原样（整体隔离+证据） | `TestConsistencyV2MigrationKeepsManagedRecords`：真实 v2 完整形态（Targets 索引 + 受管视觉/modality 单元 + v1 遗留隔离区 + 派生 risk map）→ entryFor 命中、评估 safe（非 user-owned/never-managed）、v1/v2-derived 证据保真；`TestConsistencyV1MigrationStillQuarantines`：v1 语义不变 |
| 4 wrong-root-reconcile | shared 与 pi targetKey 相同；pi TargetRef 恢复 shared intent 被判 not-applied 并清未决，文件仍在 shared 根 | `operation.go::targetKeyOf` 只用 generation（不含 CLI 作用域）——同 distro 身份的 `.pi/agent` 与 `.agents` 根共享键；`reconcileIntent` 用**当前调用者**的 UNCRoot 解释 intent 的 RelPath | ① targetKey 入 CLI 作用域（`generation\|cli` / `unknown:distro\|cli`）；② intent 新增持久 `TargetCLI` 字段（6 处创建点统一注入）；③ reconcile/reconcileTargetPath 增加 `intentScope`（字段优先，单元前缀派生兜底）归属验证——非本作用域不处置不清未决（旧格式键因键位不等天然跳过，不猜 absent） | `TestConsistencyWrongRootReconcileKeepsSharedPending`：pi reconcile 不处置 shared intent（dispositions 空、intent 保留、secret 在 shared 根、PendingSensitiveOperation=true、评估非 absent）；shared 自己 reconcile 正常收敛 committed（正向对照）；新 Coordinator 同断言 |
| 5 dir-cas-and-structure | staging 后、move-aside 前插入 EXTERNAL 文件 → applied 且文件消失 | `skills_submit.go::submitDirUnit` 仅在 staging 构建前（applySkillUnit/applyResourceUnit）做一次 CAS；intent 保存与 move-aside 之间无基准复验，外部编辑被搬走的旧树带走 | ① move-aside 前对构建基准复验（`scanTargetSkillDir(targetDir).TreeHash != ExpectedBefore` → fail `target-changed-before-move`，未授权变更不覆盖）；② 搬移后复验 prev 树 == 复验时基准（`beforeTreeHash`），不符 → no-replace 放回保全外部版本，不发布候选 | `TestConsistencyDirCASExternalEditDuringStagingRetained`：注入后提交失败、EXTERNAL 字节原位保留、原树完整、无 Undo；`TestConsistencyDirCASExternalNewTargetDuringPublishKeepsBoth`：发布前外部新建目标 → publish-conflict、两处保留 |
| 6 目录 Abort 丢空/嵌套目录 | confirmed 但 empty/nested 丢失 | 旧 `compensateSkillDir` 用内存快照重建；`snapshotSkillPreimage` 只记录文件/链接——空目录不入条目（树 hash 也不含目录），重建即丢 | ① 补偿优先用**真实持久 prev 树** no-replace 放回（结构/空目录/链接天然保全）；② 快照兜底路径也记录空目录（`skillPreimageEntry.IsDir`），恢复重建、不入 hash（与扫描面同构） | `TestConsistencyDirAbortPreservesFullTreeStructure`：TakeOver→Compensate=confirmed 且 `empty/nested`、`sub/deep.txt`、SKILL.md 字节全部恢复（完整树断言，非仅 SKILL.md） |
| 7 resource-abort | resource 目录更新后 Compensate unavailable、旧内容未恢复且 prev 已不在 | `submitDirUnit` 成功后立即 `RemoveAll(prevDir)`（唯一持久前像被先删）；资源路径无内存快照 → 只剩 unavailable；G1 报告 §四.3 承认的 partial | **partial 如实关闭**：① 提交成功不再删 prev——保留至 Release/补偿终结（撤销租用自提交起生效，reconcile 尊重活跃租用；Release/重启无租约后按终结顺序清理）；② `compensateSkillDir` 重写为与文件补偿同构：租用生效→compensation intent（ParentOpID）→树 CAS→displace 本次写入→复验→no-replace 放回 prev→树 hash 复验→finishCompensation；③ prev 丢失才退回内存快照（有界），两者皆失才 unavailable（不再先删再报）；④ 不扩大内存快照限额（大目录靠持久 prev） | `TestConsistencyResourceAbortRestoresFromPersistedPrev`：资源目录更新→Compensate=confirmed、OLD 恢复、prev 按终结清理；`TestConsistencyResourceAbortLargeUserTree`：9MB 用户大树（快照超限=false）仍从持久 prev 完整恢复（外部后改/大目录保全不靠内存限额） |
| 8 directory-restart | 已提交目录重建 Coordinator 被通用 `ReadFile` 当 unreadable（EISDIR），commit-unconfirmed 永不收敛 | `reconcileIntent` 对 dir-swap 用文件读取判定 | 按 Kind 分流：`reconcileDirIntent` 树形态验证（typed fingerprint：`AfterHash`/`PrevTreeHash` 都是树 hash）——树==after→committed（补基准）；==before→restored；目标缺失且 prev 在→no-replace 放回复验；后改→conflicted；不可核验→unconfirmed（保留材料，不猜 absent）。`submitDirUnit` 持久化 `PrevTreeHash` | `TestConsistencyDirectoryRestartReconcileConverges`：三态收敛（settled→cleanup-residual 且 prev/intent 清理；树==after→committed 补基准 `LastWrittenHash`；见下条冲突态）；`TestConsistencyDirReconcileMismatchConflicted`：树后改→conflicted、材料保留（非 not-applied） |
| 9 corrupt-pending | 首轮 RunUnits 在 pending 有 broken.json 仍 applied，末尾 Health 才变 corrupt | `RunUnits` 健康门消费**缓存**健康值（构造默认 pending=ok），扫描发生在门后的 ReconcilePending | 健康门前先 `loadIntents()` 完成本次扫描再决策；`reconcileTargetPath` 同样在自己的扫描后拒 corrupt/unreadable（写前预检不靠缓存）；损坏材料保留、禁止新写 | `TestConsistencyCorruptPendingBlocksFirstRun`：首轮 blocked（store-unhealthy）、目标未写、broken.json 保留、Health.Pending=corrupt（本次扫描值）；移除损坏文件后同协调器恢复 applied（健康/fresh 路径继续工作） |
| 10 plugin-cross-source-cleanup | A 源未决记录被 B 源成功清掉 | `plugins.go::finishPluginPending` 成功后按**同目标全清**旧 pending 插件摘要 | 只结清同一插件身份（`pluginPendingMeta` 增 `identity`；旧摘要无标注时从 source 串归一，不可归一不结清）——同包不同 ref（版本切换语义）收敛，跨源保留可见 | `TestConsistencyPluginCrossSourcePendingKept`：A 源 pending 在 B 源成功后仍在；同身份旧 pending 被成功收敛；A 的未确认副作用持续可见 |
| 11 a2-final-save | state save 持续失败后 applied + registration confirmed + PendingOperationFact=false | `coordinator.go` A2 分支先置 `Registration=Confirmed` 再调 `finishA2Summary`；后者**只改值副本**，settle 失败不回传 | `finishA2Summary` 返回 settle 错误；确认声明移到成功后（失败→Registration=pending、Detail=`a2-state-unpersisted`、内容事实保留 applied 且 Effect=committed、不回滚新 token）；observation/磁盘摘要/结果同事实 | `TestConsistencyA2FinalSaveFailureReportsUnconfirmed`：outcome=applied（token 不回滚）+ Registration=pending + PendingOperationFact=true + observation ErrorCode=a2-state-unpersisted + pending 摘要 intent 在盘且状态页可见（无需先写）+ 摘要无 auth 正文/hash |
| 12 status-without-target | 新 Coordinator 仅有 pending、无 TargetRecord 时 GetStatus Checked=false 且丢 PendingIntents/StoreHealth | `GetStatus` 在 `!found` 提前 return，位于 StoreHealth/PendingIntents 赋值之前 | 健康与未决事实在早退前填充（未检查可以，待确认事实/健康错误不可丢） | `TestConsistencyStatusWithoutTargetShowsPendingAndHealth`：Checked=false 但 PendingIntents 含 a2 摘要、StoreHealth 填充；主清单损坏时状态页展示 corrupt + pending 并存 |

契约一致性：未给任何分支加特殊绿灯；锁序（applyMu→opMu→短 state.mu）、取消（recoveryCtx 有界预算）、reconcile/release（租约自提交起生效至 Release/补偿终结）、legacy 解释（指纹弱证据 × 内容正证据 × 无关用户文件三分）全链路统一。

## 二、连带一致性修正（修 12 项时暴露的同类缺陷）

1. `persistCandidateStaging` 同样的错位隐患（当前调用恰好一致）——统一改为同源派生。
2. `resolveLegacyEvidenceIn`/`ConfirmLegacyVisionAbsent` 证据 Resolved 键双写（完整 targetKey + 裸 generation）：targetKeyOf 入 CLI 后两族消费面（逐目标 assessment / 按代际迭代的 `LegacySensitiveCopyPending`）同时收敛，`TestReviewFixPR01LegacySensitiveRiskPending` 全绿为证。
3. 撤销租用自提交（`newUndoLease`/`newDirUndoLease`）起生效——此前仅 Compensate 期间登记，提交后、补偿前的 reconcile 可清掉活跃句柄的前像材料（#7 的深层根因之一）。

## 三、测试面调整（不降低断言强度）

- `fakeProbes.Chmod`（coordinator_test.go）：增加真实文件存在性校验（生产 chmod 对不存在路径必失败；夹具面不得掩盖错位路径）。
- `TestAttackA2SummaryWriteFailureBlocksSync`（a2_consumption_test.go）：破坏方式从「pending 目录换成文件」改为「已存在目录 chmod 0500」——前者现在会被（正确的）首轮健康门以 `pending-store-unreadable` 阻断，后者精确隔离「摘要写失败但存储可读可扫」的 A2 专属语义；断言（prepare-state-failed、Sync 零调用）不变。
- `TestRestorePreimageRenameFailureKeepsStaging` / `TestRestorePreimageChirmodAndVerify`（skills_test.go）：补偿优先走持久 prev 后，两测试显式移除 prev 材料（模拟外部丢失）退回快照兜底路径——F2（rename 失败保留 staging）/F7（chmod 补偿+树复验）语义原样钉住，断言不变。

## 四、验证证据（cwd=worktree；日志 `.amagi/consistency-fixes/`）

| 验证 | 命令 | 结果 |
|---|---|---|
| 目标包普通 | `go test . ./internal/wslsupply ./internal/launcher ./internal/piplugin -count=1` | ok×4（final-target-tests.log） |
| 目标包 race | 同上 `-race` | ok×4（final-target-tests.log） |
| 一致性回归 | `go test ./internal/wslsupply -run TestConsistency -count=1 -v` | 16/16 PASS（final-target-tests.log） |
| 全仓 vet | `go vet ./...` | exit 0（final-vet-windows.log） |
| Windows 编译 | `GOOS=windows GOARCH=amd64 go build ./...` | exit 0 |
| Windows 测试编译 | `GOOS=windows GOARCH=amd64 go test ./... -run '^$' -exec /usr/bin/true` | exit 0，47 ok，仅编译未执行（windows-compile-test.log） |
| delta round-trip | G1 前像（重建基线）上 `patch -p1 --dry-run` + `patch -p1` | 应用成功；14 文件与工作树逐字节一致（cmp）；重建树上 `go test ./internal/wslsupply -count=1` ok |
| G1 前像核对 | 基线重建 `diff -rq` + `git apply -R --check attack-core.delta.patch` | 一致（仅 gitignored embed 产物）；G1 delta 逆向检查通过（起步时） |

## 五、接口变化（全部向后兼容）

- `OperationIntent.TargetCLI`（新增持久字段，`targetCli,omitempty`）：旧读者忽略、新读者缺失时从 Unit 前缀派生；intent 格式版本不变（v1）。
- `targetKeyOf` 键位格式变化（`generation\|cli`）：进程内/settled/intent 键位同步一致；G1 schema 未发布，无在产 intent 需要迁移；旧格式键（无 `|`）与新键永不相等——不可归属 intent 一律保留可见，不自动处置（不猜 absent）。
- 未导出符号签名变化：`finishA2Summary`（返回 error）、`finishPluginPending`（增 spec 参数）、`newDirUndoLease`（增 beforeTreeHash）——包内调用方同步。
- **无公共 API/绑定方法变化**：Coordinator 导出方法签名全部不变、无 wailsjs 再生需求、前端零改动（既有 `pending-op` 状态映射即 #12 的展示面）。

## 六、未覆盖项（如实）

1. Windows/WSL 真机全部维持 pending（no-replace 目录移动 UNC 行为、真实持续 I/O 故障、wsl.exe 取消后 Linux 子进程存活等）——由 Leader 承担，与本轮边界一致。
2. 已知集成问题（不属本轮、断言未动）：`TestR6_001_ClaudeExternalRunRejectsStopAndUninstallUntilStopSucceeds`（root external_headroom_lease_test.go）原生竞态另有专人修复。
3. `submitDirUnit` 无同路径未决 intent 预检（G1 既有形态，非本轮 12 项观测范围；RunUnits 健康门/reconcile 覆盖主路径）。
4. 快照兜底路径的链接恢复仍 best-effort（F1 语义原样）；持久 prev 路径保全真实树不受此限。
5. 环境残留：一个临时 scratch worktree（系统 temp 目录）因共享树守卫拦截未能自动清除——其内容为已保全补丁的可复现重放（声明见本树 `.amagi/consistency-fixes/scratch-worktree-preservation-note.txt`），Leader 可在 temp 清理后 `git worktree prune`。

## 七、回滚

在已应用树上 `patch -R -p1 < consistency-fixes.delta.patch`（或 `git apply -R`）；净增量只含本轮 14 文件，不触碰 G0/resumed-input 内容；新增测试文件随 -R 删除。代码回滚不消除已发生写入；v3/pending/前像材料保全语义与 G1 相同。
