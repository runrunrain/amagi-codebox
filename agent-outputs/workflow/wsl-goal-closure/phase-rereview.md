# WSL 目标闭环：唯一一次修复增量 full 复审

VERDICT: FIX

**剩余 5 项 Major（PR01/02/03/05/07），无 Critical、无剩余 Minor；PR04/06/08/09/10 closed。** 普通测试与 race 通过属实，但不能关闭下述可失败路径。本轮结果交 Leader 收口，**不自动开启下一修复循环**。本结论只针对 PR01–10 修复增量及其直接回归，不重新审核历史 169 文件，不是 Windows/WSL 发布认证。

## 1. 基线、输入与取证口径

完整读取本目录的 `phase-review.md`（初审 FIX：8 Major/2 Minor）、`review-fix-implementation.md`、`review-fixes.delta.patch`（2825 行，分段读完）。核对主树实际生产实现与新增/调整测试；补读 `verification.md`、`windows-validation.md`、退役脚本文本、`core-implementation.md` 的覆盖声明。初审基线 HEAD 为 `58e7577`；本轮比较基线是已集成 C1–C5、尚未加入 review-fixes.delta 的初审态，而非把全部 HEAD diff 重新当作修复增量。

- **F**：本轮通过公开 Coordinator API、合成目录/秘密和故障接缝执行的复现；不代表 Windows 自然故障时序。
- **S**：生产调用链静态证实；未假称运行真实 CLI/权限探针。
- 本轮只写本报告与 `phase-rereview-repro.go/.log`、`phase-rereview-target-race.log`；未改生产或正式测试、未 commit、未运行 Windows 脚本/AMAGI_WSL_E2E、未读取真实密钥。

## 2. PR01–10 逐号结论

| 原编号 | 状态 | 已关闭部分 / 最小剩余项 |
|---|---|---|
| PR01 Major | **partial** | Blocked/runErr/Failed 统一检查残留风险方向正确，正常同目标 legacy 与 source/identity failure 回归通过；但 v1 风险按最后检查的顶层 Distro 盲绑，可能把 A 的风险归 B，A 仍放行，见 §3。 |
| PR02 Major | **partial** | coordinator 敏感 Fresh、launcher 同字节 unknown、A2 多种失败 fallback 已收紧复验；A2 反向/并列回写**成功**的被消费旧副本仍未验权限。 |
| PR03 Major | **partial** | 递归恢复与未知目标无条件删除已消除；持续失配时备份字节不复验，取消时直接丢弃唯一前像，未满足实际可恢复。 |
| PR04 Major | **closed** | Drop 各分支经明确 distro→generation；Selection 经 entryFor 指定代；A→B→keep A/选择 A 测试通过，歧义/未知明确拒绝而非改别的目标。不是对所有历史 mutate 的重新认证。 |
| PR05 Major | **partial** | 提交后登记失败已返回 UnitFailed + Undo；但内存整体回滚丢掉首次发布事实，且曾成功后的状态继续显示旧 written；与 shared gate 组合仍漏撤回。 |
| PR06 Major | **closed** | 逐字段核对深复制：Units/Subs/SelectedKeys、Targets/InspectedAt、Plugins 四类过滤 nil/[]、Pending*、LegacySensitiveRisk 均隔离。原产品 race 与定向并发读写复跑无 race。 |
| PR07 Major | **partial；引入直接回归** | 已取消 takeover、等锁后、文件/目录入口与敏感 rename 前检查有效；新目录取消检查放在旧目录已挪走之后，取消留下目标缺失；A2 内部及插件后继提交生命周期仍不完整。 |
| PR08 Minor | **closed** | Filters 指针区分 nil 未提供与非 nil 全 nil 显式全加载；App 带完整快照；重置、nil 兼容、幂等正式回归通过。没有真实 Pi 加载证据。 |
| PR09 Minor | **closed** | fakeProbes 读写加锁；root 后台 push 由 WG 跟踪且 cleanup 先等待再还原全局接缝。原并发/后台 fixture 定向 -race 通过；Leader 四包全量 -race 也通过。 |
| PR10 Major | **closed（撤回交付物危险性）** | `.ps1` 已不存在；`.UNSAFE-RETIRED.txt` 首部明确禁止复制/执行且仅保留原始审计证据；Windows 清单不再承诺该脚本可执行，要求授权既有隔离用户、唯一根、真实 writer、完整观察生命周期。core 报告加后续覆盖声明，fix 报告回滚改为先反向 delta check，未执行回滚。**真实窗口验证仍 pending，不能写成通过。** |

