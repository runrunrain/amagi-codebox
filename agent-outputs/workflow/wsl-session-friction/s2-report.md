# P3-S2 实施报告：Pi 配置与 MCP 导入 + remote 计划接入

> 批次：S2（Pi settings/amagi/mcp 白名单投影 + D3 形态转换器全量 + 冻结/接管动作面 + remote API-provider/OpenCode WSL 计划接入，P1 批次）· 状态：已实施并验证
> 权威规格：`agent-outputs/workflow/wsl-session-friction/p3-config-supply-design.md`（下称「设计」）；前置：S0/S1（HEAD=8265ffc + 工作区 S1 未提交件，见 §0 基线说明）
> 验证环境：Windows 宿主（WSL bash + go.exe 1.25.5 windows/amd64，GOCACHE 经 WSLENV 转发；wails v2.11.0 `generate module` 再生绑定）；CI 权威全量在 macOS（见 §6）

## 0. 基线说明（重要）

S1 报告声称「S1 已落地（HEAD）」，但 `git status` 显示 S1 的四个产物仍是**工作区未提交状态**：`app_wsl_supply.go`、`internal/platform/wsl_commands.go`、`pi_wsl_provider_build_windows_test.go`（未跟踪）与 `provider_harness_sync.go`（已修改）。**HEAD 本身不可独立构建**（缺 `WSLResolveCommands`）。本批在这些未提交件之上继续（未改动 `wsl_commands.go`/`pi_wsl_provider_build_windows_test.go`/`provider_harness_sync.go` 的 S1 内容），并用独立 worktree 实证了「TestConfigMutationCAS* 在任何 Windows 机器上失败」是 HEAD 既有事实（§6）。**建议 Leader 提交本批时连同 S1 未提交件一起入库**。

## 1. 改动总览（25 文件修改 + 8 文件新增）

新增（Go）：`internal/wslsupply/keyed.go`（键级冲突引擎：§三表逐键判定 + 扁平路径取/写/删）、`internal/wslsupply/transform.go`（D3 纯函数转换器：npx.cmd/npm.cmd 裸名与标准位置 shim、Windows 专属形态拒绝、args/env 路径映射、%USERPROFILE% 展开、HTTP/远程原样、CollectTransformNeeds 预扫描）、`internal/wslsupply/pi_config.go`（三个键级 TrackedProjection 适配器骨架 + amagi 依赖诊断）、`internal/wslsupply/pi_shapes.go`（settings 白名单 / amagi 键集（复用 piconfig.ValidateAmagiConfig）/ mcp server 级形态）、`internal/platform/wsl_paths.go`（WSLTranslatePaths：批量 wslpath -u + test -e 实测映射）。
新增（测试）：`internal/wslsupply/{transform_test,pi_config_test}.go`、根包 `wsl_s2_remote_test.go`。
修改：`internal/wslsupply/{types,projection,state,coordinator,managed_models,shared_files,projection_test}`（Candidate 携带登记基准 + Subs/Outcome/Detached 状态面 + PreviewUnits/DropUnitRegistration/TakeOverUnit + pi-config 脏来源映射）、`internal/piconfig/service.go`（显式目标窄接口 ReadAgentFileAt/GetAmagiConfigAt/SaveAmagiConfigAt，UNC 写入拒绝）、`internal/platform/wsl.go`（导出 AppendWSLENVForwarding）、`internal/launchplan/types.go`（WSLConfigSupplySpec.OpenCodeLayer + OpenCodeLayerSpec）、根包 `launch_planner.go`（pi API-provider WSL 分支 + OpenCode WSL 层计划）、`launch_executor.go`（层 effect：晚绑定 PTY、OPENCODE_CONFIG+WSLENV 注入、纯配置供给 verdict 分流）、`pi_login_launch.go`（登录供给扩 5 单元 + 三单元 warn 降级）、`app.go`（SaveAmagiConfig/SavePiModelsConfig → pi-config 标脏；executor 注入 openCodeLayerPrep）、`app_wsl_supply.go`（S2 主体：源/事实源/适配器注册/动作面/状态单元详情/双链路共享 verdict/可注入 distro 接缝）、`app_wsl_supply_test.go`/`pi_login_wsl_test.go`（断言随 S2 契约 + 负缓存加固）。
前端：`frontend/src/api/wslSupply.ts`（5 动作 wrapper）、`WSLCLISettings.vue`（Pi 配置投影逐单元区：状态徽标 + 检查内含/预览/应用/保留本地/确认接管按钮 + 逐键预览展开）、`__tests__/api/wslSupply.test.ts`（+4 测）；`frontend/wailsjs/*` 经 `wails generate module` 再生（非手改，二次再生字节稳定）。

