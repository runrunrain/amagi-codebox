# Workflow：amagi-codebox WSL 会话摩擦治理

- 自治级别：interactive（批次 Gate 呈报主上，批内自主）
- 建立时间：2026-10-16；Owner：天城（Leader）
- 证据基线：`agent-outputs/wsl-session-friction/evidence-report.md`（一轮 17 条 A1-C5）、`agent-outputs/wsl-session-friction/toolcall-evidence.md`（二轮 14 条 T1-T14）

## 目标与总验收

消除 WSL 会话已裁决摩擦，按批次推送变更（主上已授权每批次 commit+push）。

| # | 总验收 | 判据 |
|---|--------|------|
| V1 | opencode WSL 会话开箱可用 | T4 修复后 WSL 会话内 opencode 能解析 amagi provider（代码级验证 + 配置落 distro 证据） |
| V2 | mirrored 网络模式下代理分流/Headroom/系统代理对 WSL 会话可用 | A1 闸门按 `.wslconfig networkingMode` 放行；NAT 维持拒绝但文案引导 |
| V3 | WSL 会话可用 Pi 订阅登录模型 | A2 闸门解除 + 凭据同步机制 + 契约文档更新 |
| V4 | 配置供给体系（五 CLI 对称播种/重同步/MCP 形态转换/~/.agents） | fuxi 设计经主上审定后落地 |
| V5 | 批次一杂项 | T12/T13/T6(B3) 各自改动+测试绿 |
| V6 | 每批次 | 相关包 go test 全绿 + 构建（go build / 前端按需）+ git 提交推送 |

## 主上裁决记录

1. ①组（A1/B1/B2 回环跨界）→ 借 WSL2 mirrored 一并解决（2026-10-16）
2. A2 → 要支持 WSL 用订阅模型（推翻「拒绝而非复制」契约，pi-login-providers.md:48 随批次二更新）
3. B7（omp 进 wslsetup 安装面）→ 关闭不修
4. 批次三走 fuxi 设计前置
5. C5（报错文案）并入批次二（随闸门解除/mirrored 文案一起重写，避免批次一写完批次二又删）

## Phase 表

| Phase | 目标 | 切片 | Agent | 依赖 | 输入 artifact | 输出 artifact | 验收 | 状态 |
|-------|------|------|-------|------|---------------|---------------|------|------|
| P1 快赢止血 | T4+T12+T13+T6 | 单任务纵向切片（共享 wsl.go/wsl_agent_config.go，不拆） | luban | git 主树干净 | 两轮证据报告 | 实现+测试+变更说明 | go build+相关包 test 绿；T4 有配置落 distro 或转发面证据 | **完成**（commit 16f707d 已推送；验收 2026-10-16，Leader 直修一处注释引号破坏） |
| P2 已裁决项 | mirrored 放行 + A2 订阅 + B4 小修 | P2-T1 mirrored（aiproxy/platform/envcheck/前端）；P2-T2 A2（app.go 闸门/wslsetup 同步/文档）；B4 并入（主上无异议） | luban ×2 | P1 完成推送 ✓ | 两轮证据 | 实现+测试 | P2-T1 mirrored 放行逻辑+测试；P2-T2 闸门解除+同步+契约文档；B4 Linux 形态 workDir 跳过 ConPtyWorkDir | **完成**（两轮 qreview：FIX 六条修复轮 + PASS_WITH_MINOR q2-1 Leader 直修；待提交） |
| P3 体系工程 | 配置供给体系设计→落地 | P3-T1 fuxi 设计；P3-T2..n 按设计拆 | fuxi→luban | P2 完成推送；fuxi 设计经主上审定 | 两轮证据 T1/T2/T3/T5/T7/T8 | 设计文档→实现 | 设计文档主上审定；落地按设计验收 | 待P2 |
| P4 实证补全 | B4 实证→T11 适配；B5 distro 可选；advisory UI 通道 | P4-T1 B4 真机实证（Leader/luban）；P4-T2 B5；P4-T3 advisory UI | luban | P4-T1 影响 T11 范围；P4-T2/T3 独立可提前 | 证据报告 B4/T11/B5/C1-C3 | 实证记录+实现 | B4 定谳记录；B5 可选+缓存刷新；advisory 通道有 UI 证据 | 待启动（P4-T2/T3 可与 P2 并行） |

