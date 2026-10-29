# Phase 1 修复批次增量复审

## 结论与基线

**FIX。** 原编号逐项结论见下表；本批修复确实收敛了多条实际缺陷，但门禁过渡、恢复命令原子性仍未收口，并引入缓存倒退、恢复 revision 拒绝合法崩溃状态、漂移录入无 UI 出路三项 Major 回归。

- 上游（均已完整读取）：本目录 `phase1-diting-review.md`、`phase1-fix.diff`、`phase1-fix-report.md`。修复报告只作为声明；未把其中“同一锁内”“全部在途有界”等注释当事实。
- 源码根：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/claude-account-rotation/`；以下代码定位均相对此根。
- fresh HEAD=`c15bd6c75db649ad31211c6b98c02526e3c19773`；基线 `3acbc90`；34 文件增量全文已读。`git diff 3acbc90..c15bd6c --no-ext-diff | shasum -a 256` 与输入 diff 一致：`5b8d11fca1c96d532926b981b72a3d6ab6ca97d4f5f4880b657d6718930cae71`。
- 本轮为增量复审，不重新全仓审计。下面反例均为**静态实际调用路径证实**，未声称动态复现竞态或接触真实账号。测试复跑事实另列。

## F01–F15 逐条判定

| 原编号 | 判定与锚点 |
|---|---|
| F01 Major | **部分关闭**：`claude_accounts_launch.go:349-386` 在释放 mutation 前发布，`:262-270,280-289` 增 blocked 复核，修复 managed 交接的基本路径；但 absent 启动仍没有与 mutation 互斥的 claim，remote absent 检查只是瞬时检查（详见残留 F01）。新增直接刷新又有 R01。 |
| F02 Major | **关闭（原触发路径）**：`claude_accounts_launch.go:529-557` 仅归因凭据 lease holder；无 lease 的 absent-era 会话不再被豁免。`TestF02_FirstCaptureFromAbsentWithRunningSessionBlocked` 经 facade→Service→生产 Gate/Quiescence 验证拒绝；PID/进程表为隔离 fixture，不是真实 CLI 实测。 |
| F03 Major | **关闭原代码缺陷，修复引入 R02**：`internal/claudeaccounts/swap.go:128-142` 同步更新二次 harvest 的 before/revisions；`:197-204` 短锁内复核存在。但二次 harvest 测试没有进入该分支，见测试有效性节；新复核对合法崩溃窗口过严，见 R02。 |
| F04 Major | **关闭原“取消盲清”缺陷，修复引入 R03**：`internal/claudeaccounts/service.go:454-494` 走 gate、静止与当前投影分类，漂移保留 journal。既有 facade 测试已反转为 NEEDS_RECONCILE+journal+blocked，并有恢复源后取消成功的后端对照。人工恢复入口交付不完整见 R03。 |
| F05 Major | **部分关闭**：`internal/claudeaccounts/harvest.go:180-213` 现在实际查询两份目标 token，刷新后未知身份、账号不符均拒绝；但组织校验只覆盖 CC，Pi 组织完全未检查，未满足原最小修复的组织配对要求（详见残留 F05）。 |
| F06 Major | **部分关闭**：`claude_accounts_facade.go:607-628` 消费 pending outcome；`swap.go:68-88` 先 reconcile 再 no-op；全 before 测试通过。但 `facade.go:600-607,647-665` 的 revision 检查与执行分属不同锁区间，仍存在 TOCTOU（详见残留 F06）。 |
| F07 Major | **关闭本次点名遗漏**：`launch_planner.go:350-360`、`pi_login_launch.go:366-377,431`、`app.go:2844-2855`、`internal/launcher/service.go:297-304,321-333` 补齐 remote Claude、Pi login 与外部直接 exec 边界。外部新增测试实际上只测 strip helper，不把它记为真实外部启动证据；代码链已静态核对。 |
| F08 Major | **关闭模板分支缺陷**：`ClaudeAccountsSection.vue:242-245` 将横幅移至共用区；SSR 渲染真实 .vue 模板的四分支断言有效。此关闭不是浏览器交互或 Wails 事件桥签收，边界裁定见下文。 |
| F09 Major | **关闭原正常 enrollment 重载丢 ID 路径**：`claude_accounts_facade.go:360-363` 返回持久化 ID；`stores/claudeAccounts.ts:137-143` 重建向导；`service.go:417-424` 锁内仍校验 TxnID/revision。漂移后故意隐藏 ID 引入 R03，不算正常续接已通过的证据。 |
| F10 Major | **部分关闭**：标记正常写成功后能跨重启区分 missing；但 `claude_accounts_launch.go:85-114` 的标记读取/写入失败被当不存在或仅日志告警，仍可退化 absent，见残留 F10。 |
| F11 Major | **关闭代码缺陷**：`internal/claudeaccounts/nativecc_darwin.go:68-84,168-176` 删除原生 Update 的 Add fallback、missing 映射稳定错误。静态 C 路径足以证明原漏洞已删除；真机 ACL/locator 仍是发布门，不以 fake 代替该证据。 |
| F12 Major | **关闭本次已证实的官方 pi-rpc 漏检**：`claude_accounts_launch.go:429-457` 补 pi-rpc 与官方包路径；fixture 正负与观察器 uncertain 路径通过。不是任意改名标题或所有 OS 启动形式的完备识别证明。 |
| F13 Minor | **关闭**：`internal/claudeaccounts/oauth.go:130-162` + `harvest.go:366-369` 字段级合并；未知扩展字段的 helper 与换入刷新端到端断言通过。 |
| F14 Minor | **关闭**：`internal/claudeaccounts/harvest.go:421-424` 锁内删除前校验 revision；stale 零删除/正确 revision 成功配对测试通过。 |
| F15 Minor | **部分关闭**：`claude_accounts_facade.go:176-207` 确实跳过运行中 victim，但全在途仍无上限或 busy；注释“在途操作数本身有界”没有实现依据，见残留 F15。 |

## 原编号残留（保持原严重度；不换编号重复计算）

### F01｜Major｜absent 检查未与实际启动互斥

- **定位**：`claude_accounts_launch.go:245-248,280-283,321-325`；`internal/remote/remote_session_adapter.go:615-626,1111-1121`；`app.go:2806`。
- **触发**：桌面在 absent 时取得 nil claim；或 remote 的 absence check 返回成功。暂停在真正进程出现之前（remote 尚有 Executor.Prepare/后续启动）。另一请求首次 capture / begin enrollment 进入 mutation，观察器看不到尚未生成的 PID；然后原启动继续。
- **实际影响**：启动没有凭据 admission/lease，nil recheck 又直接通过，能在启用/录入事务中启动，后续也不能靠 lease 阻止切换。remote 增加一次 check 只能拒绝“检查前已启用”的旧计划，不能封住“检查后启用”。这是原 F01 absent 过渡要求的残留。
- **证据**：所有涉及的检查均只读缓存，没有取得可以持续至启动/提升的互斥所有权；F01 新测试分别调用 gate release、absence helper，未在 check→Prepare/Start 窗口插入 mutation。
- **最小修复**：absent 也需要覆盖过渡期的 launch reservation，或由统一执行屏障原子完成状态判定与 reservation；不要再靠增加一次无锁检查收口。正式测试须暂停实际启动链而非只调用 helper。

### F05｜Major｜目标 Pi 组织未验证

- **定位**：`internal/claudeaccounts/harvest.go:201-213`；原基准 `phase1-diting-review.md#F05` 的最小修复明确要求目标身份/组织配对。
- **触发**：目标记录为 `(uuid-A, org-1)`，CC profile 为 `(uuid-A, org-1)`，Pi 当前/刷新后 profile 为 `(uuid-A, org-2)`。
- **实际影响/证据**：两个 UUID 比较全部通过，唯一组织判断只读 `ccID.OrganizationUUID`，未读取 Pi 的组织，也未比较两端组织；仍可把未验证属于目标组织的 Pi grant 写入。目标的本地 identity 配对也不在新验证函数中明确复核。
- **最小修复**：按已批准的身份/组织契约校验两端与目标及目标本地 identity；未知组织语义需显式保持待验证/拒绝，不静默把单端检查称为双端组织验证。补同 UUID 不同组织反例。此为静态输入反例，未发真实 profile 请求。

