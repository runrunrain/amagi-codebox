# 终验报告：Desktop Embedded Sessions / Remote Real Sessions / 生命周期一致性

- 角色：wukong（Tester）—— 只测试/诊断，**未修改任何源码/测试/配置**，未 commit。
- 仓库：`/Users/maorun/maorun-workpace/amagi-codebox`
- 环境：`go1.26.1 darwin/arm64`（本地，CI 锁 1.25.0，按 C-001 不强制降级）/ Node `v24.18.0` / npm `11.16.0` / @playwright/test `1.58.2` / chromium 已装。
- 隔离：所有 go test 置 `HOME/USERPROFILE=$ISO/home`、`XDG_CONFIG_HOME=$ISO/xdg`、`TMPDIR=$ISO/tmp`（ISO_ROOT=`/tmp/wk_iso.tn75ad`），**真实 `~/.pi ~/.omp` 未被触碰**（`~/.pi/agent` mtime 无变化），secrets 测试用临时 configDir 文件存储，不调真实 Keychain。

---

## 一、验收目标（用户问题闭环判定）

1. **desktop embedded sessions 可被 v1 lobby 列出** —— 验证 desktop 与 v1 共享同一会话 ID、external 不误曝光。
2. **remote create 不是假会话** —— 真实凭据/配置/argv/启动落盘后再发布（先 ready/bootstrap 再 publish，fail-closed）。
3. **restart/stop/remove/attach 生命周期一致** —— 同 ID、共享 authority、边界 commit 顺序、remove GC、attach 抢占。

---

## 二、命令清单与退出码

| # | 命令 | 退出码 | 结果 |
|---|------|--------|------|
| 1 | `go vet ./...` | **0** | 无告警 |
| 2 | `go test -exec=/usr/bin/true ./... -run '^$'`（全包编译） | **0** | 全部 `ok`/`?` |
| 3 | 隔离 `go test ./... -count=1 -timeout=300s` | **0** | 32 包全 `ok`，无 FAIL/panic |
| 4 | `go test -race . ./internal/launchplan ./internal/processcap ./internal/pty ./internal/remote ./internal/session -count=1` | **0** | 6 包全 `ok`，**无 DATA RACE** |
| 5 | 生产链定向（见第三节） | **0** | 全 PASS |
| 6a | `npm --prefix mobile test`（vitest run） | **0** | 60 文件 / 547 用例全过 |
| 6b | `npm --prefix mobile run build`（vue-tsc -b && vite build） | **0** | 构建成功 |
| 6c | `npm --prefix frontend run build`（vue-tsc --noEmit && vite build） | **0** | 类型检查通过 + 构建成功（仅 chunk>500kB warning，非失败） |
| 7 | `git diff --check` | **0** | 无空白错误 |
| 8 | `npx playwright test e2e/sessions-pg02.spec.ts e2e/workspace-real.spec.ts --project mobile-360` | **1** | **18 PASS / 2 FAIL**（见第六节归因） |

> 末尾 deprecation warning（race 编译时）来自既有 `internal/secrets/store_darwin_cgo.go` 的 Keychain cgo 调用，属编译期既存告警；测试运行时不触真实 Keychain。

---

## 三、定向生产链测试结果（必跑#3，逐名）

全部 **PASS**（0 FAIL / 0 SKIP）。

**remote_production_transaction_test.go（create 真实性 + restart 边界 + shared lease）—— 13 用例 PASS**
- `TestProductionPiOmpCreateExecutesCredentialConfigArgvAndBootstrap`（+ pi/omp 子测试）—— **验收2**：create 真实执行凭据/配置/argv/bootstrap
- `TestProductionCreateExactReadyBootstrapBeforePublish` —— **验收2**：先 ready/bootstrap 再 publish（非假会话）
- `TestProductionCreateReadyOrBootstrapFailurePublishesZero`（+ ready-timeout/bootstrap-write）—— **验收2**：失败 fail-closed 发布 zero
- `TestProductionRestartReusesExecutorAndReplacesExactBinding` —— **验收3**：restart 复用 executor + 替换精确绑定
- `TestProductionRestartCommitsBoundaryBeforeStagedOutput` —— **验收3**：边界 commit 在 staged output 之前
- `TestProductionRestartPreCommitExitPublishesZeroAndClosesNewBinding` —— **验收3**：pre-commit 退出 fail-closed
- `TestProductionRestartProcessEffectFailuresPublishNoNewReceipt`（+ pty-start/pty-ready/bootstrap）—— **验收3**：process effect 失败不发布新回执
- `TestProductionRestartConfigFailurePublishesNoNewReceipt` —— **验收3**：config 失败不发布新回执
- `TestProductionRestartSharedVerificationFailureReleasesOldGeneration` —— **验收3**：共享校验失败释放旧 generation
- `TestProductionSharedLeaseExactRestartAndStaleExit` —— **验收3**
- `TestProductionSharedLeaseConcurrentRestartAndOldNaturalExit` —— **验收3**
- `TestProductionSharedLeaseStopThenRestartReacquiresNewGeneration` —— **验收3**
- `TestProductionSharedLeaseExactStopAndRemove`（+ stop/remove）—— **验收3**

