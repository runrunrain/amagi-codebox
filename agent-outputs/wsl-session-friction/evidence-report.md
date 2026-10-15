# WSL 环境下启动 agent 会话：摩擦点证据报告

- 任务：梳理 amagi-codebox WSL 会话链路的摩擦点/限制/体验缺口证据（只读探查，零代码修改）
- 探查基线：仓库 X:\WorkSpace\amagi-codebox（CHANGELOG 至 1.3.105，2026-10-15）
- 方法：全文精读 docs/user 两节 + CHANGELOG WSL 全脉络 + 逐文件读 launcher/wslsetup/envcheck/platform/pty 相关源码与测试 + app.go / frontend 定向 grep。所有行号为当前工作区实读行号。

## 结论索引（每行＝主张｜三态｜锚点｜分类）

| # | 主张 | 三态 | 锚点 | 分类 |
|---|------|------|------|------|
| A1 | AI 代理分流启用时拒绝启动 WSL 会话 | 观察 | internal/aiproxy/environment.go:85-86 | A |
| A2 | Pi OAuth 登录预设不能用于 WSL 会话（桌面显式报错；remote/v1 重启路径静默按能力失败） | 观察 | app.go:3726-3729；pi_login_launch.go:147-149 | A |
| A3 | WSL shell 仅支持 embedded 启动；外部终端模式回退 direct command，WSL-only 安装的 CLI 在外部模式无法解析 | 观察 | internal/platform/resolver.go:118,253-262 | A |
| A4 | 官方文档自白：Windows 本机管理类任务建议弃 WSL 会话；DrvFs 工作目录 I/O 数倍至数十倍慢 | 观察 | docs/user/terminal.md「何时选择 Windows 原生 Shell 会话」；docs/user/usage.md:57-74 | A |
| A5 | 显式请求 wsl 但无可用发行版时静默回退默认 shell（仅 diagnostics warning） | 观察 | internal/platform/resolver.go:416-421,496-503 | A |
| B1 | Headroom 上下文压缩对 WSL 会话无护栏：base URL 注入 Windows 回环并经 WSLENV 转发，NAT 模式下 distro 内不可达 | 观察+推断 | app.go:2491-2496；internal/launcher/service.go:121-122；internal/headroom/service.go:22-26；internal/platform/wsl.go:258-264 | B |
| B2 | 系统代理 HTTP(S)_PROXY 值原样转发进 WSL（127.0.0.1 指向 distro 自身回环），无改写无提示 | 观察+推断 | internal/platform/wsl.go:266-273 | B |
| B3 | 值为 Windows 路径的环境变量仅对 PI_* 前缀剥离；ANTHROPIC_/CLAUDE_/CODEX_/OPENAI_ 及自定义键无对等防线 | 观察 | internal/launcher/wsl_agent_config.go:147-165 vs internal/platform/wsl.go:258-264,317+ | B |
| B4 | ext4/Linux 形态工作目录被无条件传给 ConPTY 的 CreateProcess lpCurrentDirectory，而文档推荐填 Linux 路径 | 观察（行为待实证） | internal/pty/service.go:321-330；vendor UserExistsError/conpty/conpty.go:157-168；docs/user/usage.md:70 | B |
| B5 | 会话 distro 不可选：固定取探测列表第一个且进程生命周期缓存永不刷新；wslsetup 仅报告单 distro | 观察 | internal/platform/wsl.go:44-52,60-69,193-197；internal/wslsetup/service.go:44-53 | B |
| B6 | 退化分支：WSL 判定命中但 distro 解析为空时配置仍写 Windows 侧，仅日志 WARN | 观察 | app.go:3521-3526,3869-3874 | B |
| B7 | omp 在混合架构预警中被建议"安装进 WSL"，但 wslsetup 安装面无 omp 条目，建议不可兑现 | 观察 | internal/envcheck/wsl_hybrid.go:34-43；internal/wslsetup/service.go:15-22 | B |
| C1 | DrvFS 性能提示仅日志 WARN（无启动期 UI 通道），不阻止启动 | 观察 | internal/pty/service.go:923-931；docs/user/usage.md:74 | C |
| C2 | 混合架构预警只在环境检测页（30s TTL、severity-only、启动路径不复检） | 观察 | internal/envcheck/wsl_hybrid.go:20-29,68-98 | C |
| C3 | WSL 内 fd/ripgrep 缺失仅日志 WARN + 环境检测页一键安装（root 确认弹窗） | 观察 | app.go:4950-4974；frontend EnvCheckSettings.vue:1032,1070-1076 | C |
| C4 | npm prefix 脏快照修复只在用户手动触发安装/重装时发生 | 观察 | internal/wslsetup/service_windows.go:191-208,271-296 | C |
| C5 | 报错文案摩擦：Pi 登录 WSL 拒绝为英文；AI 代理要求用户手动停用分流 | 观察 | app.go:3728；internal/aiproxy/environment.go:86 | C |

