# WSL 目标闭环：集成阶段 full 审核

VERDICT: FIX

**8 Major、2 Minor；无 Critical。** 结论针对 `58e7577 → 当前工作区` 的本批生产改动、集成接线与新测试，不是全仓安全认证，也不是 Windows/WSL 真机通过。已确认多数原始反例得到修正，但敏感旧副本、零变化权限、恢复失败、目标分区操作、状态持久化及取消仍未闭环。本报告一次汇总，供 Leader 成批回派。

## 1. 基线、输入与证据口径

完整读取必读材料（均位于 `agent-outputs/workflow/wsl-goal-closure/`）：

- `workflow.md`、`direction.md`、`supply-audit.md`、`runtime-audit.md`、`verification.md`。
- 接收 Leader steer 的 race 线索，并读取 `integrated-race.log:640–904`，独立复跑其中产品 race。
- 未以 C1–C5 实现报告的完成声明作为验收依据。

基线 HEAD 实查为 `58e7577`；开审 tracked diff 为 59 files / 4814 insertions / 670 deletions，另含任务列出的未跟踪生产/测试文件。源码审核以相关生产 diff 和实际入口、消费者、失败路径为主；新旧测试按验收/反例抽读并执行，不声称逐行读完每个测试。full 覆盖维度见 §4，不意味着所有外部 CLI/平台组合已执行。

证据分级：**R**＝本机真实产品 API + 合成目录/注入平台探针复现；**F**＝明确故障注入复现错误路径；**S**＝静态调用链证实。R/F 均不等同于真实 UNC、WSL 权限或 CLI 消费证据。没有读取真实密钥，没有运行真实凭据/WSL E2E、安装或网络插件重放。

## 2. 本批 findings

### PR-01｜Major｜shared gate 把未知状态放行，且 v1 隔离会隐去实际仍在的旧密钥副本

- **定位**：`app_wsl_supply.go:234–260,269–280`；`internal/wslsupply/coordinator.go:325–334,896–926,970–1016`；`internal/wslsupply/state.go:233–250,310–318`。
- **触发与失败链**：
  1. 已授权/曾发布的视觉副本仍在，供给返回 `UnitBlocked/source-unavailable`、`target-unresolved`、`inspect-failed` 或 `target-changed-since-planning`。真实 `RunUnits` 把这些异常包装成 **Blocked + nil error**，而 gate 对 Blocked 只识别 `withdrawal-unconfirmed`，其余全部 continue；`visionManagedCopyPossible` 根本不参与此分支。并非所有未知都进入失败分支。
  2. 用户从 v1 升级，随后关闭宿主导出。迁移隔离旧授权是正确的，但旧文件并未因此消失；新代没有 consent，Plan 在适配器撤回判定之前直接返回 `awaiting-consent`。新代状态又隐藏 legacy 登记，gate 放行。旧密钥文件继续留盘，五 CLI 任一启动仍可被工具消费。
- **影响**：无法兑现“源撤回/目标未知后不得静默消费受管秘密”；当前统一接线使桌面、remote/restart 同样承受此判定漏洞。问题不是要求所有可选配置失败都阻断。
- **证据 R+S**：`phase-review-repro.go/.log`：`LEGACY_WITHDRAWAL outcome=blocked detail=awaiting-consent old_secret_present=true visible_consent=false`；另有 `MANAGED_SOURCE_ERROR outcome=blocked detail=source-unavailable consented=true`。App gate 放行由上述分支直接证实；脚本没有冒充启动真实 PTY。
- **最小修复方向**：分离“当前写入授权”与“历史敏感副本待确认”事实；legacy 不继承授权，但保留有归属线索的残留风险/待人工确认状态，不能用未授权推导无旧副本。对未知/failed/blocked 只在可证明没有受管副本时放行；明确的用户独立副本选择另记，不自动删除未知归属文件。
- **回归点**：v1 written→升级→关闭→五 CLI gate；当前目标身份探测失败；源读失败且旧副本在；无历史登记/无副本的普通启动仍放行；旧代与新代/换用户严格区分。
- **归因**：SA-02 迁移与 SA-09 新 gate 的组合缺口；不是新授权跨代复用。

