# P2-B 配置统一自动供给 — 实现报告

> 角色：luban（P2-B）。worktree：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/windows-wsl-config`（分支 `amagi/windows-wsl-config`，未提交，由 Leader 汇总）。基线 = 主仓 HEAD（P1/P2-D 已接受）+ 本 worktree 干净起点。宿主 macOS：全部验证为真实 App/协调器入口级测试（假探测面 + 可注入 Home），**无 Windows/WSL 真机验证**（真机归 P3，见「真实未覆盖」）。

## 1. 设计落点（Leader 裁定覆盖 direction.md §1.2）

**不引入通配选择键**（裁定原文：不要持久化仅有 "*" 的选择集导致旧版本降级删除托管配置）：

- 每次配置对账把**来源侧显式可导入键物化进选择集**（`ExtendUnitSelectionIfUnified`，只增不减，`SelectAllMarker="*"` 仅作防御保留字、永不持久化）；`selectionMatchesKey`/`SetUnitSelection`/S4 导入协议零改动——旧版本二进制消费的选择集与用户手工勾选完全同形，**降级不删托管配置，无需降级回归证明**（结构性保证，且测试双重断言状态清单无 `"*"`）。
- 同代 `TargetRecord.Unified` 标记使**未来新增键自动纳入**：启用/会话启动 lazy/保存推送/启动对账四个时机都重新物化，Windows 新增条目在下一次对账进入（测试覆盖「新增键 → 推送 → 自动导入」）。

## 2. 产物清单（规格 → 位置）

| 规格 | 产物 | 位置 |
|---|---|---|
| wslsupply 授权原语（S-A） | `SetTargetUnified`/`UnifiedCached`/`AutoConsentIfUnified`/`ExtendUnitSelectionIfUnified`/`UnitSourceKeys`/`SetScanCompletionCallback` + `grantEntryIn`/`consentPolicyFor` | `internal/wslsupply/unified.go`（新） |
| Unified 标记持久面 | `TargetRecord.Unified/UnifiedAt` | `internal/wslsupply/state.go` |
| GrantUnitConsent 去重 + 跨目标隔离修正 | prev 查找改 `entryFor(目标代)`（A→B→A 不再把 A 的基线误挂丢） | `internal/wslsupply/coordinator.go` |
| 来源键全集（不探测目标） | `keyedProjectionAdapter.importableSourceKeys` / `codexConfigAdapter.importableSourceKeys` | `pi_config.go` / `codex_adapter.go` |
| 扫描完成回调 | `SkillsScope`/`ResourceScope` `onDone` + 协调器统一注册 | `skills.go` / `resources.go` |
| App 统一编排（S-B） | `EnableWSLUnifiedSupply`/`DisableWSLUnifiedSupply`（绑定）、启动 lazy、推送增量、资源对账、启动对账、扫描回调 | `app_wsl_unified.go`（新） |
| 启动 lazy 挂点 | `runWSLSharedFilesForSpec` 尾部 `runWSLCliConfigAtLaunch`（claudecode/codex；无授权不调 RunUnits、只告警不阻断；pi 单元仍走既有独立闸门） | `app_wsl_supply.go` |
| 保存推送增量 | `doPushWSLSupply` 尾部 `pushWSLUnifiedCliConfig`（含选择集物化）+ `reconcileWSLUnifiedResources` | `app_wsl_supply.go` |
| Startup 一行 | `a.scheduleWSLUnifiedStartupReconcile()`（统一开启时点火扫描→完成回调再推；未开启=现状 pi/shared 行为） | `app.go`（仅此一处，+3 行注释） |
| 状态面 | `WSLConfigSupplyStatus.Unified` | `app_wsl_supply.go` |
| 扫描完成→对账接线 | `newWSLSupplyCoordinator` 尾部 `SetScanCompletionCallback(app.wslUnifiedOnScanDone)` | `app_wsl_supply.go` |
| 前端 API | `enableWSLUnifiedSupply`/`disableWSLUnifiedSupply` | `frontend/src/api/wslSupply.ts` |
| 前端 UI | 「配置状态」区顶部统一管理行（启用/停用+范围说明）；unified 时隐藏旧的「同意共享视觉清单」「启用投影」入口避免双入口 | `frontend/src/views/settings/WSLCLISettings.vue` |
| steer#1 工具行 | 已安装按钮「重新安装」→「升级」，按 installed 分派 `updateCLIInWSL`（wrapper 从主树复制，编译依赖、非本任务产出）；toast/失败文案区分；补 omp 显示名；Node 提示 20→22 | `WSLCLISettings.vue` + `frontend/src/api/wslsetup.ts` |
| 用户文档 | 「统一配置管理」小节（范围/时机/保护边界/停用语义/重装需重新启用） | `docs/user/installation.md` |

## 3. 保护边界（全部保持原语义，测试覆盖）

per-generation 授权（换代 Unified 失效+旧代不写新身份）、unknown 身份拒绝（SA-02）、detached 不被自动授权覆盖（`AutoConsentIfUnified` 跳过已登记 Consented/Detached 单元，规避 `GrantUnitConsent` 重建丢 Detached 的陷阱）、同代 Subs/SelectedKeys 保留、手改冻结/unmanaged-existing/冲突判定零改动、LocalOnly（projects/trust/permissions）永不跨界（入口级断言）、视觉清单 shared fail-closed 闸门不动（本文件不重复实现）。资源新项只在后台对账纳入，不进会话启动扫描（SA-17）。

## 4. 测试证据（真实入口，非仅私有 helper）

Go（macOS 本机，`go test`，全部通过）：

| 验收点 | 测试 | 结果 |
|---|---|---|
| 原语：生命周期/unknown 拒绝/换代失效 | `internal/wslsupply/unified_test.go` TestSetTargetUnifiedLifecycle | ✅ |
| 原语：未启用 no-op、跳过 detached/已授权、不可接受单元拒绝 | TestAutoConsentIfUnifiedNoOpWhenNotUnified / GrantsAndSkips | ✅ |
| 原语：同代基准保留（Subs/SelectedKeys） | TestAutoConsentPreservesSameGenerationBaselines | ✅ |
| 显式键物化：只增不减/幂等/通配防御/无 "*" 持久化 | TestExtendUnitSelectionMaterializesExplicitKeys | ✅ |
| 新增键下次对账自动纳入 | TestExtendSelectionPicksUpNewSourceKeysOnNextReconcile | ✅ |
| detached 单元物化不动 | TestExtendSelectionDetachedUnitUntouched | ✅ |
| UnitSourceKeys（claude/codex/非键级拒绝） | TestUnitSourceKeysCodex | ✅ |
| 扫描完成回调（skills+resources+rescan+nil 清除） | TestScanCompletionCallbackFires | ✅ |
| steer#2 取消语义（取消不授权/不写选择集） | TestUnifiedPrimitivesCancelNoWrite | ✅ |
| GrantUnitConsent 重构回归（跨目标隔离） | TestGrantConsentCrossTargetIsolation | ✅ |
| **App 入口**：启用→供给（claude/codex/skills/资源/LocalOnly 防线）→启动 lazy→新增键→来源删除→keep-local→无通配 | `app_wsl_unified_test.go` TestWSLUnifiedSupplyEndToEnd（真实 App+真实协调器，`EnableWSLUnifiedSupply`/`doPushWSLSupply`/`runWSLSharedFilesForSpec`/`KeepWSLCliConfigUnitLocal` 全走真实方法） | ✅ |
| App 入口：停用停止纳入（重扫后新技能不导入、自动化停止副本保持现状）+ 换代失效 + 重装新 HOME 重新启用恢复 | TestWSLUnifiedSupplyGenerationChangeAndDisable | ✅ |
| App 入口：无授权启动不调 RunUnits/不写 awaiting-consent；启用后 lazy 运行；pi 不越面 | TestWSLCliConfigAtLaunchRequiresConsent | ✅ |
| App 入口：扫描完成回调真正触发对账（技能在扫描结束后供给） | TestWSLUnifiedScanCompletionTriggersReconcile | ✅ |

回归面：`go vet ./...` ✅；`go test ./...` **48 包全部 ok**（含既有 wslsupply 8k 行测试、root App 面、bind manifest）；`GOOS=windows go build ./...` ✅。

前端：`npx vitest run` **41 文件 408 测试全部通过**（含新增统一管理 wrapper 4 测试：enable 逐单元透传/unified 状态位/disable/错误面；`frontend/src/__tests__/api/wslSupply.test.ts`）。

## 5. 类型等待（steer#3 授权的等待项，非缺陷）

`npx vue-tsc --noEmit` 当前 **7 个错误，全部**因 wailsjs 绑定未再生（本任务未手改 wailsjs，按任务书由 Leader 统一生成）：
- `wailsjs/go/main/App` 缺导出：`UpdateCLIInWSL`（安装 Agent 的方法，本 tree 无实现）、`EnableWSLUnifiedSupply`、`DisableWSLUnifiedSupply`（本任务新增 App 方法）；
- 生成的 `WSLConfigSupplyStatus` model 缺 `unified` 字段（`WSLCLISettings.vue` 3 处 + 测试 1 处读取）。

Leader 再生绑定后这些错误全部消失；届时 `npm --prefix frontend run build`（vue-tsc 门禁）才可通过。Vitest 不经类型检查，已全部通过。

## 6. 真实未覆盖（如实列出）

1. **Windows/WSL 真机行为**（macOS 宿主）：wsl.exe 身份探测/UNC 写入/9P 权限补偿/scaling——本任务用假探测面+临时目录覆盖逻辑面；真机归 P3。
2. **`UpdateCLIInWSL` 后端与 wslsetup 升级链路**：由并行安装 Agent 在其 worktree 实现（steer#3 已集成、浏览器 18 项通过）；本 tree 只带前端 wrapper（自主树复制）与按钮接线，Go 侧无该方法。
3. **wailsjs 绑定再生**：Leader 统一执行（含 `EnableWSLUnifiedSupply`/`DisableWSLUnifiedSupply`/`UpdateCLIInWSL`/`WSLConfigSupplyStatus.unified`）。
4. **Vue 组件挂载级测试**（统一管理行显隐/按钮交互）：未写组件 mount 测试（绑定未再生无法 mock 完整；Leader 已备统一管理 UI fixture 与 browser 脚本，最终集成后验证启用/停用/部分失败/升级/OMP——steer#3）。已覆盖：api wrapper 透传测试 + 模板经 vue-tsc 解析无语法错误（仅缺绑定类型）。
5. **大规模技能/资源批量对账的性能**（数百单元 RunUnits）：逻辑正确，规模未压测。
6. **e2e 浏览器脚本**（e2e/wsl-cli-supply-ui.html 等在主树由他人修改）：不在本 tree 写面，未触碰。

## 7. steer 处置记录

- **steer#1**（工具行升级按钮）：已实现（见 §2 表）；`updateCLIInWSL` wrapper 自主树复制、不计本任务产出；不依赖 Go 侧 `UpdateCLIInWSL` 的存在编译（Go 侧零引用）。
- **steer#2**（ctx 贯通）：`ExtendUnitSelectionIfUnified` 增加 ctx 参数并贯通身份探测 + mutate 前复查；`SetTargetUnified`/`AutoConsentIfUnified` 在 ResolveTarget 后、mutate 前复查；测试 `TestUnifiedPrimitivesCancelNoWrite` 断言取消不授权/不写选择。App 侧调用已全部传 ctx。
- **steer#3**（尽快报告/不做全量收尾）：未跑全量 go build 之外的收尾（vet/test 为验证所需已跑）；未生成绑定；本报告即交付。

## 8. 回滚方式

全部为增量改动：revert 本 worktree 的 13 个修改文件 + 删除 4 个新文件（`internal/wslsupply/unified.go`、`internal/wslsupply/unified_test.go`、`app_wsl_unified.go`、`app_wsl_unified_test.go`）即回到基线；状态清单新增字段（`TargetRecord.Unified/UnifiedAt`、`selectedKeys` 显式键）旧版本自然忽略，无 schema 升版（`stateSchemaVersion` 不变），无迁移需求。运行时停用：UI「停止自动纳入新项」+ 各单元「保留本地」。
