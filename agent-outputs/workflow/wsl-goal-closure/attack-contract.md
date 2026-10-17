# WSL 供给剩余 Major：攻坚实施契约

## 结论与边界

保留 `direction.md` 的目标侧供给、按 generation 授权、普通配置条件补偿、A2 专用同步路线。PR01/02/03/05/07 按同一提交生命周期修复：**先留下可验证、可重启读取的事实，再改目标；结果、状态页和启动门消费同一事实模型。** 不再以单个失败分支返回 error 证明闭环。

本契约供鲁班实施。标“新增”的接口不是当前实现；命名可沿项目惯例调整，顺序与失败语义不可自行改写。任务提供基线为 HEAD `58e7577` 加全部未提交集成修改；本轮读取工作区，不用旧 HEAD 替代现状。

硬约束：

- 未知归属产生核验责任，不产生写权限；consent、旧副本线索、本次写入事实分别保存。
- 请求取消停止后继业务写入，不取消已经发生写入的事实登记、保全和有限恢复责任。
- 敏感副本不安全、撤回未确认、已提交未登记，不得借“无历史”或 `Fresh` 绕过启动门。
- 原目标是唯一可靠前像时，备份未验证不得替换原目标；恢复失败保留证据，不递归、不承诺无界故障下恢复成功。
- 外部后改不能被恢复覆盖或删除。进程内锁不等于文件系统原子内容 CAS。
- A2 hash+mtime、2 秒容差、宿主退出登录语义不变；auth 内容/hash 不落供给状态，不回滚新 token。
- 普通可选项失败仍按原范围降级；不扩展多 distro 产品、整树镜像、自动安装或新执行平台。

## 上游与根因链

以下输入已完整读取：

1. `agent-outputs/workflow/wsl-goal-closure/phase-rereview.md`
2. `agent-outputs/workflow/wsl-goal-closure/phase-rereview-repro.go`
3. `agent-outputs/workflow/wsl-goal-closure/phase-rereview-repro.log`
4. `agent-outputs/workflow/wsl-goal-closure/direction.md`

独立复审的五个 Major 作为已证实事实承接，不重新评级、不用静态阅读推翻动态反例。日志由上游执行，本轮未重跑。

| 已证实链条 | 当前工作区锚点 | 根因与修复后预测 |
|---|---|---|
| A 发布→只检查 B→升级：`pending_A=false pending_B=true` | `state.go::migrateLegacyState`；`coordinator.go::Inspect/LegacySensitiveCopyPending`；App shared gate | 最近接触身份被当成单元历史归属。保存未归属线索，以旧指纹和实际文件缩小候选；A 进入核验，B 不凭顶层字段继承风险/授权。 |
| distro 更新/并列，回写成功直接 Applied，0644/unknown 未验 | `wsl_pi_login_sync.go::SyncWSLPiLoginAuth` 两个成功出口；`a2.go::classifyA2` | 同步结果替代消费资格。全部可消费出口汇入权限门；回写成功不再绕门。 |
| 持续污染：`backups=1 valid_preimage=false` 却报 preserved；取消后 `backups=0` | `submitSensitive/recoverSensitiveCommitMismatch/preserveSensitivePreimageBackup` | 唯一可靠前像只在内存；提交后才备份且只验 mode。提交前验证持久前像；备份坏则目标不动，提交后取消仍有前像。 |
| 首次 final save 失败：`pending=false displayed=consented`；源关闭后 Fresh 且秘密残留；后续失败显示旧 written | `state.go::mutate`；`applyProjection/recordProjectionState`；`shared_files.go::Plan`；App gate | durable 基准回滚同时抹掉已发生写入事实；Undo 只在单次返回存活。持久基准、写前 intent、当前观测分开；重启仍可识别未完成发布/撤回。 |
| 移走旧目录后取消：`target_present=false hidden_preimages=1` | `skills_submit.go::submitDirUnit`；A2 无 ctx；`plugins.go::applyDistroFilters` | 两个 rename 被当成一个边界；业务取消与恢复取消混同。第一次移动前检查，移动后承担恢复责任；ctx 到真实 probe/runner/最终发布。 |

排除的解释：这不是换 hash 算法、补一次重试或只跑 race 就能解决的问题；缺陷在持久顺序与事实解释。无需更换整体供给架构。PR04/06/08/09/10 保持 closed，仅做本轮直接回归。

### 需要纠正的测试断言

- `TestReviewFixPR01LegacySensitiveRiskPending/without distro` 混淆“不绑定任何 distro”和“丢弃所有线索”。应保留 unresolved evidence，逐目标核验。
- `stagingSabotage.install` 只有 `writes == 1` 污染；所谓 persistent 用例实际是单发。增加每次 staging 均污染的反例。
- `TestReviewFixPR03RecoveryCanceledSkipsBackupWrites` 奖励取消后丢唯一前像。改成公开入口提交后取消，断言原目标或有效持久前像至少一处保留原字节。
- `TestReviewFixPR05StateSaveFailureNotApplied` 只验返回失败、非敏感首次文件保留、Undo 非 nil，未验状态页、shared gate、撤回、重建 Coordinator。写前 intent 接入后，Candidate 中破坏存储可能应导致“尚未提交”；另设真正 final save 故障，不能为保旧断言延后 intent。
- PR07 正式测试主要取消于入口；须覆盖 move-aside 后、filter staging 后、A2 真实探测中。

## 持久事实与状态接口

### 最小结构增量

`stateStore.current` 继续表示最后确认持久化的清单；`mutate` 保存失败不发布新 durable 基准。不能改回“先改 current 再 save”。以下结构均由同一 stateStore 管理，不建立第二份缓存或数据库。

