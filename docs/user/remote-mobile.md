# 远程控制与移动端

面向 Amagi CodeBox 的终端用户。本篇说明如何启用远程控制、远程 API 的端点与认证机制，以及如何通过 Web 浏览器或 Android 移动端接入桌面会话。远程控制默认关闭；启用后桌面端会在指定端口同时提供 HTTP REST API、WebSocket 终端桥接与（可选的）移动端静态资源。

> 路径与端点以仓库当前代码为准。`README.md` 的"远程控制 API"小节是早期精简描述，与实际注册的路由存在差异（实际路由更丰富，命名也不同）。本篇以代码为单一真相源。

相关参考：

- 内嵌终端（同一 PTY 被远程 WebSocket 复用）：[./terminal.md](./terminal.md)
- 后端 API 与 Wails 绑定方法签名：[../api.md](../api.md)
- 安全策略（Token、CORS、同源校验）：[../security.md](../security.md)

---

## 总览

远程控制由 `internal/remote` 提供，整体结构：

| 组件 | 角色 | 源码 |
|------|------|------|
| `Server` | HTTP + WebSocket 服务器，注册路由、提供静态资源 | `internal/remote/server.go` |
| `Auth` | Token、launch grant、本地会话 cookie 三层认证 | `internal/remote/auth.go` |
| `handlers.go` | REST 路由 handler | `internal/remote/handlers.go` |
| `websocket.go` | WebSocket 终端桥接 | `internal/remote/websocket.go` |
| `App` 转发层 | Wails 绑定方法，前端调用入口 | `app.go` |
| `mobile/` | 独立的 Capacitor 8 移动端应用 | `mobile/` |

桌面端是远程服务的宿主。移动端是独立的静态前端，部署后通过 REST + WebSocket 调用桌面端。

---

## 启用与配置

### 启用 / 禁用

桌面端通过 Wails 绑定方法控制：

| 方法 | 行为 |
|------|------|
| `App.ToggleRemoteServer(enabled bool)` | `true` 启动服务器，`false` 停止 |
| `App.GetRemoteStatus()` | 返回 `{host, port, token, running}` |
| `App.SetRemotePort(port)` | 修改端口（范围 1024–65535）。先持久化到 `settings.json`，再若服务器正在运行则停止→改端口→重启 |
| `App.SetRemoteHost(host)` | 修改监听地址。同上策略 |
| `App.RegenerateRemoteToken()` | 重新生成 Token 并返回新值 |
| `App.GetRemoteToken()` | 返回 legacy API Token（不用于新版二维码配对） |

默认配置（`internal/settings/service.go` 的 `defaultSettings()`）：

- `RemoteHost = "0.0.0.0"`（监听所有网络接口，局域网内设备可达）
- `RemotePort = 8680`

> 默认 `0.0.0.0` 意味着同一局域网的设备可以访问。如果只希望本机访问，把 host 改为 `127.0.0.1`。

### 服务器生命周期

- `Server.Start(ctx)` 在后台 goroutine 启动 HTTP 服务器，监听 `host:port`，读写超时均为 30 秒。
- 服务器随父 context 取消而优雅关闭（5 秒 shutdown 超时）。
- `Server.Stop()` 直接取消 context 并标记 `running=false`。

---

## 认证

### Token

- 32 字节随机数 hex 编码（64 字符）。
- 通过两种方式校验（`internal/remote/auth.go` 的 `validate`）：
    1. HTTP：`Authorization: Bearer <token>` 头。
    2. WebSocket：URL 参数 `?token=<token>`（因浏览器 WebSocket API 无法设置自定义请求头）。
- Token 失效场景：调用 `RegenerateRemoteToken()` 后旧 Token 立即失效。

> Token 只在桌面端生成与展示，**没有** REST 端点可以重新生成或获取 Token。`PUT /api/settings` 当前也只接受 `remotePort` 字段，不接受 `remoteToken`。

### 本地会话 cookie（桌面浏览器免 Token 入口）

为方便桌面浏览器访问移动端 Web UI 而不手工输入 Token，提供 launch grant → cookie 的两级机制：

