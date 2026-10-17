# 运行链证据审计（16f707d^..58e7577 · HEAD 只读）

- 审计者：baize（白泽）。方法：仓库定向读码（read/grep/find），零运行时执行（无 shell 通道，未运行任何命令/测试；不伪称运行）。
- 上游必读已读全：`wsl-goal-closure/workflow.md`、`wsl-session-friction/workflow.md`、`p4-evidence.md`；另读 `direction.md`（fuxi，引用不复述）、`verification.md`、`baseline-go.log`（Leader 基线，2026-10-16 darwin/arm64 Go1.27.2）。
- 配置供给（internal/wslsupply）内部深审由 diting 承担（supply-audit.md，16 Major/2 Minor）；本文只覆盖它与启动链的接点。
- 行号均为当前 HEAD 实读行号。三态：观察（代码直证）/推断（前提写明）/未覆盖。

## 结论索引

| # | 主张 | 三态 | 严重度 | 锚点 | 适用边界 |
|---|------|------|--------|------|---------|
| D1 | Headroom×WSL×NAT 无启动闸门：ANTHROPIC_BASE_URL=localhost:8787 原样跨界，distro 内不可达，静默失败（一轮 B1 至今未闭合；P2-T1 只修了 aiproxy 面） | 观察 | 高 | app.go:2503-2519；launcher/service.go:121-122；aiproxy/environment.go:78-97；envcheck/wsl_network.go:50-56 | NAT/未知模式；mirrored 不受影响；仅 Claude 会话级 headroom（8787） |
| D2 | remote/restart 计划链缺 EnvForwardKeys/EnvPathFlagKeys/路径值剥离：桌面防线三条在 planner 侧全部缺席，自定义键丢失 + 盘符路径值可未剥跨界 | 观察 | 高 | app.go:2373-2401 vs launch_planner.go:259/369/485/744/pi_login_launch.go:343；app_ai_proxy.go:87-102 | 仅 BootstrapWSL 会话；desktop 链不受影响 |
| D3 | Linux 形态 workDir（~/、/home、/mnt）被宿主 Abs+Stat 校验链淘汰→静默回退默认目录：显式 ext4 cwd 被换目标，且与 DrvFS advisory「建议放 ext4」文案互斥 | 观察 | 高 | launch_workdir.go:66-76,126-141；launch_planner.go:211-217；SessionSettingsView.vue:133-141；pty/service.go:1008-1016 | Windows 宿主（macOS 上 / 即本机原生不受影响）；五桌面入口+remote/restart 共用 |
| D4 | AMAGI_CODEBOX_AI_PROXY 标记信任继承 env：ApplyEnv 停用路径返回 base 原样，嵌套 CodeBox 场景 marker 残留→PinProxyEnvironment 误判激活→WSL 会话被 NAT 文案拒绝 | 观察（机制）/已定谳（测试面） | 中 | environment.go:60-62,79-87；baseline-go.log:417-458,596-598；Leader 定谳：去 marker 后四组 root WSL 测试 PASS | CodeBox 从 CodeBox 代理会话内启动的嵌套场景；亦是 root 四组基线失败的根因 |
| D5 | pi/omp WSL 侧配置写入失败回退内置 provider（丢 baseURL→第三方 key 误打官方 endpoint）：与已批准的「WSL 构建失败阻断」语义不一致 | 观察 | 中-高 | app.go:5032-5061（pi）；app.go:3988-4004（omp）；piProviderMapping 注释 app.go:5115-5126 | UNC 写失败（distro 停机/9P 异常）时触发；构建失败已阻断（不受影响） |
| D6 | codex 全局 headroom（8788）对 WSL codex 会话静默无效：路由靠 Windows 侧 config.toml 标记块，distro 侧无同步、无提示 | 观察+推断 | 中 | app.go:4455-4459（CodexHomeDir）；syncCodexGlobalHeadroomConfig app.go:4459-4509；launch_planner.go:435-451 | WSL codex 会话；「实际流量不经 8788」未真机验证 |
| O7 | restart 不携带会话原 StableRefs，身份跟随宿主最近桌面激活漂移：远端会话重启可能换 provider/model（含 pi 登录→API 漂移） | 观察 | 中 | launchplan/defaults.go:24-26；remote_session_adapter.go:971-975；control_wiring.go:204-206,264-266 | 仅 remote restart；desktop restart 未读（未覆盖）；可能是有意设计，需裁决 |
| F1 | P1-P4/S0-S5 已修复面确认：B4(267)/T12/B3 泛化/T4 OpenCode 层/A2 登录同步/P4-A1 前置拦截+advisory/P4-A2 /p 放行门/mirrored aiproxy 闸门/ALL_PROXY 精确键/UTF-8 locale | 观察 | — | 见 §3 | — |
| F2 | 基线失败分类：根包 4 组＝D4；SetPathAllow/WSLFileMTime＝平台假设测试缺陷（任何 darwin 必红，CI 为手动触发未发现）；codex 解析/envcheck 8 项＝宿主安装干扰 | 观察 | — | §5 | — |

