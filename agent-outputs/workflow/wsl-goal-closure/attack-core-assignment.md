# G1 按攻坚契约统一供给提交、恢复、风险与取消

task_tier=complex，主特色工匠闭环，轻安全/取证。主上已授权“继续，直到目标闭环”。本任务不是重复上轮分支修补，必须完整消费已接受 attack-contract.md 的持久事实和失败顺序。该文是共享方向；不自行降成内存pending、失败后才首次备份或忽略A2 ctx。

## 工作区与依赖

新worktree `wsl-goal-transaction` 初始HEAD58e7577。确认干净后机械check/apply主树报告目录 `resumed-input.patch`。保存导入态快照，最终只交你的差量。

并行G0（75639eb2）正在实现平台原语与launcher A2。完整读取 `attack-platform-assignment.md` 获取**已冻结签名**。它仅写platform/launcher，不写你的core/root。你现在可推进数据结构、状态存储、失败矩阵和提交编排，**不能私自伪造平台实现/临时假成功stub**。G0产出 `attack-platform.delta.patch` 后Leader会通知你在本树check/apply；将该patch视为依赖baseline，最终你的delta必须相对「resumed-input+G0」生成，不能把G0重复带回。若需要依赖才能验证且它未就绪，报告blocked供Leader续接，不盲轮询。

主树必须完整读取：attack-contract.md、phase-rereview.md、phase-rereview-repro.go/.log；上一轮失败断言在review_fixes_test.go。**不能把实现报告met视为证据**。所有文件在 `/Users/run.mao/workpace/amagi-codebox/agent-outputs/workflow/wsl-goal-closure/`。主树源码不写。

## 本任务完整纵向责任

1. state v3、不可变磁盘OperationIntent、SettledOperations、StoreHealth、观察/状态快照；写前intent+前像验证，final save失败不丢事实，GetStatus和启动资格消费同一事实。读错不能空值覆盖，全部新增map深复制。pending摘要不含正文；普通敏感hash按契约，A2不得落auth正文或hash。
2. 文件及目录统一CommitRequest/Receipt，前像在不可逆移动前已经持久且完整验证，候选也预验证；出错receipt保留。有界no-replace发布/恢复，取消后独立有限recoveryCtx履行已发生写入责任。原目标唯一可靠时备份坏不得移动原目标；外部后改保留不覆盖。目录move-aside之后取消必须恢复或明确保留位置，空目录不丢。不得在失败后才首次保全；不得递归提交/恢复。
3. ReconcilePending按明确target、固定已登记路径运行；重建Coordinator无旧Undo时能识别intent/settled/目标。主清单失败首次/后续一视同仁，pending覆盖旧written；写未登记→关闭源→撤回不得Fresh漏副本。write/remove相关操作结清顺序、旧intent不复活秘密、dirty进度不把未确认当成功。Compensate/Release接入真实remote Abort/Commit和desktop/manual生命周期，不丢租用/责任。
4. LegacyEvidence保留v1原entry与顶层线索、v2派生map但不盲信；同次Identity导出旧公式LegacyGeneration。按当前目标的旧指纹与实际固定视觉文件核验，A发布→检查B→升级必须A阻B不误伤，clone逐目标解除。Detached/确切withdrawn/consent-only不伪造风险。无归属/unknown保真，不授写权、不全局误阻所有WSL。提供现有检查动作可调用的“手动处理后确认当前目标absent”真实解除路线，不写不存在的takeover提示。
5. Probes Identity/Chmod/StatMode改ctx，真实调用G0 Context平台API；A2Adapter.Sync(ctx,target)接G0 SyncWSLPiLoginAuthContext，使用完整已解析目标不重新选cachedHome。A2的无秘密pending摘要、结果归因/状态保存和启动聚合接线；可选失败不得抢先return跳过A2安全资格。
6. 插件真实ctx：取锁后、runner返回后、filters读/解析/tmp/最终提交前检查；复用同一提交/恢复回调，applyMu→opMu→短state.mu锁序；原生install/remove副作用摘要与filters分别记，不假称可回滚原生安装。首次失败也有待确认摘要，重启不自动重放。保留已经关闭的nil/[]/全nil重置语义和固定锚点。
7. shared gate必须用真实assessment/result facts，不能靠LastOutcome字符串或一个bool历史判断；本次写已发生/未知/敏感资格缺失不放行。5CLI desktop/remote/restart仍统一入口；无历史/确无当前副本的普通可选失败不误阻。状态公开需真实显示待确认/历史LastSuccess，不改成“已同步”。

详细持久化顺序、失败状态表、补偿/释放、API接线与测试矩阵以attack-contract为准。若发现契约实现不可行或签名冲突，立即给具体证据blocked/问题，不静默削弱；真实无限I/O不能保证恢复成功，可留下pending及可靠材料，但不可主动毁唯一前像或虚报。

## 范围与验证纪律

允许本树 `internal/wslsupply/` 相关core/resources/skills/plugin/测试、root App/gate/executor/login/tests、必要frontend状态展示/API包装。禁止改G0负责的platform/launcher（需要修改先经Leader协调），禁止改hermetic任务的envcheck与platform resolver_test，禁止清理用户数据/远端/旧worktree。状态/绑定变化按项目规则再生wailsjs，绝不手改。无新增第三方依赖优先；不commit/push，不全仓gofmt，不安装/运行真实CLI包、不读真实auth，不跑AMAGI_WSL_E2E。

测试先行：把复审repro转公开入口正式回归；设计确定性fault seam区分prepare/intent/move/publish/verify/finalize/cleanup，持续故障不是仅第一次破坏。每条断言文件字节、持久状态、新Coordinator、真实gate/PTYSart=0，不止helper返回error。完整执行attack-contract矩阵，必要组合table-driven；未执行项明确列出，不能删矩阵凑met。生产与测试均不得goroutine假超时留下失控写者。

至少目标root/wslsupply/launcher/platform/piplugin普通与-race；go vet；Windows交叉编译（不冒充执行）；若前端变动test/build及真实组件测试。日志留本树.amagi/attack-core，主树仅写约定产物。

## 交付

主树报告目录：`attack-core-implementation.md`、`attack-core.delta.patch`（仅你相对resumed-input+G0的净增量，含新文件）。报告逐契约条目 met/partial/missed、失败矩阵真实命令/结果、持续故障/重启/外部后改/取消恢复证据、未覆盖与Windows pending、迁移回滚约束；新增公共接口完整列表。不要再用“全部完成”掩盖未接线或未覆盖分支。完成后Leader集成与独立审核，不能自证放行。
