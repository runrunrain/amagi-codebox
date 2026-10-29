# Phase 1 第三轮修复报告

> 后续 F10/R04 收口见文末「Phase 1 第四轮窄修（fix3）」；下方原正文是 cab85ae 交付前的历史记录，不替代后续审核与本轮验证。

## 结论、输入与现场

本轮补齐状态发布统一、absent reservation、F10/F15，并验证、修正中断遗留的 F05/F06/R02/R03 与测试修复。全部要求的验证命令 exit 0；Go vet 无 Go 诊断，C 编译仍有既有 macOS Security/Keychain API 弃用警告，未通过隐藏警告实现“干净”。本报告不是独立复审或真机发布签收。

- 工作目录：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/claude-account-rotation`。
- HEAD：`c15bd6c`，分支 `amagi/claude-account-rotation`。
- 必读上游（已完整读取）：
  - `/Users/run.mao/workpace/amagi-codebox/agent-outputs/2026-10-28-claude-account-rotation/phase1-diting-rereview.md`
  - `/Users/run.mao/workpace/amagi-codebox/agent-outputs/2026-10-28-claude-account-rotation/phase1-diting-review.md`
- 已读原未提交 diff 与内联 `internal/claudeaccounts/phase1_fix2_test.go`；保留其有效实现，不重放已完成工作。现场归因：乙中 F05/F06/R02/R03 和丙已有实现但未验证；甲的发布序列、absent reservation 及 F10/F15 仍缺实现。
- 修正遗留细节：F05 原先“三方组织均未知可放行”改为显式拒绝；R02 在重新分类之外，补上分类读→短锁读的 revision 再次漂移拒绝。
- Git 约束：任务正文要求一次提交，但末尾 worktree 注入段明确“不要 git commit”。遵循后者：**没有暂存、提交、push 或合并**，全部改动留工作区，交 Leader 收尾。既有 `amagi-task/` 与其他报告未修改。
- 产物按机器冻结路径写在 worktree 本路径，未另写主树副本。

## 架构统一机制

### 单一发布序列

`claude_accounts_launch.go::refreshClaudeAccountsCache` 是运行时 `pool/loadErr/recovered` 的唯一缓存发布点。`App.claudeAccountsPublishMu` 覆盖完整的 **PoolStore.Load → 存在性标记检查/落盘 → 缓存发布**；原 `publishClaudeAccountsAuthoritativeState` 只调用该入口，不再独立写缓存。启动恢复完成标志也在该入口发布。

生产 mutation release 仍持有 coordinator mutation admission 时进入同一发布路径；完成权威发布之后才释放 admission。直接 PoolStore 读取不反取正在持有的 Service mutex，避免 Service release 回调重入死锁。未装配 PoolStore 的测试兼容路径保留 Service.LoadPool；生产装配始终提供同源 PoolStore。

对 R01 目标时序的结构性证明：

1. 普通 refresh 读到旧 A 后，仍占有发布序列锁，不能把“快照已读”和“发布”拆成可被另一个发布者跨越的两段。
2. mutation 可落 journal，但它的 release 必须等待同一发布锁，且等待期间仍持 mutation admission，launch 无法取得 reservation。
3. 旧 A 发布并释放序列锁后，mutation 才能重新 Load journal、发布 blocked，再释放 admission。
4. 因此只可能是“旧发布→新 blocked 发布”，**不可能**“旧 Load 返回→新 blocked 发布→旧 refresh 发布”。该方案用互斥串行化整个读取发布区间，不是只比较 generation，也不依赖测试中的偶然调度。

### absent 启动执行屏障

- 桌面 `beginClaudeCredentialsClaim` 在池 absent 时也取 coordinator 凭据 admission；获取后复核 policy，封住判定与获取之间完成 mutation 的情况。该 reservation 沿既有成功 lease 提升/失败释放链清理。
- `claim.absent` 区分 reservation 与 managed 身份环境：absent 不剥离受管专用身份键，不新增 managed HOME/WSL 拒绝；可证明独立根仍按既有边界释放。
- remote create/restart 在 absence check 后、Executor.Prepare 前，通过 `reserveCredentialPlanAbsence` 取得同 coordinator 的 reservation，**取得后再验证 absent**，并保持至该执行调用结束。Prepare/后续启动失败均由 defer 释放。早期瞬时 absence check 仅用于提早拒绝，不再承担互斥证明。
- MutationGate 与 launch reservation 都使用同 coordinator admission，获取/互斥是原子状态转换；没有另造一套凭据互斥器。

### 恢复命令与基线

人工 complete-target 使用 `RecoverTransaction(RecoveryGuard)`；restore-source 使用 `SwapRequest.Guard`。guard 在各自 `withLocks` 内、任何写入前验证 expectedRevision，restore-source 同时验证所选 TxnID。启动自动恢复使用同一个内部 recover 实现，不要求 UI revision；adopt-current 原有锁内 expectedRevision 校验保持。

roll-forward 携带本轮分类快照进入原生短锁，先比较分类读与短锁读 revisions，再逐投影确认 before/after；unknown 或再次漂移拒绝。对于合法“Patch 已成功但进度 Save 未完成”，短锁中的当前分类可证明 after，此时冻结当前 revisions、保存新的恢复基线，只补仍为 before 的投影，而不是要求崩溃前的旧进度必已落盘。

## 验收逐项映射

以下锚点为相对工作树的文件与函数/组件测试名，可直接检索。

| 项目 / finding | 修复与锚点 | 行为证据 / 测试名 | 状态 |
|---|---|---|---|
| 1 / F01、R01 单写者 | `claude_accounts_launch.go::refreshClaudeAccountsCache`；`app.go::claudeAccountsPublishMu`；mutation release 同路径发布 | `TestFailedMutationPublishesBlockedBeforeFacadeRefresh`：真实 Switch 部分写失败留 journal，普通 runner refresh 暂停时真实 LaunchSession 已 blocked | 通过 |
| 2 / R01 旧读倒灌 | 发布锁跨完整 Load→publish，不再两条无序发布链 | `TestPublicationOldLoadCannotOverwriteNewBlocked`：旧快照形成后暂停，真实 BeginEnrollment 写 journal；等待期间 launch busy，恢复后最终 blocked | 通过 |
| 3 / F01 absent 窗口 | 桌面 absent claim；`internal/remote/remote_session_adapter.go::reserveCredentialPlanAbsence` 与 create/restart 接线 | `TestAbsentDesktopLaunchReservationBlocksFirstCapture`；`TestAbsentRemotePrepareReservationBlocksFirstCapture/create`、`/restart` | 通过 |
| 4 / F06 锁内命令校验 | `swap.go::RecoveryGuard.checkLocked`、`Swap`；`reconcile.go::RecoverTransaction/recover`；facade 不再预读后无 guard 执行 | `TestRecoveryRevisionChangedAfterFacadeRead` 两分支：真实恢复 operation 暂停，Rename 推进 revision 而不改变 generation，继续恢复被拒；`TestRecoverTransactionGuardInsideLocks`；`TestSwapGuardRejectsStaleRestoreSource` | 通过 |
| 5 / 精确屏障 | 新增根包 `claude_accounts_release_test.go`、`claude_accounts_publication_test.go`、`claude_accounts_barriers_test.go`；nil 默认语义 checkpoint 仅供隔离测试注入 | 上述 release/旧 Load/absent Prepare/恢复 revision 窗口均走 facade→Service→生产 gate，不仅调 helper；race 重复 10 轮通过 | 通过 |
| 6 / F05 Pi 组织 | `harvest.go::verifyTargetGrantsLocked/verifyOrganizationTriple`：CC/Pi 各自 uuid+org 对目标；本地 identity UUID 及其存在的组织字段校验；任一 profile 组织未知拒绝（含全部未知） | `TestSwapRejectsPiGrantOrganizationMismatch`；`TestSwapRejectsPartialOrganizationEvidence`；`TestSwapRejectsUnknownOrganizationAndLocalIdentityDrift`；`TestVerifyOrganizationTriple` | 通过 |
| 7 / F10 存在性 | `claude_accounts_launch.go::claudeAccountsMarkerStatus/writeClaudeAccountsMarker`：仅 ENOENT=不存在；非普通文件/其他 Stat 错误 blocked；写入、file Sync、目录 Sync 错误不得发布 managed | `TestMarkerErrorsFailClosedAcrossAppRebuild`：真实 ENOTDIR 与只读目录写失败（先断言 Stat ENOENT，确证走写失败分支）；`TestF10_PoolMissingAfterExternalDeletionFailsClosed` 覆盖持久标记与缺池 | 通过 |
| 8 / F15 在途上限 | `claude_accounts_facade.go::claudeOpBegin/runClaudeAccountOperation`：在途限额 16；幂等命中优先；超限稳定 `POOL_LOCKED`（busy）；完成缓存限额 128 独立淘汰 | `TestOperationInflightLimitBusyAndIdempotent` 真 runner 阻塞 16 项并验证第 17 项 busy；`TestF15_CacheEvictionKeepsRunningOperations` 验证最旧 done 被删、running 和 requestId 保留 | 通过 |
| 9 / R02 新恢复基线 | `swap.go::applyAndCommitLocked/freezeRecoveryBaselineLocked`；`reconcile.go::reconcileLocked` | `TestRecoverRollsForwardAfterPatchSucceededBeforeProgressSave`：native CC Patch 成功后使紧接的池 Save 失败，自动 reconcile 实际完成三处目标与代际提交；`TestRecoverRejectsUnknownAfterClassification` 拒绝短锁前漂移且保留未知值 | 通过 |
| 10 / R03 DTO+UI 出口 | facade `TransactionKind/EnrollmentDrifted/RecoveryActions/EnrollmentID`；`ClaudeAccountsSection.vue` 漂移录入保存/取消；`claudeAccountsView.ts` 按白名单过滤，显式空白名单不授予动作 | `TestR03_DriftedEnrollmentRecoverableThroughFacade` 两种真实后端恢复出口；SSR `录入→取消漂移→刷新后 SSR 仍有保存/取消出口，而非 swap 三选一`；capture/取消的 API 参数断言 | 通过 |
| 11 / F03-B 测试有效性 | `internal/claudeaccounts/phase1_fix_test.go::TestSwapSecondHarvestReplacesJournalBefore` 在第 3 次 ReadItem 注入 B′ | 断言第一次 harvest 仍见 B、current2 已执行、最终源快照已收回 B′，并且写失败后 journal 按全 before 清除，排除未进入二次 harvest 的假通过 | 通过 |
| 12 / SSR 恒真断言 | `frontend/src/__tests__/components/provider/claudeAccountsFix.test.ts` 移除空字符串 slice 断言 | 断言真实 `ca-absent`、不存在 `ca-enroll`，并验证真实失败文案与保存入口 | 通过 |

补充：enrollment capture 仍在领域锁内比对 TxnID+revision；取消仍按锁内当前 enrollment 与 expectedRevision 精确匹配，不新增盲清路径。事务更换必推进 revision，陈旧取消不会作用于另一笔 journal。

## 实际验证与真实输出

所有项目命令 cwd 均为报告开头的 worktree。先运行针对性测试定位遗留测试/fixture 问题，再跑全量：最初因新标记约束使未设 markerPath 的测试 fixture blocked，已给测试临时目录 marker；新增测试初次编译使用了错误 Config API/返回类型，已按真实接口修正；remote restart 初次缺控制权，补齐既有 acquireTransactionControl；在途测试结束前等待所有 done，消除临时目录清理竞争。未通过弱化产品校验解决这些失败。

| 命令 | 退出状态 | 关键实际输出 |
|---|---:|---|
| `go vet ./...` | 0 | 无 Go vet 诊断；冷构建会输出既有 Security.framework deprecated-declarations 警告（没有 suppress） |
| `go test ./...`（最终再次执行） | 0 | `ok amagi-codebox 17.193s`；`ok amagi-codebox/internal/claudeaccounts 0.959s`；`ok amagi-codebox/internal/remote 44.398s`；其余全绿 |
| `go test ./... -count=1`（完整无缓存复跑） | 0 | root `12.629s`；claudeaccounts `1.466s`；remote `27.883s`；session `51.695s`；全部包通过 |
| `go test -race ./internal/claudeaccounts ./internal/remote` | 0 | `ok amagi-codebox/internal/claudeaccounts 3.983s`；`ok amagi-codebox/internal/remote 35.827s` |
| `npm --prefix frontend run test` | 0 | `Test Files 40 passed (40)`；`Tests 401 passed (401)`；`Duration 3.10s` |
| `npm --prefix frontend run build` | 0 | vue-tsc 与 Vite 成功；`✓ built in 760ms` |
| `go test -race . -run 'TestFailedMutationPublishes\|TestPublication\|TestAbsentDesktop\|TestAbsentRemote\|TestRecoveryRevision\|TestMarkerErrors\|TestOperationInflight' -count=10` | 0 | `ok amagi-codebox 6.137s`，十轮屏障/资源退出检查通过 |
| `git diff --check` | 0 | 无输出 |

完整未缓存 Go 结果节选（原始输出）：

```text
ok  amagi-codebox                            12.629s
ok  amagi-codebox/internal/claudeaccounts      1.466s
ok  amagi-codebox/internal/envcheck           24.871s
ok  amagi-codebox/internal/headroom            7.081s
ok  amagi-codebox/internal/remote             27.883s
ok  amagi-codebox/internal/session            51.695s
ok  amagi-codebox/internal/wslsupply            6.224s
```

原始本机会话日志：`/tmp/claude-fix-all-latest.log`、`/tmp/claude-fix-vet-latest.log`、`/tmp/claude-fix-all-final.log`、`/tmp/claude-fix-race-final.log`、`/tmp/claude-fix-frontend-test-final.log`、`/tmp/claude-fix-frontend-build-final.log`、`/tmp/claude-fix-barrier-race-final.log`。这些为临时执行证据，非随仓库持久交付；上表保留关键原文供复核。

### Wails DTO 再生

`wails` 在当前 PATH 不可达，且 `$HOME/go/bin/wails` 无可执行文件；未据此声称系统完全未安装。读取 vendored Wails `internal/app/app_bindings.go` 后，用其真实 `bindings` build tag 入口：

```sh
go build -tags bindings -o /tmp/codebox-bindings.uiEcMV/bindings .
# 在该临时目录放置最小 wails.json，以独立临时 HOME 执行 ./bindings
```

exit 0，生成器实际输出包含 `ClaudeAccountsPoolDTO.TransactionKind/EnrollmentDrifted/RecoveryActions`。从生成文件按类边界再生工作树中的 `ClaudeAccountsPoolDTO`；未手写新增 TS 字段。生成类与已有手写段的差异仅为旧 enrollmentId 注释，生成结果已去除此注释。全量生成还显示其他既有 stale bindings（config/piconfig/piplugin 等）变化，未扩大本轮范围覆盖它们，也未删除工作树 generated 目录。未启动桌面窗口、未运行 App.Startup、未访问真实账号数据。

## 未覆盖、风险与回滚

- 所有账号凭据、profile、native/PoolStore、PTY/进程均为合成 fixture；没有真实登录、Keychain 授权弹窗/ACL、网络 refresh/profile、Pi proper-lock 跨产品竞争或真实请求身份签收。上游发布门继续保留。
- SSR 使用真实 Vue 模板与 store、API 隔离桩；不是浏览器点击、Wails 事件桥或视觉审美验收。实际后端 enrollment→漂移取消→重建缓存→恢复出口由独立 Go facade 测试覆盖，不把两套隔离测试冒充同进程浏览器集成。
- F10 权限写失败测试以普通用户实际执行通过；该用例在 root 环境会明确 Skip 权限反例，其余标记错误测试仍执行。标记仅证明“曾初始化”，不包含秘密；标记故障时不宣布 managed，不依赖进程布尔缓存建立跨重启历史。
- 发布锁解决 App 内发布顺序；不声称它为外部原生客户端提供 CAS，不能阻止用户在 CodeBox 之外随后启动消费者。remote reservation 的范围为本次执行调用，已运行的无凭据 lease 消费者仍由既有 quiescence 扫描保守拦截。
- 未 commit：由 Leader 审核本轮完整工作区（含中断遗留变更）后决定统一提交。既有其他未跟踪目录不应纳入提交。
- 回滚必须按文件/块审查撤回本轮产品改动及新增测试，不执行全工作树 reset/restore；中断遗留内容不能当作无主改动删除。本轮新增根包测试为 `claude_accounts_publication_test.go`、`claude_accounts_barriers_test.go`、`claude_accounts_release_test.go`，继承新增 `internal/claudeaccounts/phase1_fix2_test.go`。报告可单独撤回。
- 绝不能用代码回退或旧 token 备份覆盖 native 登录来“回滚”事务；保留 journal，经显式恢复入口完成实际账号状态恢复。

---

# Phase 1 第四轮窄修（fix3）——F10 持久化重试 / R04 absent 指纹

## 结论与基线

两项阻断均已复现并修复；指定全量 Go、race、前端测试与构建全部 exit 0。没有扩展处理已通过的其余 finding，也没有改动单一发布点/发布序列锁或 coordinator 的指纹兼容规则。独立第四轮复核与真机发布门仍交 Leader 决定。

- 本轮源码与所有验证 cwd：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/claude-account-rotation`。
- 开始实查 HEAD：`cab85ae605b796c370551ba78fb1a5124cd5012b`，分支 `amagi/claude-account-rotation`；开始时 tracked 无修改，只有既有未跟踪报告目录与 `amagi-task/`。
- 必读上游（已完整读取，重点核实阻断项）：`/Users/run.mao/workpace/amagi-codebox/agent-outputs/2026-10-28-claude-account-rotation/phase1-diting-final-review.md`。
- 续接输入：本文上方第三轮报告，以及当前 `cab85ae` 的 marker/remote adapter/App 接线与既有屏障、生产事务/外部启动测试 fixtures。
- 本轮主上重新明确授权 **worktree 提交**，不再沿用上一轮“不要 commit”。本报告在提交前落盘，提交 hash 随最终返回；不 push、不合并、不暂存其他报告/复现脚本或 `amagi-task/`。
- 产物使用冻结声明路径追加本文件，未在主树另写 `phase1-fix3-report.md`，保留原报告历史。