### F06｜Major｜revision 预读不是锁内恢复校验

- **定位**：`claude_accounts_facade.go:600-607,647-665`；`internal/claudeaccounts/service.go:232-235`；`reconcile.go:318-369`。
- **触发**：恢复 operation 的 `LoadPool` 返回 revision=N 后暂停；并发 Rename（`harvest.go:435-460`，允许 journal 存在）将 revision 更新到 N+1，或其他恢复改变事务；再继续 `RecoverAtStartup` / `Swap`。
- **实际影响/证据**：`LoadPool` 返回即释放 `s.mu`；恢复入口随后另取 withLocks，但没有接收 expectedRevision/TxnID。restore-source 传入的只是 generation，Rename 不改变 generation，因此旧 revision 恢复命令仍会执行。注释“同一锁语义”不成立；现有 stale 测试只在调用前预置旧值，未覆盖检查后漂移。
- **最小修复**：领域恢复命令接收并在同一 withLocks 内验证 expectedRevision 与所选 journal，再执行恢复/提交。no-op 与 outcome 两项本轮修复保留。

### F10｜Major｜存在性标记失败后保护仍可静默丢失

- **定位**：`claude_accounts_launch.go:85-114,375-384,686-692`；`claude_accounts_facade.go:762-769`。
- **触发**：Keychain 池已存在，但初始化标记因 ENOSPC/目录权限等无法落盘，或 Stat 非 ENOENT 失败；随后池被删除并重启。文件系统失败不等于 Keychain Save 失败。
- **实际影响/证据**：write helper 仅记录日志、无错误返回，现有池仍可被宣布 managed；Stat 将任何错误当 false。下一次无池且无可读标记时缓存写入 nil,nil，policy=absent，已管理历史消失。默认路径解析失败也明确禁用标记继续运行。正常成功 fixture 不覆盖这些失败。
- **最小修复**：将存在性记录作为启用/发布的持久化前提，失败进入明确 blocked；区分 ENOENT 与读取错误。不得仅靠本进程布尔缓存证明跨重启历史。

