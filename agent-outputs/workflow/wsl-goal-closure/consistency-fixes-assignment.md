# 持久化与目录恢复一致性修正

task_tier=complex，主特色工匠闭环。仅实现本地代码与合成夹具回归，不执行网络/SSH/真实CLI，不访问真实用户配置或凭据。这不是续接中断的独立审核，不能输出审核PASS；修复完成后仍需单独验收。

## 工作区与依据

沿用现有 `.amagi-worktrees/wsl-goal-transaction`（已交G1净增量+G0+correction），起步核对无额外写入并保存当前完整前像。本轮输出仅相对**已交G1成果**的修正增量，不改任何旧patch、不重放输入、不commit、不回主树写源码。G0 owner修正、native lease修正分别在其它隔离树，不是你的写面。

必读主树本报告目录的 attack-contract.md、attack-core-implementation.md、attack-stage-review-repro_test.go、attack-stage-review-tests.log；前三个实现文件为当前源码。独立审核44954ee3中途错误终止，没有正式报告/结论；但已落盘本地合成夹具观测，以下均为需要源码核对的具体一致性问题，**不能把打印观测的TestStageAuditEvidence PASS理解成产品通过**。先检查夹具是否对应生产可达形态；有误的夹具要指出证据，不迎合错误fixture。

## 先建立真实失败断言，再修生产

1. `backup-path`：实际文件系统检查发现chmod目标 `.amagi-staging/op-.../before` 不存在，正常已有文件更新返回backup-unavailable。核对persistPreimageBackup实际创建路径与Linux/UNC转换，正向更新必须成功且正确前像/权限可验证。fake Chmod不应对不存在文件无条件成功；至少此回归的mode/chmod使用真实临时文件状态。
2. `legacy-unknown-and-hash`：当前文件内容与历史记录hash一致但fingerprint unknown/不一致，评估user-owned/risk=false。按契约区分指纹弱证据、内容正证据与无关用户文件；不能错误解除待确认，也不能全局误阻其它发行版。用真实迁移字段证明。
3. `v2-migration`：核对真实v2生产者写出的完整state（不是仅臆造top字段），已有受管记录迁移后不能遗忘成user-owned/never-managed。补真实v2形态fixture；v1/v2分别验证。
4. `wrong-root-reconcile`：shared与pi的targetKey相同，传pi TargetRef恢复shared intent被判not-applied并清未决，而文件仍在shared根。操作必须保留并验证实际落点/CLI或等价身份，不能拿当前调用者的UNCRoot解释其它单元的RelPath。旧/不完整intent不得猜成absent；各CLI/shared交叉调用及新Coordinator均需强断言。
5. `dir-cas-and-structure`：candidate staging后、move-aside前插入外部EXTERNAL文件，最终applied且该文件消失。目录提交必须在不可逆移动前对构建基准复验，搬移后验证不符保全外部版本；未授权变更不覆盖。
6. 目录Abort不能丢原空目录/嵌套结构（已观测confirmed但empty/nested丢失）。补完整树结构断言，不只SKILL.md字节。
7. `resource-abort`：resource目录更新后Compensate unavailable、旧内容未恢复且prev已不在。落实G1报告承认的compensateSkillDir partial，资源与skills统一真实持久前像/恢复生命周期；保留可靠目录前像至Release或补偿终结，不能先删再报unavailable。不得仅扩大内存snapshot限额；外部后改与大目录亦需保全。
8. `directory-restart`：已提交目录重建Coordinator被通用文件ReadFile当unreadable，无法收敛。按Kind区分文件/树验证和恢复；typed fingerprint、结构/类型都真实；不得把目录IO错误直接当not-applied清材料。
9. `corrupt-pending`：首轮RunUnits在pending目录有broken.json仍applied，末尾Health.Pending才变corrupt。健康门需先完成本次读取/扫描再决策；损坏材料保留、禁止新写，不靠上一轮缓存健康值。健康/fresh正确路径必须继续工作。
10. `plugin-cross-source-cleanup`：A源未决记录被B源成功清掉。只能结清本操作/有证据覆盖的同一source+anchor+目标责任，不能同目标全清；A的未确认副作用仍需可见。
11. `a2-final-save`：state save持续失败后返回applied + registration confirmed + PendingOperationFact=false。结果、observation、状态页与磁盘摘要必须同事实；检查finishA2Summary是否只改值副本，失败必须回传未确认事实，dirty与启动聚合不误计成功。不保存auth正文/hash，不回滚旧token。
12. `status-without-target`：新Coordinator仅有pending、尚无TargetRecord时GetStatus Checked=false且丢PendingIntents/StoreHealth。可以未检查，但不能不显示现有待确认事实/健康错误；read-only状态查询不能靠先执行一次写入才看见风险。

以上要求来自已接受的统一生命周期契约，不重新立项。锁序/取消/reconcile/release/legacy解释必须一致，避免再给单个分支加特殊绿灯。任何确实需方向裁决的问题给最小证据与选择，不静默改契约。

## 范围与验证

允许internal/wslsupply相关实现及正式测试，必要root供给门/登录判定与对应测试、前端待确认状态测试。禁止platform/launcher/headroom_facade.go/App租约字段写入（独立节点负责），禁止更改hermetic写面。公共接口如需增加必须说明调用影响与兼容，既有五CLI接线不断。无全仓gofmt、无wailsjs手改、不清用户/远端/其它worktree。

每条观测转成强期望的正式回归（合法正向+失败/恢复反向），不可仅Logf、泛化预期或skip；持续故障/新Coordinator/外部后改按矩阵执行。原目录补偿partial必须如实关闭或保留未完成，不再met标题掩盖。测试root/wslsupply/launcher/piplugin普通与race、vet、Windows编译（非运行）、若动前端则test/build。真正网络/Windows验证由Leader承担。原生TestR6_001_ClaudeExternalRun...竞态另有专人修，报告为已知集成问题不要改断言。真实embed产物可复用，禁止空占位。

交付主树报告目录：`consistency-fixes.delta.patch`（本轮净增量含新测试）与`consistency-fixes.md`。记录每项原失败/修后结果、覆盖路径/未覆盖、真实命令日志、接口变化、与原G1前像round-trip；日志仅本树.amagi/consistency-fixes。只修已有证据支持的本地一致性缺陷，不声称替代未完成的独立审核。
