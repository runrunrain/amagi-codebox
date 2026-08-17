# pi WebUI /resume 会话内容不显示 — 根因分析与修复方案

> **状态（2026-08-17 v2 最终）：已完成并修正**。初版方案（切片 1 热切换）基于"扩展在同闭包内存活"的错误假设——真实 pi 的 createRuntime 在 switchSession 后整体重载扩展，热分支永不触发且旧 server 保活占死端口。v1.0.8 已改为：switch 类 shutdown 立即停服释放端口，新注册冷启动重绑同一注入端口；输入适配器全部防御化（修复 web 输入致 Node unhandled rejection 杀死 pi 进程）。详见 amagi-pi `agent-outputs/pi-webui-switch-stop-report.md`。切片 2（壳侧 pid 粘性 + 3s 保活 + 瞬时 503 不降级）不变且依然必要。两仓均未 commit。手工 E2E（启动 pi → /resume → web 页断线重连后显示历史；web 输入不崩进程）待主上验证。

## 症状

内嵌终端 pi 会话的 Web 版界面（Web 平面 iframe / 独立浏览器页）：
- 新启动的 pi 会话：内容实时显示 ✅
- pi TUI 内执行 `/resume`（含 `/new`、fork、reload）恢复的会话：**历史会话内容无法显示** ❌

## 根因（三层叠加，跨两仓）

pi 的 resume 流程（`agent-session-runtime.js switchSession`）：
`teardownCurrent("resume")` → emit `session_shutdown{reason:"resume"}` → 新建 SessionManager（含被恢复会话全部条目）→ emit `session_start{reason:"resume", previousSessionFile}`。reason 全集：switch 类 `resume|new|fork|reload`；真退出 `quit`。

1. **pi 侧扩展（amagi-pi `extensions/amagi-core/webui/server.ts`）**：
   `session_shutdown` handler 无条件 `broadcastBye` + 50ms 后 `server.stop()`；随后 `session_start` handler 无条件 `new WebUiServer()` 重新绑定端口——旧 server 50ms 内未释放注入端口（AMAGI_WEBUI_PORT）→ **fallback 随机 ephemeral 端口，地址漂移**；已连接页面收到 bye 后重连同源端口必然失败 → `/api/info` 连续 2 次失败 → 页面 ended。
2. **壳侧（amagi-codebox `internal/webui/service.go`）**：
   `validateAdoption` 粘性校验要求 `info.SessionID == tracker.piSessionID`。resume 后 sessionId 必变 → 强校验失败 → 注册表回退同样按 piSessionID 匹配 → 全部跳过 → available 态 failStreak 累计 → **ended**（即使新 server 活着且历史完整）。
   且前端 `ensureWebUIProbe` 在 available 后停止轮询，无法跟随任何后续变化。
3. **webui 页面（amagi-pi `webui/src`）**：`doc.ts` 明确忽略 `session_start/session_shutdown` 事件——旧假设（server 与 session 同生命周期）被 resume 打破后，即使连接存活也不刷新历史投影（新 HistoryStore rev=0 与客户端 rev 失配本可经 410 自愈，但连接已断，无从触发）。

数据面本身无问题：resume 后新 SessionManager 的 `buildContextEntries()` 包含被恢复会话全部历史。

## 修复方案

### 切片 1：amagi-pi — server 会话热切换 + 页面 refetch（根治）

- `server.ts`：
  - `session_shutdown`：reason ∈ {resume,new,fork,reload} → 只 publish 事件，**不 bye、不 stop、不清 current**；其他（quit 等）维持现行 bye+stop。
  - `session_start`：current server 存在时**热切换**：新 HistoryStore（新 manager）经 `setHistoryProvider` 注入 + `updateSession(新 sessionId)`（registry 覆盖写）+ publish `session_start{reason,...}`；**端口不变、WS 连接不断**。EventStream 保留（seq 连续、ring 保留，replay 连贯）。不存在时维持现行新建路径。
- `webui/src/http-source.ts`：`onEvent` 检测 `session_start` 且 `reason !== 'startup'` → `fullRefetch('session_start')`（现成：onClear + 重拉首页，有重试上限保护）。
- `webui/src/doc.ts`：`applyLiveEvent` 增加 `session_start` case 更新 meta（sessionId/sessionFile）；同步修正"页面不需要"注释。
- 契约 `docs/webui-protocol.md`：增补"会话切换保活"语义（同 pid 注册条目 sessionId 演进；session_start 事件驱动客户端全量 refetch）。
- 测试：`tests/webui-server.test.mjs`（shutdown 分流 / 热切换端口稳定 / history provider 切换 / registry 覆盖写）+ webui vitest（session_start → fullRefetch）。

### 切片 2：amagi-codebox — 壳跟随 sessionId 演进 + 保活探测（防御层，老扩展也兼容）

- `internal/webui/service.go`：
  - `validateAdoption`：粘性键从 piSessionID 放宽为 **pid**（`info.PID == snap.pid` 且端口校验通过即采纳）；sessionId 变化时更新 tracker 并记日志（"webui 会话切换"）。安全性不降：per-pid 注册表 + info.pid 匹配已防端口复用/他服务误采纳。
  - 注册表回退匹配键：pid 匹配即可采纳（piSessionID 仅作首选精确匹配）。
  - available 后 failStreak→ended 逻辑保留（进程真死仍正确落 ended）。
- `frontend/src/stores/session.ts`：`ensureWebUIProbe` 在 available 后转**低频保活**（3s）而非停止；unavailable/ended 仍停。URL 变化时（端口漂移场景）由 status 传导 → `TerminalView` 刷新 webUrl → `WebPlaneHost` 已有强制 reload 机制接管。
- `frontend/src/components/terminal/TerminalView.vue`：watch webuiStatus URL 变化时更新 webUrl（当前仅打开平面时取一次）。
- 测试：`internal/webui/webui_test.go` 增补 resume 场景（学到 piSessionID 后 sessionId 变化 + pid 不变 → 采纳并更新；pid 变化 → 拒绝）。

### 两切片关系

无代码依赖，可并行。切片 1 落地后端口稳定，切片 2 处理 sessionId 演进 + 兼容未升级扩展的端口漂移。二者合并后：resume → 页面连接不断 + 收 session_start 事件全量 refetch → 历史完整显示；壳侧跟随 sessionId，状态不误落 ended。

### 部署链路

amagi-pi 以 `packages: ["../../maorun-workpace/amagi-pi"]` 直连加载（`~/.pi/agent/settings.json`），改源码即生效（新 pi 进程）。codebox 走正常构建。

### 验证

- amagi-pi：`npm test`（webui-*.test.mjs）+ `webui/` vitest。
- amagi-codebox：`go vet ./...` + `go test ./internal/webui/` + `npm --prefix frontend run build`。
- 手工 E2E：启动 pi 会话 → Web 平面显示 → TUI `/resume` 选择历史会话 → Web 平面不断线且完整显示被恢复会话历史。
