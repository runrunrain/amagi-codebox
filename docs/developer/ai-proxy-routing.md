# AI 代理分流

CodeBox 左侧「AI 代理分流」是独立页面，路由为 `#/ai-proxy`。启用后，本机新建的 Claude Code、Codex、Pi、OMP、OpenCode 会话自动使用统一代理入口，按可编辑的服务规则分别选择出口。初始提供 Claude 和 OpenAI 两条规则，也可以添加其他服务。未匹配域名默认直连，GLM 等国内模型可以沿用这一默认行为。

## 使用

1. 打开「AI 代理分流」。应用启动时会只读载入本机 v2rayN 的订阅分组、未分组节点，并合并分组内重复节点；也支持手动订阅导入。
2. 在规则内选择「关联服务提供商」，或从上方选择已配置的 CodeBox 服务提供商并点击「关联并添加」。系统自动读取已启用 Anthropic / OpenAI 格式的 API 地址、提取并合并主机名，只需选择出口节点或「直连」。也可继续使用手动域名规则。
3. 点击「启用分流」。更改已运行的配置时点击「应用更改」。
4. 从 CodeBox 新建会话即可自动接入，无需复制代理环境变量。
5. 在 v2rayN 更新订阅后，点击「同步 v2rayN」，再应用需要生效的变化。同步只读取已经保存的节点，不重新下载源订阅，也不改动 v2rayN 数据库。

默认统一入口为 `127.0.0.1:18890`。默认规则保留 Claude 专用 `127.0.0.1:18892`、OpenAI 专用 `127.0.0.1:18893`；任意规则都可以设置专用端口，留空则仅参与统一入口的域名匹配。停用规则时，其专用端口不监听。本功能使用 Wails 页面管理，不增加独立 HTTP 管理端口，默认端口与独立的 ai-proxy-router 工程错开。

统一入口按列表从上到下匹配，首个命中生效。每个域名包含它的子域名，更具体的规则应放在通用规则前面。例如可以让 `api.shared.example` 使用一个节点，再让 `shared.example` 的其他子域名使用另一个节点。专用入口使用所属规则的出口；所有入口的 localhost、127.0.0.0/8、::1 均优先回环直连。没有启用规则时，其他请求使用「其他域名上游」策略，默认为直连。

例如 CodeBox 已配置 Gemini 中转服务，只需关联该提供商并选择节点。关联规则的域名只读，保存与启动时以配置服务的最新地址为准，忽略客户端伪造的域名；不会复制 URL 的路径、查询参数、认证信息、历史地址或 API Key。提供商 API 地址变化时，页面自动刷新域名并保留出口、名称草稿，点击「应用更改」后生效。应用前的新建会话与应用内模型请求会提示规则过期；已有 CLI 仍可能请求旧规则之外的域名，修改提供商地址后应及时应用。

提供商删除或更名后需要重新关联，也可停用规则或切回手动域名。若关联的多个提供商共用或覆盖同一域名，且配置不同出口，保存会报出冲突；相同域名无法按模型、API 路径或密钥区分出口。手动规则仍按显式顺序匹配，应避免更宽的规则遮挡关联规则。所有初始规则均可编辑或删除，移除后重启应用不会重新添加。Pi 原生登录来源没有配置 API 地址时，继续使用 Claude/OpenAI 等手动规则。

## v2rayN TUN 共存