### PR-02｜Major｜敏感 Fresh 路径仍能放行宽权限/权限未知副本

- **定位**：`internal/wslsupply/coordinator.go:426–429`；`internal/wslsupply/shared_files.go:139–141`；`internal/wslsupply/managed_models.go:87–91`；`internal/launcher/wsl_agent_config.go:184–198`；`internal/launcher/wsl_pi_login_sync.go:133–141`。
- **触发**：视觉导出/托管模型字节未变但文件权限被改为 0644；或 launcher 的相同字节分支里 `StatMode` 失败。
- **失败链**：协调器 ActionNone 直接 Fresh，不查权限；launcher `wslRetighten600` 更明确将 `!ok` 当成功，Pi/OMP/OpenCode 相同字节 writer 和 A2 Fresh 因而可成功放行。首次 staging fail-closed 并未覆盖这些入口。
- **影响**：已有密钥副本可在权限不安全或无法确认安全时被会话继续消费；“没有新写字节”不能证明已有副本安全。
- **证据 R+S**：`phase-review-mode-repro.go/.log` 先用真实 VisionExportAdapter 发布合成数据，再本地 chmod 0644，第二次真实 RunUnits 返回 `UNCHANGED_SECRET outcome=fresh mode=644 permission_probe_calls=0`。launcher 的 `!ok → nil` 为静态直证；未声称本机 chmod 等价于 Windows 9P。
- **最小修复方向**：敏感单元无变化也做目标侧权限验证/安全收紧；首次探测失败不得成功，允许尝试 chmod 后复验；仍不可验证则向启动 gate 返回敏感不安全结果，不删用户混合文件。A2 被选择消费的其它现有副本分支也需统一核对，不仅 Fresh。
- **回归点**：shared/model/A2 同内容 0644、首次 StatMode=false、chmod 成功但复验失败、健康 0600 零字节重写；旧副本保留但不被不安全放行。
- **归因**：SA-06 修复未完整覆盖，launcher 的 unknown 放行是本批新增路径。

### PR-03｜Major｜敏感读回失败的恢复再次递归提交，恢复失败可删除含用户内容的整个文件

- **定位**：`internal/wslsupply/coordinator.go:1520–1549`（尤其 `submitSensitiveRestore → submitSensitive`）。
- **触发**：已有混合配置；rename 后读回不一致/不可读，恢复时读回仍异常，或恢复前 ctx 已取消。
- **失败链**：读回失配调用 restore；restore 重新调用带同样恢复分支的 submitSensitive，因此每次恢复失配继续递归，无次数上限。某一层因取消/其它故障返回错误后，每层无条件 `os.Remove(uncPath)`，未判断路径当前是否仍为本次写入，错误还直接声称 target removed 而忽略删除错误。即使只失败一次，恢复也没有保护读回时看见的后继用户修改。
- **影响**：故障路径可无限重试/递归，或删除整份 models/mcp 混合配置及用户字段；不能用保秘密为由无条件删不属于本次写入的内容。
- **证据 F+S**：`phase-review-restore-repro.go/.log` 在 mode 探针接缝修改 staging 合成内容，制造真实 post-rename hash mismatch；第 4 次取消以界定执行，得到 `staging_attempts=4 ... mixed_user_file_deleted=true`，嵌套错误清楚显示递归恢复。此为错误路径故障注入，不是 Windows 自然竞态复现；未实际制造栈溢出，故不报 Critical。
- **最小修复方向**：恢复使用有界、非递归提交；异常时先保留/隔离前像与诊断，只对仍匹配本次提交的目标做条件恢复/删除；恢复自身失败不能再递归或删整份未知现状。清理错误真实传播，区分 confirmed/indeterminate。
- **回归点**：一次/持续读回失配、恢复期间取消、恢复失败且目标被用户后改、混合配置、删除失败；执行次数有上限且用户字段不丢。
- **归因**：SA-07 本批恢复逻辑引入/仍暴露的数据保全问题。

### PR-04｜Major｜多目标状态已分区，但 keep-local 操作仍修改全局“最近目标”而非传入目标

