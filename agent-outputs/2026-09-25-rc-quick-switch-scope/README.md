# 远程主机快速切换 · 差距分析与达成范围（讨论稿）

日期：2026-09-25 · 状态：待主上裁定 · 证据基准：仓库当前代码（master @ v1.3.88 + 未发布主机管理提交）

## 一、预期目标

> 在桌面端已配对多台 CodeBox 宿主的前提下，**快速切换主机，连续控制不同设备上的终端**。

关键验收口径：切换动作后 ≤1 秒量级即可在目标主机终端上操作；切回任一主机时，此前的终端工作区（打开的终端、激活 tab、可见历史）**原样恢复**，无需手工逐个重开。

## 二、现状证据：为什么现在做不到

### 2.1 后端：单连接顶替模型（契约级设计，非 bug）

| # | 证据 | 位置 |
|---|------|------|
| E1 | `rcConn` 为单指针；`RemoteClientConnect` 连接新主机前 `rcDropConnection("")` 无条件丢弃旧连接，`rcTerminals` 每次新建 | `app_remoteclient.go` Connect 替换阶段 |
| E2 | 切换即 `DetachAll()`：旧主机全部终端长连接（读泵/seq 前沿/outbox）销毁 | `app_remoteclient.go` `rcDetachTerminals`；`internal/remoteclient/conn.go` `TerminalManager.DetachAll` |
| E3 | 单连接是文档化约束："Remote Client 单连接：同时只能连接一台远程宿主，切换即断旧连" | `docs/user/remote-mobile.md` 已知限制 |

### 2.2 前端：全应用视图换血 + 工作区清零

| # | 证据 | 位置 |
|---|------|------|
| E4 | `switchToHost` 切换即 `remoteTerminalStates = {}`、`activeRemoteTerminalId = null`、`remoteSessions = []`、`resetRemoteConfigState()`——切回旧主机时终端、会话列表、激活 tab 全部从零重建 | `stores/remoteClient.ts` `switchToHost` |
| E5 | 对比：本机终端有 keep-alive 缓存（`mountedSessionIds` + `v-show`，"只隐藏不销毁 xterm buffer"）；远程终端仅在**同一主机内**有等价缓存（`mountedRemoteIds`），跨主机无 | `views/TerminalPageView.vue` 两侧注释与实现 |
| E6 | scope 单值 `'local' | hostID`：本机/远程 A/远程 B 完全互斥，切换 = 整个会话页+终端页数据面换血 | `stores/remoteClient.ts` `LOCAL_SCOPE` |

### 2.3 用户体验断点（当前一次切换的实际代价）

1. 旧主机所有终端断连销毁（E2）
2. 新主机完整 Connect 往返（凭据加载 → host/summary 验证 → 建连接，LAN 数百 ms）
3. 会话列表清空重拉（4s 轮询重启）
4. **手工逐个**重新打开终端；历史输出靠 attach 协商 + backfill 恢复（协议已支持，`backfill.go`），但"哪些终端开着、激活哪个"的记忆丢失

结论：底层终端协议（seq/backfill/outbox/重连退避）已为"断开恢复"做了充分设计，**缺口集中在连接生命周期模型与前端工作区状态管理**，不需要动远程协议。

## 三、达成范围（分阶段）

### P0 快速切换闭环（建议先做；前端为主，协议零改动）

1. **Per-host 工作区分桶**：`remoteTerminalStates` / `activeRemoteTerminalId` / 会话列表及加载态按 `hostID` 分桶持久（含本机↔远程往返）；切换 = 切换视图到目标分桶，不再清零（对应 E4/E6）。
2. **自动恢复终端**：切回主机时按分桶记忆的打开清单自动 re-attach（复用 attach 协商 + backfill 回放历史 + outbox 幂等，协议现成）；激活 tab 同步恢复。
3. **会话列表缓存先行**：目标主机分桶若有缓存列表先渲染（带既有 stale 标记），后台刷新替换——消除"列表闪空"。
4. **切换过渡态收敛**：连接中保持旧视图 + 顶部进度指示（scope 在 Connect 成功后才切，现状已如此，补指示器与失败回落文案）。
5. 回归边界：revoked / gap / 重连失败等既有 fail-closed 语义不因分桶改变（事件已带 `hostId`，路由可按主机隔离）。

P0 完成后：切换代价从"断连+重建+手工恢复"降为"一次连接往返 + 零手工恢复"，达成验收口径。

### P1 连接保活（可选增强；后端架构演进）

- `rcConn` 单例 → per-host 连接表（`map[hostID]*connection`），切换 = 指针切换（零重连）；
- 保活边界：连接数上限（建议 2~3）、无 attach 终端且空闲超时（建议 5min）自动降级断开、revoked 仍即时 fail-closed；
- 离场主机终端长连接保活，输出实时持续入前端分桶（切回零回放）；
- 需同步：单连接契约文档、移动端无影响（独立设备）、宿主端零改动（多设备并发已是既有能力）。

### P2 多主机同屏（远期可选）

终端页按主机分组 tab，多设备终端并排可见。超出"快速切换"目标，仅在 P1 稳定后评估。

## 四、风险与裁定点

1. **P0/P1 取舍**：P0 不动后端、风险低、已能满足验收口径；P1 收益是"零重连 + 离场实时缓冲"，代价是连接生命周期架构改造。建议 P0 先行，P1 视 P0 实测体验决定。
2. **自动 re-attach 的服务端 retained window 上限**：离开时间过长时历史可能不完整——沿用既有 GapNotice 如实呈现，不静默补假。
3. **分桶内存边界**：终端输出 buffer 上限沿用现有策略，主机移除（ForgetHost）时同步清理对应分桶。