## finding → 修复 → 锚点 → 测试

| finding | 根因与修复 | 代码锚点 | 正反对照与测试名 |
|---|---|---|---|
| F10 Major 残留 | 旧逻辑在 Stat regular 存在时直接返回 nil，绕过前次创建后的 Sync/Close 失败。现在已有文件也重新打开、file Sync、Close、父目录 Sync/Close；任何失败仍由唯一发布点发布 blocked。无本进程“已确认”布尔缓存。 | `claude_accounts_launch.go:105::writeClaudeAccountsMarker`、`:140::syncClaudeAccountsMarker`；`app.go::claudeAccountsMarkerSync`（nil 默认真实 File.Sync，隔离测试可注入 EIO） | `claude_accounts_marker_retry_test.go:11::TestMarkerPersistenceFailureRetriesUntilConfirmed/file-sync`、`/directory-sync`：创建成功→注入 Sync EIO→连续 3 次真实 Get/refresh blocked→重建 App 仍 blocked→取消 EIO 后完成文件/目录同步才 managed。既有 `TestMarkerErrorsFailClosedAcrossAppRebuild` 的创建前写失败/Stat 错误反例原样保留。 |
| R04 Major 新回归 | 旧 remote absent reservation 固定零指纹，与桌面 canonical 路径+generation=0 指纹不兼容。remote 改为通过注入函数取同源指纹；缺依赖或计算失败 fail closed，不回退零指纹。取得 admission 后依旧检查 absent，且保留整个执行窗口的 reservation。 | `internal/remote/remote_session_adapter.go:221::SetClaudeCredentialsFingerprint`、`::reserveCredentialPlanAbsence`；`app.go:752` 与 AbsenceCheck 同点接线到 `app.claudeCredentialsFingerprint`；既有屏障 fixture 同步接入 | `claude_accounts_mixed_launch_test.go:63::TestAbsentDesktopLeaseAllowsRemoteCreateAndRestart`（remote Claude/Pi 各一分支）；`:94::TestAbsentRemotePrepareAllowsDesktopLaunch`（create/restart 分支）；`internal/remote/credential_absence_test.go:12::TestCredentialAbsenceFingerprintErrorsFailClosed`（缺回调/指纹失败/取得后复核失败与清理）。 |

