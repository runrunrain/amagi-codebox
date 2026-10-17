# Stop-Lease 诊断报告：TestR6_001_ClaudeExternalRunRejectsStopAndUninstallUntilStopSucceeds

- 任务：只读取证，定位 g1-integrated-full-go.log:316 一次真实集成失败的原因；不改代码、不跑真实 CLI、不读凭据。
- 诊断对象：`external_headroom_lease_test.go`（主树）+ `App.StopSession` external 分支 + external run lease 释放链 + 共享协调器。
- 上游输入：`agent-outputs/workflow/wsl-goal-closure/g1-integrated-full-go.log`、`attack-stage-review-assignment.md`。
- 结论一句话：**既有（非 G1 引入）的结构性并发释放窗口——`releaseSharedLeases` 把「App registry 摘除」与「coordinator 释放」隔着一个无锁的 durable 文件写；monitor goroutine 抢先摘除时，StopSession 成功返回而 lease 尚未释放，测试 317 行「立即 ==0」断言命中该中间态。** 不是泄漏、不是丢释放、不是 flaky 的随机噪声；是「释放完成」与「StopSession 返回」之间缺少 happens-before 的确定性竞争。

## 结论索引

| # | 主张 | 状态 | 锚点 | 适用边界 |
|---|------|------|------|----------|
| C1 | 失败断言为 317 行「successful StopSession retained lease count=1」；StopSession 本身返回 nil（314 行未触发） | 观察 | external_headroom_lease_test.go:310-318；g1-integrated-full-go.log:316-317 | 本次 full-go 日志单次失败 |
| C2 | 释放链存在真实窗口：registry 摘除（sharedLeaseMu 临界区）与 ReleaseExact 之间隔 `completeExternalDurableRun`（durable journal 文件写，无锁） | 观察（代码结构） | headroom_facade.go:1041-1057；completeExternalDurableRun headroom_facade.go:946-959 | releaseSharedLeases 的所有调用方（external/legacy 终态路径） |
| C3 | 竞争双方是 StopSession 主路径与 external-run monitor goroutine；先摘除者独占释放责任，后到者变 no-op | 观察（代码结构）+推断（交错） | app.go:2919（StopSession 释放）vs app.go:2844-2854（monitor：MarkExited→release→日志） | fake 同步 Stop 语义下窗口=monitor 唤醒相位；真实 Launcher 异步终止下窗口更宽 |
| C4 | 日志「进程已退出」@30.286（FAIL 后 6ms）证明该实例的 lease 释放最终由 monitor 完成 | 观察 | g1-integrated-full-go.log:318-319；app.go:2853-2854 | 时间戳毫秒粒度 |
| C5 | 「上下文压缩已停用/代理子进程已退出」@30.280 位于 FAIL 行之前，是 t.Cleanup 的 Headroom.Stop（t.Fatalf→Goexit→cleanup 先于框架打印 --- FAIL），非 StopSession 内部行为 | 推断（前提：go test 框架在测试函数完全退出后才打印 FAIL 块） | external_headroom_lease_test.go:229-232（t.Cleanup 注册）；internal/headroom/service.go:296-306（Stop 日志）、261-265（reaper 日志） | 若日志采集器重排行序则位置证据弱化，但不影响 C2/C3 |
| C6 | 产品语义是「保守方向的迟到释放」：lease 只会晚放不会早放/不放；设计注释自述 delayed observation is conservative | 观察 | app.go:2841-2842 注释；stopAllHeadroomForUninstall 拒绝语义 app.go:4727-4734 | 紧随 StopSession 的 HeadroomStop/uninstall 可能短暂 ErrSharedServiceInUse |
| C7 | 测试断言过强：同文件 CodexNaturalExit 测试用 waitR6 等待「lease==0 && status==exited」终态，才是与异步释放设计匹配的模式；317 行要求同步 happens-before | 观察 | external_headroom_lease_test.go:335-341（waitR6 先例）vs :316-318 | 定性：产品窗口（真实，C2）× 断言同步契约（过强）叠加，非纯测试错误亦非纯产品缺陷 |
| C8 | 非 G1 引入：G1 集成 patch 不含 StopSession/releaseSharedLeases/coordinator/该测试文件的任何 diff；worktree 基线（G1 前）同链路代码逐字一致；上游记录称「既有」 | 观察 | integrated-stage.patch hunks（仅 WSL 闸门/embedded spec/NYSL 供给等）；.amagi-worktrees/wsl-goal-adapters/app.go:2853-2873 与主树 app.go:2904-2922 逻辑一致；attack-stage-review-assignment.md:11 | 「既有」= R6-001 时代引入的 releaseSharedLeases 结构 |
| C9 | race 批通过 / 普通全仓失败 = 交错概率差异，非语义差异 | 推断 | attack-stage-review-assignment.md:11（五包 -race exit0）；metadata.json:26-27（普通四包亦通过过） | 证明该失败为低概率时序命中，非确定性失败 |

