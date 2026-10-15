# WSL 会话内工具调用摩擦点证据报告（二轮：启动之后的控制面）

- 任务：探查 CodeBox 控制面（播种、配置转换、PATH、环境跨界）对五 CLI 在 WSL 会话内执行工具调用的影响（只读探查，零修改）
- 前置：一轮报告 `evidence-report.md`（启动链路 17 条，编号 A1-C5）已通读，本轮不重复
- 方法：精读 internal/wslsetup 全部源文件、internal/launcher 配置链（wsl_agent_config/pi_config/omp_config/opencode_config）、internal/platform/{wsl.go,wsl_home.go,codex_home.go}、app.go 五个启动函数、internal/pty/service.go WSL 段、gitassist/paths/piplugin/piconfig、envcheck WSL 探测面；grep 交叉核验（mcp/seed/inotify/GBK/wsl.conf 等）。行号为当前工作区实读行号。
- 基线：CHANGELOG 至 1.3.105

## 结论索引（每行＝主张｜三态｜锚点｜对工具调用的具体影响）

| # | 主张 | 三态 | 锚点 | 对工具调用的影响 |
|---|------|------|------|------------------|
| T1 | WSL 播种 = Windows `~/.pi/agent` 整目录 `cp -r`，仅 pi 触发，内容零转换 | 观察 | wslsetup/service_windows.go:231-232,335 | 播种物是 Windows 形态的原样拷贝 |
| T2 | CodeBox 不生成/不转换任何 CLI 的 MCP server 配置；mcp.json 只随整目录播种被动拷入一次 | 观察 | internal 全域 grep（launcher 生产代码零 MCP 键处理；CHANGELOG.md:711） | WSL 内 MCP 配置＝冻结副本或用户手建 |
| T3 | 播种后永不重同步：CodeBox 对 WSL 侧只 merge models.json(pi)/models.yml(omp)，settings/amagi.json/mcp.json/插件实体冻结在播种时刻 | 观察 | wslsetup/service_windows.go:229（已存在即退出）；app.go:3605,3915；app.go:419,477,482 | 配置面板后续改动对 WSL 会话失效 |
| T4 | opencode WSL 会话拿不到任何 provider 配置：唯一载体 OPENCODE_CONFIG_CONTENT 不在 WSLENV 转发面 | 观察 | opencode_config.go:217,420；platform/wsl.go:258-274；app.go:2358 | WSL 内 opencode 无 amagi provider，模型路由全断 |
| T5 | claude/codex 的 amagi provider 经 env/-c 参数可跨界，但用户自有配置文件（.claude.json、~/.codex/config.toml 的 mcp_servers 等）不播种不转发 | 观察 | platform/wsl.go:258-261；app.go:4897-4925；wslsetup 包零 claude/codex 配置播种 | amagi 托管能力可用；用户自有 MCP/配置在 WSL 侧全缺 |
| T6 | CODEX_HOME 值为 Windows 路径时会经 CODEX_ 前缀原样跨界进 WSL（B3 的具体实例） | 观察 | platform/wsl.go:258-264（CODEX_ 在名单）；codex_home.go:13-16 | WSL 内 codex 可能解析到非法 C:\ 路径 |
| T7 | `~/.agents`（skills、amagi-media-models.json、amagi-modalities.json）不播种、无任何 WSL 同步机制 | 观察 | config/vision.go:64-69；config/modalities_store.go:42-51；internal 全域 grep | WSL 会话内依赖 ~/.agents 的技能/识图委托全部缺位 |
| T8 | 播种对 Windows 路径引用、.cmd/.ps1、盘符硬编码零适配 | 观察 | service_windows.go:227-233（纯 cp/rm 脚本） | amagi 资产内 Windows 专用形态在 WSL 侧原样落地 |
| T9 | 全仓对 interop appendWindowsPath=false / wsl.conf 零感知 | 观察（负面） | 全仓 grep 零匹配 | 无感知即无处理，PATH 断供场景无护栏 |
| T10 | WSL 内依赖探测仅覆盖 fd/rg、五 CLI 路径、node+4CLI 版本；git/python 等工具链零探测 | 观察 | wsl_home.go:140-168；envcheck/wsl_hybrid.go:33-43；service_windows.go:84-113 | agent 工具调用的外部依赖缺失无预警 |
| T11 | ext4/Linux 形态工作目录下 GitAssist、PathPicker 等 Windows 侧配套功能整体不可用且无前置提示 | 观察 | gitassist/service.go:84；paths/list_directories.go:46-54；TerminalView.vue:94→GitPanel.vue:316 | 会话在 ext4 时配套面板全部失效 |
| T12 | DrvFS 慢速告警只识别盘符形态；`/mnt/x/...` Linux 形态同慢但不告警 | 观察 | pty/service.go:923-931,965-970 | Linux 形态 DrvFS 目录静默慢速 |
| T13 | WSL 会话不注入 LANG/LC_ALL；对照 darwin 路径显式补 `LANG=en_US.UTF-8` | 观察 | pty/service.go:943-948 vs pty/service_darwin.go:410-415 | UTF-8 假定完全依赖 distro 自身 locale |
| T14 | PTY 层对 interop .exe 的 GBK/UTF-16 输出零感知；CRLF 规范仅在非 raw 展示路径 | 观察（负面） | internal/pty grep；ansi.go:18-28；dockerwsl/service_windows.go:42 | 编码问题全数透传给 xterm/用户 |

