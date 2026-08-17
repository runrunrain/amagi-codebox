# 切片 2 实施报告：壳跟随 sessionId 演进 + 保活探测（amagi-codebox）

对应 [plan.md](./plan.md) 切片 2（防御层，兼容老扩展）。核心语义变化：pi TUI 内
`/resume`、`/new`、fork、reload 在**同进程**内切换会话——sessionId 必变、pid 不变
（server 可能换端口重建但 pid 相同）。壳侧采纳粘性键从 piSessionID 放宽为 **pid**，
前端 available 后由停止轮询转为低频保活。

## 改动清单

### 1. internal/webui/service.go（后端采纳逻辑）

| 位置 | 改动 |
| --- | --- |
| 文件头注释 | 增补会话切换语义说明（粘性键 = pid，sessionId 演进为合法切换）。 |
| `tracker.piSessionID` 字段注释 | 标注"跟随演进；粘性键是 pid"。 |
| `ProbeWebUI` doc 注释 | 轮询契约更新：available 后转低频保活，unavailable/ended 才停。 |
| `validateAdoption` | 删除 `snap.piSessionID != info.SessionID → 拒绝` 分支；保留 **pid 匹配 + sessionId 非空 + info.port==候选端口** 三重校验。pid 不一致仍拒绝（端口复用/他服务误采纳防线不变）。注释同步改写（含"粘性复核"段）。 |
| `adoptLocked` | 新增会话切换检测：`t.piSessionID` 已学到且与 `info.SessionID` 不同时，记 `INFO "webui 会话切换" port=<p> piSession=<旧> -> <新>`，随后跟随更新。该日志在持 `s.mu` 下打——与既有 `adoptLocked`/`probe` 内日志同模式，无新锁纪律问题。 |
| `runProbe` 注册表回退通道 | 匹配键从"已学到 piSessionID 时仅精确匹配"放宽为"piSessionID 精确匹配优先；失配但 `e.PID == snap.pid` 也入围"（per-pid 注册表天然唯一），最终归属仍由 `validateAdoption` 强校验裁决。 |

并发纪律（网络 I/O 不持全局锁、per-tracker probeMu 串行化、迟到结果按 tracker
同一性 + ended 栅栏丢弃）**零改动**；`failStreak >= endedFailThreshold → ended`
判定**零改动**（进程真死仍正确落 ended）。

### 2. frontend/src/stores/session.ts（保活探测）

- 新增常量 `WEBUI_KEEPALIVE_INTERVAL_MS = 3000`。
- `ensureWebUIProbe.tick`：terminal 判定收窄为 `unavailable | ended`；`available`
  改为非终态——下一轮延时切到保活节奏（3s），其余状态维持探测节奏（800ms）。
- 代际/迟到丢弃（`isCurrent`）、`stopWebUIProbe`（会话移除路径）、`retryWebUIProbe`
  均不变，无轮询泄漏：终态停表逻辑与换代清理语义保持。

### 3. frontend/src/components/terminal/TerminalView.vue（URL 跟随）

- 新增 `watch(() => sessionStore.webuiStatus[props.sessionId]?.url, ...)`：url
  非空、state 为 available、且当前 Web 平面可见（`activePlane === 'web'`）时更新
  `webUrl` ref。URL 直接取自 store 状态，**不调用 `openWebPlane`**（避免探测风暴）。
- url 变化 → `WebPlaneHost` 既有 `watch(() => props.url)` bump frameKey 强制重载，
  未重复实现；用户在 TUI 平面时下次切换仍走 `activateWebPlane → openWebPlane`，
  拿到的就是最新 URL。

### 4. internal/webui/webui_test.go（测试，注入 stub 模式，不起真实 pi）

- `TestProbe_SessionIDMismatchRejected`（旧语义：sessionId 失配即拒绝）按新契约
  重构为以下三个用例：
  - `TestProbe_ResumeSessionSwitchAdopted`：学到 A 后探测返回 B/pid 相同/port 一致
    → 采纳 available 且 tracker 跟随更新为 B（日志可见"webui 会话切换"）。
  - `TestProbe_SessionSwitchWithPIDChangeRejected`：sessionId 演进 + pid 也变
    （端口被其他 pi 复用）→ 拒绝，failStreak 累积两次落 ended，piSessionID 不更新。
  - `TestProbe_RegistryFallbackSessionSwitchAdopted`：预置已学 piSessionID=A，
    注册表条目 sessionId=B/pid 一致 → 入围采纳，tracker 更新为 B。
- `TestProbe_StickyRechecksPIDAndSessionID` 更名 `TestProbe_StickyRechecksPID` 并
  修正注释（粘性键现为 pid，原测试本就只变 pid，行为断言不变）。
- 新增辅助 `trackerPiSessionID`（持锁读取，断言用）。
- 前端 store 无既有 vitest 套件（frontend/src 下无 *.test/*.spec，仓库测试为
  mobile Vitest + Playwright e2e），按任务约定不强制新增。

## 验证证据

| 验收项 | 命令 | 结果 |
| --- | --- | --- |
| go vet 干净 | `go vet ./...` | 通过（无输出） |
| webui 包全绿 | `go test ./internal/webui/ -count=1` | `ok amagi-codebox/internal/webui 7.018s` |
| 会话切换采纳 | `go test -run TestProbe_ResumeSessionSwitchAdopted -v` | PASS，日志含 `INFO [webui] webui 会话切换`（pi-sess-a → pi-sess-b） |
| pid 拒绝 / 注册表回退切换 | `-run 'SessionSwitch\|RegistryFallback\|Sticky' -v` | 全部 PASS |
| 并发纪律 | `go test -race ./internal/webui/ -count=1` | `ok ... 1.815s` |
| 前端类型门禁 + 构建 | `npm --prefix frontend run build` | vue-tsc + vite `✓ built` 通过 |

## 行为对照（修复前后）

| 场景 | 修复前 | 修复后 |
| --- | --- | --- |
| /resume 后同 pid、sessionId 变、端口不变 | 粘性校验拒绝 → failStreak → 误 ended | 采纳 + tracker 跟随 + "webui 会话切换"日志 |
| /resume 后老扩展端口漂移（注入端口拒连，注册表换新条目） | 回退通道按 piSessionID 匹配全部跳过 → ended | pid 一致条目入围 → 采纳新端口 → url 传导前端 → WebPlaneHost 强制重载 |
| available 后前端轮询 | 停止（无法跟随任何后续变化） | 3s 低频保活；unavailable/ended 仍终态停止 |
| 端口被其他 pi 进程复用（pid 变） | 拒绝 → failStreak → ended | 不变（pid 防线保留） |
| 进程真死 | 连续失败 ≥2 → ended | 不变 |

## 剩余风险 / 未覆盖项

- **503 未就绪窗口**（既有行为，未改）：available 会话在探测窗口（45s，自注册起算）
  耗尽后若恰好命中一次"新 server 已 bind、session_start 未发"的 503，
  `markStillProbingLocked` 会将其降为 unavailable（前端终态停表，需手动重试）。
  概率低（子秒窗口 × 3s 保活节奏；切片 1 热切换后该窗口基本消失），本次范围外。
- 手工 E2E（TUI `/resume` → Web 平面不断线且历史完整）依赖切片 1（amagi-pi 侧
  server 热切换）落地后联合验证，本切片仅壳侧单测覆盖。
- 未 commit（按任务纪律）。