| 结构 | 必需字段与含义 | 落点/公开约束 |
|---|---|---|
| `OperationIntent`（新增） | `Version, OpID, Kind, TargetIdentity, Unit, RelPath, Sensitive, BeforeExists, BeforeHash, AfterHash, BackupRel, StagingRel, DisplacedRel, PriorEntry, ProposedEntry, ParentOpID`；Kind 为 write/remove/dir-swap/compensate，目录附结构指纹 | 配置目录 `wsl-config-supply-pending/<opID>.json`，私有、唯一、不可变；不含正文。路径仅内部生成，不接受客户端绝对路径。A2 不使用此 hash/正文结构。 |
| `StateFile.SettledOperations`（新增） | `map[OpID]{TargetKey, Unit, Disposition, RelatedOpIDs}`；Disposition 为 committed/restored/removed/not-applied/user-resolved | 与对应 StateEntry 更新同次主清单保存；处理“主清单成功、intent清理失败”和多次操作的先后关系。仅 LastOperationID 不够：后续操作会覆盖它。 |
| `StateEntry.LastOperationID`（新增） | 最近确认的操作，用于状态关联 | 主清单 schema v3；不作为唯一终结凭据。 |
| `OperationObservation`（新增，内存） | `OpID, Effect=none/committed/unknown/restored/removed, Registration=confirmed/pending, Recovery=none/available/conflict/unavailable, ErrorCode` | 独立于 current，锁内深复制；重启由 intent+目标重建，不能代替磁盘记录。 |
| `LegacyEvidence`（新增） | 稳定 ID、原 schema、原 entry generation/hash/outcome/detached、原顶层 generation/distro/home、来源、逐目标处置 | v3 主清单；保真线索，绝不转成 Consented。v2 map 原值保留作低可信来源证据。 |
| `StoreHealth`（新增） | main/pending 的 fresh/ok/missing-after-use/unreadable/corrupt/unsupported 与错误 | load 显式返回；GetStatus 仍不探测 WSL。 |

独立 intent 的必要性：本次故障点就是聚合主清单保存失败；只内存 pending 无法经受重启，写完目标再改主清单存在崩溃窗口。每操作一个小型不可变文件复用现有私有原子写与锁，留下最小写前事实。不是通用事务框架，不增加后台无限重试。

`PriorEntry/ProposedEntry` 不提供授权恢复能力：reconcile/补偿只处理基准字段，不能恢复已被用户撤销的 consent/selection/detached。主清单损坏时禁止从 intent 臆造全部授权。

### 持久化顺序与清理

1. 私有目录→唯一 tmp→完整写→文件 Sync→Close→权限检查→内容读回验证→rename 唯一 intent 名→最终读回验证。
2. intent 确认可读取后，才允许改变目标；后续主清单更新同次写 StateEntry 与 SettledOperations。
3. 主清单保存失败保留 intent；rename 后 readback/权限失败属于“保存结果不可判”，不能断言磁盘仍旧。重新读取 OpID 可确认，读取失败则 pending。
4. 终结顺序：保存 settled 事实→解除当前撤销租用→清理该操作前像/候选/displaced→删除 intent→最后删除 settled tombstone。任一步失败保留前一步证据；重启不能把清理失败误当未提交。tombstone 删除失败只留可清理历史，不重新写目标。
5. 补偿/撤回若终结先前 pending 发布，必须同次记录相关 Parent/RelatedOpIDs 的结论，不能只删除最新 intent 留旧发布重新出现。旧前像无权复活已经撤回的秘密。

使用项目已有平台私有文件能力，Windows `os.Chmod(0600)` 不作为 ACL 隔离证据。Sync/Close/rename 错误不忽略；目录持久化能力不足准确报告。本契约保证有可读持久材料时的进程崩溃/重启识别，不承诺断电或任意文件系统失效的强事务。

pending 有界扫描本目录 manifest，不扫工程/CLI 根。格式损坏记录不自动删除；范围已知则冻结该目标/单元，范围未知则停止新的自动供给写入，当前敏感消费进入现场核验。main 有读取错误时 `mutate` 不得把空默认值保存覆盖原文件；修复存储后重新读入再操作。

首次安装 main 不存在、pending 为空可识别 fresh；有 pending 时 main 缺失绝非 fresh。若外部把所有状态证据同时彻底删除，重启程序不能区分“全新安装”和“证据全毁”，不得声称仍能恢复历史；当前有敏感文件时按未受管/不确定归属处理，不自动接管。无界故障下的保证是“不先主动摧毁唯一前像、不虚报”，不是凭空重建消失的事实。

### 返回与展示

`UnitResult` 增加 `OperationID, Effect, Registration, Recovery, Consumption`；Consumption 为 `not-applicable/absent/safe/unsafe/unknown/user-owned`，默认零值不能 safe。`UnitStatus` 增加 `PendingOperation, StateUnpersisted, RecoveryPending, LastAttemptOutcome` 或等价结构。

GetStatus 合并 durable、intent、当前 observation；pending/失败优先于旧 written、consented、awaiting-consent。旧 LastSuccess 可保留，明确是历史时间。即使 Targets 的 Inspect 保存失败，也能由 intent 的完整目标展示首次发布待核实。没有当前观测时 intent 表示“可能已提交”，不可显示“确定未写”。状态页和 gate 不再各自猜 LastOutcome 字符串。

## 文件提交与恢复

### 统一文件原语

`submitFileCtx` 改收 `CommitRequest{Target, Unit, RelPath, ExpectedBefore, Candidate, ProposedEntry, Sensitive}`，返回 `CommitReceipt, error`；error 非 nil 时 receipt 也不可丢。UnitID 和候选登记必须写前已知，不能提交后才填 Undo.unit。applyProjection、文件 TakeOver、资源文件共用。

