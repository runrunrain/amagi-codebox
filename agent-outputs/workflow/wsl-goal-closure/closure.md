# WSL × Windows 工程目标：阶段收口与未通过项

## 结论

**路线保留，目标尚未闭环；当前未提交补丁不得作为已验收版本发布。**

采用 WSL 的 Linux 工具操作 Windows/NTFS 工程是合理方向，没有证据要求重做架构或强迫工程迁入 ext4。现有供给机制可以保留，但凭据消费、安全提交、失败恢复和状态事实必须先收敛，不能继续以普通测试通过替代这些失败路径的验收。

本轮完成历史范围 `16f707d^..58e7577` 的运行/供给审计、方向裁定、C1–C5隔离实现与集成、一轮合并补修及唯一一次增量复审。最终独立结论仍为 **FIX：5 Major、0 Critical、0剩余Minor**。两次实现报告中的“全部闭合”声明未获接受；最新依据是 `phase-rereview.md`，不是早期实现摘要。

按既定规则停止自动修复循环，保留现场交主上决定。没有执行真实 Windows/WSL 故障注入，没有实际用户数据损失的证据；下列失败来自合成反例和明确代码路径。

## 五项剩余阻塞

|编号|尚未解决的问题|直接风险|
|---|---|---|
|PR01|v1旧副本风险被绑定到最后检查的distro，而非实际所属目标|A发布→检查B→升级后，A的旧密钥副本可能漏过启动门，B反而被误标风险|
|PR02|A2订阅auth回写Windows成功的两个分支仍不复验WSL副本权限|0644或权限未知的auth仍可能被选择消费|
|PR03|持续读回故障时备份字节未验证；提交后取消会丢失唯一前像|错误声称可恢复但备份损坏，或用户前像根本没有持久保全|
|PR05|状态落盘失败整体回滚内存，丢失“内容已提交、登记待确认”事实|首次发布后可能漏撤回；曾成功后的状态仍显示旧written|
|PR07|目录取消检查发生在旧目录移走之后，缺少恢复；A2/插件提交处ctx仍不完整|**本次补修引入目标目录消失的回归**；取消后仍可能发生凭据或配置写入|

精确行号、触发条件、合成证据、最小修复方向见 `phase-rereview.md` §3。普通测试与race绿灯不抵消这些反例。

## 已验证的进展

- 五CLI的cwd/env主链、会话最终目录投影、Headroom NAT门、内部代理标记清理、Pi/OMP错误时不退回非等价endpoint、Codex压缩未生效的明确提示：源码/定向回归已改善并被初审确认。
- 全路径选择键、Codex profile白名单、秘密别名排除、Claude Markdown资源、停止跟随后接管、扫描与轮询生命周期已修。
- 最终复审关闭：PR04目标分区操作、PR06状态深复制/生产race、PR08过滤重置、PR09测试并发生命周期、PR10危险取证脚本。
- 原`verify-staging-window.ps1`已撤回；`.UNSAFE-RETIRED.txt`仅是禁止执行的审计证据，不提供未经真机验证的替代自动脚本。

以上不等同于五CLI最新版本真实消费、Windows互操作或UE工程构建已验证。

## 总验收 V1–V5

|验收|结论|说明|
|---|---|---|
|V1 目标/cwd/env/模型身份一致|partial|主链L2回归通过；A2权限/共享敏感门仍有Major，真实模型响应未验；O7 restart默认漂移保留为既有语义观察|
|V2 Windows工程与工具互操作|partial|修复路径误回退且保持原生语义；中文/空格NTFS工程、Windows工具/UE真实构建未在目标平台运行|
|V3 凭据、用户配置、跨目标和失败恢复|partial，安全门未通过|五项Major阻断闭环，尤其PR03/07的数据保全|
|V4 回归与独立阶段审核|partial|正式回归、race和两次独立审核已执行；Major未清零，不能验收实现完成|
|V5 证据分账与诚实边界|met（证据口径）|构建/模拟/交叉编译/真机分账完成；**真机执行本身仍未完成**|

## 最终验证账

- 主树目标四包普通测试和`-race`：均exit 0。`final-target-tests.log` / `final-target-race.log`。
- 前端：31 files / 277 tests + build通过。本次最后delta无前端变化，沿用`integrated-frontend-*`。
- 全仓`go vet`：exit 0；Windows全仓测试交叉编译：exit 0（`-exec /usr/bin/true`，不执行Windows代码）。
- 主树全仓Go测试：exit 1，仍有与基线同名的envcheck 8项及platform Codex路径发现1项；`final-go-all.log`。不采纳实施worktree“仅1项”覆盖主树结果。
- Windows CI仅新增了确定性回归执行配置，未提交、未触发CI。Windows/WSL真机未执行；清单见`windows-validation.md`。
- 最终独立复审：FIX。反例脚本完成取证的exit 0不是产品测试PASS。

## 现场与后续

- 无commit/push；未改用户全局Git/WSL/安全设置，未手改wailsjs；原有resume-relay文件未触碰。
- 当前主树保留未提交的集成改动，包括上述已知未通过路径。**不要直接合并、发布或把这批补丁用于真实凭据/重要工程验收。**
- 隔离worktree均保留，不自动删除脏树。补丁档案：C1–C5对应patch、`integrated-stage.patch`、`review-fixes.delta.patch`。
- 若主上决定继续，下一阶段仅处理这五项：先固定失败矩阵与用户前像/待确认提交事实的责任，再实现；不扩MCP、不扩大安装面、不重做架构。代码安全门通过后，再获取明确授权的Windows/WSL测试环境执行真实链路。
- 若决定撤销最后增量，必须先检查反向delta是否能精确应用，人工确认后处理；禁止用`git checkout -- <files>`笼统回退，那会一并丢掉同文件中此前的有效集成改动。本轮未执行回滚。

详细证据索引：`workflow.md`、`verification.md`、`direction.md`、`phase-review.md`、`phase-rereview.md`。
