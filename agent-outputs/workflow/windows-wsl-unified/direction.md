# P2-D 方向：Windows 统一配置自动供给 + 安装/升级双侧联动

> 角色：fuxi 设计态（只写本 artifact，不改代码）。基线 HEAD `0723bdb`，macOS 只读取证。
> 上游：`research.md`（P1）、`workflow.md`；源码定向读取见各节锚点。P2-A（worktree `windows-wsl-setup`，派发时尚无 diff）提供 `wslsetup.Service.UpdateTool(tool)` + omp 映射——本方向只消费该 API，不另选底层。
> 已遵循 steer #1：一次明确启用统一管理 → 此后自动 reconcile；暂不覆盖的类型单列（§1.6）；双侧安装状态放进 InstallResult 新增结构。

## 0. 结论（供 P2-B 一次实现）

1. **配置（R1）**：新增「统一管理」per-target 授权：用户在 WSL 卡片上点一次「启用统一管理」，即对现有安全选择集一次性授予 consent；此后会话启动、保存推送、CodeBox 启动时都会自动 reconcile 已托管项。Claude/Codex 不再需要检查/预览/勾选。底层只加两样最小原语：①选择集通配键 `"*"`（跟随全部可导入键）；②`TargetRecord.Unified` 标记 + 「已统一则自动授权尚未登记的单元」原语。冻结、unmanaged-existing、detached 以及 LocalOnly 白名单的判定逻辑一行不改。
2. **安装（R2/R3）**：envcheck 注入 `wslCLISync` 回调（照搬 `SetWSLSearchToolsInstaller` 的模式）。四个 Windows 执行汇聚点（runOperation、serializedInstallOrUpdate、serializedInstallOrUpdateWithMethod，fix 的 install_tool 已经走 runOperation）在 **Windows 成功后**同一个操作内追加 `wsl_sync` 步骤。结果写入 `InstallResult.WSL *WSLSyncResult`。`Success` 继续只表示 Windows 侧；WSL 失败时前端横幅显示 warning，不显示「成功」。不新增轮询，也不新增 OperationStatus。

## 1. 配置统一供给（R1/R4）

### 1.1 现状事实（为什么手动 S4 不算完成）
- Claude 的 `claude/mcp`、`claude/settings` 与 Codex 的 `codex/config` 都是 `requiresConsent + selectLeaf`。选择集为空时一律不导入（`pi_config.go:228-245` applySelection、`codex_adapter.go:189-206`）。现在唯一的入口是 `ImportWSLCliConfigKeys`（`app_wsl_supply_s4.go:423`）：用户得逐键勾选，然后 SetUnitSelection → GrantUnitConsent → RunUnits。
- 启动阶段只自动跑 pi 单元（`app_wsl_supply.go:182` 仅 API-provider 会话）和 shared 小文件（`runWSLSharedFilesForSpec`，`control_wiring.go:147`、`launch_executor.go:970`，五个 CLI 都经过）。保存推送 `doPushWSLSupply`（`app_wsl_supply.go:420`）只推 pi 和 shared。**Claude/Codex 单元没有任何自动运行入口。**
- Pi 投影（settings/amagi/mcp）要靠 `EnableWSLPiConfigSupply`，视觉清单要靠 `GrantWSLVisionConsent`，skills 和 Claude/Codex 资源要逐项 `Apply*`，都是逐处单独授权。
- 保护机制已经齐全，应原样复用：`resolveKeyed`（`keyed.go:78-140`，unmanaged-existing 保留、手改冻结、双侧冲突并列、来源删除只移除未改动的托管项）；资源/技能的 `planTreeUnitCore`（unmanaged-existing 阻断）；consent 按 generation 记录，unknown 身份拒绝（`coordinator.go:1632-1690` SA-02）。
- **陷阱（实现必须避开）**：`GrantUnitConsent` 重建 entry 时**不保留 `Detached`**（`coordinator.go:1664-1680`）。如果对用户已经「保留本地」的单元再授权，就会静默解除停跟随。所以自动授权必须跳过已有登记（Consented 或 Detached）的单元。