## 2. 设计对照（S2 范围 → 实现位置）

| 任务书条款 | 实现位置 | 状态 |
|---|---|---|
| settings.json 字段级白名单（绝不整拷；packages 排除；绝对路径转换或剔除） | `piSettingsShape`：白名单 `retry`（S2 已确认跨平台集；packages/lastChangelogVersion/未知字段显式不携带）；`TransformSettingValue`：字符串叶子独立路径映射/内嵌路径或不可映射→剔除该字段（不做正文替换） | ✅ |
| amagi.json（profile/角色模型/压缩模型/MCP 路由 + 既有验证器 + 未满足依赖报告） | `piAmagiShape`：键集=profile/compaction/concurrency/agents/<角色>/mcp/<段>；候选经 `piconfig.ValidateAmagiConfig` 校验；`amagiDepNotes`：provider 对照 KnownProviderIDs（models.json+builtin 目录）、server 对照 mcp.json，未满足→中文诊断（不阻断、不伪造默认值） | ✅ |
| mcp.json server 级原子单元（转换失败不新增不更新，已有同名保持） | `piMCPShape`：键=mcpServers/<name>；转换拒绝→该键保持目标现状+诊断（`TestPiMCPConversionFailureKeepsUnitAtomic` 两形态：从未写入/已托管后来源变 Windows 专属） | ✅ |
| §三冲突表全量（unmanaged/本地修改/本地删除/双侧同变/来源删除） | `keyed.go::resolveKeyed` 逐键：无记录+目标有键=unmanaged-existing 保留；记录+目标==上次写入→来源变才更新；目标变/删=冻结（双侧同变→both-changed-frozen 保留 WSL 并列报告）；来源删→仅目标==上次写入才移除。比较基准=规范化 JSON hash（空白/键序不算修改），mtime 不选胜者 | ✅ |
| 解除冻结两操作（保留 WSL 停跟随 / 预览后重新接管） | `DropUnitRegistration`（Detached 标记，目标字节不动，停机 distro 也可停跟随）/ `TakeOverUnit`（预览 hash CAS→来源覆写托管域+重建 Subs 基准；旧预览 preview-expired 拒绝） | ✅ |
| D3 确定规则全表（转换器纯函数、多 CLI 复用、不执行命令） | `transform.go` 纯函数：事实（TransformFacts）由 FactsSource 在 Apply 阶段实测（`WSLResolveCommands` command -v + `WSLTranslatePaths` wslpath+存在性 + 已部署资产前缀 .pi/agent·.agents）；探测失败=「不可确认」→转换保守拒绝不生成假可用条目 | ✅ |
| C:\...\npx.cmd 仅识别为已支持工具 shim | `isRecognizedShimLocation`：仅 nodejs 目录/npm 全局 bin 标准位置；`C:\tools\mytool\npx.cmd` 拒绝（文件名相同不证明行为等价，设计 §五原文） | ✅ |
| env 值路径转换（仅声明键）；未知 Windows 路径形态阻止并提示键名 | `knownEnvPathKeys`（S2 收录 NODE_PATH：`;`列表→`:`）；未声明键含盘符形态→拒绝并点名键（`${VAR}` 占位/URL/token 不受影响） | ✅ |
| %USERPROFILE% 仅已知字段+已确认基目录 | 仅声明路径键内展开 USERPROFILE/APPDATA/LOCALAPPDATA（HostHome 确认时）；未知 %VAR%→拒绝要求 Linux 值 | ✅ |
| HTTP/远程保留 URL（localhost 不改宿主 IP） | `TransformMCPServer` 首分支：带 url 整条目原样（env 亦不动） | ✅ |
| D4 动作分离五动作（无强制覆盖全部） | App 面：`CheckWSLPiConfigSupply`/`PreviewWSLPiConfigSupply`/`ApplyWSLPiConfigSupply`/`KeepWSLPiConfigUnitLocal`/`TakeOverWSLPiConfigUnit`；「应用安全更新」=RunUnits 本身（冻结键天然不写） | ✅ |
| 状态页逐单元（相对路径/策略/冻结原因/失败阶段/动作；敏感项只显文件名与方向） | `GetWSLConfigSupplyStatus.Units`（pi/settings·amagi·mcp 三行：relPath 仅文件名、状态/中文原因/可执行动作 key）；mcp.json Sensitive=true（env 可含明文 key→chmod 失败 fail-closed 删除） | ✅ |
| PiConfig 显式目标窄接口（不做进程级 PI_CODING_AGENT_DIR 变更） | `piconfig.{ReadAgentFileAt,GetAmagiConfigAt,SaveAmagiConfigAt}`：显式目录、无 env 变更；UNC 目标写入拒绝（跨界私有文件必须走协调器提交原语——设计 §七「rename 回退不可复用」）；原无 target 方法零变化 | ✅ |
| remote API-provider 与 OpenCode WSL 计划接入 EffectWSLConfigSupply（BuildPlan 纯读） | `buildPiOmpPlan` WSL 分支：供给 effect（managed-models+三配置单元，distro 留空 Apply 解析）替代 ConfigMutation（Windows models.json 归 providerSync）；`buildOpenCodePlan` WSL 分支：层 effect（内容经 SecretBuffer，索引 0）；executor `applyOpenCodeLayer`：与桌面同一 `prepareWSLOpenCodeLayer`，成功后 OPENCODE_CONFIG 注入 PTY env+WSLENV 追加，失败阻断启动 | ✅ |
| 明确不做 | skills（S3）、Claude/Codex 导入（S4）、插件重放（S5）、覆盖选择 UI 超出预览框架、omp（B7）——均未触碰 | ✅ |

