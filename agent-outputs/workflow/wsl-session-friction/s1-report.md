# P3-S1 实施报告：WSL 配置供给——托管 provider 与共享小文件

> 批次：S1（托管 provider 分发 + 共享小文件 + OpenCode 附加层扩展 + 最小状态 UI，P0 第二批，含 S0 diting full 的 M1/M2/M4 前置门槛修复）· 状态：已实施并验证
> 权威规格：`agent-outputs/workflow/wsl-session-friction/p3-config-supply-design.md`（下称「设计」）；前置审查：`s0-review-diting.md`（PASS_WITH_MINOR，M1/M2/M4 先修后做）
> 验证环境：Windows 宿主（go1.25.5 windows/amd64，经 WSL bash + go.exe，GOCACHE/GOPATH 经 cmd 内联 env）；wails v2.11.0 `generate module` 再生成绑定；CI 权威全量在 macOS（见 §6 环境差异）

## 0. 改动总览（17 文件修改 + 16 文件新增）

新增（Go）：`internal/wslsupply/managed_models.go`（pi/managed-models 适配器）、`internal/wslsupply/shared_files.go`（vision-export / modality-kb 适配器）、`internal/launcher/wsl_managed_models.go`（托管候选构建）、`internal/launcher/wsl_opencode_mcp.go`（MCP 形态检查）、`internal/platform/wsl_commands.go`（批量 command -v）、`app_wsl_supply.go`（App 编排面：业务快照源/启动闸门/异步推送/状态面/同意入口/附加层门面）。
新增（测试）：`internal/wslsupply/{s1_adapters_test.go, submit_primitive_test.go, s1_e2e_windows_test.go}`、`internal/launcher/{wsl_managed_models_test.go, wsl_opencode_mcp_test.go, wsl_agent_config_idempotent_test.go}`、`app_wsl_supply_test.go`、`pi_wsl_provider_build_windows_test.go`。
新增（前端）：`frontend/src/api/wslSupply.ts`、`frontend/src/__tests__/api/wslSupply.test.ts`；`frontend/wailsjs/*` 经 `wails generate module` 再生成（非手改）。
修改：`internal/wslsupply/{types,projection,coordinator,state,compensation,projection_test}`（接口演进 + M1/M2 + 同意门 + 冻结登记）、`internal/launcher/{provider_sync,wsl_agent_config}`（严格解析字节变体 + 零写入路径）、`internal/config/modalities_store.go`（导出键规范化）、`app.go`（装配/启动闸门/构建失败阻断/OpenCode 新失败语义/标脏接真实调度）、`pi_login_launch.go`（登录供给单元扩展 + verdict 降级）、`launch_executor.go`（effect 单元能力过滤）、`pi_login_wsl_test.go`（S0 断言随 S1 契约更新 + effect 过滤测试）、`frontend/src/views/settings/WSLCLISettings.vue`（配置状态区）。

## 1. 前置门槛修复（s0-review-diting M1/M2/M4）

| 门槛 | 修复 | 取舍说明 | 针对性测试 |
|---|---|---|---|
| **M1** Compensate 写路径无锁 | `coordinator.go::Compensate` 整批持 `c.applyMu`（与 Apply 同一串行化边界，对齐设计 §七「协调器按目标根/文件串行化」对补偿写入的声明） | 整批而非逐 handle 加锁：CAS 判定与恢复写跨 handle 无交错价值，且整批下锁序简单（applyMu → state.mu 叶子） | `TestCompensateApplySerialized`：4 轮「Apply 更新 + Compensate 恢复」栅栏真并发；chmod 探测入口重叠检测（30ms 宽窗口）断言 overlap=0；终态一致（完整修订或首建删除，无撕裂/无 staging 残留） |
| **M2** chmod 失败删目标不恢复前像 | `submitFile` 增 sensitive 分叉：敏感单元（models.json/视觉清单，内嵌 apiKey）维持 fail-closed 删除（宽松明文不留盘）；普通单元优先经唯一临时名恢复前像，恢复也失败才整体清理（绝不落半提交） | 选「恢复前像」而非「整体拒绝并清理」：设计 §六要求普通投影失败后「旧 WSL 配置可读且安全→启动继续」，恢复前像保住可读性；恢复写不做 chmod 补偿（原内容无密钥，umask 默认权限可接受——有密钥单元不会走该分支） | `TestSubmitFileChmodFailureRestoresPreimage` 三子测：update→前像恢复（rev1 回盘）、first-create→删除新增、sensitive→维持删除 |
| **M4** 并发测试被 applyMu 串行化，未真并发验证唯一名 | `TestSubmitFilePrimitiveTrueConcurrency` 直接并发调用私有 `submitFile`（绕开 applyMu 的写入路径级并发） | 生产同目标写入由 applyMu 串行化；本测钉原语自身的唯一名性质。实测发现：Windows 下无锁同文件并发的 rename/read-back 存在瞬态共享冲突（SHARING VIOLATION）——不属唯一名破坏（CreateTemp 无 already-exists），测试按「唯一名成立 + 终态完整 + 无残留」断言，共享冲突类错误容忍并记录（生产不可达该形态） | 三子测：distinct-files 8 并发全成功零残留、same-file 8 并发（already-exists 类错误判失败，其余瞬态容忍 + 目标完整）、chmod 故障 6 并发（前像恢复/删除一致，无撕裂） |