1. 持 applyMu；检查 ctx；复核完整目标身份、授权、detached、旧计划 hash、路径形态；同路径未决 intent 先 reconcile。读写 state helper 用 `entryFor(unit, target.Generation)`，不能通过全局 `st.entry()` 搬运基准。
2. 读前像，区分常规文件/absent/unreadable/不支持类型。首次创建记录 BeforeExists=false，不制造空文件前像。
3. 有前像则在目标 `.amagi-staging/recovery-<opID>/before` 保存。先验证目录700，再写、Sync/Close、distro chmod600、复验 mode 与完整字节，最终备份名再次读回。敏感正文不跨到宿主状态目录。任何备份损坏/不可读/权限不可证均中止，原目标仍未变；坏 tmp 清理失败报告残留，不叫可恢复前像。
4. 准备候选 staging，验证字节/类型/权限。前像与候选均有界一次准备，不递归。复核原目标仍为 ExpectedBefore；不匹配拒绝旧计划，不能用旧备份替换新目标。
5. 保存 intent 并读回。失败返回 Effect=none，原目标不动。写前存储不可用不允许“先提交、以后补记”。
6. 检查 ctx、复核 backup 和目标。前向发布同样采用下节的保留 displaced/no-replace 流程：移走目标后复验该实体仍为预期 before，再 no-replace 发布候选。出现外部版本则保留/尝试放回，不覆盖。第一次移动已是副作用，不能只在第二次 rename 前考虑取消。
7. 任一移动/发布返回 error，都重新分类实际目标，不能假定未写。已发生副作用立刻写内存 observation，保留 intent/backup。发布后精确读回候选字节和敏感权限；后改、损坏、不可读返回 unknown/conflict，禁止 Applied/删目标。
8. 目标验证通过后保存 ProposedEntry+LastOperationID+settled committed。保存失败返回 `content-committed-state-unpersisted`，旧 durable、新 observation、磁盘 intent 同时保留；首发与第 N 次同义。
9. 成功供给内容是持久配置。保留撤销租用直到调用方确认生命周期；重启不因“不知道 PTY 是否启动”自动回滚已确认供给。

`recoverSensitiveCommitMismatch` 只分类 receipt 并有限恢复既有 backup，不在错误后第一次创建前像，不调用 submit 递归恢复。普通非敏感原语也补 staging 后、最终移动前 ctx 检查。

### 恢复权与外部后改

现有 `restorePrev` 无条件 RemoveAll(target)、`restoreSkillPreimage` 先删当前再 rename、`restorePreimage` 无条件覆盖，均不得继续承担本契约恢复路径。坏备份不得触发腾位删除。

| 目标事实 | 自动处置 | 禁止 |
|---|---|---|
| 仍为 before | 验证后记录 not-applied/restored | 为证明恢复而再次改写 |
| 仍为本次 after | 验证 backup 后有条件补偿 | 先删当前再试写 backup |
| 缺失且本次已移走旧树/文件 | 验证 prev 后尝试 no-replace 放回 | 覆盖突然出现的新目标 |
| 其它字节/树/类型或不可读 | 保留目标和前像；conflict/indeterminate | 覆盖后改、宣称 confirmed |
| backup 损坏/不可读/权限不明 | 不动目标，recovery-unavailable | 用坏备份换目标、虚报 preserved |
| 完整身份变化 | 保留旧材料，不恢复到新代 | 同 distro 名继承恢复权 |

原子 no-replace 不等于原子内容 CAS。采取以下有界流程保护 CAS 后出现的外部版本：

1. 将待撤销目标移入唯一、写前已记录的 displaced 名，不覆盖已有 displaced。
2. 移动后再次核验该实体。若已是外部后改，用 no-replace 尝试放回；原路径又出现新目标就保留两处并报冲突，绝不删任何一处。
3. 确认 displaced 属本次写入、backup 有效后，将独立且已验证的恢复 staging（目录可用真实 prev）no-replace 落位；遇新目标拒绝并保留材料。
4. 不清理未知 displaced。成功且证据完整的旧对象也只按前述终结/释放顺序清理。

新增窄 `MoveNoReplace/PublishNoReplace` 放 internal/platform 的分 OS 文件：文件可用同文件系统原子硬链接发布完整 staging；目录须原子不覆盖移动。Windows 可使用不带 replace 标志的原生移动 API，UNC 行为须真机验证。禁止 `Lstat` 后 `os.Rename`、非原子直接 WriteFile(O_EXCL) 发布半文件，或 shell `mv -n` 冒充原子发布。能力不支持时返回 pending、保留 prev，不退化为覆盖。

这个原语只服务已证实恢复/目录 swap 边界，不建执行平台。临时路径可能发生可见移动，不能承诺并发不协作编辑器完全无感；发生冲突必须报告原路径及保留位置。任意外部进程持续写旧 inode 的线性一致性不在保证内；CodeBox 不主动覆盖未知版本、不主动毁唯一前像。测试要在初次 CAS 后、最终落位前注入外部写，而非只测调用前手改。

### 目录 swap

submitDirUnit 接同类 CommitRequest/Receipt。真实旧目录保留为 prev，替代“成功交换后删 prev，再依赖内存快照重建”。空目录、链接、目录结构不能丢。

顺序固定：staging 树准备/权限与结构验证→保存含 target/prev/staging/displaced 的 intent→ctx 检查→target 移到 prev→复验 prev→ctx 检查→no-replace 发布 staging→验证目标→主清单确认。

- 第一次移动前取消：原目标始终不动，清理本次 staging。
- 移走旧目录后取消：用独立 recoveryCtx 验证 prev/目标；目标 absent 时 no-replace 放回，读回确认 canceled-restored。恢复失败保留 prev，空目录也不可 Remove；明确位置与失败阶段。已有外部新目标绝不覆盖。
- 第二次发布后取消、chmod/读回失败：不能报零写入；按 receipt 条件恢复，失败保留 intent。
- 最终登记失败：新树保留，pending 覆盖旧 written；resources 与 skills 共用，不各造恢复分支。
- 恢复 manifest 校验目录类型、空目录、链接目标；保留既有来源 skillTreeHash 语义，另加恢复结构验证，不悄改所有来源 hash。

### 补偿与释放

CompensationHandle 改成 OpID+完整 target+before/after 指纹+持久位置，内存正文仅缓存。HasUndo 不表示一定恢复成功。

