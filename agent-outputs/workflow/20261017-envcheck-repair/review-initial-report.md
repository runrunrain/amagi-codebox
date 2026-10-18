# E4 full 风险审核（本阶段增量）

VERDICT: FIX

发现 **2 Major，0 Critical，0 Minor**。两项均由实际调用路径静态证实；本轮没有新增复现脚本或项目测试，不将静态证据写成运行复现。

## 基线与输入
- 主树 HEAD：`7867cc3`；审核相对 HEAD 的工作区增量，包含未跟踪文件，不审旧 worktree。
- 已完整读取同目录 `workflow.md`、`shared-contract.md`、`integration-report.md`、`backend-report.md`、`frontend-report.md`、`backend-integration-findings.md`、`frontend-integration-findings.md`、`review-task.md`。
- 排除 GeneralSettings/useDefaultEngineSettings、UsageStatsPanel、internal/usage、usage.md 统计段；未改源代码、用户配置或绑定，未提交。

## Findings

### R-1｜Major｜卸载复检仍把“候选存在但不可运行”当作已删除
- **定位**：`internal/envcheck/uninstall.go:72–86,124–169`；直接依赖 `checker_pi.go:34–40,78–109`、`service.go:279–291`。
- **触发条件 / 可复核路径**：
  1. Pi 安装在自定义 npm prefix（不在 PATH / resolver 已知目录），npm 本身可用；`npm prefix -g`/`root -g` 返回该安装。首次 `CheckOne(pi)` 经 npm fallback 的版本探测成功，得到 Installed=true 和包内 executablePath，因此允许执行卸载。
  2. 卸载失败或部分执行后，包入口文件仍存在，但其版本命令失败（例如依赖损坏、权限或 Node 运行问题）。`checkPiFromNPMGlobalPrefix` 对存在候选执行版本探测，全部失败后返回 error。
  3. `checkPi:35–40` 丢弃该 error，返回 **Installed=false + “not found in PATH”**；`finishToolCheck` 返回非 nil status、nil error。卸载的 `afterErr/nil` 防线完全不会触发，直接落入成功分支；即使命令曾报错，155–161 行仍返回 Success=true。
- **实际影响**：文件未消失却发布 `operation.status=succeeded` 和“复检确认卸载成功”；同类损坏状态在 precheck 还会被误报为“无需卸载”。这是新卸载链路暴露的真实契约违背，不是要求把既有 checker 全面重写。
- **证据与测试缺口**：现有 `TestUninstallToolStillDetectedAfterUninstallReportsHonestFailure` 的文件保留场景仍让版本探测成功，且工具在 PATH 上；没有锁定上述 fallback 失败路径。`CheckOne` 的 nil/error 检查不足这一待证疑点成立，但**不是所有版本失败都会误判**：直接 resolver 命中的路径会先设 Installed=true，所以应针对 fallback，而非泛化判断。
- **最小修复方向**：区分“确认不存在”和“候选存在但探测失败/未知”；卸载后结合原 executable / 包入口存在性核实（仅明确不存在可作为删除证据，权限错误不是不存在），保留失败诊断。不能简单要求 `status.Error==''`，因为正常缺失状态本身也带 Error。补隔离用例：prefix 不在 PATH、前检成功、卸载后入口残留且版本探测失败，终态必须失败/未确认；前检已损坏也不能成功 no-op。

### R-2｜Major｜unknown 兜底后组件 watcher 会将别的/旧操作终态报为本次成功
- **定位**：`frontend/src/views/settings/EnvCheckSettings.vue:803–829,898–904,1506–1512`。
- **触发条件 / 可复核时序**：
  1. 当前页面尚未展示过槽内旧终态 A；新卸载 B 已受理，pending.id 为空且 previousId=A（或已有 B 的 ID，但槽被其他操作顶替）。连续快照只观察到不匹配的终态 A。
  2. 跟踪器正确拒绝 A，45 秒后产出 unknown、清空 pending；组件清理缓存 operation 并显示“状态未知”。`runningOperation` 因此从本地 pending 变为 null。
  3. `watch(runningOperation)` 被触发，再取快照；若仍取得 A，1511 行将 **A 自己的 ID** 传给 `maybeSurfaceOperationCompletion`。898–904 行只检查“快照 ID 等于传入 ID”，等于自证，完全没有验证它属于 B；随后旧 A 的“卸载成功”（也可能是其他工具安装成功）覆盖 unknown 横幅。