M3（app.go 陈旧注释）：核实 HEAD 已不存在该文案（`grep "载荷内嵌同步片段" app.go internal/pty/service.go` 零命中），无需处理。

## 2. 分发设计对照（S1 范围 → 实现位置）

| 任务书条款 | 实现位置 | 状态 |
|---|---|---|
| 复用 provider builders + Reconcile 严格解析/命名空间语义构建期望集 | App 快照源 `wslPiManagedSource.DesiredPiManagedModels`（app_wsl_supply.go）：与宿主 `syncProvidersToHarnessesLocked` 同 builders/同桶（openai）/同「无模型/无 baseURL 不发布」过滤；候选构建 `launcher.BuildWSLPiManagedModelsCandidate`（严格解析复用 `readStrictJSONObject` 抽出的 `parseStrictJSONObject`；amagi-* 整体替换=mergeProviderConfig 语义变体） | ✅ |
| 不复制宿主整文件；非 amagi provider/未知顶层字段保留 | merge 保留域外 providers 与全部顶层字段（`TestBuildWSLPiManagedModelsCandidateMerge` 断言 user-custom/equivalence 保留、amagi-gone 被 prune） | ✅ |
| 经 wslsupply 分发 | `wslsupply.PiManagedModelsAdapter`（Unit=pi/managed-models，Policy=ManagedNamespace，Sensitive=true）：Plan 纯计算（来源规范化 hash + 目标 hash 双比对），Candidate 生成候选，提交走协调器私有 staging 原语（唯一临时名/0600/rename/distro 内 chmod 600/读回校验） | ✅ |
| 启动 lazy | `LaunchPiSession` WSL API-provider 分支：构建/overlay 之前 `runWSLManagedModelsAtLaunch`（损坏目标→中文阻断——后续 overlay 的宽松 merge 不得触碰损坏文件；其余失败→告警放行，由既有 overlay 回退语义决定）；登录路径并入 A2 闸门同一供给运行（`defaultRunWSLLoginSupply` 单元=[a2, managed-models]，`verdictForWSLLoginSupply` 把 managed-models 按「可选普通托管面」降级告警，A2 语义逐字不变） | ✅ |
| 保存标脏接真实调度 | `markWSLSupplyDirty` → MarkDirty 后 `scheduleWSLSupplyPush`（goroutine + single-flight 补推合并）；推送守卫：distro Running（`wslDistroRunningFn`=WSLDistroStates）且无活动会话（Sessions.List 任一 running 则推迟）才推 pi+shared 两作用域；停机 distro 只保留待同步标记不唤醒 | ✅ |
| Pi 登录 contextWindow 窄字段托管 | managed-models 的 LoginOverrides（cw>0 补齐语义，复用 `overrideMapAt` 嵌套校验/兄弟保留——与 `UpsertPiLoginModelOverride` 同合并语义）；**S1 取舍**：仅增改不清除（cw=0 的 distro 侧清除属 S2 接管框架，见 §5） | ✅ |
| 构建部分失败不以残缺集合 prune | 快照源把构建失败者记入 `RetainProviders`，`mergeManagedNamespace` 保留其既有条目；失败清单经 Candidate Notes 并入 UnitResult.Detail 与日志 | ✅ |
| 选中 provider 不可构建→阻断（中文，不静默换默认模型） | `LaunchPiSession` cfgErr 分支：WSL 路径 `wslPiProviderBuildError` 阻断（piProviderMapping 丢 baseURL 非等价回退）；Windows 路径维持既有回退（不在本批写面，报告披露） | ✅ |
| amagi-media-models.json 单向受管导出 + per-target 同意 | `VisionExportAdapter`（RequiresConsent=true；协调器 Plan 前统一同意门→awaiting-consent 阻断）；同意经 `Coordinator.GrantUnitConsent`（按目标 generation 登记，distro 重装自动失效）；发布字节逐字来自宿主导出（v1.5 schema 不扩充）；normalized（剥 updated_at）hash 驱动零动作 | ✅ |
| 0600 + 0700 私有 staging | 提交原语 CreateTemp（0600 创建）→ rename → distro 内 chmod 600（真机 e2e 从 distro 内 stat 实证，§4 验收 4）；`.agents` 目录不改权限（设计：不为同步递归改用户树） | ✅ |
| updated_at 变化但规范化内容不变→零动作 | `normalizedStripUpdatedAt`（canonical map 序列化）；`TestVisionExportConsentFlow` 断言仅时间戳变化→UnitFresh+零 chmod | ✅ |
| amagi-modalities.json schema 校验后补缺；冲突保留不反写 | `ModalityKBAdapter`：宿主/distro 双侧 version=1 schema 校验（不符→拒绝不覆盖）；按 `config.NormalizeModalityKBModelID`（本批从 config 导出，避免同规则双实现漂移）规范化 ID 补缺；同 ID 结论不同→保留 distro + Notes 冲突报告 | ✅ |
| 撤回语义：关闭 vision_export→未手改条目撤回；冲突/不可达「未确认撤回」 | 来源内容变化（含撤回）→正常更新目标（撤回落地）；目标被手改（frozen）且来源已变化→BlockedReason=withdrawal-unconfirmed-frozen，状态面中文「密钥副本未确认撤回」，目标内容原样保留（不虚报已删除）；不可达→RunUnits 全单元 blocked（target-unresolved），失败面可见 | ✅ |
| OpenCode 保留 WriteWSLOpenCodeAgentConfig + 附加层机制 | 机制不变（`~/.config/opencode/amagi-codebox.json` + OPENCODE_CONFIG Linux 路径）；写入前 `launcher.AdaptWSLOpenCodeMCP` 确定性形态检查 | ✅ |
| MCP 形态检查（§五 S1 子集） | npx.cmd/npm.cmd 裸名→仅 `platform.WSLResolveCommands`（登录 shell 批量 command -v，单次 wsl.exe）实测解析到原生 npx/npm 才映射；盘符路径/.exe/.ps1/.bat/.cmd/cmd.exe/PowerShell/参数盘符→丢弃+中文「需手动适配」（不生成假可用条目）；HTTP/远程（url 字段）保留原样 | ✅ |
| distro 全局同名 MCP 冲突默认保留本地 | 读取 distro `~/.config/opencode/opencode.json`（JSON/JSONC 注释剥离启发式；不可读/不可解析→跳过冲突检查不误伤）提取 mcp 名；同名 server 不写入附加层+报告；覆盖选择 UI 留 S2 | ✅ |
| OpenCode 失败语义变更（已批准） | `prepareWSLOpenCodeLayer`：无 distro/解析失败/写入失败→MarkFailed+中文阻断（「不再以无 provider 配置静默启动」）；进程内记录附加层状态（状态面） | ✅ |
| 最小状态 UI | `App.GetWSLConfigSupplyStatus`（脱敏快照：distro + providers/vision-export/modality-kb/opencode-layer 四面 已同步/陈旧/失败/冻结/待授权/未检查 + 最后检查时间 + pendingPush）；前端经 `api/wslSupply.ts` wrapper 消费（不手改 wailsjs）；WSLCLISettings.vue 卡片下「配置状态」区 + 视觉清单共享同意按钮（本批唯一操作入口，密钥跨界授权）；severity-only 不触碰 envcheck AllOK（独立区块，不进 CheckIssue） | ✅ |
| 零写入路径 | 托管单元 ActionNone（来源+目标双未变）零提交零 chmod；launch overlay `WriteWSLPiAgentConfig`/`WriteWSLOpenCodeAgentConfig` 增 skip-if-identical（序列化字节相等→跳过落盘与 chmod 补偿；权限若被手改宽，待下次内容变化随完整路径收紧——取舍已在代码注释） | ✅ |
| 明确不做 | Pi settings/amagi.json/mcp.json 导入（S2）、skills（S3）、Claude/Codex（S4）、插件（S5）、B5 distro 选择、详情页/操作按钮（除同意入口）——均未触碰；omp 未触碰（B7 保持关闭，WriteWSLOmpAgentConfig 原样） | ✅ |