### 1.2 共享契约（wslsupply 最小增量）
| 符号 | 位置 | 语义 |
|---|---|---|
| `const SelectAllKeys = "*"` | `types.go` | 选择集通配：跟随该单元全部可导入键 |
| `selectionMatchesKey`：开头加 `if selected[SelectAllKeys] { return true }` | `keyed.go:252` | 只改这一处，pi_config 的 applySelection、codex desiredFrom、两处 preview 的 Selected 都自动生效；导入候选仍要经过 resolveKeyed 的冻结和 unmanaged 判定 |
| `TargetRecord.Unified bool` + `UnifiedAt time.Time`（omitempty） | `state.go` TargetRecord | per-generation 统一管理授权；distro 重装或换用户后，新代为 false，需要重新点一次 |
| `(*Coordinator).SetTargetUnified(ctx, distro string, on bool) error` | `coordinator.go` | ResolveTarget；`GenerationUnknown` 时按 SA-02 原文拒绝；在 state.mutate 里写 Targets[gen] |
| `(*Coordinator).UnifiedCached(distro string) bool` | 同上 | 只读状态（`latestTargetRecord`），给状态页用，不做探测 |
| `(*Coordinator).AutoConsentIfUnified(ctx, distro string, cli CLI, units []UnitID) ([]UnitID, error)` | 同上 | ResolveTarget 后**在同一个 mutate 里**校验 `Targets[gen].Unified`。对每个单元：同代 entry 已 Consented 或 Detached 就跳过，否则按 GrantUnitConsent 的同代保留规则授权；返回实际授予的单元。可接受性校验（skills/resources/RequiresConsent）与 GrantUnitConsent 共用，抽成 `grantEntryIn` 小 helper 来消除重复 |

不变量：I1 consent 绑定可验证 generation；I2 detached 永不被自动授权覆盖；I3 冻结/冲突/unmanaged-existing 的判定不改；I4 LocalOnly 白名单不改（claude permissions/hooks/env/projects/trust，codex trust/sandbox/approval/auth.json）。

### 1.3 App 编排（新文件 `app_wsl_unified.go`，根 package）
- **`EnableWSLUnifiedSupply() []WSLConfigUnitOutcome`**（绑定，用户的一次明确授权）：
  1. `distro := a.wslDefaultDistro()`，为空就 blocked；`SetTargetUnified(true)`；
  2. 固定单元集 `wslUnifiedFixedUnits`：pi→{settings, amagi, mcp}；shared→{vision-export}；claude→{mcp, settings}；codex→{config}。逐个 CLI 调 `AutoConsentIfUnified`；
  3. 对 selectLeaf 单元（claude/mcp、claude/settings、codex/config）中**非 Detached** 的，调 `SetUnitSelection(distro, cli, unit, []string{SelectAllKeys})`；
  4. `StartSkillsScan` 和 `StartCliResourcesScan(claude/codex)` 启动后台扫描，再 `scheduleWSLSupplyPush()`；
  5. 返回逐单元结果（granted / skipped-detached / already / failed），不拿成功掩盖部分失败。