- **定位**：`internal/wslsupply/coordinator.go:1198–1244`；相同旧取法还见 `internal/wslsupply/resources.go:619–636` 的 SetUnitSelection。
- **触发**：A、B 都有登记；最近检查/运行 B 后，用户对 A 执行 `DropUnitRegistration("A", ...)`。
- **失败链**：函数没有使用传入 distro/cli 选择分区，而是 `st.entry(unit)`；这取的是全局 `st.Generation` 的最近分区。于是 A 仍跟随，B 被停跟随。SetUnitSelection 也从最近分区复制 Consented/基准等，不能认为只修读侧 entryFor 就已全链隔离。
- **影响**：对指定目标“保留本地”的授权边界失效；A 后续仍能继续写受管字段，B 的正常同步反而停止。无需同时写入或理论竞态即可触发。
- **证据 R**：`phase-review-repro.go/.log`：`KEEP_LOCAL_REQUEST_A A.detached=false B.detached=true`。
- **最小修复方向**：所有目标性 mutate 使用明确解析/已知目标的 generation 查找；不能使用全局 current。保留“停机也可停跟随”承诺时，可按目标索引显式选择待停的分区/拒绝歧义，不能停别的 distro。SetUnitSelection 禁止把其它目标基准/consent 搬入新目标。
- **回归点**：A→B→keep A，B 状态不变；选择/同意/停跟随分别在 A/B 上操作；同名换代、停机、legacy/unknown。
- **归因**：旧单槽 helper 未随本批 state v2 分区适配，是迁移暴露的回归，不是要求新增多 distro UI。

### PR-05｜Major｜投影内容提交后状态落盘失败仍返回 applied，内存状态仍显示 written

- **定位**：`internal/wslsupply/coordinator.go:642–658,804–827,1315–1320`；`internal/wslsupply/state.go:343–350`。
- **触发**：文件已提交，但本地清单 rename/写入失败（磁盘/权限/路径故障）。
- **失败链**：recordProjectionState 返回 void，只 log；调用者仍 UnitApplied；mutate 即使保存失败也保留更新后的内存态，GetStatus 仍 written 且无错误。重启后这些基准又丢失，与 UI“已同步”和后续冻结/撤回判断不一致。
- **影响**：状态不能真实反映不可恢复/未持久登记的供给；尤其敏感副本可能已落地但重启后无法归属，进一步放大 PR-01。不是要求回滚已经成功的密钥刷新。
- **证据 F**：`phase-review-repro.go/.log` 用候选阶段将**临时 fixture**清单路径替换成目录，令提交后的清单 rename 确定失败；得到 `STATE_SAVE_FAILURE outcome=applied detail=written memory_status=written error=""`。
- **最小修复方向**：记录函数返回错误，结构化区分 content-committed/state-unpersisted；状态页/调用结果明确未持久化而不是 synced。保留重试所需前像与待确认信息，不能用“下次写盘可能成功”替代当前结果。
- **回归点**：提交成功/清单失败、重启重建、部分键写入、接管路径、敏感撤回登记失败；确认插件已存在的 registration-unchanged 语义不退化。
- **归因**：SA-15 未关闭；并非原始“上次成功后这次失败仍显示成功”的唯一旧反例。

### PR-06｜Major｜stateStore.load 的浅拷贝逃逸锁，Plan 与 Apply/状态写存在真实生产 race