- Compensate 持 applyMu，逆提交顺序，先保存 compensation intent（ParentOpID 指原操作）再恢复；不递归补偿自身。恢复内容与基准，但保留当前授权/选择意图。
- 使用 `context.WithTimeout(context.WithoutCancel(requestCtx), 10*time.Second)` 作为一次恢复总预算，每步至多一次。取消停止业务，不阻止事实登记/恢复；预算耗尽留 pending。同步 UNC 调用无法被 ctx 强制打断，不能用 goroutine 超时返回留下失控写者假装完成。
- 新增 `ReleaseCompensations(handles...)`。它仅解除已终结操作的撤销租用，不表示撤回；未确认操作不可清材料。settled 表处理多个已确认操作，后一次写不会抹掉前一次终结事实。旧句柄补偿遇后一次内容必须 skipped，不能恢复旧版本。
- remote effect Abort→Compensate，Commit→Release；不能先清 e.undos 再丢未决责任。desktop/manual/save 无外层 Abort 的成功调用结束 Release，失败交持久 pending 管理。shared gate 拒绝时也处理本次 receipt，不能只扔 error。
- Reconcile 不能清理本进程仍被活跃句柄租用的前像。新进程无活跃租用时，settled committed 表示供给已完成，保留目标只清材料；未 settled 不能因 target==after 静默记成上次全部成功。

## PR05：reconcile、撤回与失败表

新增内部 `ReconcilePending(ctx,target)`，RunUnits 在 Plan 前持 applyMu 调用；Apply 写前再确认没有新的同路径冲突。只处理请求目标的登记路径，不唤醒其它 distro；GetStatus 仅展示。未决记录不能被同路径新操作覆盖；正常已 settled 且仍租用的句柄不算未决。

| D（磁盘）/T（目标） | M（内存/展示）及下一步 | 恢复权/启动 |
|---|---|---|
| 无 intent，main 健康，无副本线索 | 正常未发布 | 普通可选失败不全局阻断 |
| intent 保存失败，T 未动 | prepare-state-failed | 不授新写权；既有敏感消费仍独立核验 |
| intent 在，T==before | 核实 not-applied/restored，保存 settled 再清理 | 不报新版本完成 |
| intent 在，T==after，无 settled | committed-unregistered | 首发也阻敏感消费，直至当前核验+登记或撤回 |
| intent 在，T 未知/不可读 | commit-unconfirmed | 不自动重写/删除；保留前像 |
| settled 在，intent 仍在 | 已完成但清理未完 | 不重复提交/补偿；保留副本事实 |
| main 持续 save 失败 | 保留旧 LastSuccess，最新 pending/failed | 不无限创建同路径 intent；不报旧 written 为最新成功 |
| main/pending 不可读 | store-health 错误，停止新写 | 不能退回空登记；当前敏感路径不可证则拒绝消费 |
| T 后改 | freeze/conflict | intent 不赋覆盖权，须显式新预览 |

VisionExportAdapter.Plan 不再单靠 LastWrittenHash 判断 recorded。Coordinator 在规划前处理 pending；对已核实 after 的未登记事实可提供清理基准，但不能伪造已同步登记。来源非 ErrNotExist 仍是 source-unavailable，不猜撤回。

来源 ErrNotExist 时：

1. 可靠 ENOENT→确认消费路径 absent；不恢复旧秘密。
2. T 等于 durable last-written 或有效 pending after→有狭义清理权，保存 remove intent 后条件移除/隔离并复验 ENOENT。新授权缺失不取消清理已发布副本的责任，但不得借此首次导入。
3. T 后改/不可读/仅 legacy 疑似→withdrawal-unconfirmed，不删未知内容或混合配置。
4. 删除后 EACCES/I/O 不是 absent。已证 absent 但 final save 失败→removed-state-unpersisted，留 remove intent；当前请求可在本次资格核验中允许“不消费此副本”，但 UI/DirtyProgress 不报整项完成，重启仍重验，禁止旧 write intent 复活秘密。
5. 前像/displaced 也可能含旧秘密。分别展示“消费路径撤回”和“私有材料清理待确认”。只有恢复材料权限安全、不会被 CLI 自动发现，才能按当前路径 absent 放行；否则仍阻敏感消费。不能宣称全部秘密删除或 token 吊销。

remove 也用保留 displaced、再核验、不覆盖的原语，未知 displaced 不删。`clearSatisfiedDirtySources` 不再把所有 WarnDegraded 一律计成功。

### 每个可失败边界

| 边界 | M / D / T 失败事实 | 必须行为 |
|---|---|---|
| 身份/授权/CAS | blocked / 原记录 / 原目标 | 无新写；已知风险仍在 |
| staging 隔离 | prepare failed / 无有效提交意图 / 原目标 | 不把秘密写入未验证目录 |
| backup 写、Sync、Close、mode、内容 | invalid backup / 最多准备记录 / T 仍唯一前像 | 不移动 T，坏备份不能替换 |
| candidate 准备/验证 | prepare failed / 无有效发布 / T 原样 | 有界清理并报告残留 |
| intent save/readback | commit 未获准 / 无或未知 intent / T 原样 | 不先写后补记 |
| move 返回失败 | effect unknown / intent / 重新核验 T 与 prev | 不猜未写，不丢材料 |
| move 后取消 | cancel+recovery / intent / T 缺失或新外部目标 | no-replace 恢复，失败留 prev |
| 发布失败/不可判 | failed/unknown / intent / 实测 T | 不删未知 T |
| 发布后内容/权限失败 | unverified / intent / T 可能已改 | 拒消费，条件恢复或人工处理 |
| final state save | committed-pending / intent+旧或未知 main / 新 T | 首发事实不丢，重启可见 |
| 恢复 backup 再验失败 | recovery unavailable / intent / T 不再动 | 不先腾位、不报 preserved |
| 恢复落位前外部新建 | conflict / intent / 外部 T | no-replace 拒绝，保留两处 |
| 撤回后不能证 absent | withdrawal unknown / remove intent / 未知 | 阻敏感消费 |
| 撤回已证 absent、save失败 | absent+pending / remove intent / absent | 不复活，登记不报成功 |
| cleanup 失败 | residual / settled或intent / 已知目标 | 不重复业务、不无界删材料 |
| 外部命令中取消 | canceled+可能副作用 / 摘要 / 实测或未知 | 不执行后继写，不宣称回滚/子进程已回收 |