- **实际影响**：纯状态机保证的“不认领旧槽、未知不冒充成功”在真实组件组合后失效；操作槽顶替/持续陈旧观察正是本轮有界未知策略明确承诺覆盖的情况。
- **证据与测试缺口**：`envcheckOperationTracking.test.ts` 的旧卸载终态用例只断言到 pending=null / unknown，不运行该 watcher；e2e 13 场景也没有持续旧槽→超时场景。现有绿色测试不能排除这条组件路径。
- **最小修复方向**：终态横幅统一由匹配后的 tracker 事件产生；若保留 watcher，必须校验本次受理/已认领身份，unknown 结束后不得无条件展示槽内终态。补真实组件/替身用例：持续返回旧成功槽至超时后仍保持 unknown，且不出现任何本次成功通知；另覆盖其他工具槽顶替。

## 覆盖清单与前轮问题回核

| 维度 | 审核结果 |
|---|---|
| 卸载所有权 / 权限 | 已读完整新增 uninstall.go、uninstall_tool_test.go、uninstall_ownership_test.go。npm 固定包名和 prefix、预期包目录/shim 证据、foreign/sibling 拒绝；brew prefix 相对布局与 tool/formula allowlist 拒绝 node/python 共享 keg。未发现新的误删路径。 |
| 公共绑定 / 兼容 | 新卸载入口私有，复用 RunEnvFixAction 字符串；`git diff --name-only -- frontend/wailsjs bind_list.go` 无输出。没有新增 Service 导出方法。reflect 测试用值类型而非指针，不能独立证明这一点；实际代码检查补足该证据。 |
| PATH | 审读 fix_dispatcher 增量及相邻写入/校验路径、完整 pathfix_regress_test.go：标签排除、符号链接规范化、幂等块、当前进程同步、备份/安全目录校验、外部终端重开文案。Windows 注册表分支仅静态审核，不作为实机通过。 |
| 状态真伪 | 已读 installer/service/types 增量及 depbutton/version_status 新测试；未知最新不再 current no-op，依赖命令失败/未确认返回失败。卸载确认仍有 R-1。 |
| UI / 时序 | 审读组件 template/script、完整 solutions/tracker 及两份对应新测试：受理忙碌、失败续轮询、即时终态、时间界/previousId、长操作观察窗口、响应序号、卸载停止轮询均有实现；组合终态观察有 R-2。 |
| E2E / 文档 | 已审内联 HTML、完整 smoke/config；绑定仅内存替身，未伪装真实 OS 管理。api/faq/usage 环境检测增量与共享契约一致，但 R-1/R-2 使实际行为尚未满足文档承诺。 |
| 一致性 / 并发 / 性能 / 维护 | 操作槽复用、查询预算、shim 有界读取、缓存刷新和轮询清理已检查；未扩大至其他 App/WSL/usage 链路。 |

前轮回核：backend #1/#2/#4/#5 已关闭；#3 **未关闭（R-1）**。frontend Major 2/3 与 Minor 已关闭；Major 1 **部分未关闭（R-2）**。无以旧问题充当本阶段回归的范围外发现。

## 验证证据与可信度

本轮 cwd 均为 `/Users/run.mao/workpace/amagi-codebox`：
- `go test -mod=vendor ./internal/envcheck ./internal/platform -count=1`：exit 0，两包通过（25.671s / 1.208s）。
- `npm --prefix frontend run test`：exit 0，34 文件 / 327 测试通过。
- `git status --short`、限定 `git diff`、`git rev-parse --short HEAD`：确认增量/未跟踪文件及基线；只写本报告。
- 集成报告记录的全仓/race/vet、前端构建、13 场景浏览器与 Windows 交叉编译作为上游证据引用，本轮未重跑，未冒称独立复核其日志。

**边界**：无真实 OS 安装/卸载/PATH/profile 操作；无 Windows 实机/注册表/winget、Homebrew 实装或 WSL 全组合验证；无视觉评审。本轮 findings 是静态路径证实，需实施者补正式回归并由集成阶段复核。测试全绿不覆盖两个失败时序。

**产物与回滚**：本轮唯一写入为此 `review-report.md`，不需代码回滚；如需撤销审核产物，仅处理此报告，勿覆盖其他未提交改动。
