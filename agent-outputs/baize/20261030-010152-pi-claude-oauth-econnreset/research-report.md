# 调研报告：CodeBox 启动 Pi 使用 Claude 订阅时 OAuth refresh ECONNRESET 定位

## 元信息
- **任务ID**: 20261030-010152-pi-claude-oauth-econnreset
- **执行时间**: 2026-10-30 01:01
- **执行Agent**: 白泽（baize，只读取证）
- **项目**: amagi-codebox / amagi-pi / pi-black / pi 宿主
- **上游输入**: 用户报错（OAuth refresh failed for anthropic, url=https://platform.claude.com/v1/oauth/token, fetch failed, read ECONNRESET）；Leader steer #1–#3 实测记录

## 探索目标
定位「CodeBox 启动 Pi + Claude 订阅（pi-black）后 OAuth refresh 报 ECONNRESET」的真实根因：调查 CodeBox 的 Pi 启动代理环境注入与 subscription 路径、amagi-pi 是否修改 OAuth/fetch/代理/子进程继承，pi-black 是否涉及，并指出是否存在真实代码缺陷与最小修复方向。不读凭据、不改源码。

## 结论索引表

| # | 主张 | 三态 | 锚点 | 适用边界 |
|---|------|------|------|----------|
| 1 | 报错来自 pi 宿主 Anthropic OAuth refresh（全局 fetch POST platform.claude.com/v1/oauth/token），非 pi-black、非 amagi-pi | 观察 | pi 安装 `dist/bundle/chunks/anthropic.js`（TOKEN_URL/refreshAnthropicToken/formatErrorDetails） | 当前 pi 1.3.x 安装（fnm v24.21.0 全局包） |
| 2 | pi 全局 fetch 经 `undici.EnvHttpProxyAgent` + `setGlobalDispatcher` 遵循 HTTP(S)_PROXY/NO_PROXY，OAuth refresh 因此走 env 代理 | 观察 | pi `dist/core/http-dispatcher.js` configureHttpDispatcher | pi 自身设计；无任何绕过 |
| 3 | CodeBox 启用「AI 代理分流」时对所有内嵌会话（五 CLI 含 Pi）接管代理键 → `HTTPS_PROXY=http://127.0.0.1:18890`、`NO_PROXY=localhost,127.0.0.1,::1`、`AMAGI_CODEBOX_AI_PROXY=1` | 观察 | `app.go:2413-2423`、`app.go:3930`、`internal/aiproxy/environment.go:68-103` | 本机 ai-proxy state enabled=true |
| 4 | CodeBox 不注入 NODE_USE_ENV_PROXY / NODE_OPTIONS | 观察（负面） | 全仓 `*.go` grep 无匹配 | 仅 Go 源码面；未含前端 |
| 5 | 用户当前 claude 分流规则正确匹配 platform.claude.com：rule enabled、mode=proxy、专用口 18892、domains 含 `claude.com`（suffix 匹配）→ 运行时路由 `rule-claude-in → rule-claude-out(vless)` 已生效 | 观察 | `~/.amagi-codebox/ai-proxy/state.json`、`runtime/core.json`（非凭据字段提取）；`internal/aiproxy/router/config.go:40,198-236` | 2026-10-30 01:01 快照 |
| 6 | pi-black 不改 OAuth refresh 或全局代理；仅包装 OAuth-token 的 stream/streamSimple 选项及其请求 fetch | 观察 | `extensions/pi-black.ts:17-23`、`src/anthropic-provider.ts:15-50`、README「preserves OAuth refresh」 | 包 v0.1.0（cc 2.1.280 协议） |
| 7 | amagi-pi 不改 OAuth / 不覆盖全局 fetch；仅向子进程**透传**代理变量（三处入口） | 观察 | `task-run.ts:111-118`、`rpc-pool.ts:747-754`、`mcp-client.ts:473-483`；全扩展 grep 无 setGlobalDispatcher/globalThis.fetch 覆盖 | extensions/amagi-core 当前工作区版本 |
| 8 | subscription 路径 = CodeBox pi-login 命名空间预设（标准 auth.json + 原生 provider ID）→ pi 侧 `anthropicOAuth`（isSubscription=true，Claude Pro/Max 登录） | 观察 | `app.go:3685-3690`（LaunchPiSession 注释）、anthropic.js anthropicOAuth、`internal/launcher/pi_login_override.go`（仅窗口覆写） | pi-login 预设体系 |
| 9 | 本机现场复核：经 18890 与 18892 访问 platform.claude.com 均在 TLS 阶段 Connection reset（HTTP 000，本地 TCP 0.3ms 秒连）；直连同样 reset | 观察 | 本次 curl 实测（2026-10-30 01:01，无凭据 GET） | 本机本时段；与 Leader steer #1/#2 实测一致 |
| 10 | 根因 = 分流所选 vless 节点出口到 Anthropic/Claude 域名的连接被重置（节点不可用），直连亦不可达；OAuth refresh 因此 fetch failed ECONNRESET | 推断 | 综合 #2/#3/#5/#9 + steer #2（节点服务器 TCP 4s 超时） | 网络时点性结论，节点恢复即失效 |
| 11 | 本次调查未发现足以解释此报错的代码缺陷；所查各环节符合设计 | 推断 | 见「无缺陷依据」节各注释锚点 | 基于静态证据+实测，未做运行时调试 |