### F10 重试语义

标记按“曾存在”的文件存在性消费，内容无秘密、不承载状态。新文件仍独占创建，已有文件不截断、不删除；两种分支均须真实完成文件与父目录持久化确认。只要当轮确认失败，常规 refresh 就不能发布 managed。下轮不会把 Stat 存在当作上轮同步成功，也不会依赖内存中的失败状态；重启后的同一常规流程同样重试。

新增测试使用真实临时文件与真实 Sync，在成功创建后的文件 Sync 或目录 Sync 调用处额外返回 `syscall.EIO`。它验证的是系统调用错误传播和后续重试，不冒充真实磁盘故障。断言每次失败刷新确实重新尝试同步，并检查恢复后的每次放行都执行了文件与目录同步。原来的只读目录反例不删不弱化。

### R04 混合路径与资源退出

- 桌面方向：实际 `App.LaunchSession` 成功通过 fake guarded launcher，并将 absent claim 提升为真实 coordinator external-run lease；保持该 lease 存活，实际 remote create 与 restart 均通过生产 adapter/executor，fixture PTY start effect 合计 2 次。不仅比较 helper 返回值。
- 反向：真实 remote create/restart 在 Executor.Prepare 处暂停，尚未有该次新进程；先断言首次 capture 被真实 Service→生产 Gate 拒绝，再让实际桌面 LaunchSession 成功并提升 lease，继续断言 mutation 未被放开。
- 此混合 fixture 复用外部启动测试的 context（不是 Wails 窗口）。其中 capture 采用同步领域入口以避免向未启动的 Wails runtime 发事件；原有异步 facade 的 `TestAbsentDesktopLaunchReservationBlocksFirstCapture` 与 `TestAbsentRemotePrepareReservationBlocksFirstCapture` 全部保留并复跑。
- 假 Prepare 按既有屏障约定返回注入失败，必须经 remote 入口向外传播；defer 放开屏障并等待 goroutine 退出。桌面 fixture 在 cleanup 实际 StopSession，等待运行 lease 清理，不遗留真实 CLI 或测试后台操作。
- managed 计划的新鲜度校验和 coordinator 不同指纹拒绝规则没有修改；`TestCredentialsKind_StaleGenerationAdmissionRejected` 等既有用例通过。修复只把同一 absent 状态的参数接回同一来源，未放宽 managed 指纹约束。

