# Changelog

本项目所有值得记录的变更都会维护在此文档中。

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)，版本章节沿用仓库现有 Git 标签。

## [Unreleased]

## [1.3.35] - 2026-08-18

### Fixed

- 修复 Windows 内嵌 pi 会话 WebUI 探测始终无法采纳的问题：Windows 下内嵌 pi 走 BootstrapShellAttach（ConPTY 只起 shell，pi 命令经输入流注入），PTY pid 是 shell pid 而非 node pid，pid 等值校验在该架构下必然失配，探测被 `validateAdoption` 拒绝。修订采纳强校验为「token 即身份」：token 探测（token 非空且得到 200）时豁免 pid 校验——注入的 `AMAGI_WEBUI_TOKEN` 是壳与扩展间的共享密钥，`/api/info` 受 capability 保护（错误 token → 401/403 不可达），携正确 token 的 200 已证明服务归属；注册表回退扫描 token 相等也入围（条目 pid 是 node pid 与 tracker 的 shell pid 必然失配）。token 为空（legacy/独立终端未注入）时维持 pid 防线不变（防端口被其他进程复用）。附带修复 webui 包 gofmt 格式问题。

## [1.3.34] - 2026-08-18

### Fixed

- 修复 Windows 启动会话报 `conpty start: Failed to create console process: The directory name is invalid` 的问题：workDir 全链路此前只在空值时回退默认目录，非空值从不校验，陈旧默认目录、笔误、带引号路径会直穿到 CreateProcessW 的 lpCurrentDirectory 导致进程创建失败（ERROR_DIRECTORY 267）。新增 `launch_workdir.go` 单一校验/回退 choke point，全部 `Launch*` 入口（ClaudeCode/Codex/Pi/OMP/OpenCode）统一接入：requested 先 Clean+Abs 归一化，候选链 requested → defaultPath → 用户 Home 逐个 Stat 校验（存在且是目录），回退发生记 Warn（含原始路径/原因/回退目标），全部候选无效才报错。

### Added

- 完整配置导出（v2 快照）新增 CLI 独立配置全文 section：pi 的 `models.json`/`auth.json`/`amagi.json` 与 omp 的 `config.yml`/`models.yml`。导出为尽力而为语义——文件不存在静默跳过（不导出空骨架）、读取失败或内容非法记 Warn 跳过单个字段不阻断整体导出；导入时存在的 section 按整体替换语义写入目标设备（与 codebox 托管的 provider 配置共存），缺失 section（含旧版 v2 导出文件）自动跳过、行为不变。内容含明文凭据（pi auth token、内联 apiKey），与顶层 provider api_key 明文导出语义一致。附 292 行封闭式测试。
- 皮肤新增「前景文字加深」（`SkinSettings.TextBoost`，默认 0=不增强，0-100）：三级前景文字 token（label/secondary/tertiary）按强度向 black `color-mix`，并为设置页行标签、侧栏会话标题、导航项叠同强度淡色 text-shadow 底衬，保证任意背景图下文字可读；0 档移除变量整组声明失效回退原色（0 档=现状）。与 dim（背景蒙版）、opacity（面板本体）三者独立解耦。

## [1.3.33] - 2026-08-18

### Added

- 皮肤功能新增「内容不透明度」档位（`SkinSettings.Opacity`，默认 70，0-100 与 dim 解耦）：dim 调背景蒙版层，opacity 调内容面板（窗口/侧栏/卡片等）本体——窗口面 token 改以 `color-mix` 按 `--skin-panel-alpha` 混合 transparent，皮肤层经面板半透明透出；0 档可读性由 0.12 下限 + dim 蒙层保底。老 settings.json 无 skin 键时整体回落默认（dim=35、opacity=70）；含 skin 键但缺 opacity 子键时读入 0（合法档位不回填，前端滑块即时调整）。
- Web 平面透皮：皮肤模式下 Web 平面激活时宿主底转透明（`html[data-skin='on'] .term-body.web-active` 与 `WebPlaneHost` 宿主/iframe 透明），皮肤层经 webui 内嵌页（body 透明）一路透出；`WebPlaneHost` 加载期/错误态与 ended badge 用 `backdrop-filter` 压花保证文字对比度。
- 外观设置页新增内容不透明度滑块；`--skin-panel-alpha` CSS 变量注入与 watch 同步（App.vue）。

