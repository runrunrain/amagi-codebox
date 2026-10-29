# Phase 1 修复批次报告：diting full 审核 15 findings 全量修复

## 概要

按 `phase1-diting-review.md` 的最小修复建议逐条修复 F01-F15（12 Major + 3 Minor），同报修复四项测试有效性问题，并按各 finding 点名补齐反例测试。全部 15 条均实施修复，无「不应修/需裁定」条目；2 处实施范围说明见 §映射表备注。工作树改动未提交（按 [amagi worktree] 注入段，与任务书正文「分支提交」冲突时按较新注入段执行，1a/1b/1c 同例）。

- 目标 worktree：`.amagi-worktrees/claude-account-rotation`（分支 amagi/claude-account-rotation，HEAD 3acbc90 ✓ 与任务书一致）
- 变更规模：31 个 tracked 文件修改 + 3 个新测试文件（+816/-115），CHANGELOG 追加 1 条。
- 上游输入：`phase1-diting-review.md`（15 findings + 测试有效性节）、`design.md`（§1.3/§3.1/§3.3/§4/§6.3/§7.2 契约收敛）。

## F01-F15 逐条对照表

| Finding | 修复方式 | 新锚点（worktree 相对路径） | 新增/修正测试 |
|---|---|---|---|
| F01 Major 门禁交接窗口 | ①mutation gate 的 release 在释放 admission **之前**直接从 PoolStore 发布权威 ready/blocked/generation（不经 Service 串行锁，避免死锁）；②`beginClaudeCredentialsClaim` 取得 admission 后重校权威 policy（失败事务代际不变也拦）；③`recheckClaudeCredentialsClaim` 复核加 blocked 态；④remote create/restart 对**无凭据 admission** 的 claude/pi 计划在执行边界校验权威状态仍为 absent（新接缝 `SetClaudeCredentialsAbsenceCheck`） | `claude_accounts_launch.go:245-332,362-390`（publish/claim/recheck/absence）；`claude_accounts_facade.go`（gate 装配）；`internal/remote/remote_session_adapter.go:617,1113` | `TestF01_LaunchBlockedImmediatelyAfterFailedMutation`、`TestF01_AbsenceCheckAndRecheckCoverBlocked`（根）、`TestCredentialPlanAbsenceBoundary`（internal/remote） |
| F02 Major absent 前已运行会话被豁免 | 观察器归因集合从「所有 CodeBox 会话 PID」改为「持有 SharedServiceClaudeCredentials lease 的会话」（查 `App.sharedLeases` 按 kind 过滤）；absent 时启动的会话不持 lease → 计入 external → EXTERNAL_SESSION_UNCERTAIN | `claude_accounts_launch.go:526-556` | `TestF02_QuiescenceAttributesOnlyCredentialLeaseHolders`（含真实提升链路 lease 归因）、`TestF02_FirstCaptureFromAbsentWithRunningSessionBlocked`（absent 真实启动状态→capture 拒绝，非预置 managed cache） |
| F03 Major revision 终复核缺失/before 分裂 | ①`applyAndCommitLocked` 在 Pi 短提交锁内重新 readStable 并与 `journal.ExpectedRevisions` 逐处比较，漂移 → NEEDS_RECONCILE 停写（交由 reconcile 按 unknown→needs-reconcile 保全当前值）；②Swap 二次 harvest 返回值同步替换 `sourceOwned/sourceRevs`，journal before/expected 同源；重 harvest 后仍在变化 → fail closed | `internal/claudeaccounts/swap.go:117-146（B）,193-203（A）` | `TestSwapStopsOnNativeDriftBeforeCommit`、`TestSwapSecondHarvestReplacesJournalBefore`（internal/claudeaccounts，keychain onRead 精确屏障注入） |
| F04 Major 取消清恢复标记 | `CancelEnrollment` 走 mutation gate + 静止门 + readStable 分类：与活动源一致才清 journal；漂移/不可验证 → 保留 enrollment 并标记 `DriftedReason` → needs-reconcile（DTO 状态同步），返回稳定 NEEDS_RECONCILE；人工恢复=完成录入保存当前配对，或原生重登原账号后再取消 | `internal/claudeaccounts/service.go:438-505`；`types.go`（EnrollmentJournal.DriftedReason）；`claude_accounts_facade.go`（drifted → needs-reconcile） | **修正既有测试断言** `TestFacade_BeginEnrollmentAndCaptureFlow`（漂移取消→错误+journal 保留+blocked；重登后取消成功） |
| F05 Major 目标只查旧证据 | 新增 `verifyTargetGrantsLocked`：`ensureTargetReadyLocked` 末尾（刷新存池后、原生写入前）用目标当前两份 access token 现场查询身份，与目标身份/组织及双端配对比较；失败/不一致 → IDENTITY_UNVERIFIED/IDENTITY_MISMATCH，零原生写 | `internal/claudeaccounts/harvest.go:128-215` | **修正既有测试** `TestSwapRefreshesExpiredStandbyPiGrant`（补刷新后 token 身份映射）；新增 `TestSwapRejectsRefreshedTargetWithoutIdentity`、`TestSwapRejectsRevokedTargetGrant`（刷新后不可验证/未过期已换发均拒） |
| F06 Major 恢复未完成返回 done | ①complete-target/restore-source work 消费 outcome：roll-forward-pending/needs-reconcile/blocked → `claudeOpMarkBlocked`（终态 blocked，runner 哨兵不改写）；done 前复核 journal 已清；②两动作锁内校验 expectedRevision（陈旧恢复命令 REVISION_CONFLICT）；③Swap 的 no-op 判定移到 reconcileLocked **之后**（全 before 先清 journal 再 no-op） | `claude_accounts_facade.go:296-306,490-585`；`internal/claudeaccounts/swap.go:57-78` | `TestF06_CompleteTargetBlockedWhenLeaseHeld`、`TestF06_RestoreSourceAllBeforeClearsJournal`、`TestF06_ReconcileActionsRejectStaleRevision`（根）、`TestSwapNoOpClearsAllBeforeJournal`（领域） |
| F07 Major remote/外部路径漏身份边界 | ①`buildClaudePlan`：受管+OAuth → `managedClaudeIdentityBoundarySpec(spec)`（与桌面 embedded 同一函数）；②`buildPiLoginPlan`：admission 检查提前到 env 构建前，`piLoginEnvOverrides`/`piLoginReAnchorSpec` 传入同一键集；③`LaunchGuarded` 新增 `managedEnvStrip []string` 参数（外部终端直接 exec，最终 env 剥键；非受管 nil 零变化；接口/两处调用点同步） | `launch_planner.go:350-360`；`pi_login_launch.go:366-377,428-433`；`internal/launcher/service.go:283-304`；`app.go:2837-2848` | `TestF07_ClaudePlanStripsManagedIdentityEnv`（真实 BuildPlan，含 absent 对照零变化）、`TestF07_PiLoginBoundaryHelpersCarryManagedKeys`、`TestF07_ExternalLaunchStripsManagedIdentityEnv` |
| F08 Major 异步失败被隐藏 | operation 进行/失败横幅移出 managed/error `<template v-else>` 至各状态共用区域（absent/enrolling/needs-reconcile 均渲染）；vitest.config 接入**既有** devDependency `@vitejs/plugin-vue`，用 `vue/server-renderer` SSR 渲染真实组件验证模板分支（零新增依赖、无 DOM 环境库） | `ClaudeAccountsSection.vue:241-245`；`frontend/vitest.config.ts` | `claudeAccountsFix.test.ts`「F08」4 例（真实模板渲染：absent/enrolling/needs-reconcile 失败横幅 + running 反馈） |
| F09 Major enrollment 无法续接 | ①池 DTO 新增 `enrollmentId`（持久化 TxnID，credential-free；drifted 不返回）；②store refresh 凭 `pool.enrollmentId` 重建向导（后端不报时清本地）；wailsjs models 同步（按生成器格式手写，待 wails CLI 再生收敛） | `claude_accounts_facade.go:95-108,325-337`；`stores/claudeAccounts.ts:131-143`；`wailsjs/go/models.ts` | `TestF09_PoolDTOCarriesEnrollmentId`、`TestF09_EnrollmentSurvivesBackendRebuild`（begin→重建→续接保存成功，根）；store 3 例（续接/清理/步进保持） |
| F10 Major 池删除当未启用 | 不含秘密的「曾初始化」标记 `~/.amagi-codebox/claude-accounts.pool-initialized`（`DefaultPoolMarkerPath`，测试可注入）；池存在时幂等落盘；标记存在+池消失 → loadErr=POOL_MISSING → blocked；facade 首次 capture 在该态拒绝（不重建空池）；从未创建（无标记）→ absent 不变 | `internal/claudeaccounts/lock.go:19-27`；`claude_accounts_launch.go:78-125,669-696`；`claude_accounts_facade.go`（capture 拒绝）；`app.go`（字段） | `TestF10_PoolMissingAfterExternalDeletionFailsClosed`（含 never-created 对照） |
| F11 Major CC 原生项缺项 Add 重建 | C 层 `amagi_cc_update` 的 errSecItemNotFound 分支删除 Add fallback，缺项原样返回；Go 层 UpdateItem 映射为稳定 NEEDS_RECONCILE（「禁止删后重建」）；池 envelope 首建（amagi_ca_set_pool）与原生凭据更新分离 | `internal/claudeaccounts/nativecc_darwin.go:66-84,165-168` | 生产 C 分支无法在单测触达（真实 Keychain 禁触，fake 已是缺项报错语义——本次使生产行为与 fake 对齐）；以代码审读+编译验证，标注为发布门真机验证项 |
| F12 Major 漏 pi-rpc | 匹配表补 `pi-rpc`/`pi-rpc.exe`（官方 rpc-entry.js 设置 process.title=APP_NAME+'-rpc'）；新增参数级路径片段 `pi-coding-agent`（node 直跑官方包入口也命中，宁可误报） | `claude_accounts_launch.go:425-462` | `TestF12_ObserverMatchesPiRpcAndPackagePaths`（标题/包路径/无关进程 ping 反例 + 端到端 uncertain） |
| F13 Minor 刷新重建丢字段 | 新增 `MergePiAnthropicRefresh`：既有 anthropic JSON 字段级 sjson 更新 type/access/refresh/expires，保留未知字段；非法既有条目拒绝；空条目退回全新构造；`applyRefreshResultLocked` 改用合并 | `internal/claudeaccounts/oauth.go:127-166`；`harvest.go`（applyRefreshResultLocked） | `TestMergePiAnthropicRefreshPreservesUnknownFields`、`TestRefreshStandbyPiKeepsUnknownFieldsEndToEnd`（含 tier_hint 保留） |
| F14 Minor RemoveAccount 忽略 revision | 锁内、删除前校验 `pool.Revision != expectedRevision` → REVISION_CONFLICT | `internal/claudeaccounts/harvest.go:404-410` | `TestRemoveAccountRejectsStaleRevision`（stale 拒绝零删除 + 正确 revision 成功） |
| F15 Minor 淘汰删运行中 | `claudeOpBegin` 淘汰循环改为：从队首扫描**已完成** victim（done 已关闭）才删除，运行中跳过（不放回队尾防死循环）；全部在途 → 本轮不淘汰（在途数有界）；清理 victim 的 request 索引 | `claude_accounts_facade.go:167-206` | `TestF15_CacheEvictionKeepsRunningOperations`（运行中最旧项幸存 + 查询/幂等仍命中） |

