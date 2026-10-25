# Changelog

本项目所有值得记录的变更都会维护在此文档中。

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)，版本章节沿用仓库现有 Git 标签。

## [Unreleased]

## [1.3.120] - 2026-10-24

### Added
- Pi Agent 配置新增 CodeMode 卡片：模型自行选择或适用时优先考虑，以及脚本可覆盖的输出/时限默认值。无需角色、只读或 scope 授权开关；保存沿用原配置校验和原子落盘，保留其他字段与旧配置。

## [1.3.119] - 2026-10-24

### Added
- Pi Agent 配置新增「执行中咨询」：可选 pi 内模型或本机 Codex；默认新建并自动续接自有咨询会话，另可指定已有聊天及只读检测共享连接。接通角色、Leader 开关、预算、前后端校验及保存回读；保留其他配置字段。

## [1.3.118] - 2026-10-20

### Fixed

- **OpenCode 会话启动配置校验失败**：托管模型只配置上下文窗口或最大输出 token 时，不再生成缺少必填字段的 `limit` 对象；仅在两项均有效时输出完整限制，否则交由 OpenCode 使用模型默认值，显式 `maxTokens` 选项仍保留。新版启动时会重新同步托管提供商，修正历史版本生成的不完整配置。
- 新增回归测试，覆盖上下文单独设置、输出单独设置、完整设置、均未设置及非正值边界。

## [1.3.117] - 2026-10-20

### Fixed

- **环境检测 npm 通道前缀对齐（多 node 环境下 Pi 等工具“更新多次失败”修复）**：多 node 并存（fnm + ~/.local/node 平行副本）时，npm 全局安装/更新命令可能与检测到的工具分属不同前缀——resolver controlled candidates 把 ~/.local/bin 排首位解析到平行 node 的 npm，且 npm-cli.js 的 `#!/usr/bin/env node` shebang 在增强 PATH 上被平行 node 抢先，npm 按 process.execPath 推导全局前缀后 `install -g` 落入平行前缀，检测侧版本不变导致更新复检 "version unchanged" 恒失败（实测：Pi 1.1.0→1.3.0 更新每次都装进 ~/.local/node，fnm 侧永远 1.1.0）。修复三件套：①`npmPrefixFromInstalledPath` 从检测到的工具路径推导所属 npm 全局前缀（Unix `<prefix>/lib/node_modules`、Windows `<prefix>/node_modules`，取最外层，嵌套包内 node_modules 不干扰）；②`resolveCommandNPMPathForTool` 让 OpenCode/Codex/Pi/OMP/Claude 的 npm 安装/更新命令优先使用工具所在前缀自己的 npm（Homebrew/Native 安装不受影响，仍走自更新通道）；③`installCommand.pathPrefixDirs` + `prependPATHEntries` 在执行 npm 命令时把前缀 bin 目录前置子进程 PATH，保证 shebang node 与 npm 同前缀、`npm -g` 写回同一位置。

（验证记录：GUI 模拟 PATH 端到端复现 Pi 1.1.0→1.3.0 更新成功且入口路径不变（修复前同场景恒失败）；新增单测覆盖前缀推导/前缀对齐/Homebrew 守卫/PATH 注入；envcheck 包回归除 2 个预存环境穿透失败（干净 HEAD 基线同样失败，与本修复无关）外全过；app 层 envcheck 测试全过；清理误装平行副本）

## [1.3.116] - 2026-10-18

### Added

- **环境检测与五 Agent 管理链路修复（检测/安装/卸载/依赖/PATH 全闭环）**：①PATH 修复可验证且幂等——写入 shell 配置（Windows 为用户 PATH）排除 resolver 诊断标签、规范化目录去重，成功后同步 CodeBox 当前进程并自动复检，外部终端重启要求独立说明；②OpenCode/Codex/Pi/OMP 通用异步卸载——复用 `RunEnvFixAction` 新增 `uninstall_tool` 动作（无新增 Wails 绑定），npm 包/前缀归属（shim 只认普通小文件中的 npm 执行行+完整包路径边界、64KiB 读取上限）与 Homebrew formula 映射（以已验证 prefix 提取 Cellar/opt 相对段）核实后才执行，未知来源/另一 Node 前缀/自定义安装拒绝自动删除并说明；③检测语义修正——checker npm fallback 候选存在但版本探测失败时保留 Installed/路径/错误不再改判 missing（PATH 未命中≠未安装），检测失败与版本未知分开、安装健康与确知最新分开、查询失败不等于最新。

### Fixed

- **管理操作结果语义与 UI 状态**：依赖安装失败/未确认不再伪成功；安装/卸载以实际复检终态而非受理结果或 exit=0 定谳（卸载经 Lstat 确认原入口不存在，空路径/I/O 错误/残留入口不作成功证据）；UI 顶层与问题内修复入口、正确传 Claude method、尊重 canInstall、提示类动作本地处理不提交后台；全局写忙碌防重入、变更后确定性复检；异步受理状态独立于快照观察——无 ID 卸载受理按请求时间匹配并排除容错窗内旧槽、容错预算改为连续失配窗口（长安装不消耗）、过期快照不覆盖后到新状态、组件卸载后不再重建轮询、异步横幅仅由匹配 tracker 事件发布（旧成功槽不冒充他工具终态）、快照短时失败继续轮询持续无法确认显示「状态未知」。
- **前序改动并入本版**：通用设置五引擎默认设置重构为 `useDefaultEngineSettings` composable（引擎选项补齐 Pi/OMP）；usage 定价种子按 2026-10-17 官方页核对——新增 Claude 5.5/5.1 家族（Opus/Sonnet/Haiku/Fable/Mythos）、Sonnet 5 原定 9 月涨价已取消注记、全部条目带来源标注；用量统计图表默认视图改为「用量分布（按模型）」。

（验证记录：agent-outputs/workflow/20261017-envcheck-repair/；全仓 go test/vet、envcheck+platform -race、Windows 目标交叉编译、前端 34 文件 327 测试+构建、15 场景真实组件 smoke 全部通过；diting full 首审 2 Major 红绿关闭，增量复审 PASS）

## [1.3.115] - 2026-10-17

### Added

- **WSL 供给事务闭环与全链路安全收口（G0 平台原语 + G1 供给事务 + Windows 真机验证）**：①G0 平台私有化/原子原语——EnsurePrivateFile/EnsurePrivateDir（POSIX chmod 0600/0700；Windows owner-only DACL，真机 ACL round-trip/owner 规范化/收紧保字节验证）、MoveNoReplace 文件/目录 no-replace 原子发布（分 OS 原生实现；9P UNC 冲突归类保留原始错误，单环境结论不泛化）；②G1 供给事务核心——意图日志（intent）+ 目标内私有暂存 + no-replace 发布 + 前像 CAS + 备份/补偿 + 中断自愈 reconcile（目录单元四态收敛、A2 摘要 pending 持久化、TargetCLI 作用域键、submitDirUnit move 前复验）；③敏感凭据遗留评估与撤回确认（legacy assess + managed-copy 权限探测 fail-closed，状态未确认严格拦截共享视觉清单消费）；④launcher WSL 路径模型配置构建/写入失败硬阻断启动、会话工作目录按目标形态定谳；headroom NAT 网络模式启动闸门；piplugin 两侧操作重放宿主锚点校验门禁；opencode 附加层 MCP 覆盖许可与目标发行版代际强绑定（同名重装或代际未知拒绝生效）；⑤Windows 真机实证——platform 45 用例（symlink 特权真实创建非跳过、UNC 16 写者并发 10 轮仅 1 成功且源与字节保留）、wslsupply 230 用例、真实 WSL E2E 6/6（staging 隔离/models/skills/S4 导入/目标解析，distro 内 stat 验证 700/600，无 /tmp 残留）。

### Fixed

- intent 私有位校验在 Windows 宿主恒失败（Mode().Perm() 恒 0666）→ 改走平台原语；no-replace 冲突分类死分支（生产 os.ErrExist 从未命中 publish-conflict，commit/skills_submit/coordinator 4 处统一判定，真实并发用例暴露）；Windows 盘根相对链接目标（\abs 形态）误归 non-regular-entry → 按宿主原生分隔符判定归 absolute-link；4 处测试夹具 POSIX 权限假设改为探测权威面表达（断言强度不降，无 skip）。

（验证记录：agent-outputs/workflow/wsl-goal-closure/windows-*-real-*.log；一致性修正 12 项先红后绿；发布门禁全仓 vet/test + Windows 交叉编译 + 前端构建 exit 0）

## [1.3.114] - 2026-10-17

### Added