- **定位**：`internal/wslsupply/state.go:187–195,310–318,326–350`；`internal/wslsupply/coordinator.go:325,444,624,771`。
- **触发**：并发启动/保存/补偿导致多个 RunUnits；一个 Plan 读取 load 返回的 Units slice，同时另一个 Apply 在 mutate 锁内原位 upsert。
- **失败链**：load 虽持锁，但返回的 StateFile 仍共享 slice backing array 和嵌套 map；锁外 entryFor 读取与 upsertEntry 写同一对象。applyMu 只串行 Apply，不保护 Plan/GetStatus/插件读。
- **影响**：授权/归属/错误/基准快照不是一致视图，存在 Go data race；嵌套 map 同样未被 snapshot 隔离。没有把 race detector 输出夸大成已证实配置丢失/进程崩溃。
- **证据 R**：独立 `go test -race ./internal/wslsupply -run '^TestCompensateApplySerialized$' -count=1` **exit 1**，`phase-review-race.log`：write upsertEntry/state.go:332 vs read entryFor/state.go:316 → Plan/coordinator.go:325。此次是 recordProjectionBlocked；Leader 全量日志是 recordProjectionFailure，同一产品根因。此定向复现不依赖 fakeProbes slice 的另一条 race。
- **最小修复方向**：锁内深复制不可变读快照（Units、Subs、SelectedKeys、Targets/InspectedAt、pending、plugins filters 等），或统一受锁访问 API；不是只给测试加锁，也不是只复制顶层 struct。
- **回归点**：并发 Plan/Apply/GetStatus/Grant/MarkDirty/插件登记；相关 -race 干净，保留内容与目标隔离断言。
- **归因**：浅拷贝机制在 HEAD 已存在，本批新增状态记录/多分区并发继续暴露，属本轮承诺中的未关闭内核问题，不虚称全部由本次新增。

### PR-07｜Major｜取消保护只覆盖主 RunUnits；已取消的 skill takeover 仍实际落盘

- **定位**：`internal/wslsupply/coordinator.go:1252–1256`；`internal/wslsupply/skills_submit.go:140,178,225,316–354`；`internal/wslsupply/coordinator.go:122,157`。
- **触发**：合法技能已授权、目标不存在；将已取消 ctx 交给公开 TakeOverUnit（或技能操作读候选期间取消）。
- **失败链**：技能分支绕过新增 Apply 取消门；ResolveTarget/Inspect 忽略 ctx，applySkillUnit 参数为 `_ context.Context`，submitDirUnit 无 ctx，取消后依旧生成并交换目标树、chmod、登记成功。
- **影响**：取消不阻止后继配置写入，SA-16 的公开供给操作未闭环。
- **证据 R**：`phase-review-cancel-repro.go/.log`：`CANCELED_TAKEOVER ctx=context canceled outcome=applied detail=takeover-written target_written=true`。无正式项目测试被新增。
- **最小修复方向**：skill/resource takeover 与目录提交携带并检查 ctx，至少入口/取锁后/每单元与不可逆交换前检查；探测 timeout 不应仅传入一个实际被忽略的 ctx。A2、插件后继本地写也应按同一生命周期核对。
- **回归点**：已取消 takeover、等锁期间取消、多文件候选期间取消；取消后不产生新的目标或安装；已完成的写入按明确补偿语义处理。插件 default runner 已有取消前短路，不能由此推导 Linux 子进程已经退出。
- **附带静态边界**：`plugins.go:1117–1124,1347–1352` replay 成功后 applyDistroFilters 不接 ctx；取消恰落在此窗口仍会做本地过滤写。此窗口未独立动态复现，不单列重大 finding。
- **归因**：SA-16 原有技能路径未适配，本批主循环修复不足。

### PR-08｜Minor｜插件 update 无法将旧过滤集重置回“全部加载”，却返回 already-ok

- **定位**：`internal/wslsupply/plugins.go:1315–1328`；`internal/piplugin/target.go:137–171`；`app_wsl_plugins.go:175–180`。
- **触发**：WSL 曾按 `skills: []` 等过滤安装；Windows 把所有过滤键撤掉成为纯字符串登记，再 both/update 同版本。
- **失败链**：EntryFiltersFor 合法返回全 nil（全部加载），runUpdate 却把 `!hasFilters(op.Filters)` 解释为“沿用 reg.Filters”，不能表达显式重置；幂等检查按旧清单通过。
- **影响**：Windows/WSL 过滤不一致，原来关闭的能力继续关闭且返回成功。此次是保守地少加载，不是扩大可执行面，按 Minor。
- **证据 R（隔离 runner）**：`phase-review-filters-repro.go/.log`：首次 installed，重置 update `already-ok ok=true`，真实 settings 仍为 `{"source":"npm:demo@1.0.0","skills":[]}`。runner 只生成合成 package fixture，没有网络/安装证据。
- **最小修复方向/回归**：区分“调用方未提供过滤选择”与“明确提供全 nil＝全部加载”，或让当前 App 调用统一携带完整期望快照；测试有过滤→全无过滤、显式空数组与字段省略往返、同版幂等。
- **归因**：本批新增目标侧 filters 应用的遗漏。