## 3. 接口变化（新公开面）

```go
// internal/wslsupply
const CLICommon CLI = "shared" // ~/.agents 作用域
const (
    UnitPiManagedModels   UnitID = "pi/managed-models"
    UnitSharedVisionExport UnitID = "shared/vision-export"
    UnitSharedModalityKB   UnitID = "shared/modality-kb"
)
const PolicyManagedNamespace Policy = "ManagedNamespace"
var ErrTargetCorrupt = errors.New(...) // 两级损坏保护哨兵（Detail=target-corrupt）
type ProjectionAdapter interface{ ...; Policy() Policy; Sensitive() bool; RequiresConsent() bool; Candidate(...) (ProjectionCandidate, error) }
type ProjectionCandidate struct{ Content []byte; SourceHash string; TransformVersion string; Notes []string }
func NewPiManagedModelsAdapter(PiManagedModelsSource) *PiManagedModelsAdapter
func NewVisionExportAdapter(SharedFileSource) *VisionExportAdapter
func NewModalityKBAdapter(SharedFileSource) *ModalityKBAdapter
func (c *Coordinator) HasUnit(UnitID) bool          // 能力探测（启动链按此降级）
func (c *Coordinator) GrantUnitConsent(ctx, distro, cli, unit) error
// StateEntry/UnitStatus 新增 Consented；StateEntry 新增冻结登记语义（Frozen/FrozenReason 既有字段启用）
// internal/launcher
func BuildWSLPiManagedModelsCandidate(existing []byte, desired map[string]any, retainIDs []string, overrides []PiLoginModelOverride) ([]byte, error)
func AdaptWSLOpenCodeMCP(distro string, cfg map[string]any) (map[string]any, []string) // 中文诊断
// internal/platform
func WSLResolveCommands(distro string, commands ...string) map[string]string
// internal/config
func NormalizeModalityKBModelID(model string) string
// 根包（App 绑定面，wailsjs 已再生成）
func (a *App) GetWSLConfigSupplyStatus() WSLConfigSupplyStatus
func (a *App) GrantWSLVisionConsent() error
```