**行为变更声明**：① remote pi API-provider WSL 计划不再随启动写 Windows models.json（ConfigMutation→供给 effect；桌面 WSL 路径本就不写，本变更消除双链路差异）；② 登录供给/启动闸门单元从 [a2, managed-models] 扩为 [a2, managed-models, settings, amagi, mcp]（三新单元失败/阻断仅告警——可选偏好面；裸协调器自动降级不报 unknown unit）；③ remote OpenCode WSL 会话从「无 provider 配置静默启动」变为层写入失败即阻断（对齐 S1 已批准的桌面语义）。

## 3. 接口变化（新公开面）

```go
// internal/wslsupply
const (UnitPiSettings UnitID = "pi/settings"; UnitPiAmagi = "pi/amagi"; UnitPiMCP = "pi/mcp")
const (PiSettingsFile = "settings.json"; PiAmagiFile = "amagi.json"; PiMCPFile = "mcp.json")
const DirtySourcePiConfig = "pi-config" // + dirtySourceUnits 映射三单元
type ProjectionAdapter.Candidate(ctx, target, state *StateEntry) // 签名扩展（S1 适配器忽略 state）
type KeyedAdapter interface{ PreviewKeys; AdoptCandidate }       // 键级可选能力面
type ProjectionCandidate struct{ ...; Subs map[string]SubHash; Outcome string }
type StateEntry struct{ ...; Subs map[string]SubHash; Detached bool } // additive，schemaVersion 不变
type UnitStatus struct{ ...; Detached bool }
type SubHash struct{ Source, Written string }
type TransformFacts{ ResolvedCommands; PathMappings; PrefixMappings; HostHomeWindows } / TransformFactsSource
func TransformMCPServer / TransformSettingValue / MapWindowsPath / IsWindowsDrivePath / CollectTransformNeeds
func NewPiSettingsAdapter / NewPiAmagiAdapter / NewPiMCPAdapter(source, facts)
func (c *Coordinator) PreviewUnits / DropUnitRegistration / TakeOverUnit
type UnitPreview / KeyPreview
// internal/platform
func WSLTranslatePaths(distro, ...paths) map[string]string
func AppendWSLENVForwarding(env, extraKeys...) []string
// internal/piconfig
func (s *Service) ReadAgentFileAt / GetAmagiConfigAt / SaveAmagiConfigAt
// internal/launchplan
type WSLConfigSupplySpec struct{ ...; OpenCodeLayer *OpenCodeLayerSpec }
type OpenCodeLayerSpec struct{ Content SecretBufferRef }
// 根包（App 绑定面，wailsjs 已再生）
func (a *App) CheckWSLPiConfigSupply / PreviewWSLPiConfigSupply / ApplyWSLPiConfigSupply /
    KeepWSLPiConfigUnitLocal / TakeOverWSLPiConfigUnit
```

