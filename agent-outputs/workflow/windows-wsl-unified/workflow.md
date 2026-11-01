# Windows / WSL 统一管理 workflow

## 目标与基线
用户确认：Windows 是统一配置入口；选择 WSL 后应用在 Linux 中运行，无需用户重新配置，允许后台兼容副本。通过 CodeBox 安装或升级时同步安装或升级 Linux 版应用。范围为现有五种 CLI；复用 wslsupply、wslsetup、envcheck，不重写配置基建。不改用户真实配置、不执行真实 CLI 安装、不自行提交。基线仅有未跟踪 agent-outputs/baize/ 与 amagi-task/ 两个历史任务目录，保护原有内容。

总验收：
- R1：用户选定的 WSL 目标使用 Windows 配置源，兼容转换自动发生，不要求重复录入。
- R2：应用内安装/升级链路联动目标发行版 Linux CLI，覆盖已有五种 CLI 的受支持入口。
- R3：两侧成功/失败分别可观察，部分失败不能伪报全成功；未启用/不可用 WSL 不破坏原生流程。
- R4：保留凭据同意、权限、路径转换及非托管配置保护边界；不将整树 HOME 共享。
- R5：自动化回归、跨平台编译及用户入口链路验证，无法运行的 Windows 实机检查明确列为未覆盖。

## Phase 与节点
| ID | 目标 / Agent | 依赖 | 输入 / 输出 | 验收 | 状态 |
|---|---|---|---|---|---|
| P1 | 确定现状与实现缺口 / baize | 无 | 当前源码 → research.md | 文件符号、五CLI矩阵、入口链路与最小切片 | accepted: 2d403755 |
| P2-A | wslsetup 五CLI安装/升级原语 / luban | P1 | research.md → internal/wslsetup + setup-implementation.md | 五CLI、升级不短路、失败真实、跨平台stub、可移植回归 | accepted: 5f7aa264；已集成；race+Windows编译+全Go通过 |
| P2-D | 自动供给与双侧安装结果共享契约 / fuxi 设计态 | P1；与P2-A独立 | research.md → direction.md | R1/R3/R4、安全授权与手改保护、真实入口验证建议 | accepted: ef7615bb |
| P2-B | 统一配置原语与App/前端纵向切片 / luban | P2-D | direction.md → code + config-implementation.md | R1/R4；启用→启动/推送，手改与换代保护 | accepted: 5982ce15；已集成 |
| P2-C | 双侧安装升级全入口与结果展示 / luban | P2-D；消费P2-A UpdateTool契约 | direction.md + P2-A → code + install-implementation.md | R2/R3；sync/async/fix/WithMethod、warning | accepted: c219f32a；已集成 |
| P3 | 集成验证 / Leader | P2 | 合并代码 → verification.md | R5；用户入口与失败路径 | passed（Windows实机未覆盖）；Go全量/race/vet/Win build、frontend build/413测试、24 browser fixture |
| P4 | 高风险阶段 full 审核 / diting | P3 | 阶段 diff + 验证 → review.md | 凭据、配置覆盖、安装副作用、异步状态 | accepted: ff8d7c82；FIX（3 Major/1 Minor） |
| P4-F | F1/F2/F3合并修复 / luban | P4 | review.md → review-fix.md + code/tests | 后续重扫、会话暂停后恢复、部分授权自愈、直接换代隔离 | accepted: 468bcb29；已合入；Leader全量/竞态/vet/Windows build通过 |
| P4-L | F4跨卡片刷新与用户文档 / Leader | P4；独立于P4-F | review.md → 前端事件/prop、browser回归 | WSL终态后兄弟卡片刷新，不新增轮询 | passed；frontend build/413单测、24 browser（含跨卡片刷新） |
| P4-R | 一次Major修复定向复审 / diting | P4-F/P4-L集成验证 | fix diff + evidence → review.md增量复审节 | F1–F4关闭与保护边界未回归 | accepted: 0590d3f2；PASS；限定范围C/Major/Minor均0 |