## [1.3.32] - 2026-08-17

### Added

- 新增本地图片皮肤（壁纸）功能：
  - 后端 `internal/skins`：皮肤图片库位于 `~/.amagi-codebox/skins/`，导入即拷贝为随机 hex id（源文件不受影响），仅接受 png/jpeg/webp（魔数校验防改后缀，≤20MB），尺寸解析 png/jpeg 用 `DecodeConfig` 只读头部（webp 记 0）；通过 Wails assetserver 自定义 Handler 以 `/skins/<file>` 只读访问（不提供目录列表）。导入（ImportSkinImage）是唯一写入口。
  - 设置层：`SkinSettings`（enabled / imageId / dim 0-100 默认 35 / blur 0-40）持久化到 settings.json，clamp 越界值、零值回落默认。
  - 前端：设置页新增「外观」子页（`AppearanceSettings`）——缩略图网格、导入（原生文件对话框）、删除、启用/关闭、调光与模糊滑杆；`skin` store 启动加载并 watch 同步 `--skin-image/--skin-dim/--skin-blur` CSS 变量与 `html[data-skin]`，保存即时生效无需刷新；`App.vue` 挂载 `.skin-layer`（cover 背景 + 可调模糊）与 `.skin-dim`（调光蒙版，保底 0.35 防过亮），窗口面 token 转半透明让背景透出，终端区域不透明不受影响。
  - 绑定与文档：`Skins` 服务接入 bind 列表与 Startup（SetContext 后才能弹原生对话框）；docs/api.md 记录新接口。

## [1.3.31] - 2026-08-17

### Fixed

- 修复 pi WebUI 在 TUI 内执行 `/resume`、`/new`、fork、reload 后失联的问题：这些操作在同进程内切换会话，sessionId 必变而 pid 不变，原粘性校验把 sessionId 变化当作身份漂移拒绝采纳。后端粘性键放宽为 pid——注册表回退扫描 pid 一致也入围，`validateAdoption` 粘性复核 sessionId 失配不构成拒绝（pid 失配仍拒绝，端口被其他 pi 进程复用的防线不变），`adoptLocked` 跟随更新 piSessionID 并记「webui 会话切换」日志。
- available 后的保活探测遇瞬时 503（会话切换/服务重建窗口的 ready=false，pid 已校验确属目标进程）不再降级——unavailable/ended 只由持续不可达（failStreak）或会话退出（Invalidate）决定。
- 前端探测轮询：available 由终态改为低频保活（800ms 探测节奏 → 3000ms），跟随会话切换导致的 url 演进；TerminalView 监听 webuiStatus.url 变化同步刷新 webUrl（老扩展端口漂移场景），URL 未变时的强制 reload 仍由 WebPlaneHost 既有机制接管。

## [1.3.30] - 2026-08-16

### Fixed

- 修复 Dropdown 下拉菜单被祖先容器裁剪的问题：菜单改为 Teleport 到 document.body + `position:fixed`（left/top/bottom/width/max-height 由 JS 内联注入），彻底脱离 AppShell 主滚动容器 `overflow:auto` / 壳层 `overflow:hidden` 的裁剪——此前列表尾部行的菜单会被裁掉。
- 下拉菜单空间自适应：视口下方空间不足（< 320px 上限）且上方更宽裕时自动向上翻转（独立 `dropdown-fade-up` 过渡方向）；maxHeight 按可用空间钳制；宽度保底 140px 并向视口内钳制。
- 滚动/缩放行为：菜单打开期间监听 scroll（capture，覆盖 AppShell 内层 overflow 容器）与 resize 实时重定位；触发器滚出视口时直接关闭菜单。
- z-index 层级：菜单 2000（高于 Dialog 1000，对话框内下拉不再被遮挡），低于 SessionDetailModal(3000)/Toast(9999)/TerminalContextMenu(10000)；外点关闭判定覆盖 teleport 后的触发器 root + 菜单元素；`prefers-reduced-motion` 下禁用过渡。

