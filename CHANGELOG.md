# Changelog

本项目所有值得记录的变更都会维护在此文档中。

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)，版本章节沿用仓库现有 Git 标签。

## [1.3.60] - 2026-08-31

### Added

- **环境检测新增 WSL 搜索工具检测与一键安装**：环境检测体系覆盖 WSL distro 内 fd/fdfind/ripgrep 缺失场景（pi/omp 会话在 `PI_OFFLINE=1` 注入下不会自下载搜索工具）。Windows + 可用 distro 时探测 distro 内工具状态（包级缓存，CheckAll 的 pi/omp 共享一次探测），缺失产出 warning issue 并提供 `install_wsl_search_tools` 一键方案：root apt 安装 `fd-find ripgrep`，安装前后双重缓存失效（安装前刷新 stale 快照、安装后复检读到新状态）+ 安装后验证探测，失败结构化报错并给手动兜底命令；无可用 distro 时回落原生侧 PATH 与 agent bin 检测（winget 指引），两侧互斥、WSL 优先。前端环境检测页新增「安装搜索工具」入口与 root 确认弹窗。

## [1.3.59] - 2026-08-31

### Added

- **设置页支持开关全局设备显式代理（仅 Windows）**：新增 `App.GetSystemProxyStatus`/`App.SetSystemProxyEnabled` 与 `settings.Service.GetSystemProxyEndpoint`/`SetSystemProxyEndpoint` 绑定；`internal/platform` 新增 system proxy 写入门面（开启写 `ProxyServer`+`ProxyEnable=1` 并在 `ProxyOverride` 缺失时补默认回环绕行、关闭仅摘 `ProxyEnable=0` 保留地址、写入后广播 WinINet 刷新），端点持久化到 `settings.json`（默认 `127.0.0.1:5800`），状态卡片含 TCP+HTTP 双探测可达性提示；macOS/Linux 由 `systemProxyControlSupported` 能力门控隐藏。

### Fixed

- **后台子进程闪窗**：Windows 后台 exec 点统一窗口抑制策略（`HideWindow` + `CREATE_NO_WINDOW` 收敛到 `process_policy`），覆盖扩展管理触发的 claude plugin 查询、system proxy 注册表查询、wsl.exe 探测、wslsetup、omp models 查询、gitassist、taskkill、path lookup 等，桌面端不再弹出一闪而过的终端窗口；交互式 PTY 会话不受影响。
- **WSL 终端模式 pi/omp 配置失效**：pi/omp 会话运行在 distro 内时，models.json/models.yml 改写入 WSL 侧 `~/.pi/agent`/`~/.omp/agent`（UNC 原子写 + 保留已有 provider 合并 + chmod 补偿），修复 Windows 侧配置不可达导致的 401；剥离携带 Windows 盘符路径值的 `PI_*` 环境变量，避免 WSLENV 转发后在 Linux 侧成为非法路径；WSL 内 fd/ripgrep 缺失时按进程一次探测并 WARN 给出 apt 安装引导。
- **修复主应用运行时 `wails build` 不更新前端绑定**：`main.go` 的单实例互斥会把 wails 以 `-tags bindings` 构建的绑定生成进程静默 `os.Exit(0)`，导致 `frontend/wailsjs` 停滞（新绑定方法在前端 TS2305 缺失）；bindings 模式现在跳过互斥检查。

## [1.3.58] - 2026-08-30

### Added

- **WSL 会话体验护栏**：wslsetup 安装守卫升级精确整行匹配（脏 PATH 快照不再骗过守卫）并新增生效性校验（登录 shell 探测命中须前缀 `~/.npm-global/bin`，AlreadyOK 路径自动修复脏快照）；envcheck 新增混合架构预警（WSL 内 `command -v` 命中 `/mnt/*` 时提示将运行 Windows 侧 CLI，前端徽标展示）；WSL 会话 `--cd` 至 DrvFS 工作目录时记录 I/O 性能提示；用户文档新增「何时选择 Windows 原生 Shell 会话」与「工作目录选型：DrvFS 与 ext4」两节。
- **平台默认 Shell 与启动配置透传**：设置种子按平台区分（Windows=`wsl`、macOS=`zsh`，避免 macOS 持久化不可解析值）；SetDashboardDefaults 原样透传 pi/omp 启动模式与 Shell，不再清空会话设置页选值。