---

## Q1. MCP 配置跨界

### 1.1 播种清单的精确形态（T1）

`ensurePiConfig`（wslsetup/service_windows.go:218-234）的完整脚本是：

```
[ -d /mnt/c/Users/<u>/.pi/agent ] || { echo 'no windows .pi to seed'; exit 0; }
[ -e "$HOME/.pi/agent" ] && { echo 'wsl pi config already present'; exit 0; }
mkdir -p "$HOME/.pi"
cp -r /mnt/c/Users/<u>/.pi/agent "$HOME/.pi/agent"
rm -rf "$HOME/.pi/agent/sessions" "$HOME/.pi/agent"/settings.json.amagi-bak-*
echo 'seeded pi config from windows'
```

- 来源路径由 `wslPathFromWindowsHome` 映射为 `/mnt/c/...`（common.go:50-62），经 DrvFS 读取。
- 排除项仅两个：`sessions/`（会话历史）与 `settings.json.amagi-bak-*`（:232）。**其余一切文件原样进入**。
- 触发条件：仅 `InstallTool("pi")` 成功后（:335 `if key == "pi"`），且仅当 distro 无 `~/.pi/agent`（:229）。失败非致命（:337-338 仅写日志）。
- claude/codex/opencode/omp 无对称函数：`cliPackages` 只有 4 键（service.go:15-20，无 omp），wslsetup 包内 grep `codex|claude|opencode` 除安装探测外无配置播种代码。

### 1.2 mcp.json 在不在播种清单内？有没有转换？（T2）

- 播种是 `cp -r` 整目录——若 Windows 侧 `~/.pi/agent/mcp.json` 存在，则**被一并拷入**（amagi 的 MCP 路由读 `mcp.json/.agents/.mcp.json`，CHANGELOG.md:711），但内容零转换：无命令改写、无路径映射、无 env 过滤。
- CodeBox 生产代码不生成任何 MCP 配置：grep `internal` 全域，`mcp` 仅出现在 (a) 插件面板对 `.mcp.json` 子项的枚举/展示（plugin/metadata.go:150、opencodeplugin/service.go:506-510、codexplugin/manifest.go:523-543），(b) piconfig 对 amagi.json `mcp` 路由段的读写（piconfig/service.go:2,21）。`internal/launcher` 生产代码零 MCP 键处理（仅测试文件在验证 opencode preset 的 mcp 段被 deepMerge 保真透传）。
- WSL 会话内 MCP server 配置的真实来源：pi＝首次播种的冻结副本＋用户手动改 distro 内文件；omp/claude/codex/opencode＝**完全没有**，须用户在 distro 内手动创建。

### 1.3 各 CLI 的 WSL 配置写入面（T4/T5）

