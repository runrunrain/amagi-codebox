# PR01–09 合并修复（唯一增量修复批次）

task_tier=complex；主特色工匠闭环，轻安全/测试。按 phase-review.md 已确认 findings 修复，不重新大设计、不扩范围。

## 工作区起步

你将在新隔离 worktree `wsl-goal-review-fixes`（基线58e7577）运行。**该树初始不含已集成C1-C5未提交改动**。首先确认HEAD=58e7577且源码干净，然后机械执行 `git apply --check /Users/run.mao/workpace/amagi-codebox/agent-outputs/workflow/wsl-goal-closure/integrated-stage.patch`，成功才apply。失败就blocked，不回落主树。该500KB patch是完整集成源码/测试/CI快照，不含报告和用户resume-relay；只需机械校验/导入，不要求逐行全文读此patch。

导入后生成本树“输入快照diff/hash”（存worktree的.amagi下或报告目录），**最终另导出仅本轮PR修复的delta.patch**到主树报告目录，避免Leader误把整个C1-C5再次应用。可用git diff相对导入后文件快照或临时index生成，禁止commit。报告逐项列修改文件/修复和验证，含新文件。

## 必须完整读取

主树绝对目录 `/Users/run.mao/workpace/amagi-codebox/agent-outputs/workflow/wsl-goal-closure/` 内：phase-review.md、direction.md、verification.md。复现依据 phase-review-repro.go / phase-review-mode-repro.go / phase-review-restore-repro.go / phase-review-cancel-repro.go / phase-review-filters-repro.go 及对应.log。复制到本树.amagi子目录显式go run FILE可用（所有脚本均合成临时目录）；不要在主树运行会写源码的动作。审查说明的复现为真实证据，不以旧C2/C4报告的已完成声明替代它们。

## 修复范围与验收

- PR01：shared gate未知/blocked/failed必须区分“证明无受管副本”与“可能残留”。legacy不继承写授权，但旧敏感副本风险不能在迁移中消失；未知归属不得自动删。以明确目标generation/身份线索维护待确认状态，不能将别的distro副本全局误算进当前目标。有历史风险且无法确认的启动拒绝需中文可处理错误；无历史/无副本的普通可选配置不阻断。覆盖5CLI入口gate及identity/source failure。
- PR02：所有被选择消费的敏感Fresh/已有副本路径（coordinator的vision/models，launcher Pi/OMP/OpenCode，A2各fallback）统一验证目标权限；unknown不等于安全，可chmod后复验，仍不可证则阻断。不删混合文件。健康0600保持零字节重写。
- PR03：提交读回失败恢复必须有界、非递归、保全用户前像与后改；不能无条件删现存目标。可采用保留安全前像+返回需恢复错误而非冒险覆盖未知状态，明确诊断与恢复边界；不新增庞大事务框架。故障注入测试持续失配、取消、后改、清理失败；秘密备份同样0700/0600，避免新增泄露。
- PR04：DropUnitRegistration/SetUnitSelection等所有目标性mutate使用明确目标generation，不能st.entry取全局最近代。停机可停跟随的现有承诺需保留（目标索引可用，歧义拒绝不改别人）；unknown/legacy不搬运其他目标授权与基准。A→B→操作A回归。
- PR05：state持久化失败明确content-committed/state-unpersisted，不返回synced/applied成功。mutate失败不把内存当已持久化；后续状态/重启不可误判归属。涵盖投影、接管、撤回、部分键；不能通过删除成功写入的用户混合文件凑一致。插件既有registration-unchanged语义勿退化。
- PR06：stateStore.load提供锁内深复制一致快照，覆盖slice、Subs/选择集、Targets/InspectedAt、pending、plugins filters等嵌套结构；生产race必须消除，不止改测试。新增读快照不可变/嵌套不别名测试。
- PR07：取消贯通skill/resource takeover与目录提交、A2/插件后继本地写。入口/等锁后/每单元/不可逆交换前检查ctx；已取消请求零新写/安装。不虚称Windows wsl.exe退出已证明Linux进程退出。
- PR08：插件update支持从有过滤/显式[]重置到完整加载，与Windows当前期望一致；区分未提供选择与显式全nil期望，保留旧调用合理兼容，避免扩大CLI安装支持。
- PR09：修复测试异步push生命周期和fakeProbes并发race；等待真实退出，不sleep碰运气，不跳过race/删并发测试。
- PR10 **不由你修改**：Leader将撤回主树危险verify-staging-window.ps1，不再提供未经真机验证的替代自动脚本；最终Windows清单保留人工授权验证要求。报告记Leader负责即可。

## 写边界 / 禁止

仅修本worktree内相关root供给/启动gate/测试、internal/wslsupply、internal/launcher（必要的piplugin/App filter透传）。需要碰其它业务模块先说明必要性。禁止主树源码写入；主树只允许约定report/delta及本轮测试日志。不得动真实密钥、用户home配置、WSL用户/全局设置，不跑AMAGI_WSL_E2E。不得手改wailsjs，不全仓gofmt，不git commit/push，不清理其它worktree。保持C1/C3/C4/C5修复、原生语义和BuildPlan无新增供给副作用。

## 验证与交付

复现脚本转正式回归、先证明反例，再修。至少：`go test . ./internal/wslsupply ./internal/launcher ./internal/piplugin -count=1`、相同目标`-race`、`go vet`目标包、Windows交叉编译测试（`GOOS=windows GOARCH=amd64 go test ... -run '^$' -exec /usr/bin/true`，只编译，不伪称执行）。可以并行定向验证，避免反复全仓suite。日志持久化，失败按新回归/基线/宿主污染分账。

最终产物：主树报告目录 `review-fix-implementation.md` + `review-fixes.delta.patch`（仅本轮差量）；列PR01–09 met/partial/missed、准确未覆盖边界、每个失败路径如何保护用户文件和秘密。不能安全闭合的项如实blocked/partial，不以“已完成”掩盖。完成后Leader集成/验证，再由diting唯一一次增量复审；不自动循环修复。