## 事实清单（按问题分解）

### F1 失败现场
- 日志：`--- FAIL: TestR6_001_ClaudeExternalRunRejectsStopAndUninstallUntilStopSucceeds (0.06s)`，`external_headroom_lease_test.go:317: successful StopSession retained lease: count=1`（g1-integrated-full-go.log:316-317）。root 包 8.479s FAIL，全仓仅此一测试失败 + agent-outputs 旧 repro 编译失败（Leader 已另行处理，不在本诊断范围）。
- 测试流程（external_headroom_lease_test.go:288-322）：launch 成功（LeaseCount==1 断言过，:298）→ HeadroomStop 拒绝 ✓ → uninstall 拒绝 ✓ → 注入 stop 失败的 StopSession 返回 err ✓、LeaseCount 仍 1 ✓（:313-311）→ 解除注入重试 StopSession 返回 nil ✓（:314）→ **LeaseCount==1（:316-318 FAIL）**。
- 日志窗口 30.235-30.286 与该测试一一对应：启动→Headroom 起（30.267-269）→身份预留→「进程已启动」（30.280）→「卸载联动拒绝」WARN（30.280，app.go:4730）→「停止会话」+ERROR（30.280，#1）→「停止会话」（30.280，#2）→ [t.Cleanup 的 Headroom 停止日志] → FAIL →「进程已退出」（30.286，monitor）。

### F2 StopSession external 分支逐行（主树）
- `app.go:2904` `external := a.externalSessionLauncher()`（测试注入 fake，app.go:2244-2247）。
- `app.go:2905-2909` `external.Stop` 失败→记 ERROR「停止会话失败」→ return err（**lease 保留，注释明示**）。
- `app.go:2910` MarkStopping；`app.go:2911-2915` 复查 IsRunning：仍 running → **return nil 且 lease 保留**（「Stop accepted but Launcher Wait has not proven terminal; monitor or cleanup reaper owns the lease」）——产品对真实 Launcher（异步终止）的主路径。
- `app.go:2917-2920` 已证实 terminal → completeExternalCleanupForSession → MarkStopped → `releaseSharedLeases(sessionID)`（同步释放，测试 fake 同步 delete 走此路）。
- fake（external_headroom_lease_test.go:132-144）：Stop 成功即 `delete(l.running,id)`——同步 terminal；IsRunning（:126-130）持锁读 map。fake 下 async 分支不可达，StopSession#2 必走 :2917-2920 同步释放链。

