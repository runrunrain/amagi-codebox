# P4 full 独立审核：Windows / WSL 统一管理集成

VERDICT: FIX

发现 **3 Major、1 Minor；无已证实 Critical**。安装联动、显式键物化和授权隔离的抽检通过，但资源自动跟随和失败/延后重试存在闭环缺口。本结论只适用于下述集成范围，不代表 Windows 真机验收。

## 1. 基线、输入与方法

- 审核基线：主树 HEAD `0723bdbf7fb47fd99965dbea1340dca49af8f306`；审核工作区 `git diff HEAD`，包含本轮未跟踪的新源码、测试、文档及 E2E 脚本。历史 `agent-outputs/baize/`、`amagi-task/` 不纳入。
- 全文必读：`agent-outputs/workflow/windows-wsl-unified/{workflow,direction,config-implementation,install-implementation,setup-implementation,verification}.md`。direction 的通配选择方案已被 Leader 裁定覆盖，按**显式键物化**审核；不将旧 direction 的 `"*"` 当成验收要求。
- 只读项目源码；仅新增本报告与取证程序 `agent-outputs/workflow/windows-wsl-unified/repro-resource-cache.go`。未写产品 patch、未编写项目测试，未执行真实安装/升级，未读取真实凭据或写用户配置。
- full 采用风险驱动抽检：授权/跨代/停跟随、调度生命周期、安装全入口/错误/锁/超时、前端消费、绑定/兼容及证据真实性。不是逐行覆盖所有历史代码。

## 2. Findings

### F1｜Major｜自动资源对账永远复用首次扫描，新增/更新资源不能自动跟随

- **定位**：`app_wsl_unified.go:165-172,278-312`；依赖 `internal/wslsupply/skills.go:105-113`、`internal/wslsupply/resources.go:106-114` 的 StartScanAsync 幂等语义。
- **触发条件**：统一管理首次扫描已结束后，在 Windows 新增技能/commands/agents，或修改已有技能、CLAUDE.md 等；随后保存触发推送，或再次启动会话，而不进入高级面板点“检查”。
- **实际影响**：`reconcileWSLUnifiedResources` 只消费旧 Snapshot，不触发新扫描；`startWSLUnifiedScans` 即使再次调用，Start 方法也因 started=true 不重扫。新增单元不会成为授权/运行候选；已有单元可能按旧 hash 直接报告 fresh/no-change，实际 WSL 副本仍为旧内容。违背本轮“新条目随下一次对账自动纳入、无需重复检查”的 R1 承诺。
- **证据（已复现）**：运行 `go run ./agent-outputs/workflow/windows-wsl-unified/repro-resource-cache.go`，通过真实 Coordinator、SkillsScope、AutoConsentIfUnified、RunUnits，在临时来源/目标目录得到：
  ```text
  initial StartSkillsScan: true
  shared/skill/alpha: applied written
  after source edits StartSkillsScan: false
  shared/skill/alpha: fresh no-change
  after automatic reconcile: alpha="# alpha v1" beta_exists=false snapshot_count=1
  explicit RescanSkills: true
  shared/skill/alpha: applied written
  shared/skill/beta: applied written
  after manual rescan: alpha="# alpha v2" beta_exists=true
  ```
  程序复现的是资源缓存/对账原语；App 自动入口只调用 Start 或 Snapshot 的事实由上述静态链证实。Claude/Codex ResourceScope 同样使用一次性 Start + 旧 Snapshot，静态路径同构。
- **归因**：一次性扫描是既有手动管理设计；本轮把它直接复用为“持续自动资源对账”，暴露为新增自动管理契约缺陷。
- **最小修复方向**：增加统一管理专用的后台有界重扫/变更失效调度，在来源变更或新的对账周期刷新快照；不要把扫描移入会话启动等待链，也不要让完成回调→推送→重扫形成无限循环。正式回归由实施者补“首次完成后新增+修改→自动对账”的入口用例。