**shared coordinator（m006_shared_coordinator_test.go）—— 3 用例 PASS**
- `TestM006_UninstallRejectedWhenHeadroomLeaseActive` / `TestM006_UninstallProceedsWhenNoLease` / `TestM006_CodexHeadroomMutationRejectedWithLease`

**initial_activation（internal/remote/initial_activation_test.go）—— 4 用例 PASS**
- `TestCompositeInitialActivationStagesAndCommitsH1H3WithAuthority` / `RejectsShutdownBeforeSeal` / `ShutdownFenceWaitsAndConvergesUnavailable` / `RejectsPreActivationExit`

**session_authority_integration（internal/remote/session_authority_integration_test.go）—— 16 用例 PASS**
- `TestAuthorityDesktopAndV1ShareSameIDAndExternalIsHidden` —— **验收1**：desktop 与 v1 共享同一 ID，external 隐藏
- `TestAuthorityCreateFailsClosedBeforeAnyRawStart` —— **验收2**：create fail-closed
- `TestAuthorityRestartFailsClosedWithoutReplacingBinding` / `RunOutputAndExitUpdateExactActivityAndLifecycle` / `RemovePreflightNotFoundClosesZero`
- `TestPreparedAttachCapturesCausalEventBeforeFinalCommit` / `SupersedesConnectedHolderWithExactNewLease` —— **验收3**：attach 因果/抢占
- `TestPreparedTerminalReservationIsInvisibleUntilRemoveCommit` / `TestPreparedStopServerFenceWaitsForCompositeResolution`
- `TestAuthorityStopCommitsControlAndManagerAndRetainsClosedBinding` —— **验收3**：stop 生命周期一致
- `TestAuthorityRemoveExactCloseTombstoneTerminalAndGC` —— **验收3**：remove + GC
- `TestAuthorityIndeterminateCloseKeepsMembershipUnavailable` / `IndeterminateRemoveRetriesSameBindingAfterTerminalConfirmation`
- `TestRemoveGCRegistryRetriesReceiptKeyedDebt` / `TestJournalRecoveryNeverInfersCommittedFromPendingRemove` / `TestJournalCommittedRetryDebtUsesOperationAndReceipt`

**readiness（internal/pty/readiness_test.go）—— 5 用例 PASS（含 2 子测试）**
- `TestExactPTYReadinessShellAttachWaitsForObservableShellOutput` / `DirectDoesNotRequireShellOutput` / `ProjectsExitAndTimeout`（+ exit/timeout）

**config compensation（launch_planner_executor_test.go 等）—— 22 用例 PASS**
- `TestConfigCompensationUnavailableProjectionAndRetry` / `TestConfigCompensationDebtQueryAndExactRetry` —— 凭据补偿
- `TestConfigMutationCAS*`（4）/ `TestR6_001_*`（8，external headroom lease）/ `TestR8_001_*`（4，shutdown persistence stop gate）

---

## 四、风险→测试映射表（防过测试：每个测试映射验收条件或真实失败风险）