### PR-09｜Minor｜测试异步生命周期和 fake 探针另有 race，不能与 PR-06 混为一谈

- **定位**：`wsl_s2_remote_test.go:427,459–461`；`app_wsl_supply.go:327,348,364`；`internal/wslsupply/coordinator_test.go:48–52`；`internal/wslsupply/submit_primitive_test.go:217–220`。
- **触发/影响**：保存/启用测试启动后台 push 后未等待退出，下一个测试/cleanup 改全局 probe；并发 primitive 测试 fakeProbes 无锁写 chmodCalls/maps。使 race suite 红灯、相互污染验证可信度，不能据此说真实产品 probe 会被业务线程修改。
- **证据**：Leader `integrated-race.log:662–763,819–904` 已读取，属上游执行；本轮未单独复跑这两种 harness race。PR-06 则已独立复跑并隔离确认。
- **最小修复方向/回归**：fixture cleanup 等待 push 生命周期结束后再还原全局接缝；并发 fake 使用 mutex/独立实例；不删除并发测试、不跳过 race、不修改产品安全门凑绿。目标测试连跑和 -race 不跨测试泄漏。

### PR-10｜Major｜Windows staging 验证脚本越过授权边界且不能验证所宣称的产品原语

- **定位**：`agent-outputs/workflow/wsl-goal-closure/verify-staging-window.ps1:49–58,72–108`；文头前像故障注入/“当前构建”承诺 `:24–32`。
- **触发**：按脚本运行指引在已有 distro 上执行。与脚本是否最终 PASS 无关，开头即以 root 尝试创建固定用户 w2，并 `rm -rf /tmp/amagi-staging-verify`；没有唯一隔离根、既有资源归属核验或用户逐项授权。既有 w2 被复用，PASS 分支又无条件 `userdel -r w2`，没有“本次创建”凭据。
- **影响**：测试工具本身可修改全局用户/清除固定路径既有数据，不符合本轮禁止全局用户/安全设置改写的边界；不能交给主上直接执行。清理分支有删除既有用户及其 Home 的危险，**本轮未执行脚本，且不声称当前 PASS 分支已自然触发**。
- **证据 S**：已完整读取脚本。观测器完成输出 `NO-LEAK`，判定却用 `Contains("LEAK")`，成功完成采样也被判失败；采样被提前 Stop-Process 时也缺完整退出/覆盖证据。writer 是 shell printf/chmod/mv 复刻，并非实际 Go UNC writer；文头承诺混合前像 chmod 故障注入，正文没有实现。因此即便修判定，也不能据此签发当前构建 staging/恢复通过。
- **最小修复方向**：立即撤回/标为禁执行；重做为显式授权的现有隔离用户与唯一测试根，禁止创建/删除系统用户；记录确切 observer 生命周期与采样边界；驱动真实 Go writer（合成秘密、故障接缝），准确检查完整结果，不用子串 NO-LEAK 判 LEAK。保留既有用户/固定目录，不进行自动清理。
- **回归点**：既有同名用户/路径完全不变，拒绝未授权执行；NO-LEAK 不误报、未完成采样不可 PASS；真实原语失败注入与前像回读。Windows 清单必须先禁用旧脚本，不能用它补本报告真机缺口。
- **归因**：C2 本轮交付物新缺陷。Leader steer 的线索经独立读码确认；不是产品已发生的数据事故，故本次按 Major、不将理论 userdel 后果泛化为 Critical。

## 3. SA-01–18 / D1–D6 逐项核对

表中 **met** 仅表示对应缺陷的源码/L2 修复在本轮证据范围满足；**partial** 表示关键子路径仍不满足或真实消费者证据缺失。Windows 真机部分一律尚未 met，不能把本表作为发布通过。

