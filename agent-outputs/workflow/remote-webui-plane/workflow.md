# Workflow：远程控制网页视图 — pi/omp 会话「Web 会话平面」（对齐桌面端 WebPlaneHost 效果）

## 目标与总验收

用户在 amagi-codebox 远程控制网页视图（mobile web UI，经 remote server 打开）中查看 pi/omp 会话时，内容区只有两条路径：终端仿真（xterm）或 pattern-parser 时间线——后者对 pi TUI 流解析失败（满屏破折线 + spinner，截图 1/2）。期望内容区达到 pi Web UI「会话平面」效果（截图 3：结构化工具卡片/思考折叠/diff 摘要/输入台）。

**总验收**：
1. 远程网页视图打开 pi/omp 会话，webui 可用时默认进入 Web 会话平面，渲染效果等同桌面端 WebPlaneHost（iframe 嵌入 pi webui 页面）；历史、事件流、输入台可用。
2. webui 不可用（未装插件/未就绪）时自动回落终端仿真面，菜单可手动切换三视图（Web 平面/时间线/终端）。
3. 代理链路安全：device cookie 鉴权、pi capability token 不入 URL query/日志、`/api/input` 受会话控制门约束（无控制权 → 拒绝）。
4. 桌面端现有 WebPlaneHost（127.0.0.1 直连 iframe）行为零回归。
5. `go vet ./...`、`go test ./internal/remote ./internal/webui`、mobile Vitest、amagi-pi webui `check+test+build` 全绿。

## 根因（Phase 0 已查实）

- pi webui（amagi-pi `extensions/amagi-core/webui` server）只 bind `127.0.0.1`（契约 §6.1 冻结），移动端设备物理不可达。
- 页面基址推导冻结为 `http(s)://127.0.0.1:<页面端口>`（契约 v1.0.15 §6.5，`webui/src/main.ts` `deriveBase()`）——即使反代页面，API/WS 也会指向移动端自己的 127.0.0.1。
- mobile 前端无 Web 会话平面视图；pi/omp（isTuiCli）会话默认终端仿真，时间线视图靠 pattern-parser 解析 TUI 字节流，天然不可行。

## 冻结契约（Phase 0，切片按此并行）

### C1 代理路由（codebox remote server）
- 顶层前缀 `/webui/{sessionID}/...`（挂在 buildHandler 顶层分发，先于静态 SPA fallback）。
- 出向改写：`Host: 127.0.0.1:{port}`、删除 `Origin`、注入 `Authorization: Bearer {token}`（覆盖式，防伪造）、透传 `Sec-WebSocket-Protocol`（WS upgrade）。后端 Host/Origin 校验（§6.2）无需改动即可通过。
- 端口/token 来源：`internal/webui` Service per-session tracker（available 状态）；经 `AppInterface` 扩展由 app.go 接线。
- 鉴权：与 v1 一致的 device cookie；sessionID 严格字符白名单防路径穿越。
- `/api/input` 代理请求按会话控制门放行/拦截（无控制权 → 403，语义对齐现有移动端输入面）。

### C2 状态端点（v1 contract）
- `GET /api/remote/v1/session/{id}/webui` → `{ state: "probing"|"available"|"unavailable"|"ended"|"unknown", url?: string }`；available 时 `url = "/webui/{sid}/#/t={token}"`（fragment 承载 capability，不进请求行/日志）。

### C3 页面基址推导 v1.0.16（amagi-pi）
- `hostname === '127.0.0.1'`（或 localhost）→ 现行为不变（显式 127.0.0.1 + port）。
- 否则（trusted host 反代加载）→ `httpBase = origin + 路径前缀`、`wsBase = (https?wss)://host + 前缀`；前缀 = `location.pathname` 去掉最后一段（`/webui/{sid}/` → `/webui/{sid}`）。
- 契约 owner 是 amagi-pi 仓 `docs/webui-protocol.md`，升 v1.0.16 修订 §6.5。

### C4 mobile 视图（WorkspacePage）
- isTuiCli 会话新增 `?view=webplane`；默认视图策略：available→webplane，probing→加载态，unavailable/unknown→终端仿真（现状）。
- webplane 视图内隐藏外层 ComposerBar（页面自带输入台，避免双输入台）。
- 菜单三面切换；webui unavailable 时隐藏 Web 平面入口。

## Phase 表

| # | 切片 | Agent | cwd | 依赖 | 产物 | 验收 | 状态 |
|---|---|---|---|---|---|---|---|
| T1 | remote server webui 反代 + 状态端点 + 控制门 | luban | amagi-codebox | C1/C2 | 代码+Go 测试+报告 | httptest 假后端验证头改写/鉴权/WS/输入拦截；go vet+test 绿 | done |
| T2 | mobile Web 会话平面视图 | luoshen | amagi-codebox | C2/C4 | 代码+Vitest+报告 | 视图切换/降级/api 形状测试绿；`npm run build:mobile` 可过类型门 | done |
| T3 | webui 页面 base 感知 + 契约 v1.0.16 + 产物重建 | luoshen | amagi-pi | C3 | main.ts+契约+static 产物+测试+报告 | `npm run check`/`test`/`build` 绿；代理路径冒烟验证 | done |
| G1 | 集成验证 | Leader | 两仓 | T1-T3 | 验证记录+Leader 修复 | 见下 | done |
| G2 | 阶段审核 | diting | 两仓 | G1 | G2-review.md | VERDICT: FIX→修复批完成 | done |
| G2' | 修复批+带鉴权面浏览器 E2E | T1/T3 resume + Leader | 两仓 | G2 | 修复代码+追加 E2E 记录 | 见下 | done |