## PR01：legacy 核验与用户解除

### 可用事实与迁移

v1 entry 有 generation、LastWrittenHash、Consented、Detached、LastOutcome；只有顶层有最近 distro/home/generation，没有逐 entry 的 distro/UID/Home 明文。旧算法为 `sha256(machine-id + "|" + uid + "|" + home)`；本目录 `core.patch:1073–1095` 删除行证实算法，不把 patch 当现状。当前 `coordinator.go::generationFor` 包含 distro。

ResolveTarget 同次 Identity 可得到 machine-id/uid/home。给 TargetRef 增加内部 `LegacyGeneration` 摘要，不重新走 cached Home 探测；machine-id 缺失时仍 unknown。

- v1 原 entry 与顶层线索保真保存，再隔离授权；顶层只算“最近检查”，不算 owner。
- Detached、确切 completed withdrawn/withdrawn-already-absent 不重建受管风险；不能用 withdrawn 前缀匹配吃掉 unpersisted。单有 Consented 无写基准不伪造已发布事实。
- v2 LegacySensitiveRisk 是旧修复派生字段，可能误归属；与隔离 entry 一起重建 evidence，无法回溯时保留低可信线索，不继续盲信 map[distro]、不直接丢弃。
- 新 v3 处置按完整 target+路径记录。A 同步/撤回不能全局删掉未归属线索，也不能解除 B。相同旧指纹克隆都是候选，不确认为唯一 owner。
- 匹配只带来风险核验，不迁移 consent、selection、写权。

### 当次资格核验

新增 `AssessSensitiveConsumption(ctx, requestedTarget, unit, runFacts)`，在 effect/启动门运行；只核验当前目标固定视觉清单路径，不扫所有 distro，不返回 hash/正文。

| 线索与当前文件 | 判定与动作 |
|---|---|
| main 健康，无 legacy/pending/旧代副本线索 | 正常未受管，保持从未启用目标普通启动 |
| 旧指纹匹配、文件 hash 匹配 | 当前有旧发布字节线索，阻未授权消费；不自动写/删 |
| 旧指纹匹配、文件不同 | 可能手改旧副本，pending/conflict，不授删除权 |
| 旧指纹不同、文件 hash 匹配 | 当前路径有旧发布字节，不证明是原 owner；只关联本目标风险 |
| 当前文件可靠 absent | 本次无该消费副本；放行本目标，仍保留全局 unresolved evidence |
| 旧指纹完整且不同、当前文件可读且不同、main健康、无其它关联线索 | 未发现该目标相关受管副本，不无差别误标；按用户自有保留，不给写权。只对当前路径成立，不声称其它 Home 全量撤回。 |
| 旧 generation 缺失/unknown/非可识别摘要，或身份未知；文件在/不可读 | 无法排除当前消费残留，只对当前路径要求确认，不盲绑其它目标 |
| 未解析可信 Home/路径 | 不猜默认 /home/u；恢复身份探测后核验；已知风险不能因探测失败消失 |

旧指纹相异不能证明旧 Home 从未遗留秘密；这里只裁决当前会话消费路径。没有历史线索的普通可选失败不升级为全部 WSL 禁用。

### 明确的解除入口

当前 VisionExportAdapter 非 KeyedAdapter，TakeOverUnit 不能接管它；KeepWSLPiConfigUnitLocal 只接受 Pi 三单元。不能只写“请重新接管”却无入口。

本轮采用最小可执行解除：**用户手动处理指定文件→当前目标“检查/启动”重新核验 absent**；错误给精确 distro/Home/文件名，状态明确尚未撤回。新增窄 `ConfirmLegacyVisionAbsent(ctx,distro)` 服务入口，复用目标解析与现场核验，持久记录该完整身份/路径处置；保存失败返回未持久确认，不删除原 evidence。App 现有检查动作接入此核验，Wails 变更按规则生成 bindings。此路径不覆盖未知文件、不需要新增视觉 takeover 产品面。

现有新代 GrantConsent 保持授权动作，不能仅凭它清风险；当前目标已有未知文件仍需用户处理。现有明确 Detached 终态保真、不重新制造风险。新增“保留本地认领/接管旧视觉文件”按钮不属于本次最低闭环，不得假称已具备；若 Leader 后续选择增加，它必须新预览CAS+完整目标绑定+显式认领/新授权，不能变成忽略所有 legacy 风险开关。

## PR02/PR07：A2 统一权限门与真实 ctx

### 所有消费出口

算法主体只产生 provisional result，最终经新增 `finishA2Consumption(ctx,target,outcome,cause)` 返回：

- Copied、Fresh、DistroNewer、DistroTieKept、DistroHasCopy 全进入同一门，分支不能直接返回可消费 outcome。
- 门检查 ctx→当前 auth 可读常规文件→StatMode；非600/unknown 则 chmod600→再 StatMode；最终不可证返回 NoAuth。errors.Join 保留同步原因与权限原因，健康600保持零字节重写。
- cancellation/deadline 一律 NoAuth/失败，不由 classifyWriteFailure 按“存在”转 HasCopy；后者只能产生 provisional。
- 成功回写后的门失败，不回滚已同步新 token；报告方向性实际写入，本次不启动。安全权限不证明 token 有效。
- mtime失败维持不复制两侧内容；为消费而收紧权限是独立安全动作，修正“任何一侧零写入”的绝对注释。
- auth staging 在 rename 前增加字节读回验证，持续污染时不替换旧 auth。A2 不建立普通旧 token 回滚链、不持久化 auth hash；提交后失配报 NoAuth，不能用旧 token 恢复。