### F3 窗口本体：releaseSharedLeases 非原子（headroom_facade.go:1041-1057）
```go
func (a *App) releaseSharedLeases(sessionID string) {
    a.sharedLeaseMu.Lock()
    // 1042-1049: 收集该 SessionID 的 lease 并 delete 出 App registry
    a.sharedLeaseMu.Unlock()                 // 1050 ← 摘除完成，此后任何人再调用本函数都是 no-op
    a.completeExternalDurableRun(sessionID)  // 1051 ← 无锁间隙：durable journal 文件写（Store.Complete）
    if a.sharedCoord == nil { return }
    for _, lease := range leases {
        _ = a.sharedCoord.ReleaseExact(...)  // 1056 ← coordinator 释放（LeaseCount 由此归零）
    }
}
```
- `completeExternalDurableRun`（headroom_facade.go:946-959）：externalCleanupMu → store.Complete(record)（cleanupstore 接口 :107-115 的 NDJSON journal 写，open/write/close 文件 IO）→ 才轮到 ReleaseExact。
- **效果**：谁先进入 1042-1049 谁独占「摘除+释放」责任；后到者收集为空、直接返回。**摘除与释放之间没有任何锁保护**，期间 lease 在 coordinator 中仍持有（LeaseCount==1），App 侧却已无任何 registry 条目可触发再次释放——释放完全押在先到者 goroutine 的后续执行上。

### F4 竞争者与交错时序（本次失败的重构）
- 主 goroutine：StopSession#2 `external.Stop` delete running（30.280）→ :2910-2920 → releaseSharedLeases（app.go:2919）→ return nil → 测试断言 LeaseCount。
- monitor goroutine（LaunchSession 启动，app.go:2844-2854）：`for IsRunning(id) { select ctx.Done / 5ms timer }`（externalRunPollInterval=5ms，测试 :223）→ 观察到 false → MarkExited → **releaseSharedLeases（:2853）** → 日志「进程已退出」（:2854）。
- 命中失败的交错：
  1. 主：fake.Stop delete running（30.280）。
  2. monitor：恰逢 5ms 定时器唤醒（≤30.285），IsRunning=false，进入 MarkExited→releaseSharedLeases，**先拿到 sharedLeaseMu，摘走 registry 条目**。
  3. monitor：进入 1051 的 durable 文件写（全仓 go test 并行高负载下毫秒级），**尚未执行 ReleaseExact**。
  4. 主：MarkStopping→IsRunning(false)→MarkStopped→releaseSharedLeases→**registry 已空，no-op**→return nil。
  5. 测试：LeaseCount==1（monitor 还没 ReleaseExact）→ **FAIL @~30.28x**。
  6. monitor：文件写完→ReleaseExact→LeaseCount=0→日志「进程已退出」@30.286 ✓（与日志时间戳 6ms 差完全吻合：唤醒≤5ms+文件写~1ms）。
- 反向交错（主先摘除）：主在同一 goroutine 内顺序完成摘除→（文件写）→ReleaseExact→return→断言，count==0 通过——这是绝大多数运行的形态，与「race 批过、普通批偶发挂、单跑大概率过」（C9）一致。
- 并发正确性边界：两个 releaseSharedLeases 本身不会双重释放（收集互斥）；失败不是数据竞争（无 race 告警），是**线性化点缺失**。

### F5 该窗口的产品语义（不构成缺陷裁决，仅陈述）
- 方向性：释放只会迟到不会提前/丢失；lease 存续期间 HeadroomStop/uninstall 被拒是保守方向。app.go:2841-2842 设计注释自述「Release happens only after the Launcher reports terminal; delayed observation is conservative (blocks mutation longer, never shorter)」。
- 但「StopSession 返回 nil ⇒ lease 已释放」在实现上不成立（真实 Launcher 走 :2911-2915 异步分支更是如此）；紧随 StopSession 的任何 coordinator 检查都可能看到中间态。测试 317 把它当同步契约，是断言与实现契约的错位（C7）。

