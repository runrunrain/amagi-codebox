# P3-S0 集成 diff 对抗检验报告（谛听 · full）

- 基线：HEAD=cdb8a7a，工作区未提交改动 + 未跟踪 `internal/wslsupply/` 等。
- 方法：只读静态审查（read/grep/find；本角色工具面无 bash，未运行 git diff/测试/真机）。任务书七项关注点逐项沿代码路径构造失败假设核实；报告声称的测试结果未复跑，按命令记录采信并在盲区标注。
- 上游输入：`p3-config-supply-design.md` §七/§八（必读，已完整读取）、`s0-report.md`（必读，已完整读取）。

## 一、审查关注点结论

### 1. 提交原语安全 — 通过，2 处 Minor（M1/M2）
- `submitFile`（coordinator.go:499）：目标内 `os.CreateTemp(dir, ".amagi-supply-*")` 唯一名 → 0600 → rename → distro 内 chmod 600 补偿（失败即 Remove，fail closed）→ 读回 hash 校验；字节走 UNC，不进 wsl.exe 命令行。与设计 §七提交原语逐项对应。launcher 侧 `writeWSLAuthAtomic`/`writeWindowsAuthAtomic`（wsl_pi_login_sync.go:213/239）同构，tmp 名从固定名改 CreateTemp，注入接缝 `wslAuthCreateTempFn` 相应改写测试（staging-create 失败注入替代固定名碰撞），旧 `.amagi-tmp` 残留断言保留。
- chmod+清理双失败：`errWSLAuthLooseLeftover` → classifyWriteFailure → NoAuth fail closed，`TestSyncWSLPiLoginAuthFailureClassification` 第三子测断言文件在场仍拒绝降级。✅

### 2. A2 统一边界正确性 — 通过，判定语义零变化声明属实
- 算法本体 `SyncWSLPiLoginAuth` 判定序列（读源→缺失即 ErrWSLPiLoginSourceMissing→hash 同即 Fresh→mtime 前向/回写/±2s 容差 tie 取 distro→探测失败两侧零写入 HasCopy）与 P2T2 修复轮记载逐项一致；launcher 既有矩阵测试全数保留。实现级唯一变化（唯一临时名）已如实声明（s0-report §3.5）。静态核实，非 diff 逐行比对（见盲区）。
- 载荷退役全覆盖：全仓 grep `piLoginWSLAuthSyncSnippet`/`WSLDrvFSMountPath`/`ensurePiConfig`/`wslPathFromWindowsHome` 生产代码零命中（仅文档/墓碑注释）。`piLoginWSLReAnchorSpec` 只剩 `env -u … 'cli' 'args'; exec bash -li`；`TestPiLoginPlannerWSLGateLifted` 断言禁入 `cmp -s`/`cp --`/`stat -c %Y`/`chmod 600`/`auth.json`/`mkdir -p` 并逐 token 校验前缀。
- Abort/补偿：A2 `UnitResult.Undo` 恒 nil（classifyA2 不构造 handle，`TestA2NeverCompensates` 钉住）；effect Abort 只走投影 CAS 撤销。A2 凭据刷新不随 PTY 失败回滚成立。
- 桌面闸门：`LaunchPiSession`（app.go:3758）PTY 前调 `syncWSLPiLoginAuthAtLaunch` → `runWSLLoginSupplyFn`（生产=协调器 RunUnits，runner 缺位 fail closed）→ 同一 `verdictForWSLLoginSupply`。

### 3. remote effect 契约 — 通过
- BuildPlan 纯读：`buildPiLoginPlan`（pi_login_launch.go:336）对 WSL spec 只 append `EffectWSLConfigSupply` 描述（Distro 留空）；`TestPlanIsPureComputation`（探测计数零增量）+ Distro 空断言（计划期不 spawn）双钉。
- 顺序：rank 8（ConfigMutation∈[2,5] 之后、PTY 16 之前），`TestPlanValidateWSLConfigSupplyEffect` 含反序拒绝。注意 ConfigMutation rank=1+ConfigTarget，未来 target 扩到 7 时与 8 撞号且 Validate 允许相等（建议区 S1）。
- distro 解析：effect Apply 时 `DefaultWSLDistro(nil)`（launch_executor.go:758）与 pty `buildWSLCommandLine` 的 `DefaultWSLDistro(env)`（service.go:919）共享同进程 distro 缓存，同源成立。
- 失败传播：effect Apply 返回 verdict.launchErr → `remote_session_adapter.go:587` effectErr → AbortLaunch + 会话不启动；生产 restart 走 `authorityRestart`（972 行 BuildPlan OriginRestart → 同款 effect loop），m2a `restartRawEffect` 在生产无 launchRaw（control_wiring.go:274）不构成第二条凭据路径。gate 类别 `LaunchWSLConfigSupply` 纯增量（control_types.go:209，iota 末位），`launchEffectKindForSpec` 一行映射。
- 权限门控：`buildWSLConfigSupply`（launch_executor.go:205）Prepare 期无协调器即 fail（不静默降级旧 shell 路径）。