1. 桌面端调用 `App.OpenRemoteWebUI()`，浏览器打开形如以下的 URL：

   ```
   http://127.0.0.1:8680/?autoconnect=1&launch=<grant>
   ```

   `grant` 是 `Auth.IssueLaunchGrant(host)` 颁发的一次性令牌，TTL 2 分钟，绑定到 host。
2. 移动端 Web UI 加载后用 `POST /api/bootstrap/consume`（JSON `{launch}`）换取 cookie。
3. 服务端 `Auth.ConsumeLaunchGrant` 校验：
    - grant 未过期且未被消费；
    - grant host 与请求 host 一致；
    - 请求是可信同源浏览器请求（`isTrustedSameOriginBrowserRequest`：通过 Origin / Sec-Fetch-Site / Referer 校验同源）。
4. 校验通过后写入 cookie `amagi_codebox_local_session`，TTL 12 小时，`HttpOnly` + `SameSite=Strict`，TLS 时带 `Secure`。
5. 后续请求带 cookie即可通过 `validateLocalSession`，无需 Token。

> 这种 cookie 仅对同源浏览器请求生效，不适用于移动端 App 或第三方客户端。

### CORS

- `corsMiddleware` 仅在请求带 `Origin` 头时回显 CORS 响应。
- 通过 `isAllowedCORSOrigin` 判断 Origin 是否与请求 host 同源；不同源的浏览器请求会被拒绝（OPTIONS 直接返回 403）。
- 这条策略阻止跨源页面借助宿主浏览器访问本地 API。

### 路由认证概览

| 路由前缀 | 认证方式 |
|----------|----------|
| `/api/bootstrap/consume` | 不走全局 middleware；自身校验 launch grant 与同源 |
| `/api/*` | Auth middleware，要求 Bearer Token 或本地会话 cookie |
| `/ws/terminal/{sessionID}` | 不走全局 middleware；handler 内部自行校验 `?token=` 或 cookie |
| 其他静态资源路径 | 动态选择 webRoot 后提供静态文件，无认证 |

---

## REST API 端点

下表按 `internal/remote/handlers.go` 的 `registerRoutes` 实际注册为准。

### 应用与会话

| 方法 + 路径 | 用途 |
|-------------|------|
| `GET /api/info` | 应用信息（含 `remotePort`） |
| `GET /api/sessions` | 获取会话列表 |
| `GET /api/sessions/launch-meta` | 启动元数据：可用工作目录、各引擎的 provider/preset 选项 |
| `POST /api/sessions/launch` | 启动 Claude 会话。Body：`{providerName, presetName, mode, workDir, useProxy, useHeadroom, shellPath}` |
| `POST /api/sessions/launch-codex` | 启动 Codex 会话。Body：`{modelName, providerID, mode, workDir, shellPath}` |
| `POST /api/sessions/launch-opencode` | 启动 OpenCode 会话。Body：`{providerName, presetName, mode, workDir, shellPath}` |
| `POST /api/sessions/launch-pi` | 启动 Pi 会话。Body：`{modelName, providerID, mode, workDir, shellPath}`（对标 `launch-codex`，由 `terminal_preset` `type="pi"` 驱动） |
| `POST /api/sessions/clear-stopped` | 清理已停止会话；返回 `{cleared: <n>}` |
| `DELETE /api/sessions/{id}` | 停止指定会话 |
| `POST /api/sessions/{id}/resize` | 调整会话尺寸。Body：`{cols, rows}` |
| `DELETE /api/sessions/{id}/remove` | 从列表移除会话记录 |

> 注意：任务规格曾提到 `GET /api/status`、`POST /api/launch`、`POST /api/regenerate-token` 等端点。这些在当前代码中并不存在；对应的实际端点是 `GET /api/info` / `GET /api/sessions` / `POST /api/sessions/launch` 等。Token 重新生成只能通过桌面端 `App.RegenerateRemoteToken` 完成。

### Provider / Config / Secrets