### Fixed

- `CodexHomeDir` 回退优先取传入 env 的 HOME/USERPROFILE（原实现读本进程环境，Windows 上无视传入的 HOME，致回退目录错误）。

### 工程

- `.gitattributes` 行尾契约（LF 基线 + `.bat/.cmd/.ps1` CRLF + 二进制标记）：根治 Windows git（autocrlf=true）与 WSL git 对同一工作区的行尾判定分裂。

## [1.3.57] - 2026-08-26

### Added

- **Pi/OMP 会话与远程端点选择面支持 Anthropic 同步预设**：`ConfigService.ResolvePiOmpPresetKey` 统一按 OpenAI 优先、Anthropic `harness_sync` 兜底解析预设；`LaunchPlanner` 识别 Anthropic 桶同步预设并提取参数；远程客户端启动设置（`buildRemoteLaunchSettings`）与 Meta 端点将带同步标记的预设及对应 Provider 纳入可选列表；前端会话设置页支持在 Pi/OMP 下选择此类预设。

## [1.3.56] - 2026-08-26

### Added

- **Anthropic 预设选择性同步至 CLI 独立配置**：TerminalPreset 与合并视图新增 `harness_sync` 标记；`ManagedPresetModels` 支持 Anthropic 桶按标记 opt-in 补录预设及档位模型；标记预设在请求桶后序执行收集，同名模型参数与视觉能力后序覆盖；预设弹窗在 Anthropic 格式下提供 CLI 同步开关并在列表展示标记；补齐 HarnessSync 补录、参数传递与 Pi/OMP 托管配置生成测试。

## [1.3.55] - 2026-08-24

### Fixed

- **视觉模型导出收录回归手动标记并支持跨桶去重**：视觉导出文件（`~/.agents/amagi-media-models.json`）改回仅收录显式勾选 Vision/Video 的预设，`capabilities` 与手动标记精确对齐，解耦自动推断与探测扩充；探测结论仅驱动 pi/omp 托管条目 `input` 声明与前端探测提示，有定论后不再改写视觉导出文件；导出时引入跨桶 ID 去重（同名预设优先采用 openai 桶条目）；视觉导出契约同步演进至 v1.4。

## [1.3.54] - 2026-08-24

### Fixed

- **多模态配置推断与探测缓存完善**：Pi 模型配置（`pi_config.go`）对未命中 presetModels 的裸启/legacy 预设模型以内置知识库推断兜底多模态声明，避免被下游守卫误判为纯文本；`ModalityProbeKey` 实行双侧规范化（trim + 剥前缀 + 小写），保证写入与查询侧稳定命中同一键；补充多模态缓存双侧命中与 Pi 兜底单测，并严格冻结 `ConfigService` 注入与回写方法清单（`bind_manifest_test`）。

## [1.3.53] - 2026-08-24

### Added

- **模型多模态能力自动发现**：新增 `internal/config/modalities.go` 内置主流模型族能力知识库（`InferModelModalities`，按 model id 离线推断图片/视频理解能力，保守收录、未知族不猜测）。pi/omp 托管模型条目对被标记或推断为多模态的模型主动声明 `input=["text","image"]`，修复下游（amagi-pi 守卫）按默认 `["text"]` 误判多模态模型不支持图片输入的问题（实战：amagi-kimi/k3 被误拦 read 图片）；媒体模型导出（`~/.agents/amagi-media-models.json`）收录规则由仅手动标记扩展为「手动标记 ∪ 自动发现」，capabilities 取并集（契约 docs/vision-export-contract.md v1.1）。预设的识图/识视频开关保留为覆盖项，前端无变更。
- **多模态能力实弹探测**：预设/服务商保存与配置加载后，对知识库未知的模型自动实弹探测（先读 `/models` 模态元数据，未决再发 1x1 PNG 最小图片请求分类响应；网络/鉴权/限流等环境故障一律未决不落缓存）。有定论结论持久化到 models.json 的 `modality_probe` 缓存并联动重导出/重同步，能力判定为「手动标记 ∪ 探测缓存 ∪ 静态知识库」三层并集（契约 v1.2）。
- **设备端知识库学习层与手动探测按钮**：探测结论按模型 id 回写 `~/.agents/amagi-modalities.json`（跨 provider 泛化、含否定结论、原子写 0600），推断时学习层实证优先于内置规则表，形成自学习闭环（契约 v1.3）；预设弹窗「视觉能力」区新增「实弹探测」按钮（Wails `ProbeModelModalityNow`），用户可主动触发并即时看到结论，探测中/成功/未决状态行内展示。