## 发现摘要
- 报错文本与 pi 宿主 `refreshAnthropicToken` 的错误包装逐字段吻合（`Anthropic token refresh request failed. url=<TOKEN_URL>; details=<formatErrorDetails>`）——错误发生在 **pi 进程的 OAuth refresh fetch**，不在 pi-black / amagi-pi。
- 链路：CodeBox 启用 AI 代理分流 → Pi 会话 env `HTTPS_PROXY=127.0.0.1:18890` → pi 全局 fetch（EnvHttpProxyAgent）→ platform.claude.com 命中 `claude.com` suffix 规则 → `rule-claude-out` vless 节点 → **出口到 Anthropic 被 RST** → `read ECONNRESET`。
- 现场复核（本任务实测）：代理 18890/18892 与直连三种路径访问 platform.claude.com 全部 TLS Connection reset；本地代理 TCP 秒连证明 sing-box 存活，故障在出口段。
- pi-black 仅改 OAuth-token 请求体/header（cc_version/cch/identity），refresh 完全透传；amagi-pi 仅向子进程透传代理变量（v1.5.195 修复注释明确「子进程网络条件应与主进程一致」）。
- **结论：当前网络故障定位（出口节点不可用），非代码缺陷；修复应在 CodeBox AI 代理分流配置层换节点/出口，不应改 OAuth 重试、pi-black 或 amagi-pi。**

## 相关文件
| 文件路径 | 作用说明 | 重要性 |
|----------|----------|--------|
| amagi-codebox/app.go:2413-2423, 3930 | resolveEmbeddedLaunchSpec：五 CLI 统一漏斗，ApplyEnv 注入代理 | 高 |
| amagi-codebox/internal/aiproxy/environment.go:68-103 | ApplyEnv 接管全部代理键 → 127.0.0.1:ProxyPort | 高 |
| amagi-codebox/internal/aiproxy/router/config.go:32-44,167-236 | 默认规则/Build：domain_suffix 路由、final=direct | 高 |
| amagi-codebox/app.go:3685-3690 | LaunchPiSession：pi-login 预设=标准 auth.json+原生 provider（subscription 路径） | 中 |
| amagi-codebox/internal/launcher/pi_login_override.go | 登录预设模型窗口覆写（与认证无关，排除项） | 低 |
| ~/.amagi-codebox/ai-proxy/state.json + runtime/core.json | 用户运行态：enabled、18890、claude 规则、vless 路由 | 高 |
| pi 安装 dist/core/http-dispatcher.js:71-97 | configureHttpDispatcher：EnvHttpProxyAgent 全局 dispatcher | 高 |
| pi 安装 dist/bundle/chunks/anthropic.js | TOKEN_URL=platform.claude.com/v1/oauth/token、refresh 用全局 fetch | 高 |
| pi-black/extensions/pi-black.ts + src/anthropic-provider.ts | 仅包装 stream/streamSimple；refresh 不动 | 高 |
| amagi-pi extensions/amagi-core/task-run.ts:111-118 | ENV_WHITELIST 透传代理键给子 pi 进程 | 中 |
| amagi-pi extensions/amagi-core/rpc-pool.ts:747-754 | RPC worker 同口径透传 | 中 |
| amagi-pi extensions/amagi-core/mcp-client.ts:473-483,349 | MCP stdio 透传；注释确认「pi 全局 fetch 走 EnvHttpProxyAgent」 | 中 |