| 原编号 | 状态 | 独立核对结果/证据 |
|---|---|---|
| SA-01 generation/旧计划 | met | SameIdentity 已含 generation；Apply 重探身份，applyProjection 重查 consent/detached；`TestApplyRejectsPlanAfterGenerationSwap` 所属包通过。最终跨界 TOCTOU 仍需真机，不扩泛化 finding。 |
| SA-02 身份/授权多目标隔离 | partial | 指纹包含 distro；entryFor 拒 unknown/legacy，插件联合键保存/删除已适配；PR-01/04/06 尚在。 |
| SA-03 选择键全链 | met | 全路径键匹配与唯一叶名兼容；Claude settings 加选择入口；空选择可提交；相关 root/wslsupply 回归通过。UI 实际操作未测，toast 以单元计数不等于逐键消费者验收。 |
| SA-04 Codex profile 权限 | met | 明确 whitelist，审批/sandbox/未知字段不导入；合并保留目标本地字段；包内回归通过。新版 Codex schema/真实 profile 激活仍待验证。 |
| SA-05 秘密别名 | met | 链上 hops/final 路径排除；读候选前 Lstat；合成别名回归通过。不代表 hostile Windows reparse/父目录替换竞态已真机覆盖。 |
| SA-06 私有落盘/权限 | partial | 新提交 verified 0700→0600→rename；PR-02 无变化路径不满足。私有 staging ACL/第二用户可读性尚无 WSL 实证；现有取证脚本必须因 PR-10 禁执行。 |
| SA-07 混合文件保全 | partial | staging chmod 故障发生在 rename 前，保前像回归通过；新 post-commit restore 仍有 PR-03。 |
| SA-08 single-flight | met | pushWSLSupplyOnce 已改 worker 循环消费 rerun，不再递归保持 running；root 回归通过。后台 fixture 生命周期另见 PR-09。 |
| SA-09 启动补漏/撤回 | partial | desktop activation/remote PTY effect 都接统一 shared gate，五 CLI 的 gate 顺序测试通过；当前代删除/冻结撤回已有实现；PR-01/02/05 不允许判闭环。 |
| SA-10 Markdown 资源 | met | FileLeafUnits 真实包含 commands/agents 顶层 .md 与目录；资源回归通过；实际 Claude 发现/执行待真机。 |
| SA-11 Codex 恢复接管 | met | adopt=true 可越过 Detached，保留 selected keys；对应 Codex 回归通过。 |
| SA-12 插件 filters | partial | 目标 settings 过滤对象、nil/[] 三态和复检已实现，piplugin/wslsupply 回归通过；PR-08 重置遗漏；真实 CLI 资源加载未验证。 |
| SA-13 both 锚点 | met | Windows nil/Success=false/空锚点均 skipped，remove 仍两侧独立；root targeted 测试通过。不等于网络安装已经运行。 |
| SA-14 OpenCode 冲突/override | partial | 同读 json/jsonc、comments/trailing commas、unknown 不写、override 实时 generation 绑定；launcher/root 回归通过。但最新 OpenCode 原生配置 schema/环境覆盖层/实际 MCP 握手未验证，不能仅凭自建 command/args fixture 记消费成功。 |
| SA-15 状态真实 | partial | 成功后失败记录已改；PR-05 durable synced 仍虚报，部分结果仍只有 UnitApplied+detail，不能按单元总数推导全部键成功。 |
| SA-16 取消/超时 | partial | 主 Apply 入口/锁后/逐单元/sensitive rename 前新增检查；runner ctx 优先归因；PR-07 尚在。Linux 子进程回收未证实。 |
| SA-17 扫描轮询 | met | Get 首次扫描幂等、Check 显式重扫、unmount 停后继轮询；源码/回归满足，不冒充浏览器实操。 |
| SA-18 测试契约失配 | partial | 原取消 PATH/越界 symlink/.env fixture 三问题已修，普通包测试全通过；PR-09 仍削弱并发验证，非产品安全失败。 |
| D1 Headroom NAT | met | 桌面副作用前 admission、planner effect 前拒绝；mirrored 只准入非连通；root gate 回归通过。 |
| D2 remote/restart env | met | planner Keys/PathAllowKeys/同一 strip 谓词贯通，桌面/remote 测试通过；真实 WSLENV/Windows 子进程方向待真机。 |
| D3 cwd | met | spec 后置定谳 Linux cwd，显式无效宿主形态拒绝；最终 Recipe/Session 投影一致；activation+parity 测试通过。 |
| D4 内部 proxy marker | met | ApplyEnv 启用/停用均清理继承 marker 后唯一重建；aiproxy/root 回归通过，未删除 NAT 安全门。 |
| D5 Pi/OMP 非等价 fallback | met | WSL provider 构建/写入错误阻断；remote managed-models 非 Applied/Fresh 阻断，原生行为保持；root/launcher 回归通过。 |
| D6 Codex Headroom | met | 桌面给不生效 advisory，remote 显式选择 WSL 压缩拒绝；未新增同步/代理体系；root 测试通过。不是声明 WSL Codex 压缩已可用。 |