## [1.3.29] - 2026-08-16

### Chore

- 版本维护发布（无代码变更）：同步 wails.json / package.json / 锁文件 / README 徽章 / md5 至 1.3.29。

## [1.3.28] - 2026-08-16

### Docs

- 新增 AI CLI 本地 Web UI 能力调研文档（agent-outputs/webui-research.md）：梳理 OpenCode（`opencode web` / `opencode serve`，含鉴权与关键端点）、OpenAI Codex CLI（无本地 Web UI，仅有实验性 `app-server`）等 CLI 的本地 Web 平面现状，作为 webui 壳集成（v1.3.27）后续迭代的参考资料。

## [1.3.27] - 2026-08-16

### Added

- Pi 会话 WebUI 壳集成（TUI/Web 双平面）：
  - 后端 `internal/webui`：端口分配 + env 注入、探测状态机（PID/sessionId/port 强校验、注册表目录式回退 + 陈旧淘汰、ended 终态栅栏、锁外 IO）、capability token 注入与 fragment URL；新增 `WebUI` 绑定服务。
  - 前端：Segmented 切换（仅 pi + available 时显示）、`WebPlaneHost` iframe（`sandbox=allow-scripts allow-forms`）、sessionId 隔离、探测单飞轮询、迟到结果丢弃。
  - 生命周期：`RemoveSession`/批量清理/adapter 路径的 tracker 清理。
  - 测试：`internal/webui` 14 用例（`-race` 通过）+ terminal e2e a11y 断言；三态真实 E2E 全过（A-1/A-2/A-4/M2）。

## [1.3.26] - 2026-08-15

### Fixed

- Pi 插件登记匹配逻辑统一化：把 v1.3.25 在 `SwitchPackageSource` 里做的双向归一匹配（面板 local 源绝对形态 ⇄ settings 相对形态）提取为通用的 `findRegistered`/`containsSource`，并推广到全部登记型操作——`GetPackageDetails`、`RemovePackage`（未登记时不再提前报错，透传原值让 CLI 报错）、`UpdatePackage`（改用 settings 原始登记串调 CLI，消除 cwd 失配路径）。local 源在各操作间行为一致，不再只有 switch 一条路径做过归一化。

## [1.3.25] - 2026-08-15

### Fixed

- 修复 Pi 插件 local 源在 remove/switch 时报「No matching package found」的问题：pi 的包匹配 key 对 local 源输入侧按 `process.cwd()` 解析相对路径、settings 侧按 agentDir 解析，GUI 进程 cwd（通常为 /）≠ agentDir 时面板回传 settings 原样字符串必失配。三处根治：
  - `executePiCommand` 统一以 `agentDir` 作为子进程工作目录，输入侧与 settings 侧同一解析基准；
  - `inspectPackage` 对 local 源的 Source 输出归一为绝对路径（相对形态按 agentDir 解析），面板 remove/switch 回传绝对路径稳匹配；ID 保留 settings 原始字符串供精确登记定位；
  - `SwitchPackageSource` 登记匹配改为双向归一（settings 相对形态 ⇄ 面板绝对形态），切换前回写 settings 原始字符串。

## [1.3.24] - 2026-08-15

### Added

- Pi 插件新增包源切换（git ⇄ npm ⇄ 本地路径）：后端新增 `SwitchPackageSource`——旧源在 settings.json 登记后原子执行 remove（实体保留）→ install 新源，失败自动回滚重装旧源；新源已登记时拒绝操作（避免双引用并发加载冲突，2026-08-15 实战踩坑），同源直通；前端 Pi 插件面板新增源切换入口。

