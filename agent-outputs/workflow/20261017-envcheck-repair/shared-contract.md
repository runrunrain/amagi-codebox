# E1/E2 共享操作契约（Leader 决策）

## 已观察缺口
- EnvCheckSettings 非Claude分支只有Headroom卸载按钮。
- envcheck/service.go 只有Claude/Headroom卸载，其他四Agent无通用链路。
- 不增加Wails绑定方法；沿用 App.RunEnvFixAction(action, tool, extraPath)。

## 新动作
- action = `uninstall_tool`，tool = opencode/codex/pi/omp。
- 后端把它登记为可执行修复动作并使用现有operation槽串行管理，返回既有FixActionResult（成功表示操作已受理，之后必须以snapshot.operation终态为准）。如既有机制无法安全异步，先反馈Leader，不默默改成成功即卸载完成。
- snapshot.operation kind = `uninstall`，status沿用 running/succeeded/failed/timeout，result按现有InstallResult。
- 前端确认卸载目标后调用，立即跟踪操作并轮询，终态刷新实际检测状态。不因为Start成功就显示卸载成功。
- Claude沿用现有按来源卸载，Headroom沿用租约保护卸载，不绕过安全检查。

## 安全约束
- 只卸载可确定所有者/包管理器及安装prefix的目标。优先实现确定npm安装；可确证Homebrew等用对应包管理器，未知/外部/自定义原生路径明确拒绝或提供手动指导，不任意删目录、不删除用户配置。
- 不能拿当前默认npm卸载另一个Node版本下的包；复检发现另一份安装应如实显示，不假报全部卸载。
- uninstall_tool为动作字符串，不新增导出请求/响应字段。若仍需公共类型变更先停在接口决策。

## 未知依赖
- node/npm依赖按钮仍用 install_node；其他依赖动作有新增需求先反馈具体缺口，避免未确认实现新按钮。