## [1.3.52] - 2026-08-23

### Added

- **更新器专用 HTTP 客户端与网络代理支持**：更新服务统一替换 `DefaultClient` 为专用 `newUpdateHTTPClient` 实例，独立管理请求超时与传输层逻辑；引入 `httpproxy` 支持网络代理环境变量解析与转发配置（`golang.org/x/net` 提升为直接依赖并同步 vendor）；补充专用 HTTP 客户端及其网络代理配置相关单元测试。

## [1.3.51] - 2026-08-22

### Added

- **OpenAI 兼容端点自定义接口协议（wire_api）**：服务商 OpenAIFormat 新增 `wire_api` 字段，可显式指定 `chat` / `responses` 协议；URL 规范化支持剥离 `/responses` 后缀。Codex 启动参数与 config.toml 托管段透传 wire_api；Pi 与 OMP 引擎在 wire_api 为 responses 时自动映射 openai-responses 协议。前端服务商添加/编辑/详情页与远程配置面板新增接口协议切换与状态展示。

### Changed

- 终端 Git 提交面板重构为锚定浮层，会话详情弹窗同步清理。
- 架构下沉：根目录存储逻辑下沉至 `internal/cleanupstore`，平台差异收敛到 `internal/platform`；移除已废弃的 amagicode 内部 CLI 类型。
- 前端建立 vitest 单测体系，规范化测试文件与产物结构，清理历史备份文件。
- 架构设计、API 参考与用户运维文档全面同步至 1.3.50 状态。

## [1.3.50] - 2026-08-22

### Added

- **终端 AI 辅助 Git 提交/推送**：终端视图新增 Git 面板——仓库状态展示、分支切换、AI 总结变更生成提交信息、提交与推送，面向会话工作区一键完成；全部 git 操作经 `exec.CommandContext("git", ...)` 参数独立传递，杜绝 shell 注入。
- AI 提交总结模型可在设置页从已有终端预设中选择（`CommitSummaryPreset`，格式 `provider/preset名`，未设置时引导前往设置页）；API key 经注入 resolver 从 Keychain 解析，gitassist 包不直接依赖 secrets 包。

## [1.3.49] - 2026-08-22

### Added

- **视觉模型标记与导出**：TerminalPreset 新增 `vision` / `video` 能力标记与 `vision_priority` 优先级（契约 `docs/vision-export-contract.md` §1），Preset 编辑对话框与列表同步展示标记入口；标记独立于 preset 所在桶，anthropic 桶与 openai 桶均可标记。
- 懒导出 `~/.agents/amagi-media-models.json`（契约 §2）：preset/provider 增删与启动配置加载后幂等全量重导出，供 amagi-media-understanding 等识图/识视频 skill 消费；API key 经注入的 resolver 从 Keychain 解析（config 包不依赖 secrets 包），文件权限 0600，导出路径可被 `AMAGI_MEDIA_MODELS_PATH` 覆盖；无标记 preset 时写空 `models: []`，区分「未配置」与「文件缺失」。

## [1.3.48] - 2026-08-22

### Added