## 四项测试有效性问题（同报修复）

1. **F04 既有测试锁定错误行为** → `TestFacade_BeginEnrollmentAndCaptureFlow` 重写：漂移取消断言 NEEDS_RECONCILE 错误 + journal 保留（DriftedReason）+ policy blocked + DTO needs-reconcile；重登原账号后再取消成功 + managed。
2. **F05 expired-target 测试无身份映射仍成功** → `TestSwapRefreshesExpiredStandbyPiGrant` 补 `pi-a-fresh` 身份映射（身份门存在的正向证据）；负向见 `TestSwapRejectsRefreshedTargetWithoutIdentity`。
3. **TestCrossProcessLockConcurrentSingleWinner 只断言 total≥1** → 重写为真实双持锁互斥断言：持锁期间记录并发活跃数，`maxActive > 1` 即失败；winners==0 也失败。
4. **TestReadStableRejectsDirectory 实测 missing** → 先 `os.Mkdir` 真实目录 + Stat 前置校验，再断言拒绝。

## 实施范围说明（非静默跳过，均已在映射表标注）

- **F11**：生产 C 分支（SecKeychain）无法在不触真实 Keychain 的前提下单测（design §3.3「绝不实例化真实 Keychain backend」）；修复为删除 Add fallback + Go 层稳定错误码，与 fake 语义对齐；真机验证属 §10.2 发布门。
- **F08**：仓库规范禁新增 DOM 测试库 → 采用 vue 自带 `server-renderer` SSR 渲染真实组件（执行真实模板分支与 computed），vitest 接入的是**既有** devDependency `@vitejs/plugin-vue`（配置原注释即预留此演进）；未做浏览器点击交互（属 §10.4 真机交互验收）。
- **wailsjs**：`ClaudeAccountsPoolDTO.enrollmentId` 按生成器格式手写（wails CLI 不可得，与 1b/1c 同已披露偏差）；下次 `wails dev/build` 再生即收敛。