**O7 遗留观察**：restart 按宿主当前默认 distro/provider 语义漂移仍保留，未找到本轮使其更严重的独立反例，不列新回归、不要求本轮盲修。五 CLI × restart 的相同代码路径测试只证明新参数传递/gate 接线，不证明 O7 原契约已经改变。

## 4. full 清单覆盖与边界

| 维度 | 本轮覆盖 | 结果 |
|---|---|---|
| 正确性/入口一致 | 五 CLI app.go 启动变更、planner/executor、desktop authority activation、workdir/env、Headroom、proxy marker | L2 主链通过；shared gate 语义 PR-01 未闭环。 |
| 边界/用户归属 | state v2、unknown/legacy、consent/selection/keep-local、旧计划 CAS、源/目标撤回 | PR-01/04；不擅自移除用户独立副本。 |
| 安全/秘密 | coordinator/launcher/A2 staging、无变化权限、失败恢复、视觉导出 | PR-02/03；无真实秘密操作，UNC/ACL 真机未测。 |
| 一致性/并发 | Plan/Apply/Compensate、状态快照、single-flight、插件 settings/state | PR-05/06；外部编辑与两套 writer 锁边界仅静态，不泛报所有窗口。 |
| 兼容/迁移 | 原生 cwd/env 语义、v1 隔离、A→B→A、filters nil/[]、OpenCode jsonc | 迁移残留和动作分区仍有 PR-01/04；真实新版 CLI consumer 未验。 |
| 生命周期 | ctx 取消/锁/提交、插件 CommandContext/timeout、技能接管 | PR-07/09；Windows wsl.exe 退出不代表 Linux npm/git/pi 退出；验证脚本本身另有 PR-10。 |
| 性能 | 扫描 Get/Check/unmount、循环合并、启动不扫技能/安装插件 | 没有扩大启动扫描/安装面；无 DrvFS/UNC 性能实测或 SLA。 |
| 可维护/验证有效性 | 统一 helper 实际调用面、新测试断言层级、race 归因、CI diff | 普通测试通过不掩盖 race；无浏览器/视觉验收，未按纯风格报问题。 |

边界保持：未见本批新增 ext4 强迁、全局 Git/WSL 设置自动修改、MCP 新产品面或 omp 安装面。手装 omp 的既有启动纳入代码回归，不写成安装支持完成。

## 5. 实际验证与可信度

本轮所有命令 cwd：`/Users/run.mao/workpace/amagi-codebox`。只有报告目录写入。

| 执行者/命令 | 真实结果 | 证据 |
|---|---|---|
| 本轮 `go test . ./internal/wslsupply ./internal/launcher ./internal/piplugin ./internal/aiproxy ./internal/envvars -count=1` | exit 0，6 包 PASS | `phase-review-tests.log` |
| 本轮 `go test -race ./internal/wslsupply -run '^TestCompensateApplySerialized$' -count=1` | exit 1，产品状态 race | `phase-review-race.log` |
| 本轮 `go run ./agent-outputs/workflow/wsl-goal-closure/phase-review-repro.go` | exit 0，4 条反例输出 | `phase-review-repro.log` |
| 本轮同目录 `phase-review-mode-repro.go` / `phase-review-restore-repro.go` / `phase-review-cancel-repro.go` / `phase-review-filters-repro.go` 分别 `go run FILE` | 各 exit 0；宽权限 Fresh、恢复递归/删文件、取消仍写、过滤未重置 | 各同名 `.log` |
| 本轮 `git diff --check HEAD` | exit 0 | 工具实际无错误输出；不是功能验收 |
| Leader frontend 单测/build | 上游 `integrated-frontend-tests.log` 实际为 **31 files / 277 tests PASS**，较任务正文 271 更新；build 日志已读取开头，PASS 结论由 Leader 提供，未复跑 | `integrated-frontend-tests.log`、`integrated-frontend-build.log` |
| Leader vet/Windows 交叉编译 | steer 报最终 exit 0；vet 空日志本身不能证明退出状态；交叉编译日志各包 ok，**未运行 Windows 测试** | `integrated-vet.log`、`integrated-windows-compile.log` |
| Leader 全仓 Go / race | 全仓已知宿主 envcheck/platform 污染与本轮供给回归分账；不拿旧编译拼写错误作 finding；race 真实 FAIL 已分类 | steer、`integrated-race.log` |

