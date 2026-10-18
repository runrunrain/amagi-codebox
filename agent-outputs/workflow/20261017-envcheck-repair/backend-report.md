# E1 后端环境检测/管理链路修复报告

- 基线：HEAD=7867cc3（worktree amagi/envcheck-backend-repair，未提交，改动留在工作区）
- 输入：workflow.md、backend-task.md、local-path-evidence.md、shared-contract.md、backend-integration-findings.md、5 条 Leader steer
- 轮次：R1 五项根因修复；R2 按 backend-integration-findings.md 5 项有限清单收紧（本版）。本报告描述与实际代码一致，关键断言均可用 rg 复核（命令见 §四）。

## 一、R1 根因修复（保留，细节经 R2 收紧）

1. **RC-1 PATH 修复后当前进程环境不同步**（“PATH修复无效”）：runFixPath 原本只写 shell profile，GUI 进程整会话看不到修复目录，`path_not_in_system_path` 永不清除。修复：`applyPATHDirsToProcessEnv`（os.Setenv 前置去重）覆盖 POSIX/Windows 全部分支；Windows 补 resetNPMCache+同步复检。测试：`TestFixPathAppliesDirsToCurrentProcessEnv`、`TestFixPathIdempotentKeepsProcessEnvInSyncWithoutDuplicates`。
2. **fix_path 非幂等**：collectPathDirs 原始字符串去重，/var↔/private/var（符号链接）同目录双收 → 每次点击重写 profile 并累积重复行（R1 DBG 实证）。修复：addIfNew 统一 EvalSymlinks 规范化（Windows 键小写）。
3. **诊断标签当目录**：diags.PATHSources（"merged-env" 等标签）曾被按 cwd 相对路径收集。修复：删除该循环，只收集 resolved.Path 目录。测试：`TestCollectPathDirsIgnoresResolverDiagnosticsLabels`。
4. **install_node Windows 假成功 + 超时不足**：winget 失败曾返回 Success=true；winget/brew 120s 预算杀半途安装。修复见 R2-5；预算放宽至 npmCLIToolInstallTimeout（10min）。
5. **fnm 检测锁定**：本机只读探针证明 HEAD 上 GUI 最小 PATH 可解析 fnm 稳定根（aliases/default + node-versions）；`TestCheckPiDetectsFNMStableRootInGUIEnv` 锁定服务级行为。

## 二、R2 修复（backend-integration-findings.md 有限清单）

### F-1 新增绑定面未消除 → 已私有
- `StartUninstallTool` → **`startUninstallTool`**（私有），`CanUninstallTool` → **`canUninstallTool`**（私有）；fix_dispatcher 与全部测试同步改用私有名。rg 验证：uninstall.go 无任何 `func (s *Service) [A-Z]` 导出方法；仓库内无 StartUninstallTool/CanUninstallTool 残留引用。
- 测试：`TestUninstallToolAddsNoExportedServiceMethods`（reflect 断言 Service 无这三个导出方法名 + 编译期引用证明私有方法存在）。前端唯一通道仍是 `App.RunEnvFixAction("uninstall_tool", tool, extraPath)`。