### F15｜Minor｜全在途仍可无界入队

- **定位**：`claude_accounts_facade.go:145-210,258-263`。
- **触发**：前一个操作阻塞 I/O/持 Service mutex，大量不同 requestId（或无 requestId capture）持续提交，所有 done 均未关闭。
- **实际影响/证据**：每次先注册操作，再扫描并在无可淘汰项时 break，随后照常启动 goroutine；不存在 admission 数上限。因而保住幂等但未满足原最小修复“全运行中限制入队/显式 busy”。局部资源边界问题，保持 Minor。
- **最小修复**：显式限制在途数，超限稳定 busy；已完成结果缓存可独立淘汰。测试至少覆盖全在途超过限额。

## 本次修复引入的回归

### R01｜Major｜直接刷新可覆盖更新的权威 blocked 状态

- **定位**：`claude_accounts_launch.go:365-384,669-695`；`claude_accounts_facade.go:341,263`。
- **触发**：GetClaudeAccounts/上一操作的 refresh 直接 Load 出旧 managed 池 A，尚未取得 `claudeAccountsMu` 发布时暂停；并发 mutation 落 journal，release 在 admission 释放前发布 blocked；旧 refresh 再取得 mutex，把旧 A/err=nil 覆盖回缓存。
- **实际影响**：状态回到 managed；失败事务 generation 未增，后续 launch 的 blocked/fingerprint 检查均依据同一旧缓存，放行混合槽位。此处不是外部进程改写池，也不需要 PoolStore 数据竞争；是合法并发读结果的发布顺序错误。
- **证据**：本批把 refresh 从 `svc.LoadPool` 改为直接 PoolStore.Load，移除了与 mutation 的串行读屏障；Load 与发布之间无版本比较或序列锁。mutation release 发布与普通 refresh 使用相同缓存但不同时间的快照。
- **最小修复**：统一权威状态单写/序列化读取发布，防止旧快照（含旧错误/旧缺失）倒灌；journal 状态不能只按 generation 判新旧。加“旧 Load 已返回→新 blocked 发布→旧 refresh 发布”的精确屏障。