复现脚本均为独立取证，不是正式项目测试；使用合成字节/临时目录，退出清理。五个本轮 Go 取证脚本加 `//go:build ignore`，避免多个 main 污染后续 `go test ./...`；显式 `go run FILE` 可复核。正式回归应由实施者落地，不由独立检验者编写。未更改历史 `supply-audit-repro.go`。

## 6. 尚未验证 / Windows 后续阻塞

不能以 macOS fixture、Windows cross-compile 或 CI 配置变更关闭以下项：

1. 当前修复构建在 Windows+WSL 的五 CLI desktop / remote / restart，精确 distro、uid/Home、cwd、CLI 解析路径、选定 model/provider/endpoint 与至少真实模型响应；O7 单列语义裁决。
2. NTFS 隔离工程中文/空格路径、实际挂载映射、Linux rg/脚本编辑→Windows 工具回读；Windows git.exe/PowerShell 退出码/编码/取消；UE/VS/SDK 构建需要专用环境与写入授权。不要求迁移 ext4。
3. 真实 UNC rename、0700 staging、0600 final、ACL/所有权、第二 Linux 用户全时段观测、宽权限/StatMode 故障/清理失败与中断恢复。synthetic stat 不证明真实隔离。
4. 专用 distro 的 v1→v2 升级残留、停机期间撤回、同名重装/换用户/unknown；敏感旧副本仍在时新启动确实被阻断/安全处理。
5. 当前安装版本的 Pi/OMP models/auth、Pi packages filters、Claude Markdown/skills、Codex profile/TOML、OpenCode 原生 MCP schema 与配置层优先级/实际握手。尤其 OpenCode 当前适配的 command/args/env 与原生数组/environment 等形态，现有自建 fixture 不足以证明最新消费者接受；尚无反例证据，不额外签发 Major。
6. mirrored 声明但未实际生效、NAT/unknown、真实代理/Headroom 网络链；不可用时不能误打默认 endpoint。
7. 超时/取消后 Linux 子进程及其后继写/安装确实停止；当前只能确认 Windows runner 取消归类与显式未验证文案，不能宣布无残留任务。
8. 设置面板真实点击、供给/失败/迁移/部分成功状态、卸载轮询，以及视觉审查；本轮未启动浏览器，未作视觉结论。

**后续门禁**：先批量关闭 PR-01–07/10、复核 PR-08/09；复跑针对性普通/-race 测试，再在授权 Windows 专用环境跑上述关键链。CI 新增 Windows 确定性测试尚未提交/运行，不计真机/CI PASS。缺 Windows 入口不阻止本次代码审核交付，但阻止签发总体目标/发布通过。

## 7. 产物、现场与回滚

本轮只新增本报告、`phase-review-*.go/.log`、`phase-review-plugin-diff.log`（仅取证文本），均在声明的报告目录。未修生产文件、未编写正式项目测试、未 commit/push；未读/写/删除既有未跟踪 resume-relay 文件。产品现场保留。

回滚本轮产物仅由 Leader 按需删除本轮 `phase-review*` 文件，不需要恢复源码、用户配置或凭据；不要连带删除本目录其它人的材料。报告不是修复 patch，也未自行裁决 O7 或扩展 Windows/WSL 权限。
