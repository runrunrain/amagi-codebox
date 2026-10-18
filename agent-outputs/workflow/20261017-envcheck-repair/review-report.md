# E4 最终增量复审

VERDICT: PASS

**R-1、R-2 均关闭；本轮未发现修复引入的阻断回归。** 结论仅针对首审两项及直接影响面，不代表真实 OS 软件管理已实测通过。

## 基线与范围
- 基于主树本次补修，以上轮 `review-initial-report.md` 为问题基线；未重开全仓审计，首审未改部分沿用原审核覆盖。
- 必读输入：已完整读取 `integration-report.md`，含新增“E4 首审修复与最终回归”，并核对首审存档。
- 实码覆盖：四个 `checker_{pi,omp,codex,opencode}.go` 的 fallback 增量、`npm_candidates.go`、`uninstall.go` 的删除确认与相邻分支、完整 `uninstall_probe_failure_test.go`；组件 tracker→终态横幅/unknown→watcher 链路；e2e HTML 与 smoke 新增两场景；Service 指针方法集反射修正。
- 仅写此报告；未改源码/测试、未提交、未操作真实安装或 PATH/profile。前序 GeneralSettings/usage 等不在本次范围。

## 原编号逐项结论

### R-1｜原 Major｜已关闭
- **代码证据**：四 checker 在 fallback 返回非 nil 状态时保留其状态与 error；所有存在但版本探测失败的候选不再被转换成 missing。`npm_candidates.go:15` 的 `failedNPMCandidateStatus` 保留 Installed=true、原路径及脱敏诊断，PATHOk=false；仍允许优先选取后续健康候选。
- **卸载确认**：`uninstall.go` 在复检 Installed=false 后、成功返回前调用 `verifyUninstalledExecutable(before.ExecutablePath)`。Lstat 明确不存在才通过；空路径、残留文件、其他文件系统错误均失败，命令异常信息保留。原 ownership/prefix 限制未被放宽。
- **验证有效性**：`TestUninstallFallbackProbeFailureNeverMeansRemoved` 对四工具分别覆盖“前检已损坏”“卸载后损坏”，明确断言 resolver 不可见、入口仍存在、终态失败；不是用 mock 直接伪造 CheckStatus。原入口辅助测试覆盖残留、空路径、确认不存在、非 ENOENT 错误。正常卸载原有正向测试同轮通过。
- **红绿证据**：已读 `bg-d4910beb.log`，8 条失败确实显示旧实现假成功/误报 missing；修复后本轮两包测试独立重跑通过。首审“只判 CheckOne nil/error 不足”的问题已按实际失败链路消除。

### R-2｜原 Major｜已关闭
- **代码证据**：`EnvCheckSettings.vue:1491–1494` 的 watcher 仅维护轮询；原 `maybeSurfaceOperationCompletion` 任意槽兜底已删除。异步终态横幅只由 `applyTrackerObservation` 的匹配 terminal 事件调用 `surfaceTerminalOperation` 发布；unknown 不会再通过 running→null 转换认领槽内旧结果。
- **验证有效性**：e2e 新增 `uninstall-stale-timeout` / `uninstall-replaced-timeout`：前者持续保留 previousId 的旧成功槽，后者置为 Codex 安装成功槽；推进隔离页面时钟 46 秒后等待自动 CheckTool，再等待 2200ms 验证横幅仍为“状态未知”且不含“成功”。生产 45 秒预算未修改，也未把 tracker/组件替换成 mock。
- **红绿证据**：已读 `bg-f6dbda60.log`，旧实现实际出现“Pi 卸载成功”使首个新增断言失败；已读 `bg-a7d929e4.log`，修复后 15 场景通过，包含两个超时场景及原即时完成、首拉失败、正常卸载路径。该浏览器执行是 Leader 上游证据，本轮未重复启动浏览器。

## 直接影响面与验证记录

| 项目 | 证据 / 结果 |
|---|---|
| 本轮独立 Go 回归 | cwd=`/Users/run.mao/workpace/amagi-codebox`；`go test -mod=vendor ./internal/envcheck ./internal/platform -count=1`，exit 0（28.730s / 1.173s）；包括新增失败探测用例和既有正常卸载/所有权/PATH 用例。 |
| 本轮独立前端单测 | 同 cwd；`npm --prefix frontend run test`，exit 0，34 文件 / 327 测试通过。 |
| 前端构建 / 真实组件替身 | 已完整读取 `bg-a7d929e4.log`：vue-tsc + Vite 构建成功，15 条 smoke 通过。构建/浏览器未在本轮重跑。 |
| 全面回归上游证据 | 已完整读取 `bg-703550de.log`：全仓 Go、envcheck/platform race 和 Windows 目标编译的成功输出；integration-report 记录该批含 vet、总 exit 0。未重复全仓长测试；静默 vet 的执行与退出状态依据集成报告，不将其日志无输出视作独立证明。 |
| 绑定面检查 | 反射改用 `reflect.TypeOf((*Service)(nil))`，能检查指针接收者；补修未新增 Service 绑定方法或字段。 |

未新增 Critical/Major/Minor finding；未发现需要另列的范围外遗留观察。

## 残余边界
- OS 安装/卸载、真实 Homebrew、Windows 注册表/winget/PowerShell、外部终端 PATH 生效均未实机执行；Windows 仅目标编译，不是运行测试。
- Go 使用临时目录及注入 runner；浏览器使用真实 Vue 组件和内存绑定替身，时钟推进用于触发真实跟踪超时，不构成实际软件卸载证据。
- 原入口 Lstat 是时间点证据，不是对并发外部重装或所有未知安装位置的全局不存在证明；保留首审对未知来源拒绝、其他 Node 前缀拒绝的边界。
- 不作视觉结论；不扩大到 WSL 全组合或前序独立任务。

## 上游与产物
上游位于 `agent-outputs/workflow/20261017-envcheck-repair/`：`integration-report.md`、`review-initial-report.md`、`shared-contract.md`。日志均位于 `/Users/run.mao/.pi/agent/amagi/data/run-bg/`，文件名见上述编号。

本轮唯一产物为此 `review-report.md`；首审存档不变。如需撤销本轮产物，仅处理此报告，无代码回滚操作。