| 风险 | 对应测试 | 验收 | 结果 |
|------|----------|------|------|
| remote create 先发布后启动（假会话） | TestProductionCreateExactReadyBootstrapBeforePublish | 2 | PASS |
| create 凭据/配置/argv 不真实 | TestProductionPiOmpCreateExecutesCredentialConfigArgvAndBootstrap | 2 | PASS |
| create 失败不 fail-closed | TestProductionCreateReadyOrBootstrapFailurePublishesZero / TestAuthorityCreateFailsClosedBeforeAnyRawStart | 2 | PASS |
| desktop 与 v1 不共享 ID / external 误曝光 | TestAuthorityDesktopAndV1ShareSameIDAndExternalIsHidden | 1 | PASS |
| 真 server list 卡片不进同一 ID | workspace-real test17（a–d） | 1 | PASS（E2E） |
| restart 替换绑定/边界顺序错乱 | TestProductionRestartReusesExecutorAndReplacesExactBinding / CommitsBoundaryBeforeStagedOutput | 3 | PASS |
| restart 各失败分支不 fail-closed | PreCommitExit / ProcessEffect(3) / Config / SharedVerification 失败分支 | 3 | PASS |
| stop/remove 生命周期不一致 | TestAuthorityStopCommits... / TestAuthorityRemoveExactClose...TombstoneTerminalAndGC | 3 | PASS |
| attach 因果事件/抢占 | TestPreparedAttachCapturesCausalEventBeforeFinalCommit / SupersedesConnectedHolderWithExactNewLease | 3 | PASS |
| initial activation 边界（H1/H3，pre-activation exit） | TestCompositeInitialActivation*（4） | 2 | PASS |
| readiness 外壳观察缺失 | TestExactPTYReadiness*（5） | 2 | PASS |
| shared coordinator 并发 lease 泄漏 | TestM006_*（3）/ TestProductionSharedLease*（4） | 3 | PASS |
| config compensation 凭据补偿缺失 | TestConfigCompensationUnavailableProjectionAndRetry / DebtQueryAndExactRetry | 2 | PASS |
| 真 WS attach/output/stop/restart 全链 | workspace-real test17/18（real server，fake CLI） | 1/2/3 | PASS（E2E） |
| observer 写拒绝 / revoke 踢出 | workspace-real test19/20 | 3 | PASS（E2E） |

---

## 五、Playwright E2E（必跑#6）

**workspace-real.spec.ts（真实 Go server 全链，REST/WS/causal/journal 全真实，仅 fake CLI——spec 顶部如实声明边界）—— 4/4 PASS**，正中用户三个验证点：

- test17 `a–d. 真配对→真 list→真 WS attach→live 七类组件真实帧转化 + Composer input`（4.1s）—— **remote create（控制面真 REST，走完整 gate/catalog/stream）→ 真配对 → 大厅真 list（造的会话卡片可见）→ 点卡片导航 `#/workspace/{sessionId}`（同一 ID）→ 真 WS attach（同源 Cookie/真实升级/因果 attach）→ 获取控制权 → 注入七类输出经真实因果 drain → Composer input**。
- test18 `e. stop 真实态切换 + 重启边界真实渲染`（3.3s）—— stop/restart 真实态。
- test19 `f. 观察者（第二 device）写拒绝 vs 控制者成功`（4.3s）。
- test20 `g. revoke 后 1008 device_revoked 真实踢出`（4.4s）。

**sessions-pg02.spec.ts（route mock 组件级）—— 16/18 PASS，2 FAIL（见第六节）**。

### L3 blocked（如实区分，不伪造）
- workspace-real 的 launch seam 指向**确定性 fake CLI**（不启真实二进制/进程）。**真实 CLI 二进制 → 真实 PTY → 完整 run-observation 端到端属 M4/最终验收**，本机（macOS）未跑。
- **Windows/ConPTY 路径**：本机为 darwin/arm64，无法验证 Windows ConPTY/service_other_stub。
- **真实 Wails App 桌面进程**：未启动真实 App 二进制做桌面 embedded 会话的运行时取证（自动测试以 Go 单测/集成 + 真 server harness 覆盖同等逻辑路径）。
- 以上为环境/平台限制导致的 L3 不可达，**非自动测试失败**；已跑的自动测试均为真实 PASS。

---

## 六、失败原始原因与归因（2 个 E2E FAIL）

均位于 `e2e/sessions-pg02.spec.ts`（**route mock** 组件测试），非 workspace-real、非用户核心验证链：

1. `sessions-pg02.spec.ts:185 四类 CLI 启动：POST {cliType} → 201 → 卡片出现` —— line 205 `page.locator('.cli-card', { hasText: 'Pi' }).click()` strict mode violation：匹配到 2 个元素（"Pi" 与 "Oh My Pi"）。
2. `sessions-pg02.spec.ts:271 CLI available=false → 卡片禁用` —— line 288 `expect(piCard).toBeDisabled()` strict mode violation：`.cli-card` hasText 'Pi' 解析到 2 个元素（"Pi" 与 "Oh My Pi" 均渲染为 disabled 卡片）。