### F2｜Major｜扫描完成/启用推送被活动会话挡住后，没有恢复触发器

- **定位**：新增接入 `app_wsl_supply.go:472-475`、`app_wsl_unified.go:192-202`；既有守卫 `app_wsl_supply.go:438-442`；资源不进入 lazy 的 `app_wsl_unified.go:378-447`。
- **触发条件与复核步骤**：保持任一内嵌会话为 running（不限 WSL），此时首次启用统一管理并让三个后台扫描全部完成；不再保存其他配置，关闭该会话，然后启动一个新的 WSL 会话。
- **实际影响**：启用推送及扫描完成推送均在 hasActiveSessionsForWSLPush 处直接返回。worker 随即结束，没有保留待恢复任务、定时重试或会话结束后的调度；后续 launch lazy 只跑 Claude/Codex 键级单元，不跑技能/资源。因此首次资源副本可一直缺失，直到一次无关保存、手动操作或重启碰巧再触发推送。自动授权成功不等于资源可用，无法满足首次配置自动收敛。
- **证据（静态路径证实，未作运行态 App 复现）**：调用点搜索显示 `reconcileWSLUnifiedResources` 仅由 doPushWSLSupply 调用；扫描完成只调度一次 scheduleWSLSupplyPush；doPush 的上述早退不设置可恢复资源任务。全仓本轮相关调度入口未接会话退出事件。已有 TestWSLUnifiedScanCompletionTriggersReconcile 使用空 session manager，未覆盖此时序。
- **归因**：暂停后台写的守卫本身既有且应保留；本轮新增资源“仅靠推送”的入口使其成为自动统一管理的闭环缺陷。不能用守卫的安全意图为丢弃调度辩护。
- **最小修复方向**：保存持久或进程内可恢复的待对账标记，在活动会话归零/目标重新可安全供给时异步补推；保持停机不唤醒、活动会话不写和不阻塞启动的约束。实施者补“扫描完成被阻挡→结束会话→无需额外保存即补供给”用例。

### F3｜Major｜部分启用失败会留下 Unified=true，但固定单元永不自动补授权

- **定位**：`app_wsl_unified.go:94-110,132-138,245-271,322-325`；UI 状态分支 `frontend/src/views/settings/WSLCLISettings.vue:53-73`。
- **触发条件与复核步骤**：SetTargetUnified 已成功持久化后，后续任一固定单元分组的 ResolveTarget 或 state.mutate 短暂失败（例如 UNC 短暂不可达）；EnableWSLUnifiedSupply 返回错误；环境恢复后离开并重进设置页，或重启应用，再保存/启动会话。
- **实际影响**：函数在中途错误处返回，未回滚统一管理标记，也没调度剩余初始化。此后 UI 根据标记显示“已由 Windows 统一管理”、隐藏启用按钮；启动/保存只物化已同意单元并运行 runnable，**不会再次对固定单元调用 AutoConsentIfUnified**。未完成授权的 Pi/shared/Claude/Codex 单元可能永久保持未授权，普通自动对账不能自愈；用户须额外停用再启用或走逐项高级入口。
- **证据（静态路径证实）**：SetTargetUnified 在所有分组前提交；102-110 的授权错误直接返回；固定单元自动授权只出现在 Enable 内，后台 AutoConsentIfUnified 的另一处仅处理扫描树单元。现有浏览器 enable-failure fixture 在调用时直接抛错，不模拟“已持久化标记后才失败”，不能证明该部分失败路径正确。
- **归因**：本轮新引入的多步骤启用事务/恢复问题，不是既有手动授权行为。
- **最小修复方向**：让固定单元初始化可在统一管理对账中幂等续接（保留 per-generation 与 detached 检查），或提供真实的 incomplete 状态和可达重试入口；不要对已经成功授权的单元或保留本地项做破坏性回滚。实施者注入第二个分组失败后恢复，验证无需额外敏感授权即可收敛。

### F4｜Minor｜Windows 侧联动结束后，WSL 卡片不刷新