## [1.3.23] - 2026-08-15

### Fixed

- 修复 Pi/OMP 预设 `reasoning_effort` 静默失效的问题：`BuildPiModelsConfig`/`BuildOmpModelsConfig` 原来只在 `thinking.type == "enabled"` 时才写 `reasoning: true`，而 `reasoning_effort` 单独出现（无 thinking.type）时模型未声明 reasoning，pi 侧 `clampThinkingLevel` 会把任何 `--thinking` 值钳回 off，导致预设 `reasoning_effort=max` 长期零推理运行（实战：glm/codecode 预设）；现 `reasoning_effort` 非空同样开启 reasoning 并开放 xhigh/max 扩展思考级别，与 thinking 开关同一语义。
- 修复终端偶发跳顶：xterm 6 虚拟滚动面在 renderer 维度抖动（DPR/fit/WKWebView 滚动条重排）时内部 ScrollState 钳制可能把 scrollTop 瞬时归 0 且 `_sync` 在 ydisp 未变时不恢复位置；新增滚动跳变 guard——区分用户主动滚动（滚轮/滚动条/翻页键）与非用户意图跳顶，后者发生时若此前视口贴底则自动滚回底部。

### Added

- macOS 终端 Option+key 支持：按 Option 组合的单字符键与 Backspace 转为 ESC 前缀序列转发给 PTY，使 pi/amagi 的 ⌥W 画板、⌥T 任务、⌥R 审查等 Alt 快捷键可用（此前 xterm 默认 macOptionIsMeta 关闭，Option 键会输入特殊字符而非真实 Alt 绑定）；箭头/组合键/Cmd/Ctrl 组合不受影响。

## [1.3.22] - 2026-08-15

### Added

- 价格表新增 glm-5.3 / glm-5.1 定价条目：官方 API 计价未公布前临时沿用 GLM-5.2 费率（input 2 / output 8 / cache-read 0.2 CNY per M，注释标注「临时价」，可在价格表 UI 编辑后自动重算）；`Load` 时对历史记录按新条目重算本地估算成本（GLM-5.3 与 GLM-5.1 各一批），OpenCode 供给的 `cost_provided` 记录保持不动。

### Changed

- Provider Center 预设页层级重构：原来五项并排拆为两组——「格式预设」（Anthropic/OpenAI）与「CLI 独立配置」（OpenCode/Pi/OMP），用组标签 + 分隔线区分层级；三 CLI 配置组件改为懒加载，视图 chunk 从 213KB 降到 40KB。

## [1.3.21] - 2026-08-15

### Fixed

- 修复配置保存的并发 map 迭代/写入竞态：`ConfigService.Save` 原来在读锁内取指针、释放锁后再无锁 scrub+marshal+写盘，与并发 `SaveProvider`/`SavePreset`（写锁内改写 `s.config.Models`）之间存在窗口，可能触发「concurrent map iteration and map write」进程崩溃或 race detector 告警；现改为全程持写锁走 `saveLocked`，行为不变。
- 修复「立即同步」结果归属错误：`SyncSessionUsage` 原为 `SyncAll()` 解锁后重读 `s.syncMeta`，在解锁/重加锁窗口内已等待同一把锁的后台轮次会先执行并覆盖 meta，导致前台额外阻塞一整轮（最长 10 分钟）且读到的是后台轮次的结果；改为持锁的 `syncAllLocked` 直接返回本轮 meta。
- 修复停止会话标题回填的性能问题：桌面端 2 秒轮询 session 列表时，`List()` 对无标题的 stopped claudecode 会话每次都会把 jsonl 重扫到 EOF；新增按 (mtime, size) 指纹的负结果缓存，文件未变时跳过重扫，追加写入会令缓存失效从而仍能检测到后补标题。
- 修复 terminal_preset 桥接的跨配置代次快照：`LaunchSession`/`LaunchOpenCode` 原为 `GetProvider` + `GetPresets` 两次独立加锁，之间并发改写会拼出混合快照；新增 `SnapshotProvider` 在单次读锁内返回 provider 与 Presets 的同代深快照（Presets 非 nil 副本，可直接注入桥接条目）。
- 修复外部清理存储（external_cleanup_store）`Reserve`/`Register`/`Complete` 等路径忽略 `applyEvent` 错误的问题：事件追加成功后内存态应用失败现在会正确返回错误，不再静默吞掉。