- **`DisableWSLUnifiedSupply() error`**（绑定）：`SetTargetUnified(false)`，停止自动纳入新项。已授权单元和已有副本保持不动（撤回走现有的单元「保留本地」），这一点在 UI 上写明。
- **`reconcileWSLUnifiedResources(ctx, distro)`**（内部）：只在 `UnifiedCached` 为真且扫描快照已完成时执行。取 skills 快照和 claude/codex 资源快照里 `NotPortableReason()==""` 的项，调 `AutoConsentIfUnified`，再对「已授权且非 detached」的单元跑一次 RunUnits。复用 `ApplyWSLSkillsSupply`/`ApplyWSLCliResourcesSupply` 的循环体，抽一个 helper，两处共用。
- **`doPushWSLSupply` 增量**（`app_wsl_supply.go:420`，守卫不变：只推 Running 的 distro、有活动会话时暂停）：在 pi 和 shared 之后追加 claude/codex 的「已授权单元」RunUnits（ReasonSave），以及 `reconcileWSLUnifiedResources`。
- **CodeBox 启动对账**：在 `Startup` 里 WSLSupply 就绪后调一次 `scheduleWSLSupplyPush()`。没有统一授权时只跑 pi/shared，行为与现在相同。
- **会话启动 lazy（Claude/Codex）**：在 `runWSLSharedFilesForSpec`（`app_wsl_supply.go:215`）里，shared 闸门返回 nil 之后，追加 `runWSLCliConfigAtLaunch(ctx, runner, distro, spec.AppType)`：claudecode→claude 单元，codex→codex 单元。先用 `runner.GetStatus` 筛出「同代已 Consented 且非 Detached」的单元，**没有授权就不调 RunUnits**，以免每次启动都写一条 awaiting-consent 状态。结果**只告警不阻断**（判定沿用 `verdictForWSLPiConfigSupply` 的预期态白名单），通过 `appLogHook` 记日志。两个调用点（desktop/remote/restart）自动覆盖，不改 `control_wiring.go`/`launch_executor.go`。
- **状态**：`WSLConfigSupplyStatus` 新增 `Unified bool \`json:"unified"\``，由 `GetWSLConfigSupplyStatus` 通过 `UnifiedCached` 填充。

### 1.4 前端（`WSLCLISettings.vue` + `api/wslSupply.ts`）
- 「配置状态」区顶部加一行统一管理：`!supply.unified` 时显示授权说明和「启用统一管理」按钮。说明需列明范围：Pi settings/amagi/mcp、Claude MCP 服务器与白名单设置、Codex MCP/profile、共享技能及 CLAUDE.md/commands/agents/AGENTS.md、视觉清单；**其中 MCP env 与视觉清单含密钥副本，按 0600 写入**；WSL 手改和已有同名项不会被覆盖。`unified` 时显示「已由 Windows 统一管理（{distro}）」和「停止自动纳入新项」。
- 现有逐单元面板（预览/应用/保留本地/接管/技能勾选/S4 导入）保留，但改为折叠的「冲突处理/高级」区。正常路径不再要求打开。旧的「启用投影」「同意共享到 WSL」按钮在 `unified` 时隐藏，避免出现两个入口。

### 1.5 首次用户路径（验收口径）
用户只需点一次「启用统一管理」，之后无需再手填或预览：
1. 首个 WSL 会话启动时，pi 单元由既有 lazy 写入；claude/codex 单元由新加的 lazy 写入；shared 单元由既有闸门写入。
2. 首次后台对账（启用后推送 / CodeBox 启动 / 任意保存）时，扫描完成的资源被纳入。
3. Windows 侧以后新增 MCP server 或白名单字段，在下一次启动或推送时被自动导入（这就是通配选择的作用）；新增的技能或资源在下一次后台对账时纳入。

### 1.6 暂不自动覆盖（明确不宣称全量）
| 类型 | 处理 | 理由 |
|---|---|---|
| WSL 手改键 / 双侧冲突 / 已有同名非托管项 / 用户「保留本地」的单元 | 保持 WSL 现状，状态页给出接管入口 | R4 保护：只能由用户显式接管 |
| Claude settings 白名单外字段（permissions/hooks/env）、`.claude.json` projects/trust/onboarding；Codex trust/sandbox/approval、未知节 | 永不跨界 | 授权执行或机器状态 |
| Claude OAuth 凭据、Codex `auth.json`（订阅登录） | 不复制；API-key provider 会话经 WSLENV 注入，不受影响 | 只有 pi 有 A2 凭据通道；新增凭据跨界属于独立安全设计 |
| OpenCode 用户 `opencode.json`（provider 附加层外的 MCP/agents）、omp 的 models.yml 以外配置 | 本轮不覆盖 | 没有对应的投影适配器；新建属于新面，超出最小范围 |
| 插件 | 维持现状（只有 pi 重放） | 任务书排除全引擎迁移 |
| 新技能/资源的即时性 | 下一次后台对账才纳入，会话启动时不扫描 | SA-17：扫描不进启动链 |
| distro 重装或换默认用户 | 需要重新点一次「启用统一管理」 | per-generation 授权（SA-02） |