| CLI | amagi provider 载体 | WSL 模式下是否可达 | 用户自有配置（含 MCP）是否落地 |
|-----|--------------------|------------------|------------------------------|
| pi | models.json 合并写 distro（app.go:3604-3611 → launcher/wsl_agent_config.go:88） | ✅ UNC 直写 | ❌ 仅 models.json；settings/amagi.json/mcp.json/插件冻结 |
| omp | models.yml 合并写 distro（app.go:3912-3915 → wsl_agent_config.go:114） | ✅ | ❌ omp 连播种都没有（无 ensureOmpConfig） |
| claude | ANTHROPIC_* env 经 WSLENV（platform/wsl.go:258） | ✅ | ❌ `.claude.json`（mcpServers/onboarding/trust）零播种零转发 |
| codex | OPENAI_* env + `-c` 内联参数（app.go:4897-4925，参数随命令行跨界） | ✅ | ❌ `~/.codex/config.toml`（mcp_servers/profiles）零播种；config 写入固定 Windows 侧（app.go:4085,4094,4387 `platform.CodexHomeDir(os.Environ())`，codex_home.go:13-16 无 WSL 分支） |
| opencode | OPENCODE_CONFIG_CONTENT env（opencode_config.go:217,420） | ❌ **唯一载体不可达** | ❌ opencode.json 从不落盘（测试钉定只用 CONTENT 不用路径层，opencode_config_test.go:907-909） |

T4 的判定链：resolver WSL 分支生成 WSLENV 清单时调 `appendWSLENVForwarding(env, request.EnvForwardKeys...)`（platform/resolver.go:144）；转发面＝前缀白名单 `ANTHROPIC_/OPENAI_/CLAUDE_/CODEX_/PI_`（wsl.go:258-264）＋代理精确键（:267-274）＋`EnvForwardKeys`（app.go:2358 `req.EnvForwardKeys = a.EnvVars.Keys()`，仅 envvars.json 用户自定义键）。`OPENCODE_` 前缀不在任何一列；OPENCODE_CONFIG_CONTENT 是 CodeBox 自注入（app.go:5217 `envOverrides = ocOverrides`）而非 envvars.json 键。→ WSL 会话内该变量不存在，opencode 以无 provider 的默认配置启动。

### 1.4 结论：Windows 侧配好的 MCP server，WSL 会话内能不能用？

- **必挂**：opencode 的 amagi provider 本身（T4，连带其路由的一切）；任何 CLI 的「Windows 侧用户配置里的 mcpServers」——claude 的 .claude.json、codex 的 config.toml、opencode 的 preset mcp 段（随 CONTENT 走但 CONTENT 不过界）都不进 distro。
- **视形态而定**（仅 pi）：播种时 Windows `~/.pi/agent` 里已有的 mcp.json 原样进入 distro。其中 `command: "npx"`/`node` 裸命令在 distro 内装好 Node 后可解析；`npx.cmd`、`node C:\...`、绝对盘符路径、Windows 形态 env 值**必挂**（Linux 侧无此形态）；HTTP/远程型 server 与网络可达性相关（B1/B2 同前提）。播种之后再在 Windows 侧新增/修改的 server 永不进入（T3）。

## Q2. amagi 插件/skills 资产跨界（T7/T8）

- 「amagi 资产」的实态：Windows `~/.pi/agent` 下的全部内容——插件实体落 `<agentDir>/npm/node_modules/<name>` 与 `<agentDir>/git/<host>/<user>/<project>`（piplugin/types.go:9-10、service.go:251-267），加上 settings.json、amagi.json、mcp.json、models.json、auth.json。整个树随 `cp -r` 播种（排除项仅 1.1 所列两类）。
- 零适配：播种脚本对内容无任何处理（无路径改写、无脚本形态转换、无行尾处理）。资产内若有 Windows 路径引用/`.cmd/.ps1`/盘符硬编码，在 WSL 侧原样落地、原样失效。
- `~/.agents` 不播种：skills（`%USERPROFILE%\.agents\skills`）与 CodeBox 的两个导出物（`~/.agents/amagi-media-models.json`，vision.go:64-69；`~/.agents/amagi-modalities.json`，modalities_store.go:42-51）都不在 `~/.pi/agent` 下，不在播种面。CodeBox 所有 `~/.agents` 写入均走 Windows `os.UserHomeDir()`，grep 全仓无任何 WSL 侧 `~/.agents` 同步代码。
- 播种后 CodeBox 的插件面板（PiPlugins 绑定 Windows `defaultPiAgentDir()`，app.go:419；piplugin/executor.go:32-39 强制 `PI_CODING_AGENT_DIR` 指 Windows agentDir）与 providerSync（app.go:482）全部只操作 Windows 侧——WSL 侧插件集合冻结在播种时刻。piconfig 同理（piconfig/service.go:31-38 agentDir 解析为 Windows home）：配置 UI 对 amagi.json（含 MCP 路由）的编辑永不触及 distro。