### Changed

- 前端 API 层统一错误处理语义：全部 `frontend/src/api/*` 模块改用共享的 `callApi` 包装器（以 `[api.<module>.<fn>]` 上下文打印日志后原样 rethrow），行为与直接调用 wails 绑定一致。
- 终端 WebGL 渲染器改为动态加载：`@xterm/addon-webgl` 从主 chunk 静态依赖拆出，仅非 macOS 且探测通过时按需 import，带 in-flight 去重与 context-loss 重试，失败回退 DOM renderer（对齐 mobile 动态 xterm 栈做法）。
- 移除前端 `element-plus` 依赖及样式覆写文件（`element-overrides.css`），按现有自绘组件风格收敛。
- Headroom 共享代理明确单租户语义：启动非 headroom ClaudeCode 会话时主动拆除 :8787 代理属于已文档化的设计决策，`Stop` 失败不再被吞掉而是记录警告日志。
- CI 新增 golangci-lint 门禁（v2.12.2，与本地 pinned 一致，跑在 matrix 两条腿上覆盖平台专属文件）；前端新增 eslint（10.x + eslint-plugin-vue + typescript-eslint）与 `check:bundle` 产物校验脚本。
- 清理已无调用方的历史代码：zhipu/minimax API Key 专用存取方法、Origin 解析中的未用字段与辅助函数（`pairEndpointHostOK`/`asciiHost` 等）、`checkSharedLease` 等。

## [1.3.20] - 2026-08-14

### Fixed

- 修复从 CodeBox 启动的 Pi/OMP 终端加载缓慢甚至一直无法加载完毕的问题（网络/代理抖动时的启动期挂起）：
  - 系统代理注入的健康探测从「TCP 端口可达」升级为「TCP + HTTP 级探测」——部分代理 App 异常时端口仍接受连接但不转发流量（活端口、死代理），旧探测会把会话全部流量打进死代理，导致 pi 启动期网络操作（pi.dev 模型目录刷新、版本检查）长时间挂起；现在此类代理不再被注入，会话回退直连。
  - Pi/OMP 会话默认注入 PI_OFFLINE=1（pi 官方语义：仅禁用启动期网络操作，不影响模型推理与 amagi MCP），使内嵌会话启动不再依赖最易抖动的网络点；用户在环境变量面板显式配置 PI_OFFLINE 时尊重其值。模型目录仍可通过 `pi update` 手动刷新。
- 附带说明：MCP 工具发现速度还取决于 amagi 的 MCP 路由（mcp.json/.agents/.mcp.json 中的服务器及其所需 API Key）。CodeBox 环境变量面板注入的密钥会让这些服务器真正启动（npx stdio 冷启动约 1-6 秒/个），这是普通终端（无密钥、服务器秒失败跳过）显得"秒开"的对照原因。

## [1.3.19] - 2026-08-14

### Fixed

- 修复带提供商启动 Pi 会话（含内嵌终端）即崩溃的问题：v1.3.13 的 provider sync 重构在 LaunchPiSession 中留下了未配对的 providerSyncMu.Unlock()，对未加锁互斥量解锁触发 Go fatal error（sync: unlock of unlocked mutex），进程直接退出。补回与 Codex/OMP 对称的 Lock()。
- 修复 LaunchCodexSession 持有 providerSyncMu 从不解锁的问题（同一重构引入）：Lock 后既无 Unlock 且存在持锁提前 return，一次 Codex 启动即永久占用互斥锁，导致后续 Pi/OMP 启动与配置保存全部死锁。改为闭包 + defer，所有路径（含校验失败返回）均正确解锁。