### R02｜Major｜原生已写、进度未落盘的合法崩溃现在无法 roll-forward

- **定位**：`internal/claudeaccounts/swap.go:197-204,254-284`；`reconcile.go:166-185`。
- **触发**：prepared journal 已持久化，CC Patch 已成功，但进程在 `ExpectedRevisions.ClaudeKeychain=rev` 后对应 Save 持久化前崩溃（或该 Save 失败）；其余两处仍 before。重启恢复。
- **实际影响**：三处均能按 before/after 分类，设计应补写未完成处；但持久化 journal 仍存旧 CC revision，新的 applyAndCommitLocked 对**所有** current revisions 与旧 ExpectedRevisions 做全等比较，在写入 pending 前即 NEEDS_RECONCILE。没有未知外部值也恢复不了，入口持续关闭。
- **证据**：原生 Patch 与池 Save 是两个独立提交，无法原子化；reconcile 的 mixed 分支未将“刚分类且确认 after”的当前 revisions 作为本次恢复基线，直接复用旧 journal。既有普通写失败测试发生在已保存进度后，不覆盖这个窗口。
- **最小修复**：保留新事务短锁前的 revision 检查，但恢复必须在短锁内对当前 before/after 分类并冻结新的恢复快照，拒绝 unknown/再次漂移，而不是要求崩溃前进度必然落盘。补 native 成功→进度 Save 前终止的反例。

### R03｜Major｜漂移 enrollment 被隐藏 ID 后没有任何 UI 恢复出口

- **定位**：`service.go:487-494`；`claude_accounts_facade.go:360-365,595-682`；`stores/claudeAccounts.ts:137-143`；`ClaudeAccountsSection.vue:60-122`；`reconcile.go:273-275,345-346`。
- **触发**：录入 B 后取消，后端按新 F04 正确保留 drifted enrollment；刷新页面/重新加载池（或重启）。
- **实际影响/证据**：DTO 变 needs-reconcile 且刻意不返回 enrollmentId，store 清空向导；页面只剩 swap 三选一。complete-target 对 enrollment 返回 none，但 fresh journal 检查转 blocked；adopt-current 因任何 transaction 拒绝；restore-source 因不是 swap 拒绝。即使主上原生重登 A，页面也没有 CancelEnrollment 按钮；无法执行报告承诺的“保存当前完整配对”或“恢复源后取消”。后端直接调用 Cancel 的成功测试不能证明 UI 有这条出路。
- **最小修复**：安全 DTO 返回事务类型和适用的显式恢复动作；为 drifted enrollment 保留可续接/取消入口，并仍锁内验证 TxnID/revision。不得为恢复 UI 无路而重新盲清 journal。

## 测试有效性、时序与边界裁定