## 关键链路（证据串联）
```
CodeBox「启动 Pi 会话」(app.go:3930, LaunchPiSession:3685)
  └─ resolveEmbeddedLaunchSpec (app.go:2413) ──统一漏斗（五 CLI）
      └─ aiproxy.ApplyEnv (environment.go:68)【enabled=true】
          env := HTTP(S)_PROXY/ALL_PROXY(±小写)=http://127.0.0.1:18890
                 NO_PROXY=localhost,127.0.0.1,::1  AMAGI_CODEBOX_AI_PROXY=1
  └─ pi 进程启动（pi-login 预设 → 原生 provider，凭据=agent root auth.json）
      └─ configureHttpDispatcher (http-dispatcher.js:71) setGlobalDispatcher(EnvHttpProxyAgent)
  └─ access token 过期 → refreshAnthropicToken → postJson(TOKEN_URL)【全局 fetch】
      POST https://platform.claude.com/v1/oauth/token
      └─ EnvHttpProxyAgent → 127.0.0.1:18890（sing-box, auto-in）
          └─ route: domain_suffix "claude.com" 命中 → rule-claude-out（vless 节点）
              └─ 节点→Anthropic TLS 被重置（curl 复核 HTTP 000, Connection reset ~5s）
                  → TypeError: fetch failed; cause=read ECONNRESET（与用户报错一致）
amagi 子 pi 进程（task-run/rpc-pool 白名单透传同一代理变量）→ 同一故障面
pi-black：仅包装 OAuth-token 的 model 请求体，不在本链路
```

## 详细发现

### 1. 错误源头：pi 宿主 OAuth refresh（观察）
pi 安装目录 `dist/bundle/chunks/anthropic.js`：`TOKEN_URL="https://platform.claude.com/v1/oauth/token"`；`refreshAnthropicToken` 经 `postJson`（裸 `fetch`，无自定义 dispatcher、无 baseURL 改写）POST refresh_token，失败时包装 `Anthropic token refresh request failed. url=${TOKEN_URL}; details=${formatErrorDetails(error)}`；`formatErrorDetails` 输出 `name/code/errno/cause/stack` 以 `; ` 连接——与用户报错（`fetch failed; cause=Error: read ECONNRESET; code=ECONNRESET; errno=-54; stack=…`）逐字段吻合。`anthropicOAuth` 标注 `isSubscription:true`（Claude Pro/Max 订阅登录即此路径）。

### 2. pi 全局 fetch 走 env 代理（观察）
`dist/core/http-dispatcher.js` `configureHttpDispatcher()`：`new undici.EnvHttpProxyAgent({allowH2:false, proxyTunnel:true, …})` → `undici.setGlobalDispatcher(dispatcher)`，并保持 fetch 与 dispatcher 同为 npm undici 实现。EnvHttpProxyAgent 读取 `HTTP_PROXY/HTTPS_PROXY/NO_PROXY` 环境变量。amagi-pi `mcp-client.ts:349` 注释独立佐证（「pi 全局 fetch 走 EnvHttpProxyAgent（env 代理）」）。因此 OAuth refresh 必然走会话注入的 127.0.0.1:18890。

