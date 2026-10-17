# WSL × Windows 工程体验目标复核与修复

> **主上已授权继续，重新开启根因攻坚阶段。** 上一阶段仍FIX（PR01/02/03/05/07五项Major），历史结论保留于 `closure.md` / `phase-rereview.md`；当前补丁仍不得直接发布。新阶段先冻结失败/恢复契约，再实现与验收，不沿用实施者的全met声明。

## 目标与总验收

以 `16f707d^..58e7577` 为历史升级审计范围，最终目标：模型在 WSL 使用 Linux 工具，同时可靠操作 Windows 工程；不强迫迁移工程到 ext4，不自动更改用户全局 Git/WSL/安全配置，不提交或推送。

- V1：五 CLI 启动的 distro、cwd、env、provider/登录身份贯穿一致；失败可解释，不悄悄降级错目标。
- V2：Windows 工程路径（含空格/中文）、WSL 路径、Windows 工具互操作有明确支持边界；原生 Windows/macOS 不回归。
- V3：供给过程保护凭据、用户配置和本地编辑，跨 distro 隔离，部分失败可恢复；UI 不虚报成功。
- V4：已证实缺陷有修复和回归测试；阶段 diff 经独立对抗审核。
- V5：构建/测试/交叉编译与真实 Windows/WSL 入口验证分账，未覆盖不冒充通过。

## Phase

|阶段|内容与责任|依赖|产物|验收|状态|
|---|---|---|---|---|---|
|A|baize 运行链证据；diting 配置链缺陷审计（独立只读）|历史基线|runtime-audit.md / supply-audit.md|逐条代码证据、风险分级、可复现条件、覆盖声明|完成且接受，bdd5ad80 / 0223359a|
|B|Leader 收敛缺陷；fuxi 设计态评估|A（证据逐批回流）|direction.md|保留供给、冻结扩面、优先目标/身份/安全；报告已接受|方向完成|
|C1|luban：cwd/env桌面、remote、restart一致|B已证实运行链缺口|runtime-implementation.md|真实入口层回归、原生语义不回归|已集成且接受a715f28b；phase-review D2/D3 met|
|C2|luban：供给身份/私有提交/撤回/状态/取消|supply-audit SA01/02/06/07/08/09/15/16|core-implementation.md|合成反例转正式回归、安全失败保前像、逐目标隔离|有效改动已集成；61a7773c与补修9ce04747完整闭环声明均被拒，5 Major待主上裁定|
|C3|luban：选择贯通/安全过滤/资源消费/恢复/扫描|SA03/04/05/10/11/17/18|adapters-implementation.md|导入真实落地、拒绝秘密别名、普通md资源、目标性UI验证|已集成且接受7ea2f52d；取消/状态跨域遗留单列修复|
|C4|luban：插件/OpenCode冲突/取消|SA12/13/14/16及SA02插件侧|plugins-implementation.md|结果与filters/固定锚点一致，未知冲突不覆盖、目标授权隔离|已集成并接受acd47281补全；PR08重置由后续delta补齐且最终复审closed；提交取消余项另归PR07|
|C5|luban：Headroom网络门/内部proxy标记/非等价provider回退|runtime-audit D1/D4/D5/D6|routing-implementation.md|正确阻断、不误打官方endpoint、Headroom未生效不静默|已集成且接受54c5b10d；phase-review D1/D4/D5/D6 met|
|D|Leader 集成验证 + diting full 审核阶段 diff|C1-C5|phase-review.md / verification.md|真实入口可测项、回归矩阵，Major/Critical 清零|审核交付完成且接受27a1c590；实现Gate仍FIX（5 Major），停止自动循环；9ce04747完成声明拒绝|
|E|Leader 总验收与未覆盖边界交付|D|closure.md / verification.md|V1-V5 met/partial/missed 明示|阶段报告完成；产品目标未闭环，待主上决定下一阶段|

## 续行阶段（主上：继续，直到目标闭环）

