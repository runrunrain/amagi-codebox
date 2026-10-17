# WSL 配置供给体系历史升级缺陷审计（full / 初始审计）

VERDICT: FIX

本结论仅覆盖任务声明的供给链。发现 **16 Major、2 Minor**；没有将历史 PASS 当成本轮证据，也没有签发 Windows/WSL 真机通过结论。最先应修复身份/授权、配置丢失与秘密落盘边界，再闭环 UI → 实际消费者的主路径。

## 1. 基线、输入与方法

- 审核基线：`16f707d^..58e75774dec5a16acc5734d859f3c96a982827a8`（包含 16f707d）。当前 HEAD 为该终点；审核结束时目标产品文件相对该提交无 diff。
- 必读且已完整读取：
  - `agent-outputs/workflow/wsl-goal-closure/workflow.md`
  - `agent-outputs/workflow/wsl-session-friction/workflow.md`
  - `agent-outputs/workflow/wsl-session-friction/p3-config-supply-design.md`
- 历史归因：S0 `0cf7591`，S1 `b9bb097`，S2 `bd93f4c`，S3 `fa2cb4a`，S4 `6b015b8`，S5 `e1a6b13`。以下问题均在本轮升级新增实现/接点中证实；不是把范围前既有问题改称回归。
- 方法：先完整盘点包文件，再从授权、身份、失败与消费者反例回溯；生产组件逐个覆盖，测试按相关行为抽读并执行包测试。full 不表示每个测试文件逐行人工审阅。
- 本地环境：macOS。复现使用合成数据、注入探测面、`os.MkdirTemp`，结束删除临时目录；不读取真实用户 auth/API key，不访问或写真实 distro。没有写产品 patch、正式测试、提交或推送。

## 2. 完整文件清单与覆盖

`internal/wslsupply` **41 文件：23 生产文件、18 测试文件**，没有遗漏独立组件。逐文件行数与历史提交见 `supply-audit-contract.log`。

生产文件（均检查其实际职责与调用关系）：

```text
a2.go                  claude_adapter.go       codex_adapter.go
compensation.go        coordinator.go          keyed.go
managed_models.go      pi_config.go            pi_shapes.go
plugins.go             plugins_api.go          probes.go
projection.go          resources.go            runner.go
shared_files.go        skills.go               skills_scan.go
skills_submit.go       state.go                toml_edit.go
transform.go           types.go
```

测试文件（18）：

```text
claude_config_test.go        codex_config_test.go       coordinator_test.go
pi_config_test.go            plugins_test.go            projection_test.go
resources_test.go            s1_adapters_test.go        s1_e2e_windows_test.go
s1_fix_round_test.go          s3_skills_e2e_windows_test.go
s4_e2e_windows_test.go        skills_scan_test.go        skills_test.go
submit_primitive_test.go      toml_edit_test.go          transform_test.go
wsl_supply_e2e_windows_test.go
```

包外：`app_wsl_supply.go`、`app_wsl_supply_s4.go`、`app_wsl_plugins.go`；launcher 的 `wsl_agent_config.go`、`wsl_pi_login_sync.go`、`wsl_managed_models.go`、`wsl_opencode_mcp.go` 与 provider reconcile 接点；`app.go`、`pi_login_launch.go`、`launch_planner.go`/`launch_executor.go` 定向接点；platform identity/Home/UNC；前端 `WSLCLISettings.vue`、`PiPluginPanel.vue`、`PiPluginWSLSection.vue`、占位目标面、`api/wslSupply.ts`/`api/piPlugin.ts`；Wails App.js/d.ts/models 的相关方法/DTO。

## 3. Findings

证据等级：**R** = 本机以合成 fixture 运行真实产品 API 复现；**S** = 实际代码路径证实，未进行 Windows 真机复现。统一复现脚本 `supply-audit-repro.go` 是审核取证工具，不是正式项目测试。

### SA-01｜Major｜Apply 的身份复核不比较 generation，旧授权计划可写入新身份