- **桌面端互联（远程客户端角色）**：任意安装本应用的桌面机可作为操作台连接另一台正在运行的 CodeBox（宿主）——顶栏主机切换器（本机↔已登记主机，Cmd/Ctrl+Shift+H）、配对向导（探活→宿主摘要→输码→完成）、主机登记簿（凭据仅存本机 Keychain，不落盘不上传）。
- 远程会话管理：列表/启动（Claude Code、OpenCode、Codex、Pi、OMP 五类 CLI）/停止/重启/删除；远程终端（attach、输入、resize、历史回填、断线自动重连，输入幂等 exactly-once）。
- 控制权仲裁呈现：none/you/other/desktop 四态徽标与 acquire/release 操作；被宿主桌面接管时自动降级只读（横幅+输入锁+零丢失），恢复后一键取回；设备被撤销时 fail-closed 下线并引导重新配对。
- 远程配置管理（过渡接口）：远程模式下 Provider 中心与设置页读写远端；宿主侧访问令牌仅存 Keychain；密钥字段全程掩码不可展开，占位值上行自动剔除，明文密钥在本地拦截（双层防线）。
- 宿主端修复：生产装配下 v1 WebSocket 输入/resize 写入端口未接线（静默丢弃）问题修复——`appSessionRaw` 补齐 `PTYRawPort` 并委托既有 PTY 适配器，移动端 v1 终端同步受益。
- 客户端网络韧性：WS 读超时+ping/pong 半开检测、指数退避重连、诊断日志接入应用日志（不含凭据与终端字节）。
- WSL CLI 安装支持 Pi：`cliPackages` 增加 `@earendil-works/pi-coding-agent`，状态面板与前端显示名同步支持（Pi 安装进 WSL 后与 Claude Code/OpenCode/Codex 一致地探测与安装）。
- Pi 装入 WSL 时自动播种配置：首次安装后将 Windows 侧 `~/.pi/agent`（providers/auth/models 与 amagi 资产）种子到发行版内，会话历史与备份不入内；WSL 本地已有 `.pi/agent` 时不覆盖，失败仅记录日志不影响安装。

### Fixed

- WSL 内 Node 底线从 20 升至 22（22.19）：pi 的 undici 8.x 依赖需要 Node ≥22.19（`worker_threads.markAsUncloneable` 自 22.13 引入），旧逻辑安装 Node 20 会让 pi 在模块加载即崩溃；现探测原生 Node 版本，低于底线时经 NodeSource 升级到 Node 22。
- npm 用户前缀 PATH 同时写入 `~/.profile`：Ubuntu 的 `.bashrc` 对非交互登录 shell（`bash -lc`）提前返回，仅写 `.bashrc` 时 `bash -lc pi` 会解析到 `/mnt/c` 泄漏的 Windows shim；`.profile` 兜底保证登录 shell 优先解析 `~/.npm-global/bin`。
- 仓库 `.node-version` 从 20.19.0 升至 22.23.2：配合 fnm `--use-on-cd`，项目目录内的终端不再被钉在低于 pi 依赖要求的 Node 20 上。

## [1.3.47] - 2026-08-21

### Added

- Windows 终端默认切到 WSL（`wsl.exe` 作为一等 shell）：`DefaultShellKey` 默认值改为 wsl，探测已安装发行版（排除 docker-desktop），无可用发行版时回退 pwsh/cmd；CLI 在 WSL 内按裸名解析，注入密钥经 `WSLENV` 转发；构建 `wsl.exe -d <distro> --cd <win> -- bash -lic "<payload>"` 双层引号；Windows 脚本包装器（.cmd/.ps1）内联路径保留在 Windows shell 侧。
- WSL CLI 安装（`internal/wslsetup`）：探测发行版与原生 Node，`npm i -g` 托管 CLI；App 绑定 `GetWSLCLIStatus` / `InstallCLIToWSL`；前端 `WSLCLISettings` 面板挂载在环境检查页下（WSL 不可用时自动隐藏）。
- WSL 发行版架构版本（WSL1/WSL2）标注：`WSLDistroVersions` 探测 `wsl.exe -l -v`（UTF-16 解码复用、带缓存），解析 `* default` 标记、带空格发行版名与表头；探测失败返回空 map（老版本 wsl.exe 无 -v 支持）；前端在发行版名旁显示 WSL2/WSL1 徽章。
- 附带修复本次功能提交引入的 gofmt 格式问题（wslsetup 4 个文件）。