## 1. 链路总览（追踪对象）

五入口 `LaunchSession`(app.go:2406)/`LaunchCodexSession`(:3118)/`LaunchPiSession`(:3488)/`LaunchOmpSession`(:3889)/`LaunchOpenCode`(:5280) → `resolveLaunchWorkDir`（单一 choke point，launch_workdir.go）→ 各自 env 构建（`EnvVars.MergeWithSystem`+overrides）→ 桌面统一漏斗 `resolveEmbeddedLaunchSpec`(app.go:2349-2401：aiproxy.ApplyEnv→EnvForwardKeys/PathAllowKeys 注入→StripWSLHostPathEnvWithAllow→resolver→PinProxyEnvironment)；remote/restart 走 `appLaunchPlanner.BuildPlan`(launch_planner.go:101-234)→`resolveLaunch`(app_ai_proxy.go:87-102：仅 networkEnv→resolver→PinProxyEnvironment) → `launchplan.Executor`(launch_executor.go) Apply（WSLConfigSupply→PTYStart→Bootstrap）→ pty `StartResolvedWithRunEvidence`(service.go:297) → `buildWSLCommandLine`(service.go:918-976：`wsl.exe -d <distro> --cd <workDir> -- bash -lic '<payload>'`)。启动链与供给链的接点：pi 登录 A2 + 托管面（app.go:3624,3726↔launch_executor.go:844-870 同一 verdictForWSLLoginSupply）、OpenCode 层（app.go:5461↔launch_executor.go:794-816 同一 prepareWSLOpenCodeLayer）、pi API-provider（app.go:3624 runWSLManagedModelsAtLaunch↔executor 同语义）。双链一致性有测试钉住（TestWSLLoginSupplyFailureMatrixBothLinks 等）——接点设计成立，缺陷在各链自身的缺口（D2/D3/D5）。

## 2. 缺陷明细（复现条件/调用链/最小修复/测试漏检）

### D1 Headroom×WSL×NAT 无启动闸门（高）
- 调用链：LaunchSession useHeadroom=true → `a.Headroom.Start(realBackend)`+`SetHeadroomPort(8787)`(app.go:2503-2519) → `BuildOverrides` 注入 `ANTHROPIC_BASE_URL=http://localhost:8787`(launcher/service.go:121-122) → resolver WSL 分支把 ANTHROPIC_ 前缀变量拼进 WSLENV(platform/wsl.go:385-395) → `PinProxyEnvironment`(environment.go:78-97) 仅在 `AMAGI_CODEBOX_AI_PROXY=1` 存在时判 WSL 网络模式——Headroom-only 场景无 marker，直接放行。
- 复现：Windows + 默认 NAT（无 .wslconfig networkingMode）+ Claude Code 勾选 Headroom + WSL shell → 会话正常启动，模型请求在 distro 内连 localhost:8787（指向发行版自身）挂起/失败，无任何启动期提示。唯一感知面是 envcheck info 卡片文案（wsl_network.go:53「AI 代理分流与 Headroom 对 WSL 会话不可用」——声明了不可用，但启动不拦）。前端 AIProxyView.vue:99「启动时将提示切换」仅对 aiproxy 为真。
- 对照：aiproxy 同场景显式拒绝并给 mirrored 引导（environment.go:92-93）；workflow V2 判据「NAT 维持拒绝但文案引导」未覆盖 Headroom。
- 测试漏检：aiproxy/environment_test.go 只测代理闸门；无 headroom+WSL 用例（grep 全仓无）。
- 最小修复：在 useHeadroom 分支（或 resolveEmbeddedLaunchSpec）对 `spec.BootstrapMode==WSL && platform.WSLNetworkingMode(...)!="mirrored"` 复用 aiproxy 同款中文拒绝（或明确的产品级降级决策），补一条 planner executor headroom effect 前置判定与回归测试。