## 4. 独立验收证据（任务书 7 项）

| # | 验收 | 证据 | 状态 |
|---|---|---|---|
| 1 | 面板修改进入未手改 WSL；手改后再保存必须保留 WSL | `TestPiSettingsProjectionLifecycle`（建立→白名单外字段不携带→来源变更新→无变化零写入→手改+来源再保存=both-changed-frozen、WSL 值 900000 保留）+ `TestPiMCPServerLifecycle`（混合：未手改 server 更新/手改 server 保留/用户自有 server 原样） | ✅ PASS |
| 2 | 来源删除/目标删除/旧播种树无清单/未知字段/损坏文件/双侧同变——表驱动逐态 | `TestPiKeyedConflictTable`（7 子态：来源删未手改→移除且其它托管键保留；来源删+手改→部分冻结（移除照做+手改保留）；目标键删→local-deletion-frozen 不补回；双侧变→both-changed-frozen 保 WSL；旧播种树无清单→unmanaged-existing 字节不变零 chmod；来源未知字段不携带；目标损坏→target-corrupt 原字节不动）+ `corrupt-source` 子态 + `TestPiKeyedStatusSurfacesDetachedAndPartial` | ✅ PASS |
| 3 | 转换器矩阵 §五全表 | `transform_test.go` 8 测：裸 shim 已验证映射/未验证拒绝；标准位置 shim 路径映射/未知位置拒绝（含 `C:\tools\mytool\npx.cmd` 反例）；cmd/powershell/pwsh/ps1/exe/bat/盘符全拒绝+裸名 posix 保留；args 资产前缀/wslpath 精确映射/未验证拒绝/内嵌拒绝；env NODE_PATH 列表转换/%USERPROFILE% 展开/未知变量拒绝/未声明键盘符阻止并点名键/普通值不受影响；HTTP localhost 原样；MapWindowsPath 规则表；settings 路径转换/剔除；CollectTransformNeeds 预扫描 | ✅ PASS |
| 4 | amagi 引用缺 provider/server 明确诊断 | `TestPiAmagiDependencyDiagnostics`：缺 kimi-coding（角色+压缩+并发三处对照）与缺 zread server 的中文诊断出现在结果 Detail，不阻断投影 | ✅ PASS |
| 5 | remote 与桌面同目标一致（双链路） | `TestWSLPiConfigSupplyDualLinkDesktopVsRemote`（同一协调器构造、桌面 `runWSLManagedModelsAtLaunch` vs `wslConfigSupplyEffect.Apply`，目标根全文件逐字节相等）+ `TestWSLPiConfigSupplyGateConsistencyBothLinks`（corrupt 两链路同阻断同文案；冻结两链路同静默）+ `TestRemotePiAPIProviderWSLPlanCarriesSupply`（供给 effect 先于 PTY、无 ConfigMutation、非 WSL 计划不变）+ `TestRemoteOpenCodeWSLPlanCarriesLayer` + `TestOpenCodeLayerEffectAnchorsPTYEnv`（层失败阻断/成功注入 OPENCODE_CONFIG+WSLENV/晚于 PTY 拒绝） | ✅ PASS |
| 6 | 预览 hash 过期重新检查（旧预览不覆盖新编辑） | `TestPiKeyedPreviewKeepLocalTakeOver`：预览令牌→停跟随零动作→目标外部变更后旧令牌接管=preview-expired 拒绝且新内容保留→重新预览接管成功→接管后来源变化恢复流动；另有协调器 applyProjection 既有 CAS（target-changed-since-planning）兜底 | ✅ PASS |
| 7 | build/vet/gofmt + 改动包测试 + 前端测试 + wailsjs 再生随批 | `go build ./...`=0、`go vet ./...`=0、gofmt 全部改动文件 `-l` 空（工作区 CRLF 噪音与既有文件不动，见 §6）；wslsupply 全量 ok、launchplan ok、launcher 仅 4 既有环境失败、piconfig 仅 2 既有环境失败、根包供给/登录/计划/remote/restart/绑定域全过；前端 vue-tsc=0、vitest 14/14（含 +4）；`wails generate module` RC=0 二次再生字节稳定（5 文件 vs HEAD 变更随批在工作区） | ✅ PASS |

