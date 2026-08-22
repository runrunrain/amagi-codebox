# 远程控制与移动端

面向 Amagi CodeBox 的终端用户。本篇说明如何启用远程控制、v1 远程 API 的设备配对与认证机制、移动端接入方式，以及桌面端作为客户端连接其他 CodeBox 实例（Remote Client）的用法。远程控制默认关闭；启用后桌面端在指定端口同时提供 v1 REST API、WebSocket 终端桥接与（可选的）移动端静态资源。

> 路径与端点以仓库当前代码（`internal/remote/`）为准。`README.md` 的"远程控制 API"小节是早期精简描述，与实际注册的路由存在差异；本篇以代码为单一真相源。

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
| `Auth` | legacy Token、launch grant、本地会话 cookie 认证 | `internal/remote/auth.go` |
| 设备与配对 | 配对窗口、设备登记簿（`devices.json`）、撤销 | `internal/remote/device.go`、`device_store.go` |
| v1 会话面 | v1 REST + `/ws/v1` 单一 WebSocket 消费通道 | `internal/remote/contract/version.go`、`ws_v1_session.go` |
| `App` 转发层 | Wails 绑定方法，前端调用入口 | `app.go` |
| `mobile/` | 独立的 Capacitor 移动端应用 | `mobile/` |
| Remote Client | 桌面端作为客户端连接其他 CodeBox 实例 | `internal/remoteclient`、`app_remoteclient.go` |

---

## 启用与配置

设置入口："设置 → 远程控制"页（`frontend/src/views/settings/RemoteSettings.vue`），自上而下为：远程服务开关卡 → LAN 暴露确认卡 → 配对卡 → 可信设备卡 → 活动控制卡 → 本地可见记录卡。

桌面端通过 Wails 绑定方法控制：

| 方法 | 行为 |
|------|------|
| `App.ToggleRemoteServer(enabled bool)` | `true` 启动服务器，`false` 停止 |
| `App.GetRemoteStatus()` | 返回 `{host, port, token, running}` |
| `App.SetRemotePort(port)` | 修改端口（范围 1024–65535）。先持久化到 `settings.json`，再若服务器正在运行则停止→改端口→重启 |
| `App.SetRemoteHost(host)` / `App.SetRemoteEndpoint(host, port)` | 修改监听地址，同上策略 |
| `App.RegenerateRemoteToken()` | 重新生成 legacy Token 并返回新值 |
| `App.CreateRemotePairingWindow(confirmTerminalExposure)` | 创建短时配对窗口（一次性配对码仅此返回值可见，不持久化、不记日志） |
| `App.GetRemotePairingWindow()` / `App.CancelRemotePairingWindow(generation)` | 查询（不含配对码）/ 取消配对窗口 |
| `App.ListRemoteDevices()` | 列出可信设备（不含任何凭据材料） |
| `App.RevokeRemoteDevice(deviceID, confirm)` | 撤销设备：加入撤销集、终止其活动连接、释放其持有的会话控制权 |
| `App.ListRemoteSecurityEvents(limit)` / `App.GetRemoteSecurityHealth()` | 本地可见的安全事件记录与健康快照 |

默认配置（`internal/settings/service.go` 的 `defaultSettings()`）：

- `RemoteHost = "127.0.0.1"`（**仅本机回环**；要让局域网设备接入，需在"LAN 暴露确认卡"中确认后改为 `0.0.0.0`）
- `RemotePort = 8680`

> 与旧版不同：默认监听地址已从 `0.0.0.0` 收紧为 `127.0.0.1`，LAN 暴露需要显式确认（配对窗口创建时的 `confirmTerminalExposure` 参数）。

### 服务器生命周期

- `Server.Start(ctx)` 在后台 goroutine 启动 HTTP 服务器，读写超时均为 30 秒。
- 服务器随父 context 取消而优雅关闭（5 秒 shutdown 超时）。

---

## 认证体系

当前远程面分两套命名空间，安全边界完全不同（`internal/remote/server.go` 的 `buildHandler`）：

### v1 远程面（移动端 / LAN 设备使用的正式通道）

- REST 基路径：`/api/remote/v1`（`contract.RESTBasePath`）。
- WebSocket：`/ws/v1`（`contract.WebSocketV1Path`，唯一 WS 入口，没有第二个 `/events` 端点）。
- 认证：**设备配对**。移动端通过 `POST /api/remote/v1/pairing/complete` 提交一次性配对码换取设备凭据（HttpOnly 设备 Cookie），后续请求凭 Cookie 通行。
- 设备状态持久化在 `~/.amagi-codebox/devices.json`（`internal/remote/device_store.go`）；被撤销的设备进入永久撤销集（`auth.revoked` 语义），其连接被终止、会话控制权被释放。

v1 REST 端点全集（`contract.V1RestEndpoints`，共 10 个）：