---

## 1. WSL 会话启动链路概述

1. 前端会话设置页选定引擎/提供商/预设/工作目录/shell（Windows 平台 `DefaultShellKey="wsl"`，`internal/platform/capabilities_runtime.go:42`；shell 候选 `"WSL (Linux)"`，`internal/platform/shell_catalog.go:36`）。
2. `App.LaunchSession/LaunchPiSession/LaunchOmpSession`（app.go）→ `buildLaunchSpec` 注入 `EnvForwardKeys`（envvars.json 键名）后调 `a.cliResolver().Resolve(req)`，再过 `aiproxy.PinProxyEnvironment(spec)`（app.go:2354-2364）。
3. resolver WSL 分支（`internal/platform/resolver.go:118-166`）：embedded 模式 + 生效 shell key 为 wsl 时短路 Windows 侧 CLI 解析，直接产出 `BootstrapWSL` spec，并经 `appendWSLENVForwarding` 生成 WSLENV 清单（:144）。
4. pi/omp 专属：启动前用 `platform.EmbeddedLaunchTargetsWSL`（wsl_home.go:237）镜像判定 WSL 模式，命中则把 models.json/models.yml 写 distro 内 agent root（UNC 直写 + distro 内 chmod 补偿，`internal/launcher/wsl_agent_config.go:88-139`）。
5. PTY 层拼装命令行：`wsl.exe -d <distro> --cd "<workDir>" -- bash -lic '<cli> <args>; exec bash -li'`（`internal/pty/service.go:883-955`）；distro 取 `platform.DefaultWSLDistro`（探测列表第一个）。ConPTY 启动 wsl.exe 进程，环境经 `ConPtyEnv`（含 WSLENV）跨界。
6. 支撑设施：`internal/wslsetup`（CLI 装入 distro + npm prefix 守卫）、`internal/envcheck/wsl_hybrid.go`（混合架构预警）、`internal/platform/wsl_home.go`（Home/UNC/chmod/搜索工具探测）。

## 2. 摩擦点证据清单

### A 类：官方明确声明未支持或受限

**A1. AI 代理分流（sing-box 统一入口）不支持 WSL 会话**
- 现象：启用分流后启动 WSL 会话直接报错，会话不启动。
- 影响：WSL 用户要么放弃分流，要么改用 Windows 原生 shell 会话。
- 证据：`internal/aiproxy/environment.go:85-86`：`if spec.BootstrapMode == platform.BootstrapWSL { return spec, errors.New("AI 代理分流暂不支持 WSL 的宿主回环转发，请停用分流后启动 WSL 会话") }`；注入点在 app.go:2364（所有会话统一过闸）。契约声明 `docs/developer/ai-proxy-routing.md:52`「WSL 的宿主回环转发尚未实现；启用分流时拒绝对应 WSL 启动」；CHANGELOG 1.3.100（CHANGELOG.md:54）；前端提示 `frontend/src/views/AIProxyView.vue:99`。
- 分类：A。

**A2. Pi OAuth 登录预设不能用于 WSL 会话**
- 现象：桌面路径显式拒绝；remote/v1 重启路径按构建失败关闭。
- 影响：Pi 订阅登录用户在 WSL 会话完全不可用该提供商类型。
- 证据：`app.go:3727-3729`：`if spec.BootstrapMode == platform.BootstrapWSL { return "", fmt.Errorf("Pi host login credentials are not available in WSL") }`；`pi_login_launch.go:147-149`（remote/restart）：`if spec.BootstrapMode == platform.BootstrapWSL { return fail() }`，fail 返回 `BuildFailure{Kind: FailureCapability}`（通用能力失败，无专门文案）。契约：`docs/developer/pi-login-providers.md:48`「WSL 进程无法消费宿主登录凭据，选择 WSL 时拒绝，而不是复制 token 或降级」；CHANGELOG 1.3.94（CHANGELOG.md:113）「WSL 场景拒绝」。
- 分类：A。