## 5. 设计留白处的微观决策记录

1. **settings 白名单 S2 收录集={retry}**：pi settings.json 实测字段（packages/retry/lastChangelogVersion）中唯一确认跨平台的偏好字段；白名单机制存在且带显式排除清单，后续按「已确认」逐字段增补，不猜未知字段。
2. **amagi 键级粒度**：agents 按角色、mcp 按段（default/agents/<role>/leader 等列表键）扁平展开——整对象投影会让 WSL 独有角色（如本机 diting-quick/hongjun）把整段冻结。键名含 `/` 无法安全表示为路径→跳过+诊断。
3. **路径形态 shim 识别**：设计 §五「文件名相同不证明行为等价」落为位置判据（nodejs 目录 / %APPDATA%\npm），标准位置 npx.cmd→已验证原生 npx；其它位置同名拒绝。
4. **转换失败在 Plan 与 Candidate 两阶段的呈现**：Plan 期无事实（纯计算），按未转换键集聚合；Candidate 期转换拒绝→该键保持现状，整单元无可写键时按 `conversion-blocked` 受阻登记（非 local-modification 冻结——冻结语义专指用户手改），多键混合=partial。
5. **键级 Subs 与文件级 hash 并存**：逐键判定只读 Subs（Source=原始来源值 hash、Written=写入值 hash，规范化 JSON——目标侧格式化/键序编辑不算修改）；文件级 LastWrittenHash 保留为可观测字段。
6. **Detached（停跟随）持久化**：登记 Detached 而非删除条目——单纯删除会让「无记录+目标缺失」在下次推送重建文件，违背停跟随意图；接管清除标记。
7. **remote pi WSL 不保留 ConfigMutation**：与桌面同结果（桌面 WSL 启动不写 Windows 文件）；选中 provider 构建失败已在 BuildPlan 期 fail（与桌面 wslPiProviderBuildError 阻断等价）。
8. **OpenCode 层 effect 晚绑定 PTY**：Prepare 期全部 effect 构建后才互见（builder.ptyProcess 指针），层 Apply 校验 PTY 未启动（计划序+运行时双保险）；层内容走 SecretBuffer（含 apiKey），Dispose 生命周期随计划。
9. **wslDefaultDistroFn 接缝**：真实 distro 探测进程内负缓存（wsl.exe 一次瞬时失败=缓存空）在全量测试负载下污染后续单测——依赖默认 distro 的单测注入固定值（生产行为不变）。