- **P4-A 主路径体验：「WSL 壳 × Windows 主场」定位落地（证据驱动三批，含 T11/C1-C3 落点）**：产品定位澄清——CodeBox 给模型提供非 PowerShell 环境在 Windows 中工作（UE5 开发等），工程/资产/工具链在 Windows 侧。定量证据（p4-evidence.md §8，同仓库两文件系统对照）：DrvFS 上 git status 冷 37.7s/热 3.7s vs ext4 0.19s（20~200 倍）、cp 5000 小文件 17.4s、DDC 跨界 du 42s；E9 证据盲区修正（claude/codex/pi 检测实为 wslsupply 超集，采集漏包）。①A1 形态感知族——PathPicker 五形态（/、~/、~、/mnt/*）显式声明不再静默降级错位目录树（含错误态「从主目录开始」切回入口）、GitAssist 7 公共方法+3 exec 点前置拦截（不再裸撞 fatal）且错误结构化透传前端、DrvFS advisory 从 pty 单点扩到三入口共享面（去重/环形 50 条/只读查询）、形态判定提升 platform 共享（pty 薄委托）；②A2 路径值 per-key 放行门（安全面）——envvars.json 键级 allowPathValue 标记（缺省 false=默认剥离基线不变），显式标记的键盘符路径值放行转发且 WSLENV 加 /p 翻译（C:\... → /mnt/c/...），保留键三重拒绝无绕过（含手改 envvars.json 旁路），UE5 预置模板（纯前端静态清单无夹带）+ 风险确认门 UI；③A3 opencode 启动层 env 盘符值检测对称补齐（其余 CLI 已由 wslsupply 超集覆盖）。

（diting 双审：A1+A3 quick PASS_WITH_MINOR 六关注点全过；A2 full PASS_WITH_MINOR 安全基线 4/4 成立。m-1 错误态导航入口与 F-1 自带 WSLENV 无 /p 交叉半开态均 Leader 直修+测试；F-2 wailsjs 再生并入待办）

（过程摩擦治理同步：p4-friction-log.md 活档建立，A1-A6/B1-B4/C1-C5 十六条三分去向；run_bg 宿主不一致/go fmt 误触两次/构造物清点门等运维结论已沉淀）

## [1.3.113] - 2026-10-16

### Added

- **WSL 配置供给体系 S5：插件面板目标化与原生重放（P3 终批）**：①目标化操作面——pi 插件 install/update/remove 显式目标（Windows/WSL/同时），每目标独立结果（单侧失败独立显示，Windows 失败无锚点时 WSL skipped 不盲装）；npm 源带锚定版本、git 源固定 commit 在 distro 内原生重放（不复制 Windows 实体）。②登记与实体双校验——失败绝不登记已安装、复检登记+实体双面一致、单向漂移显 mismatch；未登记项 install 失败零登记残留（无幽灵行）。③冻结保护——WSL git 仓库脏时 update/版本切换一律冻结绝不 reset/clean；来源不可解析/CLI 缺失逐项冻结诊断不影响他项。④生命周期健壮性——ctx 到期/取消终止 wsl.exe（超时单次尝试无重试风暴、辅助命令超时结构化归类不误导为离线）、distro settings 损坏阻断不冒充成功、启动仅提示缺失不自动安装。⑤前端接线——PiPluginPanel 目标选择器 + WSL 区块（挂载于 v-if 链外恒显）、其他 CLI 面板静态契约占位（omp 排除为 B7 主上裁定的声明）；wailsjs 六方法手工补齐（变量传参，待 wails CLI 再生归一强类型）。真机链路冒烟：distro 内固定版本 npm 原生安装+消费验证 3s。

（diting quick 审 FIX→修复轮 7/7：R1 ctx 不终止 wsl.exe→CommandContext+3min deadline；R2 前端整层未接线→面板挂载+目标选择器；R3 未登记项失败幽灵登记行→跳过写入+零残留断言；R4 损坏 settings 冒充 already-ok→三态区分；R5 版本切换旁路脏冻结→前置同款 dirty 检查；R6 注释矛盾如实化；R7 校验双源→删死导出单源化。增量复审 PASS_WITH_MINOR，三 Minor（Probe 展示腿三态一致/辅助腿超时丢错误码/头注释陈旧）Leader 直修）

（实施过程四遇 GLM API 500 中断：原任务+两轮 resume 均 4 调用即断，修复轮改新会话全新上下文 47 调用零中断完成——判因不可定论，上下文超限假设已被主上证伪）

## [1.3.112] - 2026-10-16

### Added

- **WSL 配置供给体系 S4：Claude/Codex 用户配置导入 + OpenCode 附加层扩展（P3 第五批，设计 §四两表）**：①Claude 适配器——.claude.json 仅 mcpServers server 级导入（复用 S2 转换器/冲突框架）、settings.json 白名单字段级偏好、CLAUDE.md/commands/agents/skills 目录单元（复用 S3）；projects/trust/onboarding/账户状态/未知键永不跨界（负面断言字节级验证）。②Codex 适配器——config.toml TOML 保真节编辑（零编辑 round-trip 字节恒等、增删改后注释/非受管节/CRLF/多行数组/点键保留；根级点键内联表明确「不支持形态」拒绝）、可移植 profiles 白名单、AGENTS.md/skills；auth/sessions/history/cache 永不跨界。③OpenCode 附加层同名冲突显式覆盖选择（默认保留本地，勾选 overrideGlobal 才写入，写入点唯一无绕过）。④附带生产缺陷修复：wsl_home.go 探测 --exec 化（单点共享接缝，修复 S1 起真机 shim/路径探测恒空）。⑤前端两区块+wailsjs 再生+spy 实参透传测试层（补齐 vue-tsc/vitest 均不覆盖的生成层）。

（diting quick 审 FIX→修复轮 4/4：F1 wailsjs 九处字面量传参改变量透传+spy 杀伤力实证；F2 TOML 自校验收敛为本轮实际写入键（冻结/保留本地键不再否决健康更新与 adopt）；F3 点键形态明确拒绝；F4 断言升级整文相等）

## [1.3.111] - 2026-10-16

### Added

- **WSL 配置供给体系 S3：共享 skills 与用户资源同步（P3 第四批，设计 §四「共享 ~/.agents」）**：宿主侧有界扫描器（深度/数量/字节上限可配，超限如实「扫描未完成」）+ 静态兼容检查（越界/循环/绝对/目录链接逐项报因且整 skill 阻断；Windows junction 的 ModeDir|ModeSymlink 组合位先判链接绝不递归；.ps1/.cmd/.bat 明确「未支持」不原样复制；凭据排除名单链接同样套用）+ 技能单元 shared/skill/<name> 接入协调器（同意门复用跨代语义、TrackedProjection 六分支含手改/删除冻结与增量整目录重写）+ 目录级私有 staging 提交原语（唯一临时名→0644 写入→rename 交换→distro chmod 批量补偿→树 hash 读回校验；失败保留前像、候选总量 8MB 双闸）+ 目录级条件撤销 + 状态页第五面与五动作 UI。真机冒烟：agent-browser 真实技能供给 distro 临时验证位，wsl.exe test 逐文件核对 + SKILL.md 读回正确。

（diting quick 审 FIX→修复轮 7/7：F1 junction 死分支三处前移+双形态测试；F2 恢复写前像保留；F3-F7 注释时序/排除名单一致性/总量封顶/预览镜像同意门/恢复写 chmod+树 hash）

## [1.3.110] - 2026-10-16

### Added

- **WSL 配置供给体系 S2：Pi 配置与 MCP 导入 + remote 计划接入（P3 第三批，设计 §三/§四/§五）**：①Pi 配置键级 TrackedProjection 三单元（settings 白名单字段/amagi.json 键集/mcp.json server 级）——冲突判定按设计冲突表逐键执行（无记录+存在=unmanaged 保留、目标=上次写入 hash 才跟随、WSL 手改即冻结、双侧同变保留 WSL、来源删除仅托管未手改才删），冻结/停跟随/预览后接管三操作面；**三单元 per-target 同意门**（首启前需明确同意，未同意只读不写，状态页「启用投影」入口，登录静默不阻断）。②形态转换器全表（纯函数，不执行命令）：npx.cmd/npm.cmd 仅目标验证过原生命令才映射、未知位置 shim/ps1/exe/未知 shell 标需手动适配不造假、args 盘符路径先匹配已部署资产否则 wslpath 验证、env 仅声明为路径的键转换、URL scheme 词首锚定不误判（https:// 等 API endpoint 放行）、%VAR%+分隔符形态点名拒绝、HTTP/远程保留 URL。③状态页逐单元详情（相对路径/归属/冻结原因/失败阶段/可执行动作，敏感项只显文件名与方向）+五动作（检查/预览/应用安全更新/保留本地/确认接管，无强制覆盖）。④remote pi API-provider 与 OpenCode WSL 计划接入 EffectWSLConfigSupply（层内容经 SecretBuffer、OPENCODE_CONFIG 晚绑定重锚、双链路同目标逐字节一致）。

（diting full 审 FIX→修复轮 5/5：F-1 冻结登记丢 Subs（跨周期测试钉住）；F-2 URL 误判盘符；F-3 同意门 Leader 裁定落地+代际缺口顺带修复；F-4 注释如实化；F-5 %VAR% 漏检）

## [1.3.109] - 2026-10-16

### Added

- **WSL 配置供给体系 S1：托管 provider 与共享小文件分发（P3 第二批，设计 §四/§五/§六）**：①前置门槛清偿（S0 审查遗留）：Compensate 持 applyMu 串行化、submitFile chmod 失败按敏感度分叉（敏感删除/普通恢复前像）、真并发唯一名钉死。②托管 provider 分发（pi/managed-models 单元，ManagedNamespace 策略）：Config/Secrets 快照构建 amagi-* 期望集 → 严格解析合并（两级损坏保护、非 amagi provider 保留、失败 provider 不误 prune、登录 contextWindow 窄字段补齐）；启动 lazy + 保存标脏异步推送（single-flight、活动会话暂停、停机 distro 不唤醒、重启补漏）；选中 provider 不可构建阻断启动（中文报错）。③共享小文件：vision-export per-target 同意门（密钥跨界明确授权、0600+私有 staging、规范化内容不变零动作、撤回未确认可见不虚报）；modality-kb schema 校验后补缺、冲突保留不反写。④OpenCode 附加层 MCP 确定性形态检查（npx.cmd 仅目标验证过原生命令才映射、Windows 专属形态丢弃不造假可用、全局同名保留本地）+ 已批准失败语义变更（选中模型不可安全落地→阻断启动，不再无 provider 静默启动）。⑤envcheck「WSL 内的 CLI」卡片下「配置状态」区（四面状态+最后检查时间 per-CLI+待推送标记）与视觉清单同意按钮（wails generate 再生绑定）。

（diting full 审 FIX→修复轮 5/5：F1 Critical providerSyncMu 死锁（锁区间 return 无 defer）结构性修复+锁可用性测试；F2 PendingSources 累计收敛清除；F3 跨代授权 hash 归零；F4 per-CLI 检查时间；F5 consented 独立态。真机 e2e 3/3 复跑 PASS）

## [1.3.108] - 2026-10-16

### Added

- **WSL 配置供给体系 S0：安全接入与目标一致性（P3 首批，设计文档 agent-outputs/workflow/wsl-session-friction/p3-config-supply-design.md 经主上审定）**：新领域包 `internal/wslsupply` 落地供给协调器骨架——TargetRef（distro+UID/Home+generation，身份拿不到按未知处理）、只读 Inspect（批量探测→Snapshot）、纯计算 Plan（不写盘不 spawn）、复核式 Apply（预期目标 hash 前置、逐项结果）、MarkDirty、脱敏 GetStatus；状态清单 `~/.amagi-codebox/wsl-config-supply.json`（schemaVersion/lastWrittenHash/冻结等，私有原子写，损坏回退未托管，敏感 hash 不出状态面）；目标内私有 staging + 唯一临时名 + 权限先收紧再 rename + 最终校验的提交原语；CAS 条件补偿（只撤销本次产生且 hash 仍匹配的写入）。

### Changed

- **A2 凭据同步桌面/远程统一执行边界（消除 shell/Go 双实现安全差异）**：remote/v1 重启链路的登录载荷 cmp/cp 同步片段退役（载荷只剩 env -u 凭据键重锚），新增 EffectWSLConfigSupply（PTY 启动前）经协调器走与桌面同一 `SyncWSLPiLoginAuth`——判定语义零变化（SHA-256+mtime 2s 容差/DistroNewer 回写/DistroTieKept/探测失败零写入/DistroHasCopy 降级/NoAuth 拒绝/chmod+清理双失败拒绝）；BuildPlan 保持纯读零副作用；Abort 不回滚凭据刷新（持久认证状态）。双链路 9 用例故障矩阵结果一致。
- **停用 WSL 首装整树播种（T1 安全债清偿）**：wslsetup ensurePiConfig（cp -r 整目录，曾带入 sessions/auth/插件实体）删除，CLI 安装成功语义不变；旧已播种副本按未托管现状识别（不迁移不清除）；AlreadyOK 不再阻止后续检查。
- App 装配接线：桌面启动链经协调器，providerSync/预设监听挂 MarkDirty 空位（S1 接真实分发）。

（diting full 审 PASS_WITH_MINOR：4 Minor 中 M3 陈旧注释已修，M1 Compensate 加锁/M2 清理策略/M4 真并发测试列为 S1 前置门槛；真机只读冒烟 PASS）

## [1.3.107] - 2026-10-16

### Added

- **WSL2 mirrored 网络模式下 AI 代理分流对 WSL 会话放行（摩擦治理批次二，主上裁定借 mirrored 一并解决回环跨界①组）**：新增 platform.WSLNetworkingMode 探测（每次直读 %USERPROFILE%\.wslconfig 的 [wsl2] networkingMode，INI 容错含 BOM/注释/大小写；不设进程级缓存——缓存会让拒绝文案「wsl --shutdown 后重试」同进程失效；空串按 NAT fail-safe）；mirrored 模式下 localhost 双向可达，aiproxy 闸门放行 WSL 会话（代理键经 WSLENV 原样跨界，ALL_PROXY/all_proxy 补入转发白名单）；NAT/未知维持拒绝但文案改为可操作指引（.wslconfig 路径 + networkingMode=mirrored + wsl --shutdown）；环境检测页新增网络模式三态信息条目（severity-only 不影响 AllOK）；前端提示与 ai-proxy-routing 契约同步更新。真机验证：本机 .wslconfig=mirrored，闸门实测 admitted。

### Changed

- **WSL 会话可用 Pi 订阅登录模型（主上裁定推翻「拒绝而非复制」旧契约）**：桌面与 remote/v1 两处 WSL 拒绝闸门解除，Pi OAuth 登录预设可启动 WSL 会话，认证目录锚定分叉——非 WSL 会话维持 canonical Windows Home 锚定，WSL 会话锚定 distro 内 ~/.pi/agent（PI_CODING_AGENT_DIR 剥离 + 载荷 env -u 重锚，R2/R3 语义保留）；启动时惰性双向凭据同步（launcher.SyncWSLPiLoginAuth）：SHA-256 内容比对 + mtime 新陈判定（2s 容差）——Windows 新→前向覆盖 distro，distro 新→回同步 Windows 侧（UNC 原子写+chmod 600，保 WSL 内轮转出的新 token 不被旧副本摧毁），秒差并列→保守取 distro 侧不动，mtime 探测失败→两侧零写入降级；remote 载荷内嵌同步片段与 Go 侧同带 2s 容差（stat 秒差 >2 才前向，歧义带不覆盖消费侧）；契约文档 pi-login-providers.md 更新为「同步凭据副本 + 新者为准」。真机 e2e：首跑 Copied（distro 陈旧副本被刷新）、二跑 Fresh（收敛幂等）。
- **Linux 形态工作目录的 WSL 会话不再启动失败（B4，已真机定谳）**：ConPTY CreateProcess 对 Linux 形态 lpCurrentDirectory（~/、/home/...、/mnt/...）一律 err=267 ERROR_DIRECTORY——按文档推荐「仓库放 ext4、工作目录填 Linux 路径」的会话必现启动失败。修复：WSL 会话且 workDir 为 Linux 形态（/、~/ 前缀含裸 ~）时不传 ConPtyWorkDir，工作目录由 wsl.exe --cd 独自承担（其本身兼容两种形态）；盘符/UNC 形态语义不变。真机 e2e：~/ workDir 真实 ConPTY 启动+echo 回读 PASS。

（批次二经 diting 两轮快审：第一轮 FIX（1 Major 双向同步方向 + 5 Minor）修复轮全落地；增量复审 PASS_WITH_MINOR，q2-1 载荷容差由 Leader 直修；证据链 agent-outputs/workflow/wsl-session-friction/）

## [1.3.106] - 2026-10-16

### Fixed

- **WSL 会话环境供给快赢四连（WSL 摩擦治理批次一，证据报告 agent-outputs/wsl-session-friction/）**：①opencode WSL 会话开箱零 provider（T4）——唯一载体 OPENCODE_CONFIG_CONTENT 不在 WSLENV 转发面；修复走路径载体：runtime provider config 写入 distro 内 ~/.config/opencode/amagi-codebox.json（CodeBox 命名空间私有文件，原子写+chmod 补偿+fail-closed，不碰用户全局配置），经转发面新增精确键 OPENCODE_CONFIG（Linux 路径值）指向它；opencode 以附加配置层加载（实测 v1.17.18：global → OPENCODE_CONFIG → project → CONTENT）。②DrvFS 慢速告警只认盘符形态（T12）——workDirMapsToDrvFS 扩展识别 /mnt/<单盘符>/* Linux 形态（排除 /mnt/wsl、/mnt/docker-desktop 等非盘符挂载），两种形态同告警。③WSL 会话不注入 LANG（T13）——对照 darwin 补丁补齐：无 LANG/LC_ALL/LC_CTYPE 时注入 LANG=en_US.UTF-8 并同步追加进 WSLENV 清单，任一存在不覆盖。④Windows 路径值环境变量剥离仅护 PI_*（B3/T6）——StripWSLHostPathPIEnv 泛化为 StripWSLHostPathEnv，覆盖全部转发面（五前缀+精确键+envvars.json 自定义键，判据与拼清单同源 platform.ShouldForwardToWSL），剥离点上移到 resolveEmbeddedLaunchSpec 单一漏斗覆盖五 CLI，CODEX_HOME=C:\... 类跨界非法值同防。

## [1.3.105] - 2026-10-15

### Fixed

- **旧版本遗留孤儿内核也能自动回收**：v1.3.104 的回收依赖 pidfile，而旧版本从不写 pidfile，升级后首次启动仍可能撞上旧孤儿占住的端口。现在改为无 pidfile 全量扫描——macOS/Linux 扫描进程表中命令行含本机 runtime 配置路径且父进程已死的内核直接回收（v2rayN 等他人内核因配置路径不同不会误伤，其他运行中实例的内核因父进程存活不会误杀）；Windows 以可执行文件名匹配加父进程已死判定回收。升级后首次启用分流即自动清理全部旧版遗留孤儿。

## [1.3.104] - 2026-10-15

### Fixed

- **重启后代理端口被孤儿内核占用**：应用被强杀/崩溃/更新替换等非正常退出时，sing-box 内核会残留并持续占用代理端口，导致之后每次重启都报“代理端口已被占用”。现在每次启动内核前会自动回收遗留孤儿——仅当进程存活、命令行确属本机 runtime（防误杀 v2rayN 等他人内核）且父进程已死时才回收，其他运行中实例的内核不受影响；Windows 侧另将内核绑入 kill-on-close Job Object，应用无论以何种方式退出都会连带终止内核，从源头杜绝孤儿。（配套 pidfile 生命周期与端到端回收测试）

## [1.3.103] - 2026-10-15

### Changed

- **服务分流规则改为小卡片与拖拽排序**：规则列表改为与提供商页一致的边框小卡片（自适应多列网格、hover 浮起），卡片头集成拖拽手柄、序号、名称、启用开关与移除入口，替代原「上移/下移」按钮。拖拽基于 Pointer Events 自绘（WKWebView 不支持 HTML5 DnD 的环境同样可用），悬停即实时让位，支持边缘自动滚动与拖拽反馈；手柄聚焦后仍可用 ↑/↓ 键盘调整顺序。排序逻辑抽为独立纯函数模块并配套单元测试。

## [1.3.102] - 2026-10-14

### Added

- **关联服务提供商分流**：AI 代理分流页可直接选择 CodeBox 已配置的 API 服务提供商，自动提取已启用协议的域名，再分别选择出口节点；关联地址自动更新，保留节点与草稿，无需重复录入域名。
- **TUN 出口绑定**：默认自动绑定网卡，也可指定物理网卡或跟随系统路由；支持独立 DNS，保持本地 API、Headroom 与回环上游可达，减少 v2rayN TUN 再次接管节点连接的问题。

### Changed

- 关联来源删除、地址失效及共用域名的冲突出口会明确提示；地址变化后提示应用更改，新建会话和应用内模型请求拒绝使用过期关联规则。
- AI 代理私有配置升级为版本 3，自动保留原有规则、节点、端口、订阅与启用偏好；启动恢复在服务提供商配置加载后执行，策略读入前阻止自动探测使用未知出口。

## [1.3.101] - 2026-10-14

### Added

- **服务分流规则可编辑扩展**：Claude、OpenAI 改为可编辑的默认规则，支持添加任意服务、修改名称和域名、选择代理节点或直连、启停、移除与调整优先顺序。每条规则可设置独立专用端口，留空时只参与统一入口的域名分流。
- **已有代理配置自动迁移**：旧版固定的两组服务配置升级为规则列表，保留已有节点、域名、端口、订阅、上游设置和启用偏好，无需重新导入。

### Changed

- 统一入口按规则顺序匹配，首个命中生效，支持子域名与通用域名分别选择出口；专用入口始终使用所属规则的出口。停用规则不占用端口，删除全部规则后沿用未匹配域名策略。
- 域名编辑保留多行输入草稿，保存时统一小写并去重；切页保留新增规则及排序，保存与应用继续分开。

## [1.3.100] - 2026-10-14

### Added

- **AI 代理分流独立页面**：只读同步本机 v2rayN 的订阅分组和节点，Claude 与 OpenAI 分别选择出口；也支持订阅链接、节点链接、Base64、Clash YAML 和 sing-box JSON 导入。使用独立 sing-box 进程，未匹配域名默认直连，可在页面管理端口、域名规则、节点、内核和运行日志。
- **接入 CodeBox 会话与模型工具**：新建的 Claude Code、Codex、Pi、OMP、OpenCode 会话自动使用统一分流入口，Pi / OMP 多模型及继承环境的子 Agent 按目标域名选择出口；新启动的 Headroom、GitAssist、多模态与额度探测使用同一路由策略。已有会话需重启后接入，WSL 宿主回环转发暂不支持。

### Changed

- 分流配置保存与应用分开，切换页面保留未保存草稿；启动失败回滚已有内核配置，正常退出清理进程并保留启用偏好。入口只监听本机，Wails 状态不返回节点凭据或订阅 URL。详见 [使用说明](docs/developer/ai-proxy-routing.md)。

## [1.3.99] - 2026-10-13

### Added

- **Pi 登录预设支持上下文窗口配置（Leader 与 amagi 子 agent 双向生效）**：登录认证模型（Pi 原生 /login 的内置 provider）此前只能配置模型与思考等级，上下文窗口固定用 Pi 内置目录值。新增：①预设表单可选填上下文窗口（tokens，留空/0 = 内置默认，Vue number input 隐式转换兼容）；②窗口是模型级共享值（pi modelOverrides 按 provider/model 寻址）：保存时任一同模型预设的新窗口全组对齐，配置展示与实际生效一致，无静默串值；③保存/删除时经 ConfigService 注入回调（SetPiLoginPresetChangeListener，app 装配注入；回调在写锁外触发且为 reconcile 语义——落盘值以 config 当前权威状态为准，乱序事件自然收敛，不依赖事件接序）把 `providers.<provider>.modelOverrides.<model>.contextWindow` 深合并进 canonical root（~/.pi/agent）的 models.json——保留用户手写 overrides（如 anthropic supportsStrictTools）与兄弟键；清除时连带剪枝空键/空 modelOverrides/空 provider（pi 对空 provider overlay 报 composition error）；④桌面启动链（LaunchPiSession 登录分支，providerSyncMu 互斥）兑底重写；⑤BuildPlan 保持纯读零副作用契约（remote/restart 路径读已落盘目标态）。两级损坏保护：JSON 语法损坏与目标路径嵌套结构类型错误均报错不覆写。写入格式与 Pi 加载闭环实测：pi-1.1.0 ModelRuntime 加载 UpsertPiLoginModelOverride 产物 contextWindow 生效（zai/glm-5.2 400000 ✓）。amagi 子 agent 经 PI_CODING_AGENT_DIR 透传读同一份 models.json 同样生效。同时确认：amagi API 预设模型（amagi-* provider）的子 agent 上下文窗口机制本已支持（models.json 模型定义 + PI_ env 透传 + 全限定 --model），实测 pi 独立进程解析 amagi-glm/glm-5.3 → 400K，无需改动。（含 diting full 审 F01-F06/I02 六项修复：Vue number input 隐式转换、模型级共享值语义、reconcile 防乱序、嵌套损坏保护、空 provider 剪枝、清除路径幂等；删除测试专用探针导出）

## [1.3.98] - 2026-10-12

### Changed

- **Provider Center 导航关系与内容呈现精修（v4）**：①CLI 导航改父子两行——二级 Pi/OMP/OpenCode underline 在上，其正下方固定缩进的「Pi 配置」等所属标签 + 三级紧凑 pill 在下，消除二三级同层分离显示的从属关系困惑（预设页仍单行，caption 紧邻格式页签）；②内容区选择性图标矩形归组（新增无状态 ConfigContentGroup）：Pi MCP 三组（Leader/默认/角色附加）与并发三组（默认/服务商/模型）、双引擎注册表每条模型记录获得明确边界；OpenCode provider/agent/mcp 记录恢复轻矩形图标头，permission/嵌套模型/variants 保持无框列表——简单记录仍用列表，不恢复整页卡片平铺；③左栏纯净化：SectionNav 条目可视层仅名称（受管/类型/生效/需修正等说明文字全部迁至右侧常显承载或保留在屏幕阅读器描述中），限高补偿 100vh−320px。Registry 提供商正文次序有意调整（连接字段→模型→其他字段）。验证：build/全量 vitest/未修改的 Pi e2e 7 用例/静态审计 8 项/八批浏览器 153 断言 0 findings 0 page error（含新增 content 批源码与保存值契约核对）；diting quick PASS 零 findings；生产白名单恰 6 文件，保存/解析/字段函数与基线逐字一致。

## [1.3.97] - 2026-10-12

### Fixed

- **Provider Center 导航层级修正（顶侧页签恢复 + 左栏收缩到编辑器内）**：v1.3.96 的统一左栏把二三级页签也收进了左侧，主上修正为——CLI 独立配置页的 Pi/OMP/OpenCode 与 Agent 配置/模型提供商/认证登录等页签保留顶侧形式，左栏只承接页内原卡片式平铺的功能项。二三级回到顶侧单行 subnav（左 underline + 右紧凑 pill + 通栏分隔线，<1024px 收紧保单行），页面级左栏机制（选择记忆/导航快照/owner 校验）删除，左栏仅在五个有分区的编辑器内部（Pi：角色模型/上下文压缩/MCP 路由/并发限制；OMP：角色模型/子代理覆盖；OpenCode 全局 9 分区；注册表/认证的动态条目）；预设页恢复顶侧格式 Tab + 整宽分组列表（800 档从 356 恢复 480px）。页签文案从 configNavigation 纯导航表单一来源派生；生产 diff 仅 ProviderCenterView 一个文件，五编辑器/e2e/configNavigation 零改动，external 休眠代码待主上看图验收后单独清理。验证：build/全量 209 测试/e2e 7 用例（零 diff）/静态审计 10 项/七批浏览器 135 断言 0 page error（local 路径首次页面级验证）。实测修正：800 档右栏 348px（页面滚动条 8px 占布局）；Pi Agent 800×600 首分区 top 349px（v3 增量 50px，与设计估算一致）；OpenCode 预设窄档工具栏 3 行为既有规则。

## [1.3.96] - 2026-10-11

### Added

- **Provider Center 两页布局重设计（预设 / CLI 独立配置）**：两页统一为「左侧导航栏 + 右侧内容区」——二级引擎/格式、三级模式与编辑器内分区共用一条文字左栏（注册表提供商/认证条目为动态叶子，master-detail），右侧单分区无卡框显示，同文件切区保留草稿（visited + v-show）；800px 最小窗口下 112px 左栏 + ≥340px 右栏可用（容器查询分档 + 不支持时回退）。说明性文字全面降级：五个配置编辑器的 h2/subtitle 与逐字段 hint 段落清零，57 条原文按降级矩阵迁移——生效时机为常显标签、覆盖风险为警示横幅、结构性解释进键盘可达的 InfoTip、教学文案删除，关键语义（MCP 三态、并发清空回落、受管覆盖警告、OAuth 只读、压缩后果）零丢失。新增 ui 基件 SectionNav（roving tabindex 键盘导航）/EditorSection/EditorToolbar/InfoTip/FieldLabel 与纯静态 configNavigation 导航表；预设列表改分组列表行（整行键盘可编辑/删除）；懒加载、presetLoadKey 重挂载、engine 按需加载、保存链路与 api 层零改动。e2e 按新 DOM 契约适配（原 30 条业务断言逐条保留）并新增单显/草稿/键盘导航结构用例；设计/实现/审查/验收全链 artifact 归档于 `agent-outputs/workflow/provider-center-layout-redesign/`。

### Fixed

- **皮肤预烘焙同步回调无限递归（RangeError: Maximum call stack）**：`requestBake` 曾在同步 no-op 路径（blur=0/烘焙不可用/缓存命中）同步回调 `onDone`，而 App.vue 传入的回调是 `syncSkinDom` 本身——开启皮肤且 blur=0 或烘焙失败后必现同步递归栈溢出。修复：同步 no-op 路径不再回调（调用方已在同一同步渲染经 `currentSkinImage` 取到终态），`onDone` 仅在一次真实异步烘焙终态后触发；jsdom 回归用例覆盖三条路径与回调重入模式。
- **配置编辑器 chunk 加载失败无法恢复（Chromium 负缓存）**：失败的模块 URL 被 Chromium module map 负缓存，重试按钮仅重挂载组件、重跑 loader 也不会重新请求同一 URL。修复：重试改为整页重载恢复（module map 随文档重建，新部署 index 指向新 chunk），会话级标记防自动循环、App 启动时清除以允许下次故障再次走重载；sessionStorage 不可用时回退原重挂载重试。
- **预设列表读取错误被吞（误显空态）**：`provider` store 的 `loadPresets` 内部捕获读取错误不 rethrow，PresetList 与 OpenCodePresets 的本地 error 永不置位——OpenCode 预设读取失败时误显「默认行 + 暂无预设」而非错误态。修复：两组件在 await 后消费 `store.presetLoadError` 呈现 ErrorState 与重试入口（store 契约不变，其他调用方零影响）。
- **mobile 测试套件跨用例 app 泄漏根治（Windows CI 间歇性抖动溯源）**：WorkspacePage/SessionsPage 页面级测试的 mountWithRoute 只挂载从不卸载，每个用例泄漏一个完整存活 app（webui 5s 轮询定时器/store/WS 回调链），叠加 `vi.stubGlobal('fetch')` 的环境全局性，旧 app 持续消费后续用例的 fetch mock 并交叉干扰——Windows CI/满负载下 webplane 接管点击与 tui-session 观察态用例间歇性假失败（实测 1/2~1/8 复现，按钮在树内 5s×100 次点击 0 命中 handler）。修复：五文件统一登记 activeWrapper、afterEach 卸载；时序敏感断言改 5s waitFor 轮询（同套件既有惯例），acquire 断言改「至少一次」语义（重试形态下 exactly-once 过度指定）。验证：本地满负载 10/10 全绿（修复前同期必现抖动）。

### Security

- **依赖漏洞清偿（v1.3.94 推送时 GitHub 报 21 条 Dependabot 告警全部清零）**：Go `labstack/echo/v4` 4.13.3 → 4.15.3（连带 gommon/isatty/go-colorable，vendored 同步，high）；npm 侧 frontend/mobile `npm audit fix`（全部 semver 兼容内修复）：`@capacitor/android` critical（远程内容可经内部代理路径加载至应用源）、`@xmldom/xmldom`/`undici`/`@vue/server-renderer`/`vue`/`brace-expansion`/`source-map-js` high、`dompurify`/`@vitest`/`vitest`/`js-yaml`/`postcss-selector-parser` moderate/low，三套 lockfile audit 归零。验证：go build/vet + remote 包测试、frontend/mobile build + 单测全绿（mobile WorkspacePage.webplane 一例并发时序偶发 flake 与升级无关，隔离/复跑全过）。

### Changed

- **CI/Release workflow actions 升级至 Node 24 运行时大版本（消除 Node 20 弃用告警）**：checkout v4→v7、setup-go v5→v7、setup-node v4→v7、upload-artifact v4→v7、download-artifact v4→v8、softprops/action-gh-release v2→v3；golangci-lint-action@v9 已是最新不动。核查各版本破坏性变更：checkout v7 的 BREAKING 仅影响 fork PR + pull_request_target 场景（本仓仅 push/workflow_dispatch 触发，无波及）；upload-artifact v7 直传为可选参数（`archive: false`），默认归档行为不变；download-artifact v8 的不解压行为仅作用于直传形态；gh-release v3 为 Node 24 运行时迁移。工具链版本基线（Go 1.25.0 / Node 20.19.0，C-001）不变。

## [1.3.95] - 2026-10-11

### Added

- **Pi 统一压缩模型可视化配置**：Pi 配置页新增 `compaction.model` 卡片，区分未指定（删键）、统一模型（`provider/model[:thinking]`）和 pi 原生模型（显式 `null`）；复用服务商/模型/思考档位选择器，可复制 luban 当前模型为独立配置。说明对齐 amagi-pi 的项目优先级、Leader/JSON/RPC 共用、手动/自动/溢出压缩与下次压缩读取规则。前后端验证压缩字段，拒绝不完整配置并保留原文件，兼容未设置与空对象；完整配置导入在任何写入前共用校验并检查回滚快照，覆盖模型导出/还原及写入失败回滚；不改角色模型或 pi 认证配置。

### Fixed

- **模型选择与源码保全**：只将已知 thinking 后缀从模型 ID 拆出，保留 `ollama/qwen:32b` 等原生 ID；选择器可清除档位、手动输入，换模型时清除不支持的档位，未收录的当前模型保持可见，目录加载失败仍可编辑。Pi 页非法 JSON/非对象根停留源码模式，阻止旧可视化状态覆盖原文。回归覆盖纯函数、Go 隔离文件保存及真实 Chromium 中的 Vue 交互（测试专用存储）。

## [1.3.94] - 2026-10-08

### Added

- **Pi 登录认证（OAuth）提供商支持**：从标准 `~/.pi/agent/auth.json` 发现 Pi 原生 OAuth 登录项，Provider Center 以「登录认证 · Pi」卡片展示（刷新登录状态/退出登录），点击卡片保存预设（预设名、Pi 原生模型 ID、思考等级）；会话设置 → Pi 选择「提供商名 · 登录认证 (Pi)」+ 预设，经现有新建会话入口启动终端。命名空间隔离（R1）：登录来源 id `pi-login/<base64url(provider)>` 与登录预设 key 三段结构同公共 provider/stable key 规则不相交，字面名 `pi-login` 的普通 provider 不受影响，同字符串 key 双向互斥拒绝；登录失效/预设删除按错误关闭、绝不回退普通 API 路径，前端失效保留、必须显式改选（R4）。认证目录锚定（R2）与 Shell 启动重锚（R3）：发现/启动检查钉定 CodeBox 进程 Home 的 canonical `~/.pi/agent`（忽略 `PI_CODING_AGENT_DIR` 环境覆盖），desktop embedded/external/v1 重启全部显式设回该目录——环境变量面板自定义 `HOME`/`USERPROFILE` 无法再把 Pi 导向另一份 auth。登录类型仅限 Pi：Claude Code/Codex/OpenCode/OMP 启动路径拒绝登录引用，WSL 场景拒绝。测试：Go 临时 HOME/合成 fixture 覆盖发现、隔离、持久化、重启、参数、退出登录、WSL 拒绝、R1-R3；macOS 真实桌面入口真实 PTY + 真实 zsh startup（.zshenv 重导出 agent root 与凭据）双重对抗用例；前端 `piLaunchSelection`/`useSessionLaunch` 启动闸门用例；浏览器到 Go/PTY 可重复验收 harness（`e2e/pi-login.html` + 测试专用 Wails transport bridge，认证与 CLI 均为隔离 fixture）。契约文档 `docs/developer/pi-login-providers.md`。配套：.gitignore 排除 `Saved/` 本地运行时产物。

## [1.3.93] - 2026-10-01

### Fixed

- **pi 会话运行中「终端⇄Web」切换按钮消失（webui 误判 ended 根治）**：`internal/webui` 状态机在 available 后连续 2 次探测失败（单次 1.5s 超时，约 3s 持续不可达）即单向落 ended 并停止轮询——重负载任务（长测试/构建/大量输出）阻塞 pi 扩展进程事件循环时，/api/info 连续超时是「假失联」而非会话结束，切换按钮被误判永久隐藏（codebox 日志 2026-10-01 三次误判均精确落在任务执行窗口）。修复：ended 落定前先校验 pid 存活性——`internal/platform` 新增 `ProcessAlive(pid)`（unix `Signal(0)` / windows `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)+STILL_ACTIVE`，per-OS 文件）：pid 存活则保持 available、failStreak 清零、记 INFO 日志继续探测；pid 已死才落 ended（原语义保留）。`aliveFn` 测试 seam 随 `now`/`probingWindow` 既有模式；新增三用例（存活保 available / 已死落 ended / probing 窗口回归）+ ProcessAlive 真实进程正例；`go vet` + `go test ./internal/webui/ ./internal/platform/` 全绿，`GOOS=windows` 交叉编译通过。

## [1.3.92] - 2026-09-25

### Fixed

- **Pi 配置页 mcp.leader / mcp.default 白名单编辑入口不可达（实测复现）**：配置处于「不加载」（显式空数组 []）或「全部」（未设置键）时，点三态选择器「白名单」无任何反应——`switchMcpListMode` 从 all/none 切入写入空列表 `[]`，而模式高亮与编辑器渲染都派生自数据推断 `mcpListModeOf([]) = 'none'`，高亮跳回「不加载」、`v-if="mode === 'list'"` 永不成立，名单编辑器不可达（用户面对三个按钮看不到任何服务器名单输入入口，与 mcp.agents 的直接编辑体验不一致）。修复：UI 编辑态与数据推断态解耦（`mcpLeaderUiMode`/`mcpDefaultUiMode` ref，点「白名单」立即进入编辑视图，空列表中间态不跳回；JSON 重解析两条路径同步回数据态）；名单编辑器改为常显（all/none 态也直接显示，空态引导「添加服务器即切换为白名单」，输入即切白名单模式），三态选择器保留为显式模式声明；badge/hint 跟随 UI 编辑态。

## [1.3.91] - 2026-09-25

### Added

- **Docker Desktop ↔ WSL 集成健康检查与一键自愈（2026-09-25 运维复盘 §1.2 清偿）**：Docker Desktop 与用户发行版同时冷启动初始化期间，/mnt/wsl/docker-desktop 共享挂载可能被竞态挂坏——docker-desktop-user-distro 挂成 0 字节空文件（正常 23,219,968 字节），对空文件 exec → 集成无限弹窗 "unexpectedly stopped"；仅重启 GUI 不够（backend 不随 GUI 复活、坏挂载陈旧保留）。新增 internal/dockerwsl 包 + App 绑定（GetDockerWSLHealth / SelfHealDockerWSLIntegration）+ 环境检测页卡片：健康快照（挂载字节数/状态、引擎就绪与版本、docker-desktop 工具发行版、Desktop 进程计数、问题清单、自愈建议）；一键自愈固化实证序列（① 全量退出 Docker Desktop/com.docker.backend/com.docker.build 并轮询计数归零 → ② `wsl --terminate docker-desktop`（仅工具发行版，Ubuntu 与 CodeBox 会话零影响）→ ③ 全新启动 → ④ 等引擎就绪（300s 冷启动预算/5s 间隔，terminate 后重启 ≈5s）→ ⑤ 复核挂载字节数与 /usr/bin/docker 注入），同步返回分步回执与前后健康对照。真机冒烟（AMAGI_DOCKERWSL_SMOKE=1 门控）实测探针链全对（挂载 23,219,968 字节、engine 29.2.0、UTF-16 解码/CSV 计数均正确）。配套：platform 新增 WSLDistroStates（含 docker-desktop 保留发行版的 STATE，复用 wsl -l -v 单次探测与缓存）。

- **Pi 配置页适配 amagi-pi v2.8.2 `mcp.leader`（Provider Center → CLI 专属配置）**：MCP 路由卡片新增「Leader 服务器（mcp.leader）」列表编辑，与 mcp.default 同步升级为三态选择器（全部（未设置键）/ 白名单（列表）/ 不加载（显式空数组 []）），可精确区分 amagi-pi 中语义不同的「未设置 = 加载全部」与「显式空数组 = 不加载任何」；逐模式提示语与插件语义对齐（leader 装配时读一次需重启 pi 生效 / default 每次派发即时生效），卡片 badge 计数按模式计（all=0 / none=1 / list=条数）。实现抽纯函数模块 `mcpListMode.ts`（读取侧非法形态降级 list 不破坏数据；写入侧 all 删键、mcp 删空连键清理不留空壳）+ 测试 9 条。同步修正 mcp.default 空态文案错误（原「未设置默认不启用任何 MCP」与 amagi-pi 实际语义相反——未设置 = 加载全部）。
- **Pi 配置默认并发数预填 10**：可视化模式加载时若 concurrency.default 未设置则预填 10（仅 initialLoad 一次，JSON 模式编辑/模式切换不重复注入；保存落盘，用户可清空保存移除键、回落 amagi-pi 内置默认 4）；输入框占位与提示同步更新。

- **已登记远程主机管理入口（编辑/重新配对/移除）**：桌面端互联此前只在主机切换菜单提供「添加主机…」，`RemoteClientUpdateHost`/`RemoteClientRenameHost`/`RemoteClientRemoveHost` 三个绑定与 API 封装全部就位却零调用——已配对主机地址变更、换机重建、凭据丢失（登记簿 deviceId 残留但 Keychain 凭据缺失，表现为「状态灯可达却连不上」）后既无法更新配对码也无法删除重建。新增 `HostManageDialog`（主机切换菜单「管理主机…」进入）：行内编辑显示名与地址（地址变更提示重置配对态；若改的是当前连接主机先回本机避免死远程视图）、重新配对（`PairingWizardDialog` 新增 `prefill` 预填模式，`UpsertPaired` 按 hostPort/deviceID 回写原条目、显示名保留，配对完成自动连接）、移除（PG-06 危险确认，`ForgetHost` 同步清理本机 Keychain 凭据；对方 CodeBox 不受影响）。store 新增 `renameHost`/`updateHostAddress`/`removeHost` 动作与 `pairingPrefill` 状态。

## [1.3.90] - 2026-09-25

### Fixed

- **WSL 会话透传用户自定义环境变量（envvars.json）**：终端支持 WSL 后，经 WSL 启动的会话（CLI 在发行版内原生运行）读不到 CodeBox 配置的自定义环境变量（如 TAVILY_API_KEY / FIRECRAWL_API_KEY）——注入链在 Windows 侧经 `MergeWithSystem` 完成，而跨界只依赖 `appendWSLENVForwarding` 的前缀白名单（ANTHROPIC_/OPENAI_/CLAUDE_/CODEX_/PI_/代理），自定义键全部被边界拦截。修复：`ResolveRequest` 新增 `EnvForwardKeys`（app 层注入 envvars.json 键名，`EnvVarsService.Keys()` 只暴露键名不物化值），WSL 分支把清单并入 WSLENV 转发；保留键（WSLENV/PATH/PATHEXT）与缺席键不转发，Windows 侧启动路径不变。新增 wsl_test 覆盖（额外键转发/缺席键不列名/保留键拒绝/大小写归一）。

## [1.3.89] - 2026-09-24

### Added

- **移动端 Web 平面链接跳转桥（amagi:open-url）**：pi webui 嵌入态点击消息流内 http(s) 链接时上抛的跨仓冻结契约消息（`amagi:open-url`，与桌面端 1.3.87 同源），移动端此前完全缺失 message 监听——点击无任何反应。新增 `mobile/src/lib/openUrlBridge.ts`（`extractCapabilityToken`/`parseOpenUrlMessage` 守卫纯函数：type 严格匹配、capability token 与 iframe fragment 严格相等、http(s) 白名单、URL ≤2048，常量与桌面冻结值一致）+ `WebPlaneView.vue` 接线（onMounted/onBeforeUnmount 生命周期窗口监听，含 `?skin=light` 形态提取）。外链经 `window.open(url,'_blank','noopener,noreferrer')`——Capacitor 8.2.0 Android WebView 未 `setSupportMultipleWindows`/未覆写 `onCreateWindow`，`_blank` 经 `BridgeWebViewClient.shouldOverrideUrlLoading` → `launchIntent` → `Intent.ACTION_VIEW` 交系统浏览器（零新增依赖；`@capacitor/browser` 因本仓无 Android 平台工程会 reject，不采用）。测试 14+2 例正负成对（含 2048/2049 边界、卸载后监听移除）；真机落点见报告手验清单。

## [1.3.88] - 2026-09-24

### Fixed

- **远程会话列表对齐桌面端（仅活动会话 + 启动时间稳定排序）**：authority 投射层过滤已关闭会话，远程大厅仅呈现 running/stopping 状态（对齐桌面侧边栏语义）；排序由活动时间改为 startedAt 降序 + sessionId 升序 tiebreak，消除轮询活动跳变导致的列表乱序。移动端大厅 store 与用例同步对齐服务端列表语义，卡片渲染保留关闭态分支兼容旧版本宿主。
- **桌面与远程会话列表排序统一为时刻比较（谛听审核 findings 全部处置）**：桌面 `List()` 去除 RFC3339 字符串二次排序，与远程 `ListRemoteSafeSnapshots` 共用同一 comparator（时刻比较 + sessionId tiebreak）——消除跨 DST 偏移两端顺序相反与同秒并列任意序（审核 M1，已复现并回归锁定）；契约钉死 GET /sessions 排序语义为时刻比较（M2）；移动端空态文案改为「当前没有打开的会话」并同步 4 个 e2e 文件 5 处断言（M3）。session 层新增 DST 分叉/同秒并列回归测试，adapter 层补 exit/stop 双路径覆盖（I2）；谛听对抗审核报告归档 `agent-outputs/audit-remote-list-2026-09-24/`（PASS_WITH_MINOR）。

## [1.3.87] - 2026-09-22

### Added

- **Web 平面链接可点击跳转（跨仓配套 amagi-pi v2.7.4）**：pi 会话 Web 平面 iframe（sandbox 无 allow-popups）内 markdown 链接的 target=_blank 被静默阻断——新增 webui → 宿主 `amagi:open-url` 消息桥（与既有 insert-input 同构的反向通道）：WebPlaneHost 监听 message，`parseOpenUrlMessage` 纯函数守卫（token 凭证与构造 iframe URL 时严格相等 + http(s) 白名单 + 2048 长度上限），通过后经新增 App 绑定 `OpenExternalURL` 打开（Go 侧 `ValidateExternalURL` 二次白名单：仅 http/https 且带 host，纵深防御；`wailsRuntime.BrowserOpenURL` 走系统默认浏览器）。前端封装 `api/webui.openExternalURL` 经运行时注入的 window.go 绑定直调（零 wailsjs 再生成依赖，旧二进制无绑定时 reject 降级）。测试：`app_open_external_url_test.go`（正负成对 13+1 例）+ `frontend/src/__tests__/components/terminal/openUrlBridge.test.ts`（5 例：凭证/type/scheme/长度/字段类型）。

## [1.3.86] - 2026-09-22

### Added

- **额度查询拓展：OpenCode Zen Go 与 OpenRouter 家族接入**：`DetectQuotaFamily` 新增 `opencode-zen`/`openrouter` 两家族判别（host 锚定匹配，防中转/仿冒域内嵌子串误判）。Zen Go 探测 `{base}/usage` 三窗口全量映射（滚动 primary/周 secondary/月 tertiary——`QuotaWindow.Kind` 值域新增 `tertiary`，reset 时刻统一转换）；OpenRouter 主跳 `/credits` 出总额/已用/剩余（`QuotaBalance` 新增 `Used/Remaining` 指针字段表达存在性），主跳失败降级 `/key` 口径标注不硬造总额。额度卡新增第三窗口渲染槽与余额明细行，strip 摘要余额口径走 headline、降级形态拼「已用 X」；no_plan 文案通用化「该 Key 未开通对应套餐」。设计与实现报告归档 `agent-outputs/2026-09-22-quota-ext-zen-openrouter/`（含谛听 quick 复审 PASS_WITH_MINOR 与 2 项 Minor 修复记录：判别 host 锚定回归、strip 降级口径）。

## [1.3.85] - 2026-09-22

### Fixed

- **桌面与远程 Web 控制权冲突根治（对称接管）**：桌面内嵌终端输入是 take-first（每次按键部会 TakeDesktop 抢占设备持有者），且桌面持有无空闲超时/自动释放——此前在桌面终端敲任意一个键后，远程设备（终端平面与 pi Web 平面）即被永久锁死：输入 403 `control.forbidden`、acquire 409 `control.busy`，只有桌面手点「收回控制」才解锁，形成单侧死锁。修复：设备 acquire 撞上桌面持有时镜像 TakeDesktop 执行对称接管（reason takeover，旧持有者侧事件可见，在逾桌面操作被 fence，holderGeneration/controlEpoch 照常推进，stale 操作 fail-closed）——最后意图方持有，桌面下一次按键瞬时夺回、设备 acquire 随时接管，双方都无永久锁死；设备间互斥（DenyBusy）与桌面权威收回（ForceReleaseControl）/撤销/远程总开关门禁不变。契约文档端点表与变更记录同步。

## [1.3.84] - 2026-09-21

### Added

- **桌面端主动收回远程设备控制权（设置 > 远程访问 > 控制权管理）**：列出当前持有会话控制权的远程设备（会话 ID/设备名/连接中·宽限期徽标/持有起点），一键收回（二次确认）——收回链 TakeDesktop→ReleaseDesktop 保证被收回设备实时收到 takeover→released 事件且终态无人持有（可立即重新接管）；无人持有时幂等无副作用；枚举面不泄漏 token/内部指针。

### Fixed

- **移动端接管 409 冲突文案透出持有者**：「控制权冲突：另一设备刚取得控制权」在多配对身份并存（重配对产生多个身份）场景误导排障——现在优先透出权威快照持有者名（「{设备名} 正持有控制权，请先在该设备释放或稍后重试」），快照未达时回落通用文案。
- **控制权 acquire 全链可观测**：acquire 成功/冲突记 INFO 日志（请求者/持有者/持有状态）——「无人控制却 409」类争议从此日志一眼定位。

## [1.3.83] - 2026-09-20

### Fixed

- **远程 Web 会话平面输入报「invalid protocol response」（三层根治）**：远程设备在 pi 会话 Web 视图输入被写面控制门拒绝（403 control.forbidden，设备未持控制权）时，代理错误体为 v1 REST 形状（无 `v:1`），amagi-pi webui 前端误判协议违规并断开 WS（「连接中断 · 重连中」），真实错误码被完全掩盖。① 代理 iframe 数据面错误统一注入 `v:1`+`error:<code>`（新增 `writeWebUIPlaneError`/`webUIProxyEnforceAuth`，v1 REST 面零污染，安全红线保持）；② 移动端 Web 平面视图新增控制权引导条（未持控制权时显示「接管控制」按钮，显式触发，无自动抢占）；③ 鉴权镜像四分支与 ErrorHandler ACAO:null 补表驱动 parity 锁。amagi-pi 侧配套（错误信封分流+真实错误码文案映射，v2.6.5）同步发布。

## [1.3.82] - 2026-09-19

### Fixed

- **重置时刻显示不准确（大多「即将重置」）**：Codex 本地会话数据的 5h/周窗 `resets_at` 经常已过期，旧文案落「即将重置」分支误导。`formatResetCountdown` 重写为绝对时刻优先：过期 →「重置时刻已过」（诚实陈述）；未来按自然日分档「今日 HH:mm / 明日 HH:mm / 7 天内周几 HH:mm / 更远 M月d日 HH:mm」。
- **Web 视图 / strip 常显重置时间**：新增 `formatResetShort` 短形式，会话额度条常驻摘要附主窗重置时刻（「GLM · 93% 5h · 今日 22:35」）；Popover 弹层内双条重置文案同步更新。

## [1.3.81] - 2026-09-19

### Fixed

- **GLM 窗口语义错位（max 套餐实测）**：max 套餐无周额度（仅 5h 额度 + MCP 月额度），旧解析把 `TIME_LIMIT` 首条当 primary 标「5h」、其余标「周」——MCP 月额度被当 5h、5h 被当周。带凭据实证真实响应：`TIME_LIMIT + unit=5(月) + usageDetails[MCP 工具明细]` 为 MCP 月额度、`TOKENS_LIMIT + unit=3(小时) + number=5` 为 5h 主额度。重写 `glmQuotaWindows`：primary 改 `TOKENS_LIMIT` 优先（无则首条兜底）；`QuotaWindow` 新增 `label` 字段由实证枚举推导（"5h"/"月"/"MCP·月"），前端 `windowLabel`/`windowShortLabel` 优先展示；解析 `nextResetTime`（毫秒→秒）修复重置时间一直缺失。实证样本回归测试。

## [1.3.80] - 2026-09-19

### Fixed

- **TUI 额度条常显「额度获取失败」（双根因）**：① pi/omp/codex 会话的 `SessionInfo.Provider` 存的是 AppType 字面量（如 `"pi"`），真实 provider 名在 `Preset` 字段——额度条拿 `"pi"` 探测命中后端「provider 不存在」error（该分支不落盘不留日志，现场磁盘/日志均无痕），前端写入内存后常显失败文案；新增 `sessionQuotaProvider()` 按 appType 分流解析（codex 空 preset 回落订阅固定键），五组回归测试。② xterm 由 JS 后 append（DOM 序在额度条之后），flex 列把条顶到终端左上角；`order:10` 沉底修复。

## [1.3.79] - 2026-09-19

### Fixed

- **会话额度条覆盖 TUI 平面**：v1.3.78 的额度条只挂在 Web 平面（WebPlaneHost 内），而 pi 会话默认落在 TUI 平面，主用场景启动会话后看不到按钮。组件更名 `SessionQuotaStrip` 双平面复用：TUI 实例挂 term-body 底部（仅 pi 会话，pi CLI 输入区下侧），与 Web 平面实例 v-show 互斥、同源 store；term-body 转 flex 列布局（xterm `flex:1`，非 pi 会话布局不变）；`watch(isPiSession)` 显式 refit 兜底 xterm 容器高度变化（term-body 整体尺寸不变、ResizeObserver 不触发）。

## [1.3.78] - 2026-09-19

### Fixed

- **GLM 额度解析失败（线上回归）**：真实端点 `limits[].unit` 为数字形态（zcode 侧本就按 `typeof unit=="number"` 消费），v1.3.77 的 Go `string` 声明导致整包反序列化失败（`json: cannot unmarshal number into ... limits.unit of type string`），GLM 卡片落 error 态。`Unit` 改 `flexString` 宽松类型（接受 string/number/bool/null），补三形态回归测试。

## [1.3.77] - 2026-09-19

### Added

- **额度查询与常显（GLM / DeepSeek / Codex 订阅三家族）**：后端新增额度探测——GLM Coding Plan 走 `{origin}/api/monitor/usage/quota/limit`（zcode 逆向实证，`authorization` 头直用已托管 key；TIME_LIMIT 为主窗口，`code=1001`→未配置、无套餐关键词→no_plan）；DeepSeek 走 `/user/balance`（CNY 余额）；Codex 订阅纯本地读 `~/.codex/sessions` 最新 rollout 尾部的 `rate_limits`（5h/周双窗口 + credits，不触碰 auth.json）。结果落 `models.json` `provider_quota` 字段（值域零凭据），App 暴露 `GetProviderQuotas`/`ProbeProviderQuota`/`ProbeAllProviderQuotas` 三绑定，启动 30s 延迟自动探测 + 10min 成功冷却 + in-flight 单飞。前端：`/usage` 分页化为「使用统计 | 额度查询」（原 808 行统计页完整迁移）；额度页四类卡（GLM 双域/DeepSeek/Codex 订阅/其他提供商聚合灰卡），窗口型双条阈值变色（70%/90%）、余额型 ¥ 排版、六态状态矩阵与重置倒计时；pi Web 平面会话窗口输入框下侧新增 30px 额度条（迷你摘要常驻 + 点击向上弹 340px Popover + 单飞刷新）。经 diting 合并审核：修复 GLM family 按 origin 推导（z.ai 卡不渲染）与首探「查询中」占位短路。
- 设计文档：Database/01-AI技术与工具/codebox优化/正式化改造/额度管理；审核报告：agent-outputs/2026-09-19-quota-review/。

### Fixed

- **全局 Toast 迁移至右下角**：操作提示此前固定右上角，物理遮挡页面头与卡片右上角的按钮（清理/检测等高频操作首当其冲），且与 UpdateReminder 互相叠加；迁移后动画改自底边滑入并补充列表重排平滑过渡。

## [1.3.76] - 2026-09-16

### Added

- **视觉能力标记与识图清单导出拆为两个控制项（契约 v1.5）**：`TerminalPreset` 新增三态 `vision_export` 字段——缺省/true 保持既有行为（标记 Vision/Video 即导出到 `~/.agents/amagi-media-models.json`，向后兼容）；显式 `false` 时该预设不进 amagi-media-understanding 清单，但 Vision/Video 标记继续驱动 pi/omp 托管条目的 `input` 声明与 amagi-pi 守卫放行。预设编辑弹窗视觉能力区新增「导出到识图 / 识视频清单」开关（默认开，关闭时仅写 `vision_export: false`）。配套测试：config 导出器三态用例（nil/true 导出、显式 false 排除）+ launcher 回归用例（`vision_export=false` 不削弱能力标记）；`docs/vision-export-contract.md` 增补 v1.5 修订段，`docs/user/providers.md` / `usage.md` 同步。

## [1.3.75] - 2026-09-15

### Fixed

- **远程创建的 pi 会话无 Web 平面（恒 unknown）**：remote v1 创建/重启的 embedded pi 会话此前不走桌面 LaunchPiSession 的 webui 注入路径，`AMAGI_WEBUI_PORT/AMAGI_WEBUI_TOKEN` env 缺失、tracker 从未注册——`/session/{id}/webui` 恒 unknown，Web 平面视图无从出现。launch executor 新增 `webuiLaunchPlane` 窄接口接线（对齐桌面 T-1.5 同源）：pi 会话 Prepare 阶段分配空闲端口与 v1.0.2 capability token 并覆盖式注入 env（宿主残留 `AMAGI_WEBUI_*` 先移除再追加防重复键），启动 commit 后发布 per-session 探测 tracker；分配失败不阻断启动（扩展自选端口写注册表、探测侧回退发现）；omp/codex 会话与未接线（测试）路径不注入不注册。
- **会话退出/移除的 webui tracker 清理**：run-scoped PTY 退出（remote 创建/重启会话的进程退出径）先把 tracker 落 ended 再释放该 run 代共享租约（桌面 embedded 会话各有退出 goroutine 调 Invalidate，幂等）；v1 remove GC 完成后同步 `RemoveSession` 清理 tracker（对齐桌面 RemoveSession commit 点）再释放租约。
- **配套测试**：新增 `launch_webui_plane_test.go` 5 用例（pi 注入注册/omp codex 不注入/未接线静默/Abort 无残留/env 覆盖语义）。

## [1.3.74] - 2026-09-15

### Fixed

- **远程 Web 平面输入台恒 403（「连接中断，消息未发送」）**：sandbox iframe 的 opaque origin 使 Chrome 抑制 SameSite cookie，数据面请求永远带不上 device cookie——此前 capability 兜底通道主体为零，写面控制门恒拒（403），输入台永远发不出去。remote server 新增按（会话， 设备）绑定的代理铸造 plane token（`p`+30 hex，与后端 32-hex token 形状可区分、直达后端 fail-closed）：状态端点（device cookie 鉴权后）改下发 plane token，解析命中即得绑定设备主体，写面控制门据此评估真实控制权归属（持控制放行、未持/他人持有 403，语义对齐 TUI 平面「先获取控制权」）；绑定设备经活动设备库实时复核（吊销/过期 fail-closed），后端 token 轮换自动失效旧 grant，平面终态回收全部 grant，raw 后端 token 不再直接下发（只读兼容旧平面、写面仍 403）；出向 Authorization 与 WS 子协议统一换写为后端 token，后端零改动。
- **opaque iframe 读不到代理错误码**：本地生成的 401/403/503 响应在 `Origin: null` 时携带 `ACAO: null`，确保沙箱 iframe 能读到真实错误码，不再被浏览器掩盖成笼统 network_error（误导性的「连接中断」提示）；成功响应的 CORS 头仍由后端下发（不重复设置）。
- **配套测试与文档**：webui 代理 plane token 铸造/解析/轮换失效/会话回收/设备复核 fail-closed、写面控制门真实主体、ACAO:null 错误可读性等用例；remote-api-v1 契约文档同步。

## [1.3.73] - 2026-09-14

### Added

- **会话 Web 平面配色方向设置（设置→外观）**：新增 `webPlaneSkin`（`dark` 缺省深底浅字 / `light` 浅底深字）——后端 `settings.Service` 持久化（空/未知值 fail-safe 归一为 dark，零值即缺省、老 settings.json 无需迁移）；桌面端新增 Pinia `appearance` store（启动加载一次 + 并发去重）与外观设置页编辑项，`light` 时向 Web 平面 iframe URL 在 fragment 前注入 `skin=light`（webui 命中后叠加 `webui-embedded-light`：浅面板+深字，`#/t=<token>` 契约 fragment 与 token 提取不受影响），非皮肤模式宿主底随方向切换浅底，皮肤模式两方向保持透皮；URL 经 computed 跟随设置即时重载。移动端宿主固定浅色，iframe 恒注入 `skin=light`（老版本 webui 自然忽略该参数，行为不劣化）。
- **配套测试**：Go settings 归一化/持久化用例；前端 `withWebPlaneSkinParam`（fragment 前插入/合并 query/dark 原样）与 appearance store 用例；mobile WebPlaneView 浅色注入与重试/重载路径用例。

## [1.3.72] - 2026-09-14

### Fixed

- **移动端 Web 平面浅字落浅底不可读**：pi webui 嵌入模式（`#/t=` fragment 命中）body 透明 + 浅色文字 + 半透明深色面板，依赖宿主 iframe 背后的深色底；移动端此前用 `--VT-canvas`（#FAF9F5 浅奶油）导致对比度仅约 2:1。背景改用终端深墨面令牌 `--VT-surface-dark` 对齐桌面端 `.term-body` 语义；遮罩/浮层自带实底不受影响。

## [1.3.71] - 2026-09-13

### Fixed

- **移动端 Web 平面视图状态栏异常层强制展开挤占会话内容**：`StatusBar` 新增 `suppressAutoExpand` 压缩视图属性——异常层仅在胶囊行以色调提示、不自动展开明细列表（远程观察场景下控制权/历史缺口为常态，强制展开几乎恒占大量纵向空间）；Web 平面视图（iframe 自带会话状态）传入该属性，手动展开/收起不受限，非 Web 平面视图既有强制展开语义零回归。附 3 个组件测试用例。

## [1.3.70] - 2026-09-13

### Fixed

- **Web 平面「会话已结束」横条在会话恢复后永久残留**：桌面端 `WebPlaneHost` 结束态只置位无复位分支——pi 会话重启/恢复后 status 回 `running`，ended 横条仍永久残留，与 TUI 存活状态矛盾。现在 status 回 `running` 时复位结束态（置位语义保持：非 `running`/`stopping` 才进入结束态，最后画面与 badge 保留行为不变）。

## [1.3.69] - 2026-09-13

### Added

- **远程控制网页视图新增 pi/omp 会话「Web 会话平面」**：pi webui server 仅 bind `127.0.0.1`（移动设备物理不可达）且页面基址冻结为 127.0.0.1，远程端此前只能靠终端仿真/时间线查看 pi/omp 会话。remote server 新增顶层 `/webui/{sessionID}/...` 反向代理：出向改写 Host、剥离 Origin、覆盖式注入 `Authorization: Bearer {token}`、透传 WS upgrade（`Sec-WebSocket-Protocol`）；鉴权与 v1 一致（device cookie，SameSite=Strict），sessionID 字符白名单防路径穿越，`/api/input` 代理请求受会话控制门约束（无控制权 → 403）；capability token 仅经 URL fragment 下发（不进 query/日志），代理对未学到客户端凭证即删除。
- **remote v1 契约扩展状态端点**：`GET /api/remote/v1/session/{id}/webui` → `{state: probing|available|unavailable|ended|unknown, url?}`（available 时 `url = /webui/{sid}/#/t={token}`）；以加性扩展路由实现，冻结的 10 端点 manifest 与 fixture 不动，扩展路径走同一中央门序（Host → 空 query → method → origin → auth）。
- **mobile 端 Web 平面视图**：新增 `WebPlaneView`（iframe 沙箱、加载/错误/10s 看门狗/结束态状态机）与 `useSessionWebUI` 轮询 composable（probing 800ms → available 5s 监测、生命周期安全清理）；WorkspacePage 为 pi/omp（isTuiCli）会话实现默认视图策略（available → Web 平面、probing → 加载态、不可用回落终端仿真），`?view=webplane` 显式指定、菜单三面切换、Web 平面内隐藏外层输入台；非 TUI 会话与既有 `?view=terminal|timeline` 语义零回归。
- **配套测试**：Go 侧代理头改写/鉴权/控制门/token 不外泄、契约校验、冻结清单锚测试；mobile 侧 api 封装、composable 生命周期、视图状态机与集成策略 4 个测试文件（46 用例）。

## [1.3.68] - 2026-09-11

### Fixed

- **Web 平面插入工作路径真机全部被拒（契约 v1→v2）**：v1 接收端误用 `event.origin === 'null'` 识别宿主，但 `event.origin` 是发送方（宿主壳 `http://wails.localhost`）origin 永远非 `'null'`，导致真机上所有插入指令被拒。宿主→webui 桥升级 v2：消息携带 capability token（从 iframe URL fragment `#/t=<token>` 提取，形状与 amagi-pi webui transport `CAPABILITY_PATTERN` 一致），接收端以「token 与自身 fragment 严格相等」校验来源，与 origin 语义解耦；token 缺失/非法时宿主侧直接不投递。

## [1.3.67] - 2026-09-11

### Added

- **Web 平面快捷菜单插入工作路径**：路径选择确认后按平面分流——Web 平面经 `WebPlaneHost.postToFrame` 以 postMessage 注入 webui 页面输入框（iframe 未就绪/加载失败时返回 `false` 静默不抛错），TUI 平面保持原 `insertTextToTerminal` 行为零变化；快捷菜单禁用条件收敛为仅会话非 `running` 时禁用，运行中的 Web 平面提示「所选路径将插入网页对话框」。宿主→webui 桥为 sandbox iframe 单向插入指令通道，安全由接收端 `webui-embedded` 嵌入态 + `event.origin === 'null'` 双重守卫兜底。

## [1.3.66] - 2026-09-10

> 远程 Web 控制界面交付批次（调研报告见 `agent-outputs/execution-plan/20260910-remote-web-control/`）。全部变更经过 diting 阶段审核（PASS_WITH_MINOR，0 Critical/0 Major）。

### Added

- **移动端 pi/omp 终端仿真面（P1-A/P1-D）**：pi 与 omp 是全屏 TUI，行式时间线无法保真呈现——工作区对这两类会话默认切换 xterm 终端仿真视图：ANSI/全屏重绘/`\r` 覆写保真渲染且重绘帧不重复堆积；按键托盘（Esc/Tab/Ctrl+C/Ctrl+D/Ctrl+L/Enter/方向键 + Alt 前缀 ⌥W⌥T⌥R）全部过控制权过滤不扩权；控制权被占时自动降级只读横幅；断线回补缺口提示行不伪造内容；attach 历史采用 store 级「订阅即回放」原子衔接，任意时序不丢不重。其余 CLI 仍默认结构化时间线。
- **远程服务自检卡（P3-A/P3-B）**：设置→远程控制新增七项自检（运行状态/监听地址端口/端口占用/安全迁移门禁告警/宿主探测降级提示等）与负例可见反馈；配对卡在监听通配时支持手动输入或候选局域网地址（新增 `App.ListLocalLanAddresses` 绑定，私网 IPv4 优先、含接口名/掩码、VPN 网卡标注不硬滤）生成二维码；移动端访问地址一键复制。
- **远程启动自愈（P3-B）**：`Server.StartWithRetry` 有界退避重试（占用型瞬时冲突自愈）；耗尽后不翻动 enabled、固定告警并透出 enabled/lastStartError/running 漂移三元组；`GetRemoteStatus`/`GetRemoteWebUIStatus` 新增 `hostSummaryDegraded` 透出。

### Changed

- **大厅展示全部会话（裁定 A）**：去掉 running-only 过滤，已停止/已退出/不可用会话以状态徽标呈现（用户需看到已停止会话才能回看/重启/清理）；运行中计数独立统计。
- **HostSummary 探测失败降级（P2-B，不动 wire）**：`GET /api/remote/v1/host/summary` provider 失败时降级返回保守摘要（全 CLI 不可启动、`serverVersion: "unknown"`）而非 503 阻断整个 v1 读面；降级方向保持失败关闭（探测不到绝不报可启动）；配对链路仍 503 fail-closed（有测试锁死）。

### Removed

- **移动端三死页与 legacy 通道清退（P2-A）**：Dashboard/Providers/Settings（Bearer+loopback 必死页）下线重定向到引导页；TerminalPage（2576 行，依赖已断供的 legacy `/ws/terminal`）与 `mobile/src/api/`（Bearer client/旧 WS）物理删除；Settings 轻量化为 v1 契约驱动的引导页。

### Fixed

- **e2e 遗留失败全量收复（P2-C/P2-D）**：HEAD 基线 44 条遗留失败清零（最终 285 绿/0 失败/1 条件跳过）——sessions-pg02 五用例夹具迁移到两步启动器、真链组夹具对齐、a11y 双契约（44px 触控/窄视口几何）修复；KeyTray 按钮提升至 44×44 触控标准、托盘与 Composer 行改 wrap 布局，窄视口不横向溢出。
- **观察态降级用例间歇抖动**：全量并行负载下 xterm 动态 import 挂载链晚于首个 flushPromises——断言改 `vi.waitFor` 轮询（连续 4 轮 611/611 稳定）。

## [1.3.65] - 2026-09-05

### Fixed

- **思考档位下拉只显示两档（thinkingLevelMap 键 ≠ pi 支持集）**：原 `extractThinkingLevels` 只取 thinkingLevelMap 键——gpt-5.6 系列 map 只有 xhigh/minimal 两键，下拉只显示两档；而 pi 运行时语义是「标准档（off/minimal/low/medium/high）默认支持 + xhigh/max 需显式映射」（clampThinkingLevel）。抽取函数复刻 pi `getSupportedThinkingLevels` 语义（reasoning=false 仅 off；标准档缺键默认支持；xhigh/max 缺键不支持；null 显式排除；omp levels 数组形态不变），所有 provider 的档位展示与 pi 运行时一致。
- **契约文件 modelPatches 合并**：amagi-pi 对基础模型的档位补全（如 gpt-5.6 补 max→xhigh 别名）经 modelPatches 下发，codebox 整体替换 thinkingLevelMap 后重算档位集。

## [1.3.64] - 2026-09-05

### Fixed

- **Provider Center 下拉目录看不到 amagi-pi 前线模型（如 gpt-6-astra）**：amagi-pi 在 pi.dev 官方目录未收录时经 refreshModels 运行时注入的新模型不落盘 models-store.json，静态目录与 pi 运行时目录脱节。新增合并 amagi-pi 前线契约文件（`~/.pi/agent/amagi/data/codex-frontline.json`，生效时落盘/官方收录后退位删除）：`GetPiModelCatalog` 宽容合并（同 id 已存在则官方优先跳过；文件缺失/坏 JSON 静默）；provider 不在目录时补 builtin 条目。测试：frontline_contract_test.go（合并去重/负例）。
- **models.json 缺失时内置目录整体丢失（既有缺陷）**：`GetPiModelCatalog` 两处提前返回（文件不存在 / 无 providers 键）跳过 builtin 合并——纯 OAuth 用户（无 models.json）下拉里看不到 openai-codex 等内置 provider。改为空 providers 继续，builtin（models-store.json）与前线契约照常合并。
- 修复 `mergeFrontlineContract` range 值副本修改不回写的 Go 坑（索引取址）。

## [1.3.63] - 2026-09-03

### Added

- **终端快捷功能支持工作路径快速输入**：终端工具栏新增数据驱动的快捷功能菜单，支持目录树多选并组装工作路径文本写入终端（目录级选中可直接插入工作路径）；终端引擎新增 `insertTextToTerminal` 方法，感知 bracketed paste 模式自动包裹多行避免提前执行。
- **路径服务新增 `ListDirectories` 接口**：单层子目录只读遍历（仅目录、跳过 `.` 开头、大小写不敏感排序、上限 500 条超限截断返回 `truncated`），root 为空回退用户主目录，文件系统根的 parent 为 `null`；配套前端 api 包装层、目录选择弹窗（`PathPickerDialog`）与模型层（`pathPickerModel`）单测覆盖。
- **多款模型定价种子补全**：用量统计扩充 Claude 5 / GPT-5.4 / DeepSeek V4 / Kimi K3 等模型官方单价种子，并在启动时重算历史成本。
- **版本依赖同步**：frontend 1.3.50 → 1.3.57，同步更新 WSL 搜索工具与路径服务 Wails 绑定定义。

## [1.3.62] - 2026-08-31

### Changed

- **Pi amagi 并发限制行键支持目录下拉选择与自定义切换**：providers/models 行键从纯文本输入改为标准下拉单选（候选项 = 模型目录 ∪ 已配置 agent 模型 provider/spec ∪ 已配置并发键，去重 localeCompare 稳定排序），支持「＋ 自定义服务商/模型…」入口与「从列表选择」双模平滑切换；行键重命名、行级稳定渲染 ID 与自定义标记联动。
- **并发限制输入与保存收口逻辑抽取为纯函数模块**：新增 `frontend/src/components/provider/piConcurrency.ts`，`normalizeLimitInput` 统一归一化 `number|string|null|undefined` 输入（编辑中间态安全保留、不抛 TypeError、不删键不删行），`cleanConcurrencyConfig` 集中收口保存契约（空值剔除、正整数转换、空对象修剪、顶层 `concurrency` 删除），`buildProviderDropdownOptions`/`buildModelDropdownOptions` 构建下拉选项集合；配套新增 10 个单元测试用例，输入退格清空不再丢行失焦。

## [1.3.61] - 2026-08-31

### Added

- **Pi amagi 配置支持并发限制面板**：Pi amagi 配置新增 `concurrency` 可视化编辑与快捷补全，执行 CLI 前自动确保 agent 根目录存在；`agentProfile`/`provider`/`remote`/`wslsetup` 前端 API 与类型随绑定同步适配，配套 `useModelCatalog` 单测。
- **用量统计图表卡片合并**：用量页将趋势、模型占比、供应商占比与用量分布聚合为单卡片，支持分段控制器平滑切换；供应商对比与模型 Token 分布统一重构为环形图呈现，移除柱状图冗余依赖并优化空间占用。
- **终端会话优先网页平面**：pi 会话且 Web 插件可用时默认优先切至网页平面，支持显式切回终端后的防打扰锁定；完善组件挂载与路由激活守卫，后台或非活动会话不触发平面抢占。

### Fixed

- **多模块并发与资源生命周期加固**：envcheck 引入 `probeMu` 互斥锁保护 npm/python 运行时可用性探测缓存，消除并发检测数据竞争；修复已删除会话在 `TrackTitle` 中因空状态继续轮询导致的 goroutine 与磁盘 IO 泄漏；日趋势查询按币种独立汇总防止成本混加，模型价格 Upsert 与快照导入统一归一化 Pattern；修复 macOS PTY 尺寸读取竞态、自动更新解压后即时清理临时安装包、Codex 插件防御空白命令 panic、FrameBatcher 取消调度泄漏、跨数据块 CRLF 累加解析与 QR 扫描器卸载竞态。

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