## 先红后绿与实际验证

在保留旧的“exists 早返回/remote 零指纹”逻辑、仅装好测试注入缝与回调声明后，执行：

```sh
go test . -run 'TestMarkerPersistenceFailureRetriesUntilConfirmed|TestAbsentDesktopLeaseAllowsRemoteCreateAndRestart|TestAbsentRemotePrepareAllowsDesktopLaunch' -count=1
```

实际 exit 1：两个 marker 分支第二次刷新都得到 `State:enabled`；两个桌面 lease→remote 分支都得到 `service unavailable`；两个 remote Prepare→桌面分支都得到 `SESSIONS_ACTIVE ... shared service is in use by active sessions`。日志 `/tmp/claude-fix3-before2.log`。首次构造混合 fixture 时曾发现 HOME/USERPROFILE 被原 harness 隔离到不同目录而合法走独立根释放分支、异步事件试图调用未启动 Wails runtime；按真实 fixture 契约修正为同一临时 canonical home、同步 Service busy 检查后，再得到上述有效的产品缺陷复现。没有改产品校验来迁就 fixture。

应用两处修复后：

| 实际命令 | exit | 关键真实输出 |
|---|---:|---|
| `go test . -run 'TestMarkerPersistenceFailureRetriesUntilConfirmed\|TestAbsentDesktopLeaseAllowsRemoteCreateAndRestart\|TestAbsentRemotePrepareAllowsDesktopLaunch\|TestMarkerErrors\|TestAbsentRemotePrepareReservationBlocksFirstCapture\|TestAbsentDesktopLaunchReservationBlocksFirstCapture' -count=1` | 0 | `ok amagi-codebox 0.735s` |
| `go test ./internal/remote -run 'TestCredentialAbsenceFingerprintErrorsFailClosed\|TestCredentialsKind_' -count=1` | 0 | `ok amagi-codebox/internal/remote 1.498s` |
| `go vet ./...` | 0 | 无输出，无 Go vet 诊断。冷构建仍可能输出既有 Keychain API 弃用警告，本轮未 suppress。 |
| `go test ./...` | 0 | root `10.257s`，envcheck `22.348s`，remote `26.877s`，其余全绿（未改领域包可命中缓存）。 |
| `go test -race ./internal/claudeaccounts ./internal/remote` | 0 | `ok .../claudeaccounts (cached)`；`ok .../remote 34.119s`。 |
| 上述根包 targeted 命令添加 `-race`、改为 `-count=10` | 0 | `ok amagi-codebox 5.588s`；新两项和既有 marker/首次 capture 屏障十轮通过。 |
| `npm --prefix frontend run test` | 0 | `Test Files 40 passed (40)`；`Tests 401 passed (401)`；`Duration 1.43s`。 |
| `npm --prefix frontend run build` | 0 | vue-tsc 与 Vite 成功；`✓ built in 547ms`。 |
| `git diff --check` | 0 | 无输出。 |