## [1.3.46] - 2026-08-21

### Added

- 快速启动页引擎选择升级为「方块浮标」（Floating Tiles）：用带品牌色微发光、图标底盒、呼吸激活信标与悬浮微动效的方块磁贴替换原分段选择器，更直观展现各 CLI 引擎特性（顺序：Claude Code -> Pi -> OpenCode -> Oh My Pi -> Codex）。
- Provider Center 顶部新增 `AgentProfileQuickSwitch` 快捷切换挂件：可即时查看当前激活的 Agent 配置档、下拉一键切换已保存配置档、快捷快照当前 live 配置或跳转设置页。
- Provider Center 顶层导航层级重整：顶层拆分为「模型提供商」、「格式预设」与「CLI 专属配置」三个主 Tab，彻底分离跨 CLI 公共格式（Anthropic/OpenAI）与专属配置文件（OpenCode/Pi/OMP）。

### Fixed

- 皮肤模式浮层与下拉菜单实色防护：为所有下拉菜单、选项列表、浮动 Popover 与右键菜单强制注入实色纯白底色（`#FFFFFF`），彻底防止在调高透明度与面板混色时与底层背景图/内容重叠导致文字不可读。

## [1.3.45] - 2026-08-20

### Added

- Agent 配置档（v1.3.44 的 `internal/agentprofile`）补全前端界面：设置页新增「Agent 配置档」子页（`AgentProfileSettings`）——从当前配置快照为档、配置档列表（查看/应用/删除）、应用前自动备份 live 文件为 `.bak-时间戳`；侧栏设置项与路由接入。配套 `frontend/src/api/agentProfile.ts` API 封装（数据量小不引入 store）。docs/api.md 记录接口。
- 配置档 omp 侧存储从 `amagi.json` 修正为 `~/.omp/agent/config.yml`（YAML，omp 的真实 live 配置文件；留空表示不管理该侧），快照/应用/备份逻辑同步，测试补全 YAML 路径覆盖。

## [1.3.44] - 2026-08-20

### Added

- 新增命名 agent 配置档服务（`internal/agentprofile`，公司/家一键切换）：把当前 live 的 amagi 配置（pi 的 `~/.pi/agent/amagi.json` 与 omp 的 `~/.omp/agent/amagi.json`）快照为命名配置档，并一键应用回 live 文件。存储 `~/.amagi-codebox/agent-profiles.json`（0600，临时文件 + rename 原子写入，目录 0700），形状 `{"version":1,"profiles":{"<name>":{"pi":"<amagi.json 全文>","omp":"<amagi.json 全文>","updatedAt":<epoch ms>}},"lastApplied":"<name 或空>"}`。agentDir 解析复刻 piconfig/ompconfig 语义（优先 `$PI_CODING_AGENT_DIR`）。服务接入 Wails 绑定（AgentProfiles）并附 15 个封闭式测试（含原子写入与 0600 权限断言）。

## [1.3.43] - 2026-08-20

### Chore

- 版本维护发布（无代码变更）：同步 wails.json / package.json / 锁文件 / README 徽章 / md5 至 1.3.43。

## [1.3.42] - 2026-08-20

### Chore

- 版本维护发布（无代码变更）：同步 wails.json / package.json / 锁文件 / README 徽章 / md5 至 1.3.42。

## [1.3.41] - 2026-08-20

### Chore

- 版本维护发布（无代码变更）：同步 wails.json / package.json / 锁文件 / README 徽章 / md5 至 1.3.41。

## [1.3.40] - 2026-08-19

### Fixed