## [1.3.18] - 2026-08-14

### Added

- 模型下拉目录合并内置提供商（如 openai-codex 等 OAuth 登录提供商）：pi 侧合并 models-store.json 内置模型目录缓存，omp 侧通过 `omp models ls --json`（5 秒超时，CLI 不可用时静默降级为仅注册表）拉取内置模型；与注册表重名时自定义条目优先。
- provider 下拉新增来源标注：已认证 ✓、内置目录「（内置）」（凭据由 CLI 自身管理）、未认证注册表「（未认证）」；openai-codex 的 OAuth 状态由 auth.json 正确识别为已认证。
- 附带封闭式回归测试：内置目录合并/重名覆盖/认证状态标注（builtin_catalog_test.go ×2）。

## [1.3.17] - 2026-08-14

### Added

- Pi 引擎新增「认证登录」子标签：可视化管理 auth.json 提供商凭据——API Key 条目密文可编辑（留空移除字段），OAuth 条目只读展示登录状态（accountId / 过期时间，token 不展示且保存时原样保留），未知类型与额外字段走递归可视化编辑；支持添加 API Key 认证并一键填入注册表中未认证的提供商名。
- 模型目录（pi/omp）新增 hasAuth 认证状态标注：Agent 配置的 provider 下拉以「✓ /（未认证）」标明凭据状态，避免选到无凭据模型。pi 的凭据来源 = auth.json 条目或 models.json 内联 apiKey；omp 凭据内联在 models.yml（apiKey / auth / authHeader），已在注册表编辑器中可编辑。
- 后端 piconfig 新增 auth.json 读写 API（校验 + 原子写入 0600），并附带封闭式回归测试（auth_config_test.go，含目录不泄露凭据内容的断言）。

## [1.3.16] - 2026-08-14

### Changed

- 模型提供商注册表编辑器实现「可视化完全可视化」：移除全部 JSON 兜底文本框，可视化模式下所有字段均用结构化控件编辑。
- 高级字段专用可视化编辑器：thinkingLevelMap 用「输入级别 → 输出级别」行编辑器（标准级别下拉 + null 支持）；thinking 拆分为 mode 输入与 levels 列表编辑；input 用能力列表编辑；cost 用输入/输出/缓存读/缓存写四项数字编辑（全空自动移除字段）。
- 新增 VisualValueEditor 递归可视化值编辑器：按类型分发字符串/数字/布尔/字符串列表/通用数组/嵌套对象，未知字段（auth、compat 等）全程可视化编辑，类型在写回时保持；provider 级其他字段、模型级未知字段与顶层其他键均支持可视化增删改。

## [1.3.15] - 2026-08-14

### Added

- Provider Center 的 Pi / OMP 引擎标签新增三级子标签「Agent 配置 | 模型提供商」，模型提供商注册表（models.json / models.yml）接入可视化编辑。
- 新增 ProviderRegistryEditor 共享组件：每个提供商一张折叠卡片，支持 api 协议下拉（五种已知协议，未知值回退文本输入）、baseUrl、apiKey 密文编辑（留空即移除字段）、模型条目增删改（id / 显示名 / contextWindow / maxTokens / 推理开关）。
- thinkingLevelMap、thinking、cost、compat、auth 等高级字段通过 JSON 兜底编辑器修改并原样保留；顶层未知键同样保留；amagi-* 前缀提供商显示「由提供商中心同步管理」提示。
- 后端 piconfig / ompconfig 新增注册表全文读写 API（校验 + 原子写入 0600），并附带封闭式回归测试（models_config_test.go）。

## [1.3.14] - 2026-08-14

### Added