## 跨边界链路
1. EnvCheckSettings安装/升级 → App.StartInstallToolAsync/StartUpdateToolAsync（另含sync与Claude指定通道）→ envcheck.attachWSLSync → App.syncCLIToWSL → wslsetup.InstallTool/UpdateTool → InstallResult.WSL → operationCompletionBanner（部分失败warning）。F4终态emit经EnvCheckView触发WSLCLISettings单次reload；责任P2-C+Leader，入口证据install-tests.log、browser-install-f4.log。
2. EnableWSLUnifiedSupply/保存推送/启动lazy → 协调器SetTargetUnified/AutoConsentIfUnified/显式键物化 → RunUnits身份重核与路径转换 → WSL受管文件。保留detached/手改/同名项与敏感授权；资源后台扫描结果经回调调度。责任P2-B/P4-F+Leader；入口证据final-race.log，F1–F3恢复时序待修复验证。实际Linux CLI消费尚无真机证据。

## 并行与隔离
P1 输出是实现的真实依赖。实现任务预计跨多文件且超十分钟，使用 worktree dirty-snapshot；无共享写面、无契约依赖的切片同时派发。生成的 Wails bindings 只能使用生成器，不手改。无需新架构时复用现有方向，不例行调用 fuxi；遇到重大公共契约取舍再咨询。

## 自治级别与风险
interactive（已授权实现，明确需求下自主推进）。只针对用户选定/现有有效目标发行版，不默认修改全部已安装发行版；确切目标语义根据现有设置核验后记录。macOS 主机不能据模拟测试宣称 Windows 实机通过。安装或升级真实 CLI 属用户设备副作用，本次只通过测试接缝验证。

## 修订日志
- 独立审核发现F1资源缓存只初扫、F2活动会话挡住后无恢复调度、F3部分初始化不可自愈；采纳并合并修复。F4由Leader直接补兄弟组件事件/refreshRevision单次刷新，与后台写面独立。原selected派发因累积自动完成消息超过上下文上限而零副作用拒绝，改用自足任务书isolated派发，未扩大权限。
- 初始：建立活计划；P1 定向调查已启动。实现节点依实际证据细分，不预设缺口。
- P1接受：安装联动与升级确实缺失；Claude/Codex手动S4导入不算R1达成。已并行派发底层wslsetup实现与自动化方向设计。历史OMP安装面排除被本次五CLI统一范围覆盖，不新增确认阻断。不同意“只日志告警即可算同步结果可见”的建议。
- P2-A接受：底层包可移植业务流与runner接缝，五CLI+UpdateTool已集成。Leader补默认探针30s预算（避免新增自动联动永久挂起）；真实长步骤仍按npm/Node预算。go test -race ./internal/wslsetup、Windows测试编译、go test ./...已通过，见setup-tests.log与setup-full-go.log；harness自动检查unavailable已用这些真实证据替代豁免，不把豁免当测试通过。
- P2-D接受并修订：一次明确授权后自动跟随；不持久化仅含通配星号的选择集（旧版会误判来源删除），优先每次对账物化明确键。敏感授权与detached保护不变。安装门控为有效WSL且该CLI shell=wsl或已经在WSL安装；两侧结果放既有OperationState.Result.WSL，无新轮询。并行派发P2-B/P2-C，明确WSLCLISettings整个文件归P2-B，App.go改动分Startup与constructor两个独立段。Leader只补api/wslsetup.ts wrapper并最终统一生成绑定。

## 覆盖范围说明
自动供给仅承诺现有安全适配器支持的Pi设置、Claude/Codex白名单键与可移植技能/资源、共享清单以及已有各CLI provider启动配置。OpenCode全局附加层之外、OMP models.yml之外、Claude/Codex订阅OAuth和全引擎插件迁移仍未覆盖；五CLI安装升级覆盖不等于任意配置全量互通。停用不撤回既有授权。