### D2 remote/restart 计划链 env 防线不对称（高）
- 证据：桌面漏斗注入 `req.EnvForwardKeys=a.EnvVars.Keys()`、`req.EnvPathFlagKeys=a.EnvVars.PathAllowKeys()` 并在拼 WSLENV 前执行 `StripWSLHostPathEnvWithAllow`（app.go:2373-2401）。planner 五个 builder 构造的 `ResolveRequest` 均不含这两字段（launch_planner.go:259-264/369-373/485-491/744-749、pi_login_launch.go:343-348），`resolveLaunch`(app_ai_proxy.go:87-102) 也无对应注入/剥离；唯一例外是 pi 登录计划对 `PI_CODING_AGENT_DIR` 的专用兜底剥离（pi_login_launch.go:270-296，注释自认「planner 链路无该前置防线，这里统一兑底」）。
- 后果（BootstrapWSL 会话）：(a) envvars.json 自定义键（TAVILY_API_KEY 等，无白名单前缀）不进 WSLENV 清单→remote/restart WSL 会话丢失（桌面自 1.3.90 起有，CHANGELOG:227——parity 回归）；(b) allowPathValue 键无 /p 翻译；(c) 白名单前缀键携带盘符值（如用户自定义 `CLAUDE_PROJECT_DIR=D:\p`）不经剥离原样跨界→B3/T6 垃圾目录事故形态在 remote/restart 复活。
- 复现：remote v1 CreateSession（或 restart）落 WSL + envvars.json 含上述键。
- 测试漏检：wsl_s2_remote_test.go 只断言供给 effect 结构，无 WSLENV 组成/剥离断言；`launchEnvVarsPort`(launch_planner.go:57-59) 仅暴露 MergeWithSystem。
- 最小修复：`launchEnvVarsPort` 增加 `Keys()/PathAllowKeys()`；在 `resolveLaunch` 漏斗（或各 builder 构造处）统一填两字段并前置 `StripWSLHostPathEnvWithAllow`（复用 platform/launcher 现有函数，与桌面同一语义）；补桌面/remote 双链 WSLENV 一致性测试。

### D3 Linux 形态 workDir 被宿主校验链吞掉（高）
- 调用链：前端工作目录为自由文本输入（SessionSettingsView.vue:133-141「选择或输入工作目录」）/remote `CreateSessionRequest.Workdir` 任意串 → `resolveLaunchWorkDirChain`(launch_workdir.go:66-104)：`filepath.Clean+Abs` 在 Windows 把 `/home/u/repo` 变 `C:\home\u\repo`，`os.Stat` 失败→候选淘汰→回退 defaultPath（Windows 形态）→ WSL 会话 `--cd` 落 Windows 目录（DrvFS）。remote 侧同链且 `onFallback=nil`（launch_planner.go:211-217，连 Warn 都没有）。
- 矛盾点：DrvFS advisory 文案「建议将工作区放在 WSL ext4（如 ~/）内」（pty/service.go:951-959、drvfsHint.ts:29）推荐的正是入口拒绝的形态；B4 修复已让 PTY 层就绪（conPtyWorkDirFor 对 Linux 形态跳过 lpCurrentDirectory，service.go:1008-1016；wsl_workdir_test.go 钉定），但入口从不把该形态送达。
- 复现：Windows + WSL shell + workDir 填 `~/projects` 或 `/mnt/d/repo` → 会话落在默认目录（桌面有 Warn 日志，remote 无感知）。违反 V1「不悄悄降级错目标」。
- 测试漏检：launch_workdir_test.go 无 Linux 形态用例；pty 层测试不可达（入口先吞）。
- 最小修复：`resolveLaunchWorkDirChain` 增加目标感知分支——会话将落 WSL（EmbeddedLaunchTargetsWSL）且 `platform.IsLinuxStylePath(requested)` 时跳过宿主 Stat（信任 wsl.exe --cd 或改由 PTY 层失败可见），不可判目标时维持现状；同步修 remote 链 onFallback=nil 的静默。与 direction.md P0「启动身份与目录一致」一致。