## Q3. PATH 与运行时依赖

- appendWindowsPath/wsl.conf：全仓（代码+文档）grep 零匹配，仅一轮报告提过 `.wslconfig networkingMode`（B1 疑点）。T9 成立：CodeBox 既不感知用户关掉 Windows PATH 注入的场景，也不处理其后果（此时用户自装的 Windows 工具从 WSL 会话消失，且 `/mnt/*` 混合架构问题反而不复存在——两种状态都无差别提示）。
- wslsetup 的 PATH 修补只有一条：`~/.npm-global/bin` 前缀写 .bashrc+.profile（service_windows.go:199-216，精确整行守卫），服务于 CLI 解析，不涉及工具链依赖。
- WSL 内探测覆盖面（T10）：fd/fdfind/rg（wsl_home.go:140-168 + envcheck/checker_searchtools.go:65-93）、五 CLI 登录 shell 解析路径（envcheck/wsl_hybrid.go:33-43）、node+4CLI 安装/版本（service_windows.go:84-113）、Node ≥22.19 底线（:136-141,150-188）。**git、python、unzip、build 工具链等在 distro 内零探测**（负面结论：envcheck 的 python 探测属 Headroom Windows 侧 venv，与 WSL 无关；wslsetup 包 grep python/git 零匹配）。agent 会话内 `git` 缺失/为 /mnt 壳时无任何预警——只探测 CLI 本体，不探测 CLI 工具调用要 shell 出去的依赖。

## Q4. 工作目录形态对工具调用的影响（增量）

- inotify：除 docs/user/usage.md:64（一轮 A4 已引）外，代码与文档无其它提及（负面确认，vendor 除外）。
- **T11（新）**：GitAssist 在 Windows 进程内执行 `git -C <workDir>`（gitassist/service.go:84；包内 grep linux//mnt/ext4 零匹配）；workDir 直通会话工作目录（TerminalView.vue:94 → GitPanel.vue:316 → api/gitassist.ts:28）。工作目录为 `/home/u/...`、`~/...` 或 `/mnt/d/...` 时，Windows git.exe 无法解析该目录 → GitPanel 状态/分支/提交/推送全链失效，且无前置检测（一轮 B4 是 PTY 启动层断点，本条是配套功能层断点，同一根因的两个独立表现面）。PathPicker 同类：初始 root＝workDir 父目录（pathPickerModel.ts:42-43）→ Windows `os.ReadDir`（paths/list_directories.go:46-54）对 Linux 形态路径失败。Usage 统计与 Headroom 不以文件系统方式访问 workDir（Usage 为会话事件记账；Headroom 断点一轮 B1 已覆盖）。
- **T12（新）**：`workDirMapsToDrvFS` 判据是 `w[1]==':'`（pty/service.go:965-970）——用户填 `/mnt/d/repo`（Linux 形态的 DrvFS）时与盘符形态同样慢，但不触发 ：923-931 的 WARN。

## Q5. 编码与输出（T13/T14）