### 接口与真实执行链

接口固定为：

- `A2Adapter.Sync(ctx context.Context, target TargetRef) (launcher.SyncWSLPiLoginAuthOutcome, error)`；所有实现/stub 同步编译期调整。
- launcher 新增 `SyncWSLPiLoginAuthContext(ctx context.Context, target WSLAgentTarget) (SyncWSLPiLoginAuthOutcome,error)`。target 补完整身份核验所需字段，不依赖 wslsupply 类型，避免循环依赖。
- DefaultA2Adapter 从 Coordinator 已解析目标构造 launcher target，不用 cached WSLUserHome 重新选 Home。旧 SyncWSLPiLoginAuth(distro) 保留 Background 兼容包装，但生产协调器只走 Context 版；兼容包装同样过最终权限门。

ctx 必须贯穿：Coordinator Identity/Apply复核→A2目标复核→宿主读/stat→distro mtime→staging/retighten 的 chmod/stat→Windows 与 WSL 两个原子 writer 的实际 rename 前。每个同步 I/O 返回后再查 ctx；已经发生的写入只记事实，不开展后继业务。

平台新增 ctx 版 WSLTargetIdentity/WSLFileMTime/WSLFileMode/WSLChmod 与 wslScriptRunnerContext，真实使用 exec.CommandContext，保留 --exec、quoting、SuppressConsoleWindow。旧函数包装 ctx版保持其它消费者兼容；新增 OS 实现按分文件规则。Probes 的 Identity/Chmod/StatMode 改带 ctx，不能回退无 ctx probe 或 goroutine 包旧函数冒充取消。

UNC 映射/同步 os.Stat 可前后检查，但不能声称强制中断阻塞 I/O。取消 wsl.exe 不证明 Linux 子进程已退出；真实存活/后继写待 Windows 验证。不得以“快速单文件操作”省略这些调用。

A2 在首个可能改变 auth/权限的动作前保存不含 hash/token 的 pending 摘要（OpID、完整目标、方向/阶段、可能副作用）；复用 pending 存储、单独 Kind。结束保存结构化结果及 settled；recordA2State 保存失败返回 observation，不只 log。重启重新执行 A2 真实判定，不按摘要重放旧 token、不按摘要授消费资格。摘要写失败时不开始新的同步写；无写的健康 Fresh 仍须当次权限核验，并报告状态保存问题。

### 启动聚合

classifyA2 不接受成功 outcome+cancellation error 为 Applied。`verdictForWSLLoginSupply` 先确认本次 A2 资格，再处理可选项；不能因前面的可选 Failed/awaiting-consent 提前 return 跳过 A2。当前 `pi_login_launch.go:175–211` 早退顺序是接入必须覆盖的直接组合风险。

shared gate 消费统一 assessment：本次 committed/unknown、pending、unsafe/unknown 不可被 `!visionManagedCopyPossible` 放行；Applied/Fresh/WarnDegraded 也不能跳过已知风险。请求了敏感单元却缺结果/资格能力，不是安全证据。裸 runner 不注册该单元仍可能力过滤，但生产注册后必须返回资格。普通 modality 失败、确无副本线索继续放行。

## PR07：插件后继提交

原生 install/remove 是外部已授权副作用，和 filters 本地写分账，不把插件安装伪装成可回滚文件事务。

- runScript 获 opMu 后再查 ctx；runner 返回 nil 但 ctx 已结束仍 canceled，不执行后继 remove/install/filter。
- applyDistroFilters 在读后、解析后、tmp写前、tmp完成后、最终移动/发布前检查 ctx。取消清本次 tmp，不改原 settings；若已提交才取消，返回 filter-committed/verification-pending，不声称零写。
- Coordinator RunPluginOperation 持 applyMu 覆盖一次操作和身份复核，内部 opMu 保持 runner 串行。这样 filters 与 pi/settings 投影共用实际写锁；接受显式安装暂时阻塞供给的代价。锁序 applyMu→opMu→短 state.mu，state 锁不跨网络/UNC I/O。
- settings 最终发布前重读仍须等于构建基准；保留 packages 外字段。使用统一文件提交/恢复原语，插件传入内部提交回调，禁止另造 tmp+无条件 rename 旁路。
- 首个改变状态的 pi 命令前保存插件 pending 摘要（完整目标、source/ref/filters/OpID，不含秘密）。首次 hasReg=false 失败也保留可能副作用事实，不能重启后完全失去来源；摘要不自动授予管理权、Installed=false、待显式复检，不自动重放。
- install完成→filters前取消：canceled + native-replay-may-have-applied/filter-not-applied，非OK/Installed；rename后取消：canceled + filter-committed/verification-pending。主清单save失败保留 pending。
- 取消后 recoveryCtx 可登记事实/有限读回，不继续业务写。Probe/RegistrationSummary pending 优先于旧 installed，不能说原生副作用已回滚。
- nil/[]/显式全nil 的 PR08 过滤语义原样保留，不能跳过 filters 凑取消测试绿。

## 具体文件/API 调整清单

