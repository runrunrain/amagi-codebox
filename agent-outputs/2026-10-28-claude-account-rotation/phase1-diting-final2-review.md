# Phase 1 第四次收窄复核

- **F10：关闭**｜`claude_accounts_launch.go:105-145,712-718`：已有 marker 不再凭 Stat 放行，逐轮重新 open→文件 Sync/Close→父目录 Sync/Close；错误保持 blocked。`claude_accounts_marker_retry_test.go:11-76` 实际覆盖创建后 file/directory Sync EIO、连续三轮 refresh blocked、重建 App 仍 blocked、恢复同步后才 managed，并逐轮计数确认重试。
- **R04：关闭**｜`app.go:750-752`、`internal/remote/remote_session_adapter.go:219-258`：remote 与桌面均调用 `app.claudeCredentialsFingerprint`，缺指纹回调/计算失败直接拒绝，不回退零指纹；取得 reservation 后复核 absent，失败释放。`claude_accounts_mixed_launch_test.go:63-144` 覆盖桌面运行 lease→remote Claude/Pi create+restart，以及 remote create/restart Prepare 在途→桌面成功启动，同时保持首次 capture busy；`internal/remote/credential_absence_test.go:12-34` 覆盖缺依赖、计算失败、复核失败和 reservation 清理。
- **本 diff 新问题分级：无新增 Critical/Major/Minor finding。** 本轮声明范围内 Phase 1 Gate 通过，可由主上决定合并并进入 Phase 2；不替代真机发布签收。

## 基线、输入与范围

源码根（下文相对路径基准）：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/claude-account-rotation/`。
实查 HEAD=`693d963c28f93b7afca83ce68fef84aa8112b89a`，增量 `cab85ae..693d963`，8 文件 +482/−10。提供 diff 与实际 Git diff SHA-256 均为 `bcdadaf202d9c9c96122f1851901290028399e4e7ec4b76990be8221f4c576e3`。

完整读取的上游 artifact：
- `/Users/run.mao/workpace/amagi-codebox/agent-outputs/2026-10-28-claude-account-rotation/phase1-diting-final-review.md`
- `/Users/run.mao/workpace/amagi-codebox/agent-outputs/2026-10-28-claude-account-rotation/phase1-fix3.diff`
- `/Users/run.mao/workpace/amagi-codebox/agent-outputs/2026-10-28-claude-account-rotation/phase1-fix2-report.md`

只审核 F10/R04 与完整增量新增攻击面及其直接消费链。其余上轮关闭项不重新审计。8 文件中含 186 行修复报告；产品修改另有 App 同源接线/测试注入字段、旧屏障 fixture 接线，不是仅两个产品文件。

## 对抗检查与验证可信度

- **F10 错误重试**：检查 existing 分支没有截断/重写文件或成功缓存；新文件仍独占创建。文件 Write/Sync/Close 任一失败均拒绝，目录失败亦拒绝，下一轮重做确认。marker 只按存在性消费，不依赖内容；此前 Write 失败留下空文件并不绕过本轮所需的文件/目录同步确认。注入测试先执行真实 Sync 再返回 EIO，证明错误传播与重试，不是实际硬件断电/磁盘 EIO 实验。
- **R04 兼容与互斥**：核实桌面 `beginClaudeCredentialsClaim` 的同源指纹与生产 setter 接线；create/restart 在 Prepare 前取 reservation 且 defer 释放（adapter `:660-668,1165-1173`）。新增指纹计算不移除既有 mutation 互斥/absent 复核，也不改 coordinator 不同指纹拒绝语义。
- **测试有效性/资源退出**：mixed-launch 正向不是只测 helper，实际走 App.LaunchSession 和 remote adapter/executor，断言真实 fixture PTY effect 两次；反向在 Prepare 屏障暂停，先验证仅 remote reservation 已可阻止 capture，再成功提升桌面 lease。屏障 defer 关闭并等待 goroutine，cleanup StopSession 并等待 lease 清空。反向用 Claude remote；正向含 Claude/Pi，不声称完整桌面/remote CLI 笛卡尔积。
- **新增攻击面**：覆盖标记 open/同步/错误清理、同源回调失败、获取与复核时序、reservation 生命周期、三份新增测试的隔离与同步、接线消费者一致性。nil absence-check 的既有未接线兼容分支未改，本轮新增的 fingerprint fail-closed 作用于已启用 absence-check 的路径，不将旧兼容行为伪报为新回归。每轮同步的成本是本次持久化确认要求的直接代价，无性能优化承诺。

## ACHIEVEMENT: 验收→证据→状态

以下命令均在上述源码根实际执行；均 exit 0，L2 定向复核，不引用实施者全量绿灯代替自行验证。

| 验收 | 实际命令 / 证据 | 状态 |
|---|---|---|
| F10/R04 正反路径及相关首次 capture 屏障 | `go test -race . -run 'TestMarkerPersistenceFailureRetriesUntilConfirmed|TestAbsentDesktopLeaseAllowsRemoteCreateAndRestart|TestAbsentRemotePrepareAllowsDesktopLaunch|TestMarkerErrors|TestAbsentRemotePrepareReservationBlocksFirstCapture|TestAbsentDesktopLaunchReservationBlocksFirstCapture' -count=10` → `ok amagi-codebox 4.188s` | 通过 |
| 指纹失败关闭、清理与既有凭据 kind 约束 | `go test -race ./internal/remote -run 'TestCredentialAbsenceFingerprintErrorsFailClosed|TestCredentialsKind_' -count=1` → `ok amagi-codebox/internal/remote 1.622s` | 通过 |
| 原 R04 仲裁原语与相同指纹对照 | `go run -overlay=/Users/run.mao/workpace/amagi-codebox/agent-outputs/2026-10-28-claude-account-rotation/phase1-final-reservation-overlay.json agent-outputs/2026-10-28-claude-account-rotation/diting_reservation_repro.go` | 通过，详下 |
| 范围与现场保护 | `git rev-parse HEAD`、diff stat/双 SHA-256；`git diff --check`、`git diff cab85ae..693d963 --check`；测试前后 `git status --short` 相同，tracked 无改动 | 通过 |

原脚本 `/Users/run.mao/workpace/amagi-codebox/agent-outputs/2026-10-28-claude-account-rotation/phase1-final-reservation-repro.go` 保持原样，其真实输出：

```text
absent desktop active -> remote zero fingerprint: shared service is in use by active sessions
same state, same fingerprint control: <nil>
absent remote pending -> desktop canonical fingerprint: shared service is in use by active sessions
```

这证明 coordinator 仍拒绝不同指纹、相同指纹可共存；脚本刻意使用旧零指纹，输出拒绝并不表示 R04 未修复。adapter 参数已经改为同源指纹的证据来自源码接线和本轮实际混合入口测试，不能只靠这个原语脚本签发关闭。

## 未覆盖、风险与回滚

未重跑全量 Go/vet/前端测试与构建；本次修复无前端变更。未访问真实账号/Keychain、网络身份、实际 CLI/远端 UI、浏览器/Wails 事件桥或硬件故障，真实发布门继续保留。测试中的 launcher/PTY/身份/PoolStore 为明确隔离 fixture，不冒充真机端到端。未审计 diff 外的其余已关闭项。

本轮仅新写本报告；未改源码、未写项目测试、未提交或操作他人现场，原复现/overlay 只读取执行。撤回本轮产物仅需由 Leader 撤回此报告，无源码回滚动作。

VERDICT: PASS