- 定位：`internal/wslsupply/types.go:150–153`（SameIdentity）；`coordinator.go:330–345, 518–521`。
- 触发：Plan 后同名 distro 被替换，UID/Home/根路径相同但 machine-id 改变；或请求/快照 generation 不同而其余字段相同。
- 失败行为/影响：SameIdentity 不比较 Generation，Apply 接受旧计划；投影写入阶段也不重做 consent 门。旧环境的授权可把用户设置/敏感 MCP 导入新环境，违背“代际改变拒绝旧计划”。
- 证据 **R**：`GENERATION_SWAP outcome=applied detail=written`。脚本在授权和 Plan 后只改变身份探针的 machine-id，真实 Apply 仍写入。
- 最小修复/回归：比较完整身份（含 generation），未知稳定身份不得当作相同已授权身份；提交前重验授权。覆盖计划后换代、授权后换代、相同 UID/Home 的同名重装。

### SA-02｜Major｜状态按 unit/插件 identity 单槽保存，不按 distro 隔离；unknown 会跨目标继承密钥授权

- 定位：`coordinator.go:115–120`；`state.go:231–271`；`plugins.go:625–666`；`coordinator.go:754–785`。
- 触发：①两个最小 distro 都没有 machine-id（generation 都是 `unknown`）；②导出/克隆 distro 保留 machine-id、UID/Home；③正常不同代的 A/B 交替使用。
- 失败行为/影响：entryFor 只比较 generation、不比较 distro；generationFor 也不含 distro。A 授予视觉密钥共享后，B 可无同意收到副本。正常不同身份切换时则用 B 登记覆盖 A，返回 A 丢失授权和跟随基准；插件登记同样只有一个 identity 槽，不能同时维护各目标。
- 证据 **R**：`UNKNOWN_CROSS_DISTRO outcome=applied (B never consented)`；`RETURN_TO_A outcome=blocked detail=awaiting-consent`。并非只依据 UI 缓存推断。
- 最小修复/回归：清单/插件主键按 distro 身份 + UID/Home + generation + unit/插件 identity 分区；unknown 不复用持久授权。提供保守旧清单迁移；覆盖 A→B→A、克隆、无 machine-id、插件同包多目标。

### SA-03｜Major｜S4 UI 提交完整键路径，后端按叶名选择，导入零项仍虚报成功

- 定位：`frontend/src/views/settings/WSLCLISettings.vue:789–833`（previewCliUnit/importCliKeys）；`app_wsl_supply_s4.go:417–449`；`internal/wslsupply/resources.go:510–549`；`pi_config.go:229–249`、`codex_adapter.go:142–146`。
- 触发：正常从预览勾选 `mcpServers/demo`、`mcp_servers/demo` 或 `profiles/demo` 并点击导入。
- 失败行为/影响：前端传 `k.key` 原串，SetUnitSelection 原串保存；适配器却查 `selected[叶名]`。结果 desired 为空，返回 Fresh；前端失败计数为零，提示“已导入所选”，文件实际不存在。若之前通过其它入口用叶名导入过，错误选择还可能触发原托管条目删除。
- 证据 **R**：`UI_SELECTION outcome=fresh target_missing=true`；Wails JS 真实透传变量，故不是绑定自动纠正。32 个前端测试通过也未覆盖 Vue 选择值到真实后端语义。
- 最小修复/回归：统一规范化键标识，尤其区分同名 MCP/profile，不能简单全局截叶名导致两个作用域同时被选。补 UI 预览→勾选→App→文件内容的贯通测试；零项不报导入成功，并允许空选择显式取消已有导入。

### SA-04｜Major｜Codex profile 整节透传权限与未知字段，突破批准的 LocalOnly 边界

- 定位：`internal/wslsupply/codex_adapter.go:128–154, 471–488`。
- 触发：选择包含 `approval_policy = "never"`、`sandbox_mode = "danger-full-access"` 或其它机器/未知字段的 profile（通过规范叶名 API 或修复 SA-03 后正常 UI）。
- 失败行为/影响：desiredFrom 拿完整 SectionValue，convertEntry 仅做字符串路径转换，没有字段白名单。WSL 后续使用该 profile 时继承禁用审批/扩大 sandbox 的决策；前端却承诺信任/权限/未知字段不跨界。
- 证据 **R**：`CODEX_POLICY outcome=applied unsafe_policy_copied=true unknown_copied=true`。脚本读取真实目标 TOML 验证，不只是计划输出。
- 最小修复/回归：profiles 也用明确可移植字段白名单，权限/信任和未知字段不导入；覆盖 profile 内权限键、嵌套表、目标侧原有权限保留。不得以“用户选了 profile”替代越权字段授权。