## 6. 环境差异与既有失败（非本批引入；均有复现证据）

- **TestConfigMutationCAS*（6 测）/ R9_002 等 headroom 桩测试（R5/R6/R9/R10/R11 部分）**：独立 worktree 取 HEAD 复现——`SyncConfigDirectory` 的 `FlushFileBuffers` 对 GENERIC_READ 目录句柄在真实 Windows 恒 ACCESS DENIED（独立探针程序在仓库外目录同样失败，代码未改动）；headroom 桩 `exec:"sleep"` Windows 无此命令。CI 不可见（macOS 不编译 windows 路径 / windows 线仅 compile-check `-run '^$'`）。
- launcher 4 个（TestWritePi/OmpAgentConfig*TightPerms/Upgrades）、piconfig 2 个（Test{Compaction,ModelsConfig}RoundTrip 权限位）、platform 1 个（TestDarwinFNM*）、日志句柄锁 TempDir 清理类（TestAppConfigEventOnUserChange/TestSetRemoteEndpoint_*/TestOpenRemoteWebUI_*/TestAppListRemote*）——与 s0/s1 报告 §5/§6 同款。
- `-race` 不可用（CGO_ENABLED=1 需 gcc）；全量根包测试的失败集合逐轮浮动（负载相关），本批相关域（供给/登录/计划/remote/绑定）过滤运行全绿两轮以上。
- 工作区既有 CRLF 检出（autocrlf=true，提交归一化为 LF）：gofmt -l 对 CRLF 既有文件的标记是本地产物，CI（LF 检出）不受影响；本批改动文件已全部 gofmt 归一。

## 7. 未覆盖项 / 风险 / 回滚

**未覆盖**：① remote pi **OAuth 内置 provider** WSL 会话的凭据可达性（A2 语义只接了登录链路与 API-provider 托管面；OAuth API-provider 远程 WSL 是独立缺口，未在本批任务面内）；② 真机 WSL e2e 未做——S2 键级单元目标根固定 `.pi/agent`，真机会触碰用户真实 WSL 配置（违背隔离规约），语义已由假 UNC 探测面全链覆盖（UNC 提交原语 0600/chmod 已有 S1 真机证据）；③ mcp-auth.json（Windows 侧另有该文件）未投影——含凭据且消费入口未验证，留后续批；④ 转换器复用面（Claude mcpServers/Codex mcp_servers）只建了内核，S4 接入；⑤ harness 结算将跑 mobile npm test——本批未触碰 mobile/。
**风险**：① settings 白名单极小（retry）——机制完备、收录集保守，面板实际感知面小；② mcp.json env 明文 key 落 distro（用户自己的文件内容跨界，Sensitive fail-closed 已兜底权限面）；③ Preview/接管动作可唤醒 distro（「检查/显式检查允许唤醒」符合设计 §三，UI 有明示）。
**回滚**：还原 S2 文件即可（33 文件清单见 §1）；已分发内容按 lastWrittenHash/Subs 匹配可条件撤销（协调器 Compensate 原语），用户手改键不受影响；不删 auth（A2 域未动）。

## 8. 验证命令记录（cwd=仓库根，WSL bash → go.exe/wails.exe，GOCACHE 经 WSLENV=/p 转发）

```
go build ./...                                                       RC=0
go vet ./...                                                         RC=0
gofmt -l <全部 S2 改动文件>                                           空
go test ./internal/wslsupply/ -count=1                               ok（S0+S1+S2 全量）
go test ./internal/launchplan/ -count=1                              ok
go test ./internal/launcher/ -count=1                                仅 4 既有环境失败
go test ./internal/piconfig/ -count=1                                仅 2 既有环境失败（权限位）
go test ./ -count=1 -run 'TestWSL|TestPiLogin|TestPiProvider|TestRemote|TestOpenCodeLayer|TestSaveAmagiConfig|TestBuildPlan|TestLaunchPlanner|TestLaunchExecutor|TestM004|TestAuthority|TestBind|TestRestart|TestGetWSLConfig|TestGrant|TestPushWSL|TestPrepareWSLOpenCode'   ok
（对照实证）独立 worktree@HEAD + 未提交 S1 件：TestConfigMutationCASExisted 仍失败；仓库外独立探针复现 SyncConfigDirectory ACCESS DENIED
wails generate module                                                RC=0（二次再生字节稳定）
cd frontend && node node_modules/vue-tsc/bin/vue-tsc.js --noEmit     RC=0
cd frontend && node node_modules/vitest/vitest.mjs run src/__tests__/api   14/14 PASS
```

