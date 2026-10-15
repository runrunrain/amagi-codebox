# AI 代理分流

CodeBox 左侧「AI 代理分流」是独立页面，路由为 `#/ai-proxy`。启用后，本机新建的 Claude Code、Codex、Pi、OMP、OpenCode 会话自动使用本功能的统一代理入口；Claude 和 OpenAI 的请求分别使用所选出口。未匹配域名默认直连，GLM 等国内模型可以沿用这一默认行为。

## 使用

1. 打开「AI 代理分流」。应用启动时会只读载入本机 v2rayN 的订阅分组、未分组节点，并合并分组内重复节点；也支持手动订阅导入。
2. 分别选择 Claude、OpenAI 节点，点击「启用分流」。更改已运行的配置时点击「应用更改」。
3. 从 CodeBox 新建会话即可自动接入，无需复制代理环境变量。
4. 在 v2rayN 更新订阅后，点击「同步 v2rayN」，再应用需要生效的变化。同步只读取已经保存的节点，不重新下载源订阅，也不改动 v2rayN 数据库。

默认入口为统一 `127.0.0.1:18890`、Claude 专用 `127.0.0.1:18892`、OpenAI 专用 `127.0.0.1:18893`；本功能直接使用 Wails 页面管理，不增加独立 HTTP 管理端口。这些默认端口与独立的 ai-proxy-router 工程不同，可以同时运行。

高级设置允许指定 sing-box 路径、v2rayN 数据库路径、代理端口、订阅下载代理和未匹配域名的上游。自动发现失败时，可以手动指定 `guiNDB.db` 和 sing-box 可执行文件。软件调用已有内核，不随工程分发 sing-box。

## 与 CodeBox 的接合

| 路径 | 接入位置 |
| --- | --- |
| 桌面内嵌会话，含 Pi 登录预设 | `resolveEmbeddedLaunchSpec` 在合并认证环境后注入分流，再固定登录 Shell 的代理变量 |
| 外部 CLI 进程 | `LauncherService.buildEnv`，覆盖五种 CLI 的最终环境 |
| 远程创建、重启 | `appLaunchPlanner.resolveLaunch`，计划构建只读查询本机分流策略 |
| Claude / Codex Headroom | 新的代理子进程继承相同环境；本地转发地址保持回环绕行 |
| GitAssist 提交总结模型 | 单独的 HTTP Client，不改动全局 Transport |
| 多模态、额度探测 | App 创建使用相同路由选择器的 HTTP Client |

统一入口按目标域名分流，因此同一个 Pi / OMP 会话及其继承环境的子 Agent 可以同时调用两种模型。第三方 API 域名需加入对应域名规则；相同 HTTPS 域名下不同模型无法仅凭域名区分。专用入口可用于单独配置的外部客户端。

启用时会覆盖进程原有的大小写 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY`、`NO_PROXY`；绕行清单固定包含 localhost、127.0.0.1、::1，防止本地 Headroom/relay 被外部代理接管。功能停用时不修改现有启动策略，也不改变父进程环境或系统代理设置。内核意外退出时，新建会话明确失败，避免悄悄改走其他出口。

保存配置保留草稿，活跃会话继续使用已应用的端口。应用更改会重启本功能的内核，已有连接会断开；修改后需重启已有 CLI 或 Headroom 进程以接入新环境。停用会停止本功能内核；正常退出 CodeBox 时清理进程，但保留启用偏好供下次启动恢复。

## 数据与边界

- 配置位于 `~/.amagi-codebox/ai-proxy/state.json`，运行配置位于同目录的 `runtime/core.json`。目录权限 0700、文件权限 0600；其中的节点凭据不是加密存储，应按私密配置保护。现有模型 API Key 仍走 CodeBox 原有密钥服务。
- Wails 只返回节点名称、类型、选择 ID 和运行状态，不返回节点凭据或订阅 URL；带认证的下载/上游代理以占位符返回。
- v2rayN 数据库使用 SQLite `file:` URI 的 `mode=ro`，读取同一事务快照。同步失败或某分组没有兼容节点时保留已保存快照。删除分组不会修改 v2rayN；源仍存在时下次同步会重新载入。
- 本功能管理本机，远程宿主使用宿主自己的配置。WSL 的宿主回环转发尚未实现；启用分流时拒绝对应 WSL 启动，而不会注入一个无法保证可达的端点。
- v2rayN TUN 可能接管内核到节点的连接并形成代理套代理。需要直接连接所选节点时，关闭 TUN 或自行配置绕过规则。
- 节点转换和配置检查成功不等于服务地区、账号权限或真实网络可用；没有自动测速、自动换节点或周期更新。

## 验证

```sh
SING_BOX_TEST_BINARY='/path/to/sing-box' go test -race ./internal/aiproxy/... ./internal/launcher ./internal/headroom ./internal/gitassist
V2RAYN_TEST_DATABASE='/path/to/guiNDB.db' SING_BOX_TEST_BINARY='/path/to/sing-box' go test ./internal/aiproxy/v2rayn -run TestLocalV2rayNCompatibility -v
npm --prefix frontend run build
npm --prefix frontend run test
```

`app_ai_proxy_browser_test.go` 提供隔离的真实 Go/Wails 门面桥接，配合 `e2e/ai-proxy.html` 和 `e2e/ai-proxy.vite.config.mjs` 验证 Vue 页面。该夹具只使用合成数据库、临时应用目录和测试节点，不读取真实模型凭据。

[浏览器验证截图](ai-proxy-preview.png)使用上述测试节点，显示当前窄窗口下的页面；实际节点和端口以应用内配置为准。

本次验证：新增路由、TLS/SSE、直连、生命周期、凭据脱敏、外部五种 CLI、Headroom 网络策略和浏览器草稿保留通过；前端 25 个文件、213 项测试通过；Windows 交叉构建通过。完整 Go 套件的环境检测 8 项、平台路径解析 1 项在本机失败，均在未修改的 `963c1c6` 基线上复现，错误涉及本机 Claude/npm 安装发现和 Codex 路径优先级。

本机打包使用项目 CI 的 Go 1.25.0。较新的 macOS SDK 需要额外链接 UniformTypeIdentifiers：

```sh
GOTOOLCHAIN=go1.25.0 CGO_LDFLAGS='-framework UniformTypeIdentifiers' \
wails build -s -skipbindings -m -nosyncgomod
```

新增运行代码复用同工作区 ai-proxy-router 的订阅解析、v2rayN 读取和内核管理逻辑，依赖均已在 CodeBox 的 vendored 模块中存在。
