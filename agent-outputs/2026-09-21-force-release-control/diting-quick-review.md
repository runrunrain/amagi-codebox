# 谛听快审：桌面端「主动收回远程设备控制权」（quick 档）

- 日期：2026-09-21 · 审核人：diting · 范围：工作树未提交改动（git diff + 4 个未跟踪文件），`app_quota_probe*` 按任务书排除
- 对象：`internal/remote/control_arbiter.go`（ListSessionHolds + ForceReleaseControl）、`control_gate.go` 透传、`app.go` 绑定、两个 Go 测试文件、`frontend/`（RemoteSettings 卡⑤、ActivityControlCard、api/remote.ts、controlHoldsModel.ts + 单测）、wailsjs 再生成
- 方法：逐路径读源码（TakeDesktop/ReleaseDesktop/commitTransition/expireGrace/EnqueueControlTransition/SecurityEvent 联合）+ 真实运行测试取证 + diff 范围核对

## Findings

### F1｜Minor｜frontend/src/views/settings/RemoteSettings.vue:242｜注释声明不实：takeover/released 并未写本地安全记录
- **触发条件**：任何维护者依据该注释推断「桌面收回控制权」有本地审计记录。
- **实际影响**：仅误导性注释，无功能缺陷。`takeover/released` 经 `hub.EnqueueControlTransition`（internal/remote/control_event.go:363）只向**该会话在线 WS 订阅者**投递、无持久化；本地安全记录体系 `SecurityEvent` 是封闭联合（internal/remote/device.go:266，仅 pairing/device/store/service/legacy-auth 五类），注释明确禁止 session 内容，不存在控制权转移事件类型。`onHoldsChanged` 里的 `loadEvents()` 刷新因此是无效但无害的动作。
- **证据**：control_event.go:348-385（bounded memory operation, no socket I/O, 仅 subscriber FIFO）；device.go:266-285（closed union, "No … session content is permitted"）。
- **最小修复方向**：改注释为「刷新持有列表 + 事件卡」即可；若产品语义上要求收回动作可审计（联动卡⑥本地可见记录），则属上游需求澄清，不是本 diff 实现缺口。

## 逐项结论（对照任务书审核重点）

1. **收回链原子性与终态** ✅
   `ForceReleaseControl` = checkReady → public 检查 → 幂等 fast path → `TakeDesktop`（commitTransition(reasonTakeover) + cancelGraceTimerLocked）→ `ReleaseDesktop`（commitTransition(reasonReleased)）→ 终态 ownerNone。grace 复活防护是三重 exact-match（controlEpoch + attachmentGeneration + graceDeadline，control_arbiter.go:916-952）——即使 timer Stop 竞态未截住，两步链各自递增 epoch，过期回调必然 stale no-op。测试 `Advance(31s)` 断言真实（fake clock + SetGraceDuration(30s)，非缩水）。mid-chain 残留 desktop 的唯一路径：ReleaseDesktop 失败 = health latch（运行期不可恢复，全 gate fail-closed，仅 CloseForShutdown:1231 复位）/ entry tombstone（不再 public）/ preparedRemoval（即将移除）——均有兜底，源码注释声明经逐条核实属实。
2. **幂等/边界** ✅
   none → no-op 零事件（gate 与 arbiter 双层测试）；unknown → DenySessionNotFound；TakeDesktop 失败时 commitTransition 原子（owner 未变，无半程状态）。fast-path 检查与 TakeDesktop 之间存在固有 TOCTOU 窗口（读到 none 后设备恰好 acquire → 返回 no-op），属「读时无持有」的陈旧读语义，UI 刷新即收敛，不构成缺陷。
3. **ListSessionHolds 泄漏面** ✅
   仅导出 SessionID/DeviceID/DeviceName/InGrace/Since 五字段；connectionID、attachmentGeneration、desktopRunToken、credential 均不导出；App 层 View 再裁掉 Since。holderSince 仅在 commitTransition 写入真实 `clock.Now()`；grace 转换（OnUnexpectedDetachForSession:865-910）不走 commitTransition，Since 保持 acquire 时刻，与「most recent wire-visible owner transition」注释语义自洽；冻结 fake clock 测试逐字断言。
4. **绑定层** ✅
   错误链核实：`ControlGateError` 值接收者 Error() + `unwrapErr` 解引用 → `errors.As(err, &deny)` 值目标匹配成立（测试文案「会话不存在或已结束」实测通过）；control 未就绪 → 空 holds + `ErrControlNotReady` fail-closed（测试覆盖）。确认流程：`askRelease` 仅设 target，API 调用只在 ConfirmDialog `@confirm`；PG-06 组件默认焦点取消键、busy 防连点、收回期间全行按钮禁用；冻结文案在 controlHoldsModel.ts 且单测逐字断言。
5. **范围纪律** ✅
   `git status`：仅 5 个修改 + 4 个未跟踪文件，全部在声明清单内；wailsjs 三文件纯增量（+4/+8/+18 行，无既有声明删改）；mobile/ 与 app_quota_probe* 零改动。接口扩展无断裂（ControlGate 唯一实现者 controlGate）。

## 验证记录

| 命令 | cwd | 结果 |
|---|---|---|
| `go test ./internal/remote -run 'TestControlForceRelease|TestControlListSessionHolds' -count=1 -v` | repo root | 6/6 PASS |
| `go test . -run 'TestAppSessionControlHolds|TestAppReleaseSessionControl' -count=1 -v` | repo root | 3/3 PASS |
| `npm --prefix frontend test -- --run src/__tests__/components/remote/controlHoldsModel.test.ts` | repo root | 7/7 PASS |
| `go vet ./internal/remote .` | repo root | 干净（无输出） |

## 盲区

- 未运行全仓回归（quick 档，按契约聚焦 diff 高危路径）；remote 包全量测试与 e2e 未跑，无理由怀疑回归（改动为纯新增路径 + 无删改既有行为），但未证实。
- PG-06「冻结文案」的原始设计文档（前端视觉交互设计 v1.2）未在 docs/agent-outputs 中检索到原文，冻结契约以 controlHoldsModel.ts 注释 + 单测为准——文案内容本身与任务书语义（设备可重新接管，非断连/封禁）一致。
- ConfirmDialog 键盘交互（Enter 直通等）属既有组件、不在本 diff 范围，未深查。

## 结论

控制权安全面五项重点全部核实通过，测试断言真实无缩水；唯一 finding 为误导性注释（Minor）。

VERDICT: PASS_WITH_MINOR
