# AI 代理分流

CodeBox 左侧「AI 代理分流」是独立页面，路由为 `#/ai-proxy`。启用后，本机新建的 Claude Code、Codex、Pi、OMP、OpenCode 会话自动使用统一代理入口，按可编辑的服务规则分别选择出口。初始提供 Claude 和 OpenAI 两条规则，也可以添加其他服务。未匹配域名默认直连，GLM 等国内模型可以沿用这一默认行为。

## 使用

1. 打开「AI 代理分流」。应用启动时会只读载入本机 v2rayN 的订阅分组、未分组节点，并合并分组内重复节点；也支持手动订阅导入。
2. 在「分流规则」中点击服务名称展开编辑，选择「关联服务提供商」；也可点击「添加规则」，选择已有提供商并「关联并添加」。系统自动读取已启用 Anthropic / OpenAI 格式的 API 地址、提取并合并主机名，只需选择出口节点或「直连」。添加面板也提供「手动添加域名」。
3. 点击「启用分流」。更改已运行的配置时点击「应用更改」。
4. 从 CodeBox 新建会话即可自动接入，无需复制代理环境变量。
5. 在 v2rayN 更新订阅后，进入「节点与设置」点击「同步 v2rayN」，再应用需要生效的变化。同步只读取已经保存的节点，不重新下载源订阅，也不改动 v2rayN 数据库。

默认统一入口为 `127.0.0.1:18890`。默认规则保留 Claude 专用 `127.0.0.1:18892`、OpenAI 专用 `127.0.0.1:18893`；任意规则都可以设置专用端口，留空则仅参与统一入口的域名匹配。停用规则时，其专用端口不监听。本功能使用 Wails 页面管理，不增加独立 HTTP 管理端口，默认端口与独立的 ai-proxy-router 工程错开。

统一入口按列表从上到下匹配，首个命中生效。每个域名包含它的子域名；重叠域名必须使用相同出口，否则会拒绝保存，例如 `api.shared.example` 与 `shared.example` 不能指定不同节点。专用入口使用所属规则的出口；所有入口的 localhost、127.0.0.0/8、::1 均优先回环直连。没有启用规则时，其他请求使用「其他域名上游」策略，默认为直连。

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

[分流规则](ai-proxy-preview.png)、[连接日志](ai-proxy-traffic-preview.png)与[节点及网络设置](ai-proxy-tun-preview.png)为新版界面的合成数据预览；实际节点和端口以应用内配置为准。

验证覆盖：提供商双协议/旧格式/IDN/IPv6 域名提取与凭据隔离、来源更新/删除/共域冲突、启动恢复、三种出口模式、本地上游和 DNS 不可用时的回环 API、TLS/SSE、生命周期及五种 CLI 接入。浏览器验证了关联、API 地址变更后保留出口与名称草稿、添加第三种服务和指定物理网卡。TUN 开启的本机上，自动识别 en1、指定 en1 并使用独立 DNS 的 HTTPS 探测均返回 200；这不代表真实订阅节点或模型账号已验证。前端 25 个文件、213 项测试通过。完整 Go 套件仅平台路径解析 1 项在本机失败，在未修改的 `2b231d7` 基线上复现，涉及本机 Codex 路径优先级。

本机打包使用项目 CI 的 Go 1.25.0。较新的 macOS SDK 需要额外链接 UniformTypeIdentifiers：

```sh
GOTOOLCHAIN=go1.25.0 CGO_LDFLAGS='-framework UniformTypeIdentifiers' \
wails build -s -skipbindings -m -nosyncgomod
```

新增运行代码复用同工作区 ai-proxy-router 的订阅解析、v2rayN 读取和内核管理逻辑，依赖均已在 CodeBox 的 vendored 模块中存在。


## 分流连接审计

### 界面工作区

页面使用固定页头与独立滚动的内容区，分为「分流规则」「连接日志」「节点与设置」。规则默认展示服务、域名摘要、出口选择和启用开关，点击服务后展开详细编辑；一次展开一条。添加后自动展开并聚焦名称，手柄保留指针拖动和上下方向键排序。页头区分未保存与已保存待应用，并保留代理重启会断开连接的提示。