- Provider Center 新增 Pi（amagi-pi）与 OMP（oh-my-pi）可视化配置入口：在「预设」页新增 Pi / OMP 引擎标签，支持对 amagi.json 与 config.yml 进行可视化/源码双模式编辑。
- Pi 配置可视化：profile 分层策略选择、各 agent 角色的模型分配、MCP 路由（默认服务器 + 角色附加服务器）；模型通过 provider → model → thinking level 三级下拉关联，数据来自 models.json 注册表，避免手写 `provider/model:level` spec 出错。
- OMP 配置可视化：modelRoles 角色模型绑定、task.agentModelOverrides 子代理覆盖（支持 `@role` 引用与直接模型 spec 两种形态并可切换），模型下拉数据来自 models.yml 注册表；其余配置键原样保留。
- 后端新增 piconfig / ompconfig 服务：原子写入（临时文件 + rename，0600）、保存前 JSON/YAML 校验、从模型注册表抽取不含密钥的只读目录供下拉使用。

## [1.3.13] - 2026-08-14

### Added

- Provider Center 新增 OpenCode、Pi 与 Oh My Pi 提供商统一同步：仅接管各配置文件中的 `amagi-*` 命名空间，同步模型、参数与凭据，同时保留用户自有 Provider 和登录认证数据。
- 远程启动器新增每次会话的工作目录、服务提供商、终端预设、模型、Shell 与 Claude Headroom 设置，并通过不含密钥的安全契约传递。

### Changed

- 终端预设改为按 Anthropic、OpenAI 协议格式共享；Claude Code 使用 Anthropic 预设，Codex、Pi 与 Oh My Pi 共用 OpenAI 预设，历史 CLI 专属预设会无损迁移。

### Fixed

- 修复环境检测未查询 Pi 与 Oh My Pi 最新 npm 版本、导致存在新版本时仍显示“已安装”而不是“有更新”的问题。
- 修复远程 Web 将 CLI 可用性错误绑定到“最近一次桌面启动配置”、导致 OpenCode、Codex、Oh My Pi 等已安装终端仍无法启动的问题。
- 修复远程会话停止或进程退出后卡片不会自动清理的问题；大厅会过滤终止状态并定时刷新。

### Removed

- 移除已失去实际用途的工作区管理功能，包括桌面端工作区面板、项目级与全局插件部署、冲突检测、工作区持久化、完整配置中的工作区快照以及对应 Wails 绑定。
- 历史完整配置中的 `portable.workspaces` 字段继续兼容读取但会被忽略；现有工作区清单和部署产物不会被应用自动删除。
- 移除 Prompt 注入代理功能，包括注入规则页、会话代理开关、代理服务、实时代理用量来源、远程契约字段、持久化与 Wails 绑定。
- 历史完整配置中的 `portable.proxy` 字段继续兼容读取但会被忽略；现有注入规则与代理 URL 历史文件不会被应用自动删除。

## [1.3.12] - 2026-08-14

### Added

- 新增遵循 Keep a Changelog 与 Semantic Versioning 的项目变更日志，并接入 README 和发布流程文档。

### Fixed

- 修复 macOS 上“导出完整配置”和“导入完整配置”按钮点击无反应的问题：改用应用内确认弹窗，不再依赖 WebKit 环境中不可靠的浏览器原生确认框。
- 修复 macOS 钥匙串加载缓慢时完整配置操作被内部锁永久阻塞的问题；完整导出会优先打开保存对话框，并在密钥尚未就绪时给出明确提示，避免生成缺失密钥的“完整”配置。
- 完整配置导入、导出操作增加执行中状态、防重复触发和可读错误提示。

## [1.3.11] - 2026-08-13

### Added

- 应用启动后自动检查新版本，并提供全局更新提醒和软件更新页入口。
- Windows 自动更新增加独立更新助手，在主进程退出后替换被锁定的可执行文件，并在失败时回滚。

### Changed