- **定位**：`frontend/src/views/settings/EnvCheckSettings.vue:885-892`；`frontend/src/views/EnvCheckView.vue:6-8`；`frontend/src/views/settings/WSLCLISettings.vue:370-391,1133`。
- **触发条件**：同页 WSL 卡片已加载后，从上方 Windows 环境检测卡片安装/升级 CLI，并成功同步到 WSL。
- **实际影响**：终态横幅更新，但下方卡片版本/installed 状态仍是初始快照，可能继续显示“未安装/安装到 WSL”或旧版本；切页重进才恢复。direction §2.4 明确要求终态后刷新 WSL 卡片，当前未接线。
- **证据（静态路径证实）**：两个兄弟组件无事件/ref/共享刷新状态；surfaceTerminalOperation 仅设置本组件横幅；WSLCLISettings 的 load 仅 onMounted 或本卡片操作后执行，扫描轮询只更新资源状态而非 GetWSLCLIStatus。现有 6 项浏览器 smoke 只在 WSL 卡片内安装/升级，不覆盖跨卡片状态同步。
- **归因**：联动新增后的跨组件一致性缺失；原来 Windows 安装不改变 WSL，此陈旧状态此前不由该操作触发。
- **最小修复方向**：将终态 WSL 结果通过父组件事件或现有共享机制触发一次卡片 reload，无需新增持续轮询。

## 3. 覆盖与审核判断

| 检查面 | 实际核对 | 结论 |
|---|---|---|
| 身份/授权/未来单元 | SetTargetUnified、AutoConsentIfUnified、ExtendUnitSelectionIfUnified、grantEntryIn、ResolveTarget、Apply 身份复核及 state 记录 | 未发现新跨代授权/unknown 放行；AutoConsent 在同次 mutate 校验 Unified，跳过 Consented/Detached。未来资源只从可移植快照候选授权，仍走既有路径与同名/手改保护。初始化恢复有 F3 |
| keep-local/手改/权限 | 同代 entryFor、detached 筛选、既有 Apply 重验、键级冻结、资源候选 hash/形态复核及 0600 通道 | 未发现新增 detached 复活或权限绕过；本轮未重审全部历史提交/恢复算法 |
| 取消/停用 | 原语 ctx 入场与探测后检查；停用和授权/物化同一 state 临界区内判标记 | 已取消原语抽检通过；App 后台使用 Background，不能把此证据说成 App 操作全程可取消。停用本质是停止新增纳入，不是撤回所有旧授权，旧 lazy 仍可能运行 |
| lazy/push/startup/扫描 | App 三种自动路径、single-flight worker、扫描 Start/Rescan 与 callback 消费 | F1/F2/F3 阻断完整闭环 |
| 安装全入口 | envcheck 三个汇聚点，sync Install/Update/WithMethod，async runOperation/WithMethod，fix 转发；Windows 失败、headroom/uninstall 排除 | 接缝与主要入口抽检通过；Windows succeeded + WSL failed 保留双侧语义，错误未被覆盖为全成功 |
| 五 CLI/目标/锁/超时 | 五键映射（含 omp）、安装已装短路/升级 @latest、操作内一次 distro 解析、per-distro 锁、默认 probe 与长步骤预算、非 Windows nil runner | 逻辑抽检通过；生产默认发行版列表为进程缓存，未据测试接缝可以翻转目标就虚构真实目标漂移缺陷。超时杀 wsl.exe 是否清除 Linux 子进程仍须真机核对 |
| 前端/绑定 | warning 横幅、同步 wrapper、生成 App/InstallResult/Unified 模型字段与方法匹配、两套 E2E 脚本和 fixture | F4；生成器运行历史依据 verification.md，不能仅由 diff 独立证明生成过程。未手改生成目录 |
| 兼容/迁移 | Unified 字段增量、显式 SelectedKeys、保留旧选择协议、同代基线、WSL 可选结果字段 | 未引入通配降级风险；未跑真实旧二进制降级。源码 no-star 证据不等于任意降级组合均验证 |
| 性能/维护性 | 单 worker、扫描有界、安装 per-distro 串行、每资源多次身份/状态探测 | 未发现本轮明确性能回归反例；数百资源与慢 UNC 未压测，不作性能通过保证 |