| 文件/函数 | 调整内容 | 禁止旁路 |
|---|---|---|
| `state.go` | v3、LegacyEvidence、LastOperationID、SettledOperations、StoreHealth、pending存取、深复制；私有写读回 | load错→空；第二缓存；只内存pending |
| `RunUnits/Apply/TakeOverUnit` | reconcile、CommitRequest/Receipt、entryFor、错误receipt保留 | 提交后才知道unit；error丢Undo |
| `submitFileCtx/submitSensitive` | 写前验证backup/candidate、intent、displace/no-replace、final登记联动 | 坏备份仍换目标；普通路径无最后ctx |
| `recoverSensitiveCommitMismatch/preserveSensitivePreimageBackup/restorePreimage` | 分类既有receipt；前置保全；独立非递归恢复 | 错后首次备份、只验mode、取消丢前像 |
| `applyWithdrawal/recordProjectionWithdrawn` | remove intent、精确ENOENT、pending after清理基准、related settled | 无hash→从未发布；withdrawn前缀吞pending |
| `SensitiveCopyPending/LegacySensitiveCopyPending/GetStatus` | 兼容只读包装；启动改assessment；状态合并pending/health | latest generation与旧hash独自决定安全 |
| `shared_files.go::Plan` | Coordinator先reconcile；unknown不当absent；detached零自动写 | 源缺失+无durable hash直接Fresh |
| `skills_submit.go/resources.go` | 真实prev树、结构验证、同一receipt/取消恢复 | 空目录Remove；恢复无条件RemoveAll |
| `compensation.go/Compensate/runner.go` | 持久句柄、逆序、有界恢复、补偿intent、Release | 内存唯一前像；丢待核实责任 |
| `a2.go/wsl_pi_login_sync.go` | ctx+目标、统一final门、无秘密摘要 | 成功Newer/Tie绕门；存在即HasCopy |
| `wsl_agent_config.go` | ctx helpers、候选字节预验、实际rename前检查；零写权限保留 | “快操作”省ctx；A2普通回滚 |
| `probes.go/platform wsl*` | ctx真实runner、窄no-replace分OS实现、旧API兼容 | 假取消、host mode冒充WSL权限 |
| `plugins.go/plugins_api.go` | 实际文件锁、后继ctx、共享提交回调、pending摘要 | 首次失败不留事实；runner nil掩盖取消 |
| `types.go` | result/status/consumption字段与默认unknown | 默认零值safe |
| `app_wsl_supply.go` | 统一gate、风险优先UI、ConfirmLegacyVisionAbsent接检查 | capability缺失=false；WarnDegraded全放行 |
| `pi_login_launch.go/launch_executor.go` | A2优先资格、Abort/Commit持久句柄、shared gate receipt | optional早退漏auth；e.undos先清 |
| tests/stubs | probe/A2/Runner签名、公开入口矩阵、深复制新字段 | 私有helper自证、降低断言 |

launcher 不能 import wslsupply。A2 helper 留 launcher；事务在 wslsupply；OS文件移动在 platform。此处不要求把所有现存 writer 提升为通用新服务。点名之外的旧 writer/转换器问题不顺手重构。

## 验收矩阵：公开入口到文件与会话

fixture 全用临时目录与合成秘密，不读真实 auth。故障 seam 分 Prepare/Intent/Move/Publish/Verify/Finalize/Cleanup，支持持续失败、精确取消；不靠 sleep 或所有权限身份下未必失败的 chmod0500。

| 入口/场景 | 文件与磁盘断言 | 返回/会话断言 |
|---|---|---|
| v1 A发布→检查B→重建Coordinator→启动A/B | 真实旧算法摘要与不同identity；A旧文件、B absent；evidence保留，新consent=false | A待处理、B放行；不盲绑B，不仅用old-A假串覆盖迁移 |
| 同旧指纹克隆A/B | 逐目标核验，解决A后B证据仍在 | A操作不解除B、不授B写权 |
| legacy无distro/unknown代 | evidence跨重启；当前absent/present/unreadable分组 | absent放行；present待手动处理+确认；不全局禁WSL |
| withdrawn/detached/consent-only | 不重造受管风险；pending withdrawn不是终态 | 无误阻断、无授权继承 |
| 首发intent持续save失败 | target不存在；已有目标逐字不变 | Failed，无written；不先写后记 |
| 首发final save失败→GetStatus→重建→关闭源→RunUnits | intent在、目标新内容、旧consent与pending分开；恢复存储后撤回 | 首次gate拒绝，非consented/已同步；不能Fresh且secret remains |
| 曾成功→更新save持续失败→重启 | 旧LastSuccess保留，最新pending；同路径不无限新intent | 不展示旧written为最新成功 |
| final rename成功但readback/返回失败 | 可能已有settled，重建按磁盘查证 | 不重复业务/误补偿 |
| crash：intent后/move后/publish后/verify后/main后/cleanup前 | 新实例无旧Undo；前像、目标、intent证据各阶段可读 | 重启不变“无历史”，资格不靠旧outcome |
| 两次成功同路径、前次句柄尚活跃；随后重启 | 两操作settled不互相丢；后次内容保留 | 旧Undo skipped，不旧材料误判pending/回滚新内容 |
| write未登记→withdraw成功→崩溃 | Parent/Related settled记录完整 | 旧write intent不复活已撤回秘密 |
| 每次backup/candidate staging均污染 | backup坏时T原字节不变，尝试有界 | Failed，无preserved/restored假声明 |
| backup健康→publish后失配/权限失败/取消 | 原前像持久字节相等、权限probe可见，未知T保留 | Failed/canceled+恢复位置，非Applied；重启仍有事实 |
| backup在恢复前损坏 | 当前T不被先删/覆盖 | recovery-unavailable，不能宣称有效备份 |
| 外部写：入口前/CAS后/displace后/恢复落位前 | 后改字节在原路径或明确保留位；新T绝不覆盖 | conflict/skipped，非confirmed，位置可找 |
| RunUnits→Compensate，ctx已取消 | 独立有限预算；实际恢复或持久pending；A2不动 | 会话不启动，恢复责任不消失 |
| Preview→TakeOver，空/非空目录move后取消 | 正常no-replace路径恢复原树；持续故障保留prev，空目录也在 | 不只报canceled而隐去缺目标；不能假称恢复成功 |
| resource文件/目录取消/save失败 | 与共享原语同义，结构/链接/空目录保真 | 不能只用skills单例代替 |
| 撤回remove后save失败/读取I/O错 | absent与unreadable分开；remove intent在；备份残留独立 | 不复活；不可证则阻；不计dirty完成 |
| 来源关闭但T后改 | T原样，不删混合文件 | withdrawal-unconfirmed且gate拒绝 |
| A2 Fresh/Copied/Newer/Tie/HasCopy × 600/644/unknown/chmod失败/复验失败 | Newer/Tie确实成功写Windows；全部出口过mode门；Fresh mtime不变 | 只有可证权限可消费；其余NoAuth，原错误保留 |
| A2 identity/mtime/stat/chmod runner中取消、两侧rename前取消 | 真实ctx接缝退出，未提交侧前像原样、无后继写 | NoAuth/canceled，PTY Start=0，不降级HasCopy |
| A2 rename后取消 | 新token保留，无Undo/无hash/token落状态日志 | 不启动、不回滚新token |
| login可选失败/未授权先于A2失败，多种结果排序 | 真实result组合 | 全部Start=0，不能optional早退绕auth |
| Plugin install后runner nil但ctx取消 | native可能有实体、filters不后继写、pending跨重启 | canceled，非OK/Installed，不自动重放 |
| filters读后/parse后/tmp后/rename前取消 | 原settings保留，tmp清理或残留可见，nil/[]语义不变 | filter-not-applied，不绕过真实过滤 |
| filters提交后取消/save失败 | 新settings+pending，旧main不冒充新成功 | committed-unconfirmed可见 |
| shared gate+desktop/remote/restart effect | 真实Coordinator，失败Start=0、session非running | 五CLI共同门不可绕；非敏感失败无历史仍放行 |
| 并发Plan/GetStatus/Apply/Compensate/plugin写 | 新maps深复制、实际写锁无覆盖/死锁 | race回归，不以race绿替代事务断言 |