**行为变更声明**（含已批准项）：① OpenCode WSL 附加层写失败/解析失败/无 distro→启动失败（原：日志后无 provider 启动）——主上批准，测试钉新文案；② Pi WSL 路径选中 provider 构建失败→阻断启动（原：回退内置 provider 静默换模型）；③ Pi WSL 启动前新增托管分发闸门（损坏目标阻断）；④ 登录供给运行单元从 [a2] 扩为 [a2, managed-models]（裸协调器自动降级 A2-only，S0 测试兼容）；⑤ WriteWSL*AgentConfig 增零写入跳过（内容一致时不再刷新文件/mtime）。Windows 侧 Pi 构建失败回退、宿主 reconcile 语义、omp 全链路——零变化。

## 4. 独立验收证据（任务书 8 项）

| # | 验收 | 证据（真实入口/命令与结果） | 状态 |
|---|---|---|---|
| 1 | 新/旧 distro 不重装即获得托管更新 | `TestPiManagedModelsPublishAndMerge`（预置旧播种形态文件：user-own/equivalence 保留、旧 amagi-stale 被 prune、新 amagi 写入、登录覆写落盘）+ 启动闸门 `TestRunWSLManagedModelsAtLaunchVerdicts` + 真机 `TestManualRealWSLManagedModelsSupply`（真实 Ubuntu，预置非 amagi provider 后单次 RunUnits 完成托管更新，非 amagi 保留实证）；安装（wslsetup）零参与 | ✅ PASS |
| 2 | 非 amagi 保留；损坏 JSON 不覆盖（两级） | 一级语法/根形态 + 二级嵌套形态：`TestBuildWSLPiManagedModelsCandidateCorruptRefused`（语法/空文件/根非对象/providers 非对象→ErrWSLManagedModelsCorrupt）+ `...NestedShapeCorrupt`（modelOverrides 非 object）+ `TestPiManagedModelsCorruptTargetRefused`（协调器层 Detail=target-corrupt 且原文件字节不动）+ `TestModalityKBGapFillAndConflicts`（schema 版本不符→拒不改写）；非 amagi 保留：Merge 测试 + e2e（distro 内 cat 实证 user-own 在场） | ✅ PASS |
| 3 | 构建部分失败不误 prune；选中不可构建阻断 | `TestPiManagedModelsPartialBuildFailureRetains`（amagi-bad 失败保留旧条目、amagi-good 正常更新、Detail 携带构建失败清单）+ builder 侧 retain 断言；阻断：`TestWSLPiProviderBuildFailureBlocksLaunch`（Windows 全链路：真实 LaunchPiSession + WSL resolver + 无 baseURL provider →中文阻断「未改用默认模型」，本机 AMAGI_WSL_E2E=1 实跑 PASS）+ `TestWSLPiProviderBuildErrorText`（文案跨平台钉） | ✅ PASS（Windows 全链路测试 CI 不执行：macOS 不编译、windows 线仅 compile-check——与 wslsetup 布局同限制，§6 披露） |
| 4 | 导出 v1.5 收录规则不扩大；撤回失败可见；0600 从 distro 内 stat 实证 | 收录不扩大：发布字节逐字来自宿主导出文件（`TestVisionExportConsentFlow` 断言 byte-equal，schema 零改写）；撤回失败可见：`TestVisionExportHandEditFreezesAndWithdrawalUnconfirmed`（手改+来源撤回→withdrawal-unconfirmed-frozen，目标保留密钥字节且状态面冻结+中文「密钥副本未确认撤回」）与不可达 blocked 面；0600 实证：真机 `TestManualRealWSLVisionExportPublish`（Ubuntu 内 `stat -c %a` = **600**，e2e 输出 `vision export published with fake content; distro stat mode=600`；隔离规约：写入仅落 distro /tmp/amagi-wslsupply-e2e，内容为桩字节无真实密钥）+ `TestManualRealWSLManagedModelsSupply`（models.json stat=600） | ✅ PASS |
| 5 | OpenCode 全局用户配置仍生效；选中模型失败不静默换模型 | 全局生效：机制保留（附加层文件+OPENCODE_CONFIG，冲突同名默认保留本地不写入——`TestAdaptWSLOpenCodeMCPGlobalNameConflictKeepsLocal`，含 JSONC 注释形态读取；仅写 amagi-codebox.json 命名空间私有文件，opencode 全局 opencode.json 不触碰）；新语义：`TestPrepareWSLOpenCodeLayerFailureSemantics`（写入失败→中文阻断含「不再以无 provider 配置静默启动」+状态面记失败）+ `TestAdaptWSLOpenCodeMCP*`（npx.cmd 仅验证过映射/未验证丢弃、Windows 专属形态丢弃、远程保留） | ✅ PASS |
| 6 | 无变化调用跨界次数有基线记录；启动两次第二次零写入 | 真机基线（e2e 日志）：每 RunUnits 2 次身份 wsl.exe（ResolveTarget + Apply 复核，设计 §三 A2 内部探测另计）；第二次零写入实证：`TestManualRealWSLManagedModelsSupply`（mtime 1792128501 不变 + UnitFresh）+ 假探面 `TestPiManagedModelsPublishAndMerge`（第二次 chmod 调用零增量）+ overlay 半边 `TestWriteWSLPiAgentConfigSkipsIdenticalContent`/`TestWriteWSLOpenCodeAgentConfigSkipsIdenticalContent`（内容一致跳过 chmod 与落盘；变化恢复完整路径） | ✅ PASS |
| 7 | M1/M2/M4 各有针对性测试 | §1 表列三测；另 M4 实测发现并如实记录 Windows 无锁同文件并发的瞬态共享冲突形态（非唯一名破坏） | ✅ PASS |
| 8 | go build/vet/gofmt 全绿；注释引号复核；前端对应测试 | `go build ./...` RC=0、`go vet ./...` RC=0（全仓）；gofmt：全部改动文件 `-l` 空（`internal/config/defaults.go`/`defaults_test.go` 的既有未格式化非本批改动，未触碰）；注释引号：`git diff` 新增行弯引号计数 **0**（「」原样）；前端：`vue-tsc --noEmit` RC=0、`vitest run src/__tests__/api`（含新 wslSupply.test.ts 3 测）全过、`wails generate module` RC=0（绑定再生成，HEAD 基线先验证字节稳定） | ✅ PASS |