### D4 继承 marker 误判 AI 代理激活（中；测试面已定谳）
- 机制：`ApplyEnv` 停用路径 `return base, nil` 原样返回（environment.go:60-62）——继承自进程环境的 `AMAGI_CODEBOX_AI_PROXY=1` 存活；`PinProxyEnvironment` 以「spec.Env 含 marker」为激活判据（environment.go:79-87）→ 嵌套场景（CodeBox 从被 CodeBox 代理的 CLI 会话内启动）中，用户已停用代理，WSL 会话仍被 NAT 文案拒绝，文案指向「停用 AI 代理分流」——按文案操作无效。
- 测试面定谳（Leader steer #3，baseline-root-no-marker.log）：根包四组失败（TestPiLoginPlannerWSLGateLifted / TestRemotePiAPIProviderWSLPlanCarriesSupply / TestRemoteOpenCodeWSLPlanCarriesLayer / TestPiLoginDesktopWSLGateSync×6）＝测试进程 env 携带该 marker + darwin 无 .wslconfig→NAT→PinProxyEnvironment 拒绝。我读码独立推导同因：planner 链中唯一产出该 NAT 文案的代码就是 PinProxyEnvironment（app_ai_proxy.go:100），且 planner 测试的 env 只能来自 `MergeWithSystem()`（进程 env）。
- 最小修复（产品）：ApplyEnv 停用路径过滤继承的 marker 键（enabled 路径重建 out 时同样先剔除旧 marker 再追加新值）；（测试）：相关 fixture scrub `AMAGI_CODEBOX_AI_PROXY`（如 t.Setenv 置空）+ WSL stub 测试钉 USERPROFILE 至受控 .wslconfig（对齐 aiproxy service_test.go 的先例）。C1 已接手测试隔离。

### D5 pi/omp WSL 写入失败回退内置 provider（中-高）
- pi：`writePiProviderModelsAtLaunch` 中 WSL 分支 `WriteWSLPiAgentConfig` 失败→Warn+`piProviderMapping` 回退（app.go:5032-5043→5050-5052）；仅「构建失败」在 WSL 路径阻断（app.go:5055-5061，P3-S1 已批准语义）。omp：构建与写入失败均回退（app.go:3988-4004）。回退映射丢 baseURL（piProviderMapping 注释自证「会丢自定义 baseURL…误打官方 endpoint」，app.go:5115-5126）→ distro 内 pi/omp 以 amagi-* 之外的身份+裸 API key 打官方端点（401/错误计费面）。
- 不一致对照：remote executor 对 pi 登录/托管面沿用同 verdict，但 API-provider 供给失败按可选面告警放行（launch_executor.go:844-850）——三处语义（桌面构建阻断/桌面写失败回退/remote 告警放行）互不相同。
- 测试漏检：App 层 WSL 写失败回退分支无回归钉住（launcher 包只有 WriteWSL* 成功路径单测）。
- 最小修复：WSL 路径写入失败与构建失败同判阻断（wslPiProviderBuildError 同款文案），omp 对齐；remote 侧明确「amagi-* 期望集未落地」至少 WARN 可见（direction.md P0「收紧非等价 fallback」同向）。