- 更新检查与下载支持瞬时网络错误、GitHub 限流和服务端错误重试。
- 平台安装包尚未上传完成时，仍会报告新版本并提供 Release 下载页。

### Fixed

- 修复未注入构建版本时，界面版本与更新服务当前版本不一致的问题。
- 修复下载响应被截断但未返回读取错误时可能应用不完整更新包的问题。
- 升级 npm 依赖以修复已知安全告警。

## [1.3.10] - 2026-08-13

### Added

- 新增完整配置导出与导入，可迁移服务提供商、密钥、预设、应用设置、路径、环境变量、工作区、代理规则、价格表和 OpenCode 全局配置。
- 完整配置导入采用整体替换语义，并在失败时尽力回滚原配置。
- 新增 Codex 配置可移植性支持。

### Changed

- 清理内置服务提供商和终端预设，新安装使用干净的初始环境。
- 完善远程会话的配置与状态闭环。

## [1.3.09] - 2026-08-10

### Changed

- 统一桌面端与远程端的真实会话模型、生命周期和控制权限。
- 增强远程会话的启动规划、进程身份校验、断线恢复与清理补偿机制。

### Fixed

- 修复 OMP 启动测试在不同运行环境下不稳定的问题。
- 修复远程会话状态、PTY 就绪和连续性处理中的一致性问题。

## [1.3.08] - 2026-08-09

### Fixed

- 修复 Pi 终端多行输入处理。
- 修复终端刷新时视图意外跳回顶部的问题。

## [1.3.07] - 2026-08-09

### Fixed

- 修复 OMP 自定义服务提供商配置未正确写入 `models.yml` 的问题。

## [1.3.06] - 2026-08-07

### Added

- Provider Center 新增 OMP 预设管理。
- 扩展管理新增 OMP 插件面板，支持插件列表、安装、更新和卸载。

## [1.3.05] - 2026-08-07

### Added

- 集成 Oh My Pi（OMP），支持会话启动、`models.yml` 配置、环境检测与安装。
- 新增 OMP 用量解析与同步，并在桌面端和移动端展示 OMP 会话。

### Fixed

- 修复远程长会话连续性缓冲淘汰时可能误判故障的问题。

## [1.3.04] - 2026-08-06

### Fixed

- 修复用量仪表盘的聚合、读取和模型趋势展示问题。
- 修复 macOS PTY 与终端渲染稳定性问题。

## [1.3.03] - 2026-08-06

### Changed

- 增强终端渲染与 Provider 配置校验。

### Fixed

- 修复 OpenCode 插件更新成功后被误报为失败的问题。
- 修复 Codex、Claude 超长 JSONL 用量记录解析及历史游标迁移。

## [1.3.02] - 2026-08-06

### Added

- 完成远程控制基础、安全、会话工作区、连续性恢复和移动端体验。
- 新增远程配对、受信设备、单控制者仲裁和会话恢复能力。

## [1.3.01] - 2026-08-01

### Changed

- Pi 直接使用标准用户配置目录 `~/.pi/agent`，移除 CodeBox 隔离运行时副本。

## [1.3.00] - 2026-08-01

### Added

- 新增系统代理注入能力。

### Fixed

- 修复 Pi 兼容模式默认值。

[Unreleased]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.13...HEAD
[1.3.13]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.12...v1.3.13
[1.3.12]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.11...v1.3.12
[1.3.11]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.10...v1.3.11
[1.3.10]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.09...v1.3.10
[1.3.09]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.08...v1.3.09
[1.3.08]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.07...v1.3.08
[1.3.07]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.06...v1.3.07
[1.3.06]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.05...v1.3.06
[1.3.05]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.04...v1.3.05
[1.3.04]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.03...v1.3.04
[1.3.03]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.02...v1.3.03
[1.3.02]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.01...v1.3.02
[1.3.01]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.00...v1.3.01
[1.3.00]: https://github.com/runrunrain/amagi-codebox/releases/tag/v1.3.00