### F-2 卸载归属校验过宽 → 预期包/已知 formula 双收紧
- **npm 渠道**：删除原 `npmOwnedPath`（纯 prefix/根前缀判断）。现在要求二选一：(a) 解析后可执行文件位于**本工具预期包目录** `<npmRoot>/<npmPackageName(tool)>/…` 内；(b) 它是直属于 `<prefix>/bin`、`<prefix>` 或 `<npmRoot>/.bin` 的平台 shim，且 **shim 文本包含 `node_modules/<pkg>/` 路径**（读文件≤64KB 验证绑定，不执行、不信任 manifest；不可读/超大 fail-closed）。证据不足一律拒绝（不退回纯 prefix）。卸载命令固定 `npm uninstall -g --prefix <已验证prefix> <pkg>`，避免探测与卸载之间 npm 环境漂移删错根。
- **Homebrew 渠道**：新增显式映射 `homebrewUninstallFormulaAllowlist`：opencode→"opencode"、codex→"codex"、omp→"omp"（can1357/tap 的 keg 名，与 ompSelfUpdateCommand 同源）；**pi 无已知 Homebrew formula → 恒拒绝**。Cellar 路径推导的 formula 必须命中本工具映射，否则拒绝（含 node/python 等共享依赖 keg）；仍保留 `brew --prefix` 前缀复核。
- 测试（零卸载调用 + 文件未动）：`TestUninstallToolRefusesSiblingPackageUnderSamePrefix`（同根另一包拥有 shim → 拒绝）、`TestUninstallToolHomebrewRejectsSharedDependencyKeg`（Cellar/node keg → 拒绝）、`TestUninstallToolHomebrewRejectsUnmappedFormulaForTool`（opencode keg 里的 codex → 跨工具拒绝）；正向保留 `TestUninstallToolNPMChannelRemovesAndVerifies`（断言含 --prefix 与包名完整参数序列）与 `TestUninstallToolHomebrewChannel`（omp 预期 formula 可卸载）。

### F-3 检测失败不可当已卸载 → precheck/postcheck 显式处理
- precheck：`before, checkErr := s.CheckOne(tool)`；`checkErr != nil || before == nil` → installFailure（“卸载前检测失败，无法确认安装状态，已拒绝卸载”）+ 保留 error。仅明确的 `!before.Installed` 才 no-op。
- postcheck：`after, afterErr := s.CheckOne(tool)`；`afterErr != nil || after == nil` → 失败（“卸载后复检失败，无法确认卸载结果（含命令异常脱敏文本），请手动重新检测”）；`after.Installed` → 如实失败（保留剩余路径/来源 + 命令异常注记）；成功必须拿到明确未安装状态。命令错误文本沿用 R1 的脱敏保留（未回退）。
- 说明：CheckOne 对合法工具的现行契约下 status 几乎恒非 nil（finishToolCheck 吸收 error），nil/error 分支为防御性闭环，无法用注入 runner 强制触发——已按代码审读确认并在测试中覆盖 error 语义链（`TestUninstallToolStillDetectedAfterUninstallReportsHonestFailure`）。

### F-4 未知最新版本假报最新（steer #5）→ 健康与确知最新分离
- installer.go：拆分 `isHealthy`（Installed+PATHOk+无 Error，不涉及版本）与收紧后的 `isHealthyAndCurrent`（isHealthy + **非空 LatestVersion** + !HasUpdate）。
- Install no-op：healthy 即 no-op，但文案分叉——“已安装且为最新版本（x.y.z）”（确知）vs **“已安装（最新版本未知）”**（未知，不再冒充最新）。
- Update no-op：**必须** isHealthyAndCurrent（非空已验证 LatestVersion 且比较无更新）才跳过并文案“已是最新版本（x.y.z，与 registry 一致）”；最新未知时继续执行更新命令。
- 更新后版本未变：保持失败语义（既有测试锁定，不假造升级），但失败文案追加“最新版本号未知（可能已是最新但未获证实）”，不误导为安装损坏。
- 同步修正旧测试 `TestIsHealthyAndCurrent`：原用例把 LatestVersion 空当作 current（正是被点名的语义），改为 unknown→false、verified→true 两用例。
- 新测试：`TestUpdateProceedsWhenLatestVersionUnknown`（未知→执行更新并真实升级成功）、`TestUpdateNoopOnlyWhenLatestVerifiedCurrent`（确知才跳过且零安装命令）、`TestInstallNoopMessageHonestWhenLatestUnknown`（文案含“最新版本未知”且零安装命令）、`TestUpdateVersionUnchangedWithUnknownLatestIsHonestFailure`（失败 + 含“最新版本号未知”）。