| 方法 + 路径（相对 `/api/remote/v1`） | 成功状态 | 用途 |
|--------------------------------------|----------|------|
| `POST /pairing/complete` | 201 | 提交一次性配对码，完成设备配对 |
| `GET /host/summary` | 200 | 宿主摘要信息 |
| `GET /sessions` | 200 | 会话列表 |
| `GET /sessions/{id}` | 200 | 会话详情 |
| `POST /sessions` | 201 | 启动会话 |
| `POST /sessions/{id}/stop` | 200 | 停止会话 |
| `POST /sessions/{id}/restart` | 200 | 重启会话 |
| `DELETE /sessions/{id}` | 204 | 移除会话 |
| `POST /sessions/{id}/control/acquire` | 200 | 获取会话控制权 |
| `POST /sessions/{id}/control/release` | 200 | 释放会话控制权 |

### legacy 面（仅本机回环可用）

旧的 `/api/*` REST 与 `/ws/terminal/{sessionID}` 现在**只接受 loopback 对端**：非回环来源的请求在鉴权之前直接 403（无信息泄露的 oracle）。所有响应带固定的 deprecation 头。也就是说，局域网设备**不能**再用 Bearer Token 访问这些端点；它们仅服务于桌面浏览器本机打开的 Web UI。

legacy Token（32 字节随机数 hex）仍保留，仅用于本机 loopback 场景；重新生成只能通过桌面端 `App.RegenerateRemoteToken()`，没有 REST 端点可远程重置。

legacy REST 路由（`internal/remote/handlers.go` 的 `registerRoutes`，均需 loopback + Token/本地 cookie）：

| 方法 + 路径 | 用途 |
|-------------|------|
| `GET /api/info` | 应用信息（含 `remotePort`） |
| `GET /api/sessions` / `GET /api/sessions/launch-meta` | 会话列表 / 启动元数据 |
| `POST /api/sessions/launch` / `launch-codex` / `launch-opencode` / `launch-pi` / `launch-omp` | 启动五种引擎的会话 |
| `POST /api/sessions/clear-stopped` | 清理已停止会话 |
| `DELETE /api/sessions/{id}` / `DELETE /api/sessions/{id}/remove` | 停止 / 移除会话 |
| `GET /api/providers` / `GET·PUT /api/providers/{name}` / `GET /api/providers-by-type/{type}` | Provider 读取与保存 |
| `POST /api/config/save` | 保存全部配置 |
| `GET /api/secrets/diagnostics` | 密钥存储诊断 |
| `GET /api/settings` / `PUT /api/settings` | 设置读 / 写（PUT 当前仅接受 `remotePort`） |
| `GET /api/logs` / `GET /api/paths` | 日志查询 / 工作路径列表 |
| `POST /api/bootstrap/consume` | launch grant 换本地会话 cookie（见下） |

### 本地会话 cookie（桌面浏览器免 Token 入口）

为方便桌面浏览器访问移动端 Web UI 而不手工输入 Token，提供 launch grant → cookie 的两级机制：

1. 桌面端调用 `App.OpenRemoteWebUI()`，浏览器打开 `http://127.0.0.1:8680/?autoconnect=1&launch=<grant>`。grant 是一次性令牌，TTL 2 分钟，绑定 host。
2. Web UI 加载后用 `POST /api/bootstrap/consume` 换取 cookie。
3. 服务端校验 grant 未过期未消费、host 一致、且为可信同源浏览器请求（Origin / Sec-Fetch-Site / Referer）。
4. 通过后写入 cookie `amagi_codebox_local_session`，TTL 12 小时，`HttpOnly` + `SameSite=Strict`。

### CORS 与 Origin

- `corsMiddleware` 仅在请求带 `Origin` 头时回显 CORS 响应，且 Origin 必须与请求 host 同源；跨源 OPTIONS 直接 403。
- WebSocket 带 Origin 头时校验同源；空 Origin 视为允许（非浏览器客户端）。

---

## WebSocket 终端

移动端使用单一端点 `ws://<host>:<port>/ws/v1`。连接由设备 Cookie 鉴权；客户端发送 `session.attach` 后，服务端先回放最多 1 MB 的历史帧，再无缝衔接实时输出；同一会话重启时以带 `restartBoundary=true` 的 `session.state` 帧标记输出边界。终端输出按 base64 字节块传输，移动端采用流式 UTF-8 解码，字符横跨多个 WebSocket 帧也不会被截断。

`/ws/terminal/{sessionID}` 是仅保留给本机兼容调用的 legacy 通道（loopback + Token）：只接收输入（base64 `input` 帧），不再向客户端广播输出；`resize` 帧被忽略以保护桌面 PTY 尺寸权威。历史回放与实时输出统一由 `/ws/v1` 提供。

---

## 移动端

### 形态

