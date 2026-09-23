# 对抗审核报告：远程 v1 会话列表「与桌面端打开会话一致」变更

- 审核对象：commit 9ef41f1（任务书称"未提交 diff"，实际已提交为 HEAD；工作区干净，git status 空。审核以该提交变更集为准）。
- 审核档位：full（7 项逐项核查）。
- 取证脚本：本目录 sort_divergence_probe*.go（DST/同秒排序分叉复现）。

## Findings

### Critical / Major
无。

### Minor

**M1｜排序两端分叉：桌面 RFC3339 字符串比较 vs 远程 time.Time 比较**
- 位置：internal/session/manager.go:315,325（桌面）；internal/session/authority.go:586-591（远程）；docs/developer/remote-api-v1-contract.md:110（承诺「与桌面端会话列表一致」）。
- 触发条件：(a) 宿主进程跨 DST 边界长驻（欧美时区），旧会话 StartedAt 偏移 +02:00、新会话 +01:00；(b) 两个会话同一秒启动（RFC3339 秒级截断并列）。
- 实际影响：场景 (a) 两端列表顺序相反（复现：A=2026-10-25T02:30:00+02:00=00:30Z，B=02:15:00+01:00=01:15Z；桌面字符串序 [A,B]，远程时间序 [B,A]）；场景 (b) 桌面 sort.Slice 对相等字符串不稳定→顺序任意，远程按纳秒+sessionId 确定→两端不保证同序。仅展示顺序不一致，不破坏功能；需求主目标（不随 lastActivityAt 跳动）不受影响。
- 证据：probe3 输出 `After: B after A = true；字符串: A>B = true → 分叉=true`；manager.go:315 `s.StartedAt.Format(time.RFC3339)`、:325 `result[i].StartedAt > result[j].StartedAt`（字符串）。
- 最小修复方向：桌面 List() 去掉二次字符串排序（collectPresentSnapshots 已按 time.Time 排过，manager.go:284-286），或对 SessionInfo 携带排序用的 time 值；同秒并列在桌面侧同样加 sessionId tiebreak。文档措辞同步（见 M2）。

**M2｜契约文档「与桌面端会话列表一致」措辞过度承诺**
- 位置：docs/developer/remote-api-v1-contract.md:110（「排序固定为 startedAt 降序（与桌面端会话列表一致），sessionId 升序作并列打破」）；authority.go:566-568 注释同。
- 触发条件：M1 两场景。
- 实际影响：文档断言的「一致」在 DST 跨界与同秒并列下不成立（桌面无 tiebreak、字符串比较）；契约读者会据此做客户端断言而失败。
- 证据：见 M1；桌面排序代码无 sessionId tiebreak（manager.go:325）。
- 最小修复方向：改为「与桌面端排序意图一致（同为 startedAt 降序）」，或按 M1 修桌面后保留现措辞。

**M3｜mobile 空态文案与新列表语义轻微失真**
- 位置：mobile/src/views/SessionsPage.vue:299-300（「还没有会话 / 从上方选择一类 CLI 启动新会话」）。
- 触发条件：宿主有已关闭会话但无打开会话（例如用户刚停止全部会话）。
- 实际影响：大厅显示「还没有会话」，但用户可能刚停止过一个——文案与感知不符，轻微困惑；不破坏功能。
- 证据：列表现在仅含 running/stopping（本次变更）；空态分支 `lobby.sessions.length === 0`。
- 最小修复方向：文案改为「当前没有打开的会话」类措辞。

### Info

**I1｜既有边界：immediate legacy 会话桌面可见、远程永不可见**
- 位置：internal/session/manager.go:74-96（`RemoteEligible: false`，注释明示「never admitted to remote projection」）。
- 说明：经 `Create`（immediate legacy creation）创建的会话无 composite activation proof，`remoteEligible=false`，ListRemoteSafeSnapshots 的既有 `entry.private.remoteEligible` 过滤（authority.go:581）在本次变更前就排除它们。桌面侧栏显示、远程大厅不显示——「列表与桌面一致」的字面表述存在这一既有例外，非本次变更引入，不阻断。

**I2｜adapter 层测试仅覆盖 exited 终态**
- 位置：internal/remote/session_list_parity_test.go:19-21（只调 MarkExited）。
- 说明：stopped/unavailable 在 v1 列表消失仅在 authority 层测试覆盖（remote_list_parity_test.go），adapter 面未直接验证。分层尚可接受（adapter 是 authority 的薄投影），列为改进建议。