**定向回归全绿**：wslsupply 包全量；launcher 包除 4 个既有环境失败（§6）外全量；根包供给/登录/计划/restart/绑定域（TestWSLLoginSupplyFailureMatrixBothLinks、TestPiLoginPlannerWSLGateLifted（断言随 S1 契约更新为双单元）、TestPiLoginDesktopWSLGateSync、TestWSLSupplyEffectFiltersUnitsByCapability、TestM004Restart、TestAuthorityRestart*、TestBind*、TestBuildPlanFiveCLICanonicalStructure 等）全过；envcheck/config 除既有环境失败外无新增失败。

## 5. 设计留白处的微观决策记录

1. **登录 contextWindow 仅补齐不清除**：S1 无接管 UI，无法区分「我们的旧覆写」与「WSL 用户自定义同字段」；cw=0 的清除留 S2 接管框架（设计 D2 该行原文即要求「WSL 自定义同字段须先确认接管」）。
2. **单元清单能力过滤（HasUnit）**：登录供给/启动闸门/effect 三处按协调器注册面过滤单元——裸协调器（S0 布局的测试）自动降级 A2-only 不报 unknown unit；生产装配（NewApp→newWSLSupplyCoordinator 注册三适配器）恒全量。effect 侧过滤使 remote 计划（描述性）与执行面（能力）解耦。
3. **verdict 分级**：managed-models 失败/阻断=可选普通托管面（告警放行，A2 语义逐字不变）；未知单元失败仍 fail-closed（保守）。API-provider 路径独立闸门：仅 target-corrupt 阻断（overlay 宽松 merge 会破坏损坏文件），其余失败告警沿用既有回退。
4. **vision 面陈旧映射**：pendingPush 全局标记 + 各面 outcome 推导；providers 来源变更同时标视觉面陈旧（api_key/baseURL 来自 provider）——S1 不做来源→面精确路由。
5. **OpenCode 附加层状态为进程内记录**：附加层内容随启动变化、「陈旧」不适用；持久化无消费者，重启回「未检查」诚实呈现。
6. **冲突全局配置读取失败→跳过冲突检查**（不误伤 server 写入）；JSONC 剥离为启发式（字符串字面量感知的注释剥离），仅服务冲突名提取。
7. **共享面推送的日志降噪**：awaiting-consent/frozen/unmanaged-existing 属预期态不刷错误日志（状态页可见），其余失败 WARN。
8. **单飞推送实现中发现并修复一处自死锁**（running 置位后未释放入口锁）——`TestPushWSLSupplyGuards` 首跑即暴露，修复后过（测试有效性实证）。