**归因（既有失败 / 测试侧 locator 缺陷，非本次工作树引入，非生产回归）**：
- `sessions-pg02.spec.ts` 与 mobile 前端源码（CliLauncher/SessionsPage，committed HEAD 已渲染 "Oh My Pi" omp 卡片）在本工作树**均未修改**（`git status` 无 mobile 前端、无该 spec）。
- 本工作树改动**全部是 Go 后端 + Go 测试 + docs + 新增 Go 文件**；Go 后端改动**不可能影响一个全 route mock 的浏览器组件测试**（其 API 响应由 spec 内 fixture 完全 mock）。
- 故为**测试侧 locator 在新增 omp 卡片后变得非唯一**的既有基线缺陷，与"desktop embedded 列出 / remote create 真实性 / 生命周期一致"三个验证目标**正交**。
- 原始证据：截图 `test-results/sessions-pg02-.../test-failed-1.png`、`error-context.md` 已留现场（未清理）。

> 遵守契约：**只报告不修复**。该缺陷的修复（locator 收敛为精确匹配，如 `getByRole('button',{name:'Pi'})` 或排除 omp）交 luban；是否修复由 Leader 裁定。

---

## 七、现场保护与 git 状态（必跑#7）

- `git diff --check` = 0（无空白错误）。
- **post-test `git status` 与初始完全一致**（除新增本报告目录 `agent-outputs/wukong/`，属规定交付物）。测试运行**未修改任何源码/测试/配置**。
- `.claude/test-status.json` 在本次测试**启动前已是 `M`**（既有改动，非本次产生）；`.claude/agent-teams-state.json`、`.claude/settings.json` 为既有未跟踪文件，**未清理用户既有文件**。
- 工作树未跟踪的 Go 源文件（`config_sync_*.go`、`internal/launchplan/`、`internal/processcap/`、`internal/pty/readiness.go`、`internal/remote/initial_activation.go` 等）为**被测功能的既有未提交工作**，非测试运行生成。
- `test-results/`（Playwright 产物）已 `.gitignore`，不需纳入源码。
- 未执行任何 `git reset/checkout/clean/commit`。

---

## 八、限度披露

- 未编写任何新测试（角色纪律：只验证不扩量）。本次为终验，跑的是任务指定的必跑集 + 既有测试，无过测试。
- 总执行耗时约 6–7 分钟（全量 go test ~3min + race ~2min + E2E ~0.5min + builds ~1min），与"独立终验"目标匹配，必要性充分。
- 覆盖率未作目标数字；未覆盖路径已在第五节 L3 blocked 如实列出。

---

## 九、结论：met / partial / blocked

- **验收1（desktop embedded 可被 v1 lobby 列出）—— MET**：`TestAuthorityDesktopAndV1ShareSameIDAndExternalIsHidden` PASS + workspace-real test17 真 list 卡片可见 + 同 ID 导航。
- **验收2（remote create 非假会话）—— MET**：`TestProductionCreateExactReadyBootstrapBeforePublish` / `PiOmpCreateExecutesCredentialConfigArgvAndBootstrap` / `CreateReadyOrBootstrapFailurePublishesZero` / `AuthorityCreateFailsClosedBeforeAnyRawStart` 全 PASS；workspace-real test17 真 server create→publish 顺序验证。
- **验收3（restart/stop/remove/attach 生命周期一致）—— MET**：`TestProductionRestart*`（边界+四类失败分支）/ `AuthorityStopCommits...` / `AuthorityRemoveExactClose...GC` / `PreparedAttach*` 全 PASS；workspace-real test18/19/20 真 server stop/restart/observer/revoke 全 PASS。

**整体：MET（核心闭环达成）。**

**PARTIAL（正交项，不影响闭环判定）**：sessions-pg02 route-mock 组件测试 16/18，2 FAIL 为既有测试侧 locator 缺陷（omp 卡片使 `hasText:'Pi'` 非唯一），非本次工作树引入、非生产回归。

**BLOCKED（L3，环境/平台限制，非失败）**：真实 CLI 二进制→真实 PTY→完整 run-observation（M4/最终验收）；Windows/ConPTY；真实 Wails App 桌面进程运行时取证。

**建议下一步**：
1. 2 个 sessions-pg02 locator 缺陷可交 luban 修复（收敛为精确 getByRole 或排除 omp），属低优先级既有缺陷。
2. 用户三个核心验证目标已由 Go 单测/集成 + workspace-real 真 server E2E 充分闭环；L3 真实二进制/Windows 验收可在对应平台环境另行安排。
3. 本测试证据可交 diting 审核完整性与可信度。

<!--AMAGI:DONE-->