## 逐项核查结论

1. **口径一致性**：已核，无发现（I1 为既有边界）。state↔status 在全部 10 个写入点（manager.go:88,172-175,186-193,203-218,228-238；authority.go:400-404,840-854,883-886,936-940,996-997,1024-1028）均于 entry.guard 下原子成对写入，映射为双射（Running↔Running/Stopping↔Stopping/Stopped↔Stopped/Exited↔Exited/Unavailable↔Failed）。桌面 runningSessions（session.ts:99-101 过滤 running/stopping）与远程 isActiveAuthorityLifecycle（authority.go:599-600）等价；pendingLifecycleID/pendingRemoveID 占用期间 Mark* 直接 return 不改状态（manager.go:186-188,203-205,228-230），两读面各自在 entry.guard 下读同一对字段，无单侧翻转窗口。AuthorityUnavailable 全部与 StatusFailed 同步出现——桌面此时显示 failed（侧栏隐藏），远程隐藏，一致。
2. **排序分叉风险**：发现 M1（已复现，Minor）；关联 M2。
3. **隐藏消费方**：已核，无发现。ListRemoteSafeSnapshots 生产调用唯一（remote_session_adapter.go:243）；v1 GET /sessions 唯一入口（session_routes_v1.go:38-40）；legacy /api/sessions 独立 handler 不受影响；stop 收据后 refresh 与 OPERATION_COPY stop 文案（SessionsPage.vue:129「停止后该会话卡片会自动从大厅清理」）一致；restart 走 RemoteSnapshotByID 不按 state 过滤（adapter:955），运行中会话 restart 不受影响；ws broadcast/state 事件、host summary、remove GC 均不依赖列表含 stopped；internal/remoteclient 为纯客户端直映射（sessions.go:28-33），桌面 RemoteSessionsView.vue 保留 stopped/exited 徽标，连旧宿主兼容；e2e 无依赖（grep 无命中）。
4. **并发与正确性**：已核，无发现。indexMu.RLock 快照 entry 指针 + 逐 entry.guard.Lock 读（authority.go:572-584），与桌面 collectPresentSnapshots 同模式；comparator 全序（sessionId 唯一索引保证 tiebreak 严格），sort.Slice 不稳定无害；isActiveAuthorityLifecycle、idsOf 全仓无命名冲突；go vet 通过。
5. **测试质量**：已核，真实有效（I2 为改进建议）。两文件实跑通过（`go test ./internal/session -run TestRemoteList`、`go test ./internal/remote -run TestAuthorityListSessionsHides...` 均 ok）；mobile timing test 6/6 通过。桌面对照断言是两独立读面非同义反复；MarkStopped/MarkExited/MarkFailed 前置成立（CommitPreparedActivation 后 phase=present、pending 字段=0、status=Running）；时间戳互差 1 秒避开自身未锁定的并列场景（与 I2/M1 呼应）。
6. **文档准确性**：主体一致（过滤口径、排序、详情端点句均与实现相符：RemoteSnapshotByID authority.go:484 不按 state 过滤，tombstone 后 NotFound）；「与桌面一致」措辞问题归 M2。
7. **契约/产品面风险**：stop 后卡片消失符合既有文案承诺（无冲突）；取消「从大厅 restart 已停止会话」入口符合需求方向（桌面侧栏同口径；API 层 restart stopped 仍开放，仅 UI 入口消失）；空态文案 M3；P2-C 裁定 A 残留已全部清理（lobby.ts、timing test 注释/标题同步），无矛盾文案残留。

## 验证记录
- go test ./internal/session -run TestRemoteList → ok；go test ./internal/remote -run 'TestAuthorityListSessionsHidesClosedDesktopSessions|TestAuthorityDesktopAndV1Share' → ok（本机，-count=1）。
- go vet ./internal/session ./internal/remote → exit 0。
- mobile: npx vitest run SessionsPage.timing.test.ts → 1 file / 6 tests passed。
- 排序分叉复现：go run sort_divergence_probe3.go → 分叉=true。
- 未运行：全量 go test ./...、前端全量 vitest（超出本轮所需链路）。

VERDICT: PASS_WITH_MINOR
