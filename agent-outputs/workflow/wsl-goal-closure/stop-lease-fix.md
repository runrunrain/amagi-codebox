# Stop-Lease 修复报告：同步终态释放的每会话完成屏障

- 任务：在干净基线（worktree `amagi/wsl-goal-stop-lease`，HEAD 58e7577）独立修复已确认的本地会话终态释放竞态；不导入 WSL 大补丁，只交净增量。
- 上游输入：`agent-outputs/workflow/wsl-goal-closure/stop-lease-diagnosis.md`（必读，已读全文）；`g1-integrated-full-go.log:316` 附近（已读 280-380 行）。
- 产物：`stop-lease-fix.delta.patch`（app.go +6 / headroom_facade.go +59-7 / external_headroom_lease_test.go +122，仅 3 文件净增量）。
- 验证日志：worktree `.amagi/stop-lease/00-11*.log`（本树内，不入库）。

## 根因（引诊断 C2-C4，本轮已先做确定性复现证实）

`releaseSharedLeases` 旧实现把「App registry 摘除（sharedLeaseMu 临界区）」与「coordinator ReleaseExact」隔在无锁的 `completeExternalDurableRun`（cleanupstore.Store.Complete 文件写）两侧：monitor 抢先摘除并阻塞在 durable 写时，同步 StopSession 已确认 terminal 却因 registry 为空而 no-op 返回，LeaseCount 仍 1。确定性复现：门控 Store 把第一笔 Complete 停在 channel 握手上，旧实现下 StopSession/同会话释放者微秒级抢跑返回（00-newtest-oldimpl-must-fail.log，0.06s FAIL）。

## 修复设计（headroom_facade.go）

`releaseSharedLeases` 改为**每会话整轮释放完成屏障**：

1. 首个调用者在 `sharedLeaseMu` 临界区内摘取该 SessionID 全部租约并登记 `sharedLeaseReleases[sessionID]` gate（App 新增小字段，app.go），随后释放锁、独占本轮。
2. 本轮顺序保持不变：`completeExternalDurableRun`（durable 完成尝试）**先于** `ReleaseExact`——未交换持久化顺序，durable 写失败仍保留保守 stale authority，重启 replay fail-closed 语义不变。
3. 并发同会话释放者不再 no-op 抢跑：等待同一 gate（channel close 即「完成事实」），醒后重查 registry（屏障期间新登记的同会话租约仍会被后续轮次释放，保持旧语义），无在飞轮则自任新轮 owner。
4. gate 在 map 条目删除之后 close（defer 保证 panic 安全，waiter 不可能悬挂）；close(gate) happens-before waiter 返回，因此 **StopSession 同步终态路径返回 nil ⇒ 本会话租约释放责任已完成**。

### 实现要求逐项映射

| 要求 | 落点 | 状态 |
|---|---|---|
| 同步 StopSession 返回时释放责任已完成；并发释放者等同一完成事实 | releaseSharedLeases 屏障 + StopSession 调用点未改语义即获得 | ✅ 新测试 647-669 行断言两者均不能提前返回 |
| Stop accepted 但 IsRunning 仍 true 的异步分支不变 | app.go StopSession 早退分支未动（return nil，lease 归 monitor/reaper） | ✅ 未触碰 |
| 失败 Stop 不放租约 | StopSession err 早退分支未动 | ✅ 原测试 :304-311 仍过 |
| 晚到旧 run 回调不能释放新 epoch | releaseSharedLeasesExact（epoch 精确）未动；屏障只加在整会话变体 | ✅ |
| durable 完成先于 coordinator 释放的顺序保留 | finishSharedLeaseRelease 顺序未换 | ✅ |
| 不持全局锁跨磁盘等待 | gate 期间不持 sharedLeaseMu；锁序仍 App.sharedLeaseMu → coordinator.mu；shutdown ClearAll 栅栏（closeSharedLeasesForShutdown）不经过本屏障 | ✅ 代码结构 + race 干净 |
| 不改既有断言、不用 wait/sleep/skip 修绿 :317 | 原测试 0 改动；317 行立即断言因中间态消失而成立 | ✅ |

## 确定性回归测试（external_headroom_lease_test.go 新增，不动既有测试）

`TestR6_001_StopSessionWaitsForInFlightSessionLeaseRelease`：包装真实 FileStore 的 `r6GatedCleanupStore` 仅门控第一笔 Complete（其余透传）；fake 进程 terminal → monitor 摘取并停在 durable 写（`<-entered` 为确定性前提，非抽签）；断言 (a) 停驻期 LeaseCount==1；(b) StopSession 与直接 releaseSharedLeases 均不能在 300ms 负窗口内成功返回（旧代码微秒级返回，必被捕获）；(c) 放行后两者返回 nil、LeaseCount==0、状态 Stopped；(d) 后续调用幂等即回。杀虫力验证：临时回退旧实现 → 确定性 FAIL（00-*.log），恢复修复 → PASS。

## 验证证据（均 `env -u AMAGI_CODEBOX_AI_PROXY`，worktree cwd，退出码独立判读）

| 验证 | 命令 | 退出码 | 日志 |
|---|---|---|---|
| 新测试（修复态） | go test -run TestR6_001_StopSessionWaits... | 0 | 01 |
| 旧实现杀虫证明 | 同上（临时回退 releaseSharedLeases） | 1（预期） | 00 |
| R6 全组普通+verbose | go test -run TestR6_001 | 0 | 01 |
| R6 race ×3 | go test -race -count=3 -run TestR6_001 | 0 | 02 |
| external lease/cleanup suites 普通 | TestR6_001/R7/R8/R9/R10/R11_001/R11_002/cleanupstore | 0 | 03 |
| 同上 race | TestR8_/R9_/R10_/R11_001/R11_002 -race | 0 | 04 |
| 根包全量普通 | go test -count=1 . | 0 | 05 |
| 根包全量 race | go test -race -count=1 . | 1（仅 TestEnableWSLPiConfigSupply，基线既有） | 06 |
| go vet ./... | | 0（仅既有 keychain deprecation 警告） | 09 |
| gofmt | gofmt -l 三文件 | 无输出 | — |

## 基线其它失败（单列，均有 HEAD 基线复现证据，不在本修复范围）

- `TestEnableWSLPiConfigSupply` race（wsl_s2_remote_test.go:457-458 ↔ app_wsl_supply.go:260，本机基线 58e7577 单测 -race 同样 FAIL：11-wslpi-race-baseline.log）。
- 普通全仓 4 包环境相关失败，基线同样失败（08-fullrepo-baseline-pkgs.log）：internal/envvars `TestSetPathAllow`；internal/platform `TestDarwinResolveExecutableFindsCodexInLocalNodeBin`、`TestWSLFileMTime`；internal/wslsupply `TestPluginDefaultRunnerHonorsCanceledContext`、`TestScanSkillCompatChecks`、`TestScanSkillLinkNameExcluded`。

## 声明、边界与回滚

- embed 产物复用：worktree 内 `frontend/dist`、`mobile/dist` 复制自主树当前真实已构建产物（非空占位），gitignore 不入 patch。
- 未覆盖/风险：屏障使同会话并发释放者在 durable 写期间阻塞等待（有界：一次文件写 + ReleaseExact）；shutdown 路径 `closeSharedLeasesForShutdown` 不经屏障，行为不变。monitor 唤醒后日志「进程已退出」可能因等待屏障略延后，无语义影响。
- 回滚：`git apply -R stop-lease-fix.delta.patch`（或 revert 三文件改动）即可完整还原；无数据迁移、无配置变更。
- 未 commit/push；worktree 改动留在工作区由 Leader 汇总。
