# Phase 1 第三轮终审

**FIX：发布序列化的结构性要求已满足，但 F10 尚有持久化错误后的重试放行窗口，并新引入 R04（absent 桌面/remote 指纹不一致）。Phase 1 Gate 暂不通过。**

## 基线与输入

- 源码根（下文代码路径均相对此根）：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/claude-account-rotation/`。
- 实查 HEAD=`cab85ae605b796c370551ba78fb1a5124cd5012b`；增量 `c15bd6c..cab85ae`，21 文件，+1334/−148。提供的 diff 与实际 Git diff SHA-256 均为 `40b0b1fdde0a5d4181f70f1ff7e2ed4c23b9ff661562718914b3505378bb1f6c`。
- 必读上游均已完整读取：本目录 `phase1-diting-rereview.md`、`phase1-fix2.diff`、`phase1-fix2-report.md`。修复报告保留的 HEAD=c15bd6c/未提交描述是其撰写时现场，不替代本轮实查基准。
- 本轮只读审核增量与直接消费链；未修改源码、未写项目测试、未提交。独立复现脚本与编译 overlay 仅存本报告目录。

## 8+2 项逐条终判

| 项目 | 终判、锚点与理由 |
|---|---|
| F01 absent reservation | **关闭原竞态，修复引入 R04**：`claude_accounts_launch.go:261-296`；`internal/remote/remote_session_adapter.go:225-243,648-656,1153-1160`。桌面 absent 取得共享 admission，remote 在 Prepare 前取得并复核 absent、defer 释放；与 mutation 在同一 coordinator mutex 内互斥。真实 LaunchSession/remote create/restart 屏障有效。 |
| F05 Pi 组织配对 | **关闭**：`internal/claudeaccounts/harvest.go:185-240`。目标记录/CC/Pi 的 UUID 与已知组织配对，本地 identity UUID 及已有组织字段复核，未知组织显式拒绝；真实 Swap 的不同/未知组织反例有效。 |
| F06 恢复命令锁内 revision | **关闭**：`internal/claudeaccounts/swap.go:30-50,78-83`、`reconcile.go:332-345`；facade `:652,713-720`。guard 在 withLocks 内、任何写入前执行，restore-source 另带 TxnID；真实 facade 读后 Rename 的两分支屏障验证拒绝。 |
| F10 标记 fail-closed | **部分关闭，Major 残留**：`claude_accounts_launch.go:105-129,692-702`。ENOENT/其他 Stat 错误与创建失败已区分，但创建后 Write/Sync/Close/目录 Sync 失败留下的 regular marker 会在下一次 refresh 被当成功，详见 F10。 |
| F15 在途上限 | **关闭**：`claude_accounts_facade.go:174-189,275-281`。先幂等命中再限制在途 16 项，超限 POOL_LOCKED，不再新增 goroutine；真实 runner 阻塞与溢出/幂等对照有效。 |
| R01 发布序列化 | **关闭**：`claude_accounts_launch.go:365-393,678-711`。唯一缓存赋值区间在 refresh 中且发布锁跨 Load→标记→缓存写入；mutation 等到发布完成才释放 admission，结构上排除指定倒灌时序，详见下一节。 |
| R02 恢复基线锁内重冻结 | **关闭**：`internal/claudeaccounts/reconcile.go:169`、`swap.go:219-258,303-326`。分类快照带入短锁，复核分类后 revisions，逐投影重分类并持久化当前基线，再补 before；成功 Patch→进度 Save 失败反例实际 roll-forward，短锁前 unknown 拒绝。 |
| R03 漂移 enrollment UI 出路 | **关闭原无出口缺陷**：`claude_accounts_facade.go:392-416`；`frontend/src/components/provider/ClaudeAccountsSection.vue:104-137,670-674`。安全 DTO 保留 ID/事务类型/动作白名单，真实模板接入保存与恢复源后取消；后端两条出口与 SSR/API 参数断言配对。未冒充浏览器集成签收。 |
| 测试 F03-B | **关闭证据缺口**：`internal/claudeaccounts/phase1_fix_test.go:91-143`。改为第 3 次 ReadItem 注入；第一 harvest 仍见 B，最终源快照必须是 B′且 journal 清除；结合 swap 二次 harvest 分支可确认，不是只靠 reads≥4 证明分支。 |
| 测试 SSR 恒真断言 | **关闭**：`frontend/src/__tests__/components/provider/claudeAccountsFix.test.ts:110-116`。空串 slice 改为真实 ca-absent、非 ca-enroll 与保存/失败文案断言，编译并渲染真实 .vue 模板。 |

## 架构硬约束：结构性验证通过

生产 `pool/loadErr/recovered` 的缓存赋值只在 `refreshClaudeAccountsCache` 的 `:705-711`；`publishClaudeAccountsAuthoritativeState` 是 mutation release 的包装入口，最终也进入这一赋值点。**函数名不是唯一性证明，生产赋值位置及互斥范围才是。** 测试用 `setClaudeAccountsCache` 不属于运行时发布者。

1. 普通 refresh 在 `:681` 取得 `claudeAccountsPublishMu`，Load 返回后一直持有至缓存发布完成。
2. mutation 可在此期间落 journal，但 release 在 `:377` 等待同一发布序列，`ReleaseMutationAdmission` 在 `:378`，等待时仍禁止 launch reservation。
3. 所以旧 refresh 只能先发布，mutation 随后重新 Load 并发布 blocked；不存在“新 blocked 已发布→旧 refresh 后发布”的合法互斥执行序列。即使 generation 不变也成立，不靠 generation 比较。
4. 生产始终装配同源 PoolStore，发布不反取 Service mutex；测试/未装配 fallback 不作为生产无死锁证明。

屏障不是 helper 替身：`TestPublicationOldLoadCannotOverwriteNewBlocked` 包装发布路径实际 PoolStore.Load，先形成旧快照再停住，真实 BeginEnrollment 落 journal，并检查 admission 仍未释放；`TestFailedMutationPublishesBlockedBeforeFacadeRefresh` 走真实 Switch，注入 Pi Patch 失败留 journal，再暂停 runner refresh，实际 LaunchSession 被拒。absent 的 remote 测试停在实际 Executor.Prepare，恢复测试停在 facade 工作链、用实际 Rename 推进 revision。它们分别覆盖目标窗口，不据此声称跨产品真机并发已验证。

## 阻断项

### F10｜Major｜创建后的持久化失败只阻塞一次，下一次刷新绕过同步

- **定位**：`claude_accounts_launch.go:105-129`（尤其 `exists` 直接返回，以及创建后的错误返回）；`:692-702`（每次刷新以该返回值决定是否 blocked）。
- **触发条件**：首次创建 regular marker 后，文件 Sync 或目录 Sync 返回 EIO 等持久化错误；文件在当前文件系统视图中仍可 Stat。下一次 Get/operation refresh/启动恢复再次调用发布路径。
- **实际影响**：第一次返回错误并 blocked，但第二次 `claudeAccountsMarkerStatus` 仅以“regular file 存在”返回 true，`writeClaudeAccountsMarker` 未重试文件/目录同步即返回 nil，于是池可再次发布 managed。持久化成功从未被证实；若尚未持久化的目录项在崩溃后消失，后续缺池仍可退化 absent。这仍违背本轮承诺“同步错误不得宣布 managed/存在性必须持久化”。不声称每次 Sync 错误都会导致目录项丢失。
- **证据性质**：静态实际重试路径证实，未动态注入文件系统 Sync 错误。现有 `TestMarkerErrorsFailClosedAcrossAppRebuild` 的 write-error 是只读目录，失败发生于创建之前，无法检验已留下 regular marker 的分支。
- **最小修复方向**：已有文件也必须完成可重试的持久化确认（包括目录同步），不能以 Stat 存在清除上一轮持久化失败；或采用明确的持久安装协议，让未完成的标记不会被视为已确认。由实施者补创建后同步失败→连续 refresh/重建的正反对照，不删弱原 fail-closed 校验。

### R04｜Major｜新增 absent reservation 在桌面与 remote 使用不同指纹，正常混合启动被拒

- **定位**：`internal/remote/remote_session_adapter.go:232` 使用 `[32]byte{}`；`claude_accounts_launch.go:271-275` 使用 canonical 路径+generation 的 SHA-256 指纹；`app.go:2754-2759,2947-2958`、`control_wiring.go:171-178` 将 absent 桌面 claim 也提升为运行 lease；`internal/remote/shared_coordinator.go:205-217` 拒绝不同指纹的 lease/admission 共存。
- **触发条件**：macOS 服务已装配但账号池从未启用，canonical 桌面 Claude/Pi 会话已成功运行；随后 remote 创建/重启 Claude/Pi。或者 remote absent Prepare 在途时，桌面同时启动。
- **实际影响**：桌面 absent lease 使用非零 canonical 指纹，remote absent reservation 使用全零指纹。同一 absent 状态被当成两份不兼容配置，remote 在 Prepare 前被拒；反向在途重叠也拒绝桌面。前一场景不是仅持续数毫秒：桌面 lease 保留到进程退出，期间 remote 的正常启动持续失败。此前 absent 桌面没有该凭据 lease，此为本批 F01 修复新引入的兼容回归，不是本来应禁止的 mutation/launch 互斥。
- **证据**：完整入口连线为静态证实；报告目录 `phase1-final-reservation-repro.go` 动态调用本 worktree 的真实 coordinator，以合成 canonical generation=0 指纹创建并提升桌面 external-run lease，再尝试 remote 的零指纹，得到 `shared service is in use by active sessions`；相同指纹对照返回 `<nil>`。反向 remote 零指纹 pending→桌面 canonical 同样拒绝。脚本仅复现真实仲裁原语及参数，不冒充实际 CLI/Wails 端到端启动。
- **最小修复方向**：统一 absent reservation 的指纹/兼容语义，并保持 mutation 互斥与 managed 指纹约束；不可简单移除 reservation。由实施者补桌面成功启动→remote create/restart、remote Prepare 在途→桌面启动的混合路径对照，同时保留首次 capture 必须 busy 的屏障。

## ACHIEVEMENT: 验收→证据→状态

所有命令 cwd 均为本报告源码根；本轮仅 L2 抽检，没有把上游全量绿灯记为自行运行。

- 基线/输入一致→`git rev-parse HEAD`、双 SHA-256、完整增量读取→通过。
- 原 8 项与测试 2 项→上述逐项代码路径及以下针对性执行→F10 部分关闭，其余原问题关闭；新增 R04 阻断。
- 根包屏障→`go test . -run 'TestFailedMutationPublishes|TestPublication|TestAbsentDesktop|TestAbsentRemote|TestRecoveryRevision|TestMarkerErrors|TestOperationInflight|TestR03' -count=1`→exit 0，`ok amagi-codebox 0.670s`。
- 领域修复→`go test ./internal/claudeaccounts -run 'TestSwapSecondHarvest|TestSwapRejectsPiGrantOrganization|TestSwapRejectsPartialOrganization|TestSwapRejectsUnknownOrganization|TestRecoverRollsForwardAfterPatch|TestRecoverRejectsUnknownAfter|TestRecoverTransactionGuard|TestSwapGuard|TestVerifyOrganizationTriple' -count=1`→exit 0，`ok .../claudeaccounts 0.561s`。
- SSR/恢复参数→`npm --prefix frontend run test -- claudeAccountsFix`→exit 0，1 文件/11 tests passed。
- R04 复现→先直接 `go run` 报告侧脚本，exit 1：`use of internal package ... not allowed`（Go internal 源文件路径限制，不是反例失败）；改用报告侧 `phase1-final-reservation-overlay.json` 将脚本虚拟映射至 worktree 内已有报告目录，`go run -overlay=<该 JSON 绝对路径> <源码根>/agent-outputs/2026-10-28-claude-account-rotation/diting_reservation_repro.go`→exit 0，输出上述两个拒绝与同指纹成功对照。overlay 不在项目树落文件、不替换生产代码。
- 现场保护→`git diff --check` exit 0；测试前后 worktree tracked 均无改动，仍仅既有未跟踪报告目录与 `amagi-task/`→通过。

## 覆盖、盲区与回滚

- 已覆盖：21 文件完整增量、状态单写/发布互斥、桌面与 remote admission/lease 兼容、恢复命令与短锁基线、组织验证、DTO/store/模板动作、在途限额、屏障与断言有效性。无关既有问题未作为回归引入本报告。
- 未自行重跑全量 vet/go test/race×10/401 前端测试/build；Leader 已跑结果仅为上游信息。本轮针对性命令均通过，但未覆盖新反例。
- 未访问真实凭据、Keychain/ACL/授权弹窗、网络 profile/refresh、真实 Claude/Pi/远端客户端；真实浏览器交互/Wails 桥及视觉审美未签收。现有发布门继续保留。
- F10 为静态错误路径结论；R04 仲裁复现已执行，实际 UI/进程混合启动未执行。未编写正式项目测试或修复 patch。
- 本轮产物为此报告、同目录 `phase1-final-reservation-repro.go` 与 `phase1-final-reservation-overlay.json`。回滚只需由 Leader 撤回这些报告侧文件；源码无需回滚，不触碰其他未跟踪产物。

VERDICT: FIX