- 修复 Pi/OMP 启动时漏注统一同步写入的同 provider 其他预设模型的问题：启动写入是托管条目（`amagi-<name>`）的整体替换语义，此前 BuildPiModelsConfig/BuildOmpModelsConfig 只收集旧版 `provider.Presets` + DefaultModel，终端公共预设桶（openai 桶，pi/omp 消费）里的同 provider 预设模型会被挤掉。重构模型注册收集：新增 `ManagedPresetModels` 统一派生（DefaultModel → 旧版 provider.Presets → 指定 terminal 桶 preset，后源覆盖前源同 id），按 model id 排序输出确定；`buildManagedModelsConfig` 改为单次把整个 models 列表交给 builder（每个模型保留各自 Parameters，替代此前 per-model 构建再 first-seen 合并）；pi/omp 启动路径传 openai 公共预设桶的预设模型，`buildManagedModelEntries` 追加顺序为启动模型（权威）→ presetModels → 旧版 Presets → DefaultModel 兜底。裸参数继承逻辑同样先查 openai 桶 presetModels 再查旧版 Presets。

## [1.3.39] - 2026-08-19

### Fixed

- 修复 Pi 裸参数启动（default_model 直启 / 请求未带 parameters）时模型参数被剥掉的问题：v1.3.37 的多模型注册中，启动模型以零参数优先注册（同 id 去重先到先得），会把同 id 预设的 contextWindow/maxTokens/reasoning 全部剥掉——实战 glm-5.3 被裸注册后 reasoning 丢失、maxTokens 缺省回落服务端 16384，推理吃光输出预算导致 `stopReason=length` 零正文截断。修复：零值参数时回退继承同 Model 预设的 Parameters（preset 键序保证挑选确定），显式传入的参数仍优先。

## [1.3.38] - 2026-08-18

### Fixed

- 修复皮肤功能在 GPU 不可用 WebView（Windows GPU 黑名单/RDP/虚拟机常见）上拖垮终端打字回显与整体操作响应的问题：皮肤层此前对全窗背景逐帧应用 CSS `filter: blur()` + 透明根 + 全窗蒙版合成，软件光栅下每帧成本数百毫秒。新增 `skinBake` 预烘焙引擎——调参时一次性把模糊与调光烘焙进位图（`createImageBitmap` 异步解码 → 低分辨率离屏 canvas 最长边 ≤1280 上 `filter: blur()` + dim 压暗 + scale 边缘补偿 → `toDataURL`），皮肤层运行期零 filter、零逐帧混合，仅剩一张普通背景图 + 极低成本纯色层；滑块拖动 500ms 防抖重烘焙、过程中先回落原图直显不卡 UI，烘焙完成后原子换图；烘焙失败（极老 WebView 无 filter/低内存）永久回退 CSS 直显模式（行为同旧版仍正确）。blur=0 快路径零 filter。

## [1.3.37] - 2026-08-18

### Fixed

- 修复 Pi/OMP 用某预设启动时其他预设模型被整体替换丢失的问题：`BuildPiModelsConfig`/`BuildOmpModelsConfig` 此前只注册启动选中的单个模型，同 provider 其余预设的模型会被后续启动覆盖掉；改为注册整个托管模型列表——启动选中的模型排首位且参数以本次传入为准，其余预设按键序注册（各带自己的 Parameters），DefaultModel 未被覆盖时以零参数兜底，按模型 id 去重保证同 provider 多预设引用同一模型只注册一次（先注册者参数优先、输出确定，models.json/yml 幂等可比）。提取共享的 `buildManagedModelEntries`/`buildManagedModelEntry`/`appendManagedModelEntry`，pi/omp 两端同构。

## [1.3.36] - 2026-08-18

### Fixed

- 增强系统代理环境变量注入：NO_PROXY 自动同步系统代理例外列表（macOS `ExceptionsList` / Windows `ProxyOverride`），保证系统设置里配置了直连例外的内网域名与服务在 CLI 会话中同样绕开代理，避免终端被注入代理后报 503 / ECONNRESET；
- Windows 代理探测增强：`detectSystemProxy` 支持分协议格式解析（如 `http=host:port;https=host:port`），过滤 `<local>` 控制标记，并准确提取 `ProxyOverride` 例外条目合并至 NO_PROXY。

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