## 全量验证（真实输出摘要，cwd=worktree）

| 命令 | 结果 |
|---|---|
| `go vet ./...` | exit 0（仅 C 层 SecKeychain 系统级弃用警告，与 internal/secrets 基线一致） |
| `go test ./... -count=1` | exit 0，**48 包全 ok、0 FAIL**（含 claudeaccounts 70 用例、根包、remote 27s） |
| `go test -race ./internal/claudeaccounts ./internal/remote -count=1` | exit 0，ok（1.9s / 32.5s） |
| `npm --prefix frontend run test` | exit 0，**40 文件 / 397 用例全绿**（新增 7：SSR 组件 4 + store 3；修正既有 1） |
| `npm --prefix frontend run build` | exit 0（vue-tsc 零错误，✓ built in 397ms） |

## 未覆盖项与风险

- 真机发布门沿袭 1a/1b/1c：profile 响应契约、CC 刷新协议常量、Keychain locator/授权弹窗、pi-black 请求身份、Pi proper-lock 与安装版本竞争——均未因本批修复消除。
- F01 的权威发布依赖「mutation 结束时池已落盘」的既有不变量（Service 单线程串行锁 + flock）；外部进程直接改写 Keychain 池项仍超出 CodeBox 可观测面（F10 标记只能区分存在性，不能防并发外部写）。
- F02 归因依赖 App.sharedLeases 注册链（embedded/external/remote 转移路径均经 `rememberSharedLease`/transfer 提交）；若未来新增启动入口绕过该链，归因会保守多拦（方向安全）。
- blocked operation 在前端 store 轮询语义下视为仍在等待（继续轮询至 150s 上限后静默停止），UI 同步显示 blocked 横幅；不自动重放。
- 回滚方式：本批全部为工作区未提交改动，`git checkout -- <files>`（按上表文件清单）即可整体回退；新增文件 3 个测试文件 + vitest.config.ts 修改为配置层。

## 报告位置

- 主树（任务书正文路径）：`/Users/run.mao/workpace/amagi-codebox/agent-outputs/2026-10-28-claude-account-rotation/phase1-fix-report.md`（本文件副本）
- worktree（产物落位机器声明路径）：`.amagi-worktrees/claude-account-rotation/agent-outputs/2026-10-28-claude-account-rotation/phase1-fix-report.md`
