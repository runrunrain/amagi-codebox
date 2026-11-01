# P4 合并修复批次：review.md F1/F2/F3 + 直接换代回归

修复 worktree：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/windows-wsl-review-fix`（分支 `amagi/windows-wsl-review-fix`，改动留在工作区，未 commit）。

上游输入：`agent-outputs/workflow/windows-wsl-unified/review.md`（FINDINGS F1–F3）、`app_wsl_unified.go`、`app_wsl_supply.go`、`internal/wslsupply/{skills,resources,unified,coordinator}.go`、`verification.md`。

## 结论

F1/F2/F3 三个 Major 全部修复并有真实后台 loop 驱动的入口回归；按 Leader steer #1 补齐循环槽自然退出清理（done 令牌匹配）与「换代后重新 enable → 周期继续运行」回归；按 review §4 建议补「授权仍开启时直接换代」回归。改动面严格限定：`app.go`（App 字段 + Shutdown 生命周期段）、`app_wsl_supply.go`（doPush 两处接缝）、`app_wsl_unified.go`（循环 + 续接）、`app_wsl_unified_test.go`（夹具增强 + 4 个新测试）。未改 frontend / docs / envcheck / 生成绑定 / internal/wslsupply 生产代码。

## 修复明细

### F1｜自动资源对账永远复用首次扫描快照

- 根因：`reconcileWSLUnifiedResources` 只消费旧 Snapshot；`StartSkillsScan/StartCliResourcesScan` 一次性幂等（SA-17），首次扫描后没有任何自动刷新来源快照的路径，新增/修改的技能与 Claude/Codex 资源永不跟随。
- 修复：新增统一管理专用后台对账循环 `wslUnifiedReconcileLoop`（单实例，`app_wsl_unified.go`）。**rescan 心跳（默认 5 分钟，`wslUnifiedRescanInterval`）**调用既有 `RescanSkills` + `RescanCliResources(claude/codex)`（有界、只读 Windows 来源目录）；扫描完成走既有 `SetScanCompletionCallback → wslUnifiedOnScanDone → scheduleWSLSupplyPush → doPushWSLSupply → reconcileWSLUnifiedResources` 链路，对账消费新快照。推送永不触发重扫，重扫只由定时器驱动——无「扫描完成→推送→再扫描」循环。扫描不进任何启动等待链。
- 来源删除保护不变：对账候选仍只来自新快照；来源消失的已托管单元不在候选内，目标副本按 source-deleted 保留（删除需确认），不自动删。
- 循环启动点：`EnableWSLUnifiedSupply`（SetTargetUnified 成功后立即启动，部分失败也不影响）与 `scheduleWSLUnifiedStartupReconcile`（Startup，已启用时跨重启恢复）；`DisableWSLUnifiedSupply` 显式停止。未启用统一管理时循环不存在，手动管理模式（显式「检查」才重扫）行为零变化。

### F2｜扫描完成/启用推送被活动会话挡住后无恢复触发器

- 根因：`doPushWSLSupply` 在 `hasActiveSessionsForWSLPush` 早退，无任何待办保留；worker 结束后没有恢复调度。
- 修复：进程内延期标记（review 允许「持久或进程内」）+ **pending 心跳（默认 5 秒，`wslUnifiedPendingTickInterval`）**。被活动会话挡下的推送置 `wslUnifiedLoop.pushPending`；循环心跳在会话归零后原子消费标记并补推一次（复用 single-flight `scheduleWSLSupplyPush`；推送自身守卫若再次被挡会重新置标记，竞态安全）。活动会话不写、停机 distro 不唤醒两条既有守卫原样保留（停机早退不置延期标记——停机场景仍按既有「下次启动 lazy 兜底」语义）。
- 收敛与防泄漏：Disable 同步 `stopWSLUnifiedReconcileLoop`（cancel + join `done` 通道后返回，清空 cancel/done/pushPending 槽）；`App.Shutdown` 首段同样显式停循环；循环 ctx 父级取 Wails Startup ctx（应用关闭自动取消），select 即时响应 ctx.Done。

### F3｜部分启用失败后固定单元永不自动补授权

- 根因：`EnableWSLUnifiedSupply` 在 `SetTargetUnified` 已提交后，任一分组 AutoConsent 失败即中途 return；此后 UI 按 Unified 标记隐藏启用入口，保存/启动路径不再对固定单元调 AutoConsentIfUnified。
- 修复（对账幂等续接方案，未动 UI）：
  1. Enable 分组失败不再中断：逐组继续尝试并汇总 `统一管理部分授权失败（…）；成功分组保持不变，失败分组将随自动对账续接` 错误（仍逐单元返回 outcomes）；SetTargetUnified 标记不回滚；收尾的物化/扫描/推送照常执行。
  2. 新增 `reconcileWSLUnifiedFixedUnits(distro)`，在每次 `doPushWSLSupply` 过守卫后首先执行：对未就绪固定分组补 `AutoConsentIfUnified` + 选择集物化；写入由同一推送的既有分支（pi/shared/键级单元）完成。`wslUnifiedFixedUnitsSettled` 读状态缓存判定「同代已同意或 detached」——全部就绪时零探测成本跳过（不额外 spawn wsl.exe）。
  3. 保护不变：AutoConsentIfUnified 跳过同代已授权与 detached（不复活保留本地、不动 hash 基线、无需用户再次敏感授权）；换代（UnifiedCached=false）时续接为 no-op。保存推送 / 扫描完成回调 / 后台 rescan 周期 / Startup 对账任一入口都可驱动收敛，最迟一个 rescan 周期（默认 5 分钟）内自愈。

### Steer #1｜循环槽自然退出清理 + 重新 enable 回归

- 问题：`start` 仅检查 `cancel != nil`；循环因换代/ctx 自然退出后只 close(done) 不清槽，同 App 重新 enable 永远起不了新循环。
- 修复：goroutine 退出路径按 **done 令牌匹配**原子清理自身槽（`wslUnifiedLoopRetire`：仅当 `wslUnifiedLoop.done == done` 才清 cancel/done；stop 已接管或后起新循环时 no-op，不会误清后起循环）。回归见下 TestWSLUnifiedGenerationChangeWhileEnabledDoesNotInherit 尾段：统一标记关闭（不经 App.Disable）→ 循环自退并清槽 → 重新 enable 必须起新循环，且 enable 之后的下一笔来源变化仅靠新周期交付。

### 补充回归｜授权仍开启时直接换代不继承（review §4 证据弱点）

- 场景：不先停用，machine-id 直接变化（重装、新 HOME）。旧代授权不写新身份：自动周期照跑但键级/树单元均按新代 awaiting-consent fail-closed（旧目标不被改写、新目标零写入）；手动 `ApplyWSLCliConfigSupply` 同样不继承；换代后重新 enable 在新代恢复供给（含仅靠自动周期的跟随）。
- 注意（如实说明）：换代后 `UnifiedCached` 读取的是该 distro 最近代记录——新代尚无任何记录时短期显示旧代标记（陈旧显示），写入保护由各 RunUnits 路径的实时 ResolveTarget + 同代 consent 校验保证（fail-closed，测试已覆盖）。这是既有缓存显示语义，本批未改。

## 新测试（app_wsl_unified_test.go，全部走真实后台 loop，非直调 Tick helper）

| 测试 | 覆盖 | loop 注入节拍 |
|---|---|---|
| `TestWSLUnifiedPeriodicRescanFollowsSourceChanges` | F1：首次供给后修改+新增技能与 Claude 资源 → 无任何显式检查、无手动保存，仅靠真实自动周期跟随；来源删除后目标副本保留（既有保护）；重扫计数按定时器节拍有界增长（无限循环会爆炸） | pending=10min / rescan=200ms |
| `TestWSLUnifiedPushDeferredUntilActiveSessionsEnd` | F2：真实 session.Manager 会话 running 时启用推送与扫描完成推送全部被挡且零写入（确定性：wait push idle + 心跳空转窗口）；MarkExited 后无任何外部保存，延期对账自动补齐（技能 + 键级单元） | pending=80ms / rescan=10min |
| `TestWSLUnifiedPartialEnableFailureAutoContinues` | F3：探测 #1（SetTargetUnified）成功后身份注入短暂失败 → Enable 部分失败但 Unified 保持、逐单元 outcomes 返回；环境恢复后不重新启用/不额外授权，视觉清单（固定组 #2，fake 字节来源）+ claude + codex + 树单元全部自动续接收敛；KeepWSLPiConfig 后 detached 单元跨多个自动周期不被复活/改写 | pending=80ms / rescan=200ms |
| `TestWSLUnifiedGenerationChangeWhileEnabledDoesNotInherit` | 直接换代不继承（自动+手动路径都不写新身份）+ steer #1：统一标记关闭 → 循环自然退出清槽 → 重新 enable 起新循环且后续来源变化仅靠新周期交付 + Disable 同步收敛（cancel/done 槽清空） | pending=100ms / rescan=200ms |

夹具增强：身份探测计数/故障注入/恢复（`unifiedTestProbes`）、`SetScanCompletionCallback` 对齐生产装配（此前夹具缺失该接线）、`StatMode` 支持目录位（敏感 staging 0700 验证）、循环节拍注入 helper、`RescanSkills` 计数、cleanup 先停循环再排空推送（LIFO 顺序，防跨测试 goroutine 泄漏）。全部临时目录 + fake probes，无真实安装/用户配置/凭据读写。

## 验证证据（cwd = worktree 根）

| 命令 | 结果 |
|---|---|
| `go test . -count=1`（root 全包） | ok 13.5s |
| `go test ./internal/wslsupply -count=1` | ok 3.3s |
| `go test -race . -run 'TestWSLUnifiedPeriodicRescan|TestWSLUnifiedPushDeferred|TestWSLUnifiedPartialEnable|TestWSLUnifiedGenerationChangeWhileEnabled' -count=1` | ok 5.7s（4 个新测试 race 干净） |
| `go test -race . -run 'TestWSLUnified|TestWSLCliConfigAtLaunch' -count=1` | ok 7.3s（含既有 5 个统一管理测试） |
| `go test -race . -run 'TestWSLSharedGate|TestWSLSupply|TestGrantWSLVision|TestWslSupply|TestSyncCLIToWSL|TestAttachWSLSync' -count=1` | ok 1.6s |
| `go vet . ./internal/wslsupply` | exit 0 |

未执行：全量 `go test ./...` / `go vet ./...` / build（按任务书留给 Leader 统一收口；harness 结算会独立跑）。worktree 内 `frontend/dist`、`mobile/dist` 为我创建的 gitignore 占位 index.html（embed 需要，非产物，不进 diff）。

## 默认节拍（供 Leader 更新用户文档，steer #1）

- 来源重扫（技能 + Claude/Codex 资源）：**每 5 分钟**一轮有界重扫（只读 Windows 来源目录；完成回调触发一次推送，推送受停机/活动会话守卫）。
- 会话结束补推：**每 5 秒**心跳检查延期标记，活动会话归零后自动补推一次（无需用户再保存）。
- 两者仅在统一管理启用期间运行；Disable/应用关闭即停。

## 未覆盖项 / 风险

- Windows 真机：wsl.exe 身份探测/UNC 写入/npm 安装未验证（本批全部 fake probes + 临时目录；与 review §5 真机清单一致，未新增真机证据）。
- 延期补推标记是进程内的：应用重启后丢失（重启时 Startup 对账会推一轮，扫描完成回调会再推；缺口仅剩「停机 distro + 无任何后续触发」的组合，由既有启动 lazy 兜底，与 review 建议的最低实现一致）。
- 换代瞬间在途推送的 resolve→plan 窗口（TOCTOU）为既有设计（per-generation consent 按 RunUnits 解析时刻校验），本批未改；测试通过先排空在途推送规避非确定性。
- F4（前端跨组件刷新）由 Leader 独立处理，本批未触碰 frontend/docs。

## 回滚

改动仅 4 个文件（`app.go`、`app_wsl_supply.go`、`app_wsl_unified.go`、`app_wsl_unified_test.go`），无状态文件/绑定/生成物变化；回退时先备份当前工作区并核对本批 `review-fix.patch`，仅反向应用本批增量；共享主树中不可整文件还原，以免覆盖此前集成或主上的其他改动。若反向补丁上下文不匹配，先人工核对冲突，不强制覆盖。运行态需先停止统一管理循环并等待推送排空。