「TUN 与出口绑定」默认使用 sing-box 的 `route.auto_detect_interface`，让节点连接和直连请求绑定默认网卡；也可使用 `route.default_interface` 指定物理网卡。两者是 sing-box 官方提供的 TUN 路由循环规避能力：[Route 文档](https://sing-box.sagernet.org/configuration/route/)。自动识别异常时请选择实际联网的物理网卡；指定网卡失效时启动报错，不静默退回系统路由。需要跟随其他 VPN 时可显式选择「跟随系统路由」。

独立 DNS 可填写 DNS 服务器 IP，使用绑定网卡的 UDP 53 解析；留空沿用系统解析，仍可能经过 TUN / FakeIP。本地 API 和本地代理上游使用回环地址绑定，localhost 使用内置 hosts 解析，不依赖独立 DNS 是否可用。若所选节点本身指向 v2rayN 的 localhost / 127.0.0.1 入口，后续路径仍由 v2rayN 决定。强制网络过滤器、其他 VPN 策略和实际节点连通性仍需按环境验证。本功能不修改 v2rayN、系统路由或系统代理设置。

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

统一入口按目标域名分流，因此同一个 Pi / OMP 会话及其继承环境的子 Agent 可以同时调用多个服务。第三方 API 域名需加入对应规则；相同 HTTPS 域名下不同模型无法仅凭域名区分。专用入口可用于单独配置的外部客户端。

启用时会覆盖进程原有的大小写 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY`、`NO_PROXY`；绕行清单固定包含 localhost、127.0.0.1、::1，防止本地 Headroom/relay 被外部代理接管。功能停用时不修改现有启动策略，也不改变父进程环境或系统代理设置。内核意外退出时，新建会话明确失败，避免悄悄改走其他出口。

保存配置保留草稿，活跃会话继续使用已应用的端口。应用更改会重启本功能的内核，已有连接会断开；修改后需重启已有 CLI 或 Headroom 进程以接入新环境。停用会停止本功能内核；正常退出 CodeBox 时清理进程，但保留启用偏好供下次启动恢复。启动时先加载提供商，再恢复关联规则；代理偏好尚未读入时，应用内自动模型探测与新建会话返回初始化未完成错误，避免先走旧出口。

## 数据与边界

- 配置位于 `~/.amagi-codebox/ai-proxy/state.json`，运行配置位于同目录的 `runtime/core.json`。目录权限 0700、文件权限 0600；其中的节点凭据不是加密存储，应按私密配置保护。现有模型 API Key 仍走 CodeBox 原有密钥服务。
- 配置结构为版本 3。版本 1 的固定字段迁移为两条规则，版本 2 的规则原样保留（包括主动清空的规则列表），启用自动网卡绑定；保留所选节点、域名、专用端口、上游设置、订阅与启用偏好，迁移只执行一次并原子保存。旧版不支持新增策略，不应降级覆盖版本 3 配置。
- Wails 只返回节点名称、类型、选择 ID 和运行状态，不返回节点凭据或订阅 URL；带认证的下载/上游代理以占位符返回。
- v2rayN 数据库使用 SQLite `file:` URI 的 `mode=ro`，读取同一事务快照。同步失败或某分组没有兼容节点时保留已保存快照。删除分组不会修改 v2rayN；源仍存在时下次同步会重新载入。
- 本功能管理本机，远程宿主使用宿主自己的配置。WSL 会话仅在 WSL2 mirrored 网络模式下接入分流（localhost 双向可达，代理变量经 WSLENV 原样跨界）；NAT/未知模式下启动被拒并提示在 `.wslconfig` 切换 `networkingMode=mirrored`，不会注入一个无法保证可达的端点。
- 默认网卡绑定用于规避常见 TUN 路由接管；本地代理上游、系统 DNS、强制网络过滤策略的边界见上文。
- 节点转换和配置检查成功不等于服务地区、账号权限或真实网络可用；没有自动测速、自动换节点或周期更新。

## 验证

```sh
SING_BOX_TEST_BINARY='/path/to/sing-box' go test -race ./internal/aiproxy/... ./internal/launcher ./internal/headroom ./internal/gitassist
V2RAYN_TEST_DATABASE='/path/to/guiNDB.db' SING_BOX_TEST_BINARY='/path/to/sing-box' go test ./internal/aiproxy/v2rayn -run TestLocalV2rayNCompatibility -v
npm --prefix frontend run build
npm --prefix frontend run test
```

`app_ai_proxy_browser_test.go` 提供隔离的真实 Go/Wails 门面桥接，配合 `e2e/ai-proxy.html` 和 `e2e/ai-proxy.vite.config.mjs` 验证 Vue 页面。该夹具只使用合成数据库、临时应用目录和测试节点，不读取真实模型凭据。

[服务提供商关联截图](ai-proxy-preview.png)与 [TUN 管理截图](ai-proxy-tun-preview.png)使用上述测试节点；实际节点和端口以应用内配置为准。

验证覆盖：提供商双协议/旧格式/IDN/IPv6 域名提取与凭据隔离、来源更新/删除/共域冲突、启动恢复、三种出口模式、本地上游和 DNS 不可用时的回环 API、TLS/SSE、生命周期及五种 CLI 接入。浏览器验证了关联、API 地址变更后保留出口与名称草稿、添加第三种服务和指定物理网卡。TUN 开启的本机上，自动识别 en1、指定 en1 并使用独立 DNS 的 HTTPS 探测均返回 200；这不代表真实订阅节点或模型账号已验证。前端 25 个文件、213 项测试通过。完整 Go 套件仅平台路径解析 1 项在本机失败，在未修改的 `2b231d7` 基线上复现，涉及本机 Codex 路径优先级。

本机打包使用项目 CI 的 Go 1.25.0。较新的 macOS SDK 需要额外链接 UniformTypeIdentifiers：

```sh
GOTOOLCHAIN=go1.25.0 CGO_LDFLAGS='-framework UniformTypeIdentifiers' \
wails build -s -skipbindings -m -nosyncgomod
```

新增运行代码复用同工作区 ai-proxy-router 的订阅解析、v2rayN 读取和内核管理逻辑，依赖均已在 CodeBox 的 vendored 模块中存在。