L2 后续执行（本轮未运行）：

- 将复审反例转正，新增语义测试文件如 `commit_recovery_test.go`、`legacy_evidence_test.go`、`a2_consumption_test.go`；保留历史 repro/日志原文，不重写旧证据。
- `go test . ./internal/wslsupply ./internal/launcher ./internal/platform ./internal/piplugin -count=1`；相关包 `-race`；`go vet ./...`。按规模拆批，长命令遵守进度纪律。
- Windows交叉编译只记compile；App返回/动作变动需生成bindings并做相关frontend测试/build，禁止手改wailsjs。整仓最终回归由Leader安排，上游基线失败不能改称全仓绿。

**Windows/WSL真机仍未通过。** 本轮无shell、构建、测试或Windows脚本。后续需真实UNC/9P私有权限完整窗口、no-replace/目录移动、持续I/O故障与取消前像、外部改写竞争、wsl.exe取消后的Linux子进程/后继写、desktop/remote/restart实际CLI消费。fixture mode只证明调用顺序；交叉编译不证明Windows权限/运行。旧退役脚本不可复活。

## 实施依赖、真正独立面与回滚

| 切片 | 前置/交付 | 独立性 |
|---|---|---|
| 状态与提交基础 | 冻结receipt/intent/settled/health/资格；pending存储、no-replace、文件/目录恢复 | PR03+PR05+目录PR07不可按finding各补分支，必须同一责任面 |
| A2权限/ctx | 平台ctx接口冻结后改launcher/A2和成功出口矩阵 | 算法与测试可并行，Coordinator/摘要接线等基础接口 |
| legacy核验 | Evidence/Target线索/assessment冻结后改迁移、核验、absent确认 | 算法fixture独立，state.go与gate需基础归并 |
| plugin生命周期 | ctx/摘要/提交回调接口后改runner/filter | 与A2流程独立，共享锁/state不能各造一套 |
| App门与状态 | 消费稳定接口、接Abort/Release、状态和检查 | 不可用bool stub假完成，必须真Coordinator |
| 对抗验收 | 公开入口先写失败反例，再补持续故障/重启 | 设计独立，PASS等整合；真机另有环境依赖 |

执行顺序：反例转正→公共类型/持久顺序冻结→统一提交/恢复→A2与legacy/plugin独立逻辑→App资格统一→L2与独立增量审核→Windows pending。测试先行不降低断言。

v3兼容读v1/v2、迁移幂等、原evidence保留。当前旧二进制会把更高schema当空，因此禁止直接降级读取v3。回滚先停受影响自动写，保全v3/pending/backup；无未决操作后才可经明确授权导出兼容旧格式。删除字段、还原旧state快照不能消除已发生写入。代码回滚不等于秘密撤回，A2 token禁止回旧快照。

代价：额外intent/settled与前像I/O、字节读回、权限探测；插件显式操作扩大applyMu时间。容量不足拒绝新的破坏性提交，不自动淘汰唯一前像；性能优化不能缓存掉身份/权限或跳过前像验证。恢复材料可能长期保留，状态必须可见，不假称全部清理成功。

## 本轮验证、自检与产物

- 四份指定输入完整读完。任务点名 `internal/wslsupply/{coordinator,state,shared_files,skills_submit,a2,plugins,compensation}.go`、两份launcher文件、`app_wsl_supply.go` 及三个review_fixes测试文件均完整读取。补读types/probes/plugins_api；resources提交段、platform探测段、login verdict/executor补偿消费者按用途读取。
- `core.patch` 只读旧generation片段，未宣称全量审计历史patch。一次候选 `internal/wslsupply/plugins_coordinator.go` ENOENT，依据实际名称改读 `plugins_api.go`，未据错误推断功能不存在。
- 自检完成：根因可解释复审反例；持久顺序覆盖M/D/T、首次/后续save失败、持续故障、取消、重启、撤回；legacy不扩权且有可执行解除；A2真实ctx与消费出口、插件后继边界、API消费者与拆分依赖明确。
- 本文是实施契约，不宣称代码已修复、独立复审PASS、Windows真机通过。无测试命令退出码、无视觉结论；OS原语与UNC保证仍待实现/真机验证。任意外部不协作写者、证据全毁、无界I/O故障不在自动恢复成功保证内。
- 本轮仅写此指导artifact，未写项目代码、配置、凭据或Git现场。撤回本轮只需Leader处理本文件，不涉及源码回滚。