## 4. 本轮实际验证

cwd 默认 `/Users/run.mao/workpace/amagi-codebox`；均未重复全套构建/全量测试。

| 命令 | 实际结果 |
|---|---|
| `go test ./internal/wslsupply -run 'Test(SetTargetUnifiedLifecycle\|AutoConsent\|ExtendUnitSelection\|UnifiedPrimitivesCancel\|ScanCompletion)' -count=1` | exit 0，0.613s |
| `go test ./internal/envcheck -run TestAttachWSLSync -count=1` | exit 0，2.269s |
| `go test . -run 'TestWSLUnified\|TestWSLCliConfigAtLaunch\|TestSyncCLIToWSL' -count=1` | exit 0，2.438s |
| `go test ./internal/wslsetup -run 'TestOperationHoldsOneDistroIdentity\|TestSameDistroOperationsSerialize\|TestDifferentDistrosDoNotBlock\|TestInstallSearchTools\|TestCommandFailuresReported\|TestLongStepsBoundedByTimeouts\|TestUpdateToolUpgradesInstalledCLI' -count=1` | exit 0，0.522s |
| `go run ./agent-outputs/workflow/windows-wsl-unified/repro-resource-cache.go` | exit 0；确认 F1 反例，手动重扫对照恢复 |
| `npm exec -- vitest run src/__tests__/views/settings/envcheckSolutions.test.ts src/__tests__/api/wslSupply.test.ts`，cwd=`frontend` | exit 0，2 文件 / 58 用例通过 |
| `git diff HEAD --check` | exit 2；生成的 models.ts 四处行尾空白。生成器格式问题，不作为行为 finding、不要求手改生成文件 |

证据可信度：已有 Go 全量/race/vet/Windows build/frontend build/413 单测/24 浏览器 fixture 通过来自 `verification.md`，本轮未重跑，不能算独立复验。新增入口测试真实调用 App/Coordinator，但使用临时目录/内存凭据/fake probes；wslsetup 为 fake runner，浏览器为真实 Vue + 内存 Wails，均不是真机 Linux CLI 消费证据。测试确能证明正向路径，不覆盖 F1 的后续来源更新、F2 的活动会话时序、F3 的部分持久化失败及 F4 的兄弟卡片刷新。

另一个证据弱点：`TestSetTargetUnifiedLifecycle` 及 App GenerationChangeAndDisable 的换代断言在先停用后执行，不能单凭它们证明“仍启用时直接换代失效”；源码实际按 generation 校验，未据测试缺口虚构安全缺陷。建议实施者补直接换代回归。

## 5. 已知范围、既有观察与真机建议

- direction §1.6 明确未覆盖：OpenCode 用户 opencode.json 中 provider 附加层之外配置、omp models.yml 之外配置、Claude/Codex 订阅 OAuth、全引擎插件迁移。**五 CLI 安装/升级覆盖不等于五 CLI 所有配置均统一**；这些是已声明范围缺口，不冒充本轮新缺陷或“全量配置统一已完成”。对外说明应保留此限制。
- 旧的 distro 状态/列表进程缓存、single-flight 在退出边缘的 rerun 窗口、登录解析探针失败视为 inconclusive 等是既有行为；本轮不单列为新回归。尤其“停止自动纳入新项”不应宣传成强撤回：既有授权副本和 lazy 语义保留。
- Windows 真机建议：五 CLI 各做首装/已装升级和实际版本消费；默认 shell=wsl 与非 WSL 但已装的门控；断网/npm/NodeSource 失败及 UI 双侧结果；超时后 Linux 进程和 npm/apt 锁残留；UNC 0600/链接/手改冻结；重装或换默认用户后重授权；启动延迟及数百资源规模。优先加上 F1–F4 的验收时序。
- 真机未验证本身不是 finding；本轮没有足够证据宣称真实 wsl.exe、9P 权限、npm 安装和 CLI 模型消费已经通过。