`mobile/` 是独立的 Capacitor 应用，应用 ID `com.amagi.codebox`（`mobile/capacitor.config.ts`）。同一份代码可产出两种形态：纯静态 Web 页面（`dist/`，可部署到任意 HTTP 服务器）与 Android APK。移动端是**独立构建**（`npm run build:mobile`），通过 `//go:embed all:mobile/dist` 嵌入主二进制——它不是桌面 Vue 前端的子集，是另一套 Vue 3 应用。

### 主要页面（来自 `mobile/README.md`）

| 页面 | 路由 | 功能 |
|------|------|------|
| Connect | `/#/connect` | 二维码自动配对或手动输入宿主地址、一次性配对码 |
| Lobby | `/#/lobby` | 会话概览与启动入口 |
| Workspace | `/#/workspace/{id}` | 历史回放、实时输出、输入与终端诊断视图 |
| Providers | `/#/providers` | Provider 管理 |
| Settings | `/#/settings` | 连接管理 |

### 连接方式（配对码为主路径）

1. 桌面端在"设置 → 远程控制"启用远程服务，确认 LAN 暴露（host 切为 `0.0.0.0`），然后在配对卡发起短时配对窗口。
2. 桌面端展示二维码，内容是 `http://<局域网IP>:<端口>/#/connect?...` 网页 URL——监听 `0.0.0.0` 时自动选择可达的局域网 IP，而不是把不可访问的 `0.0.0.0` 写入二维码。
3. 用手机系统相机扫码即可直接打开页面；页面自动提交一次性配对码，成功后进入会话大厅。配对码放在 URL hash 中，不随 HTTP 请求发送，页面读取后立即从地址栏清除。
4. 降级处理：在 Connect 页手动填写 Server URL 与一次性配对码。

首次连接成功后，服务端写入 HttpOnly 设备 Cookie；一次性配对码不会写入 `localStorage`。设备登记在桌面端"可信设备卡"可见，可随时撤销。

---

## 桌面端互联（Remote Client）

除"作为宿主被移动端连接"外，Amagi CodeBox 桌面端还可以**作为客户端连接另一台运行 CodeBox 的机器**（`internal/remoteclient`，绑定层 `app_remoteclient.go`）：

- **主机登记簿**：已登记主机存于 `~/.amagi-codebox/remote-hosts.json`；设备凭据存于系统 Keychain（条目 `codebox-remoteclient/<DeviceID>`），进程重启后凭登记簿 DeviceID 恢复。
- **入口**：侧栏顶部的主机切换器（`HostScopeSwitcher.vue`，快捷键 `Cmd/Ctrl+Shift+H`），下拉 = 本机 + 已登记主机（状态灯绿/灰/红）+ 添加入口。添加主机走"探测 → 输入配对码完成配对 → 连接"流程（对端需在其配对卡发起配对窗口）。
- **远程模式**：切到远程主机后，会话设置页替换为 `RemoteSessionsView`，可浏览远端会话、 attach 查看输出、在远端启动/停止会话。同时至多保持一条已连接宿主，连接新主机会顶替旧连接。
- 远程模式下，设置页顶部额外显示宿主应用设置卡（legacy 通道，可配置访问令牌），其余设置项仍为本机内容。

---

## 安全注意事项

- **默认仅回环**：`127.0.0.1:8680` 默认只允许本机访问；改为 `0.0.0.0` 前请确认局域网可信，配对窗口存活期间避免向无关人员展示配对码/二维码。
- **legacy 面已收紧**：`/api/*` 与 `/ws/terminal` 对非回环来源一律 403，LAN 设备必须走 v1 配对流程。
- **HTTP 明文**：远程 API 当前不提供 HTTPS。跨公网部署必须在反向代理层启用 TLS。
- **Token 不可远程重置**：legacy Token 重新生成需在桌面端操作；设备撤销也需桌面端确认（`RevokeRemoteDevice` 的 `confirm` 参数）。
- **配对码一次性**：配对码仅存在于 `CreateRemotePairingWindow` 的返回值中，不落盘、不记日志；配对窗口可被取消，错误尝试次数有限（状态中含 `remainingAttempts`）。

完整安全策略见 [../security.md](../security.md)。

---

## 已知限制与注意事项

- **README 与代码的偏差**：`README.md` 列出的 `GET /api/status`、`POST /api/launch`、`POST /api/regenerate-token` 等端点在当前代码中不存在；本篇按实际路由描述。
- **`PUT /api/settings` 字段有限**：当前仅支持 `remotePort`，其它字段（host、token 等）必须通过桌面端修改。
- **移动端不拥有 PTY 尺寸权威**：桌面端 PTY 尺寸由桌面端会话设置决定；移动端 resize 只调整自身远程视口。
- **Remote Client 单连接**：同时只能连接一台远程宿主，切换即断旧连。

> 待核实：`mobileWebRoot` 在 `settings.json` 中的默认值；`App.GetRemoteWebUIStatus` 中 `Reason` 字段的所有可能取值。