临时完整输出：`/tmp/claude-fix3-before2.log`、`/tmp/claude-fix3-after.log`、`/tmp/claude-fix3-vet.log`、`/tmp/claude-fix3-all.log`、`/tmp/claude-fix3-race.log`、`/tmp/claude-fix3-root-race.log`、`/tmp/claude-fix3-frontend-test.log`、`/tmp/claude-fix3-frontend-build.log`。这些不是随仓库持久化的交付，关键结果已摘录于本报告。

## 未覆盖、风险与回滚

- 改动仅涉及 `app.go`、`claude_accounts_launch.go`、`internal/remote/remote_session_adapter.go`、旧 remote 屏障 fixture 一行接线，以及新增 `claude_accounts_marker_retry_test.go`、`claude_accounts_mixed_launch_test.go`、`internal/remote/credential_absence_test.go` 和本报告。
- 未触碰真实 Keychain/native 登录、实际 CLI/远端 UI、网络 identity/refresh。所有进程/凭据均为明确 fixture；真实磁盘 EIO、浏览器/Wails 事件桥与视觉未验证。上游其他发布门继续保留。
- 确认 marker 的文件/目录 Sync 每轮执行，不用成功缓存换取性能；这是本项 fail-closed 所需行为。没有额外声称性能优化或真实硬件持久性实验。
- 新 setter 的所有生产消费方和原相关测试 fixture 已同步接线；绑定/DTO/前端契约未改，无需再生 Wails 文件。
- 回滚应由 Leader 审核本轮提交后以正常反向提交撤回上述精确改动，不 reset 工作树、覆盖其他现场或动无关未跟踪文件。代码回退不能代替未结账号事务恢复，更不能回灌旧 token。