## 9. 遗留现场披露

- `X:\WorkSpace\.amagi-trash.G4XJ3O\`：HEAD 基线对照用的一次性 git worktree（纯 HEAD 检出副本，无独有内容）已移出为待清理目录；删除被递归删除守卫拦截（按规约不绕过），请随批清理。
- S1 四个未提交件仍处未提交状态（§0），本批改动叠加其上；提交时需一并纳入。

## 10. S2 diting full 审 FIX 修复轮（s2-review-diting.md，VERDICT: FIX → 5/5 修复）

> 裁定语义记录（F-3，Leader 已裁定）：**三键级单元（pi/settings、pi/amagi、pi/mcp）首启前需 per-target 明确同意**——设计 §三「用户配置、可执行资产需导入同意」+ §四 Pi 表「（同意后）」在本批生效：未同意时启动/推送/登录路径**只读不写**（状态 awaiting-consent + 状态页「启用投影」入口，与视觉清单同一 GrantUnitConsent 机制与 UI 模式）；同意后进入 TrackedProjection 流动，§八 S2 验收「面板修改进入未手改 WSL 配置」在同意后成立。distro 重装（generation 变化）后旧授权自动失效，需重新同意。

| # | Finding | 修复 | 测试证据 |
|---|---|---|---|
| **F-1** Major：recordProjectionFreeze 重建 StateEntry 丢 Subs——任一键冻结后键级登记清空，健康键永久沦为 unmanaged-existing | `recordProjectionFreeze` 补 `Subs: prev.Subs` 与 `Detached: prev.Detached`；同类扫荡：`recordProjectionPendingConsent` 同样补 `Subs/Detached/Consented`（登记重建一律不丢字段）；`DropUnitRegistration` 补 `Consented`/`TransformVersion` | **TestFrozenKeyKeepsRegistrationAcrossCycles**（跨冻结周期）：手改 agents/baize→冻结登记后，健康键 diting 来源变化**仍更新**（b→b2 落盘）、下一轮 compaction 来源删除**仍按托管未手改移除**、手改键 kimi/hand 两轮均保留、冻结原因不漂移为 unmanaged-existing | 
| **F-2** Major：ContainsWindowsDriveRef 命中一切 `scheme://`——stdio MCP 的 env 值 URL（API endpoint 常态）被整条误拒 | `ContainsWindowsDriveRef` 词首锚定：候选盘符字母的前一字符属 scheme 续集（字母/数字/+/-/.）则跳过（等价剥除 `^[a-zA-Z][a-zA-Z0-9+.-]*://`），真盘符（前一字符为空白/起始/=/引号）仍命中 | **TestTransformURLsAreNotDrivePaths**：`https://api.x.com/v1`、`postgres://…` env 值与 args 内嵌 `--endpoint=http://x` 全放行；`prefix C:\tools suffix` 仍拒并点名键 |
| **F-3** Major：三键级单元无同意门首启自动写入 Windows 用户配置（Leader 裁定：加 per-target 同意门） | `keyedProjectionAdapter.requiresConsent=true`（三构造器）→ 协调器 Plan 既有同意门生效（awaiting-consent 只读拦截 + recordProjectionPendingConsent 状态面）；新增 App 面 `EnableWSLPiConfigSupply`（三单元 GrantUnitConsent + 异步推送）+ 状态页「待启用」态与「启用投影」入口（与视觉清单同 UI 模式）；`TakeOverUnit` 补同意门（显式写入不得绕过）；登录 verdict 对 awaiting-consent 静默放行（预期态不刷告警）；**发现并修复同意门的代际缺口**：`StateFile.entryFor(unit, generation)`——代际不匹配的登记视为无登记（否则旧代 Consented/Subs 在 distro 重装后仍放行，违背「旧授权不自动扩散」），Plan/applyProjection/PreviewUnits/TakeOverUnit 四处消费点统一改用 | **TestPiConfigUnitsRequireConsent** 三子测：未同意零写入（两文件不创建+接管拒绝）／同意后首次发布 Applied／跨代不继承（machine-id 变化→awaiting-consent→删目标+重新同意→Applied，无 gen1 hash 基线误判 local-deletion-frozen）；**TestEnableWSLPiConfigSupply**（无能力 runner 拒绝/三单元授权落地）；前端 awaiting-consent 态透出（wslSupply.test.ts +2） |
| **F-4** Minor：keyed.go:133 注释与行为不符（「自然清除」实际保留） | 按裁定**保留行为**（§三「有记录+目标被删→冻结」：用户删除的键若从来源回归应判 local-deletion-frozen，登记存续是正确语义），注释改为如实描述「登记刻意保留（源回归即冻结）」并指出依赖 buildCandidate 复制 Subs 的既有行为 | 行为由 TestPiKeyedConflictTable 既有子态覆盖（source-deleted 路径）；注释修正（无行为变化） |
| **F-5** Minor：未声明 env 键的 `%USERPROFILE%\x` 变量形态漏检放行 | `containsWindowsVarPathRef`：`%VAR%` 后跟路径分隔符（var 首字符为词字符）→ 按未知变量处理，点名键名拒绝该条目（§五「无法确定则要求用户提供 Linux 值」）；`${VAR}` 占位符与裸 `%VAR%` 不受影响 | **TestTransformUndeclaredVarPathEnvRejected**：`%USERPROFILE%\.tool` 拒绝点名 TOOL_HOME；`${HOME}/.tool` 与 `%COMPLETE%` 放行 |