### 4. 状态清单 — 通过
- `state.go`：tmp 0600→rename→chmod 0600 原子写，`mutate` 全变更经锁；损坏 JSON/schema 超前版本回退空清单（`TestCorruptStateFileFallsBackToUnmanaged`：不 panic、重跑重记、不重置目标）。A2 登记永不写 hash（`TestApplyA2DelegatesAndRecordsState` 对状态文件字节断言无 sourceHash/lastWrittenHash）；GetStatus（UnitStatus 无 hash 字段）敏感 hash 不出接口，未绑前端（bind_list 无 wslsupply）。generation 变化旧登记失效不继承（upsertEntry）。

### 5. 停用播种 — 通过
- `service_windows.go:208` 墓碑注释；`TestInstallToolPiDoesNotSeedAgentTree`/`AlreadyOKIdempotent` 脚本级全量捕获断言（任何阶段脚本禁 `cp -r`/`.pi/agent`，日志禁 `[pi-config]`/`seed`），Success+版本与 AlreadyOK 幂等语义保留，配置失败未混入安装失败。注意：两测试 `skipWithoutWSLDistro` 且文件为 windows-only，CI（macOS 全量 + windows compile-check）不执行断言——历史测试布局，非本批引入（遗留观察）。

### 6. 测试有效性 — 通过，1 处 Minor（M4）
- 双链路故障矩阵 `TestWSLLoginSupplyFailureMatrixBothLinks`：9 用例 ×2 链路，两链路均为真协调器（注入 Probes+A2 stub），逐用例断言两链路 block/放行一致 + 中文文案一致；适配器恰好调用 1 次。算法级由 launcher 矩阵钉住。质量高。
- 并发测试见 M4（覆盖边界如实但报告措辞略强）。

### 7. 与设计一致性 — 通过，无越界
- s0-report §1 对照表 15 行抽核全部属实（含 Distro 留空、gate 枚举、状态脱敏字节断言等具体声明均与代码对上）。
- 8 条微观决策逐条核对均在设计留白内：#1 与 PTY 同源解析成立；#2 纯增量无 switch 消费方（已核）；#3 UnixNano 回拨只造成不更新（max 语义），无害；#4 两次身份探测已声明；#5/#6/#7 收紧而非放宽；#8 与设计 §十一致。
- S1+ 内容未提前实现：生产投影注册为零（仅测试桩），provider/models/视觉/OpenCode/skills/插件/UI 均未触碰；`wsl_agent_config.go` 不在改动面。

## 二、Findings

**M1｜Minor｜internal/wslsupply/coordinator.go:548-595（Compensate/compensateOne）**
- 触发条件：Abort 补偿与并发 Apply 同时到达同一目标文件（Compensate 的「读 current→CAS 判定→submitFile/Remove」全程不持 `applyMu`，而 Apply（:252）持锁；恢复走 submitFile 亦在锁外）。
- 实际影响：TOCTOU——CAS 读通过后、写前，另一会话的 Apply 已 rename 新内容，Compensate 将删除/覆盖该新写入。违背设计 §七「协调器按目标根/文件串行化」对补偿写入的声明。S0 生产不可触发（A2 无 Undo、投影未注册，Compensate 恒 no-op）；S1 分发接入后成为真实写-写竞态。
- 证据：静态路径（`Compensate` 未取 `c.applyMu`；`submitFile` 为被 Apply 持锁调用的私有方法，Compensate 复用它时不持锁）。未复现（S0 无触发面）。
- 最小修复方向：`Compensate` 内逐 handle（或整批）加 `c.applyMu.Lock()`；S1 接入真实分发前完成。