## 跨边界链路清单

1. **环境变量跨界链**（Windows env → WSLENV → distro 内 CLI）：`appendWSLENVForwarding`/`wslENVForwardPrefixes`（platform/wsl.go）+ 剥离函数（launcher/wsl_agent_config.go）+ 各 *_config.go 注入点。P1 全部改动集中此链，单任务避免冲突；P2-T1（proxy 精确键）同链但区域不同，靠串行批次隔离。
2. **mirrored 检测链**（新增）：`.wslconfig` 读取（platform 新函数）→ aiproxy/environment.go 放行判定 → envcheck 卡片 → 前端展示。端到端责任：P2-T1。
3. **凭据同步链**（新增）：Pi 登录/刷新事件 → wslsetup 同步函数 → distro `~/.pi/agent/auth.json` → WSL 会话启动读 distro 侧凭据。端到端责任：P2-T2。
4. **配置播种链**（P3 重构对象）：wslsetup ensurePiConfig（仅 pi/仅首装/cp -r）→ models.json(models.yml) merge → 冻结。P3 设计覆盖五 CLI。

## 并行与隔离

- P1 单任务（共享文件合并切片）。
- P2-T1 与 P2-T2 文件面基本不相交（P2-T1: aiproxy/platform/envcheck/前端；P2-T2: app.go 闸门区/pi_login_launch/wslsetup/docs），可并行派发，均不与 P1 同批。
- P4-T2/P4-T3 独立，主上同意后可与 P2 并行。
- 不使用 worktree：主树直改（运行中的 CodeBox 实例不受源码改动影响；构建验证走 go test/build，桌面端到端由主上拉取后验收或 Leader 用 codebox CLI 冒烟）。

## 专家 Gate

- P3-T1：fuxi 设计态（无可复用方向且同步时机/MCP 转换策略有重大取舍）——主上已批准。
- diting：P3 落地属高风险大阶段（配置供给体系横跨五 CLI），集成验证后 full 审一次。P1/P2 medium：实现自检+Leader 核对；若 P2-T2 凭据同步涉安全面，Gate 时升级 #qreview。

## 风险、决策与修订日志

- 2026-10-16 建立。
- 发现：git status 在 /mnt/x（DrvFS）超时 >90s → 所有 git 操作后台化+长超时预算（DrvFS I/O 摩擦活例证，佐证 A4/C1）。
- 决策：C5 并入批次二（批次一会重写那些文案，避免无效劳动）。
- 决策：P1 合并为单 luban 任务（platform/wsl.go 与 launcher/wsl_agent_config.go 被 T4/T6 共享）。
- 决策：B7 关闭（主上裁决），证据保留不派工。
- **2026-10-16 B4 真机定谳**（P4-T1 提前完成）：PowerShell P/Invoke CreateProcessW 直测——`/home/maorun`、`~/projects`、`/mnt/x/WorkSpace` 作 lpCurrentDirectory 全部返回 err=267 (ERROR_DIRECTORY)，对照 `X:\WorkSpace` 成功。**B4 是真实必现 bug**：按文档推荐填 Linux 路径的 WSL 会话启动即失败且报错不可关联。修复方案（小）：`internal/pty/service.go` 对 Linux 形态 workDir 跳过 `ConPtyWorkDir`，`wsl.exe --cd` 独自承担工作目录（其本身兼容两种形态）。原定 P4-T1 的「实证」环节完成，「适配」环节建议提前并入 P2 批次（同属 pty 层小改）——待主上确认或按原计划 P4。
- 发现（工具面）：write 工具的 POSIX 绝对路径 WSL 重定向与本会话 bash 的 /tmp 不一致（写报成功但文件不存在）——会话内跨 Windows/WSL 传文件一律走 /mnt/<盘> 相对路径，纯 WSL 内临时文件用 bash heredoc。