## 2. 安装/升级双侧联动（R2/R3）

### 2.1 共享契约（`internal/envcheck/types.go`）
```go
type WSLSyncResult struct {
    Status    string `json:"status"`            // "succeeded" | "failed" | "skipped"
    Distro    string `json:"distro,omitempty"`
    Version   string `json:"version,omitempty"`
    AlreadyOK bool   `json:"alreadyOK,omitempty"`
    Message   string `json:"message,omitempty"`
    Error     string `json:"error,omitempty"`
}
// InstallResult 追加：WSL *WSLSyncResult `json:"wsl,omitempty"`（nil = 本平台/本次不涉及 WSL）
const OperationStepWSLSync OperationStep = "wsl_sync"
```
`InstallResult.Success` 和 `OperationState.Status` 的语义不变，仍只表示 Windows 侧，以兼容 CheckOne 刷新、remote 等全部既有消费者。双侧结论由消费者读取 `result.wsl` 得出。

### 2.2 envcheck 接缝
- `Service.SetWSLCLISync(fn func(tool CLITool, kind OperationKind) *WSLSyncResult)`，与 `SetWSLSearchToolsInstaller` 同构（`service.go:182`，加 mu，nil 安全）。
- 内部 `s.attachWSLSync(tool, kind, result, reporter)`：仅当 `result.Success`、kind 为 install 或 update、tool ≠ headroom、fn 不为 nil 时调用；有 reporter 就先报 `OperationStepWSLSync`（progress 约 92，消息「正在同步到 WSL…」），再执行 `result.WSL = fn(...)`。
- 调用点：
  - `runOperation`（`service.go:995`）：放在 CheckOne 刷新之后、取 opMu 之前。覆盖 StartInstallTool、StartUpdateTool、StartInstallClaudeCodeWithMethod（Claude 指定通道），以及 fix 的 `install_tool`（`fix_dispatcher.go:628` 转调 StartInstallTool）。操作保持 running 直到 WSL 步骤结束，**用的是同一个操作槽和同一套轮询**。
  - `serializedInstallOrUpdate`（`service.go:610`）与 `serializedInstallOrUpdateWithMethod`（`service.go:1258`）：Windows 成功后调用（不传 reporter）。覆盖同步的 InstallTool、UpdateTool、InstallClaudeWithMethod，以及 fix 的 `install_claude_method`。
  - `fix_dispatcher.go:117` 的 install_claude_method 分支：`result.WSL.Status=="failed"` 时把 WSL 错误写入 `FixActionResult.Error`，并追加到 Message。
- Windows 失败时不触发 WSL（WSL 字段为 nil）：联动语义是「Windows 装好后同步 Linux 版」，失败重试仍走同一入口。