### D6 codex 全局 headroom 对 WSL codex 静默无效（中，含推断）
- 观察：codex 全局 headroom 路由载体是 Windows 侧 `CodexHomeDir/os.Environ()` 的 config.toml 标记块（app.go:4455-4509）；WSL codex 读 distro 侧 ~/.codex/config.toml，无任何同步/提示面（wslsupply 的 UnitCodexConfig 是 opt-in 的 mcp/config 导入，不含 headroom 标记，types.go:90-92）。remote codex UseHeadroom 只加 HeadroomStart effect，无路由注入（launch_planner.go:435-451）。
- 推断（未真机验证）：开启全局开关后，WSL codex 流量不经 8788，用户以为在压缩，实际直连——静默 non-effect。
- 最小修复：开关状态对 WSL 会话至少 advisory/启动日志提示；或 per-session 经 `buildCodexCLIArgs` 的 `-c` 通路注入（codex 会话已有 per-process 配置覆盖先例，app.go:4972-5003）。

### O7 restart 身份漂移（观察，待裁决）
- `RecordDesktopActivation` 仅桌面调用（launchplan/defaults.go:24-26 注释明示「Remote and restart paths do not call this method」；control_wiring.go:204-206,264-266）；remote restart BuildPlan 不带 StableRefs（remote_session_adapter.go:971-975）→ 用 HostDefaultRefs＝该 CLI 最近一次桌面激活。远端创建的会话（显式 provider/preset/model）在期间桌面又启动同 CLI 后 restart → 换 provider/model；pi 登录会话可能重启成 API 会话或反之。snapshot 里有 Provider/Preset/Model 字段但未传入。V1「provider/登录身份贯穿一致」的 restart 面未闭合；也可能是有意「restart=按当前默认重开」，需产品裁决（裁决前不改代码）。

## 3. 已修复确认（范围内，历史报告→现状）

| 历史项 | 现状锚点 | 回归测试 |
|---|---|---|
| B4 CreateProcess err=267 | service.go:336-345,1008-1016（Linux 形态不传 ConPtyWorkDir） | wsl_workdir_test.go:41-67 |
| T12 DrvFS 告警 /mnt 双形态 | service.go:1018-1035→platform.WorkDirMapsToDrvFS（wsl_form_windows.go:49-62） | wsl_drvfs_test.go:97-132 |
| B3 泛化路径值剥离 | wsl_agent_config.go:159-211（StripWSLHostPathEnvWithAllow） | wsl_agent_config_test.go:194-255 |
| T4 OpenCode WSL provider 层 | wsl_agent_config.go:213-300；桌面 app.go:5446-5468 fail-closed；remote launch_executor.go:794-816 同函数+OPENCODE_CONFIG 重锄 | wsl_s2_remote_test.go |
| A2 pi 登录 WSL | wsl_pi_login_sync.go（双向新陈+fail-closed 分类 m-2/m-3）；桌面/remote 同 verdict（pi_login_launch.go:170-236） | TestWSLLoginSupplyFailureMatrixBothLinks |
| P4-A1 GitAssist/PathPicker 前置拦截 | gitassist/service.go:53-80,108-140；paths/list_directories.go:31-84；advisory 三入口 drvfs_advisory.go；前端 GitPanel.vue:24-28/PathPickerDialog.vue:47-54/drvfsHint.ts | gitassist/paths 包测试 ok（基线） |
| P4-A2 envvars allowPathValue /p 门 | envvars/service.go:16-26,209-255；wsl.go:481-587；resolver.go:76-90,152 | wsl_path_allow_test.go, wsl_test.go:609+ |
| P2-T1 mirrored aiproxy 闸门+envcheck 卡片 | environment.go:78-97；wsl_network.go（探测 m-4 已改直读不缓存，wsl.go:230-250） | environment_test.go |
| ALL_PROXY 精确键（m-5）/OPENCODE_CONFIG | wsl.go:366-382 | wsl_test.go |
| T13 UTF-8 locale | service.go:1037-1058（含 WSLENV 追加 LANG） | pty 包测试 ok |