## 6. 环境差异与既有失败（非本批引入）

- 本机 Windows 全量 `go test ./internal/launcher ./internal/envcheck ./internal/config ./`存在与 s0-report §5 同款环境失败：POSIX 权限位断言（0666≠0600：TestWritePi/OmpAgentConfig*TightPerms/Upgrades、TestModalityKBRoundTrip、TestExportVisionModels_MarkedPresetFields）、日志句柄锁 TempDir 清理（TestAppConfigEventOnUserChange 等 4 个）、npm 路径解析（TestOpenCodeNPMGlobal*）、fnm 符号链接需特权（TestDarwinFNM*）。CI（macOS 全量）为权威。
- `-race` 不可用（CGO_ENABLED=1 需 gcc，本机无编译器）；M1/M4 并发测试以重叠检测+宽窗口机制替代。
- Windows 全链路阻断测试与 wslsetup 播种测试同布局限制：CI 两条线均不执行断言（macOS 不编译 / windows 仅 compile-check），本机实跑为证据；diting S0 审查建议区第 2 条（本地 Windows 门禁）对该类测试同样适用。
- `internal/config/defaults.go`/`defaults_test.go` 在 HEAD 上即不满足 gofmt（非本批文件，未触碰）。

## 7. 未覆盖项 / 风险 / 回滚