## 3. 剩余 findings（沿用原编号）

### PR01｜Major｜legacy 风险把“最后检查目标”误作旧副本所属目标

- **定位**：`internal/wslsupply/state.go:251–273`；`coordinator.go:1090–1103`；`app_wsl_supply.go:251–266`。
- **触发条件**：v1 在 A 发布视觉副本，之后只检查 B（无需在 B 发布）。v1 `Inspect` 会更新顶层 Distro/Home/Generation，但不更新 A 的视觉单元登记。升级时顶层是 B、视觉 entry 仍为 A 的旧 generation。
- **实际影响**：迁移无视 entry 与顶层 generation 是否一致，统一登记 `LegacySensitiveRisk[B]`；A 的真实旧副本风险消失，B 反而继承并非其所属的风险。A 的 awaiting-consent 被 App gate 当作无历史副本放行。仅有“同一个顶层 distro”的测试不能证明归属。另，迁移条件只看 Consented/hash，也会为已 withdrawn 或 detached 的旧记录重建风险（静态）。
- **证据 F+S**：`phase-rereview-repro.log`：`LEGACY top_B_entry_matches=false pending_A=false pending_B=true`，随后 `LEGACY_MISMATCH_A outcome=blocked detail=awaiting-consent old_copy_present=true pending_A=false`。实际 App 放行分支静态证实，未启动 PTY。`git show 58e7577:internal/wslsupply/coordinator.go` 的 `:176–191` 证实 v1 检查会单独覆盖顶层字段，因此不是凭空拼造不可达旧状态。
- **最小修复方向**：不可把未证实归属的旧 entry 自动绑定到最后 distro；保留可追溯的未归属待确认事实，结合旧 generation/Home 等可用线索与显式目标核验处理，不删除未知归属文件、不自动继承授权，也不简单给所有不相关目标同一风险。确认 withdrawn/detached 终态不重新制造旧副本风险。补 A 发布→检查 B→升级→启动 A/B 的正式回归。
- **归因**：PR01 新增 legacy 标记的归属缺陷；仍在原审核承诺内。

### PR02｜Major｜A2 回写成功路径仍可消费 0644/权限未知 auth

- **定位**：`internal/launcher/wsl_pi_login_sync.go:185–204`；`internal/wslsupply/a2.go:66–73`。
- **触发条件**：两侧 auth 字节不同，distro 更新或在 mtime 容差内；distro auth 为 0644 或权限探测不可用；`writeWindowsAuthAtomic` 成功。
- **实际影响**：只有 writeWindowsAuthAtomic **失败**才调用新 retighten helper；成功直接返回 DistroNewer/TieKept，classifyA2 记为 Applied，实际消费的 distro auth 未收紧/复验。Windows 回写成功不证明 WSL 副本权限安全。
- **证据 S**：上述两个成功 return 之前均无 StatMode/retighten；初始不同字节分支也没有统一权限门。同字节 Fresh 和失败 fallback 的正式测试确实通过，但没有覆盖这个成功分支。本机无法用公开 launcher 接缝独立跑真实 WSL 探针，故本项为静态路径证实，不声称动态权限复现。
- **最小修复方向**：把所有选择消费现存 distro auth 的出口汇合到同一个权限复验门（含成功 DistroNewer/TieKept），不可证则 NoAuth fail-closed；补成功回写 + 0644/unknown 的正式回归，不只测试回写失败。
- **归因**：PR02 对“所有被消费的既有敏感副本”修复遗漏；非另开安全审计。