**A3. WSL shell 仅支持 embedded 启动模式**
- 现象：外部终端模式（terminal）请求 WSL 时回退 `BootstrapDirectCommand` 并附 diagnostics warning；且该回退发生在 Windows 侧 CLI 解析之后——CLI 只装在 WSL 时，外部模式直接 cli_not_found 失败。
- 影响：默认 shell 为 wsl 的用户用外部终端模式时，WSL 默认值不生效；WSL-only 安装的 CLI 无法从外部模式启动。
- 证据：`internal/platform/resolver.go:118`（WSL 分支仅 `LaunchMode == "" || "embedded"` 进入）；`resolver.go:253-262`：注释「A wsl shell reaching this tail means a non-embedded launch (e.g. terminal mode) requested WSL…」，warning 原文 `"WSL shell is only supported for embedded launches; using a direct command for this launch mode"`（:261）。resolver.go:172-177 的 `resolveCLIForRequest` 在 WSL 分支外走 Windows PATH。
- 分类：A（代码注释与测试钉定的显式限制）。

**A4. 用户文档自白的两类限制（Windows 本机管理任务 / DrvFs 性能）**
- 现象：文档明确建议 Windows 本机管理任务改用原生 Shell 会话；DrvFs（/mnt/*）小文件、git、inotify 显著慢于 ext4，「数倍到数十倍」。
- 影响：默认落在 WSL 的会话并非普适默认，用户须自行判断并手动切换 shell 或搬仓库。
- 证据：`docs/user/terminal.md`「何时选择 Windows 原生 Shell 会话」节：摩擦表列 `.exe 语义叠加 / 路径割裂 / stdin、管道语义 / 启动开销` 四项，并注明「默认值仍是 WSL——只有当任务目标是 Windows 本身时才需要手动切换」；`docs/user/usage.md:57-74`「工作目录选型：DrvFs(/mnt/*) 与 ext4(~/)」：:66「在 DrvFS 上做 git status / npm install / 大量小文件读写的耗时通常是 ext4 的数倍到数十倍」。
- 分类：A（文档级自白）。

**A5. 无可用发行版时 wsl 请求静默回退**
- 现象：用户显式选 wsl 但 distro 探测为空时，回退默认 shell（pwsh/cmd），仅在 diagnostics warnings 里留一句话，不阻断、无独立 UI 提示。
- 影响：用户以为在 WSL 里跑，实际落在 Windows shell。
- 证据：`internal/platform/resolver.go:416-421`：`if strings.EqualFold(candidate.key, "wsl") && !hasUsableWSLDistro(env) { warnings = append(warnings, "requested shell \"wsl\" has no usable distro installed; falling back to the default shell") ...`；`resolver.go:496-503`（候选解析处同门控）；测试钉定 `internal/platform/wsl_test.go:288-294,381-387`。
- 分类：A（有意的回退设计，提示面弱）。

### B 类：代码链路存在但无提示无护栏的坑

**B1. Headroom 对 WSL 会话无护栏（Windows 回环地址跨界）**
- 现象：启用 Headroom（仅 Claude Code）时，`ANTHROPIC_BASE_URL` 被注入为 Windows 侧回环地址；该变量属 `ANTHROPIC_` 前缀白名单，原样转发进 WSL。Headroom 进程只绑 Windows 回环（安全约束），WSL2 NAT 模式下 distro 内的 127.0.0.1/localhost 不是 Windows 宿主——CLI 在 WSL 内连不上压缩代理。全链路无 WSL 分支、无警告、无拦截；同场景下 AI 代理分流有显式拒绝（A1），Headroom 没有。
- 影响：WSL 会话开 Headroom 后请求可能挂起/失败，且无任何提示指明原因。
- 证据链（观察部分）：`app.go:2491-2496`：headroom 启动后 `a.Launcher.SetHeadroomPort(headroom.DefaultPort)`，无 WSL 判定；`internal/launcher/service.go:121-122`：`} else if headroomPort > 0 { overrides["ANTHROPIC_BASE_URL"] = fmt.Sprintf("http://localhost:%d", headroomPort)`；`internal/headroom/service.go:22-26`：`const headroomHost = "127.0.0.1"`，注释「Headroom must only ever listen on loopback」；`internal/platform/wsl.go:258-264`：`wslENVForwardPrefixes` 含 `"ANTHROPIC_"`；grep `internal/headroom` 全包零 wsl 匹配。
- 推断部分（前提：aiproxy/environment.go:86 自证宿主回环对 WSL 会话不可达，团队按 NAT 假设设计）：连接失败为推断结论，未在真机复现。
- 分类：B（代码链完整；实际断连待实证，见疑点 2）。

**B2. 系统代理变量原样转发进 WSL**
- 现象：`HTTP_PROXY/HTTPS_PROXY/NO_PROXY`（含小写）被列入 WSLENV 精确转发名单，值为 `http://127.0.0.1:<port>` 时在 WSL2 NAT 下指向 distro 自身回环，代理不可达；无值改写（如改成宿主 IP）、无提示。系统代理注入本身发生在 Windows 侧（`internal/launcher/service.go` BuildEnv 网络钩子，测试 service_test.go:480-497 证注入值形态）。
- 影响：Windows 侧挂代理的用户，WSL 会话内 CLI 联网行为不可预期（直连或失败）。
- 证据：`internal/platform/wsl.go:266-273`：`wslENVForwardExact` 含 `HTTP_PROXY/HTTPS_PROXY/NO_PROXY/http_proxy/https_proxy/no_proxy`；`wsl.go:317+` `appendWSLENVForwarding` 只拼名单，不改写值（注释自述「no path-translation flags: the forwarded values are keys / URLs」）。
- 分类：B（可达性与 B1 同一前提，推断部分待实证）。

**B3. Windows 路径值环境变量的剥离仅覆盖 PI_* 前缀**
- 现象：`StripWSLHostPathPIEnv` 只剥离值为盘符路径的 `PI_*` 变量；`ANTHROPIC_/CLAUDE_/CODEX_/OPENAI_` 前缀变量与 extraForwardKeys（envvars.json 自定义键）若携带 Windows 路径值，仍经 WSLENV 原样跨界。PI_* 的剥离正源自真实事故（「PI_SESSION_FILE/PI_CODING_AGENT_DIR 泄漏，在 cwd 下创建垃圾目录」），说明该类值在 Linux 侧必然非法，但同类风险对其它前缀未封堵。
- 影响：用户配置 `CLAUDE_CONFIG_DIR=C:\...` 类变量时，WSL 会话内可能复现同类非法路径行为，无护栏。
- 证据：`internal/launcher/wsl_agent_config.go:147-165`（函数只匹配 `strings.HasPrefix(strings.ToUpper(key), "PI_")`；:141-152 `isWindowsDrivePath`）；对比 `internal/platform/wsl.go:258-264`（转发白名单含 ANTHROPIC_/OPENAI_/CLAUDE_/CODEX_/PI_）与 `wsl.go:317+`（extraForwardKeys 仅排除 WSLENV/PATH/PATHEXT，见 :296-311 `isWSLENVReservedKey`）；历史事故原文 `wsl_agent_config.go:144-146` 注释。
- 分类：B。

**B4. ext4/Linux 形态工作目录直传 CreateProcess lpCurrentDirectory**
- 现象：PTY 层对所有会话（含 BootstrapWSL）无条件把 `spec.WorkDir` 传给 `conpty.ConPtyWorkDir`；vendored conpty 将其作为 `windows.CreateProcess` 的 lpCurrentDirectory。文档却推荐「工作目录填 Linux 路径」（如 `~/projects/<repo>`、`/home/u/...`）。`workDirMapsToDrvFS` 只对盘符路径告警，Linux 形态无任何处理。
- 影响：按文档填 ext4 路径的会话可能在 ConPTY 启动阶段失败（CreateProcess 要求已存在的 Windows 目录）；若失败，报错为底层 conpty 错误，用户无从关联到路径形态。
- 证据：`internal/pty/service.go:321-330`：`workDir := spec.WorkDir ... opts = append(opts, conpty.ConPtyWorkDir(workDir))`（无 BootstrapWSL 分支）；`vendor/github.com/UserExistsError/conpty/conpty.go:157-168`：`currentDirectory, err = windows.UTF16PtrFromString(workDir)` 直传 CreateProcess（:173-181 调用点）；`docs/user/usage.md:70`「把仓库 clone 进 WSL 内（如 ~/projects/<repo>），工作目录填 Linux 路径」。
- 分类：B（代码链完整；CreateProcess 对该输入的实际行为未实证，见疑点 1）。

**B5. distro 不可选且探测缓存进程级固化**
- 现象：`DefaultWSLDistro` 固定返回探测列表第一个非保留 distro；PTY 层与整条会话链路均无 distro 参数，多 distro 用户无法选择会话落点。且 `availableWSLDistros` 首探成功后置 `wslDistroCacheDone=true`，进程内永不刷新——装新 distro 后须重启应用才能被选中。wslsetup 状态面 `Status.Distro` 为单值，前端 WSLCLISettings 面板只展示这一个 distro。
- 影响：多 distro 用户（如 Ubuntu + Debian + docker 之外的工具 distro）无法控制 CLI 装在/跑在哪个 distro；新装 distro 不重启应用不生效。
- 证据：`internal/platform/wsl.go:193-197`（`DefaultWSLDistro` 返回 `distros[0]`）；`wsl.go:44-52,60-69`（`wslDistroCacheDone` 一次性缓存，仅 `resetWSLDistroCacheForTest` 可清）；`internal/pty/service.go:905`（`distro := platform.DefaultWSLDistro(spec.Env.Variables)`）；`internal/wslsetup/service.go:44-53`（`Status` 结构仅 `Distro string`）；`frontend/src/views/settings/WSLCLISettings.vue:13-16`（单 distro 徽标）。
- 分类：B。

**B6. WSL 判定与 distro 解析不一致时的退化写盘**
- 现象：`EmbeddedLaunchTargetsWSL` 命中但 `DefaultWSLDistro` 返回空（两次探测间的状态变化/缓存分叉）时，配置仍写 Windows 侧，仅日志 WARN「WSL shell 会话但无可用发行版，配置仍写 Windows 侧」；会话若实际跑在 distro 内，将读不到配置（即 CHANGELOG 1.3.59 修复过的同类 401 场景）。
- 影响：边缘时序下的配置不可达，用户只看日志无从察觉。
- 证据：`app.go:3521-3526`（pi）：`wslDistro = platform.DefaultWSLDistro(nil); if wslDistro == "" { a.Log.Warn("pi", "WSL shell 会话但无可用发行版，配置仍写 Windows 侧", "") }`；对称分支 `app.go:3869-3874`（omp）。architecture.md:204 述退化路径回退内置 provider env 兜底。
- 分类：B（防御分支本身记录在案；触发概率低）。

**B7. omp 被混合架构预警引导安装，但 wslsetup 不支持安装 omp**
- 现象：环境检测混合架构预警覆盖 omp（`command -v omp` 命中 /mnt/* 时提示「建议在「环境检测」页的「WSL 内的 CLI」卡片把 Oh My Pi 安装进 WSL 原生运行」），但该卡片的安装面 `cliPackages` 只有 claude/opencode/codex/pi 四键，无 omp——建议不可兑现。
- 影响：omp 用户收到一个无法执行的修复指引；omp 在 WSL 内只能用户自装。
- 证据：`internal/envcheck/wsl_hybrid.go:34-43`（`wslSessionCommands` 含 `{ToolOmp, "omp"}`）、:86-88（Detail 文案引用「WSL 内的 CLI」卡片）；`internal/wslsetup/service.go:15-22`：`cliPackages = map[string]string{ "claude":…, "opencode":…, "codex":…, "pi":… }`（无 omp 键）；CHANGELOG 1.3.48（CHANGELOG.md:477）「WSL CLI 安装支持 Pi」亦未含 omp。
- 分类：B（指引与能力不匹配；omp 是否计划进入安装面属产品决策，未查到声明）。

### C 类：有护栏提示但仍需用户手动规避

**C1. DrvFS 工作目录提示仅日志 WARN**
- 现象：WSL 会话 `--cd` 至盘符路径时记一条 WARN 日志，代码注释自述「No session-start advisory UI mechanism exists」；不阻止启动。
- 影响：提示默认无人看见（需主动开 /logs 页）；用户须读文档或日志后手动搬仓库/切 shell。
- 证据：`internal/pty/service.go:923-931`（`workDirMapsToDrvFS(spec.WorkDir) && log != nil → log.Warn("pty", "DrvFS 工作区 I/O 显著慢于 ext4", ...)`；注释「Advisory log only — the session still starts. (No session-start advisory UI mechanism exists...)」）；`docs/user/usage.md:74`「这只是建议，不会阻止会话启动」。
- 分类：C。

**C2. 混合架构预警只在环境检测页**
- 现象：`bash -lc 'command -v <cli>'` 命中 `/mnt/*` 时追加结构化 warning issue，但仅在 envcheck 收尾时探测（30s TTL 缓存、severity-only 不影响 AllOK）；启动路径不执行该检测。
- 影响：用户须主动访问环境检测页才能看到；装了 Windows 侧 CLI 的机器默认处于混合状态而不知情。
- 证据：`internal/envcheck/wsl_hybrid.go:20-29`（超时/TTL 常量）、:68-98（`appendWSLHybridArchWarning`，warning 注释「it must not flip OverallStatus.AllOK」）、:100-133（TTL 探测）；CHANGELOG 1.3.58（CHANGELOG.md:391）。
- 分类：C。

**C3. WSL 内 fd/ripgrep 缺失仅日志 WARN + 手动一键安装**
- 现象：启动 pi/omp WSL 会话时进程级一次探测，缺失打 WARN 附 `sudo apt install fd-find ripgrep` 引导；代码注释自述「无会话启动期 warning UI 通道，日志 WARN 为本仓库既有可感知手段」。环境检测页有一键安装但需 root 确认弹窗。
- 影响：文件搜索能力静默降级，须用户看日志或去环境检测页处理。
- 证据：`app.go:4950-4974`（`warnWSLSearchToolsOnce`；:4953 注释「无会话启动期 warning UI 通道…」；:4971-4972 WARN 原文）；`frontend/src/views/settings/EnvCheckSettings.vue:1032,1070-1076`（`install_wsl_search_tools` 方案与 root 确认文案「此操作将在 WSL 内以 root 安装 fd-find 与 ripgrep」）。
- 分类：C。

**C4. npm prefix 脏快照修复依赖用户手动触发**
- 现象：历史上弱守卫导致 .bashrc/.profile 只含脏 PATH 快照、登录 shell 解析到 /mnt/c shim；现精确整行守卫（`grep -qF`）+ AlreadyOK 时自动重跑前缀修复 + 修复后复检。但整套修复只在用户点击「安装/重新安装」时运行，已装会话不会自愈，修复后需重开会话。
- 影响：旧装机残留的脏快照需用户主动重装才清除。
- 证据：`internal/wslsetup/service_windows.go:191-208`（精确整行守卫注释「A looser substring guard … is defeated by dirty snapshots」）；:271-296（AlreadyOK 生效性校验 + 自动修复「repaired login PATH (appended missing npm-global line)」）；:317-348（安装后最终生效性校验）；CHANGELOG 1.3.58（CHANGELOG.md:391）。
- 分类：C（护栏已闭环，残余是触发时机）。

**C5. 报错文案与操作摩擦**
- 现象：Pi 登录 WSL 拒绝报错为英文（`Pi host login credentials are not available in WSL`），与本仓库中文为主的用户报错风格不一致；AI 代理冲突要求用户「停用分流后启动 WSL 会话」，即手动去另一页面关开关再回来重启会话。
- 证据：`app.go:3728`；`internal/aiproxy/environment.go:86`。
- 分类：C。

## 3. 历史已修复清单（避免重复报）

| 版本 | 内容 | 锚点 |
|------|------|------|
| 1.3.46 | WSL 成为 Windows 默认一等 shell；wslsetup 安装辅助；WSL1/2 徽标 | CHANGELOG.md:486-493 |
| 1.3.47 | WSL 内 Node 底线升 22（pi undici 依赖）；npm 前缀 PATH 补写 .profile（防 /mnt/c shim 泄漏） | CHANGELOG.md:482-484 |
| 1.3.48 | Pi 可装入 WSL + 首装自动播种 ~/.pi/agent | CHANGELOG.md:477-478 |
| 1.3.58 | WSL 会话体验护栏：精确整行守卫+生效性校验；混合架构预警；DrvFS 提示；文档两节 | CHANGELOG.md:391-392 |
| 1.3.59 | WSL 终端模式 pi/omp 配置失效修复（配置写 WSL 侧 + 剥离 PI_* 路径值 + fd/rg 探测） | CHANGELOG.md:383-384 |
| 1.3.60 | WSL 搜索工具（fd/ripgrep）检测与一键安装 | CHANGELOG.md:373 |
| 1.3.90 | envvars.json 自定义环境变量经 WSLENV 转发修复（EnvForwardKeys） | CHANGELOG.md:142 |
| 1.3.93 | Docker Desktop ↔ WSL 集成健康检查与自愈（非会话链路，相邻参考） | CHANGELOG.md:131 |
| 1.3.94 / 1.3.100 | Pi OAuth 登录（含 WSL 拒绝）；AI 代理分流（含 WSL 不支持声明） | CHANGELOG.md:113,54 |

已闭环且本报告不再列为摩擦的：配置双文件系统不匹配（1.3.59）、PI_* 路径值泄漏（1.3.59）、自定义 envvars 跨界（1.3.90）、脏 PATH 快照守卫（1.3.58）、Node 版本底线（1.3.47）。

## 4. 不确定 / 需主上确认的疑点

1. **B4 的实际行为**：CreateProcess 收到 `/home/u/...` 或 `~/...` 作为 lpCurrentDirectory 时是报 ERROR_DIRECTORY 还是意外容忍，无法从代码判定（本角色无执行环境，未做真机启动实证）。建议以「填 `~/projects` 启动 WSL 会话」一条真机冒烟定谳。
2. **B1/B2 的可达性前提**：宿主回环对 WSL 会话不可达在 NAT 模式下成立；WSL2 mirrored networking（`.wslconfig` `networkingMode=mirrored`）下 localhost 双向可达，摩擦消失。用户实际网络模式代码层不可知；aiproxy 拒绝文案表明产品按 NAT 假设设计。判定「Headroom 是否真的在 WSL 会话挂起」需按目标用户群的网络模式分布评估。
3. **B7 的产品意图**：omp 是否计划纳入 wslsetup 安装面（cliPackages 无 omp 键）无任何声明性文件；本报告仅记录「指引与能力不匹配」的事实。
4. **前端无 piLogin×WSL 组合预检**：grep frontend/src 无「登录认证 + WSL」组合提示；用户在会话设置页选 Pi 登录提供商 + 默认 WSL shell 时，要到点击启动才收到 app.go:3728 的英文报错（remote/v1 路径更弱：pi_login_launch.go:147-149 静默 fail）。是否属缺口由主上裁决。
5. **A5 回退的感知面**：回退 warning 只进 `ResolvedLaunchSpec.Diagnostics.Warnings`，是否在任何前端视图展示未逐一核实（本次仅确认会话设置页无 WSL 分支代码）。

## 覆盖范围说明

- 已精读：docs/user/terminal.md、usage.md（相关节）、internal/platform/{wsl.go,wsl_home.go 关键函数,resolver.go WSL 分支,shell_catalog.go,capabilities_runtime.go}、internal/pty/service.go WSL 相关段、internal/launcher/{wsl_agent_config.go,pi_login_launch.go（根包）,service.go headroom 注入段}、internal/envcheck/wsl_hybrid.go、internal/wslsetup/{service.go,service_windows.go 关键段}、internal/headroom/service.go 回环常量、aiproxy/environment.go、app.go WSL 相关方法、frontend WSL 相关 5 个文件、CHANGELOG WSL 全部条目。
- 负面发现（搜索过、无匹配）：`internal/launcher/service.go`、`provider_sync.go`、`process_identity_*.go`、`internal/session/**`、`internal/agentprofile/**`、`internal/launchplan/**` 均无 WSL 字样——这些包当前不参与 WSL 分支；任务书将 provider_sync.go/process_identity_*.go 列为候选探查对象，实读结论为「与 WSL 无关」。
- vendored conpty 仅核读 CreateProcess 相关段。