### 3. CodeBox 注入面（观察）
- `app.go:2413-2423`：`resolveEmbeddedLaunchSpec` 是「全部内嵌启动（五 CLI）的单一漏斗」（注释原文），先 `a.aiProxy.ApplyEnv(env)`；Pi 会话在 `app.go:3930`（AppTypePi）进入。
- `internal/aiproxy/environment.go:68-103`：启用路径删除用户全部代理键（含大小写 ALL_PROXY/NO_PROXY），重建为统一入口 + `NO_PROXY=localhost,127.0.0.1,::1` + 内部标记 `AMAGI_CODEBOX_AI_PROXY=1`。注释明确设计意图："Enabled routing owns all proxy keys so stale ALL_PROXY or NO_PROXY=* cannot defeat the chosen exits"。
- NODE_USE_ENV_PROXY / NODE_OPTIONS：全仓 Go 源码 grep 零命中（负面发现，搜索边界=`*.go`）——CodeBox 不做 Node 层代理开关注入，也不需要（见 #2，pi 自带 EnvHttpProxyAgent）。

### 4. 用户运行态配置（观察，非凭据提取）
`~/.amagi-codebox/ai-proxy/state.json`：`enabled=true`、`proxy_port=18890`；claude 规则 `{enabled:true, mode:"proxy", port:18892, domains:["anthropic.com","claude.ai","claude.com","claudeusercontent.com"], node:<node-ref>}`。`runtime/core.json`（sing-box 实际配置）：inbound `auto-in@18890`/`rule-claude-in@18892`；route 顺序 localhost/loopback→local-direct，专用 inbound→`rule-claude-out`（type=vless），domain_suffix 规则（api.maorun.top 等直连规则 + claude 域规则）；`final=direct`（无 fallback_proxy）。**platform.claude.com 经 18890 会正确命中 claude 规则走 rule-claude-out——分流配置本身正确**，与 Leader steer #2「claude.com 已正确匹配 rule-claude-out」一致。

### 5. pi-black 排除（观察）
`extensions/pi-black.ts:17-23`：`registerProvider(wrapAnthropicProvider(anthropic, …))`。`src/anthropic-provider.ts:15-50`：仅当 `isAnthropicOAuthToken(options.apiKey)` 时以 `mergeClaudeCodeOptions` 变换 **stream/streamSimple 的请求 options**（cc_version/cch/identity 等，见 README）；非 OAuth 请求与 `refresh` 完全透传原生 provider。`src/claude-code-protocol.ts` 确有 stream 请求的 fetch 包装，但不接管全局 fetch 或 OAuth refresh。README 声明与实现一致："preserves Pi's credential storage, OAuth refresh"。

### 6. amagi-pi 排除（观察）
- OAuth：仅 trace 正则（trace.ts:59 匹配 ECONNRESET 用于展示）、guards.ts:381 保护 auth.json 不被读取、MCP 配置字段引用——无 OAuth 流程修改。
- fetch：全扩展 grep `setGlobalDispatcher|globalThis.fetch|global.fetch` 仅命中 mcp-client.ts 依赖注入默认值（不改全局）。
- 代理/子进程继承：三处子进程入口均**透传**代理变量（task-run.ts:111-118 ENV_WHITELIST、rpc-pool.ts:747-754 buildWorkerEnv、mcp-client.ts:473-483 buildStdioEnv）。task-run.ts 注释记录 v1.5.195 实战修复：env 最小化曾剥离代理变量导致子 Agent fetch failed，此后「子进程网络条件应与主进程一致」——透传是修好的行为，非缺陷。

### 7. 现场连通性复核（观察，本任务实测）
2026-10-30 01:01，无凭据 GET（`curl -x http://127.0.0.1:<port> -o /dev/null -w '%{http_code}' --max-time 8 https://platform.claude.com/`）：
- 经 18890：`curl:(35) Recv failure: Connection reset by peer`，HTTP 000，connect=0.34ms（本地代理秒连），total≈5.0s
- 经 18892：同上（connect=0.22ms，total≈5.0s）
- 直连：同上（total≈5.0s）
即：本地 sing-box 存活并接受 CONNECT，但经隧道与直连的 TLS 握手均被重置。与 Leader steer #1（dispatcher POST 全 ECONNRESET、curl SSL timeout）、steer #2（claude 节点服务器 TCP 4s 超时、18891/18892 OAuth 均 ECONNRESET、系统 5800 超时）相互印证。steer #1 曾测直连 403、本次直连 reset——直连可达性随时段/链路波动，两种结果都不可用。