### PR03｜Major｜有界恢复仍不能保证用户前像实际可恢复

- **定位**：`internal/wslsupply/coordinator.go:1673–1686,1693–1734`；测试 `review_fixes_test.go` 的 stagingSabotage 与 RecoveryCanceledSkipsBackupWrites。
- **触发条件 A**：持续故障使每次 staging 字节均损坏，包括恢复备份；mode 探测仍成功。
- **实际影响 A**：备份只验证 mode，不验证内容；rename 后直接报“user preimage preserved … for manual recovery”。实际备份并非前像，唯一内存前像随错误返回丢失。
- **触发条件 B**：敏感提交已经替换目标、读回失配/目标后改，此时 ctx 取消。
- **实际影响 B**：1677 直接返回且没有 Undo/其它持有前像的句柄；目标已不含前像，备份也不产生，原用户内容不再可恢复。取消不应取消对已发生写入的保全责任；错误文案如实说 canceled 也不能代替恢复能力。
- **证据 F**：脚本持续污染提交及备份的临时字节，真实执行 2 次 staging 后得到 `backups=1 valid_preimage=false`，错误却宣称 preserved；精确在目标已变为失配字节后取消，得到 `attempts=1 backups=0 valid_preimage=false`。脚本只用临时目录/合成内容；取消接缝不代表真实 UNC 调度。正式测试所谓持续失配实际上 `writes==1` 才污染，故第二次备份健康，掩盖持续故障；取消测试甚至明确断言“不写备份”，未断言用户前像仍有持久保全处。
- **最小修复方向**：在不可逆写入前保全可验证前像，或确保失败/取消返回后前像仍有可恢复持有者；恢复备份必须验证字节，持续失配时保留可靠前像/明确未确认而不是虚报可恢复。维持有界、无递归、不覆写后继用户编辑，不通过取消后无条件覆盖目标来补救。
- **归因**：PR03 替代恢复实现的缺口；无限递归已关闭，不再重复报告旧递归。

### PR05｜Major｜登记失败回滚丢掉已提交事实，首次漏撤回，后续仍展示旧成功

- **定位**：`internal/wslsupply/state.go:400–408`；`coordinator.go:662–668,1073–1110`；`app_wsl_supply.go:264–266`；`shared_files.go:85–93`。
- **触发条件**：内容提交成功后清单保存失败，分别覆盖首次发布与已有成功后的更新。
- **实际影响**：
  1. 首次发布：新 UnitFailed/Undo 只存在于单次返回；内存回滚到 consented、无 LastWrittenHash，`SensitiveCopyPending=false`。真实 shared gate 对该 Failed 因“无副本历史”返回 nil，未调用补偿；之后宿主关闭导出，adapter 认为从未发布，返回 Fresh，密钥仍在。
  2. 曾成功后失败：内存回到旧 written，GetStatus 没有本次失败或 state-unpersisted 待确认信息，UI 继续显示旧成功，违背不虚报要求。旧 hash 未必等于新内容，下次归属也已失配。
- **证据 F+S**：`STATE_FAIL prior=false … target_present=true pending=false displayed="consented" undo=true`；存储恢复可写后关闭合成源：`STATE_FAIL_THEN_WITHDRAW outcome=fresh detail=no-change secret_remains=true pending=false`；`prior=true … displayed="written"`。首次 App gate 放行由上述当前生产分支静态证实。脚本故障注入为临时清单路径替换目录，未触碰用户配置。
- **最小修复方向**：区分“持久登记已确认”和“内容已提交但登记待确认”的内存/状态事实，保存失败不能丢掉后者；风险门必须看到本次提交事实，首次错误不得被无历史判定放行。旧 durable 基准可以保留，但公开状态不得仍冒充最新同步成功；重试/撤回/重启恢复需有明确保全策略，不靠删除混合配置凑一致。
- **归因**：PR05 新 mutate 回滚直接引入的事实丢失，与 PR01 gate 的直接组合回归。