### SA-05｜Major｜普通文件别名可实体化被排除的凭据文件

- 定位：`internal/wslsupply/skills_scan.go:345–427`（scanLink）。
- 触发：合法 skill 内 `.env` 被排除，但 `alias.txt -> .env`；同理链接到树内 credentials/secrets 文件或被排除目录里的文件。
- 失败行为/影响：只检查链接名是否在凭据名单，解析后只检查树内/常规文件/Windows 扩展，不检查最终目标的排除规则；把秘密以普通名字加入清单，按 0644 供给到 WSL。S4 资源目录复用同扫描器，也受影响。
- 证据 **R**：`SECRET_ALIAS portable=true alias_in_manifest=true excluded=[.env]`；使用合成 `.env`，没有读取真实秘密。
- 最小修复/回归：解析链与最终路径必须同时执行凭据和排除目录策略；候选读取前重验证路径身份/链接，覆盖直接别名、多跳别名、排除目录、扫描后替换链接。现有“凭据命名的链接”测试不覆盖反向别名。

### SA-06｜Major｜秘密写入阶段没有 Linux 私有 staging，最终 chmod 无法消除已发生的泄漏窗口

- 定位：`internal/wslsupply/coordinator.go:1162–1206`；`internal/launcher/wsl_pi_login_sync.go:215–249`；`wsl_agent_config.go:23–28, 333–360`。
- 触发：目标根/其父目录在 distro 内合法为 0755、默认 umask 为 022（如已有 `~/.agents`）；UNC 创建的临时文件实际 POSIX mode 为 0644。
- 失败行为/影响：先在共享目录创建 tmp 并写入密钥，然后只调用 Windows os.Chmod；直到 rename 后才执行 Linux chmod。代码自己的平台说明已明确 Windows os.Chmod 经 9P 不落 POSIX 位。另一个 Linux 用户可在临时/最终文件收紧前读取秘密；成功返回且最终 0600 也无法撤销泄漏。与批准设计“0700 私有 staging 中生成/提交”相反。
- 证据 **S**：逐调用顺序与仓库记录的平台语义可证；本轮没有 Windows 真机权限实测，不宣称 macOS 0600 可证明此问题不存在或存在。
- 最小修复/回归：先在 distro 内建立并验证 0700 唯一 staging，秘密字节写入前确认隔离，再设 0600/rename/验证；A2/provider/共享导出采用一致原语。真机用合成秘密、第二 Linux 用户观测整个写入窗口，不只 stat 最终文件。

### SA-07｜Major｜敏感单元 chmod 失败直接删除整个原有混合配置，丢失用户自有内容

- 定位：`internal/wslsupply/coordinator.go:1180–1211`，尤其敏感分支 `_ = os.Remove(uncPath)`；`internal/wslsupply/managed_models.go:77`；`pi_shapes.go:158`。
- 触发：已有 models.json 含非 amagi provider/用户字段，或 mcp.json 含用户 MCP；新候选 rename 成功，distro chmod 失败。
- 失败行为/影响：submitFile 已读取前像却不恢复；Sensitive=true 分支删除整文件，连本次未拥有的用户配置也消失。清理失败又忽略错误，只报告 chmod，不能区分宽权限秘密残留；调用方仍可能按普通供给失败继续启动。
- 证据 **R**：`CHMOD_FAILURE outcome=failed preexisting_mixed_config_deleted=true`，fake Chmod 故障复现真实提交原语，原文件含 `local-user` 和 `userSetting`。
- 最小修复/回归：在私有 staging 中安全失败，保住原文件；提交后故障需安全前像恢复、CAS 与明确清理残留结果，不能以删整文件充当回滚。覆盖混合配置、chmod+清理双失败、恢复失败与启动闸门。

### SA-08｜Major｜single-flight 的补推递归永远认为自己仍在运行，随后所有保存推送停摆