## 6. 产物与回滚

本轮无产品修改，仅本报告和取证 Go 程序；如需撤回只移除这两份审核产物，不触碰主上未提交集成代码。复现程序只在系统临时目录写隔离 fixture，结束时删除，不安装、不访问用户 HOME 配置。

建议先由实施者处理 F1–F3，再做定向复审；F4 可随同收尾。独立检验不提供修复 patch。


## 7. 增量复审（本阶段唯一一次 Major 修复定向复审）

**最终 VERDICT: PASS**

本次限定为原 F1–F4 关闭核验，以及修复直接引入的生命周期、并发、停用/换代、停机不唤醒保护；没有重开全量审计。以上初审 FIX、原 finding 与原验证记录全部保留，作为历史证据。当前复审范围内剩余：**Critical 0 / Major 0 / Minor 0**；未发现可证实的修复新增回归。

### 7.1 输入与实际代码

全文读取同目录 `review-fix.md`、`review-fix.patch`、更新后的 `verification.md`；并核对主树实际 `app_wsl_unified.go`、`app_wsl_supply.go`、`app.go`、四个新测试、前端三个组件、浏览器断言和用户文档。修复补丁未改变 internal/wslsupply 生产授权/选择协议或公开绑定签名。另读取 `postfix-{go-tests,race,vet,windows-build}.log` 与 `browser-install-f4.log`。

### 7.2 原编号逐项结案

| 原编号 | 结论 | 修复与复核证据 |
|---|---|---|
| F1 Major | **关闭** | `app_wsl_unified.go:241-268,309-374` 在 Enable/Startup 启动 App 后台循环，rescan 定时器调用 RescanSkills 与两个 CLI 的 RescanCliResources；扫描完成仍走已有 callback→push。新 `TestWSLUnifiedPeriodicRescanFollowsSourceChanges` 在首次供给后修改 alpha/CLAUDE.md、新增 beta，不手动检查/保存，仅真实 loop 即交付；来源删除保留目标，重扫次数保持节拍有界。Coordinator.Start 继续幂等是正确的既有契约，旧原语级复现程序不能再代表已修复 App 的用户入口。 |
| F2 Major | **关闭** | `app_wsl_supply.go:441-449` 活动会话阻挡后记录 pending；`app_wsl_unified.go:337-351,378-402` 心跳在会话归零后消费并补推，若再次被挡由推送重新标记。新 `TestWSLUnifiedPushDeferredUntilActiveSessionsEnd` 使用真实 session.Manager，running 期间确认零目标写，MarkExited 后无额外保存即可自动补齐资源和键级配置。 |
| F3 Major | **关闭** | `app_wsl_unified.go:120-137,164-173,466-503` 部分授权失败继续处理分组、照常启动恢复调度，并真实返回部分失败；每次安全推送前对缺失分组幂等补授权/物化。新 `TestWSLUnifiedPartialEnableFailureAutoContinues` 在统一标记已提交后注入身份失败，恢复后无重新启用/敏感授权即交付视觉清单、Claude、Codex 和技能；跨多个周期验证 detached 单元没有复活。 |
| F4 Minor | **关闭** | `EnvCheckSettings.vue:885-895` 终态在原 operation ID 去重后 emit；`EnvCheckView.vue:6-8,22` 将事件转为 revision；`WSLCLISettings.vue:1134` watch 触发一次 load，无新轮询。已核对 `e2e/envcheck-management-smoke.mjs:64-82`：不仅验证 GetWSLCLIStatus 再调用，还在成功场景等待兄弟卡片显示 Pi 新版本 1.0.1；读取的 browser-install-f4.log 记录成功/失败/跳过三场景通过。本轮未再次运行浏览器，该部分为静态链路核对 + Leader 已执行日志证据。 |