**M2｜Minor｜internal/wslsupply/coordinator.go:499-546（submitFile chmod 失败分支）**
- 触发条件：投影单元更新（目标已存在）rename 成功后 distro 内 chmod 600 补偿失败 → `os.Remove(uncPath)` 删除目标且不恢复前像。
- 实际影响：对普通（非凭据）投影文件，用户原有文件被直接删除——超出设计 §六「可选普通设置同步失败，旧 WSL 配置可读且安全→启动继续」的失败语义（该语义要求旧配置仍可读）。对凭据文件删除是正确的 fail-closed（宽松权限明文不留盘），但提交原语未区分敏感/普通单元。S0 生产无投影单元不可触发；S1 首个真实分发单元上线时即成可达路径。
- 证据：静态路径 + `TestSubmitFileChmodFailureFailsClosed` 只覆盖首建（prevExisted=false）场景，更新场景未测。
- 最小修复方向：S1 前按单元敏感度区分清理策略（敏感：维持删除；普通：优先恢复前像，恢复亦失败才删除并在结果中标注）。

**M3｜Minor｜app.go:3752-3757（注释）**
- 触发条件：阅读桌面闸门注释。
- 实际影响：注释仍称「再以 WSL 锚定变体重锚（载荷内嵌同步片段 + env -u，remote 链路共用同一载荷语义）」——内嵌片段已退役，与 pi_login_launch.go 现状矛盾，误导维护者（同 s0-report §7 已披露的 pty/service.go:949 类问题，此处在报告未列）。
- 证据：pi_login_launch.go:206-216 注释与代码（仅 env -u 重锚）。
- 最小修复方向：改为「经 wslsupply 协调器 PTY 前供给 + env -u 重锚」。

**M4｜Minor｜internal/wslsupply/projection_test.go:160 / internal/launcher/wsl_pi_login_sync_test.go:336（测试有效性边界）**
- 触发条件：解读验收 5「并发不撞临时名」的证据强度。
- 实际影响：两个测试的 goroutine/写入序列均被 `applyMu`/测试内 mutex 串行化——唯一临时名原语在单协调器内从未于真正并发写入下执行（保护实际来自锁 + CreateTemp 的 OS 级 O_EXCL）；唯一真正依赖唯一名的路径恰是 M1 的无锁 Compensate。projection_test 注释已自我披露，s0-report §4 表述（「8 并发 RunUnits 各自文件全部成功、无残留」）与测试断言相符、不算虚报，但「不撞临时名」的归因部分由锁承担。
- 证据：projection_test.go:160 注释「同一协调器的 applyMu 串行化同一目标上的 Apply」；wsl_pi_login_sync_test.go:336 注释「本测按同一串行化前提验证」。
- 最小修复方向：M1 修复后补一个锁外的真并发 staging 测试，或接受注释现状并在 S1 验收中列明。

## 三、建议区（不影响 VERDICT）
1. `CanonicalEffectRank`：ConfigMutation rank=1+ConfigTarget，target 扩展至第 7 个时与 WSLConfigSupply 的 8 相等且 Validate 允许相等序——S1 前把 supply rank 提为独立区间或对相等 rank 定序。
2. wslsetup 播种停用两测试在两条 CI 线上均不执行断言（macOS 不编译、windows 仅 compile-check）；可与团队确认是否有本地 Windows 门禁钉住。
3. s0-report §7 已自披露的 docs/注释陈旧项随 S1 一并清理时，把 app.go:3756（M3）加入清单。

## 四、盲区与验证可信度
- 未运行 `git diff HEAD`、`go build/vet/test`、真机冒烟（本角色无 bash）：A2「一字未动」的结论来自静态比对当前代码与 P2T2/测试矩阵记载的行为契约，非逐行 diff；全部「测试全绿」「真机 PASS」采信 s0-report 命令记录，未独立复跑。
- A2 真机写入 e2e（双侧快照恢复）本轮未执行，s0-report 已如实列为待 Leader 决定项；与本审查范围一致，未降级其风险。
- 跨进程（两个 CodeBox 实例）并发写入沿用单实例假设，s0-report 已声明；未额外检验。

VERDICT: PASS_WITH_MINOR