### 8. 根因判定（推断，前提=上述观察）
分流配置正确（#4）+ 各代码环节按设计（#2/#3/#5/#6）+ 三路径实测不可达（#7）→ 当前故障可定位到 **Claude 代理出口链路不可用**。节点服务器 TCP 超时与隧道 TLS 重置已观察到，但无法仅凭客户端错误确定 RST 发出方或精确的故障跳点。直连也未提供成功认证路径（reset/403）。该结论仅覆盖当前网络故障；更换节点后仍需复测 OAuth，不能预先保证认证成功或排除后续独立问题。

### 9. 无缺陷依据（推断）
- CodeBox 代理键接管：environment.go:65-67 注释显式设计（防 stale ALL_PROXY/NO_PROXY=* 击穿选定出口），且有测试覆盖（service_test.go:168-187）。
- pi EnvHttpProxyAgent：pi 宿主自有设计（http-dispatcher.js），Node 26 兼容注释表明刻意维护。
- amagi-pi 代理透传：v1.5.195 修复注释（曾因剥离代理致子进程 fetch failed）——透传是修复后的正确行为。
- pi-black：README 承诺与源码一致，不改 refresh。
- 唯一可讨论的**已知限制**（非缺陷）：AI 代理分流启用时 CLI 进程的全部 fetch（含 OAuth/更新/遥测）都进 18890；未命中规则的域名走 final=direct。若未来某 OAuth 域名不在任何规则 domains 中且直连不可达，会出现同类错误——当前 claude 规则已覆盖 claude.com，不适用本次。

## 验证证据
| 类型 | 记录 |
|------|------|
| 只读调研 | 本文全部路径:行号锚点；pi 安装=fnm v24.21.0 全局 @earendil-works/pi-coding-agent |
| 命令验证 | grep（代理键/NODE_*/oauth/proxy/dispatcher，仓内 *.go 与 amagi-pi/extensions）；node 安全提取 state.json/core.json（仅 enabled/端口/域名/规则/outbound tag，未输出节点地址与凭据）；curl 连通性复核×3（命令与结果见 #7，cwd=amagi-codebox） |
| 现场快照 | ai-proxy enabled、sing-box 路由生效（runtime/core.json 为 Build 产物） |
| 未运行 | 未执行真实 OAuth refresh（无凭据读取）；未改任何源码/配置/运行态 |

## 未覆盖范围与待核验项
1. 节点故障的深层原因（订阅源节点状态、落地 IP 被 Anthropic 拒绝、机场侧限流）未调查——需在代理客户端/订阅源侧验证，超出本任务。
2. sing-box 运行日志（~/.amagi-codebox/ai-proxy/runtime/ 及 logs/）未读取，可进一步佐证节点错误类型（dial timeout vs TLS reset）——Leader 决定是否补。
3. 直连 403（steer #1）与直连 reset（本次）的差异未深究；均为「直连不可用」结论，不影响判定。
4. 换节点后的真实 OAuth refresh 成功验证**未完成**——需节点更换后由用户或 Leader 复测（收敛点，非本任务范围）。

## 最小修复方向（证据支持的约束，非裁决）
- 修复层=**CodeBox AI 代理分流配置**：为 claude 规则更换可达成 Anthropic 的节点（或更换订阅可用出口）。验证式：`curl -x http://127.0.0.1:18892 -sS -o /dev/null -w '%{http_code}\n' --max-time 8 https://platform.claude.com/` 返回非 000（403/200 均代表 TLS 已通）后，在 Pi 内重试即可触发 refresh。
- 不应改：OAuth 重试逻辑、pi-black、amagi-pi、CodeBox 注入代码（均无缺陷，且 environment.go 的接管语义有防击穿设计意图）。

## 建议下一步
**建议交接**: Leader（向主上申请更换节点/出口后复测）
**建议行动**: 更换 claude 规则节点 → 用上述 curl 验证出口 → 复跑 Pi 会话观察 OAuth refresh 是否成功；成功后可确认更换出口解决了本次故障，但不能由此证明全链路不存在其他代码缺陷。

---
*本报告由白泽（baize）Agent 生成，只读取证，未修改任何源码/配置/运行态。*
*任务ID: 20261030-010152-pi-claude-oauth-econnreset*