### 7.3 修复直接相关的保护复核

- **生命周期**：stop 取出 cancel/done 后在锁外取消并等待 done，避免持锁 join；自然退出按 done 令牌清自身槽，不会误清后起循环。Disable 成功后 stop，Shutdown 前段同样 stop。测试实际覆盖自然退出清槽及重新 Enable 后继续运行；没有把“cancel 字段非 nil”单独当成周期有效的全部证明。
- **停用/换代/保留本地**：新续接仍调用原 AutoConsentIfUnified，同一次 state mutate 校验当前 generation 的 Unified，跳过 Consented/Detached；没有用缓存标记替代实时授权门。直接换代的新测试在未先 Disable 时改变 machine-id/Home，验证自动/手动路径均不写新身份，重新显式 Enable 后恢复；后续自然退出再启用也可运行。旧代缓存短期显示 Unified=true 的观察仍是既有显示语义，不能当成新身份继承授权。
- **不唤醒与活动会话保护**：doPush 的 distro Running 守卫仍在任何固定单元续接/RunUnits 之前；新定时重扫只读 Windows 来源目录，pending 心跳只调度受同一守卫约束的 push。未新增绕过停机守卫的写路径；真实 Windows 停机/唤醒行为本轮仅静态核对，未冒称真机复验。
- **并发/调度规模**：推送继续复用既有 single-flight，重扫由独立 timer 驱动，push 不反向启动 rescan；scope 自身维持扫描单飞。pending 标记即使跨过某次观察窗口，后续推送与周期仍能收敛。对已排队 push/扫描 worker 的存量生命周期，stop 只承诺收敛循环本身；停用不撤回旧授权，未把它误写成停止所有既有副本写入的强屏障。
- **文档边界**：用户安装文档已说明 5 分钟来源重扫、活动会话结束补推、停机不唤醒、Pi 原有凭据机制例外以及 OpenCode/OMP 非全量配置覆盖。5 秒 pending 检查周期见 review-fix.md；不把这两个节拍当成实时同步承诺。

### 7.4 本次实际执行与证据可信度

独立执行（cwd=项目根，隔离临时 fixture/fake probes，无真实安装或用户配置/凭据读写）：

```text
go test -race . -run 'TestWSLUnifiedPeriodicRescanFollowsSourceChanges|TestWSLUnifiedPushDeferredUntilActiveSessionsEnd|TestWSLUnifiedPartialEnableFailureAutoContinues|TestWSLUnifiedGenerationChangeWhileEnabledDoesNotInherit' -count=3
ok  amagi-codebox  15.017s
```

exit 0；四个真实 loop 时序用例各重复三轮，共 12 次执行，race 未报错。本轮未新增项目测试、未重跑全量 Go/build/vet/frontend/browser。Leader 的 postfix 全量与交叉构建、413 前端测试及 24 个浏览器 fixture 证据按上游记录采信，不冒充本轮重新执行；空 vet/build 日志只能说明无输出，exit 0 依据 verification.md 的命令记录。

### 7.5 未覆盖与交付

- **真实 Windows/WSL 仍未验证**：wsl.exe 调用、UNC/9P 权限、Linux 子进程超时清理、实际 npm 安装/升级及真实 CLI 消费，仍沿用 §5 的真机验收建议。该事实不是新增缺陷，也不改变本次限定范围的 PASS。
- 并发压力/停用与启用同时到达、应用关闭时扫描与 push 已在途的任意交错未穷举；本轮结论不等于对全部跨进程生命周期作形式化证明。已核查相关锁、令牌与授权门，未发现足以新增 finding 的实际违约路径。
- 本次仅在本报告追加本节，未改产品代码。若撤回复审产物，只删除本节即可，保留初审证据与主树修复。

**最终 VERDICT: PASS（仅限 F1–F4 及修复直接影响面）。**