| 方法 + 路径 | 用途 |
|-------------|------|
| `GET /api/providers` | 列出全部提供商 |
| `GET /api/providers/{name}` | 单个提供商导出 JSON |
| `PUT /api/providers/{name}` | 保存提供商（请求体为原始 JSON） |
| `GET /api/providers-by-type/{type}` | 按类型过滤（如 `anthropic`、`openai`） |
| `POST /api/config/save` | 保存全部配置 |
| `GET /api/secrets/diagnostics` | 密钥存储诊断信息 |

### Settings / Logs / Paths

| 方法 + 路径 | 用途 |
|-------------|------|
| `GET /api/settings` | 返回 `{remotePort, remoteToken, autoStart, logLevel}` |
| `PUT /api/settings` | 当前仅接受 `remotePort`；保存后立即应用（停服务器→改端口→重启） |
| `GET /api/logs` | 日志查询。Query 参数：`level`、`source`、`keyword`、`limit`（默认 100） |
| `GET /api/paths` | 工作路径列表 |

### 静态资源与 SPA fallback

对非 `/api/` 与 `/ws/` 的路径，按以下优先级提供静态资源：

1. 用户配置的 `MobileWebRoot`（`settings.json` 的 `mobileWebRoot`，且 `index.html` 必须存在）。
2. 内置嵌入的 `mobile/dist`（构建时通过 `//go:embed all:mobile/dist` 嵌入 `main.go`）。
3. 都不可用时回退到 API handler（需认证）。

未知路径走 SPA fallback 返回 `index.html`，支持 hash 路由。

---

## WebSocket 终端

新版移动端使用单一端点 `ws://<host>:<port>/ws/v1`。连接由设备 Cookie 鉴权，
客户端发送 `session.attach` 后，服务端先回放最多 1 MB 的历史帧，再无缝衔接实时输出；
同一会话重启时以带 `restartBoundary=true` 的 `session.state` 帧标记输出边界。终端输出按 base64 字节块传输，
移动端采用流式 UTF-8 解码，字符即使横跨多个 WebSocket 帧也不会被截断。

下面的 `/ws/terminal` 是仅供本机兼容调用的 legacy 通道，不应再用于移动端输出展示。

### 端点

```
ws://<host>:<port>/ws/terminal/<sessionID>?token=<token>
```

- 路径参数：`sessionID`（注意是 `sessionID`，不是 `id`）。
- 认证：URL 参数 `token`（与 REST 共用同一 Token），或本地会话 cookie。Handler 不走全局 Auth middleware，自行校验。
- 校验失败返回 `401 Unauthorized` 与 JSON `{"error":"unauthorized"}`。

> 该 legacy 通道现在只接收输入，不再向客户端广播终端输出。历史回放和实时输出统一由 `/ws/v1` 提供。

### Legacy 兼容行为

| 方向 | 消息类型 | 内容 |
|------|----------|------|
| 客户端 → 服务端 | `input` | base64 编码的用户输入 |
| 客户端 → 服务端 | `resize` | 为保护桌面 PTY 尺寸权威而忽略 |
| 服务端 → 客户端 | — | 不发送输出、退出或尺寸事件 |

---

## 移动端

### 形态

`mobile/` 是独立的 Capacitor 8 应用，应用 ID `com.amagi.codebox`（`mobile/capacitor.config.ts`）。同一份代码可产出两种形态：

| 形态 | 用途 |
|------|------|
| 纯静态 Web 页面（`dist/`） | 浏览器直接访问；可部署到任意 HTTP 服务器 |
| Android APK | 原生应用，支持触屏交互与 Capacitor 原生能力 |

> 移动端是**独立构建**（`npm run build:mobile`），通过 `//go:embed all:mobile/dist` 嵌入主二进制。它不是桌面 Vue 前端的子集，是另一套 Vue 3 应用。

### 主要页面（来自 `mobile/README.md`）

| 页面 | 路由 | 功能 |
|------|------|------|
| Connect | `/#/connect` | 二维码自动配对或手动输入宿主地址、一次性配对码 |
| Lobby | `/#/lobby` | 会话概览与启动入口 |
| Workspace | `/#/workspace/{id}` | 历史回放、实时输出、输入与终端诊断视图 |
| Legacy Terminal | `/#/terminal/{id}` | 自动重定向到对应 Workspace 的终端诊断视图 |
| Providers | `/#/providers` | Provider 管理 |
| Settings | `/#/settings` | 连接管理 |