|阶段|责任/依赖|产物/验收|状态|
|---|---|---|---|
|F 根因攻坚|fuxi显式attack；沿已接受方向，不重新立项|attack-contract.md：权限消费、前像保全、待确认提交、legacy归属、取消责任的统一不变量与失败矩阵|220b08a8完成且接受；仅指导，不等于代码已修|
|G0 平台/A2基础|luban；依赖F；platform+launcher独立写面|ctx真实runner、no-replace、私有manifest权限、A2全消费出口门；attack-platform.delta.patch|75639eb2已交付；Leader查出Windows DACL defaulted误作present，03b82df8补正并加8专项用例。两层已集成，主树全仓/race/Windows编译均exit0（g0-integrated-*.log）。Leader另按项目规范拆stat入口build tags，独立patch留档，目标race+Windows编译再次exit0；Windows真机已执行7个SD判据用例并通过，RealACLRoundTrip及通用EnsurePrivate因admin token新建对象owner为Administrators失败。a3205b58已交owner规范化净增量并集成，正在重测；校验判据不放宽。此前MoveNoReplace文件/目录/并发真机通过，UNC边界未验证；任务暂待接受|
|G1 供给事务集成|luban；F指导，按冻结API推进；验证依赖G0实产物|写前intent/验证前像、重启reconcile、legacy核验、取消恢复、App/plugin/A2接线；attack-core.delta.patch|9de1b4eb已交并集成；目标race、前端277测试/build、vet/Windows编译通过。全仓普通测试暴露既有StopSession释放竞态。独立审核中断但留下12组本地一致性观测，不能接受G1完整闭环声明；2a02a779在原隔离树集中核对并修正，见consistency-fixes-assignment.md|
|T 测试隔离|luban，与供给攻坚无共享写入|envcheck8+platform Codex1宿主污染根因/fixture修复，不skip/删断言|84f8a67d完成且接受，3文件纯测试隔离已集成；主树全仓go test首次exit0（hermetic-integrated-full-go.log）|
|H 集成与审核|Leader实跑+diting阶段审核|五项Major清零，普通/race/build/跨编译及独立反例验证|44954ee3因provider内容策略错误中断，无正式报告；已有repro/log保留，不计PASS、不原样重试、不换模型。已有观测转成本地修复任务，正式独立审核Gate仍未满足|
|T2 原生释放竞态|baize诊断；luban确定性回归与修复|stop-lease-diagnosis.md / stop-lease-fix.delta.patch；保持原同步终态断言、持久完成先于租约释放|abbca12e静态诊断完成；e5e8f4cb在wsl-goal-stop-lease隔离树修复，禁止sleep/skip掩盖|
|W Windows入口与真机|Leader逐条SSH；独立于本地执行环境|授权测试环境、实际CLI/权限/Windows工程证据|用户确认实际中文登录名及192.168.5.11，并允许临时兼容客户端。Paramiko4.0已装到临时venv，不改系统SSH。用户确认设备/IP/本地网络权限后重试：SSH握手PASS，banner=OpenSSH_for_Windows_9.5；用户已在管理员PowerShell独立核对ED25519指纹一致；按明确选择生成长期无口令专用key（~/.ssh/amagi_winpc_ed25519，600，不打印私钥），持久独立客户端~/.local/bin/winpc-ssh已安装并固定专用known_hosts；仅离线验证PASS。用户已在Windows管理员共享授权文件追加公钥并收紧ACL，返回PUBKEY_READY；Leader用长期客户端严格host-key+指定key登录，仅echo CONNECTION_OK，exit0（debug winpc-key-login）。长期SSH入口已打通，产品真机用例已开始：专用SFTP目录、Windows preflight、Ubuntu Running/v2；平台不替换移动通过、ACL owner失败待修后重测；五CLI/UNC/Windows工程用例尚未闭环。客户端quick初审4b8718d5两Major已直修；唯一增量复审7c6a52a0接受PASS（独立离线5/5）；不等于真实认证/Windows部署通过|