- 定位：`app_wsl_supply.go:236–256`。
- 触发：一次 push 执行中发生第二次保存，设置 `rerun=true`。
- 失败行为/影响：defer 清掉 rerun 后，保持 running=true 递归调用 pushWSLSupplyOnce；递归入口又看到 running=true，只把 rerun 置回 true 并返回。外层直接 return，永远不执行 running=false。此后每次保存都只置 rerun，不再执行配置分发，直至进程重启。
- 证据 **S**：无需竞态猜测；上述布尔状态转移是该分支的必然结果。保存风暴恰是 single-flight 要处理的常规输入。
- 最小修复/回归：单 worker 循环消费 dirty/rerun，确保所有退出都释放 running；并发两次保存、合并期间再次保存、失败退出后再次保存必须能继续推进。

### SA-09｜Major｜共享小文件没有启动补漏/撤回闸门，停机期间撤回的密钥副本仍被会话使用

- 定位：`app_wsl_supply.go:260–299, 179–189`；`pi_login_launch.go:154–157`；`launch_planner.go` 的 WSL 单元清单；`launch_executor.go:825–875`；`shared_files.go:80–112`。
- 触发：视觉导出已授权/发布；distro 停机或存在活动会话时 Windows 关闭导出/删 provider；之后启动 WSL 会话。另一路径：视觉文件被本地修改，撤回被判 withdrawal-unconfirmed-frozen。
- 失败行为/影响：shared 的唯一实际分发路径是保存时 push，而该路径此时直接 return；启动的 unit 清单只有 pi/*，没有 shared/vision-export/modality-kb，也没有独立敏感撤回 gate。旧密钥副本可无限存留、继续被工具读取；UI 宣称“下次启动补齐”，批准设计要求“撤回未完成不得静默消费旧清单”，均未实现。
- 证据 **S**：全调用点搜索与实际启动清单交叉核对；不是根据 lack-of-test 判缺陷。
- 最小修复/回归：启动对已启用共享导出进行小文件补漏与撤回判定（不扫描 skills、不安装插件）；未确认撤回应显式阻断/明确独立副本选择。测试 stop→Windows 撤回→start、活动会话延迟、冻结、目标不可达与重启持久化。

### SA-10｜Major｜Claude 常规 commands/*.md 与 agents/*.md 根本不进入资源清单

- 定位：`internal/wslsupply/resources.go:132–147`；`skills_scan.go:466–479`；`app_wsl_supply_s4.go:96–108`。
- 触发：用户拥有标准 `.claude/commands/review.md` 或 `.claude/agents/helper.md`。
- 失败行为/影响：资源层把 commands/agents 和 skills 一样只枚举一级子目录；普通 Markdown 文件被 listSkillDirs 过滤，无法显示、选择或导入。嵌套目录下的文件可能被当目录单元扫描，但这不能覆盖最常用的顶层命令/agent 文件。
- 证据 **R**：建立上述两个合成 Markdown 文件，真实 ResourceScope.Scan 输出 `CLAUDE_RESOURCES normal_markdown_units=0`。
- 最小修复/回归：按 CLI 实际资源结构区分单文件与目录单元，支持正常命令/agent Markdown 和嵌套名称；用实际消费者发现规则验收，不以任意目录 fixture 存在代替。

### SA-11｜Major｜Codex 停跟随后，唯一声明的“确认接管”恢复操作必定失败

- 定位：`internal/wslsupply/codex_adapter.go:327–330`；`coordinator.go:1022–1069`。
- 触发：codex/config 选择“保留本地”后再预览并确认接管。
- 失败行为/影响：AdoptCandidate 仍先无条件拒绝 Detached（不区分 adopt=true）；协调器也未临时解除。返回 adopt-unavailable，用户无法通过承诺的恢复路径重新启用。Pi 的 adopt 实现并无此拦截差异。
- 证据 **R**：`CODEX_DETACH_TAKEOVER outcome=failed detail=adopt-unavailable`。
- 最小修复/回归：仅普通 Candidate 拒绝 detached，显式通过身份/consent/hash 校验的 adopt 允许重建并解除；覆盖 detach→preview→takeover→来源再次变化。

### SA-12｜Major｜插件过滤仅存入 CodeBox 清单，不应用到 WSL packages，实际启用面扩大

- 定位：`internal/wslsupply/plugins.go:98–107, 878–897, 1080–1093`；`state.go:104–109`；`app_wsl_plugins.go:192–195`。
- 触发：Windows 插件通过 packages 过滤对象仅启用部分 extensions/skills/prompts/themes，选择同时应用或 WSL 重放。
- 失败行为/影响：op.Filters 被登记，但实际命令只执行 `pi install <source>`，没有在 distro settings 构建过滤对象，复检也不检查 filters。WSL 消费 packages 原生登记，因而启用整包资源；Windows 禁用的可执行扩展也可能被加载，结果仍报安装/更新成功。
- 证据 **S**：代码明确注释“仅记录，不在 WSL settings 重建”，这不是批准设计的等价实现。操作隔离不等于资源过滤契约成立。
- 最小修复/回归：按目标 CLI 原生登记语义应用并复检过滤集；未支持前对携带 filters 的请求诚实 blocked，不能报等价重放成功。覆盖 allow/deny/空集和 Windows/WSL 实际消费者资源列表。

### SA-13｜Major｜both 模式 Windows 返回 Success=false 时仍安装 WSL，缺锚点时也悄悄追目标版本

- 定位：`app_wsl_plugins.go:146–168, 198–223, 252–265`。
- 触发：Windows CLI 正常返回 CommandResult，但 Success=false 且 Go error=nil；或 Windows 成功但版本/commit 锚点读取失败。
- 失败行为/影响：仅 `windowsRes == nil` 被 skipped；Success=false 的非 nil 结果直接继续 WSL。锚点读取空也允许独立解析，故“同时应用到同版本”的操作可变成 Windows 失败/不变、WSL 却安装另一 latest/ref。与该方法注释和 UI 的固定版本承诺不符。
- 证据 **S**：逐分支可证；CommandResult.Success 与 Go error 是独立结果通道，WindowsOutcome 也专门处理 false。
- 最小修复/回归：both 安装/更新要求 Windows Success=true 且锚点可验证，任一不满足 WSL skipped；用户可显式单侧重试。覆盖 error、非 nil false、成功但锚点空、git/npm 两类；保留 remove 两侧独立语义。

### SA-14｜Major｜OpenCode 冲突检查漏掉 opencode.jsonc，并将读取/解析失败当“没有冲突”

- 定位：`internal/launcher/wsl_opencode_mcp.go:306–333`；`app_wsl_supply_s4.go:936–945`。
- 触发：WSL 全局配置保存在 `~/.config/opencode/opencode.jsonc`，或 json 使用合法 JSONC 尾逗号；或全局文件存在但不可读/探测失败；附加层有同名 MCP。
- 失败行为/影响：只查 opencode.json；注释剥离不支持尾逗号；所有失败回空集合，后续默认把同名 preset server 写入高优先级附加层，覆盖本地选择。用户没有批准覆盖，预览却不报冲突。另存 override 文件只按 distro 名，无 generation，重装同名目标仍继承覆盖选择。
- 证据 **S**：读取路径和空集合回退明确可达；批准设计显式覆盖 json/jsonc 和默认保留本地，不应 fail-open。
- 最小修复/回归：按实际 CLI 配置搜索/优先级探测所有消费入口，完整 JSONC 解析；“未知”不等于“不冲突”；覆盖许可绑定目标身份和 generation。测试 jsonc、尾逗号、不可读、同名重装和真实多层优先级。

### SA-15｜Major｜新一次供给失败不登记，状态页仍显示上次“已同步”

- 定位：`internal/wslsupply/coordinator.go:518–589, 616–648`；`app_wsl_supply.go:437–466`。
- 触发：某单元曾成功；后续 Candidate/写入/CAS/权限失败，或计划 source-unavailable（非 Freeze 分支）。
- 失败行为/影响：失败直接 return UnitResult，不更新 LastErrorCode/LastOutcome；GetStatus 保留上次 written，面板无 pending 时照报 synced。即使 SA-07 已把文件删掉，也仍显示已同步；部分成功的候选亦统一 UnitApplied，依赖只计 Outcome 的调用方会把 partial 当全成功。
- 证据 **R**：同一次合成 chmod 失败后 `FAILURE_STATUS lastOutcome=written lastError=""`，前行已经证明真实目标文件被删。
- 最小修复/回归：所有尝试逐单元持久化最新结果/错误阶段，保留 lastSuccess 但与当前成功区分；清单落盘失败不得报 durable synced；partial 有结构化结果。覆盖首次失败、成功后失败、仅部分键写入、状态库写失败及 UI 展示。

### SA-16｜Major｜取消的供给请求继续写配置；插件超时也没有完整进程/错误生命周期保证

- 定位：`internal/wslsupply/coordinator.go:126, 164, 319–350, 1162`；`skills_submit.go:146`；`resources.go:376`；`plugins.go:242–246, 744–763`；`app_wsl_plugins.go:220–221`。
- 触发：remote effect ctx 已取消或在等待全局 applyMu 时取消；或插件执行达到三分钟 deadline。
- 失败行为/影响：ResolveTarget/Inspect 丢弃 ctx，Apply 取得锁后不检查，文件提交也不检查；取消任务仍可能写配置/同步凭据、持锁做跨界 I/O。插件虽然使用 CommandContext，但 replay 仅 classifyRunnerError(err)，运行中 kill 常返回 ExitError，而非 context.DeadlineExceeded；既有 fake runner 直接返回 ctx.Err，不能证明真实 timeout 归类。Windows wsl.exe 被终止是否停止 Linux npm/git/pi 子进程，本轮无证据，不能宣称取消已闭环。
- 证据：投影部分 **R**：`CANCELED_PROJECTION outcome=applied`；插件归类/进程部分 **S/未实测边界**，没有把“macOS 无 wsl.exe”当成 Windows 杀进程失败证据。
- 最小修复/回归：锁等待/取得锁后/单元前/提交前检查 ctx，平台探测面可取消；命令失败优先归因 ctx.Err，建立 Linux 子进程取消/确认机制或明确不能取消的交互状态。正式回归需真实超时进程而非 runner 直接回 ctx.Err，Windows 验证无残留写入与子进程。

### SA-17｜Minor｜状态轮询每次重启资源全扫，容易变成永不收敛的后台扫描

- 定位：`app_wsl_supply.go:1228–1235`；`app_wsl_supply_s4.go:265–267`；`skills.go:119–135`；`resources.go:98–114`；`frontend/src/views/settings/WSLCLISettings.vue:593–604, 747–756`。
- 触发：设置页轮询 scanRunning；上轮扫描已完成，下一次 Get 又调用 StartScanAsync。
- 失败行为/影响：StartScanAsync 只看 running，不看 started/缓存时效；每次 Get 在返回快照前先置 running=true。前端看到 true 又定时 Get，大目录反复 hash；离开页面没有清理这些 timers。仅因极快扫描抢先完成才可能退出，不能依靠该竞态保证收敛。
- 证据 **S**：Get→start→running snapshot→timer 的直接循环。
- 最小修复/回归：Get 只读或仅首次触发，显式 Check 才重扫；区分扫描 job ID/完成状态，卸载取消轮询。覆盖完成后多次 Get 不增加扫描计数。

### SA-18｜Minor｜三条基线失败主要是测试入口/fixture 契约失配，不应记成产品安全失败或历史全绿

- 定位：`internal/wslsupply/plugins_test.go:439–445`；`skills_scan_test.go:48–53`；`skills_test.go:562–575`。
- 触发与证据 **R**：本轮包测试复现全部三条，详见 §4。
- 实际影响/判别：
  1. 已取消 ctx 的测试仍先触发 exec.LookPath；macOS PATH 无 wsl.exe 返回 not found。此红灯本身是平台/测试前提问题，不证明 Windows 会执行已取消命令；产品真正取消缺陷另见 SA-16。
  2. out.md 指向 `../../outside.txt`，但 fixture 在 `root/outside.txt` 建文件，实际链接落在 root 的父目录，因目标不存在而先分类 non-regular-entry。未见该 fixture 导致越界读取；不能凭原因码不符认定泄漏。
  3. 测试名/注释说 `.env`，实际创建并要求排除 `dot-env`；名单只明确排除 `.env`。这不是平台差异，也不能推出任意名 dot-env 都应当被识别为秘密。真正凭据别名漏洞见 SA-05。
- 最小修复/回归：修正 fixture/平台前提，不扩大凭据黑名单来凑绿；保留真正 `.env` 链接名测试并增加 SA-05 的反向别名用例。当前测试红灯须明确留档，历史 PASS 不可复用。

## 4. 本轮验证与可信度

所有命令 cwd：`/Users/run.mao/workpace/amagi-codebox`。

| 命令/步骤 | 结果 | 能证明 / 不能证明 |
|---|---|---|
| `go test ./internal/wslsupply ./internal/launcher -count=1` | exit 1；wslsupply 三条 FAIL，launcher PASS | `supply-audit-go.log`；与 Leader steer 一致，原因已逐条分类；不是 Windows E2E |
| `go run ./agent-outputs/workflow/wsl-goal-closure/supply-audit-repro.go` | exit 0；11 行反例结果 | `supply-audit-repro.log`；真实协调器 API + 合成临时目录，身份/选择/删除/取消等确定性复现；注入 Chmod/Identity/UNC 不是 Windows 权限证据 |
| `npm --prefix frontend test -- src/__tests__/api/wslSupply.test.ts src/__tests__/wailsjs-s4-args.spy.test.ts` | exit 0；2 files / 32 tests PASS | `supply-audit-frontend.log`；wrapper 与 S4 JS 实参透传成立，不证明 Vue 点击→后端投影成功 |
| `python3 agent-outputs/workflow/wsl-goal-closure/supply-audit-contract.py` | exit 0；41 文件、33 exported App 方法，JS/d.ts 缺失均为空 | `supply-audit-contract.log`；核对名字与签名，相关 S4 JS 已由 spy 执行；S5 绑定多为 Promise<any>，只能靠 API 类型断言，未伪称强类型闭环 |
| `git diff --exit-code 58e7577 -- internal/wslsupply app_wsl_supply.go app_wsl_supply_s4.go app_wsl_plugins.go internal/launcher frontend/src frontend/wailsjs` | exit 0 | 没有修改产品或覆盖共享工作树；仅新增本报告与取证文件 |

Leader 提供的前端全套 271 tests/build、Windows 交叉编译属于上游证据，不写成本轮复跑。未重复全仓测试；已知 root Pi login/remote 计划基线失败交 baize，供给交接点见下表。没有安装依赖、启动 UI/browser 或执行任何真实 WSL 操作。

## 5. 覆盖 / 漏检矩阵

| 维度 | 已覆盖证据 | 缺口/风险与验收状态 |
|---|---|---|
| 身份/distro/generation | ResolveTarget、SameIdentity、状态主键、授权/接管、A2 专用目标解析；SA-01/02 | **不满足**；真实更换默认用户/同名重装未实测。A2/provider writer 使用进程缓存 WSLUserHome，而协调器 identity 不缓存，存在旧 Home 与新目标不一致接缝，交 baize 结合启动身份确认，不在此重复签发运行链 finding |
| 普通配置保留/冲突 | keyed、Strict JSON、source/target 删除、takeover、TOML 保真、自校验、staging/compensation；SA-07/11/15 | **部分**；外部编辑最后窗口、Windows UNC symlink/reparse、状态库磁盘失败仅静态，未全故障注入 |
| 安全/凭据/权限 | A2 源缺失/hash+mtime 分类、视觉同意、排除物、私有写入；SA-01/02/04/05/06/07/09 | **不满足**；无真实秘密使用，无第二 Linux 用户权限观测；API key 撤回不等于提供商吊销 |
| 五 CLI 实际落点 | Pi models/settings/amagi/mcp、Claude json/settings/资源、Codex TOML/资源、OpenCode 附加层、omp 既有 writer 排除声明 | **部分**；Pi `mcp.json` 是否被当前已装 MCP 扩展消费（设计还要求 `.mcp.json` 实际入口核验）没有可用的插件版本/源码证据；不能以文件存在声称 MCP 可用。Claude/Codex/OpenCode 未做真实 MCP 启动，skills 未做消费者执行 |
| 插件/部分失败 | 目标化入口、固定锚点、过滤、settings+实体复检、dirty repo、幂等、取消；SA-12/13/16 | **不满足**；未联网安装。完整操作只在每条 script 上持 opMu，且不共享 Coordinator.applyMu，操作间/配置间并发没有完整事务边界；保留为待真实并发验证，不凭注释直接宣称已丢数据 |
| 保存/启动/恢复 | dirty、single-flight、活动会话、stop、shared 调用点、remote effect；SA-08/09/15 | **不满足**；PendingProgress 未按目标/revision 绑定，存在旧执行清新 dirty 的并发疑点，尚未单独复现，列盲区不计 finding |
| UI/API/Wails | 选择、取回状态、错误、JS/d.ts 33 方法、S4 JS spy；SA-03/15/17 | **部分**；未运行浏览器，视觉未审。Claude settings 单元无 selectLeaf/Importable，而 UI 只对勾选键调用授权导入，没有对称启用按钮；这是额外入口缺口，应与 SA-03 切片核实修复，不冒充已动态复现 |
| OpenCode 冲突/消费者 | 附加层适配、override 保存、json/jsonc；SA-14 | **不满足**；当前适配按 `command:string + args/env` 判定，OpenCode 原生 schema/版本需对照实际 CLI 进一步核验（原生命令数组/environment 等不能凭假 fixture 宣称已适配）；多会话共用 amagi-codebox.json 的读取时序未实测 |
| 取消/进程 | 已取消 ctx 写入复现；真实 runner 静态检查；SA-16 | **不满足**；Windows wsl.exe→Linux 子进程清理未验证，不能用交叉编译代替 |
| 性能/可维护 | 全局 applyMu、重复扫描、I/O 接点、状态槽/泛型引擎；SA-08/17 | **部分**；没有 Windows/DrvFS 性能基准。无理由扩大成全仓优化 |
| 原生 Windows/macOS 兼容 | launcher 本机包测试，前端 API/JS 测试 | **部分**；原生完整会话没有本轮 E2E，Windows compile 为上游证据 |

## 6. 批准方向落实度与复杂度判断

已落实的有价值方向：停止整树播种、provider 命名空间保留、普通键级冻结、来源/写入双基准、插件不在启动时安装、Wails 实参透传、omp 排除保持关闭。无需推倒这些机制。

但“体系已经全部闭环”的历史表述过强：

- 单目标覆盖式状态表没有实现 per-target 授权；多个身份探测机制并存，不是真正的唯一目标。
- 启动懒校验没有覆盖共享密钥撤回；保存 single-flight 和资源轮询反而妨碍最基本的更新完成。
- 同一套 skill 目录抽象直接套到 Claude commands/agents，漏掉实际消费者的常规 Markdown 文件；多层选择/授权/预览却没统一 key 表示，正常 UI 选择完全失效。
- TOML 写了较完整的保真编辑/自校验，但权限字段 whitelist 与取消语义缺位；测试多证明自建 fixture，而不是用户实际输入与 CLI 消费语义。

建议优先缩小到可证主路径：一个稳定 TargetRef、一套私有提交原语、规范化选择标识、逐项真实结果；不新增另一套同步框架。目录资源、密钥导出、插件重放应保留各自不同的安全语义，不能为了抽象统一而掩盖实际消费者差异。

## 7. Top 修复清单（供 Leader 排序，不代为实施）

1. **P0 安全与数据保全**：SA-01/02（身份与授权）、SA-05/06/07（秘密与前像），并绑定真实 Windows/隔离 distro 的权限取证；修复前不扩大自动供给覆盖面。
2. **P0 敏感撤回**：SA-09 + SA-15，先让关闭导出/删除 provider 后的下一次会话行为可解释，不能继续静默用旧副本。
3. **P1 Windows 工程主路径**：SA-03/04/08/10；验证真实 UI 选择与 CLI 可消费的目标产物，修复范围不要求迁移工程到 ext4 或更改全局安全配置。
4. **P1 插件语义与取消**：SA-12/13/16；filters 未实现前 blocked，both 无锚点不得追 latest，真机确认无 Linux 子进程残留。
5. **P1/P2 恢复与诚实状态**：SA-11/14/15/17；按目标保存状态、支持实际 jsonc、断开状态查询与重扫；再修 SA-18 的测试前提，不能用改断言绕过产品反例。

正式回归由实施者编写；本报告只给反例脚本、触发条件和最小修复方向。与 baize 的交接重点：fresh identity vs cached Home、启动是否经过 shared 撤回 gate、最终 distro/用户/cwd 与 provider writer 一致、remote 取消后是否仍有写入/子进程。

## 8. 产物与回滚

实际新增仅本报告、`supply-audit-repro.go/.log`、`supply-audit-contract.py/.log`、`supply-audit-go.log`、`supply-audit-frontend.log`，均在授权报告目录。脚本使用合成数据，未写正式测试。

回滚仅由 Leader 按需删除本轮以上报告/脚本/日志；产品无变更、用户/distro 配置无变更，不需要恢复或回滚凭据。不能删除同目录其他并行任务产物或既有 resume-relay。