### PR07｜Major｜目录取消检查在移走旧目录之后，取消反而使有效目标消失

- **定位**：`internal/wslsupply/skills_submit.go:279–306`；A2 后继写 `coordinator.go:398–415`、`a2.go:17–33`、`internal/launcher/wsl_pi_login_sync.go:112–204`；插件后继提交 `plugins.go:923–990`。
- **触发条件**：技能/资源目录原已存在；submitDirUnit 将 targetDir rename 到 prevDir 后，请求在新增 ctx 检查点取消。
- **实际影响**：取消分支清 staging 后只 `os.Remove(prevDir)`，没有把旧目录 rename 回目标。非空旧目录残留在隐藏名，原目标路径消失且错误没有指明备份位置；空旧目录甚至被 Remove 删除。这是修复新增的直接回归，不是“只拒绝新写”。技能/资源消费者失去有效目录。
- **证据 F**：公开 PreviewUnits→TakeOverUnit，合成 context 在“目标消失且 prevDir 出现”这一状态触发取消，得到 `DIR_CANCEL outcome=failed target_present=false hidden_preimages=1`；错误仅 `commit skill dir canceled before swap`。这是确定性取消边界注入，不声称 macOS 自然并发/Windows 时序复现。
- **最小修复方向**：第一次挪走旧目录之前检查；已挪走后发生取消必须执行有界补偿/恢复旧目录，并如实报告恢复失败和备份位置，不能删除前像占位冒充现状保留。
- **同编号生命周期余项 S**：A2 Sync 完全不接 ctx，其内部存在身份/mtime/权限外部探测与跨边界 auth 写入，不是可假设不可取消的“快速单文件操作”；取消发生在内部探测后仍会前向/反向写凭据。插件 applyDistroFilters 只在入口检查一次，后续读取、解析、临时写及 rename 前未再检查，内部期间取消仍会提交 settings。前者仍可在已取消的用户请求后传播凭据，后者仍会改启用面；应把 ctx 延伸到实际提交边界，而非仅改入口。此处静态证实，未运行真实凭据/安装，不据此宣称 Linux 子进程已回收。
- **归因**：目录取消分支为本 delta 引入回归；A2 与插件为原 PR07 生命周期承诺未完全关闭。

## 4. 验证实际结果与可信度

所有本轮命令 cwd：`/Users/run.mao/workpace/amagi-codebox`。没有复跑整仓，也没有通过安装、修改环境或跳过测试取得绿灯。

| 执行来源 | 命令/核对 | 真实结果与证据 |
|---|---|---|
| 本轮 | `go run ./agent-outputs/workflow/wsl-goal-closure/phase-rereview-repro.go` | 最终 exit 0，产生上列反例；`phase-rereview-repro.log`。首次编译 exit 1：取证脚本误用不存在的 PreviewUnit；查实际 API 改为 PreviewUnits 后运行。未修改产品代码。exit 0 指脚本完成取证，不是产品验收通过。 |
| 本轮 | `go test -race . ./internal/wslsupply ./internal/launcher ./internal/piplugin -run 'Test(ReviewFix\|WSLSharedGateReviewFix\|CompensateApplySerialized\|SubmitFilePrimitiveTrueConcurrency\|SaveAmagiConfigMarksPiConfigDirty\|EnableWSLPiConfigSupply)' -count=1`（表格中的 `\|` 表示实际命令的裸竖线） | exit 0，root/wslsupply/launcher 命中测试通过；piplugin **no tests to run**。见 `phase-rereview-target-race.log`。此定向通过没有覆盖上述所有反例。 |
| 本轮 | `git diff --check` | exit 0，无输出；仅格式检查。 |
| Leader，本轮读取日志 | `go test . ./internal/wslsupply ./internal/launcher ./internal/piplugin -count=1` 及同目标 `-race` | 均 exit 0，四包 ok；`final-target-tests.log`、`final-target-race.log`。退出码由任务 steer/verification 记录，日志结果已独立读取。 |
| Leader，本轮读取日志 | `go vet ./...` | exit 0 由 steer/verification 记录；`final-vet.log` 为空，空日志本身不证明退出码。 |
| Leader，本轮读取日志 | `GOOS=windows GOARCH=amd64 go test ./... -run '^$' -exec /usr/bin/true` | exit 0，`final-windows-compile.log`；**只编译，未执行 Windows 测试**。 |
| Leader，本轮核对失败名 | `env -u AMAGI_CODEBOX_AI_PROXY go test ./... -count=1` | exit 1；`final-go-all.log` 为 envcheck 8 + platform Codex 1，共基线同名 9 项。主树结果优先于实施 worktree 报告“仅 1 项”，不改称全仓绿。 |
| 上游沿用 | frontend 31 files/277 tests + build | verification/Leader 提供，本 delta 无前端变更，本轮未重跑，不以此证明 UI/视觉/CLI 消费。 |