### 连接方式

推荐在桌面端打开「设置 › 远程访问」，发起短时配对窗口后直接扫描二维码：

1. 桌面端在监听 `0.0.0.0` 时自动选择可达的局域网 IP，而不是把不可访问的 `0.0.0.0` 写入二维码。
2. 二维码内容是 `http://<局域网IP>:<端口>/#/connect?...` 网页 URL，使用系统相机扫码即可直接打开。
3. 页面自动提交一次性配对码；成功后进入会话大厅，不再要求手工复制地址或 Token。
4. 配对码放在 URL hash 中，不会随 HTTP 请求发送；页面读取后立即从地址栏清除。

需要降级处理时，可在 Connect 页手动填写：

| 字段 | 说明 |
|------|------|
| Server URL | Amagi CodeBox Remote API 地址，形如 `http://<桌面IP>:8680` |
| Pairing Code | 桌面端短时配对窗口展示的一次性配对码 |

页面内扫码器继续兼容旧版 JSON 二维码；新版 URL 二维码既可由页面内扫码器识别，也可由手机系统相机直接打开。

首次连接成功后，服务端写入 HttpOnly 设备 Cookie；一次性配对码不会写入 `localStorage`。

### 三种部署形态（摘自 `mobile/README.md`）

| 形态 | 适用 |
|------|------|
| 部署到公网服务器（nginx / python http.server） | 跨网络访问；通过 FRP 隧道到达桌面端 |
| 本地 dev server + FRP 隧道 | 开发调试 |
| 局域网直连 | 手机与电脑同一 WiFi，直接访问局域网 IP |

> 桌面端通过 `App.OpenRemoteWebUI()` 打开内置 Web UI 时，会自动通过 launch grant 换 cookie，免去手工输入 Token。该入口仅适合桌面浏览器同源访问。

---

## 安全注意事项

- **默认监听 `0.0.0.0:8680`**：局域网内任何设备都可访问。一次性配对码有效期间应避免向无关人员展示；若不需要远程访问，应在桌面端关闭远程控制或把 host 改为 `127.0.0.1`。
- **HTTP 明文**：远程 API 当前不提供 HTTPS。局域网扫码会通过 HTTP 传输配对请求与设备 Cookie；跨公网部署必须在反向代理层启用 TLS。
- **Token 不可远程重置**：防止攻击者通过 API 自我洗牌；重新生成需在桌面端操作。
- **launch grant 严格同源**：grant 颁发时绑定 host，消费时校验 Sec-Fetch-Site / Origin / Referer，且仅存活 2 分钟，最大限度降低被第三方页面截获的风险。
- **WebSocket Origin**：`isAllowedWebSocketOrigin` 在带 Origin 头时校验同源；空 Origin 视为允许（非浏览器客户端）。
- **CORS 仅同源**：跨源页面访问被明确拒绝。

完整安全策略见 [../security.md](../security.md)。

---

## 已知限制与注意事项

- **README 与代码的偏差**：`README.md` 列出的 `GET /api/status`、`POST /api/launch`、`POST /api/regenerate-token` 等端点在当前代码中不存在；本篇按实际路由描述。
- **Token 远程获取受限**：`GET /api/settings` 返回 `remoteToken`，但已持有 Token 才能访问该端点；这意味着 Token 是"展示给已登录客户端"，而非"匿名获取"。
- **`PUT /api/settings` 字段有限**：当前仅支持 `remotePort`，其它字段（host、token 等）必须通过桌面端修改。
- **移动端不拥有 PTY 尺寸权威**：桌面端 PTY 尺寸由桌面端会话设置决定；移动端 resize 只调整自身远程视口。
- **WebGL 在移动端可能不可用**：移动端 xterm.js 同样根据平台能力选择渲染器，但具体策略由移动端代码决定（待核实：移动端是否复用桌面端的 macOS/Windows 渲染器选择逻辑）。

> 待核实：`mobileWebRoot` 在 `settings.json` 中的默认值；`App.GetRemoteWebUIStatus` 中 `Reason` 字段的所有可能取值。