**未覆盖**：① remote/v1 的 pi **API-provider** WSL 会话（buildPiOmpPlan 路径）与 remote OpenCode WSL 附加层——S0 只统一了登录 A2 链路，S1 的托管分发接的是桌面启动闸门与登录双链路（effect 单元已支持，接入 API-provider remote 计划留 S2）；② 视觉清单撤回未完成时的「阻断消费旧清单」（设计 §六表行）按任务书仅做可见性（状态+日志），未阻断启动；③ docs 陈旧项（`docs/developer/pi-login-providers.md` 载荷段、pty/service.go:949 注释）不在本批写面，未动；④ 覆盖选择 UI、接管/保持独立操作、详情页——S2。
**风险**：① 附加层/共享面状态为进程内或单 distro 状态清单，多 distro（B5 后）需扩展——S1 单目标内自洽；② 零写入跳过不重收紧权限（手改宽后待内容变化才恢复 0600）——取舍见代码注释；③ modality 冲突判定只比 vision/video 结论（source/probedAt 差异不算冲突）——知识层语义，保守。
**回滚**：还原本批 33 个文件（`git checkout` 定向还原）即可；已分发的 distro 内容（托管 models.json/amagi-* 条目、共享文件）按 lastWrittenHash 匹配可手动清理，用户手改内容不受影响；**已同意的视觉清单密钥副本**：回滚后状态清单仍在 `~/.amagi-codebox/wsl-config-supply.json`（Consented 登记），distro 内 `~/.agents/amagi-media-models.json` 须单独确认删除（删除≠吊销 key，提供商侧吊销另行处理）。不得以恢复 ensurePiConfig 整树播种作为回滚手段（设计 §八）。

## 8. 验证命令记录（cwd=仓库根，WSL bash → cmd.exe 内联 env 调 Windows go.exe/wails.exe）

```
go build ./...                                    RC=0
go vet ./...                                      RC=0
gofmt -l <全部改动文件>                            空
go test ./internal/wslsupply/ -count=1            ok
go test ./internal/launcher/ -count=1             仅 4 既有环境失败（§6），其余全过
go test ./ -count=1 -run <TestWSL*|TestPiLogin*|TestM004Restart|TestAuthorityRestart|TestBind*|TestBuildPlanFiveCLI|TestRunWSLManagedModels|TestPushWSLSupply|...>  全过
AMAGI_WSL_E2E=1 go test ./internal/wslsupply/ -run TestManualRealWSL           3/3 PASS（含 0600 distro 内 stat、零写入 mtime、非 amagi 保留）
AMAGI_WSL_E2E=1 go test ./ -run TestWSLPiProviderBuildFailureBlocksLaunch       PASS（Windows 全链路）
wails generate module                             RC=0（先在 HEAD 验证基线字节稳定）
cd frontend && node node_modules/vue-tsc/bin/vue-tsc.js --noEmit                RC=0
cd frontend && node node_modules/vitest/vitest.mjs run src/__tests__/api        10/10 PASS（含新增 3）
```

## 9. S1 diting FIX 修复轮（s1-review-diting.md，VERDICT: FIX → 5/5 修复）

