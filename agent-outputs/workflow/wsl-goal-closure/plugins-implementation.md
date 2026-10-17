# C4 插件重放 / OpenCode 冲突边界修复实施报告（SA12/13/14/16/18 + SA02 插件面）

> **C4 补全轮（第二 pass）**：Leader 复审后要求闭合 3 个仍属原 Major 的缺口，已全部修复——① SA12 显式 `[]` 过滤全链三态保留（nil=缺省全开 / 非_nil 空切片=全关）；② SA02 多目标并存 identity+known generation 保存/删除隔离；③ SA14 override 许可改为实时身份解析（不再读状态清单缓存）。新增改动 5 文件（`internal/piplugin/{service,target}.go` 及其测试、`internal/wslsupply/state.go` 仅 PluginFilters 表达/JSON 标签），全部在补全轮授权内；未改任何 Wails 公开方法签名（新增为 piplugin 包级函数与 app 内部窄接口，均不进绑定面）。真实 Windows 子进程终止边界仍未实测（见 §四）。

- Worktree：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/wsl-goal-plugins`（分支 `amagi/wsl-goal-plugins`，未提交，工作区改动共 14 文件 +2020/−169）
- 上游输入：`supply-audit.md`（full 审计）、`direction.md`（收敛决策）；均已完整读取
- 本报告落点即产物声明路径；验证命令均在 worktree 根执行（2026-10-16）

## 一、逐 SA 销项

### SA12 插件过滤只记录不应用 → 已修复（Pi 原生语义应用 + 复检）

**语义证明**：Pi 官方文档（本机 pi-coding-agent 自带 `docs/packages.md`，与 WSL 重放目标同一产品族）明确 `packages[]` 元素可为字符串源或 `{source, extensions[], skills[], prompts[], themes[]}` 过滤对象；键省略 = 该类型加载全部、`!`/`+`/`-` 前缀模式由 pi 按 glob 解释。语义可证 → 实施应用路径（而非 blocked）。

**实现**（`internal/wslsupply/plugins.go`）：
- `applyDistroFilters`：`pi install` 成功后把 distro settings.json 中该 identity 的登记经 sjson 定点改写为等价原生形态——有过滤项 → 过滤对象（模式串原样保留）；无过滤项 → 纯字符串（含从旧过滤对象归一回全量）。已是规范形态零写入；同目录 tmp+rename 原子落盘；packages 之外的顶层键与兄弟元素字节不动。
- `verifyDistroFilters` 复检：过滤对象逐字段相等（allow/deny 模式原样比对）；期望无过滤时要求登记不带过滤项。应用/复检失败 → 结构化 `filter-apply-failed` / `verify-failed`，**绝不写已安装登记、绝不报成功**。
- 幂等条件加入过滤形态（install 与 update 的 already-ok 路径）：Windows 清单变化/WSL 内手改形态漂移都会触发重放射敛，不再冒充 already-ok。
- update 沿用「面板新清单覆盖、否则保留登记清单」合并语义，重放后按同一清单应用复检。
- 登记 `PluginStateEntry.Filters` 注释由「仅记录」改为实际生效语义。

**补全轮（缺口①闭合）：显式 `[]` 与键省略的全链三态保留**。旧链路把「显式空数组 `[]`（该类型一项都不加载）」折叠成省略而启用整类扩展；本轮从读面 → App 透传 → 持久化 → distro 写入/复检全链修复，nil（缺省全开）与非 nil 空切片（全关）全程不混淆：
- **读面**（`internal/piplugin/service.go`）：`toStringSlice` 重写为 `filterSlice`——键省略 → nil；显式 `[]` → 非 nil 空切片；键存在但值非数组（用户手写异常形态，无 pi 官方语义）按收紧面视为已声明且清单空（全关），不折叠回省略而扩大启用面。新增包级函数 `EntryFiltersFor(s, source)` + `EntryFilters` 类型（`target.go`）返回三态快照——**包级函数而非 Service 方法，不进 Wails 绑定面**（wailsjs 零变化）；既有 `WindowsEntryFilters` 方法保持原签名原语义（Wails 公开方法签名未改，前端未引用）。
- **App 透传**（`app_wsl_plugins.go`）：改经 `piPluginOpFilters(EntryFiltersFor(...))` 纯函数透传；旧 `len(...)>0` 门禁废弃——`[]` 不再被当「无过滤」而全开重放。
- **持久化**（`internal/wslsupply/state.go`，仅 PluginFilters 表达/JSON 标签）：四字段去 `omitempty`——空切片序列化 `[]`、nil 序列化 `null`，磁盘 round-trip 三态保留。
- **写入/复检**（`internal/wslsupply/plugins.go`）：`hasFilters` 改「任一字段非 nil（含空切片）」；`stringSlicesEqual`/`pluginFiltersEqual` 严格三态（nil 与空切片不等——期望空数组而登记省略/纯字符串判形态不符，重放射敛）；`canonicalPackageEntryJSON` 手工构造（`writeFilterField`：nil 键省略、非 nil 一律写键含 `[]`），不再用 omitempty 结构体序列化（会把空数组省略成「全部加载」）；distro 侧 JSON 解析天然区分（本包写入面不产生 null，手写 null 按键省略处理，已注释）。
- **测试**：piplugin `TestEntryFiltersForDistinguishesEmptyArrayFromOmitted`、`TestEntryFiltersForNonArrayValueTreatedAsDeclaredEmpty`；app 层 `TestPiPluginOpFiltersKeepsNilVersusEmpty` + 经真实入口 `InstallPiPackageTargeted` 的 `TestPiPluginTargetedOpFiltersCarryExplicitEmptyArray`（Windows settings 带 `"extensions":[]` → 协调器收到非 nil 空切片）；wslsupply `TestPluginFiltersNilVersusEmptyStrictlyDistinct`（比较/写入/JSON round-trip）、`TestPluginInstallExplicitEmptyArrayFilter`（写入 `[]` + 复检 + 幂等 + 登记磁盘 round-trip）、`TestPluginEmptyArrayFilterDriftNotAlreadyOK`（漂移射敛）。piplugin 旧断言 `TestListInstalledPluginsSupportsFilterObject` 按新规格同步（旧断言把 `[]` 钉为 nil 且注释误释为「加载全部」——与 pi 语义相反；展示面零差异：`Package.*` 带 omitempty，空切片与 nil 都不序列化）。

**测试**（plugins_test.go，首轮）：过滤对象写入且 allow/deny 逐字保留、无过滤保持纯字符串、同版本过滤漂移重放射敛、清单移除归一回纯字符串、update 重建登记清单、sjson 定点替换不破坏兄弟元素与顶层键（含纯函数级 `applyDistroFilters` 直测）。

### SA13 both 模式缺锚点仍装 WSL → 已修复

**实现**（`app_wsl_plugins.go`）：both 的 install/update 经纯函数 `wslReplayAnchorGate` 门禁，三种任一不满足 WSL 侧 skipped（单次不重试，可显式 wsl 目标单侧重试）：
1. Windows 执行返回 Go error（res==nil）→ skipped `windows-anchor-missing`（原行为保留）；
2. Windows 返回非 nil CommandResult 且 `Success=false`（与 Go error 是独立通道）→ skipped `windows-failed`（新增——旧实现直接继续 WSL 独立解析）；
3. Windows 成功但实体锚点（版本/commit）不可读 → skipped `windows-anchor-missing`（新增——旧实现回退 distro 内追 latest/ref）。
remove 保持两侧独立语义不变。

**测试**（app_wsl_plugins_test.go）：门禁纯函数四分支直测（含 `[]CommandResult{Success:false}` + nil error 组合——piplugin 当前执行面不产生该组合，故钉住编排层判据本身）；集成测试「成功但实体缺失 → skipped 不触达 WSL」「update git 锚点不可读 → skipped」；原「Windows ok + WSL 失败独立显示」用例改为预置实体以在新门禁下继续覆盖独立结果显示。

**前端状态反馈**：无需改动——`PiPluginOpOutcome.outcome` 联合类型本就含 `skipped`，`PiPluginPanel.describeOutcome` 通用渲染 errCode/detail；无 Wails DTO 形状变化，wailsjs 未动。

### SA14 OpenCode 冲突边界 → 已修复

**实现**（`internal/launcher/wsl_opencode_mcp.go` + `app_wsl_supply_s4.go` 仅 OpenCode override 段）：
- 读取三态 `wslGlobalAbsent / Readable / Unknown`：同时探测 `~/.config/opencode/opencode.json` 与 `opencode.jsonc` 并集（两入口都可能被 CLI 消费，并集对「保留本地」保守）；HOME/UNC 探测失败、读取失败、解析失败均归 **Unknown**——不再回退空集合冒充「无冲突」。
- 完整 JSONC：新增 `stripJSONCTrailingCommas`（字符串字面量外的尾逗号删除，确定性钉子直测），与既有注释剥离串联；合法尾逗号不再导致解析失败。
- 写入面：Unknown 状态下所有 preset MCP 不写入附加层（任何名字都可能覆盖本地选择；覆盖勾选也不能越过未知），逐条报告原因，启动继续（可选配置失败可见不阻断）。
- 预览面：`OpenCodeMCPPreviewEntry` 新增结构化 `GlobalConflictUnknown` + 原因；s4 侧 `wslOpenCodeMCPPreviewRow` 映射为现有 `needsAdapt+reason` 通道呈现（**不扩 Wails DTO、wailsjs 未动**——launcher 内部结构体不在 Wails 绑定面；若未来要结构化字段需再生成）。
- **override 许可绑定目标身份代**：选择条目新增 `generation`；生效（`wslOpenCodeMCPOverrideSet`，预览与启动两侧同源）要求保存与当前身份代都已知且一致——**同名 distro 重装换代后旧覆盖许可不继承**（旧条目留盘供诊断，仅不再生效）。预览勾选态与生效面同函数判定，UI 所见即所得。

**补全轮（缺口③闭合）：当前代改为实时身份解析，不再读上次缓存**。旧实现「当前代」取自状态清单（最近检查/供给时写入）——重装后未再检查前状态仍记旧代，存在旧许可残留窗口。本轮：
- 新增 app 内部窄接口 `wslTargetResolver{ ResolveTarget(ctx, distro, CLIPi) }`（`app_wsl_supply_s4.go`）——**复用既有 Coordinator.ResolveTarget，不新造探测体系**；窄接口不进 Wails 绑定面、不改根启动链（启动仍经既有 `prepareWSLOpenCodeLayer → wslOpenCodeMCPOverrideSet` 接缝，仅后者内部改为实时解析）。
- 新增 `wslOpenCodeMCPResolveCurrentGeneration(distro)`：实时解析（30s 超时预算；读取不写配置——ResolveTarget 只做身份探测与 UNC 映射）；探测失败返回错误、身份代 unknown（无 machine-id）返回空串；删除旧缓存读函数 `wslOpenCodeMCPCurrentGeneration`。
- 保存面（`SetWSLOpenCodeMCPSelection`）：**解析错误拒绝保存**（含原因）、unknown 拒绝保存；不再依赖状态清单是否检查过。
- 消费面（`wslOpenCodeMCPOverrideSet`）：**解析错误/unknown 返回空覆盖**（= 保守保留本地，不写附加层覆盖同名项）；旧状态缓存保持旧代但真实身份已变（重装/换默认用户）时旧许可不生效——残留窗口随缓存语义废弃而消除。

**测试**：jsonc+行/块注释+尾逗号冲突保留本地；json+jsonc 并集；损坏 JSON → 全部不写入且逐条报告（覆盖勾选同样拦截）；HOME 探测失败按未知；预览结构化标记；尾逗号剥离纯函数钉子；s4 侧：绑定代际后生效、同名重装不继承、未知身份拒绝保存、预览行映射（含原因叠加）。补全轮：`TestWSLOpenCodeMCPOverrideNotInheritedAfterReinstall` 增强为换 machine-id 后**不重新检查**即断言旧许可失效（旧缓存语义的回归钉子）+ 身份探测不可用返回空覆盖 + 旧条目留盘；`TestWSLOpenCodeMCPSelectionSaveRefusedWhenIdentityUnresolvable` 重写为实时解析失败（Identity !OK）与 unknown（无 machine-id）两场景拒绝保存且不落盘。

### SA16 插件取消/超时（本切片插件面）→ 已修复（含诚实边界声明）

**实现**（`plugins.go` + `plugins_api.go`）：
- **优先 ctx.Err 归因**：`classifyRunnerError(ctx, err)` 改为 ctx-first——被 CommandContext 终止的 wsl.exe 常以 ExitError 形态返回（不携带 ctx 错误链），ctx 已结束时归因 timeout/canceled，不再误报 replay-failed；resolveNpmVersion/resolveGitCommit/piAvailable/gitDirtyCheck/replay 全部接线。
- **已取消不查 wsl.exe**：`defaultPluginCommandRunner` 构造命令前短路返回 ctx.Err（exec.CommandContext 对已取消 ctx 的 Start 仍可能先触发 LookPath——这正是 SA18 红灯的平台前提）；`runScript` 取 opMu 前检查（取消的请求不排队）；`PluginScope.RunPluginOperation`/`Probe` 与协调器门面 `RunPluginOperation`/`ProbeWSLPlugins` 入口拦截（取消不触发身份探测，且不把取消误报为 cli-missing 等无关阶段）。
- **不编造已 kill Linux 进程**：注释与超时/取消 detail 明确「终止的是 Windows 侧 wsl.exe；distro 内子进程是否随之退出未经 Windows 真机验证」——不宣称 Linux 侧已清理。

**测试**：ExitError 形态（runner 回 `exit status 1` + ctx 到期）归因 timeout 且 detail 陈述进程边界；入口拦截不触发身份探测/命令；锁等待期间取消不执行命令；既有 ctx.Err 直返用例保留。

**未覆盖（如实）**：wsl.exe 终止是否停止 Linux npm/git/pi 子进程——无 Windows 真机，本轮无法验证，仅以诚实文案与代码注释声明边界；真机回归留给 Leader 排期。

### SA18 插件取消测试环境前提 → 已修复

`TestPluginDefaultRunnerHonorsCanceledContext` 在 macOS 红灯的根因是 `exec.CommandContext` 已取消 ctx 下仍先做 wsl.exe LookPath。修复在产品面（runner 构造命令前检查 ctx.Err），测试前提变为平台无关——**未改断言凑绿**。该测试现随包通过（`go test ./internal/wslsupply -run TestPlugin` 全绿）。
同 finding 的另外两条（`TestScanSkillCompatChecks` fixture 落点、`TestScanSkillLinkNameExcluded` 的 `dot-env` 名）属 skills 扫描面（SA-05/SA-10 切片），不在本切片范围，仍红（见下）。

### SA02 插件登记多 distro 隔离（plugins.go 读/写逻辑）→ 已修复（identity+known generation 联合键隔离落地）

**读侧**（首轮）：`pluginEntryByIdentity` 只在双方 generation 都已知且相等时匹配；目标/登记任一方 unknown 或空 → 视为无登记（Probe reg-only 环与 `RegistrationSummary` 同规则）——unknown 跨 distro 不再继承管理权。写侧（首轮）：身份代未知时不落持久登记（读侧永不匹配的死数据不写）；安装结果仍如实返回。效果：无 machine-id 的最小 distro 上重复 install 诚实 unmanaged-existing、update/remove 诚实 not-managed（宁可退化不越权继承）。

**补全轮（缺口②闭合）：联合键保存/删除隔离**。任务书裁定：C2 已把 generationFor 改为 SHA(distro|machineID|uid|home)，现有 `PluginStateEntry.Generation` 足以作联合键，无需等待新 state 字段——本轮把 `savePluginEntry`（旧：仅 Identity 命中替换）与 `dropPluginEntry`（旧：删所有 Identity 匹配行）改为 **identity+known generation 联合键**：
- `savePluginEntry`：同 identity 且同（可绑定）generation 才替换；不同代际追加并存——同包在 A/B 两个代际目标上各自一行登记（B 的保存不覆盖 A 的单槽）。
- `dropPluginEntry(identity, generation)`（签名变更，PluginScope 内部方法非 Wails 面）：只删联合键命中的行——B 移除同包不动 A 的登记；不可绑定代际的删除请求 no-op。两个调用点（remove 成功后/登记已消失清理）均传 `target.Generation`。
- 读/写/删同键：`pluginEntryByIdentity`（首轮已严格）与 save/drop 现为同一联合键，`mutateEntry` 回写经 prev 登记（携带正确 generation）归位同一行。
- **测试** `TestPluginMultiTargetSamePackageCoexistsByGeneration`（双 fake distro + 可切换身份探测的真实协调器）：A(gen-a) 安装 @1.3.0 → B(gen-b) 安装同包 @1.4.0（两行并存且 A 行 PinnedRef 不被覆盖——单槽覆盖回归钉子）→ 回 A 同锚点 already-ok（A 登记仍可归属）→ B remove 只删 B 行、A 行保留且回 A 后幂等继续工作 → unknown 代际删除 no-op。既有 unknown 不继承钉子（`TestPluginUnknownGenerationNeverPersistsOrMatches`、`TestPluginRegistrationsDontCrossDistros`）保持全绿。
- 遗留说明：克隆 machine-id（两目标同代）场景仍归 C2 的 distro 维度 generation 公式（本树 generationFor 未含 distro，任务书已裁定不复制兄弟改动，plugins 侧全部经 `TargetRef.Generation` 消费——C2 合入后自动收敛，无需本切片改动）。

## 二、changed files（全部在任务授权范围）

| 文件 | 变更 |
|---|---|
| `internal/wslsupply/plugins.go` | SA12 应用/复检原语与接线、SA16 ctx-first 归因与入口/锁前检查、SA02 严格代际匹配与写侧抑制、诚实进程边界文案 |
| `internal/wslsupply/plugins_api.go` | SA16 门面入口取消拦截（目标解析前） |
| `internal/wslsupply/plugins_test.go` | 假 distro 支持过滤对象原形态（install/remove 不扁平化兄弟元素）+ 上述各 SA 钉子（约 15 个新用例） |
| `app_wsl_plugins.go` | SA13 `wslReplayAnchorGate` 门禁（error/Success=false/锚点空三分支 skipped） |
| `app_wsl_plugins_test.go` | SA13 门禁直测与集成回归 |
| `internal/launcher/wsl_opencode_mcp.go` | SA14 三态读取、json+jsonc 并集、尾逗号剥离、未知不写入、预览结构化标记 |
| `internal/launcher/wsl_opencode_mcp_s4_test.go` | SA14 全部钉子 |
| `app_wsl_supply_s4.go`（仅 OpenCode override 段，diff hunk 均在 §OpenCode 附加层 MCP 选择内） | override 代际绑定、预览行映射纯函数；补全轮：wslTargetResolver 窄接口 + 实时身份解析（保存/消费两侧）、删除旧缓存读函数 |
| `app_wsl_supply_s4_test.go` | 绑定/重装不继承/未知拒绝保存/行映射测试；补全轮：不检查即失效回归钉子、实时解析失败/unknown 拒绝保存重写 |
| `internal/piplugin/service.go` | 补全轮：`toStringSlice` → `filterSlice` 三态读面（键省略→nil、显式 []→非 nil 空切片、非数组异常值按收紧面全关） |
| `internal/piplugin/target.go` | 补全轮：`EntryFilters` 三态类型 + 包级函数 `EntryFiltersFor`（不进 Wails 绑定面）；`WindowsEntryFilters` 原签名保留、注释更新 |
| `internal/wslsupply/state.go` | 补全轮：`PluginFilters` JSON 标签去 omitempty（空切片序列化 []、null 与缺失可区分）——仅该表达，其余段落归 C2 切片未动 |
| `internal/piplugin/target_test.go`、`internal/piplugin/service_test.go` | 补全轮：三态读面/非数组收紧面钉子；旧契约断言按新规格同步（空数组 → 非 nil 空切片） |

未改：coordinator.go/types.go、其它 adapters、WSLCLISettings.vue、frontend/wailsjs（**无任何 Wails 公开方法签名或 DTO 形状变化**——`SetWSLOpenCodeMCPSelection([]string)` 等签名不变；新增 `EntryFiltersFor` 为 piplugin 包级函数、`wslTargetResolver` 为 app 内部接口，均不进绑定面，wailsjs 生成物无需再生成）。

**回滚方式**：worktree 未提交；`git -C .amagi-worktrees/wsl-goal-plugins checkout -- <上表 14 文件>`（或 Leader 汇总 diff 时剔除本 worktree）即可整体回退；无配置/凭据/用户现场变更。

## 三、验证证据（真实命令，cwd=worktree）

| 命令 | 结果 |
|---|---|
| `go test ./internal/wslsupply -run 'TestPlugin' -count=1`（另加 `-race`） | ok / ok（含全部新 SA 钉子） |
| `go test ./internal/wslsupply -count=1` | FAIL 仅 `TestScanSkillCompatChecks`、`TestScanSkillLinkNameExcluded` 两条（SA-18 第 2/3 条，skills 扫描 fixture 面，归 SA-05 切片；审计基线即红，非本轮引入） |
| `go test . -count=1`（root 包全量） | ok（6.2s） |
| `go test ./internal/launcher ./internal/piplugin -count=1` | ok / ok（piplugin 含补全轮三态读面钉子） |
| `go build .`；`GOOS=windows GOARCH=amd64 go build .`；`GOOS=windows go vet . ./internal/wslsupply ./internal/launcher ./internal/piplugin` | 全部通过（Windows 交叉编译含补全轮 5 文件） |
| `go test . -run 'TestWSLOpenCodeMCP|TestPiPlugin|TestImportWSL' -race -count=1` | ok（补全轮 app 面 race） |
| `gofmt -l`（全部 14 个改动文件） | 空（补全轮 5 文件 gofmt -w 后复验） |
| 前端 | 无改动 → 未跑前端 test/build（wailsjs 未动；`piPlugin.ts` outcome 联合已含 `skipped`） |

embed 缺失处理：按任务指示复制主树 `frontend/dist`、`mobile/dist` 至 worktree（gitignore 覆盖，不入 diff）。

## 四、未覆盖项与风险

1. **无 Windows 真机**：SA16 的 Linux 子进程取消语义（真实 wsl.exe 终止是否停止 distro 内 npm/git/pi 子进程——**仍未实测，边界保留**）、SA12 真机 `pi install` 后过滤对象（含空数组形态）共存行为（本轮语义依据为 pi 官方文档+piplugin 同语义实现）、SA14 真实 opencode 对 json/jsonc 双入口的优先级（本轮取并集为保守面）、SA14 补全轮实时身份解析在真机 wsl.exe 上的探测耗时（本轮 30s 预算为保守值）——均未真机验证，不宣称已闭环。
2. 补全轮闭合的 3 项缺口（SA12 `[]` 三态、SA02 联合键隔离、SA14 实时身份）在本 worktree 已有针对性测试证据（见各 SA 段）；克隆 machine-id（同代双目标）场景仍归 C2 的 distro 维度 generation 公式，合入后自动收敛。
3. wslsupply 包两条 skills 测试红灯（`TestScanSkillCompatChecks`、`TestScanSkillLinkNameExcluded`）为其它切片在途项（SA-05/SA-10），审计基线即红，未因本切片变化。
