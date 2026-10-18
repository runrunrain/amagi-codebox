# E1 候选集成前阻断（统一修复批次）

本报告基于任务终态后实际读取的 worktree 文件，不以自述报告替代代码。尚未合入主树/验收。

## 1. 新增绑定面未消除（Major）
- uninstall.go:49 仍是 `func (s *Service) StartUninstallTool`，fix_dispatcher仍调用它，尽管报告声称已私有。Service被Wails绑定，此导出方法违反既定契约。
- 改为私有 `startUninstallTool`（含测试调用），不得手改wailsjs；通过rg确认没有新增Service导出方法。

## 2. 卸载归属校验过宽（Critical/Major，必须收紧）
- npmOwnedPath仅验证executable在当前prefix或node_modules根下。`<prefix>/lib/node_modules/another-package/.../pi`同样通过，然后会卸载官方Pi包而非实际命中的包。
- Homebrew从任意Cellar路径推formula即可 `brew uninstall formula`，即使formula为node/python等共享依赖。必须确认tool与formula归属，而非仅前缀。
- npm必须确认解析后可执行在该tool预期包目录内，或对平台shim验证其绑定到预期包（证据不足拒绝，不能退回纯prefix）。命令固定传入已验证prefix以避免npm进程环境变化删错地方；不解析不可信任manifest命令执行。
- Homebrew明确tool对应formula/cask的已知关系，未识别的formula拒绝；不能从任意路径段构造卸载目标。
- 测试：同prefix另一个npm包→拒绝且零卸载调用；Cellar/node/.../pi→拒绝且零brew uninstall；预期包/预期formula仍可卸载。

## 3. 检测失败不可当已卸载（Major）
- uninstallToolWithProgress precheck和postcheck使用 `before,_ := CheckOne` / `after,_ := CheckOne`。nil或探测失败不能作为确认不存在。
- 至少处理返回error和nil；成功必须拿到明确未安装状态。保留原命令错误（已经部分修复，勿回退）。

## 4. 未知最新版本仍假报最新（Major，steer #5未落地）
- installer.go isHealthyAndCurrent仅靠!HasUpdate判断，LatestVersion空也为true；enrichWithLatestVersion在npm view失败/空时静默return。
- 健康已安装和确知最新必须分开：安装可健康no-op但只说“已安装（最新版本未知）”；Update不能以空LatestVersion直接跳过并声称最新。至少要求非空已验证版本与比较结果一致。
- 补回归，确知最新保留no-op，未知不得报“最新”。如果更新后的版本不变但最新未知，结果措辞要诚实，不假造升级。

## 5. 依赖仍存在未复检成功（Major）
- Windows winget exit0但npm复检失败仍Success=true/Changed=true，不能说明依赖已可用；应明确未确认/需要重启（不返回安装成功）。Linux手动指导分支仍Success=true也应纠正。
- PATH现已同步当前进程，RequiresRestart/nextSteps应准确指向外部终端，不仍声称CodeBox当前PATH一定没生效。

## 验收与报告
- 在原worktree修复上述有限清单，不再开放探索其他领域。
- 加针对性测试后跑envcheck+platform定向、race（必要），Windows编译；不用全仓。
- 更新backend-report内容匹配真实代码。当前report说“已私有”但实际未私有，必须纠正；不以末行done代替完成。
- Headroom安装Python仍保留明确边界，本批不新增安装器。