续行仍不授权commit/push、不自动修改用户全局安全配置或创建删除远端用户。SSH入口探测失败不阻断F/G本地任务；未取得真实入口前不声明真机闭环。

## 跨边界链路

1. UI/remote 启动 → App planner/executor → resolver → PTY → wsl.exe → CLI：跟踪 distro/cwd/env/provider，责任为运行链切片及集成批。
2. 设置/插件 UI → App supply facade → coordinator → distro filesystem → CLI consumer：跟踪 target/generation/source fingerprints/credentials，责任为供给链切片及集成批。
3. Windows 项目 → WSL 文件操作、GitAssist、PathPicker、Windows 工具调用：跟踪路径形态/执行宿主/错误提示，责任为运行链切片及集成批。
4. 代理/Headroom/订阅凭据 → WSL CLI：验证实际可达性与身份刷新边界，不把配置声明等同于连通。

## 并行、隔离与 Gate

- 证据采集独立并行；后续实现按发现划分，只在不存在共享写入时并行。
- 大范围实现使用 worktree，失败不回落共享目录；不复用或改动已有未跟踪 `agent-outputs/resume-relay-e0142362-72b3-44f0-98ae-986f42065051.md`。
- 禁止全仓 go fmt；只格式化本批变更文件；禁止手改 wailsjs，需变动时再生。
- 高风险集成完成后 diting full 审阶段 diff；必要时仅一次 Major/Critical 增量复审。
- 自治级别 interactive：正常调查修复自主推进；需要新架构/破坏性动作/真机访问授权时向用户明确说明。

## 环境与风险记录

- 宿主macOS；当前已建立用户授权的Windows SSH入口。测试只在本次专用目录/合成临时夹具，不读真实凭据、不改全局配置。历史报告中的真机PASS不能替代当前HEAD实证。
- 2026-10-17续行：首次Windows脚本因PowerShell拆分未加引号的-test.*参数未进入测试；改为字符串数组后实际执行，日志windows-platform-real-tests-v2.log。该轮ACL owner失败属产品缺陷；symlink用例因权限不足SKIP，不能算通过。
- 当前活跃修复：a3205b58(owner，已交待验)、e5e8f4cb(lease)、2a02a779(事务一致性)。主树不得直接发布，独立审核中断事实不能被实施自测代替。
- 起点工作树源码干净，仅上述已有未跟踪文件。
- 历史升级 169 files / 33726 insertions / 287 deletions，不能只审最后一批。
- 未找到沿 cwd 祖先的 projects-memory registry；采用明确项目内 workflow 归档，不捏造 registry。
- 初审 supply-audit.md：16 Major / 2 Minor，11个合成反例；报告已接受（0223359a），不等同于产品通过。运行链6项D发现已分派；O7 restart跟随宿主默认是既有契约观察，未经核定不盲改，保留待确认边界。
- C2→C1冻结内部调用接口 `runWSLSharedFilesAtLaunch(ctx context.Context, runner wslsupply.Runner, distro string) error`；仅shared小文件补漏/撤回、无授权无副本不阻断普通启动、受管敏感撤回未确认明确阻断。C2实现，C1在明确目标的desktop/remote/restart PTY前接入，集成后验证。
- C5与C1共享root app.go/launch_planner.go不同段，worktree隔离，C5仅网络/Headroom/provider失败分支，不改cwd/env。C5需要remote managed-models失败判定由C2修改既有verdict，不各造判定。
- C2/C3仅在app_wsl_supply.go不同段共享写面；C3/C4仅在app_wsl_supply_s4.go不同段共享写面，分别worktree、Leader逐hunk集成。C2保持core公开API兼容；新增共享启动gate接口要先落契约再由C1接线，避免猜测式并行。
- 初始全量Go失败，前端271测试/build、vet与Windows交叉编译通过。继承AMAGI_CODEBOX_AI_PROXY=1导致root四组WSL测试误触网络门，去marker后PASS，属于隔离问题，不能删除产品门禁来凑绿。详见verification.md。