### 2.3 App 回调（`app_wsl_cli_sync.go` + app.go 装配处一行，`app.go:651` 旁）
`func (a *App) syncCLIToWSL(tool envcheck.CLITool, kind envcheck.OperationKind) *envcheck.WSLSyncResult`：
1. `distro := wslDefaultDistroFn(nil)`，为空返回 nil（非 Windows 或无 WSL 时不出 WSL 行，不需要 GOOS 分支）；
2. `st := a.WSLSetup.GetStatus()`，从中找出该工具（key 经 `normalizeToolKey`：claude_code→claude，omp 由 P2-A 映射）的 installed 状态；
3. 门控：该 CLI 的 shell 设置为 `wsl`（`Dashboard.ClaudeShell/OpenCodeShell/CodexShell/PiShell/OmpShell`，见 `settings/service.go:200-210`）**或者** WSL 内已经安装，满足其一才执行；否则返回 `skipped`（「该 CLI 未使用 WSL 模式且 WSL 内未安装」）。这样不会给不用 WSL 的用户塞进 Node 和 CLI；
4. install 且 WSL 未装 → `a.WSLSetup.InstallTool`；install 且已装、或 update → `a.WSLSetup.UpdateTool`（P2-A）。把 wslsetup.InstallResult 映射为 WSLSyncResult，`Log` 只保留在日志里，不进结果；
5. 成功后 `invalidate` 混合预警缓存（可选，30s TTL 本来也会自愈）。

另外加绑定 **`UpdateCLIInWSL(tool) (*wslsetup.InstallResult, error)`**，供 WSL 卡片使用：已安装时按钮由名不副实的「重新安装」改为「升级」并调用它（research #4）。

### 2.4 前端
- `envcheckSolutions.ts::operationCompletionBanner`：在 succeeded 分支里读 `op.result?.wsl`。`failed` → `type:'warning'`，标题为「{name} {kind}：Windows 成功，WSL（{distro}）失败」，描述放 wsl.error；`succeeded` → 描述追加「WSL 已同步 v{ver}」；`skipped` → 描述追加原因；nil → 与现在相同。tracker（`envcheckOperationTracking.ts`）不需要改。
- `wsl_sync` 步骤的文案在既有 step 标签映射里补一项（如果有映射表）；同步路径的调用方读 `result.wsl`，按同一规则显示。
- 安装成功后刷新 WSL 卡片：`WSLCLISettings` 已有 `GetWSLCLIStatus`，在 EnvCheck 终态横幅出现时触发一次刷新（同一页面，事件或父组件 ref 均可）。

## 3. 绑定生成
新增或变更的 App 方法和结构：`EnableWSLUnifiedSupply`、`DisableWSLUnifiedSupply`、`UpdateCLIInWSL`、`InstallResult.WSL`、`WSLConfigSupplyStatus.Unified`。**wailsjs 只能由生成器产出，不能手改。**当前 PATH 里没有 wails CLI，按 steer 使用 vendor 的 `go run -tags bindings .`。执行前先核对：`main_bindings_mode.go` 跳过单实例；`NewApp` 函数体本身没有 MkdirAll、WriteFile 或 Save（本次 awk 已查），**但各 `New*Service` 构造器是否写 `~/.amagi-codebox` 没有核实**。建议以 `HOME=$(mktemp -d)`（Windows 用 `USERPROFILE`）隔离运行后，再 diff `frontend/wailsjs/`。如果确认生成器在该模式下不能落盘，按 blocked 报告，不要手写。