取证 Go 文件带 `//go:build ignore`，显式 go run 单文件；未写正式项目测试，不给 `go test ./...` 新增 main 冲突。故障注入用可信 mode **fixture**来隔离内容/状态故障，不当作 Linux 权限证据。

## 5. full 增量覆盖、盲区与 Windows pending

| 维度 | 本轮覆盖与保留边界 |
|---|---|
| 正确性/目标隔离/迁移 | 逐个 PR 的入口与消费者、legacy 归属、A/B mutation、显式 filters 期望；PR01/04/08 见结论。未重新逐行审历史配置转换器。 |
| 安全/归属/数据保全 | 所有 delta 敏感 Fresh/fallback、恢复/取消、首次与后续 state-save-failed；失败反例见 PR02/03/05/07。未读取真实凭据。 |
| 一致性/并发/生命周期 | 状态完整深复制、原生产 race、后台 fixture 等待；PR06/09 关闭，但 race 无告警不证明取消/事务语义正确。 |
| 兼容/性能/维护性 | nil/[]/全量过滤三态与旧调用兼容；深复制全字段，无新扫描/安装入口。未做 UNC 性能测量，没有额外按风格报问题。 |
| 交付物/验证有效性 | PR10 撤回成立；正式恢复测试仅单发 sabotage、首次状态失败覆盖不足及取消断言弱点已明确，不按实施报告 met 放行。 |

**Windows/WSL 真机仍 pending**：真实 UNC rename/9P mode/ACL 与第二个已授权隔离用户完整窗口观测；持续失配与取消的真实前像恢复；旧 v1/换用户/换代与残留副本核验；五 CLI desktop/remote/restart 的实际配置消费与模型响应；真实 Pi filters/Claude 资源/Codex/OpenCode schema 与握手；Linux 子进程取消后的存活与后继写；真实工程互操作/授权构建；设置面板操作与视觉。没有 Windows 主机，不将 fixture、交叉编译或“目录最终 mode 正确”写成这些项目通过。O7 restart 语义仍为初审遗留观察，不在本增量重开。

## 6. 收口与现场

最小剩余修复面仅为 **PR01 风险归属、PR02 两条成功消费出口、PR03 持续故障/取消前像保全、PR05 待确认提交事实、PR07 目录补偿与真实后继写 ctx**。本轮不补 patch、不编写项目正式测试、不自动复审循环；是否继续修复或带未闭合项收束，由 Leader 决定，不隐藏 FIX。

新增产物均在本目录；回滚本轮仅可由 Leader 按需移除本轮新增的 `phase-rereview.md`、`phase-rereview-repro.go/.log`、`phase-rereview-target-race.log`，不恢复/清理主树源码或其它人的材料。主树既有未归因改动全保留。