### F6 G1 差量比对（旧行为判定）
- `agent-outputs/workflow/wsl-goal-closure/integrated-stage.patch`（G1 集成）的全部 hunks：仅 CI、WSL 启动闸门（D1/D6）、embedded spec 工作目录、codex/pi/omp 启动链、WSL 供给/插件等；**grep 无 StopSession/releaseSharedLeases/ReleaseExact/LeaseCount/external_headroom_lease_test 匹配**。
- G1 前基线 `.amagi-worktrees/wsl-goal-adapters/`：app.go:2853-2873（StopSession external 分支）与 headroom_facade.go:1041-1057（releaseSharedLeases 含同一 completeExternalDurableRun 间隙）与主树逐字一致（仅行号偏移）。
- 上游 `attack-stage-review-assignment.md:11` 亦记载「首次全仓普通 go test 另**复现既有** …立即释放 lease 断言失败」。
- **判定：旧行为（R6-001 时代引入的释放结构），非本轮 G1 差量引入。**

## 最小确定性复现方案（不依赖 sleep、不跳断言）

结构性窗口可用「门控 Store」100% 确定性复现（`App.externalCleanupStore` 是接口 `cleanupstore.Store`，app.go:321 / cleanupstore.go:107-115）：

1. 复刻 `newR6ExternalLeaseApp` + `configureR6ClaudeProvider` 完成 launch，得到 registry 中 1 个 lease。
2. 注入包装 Store：`Complete(record)` 在内置 channel `entered`/`release` 上同步——进入即 `close(entered)`，阻塞 `<-release`；其余方法透传真实 FileStore。
3. goroutine A：直接调 `app.releaseSharedLeases(id)`（或端到端：以 1ms poll 让 monitor 先行）→ A 摘除条目后阻塞在 Complete。
4. 主线程：`<-entered` 后再调 `app.releaseSharedLeases(id)`（no-op，证明摘除互斥），随即断言 `LeaseCount(ClaudeHeadroom)==1`——**此刻 StopSession 语义上已"成功"，lease 却在协调器中滞留**，窗口闭合证明。
5. `close(release)` 放行 A，`waitR6` 断言 LeaseCount==0（释放最终到达，证明非泄漏）。
- 端到端放大版（可选，仍非裸 sleep）：externalRunPollInterval=1ms + `go test -run TestR6_001_ClaudeExternalRun -count=500 -cpu 2` 叠加 CPU 负载，命中 317 行断言；仅作概率佐证，确定性证明以上述门控版为准。

## 修复边界（供 Leader 派修复时圈定，不裁决）

- 边界 A（测试侧，最小）：317 行断言由立即 ==0 改为 `waitR6` 等待终态（LeaseCount==0 && GetStatus==Stopped），与同文件 :335-341 既有模式一致；保留 StopSession 返回 nil、失败 Stop 保留 lease 等强断言不变。符合「不得改成 sleep/跳断言」约束（waitR6 是带 2s 上限的条件轮询，非延时跳过）。
- 边界 B（产品侧，若要立「StopSession 返回 ⇒ 释放完成」强契约）：把 ReleaseExact 并入 sharedLeaseMu 同一临界区（锁序 App.sharedLeaseMu→coordinator.mu 与 acquireAndRememberExternalSharedLease:129-155 一致，无死锁风险），或将 completeExternalDurableRun 移到 ReleaseExact 之后。**注意取舍**：现顺序保证 durable 完成先于 lease 释放；颠倒后若 durable 写失败而 lease 已放，重启恢复（LoadPending/LoadActive replay）会对已终结 run 误判活跃——需按 cleanupstore fail-closed 语义重审，属设计决策，不在本诊断内裁决。
- 两边界可独立实施；A 消除测试对中间态的误判，B 消除中间态本身。

## 待核验项（证据缺口）

- 本诊断在无 shell 工具面下完成，**未实际运行复现**：门控 Store 复现与 -count 放大需由具备执行面的轮次落地验证。
- C5 的日志两流交错模型（t.Cleanup 日志先于框架 FAIL 打印）依据 go test 框架行为推断，可用 `-v` 单跑对照 stdout 与 app log 时序复核；即便 C5 不成立也不影响 C2-C4 的窗口结论。
- target-race 批的原始日志未见落盘（仅 assignment.md 文字记录 exit0），无法核对该批中该测试的 monitor 相位；如需补证可复跑五包 -race 留档。