## 4. 执行切片（一次实现，可按文件拆两人并行）
| 切片 | 写面 | 依赖 | 测试（入口级） |
|---|---|---|---|
| S-A wslsupply 原语 | `types.go`、`keyed.go`（selectionMatchesKey）、`state.go`（TargetRecord）、`coordinator.go`（SetTargetUnified/UnifiedCached/AutoConsentIfUnified + grantEntryIn） | 无 | 新增 `internal/wslsupply/unified_test.go`：通配跟随新增键；通配下来源删除只移除未改动的托管项；手改键仍冻结；unmanaged-existing 保留；Detached 不被 AutoConsent 覆盖；unknown generation 拒绝；换代后 Unified 失效；非 Unified 时 AutoConsent 为 no-op |
| S-B App 统一编排 + 前端配置面 | 新建 `app_wsl_unified.go`；`app_wsl_supply.go`（doPush 增量、runWSLSharedFilesForSpec 尾部 lazy、Status.Unified）；Startup 一行；`api/wslSupply.ts`；`WSLCLISettings.vue` | S-A | 根 `app_wsl_unified_test.go`（假 Runner 范式，参照 `app_wsl_supply_core_test.go`）：Enable 一次后 claude/codex 单元的 Consented 与 Selected=`*`；跳过 detached；会话启动 claudecode spec 只跑已授权单元且失败不阻断；未授权时不调用 RunUnits；push 带上 claude/codex 和资源；Disable 后新资源不再被授权。前端 `__tests__` 覆盖统一块显隐 |
| S-C 安装联动 | `envcheck/types.go`、`service.go`（SetWSLCLISync/attachWSLSync，三处调用）、`fix_dispatcher.go:117`；新建 `app_wsl_cli_sync.go`；`app.go` 装配一行；`App.UpdateCLIInWSL`；`envcheckSolutions.ts`；`WSLCLISettings.vue` 按钮 | P2-A UpdateTool | envcheck：注入假回调，断言 async 成功路径 Result.WSL 已填且 step 经过 wsl_sync；Windows 失败时不调用；headroom/uninstall 不调用；同步 Install/Update/WithMethod 三个入口都带 WSL；fix install_claude_method 在 WSL 失败时 Error 非空。根：门控（shell 非 wsl 且未装→skipped；已装→Update；未装且 shell=wsl→Install；无 distro→nil；omp key 映射）。前端：banner 在 wsl.failed 时为 warning，不出现「成功」 |
| 收尾 | 绑定再生成（§3） | S-B、S-C | `go vet ./...`；`go test ./internal/wslsupply ./internal/envcheck ./internal/wslsetup .`；`GOOS=windows go build ./...`；`npm --prefix frontend run build` 与 vitest |

S-B 和 S-C 都会碰 `WSLCLISettings.vue`（S-B 改配置区，S-C 只改 CLI 行按钮），段落不重叠。`app.go` 只在装配段加一行，并在 Startup 加一行。

## 5. 失败语义与可逆性
- 统一启用部分失败时逐单元返回，不汇总成「成功」。会话启动的新 lazy 只告警；shared 视觉闸门的 fail-closed 语义不变。
- 安装：Windows 失败 → 现状语义且 WSL 字段为 nil；Windows 成功而 WSL 失败 → 操作 succeeded，`wsl.status=failed`，前端显示 warning；WSL 慢时操作保持 running（现有轮询能看到 wsl_sync 步骤），期间其他安装返回 ErrBusy，与现有串行语义一致。
- 回滚：所有改动都是增量。运行时可以用 Disable 停止新增纳入，再按单元保留本地。代码回滚即 revert 上述文件。**降级风险**：旧版本二进制把 `"*"` 当普通叶名，选择集实际变空，已托管且未手改的键会走来源删除路径被移除（手改键和 unmanaged 键不受影响），需要写进发布说明。`TargetRecord.Unified` 在旧版本中会被忽略，无害。

## 6. 未知项
| 项 | 影响 | 判别 |
|---|---|---|
| P2-A `UpdateTool` 的实际签名与超时 | S-C 映射代码 | 合入后读 `service_windows.go`；本设计只假定 `(*InstallResult, error)` |
| bindings 模式下 `New*Service` 是否写用户目录 | 绑定再生成是否安全 | 按 §3 隔离 HOME 运行，或者先审构造器 |
| remote/mobile 是否消费 `InstallResult.Success` 来表达「全成功」 | 远程侧可能漏显示 WSL 失败 | `grep -rn "InstallResult\|OperationState" internal/remote mobile/src`；如有消费者，同样按 `wsl` 字段补齐 |
| 真机：Claude 启动 lazy 增加的延迟、9P 下 `.claude.json` 体积 | 启动耗时 | Windows 真机 P3 计时；本地只做单元测试，标 U |
| 「shell=wsl 或已装」门控是否符合主上预期 | 产品语义 | 当前取保守默认，可逆（改一个判断） |