## 跨边界链路清单

1. **移动端浏览器 → remote server `/webui/{sid}/` → pi webui server（127.0.0.1:port）**：fragment token → 页面 Bearer/WS 子协议 → 后端校验；代理改写 Host/删 Origin/覆盖 Authorization。责任：T1（代理）+ T3（页面 base）+ G1 端到端。
2. **remote server → webui.Service 状态**：AppInterface 扩展 → app.go `a.WebUI`。责任：T1。
3. **mobile WorkspacePage → 状态端点 → iframe src**：C2 响应形状。责任：T2（消费端按冻结形状先行，联调 G1）。
4. **webui `/api/input` → 会话控制门**：复用现有 permit 判定。责任：T1 调查 `control_permit.go`/`control_lane.go` 入口并接线。

## 并行与隔离

- T1/T2/T3 契约已冻结（C1-C4），无运行时依赖，一次 `context + tasks[]` 并行派发；T3 独立仓（cwd=amagi-pi），改文件清单严格收窄。
- 三切片均不 commit；T3 重建 `extensions/amagi-core/webui/static/`（tracked 产物，向后兼容不回归桌面端）。

## 自治级别

unattended（主上「使用编排模式进行修复」= 跑完再说；歧义取最小安全解释并记决策）。

## 风险、决策与修订日志

- 2026-09-11 创建。决策：选「remote server 反代 + iframe」复用桌面端已验证形态（而非在 mobile 端复刻渲染）——最小改动面 + 像素级对齐期望图。
- 风险：SameSite=Lax cookie 在同站 sandbox iframe 的可用性 → G1 真浏览器验证；WS 子协议经 ReverseProxy 透传 → T1 httptest 验证。
- 风险：amagi-pi 为用户活跃插件仓，T3 改动向后兼容（127.0.0.1 分支行为不变），重启会话后生效，无害。
- **G1 记录（2026-09-13）**：用真实活会话（本会话 pid 91771 webui server）+ 临时子路径反代（等价 T1 Director）+ agent-browser 真实浏览器端到端：发现 **T1 Director 一律删 Origin 导致 opaque iframe（Origin: null）CORS 预检拿不到后端 ACAO:null、实际数据请求发不出**——Leader 直修：出向改为「Origin: null 透传、其余删除」，同步修正假后端断言过严建模，新增 TestWebUIProxy_OriginNullPassthrough。修复后端到端全通：预检 204 → /api/info 200 → **WS 101 子协议往返** → /api/history 200 → iframe 内结构化会话平面真实渲染（工具卡片 ✓ 完成/思考折叠/结果折叠/已连接状态，与期望图 3 形态一致，截图 /tmp/amagi-webui-e2e/webplane-e2e.png）。全量回归绿（go vet ./... + remote/webui/contract 全 ok；T2 侧 638 用例 + build:mobile 绿；T3 侧 207+59 用例 + 产物重建）。
- **G2' 记录（2026-09-13）**：diting VERDICT: FIX（Critical-01 opaque iframe 鉴权面不可达 + Major-02 agent-interact 未设防）→ T1/T3 两 resume 修复批 + Leader 直修三追加：① T1 预检豁免（OPTIONS+Origin:null+ACRM → 本地 204+ACAC）+写面集合化+no-store+reqID fail-closed；② T3 credentials:'include'（transport/draft）+死分支+契 v1.0.16 补齐；③ Leader 应用 server.ts ACAC 补丁（越界契约移交）。**带鉴权面浏览器 E2E（T1 browser_e2e 工具 + 真实 server.ts 合成后端 + agent-browser）再抓两个隐藏断点并 Leader 直修**：A）`<script crossorigin>` 跨源 CORS-mode 子资源永不携带 cookie → 静态面（非 /api//ws）401、页面只剩骨架 → **静态面豁免鉴权**（对齐后端 §6.3 静态公开语义，TestWebUIProxy_StaticPlaneNoCookie）；B）Chrome 对 sandbox（opaque origin）文档的请求抑制 SameSite cookie（site_for_cookies=null）→ cookie 在 opaque iframe 内永不可用 → **数据面双通道鉴权**（device cookie 优先；无 cookie 时 capability token 降级：HTTP Bearer / WS 子协议比对 tracker，定长比较；token 通道零 principal → 写面控制门自动 403，TestWebUIProxy_TokenChannelDualAuth）。**终验（真实 Go 鉴权代理 + 真实 server.ts + HistoryStore 合成后端 + 真实 bundle）**：主 frame 与 opaque iframe 两场景均「已连接」live、会话消息渲染、输入台激活、WS 消息流稳定（截图 /tmp/amagi-webui-e2e/webplane-g2-final.png）；curl 层验证预检 204+ACAC/无 cookie 401/带 cookie 200。全量回归绿（go vet 0、remote/webui/contract ok、amagi-pi 32/32、mobile 638+build:mobile 绿）。