环境固有（代码不修）：DrvFS 9P 性能惩罚（E1，§8 历史定量）；CodeBox 不整体转发 PATH（IsWSLENVReservedKey 产品防护，wsl.go:447-456；不是 WSLENV 机制本身不能表示路径）；wsl.exe -d 不接受引号名（已裸传，service.go:940-945）；CreateProcess 拒 Linux 形态 cwd（已绕开）。

## 4. 基线失败分类（baseline-go.log；steer #1 落实）

| 测试 | 分类 | 依据 | 定位/修复归属 |
|---|---|---|---|
| 根包 4 组（PlannerWSLGateLifted/RemotePi/OpenCode/DesktopGateSync×6） | 测试隔离缺陷 + D4 产品面 | 错误文案唯一来源＝PinProxyEnvironment NAT 拒绝；planner 测试 env 仅来自进程 env；Leader 去除 AMAGI_CODEBOX_AI_PROXY+proxy 变量后全 PASS（baseline-root-no-marker.log） | D4 修复 + C1 测试隔离（已启动） |
| TestSetPathAllow | 测试平台假设缺陷（任何 darwin 必红） | normalizeKey 仅 Windows 大小写不敏感（envvars/service.go:888-893）；测试用小写 "vsinstalldir" 断言跨平台不敏感（wsl_path_allow_test.go:94） | 测试改 GOOS 分支断言或用原大小写；勿动 normalizeKey（Windows env 语义正确，macOS env 大小写敏感是事实） |
| TestWSLFileMTime | 同上（darwin 必红） | 产品 runtime.GOOS 守卫（wsl.go:246-248）使注入 runner 不可达；测试无 skip（wsl_test.go:577-611） | 测试加 `if runtime.GOOS != "windows" { t.Skip }` |
| TestDarwinResolveExecutableFindsCodexInLocalNodeBin | 宿主安装干扰（测试隔离） | 期望临时 HOME 的 codex，实际解析到宿主真实 `/Users/run.mao/.local/bin/codex`（resolver_test.go:304）——ambient/用户路径回退把宿主安装卷入 | 测试 stub 掉 ambient 回退或收窄断言；与 WSL 运行链无产品关系 |
| envcheck 8 项（native bootstrap/npm detect 等） | 宿主 npm/claude 安装干扰 | 日志「self-heal removed…could not resolve npm global node_modules root」；期望临时 shim 拿到宿主真实安装 | 范围外（envcheck 安装面），风险单列供 Leader |
| wslsupply 失败 | 供给域 | — | diting supply-audit（另节点） |
| CI 未发现上述确定性失败 | 流程事实 | ci.yml:4-7 仅 workflow_dispatch（C-011 手动触发）；macos-latest 全量 `go test ./...`（ci.yml:46-48） | 手动触发 CI 前必修 SetPathAllow/WSLFileMTime 两项 |

## 5. 升级方向评估：是否偏离 Windows 工程主场

- 主线未偏离：P1-P4 与 S0-S5 的落点均服务「WSL 壳 × Windows 主场」——X:\ 形态全链可用（入口校验/resolver/--cd 映射/GitAssist 直消费 NTFS/PathPicker Windows 树）；形态感知把 Linux 形态从静默错位改为显式失败；配置/凭据落 distro 侧而不动 Windows 工程。方向与主上目标一致。
- 方向内的收尾缺口（非偏离，是「主场服务面不完整」）：①advisory 推荐 ext4 与入口拒绝 ~ 互斥（D3）——文案或入口二选一收敛；②宿主回环服务（Headroom 8787/8788）对 WSL 的可用性判定不完整（D1 无闸门、D6 静默无效），mirrored-only 的可达边界只在 aiproxy 一处被强制；③remote/restart 与桌面 env 防线不对称（D2）——同一用户选择跨入口行为不一致。
- 结构性风险提示（fuxi direction.md 同向，引证不展开）：D5 的三处语义不一致说明「严格门禁按失败对象生效」尚未贯穿；约 3.4 万行增量后，五入口双链（桌面/remote）×三防线（转发/剥离/放行）的矩阵维护面已明显变大，本轮修复宜按「同一漏斗收敛」而非逐点补丁。

