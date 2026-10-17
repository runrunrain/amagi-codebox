# C3 配置适配器/资源导入修复实施报告（SA03/04/05/10/11/17/18）

- Worktree：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/wsl-goal-adapters`（分支 `amagi/wsl-goal-adapters`，基线 `58e7577`，未提交——改动留工作区由 Leader 汇总）。
- 上游输入：`agent-outputs/workflow/wsl-goal-closure/supply-audit.md`、`supply-audit-repro.go`、`direction.md`（均已完整读取）。
- 本机环境：macOS（无 Windows/WSL 真机）。全部验证使用合成数据 + fake 探测面 + 临时目录；未读取真实凭据，未触碰真实 distro。

## 0. 基线复现（修复前）

- `go test ./internal/wslsupply -count=1`（基线 58e7577）exit 1，三条失败与审计 §4 SA-18 完全一致：`TestPluginDefaultRunnerHonorsCanceledContext`（macOS 无 wsl.exe）、`TestScanSkillCompatChecks`（越界链接 fixture 落点错误）、`TestScanSkillLinkNameExcluded`（dot-env/.env 名不符）。
- 审计复现脚本（抄入 worktree 临时包后 `go run`，已删除）基线输出与本轮修复后对照见 §2 各 SA 证据。

## 1. 修复内容（逐 SA）

### SA03｜UI 规范键到后端选择贯通、零导入不报成功、空选择能撤回、Claude settings 启用入口

**根因**：前端勾选/导入传预览全路径键（`mcpServers/demo`、`profiles/demo`），后端 `applySelection`/`desiredFrom`/`PreviewKeys` 按叶名匹配 → desired 恒空 → RunUnits 返回 fresh，前端把 fresh 计入成功，虚报「已导入所选」。

**修复**：
- `keyed.go`：新增 `selectionMatchesKey`——全路径键优先匹配；历史叶名登记仅在叶名于本单元键集内唯一时兼容生效（同名 MCP/profile 双义保守不选，杜绝跨作用域误选）。
- `pi_config.go`：`applySelection` 与 `PreviewKeys.Selected` 改用全路径匹配（allKeys 覆盖期望全集 + 判定键 + 登记基准）。
- `codex_adapter.go`：`desiredFrom` 重构为先收集全集再按全路径过滤；`PreviewKeys.Selected` 同改。
- `resources.go` `SetUnitSelection`：存储不变（原样保存规范化全路径键），注释更新契约。
- `app_wsl_supply_s4.go`：`ImportWSLCliConfigKeys` 链路不变；前端消费 fresh 时不再计成功。
- 前端 `WSLCLISettings.vue`：`importCliKeys` 不再对空选择早退——空选择是显式撤回入口；提示逻辑抽出 `frontend/src/views/settings/wslSupplyMessages.ts`（`cliImportOutcomeMessage`）：零写入不报成功、空选择撤回/无托管可撤、部分失败逐项说明；按钮在有预览时常驻，0 选择时显示「撤回全部导入（空选择移除托管条目）」。
- **Claude settings 启用入口缺失（核实为真并修复）**：`claudeSettingsShape` 原无 `selectLeaf` → `SelectionAware()`=false → `SetUnitSelection` 拒绝该单元，而 S4 面唯一授权路径是导入所选 → `claude/settings` 永远无法启用（awaiting-consent 悬死）。修复：`selectLeaf: true`（白名单不变——permissions/hooks/env 不在白名单内，不可能被勾选）。`TestClaudeSettingsWhitelist` 更新为显式勾选白名单字段。

### SA04｜Codex profile 白名单，不导入权限/未知字段，目标已有本地策略保留

**修复**（`codex_adapter.go`）：
- 新增 `codexProfileWhitelist`：`model`/`model_provider`/`model_reasoning_effort`/`model_verbosity`/`disable_response_storage`/`chatgpt_base_url`（与仓内 codexplugin 字段词表一致）。`approval_policy`/`sandbox_mode`/信任/项目路径/嵌套表/一切未知字段不导入，丢弃字段逐项报告（结果记 partial）。
- `convertCodexProfileSection`：白名单过滤 + 路径转换；白名单过滤后为空（纯权限 profile）→ 该节 conversion-blocked 保持目标现状。
- `mergeCodexProfileSection`：profile 更新/接管按白名单覆盖到目标已有段——目标本地权限决定/未知字段/子表保留（mcp_servers 仍整段替换，server 定义原子单元）。
- 预览 `codexProfileBefore` 诚实标注「N 个键，其中 M 个权限/未知字段不导入」。

### SA05｜skill 链接别名读取被排除秘密/目录漏洞

**修复**（`skills_scan.go` + `skills_submit.go`）：
- `resolveSkillLink` 返回链上各跳；`scanLink` 对**每一跳与最终路径**执行 `skillPathHitsExclusion`（凭据文件名/扩展 + 排除目录段如 `.git`/`node_modules`）→ 命中按排除处理（预览可见、不阻断、不实体化）。
- `readSkillCandidateFiles` 读取前 `os.Lstat` 重验路径身份：扫描后被替换为链接/非常规形态直接拒绝（rescan required），hash 复核仍兜底内容一致。

### SA10｜Claude commands/agents 常规 md 单文件与嵌套资源（真实消费者路径）

**修复**（`resources.go` + `app_wsl_supply_s4.go`）：
- `ResourceLayout` 新增 `FileLeafUnits`：声明该 DirUnit 的顶层 `*.md` 文件为单文件单元。生产布局：Claude `commands`/`agents` 启用（`commands/review.md`、`agents/helper.md`——Claude 实际发现形态）；**Codex skills 显式不启用**（folder 发现语义，消费边界已在布局注释标注）。
- `Scan()` 枚举顶层 md 文件（`listResourceLeafMDFiles`：常规文件、非隐藏、`.md`）；`isFileUnit` 区分文件/目录单元，供给分别走 `submitFile`/`submitDirUnit` 原语。

### SA11｜显式 takeover 恢复 detached Codex

**修复**：
- `codex_adapter.go` `buildCandidate`：Detached 拒绝仅限普通供给路径（`!adopt`）；显式接管（协调器已过 CAS + 同意门）放行重建基准（与 Pi 键级适配器同构；接管成功后 `recordProjectionState` 重建条目即解除 Detached）。
- **连带真实缺陷**（C2 文件最小 hunk，见 §3）：`coordinator.go` `DropUnitRegistration` 投影单元分支原不保留 `SelectedKeys` → 停跟随后接管，用户选择集丢失、供给退化为零选择（detach→takeover→来源再次变化 场景实测失败）。补 `SelectedKeys: prev.SelectedKeys`（与资源单元分支既有行为一致）。

### SA17｜状态 Get 不重启扫描，显式 Check 才重扫、前端卸载停止轮询

**修复**：
- `skills.go`/`resources.go`：`StartScanAsync` 已启动过即幂等（Get 路径不再重启已完成扫描）；新增 `RescanAsync`（强制重扫，仅在跑时返回 false）。`Coordinator` 新增 `RescanSkills()`/`RescanCliResources()`（**新公开方法，声明**：仅此两个，均在本人责任文件内定义，未改任何既有签名）。
- `app_wsl_supply.go`（限授权段）：`wslSkillsCoordinator` 接口 + `RescanSkills`；`CheckWSLSkillsSupply` 先强制重扫再取快照；`GetWSLSkillsSupplyStatus` 注释更新。
- `app_wsl_supply_s4.go`：`wslCliSupplyCoordinator` 接口 + `RescanCliResources`；`CheckWSLCliResourcesSupply` 改用强制重扫；Get 保持首启幂等。
- 前端：`onUnmounted` 置 `alive=false`，`loadSkills`/`loadCliSupply`/`checkSkills` 轮询链全部受守卫（扫描收敛后轮询自然终止；卸载立即断链）。

### SA18｜skills 错误 fixture 修正（不扩大黑名单凑绿）

- `skills_scan_test.go`：越界链接 fixture `../../outside.txt` → `../outside.txt`（原目标落到 root 父目录不存在，被归为 non-regular；修正后命中真实 `out-of-tree-link` 路径，root/outside.txt fixture 不变）。
- `skills_test.go`：`dot-env` → `.env`（真凭据名；**未**向排除名单新增任何条目）；保留原断言结构。
- `plugins_test.go`（非本人清单文件，最小测试侧前提修正，hunk 见 §3）：已取消 ctx 测试在非 Windows 宿主（PATH 无 wsl.exe）跳过——红灯属平台前提而非取消语义（产品取消缺陷 SA-16 属 C2/C4 范围，未动 `plugins.go`）。

## 2. 逐 SA 验证证据

正式回归（修复后，全部真实命令、worktree cwd）：

| 命令 | 结果 |
|---|---|
| `go test . ./internal/wslsupply ./internal/launcher -count=1` | 全部 ok（wslsupply 由基线 3 FAIL → 全绿；root 91+ tests 含新增 App 门用例） |
| `go test -race ./internal/wslsupply -count=1` | ok（触及并发扫描代码，补 race 档） |
| `go vet ./...` | 通过（仅 internal/secrets 既有 macOS cgo deprecation 告警，主树同样存在，非本轮引入） |
| `GOOS=windows go build ./internal/wslsupply .` | exit 0（Windows 交叉编译） |
| `npm --prefix frontend run test` | 31 files / **277 tests** 全过（基线 271 + 新增 6） |
| `npm --prefix frontend run build` | vue-tsc + vite 通过（含 WSLCLISettings.vue 与新逻辑模块） |
| 审计复现脚本 `go run ./tmprepro`（抄自 supply-audit-repro.go，已删） | 见下表；C2 范围条目按预期保持失败 |

复现脚本输出对照（★=本轮范围）：

| 场景 | 基线（审计） | 修复后 |
|---|---|---|
| ★UI_SELECTION | `outcome=fresh target_missing=true` | `outcome=applied target_missing=false` |
| ★CODEX_POLICY | `unsafe_policy_copied=true unknown_copied=true` | `unsafe_policy_copied=false unknown_copied=false` |
| ★CODEX_DETACH_TAKEOVER | `outcome=failed detail=adopt-unavailable` | `outcome=applied detail=takeover-written; 键 profiles/demo：已按白名单导入 1 个字段；approval_policy/sandbox_mode/unknown_host_field 属权限/未知字段不导入（保留目标现状）` |
| ★SECRET_ALIAS | `alias_in_manifest=true` | `alias_in_manifest=false excluded=[.env alias.txt]` |
| ★CLAUDE_RESOURCES | `normal_markdown_units=0` | 脚本自建布局未声明 FileLeafUnits 故仍为 0；**生产布局**（App 布局声明 commands/agents）由 `TestCheckWSLCliResourcesForcesRescan`（`commands/review.md` 首扫入列）与 `TestResourceClaudeLeafMarkdownUnits`（清单+供给到 `.claude/commands/review.md` 消费者路径）证明为 2 |
| GENERATION_SWAP / UNKNOWN_CROSS_DISTRO / RETURN_TO_A / CHMOD_FAILURE / FAILURE_STATUS / CANCELED_PROJECTION | — | 保持失败（SA01/02/07/15/16，C2 范围，未动） |

新增回归测试（按 SA）：
- SA03：`selection_keys_test.go`（全路径贯通 claude+codex、同名叶双义零导入、历史唯一叶兼容、空选择撤回、预览 Selected 一致、未知键零写入）；`app_wsl_supply_s4_test.go::TestImportWSLCliConfigKeysFullPathAndWithdraw`（App 门：全路径键→真实目标文件内容；空选择撤回移除托管条目）；`claude_config_test.go` settings 启用入口用例。
- SA04：`codex_config_test.go` 四条（权限/未知/嵌套不跨界+目标侧权限字节保留+partial 报告；未托管→接管保留本地策略；接管后来源更新 merge 保留本地字段；纯权限 profile conversion-blocked）。
- SA05：`skills_scan_test.go::TestScanSkillAliasToExcludedCredential`（直接别名/多跳/排除目录目标均排除且不阻断可移植）；`skills_test.go::TestSkillCandidateReverifiesSourceIdentity`（扫描后源被换成符号链接→候选读取拒绝）。
- SA10：`resources_test.go`（顶层 md 单元+嵌套目录单元入清单并供给到消费者路径；codex skills 目录语义不受污染）。
- SA11：`codex_config_test.go::TestCodexDetachedExplicitTakeOverRestores`（detach→idle→preview 带 token→takeover applied→来源再变化恢复跟随）。
- SA17：scope 级（完成后 Start 不重启、Rescan 收敛新增目录）×2、App 级（`TestWSLSkillsGetDoesNotRestartCompletedScan`、`TestCheckWSLCliResourcesForcesRescan`）。
- SA18：三条基线红灯转绿（fixture/前提修正，无断言放宽、无黑名单扩大）。

**UI 交互验证（真实浏览器）**：`e2e/wsl-cli-supply-ui.html` + vite 配置（新增 harness，仿既有 ai-proxy harness 模式）挂载真实 `WSLCLISettings.vue`，页面内 stub 记录绑定调用，agent-browser 驱动：
1. Claude「预览」→ 出现全路径键勾选面（`mcpServers/demo`/`mcpServers/other`）；
2. 0 勾选时按钮为「撤回全部导入（空选择移除托管条目）」，点击 → 绑定收到 `ImportWSLCliConfigKeys("claude","claude/mcp",[])`；
3. 勾选两项 → 按钮「导入所选（2）」→ 点击 → 绑定收到 `["mcpServers/demo","mcpServers/other"]` 原样全路径（SA03 UI→绑定边界贯通）；
4. SA17：stub 对 claude 恒报 `scanRunning:true` → 挂载期间轮询活跃（11→13 次）；导航离开后 2.5s（>3 个轮询周期）计数冻结（13→13，卸载断链）。
会话已关闭、vite 已停。

## 3. 跨范围文件精确 hunk（供 Leader 集成）

1. `internal/wslsupply/coordinator.go`（C2 文件，SA11 必需的最小修复，无签名变化）：`DropUnitRegistration` 投影单元分支 `next := StateEntry{...}` 增加 `SelectedKeys: prev.SelectedKeys`（含 2 行注释）。hunk：`@@ -1011,8 +1011,11 @@`。
2. `internal/wslsupply/plugins_test.go`（非本人清单，SA18 测试前提修正）：`TestPluginDefaultRunnerHonorsCanceledContext` 头部加 `exec.LookPath("wsl.exe")` 失败即 `t.Skipf`；import 块加 `"os/exec"`。未动 `plugins.go`。
3. `app_wsl_supply.go`（授权段内 3 个 hunk，该文件其它段落属 C2）：`@@ -1164,6 +1164,7 @@`（接口 +`RescanSkills() bool`）、`@@ -1206,8 +1207,9 @@`（Get 注释）、`@@ -1225,13 +1227,19 @@`（Check 强制重扫 + Get 注释）。
4. 新增公开 API（均在本人责任文件内）：`Coordinator.RescanSkills`/`Coordinator.RescanCliResources`、`SkillsScope.RescanAsync`、`ResourceScope.RescanAsync`、`ResourceLayout.FileLeafUnits` 字段。前端 API 签名零变化（未手改 wailsjs，无需再生）。

## 4. Changed files

生产：`internal/wslsupply/{keyed,pi_config,codex_adapter,claude_adapter,resources,skills,skills_scan,skills_submit}.go`、`internal/wslsupply/coordinator.go`（§3 hunk）、`app_wsl_supply.go`（授权段）、`app_wsl_supply_s4.go`（OpenCode override 段未动）、`frontend/src/views/settings/WSLCLISettings.vue`。
测试/工具：`internal/wslsupply/{selection_keys_test,codex_config_test,claude_config_test,resources_test,skills_scan_test,skills_test,plugins_test}.go`、`app_wsl_skills_test.go`、`app_wsl_supply_s4_test.go`、`frontend/src/views/settings/wslSupplyMessages.ts`、`frontend/src/__tests__/views/settings/wslSupplyMessages.test.ts`、`e2e/wsl-cli-supply-ui.html`、`e2e/wsl-cli-supply-ui.vite.config.mjs`。
环境（不入库，gitignore 覆盖）：`frontend/dist`/`mobile/dist`（embed 借用主树产物复制）、`frontend/node_modules`（主树依赖复制，130M，未写共享树）。

## 5. 未覆盖 / 真实运行情况

- **无 Windows/WSL 真机**：所有 WSL 侧写入经 fake Probes（ToUNC→临时目录）；UNC/9P 权限、真实 distro chmod、ConPTY 行为未验证（与审计同口径，不伪称实机）。Windows 侧仅交叉编译通过。
- **CLI 真实消费边界**：Claude commands/agents 的「消费者发现形态」按其公开目录约定建模（顶层 md + 一级子目录命名空间）；未安装真实 Claude Code 验证发现行为。Codex skills 保持 folder 语义（未验证 Codex 是否会读顶层散落 md——按不虚构处理）。profile 白名单字段词表以仓内 codexplugin 引用为准，未经 Codex CLI 文档逐字段核对。
- **SA17 后台扫描收敛**：App 级断言经轮询 deadline（≤10s）验证；真实大目录（数万文件）扫描耗时未测。
- **浏览器验证覆盖面**：真实组件 + 真实路由 + 真实绑定 JS 透传；绑定实现为页面内 stub（Wails 运行时本身未运行，Go↔JS 桥接由 wailsjs spy 测试与 Go App 门测试分摊证明）。
- SA01/02/06/07/08/09/12/13/14/15/16 未处理（C2/C4 范围；复现脚本中对应条目保持失败即交接证据）。
- 回滚方式：全部改动未提交，`git checkout -- <files>` 或丢弃 worktree 即可；新增文件删除即净。无用户配置/凭据/distro 变更。
