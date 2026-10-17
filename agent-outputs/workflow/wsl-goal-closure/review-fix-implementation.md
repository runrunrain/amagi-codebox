# PR01–09 合并修复实施报告（wsl-goal-review-fixes）

> **最终验收覆盖声明**：以下met为实施者当轮自检结论，已被 `phase-rereview.md` 部分否定。PR01/02/03/05/07仍Major，完整完成声明已拒绝；保留原报告供追溯，不可据此发布。最新阶段结论见 `closure.md`。

- 工作区：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/wsl-goal-review-fixes`（分支 `amagi/wsl-goal-review-fixes`，基线 HEAD `58e7577`，无 commit；改动留在工作区由 Leader 汇总）。
- 输入快照：`integrated-stage.patch` 已机械 `git apply --check` 校验后导入（73 文件）。导入态快照 diff 存于本树 `.amagi/review-fix/imported-snapshot.diff`（sha256 `820a67eb…d8b5f`）+ `.amagi/review-fix/imported-filelist.txt`。
- 本轮差量：`review-fixes.delta.patch`（sha256 `d7378ce0…4bae3`，18 文件，仅本轮 PR 修复；已验证「HEAD + integrated-stage.patch + delta == 当前树」逐字节一致，验证用临时 worktree 已按守卫指引移动隔离并 prune）。
- 上游输入：`phase-review.md` / `direction.md` / `verification.md` 及五个复现脚本（`.log`）全部完整读取；复现脚本复制到本树 `.amagi/review-fix/repro/` 后 `go run` 逐一确认反例在导入态成立，再修复。

## PR 逐项结论

| PR | 状态 | 修复与证据 |
|---|---|---|
| PR01 | met | shared gate 区分「证明无受管副本」与「可能残留」。`state.go`：v1→v2 迁移按 v1 顶层 Distro 登记 `LegacySensitiveRisk`（无 Distro 不归属，不误算别的 distro）；`coordinator.go` 新增 `SensitiveCopyPending`（矩阵：无登记/withdrawn*/Detached/local-deletion-frozen → false；曾写入 → true）与 `LegacySensitiveCopyPending`；新代成功写入或确认撤回时清除标记。`app_wsl_supply.go` gate：withdrawal-unconfirmed 之外的全部 Blocked（含 source-unavailable / target-unresolved / inspect-failed）与 UnitFailed/runErr 分支统一经 `visionManagedCopyPossible`（=两谓词之或）判定，可能残留 → 中文可处理错误阻断；无历史/无副本的普通可选态放行（回归：`launch_wsl_review_fixes_test.go` 4 测试 + `review_fixes_test.go` PR01 两组，含 LEGACY_WITHDRAWAL/MANAGED_SOURCE_ERROR 反例转正）。五 CLI 入口接线（C1）不变，gate 语义修复即覆盖桌面/remote/restart。 |
| PR02 | met | ① coordinator `Apply` 的 `ActionNone` 对 `Sensitive()` 适配器（vision/managed-models）统一 `ensureSensitiveTargetPerms`：健康 0600 → 零字节重写保持；unknown/宽权限 → chmod 600 后 StatMode 复验，仍不可证 → `sensitive-permissions-unsafe` 失败（gate 阻断消费）。② launcher `wslRetighten600`：`!ok` 不再当安全，统一收紧+复验+失败。③ A2 各降级消费 fallback（mtime 不可判、前写失败、回写 Windows 失败、并列回写失败）统一 `retightenDistroAuthOrFailClosed`：不可证 600 → `errWSLAuthLooseLeftover` → NoAuth fail-closed。不删混合文件。回归：`review_fixes_test.go` PR02 三例 + `wsl_review_fixes_test.go`（launcher）5 例。 |
| PR03 | met | `submitSensitive` 读回失配改走 `recoverSensitiveCommitMismatch`：单次复验（瞬时读毛刺→提交有效）、目标==前像（提交未落地→如实报未证实，零写入）、其余（后改/损坏/不可读）→ 绝不删未知现状，前像经 0700 staging + 0600 收紧复验后 rename 到 `.amagi-supply-recover-*` 备份，错误链给出备份位置；取消时零新写（备份亦不写，如实报告）。普通单元 chmod 失败恢复也改为「仅目标仍等于本次提交才删」。回归：持续失配有界（staging 写 ≤2，不再递归）、备份 0600 内容=前像、preimage-equals 分支零备份零删除、恢复中取消零新写。粒度披露：「chmod 恢复失败 + 目标删除又失败」双失败分支为代码覆盖+审读，未独立注入（需文件系统级 Remove 故障接缝，本轮未新增）；错误链已区分 removed/indeterminate/left-intact 三态。 |
| PR04 | met | 新增 `resolveDistroGeneration`（Targets 索引按 distro 归属；多代并存拒绝歧义；顶层 Distro/Generation 兼容线索；停机可用——只读索引不探测）。`DropUnitRegistration` 三分支与 `SetUnitSelection` 全部改用明确 generation（entryFor 同代命中才取 prev；unknown/legacy 不搬运授权/基准）。回归：A→B→keep A 只改 A（反例转正）；A→B→对 A 选择集落 A；歧义/未知 distro 明确报错。注：detach 现原位保留全部登记字段（旧实现对技能分支丢 Frozen/SelectedKeys——更如实，属行为收紧非放松）。 |
| PR05 | met | `stateStore.mutate` 深复制上修改、落盘成功才发布内存态（失败回滚到修改前——内存不冒充已持久化）。`recordProjectionState` 返回错误：投影/接管/技能/资源在内容已提交但清单未落盘时返回 `content-committed-state-unpersisted`（UnitFailed，Err 结构化，Undo 保留，写入内容不删）；`recordProjectionWithdrawn` 失败 → `withdrawn-state-unpersisted`（WarnDegraded，撤回幂等重确认）。插件 `registration-unchanged` 语义未动。回归：反例 STATE_SAVE_FAILURE 转正（applied→failed，GetStatus 无 written 声明，文件保留，Undo 可用）。 |
| PR06 | met | `loadLocked` 返回 `cloneStateFile`（Units/Subs/SelectedKeys、Targets/InspectedAt、Plugins/Filters 三态保全、Pending*、LegacySensitiveRisk 全深复制）；mutate 在克隆上修改。产品 race 消除：`TestCompensateApplySerialized` -race 由 exit 1 → ok（导入态复现 exit 1 存 `.amagi/review-fix/repro/`）。新增不可变/嵌套不别名测试 + 并发 Plan/Apply/GetStatus race 测试。修复过程中发现的克隆自毁 bug（先重赋值再 range）已消除并有三态 round-trip 回归。 |
| PR07 | met | ctx 贯通：`TakeOverUnit`（入口+等锁后）、`takeOverSkill`/`takeOverResource`（入口+等锁后）、`applySkillUnit`/`applyResourceUnit`（入口+不可逆交换前）、`submitDirUnit`（入口+交换前检查，取消时清理 staging 保留现状）、`applyResourceUnit` 文件单元改 `submitFileCtx`、`ResolveTarget`/`Inspect` 入口检查、插件 `applyDistroFilters` 入口检查（重放后取消不再做本地过滤写，按既有 filter-apply-failed 语义收敛）、敏感恢复/备份写受取消门。回归：反例 CANCELED_TAKEOVER 转正（blocked context-canceled，目标零写入）+ 资源/常规单元取消零新写。边界如实：Windows wsl.exe 退出不证明 Linux 子进程退出（原注释保留，未虚称）；A2 适配器 Sync 为单文件快速本地操作，取消保护由 Apply 逐单元检查承担（A2Adapter 接口未扩 ctx，避免涟漪；此为已知边界非回归）。 |
| PR08 | met | `PluginOperation.Filters` 改 `*PluginFilters` 三态指针：nil=未提供（沿用 reg.Filters，旧调用兼容）；非 nil=显式完整期望（全字段 nil=全部加载/纯字符串形态）。`runUpdate` 据此可把旧过滤集重置回全量；`runInstall` 用 `opFiltersOrZero`。App 调用（`runPiPluginTargeted`）统一经 `piPluginOpFiltersPtr` 携带完整期望快照。回归：反例 CLEAR_FILTERS 转正（update=updated，settings 归一字符串形态）+ nil 兼容 + 收敛后幂等 already-ok。 |
| PR09 | met | `fakeProbes` 的 `modes/chmodErr` 加互斥锁（并发原语测试共享实例无 race）+ 新增 `setChmodError` 锁内注入；root 异步推送改 `wslSupplyPushWG` 追踪 + `waitForWSLSupplyPushIdleForTest()`：`TestSaveAmagiConfigMarksPiConfigDirty` cleanup 等待、`TestEnableWSLPiConfigSupply` 写接缝前排水+cleanup 等待（不 sleep、不删并发测试、不跳过 race）。-race 四包全绿（`target-race.log`）。 |
| PR10 | 不由本任务修改 | Leader 负责撤回主树 `verify-staging-window.ps1` 并在最终 Windows 清单保留人工授权验证要求；本轮未提供替代自动脚本（审查意见：未经真机验证的脚本不得交付）。 |

## 修改文件清单（= delta.patch 内容）

生产：`internal/wslsupply/state.go`、`coordinator.go`、`resources.go`、`skills_submit.go`、`plugins.go`；`internal/launcher/wsl_agent_config.go`、`wsl_pi_login_sync.go`；`app_wsl_supply.go`、`app_wsl_plugins.go`。
测试（含更新断言到修复后语义的既有用例）：`internal/wslsupply/coordinator_test.go`（fakeProbes 锁+磁盘权限回退）、`plugins_test.go`（指针三态）、`review_fixes_test.go`（新）；`internal/launcher/wsl_pi_login_sync_test.go`、`wsl_agent_config_idempotent_test.go`（健康 0600 前提回填）、`wsl_review_fixes_test.go`（新）；`app_wsl_supply_core_test.go`（gateStubRunner 能力面）；`wsl_s2_remote_test.go`（推送生命周期等待）；`launch_wsl_review_fixes_test.go`（新）。
既有用例改动均为「断言修复后契约」：idempotent 两测在首写后回填 fake 探测面 0600（真实不变量：staging 收紧后 rename）；`write failure with old copy intact degrades` 的注入从「StatMode 全坏」改为「仅 staging 坏 + 副本体可探测 0600」（保留原测试意图：写失败旧副本降级）；`fakeWSLForSync`/`fakeProbes` StatMode 未记录路径回退真实文件权限（模拟健康 0600 副本的可信探测面）。

## 验证（真实命令，cwd=本 worktree；日志 `.amagi/review-fix/logs/`）

| 检查 | 命令 | 结果 |
|---|---|---|
| 目标包普通测试 | `go test . ./internal/wslsupply ./internal/launcher ./internal/piplugin -count=1` | exit 0，4 包 ok（target-tests.log；含新增 TestReviewFixPR03RecoveryCanceledSkipsBackupWrites 等全部回归） |
| 目标包 race | 同上 `-race` | exit 0，4 包 ok（target-race.log；含原失败 TestCompensateApplySerialized/TestSubmitFilePrimitiveTrueConcurrency/TestEnableWSLPiConfigSupply） |
| vet | `go vet . ./internal/wslsupply ./internal/launcher ./internal/piplugin`；另跑全仓 `go vet ./...` | 均 exit 0（full-vet.log 仅既有 secrets 弃用告警） |
| Windows 交叉编译（不执行） | `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test <四包> -run '^$' -exec /usr/bin/true` | exit 0（windows-compile.log；仅编译检查，不伪称执行） |
| 全仓回归 | `env -u AMAGI_CODEBOX_AI_PROXY go test ./... -count=1` | 唯一失败 `TestDarwinResolveExecutableFindsCodexInLocalNodeBin`（宿主 `~/.local/bin/codex` 污染，= verification.md 基线已知项，与本轮无关；full-go.log） |
| 反例先行 | 5 个复现脚本在导入态 `go run` 全部复现（`.amagi/review-fix/repro/*.local.log`） | 修复后：PR02/03/04/07/08 底层行为已反转（`*.after.log`）；PR01/05 旧脚本断言的是 bug 形态（gate 判定在 App 层/登记回滚后 Units[0] 不存在），已由正式回归钉住 |
| delta 完整性 | 临时 detached worktree：HEAD+integrated patch+delta 与当前树 `diff -r` | 零差异（方法可复核） |

## 失败路径如何保护用户文件与秘密

- 未知/不可证权限的敏感副本：不再被静默消费（gate 阻断 + 中文指引）；健康 0600 零字节重写保持。
- 读回失配：目标现状（含用户后改）永不删除；用户前像保全为 0600 隐藏备份，错误链指路；恢复零递归有界。
- 状态落盘失败：已写内容保留、不虚报 synced、内存/重启不误判归属；下次运行按未托管处理需重新接管（错误链已说明）。
- 停跟随/选择集：只改请求目标的分区；歧义拒绝不改别人；停机目标经只读索引可操作。
- 取消：入口/等锁后/每单元/不可逆交换前/后继本地写检查；已取消请求零新写/安装/权限改写。
- 迁移：v1 旧授权不继承，但旧副本风险以 distro 归属的待确认状态保留至新代写入或确认撤回；无归属线索不登记（不误伤其它目标）。

## 未覆盖 / 边界

- Windows/WSL 真机（UNC rename/9P 权限、真实 distro 升级链、真实 CLI 消费、Linux 子进程取消回收）未执行——macOS fixture + 交叉编译不能替代（phase-review §6 原样适用）；`windows-validation.md` 人工清单仍待真机。
- O7 restart 语义漂移：未获新证据，按审查保留不盲修。
- A2 适配器 Sync 未扩 ctx 接口（单文件快速操作，Apply 逐单元检查承担取消门）；如后续要求交换前检查到 A2 内部需接口变更，单列。
- `DetachUnitOnDistro` 保留 Frozen/SelectedKeys 等完整登记字段（旧代码部分丢弃）——展示面更如实，如需旧行为请 Leader 指示。
- 审查复现脚本（phase-review-*-repro.go）为冻结证据，其中 PR01/PR05 两个脚本在新行为下不再可运行/断言失效（它们编码的是 bug 形态），正式回归以本报告测试为准。

## 回滚

回到导入态应对本轮 `review-fixes.delta.patch` 先执行 `git apply -R --check`，仅检查通过且没有后继修改时才经授权反向应用；**不能使用 `git checkout -- <files>`**，因为HEAD仍为58e7577，那会一并丢弃这些文件内已导入的C1–C5改动。实施者未写主树源码，后续由Leader集成。脏worktree保留，未经明确处置不清理。