- WSL 命令拼装为 `wsl.exe -d <distro> --cd <winWorkDir> -- bash -lic '<posix-quoted payload>; exec bash -li'`（service.go:904-941），payload 与 env 均无 LANG/LC_ALL 注入。对照：darwin PTY 显式补 `LANG=en_US.UTF-8`（service_darwin.go:410-415，注释言明防乱码）。→ WSL 会话输出编码完全依赖 distro 自身 /etc/default/locale 与用户 shell 配置，CodeBox 不设防；`bash -lic 输出 UTF-8` 是 distro 配置良好的假定，不是代码保证。
- GBK/UTF-16：internal/pty 对会话字节流零编码处理（grep 仅命中原样转发的 base64 通道与 darwin UTF-8 补丁）；全仓唯一 GBK 相关注释明说「GBK 中文乱码不在此层处理」（dockerwsl/service_windows.go:42）。WSL 会话内经 interop 调用 Windows .exe（clang/git.exe 混用等）产生的 GBK/UTF-16 输出无任何层面感知——预期"无"，确认成立。
- CRLF：`StripAnsi` 将 `\r\n`/`\r` 规范为 `\n`（ansi.go:27-28），但仅用于非 raw 的展示/回填路径；live PTY 流不做转换（xterm 侧自行消化 CR）。无 CRLF 混入的主动处理。

## 与一轮报告的关联

- **新摩擦**：T1-T14 全部为一轮未覆盖（一轮止步于会话启动成功点；本轮进入配置供给与工具调用依赖面）。其中 T4（opencode 零 provider）为最高严重度：WSL 默认 shell 下 opencode 会话开箱即不可用。
- **加深一轮条目**：T6 是 B3（PI_* 剥离不彻底）的具体新实例（CODEX_HOME）；T11 与 B4 同根因（ext4 路径形态），但断点面不同（B4＝PTY CreateProcess；T11＝GitAssist/PathPicker 功能层）；T12 是 C1 的判据盲区。
- **不与 A4/C1/C3 重复**：inotify 文档、fd/rg 探测本轮仅作增量负面确认。

## 负面发现清单（搜过无匹配，防三轮重搜）

1. `wsl.conf` / `appendWindowsPath`：全仓零匹配（.wslconfig 仅一轮报告文字提及）。
2. `internal/launcher` 生产代码 MCP 键处理：零（仅 *_test.go 的透传保真测试）。
3. `internal/piplugin` mcp/MCP：零匹配（CodeBox 不管理 pi MCP server）。
4. wslsetup 包 git/python 探测：零匹配；ensureClaudeConfig/ensureCodexConfig/ensureOmpConfig 等对称播种函数：不存在。
5. `~/.agents` 的 WSL 侧写入/同步：零匹配（全部写入固定 Windows os.UserHomeDir）。
6. `OPENCODE_CONFIG_CONTENT` 的 WSL 特殊处理：零（转发面无 OPENCODE_ 前缀，extraForwardKeys 仅 envvars.json）。
7. internal/gitassist 对 linux//mnt/ext4 路径的处理：零匹配。
8. inotify 在代码/文档的其它提及（usage.md:64 之外）：零（vendor 除外）。
9. ensurePiConfig/播种内容的单元测试：零（service_windows_test.go 无 seed 相关用例；wsl_agent_config_test.go 只测 models.json merge/chmod）。
10. GBK/gb18030 在 internal/pty：零匹配。

## 覆盖范围与待核验项

- 已精读：wslsetup 全包、launcher WSL 配置链、platform/{wsl,wsl_home,codex_home,resolver WSL 段}、app.go 五启动函数与 codex/agent 目录辅助函数、pty/service.go WSL 段+ansi.go、gitassist/paths/piplugin/piconfig 关键实现、envcheck WSL 探测面、CHANGELOG 相关条目（1.3.20/1.3.48/1.3.49）。
- 待实证（代码链完整、真机行为未复现）：T4 下 opencode 实际报错形态；T1 播种物内 Windows 形态 mcp command 的实际失败表现；T13 在中文 Windows 默认 Ubuntu（通常 UTF-8 locale）下的实际差异；T11 git.exe 对 `/mnt/d/...` 的报错形态。
- 未覆盖：macOS 侧无 WSL，本报告全部结论仅适用 Windows 宿主；远程/v1 重启路径的配置供给未单独追踪（pi_login_launch.go 一轮 A2 已及）。