1. **F04 反向断言：有效。** facade 测试实际改变三处 native，断言漂移取消失败、journal 保留及门禁关闭，再恢复源投影并成功取消；不是只检查报错非空。
2. **expired-target：有效修正。** happy path 添加新 token profile 映射；`TestSwapRejectsRefreshedTargetWithoutIdentity` 不加映射时失败、Pi 零写，另有同账号不符反例。尚缺组织反例，见 F05。
3. **CrossProcessLock：显著有效改进但证据有界。** 现在记录持锁活跃计数并断言 maxActive≤1、winners>0，能检验重叠，不再只断言至少一人成功；实际是同进程 goroutine 使用多个独立 flock fd，不是两个子进程实验，也非确定性屏障调度。不据此否定现有 flock 实现。
4. **ReadStableRejectsDirectory：有效。** 真实 Mkdir+Stat 后测试拒绝，与 missing 区分。
5. **F01 屏障不充分。** `claude_accounts_fix_test.go:31-96` 手工持 gate、预存 journal、释放后检查，可证明 release hook 生效；没有暂停实际 mutation 的 release 交接、没有并发 launch、也没有覆盖 R01 或 absent check 后窗口。remote 测试仅调用 helper。
6. **F03-A 注入有效；F03-B 注入位置错误（测试证据缺口，不另伪装成运行缺陷）。** `phase1_fix_test.go:91-103` 在第2次 Keychain ReadItem 注入 B′；已缓存 locator 时，`nativefiles.go:234-259` 同一次 ReadStable 先 probe（第1读），再 payload（第2读）。因此第一次 harvest 已拿 B′，并非第一次 harvest→current2 的二次 harvest 窗口。测试可在不执行 `swap.go:129` 的情况下通过。应在第3读（或显式语义 hook）触发，并证明第二次 harvest 分支确实执行。A 的第5读在最终短锁复核前，符合其窗口。
7. **F05 验证先于 journal/原生 Patch，基本顺序正确。** 与旧 UUID 字符串自洽测试不同，确实调用当前 token 的 identity provider；不要把其当 profile 真机契约已验证。
8. **F06 pending/no-op 正向有效，revision 屏障缺失。** held lease→blocked 和全 before→清 journal→no-op 均实际走 facade/Service；stale 参数测试只覆盖预先 stale，不覆盖 LoadPool 后漂移。
9. **F11 裁定：代码级关闭可以，fake 对齐不是生产证明。** 本轮直接审读 C 的缺项 return 与 Go 映射，Add fallback 已删除；不必为关闭这条确定代码漏洞而触碰真实 Keychain。macOS 编译路径由 Go 测试构建经过，但未动态执行 C missing/ACL；真机 locator/授权/ACL 行为仍属发布门。
10. **F08 裁定：SSR 足以证明本 finding 的模板可见性修复，不足以证明完整交互。** 它编译/渲染真实 .vue，并断言真实错误文案与分支主体，非纯 view helper。测试通过 `applyOperationResult` 直接注入真实 store，不发起 UI 点击/轮询/事件桥。测试中 `toContain('...'.slice(0,0))` 是恒真无效断言，但旁边真实 absent 主体和错误内容断言仍有效，不因此否定全部 SSR 证据。它不能覆盖 R03。

## 本轮执行证据（L2）

所有测试 cwd 均为上述 worktree，以下命令均 exit=0：

- `go test ./internal/claudeaccounts -count=1` → ok，0.764s。
- `go test . -run 'TestF0|TestF1|TestFacade_' -count=1` → ok，0.667s。
- `go test ./internal/remote -run 'TestCredential' -count=1` → ok，0.573s。
- `npm --prefix frontend run test -- claudeAccounts` → 4 文件，66 用例通过；232ms。
- `go test -race ./internal/claudeaccounts -count=1` → ok，1.905s。
- Git HEAD/hash/status 取证：输入一致；测试前后 tracked 无修改，均只有既有未跟踪 `agent-outputs/2026-10-28-claude-account-rotation/`、`amagi-task/` 两目录。

未重复全量 go test/vet/build/全部前端测试；上游报告的结果不记为本轮执行。未写项目测试/修复，也未另写复现脚本；上述新增及残留反例为静态路径结论，精确屏障交回实施者。

## 覆盖、盲区与独立性

- 已覆盖：本批完整 diff、15 项原最小修复、相关消费入口、权威状态缓存发布、事务恢复、目标身份、enrollment DTO/store/模板、operation 淘汰、四项测试有效性、SSR 和 C 分支证据边界。
- 未重审与修复无因果关系的全仓问题；无额外既有观察冒充本批回归。F05/F10/F15 按原编号保留，不重复编号计数。
- 未接触真实凭据/Keychain、未启动真实 Claude/Pi、未发 profile/refresh 请求；原设计的协议、Pi 安装版本 proper-lock 竞争、pi-black 请求身份、ACL/弹窗发布门继续保留。视觉审美、浏览器点击和 Wails 事件桥不在本轮签收证据中。
- 回滚：本轮仅写此报告。若不保留审核产物，由 Leader 删除此报告即可；不要回灌旧 refresh token 或用代码回退替代事务恢复。

VERDICT: FIX