### F-5 依赖未确认不冒充成功
- Windows winget exit0 + npm 复检失败 → `Success:false` + Error“Node.js 依赖未确认可用：请重启 CodeBox 后重新检测” + RequiresRestart；复检成功才 Success:true（“npm 已可用”）。测试：`TestInstallNodeWindowsWingetSuccessNPMAvailabilityUnconfirmed`、`TestInstallNodeWindowsWingetSuccessProbesNPMAvailability`（复检成功路径）。
- Linux 手动指引分支 → `Success:false` + Error“依赖未确认可用” + 保留 4 步指引（含重检确认步骤）。测试：`TestInstallNodeLinuxManualGuidanceNotSuccess`。
- PATH 修复 NextSteps 措辞校准（当前进程已同步）：Windows 写入分支改“已同步当前进程环境并重新检测；外部终端如需生效，请新开终端或重新登录”；POSIX 分支 R1 已是同等措辞。

## 三、真实验证（R2 后全部重跑）

| 项 | 命令 | 结果 |
|---|---|---|
| envcheck 全量（含 24 项新增/更新回归） | `go test -count=1 ./internal/envcheck` | ok（~25s） |
| platform 全量 | `go test -count=1 ./internal/platform` | ok |
| race | `go test -race -count=1 ./internal/envcheck ./internal/platform` | ok |
| Windows 编译 + vet | `GOOS=windows GOARCH=amd64 go build ./internal/envcheck ./internal/platform .`（stub dist）；`go vet` | exit 0 |
| gofmt | `gofmt -l internal/envcheck internal/platform` | 无输出 |
| 实码核对（非自述） | rg 见 §四 | 与本报告一致 |

未执行：真实安装/卸载/更新、Agent CLI 运行、用户配置修改。全仓回归留待 Leader 集成。

## 四、rg 实码核对命令（供 Leader 复核）

```
rg -n "func (s \*Service) [A-Z]" internal/envcheck/uninstall.go        # 空 = F-1 无导出方法
rg -n "StartUninstallTool|CanUninstallTool" internal/envcheck app.go   # 空 = 无残留
rg -n "expectedPkgDir|npmShimBindsToPackage|--prefix" internal/envcheck/uninstall.go  # F-2 npm
rg -n "homebrewUninstallFormulaAllowlist|homebrewFormulaAllowedForTool" internal/envcheck/uninstall.go  # F-2 brew
rg -n "checkErr|afterErr" internal/envcheck/uninstall.go               # F-3
rg -n "func isHealthy\(|func isHealthyAndCurrent|最新版本未知|最新版本号未知" internal/envcheck/installer.go  # F-4
rg -n "未获确认|依赖未确认" internal/envcheck/fix_dispatcher.go         # F-5
```

## 五、任务基线净改动（R1+R2 累计）

修改：internal/envcheck/{types.go, service.go, fix_dispatcher.go, installer.go, service_test.go}；新增：internal/envcheck/{uninstall.go, uninstall_tool_test.go, pathfix_regress_test.go, depbutton_regress_test.go, version_status_regress_test.go}。未触碰前端/wailsjs/usage/app.go/新依赖；未提交。回滚：git checkout 修改文件 + 删除 5 个新增文件。

公共契约：R1 报告曾误述 StartUninstallTool 已私有——本版已真实私有化；现新增导出符号仅 `SolutionUninstallTool`（SolutionType 新值，非 Service 方法、非绑定面）。Headroom Python 安装边界保持（本批不新增安装器）。

## 六、剩余边界

1. npm shim 文本验证覆盖 npm 生成的 .cmd/.ps1/无后缀 shim（均内嵌 node_modules/<pkg>/ 路径）；非 npm 生成且不含该路径的 shim 会 fail-closed 拒绝（安全侧）。
2. Homebrew formula 映射以本仓可验证来源为准（ompSelfUpdateCommand 同源）；未来 formula 改名需同步 allowlist。
3. CheckOne nil/error 防御分支不可注入触发（现行契约 status 恒非 nil），见 F-3 说明。
4. Windows reg/powershell 路径仅编译验证，建议集成轮实机冒烟；版本探测 10s×2 超时语义未动。