日志默认显示域名出口汇总，可切换为每页 100 条的连接明细。关键词搜索与出口判断常驻，高级筛选按需展开；已应用条件显示为可移除的标签。异常行同时使用侧边标记、背景色、图标与文字强调，未知证据保留独立状态。时间范围倒置时提示修正，空记录、筛选无结果与读取失败分别显示。切换工作区保留草稿、筛选和滚动位置，隐藏日志区暂停轮询；过期请求不能覆盖新的筛选结果。

订阅、出口网卡与 DNS 集中于设置区，导入、高级路径和内核日志按需展开。三个工作区支持左右方向键、Home / End 导航，焦点可见，并适配减少动态效果偏好。窄窗口将出口选择移至服务下方；表格在自身区域横向滚动，不撑宽整个页面。

前端回归与截图（合成数据，不连接真实账号）：

```sh
node frontend/node_modules/vite/bin/vite.js --config e2e/ai-proxy.vite.config.mjs
# 另一终端：
node e2e/ai-proxy-workspace.mjs
node e2e/ai-proxy-traffic.mjs
```

真实门面与内核检查使用临时配置、测试订阅和独立端口：启动 `AI_PROXY_BROWSER_BRIDGE=1 SING_BOX_TEST_BINARY=/path/to/sing-box go test -run '^TestAIProxyBrowserBridge$' -v .`，将打印的桥接 URL 通过 `AI_PROXY_BRIDGE` 传给 `node e2e/ai-proxy-workspace-live.mjs`。脚本验证保存、关联域名更新、保留编辑内容、设置应用、内核启停与空日志状态，结束后关闭测试桥接。页面布局另在 390、720、1024、1360 像素宽度检查，宽窄两端运行 axe WCAG 扫描；浏览器模拟不能替代 Windows WebView2 或 macOS WKWebView 真机验证。

### 记录与判断

“AI 代理分流”页新增连接日志：按提供商、域名、指定出口和实际出口汇总，异常优先显示；支持关键词、提供商、实际出口、判断和本地时间范围筛选。明细每页 100 条；汇总最多显示 500 组，超过时继续筛选，底层记录不按此限制裁剪。

内核连接事件实时写入 `runtime/traffic.sqlite`，不依赖原有 32 KiB 运行日志缓冲，重启后保留。每个内核进程冻结已应用的规则、节点名称和配置版本，历史判断不会随草稿或后续配置变化。域名判断独立于专用入口，因此错误入口导致的不同节点会标红；不同规则指向相同节点不误报。回环目标按既有强制直连规则判断。重叠域名、未配置出口和缺少实际出口证据保留为无法确认。解析失败或写入错误在界面明确提示。

范围是 sing-box 输出的连接级出口选择证据，不是请求正文、逐包或字节计量，也不代表连接成功。无法证明绕过 CodeBox 的流量或上游代理之后的公网出口；因此不能宣称整机 100% 流量覆盖。仅新版本启动后的事件可用，不回填旧缓冲。数据库持续保留，当前没有自动清理策略。

验证：`SING_BOX_TEST_BINARY=/path/to/sing-box go test -race ./internal/aiproxy/...` 覆盖真实正常出口和错误专用入口；启动现有 `e2e/ai-proxy.vite.config.mjs` 后运行 `node e2e/ai-proxy-traffic.mjs` 验证合成数据下的汇总、异常强调、分页、筛选和窄屏布局。


会话入口保障：启用分流后，CodeBox 的新会话在配置尚未应用、内核停止、代理入口不可达时拒绝启动。不同出口的域名规则发生重叠时拒绝应用，避免首条规则覆盖另一个提供商指定的出口。启用期间会覆盖继承的大小写 HTTP(S)/ALL/NO_PROXY，内嵌 shell 启动后重新固定代理变量；外部进程通过统一 launcher 注入。此保障针对遵守代理设置的 Pi、Claude Code 提供商请求，不等同于对工具任意子进程的系统级网络隔离。
