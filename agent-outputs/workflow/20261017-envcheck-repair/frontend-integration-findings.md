# E2 集成发现（统一修复批次）

Leader 已把 E2 净增量合入主树候选（未验收），真实组件 + mock Wails 冒烟 `node e2e/envcheck-management-smoke.mjs`：前4项通过，第5项 snapshot-error 超时失败，日志 `/Users/run.mao/.pi/agent/amagi/data/run-bg/bg-39faf13e.log`。

## Major 1：受理回执仍未接管状态，首次快照失败后不轮询
- startTrackedOperation 取得 started 但只用id筛横幅，未缓存running状态也未建立pendingOperation。
- fetchSnapshot吞错后 ensurePollingState 仍看旧快照的 operation=null，停止轮询。
- 复现 `?scenario=snapshot-error` → Pi → 安装：fixture已succeeded，界面仍未安装。
- 修复要求：以受理回执作为本次operation身份/跟踪起点，即使首次快照失败也继续有界重试；后续终态即使没见过服务端running也应显示；snapshot乱序不能把终态回退到running/旧操作。uninstall_tool无OperationState返回，需要本地pending记录（tool/kind/前一operation id）确保相同语义且不误报旧卸载终态。

## Major 2：操作前没有统一互斥，修复按钮loading key不匹配
- 模板顶层solutions用solutionKey(sol,card,1000+sIdx)，executeSolution固定idx=0。因此顶层修复按钮处理中不禁用也不显示loading；issue数组非0下标同样错。
- 真实浏览器确认：dependency→Pi→点击Node按钮，await microtask后 button.disabled=false、text仍“安装 Node.js”、请求1条仍运行。
- 普通install/update/uninstall在await受理期间也没本地pending guard，未收到running快照前可重复提交/切卡片并行操作。
- 修复要求：稳定action identity（无需位置idx）+ 全局本地操作busy并在handler内guard（不能仅template）；检查/修复/安装/卸载忙碌状态一致，成功/失败都释放。手动命令展示可以保持非写操作，不受错误屏蔽。

## Major 3：修复后异步CheckAll窗口仍可丢失
- planPostFixRefresh.pollUntilSettled 从未消费，ensurePollingState只看当前snapshot.checking；App的go CheckAll可能在首次fetch后才开始，仍会停在旧缓存。
- 成功后需要确定性的复检或受控刷新跟踪，而不是依靠是否刚好碰到checking=true。可结合后端E1最终契约，不得无限轮询；失败后有用户可见重试入口。

## Minor：文案与数据身份
- restart_notice / buildFixResultBanner注释写“GUI PATH运行中无法刷新”，E1正修复当前进程PATH同步，此绝对说法需改为“若仍有旧环境/外部终端，请完全退出重开或重新检测”等准确条件，不宣称无法修改进程环境。
- solutionDedupeKey缺少tool（同动作不同工具会错误合并）；solution identity至少type/tool/method/command。

## 验证
请工作树内修复并补纯逻辑或可运行的状态管理测试；不要复制主树候选覆盖worktree已有文件。
主树harness已就绪，Leader负责重跑10项及补重复点击/卸载首次快照失败场景。报告更新到工作树约定报告路径（目前工具映射写到了worktree，Leader会明确回流）。