**修复轮验证**（cwd=仓库根，同 §8 环境约定）：`go build ./...` RC=0；`go vet ./...` RC=0；gofmt 全部改动文件 `-l` 空；`go test ./internal/wslsupply/ -count=1` 全绿（含 5 个新修复测试）；根包供给/登录/计划/remote/绑定域全过；launcher 4 个既有环境失败不变；前端 vue-tsc RC=0、vitest 16/16（含新增 2）；`wails generate module` RC=0（EnableWSLPiConfigSupply 绑定随批再生）。

**修复轮勘误**（§6/§8 口径修正）：① piconfig 既有环境失败实为 **4 个**（Test{Amagi,Auth,Compaction,ModelsConfig}RoundTrip，全部 `want 0600 got -rw-rw-rw-` Windows chmod 类）——主轮报告的「2 个」是 `tail -8` 截断造成的漏计，非本批回归（writePrivateFile 未改动）；② 主轮根包过滤串未含 TestConfigMutationCAS*，其 Windows-only 失败（SyncConfigDirectory FlushFileBuffers 句柄权限）已由独立 worktree+仓库外探针实证为 HEAD 既有（§6）。

**改动文件（修复轮）**：`internal/wslsupply/{coordinator,state,keyed,transform,pi_config}.go`（F-1/F-2/F-3/F-4/F-5 + 同意门代际缺口）、`pi_login_launch.go`（awaiting-consent 静默）、`app_wsl_supply.go`（EnableWSLPiConfigSupply + 待启用状态面）、测试 `internal/wslsupply/{pi_config_test,transform_test}.go`（+5 测）、`wsl_s2_remote_test.go`（双链路同意前置 + TestEnableWSLPiConfigSupply）、前端 `api/wslSupply.ts`/`WSLCLISettings.vue`/`__tests__/api/wslSupply.test.ts`（启用入口 +2 测）、`frontend/wailsjs/*` 再生。