| # | Finding | 修复 | 测试证据 |
|---|---|---|---|
| **F1** Critical：providerSyncMu 死锁（WSL 构建失败阻断分支在 Lock/Unlock 区间内 return，锁永久泄漏，此后 provider 保存/Pi 启动/SaveOpenCodeConfig 全部挂死） | `app.go`：临界区整体抽取为 `writePiProviderModelsAtLaunch`（provider 构建/合并写入/env 兜底注入），内部 `defer a.providerSyncMu.Unlock()`——任何 return 路径结构性释放锁（v1.3.13 同位置事故的防再犯形态）；调用点仅消费 (settings, err)，错误在锁外上抛 | **锁可用性测试（必修项）**：`TestPiProviderWriteLockReleasedOnBuildFailure`（跨平台：helper 以参数接收 wslDistro，构建失败先于任何 WSL I/O——阻断返回后 `providerSyncMu.TryLock()` 必须成功；二次调用同样快速错误返回且锁仍可用）+ Windows 全链路 `TestWSLPiProviderBuildFailureBlocksLaunch` 追加同款 TryLock 断言与二次启动不挂死断言（本机 AMAGI_WSL_E2E=1 实跑 PASS） |
| **F2** Major：PendingSources 永不清除（pendingPush 恒亮、「已同步」不可达） | `coordinator.go`：新增来源常量 `DirtySourceProviders`/`DirtySourcePiLoginPresets`（App 两处 markWSLSupplyDirty 调用点改用常量）与映射表 `dirtySourceUnits`（providers→三托管单元；pi-login-presets→managed-models；未知来源保守不清）；`Apply` 尾部 `clearSatisfiedDirtySources` 按**来源累计**「标记后已成功（Applied/Fresh/WarnDegraded）」单元集（`StateFile.PendingProgress`，MarkDirty 重新标脏时重置）——累计覆盖全部滋养单元才清除来源标记。跨累计是必需的：一个来源的单元跨 pi/shared 两次 RunUnits，单次运行不足以判定；启动补漏与异步推送走同一 Apply 收敛路径 | `TestDirtySourcesClearedAfterFullSuccess`（标脏两来源→pi 运行成功清 pi-login-presets、providers 保留→vision 未同意阻断仍保留→同意+vision+modality 成功→全部清空）+ `TestDirtySourcesKeptOnFailure`（chmod 注入失败保留标记；恢复重跑成功后清除=补漏收敛）+ 既有 `TestMarkDirtyMergesAndPersistsWithoutProbing` 不回归 |
| **F3** Major：GrantUnitConsent 跨代复制旧代 hash（distro 重装后重新同意 → local-deletion-frozen，同意按钮在唯一需要它的场景失效） | `coordinator.go::GrantUnitConsent`：仅 `prev.Generation == target.Generation` 才保留 SourceHash/LastWrittenHash 基线；跨代授权只写授权位/consent 时间戳/新 generation（新代无任何基线，upsertEntry 换代分支不再收到旧代 hash） | `TestGrantUnitConsentCrossGeneration`（gen1 同意+发布→MachineID 变化模拟重装+删目标→重新同意→首次发布 **Applied** 且字节逐字落地；状态文件断言仅存一份 lastWrittenHash=新代登记） |
| **F4** Minor：LastInspectedAt 全局单值跨 CLI 混合（pi 探测时间错配 shared 面板） | `StateFile` 新增 `LastInspectedAtByCLI map[CLI]time.Time`（additive 字段，schemaVersion 维持 1，旧清单双向兼容）；Inspect 双写（全局字段保留仅为旧读端兼容）；`GetStatus` 只读该 CLI 作用域的实际探测时间，无记录 → Checked=false（诚实「未检查」而非沿用其它作用域时间） | `TestGetStatusPerCLICheckTime`（pi Inspect 后 pi Checked/shared 未检查；shared 自行 Inspect 后各持 own 时间） |
| **F5** Minor：consented→synced 虚高（授权后发布未落地即显示已同步） | `app_wsl_supply.go::wslFaceFromUnit`：`LastOutcome=="consented"` 独立映射 `consented`（中文「已授权，待发布到 WSL」）；发布成功（written）才 synced。前端 `WSLCLISettings.vue` faceStateText/faceBadgeClass 增对应文案与徽标（字符串状态值，无绑定再生需求） | `TestVisionFaceConsentedNotSynced`（consented→「待发布」态；written→synced） |

**修复轮验证**（cwd=仓库根，同 §8 环境约定）：`go build ./...` RC=0；`go vet ./...` RC=0；gofmt 全部改动文件 `-l` 空；`go test ./internal/wslsupply/ -count=1` 全绿（含 5 个新修复测试）；launcher WSL 域 4 组测试全过；根包供给/登录/计划/restart/绑定域 15 组模式全过；Windows 全链路 F1 测试（AMAGI_WSL_E2E=1）PASS；真机 e2e 3/3 复跑 PASS（0600 distro 内 stat 与零写入 mtime 在状态清单字段扩展后依旧成立）；前端 vue-tsc RC=0、vitest 10/10；新增行弯引号计数 0。

**改动文件（修复轮）**：`app.go`（F1 临界区抽取+帮助函数）、`provider_harness_sync.go`（来源常量+import）、`app_wsl_supply.go`（F5 consented 态）、`internal/wslsupply/coordinator.go`（F2 清除+来源常量/映射、F3 跨代授权、F4 双写与 per-CLI 读）、`internal/wslsupply/state.go`（PendingProgress + LastInspectedAtByCLI 字段与归一）、`frontend/src/views/settings/WSLCLISettings.vue`（F5 文案/徽标）、测试 `internal/wslsupply/s1_fix_round_test.go`（新）、`app_wsl_supply_test.go`（+2 测）、`pi_wsl_provider_build_windows_test.go`（+TryLock 断言）。