## 6. 优先修复短清单（供 Phase B 排序，非裁决）

1. **D3** WSL cwd 吞没——V1 直接违背；显式目标不得静默替换（含 remote onFallback 静默）。
2. **D2** remote/restart env 防线对齐（漏斗化注入 Keys/PathAllowKeys/strip）——防泄漏回归 + parity。
3. **D1** Headroom×WSL×NAT 启动闸门（复用 WSLNetworkingMode，NAT 拒绝+文案，与 aiproxy 同源）。
4. **D4** ApplyEnv 停用路径剥继承 marker（产品）＋root 测试隔离 scrub（C1 已启动）。
5. **D5** pi/omp WSL 写失败阻断（非等价 fallback 收紧；omp 同步）。
6. **F2** TestSetPathAllow/TestWSLFileMTime 平台假设修复（手动 CI 触发前必修）。
7. **O7** restart 身份语义裁决（先裁决后动）；**D6** codex 全局 headroom WSL 提示（低优先）。

## 7. 覆盖与未覆盖

已读（全文）：launch_workdir.go、launch_planner.go、launch_executor.go、app_ai_proxy.go、internal/platform/{wsl,wsl_home,wsl_commands,wsl_form_windows,wsl_form_other,drvfs_advisory}.go、internal/aiproxy/environment.go、internal/envcheck/wsl_network.go、internal/launcher/{wsl_agent_config,wsl_pi_login_sync}.go、internal/launchplan/{types,defaults}.go、pi_login_launch.go、ci.yml（前 60 行）、internal/gitassist/service.go（1-150）、internal/paths/list_directories.go（1-120）。
已读（区间）：app.go（600-660/2320-2735/3118-3380/3488-3830/3940-4080/4440-4510/4972-5130/5280-5560）、internal/pty/service.go（282-480/865-1145/1285-1335）、internal/remote/remote_session_adapter.go（400-530/945-1045）、internal/launcher/service.go（100-160）、internal/envvars/service.go（180-260/516+/888-896）、frontend SessionSettingsView.vue（128-168/590-670）、测试（pi_login_wsl_test.go、wsl_s2_remote_test.go 1-170、launch_planner_executor_test.go 28-50/340-545、launch_workdir_test.go、wsl_test.go 577-611、resolver_test.go 280-325、wsl_path_allow_test.go 78-118）、baseline-go.log。
未覆盖（明示，不断言无缺陷）：internal/wslsupply 全包（diting）、internal/headroom 服务内部、internal/remote 其余 handler/gate、launcher 各 config builder 全文（pi_config/omp_config/opencode_config 只经调用面）、frontend 其余组件与 mobile、e2e、wslsetup 安装面、桌面 restart 入口（若有独立于 remote adapter 的路径）、UNC 形态（\\wsl.localhost\...）作 workDir 的行为、Windows 侧真实运行行为（无真机）。所有「观察」均为代码级事实；涉及运行时行为的（D1 失败形态、D6 流量路径、D3 回退后的实际落点）需真机/日志复核后定级不变或调整。

## 8. 待核验项

1. D1 在真实 NAT 宿主的失败形态（挂起 vs 报错文本）——影响文案与严重度微调。
2. D3 的 UNC workDir 形态（Stat 可过→Abs 保留→`wsl.exe --cd \\wsl.localhost\...` 是否有效）未测。
3. O7 desktop restart（若有）是否同 remote 语义。
4. D6 WSL codex 实际流量路径（未真机验证）。
5. D4 修复后 ApplyEnv enabled 路径对重复 marker 的收敛（重建 out 时先剔旧值）需补测试。
6. TestDarwinResolveExecutableFindsCodexInLocalNodeBin 的 ambient 回退注入点（resolveExecutableWithEnvForOS 的宿主路径面）——只定位到现象，未读到注入实现。
